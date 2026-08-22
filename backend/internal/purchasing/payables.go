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

// Reading bills, approving blocked ones, and paying suppliers.

func (s *Service) readBill(
	ctx context.Context, tx pgx.Tx, billID uuid.UUID,
) (Bill, error) {
	var b Bill
	var poID *uuid.UUID
	var poNumber *string

	if err := tx.QueryRow(ctx, `
		SELECT b.id, b.supplier_id, s.legal_name, b.supplier_ref, b.po_id,
		       p.po_number, b.bill_date::text, b.due_date::text, b.currency,
		       b.subtotal_net::text, b.tax_total::text, b.total_inclusive::text,
		       b.amount_paid::text,
		       (b.total_inclusive - b.amount_paid)::text,
		       b.status, b.journal_entry_id IS NOT NULL
		FROM purchase_bill b
		JOIN supplier s ON s.id = b.supplier_id
		LEFT JOIN purchase_order p ON p.id = b.po_id
		WHERE b.id = $1`, billID,
	).Scan(&b.ID, &b.SupplierID, &b.Supplier, &b.SupplierRef, &poID, &poNumber,
		&b.BillDate, &b.DueDate, &b.Currency, &b.SubtotalNet, &b.TaxTotal,
		&b.Total, &b.AmountPaid, &b.Outstanding, &b.Status, &b.Posted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Bill{}, errs.New(errs.CodeNotFound, "That bill was not found.")
		}
		return Bill{}, err
	}

	b.POID = poID
	if poNumber != nil {
		b.PONumber = *poNumber
	}
	return b, nil
}

