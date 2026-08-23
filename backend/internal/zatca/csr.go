package zatca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"strings"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The certificate signing request that begins onboarding.
//
// # Provenance
//
// Read on 2026-08-23 from the primary source, and from the artefacts printed
// inside it rather than from a description of them:
//
//	ZATCA, "E-Invoicing Detailed Technical Guideline — Developer Portal
//	Manual", §5.3 "Generate a Certificate Signing Request (CSR)", pages 91-95.
//	https://zatca.gov.sa/en/E-Invoicing/Introduction/Guidelines/Documents/
//	DEVELOPER-PORTAL-MANUAL.pdf
//
// §5.3's substance is in SCREENSHOTS, not in the text layer, which is why the
// 2026-08-19 desk verification concluded the manual "defers all of it" and why
// 0045 recorded the RDN table as obtainable only from the Fatoora SDK. The
// images were extracted from the PDF and read directly. Two of them settle
// things this registry had open:
//
//   - page 91 is the complete OpenSSL configuration file, including the OID
//     that 0059 recorded as the last unknown blocking SA.ZATCA.CSR_SUBJECT_LAYOUT:
//     "certificateTemplateName= 1.3.6.1.4.1.311.20.2".
//   - page 95 is a sample CSR. It was transcribed and decoded with openssl,
//     which is self-verifying: a mis-transcribed base64 blob does not parse as
//     ASN.1. It parses, and it confirms the curve (secp256k1), the signature
//     algorithm (ecdsa-with-SHA256), and the template extension.
//
// # Where the manual contradicts itself, and what this file does about it
//
// The manual is not internally consistent, and pretending otherwise would bury
// the problem in code. Three divergences, all left visible:
//
//  1. The config file's [dn] holds C/OU/O/CN and pushes the ZATCA-specific
//     values into [alt_names]. The sample CSR instead carries serialNumber and
//     organizationIdentifier in the SUBJECT and has no subjectAltName at all.
//     The sample's organizationIdentifier is also "PSDFI-FINFSA-29884997",
//     which violates the manual's own rule that the value is 15 digits
//     beginning and ending with 3. The sample is an illustration, not a
//     conformant request; the config file is the artefact the manual tells the
//     reader to use ("its configuration file as shown below"). This file
//     implements the CONFIG FILE.
//
//  2. The config defines the ZATCA fields in [req_ext] but the command on page
//     95 passes "-extensions v3_req", which selects a section holding only
//     basicConstraints and keyUsage. Run literally, that command produces a CSR
//     with none of ZATCA's data in it — and the manual's own sample CSR does
//     not match it either, carrying the template extension and neither
//     basicConstraints nor keyUsage. [req_ext] is the only section containing
//     the values ZATCA needs, so [req_ext] is what this builds.
//
//  3. Page 91's config sets certificateTemplateName to "ZATCA-Code-Signing";
//     page 95's sample CSR carries "TSTZATCA-Code-Signing". Both appear in the
//     same document and it never says which environment takes which. So this
//     file names both and makes the caller choose rather than guessing on their
//     behalf — see TemplateName below.
//
// # Why there is no key generation here
//
// BuildCSR takes a crypto.Signer and never sees a private key. That is not a
// missing piece: the Security Features Implementation Standard v1.1 §5.3.2
// requires that "keys must be marked as non-exportable in order to prohibit key
// export out of the security module where the key was generated", and a
// crypto.Signer is exactly how Go models a key it cannot extract.
//
// It is also forced. ZATCA requires secp256k1 and the Go standard library
// cannot do that curve at all — crypto/elliptic's generic CurveParams
// arithmetic hard-codes a = -3, which every NIST curve satisfies and secp256k1
// (a = 0) does not, so ecdsa.GenerateKey panics with "attempted operation on
// invalid point" rather than returning a wrong answer. Hand-rolling
// non-constant-time ECDSA for a key that signs tax documents is not a trade
// this project should make, so the curve implementation is the caller's.

