package api

import (
	"net/http"
	"sort"
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
	RefreshToken       string    `json:"refresh_token"`
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

	httpx.JSON(w, http.StatusOK, loginResponse{
		AccessToken:        session.AccessToken,
		RefreshToken:       session.RefreshToken,
		ExpiresAt:          session.ExpiresAt,
		MustChangePassword: session.MustChangePassword,
	})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.RefreshToken == "" {
		httpx.Error(w, r, errs.New(errs.CodeUnauthenticated,
			"Your session has expired. Please sign in again."))
		return
	}

	session, err := s.auth.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, loginResponse{
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		ExpiresAt:    session.ExpiresAt,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
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
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
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
