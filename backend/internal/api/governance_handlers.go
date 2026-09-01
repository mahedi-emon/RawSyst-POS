package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/compliance"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/docs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/privacy"
)

// Document storage (D6), the PDPL module (E4, E5) and the compliance dashboard
// (E7).
//
// # The bytes arrive base64 in JSON
//
// The same convention as the logo route, for the same reason: nothing else in
// this API is multipart, and a second transport for one module would be a
// second thing to get right. The size limit is enforced twice — once on the
// encoded body here, once on the decoded bytes in the service — because a
// caller can send 12 MB of base64 that decodes to 9, and refusing after
// decoding means having decoded it.
//
// # Reading a document is a permission and an audit entry
//
// `document.view` reaches the metadata and the file. The audit entry is written
// by the service, not here, and only for personal and sensitive files: a
// delivery note being downloaded is not an event anybody will ask about, and a
// scanned ID copy is.

// --- scopes ---------------------------------------------------------------

func (s *Server) docScope(r *http.Request) (docs.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return docs.Scope{}, err
	}
	return docs.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

func (s *Server) privacyScope(r *http.Request) (privacy.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return privacy.Scope{}, err
	}
	return privacy.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

func (s *Server) complianceScope(r *http.Request) (compliance.Scope, error) {
	a := actor.From(r.Context())
	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		return compliance.Scope{}, err
	}
	return compliance.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}, nil
}

// --- documents ------------------------------------------------------------

type uploadDocumentRequest struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	FileName   string `json:"file_name"`
	// Base64 of the raw file, without a `data:` prefix.
	Data string `json:"data"`

	Classification string `json:"classification"`
	ExpiresOn      string `json:"expires_on"`
	Note           string `json:"note"`
}

func (s *Server) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	scope, err := s.docScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req uploadDocumentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Base64 is four characters per three bytes, so the encoded form of an
	// 8 MB file is about 10.7 MB. Refusing above 12 MB stops a body that could
	// never be stored before it is decoded.
	if len(req.Data) > 12<<20 {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"That file is larger than 8 MB."))
		return
	}
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(req.Data))
	if derr != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"That file could not be read. Send it base64-encoded, without a "+
				"data: prefix."))
		return
	}

	entityID, uerr := uuid.Parse(req.EntityID)
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Name the record the document belongs to."))
		return
	}

	in := docs.NewDocument{
		EntityType: req.EntityType, EntityID: entityID,
		FileName: req.FileName, Bytes: raw,
		Classification: req.Classification, Note: req.Note,
	}
	if req.ExpiresOn != "" {
		d, perr := time.Parse("2006-01-02", req.ExpiresOn)
		if perr != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"That expiry date is not a date."))
			return
		}
		in.ExpiresOn = &d
	}

	out, err := s.docs.Upload(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"document": out})
}

func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	scope, err := s.docScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	q := r.URL.Query()
	entityType := strings.TrimSpace(q.Get("entity_type"))
	// Attached to one record, or searched across the store. Two questions, one
	// route, because D6's screen is one list with a filter above it.
	if entityType != "" && q.Get("entity_id") != "" {
		entityID, uerr := uuid.Parse(q.Get("entity_id"))
		if uerr != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"That record reference is not valid."))
			return
		}
		out, err := s.docs.List(r.Context(), scope, entityType, entityID)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
		return
	}

	if q.Get("expiring") == "true" {
		days, _ := strconv.Atoi(q.Get("within_days"))
		out, err := s.docs.Expiring(r.Context(), scope, days)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
		return
	}

	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := s.docs.Search(r.Context(), scope, q.Get("q"), limit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleDownloadDocument(w http.ResponseWriter, r *http.Request) {
	scope, err := s.docScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "documentID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That document was not found."))
		return
	}

	out, err := s.docs.Download(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	etag := `"` + out.Checksum + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", out.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(out.Bytes)))
	w.Header().Set("ETag", etag)
	// Belt and braces against the sniff having been wrong: the browser is told
	// not to second-guess the type, and an attachment disposition means even a
	// type that slipped through is downloaded rather than rendered.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+sanitiseFileName(out.FileName)+`"`)
	// Personal files are never cached by a shared proxy. E4 makes the shop
	// responsible for where a copy of an ID document ends up.
	if out.Classification == "personal" ||
		out.Classification == "sensitive_personal" {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Bytes)
}

