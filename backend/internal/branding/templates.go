package branding

// Document templates, blueprint I2 / P35.
//
// In the branding package rather than a new one, because it is the same idea:
// what a client puts on their own documents, configured from the Back Office
// with nobody editing source. 0054 built the logo half and could go no further
// — the only document surface was a plain-text thermal receipt that cannot
// carry an image. UI spec §5 has since built one that can.
//
// # What a template may not do
//
// It carries PRESENTATION only. Nothing here can reach a figure, a party, a tax
// number or a date: those come off the document row, which is immutable posted
// history. That boundary is what makes a template safe to change at any time —
// no setting in it can alter what a document said about the transaction it
// recorded. A footer changed today appears on a reprint of last year's invoice,
// and that is right: a reprint is a copy on today's stationery, not a reissue.

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// DocTypes are the templates a client may configure.
//
// I2 names nine. The other five — quotation, purchase order, delivery challan,
// payment receipt, customer statement — are documents this product does not
// issue, and a template for one of those is configuration for nothing. They
// arrive with the documents rather than ahead of them.
var DocTypes = []string{"standard", "simplified", "credit_note", "debit_note"}

// Template is what a client writes on one kind of document.
type Template struct {
	DocType string `json:"doc_type"`

	// Each block in both languages. I2 asks for "Arabic/English content
	// blocks", and a shop that writes only one should have the other stay
	// empty rather than be filled with a translation nobody approved.
	HeaderText     string `json:"header_text"`
	HeaderTextAr   string `json:"header_text_ar"`
	FooterText     string `json:"footer_text"`
	FooterTextAr   string `json:"footer_text_ar"`
	ReturnPolicy   string `json:"return_policy"`
	ReturnPolicyAr string `json:"return_policy_ar"`
	PaymentTerms   string `json:"payment_terms"`
	PaymentTermsAr string `json:"payment_terms_ar"`

	ShowLogo      bool `json:"show_logo"`
	ShowTaxNumber bool `json:"show_tax_number"`

	// Configured is false for a type nobody has touched, so the screen can show
	// it as the default rather than as an empty form somebody has to fill in.
	Configured bool   `json:"configured"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// defaultTemplate is what a document uses before anybody customises it.
//
// Blocks are empty rather than seeded with suggested wording: a footer nobody
// wrote is a footer nobody meant, and a shop should not discover their invoices
// carry a sentence the software invented for them.
func defaultTemplate(docType string) Template {
	return Template{
		DocType: docType, ShowLogo: true, ShowTaxNumber: true, Configured: false,
	}
}

// Templates returns every configurable type, whether or not it has been set.
//
// All of them, always. A screen listing only the configured ones would hide the
// types a client has not thought about yet, which are exactly the ones worth
// showing them.
func (s *Service) Templates(ctx context.Context, scope Scope) ([]Template, error) {
	byType := map[string]Template{}

	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT doc_type, header_text, header_text_ar, footer_text,
			       footer_text_ar, return_policy, return_policy_ar,
			       payment_terms, payment_terms_ar, show_logo, show_tax_number,
			       to_char(updated_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00')
			FROM document_template WHERE company_id = $1`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var t Template
			if e := rows.Scan(&t.DocType, &t.HeaderText, &t.HeaderTextAr,
				&t.FooterText, &t.FooterTextAr, &t.ReturnPolicy, &t.ReturnPolicyAr,
				&t.PaymentTerms, &t.PaymentTermsAr, &t.ShowLogo, &t.ShowTaxNumber,
				&t.UpdatedAt); e != nil {
				return e
			}
			t.Configured = true
			byType[t.DocType] = t
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "Those templates could not be read.")
	}

	out := make([]Template, 0, len(DocTypes))
	for _, docType := range DocTypes {
		if found, ok := byType[docType]; ok {
			out = append(out, found)
			continue
		}
		out = append(out, defaultTemplate(docType))
	}
	return out, nil
}

