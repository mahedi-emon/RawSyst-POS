package reports

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// What is behind each figure on the dashboard.
//
// Blueprint A8 requires one-click drill-through on every widget, and the reason
// is not convenience: a KPI you cannot open is trivia. An owner who sees a
// number they did not expect has exactly one useful next question — which
// transactions made it — and a dashboard that cannot answer it sends them to a
// spreadsheet, which is where trust in the software ends.
//
// # These are the same figures, decomposed
//
// Each list below sums to the tile it sits behind. That is a property worth
// stating because it is easy to lose: a detail screen filtered slightly
// differently from its summary is worse than no detail screen, since it makes
// an owner believe the summary is wrong.
//
// # Permissions are per surface, not per dashboard
//
// The dashboard needs accounting.view. Its detail screens need the permission
// covering the records they show — sales.view for invoices, inventory.view for
// stock — because a role holding one and not the other is an ordinary
// arrangement, and the route is the only place that can be trusted to enforce
// it.

// --- Sales detail --------------------------------------------------------

// SaleRow is one invoice as the sales list shows it.
type SaleRow struct {
	ID          uuid.UUID `json:"id"`
	HumanNumber string    `json:"human_number,omitempty"`
	DocType     string    `json:"doc_type"`
	// State is the ZATCA lifecycle position, shown because an owner scanning
	// the day's takings should be able to see at a glance that everything
	// reported — and which one did not.
	State string `json:"state"`
	// IssuedAt carries the time of day. The date is already the page's subject;
	// what distinguishes one row from the next is when it happened.
	IssuedAt       string `json:"issued_at"`
	TotalInclusive string `json:"total_inclusive"`
	TaxTotal       string `json:"tax_total"`
	// Tenders is a readable summary — "Cash", or "Cash + Mada" for a split.
	// The full breakdown lives on the invoice itself.
	Tenders   string `json:"tenders"`
	LineCount int    `json:"line_count"`
	StoreName string `json:"store_name,omitempty"`
	// Voided marks a credit note. Kept in the list rather than filtered out:
	// a day where half the sales were reversed is a fact an owner needs, and
	// hiding the reversals would make the list disagree with the books.
	IsCreditNote bool `json:"is_credit_note"`
}

// SalesDetail is the day's invoices with the totals they add up to.
type SalesDetail struct {
	Date string    `json:"date"`
	Rows []SaleRow `json:"rows"`
	// Totals are computed over the WHOLE day, not over the returned page, so a
	// paged list still tells an owner what the day came to.
	SalesTotal   string `json:"sales_total"`
	RefundTotal  string `json:"refund_total"`
	NetTotal     string `json:"net_total"`
	TaxTotal     string `json:"tax_total"`
	InvoiceCount int    `json:"invoice_count"`
	RefundCount  int    `json:"refund_count"`
	HasMore      bool   `json:"has_more"`
	BaseCurrency string `json:"base_currency"`
}

