//go:build integration

// Taking a client on, over the routes the operator's screens actually call.
//
// `provisioning_test.go` already drives the narrative: the platform creates a
// tenant, the Owner signs in with the temporary password, is forced to change
// it, and completes setup. This file covers what that narrative does not, and
// most of it exists because of one defect.
//
// # The subscription that was not there
//
// A tenant was created with no `subscription` row at all. The read path then
// LEFT JOINed the absence and coalesced it -- `coalesce(s.status, 'active')` --
// so the platform reported a complete, plausible, ACTIVE subscription for a
// client who had none.
//
// The counting was the smaller half of that. The serious half is that
// `current_period_end` was NULL, and a subscription with no end date cannot
// expire. Enforcement built on top of it would have swept the table for
// subscriptions past their period end, found none for any client for ever,
// passed every test written against it, and enforced nothing. So the row's
// EXISTENCE is asserted here, at the moment the business is created, rather
// than left to be discovered later by whatever is built on top of it.
//
// # What is asserted about the trail
//
// That a Super Admin action can be READ back. The trail has been written since
// provisioning existed and until now nothing could read it: the only audit
// route is tenant-scoped behind `accounting.view`, and a Super Admin is refused
// every tenant route by design. An audit trail nobody can read is a compliance
// artefact rather than a control.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// newBusiness is what the create route answers with.
type newBusiness struct {
	TenantID          string `json:"tenant_id"`
	OwnerUserID       string `json:"owner_user_id"`
	OwnerEmail        string `json:"owner_email"`
	TemporaryPassword string `json:"temporary_password"`
	PlanTier          string `json:"plan_tier"`
	Cycle             string `json:"cycle"`
	Price             string `json:"price"`
	Currency          string `json:"currency"`
	StartedOn         string `json:"started_on"`
	ExpiresOn         string `json:"expires_on"`
	LoginURL          string `json:"login_url"`
	MailStatus        string `json:"mail_status"`
}

// createBusiness takes a client on and cleans it up afterwards.
//
// `extra` overrides or adds fields, so a test that cares about one of them says
// so and says nothing about the rest.
func (h *harness) createBusiness(
	t *testing.T, admin string, extra map[string]any,
) (newBusiness, *http.Response) {
	t.Helper()

	body := map[string]any{
		"name":        "Tea House " + randomSuffix(),
		"data_region": "sa",
		"plan_tier":   "professional",
		"market":      "sa",
		"owner_email": "owner" + randomSuffix() + "@example.test",
		"owner_name":  "Test Owner",
	}
	for k, v := range extra {
		body[k] = v
	}

	resp := h.do(t, http.MethodPost, "/api/v1/platform/tenants", admin, body)
	if resp.StatusCode != http.StatusCreated {
		return newBusiness{}, resp
	}
	defer resp.Body.Close()

	var out newBusiness
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode created business: %v", err)
	}
	h.dropTenant(t, out.TenantID)
	return out, resp
}

// dropTenant removes a tenant and everything that cascades from it.
func (h *harness) dropTenant(t *testing.T, id string) {
	t.Helper()
	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, id)
			return e
		})
	})
}

// --- the dashboard and the list ------------------------------------------

// 1. The operator can open the platform dashboard, and it answers in figures.
func TestSuperAdminCanOpenThePlatformDashboard(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	resp := h.do(t, http.MethodGet, "/api/v1/platform/health", admin, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)

	// The commercial half, which the dashboard did not have. Named
	// individually rather than counted, because a missing key reads as zero in
	// JSON and a dashboard silently showing zero businesses is worse than one
	// that fails.
	for _, key := range []string{
		"tenants", "active_subscriptions", "expired_subscriptions",
		"trial_subscriptions", "subscriptions_expiring_30d",
		"tenants_without_subscription", "business_owners",
		"suspended_tenants", "deactivated_tenants", "signups_7d",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("the dashboard does not report %q", key)
		}
	}
}

