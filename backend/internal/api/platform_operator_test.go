//go:build integration

package api

// Platform administrators: adding one, correcting one, taking access away.
//
// The property under test that matters most is that these routes touch
// PLATFORM accounts only. They are reachable by the one actor who sits above
// every tenant, and a route that would edit a business's own staff from up
// there is the interference the actor model exists to prevent.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// loginWith signs in with a password this test knows, rather than the shared
// one every seeded account uses. A generated one-time password is the only
// thing a new administrator has, so it is the only way to prove the account
// they were handed actually works.
func loginWith(t *testing.T, h *harness, email, password string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := http.Post(h.server.URL+"/api/v1/auth/login",
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("login as %s: %d %s", email, resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return out
}

// operatorRows reads the platform account list as the admin sees it.
func operatorRows(t *testing.T, h *harness, admin string) []map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet, "/api/v1/platform/operators", admin, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list operators: %d %s", resp.StatusCode, readBody(t, resp))
	}
	raw, _ := decodeJSON(t, resp)["data"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if row, ok := r.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

func TestPlatformOperatorLifecycle(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	// The caller is in their own list, and marked as themselves. A screen uses
	// that to stop offering the actions that would lock the reader out.
	var self map[string]any
	for _, row := range operatorRows(t, h, admin) {
		if row["self"] == true {
			self = row
		}
	}
	if self == nil {
		t.Fatal("the operator list does not contain the caller")
	}

	email := "ops" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
	resp := h.do(t, http.MethodPost, "/api/v1/platform/operators", admin,
		map[string]any{"email": email, "full_name": "Second Administrator"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add operator: %d %s", resp.StatusCode, readBody(t, resp))
	}
	created := decodeJSON(t, resp)
	temp, _ := created["temporary_password"].(string)
	if temp == "" {
		t.Fatal("adding an administrator returned no one-time password, so " +
			"the account cannot be handed over")
	}
	op, _ := created["operator"].(map[string]any)
	newID, _ := op["id"].(string)
	if newID == "" {
		t.Fatal("the created operator has no id")
	}
	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM app_user WHERE id = $1`, newID)
			return err
		})
	})

	// The password is one-time in the sense that matters: it works once and
	// the account cannot go on being used on it.
	if op["must_change_password"] != true {
		t.Error("a password an administrator typed into a chat window is " +
			"not one the account should keep working on")
	}
	if op["status"] != "active" {
		t.Errorf("new operator status = %v, want active", op["status"])
	}

	// The same address twice is refused rather than silently making a second
	// account somebody has to notice.
	dup := h.do(t, http.MethodPost, "/api/v1/platform/operators", admin,
		map[string]any{"email": email, "full_name": "Duplicate"})
	dup.Body.Close()
	if dup.StatusCode < 400 || dup.StatusCode >= 500 {
		t.Errorf("adding the same email twice = %d, want a 4xx refusal",
			dup.StatusCode)
	}

	// Correcting the address changes the account that exists.
	moved := "moved" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
	resp2 := h.do(t, http.MethodPut,
		"/api/v1/platform/operators/"+newID+"/email", admin,
		map[string]any{"email": moved})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("change email: %d %s", resp2.StatusCode, readBody(t, resp2))
	}

	seen := 0
	for _, row := range operatorRows(t, h, admin) {
		if row["id"] == newID {
			seen++
			if row["email"] != moved {
				t.Errorf("email after change = %v, want %s", row["email"], moved)
			}
		}
		if row["email"] == email {
			t.Error("the old address is still an account: the change created " +
				"a second administrator instead of correcting one")
		}
	}
	if seen != 1 {
		t.Errorf("the changed operator appears %d times, want once", seen)
	}

	// The change is in the audit log with both addresses, which is the whole
	// reason it is done here rather than in SQL.
	ctx := context.Background()
	var before, after string
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT coalesce(before_value->>'email',''), coalesce(after_value->>'email','')
			FROM audit_log
			WHERE action = 'platform_operator_email_changed'
			  AND entity_id = $1
			ORDER BY occurred_at DESC LIMIT 1`, newID).Scan(&before, &after)
	}); err != nil {
		t.Fatalf("read the audit entry for the change: %v", err)
	}
	if before != email || after != moved {
		t.Errorf("audit says %q -> %q, want %q -> %q", before, after, email, moved)
	}

	// The handed-over password works, once, and the account is told to change
	// it. This is the end-to-end proof that adding an administrator produces
	// an account somebody can actually sign into.
	first := loginWith(t, h, moved, temp)
	if first["must_change_password"] != true {
		t.Error("the new administrator was not asked to change the password " +
			"they were given over a chat window")
	}
	if tok, _ := first["access_token"].(string); tok == "" {
		t.Fatal("signing in with the one-time password returned no token")
	}

	// Taking access away ends the account's sessions, so a token issued a
	// minute ago is not still good.
	resp3 := h.do(t, http.MethodPut,
		"/api/v1/platform/operators/"+newID+"/status", admin,
		map[string]any{"status": "disabled"})
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("disable operator: %d %s", resp3.StatusCode, readBody(t, resp3))
	}

	var live int
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM user_session
			WHERE user_id = $1 AND revoked_at IS NULL`, newID).Scan(&live)
	}); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if live != 0 {
		t.Errorf("%d session(s) still live after disabling the account: "+
			"access that has not actually been taken away", live)
	}

	// And signing in again is refused, rather than issuing a fresh token to an
	// account whose access was withdrawn.
	body, _ := json.Marshal(map[string]string{"email": moved, "password": temp})
	again, err := http.Post(h.server.URL+"/api/v1/auth/login",
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("sign in after disabling: %v", err)
	}
	defer again.Body.Close()
	if again.StatusCode < 400 {
		t.Errorf("a disabled administrator signed in = %d, want a refusal",
			again.StatusCode)
	}

	// And it can be given back.
	resp4 := h.do(t, http.MethodPut,
		"/api/v1/platform/operators/"+newID+"/status", admin,
		map[string]any{"status": "active"})
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Errorf("re-enable operator: %d", resp4.StatusCode)
	}
}

func TestAnAdministratorCannotDisableThemselves(t *testing.T) {
	h := newHarness(t)
	adminEmail := h.seedSuperAdmin(t)
	admin := h.login(t, adminEmail)

	var selfID string
	for _, row := range operatorRows(t, h, admin) {
		if row["self"] == true {
			selfID, _ = row["id"].(string)
		}
	}
	if selfID == "" {
		t.Fatal("the caller is not in the operator list")
	}

	resp := h.do(t, http.MethodPut,
		"/api/v1/platform/operators/"+selfID+"/status", admin,
		map[string]any{"status": "disabled"})
	defer resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("disabling your own administrator account = %d, want a "+
			"refusal: nothing in the product can create a replacement on a "+
			"running deployment", resp.StatusCode)
	}
}

func TestOperatorRoutesRefuseTenantAccounts(t *testing.T) {
	h := newHarness(t)
	admin := platformAdmin(t, h)

	// A business owner: a real account, with a tenant.
	owner := h.seedUserWithRole(t, "owner")

	var ownerID uuid.UUID
	ctx := context.Background()
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM app_user WHERE email = $1`, owner).Scan(&ownerID)
	}); err != nil {
		t.Fatalf("find the owner: %v", err)
	}

	// The platform control plane administers the platform. A business's staff
	// are that business's to manage, and these routes say so rather than
	// quietly editing one.
	resp := h.do(t, http.MethodPut,
		"/api/v1/platform/operators/"+ownerID.String()+"/email", admin,
		map[string]any{"email": "someone@example.test"})
	defer resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Errorf("changing a tenant user's email through the platform route "+
			"= %d, want a refusal", resp.StatusCode)
	}

	resp2 := h.do(t, http.MethodPut,
		"/api/v1/platform/operators/"+ownerID.String()+"/status", admin,
		map[string]any{"status": "disabled"})
	defer resp2.Body.Close()
	if resp2.StatusCode < 400 || resp2.StatusCode >= 500 {
		t.Errorf("disabling a tenant user through the platform route = %d, "+
			"want a refusal", resp2.StatusCode)
	}

	// And the owner cannot read the list of who administers the platform.
	ownerToken := h.login(t, owner)
	resp3 := h.do(t, http.MethodGet, "/api/v1/platform/operators", ownerToken, nil)
	defer resp3.Body.Close()
	// 404, not 403, and deliberately: confirming that a platform endpoint
	// exists tells somebody probing where to aim. `RequireSuperAdmin` answers
	// every route on the control plane this way.
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("a business owner reading the platform operator list = %d, "+
			"want 404", resp3.StatusCode)
	}
}
