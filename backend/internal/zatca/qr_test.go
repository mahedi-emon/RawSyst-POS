package zatca

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// The two worked QR payloads ZATCA publishes in Technical Guideline V2 §6.
//
// These are golden fixtures in the strict sense: they are the authority's own
// bytes, not ours. If a change to the codec stops reproducing them exactly, the
// codec is wrong.
const (
	// p. 64. Tags 1 to 5 only, and internally consistent.
	officialQRPhase1 = "ARVCb2JzIEJhc2VtZW50IFJlY29yZHMCDzEwMDAyNTkwNjcwMDAwMwMU" +
		"MjAyMi0wNC0yNVQxNTozMDowMFoECjIxMDAxMDAuOTkFCTMxNTAxNS4xNQ=="

	// pp. 60-62. All nine tags. Reassembled from the wrapped lines of the PDF,
	// which is safe to assert because it decodes to a stream that consumes
	// exactly with no bytes left over — a mis-transcription would not.
	officialQRAllTags = "ARdBaG1lZCBNb2hhbWVkIEFMIEFobWFkeQIPMzAxMTIxOTcxNTAwMDAz" +
		"AxQyMDIyLTAzLTEzVDE0OjQwOjQwWgQHMTEwOC45MAUFMTQ0LjkGLFFuVkVleFc0bld2" +
		"NENhRTM5YS82NkpwL09YTy9ldkhROHBEbEc3d2VxLzQ9B2BNRVVDSVFENXp4eVhPQjdO" +
		"dldmNjJyVkVaQVlVNzFqcHk5SEVFblowcTlPOTZ3ckw2UUlnUUp6Q0dIYnc2WUJITFlW" +
		"ZE8xd25VaEJnS204ak1UeXZjazlNK3JQOXhZWT0IWDBWMBAGByqGSM49AgEGBSuBBAAK" +
		"A0IABGGDDKDmhWAITDv7LXqLX2cmr6+qddUkpcLCvWs5rC2O29W/hS4ajAK4Qdnahym6" +
		"MaijX75Cg3j4aao7ouYXJ9EJSDBGAiEA7mHT6yg85jtQGWp3M7tPT7Jk2+zsvVHGs3bU" +
		"5Z7YE68CIQD60ebQamYjYvdebnFjNfx4X4dop7LsEBFCNSsLY0IFaQ=="
)

// Reading ZATCA's phase-one payload must produce exactly the five documented
// values.
func TestTheOfficialPhaseOneQRDecodes(t *testing.T) {
	fields, err := DecodeQR(officialQRPhase1)
	if err != nil {
		t.Fatalf("decode the published payload: %v", err)
	}

	want := []struct {
		tag   QRTag
		value string
	}{
		{QRSellerName, "Bobs Basement Records"},
		{QRSellerVAT, "100025906700003"},
		{QRTimestamp, "2022-04-25T15:30:00Z"},
		{QRInvoiceTotal, "2100100.99"},
		{QRVATTotal, "315015.15"},
	}
	if len(fields) != len(want) {
		t.Fatalf("got %d fields, want %d", len(fields), len(want))
	}
	for i, w := range want {
		if fields[i].Tag != w.tag {
			t.Errorf("field %d has tag %d, want %d", i, fields[i].Tag, w.tag)
		}
		if got := string(fields[i].Value); got != w.value {
			t.Errorf("tag %d = %q, want %q", w.tag, got, w.value)
		}
	}
}

// And building it from those values must reproduce ZATCA's bytes exactly. This
// is the direction that catches a wrong length, a stray separator or base64
// padding applied in the wrong place.
func TestEncodingReproducesTheOfficialPhaseOneQR(t *testing.T) {
	got, err := EncodeQR(
		QRText(QRSellerName, "Bobs Basement Records"),
		QRText(QRSellerVAT, "100025906700003"),
		QRText(QRTimestamp, "2022-04-25T15:30:00Z"),
		QRText(QRInvoiceTotal, "2100100.99"),
		QRText(QRVATTotal, "315015.15"),
	)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got != officialQRPhase1 {
		t.Errorf("encoded payload does not match the published one:\n got %s\nwant %s",
			got, officialQRPhase1)
	}
}

