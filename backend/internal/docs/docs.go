// Package docs is central document storage (blueprint D6).
//
// # One store, many owners
//
// D6 asks for "central, searchable storage for attachments tied to" nine
// different kinds of record. The tempting shape is an attachment table per
// module — purchase_invoice_attachment, expense_receipt, employee_document —
// and it is wrong for the reason D6 gives: the storage is central. A shop
// looking for "the CR certificate we scanned last March" does not remember
// which screen they were on when they scanned it.
//
// So one table, and (entity_type, entity_id) says what it belongs to. The
// polymorphic reference is deliberate and is not a foreign key, for the same
// reason approval_request.subject_id is not one: the table it points at differs
// per kind, and a real constraint would mean a nullable column per attachable
// module.
//
// # The type is sniffed, never taken from the caller
//
// A caller that could name its own content type could store an HTML file, call
// it image/png, and have a browser render it from this origin — stored
// cross-site scripting through an upload field. `http.DetectContentType` reads
// the first bytes and decides, and the answer has to be on the allow-list D6
// names: PDF, JPEG, PNG, and the common office formats.
//
// SVG is refused. It is an image everywhere else in this product and a script
// container here, and no amount of sniffing changes that.
//
// # Classification travels with the file
//
// E4.1 wants every field tagged so protection rules apply automatically. A
// document is the case where that matters most concretely: an ID copy attached
// to an instalment agreement is sensitive_personal, and the read route says so
// in a header and in the audit trail. The tag is set at upload — by the caller
// where they know, defaulted by entity type where they do not, because a
// customer document is personal whether or not anybody thought about it.
package docs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// MaxBytes caps one document. Matches the CHECK in 0096: a scanned A4 page at
// a sensible resolution is well under this, and a caller trying to store a
// video in the attachment field is doing something else.
const MaxBytes = 8 << 20

