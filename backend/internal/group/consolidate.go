// Consolidated statements: the group profit and loss and the group balance
// sheet (blueprint F4).
//
// The arithmetic, stated once so it is not inferred from the SQL:
//
//   1. Read each member company's balances from the ledger, in that company's
//      own base currency, exactly as the single-company statement does.
//   2. Subtract the lines of entries a person marked as inter-company.
//   3. Multiply each company's contribution by the group's holding in it.
//   4. Add the companies up, per account CODE rather than per account ID —
//      every company has its own chart, seeded from the same template, so
//      "4100 Sales Revenue" exists four times as four different rows.
//
// Step 4 is why the response reports by code. Two companies whose charts have
// drifted apart contribute to different lines, which is the honest outcome: a
// consolidation that silently merged "4100 Sales" with "4110 Sales — Wholesale"
// because they looked similar would be a guess.

package group

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
)

// ConsolidatedLine is one account code, summed across the group.
type ConsolidatedLine struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	NameAr string `json:"name_ar,omitempty"`
	Type   string `json:"type"`
	Amount string `json:"amount"`

	// Per company, so a reader can see where a total came from. The key is the
	// company id as a string, because JSON object keys are strings.
	ByCompany map[string]string `json:"by_company"`
}

// Contribution says how one company entered the consolidation.
type Contribution struct {
	CompanyID    uuid.UUID `json:"company_id"`
	Name         string    `json:"name"`
	BaseCurrency string    `json:"base_currency"`
	Ownership    string    `json:"ownership_pct"`
	// CurrencyDiffers is true when this company's books are not in the group's
	// presentation currency. See the package note: its figures are still shown,
	// unconverted, and flagged rather than translated with a rate nobody chose.
	CurrencyDiffers bool `json:"currency_differs"`
}

// Consolidated is a group statement.
type Consolidated struct {
	GroupID  uuid.UUID `json:"group_id"`
	Name     string    `json:"name"`
	Currency string    `json:"presentation_currency"`
	From     string    `json:"from,omitempty"`
	To       string    `json:"to"`

	Companies []Contribution `json:"companies"`

	// Comparable is false when at least one member keeps its books in a
	// different currency from the group's presentation currency. The totals are
	// still returned, because a group of four riyal companies and one taka one
	// still wants to see the four; the flag is what stops the total being read
	// as a single figure.
	Comparable bool `json:"comparable"`

	Lines []ConsolidatedLine `json:"lines"`

	// The P&L subtotals. Empty on a balance sheet.
	Revenue     string `json:"revenue,omitempty"`
	CostOfSales string `json:"cost_of_sales,omitempty"`
	GrossProfit string `json:"gross_profit,omitempty"`
	Expenses    string `json:"expenses,omitempty"`
	NetProfit   string `json:"net_profit,omitempty"`

	// The balance sheet subtotals. Empty on a P&L.
	Assets      string `json:"assets,omitempty"`
	Liabilities string `json:"liabilities,omitempty"`
	Equity      string `json:"equity,omitempty"`
	Balanced    *bool  `json:"balanced,omitempty"`

	// What elimination removed, so the difference between the sum of the
	// companies and the consolidated total is visible rather than implied.
	EliminatedEntries int    `json:"eliminated_entries"`
	EliminatedAmount  string `json:"eliminated_amount"`
}

// ProfitAndLoss consolidates the group's trading for a period.
func (s *Service) ProfitAndLoss(
	ctx context.Context, scope Scope, groupID uuid.UUID, from, to time.Time,
) (Consolidated, error) {
	if to.Before(from) {
		return Consolidated{}, errs.New(errs.CodeInvalidInput,
			"The end of the period comes before its start.")
	}
	return s.consolidate(ctx, scope, groupID, &from, to, false)
}

// BalanceSheet consolidates the group's position at a date.
func (s *Service) BalanceSheet(
	ctx context.Context, scope Scope, groupID uuid.UUID, asOf time.Time,
) (Consolidated, error) {
	return s.consolidate(ctx, scope, groupID, nil, asOf, true)
}

