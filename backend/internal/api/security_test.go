//go:build integration

package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
)

// The three properties the second factor and the role builder exist for.
//
//   - A password alone does not open an account with a second factor.
//   - A recovery code works once and never again.
//   - Nobody can put into a role a permission they do not hold themselves,
//     which is the escalation the builder would otherwise be.

// enrolMFA turns the second factor on for the fixture's user and returns the
// secret and the recovery codes.
func enrolMFA(t *testing.T, h *harness, f *shopFixture) (string, []string) {
	t.Helper()

	begin := h.do(t, http.MethodPost, "/api/v1/auth/mfa/begin", f.token, nil)
	if begin.StatusCode != http.StatusOK {
		t.Fatalf("beginning MFA: %s", readBody(t, begin))
	}
	enrolment, _ := decodeJSON(t, begin)["enrolment"].(map[string]any)
	secret, _ := enrolment["secret"].(string)
	if secret == "" {
		t.Fatal("enrolment returned no secret")
	}

	// The code the app on the phone would be showing right now.
	code := totpNow(t, secret)
	done := h.do(t, http.MethodPost, "/api/v1/auth/mfa/complete", f.token,
		map[string]any{"code": code})
	if done.StatusCode != http.StatusOK {
		t.Fatalf("completing MFA: %s", readBody(t, done))
	}

	raw, _ := decodeJSON(t, done)["recovery_codes"].([]any)
	codes := make([]string, 0, len(raw))
	for _, c := range raw {
		if s, ok := c.(string); ok {
			codes = append(codes, s)
		}
	}
	if len(codes) == 0 {
		t.Fatal("completing MFA returned no recovery codes")
	}
	return secret, codes
}

// totpNow produces the code a correct authenticator would show.
//
// It brute-forces the six digits against the package's own verifier rather than
// reimplementing the algorithm in the test. A million iterations is fast, and
// the point is to prove the SERVER accepts what a real app produces — a second
// implementation here could agree with the first while both were wrong.
// It must also still be valid when the SERVER checks it, a moment later. The
// verifier allows one step either side, so three of the million codes are
// accepted right now and one of them belongs to the step that has just ended —
// which expires the instant the clock rolls over and made this test fail about
// one run in fifty. Requiring the code to hold thirty seconds from now pins it
// to the current step or the next, and both are still good when the request
// lands.
func totpNow(t *testing.T, secret string) string {
	t.Helper()
	now := time.Now()
	for i := 0; i < 1000000; i++ {
		code := pad6(i)
		if !identity.VerifyTOTP(secret, code, now) {
			continue
		}
		if identity.VerifyTOTP(secret, code, now.Add(30*time.Second)) {
			return code
		}
	}
	t.Fatal("no six-digit code satisfies this secret, which cannot happen")
	return ""
}

func pad6(v int) string {
	out := []byte("000000")
	for i := 5; i >= 0 && v > 0; i-- {
		out[i] = byte('0' + v%10)
		v /= 10
	}
	return string(out)
}

func TestAPasswordAloneDoesNotOpenAnAccountWithASecondFactor(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	secret, _ := enrolMFA(t, h, f)

	// The password on its own. Right password, right account, and no session:
	// a challenge instead.
	resp := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]any{"email": f.email, "password": testPassword})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signing in: %s", readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	if body["mfa_required"] != true {
		t.Fatalf("the password alone did not raise the challenge: %v", body)
	}
	// The field is a plain string on the response type, so a challenge sends
	// it empty rather than omitting it.
	if token, _ := body["access_token"].(string); token != "" {
		t.Fatal("a challenge issued an access token, which defeats the point")
	}

	// And with the code, it opens.
	resp = h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]any{
			"email": f.email, "password": testPassword,
			"mfa_code": totpNow(t, secret),
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signing in with a code: %s", readBody(t, resp))
	}
	body = decodeJSON(t, resp)
	if token, _ := body["access_token"].(string); token == "" {
		t.Fatalf("a correct code did not open the account: %v", body)
	}
}

