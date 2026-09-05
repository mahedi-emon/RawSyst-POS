// Taking a report away as a spreadsheet.
//
// `report.export` has been seeded on the Owner and Accountant roles since the
// permissions were written, and guarded no route: the verb existed, the roles
// held it, and there was nothing to hold. An owner who wanted the day's sales
// in a spreadsheet — to send to an accountant, to reconcile against a bank
// statement, to keep — could read them on a screen and retype them.
//
// # The exported numbers are the screen's numbers
//
// Every export here calls the SAME service method the screen calls and formats
// what comes back. It does not re-query. An export with its own SQL is an
// export that disagrees with the page it was taken from, eventually and
// silently, and the person who finds out is the one reconciling to a bank.
//
// That also means every guarantee the screen already has comes along unchanged:
// the tenant scoping, the company filter, the date handling and the base
// currency are the report's, not this file's.
//
// # Why CSV, and why the BOM
//
// CSV because it opens in whatever the reader already has. The UTF-8 byte order
// mark is there because Excel on Windows otherwise reads the file as the system
// codepage and turns every Arabic account name into mojibake — the same reason
// internal/portability writes one.
package reports

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// ExportQuery is what the screen was showing when the reader asked for it.
type ExportQuery struct {
	// On is the day for a daily report, and the as-of date for a statement.
	On time.Time
	// From and To bound a period report. Zero means "use On".
	From, To time.Time
	// Filter is the drill-down the screen had applied — an account for
	// expenses, a stock filter for stock.
	Filter string
}

// ExportKinds are the reports that can be taken away, and what each is called.
//
// A fixed list rather than anything the caller names: an export kind reaches a
// service method, and a free-text one would be a way to ask for a method that
// was never meant to be exported.
var ExportKinds = map[string]string{
	"sales":           "Sales",
	"expenses":        "Expenses",
	"stock":           "Stock",
	"trial-balance":   "Trial balance",
	"profit-and-loss": "Profit and loss",
	"balance-sheet":   "Balance sheet",
	// Both of these are screens a person reads and, until now, the only two
	// statements in the product that could not be taken away. The tax return
	// especially: it is what somebody sits down with before filing.
	"cash-flow":  "Cash flow",
	"vat-return": "Tax return",
}

// FilenameFor is what the browser should save it as.
func FilenameFor(kind string, q ExportQuery) string {
	on := q.On
	if on.IsZero() {
		on = time.Now().UTC()
	}
	return fmt.Sprintf("rawsyst-%s-%s.csv", kind, on.Format("2006-01-02"))
}

// ExportCSV writes one report to w.
func (s *Service) ExportCSV(
	ctx context.Context, scope Scope, kind string, q ExportQuery, w io.Writer,
) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if _, ok := ExportKinds[kind]; !ok {
		names := make([]string, 0, len(ExportKinds))
		for k := range ExportKinds {
			names = append(names, k)
		}
		return errs.Newf(errs.CodeInvalidInput,
			"There is no %q report to export. The ones there are: %s.",
			kind, strings.Join(names, ", "))
	}

	// Excel on Windows reads a file without this as the system codepage, which
	// turns every Arabic account name into mojibake.
	if _, err := w.Write([]byte("\ufeff")); err != nil {
		return err
	}
	out := csv.NewWriter(w)
	defer out.Flush()

	var err error
	switch kind {
	case "sales":
		err = s.exportSales(ctx, scope, q, out)
	case "expenses":
		err = s.exportExpenses(ctx, scope, q, out)
	case "stock":
		err = s.exportStock(ctx, scope, q, out)
	case "trial-balance":
		err = s.exportTrialBalance(ctx, scope, q, out)
	case "profit-and-loss":
		err = s.exportProfitAndLoss(ctx, scope, q, out)
	case "balance-sheet":
		err = s.exportBalanceSheet(ctx, scope, q, out)
	case "cash-flow":
		err = s.exportCashFlow(ctx, scope, q, out)
	case "vat-return":
		err = s.exportVATReturn(ctx, scope, q, out)
	}
	if err != nil {
		return err
	}
	out.Flush()
	return out.Error()
}

