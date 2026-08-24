package api

import (
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.Error(w, r, errs.New(errs.CodeNotFound, "Not found."))
}

// --- health ------------------------------------------------------------

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	// Deliberately checks nothing. Liveness answers "is this process running";
	// tying it to the database would make a brief database blip restart every
	// API replica, turning a recoverable incident into an outage.
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.health(); err != nil {
		httpx.Error(w, r, errs.Wrap(err, errs.CodeUnavailable,
			"The service is not ready to take traffic."))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]string{"version": s.version})
}

// --- authentication ----------------------------------------------------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Device   string `json:"device,omitempty"`

	// TenantID answers the "which business?" challenge below. Optional, and
	// only ever a filter on the account lookup — naming a business the caller
	// has no account in is refused exactly like a wrong password.
	TenantID string `json:"tenant_id,omitempty"`
}

type loginResponse struct {
	AccessToken        string    `json:"access_token"`
	RefreshToken       string    `json:"refresh_token,omitempty"`
	ExpiresAt          time.Time `json:"expires_at"`
	MustChangePassword bool      `json:"must_change_password"`

	// TenantChoiceRequired means the email and password opened accounts in more
	// than one business and the caller has to say which. No tokens are issued.
	//
	// A 200 rather than an error status, because nothing went wrong: this is a
	// challenge, the same shape as an MFA step. A client that ignores the flag
	// finds no access token and cannot proceed regardless.
	TenantChoiceRequired bool                    `json:"tenant_choice_required,omitempty"`
	Tenants              []identity.TenantChoice `json:"tenants,omitempty"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Email == "" || req.Password == "" {
		httpx.Error(w, r, errs.Validation("Enter your email and password.").
			WithField("email", "Required.").
			WithField("password", "Required."))
		return
	}

	creds := identity.Credentials{
		Email:     req.Email,
		Password:  req.Password,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
		Device:    req.Device,
	}

	// Signing in ON a paired terminal binds the session to it, so every sale
	// records which till rang it up and which ZATCA chain it belongs to.
	//
	// Resolved from the terminal's own secret, never from anything the request
	// body says — otherwise a cashier could name a till they are not standing
	// at, and the chain would be wrong in a way nothing downstream could catch.
	// A bad secret refuses the sign-in outright rather than quietly falling back
	// to an unbound session: a till that has lost its pairing must say so, not
	// carry on looking normal until the first sale fails.
	if secret := r.Header.Get(DeviceSecretHeader); secret != "" {
		id, e := s.devices.Authenticate(r.Context(), secret)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		creds.DeviceID = id.DeviceID
	}
	if req.TenantID != "" {
		chosen, e := parseUUID(req.TenantID, "tenant_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		creds.TenantID = &chosen
	}

	session, err := s.auth.Login(r.Context(), creds)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The challenge. Returned before the token fields so a reader of the
	// payload sees immediately that this is not a session.
	if len(session.Choices) > 0 {
		httpx.JSON(w, http.StatusOK, loginResponse{
			TenantChoiceRequired: true,
			Tenants:              session.Choices,
		})
		return
	}

	csrf, err := newCSRFToken()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	s.setSessionCookies(w, session.RefreshToken, csrf, s.secureCookies)

	// The browser is told nothing it could store. Only the native app, which
	// asks explicitly, gets the refresh token where script can read it -- so a
	// client that says nothing gets the safe behaviour.
	resp := loginResponse{
		AccessToken:        session.AccessToken,
		ExpiresAt:          session.ExpiresAt,
		MustChangePassword: session.MustChangePassword,
	}
	if wantsTokenInBody(r) {
		resp.RefreshToken = session.RefreshToken
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	// The body is optional now: a browser sends nothing and the cookie carries
	// the token. Decode failures are tolerated for that reason.
	var req refreshRequest
	_ = httpx.Decode(r, &req)

	token, fromCookie := refreshTokenFrom(r, req.RefreshToken)
	if token == "" {
		httpx.Error(w, r, errs.New(errs.CodeUnauthenticated,
			"Your session has expired. Please sign in again."))
		return
	}

	// A cookie is attached by the browser whether or not the page meant to
	// send it, so a cookie-authenticated request has to prove it came from a
	// page on this origin. A body-authenticated one carries a token the caller
	// had to already hold, which is proof enough on its own.
	if fromCookie {
		if err := checkCSRF(r); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}

	session, err := s.auth.Refresh(r.Context(), token)
	if err != nil {
		// Clear the cookies on refusal. Leaving a dead token in the browser
		// means every later refresh fails the same way, and reuse detection
		// would keep revoking a session that is already gone.
		if fromCookie {
			s.clearSessionCookies(w, s.secureCookies)
		}
		httpx.Error(w, r, err)
		return
	}

	resp := loginResponse{
		AccessToken: session.AccessToken,
		ExpiresAt:   session.ExpiresAt,
	}

	if fromCookie {
		// Rotated, so the new token replaces the old one in the same response
		// that spends it.
		csrf, e := newCSRFToken()
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		s.setSessionCookies(w, session.RefreshToken, csrf, s.secureCookies)
	} else {
		resp.RefreshToken = session.RefreshToken
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())

	// Cleared before the revoke is attempted. If the revoke fails the browser
	// must still lose its token -- a sign-out that leaves a working session
	// behind on a shared till is worse than one that reports an error.
	s.clearSessionCookies(w, s.secureCookies)

	if err := s.auth.Logout(r.Context(), a.SessionID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

type meResponse struct {
	UserID       uuid.UUID   `json:"user_id"`
	TenantID     *uuid.UUID  `json:"tenant_id,omitempty"`
	IsSuperAdmin bool        `json:"is_super_admin"`
	Permissions  []string    `json:"permissions"`
	StoreScope   []uuid.UUID `json:"store_scope,omitempty"`
	AmountLimit  *string     `json:"amount_limit,omitempty"`
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	g := identity.GrantsFrom(r.Context())

	resp := meResponse{
		UserID:       a.UserID,
		IsSuperAdmin: a.IsSuperAdmin,
		// Sorted so the client can diff two responses meaningfully and so the
		// payload is byte-stable for caching.
		Permissions: sortedPermissions(g),
	}
	if a.TenantID != uuid.Nil {
		t := a.TenantID
		resp.TenantID = &t
	}
	// The branches this user is confined to. Absent when they are confined to
	// none, which is the ordinary case and means every branch — an empty list
	// would read as the opposite.
	resp.StoreScope = g.StoreIDs()

	if limit := g.AmountLimit(); limit != nil {
		// A decimal string, never a JSON number: the client must not widen a
		// money value through a float on its way to a comparison.
		v := limit.StringFixed(2)
		resp.AmountLimit = &v
	}

	httpx.JSON(w, http.StatusOK, resp)
}

func sortedPermissions(g *identity.Grants) []string {
	perms := g.Permissions()
	if perms == nil {
		perms = []string{}
	}
	sort.Strings(perms)
	return perms
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	if err := s.auth.ChangePassword(r.Context(), a.UserID,
		req.CurrentPassword, req.NewPassword); err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Every session including this one is now revoked, so the client must sign
	// in again. Saying so is the difference between a deliberate security step
	// and an apparent bug.
	httpx.JSON(w, http.StatusOK, map[string]string{
		"status": "password_changed",
		"detail": "Your password has been changed. Please sign in again on all your devices.",
	})
}

// --- platform control plane --------------------------------------------

type resetPasswordRequest struct {
	// Reason records how the administrator verified the person's identity
	// before resetting. Blueprint A4.2 makes that verification the whole point
	// of the flow, so it is required and permanently audit-logged.
	Reason string `json:"reason"`
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput, "That is not a valid user id."))
		return
	}

	var req resetPasswordRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	res, err := s.auth.ResetPasswordAsSuperAdmin(r.Context(), targetID, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]string{
		"temporary_password": res.TemporaryPassword,
		"detail": "Give this to the account owner through a channel you have already " +
			"verified. They must change it when they sign in. It is shown once and " +
			"is not stored anywhere in readable form.",
	})
}

// clientIP resolves the caller's address.
//
// X-Forwarded-For is honoured only because this service runs behind Nginx,
// which overwrites it. Trusting it from a directly-reachable server would let a
// caller forge the address recorded in the audit log — and blueprint D4 makes
// "where" one of the six fields the log exists to preserve.
// The address is stored in a PostgreSQL `inet` column, which is strict about
// what it accepts. An IPv6 RemoteAddr looks like "[::1]:54321", and taking
// everything before the last colon yields "[::1]" -- brackets included, which
// inet rejects outright. That turned every sign-in from an IPv6 client into a
// 500: the audit insert failed and took the login with it.
//
// It hid because a developer machine reached the API on 127.0.0.1 and CI never
// exercised the path over IPv6. It would have hit production on the first
// client with an IPv6 address, which today is most of them.
//
// net.SplitHostPort understands the bracket form and returns "::1".
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	// No port at all, which is unusual but not worth failing a sign-in over.
	// Brackets are stripped so an address written "[::1]" still stores.
	return strings.Trim(r.RemoteAddr, "[]")
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// --- GET /api/v1/meta/ping ----------------------------------------------

// handlePing answers a terminal asking whether it can reach us.
//
// Authenticated on purpose, and 204 with no body on purpose.
//
// A reachability check that only opened a socket would report "online" while
// holding an expired token, and the till would then discover the truth by
// failing to sync a day of takings. Verifying the session IS the useful part:
// what a POS needs to know is not "is there a network" but "can I sync right
// now", and those differ exactly when it matters.
//
// No body, no business data, no work beyond the auth the middleware already
// did. A terminal polling this every half-minute costs less than one product
// search.
func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	httpx.NoContent(w)
}
