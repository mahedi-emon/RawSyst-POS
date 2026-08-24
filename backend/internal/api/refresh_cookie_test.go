//go:build integration

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The refresh token must not be reachable by script in a browser.
//
// It used to be returned in the sign-in body, and the browser client kept it in
// localStorage — where an extension, a successful XSS or a compromised
// dependency could read it. A refresh token is the durable half of a session:
// stealing one is stealing the account until somebody notices.

// signInRaw signs in and hands back the raw response, so the cookies and the
// body can both be inspected.
func signInRaw(t *testing.T, h *harness, email string, native bool) *http.Response {
	t.Helper()
	body := map[string]string{"email": email, "password": testPassword}
	raw, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/v1/auth/login", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("building the sign-in: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if native {
		req.Header.Set(clientKindHeader, clientKindNative)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("signing in: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in status %d", resp.StatusCode)
	}
	return resp
}

func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func decodeLogin(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding the sign-in: %v", err)
	}
	return out
}

func TestABrowserSignInPutsTheRefreshTokenOutOfReachOfScript(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")

	resp := signInRaw(t, h, email, false)
	body := decodeLogin(t, resp)

	if _, present := body["refresh_token"]; present {
		t.Error("the sign-in body carried a refresh token to a browser. Anything " +
			"running in the page could read and keep it.")
	}
	if body["access_token"] == "" || body["access_token"] == nil {
		t.Fatal("the sign-in returned no access token")
	}

	refresh := cookieNamed(resp, refreshCookieName)
	if refresh == nil {
		t.Fatal("no refresh cookie was set, so the browser has no way to stay signed in")
	}
	if !refresh.HttpOnly {
		t.Error("the refresh cookie is readable by script, which is the whole " +
			"thing this design exists to prevent")
	}
	if refresh.SameSite != http.SameSiteStrictMode {
		t.Errorf("the refresh cookie is SameSite=%v; Strict is what stops it "+
			"being attached to a cross-site request", refresh.SameSite)
	}
	if refresh.Path != refreshCookiePath {
		t.Errorf("the refresh cookie is scoped to %q; it should ride only on the "+
			"auth routes rather than on every API call", refresh.Path)
	}

	// Its partner must be readable: echoing it is the double-submit check.
	csrf := cookieNamed(resp, csrfCookieName)
	if csrf == nil {
		t.Fatal("no CSRF cookie was set, so the refresh route cannot be verified")
	}
	if csrf.HttpOnly {
		t.Error("the CSRF cookie is httpOnly, so the page cannot echo it and " +
			"every refresh would be refused")
	}
	// document.cookie only exposes cookies whose path matches the current
	// page, and the app is served from "/". Scoped to the auth path the token
	// is invisible to the page that has to echo it, and every refresh is
	// refused with a 403 while the cookie sits there looking correct.
	if csrf.Path != "/" {
		t.Errorf("the CSRF cookie is scoped to %q, so a page at / cannot read "+
			"it and cannot echo it", csrf.Path)
	}
}

// The native app has no browser cookie jar, so it still receives the token —
// but only when it asks, so the safe behaviour is what a silent client gets.
func TestTheNativeAppStillReceivesItsRefreshToken(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")

	body := decodeLogin(t, signInRaw(t, h, email, true))
	if body["refresh_token"] == nil || body["refresh_token"] == "" {
		t.Error("the native client asked for a refresh token and did not get one")
	}
}

