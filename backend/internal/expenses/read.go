package expenses

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Filter narrows a list of expenses.
//
// Blueprint C3.1 asks for "one-click, customizable 'where is my money going'
// — filterable by day/week/month/year/custom range, by category, by store".
// The range is a pair of dates rather than a named period, because a named one
// would have to agree with whatever the caller means by "this month" and the
// caller already knows.
type Filter struct {
	From, To time.Time
	HeadID   *uuid.UUID
	StoreID  *uuid.UUID
}

// Summary is what a period cost, broken down the way the Owner asked to see it.
type Summary struct {
	From string `json:"from"`
	To   string `json:"to"`

	Total          string `json:"total"`
	TaxRecoverable string `json:"tax_recoverable"`
	// TaxAbsorbed is VAT the shop paid and cannot reclaim. On its own line
	// because it is a real cost that looks like tax, and an owner comparing
	// their expense total to their invoices needs to see where it came from.
	TaxAbsorbed string `json:"tax_absorbed"`

	ByHead  []HeadSpend `json:"by_head"`
	Count   int         `json:"count"`
	Expense []Expense   `json:"expenses"`
}

// HeadSpend is one category's share of the period.
type HeadSpend struct {
	HeadID uuid.UUID `json:"head_id"`
	Head   string    `json:"head"`
	Amount string    `json:"amount"`
	// Share is the percentage of the period this category accounts for, so the
	// answer to "where is my money going" does not need arithmetic to read.
	Share string `json:"share"`
}

// Between lists expenses in a period, with the breakdown by category.
func (s *Service) Between(
	ctx context.Context, scope Scope, f Filter,
) (Summary, error) {
	out := Summary{
		From: f.From.Format("2006-01-02"), To: f.To.Format("2006-01-02"),
		ByHead: []HeadSpend{}, Expense: []Expense{},
	}
	if f.To.Before(f.From) {
		return Summary{}, errs.New(errs.CodeInvalidInput,
			"The end of the period is before its start.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT x.id, x.expense_no, x.expense_date::text,
			       coalesce(x.reference, ''), coalesce(x.description, ''),
			       x.paid_from, coalesce(st.name, ''), coalesce(sup.legal_name, ''),
			       x.currency, x.subtotal_net, x.tax_total, x.tax_recoverable,
			       x.tax_absorbed, x.total_inclusive
			FROM expense x
			LEFT JOIN store st     ON st.id = x.store_id
			LEFT JOIN supplier sup ON sup.id = x.supplier_id
			WHERE x.company_id = $1
			  AND x.expense_date BETWEEN $2::date AND $3::date
			  AND ($4::uuid IS NULL OR x.store_id = $4::uuid)
			  AND ($5::uuid IS NULL OR EXISTS (
			        SELECT 1 FROM expense_line l
			        WHERE l.expense_id = x.id AND l.head_id = $5::uuid))
			ORDER BY x.expense_date DESC, x.expense_no DESC`,
			scope.CompanyID, f.From, f.To, f.StoreID, f.HeadID)
		if e != nil {
			return e
		}
		defer rows.Close()

		total := decimal.Zero
		recoverable, absorbed := decimal.Zero, decimal.Zero
		for rows.Next() {
			var x Expense
			var net, tax, rec, abs, gross decimal.Decimal
			if e := rows.Scan(&x.ID, &x.ExpenseNo, &x.Date, &x.Reference,
				&x.Description, &x.PaidFrom, &x.Store, &x.Supplier, &x.Currency,
				&net, &tax, &rec, &abs, &gross); e != nil {
				return e
			}
			x.SubtotalNet = net.StringFixed(moneyScale)
			x.TaxTotal = tax.StringFixed(moneyScale)
			x.TaxRecoverable = rec.StringFixed(moneyScale)
			x.TaxAbsorbed = abs.StringFixed(moneyScale)
			x.Total = gross.StringFixed(moneyScale)
			out.Expense = append(out.Expense, x)

			total = total.Add(gross)
			recoverable = recoverable.Add(rec)
			absorbed = absorbed.Add(abs)
		}
		if e := rows.Err(); e != nil {
			return e
		}

		out.Count = len(out.Expense)
		out.Total = total.StringFixed(moneyScale)
		out.TaxRecoverable = recoverable.StringFixed(moneyScale)
		out.TaxAbsorbed = absorbed.StringFixed(moneyScale)

		// The breakdown, summed from the LINES rather than from the headers:
		// one receipt can cover three categories, and an owner asking where
		// the money went wants it split the way it was actually spent.
		//
		// charge_amount, not net: it is what the expense account was debited,
		// so the shares add up to what the P&L shows rather than to a figure
		// that is short by whatever VAT could not be reclaimed.
		byHead, e := tx.Query(ctx, `
			SELECT h.id, h.name, sum(l.charge_amount)
			FROM expense_line l
			JOIN expense x      ON x.id = l.expense_id
			JOIN expense_head h ON h.id = l.head_id
			WHERE x.company_id = $1
			  AND x.expense_date BETWEEN $2::date AND $3::date
			  AND ($4::uuid IS NULL OR x.store_id = $4::uuid)
			  AND ($5::uuid IS NULL OR l.head_id = $5::uuid)
			GROUP BY h.id, h.name
			ORDER BY sum(l.charge_amount) DESC`,
			scope.CompanyID, f.From, f.To, f.StoreID, f.HeadID)
		if e != nil {
			return e
		}
		defer byHead.Close()

		spends := []HeadSpend{}
		spent := decimal.Zero
		for byHead.Next() {
			var h HeadSpend
			var amount decimal.Decimal
			if e := byHead.Scan(&h.HeadID, &h.Head, &amount); e != nil {
				return e
			}
			h.Amount = amount.StringFixed(moneyScale)
			spends = append(spends, h)
			spent = spent.Add(amount)
		}
		if e := byHead.Err(); e != nil {
			return e
		}

		// The share is against what the CATEGORIES came to, not against the
		// gross. The gross includes recoverable VAT, which is not a cost and
		// belongs to no category, so dividing by it would make every share
		// read low and the column would not reach 100.
		for i := range spends {
			if spent.IsPositive() {
				amount := decimal.RequireFromString(spends[i].Amount)
				spends[i].Share = amount.Mul(decimal.NewFromInt(100)).
					Div(spent).Round(1).String()
			} else {
				spends[i].Share = "0"
			}
		}
		out.ByHead = spends
		return nil
	})
	if err != nil {
		return Summary{}, db.Translate(err, "")
	}
	return out, nil
}

// Read returns one expense with its lines.
func (s *Service) Read(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Expense, error) {
	var out Expense
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		x, e := s.read(ctx, tx, scope, id)
		if e != nil {
			return e
		}
		out = x
		return nil
	})
	return out, err
}

func (s *Service) alreadyRecorded(
	ctx context.Context, tx pgx.Tx, scope Scope, docUUID uuid.UUID,
) (Expense, bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM expense WHERE company_id = $1 AND uuid = $2`,
		scope.CompanyID, docUUID).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Expense{}, false, nil
		}
		return Expense{}, false, err
	}
	x, err := s.read(ctx, tx, scope, id)
	if err != nil {
		return Expense{}, false, err
	}
	return x, true, nil
}