func (s *Service) alreadyBilled(
	ctx context.Context, tx pgx.Tx, scope Scope, docUUID uuid.UUID,
) (Bill, bool, error) {
	var billID uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM purchase_bill WHERE tenant_id = $1 AND uuid = $2`,
		scope.TenantID, docUUID).Scan(&billID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Bill{}, false, nil
	}
	if err != nil {
		return Bill{}, false, err
	}

	bill, err := s.readBill(ctx, tx, billID)
	if err != nil {
		return Bill{}, false, err
	}
	match, err := readMatch(ctx, tx, billID)
	if err != nil {
		return Bill{}, false, err
	}
	bill.Match = match
	return bill, true, nil
}

func readMatch(
	ctx context.Context, tx pgx.Tx, billID uuid.UUID,
) ([]MatchLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT dimension, coalesce(ordered::text,''), coalesce(received::text,''),
		       coalesce(billed::text,''), variance::text,
		       coalesce(variance_pct::text,''), outcome, coalesce(detail,'')
		FROM three_way_match WHERE bill_id = $1
		ORDER BY checked_at, dimension`, billID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []MatchLine{}
	for rows.Next() {
		var m MatchLine
		if err := rows.Scan(&m.Dimension, &m.Ordered, &m.Received, &m.Billed,
			&m.Variance, &m.VariancePct, &m.Outcome, &m.Detail); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReadBill returns one bill with the evidence of its match.
func (s *Service) ReadBill(
	ctx context.Context, scope Scope, billID uuid.UUID,
) (Bill, error) {
	var out Bill
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var companyID uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT company_id FROM purchase_bill WHERE id = $1`, billID).
			Scan(&companyID); errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That bill was not found.")
		} else if e != nil {
			return e
		}
		if companyID != scope.CompanyID {
			return errs.New(errs.CodeNotFound, "That bill was not found.")
		}

		bill, e := s.readBill(ctx, tx, billID)
		if e != nil {
			return e
		}
		match, e := readMatch(ctx, tx, billID)
		if e != nil {
			return e
		}
		bill.Match = match
		out = bill
		return nil
	})
	return out, err
}

// ListBills returns the payables ledger.
func (s *Service) ListBills(
	ctx context.Context, scope Scope, status string, limit int,
) ([]Bill, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	out := []Bill{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `
			SELECT b.id, b.supplier_id, s.legal_name, b.supplier_ref,
			       coalesce(p.po_number,''), b.bill_date::text, b.due_date::text,
			       b.currency, b.subtotal_net::text, b.tax_total::text,
			       b.total_inclusive::text, b.amount_paid::text,
			       (b.total_inclusive - b.amount_paid)::text, b.status,
			       b.journal_entry_id IS NOT NULL
			FROM purchase_bill b
			JOIN supplier s ON s.id = b.supplier_id
			LEFT JOIN purchase_order p ON p.id = b.po_id
			WHERE b.company_id = $1 AND ($2 = '' OR b.status = $2)
			ORDER BY b.due_date, b.bill_date DESC
			LIMIT $3`, scope.CompanyID, status, limit)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var b Bill
			if e := rows.Scan(&b.ID, &b.SupplierID, &b.Supplier, &b.SupplierRef,
				&b.PONumber, &b.BillDate, &b.DueDate, &b.Currency,
				&b.SubtotalNet, &b.TaxTotal, &b.Total, &b.AmountPaid,
				&b.Outstanding, &b.Status, &b.Posted); e != nil {
				return e
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, err
}

// ApproveBill accepts a discrepancy the match blocked, and posts the bill.
//
// The reason is required, and so is the name. B5.2's control is worthless if an
// override leaves nothing behind: the whole value of blocking a bill is that
// somebody has to put their name to letting it through, and a database
// constraint enforces that the two arrive together.
func (s *Service) ApproveBill(
	ctx context.Context, scope Scope, billID uuid.UUID, reason string,
) (Bill, error) {
	if trim(reason) == "" {
		return Bill{}, errs.New(errs.CodeInvalidInput,
			"Say why this discrepancy is being accepted. It is recorded against your name.")
	}

	var out Bill
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status, supplier string
		var billDate time.Time
		var net, tax, total decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT b.status, s.legal_name, b.bill_date,
			       b.subtotal_net, b.tax_total, b.total_inclusive
			FROM purchase_bill b
			JOIN supplier s ON s.id = b.supplier_id
			WHERE b.id = $1 AND b.company_id = $2`,
			billID, scope.CompanyID,
		).Scan(&status, &supplier, &billDate, &net, &tax, &total); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That bill was not found.")
			}
			return e
		}

		if status != "blocked" {
			return errs.Newf(errs.CodeConflict,
				"That bill is %s, so there is nothing to approve.", status)
		}

		// Posted only now. A blocked bill was deliberately kept out of the
		// ledger, so approving it is what creates the liability.
		if e := s.postBill(ctx, tx, scope, billID, billDate,
			net, tax, total, supplier); e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			UPDATE purchase_bill
			SET status = 'approved', approved_by = $2, approved_at = now(),
			    approval_reason = $3
			WHERE id = $1`, billID, scope.UserID, reason); e != nil {
			return e
		}

		bill, e := s.readBill(ctx, tx, billID)
		if e != nil {
			return e
		}
		match, e := readMatch(ctx, tx, billID)
		if e != nil {
			return e
		}
		bill.Match = match
		out = bill
		return nil
	})
	return out, err
}

// --- Paying --------------------------------------------------------------

type Allocation struct {
	BillID uuid.UUID
	Amount decimal.Decimal
}

type NewPayment struct {
	UUID        uuid.UUID
	SupplierID  uuid.UUID
	Method      string
	Reference   string
	PaidOn      time.Time
	Allocations []Allocation
}

type Payment struct {
	ID            uuid.UUID     `json:"id"`
	PaymentNumber string        `json:"payment_number"`
	SupplierID    uuid.UUID     `json:"supplier_id"`
	Supplier      string        `json:"supplier"`
	PaidOn        string        `json:"paid_on"`
	Method        string        `json:"method"`
	Reference     string        `json:"reference,omitempty"`
	Amount        string        `json:"amount"`
	Currency      string        `json:"currency"`
	Settled       []SettledBill `json:"settled"`
	AlreadyPaid   bool          `json:"already_paid"`

	// ReversesID names the payment this one undoes, when it undoes one. A
	// screen shows a reversal differently from a payment, and a payment that
	// has been reversed differently again — see the supplier_payment_effect
	// view in migration 0050.
	ReversesID *uuid.UUID `json:"reverses_id,omitempty"`
	// Reversed is true once something else has undone this payment, so a
	// screen can strike it through rather than offer to reverse it twice.
	Reversed bool `json:"reversed,omitempty"`
}

type SettledBill struct {
	BillID      uuid.UUID `json:"bill_id"`
	SupplierRef string    `json:"supplier_ref"`
	Amount      string    `json:"amount"`
	Outstanding string    `json:"outstanding"`
	Status      string    `json:"status"`
}

// PaySupplier settles one or more bills, atomically.
//
// The allocation is explicit rather than "oldest first". A shop paying a
// supplier is usually paying specific invoices they have agreed, and guessing
// which ones would produce a remittance the supplier disputes — which is the
// thing that turns a payment into a week of emails.
func (s *Service) PaySupplier(
	ctx context.Context, scope Scope, in NewPayment,
) (Payment, error) {
	if len(in.Allocations) == 0 {
		return Payment{}, errs.New(errs.CodeInvalidInput,
			"Say which bills this payment settles.")
	}
	if in.UUID == uuid.Nil {
		return Payment{}, errs.New(errs.CodeInvalidInput,
			"A payment must carry an identifier so a retry does not pay twice.")
	}
	if trim(in.Method) == "" {
		return Payment{}, errs.New(errs.CodeInvalidInput,
			"Say how the supplier was paid.")
	}

	var out Payment
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if existing, found, e := s.alreadyPaid(ctx, tx, scope, in.UUID); e != nil {
			return e
		} else if found {
			out = existing
			out.AlreadyPaid = true
			return nil
		}

		var currency, country, supplier string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency, country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency, &country); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT legal_name FROM supplier WHERE id = $1 AND company_id = $2`,
			in.SupplierID, scope.CompanyID).Scan(&supplier); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That supplier was not found.")
			}
			return e
		}

		paidOn := in.PaidOn
		if paidOn.IsZero() {
			paidOn = time.Now().UTC()
		}

		total := decimal.Zero
		for _, a := range in.Allocations {
			if !a.Amount.IsPositive() {
				return errs.New(errs.CodeInvalidInput,
					"A payment of nothing is not a payment.")
			}
			total = total.Add(a.Amount)
		}

		number, e := claimNumber(ctx, tx, scope.CompanyID, "payment", "PAY")
		if e != nil {
			return e
		}

		var paymentID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO supplier_payment
			  (tenant_id, company_id, supplier_id, payment_number, uuid,
			   paid_on, method, reference, amount, currency, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.SupplierID, number, in.UUID,
			paidOn, in.Method, nullText(in.Reference), total, currency,
			scope.UserID).Scan(&paymentID); e != nil {
			return db.Translate(e, "That payment has already been recorded.")
		}

		out = Payment{
			ID: paymentID, PaymentNumber: number, SupplierID: in.SupplierID,
			Supplier: supplier, PaidOn: paidOn.Format("2006-01-02"),
			Method: in.Method, Reference: in.Reference,
			Amount: total.StringFixed(2), Currency: currency,
			Settled: []SettledBill{},
		}

		for _, a := range in.Allocations {
			// Locked before it is read. Two payments allocated to the same
			// bill at the same moment would otherwise both see the old
			// amount_paid and between them overpay it.
			var outstanding decimal.Decimal
			var ref, status string
			if e := tx.QueryRow(ctx, `
				SELECT total_inclusive - amount_paid, supplier_ref, status
				FROM purchase_bill
				WHERE id = $1 AND company_id = $2 AND supplier_id = $3
				FOR UPDATE`,
				a.BillID, scope.CompanyID, in.SupplierID).
				Scan(&outstanding, &ref, &status); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return errs.New(errs.CodeInvalidInput,
						"One of those bills does not belong to this supplier.")
				}
				return e
			}

			// A blocked bill is blocked FROM PAYMENT. That is the entire point
			// of the control, and letting a payment through would make the
			// three-way match decorative.
			if status == "blocked" {
				return errs.Newf(errs.CodeConflict,
					"Bill %s is held by the three-way match and cannot be paid "+
						"until the discrepancy is approved.", ref)
			}
			if status == "cancelled" || status == "draft" {
				return errs.Newf(errs.CodeConflict,
					"Bill %s is %s and cannot be paid.", ref, status)
			}
			if a.Amount.GreaterThan(outstanding) {
				return errs.Newf(errs.CodeInvalidInput,
					"Bill %s has %s outstanding, less than the %s allocated to it.",
					ref, outstanding.StringFixed(2), a.Amount.StringFixed(2))
			}

			// `status` is captured as it was BEFORE this allocation touched
			// the bill, so a reversal can put back exactly what it found.
			// Guessing between 'matched' and 'approved' would invent a rule
			// and quietly rewrite whether the three-way match agreed — see
			// migration 0050.
			if _, e := tx.Exec(ctx, `
				INSERT INTO supplier_payment_allocation
				  (tenant_id, payment_id, bill_id, amount, bill_status_before)
				VALUES ($1,$2,$3,$4,$5)`,
				scope.TenantID, paymentID, a.BillID, a.Amount, status); e != nil {
				return db.Translate(e,
					"That bill is allocated twice on the same payment.")
			}

			var newStatus string
			if e := tx.QueryRow(ctx, `
				UPDATE purchase_bill
				SET amount_paid = amount_paid + $2,
				    status = CASE WHEN amount_paid + $2 >= total_inclusive
				                  THEN 'paid' ELSE status END
				WHERE id = $1
				RETURNING status`, a.BillID, a.Amount).Scan(&newStatus); e != nil {
				return e
			}

			out.Settled = append(out.Settled, SettledBill{
				BillID: a.BillID, SupplierRef: ref,
				Amount:      a.Amount.StringFixed(2),
				Outstanding: outstanding.Sub(a.Amount).StringFixed(2),
				Status:      newStatus,
			})
		}

		// Rule 7, unchanged: debit accounts payable, credit whatever the money
		// left through.
		result, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date: paidOn, SourceType: "supplier_payment", SourceID: paymentID,
			PostedBy: &scope.UserID, RuleKey: "payment.supplier",
			Memo: "Paid " + supplier,
		}, country, accounting.Transaction{
			Amounts: map[string]decimal.Decimal{"amount": total},
			Groups: map[string]accounting.Group{
				"payments": {{
					Role: tenderRole(in.Method), Amount: total, Memo: in.Method,
				}},
			},
		})
		if e != nil {
			return e
		}

		_, e = tx.Exec(ctx,
			`UPDATE supplier_payment SET journal_entry_id = $2 WHERE id = $1`,
			paymentID, result.EntryID)
		return e
	})
	return out, err
}

