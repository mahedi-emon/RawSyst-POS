package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// The catalogue surface.
//
// Cost price and margin never appear here. A Cashier holds catalog.view and is
// deliberately denied catalog.view_cost_price, so a payload that carried cost
// would defeat the masking the permission exists to provide — the route would
// be gated correctly and the data would leak anyway.

type createProductRequest struct {
	CompanyID string `json:"company_id"`

	SKU         string `json:"sku"`
	Name        string `json:"name"`
	NameAr      string `json:"name_ar"`
	Description string `json:"description"`

	CategoryID string `json:"category_id"`
	BrandID    string `json:"brand_id"`
	UnitID     string `json:"unit_id"`

	TaxTreatment       string `json:"tax_treatment"`
	TaxExemptionReason string `json:"tax_exemption_reason_code"`

	TrackSerial bool `json:"track_serial"`
	TrackBatch  bool `json:"track_batch"`
}

func (s *Server) handleCreateProduct(w http.ResponseWriter, r *http.Request) {
	var req createProductRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	companyID, err := parseUUID(req.CompanyID, "company_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !a.CanAccessCompany(companyID) {
		httpx.Error(w, r, errs.New(errs.CodeNotFound, "That company was not found."))
		return
	}

	in := catalog.NewProduct{
		CompanyID: companyID,
		SKU:       req.SKU, Name: req.Name, NameAr: req.NameAr,
		Description:  req.Description,
		TaxTreatment: req.TaxTreatment,
		// A blank treatment would fall through to the column default and skip
		// the registry check, so it is filled in here and validated like any
		// other.
		TaxExemptionReason: req.TaxExemptionReason,
		TrackSerial:        req.TrackSerial,
		TrackBatch:         req.TrackBatch,
	}
	if in.TaxTreatment == "" {
		in.TaxTreatment = "standard"
	}
	userID := a.UserID
	in.CreatedBy = &userID

	for _, ref := range []struct {
		raw   string
		field string
		dst   **uuid.UUID
	}{
		{req.CategoryID, "category_id", &in.CategoryID},
		{req.BrandID, "brand_id", &in.BrandID},
		{req.UnitID, "unit_id", &in.UnitID},
	} {
		id, err := parseOptionalUUID(ref.raw, ref.field)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		if id != uuid.Nil {
			v := id
			*ref.dst = &v
		}
	}

	out, err := s.catalog.CreateProduct(r.Context(), a.TenantID, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleListProducts(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())

	companyID, err := parseUUID(r.URL.Query().Get("company_id"), "company_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !a.CanAccessCompany(companyID) {
		httpx.Error(w, r, errs.New(errs.CodeNotFound, "That company was not found."))
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	// Cursored, not offset-paged: an offset walks and discards every row before
	// the page, and rows shift underneath it while someone is reading.
	var after *uuid.UUID
	if raw := r.URL.Query().Get("after"); raw != "" {
		id, err := parseUUID(raw, "after")
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		after = &id
	}

	products, err := s.catalog.ListProducts(r.Context(), a.TenantID, companyID,
		r.URL.Query().Get("search"), limit, after)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	page := map[string]any{"limit": limit, "has_more": false}
	if len(products) > 0 {
		page["cursor"] = products[len(products)-1].ID
		page["has_more"] = len(products) == limit
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": products, "page": page})
}

type matrixRequest struct {
	Axes         []catalog.Axis `json:"axes"`
	BasePrice    string         `json:"base_price"`
	PriceFloor   string         `json:"price_floor"`
	CostStandard string         `json:"cost_standard"`
}

func (s *Server) handleGenerateMatrix(w http.ResponseWriter, r *http.Request) {
	var req matrixRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	productID, err := parseUUID(chi.URLParam(r, "productID"), "productID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	basePrice, err := parseAmount(req.BasePrice, "base_price", -1)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	in := catalog.MatrixRequest{
		ProductID: productID, Axes: req.Axes, BasePrice: basePrice,
	}
	for _, opt := range []struct {
		raw   string
		field string
		dst   **decimal.Decimal
	}{
		{req.PriceFloor, "price_floor", &in.PriceFloor},
		{req.CostStandard, "cost_standard", &in.CostStandard},
	} {
		if opt.raw == "" {
			continue
		}
		v, err := parseAmount(opt.raw, opt.field, -1)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		value := v
		*opt.dst = &value
	}

	a := actor.From(r.Context())
	out, err := s.catalog.GenerateMatrix(r.Context(), a.TenantID, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleReadMatrix(w http.ResponseWriter, r *http.Request) {
	productID, err := parseUUID(chi.URLParam(r, "productID"), "productID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	grid, err := s.catalog.ReadMatrix(r.Context(), a.TenantID, productID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": grid})
}

// handleWithdrawVariant takes a variant off sale. Never a delete: it is
// referenced by invoice lines, stock movements and cost layers, all of which
// are immutable history.
func (s *Server) handleWithdrawVariant(w http.ResponseWriter, r *http.Request) {
	variantID, err := parseUUID(chi.URLParam(r, "variantID"), "variantID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	a := actor.From(r.Context())
	if err := s.catalog.Deactivate(r.Context(), a.TenantID, variantID); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

// handleScanBarcode is the till's lookup: one scan, one variant.
func (s *Server) handleScanBarcode(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())

	// company_id is OPTIONAL here, unlike on the reporting routes.
	//
	// A till knows its barcode and nothing else. Everything about where it is
	// trading — company, store, EGS unit, warehouse — is resolved from the
	// registered device, because a terminal that could name its own company
	// could read another company's catalogue. Requiring the till to supply it
	// would mean either teaching it a fact it has no way to learn, or letting
	// it assert one.
	//
	// A back-office caller has no device and names the company explicitly.
	var companyID uuid.UUID
	if raw := r.URL.Query().Get("company_id"); raw != "" {
		id, err := parseUUID(raw, "company_id")
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		if !a.CanAccessCompany(id) {
			httpx.Error(w, r, errs.New(errs.CodeNotFound, "That company was not found."))
			return
		}
		companyID = id
	} else {
		if a.DeviceID == uuid.Nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"Say which company to search, or scan from a registered terminal."))
			return
		}
		id, err := s.catalog.CompanyForDevice(r.Context(), a.TenantID, a.DeviceID)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		companyID = id
	}

	barcode := r.URL.Query().Get("barcode")
	if barcode == "" {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Scan a barcode or type one in."))
		return
	}

	out, err := s.catalog.FindByBarcode(r.Context(), a.TenantID, companyID, barcode)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
