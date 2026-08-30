package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/fiscal"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// Fiscal periods (C10) and the audit trail (D4).
//
// Together because they are two halves of the same guarantee. A closed period
// is what makes a set of statements trustworthy; the trail is what makes the
// closing itself checkable. Neither is worth much without the other — a lock
// nobody can audit, or an audit of nothing that was ever locked.

func (s *Server) fiscalScope(r *http.Request) (fiscal.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return fiscal.Scope{}, err
	}
	return fiscal.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

func (s *Server) handleFiscalCalendar(w http.ResponseWriter, r *http.Request) {
	scope, err := s.fiscalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.fiscal.Calendar(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleOpenFiscalYear(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FiscalYear int `json:"fiscal_year"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.FiscalYear == 0 {
		httpx.Error(w, r, errs.Validation("Say which year to open.").
			WithField("fiscal_year", "The calendar year the fiscal year starts in."))
		return
	}
	scope, err := s.fiscalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	made, err := s.fiscal.OpenYear(r.Context(), scope, req.FiscalYear)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// How many were actually created, not just that it worked. Opening a year
	// that already exists is not an error — two people can press it on the same
	// morning — but the screen should be able to tell the two apart.
	httpx.JSON(w, http.StatusOK, map[string]any{
		"fiscal_year": req.FiscalYear, "periods_created": made,
	})
}

func (s *Server) handleClosePeriod(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.fiscalScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.fiscal.Close(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleReopenPeriod(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, id, err := s.fiscalScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.fiscal.Reopen(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) fiscalScopeAndID(
	r *http.Request,
) (fiscal.Scope, uuid.UUID, error) {
	scope, err := s.fiscalScope(r)
	if err != nil {
		return fiscal.Scope{}, uuid.Nil, err
	}
	id, err := parseUUID(chi.URLParam(r, "periodID"), "periodID")
	if err != nil {
		return fiscal.Scope{}, uuid.Nil, err
	}
	return scope, id, nil
}

// --- the audit trail (D4) -------------------------------------------------

// The trail is tenant-wide, not company-scoped.
//
// `audit_log` carries no company dimension, deliberately: creating a company is
// an action that does not belong to one, and neither does signing in. So this
// reads everything the caller's tenant recorded, bounded by row-level security
// exactly as every other read is.
func (s *Server) handleAuditTrail(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	q := r.URL.Query()

	query := audit.Query{
		Action:     strings.TrimSpace(q.Get("action")),
		EntityType: strings.TrimSpace(q.Get("entity_type")),
		Limit:      atoiOr(q.Get("limit"), 0),
	}
	if v := strings.TrimSpace(q.Get("entity_id")); v != "" {
		id, err := parseUUID(v, "entity_id")
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		query.EntityID = &id
	}
	if v := strings.TrimSpace(q.Get("actor_id")); v != "" {
		id, err := parseUUID(v, "actor_id")
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		query.ActorID = &id
	}
	var err error
	if query.From, err = optionalDay(q.Get("from")); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if query.To, err = optionalDay(q.Get("to")); err != nil {
		httpx.Error(w, r, err)
		return
	}
	// `to` is inclusive to a reader — "up to and including the 31st" — and
	// exclusive in the query. Adding the day here rather than asking a person
	// to type the first of the next month.
	if query.To != nil {
		next := query.To.AddDate(0, 0, 1)
		query.To = &next
	}

	records, actions, err := s.audit.Trail(r.Context(), a.TenantID, query)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": records,
		// The verbs that actually appear in this tenant's trail, so the filter
		// offers what is there rather than a fixed list that goes stale every
		// time a module is added.
		"actions": actions,
	})
}

func optionalDay(v string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	day, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, errs.Newf(errs.CodeInvalidInput,
			"%q is not a date. Use the form 2026-08-30.", v)
	}
	return &day, nil
}
