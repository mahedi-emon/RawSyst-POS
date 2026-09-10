// The Platform Owner's regulatory source workflow (E8).
//
// Six routes and one shape: a document arrives, it is read, a person looks at
// the reading with the sentences behind it, and applying it records the legal
// value. Everything is Super Admin, for the same reason the rules themselves
// are — the Saudi Labour Law is not one business's setting.
package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

func (s *Server) handleListRegulatorySources(w http.ResponseWriter, r *http.Request) {
	out, err := s.rules.SourceDocuments(r.Context(),
		r.URL.Query().Get("rule_key"), r.URL.Query().Get("country"))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handleGetRegulatorySource(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "sourceID"), "source_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out, e := s.rules.SourceDocument(r.Context(), id)
	if e != nil {
		httpx.Error(w, r, e)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"document": out})
}

// handleRegulatorySourceContent serves the artefact itself.
//
// The point of keeping the bytes is that somebody can open what was actually
// read rather than take this product's word for it. Served as an attachment:
// rendering an arbitrary retrieved document inline in the operator's browser,
// on the platform's own origin, would make a fetched HTML page a script running
// with a Super Admin session behind it.
func (s *Server) handleRegulatorySourceContent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "sourceID"), "source_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	content, mediaType, title, e := s.rules.SourceContent(r.Context(), id)
	if e != nil {
		httpx.Error(w, r, e)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+safeFilename(title, mediaType)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// safeFilename keeps a document's own name without letting it write a header.
func safeFilename(title, mediaType string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == ' ', r == '-', r == '_':
			return '-'
		default:
			return -1
		}
	}, title)
	if len(name) > 60 {
		name = name[:60]
	}
	if name == "" {
		name = "regulatory-source"
	}
	switch {
	case strings.Contains(mediaType, "pdf"):
		return name + ".pdf"
	case strings.Contains(mediaType, "html"):
		return name + ".html"
	default:
		return name + ".txt"
	}
}

type fetchRegulatorySourceRequest struct {
	RuleKey string `json:"rule_key"`

	// URL is optional. The source pack records where each rule is published
	// and that is the default; passing one lets an operator name a different
	// document ON THE SAME AUTHORITY'S SITE, which is what happens when a
	// ministry reorganises its file paths.
	URL string `json:"url"`
}

// handleFetchRegulatorySource retrieves the official document.
func (s *Server) handleFetchRegulatorySource(w http.ResponseWriter, r *http.Request) {
	var req fetchRegulatorySourceRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	a := actor.From(r.Context())
	out, err := s.rules.FetchSource(r.Context(), req.RuleKey, req.URL, a.UserID)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"document": out})
}

// handleUploadRegulatorySource takes the document from the operator.
//
// The other half of fetching, and not a lesser one. A ministry may block
// automated requests, publish behind a portal, or hand the document out at a
// counter. None of that is a software condition: the bytes arrive, they are
// hashed, read, validated and applied by exactly the same code. What the
// upload cannot do is claim an address it did not come from, so `url` here is
// recorded as provenance the operator asserts rather than as somewhere this
// product retrieved anything.
func (s *Server) handleUploadRegulatorySource(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(registry.MaxSourceBytes + 1); err != nil {
		httpx.Error(w, r, errs.Wrap(err, errs.CodeInvalidInput,
			"That upload could not be read. Send the document as a file in a "+
				"multipart form."))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, r, errs.Validation("Attach the document.").
			WithField("file", "The published PDF, HTML or text of the "+
				"regulation, as the authority issued it."))
		return
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, registry.MaxSourceBytes+1))
	if err != nil {
		httpx.Error(w, r, errs.Wrap(err, errs.CodeInvalidInput,
			"That file could not be read to the end."))
		return
	}
	if len(content) > registry.MaxSourceBytes {
		httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
			"That document is larger than %d MB, which is past what this "+
				"product will hold as evidence.", registry.MaxSourceBytes>>20))
		return
	}

	declared := header.Header.Get("Content-Type")
	a := actor.From(r.Context())
	out, e := s.rules.RecordSource(r.Context(), registry.NewSourceDocument{
		RuleKey:   r.FormValue("rule_key"),
		Country:   r.FormValue("country"),
		Title:     r.FormValue("title"),
		URL:       r.FormValue("url"),
		Origin:    "upload",
		MediaType: declared,
		Content:   content,
		Notes:     r.FormValue("notes"),
	}, a.UserID)
	if e != nil {
		httpx.Error(w, r, e)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"document": out})
}

type applyRegulatorySourceRequest struct {
	From string `json:"effective_from"`

	// Verified is the operator asserting they have read the evidence on the
	// preview and it says what the reading says. Recording without it stages
	// the value for somebody else to confirm, exactly as the rules screen does.
	Verified bool   `json:"verified"`
	Notes    string `json:"notes"`
}

func (s *Server) handleApplyRegulatorySource(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "sourceID"), "source_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req applyRegulatorySourceRequest
	if e := httpx.Decode(r, &req); e != nil {
		httpx.Error(w, r, e)
		return
	}
	from, e := optionalDate(req.From, "effective_from")
	if e != nil {
		httpx.Error(w, r, e)
		return
	}
	if from == nil {
		httpx.Error(w, r, errs.Validation("Say when this takes effect.").
			WithField("effective_from",
				"For a statute this is when the article came into force, not "+
					"when the document was read."))
		return
	}

	a := actor.From(r.Context())
	rule, e := s.rules.ApplySource(r.Context(), id, *from, req.Verified,
		req.Notes, a.UserID)
	if e != nil {
		httpx.Error(w, r, e)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"rule": rule})
}

type rejectRegulatorySourceRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleRejectRegulatorySource(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "sourceID"), "source_id")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req rejectRegulatorySourceRequest
	if e := httpx.Decode(r, &req); e != nil {
		httpx.Error(w, r, e)
		return
	}
	a := actor.From(r.Context())
	if e := s.rules.RejectSource(r.Context(), id, req.Reason, a.UserID); e != nil {
		httpx.Error(w, r, e)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "rejected"})
}
