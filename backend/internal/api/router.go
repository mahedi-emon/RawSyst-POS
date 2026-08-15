// Package api wires HTTP routes to handlers.
//
// # The route registry
//
// Every route is declared in Routes() as data before it is mounted. That is not
// bookkeeping: it makes two properties testable that are otherwise only
// conventions.
//
//   - Every tenant route names the permission it requires. A route added
//     without one fails TestEveryRouteDeclaresAPermission, so QA gate M7 cannot
//     be bypassed by forgetting rather than by deciding.
//   - The full permission surface is enumerable, so the seeded roles can be
//     checked against it and a typo like "sales.refnud" is caught by a test
//     rather than by a cashier who cannot issue a refund.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sync"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/vat"
)

// Access says who may reach a route.
type Access int

const (
	// AccessPublic is reachable without a token. Deliberately rare: sign-in,
	// token refresh, and health checks only.
	AccessPublic Access = iota

	// AccessAuthenticated needs a valid token but no particular permission.
	// Used only for "about the caller themselves" routes, where the identity in
	// the token IS the authorization.
	AccessAuthenticated

	// AccessPermission needs a named permission.
	AccessPermission

	// AccessSuperAdmin is the platform control plane.
	AccessSuperAdmin
)

// Route is one endpoint.
type Route struct {
	Method     string
	Pattern    string
	Access     Access
	Permission string // required when Access is AccessPermission
	Handler    http.HandlerFunc

	// Why records the reason a route is public or permission-free. Required for
	// AccessPublic and AccessAuthenticated, so widening access is a deliberate,
	// reviewed act rather than a default someone drifted into.
	Why string
}

// Server holds the handler dependencies.
type Server struct {
	auth         *identity.Service
	mw           *identity.Middleware
	authz        *identity.Authorizer
	provisioning *provisioning.Service
	sales        *sales.Service
	reports      *reports.Service
	vat          *vat.Service
	catalog      *catalog.Service
	sync         *sync.Engine
	health       func() error
	version      string
}

func NewServer(
	auth *identity.Service,
	mw *identity.Middleware,
	authz *identity.Authorizer,
	prov *provisioning.Service,
	salesSvc *sales.Service,
	reportSvc *reports.Service,
	vatSvc *vat.Service,
	catalogSvc *catalog.Service,
	syncEngine *sync.Engine,
	health func() error,
	version string,
) *Server {
	return &Server{
		auth:         auth,
		mw:           mw,
		authz:        authz,
		provisioning: prov,
		sales:        salesSvc,
		reports:      reportSvc,
		vat:          vatSvc,
		catalog:      catalogSvc,
		sync:         syncEngine,
		health:       health,
		version:      version,
	}
}

