//go:build integration

// Sending goods back to a supplier (B5).
//
//	"Purchase Return (to Supplier): for defective/excess stock —
//	 auto-generates a Debit Note and instantly deducts inventory."
//
// Two properties carry the weight, and they pull in opposite directions.
//
// The supplier is claimed the price they BILLED, because that is the document
// they will argue with. The stock leaves at what the VALUATION says it was
// worth, because that is what the shelf was carrying. Those are different
// numbers whenever landed cost was added on receipt or the shop has bought the
// same item since at another price, and a return that forced them together
// would either claim the wrong amount or part the stock report from the balance
// sheet.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// receiveAndBill puts stock on a shelf at a known cost and invoices it, which
// is the state a purchase return needs to exist at all.
func receiveAndBill(
	t *testing.T, h *harness, f *buyingFixture, qty, cost string,
) (billID, billLineID string) {
	t.Helper()

	poID, poLineID := raiseOrder(t, h, f, qty, cost)

	received := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{
				"po_line_id": poLineID, "qty_received": qty, "qty_rejected": "0",
			}},
		})
	if received.StatusCode != 201 {
		t.Fatalf("receipt: %s", readBody(t, received))
	}

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-RET-" + qty,
			"lines": []map[string]any{{
				"po_line_id": poLineID, "variant_id": f.variantID.String(),
				"description": "Abaya", "qty": qty, "unit_cost": cost,
			}},
		})
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, billed))
	}
	body := decodeJSON(t, billed)
	billID, _ = body["id"].(string)

	lines := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID+"/returnable"), f.token, nil))
	rows, _ := lines["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("the bill reported no returnable lines")
	}
	row, _ := rows[0].(map[string]any)
	billLineID, _ = row["bill_line_id"].(string)
	return billID, billLineID
}

