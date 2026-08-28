// Package reports draws the financial statements from the journal.
//
// # One source, several views
//
// Every figure below comes from journal_line. Nothing is stored, cached or
// maintained alongside the ledger, because a second copy of a balance is a
// second thing that can be wrong — and the copy is always the one that gets
// believed, since it is the one on the screen.
//
// # A period and a moment are different questions
//
// A Profit and Loss covers a PERIOD: what was earned and spent between two
// dates. A Balance Sheet is a MOMENT: what is owned and owed on one date,
// accumulated from the beginning of the books. Mixing them up is the commonest
// way a statement stops balancing, and it is why the two take different
// arguments here rather than sharing a date range.
//
// # Why the Balance Sheet includes current earnings
//
// Assets = Liabilities + Equity only holds once profit earned this year is
// counted as equity. Until the year is closed, that profit sits in revenue and
// expense accounts rather than in retained earnings — so a balance sheet drawn
// from the equity accounts alone is short by exactly the year's profit, every
// day of the year until year end. Current earnings are therefore computed and
// shown as their own equity line.
package reports

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service draws statements for a company.
type Service struct {
	pool *db.Pool
}

func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope narrows a statement.
//
// StoreID is optional and filters by the dimension carried on each journal
// line. A branch P&L is a real question — which shop is making money — but a
// branch BALANCE SHEET is usually not, because assets like bank accounts are
// rarely attributable to one branch. The filter is offered on both and the
// caller decides; what matters is that the same filter means the same thing in
// both places.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	StoreID   *uuid.UUID
}

// --- Trial balance -------------------------------------------------------

// TrialBalanceRow is one account's totals.
type TrialBalanceRow struct {
	AccountID uuid.UUID `json:"account_id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`

	Debit  string `json:"debit"`
	Credit string `json:"credit"`
}

// TrialBalance is every account with a balance, plus the proof it balances.
type TrialBalance struct {
	AsOf string            `json:"as_of"`
	Rows []TrialBalanceRow `json:"rows"`

	TotalDebit  string `json:"total_debit"`
	TotalCredit string `json:"total_credit"`

	// Difference must be zero. It is reported rather than asserted because a
	// trial balance whose whole purpose is to reveal an imbalance should show
	// the imbalance, not refuse to render.
	Difference string `json:"difference"`
	Balanced   bool   `json:"balanced"`
}

// TrialBalanceAt draws the trial balance as at a date, inclusive.
func (s *Service) TrialBalanceAt(
	ctx context.Context, scope Scope, asOf time.Time,
) (TrialBalance, error) {
	out := TrialBalance{AsOf: asOf.Format("2006-01-02"), Rows: []TrialBalanceRow{}}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT a.id, a.code, a.name, a.type,
			       coalesce(sum(l.base_debit), 0),
			       coalesce(sum(l.base_credit), 0)
			FROM account a
			JOIN journal_line l  ON l.account_id = a.id
			JOIN journal_entry e ON e.id = l.entry_id
			WHERE a.company_id = $1
			  AND e.entry_date <= $2::date
			  AND ($3::uuid IS NULL OR l.store_id = $3::uuid)
			GROUP BY a.id, a.code, a.name, a.type
			HAVING coalesce(sum(l.base_debit), 0) <> 0
			    OR coalesce(sum(l.base_credit), 0) <> 0
			ORDER BY a.code`,
			scope.CompanyID, asOf, scope.StoreID)
		if e != nil {
			return e
		}
		defer rows.Close()

		totalDebit, totalCredit := decimal.Zero, decimal.Zero
		for rows.Next() {
			var r TrialBalanceRow
			var debit, credit decimal.Decimal
			if e := rows.Scan(&r.AccountID, &r.Code, &r.Name, &r.Type,
				&debit, &credit); e != nil {
				return e
			}
			r.Debit, r.Credit = debit.String(), credit.String()
			totalDebit = totalDebit.Add(debit)
			totalCredit = totalCredit.Add(credit)
			out.Rows = append(out.Rows, r)
		}
		if e := rows.Err(); e != nil {
			return e
		}

		diff := totalDebit.Sub(totalCredit)
		out.TotalDebit, out.TotalCredit = totalDebit.String(), totalCredit.String()
		out.Difference = diff.String()
		out.Balanced = diff.IsZero()
		return nil
	})
	return out, err
}

// --- Profit and loss -----------------------------------------------------

// StatementLine is one account's contribution to a statement.
type StatementLine struct {
	AccountID uuid.UUID `json:"account_id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	// NameAr is what an Arabic screen shows instead, and is empty for an
	// account nobody has written one for.
	//
	// Both names are sent and the screen picks, which is how the catalogue
	// already carries a product's Arabic name. The alternative — the server
	// choosing from a request header — puts the reader's language into a cache
	// key and into every report that is generated rather than viewed.
	NameAr string `json:"name_ar,omitempty"`
	Amount string `json:"amount"`
}

