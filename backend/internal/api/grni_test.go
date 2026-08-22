//go:build integration

package api

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// GRNI and landed cost.
//
// The invariant these exist to hold, from design 02 §6.6 quoting C13:
//
//	inventory_valuation(company) == Inventory control account balance
//
// Before this milestone the two diverged by the full value of every receipt for
// the whole window between a delivery arriving and the supplier invoicing it. A
// diagnostic measured 1600 against 600 on a single ten-unit receipt.

// tieOut is the invariant as a number, through the same SQL function the
// nightly job and a support engineer would use.
func tieOut(t *testing.T, h *harness, f *buyingFixture) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT inventory_gl_difference($1)`, f.companyID).Scan(&d)
	}); err != nil {
		t.Fatalf("tie-out: %v", err)
	}
	return d
}

// roleBalance is the net movement on the account a role points at.
//
// Takes the shop rather than the buying fixture because settlement needs it
// too and buyingFixture embeds shopFixture, so one helper serves both.
func roleBalance(t *testing.T, h *harness, f *shopFixture, role string) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM journal_line l
			JOIN account_role_map m ON m.account_id = l.account_id
			WHERE m.company_id = $1 AND m.role = $2`,
			f.companyID, role).Scan(&d)
	}); err != nil {
		t.Fatalf("%s balance: %v", role, err)
	}
	return d
}

// receiveWith records a delivery, optionally carrying freight and import VAT.
func receiveWith(
	t *testing.T, h *harness, f *buyingFixture,
	poID, lineID, qty, landedCost, importVAT string,
) {
	t.Helper()
	body := map[string]any{
		"uuid": newUUID(), "po_id": poID,
		"lines": []map[string]any{{"po_line_id": lineID, "qty_received": qty}},
	}
	if landedCost != "" {
		body["landed_cost"] = landedCost
	}
	if importVAT != "" {
		body["import_vat"] = importVAT
	}
	if resp := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token, body); resp.StatusCode != 201 {
		t.Fatalf("receive: %s", readBody(t, resp))
	}
}

// --- The invariant -------------------------------------------------------

// The whole point. It must hold at every step, not only at the end.
func TestTheTieOutHoldsThroughTheWholePurchaseCycle(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	if d := tieOut(t, h, f); !d.IsZero() {
		t.Fatalf("the tie-out was already broken before anything happened: %s", d)
	}

	poID, lineID := raiseOrder(t, h, f, "10", "100.00")
	if d := tieOut(t, h, f); !d.IsZero() {
		t.Errorf("raising an order broke the tie-out by %s", d)
	}

	// This is the step that used to break it, by 1000.
	receiveWith(t, h, f, poID, lineID, "10", "", "")
	if d := tieOut(t, h, f); !d.IsZero() {
		t.Errorf("receiving broke the tie-out by %s — the valuation is ahead of "+
			"the Inventory control account, which design 02 §6.6 forbids", d)
	}

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "TIE-1",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, billed))
	}
	if d := tieOut(t, h, f); !d.IsZero() {
		t.Errorf("billing broke the tie-out by %s", d)
	}
}

// The accrual is a real, reportable figure: what is on the shelves and not yet
// invoiced. It carries a balance between the two events and clears on the bill.
func TestTheAccrualHoldsTheValueUntilTheBillArrives(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	before := roleBalance(t, h, f.shopFixture, "grni")

	receiveWith(t, h, f, poID, lineID, "10", "", "")

	// A liability, so it is credit-normal and the signed balance goes DOWN by
	// what is owed.
	afterReceipt := roleBalance(t, h, f.shopFixture, "grni")
	if !before.Sub(afterReceipt).Equal(decimal.RequireFromString("1000")) {
		t.Errorf("the accrual moved by %s on a 1000 receipt", before.Sub(afterReceipt))
	}

	h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "ACC-1",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})

	if after := roleBalance(t, h, f.shopFixture, "grni"); !after.Equal(before) {
		t.Errorf("the accrual is %s after billing, want back to %s — it did not "+
			"clear on the invoice it belongs to", after, before)
	}
}

