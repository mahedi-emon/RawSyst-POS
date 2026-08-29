//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The gap this module closed: a shop onboards and then has no way to create the
// cashier who works the till. Everything downstream assumes those users exist —
// the cashier role, shift ownership, blind close, and the "who counted this
// drawer" trail that makes a cash difference attributable to a person.
func TestAnOwnerCanAddACashierWhoCanThenSignIn(t *testing.T) {
	h := newHarness(t)
	ownerEmail := h.seedUserWithRole(t, "owner")
	ownerToken := h.login(t, ownerEmail)

	// The roles an owner may hand over.
	rolesResp := h.do(t, http.MethodGet, "/api/v1/people/roles", ownerToken, nil)
	defer rolesResp.Body.Close()
	if rolesResp.StatusCode != http.StatusOK {
		t.Fatalf("listing roles = %d: %s", rolesResp.StatusCode, readBody(t, rolesResp))
	}
	var roles struct {
		Data []struct {
			ID         string `json:"id"`
			Key        string `json:"key"`
			Assignable bool   `json:"assignable"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rolesResp.Body).Decode(&roles); err != nil {
		t.Fatalf("decode roles: %v", err)
	}

	var cashierRole string
	for _, r := range roles.Data {
		if r.Key == "cashier" {
			cashierRole = r.ID
			// An Owner holds everything, so every seeded role is assignable.
			if !r.Assignable {
				t.Error("an Owner cannot assign the cashier role, which means the " +
					"subset rule is refusing a superset")
			}
		}
	}
	if cashierRole == "" {
		t.Fatal("no cashier role is offered, so no till can ever be staffed")
	}

	// Add the cashier.
	email := "cashier" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10] + "@example.test"
	created := h.doRaw(t, http.MethodPost, "/api/v1/people", ownerToken,
		`{"email":"`+email+`","full_name":"Counter Staff","role_id":"`+cashierRole+`"}`)
	defer created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("creating a cashier = %d: %s", created.StatusCode, readBody(t, created))
	}
	var out struct {
		Data struct {
			Person struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"person"`
			TemporaryPassword string `json:"temporary_password"`
		} `json:"data"`
	}
	if err := json.NewDecoder(created.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Data.TemporaryPassword == "" {
		t.Fatal("no password was issued, so the account cannot be handed to anybody")
	}
	if out.Data.Person.Status != "invited" {
		t.Errorf("a new person's status = %q, want invited", out.Data.Person.Status)
	}

	// The whole point: they can sign in, and are told to change it.
	loginResp := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": email, "password": out.Data.TemporaryPassword})
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("the new cashier cannot sign in: %d %s",
			loginResp.StatusCode, readBody(t, loginResp))
	}
	var session struct {
		AccessToken        string `json:"access_token"`
		MustChangePassword bool   `json:"must_change_password"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&session); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if !session.MustChangePassword {
		t.Error("a one-time password did not require changing on first use")
	}

	// And they hold the cashier's permissions, not nothing.
	meResp := h.do(t, http.MethodGet, "/api/v1/auth/me", session.AccessToken, nil)
	defer meResp.Body.Close()
	var me struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(meResp.Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	held := map[string]bool{}
	for _, p := range me.Permissions {
		held[p] = true
	}
	if !held["sales.create"] {
		t.Errorf("the new cashier cannot ring up a sale; permissions = %v", me.Permissions)
	}
	if held["accounting.view"] {
		t.Error("the new cashier can read the ledger, which the cashier role does not carry")
	}
}

// Delegation is not escalation.
//
// Somebody with `identity.manage_roles` may give away what they have. Giving
// away what they do NOT have is how a store manager becomes an Owner — create
// an Owner account, sign in as it, and the tenant boundary has held while every
// boundary inside it has gone.
func TestAStoreManagerCannotCreateAnOwner(t *testing.T) {
	h := newHarness(t)

	// A store manager, plus the two verbs that let them manage staff at all.
	// Without them the request is refused for a different reason and the test
	// would prove nothing about the subset rule.
	managerEmail := h.seedUserWithRole(t, "store_manager")
	h.grantPermissions(t, managerEmail, "identity.view", "identity.create", "identity.manage_roles")
	managerToken := h.login(t, managerEmail)

	resp := h.do(t, http.MethodGet, "/api/v1/people/roles", managerToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing roles = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var roles struct {
		Data []struct {
			ID         string   `json:"id"`
			Key        string   `json:"key"`
			Assignable bool     `json:"assignable"`
			Withheld   []string `json:"withheld_permissions"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&roles); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var ownerRole string
	for _, r := range roles.Data {
		if r.Key != "owner" {
			continue
		}
		ownerRole = r.ID
		if r.Assignable {
			t.Error("a store manager is offered the Owner role as assignable")
		}
		if len(r.Withheld) == 0 {
			t.Error("the Owner role is marked unassignable with no reason given, " +
				"so the screen cannot say why it is greyed out")
		}
	}
	if ownerRole == "" {
		t.Fatal("the Owner role was not listed at all")
	}

	// The list marking it is a convenience. This is the control.
	email := "escalate" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10] + "@example.test"
	attempt := h.doRaw(t, http.MethodPost, "/api/v1/people", managerToken,
		`{"email":"`+email+`","full_name":"Not An Owner","role_id":"`+ownerRole+`"}`)
	defer attempt.Body.Close()
	if attempt.StatusCode != http.StatusForbidden {
		t.Fatalf("a store manager created an Owner: status %d, body %s",
			attempt.StatusCode, readBody(t, attempt))
	}

	// The refusal names what was withheld, so the person reading it can act.
	body := readBody(t, attempt)
	if !strings.Contains(body, "identity.manage_roles") &&
		!strings.Contains(body, "accounting") {
		t.Errorf("the refusal does not say which permission was the problem: %s", body)
	}
}

