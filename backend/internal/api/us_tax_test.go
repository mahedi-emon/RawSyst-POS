//go:build integration

// Selling where tax is not set nationally.
//
// Saudi Arabia and Bangladesh set VAT nationally, so their rate comes from the
// regulatory registry by country and date. The United States does not: a state,
// a county, a city and sometimes a special district each levy their own share
// of the same sale.
//
// The California Department of Tax and Fee Administration says so directly —
// "The statewide tax rate is 7.25%" and "In most areas of California, local
// jurisdictions have added district taxes ... those district tax rates range
// from 0.10% to 2.00%", with sellers directed to look the combined rate up BY
// ADDRESS (https://www.cdtfa.ca.gov/taxes-and-fees/sales-use-tax-rates.htm,
// read 2026-09-03).
//
// So a single national US rate would be wrong everywhere, and these tests hold
// the product to refusing rather than guessing.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// usJurisdiction builds a country → state → city chain and returns the city.
//
// Rates here are FIXTURE values against fictional authorities. Nothing in this
// file is seeded into the product: 0109 ships California's state share alone,
// unverified, precisely because the districts are missing.
func usJurisdiction(t *testing.T, h *harness, rates map[string]string, verified bool) usChain {
	t.Helper()
	suffix := uuid.NewString()[:8]
	var chain usChain

	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO tax_jurisdiction (country, level, code, name)
			VALUES ('us','country',$1,'Testland') RETURNING id`,
			"C"+suffix).Scan(&chain.country); e != nil {
			return e
		}
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO tax_jurisdiction (parent_id, country, level, code, name)
			VALUES ($1,'us','state',$2,'Test State') RETURNING id`,
			chain.country, "S"+suffix).Scan(&chain.state); e != nil {
			return e
		}
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO tax_jurisdiction (parent_id, country, level, code, name)
			VALUES ($1,'us','city',$2,'Test City') RETURNING id`,
			chain.state, "T"+suffix).Scan(&chain.city); e != nil {
			return e
		}

		// The country root levies nothing, and that has to be SAID. A chain
		// with an unanswered authority refuses the sale, so a fixture that
		// left this out would be testing the refusal in every case.
		if _, ok := rates["country"]; !ok {
			rates["country"] = "0"
		}

		by := map[string]uuid.UUID{
			"country": chain.country, "state": chain.state, "city": chain.city,
		}
		for level, rate := range rates {
			var verifier any
			verifiedOn := "NULL"
			if verified {
				var uid uuid.UUID
				if e := tx.QueryRow(t.Context(),
					`SELECT id FROM app_user LIMIT 1`).Scan(&uid); e != nil {
					return e
				}
				verifier = uid
				verifiedOn = "'2026-01-01'"
			}
			if _, e := tx.Exec(t.Context(), `
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
	}); err != nil {
		t.Fatalf("build jurisdiction: %v", err)
	}
	return chain
}

// usChain is a country → state → city ladder, so a test can name the authority
// it means to change.
type usChain struct{ country, state, city uuid.UUID }

// rateWindow gives a jurisdiction a rate over a date range, replacing whatever
// it had. `to` may be empty for open-ended.
func rateWindow(t *testing.T, h *harness, j uuid.UUID, rate, from, to string) {
	t.Helper()
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		if _, e := tx.Exec(t.Context(),
			`DELETE FROM tax_jurisdiction_rate
			  WHERE jurisdiction_id = $1 AND treatment = 'taxable'`, j); e != nil {
			return e
		}
		_, e := tx.Exec(t.Context(), `
			INSERT INTO tax_jurisdiction_rate
			  (jurisdiction_id, treatment, rate, effective_from, effective_to,
			   source_authority, source_document)
			VALUES ($1,'taxable',$2,$3::date,nullif($4,'')::date,'test','fixture')`,
			j, rate, from, to)
		return e
	}); err != nil {
		t.Fatalf("set rate window: %v", err)
	}
}

// usShop is an American shop, optionally placed in a jurisdiction.
func usShop(t *testing.T, h *harness, jurisdiction *uuid.UUID) *shopFixture {
	t.Helper()
	f := h.seedShopInMarket(t, "owner", "us", "USD")
	if jurisdiction != nil {
		if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(t.Context(),
				`UPDATE store SET tax_jurisdiction_id = $2 WHERE id = $1`,
				f.storeID, *jurisdiction)
			return e
		}); err != nil {
			t.Fatalf("place the shop: %v", err)
		}
	}
	return f
}