// A rejected line was never put on a shelf, so it accrues nothing.
func TestRejectedGoodsAccrueNothing(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	before := roleBalance(t, h, f.shopFixture, "grni")
	if resp := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{
				"po_line_id": lineID, "qty_received": "10", "qty_rejected": "4",
				"reject_reason": "water damaged",
			}},
		}); resp.StatusCode != 201 {
		t.Fatalf("receive: %s", readBody(t, resp))
	}

	// Six kept at 100.
	moved := before.Sub(roleBalance(t, h, f.shopFixture, "grni"))
	if !moved.Equal(decimal.RequireFromString("600")) {
		t.Errorf("the accrual moved by %s; six of ten were kept so it should be 600",
			moved)
	}
	if d := tieOut(t, h, f); !d.IsZero() {
		t.Errorf("a partly rejected delivery broke the tie-out by %s", d)
	}
}

// --- Landed cost ---------------------------------------------------------

// Freight belongs in the cost of the stock, so it raises the valuation and the
// accrual together and the tie-out still holds.
func TestFreightRaisesTheCostOfTheStock(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	valuationBefore := valuation(t, h, f)
	receiveWith(t, h, f, poID, lineID, "10", "50.00", "")

	// 1000 of goods plus 50 of freight.
	rose := valuation(t, h, f).Sub(valuationBefore)
	if !rose.Equal(decimal.RequireFromString("1050")) {
		t.Errorf("the valuation rose by %s; ten at 100 with 50 of freight is 1050",
			rose)
	}
	if d := tieOut(t, h, f); !d.IsZero() {
		t.Errorf("freight broke the tie-out by %s", d)
	}

	// And the unit cost carries it, because that is what a later sale's COGS
	// reads. 10-catalog-and-inventory.md: cost_layer.unit_cost INCLUDES the
	// allocation.
	var unitCost decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT unit_cost FROM grn_line WHERE po_line_id = $1`, lineID).
			Scan(&unitCost)
	}); err != nil {
		t.Fatalf("read unit cost: %v", err)
	}
	if !unitCost.Equal(decimal.RequireFromString("105")) {
		t.Errorf("unit cost is %s, want 105 — the freight was not carried into "+
			"the cost layer, so a sale would report a margin it never earned",
			unitCost)
	}
}

// E2.5: duty is inventory cost, import VAT is recoverable. Adding them together
// overstates stock and understates the reclaim.
func TestImportVATNeverEntersTheCostOfTheStock(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	valuationBefore := valuation(t, h, f)
	// 50 of duty, 150 of import VAT.
	receiveWith(t, h, f, poID, lineID, "10", "50.00", "150.00")

	rose := valuation(t, h, f).Sub(valuationBefore)
	if !rose.Equal(decimal.RequireFromString("1050")) {
		t.Errorf("the valuation rose by %s; the 150 of import VAT must NOT be in "+
			"the cost of the stock (E2.5), so it should be 1050", rose)
	}
	if d := tieOut(t, h, f); !d.IsZero() {
		t.Errorf("import VAT broke the tie-out by %s", d)
	}
}

// The remainder rule, for the sixth time in this codebase. Three lines sharing
// 500 by thirds is 166.67 each and 500.01 total, and a cost that does not add up
// to what was paid puts the tie-out out.
func TestFreightAllocationSumsToWhatWasPaid(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	created := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "description": "A",
					"qty": "1", "unit_cost": "100.00", "tax_rate": "0.15"},
				{"variant_id": f.variantID.String(), "description": "B",
					"qty": "1", "unit_cost": "100.00", "tax_rate": "0.15"},
				{"variant_id": f.variantID.String(), "description": "C",
					"qty": "1", "unit_cost": "100.00", "tax_rate": "0.15"},
			},
		})
	if created.StatusCode != 201 {
		t.Fatalf("order: %s", readBody(t, created))
	}
	body := decodeJSON(t, created)
	poID, _ := body["id"].(string)
	lines, _ := body["lines"].([]any)

	h.do(t, "POST", f.path("/api/v1/purchasing/orders/"+poID+"/issue"), f.token, nil)

	received := make([]map[string]any, 0, 3)
	for _, raw := range lines {
		line, _ := raw.(map[string]any)
		received = append(received, map[string]any{
			"po_line_id": line["id"], "qty_received": "1",
		})
	}

	valuationBefore := valuation(t, h, f)
	if resp := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"landed_cost": "500.00", "lines": received,
		}); resp.StatusCode != 201 {
		t.Fatalf("receive: %s", readBody(t, resp))
	}

	// 300 of goods plus exactly 500 of freight, not 500.01 and not 499.99.
	rose := valuation(t, h, f).Sub(valuationBefore)
	if !rose.Equal(decimal.RequireFromString("800")) {
		t.Errorf("the valuation rose by %s, want exactly 800 — the freight "+
			"allocation did not sum to what was paid", rose)
	}
	if d := tieOut(t, h, f); !d.IsZero() {
		t.Errorf("a three-way freight split broke the tie-out by %s", d)
	}

	var allocated decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(gl.landed_cost_alloc), 0)
			FROM grn_line gl
			JOIN goods_receipt g ON g.id = gl.grn_id
			WHERE g.company_id = $1`, f.companyID).Scan(&allocated)
	}); err != nil {
		t.Fatalf("read allocation: %v", err)
	}
	if !allocated.Equal(decimal.RequireFromString("500")) {
		t.Errorf("the stored allocation sums to %s, want 500", allocated)
	}
}

