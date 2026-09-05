//go:build integration

// The document-expiry alert, which had never once answered.
//
// A shop that employs foreign staff runs on residency permits, and one that
// expires is a person who cannot legally work tomorrow. B-side HR names the
// alert; `GET /employees/expiring` is it.
//
// It answered 500 on every call it ever received. The query said
// `current_date + ($2 || ' days')::interval` and `$2` is bound as an INT:
// Postgres has no operator appending text to an integer, so the statement
// failed to plan. Nothing caught it because nothing called it — the route had
// no screen, so no test and no person ever asked it a question.
//
// Found by driving the HR routes before building that screen.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// expiringOn hires somebody whose permit runs out on a given date.
func expiringOn(t *testing.T, h *harness, f *shopFixture, when string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO employee
			  (tenant_id, company_id, employee_no, full_name, joined_on,
			   basic_salary, is_saudi, currency, iqama_no, id_expires_on)
			VALUES ($1,$2,$3,'Expiring Person','2024-01-01',3000,false,
			        (SELECT base_currency FROM company WHERE id = $2),
			        '2345678901', $4::date)
			RETURNING id`,
			f.tenantID, f.companyID, "E"+uuid.NewString()[:8], when).Scan(&id)
	}); err != nil {
		t.Fatalf("hire: %v", err)
	}
	return id
}

func TestTheExpiringDocumentAlertAnswersAtAll(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// Empty is a perfectly good answer, and it was the one this route could
	// never give: the failure was in planning the statement, so it did not
	// need a row to fail on.
	resp := h.do(t, http.MethodGet,
		"/api/v1/employees/expiring?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the expiry alert answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// And it answers the question it was asked.
func TestTheExpiringDocumentAlertFindsWhatIsAboutToRunOut(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	soon := expiringOn(t, h, f, "2026-09-20")
	expiringOn(t, h, f, "2030-01-01")

	body := decodeJSON(t, h.do(t, http.MethodGet,
		"/api/v1/employees/expiring?company_id="+f.companyID.String()+"&days=3650",
		f.token, nil))
	rows, _ := body["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("%d people inside ten years, want 2", len(rows))
	}

	// A narrower window finds only the one that is actually urgent, which is
	// the whole point of the parameter that was breaking the query.
	narrow := decodeJSON(t, h.do(t, http.MethodGet,
		"/api/v1/employees/expiring?company_id="+f.companyID.String()+"&days=1",
		f.token, nil))
	near, _ := narrow["data"].([]any)
	if len(near) != 0 {
		t.Errorf("%d people expire within a day of today, want 0", len(near))
	}

	// Ordered soonest first: an alert that listed the 2030 permit above the
	// one running out this month would bury the thing it exists to surface.
	first, _ := rows[0].(map[string]any)
	if id, _ := first["id"].(string); id != soon.String() {
		t.Errorf("the first row is %v, want the permit expiring soonest", id)
	}
}

// Somebody who has left is not chased for a permit.
func TestTheExpiringAlertLeavesOutPeopleWhoHaveGone(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	gone := expiringOn(t, h, f, "2026-09-20")
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE employee SET status = 'left', left_on = current_date WHERE id = $1`,
			gone)
		return e
	}); err != nil {
		t.Fatalf("mark as left: %v", err)
	}

	body := decodeJSON(t, h.do(t, http.MethodGet,
		"/api/v1/employees/expiring?company_id="+f.companyID.String()+"&days=3650",
		f.token, nil))
	rows, _ := body["data"].([]any)
	if len(rows) != 0 {
		t.Errorf("%d rows, want 0: somebody who has left needs no permit", len(rows))
	}
}

var _ = pgx.ErrNoRows
