//go:build integration

// A month with an absence in it could not be approved.
//
// `payroll.accrue` version 1 debited the gross and credited social insurance,
// the advance recovery and the net. But a payslip has five deductions, not
// three: `absence_deduction` and `other_deduction` are subtracted to reach the
// net as well, so the entry was short by exactly their sum and Post refused it
// with "This entry does not balance". The wage could not be booked, the run
// could not be paid, and the only way forward was deleting attendance that had
// been recorded correctly.
//
// Every other payroll test pays people who were never away, which is why this
// stood. Found by running a real month against the running server.
//
// 0129 supersedes the rule with a version that credits the absence back to the
// wage expense — a day not worked is pay never earned, not money owed to
// anybody — and books an other deduction to a liability, because that IS money
// the business is holding.
package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// lastMonth is the period these tests run: a whole month, in the past, so
// every day of it can carry attendance.
func lastMonth() (period string, first time.Time) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, -1, 0)
	return start.Format("2006-01"), start
}

// away marks somebody absent for one day of a period, through the route a
// manager uses.
func (h *harness) away(t *testing.T, f *shopFixture, who string, on time.Time) {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/attendance?company_id="+f.companyID.String(), f.token,
		map[string]any{"days": []map[string]any{{
			"employee_id":  who,
			"on_date":      on.Format("2006-01-02"),
			"status":       "absent",
			"hours_worked": "0",
		}}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("record an absent day: %s", readBody(t, resp))
	}
}

// prepared computes a run for a period and returns it.
func (h *harness) prepared(t *testing.T, f *shopFixture, period string) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/payroll?company_id="+f.companyID.String(), f.token,
		map[string]any{"period": period})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("prepare %s: %s", period, readBody(t, resp))
	}
	return decodeJSONFrom(t, resp)
}

func TestAMonthWithAnAbsenceInItCanBeApproved(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	who := h.hire(t, f, "Sometimes Away", "6000.00", nil)
	period, first := lastMonth()
	// The 8th, so the day is inside the month wherever this runs.
	h.away(t, f, who, first.AddDate(0, 0, 7))

	run := h.prepared(t, f, period)

	// The absence has to be ON the slip, or this test proves nothing about the
	// rule: a run with nothing deducted balanced under version 1 as well.
	slips := run["payslips"].([]any)
	docked := decimal.RequireFromString(
		slips[0].(map[string]any)["absence_deduction"].(string))
	if !docked.GreaterThan(decimal.Zero) {
		t.Fatalf("nobody was docked for the absent day, so this run would "+
			"have balanced before the fix too: %v", slips[0])
	}

	// The whole point. Under version 1 this answered 500 with "This entry does
	// not balance ... a difference of <the absence>".
	resp := h.do(t, http.MethodPost,
		"/api/v1/payroll/"+run["id"].(string)+"/approve?company_id="+
			f.companyID.String(), f.token, map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approving a month with one absent day answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func TestTheWageCostIsWhatWasEarnedNotWhatWasScheduled(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	who := h.hire(t, f, "Sometimes Away", "6000.00", nil)
	period, first := lastMonth()
	h.away(t, f, who, first.AddDate(0, 0, 7))

	run := h.prepared(t, f, period)
	runID := run["id"].(string)
	slips := run["payslips"].([]any)
	docked := decimal.RequireFromString(
		slips[0].(map[string]any)["absence_deduction"].(string))
	gross := decimal.RequireFromString(run["gross_total"].(string))

	resp := h.do(t, http.MethodPost,
		"/api/v1/payroll/"+runID+"/approve?company_id="+f.companyID.String(),
		f.token, map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %s", readBody(t, resp))
	}

	// What the ledger says employing this person cost. The absence credits the
	// SAME expense account the gross debits, so the net charge is what was
	// earned — and both figures stay on the entry, which is what lets a payroll
	// register be reconciled to the books line by line.
	var debit, credit decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.debit), 0), coalesce(sum(l.credit), 0)
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			JOIN account_role_map m ON m.account_id = l.account_id
			WHERE e.source_type = 'payroll_run' AND e.source_id = $1::uuid
			  AND m.role = 'expense_salaries'`, runID).Scan(&debit, &credit)
	}); err != nil {
		t.Fatalf("read the wage expense: %v", err)
	}

	if !debit.Equal(gross) {
		t.Errorf("the wage expense was debited %s, not the gross of %s",
			debit, gross)
	}
	if !credit.Equal(docked) {
		t.Errorf("the absence of %s was not credited back to the wage "+
			"expense; that account took %s in credits", docked, credit)
	}
	if net := debit.Sub(credit); !net.Equal(gross.Sub(docked)) {
		t.Errorf("employing somebody cost %s in the books against %s earned",
			net, gross.Sub(docked))
	}
}

func TestTheWageFileNamesWhoIsMissingWhatReadably(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// No IBAN and no GOSI number, which is what a half-entered record looks
	// like on the day somebody tries to pay it.
	h.hire(t, f, "No Bank Details", "6000.00", map[string]any{
		"iban": "", "gosi_number": "",
	})
	period, _ := lastMonth()
	run := h.prepared(t, f, period)

	ok := h.do(t, http.MethodPost,
		"/api/v1/payroll/"+run["id"].(string)+"/approve?company_id="+
			f.companyID.String(), f.token, map[string]any{})
	ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("approve: %s", readBody(t, ok))
	}

	// This refusal is shown to a payroll clerk, so it has to read like
	// English. It said "has no a bank account and no a GOSI registration
	// number": the sentence supplies the "no" and each item supplied its own
	// article as well.
	resp := h.do(t, http.MethodPost,
		"/api/v1/payroll/"+run["id"].(string)+"/wage-file?company_id="+
			f.companyID.String(), f.token, map[string]any{})
	defer resp.Body.Close()
	body := readBody(t, resp)
	if strings.Contains(body, "no a ") {
		t.Errorf("the wage-file refusal doubles the article: %s", body)
	}
	if !strings.Contains(body, "No Bank Details has no ") {
		t.Errorf("the refusal does not name who is missing what: %s", body)
	}
}
