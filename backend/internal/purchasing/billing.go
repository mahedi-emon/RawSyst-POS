package purchasing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Supplier bills, the three-way match, and paying.
//
// B5.2 calls the three-way match "the single most effective control against
// supplier overbilling and internal fraud", and the reason it works is that it
// compares three INDEPENDENTLY RECORDED numbers: what was ordered, what
// arrived, and what is being charged for. A system that derived any of the
// three from another could not detect the disagreement it exists to find.
//
// # What the match does and does not do
//
// It does not refuse the bill. A supplier who overbills has still sent a
// document the shop has to account for, and refusing to record it means the
// liability sits nowhere. So the bill is always recorded; what the match
// controls is whether it can be PAID, and a breach routes it to an approver
// with the discrepancy named.
//
// # Tolerance is both a percentage and an amount
//
// B5.2 gives "2% or SAR 50" as the example and both apply, whichever is larger.
// A percentage alone lets a large order absorb a big absolute overcharge; an
// absolute figure alone blocks every rounding difference on a small one.

type BillLine struct {
	POLineID     *uuid.UUID
	VariantID    *uuid.UUID
	Description  string
	Qty          decimal.Decimal
	UnitCost     decimal.Decimal
	TaxTreatment string
	TaxRate      decimal.Decimal
}

type NewBill struct {
	// UUID is assigned before the call, so a retry after a network failure is
	// recognised rather than recording the supplier's invoice twice.
	UUID uuid.UUID

	SupplierID  uuid.UUID
	POID        *uuid.UUID
	SupplierRef string
	BillDate    time.Time
	Lines       []BillLine

	// Currency the supplier billed in. Empty means the company's own, which is
	// the overwhelming majority of bills. A foreign one is translated at the
	// rate in force on the BILL DATE and carries that rate for life, so the
	// payable stays what the business agreed to owe even as the market moves —
	// the difference is realised when it is paid (0114).
	Currency string
}

type Bill struct {
	ID          uuid.UUID  `json:"id"`
	SupplierID  uuid.UUID  `json:"supplier_id"`
	Supplier    string     `json:"supplier"`
	SupplierRef string     `json:"supplier_ref"`
	POID        *uuid.UUID `json:"po_id,omitempty"`
	PONumber    string     `json:"po_number,omitempty"`
	BillDate    string     `json:"bill_date"`
	DueDate     string     `json:"due_date"`
	Currency    string     `json:"currency"`
	SubtotalNet string     `json:"subtotal_net"`
	TaxTotal    string     `json:"tax_total"`
	Total       string     `json:"total_inclusive"`
	AmountPaid  string     `json:"amount_paid"`
	// What was claimed back on this bill. Beside `amount_paid` rather than
	// folded into it: a credit is not a payment, and a supplier portal that
	// showed them as one would tell a supplier they had been paid for goods
	// they had taken back.
	AmountCredited string `json:"amount_credited"`
	Outstanding string     `json:"outstanding"`
	Status      string     `json:"status"`

	// Match is the evidence, kept rather than recomputed. A control that
	// leaves no record cannot be audited, and recomputing later would give a
	// different answer once someone amends the order — which is exactly when
	// somebody would want to check what it originally said.
	Match []MatchLine `json:"match,omitempty"`
	// Posted is false for a blocked bill: it is recorded but not in the
	// ledger until somebody accepts the discrepancy.
	Posted          bool `json:"posted"`
	AlreadyRecorded bool `json:"already_recorded"`
}

// MatchLine is one comparison the match made.
type MatchLine struct {
	Dimension   string `json:"dimension"`
	Description string `json:"description,omitempty"`
	Ordered     string `json:"ordered,omitempty"`
	Received    string `json:"received,omitempty"`
	// PreviouslyBilled is how much of what arrived earlier invoices have
	// already claimed. Reported separately from Received rather than netted
	// into it, because a buyer looking at a blocked bill needs to see both
	// facts: the goods did arrive, and they have already been invoiced.
	PreviouslyBilled string `json:"previously_billed,omitempty"`
	Billed           string `json:"billed,omitempty"`
	Variance         string `json:"variance"`
	VariancePct      string `json:"variance_pct,omitempty"`
	Outcome          string `json:"outcome"`
	Detail           string `json:"detail,omitempty"`
}

