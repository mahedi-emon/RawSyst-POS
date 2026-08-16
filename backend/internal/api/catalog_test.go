//go:build integration

// Catalogue and the variant matrix.
//
// Blueprint B2: a fashion retailer sets up one Abaya and needs the size by
// colour grid to exist as sellable variants. The property that matters is not
// that generation works once — it is that regenerating with one more colour
// adds a column and touches nothing else, because the existing cells have been
// sold and carry their own prices, barcodes and stock.
package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

func (h *harness) catalogue() *catalog.Service {
	return catalog.NewService(h.pool, registry.New(h.pool, false))
}

func (h *harness) newProduct(t *testing.T, f *shopFixture, sku, name string) uuid.UUID {
	t.Helper()
	p, err := h.catalogue().CreateProduct(t.Context(), f.tenantID, catalog.NewProduct{
		CompanyID: f.companyID, SKU: sku, Name: name,
		NameAr: "عباية", TaxTreatment: "standard",
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	return p.ID
}

func sizeColourAxes(colours ...string) []catalog.Axis {
	return []catalog.Axis{
		{Name: "size", Values: []string{"S", "M", "L"}},
		{Name: "colour", Values: colours},
	}
}

// The grid is the cartesian product, with a SKU derived from the product's.
func TestGeneratingAVariantMatrix(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	productID := h.newProduct(t, f, "ABAYA-EXEC-2", "Executive Abaya")

	got, err := h.catalogue().GenerateMatrix(t.Context(), f.tenantID,
		catalog.MatrixRequest{
			ProductID: productID,
			Axes:      sizeColourAxes("Black", "Navy"),
			BasePrice: decimal.RequireFromString("1150.00"),
		})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if got.Combinations != 6 {
		t.Errorf("%d combinations, want 6 (3 sizes x 2 colours)", got.Combinations)
	}
	if len(got.Created) != 6 {
		t.Fatalf("%d variants created, want 6", len(got.Created))
	}
	if len(got.Existing) != 0 {
		t.Errorf("%d existing variants reported on a first generation", len(got.Existing))
	}

	// SKUs follow AXIS order, not map order, so they are stable across runs —
	// a SKU that changed between generations would be printed on two different
	// labels for the same garment.
	skus := map[string]bool{}
	for _, v := range got.Created {
		skus[v.SKU] = true
		if !strings.HasPrefix(v.SKU, "ABAYA-EXEC-2-") {
			t.Errorf("variant SKU %q does not derive from the product code", v.SKU)
		}
	}
	if !skus["ABAYA-EXEC-2-S-BLACK"] || !skus["ABAYA-EXEC-2-L-NAVY"] {
		t.Errorf("SKUs are not in axis order: %v", skus)
	}
}

// Regeneration adds the new column and leaves the old cells alone. This is the
// property the whole design exists for.
func TestRegeneratingAddsOnlyWhatIsMissing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	svc := h.catalogue()
	productID := h.newProduct(t, f, "SHIRT", "Shirt")

	first, err := svc.GenerateMatrix(t.Context(), f.tenantID, catalog.MatrixRequest{
		ProductID: productID, Axes: sizeColourAxes("Black"),
		BasePrice: decimal.RequireFromString("100.00"),
	})
	if err != nil {
		t.Fatalf("first generation: %v", err)
	}
	if len(first.Created) != 3 {
		t.Fatalf("%d created, want 3", len(first.Created))
	}

	// One of them gets a price of its own, as a shop would do.
	repriced := first.Created[0]
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE variant SET price_retail = 133.00 WHERE id = $1`, repriced.ID)
		return e
	}); err != nil {
		t.Fatalf("reprice: %v", err)
	}

	// Now the shop adds Navy.
	second, err := svc.GenerateMatrix(t.Context(), f.tenantID, catalog.MatrixRequest{
		ProductID: productID, Axes: sizeColourAxes("Black", "Navy"),
		BasePrice: decimal.RequireFromString("100.00"),
	})
	if err != nil {
		t.Fatalf("second generation: %v", err)
	}

	if len(second.Created) != 3 {
		t.Errorf("%d created on the second run, want the 3 new Navy cells",
			len(second.Created))
	}
	if len(second.Existing) != 3 {
		t.Errorf("%d existing reported, want the 3 Black cells", len(second.Existing))
	}

	// The repriced cell kept its price. Overwriting it would silently undo a
	// deliberate decision, and on a live catalogue nobody would notice until a
	// customer was charged the wrong amount.
	var price decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT price_retail FROM variant WHERE id = $1`, repriced.ID).Scan(&price)
	}); err != nil {
		t.Fatalf("read price: %v", err)
	}
	if !price.Equal(decimal.RequireFromString("133")) {
		t.Errorf("the repriced variant is now %s; regeneration overwrote it", price)
	}

	// Six cells in total, not nine.
	grid, err := svc.ReadMatrix(t.Context(), f.tenantID, productID)
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	if len(grid) != 6 {
		t.Errorf("the grid holds %d variants, want 6", len(grid))
	}
}

