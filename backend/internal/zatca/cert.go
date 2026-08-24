package zatca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Certificates: reading the real one, and making a fake one for development.
//
// # Reading
//
// ParseCertificate pulls the issuer, serial and public key out of a DER
// certificate without crypto/x509, which refuses secp256k1 for the same reason
// the rest of the standard library does — it only knows the NIST curves. Only
// the fields the signature needs are read; this is not a general X.509 parser
// and does not pretend to validate anything.
//
// # Making
//
// SelfSignedDevelopmentCertificate exists so the whole signing path can be
// exercised, and tested, before a taxpayer has onboarded. It is NOT a CSID and
// must never be treated as one:
//
//   - ZATCA's certificate is issued by ZATCA's CA. This one is issued by
//     itself, and any validator checking the chain will say so.
//   - it is refused in production by NewCertificateStore, which is the gate
//     that keeps it out of a real deployment.
//
// Its purpose is to make "does the XAdES structure, the canonicalisation and
// the digests hold together" a question that can be answered on a laptop,
// rather than one that waits on an onboarding nobody can perform yet.

// ParseCertificate reads what the signature needs out of a DER certificate.
func ParseCertificate(der []byte, signer crypto.Signer) (Certificate, error) {
	bad := errs.New(errs.CodeInvalidInput,
		"That does not read as an X.509 certificate.")

	// Certificate ::= SEQUENCE { tbsCertificate, signatureAlgorithm, signature }
	_, body, _, err := derParse(der)
	if err != nil {
		return Certificate{}, bad
	}
	_, tbs, _, err := derParse(body)
	if err != nil {
		return Certificate{}, bad
	}

	// TBSCertificate ::= SEQUENCE { [0] version, serialNumber, signature,
	//                               issuer, validity, subject, spki, ... }
	rest := tbs
	if len(rest) > 0 && rest[0] == derContextConstructed|0 {
		_, _, next, err := derParse(rest)
		if err != nil {
			return Certificate{}, bad
		}
		rest = next
	}

	serialTag, serialBytes, rest, err := derParse(rest)
	if err != nil || serialTag != derInteger {
		return Certificate{}, bad
	}
	serial := new(big.Int).SetBytes(serialBytes)

	// signature AlgorithmIdentifier, skipped.
	_, _, rest, err = derParse(rest)
	if err != nil {
		return Certificate{}, bad
	}

	// issuer Name
	_, issuerBody, rest, err := derParse(rest)
	if err != nil {
		return Certificate{}, bad
	}

	// validity ::= SEQUENCE { notBefore Time, notAfter Time }
	//
	// Read rather than skipped, because a certificate that has quietly expired
	// is the difference between "invoices are being reported" and "invoices
	// have been silently failing for a fortnight". Renewal needs a date, and
	// the only authoritative one is the certificate's own.
	var notAfter time.Time
	if _, validity, _, err := derParse(rest); err == nil {
		// notBefore first, then notAfter.
		if _, _, afterNotBefore, err := derParse(validity); err == nil {
			if tag, body, _, err := derParse(afterNotBefore); err == nil {
				notAfter = parseCertTime(tag, body)
			}
		}
	}

	return Certificate{
		DER:          der,
		IssuerName:   renderName(issuerBody),
		SerialNumber: serial.String(),
		NotAfter:     notAfter,
		Signer:       signer,
	}, nil
}

// parseCertTime reads an X.509 Time, which is one of two encodings.
//
// UTCTime carries a TWO-digit year, and RFC 5280 pins the pivot: values 50-99
// are 19xx and 00-49 are 20xx. Getting that wrong would read a 2049 expiry as
// 1949 and treat a valid certificate as long dead.
func parseCertTime(tag byte, body []byte) time.Time {
	switch tag {
	case derUTCTime:
		t, err := time.Parse("060102150405Z0700", string(body))
		if err != nil {
			if t, err = time.Parse("060102150405Z", string(body)); err != nil {
				return time.Time{}
			}
		}
		t = t.UTC()

		// Go's "06" layout pivots at 69: it reads 69-99 as 19xx and 00-68 as
		// 20xx. RFC 5280 §4.1.2.5.1 pivots at 50 -- "values 50-99 are 19xx,
		// 00-49 are 20xx" -- so the years 50 to 68 disagree, and Go reads them
		// a century late. Corrected here rather than tolerated: a certificate
		// whose expiry is read as 2068 instead of 1968 is one that never looks
		// like it needs renewing.
		if len(body) >= 2 {
			if yy := int(body[0]-'0')*10 + int(body[1]-'0'); yy >= 50 && t.Year() >= 2050 {
				t = t.AddDate(-100, 0, 0)
			}
		}
		return t
	case derGeneralizedTime:
		t, err := time.Parse("20060102150405Z0700", string(body))
		if err != nil {
			if t, err = time.Parse("20060102150405Z", string(body)); err != nil {
				return time.Time{}
			}
		}
		return t.UTC()
	default:
		return time.Time{}
	}
}

