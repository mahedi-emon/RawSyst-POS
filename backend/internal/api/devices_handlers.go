package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/devices"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// The terminal-management surface, blueprint H3.
//
// One route here is deliberately unauthenticated, and it is worth being explicit
// about why: a terminal being paired has no credential yet, which is the entire
// problem being solved. Everything that would normally come from a token —
// tenant, company, store, which terminal — is derived from the enrolment code
// instead, and the code is single-use, expiring and attempt-limited to make
// that safe. Nothing else in this file is reachable without a signed-in
// administrator.

// DeviceSecretHeader is how a paired terminal identifies itself when signing a
// cashier in.
//
// A header rather than a body field, so it never lands in a request log that
// captures bodies, and never in a URL that lands in one that captures those.
const DeviceSecretHeader = "X-Device-Secret" //nolint:gosec // a header name

func (s *Server) deviceScope(r *http.Request) (devices.Scope, error) {
	a := actor.From(r.Context())

	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return devices.Scope{}, err
	}
	return devices.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- Registering and listing ----------------------------------------------

type terminalRequest struct {
	StoreID string `json:"store_id"`
	Label   string `json:"terminal_label"`
	// Which EGS unit signs for this terminal. Required when registering,
	// optional when amending — where it is also the only way to give a
	// terminal registered before 0043 the unit it needs to sell.
	EGSUnitID string `json:"egs_unit_id"`
}

func (s *Server) handleRegisterTerminal(w http.ResponseWriter, r *http.Request) {
	var req terminalRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.deviceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	storeID, err := parseUUID(req.StoreID, "store_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// Optional at the boundary so an omitted unit is refused by the service
	// with a sentence about e-invoicing, rather than here with a parse error
	// naming a field the caller has never heard of.
	egsUnitID, err := parseOptionalUUID(req.EGSUnitID, "egs_unit_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.devices.Register(r.Context(), scope,
		devices.NewTerminal{StoreID: storeID, Label: req.Label, EGSUnitID: egsUnitID})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListTerminals(w http.ResponseWriter, r *http.Request) {
	scope, err := s.deviceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.devices.List(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadTerminal(w http.ResponseWriter, r *http.Request) {
	deviceID, err := parseUUID(chi.URLParam(r, "deviceID"), "deviceID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.deviceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.devices.Read(r.Context(), scope, deviceID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleAmendTerminal(w http.ResponseWriter, r *http.Request) {
	deviceID, err := parseUUID(chi.URLParam(r, "deviceID"), "deviceID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req terminalRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.deviceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// Optional: renaming without moving is the common case.
	storeID, err := parseOptionalUUID(req.StoreID, "store_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	egsUnitID, err := parseOptionalUUID(req.EGSUnitID, "egs_unit_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.devices.Amend(r.Context(), scope, deviceID,
		devices.Amendment{Label: req.Label, StoreID: storeID, EGSUnitID: egsUnitID})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleSetTerminalActive(w http.ResponseWriter, r *http.Request) {
	deviceID, err := parseUUID(chi.URLParam(r, "deviceID"), "deviceID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req activeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.deviceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.devices.SetActive(r.Context(), scope, deviceID, req.Active)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type revokeRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleRevokeTerminal(w http.ResponseWriter, r *http.Request) {
	deviceID, err := parseUUID(chi.URLParam(r, "deviceID"), "deviceID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req revokeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.deviceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.devices.Revoke(r.Context(), scope, deviceID, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- The enrolment code ---------------------------------------------------

// handleIssueEnrolmentCode returns the code in the response, once.
//
// It is never readable again: only its hash is stored, exactly as a password
// is. A caller who loses it issues another, which supersedes the first.
func (s *Server) handleIssueEnrolmentCode(w http.ResponseWriter, r *http.Request) {
	deviceID, err := parseUUID(chi.URLParam(r, "deviceID"), "deviceID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.deviceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.devices.IssueCode(r.Context(), scope, deviceID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// --- Claiming a terminal ---------------------------------------------------

type enrolRequest struct {
	Code       string `json:"code"`
	OS         string `json:"os"`
	AppVersion string `json:"app_version"`
}

// handleEnrol is the one unauthenticated route in this file. See the file
// comment for why, and internal/devices for what makes it safe.
func (s *Server) handleEnrol(w http.ResponseWriter, r *http.Request) {
	var req enrolRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.devices.Enrol(r.Context(), devices.Claim{
		Code: req.Code, OS: req.OS, AppVersion: req.AppVersion,
		IP: clientIP(r),
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// handleTerminalIdentity tells a paired terminal who it is.
//
// The call a till makes on startup, before anybody has signed in: it proves the
// secret still works and returns the label, store and company to show on the
// screen. It is also how a revoked terminal finds out — the refusal names the
// state, so the till can say "this terminal was revoked" rather than sitting on
// a spinner.
func (s *Server) handleTerminalIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := s.devices.Authenticate(r.Context(), r.Header.Get(DeviceSecretHeader))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	s.devices.Touch(r.Context(), id)

	httpx.JSON(w, http.StatusOK, map[string]any{
		"device_id":      id.DeviceID,
		"terminal_label": id.Label,
		"store_id":       id.StoreID,
		"company_id":     id.CompanyID,
	})
}

func (s *Server) handleListDeviceStores(w http.ResponseWriter, r *http.Request) {
	scope, err := s.deviceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.devices.StoresFor(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}