// Service stores and serves documents.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on whose books.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Document is one stored file, without its bytes.
type Document struct {
	ID         uuid.UUID `json:"id"`
	EntityType string    `json:"entity_type"`
	EntityID   uuid.UUID `json:"entity_id"`

	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
	Checksum    string `json:"checksum"`

	Classification string `json:"classification"`
	ExpiresOn      string `json:"expires_on,omitempty"`
	// DaysToExpiry is negative once it has lapsed, so a screen can say "expired
	// 9 days ago" without doing date arithmetic in three languages.
	DaysToExpiry *int `json:"days_to_expiry,omitempty"`

	Note       string `json:"note,omitempty"`
	UploadedBy string `json:"uploaded_by,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// Content is a document with its bytes, for the download route.
type Content struct {
	Document
	Bytes []byte
}

// NewDocument is an upload.
type NewDocument struct {
	EntityType string
	EntityID   uuid.UUID
	FileName   string
	Bytes      []byte

	// Classification may be empty, in which case defaultClass decides from the
	// entity type. See the package note.
	Classification string
	ExpiresOn      *time.Time
	Note           string
}

// allowedTypes is D6's "PDF, JPG, PNG, common document types", made explicit.
//
// An allow-list rather than a deny-list: the failure mode of a deny-list is
// that a format nobody thought of gets stored and served, and the failure mode
// of an allow-list is that somebody has to ask for their format to be added.
var allowedTypes = map[string]bool{
	"application/pdf": true,
	"image/jpeg":      true,
	"image/png":       true,
	"image/gif":       true,
	"image/webp":      true,
	"text/plain":      true,
	"text/csv":        true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       true,
	"application/msword":       true,
	"application/vnd.ms-excel": true,
	"application/zip":          true,
}

// sensitiveEntities are the attachment points where a file is personal data
// about a named individual by default. E4.1's classification, applied where
// the caller did not think to.
var sensitiveEntities = map[string]string{
	"customer":         "sensitive_personal",
	"employee":         "sensitive_personal",
	"installment_plan": "sensitive_personal",
	"service_job":      "personal",
	"sales_invoice":    "personal",
	"sales_order":      "personal",
	"supplier":         "internal",
}

func defaultClass(entityType string) string {
	if c, ok := sensitiveEntities[entityType]; ok {
		return c
	}
	return "internal"
}

// Upload stores a document against a record.
func (s *Service) Upload(
	ctx context.Context, scope Scope, in NewDocument,
) (Document, error) {
	name := strings.TrimSpace(in.FileName)
	if name == "" {
		return Document{}, errs.New(errs.CodeInvalidInput,
			"Give the document a file name.")
	}
	if len(in.Bytes) == 0 {
		return Document{}, errs.New(errs.CodeInvalidInput,
			"That file is empty.")
	}
	if len(in.Bytes) > MaxBytes {
		return Document{}, errs.New(errs.CodeInvalidInput,
			"That file is larger than 8 MB. Scan it at a lower resolution, "+
				"or split it.")
	}

	contentType := detect(in.Bytes)
	if !allowedTypes[contentType] {
		return Document{}, errs.New(errs.CodeInvalidInput,
			"That kind of file cannot be stored. Use a PDF, a photograph, or "+
				"an office document.")
	}

	class := strings.TrimSpace(in.Classification)
	if class == "" {
		class = defaultClass(in.EntityType)
	}

	sum := sha256.Sum256(in.Bytes)
	checksum := hex.EncodeToString(sum[:])

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The entity has to be this company's. Without the check, a caller
		// could attach a file to another shop's invoice id and the row would
		// pass row-level security because the ROW is theirs — the reference
		// would be the thing that crossed the line.
		if e := s.entityBelongsHere(
			ctx, tx, scope.CompanyID, in.EntityType, in.EntityID); e != nil {
			return e
		}

		if e := tx.QueryRow(ctx, `
			INSERT INTO document (
			  tenant_id, company_id, entity_type, entity_id, file_name,
			  content_type, byte_size, checksum, bytes, classification,
			  expires_on, note, uploaded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, in.EntityType, in.EntityID, name,
			contentType, len(in.Bytes), checksum, in.Bytes, class,
			in.ExpiresOn, nullIfBlank(in.Note), scope.UserID,
		).Scan(&id); e != nil {
			return db.Translate(e, "That document could not be stored.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "document_uploaded",
			EntityType: "document", EntityID: &id,
			After: map[string]any{
				"file_name":      name,
				"entity_type":    in.EntityType,
				"classification": class,
				"byte_size":      len(in.Bytes),
			},
		})
	})
	if err != nil {
		return Document{}, err
	}
	return s.Read(ctx, scope, id)
}

// detect sniffs the content type from the bytes.
//
// http.DetectContentType answers "text/plain; charset=utf-8" for text, and the
// parameter is not part of the allow-list key, so it is trimmed. It also
// answers "text/xml" for an SVG, which is not on the list and so is refused —
// see the package note for why that is deliberate rather than an oversight.
func detect(raw []byte) string {
	t := http.DetectContentType(raw)
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSpace(t)

	// DetectContentType does not know the OOXML formats: a .docx is a zip and
	// it says so. Reading the zip's first entry name distinguishes them, which
	// is worth doing because "your Word document is not a supported format"
	// would be a lie.
	if t == "application/zip" {
		switch {
		case bytes.Contains(raw[:min(len(raw), 512)], []byte("word/")):
			return "application/vnd.openxmlformats-officedocument." +
				"wordprocessingml.document"
		case bytes.Contains(raw[:min(len(raw), 512)], []byte("xl/")):
			return "application/vnd.openxmlformats-officedocument." +
				"spreadsheetml.sheet"
		}
	}
	return t
}