// renderName turns an RDNSequence into the one-line form X509IssuerName wants.
//
// RFC 2253 order: the RDNs are written in REVERSE of their DER order, which is
// the opposite of what reading the bytes suggests and a common way to produce
// an issuer string a verifier will not match.
func renderName(rdnSequence []byte) string {
	var parts []string
	rest := rdnSequence
	for len(rest) > 0 {
		_, rdn, next, err := derParse(rest)
		if err != nil {
			break
		}
		rest = next

		_, atv, _, err := derParse(rdn)
		if err != nil {
			continue
		}
		oidTag, oidBytes, valueRest, err := derParse(atv)
		if err != nil || oidTag != 0x06 {
			continue
		}
		_, value, _, err := derParse(valueRest)
		if err != nil {
			continue
		}
		parts = append(parts, shortNameFor(oidBytes)+"="+string(value))
	}

	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, ", ")
}

// shortNameFor maps the attribute types that appear in a ZATCA issuer.
func shortNameFor(oid []byte) string {
	switch string(oid) {
	case "\x55\x04\x03":
		return "CN"
	case "\x55\x04\x06":
		return "C"
	case "\x55\x04\x07":
		return "L"
	case "\x55\x04\x0a":
		return "O"
	case "\x55\x04\x0b":
		return "OU"
	case "\x55\x04\x05":
		return "SERIALNUMBER"
	case "\x55\x04\x61":
		return "OID.2.5.4.97"
	case "\x09\x92\x26\x89\x93\xf2\x2c\x64\x01\x01":
		return "UID"
	default:
		return "OID"
	}
}

// SelfSignedDevelopmentCertificate builds a certificate for development only.
//
// The subject and issuer are deliberately worded so that anybody who sees this
// certificate anywhere near a real invoice knows immediately what it is.
func SelfSignedDevelopmentCertificate(signer *SoftwareSigner, validFor time.Duration) (Certificate, error) {
	pub, ok := signer.Public().(*ecdsa.PublicKey)
	if !ok {
		return Certificate{}, errs.New(errs.CodeInternal, "the signer has no EC key")
	}
	if err := checkSecp256k1(pub); err != nil {
		return Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 96))
	if err != nil {
		return Certificate{}, errs.New(errs.CodeInternal, "no randomness available")
	}

	name := derWrap(derSequence, concat(
		rdn(oidCountry, derPrintableString, "SA"),
		rdn(oidOrganization, derUTF8String, "RawSyst development"),
		rdn(oidCommonName, derUTF8String, "NOT A ZATCA CSID — development only"),
	))

	now := time.Now().UTC()
	validity := derWrap(derSequence, concat(
		utcTime(now.Add(-time.Hour)),
		utcTime(now.Add(validFor)),
	))

	algorithm := derWrap(derSequence, marshalOID(oidECDSAWithSHA256))

	tbs := derWrap(derSequence, concat(
		// version [0] EXPLICIT INTEGER 2 (v3)
		derWrap(derContextConstructed|0, []byte{derInteger, 0x01, 0x02}),
		derWrap(derInteger, serial.Bytes()),
		algorithm,
		name, // issuer
		validity,
		name, // subject — self-signed
		subjectPublicKeyInfo(pub),
	))

	digest := sha256.Sum256(tbs)
	signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return Certificate{}, errs.New(errs.CodeInternal,
			"the development certificate could not be signed")
	}

	der := derWrap(derSequence, concat(tbs, algorithm, derBitString(signature)))
	return ParseCertificate(der, signer)
}

// utcTime encodes a time as an ASN.1 UTCTime, which X.509 uses until 2050.
func utcTime(t time.Time) []byte {
	return derWrap(derUTCTime, []byte(t.UTC().Format("060102150405Z")))
}

// PEM renders the certificate for storage or inspection.
func (c Certificate) PEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.DER})
}

// Base64 is the form ZATCA's binarySecurityToken carries and KeyInfo wants.
func (c Certificate) Base64() string {
	return base64.StdEncoding.EncodeToString(c.DER)
}

// CertificateFromBinarySecurityToken decodes what onboarding returned.
//
// ZATCA answers with the certificate base64-encoded in binarySecurityToken.
// Some environments return the base64 of the PEM rather than of the DER, so
// both are accepted — the alternative is an onboarding that fails with
// "not a certificate" against a service that just issued one.
func CertificateFromBinarySecurityToken(token string, signer crypto.Signer) (Certificate, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return Certificate{}, errs.New(errs.CodeInvalidInput,
			"That security token is not base64.")
	}

	if block, _ := pem.Decode(raw); block != nil && block.Type == "CERTIFICATE" {
		return ParseCertificate(block.Bytes, signer)
	}
	return ParseCertificate(raw, signer)
}

// IsDevelopmentCertificate reports whether this is the self-signed stand-in.
//
// Checked by name rather than by chain, because there is no chain to check: the
// point is to recognise the certificate this package itself makes, so it can be
// refused where it must never appear.
func (c Certificate) IsDevelopmentCertificate() bool {
	return strings.Contains(c.IssuerName, "NOT A ZATCA CSID")
}

func (c Certificate) describe() string {
	return fmt.Sprintf("issuer %q serial %s", c.IssuerName, c.SerialNumber)
}
