package api

import (
	"bufio"
	"net"
	"net/http"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
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

// announceStockMovements tells the tills what moved on the main stock ledger.
//
// # Wrapped around every route, not called from one handler
//
// The main stock ledger is the single source of truth and a till's figure is a
// cache of it, so every authoritative mutation has to be announced or the
// caches drift silently.
//
// The first version of this was a call in the sale handler. It covered an
// online sale and missed every other way stock moves — an OFFLINE sale replayed
// by the sync engine on reconnect, a goods receipt, an adjustment, both halves
// of a transfer, a return, a part issued to a repair. `inventory.Consume` alone
// has five callers.
//
// So the recording happens at the ledger itself (see inventory/observed.go) and
// this drains what was recorded. Adding a route that moves stock now announces
// it without anybody remembering to.
//
// # After the response, and only a successful one
//
// The handler has returned by the time this drains, so its transaction has
// committed. A non-2xx rolled back, and a push about stock that did not move
// would never be corrected.
//
// The payload names the variant, the warehouse and the quantity, and nothing
// else. Not the cost, not the customer, not the document — a push travels to
// every socket in the tenant, and what a client may SEE is decided by the
// endpoint it reads next, not by this.
func (s *Server) announceStockMovements(next http.Handler) http.Handler {
	if s.live == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, collector := inventory.Collecting(r.Context())
		recorder := &statusWatcher{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r.WithContext(ctx))

		// Only on success. A handler that answered 4xx or 5xx rolled its
		// transaction back, and a push about stock that did not move would
		// never be corrected -- there is no second event to say the sale came
		// undone.
		if recorder.status < 200 || recorder.status >= 300 {
			return
		}

		for _, m := range collector.Drain() {
			s.live.Publish(r.Context(), m.TenantID, m.CompanyID, live.Message{
				Kind: "stock.moved",
				Payload: map[string]any{
					"variant_id":   m.VariantID.String(),
					"warehouse_id": m.WarehouseID.String(),
					// A string, for the same reason every other quantity in
					// this API is one: a decimal sent as a JSON number has been
					// through a float64 by the time it arrives.
					"delta": m.Delta.String(),
				},
			})
		}
	})
}

// statusWatcher remembers what was written, and passes through the optional
// interfaces a real writer implements.
//
// The passthroughs are not optional politeness: this wraps every route, the
// live socket included, and a wrapper that hid http.Hijacker would refuse every
// upgrade with an error naming nothing recognisable. The same trap as the
// metrics middleware, and the same fix.
type statusWatcher struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusWatcher) WriteHeader(code int) {
	if !s.written {
		s.status = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWatcher) Write(b []byte) (int, error) {
	s.written = true
	return s.ResponseWriter.Write(b)
}

func (s *statusWatcher) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWatcher) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func (s *statusWatcher) Unwrap() http.ResponseWriter { return s.ResponseWriter }
