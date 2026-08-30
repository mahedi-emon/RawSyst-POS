package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/people"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// Employees, attendance, leave, advances and payroll (C5, C6, E6).
//
// Every route here resolves the company from the query string like the rest of
// the back office, and additionally carries A6.2's pay masking into the
// service: `MaySeePay` is decided ONCE here from the caller's grants, so a
// query cannot be written that forgets it.

func hrScope(r *http.Request) (people.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := parseUUID(r.URL.Query().Get("company_id"), "company_id")
	if err != nil {
		return people.Scope{}, err
	}
	if !a.CanAccessCompany(companyID) {
		return people.Scope{}, errs.New(errs.CodeNotFound,
			"That company was not found.")
	}

	g := identity.GrantsFrom(r.Context())
	return people.Scope{
		TenantID:  a.TenantID,
		CompanyID: companyID,
		UserID:    a.UserID,
		MaySeePay: g != nil && g.Can("hr.view_pay"),
	}, nil
}

// --- Employees -----------------------------------------------------------

type employeeRequest struct {
	UserID     string `json:"user_id"`
	FullName   string `json:"full_name"`
	NameAr     string `json:"name_ar"`
	Phone      string `json:"phone"`
	Email      string `json:"email"`
	Position   string `json:"position"`
	Department string `json:"department"`
	StoreID    string `json:"store_id"`

	NationalID   string `json:"national_id"`
	IqamaNo      string `json:"iqama_no"`
	IDExpiresOn  string `json:"id_expires_on"`
	GOSINumber   string `json:"gosi_number"`
	QiwaContract string `json:"qiwa_contract_no"`
	Nationality  string `json:"nationality"`
	IsSaudi      bool   `json:"is_saudi"`

	IBAN     string `json:"iban"`
	BankName string `json:"bank_name"`
	JoinedOn string `json:"joined_on"`

	Basic              string `json:"basic_salary"`
	Housing            string `json:"housing_allowance"`
	Transport          string `json:"transport_allowance"`
	OtherAllowance     string `json:"other_allowance"`
	CommissionEligible bool   `json:"commission_eligible"`
	Notes              string `json:"notes"`
}