// Object identifiers used in the request.
var (
	oidCountry                = asn1.ObjectIdentifier{2, 5, 4, 6}
	oidOrganization           = asn1.ObjectIdentifier{2, 5, 4, 10}
	oidOrganizationalUnit     = asn1.ObjectIdentifier{2, 5, 4, 11}
	oidCommonName             = asn1.ObjectIdentifier{2, 5, 4, 3}
	oidSerialNumber           = asn1.ObjectIdentifier{2, 5, 4, 5}
	oidTitle                  = asn1.ObjectIdentifier{2, 5, 4, 12}
	oidRegisteredAddress      = asn1.ObjectIdentifier{2, 5, 4, 26}
	oidBusinessCategory       = asn1.ObjectIdentifier{2, 5, 4, 15}
	oidUserID                 = asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}
	oidExtensionRequest       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 14}
	oidSubjectAltName         = asn1.ObjectIdentifier{2, 5, 29, 17}
	oidECPublicKey            = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidSecp256k1              = asn1.ObjectIdentifier{1, 3, 132, 0, 10}
	oidECDSAWithSHA256        = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidCertificateTemplateOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 20, 2}
)

// The certificateTemplateName values ZATCA publishes.
//
// Two of these come from SA.ZATCA.CSR_CERTIFICATE_TEMPLATE, which quotes the
// mapping exactly: "For FATOORA Portal ... ZATCA-Code-Signing" and "For FATOORA
// Simulation Portal ... PREZATCA-Code-Signing". That rule also records that the
// value for the Developer Portal Integration Sandbox "is not published in any
// of the four documents and must not be inferred from these two".
//
// The third is what the Developer Portal Manual's own sample CSR carries, which
// is new evidence against exactly that gap — but the same section's config file
// prints ZATCA-Code-Signing, so the manual shows two values for one environment
// and never assigns either. The constant is therefore named after the value and
// not after an environment: which one the sandbox wants is still open.
//
// Getting this wrong is not a validation error. The environments are
// independent and onboard independently, so a unit built with the wrong
// template name onboards successfully into the wrong one.
const (
	// TemplateNameZATCACodeSigning is the FATOORA (production) value, and the
	// one in the Developer Portal Manual's OpenSSL config file on page 91.
	TemplateNameZATCACodeSigning = "ZATCA-Code-Signing"

	// TemplateNamePREZATCACodeSigning is the FATOORA Simulation value.
	TemplateNamePREZATCACodeSigning = "PREZATCA-Code-Signing"

	// TemplateNameTSTZATCACodeSigning is the value carried by the sample CSR on
	// page 95 of the Developer Portal Manual, confirmed by decoding it.
	TemplateNameTSTZATCACodeSigning = "TSTZATCA-Code-Signing"
)

// PublishedTemplateNames are the only values BuildCSR will encode.
var PublishedTemplateNames = []string{
	TemplateNameZATCACodeSigning,
	TemplateNamePREZATCACodeSigning,
	TemplateNameTSTZATCACodeSigning,
}

// CSRSubject is the taxpayer and device data that goes into the request.
//
// Field names follow the manual's own table (§5.3.1) rather than the X.509
// attribute names, because that table is what an implementer reads alongside
// this.
type CSRSubject struct {
	// CommonName is the EGS unit's common name. The config file's sample is an
	// IP address; the manual constrains it only as free text.
	CommonName string

	// OrganizationName is the taxpayer name, OrganizationalUnit the branch. The
	// manual adds one conditional rule to the unit: it carries the 10-digit TIN
	// when the 11th digit of the organization identifier is 1.
	OrganizationName   string
	OrganizationalUnit string

	// CountryCode is ISO 3166 Alpha-2, "a 2 letter code" per the manual.
	CountryCode string

	// EGSSerialNumber identifies the unit as "1-<solution>|2-<model>|3-<serial>".
	EGSSerialNumber string

	// OrganizationID is the VAT registration number: "15 digits, starting and
	// ending with 3".
	OrganizationID string

	// InvoiceTypes is the functionality map: "4-digit binary number (0s and 1s
	// only, cannot all be 0s)" over the positions TSCZ.
	InvoiceTypes string

	// Location is the branch address, Industry the sector. Both free text.
	Location string
	Industry string
}

