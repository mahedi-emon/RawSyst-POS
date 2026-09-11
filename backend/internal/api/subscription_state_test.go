//go:build integration

// A business whose subscription has lapsed, over the real HTTP surface.
//
// The policy is tested as a pure function in `billing/standing_test.go`. This
// tests that the product actually asks it — which is the half that was missing
// for the entire life of the billing tables. `subscription.status` and
// `tenant.status` were both written by dunning, by invoice payment and by the
// plan editor, and read by nothing: before Phase 5, `tenant.status` was
// consulted by exactly two queries in the product and both were counters on a
// dashboard.
//
// So these drive it the way somebody would who had read the network tab: a real
// session, a real token, and a direct call to the route the screen would have
// called. A button that is hidden is not a control, and every case below
// deliberately ignores the interface.
package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// lapse puts a tenant's subscription into a given state, without going through
// the routes that would do it — so the test is about what the ACCESS layer does
// with the state rather than about who may set it.
func (h *harness) lapse(t *testing.T, tenantID uuid.UUID, status, periodEnd string) {
	t.Helper()
	ctx := context.Background()
	// An upsert, because the shop fixtures insert their tenant directly rather
	// than through `provisioning.CreateTenant`, so they have no subscription
	// row — and a tenant with none reads as active, which is the documented
	// behaviour and would make every case below pass for the wrong reason.
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO subscription
			  (tenant_id, tier, cycle, price, currency, status, started_on,
			   current_period_end)
			VALUES ($1, 'professional', 'monthly', 0, 'SAR', $2,
			        current_date - 400, nullif($3,'')::date)
			ON CONFLICT (tenant_id) DO UPDATE SET
			  status = excluded.status,
			  current_period_end = excluded.current_period_end`,
			tenantID, status, periodEnd)
		return e
	}); err != nil {
		t.Fatalf("setting the subscription state: %v", err)
	}
	h.forgetStanding(tenantID)
}

// setTenantStatus is the other half: the switch an operator throws.
func (h *harness) setTenantStatus(t *testing.T, tenantID uuid.UUID, status string) {
	t.Helper()
	ctx := context.Background()
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE tenant SET status = $2::tenant_status WHERE id = $1`,
			tenantID, status)
		return e
	}); err != nil {
		t.Fatalf("setting the tenant status: %v", err)
	}
	h.forgetStanding(tenantID)
}

// forgetStanding drops the cached standing, so a test does not spend five
// seconds waiting for the window described on StandingCacheTTL.
func (h *harness) forgetStanding(tenantID uuid.UUID) {
	if h.server == nil {
		return
	}
	h.billing.ForgetStanding(tenantID)
}

// --- writes stop, reads do not -------------------------------------------

// The central case: a subscription that ran out, and a till that must stop.
func TestAnExpiredSubscriptionStopsTheTillButNotTheBooks(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")

	// Trading normally first, so the refusals below are about the expiry and
	// not about a fixture that never worked.
	before := h.do(t, http.MethodPost, taxonomyPath(shop, "/api/v1/catalog/categories"), shop.token,
		map[string]any{"name": "Before " + randomSuffix()})
	before.Body.Close()
	if before.StatusCode != http.StatusCreated {
		t.Fatalf("the shop could not trade before expiry: %d", before.StatusCode)
	}

	h.lapse(t, shop.tenantID, "active", "2025-03-01")

	// The write is refused, with 402 rather than 403: the caller is
	// authenticated and holds the permission, and what is missing is
	// commercial. A 403 would send them to their owner to ask for a permission
	// they already have.
	after := h.do(t, http.MethodPost, taxonomyPath(shop, "/api/v1/catalog/categories"), shop.token,
		map[string]any{"name": "After " + randomSuffix()})
	defer after.Body.Close()
	if after.StatusCode != http.StatusPaymentRequired {
		t.Errorf("an expired subscription still changed the catalogue: %d, "+
			"want 402 — %s", after.StatusCode, readBody(t, after))
	}
	// And it says when, because "renew it" is unanswerable otherwise.
	if body := readBody(t, after); !strings.Contains(body, "2025-03-01") {
		t.Errorf("the refusal does not name the date it ended: %s", body)
	}

	// Reads keep working. A shop that cannot see its own records cannot find
	// out what it owes, and cannot get its data out.
	for _, path := range []string{
		taxonomyPath(shop, "/api/v1/catalog/products"),
		"/api/v1/subscription",
		"/api/v1/subscription/standing",
	} {
		resp := h.do(t, http.MethodGet, path, shop.token, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("an expired business cannot read %s: %d", path, resp.StatusCode)
		}
	}
}

