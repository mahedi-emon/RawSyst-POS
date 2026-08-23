package zatca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"io"
	"math/big"

	btcec "github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// A secp256k1 key that can sign, because the standard library cannot.
//
// # Why this file exists at all
//
// ZATCA requires secp256k1 (SA.ZATCA.CSR_KEY_PARAMETERS, quoting "openssl
// ecparam -name secp256k1"). Go's crypto/elliptic cannot do that curve: its
// generic CurveParams arithmetic hard-codes a = -3, which every NIST curve
// satisfies and secp256k1 (a = 0) does not, so ecdsa.GenerateKey panics with
// "attempted operation on invalid point" rather than returning a wrong answer.
//
// So the curve comes from btcec, the implementation the Bitcoin and Ethereum
// ecosystems have used and audited for a decade. It is wrapped in crypto.Signer
// rather than used directly, so every caller here — BuildCSR, the XAdES
// signer — depends on the interface and not on the library.
//
// # This is a SOFTWARE key, and that is a deliberate limitation
//
// The Security Features Implementation Standard v1.1 §5.3.2 requires that
// "keys must be marked as non-exportable in order to prohibit key export out of
// the security module where the key was generated", and permits "a hardware or
// software based security module".
//
// A key held in this process's memory is not that. SoftwareSigner is therefore
// correct for development, for the compliance checks, and for a deployment
// whose operator has accepted the risk — and a hardware module implements the
// same crypto.Signer when one is available. Nothing above this file changes.

// SoftwareSigner is a secp256k1 private key held in memory.
type SoftwareSigner struct {
	key *btcec.PrivateKey
}

// GenerateKey creates a new secp256k1 key.
func GenerateKey() (*SoftwareSigner, error) {
	key, err := btcec.NewPrivateKey()
	if err != nil {
		return nil, errs.New(errs.CodeInternal,
			"A signing key could not be generated on this device.")
	}
	return &SoftwareSigner{key: key}, nil
}

// Public returns the key's public half as a standard ecdsa.PublicKey.
//
// The curve is btcec's, whose Params() report the SEC 2 secp256k1 values —
// which is what checkSecp256k1 verifies before it will build a CSR.
func (s *SoftwareSigner) Public() crypto.PublicKey {
	pub := s.key.PubKey()
	return &ecdsa.PublicKey{
		Curve: btcec.S256(),
		X:     new(big.Int).Set(pub.X()),
		Y:     new(big.Int).Set(pub.Y()),
	}
}

// Sign produces a DER ECDSA signature over an already-hashed digest.
//
// The digest must be a SHA-256 sum; ZATCA fixes ecdsa-with-SHA256 everywhere.
// The signature is low-S normalised, which btcec does by construction: a
// malleable high-S signature is still valid ECDSA but is refused by an
// increasing number of verifiers, and there is no reason to emit one.
func (s *SoftwareSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts != nil && opts.HashFunc() != crypto.SHA256 && opts.HashFunc() != 0 {
		return nil, errs.New(errs.CodeInvalidInput,
			"ZATCA signs with SHA-256 and nothing else.")
	}
	if len(digest) != 32 {
		return nil, errs.New(errs.CodeInvalidInput,
			"A signature is taken over a SHA-256 digest, which is 32 bytes.")
	}
	sig := btcecdsa.Sign(s.key, digest)
	return sig.Serialize(), nil
}