// tenderRole maps how the supplier was paid onto the account it left.
//
// Deliberately NOT shared with the sale side's version. A shop paying a
// supplier by card is using its own bank card, which clears through the bank
// rather than through an acquirer's settlement account — the same word means a
// different account depending on which direction the money is going.
func tenderRole(method string) string {
	switch method {
	case "cash":
		return "cash"
	default:
		return "bank"
	}
}

func (s *Service) alreadyPaid(
	ctx context.Context, tx pgx.Tx, scope Scope, docUUID uuid.UUID,
) (Payment, bool, error) {
	var p Payment
	err := tx.QueryRow(ctx, `
		SELECT sp.id, sp.payment_number, sp.supplier_id, s.legal_name,
		       sp.paid_on::text, sp.method, coalesce(sp.reference,''),
		       sp.amount::text, sp.currency, sp.reverses_id,
		       EXISTS (SELECT 1 FROM supplier_payment r WHERE r.reverses_id = sp.id)
		FROM supplier_payment sp
		JOIN supplier s ON s.id = sp.supplier_id
		WHERE sp.tenant_id = $1 AND sp.uuid = $2`,
		scope.TenantID, docUUID,
	).Scan(&p.ID, &p.PaymentNumber, &p.SupplierID, &p.Supplier, &p.PaidOn,
		&p.Method, &p.Reference, &p.Amount, &p.Currency, &p.ReversesID,
		&p.Reversed)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, false, nil
	}
	if err != nil {
		return Payment{}, false, err
	}

	rows, err := tx.Query(ctx, `
		SELECT a.bill_id, b.supplier_ref, a.amount::text,
		       (b.total_inclusive - b.amount_paid)::text, b.status
		FROM supplier_payment_allocation a
		JOIN purchase_bill b ON b.id = a.bill_id
		WHERE a.payment_id = $1`, p.ID)
	if err != nil {
		return Payment{}, false, err
	}
	defer rows.Close()

	p.Settled = []SettledBill{}
	for rows.Next() {
		var sb SettledBill
		if err := rows.Scan(&sb.BillID, &sb.SupplierRef, &sb.Amount,
			&sb.Outstanding, &sb.Status); err != nil {
			return Payment{}, false, err
		}
		p.Settled = append(p.Settled, sb)
	}
	return p, true, rows.Err()
}

