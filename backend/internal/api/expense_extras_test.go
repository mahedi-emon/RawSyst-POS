//go:build integration

// Departments and recurring expenses (blueprint C3.1).
//
// The last two items on the list migration 0071 recorded as deliberately not
// built. Receipt attachments were on that list too and turned out to have been
// built since: 0096's `document` names 'expense' among the things it attaches
// to, so there was nothing to add.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

func expenseHeadID(t *testing.T, h *harness, f *expenseFixture) string {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		"/api/v1/expenses/heads?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heads: %s", readBody(t, resp))
	}
	rows, _ := decodeJSON(t, resp)["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("the company has no expense heads")
	}
	head, _ := rows[0].(map[string]any)
	id, _ := head["id"].(string)
	return id
}

func newDepartment(t *testing.T, h *harness, f *expenseFixture, code, name string) string {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/expenses/departments?company_id="+f.companyID.String(),
		f.token, map[string]any{"code": code, "name": name})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create department: %d %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	return id
}

// openYear makes sure the months a schedule will post into exist.
//
// The shop fixture opens the year around today; a catch-up test deliberately
// reaches back before that, and a month with no accounting period refuses the
// posting — correctly, which is why the test has to open it rather than work
// around it.
func openYear(t *testing.T, h *harness, f *expenseFixture, year int) {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/accounting/periods?company_id="+f.companyID.String(),
		f.token, map[string]any{"fiscal_year": year})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("open %d: %d %s", year, resp.StatusCode, readBody(t, resp))
	}
}

// --- departments ------------------------------------------------------------

// A department can be created, renamed and retired.
func TestADepartmentIsCreatedRenamedAndRetired(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	id := newDepartment(t, h, f, "OPS", "Operations")

	rename := h.do(t, http.MethodPut,
		"/api/v1/expenses/departments/"+id+"?company_id="+
			f.companyID.String(), f.token,
		map[string]any{"name": "Shop floor"})
	if rename.StatusCode != http.StatusOK {
		t.Fatalf("rename: %s", readBody(t, rename))
	}
	got := decodeJSONFrom(t, rename)
	if n, _ := got["name"].(string); n != "Shop floor" {
		t.Errorf("name is %q after renaming", n)
	}
	// The code is what reports already made something of, so it does not move.
	if c, _ := got["code"].(string); c != "OPS" {
		t.Errorf("code changed to %q; it should stay OPS", c)
	}

	retire := h.do(t, http.MethodPost,
		"/api/v1/expenses/departments/"+id+"/active?company_id="+
			f.companyID.String(), f.token, map[string]any{"active": false})
	if retire.StatusCode != http.StatusOK {
		t.Fatalf("retire: %s", readBody(t, retire))
	}
	retire.Body.Close()

	// Retired departments are hidden by default and visible on request.
	list := h.do(t, http.MethodGet,
		"/api/v1/expenses/departments?company_id="+f.companyID.String(),
		f.token, nil)
	rows, _ := decodeJSON(t, list)["data"].([]any)
	list.Body.Close()
	if len(rows) != 0 {
		t.Errorf("a retired department is still offered: %v", rows)
	}

	all := h.do(t, http.MethodGet,
		"/api/v1/expenses/departments?include_inactive=true&company_id="+
			f.companyID.String(), f.token, nil)
	defer all.Body.Close()
	rows, _ = decodeJSON(t, all)["data"].([]any)
	if len(rows) != 1 {
		t.Errorf("the retired department is not listed even on request")
	}
}

// An expense can be booked to a department, and keeps it.
func TestAnExpenseIsBookedToADepartment(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	dept := newDepartment(t, h, f, "MKT", "Marketing")
	head := expenseHeadID(t, h, f)

	resp := h.do(t, http.MethodPost,
		"/api/v1/expenses?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"uuid": newUUID(), "expense_date": "2026-08-15",
			"paid_from": "cash", "department_id": dept,
			"description": "Leaflets",
			"lines": []any{map[string]any{
				"head_id": head, "net_amount": "120.00",
				"tax_treatment": "standard"}},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record: %d %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	var booked int
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(), `
				SELECT count(*) FROM expense
				WHERE company_id = $1 AND department_id = $2`,
				f.companyID, dept).Scan(&booked)
		}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if booked != 1 {
		t.Errorf("%d expenses carry the department, want 1", booked)
	}
}

