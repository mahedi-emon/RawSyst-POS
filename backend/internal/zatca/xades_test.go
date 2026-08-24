package zatca

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	btcec "github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"
)

// The signature is tested by verifying it, not by looking at it.
//
// A test that asserted "SignInvoice produced some bytes" would pass while every
// invoice was rejected. So the test below does what ZATCA's verifier does:
// takes the SIGNED document, recomputes both digests from it, and checks the
// ECDSA signature over the canonical SignedInfo. Nothing is taken from the
// signing side — every value is read back out of the finished document.

func signTestInvoice(t *testing.T) (SignedInvoice, Certificate, Invoice) {
	t.Helper()

	signer, err := GenerateKey()
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	cert, err := SelfSignedDevelopmentCertificate(signer, 365*24*time.Hour)
	if err != nil {
		t.Fatalf("making a development certificate: %v", err)
	}

	inv := sampleInvoice()
	unsigned, err := BuildInvoiceXML(inv)
	if err != nil {
		t.Fatalf("building the invoice: %v", err)
	}

	signed, err := SignInvoice(unsigned, cert, QRSeller{
		Name:      inv.Supplier.RegistrationName,
		VATNumber: inv.Supplier.VATNumber,
		Timestamp: "2022-03-13T14:40:40Z",
		Total:     inv.TaxInclusiveAmount,
		VATTotal:  inv.VATTotal,
	}, time.Date(2022, 3, 13, 14, 40, 40, 0, time.UTC))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signed, cert, inv
}

// digestOf returns the text of the DigestValue inside the reference matching
// the predicate, read out of the signed document.
func referenceDigest(t *testing.T, doc []byte, wantType string) string {
	t.Helper()
	root, err := parseXML(doc)
	if err != nil {
		t.Fatalf("parsing the signed document: %v", err)
	}

	var found string
	var walk func(e *xmlElement)
	walk = func(e *xmlElement) {
		if e.local == "Reference" {
			var typeAttr string
			for _, a := range e.attrs {
				if a.local == "Type" {
					typeAttr = a.value
				}
			}
			if typeAttr == wantType {
				found = childText(e, "DigestValue")
				return
			}
		}
		for _, c := range e.children {
			if el, ok := c.(*xmlElement); ok && found == "" {
				walk(el)
			}
		}
	}
	walk(root)
	return found
}

// The first reference's digest must equal the digest of the signed document put
// back through the transform chain. That is the whole point of the transforms:
// the signature and the QR are removed again, so the value they were computed
// from is recoverable from the finished invoice.
func TestTheInvoiceDigestSurvivesBeingSigned(t *testing.T) {
	signed, _, _ := signTestInvoice(t)

	canonical, err := CanonicalInvoice(signed.XML)
	if err != nil {
		t.Fatalf("canonicalising the signed document: %v", err)
	}
	sum := sha256.Sum256(canonical)
	recomputed := base64.StdEncoding.EncodeToString(sum[:])

	if recomputed != signed.InvoiceHash {
		t.Errorf("the digest of the signed document does not match the one "+
			"signed:\n  in the signature %s\n  recomputed       %s",
			signed.InvoiceHash, recomputed)
	}

	inDocument := referenceDigest(t, signed.XML, "")
	if inDocument != signed.InvoiceHash {
		t.Errorf("the first reference carries %q, want %q", inDocument, signed.InvoiceHash)
	}
}

// The second reference's digest must equal the digest of the SignedProperties
// as that element appears in the finished document — canonicalised in context,
// with every namespace the Invoice element binds rendered on it.
func TestTheSignedPropertiesDigestMatchesTheDocument(t *testing.T) {
	signed, _, _ := signTestInvoice(t)

	canonical, err := canonicalSubtree(signed.XML, elementWithID(signedPropertiesID))
	if err != nil {
		t.Fatalf("canonicalising the signed properties: %v", err)
	}
	sum := sha256.Sum256(canonical)
	recomputed := base64.StdEncoding.EncodeToString(sum[:])

	inDocument := referenceDigest(t, signed.XML, SignedPropertiesType)
	if inDocument == "" {
		t.Fatal("the signed document has no SignedProperties reference")
	}
	if inDocument != recomputed {
		t.Errorf("the properties digest does not match the document:\n"+
			"  in the signature %s\n  recomputed       %s", inDocument, recomputed)
	}
}

