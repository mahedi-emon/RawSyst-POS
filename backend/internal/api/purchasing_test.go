//go:build integration

package api

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// Purchasing, end to end.
//
// The properties that matter are the ones B5 and B5.2 name: a purchase order
// alone never inflates inventory, the three-way match catches a supplier
// billing for goods they did not send, and a blocked bill cannot be paid until
// somebody puts their name to accepting the discrepancy.

// buyingFixture is a company that can actually buy something: a chart of
// accounts to post to, a supplier to buy from, and a warehouse to receive into.
type buyingFixture struct {
	*shopFixture
	supplierID  string
	warehouseID string
}

func seedBuying(t *testing.T, h *harness) *buyingFixture {
	t.Helper()
	f := h.seedShop(t, "owner")
	ctx := t.Context()

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return provisioning.SeedChartOfAccounts(ctx, tx, f.tenantID, f.companyID)
	}); err != nil {
		t.Fatalf("seed chart: %v", err)
	}

	created := h.do(t, "POST",
		"/api/v1/purchasing/suppliers?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"code": "ACME", "legal_name": "Acme Textiles",
			"payment_terms_days": 30,
		})
	if created.StatusCode != 201 {
		t.Fatalf("supplier: %s", readBody(t, created))
	}
	supplierID, _ := decodeJSON(t, created)["id"].(string)

	var warehouseID string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id::text FROM warehouse WHERE company_id = $1 LIMIT 1`,
			f.companyID).Scan(&warehouseID)
	}); err != nil {
		t.Fatalf("warehouse: %v", err)
	}

	return &buyingFixture{shopFixture: f, supplierID: supplierID, warehouseID: warehouseID}
}

func (b *buyingFixture) path(base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + b.companyID.String()
}

// raiseOrder creates and issues an order for qty at unitCost, returning the id
// and the first line's id.
func raiseOrder(t *testing.T, h *harness, f *buyingFixture, qty, cost string) (string, string) {
	t.Helper()

	created := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(), "description": "Abaya",
				"qty": qty, "unit_cost": cost, "tax_rate": "0.15",
			}},
		})
	if created.StatusCode != 201 {
		t.Fatalf("order: %s", readBody(t, created))
	}
	body := decodeJSON(t, created)
	poID, _ := body["id"].(string)

	lines, _ := body["lines"].([]any)
	line, _ := lines[0].(map[string]any)
	lineID, _ := line["id"].(string)

	issued := h.do(t, "POST",
		f.path("/api/v1/purchasing/orders/"+poID+"/issue"), f.token, nil)
	if issued.StatusCode != 200 {
		t.Fatalf("issue: %s", readBody(t, issued))
	}
	return poID, lineID
}

func onHand(t *testing.T, h *harness, f *buyingFixture) decimal.Decimal {
	t.Helper()
	var qty decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT coalesce(sum(stock_on_hand($1, w.id)), 0)
			 FROM warehouse w WHERE w.company_id = $2`,
			f.variantID, f.companyID).Scan(&qty)
	}); err != nil {
		t.Fatalf("stock: %v", err)
	}
	return qty
}

// --- B5's central rule ---------------------------------------------------

// "Only GRN increases stock — a PO alone never inflates inventory."
func TestAPurchaseOrderDoesNotTouchStock(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	before := onHand(t, h, f)
	raiseOrder(t, h, f, "10", "100.00")
	after := onHand(t, h, f)

	if !before.Equal(after) {
		t.Errorf("stock moved from %s to %s on an order alone; B5 forbids it",
			before, after)
	}
}

