package api

// Two lists a back office could not get at.
//
// The till routes are built for a till: `GET /shifts/current` needs a token
// bound to a terminal, and `GET /shifts/{sessionID}` needs an id only the till
// that opened it has ever held. So the variance on last night's drawer — the
// single signal a blind close exists to produce — was reachable from nowhere
// but the till that produced it, and only while it was still standing there. A
// supervisor reviewing the morning after had no route at all.
//
// The wallets were the same shape of gap in the other direction: one customer
// at a time, so the total a business owes in store credit could only be found
// by asking about every customer in turn.
//
// Both found by building the screens the navigation had always promised.

import (
	"net/http"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// handleListShifts is the supervisor's register of tills.
//
// Behind `report.view`, the same permission as the X report and for the same
// reason: it shows the expected figure and the variance beside it, and a
// cashier who can read those can make tonight's drawer agree with the screen.
// The Cashier role holds `sales.receive_payment` and not this.
func (s *Server) handleListShifts(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	storeID, err := optionalUUID(r.URL.Query().Get("store_id"), "store_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// A fortnight, when nobody says -- but the default is left to the service,
	// which resolves it in the SHOP's timezone.
	//
	// Computing it here meant computing it in UTC, and a session opened after
	// local midnight but before UTC midnight then fell outside its own day: in
	// Riyadh every shift opened between midnight and three was missing from
	// the register that exists to review it.
	from, err := optionalReportDate(r.URL.Query().Get("from"), "from")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	to, err := optionalReportDate(r.URL.Query().Get("to"), "to")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if from != nil && to != nil && to.Before(*from) {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"The end of the period comes before its start."))
		return
	}

	out, err := s.shift.Sessions(r.Context(), a.TenantID, companyID,
		storeID, from, to, 0)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// handleListWallets is what the business owes in store credit, whole.
func (s *Server) handleListWallets(w http.ResponseWriter, r *http.Request) {
	scope, err := s.walletScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.wallet.Wallets(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}
