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
	Market     string `json:"market"`
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`

	// The commercial terms. All optional: a client is often taken on before
	// the price is agreed, and the service fills in monthly / 0 / SAR / today
	// rather than refusing. The dates are validated by
	// `billing.ResolvePlanDates`, which is also what the billing screen goes
	// through, so a business signed up here and one edited later cannot end up
	// with differently-shaped terms.
	Cycle     string `json:"cycle"`
	Price     string `json:"price"`
	Currency  string `json:"currency"`
	StartedOn string `json:"started_on"`
	ExpiresOn string `json:"expires_on"`
}

func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	var req createTenantRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Note what is NOT read from the body: the tenant this owner belongs to.
	// It is the tenant this request is about to create, and nothing a caller
	// sends can name a different one -- which is what makes it impossible to
	// attach a new owner to somebody else's business through this route.
	out, err := s.provisioning.CreateTenant(r.Context(), provisioning.NewTenant{
		Name:       req.Name,
		DataRegion: req.DataRegion,
		PlanTier:   req.PlanTier,
		Market:     req.Market,
		OwnerEmail: req.OwnerEmail,
		OwnerName:  req.OwnerName,
		Cycle:      req.Cycle,
		Price:      req.Price,
		Currency:   req.Currency,
		StartedOn:  req.StartedOn,
		ExpiresOn:  req.ExpiresOn,
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

		// What was just committed, so the handover screen can state the terms
		// rather than the operator opening the billing screen to find out what
		// they have sold.
		"plan_tier":  out.PlanTier,
		"cycle":      out.Cycle,
		"price":      out.Price,
		"currency":   out.Currency,
		"started_on": out.StartedOn,
		"expires_on": out.ExpiresOn,
		"login_url":  out.LoginURL,

		// Never "sent". See the MailStatus constants: this says what the
		// deployment will DO with the message, and in every deployment that
		// exists today the answer is that nobody receives it.
		"mail_status": out.MailStatus,

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
