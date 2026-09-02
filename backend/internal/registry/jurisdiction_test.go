//go:build integration

package registry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// jurisdictions builds a country -> state -> city chain and returns the city.
//
// Rates here are FIXTURE values with fictional authorities, not claims about
// any real place. Nothing in this file is seeded into the product, and 0106
// deliberately ships with no rates at all.
func (s *Service) fixtureChain(
	t *testing.T, verified bool, rates map[string]string,
) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	var cityID uuid.UUID
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var countryID, stateID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO tax_jurisdiction (country, level, code, name)
			VALUES ('us','country',$1,'Testland') RETURNING id`,
			"C"+suffix).Scan(&countryID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO tax_jurisdiction (parent_id, country, level, code, name)
			VALUES ($1,'us','state',$2,'Test State') RETURNING id`,
			countryID, "S"+suffix).Scan(&stateID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO tax_jurisdiction (parent_id, country, level, code, name)
			VALUES ($1,'us','city',$2,'Test City') RETURNING id`,
			stateID, "T"+suffix).Scan(&cityID); e != nil {
			return e
		}

		by := map[string]uuid.UUID{"state": stateID, "city": cityID}
		for level, rate := range rates {
			verifiedOn := "NULL"
			if verified {
				verifiedOn = "'2026-01-01'"
			}
			// verified_by must be present exactly when verified_on is, and a
			// real user id is not needed for the coherence constraint.
			var verifier any
			if verified {
				var uid uuid.UUID
				if e := tx.QueryRow(ctx,
					`SELECT id FROM app_user LIMIT 1`).Scan(&uid); e != nil {
					return e
				}
				verifier = uid
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO tax_jurisdiction_rate
				  (jurisdiction_id, treatment, rate, effective_from,
				   source_authority, source_document, verified_on, verified_by)
				VALUES ($1,'taxable',$2,'2020-01-01','test','fixture',
				        `+verifiedOn+`::date, $3)`,
				by[level], rate, verifier); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("build jurisdiction fixture: %v", err)
	}

	t.Cleanup(func() {
		_ = s.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(), `
				DELETE FROM tax_jurisdiction_rate
				WHERE jurisdiction_id IN (
				  SELECT id FROM tax_jurisdiction WHERE code LIKE $1)`,
				"%"+suffix)
			if e != nil {
				return e
			}
			// Children first: parent_id is ON DELETE RESTRICT on purpose.
			for _, lvl := range []string{"city", "state", "country"} {
				if _, e := tx.Exec(context.Background(),
					`DELETE FROM tax_jurisdiction WHERE level = $1 AND code LIKE $2`,
					lvl, "%"+suffix); e != nil {
					return e
				}
			}
			return nil
		})
	})
	return cityID
}

// A sale in a city is taxed by the city AND every authority above it.
//
// This is the whole reason a national decimal cannot express US sales tax: two
// authorities levy on the same sale, and the customer pays the sum.
func TestACombinedRateSumsEveryAuthorityAboveTheSale(t *testing.T) {
	s := newRegistry(t)
	city := s.fixtureChain(t, true, map[string]string{
		"state": "0.0625",
		"city":  "0.0200",
	})

	got, err := s.JurisdictionRate(context.Background(), nil, city, "taxable",
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if !got.Total.Equal(decimal.RequireFromString("0.0825")) {
		t.Errorf("combined rate = %s, want 0.0825", got.Total)
	}
	// The parts have to survive: a shop files with each authority separately,
	// and a total that cannot be broken down cannot be filed.
	if len(got.Shares) != 2 {
		t.Fatalf("shares = %d, want 2 (state and city)", len(got.Shares))
	}
	levels := map[string]bool{}
	for _, sh := range got.Shares {
		levels[sh.Level] = true
	}
	if !levels["state"] || !levels["city"] {
		t.Errorf("shares do not name both authorities: %+v", got.Shares)
	}
}

// One unverified share refuses the whole rate.
//
// Not skipped and not treated as zero: a combined rate assembled from a
// verified share and an unverified one is wrong by exactly the unverified
// share, and would be charged to a customer and remitted as if it were right.
func TestAnUnverifiedShareRefusesTheCombinedRate(t *testing.T) {
	s := newRegistry(t)
	s.requireVerified = true

	city := s.fixtureChain(t, false, map[string]string{"state": "0.0625"})

	_, err := s.JurisdictionRate(context.Background(), nil, city, "taxable",
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("an unverified rate was used to price a sale")
	}
	if errs.CodeOf(err) != errs.CodeUnverifiedRule {
		t.Errorf("code = %v, want %v: %v", errs.CodeOf(err),
			errs.CodeUnverifiedRule, err)
	}
}

// A jurisdiction with no rates recorded is refused, not treated as untaxed.
//
// This is the state every US jurisdiction is in today. Returning zero would be
// making a legal claim — that this sale is not taxed — that nobody has made.
func TestAJurisdictionWithNoRatesIsRefusedRatherThanZero(t *testing.T) {
	s := newRegistry(t)
	city := s.fixtureChain(t, true, nil) // the chain exists; no rates on it

	_, err := s.JurisdictionRate(context.Background(), nil, city, "taxable",
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("a jurisdiction with no rates priced a sale at zero tax")
	}
	if errs.CodeOf(err) != errs.CodeUnverifiedRule {
		t.Errorf("code = %v, want %v: %v", errs.CodeOf(err),
			errs.CodeUnverifiedRule, err)
	}
}

// A rate that had not taken effect yet does not apply.
//
// Rates are dated so an invoice issued last year stays explainable by the rules
// in force then. Resolving at the transaction date rather than at today is what
// makes a reprint match the original.
func TestARateIsResolvedAtTheTransactionDateNotToday(t *testing.T) {
	s := newRegistry(t)
	city := s.fixtureChain(t, true, map[string]string{"state": "0.0625"})

	// Before 2020-01-01, when the fixture rate takes effect.
	_, err := s.JurisdictionRate(context.Background(), nil, city, "taxable",
		time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Error("a rate applied to a sale predating its effective date")
	}
}

// The product ships with no jurisdiction rates at all.
//
// 0106 seeds none on purpose: every rate is a legal value and must arrive with
// its source and a verification date. This fails if somebody ever adds a
// plausible-looking rate to a migration, which is the exact mistake that would
// look correct in review.
func TestNoTaxJurisdictionRatesAreSeededWithTheProduct(t *testing.T) {
	s := newRegistry(t)
	ctx := context.Background()

	var seeded int
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM tax_jurisdiction_rate
			WHERE source_authority <> 'test'`).Scan(&seeded)
	})
	if err != nil {
		t.Fatalf("count seeded rates: %v", err)
	}
	if seeded != 0 {
		t.Errorf("%d tax rates ship with the product; every rate is a legal "+
			"value and must be entered with its source, never seeded", seeded)
	}
}
