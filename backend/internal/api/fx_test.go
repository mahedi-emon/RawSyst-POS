//go:build integration

// G2's exchange rates.
//
// Multi-currency was half built: `sales_invoice` carries a currency and an
// `fx_rate`, and `accounting.Entry` translates every journal line into the
// company's base currency. What was missing was the rate itself — every caller
// in the repository passed `decimal.NewFromInt(1)`, so a EUR invoice landed in
// a SAR book at par and nothing objected.
//
// 0113 makes a rate a recorded fact with a date and a source, and makes a
// missing one refuse rather than default to 1. That is the same judgement the
// tax registry makes: a legal or market figure nobody wrote down is not one the
// product may assume.
package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/fx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// putRate records a rate through the route that owns them.
func putRate(
	t *testing.T, h *harness, f *shopFixture, from, to, rate, asOf string,
) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPut, "/api/v1/exchange-rates", f.token,
		map[string]any{
			"from_currency": from, "to_currency": to, "rate": rate,
			"as_of": asOf, "source": "Test Bank morning rate",
		})
}

// A rate can be recorded and read back.
func TestAnExchangeRateCanBeRecordedAndRead(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := putRate(t, h, f, "USD", "SAR", "3.75", "2026-08-01")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("record rate: %d %s", resp.StatusCode, readBody(t, resp))
	}

	list := h.do(t, http.MethodGet, "/api/v1/exchange-rates", f.token, nil)
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list rates: %d", list.StatusCode)
	}
	rows, _ := decodeJSON(t, list)["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the register holds %d rates, want 1", len(rows))
	}
}

// A pair with no rate on file refuses rather than booking at par.
//
// The defect this closes. Treating an unrecorded rate as 1 would put a wrong
// figure in the ledger with nothing anywhere to indicate it — a EUR invoice
// worth 4,000 riyals recorded as 1,000.
func TestAMissingExchangeRateRefusesRatherThanAssumingPar(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	svc := fx.New(h.pool)

	_, err := svc.RateOn(t.Context(), nil, f.tenantID, "EUR", "SAR",
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("a currency pair with no rate on file was converted anyway")
	}
	if errs.CodeOf(err) != errs.CodeUnverifiedRule {
		t.Errorf("code = %v, want %v: %v", errs.CodeOf(err),
			errs.CodeUnverifiedRule, err)
	}
}

// A currency against itself is one, without consulting anything.
func TestACurrencyAgainstItselfIsOne(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	svc := fx.New(h.pool)

	got, err := svc.RateOn(t.Context(), nil, f.tenantID, "SAR", "SAR",
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("same-currency conversion: %v", err)
	}
	if !got.Equal(decimal.NewFromInt(1)) {
		t.Errorf("SAR to SAR = %s, want 1", got)
	}
}

// A rate applies from its day, and the rate in force is the latest one before.
//
// An invoice is translated at the rate that applied when it was ISSUED and
// keeps it. Resolving at today's rate would make a reprint disagree with the
// original and restate closed periods whenever somebody entered a new rate.
func TestTheRateInForceIsTheLatestOneNotAfterTheDate(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	svc := fx.New(h.pool)

	putRate(t, h, f, "USD", "SAR", "3.70", "2026-08-01").Body.Close()
	putRate(t, h, f, "USD", "SAR", "3.80", "2026-08-20").Body.Close()

	on := func(day string) decimal.Decimal {
		d, _ := time.Parse("2006-01-02", day)
		got, err := svc.RateOn(t.Context(), nil, f.tenantID, "USD", "SAR", d)
		if err != nil {
			t.Fatalf("resolve %s: %v", day, err)
		}
		return got
	}

	if got := on("2026-08-10"); !got.Equal(decimal.RequireFromString("3.70")) {
		t.Errorf("the 10th resolved %s, want 3.70 — the rate in force then", got)
	}
	if got := on("2026-08-25"); !got.Equal(decimal.RequireFromString("3.80")) {
		t.Errorf("the 25th resolved %s, want 3.80 — the later rate", got)
	}
	// Before any rate exists, there is no rate; it does not reach back.
	d, _ := time.Parse("2006-01-02", "2026-07-01")
	if _, err := svc.RateOn(t.Context(), nil, f.tenantID, "USD", "SAR", d); err == nil {
		t.Error("a date before the first recorded rate resolved anyway")
	}
}

