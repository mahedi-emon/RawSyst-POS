//go:build integration

// Light production cost tracking (blueprint C3.1).
//
// A garment retailer buys cloth, has it stitched, packs it, and sells a shirt.
// What C3.1 asks for is that the shirt's cost be a real number: raw material,
// stitching labour and packaging "recorded and allocated per production batch,
// so the true cost of a locally-made item is known and flows correctly into
// COGS and margin".
//
// These hold it to the two consequences that matter and are easy to get wrong:
// the ledger has to balance, and the Inventory account has to rise by exactly
// the work done — the cloth was already owned, so only the labour and packaging
// are new value.
package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
)

// componentInStock creates a raw material and puts some in stock at a known
// cost, with the matching ledger entry.
//
// Both halves matter. `inventory.Receive` moves the stock and values it;
// without the posting beside it the Inventory account would not know about the
// cloth, and the tie-out these tests assert would fail for the seeding rather
// than for anything production did.
func componentInStock(
	t *testing.T, h *harness, f *shopFixture, sku string,
	qty, unitCost string,
) string {
	t.Helper()
	ctx := context.Background()
	var variantID uuid.UUID
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var productID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO product (tenant_id, company_id, sku, name)
			VALUES ($1,$2,$3,$4) RETURNING id`,
			f.tenantID, f.companyID, sku, "Raw "+sku).Scan(&productID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO variant (tenant_id, company_id, product_id, sku,
			                     price_retail, is_active)
			VALUES ($1,$2,$3,$4,0,true) RETURNING id`,
			f.tenantID, f.companyID, productID, sku).Scan(&variantID); e != nil {
			return e
		}

		posted, e := inventory.Receive(ctx, tx, inventory.Receipt{
			TenantID: f.tenantID, CompanyID: f.companyID,
			VariantID: variantID, WarehouseID: f.warehouseID,
			Qty:      decimal.RequireFromString(qty),
			UnitCost: decimal.RequireFromString(unitCost),
			Reason:   "opening",
		})
		if e != nil {
			return e
		}

		// The entry the stock arrived with. Credited to cash because the shop
		// bought the cloth; what matters here is only that the Inventory
		// account knows it holds it.
		sourceID := uuid.New()
		_, e = accounting.Post(ctx, tx, accounting.Entry{
			TenantID: f.tenantID, CompanyID: f.companyID,
			Date:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			SourceType: "opening_stock", SourceID: sourceID,
			Memo: "Component seeded for a production test",
			Lines: []accounting.Line{
				{Role: "inventory", Side: accounting.Debit, Amount: posted},
				{Role: "cash", Side: accounting.Credit, Amount: posted},
			},
		})
		return e
	}); err != nil {
		t.Fatalf("seed component %s: %v", sku, err)
	}
	return variantID.String()
}

func recordProduction(t *testing.T, h *harness, f *shopFixture, body map[string]any) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost,
		"/api/v1/stock/production?company_id="+f.companyID.String(),
		f.token, body)
}

