//go:build integration

// Saudi obligations do not follow a business into other markets.
//
// `people`, `privacy` and `compliance` resolved Saudi rule keys — SA.GOSI.RATES,
// SA.WPS.WAGE_FILE_FORMAT, SA.EOSB.ENTITLEMENT, SA.PDPL.* — for whatever
// country the company was in. Outside Saudi Arabia those rules do not exist, so
// the registry answered "No regulatory rule is on record", and that error came
// back through the module's default branch.
//
// The effect was not a missing feature but a broken one: **a Bangladeshi shop
// could not run payroll at all**. Not "payroll without a GOSI line" — no
// payslips, no wages, the whole run refused because a Saudi social-insurance
// rate was missing for a country that has no GOSI.
//
// Each obligation is now asked of the market. Where it does not apply the
// module declines that ONE figure and carries on, and no foreign equivalent is
// invented: this product has Saudi's rules and no others, and applying Saudi
// service bands or contribution rates to a foreign contract would be worse than
// declining.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// employeeIn hires somebody on a monthly wage and returns their id.
func employeeIn(t *testing.T, h *harness, f *shopFixture, basic string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO employee
			  (tenant_id, company_id, employee_no, full_name, joined_on,
			   basic_salary, is_saudi, currency)
			VALUES ($1,$2,$3,'Test Employee','2024-01-01',$4::numeric,false,
			        (SELECT base_currency FROM company WHERE id = $2))
			RETURNING id`,
			f.tenantID, f.companyID, "E"+uuid.NewString()[:8], basic).Scan(&id)
	}); err != nil {
		t.Fatalf("hire an employee: %v", err)
	}
	return id
}

// A Bangladeshi shop can run payroll.
//
// The assertion this whole change exists for. Before it, the run failed on a
// Saudi social-insurance rate that Bangladesh has no equivalent of, and nobody
// got paid.
func TestABangladeshiShopCanRunPayroll(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopInMarket(t, "owner", "bd", "BDT")
	owner := h.login(t, f.email)
	employeeIn(t, h, f, "30000")

	resp := h.do(t, http.MethodPost,
		"/api/v1/payroll?company_id="+f.companyID.String(), owner,
		map[string]any{"period": "2026-08-01"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		if containsAny(body, "GOSI", "SA.GOSI", "regulatory rule") {
			t.Fatalf("a Bangladeshi payroll run was refused for a SAUDI rule: %s", body)
		}
		t.Fatalf("payroll run: %d %s", resp.StatusCode, body)
	}
}

// A Saudi shop still gets the Saudi treatment.
//
// The other half, and the one that must not be weakened to make the first pass:
// where the obligation DOES apply, social insurance has to appear.
//
// This used to accept "it reported GOSI as blocked" as a pass, which was right
// while `SA.GOSI.RATES` held __VERIFY__ in every field. 0117 records the rates
// from GOSI's own employer guidance, so the honest assertion is now the
// stronger one: the figures are actually on the payslip. A Saudi run that
// computed no social insurance would be understating both the employee's
// deduction and the employer's cost, which is the failure this test exists to
// catch either way.
func TestASaudiShopStillAnswersForItsOwnRules(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner") // Saudi fixture
	owner := h.login(t, f.email)
	employeeIn(t, h, f, "10000")

	resp := h.do(t, http.MethodPost,
		"/api/v1/payroll?company_id="+f.companyID.String(), owner,
		map[string]any{"period": "2026-08"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a Saudi payroll run was refused: %d %s",
			resp.StatusCode, readBody(t, resp))
	}

	run := decodeJSONFrom(t, resp)
	if blocked, _ := run["gosi_unavailable"].(bool); blocked {
		t.Fatalf("social insurance is reported as uncalculable: %v",
			run["gosi_blocked_reason"])
	}

	slips, _ := run["payslips"].([]any)
	if len(slips) == 0 {
		t.Fatal("the run produced no payslips")
	}
	slip, _ := slips[0].(map[string]any)

	employee, _ := slip["gosi_employee"].(string)
	employer, _ := slip["gosi_employer"].(string)
	if employee == "" || employer == "" {
		t.Fatalf("the payslip carries no social insurance figures: %v", slip)
	}

	// The EMPLOYER share is the one that must always be there. This fixture
	// hires a non-Saudi, and for an expatriate the employee contributes
	// nothing: GOSI puts non-Saudis in the Occupational Hazards Branch alone,
	// at 2% paid entirely by the employer, while the Annuities Branch and SANED
	// are Saudi-only. So 2% of 10,000 is 200.00 from the employer and a
	// legitimate zero from the worker — asserting both were non-zero would be
	// asserting a deduction the law does not make.
	if amountsEqual(employer, "0.00") {
		t.Errorf("a Saudi shop's payslip charges the employer nothing for " +
			"social insurance; occupational hazards applies to every worker " +
			"regardless of nationality")
	}
	if !amountsEqual(employer, "200.00") {
		t.Errorf("the employer's share is %s, want 200.00 — 2%% of 10,000 "+
			"for an expatriate", employer)
	}
	if !amountsEqual(employee, "0.00") {
		t.Errorf("an expatriate was charged %s; the Annuities Branch and "+
			"SANED are Saudi-only", employee)
	}
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
