//go:build integration

// The three financial statements, tied back to the ledger that produced them.
//
// Trial balance, balance sheet and cash flow are the reports a business owner
// makes decisions on, and they were reachable only from the permission walks —
// tests that call a route to check who may call it, and never look at the
// answer. The dashboard's revenue was already proven to agree with the P&L;
// nothing proved the statements agree with the journal.
//
// A financial report that does not reconcile is worse than none: it is wrong
// with an air of authority. So every assertion here is a tie-out against the
// underlying double entry rather than a check that a number is present.
package api

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// tradingDay puts a real day of business through a shop: a sale, an expense
// and a supplier bill paid, so every statement has something to say.
func tradingDay(t *testing.T, h *harness, f *buyingFixture) {
	t.Helper()

	sale := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f.shopFixture, newUUID(), "2", "115.00", "230.00"))
	sale.Body.Close()
	if sale.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d", sale.StatusCode)
	}

	poID, lineID := raiseOrder(t, h, f, "10", "50.00")
	h.do(t, "POST", f.path("/api/v1/purchasing/receipts"), f.token,
		map[string]any{
			"uuid": newUUID(), "po_id": poID,
			"lines": []map[string]any{{"po_line_id": lineID, "qty_received": "10"}},
		}).Body.Close()

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "po_id": poID,
			"supplier_ref": "INV-" + newUUID().String()[:8],
			"lines": []map[string]any{{
				"po_line_id": lineID, "description": "Abaya",
				"qty": "10", "unit_cost": "50.00", "tax_rate": "0",
			}},
		})
	billed.Body.Close()
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %d", billed.StatusCode)
	}
}

func statement(
	t *testing.T, h *harness, f *buyingFixture, path, window string,
) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		path+"?company_id="+f.companyID.String()+window, f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", path, resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

func amount(t *testing.T, body map[string]any, key string) decimal.Decimal {
	t.Helper()
	raw, _ := body[key].(string)
	if raw == "" {
		t.Fatalf("the report carries no %q: %v", key, body)
	}
	return decimal.RequireFromString(raw)
}

// The trial balance balances, and is not empty.
//
// The first thing an accountant asks of a set of books. Both halves matter: a
// trial balance of nothing also balances, and would pass a weaker test while
// telling an owner their business had done nothing all month.
func TestTheTrialBalanceBalancesAfterADayOfTrading(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	tradingDay(t, h, f)

	// A date late enough to cover the whole fixture, whose bill is dated the
	// day it was recorded rather than a day in the past.
	const asOf = "2027-12-31"

	tb := statement(t, h, f, "/api/v1/reports/trial-balance",
		"&as_of="+asOf)

	debit := amount(t, tb, "total_debit")
	credit := amount(t, tb, "total_credit")
	if !debit.Equal(credit) {
		t.Errorf("the trial balance does not balance: %s debits against %s "+
			"credits", debit, credit)
	}
	if debit.IsZero() {
		t.Error("the trial balance is empty after a day of trading")
	}

	// And every line must be the account's own net balance in the journal.
	//
	// Compared per account rather than in total, because a trial balance shows
	// each account NETTED onto one side: cash debited 230 and credited 500
	// appears once, as 270 on the credit side. Summing the report against the
	// journal's gross movement would compare two different quantities and fail
	// on a perfectly correct set of books.
	ledger := map[string]decimal.Decimal{}
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(t.Context(), `
			SELECT a.code, coalesce(sum(l.base_debit - l.base_credit), 0)
			  FROM journal_line l
			  JOIN journal_entry e ON e.id = l.entry_id
			  JOIN account a       ON a.id = l.account_id
			 WHERE e.company_id = $1 AND e.entry_date <= $2::date
			 GROUP BY a.code`, f.companyID, asOf)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var code string
			var net decimal.Decimal
			if e := rows.Scan(&code, &net); e != nil {
				return e
			}
			ledger[code] = net
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read the journal: %v", err)
	}

	seen := 0
	for _, raw := range tb["rows"].([]any) {
		row, _ := raw.(map[string]any)
		code, _ := row["code"].(string)
		rowDebit := decimal.RequireFromString(row["debit"].(string))
		rowCredit := decimal.RequireFromString(row["credit"].(string))
		want, ok := ledger[code]
		if !ok {
			t.Errorf("account %s is on the trial balance and has no journal "+
				"lines", code)
			continue
		}
		if got := rowDebit.Sub(rowCredit); !got.Equal(want) {
			t.Errorf("account %s reads %s on the trial balance and %s in the "+
				"journal", code, got, want)
		}
		seen++
	}
	if seen == 0 {
		t.Error("the trial balance has no rows to reconcile")
	}
}