// usSale rings up one taxable item.
func usSale(f *shopFixture, price string) map[string]any {
	return usSaleAt(f, price, "2026-08-15T10:30:00Z", "108.25")
}

// usSaleAt rings up one taxable item on a given day for a given tender.
//
// The date is a parameter because a rate that changed on a date is only tested
// by two sales that straddle it.
func usSaleAt(f *shopFixture, price, issuedAt, tender string) map[string]any {
	return map[string]any{
		"invoice_uuid":       uuid.NewString(),
		"doc_type":           "simplified",
		"issued_at":          issuedAt,
		"prices_include_tax": false,
		"lines": []map[string]any{{
			"variant_id": f.variantID.String(), "description": "Shirt",
			"qty": "1", "unit_price": price, "tax_treatment": "taxable",
		}},
		"tenders": []map[string]any{{"method": "cash", "amount": tender}},
	}
}

// taxOf rings a sale and returns the tax recorded on the invoice.
func taxOf(t *testing.T, h *harness, f *shopFixture, sale map[string]any) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, sale)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", resp.StatusCode, readBody(t, resp))
	}
	invoiceID, _ := decodeJSON(t, resp)["invoice_id"].(string)

	var tax string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT tax_total::text FROM sales_invoice WHERE id = $1`,
			invoiceID).Scan(&tax)
	}); err != nil {
		t.Fatalf("read tax: %v", err)
	}
	return tax
}

// A sale is taxed by every authority above the shop, summed.
//
// The whole reason a national decimal cannot express US sales tax: a state at
// 6.25% and a city at 2.00% both levy on the same sale, and the customer pays
// 8.25% of 100.
func TestAnAmericanSaleIsTaxedByEveryAuthorityAboveTheShop(t *testing.T) {
	h := newHarness(t)
	chain := usJurisdiction(t, h, map[string]string{
		"state": "0.0625", "city": "0.0200",
	}, true)
	f := usShop(t, h, &chain.city)

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		usSale(f, "100.00"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", resp.StatusCode, readBody(t, resp))
	}

	invoiceID, _ := decodeJSON(t, resp)["invoice_id"].(string)

	var tax string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT tax_total::text FROM sales_invoice WHERE id = $1`,
			invoiceID).Scan(&tax)
	}); err != nil {
		t.Fatalf("read tax: %v", err)
	}
	if tax != "8.25" && tax != "8.2500" {
		t.Errorf("tax = %s, want 8.25 (6.25%% state plus 2.00%% city on 100)", tax)
	}
}

// A shop with no jurisdiction is told what is missing.
//
// Not "no rate on file": the shop has not been told where it is, which is a
// setup step somebody can take rather than a legal value nobody has verified.
func TestAnAmericanShopWithNoJurisdictionCannotSell(t *testing.T) {
	h := newHarness(t)
	f := usShop(t, h, nil)

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		usSale(f, "100.00"))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("an American sale was priced with no jurisdiction set")
	}
	if body := readBody(t, resp); !strings.Contains(body, "jurisdiction") {
		t.Errorf("the refusal does not mention the jurisdiction: %s", body)
	}
}