// Routes returns every route as data.
func (s *Server) Routes() []Route {
	return []Route{
		// --- public ---
		{http.MethodGet, "/healthz", AccessPublic, "", s.handleLive,
			"liveness probe; must answer before any dependency is reachable"},
		{http.MethodGet, "/readyz", AccessPublic, "", s.handleReady,
			"readiness probe for the load balancer"},
		{http.MethodGet, "/api/v1/meta/version", AccessPublic, "", s.handleVersion,
			"build version, so a support call can establish what the client is running"},

		{http.MethodPost, "/api/v1/auth/login", AccessPublic, "", s.handleLogin,
			"sign-in is how a caller obtains a token; it cannot require one"},
		{http.MethodPost, "/api/v1/auth/refresh", AccessPublic, "", s.handleRefresh,
			"the access token is expired by definition; the refresh token authenticates"},

		// --- authenticated, no permission ---
		{http.MethodPost, "/api/v1/auth/logout", AccessAuthenticated, "", s.handleLogout,
			"ends the caller's own session; the token is the authorization"},
		{http.MethodGet, "/api/v1/auth/me", AccessAuthenticated, "", s.handleMe,
			"returns the caller's own identity and permissions, so the client can shape its UI"},
		{http.MethodPost, "/api/v1/auth/change-password", AccessAuthenticated, "", s.handleChangePassword,
			"changes the caller's own password; the current password is re-verified"},
		{http.MethodGet, "/api/v1/catalog/snapshot", AccessPermission, "catalog.view", s.handleCatalogSnapshot,
			"the sellable catalogue a till caches to scan offline; cursored so later pulls are deltas"},
		{http.MethodGet, "/api/v1/meta/ping", AccessAuthenticated, "", s.handlePing,
			"a terminal asking whether it can sync; the token IS the answer, so no permission applies"},

		// --- onboarding (blueprint A5) ---
		//
		// Gated on identity.view rather than a dedicated permission: setup is
		// the Owner's job, and the Owner role already holds it. Inventing an
		// onboarding-only permission would mean every tenant's custom roles
		// need updating before anyone can finish setup.
		{http.MethodGet, "/api/v1/onboarding", AccessPermission, "identity.view",
			s.handleOnboardingProgress, ""},
		{http.MethodPut, "/api/v1/onboarding/steps/{step}", AccessPermission, "identity.edit",
			s.handleOnboardingSaveStep, ""},
		{http.MethodPost, "/api/v1/onboarding/steps/{step}/complete", AccessPermission, "identity.edit",
			s.handleOnboardingCompleteStep, ""},
		{http.MethodPost, "/api/v1/onboarding/company", AccessPermission, "identity.edit",
			s.handleOnboardingCommitCompany, ""},

		// --- catalogue ---
		//
		// Cost price and margin never cross this boundary. A Cashier holds
		// catalog.view and is deliberately denied catalog.view_cost_price, so a
		// payload carrying cost would defeat the masking the permission exists
		// for — correctly gated, and leaking anyway.
		{http.MethodPost, "/api/v1/catalog/products", AccessPermission, "catalog.create",
			s.handleCreateProduct, ""},
		{http.MethodGet, "/api/v1/catalog/products", AccessPermission, "catalog.view",
			s.handleListProducts, ""},
		{http.MethodPost, "/api/v1/catalog/products/{productID}/matrix", AccessPermission, "catalog.create",
			s.handleGenerateMatrix, ""},
		{http.MethodGet, "/api/v1/catalog/products/{productID}/matrix", AccessPermission, "catalog.view",
			s.handleReadMatrix, ""},
		// Withdrawing needs catalog.delete even though nothing is deleted: it is
		// the destructive-intent permission, and a variant off sale is as
		// disruptive to a shop as one removed would be.
		{http.MethodDelete, "/api/v1/catalog/variants/{variantID}", AccessPermission, "catalog.delete",
			s.handleWithdrawVariant, ""},
		{http.MethodGet, "/api/v1/catalog/scan", AccessPermission, "catalog.view",
			s.handleScanBarcode, ""},

		// --- point of sale ---
		//
		// Refunding is its own permission, not a consequence of being able to
		// sell. The seeded Cashier happens to hold both, but a tenant can build
		// a role that sells and cannot reverse — which matters because refund
		// fraud is among the commonest forms of till theft, and C14 treats a
		// return as its own act for exactly that reason.
		{http.MethodPost, "/api/v1/pos/sales", AccessPermission, "sales.create",
			s.handleCreateSale, ""},
		{http.MethodPost, "/api/v1/pos/returns", AccessPermission, "sales.refund",
			s.handleCreateReturn, ""},
		{http.MethodGet, "/api/v1/pos/sales/{invoiceID}", AccessPermission, "sales.view",
			s.handleGetSale, ""},
		// The terminal hands back what it signed locally. Gated on sales.create
		// because it completes a sale rather than reading one, and refused
		// outright for a session with no terminal behind it.
		{http.MethodPut, "/api/v1/pos/sales/{invoiceID}/signed-document",
			AccessPermission, "sales.create", s.handleUploadSignedDocument, ""},

		// --- sync ---
		//
		// The device comes from the token, never the body. A terminal that could
		// name its own device id would push another till's sales onto another
		// till's ZATCA chain, and both tills belong to the same tenant so
		// row-level security would not notice.
		{http.MethodPost, "/api/v1/sync/push", AccessPermission, "sales.create",
			s.handleSyncPush, ""},
		{http.MethodGet, "/api/v1/sync/health", AccessPermission, "sales.view",
			s.handleSyncHealth, ""},

		// --- financial statements ---
		//
		// Gated on accounting.view, not sales.view. A statement exposes the whole
		// company position — margin, cash, what is owed — which is precisely the
		// information a cashier is deliberately kept away from.
		{http.MethodGet, "/api/v1/reports/trial-balance", AccessPermission, "accounting.view",
			s.handleTrialBalance, ""},
		{http.MethodGet, "/api/v1/reports/profit-and-loss", AccessPermission, "accounting.view",
			s.handleProfitAndLoss, ""},
		{http.MethodGet, "/api/v1/reports/balance-sheet", AccessPermission, "accounting.view",
			s.handleBalanceSheet, ""},
		{http.MethodGet, "/api/v1/reports/cash-flow", AccessPermission, "accounting.view",
			s.handleCashFlow, ""},

		// Preparation, never filing. The official form layout is a regulatory
		// value and no verified rule for it exists, so these totals are not
		// mapped onto numbered boxes.
		{http.MethodGet, "/api/v1/reports/vat-return", AccessPermission, "accounting.view",
			s.handleVATReturn, ""},

		// --- platform control plane ---
		{http.MethodPost, "/api/v1/platform/tenants", AccessSuperAdmin, "",
			s.handleCreateTenant, ""},
		{http.MethodPost, "/api/v1/platform/users/{userID}/reset-password",
			AccessSuperAdmin, "", s.handleAdminResetPassword, ""},
	}
}

// Handler builds the mounted router.
func (s *Server) Handler(mws ...func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	for _, mw := range mws {
		r.Use(mw)
	}

	for _, rt := range s.Routes() {
		switch rt.Access {
		case AccessPublic:
			r.Method(rt.Method, rt.Pattern, rt.Handler)

		case AccessAuthenticated:
			r.With(s.mw.Authenticate).Method(rt.Method, rt.Pattern, rt.Handler)

		case AccessPermission:
			r.With(s.mw.Authenticate, s.mw.Require(rt.Permission)).
				Method(rt.Method, rt.Pattern, rt.Handler)

		case AccessSuperAdmin:
			r.With(s.mw.Authenticate, s.mw.RequireSuperAdmin).
				Method(rt.Method, rt.Pattern, rt.Handler)
		}
	}

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeNotFound(w, r)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeNotFound(w, r)
	})

	return r
}
