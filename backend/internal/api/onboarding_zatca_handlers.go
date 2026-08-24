package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// ZATCA onboarding, over HTTP.
//
// # What crosses this boundary, and what never does
//
// INWARD: a certificate request, which is public and carries only a public
// key, and a one-time password, which the taxpayer reads from their own
// Fatoora account. The OTP arrives in the body rather than the query string --
// query strings end up in access logs, proxy logs and browser history, and a
// credential has no business in any of them.
//
// OUTWARD: status, the CSID, expiry dates and whatever ZATCA said when it
// refused. Never the secret, and never anything derived from it. The type the
// service returns has no field for it, so this cannot leak one by forgetting
// to filter.
//
// # Why the OTP is not stored, even briefly
//
// It is single-use and expires in minutes. It is read from the body, passed as
// an argument, put in one header and forgotten. There is no column for it and
// no log line that could carry it.

// renewalWindow is how far ahead the status call looks for expiry.
//
// ZATCA publishes no required renewal lead time, so this is a product choice
// rather than a rule, and it is stated as one: thirty days is long enough that
// a shop closed for a fortnight still sees the warning while it can act on it.
const renewalWindow = 30 * 24 * time.Hour

func (s *Server) onboardingUnitID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "unitID"))
	if err != nil {
		return uuid.Nil, errs.New(errs.CodeInvalidInput, "That is not a till identifier.")
	}
	return id, nil
}

// onboardingEnvironment reads the environment and refuses anything else.
//
// Refused rather than defaulted. A default would mean a mistyped environment
// silently onboarded somewhere the shop did not intend -- and the two mistakes
// that matters for are opposite: a production onboarding that lands in the
// sandbox never reports a real invoice, and a sandbox onboarding that lands in
// production consumes the shop's real OTP.
func onboardingEnvironment(raw string) (zatca.Environment, error) {
	env := zatca.Environment(strings.ToLower(strings.TrimSpace(raw)))
	if !env.Valid() {
		return "", errs.New(errs.CodeInvalidInput,
			"Choose the ZATCA environment: sandbox, simulation or production.")
	}
	return env, nil
}

func (s *Server) onboardingAvailable() error {
	if s.onboarding == nil {
		return errs.New(errs.CodeComplianceBlocked,
			"This installation is not configured for e-invoicing onboarding. "+
				"Set RAWSYST_DATA_ENCRYPTION_KEYS so the credential ZATCA issues "+
				"can be stored securely.")
	}
	return nil
}

// handleZATCAOnboardingStatus reports where a till has got to.
//
// Under einvoicing.VIEW rather than onboard, deliberately. 0043's split is
// seeing versus doing: a store manager whose till has stopped selling needs to
// find out that its certificate expired, and making them ask the Owner to look
// is how a shop stays broken for a day.
func (s *Server) handleZATCAOnboardingStatus(w http.ResponseWriter, r *http.Request) {
	if err := s.onboardingAvailable(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	unitID, err := s.onboardingUnitID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	env, err := onboardingEnvironment(r.URL.Query().Get("environment"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	status, err := s.onboarding.Status(r.Context(), unitID, env, renewalWindow)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, status)
}

// handleZATCARequestComplianceCSID performs step 1 of onboarding.
func (s *Server) handleZATCARequestComplianceCSID(w http.ResponseWriter, r *http.Request) {
	if err := s.onboardingAvailable(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	unitID, err := s.onboardingUnitID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var body struct {
		Environment string `json:"environment"`

		// CSR is the certificate request the till produced. Public: it carries
		// the public key and the nine registration fields, never the private
		// half. The private key is generated on the terminal and stays in its
		// OS keystore, which docs/system-design/01-invoice-zatca-engine.md §7
		// records as a locked rule.
		CSR string `json:"csr"`

		// OTP is read from the taxpayer's own Fatoora portal. Never stored.
		OTP string `json:"otp"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.Error(w, r, err)
		return
	}

	env, err := onboardingEnvironment(body.Environment)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	result, err := s.onboarding.RequestComplianceCSID(
		r.Context(), unitID, env, []byte(body.CSR), body.OTP)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"status":     "compliance_csid",
		"csid":       result.Credential.CSID,
		"expires_at": result.Credential.ExpiresAt,
		"request_id": result.RequestID,
	})
}

// handleZATCARequestProductionCSID performs step 2.
func (s *Server) handleZATCARequestProductionCSID(w http.ResponseWriter, r *http.Request) {
	if err := s.onboardingAvailable(); err != nil {
		httpx.Error(w, r, err)
		return
	}
	unitID, err := s.onboardingUnitID(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var body struct {
		Environment string `json:"environment"`
		CSR         string `json:"csr"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.Error(w, r, err)
		return
	}

	env, err := onboardingEnvironment(body.Environment)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// No OTP here, and that is not an omission: the production call
	// authenticates with the compliance credential, which is itself the proof
	// that one was presented.
	result, err := s.onboarding.RequestProductionCSID(
		r.Context(), unitID, env, []byte(body.CSR))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"status":     "production_csid",
		"csid":       result.Credential.CSID,
		"expires_at": result.Credential.ExpiresAt,
		"request_id": result.RequestID,
	})
}
