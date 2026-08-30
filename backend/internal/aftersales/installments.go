// Instalment / EMI plans (blueprint B14).
//
// # The rounding rule this module inherits
//
// A plan divides one amount into N instalments, and the last one takes the
// remainder. 1,000 over 3 is not 333.33 three times — that is 999.99, and the
// hundredth that goes missing is a debt the customer never clears and the
// receivable never closes. The same rule already governs invoice discount
// allocation, partial returns and stock consumption.
package aftersales

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

// Plan is an instalment agreement.
type Plan struct {
	ID         uuid.UUID `json:"id"`
	Number     string    `json:"plan_no"`
	Status     string    `json:"status"`
	CustomerID uuid.UUID `json:"customer_id"`
	Customer   string    `json:"customer,omitempty"`
	InvoiceID  uuid.UUID `json:"invoice_id"`

	Principal   string `json:"principal"`
	DownPayment string `json:"down_payment"`
	Financed    string `json:"financed"`
	MarkupRate  string `json:"markup_rate"`
	Markup      string `json:"markup_amount"`
	Tenure      int    `json:"tenure_months"`
	Installment string `json:"installment_amount"`

	LateFeeFlat string `json:"late_fee_flat"`
	LateFeeRate string `json:"late_fee_rate"`
	GraceDays   int    `json:"grace_days"`

	Currency string `json:"currency"`
	StartsOn string `json:"starts_on"`

	GuarantorName  string `json:"guarantor_name,omitempty"`
	GuarantorPhone string `json:"guarantor_phone,omitempty"`

	// Outstanding is what is still owed on the schedule: the total of the
	// instalments less what has been paid or waived. Summed, never stored.
	Outstanding string `json:"outstanding"`

	Schedule []Due `json:"schedule,omitempty"`
}

// Due is one instalment.
type Due struct {
	ID      uuid.UUID `json:"id"`
	Seq     int       `json:"seq"`
	DueOn   string    `json:"due_on"`
	Amount  string    `json:"amount"`
	Paid    string    `json:"paid"`
	Waived  string    `json:"waived"`
	LateFee string    `json:"late_fee"`
	// State is computed by the database from the money and the date, so it
	// cannot go stale the way a stored status would overnight.
	State string `json:"state"`
}

// NewPlan is an agreement being set up.
type NewPlan struct {
	CustomerID  uuid.UUID
	InvoiceID   uuid.UUID
	DownPayment decimal.Decimal
	MarkupRate  decimal.Decimal
	Tenure      int
	StartsOn    time.Time

	LateFeeFlat decimal.Decimal
	LateFeeRate decimal.Decimal
	GraceDays   int

	GuarantorName  string
	GuarantorPhone string
	GuarantorIDNo  string
	Verification   string
}