// SalesFor lists a day's invoices, newest first.
//
// Newest first because an owner opening this mid-afternoon is usually looking
// for something that just happened — a sale they were told about, a refund they
// authorised — rather than reading the day from the beginning.
func (s *Service) SalesFor(
	ctx context.Context, scope Scope, day time.Time, limit int,
) (SalesDetail, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	from := startOfDay(day)
	to := from.AddDate(0, 0, 1)

	out := SalesDetail{Date: day.Format("2006-01-02"), Rows: []SaleRow{}}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := readCurrency(ctx, tx, scope.CompanyID, &out.BaseCurrency); e != nil {
			return e
		}

		// The day's totals first, over every row rather than the page. Sales
		// and refunds are kept apart: netting them into one figure hides a day
		// where a lot was sold and a lot came back, which is exactly the day an
		// owner needs to see.
		if e := tx.QueryRow(ctx, `
			SELECT
			  coalesce(sum(total_inclusive) FILTER (WHERE doc_type <> 'credit_note'), 0)::text,
			  coalesce(sum(total_inclusive) FILTER (WHERE doc_type =  'credit_note'), 0)::text,
			  coalesce(sum(tax_total)       FILTER (WHERE doc_type <> 'credit_note'), 0)::text,
			  count(*) FILTER (WHERE doc_type <> 'credit_note'),
			  count(*) FILTER (WHERE doc_type =  'credit_note')
			FROM sales_invoice
			WHERE company_id = $1 AND issue_date >= $2 AND issue_date < $3
			  AND ($4::uuid IS NULL OR store_id = $4::uuid)`,
			scope.CompanyID, from, to, scope.StoreID,
		).Scan(&out.SalesTotal, &out.RefundTotal, &out.TaxTotal,
			&out.InvoiceCount, &out.RefundCount); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT i.id, coalesce(i.human_number, ''), i.doc_type, i.state,
			       to_char(i.issued_at, 'HH24:MI'),
			       i.total_inclusive::text, i.tax_total::text,
			       coalesce((
			         SELECT string_agg(DISTINCT initcap(replace(t.method, '_', ' ')), ' + ')
			         FROM sales_tender t
			         WHERE t.invoice_id = i.id AND t.method <> 'exchange_clearing'
			       ), ''),
			       (SELECT count(*) FROM sales_invoice_line l WHERE l.invoice_id = i.id),
			       coalesce(st.name, '')
			FROM sales_invoice i
			LEFT JOIN store st ON st.id = i.store_id
			WHERE i.company_id = $1 AND i.issue_date >= $2 AND i.issue_date < $3
			  AND ($4::uuid IS NULL OR i.store_id = $4::uuid)
			ORDER BY i.issued_at DESC, i.id DESC
			LIMIT $5`,
			scope.CompanyID, from, to, scope.StoreID, limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var r SaleRow
			if e := rows.Scan(&r.ID, &r.HumanNumber, &r.DocType, &r.State,
				&r.IssuedAt, &r.TotalInclusive, &r.TaxTotal, &r.Tenders,
				&r.LineCount, &r.StoreName); e != nil {
				return e
			}
			r.IsCreditNote = r.DocType == "credit_note"
			out.Rows = append(out.Rows, r)
		}
		return rows.Err()
	})
	if err != nil {
		return SalesDetail{}, err
	}

	// One extra was fetched to answer "is there more" without a second count.
	if len(out.Rows) > limit {
		out.Rows = out.Rows[:limit]
		out.HasMore = true
	}

	net := dec(out.SalesTotal).Sub(dec(out.RefundTotal))
	out.NetTotal = net.StringFixed(2)
	return out, nil
}

// --- Expenses by account -------------------------------------------------

// ExpenseEntry is one posting behind an expense account.
type ExpenseEntry struct {
	EntryID   uuid.UUID `json:"entry_id"`
	EntryNo   string    `json:"entry_no"`
	Date      string    `json:"date"`
	Memo      string    `json:"memo"`
	AccountID uuid.UUID `json:"account_id"`
	Account   string    `json:"account"`
	// The same account in Arabic, empty when nobody has written one. Both are
	// sent and the screen picks; see StatementLine for why.
	AccountAr string `json:"account_ar,omitempty"`
	Code      string `json:"code"`
	Amount    string `json:"amount"`
	// SourceType names what caused the posting — a sale, a return, an
	// adjustment. An owner asking "what is this expense" is usually asking
	// what created it, not which account it landed in.
	SourceType string `json:"source_type,omitempty"`
}

type ExpensesDetail struct {
	Date         string          `json:"date"`
	Entries      []ExpenseEntry  `json:"entries"`
	ByAccount    []StatementLine `json:"by_account"`
	Total        string          `json:"total"`
	BaseCurrency string          `json:"base_currency"`
	// AccountID echoes the filter so the screen can show what it narrowed to
	// without having to remember what it asked for.
	AccountID string `json:"account_id,omitempty"`
}

// ExpensesFor lists the postings behind a day's expenses.
//
// Optionally narrowed to one account, which is the drill-through the dashboard
// tile offers: an owner clicks a line in the summary and expects the entries
// behind that line, not the whole day again.
func (s *Service) ExpensesFor(
	ctx context.Context, scope Scope, day time.Time, accountID *uuid.UUID,
) (ExpensesDetail, error) {
	from := startOfDay(day)
	to := from.AddDate(0, 0, 1)

	out := ExpensesDetail{
		Date:    day.Format("2006-01-02"),
		Entries: []ExpenseEntry{}, ByAccount: []StatementLine{},
	}
	if accountID != nil {
		out.AccountID = accountID.String()
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := readCurrency(ctx, tx, scope.CompanyID, &out.BaseCurrency); e != nil {
			return e
		}

		// Cost of sales is excluded here exactly as it is on the tile. It is
		// already counted as the profit tile's cost, and showing it again under
		// "expenses" would double it in the owner's head.
		const scopeSQL = `
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			JOIN account a       ON a.id = l.account_id
			WHERE e.company_id = $1
			  AND a.type = 'expense'
			  AND a.id NOT IN (SELECT account_id FROM account_role_map
			                   WHERE company_id = $1 AND role = 'cogs')
			  AND e.entry_date >= $2 AND e.entry_date < $3
			  AND ($4::uuid IS NULL OR l.store_id = $4::uuid)
			  AND ($5::uuid IS NULL OR a.id = $5::uuid)`

		summary, e := tx.Query(ctx, `
			SELECT a.id, a.code, a.name, coalesce(a.translations->>'ar', ''),
			       sum(l.base_debit - l.base_credit)::text`+
			scopeSQL+`
			GROUP BY a.id, a.code, a.name, a.translations
			HAVING sum(l.base_debit - l.base_credit) <> 0
			ORDER BY sum(l.base_debit - l.base_credit) DESC`,
			scope.CompanyID, from, to, scope.StoreID, accountID)
		if e != nil {
			return e
		}
		defer summary.Close()

		total := dec("0")
		for summary.Next() {
			var line StatementLine
			if e := summary.Scan(&line.AccountID, &line.Code, &line.Name,
				&line.NameAr, &line.Amount); e != nil {
				return e
			}
			total = total.Add(dec(line.Amount))
			out.ByAccount = append(out.ByAccount, line)
		}
		if e := summary.Err(); e != nil {
			return e
		}
		out.Total = total.StringFixed(2)

		entries, e := tx.Query(ctx, `
			SELECT e.id, coalesce(e.entry_no::text, ''), e.entry_date::date::text,
			       coalesce(e.memo, ''), a.id, a.name,
			       coalesce(a.translations->>'ar', ''), a.code,
			       (l.base_debit - l.base_credit)::text,
			       coalesce(e.source_type, '')`+
			scopeSQL+`
			  AND (l.base_debit - l.base_credit) <> 0
			ORDER BY e.entry_date DESC, e.entry_no DESC
			LIMIT 500`,
			scope.CompanyID, from, to, scope.StoreID, accountID)
		if e != nil {
			return e
		}
		defer entries.Close()

		for entries.Next() {
			var row ExpenseEntry
			if e := entries.Scan(&row.EntryID, &row.EntryNo, &row.Date, &row.Memo,
				&row.AccountID, &row.Account, &row.AccountAr, &row.Code,
				&row.Amount, &row.SourceType); e != nil {
				return e
			}
			out.Entries = append(out.Entries, row)
		}
		return entries.Err()
	})
	return out, err
}

// --- Compliance queue ----------------------------------------------------

// ComplianceRow is one invoice that has not finished its ZATCA lifecycle.
type ComplianceRow struct {
	InvoiceID   uuid.UUID `json:"invoice_id"`
	HumanNumber string    `json:"human_number,omitempty"`
	DocType     string    `json:"doc_type"`
	State       string    `json:"state"`
	IssuedAt    string    `json:"issued_at"`
	Total       string    `json:"total_inclusive"`
	// ICV is the position on this terminal's chain. Shown because a gap in the
	// sequence is the exact signal tamper detection looks for, and an owner
	// chasing a stuck invoice needs to know where in the chain it sits.
	ICV int `json:"icv"`
	// AgeHours drives the escalation thresholds of design 08 §4: notice past
	// 12 hours, warning past 24, critical past 72.
	AgeHours  int    `json:"age_hours"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error,omitempty"`
}

