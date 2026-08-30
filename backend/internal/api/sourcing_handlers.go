package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
)

// The sourcing surface: requisitions, RFQs, quotes and the award (B5, B5.1).
//
// It reuses purchaseScope for the same reason the rest of purchasing does —
// sourcing happens in a back office at a browser, with no registered terminal
// to resolve a company from, so the company is a parameter and
// CanAccessCompany is what stops a caller naming one they have no business in.
//
// The permissions are deliberately finer than "purchasing". Asking for stock,
// approving somebody else's request, running a comparison and awarding it are
// four different acts of trust, and B5.1's whole value is that the last of them
// is answerable: "who chose this supplier, and why" is worth less if the person
// running the comparison also signs it off.

// --- Requisitions --------------------------------------------------------

type requisitionLineRequest struct {
	VariantID   string `json:"variant_id"`
	Description string `json:"description"`
	Qty         string `json:"qty"`
	Note        string `json:"note"`
}

type requisitionRequest struct {
	StoreID     string                   `json:"store_id"`
	WarehouseID string                   `json:"warehouse_id"`
	NeededBy    string                   `json:"needed_by"`
	Why         string                   `json:"justification"`
	Lines       []requisitionLineRequest `json:"lines"`
}

func (s *Server) handleRaiseRequisition(w http.ResponseWriter, r *http.Request) {
	var req requisitionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := purchasing.NewRequisition{Why: req.Why}
	if in.StoreID, err = optionalUUID(req.StoreID, "store_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.WarehouseID, err = optionalUUID(req.WarehouseID, "warehouse_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.NeededBy, err = optionalDate(req.NeededBy, "needed_by"); err != nil {
		httpx.Error(w, r, err)
		return
	}

	for i, l := range req.Lines {
		qty, e := parseAmount(l.Qty, "qty", i)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		variantID, e := optionalUUID(l.VariantID, "variant_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.Lines = append(in.Lines, purchasing.NewRequisitionLine{
			VariantID: variantID, Description: l.Description,
			Qty: qty, Note: l.Note,
		})
	}

	out, err := s.purchasing.RaiseRequisition(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListRequisitions(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.purchasing.ListRequisitions(
		r.Context(), scope, r.URL.Query().Get("status"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"requisitions": out})
}

func (s *Server) handleReadRequisition(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "requisitionID"), "requisitionID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.purchasing.ReadRequisition(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type requisitionDecisionRequest struct {
	Approve bool   `json:"approve"`
	Note    string `json:"note"`
}

func (s *Server) handleDecideRequisition(w http.ResponseWriter, r *http.Request) {
	var req requisitionDecisionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "requisitionID"), "requisitionID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.purchasing.DecideRequisition(
		r.Context(), scope, id, req.Approve, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- RFQ -----------------------------------------------------------------

type rfqLineRequest struct {
	VariantID   string `json:"variant_id"`
	Description string `json:"description"`
	Qty         string `json:"qty"`
}

type rfqRequest struct {
	RequisitionID string           `json:"requisition_id"`
	WarehouseID   string           `json:"warehouse_id"`
	ClosesOn      string           `json:"closes_on"`
	Notes         string           `json:"notes"`
	SupplierIDs   []string         `json:"supplier_ids"`
	Lines         []rfqLineRequest `json:"lines"`
}

func (s *Server) handleRaiseRFQ(w http.ResponseWriter, r *http.Request) {
	var req rfqRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	warehouseID, err := parseUUID(req.WarehouseID, "warehouse_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := purchasing.NewRFQ{WarehouseID: warehouseID, Notes: req.Notes}
	if in.RequisitionID, err = optionalUUID(req.RequisitionID, "requisition_id"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.ClosesOn, err = optionalDate(req.ClosesOn, "closes_on"); err != nil {
		httpx.Error(w, r, err)
		return
	}

	for _, raw := range req.SupplierIDs {
		id, e := parseUUID(raw, "supplier_ids")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.SupplierIDs = append(in.SupplierIDs, id)
	}

	for i, l := range req.Lines {
		variantID, e := parseUUID(l.VariantID, "variant_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		qty, e := parseAmount(l.Qty, "qty", i)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.Lines = append(in.Lines, purchasing.NewRFQLine{
			VariantID: variantID, Description: l.Description, Qty: qty,
		})
	}

	out, err := s.purchasing.RaiseRFQ(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListRFQs(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.purchasing.ListRFQs(
		r.Context(), scope, r.URL.Query().Get("status"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"rfqs": out})
}

// handleCompareRFQ is B5.1's comparison screen: every live quote against one
// request, side by side.
func (s *Server) handleCompareRFQ(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "rfqID"), "rfqID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.purchasing.Compare(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

type cancelRFQRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleCancelRFQ(w http.ResponseWriter, r *http.Request) {
	var req cancelRFQRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "rfqID"), "rfqID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.purchasing.CancelRFQ(r.Context(), scope, id, req.Reason); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Quotes --------------------------------------------------------------

type quoteLineRequest struct {
	RFQLineID    string `json:"rfq_line_id"`
	Qty          string `json:"qty"`
	UnitCost     string `json:"unit_cost"`
	TaxTreatment string `json:"tax_treatment"`
	TaxRate      string `json:"tax_rate"`
	Note         string `json:"note"`
}

type quoteRequest struct {
	SupplierID   string             `json:"supplier_id"`
	QuoteNumber  string             `json:"quote_number"`
	ReceivedOn   string             `json:"received_on"`
	ValidUntil   string             `json:"valid_until"`
	LeadTimeDays *int               `json:"lead_time_days"`
	TermsDays    *int               `json:"payment_terms_days"`
	QualityNote  string             `json:"quality_note"`
	Notes        string             `json:"notes"`
	Lines        []quoteLineRequest `json:"lines"`
}

func (s *Server) handleRecordQuote(w http.ResponseWriter, r *http.Request) {
	var req quoteRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	rfqID, err := parseUUID(chi.URLParam(r, "rfqID"), "rfqID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	supplierID, err := parseUUID(req.SupplierID, "supplier_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := purchasing.NewQuote{
		RFQID: rfqID, SupplierID: supplierID, QuoteNumber: req.QuoteNumber,
		LeadTimeDays: req.LeadTimeDays, TermsDays: req.TermsDays,
		QualityNote: req.QualityNote, Notes: req.Notes,
	}
	if in.ReceivedOn, err = optionalDate(req.ReceivedOn, "received_on"); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if in.ValidUntil, err = optionalDate(req.ValidUntil, "valid_until"); err != nil {
		httpx.Error(w, r, err)
		return
	}

	for i, l := range req.Lines {
		lineID, e := parseUUID(l.RFQLineID, "rfq_line_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		qty, e := parseAmount(l.Qty, "qty", i)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		cost, e := parseAmount(l.UnitCost, "unit_cost", i)
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		rate := decimal.Zero
		if l.TaxRate != "" {
			if rate, e = parseAmount(l.TaxRate, "tax_rate", i); e != nil {
				httpx.Error(w, r, e)
				return
			}
		}
		in.Lines = append(in.Lines, purchasing.NewQuoteLine{
			RFQLineID: lineID, Qty: qty, UnitCost: cost,
			TaxTreatment: l.TaxTreatment, TaxRate: rate, Note: l.Note,
		})
	}

	out, err := s.purchasing.RecordQuote(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

type declineQuoteRequest struct {
	SupplierID string `json:"supplier_id"`
	Reason     string `json:"reason"`
}

func (s *Server) handleDeclineToQuote(w http.ResponseWriter, r *http.Request) {
	var req declineQuoteRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	rfqID, err := parseUUID(chi.URLParam(r, "rfqID"), "rfqID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	supplierID, err := parseUUID(req.SupplierID, "supplier_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.purchasing.DeclineToQuote(
		r.Context(), scope, rfqID, supplierID, req.Reason); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type awardRequest struct {
	QuoteID string `json:"quote_id"`
	Reason  string `json:"reason"`
}

// handleAwardRFQ picks the winner and raises the purchase order.
//
// 201, not 200: the call creates a purchase order, and the order is in the
// response body. A caller that treated this as an idempotent update would
// happily send it twice — the service refuses the second, but the status code
// should say what happened the first time.
func (s *Server) handleAwardRFQ(w http.ResponseWriter, r *http.Request) {
	var req awardRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	rfqID, err := parseUUID(chi.URLParam(r, "rfqID"), "rfqID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	quoteID, err := parseUUID(req.QuoteID, "quote_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.purchasing.AwardQuote(r.Context(), scope, rfqID, quoteID, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// handleSupplierQuoteHistory is B5.1's archive: what this supplier has quoted
// before, won or lost, so the next negotiation starts from a fact.
func (s *Server) handleSupplierQuoteHistory(w http.ResponseWriter, r *http.Request) {
	scope, err := purchaseScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "supplierID"), "supplierID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.purchasing.QuotesFromSupplier(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"quotes": out})
}

// --- shared parsing ------------------------------------------------------

// optionalUUID parses an id that may legitimately be absent. An empty string
// yields nil rather than an error, and anything else must be a real uuid — a
// malformed id is never silently treated as "not supplied", because that turns
// a typo into a quietly different request.
func optionalUUID(raw, field string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := parseUUID(raw, field)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// optionalDate parses a date that may be absent, in the one format the rest of
// this API uses.
func optionalDate(raw, field string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	when, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, errs.Validation("Dates look like 2026-08-16.").
			WithField(field, "Use the year, month and day.")
	}
	return &when, nil
}