// The nine-tag example is the one that matters for the tags we cannot yet
// interpret: tags 8 and 9 are raw DER, not text. Decoding and re-encoding must
// return the identical payload, byte for byte, without treating those values as
// strings.
func TestTheOfficialNineTagQRSurvivesARoundTrip(t *testing.T) {
	fields, err := DecodeQR(officialQRAllTags)
	if err != nil {
		t.Fatalf("decode the published payload: %v", err)
	}

	// Lengths taken from the payload itself. Note these are NOT the lengths
	// printed in the table beside it in the guideline: that table says 6, 45,
	// 192, 48 and 144 for tags 4, 5, 7, 8 and 9, which the payload contradicts.
	// The payload is the artefact a scanner reads, so the payload wins.
	wantLen := map[QRTag]int{
		QRSellerName: 23, QRSellerVAT: 15, QRTimestamp: 20,
		QRInvoiceTotal: 7, QRVATTotal: 5,
		QRInvoiceHash: 44, QRStamp: 96, QRPublicKey: 88, QRCAStamp: 72,
	}
	if len(fields) != len(wantLen) {
		t.Fatalf("got %d fields, want all %d tags", len(fields), len(wantLen))
	}
	for _, f := range fields {
		if want := wantLen[f.Tag]; len(f.Value) != want {
			t.Errorf("tag %d is %d bytes, want %d", f.Tag, len(f.Value), want)
		}
	}

	// Tag 8 carries a DER SubjectPublicKeyInfo rather than text, so a codec that
	// round-trips it through a string would corrupt it here.
	pub := fields[7]
	if pub.Tag != QRPublicKey {
		t.Fatalf("eighth field is tag %d, want %d", pub.Tag, QRPublicKey)
	}
	if pub.Value[0] != 0x30 {
		t.Errorf("tag 8 starts with %#x, want a DER sequence (0x30)", pub.Value[0])
	}

	got, err := EncodeQR(fields...)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if got != officialQRAllTags {
		t.Error("re-encoding the published nine-tag payload did not reproduce it")
	}
}

// An Arabic seller name is the case §6.4 singles out as a common mistake. The
// length byte must count bytes, not letters.
func TestArabicTextIsMeasuredInBytes(t *testing.T) {
	const name = "محمد" // four letters, eight bytes in UTF-8

	payload, err := EncodeQR(
		QRText(QRSellerName, name),
		QRText(QRSellerVAT, "300000000000003"),
		QRText(QRTimestamp, "2026-08-19T10:00:00Z"),
		QRText(QRInvoiceTotal, "115.00"),
		QRText(QRVATTotal, "15.00"),
	)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if raw[1] != 8 {
		t.Errorf("the length byte is %d; four Arabic letters are 8 bytes, and "+
			"writing the character count would truncate the value", raw[1])
	}

	fields, err := DecodeQR(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := string(fields[0].Value); got != name {
		t.Errorf("seller name came back as %q, want %q", got, name)
	}
}

func TestMalformedQRPayloadsAreRefused(t *testing.T) {
	// A well-formed stream, then the same stream damaged in specific ways.
	tlv := func(b ...byte) string { return base64.StdEncoding.EncodeToString(b) }

	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"empty", ""},
		{"not base64", "this is not base64 !!"},
		{"decodes to nothing", base64.StdEncoding.EncodeToString(nil)},
		{"tag with no length", tlv(0x01)},
		{"tag zero", tlv(0x00, 0x01, 0x41)},
		{"tag beyond nine", tlv(0x0a, 0x01, 0x41)},
		{"length overruns the stream", tlv(0x01, 0x09, 0x41, 0x42)},
		{"tags out of order", tlv(0x02, 0x01, 0x41, 0x01, 0x01, 0x42)},
		{"a tag repeated", tlv(0x01, 0x01, 0x41, 0x01, 0x01, 0x42)},
		{"trailing byte after the last field", tlv(0x01, 0x01, 0x41, 0x02)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeQR(tc.payload); err == nil {
				t.Errorf("this payload was accepted and should not have been")
			}
		})
	}
}

