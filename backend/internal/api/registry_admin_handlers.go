// The Platform Owner's tax and legal-value screens (A4, E8).
//
// Global rather than per tenant, and Super Admin only: a VAT rate, a social
// insurance schedule and a city's sales tax are the law, not one business's
// settings. A tenant that could edit them would be choosing what it owes.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/billing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	out, err := s.rules.Rules(r.Context(), r.URL.Query().Get("country"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type recordRuleRequest struct {
	Key       string          `json:"rule_key"`
	Country   string          `json:"country"`
	Payload   json.RawMessage `json:"payload"`
	From      string          `json:"effective_from"`
	Authority string          `json:"source_authority"`
	Document  string          `json:"source_document"`
	URL       string          `json:"source_url"`
	Blocker   bool            `json:"release_blocker"`
	Notes     string          `json:"notes"`

	// Verified is the caller putting their name to the figure. Recording
	// without it stages a value for somebody else to confirm.
	Verified bool `json:"verified"`
}

func (s *Server) handleRecordRule(w http.ResponseWriter, r *http.Request) {
	var req recordRuleRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, err := optionalDate(req.From, "effective_from")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if from == nil {
		httpx.Error(w, r, errs.Validation("Say when this takes effect.").
			WithField("effective_from",
				"A legal value applies from a date, and the product resolves "+
					"it at the date of the document being processed."))
		return
	}

	a := actor.From(r.Context())
	out, err := s.rules.RecordRule(r.Context(), registry.NewRule{
		Key: req.Key, Country: req.Country, Payload: req.Payload,
		From: *from, Authority: req.Authority, Document: req.Document,
		URL: req.URL, Blocker: req.Blocker, Notes: req.Notes,
		Verified: req.Verified,
	}, a.UserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"rule": out})
}

