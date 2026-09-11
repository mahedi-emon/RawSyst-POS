// Which hostname may call which API route.
//
// # The half that was missing
//
// Phase 2 split the two user interfaces by hostname at the web tier: the
// control plane's PAGES answer only on the console host, and the business
// application's pages only on the business host. That is `web-next/src/proxy.ts`
// and it is worth having.
//
// It is also worthless on its own against anybody who does not use a browser.
// The web tier lets every `/api/` path straight through to this service — it
// has to, because both origins call the API on their own origin — so a split
// enforced only at the edge is one a direct call walks past. `curl` at the
// business hostname reached every platform route exactly as before.
//
// This is the API's own half of the same rule.
//
// # What it is, and what it is not
//
// It is not the security boundary and nothing here should be mistaken for one.
// `RequireSuperAdmin` answers 404 to any caller who is not a platform operator,
// and that is what actually protects the control plane. A caller can put any
// value in a `Host` header, so this refuses nothing that authorization would
// have allowed.
//
// What it buys is reduction of surface. The platform API is not reachable at
// the address every shop in the world has in their browser history, which means
// a credential stolen from a shop cannot even be tried against it there, and
// scanning the business hostname reveals no control plane to attack.
//
// # Unset means unchanged
//
// With no console host configured — a developer's machine, and every deployment
// that has not opted in — this allows everything and the API behaves exactly as
// it did. Turning the split on is a deployment decision.
package api

import (
	"net/http"
	"strings"
)

// sharedAPIPrefixes are the routes both hostnames must reach.
//
// Short, and each entry is load-bearing:
//
//   - `/api/v1/auth/` — the console signs in through the same endpoint as
//     everybody else, and must reach it on ITS OWN origin or the
//     `SameSite=Strict` refresh cookie is never sent. That is the whole reason
//     no CORS is needed here. It also covers refresh, logout, the password
//     screens and the second factor, every one of which the console needs.
//   - `/api/v1/meta/` — the build a support engineer asks for, and the ping a
//     terminal uses. Neither belongs to either half.
//   - `/api/v1/plans` — the price list. Public to any signed-in caller, and the
//     console's billing screen reads it to populate the tier dropdown; it is
//     the one non-platform route the control plane calls.
//
// Anything not listed and not under `/api/v1/platform/` belongs to the business
// application. `TestTheConsoleReachesEveryRouteItsScreensCall` walks the
// console's own sources and fails if a new screen starts calling something this
// list does not cover.
var sharedAPIPrefixes = []string{
	"/api/v1/auth/",
	"/api/v1/meta/",
	"/api/v1/plans",
}

// platformAPIPrefix is the control plane's own routes.
const platformAPIPrefix = "/api/v1/platform/"

// hostServesPath decides whether a request arriving at `host` may reach `path`.
//
// Pure, so the rule can be tested without a server, a proxy or a container in
// the way — which matters here more than usual, because the last time this
// product decided something from a hostname the unit tests all passed and the
// behaviour was wrong inside a container. See the note on reading the header in
// `hostAllowed` below.
//
// # Why an unrecognised host is treated as the business host
//
// Fail towards reachability, not towards refusal. A reverse proxy that passes a
// hostname nobody predicted — a health check addressing the container by name,
// an internal call, an operator testing by IP — would otherwise be refused
// everywhere, turning a configuration surprise into an outage.
//
// The direction that matters is still enforced: the platform API is reachable
// ONLY from the console host, so an unrecognised host gets exactly what a shop
// gets, which is the safe side of this particular fence.
func hostServesPath(consoleHost, host, path string) bool {
	configured := normaliseAPIHost(consoleHost)
	if configured == "" {
		return true
	}

	// Health and readiness carry no `/api/v1` prefix and must answer to
	// anything. Docker's own health check addresses the container as
	// `localhost`, which is neither hostname, and refusing it would restart a
	// perfectly healthy service in a loop.
	if !strings.HasPrefix(path, "/api/") {
		return true
	}

	for _, p := range sharedAPIPrefixes {
		if path == strings.TrimSuffix(p, "/") || strings.HasPrefix(path, p) {
			return true
		}
	}

	onConsole := normaliseAPIHost(host) == configured
	if strings.HasPrefix(path, platformAPIPrefix) ||
		path == strings.TrimSuffix(platformAPIPrefix, "/") {
		return onConsole
	}
	return !onConsole
}

// onTheRightHost refuses a route the caller's hostname does not serve.
//
// 404, never 403 and never a redirect, for the same reason `RequireSuperAdmin`
// answers 404: a 403 confirms the route exists and a redirect would publish the
// console's address to whoever guessed the path. Both undo most of the value of
// not linking to it anywhere.
//
// It runs BEFORE authentication, so a platform route on the business hostname
// is absent rather than merely forbidden — there is nothing to sign in to and
// nothing to probe.
func (s *Server) onTheRightHost(rt Route) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.consoleHost == "" {
				next.ServeHTTP(w, r)
				return
			}
			host := requestHost(r.Host, r.Header.Get("X-Forwarded-Host"))
			if hostServesPath(s.consoleHost, host, rt.Pattern) {
				next.ServeHTTP(w, r)
				return
			}
			writeNotFound(w, r)
		})
	}
}

// requestHost is the hostname a request was addressed to.
//
// `r.Host` is the `Host` header, which every reverse proxy in this deployment
// preserves: the bundled nginx sets `proxy_set_header Host $host`, Nginx Proxy
// Manager does the same by default, and Cloudflare forwards the original host
// to the origin. So the ordinary header is the right answer and no special
// configuration is needed.
//
// `X-Forwarded-Host` is consulted only when `Host` is absent or is something a
// proxy substituted for its own upstream address. It is checked SECOND rather
// than first on purpose: it is the more forgeable of the two and preferring it
// would let any caller choose their hostname even where the proxy was being
// careful.
//
// Neither is trusted as proof of anything. See the file note: this reduces
// surface and `RequireSuperAdmin` is what refuses people.
func requestHost(host string, forwarded string) string {
	if h := normaliseAPIHost(host); h != "" {
		return h
	}
	// A forwarded chain is comma-separated; the first entry is the original.
	if i := strings.Index(forwarded, ","); i >= 0 {
		forwarded = forwarded[:i]
	}
	return normaliseAPIHost(forwarded)
}

// normaliseAPIHost strips the port and lower-cases, because a browser sends a
// port and a configuration file usually does not.
//
// An IPv6 literal is bracketed and its colons are not a port separator.
func normaliseAPIHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(h, "[") {
		if end := strings.Index(h, "]"); end >= 0 {
			return h[:end+1]
		}
		return h
	}
	if i := strings.Index(h, ":"); i >= 0 {
		return h[:i]
	}
	return h
}
