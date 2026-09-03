//go:build integration

// Employees, attendance, payroll, GOSI, advances and the wage file
// (blueprint C5, C6, E6).
package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func (h *harness) hire(
	t *testing.T, f *shopFixture, name string, basic string, extra map[string]any,
) string {
	t.Helper()
	body := map[string]any{
		"full_name":    name,
		"joined_on":    "2023-01-15",
		"basic_salary": basic,
		"iban":         "SA0380000000608010167519",
		"gosi_number":  "1234567890",
		"national_id":  "1098765432",
	}
	for k, v := range extra {
		body[k] = v
	}
	resp := h.do(t, http.MethodPost,
		"/api/v1/employees?company_id="+f.companyID.String(), f.token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("hire %s: %s", name, readBody(t, resp))
	}
	return decodeJSONFrom(t, resp)["id"].(string)
}

// The whole run: staff in, month computed, approved, paid — and the books
// balance at the end, which is the assertion that matters.
func TestAPayrollRunPostsAndBalances(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	h.hire(t, f, "Aisha Rahman", "6000.00", nil)
	h.hire(t, f, "Omar Farouk", "4500.00", nil)

	period := time.Now().UTC().Format("2006-01")

	resp := h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
		map[string]any{"period": period})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("prepare: %s", readBody(t, resp))
	}
	run := decodeJSONFrom(t, resp)

	if got := run["gross_total"].(string); got != "10500.00" {
		t.Errorf("gross is %s, want 10500.00", got)
	}
	if got := run["status"].(string); got != "draft" {
		t.Errorf("a fresh run is %q, want draft — a calculation somebody "+
			"checks before the money moves", got)
	}
	slips, _ := run["payslips"].([]any)
	if len(slips) != 2 {
		t.Fatalf("the run has %d payslips, want 2", len(slips))
	}

	// The same month cannot be run twice: a second would pay everybody again.
	dup := h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
		map[string]any{"period": period})
	if dup.StatusCode != http.StatusConflict {
		t.Errorf("a second run for the same month got %d, want 409",
			dup.StatusCode)
	}
	dup.Body.Close()

	runID := run["id"].(string)
	resp = h.do(t, http.MethodPost,
		"/api/v1/payroll/"+runID+"/approve"+company, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: %s", readBody(t, resp))
	}
	if got := decodeJSONFrom(t, resp)["status"].(string); got != "approved" {
		t.Errorf("status after approval is %q", got)
	}

	assertTrialBalanceBalances(t, h, f)
}

// GOSI is a legal rate that nobody has verified yet. The run still works —
// wages are wages — and says plainly which figure is missing rather than
// GOSI is computed, and appears on both sides of the payslip.
//
// This test used to assert the opposite: that a run reports social insurance
// as uncalculable. That was correct while `SA.GOSI.RATES` held __VERIFY__ in
// every field — an unparseable figure, which the run reported rather than
// silently treating as zero.
//
// 0117 records the rates from GOSI's own employer guidance, so the honest
// assertion is now that the deduction happens. The refusal path it replaced is
// still covered where it can be tested without mutating a GLOBAL registry rule
// underneath every other test in this package: `TestAPlaceholderPayloadIsRefused`
// proves a placeholder cannot be recorded, and the production verification gate
// is tested in the registry package.
func TestGOSIIsDeductedAndChargedToTheEmployer(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	h.hire(t, f, "Saud Al-Qahtani", "8000.00", map[string]any{
		"is_saudi": true, "nationality": "SA",
	})

	resp := h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
		map[string]any{"period": "2026-08"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("prepare: %s", readBody(t, resp))
	}
	run := decodeJSONFrom(t, resp)

	if got := run["gross_total"].(string); got != "8000.00" {
		t.Errorf("gross is %s, want 8000.00", got)
	}
	if blocked, _ := run["gosi_unavailable"].(bool); blocked {
		t.Fatalf("social insurance is reported as uncalculable: %v",
			run["gosi_blocked_reason"])
	}

	slips, _ := run["payslips"].([]any)
	if len(slips) != 1 {
		t.Fatalf("the run has %d payslips, want 1", len(slips))
	}
	slip, _ := slips[0].(map[string]any)

	// 9.75% of 8,000 is 780.00 for the employee (9% Annuities + 0.75% SANED)
	// and 11.75% is 940.00 for the employer (those plus 2% Occupational
	// Hazards). Both figures come from GOSI's employer guidance; see 0117.
	if got, _ := slip["gosi_employee"].(string); !amountsEqual(got, "780.00") {
		t.Errorf("the employee's GOSI is %s, want 780.00 — 9.75%% of 8,000",
			got)
	}
	if got, _ := slip["gosi_employer"].(string); !amountsEqual(got, "940.00") {
		t.Errorf("the employer's GOSI is %s, want 940.00 — 11.75%% of 8,000",
			got)
	}
}

