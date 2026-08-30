package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/promotions"
)

// Promotions and the pricing engine (blueprint B9).
//
// # Why the till may QUOTE but not create
//
// A cashier holds `promotion.view`, which reaches the quote route: a cart being
// built has to be priced, repeatedly, while items are scanned. Setting a
// campaign up needs `promotion.manage`, which an Owner holds and a cashier does
// not — a campaign decides what every till in every branch will charge, and B9
// puts manager authorisation around discounts far smaller than that.

func (s *Server) promotionScope(r *http.Request) (promotions.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return promotions.Scope{}, err
	}
	return promotions.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

func (s *Server) handleListPromotions(w http.ResponseWriter, r *http.Request) {
	scope, err := s.promotionScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.promotions.List(r.Context(), scope,
		r.URL.Query().Get("include_finished") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type promotionRequest struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	NameAr string `json:"name_ar"`
	Kind   string `json:"kind"`

	Value  string `json:"value"`
	BuyQty string `json:"buy_qty"`
	GetQty string `json:"get_qty"`

	CategoryID   string `json:"category_id"`
	BrandID      string `json:"brand_id"`
	VariantID    string `json:"variant_id"`
	CustomerType string `json:"customer_type"`

	StartsOn    string `json:"starts_on"`
	EndsOn      string `json:"ends_on"`
	StoreID     string `json:"store_id"`
	MinPurchase string `json:"min_purchase"`

	CouponCode         string `json:"coupon_code"`
	MaxUses            *int   `json:"max_uses"`
	MaxUsesPerCustomer *int   `json:"max_uses_per_customer"`

	Priority int `json:"priority"`
}

func (req promotionRequest) into() (promotions.NewPromotion, error) {
	out := promotions.NewPromotion{
		Code: req.Code, Name: req.Name, NameAr: req.NameAr,
		Kind: strings.TrimSpace(req.Kind), CustomerType: req.CustomerType,
		CouponCode: req.CouponCode, MaxUses: req.MaxUses,
		MaxUsesPerCustomer: req.MaxUsesPerCustomer, Priority: req.Priority,
	}

	for _, f := range []struct {
		raw   string
		into  *decimal.Decimal
		field string
	}{
		{req.Value, &out.Value, "value"},
		{req.BuyQty, &out.BuyQty, "buy_qty"},
		{req.GetQty, &out.GetQty, "get_qty"},
		{req.MinPurchase, &out.MinPurchase, "min_purchase"},
	} {
		if strings.TrimSpace(f.raw) == "" {
			continue
		}
		v, err := decimal.NewFromString(strings.TrimSpace(f.raw))
		if err != nil {
			return promotions.NewPromotion{}, errs.Newf(errs.CodeInvalidInput,
				"%q is not a number.", f.raw)
		}
		*f.into = v
	}

	for _, f := range []struct {
		raw   string
		into  **uuid.UUID
		field string
	}{
		{req.CategoryID, &out.CategoryID, "category_id"},
		{req.BrandID, &out.BrandID, "brand_id"},
		{req.VariantID, &out.VariantID, "variant_id"},
		{req.StoreID, &out.StoreID, "store_id"},
	} {
		if strings.TrimSpace(f.raw) == "" {
			continue
		}
		id, err := parseUUID(f.raw, f.field)
		if err != nil {
			return promotions.NewPromotion{}, err
		}
		*f.into = &id
	}

	for _, f := range []struct {
		raw   string
		into  **time.Time
		field string
	}{
		{req.StartsOn, &out.StartsOn, "starts_on"},
		{req.EndsOn, &out.EndsOn, "ends_on"},
	} {
		if strings.TrimSpace(f.raw) == "" {
			continue
		}
		day, err := parseReportDate(f.raw, f.field, time.Time{})
		if err != nil {
			return promotions.NewPromotion{}, err
		}
		*f.into = &day
	}

	return out, nil
}

func (s *Server) handleCreatePromotion(w http.ResponseWriter, r *http.Request) {
	var req promotionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	in, err := req.into()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.promotionScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.promotions.Create(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleSetPromotionActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active *bool `json:"is_active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Active == nil {
		httpx.Error(w, r, errs.Validation("Say whether the campaign is running.").
			WithField("is_active", "true to run it, false to stop it."))
		return
	}
	scope, err := s.promotionScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "promotionID"), "promotionID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.promotions.SetActive(r.Context(), scope, id, *req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleQuotePromotions prices a cart.
//
// Read-only, and deliberately so: a cart is priced many times while it is being
// built, and a route that recorded a redemption every time would report a
// campaign as having been used forty times for one sale. Redemption happens
// when the sale is finalised, inside its transaction.
func (s *Server) handleQuotePromotions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StoreID      string `json:"store_id"`
		CustomerID   string `json:"customer_id"`
		CustomerType string `json:"customer_type"`
		CouponCode   string `json:"coupon_code"`
		On           string `json:"on"`
		Lines        []struct {
			VariantID  string `json:"variant_id"`
			Qty        string `json:"qty"`
			UnitPrice  string `json:"unit_price"`
			FloorPrice string `json:"floor_price"`
			CategoryID string `json:"category_id"`
			BrandID    string `json:"brand_id"`
		} `json:"lines"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	on, err := parseReportDate(req.On, "on", time.Now().UTC())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	basket := promotions.Basket{
		CustomerType: req.CustomerType, CouponCode: req.CouponCode, On: on,
	}
	if v := strings.TrimSpace(req.StoreID); v != "" {
		id, e := parseUUID(v, "store_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		basket.StoreID = &id
	}
	if v := strings.TrimSpace(req.CustomerID); v != "" {
		id, e := parseUUID(v, "customer_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		basket.CustomerID = &id
	}

	for i, l := range req.Lines {
		variantID, e := parseUUID(l.VariantID, "variant_id")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		qty, e := decimal.NewFromString(strings.TrimSpace(l.Qty))
		if e != nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not say how many.", i+1))
			return
		}
		unit, e := decimal.NewFromString(strings.TrimSpace(l.UnitPrice))
		if e != nil {
			httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
				"Line %d does not carry a price.", i+1))
			return
		}
		floor := decimal.Zero
		if strings.TrimSpace(l.FloorPrice) != "" {
			floor, _ = decimal.NewFromString(strings.TrimSpace(l.FloorPrice))
		}

		line := promotions.Line{
			VariantID: variantID, Qty: qty, UnitPrice: unit, FloorPrice: floor,
		}
		if v := strings.TrimSpace(l.CategoryID); v != "" {
			id, e := parseUUID(v, "category_id")
			if e != nil {
				httpx.Error(w, r, e)
				return
			}
			line.CategoryID = &id
		}
		if v := strings.TrimSpace(l.BrandID); v != "" {
			id, e := parseUUID(v, "brand_id")
			if e != nil {
				httpx.Error(w, r, e)
				return
			}
			line.BrandID = &id
		}
		basket.Lines = append(basket.Lines, line)
	}

	scope, err := s.promotionScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.promotions.Quote(r.Context(), scope, basket)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}
