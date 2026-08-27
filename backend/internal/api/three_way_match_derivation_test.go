//go:build integration

package api

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

// The three-way match, re-derived from the design rather than from the code.
//
// docs/system-design/20-later-phases.md states the control in two parts:
//
//	"three_way_match | PO + GRN + Bill on qty, price, VAT, discount, total"
//	"Three-way match blocks payment beyond tolerance (configurable, e.g. 2% or
//	 SAR 50) and routes to an approver with the discrepancy highlighted."
//
// Two things follow that the existing tests never asked.
//
// First, "2% or SAR 50" is a tolerance on MONEY AT STAKE. SAR 50 is a sum the
// shop is willing not to argue about. Applied to a unit price it stops being a
// sum at all and becomes a rate: fifty riyals per unit, times however many
// units were ordered. On a thousand-unit line it forgives fifty thousand.
//
// Second, VAT and total are named dimensions. A supplier can agree a zero-rated
// line and bill it at fifteen percent without touching either quantity or unit
// price, and nothing compares the one field that changed.
//
// These tests state the arithmetic from the design and check the system against
// it, rather than checking that the match agrees with itself.

// billOf sends a supplier invoice with whatever lines it is given.
func billOf(t *testing.T, h *harness, f *buyingFixture,
	poID, ref string, lines []map[string]any) map[string]any {
	t.Helper()

	resp := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": ref, "lines": lines,
		})
	if resp.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

// orderLines raises and issues an order of several lines, returning the order
// id and each line's id in order.
func orderLines(t *testing.T, h *harness, f *buyingFixture,
	lines []map[string]any) (string, []string) {
	t.Helper()

	created := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": lines,
		})
	if created.StatusCode != 201 {
		t.Fatalf("order: %s", readBody(t, created))
	}
	body := decodeJSON(t, created)
	poID, _ := body["id"].(string)

	raw, _ := body["lines"].([]any)
	ids := make([]string, 0, len(raw))
	for _, r := range raw {
		line, _ := r.(map[string]any)
		id, _ := line["id"].(string)
		ids = append(ids, id)
	}

	issued := h.do(t, "POST",
		f.path("/api/v1/purchasing/orders/"+poID+"/issue"), f.token, nil)
	if issued.StatusCode != 200 {
		t.Fatalf("issue: %s", readBody(t, issued))
	}
	return poID, ids
}

// receiveAll takes delivery of every line in full.
func receiveAll(t *testing.T, h *harness, f *buyingFixture,
	poID string, lines []map[string]any) {
	t.Helper()

	resp := h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{"uuid": newUUID(), "po_id": poID, "lines": lines})
	if resp.StatusCode != 201 {
		t.Fatalf("receipt: %s", readBody(t, resp))
	}
	resp.Body.Close()
}

// dimensionsOf indexes a bill's recorded match by dimension.
func dimensionsOf(bill map[string]any) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	raw, _ := bill["match"].([]any)
	for _, r := range raw {
		m, _ := r.(map[string]any)
		d, _ := m["dimension"].(string)
		out[d] = append(out[d], m)
	}
	return out
}

// breachedOn reports whether any row of a dimension came back a breach.
func breachedOn(bill map[string]any, dimension string) bool {
	for _, m := range dimensionsOf(bill)[dimension] {
		if m["outcome"] == "breach" {
			return true
		}
	}
	return false
}