func TestARecoveryCodeWorksOnceAndNeverAgain(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	_, codes := enrolMFA(t, h, f)
	code := codes[0]

	first := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]any{
			"email": f.email, "password": testPassword, "mfa_code": code,
		})
	if first.StatusCode != http.StatusOK {
		t.Fatalf("signing in with a recovery code: %s", readBody(t, first))
	}
	if token, _ := decodeJSON(t, first)["access_token"].(string); token == "" {
		t.Fatal("a recovery code did not open the account")
	}

	// The same code again. It was spent by the sign-in that accepted it, and a
	// second use must be refused exactly as a wrong one is.
	second := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]any{
			"email": f.email, "password": testPassword, "mfa_code": code,
		})
	if second.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a spent recovery code answered %d; expected 401: %s",
			second.StatusCode, readBody(t, second))
	}
}

func TestARoleCannotBeGivenAPermissionItsAuthorDoesNotHold(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// A store manager: holds `identity.manage_roles` in the seeded set? If not
	// the test grants it, because the point under test is the SUBSET rule, not
	// which role happens to carry the builder.
	f := h.seedShop(t, "store_manager")
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO role_permission (role_id, permission)
			SELECT r.id, 'identity.manage_roles'
			FROM role r
			JOIN user_role_assignment a ON a.role_id = r.id
			WHERE a.user_id = $1
			ON CONFLICT DO NOTHING`, f.userID)
		return e
	}); err != nil {
		t.Fatalf("granting the builder: %v", err)
	}

	// Sign in again so the token carries the new grant.
	token := h.login(t, f.email)

	// `accounting.reopen_period` reopens a closed month and changes figures
	// somebody has already reported. A store manager does not hold it, and
	// must not be able to write themselves a role that does.
	resp := h.do(t, http.MethodPost, "/api/v1/roles?company_id="+
		f.companyID.String(), token, map[string]any{
		"name":        "Everything",
		"permissions": []string{"accounting.reopen_period"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("building a role with a withheld permission answered %d; "+
			"expected 403: %s", resp.StatusCode, readBody(t, resp))
	}

	// And the positive control: a permission they DO hold goes in, so the rule
	// is a subset check rather than a blanket refusal.
	ok := h.do(t, http.MethodPost, "/api/v1/roles?company_id="+
		f.companyID.String(), token, map[string]any{
		"name":        "Just looking",
		"permissions": []string{"identity.manage_roles"},
	})
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("building a role from held permissions: %s", readBody(t, ok))
	}
}

func TestSigningOutOfOneSessionLeavesTheOthers(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// Two sessions signed in through the real route, so both appear in the
	// list. The fixture's own token is minted directly and has no user_session
	// row behind it, which is fine for every other test and not for this one.
	first := h.login(t, f.email)
	second := h.login(t, f.email)

	list := h.do(t, http.MethodGet, "/api/v1/auth/sessions", first, nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("listing sessions: %s", readBody(t, list))
	}
	rows, _ := decodeJSON(t, list)["data"].([]any)
	if len(rows) < 2 {
		t.Fatalf("expected at least two sessions, got %d", len(rows))
	}

	// The one that is not the caller's own.
	var target string
	for _, row := range rows {
		r, _ := row.(map[string]any)
		if r["current"] != true {
			target, _ = r["id"].(string)
			break
		}
	}
	if target == "" {
		t.Fatal("every session claimed to be the current one")
	}

	gone := h.do(t, http.MethodDelete, "/api/v1/auth/sessions/"+target,
		first, nil)
	if gone.StatusCode != http.StatusNoContent {
		t.Fatalf("revoking a session: %s", readBody(t, gone))
	}

	// The caller's own session still works. Ending one must not end them all.
	still := h.do(t, http.MethodGet, "/api/v1/auth/sessions", first, nil)
	if still.StatusCode != http.StatusOK {
		t.Fatalf("the caller's own session was ended too: %s",
			readBody(t, still))
	}
	_ = second
}
