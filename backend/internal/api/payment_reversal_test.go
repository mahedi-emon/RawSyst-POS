//go:build integration

package api

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// Putting a supplier payment right — the AP mirror of P21.
//
// The property every test here protects is the one design 02 §111 states
// absolutely: posted history is never edited. A wrong payment is corrected by a
// NEW document that reverses it, and both stay on the record.
//
// Payables makes this harder than receivables in one specific way, and the
// tests below are shaped around it. Receivables derives what is owed;
// `purchase_bill` STORES `amount_paid` and flips to 'paid' when settled. A
// reversal therefore has to unwind stored state, and put the status back to
// what it actually was rather than to whichever value looks plausible.

// --- fixture --------------------------------------------------------------

// billedAndPaid raises an order, receives it, bills it and pays it in full.
// Returns the bill id and the payment id.
func billedAndPaid(t *testing.T, h *harness, f *buyingFixture) (string, string) {
	t.Helper()
	poID, lineID := raiseOrder(t, h, f, "10", "100.00")

	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		})

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-" + newUUID().String()[:8],
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "100.00", "tax_rate": "0.15",
			}},
		})
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, billed))
	}
	billID, _ := decodeJSON(t, billed)["id"].(string)

	paid := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "method": "bank_transfer",
			"allocations": []map[string]any{{"bill_id": billID, "amount": "1150.00"}},
		})
	if paid.StatusCode != 201 {
		t.Fatalf("pay: %s", readBody(t, paid))
	}
	paymentID, _ := decodeJSON(t, paid)["id"].(string)
	return billID, paymentID
}

func reverse(
	t *testing.T, h *harness, f *buyingFixture, paymentID, docUUID string,
) (int, map[string]any) {
	t.Helper()
	resp := h.do(t, "POST",
		f.path("/api/v1/purchasing/payments/"+paymentID+"/reverse"), f.token,
		map[string]any{"uuid": docUUID})
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return resp.StatusCode, map[string]any{"body": readBody(t, resp)}
	}
	return resp.StatusCode, decodeJSON(t, resp)
}

// payableBalance is what the AP control account holds, which is the figure a
// reversal has to put back.
func payableBalance(t *testing.T, h *harness, f *buyingFixture) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_credit - l.base_debit), 0)
			FROM journal_line l
			JOIN account_role_map m ON m.account_id = l.account_id
			WHERE m.company_id = $1 AND m.role = 'accounts_payable'`,
			f.companyID).Scan(&d)
	}); err != nil {
		t.Fatalf("payable balance: %v", err)
	}
	return d
}

// --- The point of the whole thing -----------------------------------------

// Reversing a payment puts the bill back to owed, and puts the money back.
func TestReversingAPaymentPutsTheBillBackOnTheAccount(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	billID, paymentID := billedAndPaid(t, h, f)

	// Paid in full: nothing outstanding, and the payable is discharged.
	if owed := payableBalance(t, h, f); !owed.IsZero() {
		t.Fatalf("after paying in full the payable is %s, want 0", owed)
	}

	status, out := reverse(t, h, f, paymentID, newUUID().String())
	if status != 201 {
		t.Fatalf("reverse: %d %v", status, out)
	}

	// The money is owed again.
	if owed := payableBalance(t, h, f); !owed.Equal(decimal.RequireFromString("1150")) {
		t.Errorf("after reversing, the payable is %s, want 1150", owed)
	}

	// And the bill says so.
	settled, _ := out["settled"].([]any)
	if len(settled) != 1 {
		t.Fatalf("the reversal settled %d bills, want 1", len(settled))
	}
	row, _ := settled[0].(map[string]any)
	if got, _ := row["outstanding"].(string); !amountsEqual(got, "1150.00") {
		t.Errorf("outstanding after reversal = %q, want 1150.00", got)
	}

	var amountPaid decimal.Decimal
	var billStatus string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT amount_paid, status FROM purchase_bill WHERE id = $1`,
			billID).Scan(&amountPaid, &billStatus)
	}); err != nil {
		t.Fatalf("read the bill: %v", err)
	}
	if !amountPaid.IsZero() {
		t.Errorf("amount_paid after reversal = %s, want 0", amountPaid)
	}
	if billStatus == "paid" {
		t.Error("a reversed bill is still marked paid")
	}
}