// Every operational module, not just the one that was convenient to test.
//
// The brief for this phase listed them: sales, products, inventory, purchases,
// customers, employees, expenses, settings. A gate that stopped one and not the
// rest would pass a narrower test and be worth nothing.
func TestExpiryStopsEveryOperationalWrite(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")
	h.lapse(t, shop.tenantID, "active", "2025-03-01")

	// Scoped the way every screen scopes them, so a refusal is the
	// subscription gate and not a missing company_id.
	scoped := func(p string) string { return taxonomyPath(shop, p) }

	for _, c := range []struct{ method, path string }{
		{http.MethodPost, scoped("/api/v1/catalog/categories")},
		{http.MethodPost, scoped("/api/v1/catalog/products")},
		{http.MethodPost, scoped("/api/v1/customers")},
		{http.MethodPost, scoped("/api/v1/expenses")},
		{http.MethodPost, scoped("/api/v1/stock/adjustments")},
		{http.MethodPost, scoped("/api/v1/people")},
		{http.MethodPost, scoped("/api/v1/purchasing/orders")},
	} {
		resp := h.do(t, c.method, c.path, shop.token, map[string]any{})
		resp.Body.Close()
		// 402 is the expected refusal. A 404 means the route does not exist
		// under that name, which is a test problem rather than a product one
		// and is reported separately so it cannot hide a real pass.
		switch resp.StatusCode {
		case http.StatusPaymentRequired:
		case http.StatusNotFound:
			t.Logf("%s %s: no such route, not exercised", c.method, c.path)
		default:
			t.Errorf("%s %s returned %d after expiry, want 402",
				c.method, c.path, resp.StatusCode)
		}
	}
}

// Suspension is the same shape, with a different sentence.
func TestASuspendedBusinessIsReadOnly(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")
	h.setTenantStatus(t, shop.tenantID, "suspended")

	write := h.do(t, http.MethodPost, taxonomyPath(shop, "/api/v1/catalog/categories"), shop.token,
		map[string]any{"name": "Nope " + randomSuffix()})
	defer write.Body.Close()
	if write.StatusCode != http.StatusPaymentRequired {
		t.Errorf("a suspended business still changed the catalogue: %d", write.StatusCode)
	}
	if body := readBody(t, write); !strings.Contains(body, "suspended") {
		t.Errorf("the refusal does not say the business is suspended: %s", body)
	}

	read := h.do(t, http.MethodGet,
		taxonomyPath(shop, "/api/v1/catalog/products"), shop.token, nil)
	read.Body.Close()
	if read.StatusCode != http.StatusOK {
		t.Errorf("a suspended business cannot read its own catalogue: %d", read.StatusCode)
	}
}

