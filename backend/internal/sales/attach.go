package sales

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// AttachedDocument is what the till gets back after uploading.
type AttachedDocument struct {
	InvoiceID uuid.UUID `json:"invoice_id"`

	// Stored is false when the document was already present and identical — a
	// retry rather than a conflict, which a terminal that lost the response
	// needs to be able to make.
	Stored bool `json:"stored"`

	// Submittable says whether the server now has everything it needs. It is
	// NOT a promise that the invoice will be accepted, only that nothing is
	// missing on this side.
	Submittable bool `json:"submittable"`

	// SubmissionAvailable is false while the document format is unverified.
	// Reported plainly so a terminal is never left assuming its invoice has
	// been reported when the transport is still gated.
	SubmissionAvailable bool `json:"submission_available"`
}

// AttachSignedDocument stores the document a terminal signed locally.
//
// The device is taken from the caller's token and checked against the invoice.
// A terminal uploading a document for an invoice from ANOTHER till would attach
// its signature to a chain position it does not own — and because both tills
// belong to the same tenant, row-level security would not notice.
func (s *Service) AttachSignedDocument(
	ctx context.Context, tenantID, deviceID, invoiceID uuid.UUID,
	doc zatca.SignedDocument,
) (AttachedDocument, error) {
	if s.pool == nil {
		return AttachedDocument{}, errs.New(errs.CodeInternal,
			"The sales service was built without a database connection.")
	}

	out := AttachedDocument{InvoiceID: invoiceID}

	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var invoiceDevice *uuid.UUID
		e := tx.QueryRow(ctx,
			`SELECT device_id FROM sales_invoice WHERE id = $1`, invoiceID).
			Scan(&invoiceDevice)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That invoice was not found.")
		}
		if e != nil {
			return e
		}
		if invoiceDevice == nil || *invoiceDevice != deviceID {
			return errs.New(errs.CodeForbidden,
				"That invoice was rung up on a different terminal. Only the "+
					"terminal that signed a document may upload it.")
		}

		before, hadDocument, e := zatca.ReadSignedDocument(ctx, tx, invoiceID)
		if e != nil {
			return e
		}

		if e := zatca.RecordSignedDocument(ctx, tx, invoiceID, doc); e != nil {
			return e
		}

		out.Stored = !hadDocument || before.XML != doc.XML
		out.Submittable = true
		return nil
	})
	if err != nil {
		return AttachedDocument{}, err
	}

	// Read from the same seam the worker uses, so the till is told the truth
	// about whether anything can actually be sent rather than a hopeful
	// default.
	out.SubmissionAvailable = s.submitter != nil && s.submitter.Available()
	return out, nil
}

// WithSubmitter tells the service whether ZATCA submission is available, so it
// can report that to a terminal without guessing.
func (s *Service) WithSubmitter(sub zatca.Submitter) *Service {
	s.submitter = sub
	return s
}
