package api

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/branding"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// A client's own logo, blueprint I2.
//
// The company is a path parameter rather than resolved from a terminal, because
// these are back-office routes and a browser has no device to resolve one from
// — the same reason purchasing and receivables take it. Row-level security is
// what makes naming it safe: another tenant's company reads as absent.
//
// # The bytes arrive base64 in JSON
//
// Not multipart, because nothing in this API is multipart and the one binary
// route that already exists — the terminal handing back a signed document —
// carries its bytes the same way. A second transport convention for one route
// would be a second thing to get right.

// --- request shapes -----------------------------------------------------

type putLogoRequest struct {
	// Base64 of the raw file. The `data:` prefix a browser's FileReader
	// produces is stripped by the client before it is sent; a stray one is
	// rejected here rather than stored as part of the image.
	Data string `json:"data"`
}

// --- GET /api/v1/companies/{companyID}/logo -----------------------------

// What is set, without the bytes. A settings screen needs to know a logo
// exists, its shape and its size; it does not need half a megabyte to say so.
func (s *Server) handleGetLogo(w http.ResponseWriter, r *http.Request) {
	scope, err := s.logoScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	logo, err := s.branding.Get(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// Absent is a state, not a failure: a company with no logo is the ordinary
	// case and the screen renders an empty panel for it.
	httpx.JSON(w, http.StatusOK, map[string]any{"logo": logo})
}

// --- GET /api/v1/companies/{companyID}/logo/image -----------------------

// The file itself.
//
// Authenticated rather than gated on a settings permission: a logo is the
// shop's public mark and is destined for every receipt it prints, so a till
// that could not read it could not print one. Row-level security still confines
// it to the caller's own tenant, which is the part that matters.
func (s *Server) handleGetLogoImage(w http.ResponseWriter, r *http.Request) {
	scope, err := s.logoScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	img, err := s.branding.Image(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The checksum is the validator, so a till that already holds this logo is
	// answered with a 304 rather than the image again on every receipt.
	etag := `"` + img.Checksum + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", img.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(img.Bytes)))
	w.Header().Set("ETag", etag)
	// Private: this is one tenant's asset and must not be held by a shared
	// cache that another tenant's request could reach.
	w.Header().Set("Cache-Control", "private, max-age=300")
	// Belt and braces against the content type being wrong despite the sniff
	// on the way in: tell the browser not to second-guess it either.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(img.Bytes)
}

// --- PUT /api/v1/companies/{companyID}/logo -----------------------------

func (s *Server) handlePutLogo(w http.ResponseWriter, r *http.Request) {
	var req putLogoRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	scope, err := s.logoScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"That image could not be read. Choose the file again."))
		return
	}

	logo, err := s.branding.Put(r.Context(), scope, raw)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, logo)
}

// --- DELETE /api/v1/companies/{companyID}/logo --------------------------

// Removing returns the company to the default RawSyst mark. Removing one that
// is not there succeeds: the client asked for no logo and there is no logo.
func (s *Server) handleDeleteLogo(w http.ResponseWriter, r *http.Request) {
	scope, err := s.logoScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.branding.Remove(r.Context(), scope); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (s *Server) logoScope(r *http.Request) (branding.Scope, error) {
	companyID, err := parseUUID(chi.URLParam(r, "companyID"), "company_id")
	if err != nil {
		return branding.Scope{}, err
	}
	a := actor.From(r.Context())
	return branding.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}
