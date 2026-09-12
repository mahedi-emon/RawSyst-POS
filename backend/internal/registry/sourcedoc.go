// The workflow that takes a published document and ends in a legal value.
//
//	fetch or upload -> decode -> normalise -> read -> validate
//	   -> preview -> apply -> audit -> the engine computes with it
//
// # What each stage is for
//
// FETCH or UPLOAD puts the artefact in the database with its SHA-256, its
// address and the moment it arrived. That row is evidence and never changes.
// Fetching is bounded — one address, one authority, a timeout, a size ceiling —
// because a regulatory importer that can be pointed anywhere is a request
// forgery engine with a ministry's name on it.
//
// DECODE and NORMALISE turn the artefact into the sentences a person reads.
// READ locates each field of the rule in those sentences and reports what it
// found with the sentence it found it in. VALIDATE puts the result through the
// same check every other route into this registry goes through.
//
// PREVIEW is where a person sees all of that: the document, its hash, the
// figures, the article behind each one and the sentence supporting it. APPLY is
// their act, and it goes through `RecordRule`, so a value imported from a
// document supersedes by date, is audited, invalidates the resolver's cache and
// is refused for exactly the same reasons a hand-typed one would be.
//
// # What this deliberately does not do
//
// It does not apply anything on its own, and the refresh job does not either.
// A machine reading of a statute is a reading. The registry has always required
// a person to put their name to a legal value and it still does — what changes
// is that they are now confirming a reading they can see the evidence for,
// rather than transcribing figures into a form.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/audit"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// SourceDocument is a retrieved artefact and what was read out of it.
//
// `Content` is deliberately absent: a list of five documents would carry two
// megabytes of PDF for no reason, and the bytes are served by their own route
// when somebody actually wants to read them.
type SourceDocument struct {
	ID        uuid.UUID `json:"id"`
	RuleKey   string    `json:"rule_key"`
	Country   string    `json:"country"`
	Authority string    `json:"source_authority"`
	Title     string    `json:"title"`
	URL       string    `json:"url,omitempty"`
	Origin    string    `json:"origin"`

	MediaType string `json:"media_type"`
	ByteSize  int64  `json:"byte_size"`
	SHA256    string `json:"content_sha256"`

	RetrievedOn string `json:"retrieved_on"`
	RetrievedBy string `json:"retrieved_by,omitempty"`

	Extracted       []Extracted `json:"extracted,omitempty"`
	ExtractionError string      `json:"extraction_error,omitempty"`

	// Validation is what `ValidatePayload` says about the extraction, computed
	// on read rather than stored: the source pack can gain a field or tighten a
	// unit, and a stored verdict would go on asserting yesterday's answer.
	Validation string `json:"validation_error,omitempty"`
	Valid      bool   `json:"valid"`

	Status        string `json:"status"`
	AppliedRuleID string `json:"applied_rule_id,omitempty"`
	AppliedAt     string `json:"applied_at,omitempty"`
	RejectedWhy   string `json:"rejected_reason,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

// NewSourceDocument is a document arriving, however it arrived.
type NewSourceDocument struct {
	RuleKey string
	Country string
	Title   string

	// URL is where it came from. Required for a fetch, empty for an upload.
	URL string

	// Origin is `fetch`, `upload` or `refresh`.
	Origin string

	MediaType string
	Content   []byte
	Notes     string
}

// --- retrieval --------------------------------------------------------------

// fetchTimeout bounds one retrieval end to end.
//
// A ministry's file server is not this product's dependency and must never
// become one: a request that hangs holds a connection, a goroutine and an
// operator's screen, and the answer to "it is slow today" is to come back
// later, not to wait indefinitely.
const fetchTimeout = 30 * time.Second

// fetchRedirects is how many hops a retrieval will follow.
//
// Government sites redirect http to https and bare to www, so zero is too few.
// A chain longer than this is either a loop or somewhere the request was never
// meant to end up.
const fetchRedirects = 5

// FetchSource retrieves the document a rule's figures are published in.
//
// # Why the address is not free text
//
// The caller does not choose the host. The source pack records where each rule
// is published, and a fetch must land on that authority's site. Without that
// this route is server-side request forgery wearing a compliance badge: a
// Super Admin account, or anything that reaches one, could point "fetch the
// official source" at a cloud metadata endpoint, an internal admin port or a
// machine on the same network, and the response would be stored in the
// database and rendered back on a screen.
//
// The pack's URL is also the default, so the ordinary case is a caller passing
// no address at all.
func (s *Service) FetchSource(
	ctx context.Context, ruleKey, from string, by uuid.UUID,
) (SourceDocument, error) {
	key := strings.ToUpper(strings.TrimSpace(ruleKey))
	src, described := SourceFor(key)
	if !described {
		return SourceDocument{}, errs.Newf(errs.CodeInvalidInput,
			"The source pack does not say where %s is published, so this "+
				"product does not know which address is the authority's. "+
				"Upload the document instead.", key)
	}

	target := strings.TrimSpace(from)
	if target == "" {
		target = src.URL
	}
	if err := sameAuthority(target, src.URL); err != nil {
		return SourceDocument{}, err
	}

	content, mediaType, err := fetchDocument(ctx, target)
	if err != nil {
		return SourceDocument{}, err
	}

	return s.RecordSource(ctx, NewSourceDocument{
		RuleKey: key, Country: src.Country, Title: src.Document,
		URL: target, Origin: "fetch", MediaType: mediaType, Content: content,
	}, by)
}

// sameAuthority refuses an address that is not the one the pack publishes.
//
// Host equality, or a subdomain of it. Not a substring test: `hrsd.gov.sa.evil`
// contains `hrsd.gov.sa` and is a different site entirely, which is exactly the
// mistake this kind of check is usually written with.
func sameAuthority(target, published string) error {
	t, err := url.Parse(target)
	if err != nil || t.Scheme != "https" || t.Host == "" {
		return errs.Newf(errs.CodeInvalidInput,
			"%q is not an https address.", target)
	}
	p, err := url.Parse(published)
	if err != nil || p.Host == "" {
		return errs.New(errs.CodeInternal,
			"The source pack does not record a usable address for this rule.")
	}

	// `www.` is a presentation detail, not a different organisation. Folding it
	// away makes the published address `hrsd.gov.sa` and lets a document move
	// to `laws.hrsd.gov.sa` — a ministry reorganising its own site — without
	// letting anything else through.
	want := baseHost(p.Hostname())
	got := baseHost(t.Hostname())

	// Equal, or a name UNDER it. Only in that direction: matching the other way
	// round would treat `gov.sa` as covering `hrsd.gov.sa` and open the fetch to
	// every government site in the country. And a suffix test on the whole
	// string rather than on a label boundary would accept
	// `www.hrsd.gov.sa.example.com`, which is the mistake this kind of check is
	// usually written with.
	if got == want || strings.HasSuffix(got, "."+want) {
		return nil
	}
	return errs.Newf(errs.CodeInvalidInput,
		"This rule is published by %s and that address is on %s. A regulatory "+
			"source is fetched from the authority that publishes it and from "+
			"nowhere else. If the authority has moved the document, upload it "+
			"and record where it came from.", want, got)
}

// fetchDocument performs the bounded retrieval.
//
// # Two attempts, and the second one is IPv4
//
// Not a retry loop. Government file servers and the content delivery networks
// in front of them reset connections, and this one was observed doing it
// reproducibly over IPv6 from a host whose IPv4 path to the same address
// worked. A single retry pinned to IPv4 covers both the transient reset and the
// broken-IPv6-path case, and two attempts is where it stops: a regulatory
// importer that keeps trying is a regulatory importer that gets a deployment
// blocked at a ministry's edge.
//
// When both fail the refusal says so and points at the upload, which is not a
// consolation prize — the bytes are hashed, read, validated and applied by
// exactly the same code either way.
func fetchDocument(ctx context.Context, target string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	content, mediaType, err := fetchOnce(ctx, target, "tcp")
	if err == nil {
		return content, mediaType, nil
	}
	// The first attempt failed at the transport. Anything the server actually
	// answered — a 404, a redirect loop, a body over the ceiling — is already
	// an `*errs.Error` and is not worth asking twice.
	if errs.As(err) != nil {
		return nil, "", err
	}
	content, mediaType, retryErr := fetchOnce(ctx, target, "tcp4")
	if retryErr == nil {
		return content, mediaType, nil
	}
	return nil, "", errs.Wrap(retryErr, errs.CodeInvalidInput, fmt.Sprintf(
		"That document could not be retrieved from %s, over IPv6 or IPv4. "+
			"The site may be unreachable from this deployment, or may refuse "+
			"automated requests. Upload the document instead — everything "+
			"after retrieval is the same.", target))
}

// fetchOnce is one attempt, on one address family.
func fetchOnce(
	ctx context.Context, target, network string,
) ([]byte, string, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	client := &http.Client{
		// A fresh client per retrieval, with no keep-alive pool. This runs a
		// handful of times a year; holding idle connections to a ministry for
		// the life of the process is memory spent on nothing.
		Transport: &http.Transport{
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: 10 * time.Second,
			DialContext: func(c context.Context, _, addr string) (net.Conn, error) {
				return dialer.DialContext(c, network, addr)
			},
		},
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= fetchRedirects {
				return fmt.Errorf("stopped after %d redirects", fetchRedirects)
			}
			if r.URL.Scheme != "https" {
				return errors.New("redirected off https")
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", errs.Wrap(err, errs.CodeInvalidInput,
			"That address could not be requested.")
	}
	// Named honestly. A regulator reading their logs should be able to tell
	// what this is, and a product that disguises itself as a browser to fetch
	// a public document has started the wrong kind of relationship.
	req.Header.Set("User-Agent", "Biz1core-Regulatory/1.0 (+regulatory source retrieval)")
	req.Header.Set("Accept", "application/pdf,text/html,text/plain;q=0.9,*/*;q=0.5")

	// Returned bare, not wrapped. `fetchDocument` decides whether a transport
	// failure is worth a second attempt, and it tells them apart by whether
	// the error is one of ours.
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", errs.Newf(errs.CodeInvalidInput,
			"%s answered %d. That is not the document; recording it as one "+
				"would put an error page in the registry's evidence.",
			target, resp.StatusCode)
	}

	// Read one byte past the ceiling, so a document exactly at the limit is
	// accepted and one over it is refused rather than silently truncated. A
	// truncated statute is the worst possible input to a reader: it parses.
	content, err := io.ReadAll(io.LimitReader(resp.Body, MaxSourceBytes+1))
	if err != nil {
		return nil, "", errs.Wrap(err, errs.CodeInvalidInput,
			"That document could not be read to the end.")
	}
	if len(content) > MaxSourceBytes {
		return nil, "", errs.Newf(errs.CodeInvalidInput,
			"That document is larger than %d MB, which is past what this "+
				"product will hold as evidence.", MaxSourceBytes>>20)
	}
	if len(content) == 0 {
		return nil, "", errs.Newf(errs.CodeInvalidInput,
			"%s answered with an empty body.", target)
	}

	return content, SniffMediaType(content, resp.Header.Get("Content-Type")), nil
}

// --- recording --------------------------------------------------------------

// RecordSource stores an artefact and reads it.
//
// Extraction failing is NOT an error here. The document is still evidence, and
// a reading that could not be performed is a fact about this document worth
// keeping: it is what an operator needs in order to decide whether they have
// the wrong edition, the wrong file, or the right one that this product cannot
// yet read. The row records why, and the guided form remains open.
func (s *Service) RecordSource(
	ctx context.Context, in NewSourceDocument, by uuid.UUID,
) (SourceDocument, error) {
	key := strings.ToUpper(strings.TrimSpace(in.RuleKey))
	country := strings.ToLower(strings.TrimSpace(in.Country))
	if key == "" || country == "" {
		return SourceDocument{}, errs.Validation(
			"Say which rule and which market this document is evidence for.")
	}
	if len(in.Content) == 0 {
		return SourceDocument{}, errs.Validation("That document is empty.").
			WithField("file", "Nothing was received.")
	}
	if len(in.Content) > MaxSourceBytes {
		return SourceDocument{}, errs.Newf(errs.CodeInvalidInput,
			"That document is larger than %d MB.", MaxSourceBytes>>20)
	}

	src, described := SourceFor(key)
	title := strings.TrimSpace(in.Title)
	if title == "" && described {
		title = src.Document
	}
	if title == "" {
		return SourceDocument{}, errs.Validation(
			"Name the document.").
			WithField("title",
				"What it calls itself, as printed on it: a citation nobody "+
					"can follow is not provenance.")
	}
	authority := "mhrsd"
	if described {
		authority = src.Authority
	}

	mediaType := SniffMediaType(in.Content, in.MediaType)
	sum := sha256.Sum256(in.Content)
	hash := hex.EncodeToString(sum[:])

	// Read it before anything is written, so a document that cannot be decoded
	// at all is reported as such rather than stored and then complained about.
	fields, extractionErr := readDocument(key, mediaType, in.Content)

	var extractedJSON any
	if len(fields) > 0 {
		b, err := json.Marshal(fields)
		if err != nil {
			return SourceDocument{}, errs.Wrap(err, errs.CodeInternal,
				"That reading could not be recorded.")
		}
		extractedJSON = string(b)
	}

	var id uuid.UUID
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// The same document, retrieved again, is the same document.
		//
		// The refresh job exists to run repeatedly and an unchanged source is
		// its expected outcome every time. Returning the row that is already
		// there is the honest answer and keeps the table one row per distinct
		// document rather than one per retrieval.
		if e := tx.QueryRow(ctx, `
			SELECT id FROM regulatory_source_document
			WHERE rule_key = $1 AND country = $2 AND content_sha256 = $3`,
			key, country, hash).Scan(&id); e == nil {
			return nil
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		var actor any
		if by != uuid.Nil {
			actor = by
		}
		var urlValue any
		if u := strings.TrimSpace(in.URL); u != "" {
			urlValue = u
		}

		if e := tx.QueryRow(ctx, `
			INSERT INTO regulatory_source_document
			  (rule_key, country, source_authority, title, url, origin,
			   media_type, byte_size, content_sha256, content, retrieved_by,
			   extracted, extraction_error, notes)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,
			        nullif($13,''), nullif($14,''))
			RETURNING id`,
			key, country, authority, title, urlValue,
			strings.TrimSpace(in.Origin), mediaType, int64(len(in.Content)),
			hash, in.Content, actor, extractedJSON, extractionErr,
			strings.TrimSpace(in.Notes)).Scan(&id); e != nil {
			return e
		}

		// In the trail before anybody looks at it. Retrieving a document is
		// the act that decides what this installation believes the law says,
		// and the hash is what makes the claim checkable later.
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    actorOrNil(by),
			ActorLabel: audit.LabelFor(ctx, tx, by),
			Action:     "regulatory_source_retrieved",
			EntityType: "regulatory_source_document",
			EntityID:   &id,
			After: map[string]any{
				"rule_key": key, "country": country, "origin": in.Origin,
				"url": in.URL, "title": title, "media_type": mediaType,
				"byte_size": len(in.Content), "content_sha256": hash,
				"fields_read":      len(fields),
				"extraction_error": extractionErr,
			},
		})
	})
	if err != nil {
		return SourceDocument{}, db.Translate(err,
			"That document could not be recorded.")
	}
	return s.SourceDocument(ctx, id)
}

func actorOrNil(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// --- reading back ------------------------------------------------------------

// The columns every read of a source document takes, in the order the scanner
// expects them. `content` is appended by the single-document read alone.
const sourceDocumentColumns = `
	d.id, d.rule_key, d.country, d.source_authority, d.title,
	coalesce(d.url, ''), d.origin, d.media_type, d.byte_size,
	d.content_sha256, to_char(d.retrieved_on, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	coalesce(u.email, ''), d.extracted, coalesce(d.extraction_error, ''),
	d.status, d.applied_rule_id,
	coalesce(to_char(d.applied_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), ''),
	coalesce(d.rejected_reason, ''), coalesce(d.notes, '')`

const sourceDocumentFrom = `
	FROM regulatory_source_document d
	LEFT JOIN app_user u ON u.id = d.retrieved_by`

// sourceDocumentQuery assembles one, with the artefact or without it.
func sourceDocumentQuery(withContent bool, where string) string {
	columns := sourceDocumentColumns
	if withContent {
		columns += ", d.content"
	}
	return "SELECT " + columns + sourceDocumentFrom + " " + where
}

func scanSourceDocument(row pgx.Row, withContent bool) (SourceDocument, []byte, error) {
	var d SourceDocument
	var extracted, content []byte
	var appliedRule *uuid.UUID

	into := []any{&d.ID, &d.RuleKey, &d.Country, &d.Authority, &d.Title,
		&d.URL, &d.Origin, &d.MediaType, &d.ByteSize, &d.SHA256,
		&d.RetrievedOn, &d.RetrievedBy, &extracted, &d.ExtractionError,
		&d.Status, &appliedRule, &d.AppliedAt, &d.RejectedWhy, &d.Notes}
	if withContent {
		into = append(into, &content)
	}
	if err := row.Scan(into...); err != nil {
		return SourceDocument{}, nil, err
	}

	if appliedRule != nil {
		d.AppliedRuleID = appliedRule.String()
	}
	if len(extracted) > 0 {
		_ = json.Unmarshal(extracted, &d.Extracted)
	}
	d.Valid, d.Validation = validateExtraction(d.RuleKey, d.Extracted)
	return d, content, nil
}

// validateExtraction runs the reading through the registry's own validator.
//
// Computed on read and not stored. The source pack is versioned with the
// binary: it can gain a field or tighten a unit, and a verdict written into the
// row last year would go on asserting an answer this build no longer gives.
func validateExtraction(key string, fields []Extracted) (bool, string) {
	if len(fields) == 0 {
		return false, ""
	}
	values := make(map[string]string, len(fields))
	for _, f := range fields {
		values[f.Field] = f.Value
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return false, userMessage(err)
	}
	if err := ValidatePayload(key, payload); err != nil {
		return false, userMessage(err)
	}
	return true, ""
}

// SourceDocuments lists what has been retrieved, newest first.
func (s *Service) SourceDocuments(
	ctx context.Context, ruleKey, country string,
) ([]SourceDocument, error) {
	out := []SourceDocument{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, sourceDocumentQuery(false, `
			WHERE ($1 = '' OR d.rule_key = $1)
			  AND ($2 = '' OR d.country = $2)
			ORDER BY d.retrieved_on DESC`),
			strings.ToUpper(strings.TrimSpace(ruleKey)),
			strings.ToLower(strings.TrimSpace(country)))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			d, _, e := scanSourceDocument(rows, false)
			if e != nil {
				return e
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SourceDocument reads one, re-deriving the reading from the stored artefact.
//
// # Why it re-reads rather than returning what was stored
//
// A reading is a function of the document and of the build that read it, and
// this product's readings improve. The first version of the Saudi reader found
// six of Article 85's seven figures, because the Ministry's PDF prints "the
// full award" with the word broken in half by a kerning gap. Fixing that must
// reach the document already retrieved — and it cannot arrive by re-fetching,
// because the same bytes are the same document and this table holds one row
// per document on purpose.
//
// So the artefact is the frozen thing and the reading is derived from it, here,
// every time somebody looks. One document, decoded once, on a screen a platform
// operator opens a handful of times a year. What was read at the moment of
// APPLYING is permanent regardless: it is the rule's payload and the audit
// entry beside it.
func (s *Service) SourceDocument(
	ctx context.Context, id uuid.UUID,
) (SourceDocument, error) {
	var out SourceDocument
	var content []byte
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		d, raw, e := scanSourceDocument(tx.QueryRow(ctx,
			sourceDocumentQuery(true, `WHERE d.id = $1`), id), true)
		if e != nil {
			return e
		}
		out, content = d, raw
		return nil
	})
	if err != nil {
		return SourceDocument{}, db.Translate(err, "That document is not here.")
	}

	fields, readErr := readDocument(out.RuleKey, out.MediaType, content)
	changed := readErr != out.ExtractionError || !sameReading(fields, out.Extracted)
	out.Extracted, out.ExtractionError = fields, readErr
	out.Valid, out.Validation = validateExtraction(out.RuleKey, fields)

	// Write the fresh reading back, so the list — which cannot afford to decode
	// every artefact — agrees with the preview.
	if changed {
		var extractedJSON any
		if len(fields) > 0 {
			if b, e := json.Marshal(fields); e == nil {
				extractedJSON = string(b)
			}
		}
		_ = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `
				UPDATE regulatory_source_document
				SET extracted = $2::jsonb, extraction_error = nullif($3,'')
				WHERE id = $1`, id, extractedJSON, readErr)
			return e
		})
	}
	return out, nil
}

// readDocument decodes an artefact and reads the rule's figures out of it.
//
// A failure is returned as a sentence rather than an error: it is a fact about
// this document that belongs on the row and on the screen, not a reason to
// refuse to show somebody the document they retrieved.
func readDocument(ruleKey, mediaType string, content []byte) ([]Extracted, string) {
	text, err := DecodeSource(mediaType, content)
	if err != nil {
		return nil, userMessage(err)
	}
	got, err := Extract(ruleKey, NormaliseSource(text))
	if err != nil {
		return nil, userMessage(err)
	}
	return got.Fields, ""
}

func sameReading(a, b []Extracted) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SourceContent serves the artefact itself.
//
// The point of storing the bytes is that somebody can open what was actually
// read, rather than take this product's word for what it said.
func (s *Service) SourceContent(
	ctx context.Context, id uuid.UUID,
) ([]byte, string, string, error) {
	var content []byte
	var mediaType, title string
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT content, media_type, title
			FROM regulatory_source_document WHERE id = $1`, id).
			Scan(&content, &mediaType, &title)
	})
	if err != nil {
		return nil, "", "", db.Translate(err, "That document is not here.")
	}
	return content, mediaType, title, nil
}