// RecordBill enters a supplier's invoice, matches it, and posts it if it passes.
func (s *Service) RecordBill(
	ctx context.Context, scope Scope, in NewBill,
) (Bill, error) {
	if len(in.Lines) == 0 {
		return Bill{}, errs.New(errs.CodeInvalidInput,
			"A bill needs at least one line.")
	}
	if trim(in.SupplierRef) == "" {
		return Bill{}, errs.New(errs.CodeInvalidInput,
			"Enter the supplier's own invoice number, so the same document cannot be paid twice.")
	}
	if in.UUID == uuid.Nil {
		return Bill{}, errs.New(errs.CodeInvalidInput,
			"A bill must carry an identifier so a retry is not recorded twice.")
	}

	var out Bill
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if existing, found, e := s.alreadyBilled(ctx, tx, scope, in.UUID); e != nil {
			return e
		} else if found {
			out = existing
			out.AlreadyRecorded = true
			return nil
		}

		var base string
		var tolerancePct, toleranceAmount decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT base_currency, match_tolerance_pct, match_tolerance_amount
			FROM company WHERE id = $1`, scope.CompanyID).
			Scan(&base, &tolerancePct, &toleranceAmount); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}
		currency := strings.ToUpper(strings.TrimSpace(in.Currency))
		if currency == "" {
			currency = base
		}

		var terms int
		var supplierName string
		if e := tx.QueryRow(ctx, `
			SELECT payment_terms_days, legal_name FROM supplier
			WHERE id = $1 AND company_id = $2`,
			in.SupplierID, scope.CompanyID).Scan(&terms, &supplierName); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That supplier was not found.")
			}
			return e
		}

		billDate := in.BillDate
		if billDate.IsZero() {
			billDate = time.Now().UTC()
		}

		// What one unit of the bill's currency was worth in the company's own
		// on the day it was raised. One when they are the same currency, which
		// is arithmetic rather than a market fact and needs no rate on file.
		//
		// A foreign bill with no rate recorded is REFUSED. Booking it at par
		// would put a figure in the ledger that is wrong by however far the
		// currencies differ, with nothing anywhere to indicate it — the same
		// judgement the tax registry makes about an unrecorded rate.
		rate := decimal.NewFromInt(1)
		if currency != base {
			if s.rates == nil {
				return errs.New(errs.CodeInternal,
					"This bill is in another currency and the purchasing "+
						"service was built without the exchange rates.")
			}
			r, e := s.rates.RateOn(ctx, tx, scope.TenantID, currency, base,
				billDate)
			if e != nil {
				return e
			}
			rate = r
		}
		// The due date follows the supplier's agreed terms rather than being
		// stated by the caller. Terms are negotiated with the supplier, not
		// chosen per invoice by whoever is typing it in.
		dueDate := billDate.AddDate(0, 0, terms)

		var billID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO purchase_bill
			  (tenant_id, company_id, supplier_id, po_id, supplier_ref, uuid,
			   bill_date, due_date, currency, fx_rate, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.SupplierID, in.POID,
			in.SupplierRef, in.UUID, billDate, dueDate, currency, rate,
			scope.UserID).Scan(&billID); e != nil {
			return conflictMessage(db.Translate(e, ""),
				"Invoice "+in.SupplierRef+" has already been recorded for this "+
					"supplier. Paying the same invoice twice is the commonest way "+
					"a shop loses money to its own paperwork, so it is refused.")
		}

		country, e := countryOf(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		subtotal, tax, total := decimal.Zero, decimal.Zero, decimal.Zero
		for i, line := range in.Lines {
			if !line.Qty.IsPositive() {
				return errs.Newf(errs.CodeInvalidInput,
					"Line %d has no quantity.", i+1)
			}
			if line.UnitCost.IsNegative() {
				return errs.Newf(errs.CodeInvalidInput,
					"Line %d has a negative cost.", i+1)
			}

			// From the register at the BILL date, not the order date and not
			// the request. A supplier invoicing in March for goods ordered in
			// January is taxed at March's rate, and that is the rate the shop
			// reclaims -- so it is read at the date on their paperwork.
			treatment, taxRate, e := s.taxRateFor(ctx, tx, scope, country,
				line.TaxTreatment, billDate)
			if e != nil {
				return e
			}
			if e := agreesOnRate(line.TaxRate, taxRate, i); e != nil {
				return e
			}

			net := line.Qty.Mul(line.UnitCost).Round(4)
			lineTax := net.Mul(taxRate).Round(4)
			gross := net.Add(lineTax)

			if _, e := tx.Exec(ctx, `
				INSERT INTO bill_line
				  (tenant_id, bill_id, po_line_id, variant_id, line_no,
				   description, qty_billed, unit_cost, tax_treatment, tax_rate,
				   net_amount, tax_amount, gross_amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
				scope.TenantID, billID, line.POLineID, line.VariantID, i+1,
				line.Description, line.Qty, line.UnitCost, treatment,
				taxRate, net, lineTax, gross); e != nil {
				return e
			}

			subtotal = subtotal.Add(net)
			tax = tax.Add(lineTax)
			total = total.Add(gross)
		}

		if _, e := tx.Exec(ctx, `
			UPDATE purchase_bill
			SET subtotal_net = $2, tax_total = $3, total_inclusive = $4
			WHERE id = $1`, billID, subtotal, tax, total); e != nil {
			return e
		}

		match, breached, e := s.runMatch(ctx, tx, scope, billID, in,
			tolerancePct, toleranceAmount)
		if e != nil {
			return e
		}

		status := "matched"
		if breached {
			// Recorded, not refused. A supplier who overbills has still sent a
			// document the shop must account for; what is withheld is payment.
			status = "blocked"
		}

		if !breached {
			if e := s.postBill(ctx, tx, scope, billID, billDate,
				subtotal, tax, total, supplierName); e != nil {
				return e
			}
		}

		if _, e := tx.Exec(ctx,
			`UPDATE purchase_bill SET status = $2 WHERE id = $1`,
			billID, status); e != nil {
			return e
		}

		read, e := s.readBill(ctx, tx, billID)
		if e != nil {
			return e
		}
		read.Match = match
		out = read
		return nil
	})
	return out, err
}