// Validate applies the rules the manual states, and only those.
//
// Every check below quotes a constraint from §5.3.1. Nothing is invented: the
// fields the manual describes only as "free text" are checked for presence and
// nothing more, because a CSR missing a required RDN is refused at onboarding
// with an error that does not say which one.
func (s CSRSubject) Validate() error {
	missing := func(name string) error {
		return errs.New(errs.CodeInvalidInput,
			"The certificate request is missing "+name+
				", which ZATCA requires. Complete the e-invoicing settings and try again.")
	}

	switch {
	case strings.TrimSpace(s.CommonName) == "":
		return missing("a common name")
	case strings.TrimSpace(s.OrganizationName) == "":
		return missing("the organization name")
	case strings.TrimSpace(s.OrganizationalUnit) == "":
		return missing("the organization unit name")
	case strings.TrimSpace(s.Location) == "":
		return missing("the branch location")
	case strings.TrimSpace(s.Industry) == "":
		return missing("the industry")
	}

	if len(s.CountryCode) != 2 || !isAlpha(s.CountryCode) {
		return errs.New(errs.CodeInvalidInput,
			"The country code must be the two-letter ISO 3166 code, such as SA.")
	}

	// "15 digits, starting and ending with 3."
	if len(s.OrganizationID) != 15 || !isDigits(s.OrganizationID) ||
		s.OrganizationID[0] != '3' || s.OrganizationID[14] != '3' {
		return errs.New(errs.CodeInvalidInput,
			"The VAT registration number must be 15 digits and must start and end with 3.")
	}

	// "4-digit binary number (0s and 1s only, cannot all be 0s)."
	if len(s.InvoiceTypes) != 4 {
		return errs.New(errs.CodeInvalidInput,
			"The invoice type must be four digits, one for each of T, S, C and Z.")
	}
	allZero := true
	for i := 0; i < 4; i++ {
		switch s.InvoiceTypes[i] {
		case '0':
		case '1':
			allZero = false
		default:
			return errs.New(errs.CodeInvalidInput,
				"The invoice type may contain only 0 and 1.")
		}
	}
	if allZero {
		return errs.New(errs.CodeInvalidInput,
			"The invoice type cannot be 0000: the unit must issue at least one document type.")
	}

	// "1-<solution>|2-<model>|3-<serial>". The manual gives the shape and no
	// constraint on the parts, so only the shape is checked.
	parts := strings.Split(s.EGSSerialNumber, "|")
	if len(parts) != 3 ||
		!strings.HasPrefix(parts[0], "1-") ||
		!strings.HasPrefix(parts[1], "2-") ||
		!strings.HasPrefix(parts[2], "3-") {
		return errs.New(errs.CodeInvalidInput,
			"The device serial number must read 1-<solution>|2-<model>|3-<serial>.")
	}

	return nil
}

// BuildCSR produces a PEM-encoded PKCS#10 request, signed by signer.
//
// signer must hold a secp256k1 key: ZATCA accepts no other curve, and a request
// built on P-256 is well formed, indistinguishable by eye, and rejected at
// onboarding. The curve is therefore checked against the SEC 2 parameters
// rather than against the curve's name, which any caller can set to anything.
func BuildCSR(subject CSRSubject, templateName string, signer crypto.Signer) ([]byte, error) {
	if err := subject.Validate(); err != nil {
		return nil, err
	}
	published := false
	for _, name := range PublishedTemplateNames {
		if templateName == name {
			published = true
			break
		}
	}
	if !published {
		return nil, errs.New(errs.CodeInvalidInput,
			"The certificate template must be one of the values ZATCA publishes.")
	}

	pub, ok := signer.Public().(*ecdsa.PublicKey)
	if !ok {
		return nil, errs.New(errs.CodeInvalidInput,
			"The signing key is not an elliptic curve key.")
	}
	if err := checkSecp256k1(pub); err != nil {
		return nil, err
	}

	info, err := certificationRequestInfo(subject, templateName, pub)
	if err != nil {
		return nil, err
	}

	digest := sha256.Sum256(info)
	sig, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return nil, errs.New(errs.CodeInternal,
			"The certificate request could not be signed by the device key.")
	}

	algorithm := derWrap(derSequence, marshalOID(oidECDSAWithSHA256))
	request := derWrap(derSequence, concat(info, algorithm, derBitString(sig)))

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: request}), nil
}

