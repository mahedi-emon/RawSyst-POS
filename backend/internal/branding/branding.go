// Package branding holds a client's own logo, blueprint I2.
//
// I2 makes the logo customizable per template type. This package builds the
// half that can honestly be built today: a client sets their own mark through
// the Back Office, with nobody editing source for them. Nothing renders it yet
// — `receipt.ts` is a 42-column plain-text thermal receipt that cannot carry an
// image, and no A4 or PDF surface exists — so this stores, validates and serves
// the bytes and stops there.
//
// # The client is not trusted about what they uploaded
//
// The content type is decided by SNIFFING the bytes, never by reading what the
// request claimed. A caller that could name its own type could have a browser
// render whatever it liked from this origin, which is how an "image" upload
// becomes stored cross-site scripting. `image.DecodeConfig` both identifies the
// format and yields the dimensions, so one call answers "is this really a PNG"
// and "how big is it" together.
//
// SVG is refused for the same reason and cannot be made safe by sniffing: it is
// executable markup by design.
package branding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	// Registered for their decoders. DecodeConfig reads only the header, so
	// this never allocates a full bitmap for a file it is about to refuse.
	_ "image/jpeg"
	_ "image/png"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The limits, mirrored from 0054's constraints so a client gets a sentence
// naming the problem rather than a constraint violation naming an index.
const (
	MaxBytes     = 512 * 1024
	MinDimension = 32
	MaxDimension = 2048
)

// Service stores and serves company logos.
type Service struct {
	pool *db.Pool
}

func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and about which company. Mirrors egs.Scope rather than
// inventing a second shape for the same idea.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Logo is what the Back Office shows about a company's mark. The bytes are
// deliberately absent: a screen listing settings does not need half a megabyte
// of image to tell somebody one is set.
type Logo struct {
	ContentType string `json:"content_type"`
	ByteSize    int    `json:"byte_size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Checksum    string `json:"checksum"`
	UploadedAt  string `json:"uploaded_at"`
}

// Image is the logo itself, for the route that serves the file.
type Image struct {
	ContentType string
	Bytes       []byte
	Checksum    string
}

// Put stores or replaces a company's logo.
//
// Replacing is an upsert on the company, because a logo is current
// configuration rather than a posted document: there is no history to keep and
// nobody would look at the six marks a client tried before settling.
func (s *Service) Put(ctx context.Context, scope Scope, raw []byte) (Logo, error) {
	contentType, width, height, err := inspect(raw)
	if err != nil {
		return Logo{}, err
	}

	sum := sha256.Sum256(raw)
	checksum := hex.EncodeToString(sum[:])

	var out Logo
	err = s.pool.Tx(ctx, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			INSERT INTO company_logo
			  (company_id, tenant_id, content_type, bytes, byte_size,
			   width, height, checksum, uploaded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (company_id) DO UPDATE SET
			  content_type = EXCLUDED.content_type,
			  bytes        = EXCLUDED.bytes,
			  byte_size    = EXCLUDED.byte_size,
			  width        = EXCLUDED.width,
			  height       = EXCLUDED.height,
			  checksum     = EXCLUDED.checksum,
			  uploaded_by  = EXCLUDED.uploaded_by,
			  uploaded_at  = now()
			RETURNING content_type, byte_size, width, height, checksum,
			          to_char(uploaded_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00')`,
			scope.CompanyID, scope.TenantID, contentType, raw, len(raw),
			width, height, checksum, scope.UserID).
			Scan(&out.ContentType, &out.ByteSize, &out.Width, &out.Height,
				&out.Checksum, &out.UploadedAt)
	})
	if err != nil {
		if errs.As(err) != nil {
			return Logo{}, err
		}
		return Logo{}, db.Translate(err, "That logo could not be saved.")
	}
	return out, nil
}