// THE DEFECT THIS FILE FOUND.
//
// A thousand abayas were ordered at SAR 10.00 — an order of SAR 10,000. All
// thousand arrived, so quantity is exactly right and cannot be the thing that
// catches this. The supplier then bills SAR 59.99 each: SAR 59,990 for goods
// worth SAR 10,000, an overcharge of SAR 49,990 and very nearly six times what
// was authorised.
//
// The unit price is out by SAR 49.99, which is under the SAR 50 tolerance, so
// the match passes it and the bill posts and can be paid.
func TestTheAbsoluteToleranceIsASumOfMoneyNotARatePerUnit(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "1000", "10.00")

	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "1000"},
	})

	bill := billOf(t, h, f, poID, "INV-TOL-1", []map[string]any{{
		"po_line_id": lineID, "description": "Abaya",
		"qty": "1000", "unit_cost": "59.99", "tax_rate": "0.15",
	}})

	if bill["status"] != "blocked" {
		t.Errorf("a bill of SAR 59,990 against an order of SAR 10,000 came back "+
			"%v, want blocked. The unit price is out by SAR 49.99, under the SAR "+
			"50 tolerance -- but SAR 50 is a sum the shop agreed not to argue "+
			"about, not fifty riyals per unit. Multiplied by the quantity it "+
			"forgave SAR 49,990.", bill["status"])
	}
	if posted, _ := bill["posted"].(bool); posted {
		t.Error("the bill was posted to the ledger, so the overcharge is now a " +
			"payable somebody can settle")
	}
	if !breachedOn(bill, "price") && !breachedOn(bill, "total") {
		t.Error("no price or total breach was recorded, so an approver would " +
			"see nothing to approve")
	}
}

// The tolerance must still forgive what it is for: a genuine rounding
// difference on a small order, which is the reason an absolute figure exists
// alongside the percentage.
func TestASmallRoundingDifferenceIsStillForgiven(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "3", "10.00")

	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "3"},
	})

	// Thirty riyals agreed, thirty-three billed. Two per cent of SAR 10.00 is
	// two hallalas and two per cent of SAR 30 is sixty, so the percentage
	// refuses this on its own: the absolute tolerance is the only thing that can
	// let it through, and letting a SAR 3 difference through on a SAR 30 order
	// is exactly its job. If the absolute figure is ever narrowed to nothing,
	// this is the test that notices.
	bill := billOf(t, h, f, poID, "INV-TOL-2", []map[string]any{{
		"po_line_id": lineID, "description": "Abaya",
		"qty": "3", "unit_cost": "11.00", "tax_rate": "0.15",
	}})

	if bill["status"] != "matched" {
		t.Errorf("a SAR 3 difference on a SAR 30 order came back %v, want "+
			"matched. Blocking payment over small change wastes a buyer's "+
			"afternoon, which is why the absolute tolerance exists at all.",
			bill["status"])
	}
}

// VAT TREATMENT, which the design names and the match never looked at.
//
// Imported goods are frequently agreed zero-rated inbound and sold standard
// rated — po_line carries the AGREED treatment for exactly this reason. A
// supplier who bills fifteen per cent on a line agreed at zero has changed
// neither the quantity nor the unit price, so both existing dimensions pass,
// and the shop pays SAR 1,500 of VAT it never agreed to.
func TestBillingVATOnAZeroRatedLineIsCaught(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	poID, ids := orderLines(t, h, f, []map[string]any{{
		"variant_id": f.variantID.String(), "description": "Imported abaya",
		"qty": "100", "unit_cost": "100.00",
		"tax_treatment": "zero_rated", "tax_rate": "0",
	}})
	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": ids[0], "qty_received": "100"},
	})

	// Same quantity, same unit price. Only the tax changed.
	bill := billOf(t, h, f, poID, "INV-VAT-1", []map[string]any{{
		"po_line_id": ids[0], "description": "Imported abaya",
		"qty": "100", "unit_cost": "100.00",
		"tax_treatment": "standard", "tax_rate": "0.15",
	}})

	if bill["status"] != "blocked" {
		t.Errorf("a bill charging 15%% VAT on a line agreed zero-rated came back "+
			"%v, want blocked. Quantity and unit price both agree exactly, so "+
			"the only two dimensions the match compared saw nothing wrong, and "+
			"SAR 1,500 of VAT nobody agreed to is now payable.", bill["status"])
	}
	if !breachedOn(bill, "tax") {
		t.Error("no tax breach was recorded. The design names VAT as one of the " +
			"compared dimensions and po_line carries the agreed treatment, so " +
			"there is something to compare against.")
	}
}

