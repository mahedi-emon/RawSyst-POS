package zatca

import (
	"context"
	"crypto/sha256"
	"encoding/base64"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The document hasher is the one part of ZATCA still unbuilt, and this file
// makes that fact operational rather than a note in a document.
//
// The chain itself is real: ICV allocation, PIH linkage, replay and gap
// refusal, verification across a whole terminal's history. What is missing is
// the CONTENT — the byte-level UBL 2.1 XML, the canonicalisation, and the QR
// TLV encoding. `SA.ZATCA.QR_TLV_FIELDS` is still unverified against primary
// sources, and the blueprint is explicit that regulatory values must never be
// filled in from assumption.
//
// So there are two hashers and the environment chooses, exactly as
// registry.New(pool, isProduction) chooses whether unverified legal values may
// be served. The pattern is deliberate: an unverified rule and an unimplemented
// document format are the same class of problem, and they should fail the same
// way.

// HasherFor returns the hasher appropriate to the environment.
//
// Production gets one that refuses. A development or test environment gets one
// that lets the rest of the system be exercised end to end, clearly labelled so
// its output can never be mistaken for a real invoice hash.
func HasherFor(isProduction bool) DocumentHasher {
	if isProduction {
		return UnimplementedHasher{}
	}
	return DevelopmentHasher{}
}

// UnimplementedHasher refuses to produce a hash.
//
// This is the correct production behaviour until the format is verified.
// Issuing an invoice whose hash was guessed would be worse than refusing the
// sale: the invoice would look valid, take a real position on the chain, be
// handed to a customer, and then fail at ZATCA — by which time the counter has
// advanced and the document cannot be withdrawn without leaving exactly the gap
// tamper detection looks for.
type UnimplementedHasher struct{}

func (UnimplementedHasher) SchemaVersion() string { return "" }

func (UnimplementedHasher) Hash(context.Context, Document) (string, error) {
	return "", errs.New(errs.CodeComplianceBlocked,
		"E-invoicing is not yet available on this installation: the invoice "+
			"format has not been verified against ZATCA's published standard. "+
			"Sales cannot be issued until it is.")
}

// DevelopmentHasher produces a deterministic placeholder hash.
//
// Deterministic because the chain's own tests depend on the same document
// hashing to the same value; a random hash would make a replay look like a
// different invoice and hide the very duplicate the chain exists to catch.
//
// The schema version is deliberately NOT a real ZATCA version string. It reads
// as what it is, so an invoice produced in development can never be mistaken
// for one produced under a verified standard — including by a future support
// engineer looking at a row in a database.
type DevelopmentHasher struct{}

func (DevelopmentHasher) SchemaVersion() string { return "unverified-dev" }

func (DevelopmentHasher) Hash(_ context.Context, doc Document) (string, error) {
	// The real implementation will hash canonicalised UBL 2.1 XML. This hashes
	// the invoice's identity instead, which is enough to exercise the chain and
	// obviously not enough to be a real document hash.
	sum := sha256.Sum256(append([]byte("rawsyst-dev:"), doc.InvoiceUUID[:]...))
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}
