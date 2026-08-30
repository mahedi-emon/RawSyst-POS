//go:build integration

// C10's year-end closing routine.
//
//	"Year-end closing routine: verify trial balance, post adjusting entries,
//	 close revenue and expense accounts into Retained Earnings, roll balances
//	 forward, generate the closing financial statement pack, and lock the year."
//
// The last test in this file is the one that matters most. A year closed twice
// posts the profit into Retained Earnings twice — and that error balances
// perfectly, survives every check this product makes, and is found a year later
// by an accountant looking at an opening position that is wrong by exactly one
// year's profit.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// "close revenue and expense accounts into Retained Earnings"
//
// A year with a sale in it: revenue and cost of sales both hold a balance, the
// close empties both, and the difference lands in Retained Earnings.
func TestClosingAYearEmptiesRevenueAndExpenseIntoRetainedEarnings(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)
	f.postResult(t, h, "4100", "5100", "1000.00", "600.00")
	f.closeEveryMonth(t, h)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/accounting/year-end"),
		f.token, map[string]any{"fiscal_year": 2026})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close the year: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	if body["profit_to_retained_earnings"] != "400.00" {
		t.Errorf("1000 of revenue less 600 of cost is 400 of profit, not %v",
			body["profit_to_retained_earnings"])
	}

	// Both period accounts are now nil, which is what closing them means.
	for _, code := range []string{"4100", "5100"} {
		if got := f.balance(t, h, code); !got.IsZero() {
			t.Errorf("%s still holds %s after the year was closed; a period "+
				"account starts the new year at nothing", code, got.StringFixed(2))
		}
	}
	// Credit-normal, so a profit reads negative in debit-less-credit terms.
	if got := f.balance(t, h, "3200"); !got.Equal(decimal.RequireFromString("-400")) {
		t.Errorf("Retained Earnings should hold the 400 of profit as a credit, "+
			"not %s", got.StringFixed(2))
	}
}

// A balance sheet account is not touched. Cash does not reset in January.
func TestClosingAYearLeavesTheBalanceSheetAlone(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)
	f.postResult(t, h, "4100", "5100", "1000.00", "600.00")

	before := f.balance(t, h, "1100")
	f.closeEveryMonth(t, h)
	h.do(t, http.MethodPost, f.path("/api/v1/accounting/year-end"),
		f.token, map[string]any{"fiscal_year": 2026})

	if got := f.balance(t, h, "1100"); !got.Equal(before) {
		t.Fatalf("Cash moved from %s to %s across a year-end. Rolling balances "+
			"forward means leaving them alone, not resetting them.",
			before.StringFixed(2), got.StringFixed(2))
	}
}

// The year is locked, and a locked month is not reopened by the ordinary route.
func TestClosingAYearLocksItBeyondReopening(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)
	f.postResult(t, h, "4100", "5100", "100.00", "40.00")
	f.closeEveryMonth(t, h)

	h.do(t, http.MethodPost, f.path("/api/v1/accounting/year-end"),
		f.token, map[string]any{"fiscal_year": 2026})

	january := f.periodNumbered(t, h, 1)
	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/accounting/periods/"+january+"/reopen"), f.token,
		map[string]any{"reason": "We would like to change last year's figures."})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a month in a closed year should refuse reopening, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// A year with months still open cannot be closed.
func TestAYearWithOpenMonthsCannotBeClosed(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/accounting/year-end"),
		f.token, map[string]any{"fiscal_year": 2026})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("closing a year with open months should conflict, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// The one that matters most.
func TestClosingAYearTwiceDoesNotDoubleTheProfit(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)
	f.postResult(t, h, "4100", "5100", "900.00", "300.00")
	f.closeEveryMonth(t, h)

	first := h.do(t, http.MethodPost, f.path("/api/v1/accounting/year-end"),
		f.token, map[string]any{"fiscal_year": 2026})
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first close: %s", readBody(t, first))
	}
	after := f.balance(t, h, "3200")

	second := h.do(t, http.MethodPost, f.path("/api/v1/accounting/year-end"),
		f.token, map[string]any{"fiscal_year": 2026})
	if second.StatusCode != http.StatusOK {
		t.Fatalf("a second close should report the first, got %d — %s",
			second.StatusCode, readBody(t, second))
	}
	if decodeJSON(t, second)["already_closed"] != true {
		t.Error("the second close should say the year was already closed")
	}
	if got := f.balance(t, h, "3200"); !got.Equal(after) {
		t.Fatalf("Retained Earnings moved from %s to %s on a second close. A "+
			"doubled year-end entry balances perfectly and is found a year "+
			"later by an accountant.", after.StringFixed(2), got.StringFixed(2))
	}
}

// --- helpers --------------------------------------------------------------

// postResult puts a year of trading into the books: a sale and its cost, both
// dated inside the year, so there is something to close.
func (f *calendarFixture) postResult(
	t *testing.T, h *harness, revenueCode, expenseCode, revenue, expense string,
) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		var periodID, revenueID, expenseID, cashID uuid.UUID
		if e := tx.QueryRow(t.Context(), `
			SELECT id FROM fiscal_period
			WHERE company_id = $1 AND fiscal_year = 2026 AND period_no = 6`,
			f.companyID).Scan(&periodID); e != nil {
			return e
		}
		for _, m := range []struct {
			code string
			into *uuid.UUID
		}{
			{revenueCode, &revenueID}, {expenseCode, &expenseID}, {"1100", &cashID},
		} {
			if e := tx.QueryRow(t.Context(),
				`SELECT id FROM account WHERE company_id = $1 AND code = $2`,
				f.companyID, m.code).Scan(m.into); e != nil {
				return e
			}
		}

		var entryID uuid.UUID
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
			VALUES ($1,$2,$3,claim_entry_no($2),'2026-06-15','test_trading')
			RETURNING id`,
			f.tenantID, f.companyID, periodID).Scan(&entryID); e != nil {
			return e
		}

		// Dr Cash / Cr Revenue, then Dr Cost of sales / Cr Cash. Balanced, and
		// it leaves cash holding the profit — which is what a year of trading
		// looks like on a very small scale.
		for i, l := range []struct {
			account       uuid.UUID
			debit, credit string
		}{
			{cashID, revenue, "0"},
			{revenueID, "0", revenue},
			{expenseID, expense, "0"},
			{cashID, "0", expense},
		} {
			if _, e := tx.Exec(t.Context(), `
				INSERT INTO journal_line
				  (tenant_id, entry_id, line_no, account_id, currency,
				   debit, credit, base_debit, base_credit)
				VALUES ($1,$2,$3,$4,'SAR',$5,$6,$5,$6)`,
				f.tenantID, entryID, i+1, l.account, l.debit, l.credit); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("post a year of trading: %v", err)
	}
}

// closeEveryMonth closes 2026 from the front, the way a person would.
func (f *calendarFixture) closeEveryMonth(t *testing.T, h *harness) {
	t.Helper()
	for n := 1; n <= 12; n++ {
		id := f.periodNumbered(t, h, n)
		resp := h.do(t, http.MethodPost,
			f.path("/api/v1/accounting/periods/"+id+"/close"), f.token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("close month %d: status %d — %s",
				n, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
}

func (f *calendarFixture) balance(
	t *testing.T, h *harness, code string,
) decimal.Decimal {
	t.Helper()
	var d decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
			FROM journal_line l
			JOIN account a ON a.id = l.account_id
			WHERE a.company_id = $1 AND a.code = $2`,
			f.companyID, code).Scan(&d)
	}); err != nil {
		t.Fatalf("balance of %s: %v", code, err)
	}
	return d
}
