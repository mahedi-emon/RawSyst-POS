package sales

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// OriginalLine is a sale line as it was actually charged.
//
// These are the STORED values, read back from the invoice — not recomputed.
// Blueprint C14 warns that proportional discount allocation is "a common place
// where cheaper POS software silently produces wrong numbers", and recomputing
// is exactly how that happens: the rate may have changed, the rounding will not
// reproduce identically, and the customer is refunded something other than what
// they paid.
type OriginalLine struct {
	LineID      uuid.UUID
	LineNo      int
	VariantID   uuid.UUID
	Description string

	QtySold     decimal.Decimal
	QtyReturned decimal.Decimal // from previous partial returns
	UnitPrice   decimal.Decimal

	// What earlier partial returns have ALREADY credited. Needed because the
	// return that exhausts a line must credit the remainder rather than a
	// proportion — see ComputeReturn.
	NetReturned           decimal.Decimal
	TaxReturned           decimal.Decimal
	DiscountAllocReturned decimal.Decimal
	COGSReturned          decimal.Decimal

	// As charged. The rate travels with them so a return computed years later
	// uses the rate that was actually applied.
	TaxTreatment         string
	TaxRate              decimal.Decimal
	NetAmount            decimal.Decimal
	TaxAmount            decimal.Decimal
	LineDiscount         decimal.Decimal
	InvoiceDiscountAlloc decimal.Decimal
	COGSAmount           decimal.Decimal
}

// QtyReturnable is what remains.
func (o OriginalLine) QtyReturnable() decimal.Decimal {
	return o.QtySold.Sub(o.QtyReturned)
}

// ReturnRequest asks to return a quantity from one original line.
type ReturnRequest struct {
	LineID uuid.UUID
	Qty    decimal.Decimal
}

// ReturnLine is one computed credit-note line.
type ReturnLine struct {
	OriginalLineID uuid.UUID
	LineNo         int
	VariantID      uuid.UUID
	Description    string

	Qty       decimal.Decimal
	UnitPrice decimal.Decimal

	TaxTreatment string
	TaxRate      decimal.Decimal

	// Every amount below is a REVERSAL: positive figures that will be credited.
	NetAmount            decimal.Decimal
	TaxAmount            decimal.Decimal
	GrossAmount          decimal.Decimal
	InvoiceDiscountAlloc decimal.Decimal
	COGSAmount           decimal.Decimal

	// True when the whole remaining quantity is going back, which is the case
	// that must reconcile to the penny.
	IsFullReturn bool
}

// ComputedReturn is a credit note before it is written.
type ComputedReturn struct {
	Lines []ReturnLine

	SubtotalNet    decimal.Decimal
	TaxTotal       decimal.Decimal
	TotalInclusive decimal.Decimal
	DiscountTotal  decimal.Decimal
	COGSTotal      decimal.Decimal
}