// Get reports what is set, without the bytes. Absent is not an error: a company
// with no logo is the ordinary state, and the screen renders it as an empty
// panel rather than a failure.
func (s *Service) Get(ctx context.Context, scope Scope) (*Logo, error) {
	var out Logo
	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		// The reads check the company exists as well as the writes.
		//
		// "No logo set" and "no such company" are different answers and were
		// being given the same one, because a company_logo row for a company
		// that is not this caller's is simply absent — so naming ANY id, from
		// any tenant, came back as a cheerful `{"logo": null}`. That is a
		// caller learning nothing about the logo and something about the API:
		// it does not check what it was handed.
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		e := tx.QueryRow(ctx, `
			SELECT content_type, byte_size, width, height, checksum,
			       to_char(uploaded_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00')
			FROM company_logo WHERE company_id = $1`, scope.CompanyID).
			Scan(&out.ContentType, &out.ByteSize, &out.Width, &out.Height,
				&out.Checksum, &out.UploadedAt)
		return e
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, db.Translate(err, "That logo could not be read.")
	}
	return &out, nil
}

// Image serves the file itself.
//
// Another tenant's logo reads as absent rather than forbidden, because
// row-level security hides the row and that is the right answer: whether
// another company has set a logo is not this caller's business.
func (s *Service) Image(ctx context.Context, scope Scope) (Image, error) {
	var out Image
	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT content_type, bytes, checksum
			FROM company_logo WHERE company_id = $1`, scope.CompanyID).
			Scan(&out.ContentType, &out.Bytes, &out.Checksum)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Image{}, errs.New(errs.CodeNotFound, "This business has no logo set.")
	}
	if err != nil {
		return Image{}, db.Translate(err, "That logo could not be read.")
	}
	return out, nil
}

// Remove clears a company's logo, returning it to the default RawSyst mark.
//
// Removing one that is not there succeeds. A client pressing Remove twice, or
// on a company that never had one, has got the outcome they asked for, and
// refusing the second press would be pedantry dressed as correctness.
func (s *Service) Remove(ctx context.Context, scope Scope) error {
	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `DELETE FROM company_logo WHERE company_id = $1`,
			scope.CompanyID)
		return e
	})
	if err != nil {
		if errs.As(err) != nil {
			return err
		}
		return db.Translate(err, "That logo could not be removed.")
	}
	return nil
}

// --- validation -----------------------------------------------------------

// inspect decides what the bytes actually are, and whether they are usable.
//
// The order matters. Size is checked first because it is the cheapest refusal
// and the commonest, then the format is read from the header, then the
// dimensions. A 40 MB file is refused without ever being handed to a decoder.
func inspect(raw []byte) (contentType string, width, height int, err error) {
	if len(raw) == 0 {
		return "", 0, 0, errs.New(errs.CodeInvalidInput,
			"That file is empty. Choose a PNG or JPEG image.")
	}
	if len(raw) > MaxBytes {
		return "", 0, 0, errs.Newf(errs.CodeInvalidInput,
			"That image is %d KB. A logo must be %d KB or smaller — it is "+
				"reprinted on every receipt.", len(raw)/1024, MaxBytes/1024)
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", 0, 0, errs.New(errs.CodeInvalidInput,
			"That file could not be read as an image. Upload a PNG or a JPEG.")
	}

	switch format {
	case "png":
		contentType = "image/png"
	case "jpeg":
		contentType = "image/jpeg"
	default:
		// Reached only if a decoder is registered that this does not name, so
		// adding an import cannot silently widen what is accepted.
		return "", 0, 0, errs.Newf(errs.CodeInvalidInput,
			"%s images are not supported. Upload a PNG or a JPEG.", format)
	}

	if cfg.Width < MinDimension || cfg.Height < MinDimension {
		return "", 0, 0, errs.Newf(errs.CodeInvalidInput,
			"That image is %d by %d pixels. A logo must be at least %d pixels "+
				"on each side to print legibly.", cfg.Width, cfg.Height, MinDimension)
	}
	if cfg.Width > MaxDimension || cfg.Height > MaxDimension {
		return "", 0, 0, errs.Newf(errs.CodeInvalidInput,
			"That image is %d by %d pixels. A logo must be %d pixels or fewer "+
				"on each side; no receipt printer resolves more.",
			cfg.Width, cfg.Height, MaxDimension)
	}

	return contentType, cfg.Width, cfg.Height, nil
}

// requireCompany refuses a company this caller cannot see.
//
// Row-level security already hides another tenant's company, so this turns a
// silent no-op into a 404 — a client whose upload quietly did nothing is worse
// off than one who is told the business was not found.
func requireCompany(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM company WHERE id = $1)`, companyID).
		Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errs.New(errs.CodeNotFound, "That business was not found.")
	}
	return nil
}