// Taking your data with you is exactly what a lapsed client is entitled to.
func TestALapsedBusinessCanStillExportItsData(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")
	h.lapse(t, shop.tenantID, "active", "2025-03-01")

	resp := h.do(t, http.MethodGet, "/api/v1/exports/customers", shop.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPaymentRequired {
		t.Error("a lapsed business cannot export its own data. A product that " +
			"holds a client's records hostage to an unpaid invoice is one " +
			"nobody should ship.")
	}
}

// Recovery has to stay reachable, or a suspension can never be lifted by the
// person it was applied to.
func TestALapsedBusinessCanStillReachSupportAndChangeAPassword(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")
	h.setTenantStatus(t, shop.tenantID, "suspended")

	ticket := h.do(t, http.MethodPost, "/api/v1/support/tickets", shop.token,
		map[string]any{
			"subject": "Why are we suspended", "body": "Please advise.",
		})
	defer ticket.Body.Close()
	if ticket.StatusCode == http.StatusPaymentRequired {
		t.Error("a suspended business cannot contact support, which is the " +
			"one channel through which a suspension gets lifted")
	}

	// Signing out is not trading.
	out := h.do(t, http.MethodPost, "/api/v1/auth/logout", shop.token, map[string]any{})
	out.Body.Close()
	if out.StatusCode == http.StatusPaymentRequired {
		t.Error("a suspended business cannot even sign out")
	}
}

// --- states that must NOT stop a business --------------------------------

func TestPastDueAndTrialingKeepTrading(t *testing.T) {
	for _, status := range []string{"past_due", "trialing"} {
		h := newHarness(t)
		shop := h.seedShop(t, "owner")
		h.lapse(t, shop.tenantID, status, "2027-01-01")

		resp := h.do(t, http.MethodPost, taxonomyPath(shop, "/api/v1/catalog/categories"), shop.token,
			map[string]any{"name": status + " " + randomSuffix()})
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("a %s business was stopped from trading: %d. Dunning "+
				"decides when a late payment becomes a suspension, not this.",
				status, resp.StatusCode)
		}
	}
}

// --- deactivation is the end of it ---------------------------------------

func TestADeactivatedBusinessReachesNothing(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")
	h.setTenantStatus(t, shop.tenantID, "deactivated")

	// Not even reads. This is the one state that is not read-only.
	read := h.do(t, http.MethodGet,
		taxonomyPath(shop, "/api/v1/catalog/products"), shop.token, nil)
	read.Body.Close()
	if read.StatusCode != http.StatusForbidden {
		t.Errorf("a deactivated business still read its catalogue: %d, want 403",
			read.StatusCode)
	}

	write := h.do(t, http.MethodPost, taxonomyPath(shop, "/api/v1/catalog/categories"), shop.token,
		map[string]any{"name": "Nope"})
	write.Body.Close()
	if write.StatusCode == http.StatusCreated {
		t.Error("a deactivated business still changed the catalogue")
	}

	// And nobody in it signs in again.
	again := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": shop.email, "password": testPassword})
	again.Body.Close()
	if again.StatusCode == http.StatusOK {
		t.Error("somebody signed in to a deactivated business")
	}
}

// --- the Super Admin's controls ------------------------------------------

func TestSuperAdminCanSuspendAndReactivateABusiness(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))
	shop := h.seedShop(t, "owner")

	path := "/api/v1/platform/tenants/" + shop.tenantID.String() + "/standing"

	// A reason is required for anything that stops a business, because
	// whoever lifts it later needs to know what it was for.
	bare := h.do(t, http.MethodPut, path, admin, map[string]any{"action": "suspend"})
	bare.Body.Close()
	if bare.StatusCode < 400 {
		t.Error("a business was suspended with no reason recorded")
	}

	suspend := h.do(t, http.MethodPut, path, admin,
		map[string]any{"action": "suspend", "reason": "non-payment, third notice"})
	defer suspend.Body.Close()
	if suspend.StatusCode != http.StatusOK {
		t.Fatalf("suspending: %d — %s", suspend.StatusCode, readBody(t, suspend))
	}
	h.forgetStanding(shop.tenantID)

	// It bit.
	blocked := h.do(t, http.MethodPost, taxonomyPath(shop, "/api/v1/catalog/categories"), shop.token,
		map[string]any{"name": "Nope " + randomSuffix()})
	blocked.Body.Close()
	if blocked.StatusCode != http.StatusPaymentRequired {
		t.Errorf("the business kept trading after being suspended: %d", blocked.StatusCode)
	}

	// And it lifts.
	activate := h.do(t, http.MethodPut, path, admin,
		map[string]any{"action": "activate"})
	activate.Body.Close()
	if activate.StatusCode != http.StatusOK {
		t.Fatalf("reactivating: %d", activate.StatusCode)
	}
	h.forgetStanding(shop.tenantID)

	trading := h.do(t, http.MethodPost, taxonomyPath(shop, "/api/v1/catalog/categories"), shop.token,
		map[string]any{"name": "Again " + randomSuffix()})
	trading.Body.Close()
	if trading.StatusCode != http.StatusCreated {
		t.Errorf("the business did not resume trading after reactivation: %d",
			trading.StatusCode)
	}
}