// The status goes back to what it WAS, not to whichever value looks plausible.
//
// A bill reaching payment as 'matched' means the three-way match agreed. One
// reaching it as 'approved' means it did not, and somebody accepted the
// discrepancy by name. B5.2's control is decorative if reversing a payment
// silently converts the second into the first.
func TestAReversalRestoresTheStatusTheBillActuallyHad(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	billID, paymentID := billedAndPaid(t, h, f)

	var before string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT bill_status_before FROM supplier_payment_allocation
			 WHERE bill_id = $1 LIMIT 1`, billID).Scan(&before)
	}); err != nil {
		t.Fatalf("read the recorded status: %v", err)
	}
	if before != "matched" {
		t.Fatalf("the allocation recorded %q, want the status the bill had", before)
	}

	if status, out := reverse(t, h, f, paymentID, newUUID().String()); status != 201 {
		t.Fatalf("reverse: %d %v", status, out)
	}

	var after string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT status FROM purchase_bill WHERE id = $1`, billID).Scan(&after)
	}); err != nil {
		t.Fatalf("read the bill: %v", err)
	}
	if after != before {
		t.Errorf("the bill came back as %q, want the %q it actually was", after, before)
	}
}

// The journal is a flipped copy of the original, linked to it.
func TestAReversingPaymentFlipsTheOriginalJournal(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	_, paymentID := billedAndPaid(t, h, f)
	status, out := reverse(t, h, f, paymentID, newUUID().String())
	if status != 201 {
		t.Fatalf("reverse: %d %v", status, out)
	}
	reversalID, _ := out["id"].(string)

	var origEntry, revEntry string
	var reversesID *string
	var debits, credits decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(),
			`SELECT journal_entry_id::text FROM supplier_payment WHERE id = $1`,
			paymentID).Scan(&origEntry); e != nil {
			return e
		}
		if e := tx.QueryRow(t.Context(),
			`SELECT journal_entry_id::text FROM supplier_payment WHERE id = $1`,
			reversalID).Scan(&revEntry); e != nil {
			return e
		}
		if e := tx.QueryRow(t.Context(),
			`SELECT reverses_id::text FROM journal_entry WHERE id = $1::uuid`,
			revEntry).Scan(&reversesID); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(base_debit),0), coalesce(sum(base_credit),0)
			FROM journal_line WHERE entry_id = $1::uuid`,
			revEntry).Scan(&debits, &credits)
	}); err != nil {
		t.Fatalf("read the journal: %v", err)
	}

	if reversesID == nil || *reversesID != origEntry {
		t.Errorf("the reversing entry points at %v, want the original %s",
			reversesID, origEntry)
	}
	// Still balanced — the invariant the whole posting engine exists to hold.
	if !debits.Equal(credits) {
		t.Errorf("the reversing entry is unbalanced: %s debits, %s credits",
			debits, credits)
	}
	if debits.IsZero() {
		t.Error("the reversing entry has no lines")
	}
}

// --- Idempotency and one-reversal-per-payment ------------------------------

// A retry with the same identifier is the same reversal, not a second one.
func TestARetriedPaymentReversalDoesNotReverseTwice(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	_, paymentID := billedAndPaid(t, h, f)
	docUUID := newUUID().String()

	first, out := reverse(t, h, f, paymentID, docUUID)
	if first != 201 {
		t.Fatalf("first reversal: %d %v", first, out)
	}
	firstID, _ := out["id"].(string)

	second, again := reverse(t, h, f, paymentID, docUUID)
	if second != 200 {
		t.Fatalf("the retry returned %d, want 200: %v", second, again)
	}
	if got, _ := again["id"].(string); got != firstID {
		t.Errorf("the retry made a new document %q, want the original %q", got, firstID)
	}
	if paid, _ := again["already_paid"].(bool); !paid {
		t.Error("the retry did not say it had already been recorded")
	}

	// One reversal, so one restoration.
	if owed := payableBalance(t, h, f); !owed.Equal(decimal.RequireFromString("1150")) {
		t.Errorf("payable is %s after a retried reversal, want 1150", owed)
	}
}

// A different identifier against an already-reversed payment is refused.
func TestASecondReversalOfTheSamePaymentIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	_, paymentID := billedAndPaid(t, h, f)
	if status, out := reverse(t, h, f, paymentID, newUUID().String()); status != 201 {
		t.Fatalf("first reversal: %d %v", status, out)
	}

	status, out := reverse(t, h, f, paymentID, newUUID().String())
	if status != 409 {
		t.Fatalf("a second reversal returned %d, want 409: %v", status, out)
	}
	if owed := payableBalance(t, h, f); !owed.Equal(decimal.RequireFromString("1150")) {
		t.Errorf("payable is %s, want 1150 — the refused reversal moved it", owed)
	}
}

// Reversing a reversal would let a clerk walk a balance anywhere.
func TestAPaymentReversalCannotItselfBeReversed(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	_, paymentID := billedAndPaid(t, h, f)
	_, out := reverse(t, h, f, paymentID, newUUID().String())
	reversalID, _ := out["id"].(string)

	status, body := reverse(t, h, f, reversalID, newUUID().String())
	if status != 409 {
		t.Fatalf("reversing a reversal returned %d, want 409: %v", status, body)
	}
}

// A UUID already spent on an ordinary payment cannot be reused as a reversal.
func TestAPaymentUUIDCannotBeReusedAsAReversal(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	_, paymentID := billedAndPaid(t, h, f)

	// A second bill, so the identifier is spent on a payment that genuinely
	// succeeds. Spending it on the first bill would be refused for having
	// nothing left to pay, and the test would then prove nothing.
	secondBill, _ := billedAndPaid(t, h, f)
	_ = secondBill

	var spent string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT uuid::text FROM supplier_payment
			WHERE company_id = $1 AND id <> $2 AND reverses_id IS NULL`,
			f.companyID, paymentID).Scan(&spent)
	}); err != nil {
		t.Fatalf("read the second payment: %v", err)
	}

	status, out := reverse(t, h, f, paymentID, spent)
	if status != 409 {
		t.Fatalf("reusing a payment UUID returned %d, want 409: %v", status, out)
	}
}