func (s *Server) handleHireEmployee(w http.ResponseWriter, r *http.Request) {
	var req employeeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := people.NewEmployee{
		FullName: req.FullName, NameAr: req.NameAr, Phone: req.Phone,
		Email: req.Email, Position: req.Position, Department: req.Department,
		NationalID: req.NationalID, IqamaNo: req.IqamaNo,
		GOSINumber: req.GOSINumber, QiwaContract: req.QiwaContract,
		Nationality: req.Nationality, IsSaudi: req.IsSaudi,
		IBAN: req.IBAN, BankName: req.BankName,
		CommissionEligible: req.CommissionEligible, Notes: req.Notes,
		Basic: decimal.Zero, Housing: decimal.Zero,
		Transport: decimal.Zero, OtherAllowance: decimal.Zero,
	}
	if in.UserID, err = optionalUUID(req.UserID, "user_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.StoreID, err = optionalUUID(req.StoreID, "store_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.IDExpiresOn, err = optionalDate(req.IDExpiresOn, "id_expires_on"); err != nil {
		httpx.Error(w, r, err)
		return
	}

	joined, err := optionalDate(req.JoinedOn, "joined_on")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if joined == nil {
		httpx.Error(w, r, errs.Validation("Say when they joined.").
			WithField("joined_on",
				"Length of service decides the end-of-service benefit."))
		return
	}
	in.JoinedOn = *joined

	// Setting pay on the way in is setting pay: it needs the same permission
	// as changing it later, or a hiring form becomes the way round A6.2.
	for _, p := range []struct {
		raw   string
		field string
		dst   *decimal.Decimal
	}{{req.Basic, "basic_salary", &in.Basic},
		{req.Housing, "housing_allowance", &in.Housing},
		{req.Transport, "transport_allowance", &in.Transport},
		{req.OtherAllowance, "other_allowance", &in.OtherAllowance}} {
		if p.raw == "" {
			continue
		}
		if !scope.MaySeePay {
			httpx.Error(w, r, errs.New(errs.CodeForbidden,
				"Setting pay needs permission to see pay."))
			return
		}
		v, e := parseAmount(p.raw, p.field, 0)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		*p.dst = v
	}

	out, err := s.people.Hire(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListEmployees(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.Directory(r.Context(), scope,
		r.URL.Query().Get("include_leavers") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadEmployee(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "employeeID"), "employeeID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.ReadEmployee(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateEmployee(w http.ResponseWriter, r *http.Request) {
	var req employeeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "employeeID"), "employeeID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var in people.EmployeeUpdate
	set := func(raw string, dst **string) {
		if raw != "" {
			v := raw
			*dst = &v
		}
	}
	set(req.FullName, &in.FullName)
	set(req.NameAr, &in.NameAr)
	set(req.Phone, &in.Phone)
	set(req.Email, &in.Email)
	set(req.Position, &in.Position)
	set(req.Department, &in.Department)
	set(req.IqamaNo, &in.IqamaNo)
	set(req.GOSINumber, &in.GOSINumber)
	set(req.QiwaContract, &in.QiwaContract)
	set(req.IBAN, &in.IBAN)
	set(req.BankName, &in.BankName)
	set(req.Notes, &in.Notes)

	if in.StoreID, err = optionalUUID(req.StoreID, "store_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.IDExpiresOn, err = optionalDate(req.IDExpiresOn, "id_expires_on"); err != nil {
		httpx.Error(w, r, err)
		return
	}

	for _, p := range []struct {
		raw   string
		field string
		dst   **decimal.Decimal
	}{{req.Basic, "basic_salary", &in.Basic},
		{req.Housing, "housing_allowance", &in.Housing},
		{req.Transport, "transport_allowance", &in.Transport},
		{req.OtherAllowance, "other_allowance", &in.OtherAllowance}} {
		if p.raw == "" {
			continue
		}
		v, e := parseAmount(p.raw, p.field, 0)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		*p.dst = &v
	}

	out, err := s.people.Update(r.Context(), scope, id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type leaveEmploymentRequest struct {
	LeftOn string `json:"left_on"`
	Reason string `json:"reason"`
}

func (s *Server) handleEmployeeLeaves(w http.ResponseWriter, r *http.Request) {
	var req leaveEmploymentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "employeeID"), "employeeID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	on, err := optionalDate(req.LeftOn, "left_on")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	when := time.Now().UTC()
	if on != nil {
		when = *on
	}

	out, err := s.people.Leave(r.Context(), scope, id, when, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleExpiringDocuments is C5's Iqama/ID expiry alert.
func (s *Server) handleExpiringDocuments(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	days := 60
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, e := parseAmount(raw, "days", 0)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		days = int(n.IntPart())
	}
	out, err := s.people.ExpiringDocuments(r.Context(), scope, days)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- Attendance and leave ------------------------------------------------

type attendanceRequest struct {
	Days []struct {
		EmployeeID string `json:"employee_id"`
		OnDate     string `json:"on_date"`
		Status     string `json:"status"`
		Hours      string `json:"hours_worked"`
		Overtime   string `json:"overtime_hours"`
		LateMins   int    `json:"late_minutes"`
		Note       string `json:"note"`
	} `json:"days"`
}

func (s *Server) handleRecordAttendance(w http.ResponseWriter, r *http.Request) {
	var req attendanceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := make([]people.NewAttendance, 0, len(req.Days))
	for i, d := range req.Days {
		employeeID, e := parseUUID(d.EmployeeID, "employee_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		on, e := optionalDate(d.OnDate, "on_date")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		if on == nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Row %d has no date.", i+1))
			return
		}
		day := people.NewAttendance{
			EmployeeID: employeeID, OnDate: *on, Status: d.Status,
			LateMins: d.LateMins, Note: d.Note,
			Hours: decimal.Zero, Overtime: decimal.Zero,
		}
		if d.Hours != "" {
			if day.Hours, e = parseAmount(d.Hours, "hours_worked", i); e != nil {
				httpx.Error(w, r, e)
				return
			}
		}
		if d.Overtime != "" {
			if day.Overtime, e = parseAmount(d.Overtime, "overtime_hours", i); e != nil {
				httpx.Error(w, r, e)
				return
			}
		}
		in = append(in, day)
	}

	written, err := s.people.RecordAttendance(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"recorded": written})
}

func (s *Server) handleReadAttendance(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, err := optionalDate(r.URL.Query().Get("from"), "from")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	to, err := optionalDate(r.URL.Query().Get("to"), "to")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// A month, when the caller did not say: the period an attendance screen
	// opens on, and a bounded query rather than the whole history.
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if from != nil {
		start = *from
	}
	end := start.AddDate(0, 1, -1)
	if to != nil {
		end = *to
	}

	employeeID, err := optionalUUID(r.URL.Query().Get("employee_id"), "employee_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.people.Attendance(r.Context(), scope, start, end, employeeID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type leaveRequestBody struct {
	EmployeeID string `json:"employee_id"`
	Kind       string `json:"kind"`
	IsPaid     bool   `json:"is_paid"`
	StartsOn   string `json:"starts_on"`
	EndsOn     string `json:"ends_on"`
	Days       string `json:"days"`
	Reason     string `json:"reason"`
}

func (s *Server) handleRequestLeave(w http.ResponseWriter, r *http.Request) {
	var req leaveRequestBody
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	employeeID, err := parseUUID(req.EmployeeID, "employee_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, err := optionalDate(req.StartsOn, "starts_on")
	if err != nil || from == nil {
		httpx.Error(w, r, errs.Validation("Say when the leave starts.").
			WithField("starts_on", "Dates look like 2026-08-16."))
		return
	}
	to, err := optionalDate(req.EndsOn, "ends_on")
	if err != nil || to == nil {
		httpx.Error(w, r, errs.Validation("Say when the leave ends.").
			WithField("ends_on", "Dates look like 2026-08-16."))
		return
	}
	days := decimal.Zero
	if req.Days != "" {
		if days, err = parseAmount(req.Days, "days", 0); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}

	out, err := s.people.RequestLeave(r.Context(), scope, employeeID, req.Kind,
		req.IsPaid, *from, *to, days, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleDecideLeave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "leaveID"), "leaveID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.DecideLeave(r.Context(), scope, id, req.Approve, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListLeave(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.LeaveRequests(r.Context(), scope,
		r.URL.Query().Get("status"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- Advances ------------------------------------------------------------

func (s *Server) handleIssueAdvance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EmployeeID   string `json:"employee_id"`
		AccountID    string `json:"account_id"`
		Amount       string `json:"amount"`
		Installments int    `json:"installments"`
		Reason       string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	employeeID, err := parseUUID(req.EmployeeID, "employee_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	accountID, err := parseUUID(req.AccountID, "account_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	amount, err := parseAmount(req.Amount, "amount", 0)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.people.IssueAdvance(r.Context(), scope, employeeID,
		accountID, amount, req.Installments, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListAdvances(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.Advances(r.Context(), scope,
		r.URL.Query().Get("include_settled") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- Payroll -------------------------------------------------------------

func (s *Server) handlePreparePayroll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Period string `json:"period"`
		Note   string `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	period := time.Now().UTC()
	if req.Period != "" {
		// A month, "2026-08". Accepting a full date too, because a client that
		// sends the first of the month is not wrong.
		parsed, e := time.Parse("2006-01", req.Period)
		if e != nil {
			full, e2 := time.Parse("2006-01-02", req.Period)
			if e2 != nil {
				httpx.Error(w, r, errs.Validation(
					"A payroll period is a month, like 2026-08.").
					WithField("period", "Year and month."))
				return
			}
			parsed = full
		}
		period = parsed
	}

	out, err := s.people.Prepare(r.Context(), scope, period, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleApprovePayroll(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "runID"), "runID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.Approve(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handlePayPayroll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID string `json:"account_id"`
		PaidOn    string `json:"paid_on"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "runID"), "runID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	accountID, err := parseUUID(req.AccountID, "account_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	on, err := optionalDate(req.PaidOn, "paid_on")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	when := time.Now().UTC()
	if on != nil {
		when = *on
	}

	out, err := s.people.Pay(r.Context(), scope, id, accountID, when)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListPayrollRuns(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.Runs(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadPayrollRun(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "runID"), "runID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.ReadRun(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleGenerateWageFile builds the WPS submission.
//
// Refuses while the Mudad layout is unverified, naming the rule — see
// people.GenerateWageFile for why a plausible file is worse than none.
func (s *Server) handleGenerateWageFile(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "runID"), "runID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.GenerateWageFile(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// --- End of service and commission ---------------------------------------

func (s *Server) handleAccrueEOSB(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Period string `json:"period"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	period := time.Now().UTC()
	if req.Period != "" {
		parsed, e := time.Parse("2006-01", req.Period)
		if e != nil {
			httpx.Error(w, r, errs.Validation(
				"An accrual period is a month, like 2026-08.").
				WithField("period", "Year and month."))
			return
		}
		period = parsed
	}

	charged, err := s.people.AccrueEOSB(r.Context(), scope, period)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"accrued": charged})
}

func (s *Server) handleEOSBPositions(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.EOSBPositions(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSetCommissionRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		Basis      string `json:"basis"`
		EmployeeID string `json:"employee_id"`
		StoreID    string `json:"store_id"`
		Rate       string `json:"rate"`
		Tiers      string `json:"tiers"`
		From       string `json:"effective_from"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	employeeID, err := optionalUUID(req.EmployeeID, "employee_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	storeID, err := optionalUUID(req.StoreID, "store_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	rate := decimal.Zero
	if req.Rate != "" {
		if rate, err = parseAmount(req.Rate, "rate", 0); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	from, err := optionalDate(req.From, "effective_from")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	when := time.Now().UTC()
	if from != nil {
		when = *from
	}

	out, err := s.people.SetCommissionRule(r.Context(), scope, req.Name,
		req.Basis, employeeID, storeID, rate, req.Tiers, when)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListCommissionRules(w http.ResponseWriter, r *http.Request) {
	scope, err := hrScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.people.CommissionRules(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}