// ProfitAndLoss covers a period.
type ProfitAndLoss struct {
	From string `json:"from"`
	To   string `json:"to"`

	Revenue      []StatementLine `json:"revenue"`
	RevenueTotal string          `json:"revenue_total"`

	// CostOfSales is separated from other expenses because gross profit is the
	// number a retailer actually manages. Folding COGS into operating expenses
	// hides whether the shop is buying and pricing well, which is a different
	// question from whether it is spending well.
	CostOfSales      []StatementLine `json:"cost_of_sales"`
	CostOfSalesTotal string          `json:"cost_of_sales_total"`
	GrossProfit      string          `json:"gross_profit"`

	Expenses      []StatementLine `json:"expenses"`
	ExpensesTotal string          `json:"expenses_total"`

	NetProfit string `json:"net_profit"`
}

// ProfitAndLossFor draws the P&L between two dates, both inclusive.
func (s *Service) ProfitAndLossFor(
	ctx context.Context, scope Scope, from, to time.Time,
) (ProfitAndLoss, error) {
	if to.Before(from) {
		return ProfitAndLoss{}, errs.New(errs.CodeInvalidInput,
			"The end of the period comes before its start.")
	}

	out := ProfitAndLoss{
		From: from.Format("2006-01-02"), To: to.Format("2006-01-02"),
		Revenue: []StatementLine{}, CostOfSales: []StatementLine{},
		Expenses: []StatementLine{},
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// Revenue is credit-normal and expense debit-normal, so each is signed
		// to read positive when it behaves normally. A negative revenue line is
		// then visibly odd — a contra entry or a mistake — rather than hidden by
		// an absolute value.
		rows, e := tx.Query(ctx, `
			SELECT a.id, a.code, a.name, a.type, a.is_control, a.control_of,
			       CASE WHEN a.type = 'revenue'
			            THEN coalesce(sum(l.base_credit - l.base_debit), 0)
			            ELSE coalesce(sum(l.base_debit - l.base_credit), 0) END
			FROM account a
			JOIN journal_line l  ON l.account_id = a.id
			JOIN journal_entry e ON e.id = l.entry_id
			WHERE a.company_id = $1
			  AND a.type IN ('revenue', 'expense')
			  AND e.entry_date BETWEEN $2::date AND $3::date
			  AND ($4::uuid IS NULL OR l.store_id = $4::uuid)
			GROUP BY a.id, a.code, a.name, a.type, a.is_control, a.control_of
			HAVING coalesce(sum(l.base_debit - l.base_credit), 0) <> 0
			ORDER BY a.code`,
			scope.CompanyID, from, to, scope.StoreID)
		if e != nil {
			return e
		}
		defer rows.Close()

		revenue, cogs, expenses := decimal.Zero, decimal.Zero, decimal.Zero
		for rows.Next() {
			var l StatementLine
			var kind string
			var isControl bool
			var controlOf *string
			var amount decimal.Decimal
			if e := rows.Scan(&l.AccountID, &l.Code, &l.Name, &kind,
				&isControl, &controlOf, &amount); e != nil {
				return e
			}
			l.Amount = amount.String()

			switch {
			case kind == "revenue":
				out.Revenue = append(out.Revenue, l)
				revenue = revenue.Add(amount)
			case isCostOfSales(l.Code, l.Name):
				out.CostOfSales = append(out.CostOfSales, l)
				cogs = cogs.Add(amount)
			default:
				out.Expenses = append(out.Expenses, l)
				expenses = expenses.Add(amount)
			}
		}
		if e := rows.Err(); e != nil {
			return e
		}

		out.RevenueTotal = revenue.String()
		out.CostOfSalesTotal = cogs.String()
		out.GrossProfit = revenue.Sub(cogs).String()
		out.ExpensesTotal = expenses.String()
		out.NetProfit = revenue.Sub(cogs).Sub(expenses).String()
		return nil
	})
	return out, err
}

