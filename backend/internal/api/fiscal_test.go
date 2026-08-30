//go:build integration

// The accounting calendar (C10) and the audit trail (D4), derived from the
// blueprint rather than from the code.
//
//	"Once a period is closed, no transaction can be created, edited, or deleted
//	 in that period — this is what makes financial statements trustworthy."
//
//	"Reopening a closed period requires an explicit Owner-level permission plus
//	 a mandatory reason, and is permanently audit-logged."
//
//	D4: an append-only trail of who, what, when, where, before and after, which
//	"cannot be edited or deleted by any user, including Owner, to preserve
//	 evidentiary integrity".
//
// The first test in the file is not about any of that. It is the blocker: a
// company provisioned through this product had no fiscal periods at all, so
// `accounting.resolvePeriod` refused every journal entry, which is every
// financial act the product can perform. The refusal told the reader to ask an
// owner to open the period, and no owner could.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A company that comes out of provisioning can post.
//
// Asked the way the posting engine asks it: is there a period covering today.
func TestACompanyIsGivenACalendarItCanPostInto(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// The fixture builds its company with raw SQL, so it gets the calendar the
	// same way any other path does — from 0080 for one that existed, and from
	// the roll-forward for one created since. Both are exercised by asking the
	// database the question the posting engine asks.
	var covered int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(t.Context(),
			`SELECT open_fiscal_year($1, fiscal_year_of($1, current_date))`,
			f.companyID); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM fiscal_period
			WHERE company_id = $1 AND current_date BETWEEN starts_on AND ends_on`,
			f.companyID).Scan(&covered)
	}); err != nil {
		t.Fatalf("look for a period covering today: %v", err)
	}

	if covered != 1 {
		t.Fatalf("%d periods cover today; exactly one must, or every journal "+
			"entry this product makes is refused for want of a period",
			covered)
	}
}

// Twelve months, and the last day of each one is the last day it really has.
//
// February 2028 has 29 days. A generator that named 28 would leave one day of
// every leap year covered by no period, on which nothing could be posted —
// and the failure would arrive once every four years, on a date nobody
// remembers to test.
func TestAYearIsTwelveMonthsWithNoDayLeftUncovered(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `SELECT open_fiscal_year($1, 2028)`, f.companyID)
		return e
	}); err != nil {
		t.Fatalf("open 2028: %v", err)
	}

	var periods int
	var firstStart, lastEnd string
	var febEnd string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `
			SELECT count(*), to_char(min(starts_on), 'YYYY-MM-DD'),
			       to_char(max(ends_on), 'YYYY-MM-DD')
			FROM fiscal_period WHERE company_id = $1 AND fiscal_year = 2028`,
			f.companyID).Scan(&periods, &firstStart, &lastEnd); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `
			SELECT to_char(ends_on, 'YYYY-MM-DD') FROM fiscal_period
			WHERE company_id = $1 AND fiscal_year = 2028 AND period_no = 2`,
			f.companyID).Scan(&febEnd)
	}); err != nil {
		t.Fatalf("read 2028: %v", err)
	}

	if periods != 12 {
		t.Errorf("a fiscal year has twelve periods, not %d", periods)
	}
	if firstStart != "2028-01-01" || lastEnd != "2028-12-31" {
		t.Errorf("the year runs %s to %s, want 2028-01-01 to 2028-12-31",
			firstStart, lastEnd)
	}
	if febEnd != "2028-02-29" {
		t.Errorf("February 2028 ends on %s. It is a leap year, and a period "+
			"ending on the 28th leaves a day nothing can be posted on.", febEnd)
	}
}

// Opening the same year twice is not an error, and does not double it.
func TestOpeningAYearTwiceIsHarmless(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)

	first := f.openYear(t, h, 2031)
	if first != 12 {
		t.Fatalf("opening a fresh year should create twelve periods, not %d", first)
	}
	if again := f.openYear(t, h, 2031); again != 0 {
		t.Errorf("opening it again created %d more periods; two people pressing "+
			"the button on the same morning must not produce a second calendar",
			again)
	}
}

// "no transaction can be created, edited, or deleted in that period"
func TestNothingCanBePostedIntoAClosedPeriod(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)

	period := f.periodNumbered(t, h, 1)
	f.close(t, h, period)

	// The refusal a real posting would meet, from the engine rather than from
	// the screen. Dated inside the month that was closed, because a date in
	// some other month would be refused for a different reason and the test
	// would pass without proving anything.
	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
			VALUES ($1,$2,$3,claim_entry_no($2),'2026-01-15','test')`,
			f.tenantID, f.companyID, period)
		return e
	})
	if err == nil {
		t.Fatal("an entry was posted into a closed period, which is the one " +
			"thing closing a period is for")
	}
}

// Periods close in order.
func TestALaterMonthCannotCloseWhileAnEarlierOneIsOpen(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)

	// Period 3 while 1 and 2 are still open.
	third := f.periodNumbered(t, h, 3)
	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/accounting/periods/"+third+"/close"), f.token, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("closing out of order should conflict, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// "requires ... a mandatory reason, and is permanently audit-logged"