// The whole of it: stock out, payable down, debit note raised.
func TestReturningGoodsTakesTheStockOutAndReducesWhatIsOwed(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	billID, billLineID := receiveAndBill(t, h, f, "10", "100.00")

	before := onHand(t, h, f)

	sent := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID,
			"warehouse_id": f.warehouseID,
			"reason":       "Three arrived with the stitching split",
			"lines":        []map[string]any{{"bill_line_id": billLineID, "qty": "3"}},
		})
	if sent.StatusCode != http.StatusCreated {
		t.Fatalf("return: %s", readBody(t, sent))
	}
	body := decodeJSON(t, sent)

	if after := onHand(t, h, f); !before.Sub(after).Equal(decimal.RequireFromString("3")) {
		t.Errorf("stock went from %s to %s; three units went back", before, after)
	}

	// The claim is the BILL's price and the BILL's tax: 3 x 100 plus 15%.
	if got, _ := body["subtotal_net"].(string); got != "300.00" {
		t.Errorf("the claim is %s, want 300.00 — the price the supplier billed", got)
	}
	if got, _ := body["tax_total"].(string); got != "45.00" {
		t.Errorf("the tax claimed back is %s, want 45.00", got)
	}
	if got, _ := body["total_inclusive"].(string); got != "345.00" {
		t.Errorf("the debit note comes to %s, want 345.00", got)
	}

	// The stock left at whatever the VALUATION held, which is a different
	// question and answered by the company's costing method rather than by the
	// invoice. Under FIFO a shop that already had cheaper stock of the same
	// item sends back the oldest layer, so this is 240.00 and not 300.00 — the
	// units on the lorry are the ones the invoice named, and the units the
	// books release are the ones the method says.
	//
	// Asserted as a RELATIONSHIP rather than a figure, because pinning the
	// number here would be pinning the fixture's opening stock, and the
	// property is that the two are tracked apart and reconciled by variance.
	claim := decimal.RequireFromString(mustString(body["subtotal_net"]))
	stockOut := decimal.RequireFromString(mustString(body["stock_value"]))
	variance := decimal.RequireFromString(mustString(body["variance"]))
	if !claim.Sub(stockOut).Equal(variance) {
		t.Errorf("claim %s less stock %s is not the reported variance %s",
			claim, stockOut, variance)
	}
	if !stockOut.IsPositive() {
		t.Errorf("stock left at %s; the shelf was carrying these units", stockOut)
	}

	// What the supplier owes back comes off the bill straight away, rather
	// than waiting for their credit note to arrive.
	bill := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID), f.token, nil))
	if got, _ := bill["outstanding"].(string); !amountsEqual(got, "805.00") {
		t.Errorf("outstanding is %s, want 805.00 (1150 billed less a 345 claim)", got)
	}

	// And it is recorded as a CREDIT, not as money paid. Writing it into
	// `amount_paid` told the supplier portal and the ageing report that goods
	// taken back had been paid for, and on a bill already settled it pushed the
	// balance below zero -- which the payment screen then offered as something
	// to settle. Found by running the verification suite: "A payment of
	// nothing is not a payment."
	if got, _ := bill["amount_paid"].(string); !amountsEqual(got, "0") {
		t.Errorf("amount_paid is %s; nothing has been paid on this bill", got)
	}
	if got, _ := bill["amount_credited"].(string); !amountsEqual(got, "345.00") {
		t.Errorf("amount_credited is %s, want 345.00", got)
	}

	// And the books balance, which is the property a stock movement and a
	// journal entry written by two different packages can quietly break.
	var tbDiff float64
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT trial_balance_difference($1)`, f.companyID).Scan(&tbDiff)
	}); err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if tbDiff != 0 {
		t.Errorf("the trial balance is out by %v after a purchase return", tbDiff)
	}
}

// Cumulative, per line. A clerk raising two returns for the same case would
// claim twice from the supplier and take the stock out twice.
func TestGoodsCannotBeSentBackTwice(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	billID, billLineID := receiveAndBill(t, h, f, "10", "100.00")

	first := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID,
			"warehouse_id": f.warehouseID, "reason": "Damaged",
			"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "6"}},
		})
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first return: %s", readBody(t, first))
	}

	// Four are left, so five is refused — and the refusal says how many.
	second := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID,
			"warehouse_id": f.warehouseID, "reason": "Damaged",
			"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "5"}},
		})
	if second.StatusCode == http.StatusCreated {
		t.Fatal("eleven of ten units could be sent back")
	}
	if body := readBody(t, second); !strings.Contains(body, "4") {
		t.Errorf("the refusal does not say how many are left: %s", body)
	}

	// Four exactly is fine, and then nothing is.
	if resp := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID,
			"warehouse_id": f.warehouseID, "reason": "Damaged",
			"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "4"}},
		}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("the last four could not be sent back: %s", readBody(t, resp))
	}

	left := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID+"/returnable"), f.token, nil))
	rows, _ := left["data"].([]any)
	row, _ := rows[0].(map[string]any)
	if got, _ := row["qty_returnable"].(string); !amountsEqual(got, "0") {
		t.Errorf("%s is still returnable after all ten went back", got)
	}
}

// A retry claims once, and says what it said the first time.
func TestRetryingAReturnClaimsOnce(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	billID, billLineID := receiveAndBill(t, h, f, "10", "100.00")

	body := map[string]any{
		"uuid": newUUID(), "bill_id": billID,
		"warehouse_id": f.warehouseID, "reason": "Damaged in transit",
		"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "2"}},
	}

	first := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("return: %s", readBody(t, first))
	}
	firstBody := decodeJSON(t, first)

	before := onHand(t, h, f)

	second := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token, body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("retry returned %d, want 200: %s",
			second.StatusCode, readBody(t, second))
	}
	secondBody := decodeJSON(t, second)

	if after := onHand(t, h, f); !before.Equal(after) {
		t.Errorf("the retry took another %s out of stock", before.Sub(after))
	}

	// And it ANSWERS what it answered. Returning the id with zeros is the
	// mistake the exchange replay made; a clerk whose first response was lost
	// needs the number to put on the paperwork.
	for _, field := range []string{"return_no", "total_inclusive", "stock_value"} {
		if firstBody[field] != secondBody[field] {
			t.Errorf("the replayed return's %s is %v, was %v",
				field, secondBody[field], firstBody[field])
		}
	}
	if already, _ := secondBody["already_returned"].(bool); !already {
		t.Error("the replay did not say it was one")
	}
}

// C14 asks a customer return for a reason and this asks the same, for the same
// reason: an unexplained return is how the value of a pallet goes missing
// between a clerk and a driver.
func TestAReturnNeedsAReason(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	billID, billLineID := receiveAndBill(t, h, f, "10", "100.00")

	resp := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID,
			"warehouse_id": f.warehouseID, "reason": "",
			"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "1"}},
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a return with no reason returned %d, want 400", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, "reason") {
		t.Errorf("the refusal does not name the field: %s", body)
	}
}

// You cannot send back what the shelf does not hold.
//
// `inventory.Consume` REPORTS a shortfall rather than refusing one, because
// whether stock may go below zero is the company's policy. Skipping that check
// was a real bug: a return raised against a location holding none of the item
// took nothing out, valued the goods at zero, and still claimed the full amount
// from the supplier — posting the whole claim to variance while reading, on the
// screen, as a successful return.
func TestGoodsThatAreNotOnTheShelfCannotGoBack(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	billID, billLineID := receiveAndBill(t, h, f, "10", "100.00")

	// A second location, holding none of it.
	var otherID string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO warehouse (tenant_id, company_id, code, name, kind)
			VALUES ($1, $2, 'EMPTY-WH', 'Empty Room', 'store_room')
			RETURNING id::text`, f.tenantID, f.companyID).Scan(&otherID)
	}); err != nil {
		t.Fatalf("second warehouse: %v", err)
	}

	resp := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID,
			"warehouse_id": otherID, "reason": "Damaged",
			"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "1"}},
		})
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("goods went back from a room that never held them")
	}
	if body := readBody(t, resp); !strings.Contains(body, "fewer") {
		t.Errorf("the refusal does not say the shelf is short: %s", body)
	}

	// And with two locations and neither named, the return says so rather than
	// guessing — the same rule a sale follows.
	silent := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID, "reason": "Damaged",
			"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "1"}},
		})
	if silent.StatusCode != http.StatusBadRequest {
		t.Errorf("a return with two locations and neither named returned %d",
			silent.StatusCode)
	}
}

