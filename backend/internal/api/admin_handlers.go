package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/integration"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/ops"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/portability"
)

// Integration (H6), migration and export (H7), backups (H4) and support (H10).
//
// # The key is in the response body exactly once
//
// `POST /api/v1/api-keys` is the only place a key is ever readable, and it is
// not readable again from any route. That is not an inconvenience to route
// around later: a product that can show a key twice is a product where the key
// is recoverable from the database, which is the breach.
//
// # An import is three requests, not one
//
// Upload stages, validate checks, commit writes. A single "import this file"
// route would be simpler and would remove H7's preview step, which is the whole
// reason a shop can see what will happen before their master data moves.
//
// # An export is a download, not JSON
//
// It streams CSV with a filename, because what a person does next with it is
// open it in Excel. Wrapping rows in a JSON envelope would mean the browser
// downloading something no spreadsheet opens.

func (s *Server) integrationScope(r *http.Request) (integration.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return integration.Scope{}, err
	}
	return integration.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

func (s *Server) portabilityScope(r *http.Request) (portability.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return portability.Scope{}, err
	}
	return portability.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

func (s *Server) opsScope(r *http.Request) (ops.Scope, error) {
	a := actor.From(r.Context())
	// A backup is a tenant-level fact and a support ticket may be raised before
	// a company has been chosen — an owner whose first problem is that
	// onboarding will not finish still has to be able to ask for help. So a
	// caller who has not named a company gets a scope without one, and both
	// modules treat the company as optional. Everything else in this file
	// refuses.
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		companyID = uuid.Nil
	}
	return ops.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- webhooks -------------------------------------------------------------

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	scope, err := s.integrationScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.integrations.Endpoints(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// The event vocabulary travels with the list, so a screen offering
	// checkboxes never offers an event the server would refuse.
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": out, "events": integration.Events,
	})
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string   `json:"name"`
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.integrationScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.integrations.SaveEndpoint(r.Context(), scope, req.Name,
		req.URL, req.Events)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleSetWebhookActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool `json:"is_active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.integrationScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "endpointID"), "endpointID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.integrations.SetEndpointActive(r.Context(), scope, id,
		req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	scope, err := s.integrationScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "endpointID"), "endpointID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.integrations.Deliveries(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- API keys -------------------------------------------------------------

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	scope, err := s.integrationScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.integrations.Keys(r.Context(), scope,
		r.URL.Query().Get("include_revoked") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// What the caller may put on a key, which is what they hold themselves.
	// Sent with the list so the form cannot offer an escalation and then be
	// told no.
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": out, "grantable": grantable(r),
	})
}

func (s *Server) handleMintAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
		ExpiresOn   string   `json:"expires_on"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	var expires *time.Time
	if v := strings.TrimSpace(req.ExpiresOn); v != "" {
		day, e := parseReportDate(v, "expires_on", time.Time{})
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		expires = &day
	}

	scope, err := s.integrationScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The intersection happens in the service; what the handler supplies is
	// the caller's own grant set, read from the token rather than from a
	// request field nobody could trust.
	held := map[string]bool{}
	for _, p := range grantable(r) {
		held[p] = true
	}

	out, err := s.integrations.Mint(r.Context(), scope, req.Name,
		req.Permissions, held, expires)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	scope, err := s.integrationScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "keyID"), "keyID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.integrations.Revoke(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// grantable is every permission the caller holds.
//
// A key may carry a subset of this and nothing else, so a person cannot mint a
// machine credential that outranks them.
func grantable(r *http.Request) []string {
	return identity.GrantsFrom(r.Context()).All()
}

// --- import ---------------------------------------------------------------

func (s *Server) handleImportShapes(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": portability.Kinds, "exports": portability.Exports,
	})
}

