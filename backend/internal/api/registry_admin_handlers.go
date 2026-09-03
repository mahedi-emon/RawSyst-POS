// The Platform Owner's tax and legal-value screens (A4, E8).
//
// Global rather than per tenant, and Super Admin only: a VAT rate, a social
// insurance schedule and a city's sales tax are the law, not one business's
// settings. A tenant that could edit them would be choosing what it owes.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

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
