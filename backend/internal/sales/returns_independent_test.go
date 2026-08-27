package sales

import (
	"testing"

	"github.com/shopspring/decimal"
)

// Return arithmetic checked as invariants over many shapes, rather than one
// worked example per rule.
//
// The existing return tests each pin one case, and they pin it well. What they
// cannot do is tell you whether the rule holds for the shape nobody thought
// of: a line of 7 returned one at a time, a line worth a single hallala split
// three ways, a return sequence that arrives in the awkward order. Money that
// leaks a hallala per partial return leaks it quietly, and a reconciliation
// months later is where it surfaces.
//
// So this file states the four things that must be true of ANY return sequence
// and then drives a lot of sequences through them.

// returnSequence plays a list of return quantities against one line, carrying
// forward what each earlier return credited exactly as the service does when
// it reads the stored credit notes back.
//
// It returns the totals credited across the whole sequence.
type credited struct {
	net, tax, discount, cogs, qty decimal.Decimal
}

func playReturns(t *testing.T, orig OriginalLine, quantities []string) credited {
	t.Helper()
	var c credited
	c.net, c.tax, c.discount, c.cogs, c.qty =
		decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero

	for i, q := range quantities {
		o := orig
		o.QtyReturned = c.qty
		o.NetReturned = c.net
		o.TaxReturned = c.tax
		o.DiscountAllocReturned = c.discount
		o.COGSReturned = c.cogs

		got, err := ComputeReturn([]OriginalLine{o},
			[]ReturnRequest{{LineID: o.LineID, Qty: dec(q)}})
		if err != nil {
			t.Fatalf("return %d of %v: %v", i+1, quantities, err)
		}

		// No individual credit may be negative. A negative credit note line is
		// a charge dressed as a refund.
		for _, l := range got.Lines {
			if l.NetAmount.IsNegative() || l.TaxAmount.IsNegative() ||
				l.GrossAmount.IsNegative() || l.COGSAmount.IsNegative() {
				t.Errorf("return %d credited a negative amount: net=%s tax=%s cogs=%s",
					i+1, l.NetAmount, l.TaxAmount, l.COGSAmount)
			}
			if !l.NetAmount.Add(l.TaxAmount).Equal(l.GrossAmount) {
				t.Errorf("return %d: %s + %s is not %s",
					i+1, l.NetAmount, l.TaxAmount, l.GrossAmount)
			}
		}

		c.net = c.net.Add(got.SubtotalNet)
		c.tax = c.tax.Add(got.TaxTotal)
		c.discount = c.discount.Add(got.DiscountTotal)
		c.cogs = c.cogs.Add(got.COGSTotal)
		c.qty = c.qty.Add(dec(q))
	}
	return c
}

