package zatca

import (
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