// 4. The operator can list businesses, with the columns they are there to read.
func TestSuperAdminCanListBusinessesWithOwnerAndSubscription(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	made, _ := h.createBusiness(t, admin, map[string]any{
		"owner_name": "Amina Rahman",
		"cycle":      "yearly",
	})

	resp := h.do(t, http.MethodGet,
		"/api/v1/platform/tenants?limit=50&search="+url.QueryEscape("Tea House"),
		admin, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	var row map[string]any
	for _, r := range decodeJSON(t, resp)["data"].([]any) {
		m := r.(map[string]any)
		if m["id"] == made.TenantID {
			row = m
			break
		}
	}
	if row == nil {
		t.Fatal("the business just created is not in the list")
	}

	// Who to ring, without opening the account.
	if row["owner_name"] != "Amina Rahman" {
		t.Errorf("owner_name = %v, want the owner that was just created",
			row["owner_name"])
	}
	if row["owner_email"] != made.OwnerEmail {
		t.Errorf("owner_email = %v, want %q", row["owner_email"], made.OwnerEmail)
	}

	// And the commercial state, which is the other reason to open the account.
	if row["subscription_status"] != "active" {
		t.Errorf("subscription_status = %v, want active", row["subscription_status"])
	}
	if row["subscription_expires_on"] == nil || row["subscription_expires_on"] == "" {
		t.Error("the list shows no expiry date. A subscription with no end is " +
			"one nothing can ever find expired")
	}
	// Onboarding has not been started, so it reports the step the owner is on
	// rather than nothing at all.
	if row["onboarding"] == nil || row["onboarding"] == "" {
		t.Error("the list shows no onboarding state for a business that has " +
			"only just been created")
	}
}

// --- creating a business -------------------------------------------------

// 5, 6, 7. The business, its owner and its subscription, all on the same tenant.
func TestCreatingABusinessLinksOwnerAndSubscriptionToThatTenantAlone(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	// A second business created first, so "linked to the right tenant" is a
	// claim with something to be wrong about.
	other, _ := h.createBusiness(t, admin, nil)
	made, _ := h.createBusiness(t, admin, map[string]any{
		"plan_tier":  "business",
		"cycle":      "yearly",
		"price":      "1200.00",
		"currency":   "SAR",
		"started_on": "2026-01-01",
	})

	if made.TenantID == "" || made.TenantID == other.TenantID {
		t.Fatalf("two businesses share a tenant id: %q", made.TenantID)
	}

	ctx := context.Background()
	err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// 6. The owner is on the tenant that was just created, and on no other.
		var ownerTenant uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT tenant_id FROM app_user WHERE id = $1`,
			made.OwnerUserID).Scan(&ownerTenant); e != nil {
			return fmt.Errorf("read owner: %w", e)
		}
		if ownerTenant.String() != made.TenantID {
			t.Errorf("the owner is on tenant %s, but the business created was %s",
				ownerTenant, made.TenantID)
		}

		// 7. The subscription EXISTS -- the whole point -- and is on the same
		// tenant, with the terms that were asked for.
		var subTenant uuid.UUID
		var tier, cycle, status string
		var started, expires *string
		e := tx.QueryRow(ctx, `
			SELECT tenant_id, tier::text, cycle, status,
			       started_on::text, current_period_end::text
			FROM subscription WHERE tenant_id = $1`,
			made.TenantID).Scan(&subTenant, &tier, &cycle, &status,
			&started, &expires)
		if e == pgx.ErrNoRows {
			t.Fatal("the business was created with no subscription row. The " +
				"read path coalesces that absence into a report of an ACTIVE " +
				"subscription, and a NULL period end can never expire")
		}
		if e != nil {
			return fmt.Errorf("read subscription: %w", e)
		}

		if tier != "business" {
			t.Errorf("subscription tier = %q, want business", tier)
		}
		if cycle != "yearly" {
			t.Errorf("subscription cycle = %q, want yearly", cycle)
		}
		if status != "active" {
			t.Errorf("subscription status = %q, want active", status)
		}
		if started == nil || *started != "2026-01-01" {
			t.Errorf("started_on = %v, want the date the operator gave", started)
		}
		// Derived from the start, not from the clock.
		if expires == nil || *expires != "2027-01-01" {
			t.Errorf("current_period_end = %v, want 2027-01-01 — a year after "+
				"the start", expires)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	// The tenant's own tier moved with the subscription, so the ceilings and
	// the commercial record cannot disagree.
	if made.PlanTier != "business" {
		t.Errorf("the response reports plan %q, want business", made.PlanTier)
	}
}

// The client cannot choose which tenant an owner is attached to.
//
// There is no field for it and there must never be: the tenant is the one this
// request is about to create. Sending one anyway is refused as an unknown
// field rather than quietly ignored, which is the stronger of the two
// behaviours -- an ignored field leaves the caller believing it worked.
func TestCreatingABusinessWillNotTakeATenantFromTheClient(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	victim, _ := h.createBusiness(t, admin, nil)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/tenants", admin,
		map[string]any{
			"name":        "Impostor " + randomSuffix(),
			"data_region": "sa",
			"plan_tier":   "starter",
			"market":      "sa",
			"owner_email": "owner" + randomSuffix() + "@example.test",
			"owner_name":  "Test Owner",
			"tenant_id":   victim.TenantID,
		})
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a caller named the tenant their new owner should belong to " +
			"and the route accepted it")
	}
	if resp.StatusCode != http.StatusBadRequest &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status %d — %s", resp.StatusCode, readBody(t, resp))
	}
}

// 8. The same request sent twice creates one business, not two.
//
// The screen's create button disables itself on the first press, and that is
// worth nothing here: it does not survive a reloaded form, a retried request, a
// dropped connection, or anybody calling the route directly. Two identical
// businesses is the expensive mistake, because unpicking one means deleting a
// tenant a real person may already have signed into.
//
// 18. It is also the orphan test. The guard runs as the first statement in the
// tenant transaction and the refusal aborts it, so nothing downstream of it --
// the limits, the owner, the onboarding record, the subscription -- can be left
// behind.
func TestTheSameBusinessSubmittedTwiceIsCreatedOnce(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	name := "Double Submit " + randomSuffix()
	owner := "owner" + randomSuffix() + "@example.test"
	body := map[string]any{
		"name": name, "data_region": "sa", "plan_tier": "starter",
		"market": "sa", "owner_email": owner, "owner_name": "Test Owner",
	}

	first := h.do(t, http.MethodPost, "/api/v1/platform/tenants", admin, body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first submission: status %d — %s",
			first.StatusCode, readBody(t, first))
	}
	var made newBusiness
	if err := json.NewDecoder(first.Body).Decode(&made); err != nil {
		t.Fatalf("decode: %v", err)
	}
	first.Body.Close()
	h.dropTenant(t, made.TenantID)

	resp := h.do(t, http.MethodPost, "/api/v1/platform/tenants", admin, body)
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		var second newBusiness
		_ = json.NewDecoder(resp.Body).Decode(&second)
		h.dropTenant(t, second.TenantID)
		t.Fatal("the same business was submitted twice and created twice")
	}

	// One business by that name, not two, and nothing half-written behind the
	// refusal. A tenant with no owner is unreachable and would need repair
	// from outside the product.
	ctx := context.Background()
	_ = h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var n int
		if e := tx.QueryRow(ctx,
			`SELECT count(*)::int FROM tenant WHERE name = $1`, name).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			t.Errorf("%d businesses called %q exist; the first submission "+
				"should have created exactly one", n, name)
		}

		// The first business is whole: an owner, ceilings, an onboarding
		// record and commercial terms.
		for _, table := range []string{
			"app_user", "tenant_limit", "onboarding_progress", "subscription",
		} {
			if e := tx.QueryRow(ctx, fmt.Sprintf(
				`SELECT count(*)::int FROM %s WHERE tenant_id = $1`, table),
				made.TenantID).Scan(&n); e != nil {
				return e
			}
			if n == 0 {
				t.Errorf("the business has no %s row", table)
			}
		}

		// And nothing anywhere points at a tenant that is not there.
		for _, table := range []string{
			"subscription", "tenant_limit", "onboarding_progress",
		} {
			if e := tx.QueryRow(ctx, fmt.Sprintf(`
				SELECT count(*)::int FROM %s x
				WHERE NOT EXISTS (
				  SELECT 1 FROM tenant t WHERE t.id = x.tenant_id)`, table)).
				Scan(&n); e != nil {
				return e
			}
			if n != 0 {
				t.Errorf("%d %s row(s) point at no tenant", n, table)
			}
		}
		return nil
	})
}

// And the guard is not a rule that one person owns one business.
//
// The same owner across differently-named businesses is supported the whole way
// through -- sign-in asks which one, and `tenant_login_test.go` covers that --
// so a double-submit guard that quietly forbade it would break a shape the
// product is sold on.
func TestOnePersonCanOwnTwoDifferentlyNamedBusinesses(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	owner := "owner" + randomSuffix() + "@example.test"

	first, resp := h.createBusiness(t, admin, map[string]any{
		"name": "Riyadh Branch " + randomSuffix(), "owner_email": owner,
	})
	if first.TenantID == "" {
		t.Fatalf("first business: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	second, resp := h.createBusiness(t, admin, map[string]any{
		"name": "Jeddah Branch " + randomSuffix(), "owner_email": owner,
	})
	if second.TenantID == "" {
		t.Fatalf("the same person was refused a second, differently named "+
			"business: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	if first.TenantID == second.TenantID {
		t.Fatal("both businesses landed on the same tenant")
	}
	// Two separate accounts, one per business. Cross-tenant reach is what
	// `tenant_login_test.go` pins down; this only asserts they are distinct.
	if first.OwnerUserID == second.OwnerUserID {
		t.Error("one account was attached to two businesses")
	}
}

// 9. Dates that do not describe a period are refused at the route.
//
// Not in the browser alone. `billing/plandates_test.go` pins the rules
// themselves; this pins that the route goes through them, because validation
// that only a form performs is validation anybody can skip with curl.
func TestABusinessCannotBeCreatedWithImpossibleDates(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	for _, bad := range []map[string]any{
		{"cycle": "monthly", "started_on": "2026-06-01", "expires_on": "2026-01-01"},
		{"cycle": "monthly", "started_on": "2026-06-01", "expires_on": "2026-06-01"},
		{"cycle": "lifetime", "expires_on": "2027-01-01"},
		{"cycle": "monthly", "started_on": "the first of June"},
		{"cycle": "fortnightly"},
	} {
		body := map[string]any{
			"name":        "Bad Dates " + randomSuffix(),
			"data_region": "sa",
			"plan_tier":   "starter",
			"market":      "sa",
			"owner_email": "owner" + randomSuffix() + "@example.test",
			"owner_name":  "Test Owner",
		}
		for k, v := range bad {
			body[k] = v
		}

		resp := h.do(t, http.MethodPost, "/api/v1/platform/tenants", admin, body)
		if resp.StatusCode == http.StatusCreated {
			var made newBusiness
			_ = json.NewDecoder(resp.Body).Decode(&made)
			h.dropTenant(t, made.TenantID)
			t.Errorf("accepted %v as a subscription period", bad)
		}
		resp.Body.Close()
	}
}

// 19. What the operator is told about the welcome message is true.
//
// The vocabulary has four words and none of them is "sent". Nothing in this
// product can honestly say sent until a mail provider is wired: in development
// the worker logs the message, and anywhere else it refuses the job so the
// failure is visible in the failed-jobs view rather than silent.
func TestTheOperatorIsNotToldAMailWasSent(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	made, _ := h.createBusiness(t, admin, nil)

	switch made.MailStatus {
	case "queued_for_logging", "queued_no_provider", "queued", "not_configured":
	case "":
		t.Fatal("the response says nothing about what became of the welcome " +
			"message, so an operator cannot know whether to hand the details " +
			"over themselves")
	default:
		t.Fatalf("mail_status = %q, which is not one of the four honest answers",
			made.MailStatus)
	}

	if strings.Contains(made.MailStatus, "sent") ||
		strings.Contains(made.MailStatus, "delivered") {
		t.Errorf("mail_status = %q claims a delivery that no deployment of "+
			"this product can currently perform", made.MailStatus)
	}
}

// 13. The queued message carries no credential.
//
// The temporary password is shown to the operator once, on screen, and handed
// over by them. Putting it in a queued message would write it to the jobs table
// in readable form, and from there to whatever a mail provider logs.
func TestTheWelcomeMessageCarriesNoPassword(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	made, _ := h.createBusiness(t, admin, nil)
	if made.TemporaryPassword == "" {
		t.Fatal("no temporary password was issued")
	}

	ctx := context.Background()
	_ = h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx,
			`SELECT payload::text FROM job WHERE kind = 'notify.send'`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var payload string
			if e := rows.Scan(&payload); e != nil {
				return e
			}
			if strings.Contains(payload, made.TemporaryPassword) {
				t.Fatal("a queued notification contains the owner's temporary " +
					"password in readable form")
			}
		}
		return rows.Err()
	})

	// Nor is it in the audit trail, which is permanent and append-only.
	_ = h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var n int
		e := tx.QueryRow(ctx, `
			SELECT count(*)::int FROM audit_log
			WHERE tenant_id = $1
			  AND (coalesce(after_value::text,'') LIKE '%' || $2 || '%'
			    OR coalesce(before_value::text,'') LIKE '%' || $2 || '%')`,
			made.TenantID, made.TemporaryPassword).Scan(&n)
		if e != nil {
			return e
		}
		if n != 0 {
			t.Errorf("%d audit row(s) contain the temporary password, in a "+
				"table that cannot be edited or deleted", n)
		}
		return nil
	})
}

// --- the trail -----------------------------------------------------------

// 17. A Super Admin action can be read back.
func TestTakingAClientOnAppearsInThePlatformTrail(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	made, _ := h.createBusiness(t, admin, nil)

	resp := h.do(t, http.MethodGet, "/api/v1/platform/audit?limit=100", admin, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	found := false
	for _, r := range decodeJSON(t, resp)["data"].([]any) {
		m := r.(map[string]any)
		if m["action"] == "tenant_provisioned" && m["tenant_id"] == made.TenantID {
			found = true
			if m["actor"] == nil || m["actor"] == "" {
				t.Error("the entry records no actor, so the trail cannot say " +
					"who took this client on")
			}
			if m["tenant_name"] == nil || m["tenant_name"] == "" {
				t.Error("the entry names no business, and a list of uuids is " +
					"a list nobody audits")
			}
		}
	}
	if !found {
		t.Error("provisioning a business left no readable entry in the " +
			"platform trail")
	}
}

// 2, 3. Neither an owner nor an employee can reach the platform plane.
//
// `access_test.go` walks every route against a cashier. This adds the two
// specific routes this phase introduced or changed, and adds the OWNER --
// the account most likely to be assumed harmless, since they hold every
// permission their business has.
func TestABusinessUserIsRefusedTheControlPlane(t *testing.T) {
	h := newHarness(t)

	for _, who := range []struct {
		name  string
		token string
	}{
		{"owner", h.login(t, h.seedUserWithRole(t, "owner"))},
		{"cashier", h.login(t, h.seedUserWithRole(t, "cashier"))},
	} {
		for _, path := range []string{
			"/api/v1/platform/health",
			"/api/v1/platform/tenants",
			"/api/v1/platform/audit",
		} {
			resp := h.do(t, http.MethodGet, path, who.token, nil)
			resp.Body.Close()
			// 404, not 403. A 403 confirms the route exists, which tells
			// somebody probing exactly where to keep pushing.
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s reached %s: status %d, want 404",
					who.name, path, resp.StatusCode)
			}
		}

		resp := h.do(t, http.MethodPost, "/api/v1/platform/tenants", who.token,
			map[string]any{
				"name": "Escalation " + randomSuffix(), "data_region": "sa",
				"plan_tier": "starter", "market": "sa",
				"owner_email": "x" + randomSuffix() + "@example.test",
				"owner_name":  "X",
			})
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s created a business: status %d, want 404",
				who.name, resp.StatusCode)
		}
	}
}
