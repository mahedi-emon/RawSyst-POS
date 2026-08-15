package api

import (
	"net/http"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
)

// The reporting surface.
//
// Every one of these reads the journal and writes nothing. They are gated on
// accounting.view rather than sales.view because a statement exposes the whole
// company's position — margin, cash, what is owed — which is exactly the
// information a cashier is deliberately kept away from.

// reportScope reads the company and optional branch from the query string.
//
// The company is required rather than inferred. A tenant may run several legal
// entities with separate books and separate VAT registrations (blueprint F4),
// so "the company" is not a thing the server can guess — and guessing would
// silently produce a statement for the wrong entity, which is worse than
// asking.
func reportScope(r *http.Request) (reports.Scope, error) {
	a := actor.From(r.Context())

	companyID, err := parseUUID(r.URL.Query().Get("company_id"), "company_id")
	if err != nil {
		return reports.Scope{}, errs.New(errs.CodeInvalidInput,
			"Say which company this statement is for. A tenant may keep separate "+
				"books for several legal entities.")
	}
	if !a.CanAccessCompany(companyID) {
		// Reported as absent rather than forbidden, for the same reason a
		// cross-tenant read is: confirming the company exists leaks something.
		return reports.Scope{}, errs.New(errs.CodeNotFound, "That company was not found.")
	}

	scope := reports.Scope{TenantID: a.TenantID, CompanyID: companyID}

	if raw := r.URL.Query().Get("store_id"); raw != "" {
		storeID, err := parseUUID(raw, "store_id")
		if err != nil {
			return reports.Scope{}, err
		}
		scope.StoreID = &storeID
	}
	return scope, nil
}

// parseReportDate reads a date, defaulting to today when omitted.
func parseReportDate(raw, field string, fallback time.Time) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	d, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, errs.Newf(errs.CodeInvalidInput,
			"%s must be a date like 2026-08-15.", field)
	}
	return d, nil
}

// reportPeriod reads from/to, defaulting to the current calendar month.
//
// A default of "this month" rather than "all time": an accountant asking for a
// P&L almost always means the current period, and a cumulative total presented
// as a monthly one is the kind of mistake that reaches a tax return.
func reportPeriod(r *http.Request) (from, to time.Time, err error) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	if from, err = parseReportDate(r.URL.Query().Get("from"), "from", monthStart); err != nil {
		return from, to, err
	}
	if to, err = parseReportDate(r.URL.Query().Get("to"), "to", now); err != nil {
		return from, to, err
	}
	return from, to, nil
}

func (s *Server) handleTrialBalance(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	asOf, err := parseReportDate(r.URL.Query().Get("as_of"), "as_of", time.Now().UTC())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.reports.TrialBalanceAt(r.Context(), scope, asOf)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleProfitAndLoss(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, to, err := reportPeriod(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.reports.ProfitAndLossFor(r.Context(), scope, from, to)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleBalanceSheet(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	asOf, err := parseReportDate(r.URL.Query().Get("as_of"), "as_of", time.Now().UTC())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.reports.BalanceSheetAt(r.Context(), scope, asOf)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCashFlow(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, to, err := reportPeriod(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.reports.CashFlowFor(r.Context(), scope, from, to)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
