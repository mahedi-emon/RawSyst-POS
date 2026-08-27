//go:build integration

// Two receipts allocated to one invoice at the same moment.
//
// The receivable is the shop's claim on a customer, and design 12 makes it a
// control account: what customer_open_invoices says is outstanding must equal
// what Accounts Receivable says is owed, at all times. An invoice that receives
// more than it was for breaks both at once — it makes a customer's statement
// show a credit against a document that was never a credit, and it puts the
// control account and its subsidiary ledger permanently out of step.
//
// TakePayment reads the outstanding figure and then compares the allocation to
// it. That is one read and one decision, in a system where a shop can take a
// card payment at the counter while the back office is posting a bank transfer
// against the same invoice: an ordinary Tuesday, not an exotic one.
package api

import (
	"net/http"
	"testing"
)

// An invoice can be paid to zero and no further, however many people are paying
// it at once.
//
// Both receipts are for the WHOLE outstanding amount, so exactly one of them
// can be right. If both are accepted the invoice has been paid twice and the
// customer is owed money the shop does not think it holds.
func TestTwoReceiptsForTheWholeInvoiceAtOnceOnlyOneSucceeds(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != http.StatusCreated {
		t.Fatalf("sale: %s", invoiceID)
	}

	statuses := make([]int, 2)
	errs := concurrently(2, func(i int) error {
		resp := h.do(t, "POST", f.path("/api/v1/receivables/receipts"), f.token,
			map[string]any{
				"uuid": newUUID(), "customer_id": f.customerID, "method": "cash",
				"allocations": []map[string]any{
					{"invoice_id": invoiceID, "amount": "115.00"},
				},
			})
		resp.Body.Close()
		statuses[i] = resp.StatusCode
		return nil
	})
	for _, e := range errs {
		if e != nil {
			t.Fatalf("receipt: %v", e)
		}
	}

	accepted := 0
	for _, s := range statuses {
		if s == http.StatusCreated {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("%d of two receipts for the whole of one invoice were accepted "+
			"(statuses %v); an invoice of 115.00 cannot be settled twice",
			accepted, statuses)
	}

	// Whatever the statuses said, the books are the authority.
	balance := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))["balance"]
	if got, _ := balance.(string); !amountsEqual(got, "0.00") {
		t.Errorf("the customer's balance is %q after one 115.00 invoice and two "+
			"races to pay it; anything but 0.00 means one payment landed against "+
			"a document that did not owe it", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("the receivable control account is out by %s against the open "+
			"invoices behind it", d)
	}
}

// The same race with halves, which must BOTH succeed.
//
// A guard that serialised by refusing anything concurrent would pass the test
// above and be useless: a shop with two people taking money has two people
// taking money. Splitting the invoice between them is a legitimate day.
func TestTwoReceiptsThatShareOneInvoiceBothSucceed(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")

	invoiceID, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
	if status != http.StatusCreated {
		t.Fatalf("sale: %s", invoiceID)
	}

	statuses := make([]int, 2)
	concurrently(2, func(i int) error {
		resp := h.do(t, "POST", f.path("/api/v1/receivables/receipts"), f.token,
			map[string]any{
				"uuid": newUUID(), "customer_id": f.customerID, "method": "cash",
				"allocations": []map[string]any{
					{"invoice_id": invoiceID, "amount": "57.50"},
				},
			})
		resp.Body.Close()
		statuses[i] = resp.StatusCode
		return nil
	})

	for i, s := range statuses {
		if s != http.StatusCreated {
			t.Errorf("receipt %d for half of an invoice was refused with %d; two "+
				"people taking money is not a conflict", i, s)
		}
	}
	balance := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))["balance"]
	if got, _ := balance.(string); !amountsEqual(got, "0.00") {
		t.Errorf("balance = %q after two halves of 115.00, want 0.00", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("the receivable is out by %s", d)
	}
}

// A credit limit is a limit however many tills are selling.
//
// Blueprint B6 makes the limit the shop's exposure to one customer, and
// CheckCredit reads the balance and decides in two steps. Two tills serving the
// same trade customer at once — a wholesaler with a counter and a phone line is
// the ordinary case — would otherwise both read the same balance, both find
// room under the limit, and between them grant twice the credit the Owner
// agreed to.
func TestTwoTillsCannotBothGrantTheLastOfACustomersCredit(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "115.00") // room for exactly one sale

	statuses := make([]int, 2)
	bodies := make([]string, 2)
	concurrently(2, func(i int) error {
		body, status := sellOnAccount(t, h, f, "115.00", "115.00", "0.00")
		statuses[i], bodies[i] = status, body
		return nil
	})

	accepted := 0
	for _, s := range statuses {
		if s == http.StatusCreated {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("%d of two sales on account were accepted against a limit that "+
			"has room for one (statuses %v, bodies %v); the shop has extended "+
			"twice the credit its Owner agreed to", accepted, statuses, bodies)
	}

	balance := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/customers/"+f.customerID), f.token, nil))["balance"]
	if got, _ := balance.(string); !amountsEqual(got, "115.00") {
		t.Errorf("the customer owes %q against a limit of 115.00", got)
	}
	if d := receivableTieOut(t, h, f); !d.IsZero() {
		t.Errorf("the receivable is out by %s", d)
	}
}
