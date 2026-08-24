package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Where a browser's refresh token lives.
//
// # Why a cookie rather than the response body
//
// It used to come back in the JSON body, and the browser client put it in
// localStorage. Anything running in the page could read it there -- an
// extension, a successful XSS, a compromised dependency -- and a refresh token
// is the durable half of a session: stealing one is stealing the account until
// somebody notices.
//
// httpOnly puts it somewhere script cannot reach at all. The page never sees
// it, so there is nothing for a script to exfiltrate, and no amount of care in
// the client is required to keep it that way.
//
// # Why the native app is different, and still correct
//
// The Tauri POS has no browser cookie jar and no other origin sharing it. Its
// storage is the application's own, on a till in a shop. It still receives the
// token in the body, and asks for it explicitly with a header, so the SAFE
// behaviour is the default: a client that says nothing gets the cookie and no
// readable token.
//
// # Why SameSite=Strict and a CSRF token both
//
// A cookie is attached by the browser automatically, which is what makes
// cookie-authenticated endpoints forgeable from another site. Strict stops the
// cookie being sent on any cross-site request at all, which closes it.
//
// The double-submit token is defence in depth for the case where Strict does
// not apply -- an old browser, or a future change that relaxes it to Lax for a
// legitimate reason. Belt and braces on the one endpoint that authenticates
// with a cookie is proportionate; adding it to routes that authenticate with a
// Bearer token from JavaScript memory would be cargo cult, because an attacker's
// page cannot read that token and so cannot forge those requests.

const (
	// refreshCookieName holds the durable half of the session.
	refreshCookieName = "rawsyst_refresh"

	// csrfCookieName is deliberately READABLE by script: the client has to
	// echo it back in a header, and that echo is the whole mechanism. It is
	// not a secret -- it proves the request came from a page on this origin,
	// which is exactly what a cross-site attacker cannot arrange.
	csrfCookieName = "rawsyst_csrf"

	// csrfHeaderName carries the echo.
	csrfHeaderName = "X-CSRF-Token"

	// clientKindHeader lets the native app ask for the token in the body.
	// Absent means browser, which is the safe default.
	clientKindHeader = "X-Client-Kind"
	clientKindNative = "native"

	// refreshCookiePath scopes the cookie to the only routes that read it, so
	// it is not attached to every API call the browser makes.
	refreshCookiePath = "/api/v1/auth"
)

// wantsTokenInBody reports whether the caller is the native application.
//
// Forging this header gains nothing: it only changes whether a caller's OWN
// response carries their OWN token, and a cross-origin page cannot read that
// response anyway.
func wantsTokenInBody(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get(clientKindHeader), clientKindNative)
}

// newCSRFToken mints a token for the double-submit check.
func newCSRFToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", errs.New(errs.CodeInternal, "A session could not be started.")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// setSessionCookies writes the refresh token and its CSRF partner.
//
// secure is false only in development, where the browser talks to the API over
// plain HTTP on localhost and would silently drop a Secure cookie -- which
// would look exactly like a broken sign-in.
func (s *Server) setSessionCookies(w http.ResponseWriter, refreshToken, csrf string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    refreshToken,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		// No Expires or MaxAge: a session cookie for the browser, so closing it
		// ends the session. The server-side refresh token has its own lifetime
		// and is what actually decides how long a session may live.
	})
	http.SetCookie(w, &http.Cookie{
		Name:  csrfCookieName,
		Value: csrf,
		// Path "/" and not the auth path, which is the mistake the first cut
		// of this made. document.cookie only exposes cookies whose path
		// matches the CURRENT page, and the app is served from "/". Scoped to
		// /api/v1/auth the token was invisible to the very page that has to
		// echo it, so every refresh came back 403 and the session ended at the
		// first expiry -- with the cookie sitting there, correctly set, doing
		// nothing.
		//
		// Widening it costs nothing: the token is not a secret. Being readable
		// by a page on THIS origin is the entire mechanism, and that is exactly
		// what a cross-site attacker cannot arrange.
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearSessionCookies removes both, on sign-out and on a refusal.
func (s *Server) clearSessionCookies(w http.ResponseWriter, secure bool) {
	// The path must match the one the cookie was set with, or the browser
	// treats this as a different cookie and leaves the original in place.
	for _, c := range []struct {
		name string
		path string
	}{
		{refreshCookieName, refreshCookiePath},
		{csrfCookieName, "/"},
	} {
		http.SetCookie(w, &http.Cookie{
			Name:     c.name,
			Value:    "",
			Path:     c.path,
			HttpOnly: c.name == refreshCookieName,
			Secure:   secure,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
	}
}

// refreshTokenFrom resolves the token for this request, and says whether it
// came from a cookie.
//
// The cookie wins when both are present. A body value cannot override it,
// which would otherwise let a page that HAS somehow obtained a token use it
// while the cookie sat there unused.
func refreshTokenFrom(r *http.Request, bodyToken string) (token string, fromCookie bool) {
	if c, err := r.Cookie(refreshCookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	return bodyToken, false
}

// checkCSRF enforces the double submit for a cookie-authenticated request.
//
// Compared in constant time. The comparison is not secret-dependent in a way
// that a timing attack would obviously help with, but a token comparison is
// exactly the place where that habit costs nothing and its absence gets
// flagged in every review.
func checkCSRF(r *http.Request) error {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return errs.New(errs.CodeUnauthenticated,
			"Your session could not be verified. Please sign in again.")
	}
	sent := r.Header.Get(csrfHeaderName)
	if sent == "" {
		return errs.New(errs.CodeForbidden,
			"This request is missing its verification token.")
	}
	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(sent)) != 1 {
		return errs.New(errs.CodeForbidden,
			"This request could not be verified.")
	}
	return nil
}
