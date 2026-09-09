//go:build integration

// Finding the payment you need to reverse, and saying why.
//
// `POST /purchasing/payments/{id}/reverse` had been live since payables landed
// and nothing in the product could name a payment id: the route existed, was
// tested, and was reachable from no screen at all, because a screen would have
// had to ask somebody to type a uuid. `GET /purchasing/payments` is the half
// that was missing, and these tests hold the two properties a picker built on
// it depends on — that what it offers can actually be reversed, and that what
// has been reversed says so.
//
// The reason is tested here rather than on the money, deliberately. It changes
// no figure; it lands in the audit trail, which is the register somebody reads
// when they ask months later why a supplier was paid and then unpaid.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// listPayments reads the ledger the reversal screen is built on.
func listPayments(
	t *testing.T, h *harness, f *buyingFixture, token, query string,
) []map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/purchasing/payments")+query, token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list payments: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	raw, _ := decodeJSON(t, resp)["data"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, row := range raw {
		if m, ok := row.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func paymentRow(rows []map[string]any, id string) map[string]any {
	for _, r := range rows {
		if r["id"] == id {
			return r
		}
	}
	return nil
}

// The list names the payment, the supplier and the bill it settled.
//
// Every one of those is on the screen for a reason: a person choosing which
// payment to take back recognises "Acme Textiles, INV-3f2a, 1,150.00" and does
// not recognise a uuid.
func TestThePaymentListSaysWhatEachPaymentSettled(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	_, paymentID := billedAndPaid(t, h, f)

	row := paymentRow(listPayments(t, h, f, f.token, ""), paymentID)
	if row == nil {
		t.Fatal("the payment just made is not in the list of payments")
	}
	if row["supplier"] != "Acme Textiles" {
		t.Errorf("supplier = %v, want the name the buyer knows", row["supplier"])
	}
	if row["amount"] != "1150.00" {
		t.Errorf("amount = %v, want 1150.00", row["amount"])
	}
	if row["currency"] == nil || row["currency"] == "" {
		t.Error("the amount is stated with no currency, which is legible only " +
			"to somebody who already knows which country the shop is in")
	}
	if row["posted"] != true {
		t.Error("a payment that reached the journal reports itself unposted")
	}
	bills, _ := row["bills"].([]any)
	if len(bills) != 1 {
		t.Fatalf("the payment settled one bill and the list names %d", len(bills))
	}
	if ref, _ := bills[0].(string); ref == "" {
		t.Error("the bill is named by nothing a person could recognise")
	}
}

// `?reversible=true` offers only what can actually be reversed.
//
// The filter is the whole reason the list exists in the shape it does. A
// picker that offered a payment the service refuses teaches somebody that the
// picker lies, and they stop trusting the ones that do not.
func TestThePickerOffersOnlyPaymentsThatCanStillBeReversed(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	_, paymentID := billedAndPaid(t, h, f)

	before := listPayments(t, h, f, f.token, "&reversible=true")
	if paymentRow(before, paymentID) == nil {
		t.Fatal("a live, posted, un-reversed payment is not offered for reversal")
	}

	status, out := reverseWithReason(t, h, f, paymentID, uuid.NewString(),
		"Paid the wrong supplier; Acme was not owed this.")
	if status != http.StatusCreated {
		t.Fatalf("reverse: status %d — %v", status, out)
	}
	reversalID, _ := out["id"].(string)

	after := listPayments(t, h, f, f.token, "&reversible=true")
	if paymentRow(after, paymentID) != nil {
		t.Error("a payment that has already been reversed is still offered for " +
			"reversal; the service refuses it, so the picker is lying")
	}
	if paymentRow(after, reversalID) != nil {
		t.Error("a reversal is offered for reversal, which would let somebody " +
			"walk a balance anywhere by alternating documents")
	}

	// Both documents stay on the unfiltered list, and each says which it is.
	all := listPayments(t, h, f, f.token, "")
	original := paymentRow(all, paymentID)
	reversal := paymentRow(all, reversalID)
	if original == nil || reversal == nil {
		t.Fatal("a reversed payment or its reversal has vanished from the ledger")
	}
	if original["reversed"] != true || original["reversal"] != false {
		t.Errorf("the original reads reversal=%v reversed=%v; it was reversed, "+
			"and it is not itself a reversal",
			original["reversal"], original["reversed"])
	}
	if reversal["reversal"] != true {
		t.Error("the reversing document does not say it is one, so the ledger " +
			"shows two payments of the same amount and no explanation")
	}
}

