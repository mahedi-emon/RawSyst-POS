//go:build integration

// UI spec §4 — the variant matrix, over the route the Back Office reads.
//
// The screen draws each cell from three facts, and all three come from the
// server because two of them need the whole company: quantity is summed ACROSS
// warehouses — comparing per warehouse reports a shop as low while a full box
// sits in the back room — and the last sale is the last one anywhere, not the
// last one on the till that happens to be asking.
//
// A client that computed any of them would be guessing with a subset of the
// data, which is the failure the matrix exists to prevent.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// uniqueSKU keeps these fixtures from colliding with each other or with the
// product seedShop already creates. A SKU is unique per company by index, and
// a fixed one here makes the test depend on which other tests ran first.
func uniqueSKU(prefix string) string {
	return prefix + "-" + strings.ToUpper(uuid.NewString()[:8])
}

// matrixOf reads the grid for a product over the real route.
func matrixOf(t *testing.T, h *harness, f *shopFixture, productID uuid.UUID) []map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		"/api/v1/catalog/products/"+productID.String()+"/matrix", f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read matrix: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	raw, _ := decodeJSON(t, resp)["data"].([]any)

	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if cell, ok := r.(map[string]any); ok {
			out = append(out, cell)
		}
	}
	return out
}

func cellBySKU(cells []map[string]any, sku string) map[string]any {
	for _, c := range cells {
		if c["sku"] == sku {
			return c
		}
	}
	return nil
}

// The grid carries stock, the reorder level and the last sale.
//
// Without all three the screen cannot tell "In stock" from "Low" from "Dead",
// and would have to invent a rule for whichever it was missing.
func TestTheMatrixCarriesWhatEachCellIsDrawnFrom(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()

	productID := h.newProduct(t, f, uniqueSKU("ABAYA"), "Executive Abaya")
	blackSKU, navySKU := uniqueSKU("BLK"), uniqueSKU("NVY")

	// Two variants: one with a reorder level, one without. A variant with no
	// reorder level is never "low", only in stock or out, and the grid has to
	// be able to say so.
	var withLevel, without uuid.UUID
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO variant
			  (tenant_id, company_id, product_id, sku, attributes,
			   price_retail, reorder_level)
			VALUES ($1,$2,$3,$4,'{"colour":"Black","size":"L"}',449,5)
			RETURNING id`, f.tenantID, f.companyID, productID, blackSKU).Scan(&withLevel); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			INSERT INTO variant
			  (tenant_id, company_id, product_id, sku, attributes, price_retail)
			VALUES ($1,$2,$3,$4,'{"colour":"Navy","size":"M"}',449)
			RETURNING id`, f.tenantID, f.companyID, productID, navySKU).Scan(&without)
	}); err != nil {
		t.Fatalf("seed variants: %v", err)
	}

	cells := matrixOf(t, h, f, productID)
	if len(cells) != 2 {
		t.Fatalf("the grid holds %d cells, want 2", len(cells))
	}

	black := cellBySKU(cells, blackSKU)
	navy := cellBySKU(cells, navySKU)
	if black == nil || navy == nil {
		t.Fatalf("the grid is missing a variant: %v", cells)
	}

	// The attributes are what the screen grids on. Blueprint B2 allows any
	// number of them, so they arrive as a map rather than a fixed pair.
	attrs, _ := black["attributes"].(map[string]any)
	if attrs["colour"] != "Black" || attrs["size"] != "L" {
		t.Errorf("attributes = %v, want colour Black and size L", attrs)
	}

	// Carries the column's scale, like the quantity. The grid compares it as a
	// number, so the scale is harmless there — it matters only where a figure
	// is drawn, which is why the cell trims before rendering.
	if black["reorder_level"] != "5.0000" {
		t.Errorf("reorder level = %v, want 5", black["reorder_level"])
	}
	// Absent rather than zero. Nobody has said what low means for this one, and
	// a zero would read as "low the moment it has anything".
	if got, present := navy["reorder_level"]; present && got != "" {
		t.Errorf("a variant with no reorder level reported %v, want it absent", got)
	}

	// Nothing has sold, so nothing has a last sale, and both hold nothing.
	for _, c := range cells {
		if c["on_hand"] != "0" && c["on_hand"] != "0.0000" {
			t.Errorf("%v holds %v before any receipt, want 0", c["sku"], c["on_hand"])
		}
		if got, present := c["last_sold_at"]; present && got != "" {
			t.Errorf("%v reports a last sale of %v before anything sold", c["sku"], got)
		}
	}
}

