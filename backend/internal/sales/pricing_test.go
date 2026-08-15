package sales

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
)

var (
	saudi = catalog.TaxRules{
		Country: "sa", Model: catalog.TaxModelVAT, InputTaxRecoverable: true,
		Treatments: []string{"standard", "zero_rated", "exempt", "out_of_scope",
			"export", "reverse_charge", "import"},
	}
	usa = catalog.TaxRules{
		Country: "us", Model: catalog.TaxModelSalesTax, InputTaxRecoverable: false,
		Treatments: []string{"taxable", "non_taxable", "exempt"},
	}
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// Blueprint C9.2 Rule 1, the canonical Saudi retail sale: a shelf price of
// SAR 1,150 that already includes 15% VAT decomposes to 1,000 net and 150 tax.
func TestVATInclusivePricingBackCalculates(t *testing.T) {
	got, err := Compute(SaleInput{
		PricesIncludeTax: true,
		TaxRate:          dec("0.15"),
		Rules:            saudi,
		Lines: []LineInput{{
			Description: "Executive Abaya", Qty: dec("1"),
			UnitPrice: dec("1150.00"), TaxTreatment: "standard",
		}},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if !got.SubtotalNet.Equal(dec("1000")) {
		t.Errorf("net = %s, want 1000", got.SubtotalNet)
	}
	if !got.TaxTotal.Equal(dec("150")) {
		t.Errorf("tax = %s, want 150", got.TaxTotal)
	}
	if !got.TotalInclusive.Equal(dec("1150")) {
		t.Errorf("total = %s, want 1150", got.TotalInclusive)
	}
}

// The same figures the other way round, for a market that quotes prices before
// tax.
func TestTaxExclusivePricingAddsTax(t *testing.T) {
	got, err := Compute(SaleInput{
		PricesIncludeTax: false,
		TaxRate:          dec("0.15"),
		Rules:            saudi,
		Lines: []LineInput{{
			Description: "Executive Abaya", Qty: dec("1"),
			UnitPrice: dec("1000.00"), TaxTreatment: "standard",
		}},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !got.TotalInclusive.Equal(dec("1150")) {
		t.Errorf("total = %s, want 1150", got.TotalInclusive)
	}
}

// The printed lines must add up to the printed total. Rounding once at the end
// instead of per line produces a receipt a customer can prove wrong.
func TestLinesSumExactlyToTheTotal(t *testing.T) {
	// Prices chosen so the per-line VAT lands on a half-hallala.
	got, err := Compute(SaleInput{
		PricesIncludeTax: true,
		TaxRate:          dec("0.15"),
		Rules:            saudi,
		Lines: []LineInput{
			{Description: "A", Qty: dec("1"), UnitPrice: dec("33.33"), TaxTreatment: "standard"},
			{Description: "B", Qty: dec("3"), UnitPrice: dec("11.11"), TaxTreatment: "standard"},
			{Description: "C", Qty: dec("7"), UnitPrice: dec("2.05"), TaxTreatment: "standard"},
		},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	net, tax, gross := decimal.Zero, decimal.Zero, decimal.Zero
	for _, l := range got.Lines {
		net = net.Add(l.NetAmount)
		tax = tax.Add(l.TaxAmount)
		gross = gross.Add(l.GrossAmount)

		if !l.GrossAmount.Equal(l.NetAmount.Add(l.TaxAmount)) {
			t.Errorf("line %d: %s + %s <> %s", l.LineNo, l.NetAmount, l.TaxAmount, l.GrossAmount)
		}
		if l.NetAmount.Exponent() < -2 || l.TaxAmount.Exponent() < -2 {
			t.Errorf("line %d carries more than two decimals: net %s tax %s",
				l.LineNo, l.NetAmount, l.TaxAmount)
		}
	}

	if !net.Equal(got.SubtotalNet) || !tax.Equal(got.TaxTotal) || !gross.Equal(got.TotalInclusive) {
		t.Fatalf("header disagrees with its lines: net %s/%s tax %s/%s total %s/%s",
			got.SubtotalNet, net, got.TaxTotal, tax, got.TotalInclusive, gross)
	}
}

// The allocation must sum back to the discount given. Blueprint C14 names this
// as a common source of silently wrong numbers, and the reason is rounding: a
// proportional split rounded independently per line drifts.
func TestInvoiceDiscountAllocationSumsBackExactly(t *testing.T) {
	// 100 split across three lines in a ratio that does not divide evenly.
	got, err := Compute(SaleInput{
		PricesIncludeTax: true,
		TaxRate:          dec("0.15"),
		Rules:            saudi,
		InvoiceDiscount:  dec("100.00"),
		Lines: []LineInput{
			{Description: "A", Qty: dec("1"), UnitPrice: dec("333.33"), TaxTreatment: "standard"},
			{Description: "B", Qty: dec("1"), UnitPrice: dec("333.33"), TaxTreatment: "standard"},
			{Description: "C", Qty: dec("1"), UnitPrice: dec("333.34"), TaxTreatment: "standard"},
		},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	allocated := decimal.Zero
	for _, l := range got.Lines {
		allocated = allocated.Add(l.InvoiceDiscountAlloc)
	}
	if !allocated.Equal(dec("100.00")) {
		t.Fatalf("allocated %s of a 100.00 discount; the shares must sum back to "+
			"the whole or a partial return will not reconcile", allocated)
	}
}

// A zero-rated or exempt line carries no tax whatever the standard rate is.
func TestNonStandardTreatmentsCarryNoTax(t *testing.T) {
	for _, treatment := range []string{"zero_rated", "exempt", "out_of_scope", "export"} {
		t.Run(treatment, func(t *testing.T) {
			got, err := Compute(SaleInput{
				PricesIncludeTax: true,
				TaxRate:          dec("0.15"),
				Rules:            saudi,
				Lines: []LineInput{{
					Description: "X", Qty: dec("1"),
					UnitPrice: dec("100.00"), TaxTreatment: treatment,
				}},
			})
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if !got.TaxTotal.IsZero() {
				t.Fatalf("%s line was taxed %s", treatment, got.TaxTotal)
			}
			if !got.TotalInclusive.Equal(dec("100")) {
				t.Fatalf("total = %s, want 100", got.TotalInclusive)
			}
		})
	}
}

// A mixed basket: taxable and exempt items on one invoice, which is the normal
// case in a grocery and a common source of wrong totals.
func TestMixedTreatmentsOnOneInvoice(t *testing.T) {
	got, err := Compute(SaleInput{
		PricesIncludeTax: true,
		TaxRate:          dec("0.15"),
		Rules:            saudi,
		Lines: []LineInput{
			{Description: "Taxable", Qty: dec("1"), UnitPrice: dec("115.00"), TaxTreatment: "standard"},
			{Description: "Exempt", Qty: dec("1"), UnitPrice: dec("100.00"), TaxTreatment: "exempt"},
		},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !got.TaxTotal.Equal(dec("15")) {
		t.Errorf("tax = %s, want 15 — only the taxable line should attract tax", got.TaxTotal)
	}
	if !got.TotalInclusive.Equal(dec("215")) {
		t.Errorf("total = %s, want 215", got.TotalInclusive)
	}
}

// The US names its treatments differently, and a Saudi name must not sneak
// through as taxable.
func TestUnitedStatesTreatmentNames(t *testing.T) {
	got, err := Compute(SaleInput{
		PricesIncludeTax: false,
		TaxRate:          dec("0.0825"),
		Rules:            usa,
		Lines: []LineInput{
			{Description: "Shirt", Qty: dec("1"), UnitPrice: dec("100.00"), TaxTreatment: "taxable"},
			{Description: "Groceries", Qty: dec("1"), UnitPrice: dec("50.00"), TaxTreatment: "non_taxable"},
		},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !got.TaxTotal.Equal(dec("8.25")) {
		t.Errorf("tax = %s, want 8.25", got.TaxTotal)
	}

	// "standard" is a VAT word and must not be accepted in a US catalogue.
	_, err = Compute(SaleInput{
		PricesIncludeTax: false, TaxRate: dec("0.0825"), Rules: usa,
		Lines: []LineInput{{
			Description: "X", Qty: dec("1"), UnitPrice: dec("10"), TaxTreatment: "standard",
		}},
	})
	if err == nil {
		t.Fatal("a Saudi treatment name was accepted on a US sale")
	}
}

// COGS is captured at the moment of sale so gross profit is real-time rather
// than a month-end reconstruction (blueprint C13).
func TestCOGSIsCapturedPerLine(t *testing.T) {
	got, err := Compute(SaleInput{
		PricesIncludeTax: true, TaxRate: dec("0.15"), Rules: saudi,
		Lines: []LineInput{
			{Description: "A", Qty: dec("3"), UnitPrice: dec("115.00"),
				TaxTreatment: "standard", CostPerUnit: dec("60.00")},
		},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !got.COGSTotal.Equal(dec("180")) {
		t.Fatalf("COGS = %s, want 180 (3 x 60)", got.COGSTotal)
	}
	// Gross profit is knowable immediately.
	if profit := got.SubtotalNet.Sub(got.COGSTotal); !profit.Equal(dec("120")) {
		t.Fatalf("gross profit = %s, want 120", profit)
	}
}

// Blueprint B7's worked example: SAR 1,000 paid as 200 cash + 300 Mada + 500
// BNPL on one invoice.
func TestSplitTenderMustCoverTheTotalExactly(t *testing.T) {
	total := dec("1000.00")

	if err := ValidateTenders(total, []decimal.Decimal{
		dec("200.00"), dec("300.00"), dec("500.00"),
	}); err != nil {
		t.Fatalf("an exact split payment was refused: %v", err)
	}

	// Short by one hallala.
	err := ValidateTenders(total, []decimal.Decimal{dec("200.00"), dec("799.99")})
	if err == nil {
		t.Fatal("an underpayment of 0.01 was accepted")
	}
	if !contains(err.Error(), "0.01") {
		t.Errorf("the refusal does not name the shortfall: %v", err)
	}

	// Over by one hallala.
	err = ValidateTenders(total, []decimal.Decimal{dec("200.00"), dec("800.01")})
	if err == nil {
		t.Fatal("an overpayment of 0.01 was accepted")
	}
}

func TestSaleNeedsAtLeastOneLine(t *testing.T) {
	if _, err := Compute(SaleInput{TaxRate: dec("0.15"), Rules: saudi}); err == nil {
		t.Fatal("an empty sale was accepted")
	}
}

func TestDiscountLargerThanTheSaleIsRefused(t *testing.T) {
	_, err := Compute(SaleInput{
		PricesIncludeTax: true, TaxRate: dec("0.15"), Rules: saudi,
		InvoiceDiscount: dec("500.00"),
		Lines: []LineInput{{
			Description: "A", Qty: dec("1"), UnitPrice: dec("100.00"), TaxTreatment: "standard",
		}},
	})
	if err == nil {
		t.Fatal("a discount larger than the sale was accepted")
	}
}

func TestLineDiscountLargerThanTheLineIsRefused(t *testing.T) {
	_, err := Compute(SaleInput{
		PricesIncludeTax: true, TaxRate: dec("0.15"), Rules: saudi,
		Lines: []LineInput{{
			Description: "A", Qty: dec("1"), UnitPrice: dec("100.00"),
			LineDiscount: dec("150.00"), TaxTreatment: "standard",
		}},
	})
	if err == nil {
		t.Fatal("a line discount larger than the line was accepted")
	}
}

// Fractional quantities are normal where goods are sold by length or weight.
func TestFractionalQuantity(t *testing.T) {
	got, err := Compute(SaleInput{
		PricesIncludeTax: true, TaxRate: dec("0.15"), Rules: saudi,
		Lines: []LineInput{{
			Description: "Fabric", Qty: dec("2.5"),
			UnitPrice: dec("46.00"), TaxTreatment: "standard",
		}},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	// 2.5 x 46 = 115 inclusive -> 100 net + 15 tax
	if !got.TotalInclusive.Equal(dec("115")) {
		t.Fatalf("total = %s, want 115", got.TotalInclusive)
	}
	if !got.TaxTotal.Equal(dec("15")) {
		t.Fatalf("tax = %s, want 15", got.TaxTotal)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
