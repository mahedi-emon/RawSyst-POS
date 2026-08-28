package reports

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The Owner Dashboard's figures.
//
// Blueprint A2 #10: answer "where is my money going" in one click. A8 requires
// every widget to drill through, so nothing here is a dead end — each block
// carries the identifiers the screen needs to open the detail behind it.
//
// # Everything is computed here
//
// Not one number on this screen is arithmetic the browser did. The dashboard is
// the same journal the trial balance reads, aggregated differently, so a figure
// an owner disputes can be traced to entries rather than to client-side code
// nobody can audit. It also means the Arabic build, the phone and the desktop
// cannot disagree about revenue.
//
// # What is absent is absent, not zero
//
// Purchases, suppliers, employees and CRM are Phase 2 and 3. Their tables do not
// exist, and this deliberately reports them as unavailable rather than as zero:
// an owner shown "Payables: 0.00" would reasonably conclude they owe nobody,
// which is a different and much worse statement than "not yet built".

// Overview is everything the dashboard's first screen needs, in one call.
//
// One request rather than nine, because a dashboard that fires nine parallel
// requests renders nine times and reflows under the owner's eyes. It also keeps
// every figure from the same instant — a screen where sales came from one
// moment and cash from another can appear not to balance.
type Overview struct {
	// Date is the business day these figures describe, as the company sees it.
	Date string `json:"date"`

	Sales        SalesToday    `json:"sales"`
	Profit       ProfitToday   `json:"profit"`
	Expenses     ExpensesToday `json:"expenses"`
	Money        MoneyPosition `json:"money"`
	Inventory    InventoryNow  `json:"inventory"`
	Tenders      []TenderSlice `json:"tenders"`
	Attention    []Attention   `json:"attention"`
	Unbuilt      []string      `json:"unbuilt"`
	BaseCurrency string        `json:"base_currency"`
}

// SalesToday carries yesterday alongside today, because a number with nothing
// to compare it to tells an owner nothing about whether it is a good day.
type SalesToday struct {
	Total     string `json:"total"`
	Yesterday string `json:"yesterday"`
	// ChangePct is omitted when yesterday was zero — "up 100%" from nothing is
	// a division artefact, not information.
	ChangePct    *string `json:"change_pct"`
	InvoiceCount int     `json:"invoice_count"`
	// Trend is the last 14 days including today, oldest first, for the
	// sparkline. Fourteen because a week alone cannot show a weekly rhythm.
	Trend []TrendPoint `json:"trend"`
}

type TrendPoint struct {
	Date  string `json:"date"`
	Total string `json:"total"`
}

// ProfitToday is revenue less cost of sales, from the same posting the P&L
// reads. Posted with the sale (C13), so gross profit is live rather than
// reconstructed at month end.
type ProfitToday struct {
	Revenue string `json:"revenue"`
	Cost    string `json:"cost"`
	Gross   string `json:"gross"`
	// MarginPct is omitted when there was no revenue, for the same reason
	// ChangePct is.
	MarginPct *string `json:"margin_pct"`
}

type ExpensesToday struct {
	Total string `json:"total"`
	// ByAccount drills through, per A8. Empty is a legitimate answer — most
	// shops post no expenses on most days.
	ByAccount []StatementLine `json:"by_account"`
}

// MoneyPosition answers "where is my money" directly.
type MoneyPosition struct {
	Cash string `json:"cash"`
	Bank string `json:"bank"`
	// Unsettled is money taken but still with the acquirer (C12). Its own
	// figure on purpose: an owner counting the drawer and the bank would
	// otherwise conclude a day's card takings had vanished.
	Unsettled   string `json:"unsettled"`
	Receivable  string `json:"receivable"`
	StoreCredit string `json:"store_credit"`
	// Accrued is goods on the shelves that no supplier has invoiced yet.
	//
	// Reported because it is money the shop is going to owe and has not been
	// asked for. An owner reading their payables without it would think they
	// owed less than they do — the invoice is coming, and the stock is already
	// being sold.
	Accrued string `json:"accrued_purchases"`
	Total   string `json:"total"`
}

type InventoryNow struct {
	// Value is what the stock on hand cost, from the same valuation the
	// balance sheet uses — never a retail-price estimate.
	Value        string `json:"value"`
	LowStock     int    `json:"low_stock"`
	OutOfStock   int    `json:"out_of_stock"`
	VariantCount int    `json:"variant_count"`
}

