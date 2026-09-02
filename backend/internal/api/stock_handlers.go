package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/stockops"
)

// Stock operations, blueprint B4.
//
// A back-office surface, so the company is named in the query string and
// checked against the caller's confinement, exactly as purchasing, receivables
// and expenses do.
//
// The till deliberately gets none of this. A cashier who could write stock off
// could cover a theft with two presses, and B4's whole point is that a
// correction carries a reason and a name — which is a back-office act performed
// at a desk, not a counter act performed in front of a queue.

func (s *Server) stockScope(r *http.Request) (stockops.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return stockops.Scope{}, err
	}
	return stockops.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- stock locations ------------------------------------------------------

func (s *Server) handleListStockLocations(w http.ResponseWriter, r *http.Request) {
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.Locations(r.Context(), scope,
		r.URL.Query().Get("include_retired") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// Already an envelope: `data` is the locations and `branches` is what a new
	// one can be attached to.
	httpx.JSON(w, http.StatusOK, out)
}

type stockLocationRequest struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	StoreID string `json:"store_id"`
}

func (s *Server) handleCreateStockLocation(w http.ResponseWriter, r *http.Request) {
	var req stockLocationRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := stockops.NewLocation{Code: req.Code, Name: req.Name, Kind: req.Kind}
	if strings.TrimSpace(req.StoreID) != "" {
		id, e := parseUUID(req.StoreID, "store_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.StoreID = &id
	}

	out, err := s.stock.CreateLocation(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleRenameStockLocation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, id, err := s.stockScopeAndID(r, "locationID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.stock.RenameLocation(r.Context(), scope, id, req.Name); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetStockLocationActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active *bool `json:"is_active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Active == nil {
		httpx.Error(w, r, errs.Validation(
			"Say whether the location is in use.").
			WithField("is_active", "true to bring it back, false to retire it."))
		return
	}
	scope, id, err := s.stockScopeAndID(r, "locationID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.stock.SetLocationActive(r.Context(), scope, id, *req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- what is on the shelf -------------------------------------------------

func (s *Server) handleStockOnHand(w http.ResponseWriter, r *http.Request) {
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	f, err := stockFilterFrom(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.OnHand(r.Context(), scope, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleStockMovements(w http.ResponseWriter, r *http.Request) {
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	f, err := stockFilterFrom(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.Movements(r.Context(), scope, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func stockFilterFrom(r *http.Request) (stockops.StockFilter, error) {
	q := r.URL.Query()
	f := stockops.StockFilter{
		Search:  strings.TrimSpace(q.Get("q")),
		OnlyLow: q.Get("low") == "true",
		Limit:   atoiOr(q.Get("limit"), 0),
	}
	if v := strings.TrimSpace(q.Get("location_id")); v != "" {
		id, err := parseUUID(v, "location_id")
		if err != nil {
			return stockops.StockFilter{}, err
		}
		f.WarehouseID = &id
	}
	return f, nil
}

// --- adjustments and wastage ----------------------------------------------

type adjustmentRequest struct {
	// UUID is the caller's own identifier for this voucher, so a retry after a
	// lost response returns the original rather than writing the stock off
	// twice.
	UUID       string `json:"uuid"`
	LocationID string `json:"location_id"`
	Kind       string `json:"kind"`
	Reason     string `json:"reason"`
	Note       string `json:"note"`
	Lines      []struct {
		VariantID string `json:"variant_id"`
		Delta     string `json:"delta"`
	} `json:"lines"`
}

func (req adjustmentRequest) into() (stockops.NewAdjustment, error) {
	id, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		return stockops.NewAdjustment{}, err
	}
	locationID, err := parseUUID(req.LocationID, "location_id")
	if err != nil {
		return stockops.NewAdjustment{}, err
	}

	out := stockops.NewAdjustment{
		UUID:        id,
		WarehouseID: locationID,
		Kind:        strings.TrimSpace(req.Kind),
		Reason:      strings.TrimSpace(req.Reason),
		Note:        req.Note,
	}
	for i, l := range req.Lines {
		variantID, e := parseUUID(l.VariantID, "variant_id")
		if e != nil {
			return stockops.NewAdjustment{}, e
		}
		delta, e := decimal.NewFromString(strings.TrimSpace(l.Delta))
		if e != nil {
			return stockops.NewAdjustment{}, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not say how much the stock is out by.", i+1)
		}
		out.Lines = append(out.Lines, stockops.NewAdjustmentLine{
			VariantID: variantID, Delta: delta,
		})
	}
	return out, nil
}

func (s *Server) handleRecordStockAdjustment(w http.ResponseWriter, r *http.Request) {
	var req adjustmentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	in, err := req.into()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.RecordAdjustment(r.Context(), scope, in)
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

func (s *Server) handleListStockAdjustments(w http.ResponseWriter, r *http.Request) {
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	q := r.URL.Query()
	f := stockops.AdjustmentFilter{
		Kind:   strings.TrimSpace(q.Get("kind")),
		Status: strings.TrimSpace(q.Get("status")),
		Limit:  atoiOr(q.Get("limit"), 0),
	}
	if v := strings.TrimSpace(q.Get("location_id")); v != "" {
		id, e := parseUUID(v, "location_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		f.WarehouseID = &id
	}
	out, err := s.stock.Adjustments(r.Context(), scope, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleGetStockAdjustment(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.stockScopeAndID(r, "adjustmentID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.Adjustment(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- the physical count ---------------------------------------------------

func (s *Server) handleOpenStockCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LocationID string   `json:"location_id"`
		CategoryID string   `json:"category_id"`
		VariantIDs []string `json:"variant_ids"`
		Note       string   `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	locationID, err := parseUUID(req.LocationID, "location_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := stockops.CountScope{WarehouseID: locationID, Note: req.Note}
	if strings.TrimSpace(req.CategoryID) != "" {
		id, e := parseUUID(req.CategoryID, "category_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.CategoryID = &id
	}
	for _, v := range req.VariantIDs {
		id, e := parseUUID(v, "variant_ids")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.VariantIDs = append(in.VariantIDs, id)
	}

	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.OpenCount(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleSaveStockCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lines []struct {
			VariantID string `json:"variant_id"`
			Qty       string `json:"counted_qty"`
		} `json:"lines"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, id, err := s.stockScopeAndID(r, "adjustmentID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	lines := make([]stockops.CountedLine, 0, len(req.Lines))
	for i, l := range req.Lines {
		variantID, e := parseUUID(l.VariantID, "variant_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		qty, e := decimal.NewFromString(strings.TrimSpace(l.Qty))
		if e != nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not say how many were counted.", i+1))
			return
		}
		lines = append(lines, stockops.CountedLine{VariantID: variantID, Qty: qty})
	}

	if err := s.stock.SaveCount(r.Context(), scope, id, lines); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePostStockCount(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.stockScopeAndID(r, "adjustmentID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.PostCount(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCancelStockCount(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.stockScopeAndID(r, "adjustmentID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.stock.CancelCount(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- transfers ------------------------------------------------------------

type transferQtyRequest struct {
	VariantID string `json:"variant_id"`
	Qty       string `json:"qty"`
}

func transferQuantities(in []transferQtyRequest) ([]stockops.TransferQty, error) {
	out := make([]stockops.TransferQty, 0, len(in))
	for i, l := range in {
		variantID, err := parseUUID(l.VariantID, "variant_id")
		if err != nil {
			return nil, err
		}
		qty, err := decimal.NewFromString(strings.TrimSpace(l.Qty))
		if err != nil {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not say how many.", i+1)
		}
		out = append(out, stockops.TransferQty{VariantID: variantID, Qty: qty})
	}
	return out, nil
}

func (s *Server) handleRequestStockTransfer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromLocationID string               `json:"from_location_id"`
		ToLocationID   string               `json:"to_location_id"`
		Note           string               `json:"note"`
		Lines          []transferQtyRequest `json:"lines"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, err := parseUUID(req.FromLocationID, "from_location_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	to, err := parseUUID(req.ToLocationID, "to_location_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	lines, err := transferQuantities(req.Lines)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.RequestTransfer(r.Context(), scope, stockops.NewTransfer{
		FromWarehouseID: from, ToWarehouseID: to, Note: req.Note, Lines: lines,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleApproveStockTransfer(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.stockScopeAndID(r, "transferID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.ApproveTransfer(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleDispatchStockTransfer(w http.ResponseWriter, r *http.Request) {
	s.transferStep(w, r, s.stock.DispatchTransfer)
}

func (s *Server) handleReceiveStockTransfer(w http.ResponseWriter, r *http.Request) {
	s.transferStep(w, r, s.stock.ReceiveTransfer)
}

// transferStep is dispatch and receipt, which take the same body — a list of
// quantities that may amend what the document says — and differ only in which
// service call they make.
func (s *Server) transferStep(
	w http.ResponseWriter, r *http.Request,
	step func(ctx context.Context, scope stockops.Scope, id uuid.UUID,
		qty []stockops.TransferQty) (stockops.Transfer, error),
) {
	var req struct {
		Lines []transferQtyRequest `json:"lines"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	lines, err := transferQuantities(req.Lines)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, id, err := s.stockScopeAndID(r, "transferID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := step(r.Context(), scope, id, lines)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCancelStockTransfer(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.stockScopeAndID(r, "transferID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.stock.CancelTransfer(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListStockTransfers(w http.ResponseWriter, r *http.Request) {
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	q := r.URL.Query()
	out, err := s.stock.Transfers(r.Context(), scope,
		strings.TrimSpace(q.Get("status")), atoiOr(q.Get("limit"), 0))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleGetStockTransfer(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.stockScopeAndID(r, "transferID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.Transfer(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- shared ---------------------------------------------------------------

func (s *Server) stockScopeAndID(
	r *http.Request, param string,
) (stockops.Scope, uuid.UUID, error) {
	scope, err := s.stockScope(r)
	if err != nil {
		return stockops.Scope{}, uuid.Nil, err
	}
	id, err := parseUUID(chi.URLParam(r, param), param)
	if err != nil {
		return stockops.Scope{}, uuid.Nil, err
	}
	return scope, id, nil
}

func atoiOr(s string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return fallback
}

// handleTerminalStock serves the till its own warehouse's quantities.
//
// # Why the till does not call /stock/on-hand
//
// It cannot say which company or which warehouse: a terminal deliberately
// knows neither, and every POS route resolves both from the DEVICE. A till
// that could name its own company could read another company's figures, and
// row-level security would not catch it because the rows belong to the same
// tenant.
//
// # What the till does with it
//
// Caches it, and applies `stock.moved` deltas to the cache while it is online.
// The cache is provisional by construction — it is stale the moment another
// till sells something — and design 03 forbids it from refusing a sale. It
// exists so a cashier can be told, not so the terminal can decide.
//
// Gated on `inventory.view` like every other reading of stock levels. A cashier
// without it simply gets no cached quantity and the till says nothing about
// stock, which is exactly how it behaved before this existed.
func (s *Server) handleTerminalStock(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	f, err := stockFilterFrom(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.stock.OnHandForTerminal(r.Context(), a.TenantID, a.DeviceID, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- batches (B4) ----------------------------------------------------------

// handleListBatches lists lots, soonest to expire first.
//
// `expiring_within_days` narrows it to what a shop has to act on, which is the
// question the screen usually opens with.
func (s *Server) handleListBatches(w http.ResponseWriter, r *http.Request) {
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var f stockops.BatchFilter
	if raw := r.URL.Query().Get("variant_id"); raw != "" {
		id, e := parseUUID(raw, "variant_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		f.VariantID = &id
	}
	if raw := r.URL.Query().Get("expiring_within_days"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 0 {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"expiring_within_days must be a whole number of days."))
			return
		}
		f.ExpiringWithinDays = n
	}
	f.IncludeEmpty = r.URL.Query().Get("include_empty") == "true"

	out, err := s.stock.Batches(r.Context(), scope, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type recallRequest struct {
	Reason string `json:"reason"`
}

// handleRecallBatch withdraws a lot from sale and answers who bought from it.
func (s *Server) handleRecallBatch(w http.ResponseWriter, r *http.Request) {
	batchID, err := parseUUID(chi.URLParam(r, "batchID"), "batchID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req recallRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.stockScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.stock.Recall(r.Context(), scope, batchID, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