// Recording the same day twice corrects the rate rather than adding a second.
//
// Two rates for one pair on one day would leave the resolver choosing, and a
// bookkeeper fixing a typo means to replace the figure, not to add an opinion.
func TestRecordingARateTwiceForOneDayCorrectsIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	svc := fx.New(h.pool)

	putRate(t, h, f, "USD", "SAR", "3.70", "2026-08-01").Body.Close()
	putRate(t, h, f, "USD", "SAR", "3.75", "2026-08-01").Body.Close()

	var rows int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM exchange_rate WHERE tenant_id = $1`,
			f.tenantID).Scan(&rows)
	}); err != nil {
		t.Fatalf("count rates: %v", err)
	}
	if rows != 1 {
		t.Errorf("the register holds %d rows for one pair on one day, want 1",
			rows)
	}

	d, _ := time.Parse("2006-01-02", "2026-08-01")
	got, err := svc.RateOn(t.Context(), nil, f.tenantID, "USD", "SAR", d)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !got.Equal(decimal.RequireFromString("3.75")) {
		t.Errorf("rate = %s, want the corrected 3.75", got)
	}
}

// The inverse of a recorded pair is derived rather than demanded twice.
//
// A shop that said one dollar buys 3.75 riyals has said what a riyal buys in
// dollars. Asking them to enter both directions would invite the two to drift
// apart, and a book whose USD->SAR and SAR->USD disagree does not balance.
func TestTheInverseOfARecordedPairIsDerived(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	svc := fx.New(h.pool)

	putRate(t, h, f, "USD", "SAR", "4.00", "2026-08-01").Body.Close()

	d, _ := time.Parse("2006-01-02", "2026-08-15")
	got, err := svc.RateOn(t.Context(), nil, f.tenantID, "SAR", "USD", d)
	if err != nil {
		t.Fatalf("resolve the inverse: %v", err)
	}
	if !got.Equal(decimal.RequireFromString("0.25")) {
		t.Errorf("SAR to USD = %s, want 0.25 — the inverse of 4.00", got)
	}
}

// A rate must say where it came from.
//
// Which feed a business books at is its own decision and this product does not
// pick one, but it can insist that whoever entered a figure said which.
func TestARateMustNameItsSource(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut, "/api/v1/exchange-rates", f.token,
		map[string]any{
			"from_currency": "USD", "to_currency": "SAR", "rate": "3.75",
			"as_of": "2026-08-01", "source": "",
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a rate was recorded with no source")
	}
}

// A nonsense rate is refused.
func TestANonPositiveRateIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	for _, bad := range []string{"0", "-3.75"} {
		resp := putRate(t, h, f, "USD", "SAR", bad, "2026-08-01")
		if resp.StatusCode == http.StatusOK {
			t.Errorf("a rate of %s was accepted", bad)
		}
		resp.Body.Close()
	}
}

// One tenant's rates are not another's.
//
// Two businesses closing the same month may legitimately book at different
// rates from different banks, so these are tenant data and must not mix.
func TestExchangeRatesDoNotCrossTenants(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")
	svc := fx.New(h.pool)

	putRate(t, h, mine, "USD", "SAR", "3.75", "2026-08-01").Body.Close()

	d, _ := time.Parse("2006-01-02", "2026-08-15")
	if _, err := svc.RateOn(t.Context(), nil, theirs.tenantID, "USD", "SAR",
		d); err == nil {
		t.Error("one tenant's exchange rate resolved for another")
	}
}

// Entering a rate takes more than reading one.
func TestOnlySomebodyWhoKeepsTheBooksMaySetARate(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := putRate(t, h, f, "USD", "SAR", "3.75", "2026-08-01")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a cashier set the company's exchange rate")
	}
}