// TenderSlice is one payment method's share of the day.
//
// Never merged into "card". Mada's fee is materially lower than a scheme card's,
// so folding them together misstates margin — E3.1 requires per-tender
// visibility and the dashboard is where an owner would notice.
type TenderSlice struct {
	Method string `json:"method"`
	Total  string `json:"total"`
	Count  int    `json:"count"`
}

// Attention is one row of the "needs attention" list.
type Attention struct {
	// Severity is critical | warning | notice. The screen sorts on it and
	// stripes accordingly; it never relies on colour alone.
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Count    int    `json:"count"`
	// Link is where the row drills through to. Empty means there is nowhere
	// useful to go yet, and the screen renders it as plain text rather than a
	// dead link.
	Link string `json:"link"`
}

// OverviewFor gathers the dashboard in one pass.
func (s *Service) OverviewFor(
	ctx context.Context, scope Scope, day time.Time,
) (Overview, error) {
	out := Overview{
		Date:      day.Format("2006-01-02"),
		Tenders:   []TenderSlice{},
		Attention: []Attention{},
		// Named plainly so the screen can say what is coming rather than
		// showing an empty widget that looks broken.
		Unbuilt: []string{"employees"},
	}

	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	prevFrom := from.AddDate(0, 0, -1)

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// Row-level security hides another tenant's company entirely, so this
		// comes back with no rows rather than with a permission error. Said as
		// "not found", which is also the right answer to give: confirming that
		// a company id exists but belongs to someone else is itself a leak.
		e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&out.BaseCurrency)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That company was not found.")
		}
		if e != nil {
			return e
		}
		if e := s.salesToday(ctx, tx, scope, from, to, prevFrom, &out); e != nil {
			return e
		}
		if e := s.profitToday(ctx, tx, scope, from, to, &out); e != nil {
			return e
		}
		if e := s.expensesToday(ctx, tx, scope, from, to, &out); e != nil {
			return e
		}
		if e := s.moneyPosition(ctx, tx, scope, to, &out); e != nil {
			return e
		}
		if e := s.inventoryNow(ctx, tx, scope, &out); e != nil {
			return e
		}
		if e := s.tenderMix(ctx, tx, scope, from, to, &out); e != nil {
			return e
		}
		return s.needsAttention(ctx, tx, scope, &out)
	})
	return out, err
}

func (s *Service) salesToday(
	ctx context.Context, tx pgx.Tx, scope Scope,
	from, to, prevFrom time.Time, out *Overview,
) error {
	// Credit notes are excluded from the headline and counted in profit as a
	// reversal, because "today's sales" is what went over the counter. An
	// owner comparing this to the till's Z-report expects them to agree.
	if e := tx.QueryRow(ctx, `
		SELECT
		  coalesce(sum(i.total_inclusive) FILTER (
		    WHERE i.issue_date >= $2 AND i.issue_date < $3), 0)::text,
		  coalesce(sum(i.total_inclusive) FILTER (
		    WHERE i.issue_date >= $4 AND i.issue_date < $2), 0)::text,
		  count(*) FILTER (WHERE i.issue_date >= $2 AND i.issue_date < $3)
		FROM sales_invoice i
		WHERE i.company_id = $1
		  AND i.doc_type <> 'credit_note'
		  AND ($5::uuid IS NULL OR i.store_id = $5::uuid)`,
		scope.CompanyID, from, to, prevFrom, scope.StoreID,
	).Scan(&out.Sales.Total, &out.Sales.Yesterday, &out.Sales.InvoiceCount); e != nil {
		return e
	}

	today := decimal.RequireFromString(out.Sales.Total)
	yesterday := decimal.RequireFromString(out.Sales.Yesterday)
	if yesterday.IsPositive() {
		pct := today.Sub(yesterday).Div(yesterday).
			Mul(decimal.NewFromInt(100)).Round(1).String()
		out.Sales.ChangePct = &pct
	}

	// The trend. A left join over a generated series so quiet days appear as
	// zero rather than as a gap — a sparkline that silently closes over a
	// closed Friday misreports the shape of the week.
	rows, e := tx.Query(ctx, `
		SELECT d::date::text,
		       coalesce(sum(i.total_inclusive), 0)::text
		FROM generate_series($2::date - interval '13 days', $2::date,
		                     interval '1 day') d
		LEFT JOIN sales_invoice i
		  ON i.issue_date >= d AND i.issue_date < d + interval '1 day'
		 AND i.company_id = $1
		 AND i.doc_type <> 'credit_note'
		 AND ($3::uuid IS NULL OR i.store_id = $3::uuid)
		GROUP BY d ORDER BY d`,
		scope.CompanyID, from, scope.StoreID)
	if e != nil {
		return e
	}
	defer rows.Close()

	out.Sales.Trend = []TrendPoint{}
	for rows.Next() {
		var p TrendPoint
		if e := rows.Scan(&p.Date, &p.Total); e != nil {
			return e
		}
		out.Sales.Trend = append(out.Sales.Trend, p)
	}
	return rows.Err()
}

