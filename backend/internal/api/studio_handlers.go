package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/insight"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/labels"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// The label studio (B3), global search (D7), analytics (D2) and the Super
// Admin control plane (H8).
//
// # Search carries no permission of its own
//
// It is a lens over what the caller can already reach. Each branch of the query
// is gated by the permission that guards the thing it finds — a cashier holds
// `catalog.view` and not `hr.view`, so searching "Omar" returns the product and
// not the employee. A permission on the route would have to be either narrow
// enough to find nothing or wide enough to hand the staff list to a till.
//
// # Printing a label and designing one are different verbs
//
// `label.print` is a daily act by whoever is putting stock on the shelf.
// `label.manage` redesigns the tag that carries the shop's price and logo, and
// changes the rule that builds every barcode the shop generates from then on.

func (s *Server) labelScope(r *http.Request) (labels.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return labels.Scope{}, err
	}
	return labels.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

func (s *Server) insightScope(r *http.Request) (insight.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return insight.Scope{}, err
	}
	return insight.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- the barcode scheme ---------------------------------------------------

func (s *Server) handleGetBarcodeScheme(w http.ResponseWriter, r *http.Request) {
	scope, err := s.labelScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.labels.ReadScheme(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleSetBarcodeScheme(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Parts      []string `json:"parts"`
		Separator  string   `json:"separator"`
		Symbology  string   `json:"symbology"`
		PartLength int      `json:"part_length"`
		Prefix     string   `json:"prefix"`
		// Accepted and ignored: the screen reads the scheme back with its
		// worked example and puts the whole object return, and refusing a field
		// this endpoint produced would break the natural round trip.
		Example string `json:"example"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.labelScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.labels.SaveScheme(r.Context(), scope, labels.Scheme{
		Parts: req.Parts, Separator: req.Separator, Symbology: req.Symbology,
		PartLength: req.PartLength, Prefix: req.Prefix,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleGenerateBarcodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VariantIDs []string `json:"variant_ids"`
		Overwrite  bool     `json:"overwrite"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	ids, err := parseUUIDs(req.VariantIDs, "variant_ids")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.labelScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.labels.Generate(r.Context(), scope, ids, req.Overwrite)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleSetVariantBarcode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Barcode string `json:"barcode"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.labelScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	variantID, err := parseUUID(chi.URLParam(r, "variantID"), "variantID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.labels.SetBarcode(r.Context(), scope, variantID,
		req.Barcode); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- label templates ------------------------------------------------------

func (s *Server) handleListLabelTemplates(w http.ResponseWriter, r *http.Request) {
	scope, err := s.labelScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.labels.Templates(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSaveLabelTemplate(w http.ResponseWriter, r *http.Request) {
	tpl, err := decodeTemplate(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	scope, err := s.labelScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	var id *uuid.UUID
	if raw := chi.URLParam(r, "templateID"); raw != "" {
		parsed, e := parseUUID(raw, "templateID")
		if e != nil {
			httpx.Error(w, r, e)
			return
		}
		id = &parsed
	}

	out, err := s.labels.SaveTemplate(r.Context(), scope, id, tpl)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	status := http.StatusCreated
	if id != nil {
		status = http.StatusOK
	}
	httpx.JSON(w, status, out)
}

func (s *Server) handleDeleteLabelTemplate(w http.ResponseWriter, r *http.Request) {
	scope, err := s.labelScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "templateID"), "templateID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.labels.DeleteTemplate(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeTemplate(r *http.Request) (labels.Template, error) {
	var req struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Width     string `json:"width_mm"`
		Height    string `json:"height_mm"`
		Columns   *int   `json:"columns"`
		Rows      *int   `json:"rows"`
		Margin    string `json:"margin_mm"`
		Gap       string `json:"gap_mm"`
		IsDefault bool   `json:"is_default"`
		Fields    []struct {
			Field  string `json:"field"`
			Size   int    `json:"size"`
			Height int    `json:"height"`
			Bold   bool   `json:"bold"`
			RTL    bool   `json:"rtl"`
		} `json:"fields"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		return labels.Template{}, err
	}

	out := labels.Template{
		Name: req.Name, Kind: req.Kind, Width: req.Width, Height: req.Height,
		Columns: req.Columns, Rows: req.Rows, Margin: req.Margin,
		Gap: req.Gap, IsDefault: req.IsDefault,
	}
	for _, f := range req.Fields {
		out.Fields = append(out.Fields, labels.Field{
			Field: f.Field, Size: f.Size, Height: f.Height,
			Bold: f.Bold, RTL: f.RTL,
		})
	}
	return out, nil
}

// handlePrintLabels assembles a run.
//
// A POST, even though it writes nothing. The selection can name several hundred
// variant ids and a URL cannot carry them; a GET whose query string is eight
// kilobytes is a GET some proxy between the till and the server will truncate.
func (s *Server) handlePrintLabels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TemplateID string   `json:"template_id"`
		Kind       string   `json:"kind"`
		VariantIDs []string `json:"variant_ids"`
		CategoryID string   `json:"category_id"`
		BrandID    string   `json:"brand_id"`
		Search     string   `json:"search"`
		Copies     int      `json:"copies"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	q := labels.Query{Kind: req.Kind, Search: req.Search, Copies: req.Copies}
	ids, err := parseUUIDs(req.VariantIDs, "variant_ids")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	q.VariantIDs = ids

	for _, opt := range []struct {
		raw   string
		field string
		into  **uuid.UUID
	}{
		{req.TemplateID, "template_id", &q.TemplateID},
		{req.CategoryID, "category_id", &q.CategoryID},
		{req.BrandID, "brand_id", &q.BrandID},
	} {
		if v := strings.TrimSpace(opt.raw); v != "" {
			parsed, e := parseUUID(v, opt.field)
			if e != nil {
				httpx.Error(w, r, e)
				return
			}
			*opt.into = &parsed
		}
	}

	scope, err := s.labelScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.labels.Build(r.Context(), scope, q)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// --- global search --------------------------------------------------------

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	scope, err := s.insightScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	grants := identity.GrantsFrom(r.Context())
	out, err := s.insight.Search(r.Context(), scope,
		r.URL.Query().Get("q"), grants.Can)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- analytics ------------------------------------------------------------

func (s *Server) handleMovers(w http.ResponseWriter, r *http.Request) {
	scope, err := s.insightScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.insight.Movers(r.Context(), scope,
		atoiOr(r.URL.Query().Get("days"), 90))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleForecast(w http.ResponseWriter, r *http.Request) {
	scope, err := s.insightScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.insight.Forecasts(r.Context(), scope,
		atoiOr(r.URL.Query().Get("window_days"), 90),
		atoiOr(r.URL.Query().Get("forecast_days"), 30))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleProfitability(w http.ResponseWriter, r *http.Request) {
	scope, err := s.insightScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, to, err := analyticsPeriod(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.insight.Ranking(r.Context(), scope,
		r.URL.Query().Get("by"), from, to)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleKPIs(w http.ResponseWriter, r *http.Request) {
	scope, err := s.insightScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	from, to, err := analyticsPeriod(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.insight.Dashboard(r.Context(), scope, from, to)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// analyticsPeriod reads the window, defaulting to the last thirty days.
//
// `to` is exclusive and the default runs to TOMORROW, so a sale rung five
// minutes ago is inside it. A period ending at midnight tonight would leave an
// owner checking today's figures at four in the afternoon and finding nothing.
func analyticsPeriod(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from, err := parseReportDate(r.URL.Query().Get("from"), "from",
		now.AddDate(0, 0, -30).Truncate(24*time.Hour))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := parseReportDate(r.URL.Query().Get("to"), "to",
		now.AddDate(0, 0, 1).Truncate(24*time.Hour))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, errs.New(errs.CodeInvalidInput,
			"The period has to end after it starts.")
	}
	return from, to, nil
}

// --- the control plane ----------------------------------------------------

func (s *Server) handlePlatformHealth(w http.ResponseWriter, r *http.Request) {
	out, err := s.platform.Overview(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handleListTenants(w http.ResponseWriter, r *http.Request) {
	out, err := s.platform.Tenants(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleFailedJobs(w http.ResponseWriter, r *http.Request) {
	out, err := s.platform.FailedJobs(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "jobID"), "jobID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := s.platform.RetryJob(r.Context(), id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSupportQueue(w http.ResponseWriter, r *http.Request) {
	out, err := s.ops.Queue(r.Context(),
		r.URL.Query().Get("include_closed") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleAnswerTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Body   string `json:"body"`
		Status string `json:"status"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "ticketID"), "ticketID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	// The author is read from the token, never from the body: a reply that
	// could name its own author is a reply anybody could sign as support.
	a := actor.From(r.Context())
	out, err := s.ops.Answer(r.Context(), id, req.Body,
		s.platformAuthorLabel(r, a.UserID), req.Status)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// platformAuthorLabel is who a support reply is from.
//
// A label rather than a user id on the message, because the platform's staff
// are not rows in any tenant's app_user table and a foreign key would have
// nowhere to point.
func (s *Server) platformAuthorLabel(r *http.Request, userID uuid.UUID) string {
	if label := strings.TrimSpace(r.Header.Get("X-Support-Agent")); label != "" {
		return label
	}
	return "RawSyst Support"
}

func parseUUIDs(raw []string, field string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, v := range raw {
		if strings.TrimSpace(v) == "" {
			continue
		}
		id, err := parseUUID(strings.TrimSpace(v), field)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}