// Whatever order and whatever sizes, returning a line in full credits exactly
// what was charged for it -- to the hallala, on every figure.
//
// Not approximately. A shortfall means the customer is out of pocket; an
// excess means the business refunded money it never took; and either one puts
// the VAT return out, because the tax credited has to match the tax charged.
func TestAnyFullReturnSequenceCreditsExactlyWhatWasCharged(t *testing.T) {
	lines := map[string]OriginalLine{
		// The classic: 100 across 3 does not divide.
		"three into a hundred": origLine("3", "100.00", "15.00", "60.00", "9.00"),
		// Seven is worse, and the tax is small enough to vanish per unit.
		"seven into one":  origLine("7", "1.00", "0.15", "0.70", "0.07"),
		"seven into ten":  origLine("7", "10.00", "1.50", "6.00", "0.33"),
		"a single unit":   origLine("1", "33.33", "5.00", "20.00", "1.11"),
		"a whole hallala": origLine("3", "0.01", "0.00", "0.01", "0.00"),
		"large line":      origLine("13", "9999999.99", "1499999.99", "5000000.00", "77777.77"),
		"no discount":     origLine("6", "50.00", "7.50", "30.00", "0"),
		"no cogs":         origLine("4", "19.99", "3.00", "0", "1.00"),
	}

	for name, orig := range lines {
		t.Run(name, func(t *testing.T) {
			qty := orig.QtySold

			// Every sequence below must return the whole quantity.
			plans := [][]string{}
			plans = append(plans, []string{qty.String()}) // all at once

			ones := []string{}
			for i := decimal.Zero; i.LessThan(qty); i = i.Add(dec("1")) {
				ones = append(ones, "1")
			}
			if len(ones) > 0 && decimal.NewFromInt(int64(len(ones))).Equal(qty) {
				plans = append(plans, ones) // one at a time
			}

			// Uneven: as much as possible, then the rest one at a time.
			if qty.GreaterThan(dec("2")) {
				head := qty.Sub(dec("2")).String()
				plans = append(plans, []string{head, "1", "1"})
				plans = append(plans, []string{"1", head, "1"})
			}

			for _, plan := range plans {
				got := playReturns(t, orig, plan)

				if !got.net.Equal(orig.NetAmount) {
					t.Errorf("%v credited %s net against %s charged",
						plan, got.net, orig.NetAmount)
				}
				if !got.tax.Equal(orig.TaxAmount) {
					t.Errorf("%v credited %s tax against %s charged -- the VAT "+
						"return would not reconcile", plan, got.tax, orig.TaxAmount)
				}
				if !got.discount.Equal(orig.InvoiceDiscountAlloc) {
					t.Errorf("%v credited %s of discount against %s allocated",
						plan, got.discount, orig.InvoiceDiscountAlloc)
				}
				if !got.cogs.Equal(orig.COGSAmount) {
					t.Errorf("%v credited %s of cost against %s booked -- gross "+
						"profit would be wrong for good", plan, got.cogs, orig.COGSAmount)
				}
			}
		})
	}
}

// THE DEFECT THIS FILE FOUND.
//
// A hundred sachets at a net of 30.60, brought back one at a time.
//
// Worked by hand: each unit's own share is round(30.60 / 100, 2) = 0.31 --
// rounded UP, because the true share is 0.306. Ninety-nine of those come to
// 30.69, already more than the line was ever worth, and the hundredth return
// was handed 30.60 - 30.69 = MINUS 0.09. The tax is worse: 4.59 across a
// hundred rounds to 0.05 a unit, which is 4.95 over ninety-nine, and the last
// return came out at -0.36.
//
// A negative credit is not a small inaccuracy. usableLines refuses a negative
// amount rather than writing a negative debit, so the credit note could not be
// posted -- and a customer at the counter with the last sachet of a hundred
// they had every right to bring back would be told the till cannot take it, on
// that attempt and on every retry.
//
// None of the shapes above reaches this. It takes a net below
// 0.005 x qty x (qty - 1) for the accumulated half-hallalas to outrun the line,
// which means a quantity in the dozens and a unit price in small change --
// sweets, sachets, fasteners, the things a grocery sells by the piece.
func TestManyCheapUnitsReturnedOneAtATimeNeverCreditANegative(t *testing.T) {
	orig := origLine("100", "30.60", "4.59", "18.40", "1.20")

	ones := make([]string, 100)
	for i := range ones {
		ones[i] = "1"
	}

	// playReturns refuses a negative credit on any return in the sequence,
	// which is the assertion this test exists for.
	got := playReturns(t, orig, ones)

	// And a hundred credits still have to come to exactly what was charged.
	for _, c := range []struct {
		what      string
		got, want decimal.Decimal
	}{
		{"net", got.net, orig.NetAmount},
		{"tax", got.tax, orig.TaxAmount},
		{"cost", got.cogs, orig.COGSAmount},
		{"discount", got.discount, orig.InvoiceDiscountAlloc},
	} {
		if !c.got.Equal(c.want) {
			t.Errorf("a hundred returns credited %s of %s against %s charged",
				c.got, c.what, c.want)
		}
	}
}

