package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/branding"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// Document templates, blueprint I2 / P35.
//
// The company is a path parameter for the same reason the logo's is: these are
// back-office routes and a browser has no device to resolve one from. Row-level
// security is what makes naming it safe.

type saveTemplateRequest struct {
	HeaderText     string `json:"header_text"`
	HeaderTextAr   string `json:"header_text_ar"`
	FooterText     string `json:"footer_text"`
	FooterTextAr   string `json:"footer_text_ar"`
	ReturnPolicy   string `json:"return_policy"`
	ReturnPolicyAr string `json:"return_policy_ar"`
	PaymentTerms   string `json:"payment_terms"`
	PaymentTermsAr string `json:"payment_terms_ar"`

	ShowLogo      bool `json:"show_logo"`
	ShowTaxNumber bool `json:"show_tax_number"`
}

// --- GET /api/v1/companies/{companyID}/templates ------------------------

// Every configurable type, whether or not it has been set.
//
// Merely authenticated, like the logo image and for the same reason: a template
// is the shop's own stationery and is destined for every document it prints, so
// a till or a cashier reading an invoice has to be able to fetch it. Gating it
// behind a settings permission would put the gate in the wrong place. RLS
// confines it to the caller's own tenant, which is the part that matters.
func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	scope, err := s.logoScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	templates, err := s.branding.Templates(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": templates})
}

// --- PUT /api/v1/companies/{companyID}/templates/{docType} --------------

func (s *Server) handleSaveTemplate(w http.ResponseWriter, r *http.Request) {
	var req saveTemplateRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	scope, err := s.logoScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	saved, err := s.branding.SaveTemplate(r.Context(), scope, branding.Template{
		DocType:        chi.URLParam(r, "docType"),
		HeaderText:     req.HeaderText,
		HeaderTextAr:   req.HeaderTextAr,
		FooterText:     req.FooterText,
		FooterTextAr:   req.FooterTextAr,
		ReturnPolicy:   req.ReturnPolicy,
		ReturnPolicyAr: req.ReturnPolicyAr,
		PaymentTerms:   req.PaymentTerms,
		PaymentTermsAr: req.PaymentTermsAr,
		ShowLogo:       req.ShowLogo,
		ShowTaxNumber:  req.ShowTaxNumber,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, saved)
}

// --- DELETE /api/v1/companies/{companyID}/templates/{docType} -----------

// Resetting returns the type to the RawSyst default. Resetting one that was
// never customised succeeds: the client asked for the default and has it.
func (s *Server) handleResetTemplate(w http.ResponseWriter, r *http.Request) {
	scope, err := s.logoScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.branding.ResetTemplate(r.Context(), scope,
		chi.URLParam(r, "docType")); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}
