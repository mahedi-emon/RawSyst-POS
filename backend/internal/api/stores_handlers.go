package api

// The branches of the company the caller is in.
//
// # Why this exists at all
//
// Three routes already return a branch list and every one is somebody else's:
// `GET /devices/stores` is "the branches a terminal can be registered in" and is
// gated on `devices.view`; `GET /stock/locations` carries them as a side payload
// behind an inventory permission; `POST /onboarding/stores` creates them during
// setup. None is a general answer to "which branches does this business have",
// and screens outside those three modules need one — an HR screen assigning
// somebody to a branch, a supervisor filtering shifts by branch, a settings
// screen listing them.
//
// Found by building those screens and watching them ask for `/stores`, which
// answered 404. Three branch dropdowns had been written against a route that did
// not exist and could never have filled.
//
// # Merely authenticated, like GET /companies
//
// The same decision, one level down, for the same reason. `GET /companies` is
// `AccessAuthenticated` because "every signed-in user needs to know which
// companies they are in before asking about one; scoped by RLS and the token" —
// and a branch's name and code is not a secret from somebody already signed into
// that company. Row-level security confines it to their tenant and the company
// filter to the company they asked about.
//
// Gating it on a permission would mean choosing one, and every candidate is
// wrong for somebody: `identity.view` is held only by the Owner and the Auditor
// in the base seed, so an HR Manager could not fill a branch dropdown, and
// `devices.view` is about tills. A permission that must be granted to every
// tenant's cloned roles before a dropdown works is the trap 0032 and 0033 fell
// into, and the branding routes were written as they were to avoid it.

import (
	"net/http"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// handleListStores answers which branches this company has.
func (s *Server) handleListStores(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	// The resolver already refuses a company outside the token's scope, with
	// "that company was not found" rather than a forbidden — whether another
	// company exists is not this caller's to learn. Repeating the check here
	// would be a second copy of a rule that has one home.
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Across TENANTS the guard is row-level security rather than the token
	// scope: an id from another business passes the scope check, and the query
	// then simply matches nothing. Empty, never somebody else's branches.
	out, err := s.reports.StoresIn(r.Context(), a.TenantID, companyID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}
