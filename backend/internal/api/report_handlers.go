package api

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
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
		// A branch manager asking for the branch next door is told it does not
		// exist, rather than that they may not look — probing ids should not
		// enumerate the estate.
		if err := identity.CheckStoreScope(r.Context(), storeID); err != nil {
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

// --- GET /api/v1/reports/vat-return -------------------------------------

func (s *Server) handleVATReturn(w http.ResponseWriter, r *http.Request) {
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

	out, err := s.vat.Prepare(r.Context(), scope.TenantID, scope.CompanyID, from, to)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- GET /api/v1/dashboard/overview -------------------------------------

// handleDashboardOverview serves the Owner Dashboard in one call.
//
// One request rather than nine. A dashboard that fires a request per widget
// renders nine times and reflows under the owner”s eyes, and — worse — takes
// its figures from nine different instants, so a screen can appear not to
// balance when nothing is wrong.
//
// Gated on accounting.view, the same permission the statements carry. The
// dashboard shows revenue, cost, margin and cash position, which is precisely
// what that permission exists to protect; a Cashier holding sales.create must
// not learn the shop”s margin from a tile.
func (s *Server) handleDashboardOverview(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Defaults to today. Explicit dates are what make the tiles drillable
	// backwards — an owner asking "what happened last Tuesday" uses the same
	// screen rather than a separate report.
	day := time.Now().UTC()
	if raw := r.URL.Query().Get("date"); raw != "" {
		parsed, e := time.Parse("2006-01-02", raw)
		if e != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"Dates look like 2026-08-16."))
			return
		}
		day = parsed
	}

	out, err := s.reports.OverviewFor(r.Context(), scope, day)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- GET /api/v1/companies ----------------------------------------------

// handleListCompanies names the legal entities this caller may work in.
//
// Authenticated rather than permission-gated, and that is deliberate: every
// signed-in user needs to know which companies they are in before they can ask
// for anything about one, so requiring a permission would make the app
// unusable for whoever lacked it. It discloses nothing new — row-level
// security scopes the query to the tenant, and CanAccessCompany narrows it
// further to the entities this user is actually assigned to.
func (s *Server) handleListCompanies(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())

	companies, err := s.reports.CompaniesFor(r.Context(), a.TenantID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Filtered here rather than in SQL because the scope lives on the token,
	// not in the database. An empty CompanyIDs means every company in the
	// tenant, which is the ordinary case for an owner.
	allowed := companies[:0]
	for _, c := range companies {
		if a.CanAccessCompany(c.ID) {
			allowed = append(allowed, c)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": allowed})
}

// --- Drill-through -------------------------------------------------------
//
// Blueprint A8 requires one-click drill-through on every dashboard widget. A
// KPI you cannot open is trivia: an owner who sees an unexpected number has one
// useful next question — which transactions made it — and a dashboard that
// cannot answer it sends them to a spreadsheet.
//
// Each is gated on the permission covering the RECORDS it shows rather than on
// the dashboard's own accounting.view. A role holding one and not the other is
// an ordinary arrangement, and the route is the only place that can enforce it.

// dayFromQuery reads the optional ?date, defaulting to today.
func dayFromQuery(r *http.Request) (time.Time, error) {
	raw := r.URL.Query().Get("date")
	if raw == "" {
		return time.Now().UTC(), nil
	}
	day, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, errs.New(errs.CodeInvalidInput,
			"Dates look like 2026-08-16.")
	}
	return day, nil
}

func (s *Server) handleSalesDetail(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	day, err := dayFromQuery(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	out, err := s.reports.SalesFor(r.Context(), scope, day, limit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleExpensesDetail(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	day, err := dayFromQuery(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Optional: absent means the whole day, present means the one account the
	// owner clicked in the summary.
	var accountID *uuid.UUID
	if raw := r.URL.Query().Get("account_id"); raw != "" {
		id, e := parseUUID(raw, "account_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		accountID = &id
	}

	out, err := s.reports.ExpensesFor(r.Context(), scope, day, accountID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleComplianceQueue(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	out, err := s.reports.ComplianceFor(r.Context(), scope, limit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleStockDetail(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "low"
	}

	out, err := s.reports.StockFor(r.Context(), scope, filter)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- saved and scheduled reports (D1), and Saudization (E6) ----------------
//
// A saved report is a saved SHAPE of a report the product already computes —
// which one, over what relative window, filtered to which branch. See the note
// in reports/saved.go for why it is not a free-form query builder.

func (s *Server) handleListSavedReports(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.reports.SavedReports(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSaveReport(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req reports.Saved
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a := actor.From(r.Context())
	out, err := s.reports.SaveReport(r.Context(), scope, a.UserID, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"report": out})
}

func (s *Server) handleRemoveSavedReport(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "savedID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That report was not found."))
		return
	}
	if err := s.reports.RemoveSavedReport(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkforce is E6's Saudization reading.
//
// A count and a ratio, never a Nitaqat band: the band depends on the
// establishment's activity and size bracket against a schedule the ministry
// publishes, and asserting one from a head count would be inventing a
// regulatory classification.
func (s *Server) handleWorkforce(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.reports.Workforce(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"workforce": out})
}

// handleExportReport sends a report as a CSV file.
//
// Behind `report.export`, a permission that has been seeded on the Owner and
// Accountant roles since the permissions were written and guarded no route
// until now: an owner who wanted the day's sales in a spreadsheet could read
// them on a screen and retype them.
//
// The figures are produced by the same service calls the screens use, so the
// file and the page cannot drift apart.
func (s *Server) handleExportReport(w http.ResponseWriter, r *http.Request) {
	scope, err := reportScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	kind := chi.URLParam(r, "kind")
	if _, ok := reports.ExportKinds[kind]; !ok {
		httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
			"There is no %q report to export.", kind))
		return
	}

	q := reports.ExportQuery{Filter: r.URL.Query().Get("filter")}
	if q.On, err = parseReportDate(
		r.URL.Query().Get("on"), "on", time.Now().UTC()); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if v := r.URL.Query().Get("from"); v != "" {
		if q.From, err = parseReportDate(v, "from", q.On); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if q.To, err = parseReportDate(v, "to", q.On); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	if q.Filter == "" {
		// The drill-downs name their filter differently on each screen; accept
		// what each one already sends rather than making them learn a new name.
		q.Filter = firstNonEmpty(r.URL.Query().Get("account_id"),
			r.URL.Query().Get("stock"))
	}

	// Headers before the body: once bytes are written the status is sent, and
	// an error after that cannot be turned into a JSON error response. So the
	// report is built into memory first and only then written out.
	var buf bytes.Buffer
	if err := s.reports.ExportCSV(r.Context(), scope, kind, q, &buf); err != nil {
		httpx.Error(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+reports.FilenameFor(kind, q)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