// Running the same generation twice must create nothing the second time.
func TestGeneratingTheSameGridTwiceIsANoOp(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	svc := h.catalogue()
	productID := h.newProduct(t, f, "SCARF", "Scarf")

	req := catalog.MatrixRequest{
		ProductID: productID, Axes: sizeColourAxes("Black"),
		BasePrice: decimal.RequireFromString("50.00"),
	}

	if _, err := svc.GenerateMatrix(t.Context(), f.tenantID, req); err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := svc.GenerateMatrix(t.Context(), f.tenantID, req)
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if len(again.Created) != 0 {
		t.Errorf("%d variants created by regenerating an unchanged grid",
			len(again.Created))
	}
	if len(again.Existing) != 3 {
		t.Errorf("%d existing reported, want 3", len(again.Existing))
	}
}

// A grid is a convenience, not a bulk import, and an accidental extra axis
// should be caught before a shop has a thousand barcodes to print.
func TestAnAbsurdlyLargeGridIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	productID := h.newProduct(t, f, "HUGE", "Huge")

	many := make([]string, 30)
	for i := range many {
		many[i] = string(rune('A'+i%26)) + strings.Repeat("x", i%3)
	}

	_, err := h.catalogue().GenerateMatrix(t.Context(), f.tenantID,
		catalog.MatrixRequest{
			ProductID: productID,
			Axes: []catalog.Axis{
				{Name: "a", Values: many},
				{Name: "b", Values: many},
			},
			BasePrice: decimal.RequireFromString("1.00"),
		})
	if err == nil {
		t.Fatal("a 900-cell grid was generated in one go")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("the refusal does not say what the limit is: %v", err)
	}
}

// Bad axes are caught with messages that name the problem.
func TestMalformedAxesAreRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	productID := h.newProduct(t, f, "BAD", "Bad")
	svc := h.catalogue()

	for _, tc := range []struct {
		name string
		axes []catalog.Axis
		want string
	}{
		{"no axes", nil, "at least one axis"},
		{"unnamed axis", []catalog.Axis{{Values: []string{"S"}}}, "needs a name"},
		{"empty axis", []catalog.Axis{{Name: "size"}}, "no values"},
		{"duplicate value", []catalog.Axis{
			{Name: "size", Values: []string{"S", "M", "s"}}}, "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.GenerateMatrix(t.Context(), f.tenantID,
				catalog.MatrixRequest{ProductID: productID, Axes: tc.axes,
					BasePrice: decimal.RequireFromString("1.00")})
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message does not explain the problem: %v", err)
			}
		})
	}
}

// A tax treatment the country does not recognise is refused at setup, not at
// the till with a customer waiting.
func TestAnUnknownTaxTreatmentIsRefusedAtSetup(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	_, err := h.catalogue().CreateProduct(t.Context(), f.tenantID, catalog.NewProduct{
		CompanyID: f.companyID, SKU: "ODD", Name: "Odd",
		TaxTreatment: "gst",
	})
	if err == nil {
		t.Fatal("a product was created with a treatment Saudi Arabia does not use")
	}
	if !strings.Contains(err.Error(), "gst") {
		t.Errorf("the refusal does not name the treatment: %v", err)
	}
}

