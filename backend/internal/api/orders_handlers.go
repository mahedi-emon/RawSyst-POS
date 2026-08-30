package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/orders"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// Quotations, sales orders and the warehouse documents (blueprint B11, B12).
//
// Two verbs. `order.view` reaches the list, one order and the three printable
// documents — a picker and a driver both need those and neither should be able
// to change a price. `order.manage` raises, advances, cancels, and records what
// was picked and delivered.

func (s *Server) orderScope(r *http.Request) (orders.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return orders.Scope{}, err
	}
	return orders.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// Named for the sales side, because `handleListOrders` was already the
// PURCHASE order list. A purchase order and a sales order are different
// documents pointing in opposite directions.
func (s *Server) handleListSalesOrders(w http.ResponseWriter, r *http.Request) {
	scope, err := s.orderScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	q := r.URL.Query()
	f := orders.Filter{
		State:   strings.TrimSpace(q.Get("state")),
		Channel: strings.TrimSpace(q.Get("channel")),
		Open:    q.Get("open") == "true",
		Limit:   atoiOr(q.Get("limit"), 0),
	}
	if v := strings.TrimSpace(q.Get("customer_id")); v != "" {
		id, e := parseUUID(v, "customer_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		f.CustomerID = &id
	}
	out, err := s.orders.List(r.Context(), scope, f)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.orderScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.orders.Order(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleRaiseOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CustomerID   string `json:"customer_id"`
		StoreID      string `json:"store_id"`
		Channel      string `json:"channel"`
		Region       string `json:"region"`
		ValidUntil   string `json:"valid_until"`
		DeliverTo    string `json:"deliver_to"`
		DeliverPhone string `json:"deliver_phone"`
		Notes        string `json:"notes"`
		Lines        []struct {
			VariantID   string `json:"variant_id"`
			Description string `json:"description"`
			Qty         string `json:"qty"`
			UnitPrice   string `json:"unit_price"`
			Discount    string `json:"discount"`
		} `json:"lines"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := orders.NewOrder{
		Channel: req.Channel, Region: req.Region, Notes: req.Notes,
		DeliverTo: req.DeliverTo, DeliverPhone: req.DeliverPhone,
	}
	if v := strings.TrimSpace(req.CustomerID); v != "" {
		id, e := parseUUID(v, "customer_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.CustomerID = &id
	}
	if v := strings.TrimSpace(req.StoreID); v != "" {
		id, e := parseUUID(v, "store_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.StoreID = &id
	}
	if v := strings.TrimSpace(req.ValidUntil); v != "" {
		day, e := parseReportDate(v, "valid_until", time.Time{})
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.ValidUntil = &day
	}

	for i, l := range req.Lines {
		variantID, e := parseUUID(l.VariantID, "variant_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		qty, e := decimal.NewFromString(strings.TrimSpace(l.Qty))
		if e != nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not say how many.", i+1))
			return
		}
		price, e := decimal.NewFromString(strings.TrimSpace(l.UnitPrice))
		if e != nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not carry a price.", i+1))
			return
		}
		discount := decimal.Zero
		if strings.TrimSpace(l.Discount) != "" {
			discount, _ = decimal.NewFromString(strings.TrimSpace(l.Discount))
		}
		in.Lines = append(in.Lines, orders.NewLine{
			VariantID: variantID, Description: l.Description,
			Qty: qty, UnitPrice: price, Discount: discount,
		})
	}

	scope, err := s.orderScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.orders.Raise(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleAdvanceOrder(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.orderScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.orders.Advance(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, id, err := s.orderScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.orders.Cancel(r.Context(), scope, id, req.Reason); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePickOrder(w http.ResponseWriter, r *http.Request) {
	s.orderQuantities(w, r, s.orders.Pick)
}

func (s *Server) handleDeliverOrder(w http.ResponseWriter, r *http.Request) {
	s.orderQuantities(w, r, s.orders.Deliver)
}

// orderQuantities is picking and delivery, which take the same body and differ
// only in which column they write.
func (s *Server) orderQuantities(
	w http.ResponseWriter, r *http.Request,
	record func(ctx context.Context, scope orders.Scope, id uuid.UUID,
		qty []orders.LineQty) (orders.Order, error),
) {
	var req struct {
		Lines []struct {
			LineID string `json:"line_id"`
			Qty    string `json:"qty"`
		} `json:"lines"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, id, err := s.orderScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	quantities := make([]orders.LineQty, 0, len(req.Lines))
	for i, l := range req.Lines {
		lineID, e := parseUUID(l.LineID, "line_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		qty, e := decimal.NewFromString(strings.TrimSpace(l.Qty))
		if e != nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not say how many.", i+1))
			return
		}
		quantities = append(quantities, orders.LineQty{LineID: lineID, Qty: qty})
	}

	out, err := record(r.Context(), scope, id, quantities)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleOrderDocument draws a picking slip, a packing slip or a delivery note.
//
// Behind `order.view` rather than `order.manage`: a picker and a driver both
// need to print one, and neither should be able to change a price. The delivery
// note carries no prices at all — B11 is explicit, and the type has no fields
// for them.
func (s *Server) handleOrderDocument(w http.ResponseWriter, r *http.Request) {
	scope, id, err := s.orderScopeAndID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.orders.Documentation(r.Context(), scope, id,
		chi.URLParam(r, "kind"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) orderScopeAndID(
	r *http.Request,
) (orders.Scope, uuid.UUID, error) {
	scope, err := s.orderScope(r)
	if err != nil {
		return orders.Scope{}, uuid.Nil, err
	}
	id, err := parseUUID(chi.URLParam(r, "orderID"), "orderID")
	if err != nil {
		return orders.Scope{}, uuid.Nil, err
	}
	return scope, id, nil
}
