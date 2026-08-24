//go:build integration

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Every permission-guarded route must refuse a signed-in user who does not
// hold its permission.
//
// TestUnauthenticatedIsRefusedEverywhere already walks every route with no
// token at all. This walks them WITH one, as the far more realistic attacker:
// somebody who has a legitimate account, has seen the API in their browser's
// network tab, and is calling a route their screen never showed them.
//
// Blueprint QA gate M7 puts it plainly -- the call is made directly, with no UI
// in the way. A route whose only protection is that no button points at it is
// not protected. This is the test that says so for all of them at once, so a
// route added next year without a permission is caught by a test nobody has to
// remember to update.

// permissionsOf reads what a signed-in user actually holds.
func permissionsOf(t *testing.T, h *harness, token string) map[string]bool {
	t.Helper()
	resp := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading identity: status %d", resp.StatusCode)
	}
	var me struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode identity: %v", err)
	}
	held := make(map[string]bool, len(me.Permissions))
	for _, p := range me.Permissions {
		held[p] = true
	}
	return held
}

// fillPathParams puts a syntactically valid id into every {placeholder}, so a
// route is refused for want of PERMISSION rather than for a malformed path.
func fillPathParams(pattern string) string {
	out := pattern
	for {
		open := strings.Index(out, "{")
		if open < 0 {
			break
		}
		close := strings.Index(out[open:], "}")
		if close < 0 {
			break
		}
		out = out[:open] + uuid.NewString() + out[open+close+1:]
	}
	return out
}

func TestEveryGuardedRouteRefusesAUserWithoutThePermission(t *testing.T) {
	h := newHarness(t)

	// A cashier: a real, active, legitimately signed-in account that holds the
	// narrowest set of permissions the product seeds.
	email := h.seedUserWithRole(t, "cashier")
	token := h.login(t, email)
	held := permissionsOf(t, h, token)

	if len(held) == 0 {
		t.Fatal("the cashier resolved to no permissions at all; the role clone " +
			"failed and this test would pass vacuously")
	}

	s := &Server{}
	checked := 0

	for _, rt := range s.Routes() {
		if rt.Access != AccessPermission {
			continue
		}
		if held[rt.Permission] {
			continue // legitimately allowed; the allowed side is tested elsewhere
		}

		checked++
		t.Run(rt.Method+" "+rt.Pattern, func(t *testing.T) {
			// A body for the verbs that take one. Its contents do not matter:
			// authorization runs before the handler reads it, so a route that
			// answers 400 here is one that decoded a body it should never have
			// been given the chance to see.
			var body any
			if rt.Method != http.MethodGet && rt.Method != http.MethodDelete {
				body = map[string]string{}
			}

			resp := h.do(t, rt.Method, fillPathParams(rt.Pattern), token, body)
			defer resp.Body.Close()

			switch resp.StatusCode {
			case http.StatusForbidden:
				// The expected answer for a tenant route.
			case http.StatusNotFound:
				// The platform plane answers 404 so a tenant user does not learn
				// that it exists. Legitimate for those routes only.
				if !strings.HasPrefix(rt.Pattern, "/api/v1/platform/") {
					t.Errorf("a tenant route answered 404 rather than 403; if it "+
						"is deliberately hidden say so, otherwise it may be "+
						"missing its guard (permission %q)", rt.Permission)
				}
			default:
				t.Errorf("a cashier reached %s %s and got %d. It requires %q, "+
					"which they do not hold. A route whose only protection is "+
					"that no button points at it is not protected.",
					rt.Method, rt.Pattern, resp.StatusCode, rt.Permission)
			}
		})
	}

	if checked == 0 {
		t.Fatal("no route was actually exercised; the cashier appears to hold " +
			"every permission, which would make this test meaningless")
	}
	t.Logf("%d guarded routes refused a cashier who lacked their permission", checked)
}

// Every route that requires a permission must name one, and every route that
// does NOT require one must say why.
//
// The Why field exists so widening access is a deliberate, reviewed act rather
// than a default somebody drifted into. A blank one on a public route is how a
// route becomes unguarded without anybody deciding it should be.
func TestEveryRouteDeclaresItsAccessHonestly(t *testing.T) {
	s := &Server{}
	seen := map[string]bool{}

	for _, rt := range s.Routes() {
		key := rt.Method + " " + rt.Pattern
		if seen[key] {
			t.Errorf("%s is registered twice; the second registration silently "+
				"shadows the first", key)
		}
		seen[key] = true

		switch rt.Access {
		case AccessPermission:
			if strings.TrimSpace(rt.Permission) == "" {
				t.Errorf("%s is guarded by a permission but names none, so the "+
					"guard checks an empty string", key)
			}
		case AccessPublic, AccessAuthenticated:
			if strings.TrimSpace(rt.Why) == "" {
				t.Errorf("%s is reachable without a permission and does not say "+
					"why. Widening access has to be a decision somebody wrote "+
					"down, not a default", key)
			}
			if strings.TrimSpace(rt.Permission) != "" {
				t.Errorf("%s names the permission %q but does not require it, "+
					"which reads as guarded and is not", key, rt.Permission)
			}
		}
	}

	if len(seen) == 0 {
		t.Fatal("the server registered no routes at all")
	}
	t.Logf("%d routes declared", len(seen))
}