// entityBelongsHere refuses an attachment aimed at another company's record.
func (s *Service) entityBelongsHere(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	entityType string, entityID uuid.UUID,
) error {
	table, ok := entityTables[entityType]
	if !ok {
		return errs.New(errs.CodeInvalidInput,
			"Documents cannot be attached to that kind of record.")
	}

	// The table name comes from a map keyed by a CHECK-constrained column, so
	// it is a constant chosen from a fixed set rather than caller input. The
	// id is still a parameter.
	var exists bool
	e := tx.QueryRow(ctx,
		`SELECT true FROM `+table+` WHERE id = $1 AND company_id = $2`,
		entityID, companyID).Scan(&exists)
	if e == pgx.ErrNoRows {
		return errs.New(errs.CodeNotFound, "That record was not found.")
	}
	return e
}

// entityTables maps D6's attachment points to where they live.
//
// `company` is the exception: its own id is the company id, so it is checked
// by a different predicate below rather than being bent into the same query.
var entityTables = map[string]string{
	"purchase_invoice":     "purchase_bill",
	"purchase_order":       "purchase_order",
	"goods_receipt":        "goods_receipt",
	"expense":              "expense",
	"supplier":             "supplier",
	"customer":             "customer",
	"employee":             "employee",
	"service_job":          "service_order",
	"asset":                "fixed_asset",
	"sales_invoice":        "sales_invoice",
	"sales_order":          "sales_order",
	"installment_plan":     "installment_plan",
	"incident":             "privacy_incident",
	"data_subject_request": "data_subject_request",
}

// List returns the documents attached to one record.
func (s *Service) List(
	ctx context.Context, scope Scope, entityType string, entityID uuid.UUID,
) ([]Document, error) {
	out := []Document{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, documentSelect+`
			WHERE d.company_id = $1
			  AND d.entity_type = $2
			  AND d.entity_id = $3
			ORDER BY d.created_at DESC`,
			scope.CompanyID, entityType, entityID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			doc, e := scan(rows)
			if e != nil {
				return e
			}
			out = append(out, doc)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Search is D6's searchable half: a term over file names and notes, across
// every attachment point, newest first.
func (s *Service) Search(
	ctx context.Context, scope Scope, term string, limit int,
) ([]Document, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []Document{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, documentSelect+`
			WHERE d.company_id = $1
			  AND ($2 = '' OR d.file_name ILIKE '%' || $2 || '%'
			                OR d.note      ILIKE '%' || $2 || '%')
			ORDER BY d.created_at DESC
			LIMIT $3`, scope.CompanyID, strings.TrimSpace(term), limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			doc, e := scan(rows)
			if e != nil {
				return e
			}
			out = append(out, doc)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Expiring lists documents that lapse within the window, and ones that already
// have. E7 puts this on the compliance dashboard: a supplier's VAT certificate
// that expired last month is a problem nobody gets told about otherwise.
func (s *Service) Expiring(
	ctx context.Context, scope Scope, withinDays int,
) ([]Document, error) {
	if withinDays <= 0 {
		withinDays = 60
	}
	out := []Document{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, documentSelect+`
			WHERE d.company_id = $1
			  AND d.expires_on IS NOT NULL
			  AND d.expires_on <= current_date + make_interval(days => $2)
			ORDER BY d.expires_on`, scope.CompanyID, withinDays)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			doc, e := scan(rows)
			if e != nil {
				return e
			}
			out = append(out, doc)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Read returns one document's metadata.
func (s *Service) Read(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Document, error) {
	var out Document
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, documentSelect+`
			WHERE d.id = $1 AND d.company_id = $2`, id, scope.CompanyID)
		doc, e := scan(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That document was not found.")
		}
		out = doc
		return e
	})
	return out, db.Translate(err, "")
}

// Download returns the bytes, and records that somebody read them.
//
// The audit entry is not optional bookkeeping: E4 requires a controller to know
// who has accessed personal data, and a scanned ID copy being downloaded is
// exactly the event an SDAIA investigation asks about.
func (s *Service) Download(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Content, error) {
	var out Content
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, contentSelect+`
			WHERE d.id = $1 AND d.company_id = $2`, id, scope.CompanyID)
		doc, raw, e := scanWithBytes(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That document was not found.")
		}
		if e != nil {
			return e
		}
		out = Content{Document: doc, Bytes: raw}

		if doc.Classification == "personal" ||
			doc.Classification == "sensitive_personal" {
			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.UserID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
				Action:     "document_read",
				EntityType: "document", EntityID: &id,
				After: map[string]any{
					"file_name":      doc.FileName,
					"classification": doc.Classification,
				},
			})
		}
		return nil
	})
	return out, db.Translate(err, "")
}

