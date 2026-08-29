// Staff management, over HTTP.
//
// Blueprint A5 makes the Owner "fully self-sufficient" after Super Admin
// creates their first login, and A6 gives them the model to delegate with. The
// database carried all of it — twelve seeded roles, four scope dimensions on
// every assignment — and none of it had a route, so a shop that onboarded had
// no way to create the cashier who works the till.
//
// # The permission split, and why it is three verbs rather than one
//
// `identity.view` reads the list. `identity.create` adds and edits a person.
// `identity.manage_roles` decides what they may do. They are separate because
// they are separately dangerous: an office manager can reasonably be trusted to
// keep a staff list current without also being able to hand somebody the
// ability to see the bank ledger.
package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// peopleScope carries who is asking AND what they themselves hold.
//
// The second half is what makes delegation safe. `checkRoleIsGrantable`
// compares the role being handed over against the caller's own permission set,
// so a store manager with `identity.manage_roles` can create staff and cannot
// create an Owner. Resolving it here rather than inside the service keeps the
// service testable without a request.
func peopleScope(r *http.Request) (identity.PeopleScope, error) {
	a := actor.From(r.Context())
	if a.TenantID == uuid.Nil || a.UserID == uuid.Nil {
		return identity.PeopleScope{}, errs.New(errs.CodeForbidden,
			"This action is taken by a person, and this request is not signed "+
				"in as one.")
	}

	grants := identity.GrantsFrom(r.Context())
	holds := map[string]bool{}
	for _, p := range grants.Permissions() {
		holds[p] = true
	}

	return identity.PeopleScope{
		TenantID: a.TenantID,
		ActorID:  a.UserID,
		Holds:    holds,
	}, nil
}

// --- GET /api/v1/people ------------------------------------------------