// EncodeCSRForAPI base64-encodes the PEM text for the compliance CSID call.
//
// The body carries base64 of the PEM BLOCK — the armour lines included — not
// base64 of the DER. That is visible in the Postman screenshot on page 96,
// whose "csr" value begins "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURSBSRVFVRVNU", which
// decodes to "-----BEGIN CERTIFICATE REQUEST". Sending base64 of the DER
// instead produces a request that looks correct and is refused.
func EncodeCSRForAPI(csrPEM []byte) string {
	return base64.StdEncoding.EncodeToString(csrPEM)
}

// certificationRequestInfo builds the signed-over half of the request.
func certificationRequestInfo(s CSRSubject, templateName string, pub *ecdsa.PublicKey) ([]byte, error) {
	// RFC 2986: version is 0.
	version, err := asn1.Marshal(0)
	if err != nil {
		return nil, errs.New(errs.CodeInternal, "The certificate request could not be encoded.")
	}

	// [dn], in the order the config file lists it. Order is part of the
	// encoding: a distinguished name is a sequence, and two names with the same
	// components in a different order are different names.
	subjectDN := derWrap(derSequence, concat(
		rdn(oidCountry, derPrintableString, s.CountryCode),
		rdn(oidOrganizationalUnit, derUTF8String, s.OrganizationalUnit),
		rdn(oidOrganization, derUTF8String, s.OrganizationName),
		rdn(oidCommonName, derUTF8String, s.CommonName),
	))

	// [alt_names], likewise in config order.
	altNameDN := derWrap(derSequence, concat(
		rdn(oidSerialNumber, derPrintableString, s.EGSSerialNumber),
		rdn(oidUserID, derUTF8String, s.OrganizationID),
		rdn(oidTitle, derUTF8String, s.InvoiceTypes),
		rdn(oidRegisteredAddress, derUTF8String, s.Location),
		rdn(oidBusinessCategory, derUTF8String, s.Industry),
	))

	// GeneralName directoryName is [4] EXPLICIT, because Name is a CHOICE and
	// an implicit tag would erase which alternative was chosen.
	generalNames := derWrap(derSequence, derWrap(derContextConstructed|4, altNameDN))

	extensions := derWrap(derSequence, concat(
		extension(oidCertificateTemplateOID, derWrap(derPrintableString, []byte(templateName))),
		extension(oidSubjectAltName, generalNames),
	))

	// Attribute ::= SEQUENCE { type OID, values SET OF ANY }
	attribute := derWrap(derSequence, concat(
		marshalOID(oidExtensionRequest),
		derWrap(derSet, extensions),
	))

	// attributes [0] IMPLICIT SET OF Attribute — the implicit tag replaces the
	// SET tag, so the attributes sit directly inside it.
	attributes := derWrap(derContextConstructed|0, attribute)

	return derWrap(derSequence, concat(
		version,
		subjectDN,
		subjectPublicKeyInfo(pub),
		attributes,
	)), nil
}

// subjectPublicKeyInfo encodes the key as an uncompressed point.
//
// Uncompressed, not compressed, even though §5.3.2.2 has the reader extract a
// COMPRESSED public key with "-conv_form compressed". That extraction produces
// a separate artefact used elsewhere; the CSR itself carries the uncompressed
// form, which is settled by decoding the manual's own sample — its public key
// begins 04 and runs 65 bytes.
func subjectPublicKeyInfo(pub *ecdsa.PublicKey) []byte {
	size := (pub.Curve.Params().BitSize + 7) / 8
	point := make([]byte, 1+2*size)
	point[0] = 4
	pub.X.FillBytes(point[1 : 1+size])
	pub.Y.FillBytes(point[1+size:])

	algorithm := derWrap(derSequence, concat(
		marshalOID(oidECPublicKey),
		marshalOID(oidSecp256k1),
	))
	return derWrap(derSequence, concat(algorithm, derBitString(point)))
}