// consolidate does both, because they differ only in which account types they
// read and whether the window has a start.
//
// One function rather than two nearly identical ones: the member resolution,
// the elimination, the ownership scaling and the summing by code are the same
// work, and the parts that differ are two lines.
func (s *Service) consolidate(
	ctx context.Context, scope Scope, groupID uuid.UUID,
	from *time.Time, to time.Time, balanceSheet bool,
) (Consolidated, error) {
	out := Consolidated{
		GroupID: groupID, To: to.Format("2006-01-02"),
		Companies: []Contribution{}, Lines: []ConsolidatedLine{},
		Comparable: true,
	}
	if from != nil {
		out.From = from.Format("2006-01-02")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			SELECT name, presentation_currency FROM company_group
			 WHERE id = $1 AND tenant_id = $2`,
			groupID, scope.TenantID).Scan(&out.Name, &out.Currency); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That group was not found.")
			}
			return e
		}

		// Members that were in the group on the reporting date. A company that
		// left in March does not belong in the December statement.
		rows, e := tx.Query(ctx, `
			SELECT m.company_id, c.legal_name, c.base_currency, m.ownership_pct
			FROM company_group_member m
			JOIN company c ON c.id = m.company_id
			WHERE m.group_id = $1
			  AND m.joined_on <= $2::date
			  AND (m.left_on IS NULL OR m.left_on >= $2::date)
			ORDER BY m.is_parent DESC, c.legal_name`, groupID, to)
		if e != nil {
			return e
		}
		type member struct {
			id        uuid.UUID
			ownership decimal.Decimal
		}
		members := []member{}
		for rows.Next() {
			var m member
			var c Contribution
			if e := rows.Scan(&c.CompanyID, &c.Name, &c.BaseCurrency,
				&m.ownership); e != nil {
				rows.Close()
				return e
			}
			m.id = c.CompanyID
			c.Ownership = m.ownership.StringFixed(2)
			c.CurrencyDiffers = c.BaseCurrency != out.Currency
			if c.CurrencyDiffers {
				out.Comparable = false
			}
			members = append(members, m)
			out.Companies = append(out.Companies, c)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}
		if len(members) == 0 {
			return errs.New(errs.CodeInvalidInput,
				"That group has no companies in it on that date, so there "+
					"is nothing to consolidate.")
		}

		types := "('revenue', 'expense')"
		if balanceSheet {
			types = "('asset', 'liability', 'equity')"
		}
		// The window. A balance sheet is cumulative from the first entry the
		// books hold, which is what a NULL start means here.
		var start any
		if from != nil {
			start = *from
		}

		// One query per company rather than one query over all of them.
		//
		// Not for tidiness: `l.base_debit` is denominated in the COMPANY's base
		// currency, and a single SUM over several companies would silently add
		// riyals to taka. Reading them apart is what makes the currency flag
		// above meaningful rather than decorative.
		byCode := map[string]*ConsolidatedLine{}
		for i, m := range members {
			lines, e := s.balancesOf(
				ctx, tx, m.id, start, to, types, balanceSheet)
			if e != nil {
				return e
			}
			key := m.id.String()
			for _, l := range lines {
				share := l.amount.Mul(m.ownership).
					Div(decimal.NewFromInt(100)).Round(2)

				agg, ok := byCode[l.code]
				if !ok {
					agg = &ConsolidatedLine{
						Code: l.code, Name: l.name, NameAr: l.nameAr,
						Type: l.kind, Amount: "0.00",
						ByCompany: map[string]string{},
					}
					byCode[l.code] = agg
				}
				running, _ := decimal.NewFromString(agg.Amount)
				agg.Amount = running.Add(share).StringFixed(2)
				agg.ByCompany[key] = share.StringFixed(2)
			}
			_ = i
		}

		// What elimination removed, reported beside the total.
		if e := s.eliminationTotals(
			ctx, tx, groupID, start, to, &out); e != nil {
			return e
		}

		codes := make([]string, 0, len(byCode))
		for code := range byCode {
			codes = append(codes, code)
		}
		sortStrings(codes)
		for _, code := range codes {
			out.Lines = append(out.Lines, *byCode[code])
		}

		s.subtotals(&out, balanceSheet)
		return nil
	})
	return out, db.Translate(err, "")
}

// balance is one account's contribution from one company.
type balance struct {
	code, name, nameAr, kind string
	amount                   decimal.Decimal
}

// balancesOf reads one company's account balances, with inter-company lines
// removed.
//
// The sign convention matches the single-company statements: revenue and
// liability and equity are credit-normal and are signed to read positive when
// they behave normally, so a negative revenue line is visibly odd rather than
// hidden by an absolute value.
func (s *Service) balancesOf(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	start any, to time.Time, types string, balanceSheet bool,
) ([]balance, error) {
	// `types` is one of two constants chosen above, never caller input.
	rows, err := tx.Query(ctx, `
		SELECT a.code, a.name, coalesce(a.translations->>'ar', ''), a.type,
		       CASE WHEN a.type IN ('revenue', 'liability', 'equity')
		            THEN coalesce(sum(l.base_credit - l.base_debit), 0)
		            ELSE coalesce(sum(l.base_debit - l.base_credit), 0) END
		FROM account a
		JOIN journal_line l  ON l.account_id = a.id
		JOIN journal_entry e ON e.id = l.entry_id
		WHERE a.company_id = $1
		  AND a.type IN `+types+`
		  AND ($2::date IS NULL OR e.entry_date >= $2::date)
		  AND e.entry_date <= $3::date
		  AND NOT EXISTS (
		    SELECT 1 FROM intercompany_entry ic WHERE ic.entry_id = e.id)
		GROUP BY a.code, a.name, a.translations, a.type
		HAVING coalesce(sum(l.base_debit - l.base_credit), 0) <> 0
		ORDER BY a.code`, companyID, start, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []balance{}
	for rows.Next() {
		var b balance
		if e := rows.Scan(&b.code, &b.name, &b.nameAr, &b.kind,
			&b.amount); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	_ = balanceSheet
	return out, rows.Err()
}

// eliminationTotals says how much was taken out and from how many entries.
func (s *Service) eliminationTotals(
	ctx context.Context, tx pgx.Tx, groupID uuid.UUID,
	start any, to time.Time, out *Consolidated,
) error {
	var amount decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT e.id),
		       coalesce(sum(l.base_debit), 0)
		FROM intercompany_entry ic
		JOIN journal_entry e ON e.id = ic.entry_id
		JOIN journal_line l  ON l.entry_id = e.id
		JOIN company_group_member m ON m.company_id = ic.company_id
		WHERE m.group_id = $1
		  AND ($2::date IS NULL OR e.entry_date >= $2::date)
		  AND e.entry_date <= $3::date`,
		groupID, start, to).Scan(&out.EliminatedEntries, &amount); err != nil {
		return err
	}
	out.EliminatedAmount = amount.StringFixed(2)
	return nil
}