// Weighed goods, brought back in fractions of a kilo.
//
// Nothing in the rule needs a whole number, and a grocery that sells dates by
// weight takes them back the same way. These quantities sum to the 2.5 kg sold
// without one of them being a unit, and the credits still have to come to
// exactly what was charged -- which the one-at-a-time sequences above cannot
// say, because they can only count in ones.
func TestAWeighedLineReturnedInFractionsCreditsExactly(t *testing.T) {
	orig := origLine("2.5", "87.53", "13.13", "52.50", "3.11")

	got := playReturns(t, orig, []string{"0.25", "0.75", "0.4", "0.6", "0.5"})

	if !got.qty.Equal(dec("2.5")) {
		t.Fatalf("the sequence returned %s kg, expected 2.5", got.qty)
	}
	if !got.net.Equal(orig.NetAmount) {
		t.Errorf("credited %s net against %s charged", got.net, orig.NetAmount)
	}
	if !got.tax.Equal(orig.TaxAmount) {
		t.Errorf("credited %s tax against %s charged", got.tax, orig.TaxAmount)
	}
	if !got.cogs.Equal(orig.COGSAmount) {
		t.Errorf("credited %s of cost against %s booked", got.cogs, orig.COGSAmount)
	}
}

// A sequence that stops short must have credited strictly less than the whole,
// on every figure. Otherwise a customer could be refunded in full for a
// partial return and keep the goods.
func TestAPartialSequenceNeverCreditsTheWholeLine(t *testing.T) {
	orig := origLine("10", "100.00", "15.00", "60.00", "9.00")

	// Nine of ten back: everything credited must still be short of the whole.
	got := playReturns(t, orig, []string{"1", "2", "3", "3"})

	if !got.qty.Equal(dec("9")) {
		t.Fatalf("the sequence returned %s units, expected 9", got.qty)
	}
	for _, c := range []struct {
		what      string
		got, full decimal.Decimal
	}{
		{"net", got.net, orig.NetAmount},
		{"tax", got.tax, orig.TaxAmount},
		{"cost", got.cogs, orig.COGSAmount},
	} {
		if c.got.GreaterThanOrEqual(c.full) {
			t.Errorf("nine of ten units back credited %s of %s (%s), which is "+
				"the whole line or more", c.got, c.full, c.what)
		}
	}
}

// The line that exhausts the quantity is the one that absorbs the rounding, so
// it must credit the remainder rather than a proportion -- and it must be
// marked as the full return, because the caller uses that to close the line.
func TestTheLastReturnAbsorbsTheRoundingAndIsMarkedFull(t *testing.T) {
	// 100.00 across 3: 33.33 twice leaves 33.34 for the last.
	orig := origLine("3", "100.00", "15.00", "60.00", "9.00")

	o := orig
	o.QtyReturned = dec("2")
	o.NetReturned = dec("66.66")
	o.TaxReturned = dec("10.00")
	o.DiscountAllocReturned = dec("6.00")
	o.COGSReturned = dec("40.00")

	got, err := ComputeReturn([]OriginalLine{o},
		[]ReturnRequest{{LineID: o.LineID, Qty: dec("1")}})
	if err != nil {
		t.Fatalf("final return: %v", err)
	}

	l := got.Lines[0]
	if !l.IsFullReturn {
		t.Error("the return that exhausts the line is not marked as a full return")
	}
	// The remainder, not a third.
	eq(t, "net", l.NetAmount, "33.34")
	eq(t, "tax", l.TaxAmount, "5.00")
	eq(t, "discount", l.InvoiceDiscountAlloc, "3.00")
	eq(t, "cost", l.COGSAmount, "20.00")
}

