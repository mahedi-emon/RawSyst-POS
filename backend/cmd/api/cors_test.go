package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// The desktop till has to be able to call this API, and nothing in a build or a
// unit suite could previously tell you whether it could.
//
// # What went wrong
//
// A packaged Tauri application serves its own assets from a custom protocol, so
// every request it makes carries an origin the framework chose:
// `http://tauri.localhost` on Windows, `tauri://localhost` elsewhere. The API's
// allow-list held the two dev-server origins and nothing else, and neither the
// deployed configuration nor .env.example mentioned the till.
//
// So the preflight came back 204 with no allow-origin header, the browser
// inside the window discarded the response before the app saw it, and the only
// thing a cashier was told was "Sign-in did not complete. Try again." Every
// installed till would have done that on the first screen.
//
// Found by driving the real application under tauri-driver — see e2e/tauri.mjs.
// Not by a unit test, not by a browser walk, and not by any amount of reading:
// the two halves are in different languages in different directories and each
// is correct on its own.
//
// # Why this is a test and not a note in the documentation
//
// It was in the documentation's shape already — a variable somebody was
// expected to set — and that is exactly what failed. A constant the API always
// allows, with a test that says so, cannot be forgotten by a deployment.

func TestTheDesktopTillsOriginIsAlwaysAllowed(t *testing.T) {
	// Both shapes, because the till's origin depends on the platform it was
	// installed on and one API serves all of them.
	for _, origin := range []string{
		"http://tauri.localhost",
		"tauri://localhost",
	} {
		t.Run(origin, func(t *testing.T) {
			t.Setenv("RAWSYST_CORS_ORIGINS", "")

			handler := httpx.CORS(corsOrigins())(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

			r := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
			r.Header.Set("Origin", origin)
			r.Header.Set("Access-Control-Request-Method", "POST")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Errorf("a preflight from %s came back with allow-origin %q. The "+
					"browser inside the till's own window will discard every "+
					"response, and the only thing the cashier is told is that "+
					"sign-in did not complete.", origin, got)
			}
			if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Errorf("the till's origin is allowed but not for credentialed " +
					"requests, so it can reach the API and never stay signed in")
			}
		})
	}
}

// Configuring browser origins must not displace the till's.
//
// The bug was an allow-list that held only what a deployment had thought to
// name. Appending rather than replacing is the whole fix, and this is what
// pins it.
func TestConfiguredOriginsAreAddedToTheTillsRatherThanReplacingIt(t *testing.T) {
	t.Setenv("RAWSYST_CORS_ORIGINS", "https://books.example.com, https://pos.example.com")

	got := corsOrigins()
	joined := strings.Join(got, " ")

	for _, want := range []string{
		"http://tauri.localhost",
		"tauri://localhost",
		"https://books.example.com",
		"https://pos.example.com",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q is not in the allow-list %v", want, got)
		}
	}
}

// A wildcard is still not a thing this API will do.
//
// The API is credentialed: a wildcard with credentials would let any page a
// signed-in owner happened to visit read their books. Adding the till's origin
// must not have quietly widened anything else.
func TestAnUnlistedOriginIsStillRefused(t *testing.T) {
	t.Setenv("RAWSYST_CORS_ORIGINS", "https://books.example.com")

	handler := httpx.CORS(corsOrigins())(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	r := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("an unlisted origin was allowed: %q", got)
	}
}

// And with nothing configured at all, the till still works.
//
// The state a developer's machine is in, and the state a deployment that forgot
// the variable is in. Both must leave the till able to sign in.
func TestWithNoConfigurationTheTillCanStillReachTheAPI(t *testing.T) {
	if err := os.Unsetenv("RAWSYST_CORS_ORIGINS"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	got := corsOrigins()
	if len(got) == 0 {
		t.Fatal("with no configuration the allow-list is empty, so a till " +
			"talking to a local API is refused by the browser in its own window")
	}
}
