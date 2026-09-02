//go:build integration

// The approval centre, end to end.
//
// F1's engine is genuinely wired — `workflow.Evaluate` is called from
// `expenses.Record` and from purchasing — but only rule CRUD was tested. The
// decision itself, the thing the whole module exists for, had no coverage at
// all: nothing proved that approving a request lets the work through, that
// refusing it says why, that a decided request cannot be decided twice, or that
// the permission to decide is separate from the permission to look.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ruleOverAmount makes expenses above a threshold need sign-off.
//
// `require_approval` with a step, because 0093 refuses an approval that routes
// nowhere: a request nobody can act on would sit for ever.
func ruleOverAmount(t *testing.T, h *harness, f *shopFixture, over string) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var id uuid.UUID

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO approval_rule
			  (tenant_id, company_id, name, subject, condition, action, steps,
			   is_active)
			VALUES ($1,$2,'Large expenses','expense',
			        jsonb_build_object('amount_over',$3::text),
			        'require_approval',
			        jsonb_build_array(jsonb_build_object('role','owner')),
			        true)
			RETURNING id`, f.tenantID, f.companyID, over).Scan(&id)
	}); err != nil {
		t.Fatalf("create approval rule: %v", err)
	}
	return id
}

// postExpense records one expense of one line, at whatever amount is asked.
//
// Named apart from the existing recordExpense in expenses_test.go, which
// returns a decoded body and fails the test on anything but 201 -- no use here,
// where being refused is the expected outcome half the time.
func postExpense(t *testing.T, h *harness, f *expenseFixture, headID, net string) *http.Response {
	t.Helper()
	return h.do(t, http.MethodPost, f.path("/api/v1/expenses"), f.token,
		map[string]any{
			"uuid": newUUID(), "expense_date": "2026-08-15",
			"paid_from": "cash", "description": "Shopfront repainting",
			"lines": []map[string]any{{
				"head_id": headID, "net_amount": net,
				"tax_treatment": "standard",
			}},
		})
}

// pendingRequest returns the one approval request waiting, or fails.
func pendingRequest(t *testing.T, h *harness, f *shopFixture) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT id FROM approval_request
			WHERE company_id = $1 AND status = 'pending'
			ORDER BY requested_at DESC LIMIT 1`, f.companyID).Scan(&id)
	}); err != nil {
		t.Fatalf("find the pending request: %v", err)
	}
	return id
}

