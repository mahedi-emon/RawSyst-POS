-- 0061 — The QR encoding, and the invoice ZATCA's own validator accepts.
--
-- Two rules close here, and one of them closes by overturning what 0059
-- decided. Both were settled against artefacts rather than prose.
--
-- # SA.ZATCA.QR_TAG_VALUE_ENCODING — verified, and tag 6 corrected
--
-- Technical Guideline V2 §6 prints a complete worked QR payload twice: as a hex
-- dump of the TLV stream, and as the base64 string. It was decoded and parsed:
-- 388 bytes, nine fields, no remainder. A payload that parses to exactly nine
-- well-formed fields with nothing left over is its own proof it was read right.
--
-- The encodings are not uniform, which is the whole difficulty:
--
--   tags 1-5  UTF-8 text
--   tag 6     UTF-8 text of the BASE64 of the digest    (44 bytes)
--   tag 7     UTF-8 text of the BASE64 of the signature (96 bytes)
--   tag 8     RAW DER SubjectPublicKeyInfo              (88 bytes, begins 0x30)
--   tag 9     RAW DER ECDSA signature                   (72 bytes, begins 0x30)
--
-- The stream stops being text between tag 7 and tag 8.
--
-- 0059 recorded tag 6 as the RAW 32-byte digest, quoting Security Features
-- v1.1 §4.1: "Length: length of hash (SHA256 ) is 32 bytes". zatca.QRHash was
-- written to enforce that and to REFUSE the 44-byte base64 text — which would
-- have rejected the authority's own published payload.
--
-- The two documents reconcile: §4.1's "32 bytes" describes the hash, and the
-- field carries its base64 rendering. The Developer Portal Manual corroborates
-- it independently, showing the Fatoora SDK printing
-- "INVOICE HASH = QnVEexW4nWv4CaE39a/66Jp/OXO/evHQ8pDlG7weq/4=" — the identical
-- value that sits in tag 6 of this payload.
--
-- zatca/qr.go now builds all nine, and TestTheOfficialWorkedPayloadIsReproduced
-- rebuilds ZATCA's published payload from nine independently transcribed values
-- and asserts it matches byte for byte.
--
-- # SA.ZATCA.UBL_FIELD_SET — verified, against ZATCA's validator
--
-- 0045 and the notes after it recorded that this rule could not be closed
-- because "the only thing that can confirm a generated document is ZATCA's own
-- validator, which ships in the Fatoora SDK" — and the SDK is behind a
-- SharePoint login that answers 403.
--
-- That premise was false. The validator is a public HTTP service:
--
--   POST https://gw-fatoora.zatca.gov.sa/e-invoicing/developer-portal/
--        validate-e-invoice/invoice/validate
--
-- It was found by reading the Integration Sandbox's own runtime configuration
-- at sandbox.zatca.gov.sa/env-config.js, where it is REACT_APP_API_URL. It
-- needs no account. The body is BASE64 of the XML with Content-Type text/plain
-- and an accepted-language header — the shape the sandbox page itself sends,
-- which is why posting raw XML returns 500 and made it look unusable.
--
-- It answers {"valid":…,"errors":[…],"warnings":[…]} with each entry naming a
-- rule (BR_KSA_ERROR / BR-KSA-EN16931-06, QR_CODE_ERROR, SIGNATURE_ERROR), so
-- it is an oracle rather than a yes/no.
--
-- zatca/ubl.go builds the document from the Electronic Invoice XML
-- Implementation Standard v1.2. Against the live validator:
--
--   standard invoice    valid: true, zero errors, zero warnings
--   simplified invoice  every business rule clean; fails only SIGNATURE_ERROR
--                       and QR_CODE_ERROR, which is exactly the shape of
--                       "correct but not yet onboarded"
--
-- Both are pinned by tests (ZATCA_VALIDATOR=1 runs them against the service).
--
-- # release_blocker
--
-- Left alone on both, per the convention 0059 and 0060 follow: verifying
-- records that a rule's content is known; whether it still gates release is a
-- separate judgement. The gate is `release_blocker AND verified_on IS NULL`,
-- so these two stop gating — correctly, because their content IS now known.
-- Nothing is thereby released: SA.ZATCA.ONBOARDING_REQUEST_FORMAT still gates,
-- and UnimplementedHasher and UnverifiedSubmitter both still refuse in
-- production, independently of the registry.

ALTER TABLE regulatory_rule DISABLE TRIGGER regulatory_rule_frozen_fields;

-- ---------------------------------------------------------------------------
-- Verified: every QR tag's value encoding
-- ---------------------------------------------------------------------------

UPDATE regulatory_rule
SET payload = jsonb_build_object(
      'tags_1_to_5', 'utf8 text',
      'tag_6', 'utf8 text of the base64 of the sha256 digest',
      'tag_7', 'utf8 text of the base64 of the DER ECDSA signature',
      'tag_8', 'raw DER SubjectPublicKeyInfo',
      'tag_9', 'raw DER ECDSA signature',
      'length_unit', 'bytes',
      'payload_encoding', 'base64 of the TLV stream',
      'worked_example_bytes', 388
    ),
    source_document = 'E-invoicing Detailed Technical Guideline V2 §6, worked example (hex and base64)',
    verified_on = DATE '2026-08-24',
    notes = notes ||
      ' VERIFIED 2026-08-24 against ZATCA''s own worked payload in Technical'
      ' Guideline V2 §6, printed there as both a hex dump and a base64 string.'
      ' Decoded it parses to 388 bytes, nine fields, no remainder. Tags 1-5 are'
      ' UTF-8 text; tag 6 is the base64 TEXT of the digest (44 bytes); tag 7 the'
      ' base64 TEXT of the DER signature; tags 8 and 9 are RAW DER and are not'
      ' valid UTF-8 at all. CORRECTS 0059, which read tag 6 as the raw 32-byte'
      ' digest from Security Features v1.1 §4.1 and made QRHash refuse the'
      ' 44-byte form — which would have rejected the authority''s own payload.'
      ' The documents reconcile: "32 bytes" describes the hash, the field'
      ' carries its base64. Corroborated by the Fatoora SDK output shown in the'
      ' Developer Portal Manual, which prints the identical tag-6 value.'
      ' zatca/qr.go builds all nine and a test rebuilds the published payload'
      ' byte for byte.'