// You cannot lock yourself out.
//
// Suspending your own account, or removing your own last role, leaves a login
// that can do nothing and no way to fix it from inside the tenant. A4.2's
// Super-Admin-assisted recovery is the route out, and it involves a phone call.
func TestYouCannotLockYourselfOut(t *testing.T) {
	h := newHarness(t)
	ownerEmail := h.seedUserWithRole(t, "owner")
	token := h.login(t, ownerEmail)

	var me struct {
		UserID string `json:"user_id"`
	}
	meResp := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	defer meResp.Body.Close()
	if err := json.NewDecoder(meResp.Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.UserID == "" {
		t.Fatal("/auth/me did not say who the caller is")
	}

	suspend := h.doRaw(t, http.MethodPost,
		"/api/v1/people/"+me.UserID+"/active", token, `{"active":false}`)
	defer suspend.Body.Close()
	if suspend.StatusCode < 400 {
		t.Fatalf("an owner suspended their own account: status %d", suspend.StatusCode)
	}

	// And the last role.
	// In the TENANT plane. `user_role_assignment` carries row-level security on
	// the tenant, so the platform plane — where current_tenant_id() is NULL —
	// sees none of it. That is the isolation working, not a missing row.
	var tenantID uuid.UUID
	if err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT tenant_id FROM app_user WHERE email = $1`, ownerEmail).Scan(&tenantID)
	}); err != nil {
		t.Fatalf("reading the tenant: %v", err)
	}

	var assignmentID string
	if err := h.pool.TxAsTenant(context.Background(), tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT id::text FROM user_role_assignment WHERE user_id = $1 LIMIT 1`,
			me.UserID).Scan(&assignmentID)
	}); err != nil {
		t.Fatalf("reading the owner's assignment: %v", err)
	}

	remove := h.do(t, http.MethodDelete,
		"/api/v1/people/roles/"+assignmentID, token, nil)
	defer remove.Body.Close()
	if remove.StatusCode < 400 {
		t.Fatalf("an owner removed their own last role: status %d", remove.StatusCode)
	}
}