// The same in reverse is in the shop's favour and must not block anybody.
func TestBillingLessVATThanAgreedDoesNotBlockPayment(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	poID, ids := orderLines(t, h, f, []map[string]any{{
		"variant_id": f.variantID.String(), "description": "Abaya",
		"qty": "10", "unit_cost": "100.00",
		"tax_treatment": "standard", "tax_rate": "0.15",
	}})
	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": ids[0], "qty_received": "10"},
	})

	bill := billOf(t, h, f, poID, "INV-VAT-2", []map[string]any{{
		"po_line_id": ids[0], "description": "Abaya",
		"qty": "10", "unit_cost": "100.00",
		"tax_treatment": "zero_rated", "tax_rate": "0",
	}})

	if bill["status"] != "matched" {
		t.Errorf("a bill charging LESS tax than agreed came back %v, want "+
			"matched. The shop owes less, and blocking a payment over a "+
			"supplier's error in the shop's favour helps nobody.", bill["status"])
	}
}

// TOTAL VALUE, and why it is not redundant once price is compared per line.
//
// The absolute tolerance is granted once per line. Three lines each forgiven
// most of SAR 50 forgives most of SAR 150, and a supplier who knows the
// tolerance can spread an overcharge across lines so that no single one of them
// ever trips it.
//
// Worked by hand: three lines of 100 units at SAR 10.00 is SAR 1,000 each and
// SAR 3,000 agreed. Billed at SAR 10.45, each line is SAR 1,045 — out by SAR
// 45, inside the SAR 50 a line is allowed. The bill totals SAR 3,135, out by
// SAR 135, where the whole order is allowed max(2% of 3,000, 50) = SAR 60.
func TestAnOverchargeSpreadAcrossLinesIsCaughtOnTheTotal(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	line := func() map[string]any {
		return map[string]any{
			"variant_id": f.variantID.String(), "description": "Abaya",
			"qty": "100", "unit_cost": "10.00", "tax_rate": "0.15",
		}
	}
	poID, ids := orderLines(t, h, f, []map[string]any{line(), line(), line()})

	received := make([]map[string]any, 0, len(ids))
	billed := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		received = append(received,
			map[string]any{"po_line_id": id, "qty_received": "100"})
		billed = append(billed, map[string]any{
			"po_line_id": id, "description": "Abaya",
			"qty": "100", "unit_cost": "10.45", "tax_rate": "0.15",
		})
	}
	receiveAll(t, h, f, poID, received)

	bill := billOf(t, h, f, poID, "INV-TOT-1", billed)

	if bill["status"] != "blocked" {
		t.Errorf("a bill of SAR 3,135 against SAR 3,000 agreed came back %v, "+
			"want blocked. No single line is out by more than the SAR 50 a line "+
			"is allowed, which is exactly how an overcharge gets spread across "+
			"lines. The design names total as a compared dimension for this.",
			bill["status"])
	}
	if !breachedOn(bill, "total") {
		t.Error("no total breach was recorded, so an approver would be shown " +
			"three lines that each look acceptable and no reason for the block")
	}
}

// A bill that agrees with the order in every respect must still pass all four
// dimensions, or the checks above are just a way of blocking everything.
func TestAnHonestBillPassesEveryDimension(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "10"},
	})

	bill := billOf(t, h, f, poID, "INV-OK-1", []map[string]any{{
		"po_line_id": lineID, "description": "Abaya",
		"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
	}})

	if bill["status"] != "matched" {
		t.Fatalf("an exact bill came back %v, want matched", bill["status"])
	}

	got := dimensionsOf(bill)
	for _, want := range []string{"qty", "price", "tax", "total"} {
		if len(got[want]) == 0 {
			t.Errorf("the match recorded no %q comparison. The design names four "+
				"dimensions, and 'we checked and it was fine' is itself the "+
				"evidence an auditor asks for.", want)
		}
		for _, m := range got[want] {
			if m["outcome"] == "breach" {
				t.Errorf("%q came back a breach on an exact bill: %v",
					want, m["detail"])
			}
		}
	}
}

