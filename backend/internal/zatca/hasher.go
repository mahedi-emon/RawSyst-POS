package zatca

import (
	"context"
	"crypto/sha256"
	"encoding/base64"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Hashing a document, which is now a real computation rather than a refusal.
//
// # What changed
//
// This file used to hold UnimplementedHasher and a note that the byte-level
// format was unverified. It was: the canonicalisation, the UBL field set and
// the QR encoding were all open. They are not any more —
//
//	SA.ZATCA.XML_CANONICALIZATION   verified, and implemented in c14n.go
//	SA.ZATCA.UBL_FIELD_SET          verified against ZATCA's own validator
//	SA.ZATCA.QR_TAG_VALUE_ENCODING  verified against ZATCA's worked payload
//
// so the hash is exactly what §3 of the Security Features standard says it is:
// "applying the same transform as is used for the cryptographic stamp and as
// specified in section 2.3.3 and taking the sha256 algorithm".
//
// # What has NOT changed
//
// A hash is not permission to issue an invoice. Signing needs a certificate
// ZATCA issued, and submission needs credentials ZATCA issued. Those gates
// live in submit.go and in the certificate store, and neither is opened by
// this file. What is removed here is only the pretence that hashing itself was
// unsolved.

// DocumentHasher is implemented by anything that can hash a document.
//
// Kept as an interface because a terminal signing locally and a server hashing
// for verification are the same computation performed in two places, and
// because the tests substitute one.

// StandardHasher computes the hash ZATCA specifies.
//
// The whole of it: canonicalise through the transform chain, SHA-256, base64.
// The transform removals are what make it well defined on a SIGNED document
// too — the signature and the QR are taken back out, so the value recomputed
// from a finished invoice is the value that was signed.
type StandardHasher struct{}

// SchemaVersion names the standard this implements, and is recorded against
// every invoice so an archived document stays verifiable against the rules
// that produced it — fifteen years later, per the retention tiers.
func (StandardHasher) SchemaVersion() string { return "UBL-2.1-KSA-1.2" }

func (StandardHasher) Hash(_ context.Context, doc Document) (string, error) {
	if len(doc.XML) == 0 {
		return "", errs.New(errs.CodeInvalidInput,
			"There is no document here to hash.")
	}

	canonical, err := CanonicalInvoice(doc.XML)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// HasherFor returns the hasher for the environment.
//
// The same one everywhere now. The parameter is kept because both callers pass
// it and because an environment-dependent hash would be a bug worth being able
// to express: a development hash that differed from production would break the
// chain the moment a database was copied between them.
func HasherFor(bool) DocumentHasher { return StandardHasher{} }