type ComplianceQueue struct {
	Rows []ComplianceRow `json:"rows"`
	// Outstanding counts every unfinished invoice, not just the page.
	Outstanding int `json:"outstanding"`
	// OldestHours is what the escalation is actually measured against.
	OldestHours  int    `json:"oldest_hours"`
	BaseCurrency string `json:"base_currency"`
	// SigningAvailable reports the P1 gate honestly. False means the terminal
	// cannot yet sign, which is WHY these are outstanding — and the screen must
	// say so rather than implying a transient failure somebody can retry away.
	SigningAvailable bool `json:"signing_available"`
}

// ComplianceFor lists invoices that have not completed reporting or clearance.
//
// This screen must never imply that a retry will fix what a retry cannot fix.
// The P1 verification gate is open: the terminal refuses to sign because the
// canonicalisation and QR TLV encoding are unverified, and every invoice here
// is waiting on that rather than on a flaky network. SigningAvailable carries
// that fact so the screen can state it plainly.
func (s *Service) ComplianceFor(
	ctx context.Context, scope Scope, limit int,
) (ComplianceQueue, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	out := ComplianceQueue{Rows: []ComplianceRow{}}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := readCurrency(ctx, tx, scope.CompanyID, &out.BaseCurrency); e != nil {
			return e
		}

		// Unfinished is defined as "has not reached a terminal state". Listing
		// by what is NOT done rather than by naming the pending states means a
		// state added later shows up here by default, which is the safe
		// direction for a compliance screen to fail in.
		const pending = `
			FROM sales_invoice i
			LEFT JOIN zatca_invoice z ON z.invoice_id = i.id
			WHERE i.company_id = $1
			  AND i.state NOT IN ('draft', 'cleared', 'reported', 'cancelled')`

		if e := tx.QueryRow(ctx, `
			SELECT count(*),
			       coalesce(max(extract(epoch FROM (now() - i.issued_at)) / 3600), 0)::int`+
			pending, scope.CompanyID).
			Scan(&out.Outstanding, &out.OldestHours); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT i.id, coalesce(i.human_number, ''), i.doc_type, i.state,
			       to_char(i.issued_at, 'YYYY-MM-DD HH24:MI'),
			       i.total_inclusive::text,
			       coalesce(z.icv, 0),
			       (extract(epoch FROM (now() - i.issued_at)) / 3600)::int,
			       (SELECT count(*) FROM zatca_submission_attempt sa
			        WHERE sa.invoice_id = i.id),
			       coalesce((SELECT sa.outcome FROM zatca_submission_attempt sa
			                 WHERE sa.invoice_id = i.id
			                 ORDER BY sa.attempt_no DESC LIMIT 1), '')`+
			pending+`
			ORDER BY i.issued_at ASC
			LIMIT $2`, scope.CompanyID, limit)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var r ComplianceRow
			if e := rows.Scan(&r.InvoiceID, &r.HumanNumber, &r.DocType, &r.State,
				&r.IssuedAt, &r.Total, &r.ICV, &r.AgeHours,
				&r.Attempts, &r.LastError); e != nil {
				return e
			}
			out.Rows = append(out.Rows, r)
		}
		return rows.Err()
	})

	// Hard-coded false, and it must stay that way until Tier-1 verification
	// passes. Reporting this as true would have the screen tell an owner that
	// submission is working, which is the single most damaging thing this
	// product could claim while P1 is open.
	out.SigningAvailable = false
	return out, err
}

// --- Stock -------------------------------------------------------------

// StockRow is one variant and what is on hand.
type StockRow struct {
	VariantID    uuid.UUID `json:"variant_id"`
	SKU          string    `json:"sku"`
	Name         string    `json:"name"`
	Barcode      string    `json:"barcode,omitempty"`
	OnHand       string    `json:"on_hand"`
	ReorderLevel string    `json:"reorder_level,omitempty"`
	Value        string    `json:"value"`
}

type StockDetail struct {
	Filter       string     `json:"filter"`
	Rows         []StockRow `json:"rows"`
	Count        int        `json:"count"`
	BaseCurrency string     `json:"base_currency"`
}

// StockFor lists variants that are out of stock or below their reorder level.
//
// Quantities are summed across the company's warehouses before being compared
// to the reorder level. Comparing per warehouse would report a shop as low on
// stock while a full box sat in the back room.
func (s *Service) StockFor(
	ctx context.Context, scope Scope, filter string,
) (StockDetail, error) {
	switch filter {
	case "low", "out":
	default:
		return StockDetail{}, errs.New(errs.CodeInvalidInput,
			"Ask for stock that is low or out.")
	}

	out := StockDetail{Filter: filter, Rows: []StockRow{}}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := readCurrency(ctx, tx, scope.CompanyID, &out.BaseCurrency); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			WITH on_hand AS (
			  SELECT v.id, v.sku, p.name, coalesce(v.barcode, '') AS barcode,
			         v.reorder_level,
			         coalesce(sum(stock_on_hand(v.id, w.id)), 0) AS qty
			  FROM variant v
			  JOIN product p ON p.id = v.product_id
			  LEFT JOIN warehouse w ON w.company_id = v.company_id
			  WHERE v.company_id = $1 AND v.is_active
			  GROUP BY v.id, v.sku, p.name, v.barcode, v.reorder_level
			)
			SELECT id, sku, name, barcode, qty::text,
			       coalesce(reorder_level::text, ''),
			       '0'::text
			FROM on_hand
			WHERE CASE WHEN $2 = 'out' THEN qty <= 0
			           ELSE reorder_level IS NOT NULL
			                AND qty > 0 AND qty <= reorder_level END
			ORDER BY qty, sku
			LIMIT 500`, scope.CompanyID, filter)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var r StockRow
			if e := rows.Scan(&r.VariantID, &r.SKU, &r.Name, &r.Barcode,
				&r.OnHand, &r.ReorderLevel, &r.Value); e != nil {
				return e
			}
			out.Rows = append(out.Rows, r)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		out.Count = len(out.Rows)
		return nil
	})
	return out, err
}

// --- shared --------------------------------------------------------------

func startOfDay(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
}

// readCurrency doubles as the tenant check. Row-level security hides another
// tenant's company entirely, so this comes back with no rows rather than with
// a permission error — and "not found" is the right answer to give, since
// confirming that a company id exists but belongs to someone else is itself a
// leak.
func readCurrency(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, into *string,
) error {
	e := tx.QueryRow(ctx,
		`SELECT base_currency FROM company WHERE id = $1`, companyID).Scan(into)
	if e == pgx.ErrNoRows {
		return errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return e
}
