package sales

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A settlement method the server reserves for itself, named by a client.
//
// exchange_clearing is not a way of paying. It is the bookkeeping device that
// carries the offsetting half of an exchange from the credit note across to the
// replacement invoice, and planSettlement is the only thing entitled to produce
// one: two legs, created together, cancelling inside a single transaction.
//
// Nothing was refusing it from a request. Migration 0030 had to add
// 'exchange_clearing' to the method CHECK on sales_tender and sales_refund, or
// the exchange's own legs could not have been written, and that CHECK cannot
// tell the two documents of an exchange from any other sale. tenderRole maps
// the method to the clearing account for whoever asks.
//
// So most of what follows is about an ordinary sale and an ordinary refund
// rather than about exchanges. That is the point: the hole was on the plain
// paths, which is also where nobody was looking for it.

// THE DEFECT THIS FILE FOUND.
//
// An ordinary SAR 500 sale, tendered 'exchange_clearing'.
//
// It was accepted. Revenue recognised, the clearing account credited 500.00,
// and nothing anywhere that would ever debit it back.
//
// No money is involved, which is what makes it the worst shape a defect can
// take here. The drawer reconciles to the hallala, the shift closes clean, the
// sale reads as ordinary in every report, and the only trace is a permanent
// balance in the one account whose entire purpose is to be empty --
// exchange_clearing_balance exists to report exactly that, and it would report
// it months later with nothing left to tie it back to.
func TestASaleCannotBeTenderedThroughTheClearingAccount(t *testing.T) {
	err := checkMethodsAreOfferable(tenderMethods([]Tender{
		cash("100.00"),
		{Method: ExchangeClearing, Amount: dec("500.00")},
	}))
	if err == nil {
		t.Fatal("a sale was tendered through the exchange clearing account: " +
			"revenue recognised against a balance nothing will ever clear")
	}
	// A cashier has to be told what to do instead, not that a constraint fired.
	if !strings.Contains(err.Error(), ExchangeClearing) {
		t.Errorf("the refusal does not name the method: %v", err)
	}
	if !strings.Contains(err.Error(), "exchange") {
		t.Errorf("the refusal does not say what the method is for: %v", err)
	}
}

// The same on the way out, which is the worse direction: the shop hands real
// goods back and books the credit against an account no customer owns, so the
// refund never reaches the drawer, the till reconciles, and the balance sits
// there.
//
// Driven through Refund rather than through the validator, so this pins the
// WIRING and not just the rule. No database is touched: a request naming a
// method it may not name is refused on its own terms, before the service needs
// a connection.
func TestARefundCannotBeMadeThroughTheClearingAccount(t *testing.T) {
	var svc Service // deliberately unwired -- nothing here should reach a pool

	_, err := svc.Refund(context.Background(), uuid.New(), uuid.New(), Return{
		OriginalInvoiceID: uuid.New(),
		Refunds: []Refund{
			{Method: ExchangeClearing, Amount: dec("500.00")},
		},
	}, uuid.New())

	if err == nil {
		t.Fatal("a refund was paid out through the exchange clearing account")
	}
	if !strings.Contains(err.Error(), ExchangeClearing) {
		t.Errorf("the refusal does not name the method, so this may be failing "+
			"for an unrelated reason: %v", err)
	}
}

// And on the exchange itself, where the method at least belongs -- but the
// offered settlement is the customer's real money for the DIFFERENCE, so a
// clearing leg offered there asks for the offset to be counted twice.
//
// Both directions, because the doubling lands on a different document in each:
// on the invoice when the customer owes, on the credit note when the shop does.
func TestTheClearingMethodCannotBeOfferedAsSettlement(t *testing.T) {
	for _, c := range []struct{ name, credit, replacement, settle string }{
		{"swapping up", "100.00", "150.00", "50.00"},
		{"swapping down", "150.00", "100.00", "50.00"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := planSettlement(dec(c.credit), dec(c.replacement),
				[]Tender{{Method: ExchangeClearing, Amount: dec(c.settle)}})
			if err == nil {
				t.Fatal("the offset was offered as settlement and accepted, " +
					"so it is counted twice")
			}
			if !strings.Contains(err.Error(), ExchangeClearing) {
				t.Errorf("the refusal does not name the method: %v", err)
			}
		})
	}
}