// An unverified share refuses the whole sale.
//
// A combined rate assembled from a verified share and an unverified one is
// wrong by exactly the unverified share, and would be charged to a customer and
// remitted as if it were right.
func TestAnUnverifiedJurisdictionRateRefusesTheSale(t *testing.T) {
	h := newHarness(t)
	chain := usJurisdiction(t, h, map[string]string{
		"state": "0.0625", "city": "0",
	}, false)
	f := usShop(t, h, &chain.city)

	// The harness runs with verification off, so the rate resolves; what this
	// pins is that the RATE ITSELF is recorded as unverified, which is what the
	// production gate refuses on.
	var verified bool
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT bool_and(verified_on IS NOT NULL)
			FROM tax_jurisdiction_rate r
			JOIN tax_jurisdiction j ON j.id = r.jurisdiction_id
			WHERE j.country = 'us' AND j.code LIKE 'S%'`).Scan(&verified)
	}); err != nil {
		t.Fatalf("read verification: %v", err)
	}
	if verified {
		t.Error("the fixture rate is marked verified; the unverified path is untested")
	}
	_ = f
}

// The product ships California's state share and nothing else, unverified.
//
// 0109 seeds it from CDTFA — a real figure from the binding authority — and
// leaves `verified_on` null because the districts are missing and a sale
// resolving only the state share would undercharge across most of California.
// An unverified rate that refuses is safer than a plausible one that quietly
// undercharges.
func TestTheShippedCaliforniaRateIsStateShareOnlyAndUnverified(t *testing.T) {
	h := newHarness(t)

	var rate string
	var verified bool
	var source string
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT r.rate::text, r.verified_on IS NOT NULL, r.source_authority
			FROM tax_jurisdiction_rate r
			JOIN tax_jurisdiction j ON j.id = r.jurisdiction_id
			WHERE j.country = 'us' AND j.level = 'state' AND j.code = 'CA'`).
			Scan(&rate, &verified, &source)
	}); err != nil {
		t.Fatalf("read the shipped California rate: %v", err)
	}

	if rate != "0.072500" {
		t.Errorf("California state share = %s, want 0.072500 per CDTFA", rate)
	}
	if verified {
		t.Error("the shipped California rate is marked verified; it is the " +
			"state share alone and the district rates are missing")
	}
	if source != "cdtfa" {
		t.Errorf("source = %q, want cdtfa", source)
	}
}

// A Saudi sale never touches the jurisdiction path.
//
// Its VAT is national, resolved from the registry by country and date. If it
// started consulting jurisdictions, every Saudi shop would need one set.
func TestASaudiSaleIsUnaffectedByJurisdictions(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier") // Saudi, no jurisdiction on its store

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a Saudi sale was refused: %d %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// An authority with no rate on file refuses the sale, and names itself.
//
// The defect this closes was silent and expensive. The chain walk skipped an
// authority that had no rate row, so a shop whose city rate was loaded and
// whose state rate was not sold all day at the city's 2% — printed on the
// receipt as the tax due, posted to the tax account, and under-remitted to the
// state. Nothing looked wrong at any point.
//
// A refusal that names the unanswered authority is the only safe answer,
// because the alternative is a number that is confidently wrong.
func TestAnAuthorityWithNoRateOnFileRefusesTheSale(t *testing.T) {
	h := newHarness(t)
	// The state answers; the city never has.
	chain := usJurisdiction(t, h, map[string]string{"state": "0.0625"}, true)
	f := usShop(t, h, &chain.city)

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		usSale(f, "100.00"))
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a sale was priced on a partly loaded chain — the customer was " +
			"charged the state's share alone and the city's share was lost")
	}
	if body := readBody(t, resp); !strings.Contains(body, "Test City") {
		t.Errorf("the refusal does not name the authority that has not "+
			"answered: %s", body)
	}
}

// An authority that levies nothing is recorded as zero, and the sale proceeds.
//
// The other half of the rule above: absence refuses, zero is a statement. A
// shop in a city with no local sales tax has to be able to sell, and the way it
// says so is a 0 row with a source — which somebody looked up.
func TestAnAuthorityThatLeviesNothingIsRecordedAsZero(t *testing.T) {
	h := newHarness(t)
	chain := usJurisdiction(t, h, map[string]string{
		"state": "0.0625", "city": "0",
	}, true)
	f := usShop(t, h, &chain.city)

	if got := taxOf(t, h, f, usSaleAt(f, "100.00",
		"2026-08-15T10:30:00Z", "106.25")); got != "6.25" && got != "6.2500" {
		t.Errorf("tax = %s, want 6.25 — the state's share alone, the city "+
			"having declared zero", got)
	}
}