// inventoryBalance is what the Inventory control account says.
func inventoryBalance(t *testing.T, h *harness, f *shopFixture) decimal.Decimal {
	t.Helper()
	var bal decimal.Decimal
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
				FROM journal_line l
				JOIN account a ON a.id = l.account_id
				WHERE a.company_id = $1 AND a.is_control
				  AND a.control_of = 'inventory'`, f.companyID).Scan(&bal)
		}); err != nil {
		t.Fatalf("read inventory balance: %v", err)
	}
	return bal
}

// A batch costs its output from what went into it.
//
// Ten metres of cloth at 20 is 200 of material; 150 of stitching and 50 of
// packaging make 400 for ten shirts, so a shirt costs 40. That number is the
// whole point of C3.1.
func TestAProductionBatchCostsWhatWentIntoIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cloth := componentInStock(t, h, f, "CLOTH-1", "10", "20.00")

	resp := recordProduction(t, h, f, map[string]any{
		"uuid": newUUID(), "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty_produced": "10",
		"inputs": []any{map[string]any{
			"variant_id": cloth, "qty": "10"}},
		"labour_cost": "150.00", "packaging_cost": "50.00",
		"paid_from": "cash", "produced_on": "2026-08-15",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record: %d %s", resp.StatusCode, readBody(t, resp))
	}
	out := decodeJSONFrom(t, resp)

	for _, c := range []struct{ field, want string }{
		{"material_cost", "200.00"},
		{"labour_cost", "150.00"},
		{"packaging_cost", "50.00"},
		{"total_cost", "400.00"},
		{"unit_cost", "40.00"},
	} {
		got, _ := out[c.field].(string)
		if !amountsEqual(got, c.want) {
			t.Errorf("%s = %s, want %s", c.field, got, c.want)
		}
	}
	if no, _ := out["batch_no"].(string); no == "" {
		t.Error("the batch has no number")
	}
	assertTrialBalanceBalances(t, h, f)
}

// Inventory rises by exactly the work done, and the books balance.
//
// The cloth was already owned, so producing does not make the shop richer by
// the cloth's value a second time. Only the labour and the packaging are new
// value in stock — that is the statement the ledger has to make.
func TestProductionAddsOnlyTheWorkToInventory(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cloth := componentInStock(t, h, f, "CLOTH-2", "10", "20.00")

	before := inventoryBalance(t, h, f)

	resp := recordProduction(t, h, f, map[string]any{
		"uuid": newUUID(), "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty_produced": "10",
		"inputs":      []any{map[string]any{"variant_id": cloth, "qty": "10"}},
		"labour_cost": "150.00", "packaging_cost": "50.00",
		"paid_from": "cash", "produced_on": "2026-08-15",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record: %s", readBody(t, resp))
	}
	resp.Body.Close()

	after := inventoryBalance(t, h, f)
	rise := after.Sub(before)
	if !rise.Equal(decimal.RequireFromString("200")) {
		t.Errorf("Inventory rose by %s; the cloth was already owned, so only "+
			"the 150 labour and 50 packaging are new value", rise)
	}
	assertTrialBalanceBalances(t, h, f)
	assertInventoryTiesToTheLedger(t, h, f)
}

// The finished units are in stock, and the components are not.
func TestProductionMovesTheStockItCosts(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cloth := componentInStock(t, h, f, "CLOTH-3", "10", "20.00")

	recordProduction(t, h, f, map[string]any{
		"uuid": newUUID(), "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty_produced": "10",
		"inputs":      []any{map[string]any{"variant_id": cloth, "qty": "10"}},
		"labour_cost": "150.00", "paid_from": "cash",
		"produced_on": "2026-08-15",
	}).Body.Close()

	ctx := context.Background()
	var clothLeft decimal.Decimal
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT stock_on_hand($1::uuid, $2)`,
			cloth, f.warehouseID).Scan(&clothLeft)
	}); err != nil {
		t.Fatalf("read component stock: %v", err)
	}
	if !clothLeft.IsZero() {
		t.Errorf("%s of the component is left, want 0 — it went into the batch",
			clothLeft)
	}
}

// A batch made from stock the shop does not have is refused when the policy
// blocks it — the same question as selling a shirt that is not there.
func TestProductionCannotConsumeStockThatIsNotThere(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cloth := componentInStock(t, h, f, "CLOTH-4", "2", "20.00")

	if err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		_, e := tx.Exec(context.Background(),
			`UPDATE company SET negative_stock_policy = 'block' WHERE id = $1`,
			f.companyID)
		return e
	}); err != nil {
		t.Fatalf("set the policy: %v", err)
	}

	resp := recordProduction(t, h, f, map[string]any{
		"uuid": newUUID(), "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty_produced": "10",
		"inputs":      []any{map[string]any{"variant_id": cloth, "qty": "10"}},
		"labour_cost": "150.00", "paid_from": "cash",
		"produced_on": "2026-08-15",
	})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("a batch consumed cloth the shop did not have")
	}
}

// A retry records one batch, not two.
func TestTheSameProductionBatchArrivingTwiceIsRecordedOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cloth := componentInStock(t, h, f, "CLOTH-5", "20", "20.00")

	body := map[string]any{
		"uuid": newUUID(), "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty_produced": "10",
		"inputs":      []any{map[string]any{"variant_id": cloth, "qty": "10"}},
		"labour_cost": "150.00", "paid_from": "cash",
		"produced_on": "2026-08-15",
	}

	first := recordProduction(t, h, f, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first: %s", readBody(t, first))
	}
	firstID, _ := decodeJSONFrom(t, first)["id"].(string)

	second := recordProduction(t, h, f, body)
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second: %d %s", second.StatusCode, readBody(t, second))
	}
	secondID, _ := decodeJSONFrom(t, second)["id"].(string)

	if firstID != secondID {
		t.Errorf("the retry made a second batch (%s then %s)",
			firstID, secondID)
	}
	assertTrialBalanceBalances(t, h, f)
}

// A batch of nothing is refused, and so is one that makes nothing.
func TestProductionRefusesNonsense(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"no quantity", map[string]any{
			"uuid": newUUID(), "variant_id": f.variantID.String(),
			"warehouse_id": f.warehouseID.String(), "qty_produced": "0",
			"labour_cost": "10.00", "paid_from": "cash",
		}},
		{"nothing in it", map[string]any{
			"uuid": newUUID(), "variant_id": f.variantID.String(),
			"warehouse_id": f.warehouseID.String(), "qty_produced": "5",
			"paid_from": "cash",
		}},
		{"a negative cost", map[string]any{
			"uuid": newUUID(), "variant_id": f.variantID.String(),
			"warehouse_id": f.warehouseID.String(), "qty_produced": "5",
			"labour_cost": "-10.00", "paid_from": "cash",
		}},
		{"made out of itself", map[string]any{
			"uuid": newUUID(), "variant_id": f.variantID.String(),
			"warehouse_id": f.warehouseID.String(), "qty_produced": "5",
			"inputs": []any{map[string]any{
				"variant_id": f.variantID.String(), "qty": "1"}},
			"paid_from": "cash",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := recordProduction(t, h, f, c.body)
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusCreated {
				t.Errorf("%s was accepted", c.name)
			}
		})
	}
}

