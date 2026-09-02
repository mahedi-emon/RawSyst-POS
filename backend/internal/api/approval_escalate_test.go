//go:build integration

// Escalation and delegation — the last two untested paths in F1.
//
// Both exist because an approval that nobody answers is worse than one that is
// refused: the work stops and nobody is accountable for the silence. F1 asks
// for "if an approver doesn't respond within X hours, escalate", and for cover
// while somebody is away.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ruleEscalatingAfter makes expenses over a threshold escalate after some hours.
func ruleEscalatingAfter(
	t *testing.T, h *harness, f *shopFixture, over string, hours int,
) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var id uuid.UUID

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO approval_rule
			  (tenant_id, company_id, name, subject, condition, action, steps,
			   escalate_after_hours, is_active)
			VALUES ($1,$2,'Escalating expenses','expense',
			        jsonb_build_object('amount_over',$3::text),
			        'require_approval',
			        jsonb_build_array(jsonb_build_object('role','owner')),
			        $4, true)
			RETURNING id`, f.tenantID, f.companyID, over, hours).Scan(&id)
	}); err != nil {
		t.Fatalf("create escalating rule: %v", err)
	}
	return id
}

func statusOfRequest(t *testing.T, h *harness, f *shopFixture, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT status FROM approval_request WHERE id = $1`, id).Scan(&status)
	}); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}

// A request nobody answered in time is escalated.
//
// The deadline is moved into the past rather than the test waiting for it: what
// is under test is that the sweep acts on an elapsed deadline, not that time
// passes.
func TestARequestNobodyAnsweredIsEscalated(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head, _ := headNamed(t, h, f, "RENT")["id"].(string)
	ruleEscalatingAfter(t, h, f.shopFixture, "500", 4)

	postExpense(t, h, f, head, "5000.00").Body.Close()
	req := pendingRequest(t, h, f.shopFixture)

	if got := statusOfRequest(t, h, f.shopFixture, req); got != "pending" {
		t.Fatalf("a new request is %q, want pending", got)
	}

	// The four hours have passed.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE approval_request SET escalate_at = now() - interval '1 hour'
			 WHERE id = $1`, req)
		return e
	}); err != nil {
		t.Fatalf("age the deadline: %v", err)
	}

	resp := h.do(t, http.MethodPost,
		"/api/v1/approvals/escalate?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("escalate: %d %s", resp.StatusCode, readBody(t, resp))
	}

	if got := statusOfRequest(t, h, f.shopFixture, req); got != "escalated" {
		t.Errorf("status = %q, want escalated", got)
	}
}

// A request still within its deadline is left alone.
//
// The half that keeps escalation meaningful: a sweep that escalated everything
// would make the state say nothing.
func TestARequestInsideItsDeadlineIsNotEscalated(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head, _ := headNamed(t, h, f, "RENT")["id"].(string)
	ruleEscalatingAfter(t, h, f.shopFixture, "500", 24)

	postExpense(t, h, f, head, "5000.00").Body.Close()
	req := pendingRequest(t, h, f.shopFixture)

	resp := h.do(t, http.MethodPost,
		"/api/v1/approvals/escalate?company_id="+f.companyID.String(), f.token, nil)
	resp.Body.Close()

	if got := statusOfRequest(t, h, f.shopFixture, req); got != "pending" {
		t.Errorf("status = %q, want pending — a request inside its deadline "+
			"was escalated", got)
	}
}

// An escalated request can still be decided.
//
// Escalation raises the alarm; it does not close the request. A request that
// became undecidable by being escalated would strand the work for good.
func TestAnEscalatedRequestCanStillBeDecided(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head, _ := headNamed(t, h, f, "RENT")["id"].(string)
	ruleEscalatingAfter(t, h, f.shopFixture, "500", 4)

	postExpense(t, h, f, head, "5000.00").Body.Close()
	req := pendingRequest(t, h, f.shopFixture)

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE approval_request SET status = 'escalated' WHERE id = $1`, req)
		return e
	}); err != nil {
		t.Fatalf("escalate directly: %v", err)
	}

	resp := h.do(t, http.MethodPost,
		"/api/v1/approvals/"+req.String()+"/decide?company_id="+f.companyID.String(),
		f.token, map[string]any{"approve": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Errorf("an escalated request could not be decided: %d %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// Cover can be arranged, and nonsense cover is refused.
//
// Delegating to yourself changes nothing, and cover that ends before it starts
// is a date entered wrongly — both are refused rather than stored as a record
// nobody can act on.
func TestDelegationRefusesNonsenseAndAcceptsCover(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)

	// Somebody to cover for.
	deputy := h.seedUserWithRole(t, "store_manager")
	var deputyID uuid.UUID
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT id FROM app_user WHERE email = $1`, deputy).Scan(&deputyID)
	}); err != nil {
		t.Fatalf("find the deputy: %v", err)
	}

	path := "/api/v1/approval-delegations?company_id=" + f.companyID.String()

	// Ends before it starts.
	backwards := h.do(t, http.MethodPost, path, f.token, map[string]any{
		"to_user_id": deputyID.String(),
		"starts_on":  "2026-09-10", "ends_on": "2026-09-01",
	})
	backwards.Body.Close()
	if backwards.StatusCode == http.StatusOK || backwards.StatusCode == http.StatusCreated ||
		backwards.StatusCode == http.StatusNoContent {
		t.Error("cover that ends before it starts was accepted")
	}

	// A sensible period.
	ok := h.do(t, http.MethodPost, path, f.token, map[string]any{
		"to_user_id": deputyID.String(),
		"starts_on":  "2026-09-01", "ends_on": "2026-09-10",
	})
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK && ok.StatusCode != http.StatusCreated &&
		ok.StatusCode != http.StatusNoContent {
		t.Fatalf("cover was refused: %d %s", ok.StatusCode, readBody(t, ok))
	}

	list := h.do(t, http.MethodGet, path, f.token, nil)
	defer list.Body.Close()
	if body := readBody(t, list); !strings.Contains(body, deputyID.String()) {
		t.Errorf("the arranged cover is not listed: %s", body)
	}
}
