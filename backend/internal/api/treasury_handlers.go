package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/treasury"
)

// Cash and bank (C2), and the reconciliation (C11).
//
// Three permissions, split by what the act asserts:
//
//	accounting.view             — reading a balance
//	accounting.create           — moving money between the company's own
//	                              accounts, which is a journal entry
//	accounting.manage_accounts  — deciding which accounts exist
//	accounting.reconcile        — asserting that the books agree with an
//	                              outside party, which is the assertion an
//	                              auditor relies on

func (s *Server) treasuryScope(r *http.Request) (treasury.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return treasury.Scope{}, err
	}
	return treasury.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- accounts -------------------------------------------------------------

func (s *Server) handleListMoneyAccounts(w http.ResponseWriter, r *http.Request) {
	scope, err := s.treasuryScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.treasury.Accounts(r.Context(), scope,
		r.URL.Query().Get("include_retired") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleCreateMoneyAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind          string `json:"kind"`
		Name          string `json:"name"`
		NameAr        string `json:"name_ar"`
		Currency      string `json:"currency"`
		StoreID       string `json:"store_id"`
		AccountID     string `json:"account_id"`
		BankName      string `json:"bank_name"`
		AccountNumber string `json:"account_number"`
		IBAN          string `json:"iban"`
		SWIFT         string `json:"swift"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	accountID, err := parseUUID(req.AccountID, "account_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := treasury.NewAccount{
		Kind: req.Kind, Name: req.Name, NameAr: req.NameAr,
		Currency: req.Currency, AccountID: accountID,
		BankName: req.BankName, AccountNumber: req.AccountNumber,
		IBAN: req.IBAN, SWIFT: req.SWIFT,
	}
	if strings.TrimSpace(req.StoreID) != "" {
		id, e := parseUUID(req.StoreID, "store_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.StoreID = &id
	}

	scope, err := s.treasuryScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.treasury.CreateAccount(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleSetMoneyAccountActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active *bool `json:"is_active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Active == nil {
		httpx.Error(w, r, errs.Validation("Say whether the account is in use.").
			WithField("is_active", "true to bring it back, false to retire it."))
		return
	}
	scope, err := s.treasuryScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "accountID"), "accountID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.treasury.SetAccountActive(r.Context(), scope, id, *req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- transfers ------------------------------------------------------------

func (s *Server) handleMoveMoney(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID          string `json:"uuid"`
		FromAccountID string `json:"from_account_id"`
		ToAccountID   string `json:"to_account_id"`
		Amount        string `json:"amount"`
		MovedOn       string `json:"moved_on"`
		Reference     string `json:"reference"`
		Note          string `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	id, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, err := parseUUID(req.FromAccountID, "from_account_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	to, err := parseUUID(req.ToAccountID, "to_account_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(req.Amount))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say how much is being moved."))
		return
	}
	movedOn, err := parseReportDate(req.MovedOn, "moved_on", time.Now().UTC())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	scope, err := s.treasuryScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.treasury.Move(r.Context(), scope, treasury.NewTransfer{
		UUID: id, FromAccountID: from, ToAccountID: to,
		Amount: amount, MovedOn: movedOn,
		Reference: req.Reference, Note: req.Note,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	status := http.StatusCreated
	if out.AlreadyRecorded {
		status = http.StatusOK
	}
	httpx.JSON(w, status, out)
}

func (s *Server) handleListMoneyTransfers(w http.ResponseWriter, r *http.Request) {
	scope, err := s.treasuryScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.treasury.Transfers(r.Context(), scope,
		atoiOr(r.URL.Query().Get("limit"), 0))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- reconciliation -------------------------------------------------------

func (s *Server) handleImportStatement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID string `json:"account_id"`
		StartsOn  string `json:"starts_on"`
		EndsOn    string `json:"ends_on"`
		Opening   string `json:"opening_balance"`
		Closing   string `json:"closing_balance"`
		Reference string `json:"reference"`
		Lines     []struct {
			ValueDate   string `json:"value_date"`
			Description string `json:"description"`
			Reference   string `json:"reference"`
			Amount      string `json:"amount"`
		} `json:"lines"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	accountID, err := parseUUID(req.AccountID, "account_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	startsOn, err := parseReportDate(req.StartsOn, "starts_on", time.Time{})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	endsOn, err := parseReportDate(req.EndsOn, "ends_on", time.Time{})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	opening, err := decimal.NewFromString(strings.TrimSpace(req.Opening))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say what the account held at the start of the statement."))
		return
	}
	closing, err := decimal.NewFromString(strings.TrimSpace(req.Closing))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say what the account held at the end of the statement."))
		return
	}

	in := treasury.NewStatement{
		AccountID: accountID, StartsOn: startsOn, EndsOn: endsOn,
		Opening: opening, Closing: closing, Reference: req.Reference,
	}
	for i, l := range req.Lines {
		date, e := parseReportDate(l.ValueDate, "value_date", time.Time{})
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		amount, e := decimal.NewFromString(strings.TrimSpace(l.Amount))
		if e != nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not carry an amount.", i+1))
			return
		}
		in.Lines = append(in.Lines, treasury.NewStatementLine{
			ValueDate: date, Description: l.Description,
			Reference: l.Reference, Amount: amount,
		})
	}

	scope, err := s.treasuryScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.treasury.Import(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListStatements(w http.ResponseWriter, r *http.Request) {
	scope, err := s.treasuryScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var accountID *uuid.UUID
	if v := strings.TrimSpace(r.URL.Query().Get("account_id")); v != "" {
		id, e := parseUUID(v, "account_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		accountID = &id
	}
	out, err := s.treasury.Statements(r.Context(), scope, accountID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleGetStatement(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.statementScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.treasury.Statement(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleMatchStatementLine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JournalLineID string `json:"journal_line_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.treasuryScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	lineID, err := parseUUID(chi.URLParam(r, "lineID"), "lineID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// An empty journal line is how a match is undone. One route rather than
	// two, because "point this line at that entry" and "point it at nothing"
	// are the same edit and a person toggles between them.
	if strings.TrimSpace(req.JournalLineID) == "" {
		if err := s.treasury.Unmatch(r.Context(), scope, lineID); err != nil {
			httpx.Error(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	journalLineID, err := parseUUID(req.JournalLineID, "journal_line_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.treasury.Match(r.Context(), scope, lineID, journalLineID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReconcileStatement(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.statementScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.treasury.Reconcile(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) statementScope(
	r *http.Request,
) (treasury.Scope, uuid.UUID, error) {
	scope, err := s.treasuryScope(r)
	if err != nil {
		return treasury.Scope{}, uuid.Nil, err
	}
	id, err := parseUUID(chi.URLParam(r, "statementID"), "statementID")
	if err != nil {
		return treasury.Scope{}, uuid.Nil, err
	}
	return scope, id, nil
}
