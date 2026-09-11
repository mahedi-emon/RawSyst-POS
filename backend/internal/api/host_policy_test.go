// Which hostname may call which API route.
//
// No server, no database. The rule is a decision about two strings, and the one
// thing this product has already learned the hard way about deciding from a
// hostname is that the unit tests can all pass while the behaviour inside a
// container is wrong — so `TestTheSplitHoldsOverRealHTTP` in
// `host_split_test.go` drives the same rule through the actual router, and this
// file pins the rule itself.

package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	consoleHost  = "console.example.com"
	businessHost = "app.example.com"
)

func TestTheConsoleServesThePlatformAPIAndNobodyElseDoes(t *testing.T) {
	for _, path := range []string{
		"/api/v1/platform/health",
		"/api/v1/platform/tenants",
		"/api/v1/platform/audit",
		"/api/v1/platform/tenants/{tenantID}/standing",
	} {
		if !hostServesPath(consoleHost, consoleHost, path) {
			t.Errorf("the console cannot reach %s, which is its own API", path)
		}
		// The thing this exists to prevent: the platform API answering at the
		// address every shop has in their browser history.
		if hostServesPath(consoleHost, businessHost, path) {
			t.Errorf("the business hostname reaches %s", path)
		}
	}
}

func TestTheBusinessAPIIsNotOnTheConsole(t *testing.T) {
	for _, path := range []string{
		"/api/v1/catalog/products",
		"/api/v1/pos/sales",
		"/api/v1/customers",
		"/api/v1/subscription",
		"/api/v1/exports/customers",
	} {
		if !hostServesPath(consoleHost, businessHost, path) {
			t.Errorf("the business hostname cannot reach %s", path)
		}
		if hostServesPath(consoleHost, consoleHost, path) {
			t.Errorf("the console reaches %s, a business route", path)
		}
	}
}

// The load-bearing exception. Both origins sign in through the same endpoint
// and must reach it on their OWN origin, or the SameSite=Strict refresh cookie
// is never sent — which is the whole reason this split needs no CORS.
func TestSigningInWorksOnBothHostnames(t *testing.T) {
	for _, path := range []string{
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
		"/api/v1/auth/logout",
		"/api/v1/auth/me",
		"/api/v1/auth/change-password",
		"/api/v1/auth/forgot-password",
		"/api/v1/auth/reset-password",
		"/api/v1/auth/mfa",
		"/api/v1/auth/mfa/begin",
		"/api/v1/meta/version",
		"/api/v1/plans",
	} {
		for _, host := range []string{consoleHost, businessHost} {
			if !hostServesPath(consoleHost, host, path) {
				t.Errorf("%s cannot reach %s", host, path)
			}
		}
	}
}

// A health check addresses the container, not the site.
//
// Docker's own check calls `localhost`, which is neither hostname. Refusing it
// would restart a perfectly healthy service in a loop — a configuration
// surprise turned into an outage.
func TestHealthChecksAnswerToAnything(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		for _, host := range []string{
			consoleHost, businessHost, "localhost:8080", "172.18.0.4", "",
		} {
			if !hostServesPath(consoleHost, host, path) {
				t.Errorf("%q cannot reach %s", host, path)
			}
		}
	}
}

// An unrecognised hostname is treated as a business one.
//
// Fail towards reachability: a proxy passing something nobody predicted must
// not lock everybody out. The direction that matters is still enforced, which
// the platform assertion below is what proves.
func TestAnUnknownHostGetsWhatAShopGets(t *testing.T) {
	for _, host := range []string{
		"172.18.0.4:8080", "rawsyst-api", "localhost", "some.other.domain",
	} {
		if !hostServesPath(consoleHost, host, "/api/v1/catalog/products") {
			t.Errorf("%q was refused a business route", host)
		}
		if hostServesPath(consoleHost, host, "/api/v1/platform/tenants") {
			t.Errorf("%q reached the platform API", host)
		}
	}
}