// A draft run is not a payment instruction.
//
// This test used to assert that a wage file refuses while the layout is
// unverified, which was right while `SA.WPS.WAGE_FILE_FORMAT` held __VERIFY__.
// 0116 records the Ministry's own layout from its published specification, so
// the refusal it tested no longer fires — and staging an unverified version to
// resurrect it would mutate a GLOBAL registry rule underneath every other test
// in this package.
//
// What survives, and matters just as much, is the gate before it: a run that
// nobody has approved must not produce a file a bank would act on.
func TestAWageFileCannotBeDrawnFromAnUnapprovedRun(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	h.hire(t, f, "Aisha Rahman", "6000.00", nil)

	resp := h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
		map[string]any{"period": "2026-08"})
	runID := decodeJSONFrom(t, resp)["id"].(string)

	early := h.do(t, http.MethodPost,
		"/api/v1/payroll/"+runID+"/wage-file"+company, f.token, nil)
	defer early.Body.Close()
	if early.StatusCode != http.StatusConflict {
		t.Errorf("a wage file from a draft got %d, want 409", early.StatusCode)
	}
	if body := readBody(t, early); !containsFold(body, "approve") {
		t.Errorf("the refusal does not say what to do: %s", body)
	}
}

// The consistency check design 20 requires runs BEFORE the file is built:
// missing bank details are cheap to fix now and expensive after a rejection.
func TestTheWageFileChecksBankAndGOSIDetailsFirst(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	// Somebody with no IBAN and no GOSI number.
	resp := h.do(t, http.MethodPost, "/api/v1/employees"+company, f.token,
		map[string]any{
			"full_name": "Nadia Karim", "joined_on": "2024-03-01",
			"basic_salary": "5000.00",
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("hire: %s", readBody(t, resp))
	}

	period := time.Now().UTC().Format("2006-01")
	resp = h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
		map[string]any{"period": period})
	runID := decodeJSONFrom(t, resp)["id"].(string)
	h.do(t, http.MethodPost,
		"/api/v1/payroll/"+runID+"/approve"+company, f.token, nil).Body.Close()

	resp = h.do(t, http.MethodPost,
		"/api/v1/payroll/"+runID+"/wage-file"+company, f.token, nil)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a wage file with no bank details got %d, want 400: %s",
			resp.StatusCode, body)
	}
	for _, want := range []string{"Nadia Karim", "bank account"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not mention %q: %s", want, body)
		}
	}
}

