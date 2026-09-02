package api

import (
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// The second factor (H1), the role builder (A6.2), and a person's own sessions
// (H1).
//
// # The MFA and session routes carry no permission
//
// They are about the CALLER themselves: every query is scoped to the user id in
// the token and no route here takes a parameter naming somebody else. A
// permission would suggest that turning on another person's second factor, or
// reading their session list, is a thing that can be granted — and there is no
// code path that would serve it.
//
// The role builder is the opposite and carries `identity.manage_roles`, which
// is the most powerful permission in the product: anybody holding it can put
// into a role anything they hold themselves.

// --- the second factor ----------------------------------------------------

type mfaCodeRequest struct {
	Code string `json:"code"`
}

func (s *Server) handleMFAStatus(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	out, err := s.auth.MFAStatus(r.Context(), a.UserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"mfa": out})
}

// handleBeginMFA generates a secret and returns the QR payload.
//
// Nothing is switched on here. See the note on BeginMFA: a one-step enrolment
// is how somebody locks themselves out of an account whose scan silently
// failed.
func (s *Server) handleBeginMFA(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	out, err := s.auth.BeginMFA(r.Context(), a.UserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"enrolment": out})
}

// handleCompleteMFA switches it on and returns the recovery codes.
//
// The codes are in this response and in no other. A route that could show them
// again would be a way for somebody holding a stolen session to mint a way back
// in after the password is changed.
func (s *Server) handleCompleteMFA(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	var req mfaCodeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	codes, err := s.auth.CompleteMFA(r.Context(), a.UserID, req.Code)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (s *Server) handleDisableMFA(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	var req mfaCodeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.auth.DisableMFA(r.Context(), a.UserID, req.Code); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRegenerateRecoveryCodes(
	w http.ResponseWriter, r *http.Request,
) {
	a := actor.From(r.Context())
	var req mfaCodeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	codes, err := s.auth.RegenerateRecoveryCodes(
		r.Context(), a.UserID, req.Code)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// --- the caller's own sessions --------------------------------------------

func (s *Server) handleMySessions(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	out, err := s.auth.MySessions(r.Context(), a.UserID, a.SessionID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleRevokeMySession(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	id, uerr := uuid.Parse(chi.URLParam(r, "sessionID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That session was not found."))
		return
	}
	if err := s.auth.RevokeMySession(r.Context(), a.UserID, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- the role builder -----------------------------------------------------

// knownPermissions is every permission the route registry enforces.
//
// Read from the registry rather than a list kept beside it, so a permission
// added with a route appears in the builder the same day. A second list would
// be a second thing to remember, and the one that gets forgotten is the one
// nobody can grant.
func (s *Server) knownPermissions() []string {
	seen := map[string]bool{}
	for _, rt := range s.Routes() {
		if rt.Access == AccessPermission && rt.Permission != "" {
			seen[rt.Permission] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (s *Server) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.auth.Permissions(r.Context(), scope, s.knownPermissions())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSaveRole(w http.ResponseWriter, r *http.Request) {
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req identity.CustomRole
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	// The id comes from the path on an edit, never from the body: a body that
	// could name a different role than the URL is a body that can be pointed
	// somewhere the caller did not intend.
	if raw := chi.URLParam(r, "roleID"); raw != "" {
		id, uerr := uuid.Parse(raw)
		if uerr != nil {
			httpx.Error(w, r, errs.New(errs.CodeNotFound,
				"That role was not found."))
			return
		}
		req.ID = id
	} else {
		req.ID = uuid.Nil
	}

	out, err := s.auth.SaveRole(r.Context(), scope, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"role": out})
}

func (s *Server) handleReadRole(w http.ResponseWriter, r *http.Request) {
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "roleID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That role was not found."))
		return
	}
	out, err := s.auth.Role(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"role": out})
}

func (s *Server) handleRemoveRole(w http.ResponseWriter, r *http.Request) {
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "roleID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That role was not found."))
		return
	}
	if err := s.auth.RemoveRole(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
