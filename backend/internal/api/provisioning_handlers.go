package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
)

// --- platform: tenant creation -----------------------------------------

type createTenantRequest struct {
	Name       string `json:"name"`
	DataRegion string `json:"data_region"`
	PlanTier   string `json:"plan_tier"`
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
}

func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	var req createTenantRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.provisioning.CreateTenant(r.Context(), provisioning.NewTenant{
		Name:       req.Name,
		DataRegion: req.DataRegion,
		PlanTier:   req.PlanTier,
		OwnerEmail: req.OwnerEmail,
		OwnerName:  req.OwnerName,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"tenant_id":          out.TenantID,
		"owner_user_id":      out.OwnerUserID,
		"owner_email":        out.OwnerEmail,
		"temporary_password": out.TemporaryPassword,
		"detail": "Give the owner their email and this temporary password. They must " +
			"change it when they first sign in. It is shown once and is not stored " +
			"anywhere in readable form.",
	})
}

// --- onboarding wizard --------------------------------------------------

func (s *Server) handleOnboardingProgress(w http.ResponseWriter, r *http.Request) {
	p, err := s.provisioning.GetProgress(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

func (s *Server) handleOnboardingSaveStep(w http.ResponseWriter, r *http.Request) {
	step := chi.URLParam(r, "step")

	// The step payload is free-form by design — each step carries different
	// answers — so it is read as raw JSON rather than decoded into a struct.
	// The size cap still applies; only the shape is open.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		httpx.Error(w, r, errs.Wrap(err, errs.CodeInvalidInput,
			"That step's answers could not be read."))
		return
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	if !json.Valid(body) {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"That step's answers are not valid JSON."))
		return
	}

	if err := s.provisioning.SaveStep(r.Context(), step, body); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Server) handleOnboardingCompleteStep(w http.ResponseWriter, r *http.Request) {
	step := chi.URLParam(r, "step")

	p, err := s.provisioning.CompleteStep(r.Context(), step)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

func (s *Server) handleOnboardingCommitCompany(w http.ResponseWriter, r *http.Request) {
	companyID, err := s.provisioning.CommitBusinessInfo(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"company_id": companyID})
}

// handleOnboardingCommitStores turns the wizard's store answers into branches.
//
// Separate from the company commit because a tenant can add branches later, and
// because the two fail for different reasons: a company is refused by the plan's
// company ceiling, a store by its store ceiling or by an incomplete National
// Address.
func (s *Server) handleOnboardingCommitStores(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CompanyID string `json:"company_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.Error(w, r, err)
		return
	}

	companyID, err := uuid.Parse(body.CompanyID)
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say which company these stores belong to."))
		return
	}

	ids, err := s.provisioning.CommitStores(r.Context(), companyID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"store_ids": ids})
}