func (s *Server) handleListPeople(w http.ResponseWriter, r *http.Request) {
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Somebody who has left is `disabled` rather than deleted, so they stay
	// off the list unless asked for. Their name is still on the invoices they
	// rang up and the shifts they counted, which is the point.
	includeInactive := r.URL.Query().Get("include_inactive") == "true"

	out, err := s.auth.ListPeople(r.Context(), scope, includeInactive)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- GET /api/v1/people/roles ------------------------------------------

func (s *Server) handleListRoles(w http.ResponseWriter, r *http.Request) {
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.auth.ListRoles(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// personRequest is a member of staff being created or edited.
type personRequest struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`

	RoleID       string   `json:"role_id"`
	CompanyID    string   `json:"company_id"`
	StoreIDs     []string `json:"store_ids"`
	WarehouseIDs []string `json:"warehouse_ids"`
	AmountLimit  string   `json:"amount_limit"`
	ValidFrom    string   `json:"valid_from"`
	ValidUntil   string   `json:"valid_until"`
}

/** parseScopes turns the request's ids and dates into what the service takes. */
func (req personRequest) parseScopes() (
	companyID *uuid.UUID,
	stores, warehouses []uuid.UUID,
	from, until *time.Time,
	err error,
) {
	if v := strings.TrimSpace(req.CompanyID); v != "" {
		id, e := parseUUID(v, "company_id")
		if e != nil {
			return nil, nil, nil, nil, nil, e
		}
		companyID = &id
	}
	for _, raw := range req.StoreIDs {
		id, e := parseUUID(raw, "store_ids")
		if e != nil {
			return nil, nil, nil, nil, nil, e
		}
		stores = append(stores, id)
	}
	for _, raw := range req.WarehouseIDs {
		id, e := parseUUID(raw, "warehouse_ids")
		if e != nil {
			return nil, nil, nil, nil, nil, e
		}
		warehouses = append(warehouses, id)
	}
	if v := strings.TrimSpace(req.ValidFrom); v != "" {
		t, e := time.Parse(time.RFC3339, v)
		if e != nil {
			return nil, nil, nil, nil, nil, errs.Validation("Some details are missing.").
				WithField("valid_from", "Give the date this role starts.")
		}
		from = &t
	}
	if v := strings.TrimSpace(req.ValidUntil); v != "" {
		t, e := time.Parse(time.RFC3339, v)
		if e != nil {
			return nil, nil, nil, nil, nil, errs.Validation("Some details are missing.").
				WithField("valid_until", "Give the date this role ends.")
		}
		until = &t
	}
	return companyID, stores, warehouses, from, until, nil
}

// --- POST /api/v1/people -----------------------------------------------

func (s *Server) handleCreatePerson(w http.ResponseWriter, r *http.Request) {
	var req personRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Creating somebody assigns them a role, and assigning a role is its own
	// permission. Without this check `identity.create` alone would let a
	// person hand out any role in the tenant, which is the escalation the
	// subset rule exists to prevent — reached by the other door.
	if !identity.GrantsFrom(r.Context()).Can("identity.manage_roles") {
		httpx.Error(w, r, errs.New(errs.CodeForbidden,
			"Adding somebody means deciding what they may do, and that is not "+
				"something your role allows. Ask an owner."))
		return
	}

	roleID, err := parseUUID(req.RoleID, "role_id")
	if err != nil {
		httpx.Error(w, r, errs.Validation("Some details are missing.").
			WithField("role_id", "Choose what this person is allowed to do."))
		return
	}

	companyID, stores, warehouses, from, until, err := req.parseScopes()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.auth.CreatePerson(r.Context(), scope, identity.NewPerson{
		Email:        req.Email,
		FullName:     req.FullName,
		Phone:        req.Phone,
		RoleID:       roleID,
		CompanyID:    companyID,
		StoreIDs:     stores,
		WarehouseIDs: warehouses,
		AmountLimit:  strings.TrimSpace(req.AmountLimit),
		ValidFrom:    from,
		ValidUntil:   until,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"data": out})
}

// --- PUT /api/v1/people/{userID} ---------------------------------------

func (s *Server) handleUpdatePerson(w http.ResponseWriter, r *http.Request) {
	userID, err := parseUUID(chi.URLParam(r, "userID"), "userID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req personRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.auth.UpdatePerson(r.Context(), scope, userID,
		req.FullName, req.Phone, req.Email); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- POST /api/v1/people/{userID}/active -------------------------------

func (s *Server) handleSetPersonActive(w http.ResponseWriter, r *http.Request) {
	userID, err := parseUUID(chi.URLParam(r, "userID"), "userID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req struct {
		Active bool `json:"active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.auth.SetPersonStatus(r.Context(), scope, userID, req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- POST /api/v1/people/{userID}/reset-password -----------------------

func (s *Server) handleResetPersonPassword(w http.ResponseWriter, r *http.Request) {
	userID, err := parseUUID(chi.URLParam(r, "userID"), "userID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	temporary, err := s.auth.ResetPersonPassword(r.Context(), scope, userID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// Shown once, in this response, and never retrievable afterwards. The
	// stored form is an argon2id hash — A4.2 calls the irreversibility "a
	// security requirement, not just a policy choice".
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]string{"temporary_password": temporary},
	})
}

// --- POST /api/v1/people/{userID}/roles --------------------------------

func (s *Server) handleAssignRole(w http.ResponseWriter, r *http.Request) {
	userID, err := parseUUID(chi.URLParam(r, "userID"), "userID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req personRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	roleID, err := parseUUID(req.RoleID, "role_id")
	if err != nil {
		httpx.Error(w, r, errs.Validation("Some details are missing.").
			WithField("role_id", "Choose a role."))
		return
	}
	companyID, stores, warehouses, from, until, err := req.parseScopes()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	if err := s.auth.AssignRoleTo(r.Context(), scope, userID, identity.RoleGrant{
		RoleID:       roleID,
		CompanyID:    companyID,
		StoreIDs:     stores,
		WarehouseIDs: warehouses,
		AmountLimit:  strings.TrimSpace(req.AmountLimit),
		ValidFrom:    from,
		ValidUntil:   until,
	}); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- DELETE /api/v1/people/roles/{assignmentID} ------------------------

func (s *Server) handleRemoveAssignment(w http.ResponseWriter, r *http.Request) {
	assignmentID, err := parseUUID(chi.URLParam(r, "assignmentID"), "assignmentID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := peopleScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.auth.RemoveAssignment(r.Context(), scope, assignmentID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