// A costed batch is history: its unit cost is carried by the units it made.
func TestACostedBatchCannotBeEditedOrDeleted(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cloth := componentInStock(t, h, f, "CLOTH-6", "10", "20.00")

	resp := recordProduction(t, h, f, map[string]any{
		"uuid": newUUID(), "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty_produced": "10",
		"inputs":      []any{map[string]any{"variant_id": cloth, "qty": "10"}},
		"labour_cost": "150.00", "paid_from": "cash",
		"produced_on": "2026-08-15",
	})
	id, _ := decodeJSONFrom(t, resp)["id"].(string)

	ctx := context.Background()
	for _, c := range []struct{ name, sql string }{
		{"the unit cost", `UPDATE production_batch SET unit_cost = 1 WHERE id = $1`},
		{"the quantity", `UPDATE production_batch SET qty_produced = 99 WHERE id = $1`},
		{"the row itself", `DELETE FROM production_batch WHERE id = $1`},
	} {
		err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, c.sql, id)
			return e
		})
		if err == nil {
			t.Errorf("%s could be changed after costing", c.name)
		}
	}
}

// Somebody who may not move stock may not produce.
func TestACashierCannotRecordProduction(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := recordProduction(t, h, f, map[string]any{
		"uuid": newUUID(), "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty_produced": "1",
		"labour_cost": "10.00", "paid_from": "cash",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cashier recording production got %d, want 403",
			resp.StatusCode)
	}
}

// Two batches of the same item cost independently.
//
// The second batch's cloth is drawn at whatever the costing method says is
// left, not at the first batch's rate, and the two unit costs differ because
// the labour differed.
func TestTwoBatchesCostIndependently(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cloth := componentInStock(t, h, f, "CLOTH-7", "20", "20.00")

	first := recordProduction(t, h, f, map[string]any{
		"uuid": newUUID(), "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty_produced": "10",
		"inputs":      []any{map[string]any{"variant_id": cloth, "qty": "10"}},
		"labour_cost": "100.00", "paid_from": "cash",
		"produced_on": "2026-08-15",
	})
	firstUnit, _ := decodeJSONFrom(t, first)["unit_cost"].(string)

	second := recordProduction(t, h, f, map[string]any{
		"uuid": newUUID(), "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty_produced": "10",
		"inputs":      []any{map[string]any{"variant_id": cloth, "qty": "10"}},
		"labour_cost": "300.00", "paid_from": "cash",
		"produced_on": "2026-08-16",
	})
	secondUnit, _ := decodeJSONFrom(t, second)["unit_cost"].(string)

	// 200 cloth + 100 labour over ten is 30; 200 + 300 over ten is 50.
	if !amountsEqual(firstUnit, "30.00") {
		t.Errorf("the first batch costs %s a unit, want 30.00", firstUnit)
	}
	if !amountsEqual(secondUnit, "50.00") {
		t.Errorf("the second batch costs %s a unit, want 50.00", secondUnit)
	}
	assertTrialBalanceBalances(t, h, f)
	assertInventoryTiesToTheLedger(t, h, f)
}

// One business cannot produce into another's stock.
func TestProductionCannotCrossCompanies(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	resp := recordProduction(t, h, mine, map[string]any{
		"uuid": newUUID(), "variant_id": theirs.variantID.String(),
		"warehouse_id": theirs.warehouseID.String(), "qty_produced": "1",
		"labour_cost": "10.00", "paid_from": "cash",
	})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("a batch produced into another company's stock")
	}
}

// assertInventoryTiesToTheLedger holds C13's invariant: the stock valuation and
// the Inventory control account agree to the cent.
//
// The check that makes production costing trustworthy. A batch that valued its
// output differently from what it posted would drift these apart, and the drift
// is what an auditor finds a year later.
func assertInventoryTiesToTheLedger(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	var diff decimal.Decimal
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(),
				`SELECT inventory_gl_difference($1)`, f.companyID).Scan(&diff)
		}); err != nil {
		t.Fatalf("read the tie-out: %v", err)
	}
	if !diff.IsZero() {
		t.Errorf("the stock valuation and the Inventory account are out by %s",
			diff)
	}
}