// subtotals adds the summary figures a reader actually looks at.
//
// Cost of sales is identified by account role rather than by code range: this
// product seeds a chart where the COGS account carries the `cogs` role, and a
// code range would break the first time a client renumbered their chart.
func (s *Service) subtotals(out *Consolidated, balanceSheet bool) {
	if balanceSheet {
		assets, liabilities, equity := decimal.Zero, decimal.Zero, decimal.Zero
		for _, l := range out.Lines {
			v, _ := decimal.NewFromString(l.Amount)
			switch l.Type {
			case "asset":
				assets = assets.Add(v)
			case "liability":
				liabilities = liabilities.Add(v)
			case "equity":
				equity = equity.Add(v)
			}
		}
		out.Assets = assets.StringFixed(2)
		out.Liabilities = liabilities.StringFixed(2)
		out.Equity = equity.StringFixed(2)
		// Reported rather than asserted, for the reason the single-company
		// trial balance gives: a statement whose purpose is to reveal an
		// imbalance should show it rather than refuse to render.
		//
		// A consolidated balance sheet at less than 100% ownership does not
		// balance, and correctly so — the missing side is the minority
		// interest this product does not compute. See the package note.
		balanced := assets.Sub(liabilities.Add(equity)).Abs().
			LessThan(decimal.NewFromFloat(0.01))
		out.Balanced = &balanced
		return
	}

	revenue, cogs, expenses := decimal.Zero, decimal.Zero, decimal.Zero
	for _, l := range out.Lines {
		v, _ := decimal.NewFromString(l.Amount)
		switch {
		case l.Type == "revenue":
			revenue = revenue.Add(v)
		case l.Type == "expense" && reports.IsCostOfSales(l.Code, l.Name):
			cogs = cogs.Add(v)
		case l.Type == "expense":
			expenses = expenses.Add(v)
		}
	}
	out.Revenue = revenue.StringFixed(2)
	out.CostOfSales = cogs.StringFixed(2)
	out.GrossProfit = revenue.Sub(cogs).StringFixed(2)
	out.Expenses = expenses.StringFixed(2)
	out.NetProfit = revenue.Sub(cogs).Sub(expenses).StringFixed(2)
}

// sortStrings orders account codes.
//
// Lexicographic, which is right for zero-padded numeric codes and is what the
// single-company statements already do with `ORDER BY a.code`.
func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
