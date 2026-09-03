// Exchange rates (G2).
//
// Two verbs over `internal/fx`: read what has been recorded, and record a day's
// rate for a pair. There is deliberately no "fetch today's rates from a feed"
// route — which feed a business books at is its own decision, and this product
// has no standing to pick one for them. See 0113.
package api

import (
	"net/http"

	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/fx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// fxScope reads the caller. Rates belong to the tenant rather than to one of
// its companies: a business closing its books uses one rate for the group, and
// two companies quoting the same day at different rates would not consolidate.
func (s *Server) fxScope(r *http.Request) (fx.Scope, error) {
	a := actor.From(r.Context())
	return fx.Scope{TenantID: a.TenantID, UserID: a.UserID}, nil
}

func (s *Server) handleListExchangeRates(w http.ResponseWriter, r *http.Request) {
	scope, err := s.fxScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.fx.Rates(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleRecordExchangeRate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From   string `json:"from_currency"`
		To     string `json:"to_currency"`
		Rate   string `json:"rate"`
		AsOf   string `json:"as_of"`
		Source string `json:"source"`
		Note   string `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.fxScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	rate, err := decimal.NewFromString(req.Rate)
	if err != nil {
		httpx.Error(w, r, errs.Validation("That rate is not a number.").
			WithField("rate", "How many units of the second currency one of "+
				"the first buys."))
		return
	}
	asOf, err := optionalDate(req.AsOf, "as_of")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if asOf == nil {
		httpx.Error(w, r, errs.Validation("Say which day the rate is for.").
			WithField("as_of", "A rate without a date cannot translate an "+
				"invoice, because an invoice is translated at the rate in "+
				"force when it was issued."))
		return
	}

	out, err := s.fx.Record(r.Context(), scope, req.From, req.To, rate, *asOf,
		req.Source, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"rate": out})
}