// THE SECOND DEFECT THIS FILE FOUND.
//
// The match records the variance as a percentage of what was agreed, in a
// numeric(9,4) column — five digits before the point. A line agreed at one
// hallala and billed at ten thousand riyals is out by ninety-nine million per
// cent, and writing that number aborts the transaction it is part of.
//
// The bill is then not recorded AT ALL. The buyer gets a server error, retries,
// gets the same error, and the largest overcharge the system can be shown is the
// one it cannot even accept, let alone block. A control that crashes is worse
// than one that merely passes something: at least a pass leaves a record.
func TestAnEnormousOverchargeIsBlockedRatherThanCrashing(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	// A hallala a unit. Legitimate — a supplier throwing in fixings at a
	// nominal price is ordinary — and the schema permits any cost at or above
	// zero.
	poID, lineID := raiseOrder(t, h, f, "1", "0.01")
	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "1"},
	})

	bill := billOf(t, h, f, poID, "INV-HUGE-1", []map[string]any{{
		"po_line_id": lineID, "description": "Abaya",
		"qty": "1", "unit_cost": "10000.00", "tax_rate": "0.15",
	}})

	if bill["status"] != "blocked" {
		t.Errorf("SAR 10,000 billed against one hallala agreed came back %v, "+
			"want blocked", bill["status"])
	}
	if !breachedOn(bill, "price") {
		t.Error("no price breach was recorded for a millionfold overcharge")
	}

	// And the evidence must be there, which is the half that the overflow took
	// out: the row is what fails to insert, so a missing breach here is the
	// symptom even when the status happens to look right.
	ctx := t.Context()
	var breaches int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM three_way_match
			 WHERE bill_id = $1 AND outcome = 'breach'`, bill["id"]).Scan(&breaches)
	}); err != nil {
		t.Fatalf("reading the evidence: %v", err)
	}
	if breaches == 0 {
		t.Error("the breach did not reach three_way_match")
	}
}

// The percentage is still recorded when it fits, or dropping it beyond the
// column's range would be indistinguishable from never computing it.
func TestTheVarianceIsReportedAsAPercentageWhenItFits(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "1", "100.00")
	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "1"},
	})

	// 160 against 100 agreed: sixty per cent over, and a breach.
	bill := billOf(t, h, f, poID, "INV-PCT-1", []map[string]any{{
		"po_line_id": lineID, "description": "Abaya",
		"qty": "1", "unit_cost": "160.00", "tax_rate": "0.15",
	}})

	var pct string
	for _, m := range dimensionsOf(bill)["price"] {
		pct, _ = m["variance_pct"].(string)
	}
	if !amountsEqual(pct, "60") {
		t.Errorf("the price variance is reported as %q per cent, want 60", pct)
	}
}

// A breach must reach the stored evidence, not only the response body.
func TestTheMatchEvidenceIsStoredNotJustReturned(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "1000", "10.00")

	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "1000"},
	})
	bill := billOf(t, h, f, poID, "INV-EV-1", []map[string]any{{
		"po_line_id": lineID, "description": "Abaya",
		"qty": "1000", "unit_cost": "59.99", "tax_rate": "0.15",
	}})
	billID, _ := bill["id"].(string)

	ctx := t.Context()
	var breaches int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM three_way_match
			 WHERE bill_id = $1 AND outcome = 'breach'`, billID).Scan(&breaches)
	}); err != nil {
		t.Fatalf("reading the evidence: %v", err)
	}

	if breaches == 0 {
		t.Error("the overcharge left no breach in three_way_match. A control " +
			"that leaves no record cannot be audited, and the response body is " +
			"gone as soon as the screen closes.")
	}
}
