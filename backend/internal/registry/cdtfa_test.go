//go:build integration

// The shipped California schedule, against the gate that guards it.
//
// jurisdiction_test.go proves the resolver's rules with fixture chains. This
// file points the same resolver at the rows 0118 actually ships — CDTFA's own
// published figures — and asks the two questions that matter in production:
// does an unverified schedule refuse, and once verified does it charge exactly
// what the authority prints?
//
// Both run inside a transaction that is rolled back, so nothing here changes
// the shipped data that internal/api asserts is unverified.
package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// alamedaID is the real jurisdiction 0118 ships for the city of Alameda, whose
// combined rate CDTFA publishes as 10.75%.
func alamedaID(t *testing.T, s *Service) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.pool.TxAsPlatform(context.Background(),
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT id FROM tax_jurisdiction
				WHERE country = 'us' AND level = 'city'
				  AND code = 'CA-ALAMEDA'`).Scan(&id)
		}); err != nil {
		t.Fatalf("read the shipped Alameda row: %v", err)
	}
	return id
}

// saleDate is after the 2026-07-01 schedule takes effect.
var saleDate = time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

// The shipped schedule prices a Californian sale, because it is active.
//
// 0120 required two named people before any imported rate could be charged, and
// that left 541 lawfully published locations unusable — an internal control
// dressed up as a CDTFA requirement, which it never was. 0124 separates the two
// ideas: ACTIVATION is what software can honestly assert about a published rate
// (it names its authority, its document and its page, and its jurisdiction
// resolves to a country), and VERIFICATION stays as the stronger, optional
// record that a person checked the figure by hand.
//
// So this asserts the market works, and the test below asserts the protection
// that actually mattered still does.
func TestTheShippedCaliforniaScheduleIsActiveAndPricesASale(t *testing.T) {
	s := newRegistry(t)
	s.requireVerified = true

	got, err := s.JurisdictionRate(context.Background(), nil,
		alamedaID(t, s), "taxable", saleDate)
	if err != nil {
		t.Fatalf("a Californian sale could not be priced: %v", err)
	}
	if want := decimal.RequireFromString("0.1075"); !got.Total.Equal(want) {
		t.Errorf("Alameda is charged %s; CDTFA publishes %s", got.Total, want)
	}
}

// An unactivated rate still refuses, which is the protection that mattered.
//
// Loosening the gate from "verified" to "activated" must not loosen it to
// "anything on file". A rate nobody has validated cannot price a sale, and this
// stages one inside a transaction that is rolled back to prove it.
func TestAnUnactivatedRateStillRefusesToPriceASale(t *testing.T) {
	s := newRegistry(t)
	s.requireVerified = true
	ctx := context.Background()

	rollback := errors.New("rollback: this test must not deactivate anything")
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `
			UPDATE tax_jurisdiction_rate r
			SET activated_on = NULL, activation_note = NULL
			FROM tax_jurisdiction j
			WHERE j.id = r.jurisdiction_id
			  AND j.country = 'us' AND j.level = 'city'
			  AND j.code = 'CA-ALAMEDA'`); e != nil {
			return e
		}

		_, e := s.JurisdictionRate(ctx, tx, alamedaID(t, s), "taxable", saleDate)
		if e == nil {
			t.Error("a rate nobody validated was used to price a sale")
		} else if errs.CodeOf(e) != errs.CodeUnverifiedRule {
			t.Errorf("code = %v, want %v: %v", errs.CodeOf(e),
				errs.CodeUnverifiedRule, e)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("stage an unactivated rate: %v", err)
	}
}

// Once verified, the shipped chain charges exactly what CDTFA publishes.
//
// The verification happens inside a transaction that is rolled back, so this
// asserts against the real seeded rows without marking anything verified for
// anybody else. 7.25% statewide plus Alameda's 3.5% share is the 10.75% CDTFA
// prints for the city.
func TestTheShippedCaliforniaScheduleChargesCDTFAsRateOnceVerified(t *testing.T) {
	s := newRegistry(t)
	s.requireVerified = true
	ctx := context.Background()
	alameda := alamedaID(t, s)

	rollback := errors.New("rollback: this test must not verify anything")
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var verifier uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT id FROM app_user LIMIT 1`).Scan(&verifier); e != nil {
			return e
		}
		// Verify every authority above the shop, exactly as an operator would
		// after checking the conversion against CDTFA's page.
		if _, e := tx.Exec(ctx, `
			WITH RECURSIVE chain AS (
			  SELECT id, parent_id FROM tax_jurisdiction WHERE id = $1
			  UNION ALL
			  SELECT j.id, j.parent_id FROM tax_jurisdiction j
			  JOIN chain c ON c.parent_id = j.id
			)
			UPDATE tax_jurisdiction_rate r
			SET verified_on = DATE '2026-07-01', verified_by = $2
			FROM chain
			WHERE r.jurisdiction_id = chain.id AND r.treatment = 'taxable'`,
			alameda, verifier); e != nil {
			return e
		}

		got, e := s.JurisdictionRate(ctx, tx, alameda, "taxable", saleDate)
		if e != nil {
			return e
		}
		if want := decimal.RequireFromString("0.1075"); !got.Total.Equal(want) {
			t.Errorf("the till would charge %s in Alameda; CDTFA publishes "+
				"%s", got.Total, want)
		}
		// Each authority's share has to be nameable on its own, because that
		// is what a return is filed against.
		levels := map[string]decimal.Decimal{}
		for _, sh := range got.Shares {
			levels[sh.Level] = sh.Rate
		}
		if r, ok := levels["state"]; !ok ||
			!r.Equal(decimal.RequireFromString("0.0725")) {
			t.Errorf("California's share is %v, want 0.0725", levels["state"])
		}
		if r, ok := levels["city"]; !ok ||
			!r.Equal(decimal.RequireFromString("0.035")) {
			t.Errorf("Alameda's share is %v, want 0.035 — 10.75%% published "+
				"less the statewide 7.25%%", levels["city"])
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("resolve the verified California chain: %v", err)
	}
}