// A variant is withdrawn, never deleted. It is referenced by invoice lines,
// stock movements and cost layers, all of which are immutable history.
func TestAVariantIsWithdrawnRatherThanDeleted(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	// The seeded variant has already been sold in other tests' shape: sell it
	// here so it genuinely carries history.
	if resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00")); resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}

	if err := h.catalogue().Deactivate(ctx, f.tenantID, f.variantID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	var active bool
	var lines int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT is_active FROM variant WHERE id = $1`, f.variantID).
			Scan(&active); e != nil {
			return e
		}
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM sales_invoice_line WHERE variant_id = $1`,
			f.variantID).Scan(&lines)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}

	if active {
		t.Error("the variant is still on sale after being withdrawn")
	}
	if lines == 0 {
		t.Error("withdrawing the variant removed its sales history")
	}
}

// One tenant cannot read or generate into another's catalogue.
func TestOneTenantCannotTouchAnothersCatalogue(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")
	svc := h.catalogue()

	theirProduct := h.newProduct(t, theirs, "THEIRS", "Theirs")

	_, err := svc.GenerateMatrix(t.Context(), mine.tenantID, catalog.MatrixRequest{
		ProductID: theirProduct, Axes: sizeColourAxes("Black"),
		BasePrice: decimal.RequireFromString("10.00"),
	})
	if err == nil {
		t.Fatal("one tenant generated variants onto another tenant's product")
	}

	grid, err := svc.ReadMatrix(t.Context(), mine.tenantID, theirProduct)
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	if len(grid) != 0 {
		t.Fatalf("one tenant read %d of another tenant's variants", len(grid))
	}
}

// Product search finds by name or code, and does not cross the company line.
func TestProductSearch(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	svc := h.catalogue()

	h.newProduct(t, f, "ABAYA-A", "Silk Abaya")
	h.newProduct(t, f, "THOBE-A", "Cotton Thobe")

	byName, err := svc.ListProducts(t.Context(), f.tenantID, f.companyID, "abaya", 50, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// The seeded fixture product is also called Abaya, so two match.
	if len(byName) < 1 {
		t.Fatalf("searching for abaya returned nothing")
	}
	for _, p := range byName {
		if !strings.Contains(strings.ToLower(p.Name+p.SKU), "abaya") {
			t.Errorf("search returned %q, which does not match", p.Name)
		}
	}

	byCode, err := svc.ListProducts(t.Context(), f.tenantID, f.companyID, "THOBE", 50, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(byCode) != 1 {
		t.Errorf("searching by code returned %d products, want 1", len(byCode))
	}
}

// A till scans without knowing its company.
//
// Everything about where a terminal trades is resolved from the registered
// device. Requiring the till to supply a company id would mean either teaching
// it a fact it has no way to learn, or letting it assert one — and a terminal
// that could name its own company could read another company's catalogue,
// which row-level security would not catch because both belong to one tenant.
func TestATillScansWithoutNamingItsCompany(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE variant SET barcode = '6281000000017' WHERE id = $1`, f.variantID)
		return e
	}); err != nil {
		t.Fatalf("give the variant a barcode: %v", err)
	}

	resp := h.do(t, "GET", "/api/v1/catalog/scan?barcode=6281000000017", f.token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("scan: %d %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if body["id"] != f.variantID.String() {
		t.Errorf("scan returned %v, want the seeded variant", body["id"])
	}
	// Money crosses as a string, and stays one all the way to the till.
	if _, ok := body["price"].(string); !ok {
		t.Errorf("price came back as %T, not a string", body["price"])
	}
}

// A caller with no terminal must say which company, because nothing else can.
func TestScanningWithoutATerminalNeedsACompany(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	token, _, err := h.tokens.IssueAccess(actorWithoutDevice(f))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	resp := h.do(t, "GET", "/api/v1/catalog/scan?barcode=123", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// One tenant cannot scan another tenant's catalogue, even naming the company.
func TestATillCannotScanAnotherCompanysCatalogue(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")

	resp := h.do(t, "GET",
		"/api/v1/catalog/scan?barcode=123&company_id="+theirs.companyID.String(),
		mine.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// The catalogue a till caches so it can scan with the network down.
//
// This is the difference between a terminal that can finish a sale already in
// the cart and one that can start a new one, which is the only definition of
// offline-capable a shop would accept.
func TestSnapshotServesTheTillTheCatalogue(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	ctx := t.Context()
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE variant SET barcode = '6281000000017' WHERE id = $1`, f.variantID)
		return e
	}); err != nil {
		t.Fatalf("give the variant a barcode: %v", err)
	}

	resp := h.do(t, "GET", "/api/v1/catalog/snapshot", f.token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("snapshot: %d %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	items, _ := body["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("snapshot returned nothing; the till would have an empty catalogue")
	}

	var found map[string]any
	for _, raw := range items {
		row, _ := raw.(map[string]any)
		if row["id"] == f.variantID.String() {
			found = row
		}
	}
	if found == nil {
		t.Fatalf("the seeded variant was not in the snapshot")
	}

	if found["barcode"] != "6281000000017" {
		t.Errorf("barcode came back as %v; a till cannot scan without it", found["barcode"])
	}
	// Money crosses as a string here exactly as it does everywhere else.
	if _, ok := found["price"].(string); !ok {
		t.Errorf("price came back as %T, not a string", found["price"])
	}

	// The cursor the till stores so its next pull is a delta.
	if body["next_since"] == nil || body["next_since_id"] == nil {
		t.Errorf("no cursor returned; every pull would download the whole catalogue")
	}
}