// Sending goods back is not receiving them.
//
// Taking a delivery in is a warehouse act; sending one back reduces what the
// business owes and produces a document the supplier will argue with. A
// storeman who may unload a lorry has no business deciding what the shop claims
// back from the people who sent it.
func TestReturningGoodsNeedsItsOwnPermission(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	found := false
	for _, rt := range (&Server{}).Routes() {
		if rt.Method != http.MethodPost ||
			rt.Pattern != "/api/v1/purchasing/returns" {
			continue
		}
		found = true
		if rt.Permission != "purchasing.return_goods" {
			t.Errorf("returns are gated on %q, want purchasing.return_goods",
				rt.Permission)
		}
	}
	if !found {
		t.Fatal("POST /api/v1/purchasing/returns is not registered")
	}

	// A store manager receives and does not return: 0032 gives them
	// receive_goods and not record_bill, and a return is a claim against a
	// bill they cannot see.
	var has bool
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT EXISTS (
			  SELECT 1 FROM role_permission rp
			  JOIN role r ON r.id = rp.role_id
			  WHERE r.key = 'store_manager'
			    AND rp.permission = 'purchasing.return_goods')`).Scan(&has)
	}); err != nil {
		t.Fatalf("permission lookup: %v", err)
	}
	if has {
		t.Error("the store manager can send goods back, which is a claim " +
			"against a bill they cannot read")
	}
}

// The claim and the stock are two figures, and the gap between them is real.
//
// Landed cost is added to the layers on receipt (0034), so stock received with
// freight on it is carrying more than the invoice line says. Sending it back
// claims the invoice price and takes the higher cost off the shelf; the
// difference is a loss and belongs in cost_variance rather than being hidden in
// either of the other two.
func TestAReturnBooksTheGapBetweenTheClaimAndTheCost(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, poLineID := raiseOrder(t, h, f, "10", "100.00")

	// Freight on the delivery, which lands on the cost layers.
	received := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"landed_cost": "200.00", "landed_cost_basis": "quantity",
			"lines": []map[string]any{{
				"po_line_id": poLineID, "qty_received": "10", "qty_rejected": "0",
			}},
		})
	if received.StatusCode != 201 {
		t.Fatalf("receipt: %s", readBody(t, received))
	}

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-FREIGHT",
			"lines": []map[string]any{{
				"po_line_id": poLineID, "variant_id": f.variantID.String(),
				"description": "Abaya", "qty": "10", "unit_cost": "100.00",
			}},
		})
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, billed))
	}
	billID, _ := decodeJSON(t, billed)["id"].(string)

	rows, _ := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID+"/returnable"),
		f.token, nil))["data"].([]any)
	row, _ := rows[0].(map[string]any)
	billLineID, _ := row["bill_line_id"].(string)

	sent := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID,
			"warehouse_id": f.warehouseID, "reason": "Faulty batch",
			"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "1"}},
		})
	if sent.StatusCode != http.StatusCreated {
		t.Fatalf("return: %s", readBody(t, sent))
	}
	body := decodeJSON(t, sent)

	claim, _ := body["subtotal_net"].(string)
	stock, _ := body["stock_value"].(string)
	if amountsEqual(stock, claim) {
		t.Errorf("stock left at %s and the claim was %s; freight should have "+
			"made them differ", stock, claim)
	}

	// Whatever the gap is, the books still balance — which is the assertion
	// that proves the variance line carried it rather than nothing did.
	var tbDiff float64
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT trial_balance_difference($1)`, f.companyID).Scan(&tbDiff)
	}); err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if tbDiff != 0 {
		t.Errorf("the trial balance is out by %v when the claim and the cost "+
			"differ", tbDiff)
	}
}

