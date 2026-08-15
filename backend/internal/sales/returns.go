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
// # Why a full return is a special case
//
// Returning everything must refund exactly what was charged. Computing it as
// proportion × amount does not guarantee that: for a line of 3 at a net of
// 33.33, each unit is 11.11, and three of them is 33.33 only by luck — with
// 100.00 across 3 it is 33.33 × 3 = 99.99, and the customer is short a
// hallala on a full return of a whole invoice.
//
// So when the entire remaining quantity goes back, the stored amounts are used
// verbatim. Only a genuinely partial return computes a proportion, and there
// the rounding difference is real rather than an artefact.
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

		if line.IsFullReturn {
			// The line is now fully returned, so credit exactly what is left
			// unrefunded. For a single whole-line return that is the original
			// amount; for the last of several partials it absorbs the rounding
			// remainder the earlier ones left behind.
			//
			// Without this the customer is short. Three one-unit returns of a
			// line of 3 at a net of 100.00 each compute 33.33, totalling 99.99 —
			// the business quietly keeps a hallala on every partial-return
			// sequence that does not divide evenly. It is the same remainder
			// problem the invoice-discount allocation solves by giving the last
			// line what is left.
			line.NetAmount = orig.NetAmount.Sub(orig.NetReturned)
			line.TaxAmount = orig.TaxAmount.Sub(orig.TaxReturned)
			line.InvoiceDiscountAlloc = orig.InvoiceDiscountAlloc.Sub(orig.DiscountAllocReturned)
			line.COGSAmount = orig.COGSAmount.Sub(orig.COGSReturned)
		} else {
			proportion := req.Qty.Div(orig.QtySold)
			line.NetAmount = orig.NetAmount.Mul(proportion).Round(moneyScale)
			line.TaxAmount = orig.TaxAmount.Mul(proportion).Round(moneyScale)
			line.InvoiceDiscountAlloc = orig.InvoiceDiscountAlloc.Mul(proportion).Round(moneyScale)
			line.COGSAmount = orig.COGSAmount.Mul(proportion).Round(moneyScale)
		}

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