func (s *Service) read(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Expense, error) {
	var x Expense
	var net, tax, rec, abs, gross decimal.Decimal

	err := tx.QueryRow(ctx, `
		SELECT x.id, x.expense_no, x.expense_date::text,
		       coalesce(x.reference, ''), coalesce(x.description, ''),
		       x.paid_from, coalesce(st.name, ''), coalesce(sup.legal_name, ''),
		       x.currency, x.subtotal_net, x.tax_total, x.tax_recoverable,
		       x.tax_absorbed, x.total_inclusive
		FROM expense x
		LEFT JOIN store st     ON st.id = x.store_id
		LEFT JOIN supplier sup ON sup.id = x.supplier_id
		WHERE x.id = $1 AND x.company_id = $2`, id, scope.CompanyID).
		Scan(&x.ID, &x.ExpenseNo, &x.Date, &x.Reference, &x.Description,
			&x.PaidFrom, &x.Store, &x.Supplier, &x.Currency,
			&net, &tax, &rec, &abs, &gross)

	if err == pgx.ErrNoRows {
		return Expense{}, errs.New(errs.CodeNotFound, "That expense was not found.")
	}
	if err != nil {
		return Expense{}, err
	}

	x.SubtotalNet = net.StringFixed(moneyScale)
	x.TaxTotal = tax.StringFixed(moneyScale)
	x.TaxRecoverable = rec.StringFixed(moneyScale)
	x.TaxAbsorbed = abs.StringFixed(moneyScale)
	x.Total = gross.StringFixed(moneyScale)
	x.Lines = []Line{}

	rows, err := tx.Query(ctx, `
		SELECT l.head_id, h.name, coalesce(l.description, ''),
		       l.net_amount, l.tax_treatment, l.tax_rate, l.tax_amount,
		       l.tax_recoverable, l.tax_absorbed, l.charge_amount
		FROM expense_line l
		JOIN expense_head h ON h.id = l.head_id
		WHERE l.expense_id = $1
		ORDER BY l.line_no`, id)
	if err != nil {
		return Expense{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var l Line
		var lineNet, rate, lineTax, lineRec, lineAbs, charge decimal.Decimal
		if e := rows.Scan(&l.HeadID, &l.Head, &l.Description,
			&lineNet, &l.TaxTreatment, &rate, &lineTax,
			&lineRec, &lineAbs, &charge); e != nil {
			return Expense{}, e
		}
		l.Net = lineNet.StringFixed(moneyScale)
		l.TaxRate = rate.String()
		l.Tax = lineTax.StringFixed(moneyScale)
		l.TaxRecoverable = lineRec.StringFixed(moneyScale)
		l.TaxAbsorbed = lineAbs.StringFixed(moneyScale)
		l.Charge = charge.StringFixed(moneyScale)
		x.Lines = append(x.Lines, l)
	}
	return x, rows.Err()
}