// sanitiseFileName keeps a quote or a newline in a stored name out of the
// Content-Disposition header, where either would end the value early.
func sanitiseFileName(name string) string {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '\r', '\n':
			return -1
		}
		if r < 32 {
			return -1
		}
		return r
	}, name)
	if clean == "" {
		return "document"
	}
	return clean
}

func (s *Server) handleRemoveDocument(w http.ResponseWriter, r *http.Request) {
	scope, err := s.docScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "documentID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That document was not found."))
		return
	}
	if err := s.docs.Remove(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- consent --------------------------------------------------------------

type recordConsentRequest struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	LawfulBasis string `json:"lawful_basis"`
	Purpose     string `json:"purpose"`
	Channel     string `json:"channel"`
	Proof       string `json:"proof"`
}

func (s *Server) handleListConsents(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	q := r.URL.Query()
	var subjectID *uuid.UUID
	if raw := q.Get("subject_id"); raw != "" {
		id, uerr := uuid.Parse(raw)
		if uerr != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"That person reference is not valid."))
			return
		}
		subjectID = &id
	}
	out, err := s.privacy.Consents(r.Context(), scope,
		strings.TrimSpace(q.Get("subject_type")), subjectID,
		q.Get("live") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleRecordConsent(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req recordConsentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	subjectID, uerr := uuid.Parse(req.SubjectID)
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Name the person the agreement is with."))
		return
	}
	out, err := s.privacy.RecordConsent(r.Context(), scope, privacy.NewConsent{
		SubjectType: req.SubjectType, SubjectID: subjectID,
		LawfulBasis: req.LawfulBasis, Purpose: req.Purpose,
		Channel: req.Channel, Proof: req.Proof,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"consent": out})
}

func (s *Server) handleWithdrawConsent(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "consentID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That agreement was not found."))
		return
	}
	out, err := s.privacy.WithdrawConsent(r.Context(), scope, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"consent": out})
}

// --- data subject requests -------------------------------------------------

type openRequestRequest struct {
	Kind           string `json:"kind"`
	SubjectType    string `json:"subject_type"`
	SubjectID      string `json:"subject_id"`
	SubjectName    string `json:"subject_name"`
	SubjectContact string `json:"subject_contact"`
}

type closeRequestRequest struct {
	Outcome string `json:"outcome"`
	Note    string `json:"note"`
}

type extendRequestRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleListDSR(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.Requests(r.Context(), scope,
		r.URL.Query().Get("open") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleOpenDSR(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req openRequestRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	in := privacy.NewRequest{
		Kind: req.Kind, SubjectType: req.SubjectType,
		SubjectName: req.SubjectName, SubjectContact: req.SubjectContact,
	}
	if req.SubjectID != "" {
		id, uerr := uuid.Parse(req.SubjectID)
		if uerr != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"That person reference is not valid."))
			return
		}
		in.SubjectID = &id
	}
	out, err := s.privacy.OpenRequest(r.Context(), scope, in)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"request": out})
}

func (s *Server) handleExtendDSR(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "requestID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That request was not found."))
		return
	}
	var req extendRequestRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.ExtendRequest(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"request": out})
}

func (s *Server) handleCloseDSR(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "requestID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That request was not found."))
		return
	}
	var req closeRequestRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.CloseRequest(r.Context(), scope, id,
		req.Outcome, req.Note)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"request": out})
}

// --- incidents -------------------------------------------------------------

type logIncidentRequest struct {
	Title          string `json:"title"`
	WhatHappened   string `json:"what_happened"`
	DataCategories string `json:"data_categories"`
	Subjects       *int   `json:"subjects_affected"`
	Consequences   string `json:"consequences"`
	Containment    string `json:"containment"`
	Severity       string `json:"severity"`
	DiscoveredAt   string `json:"discovered_at"`
}