func (s *Server) handleListImports(w http.ResponseWriter, r *http.Request) {
	scope, err := s.portabilityScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portability.Batches(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// handleUploadImport stages a file.
//
// The CSV arrives as text in a JSON field rather than as multipart. A migration
// file is text, the mapping has to arrive with it, and one JSON body keeps the
// two together — a multipart upload would let a file be staged with no mapping
// and leave a batch nobody can validate.
func (s *Server) handleUploadImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind     string            `json:"kind"`
		Filename string            `json:"filename"`
		Mapping  map[string]string `json:"mapping"`
		CSV      string            `json:"csv"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if strings.TrimSpace(req.CSV) == "" {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"That upload carried no file."))
		return
	}

	scope, err := s.portabilityScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portability.Upload(r.Context(), scope, req.Kind,
		req.Filename, req.Mapping, strings.NewReader(req.CSV))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetImport(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.importScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portability.Batch(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleValidateImport(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.importScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portability.Validate(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCommitImport(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.importScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.portability.Commit(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCancelImport(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.importScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.portability.Cancel(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) importScopeAndID(
	r *http.Request,
) (portability.Scope, uuid.UUID, error) {
	scope, err := s.portabilityScope(r)
	if err != nil {
		return portability.Scope{}, uuid.UUID{}, err
	}
	id, err := parseUUID(chi.URLParam(r, "importID"), "importID")
	if err != nil {
		return portability.Scope{}, uuid.UUID{}, err
	}
	return scope, id, nil
}

// --- export ---------------------------------------------------------------

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	scope, err := s.portabilityScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	kind := chi.URLParam(r, "kind")
	var filename string
	for _, e := range portability.Exports {
		if strings.EqualFold(e.Kind, kind) {
			filename = e.File
		}
	}
	if filename == "" {
		httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
			"There is nothing called %q to export.", kind))
		return
	}

	// The headers go out before the first row, because the body is streamed
	// and an error discovered halfway cannot un-send them. The kind was
	// checked above, which is what makes that safe.
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	if err := s.portability.WriteExport(r.Context(), scope, kind, w); err != nil {
		// Nothing useful can be sent now: the status line has gone. Logged by
		// the middleware; the download simply ends short, which is what every
		// interrupted download looks like.
		return
	}
}

// --- backups --------------------------------------------------------------

func (s *Server) handleBackupHealth(w http.ResponseWriter, r *http.Request) {
	scope, err := s.opsScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.BackupHealth(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	scope, err := s.opsScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.Backups(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleStartBackup(w http.ResponseWriter, r *http.Request) {
	scope, err := s.opsScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.RecordBackup(r.Context(), scope, "manual")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleFinishBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Location string `json:"location"`
		Checksum string `json:"checksum"`
		Size     *int64 `json:"size_bytes"`
		Error    string `json:"error"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, id, err := s.backupScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.FinishBackup(r.Context(), scope, id, req.Location,
		req.Checksum, req.Size, req.Error)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleVerifyBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Error string `json:"error"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, id, err := s.backupScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.VerifyBackup(r.Context(), scope, id, req.Error)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) backupScopeAndID(
	r *http.Request,
) (ops.Scope, uuid.UUID, error) {
	scope, err := s.opsScope(r)
	if err != nil {
		return ops.Scope{}, uuid.UUID{}, err
	}
	id, err := parseUUID(chi.URLParam(r, "backupID"), "backupID")
	if err != nil {
		return ops.Scope{}, uuid.UUID{}, err
	}
	return scope, id, nil
}

// --- support --------------------------------------------------------------

func (s *Server) handleListTickets(w http.ResponseWriter, r *http.Request) {
	scope, err := s.opsScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.Tickets(r.Context(), scope,
		r.URL.Query().Get("include_closed") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleRaiseTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subject  string `json:"subject"`
		Body     string `json:"body"`
		Kind     string `json:"kind"`
		Priority string `json:"priority"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.opsScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.Raise(r.Context(), scope, req.Subject, req.Body,
		req.Kind, req.Priority)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.ticketScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.Ticket(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleReplyToTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Body string `json:"body"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, id, err := s.ticketScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.Reply(r.Context(), scope, id, req.Body)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCloseTicket(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.ticketScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.ops.Close(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) ticketScopeAndID(
	r *http.Request,
) (ops.Scope, uuid.UUID, error) {
	scope, err := s.opsScope(r)
	if err != nil {
		return ops.Scope{}, uuid.UUID{}, err
	}
	id, err := parseUUID(chi.URLParam(r, "ticketID"), "ticketID")
	if err != nil {
		return ops.Scope{}, uuid.UUID{}, err
	}
	return scope, id, nil
}