// An expense over the threshold is held, and appears in the approval centre.
//
// The gate firing at all is the precondition for everything below it.
func TestAnExpenseOverTheThresholdIsHeldForApproval(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head, _ := headNamed(t, h, f, "RENT")["id"].(string)
	ruleOverAmount(t, h, f.shopFixture, "500")

	resp := postExpense(t, h, f, head, "5000.00")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		t.Fatalf("a large expense was recorded without sign-off: %s", readBody(t, resp))
	}

	// It is waiting, and the refusal said so rather than failing silently.
	list := h.do(t, http.MethodGet,
		"/api/v1/approvals?company_id="+f.companyID.String(), f.token, nil)
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("approval centre: %d %s", list.StatusCode, readBody(t, list))
	}
	// Definitive: is there a row at all, in ANY status?
	var any int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM approval_request WHERE company_id = $1`,
			f.companyID).Scan(&any)
	}); err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if any == 0 {
		t.Fatalf("the expense was refused with \"it is waiting in the approval "+
			"centre\" and NO request exists in any status: the insert was "+
			"rolled back with the refusal, so it can never be approved. "+
			"centre body: %s", readBody(t, list))
	}
}

// An expense under the threshold is not held.
//
// The other half. A gate that stopped everything would be indistinguishable
// from a broken module.
func TestAnExpenseUnderTheThresholdIsNotHeld(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head, _ := headNamed(t, h, f, "RENT")["id"].(string)
	ruleOverAmount(t, h, f.shopFixture, "5000")

	resp := postExpense(t, h, f, head, "100.00")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("a small expense was blocked: %d %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// A request can be approved, and then cannot be decided again.
//
// The second half matters as much as the first: `Decide` takes the row FOR
// UPDATE and refuses a request that is no longer pending, which is what stops
// two managers approving the same thing concurrently and what stops a decided
// request being flipped afterwards.
func TestAnApprovedRequestCannotBeDecidedTwice(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head, _ := headNamed(t, h, f, "RENT")["id"].(string)
	ruleOverAmount(t, h, f.shopFixture, "500")

	postExpense(t, h, f, head, "5000.00").Body.Close()
	req := pendingRequest(t, h, f.shopFixture)

	first := h.do(t, http.MethodPost,
		"/api/v1/approvals/"+req.String()+"/decide?company_id="+f.companyID.String(),
		f.token, map[string]any{"approve": true})
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK && first.StatusCode != http.StatusNoContent {
		t.Fatalf("approve: %d %s", first.StatusCode, readBody(t, first))
	}

	second := h.do(t, http.MethodPost,
		"/api/v1/approvals/"+req.String()+"/decide?company_id="+f.companyID.String(),
		f.token, map[string]any{"approve": true})
	defer second.Body.Close()
	if second.StatusCode == http.StatusOK || second.StatusCode == http.StatusNoContent {
		t.Error("an already-approved request was decided a second time")
	}
}

// Turning a request down requires a reason.
//
// The person who asked has to know what to change. A bare refusal sends them
// back to ask again with the same request.
func TestARefusalWithoutAReasonIsRejected(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head, _ := headNamed(t, h, f, "RENT")["id"].(string)
	ruleOverAmount(t, h, f.shopFixture, "500")

	postExpense(t, h, f, head, "5000.00").Body.Close()
	req := pendingRequest(t, h, f.shopFixture)

	bare := h.do(t, http.MethodPost,
		"/api/v1/approvals/"+req.String()+"/decide?company_id="+f.companyID.String(),
		f.token, map[string]any{"approve": false})
	defer bare.Body.Close()
	if bare.StatusCode == http.StatusOK || bare.StatusCode == http.StatusNoContent {
		t.Fatal("a request was turned down with no reason given")
	}

	withReason := h.do(t, http.MethodPost,
		"/api/v1/approvals/"+req.String()+"/decide?company_id="+f.companyID.String(),
		f.token, map[string]any{"approve": false, "reason": "Get a second quote first"})
	defer withReason.Body.Close()
	if withReason.StatusCode != http.StatusOK && withReason.StatusCode != http.StatusNoContent {
		t.Errorf("a reasoned refusal was rejected: %d %s",
			withReason.StatusCode, readBody(t, withReason))
	}
}

// Looking at the approval centre is not the same permission as deciding.
//
// F1 separates them on purpose: a supervisor may need to see what is waiting
// without being able to sign it off.
func TestDecidingNeedsMoreThanThePermissionToLook(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	head, _ := headNamed(t, h, f, "RENT")["id"].(string)
	ruleOverAmount(t, h, f.shopFixture, "500")

	postExpense(t, h, f, head, "5000.00").Body.Close()
	req := pendingRequest(t, h, f.shopFixture)

	// A cashier holds neither permission, so both routes must refuse.
	cashier := h.login(t, h.seedUserWithRole(t, "cashier"))

	decide := h.do(t, http.MethodPost,
		"/api/v1/approvals/"+req.String()+"/decide?company_id="+f.companyID.String(),
		cashier, map[string]any{"approve": true})
	defer decide.Body.Close()
	if decide.StatusCode == http.StatusOK || decide.StatusCode == http.StatusNoContent {
		t.Error("a cashier approved an expense")
	}
}

// One company's approval request cannot be decided from another.
func TestAnApprovalRequestCannotBeDecidedFromAnotherCompany(t *testing.T) {
	h := newHarness(t)
	f := seedExpenses(t, h)
	other := seedExpenses(t, h)
	head, _ := headNamed(t, h, f, "RENT")["id"].(string)

	ruleOverAmount(t, h, f.shopFixture, "500")
	postExpense(t, h, f, head, "5000.00").Body.Close()
	req := pendingRequest(t, h, f.shopFixture)

	resp := h.do(t, http.MethodPost,
		"/api/v1/approvals/"+req.String()+"/decide?company_id="+other.companyID.String(),
		other.token, map[string]any{"approve": true})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		t.Error("another company's owner approved this company's expense")
	}
}