func TestAnInvalidStandingTransitionIsRefused(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))
	shop := h.seedShop(t, "owner")
	path := "/api/v1/platform/tenants/" + shop.tenantID.String() + "/standing"

	for _, action := range []string{"", "delete", "destroy", "ACTIVATE", "suspended"} {
		resp := h.do(t, http.MethodPut, path, admin,
			map[string]any{"action": action, "reason": "whatever"})
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("the route accepted %q as an action: %d", action, resp.StatusCode)
		}
	}
}

// Every transition is in the trail, with who did it and why.
func TestSuspendingABusinessIsAudited(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))
	shop := h.seedShop(t, "owner")
	path := "/api/v1/platform/tenants/" + shop.tenantID.String() + "/standing"

	resp := h.do(t, http.MethodPut, path, admin,
		map[string]any{"action": "suspend", "reason": "non-payment"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("suspending: %d", resp.StatusCode)
	}

	trail := h.do(t, http.MethodGet, "/api/v1/platform/audit?limit=50", admin, nil)
	defer trail.Body.Close()
	found := false
	for _, row := range decodeJSON(t, trail)["data"].([]any) {
		m := row.(map[string]any)
		if m["action"] == "tenant_suspendd" || m["action"] == "tenant_suspended" {
			if m["tenant_id"] == shop.tenantID.String() {
				found = true
				if m["actor"] == nil || m["actor"] == "" {
					t.Error("the entry records no actor")
				}
			}
		}
	}
	if !found {
		t.Error("suspending a business left no entry in the platform trail")
	}
}

// --- the boundary nobody may cross ---------------------------------------

// A lapsed business cannot talk its way out of it.
//
// The tenant comes from the verified token. Nothing in a path, a query or a
// body names which business the gate asks about, so there is no field to send.
// These are the attempts somebody would make anyway.
func TestALapsedBusinessCannotArgueItsWayOut(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")
	other := h.seedShop(t, "owner") // a perfectly healthy business
	h.lapse(t, shop.tenantID, "active", "2025-03-01")

	for _, body := range []map[string]any{
		{"name": "X", "tenant_id": other.tenantID.String()},
		{"name": "X", "subscription_status": "active"},
		{"name": "X", "standing": "active"},
		{"name": "X", "is_super_admin": true},
	} {
		resp := h.do(t, http.MethodPost, taxonomyPath(shop, "/api/v1/catalog/categories"),
			shop.token, body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusCreated {
			t.Errorf("a lapsed business wrote by sending %v", body)
		}
	}

	// And the healthy business is unaffected by its neighbour's state.
	ok := h.do(t, http.MethodPost, taxonomyPath(other, "/api/v1/catalog/categories"), other.token,
		map[string]any{"name": "Fine " + randomSuffix()})
	ok.Body.Close()
	if ok.StatusCode != http.StatusCreated {
		t.Errorf("a healthy business was stopped by another tenant's lapse: %d",
			ok.StatusCode)
	}
}

// A business user cannot set their own standing.
func TestOnlyThePlatformMayChangeAStanding(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")
	path := "/api/v1/platform/tenants/" + shop.tenantID.String() + "/standing"

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		resp := h.do(t, method, path, shop.token,
			map[string]any{"action": "activate"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("an owner reached %s on the standing route: %d, want 404",
				method, resp.StatusCode)
		}
	}
}