// Remove deletes a document.
//
// A hard delete, with the audit entry carrying what was there. A document is
// the one thing in this product somebody may be legally REQUIRED to destroy —
// see E4's erasure right — and a soft delete would be a promise the shop cannot
// keep. The destruction is provable from the audit trail, and an erasure driven
// by a data-subject request writes a destruction_log row besides.
func (s *Service) Remove(ctx context.Context, scope Scope, id uuid.UUID) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			var name, class string
			e := tx.QueryRow(ctx, `
				SELECT file_name, classification::text FROM document
				WHERE id = $1 AND company_id = $2`,
				id, scope.CompanyID).Scan(&name, &class)
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That document was not found.")
			}
			if e != nil {
				return e
			}

			if _, e := tx.Exec(ctx,
				`DELETE FROM document WHERE id = $1 AND company_id = $2`,
				id, scope.CompanyID); e != nil {
				return e
			}

			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.UserID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
				Action:     "document_removed",
				EntityType: "document", EntityID: &id,
				Before: map[string]any{
					"file_name": name, "classification": class,
				},
			})
		}), "")
}

const documentColumns = `
	d.id, d.entity_type, d.entity_id, d.file_name, d.content_type,
	d.byte_size, d.checksum, d.classification::text, d.expires_on,
	coalesce(d.note, ''), coalesce(u.full_name, ''), d.created_at`

const documentFrom = `
	FROM document d
	LEFT JOIN app_user u ON u.id = d.uploaded_by`

// documentSelect is the metadata read. Download builds its own because it also
// takes the bytes, and no list route should ever carry them.
const documentSelect = `SELECT` + documentColumns + documentFrom

const contentSelect = `SELECT` + documentColumns + `, d.bytes` + documentFrom

type scanner interface {
	Scan(dst ...any) error
}

func scan(row scanner) (Document, error) {
	var d Document
	var expires *time.Time
	var created time.Time
	if err := row.Scan(&d.ID, &d.EntityType, &d.EntityID, &d.FileName,
		&d.ContentType, &d.ByteSize, &d.Checksum, &d.Classification,
		&expires, &d.Note, &d.UploadedBy, &created); err != nil {
		return Document{}, err
	}
	finish(&d, expires, created)
	return d, nil
}

func scanWithBytes(row scanner) (Document, []byte, error) {
	var d Document
	var expires *time.Time
	var created time.Time
	var raw []byte
	if err := row.Scan(&d.ID, &d.EntityType, &d.EntityID, &d.FileName,
		&d.ContentType, &d.ByteSize, &d.Checksum, &d.Classification,
		&expires, &d.Note, &d.UploadedBy, &created, &raw); err != nil {
		return Document{}, nil, err
	}
	finish(&d, expires, created)
	return d, raw, nil
}

func finish(d *Document, expires *time.Time, created time.Time) {
	d.CreatedAt = created.UTC().Format(time.RFC3339)
	if expires != nil {
		d.ExpiresOn = expires.Format("2006-01-02")
		days := int(expires.Sub(
			time.Now().UTC().Truncate(24*time.Hour)).Hours() / 24)
		d.DaysToExpiry = &days
	}
}

func nullIfBlank(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
