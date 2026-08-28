package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/expenses"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// Cash expenses, blueprint C3.
//
// A back-office surface, so the company is named in the query string and
// checked against the caller's confinement, exactly as purchasing and
// receivables do. There is deliberately no till-side equivalent: money taken
// out of a drawer for a small purchase is a CASH MOVEMENT against the open
// session — reason `petty_cash` — and recording it as an expense as well would
// take it out of the drawer twice.

func (s *Server) expenseScope(r *http.Request) (expenses.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return expenses.Scope{}, err
	}
	return expenses.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- expense heads --------------------------------------------------------

type expenseHeadRequest struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	NameAr string `json:"name_ar"`
	// AccountID is the expense account this category posts to. Required, and
	// checked against the company by a database trigger.
	AccountID string `json:"account_id"`
	// InputVATRecoverable is a POINTER so that omitting it is refused rather
	// than defaulting to false. Defaulting either way is wrong: false silently
	// stops a shop reclaiming VAT it is entitled to, true silently claims VAT
	// on entertainment. E2.3 makes this a decision, so the request has to carry
	// one.
	InputVATRecoverable *bool `json:"input_vat_recoverable"`
}

func (req expenseHeadRequest) into() (expenses.NewHead, error) {
	accountID, err := parseUUID(req.AccountID, "account_id")
	if err != nil {
		return expenses.NewHead{}, err
	}
	if req.InputVATRecoverable == nil {
		return expenses.NewHead{}, errs.Validation(
			"Say whether the VAT on this category can be reclaimed.").
			WithField("input_vat_recoverable",
				"Entertainment, some vehicles and fuel cannot be reclaimed.")
	}
	return expenses.NewHead{
		Code: req.Code, Name: req.Name, NameAr: req.NameAr,
		AccountID:           accountID,
		InputVATRecoverable: *req.InputVATRecoverable,
	}, nil
}

func (s *Server) handleListExpenseHeads(w http.ResponseWriter, r *http.Request) {
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.Heads(r.Context(), scope,
		r.URL.Query().Get("include_retired") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleCreateExpenseHead(w http.ResponseWriter, r *http.Request) {
	var req expenseHeadRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	in, err := req.into()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.CreateHead(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateExpenseHead(w http.ResponseWriter, r *http.Request) {
	var req expenseHeadRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "headID"), "headID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	in, err := req.into()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.UpdateHead(r.Context(), scope, id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleSetExpenseHeadActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool `json:"active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "headID"), "headID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.expenses.SetHeadActive(r.Context(), scope, id, req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Server) handleListExpenseAccounts(w http.ResponseWriter, r *http.Request) {
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.Accounts(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- expenses -------------------------------------------------------------

type expenseRequest struct {
	UUID        string `json:"uuid"`
	Date        string `json:"expense_date"`
	StoreID     string `json:"store_id"`
	SupplierID  string `json:"supplier_id"`
	Reference   string `json:"reference"`
	Description string `json:"description"`
	PaidFrom    string `json:"paid_from"`

	Lines []struct {
		HeadID      string `json:"head_id"`
		Description string `json:"description"`
		// Net, never gross. The server computes the tax from the registry rate
		// for the expense date, so a client cannot decide what the VAT return
		// claims.
		Net          string `json:"net_amount"`
		TaxTreatment string `json:"tax_treatment"`
	} `json:"lines"`
}

func (s *Server) handleRecordExpense(w http.ResponseWriter, r *http.Request) {
	var req expenseRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	docUUID, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	when, err := parseReportDate2(req.Date, "expense_date")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := expenses.NewExpense{
		UUID: docUUID, Date: when,
		Reference: req.Reference, Description: req.Description,
		PaidFrom: strings.TrimSpace(req.PaidFrom),
	}
	if req.StoreID != "" {
		id, e := parseUUID(req.StoreID, "store_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.StoreID = &id
	}
	if req.SupplierID != "" {
		id, e := parseUUID(req.SupplierID, "supplier_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.SupplierID = &id
	}

	for i, l := range req.Lines {
		headID, e := parseUUID(l.HeadID, "head_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		net, e := parseAmount(l.Net, "net_amount", i)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.Lines = append(in.Lines, expenses.NewLine{
			HeadID: headID, Description: l.Description,
			Net: net, TaxTreatment: l.TaxTreatment,
		})
	}

	out, err := s.expenses.Record(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// 200 rather than 201 on a replay: nothing was created, and a client that
	// counts 201s to know what it wrote should count this one as nothing.
	status := http.StatusCreated
	if out.AlreadyRecorded {
		status = http.StatusOK
	}
	httpx.JSON(w, status, out)
}

func (s *Server) handleListExpenses(w http.ResponseWriter, r *http.Request) {
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	q := r.URL.Query()
	// Defaulting to the current month rather than to everything. "Where is my
	// money going" is a question about a period, and a shop three years in
	// would otherwise wait for its whole history to answer it.
	now := time.Now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, -1)

	if raw := q.Get("from"); raw != "" {
		d, e := parseReportDate2(raw, "from")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		from = d
	}
	if raw := q.Get("to"); raw != "" {
		d, e := parseReportDate2(raw, "to")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		to = d
	}

	f := expenses.Filter{From: from, To: to}
	if raw := q.Get("head_id"); raw != "" {
		id, e := parseUUID(raw, "head_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		f.HeadID = &id
	}
	if raw := q.Get("store_id"); raw != "" {
		id, e := parseUUID(raw, "store_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		f.StoreID = &id
	}

	out, err := s.expenses.Between(r.Context(), scope, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleReadExpense(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "expenseID"), "expenseID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.Read(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

var _ = uuid.Nil

// parseReportDate2 reads a required date.
//
// parseReportDate takes a fallback because a report without dates means "this
// period". An expense without a date means nothing at all — it decides which
// fiscal period the money left, and guessing today would put a receipt keyed in
// January into whatever month somebody happened to enter it.
func parseReportDate2(raw, field string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, errs.Validation("Say the date.").
			WithField(field, "A date like 2026-08-15 is required.")
	}
	d, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, errs.Newf(errs.CodeInvalidInput,
			"%s must be a date like 2026-08-15.", field)
	}
	return d, nil
}