// A department that has been spent against cannot be deleted.
//
// The history of every expense booked to it names it, and last year's report
// has to stay reproducible.
func TestADepartmentInUseCannotBeDeleted(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	dept := newDepartment(t, h, f, "FIN", "Finance")
	head := expenseHeadID(t, h, f)

	resp := h.do(t, http.MethodPost,
		"/api/v1/expenses?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"uuid": newUUID(), "expense_date": "2026-08-15",
			"paid_from": "cash", "department_id": dept,
			"lines": []any{map[string]any{
				"head_id": head, "net_amount": "50.00",
				"tax_treatment": "standard"}},
		})
	resp.Body.Close()

	err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(),
				`DELETE FROM department WHERE id = $1`, dept)
			return e
		})
	if err == nil {
		t.Error("a department that has been spent against was deleted")
	}
}

// A retired department cannot take new expenses.
func TestARetiredDepartmentTakesNoNewExpenses(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	dept := newDepartment(t, h, f, "OLD", "Closed down")
	head := expenseHeadID(t, h, f)

	h.do(t, http.MethodPost,
		"/api/v1/expenses/departments/"+dept+"/active?company_id="+
			f.companyID.String(), f.token,
		map[string]any{"active": false}).Body.Close()

	resp := h.do(t, http.MethodPost,
		"/api/v1/expenses?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"uuid": newUUID(), "expense_date": "2026-08-15",
			"paid_from": "cash", "department_id": dept,
			"lines": []any{map[string]any{
				"head_id": head, "net_amount": "10.00",
				"tax_treatment": "standard"}},
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("an expense was booked to a retired department")
	}
}

// One business cannot book to another's department.
func TestADepartmentFromAnotherCompanyIsRefused(t *testing.T) {
	h := newHarness(t)
	mine := seedExpenses(t, h)
	theirs := seedExpenses(t, h)
	theirDept := newDepartment(t, h, theirs, "THX", "Theirs")
	head := expenseHeadID(t, h, mine)

	resp := h.do(t, http.MethodPost,
		"/api/v1/expenses?company_id="+mine.companyID.String(), mine.token,
		map[string]any{
			"uuid": newUUID(), "expense_date": "2026-08-15",
			"paid_from": "cash", "department_id": theirDept,
			"lines": []any{map[string]any{
				"head_id": head, "net_amount": "10.00",
				"tax_treatment": "standard"}},
		})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("an expense was booked to another company's department")
	}
}

// --- recurring expenses -----------------------------------------------------

func newSchedule(t *testing.T, h *harness, f *expenseFixture, body map[string]any) string {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/expenses/recurring?company_id="+f.companyID.String(),
		f.token, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create schedule: %d %s", resp.StatusCode, readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)
	return id
}

func generate(t *testing.T, h *harness, f *expenseFixture, upTo string) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/expenses/recurring/generate?up_to="+upTo+"&company_id="+
			f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generate: %d %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

func expenseCount(t *testing.T, h *harness, f *expenseFixture) int {
	t.Helper()
	var n int
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(),
				`SELECT count(*) FROM expense WHERE company_id = $1`,
				f.companyID).Scan(&n)
		}); err != nil {
		t.Fatalf("count expenses: %v", err)
	}
	return n
}

// A monthly schedule books one expense when it falls due.
func TestARecurringExpenseBooksWhenItFallsDue(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head := expenseHeadID(t, h, f)

	newSchedule(t, h, f, map[string]any{
		"name": "Shop rent", "head_id": head, "amount": "3000.00",
		"paid_from": "bank", "frequency": "monthly", "interval_count": 1,
		"starts_on": "2026-08-01",
	})

	out := generate(t, h, f, "2026-08-15")
	if n, _ := out["created"].(float64); int(n) != 1 {
		t.Fatalf("generated %v expenses, want 1: %v", out["created"], out)
	}
	if got := expenseCount(t, h, f); got != 1 {
		t.Errorf("%d expenses exist, want 1", got)
	}
	assertTrialBalanceBalances(t, h, f.shopFixture)
}

// Running the generator twice does not pay the rent twice.
//
// The guard is a unique index on (schedule, due date), not a check the
// generator performs — a generator that looked first would be correct until two
// of them looked at the same moment.
func TestGeneratingTwiceDoesNotBookTheRentTwice(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head := expenseHeadID(t, h, f)

	newSchedule(t, h, f, map[string]any{
		"name": "Internet", "head_id": head, "amount": "400.00",
		"paid_from": "bank", "frequency": "monthly",
		"starts_on": "2026-08-01",
	})

	generate(t, h, f, "2026-08-15")
	before := expenseCount(t, h, f)

	second := generate(t, h, f, "2026-08-15")
	if n, _ := second["created"].(float64); int(n) != 0 {
		t.Errorf("the second run booked %v more, want 0", second["created"])
	}
	if after := expenseCount(t, h, f); after != before {
		t.Errorf("expenses went from %d to %d on a repeat run", before, after)
	}
	assertTrialBalanceBalances(t, h, f.shopFixture)
}