// A plan's user ceiling is a real ceiling. Blueprint A3 requires every
// "unlimited" claim to become a concrete, testable number.
func TestTheUserCeilingIsEnforced(t *testing.T) {
	h := newHarness(t)
	ownerEmail := h.seedUserWithRole(t, "owner")
	token := h.login(t, ownerEmail)

	var tenantID uuid.UUID
	if err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT tenant_id FROM app_user WHERE email = $1`, ownerEmail).Scan(&tenantID)
	}); err != nil {
		t.Fatalf("reading the tenant: %v", err)
	}

	// Set the ceiling to exactly what is already used, so the next add is the
	// one over the line.
	if err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		_, e := tx.Exec(context.Background(), `
			UPDATE tenant_limit
			   SET max_users = (SELECT count(*) FROM app_user
			                     WHERE tenant_id = $1 AND status <> 'disabled')
			 WHERE tenant_id = $1`, tenantID)
		return e
	}); err != nil {
		t.Fatalf("setting the ceiling: %v", err)
	}

	roleID := h.roleID(t, "cashier")
	email := "overtheline" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8] + "@example.test"
	resp := h.doRaw(t, http.MethodPost, "/api/v1/people", token,
		`{"email":"`+email+`","full_name":"One Too Many","role_id":"`+roleID+`"}`)
	defer resp.Body.Close()

	if resp.StatusCode < 400 {
		t.Fatalf("the user ceiling was not enforced: status %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	// The refusal has to say the number, or an owner cannot tell whether to
	// buy a bigger plan or disable somebody who has left.
	if !strings.Contains(body, "plan allows") {
		t.Errorf("the refusal does not name the ceiling: %s", body)
	}
}

// Somebody who has left is disabled, never deleted: their name is on the
// invoices they rang up, the shifts they counted and the audit log. Deleting
// the row would leave a trail of missing people, which is what an audit trail
// exists to prevent.
func TestSuspendingSomebodyEndsTheirSessionAndKeepsTheirRecord(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.login(t, h.seedUserWithRole(t, "owner"))

	roleID := h.roleID(t, "cashier")
	email := "leaver" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10] + "@example.test"
	created := h.doRaw(t, http.MethodPost, "/api/v1/people", ownerToken,
		`{"email":"`+email+`","full_name":"Has Left","role_id":"`+roleID+`"}`)
	defer created.Body.Close()
	var out struct {
		Data struct {
			Person struct {
				ID string `json:"id"`
			} `json:"person"`
			TemporaryPassword string `json:"temporary_password"`
		} `json:"data"`
	}
	if err := json.NewDecoder(created.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// They sign in, then are suspended mid-session.
	loginResp := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": email, "password": out.Data.TemporaryPassword})
	defer loginResp.Body.Close()
	var session struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&session); err != nil {
		t.Fatalf("decode login: %v", err)
	}

	suspend := h.doRaw(t, http.MethodPost,
		"/api/v1/people/"+out.Data.Person.ID+"/active", ownerToken, `{"active":false}`)
	defer suspend.Body.Close()
	if suspend.StatusCode != http.StatusNoContent {
		t.Fatalf("suspending = %d: %s", suspend.StatusCode, readBody(t, suspend))
	}

	// Suspending is pointless if the session survives it.
	if session.RefreshToken != "" {
		refresh := h.do(t, http.MethodPost, "/api/v1/auth/refresh", "",
			map[string]string{"refresh_token": session.RefreshToken})
		defer refresh.Body.Close()
		if refresh.StatusCode < 400 {
			t.Errorf("a suspended person refreshed their session: %d", refresh.StatusCode)
		}
	}

	// The record survives, off the default list and on the full one.
	list := h.do(t, http.MethodGet, "/api/v1/people", ownerToken, nil)
	defer list.Body.Close()
	if strings.Contains(readBody(t, list), email) {
		t.Error("a disabled person is still on the default list")
	}

	all := h.do(t, http.MethodGet, "/api/v1/people?include_inactive=true", ownerToken, nil)
	defer all.Body.Close()
	if !strings.Contains(readBody(t, all), email) {
		t.Error("a disabled person has vanished from the full list, so their " +
			"name on old invoices now points at nobody")
	}
}