// Template returns one type, defaulted when nobody has set it.
//
// The document surfaces use this, and they must render something for a company
// that has never opened the settings screen.
func (s *Service) Template(
	ctx context.Context, scope Scope, docType string,
) (Template, error) {
	if !knownDocType(docType) {
		return Template{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a document type this product issues.", docType)
	}

	all, err := s.Templates(ctx, scope)
	if err != nil {
		return Template{}, err
	}
	for _, t := range all {
		if t.DocType == docType {
			return t, nil
		}
	}
	return defaultTemplate(docType), nil
}

// SaveTemplate stores what a client wrote for one document type.
func (s *Service) SaveTemplate(
	ctx context.Context, scope Scope, in Template,
) (Template, error) {
	if !knownDocType(in.DocType) {
		return Template{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a document type this product issues.", in.DocType)
	}
	if err := validateTemplate(in); err != nil {
		return Template{}, err
	}

	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			INSERT INTO document_template
			  (tenant_id, company_id, doc_type, header_text, header_text_ar,
			   footer_text, footer_text_ar, return_policy, return_policy_ar,
			   payment_terms, payment_terms_ar, show_logo, show_tax_number,
			   updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (company_id, doc_type) DO UPDATE SET
			  header_text      = EXCLUDED.header_text,
			  header_text_ar   = EXCLUDED.header_text_ar,
			  footer_text      = EXCLUDED.footer_text,
			  footer_text_ar   = EXCLUDED.footer_text_ar,
			  return_policy    = EXCLUDED.return_policy,
			  return_policy_ar = EXCLUDED.return_policy_ar,
			  payment_terms    = EXCLUDED.payment_terms,
			  payment_terms_ar = EXCLUDED.payment_terms_ar,
			  show_logo        = EXCLUDED.show_logo,
			  show_tax_number  = EXCLUDED.show_tax_number,
			  updated_by       = EXCLUDED.updated_by,
			  updated_at       = now()`,
			scope.TenantID, scope.CompanyID, in.DocType,
			strings.TrimSpace(in.HeaderText), strings.TrimSpace(in.HeaderTextAr),
			strings.TrimSpace(in.FooterText), strings.TrimSpace(in.FooterTextAr),
			strings.TrimSpace(in.ReturnPolicy), strings.TrimSpace(in.ReturnPolicyAr),
			strings.TrimSpace(in.PaymentTerms), strings.TrimSpace(in.PaymentTermsAr),
			in.ShowLogo, in.ShowTaxNumber, scope.UserID)
		return e
	})
	if err != nil {
		if errs.As(err) != nil {
			return Template{}, err
		}
		return Template{}, db.Translate(err, "That template could not be saved.")
	}

	return s.Template(ctx, scope, in.DocType)
}

// ResetTemplate returns one type to the RawSyst default.
//
// A delete rather than a blanking update. "Never configured" and "configured
// back to empty" produce the same document, and keeping a row to record the
// difference would leave the screen claiming a client had customised something
// they had just undone.
func (s *Service) ResetTemplate(
	ctx context.Context, scope Scope, docType string,
) error {
	if !knownDocType(docType) {
		return errs.Newf(errs.CodeInvalidInput,
			"%q is not a document type this product issues.", docType)
	}

	err := s.pool.Tx(ctx, func(tx pgx.Tx) error {
		if e := requireCompany(ctx, tx, scope.CompanyID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			DELETE FROM document_template
			WHERE company_id = $1 AND doc_type = $2`, scope.CompanyID, docType)
		return e
	})
	if err != nil {
		if errs.As(err) != nil {
			return err
		}
		return db.Translate(err, "That template could not be reset.")
	}
	return nil
}

func knownDocType(docType string) bool {
	for _, d := range DocTypes {
		if d == docType {
			return true
		}
	}
	return false
}

// The block limits, mirrored from 0056 so a client gets a sentence naming the
// field rather than a constraint violation naming an index.
const (
	maxShortBlock = 500
	maxLongBlock  = 2000
)

func validateTemplate(t Template) error {
	e := errs.Validation("Some of this template needs correcting.")
	bad := false

	for _, f := range []struct {
		field, value string
		limit        int
	}{
		{"header_text", t.HeaderText, maxShortBlock},
		{"header_text_ar", t.HeaderTextAr, maxShortBlock},
		{"footer_text", t.FooterText, maxShortBlock},
		{"footer_text_ar", t.FooterTextAr, maxShortBlock},
		{"return_policy", t.ReturnPolicy, maxLongBlock},
		{"return_policy_ar", t.ReturnPolicyAr, maxLongBlock},
		{"payment_terms", t.PaymentTerms, maxShortBlock},
		{"payment_terms_ar", t.PaymentTermsAr, maxShortBlock},
	} {
		// Counted in runes, not bytes. An Arabic return policy is two bytes a
		// character, and a byte limit would cut it off at half the length an
		// English one gets for no reason a client could understand.
		if len([]rune(strings.TrimSpace(f.value))) > f.limit {
			e.WithField(f.field, fmt.Sprintf(
				"This is longer than %d characters. It is reprinted on every "+
					"copy of the document.", f.limit))
			bad = true
		}
	}

	if bad {
		return e
	}
	return nil
}
