//go:build integration

// Being invoiced twice for one delivery.
//
// Blueprint B5.2 puts the three-way match there to answer one question — "did
// we agree to this, did it arrive, and is this what it costs?" — and the
// commonest way a shop loses money to its own paperwork is not an inflated
// price. It is paying the same goods twice: a supplier who invoices a delivery,
// hears nothing, and invoices it again under a new number; a buyer who enters a
// statement copy alongside the original; a duplicate that arrives by email after
// one arrived by post.
//
// The product has one existing defence, and it stops at the obvious case.
// purchase_bill is unique on (supplier_id, supplier_ref), so the SAME invoice
// number cannot be entered twice — the comment on it says as much. A different
// number for the same goods passes straight through it.
//
// # What was wrong
//
// The match compared the billed quantity against po_outstanding.qty_received:
// everything that ever arrived on the order line. The same function has always
// returned qty_billed beside it and the match never read it, so each invoice was
// judged as though it were the only one. A hundred received, a hundred billed:
// pass, on every dimension, and the bill posts and can be paid.
//
// The accrual went with it. postBill discharges Goods Received Not Invoiced by
// the quantity billed capped at the quantity RECEIVED, so a second invoice
// discharged an accrual that had only ever been raised once — taking a
// liability account through zero into a debit balance, which asserts that the
// shop's own stockroom owes it money.
//
// # How these tests are written
//
// From the control's own words rather than from the code: what is billable is
// what arrived and has not been billed yet. Everything follows from that one
// sentence, including the cases that must NOT block — instalment invoicing
// against one delivery is ordinary trade, and a control that refused it would
// be turned off within a week.
package api

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Goods Received Not Invoiced is read through roleBalance, which reports
// debits less credits.
//
// GRNI is a liability — goods are here, the invoice is not, so the shop owes
// for them — so its natural balance is a credit and roleBalance reports it
// NEGATIVE. Zero is normal once everything is invoiced. A positive figure is
// not a smaller version of either: it says the receiving bay owes the shop
// money, which is not a thing that can be true, and it is what discharging one
// accrual twice produces.

// THE DEFECT THIS FILE FOUND.
//
// One order of 100 at SAR 10.00. All 100 arrive. The supplier invoices them,
// and then invoices them again under a different number.
//
// The second invoice agrees with the order on price, on VAT and on total, and
// names a quantity that did arrive. The only thing wrong with it is that
// somebody has already been paid for those goods, and quantity is the only
// dimension in a position to notice.
func TestASecondInvoiceForGoodsAlreadyBilledIsBlocked(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "100", "10.00")

	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "100"},
	})

	line := func() []map[string]any {
		return []map[string]any{{
			"po_line_id": lineID, "description": "Abaya",
			"qty": "100", "unit_cost": "10.00", "tax_rate": "0.15",
		}}
	}

	first := billOf(t, h, f, poID, "SUP-INV-8801", line())
	if first["status"] != "matched" {
		t.Fatalf("the genuine invoice came back %v, want matched — the goods "+
			"arrived and this is the first claim on them", first["status"])
	}
	afterFirst := roleBalance(t, h, f.shopFixture, "grni")

	// A different number, so the unique key on (supplier, supplier_ref) does
	// not fire. This is what a duplicate actually looks like.
	second := billOf(t, h, f, poID, "SUP-INV-8814", line())

	if second["status"] != "blocked" {
		t.Errorf("a second invoice for goods already billed came back %v. The "+
			"shop can pay SAR 1,150 twice for one delivery and nothing in the "+
			"three-way match said a word.", second["status"])
	}
	if !breachedOn(second, "qty") {
		t.Errorf("the quantity dimension passed the duplicate: %v",
			dimensionsOf(second)["qty"])
	}

	// The evidence has to say WHY, or a buyer reading a blocked bill sees
	// "billed 100 against 100 received, breach" and reports a malfunction.
	for _, m := range dimensionsOf(second)["qty"] {
		if m["previously_billed"] != "100" {
			t.Errorf("the recorded evidence does not name what was already "+
				"billed: %v", m)
		}
	}

	if after := roleBalance(t, h, f.shopFixture, "grni"); !after.Equal(afterFirst) {
		t.Errorf("the duplicate moved Goods Received Not Invoiced from %s to "+
			"%s. A blocked bill is not posted, so the accrual must not have "+
			"moved at all.", afterFirst, after)
	}
	if afterFirst.IsPositive() {
		t.Errorf("GRNI is %s after one delivery and one invoice: a debit "+
			"balance on a liability says the receiving bay owes the shop money",
			afterFirst)
	}
}