// A value too long to express is refused rather than silently truncated, which
// is the failure that would otherwise reach a printed receipt.
func TestAValueTooLongForOneLengthByteIsRefused(t *testing.T) {
	_, err := EncodeQR(QRText(QRSellerName, strings.Repeat("a", 256)))
	if err == nil {
		t.Fatal("a 256-byte field was accepted; one length byte cannot express it")
	}
	if !strings.Contains(err.Error(), "single byte") {
		t.Errorf("the error does not explain why: %v", err)
	}

	if _, err := EncodeQR(QRText(QRSellerName, strings.Repeat("a", 255))); err != nil {
		t.Errorf("255 bytes is the limit and should be accepted: %v", err)
	}
}

func TestAQRPayloadOverSevenHundredCharactersIsRefused(t *testing.T) {
	// Nine tags of 255 bytes each comfortably exceeds the ceiling.
	var fields []QRField
	for tag := QRSellerName; tag <= QRCAStamp; tag++ {
		fields = append(fields, QRText(tag, strings.Repeat("a", 255)))
	}
	if _, err := EncodeQR(fields...); err == nil {
		t.Fatal("a payload beyond 700 characters was accepted")
	}
}

func TestValidationRequiresTheFiveFieldsEnforcedSince2021(t *testing.T) {
	// Phase one is complete and valid.
	if err := ValidateQR(officialQRPhase1); err != nil {
		t.Errorf("the published phase-one payload was refused: %v", err)
	}
	// So is the nine-tag payload.
	if err := ValidateQR(officialQRAllTags); err != nil {
		t.Errorf("the published nine-tag payload was refused: %v", err)
	}

	// Dropping the VAT total leaves a structurally valid stream that is not a
	// usable QR, which is exactly the case framing checks alone would miss.
	short, err := EncodeQR(
		QRText(QRSellerName, "Bobs Basement Records"),
		QRText(QRSellerVAT, "100025906700003"),
		QRText(QRTimestamp, "2022-04-25T15:30:00Z"),
		QRText(QRInvoiceTotal, "2100100.99"),
	)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := DecodeQR(short); err != nil {
		t.Fatalf("the framing is fine, so decoding should succeed: %v", err)
	}
	if err := ValidateQR(short); err == nil {
		t.Error("a payload with no VAT total passed validation")
	}
}

// Tags 6 to 9 are not required here, because whether they apply depends on the
// document type and the taxpayer's wave. Asserted so that adding a blanket
// requirement later is a deliberate decision rather than a quiet one.
func TestTagsSixToNineAreNotRequiredByValidation(t *testing.T) {
	if err := ValidateQR(officialQRPhase1); err != nil {
		t.Errorf("a phase-one payload must remain valid: %v", err)
	}
}

// Tag 6 carries the base64 TEXT of the digest, not the raw digest.
//
// This reverses what these tests asserted on 2026-08-23, and the reversal is
// the point. The old reading came from Security Features v1.1 §4.1 ("length of
// hash (SHA256 ) is 32 bytes"); ZATCA's own worked payload in Technical
// Guideline V2 §6 carries 44 bytes of base64 text there. An artefact encoded in
// the format beats prose describing it, and the reproduction test below
// rebuilds that artefact byte for byte.
func TestTag6CarriesTheBase64TextOfTheDigest(t *testing.T) {
	digest := sha256.Sum256([]byte("an invoice"))

	field, err := QRHash(digest[:])
	if err != nil {
		t.Fatalf("a 32-byte digest was refused: %v", err)
	}
	if field.Tag != QRInvoiceHash {
		t.Errorf("tag = %d, want %d", field.Tag, QRInvoiceHash)
	}

	want := base64.StdEncoding.EncodeToString(digest[:])
	if string(field.Value) != want {
		t.Errorf("value = %q, want the base64 text %q", field.Value, want)
	}
	if len(field.Value) != 44 {
		t.Errorf("value is %d bytes, want 44", len(field.Value))
	}

	// The input is still strictly the raw digest. Passing the base64 text would
	// otherwise be encoded a second time, producing well-formed wrong bytes.
	if _, err := QRHash([]byte(want)); err == nil {
		t.Error("the base64 text of a digest was accepted as input to QRHash")
	}
	for _, wrong := range [][]byte{nil, {}, digest[:31], append(digest[:], 0)} {
		if _, err := QRHash(wrong); err == nil {
			t.Errorf("a %d-byte value was accepted as a SHA-256 digest", len(wrong))
		}
	}
}