// Missed periods are caught up one at a time, not collapsed into one.
//
// Three months of rent were owed; one entry would understate two of them.
func TestMissedPeriodsAreCaughtUpOneByOne(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head := expenseHeadID(t, h, f)
	openYear(t, h, f, 2026)

	newSchedule(t, h, f, map[string]any{
		"name": "Cleaner", "head_id": head, "amount": "250.00",
		"paid_from": "cash", "frequency": "monthly",
		"starts_on": "2026-06-01",
	})

	out := generate(t, h, f, "2026-08-15")
	if n, _ := out["created"].(float64); int(n) != 3 {
		t.Errorf("caught up %v periods, want 3 (June, July, August): %v",
			out["created"], out)
	}
	if got := expenseCount(t, h, f); got != 3 {
		t.Errorf("%d expenses exist, want 3", got)
	}
	assertTrialBalanceBalances(t, h, f.shopFixture)
}

// A schedule stops at its end date.
func TestAScheduleStopsAtItsEndDate(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head := expenseHeadID(t, h, f)
	openYear(t, h, f, 2026)

	newSchedule(t, h, f, map[string]any{
		"name": "Short lease", "head_id": head, "amount": "100.00",
		"paid_from": "cash", "frequency": "monthly",
		"starts_on": "2026-06-01", "ends_on": "2026-07-31",
	})

	out := generate(t, h, f, "2026-12-31")
	if n, _ := out["created"].(float64); int(n) != 2 {
		t.Errorf("generated %v, want 2 (June and July only): %v",
			out["created"], out)
	}
}

// A paused schedule books nothing.
func TestAPausedScheduleBooksNothing(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head := expenseHeadID(t, h, f)

	id := newSchedule(t, h, f, map[string]any{
		"name": "Paused", "head_id": head, "amount": "75.00",
		"paid_from": "cash", "frequency": "monthly",
		"starts_on": "2026-08-01",
	})
	h.do(t, http.MethodPost,
		"/api/v1/expenses/recurring/"+id+"/active?company_id="+
			f.companyID.String(), f.token,
		map[string]any{"active": false}).Body.Close()

	out := generate(t, h, f, "2026-08-15")
	if n, _ := out["created"].(float64); int(n) != 0 {
		t.Errorf("a paused schedule booked %v expenses", out["created"])
	}
}

// A schedule on the 31st does not drift when it meets a shorter month.
//
// Go's AddDate turns 31 January plus a month into 3 March, which would move the
// day of month every short month until it settled on the 3rd.
func TestAMonthEndScheduleDoesNotDrift(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head := expenseHeadID(t, h, f)
	openYear(t, h, f, 2026)

	newSchedule(t, h, f, map[string]any{
		"name": "Month end", "head_id": head, "amount": "10.00",
		"paid_from": "cash", "frequency": "monthly",
		"starts_on": "2026-01-31",
	})

	// Through February, which has no 31st, and on into March.
	generate(t, h, f, "2026-03-31")

	var dates []string
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			rows, e := tx.Query(context.Background(), `
				SELECT to_char(expense_date, 'YYYY-MM-DD') FROM expense
				WHERE company_id = $1 ORDER BY expense_date`, f.companyID)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var d string
				if e := rows.Scan(&d); e != nil {
					return e
				}
				dates = append(dates, d)
			}
			return rows.Err()
		}); err != nil {
		t.Fatalf("read dates: %v", err)
	}

	want := []string{"2026-01-31", "2026-02-28", "2026-03-31"}
	if len(dates) != len(want) {
		t.Fatalf("dates = %v, want %v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Errorf("date %d is %s, want %s — the day of month drifted",
				i, dates[i], want[i])
		}
	}
}

// Generating is recording an expense, so it needs the permission to record one.
func TestACashierCannotGenerateRecurringExpenses(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, http.MethodPost,
		"/api/v1/expenses/recurring/generate?company_id="+
			f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cashier generating expenses got %d, want 403",
			resp.StatusCode)
	}
}

// One business's schedules never generate into another's books.
func TestSchedulesDoNotCrossCompanies(t *testing.T) {
	h := newHarness(t)
	mine := seedExpenses(t, h)
	theirs := seedExpenses(t, h)
	head := expenseHeadID(t, h, mine)

	newSchedule(t, h, mine, map[string]any{
		"name": "Mine", "head_id": head, "amount": "500.00",
		"paid_from": "cash", "frequency": "monthly",
		"starts_on": "2026-08-01",
	})

	out := generate(t, h, theirs, "2026-08-15")
	if n, _ := out["created"].(float64); int(n) != 0 {
		t.Errorf("another company's schedule generated %v expenses here",
			out["created"])
	}
	if got := expenseCount(t, h, theirs); got != 0 {
		t.Errorf("%d expenses landed in the wrong company", got)
	}
}
