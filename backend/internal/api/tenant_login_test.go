//go:build integration

package api

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
)

// P20: signing in when one email belongs to more than one business.
//
// Email is unique WITHIN a tenant, which is correct — a bookkeeper serving two
// shops, an owner with two companies and a shared ops address are all ordinary.
// Login used to look the account up with a bare `WHERE email = $1` and take
// whichever row came back, so one of those people could never sign in and was
// told their password was wrong.
//
// The property that matters most here is not that the picker appears. It is
// that naming a business you have no account in is refused exactly like a wrong
// password, and that nothing about which businesses hold an address is
// disclosed to somebody who does not have the password.

// planted puts the same email in a second tenant with its own password, and
// returns that tenant's id.
func planted(
	t *testing.T, h *harness, email, password string, tenantName string,
) uuid.UUID {
	t.Helper()
	ctx := t.Context()

	var tenantID, roleID, userID uuid.UUID
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO tenant (name, data_region, plan_tier)
			VALUES ($1, 'sa', 'business') RETURNING id`, tenantName).
			Scan(&tenantID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			SELECT id FROM role WHERE tenant_id IS NULL AND key = 'owner'`).
			Scan(&roleID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			INSERT INTO app_user
			  (tenant_id, email, full_name, password_hash, status)
			VALUES ($1,$2,'Second Account',$3,'active') RETURNING id`,
			tenantID, email, hashFor(t, password)).Scan(&userID)
	}); err != nil {
		t.Fatalf("plant second account: %v", err)
	}

	// The role assignment is a TENANT table, so row-level security correctly
	// refuses it on the platform plane. Two transactions rather than one
	// relaxed policy: the boundary is doing its job and the test should work
	// with it rather than around it.
	if err := h.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO user_role_assignment (tenant_id, user_id, role_id)
			VALUES ($1,$2,$3)`, tenantID, userID, roleID)
		return e
	}); err != nil {
		t.Fatalf("assign role in the second tenant: %v", err)
	}
	return tenantID
}

