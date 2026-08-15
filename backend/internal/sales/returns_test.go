package sales

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func origLine(qty, net, tax, cogs, discAlloc string) OriginalLine {
	return OriginalLine{
		LineID: uuid.New(), LineNo: 1, VariantID: uuid.New(),
		Description: "Executive Abaya",
		QtySold:     dec(qty), QtyReturned: decimal.Zero,
		UnitPrice:    dec("1150.00"),
		TaxTreatment: "standard", TaxRate: dec("0.15"),
		NetAmount: dec(net), TaxAmount: dec(tax),
		COGSAmount: dec(cogs), InvoiceDiscountAlloc: dec(discAlloc),
	}
}

// Returning everything must give back exactly what was charged — not what a
// proportion happens to compute. This is the case that a naive
// proportion x amount gets wrong: 100.00 across 3 units is 33.33 each, and
// three of those is 99.99.
func TestFullReturnRefundsExactlyWhatWasCharged(t *testing.T) {
	orig := origLine("3", "100.00", "15.00", "60.00", "9.00")

	got, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("3")}})
	if err != nil {
		t.Fatalf("ComputeReturn: %v", err)
	}

	if !got.SubtotalNet.Equal(dec("100.00")) {
		t.Errorf("net credited = %s, want exactly the 100.00 charged", got.SubtotalNet)
	}
	if !got.TaxTotal.Equal(dec("15.00")) {
		t.Errorf("tax credited = %s, want exactly the 15.00 charged", got.TaxTotal)
	}
	if !got.COGSTotal.Equal(dec("60.00")) {
		t.Errorf("COGS reversed = %s, want exactly the 60.00 booked", got.COGSTotal)
	}
	if !got.DiscountTotal.Equal(dec("9.00")) {
		t.Errorf("discount reversed = %s, want exactly the 9.00 allocated", got.DiscountTotal)
	}
	if !got.Lines[0].IsFullReturn {
		t.Error("a return of the whole quantity was not treated as a full return")
	}
}

// A genuinely partial return is proportional across all four figures, not just
// the revenue. C14 calls out proportional VAT, discount and COGS specifically.
func TestPartialReturnIsProportionalAcrossEveryFigure(t *testing.T) {
	orig := origLine("4", "400.00", "60.00", "240.00", "40.00")

	got, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("1")}})
	if err != nil {
		t.Fatalf("ComputeReturn: %v", err)
	}

	l := got.Lines[0]
	if l.IsFullReturn {
		t.Error("a one-of-four return was treated as a full return")
	}
	for _, c := range []struct {
		name string
		got  decimal.Decimal
		want string
	}{
		{"net", l.NetAmount, "100.00"},
		{"tax", l.TaxAmount, "15.00"},
		{"COGS", l.COGSAmount, "60.00"},
		{"discount allocation", l.InvoiceDiscountAlloc, "10.00"},
	} {
		if !c.got.Equal(dec(c.want)) {
			t.Errorf("%s reversed = %s, want %s", c.name, c.got, c.want)
		}
	}
}

// Successive partial returns must never exceed what was sold, and the
// remaining quantity must shrink as they accumulate.
func TestSuccessivePartialReturnsCannotExceedTheOriginal(t *testing.T) {
	orig := origLine("5", "500.00", "75.00", "300.00", "0")
	orig.QtyReturned = dec("3") // two earlier returns already took 3

	// Two more is fine.
	if _, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("2")}}); err != nil {
		t.Fatalf("returning the last 2 of 5 was refused: %v", err)
	}

	// Three is one too many.
	_, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("3")}})
	if err == nil {
		t.Fatal("returned 3 when only 2 remained; the customer would be refunded " +
			"more than they paid")
	}
	// The message must state the arithmetic, since the cashier has a customer
	// waiting and needs to know what to offer.
	for _, want := range []string{"2", "5", "3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// Returning against a fully-returned line says so plainly rather than quoting
// a remaining quantity of zero.
func TestFullyReturnedLineSaysSo(t *testing.T) {
	orig := origLine("2", "200.00", "30.00", "120.00", "0")
	orig.QtyReturned = dec("2")

	_, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("1")}})
	if err == nil {
		t.Fatal("an already fully-returned line accepted another return")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "already been returned in full") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// The same line twice in one request would pass the remaining-quantity check
