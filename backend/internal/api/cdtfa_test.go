//go:build integration

// California's real published rates, and what the till does with them.
//
// us_tax_test.go proves the SHAPE of American sales tax against fixture
// authorities. This file is the other half: the actual figures the California
// Department of Tax and Fee Administration publishes, checked against the data
// 0118 ships and against what the product charges.
//
// Every rate asserted here is transcribed from the authority's own file —
// "California City & County Sales & Use Tax Rates" effective 1 July 2026,
// SalesTaxRates07-01-26.xlsx, linked from
// https://www.cdtfa.ca.gov/taxes-and-fees/sales-use-tax-rates.htm and
// downloaded 2026-09-04. Nothing in this file is a rate somebody invented to
// make a test pass.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// californiaStateShare is the statewide rate, seeded by 0109 and unchanged by
// the 2026-07-01 schedule. CDTFA: "The statewide tax rate is 7.25%."
const californiaStateShare = "0.0725"

// published is what CDTFA prints for each of these locations: the COMBINED
// rate, statewide share included. The product stores each location's own share
// instead, so the two are related by the statewide rate above.
//
// A deliberate spread: a small district (Adelanto), the highest in the state
// (Santa Fe Springs), a county and a city of the same name that levy
// differently (Alameda 10.25% vs 10.75%), an accented name, a county that
// levies nothing at all (Alpine), and one of the five counties CDTFA publishes
// no county-wide figure for (Kern, present only as its unincorporated area and
// its cities).
var published = []struct {
	level, code, name, combined string
}{
	{"city", "CA-ADELANTO", "Adelanto", "0.0775"},
	{"city", "CA-ALAMEDA", "Alameda", "0.1075"},
	{"county", "CA-ALAMEDA-COUNTY", "Alameda County", "0.1025"},
	{"county", "CA-ALPINE-COUNTY", "Alpine County", "0.0725"},
	{"city", "CA-BAKERSFIELD", "Bakersfield", "0.0825"},
	{"county", "CA-KERN-COUNTY-UNINCORPORATED-AREA",
		"Kern County Unincorporated Area", "0.0825"},
	{"city", "CA-LA-CANADA-FLINTRIDGE", "La Cañada Flintridge", "0.105"},
	{"city", "CA-LOS-ANGELES", "Los Angeles", "0.0975"},
	{"city", "CA-SANTA-FE-SPRINGS", "Santa Fe Springs", "0.11"},
}

type shippedRate struct {
	rate     decimal.Decimal
	name     string
	verified bool
	from     string
	parent   uuid.UUID
	found    bool
}

// shippedCA reads what 0118 put on file for one location.
func shippedCA(t *testing.T, h *harness, level, code string) shippedRate {
	t.Helper()
	var out shippedRate
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		var rate string
		e := tx.QueryRow(t.Context(), `
			SELECT r.rate::text, j.name, r.verified_on IS NOT NULL,
			       r.effective_from::text, j.parent_id
			FROM tax_jurisdiction_rate r
			JOIN tax_jurisdiction j ON j.id = r.jurisdiction_id
			WHERE j.country = 'us' AND j.level = $1 AND j.code = $2
			  AND r.treatment = 'taxable'
			ORDER BY r.effective_from DESC
			LIMIT 1`, level, code).
			Scan(&rate, &out.name, &out.verified, &out.from, &out.parent)
		if e == pgx.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		d, e := decimal.NewFromString(rate)
		if e != nil {
			return e
		}
		out.rate, out.found = d, true
		return nil
	}); err != nil {
		t.Fatalf("read the shipped rate for %s: %v", code, err)
	}
	return out
}