// The signature itself: verified against the certificate's own public key, over
// the canonical SignedInfo read back out of the finished document.
//
// This is exactly the computation a verifier performs. If it passes, the
// canonicalisation, the reference structure and the signature agree.
func TestTheSignatureVerifiesAgainstTheCertificate(t *testing.T) {
	signed, cert, _ := signTestInvoice(t)

	canonical, err := canonicalSubtree(signed.XML, elementNamed("SignedInfo"))
	if err != nil {
		t.Fatalf("canonicalising SignedInfo: %v", err)
	}
	digest := sha256.Sum256(canonical)

	raw, err := base64.StdEncoding.DecodeString(signed.SignatureValue)
	if err != nil {
		t.Fatalf("the signature is not base64: %v", err)
	}
	parsed, err := btcecdsa.ParseDERSignature(raw)
	if err != nil {
		t.Fatalf("the signature is not a DER ECDSA signature: %v", err)
	}

	// The public key comes out of the CERTIFICATE, not out of the signer, so
	// this proves the document carries the key that made it.
	spki := certPublicKeyDER(cert.DER)
	if spki == nil {
		t.Fatal("the certificate has no public key")
	}
	point := spkiPoint(t, spki)
	pub, err := btcec.ParsePubKey(point)
	if err != nil {
		t.Fatalf("the certificate's public key does not parse: %v", err)
	}

	if !parsed.Verify(digest[:], pub) {
		t.Error("the signature does not verify over the canonical SignedInfo " +
			"— a verifier following the specification would reject this invoice")
	}
}

// spkiPoint pulls the uncompressed point out of a SubjectPublicKeyInfo.
func spkiPoint(t *testing.T, spki []byte) []byte {
	t.Helper()
	_, body, _, err := derParse(spki)
	if err != nil {
		t.Fatalf("reading the public key: %v", err)
	}
	// SEQUENCE { AlgorithmIdentifier, BIT STRING }
	_, _, rest, err := derParse(body)
	if err != nil {
		t.Fatalf("reading the algorithm: %v", err)
	}
	tag, bits, _, err := derParse(rest)
	if err != nil || tag != derBitStringTag {
		t.Fatalf("reading the key bits: %v", err)
	}
	if len(bits) < 2 || bits[0] != 0 {
		t.Fatal("the key bit string is malformed")
	}
	return bits[1:]
}

// Changing one byte of the invoice must invalidate the signature. Without this
// the tests above could pass against a signature that covered nothing.
func TestTamperingWithTheInvoiceBreaksTheDigest(t *testing.T) {
	signed, _, _ := signTestInvoice(t)

	tampered := strings.Replace(string(signed.XML),
		"<cbc:PayableAmount currencyID=\"SAR\">1110.90</cbc:PayableAmount>",
		"<cbc:PayableAmount currencyID=\"SAR\">1110.91</cbc:PayableAmount>", 1)
	if tampered == string(signed.XML) {
		t.Fatal("the amount this test tampers with is not in the document")
	}

	canonical, err := CanonicalInvoice([]byte(tampered))
	if err != nil {
		t.Fatalf("canonicalising: %v", err)
	}
	sum := sha256.Sum256(canonical)
	if base64.StdEncoding.EncodeToString(sum[:]) == signed.InvoiceHash {
		t.Error("changing the payable amount did not change the digest, so the " +
			"signature does not actually cover the invoice")
	}
}

// The structure §2.3.3 requires, asserted where the spec is explicit.
func TestTheSignatureCarriesWhatTheStandardRequires(t *testing.T) {
	signed, _, _ := signTestInvoice(t)
	doc := string(signed.XML)

	for _, want := range []string{
		`Algorithm="` + CanonicalizationC14N11 + `"`,
		`Algorithm="` + SignatureMethodECDSASHA256 + `"`,
		`Algorithm="` + DigestMethodSHA256 + `"`,
		`Type="` + SignedPropertiesType + `"`,
		`URI="#` + signedPropertiesID + `"`,
		`<ds:X509Certificate>`,
		`<xades:SigningTime>`,
		`<ext:ExtensionURI>` + extensionURIXAdES + `</ext:ExtensionURI>`,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the signed document is missing %s", want)
		}
	}

	// The three XPath transforms, in the order §2.3.3 lists them. Order is part
	// of the specification: a chain runs in sequence.
	at := -1
	for _, xpath := range InvoiceTransforms {
		i := strings.Index(doc, escapeXMLText(xpath))
		if i < 0 {
			t.Errorf("the transform %q is missing", xpath)
			continue
		}
		if i < at {
			t.Errorf("the transform %q appears out of order", xpath)
		}
		at = i
	}
}