// The accrual is discharged from the RECEIPT's value, not the bill's. A
// supplier overcharging must not clear more accrual than was ever raised.
func TestAnOverchargingBillDoesNotOverClearTheAccrual(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	before := roleBalance(t, h, f.shopFixture, "grni")
	receiveWith(t, h, f, poID, lineID, "10", "", "")

	// Billed at 160 a unit against 100 agreed — a breach, so the bill is held
	// and not posted at all. The accrual must be untouched.
	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "OVER-1",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "160.00", "tax_rate": "0.15",
			}},
		})
	bill := decodeJSON(t, billed)
	if bill["status"] != "blocked" {
		t.Fatalf("expected the match to hold this bill, got %v", bill["status"])
	}

	held := roleBalance(t, h, f.shopFixture, "grni")
	if !before.Sub(held).Equal(decimal.RequireFromString("1000")) {
		t.Errorf("a blocked bill changed the accrual; it is %s and should still "+
			"hold the 1000 from the receipt", before.Sub(held))
	}

	// Approving it posts. The accrual clears by exactly what was received and
	// accrued — 1000 — and the 600 excess goes to inventory, which is where a
	// price the shop accepted belongs.
	billID, _ := bill["id"].(string)
	if resp := h.do(t, "POST", f.path("/api/v1/purchasing/bills/"+billID+"/approve"),
		f.token, map[string]any{"reason": "price rise accepted"}); resp.StatusCode != 200 {
		t.Fatalf("approve: %s", readBody(t, resp))
	}

	if after := roleBalance(t, h, f.shopFixture, "grni"); !after.Equal(before) {
		t.Errorf("the accrual is %s after approval, want back to %s — it cleared "+
			"by the wrong amount", after, before)
	}
}

// A bill with no receipt behind it — rent, a utility — has no accrual to clear
// and must keep posting through the original rule.
func TestADirectBillStillPostsWithoutAnAccrual(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	before := roleBalance(t, h, f.shopFixture, "grni")

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID,
			"supplier_ref": "RENT-1",
			"lines": []map[string]any{{
				"description": "Shop rent", "qty": "1", "unit_cost": "5000.00",
				"tax_rate": "0.15",
			}},
		})
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, billed))
	}
	if posted, _ := decodeJSON(t, billed)["posted"].(bool); !posted {
		t.Error("a direct bill was not posted")
	}
	if after := roleBalance(t, h, f.shopFixture, "grni"); !after.Equal(before) {
		t.Errorf("a bill with no receipt touched the accrual: %s to %s",
			before, after)
	}
}

// What the shop has received and not been invoiced for, as one number.
func TestTheOutstandingAccrualIsReportable(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	outstanding := func() decimal.Decimal {
		var d decimal.Decimal
		if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(t.Context(),
				`SELECT grn_accrual_outstanding($1)`, f.companyID).Scan(&d)
		}); err != nil {
			t.Fatalf("outstanding: %v", err)
		}
		return d
	}

	if d := outstanding(); !d.IsZero() {
		t.Fatalf("something is accrued before any delivery: %s", d)
	}

	receiveWith(t, h, f, poID, lineID, "10", "", "")
	if d := outstanding(); !d.Equal(decimal.RequireFromString("1000")) {
		t.Errorf("outstanding accrual is %s after a 1000 receipt", d)
	}

	h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "OUT-1",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	if d := outstanding(); !d.IsZero() {
		t.Errorf("outstanding accrual is %s after the bill, want zero", d)
	}
}

func valuation(t *testing.T, h *harness, f *buyingFixture) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT inventory_valuation($1)`, f.companyID).Scan(&d)
	}); err != nil {
		t.Fatalf("valuation: %v", err)
	}
	return d
}
