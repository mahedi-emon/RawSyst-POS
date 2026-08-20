package zatca

import (
	"encoding/base64"
	"unicode/utf8"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The QR code printed on a ZATCA invoice is a base64-encoded TLV stream: each
// field is one byte of tag, one byte of length, and that many bytes of value,
// with nothing between fields.
//
// Verified on 2026-08-19 against E-invoicing Detailed Technical Guideline V2
// §6 (pp. 58-64), quoted:
//
//   - "QR code is the base64 encoded TLV (Tag, Length, Value)"
//   - "Tag: The tag value as mentioned above stored in one byte."
//   - "Length: The length of the byte array resulted from the UTF8 encoding of
//     the field value. The length shall be stored in one byte."
//   - "Value: The byte array resulting from the UTF8 encoding of the field value."
//   - "There should be no padding or separators between the TLV sets in the
//     resulting file"
//   - "It is mandatory to generate and print QR code encoded in Base64 format
//     with up to 700 characters"
//
// What this file deliberately does NOT decide is how the values of tags 6 to 9
// are themselves encoded. ZATCA's worked example carries tags 6 and 7 as base64
// TEXT and tags 8 and 9 as raw DER BYTES — which the prose above does not
// describe, and which the length table printed beside that same example
// contradicts. That open question is SA.ZATCA.QR_TAG_VALUE_ENCODING and it
// blocks release. So the code below frames and reads a payload without claiming
// to know what tags 6 to 9 mean.

// QRTag identifies a field in the QR payload. The nine tags are fixed by the
// table on p. 58.
type QRTag byte

const (
	QRSellerName   QRTag = 1 // seller's name
	QRSellerVAT    QRTag = 2 // VAT registration number of the seller
	QRTimestamp    QRTag = 3 // invoice date and time
	QRInvoiceTotal QRTag = 4 // invoice total, including VAT
	QRVATTotal     QRTag = 5 // VAT total
	QRInvoiceHash  QRTag = 6 // hash of the XML invoice
	QRStamp        QRTag = 7 // ECDSA signature
	QRPublicKey    QRTag = 8 // ECDSA public key
	QRCAStamp      QRTag = 9 // ZATCA's CA signature over that public key
)

const (
	// QRMaxValueBytes is what one length byte can express. A field longer than
	// this cannot be represented at all, so it is refused rather than truncated.
	QRMaxValueBytes = 255

	// QRMaxBase64Chars is ZATCA's stated ceiling on the encoded payload.
	QRMaxBase64Chars = 700

	// Tags 1 to 5 carry an enforcement date of 4 December 2021 and are text.
	// Tags 6 to 9 began in waves from 1 January 2023 and depend on the document
	// type, so their absence here is not by itself malformed — that judgement
	// belongs to whatever knows the document type and the taxpayer's wave.
	qrLastAlwaysRequired = QRVATTotal
)

// QRField is one tag-length-value triple.
type QRField struct {
	Tag   QRTag
	Value []byte
}

// QRText builds a field from text, which is what tags 1 to 5 hold. The length
// written to the stream is the length in BYTES, not in characters: §6.4 lists
// "Not using UTF8 Encoding for Arabic Text" among the common mistakes, and an
// Arabic seller name is roughly two bytes per letter.
func QRText(tag QRTag, value string) QRField {
	return QRField{Tag: tag, Value: []byte(value)}
}

// EncodeQR renders fields as the base64 TLV payload.
//
// Fields must arrive in ascending tag order and each tag at most once. An
// out-of-order stream is refused rather than quietly sorted: the QR is derived
// from a document the terminal has already signed, and reordering someone's
// payload for them would change bytes they are accountable for.
func EncodeQR(fields ...QRField) (string, error) {
	if len(fields) == 0 {
		return "", errs.New(errs.CodeInvalidInput,
			"A QR payload needs at least one field.")
	}

	var (
		buf      []byte
		previous QRTag
	)
	for i, f := range fields {
		if f.Tag < QRSellerName || f.Tag > QRCAStamp {
			return "", errs.Newf(errs.CodeInvalidInput,
				"Tag %d is not one of the nine ZATCA QR fields.", f.Tag)
		}
		if i > 0 && f.Tag <= previous {
			return "", errs.Newf(errs.CodeInvalidInput,
				"QR fields must run in ascending tag order and appear once each, "+
					"and tag %d follows tag %d.", f.Tag, previous)
		}
		if len(f.Value) == 0 {
			return "", errs.Newf(errs.CodeInvalidInput,
				"Tag %d has no value, and every field present must carry one.", f.Tag)
		}
		if len(f.Value) > QRMaxValueBytes {
			return "", errs.Newf(errs.CodeInvalidInput,
				"Tag %d is %d bytes long and a QR field holds at most %d, because "+
					"its length is stored in a single byte.",
				f.Tag, len(f.Value), QRMaxValueBytes)
		}

		buf = append(buf, byte(f.Tag), byte(len(f.Value)))
		buf = append(buf, f.Value...)
		previous = f.Tag
	}

	out := base64.StdEncoding.EncodeToString(buf)
	if len(out) > QRMaxBase64Chars {
		return "", errs.Newf(errs.CodeInvalidInput,
			"This QR payload comes to %d characters and ZATCA allows up to %d.",
			len(out), QRMaxBase64Chars)
	}
	return out, nil
}

// DecodeQR reads a base64 TLV payload into its fields.
//
// It establishes that the bytes are a well-formed stream of known tags and
// nothing more. It does not establish that the values are the right values.
func DecodeQR(payload string) ([]QRField, error) {
	if payload == "" {
		return nil, errs.New(errs.CodeInvalidInput,
			"There is no QR payload here to read.")
	}
	if len(payload) > QRMaxBase64Chars {
		return nil, errs.Newf(errs.CodeInvalidInput,
			"This QR payload is %d characters and ZATCA allows up to %d.",
			len(payload), QRMaxBase64Chars)
	}

	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, errs.New(errs.CodeInvalidInput,
			"This QR payload is not valid base64, so it cannot be read.")
	}

	var (
		fields   []QRField
		previous QRTag
	)
	for i := 0; i < len(raw); {
		if i+2 > len(raw) {
			return nil, errs.New(errs.CodeInvalidInput,
				"This QR payload ends in the middle of a field: there is a tag "+
					"with no length after it.")
		}

		tag, length := QRTag(raw[i]), int(raw[i+1])
		if tag < QRSellerName || tag > QRCAStamp {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"This QR payload carries tag %d, which is not one of the nine "+
					"ZATCA fields.", tag)
		}
		if len(fields) > 0 && tag <= previous {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"This QR payload has tag %d after tag %d; the fields must ascend "+
					"and appear once each.", tag, previous)
		}

		end := i + 2 + length
		if end > len(raw) {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"Tag %d claims %d bytes but only %d are left, so this QR payload "+
					"is truncated.", tag, length, len(raw)-i-2)
		}

		// Copied rather than sub-sliced so a caller holding a field cannot see
		// or mutate the rest of the stream through it.
		value := make([]byte, length)
		copy(value, raw[i+2:end])

		fields = append(fields, QRField{Tag: tag, Value: value})
		previous = tag
		i = end
	}

	if len(fields) == 0 {
		return nil, errs.New(errs.CodeInvalidInput,
			"This QR payload decodes to nothing at all.")
	}
	return fields, nil
}

// ValidateQR reports whether a payload is a well-formed QR that the product is
// willing to store and print. Framing errors, unknown tags and a missing
// mandatory field are all refused.
func ValidateQR(payload string) error {
	fields, err := DecodeQR(payload)
	if err != nil {
		return err
	}

	seen := make(map[QRTag]bool, len(fields))
	for _, f := range fields {
		seen[f.Tag] = true

		// Only tags 1 to 5 are checked as text. ZATCA's own example carries tags
		// 8 and 9 as raw DER, which is not valid UTF-8, so demanding UTF-8 of
		// every field would reject the authority's own worked payload.
		if f.Tag <= qrLastAlwaysRequired && !utf8.Valid(f.Value) {
			return errs.Newf(errs.CodeInvalidInput,
				"Tag %d should be text and is not valid UTF-8.", f.Tag)
		}
	}

	for tag := QRSellerName; tag <= qrLastAlwaysRequired; tag++ {
		if !seen[tag] {
			return errs.Newf(errs.CodeInvalidInput,
				"This QR payload is missing tag %d. The seller name, VAT number, "+
					"timestamp, invoice total and VAT total have all been required "+
					"since 4 December 2021.", tag)
		}
	}
	return nil
}