// ComputeReturn works out what to credit.
//
// # Credits are cumulative, not proportional
//
// A line brought back in pieces must credit exactly what was charged for it by
// the time the last piece arrives — no more and no less. Taking each return as
// its own proportion of the line does not do that: a line of 3 at a net of
// 100.00 returned a unit at a time credits 33.33 three times, and the business
// quietly keeps a hallala on every partial-return sequence that does not divide
// evenly.
//
// So a return credits the difference between two CUMULATIVE figures: what the
// line's credits should stand at once this return is counted, less what they
// already stand at. The last return's target is the whole amount, so the line
// necessarily nets back to what was charged.
//
// # Why the obvious remainder rule is not enough
//
// Crediting a proportion each time and letting the LAST return absorb whatever
// is left also sums correctly, and that is what this used to do. It lets the
// last credit go NEGATIVE. Each partial rounds up by as much as half a hallala,
// and enough of them take more than the line was worth between them.
//
// A hundred sachets at a net of 30.60, returned one at a time, does it: each of
// the first ninety-nine credits round(30.60 / 100, 2) = 0.31, which is 30.69
// between them, and the hundredth is handed 30.60 - 30.69 = -0.09. The posting
// engine refuses a negative amount rather than writing a negative debit, so the
// last return of a line the shop really did take back could not be recorded at
// all — not on the retry either.
//
// This is the same defect and the same fix as allocate in accounting,
// allocateFee in settlement, allocateLandedCost in purchasing and the
// invoice-discount allocation in pricing.go. The difference is that here the
// parts arrive over weeks rather than all at once.
func ComputeReturn(originals []OriginalLine, requests []ReturnRequest) (ComputedReturn, error) {
	if len(requests) == 0 {
		return ComputedReturn{}, errs.New(errs.CodeInvalidInput,
			"Choose at least one item to return.")
	}

	byID := make(map[uuid.UUID]OriginalLine, len(originals))
	for _, o := range originals {
		byID[o.LineID] = o
	}

	out := ComputedReturn{Lines: make([]ReturnLine, 0, len(requests))}
	seen := make(map[uuid.UUID]bool, len(requests))

	for i, req := range requests {
		orig, found := byID[req.LineID]
		if !found {
			return ComputedReturn{}, errs.New(errs.CodeNotFound,
				"That item is not on the original invoice.")
		}
		// Two requests against one line would each pass the remaining-quantity
		// check while together exceeding it.
		if seen[req.LineID] {
			return ComputedReturn{}, errs.Newf(errs.CodeInvalidInput,
				"%q appears twice in this return. Combine them into one line.",
				orig.Description)
		}
		seen[req.LineID] = true

		if !req.Qty.IsPositive() {
			return ComputedReturn{}, errs.New(errs.CodeInvalidInput,
				"A return quantity must be greater than zero.")
		}

		remaining := orig.QtyReturnable()
		if req.Qty.GreaterThan(remaining) {
			if remaining.IsZero() {
				return ComputedReturn{}, errs.Newf(errs.CodeInvalidInput,
					"%q has already been returned in full.", orig.Description)
			}
			return ComputedReturn{}, errs.Newf(errs.CodeInvalidInput,
				"Only %s of %q can still be returned; %s were sold and %s already came back.",
				remaining.String(), orig.Description,
				orig.QtySold.String(), orig.QtyReturned.String())
		}

		line := ReturnLine{
			OriginalLineID: orig.LineID,
			LineNo:         i + 1,
			VariantID:      orig.VariantID,
			Description:    orig.Description,
			Qty:            req.Qty,
			UnitPrice:      orig.UnitPrice,
			TaxTreatment:   orig.TaxTreatment,
			TaxRate:        orig.TaxRate,
			// True when this return exhausts the line, whether it is the only
			// return or the last of several.
			IsFullReturn: req.Qty.Equal(remaining),
		}

		// All four amounts on the same cumulative rule, so a line credited in
		// pieces reverses to exactly what was charged and no piece is negative.
		credit := returnShare{
			exhausted: line.IsFullReturn,
			returned:  orig.QtyReturned.Add(req.Qty),
			sold:      orig.QtySold,
		}
		line.NetAmount = credit.of(orig.NetAmount, orig.NetReturned)
		line.TaxAmount = credit.of(orig.TaxAmount, orig.TaxReturned)
		line.InvoiceDiscountAlloc = credit.of(
			orig.InvoiceDiscountAlloc, orig.DiscountAllocReturned)
		line.COGSAmount = credit.of(orig.COGSAmount, orig.COGSReturned)

		line.GrossAmount = line.NetAmount.Add(line.TaxAmount)

		out.Lines = append(out.Lines, line)
		out.SubtotalNet = out.SubtotalNet.Add(line.NetAmount)
		out.TaxTotal = out.TaxTotal.Add(line.TaxAmount)
		out.TotalInclusive = out.TotalInclusive.Add(line.GrossAmount)
		out.DiscountTotal = out.DiscountTotal.Add(line.InvoiceDiscountAlloc)
		out.COGSTotal = out.COGSTotal.Add(line.COGSAmount)
	}

	return out, nil
}

// returnShare is the cumulative rule one return applies to each of a line's
// amounts: the net, the tax, the invoice discount allocated to it and its cost
// of sales.
//
// One value applied four times, because the four have to move together. A tax
// credit that does not match the net it was charged on leaves the VAT return
// disagreeing with the sales figure it is computed from, and a cost credit that
// does not match the quantity restored parts the valuation from the ledger.
type returnShare struct {
	// exhausted is true when this return takes the last of the line. It is what
	// makes the credits sum to exactly what was charged, because the final
	// target is the whole amount rather than a proportion of it.
	exhausted bool

	// returned is how much of the line has come back INCLUDING this return, and
	// sold is what the line was sold in. Their ratio is the cumulative target.
	returned, sold decimal.Decimal
}