func login(t *testing.T, h *harness, body map[string]any) (int, map[string]any) {
	t.Helper()
	resp := h.do(t, "POST", "/api/v1/auth/login", "", body)
	if resp.StatusCode >= 500 {
		t.Fatalf("login: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if resp.StatusCode == 204 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, decodeJSON(t, resp)
}

// --- The unambiguous case must be untouched ------------------------------

func TestAUniqueEmailSignsInExactlyAsBefore(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	status, body := login(t, h, map[string]any{
		"email": f.email, "password": testPassword,
	})
	if status != 200 {
		t.Fatalf("login returned %d", status)
	}
	if body["access_token"] == nil || body["access_token"] == "" {
		t.Error("no access token for an unambiguous sign-in")
	}
	if body["tenant_choice_required"] != nil {
		t.Error("a unique email was asked to choose a business")
	}
}

// Naming a tenant when there is nothing to disambiguate must still work: a
// client that always sends it should not be penalised.
func TestAUniqueEmailMayNameItsOwnTenant(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	status, body := login(t, h, map[string]any{
		"email": f.email, "password": testPassword,
		"tenant_id": f.tenantID.String(),
	})
	if status != 200 || body["access_token"] == nil {
		t.Errorf("naming the correct tenant failed: %d %v", status, body["error"])
	}
}

func TestAWrongPasswordIsStillRefusedGenerically(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	status, body := login(t, h, map[string]any{
		"email": f.email, "password": "definitely-not-it",
	})
	if status != 401 {
		t.Errorf("got %d for a wrong password, want 401", status)
	}
	if err, _ := body["error"].(map[string]any); err != nil {
		if msg, _ := err["message"].(string); msg == "" {
			t.Error("no message on the refusal")
		}
	}
}

// --- The ambiguous case --------------------------------------------------

func TestAnEmailInTwoBusinessesIsAskedWhich(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	second := planted(t, h, f.email, testPassword, "Second Shop")

	status, body := login(t, h, map[string]any{
		"email": f.email, "password": testPassword,
	})
	if status != 200 {
		t.Fatalf("login returned %d %v", status, body["error"])
	}

	if required, _ := body["tenant_choice_required"].(bool); !required {
		t.Fatalf("no choice was offered; the caller cannot proceed: %v", body)
	}
	// Crucially, no session was opened.
	if body["access_token"] != nil && body["access_token"] != "" {
		t.Error("a token was issued before the business was chosen")
	}

	tenants, _ := body["tenants"].([]any)
	if len(tenants) != 2 {
		t.Fatalf("offered %d businesses, want 2", len(tenants))
	}
	seen := map[string]bool{}
	for _, raw := range tenants {
		row, _ := raw.(map[string]any)
		id, _ := row["tenant_id"].(string)
		seen[id] = true
		if name, _ := row["name"].(string); name == "" {
			t.Error("a business is offered with no name to recognise it by")
		}
	}
	if !seen[f.tenantID.String()] || !seen[second.String()] {
		t.Errorf("the offered businesses are not the two the account is in: %v", seen)
	}
}

func TestChoosingABusinessSignsInToThatOne(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	second := planted(t, h, f.email, testPassword, "Second Shop")

	for _, want := range []uuid.UUID{f.tenantID, second} {
		status, body := login(t, h, map[string]any{
			"email": f.email, "password": testPassword,
			"tenant_id": want.String(),
		})
		if status != 200 {
			t.Fatalf("choosing %s returned %d %v", want, status, body["error"])
		}
		token, _ := body["access_token"].(string)
		if token == "" {
			t.Fatalf("no token after choosing %s", want)
		}

		// And the session really is in that tenant, not the other one. Read
		// through /auth/me rather than trusting the response.
		me := decodeJSON(t, h.do(t, "GET", "/api/v1/auth/me", token, nil))
		if me["tenant_id"] != want.String() {
			t.Errorf("chose %s but the session is in %v", want, me["tenant_id"])
		}
	}
}

// The security property. Naming a business you have no account in must be
// indistinguishable from guessing a password.
func TestNamingABusinessYouAreNotInIsRefused(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	status, body := login(t, h, map[string]any{
		"email": mine.email, "password": testPassword,
		"tenant_id": theirs.tenantID.String(),
	})
	if status != 401 {
		t.Fatalf("got %d naming another tenant, want 401: %v", status, body)
	}
	if body["access_token"] != nil && body["access_token"] != "" {
		t.Fatal("a token was issued for a tenant the user has no account in")
	}
}

// A tenant id that is not a tenant at all gets the same answer, so the response
// does not confirm which ids exist.
func TestNamingANonexistentBusinessIsRefusedTheSameWay(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	status, _ := login(t, h, map[string]any{
		"email": f.email, "password": testPassword,
		"tenant_id": uuid.NewString(),
	})
	if status != 401 {
		t.Errorf("got %d for an unknown tenant, want the same 401 as a wrong "+
			"password", status)
	}
}

// The disclosure property. Somebody without the password must learn nothing
// about which businesses hold an address.
func TestAWrongPasswordRevealsNoBusinesses(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	planted(t, h, f.email, testPassword, "Second Shop")

	status, body := login(t, h, map[string]any{
		"email": f.email, "password": "wrong-on-purpose",
	})
	if status != 401 {
		t.Fatalf("got %d, want 401", status)
	}
	if body["tenants"] != nil {
		t.Error("the businesses holding this address were disclosed to somebody " +
			"who does not have the password")
	}
	if required, _ := body["tenant_choice_required"].(bool); required {
		t.Error("a choice was offered without a correct password")
	}
}

// Different tenants may hold different passwords for the same person. Only the
// accounts the password actually opens are theirs, so a password matching one
// of two signs straight in with no picker.
func TestADifferentPasswordPerBusinessNeedsNoChoice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	second := planted(t, h, f.email, "a-completely-different-password", "Second Shop")

	status, body := login(t, h, map[string]any{
		"email": f.email, "password": testPassword,
	})
	if status != 200 {
		t.Fatalf("login returned %d", status)
	}
	if required, _ := body["tenant_choice_required"].(bool); required {
		t.Fatal("a choice was offered when only one password matched")
	}

	token, _ := body["access_token"].(string)
	if token == "" {
		t.Fatal("no token issued")
	}
	me := decodeJSON(t, h.do(t, "GET", "/api/v1/auth/me", token, nil))
	if me["tenant_id"] != f.tenantID.String() {
		t.Errorf("signed in to %v, want the tenant whose password matched (%s), "+
			"not %s", me["tenant_id"], f.tenantID, second)
	}
}

// --- Isolation after the choice ------------------------------------------

// M8 still holds through the new path: a session opened by choosing one
// business cannot read the other's data.
func TestASessionFromAChoiceCannotCrossIntoTheOtherBusiness(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	otherTenant := planted(t, h, mine.email, testPassword, "Second Shop")

	status, body := login(t, h, map[string]any{
		"email": mine.email, "password": testPassword,
		"tenant_id": otherTenant.String(),
	})
	if status != 200 {
		t.Fatalf("login returned %d", status)
	}
	token, _ := body["access_token"].(string)

	// That session belongs to the second business, which has no companies. It
	// must not be able to reach the first one's.
	resp := h.do(t, "GET",
		"/api/v1/dashboard/overview?company_id="+mine.companyID.String(), token, nil)
	if resp.StatusCode == 200 {
		t.Fatal("a session in one business read another's dashboard")
	}
	if resp.StatusCode != 404 && resp.StatusCode != 403 {
		t.Errorf("got %d, want 404 or 403", resp.StatusCode)
	}

	// And its own company list is empty rather than showing the other's.
	companies := decodeJSON(t, h.do(t, "GET", "/api/v1/companies", token, nil))
	list, _ := companies["data"].([]any)
	if len(list) != 0 {
		t.Errorf("the second business sees %d companies, want none of its own "+
			"and none of anybody else's", len(list))
	}
}

// A session opened through the picker is an ordinary session: it refreshes,
// carries permissions, and survives being used.
func TestASessionFromAChoiceBehavesLikeAnyOther(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	planted(t, h, f.email, testPassword, "Second Shop")

	status, body := login(t, h, map[string]any{
		"email": f.email, "password": testPassword,
		"tenant_id": f.tenantID.String(),
	})
	if status != 200 {
		t.Fatalf("login returned %d", status)
	}
	token, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	if refresh == "" {
		t.Fatal("no refresh token issued through the choice path")
	}

	me := decodeJSON(t, h.do(t, "GET", "/api/v1/auth/me", token, nil))
	perms, _ := me["permissions"].([]any)
	if len(perms) == 0 {
		t.Error("the session carries no permissions")
	}

	// The refresh chain works, so the session is a real one rather than a
	// one-shot token.
	refreshed := h.do(t, "POST", "/api/v1/auth/refresh", "",
		map[string]any{"refresh_token": refresh})
	if refreshed.StatusCode != 200 {
		t.Errorf("refresh returned %d: %s", refreshed.StatusCode, readBody(t, refreshed))
	}
}

// A failed attempt must not lock the person out of a different business they
// are also in.
func TestAFailedAttemptDoesNotLockTheOtherBusiness(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	second := planted(t, h, f.email, "second-password-here", "Second Shop")

	// Wrong for the second account, right for the first. The first signs in;
	// the second's counter goes up.
	for i := 0; i < 3; i++ {
		if status, _ := login(t, h, map[string]any{
			"email": f.email, "password": testPassword,
		}); status != 200 {
			t.Fatalf("attempt %d: the correct password was refused (%d)", i, status)
		}
	}

	// The second account is still reachable with its own password.
	status, body := login(t, h, map[string]any{
		"email": f.email, "password": "second-password-here",
		"tenant_id": second.String(),
	})
	if status != 200 {
		t.Errorf("the second business is locked out at %d after failures that "+
			"were never against it: %v", status, body["error"])
	}
}

// hashFor hashes a password the same way the product does, so a planted
// account is indistinguishable from a real one.
func hashFor(t *testing.T, password string) string {
	t.Helper()
	h, err := identity.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}