// The QR carries all nine tags once a signature exists.
func TestTheSignedInvoiceCarriesACompleteQR(t *testing.T) {
	signed, _, _ := signTestInvoice(t)

	fields, err := DecodeQR(signed.QR)
	if err != nil {
		t.Fatalf("the QR does not decode: %v", err)
	}
	if len(fields) != 9 {
		t.Fatalf("the QR carries %d tags, want all 9", len(fields))
	}
	if err := ValidateQR(signed.QR); err != nil {
		t.Errorf("the QR is not well formed: %v", err)
	}

	// Tag 6 must be the invoice hash the signature covers, not some other
	// digest. This is the one that silently goes wrong.
	var tag6 string
	for _, f := range fields {
		if f.Tag == QRInvoiceHash {
			tag6 = string(f.Value)
		}
	}
	if tag6 != signed.InvoiceHash {
		t.Errorf("QR tag 6 is %q, want the invoice hash %q", tag6, signed.InvoiceHash)
	}
}

// A development certificate must be recognisable, so it can be refused where a
// real one is required.
func TestTheDevelopmentCertificateSaysWhatItIs(t *testing.T) {
	_, cert, _ := signTestInvoice(t)

	if !cert.IsDevelopmentCertificate() {
		t.Error("the development certificate does not identify itself, so " +
			"nothing can refuse it")
	}
	if !strings.Contains(cert.String(), "der:") {
		t.Errorf("the certificate does not print its size: %s", cert.String())
	}
}

// --- against ZATCA's validator ----------------------------------------------

// A signed invoice, put through ZATCA's own validator.
//
// The certificate is self-signed, so ZATCA cannot accept it — the chain does
// not reach their CA, and it should not. What this checks is everything BEFORE
// that: the business rules, the QR, and the shape of the signature. A failure
// naming anything other than the certificate is a defect here.
func TestTheSignedInvoicePassesEverythingButTheCertificate(t *testing.T) {
	if os.Getenv("ZATCA_VALIDATOR") == "" {
		t.Skip("set ZATCA_VALIDATOR=1 to check against ZATCA's live validator")
	}

	signed, _, _ := signTestInvoice(t)
	result := validateWithZATCA(t, signed.XML)

	for _, e := range result.Errors {
		switch e.Category {
		case "SIGNATURE_ERROR", "CERTIFICATE_ERROR":
			t.Logf("expected while self-signed: %s %s — %s", e.Category, e.Code, e.Message)
		default:
			t.Errorf("business or QR failure: %s %s — %s", e.Category, e.Code, e.Message)
		}
	}
	for _, w := range result.Warnings {
		t.Logf("warning: %s %s — %s", w.Category, w.Code, w.Message)
	}
}

// The hasher computes what §3 says, and agrees with the signing path.
//
// §3, in full: the previous-invoice hash is produced "by applying the same
// transform as is used for the cryptographic stamp and as specified in section
// 2.3.3 and taking the sha256 algorithm". So the chain's PIH and the value the
// signature covers are one computation, and if these two ever disagreed the
// chain would break silently — every later invoice linking to a hash nobody
// else computes.
func TestTheHasherAgreesWithTheSignature(t *testing.T) {
	signed, _, _ := signTestInvoice(t)

	got, err := StandardHasher{}.Hash(context.Background(), Document{XML: signed.XML})
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	if got != signed.InvoiceHash {
		t.Errorf("the hasher and the signature disagree:\n  hasher    %s\n  signature %s",
			got, signed.InvoiceHash)
	}
}

// It hashes the UNSIGNED document to the same value, which is what makes the
// transform chain an exact inverse of the injection.
func TestTheHashIsTheSameBeforeAndAfterSigning(t *testing.T) {
	inv := sampleInvoice()
	unsigned, err := BuildInvoiceXML(inv)
	if err != nil {
		t.Fatalf("building: %v", err)
	}

	before, err := StandardHasher{}.Hash(context.Background(), Document{XML: unsigned})
	if err != nil {
		t.Fatalf("hashing the unsigned document: %v", err)
	}

	signer, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := SelfSignedDevelopmentCertificate(signer, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignInvoice(unsigned, cert, QRSeller{
		Name: "S", VATNumber: "301121971500003", Timestamp: "2022-03-13T14:40:40Z",
		Total: "1110.90", VATTotal: "144.90",
	}, time.Now())
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	after, err := StandardHasher{}.Hash(context.Background(), Document{XML: signed.XML})
	if err != nil {
		t.Fatalf("hashing the signed document: %v", err)
	}
	if before != after {
		t.Errorf("signing changed the hash:\n  before %s\n  after  %s", before, after)
	}
}

// An empty document is refused rather than hashed to the digest of nothing,
// which is a real value that would sit on a chain looking legitimate.
func TestHashingNothingIsRefused(t *testing.T) {
	if _, err := (StandardHasher{}).Hash(context.Background(), Document{}); err == nil {
		t.Error("an empty document was hashed")
	}
}