// OpenPlan turns a credit invoice into a schedule.
//
// It does NOT post the sale. The invoice already did that — revenue, VAT and
// cost of goods all landed when the goods went out — and the customer already
// owes the whole amount as an ordinary receivable. What this adds is the
// finance charge, which is money the shop will earn for waiting and is
// therefore not income yet.
func (s *Service) OpenPlan(
	ctx context.Context, scope Scope, in NewPlan,
) (Plan, error) {
	if in.Tenure <= 0 {
		return Plan{}, errs.Validation("Say how many months it runs for.").
			WithField("tenure_months", "Three, six, twelve or twenty-four.")
	}
	if in.DownPayment.IsNegative() {
		return Plan{}, errs.New(errs.CodeInvalidInput,
			"A down payment cannot be negative.")
	}
	if in.MarkupRate.IsNegative() {
		return Plan{}, errs.New(errs.CodeInvalidInput,
			"A markup rate cannot be negative.")
	}

	var out Plan
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var principal decimal.Decimal
		var currency string
		var invoiceCustomer *uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT total_inclusive, currency, customer_id
			FROM sales_invoice WHERE id = $1 AND company_id = $2`,
			in.InvoiceID, scope.CompanyID).
			Scan(&principal, &currency, &invoiceCustomer)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That invoice was not found.")
		}
		if e != nil {
			return e
		}

		// The plan must be for the person who bought the goods. Otherwise a
		// schedule would collect against one customer's ledger for another's
		// receivable, and neither balance would be right.
		if invoiceCustomer == nil || *invoiceCustomer != in.CustomerID {
			return errs.New(errs.CodeInvalidInput,
				"That invoice belongs to a different customer.")
		}
		if in.DownPayment.GreaterThan(principal) {
			return errs.New(errs.CodeInvalidInput,
				"The down payment is more than the invoice.")
		}

		financed := principal.Sub(in.DownPayment)
		markup := financed.Mul(in.MarkupRate).Round(2)
		total := financed.Add(markup)

		// B14's example: 1,200 − 300 down = 900 over 3 = 300 a month.
		base := total.Div(decimal.NewFromInt(int64(in.Tenure))).
			RoundDown(2)
		if !base.IsPositive() {
			return errs.New(errs.CodeInvalidInput,
				"There is nothing left to spread over instalments.")
		}

		number, e := claimNo(ctx, tx, scope.CompanyID, "installment", "EMI")
		if e != nil {
			return e
		}

		var planID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO installment_plan
			  (tenant_id, company_id, plan_no, customer_id, invoice_id,
			   status, principal, down_payment, financed, markup_rate,
			   markup_amount, tenure_months, installment_amount,
			   late_fee_flat, late_fee_rate, grace_days, currency, starts_on,
			   guarantor_name, guarantor_phone, guarantor_id_no,
			   verification_note, created_by)
			VALUES ($1,$2,$3,$4,$5,'active',$6,$7,$8,$9,$10,$11,$12,$13,$14,
			        $15,$16,$17,$18,$19,$20,$21,$22)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, number, in.CustomerID,
			in.InvoiceID, principal, in.DownPayment, financed, in.MarkupRate,
			markup, in.Tenure, base, in.LateFeeFlat, in.LateFeeRate,
			in.GraceDays, currency, in.StartsOn,
			nullText(in.GuarantorName), nullText(in.GuarantorPhone),
			nullText(in.GuarantorIDNo), nullText(in.Verification),
			scope.UserID).Scan(&planID); e != nil {
			return db.Translate(e,
				"That invoice already has an instalment plan.")
		}

		// The schedule. The LAST instalment takes the remainder, so the parts
		// add back to the whole — see the package comment.
		running := decimal.Zero
		for i := 1; i <= in.Tenure; i++ {
			amount := base
			if i == in.Tenure {
				amount = total.Sub(running)
			}
			running = running.Add(amount)

			if _, e := tx.Exec(ctx, `
				INSERT INTO installment_due
				  (tenant_id, plan_id, seq, due_on, amount)
				VALUES ($1,$2,$3,$4,$5)`,
				scope.TenantID, planID, i,
				in.StartsOn.AddDate(0, i, 0), amount); e != nil {
				return e
			}
		}

		// The finance charge: owed by the customer, not yet earned by the shop.
		if markup.IsPositive() {
			var country string
			if e := tx.QueryRow(ctx,
				`SELECT country FROM company WHERE id = $1`,
				scope.CompanyID).Scan(&country); e != nil {
				return e
			}
			if _, e := accounting.PostByRule(ctx, tx, accounting.Entry{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				Date:       in.StartsOn,
				SourceType: "installment_plan", SourceID: planID,
				RuleKey:      "installment.open",
				Currency:     currency,
				BaseCurrency: currency,
				FXRate:       decimal.NewFromInt(1),
				Memo:         "Finance charge on " + number,
				PostedBy:     &scope.UserID,
			}, country, accounting.Transaction{
				Amounts: map[string]decimal.Decimal{"markup": markup},
			}); e != nil {
				return e
			}
		}

		read, e := s.readPlan(ctx, tx, scope.CompanyID, planID)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// CollectInstalment marks a receipt against instalments.
//
// The MONEY is not moved here: a customer receipt already exists, already
// posts the cash and already reduces the receivable. This records what the
// receipt was for, oldest instalment first, so the schedule can be marked off
// without a second payment path that would count the money twice.
func (s *Service) CollectInstalment(
	ctx context.Context, scope Scope, planID, receiptID uuid.UUID,
	amount decimal.Decimal,
) (Plan, error) {
	if !amount.IsPositive() {
		return Plan{}, errs.New(errs.CodeInvalidInput,
			"Say how much was collected.")
	}

	var out Plan
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status string
		e := tx.QueryRow(ctx, `
			SELECT status FROM installment_plan
			WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			planID, scope.CompanyID).Scan(&status)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That plan was not found.")
		}
		if e != nil {
			return e
		}
		if status != "active" {
			return errs.Newf(errs.CodeConflict,
				"That plan is %s, so nothing is due on it.", status)
		}

		// The receipt must belong to this plan's customer, or the money would
		// settle one person's schedule out of another's payment.
		var ok bool
		if e := tx.QueryRow(ctx, `
			SELECT true FROM customer_receipt r
			JOIN installment_plan p ON p.customer_id = r.customer_id
			WHERE r.id = $1 AND p.id = $2`,
			receiptID, planID).Scan(&ok); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeInvalidInput,
					"That receipt belongs to a different customer.")
			}
			return e
		}

		// Oldest first. A customer paying one instalment when two are overdue
		// clears the older one, which is what both parties assume and what
		// keeps the late-fee arithmetic honest.
		rows, e := tx.Query(ctx, `
			SELECT d.id, d.amount, d.waived,
			       coalesce((SELECT sum(p.amount) FROM installment_payment p
			                 WHERE p.due_id = d.id), 0)
			FROM installment_due d
			WHERE d.plan_id = $1
			ORDER BY d.seq
			FOR UPDATE`, planID)
		if e != nil {
			return e
		}
		type due struct {
			id                  uuid.UUID
			amount, waived, pay decimal.Decimal
		}
		var dues []due
		for rows.Next() {
			var d due
			if e := rows.Scan(&d.id, &d.amount, &d.waived, &d.pay); e != nil {
				rows.Close()
				return e
			}
			dues = append(dues, d)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		left := amount
		for _, d := range dues {
			if !left.IsPositive() {
				break
			}
			owing := d.amount.Sub(d.waived).Sub(d.pay)
			if !owing.IsPositive() {
				continue
			}
			take := owing
			if left.LessThan(take) {
				take = left
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO installment_payment
				  (tenant_id, due_id, receipt_id, amount)
				VALUES ($1,$2,$3,$4)`,
				scope.TenantID, d.id, receiptID, take); e != nil {
				return e
			}
			left = left.Sub(take)
		}

		// Money beyond the schedule is a refusal rather than a silent credit:
		// it means somebody typed the wrong figure, and quietly absorbing it
		// would leave the receipt and the schedule disagreeing.
		if left.IsPositive() {
			return errs.Newf(errs.CodeInvalidInput,
				"That is %s more than the plan still owes.", left.StringFixed(2))
		}

		if e := settleIfClear(ctx, tx, planID); e != nil {
			return e
		}

		read, e := s.readPlan(ctx, tx, scope.CompanyID, planID)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// settleIfClear closes a plan whose schedule is fully paid.
func settleIfClear(ctx context.Context, tx pgx.Tx, planID uuid.UUID) error {
	var outstanding decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(d.amount - d.waived
		     - coalesce((SELECT sum(p.amount) FROM installment_payment p
		                 WHERE p.due_id = d.id), 0)), 0)
		FROM installment_due d WHERE d.plan_id = $1`,
		planID).Scan(&outstanding); err != nil {
		return err
	}
	if outstanding.IsPositive() {
		return nil
	}
	_, err := tx.Exec(ctx,
		`UPDATE installment_plan SET status = 'settled' WHERE id = $1`, planID)
	return err
}

// AccrueDue earns the finance income on instalments that have fallen due.
//
// Run from the job queue. The markup was parked in a liability when the plan
// opened; this releases the share belonging to instalments the customer has
// now reached, so the shop reports finance income across the term rather than
// all of it on day one.
func (s *Service) AccrueDue(ctx context.Context, scope Scope) (int, error) {
	var accrued int
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT d.id, p.id, p.markup_amount, p.tenure_months, p.currency,
			       d.due_on
			FROM installment_due d
			JOIN installment_plan p ON p.id = d.plan_id
			WHERE p.company_id = $1 AND p.status = 'active'
			  AND p.markup_amount > 0
			  AND d.due_on <= current_date
			  AND NOT EXISTS (
			    SELECT 1 FROM journal_entry j
			    WHERE j.source_type = 'installment_due'
			      AND j.source_id = d.id
			      AND j.rule_key = 'installment.accrue')
			ORDER BY d.due_on`, scope.CompanyID)
		if e != nil {
			return e
		}
		type row struct {
			dueID, planID uuid.UUID
			markup        decimal.Decimal
			tenure        int
			currency      string
			dueOn         time.Time
		}
		var pending []row
		for rows.Next() {
			var r row
			if e := rows.Scan(&r.dueID, &r.planID, &r.markup, &r.tenure,
				&r.currency, &r.dueOn); e != nil {
				rows.Close()
				return e
			}
			pending = append(pending, r)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}
		if len(pending) == 0 {
			return nil
		}

		var country string
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&country); e != nil {
			return e
		}

		for _, r := range pending {
			share := r.markup.Div(decimal.NewFromInt(int64(r.tenure))).Round(2)
			if !share.IsPositive() {
				continue
			}
			if _, e := accounting.PostByRule(ctx, tx, accounting.Entry{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				Date:       r.dueOn,
				SourceType: "installment_due", SourceID: r.dueID,
				RuleKey:      "installment.accrue",
				Currency:     r.currency,
				BaseCurrency: r.currency,
				FXRate:       decimal.NewFromInt(1),
				Memo:         "Finance income earned",
				PostedBy:     &scope.UserID,
			}, country, accounting.Transaction{
				Amounts: map[string]decimal.Decimal{"amount": share},
			}); e != nil {
				return e
			}
			accrued++
		}
		return nil
	})
	return accrued, db.Translate(err, "")
}

// ApplyLateFees charges the penalty on overdue instalments.
//
// B14 calls the penalty configurable, so a shop that charges nothing has both
// figures at zero and this does nothing. The fee is recorded on the instalment
// rather than posted: it is a charge the shop may still waive, and posting a
// receivable the shop routinely forgives would overstate what it is owed.
func (s *Service) ApplyLateFees(ctx context.Context, scope Scope) (int, error) {
	var charged int
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE installment_due d
			SET late_fee = round(
			      p.late_fee_flat + (d.amount * p.late_fee_rate), 2)
			FROM installment_plan p
			WHERE p.id = d.plan_id
			  AND p.company_id = $1
			  AND p.status = 'active'
			  AND (p.late_fee_flat > 0 OR p.late_fee_rate > 0)
			  AND d.late_fee = 0
			  AND d.due_on + (p.grace_days || ' days')::interval < current_date
			  AND installment_state(d.id) IN ('overdue', 'partial')`,
			scope.CompanyID)
		if e != nil {
			return e
		}
		charged = int(tag.RowsAffected())
		return nil
	})
	return charged, db.Translate(err, "")
}