// postBill writes the journal through the seeded purchase.credit rule.
//
// Rule 3, unchanged: stock at landed cost, recoverable tax separated, the whole
// gross owed to the supplier. E2.5 puts duty in inventory cost and recoverable
// tax in a receivable, and merging them would overstate stock while
// understating the reclaim.
func (s *Service) postBill(
	ctx context.Context, tx pgx.Tx, scope Scope, billID uuid.UUID,
	billDate time.Time, net, tax, total decimal.Decimal, supplier string,
) error {
	var country string
	if err := tx.QueryRow(ctx,
		`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
		Scan(&country); err != nil {
		return err
	}

	// How much of this bill answers stock already received and accrued.
	//
	// Taken from the RECEIPT's own recorded value, not from the bill's, and
	// capped at what the bill actually claims. A supplier billing a different
	// price is a discrepancy the three-way match reports; it must not change
	// how much accrual is discharged, or the accrual would never clear to zero
	// on the receipt it belongs to.
	accrued, err := s.accruedFor(ctx, tx, billID)
	if err != nil {
		return err
	}
	if accrued.GreaterThan(net) {
		accrued = net
	}

	// Anything the bill charges beyond what was received and accrued goes
	// straight to inventory, as it always did. That covers a bill with no
	// receipt behind it — rent, a utility, a consultant — and the excess on a
	// bill that overcharges, which is held by the match anyway.
	unaccrued := net.Sub(accrued)

	rule := "purchase.clear_accrual"
	if accrued.IsZero() {
		// Nothing accrued, so nothing to discharge: the original Rule 3, whose
		// shape is unchanged and whose history stays readable.
		rule = "purchase.credit"
	}

	amounts := map[string]decimal.Decimal{
		"net_amount":       net,
		"tax_amount":       tax,
		"total_inclusive":  total,
		"accrued_amount":   accrued,
		"unaccrued_amount": unaccrued,
	}

	result, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date: billDate, SourceType: "purchase_bill", SourceID: billID,
		PostedBy: &scope.UserID,
		RuleKey:  rule,
		Memo:     "Purchase from " + supplier,
	}, country, accounting.Transaction{Amounts: amounts})
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		`UPDATE purchase_bill SET journal_entry_id = $2 WHERE id = $1`,
		billID, result.EntryID)
	return err
}

// runMatch compares the bill against the order and the receipts.
//
// Four dimensions per B5.2: quantity, price, tax and total. Each is recorded
// whether it passes or not, because "we checked and it was fine" is itself
// evidence an auditor asks for.
//
// They are deliberately orthogonal, so one disagreement is reported once rather
// than showing up on three dimensions and looking like three problems:
//
//	qty    billed against RECEIVED AND NOT ALREADY BILLED, at no tolerance
//	price  unit cost against the agreed unit cost, per line
//	tax    measured on the bill's own net, at each side's rate
//	total  billed net against agreed net, over the whole order
//
// B5.2 names discount as a fifth. Nothing in purchasing records one — not
// purchase_order, not po_line, not bill_line — so there is no agreed figure to
// compare a billed one against, and a dimension invented out of nothing would
// be a false assurance rather than a control. It is left out on purpose.
func (s *Service) runMatch(
	ctx context.Context, tx pgx.Tx, scope Scope, billID uuid.UUID,
	in NewBill, tolerancePct, toleranceAmount decimal.Decimal,
) ([]MatchLine, bool, error) {
	out := []MatchLine{}

	// A bill with no order behind it cannot be three-way matched — there is
	// only one document. Said plainly rather than silently passing: an expense
	// invoice with no PO is legitimate, and pretending it was matched would be
	// a false assurance.
	if in.POID == nil {
		line := MatchLine{
			Dimension: "total", Outcome: "pass", Variance: "0",
			Detail: "No purchase order, so there is nothing to match against. " +
				"This bill was entered directly.",
		}
		out = append(out, line)
		if err := recordMatch(ctx, tx, scope, billID, nil, line); err != nil {
			return nil, false, err
		}
		return out, false, nil
	}

	breached := false

	// Running totals for the fourth dimension, over the lines that actually
	// have an order line behind them. A line the order never had is already a
	// breach on its own account; folding its value into a total comparison
	// would double-report it.
	agreedTotal, billedTotal := decimal.Zero, decimal.Zero
	matched := 0

	// What THIS bill puts on each order line, totalled before the walk begins.
	//
	// po_outstanding reports the billed quantity from bill_line, and this
	// bill's own lines are already written by the time the match runs — so the
	// figure it returns includes them. Subtracting what this document claims
	// leaves what the EARLIER documents claimed, which is the only part that
	// should reduce what is still available to invoice.
	thisBill := map[uuid.UUID]decimal.Decimal{}
	for _, l := range in.Lines {
		if l.POLineID != nil {
			thisBill[*l.POLineID] = thisBill[*l.POLineID].Add(l.Qty)
		}
	}

	// How much of each order line's receipts has been answered by an invoice
	// so far. It starts at what earlier bills claimed and rises as this bill's
	// own lines are walked, so a bill that names one order line twice cannot
	// be paid twice over for the same delivery either.
	consumed := map[uuid.UUID]decimal.Decimal{}

	for _, line := range in.Lines {
		if line.POLineID == nil {
			// A line the order never had. Always a breach: it is the shape a
			// supplier's padded invoice takes.
			m := MatchLine{
				Dimension: "qty", Description: line.Description,
				Billed: line.Qty.String(), Variance: line.Qty.String(),
				Outcome: "breach",
				Detail:  "This line is not on the purchase order.",
			}
			out = append(out, m)
			breached = true
			if err := recordMatch(ctx, tx, scope, billID, nil, m); err != nil {
				return nil, false, err
			}
			continue
		}

		var ordered, received, billedOnPOLine, unitCost, agreedRate decimal.Decimal
		var description, agreedTreatment string
		if err := tx.QueryRow(ctx, `
			SELECT o.qty_ordered, o.qty_received, o.qty_billed, o.unit_cost,
			       o.description, l.tax_treatment, l.tax_rate
			FROM po_outstanding($1) o
			JOIN po_line l ON l.id = o.po_line_id
			WHERE o.po_line_id = $2`,
			*in.POID, *line.POLineID).
			Scan(&ordered, &received, &billedOnPOLine, &unitCost, &description,
				&agreedTreatment, &agreedRate); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				m := MatchLine{
					Dimension: "qty", Description: line.Description,
					Billed: line.Qty.String(), Variance: line.Qty.String(),
					Outcome: "breach",
					Detail:  "This line belongs to a different purchase order.",
				}
				out = append(out, m)
				breached = true
				if e := recordMatch(ctx, tx, scope, billID, nil, m); e != nil {
					return nil, false, e
				}
				continue
			}
			return nil, false, err
		}

		// Quantity: billed against what was RECEIVED AND IS NOT ALREADY ON AN
		// INVOICE, not against ordered.
		//
		// Against received alone this control has a hole a supplier can drive a
		// second invoice through. A hundred arrive and are billed; the same
		// hundred are billed again under a different invoice number, so the
		// unique key on (supplier, supplier_ref) does not fire; each bill on its
		// own reads "billed 100 against 100 received" and passes. Nothing else
		// in the flow compares the two documents, so the shop pays for the goods
		// twice and the accrual is discharged twice with it.
		//
		// Comparing to what is still outstanding closes it, and closes the same
		// hole in the other direction: a supplier who ships 90 of 100 and bills
		// for 100 is still caught, because 100 against 90 outstanding is exactly
		// the comparison that was there before when nothing had been billed yet.
		poLineID := *line.POLineID
		if _, walked := consumed[poLineID]; !walked {
			earlier := billedOnPOLine.Sub(thisBill[poLineID])
			if earlier.IsNegative() {
				// Only reachable if a bill line were written between the insert
				// above and this read, which the transaction prevents. Floored
				// rather than trusted: a negative here would INVENT quantity to
				// bill against.
				earlier = decimal.Zero
			}
			consumed[poLineID] = earlier
		}
		alreadyBilled := consumed[poLineID]
		outstanding := received.Sub(alreadyBilled)
		consumed[poLineID] = alreadyBilled.Add(line.Qty)

		qtyVariance := line.Qty.Sub(outstanding)
		qtyMatch := MatchLine{
			Dimension: "qty", Description: description,
			Ordered: ordered.String(), Received: received.String(),
			Billed: line.Qty.String(), Variance: qtyVariance.String(),
		}
		if alreadyBilled.IsPositive() {
			qtyMatch.PreviouslyBilled = alreadyBilled.String()
		}
		qtyMatch.Outcome, qtyMatch.Detail =
			judgeQty(qtyVariance, outstanding, alreadyBilled)
		if qtyMatch.Outcome == "breach" {
			breached = true
		}
		out = append(out, qtyMatch)
		if err := recordMatch(ctx, tx, scope, billID, nil, qtyMatch); err != nil {
			return nil, false, err
		}

		// Price: billed unit cost against the agreed one.
		//
		// The tolerance needs converting first. "2% or SAR 50" describes money
		// the shop decided not to argue over, and SAR 50 compared against a
		// UNIT price stops being a sum of money and becomes a rate: fifty
		// riyals per unit, times however many units were bought. A thousand
		// units at SAR 10.00 agreed, billed at SAR 59.99, is out by SAR 49.99 a
		// unit — under the tolerance — and SAR 49,990 on the line. So the
		// absolute figure is spread across the units it will be multiplied by.
		// The percentage needs no such treatment, being scale-free already.
		unitTolerance := toleranceAmount
		if line.Qty.IsPositive() {
			unitTolerance = toleranceAmount.Div(line.Qty)
		}

		priceVariance := line.UnitCost.Sub(unitCost)
		priceMatch := MatchLine{
			Dimension: "price", Description: description,
			Ordered: unitCost.String(), Billed: line.UnitCost.String(),
			Variance: priceVariance.String(),
		}
		priceMatch.Outcome, priceMatch.VariancePct, priceMatch.Detail =
			judgeAmount(priceVariance, unitCost, tolerancePct, unitTolerance,
				"The unit price is above what was agreed.")
		if priceMatch.Outcome == "breach" {
			breached = true
		}
		out = append(out, priceMatch)
		if err := recordMatch(ctx, tx, scope, billID, nil, priceMatch); err != nil {
			return nil, false, err
		}

		// Tax, the third dimension the design names.
		//
		// po_line carries the treatment AGREED with the supplier, which is not
		// necessarily how the item is sold — imported goods are frequently
		// zero-rated inbound and standard-rated on sale. A supplier who bills
		// fifteen per cent on a line agreed at zero has changed neither the
		// quantity nor the unit price, so both comparisons above pass, and the
		// shop pays VAT it never agreed to.
		//
		// Measured on the bill's OWN net, at each rate. Using the agreed net
		// instead would fold a price disagreement into this figure and report
		// one overcharge twice, on two dimensions, as though it were two.
		billedNet := line.Qty.Mul(line.UnitCost).Round(4)
		taxBilled := billedNet.Mul(line.TaxRate).Round(4)
		taxAgreed := billedNet.Mul(agreedRate).Round(4)

		taxDetail := "More VAT than was agreed for this line."
		treatment := line.TaxTreatment
		if treatment == "" {
			treatment = "standard"
		}
		if treatment != agreedTreatment {
			// Worth naming, because the amount alone does not explain itself:
			// an approver needs to know the supplier has re-categorised the
			// supply, not merely arrived at a different number.
			taxDetail = "Billed as " + treatment + " where " + agreedTreatment +
				" was agreed."
		}

		taxMatch := MatchLine{
			Dimension: "tax", Description: description,
			Ordered: taxAgreed.String(), Billed: taxBilled.String(),
			Variance: taxBilled.Sub(taxAgreed).String(),
		}
		taxMatch.Outcome, taxMatch.VariancePct, taxMatch.Detail =
			judgeAmount(taxBilled.Sub(taxAgreed), taxAgreed,
				tolerancePct, toleranceAmount, taxDetail)
		if taxMatch.Outcome == "breach" {
			breached = true
		}
		out = append(out, taxMatch)
		if err := recordMatch(ctx, tx, scope, billID, nil, taxMatch); err != nil {
			return nil, false, err
		}

		agreedTotal = agreedTotal.Add(line.Qty.Mul(unitCost).Round(4))
		billedTotal = billedTotal.Add(billedNet)
		matched++
	}

	// Total value, the fourth dimension, and not redundant once every line has
	// been priced.
	//
	// The absolute tolerance is granted once PER LINE. Ten lines each forgiven
	// most of SAR 50 forgive most of SAR 500 between them, and a supplier who
	// knows the tolerance can spread an overcharge so that no single line ever
	// trips it. Three lines of 100 units at SAR 10.00 agreed, billed at SAR
	// 10.45, are each out by SAR 45 against the SAR 50 a line is allowed — and
	// the bill is out by SAR 135 against the SAR 60 the order is allowed.
	//
	// Both sides use the BILLED quantities, so only price is in play here. A
	// quantity disagreement has its own dimension above, at no tolerance at all,
	// and putting the ordered quantity on one side of this comparison would
	// report that same disagreement a second time as though it were a second
	// problem. Net, not gross, for the same reason with respect to tax.
	if matched > 0 {
		totalVariance := billedTotal.Sub(agreedTotal)
		totalMatch := MatchLine{
			Dimension: "total",
			Ordered:   agreedTotal.String(), Billed: billedTotal.String(),
			Variance: totalVariance.String(),
		}
		totalMatch.Outcome, totalMatch.VariancePct, totalMatch.Detail =
			judgeAmount(totalVariance, agreedTotal,
				tolerancePct, toleranceAmount,
				"The bill totals more than the goods were agreed to cost, even "+
					"though no single line is out by enough to say so.")
		if totalMatch.Outcome == "breach" {
			breached = true
		}
		out = append(out, totalMatch)
		if err := recordMatch(ctx, tx, scope, billID, nil, totalMatch); err != nil {
			return nil, false, err
		}
	}

	return out, breached, nil
}

// judgeQty decides a quantity comparison.
//
// Asymmetric on purpose. Being billed for LESS than is outstanding is in the
// shop's favour and needs no approval — a supplier undercharging, or invoicing
// a delivery in instalments, is not something to block a buyer's afternoon
// over. Being billed for more is the fraud case, and any amount of it is a
// breach: there is no tolerance for goods that never came or that somebody has
// already been paid for, because a quantity is a count rather than a
// measurement and cannot be out by rounding.
func judgeQty(
	variance, outstanding, alreadyBilled decimal.Decimal,
) (outcome, detail string) {
	if variance.IsZero() {
		return "pass", ""
	}
	if variance.IsNegative() {
		return "pass", "Billed for less than is outstanding, which is in your favour."
	}
	if alreadyBilled.IsPositive() {
		// Named apart from "more than has been received", because the two have
		// different answers. Goods that never came are a delivery dispute with
		// the supplier; goods already invoiced are a duplicate to be credited,
		// and telling a buyer the wrong one sends them to the wrong
		// conversation.
		return "breach", "Earlier invoices have already billed " +
			alreadyBilled.String() + " of what was received on this line, so " +
			"only " + outstanding.String() + " is still outstanding."
	}
	if !outstanding.IsPositive() {
		return "breach", "Billed for goods that have not been received at all."
	}
	return "breach", "Billed for more than has been received."
}

// judgeAmount decides a money comparison against both tolerances.
//
// Whichever is LARGER wins, per B5.2. A percentage alone lets a large order
// absorb a big absolute overcharge; an absolute figure alone blocks every
// rounding difference on a small one.
func judgeAmount(
	variance, baseline, tolerancePct, toleranceAmount decimal.Decimal,
	breachDetail string,
) (outcome, pct, detail string) {
	if variance.IsZero() {
		return "pass", "", ""
	}
	// Undercharging is in the shop's favour and never blocks a payment.
	if variance.IsNegative() {
		return "pass", "", "Charged less than agreed, which is in your favour."
	}

	allowed := toleranceAmount
	if baseline.IsPositive() {
		byPercent := baseline.Mul(tolerancePct).Div(decimal.NewFromInt(100))
		if byPercent.GreaterThan(allowed) {
			allowed = byPercent
		}
		pct = percentOf(variance, baseline)
	}

	if variance.LessThanOrEqual(allowed) {
		return "within_tolerance", pct,
			"Above what was agreed, but inside the tolerance you have set."
	}
	return "breach", pct, breachDetail
}

// pctLimit is the largest percentage three_way_match.variance_pct can hold:
// numeric(9,4) is nine digits with four after the point, so 99999.9999.
var pctLimit = decimal.RequireFromString("100000")

// percentOf is the variance as a percentage of the baseline, or nothing when
// that percentage will not fit the column it is stored in.
//
// A tiny baseline makes the percentage enormous — a line agreed at one hallala
// and billed at ten thousand riyals is ninety-nine million per cent — and
// writing that into numeric(9,4) is a `numeric field overflow`, which aborts
// the whole transaction. The bill is then not recorded at all and the buyer
// gets a server error however many times they retry, so the very worst
// overcharge is the one nobody can enter, let alone block.
//
// Returning nothing rather than a clamped figure, because a clamped one would
// be a wrong number presented as a right one. The absolute variance is stored
// at full precision beside it and says everything the percentage would.
func percentOf(variance, baseline decimal.Decimal) string {
	pct := variance.Div(baseline).Mul(decimal.NewFromInt(100)).Round(2)
	if pct.Abs().GreaterThanOrEqual(pctLimit) {
		return ""
	}
	return pct.String()
}

func recordMatch(
	ctx context.Context, tx pgx.Tx, scope Scope,
	billID uuid.UUID, lineID *uuid.UUID, m MatchLine,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO three_way_match
		  (tenant_id, bill_id, bill_line_id, dimension,
		   ordered, received, previously_billed, billed, variance,
		   variance_pct, outcome, detail)
		VALUES ($1,$2,$3,$4,
		        nullif($5,'')::numeric, nullif($6,'')::numeric,
		        nullif($7,'')::numeric, nullif($8,'')::numeric, $9::numeric,
		        nullif($10,'')::numeric, $11, $12)`,
		scope.TenantID, billID, lineID, m.Dimension,
		m.Ordered, m.Received, m.PreviouslyBilled, m.Billed, m.Variance,
		m.VariancePct, m.Outcome, nullText(m.Detail))
	return err
}

