package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/egs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// EGS units — the signing units a terminal's invoice chain belongs to.
//
// Four routes and no more. There is no route that onboards a unit, requests a
// CSID or submits anything, because every one of those needs byte-level formats
// ZATCA has published and this repository has not yet verified. The CSID fields
// travel outward on every response and inward on none.

func (s *Server) egsScope(r *http.Request) (egs.Scope, error) {
	a := actor.From(r.Context())

	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return egs.Scope{}, err
	}
	return egs.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// csrRequest mirrors egs.CSR rather than reusing it, so the wire shape is
// visible in this file and a rename in the service cannot quietly change the
// API. The nine names are ZATCA's, from Technical Guideline V2 §3.3.3.
type csrRequest struct {
	CommonName             string `json:"common_name"`
	EGSSerialNumber        string `json:"egs_serial_number"`
	OrganizationIdentifier string `json:"organization_identifier"`
	OrganizationUnit       string `json:"organization_unit"`
	OrganizationName       string `json:"organization_name"`
	Country                string `json:"country"`
	InvoiceType            string `json:"invoice_type"`
	Location               string `json:"location"`
	Industry               string `json:"industry"`
}

func (c csrRequest) toCSR() egs.CSR {
	return egs.CSR{
		CommonName:             c.CommonName,
		EGSSerialNumber:        c.EGSSerialNumber,
		OrganizationIdentifier: c.OrganizationIdentifier,
		OrganizationUnit:       c.OrganizationUnit,
		OrganizationName:       c.OrganizationName,
		Country:                c.Country,
		InvoiceType:            c.InvoiceType,
		Location:               c.Location,
		Industry:               c.Industry,
	}
}

type egsUnitRequest struct {
	Label string `json:"label"`
	// Ignored when amending: the architecture decides where the signing key
	// lives, and a unit that has one cannot be told it lives somewhere else.
	Architecture string     `json:"architecture"`
	StoreID      string     `json:"store_id"`
	CSR          csrRequest `json:"csr"`
}

func (s *Server) handleListEGSUnits(w http.ResponseWriter, r *http.Request) {
	scope, err := s.egsScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.egs.List(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleReadEGSUnit(w http.ResponseWriter, r *http.Request) {
	unitID, err := parseUUID(chi.URLParam(r, "unitID"), "unitID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.egsScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.egs.Read(r.Context(), scope, unitID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateEGSUnit(w http.ResponseWriter, r *http.Request) {
	var req egsUnitRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.egsScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// Optional here because a central unit has no branch, and the service
	// decides which architectures require one.
	storeID, err := parseOptionalUUID(req.StoreID, "store_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.egs.Create(r.Context(), scope, egs.NewUnit{
		Label:        req.Label,
		Architecture: req.Architecture,
		StoreID:      storeID,
		CSR:          req.CSR.toCSR(),
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleAmendEGSUnit(w http.ResponseWriter, r *http.Request) {
	unitID, err := parseUUID(chi.URLParam(r, "unitID"), "unitID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req egsUnitRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.egsScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	storeID, err := parseOptionalUUID(req.StoreID, "store_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.egs.Amend(r.Context(), scope, unitID, egs.Amendment{
		Label:   req.Label,
		StoreID: storeID,
		CSR:     req.CSR.toCSR(),
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