func TestReceivingGoodsIncreasesStockAndValuesIt(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	before := onHand(t, h, f)

	resp := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{
				"po_line_id": lineID, "qty_received": "10",
			}},
		})
	if resp.StatusCode != 201 {
		t.Fatalf("receive: %s", readBody(t, resp))
	}

	after := onHand(t, h, f)
	if !after.Sub(before).Equal(decimal.RequireFromString("10")) {
		t.Errorf("stock went from %s to %s, want 10 more", before, after)
	}

	// And it is valued at the agreed cost, through the same costing engine
	// every other receipt uses.
	var valuation decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT inventory_valuation($1)`, f.companyID).Scan(&valuation)
	}); err != nil {
		t.Fatalf("valuation: %v", err)
	}
	if valuation.LessThan(decimal.RequireFromString("1000")) {
		t.Errorf("valuation is %s; ten at 100 should have added 1000", valuation)
	}
}

// Rejected goods arrived and went straight back. Recorded as having come, but
// never put on the shelf.
func TestRejectedGoodsNeverEnterStock(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	before := onHand(t, h, f)
	resp := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{
				"po_line_id": lineID, "qty_received": "10",
				"qty_rejected": "3", "reject_reason": "water damaged",
			}},
		})
	if resp.StatusCode != 201 {
		t.Fatalf("receive: %s", readBody(t, resp))
	}

	after := onHand(t, h, f)
	if !after.Sub(before).Equal(decimal.RequireFromString("7")) {
		t.Errorf("stock rose by %s; three of the ten were rejected so seven "+
			"should have landed", after.Sub(before))
	}
}

// A draft has been sent to nobody, so nothing can have arrived against it.
func TestCannotReceiveAgainstADraftOrder(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	created := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(), "description": "Abaya",
				"qty": "5", "unit_cost": "100.00",
			}},
		})
	body := decodeJSON(t, created)
	poID, _ := body["id"].(string)
	lines, _ := body["lines"].([]any)
	line, _ := lines[0].(map[string]any)

	resp := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{
				"po_line_id": line["id"], "qty_received": "5",
			}},
		})
	if resp.StatusCode != 409 {
		t.Errorf("receiving against a draft returned %d, want 409", resp.StatusCode)
	}
}

// A retry after a network failure must not put the same delivery into stock
// twice.
func TestReceivingIsIdempotent(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	delivery := map[string]any{
		"uuid": newUUID(), "po_id": poID,
		"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
	}

	first := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token, delivery)
	if first.StatusCode != 201 {
		t.Fatalf("first receive: %s", readBody(t, first))
	}
	afterFirst := onHand(t, h, f)

	second := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token, delivery)
	if second.StatusCode != 200 {
		t.Errorf("a repeated delivery returned %d, want 200 with the original",
			second.StatusCode)
	}
	if flagged, _ := decodeJSON(t, second)["already_received"].(bool); !flagged {
		t.Error("the retry was not reported as already received")
	}

	if after := onHand(t, h, f); !after.Equal(afterFirst) {
		t.Errorf("stock went from %s to %s on a retry — the delivery was "+
			"received twice", afterFirst, after)
	}
}

// --- The three-way match -------------------------------------------------

func TestAMatchingBillPostsToThePayablesLedger(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		})

	resp := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-9001",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	if resp.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, resp))
	}

	bill := decodeJSON(t, resp)
	if bill["status"] != "matched" {
		t.Errorf("a bill agreeing with the order and the receipt is %v, want matched",
			bill["status"])
	}
	if posted, _ := bill["posted"].(bool); !posted {
		t.Error("a matched bill was not posted to the ledger")
	}
	if !amountsEqual(bill["total_inclusive"].(string), "1150.00") {
		t.Errorf("total is %v, want 1150.00", bill["total_inclusive"])
	}
}

// The case B5.2 exists to catch: a supplier ships 8 and bills for 10.
func TestBillingForMoreThanArrivedIsBlocked(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	// Only eight turned up.
	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "8"}},
		})

	resp := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-9002",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	if resp.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, resp))
	}

	bill := decodeJSON(t, resp)
	if bill["status"] != "blocked" {
		t.Errorf("billing for ten against eight received is %v, want blocked",
			bill["status"])
	}
	// Recorded but NOT posted: the liability waits for somebody to accept it.
	if posted, _ := bill["posted"].(bool); posted {
		t.Error("a blocked bill was posted to the ledger anyway")
	}

	// And the evidence names the discrepancy.
	match, _ := bill["match"].([]any)
	var found bool
	for _, raw := range match {
		m, _ := raw.(map[string]any)
		if m["dimension"] == "qty" && m["outcome"] == "breach" {
			found = true
			if m["received"] != "8.0000" && m["received"] != "8" {
				t.Errorf("the match recorded %v received, want 8", m["received"])
			}
		}
	}
	if !found {
		t.Error("no quantity breach recorded; the evidence cannot be audited")
	}
}

// Being billed for LESS than arrived is in the shop's favour and must not
// block a payment over it.
func TestBillingForLessThanArrivedPasses(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		})

	resp := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-9003",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "9", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})

	if bill := decodeJSON(t, resp); bill["status"] != "matched" {
		t.Errorf("being undercharged is %v; it is in the shop's favour and "+
			"should not block anything", bill["status"])
	}
}

// A price rise inside the tolerance passes; one beyond it blocks.
func TestPriceToleranceIsBothPercentAndAmount(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	// Default tolerance: 2% or 50, whichever is larger. On a unit cost of 100
	// that is 50, so 140 is inside it and 160 is not.
	for _, c := range []struct {
		ref, billed, want string
	}{
		{"INV-T1", "140.00", "matched"},
		{"INV-T2", "160.00", "blocked"},
	} {
		poID, lineID := raiseOrder(t, h, f, "1", "100.00")
		h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
			map[string]any{
				"uuid": newUUID(), "po_id": poID,
				"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "1"}},
			})

		resp := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
			map[string]any{
				"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
				"supplier_ref": c.ref,
				"lines": []map[string]any{{
					"po_line_id": lineID, "description": "Abaya",
					"qty": "1", "unit_cost": c.billed, "tax_rate": "0.15",
				}},
			})
		if got := decodeJSON(t, resp)["status"]; got != c.want {
			t.Errorf("a unit cost of %s against 100 is %v, want %s",
				c.billed, got, c.want)
		}
	}
}

// The same supplier invoice number cannot be entered twice. Paying one invoice
// two times is the commonest way a shop loses money to its own paperwork.
func TestTheSameSupplierInvoiceCannotBeEnteredTwice(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	body := func() map[string]any {
		return map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID,
			"supplier_ref": "DUPLICATE-1",
			"lines": []map[string]any{{
				"description": "Consulting", "qty": "1", "unit_cost": "500.00",
			}},
		}
	}

	if first := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token, body()); first.StatusCode != 201 {
		t.Fatalf("first bill: %s", readBody(t, first))
	}
	second := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token, body())
	if second.StatusCode != 409 {
		t.Errorf("the same invoice number was accepted twice: %d", second.StatusCode)
	}
}

// --- Payment -------------------------------------------------------------

// The control has to bite, or it is decoration.
func TestABlockedBillCannotBePaid(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "5"}},
		})

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-BLOCK",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	bill := decodeJSON(t, billed)
	if bill["status"] != "blocked" {
		t.Fatalf("expected a blocked bill, got %v", bill["status"])
	}
	billID, _ := bill["id"].(string)

	paid := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "method": "bank",
			"allocations": []map[string]any{{"bill_id": billID, "amount": "100.00"}},
		})
	if paid.StatusCode != 409 {
		t.Errorf("a blocked bill was paid: %d %s", paid.StatusCode, readBody(t, paid))
	}
}

// Approving takes a reason and a name, and only then does the liability post.
func TestApprovingABlockedBillPostsItAndRecordsWho(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "9"}},
		})
	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-APPROVE",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	billID, _ := decodeJSON(t, billed)["id"].(string)

	// A reason is not optional. The control is worthless if an override leaves
	// nothing behind.
	blank := h.do(t, "POST",
		f.path("/api/v1/purchasing/bills/"+billID+"/approve"), f.token,
		map[string]any{"reason": ""})
	if blank.StatusCode != 400 {
		t.Errorf("approval without a reason returned %d, want 400", blank.StatusCode)
	}

	ok := h.do(t, "POST",
		f.path("/api/v1/purchasing/bills/"+billID+"/approve"), f.token,
		map[string]any{"reason": "short delivery agreed by phone with Acme"})
	if ok.StatusCode != 200 {
		t.Fatalf("approve: %s", readBody(t, ok))
	}

	approved := decodeJSON(t, ok)
	if approved["status"] != "approved" {
		t.Errorf("status is %v after approval, want approved", approved["status"])
	}
	if posted, _ := approved["posted"].(bool); !posted {
		t.Error("an approved bill was still not posted to the ledger")
	}

	// And now it can be paid.
	paid := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "method": "bank",
			"allocations": []map[string]any{{"bill_id": billID, "amount": "100.00"}},
		})
	if paid.StatusCode != 201 {
		t.Errorf("an approved bill could not be paid: %s", readBody(t, paid))
	}
}

func TestPayingSettlesTheBillAndReducesWhatIsOwed(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		})
	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-PAY",
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	billID, _ := decodeJSON(t, billed)["id"].(string)

	// Part payment first: the bill stays open with the balance reduced.
	part := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "method": "bank",
			"allocations": []map[string]any{{"bill_id": billID, "amount": "150.00"}},
		})
	if part.StatusCode != 201 {
		t.Fatalf("part payment: %s", readBody(t, part))
	}

	after := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID), f.token, nil))
	if !amountsEqual(after["outstanding"].(string), "1000.00") {
		t.Errorf("outstanding is %v after paying 150 of 1150, want 1000.00",
			after["outstanding"])
	}
	if after["status"] == "paid" {
		t.Error("a part-paid bill is marked paid")
	}

	// Overpaying the remainder is refused rather than silently creating a
	// credit nobody asked for.
	over := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "method": "bank",
			"allocations": []map[string]any{{"bill_id": billID, "amount": "5000.00"}},
		})
	if over.StatusCode != 400 {
		t.Errorf("overpaying returned %d, want 400", over.StatusCode)
	}

	// Settling the rest closes it.
	rest := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "method": "bank",
			"allocations": []map[string]any{{"bill_id": billID, "amount": "1000.00"}},
		})
	if rest.StatusCode != 201 {
		t.Fatalf("final payment: %s", readBody(t, rest))
	}

	settled := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID), f.token, nil))
	if settled["status"] != "paid" {
		t.Errorf("status is %v after full settlement, want paid", settled["status"])
	}
}

func TestPayingIsIdempotent(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID,
			"supplier_ref": "INV-IDEM",
			"lines": []map[string]any{{
				"description": "Rent", "qty": "1", "unit_cost": "1000.00",
			}},
		})
	billID, _ := decodeJSON(t, billed)["id"].(string)

	payment := map[string]any{
		"uuid": newUUID(), "supplier_id": f.supplierID, "method": "bank",
		"allocations": []map[string]any{{"bill_id": billID, "amount": "400.00"}},
	}

	if first := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token, payment); first.StatusCode != 201 {
		t.Fatalf("first payment: %s", readBody(t, first))
	}
	second := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token, payment)
	if second.StatusCode != 200 {
		t.Errorf("a repeated payment returned %d, want 200", second.StatusCode)
	}

	after := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID), f.token, nil))
	if !amountsEqual(after["outstanding"].(string), "600.00") {
		t.Errorf("outstanding is %v; the retry paid twice", after["outstanding"])
	}
}

// --- Ageing --------------------------------------------------------------

// Aged from the DUE date, not the bill date. A 30-day invoice raised today is
// not overdue, and saying it was would have a buyer chase a supplier owed
// nothing yet.
func TestAgeingMeasuresFromTheDueDate(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID,
			"supplier_ref": "INV-AGE", "bill_date": "2026-08-15",
			"lines": []map[string]any{{
				"description": "Stock", "qty": "1", "unit_cost": "1000.00",
			}},
		})

	// Terms are 30 days, so on 2026-08-20 it is not yet due.
	early := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/ageing?as_of=2026-08-20"), f.token, nil))
	rows, _ := early["rows"].([]any)
	if len(rows) == 0 {
		t.Fatal("the supplier does not appear in the ageing at all")
	}
	row, _ := rows[0].(map[string]any)
	if !amountsEqual(row["not_due"].(string), "1000.00") {
		t.Errorf("not_due is %v five days after a 30-day bill, want 1000.00",
			row["not_due"])
	}

	// Forty days past due lands in 31–60.
	late := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/ageing?as_of=2026-10-25"), f.token, nil))
	lateRows, _ := late["rows"].([]any)
	lateRow, _ := lateRows[0].(map[string]any)
	if !amountsEqual(lateRow["days_31_60"].(string), "1000.00") {
		t.Errorf("a bill 40 days past due is in %v, want the 31-60 bucket", lateRow)
	}
}

// --- RBAC and tenancy ----------------------------------------------------

// B5.2's separation of duties, enforced server-side. The person who records a
// bill must not necessarily be able to approve their own discrepancy.
func TestPurchasingRoutesAreGatedPerVerb(t *testing.T) {
	want := map[string]string{
		"/api/v1/purchasing/orders":                 "purchasing.create_order",
		"/api/v1/purchasing/orders/{poID}/issue":    "purchasing.issue_order",
		"/api/v1/purchasing/receipts":               "purchasing.receive_goods",
		"/api/v1/purchasing/bills":                  "purchasing.record_bill",
		"/api/v1/purchasing/bills/{billID}/approve": "purchasing.approve_bill",
		"/api/v1/purchasing/payments":               "purchasing.pay_supplier",
		"/api/v1/purchasing/suppliers":              "purchasing.manage_suppliers",
	}

	found := map[string]bool{}
	for _, rt := range (&Server{}).Routes() {
		if rt.Method != "POST" {
			continue
		}
		expected, watched := want[rt.Pattern]
		if !watched {
			continue
		}
		found[rt.Pattern] = true
		if rt.Permission != expected {
			t.Errorf("%s is gated on %q, want %q", rt.Pattern, rt.Permission, expected)
		}
	}
	for pattern := range want {
		if !found[pattern] {
			t.Errorf("%s is not registered", pattern)
		}
	}
}

// A cashier has no business anywhere near a cost document.
func TestCashierCannotReachPurchasing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	for _, path := range []string{
		"/api/v1/purchasing/suppliers",
		"/api/v1/purchasing/orders",
		"/api/v1/purchasing/bills",
	} {
		resp := h.do(t, "GET", path+"?company_id="+f.companyID.String(), f.token, nil)
		if resp.StatusCode != 403 {
			t.Errorf("a cashier got %d from %s, want 403", resp.StatusCode, path)
		}
	}
}

func TestPurchasingCannotCrossTenants(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := seedBuying(t, h)

	resp := h.do(t, "GET",
		"/api/v1/purchasing/orders?company_id="+theirs.companyID.String(),
		mine.token, nil)
	if resp.StatusCode == 200 {
		t.Fatal("an owner read another tenant's purchase orders")
	}
	if resp.StatusCode != 404 && resp.StatusCode != 403 {
		t.Errorf("got %d, want 404 or 403", resp.StatusCode)
	}
}

// Money crosses as a string, here as everywhere.
func TestPurchasingMoneyIsAlwaysAString(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	raiseOrder(t, h, f, "3", "99.99")

	body := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/orders"), f.token, nil))
	orders, _ := body["data"].([]any)
	order, _ := orders[0].(map[string]any)

	for _, field := range []string{"subtotal_net", "tax_total", "total_inclusive"} {
		if _, ok := order[field].(string); !ok {
			t.Errorf("%s came back as %T, not a string", field, order[field])
		}
	}
}