// header writes the two lines every export opens with: what this is, and what
// currency the figures are in.
//
// A column of money with no currency on it is a page of numbers, and this
// product sells into three markets, so the reader cannot infer it.
func header(out *csv.Writer, title, currency, period string) error {
	if err := out.Write([]string{title}); err != nil {
		return err
	}
	if err := out.Write([]string{"Period", period}); err != nil {
		return err
	}
	if err := out.Write([]string{"Currency", currency}); err != nil {
		return err
	}
	return out.Write(nil)
}

func (s *Service) exportSales(
	ctx context.Context, scope Scope, q ExportQuery, out *csv.Writer,
) error {
	// The page limit the screen uses is a screen concern. An export is taken to
	// be complete, so it asks for the largest page the service allows.
	d, err := s.SalesFor(ctx, scope, q.On, 500)
	if err != nil {
		return err
	}
	if err := header(out, "Sales", d.BaseCurrency, d.Date); err != nil {
		return err
	}
	if err := out.Write([]string{"Number", "Type", "State", "Issued at",
		"Total", "Tax", "Tenders", "Lines"}); err != nil {
		return err
	}
	for _, r := range d.Rows {
		if err := out.Write([]string{
			r.HumanNumber, r.DocType, r.State, r.IssuedAt,
			r.TotalInclusive, r.TaxTotal, r.Tenders,
			fmt.Sprint(r.LineCount),
		}); err != nil {
			return err
		}
	}

	// Totals over the whole day, not over the rows above — the same figures the
	// screen shows under the list, and the reason a paged export still tells
	// the reader what the day came to.
	if err := out.Write(nil); err != nil {
		return err
	}
	for _, t := range [][2]string{
		{"Sales", d.SalesTotal},
		{"Refunds", d.RefundTotal},
		{"Net", d.NetTotal},
		{"Tax", d.TaxTotal},
		{"Invoices", fmt.Sprint(d.InvoiceCount)},
		{"Refund count", fmt.Sprint(d.RefundCount)},
	} {
		if err := out.Write([]string{t[0], t[1]}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) exportExpenses(
	ctx context.Context, scope Scope, q ExportQuery, out *csv.Writer,
) error {
	var account *uuid.UUID
	if f := strings.TrimSpace(q.Filter); f != "" {
		id, e := uuid.Parse(f)
		if e != nil {
			return errs.Validation("That account filter is not an account.").
				WithField("account_id", "The id of the account to narrow to.")
		}
		account = &id
	}
	d, err := s.ExpensesFor(ctx, scope, q.On, account)
	if err != nil {
		return err
	}
	if err := header(out, "Expenses", d.BaseCurrency, d.Date); err != nil {
		return err
	}
	if err := out.Write([]string{"Entry", "Date", "Code", "Account", "Memo",
		"Amount"}); err != nil {
		return err
	}
	for _, e := range d.Entries {
		if err := out.Write([]string{
			e.EntryNo, e.Date, e.Code, e.Account, e.Memo, e.Amount,
		}); err != nil {
			return err
		}
	}
	if err := out.Write(nil); err != nil {
		return err
	}
	return out.Write([]string{"Total", d.Total})
}

func (s *Service) exportStock(
	ctx context.Context, scope Scope, q ExportQuery, out *csv.Writer,
) error {
	// StockFor is the low/out-of-stock report, not a full listing, and the
	// screen always names which. An export asked for without one gets the
	// same view the screen opens on rather than a refusal.
	filter := strings.TrimSpace(q.Filter)
	if filter == "" {
		filter = "low"
	}
	d, err := s.StockFor(ctx, scope, filter)
	if err != nil {
		return err
	}
	if err := header(out, "Stock", d.BaseCurrency, d.Filter); err != nil {
		return err
	}
	if err := out.Write([]string{"SKU", "Name", "Barcode", "On hand",
		"Reorder level", "Value"}); err != nil {
		return err
	}
	for _, r := range d.Rows {
		if err := out.Write([]string{
			r.SKU, r.Name, r.Barcode, r.OnHand, r.ReorderLevel, r.Value,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) exportTrialBalance(
	ctx context.Context, scope Scope, q ExportQuery, out *csv.Writer,
) error {
	d, err := s.TrialBalanceAt(ctx, scope, q.On)
	if err != nil {
		return err
	}
	if err := header(out, "Trial balance", d.BaseCurrency,
		"as at "+d.AsOf); err != nil {
		return err
	}
	if err := out.Write([]string{"Code", "Account", "Type", "Debit",
		"Credit"}); err != nil {
		return err
	}
	for _, r := range d.Rows {
		if err := out.Write([]string{
			r.Code, r.Name, r.Type, r.Debit, r.Credit,
		}); err != nil {
			return err
		}
	}
	if err := out.Write(nil); err != nil {
		return err
	}
	if err := out.Write([]string{"", "Total", "", d.TotalDebit,
		d.TotalCredit}); err != nil {
		return err
	}
	// Carried into the export because it is the reason to trust the rest of
	// the file, and a reader who cannot see it has to take the totals on faith.
	return out.Write([]string{"", "Difference", "", d.Difference})
}

func (s *Service) exportProfitAndLoss(
	ctx context.Context, scope Scope, q ExportQuery, out *csv.Writer,
) error {
	from, to := q.period()
	d, err := s.ProfitAndLossFor(ctx, scope, from, to)
	if err != nil {
		return err
	}
	if err := header(out, "Profit and loss", d.BaseCurrency,
		d.From+" to "+d.To); err != nil {
		return err
	}
	if err := out.Write([]string{"Section", "Code", "Account",
		"Amount"}); err != nil {
		return err
	}
	for _, sec := range []struct {
		name  string
		lines []StatementLine
	}{
		{"Revenue", d.Revenue},
		{"Cost of sales", d.CostOfSales},
		{"Expenses", d.Expenses},
	} {
		for _, l := range sec.lines {
			if err := out.Write([]string{
				sec.name, l.Code, l.Name, l.Amount,
			}); err != nil {
				return err
			}
		}
	}
	if err := out.Write(nil); err != nil {
		return err
	}
	for _, t := range [][2]string{
		{"Revenue", d.RevenueTotal},
		{"Cost of sales", d.CostOfSalesTotal},
		{"Gross profit", d.GrossProfit},
		{"Expenses", d.ExpensesTotal},
		{"Net profit", d.NetProfit},
	} {
		if err := out.Write([]string{t[0], "", "", t[1]}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) exportBalanceSheet(
	ctx context.Context, scope Scope, q ExportQuery, out *csv.Writer,
) error {
	d, err := s.BalanceSheetAt(ctx, scope, q.On)
	if err != nil {
		return err
	}
	if err := header(out, "Balance sheet", d.BaseCurrency,
		"as at "+d.AsOf); err != nil {
		return err
	}
	if err := out.Write([]string{"Section", "Code", "Account",
		"Amount"}); err != nil {
		return err
	}
	for _, sec := range []struct {
		name  string
		lines []StatementLine
	}{
		{"Assets", d.Assets},
		{"Liabilities", d.Liabilities},
		{"Equity", d.Equity},
	} {
		for _, l := range sec.lines {
			if err := out.Write([]string{
				sec.name, l.Code, l.Name, l.Amount,
			}); err != nil {
				return err
			}
		}
	}
	if err := out.Write(nil); err != nil {
		return err
	}
	for _, t := range [][2]string{
		{"Total assets", d.AssetsTotal},
		{"Total liabilities", d.LiabilitiesTotal},
		{"Total equity", d.EquityTotal},
		{"Current earnings", d.CurrentEarnings},
		{"Equity and liabilities", d.EquityAndLiabilities},
		// The proof the sheet balances, carried into the file for the same
		// reason the trial balance carries its difference.
		{"Difference", d.Difference},
	} {
		if err := out.Write([]string{t[0], "", "", t[1]}); err != nil {
			return err
		}
	}
	return nil
}

// period gives a P&L its window, defaulting to the month containing On.
func (q ExportQuery) period() (time.Time, time.Time) {
	if !q.From.IsZero() && !q.To.IsZero() {
		return q.From, q.To
	}
	on := q.On
	if on.IsZero() {
		on = time.Now().UTC()
	}
	from := time.Date(on.Year(), on.Month(), 1, 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(0, 1, -1)
}

func (s *Service) exportCashFlow(
	ctx context.Context, scope Scope, q ExportQuery, out *csv.Writer,
) error {
	from, to := q.period()
	d, err := s.CashFlowFor(ctx, scope, from, to)
	if err != nil {
		return err
	}
	if err := header(out, "Cash flow", d.BaseCurrency,
		d.From+" to "+d.To); err != nil {
		return err
	}
	// Said in the file as well as on the screen. A statement labelled only
	// "Cash flow" is read as an IAS 7 indirect one by anybody who opens it in
	// a spreadsheet, and this is the direct method.
	if err := out.Write([]string{"Method", d.Method}); err != nil {
		return err
	}
	if err := out.Write(nil); err != nil {
		return err
	}
	if err := out.Write([]string{"Direction", "Code", "Account",
		"Amount"}); err != nil {
		return err
	}
	for _, sec := range []struct {
		name  string
		lines []CashFlowLine
	}{
		{"In", d.In},
		{"Out", d.Out},
	} {
		for _, l := range sec.lines {
			if err := out.Write([]string{
				sec.name, l.Code, l.Name, l.Amount,
			}); err != nil {
				return err
			}
		}
	}
	if err := out.Write(nil); err != nil {
		return err
	}
	for _, t := range [][2]string{
		{"Opening", d.Opening},
		{"Net movement", d.NetTotal},
		{"Closing", d.Closing},
	} {
		if err := out.Write([]string{t[0], "", "", t[1]}); err != nil {
			return err
		}
	}
	return nil
}

// exportVATReturn writes a prepared return, caveats first.
//
// The Outstanding lines go ABOVE the figures rather than in a footer. A
// spreadsheet is scrolled, printed and forwarded; a caveat at the bottom of one
// is a caveat somebody files without reading, and this file exists to be filed
// from.
//
// It also states, in the file, that nothing here has been submitted. A CSV
// called "Tax return" that says nothing else invites exactly that assumption.
func (s *Service) exportVATReturn(
	ctx context.Context, scope Scope, q ExportQuery, out *csv.Writer,
) error {
	if s.vat == nil {
		return errs.New(errs.CodeInvalidInput,
			"This build cannot prepare a tax return, so there is nothing to "+
				"export.")
	}
	from, to := q.period()
	d, err := s.vat.PrepareReturn(ctx, scope.TenantID, scope.CompanyID, from, to)
	if err != nil {
		return err
	}

	if err := header(out, "Tax return", d.BaseCurrency, d.From+" to "+d.To); err != nil {
		return err
	}
	if err := out.Write([]string{"Country", d.Country}); err != nil {
		return err
	}
	if err := out.Write([]string{"Model", d.Model}); err != nil {
		return err
	}
	if err := out.Write([]string{"Filed", boolWord(d.Filed)}); err != nil {
		return err
	}
	if err := out.Write([]string{"Agrees with the ledger",
		boolWord(d.Reconciled)}); err != nil {
		return err
	}

	if len(d.Outstanding) > 0 {
		if err := out.Write(nil); err != nil {
			return err
		}
		if err := out.Write([]string{"Not included, and why"}); err != nil {
			return err
		}
		for _, why := range d.Outstanding {
			if err := out.Write([]string{"", why}); err != nil {
				return err
			}
		}
	}

	if err := out.Write(nil); err != nil {
		return err
	}
	if err := out.Write([]string{"Treatment", "Invoices", "Net",
		"Tax"}); err != nil {
		return err
	}
	for _, l := range d.Supplies {
		if err := out.Write([]string{
			l.Treatment, strconv.FormatInt(l.InvoiceCount, 10),
			l.NetAmount, l.TaxAmount,
		}); err != nil {
			return err
		}
	}

	if err := out.Write(nil); err != nil {
		return err
	}
	totals := [][2]string{
		{"Total net", d.TotalNet},
		{"Output tax", d.OutputTaxTotal},
	}
	// A sales tax has no input side at all, so an empty row would be wrong
	// rather than merely blank -- it would state a recoverable figure of zero.
	if d.InputTaxTotal != nil {
		totals = append(totals, [2]string{"Input tax", *d.InputTaxTotal})
	}
	if d.NetPayable != nil {
		totals = append(totals, [2]string{"Net payable", *d.NetPayable})
	}
	totals = append(totals,
		[2]string{"Output tax in the ledger", d.LedgerOutputTax},
		[2]string{"Difference", d.Difference},
	)
	for _, t := range totals {
		if err := out.Write([]string{t[0], "", "", t[1]}); err != nil {
			return err
		}
	}
	return nil
}

func boolWord(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}