// A refresh authenticated by the cookie must prove it came from a page on this
// origin. Without the echo it is forgeable from any site the user visits.
func TestARefreshWithoutTheCSRFEchoIsRefused(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")

	signIn := signInRaw(t, h, email, false)
	refresh := cookieNamed(signIn, refreshCookieName)
	csrf := cookieNamed(signIn, csrfCookieName)
	signIn.Body.Close()
	if refresh == nil || csrf == nil {
		t.Fatal("the sign-in did not set the session cookies")
	}

	call := func(withEcho bool) int {
		req, _ := http.NewRequest(http.MethodPost,
			h.server.URL+"/api/v1/auth/refresh", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(refresh)
		req.AddCookie(csrf)
		if withEcho {
			req.Header.Set(csrfHeaderName, csrf.Value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("refreshing: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := call(false); got != http.StatusForbidden {
		t.Errorf("a refresh with no CSRF echo returned %d, want 403. The cookie "+
			"is attached automatically, so without the echo any site could "+
			"trigger a rotation", got)
	}
	if got := call(true); got != http.StatusOK {
		t.Errorf("a refresh WITH the echo returned %d, want 200", got)
	}
}

// A wrong echo is as bad as none.
func TestARefreshWithAMismatchedEchoIsRefused(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")

	signIn := signInRaw(t, h, email, false)
	refresh := cookieNamed(signIn, refreshCookieName)
	csrf := cookieNamed(signIn, csrfCookieName)
	signIn.Body.Close()

	req, _ := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/v1/auth/refresh", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(refresh)
	req.AddCookie(csrf)
	req.Header.Set(csrfHeaderName, csrf.Value+"x")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a refresh with a mismatched echo returned %d, want 403",
			resp.StatusCode)
	}
}

// The refresh must actually work, rotate, and keep the new token out of reach.
func TestACookieRefreshRotatesAndStaysOutOfReach(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")

	signIn := signInRaw(t, h, email, false)
	first := decodeLogin(t, signIn)
	refresh := cookieNamed(signIn, refreshCookieName)
	csrf := cookieNamed(signIn, csrfCookieName)

	req, _ := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/v1/auth/refresh", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(refresh)
	req.AddCookie(csrf)
	req.Header.Set(csrfHeaderName, csrf.Value)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status %d", resp.StatusCode)
	}

	rotated := cookieNamed(resp, refreshCookieName)
	body := decodeLogin(t, resp)

	if _, present := body["refresh_token"]; present {
		t.Error("the refresh body carried the new token to a browser")
	}
	if body["access_token"] == nil || body["access_token"] == "" {
		t.Fatal("the refresh returned no access token")
	}
	if body["access_token"] == first["access_token"] {
		t.Error("the refresh returned the same access token it was given")
	}
	if rotated == nil {
		t.Fatal("the refresh set no new refresh cookie, so the token was not rotated")
	}
	if rotated.Value == refresh.Value {
		t.Error("the refresh cookie was not rotated; a stolen token would stay " +
			"valid for as long as the session did")
	}
	if !rotated.HttpOnly {
		t.Error("the rotated refresh cookie is readable by script")
	}
}

// The access token the refresh returns has to actually work.
func TestTheTokenARefreshReturnsIsUsable(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")

	signIn := signInRaw(t, h, email, false)
	refresh := cookieNamed(signIn, refreshCookieName)
	csrf := cookieNamed(signIn, csrfCookieName)
	signIn.Body.Close()

	req, _ := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/v1/auth/refresh", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(refresh)
	req.AddCookie(csrf)
	req.Header.Set(csrfHeaderName, csrf.Value)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	body := decodeLogin(t, resp)

	token, _ := body["access_token"].(string)
	if token == "" {
		t.Fatal("no access token")
	}

	check := h.do(t, http.MethodGet, "/api/v1/auth/me", token, nil)
	defer check.Body.Close()
	if check.StatusCode != http.StatusOK {
		t.Errorf("the refreshed token was refused: status %d", check.StatusCode)
	}
}

// Signing out must take the cookie away, or a shared till hands the next
// person a working session.
func TestSigningOutClearsTheRefreshCookie(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")
	token := h.login(t, email)

	resp := h.do(t, http.MethodPost, "/api/v1/auth/logout", token, map[string]string{})
	defer resp.Body.Close()

	cleared := cookieNamed(resp, refreshCookieName)
	if cleared == nil {
		t.Fatal("signing out set no refresh cookie at all, so an existing one stays")
	}
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("the refresh cookie was not expired on sign-out: value=%q maxAge=%d",
			cleared.Value, cleared.MaxAge)
	}
}

// A refresh with no token at all is refused, and says so in words a person can
// act on rather than a bare 401.
func TestARefreshWithNoTokenSaysTheSessionEnded(t *testing.T) {
	h := newHarness(t)

	req, _ := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/v1/auth/refresh", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", resp.StatusCode)
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	if !strings.Contains(strings.ToLower(payload.Error.Message), "sign in") {
		t.Errorf("the refusal does not tell the person what to do: %q",
			payload.Error.Message)
	}
}

// A refusal must take the dead cookie with it. Leaving it means every later
// refresh fails identically, and reuse detection keeps revoking a session that
// has already gone.
func TestARefusedRefreshClearsTheCookie(t *testing.T) {
	h := newHarness(t)
	email := h.seedUserWithRole(t, "owner")

	signIn := signInRaw(t, h, email, false)
	csrf := cookieNamed(signIn, csrfCookieName)
	signIn.Body.Close()

	req, _ := http.NewRequest(http.MethodPost,
		h.server.URL+"/api/v1/auth/refresh", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "not-a-real-token"})
	req.AddCookie(csrf)
	req.Header.Set(csrfHeaderName, csrf.Value)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refreshing: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatal("a made-up refresh token was accepted")
	}
	cleared := cookieNamed(resp, refreshCookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Error("a refused refresh left the dead cookie in place")
	}
}
