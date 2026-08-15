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

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
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
	health       func() error
	version      string
}

func NewServer(
	auth *identity.Service,
	mw *identity.Middleware,
	authz *identity.Authorizer,
	prov *provisioning.Service,
	salesSvc *sales.Service,
	health func() error,
	version string,
) *Server {
	return &Server{
		auth:         auth,
		mw:           mw,
		authz:        authz,
		provisioning: prov,
		sales:        salesSvc,
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
