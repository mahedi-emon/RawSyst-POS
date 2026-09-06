package api

// Categories, brands and units — the routes that were missing under
// `POST /catalog/products`.
//
// The product create route accepts `category_id`, `brand_id` and `unit_id`.
// Nothing could list any of the three and nothing could create one, so all
// three parameters took an id that no part of the product could produce. See
// the note at the top of internal/catalog/taxonomy.go.
//
// # Reading is catalog.view, writing is catalog.edit
//
// Not `catalog.create`: creating a CATEGORY is not creating a product, and the
// two are held by different people in a shop of any size — a manager tidies the
// departments, an assistant adds the stock. `catalog.edit` is the permission
// that already means "change how the catalogue is arranged".
//
// A cashier holds `catalog.view` and will read these lists, which is correct:
// a till that cannot name a department cannot show a menu of them.

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// taxonomyScope resolves the company the caller named, and refuses one they
// may not reach with a not-found rather than a forbidden.
func taxonomyScope(r *http.Request) (actor.Actor, uuid.UUID, error) {
	a := actor.From(r.Context())
	companyID, err := parseUUID(r.URL.Query().Get("company_id"), "company_id")
	if err != nil {
		return a, uuid.Nil, err
	}
	if !a.CanAccessCompany(companyID) {
		return a, uuid.Nil, errs.New(errs.CodeNotFound, "That company was not found.")
	}
	return a, companyID, nil
}

// retiredWanted is `?include_retired=true`, the same spelling the expense
// setup screens use.
func retiredWanted(r *http.Request) bool {
	return r.URL.Query().Get("include_retired") == "true"
}

// --- categories ----------------------------------------------------------

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.Categories(r.Context(), a.TenantID, companyID,
		retiredWanted(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type categoryRequest struct {
	Name      string `json:"name"`
	NameAr    string `json:"name_ar"`
	ParentID  string `json:"parent_id"`
	SortOrder int    `json:"sort_order"`
}

func (req categoryRequest) toNew() (catalog.NewCategory, error) {
	in := catalog.NewCategory{
		Name: req.Name, NameAr: req.NameAr, SortOrder: req.SortOrder,
	}
	if req.ParentID != "" {
		id, err := parseUUID(req.ParentID, "parent_id")
		if err != nil {
			return catalog.NewCategory{}, err
		}
		in.ParentID = &id
	}
	return in, nil
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	in, err := req.toNew()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.CreateCategory(r.Context(), a.TenantID, companyID,
		a.UserID, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "categoryID"), "categoryID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	in, err := req.toNew()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.UpdateCategory(r.Context(), a.TenantID, companyID, id, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleSetCategoryActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool `json:"is_active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "categoryID"), "categoryID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.catalog.SetCategoryActive(
		r.Context(), a.TenantID, companyID, id, req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- brands --------------------------------------------------------------

func (s *Server) handleListBrands(w http.ResponseWriter, r *http.Request) {
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.Brands(r.Context(), a.TenantID, companyID,
		retiredWanted(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type brandRequest struct {
	Name   string `json:"name"`
	NameAr string `json:"name_ar"`
}

func (s *Server) handleCreateBrand(w http.ResponseWriter, r *http.Request) {
	var req brandRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.SaveBrand(r.Context(), a.TenantID, companyID, nil,
		req.Name, req.NameAr)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateBrand(w http.ResponseWriter, r *http.Request) {
	var req brandRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "brandID"), "brandID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.SaveBrand(r.Context(), a.TenantID, companyID, &id,
		req.Name, req.NameAr)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleSetBrandActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool `json:"is_active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "brandID"), "brandID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.catalog.SetBrandActive(
		r.Context(), a.TenantID, companyID, id, req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- units of measure ----------------------------------------------------

func (s *Server) handleListUnits(w http.ResponseWriter, r *http.Request) {
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.Units(r.Context(), a.TenantID, companyID,
		retiredWanted(r))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

type unitRequest struct {
	Code           string `json:"code"`
	Name           string `json:"name"`
	NameAr         string `json:"name_ar"`
	AllowsFraction bool   `json:"allows_fraction"`
}

func (req unitRequest) toNew() catalog.NewUnit {
	return catalog.NewUnit{
		Code: req.Code, Name: req.Name, NameAr: req.NameAr,
		AllowsFraction: req.AllowsFraction,
	}
}

func (s *Server) handleCreateUnit(w http.ResponseWriter, r *http.Request) {
	var req unitRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.SaveUnit(r.Context(), a.TenantID, companyID, nil,
		req.toNew())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateUnit(w http.ResponseWriter, r *http.Request) {
	var req unitRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "unitID"), "unitID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.SaveUnit(r.Context(), a.TenantID, companyID, &id,
		req.toNew())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleSetUnitActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool `json:"is_active"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "unitID"), "unitID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.catalog.SetUnitActive(
		r.Context(), a.TenantID, companyID, id, req.Active); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- what tax treatments this company may use ----------------------------

// handleListTaxTreatments answers what a product form may offer.
//
// Without it the form has to guess, and a form that offers "zero_rated" in a
// US catalogue refuses on save with a list it should have shown first.
func (s *Server) handleListTaxTreatments(w http.ResponseWriter, r *http.Request) {
	a, companyID, err := taxonomyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.catalog.TreatmentsFor(r.Context(), a.TenantID, companyID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