func (s *Service) profitToday(
	ctx context.Context, tx pgx.Tx, scope Scope, from, to time.Time, out *Overview,
) error {
	// From the journal, not from invoice lines. The posting engine is the
	// authority on what revenue and cost are, and a dashboard that computed
	// them from the sales tables could disagree with the P&L an accountant
	// signs off.
	if e := tx.QueryRow(ctx, `
		SELECT
		  coalesce(sum(l.base_credit - l.base_debit) FILTER (
		    WHERE a.type = 'revenue'), 0)::text,
		  coalesce(sum(l.base_debit - l.base_credit) FILTER (
		    WHERE a.type = 'expense' AND a.id IN (
		      SELECT account_id FROM account_role_map
		      WHERE company_id = $1 AND role = 'cogs')), 0)::text
		FROM journal_line l
		JOIN journal_entry e ON e.id = l.entry_id
		JOIN account a       ON a.id = l.account_id
		WHERE e.company_id = $1
		  AND e.entry_date >= $2 AND e.entry_date < $3
		  AND ($4::uuid IS NULL OR l.store_id = $4::uuid)`,
		scope.CompanyID, from, to, scope.StoreID,
	).Scan(&out.Profit.Revenue, &out.Profit.Cost); e != nil {
		return e
	}

	revenue := decimal.RequireFromString(out.Profit.Revenue)
	cost := decimal.RequireFromString(out.Profit.Cost)
	gross := revenue.Sub(cost)
	out.Profit.Gross = gross.StringFixed(2)

	if revenue.IsPositive() {
		pct := gross.Div(revenue).Mul(decimal.NewFromInt(100)).Round(1).String()
		out.Profit.MarginPct = &pct
	}
	return nil
}

func (s *Service) expensesToday(
	ctx context.Context, tx pgx.Tx, scope Scope, from, to time.Time, out *Overview,
) error {
	out.Expenses.ByAccount = []StatementLine{}

	// Cost of sales is excluded: it is already the profit tile's cost, and
	// showing it again under "expenses" would double it in the owner's head.
	rows, e := tx.Query(ctx, `
		SELECT a.id, a.code, a.name, coalesce(a.translations->>'ar', ''),
		       sum(l.base_debit - l.base_credit)::text
		FROM journal_line l
		JOIN journal_entry e ON e.id = l.entry_id
		JOIN account a       ON a.id = l.account_id
		WHERE e.company_id = $1
		  AND a.type = 'expense'
		  AND a.id NOT IN (
		    SELECT account_id FROM account_role_map
		    WHERE company_id = $1 AND role = 'cogs')
		  AND e.entry_date >= $2 AND e.entry_date < $3
		  AND ($4::uuid IS NULL OR l.store_id = $4::uuid)
		GROUP BY a.id, a.code, a.name, a.translations
		HAVING sum(l.base_debit - l.base_credit) <> 0
		ORDER BY sum(l.base_debit - l.base_credit) DESC`,
		scope.CompanyID, from, to, scope.StoreID)
	if e != nil {
		return e
	}
	defer rows.Close()

	total := decimal.Zero
	for rows.Next() {
		var line StatementLine
		var amount string
		if e := rows.Scan(&line.AccountID, &line.Code, &line.Name, &line.NameAr,
			&amount); e != nil {
			return e
		}
		line.Amount = amount
		total = total.Add(decimal.RequireFromString(amount))
		out.Expenses.ByAccount = append(out.Expenses.ByAccount, line)
	}
	if e := rows.Err(); e != nil {
		return e
	}
	out.Expenses.Total = total.StringFixed(2)
	return nil
}