// isCostOfSales decides whether an expense account belongs above the gross
// profit line.
//
// Recognised by the account ROLE mapping in a mature chart of accounts; until
// the posting-rule engine reads roles for reporting too, this falls back to the
// conventional 5xxx range that the seeded chart uses. It is a presentation
// choice and cannot change any total: revenue less cost of sales less expenses
// equals net profit whichever side of the line an account is shown on.
func isCostOfSales(code, name string) bool {
	if len(code) > 0 && code[0] == '5' {
		return true
	}
	return name == "Cost of Goods Sold"
}

// --- Balance sheet -------------------------------------------------------

// BalanceSheet is a moment.
type BalanceSheet struct {
	AsOf string `json:"as_of"`

	Assets      []StatementLine `json:"assets"`
	AssetsTotal string          `json:"assets_total"`

	Liabilities      []StatementLine `json:"liabilities"`
	LiabilitiesTotal string          `json:"liabilities_total"`

	Equity      []StatementLine `json:"equity"`
	EquityTotal string          `json:"equity_total"`

	// CurrentEarnings is profit earned since the books began that has not yet
	// been closed into retained earnings. Without it the sheet is short by
	// exactly the year's profit on every day of the year.
	CurrentEarnings string `json:"current_earnings"`

	// EquityAndLiabilities is the right-hand side, including current earnings.
	EquityAndLiabilities string `json:"equity_and_liabilities"`

	Difference string `json:"difference"`
	Balanced   bool   `json:"balanced"`
}

// BalanceSheetAt draws the balance sheet as at a date, inclusive.
func (s *Service) BalanceSheetAt(
	ctx context.Context, scope Scope, asOf time.Time,
) (BalanceSheet, error) {
	out := BalanceSheet{
		AsOf:   asOf.Format("2006-01-02"),
		Assets: []StatementLine{}, Liabilities: []StatementLine{},
		Equity: []StatementLine{},
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT a.id, a.code, a.name, a.type,
			       CASE WHEN a.type = 'asset'
			            THEN coalesce(sum(l.base_debit - l.base_credit), 0)
			            ELSE coalesce(sum(l.base_credit - l.base_debit), 0) END
			FROM account a
			JOIN journal_line l  ON l.account_id = a.id
			JOIN journal_entry e ON e.id = l.entry_id
			WHERE a.company_id = $1
			  AND a.type IN ('asset', 'liability', 'equity')
			  AND e.entry_date <= $2::date
			  AND ($3::uuid IS NULL OR l.store_id = $3::uuid)
			GROUP BY a.id, a.code, a.name, a.type
			HAVING coalesce(sum(l.base_debit - l.base_credit), 0) <> 0
			ORDER BY a.code`,
			scope.CompanyID, asOf, scope.StoreID)
		if e != nil {
			return e
		}
		defer rows.Close()

		assets, liabilities, equity := decimal.Zero, decimal.Zero, decimal.Zero
		for rows.Next() {
			var l StatementLine
			var kind string
			var amount decimal.Decimal
			if e := rows.Scan(&l.AccountID, &l.Code, &l.Name, &kind, &amount); e != nil {
				return e
			}
			l.Amount = amount.String()

			switch kind {
			case "asset":
				out.Assets = append(out.Assets, l)
				assets = assets.Add(amount)
			case "liability":
				out.Liabilities = append(out.Liabilities, l)
				liabilities = liabilities.Add(amount)
			default:
				out.Equity = append(out.Equity, l)
				equity = equity.Add(amount)
			}
		}
		if e := rows.Err(); e != nil {
			return e
		}

		// Everything earned and spent up to this date, still sitting in revenue
		// and expense accounts.
		//
		// One expression covers both: revenue is credit-normal so credit less
		// debit reads positive, and expense is debit-normal so the same
		// expression reads negative. Their sum is therefore profit, with no
		// per-type branching to get backwards.
		var earnings decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(l.base_credit - l.base_debit), 0)
			FROM account a
			JOIN journal_line l  ON l.account_id = a.id
			JOIN journal_entry e ON e.id = l.entry_id
			WHERE a.company_id = $1
			  AND a.type IN ('revenue', 'expense')
			  AND e.entry_date <= $2::date
			  AND ($3::uuid IS NULL OR l.store_id = $3::uuid)`,
			scope.CompanyID, asOf, scope.StoreID).Scan(&earnings); e != nil {
			return e
		}

		right := liabilities.Add(equity).Add(earnings)
		diff := assets.Sub(right)

		out.AssetsTotal = assets.String()
		out.LiabilitiesTotal = liabilities.String()
		out.EquityTotal = equity.String()
		out.CurrentEarnings = earnings.String()
		out.EquityAndLiabilities = right.String()
		out.Difference = diff.String()
		out.Balanced = diff.IsZero()
		return nil
	})
	return out, err
}

