package identity

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

type ctxKey int

const ctxKeyGrants ctxKey = iota

// GrantsFrom returns the resolved grants for the current request.
func GrantsFrom(ctx context.Context) *Grants {
	g, _ := ctx.Value(ctxKeyGrants).(*Grants)
	return g
}

// Middleware carries the dependencies the auth middlewares need.
type Middleware struct {
	tokens  *TokenService
	authz   *Authorizer
	devices DeviceChecker
}

// DeviceChecker reports whether a paired terminal may still act.
//
// An interface rather than a direct dependency because the devices package
// already imports this one for password hashing, and a cycle would be the
// price of the convenience. It also keeps the middleware testable without a
// terminal.
type DeviceChecker interface {
	Active(ctx context.Context, deviceID uuid.UUID) error
}

func NewMiddleware(tokens *TokenService, authz *Authorizer) *Middleware {
	return &Middleware{tokens: tokens, authz: authz}
}

// WithDevices makes device-bound tokens subject to the terminal's current
// status on every request.
//
// Without it a token issued to a till keeps working until it expires, which for
// a terminal revoked because it was stolen is the wrong answer by up to its
// full lifetime.
func (m *Middleware) WithDevices(d DeviceChecker) *Middleware {
	m.devices = d
	return m
}

// Authenticate verifies the bearer token and attaches the actor and grants.
//
// It does not decide whether the caller may do anything — that is Require's
// job. Splitting the two means a route cannot accidentally be authenticated but
// unauthorized: Require is what registers a route in the permission table, and
// a route with no Require fails the route-coverage test.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			httpx.Error(w, r, errs.New(errs.CodeUnauthenticated,
				"Please sign in to continue."))
			return
		}

		act, err := m.tokens.Verify(raw)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}

		// A device-bound token is only as good as the terminal behind it, right
		// now. Same reasoning as resolving permissions per request rather than
		// trusting what the token said when it was minted.
		if act.DeviceID != uuid.Nil && m.devices != nil {
			if err := m.devices.Active(r.Context(), act.DeviceID); err != nil {
				httpx.Error(w, r, err)
				return
			}
		}

		ctx := actor.Into(r.Context(), act)

		grants, err := m.authz.Resolve(ctx, act)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		ctx = context.WithValue(ctx, ctxKeyGrants, grants)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Require gates a route on a permission.
//
// Blueprint A6.2: "All permission checks are enforced server-side on every API
// call — a hidden button in the UI is never treated as real security." QA gate
// M7 calls every restricted route directly as a Cashier and expects a refusal.
func (m *Middleware) Require(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			g := GrantsFrom(r.Context())
			if g == nil {
				httpx.Error(w, r, errs.New(errs.CodeUnauthenticated,
					"Please sign in to continue."))
				return
			}

			// The platform plane holds no tenant permissions by design, so a
			// tenant route is closed to it. Blueprint A4: Super Admin "does not
			// interfere in the Owner's day-to-day business data."
			if g.IsSuperAdmin() {
				httpx.Error(w, r, errs.New(errs.CodeForbidden,
					"Platform administrators cannot access tenant business data."))
				return
			}

			if !g.Can(permission) {
				httpx.Error(w, r, errs.Newf(errs.CodeForbidden,
					"You do not have permission to do this. Ask your owner for the "+
						"%q permission if you need it.", permission))
				return
			}
			next.ServeHTTP(w, r.WithContext(r.Context()))
		})
	}
}

// RequireSuperAdmin gates the platform control plane.
func (m *Middleware) RequireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g := GrantsFrom(r.Context())
		if g == nil || !g.IsSuperAdmin() {
			// 404, not 403. Confirming that a platform endpoint exists tells an
			// attacker where to aim; the same reasoning as returning 404 for
			// another tenant's record.
			httpx.Error(w, r, errs.New(errs.CodeNotFound, "Not found."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CheckAmountLimit reports whether the actor may authorise an amount, and
// returns a message naming the ceiling when they may not.
//
// Handlers call this rather than a middleware, because the amount is in the
// request body and middleware would have to consume it. The message is written
// so the cashier knows the next step — the client turns it into a manager-PIN
// prompt rather than a dead end.
func CheckAmountLimit(ctx context.Context, amount decimal.Decimal, what string) error {
	g := GrantsFrom(ctx)
	if g == nil {
		return errs.New(errs.CodeUnauthenticated, "You are not signed in.")
	}
	if g.WithinLimit(amount) {
		return nil
	}
	limit := g.AmountLimit()
	return errs.Newf(errs.CodeAmountLimitExceeded,
		"This %s is %s, above your limit of %s. Ask a manager to approve it.",
		what, amount.Abs().StringFixed(2), limit.StringFixed(2))
}

// CheckStoreScope reports whether the actor may act in a given store.
//
// A separate call rather than middleware, for the same reason as the amount
// check: the store id comes from the request body or a path parameter the
// handler has already parsed.
func CheckStoreScope(ctx context.Context, storeID uuid.UUID) error {
	g := GrantsFrom(ctx)
	if g == nil {
		return errs.New(errs.CodeUnauthenticated, "You are not signed in.")
	}
	if g.InStore(storeID) {
		return nil
	}
	// Not found rather than forbidden: a manager scoped to one branch should
	// not learn which other branches exist by probing ids.
	return errs.New(errs.CodeNotFound, "That store was not found.")
}

// CheckWarehouseScope reports whether the actor may act in a given warehouse.
//
// The third of design 04's four scope dimensions, and the same shape as the
// store check because the question is the same one: blueprint A6.2 scopes
// inventory staff to a warehouse, and a goods receipt names the warehouse it is
// received into.
func CheckWarehouseScope(ctx context.Context, warehouseID uuid.UUID) error {
	g := GrantsFrom(ctx)
	if g == nil {
		return errs.New(errs.CodeUnauthenticated, "You are not signed in.")
	}
	if g.InWarehouse(warehouseID) {
		return nil
	}
	return errs.New(errs.CodeNotFound, "That warehouse was not found.")
}

// CheckCompanyScope reports whether the actor may act within a legal entity.
// Relevant for group tenants, where one Owner login spans several companies
// that keep separate books and separate VAT registrations.
func CheckCompanyScope(ctx context.Context, companyID uuid.UUID) error {
	g := GrantsFrom(ctx)
	if g == nil {
		return errs.New(errs.CodeUnauthenticated, "You are not signed in.")
	}
	if g.InCompany(companyID) {
		return nil
	}
	return errs.New(errs.CodeNotFound, "That company was not found.")
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}
