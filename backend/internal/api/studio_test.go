//go:build integration

// The barcode engine and label studio (B3), global search (D7) and analytics
// (D2).
//
// The tests here are about the two things this module could get wrong in a way
// nobody notices until it is physically attached to a garment: a barcode that
// scans as the wrong product, and a price on a tag that disagrees with what the
// till charges.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// --- barcodes -------------------------------------------------------------

// B3's example, exactly: Men / Winter / Black / XL becomes a code a person can
// read off a shelf edge.
func TestAGeneratedBarcodeIsReadable(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	variantID := seedFashionVariant(t, h, f, "SHIRT-BLK-XL", "Black", "XL",
		"Menswear", "Winter")

	setScheme(t, h, f, []string{"category", "season", "colour", "size"}, "code128")

	resp := h.do(t, http.MethodPost, adminPath(f, "/api/v1/labels/barcodes"),
		f.token, map[string]any{"variant_ids": []string{variantID}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generate: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	assigned, _ := decodeJSON(t, resp)["assigned"].([]any)
	if len(assigned) != 1 {
		t.Fatalf("%d barcodes assigned, want 1", len(assigned))
	}
	row, _ := assigned[0].(map[string]any)

	if got, _ := row["barcode"].(string); got != "MEN-WIN-BLA-XL" {
		t.Errorf("the generated code reads %q; B3 asks for a code a person can "+
			"read off a shelf edge, like MEN-WIN-BLA-XL", got)
	}
}

// A code that is already printed on tags in the stockroom does not move
// because somebody ran the generator again.
func TestGeneratingAgainLeavesExistingBarcodesAlone(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	variantID := seedFashionVariant(t, h, f, "SHIRT-RED-M", "Red", "M",
		"Menswear", "Summer")
	setScheme(t, h, f, []string{"category", "colour", "size"}, "code128")

	h.do(t, http.MethodPost, adminPath(f, "/api/v1/labels/barcodes"), f.token,
		map[string]any{"variant_ids": []string{variantID}}).Body.Close()
	first := barcodeOf(t, h, f, variantID)

	// Somebody presses the button again. Nothing should move.
	again := h.do(t, http.MethodPost, adminPath(f, "/api/v1/labels/barcodes"),
		f.token, map[string]any{"variant_ids": []string{variantID}})
	if again.StatusCode != http.StatusOK {
		t.Fatalf("second run: status %d — %s", again.StatusCode, readBody(t, again))
	}
	if got := decodeJSON(t, again)["count"]; got != float64(0) {
		t.Errorf("the second run assigned %v codes; it should assign none", got)
	}
	if got := barcodeOf(t, h, f, variantID); got != first {
		t.Fatalf("the barcode moved from %q to %q. Every hang tag already "+
			"printed now scans as nothing.", first, got)
	}
}

// EAN-13 is thirteen digits with a check digit, and "MEN-WIN-BLA-XL" is not
// one. A scheme that tried to encode the readable string as EAN-13 would
// produce labels no scanner reads.
func TestADigitSymbologyGetsDigits(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	variantID := seedFashionVariant(t, h, f, "SHOE-BLK-42", "Black", "42",
		"Footwear", "Winter")
	setScheme(t, h, f, []string{"category", "colour", "size"}, "ean13")

	h.do(t, http.MethodPost, adminPath(f, "/api/v1/labels/barcodes"), f.token,
		map[string]any{"variant_ids": []string{variantID}}).Body.Close()

	code := barcodeOf(t, h, f, variantID)
	if len(code) != 13 {
		t.Fatalf("an EAN-13 code is %q, which is %d characters", code, len(code))
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("an EAN-13 code contains %q: %s", r, code)
		}
	}
	// The GS1 in-store range, so a shop's own code can never collide with a
	// real manufacturer's on a product they also stock.
	if !strings.HasPrefix(code, "2") {
		t.Errorf("a generated EAN-13 starts %q; in-store codes begin with 2",
			code[:1])
	}
	if !checkDigitHolds(code) {
		t.Errorf("the check digit on %q does not verify, so a scanner will "+
			"refuse the label", code)
	}
}

// --- labels ---------------------------------------------------------------

// The shelf price is the price paid. A tag showing the net amount would
// undercharge every customer who reads it.
func TestALabelPrintsTheVATInclusivePrice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	seedStudio(t, h, f)

	resp := h.do(t, http.MethodPost, adminPath(f, "/api/v1/labels/print"),
		f.token, map[string]any{"kind": "thermal", "copies": 1})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("print: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	sheet := decodeJSON(t, resp)

	labels, _ := sheet["labels"].([]any)
	if len(labels) == 0 {
		t.Fatal("the run produced no labels")
	}
	first, _ := labels[0].(map[string]any)

	// The shop fixture prices its abaya at 115.00, and the till rings it up at
	// 115.00: prices in this product INCLUDE tax, and the 15.00 inside it is
	// derived by division. A tag printing 132.25 would be a wrong price glued
	// to a garment.
	if got := first["price"]; got != "115.00" {
		t.Errorf("the tag prints %v; the abaya sells for 115.00", got)
	}
	if got := first["tax_rate"]; got == "" || got == "0" {
		t.Errorf("the tag carries no rate, so it cannot say VAT is included: %v",
			got)
	}
	if first["currency"] == "" {
		t.Error("the tag carries no currency, and this product sells in three")
	}
}

// Copies are counted server-side, so what is printed and what was asked for
// cannot diverge.
func TestCopiesRepeatTheLabel(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	seedStudio(t, h, f)

	resp := h.do(t, http.MethodPost, adminPath(f, "/api/v1/labels/print"),
		f.token, map[string]any{"kind": "thermal", "copies": 3})
	labels, _ := decodeJSON(t, resp)["labels"].([]any)
	if len(labels) != 3 {
		t.Errorf("%d labels for one product at three copies, want 3", len(labels))
	}
}

// An A4 sheet says how many fit on it, because that is how a shopkeeper buys
// the paper.
func TestAnA4TemplateSaysHowManyFitOnASheet(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	seedStudio(t, h, f)

	resp := h.do(t, http.MethodGet, adminPath(f, "/api/v1/labels/templates"),
		f.token, nil)
	templates, _ := decodeJSON(t, resp)["data"].([]any)

	found := false
	for _, tpl := range templates {
		row, _ := tpl.(map[string]any)
		if row["kind"] != "a4_sheet" {
			continue
		}
		found = true
		if row["per_sheet"] == nil || row["per_sheet"] == float64(0) {
			t.Errorf("%v does not say how many labels fit on it", row["name"])
		}
	}
	if !found {
		t.Fatal("no A4 sheet template was seeded, so a shop with no thermal " +
			"printer has nothing to print on")
	}
}

// --- global search --------------------------------------------------------

// D7's one box, and the permission check that makes it safe.
func TestSearchFindsWhatTheCallerMayOpen(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/search?q=ABAYA"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	hits, _ := decodeJSON(t, resp)["data"].([]any)
	if len(hits) == 0 {
		t.Fatal("searching a SKU that exists found nothing")
	}
	first, _ := hits[0].(map[string]any)
	if first["kind"] != "product" {
		t.Errorf("the first hit for a SKU is a %v; an exact code match should "+
			"outrank everything", first["kind"])
	}
}

// A cashier does not hold hr.view, so their search does not return staff. The
// branch is not run rather than run and filtered.
func TestACashierSearchDoesNotReturnStaff(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	seedEmployee(t, h, f, "Abaya Nassar")

	resp := h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/search?q=Abaya"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	hits, _ := decodeJSON(t, resp)["data"].([]any)
	for _, hit := range hits {
		row, _ := hit.(map[string]any)
		if row["kind"] == "employee" {
			t.Fatal("a cashier's search returned an employee. D7 says the box " +
				"finds what the user is authorized to see, and a till is not " +
				"authorized to see the staff list.")
		}
	}
	// And it still found the product, rather than refusing the whole query
	// because one branch was closed.
	if len(hits) == 0 {
		t.Error("the search returned nothing at all; the branches the cashier " +
			"CAN see should still answer")
	}
}

// One character matches most of a catalogue.
func TestAOneLetterSearchAnswersNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	hits, _ := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/search?q=A"), f.token, nil))["data"].([]any)
	if len(hits) != 0 {
		t.Errorf("a one-letter search returned %d rows; it should return "+
			"nothing rather than most of the catalogue", len(hits))
	}
}

