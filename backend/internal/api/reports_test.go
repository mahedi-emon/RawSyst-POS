//go:build integration

// The financial statements, drawn from a real day's trading.
//
// QA gate M1 asks that the trial balance always balances and that sub-ledgers
// tie to their control accounts. These tests go further and check the two
// things a statement gets wrong most often: that a Balance Sheet includes
// profit that has not yet been closed into retained earnings, and that a
// Profit and Loss reports a PERIOD rather than everything up to a date.
package api

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
)

func day(d int) time.Time {
	return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC)
}

func (f *shopFixture) scope() reports.Scope {
	return reports.Scope{TenantID: f.tenantID, CompanyID: f.companyID}
}

// tradeOneDay rings up two cash sales, so every statement has something real
// behind it rather than hand-written journal rows.
func (h *harness) tradeOneDay(t *testing.T, f *shopFixture) {
	t.Helper()
	for _, qty := range []string{"1", "2"} {
		paid := "115.00"
		if qty == "2" {
			paid = "230.00"
		}
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), qty, "115.00", paid))
		if resp.StatusCode != 201 {
			t.Fatalf("sale: %s", readBody(t, resp))
		}
	}
}

// The trial balance must balance. If it does not, nothing else on this page
// means anything.
func TestTrialBalanceBalancesAfterTrading(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	h.tradeOneDay(t, f)

	svc := reports.NewService(h.pool)
	tb, err := svc.TrialBalanceAt(t.Context(), f.scope(), day(31))
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}

	if !tb.Balanced {
		t.Fatalf("trial balance is out by %s: debits %s against credits %s",
			tb.Difference, tb.TotalDebit, tb.TotalCredit)
	}
	if len(tb.Rows) == 0 {
		t.Fatal("the trial balance is empty after two sales")
	}

	// Three sales of 115 gross: 300 net revenue and 45 output VAT.
	byCode := map[string]reports.TrialBalanceRow{}
	for _, r := range tb.Rows {
		byCode[r.Code] = r
	}
	if got := byCode["4100"].Credit; got != "300" {
		t.Errorf("revenue credit = %s, want 300", got)
	}
	if got := byCode["2200"].Credit; got != "45" {
		t.Errorf("output VAT credit = %s, want 45", got)
	}
}

// The balance sheet must balance, and it only does if profit earned this year
// is counted as equity. Until the year is closed that profit sits in revenue
// and expense accounts, so a sheet drawn from the equity accounts alone is
// short by exactly the year's profit every day of the year.
func TestBalanceSheetBalancesIncludingCurrentEarnings(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	h.tradeOneDay(t, f)

	svc := reports.NewService(h.pool)
	bs, err := svc.BalanceSheetAt(t.Context(), f.scope(), day(31))
	if err != nil {
		t.Fatalf("balance sheet: %v", err)
	}

	if !bs.Balanced {
		t.Fatalf("balance sheet is out by %s: assets %s against %s",
			bs.Difference, bs.AssetsTotal, bs.EquityAndLiabilities)
	}

	// Three units sold at 60 cost = 180 COGS against 300 net revenue.
	if bs.CurrentEarnings != "120" {
		t.Errorf("current earnings = %s, want 120 (300 revenue less 180 cost)",
			bs.CurrentEarnings)
	}

	// The seeded equity accounts are empty, so without current earnings the
	// sheet would be out by the whole 120. Proving that here is the point.
	if bs.EquityTotal != "0" {
		t.Errorf("equity accounts hold %s; this test assumes they are empty",
			bs.EquityTotal)
	}
}

// A Profit and Loss covers a PERIOD. Asking for a window with no trading in it
// must report nothing, not everything to date.
func TestProfitAndLossIsAPeriodNotACumulativeTotal(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	h.tradeOneDay(t, f) // all sales are dated the 15th

	svc := reports.NewService(h.pool)

	within, err := svc.ProfitAndLossFor(t.Context(), f.scope(), day(1), day(31))
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	if within.RevenueTotal != "300" {
		t.Errorf("revenue = %s, want 300", within.RevenueTotal)
	}
	if within.CostOfSalesTotal != "180" {
		t.Errorf("cost of sales = %s, want 180", within.CostOfSalesTotal)
	}
	if within.GrossProfit != "120" {
		t.Errorf("gross profit = %s, want 120", within.GrossProfit)
	}
	if within.NetProfit != "120" {
		t.Errorf("net profit = %s, want 120", within.NetProfit)
	}

	// A window that ends before the trading day.
	before, err := svc.ProfitAndLossFor(t.Context(), f.scope(), day(1), day(10))
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	if before.RevenueTotal != "0" || before.NetProfit != "0" {
		t.Errorf("a period before any trading reported revenue %s and profit %s; "+
			"the P&L is behaving cumulatively",
			before.RevenueTotal, before.NetProfit)
	}
}