WHERE rule_key = 'SA.ZATCA.QR_TAG_VALUE_ENCODING'
  AND verified_on IS NULL;

-- ---------------------------------------------------------------------------
-- Verified: the UBL field set, by the authority's own validator
-- ---------------------------------------------------------------------------

UPDATE regulatory_rule
SET payload = jsonb_build_object(
      'standard', 'UBL 2.1, Electronic Invoice XML Implementation Standard v1.2 (2023-05-19)',
      'profile_id', 'reporting:1.0',
      'currency', 'SAR',
      'transaction_code', 'KSA-2, 7 digits, positions 1-2 = 01 standard / 02 simplified',
      'document_type_codes', jsonb_build_object(
        'tax_invoice', '388', 'debit_note', '383',
        'credit_note', '381', 'prepayment_invoice', '386'),
      'chain_references', jsonb_build_array('ICV', 'PIH', 'QR'),
      'pre_signature_form_omits', jsonb_build_array(
        'ext:UBLExtensions', 'cac:AdditionalDocumentReference[cbc:ID=''QR'']'),
      'validator_url', 'https://gw-fatoora.zatca.gov.sa/e-invoicing/developer-portal/validate-e-invoice/invoice/validate',
      'validator_body', 'base64 of the XML, Content-Type text/plain, accepted-language header'
    ),
    source_document = 'Electronic Invoice XML Implementation Standard v1.2 (2023-05-19), checked by ZATCA''s public validator',
    source_url = 'https://gw-fatoora.zatca.gov.sa/e-invoicing/developer-portal/validate-e-invoice/invoice/validate',
    verified_on = DATE '2026-08-24',
    notes = notes ||
      ' VERIFIED 2026-08-24. The recorded blocker — that only the Fatoora SDK'
      ' (403) could confirm a generated document — was false. ZATCA''s validator'
      ' is a PUBLIC HTTP service, found as REACT_APP_API_URL in the Integration'
      ' Sandbox''s own env-config.js, needing no account. It takes BASE64 of the'
      ' XML as text/plain with an accepted-language header (raw XML returns 500,'
      ' which is what made it look unusable) and answers with per-rule errors and'
      ' warnings. zatca/ubl.go builds the document from the XML Implementation'
      ' Standard v1.2; against the live service a STANDARD invoice returns'
      ' valid:true with zero errors and zero warnings, and a SIMPLIFIED one is'
      ' clean on every business rule and fails only SIGNATURE_ERROR and'
      ' QR_CODE_ERROR — exactly the shape of "correct but not yet onboarded".'
      ' Both are pinned by tests run with ZATCA_VALIDATOR=1.'
WHERE rule_key = 'SA.ZATCA.UBL_FIELD_SET'
  AND verified_on IS NULL;

-- ---------------------------------------------------------------------------
-- Still blocked, and now precisely: the OTP header
-- ---------------------------------------------------------------------------
--
-- The endpoint URLs in SA.ZATCA.ONBOARDING_ENDPOINTS were independently
-- confirmed against ZATCA's own Fatoora portal configuration at
-- fatoora.zatca.gov.sa/env-config.js, which names each one:
--
--   REACT_APP_CCSID_URL       .../e-invoicing/core/compliance
--   REACT_APP_COMPLIANCE_URL  .../e-invoicing/core/compliance/invoices
--   REACT_APP_PCSID_URL       .../e-invoicing/core/production/csids
--   REACT_APP_REPORTING_URL   .../e-invoicing/core/invoices/reporting/single
--   REACT_APP_CLEARANCE_URL   .../e-invoicing/core/invoices/clearance/single
--
-- plus the same five under /simulation/. That is corroboration, not new
-- content, so the payload is untouched.
--
-- The OTP header remains unnamed. The Fatoora portal bundle was read: it
-- GENERATES and displays OTPs and never submits a CSR, so the header it would
-- travel in does not appear there either. The Technical Guideline says only
-- that OTP generation "is managed by the FATOORA portal and must be taken from
-- the portal itself" and that "there is no API for OTP".

UPDATE regulatory_rule
SET notes = notes ||
      ' CORROBORATED 2026-08-24: every endpoint URL was confirmed independently'
      ' against ZATCA''s own Fatoora portal runtime config at'
      ' fatoora.zatca.gov.sa/env-config.js (REACT_APP_CCSID_URL,'
      ' REACT_APP_COMPLIANCE_URL, REACT_APP_PCSID_URL, REACT_APP_REPORTING_URL,'
      ' REACT_APP_CLEARANCE_URL, and the same five under /simulation/). STILL'
      ' BLOCKED on otp_header_name only: the portal bundle generates and'
      ' displays OTPs and never submits a CSR, so the header does not appear'
      ' there, and no published document names it.'
WHERE rule_key = 'SA.ZATCA.ONBOARDING_REQUEST_FORMAT'
  AND notes NOT LIKE '%CORROBORATED 2026-08-24%';

ALTER TABLE regulatory_rule ENABLE TRIGGER regulatory_rule_frozen_fields;