// Returning more than remains must be refused with the numbers in the message,
// because a cashier holding goods needs to know how many the till will take.
func TestReturningMoreThanRemainsIsRefusedWithTheNumbers(t *testing.T) {
	orig := origLine("5", "100.00", "15.00", "60.00", "0")
	orig.QtyReturned = dec("3")

	_, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("3")}})
	if err == nil {
		t.Fatal("a return of three was accepted against two remaining")
	}
	for _, want := range []string{"2", "5", "3"} {
		if !containsDigit(err.Error(), want) {
			t.Errorf("the message does not carry %q: %s", want, err)
		}
	}
}

func containsDigit(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// A line already fully returned says so, rather than reporting a quantity of
// zero remaining and leaving the cashier to work out what that means.
func TestAnExhaustedLineSaysItIsAlreadyBack(t *testing.T) {
	orig := origLine("2", "100.00", "15.00", "60.00", "0")
	orig.QtyReturned = dec("2")

	_, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("1")}})
	if err == nil {
		t.Fatal("a return was accepted against a fully returned line")
	}
	if !containsDigit(err.Error(), "already been returned in full") {
		t.Errorf("the message does not say the line is exhausted: %s", err)
	}
}

// Credits must never be computed from the current tax rate. A return years
// later credits the rate that was actually charged, which is why the rate
// travels on the stored line.
func TestACreditUsesTheStoredRateNotTodays(t *testing.T) {
	orig := origLine("1", "100.00", "5.00", "60.00", "0")
	orig.TaxRate = dec("0.05") // an old, lower rate

	got, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("1")}})
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	eq(t, "tax", got.TaxTotal, "5.00")
	eq(t, "rate", got.Lines[0].TaxRate, "0.05")
}

// THE SECOND DEFECT THIS FILE FOUND, and the one that cost a shop its stock
// records.
//
// A hundred pieces at a net of 0.50, brought back two at a time. Worked by
// hand: the first piece's cumulative target is round(0.50 x 1/100, 2) =
// round(0.005, 2) = 0.01, so it credits a hallala. The second piece's target is
// round(0.50 x 2/100, 2) = round(0.01, 2) = 0.01 -- the SAME hallala -- so the
// second return credits nothing at all. Correctly: two pieces of a fifty-hallala
// line are worth one hallala between them, and the first return already gave it
// back.
//
// The cost does not move in step, and that is the whole point of this test. Cost
// 0.30 across a hundred targets round(0.003, 2) = 0.00 on the first piece and
// round(0.006, 2) = 0.01 on the second, so the return that credits NO money
// still has a hallala of stock value to put back.
//
// postReturn used to post the revenue reversal unconditionally. With every
// amount zero, usableLines drops every line, and PostByRule refuses with "an
// accounting entry needs at least one debit and one credit" -- and because the
// whole return is one transaction, the refusal took inventory.Restore down with
// it. The goods were on the counter, the records still said sold, and every
// retry failed identically. So the guard cannot be an early return either: skip
// the reversal, still post the cost.
func TestAPartialReturnCanCreditNothingAndStillHaveACostToReverse(t *testing.T) {
	orig := origLine("100", "0.50", "0.08", "0.30", "0")

	// The first piece back, which takes the line's only hallala.
	first, err := ComputeReturn([]OriginalLine{orig},
		[]ReturnRequest{{LineID: orig.LineID, Qty: dec("1")}})
	if err != nil {
		t.Fatalf("first return: %v", err)
	}
	eq(t, "the first piece's credit", first.TotalInclusive, "0.01")

	// The second piece back, carrying forward what the first credited exactly as
	// the service does when it reads the stored credit note back.
	o := orig
	o.QtyReturned = dec("1")
	o.NetReturned = first.SubtotalNet
	o.TaxReturned = first.TaxTotal
	o.DiscountAllocReturned = first.DiscountTotal
	o.COGSReturned = first.COGSTotal

	second, err := ComputeReturn([]OriginalLine{o},
		[]ReturnRequest{{LineID: o.LineID, Qty: dec("1")}})
	if err != nil {
		t.Fatalf("second return: %v", err)
	}

	// Nothing to credit -- which is right, and must not be an error.
	eq(t, "the second piece's credit", second.TotalInclusive, "0.00")
	eq(t, "its net", second.SubtotalNet, "0.00")
	eq(t, "its tax", second.TaxTotal, "0.00")

	// But there IS stock value coming back, so a guard that returned early here
	// would leave the Inventory account short of the valuation by this much,
	// which is exactly what inventory.GLDifference reports on.
	if !second.COGSTotal.IsPositive() {
		t.Fatalf("the second return has no cost to reverse (%s), so this no "+
			"longer exercises a zero credit with a live cost entry",
			second.COGSTotal)
	}
	eq(t, "its cost", second.COGSTotal, "0.01")
}