// checkSecp256k1 compares the curve to the SEC 2 parameters.
func checkSecp256k1(pub *ecdsa.PublicKey) error {
	wrong := errs.New(errs.CodeInvalidInput,
		"The device signing key is not on the secp256k1 curve, which is the only "+
			"curve ZATCA accepts.")

	if pub.Curve == nil {
		return wrong
	}
	p := pub.Curve.Params()
	if p.BitSize != 256 {
		return wrong
	}
	for _, pair := range [][2]*big.Int{
		{p.P, secp256k1P},
		{p.N, secp256k1N},
		{p.B, secp256k1B},
		{p.Gx, secp256k1Gx},
		{p.Gy, secp256k1Gy},
	} {
		if pair[0] == nil || pair[0].Cmp(pair[1]) != 0 {
			return wrong
		}
	}
	if pub.X == nil || pub.Y == nil {
		return wrong
	}
	return nil
}

// The secp256k1 domain parameters, from SEC 2 v2 §2.4.1. Held here so the
// curve can be checked without depending on whichever library supplies it.
var (
	secp256k1P  = hexInt("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F")
	secp256k1N  = hexInt("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141")
	secp256k1B  = big.NewInt(7)
	secp256k1Gx = hexInt("79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798")
	secp256k1Gy = hexInt("483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8")
)

func hexInt(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic("zatca: bad secp256k1 constant")
	}
	return n
}

// --- minimal DER writing -----------------------------------------------------
//
// Hand-written rather than reached for through encoding/asn1's struct tags,
// because the parts that matter here are the ones struct tags express badly: a
// SET OF inside a SEQUENCE OF, an implicit context tag standing in for a SET,
// and per-attribute control over PrintableString versus UTF8String. The
// alternative is a tower of wrapper types that is harder to check against a
// hex dump than the encoding itself.

const (
	derPrintableString = 0x13
	derUTF8String      = 0x0c
	derBitStringTag    = 0x03
	derSequence        = 0x30
	derSet             = 0x31
	derOctetString     = 0x04

	derContextConstructed = 0xa0
)

func derWrap(tag byte, content []byte) []byte {
	out := []byte{tag}
	switch n := len(content); {
	case n < 0x80:
		out = append(out, byte(n))
	default:
		var length []byte
		for v := n; v > 0; v >>= 8 {
			length = append([]byte{byte(v)}, length...)
		}
		out = append(out, byte(0x80|len(length)))
		out = append(out, length...)
	}
	return append(out, content...)
}

// derBitString wraps content with the leading "unused bits" octet, which is
// always zero here because every value is a whole number of bytes.
func derBitString(content []byte) []byte {
	return derWrap(derBitStringTag, append([]byte{0}, content...))
}

// rdn builds SET { SEQUENCE { type, value } } — one attribute per RDN, which is
// what OpenSSL emits for a config file that lists them on separate lines.
func rdn(oid asn1.ObjectIdentifier, stringTag byte, value string) []byte {
	attr := derWrap(derSequence, concat(marshalOID(oid), derWrap(stringTag, []byte(value))))
	return derWrap(derSet, attr)
}

// extension builds SEQUENCE { extnID, extnValue }, omitting the critical flag.
// Omitted rather than written as FALSE: DER forbids encoding a DEFAULT at its
// default value, and the manual's sample CSR omits it.
func extension(oid asn1.ObjectIdentifier, value []byte) []byte {
	return derWrap(derSequence, concat(marshalOID(oid), derWrap(derOctetString, value)))
}

func marshalOID(oid asn1.ObjectIdentifier) []byte {
	out, err := asn1.Marshal(oid)
	if err != nil {
		// Every OID in this file is a compile-time constant, so this is
		// unreachable outside of an edit that breaks one.
		panic("zatca: bad object identifier")
	}
	return out
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func isAlpha(s string) bool {
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return len(s) > 0
}
