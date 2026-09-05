package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
)

// Sending goods back to a supplier (B5).
//
// Three routes and no more: what is left to send back on a bill, the claim
// itself, and reading one. There is no draft and no edit — the stock has left
// the building by the time the document exists, and correcting a return means
// receiving the goods back, not amending the claim.

// --- GET /api/v1/purchasing/bills/{billID}/returnable --------------------

// handleBillReturnable says how much of each line may still go back.
//
// The screen reads this rather than working it out from the bill. Earlier
// returns are rows a browser may never have seen, and a client that subtracted
// for itself would eventually claim the same pallet twice — the same reason
// the till reads `returnable` rather than counting credit notes.
func (s *Server) handleBillReturnable(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	billID, err := parseUUID(chi.URLParam(r, "billID"), "billID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.purchasing.ReturnableLines(r.Context(), scope, billID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- GET /api/v1/purchasing/returns --------------------------------------

func (s *Server) handleListPurchaseReturns(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var billID *uuid.UUID
	if v := strings.TrimSpace(r.URL.Query().Get("bill_id")); v != "" {
		id, e := parseUUID(v, "bill_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		billID = &id
	}
	out, err := s.purchasing.Returns(r.Context(), scope, billID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- GET /api/v1/purchasing/returns/{returnID} ---------------------------

func (s *Server) handleReadPurchaseReturn(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "returnID"), "returnID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.purchasing.Return(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- POST /api/v1/purchasing/returns -------------------------------------

type purchaseReturnRequest struct {
	// UUID is minted by the caller, so a clerk pressing the button twice on a
	// bad connection claims once and takes the stock out once.
	UUID   string `json:"uuid"`
	BillID string `json:"bill_id"`
	// WarehouseID is where the stock leaves from. Optional, and required only
	// where a business keeps stock in more than one place.
	WarehouseID string `json:"warehouse_id"`
	ReturnedOn  string `json:"returned_on"`
	Reason      string `json:"reason"`
	Lines       []struct {
		BillLineID string `json:"bill_line_id"`
		Qty        string `json:"qty"`
	} `json:"lines"`
}

func (s *Server) handleReturnGoods(w http.ResponseWriter, r *http.Request) {
	var req purchaseReturnRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := purchasing.NewReturn{Reason: req.Reason}
	if in.UUID, err = parseUUID(req.UUID, "uuid"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.BillID, err = parseUUID(req.BillID, "bill_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if strings.TrimSpace(req.WarehouseID) != "" {
		id, e := parseUUID(req.WarehouseID, "warehouse_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.WarehouseID = &id
	}
	if in.ReturnedOn, err = parseReportDate(
		req.ReturnedOn, "returned_on", time.Now().UTC()); err != nil {
		httpx.Error(w, r, err)
		return
	}

	for i, line := range req.Lines {
		lineID, e := parseUUID(line.BillLineID, "lines[].bill_line_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		qty, e := decimal.NewFromString(strings.TrimSpace(line.Qty))
		if e != nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Line %d has no quantity.", i+1))
			return
		}
		in.Lines = append(in.Lines,
			purchasing.ReturnLine{BillLineID: lineID, Qty: qty})
	}

	out, err := s.purchasing.ReturnGoods(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// 200 rather than 201 for a recognised retry, with the stored claim. The
	// books already say what the caller wanted them to say.
	status := http.StatusCreated
	if out.AlreadyReturned {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	httpx.JSON(w, status, out)
}
