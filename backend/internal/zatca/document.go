package zatca

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The terminal hands its signed document back.
//
// # Why the server has to be told
//
// Signing is local and locked (E1.3 RULE 1): the device holds its own CSID
// private key and this process never sees it. But the server allocates the ICV
// and the PIH, because those are a per-terminal sequence that only one
// authority can arbitrate. So the order is necessarily:
//
//	server   allocates ICV and PIH
//	terminal builds the UBL, signs it, derives the QR
//	terminal uploads the document, the stamp and the QR
//	server   submits to ZATCA on the terminal's behalf
//
// The upload is the step that was missing. Without it the server held a chain
// position with nothing attached to it, and had nothing to submit.
//
// # What this deliberately does NOT do
//
// It does not build the XML, sign anything, derive a QR, or recompute the
// chain hash. Design 01 §3 defines the hash as SHA-256 over the CANONICAL
// signed XML, and the canonicalisation is one of the values still marked
// `__VERIFY__` in the registry. Recomputing it here from a guess would produce
// a hash that disagrees with the terminal's, break the PIH linkage of every
// later invoice, and do so silently. Reconciling the server's allocated hash
// with the terminal's is part of the verification pass, not something to
// improvise.

// SignedDocument is what a terminal produced locally.
type SignedDocument struct {
	// XML is the canonical signed UBL 2.1 document.
	XML string

	// Stamp is the ECDSA cryptographic stamp over that document.
	Stamp string

	// QRTLV is the base64 TLV payload printed on the receipt.
	QRTLV string
}

// RecordSignedDocument stores what the terminal signed, exactly once.
//
// Write-once is enforced by the database (migration 0029), not by this check.
// The check exists to give a terminal a sentence it can act on; the trigger is
// what makes the guarantee true for a support script and a migration too.
func RecordSignedDocument(
	ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, doc SignedDocument,
) error {
	if doc.XML == "" {
		return errs.New(errs.CodeInvalidInput,
			"A signed document upload must include the signed XML.")
	}
	if doc.Stamp == "" {
		return errs.New(errs.CodeInvalidInput,
			"A signed document upload must include the terminal's stamp.")
	}

	var existing *string
	err := tx.QueryRow(ctx,
		`SELECT xml FROM zatca_invoice WHERE invoice_id = $1 FOR UPDATE`,
		invoiceID).Scan(&existing)

	if errors.Is(err, pgx.ErrNoRows) {
		// Under row-level security another tenant's invoice reads as absent,
		// which is the right answer: its existence is not this caller's
		// business.
		return errs.New(errs.CodeNotFound,
			"That invoice has no position on an e-invoicing chain, so there is "+
				"nothing to attach a signed document to.")
	}
	if err != nil {
		return err
	}

	if existing != nil {
		if *existing == doc.XML {
			// The same document arriving twice is a retry, not a conflict. A
			// terminal that lost the response must be able to send again.
			return nil
		}
		return errs.New(errs.CodeImmutable,
			"This invoice already has a signed document. A document cannot be "+
				"replaced once signed — issue a credit note instead.")
	}

	_, err = tx.Exec(ctx, `
		UPDATE zatca_invoice
		SET xml = $2, stamp = $3, qr_tlv = nullif($4, '')
		WHERE invoice_id = $1`,
		invoiceID, doc.XML, doc.Stamp, doc.QRTLV)
	if err != nil {
		return db.Translate(err,
			"That signed document could not be stored.")
	}
	return nil
}

// ReadSignedDocument returns what the terminal uploaded, if anything.
func ReadSignedDocument(
	ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID,
) (SignedDocument, bool, error) {
	var xml, stamp, qr *string
	err := tx.QueryRow(ctx,
		`SELECT xml, stamp, qr_tlv FROM zatca_invoice WHERE invoice_id = $1`,
		invoiceID).Scan(&xml, &stamp, &qr)

	if errors.Is(err, pgx.ErrNoRows) {
		return SignedDocument{}, false, nil
	}
	if err != nil {
		return SignedDocument{}, false, err
	}
	if xml == nil {
		return SignedDocument{}, false, nil
	}

	doc := SignedDocument{XML: *xml}
	if stamp != nil {
		doc.Stamp = *stamp
	}
	if qr != nil {
		doc.QRTLV = *qr
	}
	return doc, true, nil
}