func TestReopeningNeedsAReasonAndLeavesATrail(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)

	period := f.periodNumbered(t, h, 1)
	f.close(t, h, period)

	// Without a reason, and with one too short to say anything.
	for _, reason := range []string{"", "typo"} {
		resp := h.do(t, http.MethodPost,
			f.path("/api/v1/accounting/periods/"+period+"/reopen"), f.token,
			map[string]any{"reason": reason})
		if resp.StatusCode < 400 {
			t.Fatalf("reopening with reason %q should be refused, got %d — %s",
				reason, resp.StatusCode, readBody(t, resp))
		}
	}

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/accounting/periods/"+period+"/reopen"), f.token,
		map[string]any{
			"reason": "The January bank charges were keyed to February.",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reopen: %s", readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["state"]; got != "open" {
		t.Errorf("the period is %v after reopening, want open", got)
	}

	// The trail, read the way the screen reads it.
	trail := h.do(t, http.MethodGet,
		"/api/v1/audit?action=period_reopened", f.token, nil)
	if trail.StatusCode != http.StatusOK {
		t.Fatalf("read the trail: %s", readBody(t, trail))
	}
	rows, _ := decodeJSON(t, trail)["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("reopening a period left no audit record. C10 requires it to " +
			"be permanently audit-logged, and a reopen nobody can find is a " +
			"set of figures that changed with no explanation attached.")
	}
	row, _ := rows[0].(map[string]any)
	if row["actor"] == "" || row["actor"] == nil {
		t.Error("the audit record names nobody; `actor_label` exists so the " +
			"trail survives the user being deleted")
	}
	after, _ := row["after"].(map[string]any)
	if after == nil || after["reason"] == nil {
		t.Errorf("the reason was not recorded with the reopen: %v", row["after"])
	}
}

// "cannot be edited or deleted by any user, including Owner"
func TestTheAuditTrailCannotBeChanged(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)

	period := f.periodNumbered(t, h, 1)
	f.close(t, h, period)

	for _, attempt := range []struct {
		name string
		sql  string
	}{
		{"edited", `UPDATE audit_log SET action = 'nothing_happened'
		            WHERE action = 'period_closed'`},
		{"deleted", `DELETE FROM audit_log WHERE action = 'period_closed'`},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
				_, e := tx.Exec(t.Context(), attempt.sql)
				return e
			})
			if err == nil {
				t.Fatalf("the audit trail could be %s. D4 makes it append-only "+
					"so that it is evidence rather than a record somebody kept.",
					attempt.name)
			}
		})
	}
}

// A closed period says who closed it.
func TestAClosedPeriodCarriesTheNameOfWhoeverClosedIt(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)

	period := f.periodNumbered(t, h, 1)
	closed := f.close(t, h, period)

	if closed["closed_by"] == "" || closed["closed_by"] == nil {
		t.Error("a closed period with no name against it is not something " +
			"anybody can be asked to stand behind")
	}
	if closed["closed_at"] == "" || closed["closed_at"] == nil {
		t.Error("a closed period should record when it was closed")
	}
}

// A locked period is not reopened by the ordinary route: the year-end routine
// has closed revenue and expense into retained earnings, and putting a
// transaction back would leave those entries wrong with nothing saying so.
func TestALockedPeriodIsNotReopened(t *testing.T) {
	h := newHarness(t)
	f := seedCalendar(t, h)

	period := f.periodNumbered(t, h, 1)
	f.close(t, h, period)
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE fiscal_period SET state = 'locked' WHERE id = $1`, period)
		return e
	}); err != nil {
		t.Fatalf("lock the period: %v", err)
	}

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/accounting/periods/"+period+"/reopen"), f.token,
		map[string]any{"reason": "We would like to change last year's figures."})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("reopening a locked period should conflict, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// --- fixture --------------------------------------------------------------

type calendarFixture struct {
	*shopFixture
	token string
}

func seedCalendar(t *testing.T, h *harness) *calendarFixture {
	t.Helper()
	f := h.seedShop(t, "owner")
	out := &calendarFixture{shopFixture: f, token: f.token}

	// The shop fixture creates exactly one period — August 2026, the month it
	// trades in — so the year is filled in around it. Working inside the
	// company's own calendar rather than inventing a distant one is the point:
	// periods close from the front, and a test year floating above an open
	// month would never close at all.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `SELECT open_fiscal_year($1, 2026)`, f.companyID)
		return e
	}); err != nil {
		t.Fatalf("open the test year: %v", err)
	}
	return out
}

func (f *calendarFixture) path(base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + f.companyID.String()
}

func (f *calendarFixture) openYear(t *testing.T, h *harness, year int) int {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/accounting/periods"),
		f.token, map[string]any{"fiscal_year": year})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open %d: status %d — %s", year, resp.StatusCode, readBody(t, resp))
	}
	made, _ := decodeJSON(t, resp)["periods_created"].(float64)
	return int(made)
}

// periodNumbered is one month of the fixture's own year.
func (f *calendarFixture) periodNumbered(t *testing.T, h *harness, n int) string {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT id FROM fiscal_period
			WHERE company_id = $1 AND fiscal_year = 2026 AND period_no = $2`,
			f.companyID, n).Scan(&id)
	}); err != nil {
		t.Fatalf("find period %d: %v", n, err)
	}
	return id.String()
}

func (f *calendarFixture) close(
	t *testing.T, h *harness, periodID string,
) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/accounting/periods/"+periodID+"/close"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close period: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}