// Tags 8 and 9 are raw DER, which is where the payload stops being text.
func TestTags8And9CarryRawDERAndRefuseBase64(t *testing.T) {
	der := []byte{0x30, 0x06, 0x02, 0x01, 0x01, 0x02, 0x01, 0x02}

	for _, c := range []struct {
		name  string
		build func([]byte) (QRField, error)
		tag   QRTag
	}{
		{"tag 8", QRPublicKeyField, QRPublicKey},
		{"tag 9", QRCAStampField, QRCAStamp},
	} {
		field, err := c.build(der)
		if err != nil {
			t.Fatalf("%s refused valid DER: %v", c.name, err)
		}
		if field.Tag != c.tag {
			t.Errorf("%s: tag = %d, want %d", c.name, field.Tag, c.tag)
		}
		if !bytes.Equal(field.Value, der) {
			t.Errorf("%s: the DER was not carried through unchanged", c.name)
		}

		// The base64 text of the same DER begins 'M', not 0x30.
		text := []byte(base64.StdEncoding.EncodeToString(der))
		if _, err := c.build(text); err == nil {
			t.Errorf("%s accepted base64 text where raw DER belongs", c.name)
		}
		if _, err := c.build(nil); err == nil {
			t.Errorf("%s accepted an empty value", c.name)
		}
	}
}

// Tag 7 is text: the base64 of the DER signature.
func TestTag7CarriesTheBase64TextOfTheSignature(t *testing.T) {
	der := []byte{0x30, 0x06, 0x02, 0x01, 0x01, 0x02, 0x01, 0x02}

	field, err := QRStampField(der)
	if err != nil {
		t.Fatalf("QRStampField refused valid DER: %v", err)
	}
	if want := base64.StdEncoding.EncodeToString(der); string(field.Value) != want {
		t.Errorf("value = %q, want %q", field.Value, want)
	}
}

// The whole point, in one assertion: rebuild ZATCA's published payload.
//
// The nine values are written out independently — the two base64 strings and
// the two DER blobs transcribed from Technical Guideline V2 §6 — and the
// expected result is the authority's own base64 string. If a constructor or the
// framing is wrong anywhere, these will not meet.
func TestTheOfficialWorkedPayloadIsReproduced(t *testing.T) {
	digest, err := base64.StdEncoding.DecodeString(officialInvoiceHash)
	if err != nil {
		t.Fatalf("the published hash is not base64: %v", err)
	}
	stamp, err := base64.StdEncoding.DecodeString(officialStampBase64)
	if err != nil {
		t.Fatalf("the published signature is not base64: %v", err)
	}

	hash, err := QRHash(digest)
	if err != nil {
		t.Fatalf("QRHash: %v", err)
	}
	tag7, err := QRStampField(stamp)
	if err != nil {
		t.Fatalf("QRStampField: %v", err)
	}
	tag8, err := QRPublicKeyField(officialPublicKeyDER)
	if err != nil {
		t.Fatalf("QRPublicKeyField: %v", err)
	}
	tag9, err := QRCAStampField(officialCAStampDER)
	if err != nil {
		t.Fatalf("QRCAStampField: %v", err)
	}

	got, err := EncodeQR(
		QRText(QRSellerName, "Ahmed Mohamed AL Ahmady"),
		QRText(QRSellerVAT, "301121971500003"),
		QRText(QRTimestamp, "2022-03-13T14:40:40Z"),
		QRText(QRInvoiceTotal, "1108.90"),
		QRText(QRVATTotal, "144.9"),
		hash, tag7, tag8, tag9,
	)
	if err != nil {
		t.Fatalf("EncodeQR: %v", err)
	}
	if got != officialQRPayload {
		t.Errorf("the rebuilt payload does not match ZATCA's published one."+
			"\ngot:  %s\nwant: %s", got, officialQRPayload)
	}
}

// A payload carrying tags 8 and 9 is not valid UTF-8 throughout, and must still
// decode and validate.
func TestTheOfficialWorkedPayloadValidates(t *testing.T) {
	if err := ValidateQR(officialQRPayload); err != nil {
		t.Fatalf("ZATCA's own payload was refused: %v", err)
	}
	fields, err := DecodeQR(officialQRPayload)
	if err != nil {
		t.Fatalf("DecodeQR: %v", err)
	}
	if len(fields) != 9 {
		t.Fatalf("decoded %d fields, want 9", len(fields))
	}
}

