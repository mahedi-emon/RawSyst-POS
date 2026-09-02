package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/live"
)

// The live socket (design 03) and the scrape endpoint.
//
// # The socket carries no permission of its own
//
// It is bound to the caller's OWN tenant, taken from their token, and there is
// no parameter naming another one. What travels down it is a nudge — "stock
// for this variant moved", "a notification arrived" — and every one of them is
// followed by the client reading a real endpoint, where row-level security and
// the permission check still apply.
//
// A push is therefore not an authorisation decision, and treating it as one is
// the mistake this note exists to prevent. Nothing that a permission would
// gate may ever be put in a payload.

// handleLiveSocket upgrades the connection and holds it open.
func (s *Server) handleLiveSocket(w http.ResponseWriter, r *http.Request) {
	if s.live == nil {
		httpx.Error(w, r, errs.New(errs.CodeUnavailable,
			"Live updates are not switched on for this installation."))
		return
	}
	a := actor.From(r.Context())

	// Optional NARROWING, and narrowing only.
	//
	// The tenant comes from the token and is the boundary; this parameter can
	// only ever remove traffic from what the socket would otherwise receive.
	// `CanAccessCompany` is the same gate every other route uses, and for an
	// unscoped owner it answers yes to any id — elsewhere the following query
	// is what refuses another tenant's record, and here there is no following
	// query.
	//
	// That is not a hole: the hub matches on tenant FIRST, so a company id
	// belonging to somebody else matches no message this socket could ever be
	// offered. The worst it produces is a socket that hears less than the
	// caller expected.
	companyID := uuid.Nil
	if raw := r.URL.Query().Get("company_id"); raw != "" {
		id, err := parseUUID(raw, "company_id")
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		if !a.CanAccessCompany(id) {
			httpx.Error(w, r, errs.New(errs.CodeNotFound,
				"That company was not found."))
			return
		}
		companyID = id
	}

	s.live.Serve(w, r, a.TenantID, companyID)
}

// Publish sends a live message, if live push is wired.
//
// On the Server rather than on the hub so every caller is one nil check
// shorter, and so an installation without a hub simply does nothing.
func (s *Server) Publish(
	r *http.Request, tenantID, companyID uuid.UUID, m live.Message,
) {
	if s.live == nil {
		return
	}
	s.live.Publish(r.Context(), tenantID, companyID, m)
}

// handleMetrics serves the Prometheus text.
//
// Guarded by a bearer token inside the handler rather than by the route
// table's access levels, because a scraper is not a person: it has no session
// and cannot hold one of this product's tokens.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		httpx.Error(w, r, errs.New(errs.CodeUnavailable,
			"Metrics are not switched on for this installation."))
		return
	}
	s.metrics.Handler(s.metricsToken).ServeHTTP(w, r)
}

// publishStockMoved tells the other tills what a sale took off the shelf.
//
// One message per line rather than one per sale, because that is the shape a
// till consumes: it holds a quantity per variant and applies a delta to it.
//
// The payload names the variant, the warehouse and the quantity, and nothing
// else. Not the price, not the customer, not the invoice — a push travels to
// every socket in the tenant, and what a client is allowed to see is decided
// by the endpoint it reads next, not by this.
func (s *Server) publishStockMoved(
	r *http.Request, tenantID, warehouseID uuid.UUID, sale sales.Sale,
) {
	if s.live == nil || len(sale.Lines) != len(sale.Input.Lines) {
		return
	}
	for i, line := range sale.Lines {
		s.live.Publish(r.Context(), tenantID, uuid.Nil, live.Message{
			Kind: "stock.moved",
			Payload: map[string]any{
				"variant_id":   line.VariantID.String(),
				"warehouse_id": warehouseID.String(),
				// Negative: a sale takes stock away. Sent as a string for the
				// same reason every other quantity in this API is — a decimal
				// through a JSON number has been through a float64 by the time
				// it arrives.
				"delta": sale.Input.Lines[i].Qty.Neg().String(),
			},
		})
	}
}
