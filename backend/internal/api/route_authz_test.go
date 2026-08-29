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

// A permission a route requires must be one some role actually holds.
//
// `TestEveryRouteDeclaresItsAccess` proves each route NAMES a permission. It
// cannot prove the permission EXISTS, because there is no catalogue table to
// check against: `role_permission` is free text by design — blueprint A6.2
// lists fourteen verbs and then uses three that are not in the list, so the
// verb set has to be extensible per module.
//
// The cost of that freedom is that `purchasing.viewe` compiles, registers,
// passes every existing guard, and returns 403 to every user of the product
// forever. Nobody finds it until a customer reports a screen that will not
// open — and the report says "permission denied", which reads as a
// configuration problem at their end rather than a typo at ours.
//
// What makes it checkable is the seeded system roles. A permission granted to
// no role is a permission nobody can hold.
func TestEveryRoutePermissionIsOneSomeRoleHolds(t *testing.T) {
	h := newHarness(t)

	held := map[string]bool{}
	err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		rows, e := tx.Query(context.Background(),
			`SELECT DISTINCT permission FROM role_permission`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if e := rows.Scan(&p); e != nil {
				return e
			}
			held[p] = true
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("reading the seeded permissions: %v", err)
	}
	if len(held) == 0 {
		t.Fatal("no permissions are seeded at all, so this proves nothing")
	}

	unreachable := map[string][]string{}
	for _, rt := range (&Server{}).Routes() {
		if rt.Access != AccessPermission {
			continue
		}
		p := strings.TrimSpace(rt.Permission)
		if p == "" || held[p] {
			continue
		}
		unreachable[p] = append(unreachable[p], rt.Method+" "+rt.Pattern)
	}

	for permission, routes := range unreachable {
		t.Errorf("no role holds %q, so these routes refuse everybody:\n    %s",
			permission, strings.Join(routes, "\n    "))
	}
}

// And the other direction: a seeded permission with no route behind it.
//
// Reported rather than failed. Seeding a module's verbs before its routes is
// deliberate here — the chart of accounts carries accounts before anything
// posts to them, and `inventory.adjust_stock` and `inventory.transfer_stock`
// are seeded for the Phase 2 stock-adjustment module in the same spirit. The
// list exists so the next reader can tell that kind apart from a verb that was
// renamed and left a widow behind.
func TestSeededPermissionsWithNoRoute(t *testing.T) {
	h := newHarness(t)

	// Every seeded verb that guards no route, and the reason it does not.
	//
	// A ledger rather than a pass/fail, because "no route requires this" means
	// four different things and only one of them is a mistake. What the test
	// enforces is that somebody DECIDED: a verb reaching a role without an
	// entry here fails, and the entry has to say which kind it is.
	//
	// The kinds, and why each is legitimate:
	//
	//   structural — the permission names a decision enforced by the shape of
	//     the data rather than by a check. `catalog.view_cost_price` is the
	//     clearest: the till's `SellableVariant` has no cost field at all, so
	//     cost cannot reach a terminal whatever anybody's role says. A check
	//     would be weaker than the type, not stronger.
	//
	//   local — the action never reaches the server. Holding and resuming a
	//     cart happens in the till's own SQLite; a draft that is voided was
	//     never sent. There is no route to guard because there is no request.
	//
	//   awaited — seeded ahead of the module that will use it, deliberately,
	//     the way the chart of accounts carries accounts before anything posts
	//     to them. Must name the phase.
	//
	//   widow — a verb that was renamed, whose old grant was left behind. This
	//     is the kind that is a mistake, and there should be none: 0071 removed
	//     the two that existed. An entry appearing here again means a rename
	//     dropped its cleanup.
	explained := map[string]string{
		// structural
		"catalog.view_cost_price": "structural — SellableVariant carries no cost field, so no till can receive one",
		"catalog.view_profit_margin": "structural — margin is derived from cost and is absent for the same reason",

		// local to the terminal
		"sales.hold":       "local — a held cart lives in the till's own SQLite and is never sent",
		"sales.void_draft": "local — a draft that is voided was never sent to the server",
		"sales.discount":   "local — a discount is a field on the sale the till submits, priced and floor-checked server-side by sales.create",

		// awaited
		"inventory.adjust_stock":      "awaited — Phase 2, stock adjustments and counts",
		"inventory.transfer_stock":    "awaited — Phase 2, inter-warehouse transfers",
		"identity.create":             "awaited — Phase 2, user management; the wizard's People step is optional and says so",
		"identity.manage_roles":       "awaited — Phase 2, the role editor",
		"catalog.edit":                "awaited — Phase 2, product editing; Phase 1 creates and retires",
		"accounting.approve":          "awaited — Phase 2, journal approval workflow",
		"accounting.close_period":     "awaited — Phase 2; the period lock itself is enforced in the database from 0015",
		"accounting.reopen_period":    "awaited — Phase 2, as above",
		"report.export":               "awaited — Phase 3, report generation and delivery (design 08 job kinds)",
		"compliance.retry_submission": "awaited — deliberately not offered: submission is automatic and ordered, and the screen says so",
	}

	required := map[string]bool{}
	for _, rt := range (&Server{}).Routes() {
		if rt.Access == AccessPermission {
			required[strings.TrimSpace(rt.Permission)] = true
		}
	}

	var seeded []string
	err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		rows, e := tx.Query(context.Background(),
			`SELECT DISTINCT permission FROM role_permission ORDER BY permission`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if e := rows.Scan(&p); e != nil {
				return e
			}
			seeded = append(seeded, p)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("reading the seeded permissions: %v", err)
	}
	if len(seeded) == 0 {
		t.Fatal("no permissions are seeded at all, so this proves nothing")
	}

	for _, p := range seeded {
		if required[p] {
			continue
		}
		if why, decided := explained[p]; decided {
			t.Logf("%-28s %s", p, why)
			continue
		}
		t.Errorf("%q is granted to a role and guards no route, and no reason is "+
			"recorded. Either a route is missing, or the verb was renamed and "+
			"this grant is a widow. Add it to `explained` with its kind.", p)
	}

	// The other direction: an entry here for a verb that DOES guard a route is
	// a stale note, and a stale note is how a ledger stops being read.
	for p := range explained {
		if required[p] {
			t.Errorf("%q is listed as having no route, but a route requires it. "+
				"Remove the entry.", p)
		}
	}
}