// Quantity is summed across the company's warehouses.
//
// The distinction that matters: a shop with four on the floor and a box of
// twenty in the back room is not low. Reporting per warehouse would send it
// reordering against a shortage it does not have.
func TestTheMatrixSumsStockAcrossWarehouses(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()

	productID := h.newProduct(t, f, uniqueSKU("SCARF"), "Silk Scarf")

	var variantID, backRoom uuid.UUID
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO variant
			  (tenant_id, company_id, product_id, sku, attributes, price_retail)
			VALUES ($1,$2,$3,$4,'{"colour":"Red","size":"One"}',99)
			RETURNING id`, f.tenantID, f.companyID, productID,
			uniqueSKU("SCARF-RED")).Scan(&variantID); e != nil {
			return e
		}
		// A second warehouse in the same company.
		return tx.QueryRow(ctx, `
			INSERT INTO warehouse (tenant_id, company_id, store_id, code, name)
			VALUES ($1,$2,$3,'WH2','Back room') RETURNING id`,
			f.tenantID, f.companyID, f.storeID).Scan(&backRoom)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Four on the floor, twenty out the back.
	for _, r := range []struct {
		warehouse uuid.UUID
		qty       string
	}{{f.warehouseID, "4"}, {backRoom, "20"}} {
		if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `
				INSERT INTO stock_movement
				  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, value_delta)
				VALUES ($1,$2,$3,$4,$5,'grn',0)`,
				f.tenantID, f.companyID, variantID, r.warehouse, r.qty)
			return e
		}); err != nil {
			t.Fatalf("seed stock: %v", err)
		}
	}

	cells := matrixOf(t, h, f, productID)
	if len(cells) != 1 {
		t.Fatalf("the grid holds %d cells, want 1", len(cells))
	}
	// The column is numeric(18,4), so the sum arrives with its scale — which is
	// exactly why the grid trims it before drawing a cell.
	if got := cells[0]["on_hand"]; got != "24.0000" {
		t.Fatalf("on hand = %v, want 24 — four on the floor and twenty in the "+
			"back room are the same shop's stock", got)
	}
}

// The last sale is a SALE, not any movement.
//
// A transfer between warehouses or a stock adjustment moves the line without
// anybody buying it. Counting those would hide the dead stock this column
// exists to surface — the whole point of §4's grey cell.
func TestTheMatrixDatesTheLastSaleAndIgnoresOtherMovements(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	ctx := t.Context()

	productID := h.newProduct(t, f, uniqueSKU("THOBE"), "Cotton Thobe")

	var variantID uuid.UUID
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO variant
			  (tenant_id, company_id, product_id, sku, attributes, price_retail)
			VALUES ($1,$2,$3,$4,'{"colour":"White","size":"L"}',299)
			RETURNING id`, f.tenantID, f.companyID, productID,
			uniqueSKU("THOBE-WHT")).Scan(&variantID)
	}); err != nil {
		t.Fatalf("seed variant: %v", err)
	}

	// A receipt and a transfer, both recent, neither a sale.
	for _, reason := range []string{"grn", "transfer_in"} {
		if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `
				INSERT INTO stock_movement
				  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, value_delta)
				VALUES ($1,$2,$3,$4,5,$5,0)`,
				f.tenantID, f.companyID, variantID, f.warehouseID, reason)
			return e
		}); err != nil {
			t.Fatalf("seed %s: %v", reason, err)
		}
	}

	if got := matrixOf(t, h, f, productID)[0]["last_sold_at"]; got != nil && got != "" {
		t.Fatalf("a receipt and a transfer produced a last sale of %v; the grid "+
			"would call moved stock sold and hide a dead line", got)
	}

	// Now an actual sale.
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, value_delta)
			VALUES ($1,$2,$3,$4,-1,'sale',0)`,
			f.tenantID, f.companyID, variantID, f.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("seed sale: %v", err)
	}

	got, _ := matrixOf(t, h, f, productID)[0]["last_sold_at"].(string)
	if len(got) != 10 {
		t.Fatalf("last sold = %q, want a YYYY-MM-DD date once something sold", got)
	}
}

// QA gate M8 on the grid: one shop cannot read another's stock.
//
// Stock levels are commercially sensitive — what a rival holds and what is not
// moving for them is exactly what a competitor would want.
func TestOneShopCannotReadAnothersGrid(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	theirProduct := h.newProduct(t, theirs, uniqueSKU("SECRET"), "Their Product")

	// Not found rather than forbidden: a 403 would confirm the product exists.
	resp := h.do(t, http.MethodGet,
		"/api/v1/catalog/products/"+theirProduct.String()+"/matrix", mine.token, nil)
	if resp.StatusCode == http.StatusOK {
		if cells, _ := decodeJSON(t, resp)["data"].([]any); len(cells) > 0 {
			t.Fatalf("one shop read %d cells of another's grid", len(cells))
		}
	} else if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reading another shop's grid: status %d, want 404 or an empty "+
			"grid — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// QA gate M7: reading the grid needs catalog.view.
//
// A cashier holds it — they scan the catalogue all day — and a delivery driver
// does not.
func TestTheGridNeedsCatalogView(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	productID := h.newProduct(t, f, uniqueSKU("BAG"), "Leather Bag")
	path := "/api/v1/catalog/products/" + productID.String() + "/matrix"

	cashier := h.seedUserIn(t, f, "cashier")
	resp := h.do(t, http.MethodGet, path, cashier, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cashier reading the grid: status %d, want 200 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	driver := h.seedUserIn(t, f, "delivery_staff")
	resp = h.do(t, http.MethodGet, path, driver, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("delivery staff reading the grid: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}
