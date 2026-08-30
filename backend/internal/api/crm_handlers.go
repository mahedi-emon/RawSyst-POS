package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/loyalty"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/wallet"
)

// Loyalty, store credit, gift cards and fitting history (blueprint B16).
//
// Two verbs on each side. `loyalty.view` and `wallet.view` are what a cashier
// holds: they have to be able to say what is on a card and how many points
// somebody has before taking either in payment. `loyalty.manage` and
// `wallet.manage` set the scheme up, issue cards and adjust balances — each of
// which creates money out of nothing from the shop's point of view, which is
// why they sit with the people who can also set a credit limit.
//
// Sizes are behind the CUSTOMER permissions rather than a pair of their own. A
// size is part of the customer record; anybody who may read a customer may read
// what size they are, and inventing `size.view` would be a permission an
// administrator has to grant before the feature works.

func (s *Server) loyaltyScope(r *http.Request) (loyalty.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return loyalty.Scope{}, err
	}
	return loyalty.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

func (s *Server) walletScope(r *http.Request) (wallet.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return wallet.Scope{}, err
	}
	return wallet.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- the scheme -----------------------------------------------------------

func (s *Server) handleGetLoyaltyProgram(w http.ResponseWriter, r *http.Request) {
	scope, err := s.loyaltyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.loyalty.Program(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleSetLoyaltyProgram(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active        bool   `json:"is_active"`
		SpendPerPoint string `json:"spend_per_point"`
		PointValue    string `json:"point_value"`
		ExpiryMonths  *int   `json:"expiry_months"`
		Tiers         []struct {
			Key      string `json:"key"`
			Name     string `json:"name"`
			NameAr   string `json:"name_ar"`
			MinSpend string `json:"min_spend"`
			Discount string `json:"discount_percent"`
		} `json:"tiers"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	spend, err := decimal.NewFromString(strings.TrimSpace(req.SpendPerPoint))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say how much has to be spent to earn a point."))
		return
	}
	value, err := decimal.NewFromString(strings.TrimSpace(req.PointValue))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say what a point is worth."))
		return
	}

	in := loyalty.NewProgram{
		Active: req.Active, SpendPerPoint: spend, PointValue: value,
		ExpiryMonths: req.ExpiryMonths,
	}
	for _, t := range req.Tiers {
		in.Tiers = append(in.Tiers, loyalty.Tier{
			Key: t.Key, Name: t.Name, NameAr: t.NameAr,
			MinSpend: t.MinSpend, Discount: t.Discount,
		})
	}

	scope, err := s.loyaltyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.loyalty.SetProgram(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListLoyaltyMembers(w http.ResponseWriter, r *http.Request) {
	scope, err := s.loyaltyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.loyalty.Members(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleGetLoyaltyCard(w http.ResponseWriter, r *http.Request) {
	scope, err := s.loyaltyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.loyalty.Card(r.Context(), scope, customerID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleAdjustPoints(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Points int    `json:"points"`
		Note   string `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.loyaltyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.loyalty.Adjust(r.Context(), scope, customerID, req.Points, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleExpirePoints(w http.ResponseWriter, r *http.Request) {
	scope, err := s.loyaltyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	points, err := s.loyalty.Expire(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"points_expired": points})
}

// --- store credit and gift cards ------------------------------------------

func (s *Server) handleGetWallet(w http.ResponseWriter, r *http.Request) {
	scope, err := s.walletScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.wallet.Wallet(r.Context(), scope, customerID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleGiveStoreCredit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount    string `json:"amount"`
		ExpiresOn string `json:"expires_on"`
		Note      string `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(req.Amount))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say how much credit is being given."))
		return
	}
	var expires *time.Time
	if v := strings.TrimSpace(req.ExpiresOn); v != "" {
		day, e := parseReportDate(v, "expires_on", time.Time{})
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		expires = &day
	}

	scope, err := s.walletScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.wallet.Give(r.Context(), scope, customerID, amount, expires, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListGiftCards(w http.ResponseWriter, r *http.Request) {
	scope, err := s.walletScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.wallet.Cards(r.Context(), scope,
		r.URL.Query().Get("include_cancelled") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleIssueGiftCard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code       string `json:"code"`
		FaceValue  string `json:"face_value"`
		ExpiresOn  string `json:"expires_on"`
		CustomerID string `json:"customer_id"`
		Note       string `json:"note"`
		Proceeds   []struct {
			Role   string `json:"role"`
			Amount string `json:"amount"`
		} `json:"proceeds"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	face, err := decimal.NewFromString(strings.TrimSpace(req.FaceValue))
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Say what the card is worth."))
		return
	}

	in := wallet.NewCard{Code: req.Code, FaceValue: face, Note: req.Note}
	if v := strings.TrimSpace(req.ExpiresOn); v != "" {
		day, e := parseReportDate(v, "expires_on", time.Time{})
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.ExpiresOn = &day
	}
	if v := strings.TrimSpace(req.CustomerID); v != "" {
		id, e := parseUUID(v, "customer_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		in.CustomerID = &id
	}
	for i, p := range req.Proceeds {
		amount, e := decimal.NewFromString(strings.TrimSpace(p.Amount))
		if e != nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Payment %d does not say how much.", i+1))
			return
		}
		in.Proceeds = append(in.Proceeds, wallet.Payment{
			Role: strings.TrimSpace(p.Role), Amount: amount,
		})
	}

	scope, err := s.walletScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.wallet.Issue(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetGiftCard(w http.ResponseWriter, r *http.Request) {
	scope, err := s.walletScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "cardID"), "cardID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.wallet.Card(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// handleLookUpGiftCard is what a till calls when a cashier types a card number.
//
// A GET with the number in the path rather than a POST: it changes nothing, and
// a cashier checking a balance before a customer commits to using it is the
// most common thing that happens to a gift card.
func (s *Server) handleLookUpGiftCard(w http.ResponseWriter, r *http.Request) {
	scope, err := s.walletScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.wallet.Lookup(r.Context(), scope, chi.URLParam(r, "code"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleVoidGiftCard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.walletScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "cardID"), "cardID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.wallet.Void(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleExpireStoreCredit(w http.ResponseWriter, r *http.Request) {
	scope, err := s.walletScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.wallet.ExpireCredit(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- fitting history ------------------------------------------------------

func (s *Server) handleListCustomerSizes(w http.ResponseWriter, r *http.Request) {
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.receivables.Sizes(r.Context(), scope, customerID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleRecordCustomerSize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Garment      string            `json:"garment"`
		Size         string            `json:"size"`
		Measurements map[string]string `json:"measurements"`
		Note         string            `json:"note"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.receivables.RecordSize(r.Context(), scope, customerID,
		receivables.NewSize{
			Garment: req.Garment, Size: req.Size,
			Measurements: req.Measurements, Note: req.Note,
		})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleForgetCustomerSize(w http.ResponseWriter, r *http.Request) {
	scope, err := s.customerScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	customerID, err := parseUUID(chi.URLParam(r, "customerID"), "customerID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	sizeID, err := parseUUID(chi.URLParam(r, "sizeID"), "sizeID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.receivables.ForgetSize(r.Context(), scope, customerID, sizeID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}
