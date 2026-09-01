package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/billing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/group"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// Subscription, entitlements and billing (H5), and multi-company groups with
// consolidated reporting (F4).
//
// # A client reads their plan; only the platform writes it
//
// `subscription.view` reaches a tenant's own plan, entitlements and invoices —
// H5 puts invoicing to tenants in the product, and a bill nobody can read is a
// bill nobody pays. Every write is AccessSuperAdmin, because a tenant who could
// edit their own entitlements would be a tenant on the Enterprise plan.
//
// # A group statement is not a report
//
// `group.view` rather than `report.view`. Reading a consolidated statement is
// reading every company's books at once, and a store manager holding
// report.view for their own shop must not reach the group's profit through it.

// --- scopes ---------------------------------------------------------------

func (s *Server) billingScope(r *http.Request) billing.Scope {
	a := actor.From(r.Context())
	return billing.Scope{TenantID: a.TenantID, UserID: a.UserID}
}

func (s *Server) groupScope(r *http.Request) group.Scope {
	a := actor.From(r.Context())
	return group.Scope{TenantID: a.TenantID, UserID: a.UserID}
}

// --- what a client may see about their own subscription -------------------

func (s *Server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	out, err := s.billing.Subscription(r.Context(), s.billingScope(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"subscription": out})
}

