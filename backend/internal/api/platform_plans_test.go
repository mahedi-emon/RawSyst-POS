//go:build integration

// The two things the control plane could not do.
//
// A software owner's own list of what the Super Admin panel must cover had two
// entries with nothing behind them: seeing who is inside a business, and
// changing what a plan includes. Both existed as counts or as migrations, which
// is not the same as being able to do them.
package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// --- who is inside a business --------------------------------------------

// An operator can see a client's staff, which until now they could only count.
//
// The question this answers in practice is "how many of your five seats are in
// use" and "who am I about to reset a password for", asked without making the
// client read it out.
func TestTheConsoleCanSeeWhoIsInsideABusiness(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	made, resp := h.createBusiness(t, admin, map[string]any{
		"owner_name": "Amina Rahman",
	})
	if made.TenantID == "" {
		t.Fatalf("creating the business: %d", resp.StatusCode)
	}

	list := h.do(t, http.MethodGet,
		"/api/v1/platform/tenants/"+made.TenantID+"/users", admin, nil)
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("listing staff: %d — %s", list.StatusCode, readBody(t, list))
	}

	rows, _ := decodeJSON(t, list)["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("a business created seconds ago has no people in it")
	}

	owner := rows[0].(map[string]any)
	if owner["email"] != made.OwnerEmail {
		t.Errorf("first row is %v, want the owner %q",
			owner["email"], made.OwnerEmail)
	}
	if owner["full_name"] != "Amina Rahman" {
		t.Errorf("full_name = %v", owner["full_name"])
	}
	// `invited` until they use the one-time password, which is exactly the
	// state an operator needs to see when a client says nobody can get in.
	if owner["status"] != "invited" {
		t.Errorf("status = %v, want invited", owner["status"])
	}
	if owner["last_login_at"] != nil {
		t.Errorf("last_login_at = %v for somebody who has never signed in",
			owner["last_login_at"])
	}
}

// The list carries no password hash and no role.
//
// The hash is irreversible, so there is nothing to reveal — but a support
// screen that selected it would put it in a log the first time somebody dumped
// a response. The roles are a harder rule: `role` and `user_role_assignment`
// are deliberately unreadable from the platform plane, and decorating a support
// list is not a good enough reason to widen that.
func TestTheStaffListCarriesNoSecretAndNoRole(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))
	made, _ := h.createBusiness(t, admin, nil)

	list := h.do(t, http.MethodGet,
		"/api/v1/platform/tenants/"+made.TenantID+"/users", admin, nil)
	defer list.Body.Close()
	body := readBody(t, list)

	for _, forbidden := range []string{
		"password", "hash", "role", "permission", "mfa_secret",
	} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("the staff list mentions %q: %s", forbidden, body)
		}
	}
}

// It is one business's staff, not the platform's address book.
func TestTheStaffListIsOneBusinessOnly(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	one, _ := h.createBusiness(t, admin, nil)
	two, _ := h.createBusiness(t, admin, nil)

	list := h.do(t, http.MethodGet,
		"/api/v1/platform/tenants/"+one.TenantID+"/users", admin, nil)
	defer list.Body.Close()
	body := readBody(t, list)

	if !strings.Contains(body, one.OwnerEmail) {
		t.Error("the list does not contain the business's own owner")
	}
	if strings.Contains(body, two.OwnerEmail) {
		t.Error("the list contains another business's owner")
	}
}

// And a business user cannot read it at all.
func TestOnlyThePlatformCanSeeABusinessesStaffList(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/platform/tenants/"+shop.tenantID.String()+"/users",
		shop.token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("an owner read the platform staff list: %d, want 404",
			resp.StatusCode)
	}
}

// --- what a plan includes -------------------------------------------------

// The price list is editable, and editing it changes what clients may reach.
//
// Until now `plan_feature` was written by a migration and by nothing else, so
// moving a module between tiers was a code change, a review, a build and a
// deploy — for a decision that is a sentence.
func TestTheSoftwareOwnerCanChangeWhatAPlanIncludes(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	before := h.do(t, http.MethodGet, "/api/v1/platform/plans", admin, nil)
	defer before.Body.Close()
	if before.StatusCode != http.StatusOK {
		t.Fatalf("reading the price list: %d — %s",
			before.StatusCode, readBody(t, before))
	}
	plans, _ := decodeJSON(t, before)["data"].([]any)
	if len(plans) != 4 {
		t.Fatalf("%d tiers, want the four the enum allows", len(plans))
	}

	// A module the top tier has and the bottom one does not: the shape every
	// tiered product has, and the thing being moved.
	starter := plans[0].(map[string]any)
	if starter["tier"] != "starter" {
		t.Fatalf("first tier is %v, want starter", starter["tier"])
	}
	enterprise := plans[3].(map[string]any)
	feature := firstMissing(
		toStrings(enterprise["features"]), toStrings(starter["features"]))
	if feature == "" {
		t.Skip("starter already includes everything enterprise does")
	}

	// Grant it to starter.
	grant := h.do(t, http.MethodPut, "/api/v1/platform/plans/starter/features",
		admin, map[string]any{"feature": feature, "included": true})
	defer grant.Body.Close()
	if grant.StatusCode != http.StatusOK {
		t.Fatalf("granting %s to starter: %d — %s",
			feature, grant.StatusCode, readBody(t, grant))
	}
	t.Cleanup(func() {
		r := h.do(t, http.MethodPut, "/api/v1/platform/plans/starter/features",
			admin, map[string]any{"feature": feature, "included": false})
		r.Body.Close()
	})

	after, _ := decodeJSON(t, grant)["data"].([]any)
	if !hasString(toStrings(after[0].(map[string]any)["features"]), feature) {
		t.Errorf("starter still does not include %s after granting it", feature)
	}
}

