//go:build integration

// H5's subscription gate, enforced.
//
// `billing.Allows` resolved a tenant's entitlement correctly and nothing asked
// it, so plan tiers gated nothing: a starter tenant, whose plan excludes
// payroll, loyalty, analytics, approvals, assets, warranty, instalments, API
// keys and webhooks, reached every one of them.
//
// These tests provision the tier they mean to exercise, rather than relying on
// whatever the shared fixture happens to use.
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// tenantOnTier moves a fixture's tenant to a plan and returns its owner token.
func tenantOnTier(t *testing.T, h *harness, f *shopFixture, tier string) string {
	t.Helper()
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE tenant SET plan_tier = $2::plan_tier WHERE id = $1`,
			f.tenantID, tier)
		return e
	}); err != nil {
		t.Fatalf("move to %s: %v", tier, err)
	}
	return h.login(t, f.email)
}

// payrollPath is a route the starter plan excludes and business includes.
func payrollPath(f *shopFixture) string {
	return "/api/v1/payroll?company_id=" + f.companyID.String()
}

// A starter plan is refused a module it does not include, with 402.
//
// Not 403: the caller is signed in and holds `payroll.view`. What is missing is
// commercial, and the remedy is a subscription rather than a permission.
func TestAStarterPlanIsRefusedPayrollWithPaymentRequired(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	token := tenantOnTier(t, h, f, "starter")

	resp := h.do(t, http.MethodGet, payrollPath(f), token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 Payment Required: %s",
			resp.StatusCode, readBody(t, resp))
	}
	// The refusal has to name the module, or an owner cannot tell which part of
	// their subscription to change.
	if body := readBody(t, resp); !strings.Contains(body, "payroll") {
		t.Errorf("the refusal does not name the module: %s", body)
	}
}

// A plan that includes the module lets it through.
//
// The half that keeps the gate meaningful: one that refused everything would be
// indistinguishable from a broken route table.
func TestABusinessPlanReachesPayroll(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	token := tenantOnTier(t, h, f, "business")

	resp := h.do(t, http.MethodGet, payrollPath(f), token, nil)
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPaymentRequired {
		t.Errorf("a business plan was refused payroll, which its tier includes")
	}
}

// The core of the product is in every tier.
//
// Selling, stock, customers and accounting are not modules a plan withholds. A
// starter shop that could not ring up a sale would not be a shop.
func TestTheCoreProductIsNotGatedOnAnyPlan(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	token := tenantOnTier(t, h, f, "starter")

	for _, path := range []string{
		"/api/v1/catalog/products?company_id=",
		"/api/v1/stock/on-hand?company_id=",
		"/api/v1/customers?company_id=",
		"/api/v1/expenses?company_id=",
	} {
		resp := h.do(t, http.MethodGet, path+f.companyID.String(), token, nil)
		if resp.StatusCode == http.StatusPaymentRequired {
			t.Errorf("%s is gated behind a plan; the core product is not a module",
				path)
		}
		resp.Body.Close()
	}
}

// A per-tenant grant opens a module the plan excludes.
//
// H5's override: a shop negotiates one module without being moved to a tier it
// does not otherwise need. The gate has to honour it, or the override is a row
// nobody reads.
func TestATenantGrantOpensAModuleThePlanExcludes(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	token := tenantOnTier(t, h, f, "starter")

	before := h.do(t, http.MethodGet, payrollPath(f), token, nil)
	before.Body.Close()
	if before.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("starter reached payroll before the grant: %d", before.StatusCode)
	}

	setFeature(t, h, f.tenantID, "payroll", true, "")

	after := h.do(t, http.MethodGet, payrollPath(f), token, nil)
	defer after.Body.Close()
	if after.StatusCode == http.StatusPaymentRequired {
		t.Error("a granted module is still refused")
	}
}

// An expired grant closes again.
//
// Without the expiry check a trial or a goodwill month would be permanent, and
// it would fail open — nobody would notice.
func TestAnExpiredGrantClosesTheModuleAgain(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	token := tenantOnTier(t, h, f, "starter")

	setFeature(t, h, f.tenantID, "payroll", true, "2020-01-01")

	resp := h.do(t, http.MethodGet, payrollPath(f), token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402 — a grant that expired in 2020 is "+
			"still opening the module", resp.StatusCode)
	}
}

// A withdrawn module closes even on a tier that includes it.
//
// The direction that matters for suspension: a tenant in arrears keeps its tier
// and loses a module.
func TestAWithdrawnModuleClosesOnAPlanThatIncludesIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	token := tenantOnTier(t, h, f, "business")

	setFeature(t, h, f.tenantID, "payroll", false, "")

	resp := h.do(t, http.MethodGet, payrollPath(f), token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402 — a withdrawn module is still open",
			resp.StatusCode)
	}
}

// One tenant's plan does not decide another's access.
func TestOneTenantsPlanDoesNotOpenAnothersModules(t *testing.T) {
	h := newHarness(t)
	rich := h.seedShop(t, "owner")
	poor := h.seedShop(t, "owner")

	richToken := tenantOnTier(t, h, rich, "business")
	poorToken := tenantOnTier(t, h, poor, "starter")

	open := h.do(t, http.MethodGet, payrollPath(rich), richToken, nil)
	open.Body.Close()
	if open.StatusCode == http.StatusPaymentRequired {
		t.Fatal("the business tenant was refused its own module")
	}

	shut := h.do(t, http.MethodGet, payrollPath(poor), poorToken, nil)
	defer shut.Body.Close()
	if shut.StatusCode != http.StatusPaymentRequired {
		t.Errorf("the starter tenant reached payroll (status %d) — one "+
			"tenant's plan opened another's module", shut.StatusCode)
	}
}

// Every gated feature is one the plans actually sell.
//
// The route map and `plan_feature` are two statements of the same commercial
// model. If they drift, a route is gated on a feature no plan grants — which
// would refuse it on every tier, for ever, with a message telling the owner to
// upgrade to something that does not exist.
func TestEveryGatedFeatureIsOneThePlansSell(t *testing.T) {
	h := newHarness(t)

	sold := map[string]bool{}
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		rows, e := tx.Query(t.Context(), `SELECT DISTINCT feature FROM plan_feature`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var f string
			if e := rows.Scan(&f); e != nil {
				return e
			}
			sold[f] = true
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read the plans: %v", err)
	}

	for prefix, feature := range featureOfRoute {
		if !sold[feature] {
			t.Errorf("/api/v1/%s is gated on %q, which no plan sells",
				prefix, feature)
		}
	}
}

// Super Admin is not a subscriber.
//
// The platform control plane has no plan of its own, and gating it on a
// tenant's entitlement would be asking the wrong question of the wrong party.
func TestThePlatformControlPlaneIsNotGatedOnAPlan(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	resp := h.do(t, http.MethodGet, "/api/v1/platform/tenants", admin, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPaymentRequired {
		t.Error("the platform control plane was refused on a subscription")
	}
}
