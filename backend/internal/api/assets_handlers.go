package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/assets"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// Fixed assets (C7) and investors (C3.2).
//
// Two registers behind four permissions, split the way the rest of the product
// splits them: reading a register is one thing, changing what is in it is
// another. Neither is offered to a Store Manager — 0005 describes that role as
// unable to see bank ledgers or true net profit, and an investor register is a
// statement of who owns the business.

func (s *Server) assetScope(r *http.Request) (assets.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return assets.Scope{}, err
	}
	return assets.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- the asset register ---------------------------------------------------

func (s *Server) handleAssetRegister(w http.ResponseWriter, r *http.Request) {
	scope, err := s.assetScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.assets.Register(r.Context(), scope,
		r.URL.Query().Get("include_disposed") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleAddAsset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		NameAr        string `json:"name_ar"`
		Category      string `json:"category"`
		StoreID       string `json:"store_id"`
		CustodianID   string `json:"custodian_id"`
		SerialNumber  string `json:"serial_number"`
		WarrantyUntil string `json:"warranty_until"`
		AcquiredOn    string `json:"acquired_on"`
		Cost          string `json:"cost"`
		Residual      string `json:"residual_value"`
		LifeMonths    int    `json:"useful_life_months"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	cost, err := decimal.NewFromString(strings.TrimSpace(req.Cost))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say what the asset cost."))
		return
	}
	residual := decimal.Zero
	if strings.TrimSpace(req.Residual) != "" {
		residual, err = decimal.NewFromString(strings.TrimSpace(req.Residual))
		if err != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"What the asset will be worth at the end is not a number."))
			return
		}
	}
	acquiredOn, err := parseReportDate(req.AcquiredOn, "acquired_on", time.Now().UTC())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := assets.NewAsset{
		Name: req.Name, NameAr: req.NameAr, Category: req.Category,
		SerialNumber: req.SerialNumber, AcquiredOn: acquiredOn,
		Cost: cost, Residual: residual, LifeMonths: req.LifeMonths,
	}
	if v := strings.TrimSpace(req.StoreID); v != "" {
		id, e := parseUUID(v, "store_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.StoreID = &id
	}
	if v := strings.TrimSpace(req.CustodianID); v != "" {
		id, e := parseUUID(v, "custodian_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.CustodianID = &id
	}
	if v := strings.TrimSpace(req.WarrantyUntil); v != "" {
		day, e := parseReportDate(v, "warranty_until", time.Time{})
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.WarrantyUntil = &day
	}

	scope, err := s.assetScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.assets.Add(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleDepreciate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		// Month as YYYY-MM-DD; any day in it will do, and the service takes
		// the month it falls in.
		Month string `json:"month"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	month, err := parseReportDate(req.Month, "month", time.Now().UTC().AddDate(0, -1, 0))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.assetScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.assets.Depreciate(r.Context(), scope, month)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleDisposeAsset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Proceeds       string `json:"proceeds"`
		MoneyAccountID string `json:"money_account_id"`
		DisposedOn     string `json:"disposed_on"`
		Note           string `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	assetID, err := parseUUID(chi.URLParam(r, "assetID"), "assetID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	proceeds := decimal.Zero
	if strings.TrimSpace(req.Proceeds) != "" {
		proceeds, err = decimal.NewFromString(strings.TrimSpace(req.Proceeds))
		if err != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"Say what the business got for it, or nothing if it was scrapped."))
			return
		}
	}
	disposedOn, err := parseReportDate(req.DisposedOn, "disposed_on", time.Now().UTC())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := assets.Disposal{
		AssetID: assetID, Proceeds: proceeds,
		DisposedOn: disposedOn, Note: req.Note,
	}
	if v := strings.TrimSpace(req.MoneyAccountID); v != "" {
		id, e := parseUUID(v, "money_account_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.MoneyAccountID = &id
	}

	scope, err := s.assetScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.assets.Dispose(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- investors ------------------------------------------------------------

func (s *Server) handleListInvestors(w http.ResponseWriter, r *http.Request) {
	scope, err := s.assetScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.assets.Investors(r.Context(), scope,
		r.URL.Query().Get("include_retired") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleAddInvestor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		NameAr string `json:"name_ar"`
		Kind   string `json:"kind"`
		Email  string `json:"email"`
		Phone  string `json:"phone"`
		Note   string `json:"note"`
		UserID string `json:"user_id"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	in := assets.NewInvestor{
		Name: req.Name, NameAr: req.NameAr, Kind: req.Kind,
		Email: req.Email, Phone: req.Phone, Note: req.Note,
	}
	if v := strings.TrimSpace(req.UserID); v != "" {
		id, e := parseUUID(v, "user_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.UserID = &id
	}

	scope, err := s.assetScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.assets.AddInvestor(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleRecordInvestment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID           string `json:"uuid"`
		InvestorID     string `json:"investor_id"`
		Direction      string `json:"direction"`
		Amount         string `json:"amount"`
		MovedOn        string `json:"moved_on"`
		MoneyAccountID string `json:"money_account_id"`
		Reference      string `json:"reference"`
		Note           string `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	id, err := parseUUID(req.UUID, "uuid")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	investorID, err := parseUUID(req.InvestorID, "investor_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	accountID, err := parseUUID(req.MoneyAccountID, "money_account_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(req.Amount))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say how much is moving."))
		return
	}
	movedOn, err := parseReportDate(req.MovedOn, "moved_on", time.Now().UTC())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	scope, err := s.assetScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.assets.Record(r.Context(), scope, assets.NewMovement{
		UUID: id, InvestorID: investorID,
		Direction: strings.TrimSpace(req.Direction), Amount: amount,
		MovedOn: movedOn, MoneyAccountID: accountID,
		Reference: req.Reference, Note: req.Note,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// handleInvestorStatement reads one investor's history.
//
// C3.2: "each investor can (if given access) see only their own contribution
// and return history." The route takes `investor.view`, which an Owner and an
// Accountant hold; an investor given their own login holds it too, and this
// checks that the statement they are asking for is theirs.
func (s *Server) handleInvestorStatement(w http.ResponseWriter, r *http.Request) {
	scope, err := s.assetScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	investorID, err := parseUUID(chi.URLParam(r, "investorID"), "investorID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	if err := s.assets.MayReadStatement(r.Context(), scope, investorID); err != nil {
		httpx.Error(w, r, err)
		return
	}

	out, err := s.assets.Statement(r.Context(), scope, investorID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

var _ = uuid.Nil