func TestHostsAreComparedTheWayABrowserSendsThem(t *testing.T) {
	for _, host := range []string{
		"CONSOLE.example.com",
		"console.example.com:443",
		" console.example.com ",
	} {
		if !hostServesPath(consoleHost, host, "/api/v1/platform/tenants") {
			t.Errorf("%q was not recognised as the console", host)
		}
	}
	// And a configured value with a scheme or a path somebody pasted in is
	// reduced by config.hostOnly before it reaches here; a substring is never
	// enough on its own.
	for _, host := range []string{
		"console.example.com.attacker.test",
		"notconsole.example.com",
		"example.com",
	} {
		if hostServesPath(consoleHost, host, "/api/v1/platform/tenants") {
			t.Errorf("%q was accepted as the console", host)
		}
	}
}

// Unset changes nothing, which is every deployment that has not opted in.
func TestNoConsoleHostMeansTheAPIBehavesExactlyAsBefore(t *testing.T) {
	for _, path := range []string{
		"/api/v1/platform/tenants",
		"/api/v1/catalog/products",
		"/healthz",
	} {
		for _, host := range []string{"localhost:3000", "app.example.com", ""} {
			if !hostServesPath("", host, path) {
				t.Errorf("with no console host, %q was refused %s", host, path)
			}
		}
	}
}

func TestTheHostHeaderIsPreferredOverTheForwardedOne(t *testing.T) {
	// `Host` wins when present. Preferring the forwarded header would let any
	// caller choose their hostname even where the proxy was being careful.
	if got := requestHost("app.example.com", "console.example.com"); got != businessHost {
		t.Errorf("requestHost = %q, want the Host header", got)
	}
	// And it is the fallback, first entry of the chain, when Host is absent.
	if got := requestHost("", "console.example.com, proxy.internal"); got != consoleHost {
		t.Errorf("requestHost = %q, want the first forwarded entry", got)
	}
	if got := requestHost("", ""); got != "" {
		t.Errorf("requestHost = %q, want empty", got)
	}
}

// Every route the console's own screens call must be reachable on the console.
//
// # Why this reads the front end from a Go test
//
// Because the failure it prevents is silent and lands in production. The
// console calls exactly one route outside `/api/v1/platform/` today — the price
// list its billing screen reads to fill the tier dropdown — and nothing stops
// somebody adding a second next year. The screen would work perfectly in
// development, where no console host is configured and this rule allows
// everything, and 404 the moment it reached a deployment that had opted in.
//
// So the list of shared prefixes is checked against the sources that depend on
// it rather than against somebody's memory of them.
func TestTheConsoleReachesEveryRouteItsScreensCall(t *testing.T) {
	roots := []string{
		filepath.Join("..", "..", "..", "web-next", "src", "app", "(platform)"),
		filepath.Join("..", "..", "..", "web-next", "src", "lib", "platform"),
	}

	// A quoted path that looks like an API call: leading slash, lower case,
	// no file extension. Template placeholders are reduced to a segment.
	call := regexp.MustCompile(`['"` + "`" + `](/[a-z0-9][a-z0-9/_${}.-]*)['"` + "`" + `]`)

	found := map[string]string{}
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			if !strings.HasSuffix(path, ".tsx") && !strings.HasSuffix(path, ".ts") {
				return nil
			}
			if strings.HasSuffix(path, ".test.ts") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range call.FindAllStringSubmatch(string(body), -1) {
				p := m[1]
				// Interface routes, not API ones. The client prefixes
				// `/api/v1` itself; a path that is one of this app's own pages
				// is governed by `web-next/src/lib/origin.ts` instead.
				if strings.HasPrefix(p, "/platform/") || p == "/platform" {
					continue
				}
				if strings.Contains(p, ".") {
					continue // a file, not a route
				}
				found["/api/v1"+p] = path
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	if len(found) == 0 {
		t.Skip("the console sources were not found from here; nothing to check")
	}

	for path, where := range found {
		if !hostServesPath(consoleHost, consoleHost, path) {
			t.Errorf("%s calls %s, which the console hostname cannot reach. "+
				"Add its prefix to sharedAPIPrefixes, or the screen will work "+
				"in development and 404 in a deployment that has opted in.",
				where, path)
		}
	}
	t.Logf("%d distinct API paths called by the console, all reachable", len(found))
}