func TestAPaymentReversalWithoutAnIdentifierIsRefused(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	_, paymentID := billedAndPaid(t, h, f)
	resp := h.do(t, "POST",
		f.path("/api/v1/purchasing/payments/"+paymentID+"/reverse"), f.token,
		map[string]any{})
	if resp.StatusCode != 400 {
		t.Errorf("a reversal with no identifier returned %d, want 400", resp.StatusCode)
	}
}

func TestAnUnknownPaymentCannotBeReversed(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	status, _ := reverse(t, h, f, newUUID().String(), newUUID().String())
	if status != 404 {
		t.Errorf("reversing an unknown payment returned %d, want 404", status)
	}
}

// --- Immutability ----------------------------------------------------------

// The original is frozen at the database, not merely by convention.
func TestTheOriginalPaymentCannotBeEditedOrDeleted(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	_, paymentID := billedAndPaid(t, h, f)

	for _, attempt := range []struct {
		what string
		sql  string
	}{
		{"change the amount", `UPDATE supplier_payment SET amount = 1 WHERE id = $1`},
		{"change the method", `UPDATE supplier_payment SET method = 'cash' WHERE id = $1`},
		{"delete it", `DELETE FROM supplier_payment WHERE id = $1`},
		{"delete an allocation",
			`DELETE FROM supplier_payment_allocation WHERE payment_id = $1`},
	} {
		err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(t.Context(), attempt.sql, paymentID)
			return e
		})
		if err == nil {
			t.Errorf("it was possible to %s on a posted payment", attempt.what)
		}
	}
}

// --- Isolation, permission, closed period ---------------------------------

// M8: another tenant's payment reads as absent, not as forbidden.
func TestAnotherTenantsPaymentCannotBeReversed(t *testing.T) {
	h := newHarness(t)
	mine := seedBuying(t, h)
	theirs := seedBuying(t, h)

	_, theirPayment := billedAndPaid(t, h, theirs)

	resp := h.do(t, "POST",
		mine.path("/api/v1/purchasing/payments/"+theirPayment+"/reverse"),
		mine.token, map[string]any{"uuid": newUUID().String()})
	if resp.StatusCode != 404 {
		t.Errorf("reversing another tenant's payment returned %d, want 404: %s",
			resp.StatusCode, readBody(t, resp))
	}

	// And theirs is untouched.
	if owed := payableBalance(t, h, theirs); !owed.IsZero() {
		t.Errorf("the other tenant's payable moved to %s", owed)
	}
}

// Reversing money is paying money. An auditor reads; they do not send funds.
func TestAnAuditorCannotReverseAPayment(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	auditor := h.seedUserIn(t, f.shopFixture, "auditor")

	_, paymentID := billedAndPaid(t, h, f)

	resp := h.do(t, "POST",
		f.path("/api/v1/purchasing/payments/"+paymentID+"/reverse"), auditor,
		map[string]any{"uuid": newUUID().String()})
	if resp.StatusCode != 403 {
		t.Errorf("an auditor reversed a payment (%d)", resp.StatusCode)
	}
}

