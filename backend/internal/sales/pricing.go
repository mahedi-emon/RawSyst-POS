// Package sales computes and finalises a sale.
//
// The tax arithmetic here is the most correctness-sensitive code in the
// product. It decides what a customer is charged, what appears on a signed tax
// invoice, and what a VAT return reports — and once ZATCA holds an invoice it
// cannot be corrected in place, only credited and reissued.
//
// Four rules govern it, and each one is a mistake that has been made before:
//
//  1. The rate is resolved at the invoice's ISSUE DATE, never at "now".
//     Reprinting a March invoice in June must show March's rate.
//  2. VAT-inclusive pricing is the Saudi retail default: the shelf price
//     already includes tax and the net is back-calculated from it.
//  3. Rounding is per line, half-up, to two decimals — then summed. Summing
//     first and rounding once gives a total that disagrees with the printed
//     lines, and a customer checking the arithmetic is right to complain.
//  4. Everything is decimal. 0.15 has no exact binary representation, and a
//     tax figure wrong in the last hallala is wrong on a tax return.
package sales

import (
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// moneyScale is the number of decimals a monetary amount may carry.
//
// ZATCA's XML Implementation Standard caps monetary and VAT amounts at two
// decimals and specifies half-up rounding applied on the third. Unit price is
// deliberately NOT capped — a price of 3.333 per metre is valid input; the line
// total it produces is what gets rounded.
const moneyScale int32 = 2

// LineInput is one item as the cashier rang it up.
type LineInput struct {
	VariantID     string
	Description   string
	DescriptionAr string

	Qty       decimal.Decimal
	UnitPrice decimal.Decimal

	// Discount on this line specifically, as an absolute amount.
	LineDiscount decimal.Decimal

	TaxTreatment string
}

// SaleInput is a complete sale awaiting computation.
type SaleInput struct {
	Lines []LineInput

	// A discount applied to the whole sale, spread across the lines.
	InvoiceDiscount decimal.Decimal

	// True where displayed prices already contain tax — the Saudi retail norm.
	PricesIncludeTax bool

	TaxRate decimal.Decimal
	Rules   catalog.TaxRules
}

// ComputedLine is one line after tax.
type ComputedLine struct {
	LineInput
	LineNo int

	InvoiceDiscountAlloc decimal.Decimal
	TaxRate              decimal.Decimal
	TaxAmount            decimal.Decimal
	NetAmount            decimal.Decimal
	GrossAmount          decimal.Decimal

	// COGSAmount is zero until ApplyCosts fills it from the costing engine.
	// Pricing cannot know it: what a sale costs is what actually left the
	// stock, valued by the company's costing method, not a figure the till
	// asserted.
	COGSAmount decimal.Decimal
}

// ComputedSale is the finished arithmetic.
type ComputedSale struct {
	Lines []ComputedLine

	SubtotalNet    decimal.Decimal
	DiscountTotal  decimal.Decimal
	TaxTotal       decimal.Decimal
	TotalInclusive decimal.Decimal

	// COGSTotal is zero until ApplyCosts fills it. See ComputedLine.COGSAmount.
	COGSTotal decimal.Decimal
}

// ApplyCosts records what each line actually cost, one figure per line in the
// order Compute produced them.
//
// Cost is deliberately a second step rather than an input to pricing. A till
// cannot know what a sale costs: the answer depends on which stock the costing
// engine drew down and on the company's method, and under weighted average it
// depends on every receipt since the last sale. Letting the caller supply a
// cost makes gross profit an assertion rather than a measurement, and C13
// requires it to be a measurement — posted with the sale, not reconstructed at
// month end.
//
// The count must match exactly. A mismatch means the caller costed a different
// set of lines from the ones it priced, and a sale that books the cost of the
// wrong item is worse than one that fails.
func (s *ComputedSale) ApplyCosts(costs []decimal.Decimal) error {
	if len(costs) != len(s.Lines) {
		return errs.Newf(errs.CodeInternal,
			"This sale priced %d lines but %d were costed.",
			len(s.Lines), len(costs))
	}

	total := decimal.Zero
	for i, c := range costs {
		if c.IsNegative() {
			return errs.Newf(errs.CodeInternal,
				"Line %d was costed at %s. A negative cost of sale would "+
					"overstate profit.", i+1, c)
		}
		s.Lines[i].COGSAmount = c
		total = total.Add(c)
	}
	s.COGSTotal = total
	return nil
}

// Compute turns a rung-up sale into the figures that will be signed.
func Compute(in SaleInput) (ComputedSale, error) {
	if len(in.Lines) == 0 {
		return ComputedSale{}, errs.New(errs.CodeInvalidInput,
			"A sale needs at least one item.")
	}
	if in.TaxRate.IsNegative() || in.TaxRate.GreaterThanOrEqual(decimal.NewFromInt(1)) {
		return ComputedSale{}, errs.New(errs.CodeInternal,
			"The tax rate resolved for this sale is not usable.")
	}

	// Gross before any invoice-level discount, used to weight the allocation.
	grossBeforeInvoiceDiscount := decimal.Zero
	for _, l := range in.Lines {
		if l.Qty.IsZero() {
			return ComputedSale{}, errs.New(errs.CodeInvalidInput,
				"An item has a quantity of zero.")
		}
		if l.UnitPrice.IsNegative() {
			return ComputedSale{}, errs.New(errs.CodeInvalidInput,
				"An item has a negative price.")
		}
		lineGross := l.Qty.Mul(l.UnitPrice).Sub(l.LineDiscount)
		if lineGross.IsNegative() {
			return ComputedSale{}, errs.New(errs.CodeInvalidInput,
				"A discount is larger than the item it applies to.")
		}
		grossBeforeInvoiceDiscount = grossBeforeInvoiceDiscount.Add(lineGross)
	}

	if in.InvoiceDiscount.GreaterThan(grossBeforeInvoiceDiscount) {
		return ComputedSale{}, errs.New(errs.CodeInvalidInput,
			"The discount is larger than the sale.")
	}

	out := ComputedSale{Lines: make([]ComputedLine, 0, len(in.Lines))}
	allocated := decimal.Zero
	cumulativeGross := decimal.Zero

	for i, l := range in.Lines {
		if err := catalog.ValidateTreatment(in.Rules, l.TaxTreatment); err != nil {
			return ComputedSale{}, err
		}

		lineGross := l.Qty.Mul(l.UnitPrice).Sub(l.LineDiscount)
		cumulativeGross = cumulativeGross.Add(lineGross)

		// Allocate the invoice discount against the RUNNING TOTAL rather than
		// line by line, and take each line's share as the difference between
		// successive cumulative targets.
		//
		// The obvious approach — round each line's own share and hand the
		// remainder to the last line — accumulates. Twenty lines each rounding
		// down by 0.004 leave the final line holding 0.08 more discount than it
		// is worth, and it goes NEGATIVE: net -0.07, tax -0.01, and an invoice
		// total below zero. That is not a rounding blemish. It is a tax invoice
		// with negative VAT on it, which ZATCA rejects and a VAT return would
		// carry.
		//
		// Rounding the cumulative figure instead bounds the error at one
		// hallala for the whole invoice rather than one per line, and because
		// the final cumulative target is the discount itself, the shares still
		// sum back to it exactly — which is what a partial return depends on.
		share := decimal.Zero
		if in.InvoiceDiscount.IsPositive() && grossBeforeInvoiceDiscount.IsPositive() {
			target := in.InvoiceDiscount.
				Mul(cumulativeGross).
				Div(grossBeforeInvoiceDiscount).
				Round(moneyScale)
			share = target.Sub(allocated)

			// A line can only give up what it is worth. When rounding pushes a
			// share past that — reachable at a 100% discount, where a line's
			// exact share IS its gross — the excess is left unallocated here
			// and picked up by the next line automatically, because the next
			// share is measured against the cumulative target rather than
			// against this one.
			if share.GreaterThan(lineGross) {
				// Rounded DOWN to the four decimals the column stores, so
				// storage cannot round it back up and reintroduce the negative.
				share = lineGross.RoundDown(4)
			}
			if share.IsNegative() {
				share = decimal.Zero
			}
			allocated = allocated.Add(share)
		}

		afterDiscount := lineGross.Sub(share)

		// A zero-rated, exempt or out-of-scope line carries no tax whatever the
		// country's standard rate is.
		rate := in.TaxRate
		if !taxable(in.Rules, l.TaxTreatment) {
			rate = decimal.Zero
		}

		var net, tax decimal.Decimal
		if in.PricesIncludeTax {
			// Shelf price contains the tax: net = gross / (1 + rate).
			net = afterDiscount.Div(decimal.NewFromInt(1).Add(rate)).Round(moneyScale)
			tax = afterDiscount.Round(moneyScale).Sub(net)
		} else {
			net = afterDiscount.Round(moneyScale)
			tax = net.Mul(rate).Round(moneyScale)
		}

		gross := net.Add(tax)

		out.Lines = append(out.Lines, ComputedLine{
			LineInput:            l,
			LineNo:               i + 1,
			InvoiceDiscountAlloc: share,
			TaxRate:              rate,
			TaxAmount:            tax,
			NetAmount:            net,
			GrossAmount:          gross,
		})

		out.SubtotalNet = out.SubtotalNet.Add(net)
		out.TaxTotal = out.TaxTotal.Add(tax)
		out.TotalInclusive = out.TotalInclusive.Add(gross)
		out.DiscountTotal = out.DiscountTotal.Add(l.LineDiscount).Add(share)
	}

	return out, nil
}

// taxable reports whether a treatment attracts tax at the standard rate.
//
// The names differ per market, which is why this consults the country's rules
// rather than matching a fixed list: "standard" is taxable in Saudi Arabia and
// meaningless in the USA, where the equivalent is "taxable".
func taxable(rules catalog.TaxRules, treatment string) bool {
	switch rules.Model {
	case catalog.TaxModelSalesTax:
		return treatment == "taxable"
	default:
		// "reduced" is taxable, and this engine would charge it the STANDARD
		// rate, because SaleInput carries one rate rather than a rate per
		// treatment.
		//
		// That is wrong, and it is currently unreachable rather than fixed.
		// Only Bangladesh lists the treatment, its rule is seeded unverified,
		// and registry.New(pool, requireVerified) refuses an unverified rule in
		// production -- so no Bangladeshi sale can be priced there at all.
		//
		// Whoever ships Bangladesh must give the rate resolution a treatment,
		// not just a country and a date. Charging a reduced-rate line at the
		// standard rate overcharges the customer and overstates the return, and
		// it would do so silently.
		return treatment == "standard" || treatment == "reduced"
	}
}

// ValidateTenders checks that payment covers the sale exactly.
//
// Exactly, not approximately. The database enforces this too, but failing here
// gives the cashier a message naming the shortfall instead of a constraint
// violation — and once ZATCA holds the invoice, an imbalance cannot be fixed in
// place.
func ValidateTenders(total decimal.Decimal, tenders []decimal.Decimal) error {
	sum := decimal.Zero
	for _, t := range tenders {
		if !t.IsPositive() {
			return errs.New(errs.CodeInvalidInput,
				"A payment amount must be greater than zero.")
		}
		sum = sum.Add(t)
	}

	switch {
	case sum.Equal(total):
		return nil
	case sum.LessThan(total):
		return errs.Newf(errs.CodeInvalidInput,
			"Payments come to %s but the total is %s. %s is still owed.",
			sum.StringFixed(moneyScale), total.StringFixed(moneyScale),
			total.Sub(sum).StringFixed(moneyScale))
	default:
		return errs.Newf(errs.CodeInvalidInput,
			"Payments come to %s but the total is %s, an overpayment of %s. "+
				"Give change from the cash tender rather than overpaying.",
			sum.StringFixed(moneyScale), total.StringFixed(moneyScale),
			sum.Sub(total).StringFixed(moneyScale))
	}
}
