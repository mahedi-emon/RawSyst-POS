// Opening a POS counter from a browser.
//
// # The model
//
//	business (tenant) -> company -> shop (store) -> counter -> session -> sale
//
// A shop has as many counters as it has places to stand. Each is a `device`
// row, each keeps its own shift, its own drawer and its own numbering, and two
// cashiers on two counters in the same shop work at the same time without
// meeting: everything downstream is already keyed by device, so concurrency
// here is a property of the existing model rather than something these two
// routes add.
//
// # Why a counter is not named in a request body
//
// Every POS route reads the till from `did` in the token and never from what
// the request says. A cashier who could name a counter could ring a sale onto
// another counter's shift, another shop's stock, and — where the market has one
// — another terminal's invoice chain, all inside their own tenant where
// row-level security has no reason to object.
//
// So the counter is chosen ONCE, here, against the caller's own grants, and
// from then on it is in the token the server signed. That is the same rule the
// paired desktop till follows; these routes only give a browser a way to reach
// it that does not involve holding a machine secret it has nowhere safe to put.
package api

import (
	"net/http"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/devices"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// handleListCounters answers "which counter am I about to stand at?"
//
// Filtered by store scope in this layer rather than in SQL, because the grants
// live on the request. A branch manager scoped to one shop sees that shop's
// counters and is not told the others exist.
func (s *Server) handleListCounters(w http.ResponseWriter, r *http.Request) {
	scope, err := s.deviceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	all, err := s.devices.Counters(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out := make([]any, 0, len(all))
	for _, c := range all {
		if identity.CheckStoreScope(r.Context(), c.StoreID) != nil {
			continue
		}
		out = append(out, c)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type openCounterRequest struct {
	DeviceID string `json:"device_id"`
}

// handleOpenCounterSession binds the caller's session to a counter.
//
// The four checks below are in the order that reveals least. Company scope
// first, because a counter in a company this user cannot see should read as
// absent rather than as forbidden; then the counter itself, which refuses a
// paired one by name; then store scope; and only then is a token minted.
func (s *Server) handleOpenCounterSession(w http.ResponseWriter, r *http.Request) {
	var req openCounterRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	deviceID, err := parseUUID(req.DeviceID, "device_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The company comes from the COUNTER, not from a query parameter.
	//
	// The caller has already said which counter, and a counter belongs to
	// exactly one company — so asking them to name the company as well would be
	// asking for a fact the server can look up, and would give them a way to
	// name a different one from the counter's. Access to that company is then
	// checked below rather than assumed.
	a := actor.From(r.Context())
	companyID, err := s.catalog.CompanyForDevice(r.Context(), a.TenantID, deviceID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !a.CanAccessCompany(companyID) {
		// Not found rather than forbidden: a user confined to one company
		// should not learn which counters exist in another by probing ids.
		httpx.Error(w, r, errs.New(errs.CodeNotFound, "That counter was not found."))
		return
	}

	scope := devices.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}

	counter, err := s.devices.Openable(r.Context(), scope, deviceID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := identity.CheckStoreScope(r.Context(), counter.StoreID); err != nil {
		httpx.Error(w, r, err)
		return
	}

	session, err := s.auth.OpenCounterSession(a, counter.ID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The counter is echoed back so the screen can name what it opened without
	// a second round trip, and so a cashier can see at a glance that they are
	// standing at Till 2 and not Till 1.
	httpx.JSON(w, http.StatusOK, map[string]any{
		"access_token": session.AccessToken,
		"expires_at":   session.ExpiresAt,
		"counter":      counter,
	})
}