// A closed period takes the whole reversal with it — the document and the
// stored bill state roll back together, or a bill would be un-paid with no
// journal saying so.
func TestAClosedPeriodRollsThePaymentReversalBack(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	billID, paymentID := billedAndPaid(t, h, f)

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			UPDATE fiscal_period
			SET state = 'closed', closed_at = now(), closed_by = $2
			WHERE company_id = $1 AND state = 'open'`, f.companyID, f.userID)
		return e
	}); err != nil {
		t.Fatalf("close the period: %v", err)
	}

	status, out := reverse(t, h, f, paymentID, newUUID().String())
	if status == 201 || status == 200 {
		t.Fatalf("a reversal posted into a closed period: %v", out)
	}

	// Nothing half-happened.
	var amountPaid decimal.Decimal
	var billStatus string
	var reversals int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(),
			`SELECT amount_paid, status FROM purchase_bill WHERE id = $1`,
			billID).Scan(&amountPaid, &billStatus); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM supplier_payment WHERE reverses_id = $1`,
			paymentID).Scan(&reversals)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}

	if reversals != 0 {
		t.Errorf("a reversal document survived the rollback (%d)", reversals)
	}
	if !amountPaid.Equal(decimal.RequireFromString("1150")) {
		t.Errorf("amount_paid rolled to %s, want the original 1150", amountPaid)
	}
	if billStatus != "paid" {
		t.Errorf("the bill is %q after a rolled-back reversal, want paid", billStatus)
	}
}

// --- Paying again ----------------------------------------------------------

// The point of reversing: pay it correctly afterwards.
func TestAReversedPaymentCanBePaidAgain(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	billID, paymentID := billedAndPaid(t, h, f)
	if status, out := reverse(t, h, f, paymentID, newUUID().String()); status != 201 {
		t.Fatalf("reverse: %d %v", status, out)
	}

	again := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "method": "cash",
			"allocations": []map[string]any{{"bill_id": billID, "amount": "1150.00"}},
		})
	if again.StatusCode != 201 {
		t.Fatalf("paying a reversed bill again: %s", readBody(t, again))
	}

	if owed := payableBalance(t, h, f); !owed.IsZero() {
		t.Errorf("after paying again the payable is %s, want 0", owed)
	}

	var billStatus string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT status FROM purchase_bill WHERE id = $1`, billID).Scan(&billStatus)
	}); err != nil {
		t.Fatalf("read the bill: %v", err)
	}
	if billStatus != "paid" {
		t.Errorf("the re-paid bill is %q, want paid", billStatus)
	}
}

// The supplier's ageing reflects the reversal, which is what a buyer reads.
func TestReversingAPaymentReopensTheSupplierAgeing(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	_, paymentID := billedAndPaid(t, h, f)

	settled := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/ageing"), f.token, nil))
	if got, _ := settled["total"].(string); !amountsEqual(got, "0.00") {
		t.Fatalf("after paying, ageing total is %q, want 0.00", got)
	}

	if status, out := reverse(t, h, f, paymentID, newUUID().String()); status != 201 {
		t.Fatalf("reverse: %d %v", status, out)
	}

	owed := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/ageing"), f.token, nil))
	if got, _ := owed["total"].(string); !amountsEqual(got, "1150.00") {
		t.Errorf("after reversing, ageing total is %q, want 1150.00", got)
	}
}

// The net-paid view signs reversals, so a statement does not read as two
// payments.
func TestThePaymentEffectViewNetsAReversalOut(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	_, paymentID := billedAndPaid(t, h, f)
	if status, out := reverse(t, h, f, paymentID, newUUID().String()); status != 201 {
		t.Fatalf("reverse: %d %v", status, out)
	}

	var net decimal.Decimal
	var rows int
	var originalMarked bool
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(net_amount), 0), count(*)
			FROM supplier_payment_effect WHERE company_id = $1`,
			f.companyID).Scan(&net, &rows); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(),
			`SELECT is_reversed FROM supplier_payment_effect WHERE id = $1`,
			paymentID).Scan(&originalMarked)
	}); err != nil {
		t.Fatalf("read the view: %v", err)
	}

	if !net.IsZero() {
		t.Errorf("net paid is %s after a full reversal, want 0", net)
	}
	if rows != 2 {
		t.Errorf("the view shows %d documents, want both the payment and its reversal", rows)
	}
	if !originalMarked {
		t.Error("the original is not marked as reversed, so a screen would offer to reverse it again")
	}
}

// A refusal must not leak another tenant's data through the message.
func TestAPaymentReversalRefusalSaysNothingUseful(t *testing.T) {
	h := newHarness(t)
	mine := seedBuying(t, h)
	theirs := seedBuying(t, h)

	_, theirPayment := billedAndPaid(t, h, theirs)

	body := readBody(t, h.do(t, "POST",
		mine.path("/api/v1/purchasing/payments/"+theirPayment+"/reverse"),
		mine.token, map[string]any{"uuid": newUUID().String()}))

	for _, leak := range []string{theirs.companyID.String(), theirs.supplierID} {
		if strings.Contains(body, leak) {
			t.Errorf("the refusal leaks %q: %s", leak, body)
		}
	}
}
