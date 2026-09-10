// The write freeze, in front of every business route.
//
// # Why it is here and not in each handler
//
// Because it has to be in front of ALL of them. A freeze that covers the
// routes somebody remembered is a freeze with a hole in it, and the hole is
// where the sale that gets lost goes through. There are around six hundred
// routes in this product and exactly one place they are all wrapped.
//
// # What it lets through
//
//   - Everything, when maintenance is off. One cached boolean; see
//     internal/maintenance for what the two-second cache costs.
//   - Reads, by default. A cashier looking at yesterday's totals writes nothing
//     and loses nothing, and locking them out turns a planned ten minutes into
//     a support call. An operator who wants everything closed can say so.
//   - Public routes, which are not wrapped at all: signing in and refreshing a
//     token have to work or the only way to end a freeze is a database client.
//   - Platform operators, who are not wrapped either: one of them is performing
//     the migration.
//
// # What it refuses, and with what
//
// 503, not 403. This is temporary and a client that retries later is doing
// exactly the right thing, which is what a 503 means and a 403 does not. The
// message is the operator's own sentence, because it is about this particular
// afternoon.
package api

import (
	"net/http"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/maintenance"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// frozen refuses a route while the product is in maintenance.
//
// Takes the route so the decision can be made from the METHOD rather than from
// anything in the request: `GET` and `HEAD` are reads and everything else is
// treated as a write, which is true of this API by construction — there is no
// GET in the route table that changes anything.
func (s *Server) frozen(rt Route) func(http.Handler) http.Handler {
	readOnly := rt.Method == http.MethodGet || rt.Method == http.MethodHead ||
		rt.Method == http.MethodOptions

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.maintenance == nil {
				next.ServeHTTP(w, r)
				return
			}
			state := s.maintenance.Current(r.Context())
			if !state.Active {
				next.ServeHTTP(w, r)
				return
			}
			if readOnly && state.AllowReads {
				next.ServeHTTP(w, r)
				return
			}
			httpx.Error(w, r, maintenance.Refused(state))
		})
	}
}
