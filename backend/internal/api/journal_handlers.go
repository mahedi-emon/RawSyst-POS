// Hand-written journal entries (blueprint C10).
//
// Behind `accounting.create`, which 0101 already defines as "Write a journal
// entry by hand" and describes as posting "straight to the ledger, past every
// other screen". The permission existed and the thing it named did not; this is
// that thing, so no new verb is minted for it.
//
// The company is resolved from the authenticated caller and checked against the
// companies they may reach. It is never taken from the body — a journal names
// accounts directly, so a caller who could choose the company could post into
// somebody else's books.
package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

func (s *Server) journalScope(r *http.Request) (accounting.JournalScope, error) {
	company, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return accounting.JournalScope{}, err
	}
	a := actor.From(r.Context())
	return accounting.JournalScope{
		TenantID: a.TenantID, CompanyID: company, UserID: a.UserID,
	}, nil
}

// unavailable answers when the service was not wired.
func (s *Server) journalsReady(w http.ResponseWriter, r *http.Request) bool {
	if s.journals == nil {
		httpx.Error(w, r, errs.New(errs.CodeUnavailable,
			"Hand-written journals are not enabled on this deployment."))
		return false
	}
	return true
}

type journalLineRequest struct {
	AccountID string `json:"account_id"`
	Debit     string `json:"debit"`
	Credit    string `json:"credit"`
	Memo      string `json:"memo"`
	StoreID   string `json:"store_id"`
}

type recordJournalRequest struct {
	UUID   string               `json:"uuid"`
	Date   string               `json:"entry_date"`
	Reason string               `json:"reason"`
	Memo   string               `json:"memo"`
	Lines  []journalLineRequest `json:"lines"`
}

// handleRecordJournal posts an adjustment.
func (s *Server) handleRecordJournal(w http.ResponseWriter, r *http.Request) {
	if !s.journalsReady(w, r) {
		return
	}
	var req recordJournalRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.journalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := accounting.NewJournal{Reason: req.Reason, Memo: req.Memo}
	if in.UUID, err = parseUUID(req.UUID, "uuid"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	on, err := optionalDate(req.Date, "entry_date")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if on == nil {
		now := time.Now().UTC()
		on = &now
	}
	in.Date = *on

	for i, l := range req.Lines {
		line := accounting.NewJournalLine{Memo: l.Memo}
		if line.AccountID, err = parseUUID(l.AccountID, "account_id"); err != nil {
			httpx.Error(w, r, err)
			return
		}
		if line.Debit, err = optionalAmount(l.Debit, i+1, "debit"); err != nil {
			httpx.Error(w, r, err)
			return
		}
		if line.Credit, err = optionalAmount(l.Credit, i+1, "credit"); err != nil {
			httpx.Error(w, r, err)
			return
		}
		if l.StoreID != "" {
			store, e := parseUUID(l.StoreID, "store_id")
			if e != nil {
				httpx.Error(w, r, e)
				return
			}
			line.StoreID = &store
		}
		in.Lines = append(in.Lines, line)
	}

	out, err := s.journals.Record(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// optionalAmount reads one side of a line. Empty is zero, which is how a line
// says it is the other side.
func optionalAmount(raw string, line int, field string) (decimal.Decimal, error) {
	if raw == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"Line %d has a %s that is not a number.", line, field)
	}
	return d, nil
}

type reverseJournalRequest struct {
	UUID   string `json:"uuid"`
	Reason string `json:"reason"`
}

// handleReverseJournal posts the opposite of a journal already written.
func (s *Server) handleReverseJournal(w http.ResponseWriter, r *http.Request) {
	if !s.journalsReady(w, r) {
		return
	}
	id, err := parseUUID(chi.URLParam(r, "journalID"), "journalID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req reverseJournalRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.journalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	noteUUID := uuid.New()
	if req.UUID != "" {
		if noteUUID, err = parseUUID(req.UUID, "uuid"); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}

	out, err := s.journals.Reverse(r.Context(), scope, id, req.Reason, noteUUID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListJournals(w http.ResponseWriter, r *http.Request) {
	if !s.journalsReady(w, r) {
		return
	}
	scope, err := s.journalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		limit = atoiOr(v, 0)
	}
	out, err := s.journals.Journals(r.Context(), scope,
		r.URL.Query().Get("from"), r.URL.Query().Get("to"), limit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadJournal(w http.ResponseWriter, r *http.Request) {
	if !s.journalsReady(w, r) {
		return
	}
	id, err := parseUUID(chi.URLParam(r, "journalID"), "journalID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.journalScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.journals.Journal(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