func (s *Server) handleListJurisdictions(w http.ResponseWriter, r *http.Request) {
	out, err := s.rules.Jurisdictions(r.Context(), r.URL.Query().Get("country"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type saveJurisdictionRequest struct {
	ParentID    string `json:"parent_id"`
	Country     string `json:"country"`
	Level       string `json:"level"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	OriginBased *bool  `json:"is_origin_based"`
}

func (s *Server) handleSaveJurisdiction(w http.ResponseWriter, r *http.Request) {
	var req saveJurisdictionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	in := registry.NewJurisdiction{
		Country: req.Country, Level: req.Level, Code: req.Code,
		Name: req.Name, OriginBased: req.OriginBased,
	}
	if req.ParentID != "" {
		parent, e := parseUUID(req.ParentID, "parent_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.ParentID = &parent
	}

	out, err := s.rules.SaveJurisdiction(r.Context(), in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"jurisdiction": out})
}

type recordRateRequest struct {
	Treatment string `json:"treatment"`
	Rate      string `json:"rate"`
	From      string `json:"effective_from"`
	Authority string `json:"source_authority"`
	Document  string `json:"source_document"`
	URL       string `json:"source_url"`
	Notes     string `json:"notes"`
	Verified  bool   `json:"verified"`
}

func (s *Server) handleRecordJurisdictionRate(
	w http.ResponseWriter, r *http.Request,
) {
	jurisdictionID, err := parseUUID(
		chi.URLParam(r, "jurisdictionID"), "jurisdictionID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req recordRateRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	rate, rerr := decimal.NewFromString(req.Rate)
	if rerr != nil {
		httpx.Error(w, r, errs.Validation("That rate is not a number.").
			WithField("rate", "0.0725 is 7.25 per cent."))
		return
	}
	from, err := optionalDate(req.From, "effective_from")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if from == nil {
		httpx.Error(w, r, errs.Validation("Say when this rate takes effect.").
			WithField("effective_from",
				"A sale is taxed at the rate in force on the day it was made."))
		return
	}

	a := actor.From(r.Context())
	if err := s.rules.RecordJurisdictionRate(r.Context(),
		registry.NewJurisdictionRate{
			JurisdictionID: jurisdictionID, Treatment: req.Treatment,
			Rate: rate, From: *from, Authority: req.Authority,
			Document: req.Document, URL: req.URL, Notes: req.Notes,
			Verified: req.Verified,
		}, a.UserID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var _ = time.Now

type importRowRequest struct {
	Level      string `json:"level"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	ParentCode string `json:"parent_code"`
	Rate       string `json:"rate"`
}

type importRatesRequest struct {
	Country   string             `json:"country"`
	Treatment string             `json:"treatment"`
	From      string             `json:"effective_from"`
	Authority string             `json:"source_authority"`
	Document  string             `json:"source_document"`
	URL       string             `json:"source_url"`
	Notes     string             `json:"notes"`
	Verified  bool               `json:"verified"`
	Rows      []importRowRequest `json:"rows"`
}

// handleImportRates loads a published rate schedule in one transaction.
//
// The shape a state's own publication is converted into. CDTFA issues a
// spreadsheet each quarter rather than an API, so the conversion is a step
// somebody performs; what the product owes them is a way to apply the result
// atomically, with the source attached and nothing invented.
func (s *Server) handleImportRates(w http.ResponseWriter, r *http.Request) {
	var req importRatesRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, err := optionalDate(req.From, "effective_from")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if from == nil {
		httpx.Error(w, r, errs.Validation(
			"Say when this schedule takes effect.").
			WithField("effective_from",
				"A sale is taxed at the rate in force on the day it was made."))
		return
	}

	in := registry.Import{
		Country: req.Country, Treatment: req.Treatment, From: *from,
		Authority: req.Authority, Document: req.Document, URL: req.URL,
		Notes: req.Notes, Verified: req.Verified,
	}
	for i, row := range req.Rows {
		rate, rerr := decimal.NewFromString(row.Rate)
		if rerr != nil {
			httpx.Error(w, r, errs.Validation(
				fmt.Sprintf("Row %d has a rate that is not a number.", i+1)).
				WithField("rate", "0.0725 is 7.25 per cent."))
			return
		}
		in.Rows = append(in.Rows, registry.ImportRow{
			Level: row.Level, Code: row.Code, Name: row.Name,
			ParentCode: row.ParentCode, Rate: rate,
		})
	}

	a := actor.From(r.Context())
	out, err := s.rules.ImportRates(r.Context(), in, a.UserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"result": out})
}

type setLimitsRequest struct {
	MaxCompanies   *int `json:"max_companies"`
	MaxStores      *int `json:"max_stores"`
	MaxUsers       *int `json:"max_users"`
	MaxTerminals   *int `json:"max_terminals"`
	MaxSKUs        *int `json:"max_skus"`
	MaxCustomRoles *int `json:"max_custom_roles"`
	MaxStorageMB   *int `json:"max_storage_mb"`
	SMSCredits     *int `json:"sms_credits"`
}

func (s *Server) handleTenantLimits(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseUUID(chi.URLParam(r, "tenantID"), "tenantID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.billing.TenantLimits(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"limits": out})
}

// handleSetTenantLimits raises or lowers what a business is allowed.
//
// `tenant_limit` is enforced everywhere — provisioning refuses a second company
// on a one-company plan, identity refuses the sixth user on a plan that sells
// five — and it was written once at signup and then unreachable. A tenant that
// upgraded could not be given the headroom they had paid for.
func (s *Server) handleSetTenantLimits(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseUUID(chi.URLParam(r, "tenantID"), "tenantID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req setLimitsRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	out, err := s.billing.SetLimits(r.Context(), tenantID, a.UserID,
		billing.LimitChange{
			MaxCompanies: req.MaxCompanies, MaxStores: req.MaxStores,
			MaxUsers: req.MaxUsers, MaxTerminals: req.MaxTerminals,
			MaxSKUs: req.MaxSKUs, MaxCustomRoles: req.MaxCustomRoles,
			MaxStorageMB: req.MaxStorageMB, SMSCredits: req.SMSCredits,
		})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"limits": out})
}

// --- getting an imported schedule into production ---------------------------

func (s *Server) handleRateBatches(w http.ResponseWriter, r *http.Request) {
	out, err := s.rules.RateBatches(r.Context(), r.URL.Query().Get("country"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type batchRequest struct {
	Country   string `json:"country"`
	Document  string `json:"source_document"`
	Treatment string `json:"treatment"`
	From      string `json:"effective_from"`
	Note      string `json:"note"`
}

func (s *Server) batchRef(r *http.Request, req batchRequest) (
	registry.BatchRef, error,
) {
	from, err := optionalDate(req.From, "effective_from")
	if err != nil {
		return registry.BatchRef{}, err
	}
	if from == nil {
		return registry.BatchRef{}, errs.Validation(
			"Say which effective date this schedule is.").
			WithField("effective_from",
				"One authority publishes many schedules; the date is what "+
					"tells them apart.")
	}
	return registry.BatchRef{
		Country: req.Country, Document: req.Document,
		Treatment: req.Treatment, From: *from,
	}, nil
}

// handleReviewRates records that somebody checked a schedule against its source.
func (s *Server) handleReviewRates(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	ref, err := s.batchRef(r, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	n, err := s.rules.ReviewRates(r.Context(), ref, a.UserID, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"reviewed": n})
}

// handleVerifyRates signs a reviewed schedule off, so shops can charge it.
//
// Refused if the caller is the one who reviewed it: two people look at a tax
// rate before a customer is charged it.
func (s *Server) handleVerifyRates(w http.ResponseWriter, r *http.Request) {
	var req batchRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	ref, err := s.batchRef(r, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	n, err := s.rules.VerifyRates(r.Context(), ref, a.UserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"verified": n})
}
