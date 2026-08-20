-- 0046 — The QR TLV field set and byte layout, read from the standard.
--
-- SA.ZATCA.QR_TLV_FIELDS was seeded in 0005 as {"fields": "__VERIFY__"} with the
-- note "Exact TLV field set and byte layout". Both of those are answered by
-- E-invoicing Detailed Technical Guideline V2 §6 (pp. 58-64), which was read on
-- 2026-08-19, so the rule is verified for the scope it claimed.
--
-- What §6 states, quoted:
--
--   "QR code is the base64 encoded TLV (Tag, Length, Value)"
--   "Tag: The tag value as mentioned above stored in one byte."
--   "Length: The length of the byte array resulted from the UTF8 encoding of the
--    field value. The length shall be stored in one byte."
--   "Value: The byte array resulting from the UTF8 encoding of the field value."
--   "There should be no padding or separators between the TLV sets in the
--    resulting file"
--   "It is mandatory to generate and print QR code encoded in Base64 format with
--    up to 700 characters"
--
-- The nine tags and their enforcement dates come from the table on p. 58.
--
-- This is NOT a blanket verification of the QR. One question inside it is worse
-- than unanswered — it is answered inconsistently by the standard itself — and
-- 0044 established that such a half goes in as its own blocker rather than
-- hiding under a verified_on. That half is SA.ZATCA.QR_TAG_VALUE_ENCODING below.
--
-- # Why this edits the row in place
--
-- Following 0011: correcting a rule normally means closing the old row's
-- effective_to and inserting a new one, which is right for a legal change and
-- wrong here. ZATCA's QR specification did not change on 2026-08-19 — it has
-- said this since the tags were enforced in 2021, and what changed is that
-- someone read it. Opening a new effective period would record a legal event
-- that never happened, and a report reconstructing "what did we believe the
-- rules were" would show a phantom. So the frozen-column trigger is lifted for
-- the one statement that replaces a __VERIFY__ placeholder with the values it
-- was always standing in for.

ALTER TABLE regulatory_rule DISABLE TRIGGER regulatory_rule_frozen_fields;

UPDATE regulatory_rule SET
  payload = '{"encoding": "base64_of_the_whole_tlv_stream",
              "tag_bytes": 1,
              "length_bytes": 1,
              "length_counts": "bytes_of_the_utf8_encoded_value",
              "separators": "none",
              "order": "ascending_by_tag",
              "max_base64_chars": 700,
              "fields": {"1": "seller_name",
                         "2": "seller_vat_registration_number",
                         "3": "invoice_timestamp",
                         "4": "invoice_total_including_vat",
                         "5": "vat_total",
                         "6": "hash_of_the_xml_invoice",
                         "7": "ecdsa_signature",
                         "8": "ecdsa_public_key",
                         "9": "zatca_ca_signature_over_that_public_key"},
              "enforced_from": {"tags_1_to_5": "2021-12-04",
                                "tags_6_to_9": "2023-01-01, in waves"}}'::jsonb,
  source_document = 'E-invoicing Detailed Technical Guideline V2 §6 pp.58-64',
  verified_on = DATE '2026-08-19',
  notes = 'VERIFIED for the field set and the byte layout, which is what this '
    'rule was seeded to cover. One byte of tag, one byte of length, that many '
    'bytes of value, nothing between fields, and the whole stream base64 encoded '
    'to at most 700 characters. The length counts BYTES of the UTF-8 encoding, '
    'not characters — §6.4 lists "Not using UTF8 Encoding for Arabic Text" among '
    'the common mistakes, and an Arabic seller name runs about two bytes per '
    'letter, so a character count would truncate every Arabic value. Both worked '
    'payloads published in §6 are reproduced byte for byte by zatca.EncodeQR as '
    'golden tests. SCOPE: the framing and the field set only. How the values of '
    'tags 6 to 9 are themselves encoded is a separate rule, '
    'SA.ZATCA.QR_TAG_VALUE_ENCODING, which remains unverified because the '
    'standard contradicts itself on it.'
WHERE rule_key = 'SA.ZATCA.QR_TLV_FIELDS'
  AND payload->>'fields' = '__VERIFY__';

ALTER TABLE regulatory_rule ENABLE TRIGGER regulatory_rule_frozen_fields;

-- The half the standard answers two different ways.
INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from,
   source_authority, source_document, source_url, release_blocker, notes)
VALUES
('SA.ZATCA.QR_TAG_VALUE_ENCODING', 'sa',
 '{"tag_6_value": "__VERIFY__",
   "tag_7_value": "__VERIFY__",
   "tag_8_value": "__VERIFY__",
   "tag_9_value": "__VERIFY__"}'::jsonb,
 '2024-01-01', 'zatca',
 'E-invoicing Detailed Technical Guideline V2 §6.1-§6.2 (self-contradictory); '
 'E-Invoicing Security Features Implementation Standard',
 'https://zatca.gov.sa', true,
 'RELEASE BLOCKER, and unusual in that the standard does not omit this — it '
 'answers it two ways. §6.1 states normatively that a value is "the byte array '
 'resulting from the UTF8 encoding of the field value", which describes text. '
 'The worked payload on pp.60-62 does something else: decoded, its tag 6 holds '
 '44 bytes of ASCII base64 ("QnVEexW4...") and tag 7 holds 96 bytes of ASCII '
 'base64 ("MEUCIQD5..."), while tag 8 holds 88 raw DER bytes beginning 0x30 0x56 '
 'and tag 9 holds 72 raw DER bytes beginning 0x30 0x46. Raw DER is not UTF-8 '
 'text, and tag 8 in that document''s own hex table is visibly mangled by a '
 'UTF-8 round trip (0xEF 0xBF 0xBD replacement characters), which suggests the '
 'confusion is the authority''s and not the reader''s. The length table printed '
 'beside the same payload disagrees with the payload on five of nine tags — it '
 'says 6, 45, 192, 48 and 144 for tags 4, 5, 7, 8 and 9 where the payload '
 'carries 7, 5, 96, 88 and 72 — and states tag 5 as "114.90" where the payload '
 'says "144.9". Because a scanner reads the payload, the payload was treated as '
 'authoritative for the golden tests, but a contradiction of this size must not '
 'be resolved by preferring whichever half suits us. Resolve it against the '
 'Security Features Implementation Standard, whose certificate and signature '
 'profile is the natural home for the answer. Until then zatca.ValidateQR checks '
 'the framing of tags 6 to 9 and says nothing about their contents, and no code '
 'in this repository constructs them.');
