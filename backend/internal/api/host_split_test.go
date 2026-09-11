//go:build integration

// The hostname split, driven through the real router.
//
// `host_policy_test.go` pins the rule. This proves the server asks it, which is
// a separate claim and the one that has been wrong before: when this product
// last decided something from a hostname, every unit test passed and the
// behaviour inside a container was the opposite of what was intended, because
// the code read the server's own origin instead of the request's. Only driving
// real HTTP with a real `Host` header catches that.
package api

import (
	"net/http"
	"strings"
	"testing"
)

// atHost sends a request with a chosen `Host`, the way a browser addressing a
// hostname does and the way every reverse proxy in this deployment forwards it.
func (h *harness) atHost(
	t *testing.T, method, path, host, token string,
) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("calling %s at %s: %v", path, host, err)
	}
	return resp
}

func TestTheSplitHoldsOverRealHTTP(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	// Off by default, which every existing deployment depends on.
	before := h.atHost(t, http.MethodGet, "/api/v1/platform/health",
		businessHost, admin)
	before.Body.Close()
	if before.StatusCode != http.StatusOK {
		t.Fatalf("with no console host configured, the platform API was "+
			"refused on the business hostname: %d. Unset must change nothing.",
			before.StatusCode)
	}

	// Turned on the way a deployment turns it on.
	h.srv.WithConsoleHost(consoleHost)
	t.Cleanup(func() { h.srv.WithConsoleHost("") })
	// The router is built once, and the middleware reads the field on every
	// request — so the change takes effect without rebuilding the server,
	// which is also what makes this safe to flip in a test.

	cases := []struct {
		name   string
		host   string
		path   string
		method string
		want   int
	}{
		{"platform API on the console", consoleHost,
			"/api/v1/platform/health", http.MethodGet, http.StatusOK},

		// The point of the whole exercise.
		{"platform API on the business host", businessHost,
			"/api/v1/platform/health", http.MethodGet, http.StatusNotFound},
		{"platform tenants on the business host", businessHost,
			"/api/v1/platform/tenants", http.MethodGet, http.StatusNotFound},

		// A hostname nobody configured is treated as a shop's: it reaches the
		// business API and never the platform one.
		{"platform API on an unknown host", "192.0.2.10:8080",
			"/api/v1/platform/health", http.MethodGet, http.StatusNotFound},

		// Health answers to anything. Docker's own check addresses the
		// container as localhost, and refusing it restarts a healthy service.
		{"health on the console", consoleHost, "/healthz",
			http.MethodGet, http.StatusOK},
		{"health on the business host", businessHost, "/healthz",
			http.MethodGet, http.StatusOK},
		{"health on the container itself", "localhost:8080", "/healthz",
			http.MethodGet, http.StatusOK},

		// Signing in works on both, or the console cannot sign in at all.
		{"version on the console", consoleHost, "/api/v1/meta/version",
			http.MethodGet, http.StatusOK},
		{"version on the business host", businessHost, "/api/v1/meta/version",
			http.MethodGet, http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := h.atHost(t, c.method, c.path, c.host, admin)
			defer resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Errorf("%s %s at %s: status %d, want %d — %s",
					c.method, c.path, c.host, resp.StatusCode, c.want,
					readBody(t, resp))
			}
		})
	}
}

// Signing in reaches the same endpoint from both hostnames.
//
// The load-bearing case. If the console cannot POST to `/api/v1/auth/login` on
// its own origin, the only fixes are CORS and a weakened cookie — the exact
// trade this design exists to avoid.
func TestTheConsoleCanSignInOnItsOwnOrigin(t *testing.T) {
	h := newHarness(t)
	email := h.seedSuperAdmin(t)
	h.srv.WithConsoleHost(consoleHost)
	t.Cleanup(func() { h.srv.WithConsoleHost("") })

	for _, host := range []string{consoleHost, businessHost} {
		req, _ := http.NewRequest(http.MethodPost,
			h.server.URL+"/api/v1/auth/login",
			strings.NewReader(`{"email":"`+email+`","password":"`+testPassword+`"}`))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("signing in at %s: %v", host, err)
		}
		body := readBody(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("signing in at %s: %d — %s", host, resp.StatusCode, body)
		}
	}
}

// A business user gains nothing by claiming the console's hostname.
//
// The header is not proof of anything and was never treated as such: the split
// reduces surface, and `RequireSuperAdmin` is what refuses people. This says so
// in a test rather than only in a comment.
func TestClaimingTheConsoleHostnameGrantsNothing(t *testing.T) {
	h := newHarness(t)
	owner := h.login(t, h.seedUserWithRole(t, "owner"))
	h.srv.WithConsoleHost(consoleHost)
	t.Cleanup(func() { h.srv.WithConsoleHost("") })

	for _, path := range []string{
		"/api/v1/platform/health",
		"/api/v1/platform/tenants",
		"/api/v1/platform/audit",
	} {
		resp := h.atHost(t, http.MethodGet, path, consoleHost, owner)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("an owner reached %s by sending the console's hostname: "+
				"%d, want 404", path, resp.StatusCode)
		}
	}
}

// And the business application is not served from the console hostname.
func TestBusinessRoutesAreAbsentFromTheConsole(t *testing.T) {
	h := newHarness(t)
	shop := h.seedShop(t, "owner")
	h.srv.WithConsoleHost(consoleHost)
	t.Cleanup(func() { h.srv.WithConsoleHost("") })

	for _, path := range []string{
		"/api/v1/catalog/products",
		"/api/v1/subscription",
		"/api/v1/exports/customers",
	} {
		resp := h.atHost(t, http.MethodGet, path, consoleHost, shop.token)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s answered on the console hostname: %d, want 404",
				path, resp.StatusCode)
		}

		// And still answers on the business hostname, so the split has not
		// simply broken the product.
		ok := h.atHost(t, http.MethodGet,
			path+"?company_id="+shop.companyID.String(), businessHost, shop.token)
		ok.Body.Close()
		if ok.StatusCode == http.StatusNotFound {
			t.Errorf("%s is now absent from the business hostname too", path)
		}
	}
}
