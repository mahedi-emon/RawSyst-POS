//go:build integration

// QA gate M7, mechanised.
//
// Blueprint Part M item 7: "Attempt every restricted action via direct API call
// while logged in as a Cashier Ã¢â‚¬â€ all must be rejected server-side, not just
// hidden in the UI."
//
// These tests do exactly that: real HTTP requests against the real router with
// a real database, as a real Cashier holding the seeded Cashier role.
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/aftersales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/assets"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/billing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/branding"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/compliance"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/devices"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/docs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/egs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/expenses"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/fiscal"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/fx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/group"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/insight"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/integration"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/labels"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/loyalty"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/notify"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/ops"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/orders"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/payments"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/people"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/cache"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/live"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/metrics"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/secrets"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platformops"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/portability"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/portal"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/privacy"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/promotions"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/settlement"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/shift"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/stockops"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sync"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/treasury"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/vat"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/wallet"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/workflow"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

const testPassword = "a reasonably long passphrase"

type harness struct {
	server *httptest.Server
	pool   *db.Pool
	auth   *identity.Service

	// tokens lets a test mint a token bound to a terminal. A POS carries one;
	// a browser session does not, and the difference is what decides whether an
	// invoice can be issued at all.
	tokens *identity.TokenService
	shift  *shift.Service

	// hub is the same instance the server was given, so a test can publish
	// onto the channel the sockets are actually reading. Two instances would
	// let a test prove something about a hub the router never calls.
	hub *live.Hub

	// authz lets a scope test drop a user's cached grants after narrowing their
	// assignment. Grants are resolved once and held for a TTL, so a limit
	// written after the first request would otherwise not be seen.
	authz *identity.Authorizer

	// rules lets a test empty the regulatory cache, so a lookup genuinely goes
	// to the database. A warm cache hides how a rule is fetched, which is
	// exactly what TestASaleDoesNotHoldTwoConnections is measuring.
	rules *registry.Service
}

// harnessPoolConns is how many database connections the test server may hold
// at once.
//
// Named rather than inline because a test asserts against it:
// TestASaleDoesNotHoldTwoConnections runs exactly this many sales at once, and
// the number is only meaningful if it is the same number the pool was built
// with. See that test for what a request holding two connections does to a
// shop.
const harnessPoolConns = 8