// Gross profit is separated from operating expenses, because whether a shop
// buys and prices well is a different question from whether it spends well.
func TestGrossProfitIsSeparatedFromOperatingExpenses(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	h.tradeOneDay(t, f)
	ctx := t.Context()

	// Rent: an operating expense, below the gross profit line.
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var rentID, cashID, periodID, entryID any
		if e := tx.QueryRow(ctx, `
			INSERT INTO account (tenant_id, company_id, code, name, type)
			VALUES ($1,$2,'6100','Rent','expense') RETURNING id`,
			f.tenantID, f.companyID).Scan(&rentID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT account_id FROM account_role_map
			 WHERE company_id=$1 AND role='cash'`, f.companyID).Scan(&cashID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT id FROM fiscal_period WHERE company_id=$1`, f.companyID).
			Scan(&periodID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
			VALUES ($1,$2,$3,claim_entry_no($2),'2026-08-20','expense')
			RETURNING id`,
			f.tenantID, f.companyID, periodID).Scan(&entryID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,1,$3,'SAR',50,0,50,0)`,
			f.tenantID, entryID, rentID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,2,$3,'SAR',0,50,0,50)`,
			f.tenantID, entryID, cashID)
		return e
	}); err != nil {
		t.Fatalf("post rent: %v", err)
	}

	pl, err := reports.NewService(h.pool).
		ProfitAndLossFor(ctx, f.scope(), day(1), day(31))
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}

	if pl.GrossProfit != "120" {
		t.Errorf("gross profit = %s, want 120; rent must sit below the line",
			pl.GrossProfit)
	}
	if pl.ExpensesTotal != "50" {
		t.Errorf("operating expenses = %s, want 50", pl.ExpensesTotal)
	}
	if pl.NetProfit != "70" {
		t.Errorf("net profit = %s, want 70", pl.NetProfit)
	}

	// And the balance sheet still balances with an expense in play.
	bs, err := reports.NewService(h.pool).BalanceSheetAt(ctx, f.scope(), day(31))
	if err != nil {
		t.Fatalf("balance sheet: %v", err)
	}
	if !bs.Balanced {
		t.Errorf("balance sheet is out by %s once an expense is posted", bs.Difference)
	}
	if bs.CurrentEarnings != "70" {
		t.Errorf("current earnings = %s, want 70", bs.CurrentEarnings)
	}
}

// The cash flow reads what actually moved through cash and bank, and its
// closing balance must agree with the trial balance's cash figure.
func TestCashFlowClosingAgreesWithTheLedger(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	h.tradeOneDay(t, f)

	svc := reports.NewService(h.pool)
	cf, err := svc.CashFlowFor(t.Context(), f.scope(), day(1), day(31))
	if err != nil {
		t.Fatalf("cash flow: %v", err)
	}

	if cf.Method != "direct" {
		t.Errorf("method = %s; the statement must say which method it used",
			cf.Method)
	}

	tb, err := svc.TrialBalanceAt(t.Context(), f.scope(), day(31))
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}

	// Cash: 600 paid out for opening stock, 345 taken in from sales.
	var cashDebit, cashCredit string
	for _, r := range tb.Rows {
		if r.Code == "1100" {
			cashDebit, cashCredit = r.Debit, r.Credit
		}
	}
	if cashDebit == "" {
		t.Fatal("no cash account on the trial balance")
	}
	if cf.Closing != "-255" {
		t.Errorf("closing cash = %s, want -255 (345 in less 600 for stock); "+
			"ledger shows %s debit and %s credit",
			cf.Closing, cashDebit, cashCredit)
	}

	if len(cf.In) == 0 {
		t.Error("no cash inflows reported after two cash sales")
	}
}

// A branch filter must mean the same thing on every statement.
func TestStoreFilterNarrowsEveryStatementConsistently(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	h.tradeOneDay(t, f)

	svc := reports.NewService(h.pool)
	scoped := f.scope()
	scoped.StoreID = &f.storeID

	pl, err := svc.ProfitAndLossFor(t.Context(), scoped, day(1), day(31))
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	// Every sale was rung on this store's till, so the branch P&L equals the
	// company's revenue.
	if pl.RevenueTotal != "300" {
		t.Errorf("branch revenue = %s, want 300", pl.RevenueTotal)
	}

	// A store that did no trading reports nothing rather than everything.
	other := h.seedShop(t, "cashier")
	elsewhere := f.scope()
	elsewhere.StoreID = &other.storeID
	empty, err := svc.ProfitAndLossFor(t.Context(), elsewhere, day(1), day(31))
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	if empty.RevenueTotal != "0" {
		t.Errorf("a branch that did no trading reported revenue of %s",
			empty.RevenueTotal)
	}
}

// One tenant cannot draw another's statements.
func TestOneTenantCannotDrawAnothersStatements(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")
	h.tradeOneDay(t, theirs)

	svc := reports.NewService(h.pool)
	crossed := reports.Scope{TenantID: mine.tenantID, CompanyID: theirs.companyID}

	// Their company, read in my tenant context.
	//
	// This used to return an EMPTY statement: row-level security hid every
	// line, the totals came out at zero, and the test asserted the zero. That
	// was safe and it was a weaker answer than the one given now.
	//
	// Every statement reads the company's base currency first — a statement
	// that does not say which currency its figures are in is a page of numbers
	// — and the company row is hidden by the same policy that hides the lines.
	// So the read fails, with the same "not found" a genuinely missing company
	// gets, which discloses nothing about whether the id exists.
	//
	// All four are checked rather than only the P&L, because the currency read
	// is what refuses and each statement does its own.
	for _, c := range []struct {
		name string
		draw func() error
	}{
		{"profit and loss", func() error {
			_, e := svc.ProfitAndLossFor(t.Context(), crossed, day(1), day(31))
			return e
		}},
		{"balance sheet", func() error {
			_, e := svc.BalanceSheetAt(t.Context(), crossed, day(31))
			return e
		}},
		{"trial balance", func() error {
			_, e := svc.TrialBalanceAt(t.Context(), crossed, day(31))
			return e
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.draw(); err == nil {
				t.Fatalf("one tenant drew another tenant's %s", c.name)
			}
		})
	}
}