// of is what this return credits of one amount.
func (s returnShare) of(total, credited decimal.Decimal) decimal.Decimal {
	if s.exhausted {
		// Everything still unreversed, so the line comes back to exactly what
		// was charged however the earlier partials rounded.
		return total.Sub(credited)
	}

	// Safe to divide: a line with nothing left to return has already been
	// refused, and something left to return means sold exceeds what has come
	// back and is therefore above zero.
	target := total.Mul(s.returned).Div(s.sold).Round(moneyScale)

	// A target only rises as more of the line comes back, so the difference
	// cannot be negative — provided every earlier credit on the line was taken
	// the same way, which is the reason this is not merely a tidier spelling of
	// the proportional rule.
	return target.Sub(credited)
}

// ReturnEffects enumerates what a return must do, so nothing is quietly
// dropped.
//
// Blueprint C14 lists nine, and the ones that get forgotten are rarely the
// obvious four. Loyalty points earned on a returned sale stay in the
// customer's balance; commission stays in the salesperson's payroll. Both are
// real money leaving the business through a gap nobody notices until a
// reconciliation months later.
type ReturnEffects struct {
	InventoryRestored  bool // 1. quantity AND value
	RevenueReversed    bool // 2.
	OutputTaxReversed  bool // 3.
	COGSReversed       bool // 4.
	RefundSettled      bool // 5.
	LoyaltyReversed    bool // 6. easily forgotten
	CommissionReversed bool // 7. easily forgotten
	CreditNoteIssued   bool // 8. linked to the original
	JournalPosted      bool // 9. with its audit record
}

// Complete reports whether every required effect happened.
func (e ReturnEffects) Complete() bool {
	return e.InventoryRestored && e.RevenueReversed && e.OutputTaxReversed &&
		e.COGSReversed && e.RefundSettled && e.LoyaltyReversed &&
		e.CommissionReversed && e.CreditNoteIssued && e.JournalPosted
}

// Missing names the effects that did not happen, for an exception report.
func (e ReturnEffects) Missing() []string {
	var missing []string
	for _, c := range []struct {
		done bool
		name string
	}{
		{e.InventoryRestored, "inventory not restored"},
		{e.RevenueReversed, "revenue not reversed"},
		{e.OutputTaxReversed, "output tax not reversed"},
		{e.COGSReversed, "cost of goods sold not reversed"},
		{e.RefundSettled, "refund not settled"},
		{e.LoyaltyReversed, "loyalty points not reversed"},
		{e.CommissionReversed, "sales commission not reversed"},
		{e.CreditNoteIssued, "credit note not issued"},
		{e.JournalPosted, "journal entry not posted"},
	} {
		if !c.done {
			missing = append(missing, c.name)
		}
	}
	return missing
}

// ValidateRefunds checks that the refund settles the credit note exactly.
func ValidateRefunds(creditTotal decimal.Decimal, refunds []decimal.Decimal) error {
	sum := decimal.Zero
	for _, r := range refunds {
		if !r.IsPositive() {
			return errs.New(errs.CodeInvalidInput,
				"A refund amount must be greater than zero.")
		}
		sum = sum.Add(r)
	}

	if sum.Equal(creditTotal) {
		return nil
	}
	if sum.LessThan(creditTotal) {
		return errs.Newf(errs.CodeInvalidInput,
			"Refunds come to %s but the credit note is %s. %s is still owed to the customer.",
			sum.StringFixed(moneyScale), creditTotal.StringFixed(moneyScale),
			creditTotal.Sub(sum).StringFixed(moneyScale))
	}
	return errs.Newf(errs.CodeInvalidInput,
		"Refunds come to %s but the credit note is only %s, an overpayment of %s.",
		sum.StringFixed(moneyScale), creditTotal.StringFixed(moneyScale),
		sum.Sub(creditTotal).StringFixed(moneyScale))
}