// --- applying ----------------------------------------------------------------

// ApplySource records the rule the document states.
//
// Everything about the value goes through `RecordRule`: the placeholder
// refusal, the source-pack validation, superseding by date rather than
// overwriting, the audit entry and the cache invalidation. This adds the
// provenance — which document, which hash — and the link back to it.
//
// `verified` is the caller asserting that they have read the evidence on the
// preview and it says what the extraction says. That is the same assertion the
// registry has always required and it is still a person's to make: the
// difference is that they are now confirming a reading they can check, rather
// than transcribing figures into a form.
func (s *Service) ApplySource(
	ctx context.Context, id uuid.UUID, from time.Time, verified bool,
	notes string, by uuid.UUID,
) (RuleRow, error) {
	doc, err := s.SourceDocument(ctx, id)
	if err != nil {
		return RuleRow{}, err
	}
	if doc.Status == "applied" {
		return RuleRow{}, errs.Newf(errs.CodeConflict,
			"That document was already applied on %s. Applying it again "+
				"would record the same reading twice. To correct a figure, "+
				"retrieve the document again and apply the new retrieval.",
			doc.AppliedAt)
	}
	if len(doc.Extracted) == 0 {
		return RuleRow{}, errs.Newf(errs.CodeInvalidInput,
			"Nothing was read out of that document%s. It cannot be applied; "+
				"record the rule through the guided form instead.",
			suffixIf(doc.ExtractionError, ": "))
	}
	if !doc.Valid {
		return RuleRow{}, errs.Newf(errs.CodeInvalidInput,
			"What was read out of that document does not pass validation, so "+
				"it cannot become a legal value: %s", doc.Validation)
	}
	if from.IsZero() {
		return RuleRow{}, errs.Validation("Say when this takes effect.").
			WithField("effective_from",
				"A legal value applies from a date, and the product resolves "+
					"it at the date of the document being processed. For a "+
					"statute this is when the article came into force, not "+
					"when it was read.")
	}

	values := make(map[string]string, len(doc.Extracted))
	for _, f := range doc.Extracted {
		values[f.Field] = f.Value
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return RuleRow{}, errs.Wrap(err, errs.CodeInternal,
			"That reading could not be rendered as a rule.")
	}

	if strings.TrimSpace(notes) == "" {
		notes = fmt.Sprintf(
			"Read from %s, retrieved %s, SHA-256 %s.",
			doc.Title, doc.RetrievedOn, doc.SHA256)
	}

	rule, err := s.RecordRule(ctx, NewRule{
		Key: doc.RuleKey, Country: doc.Country, Payload: payload,
		From: from, Authority: doc.Authority, Document: doc.Title,
		URL: doc.URL, Blocker: true, Blocks: "feature",
		Notes: notes, Verified: verified,
	}, by)
	if err != nil {
		return RuleRow{}, err
	}

	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `
			UPDATE regulatory_source_document
			SET status = 'applied', applied_rule_id = $2, applied_at = now(),
			    applied_by = $3
			WHERE id = $1`, id, rule.ID, actorOrNil(by)); e != nil {
			return e
		}
		// Earlier candidates for the same rule are no longer candidates.
		if _, e := tx.Exec(ctx, `
			UPDATE regulatory_source_document
			SET status = 'superseded'
			WHERE rule_key = $1 AND country = $2 AND id <> $3
			  AND status = 'candidate'`,
			doc.RuleKey, doc.Country, id); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			UPDATE regulatory_rule SET source_document_id = $2 WHERE id = $1`,
			rule.ID, id); e != nil {
			return e
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    actorOrNil(by),
			ActorLabel: audit.LabelFor(ctx, tx, by),
			Action:     "regulatory_source_applied",
			EntityType: "regulatory_source_document",
			EntityID:   &id,
			After: map[string]any{
				"rule_key": doc.RuleKey, "country": doc.Country,
				"content_sha256": doc.SHA256, "rule_id": rule.ID.String(),
				"effective_from": from.Format("2006-01-02"),
				"verified":       verified, "payload": string(payload),
			},
		})
	})
	if err != nil {
		return RuleRow{}, db.Translate(err,
			"The rule was recorded and the document could not be marked "+
				"applied.")
	}

	rule.Blocks = "feature"
	return rule, nil
}

// RejectSource closes off a candidate nobody is going to apply.
//
// It is not a delete. The document was retrieved, that happened, and a
// regulatory trail that quietly loses the readings somebody decided against is
// a trail with the interesting parts removed.
func (s *Service) RejectSource(
	ctx context.Context, id uuid.UUID, why string, by uuid.UUID,
) error {
	why = strings.TrimSpace(why)
	if why == "" {
		return errs.Validation("Say why this document is not being applied.").
			WithField("reason",
				"Somebody will find this row later and need to know whether "+
					"to try again.")
	}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE regulatory_source_document
			SET status = 'rejected', rejected_reason = $2
			WHERE id = $1 AND status = 'candidate'`, id, why)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeInvalidInput,
				"That document is not an open candidate, so there is nothing "+
					"to reject.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    actorOrNil(by),
			ActorLabel: audit.LabelFor(ctx, tx, by),
			Action:     "regulatory_source_rejected",
			EntityType: "regulatory_source_document",
			EntityID:   &id,
			After:      map[string]any{"reason": why},
		})
	})
	return db.Translate(err, "That document could not be rejected.")
}

