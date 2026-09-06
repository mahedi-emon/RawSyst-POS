//go:build integration

// GET /receivables/receipts — the endpoint whose absence made a screen ask for
// a UUID — and the over-spend it exposed on the way.
//
// `POST /installments/{id}/collect` takes a `receipt_id`. Receipts could be
// created and reversed and never listed, so `/money/installments` printed a
// text box and a sentence explaining where the reference comes from.
//
// Building the picker surfaced the larger problem underneath it: the collect
// route checked only that the receipt belonged to the plan's customer. It
// never checked that there was money left on it, so one receipt could mark off
// any number of instalments. See TestAReceiptCannotCollectMoreThanItIsWorth.
package api

import (
	"net/http"
	"testing"
)

func listReceipts(t *testing.T, h *harness, f *arFixture, query string) []any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/receivables/receipts")+query, f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing receipts: %d %s", resp.StatusCode, readBody(t, resp))
	}
	rows, _ := decodeJSONFrom(t, resp)["data"].([]any)
	return rows
}

// takeReceipt records money against an invoice and answers the receipt id.
func takeReceipt(
	t *testing.T, h *harness, f *arFixture, invoiceID, amount string,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/receivables/receipts"),
		f.token, map[string]any{
			"uuid": newUUID().String(), "customer_id": f.customerID,
			"method": "cash",
			"allocations": []map[string]any{
				{"invoice_id": invoiceID, "amount": amount},
			},
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("receipt for %s: %d %s", amount, resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSONFrom(t, resp)["id"].(string)
	return id
}

// A receipt that was taken can be found, by the name of the person who paid.
func TestReceiptsCanBeListed(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != http.StatusCreated {
		t.Fatalf("sale: %s", invoiceID)
	}
	takeReceipt(t, h, f, invoiceID, "40.00")

	rows := listReceipts(t, h, f, "")
	if len(rows) != 1 {
		t.Fatalf("took one receipt, listed %d", len(rows))
	}
	got, _ := rows[0].(map[string]any)
	if amount, _ := got["amount"].(string); amount != "40.00" {
		t.Errorf("amount %q, want 40.00", amount)
	}
	if left, _ := got["unapplied"].(string); left != "40.00" {
		t.Errorf("unapplied %q, want 40.00 — nothing has been collected "+
			"against a plan with it yet", left)
	}
	if number, _ := got["receipt_number"].(string); number == "" {
		t.Error("the row carries no receipt number, which is what a person reads")
	}
	if customer, _ := got["customer"].(string); customer != "Al Noor Trading" {
		t.Errorf("customer %q — a picker showing ids and no names is not a picker",
			customer)
	}

	// Narrowing to somebody else's account finds nothing rather than everything.
	if rows := listReceipts(t, h, f, "&customer_id="+newUUID().String()); len(rows) != 0 {
		t.Errorf("filtering by another customer returned %d rows", len(rows))
	}
}

// A reversed receipt stays in the list and leaves the picker.
//
// Both halves matter: a reversal explains a number in the ledger, so hiding it
// makes the statement unreadable; and it is not money, so offering it to
// collect against would record a collection that has been undone.
func TestAReversedReceiptIsListedButNotOffered(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != http.StatusCreated {
		t.Fatalf("sale: %s", invoiceID)
	}
	receiptID := takeReceipt(t, h, f, invoiceID, "70.00")

	if offered := listReceipts(t, h, f, "&unapplied=true"); len(offered) != 1 {
		t.Fatalf("before the reversal the picker offered %d receipts, want 1",
			len(offered))
	}

	reversed := h.do(t, http.MethodPost,
		f.path("/api/v1/receivables/receipts/"+receiptID+"/reverse"), f.token,
		map[string]any{"uuid": newUUID().String()})
	if reversed.StatusCode != http.StatusCreated {
		t.Fatalf("reversing: %d %s", reversed.StatusCode, readBody(t, reversed))
	}
	reversed.Body.Close()

	if offered := listReceipts(t, h, f, "&unapplied=true"); len(offered) != 0 {
		t.Errorf("a reversed receipt is still offered to collect against (%d rows)",
			len(offered))
	}

	all := listReceipts(t, h, f, "")
	if len(all) != 2 {
		t.Fatalf("want the receipt and its reversal, got %d", len(all))
	}
	var sawReversal, sawReversed bool
	for _, row := range all {
		r, _ := row.(map[string]any)
		if is, _ := r["reversal"].(bool); is {
			sawReversal = true
		}
		if was, _ := r["reversed"].(bool); was {
			sawReversed = true
		}
	}
	if !sawReversal || !sawReversed {
		t.Errorf("the list does not distinguish the reversal from what it "+
			"reversed (reversal seen %v, reversed seen %v)",
			sawReversal, sawReversed)
	}
}

// The list is this company's, and names no other.
func TestReceiptsFromAnotherBusinessAreNotListed(t *testing.T) {
	h := newHarness(t)
	mine := seedSelling(t, h, "5000.00")
	theirs := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, theirs, "115.00", "115.00", "0.00")
	if status != http.StatusCreated {
		t.Fatalf("their sale: %s", invoiceID)
	}
	takeReceipt(t, h, theirs, invoiceID, "115.00")

	if rows := listReceipts(t, h, mine, ""); len(rows) != 0 {
		t.Errorf("another business's receipts are listed here (%d rows)", len(rows))
	}
}

// --- The defect the picker found ----------------------------------------

// openPlanOn turns a credit invoice into a schedule and answers the plan id.
func openPlanOn(
	t *testing.T, h *harness, f *arFixture, invoiceID string, months int,
) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/installments"), f.token,
		map[string]any{
			"customer_id": f.customerID, "invoice_id": invoiceID,
			"tenure_months": months,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("opening a plan: %d %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSONFrom(t, resp)
	id, _ := body["id"].(string)
	if id == "" {
		if plan, ok := body["plan"].(map[string]any); ok {
			id, _ = plan["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("the plan response carries no id: %v", body)
	}
	return id
}

func collect(
	t *testing.T, h *harness, f *arFixture, planID, receiptID, amount string,
) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost,
		f.path("/api/v1/installments/"+planID+"/collect"), f.token,
		map[string]any{"receipt_id": receiptID, "amount": amount})
}

// A receipt cannot mark off more instalments than it is worth, and cannot be
// presented twice.
//
// Ownership was the only check. A 100 receipt could settle a 300 schedule, or
// be handed in three times for 100 each — the plan would close, and two thirds
// of the money never arrived. `installment_payment` is the memo of what a
// receipt was FOR, so an inflated memo is invisible in the ledger: the only
// symptom is a customer who stops paying a plan the product says is finished.
func TestAReceiptCannotCollectMoreThanItIsWorth(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "300.00", "300.00", "0.00")
	if status != http.StatusCreated {
		t.Fatalf("sale: %s", invoiceID)
	}
	planID := openPlanOn(t, h, f, invoiceID, 3)
	receiptID := takeReceipt(t, h, f, invoiceID, "100.00")

	// Straight over-spend: 300 marked off with 100 in hand.
	over := collect(t, h, f, planID, receiptID, "300.00")
	body := readBody(t, over)
	over.Body.Close()
	if over.StatusCode != http.StatusBadRequest {
		t.Fatalf("collecting 300 against a 100 receipt answered %d, want 400: %s",
			over.StatusCode, body)
	}

	// The honest collection is allowed.
	ok := collect(t, h, f, planID, receiptID, "100.00")
	if ok.StatusCode != http.StatusOK && ok.StatusCode != http.StatusCreated {
		t.Fatalf("collecting the receipt's own 100 answered %d: %s",
			ok.StatusCode, readBody(t, ok))
	}
	ok.Body.Close()

	// And the same receipt cannot be handed in again.
	again := collect(t, h, f, planID, receiptID, "100.00")
	body = readBody(t, again)
	again.Body.Close()
	if again.StatusCode != http.StatusBadRequest {
		t.Errorf("the same receipt collected twice answered %d, want 400: %s",
			again.StatusCode, body)
	}

	// The picker no longer offers it, because there is nothing left on it.
	for _, row := range listReceipts(t, h, f, "&unapplied=true") {
		if id, _ := row.(map[string]any)["id"].(string); id == receiptID {
			t.Error("a spent receipt is still offered to collect against")
		}
	}

	// And the list reports zero left rather than the amount.
	var left string
	for _, row := range listReceipts(t, h, f, "") {
		r, _ := row.(map[string]any)
		if id, _ := r["id"].(string); id == receiptID {
			left, _ = r["unapplied"].(string)
		}
	}
	if left != "0.00" {
		t.Errorf("unapplied is %q after the receipt was fully collected, want 0.00",
			left)
	}
}

// A reversed receipt cannot collect at all.
func TestAReversedReceiptCannotCollectAnInstalment(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "300.00", "300.00", "0.00")
	if status != http.StatusCreated {
		t.Fatalf("sale: %s", invoiceID)
	}
	planID := openPlanOn(t, h, f, invoiceID, 3)
	receiptID := takeReceipt(t, h, f, invoiceID, "100.00")

	undone := h.do(t, http.MethodPost,
		f.path("/api/v1/receivables/receipts/"+receiptID+"/reverse"), f.token,
		map[string]any{"uuid": newUUID().String()})
	if undone.StatusCode != http.StatusCreated {
		t.Fatalf("reversing: %d %s", undone.StatusCode, readBody(t, undone))
	}
	undone.Body.Close()

	refused := collect(t, h, f, planID, receiptID, "100.00")
	body := readBody(t, refused)
	refused.Body.Close()
	if refused.StatusCode != http.StatusBadRequest {
		t.Errorf("collecting against a reversed receipt answered %d, want 400: %s",
			refused.StatusCode, body)
	}
}