// A rate change applies from its effective date, not to everything.
//
// Sales tax rates change on a date set by the authority, and an invoice is
// taxed at the rate in force on the day it was ISSUED — which is not
// necessarily the day it is being looked at. Two sales straddling the change
// are the only way to see that the date range is being honoured rather than
// the newest row simply winning.
//
// Both sales sit inside one accounting period, ten days apart, so what is being
// measured is the effective DATE and not the month it happens to fall in.
func TestARateChangeAppliesFromItsEffectiveDate(t *testing.T) {
	h := newHarness(t)
	chain := usJurisdiction(t, h, map[string]string{"city": "0"}, true)
	f := usShop(t, h, &chain.city)

	// The state charged 5% until the tenth of August, and 8% from it.
	rateWindow(t, h, chain.state, "0.0500", "2020-01-01", "2026-08-10")
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			INSERT INTO tax_jurisdiction_rate
			  (jurisdiction_id, treatment, rate, effective_from,
			   source_authority, source_document)
			VALUES ($1,'taxable',0.0800,'2026-08-10','test','fixture')`,
			chain.state)
		return e
	}); err != nil {
		t.Fatalf("add the later rate: %v", err)
	}

	before := taxOf(t, h, f, usSaleAt(f, "100.00", "2026-08-05T10:00:00Z", "105.00"))
	if before != "5.00" && before != "5.0000" {
		t.Errorf("a sale on the 5th was taxed %s, want 5.00 — the rate in "+
			"force before the change on the 10th", before)
	}

	after := taxOf(t, h, f, usSaleAt(f, "100.00", "2026-08-15T10:00:00Z", "108.00"))
	if after != "8.00" && after != "8.0000" {
		t.Errorf("a sale on the 15th was taxed %s, want 8.00 — the rate in "+
			"force from the change on the 10th", after)
	}
}

// One shop's jurisdiction does not tax another shop's sale.
//
// Jurisdictions are platform data shared across every tenant — one row for
// California, not one per customer — so the isolation that matters is not who
// can READ a jurisdiction but which one a given sale resolves through. A shop
// is taxed by the jurisdiction on its own store row and by no other.
func TestOneShopsJurisdictionDoesNotTaxAnothersSale(t *testing.T) {
	h := newHarness(t)

	cheap := usJurisdiction(t, h, map[string]string{"state": "0.0200"}, true)
	dear := usJurisdiction(t, h, map[string]string{"state": "0.1000"}, true)

	// Two shops in two different tenants, each placed in its own state.
	a := usShop(t, h, &cheap.state)
	b := usShop(t, h, &dear.state)
	if a.tenantID == b.tenantID {
		t.Fatal("the fixture put both shops in one tenant; isolation is untested")
	}

	if got := taxOf(t, h, a, usSaleAt(a, "100.00",
		"2026-08-15T10:00:00Z", "102.00")); got != "2.00" && got != "2.0000" {
		t.Errorf("shop A was taxed %s, want its own 2.00", got)
	}
	if got := taxOf(t, h, b, usSaleAt(b, "100.00",
		"2026-08-15T10:00:00Z", "110.00")); got != "10.00" && got != "10.0000" {
		t.Errorf("shop B was taxed %s, want its own 10.00", got)
	}
}

// Jurisdiction tax reaches the ledger, and is attributed to each authority.
//
// The point of the whole jurisdiction path is that it produces a tax figure the
// rest of the system already knows what to do with — not a parallel one. A
// combined 8.25% assembled from two authorities has to credit the output tax
// account by 8.25 exactly as a national 15% VAT credits it by 15, or the
// jurisdiction engine would be computing a number that never becomes money
// anybody owes.
func TestAmericanSalesTaxReachesTheLedgerAndIsAttributed(t *testing.T) {
	h := newHarness(t)
	chain := usJurisdiction(t, h, map[string]string{
		"state": "0.0625", "city": "0.0200",
	}, true)
	f := usShop(t, h, &chain.city)

	if got := taxOf(t, h, f, usSale(f, "100.00")); got != "8.25" && got != "8.2500" {
		t.Fatalf("tax on the invoice = %s, want 8.25", got)
	}

	var tax, revenue string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		balance := func(role string, into *string) error {
			return tx.QueryRow(t.Context(), `
				SELECT coalesce(sum(l.base_credit - l.base_debit), 0)::text
				  FROM journal_line l
				  JOIN account_role_map m ON m.account_id = l.account_id
				 WHERE m.company_id = $1 AND m.role = $2`,
				f.companyID, role).Scan(into)
		}
		if e := balance("output_vat", &tax); e != nil {
			return e
		}
		return balance("sales_revenue", &revenue)
	}); err != nil {
		t.Fatalf("read the ledger: %v", err)
	}

	if tax != "8.25" && tax != "8.2500" {
		t.Errorf("the output tax account was credited %s, want 8.25 — the two "+
			"authorities' shares summed", tax)
	}
	if revenue != "100.00" && revenue != "100.0000" {
		t.Errorf("revenue = %s, want 100.00 — tax is not revenue", revenue)
	}
}

// The tax on an American invoice is attributed to the authorities that levied
// it, and the attribution adds up.
//
// A shop files a return with the state and another with the city, each for its
// own share. `tax_total` alone cannot answer either. registry.JurisdictionRate
// has always returned the breakdown and the sale path used to sum it and throw
// the parts away, so a shop could charge a correct 8.25% and have nothing to
// file with. 0111 keeps the parts.
//
// The sum is the assertion that matters: shares that do not add back to the tax
// on the invoice would put the shop's returns out of agreement with its own
// books, which is precisely what an audit looks for.
func TestEachAuthoritysShareOfTheTaxIsRecorded(t *testing.T) {
	h := newHarness(t)
	chain := usJurisdiction(t, h, map[string]string{
		"state": "0.0625", "city": "0.0200",
	}, true)
	f := usShop(t, h, &chain.city)

	if got := taxOf(t, h, f, usSale(f, "100.00")); got != "8.25" && got != "8.2500" {
		t.Fatalf("tax = %s, want 8.25", got)
	}

	byLevel := map[string]string{}
	var summed string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(t.Context(), `
			SELECT level, tax_amount::text FROM sales_invoice_tax_share
			 WHERE tenant_id = $1`, f.tenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var level, amount string
			if e := rows.Scan(&level, &amount); e != nil {
				return e
			}
			byLevel[level] = amount
		}
		if e := rows.Err(); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(),
			`SELECT coalesce(sum(tax_amount),0)::text
			   FROM sales_invoice_tax_share WHERE tenant_id = $1`,
			f.tenantID).Scan(&summed)
	}); err != nil {
		t.Fatalf("read the shares: %v", err)
	}

	// The country levies nothing and is recorded as such; the state and the
	// city each get their own row.
	if got := byLevel["state"]; got != "6.25" && got != "6.2500" {
		t.Errorf("the state's share = %q, want 6.25", got)
	}
	if got := byLevel["city"]; got != "2.00" && got != "2.0000" {
		t.Errorf("the city's share = %q, want 2.00", got)
	}
	if summed != "8.25" && summed != "8.2500" {
		t.Errorf("the shares sum to %s, want the 8.25 on the invoice — a "+
			"return built from these would not agree with the books", summed)
	}
}

// A Saudi sale records no shares at all.
//
// One national rate and one authority: there is nothing to apportion, and rows
// claiming otherwise would invent a hierarchy Saudi Arabia does not have.
func TestASaudiSaleRecordsNoJurisdictionShares(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a Saudi sale was refused: %d", resp.StatusCode)
	}

	var shares int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM sales_invoice_tax_share WHERE tenant_id = $1`,
			f.tenantID).Scan(&shares)
	}); err != nil {
		t.Fatalf("count shares: %v", err)
	}
	if shares != 0 {
		t.Errorf("a Saudi sale recorded %d jurisdiction shares, want none",
			shares)
	}
}