func (s *Service) moneyPosition(
	ctx context.Context, tx pgx.Tx, scope Scope, asOf time.Time, out *Overview,
) error {
	// Balances by ROLE, so a company that named its cash account something
	// else still reports correctly. Cumulative to date rather than for the
	// day: a bank balance is a position, not a flow.
	var cash, bank, unsettled, receivable, storeCredit, accrued string
	if e := tx.QueryRow(ctx, `
		WITH balances AS (
		  SELECT m.role,
		         coalesce(sum(l.base_debit - l.base_credit), 0) AS balance
		  FROM account_role_map m
		  LEFT JOIN journal_line l  ON l.account_id = m.account_id
		  LEFT JOIN journal_entry e ON e.id = l.entry_id AND e.entry_date < $2
		  WHERE m.company_id = $1
		  GROUP BY m.role
		)
		SELECT
		  coalesce((SELECT balance FROM balances WHERE role = 'cash'), 0)::text,
		  coalesce((SELECT balance FROM balances WHERE role = 'bank'), 0)::text,
		  coalesce((SELECT balance FROM balances WHERE role = 'card_clearing'), 0)::text,
		  coalesce((SELECT balance FROM balances WHERE role = 'accounts_receivable'), 0)::text,
		  -- A liability: credit-normal, so the sign is flipped to read as
		  -- "what customers hold", which is how an owner thinks about it.
		  coalesce(-(SELECT balance FROM balances WHERE role = 'store_credit_liability'), 0)::text,
		  -- Also credit-normal, so also flipped to read as "what we will owe".
		  coalesce(-(SELECT balance FROM balances WHERE role = 'grni'), 0)::text`,
		scope.CompanyID, asOf,
	).Scan(&cash, &bank, &unsettled, &receivable, &storeCredit, &accrued); e != nil {
		return e
	}

	out.Money = MoneyPosition{
		Cash: cash, Bank: bank, Unsettled: unsettled,
		Receivable: receivable, StoreCredit: storeCredit, Accrued: accrued,
	}
	// Cash plus bank plus what the acquirer still holds. Receivables are
	// deliberately excluded — money a customer owes is not money the shop has,
	// and adding it to "where is my money" is how a business talks itself into
	// a cash-flow problem.
	out.Money.Total = decimal.RequireFromString(cash).
		Add(decimal.RequireFromString(bank)).
		Add(decimal.RequireFromString(unsettled)).StringFixed(2)
	return nil
}

func (s *Service) inventoryNow(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Overview,
) error {
	// Valuation from the costing engine, whichever model the company uses —
	// never quantity times retail price, which would overstate the balance
	// sheet by the whole margin.
	// Stock is held per warehouse, but an owner asks "have I got any" about the
	// company, so quantities are summed across warehouses before being compared
	// to the reorder level. Comparing per warehouse would report a shop as low
	// on stock while a full box sat in the back room.
	return tx.QueryRow(ctx, `
		WITH on_hand AS (
		  SELECT v.id,
		         v.reorder_level,
		         coalesce(sum(stock_on_hand(v.id, w.id)), 0) AS qty
		  FROM variant v
		  LEFT JOIN warehouse w ON w.company_id = v.company_id
		  WHERE v.company_id = $1 AND v.is_active
		  GROUP BY v.id, v.reorder_level
		)
		SELECT
		  inventory_valuation($1)::text,
		  (SELECT count(*) FROM on_hand
		   WHERE reorder_level IS NOT NULL AND qty > 0 AND qty <= reorder_level),
		  (SELECT count(*) FROM on_hand WHERE qty <= 0),
		  (SELECT count(*) FROM on_hand)`,
		scope.CompanyID,
	).Scan(&out.Inventory.Value, &out.Inventory.LowStock,
		&out.Inventory.OutOfStock, &out.Inventory.VariantCount)
}

