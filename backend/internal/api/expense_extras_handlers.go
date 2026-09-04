// Departments and recurring expenses (blueprint C3.1).
//
// Both were on the list migration 0071 recorded as deliberately not built.
// Departments are the last field on C3.1's list of what an expense stores;
// recurring expenses are the schedule a shop agrees once and then owes monthly.
//
// Both sit behind the permissions expenses already use: `expense.view` to read,
// `expense.manage_heads` to configure, `expense.record` to generate — because
// generating is recording an expense, and a person who may not record one must
// not be able to make the schedule do it for them.
package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/expenses"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// --- departments ------------------------------------------------------------

type departmentRequest struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	NameAr string `json:"name_ar"`
}

func (s *Server) handleListDepartments(w http.ResponseWriter, r *http.Request) {
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.Departments(r.Context(), scope,
		r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleCreateDepartment(w http.ResponseWriter, r *http.Request) {
	var req departmentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.CreateDepartment(r.Context(), scope,
		expenses.NewDepartment{Code: req.Code, Name: req.Name,
			NameAr: req.NameAr})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateDepartment(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "departmentID"), "departmentID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req departmentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.UpdateDepartment(r.Context(), scope, id,
		expenses.NewDepartment{Name: req.Name, NameAr: req.NameAr})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleSetDepartmentActive retires a department or brings it back.
//
// There is no delete. A department that has been spent against is part of the
// history of every expense booked to it, and the foreign key refuses to remove
// one that is in use.
func (s *Server) handleSetDepartmentActive(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "departmentID"), "departmentID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req activeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.SetDepartmentActive(r.Context(), scope, id,
		req.Active)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- recurring expenses -----------------------------------------------------

type recurringRequest struct {
	Name         string `json:"name"`
	HeadID       string `json:"head_id"`
	StoreID      string `json:"store_id"`
	SupplierID   string `json:"supplier_id"`
	DepartmentID string `json:"department_id"`
	Amount       string `json:"amount"`
	PaidFrom     string `json:"paid_from"`
	Description  string `json:"description"`
	Frequency    string `json:"frequency"`
	Interval     int    `json:"interval_count"`
	StartsOn     string `json:"starts_on"`
	EndsOn       string `json:"ends_on"`
}

func (s *Server) handleListRecurringExpenses(w http.ResponseWriter, r *http.Request) {
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.RecurringList(r.Context(), scope,
		r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleCreateRecurringExpense(w http.ResponseWriter, r *http.Request) {
	var req recurringRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := expenses.NewRecurring{
		Name: req.Name, PaidFrom: req.PaidFrom, Description: req.Description,
		Frequency: req.Frequency, Interval: req.Interval,
	}
	if in.HeadID, err = parseUUID(req.HeadID, "head_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.Amount, err = decimal.NewFromString(req.Amount); err != nil {
		httpx.Error(w, r, errs.Validation("That amount is not a number.").
			WithField("amount", "The amount due each period."))
		return
	}
	for _, opt := range []struct {
		raw   string
		field string
		into  **uuid.UUID
	}{
		{req.StoreID, "store_id", &in.StoreID},
		{req.SupplierID, "supplier_id", &in.SupplierID},
		{req.DepartmentID, "department_id", &in.DepartmentID},
	} {
		if opt.raw == "" {
			continue
		}
		id, e := parseUUID(opt.raw, opt.field)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		*opt.into = &id
	}

	starts, err := optionalDate(req.StartsOn, "starts_on")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if starts == nil {
		now := time.Now().UTC()
		starts = &now
	}
	in.StartsOn = *starts
	if in.EndsOn, err = optionalDate(req.EndsOn, "ends_on"); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.expenses.CreateRecurring(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleSetRecurringActive(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "recurringID"), "recurringID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req activeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.SetRecurringActive(r.Context(), scope, id,
		req.Active)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleGenerateRecurringExpenses turns every schedule that has fallen due into
// an expense.
//
// Behind `expense.record` rather than a configuration permission: this writes
// expenses, and somebody who may not record one must not be able to make a
// schedule do it for them.
//
// Run on demand rather than only by a timer, so an owner who wants this month's
// rent booked now does not have to wait for the overnight job — and running it
// twice is safe, which is the property the unique run key exists for.
func (s *Server) handleGenerateRecurringExpenses(w http.ResponseWriter, r *http.Request) {
	scope, err := s.expenseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	upTo, err := parseReportDate(r.URL.Query().Get("up_to"), "up_to",
		time.Now().UTC())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.expenses.Generate(r.Context(), scope, upTo)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