func (s *Server) handleGetEntitlements(w http.ResponseWriter, r *http.Request) {
	out, err := s.billing.Entitlements(r.Context(), s.billingScope(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// handlePlans is the tier comparison.
//
// AccessAuthenticated with no permission: it is the same public price list for
// every client, and a cashier who sees "Wholesale is on the Business plan" has
// learned nothing about their employer.
func (s *Server) handlePlans(w http.ResponseWriter, r *http.Request) {
	out, err := s.billing.Plans(r.Context(), s.billingScope(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"plans": out})
}

func (s *Server) handleListSubscriptionInvoices(
	w http.ResponseWriter, r *http.Request,
) {
	out, err := s.billing.Invoices(r.Context(), s.billingScope(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- the platform's side --------------------------------------------------

type setPlanRequest struct {
	Tier        string `json:"tier"`
	Cycle       string `json:"cycle"`
	Price       string `json:"price"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	TrialEndsOn string `json:"trial_ends_on"`
	GraceDays   int    `json:"grace_days"`
	Note        string `json:"note"`
}

type setFeatureRequest struct {
	Feature   string `json:"feature"`
	Enabled   bool   `json:"enabled"`
	Reason    string `json:"reason"`
	ExpiresOn string `json:"expires_on"`
	// Clear drops the exception so the plan's own answer resumes. A field
	// rather than a DELETE route, because the screen is one row with three
	// states and a separate verb would make "back to the plan" feel like a
	// different kind of act than the other two.
	Clear bool `json:"clear"`
}

type issueInvoiceRequest struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Amount      string `json:"amount"`
	Note        string `json:"note"`
}

type settleInvoiceRequest struct {
	PaymentRef string `json:"payment_ref"`
	Void       bool   `json:"void"`
	Reason     string `json:"reason"`
}

func tenantParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "tenantID"))
	if err != nil {
		return uuid.Nil, errs.New(errs.CodeNotFound,
			"That client was not found.")
	}
	return id, nil
}

func (s *Server) handlePlatformSubscription(
	w http.ResponseWriter, r *http.Request,
) {
	tenantID, err := tenantParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.billing.SubscriptionOf(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	invoices, err := s.billing.InvoicesOf(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"subscription": out, "invoices": invoices,
	})
}

func (s *Server) handleSetPlan(w http.ResponseWriter, r *http.Request) {
	tenantID, err := tenantParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req setPlanRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a := actor.From(r.Context())
	out, err := s.billing.SetPlan(r.Context(), a.UserID, tenantID,
		billing.NewPlan{
			Tier: req.Tier, Cycle: req.Cycle, Price: req.Price,
			Currency: req.Currency, Status: req.Status,
			TrialEndsOn: req.TrialEndsOn, GraceDays: req.GraceDays,
			Note: req.Note,
		})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"subscription": out})
}

func (s *Server) handleSetFeature(w http.ResponseWriter, r *http.Request) {
	tenantID, err := tenantParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req setFeatureRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a := actor.From(r.Context())

	if req.Clear {
		if err := s.billing.ClearFeature(
			r.Context(), a.UserID, tenantID, req.Feature); err != nil {
			httpx.Error(w, r, err)
			return
		}
	} else if err := s.billing.SetFeature(r.Context(), a.UserID, tenantID,
		req.Feature, req.Enabled, req.Reason, req.ExpiresOn); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.billing.SubscriptionOf(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"subscription": out})
}

func (s *Server) handleIssueSubscriptionInvoice(
	w http.ResponseWriter, r *http.Request,
) {
	tenantID, err := tenantParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req issueInvoiceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a := actor.From(r.Context())
	out, err := s.billing.IssueInvoice(r.Context(), a.UserID, tenantID,
		req.PeriodStart, req.PeriodEnd, req.Amount, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"invoice": out})
}

func (s *Server) handleSettleSubscriptionInvoice(
	w http.ResponseWriter, r *http.Request,
) {
	invoiceID, uerr := uuid.Parse(chi.URLParam(r, "subInvoiceID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That invoice was not found."))
		return
	}
	var req settleInvoiceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a := actor.From(r.Context())

	var out billing.Invoice
	var err error
	if req.Void {
		out, err = s.billing.VoidInvoice(
			r.Context(), a.UserID, invoiceID, req.Reason)
	} else {
		out, err = s.billing.MarkPaid(
			r.Context(), a.UserID, invoiceID, req.PaymentRef)
	}
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"invoice": out})
}

// handleRunDunning suspends clients past their grace period.
//
// Reachable so a platform operator can run it deliberately after a billing
// run, rather than only on the schedule. It is idempotent — a tenant already
// suspended is not suspended again — so pressing it twice is safe.
func (s *Server) handleRunDunning(w http.ResponseWriter, r *http.Request) {
	moved, err := s.billing.Dun(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"suspended": moved})
}

// --- groups (F4) ----------------------------------------------------------

type saveGroupRequest struct {
	Name     string `json:"name"`
	NameAr   string `json:"name_ar"`
	Currency string `json:"presentation_currency"`
}

type memberRequest struct {
	CompanyID string `json:"company_id"`
	Ownership string `json:"ownership_pct"`
	IsParent  bool   `json:"is_parent"`
}

type markIntercompanyRequest struct {
	EntryID      string `json:"entry_id"`
	Counterparty string `json:"counterparty_id"`
	Kind         string `json:"kind"`
	Note         string `json:"note"`
	// Unmark takes the annotation off, for the same reason `Clear` exists on a
	// feature flag: one screen, one row, and undoing is not a different verb.
	Unmark bool `json:"unmark"`
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	out, err := s.groups.Groups(r.Context(), s.groupScope(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSaveGroup(w http.ResponseWriter, r *http.Request) {
	var req saveGroupRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	var id *uuid.UUID
	if raw := chi.URLParam(r, "groupID"); raw != "" {
		parsed, uerr := uuid.Parse(raw)
		if uerr != nil {
			httpx.Error(w, r, errs.New(errs.CodeNotFound,
				"That group was not found."))
			return
		}
		id = &parsed
	}
	out, err := s.groups.SaveGroup(r.Context(), s.groupScope(r), id,
		req.Name, req.NameAr, req.Currency)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"group": out})
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	id, uerr := uuid.Parse(chi.URLParam(r, "groupID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That group was not found."))
		return
	}
	out, err := s.groups.Group(r.Context(), s.groupScope(r), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"group": out})
}

func (s *Server) handleRemoveGroup(w http.ResponseWriter, r *http.Request) {
	id, uerr := uuid.Parse(chi.URLParam(r, "groupID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That group was not found."))
		return
	}
	if err := s.groups.RemoveGroup(
		r.Context(), s.groupScope(r), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSaveGroupMember(
	w http.ResponseWriter, r *http.Request,
) {
	groupID, uerr := uuid.Parse(chi.URLParam(r, "groupID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That group was not found."))
		return
	}
	var req memberRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	companyID, cerr := uuid.Parse(req.CompanyID)
	if cerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Name the company."))
		return
	}
	out, err := s.groups.AddMember(r.Context(), s.groupScope(r), groupID,
		companyID, req.Ownership, req.IsParent)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"group": out})
}

func (s *Server) handleRemoveGroupMember(
	w http.ResponseWriter, r *http.Request,
) {
	groupID, uerr := uuid.Parse(chi.URLParam(r, "groupID"))
	companyID, cerr := uuid.Parse(chi.URLParam(r, "memberID"))
	if uerr != nil || cerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That company is not in that group."))
		return
	}
	if err := s.groups.RemoveMember(
		r.Context(), s.groupScope(r), groupID, companyID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGroupStatement serves both consolidated statements.
//
// One route with a `statement` parameter rather than two, because they take the
// same window and the same group and a screen switching between them is one
// control. `pl` needs a start date; `bs` is cumulative and ignores one.
func (s *Server) handleGroupStatement(w http.ResponseWriter, r *http.Request) {
	groupID, uerr := uuid.Parse(chi.URLParam(r, "groupID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That group was not found."))
		return
	}

	q := r.URL.Query()
	to, err := parseDayOrToday(q.Get("to"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	if q.Get("statement") == "balance_sheet" {
		out, err := s.groups.BalanceSheet(
			r.Context(), s.groupScope(r), groupID, to)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"statement": out})
		return
	}

	from, err := parseDay(q.Get("from"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.groups.ProfitAndLoss(
		r.Context(), s.groupScope(r), groupID, from, to)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"statement": out})
}

func (s *Server) handleListIntercompany(
	w http.ResponseWriter, r *http.Request,
) {
	groupID, uerr := uuid.Parse(chi.URLParam(r, "groupID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That group was not found."))
		return
	}
	q := r.URL.Query()
	from, err := parseDay(q.Get("from"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	to, err := parseDayOrToday(q.Get("to"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.groups.Intercompanies(
		r.Context(), s.groupScope(r), groupID, from, to)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleMarkIntercompany(
	w http.ResponseWriter, r *http.Request,
) {
	var req markIntercompanyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	entryID, eerr := uuid.Parse(req.EntryID)
	if eerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Name the journal entry."))
		return
	}

	if req.Unmark {
		if err := s.groups.UnmarkIntercompany(
			r.Context(), s.groupScope(r), entryID); err != nil {
			httpx.Error(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	counterparty, cerr := uuid.Parse(req.Counterparty)
	if cerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Name the other company in the transaction."))
		return
	}
	if err := s.groups.MarkIntercompany(r.Context(), s.groupScope(r),
		entryID, counterparty, req.Kind, req.Note); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseDay reads a required YYYY-MM-DD.
func parseDay(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, errs.New(errs.CodeInvalidInput,
			"Say which period the statement covers.")
	}
	d, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, errs.New(errs.CodeInvalidInput,
			"That date is not a date.")
	}
	return d, nil
}

// parseDayOrToday reads an optional YYYY-MM-DD, defaulting to today.
func parseDayOrToday(raw string) (time.Time, error) {
	if raw == "" {
		return time.Now().UTC().Truncate(24 * time.Hour), nil
	}
	return parseDay(raw)
}
