package api

// Business settings, blueprint I1.
//
// The company record and its branches, after setup rather than during it. The
// wizard's routes under /onboarding write the scratch answers a new business
// gives once; these are the routes it lives with afterwards.
//
// # Why the company is a path parameter here
//
// The same reason the logo and template routes take one: these are back-office
// routes, a browser has no terminal to resolve a company from, and a tenant may
// hold more than one company. Row-level security is what makes naming it safe —
// another tenant's company reads as absent rather than as forbidden, so nobody
// learns from a refusal that a company exists.
//
// # The permissions, and why they are the ones company setup already uses
//
// Reading is `identity.view` and writing `identity.edit`, which is exactly what
// /onboarding/company and the branding routes carry. There is deliberately no
// `settings.*` verb: a new permission would have to be granted to every
// tenant's already-cloned roles before anybody could open the screen, which is
// the trap 0032 and 0033 fell into and which the branding routes were written
// to avoid.
//
// Branches are the same pair. A branch is company setup — its code goes into
// every document number, its address onto every invoice — so it takes the
// authority that changes company setup, not `devices.manage`, which is about
// tills.

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// companyPathScope reads {companyID} and confines it to the caller's scope.
//
// The refusal is "not found" rather than "forbidden", matching logoScope: which
// companies exist beyond the caller's own is not something a refusal should
// teach. Across tenants the guard is row-level security instead, and the read
// simply matches nothing.
func (s *Server) companyPathScope(r *http.Request) (uuid.UUID, error) {
	companyID, err := parseUUID(chi.URLParam(r, "companyID"), "company_id")
	if err != nil {
		return uuid.Nil, err
	}
	if !actor.From(r.Context()).CanAccessCompany(companyID) {
		return uuid.Nil, errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return companyID, nil
}

// --- GET /api/v1/companies/{companyID} ----------------------------------

// The business record, with what may still be changed about it.
//
// The branches come with it. A settings screen shows the company and its
// branches together, and the alternative — a list call plus one detail call per
// branch — is the thirteen-round-trip shape that GET /people/roles was fixed
// for in this same session's item 12.
func (s *Server) handleReadBusiness(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	companyID, err := s.companyPathScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	business, err := s.provisioning.ReadBusiness(r.Context(), a.TenantID, companyID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	branches, err := s.provisioning.Branches(r.Context(), a.TenantID, companyID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"business": business,
		"branches": branches,
	})
}

// --- PUT /api/v1/companies/{companyID} ----------------------------------

// A partial amendment to the business record.
//
// Answers the record as it now stands, including a freshly computed `settled`,
// so a screen never has to guess what its save did — and so that a field which
// became settled between the read and the write is reported as such rather than
// left looking editable until the next reload.
func (s *Server) handleAmendBusiness(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	companyID, err := s.companyPathScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var req provisioning.BusinessChange
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	business, err := s.provisioning.AmendBusiness(r.Context(), a.TenantID, companyID, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"business": business})
}

// --- POST /api/v1/companies/{companyID}/branches ------------------------

// Opens a branch.
//
// Under the company rather than at /stores, because a branch has no meaning
// apart from the company it belongs to and the ceiling that governs it is the
// company's plan. `GET /stores` stays where it is: it answers a different
// question, for a different audience, at a different permission.
func (s *Server) handleOpenBranch(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	companyID, err := s.companyPathScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var req provisioning.BranchChange
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	branch, err := s.provisioning.OpenBranch(r.Context(), a.TenantID, companyID, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"branch": branch})
}

// --- PUT /api/v1/companies/{companyID}/branches/{branchID} --------------

// Amends a branch, including closing and reopening it.
//
// There is no DELETE. A branch's name is on every invoice it ever issued and
// its code is inside those invoices' document numbers, so "we do not trade here
// any more" is `is_active`, not a removed row.
func (s *Server) handleAmendBranch(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())
	companyID, err := s.companyPathScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	branchID, err := parseUUID(chi.URLParam(r, "branchID"), "branch_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var req provisioning.BranchChange
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	branch, err := s.provisioning.AmendBranch(
		r.Context(), a.TenantID, companyID, branchID, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"branch": branch})
}
