//go:build integration

// Feature entitlement: what a tenant's plan says it may use.
//
// H5 gates modules by subscription tier, with a per-tenant override on top —
// so a starter shop does not get payroll, and a shop that negotiated one
// feature gets that one without being moved to a higher tier.
//
// The resolution is written and correct. **It is not enforced anywhere**, and
// the last test in this file says so in executable form rather than in a
// comment somebody can miss. See TestEntitlementIsResolvedButNotYetEnforced.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/billing"
)

// allows asks the entitlement resolver directly.
//
// Through the service rather than a route, because there is no route that
// enforces it — Entitlements only REPORTS. The resolver takes the caller's
// transaction by design: it is meant to be asked in front of a handler, and
// resolving it on a second connection while the handler holds the first is the
// pool deadlock this codebase has already met once.
func allows(t *testing.T, h *harness, tenantID uuid.UUID, feature string) bool {
	t.Helper()
	svc := billing.NewService(h.pool)

	var out bool
	err := h.pool.TxAsTenant(context.Background(), tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = svc.Allows(context.Background(), tx, tenantID, feature)
		return e
	})
	if err != nil {
		t.Fatalf("resolve %q: %v", feature, err)
	}
	return out
}

// setFeature grants or withdraws one feature for a tenant, as Super Admin does.
func setFeature(t *testing.T, h *harness, tenantID uuid.UUID, feature string, on bool, expires string) {
	t.Helper()
	admin := h.login(t, h.seedSuperAdmin(t))

	body := map[string]any{"feature": feature, "enabled": on, "reason": "test"}
	if expires != "" {
		body["expires_on"] = expires
	}
	resp := h.do(t, http.MethodPut,
		"/api/v1/platform/tenants/"+tenantID.String()+"/features", admin, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set feature %q: %d %s", feature, resp.StatusCode, readBody(t, resp))
	}
}

// The plan decides, when the tenant has no override.
//
// A starter tenant gets promotions and does not get loyalty — straight from
// plan_feature, seeded in 0097.
func TestAPlanTierDecidesWhichFeaturesATenantGets(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	_ = tenantOnTier(t, h, f, "starter")

	if !allows(t, h, f.tenantID, "promotions") {
		t.Error("promotions is included in every tier and was refused")
	}
	if allows(t, h, f.tenantID, "payroll") {
		t.Error("payroll is not in the starter tier and was allowed")
	}
}

// A per-tenant grant beats the plan default.
//
// The point of the override: a shop negotiates one module without being moved
// to a tier it does not otherwise need.
func TestATenantOverrideBeatsThePlanDefault(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	_ = tenantOnTier(t, h, f, "starter")

	if allows(t, h, f.tenantID, "payroll") {
		t.Fatal("payroll was already allowed; the fixture is not starter-tier")
	}
	setFeature(t, h, f.tenantID, "payroll", true, "")

	if !allows(t, h, f.tenantID, "payroll") {
		t.Error("a granted feature is still refused")
	}
}

// An override can also take a feature AWAY that the plan includes.
//
// The direction that matters for suspension: a tenant in arrears keeps its tier
// and loses a module, without rewriting its subscription.
func TestAnOverrideCanWithdrawAFeatureThePlanIncludes(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	if !allows(t, h, f.tenantID, "promotions") {
		t.Fatal("promotions should be included before it is withdrawn")
	}
	setFeature(t, h, f.tenantID, "promotions", false, "")

	if allows(t, h, f.tenantID, "promotions") {
		t.Error("a withdrawn feature is still allowed")
	}
}

// An expired override stops applying and the plan answers again.
//
// Without the expiry check a temporary grant — a trial, a goodwill month —
// would be permanent, and nobody would notice because it fails open.
func TestAnExpiredOverrideFallsBackToThePlan(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	_ = tenantOnTier(t, h, f, "starter")

	setFeature(t, h, f.tenantID, "payroll", true, "2020-01-01")

	if allows(t, h, f.tenantID, "payroll") {
		t.Error("an override that expired in 2020 is still granting access")
	}
}

// A feature nobody has heard of is refused.
//
// Fails closed. A typo in a gate name must not open a module.
func TestAnUnknownFeatureIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	if allows(t, h, f.tenantID, "teleportation") {
		t.Error("an unknown feature was allowed")
	}
}

// One tenant's grant does not reach another.
func TestAFeatureGrantDoesNotLeakToAnotherTenant(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")
	_ = tenantOnTier(t, h, mine, "starter")
	_ = tenantOnTier(t, h, theirs, "starter")

	setFeature(t, h, mine.tenantID, "payroll", true, "")

	if !allows(t, h, mine.tenantID, "payroll") {
		t.Fatal("the grant did not take effect on its own tenant")
	}
	if allows(t, h, theirs.tenantID, "payroll") {
		t.Error("one tenant's feature grant reached another tenant")
	}
}

// Only the platform operator may change a plan or a feature.
//
// These are commercial controls. A business owner who could grant themselves a
// module would be setting their own bill.
func TestOnlySuperAdminMayChangeAPlanOrFeature(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	owner := h.login(t, f.email)

	for _, c := range []struct {
		what string
		path string
		body map[string]any
	}{
		{"feature", "/features", map[string]any{"feature": "payroll", "enabled": true}},
		{"plan", "/subscription", map[string]any{"tier": "enterprise"}},
	} {
		resp := h.do(t, http.MethodPut,
			"/api/v1/platform/tenants/"+f.tenantID.String()+c.path, owner, c.body)
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			t.Errorf("a business owner changed their own %s", c.what)
		}
		resp.Body.Close()
	}
}

// A tenant reads its own entitlements and nobody else's.
func TestATenantReadsItsOwnEntitlements(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	owner := h.login(t, f.email)

	resp := h.do(t, http.MethodGet, "/api/v1/subscription/entitlements", owner, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read entitlements: %d %s", resp.StatusCode, readBody(t, resp))
	}
	if body := readBody(t, resp); len(body) == 0 {
		t.Error("entitlements came back empty; a tenant cannot see what it may use")
	}
}

// The gap this file used to record is closed.
//
// `TestEntitlementIsResolvedButNotYetEnforced` stood here and passed on purpose,
// asserting that nothing refused a request on the strength of an entitlement.
// Its own comment said to delete it the moment somebody wired the gate. That
// happened -- see api/entitlement.go and the refusals in
// entitlement_gate_test.go, which assert the 402 in the modules H5 gates.
//
// The tests above continue to prove the RESOLUTION is right, and provision the
// tier they mean to exercise rather than relying on the shared fixture, which
// sits on the top tier so that a module test is not really a subscription test.