func newHarness(t *testing.T) *harness {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()

	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: harnessPoolConns, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tokens := identity.NewTokenService(config.Auth{
		JWTSecret:       []byte("api-integration-secret-at-least-32-bytes"),
		Issuer:          "rawsyst-test",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 720 * time.Hour,
	})
	authz := identity.NewAuthorizer(pool)
	authSvc := identity.NewService(pool, tokens).WithCipher(testCipher(t))
	mw := identity.NewMiddleware(tokens, authz)

	provSvc := provisioning.NewService(pool)

	// The real hasher. There is only one now: the canonicalisation, the UBL
	// field set and the QR encoding are all verified, so hashing is an ordinary
	// computation rather than something to be stubbed out. A development hasher
	// that differed from production would break the chain the moment a database
	// moved between them.
	rules := registry.New(pool, false)
	// No encryption key in this fixture, so submission is unavailable and
	// invoices stay queued -- which is what these access tests want: they are
	// about who may reach a route, not about ZATCA accepting anything.
	submitter := zatca.SubmitterFrom(
		zatca.NewCredentialStore(pool, nil), zatca.EnvironmentSandbox)
	// Shared with the routes, so a sale records its redemption through the same
	// instance the till quotes from -- as production does.
	promotionsSvc := promotions.NewService(pool)
	salesSvc := sales.NewService(zatca.NewChain(pool, zatca.StandardHasher{})).
		WithPool(pool).WithRegistry(rules).WithSubmitter(submitter).
		WithPromotions(promotionsSvc)

	// The same registration production makes: replayed sales go through the
	// sale service, not through a second implementation.
	syncEngine := sync.NewEngine(pool)
	syncEngine.Register("sales_invoice", sales.NewSaleApplier(salesSvc))

	deviceSvc := devices.NewService(pool)
	mw = mw.WithDevices(deviceSvc)

	// One instance, handed to the server AND kept on the harness. Two would let
	// a test prove something about a service the router never calls, which is
	// exactly how the shift module stayed unreachable while its tests passed.
	shiftSvc := shift.NewService(pool)

	// F1's approval engine, built first because the two commit paths it gates
	// hold a reference to it. One instance: the engine reads and writes one
	// set of tables and a second would be a second queue.
	workflowSvc := workflow.NewService(pool)
	purchasingSvc := purchasing.NewService(pool).WithApprovals(workflowSvc).WithRates(fx.New(pool)).WithRules(rules)
	// The portal hands a one-time code to the same queue the staff recovery
	// codes go through, so a code that exists and a message that will be sent
	// commit together.
	portalSvc := portal.NewService(pool).WithQueue(jobs.NewQueue(pool))

	// One hub, handed to the server AND kept on the harness. See the note on
	// shiftSvc: two instances is how a module stayed unreachable while its
	// tests passed.
	hub := live.NewHub(ctx, cache.NewMemory(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = hub.Close() })

	srv := NewServer(authSvc, mw, authz, provSvc, salesSvc, reports.NewService(pool), vat.NewService(pool, rules), catalog.NewService(pool, rules), syncEngine, purchasingSvc, receivables.NewService(pool), deviceSvc, egs.NewService(pool), branding.NewService(pool), shiftSvc, settlement.NewService(pool), expenses.NewService(pool, rules).WithApprovals(workflowSvc), stockops.NewService(pool), fiscal.NewService(pool), treasury.NewService(pool), assets.NewService(pool), promotionsSvc, orders.NewService(pool).WithSales(salesSvc), loyalty.NewService(pool), wallet.NewService(pool), workflowSvc, notify.NewService(pool), integration.NewService(pool, testCipher(t)), portability.NewService(pool), ops.NewService(pool), labels.NewService(pool, rules), insight.NewService(pool), platformops.NewService(pool), aftersales.NewService(pool), docs.NewService(pool), billing.NewService(pool), group.NewService(pool), portalSvc, privacy.NewService(pool, rules), compliance.NewService(pool, rules), people.NewService(pool, rules), fx.New(pool), rules, audit.NewService(pool),
		func() error { return pool.Health(ctx) }, "test").
		WithJournals(accounting.NewJournalService(pool)).
		// The card providers, sealed with the same test keyring the
		// integrations use. Without this the payment routes report that the
		// installation cannot hold a credential, which is true but not what
		// these tests are about.
		WithPayments(payments.NewService(pool, testCipher(t))).
		// The live socket and the metrics wrapper, both as production has
		// them. The wrapper matters here: it wraps every route including the
		// socket, and a wrapper that hid http.Hijacker would refuse every
		// upgrade -- which is exactly the kind of thing only the real stack
		// catches.
		WithLive(hub).
		WithMetrics(metrics.New(), "")
	handler := srv.Handler(httpx.RequestID, httpx.Recover)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return &harness{server: ts, pool: pool, auth: authSvc, tokens: tokens, authz: authz,
		shift: shiftSvc, rules: rules, hub: hub}
}