// A Cashier holds catalog.view and is denied catalog.view_cost_price. A cache
// that carried cost would put it on every till in the shop and defeat the
// masking the permission exists to provide.
func TestSnapshotNeverCarriesCostPrice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "GET", "/api/v1/catalog/snapshot", f.token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("snapshot: %d %s", resp.StatusCode, readBody(t, resp))
	}

	body := strings.ToLower(readBody(t, resp))
	for _, leak := range []string{"cost", "margin"} {
		if strings.Contains(body, leak) {
			t.Errorf("the snapshot mentions %q; a till must never hold it", leak)
		}
	}
}

// The cursor is what makes a later pull a delta rather than a full download.
func TestSnapshotCursorReturnsOnlyWhatChanged(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	firstResp := h.do(t, "GET", "/api/v1/catalog/snapshot", f.token, nil)
	if firstResp.StatusCode != 200 {
		t.Fatalf("snapshot: %d %s", firstResp.StatusCode, readBody(t, firstResp))
	}
	first := decodeJSON(t, firstResp)
	since, _ := first["next_since"].(string)
	sinceID, _ := first["next_since_id"].(string)
	if since == "" || sinceID == "" {
		t.Fatalf("no cursor to follow")
	}

	path := "/api/v1/catalog/snapshot?since=" + url.QueryEscape(since) +
		"&since_id=" + url.QueryEscape(sinceID)
	secondResp := h.do(t, "GET", path, f.token, nil)
	if secondResp.StatusCode != 200 {
		t.Fatalf("snapshot delta: %d %s", secondResp.StatusCode, readBody(t, secondResp))
	}
	second := decodeJSON(t, secondResp)

	items, _ := second["items"].([]any)
	if len(items) != 0 {
		t.Errorf("a caught-up till was sent %d rows again", len(items))
	}
}

// The probe a terminal uses to decide whether it can sync.
//
// Authenticated deliberately: what a till needs to know before draining a day
// of takings is not "is there a network" but "can I sync right now".
func TestPingAnswersOnlyASignedInTerminal(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "GET", "/api/v1/meta/ping", f.token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("ping returned %d, want 204 — the terminal treats anything "+
			"else as unreachable", resp.StatusCode)
	}
	// An empty body is the point: a captive portal returning 200 with a login
	// page must not be mistaken for us.
	if body := readBody(t, resp); body != "" {
		t.Errorf("ping returned a body %q; it should be empty", body)
	}

	unauth := h.do(t, "GET", "/api/v1/meta/ping", "", nil)
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Errorf("ping without a token returned %d, want 401", unauth.StatusCode)
	}
}
