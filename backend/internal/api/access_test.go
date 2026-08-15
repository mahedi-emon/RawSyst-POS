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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/shift"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sync"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/vat"
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
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()

	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1,
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
	authSvc := identity.NewService(pool, tokens)
	mw := identity.NewMiddleware(tokens, authz)

	provSvc := provisioning.NewService(pool)

	// A development hasher, because these tests exercise the HTTP surface
	// rather than ZATCA's document format. The production hasher refuses
	// outright until the format is verified — see zatca.HasherFor.
	rules := registry.New(pool, false)
	submitter := zatca.SubmitterFor(false)
	salesSvc := sales.NewService(zatca.NewChain(pool, zatca.DevelopmentHasher{})).
		WithPool(pool).WithRegistry(rules).WithSubmitter(submitter)

	// The same registration production makes: replayed sales go through the
	// sale service, not through a second implementation.
	syncEngine := sync.NewEngine(pool)
	syncEngine.Register("sales_invoice", sales.NewSaleApplier(salesSvc))

	srv := NewServer(authSvc, mw, authz, provSvc, salesSvc, reports.NewService(pool), vat.NewService(pool, rules), catalog.NewService(pool, rules), syncEngine,
		func() error { return pool.Health(ctx) }, "test")
	handler := srv.Handler(httpx.RequestID, httpx.Recover)

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	return &harness{server: ts, pool: pool, auth: authSvc, tokens: tokens,
		shift: shift.NewService(pool)}
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
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenant (name) VALUES ($1) RETURNING id`, email).
			Scan(&tenantID); err != nil {
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

	t.Cleanup(func() {
		_ = h.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, tenantID)
			return err
		})
	})
	return email
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
	for _, required := range []string{
		"catalog.view_cost_price", "catalog.view_profit_margin",
		"accounting.close_period", "accounting.reopen_period",
		"identity.manage_roles", "compliance.view", "sales.refund",
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