// Invoicing one delivery in instalments is ordinary trade and must not block.
//
// A supplier who ships 100 and sends two invoices of 50 has done nothing wrong,
// and a control that refused the second one would be switched off inside a
// week — which would take the duplicate check with it.
func TestTwoInvoicesThatShareOutOneDeliveryBothPass(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "100", "10.00")

	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "100"},
	})

	half := func() []map[string]any {
		return []map[string]any{{
			"po_line_id": lineID, "description": "Abaya",
			"qty": "50", "unit_cost": "10.00", "tax_rate": "0.15",
		}}
	}

	first := billOf(t, h, f, poID, "SUP-INV-9001", half())
	if first["status"] != "matched" {
		t.Fatalf("the first half came back %v: %v",
			first["status"], dimensionsOf(first)["qty"])
	}

	second := billOf(t, h, f, poID, "SUP-INV-9002", half())
	if second["status"] != "matched" {
		t.Fatalf("the second half of one delivery came back %v, and it is "+
			"exactly what is still outstanding: %v",
			second["status"], dimensionsOf(second)["qty"])
	}

	// And the one that goes over the line is caught, at no tolerance, because
	// a quantity is a count and cannot be out by rounding.
	third := billOf(t, h, f, poID, "SUP-INV-9003", []map[string]any{{
		"po_line_id": lineID, "description": "Abaya",
		"qty": "1", "unit_cost": "10.00", "tax_rate": "0.15",
	}})
	if third["status"] != "blocked" {
		t.Errorf("a 101st unit against a delivery of 100 came back %v",
			third["status"])
	}
}

// The same bill naming one order line twice is the duplicate turned inwards.
//
// It is what a buyer produces by pasting a line and forgetting to change it,
// and comparing each line against the delivery on its own would pass both.
func TestOneBillCannotClaimTheSameDeliveryOnTwoOfItsOwnLines(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "100", "10.00")

	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "100"},
	})

	bill := billOf(t, h, f, poID, "SUP-INV-7700", []map[string]any{
		{"po_line_id": lineID, "description": "Abaya",
			"qty": "100", "unit_cost": "10.00", "tax_rate": "0.15"},
		{"po_line_id": lineID, "description": "Abaya",
			"qty": "100", "unit_cost": "10.00", "tax_rate": "0.15"},
	})

	if bill["status"] != "blocked" {
		t.Errorf("a bill claiming 200 against a delivery of 100 across two of "+
			"its own lines came back %v: %v",
			bill["status"], dimensionsOf(bill)["qty"])
	}
}

// A cancelled bill releases what it claimed.
//
// Otherwise correcting a mistake is impossible: the wrong invoice is entered,
// cancelled, and the right one is then refused for goods nothing is claiming
// any more. po_outstanding has always excluded cancelled bills; this checks the
// match inherits that rather than counting them.
func TestCancellingABillReleasesTheDeliveryItClaimed(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	poID, lineID := raiseOrder(t, h, f, "100", "10.00")

	receiveAll(t, h, f, poID, []map[string]any{
		{"po_line_id": lineID, "qty_received": "100"},
	})

	line := func() []map[string]any {
		return []map[string]any{{
			"po_line_id": lineID, "description": "Abaya",
			"qty": "100", "unit_cost": "10.00", "tax_rate": "0.15",
		}}
	}

	wrong := billOf(t, h, f, poID, "SUP-INV-6001", line())
	billID, _ := wrong["id"].(string)

	ctx := context.Background()
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE purchase_bill SET status = 'cancelled' WHERE id = $1`, billID)
		return e
	}); err != nil {
		t.Fatalf("cancel the wrong bill: %v", err)
	}

	right := billOf(t, h, f, poID, "SUP-INV-6002", line())
	if right["status"] != "matched" {
		t.Errorf("the replacement invoice came back %v after the one it "+
			"replaces was cancelled: %v",
			right["status"], dimensionsOf(right)["qty"])
	}
}
