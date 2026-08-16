package sales

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// How an exchange settles is the whole of it. Get the arithmetic wrong and the
// shop either gives goods away or takes money it is not owed, and neither
// shows up until somebody counts the drawer.

func planned(t *testing.T, credit, replacement string, offered ...Tender) settlement {
	t.Helper()
	p, err := planSettlement(dec(credit), dec(replacement), offered)
	if err != nil {
		t.Fatalf("planSettlement(%s, %s): %v", credit, replacement, err)
	}
	return p
}

func cash(amount string) Tender {
	return Tender{Method: "cash", Amount: dec(amount)}
}

// The ordinary case: the customer swaps up and pays the difference.
func TestExchangeUpwardTakesOnlyTheDifference(t *testing.T) {
	p := planned(t, "100.00", "150.00", cash("50.00"))

	if !p.difference.Equal(dec("50.00")) {
		t.Errorf("difference = %s, want 50.00", p.difference)
	}
	if !p.credit.Equal(dec("100.00")) {
		t.Errorf("credit applied = %s, want the whole 100.00 coming back", p.credit)
	}

	// The customer hands over 50, not 150. Anything else would have the drawer
	// expected to hold cash that never passed through it.
	if got := cashIn(p.tenders); !got.Equal(dec("50.00")) {
		t.Errorf("cash taken = %s, want 50.00", got)
	}
	if got := cashIn(refundTenders(p.refunds)); !got.IsZero() {
		t.Errorf("cash paid out = %s, want none on an upward swap", got)
	}
	if err := p.check(); err != nil {
		t.Errorf("clearing does not net to zero: %v", err)
	}
}

// Swapping down: the shop hands the difference back, and it goes out through
// the credit note so the day's cash-out is right.
func TestExchangeDownwardRefundsOnlyTheDifference(t *testing.T) {
	p := planned(t, "150.00", "100.00", cash("50.00"))

	if !p.difference.Equal(dec("-50.00")) {
		t.Errorf("difference = %s, want -50.00", p.difference)
	}
	if !p.credit.Equal(dec("100.00")) {
		t.Errorf("credit applied = %s, want the 100.00 that offsets", p.credit)
	}

	if got := cashIn(refundTenders(p.refunds)); !got.Equal(dec("50.00")) {
		t.Errorf("cash paid out = %s, want 50.00", got)
	}
	if got := cashIn(p.tenders); !got.IsZero() {
		t.Errorf("cash taken = %s, want none on a downward swap", got)
	}
	if err := p.check(); err != nil {
		t.Errorf("clearing does not net to zero: %v", err)
	}
}

// An even swap moves no money at all, and must not pretend otherwise.
func TestEvenExchangeMovesNoMoney(t *testing.T) {
	p := planned(t, "120.00", "120.00")

	if !p.difference.IsZero() {
		t.Errorf("difference = %s, want zero", p.difference)
	}
	if got := cashIn(p.tenders); !got.IsZero() {
		t.Errorf("cash taken = %s on an even swap", got)
	}
	if got := cashIn(refundTenders(p.refunds)); !got.IsZero() {
		t.Errorf("cash paid out = %s on an even swap", got)
	}

	// Both documents still settle in full — through the clearing account.
	if !p.refunds[0].Amount.Equal(dec("120.00")) {
		t.Errorf("credit note settled %s, want the full 120.00", p.refunds[0].Amount)
	}
	if !p.tenders[0].Amount.Equal(dec("120.00")) {
		t.Errorf("invoice settled %s, want the full 120.00", p.tenders[0].Amount)
	}
	if err := p.check(); err != nil {
		t.Errorf("clearing does not net to zero: %v", err)
	}
}

// The invariant the mechanism rests on: whatever the shape, the clearing
// account is empty when the transaction ends.
func TestClearingAlwaysNetsToZero(t *testing.T) {
	for _, c := range []struct{ credit, replacement, settle string }{
		{"100.00", "150.00", "50.00"},
		{"150.00", "100.00", "50.00"},
		{"120.00", "120.00", ""},
		{"0.01", "999.99", "999.98"},
		{"999.99", "0.01", "999.98"},
		{"33.33", "66.67", "33.34"},
	} {
		var offered []Tender
		if c.settle != "" {
			offered = []Tender{cash(c.settle)}
		}
		p := planned(t, c.credit, c.replacement, offered...)
		if err := p.check(); err != nil {
			t.Errorf("credit %s -> replacement %s: %v", c.credit, c.replacement, err)
		}
		// And the offsetting amount is never more than either side.
		if p.credit.GreaterThan(dec(c.credit)) ||
			p.credit.GreaterThan(dec(c.replacement)) {
			t.Errorf("credit %s -> replacement %s applied %s, more than one side holds",
				c.credit, c.replacement, p.credit)
		}
	}
}