// The same zero credit without any rounding involved: a line given away.
//
// pricing refuses only a NEGATIVE unit price, so nothing stops a free line, and
// it does not need an invoice of its own -- a giveaway rides along on a paid
// invoice as a second line, which is how a shop actually rings up "buy one, get
// one" or a no-charge warranty replacement. The invoice total is positive, so
// the sale posts; returning ONLY the free line credits nothing.
//
// It still cost the shop what it cost, so the stock value has to come back.
func TestAFreeLineReturnsWithNoCreditButACostToReverse(t *testing.T) {
	free := origLine("2", "0", "0", "1.00", "0")
	free.UnitPrice = dec("0")

	// Half of it, where the rule takes a proportion.
	partial, err := ComputeReturn([]OriginalLine{free},
		[]ReturnRequest{{LineID: free.LineID, Qty: dec("1")}})
	if err != nil {
		t.Fatalf("returning one of a free pair: %v", err)
	}
	eq(t, "the credit", partial.TotalInclusive, "0.00")
	eq(t, "the cost", partial.COGSTotal, "0.50")

	// And the whole of it, where the rule takes the remainder instead. Both
	// branches of returnShare.of have to reach a zero credit, because the branch
	// taken depends only on whether the line is exhausted.
	whole, err := ComputeReturn([]OriginalLine{free},
		[]ReturnRequest{{LineID: free.LineID, Qty: dec("2")}})
	if err != nil {
		t.Fatalf("returning a free pair in full: %v", err)
	}
	if !whole.Lines[0].IsFullReturn {
		t.Error("returning both is not marked a full return, so this test is " +
			"not exercising the exhausted branch")
	}
	eq(t, "the credit", whole.TotalInclusive, "0.00")
	eq(t, "the cost", whole.COGSTotal, "1.00")
}

// A credit note worth nothing is settled by refunding nothing, which is the
// premise the guard in postReturn rests on: it may skip the reversal entry
// knowing there are no refund legs it would be dropping.
//
// Pinned rather than assumed, because the two halves are separate rules. A
// refund of zero is refused for being zero, so a zero credit note cannot carry
// even one leg -- it has to carry none.
func TestAZeroCreditNoteIsSettledByRefundingNothing(t *testing.T) {
	if err := ValidateRefunds(dec("0.00"), nil); err != nil {
		t.Errorf("a credit note worth nothing demanded a refund: %v", err)
	}
	if err := ValidateRefunds(dec("0.00"), []decimal.Decimal{}); err != nil {
		t.Errorf("a credit note worth nothing demanded a refund: %v", err)
	}

	// And nothing may be refunded against it, so there is no third case where
	// the reversal entry would have had a leg to post.
	if err := ValidateRefunds(dec("0.00"),
		[]decimal.Decimal{dec("0.00")}); err == nil {
		t.Error("a refund of nothing was accepted as a refund")
	}
	if err := ValidateRefunds(dec("0.00"),
		[]decimal.Decimal{dec("0.01")}); err == nil {
		t.Error("a hallala was paid out against a credit note worth nothing")
	}
}