// --- ZATCA's worked example, Technical Guideline V2 §6 ----------------------

const (
	officialQRPayload = "ARdBaG1lZCBNb2hhbWVkIEFMIEFobWFkeQIPMzAxMTIxOTcxNTAwMDAzAxQyMDIyLTAzLTEzVDE0OjQwOjQwWgQHMTEwOC45MAUFMTQ0LjkGLFFuVkVleFc0bld2NENhRTM5YS82NkpwL09YTy9ldkhROHBEbEc3d2VxLzQ9B2BNRVVDSVFENXp4eVhPQjdOdldmNjJyVkVaQVlVNzFqcHk5SEVFblowcTlPOTZ3ckw2UUlnUUp6Q0dIYnc2WUJITFlWZE8xd25VaEJnS204ak1UeXZjazlNK3JQOXhZWT0IWDBWMBAGByqGSM49AgEGBSuBBAAKA0IABGGDDKDmhWAITDv7LXqLX2cmr6+qddUkpcLCvWs5rC2O29W/hS4ajAK4Qdnahym6MaijX75Cg3j4aao7ouYXJ9EJSDBGAiEA7mHT6yg85jtQGWp3M7tPT7Jk2+zsvVHGs3bU5Z7YE68CIQD60ebQamYjYvdebnFjNfx4X4dop7LsEBFCNSsLY0IFaQ=="

	// As the Fatoora SDK prints it in the Developer Portal Manual.
	officialInvoiceHash = "QnVEexW4nWv4CaE39a/66Jp/OXO/evHQ8pDlG7weq/4="

	officialStampBase64 = "MEUCIQD5zxyXOB7NvWf62rVEZAYU71jpy9HEEnZ0q9O96wrL6QIgQJzCGHbw6YBHLYVdO1wnUhBgKm8jMTyvck9M+rP9xYY="
)

// SubjectPublicKeyInfo: SEQUENCE, id-ecPublicKey, secp256k1, uncompressed point.
var officialPublicKeyDER = []byte{
	0x30, 0x56, 0x30, 0x10, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02,
	0x01, 0x06, 0x05, 0x2b, 0x81, 0x04, 0x00, 0x0a, 0x03, 0x42, 0x00, 0x04,
	0x61, 0x83, 0x0c, 0xa0, 0xe6, 0x85, 0x60, 0x08, 0x4c, 0x3b, 0xfb, 0x2d,
	0x7a, 0x8b, 0x5f, 0x67, 0x26, 0xaf, 0xaf, 0xaa, 0x75, 0xd5, 0x24, 0xa5,
	0xc2, 0xc2, 0xbd, 0x6b, 0x39, 0xac, 0x2d, 0x8e, 0xdb, 0xd5, 0xbf, 0x85,
	0x2e, 0x1a, 0x8c, 0x02, 0xb8, 0x41, 0xd9, 0xda, 0x87, 0x29, 0xba, 0x31,
	0xa8, 0xa3, 0x5f, 0xbe, 0x42, 0x83, 0x78, 0xf8, 0x69, 0xaa, 0x3b, 0xa2,
	0xe6, 0x17, 0x27, 0xd1,
}

// ZATCA's ECDSA signature over that certificate.
var officialCAStampDER = []byte{
	0x30, 0x46, 0x02, 0x21, 0x00, 0xee, 0x61, 0xd3, 0xeb, 0x28, 0x3c, 0xe6,
	0x3b, 0x50, 0x19, 0x6a, 0x77, 0x33, 0xbb, 0x4f, 0x4f, 0xb2, 0x64, 0xdb,
	0xec, 0xec, 0xbd, 0x51, 0xc6, 0xb3, 0x76, 0xd4, 0xe5, 0x9e, 0xd8, 0x13,
	0xaf, 0x02, 0x21, 0x00, 0xfa, 0xd1, 0xe6, 0xd0, 0x6a, 0x66, 0x23, 0x62,
	0xf7, 0x5e, 0x6e, 0x71, 0x63, 0x35, 0xfc, 0x78, 0x5f, 0x87, 0x68, 0xa7,
	0xb2, 0xec, 0x10, 0x11, 0x42, 0x35, 0x2b, 0x0b, 0x63, 0x42, 0x05, 0x69,
}