// individually while exceeding it together.
func TestDuplicateLineInOneReturnIsRefused(t *testing.T) {
	orig := origLine("2", "200.00", "30.00", "120.00", "0")

	_, err := ComputeReturn([]OriginalLine{orig}, []ReturnRequest{
		{LineID: orig.LineID, Qty: dec("1")},
		{LineID: orig.LineID, Qty: dec("2")},
	})
	if err == nil {
		t.Fatal("the same line was returned twice in one credit note, together " +
			"exceeding what was sold")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "twice") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// A zero-rated line refunds no tax, because none was charged.
func TestZeroRatedLineRefundsNoTax(t *testing.T) {
	orig := origLine("1", "100.00", "0", "60.00", "0")
	orig.TaxTreatment = "zero_rated"
	orig.TaxRate = decimal.Zero

	got, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("1")}})
	if err != nil {
		t.Fatalf("ComputeReturn: %v", err)
	}
	if !got.TaxTotal.IsZero() {
		t.Fatalf("tax credited = %s on a zero-rated line", got.TaxTotal)
	}
	if !got.TotalInclusive.Equal(dec("100.00")) {
		t.Fatalf("total = %s, want 100.00", got.TotalInclusive)
	}
}

// The rate stored on the original travels with the return, so a credit note
// raised after a rate change still reverses what was actually charged.
func TestReturnUsesTheRateThatWasCharged(t *testing.T) {
	// Sold under a 5% regime; today's standard rate is 15%.
	orig := origLine("1", "100.00", "5.00", "60.00", "0")
	orig.TaxRate = dec("0.05")

	got, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("1")}})
	if err != nil {
		t.Fatalf("ComputeReturn: %v", err)
	}
	if !got.TaxTotal.Equal(dec("5.00")) {
		t.Fatalf("tax credited = %s, want the 5.00 originally charged. Refunding "+
			"at today's rate would return money that was never collected.", got.TaxTotal)
	}
	if !got.Lines[0].TaxRate.Equal(dec("0.05")) {
		t.Errorf("credit line carries rate %s, want the original 0.05", got.Lines[0].TaxRate)
	}
}

// A multi-line return sums correctly and each line keeps its own treatment.
func TestMultiLineReturn(t *testing.T) {
	taxable := origLine("2", "200.00", "30.00", "120.00", "0")
	exempt := origLine("1", "100.00", "0", "70.00", "0")
	exempt.TaxTreatment = "exempt"
	exempt.TaxRate = decimal.Zero
	exempt.Description = "Exempt item"

	got, err := ComputeReturn([]OriginalLine{taxable, exempt}, []ReturnRequest{
		{LineID: taxable.LineID, Qty: dec("2")},
		{LineID: exempt.LineID, Qty: dec("1")},
	})
	if err != nil {
		t.Fatalf("ComputeReturn: %v", err)
	}

	if !got.SubtotalNet.Equal(dec("300.00")) {
		t.Errorf("net = %s, want 300.00", got.SubtotalNet)
	}
	if !got.TaxTotal.Equal(dec("30.00")) {
		t.Errorf("tax = %s, want 30.00 — only the taxable line carried tax", got.TaxTotal)
	}
	if !got.TotalInclusive.Equal(dec("330.00")) {
		t.Errorf("total = %s, want 330.00", got.TotalInclusive)
	}
	if !got.COGSTotal.Equal(dec("190.00")) {
		t.Errorf("COGS = %s, want 190.00", got.COGSTotal)
	}
}

func TestReturnOfALineNotOnTheInvoiceIsRefused(t *testing.T) {
	orig := origLine("1", "100.00", "15.00", "60.00", "0")

	_, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: uuid.New(), Qty: dec("1")}})
	if err == nil {
		t.Fatal("a line that is not on the invoice was returned")
	}
}

func TestEmptyReturnIsRefused(t *testing.T) {
	orig := origLine("1", "100.00", "15.00", "60.00", "0")
	if _, err := ComputeReturn([]OriginalLine{orig}, nil); err == nil {
		t.Fatal("an empty return was accepted")
	}
}

