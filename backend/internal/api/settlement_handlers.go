package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/actor"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/httpx"
	"github.com/mahedi-emon/Biz1core/backend/internal/settlement"
)

// The card-settlement surface (P15, blueprint C12, design 02 §8).
//
// Back-office routes, so the company is named explicitly and CanAccessCompany
// answers "not found" for one the caller has no business in — the same shape as
// purchasing and receivables. There is no till-resolved variant on purpose: a
// deposit is reconciled against a bank statement by whoever does the books, not
// at a counter.
//
// Money crosses this boundary as STRINGS, like everywhere else. The net deposit
// is the one figure in this module that comes from outside the system, and
// widening it through a JavaScript float on the way in would put the rounding
// error into the ledger rather than into a display.

func settlementScope(r *http.Request) (settlement.Scope, error) {
	a := actor.From(r.Context())

	companyID, err := parseUUID(r.URL.Query().Get("company_id"), "company_id")
	if err != nil {
		return settlement.Scope{}, err
	}
	if !a.CanAccessCompany(companyID) {
		return settlement.Scope{}, errs.New(errs.CodeNotFound,
			"That company was not found.")
	}
	return settlement.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// GET /api/v1/settlement/pending — card money taken and not yet in the bank.
func (s *Server) handlePendingSettlement(w http.ResponseWriter, r *http.Request) {
	scope, err := settlementScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.settlement.Pending(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type settlementRequest struct {
	// UUID is assigned by the caller before the request, so a retry after a
	// lost response returns the original deposit rather than clearing the same
	// payments twice.
	UUID        string   `json:"uuid"`
	Reference   string   `json:"reference"`
	DepositedOn string   `json:"deposited_on"`
	NetAmount   string   `json:"net_amount"`
	TenderIDs   []string `json:"tender_ids"`
}

// POST /api/v1/settlement/batches — record a deposit.
func (s *Server) handleRecordSettlement(w http.ResponseWriter, r *http.Request) {
	var req settlementRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := settlementScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	docUUID, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	net, err := parseAmount(req.NetAmount, "net_amount", -1)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	depositedOn, err := time.Parse("2006-01-02", req.DepositedOn)
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"deposited_on must be a date like 2026-08-16. It is the day the "+
				"money landed, which is the day this posts."))
		return
	}

	tenderIDs := make([]uuid.UUID, 0, len(req.TenderIDs))
	for _, raw := range req.TenderIDs {
		id, e := parseUUID(raw, "tender_ids[]")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		tenderIDs = append(tenderIDs, id)
	}

	out, err := s.settlement.Record(r.Context(), scope, settlement.NewBatch{
		UUID: docUUID, Reference: req.Reference, DepositedOn: depositedOn,
		NetAmount: net, TenderIDs: tenderIDs,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	status := http.StatusCreated
	if out.AlreadyRecorded {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	httpx.JSON(w, status, out)
}

// GET /api/v1/settlement/batches — the deposits already matched.
//
// The half that was missing. Recording a deposit answered with its id once and
// nothing could find it again, so the detail route below was reachable only
// from a response somebody had kept. Reconciling a month means reading back
// what was already matched.
func (s *Server) handleListSettlements(w http.ResponseWriter, r *http.Request) {
	scope, err := settlementScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	q := r.URL.Query()
	var f settlement.BatchFilter
	for _, d := range []struct {
		key  string
		into **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		raw := q.Get(d.key)
		if raw == "" {
			continue
		}
		when, e := time.Parse("2006-01-02", raw)
		if e != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"Dates look like 2026-08-16."))
			return
		}
		*d.into = &when
	}
	if raw := q.Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n <= 0 {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"A limit is a whole number above zero."))
			return
		}
		f.Limit = n
	}

	out, err := s.settlement.List(r.Context(), scope, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// GET /api/v1/settlement/batches/{batchID} — a deposit and the sales it covered.
func (s *Server) handleReadSettlement(w http.ResponseWriter, r *http.Request) {
	scope, err := settlementScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	batchID, err := parseUUID(chi.URLParam(r, "batchID"), "batchID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.settlement.Read(r.Context(), scope, batchID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
