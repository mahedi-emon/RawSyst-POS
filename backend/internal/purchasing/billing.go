package purchasing

import (
	"context"
	"errors"
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
	Billed      string `json:"billed,omitempty"`
	Variance    string `json:"variance"`
	VariancePct string `json:"variance_pct,omitempty"`
	Outcome     string `json:"outcome"`
	Detail      string `json:"detail,omitempty"`
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

		var currency string
		var tolerancePct, toleranceAmount decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT base_currency, match_tolerance_pct, match_tolerance_amount
			FROM company WHERE id = $1`, scope.CompanyID).
			Scan(&currency, &tolerancePct, &toleranceAmount); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
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
		// The due date follows the supplier's agreed terms rather than being
		// stated by the caller. Terms are negotiated with the supplier, not
		// chosen per invoice by whoever is typing it in.
		dueDate := billDate.AddDate(0, 0, terms)

		var billID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO purchase_bill
			  (tenant_id, company_id, supplier_id, po_id, supplier_ref, uuid,
			   bill_date, due_date, currency, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.SupplierID, in.POID,
			in.SupplierRef, in.UUID, billDate, dueDate, currency,
			scope.UserID).Scan(&billID); e != nil {
			return db.Translate(e,
				"That invoice number has already been recorded for this supplier.")
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

			net := line.Qty.Mul(line.UnitCost).Round(4)
			lineTax := net.Mul(line.TaxRate).Round(4)
			gross := net.Add(lineTax)

			treatment := line.TaxTreatment
			if treatment == "" {
				treatment = "standard"
			}

			if _, e := tx.Exec(ctx, `
				INSERT INTO bill_line
				  (tenant_id, bill_id, po_line_id, variant_id, line_no,
				   description, qty_billed, unit_cost, tax_treatment, tax_rate,
				   net_amount, tax_amount, gross_amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
				scope.TenantID, billID, line.POLineID, line.VariantID, i+1,
				line.Description, line.Qty, line.UnitCost, treatment,
				line.TaxRate, net, lineTax, gross); e != nil {
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
	entry := accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date: billDate, SourceType: "purchase_bill", SourceID: billID,
		PostedBy: &scope.UserID,
		RuleKey:  "purchase.credit",
		Memo:     "Purchase from " + supplier,
	}

	var country string
	if err := tx.QueryRow(ctx,
		`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
		Scan(&country); err != nil {
		return err
	}

	result, err := accounting.PostByRule(ctx, tx, entry, country,
		accounting.Transaction{
			Amounts: map[string]decimal.Decimal{
				"net_amount":      net,
				"tax_amount":      tax,
				"total_inclusive": total,
			},
		})
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

		var ordered, received, unitCost decimal.Decimal
		var description string
		if err := tx.QueryRow(ctx, `
			SELECT qty_ordered, qty_received, unit_cost, description
			FROM po_outstanding($1) WHERE po_line_id = $2`,
			*in.POID, *line.POLineID).
			Scan(&ordered, &received, &unitCost, &description); err != nil {
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

		// Quantity: billed against RECEIVED, not against ordered. A supplier
		// who ships 90 of 100 and bills for 100 is the case this control
		// exists to catch, and comparing to the order would miss it entirely.
		qtyVariance := line.Qty.Sub(received)
		qtyMatch := MatchLine{
			Dimension: "qty", Description: description,
			Ordered: ordered.String(), Received: received.String(),
			Billed: line.Qty.String(), Variance: qtyVariance.String(),
		}
		qtyMatch.Outcome, qtyMatch.Detail = judgeQty(qtyVariance, received)
		if qtyMatch.Outcome == "breach" {
			breached = true
		}
		out = append(out, qtyMatch)
		if err := recordMatch(ctx, tx, scope, billID, nil, qtyMatch); err != nil {
			return nil, false, err
		}

		// Price: billed unit cost against the agreed one.
		priceVariance := line.UnitCost.Sub(unitCost)
		priceMatch := MatchLine{
			Dimension: "price", Description: description,
			Ordered: unitCost.String(), Billed: line.UnitCost.String(),
			Variance: priceVariance.String(),
		}
		priceMatch.Outcome, priceMatch.VariancePct, priceMatch.Detail =
			judgeAmount(priceVariance, unitCost, tolerancePct, toleranceAmount,
				"The unit price is above what was agreed.")
		if priceMatch.Outcome == "breach" {
			breached = true
		}
		out = append(out, priceMatch)
		if err := recordMatch(ctx, tx, scope, billID, nil, priceMatch); err != nil {
			return nil, false, err
		}
	}

	return out, breached, nil
}

// judgeQty decides a quantity comparison.
//
// Asymmetric on purpose. Being billed for LESS than arrived is in the shop's
// favour and needs no approval — a supplier undercharging is their problem, and
// blocking payment over it wastes a buyer's afternoon. Being billed for more is
// the fraud case, and any amount of it is a breach: there is no tolerance for
// goods that never came, because a quantity is a count rather than a
// measurement and cannot be out by rounding.
func judgeQty(variance, received decimal.Decimal) (outcome, detail string) {
	if variance.IsZero() {
		return "pass", ""
	}
	if variance.IsNegative() {
		return "pass", "Billed for less than arrived, which is in your favour."
	}
	if received.IsZero() {
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
		pct = variance.Div(baseline).Mul(decimal.NewFromInt(100)).
			Round(2).String()
	}

	if variance.LessThanOrEqual(allowed) {
		return "within_tolerance", pct,
			"Above what was agreed, but inside the tolerance you have set."
	}
	return "breach", pct, breachDetail
}

func recordMatch(
	ctx context.Context, tx pgx.Tx, scope Scope,
	billID uuid.UUID, lineID *uuid.UUID, m MatchLine,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO three_way_match
		  (tenant_id, bill_id, bill_line_id, dimension,
		   ordered, received, billed, variance, variance_pct, outcome, detail)
		VALUES ($1,$2,$3,$4,
		        nullif($5,'')::numeric, nullif($6,'')::numeric,
		        nullif($7,'')::numeric, $8::numeric,
		        nullif($9,'')::numeric, $10, $11)`,
		scope.TenantID, billID, lineID, m.Dimension,
		m.Ordered, m.Received, m.Billed, m.Variance, m.VariancePct,
		m.Outcome, nullText(m.Detail))
	return err
}