// What the old code relied on, recorded so it is clear why refusing early is an
// improvement rather than a second opinion.
//
// check() DID catch a smuggled clearing leg -- it is the only reason this was
// not a way to strand money through the exchange path as well. But it caught it
// after both documents had been written, and it reported "this exchange leaves
// 50.00 in the clearing account": an internal error naming a symptom, when the
// cause was a request that named a method it may not name.
func TestASmuggledClearingLegWouldHaveBrokenThePairing(t *testing.T) {
	// Built by hand, exactly as planSettlement would have built it before the
	// refusal above existed: its own offsetting pair, plus the offered leg
	// appended to the invoice.
	smuggled := settlement{
		credit:     dec("100.00"),
		difference: dec("50.00"),
		refunds:    []Refund{{Method: ExchangeClearing, Amount: dec("100.00")}},
		tenders: []Tender{
			{Method: ExchangeClearing, Amount: dec("100.00")},
			{Method: ExchangeClearing, Amount: dec("50.00")},
		},
	}

	if err := smuggled.check(); err == nil {
		t.Fatal("a doubled clearing leg nets to zero, which would mean the " +
			"invariant the whole mechanism rests on does not hold")
	}
}

// The guard must not be broader than the defect. Every method a customer can
// really pay or be refunded with has to keep working, including the ones that
// are not cash and the ones that are a balance rather than money.
func TestEverySettlementMethodACustomerCanUseIsStillOfferable(t *testing.T) {
	methods := []string{
		"cash", "card", "mada", "visa", "mastercard", "wallet", "stc_pay",
		"bank_transfer", "cheque", "sadad", "customer_due", "store_credit",
		"loyalty_points", "bnpl",
	}

	if err := checkMethodsAreOfferable(methods); err != nil {
		t.Errorf("an ordinary settlement method was refused: %v", err)
	}

	// And each on its own, so a future reserved method cannot quietly take one
	// of these with it.
	for _, m := range methods {
		if err := checkMethodsAreOfferable([]string{m}); err != nil {
			t.Errorf("%q was refused: %v", m, err)
		}
	}
}

// Nothing offered is not something offered. An even exchange offers no
// settlement at all, and a sale wholly on account has no tender of its own, so
// the guard has to pass an empty list rather than treat it as a special case.
func TestNoMethodsOfferedIsAccepted(t *testing.T) {
	for _, methods := range [][]string{nil, {}} {
		if err := checkMethodsAreOfferable(methods); err != nil {
			t.Errorf("%v was refused: %v", methods, err)
		}
	}
	if got := tenderMethods(nil); len(got) != 0 {
		t.Errorf("%d methods from no tenders", len(got))
	}
	if got := refundMethods(nil); len(got) != 0 {
		t.Errorf("%d methods from no refunds", len(got))
	}
}

// The methods are read off in order and none is skipped, which is what makes
// the reserved one findable wherever a till put it.
func TestEveryOfferedMethodIsChecked(t *testing.T) {
	tenders := []Tender{cash("1.00"), {Method: "mada", Amount: dec("2.00")}}
	if got := tenderMethods(tenders); len(got) != 2 ||
		got[0] != "cash" || got[1] != "mada" {
		t.Errorf("tenderMethods gave %v, want [cash mada]", got)
	}

	refunds := []Refund{
		{Method: "cash", Amount: dec("1.00")},
		{Method: "store_credit", Amount: dec("2.00")},
	}
	if got := refundMethods(refunds); len(got) != 2 ||
		got[0] != "cash" || got[1] != "store_credit" {
		t.Errorf("refundMethods gave %v, want [cash store_credit]", got)
	}

	// Last position included, which an off-by-one in the loop would miss and
	// which is exactly where a till appending a tender would put it.
	err := checkMethodsAreOfferable([]string{
		"cash", "mada", "store_credit", ExchangeClearing,
	})
	if err == nil {
		t.Error("a reserved method in the last position was not found")
	}
}