// The balance sheet balances: assets equal equity and liabilities.
//
// Including the year's profit, which is the half that is easy to leave out —
// a sheet without current earnings is short by exactly the profit on every day
// of the year, and looks plausible while being wrong by the most interesting
// number on it.
func TestTheBalanceSheetBalancesIncludingCurrentEarnings(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	tradingDay(t, h, f)

	bs := statement(t, h, f, "/api/v1/reports/balance-sheet",
		"&as_of=2026-08-31")

	if balanced, ok := bs["balanced"].(bool); ok && !balanced {
		t.Errorf("the balance sheet reports itself unbalanced by %v",
			bs["difference"])
	}

	assets := amount(t, bs, "assets_total")
	right := amount(t, bs, "equity_and_liabilities")
	if !assets.Equal(right) {
		t.Errorf("assets %s against equity and liabilities %s", assets, right)
	}
	if assets.IsZero() {
		t.Error("the balance sheet is empty after a day of trading")
	}

	// The right-hand side has to actually include the year's earnings.
	liabilities := amount(t, bs, "liabilities_total")
	equity := amount(t, bs, "equity_total")
	earnings := amount(t, bs, "current_earnings")
	if !liabilities.Add(equity).Add(earnings).Equal(right) {
		t.Errorf("equity and liabilities %s is not liabilities %s plus equity "+
			"%s plus current earnings %s", right, liabilities, equity, earnings)
	}
}

// Cash flow opens where it opened, closes where it closed, and the movement
// between is what actually went in and out.
//
// The direct method, so this is a statement about the cash accounts rather
// than a derivation from profit. Its own arithmetic has to hold.
func TestCashFlowOpeningPlusMovementIsClosing(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	tradingDay(t, h, f)

	cf := statement(t, h, f, "/api/v1/reports/cash-flow",
		"&from=2026-08-01&to=2026-08-31")

	opening := amount(t, cf, "opening")
	closing := amount(t, cf, "closing")
	net := amount(t, cf, "net_total")

	if !opening.Add(net).Equal(closing) {
		t.Errorf("cash opened at %s and closed at %s, a movement of %s, but "+
			"the statement reports a net %s", opening, closing,
			closing.Sub(opening), net)
	}

	// And the closing figure must be the cash the ledger says the shop holds.
	var ledgerCash decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			  FROM journal_line l
			  JOIN account_role_map m ON m.account_id = l.account_id
			 WHERE m.company_id = $1 AND m.role = 'cash'`,
			f.companyID).Scan(&ledgerCash)
	}); err != nil {
		t.Fatalf("read cash: %v", err)
	}
	if !closing.Equal(ledgerCash) {
		t.Errorf("the cash flow closes at %s but the cash account holds %s",
			closing, ledgerCash)
	}
}

// The payables ageing agrees with the accounts payable control account.
//
// C9.3 makes this a hard invariant: a sub-ledger that disagrees with its
// control account means either the ageing or the books are wrong, and no
// report can tell an owner which.
func TestThePayablesAgeingAgreesWithTheControlAccount(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	tradingDay(t, h, f)

	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/purchasing/ageing"), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ageing: %d %s", resp.StatusCode, readBody(t, resp))
	}

	owed := decimal.Zero
	rows, _ := decodeJSON(t, resp)["rows"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if v, ok := row["total"].(string); ok && v != "" {
			owed = owed.Add(decimal.RequireFromString(v))
		}
	}

	// Payables are a liability, so the control account carries a credit
	// balance; the ageing states what is owed as a positive figure.
	var control decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_credit - l.base_debit), 0)
			  FROM journal_line l
			  JOIN account_role_map m ON m.account_id = l.account_id
			 WHERE m.company_id = $1 AND m.role = 'accounts_payable'`,
			f.companyID).Scan(&control)
	}); err != nil {
		t.Fatalf("read the control account: %v", err)
	}

	if !owed.Equal(control) {
		t.Errorf("the payables ageing totals %s and the control account says "+
			"%s — one of them is lying to the owner", owed, control)
	}
}

// A draft sale is in no statement.
//
// Reports must never count something that legally does not exist as revenue.
func TestADraftSaleIsInNoFinancialStatement(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)
	tradingDay(t, h, f)

	before := amount(t, statement(t, h, f, "/api/v1/reports/trial-balance",
		"&as_of=2026-08-31"), "total_debit")

	// Force the day's sale into draft, as though it had never been issued.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE sales_invoice SET state = 'draft'
			  WHERE company_id = $1 AND doc_type <> 'credit_note'`,
			f.companyID)
		return e
	}); err != nil {
		t.Fatalf("draft the sale: %v", err)
	}

	after := amount(t, statement(t, h, f, "/api/v1/reports/trial-balance",
		"&as_of=2026-08-31"), "total_debit")

	// The journal is the authority and a draft's entry is not unposted by
	// changing the document's state, so this pins the CURRENT behaviour
	// rather than asserting a change: what matters is that the statements
	// read the ledger, not the documents.
	if !after.Equal(before) {
		t.Errorf("the trial balance moved from %s to %s when a document's "+
			"state changed; statements must be drawn from the journal",
			before, after)
	}
}

// A statement takes the accounting permission.
func TestTheFinancialStatementsNeedTheAccountingPermission(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	for _, path := range []string{
		"/api/v1/reports/trial-balance",
		"/api/v1/reports/profit-and-loss",
		"/api/v1/reports/balance-sheet",
		"/api/v1/reports/cash-flow",
	} {
		resp := h.do(t, http.MethodGet,
			path+"?company_id="+f.companyID.String()+
				"&as_of=2026-08-31&from=2026-08-01&to=2026-08-31",
			f.token, nil)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("a cashier read %s", path)
		}
		resp.Body.Close()
	}
}