// Plans lists agreements.
func (s *Service) Plans(
	ctx context.Context, scope Scope, status string,
) ([]Plan, error) {
	out := []Plan{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, planSelect+`
			WHERE p.company_id = $1 AND ($2 = '' OR p.status = $2)
			ORDER BY p.created_at DESC LIMIT 500`, scope.CompanyID, status)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			pl, e := scanPlan(rows)
			if e != nil {
				return e
			}
			out = append(out, pl)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// ReadPlan returns one agreement with its schedule.
func (s *Service) ReadPlan(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Plan, error) {
	var out Plan
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.readPlan(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

const planSelect = `
	SELECT p.id, p.plan_no, p.status, p.customer_id, coalesce(c.name, ''),
	       p.invoice_id, p.principal, p.down_payment, p.financed,
	       p.markup_rate, p.markup_amount, p.tenure_months,
	       p.installment_amount, p.late_fee_flat, p.late_fee_rate,
	       p.grace_days, p.currency, p.starts_on,
	       coalesce(p.guarantor_name, ''), coalesce(p.guarantor_phone, ''),
	       coalesce((SELECT sum(d.amount - d.waived
	           - coalesce((SELECT sum(ip.amount) FROM installment_payment ip
	                       WHERE ip.due_id = d.id), 0))
	         FROM installment_due d WHERE d.plan_id = p.id), 0)
	FROM installment_plan p
	LEFT JOIN customer c ON c.id = p.customer_id`

func (s *Service) readPlan(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Plan, error) {
	row := tx.QueryRow(ctx, planSelect+`
		WHERE p.id = $1 AND p.company_id = $2`, id, companyID)
	out, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, errs.New(errs.CodeNotFound, "That plan was not found.")
	}
	if err != nil {
		return Plan{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT d.id, d.seq, d.due_on, d.amount, d.waived, d.late_fee,
		       coalesce((SELECT sum(p.amount) FROM installment_payment p
		                 WHERE p.due_id = d.id), 0),
		       installment_state(d.id)
		FROM installment_due d
		WHERE d.plan_id = $1 ORDER BY d.seq`, id)
	if err != nil {
		return Plan{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Due
		var on time.Time
		var amount, waived, fee, paid decimal.Decimal
		if e := rows.Scan(&d.ID, &d.Seq, &on, &amount, &waived, &fee,
			&paid, &d.State); e != nil {
			return Plan{}, e
		}
		d.DueOn = on.Format("2006-01-02")
		d.Amount = amount.StringFixed(2)
		d.Waived = waived.StringFixed(2)
		d.LateFee = fee.StringFixed(2)
		d.Paid = paid.StringFixed(2)
		out.Schedule = append(out.Schedule, d)
	}
	return out, rows.Err()
}

func scanPlan(row scanner) (Plan, error) {
	var p Plan
	var principal, down, financed, rate, markup, amount decimal.Decimal
	var feeFlat, feeRate, outstanding decimal.Decimal
	var starts time.Time
	if err := row.Scan(&p.ID, &p.Number, &p.Status, &p.CustomerID, &p.Customer,
		&p.InvoiceID, &principal, &down, &financed, &rate, &markup,
		&p.Tenure, &amount, &feeFlat, &feeRate, &p.GraceDays, &p.Currency,
		&starts, &p.GuarantorName, &p.GuarantorPhone, &outstanding); err != nil {
		return Plan{}, err
	}
	p.Principal = principal.StringFixed(2)
	p.DownPayment = down.StringFixed(2)
	p.Financed = financed.StringFixed(2)
	p.MarkupRate = rate.String()
	p.Markup = markup.StringFixed(2)
	p.Installment = amount.StringFixed(2)
	p.LateFeeFlat = feeFlat.StringFixed(2)
	p.LateFeeRate = feeRate.String()
	p.Outstanding = outstanding.StringFixed(2)
	p.StartsOn = starts.Format("2006-01-02")
	p.Schedule = []Due{}
	return p, nil
}

// Quote previews a schedule without creating anything.
//
// B14's "EMI Plan Generator": a customer at the counter asks what 12 months
// would cost, and the answer must not require committing them to a plan.
type QuotedPlan struct {
	Principal    string `json:"principal"`
	DownPayment  string `json:"down_payment"`
	Financed     string `json:"financed"`
	Markup       string `json:"markup_amount"`
	Total        string `json:"total_payable"`
	Tenure       int    `json:"tenure_months"`
	Installment  string `json:"installment_amount"`
	FinalPayment string `json:"final_payment"`
}

// QuotePlan computes what a plan would look like.
func QuotePlan(
	principal, down, markupRate decimal.Decimal, tenure int,
) (QuotedPlan, error) {
	if tenure <= 0 {
		return QuotedPlan{}, errs.New(errs.CodeInvalidInput,
			"Say how many months it runs for.")
	}
	if down.GreaterThan(principal) {
		return QuotedPlan{}, errs.New(errs.CodeInvalidInput,
			"The down payment is more than the price.")
	}

	financed := principal.Sub(down)
	markup := financed.Mul(markupRate).Round(2)
	total := financed.Add(markup)
	base := total.Div(decimal.NewFromInt(int64(tenure))).RoundDown(2)
	// The last instalment carries the remainder, so the schedule adds back to
	// the total rather than falling short by a few hallalas.
	final := total.Sub(base.Mul(decimal.NewFromInt(int64(tenure - 1))))

	return QuotedPlan{
		Principal:    principal.StringFixed(2),
		DownPayment:  down.StringFixed(2),
		Financed:     financed.StringFixed(2),
		Markup:       markup.StringFixed(2),
		Total:        total.StringFixed(2),
		Tenure:       tenure,
		Installment:  base.StringFixed(2),
		FinalPayment: final.StringFixed(2),
	}, nil
}

// CancelPlan unwinds an agreement, e.g. when the goods came back.
func (s *Service) CancelPlan(
	ctx context.Context, scope Scope, id uuid.UUID, reason string,
) error {
	if strings.TrimSpace(reason) == "" {
		return errs.Validation("Say why the plan is being cancelled.").
			WithField("reason", "It is a change to what a customer owes.")
	}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE installment_plan
			SET status = 'cancelled',
			    verification_note = coalesce(verification_note || ' | ', '')
			      || 'Cancelled: ' || $3
			WHERE id = $1 AND company_id = $2 AND status = 'active'`,
			id, scope.CompanyID, strings.TrimSpace(reason))
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeConflict,
				"That plan is not active, so it cannot be cancelled.")
		}
		return nil
	})
	return db.Translate(err, "")
}