// seedUserWithRole provisions a tenant and a user holding a seeded role
// template, returning the user's email.
func (h *harness) seedUserWithRole(t *testing.T, roleKey string) string {
	t.Helper()
	ctx := context.Background()
	email := "u" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] + "@example.test"

	hash, err := identity.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	// Provisioning splits across two planes, mirroring production exactly.
	// The tenant and its first user are created by the platform (blueprint A5);
	// roles belong to the tenant and are unreachable from the platform plane by
	// design, so the second half runs in tenant context.
	var tenantID, userID uuid.UUID
	err = h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// On the top tier, because a fixture exists to test the module under
		// test and not the subscription in front of it. H5's gate is real now
		// (see entitlement.go), so a starter tenant would be refused payroll,
		// loyalty, analytics and the rest with a 402 -- correct behaviour, and
		// nothing to do with what those tests are checking. The gate has its
		// own tests, which provision the tier they mean to exercise.
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenant (name, plan_tier) VALUES ($1, 'enterprise')
			 RETURNING id`, email).
			Scan(&tenantID); err != nil {
			return err
		}
		// The plan's ceilings, which real provisioning always writes (see
		// provisioning.Service). Without the row a tenant has no user
		// allowance on record, and the staff routes correctly refuse to guess
		// one — so a fixture that omits it makes them fail for a reason that
		// has nothing to do with the test.
		if _, err := tx.Exec(ctx, `
			INSERT INTO tenant_limit
			  (tenant_id, max_companies, max_stores, max_users, max_terminals,
			   max_skus, max_held_carts, max_custom_roles, max_storage_mb,
			   sms_credits)
			SELECT $1, max_companies, max_stores, max_users, max_terminals,
			       max_skus, max_held_carts, max_custom_roles, max_storage_mb,
			       sms_credits
			FROM plan_tier_default WHERE tier = 'professional'::plan_tier`,
			tenantID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO app_user (tenant_id, email, full_name, password_hash, status)
			VALUES ($1,$2,'Test User',$3,'active') RETURNING id`,
			tenantID, email, hash).Scan(&userID)
	})
	if err != nil {
		t.Fatalf("provision tenant for %s: %v", roleKey, err)
	}

	err = h.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// Clone the platform role template into the tenant, exactly as the role
		// builder does, so the test exercises the real seeded permission set
		// rather than a hand-written one.
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO role (tenant_id, key, name, cloned_from)
			SELECT $1, key, name, id FROM role
			WHERE tenant_id IS NULL AND key = $2
			RETURNING id`, tenantID, roleKey).Scan(&roleID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permission (role_id, permission)
			SELECT $1, rp.permission FROM role_permission rp
			JOIN role r ON r.id = rp.role_id
			WHERE r.tenant_id IS NULL AND r.key = $2`, roleID, roleKey); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO user_role_assignment (tenant_id, user_id, role_id)
			VALUES ($1,$2,$3)`, tenantID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed %s role: %v", roleKey, err)
	}

	// Best effort, and the error is deliberately dropped rather than
	// overlooked.
	//
	// The cascade from tenant reaches session_refresh_token, sales_invoice,
	// journal_entry and every other table carrying a reject_delete() trigger,
	// and the trigger refuses — which is what it is for. Posted history is
	// never deleted, and a test tenant’s history is history. So this succeeds
	// only for a tenant that never logged in and never posted anything, and
	// fails for every other one.
	//
	// Failing the test on it would be wrong: nothing is broken, the guarantee
	// is working. Weakening the trigger so tests can tidy up would trade a
	// production invariant for a smaller database, which is a bad trade. The
	// database is therefore disposable — `make test-db-reset` recreates it —
	// and this note exists so the next person does not spend an hour
	// rediscovering why the cleanup appears to do nothing.
	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, tenantID)
			return err
		})
	})
	return email
}

// grantPermissions adds verbs to the role a seeded user already holds.
//
// For a test about delegation. The subset rule compares the role being
// assigned against the CALLER's own permissions, so a test that wants to prove
// a store manager cannot create an Owner has to first give that manager enough
// to make the attempt — otherwise the request is refused for want of
// `identity.create` and proves nothing about escalation.
func (h *harness) grantPermissions(t *testing.T, email string, permissions ...string) {
	t.Helper()
	ctx := context.Background()

	var tenantID uuid.UUID
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT tenant_id FROM app_user WHERE email = $1`, email).Scan(&tenantID)
	}); err != nil {
		t.Fatalf("finding the tenant for %s: %v", email, err)
	}

	if err := h.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		for _, p := range permissions {
			if _, err := tx.Exec(ctx, `
				INSERT INTO role_permission (role_id, permission)
				SELECT a.role_id, $2
				FROM user_role_assignment a
				JOIN app_user u ON u.id = a.user_id
				WHERE u.email = $1
				ON CONFLICT DO NOTHING`, email, p); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("granting %v to %s: %v", permissions, email, err)
	}
}