func (s *Service) tenderMix(
	ctx context.Context, tx pgx.Tx, scope Scope, from, to time.Time, out *Overview,
) error {
	// exchange_clearing is excluded: it is an internal offset that moves no
	// money, and showing it beside Mada and cash would tell an owner they had
	// taken payments they never took.
	rows, e := tx.Query(ctx, `
		SELECT t.method, sum(t.amount)::text, count(*)
		FROM sales_tender t
		JOIN sales_invoice i ON i.id = t.invoice_id
		WHERE i.company_id = $1
		  AND i.doc_type <> 'credit_note'
		  AND i.issue_date >= $2 AND i.issue_date < $3
		  AND t.method <> 'exchange_clearing'
		  AND ($4::uuid IS NULL OR i.store_id = $4::uuid)
		GROUP BY t.method
		ORDER BY sum(t.amount) DESC`,
		scope.CompanyID, from, to, scope.StoreID)
	if e != nil {
		return e
	}
	defer rows.Close()

	for rows.Next() {
		var slice TenderSlice
		if e := rows.Scan(&slice.Method, &slice.Total, &slice.Count); e != nil {
			return e
		}
		out.Tenders = append(out.Tenders, slice)
	}
	return rows.Err()
}

func (s *Service) needsAttention(
	ctx context.Context, tx pgx.Tx, scope Scope, out *Overview,
) error {
	// Compliance first, always. An unreported invoice has a legal deadline
	// attached to it; a low stock level has an inconvenience.
	rows, e := tx.Query(ctx, `
		SELECT level, kind, count(*)
		FROM compliance_alert
		WHERE company_id = $1 AND cleared_at IS NULL
		GROUP BY level, kind`,
		scope.CompanyID)
	if e != nil {
		return e
	}
	defer rows.Close()

	for rows.Next() {
		var level, kind string
		var count int
		if e := rows.Scan(&level, &kind, &count); e != nil {
			return e
		}
		out.Attention = append(out.Attention, Attention{
			Severity: severityOf(level), Kind: kind,
			Title:  "Invoices not yet reported",
			Detail: "Submission to ZATCA is overdue. Sales are recorded correctly; the reporting is outstanding.",
			Count:  count, Link: "/compliance",
		})
	}
	if e := rows.Err(); e != nil {
		return e
	}

	if out.Inventory.OutOfStock > 0 {
		out.Attention = append(out.Attention, Attention{
			Severity: "warning", Kind: "out_of_stock",
			Title:  "Items out of stock",
			Detail: "These cannot be sold until stock is received.",
			Count:  out.Inventory.OutOfStock, Link: "/inventory?filter=out",
		})
	}
	if out.Inventory.LowStock > 0 {
		out.Attention = append(out.Attention, Attention{
			Severity: "notice", Kind: "low_stock",
			Title:  "Items below reorder level",
			Detail: "Still sellable, but worth ordering.",
			Count:  out.Inventory.LowStock, Link: "/inventory?filter=low",
		})
	}
	return nil
}

// severityOf maps the escalation levels of design 08 §4 onto what the screen
// shows. critical is >72h, warning >24h, notice >12h.
func severityOf(level string) string {
	switch level {
	case "critical":
		return "critical"
	case "warning":
		return "warning"
	default:
		return "notice"
	}
}

// Company is a legal entity the caller can work in.
//
// The minimum a client needs to choose one and format money for it. Deliberately
// not the full record: a company row carries VAT registration, CR number and
// ZATCA wave, none of which a screen needs merely to pick between two shops.
type Company struct {
	ID           uuid.UUID `json:"id"`
	LegalName    string    `json:"legal_name"`
	TradeName    string    `json:"trade_name,omitempty"`
	Country      string    `json:"country"`
	BaseCurrency string    `json:"base_currency"`
}

// CompaniesFor lists the tenant's companies, oldest first.
//
// Stable order on purpose: a company picker that reshuffles between page loads
// makes an owner check twice that they are looking at the right shop.
func (s *Service) CompaniesFor(
	ctx context.Context, tenantID uuid.UUID,
) ([]Company, error) {
	out := []Company{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, legal_name, coalesce(trade_name, ''), country, base_currency
			FROM company
			ORDER BY created_at, id`)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var c Company
			if e := rows.Scan(&c.ID, &c.LegalName, &c.TradeName,
				&c.Country, &c.BaseCurrency); e != nil {
				return e
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// dec parses a decimal the database produced. Panics on malformed input,
// deliberately: every caller passes a value Postgres just serialised from a
// numeric column, so a failure here is a schema bug rather than bad user
// input, and returning zero would hide it inside a total.
func dec(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	return decimal.RequireFromString(s)
}