// --- re-checking ------------------------------------------------------------

// RefreshOutcome is what one re-check found.
type RefreshOutcome struct {
	RuleKey string `json:"rule_key"`
	Country string `json:"country"`
	URL     string `json:"url"`

	PreviousSHA256 string `json:"previous_sha256"`
	SHA256         string `json:"content_sha256,omitempty"`

	// Changed says the authority is serving different bytes from the ones the
	// figure in force was read out of. A candidate has been filed; nothing has
	// been applied.
	Changed bool `json:"changed"`

	DocumentID string `json:"document_id,omitempty"`
	Err        string `json:"error,omitempty"`
}

// RefreshSources re-retrieves every applied source and files a candidate where
// one has changed.
//
// # It never applies anything
//
// Not when the new reading validates, not when it is identical to the figure in
// force. A legal value here carries the name of the person who put it there and
// the date they did; a job can supply neither. And an amended statute is
// precisely where a machine reading is least trustworthy — an amendment can add
// an article, move a threshold or reword a sentence into something the reader
// half-matches — so the case where automation would save the most work is the
// case where it should do the least.
//
// # It is bounded by what it is
//
// One request per applied source, at most once a day, with the same thirty-
// second ceiling and the same authority restriction as a fetch. There are two
// applied sources on a full installation. This is not a crawler and the ceiling
// on its cost is the number of laws this product implements.
func (s *Service) RefreshSources(
	ctx context.Context, by uuid.UUID,
) ([]RefreshOutcome, error) {
	type applied struct {
		key, country, url, sha string
	}
	var sources []applied

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT DISTINCT ON (rule_key, country)
			       rule_key, country, url, content_sha256
			FROM regulatory_source_document
			WHERE status = 'applied' AND url IS NOT NULL
			ORDER BY rule_key, country, applied_at DESC`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var a applied
			if e := rows.Scan(&a.key, &a.country, &a.url, &a.sha); e != nil {
				return e
			}
			sources = append(sources, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}

	out := make([]RefreshOutcome, 0, len(sources))
	for _, a := range sources {
		o := RefreshOutcome{
			RuleKey: a.key, Country: a.country, URL: a.url,
			PreviousSHA256: a.sha,
		}

		src, described := SourceFor(a.key)
		if !described {
			o.Err = "the source pack no longer describes this rule"
			out = append(out, o)
			continue
		}
		if e := sameAuthority(a.url, src.URL); e != nil {
			o.Err = userMessage(e)
			out = append(out, o)
			continue
		}

		content, mediaType, e := fetchDocument(ctx, a.url)
		if e != nil {
			o.Err = userMessage(e)
			out = append(out, o)
			continue
		}

		sum := sha256.Sum256(content)
		o.SHA256 = hex.EncodeToString(sum[:])
		if o.SHA256 == a.sha {
			out = append(out, o)
			continue
		}

		// Different bytes. Filed as a candidate with its own provenance, read,
		// and left for somebody to look at.
		doc, e := s.RecordSource(ctx, NewSourceDocument{
			RuleKey: a.key, Country: a.country, Title: src.Document,
			URL: a.url, Origin: "refresh", MediaType: mediaType,
			Content: content,
			Notes: "Filed by the daily re-check: the authority is serving " +
				"different bytes from the ones the figure in force was read " +
				"out of. Not applied.",
		}, by)
		if e != nil {
			o.Err = userMessage(e)
			out = append(out, o)
			continue
		}
		o.Changed, o.DocumentID = true, doc.ID.String()
		out = append(out, o)
	}
	return out, nil
}

func suffixIf(s, sep string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return sep + s
}

// userMessage is the sentence an error is willing to show somebody.
//
// An extraction failure is stored on the row and rendered on a screen, so it
// has to be the message the error was written to show rather than whatever a
// wrapped cause happens to say.
func userMessage(err error) string {
	if err == nil {
		return ""
	}
	if e := errs.As(err); e != nil && strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return err.Error()
}

// baseHost lower-cases a host name and drops a leading `www.`.
//
// Not a public-suffix computation, and deliberately not: reducing
// `www.hrsd.gov.sa` to a registrable domain without the public suffix list
// gives `gov.sa`, which would open a fetch to every government site in the
// country. Dropping one well-known prefix is the whole of the leniency here.
func baseHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.TrimPrefix(host, "www.")
}