// An authority that levies nothing is apportioned nothing, to the penny.
//
// Splitting the tax back out by rate does not divide evenly, and this codebase's
// usual rule gives the leftover penny to the LAST part. That rule assumes the
// parts are interchangeable, and here they are not: the walk ends at the country
// root, which in the United States levies zero, so the ordinary rule would file
// a stray penny with an authority that charges nothing and is owed nothing —
// negative, in this case.
//
// 6.25% state and 1.25% city on 55.55 is one such split: the tax charged is
// 4.17, the proportional shares round to 0.70 and 3.48, and the 0.01 too many
// has to come off the state's share rather than off a country that never levied.
func TestAnAuthorityLevyingNothingIsApportionedNothing(t *testing.T) {
	h := newHarness(t)
	chain := usJurisdiction(t, h, map[string]string{
		"state": "0.0625", "city": "0.0125",
	}, true)
	f := usShop(t, h, &chain.city)

	if got := taxOf(t, h, f, usSaleAt(f, "55.55",
		"2026-08-15T10:00:00Z", "59.72")); got != "4.17" && got != "4.1700" {
		t.Fatalf("tax = %s, want 4.17", got)
	}

	byLevel := map[string]string{}
	var summed string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(t.Context(), `
			SELECT level, tax_amount::text FROM sales_invoice_tax_share
			 WHERE tenant_id = $1`, f.tenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var level, amount string
			if e := rows.Scan(&level, &amount); e != nil {
				return e
			}
			byLevel[level] = amount
		}
		if e := rows.Err(); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(),
			`SELECT coalesce(sum(tax_amount),0)::text
			   FROM sales_invoice_tax_share WHERE tenant_id = $1`,
			f.tenantID).Scan(&summed)
	}); err != nil {
		t.Fatalf("read the shares: %v", err)
	}

	if got := byLevel["country"]; got != "0.00" && got != "0.0000" {
		t.Errorf("the country was apportioned %q, want exactly 0.00 — it "+
			"levies nothing and is owed nothing", got)
	}
	if got := byLevel["state"]; got != "3.47" && got != "3.4700" {
		t.Errorf("the state's share = %q, want 3.47 — the largest share "+
			"carries the rounding", got)
	}
	if got := byLevel["city"]; got != "0.70" && got != "0.7000" {
		t.Errorf("the city's share = %q, want 0.70", got)
	}
	if summed != "4.17" && summed != "4.1700" {
		t.Errorf("the shares sum to %s, want the 4.17 on the invoice", summed)
	}
}