// The reason reaches the audit trail, and the trail names who wrote it.
func TestReversingASupplierPaymentIsAuditedWithItsReason(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	_, paymentID := billedAndPaid(t, h, f)

	const why = "Duplicate remittance; the bank sent it twice."
	status, out := reverseWithReason(t, h, f, paymentID, uuid.NewString(), why)
	if status != http.StatusCreated {
		t.Fatalf("reverse: status %d — %v", status, out)
	}

	var label, reason string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(actor_label,''), coalesce(after_value->>'reason','')
			FROM audit_log
			WHERE action = 'supplier_payment_reversed'
			  AND entity_id = $1`, paymentID).Scan(&label, &reason)
	}); err != nil {
		t.Fatalf("read the trail: %v", err)
	}

	if label == "" {
		t.Error("the reversal entry names nobody; actor_label is denormalised " +
			"precisely so the trail survives the user row being deleted")
	}
	if reason != why {
		t.Errorf("the trail records the reason as %q, want %q", reason, why)
	}
}

// An auditor may read the ledger and may not move money in it.
//
// The permission split this route was written to: `purchasing.view` reads,
// `purchasing.pay_supplier` acts. Requiring the authority to MOVE money in
// order to LOOK at a payment would leave the payables ledger unreadable by
// everybody who reconciles it.
func TestAnAuditorReadsThePaymentLedgerAndCannotReverseIt(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	_, paymentID := billedAndPaid(t, h, f)

	auditor := h.seedUserIn(t, f.shopFixture, "auditor")

	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/purchasing/payments"), auditor, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an auditor cannot read what the business has paid: %d",
			resp.StatusCode)
	}

	refused := h.do(t, http.MethodPost,
		f.path("/api/v1/purchasing/payments/"+paymentID+"/reverse"), auditor,
		map[string]any{"uuid": uuid.NewString(), "reason": "no"})
	refused.Body.Close()
	if refused.StatusCode != http.StatusForbidden {
		t.Errorf("an auditor reversed a payment: status %d", refused.StatusCode)
	}
}

// One company's payments never appear in another's list.
func TestThePaymentListIsConfinedToItsOwnCompany(t *testing.T) {
	h := newHarness(t)
	mine := seedBuying(t, h)
	theirs := seedBuying(t, h)
	_, theirPayment := billedAndPaid(t, h, theirs)

	rows := listPayments(t, h, mine, mine.token, "")
	if paymentRow(rows, theirPayment) != nil {
		t.Fatal("one business can read another's supplier payments")
	}

	// And naming their company id directly returns nothing rather than their
	// ledger. An unscoped actor passes `CanAccessCompany` for any id — the
	// check only narrows a branch-confined user — so what actually confines
	// this is row-level security on the tenant, and that is the property worth
	// asserting: the request is answered, and the answer is empty.
	resp := h.do(t, http.MethodGet,
		"/api/v1/purchasing/payments?company_id="+theirs.companyID.String(),
		mine.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("naming another company answered %d", resp.StatusCode)
	}
	leaked, _ := decodeJSON(t, resp)["data"].([]any)
	if len(leaked) != 0 {
		t.Fatalf("naming another company's id returned %d of their payments",
			len(leaked))
	}
}

// reverseWithReason posts a reversal carrying an explanation.
func reverseWithReason(
	t *testing.T, h *harness, f *buyingFixture, paymentID, docUUID, reason string,
) (int, map[string]any) {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/purchasing/payments/"+paymentID+"/reverse"), f.token,
		map[string]any{"uuid": docUUID, "reason": reason})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return resp.StatusCode, map[string]any{"body": readBody(t, resp)}
	}
	return resp.StatusCode, decodeJSON(t, resp)
}