// A module nothing sells is a typo, and a typo that silently grants nothing is
// worse than a refusal.
func TestAModuleThatDoesNotExistIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	resp := h.do(t, http.MethodPut, "/api/v1/platform/plans/starter/features",
		admin, map[string]any{"feature": "teleportation", "included": true})
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("a module nobody sells was added to a plan: %d", resp.StatusCode)
	}
}

func TestATierThatDoesNotExistIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	for _, tier := range []string{"platinum", "", "STARTER"} {
		resp := h.do(t, http.MethodPut,
			"/api/v1/platform/plans/"+tier+"/limits", admin,
			map[string]any{"max_users": 10})
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("tier %q was accepted: %d", tier, resp.StatusCode)
		}
	}
}

// Raising a tier's ceilings changes what NEW clients get and leaves existing
// ones alone.
//
// The safe direction, and the one that needs saying: the commonest reason a
// tenant's ceiling differs from its tier is that an operator raised it for that
// client on purpose, and rewriting every tenant from a plan edit would silently
// undo exactly those exceptions.
func TestRaisingATierDoesNotRewriteExistingClients(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	made, _ := h.createBusiness(t, admin, map[string]any{"plan_tier": "starter"})

	ctx := context.Background()
	var before int
	_ = h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT max_users FROM tenant_limit WHERE tenant_id = $1`,
			made.TenantID).Scan(&before)
	})
	if before == 0 {
		t.Fatal("the business has no ceilings on record")
	}

	// Read the tier's current defaults so they can be restored.
	plans := h.do(t, http.MethodGet, "/api/v1/platform/plans", admin, nil)
	original := map[string]any{}
	for _, p := range decodeJSON(t, plans)["data"].([]any) {
		m := p.(map[string]any)
		if m["tier"] == "starter" {
			original, _ = m["limits"].(map[string]any)
		}
	}
	plans.Body.Close()
	if len(original) == 0 {
		t.Fatal("starter has no ceilings on record")
	}
	t.Cleanup(func() {
		r := h.do(t, http.MethodPut, "/api/v1/platform/plans/starter/limits",
			admin, original)
		r.Body.Close()
	})

	raised := map[string]any{}
	for k, v := range original {
		raised[k] = v
	}
	raised["max_users"] = before + 500

	set := h.do(t, http.MethodPut, "/api/v1/platform/plans/starter/limits",
		admin, raised)
	defer set.Body.Close()
	if set.StatusCode != http.StatusOK {
		t.Fatalf("raising the tier: %d — %s", set.StatusCode, readBody(t, set))
	}

	var after int
	_ = h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT max_users FROM tenant_limit WHERE tenant_id = $1`,
			made.TenantID).Scan(&after)
	})
	if after != before {
		t.Errorf("an existing client's ceiling moved from %d to %d when the "+
			"tier was raised. The commonest reason a tenant's ceiling differs "+
			"from its tier is a deliberate exception, and this would undo them.",
			before, after)
	}
}

// A business user cannot edit the price list.
func TestOnlyThePlatformCanChangeThePriceList(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/platform/plans"},
		{http.MethodPut, "/api/v1/platform/plans/starter/features"},
		{http.MethodPut, "/api/v1/platform/plans/starter/limits"},
	} {
		resp := h.do(t, c.method, c.path, shop.token,
			map[string]any{"feature": "payroll", "included": true})
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("an owner reached %s %s: %d, want 404",
				c.method, c.path, resp.StatusCode)
		}
	}
}

// Changing the product is in the trail, with no tenant, because it belongs to
// none and changes what every client on that tier may reach.
func TestChangingAPlanIsAudited(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	set := h.do(t, http.MethodPut, "/api/v1/platform/plans/starter/features",
		admin, map[string]any{"feature": "payroll", "included": false})
	set.Body.Close()
	if set.StatusCode != http.StatusOK {
		t.Fatalf("changing a plan: %d", set.StatusCode)
	}
	t.Cleanup(func() {
		r := h.do(t, http.MethodPut, "/api/v1/platform/plans/starter/features",
			admin, map[string]any{"feature": "payroll", "included": true})
		r.Body.Close()
	})

	trail := h.do(t, http.MethodGet, "/api/v1/platform/audit?limit=50", admin, nil)
	defer trail.Body.Close()
	found := false
	for _, row := range decodeJSON(t, trail)["data"].([]any) {
		if row.(map[string]any)["action"] == "plan_feature_set" {
			found = true
		}
	}
	if !found {
		t.Error("changing what a plan includes left no entry in the trail")
	}
}

// --- small helpers --------------------------------------------------------

func toStrings(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// firstMissing is the first entry of `all` that `some` does not have.
func firstMissing(all, some []string) string {
	for _, a := range all {
		if !hasString(some, a) {
			return a
		}
	}
	return ""
}