// accruedFor is how much of a bill answers goods already received and accrued.
//
// Per line, by quantity: the receipt's own unit cost — which includes its share
// of landed cost — times whichever is smaller, what is still accrued or what is
// being billed. Using the bill's price instead would let a supplier's overcharge
// discharge more accrual than was ever raised, and the GRNI balance would drift
// away from the goods it represents.
//
// "Still accrued" and "received" are the same figure only for the first invoice
// against a delivery. On the second they are not, and taking the received
// quantity would discharge the accrual a second time for goods it was only ever
// raised on once — driving Goods Received Not Invoiced through zero and into a
// debit, which is a liability account reporting that the shop is owed money by
// its own stockroom. The match now blocks the duplicate that produces this, so
// this figure should never be asked for it; it is derived correctly anyway,
// because an approver may let a blocked bill through and the ledger must still
// be right when they do.
func (s *Service) accruedFor(
	ctx context.Context, tx pgx.Tx, billID uuid.UUID,
) (decimal.Decimal, error) {
	var accrued decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(
			least(bl.qty_billed, greatest(r.received - coalesce(e.billed, 0), 0))
			* r.unit_cost
		), 0)
		FROM bill_line bl
		JOIN (
			SELECT gl.po_line_id,
			       sum(gl.qty_received - gl.qty_rejected) AS received,
			       -- Weighted, because two deliveries against one order line can
			       -- carry different freight and therefore different unit costs.
			       CASE WHEN sum(gl.qty_received - gl.qty_rejected) > 0
			            THEN sum((gl.qty_received - gl.qty_rejected) * gl.unit_cost)
			                 / sum(gl.qty_received - gl.qty_rejected)
			            ELSE 0 END AS unit_cost
			FROM grn_line gl
			GROUP BY gl.po_line_id
		) r ON r.po_line_id = bl.po_line_id
		-- What EARLIER bills already answered on this order line. This bill's
		-- own lines are excluded by id, so a bill is never measured against
		-- itself.
		LEFT JOIN (
			SELECT bl2.po_line_id, sum(bl2.qty_billed) AS billed
			FROM bill_line bl2
			JOIN purchase_bill pb ON pb.id = bl2.bill_id
			WHERE bl2.bill_id <> $1 AND bl2.po_line_id IS NOT NULL
			  AND pb.status <> 'cancelled'
			GROUP BY bl2.po_line_id
		) e ON e.po_line_id = bl.po_line_id
		WHERE bl.bill_id = $1 AND bl.po_line_id IS NOT NULL`,
		billID).Scan(&accrued)
	return accrued.Round(4), err
}