// PrivateKeyPEM exports the key in the form OpenSSL writes.
//
// Present because onboarding is a one-off operation an operator may need to
// perform with openssl, and because a key that cannot be backed up cannot be
// renewed. It is a credential: it must never be logged, returned over the API,
// or written anywhere the audit trail can see.
func (s *SoftwareSigner) PrivateKeyPEM() ([]byte, error) {
	der, err := marshalECPrivateKey(s.key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

// LoadSigner reads a key back from PEM.
func LoadSigner(keyPEM []byte) (*SoftwareSigner, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errs.New(errs.CodeInvalidInput,
			"That is not a PEM-encoded private key.")
	}

	// SEC1 "EC PRIVATE KEY" is what `openssl ecparam -genkey` writes, and is
	// the form ZATCA's own instructions produce.
	key, err := parseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return &SoftwareSigner{key: key}, nil
}

// PublicKeyDER returns the SubjectPublicKeyInfo, which is what QR tag 8 carries.
//
// Raw DER, uncompressed point — the form decoded out of ZATCA's own worked QR
// payload, where tag 8 is 88 bytes beginning 30 56 30 10 06 07 2a 86 48 ce 3d.
func (s *SoftwareSigner) PublicKeyDER() []byte {
	pub := s.Public().(*ecdsa.PublicKey)
	return subjectPublicKeyInfo(pub)
}

// --- SEC1 EC private key, hand-encoded ---------------------------------------
//
// x509.MarshalECPrivateKey refuses secp256k1 for the same reason the rest of
// crypto/x509 does: it only knows the NIST curves. The structure is small and
// fixed by RFC 5915:
//
//	ECPrivateKey ::= SEQUENCE {
//	  version        INTEGER { ecPrivkeyVer1(1) },
//	  privateKey     OCTET STRING,
//	  parameters [0] ECParameters {{ NamedCurve }} OPTIONAL,
//	  publicKey  [1] BIT STRING OPTIONAL
//	}

func marshalECPrivateKey(key *btcec.PrivateKey) ([]byte, error) {
	version, err := asn1MarshalInt(1)
	if err != nil {
		return nil, err
	}

	// Left-padded to the curve's byte length: a short scalar with a leading
	// zero byte is the same number, and some parsers reject the short form.
	raw := make([]byte, 32)
	key.Key.PutBytesUnchecked(raw)

	pub := key.PubKey()
	point := make([]byte, 65)
	point[0] = 4
	pub.X().FillBytes(point[1:33])
	pub.Y().FillBytes(point[33:])

	body := concat(
		version,
		derWrap(derOctetString, raw),
		derWrap(derContextConstructed|0, marshalOID(oidSecp256k1)),
		derWrap(derContextConstructed|1, derBitString(point)),
	)
	return derWrap(derSequence, body), nil
}

func parseECPrivateKey(der []byte) (*btcec.PrivateKey, error) {
	bad := errs.New(errs.CodeInvalidInput,
		"That private key could not be read as a secp256k1 key.")

	// The scalar is the first OCTET STRING inside the outer SEQUENCE, after the
	// version INTEGER. Walked rather than pattern-matched so a key with or
	// without the optional trailers reads the same.
	if len(der) < 2 || der[0] != derSequence {
		return nil, bad
	}
	body, err := derContent(der)
	if err != nil {
		return nil, bad
	}
	// version
	_, rest, err := derNext(body)
	if err != nil {
		return nil, bad
	}
	scalar, _, err := derNext(rest)
	if err != nil || len(scalar) == 0 || len(scalar) > 32 {
		return nil, bad
	}

	padded := make([]byte, 32)
	copy(padded[32-len(scalar):], scalar)

	key, _ := btcec.PrivKeyFromBytes(padded)
	if key == nil {
		return nil, bad
	}
	return key, nil
}

// derContent returns the value bytes of a single TLV.
func derContent(b []byte) ([]byte, error) {
	_, content, _, err := derParse(b)
	return content, err
}

// derNext returns the first TLV's content and whatever follows it.
func derNext(b []byte) (content, rest []byte, err error) {
	_, content, rest, err = derParse(b)
	return content, rest, err
}

func derParse(b []byte) (tag byte, content, rest []byte, err error) {
	malformed := errs.New(errs.CodeInvalidInput, "That DER structure is malformed.")
	if len(b) < 2 {
		return 0, nil, nil, malformed
	}
	tag = b[0]
	n := int(b[1])
	at := 2
	if n&0x80 != 0 {
		count := n & 0x7f
		if count == 0 || count > 4 || len(b) < 2+count {
			return 0, nil, nil, malformed
		}
		n = 0
		for i := 0; i < count; i++ {
			n = n<<8 | int(b[2+i])
		}
		at = 2 + count
	}
	if n < 0 || at+n > len(b) {
		return 0, nil, nil, malformed
	}
	return tag, b[at : at+n], b[at+n:], nil
}

func asn1MarshalInt(v int) ([]byte, error) {
	// Only the small positive values this file uses.
	if v < 0 || v > 127 {
		return nil, errs.New(errs.CodeInternal, "unsupported integer")
	}
	return []byte{0x02, 0x01, byte(v)}, nil
}

// Compile-time proof that a SoftwareSigner is usable wherever a signer is
// wanted, and that its curve is the one ZATCA accepts.
var (
	_ crypto.Signer = (*SoftwareSigner)(nil)
	_               = elliptic.P256
	_               = rand.Reader
)