type notifyIncidentRequest struct {
	Who string `json:"who"`
}

type closeIncidentRequest struct {
	Containment string `json:"containment"`
}

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.Incidents(r.Context(), scope,
		r.URL.Query().Get("open") == "true")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleLogIncident(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req logIncidentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Defaulted to now rather than refused, because the 72 hours run from
	// becoming aware and somebody logging an incident the moment they find it
	// should not be stopped by a date field.
	discovered := time.Now().UTC()
	if req.DiscoveredAt != "" {
		t, perr := time.Parse(time.RFC3339, req.DiscoveredAt)
		if perr != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"That discovery time is not a time."))
			return
		}
		discovered = t.UTC()
	}

	out, err := s.privacy.LogIncident(r.Context(), scope, privacy.NewIncident{
		Title: req.Title, WhatHappened: req.WhatHappened,
		DataCategories: req.DataCategories, Subjects: req.Subjects,
		Consequences: req.Consequences, Containment: req.Containment,
		Severity: req.Severity, DiscoveredAt: discovered,
	})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"incident": out})
}

func (s *Server) handleNotifyIncident(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "incidentID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That incident was not found."))
		return
	}
	var req notifyIncidentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.MarkNotified(r.Context(), scope, id, req.Who)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"incident": out})
}

func (s *Server) handleCloseIncident(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "incidentID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That incident was not found."))
		return
	}
	var req closeIncidentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.CloseIncident(r.Context(), scope, id, req.Containment)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"incident": out})
}

// --- the register: RoPA, retention, holds, destruction ---------------------

func (s *Server) handleListActivities(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.Activities(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSaveActivity(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req privacy.Activity
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.SaveActivity(r.Context(), scope, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"activity": out})
}

func (s *Server) handleRemoveActivity(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "activityID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That activity was not found."))
		return
	}
	if err := s.privacy.RemoveActivity(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListRetentions(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.Retentions(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSaveRetention(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req privacy.Retention
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.SaveRetention(r.Context(), scope, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": out})
}

func (s *Server) handleListHolds(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.Holds(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handlePlaceHold(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req privacy.Hold
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.PlaceHold(r.Context(), scope, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"hold": out})
}

func (s *Server) handleReleaseHold(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	id, uerr := uuid.Parse(chi.URLParam(r, "holdID"))
	if uerr != nil {
		httpx.Error(w, r, errs.New(errs.CodeNotFound,
			"That hold was not found."))
		return
	}
	if err := s.privacy.ReleaseHold(r.Context(), scope, id); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListDestructions(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.Destructions(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// --- settings and disclosures ----------------------------------------------

func (s *Server) handleGetPrivacySettings(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.Settings(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"settings": out})
}

func (s *Server) handleSavePrivacySettings(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req privacy.Settings
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.SaveSettings(r.Context(), scope, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"settings": out})
}

func (s *Server) handleGetDisclosure(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.Disclosure(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"disclosure": out})
}

func (s *Server) handleSaveDisclosure(w http.ResponseWriter, r *http.Request) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req privacy.Disclosure
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.SaveDisclosure(r.Context(), scope, req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"disclosure": out})
}

// handleListSubprocessors is the platform's own disclosure.
//
// Authenticated rather than permission-gated: a tenant's RoPA has to name who
// else touches their data, the answer is the same for every tenant, and it is
// the platform admitting something about itself rather than reading anything
// of the tenant's.
func (s *Server) handleListSubprocessors(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := s.privacyScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.Subprocessors(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleSaveSubprocessor(
	w http.ResponseWriter, r *http.Request,
) {
	var req privacy.Subprocessor
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.privacy.SaveSubprocessor(r.Context(), req)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"subprocessor": out})
}

// --- the compliance dashboard ----------------------------------------------

func (s *Server) handleComplianceReport(
	w http.ResponseWriter, r *http.Request,
) {
	scope, err := s.complianceScope(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, err := s.compliance.Read(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"report": out})
}