// --- Cash flow -----------------------------------------------------------

// CashFlowLine is one counterpart account's net effect on cash.
type CashFlowLine struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Amount string `json:"amount"`
}

// CashFlow is where money actually moved, by the direct method.
//
// Direct rather than indirect, deliberately. The indirect method starts from
// net profit and adjusts for non-cash items and working-capital movements,
// which needs every account classified as operating, investing or financing —
// classification this chart of accounts does not yet carry, and which cannot be
// invented without producing a statement that looks authoritative and is wrong.
//
// The direct method needs no classification: it reads what went in and out of
// the cash and bank accounts, and shows what each movement was against. For a
// retailer that is also the more useful statement.
type CashFlow struct {
	From string `json:"from"`
	To   string `json:"to"`

	Opening string `json:"opening"`
	Closing string `json:"closing"`

	In       []CashFlowLine `json:"in"`
	Out      []CashFlowLine `json:"out"`
	NetTotal string         `json:"net_total"`

	// Method is stated so nobody mistakes this for an IAS 7 indirect statement.
	Method string `json:"method"`
}

// CashFlowFor draws movements across cash and bank accounts between two dates.
func (s *Service) CashFlowFor(
	ctx context.Context, scope Scope, from, to time.Time,
) (CashFlow, error) {
	if to.Before(from) {
		return CashFlow{}, errs.New(errs.CodeInvalidInput,
			"The end of the period comes before its start.")
	}

	out := CashFlow{
		From: from.Format("2006-01-02"), To: to.Format("2006-01-02"),
		In: []CashFlowLine{}, Out: []CashFlowLine{}, Method: "direct",
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		cashAccounts, e := cashAccountIDs(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}
		if len(cashAccounts) == 0 {
			return errs.New(errs.CodeConflict,
				"This company has no cash or bank account mapped, so there is no "+
					"cash flow to draw. Map them under the chart of accounts.")
		}

		var opening decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM journal_line l JOIN journal_entry e ON e.id = l.entry_id
			WHERE l.account_id = ANY($1) AND e.entry_date < $2::date`,
			cashAccounts, from).Scan(&opening); e != nil {
			return e
		}

		// What each cash movement was AGAINST: the other side of every entry
		// that touched cash. That is the whole content of a direct cash flow —
		// money in from sales, out to suppliers, out to wages.
		rows, e := tx.Query(ctx, `
			SELECT a.code, a.name,
			       coalesce(sum(other.base_credit - other.base_debit), 0)
			FROM journal_line cash
			JOIN journal_entry e   ON e.id = cash.entry_id
			JOIN journal_line other ON other.entry_id = cash.entry_id
			                       AND other.account_id <> ALL($1)
			JOIN account a ON a.id = other.account_id
			WHERE cash.account_id = ANY($1)
			  AND e.entry_date BETWEEN $2::date AND $3::date
			  AND ($4::uuid IS NULL OR cash.store_id = $4::uuid)
			GROUP BY a.code, a.name
			HAVING coalesce(sum(other.base_credit - other.base_debit), 0) <> 0
			ORDER BY a.code`,
			cashAccounts, from, to, scope.StoreID)
		if e != nil {
			return e
		}
		defer rows.Close()

		net := decimal.Zero
		for rows.Next() {
			var l CashFlowLine
			var amount decimal.Decimal
			if e := rows.Scan(&l.Code, &l.Name, &amount); e != nil {
				return e
			}
			l.Amount = amount.String()
			if amount.IsPositive() {
				out.In = append(out.In, l)
			} else {
				out.Out = append(out.Out, l)
			}
			net = net.Add(amount)
		}
		if e := rows.Err(); e != nil {
			return e
		}

		out.Opening = opening.String()
		out.NetTotal = net.String()
		out.Closing = opening.Add(net).String()
		return nil
	})
	return out, err
}

// cashAccountIDs finds the accounts that hold actual money.
//
// Read from the role mapping rather than guessed from account codes, so a
// tenant that renumbers its chart does not silently lose its cash flow
// statement. Card clearing is excluded on purpose: that money is owed by the
// acquirer and has not arrived.
func cashAccountIDs(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT account_id FROM account_role_map
		WHERE company_id = $1 AND role IN ('cash', 'bank')`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
