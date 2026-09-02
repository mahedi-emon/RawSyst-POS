package sales

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// Arithmetic checked against figures worked out by hand, not against the
// implementation.
//
// The existing pricing tests were written alongside the code and share its
// reasoning, which means a wrong formula and a wrong expectation agree with
// each other. Every expected value below was derived independently from the
// four rules in the package comment, and the working that produced it is
// written into the test, so a reader can check the arithmetic without running
// anything.
//
// This file found a real defect. See
// TestAHundredPercentDiscountNeverDrivesALineNegative.

// line is a terse constructor, so a table of cases reads as a table.
func line(qty, price, treatment string) LineInput {
	return LineInput{
		VariantID: "v", Description: "item",
		Qty: dec(qty), UnitPrice: dec(price), TaxTreatment: treatment,
	}
}

func mustCompute(t *testing.T, in SaleInput) ComputedSale {
	t.Helper()
	got, err := Compute(in)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	return got
}

func eq(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// Every line must satisfy net + tax = gross, no line may be negative, and the
// totals must be the sum of the lines. Checked on every case below through
// this helper rather than restated each time.
func checkInternallyConsistent(t *testing.T, got ComputedSale) {
	t.Helper()
	net, tax, gross := decimal.Zero, decimal.Zero, decimal.Zero
	for _, l := range got.Lines {
		if !l.NetAmount.Add(l.TaxAmount).Equal(l.GrossAmount) {
			t.Errorf("line %d: %s + %s is not %s",
				l.LineNo, l.NetAmount, l.TaxAmount, l.GrossAmount)
		}
		if l.NetAmount.IsNegative() || l.TaxAmount.IsNegative() || l.GrossAmount.IsNegative() {
			t.Errorf("line %d is negative: net=%s tax=%s gross=%s",
				l.LineNo, l.NetAmount, l.TaxAmount, l.GrossAmount)
		}
		if l.InvoiceDiscountAlloc.IsNegative() {
			t.Errorf("line %d has a negative discount share: %s",
				l.LineNo, l.InvoiceDiscountAlloc)
		}
		net = net.Add(l.NetAmount)
		tax = tax.Add(l.TaxAmount)
		gross = gross.Add(l.GrossAmount)
	}
	if !net.Equal(got.SubtotalNet) {
		t.Errorf("lines net to %s but the sale says %s", net, got.SubtotalNet)
	}
	if !tax.Equal(got.TaxTotal) {
		t.Errorf("lines tax to %s but the sale says %s", tax, got.TaxTotal)
	}
	if !gross.Equal(got.TotalInclusive) {
		t.Errorf("lines gross to %s but the sale says %s", gross, got.TotalInclusive)
	}
}

// THE DEFECT THIS FILE FOUND.
//
// Twenty lines at 1.004 and one at 0.05, discounted in full. Each of the first
// twenty rounds down by 0.004, so the old line-by-line allocation left the
// final line holding 0.13 of discount against 0.05 of value.
//
// Worked by hand: gross is 20 x 1.004 + 0.05 = 20.13. A discount of the whole
// amount means every line gives up exactly what it is worth and the invoice
// totals zero. The old code produced net -0.07, tax -0.01 and a total of
// -0.08: a tax invoice carrying negative VAT, which ZATCA rejects under BR-12
// and BR-13 and which a VAT return would then report.
func TestAHundredPercentDiscountNeverDrivesALineNegative(t *testing.T) {
	var lines []LineInput
	for i := 0; i < 20; i++ {
		lines = append(lines, line("1", "1.004", "standard"))
	}
	lines = append(lines, line("1", "0.05", "standard"))

	got := mustCompute(t, SaleInput{
		Lines:            lines,
		InvoiceDiscount:  dec("20.13"),
		PricesIncludeTax: true,
		TaxRates:         standardOnly(dec("0.15")),
		Rules:            saudi,
	})

	checkInternallyConsistent(t, got)
	eq(t, "total", got.TotalInclusive, "0")
	eq(t, "tax", got.TaxTotal, "0")

	if got.DiscountTotal.GreaterThan(dec("20.13")) {
		t.Errorf("the discount applied, %s, is more than the sale was worth",
			got.DiscountTotal)
	}
}

// The general form of the same defect. Whatever the shape of the invoice, no
// line may end up negative and no total may fall below zero.
func TestNoAllocationShapeProducesANegativeLine(t *testing.T) {
	shapes := map[string][]LineInput{
		"tiny last line": {
			line("1", "99.99", "standard"), line("1", "0.01", "standard"),
		},
		"tiny first line": {
			line("1", "0.01", "standard"), line("1", "99.99", "standard"),
		},
		"equal thirds": {
			line("1", "33.33", "standard"), line("1", "33.33", "standard"),
			line("1", "33.34", "standard"),
		},
		"three decimal prices": {
			line("3", "3.333", "standard"), line("7", "1.001", "standard"),
			line("1", "0.02", "standard"),
		},
		"fractional quantities": {
			line("0.125", "80", "standard"), line("2.5", "4.44", "standard"),
		},
		"mixed treatments": {
			line("1", "33.33", "standard"), line("1", "0.03", "zero_rated"),
			line("4", "2.505", "standard"),
		},
	}

	for name, lines := range shapes {
		t.Run(name, func(t *testing.T) {
			gross := decimal.Zero
			for _, l := range lines {
				gross = gross.Add(l.Qty.Mul(l.UnitPrice))
			}

			// Discounting the whole thing is the worst case for allocation;
			// the fractions either side of it catch the shapes in between.
			for _, portion := range []string{"1", "0.999", "0.5", "0.333"} {
				discount := gross.Mul(dec(portion)).Round(2)
				if discount.GreaterThan(gross) {
					discount = gross
				}
				got := mustCompute(t, SaleInput{
					Lines:            lines,
					InvoiceDiscount:  discount,
					PricesIncludeTax: true,
					TaxRates:         standardOnly(dec("0.15")),
					Rules:            saudi,
				})
				checkInternallyConsistent(t, got)
				if got.TotalInclusive.IsNegative() {
					t.Errorf("discounting %s of %s left a total of %s",
						portion, gross, got.TotalInclusive)
				}
			}
		})
	}
}

// Tax-exclusive, worked by hand.
//
// 3 x 33.33 is 99.99 net. 99.99 x 0.15 is 14.9985, which rounds half-up on the
// third decimal to 15.00. The gross is 114.99.
func TestExclusivePricingRoundsTaxHalfUp(t *testing.T) {
	got := mustCompute(t, SaleInput{
		Lines:    []LineInput{line("3", "33.33", "standard")},
		TaxRates: standardOnly(dec("0.15")),
		Rules:    saudi,
	})
	checkInternallyConsistent(t, got)
	eq(t, "net", got.SubtotalNet, "99.99")
	eq(t, "tax", got.TaxTotal, "15.00")
	eq(t, "total", got.TotalInclusive, "114.99")
}

// Tax-inclusive, worked by hand.
//
// A shelf price of 100 with 15% already in it: 100 / 1.15 is 86.9565..., which
// rounds to 86.96. The tax is the remainder, 100 - 86.96 = 13.04 -- taken as a
// remainder rather than recomputed, so the line always sums back to the price
// on the shelf edge.
func TestInclusivePricingTakesTaxAsTheRemainder(t *testing.T) {
	got := mustCompute(t, SaleInput{
		Lines:            []LineInput{line("2", "50", "standard")},
		PricesIncludeTax: true,
		TaxRates:         standardOnly(dec("0.15")),
		Rules:            saudi,
	})
	checkInternallyConsistent(t, got)
	eq(t, "net", got.SubtotalNet, "86.96")
	eq(t, "tax", got.TaxTotal, "13.04")
	eq(t, "total", got.TotalInclusive, "100")
}

// A line discount comes off before tax is worked out.
//
// 2 x 50 is 100, less 10 leaves 90 gross. 90 / 1.15 is 78.2608..., rounding to
// 78.26, and the tax is 90 - 78.26 = 11.74.
func TestALineDiscountIsAppliedBeforeTax(t *testing.T) {
	l := line("2", "50", "standard")
	l.LineDiscount = dec("10")

	got := mustCompute(t, SaleInput{
		Lines:            []LineInput{l},
		PricesIncludeTax: true,
		TaxRates:         standardOnly(dec("0.15")),
		Rules:            saudi,
	})
	checkInternallyConsistent(t, got)
	eq(t, "net", got.SubtotalNet, "78.26")
	eq(t, "tax", got.TaxTotal, "11.74")
	eq(t, "total", got.TotalInclusive, "90")
	eq(t, "discount", got.DiscountTotal, "10")
}

// An invoice discount is split in proportion to what each line is worth.
//
// 60 and 40 make 100, so a discount of 10 is 6 and 4. Each line is then taxed
// on what remains:
//
//	54 / 1.15 = 46.9565 -> 46.96 net, 7.04 tax
//	36 / 1.15 = 31.3043 -> 31.30 net, 4.70 tax
func TestAnInvoiceDiscountIsSplitInProportion(t *testing.T) {
	got := mustCompute(t, SaleInput{
		Lines: []LineInput{
			line("1", "60", "standard"),
			line("1", "40", "standard"),
		},
		InvoiceDiscount:  dec("10"),
		PricesIncludeTax: true,
		TaxRates:         standardOnly(dec("0.15")),
		Rules:            saudi,
	})
	checkInternallyConsistent(t, got)

	eq(t, "line 1 share", got.Lines[0].InvoiceDiscountAlloc, "6")
	eq(t, "line 2 share", got.Lines[1].InvoiceDiscountAlloc, "4")
	eq(t, "line 1 net", got.Lines[0].NetAmount, "46.96")
	eq(t, "line 1 tax", got.Lines[0].TaxAmount, "7.04")
	eq(t, "line 2 net", got.Lines[1].NetAmount, "31.30")
	eq(t, "line 2 tax", got.Lines[1].TaxAmount, "4.70")
	eq(t, "total", got.TotalInclusive, "90")
	eq(t, "discount", got.DiscountTotal, "10")
}

// Thirds: the shares must still sum to the discount exactly.
//
// 33.33, 33.33 and 33.34 make 100, so a discount of 10 is 3.333, 3.333 and
// 3.334. Rounding each share in isolation gives 3.33 three times, which is
// 9.99 and loses a hallala. Rounding the running total instead gives 3.33,
// 3.34 and 3.33, which is exactly 10.
func TestDiscountSharesSumBackToTheDiscountExactly(t *testing.T) {
	got := mustCompute(t, SaleInput{
		Lines: []LineInput{
			line("1", "33.33", "standard"),
			line("1", "33.33", "standard"),
			line("1", "33.34", "standard"),
		},
		InvoiceDiscount:  dec("10"),
		PricesIncludeTax: true,
		TaxRates:         standardOnly(dec("0.15")),
		Rules:            saudi,
	})
	checkInternallyConsistent(t, got)

	sum := decimal.Zero
	for _, l := range got.Lines {
		sum = sum.Add(l.InvoiceDiscountAlloc)
	}
	eq(t, "allocated", sum, "10")
	eq(t, "total", got.TotalInclusive, "90")
}

// A zero-rated line carries no tax whatever the standard rate is, and sits
// beside a taxed one without disturbing it.
//
// 115 standard inclusive is 100 net and 15 tax. 50 zero-rated is 50 net and
// nothing else.
func TestZeroRatedAndStandardOnOneInvoice(t *testing.T) {
	got := mustCompute(t, SaleInput{
		Lines: []LineInput{
			line("1", "115", "standard"),
			line("1", "50", "zero_rated"),
		},
		PricesIncludeTax: true,
		TaxRates:         standardOnly(dec("0.15")),
		Rules:            saudi,
	})
	checkInternallyConsistent(t, got)
	eq(t, "line 1 tax", got.Lines[0].TaxAmount, "15")
	eq(t, "line 2 tax", got.Lines[1].TaxAmount, "0")
	eq(t, "line 2 rate", got.Lines[1].TaxRate, "0")
	eq(t, "net", got.SubtotalNet, "150")
	eq(t, "tax", got.TaxTotal, "15")
	eq(t, "total", got.TotalInclusive, "165")
}

// The smallest amount the currency has.
//
// 0.01 inclusive of 15%: 0.01 / 1.15 is 0.008695..., which rounds to 0.01, and
// the tax is then 0.01 - 0.01 = 0. A hallala cannot be split, so all of it is
// net and the tax on it is nothing. That is arithmetically right, and the
// thing to guard is that it never becomes negative.
func TestTheSmallestAmountBehaves(t *testing.T) {
	got := mustCompute(t, SaleInput{
		Lines:            []LineInput{line("1", "0.01", "standard")},
		PricesIncludeTax: true,
		TaxRates:         standardOnly(dec("0.15")),
		Rules:            saudi,
	})
	checkInternallyConsistent(t, got)
	eq(t, "net", got.SubtotalNet, "0.01")
	eq(t, "tax", got.TaxTotal, "0")
	eq(t, "total", got.TotalInclusive, "0.01")
}

// A large sale must not lose precision.
//
// 9,999,999.99 inclusive of 15%: 9999999.99 / 1.15 is 8695652.1652..., which
// rounds to 8695652.17, and the tax is the remainder, 1304347.82.
func TestALargeAmountKeepsItsPrecision(t *testing.T) {
	got := mustCompute(t, SaleInput{
		Lines:            []LineInput{line("1", "9999999.99", "standard")},
		PricesIncludeTax: true,
		TaxRates:         standardOnly(dec("0.15")),
		Rules:            saudi,
	})
	checkInternallyConsistent(t, got)
	eq(t, "net", got.SubtotalNet, "8695652.17")
	eq(t, "tax", got.TaxTotal, "1304347.82")
	eq(t, "total", got.TotalInclusive, "9999999.99")
}

// A zero standard rate is not the same thing as a zero-rated line, and both
// must come out at no tax.
func TestAZeroStandardRateChargesNothing(t *testing.T) {
	got := mustCompute(t, SaleInput{
		Lines:            []LineInput{line("2", "25", "standard")},
		PricesIncludeTax: true,
		TaxRates:         standardOnly(decimal.Zero),
		Rules:            saudi,
	})
	checkInternallyConsistent(t, got)
	eq(t, "net", got.SubtotalNet, "50")
	eq(t, "tax", got.TaxTotal, "0")
}

// Line and invoice discounts together, worked by hand.
//
// Line 1 is 2 x 60 = 120, less a line discount of 20, leaving 100.
// Line 2 is 1 x 100 = 100 with no line discount.
// The gross before the invoice discount is 200, so a discount of 20 splits
// evenly, 10 to each. Both lines are then taxed on 90: net 78.26, tax 11.74.
// The discount recorded is the line discount plus both shares: 20 + 20 = 40.
func TestLineAndInvoiceDiscountsCombine(t *testing.T) {
	l1 := line("2", "60", "standard")
	l1.LineDiscount = dec("20")

	got := mustCompute(t, SaleInput{
		Lines:            []LineInput{l1, line("1", "100", "standard")},
		InvoiceDiscount:  dec("20"),
		PricesIncludeTax: true,
		TaxRates:         standardOnly(dec("0.15")),
		Rules:            saudi,
	})
	checkInternallyConsistent(t, got)
	eq(t, "line 1 share", got.Lines[0].InvoiceDiscountAlloc, "10")
	eq(t, "line 2 share", got.Lines[1].InvoiceDiscountAlloc, "10")
	eq(t, "net", got.SubtotalNet, "156.52")
	eq(t, "tax", got.TaxTotal, "23.48")
	eq(t, "total", got.TotalInclusive, "180")
	eq(t, "discount", got.DiscountTotal, "40")
}

// Payment must cover the sale exactly. Overpayment is refused rather than
// silently becoming change: the cashier gives change from the cash tender, and
// an overpaid invoice would not balance.
func TestPaymentMustCoverTheSaleExactly(t *testing.T) {
	total := dec("115.00")

	if err := ValidateTenders(total, []decimal.Decimal{dec("100"), dec("15")}); err != nil {
		t.Errorf("an exact split tender was refused: %v", err)
	}
	if err := ValidateTenders(total, []decimal.Decimal{dec("114.99")}); err == nil {
		t.Error("a shortfall of one hallala was accepted")
	}
	if err := ValidateTenders(total, []decimal.Decimal{dec("115.01")}); err == nil {
		t.Error("an overpayment of one hallala was accepted")
	}
	if err := ValidateTenders(total, []decimal.Decimal{dec("0")}); err == nil {
		t.Error("a zero tender was accepted")
	}
	if err := ValidateTenders(total, []decimal.Decimal{dec("-115")}); err == nil {
		t.Error("a negative tender was accepted")
	}
	if err := ValidateTenders(total, nil); err == nil {
		t.Error("a sale with no payment at all was accepted")
	}
}

// The shortfall is named, because a cashier needs the number rather than a
// refusal.
func TestAShortfallSaysHowMuchIsMissing(t *testing.T) {
	err := ValidateTenders(dec("115.00"), []decimal.Decimal{dec("100.00")})
	if err == nil {
		t.Fatal("a short payment was accepted")
	}
	if !strings.Contains(err.Error(), "15.00") {
		t.Errorf("the message does not name the shortfall: %s", err)
	}
}