// The server states the difference; the till does not get to disagree with it.
func TestExchangeRefusesTheWrongSettlement(t *testing.T) {
	for _, c := range []struct{ name, offered string }{
		{"too little", "40.00"},
		{"too much", "60.00"},
		{"nothing at all", ""},
	} {
		var offered []Tender
		if c.offered != "" {
			offered = []Tender{cash(c.offered)}
		}
		_, err := planSettlement(dec("100.00"), dec("150.00"), offered)
		if err == nil {
			t.Errorf("%s: settled 50.00 with %q", c.name, c.offered)
			continue
		}
		// The message names both figures, because a cashier looking at a
		// rejected screen needs to know which way it is out.
		if !strings.Contains(err.Error(), "50") {
			t.Errorf("%s: message does not name the difference: %v", c.name, err)
		}
	}
}

// An even swap needs nothing offered, and offering something is a mistake
// rather than a courtesy — it would leave the customer short.
func TestEvenExchangeRefusesAnyPayment(t *testing.T) {
	if _, err := planSettlement(dec("120.00"), dec("120.00"),
		[]Tender{cash("10.00")}); err == nil {
		t.Error("an even swap accepted a payment of 10.00")
	}
}

// Neither half may be empty. An exchange with nothing coming back is a sale,
// and one with nothing going out is a return; both have their own paths, and
// routing them through here would produce a credit note or an invoice for
// nothing.
func TestExchangeNeedsBothHalves(t *testing.T) {
	if _, err := planSettlement(decimal.Zero, dec("100.00"), nil); err == nil {
		t.Error("accepted an exchange with nothing coming back")
	} else if !strings.Contains(err.Error(), "sale") {
		t.Errorf("message does not point at the right path: %v", err)
	}

	if _, err := planSettlement(dec("100.00"), decimal.Zero, nil); err == nil {
		t.Error("accepted an exchange with nothing going out")
	} else if !strings.Contains(err.Error(), "return") {
		t.Errorf("message does not point at the right path: %v", err)
	}
}

func TestExchangeRefusesNegativeAmounts(t *testing.T) {
	if _, err := planSettlement(dec("-1.00"), dec("100.00"), nil); err == nil {
		t.Error("accepted a negative credit")
	}
	if _, err := planSettlement(dec("100.00"), dec("-1.00"), nil); err == nil {
		t.Error("accepted a negative replacement")
	}
	if _, err := planSettlement(dec("100.00"), dec("150.00"),
		[]Tender{{Method: "cash", Amount: dec("-50.00")}}); err == nil {
		t.Error("accepted a negative settlement")
	}
}

// Card money must be able to go back the way it came, and a split difference
// is an ordinary thing at a counter.
func TestExchangeSettlesAcrossSeveralTenders(t *testing.T) {
	p := planned(t, "100.00", "180.00",
		cash("30.00"), Tender{Method: "mada", Amount: dec("50.00")})

	if !p.difference.Equal(dec("80.00")) {
		t.Errorf("difference = %s, want 80.00", p.difference)
	}
	// Three tenders on the invoice: the clearing offset plus the two payments.
	if len(p.tenders) != 3 {
		t.Fatalf("invoice carries %d tenders, want 3", len(p.tenders))
	}
	if err := p.check(); err != nil {
		t.Errorf("clearing does not net to zero: %v", err)
	}
}

// The clearing method never reaches the drawer. cash_session_cash_in filters on
// method = 'cash', so this is what keeps the blind Z-count honest.
func TestClearingIsNeverCash(t *testing.T) {
	p := planned(t, "100.00", "150.00", cash("50.00"))

	// The 100 that offsets must not appear in the cash total. Only the 50 the
	// customer actually handed over does.
	if got := cashIn(p.tenders); !got.Equal(dec("50.00")) {
		t.Errorf("cash on the invoice = %s, want only the 50.00 that moved", got)
	}
	for _, td := range p.tenders {
		if td.Method == ExchangeClearing && !td.Amount.Equal(dec("100.00")) {
			t.Errorf("clearing tender is %s, want the 100.00 offset", td.Amount)
		}
	}
	// And it posts to its own account rather than borrowing another's.
	if role := tenderRole(ExchangeClearing); role != ExchangeClearing {
		t.Errorf("clearing posts to %q, want its own account", role)
	}
	// Specifically not store credit, which is a real customer balance.
	if tenderRole(ExchangeClearing) == tenderRole("store_credit") {
		t.Error("exchange clearing shares an account with store credit")
	}
}

func cashIn(tenders []Tender) decimal.Decimal {
	sum := decimal.Zero
	for _, t := range tenders {
		if t.Method == "cash" {
			sum = sum.Add(t.Amount)
		}
	}
	return sum
}

func refundTenders(refunds []Refund) []Tender {
	out := make([]Tender, len(refunds))
	for i, r := range refunds {
		out[i] = Tender{Method: r.Method, Amount: r.Amount}
	}
	return out
}