// --- Ageing --------------------------------------------------------------

// AgeingRow is what one supplier is owed, by how overdue it is.
type AgeingRow struct {
	SupplierID uuid.UUID `json:"supplier_id"`
	Supplier   string    `json:"supplier"`
	NotDue     string    `json:"not_due"`
	Days0To30  string    `json:"days_0_30"`
	Days31To60 string    `json:"days_31_60"`
	Days61To90 string    `json:"days_61_90"`
	Days90Plus string    `json:"days_90_plus"`
	Total      string    `json:"total"`
}

type Ageing struct {
	AsOf         string      `json:"as_of"`
	Rows         []AgeingRow `json:"rows"`
	Total        string      `json:"total"`
	BaseCurrency string      `json:"base_currency"`
}

// AgeingAt reports what is owed to whom, and for how long.
//
// Measured from the DUE date rather than the bill date, per B6. A 60-day-terms
// invoice raised 45 days ago is not overdue, and ageing it from issue would
// say it was — which would have a buyer chasing a supplier who is owed nothing
// yet.
func (s *Service) AgeingAt(
	ctx context.Context, scope Scope, asOf time.Time,
) (Ageing, error) {
	out := Ageing{AsOf: asOf.Format("2006-01-02"), Rows: []AgeingRow{}}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&out.BaseCurrency); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT supplier_id, supplier_name, not_due::text, days_0_30::text,
			       days_31_60::text, days_61_90::text, days_90_plus::text,
			       total::text
			FROM supplier_ageing($1, $2)`, scope.CompanyID, asOf)
		if e != nil {
			return e
		}
		defer rows.Close()

		total := decimal.Zero
		for rows.Next() {
			var r AgeingRow
			if e := rows.Scan(&r.SupplierID, &r.Supplier, &r.NotDue,
				&r.Days0To30, &r.Days31To60, &r.Days61To90, &r.Days90Plus,
				&r.Total); e != nil {
				return e
			}
			total = total.Add(decimal.RequireFromString(r.Total))
			out.Rows = append(out.Rows, r)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		out.Total = total.StringFixed(2)
		return nil
	})
	return out, err
}

// GLDifference is C9.3's hard invariant for payables as a number: what
// suppliers are owed, less the Accounts Payable control account balance, which
// must be zero.
//
// Deliberately the same shape and the same name as inventory.GLDifference and
// receivables.GLDifference, so the nightly tie-out, the acceptance test and a
// support engineer looking at a live tenant all ask one question and get one
// answer — and so a third invariant does not arrive with a third way of asking
// about it.
func GLDifference(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (decimal.Decimal, error) {
	var d decimal.Decimal
	err := tx.QueryRow(ctx,
		`SELECT payable_gl_difference($1)`, companyID).Scan(&d)
	return d, err
}
