//go:build integration

// E7's compliance dashboard, which had never answered.
//
// `GET /compliance` gathers the readings a business is judged on: invoicing,
// VAT, privacy, storefront disclosures, payroll, expiring documents, archive
// health, and how many regulatory rules are still unverified.
//
// It answered 500 for every business that had ever run payroll. `max(period)`
// on `payroll_run.period` is a DATE and was scanned into a `*string`; pgx
// refuses to put a date into a string in binary format. An empty table hands
// back a NULL and nothing has to be converted, which is exactly why the fault
// survived: it worked until the month a business first paid anybody, and never
// again after. Nothing caught it because nothing called it — no screen.
//
// And the people reading was blind to the case it exists for. Its own comment
// says "E7 names Iqama and work permits specifically", and it counted rows in
// the `document` table only. An employee whose residency permit expires next
// month — the exact thing `GET /employees/expiring` lists by name — did not
// appear, so the dashboard told an owner nothing was expiring while their
// cashier was weeks from being unable to work legally.
package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// theCompliance reads the dashboard the way the screen does.
func theCompliance(t *testing.T, h *harness, f *shopFixture) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		"/api/v1/compliance?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the compliance dashboard answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
	return decodeJSONFrom(t, resp)["report"].(map[string]any)
}

func TestTheComplianceDashboardAnswersAtAll(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// A business that has run no payroll. This case ALWAYS worked: `max()` over
	// an empty table is NULL, and pgx hands a NULL back without deciding what
	// type it would have been. Which is exactly why the fault survived — the
	// dashboard broke on the first business that paid anybody, and stayed
	// broken for them for ever. The next test is the one that reproduces it.
	report := theCompliance(t, h, f)
	for _, section := range []string{
		"invoicing", "vat", "privacy", "storefront", "payroll", "people",
		"records",
	} {
		if _, ok := report[section]; !ok {
			t.Errorf("the dashboard reports nothing about %q", section)
		}
	}
}

func TestTheDashboardNamesTheMonthPayrollLastRan(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	h.hire(t, f, "Somebody Paid", "6000.00", nil)
	period, _ := lastMonth()
	run := h.prepared(t, f, period)
	if run["id"] == nil {
		t.Fatal("no run was prepared")
	}

	// One row is all it takes: with a date to hand back rather than a NULL,
	// pgx has to decide what a DATE becomes in a *string, and refuses. The
	// whole dashboard answered 500 from the first month a business ran.
	payroll := theCompliance(t, h, f)["payroll"].(map[string]any)
	got, _ := payroll["last_run_period"].(string)
	if got != period {
		t.Errorf("the dashboard says payroll last ran %q, and it ran %q",
			got, period)
	}
	// A month, not a day. The rest of the product speaks of a run as "2026-08",
	// and "2026-08-01" invites somebody to wonder what happened on the first.
	if len(got) != len("2006-01") {
		t.Errorf("the period reads %q, which is not a month", got)
	}
}

func TestAnExpiringResidencyPermitReachesTheDashboard(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// Inside the sixty-day window the staff alert uses.
	soon := time.Now().UTC().AddDate(0, 0, 31).Format("2006-01-02")
	expiringOn(t, h, f, soon)

	// The staff screen finds them by name.
	resp := h.do(t, http.MethodGet,
		"/api/v1/employees/expiring?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the expiry alert answered %d", resp.StatusCode)
	}
	listed := decodeJSONFrom(t, resp)["data"].([]any)
	if len(listed) == 0 {
		t.Fatalf("the staff alert does not list the person whose permit "+
			"expires on %s, so this test proves nothing", soon)
	}

	// And so must the dashboard, which is the reading an owner checks when they
	// are not looking at the staff screen.
	people := theCompliance(t, h, f)["people"].(map[string]any)
	staff, _ := people["staff_expiring_soon"].(float64)
	if staff < 1 {
		t.Errorf("the dashboard counts %v staff permits expiring, and one "+
			"expires on %s", people["staff_expiring_soon"], soon)
	}
	total, _ := people["expiring_soon"].(float64)
	if total < staff {
		t.Errorf("the total of %v expiring is smaller than the %v staff "+
			"permits inside it", total, staff)
	}
}

func TestSomebodyWhoHasLeftIsNotThisBusinessesExposure(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	soon := time.Now().UTC().AddDate(0, 0, 20).Format("2006-01-02")
	who := expiringOn(t, h, f, soon)

	before := theCompliance(t, h, f)["people"].(map[string]any)
	if n, _ := before["staff_expiring_soon"].(float64); n < 1 {
		t.Fatalf("the permit expiring on %s was not counted to begin with", soon)
	}

	markLeft(t, h, f, who)

	// Their permit lapsing is no longer this business's problem. Counting it
	// would leave a dashboard permanently reporting an exposure nobody can fix.
	after := theCompliance(t, h, f)["people"].(map[string]any)
	if n, _ := after["staff_expiring_soon"].(float64); n != 0 {
		t.Errorf("somebody who has left still counts as an expiring permit: %v",
			after["staff_expiring_soon"])
	}
}

// markLeft records a departure directly, which is what the leaving route does.
func markLeft(t *testing.T, h *harness, f *shopFixture, who uuid.UUID) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE employee SET left_on = current_date, status = 'left'
			 WHERE id = $1 AND company_id = $2`, who, f.companyID)
		return e
	}); err != nil {
		t.Fatalf("record a departure: %v", err)
	}
}