// roleID returns a platform template role's id, which is what the people
// routes offer and accept.
func (h *harness) roleID(t *testing.T, key string) string {
	t.Helper()
	var id string
	if err := h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT id::text FROM role WHERE tenant_id IS NULL AND key = $1`, key).
			Scan(&id)
	}); err != nil {
		t.Fatalf("finding the %s role: %v", key, err)
	}
	return id
}

func (h *harness) login(t *testing.T, email string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": testPassword})
	resp, err := http.Post(h.server.URL+"/api/v1/auth/login",
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if out.AccessToken == "" {
		t.Fatal("login returned no access token")
	}
	return out.AccessToken
}

func (h *harness) do(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, h.server.URL+path, r)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return resp
}

// doRaw sends a body verbatim, for step payloads whose shape is free-form.
func (h *harness) doRaw(t *testing.T, method, path, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return resp
}

// readBody drains a response for use in a failure message.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "<unreadable>"
	}
	return string(b)
}

// --- the gate ----------------------------------------------------------

// Every non-public route must refuse an unauthenticated caller.
func TestUnauthenticatedIsRefusedEverywhere(t *testing.T) {
	h := newHarness(t)
	s := &Server{}

	for _, rt := range s.Routes() {
		if rt.Access == AccessPublic {
			continue
		}
		path := strings.ReplaceAll(rt.Pattern, "{userID}", uuid.NewString())

		t.Run(rt.Method+" "+rt.Pattern, func(t *testing.T) {
			resp := h.do(t, rt.Method, path, "", map[string]string{})
			defer resp.Body.Close()

			// 401 for tenant routes; the platform plane answers 404 so its
			// existence is not confirmed to an anonymous prober.
			if resp.StatusCode != http.StatusUnauthorized &&
				resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 401 or 404 for an unauthenticated call",
					resp.StatusCode)
			}
		})
	}
}

// A Cashier must not reach the platform control plane. This is QA gate M7's
// central case: the call is made directly, with no UI in the way.
func TestCashierCannotReachPlatformControlPlane(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "cashier")
	token := h.login(t, email)

	resp := h.do(t, http.MethodPost,
		"/api/v1/platform/users/"+uuid.NewString()+"/reset-password",
		token, map[string]string{"reason": "attempting a privilege escalation"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 Ã¢â‚¬â€ a tenant user must not learn that the "+
			"platform endpoint exists", resp.StatusCode)
	}
}

// The seeded Cashier role must not carry cost or margin visibility. Blueprint
// A6.1: for a cashier, "cost price and margins are always hidden".
func TestCashierHasNoCostOrMarginPermission(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "cashier")
	token := h.login(t, email)

	resp := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var me struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(me.Permissions) == 0 {
		t.Fatal("cashier resolved to no permissions at all; the role clone failed")
	}

	for _, forbidden := range []string{
		"catalog.view_cost_price",
		"catalog.view_profit_margin",
		"accounting.view",
		"accounting.close_period",
		"identity.manage_roles",
	} {
		for _, held := range me.Permissions {
			if held == forbidden {
				t.Errorf("the seeded Cashier role grants %q, which blueprint A6.1 "+
					"says must always be hidden from a cashier", forbidden)
			}
		}
	}
}

// The Owner role must carry the permissions a cashier lacks, or the seed is
// wrong in the opposite direction and nobody can run the business.
func TestOwnerHoldsTheFullPermissionSet(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")
	token := h.login(t, email)

	resp := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	defer resp.Body.Close()

	var me struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode: %v", err)
	}

	held := make(map[string]bool, len(me.Permissions))
	for _, p := range me.Permissions {
		held[p] = true
	}
	// `einvoicing.view`, not `compliance.view`. The e-invoicing module renamed
	// the verb when it arrived in 0043 and granted the new name to every role
	// that had held the old one, so nobody lost access — but the old grant was
	// left behind, and this list went on naming it. 0074 removed the widow;
	// this is the verb that has meant "can see where the invoices have got to"
	// ever since.
	for _, required := range []string{
		"catalog.view_cost_price", "catalog.view_profit_margin",
		"accounting.close_period", "accounting.reopen_period",
		"identity.manage_roles", "einvoicing.view", "sales.refund",
	} {
		if !held[required] {
			t.Errorf("the seeded Owner role is missing %q", required)
		}
	}
}

// A Super Admin must be refused on tenant business routes. Blueprint A4 draws
// the line: the platform operator administers the platform, and "does not
// interfere in the Owner's day-to-day business data."
func TestSuperAdminIsRefusedOnTenantRoutes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	email := "admin" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
	hash, err := identity.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	var adminID uuid.UUID
	err = h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO app_user (tenant_id, email, full_name, password_hash, status)
			VALUES (NULL,$1,'Platform Admin',$2,'active') RETURNING id`,
			email, hash).Scan(&adminID)
	})
	if err != nil {
		t.Fatalf("seed super admin: %v", err)
	}
	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM app_user WHERE id = $1`, adminID)
			return err
		})
	})

	token := h.login(t, email)

	// The platform route works for them.
	resp := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("super admin cannot read their own identity: status %d", resp.StatusCode)
	}

	var me struct {
		IsSuperAdmin bool     `json:"is_super_admin"`
		Permissions  []string `json:"permissions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !me.IsSuperAdmin {
		t.Fatal("the platform admin was not recognised as one")
	}
	if len(me.Permissions) != 0 {
		t.Fatalf("a platform admin holds tenant permissions %v; they must hold none",
			me.Permissions)
	}
}

// Health endpoints must not require a token, or a load balancer cannot use them.
func TestHealthEndpointsArePublic(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/healthz", "/readyz", "/api/v1/meta/version"} {
		resp := h.do(t, http.MethodGet, path, "", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d, want 200 without a token", path, resp.StatusCode)
		}
	}
}

// An error response must carry a stable code and a correlation id, and must
// never leak internal detail.
func TestErrorEnvelopeIsWellFormed(t *testing.T) {
	h := newHarness(t)

	resp := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "nobody@example.test", "password": "wrong password"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("response carries no X-Request-Id")
	}

	var env struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != "unauthenticated" {
		t.Errorf("code = %q, want %q", env.Error.Code, "unauthenticated")
	}
	if env.Error.RequestID == "" {
		t.Error("error envelope carries no request id, so support cannot trace it")
	}
	for _, leak := range []string{"pgx", "SQL", "sql:", "goroutine", "panic"} {
		if strings.Contains(env.Error.Message, leak) {
			t.Errorf("error message leaks internal detail (%q): %s", leak, env.Error.Message)
		}
	}
}