// --- analytics ------------------------------------------------------------

// A shop with no sales in the period has no gross margin. Reporting 0.0% would
// read as "we made nothing on everything we sold".
func TestARatioWithNothingUnderneathItIsEmpty(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	kpis := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/analytics/kpis?from=2020-01-01&to=2020-02-01"),
		f.token, nil))
	if kpis["gross_margin_pct"] != "" {
		t.Errorf("a period with no sales reports a margin of %v",
			kpis["gross_margin_pct"])
	}
	if kpis["revenue"] != "0.00" {
		t.Errorf("revenue in an empty period is %v, want 0.00", kpis["revenue"])
	}
}

// The figures have to agree with each other, which is why they come from one
// query.
func TestTheKPIsAddUp(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	for i := 0; i < 2; i++ {
		if resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00")); resp.StatusCode != http.StatusCreated {
			t.Fatalf("sale: %s", readBody(t, resp))
		}
	}

	kpis := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/analytics/kpis"), f.token, nil))

	if kpis["orders"] != float64(2) {
		t.Fatalf("%v orders, want 2", kpis["orders"])
	}
	// Two sales of 115.00 net is 230.00, and the average order value has to be
	// that divided by those — not a figure from a second query taken a moment
	// later with a third sale in between.
	// Revenue is NET of tax: two sales at a 115.00 shelf price are 200.00 of
	// turnover and 30.00 the shop is holding for the tax authority. Reporting
	// the gross as revenue would overstate every margin on the dashboard.
	if kpis["revenue"] != "200.00" {
		t.Errorf("revenue is %v, want 200.00 net of VAT", kpis["revenue"])
	}
	if kpis["average_order_value"] != "100.00" {
		t.Errorf("average order value is %v, want 100.00 — which is the "+
			"revenue divided by the orders on the same rows",
			kpis["average_order_value"])
	}
}