// An advance is a loan, recovered by the next run, and never more than the pay
// it is coming out of.
func TestAnAdvanceIsRecoveredByTheNextRun(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	employee := h.hire(t, f, "Omar Farouk", "4000.00", nil)

	// The cash account the advance comes out of. Created rather than assumed:
	// a skipped test proves nothing about whether the advance posts.
	var cashLedger string
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT a.id::text FROM account a
				JOIN account_role_map m ON m.account_id = a.id
				WHERE a.company_id = $1 AND m.role = 'cash'`,
				f.companyID).Scan(&cashLedger)
		}); err != nil {
		t.Fatalf("find the cash ledger account: %v", err)
	}

	resp := h.do(t, http.MethodPost, "/api/v1/treasury/accounts"+company,
		f.token, map[string]any{
			"kind": "cash", "name": "Petty Cash", "currency": "SAR",
			"account_id": cashLedger,
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open a cash account: %s", readBody(t, resp))
	}
	accountID := decodeJSONFrom(t, resp)["id"].(string)

	resp = h.do(t, http.MethodPost, "/api/v1/advances"+company, f.token,
		map[string]any{
			"employee_id": employee, "account_id": accountID,
			"amount": "1000.00", "installments": 2, "reason": "Family expenses",
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("advance: %s", readBody(t, resp))
	}
	if got := decodeJSONFrom(t, resp)["outstanding"].(string); got != "1000.00" {
		t.Errorf("outstanding is %s, want 1000.00", got)
	}

	period := time.Now().UTC().Format("2006-01")
	resp = h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
		map[string]any{"period": period})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("prepare: %s", readBody(t, resp))
	}
	slips := decodeJSONFrom(t, resp)["payslips"].([]any)
	slip := slips[0].(map[string]any)

	// Two instalments of a thousand is five hundred off this month.
	if got := slip["advance_recovery"].(string); got != "500.00" {
		t.Errorf("recovered %s, want 500.00", got)
	}
	if got := slip["net"].(string); got != "3500.00" {
		t.Errorf("net is %s, want 3500.00 (4000 less the 500 recovered)", got)
	}

	// And the books still balance after the advance posted.
	assertTrialBalanceBalances(t, h, f)
}

// A6.2: a manager who may keep a roster must not learn what the branch earns.
func TestPayIsHiddenFromSomebodyWhoMayNotSeeIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	h.hire(t, f, "Aisha Rahman", "6000.00", nil)

	manager := h.seedUserIn(t, f, "store_manager")
	resp := h.do(t, http.MethodGet, "/api/v1/employees"+company, manager, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a store manager reading the directory: %s", readBody(t, resp))
	}
	staff := decodeJSONFrom(t, resp)["data"].([]any)
	if len(staff) == 0 {
		t.Fatal("the directory came back empty")
	}
	person := staff[0].(map[string]any)

	if _, present := person["basic_salary"]; present {
		t.Error("a store manager can see what somebody is paid; A6.2 requires " +
			"staff to be blockable from other employees' salaries")
	}
	if person["full_name"] == nil || person["full_name"].(string) == "" {
		t.Error("but they must still see the directory itself")
	}

	// And cannot set pay either, which would be the way round the masking.
	resp = h.do(t, http.MethodPost, "/api/v1/employees"+company, manager,
		map[string]any{
			"full_name": "New Person", "joined_on": "2026-01-01",
			"basic_salary": "9000.00",
		})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a store manager setting pay got %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()
}

// Absence comes off, and approved leave is what decides whether a day is an
// absence at all — so payroll reads one source rather than two that disagree.
func TestUnpaidLeaveBecomesAnAbsenceDeduction(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	employee := h.hire(t, f, "Omar Farouk", "3000.00", nil)

	last := time.Now().UTC()
	start := time.Date(last.Year(), last.Month(), 1, 0, 0, 0, 0, time.UTC)

	resp := h.do(t, http.MethodPost, "/api/v1/leave"+company, f.token,
		map[string]any{
			"employee_id": employee, "kind": "unpaid", "is_paid": false,
			"starts_on": start.Format("2006-01-02"),
			"ends_on":   start.AddDate(0, 0, 2).Format("2006-01-02"),
			"reason":    "Personal",
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("request leave: %s", readBody(t, resp))
	}
	leaveID := decodeJSONFrom(t, resp)["id"].(string)

	resp = h.do(t, http.MethodPost,
		"/api/v1/leave/"+leaveID+"/decision"+company, f.token,
		map[string]any{"approve": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve leave: %s", readBody(t, resp))
	}

	resp = h.do(t, http.MethodPost, "/api/v1/payroll"+company, f.token,
		map[string]any{"period": last.Format("2006-01")})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("prepare: %s", readBody(t, resp))
	}
	slip := decodeJSONFrom(t, resp)["payslips"].([]any)[0].(map[string]any)

	if slip["absence_deduction"].(string) == "0.00" {
		t.Error("three days of unpaid leave did not reduce the pay")
	}
	if slip["net"].(string) == slip["gross"].(string) {
		t.Error("net equals gross despite an unpaid absence")
	}
}

// A cashier has no business in the payroll at all.
func TestACashierCannotReachPayroll(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cashier := h.seedUserIn(t, f, "cashier")
	company := "?company_id=" + f.companyID.String()

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/employees" + company},
		{http.MethodGet, "/api/v1/payroll" + company},
		{http.MethodGet, "/api/v1/advances" + company},
		{http.MethodGet, "/api/v1/eosb" + company},
	} {
		resp := h.do(t, c.method, c.path, cashier, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as a cashier: status %d, want 403",
				c.method, c.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