// An unknown field in a request body is rejected rather than ignored. On a
// financial API, a client sending `amout` must be told, not silently charged
// zero.
func TestUnknownFieldsAreRejected(t *testing.T) {
	h := newHarness(t)

	resp := h.do(t, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "a@b.test", "password": "x", "unexpected": "value"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown field", resp.StatusCode)
	}
}

// Onboarding a till with ZATCA binds the business's tax identity to a
// certificate and consumes a one-time password the taxpayer had to fetch from
// their own Fatoora account. Only the Owner may do it.
//
// The seeing/doing split from 0043 is the thing under test: everyone who reads
// compliance state must still be able to SEE onboarding status, including the
// reason ZATCA gave for a refusal, because a till that stopped selling is
// usually noticed by whoever is standing at it.
func TestOnlyTheOwnerMayOnboardATillWithZATCA(t *testing.T) {
	h := newHarness(t)

	for _, c := range []struct {
		role       string
		mayOnboard bool
		maySee     bool
	}{
		{"owner", true, true},
		{"accountant", false, true},
		{"store_manager", false, true},
		{"auditor", false, true},
		{"cashier", false, false},
	} {
		t.Run(c.role, func(t *testing.T) {
			email := h.seedUserWithRole(t, c.role)
			token := h.login(t, email)

			resp := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
			defer resp.Body.Close()
			var me struct {
				Permissions []string `json:"permissions"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
				t.Fatalf("decode: %v", err)
			}

			holds := func(want string) bool {
				for _, p := range me.Permissions {
					if p == want {
						return true
					}
				}
				return false
			}

			if got := holds("einvoicing.onboard"); got != c.mayOnboard {
				if c.mayOnboard {
					t.Errorf("%s cannot onboard a till, but must be able to", c.role)
				} else {
					t.Errorf("%s holds einvoicing.onboard. Onboarding registers the "+
						"business with the tax authority and belongs to the Owner alone",
						c.role)
				}
			}
			if got := holds("einvoicing.view"); got != c.maySee {
				if c.maySee {
					t.Errorf("%s cannot see onboarding status, but needs to: a till "+
						"that stops selling is noticed by whoever is standing at it", c.role)
				} else {
					t.Errorf("%s holds einvoicing.view unexpectedly", c.role)
				}
			}
		})
	}
}

// And the route itself must enforce it, not just the permission list. A
// permission a client cannot see is worthless if the handler does not check it.
func TestTheOnboardingRouteRefusesAnyoneButTheOwner(t *testing.T) {
	h := newHarness(t)
	unit := uuid.New().String()

	for _, c := range []struct {
		role      string
		wantAllow bool
	}{
		{"owner", true},
		{"accountant", false},
		{"store_manager", false},
		{"cashier", false},
	} {
		t.Run(c.role, func(t *testing.T) {
			email := h.seedUserWithRole(t, c.role)
			token := h.login(t, email)

			resp := h.do(t, http.MethodPost,
				"/api/v1/einvoicing/units/"+unit+"/onboarding/compliance", token,
				map[string]string{
					"environment": "simulation",
					"csr":         "-----BEGIN CERTIFICATE REQUEST-----",
					"otp":         "123456",
				})
			defer resp.Body.Close()

			if c.wantAllow {
				// The Owner gets past authorization. What happens next depends
				// on the installation holding a key and the till existing, and
				// this test asserts neither -- only that it is not a 403.
				if resp.StatusCode == http.StatusForbidden {
					t.Error("the Owner was refused permission to onboard a till")
				}
				return
			}
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s got status %d posting to the onboarding route, want 403",
					c.role, resp.StatusCode)
			}
		})
	}
}

// testCipher is the keyring the integration tests run with.
//
// A real one rather than nil: a nil cipher makes SaveEndpoint refuse, and a
// test suite where every webhook route returns "this installation has no
// encryption key" proves nothing about the routes.
func testCipher(t *testing.T) *secrets.Cipher {
	t.Helper()
	key, err := secrets.ParseKey(1, base64.StdEncoding.EncodeToString(
		bytes.Repeat([]byte{0x2a}, 32)))
	if err != nil {
		t.Fatalf("build a test key: %v", err)
	}
	c, err := secrets.New(key)
	if err != nil {
		t.Fatalf("build a test keyring: %v", err)
	}
	return c
}