// D2's fast movers, and the velocity everything else in the module reads off.
func TestSellingSomethingMakesItAMover(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "4", "115.00", "460.00")).Body.Close()

	movers, _ := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/analytics/movers?days=30"), f.token,
		nil))["data"].([]any)
	if len(movers) == 0 {
		t.Fatal("nothing came back from the movers report")
	}
	first, _ := movers[0].(map[string]any)
	if first["sold_qty"] != "4" {
		t.Errorf("the top mover sold %v, want the 4 that were rung up",
			first["sold_qty"])
	}
	// Never sold reads as −1, which must not sort as recent.
	if first["days_since_sold"] == float64(-1) {
		t.Error("something that just sold reports never having sold")
	}
}

// A forecast that did not say what it was would be read as a model.
func TestAForecastSaysWhatItIs(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "4", "115.00", "460.00")).Body.Close()

	rows, _ := decodeJSON(t, h.do(t, http.MethodGet,
		adminPath(f, "/api/v1/analytics/forecast?window_days=30&forecast_days=30"),
		f.token, nil))["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("a product that sold produced no forecast")
	}
	first, _ := rows[0].(map[string]any)
	if basis, _ := first["basis"].(string); !strings.Contains(basis, "repeated") {
		t.Errorf("the forecast does not say what it is based on: %q", basis)
	}
}

// --- helpers --------------------------------------------------------------

func setScheme(
	t *testing.T, h *harness, f *shopFixture, parts []string, symbology string,
) {
	t.Helper()
	resp := h.do(t, http.MethodPut, adminPath(f, "/api/v1/labels/scheme"),
		f.token, map[string]any{
			"parts": parts, "separator": "-", "symbology": symbology,
			"part_length": 3,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set the scheme: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// seedFashionVariant makes a product with the attributes a smart barcode is
// built from.
func seedFashionVariant(
	t *testing.T, h *harness, f *shopFixture,
	sku, colour, size, category, season string,
) string {
	t.Helper()
	var variantID string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		var categoryID string
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO category (tenant_id, company_id, name)
			VALUES ($1,$2,$3) RETURNING id`,
			f.tenantID, f.companyID, category).Scan(&categoryID); e != nil {
			return e
		}
		var productID string
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO product
			  (tenant_id, company_id, sku, name, category_id, season,
			   tax_treatment)
			VALUES ($1,$2,$3,$4,$5,$6,'standard') RETURNING id`,
			f.tenantID, f.companyID, sku, "Shirt", categoryID, season).
			Scan(&productID); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			INSERT INTO variant
			  (tenant_id, company_id, product_id, sku, attributes, price_retail)
			VALUES ($1,$2,$3,$4,
			        jsonb_build_object('colour',$5::text,'size',$6::text),
			        100.00)
			RETURNING id`,
			f.tenantID, f.companyID, productID, sku, colour, size).
			Scan(&variantID)
	}); err != nil {
		t.Fatalf("seed a fashion variant: %v", err)
	}
	return variantID
}

func seedEmployee(t *testing.T, h *harness, f *shopFixture, name string) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			INSERT INTO employee
			  (tenant_id, company_id, employee_no, full_name, joined_on,
			   currency)
			SELECT $1, $2, $3, $4, current_date, c.base_currency
			FROM company c WHERE c.id = $2`,
			f.tenantID, f.companyID, "E-"+name[:3], name)
		return e
	}); err != nil {
		t.Fatalf("seed an employee: %v", err)
	}
}

func barcodeOf(
	t *testing.T, h *harness, f *shopFixture, variantID string,
) string {
	t.Helper()
	var code string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT coalesce(barcode, '') FROM variant WHERE id = $1`,
			variantID).Scan(&code)
	}); err != nil {
		t.Fatalf("read the barcode: %v", err)
	}
	return code
}

// checkDigitHolds verifies an EAN or UPC check digit.
//
// Recomputed here rather than by calling the package's own function, so this
// test proves the digit is right rather than proving the code agrees with
// itself.
func checkDigitHolds(code string) bool {
	sum := 0
	weight := 1
	for i := len(code) - 2; i >= 0; i-- {
		digit := int(code[i] - '0')
		if digit < 0 || digit > 9 {
			return false
		}
		if weight == 1 {
			sum += digit * 3
			weight = 3
		} else {
			sum += digit
			weight = 1
		}
	}
	return (10-sum%10)%10 == int(code[len(code)-1]-'0')
}

// seedStudio gives the test company its label templates.
//
// The shop fixture builds a company by hand rather than through onboarding, so
// it misses what SeedLabelStudio gives a real one — the same gap the fixture
// has always had with the chart of accounts.
func seedStudio(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedLabelStudio(t.Context(), tx, f.tenantID,
			f.companyID)
	}); err != nil {
		t.Fatalf("seed the label studio: %v", err)
	}
}