func californiaStateID(t *testing.T, h *harness) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT id FROM tax_jurisdiction
			WHERE country = 'us' AND level = 'state' AND code = 'CA'`).Scan(&id)
	}); err != nil {
		t.Fatalf("read California: %v", err)
	}
	return id
}

// --- the shipped data -----------------------------------------------------

// Every shipped share, added to the statewide rate, is what CDTFA published.
//
// This is the whole correctness claim of 0118. CDTFA prints combined rates;
// the product sums a chain. If the conversion were wrong in either direction —
// storing the combined figure, or subtracting the wrong base — every American
// sale in the state would be wrong, and it would be wrong quietly.
func TestTheShippedCaliforniaRatesAddUpToWhatCDTFAPublishes(t *testing.T) {
	h := newHarness(t)
	state := decimal.RequireFromString(californiaStateShare)
	ca := californiaStateID(t, h)

	for _, want := range published {
		got := shippedCA(t, h, want.level, want.code)
		if !got.found {
			t.Errorf("%s (%s) is not on file; CDTFA publishes %s for it",
				want.name, want.code, want.combined)
			continue
		}
		combined := state.Add(got.rate)
		if !combined.Equal(decimal.RequireFromString(want.combined)) {
			t.Errorf("%s: the chain charges %s, CDTFA publishes %s "+
				"(statewide %s + local %s)",
				want.name, combined, want.combined, state, got.rate)
		}
		if got.name != want.name {
			t.Errorf("%s is filed as %q, CDTFA calls it %q",
				want.code, got.name, want.name)
		}
		// Hung off the state, never off its county: CDTFA's city figure
		// already contains any county district, so a city nested under its
		// county would charge that district twice.
		if got.parent != ca {
			t.Errorf("%s hangs off %s, not California — its county's "+
				"district would be counted twice", want.code, got.parent)
		}
		if got.from != "2026-07-01" {
			t.Errorf("%s takes effect %s, want 2026-07-01 — the date on "+
				"CDTFA's file", want.code, got.from)
		}
	}
}

// The shipped schedule is not marked verified, so it cannot yet be charged.
//
// The figures are the authority's own, but the conversion from combined rates
// to shares is this product's arithmetic and nobody has put their name to it.
// An unverified rate refuses the sale; a plausible one would undercharge.
func TestTheShippedCaliforniaScheduleIsNotMarkedVerified(t *testing.T) {
	h := newHarness(t)
	for _, want := range published {
		if got := shippedCA(t, h, want.level, want.code); got.found &&
			got.verified {
			t.Errorf("%s is marked verified; nobody has checked the "+
				"conversion against CDTFA's page", want.name)
		}
	}
}

// The five counties CDTFA publishes no rate for are absent, not guessed.
//
// Del Norte, Kern, Monterey, Santa Cruz and Yuba each carry an empty Rate cell
// and a note directing the reader to the city or the unincorporated area. A
// county-wide figure invented for them — or a zero, which reads as "levies
// nothing" — would undercharge every sale in them.
func TestTheCountiesWithNoPublishedRateAreNotInvented(t *testing.T) {
	h := newHarness(t)
	for _, county := range []string{"DEL-NORTE", "KERN", "MONTEREY",
		"SANTA-CRUZ", "YUBA"} {
		if got := shippedCA(t, h, "county", "CA-"+county+"-COUNTY"); got.found {
			t.Errorf("CA-%s-COUNTY is on file at %s; CDTFA publishes no "+
				"county-wide rate for it", county, got.rate)
		}
		// What CDTFA does publish for it: the unincorporated area.
		if got := shippedCA(t, h, "county",
			"CA-"+county+"-COUNTY-UNINCORPORATED-AREA"); !got.found {
			t.Errorf("CA-%s-COUNTY-UNINCORPORATED-AREA is missing, so a shop "+
				"outside that county's cities has nowhere to be", county)
		}
	}
}

// A county that levies nothing says so, rather than being left out.
//
// Alpine County's combined rate is exactly the statewide 7.25%, so its own
// share is zero. Absence and zero are different facts: the resolver refuses a
// chain with an unanswered authority, so leaving Alpine out would block every
// sale in it.
func TestACaliforniaCountyThatLeviesNothingIsRecordedAsZero(t *testing.T) {
	h := newHarness(t)
	got := shippedCA(t, h, "county", "CA-ALPINE-COUNTY")
	if !got.found {
		t.Fatal("Alpine County is not on file; a sale there would be " +
			"refused for an authority that in fact levies nothing")
	}
	if !got.rate.IsZero() {
		t.Errorf("Alpine County levies %s; CDTFA publishes 7.25%% combined, "+
			"which is the statewide rate alone", got.rate)
	}
}

// --- what the till charges ------------------------------------------------

// Given CDTFA's real figures, the till charges CDTFA's published rate.
//
// The jurisdictions here are test-local so this cannot disturb the shipped
// schedule, but the NUMBERS are California's: 7.25% statewide plus the share
// CDTFA's file implies for each location. The assertion is the combined rate
// the authority prints, on a round hundred dollars.
func TestACaliforniaSaleIsTaxedAtTheRateCDTFAPublishes(t *testing.T) {
	cases := []struct {
		name, local, tax, tender string
	}{
		{"Adelanto", "0.005", "7.75", "107.75"},
		{"Alameda", "0.035", "10.75", "110.75"},
		{"Alameda County", "0.03", "10.25", "110.25"},
		{"Alpine County", "0", "7.25", "107.25"},
		{"Bakersfield", "0.01", "8.25", "108.25"},
		{"La Cañada Flintridge", "0.0325", "10.50", "110.50"},
		{"Los Angeles", "0.025", "9.75", "109.75"},
		{"Santa Fe Springs", "0.0375", "11.00", "111.00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			chain := usJurisdiction(t, h, map[string]string{
				"state": californiaStateShare, "city": c.local,
			}, true)
			f := usShop(t, h, &chain.city)

			got := taxOf(t, h, f,
				usSaleAt(f, "100.00", "2026-08-15T10:30:00Z", c.tender))
			if !decimal.RequireFromString(got).
				Equal(decimal.RequireFromString(c.tax)) {
				t.Errorf("tax on $100 in %s = %s, CDTFA publishes a combined "+
					"rate that makes it %s", c.name, got, c.tax)
			}
		})
	}
}

// An invoice already issued is not rewritten by next quarter's schedule.
//
// CDTFA republishes every quarter. A sale is taxed at the rate in force on the
// day it was made, and the return that was filed for it has to stay filed —
// so the invoice keeps the figures it was issued with, even when a later
// schedule covers its date.
func TestALaterScheduleDoesNotChangeAnInvoiceAlreadyIssued(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)
	suffix := upperSuffix(5)

	load := func(local, from string) {
		t.Helper()
		resp := h.do(t, http.MethodPost,
			"/api/v1/platform/jurisdictions/import", admin, map[string]any{
				"country": "us", "effective_from": from,
				// A FIXTURE authority, not CDTFA: these are invented codes
				// with invented rates, and labelling them with a real
				// authority would put them among the shipped rates that
				// TestUnverifiedRulesAreNotDisguised checks.
				"source_authority": "test",
				"source_document":  "Schedule effective " + from,
				"verified":         true,
				"rows": []map[string]any{
					{"level": "country", "code": "C" + suffix,
						"name": "Testland", "rate": "0"},
					{"level": "state", "code": "S" + suffix, "name": "State",
						"parent_code": "C" + suffix,
						"rate":        californiaStateShare},
					{"level": "city", "code": "T" + suffix, "name": "City",
						"parent_code": "S" + suffix, "rate": local},
				},
			})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("import %s: %d %s", from, resp.StatusCode,
				readBody(t, resp))
		}
	}

	// 10.75% — Alameda's published rate.
	load("0.035", "2020-01-01")
	city := jurisdictionByCode(t, h, admin, "T"+suffix)
	f := usShop(t, h, &city)

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		usSaleAt(f, "100.00", "2026-08-15T10:30:00Z", "110.75"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", resp.StatusCode, readBody(t, resp))
	}
	invoiceID, _ := decodeJSON(t, resp)["invoice_id"].(string)
	resp.Body.Close()

	// A later schedule that COVERS the sale's date. If anything downstream
	// re-resolved the rate instead of reading what was stored, the invoice
	// would move to 11.75.
	load("0.045", "2026-08-01")

	var tax, share string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(),
			`SELECT tax_total::text FROM sales_invoice WHERE id = $1`,
			invoiceID).Scan(&tax); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			SELECT rate::text FROM sales_invoice_tax_share
			WHERE invoice_id = $1 AND code = $2`,
			invoiceID, "T"+suffix).Scan(&share)
	}); err != nil {
		t.Fatalf("re-read the invoice: %v", err)
	}

	if !decimal.RequireFromString(tax).Equal(decimal.RequireFromString("10.75")) {
		t.Errorf("the invoice now says %s tax; it was issued at 10.75 and a "+
			"return has been filed on it", tax)
	}
	if !decimal.RequireFromString(share).
		Equal(decimal.RequireFromString("0.035")) {
		t.Errorf("the city's recorded share moved to %s; it was 0.035 when "+
			"the sale was made", share)
	}
}