func TestNegativeReturnQuantityIsRefused(t *testing.T) {
	orig := origLine("2", "200.00", "30.00", "120.00", "0")
	_, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("-1")}})
	if err == nil {
		t.Fatal("a negative return quantity was accepted, which would credit the " +
			"customer for buying more")
	}
}

// The refund must settle the credit note exactly, for the same reason a sale
// must be paid in full.
func TestRefundMustSettleTheCreditNoteExactly(t *testing.T) {
	total := dec("330.00")

	if err := ValidateRefunds(total, []decimal.Decimal{dec("200.00"), dec("130.00")}); err != nil {
		t.Fatalf("an exact split refund was refused: %v", err)
	}
	if err := ValidateRefunds(total, []decimal.Decimal{dec("300.00")}); err == nil {
		t.Fatal("a short refund was accepted")
	}
	if err := ValidateRefunds(total, []decimal.Decimal{dec("400.00")}); err == nil {
		t.Fatal("a refund larger than the credit note was accepted")
	}
}

// C14's nine effects. The checklist exists because the ones that get forgotten
// are rarely the obvious four — loyalty points and commission are real money
// leaving through a gap nobody notices for months.
func TestAllNineEffectsAreRequired(t *testing.T) {
	full := ReturnEffects{
		InventoryRestored: true, RevenueReversed: true, OutputTaxReversed: true,
		COGSReversed: true, RefundSettled: true, LoyaltyReversed: true,
		CommissionReversed: true, CreditNoteIssued: true, JournalPosted: true,
	}
	if !full.Complete() {
		t.Fatal("all nine effects present but the return was reported incomplete")
	}
	if len(full.Missing()) != 0 {
		t.Fatalf("complete return reports missing effects: %v", full.Missing())
	}

	// The two most commonly forgotten.
	partial := full
	partial.LoyaltyReversed = false
	partial.CommissionReversed = false

	if partial.Complete() {
		t.Fatal("a return that left loyalty points and commission in place was " +
			"reported complete; both are money leaving the business")
	}
	missing := partial.Missing()
	if len(missing) != 2 {
		t.Fatalf("missing = %v, want exactly the two skipped effects", missing)
	}
	joined := strings.Join(missing, "; ")
	if !strings.Contains(joined, "loyalty") || !strings.Contains(joined, "commission") {
		t.Fatalf("the report does not name what was skipped: %v", missing)
	}
}

// Partial returns of a line whose amounts do not divide evenly must never
// credit more than was charged, however many times they are taken.
func TestRepeatedPartialReturnsNeverExceedTheOriginal(t *testing.T) {
	// 100.00 net across 3 units: 33.33 each, leaving a hallala over.
	orig := origLine("3", "100.00", "15.00", "60.00", "0")

	credited := decimal.Zero
	creditedTax := decimal.Zero
	returned := decimal.Zero

	for i := 0; i < 3; i++ {
		o := orig
		// Carry forward what earlier returns already credited, exactly as the
		// service reads it back from the stored credit notes.
		o.QtyReturned = returned
		o.NetReturned = credited
		o.TaxReturned = creditedTax

		got, err := ComputeReturn([]OriginalLine{o},
			[]ReturnRequest{{LineID: o.LineID, Qty: dec("1")}})
		if err != nil {
			t.Fatalf("return %d: %v", i+1, err)
		}
		credited = credited.Add(got.SubtotalNet)
		creditedTax = creditedTax.Add(got.TaxTotal)
		returned = returned.Add(dec("1"))
	}

	if !creditedTax.Equal(dec("15.00")) {
		t.Fatalf("three partial returns credited %s of tax against 15.00 charged; "+
			"the VAT return would not reconcile", creditedTax)
	}

	if credited.GreaterThan(dec("100.00")) {
		t.Fatalf("three partial returns credited %s against a charge of 100.00; "+
			"the business paid out money it never took", credited)
	}
	// The final unit is a full return of what remains, so it absorbs the
	// rounding and the customer is made whole.
	if !credited.Equal(dec("100.00")) {
		t.Fatalf("three partial returns credited %s, want the full 100.00 back", credited)
	}
}