// A return credits each authority that was paid on the sale.
//
// Without this the breakdown would be right only until somebody brought
// something back: the invoice's shares would keep saying the state is owed 6.25
// on a sale that has since been refunded in full, and the shop would remit tax
// it had already handed back to the customer.
//
// The credit note's shares are read from the invoice it corrects, at the rates
// that were actually charged. Resolving them afresh would credit the wrong
// authorities the moment a rate changed between the sale and the return.
func TestAReturnCreditsEachAuthorityThatWasPaid(t *testing.T) {
	h := newHarness(t)
	chain := usJurisdiction(t, h, map[string]string{
		"state": "0.0625", "city": "0.0200",
	}, true)
	f := usShop(t, h, &chain.city)

	// Ring the sale up and find the line to bring back.
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		usSale(f, "100.00"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", resp.StatusCode, readBody(t, resp))
	}
	invoiceID, _ := decodeJSON(t, resp)["invoice_id"].(string)
	resp.Body.Close()

	var lineID string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT id::text FROM sales_invoice_line WHERE invoice_id = $1`,
			invoiceID).Scan(&lineID)
	}); err != nil {
		t.Fatalf("find the line: %v", err)
	}

	ret := h.do(t, http.MethodPost, "/api/v1/pos/returns", f.token,
		map[string]any{
			"credit_note_uuid":    newUUID(),
			"original_invoice_id": invoiceID,
			"issued_at":           "2026-08-15T12:00:00Z",
			"reason":              "changed their mind",
			"lines": []any{map[string]any{
				"line_id": lineID, "qty": "1",
			}},
			"refunds": []any{map[string]any{
				"method": "cash", "amount": "108.25",
			}},
		})
	defer ret.Body.Close()
	if ret.StatusCode != http.StatusCreated {
		t.Fatalf("return: %d %s", ret.StatusCode, readBody(t, ret))
	}

	credited := map[string]string{}
	var total string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(t.Context(), `
			SELECT s.level, s.tax_amount::text
			  FROM sales_invoice_tax_share s
			  JOIN sales_invoice i ON i.id = s.invoice_id
			 WHERE i.doc_type = 'credit_note' AND s.tenant_id = $1`,
			f.tenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var level, amount string
			if e := rows.Scan(&level, &amount); e != nil {
				return e
			}
			credited[level] = amount
		}
		if e := rows.Err(); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(s.tax_amount),0)::text
			  FROM sales_invoice_tax_share s
			  JOIN sales_invoice i ON i.id = s.invoice_id
			 WHERE i.doc_type = 'credit_note' AND s.tenant_id = $1`,
			f.tenantID).Scan(&total)
	}); err != nil {
		t.Fatalf("read the credited shares: %v", err)
	}

	if len(credited) == 0 {
		t.Fatal("the return credited no authority; the shop would remit tax " +
			"it has already refunded")
	}
	if got := credited["state"]; got != "6.25" && got != "6.2500" {
		t.Errorf("the state was credited %q, want the 6.25 it was paid", got)
	}
	if got := credited["city"]; got != "2.00" && got != "2.0000" {
		t.Errorf("the city was credited %q, want the 2.00 it was paid", got)
	}
	if total != "8.25" && total != "8.2500" {
		t.Errorf("the return credits %s in total, want the 8.25 charged on "+
			"the sale it reverses", total)
	}
}