// A bill that was PAID and then partly returned does not go below zero.
//
// The supplier now owes the shop money, which is a debit balance on the
// supplier rather than a negative payable on one invoice. The bill itself holds
// nothing further, and the journal carries the rest.
func TestAPaidBillThatIsPartlyReturnedIsNotOwedBackwards(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	billID, billLineID := receiveAndBill(t, h, f, "10", "100.00")

	bill := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID), f.token, nil))
	owed, _ := bill["outstanding"].(string)

	if paid := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "method": "bank",
			"allocations": []map[string]any{{"bill_id": billID, "amount": owed}},
		}); paid.StatusCode != 201 {
		t.Fatalf("payment: %s", readBody(t, paid))
	}

	if sent := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID,
			"warehouse_id": f.warehouseID, "reason": "Found faulty after payment",
			"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "2"}},
		}); sent.StatusCode != http.StatusCreated {
		t.Fatalf("return: %s", readBody(t, sent))
	}

	after := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID), f.token, nil))
	got, _ := after["outstanding"].(string)
	if decimal.RequireFromString(got).IsNegative() {
		t.Errorf("outstanding is %s; a bill cannot be owed backwards", got)
	}
	if !amountsEqual(got, "0") {
		t.Errorf("outstanding is %s, want 0 on a bill paid in full", got)
	}
}

// A raised return is evidence. It cannot be edited or deleted.
func TestARaisedReturnIsFrozen(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	billID, billLineID := receiveAndBill(t, h, f, "10", "100.00")

	sent := h.do(t, "POST", f.path("/api/v1/purchasing/returns"), f.token,
		map[string]any{
			"uuid": newUUID(), "bill_id": billID,
			"warehouse_id": f.warehouseID, "reason": "Damaged",
			"lines": []map[string]any{{"bill_line_id": billLineID, "qty": "1"}},
		})
	returnID, _ := decodeJSON(t, sent)["id"].(string)

	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE purchase_return SET reason = 'something else' WHERE id = $1`,
			returnID)
		return e
	})
	if err == nil {
		t.Error("a raised return could be edited; the goods have already gone")
	}

	err = h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`DELETE FROM purchase_return WHERE id = $1`, returnID)
		return e
	})
	if err == nil {
		t.Error("a raised return could be deleted")
	}
}

var _ = pgx.ErrNoRows
