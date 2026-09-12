// Enforcing what a subscription's STATE allows, as opposed to what its tier
// includes.
//
// `entitlement.go` next door answers "does this plan sell that module". This
// answers a different question that nothing was asking: "is this business still
// entitled to anything at all". A shop whose subscription ran out eighteen
// months ago passed every check in the product and went on ringing up sales.
//
// # Why middleware, and why here
//
// The feature gate cannot carry this, because it only wraps routes that name a
// module — most routes name none, and the rule has to reach all of them.
//
// The shape is taken from `frozen()` in maintenance_middleware.go, which solves
// the same problem for a migration window: classify the route read or write by
// its method, consult a cached state, let reads through, refuse writes. That
// file is the precedent and this deliberately looks like it.
//
// # Read-only, not locked out
//
// A business whose subscription has lapsed keeps every read. Two reasons, and
// the second is the one that settles it.
//
// A shop locked out entirely cannot see what it owes or find the screen to pay
// it, which makes recovery harder than the lapse warrants and turns a billing
// problem into a support call.
//
// And the export routes must keep working. A business that has stopped paying
// is precisely the one entitled to take its data elsewhere; a product that
// holds it hostage to an unpaid invoice is one nobody should build. Those
// routes are GETs, so they are allowed by the same rule that allows every other
// read, without needing an exception.
//
// # 402, like the tier gate
//
// The caller is authenticated and holds the permission. What is missing is
// commercial, and a 403 would send them to their owner to ask for a permission
// they already have. `CodeFeatureNotInPlan` maps to 402 Payment Required, which
// is the right family of answer and the one the client already knows how to
// route to a subscription screen.
package api

import (
	"net/http"
	"strings"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/actor"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/httpx"
)

// writableWhenLapsed are the write routes that keep working when a
// subscription has run out or been suspended.
//
// Deliberately short, and every entry is either something the person needs in
// order to RECOVER, or something about their own account rather than about the
// business.
//
// Note what is not here: nothing that records a sale, moves stock, changes a
// price, edits a person, or touches the books. That is the whole point.
var writableWhenLapsed = map[string]bool{
	// Ending a session, and choosing a new password. Neither is trading, and
	// being unable to sign out of a lapsed account would be absurd.
	"/api/v1/auth/logout":          true,
	"/api/v1/auth/change-password": true,

	// The second factor on your own account. Somebody whose subscription
	// lapsed while they were setting up MFA should be able to finish.
	"/api/v1/auth/mfa/begin":          true,
	"/api/v1/auth/mfa/complete":       true,
	"/api/v1/auth/mfa/disable":        true,
	"/api/v1/auth/mfa/recovery-codes": true,

	// Reaching Biz1core. The one channel through which a suspension gets
	// explained or lifted, and closing it would leave the client with nothing
	// but the telephone.
	"/api/v1/support/tickets": true,

	// Marking a notification read. It is a write in the HTTP sense and is not
	// a business act; leaving it blocked would give a lapsed tenant a bell
	// that never stops ringing.
	"/api/v1/notifications/read": true,
}

// writablePrefixesWhenLapsed are the same, for routes that carry an id.
var writablePrefixesWhenLapsed = []string{
	// Replying on, or closing, a support ticket you already raised.
	"/api/v1/support/tickets/",
	// One notification, marked read.
	"/api/v1/notifications/",
}

// subscribed refuses an operational write from a business that may no longer
// make one.
//
// The route's method decides read from write, exactly as the maintenance freeze
// does: GET, HEAD and OPTIONS are reads and are never refused here.
func (s *Server) subscribed(rt Route) func(http.Handler) http.Handler {
	reads := rt.Method == http.MethodGet || rt.Method == http.MethodHead ||
		rt.Method == http.MethodOptions
	exempt := writesAllowedWhenLapsed(rt.Pattern)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.billing == nil {
				next.ServeHTTP(w, r)
				return
			}

			a := actor.From(r.Context())
			// The platform operator is not a subscriber. Gating the control
			// plane on a tenant's standing would ask the wrong question of the
			// wrong party — and would let a suspended tenant's own state stop
			// the operator from lifting it.
			if a.IsSuperAdmin || a.TenantID.String() == "" {
				next.ServeHTTP(w, r)
				return
			}

			// The tenant comes from the verified token, never from the path,
			// the query or the body. There is nothing a caller can submit that
			// changes which business this asks about.
			standing := s.billing.StandingOf(r.Context(), a.TenantID)

			// A switched-off business reaches nothing at all, reads included.
			// This is the one state that is not read-only, and it is checked
			// before the read short-circuit for exactly that reason.
			if !standing.Read {
				httpx.Error(w, r, errs.New(errs.CodeForbidden, standing.Blocks()))
				return
			}

			if reads || exempt || standing.Write {
				next.ServeHTTP(w, r)
				return
			}

			httpx.Error(w, r, errs.New(errs.CodeFeatureNotInPlan,
				standing.Blocks()))
		})
	}
}

// writesAllowedWhenLapsed is whether a write route stays open to a lapsed
// business.
func writesAllowedWhenLapsed(pattern string) bool {
	if writableWhenLapsed[pattern] {
		return true
	}
	for _, p := range writablePrefixesWhenLapsed {
		if strings.HasPrefix(pattern, p) {
			return true
		}
	}
	return false
}
