-- 0060 — The CSR layout, read out of the screenshots nobody had opened.
--
-- On 2026-08-23 the Developer Portal Manual was obtained from zatca.gov.sa and
-- read directly:
--
--   ZATCA, "Developer Portal Manual", §5.3 "Generate a Certificate Signing
--   Request (CSR)", pages 91-96. 13,251,887 bytes, Last-Modified 2022-11-27.
--   https://zatca.gov.sa/en/E-Invoicing/Introduction/Guidelines/Documents/
--   DEVELOPER-PORTAL-MANUAL.pdf
--
-- # Why this document was previously read as saying nothing
--
-- 0045 recorded that the manual "defers all of it", and that was a fair reading
-- of its TEXT. §5.3's substance is not in the text layer. It is in embedded
-- images: the section reads "The screenshot below represents the information
-- the user must use to generate a CSR ... and its configuration file as shown
-- below" and then stops, because what follows is a picture.
--
-- The images were extracted from the PDF and read. Two of them carry what four
-- previous verification passes went looking for.
--
-- # What is now verified
--
-- SA.ZATCA.CSR_SUBJECT_LAYOUT. 0059 left this rule unverified for one stated
-- reason: "the OID under which certificateTemplateName is carried ... does not
-- appear anywhere in this document. A CSR with a correct DN and no template
-- extension is still refused at onboarding."
--
-- Page 91 is the complete OpenSSL configuration file, and its first three lines
-- are:
--
--   oid_section = OIDs
--   [ OIDs ]
--   certificateTemplateName= 1.3.6.1.4.1.311.20.2
--
-- That is the stated unknown, closed. The same screenshot gives the whole
-- layout, which had been assembled from a different document and never seen as
-- a working artefact:
--
--   [ dn ]     C, OU, O, CN
--   [req_ext]  certificateTemplateName = ASN1:PRINTABLESTRING:ZATCA-Code-Signing
--              subjectAltName = dirName:alt_names
--   [alt_names] SN, UID, title, registeredAddress, businessCategory
--
-- Page 95 is a sample CSR. It was transcribed and decoded with openssl, which
-- is self-checking — a mis-transcribed base64 blob does not parse as ASN.1. It
-- parses, and confirms the curve (secp256k1), the signature algorithm
-- (ecdsa-with-SHA256) and the template extension carrying a PrintableString.
--
-- The layout is implemented in zatca/csr.go and pinned by csr_test.go, which
-- verifies the emitted request with openssl rather than against a golden blob
-- this repository produced itself.
--
-- # Three places the manual disagrees with itself, left visible rather than resolved
--
-- 1. The config file's [dn] holds C/OU/O/CN and puts the ZATCA-specific values
--    in [alt_names]. The sample CSR instead carries serialNumber and
--    organizationIdentifier in the SUBJECT, has no subjectAltName at all, and
--    its organizationIdentifier is "PSDFI-FINFSA-29884997" — which breaks the
--    manual's own rule that the value is 15 digits beginning and ending with 3.
--    The sample is an illustration; the config file is what the manual tells
--    the reader to use. The config file is implemented.
--
-- 2. The config defines the ZATCA fields in [req_ext], but the command on page
--    95 passes "-extensions v3_req", a section holding only basicConstraints
--    and keyUsage. Run literally, that command emits a CSR containing none of
--    ZATCA's data — and the sample CSR matches neither section, carrying the
--    template extension and neither basicConstraints nor keyUsage. [req_ext] is
--    the only section with the values ZATCA needs, so [req_ext] is implemented.
--
-- 3. Page 91's config says ZATCA-Code-Signing; page 95's sample CSR carries
--    TSTZATCA-Code-Signing. See the note added to SA.ZATCA.CSR_CERTIFICATE_TEMPLATE
--    below.
--
-- # release_blocker is not touched
--
-- Same convention as 0059 and SA.ZATCA.HASH_ALGORITHM: verifying a rule records
-- that its content is known. Whether it still blocks release is a separate
-- judgement, and a CSR that can be built still cannot be SENT — see
-- SA.ZATCA.ONBOARDING_REQUEST_FORMAT, which stays open below.

ALTER TABLE regulatory_rule DISABLE TRIGGER regulatory_rule_frozen_fields;

-- ---------------------------------------------------------------------------
-- Verified: the CSR subject layout
-- ---------------------------------------------------------------------------

UPDATE regulatory_rule
SET payload = jsonb_build_object(
      'subject_rdns', jsonb_build_array('C', 'OU', 'O', 'CN'),
      'alt_name_rdns', jsonb_build_array(
        'SN', 'UID', 'title', 'registeredAddress', 'businessCategory'),
      'subject_alt_name_form', 'dirName',
      'certificate_template_oid', '1.3.6.1.4.1.311.20.2',
      'certificate_template_encoding', 'ASN1:PRINTABLESTRING',
      'signature_algorithm', 'ecdsa-with-SHA256',
      'country_code', 'ISO 3166 Alpha-2',
      'egs_serial_form', '1-<solution>|2-<model>|3-<serial>',
      'organization_identifier', jsonb_build_object(
        'digits', 15, 'starts_with', '3', 'ends_with', '3'),
      'invoice_type_positions', 'TSCZ',
      'invoice_type_format', '4 binary digits, not all zero',
      'string_types', jsonb_build_object(
        'C', 'PrintableString', 'SN', 'PrintableString', 'default', 'UTF8String')
    ),
    source_document = 'Developer Portal Manual §5.3.1 (config file, p.91) and §5.3.3 (sample CSR, p.95)',
    source_url = 'https://zatca.gov.sa/en/E-Invoicing/Introduction/Guidelines/Documents/DEVELOPER-PORTAL-MANUAL.pdf',
    verified_on = DATE '2026-08-23',
    notes = notes ||
      ' VERIFIED 2026-08-23 from the Developer Portal Manual §5.3, read from the'
      ' EMBEDDED IMAGES rather than the text layer — which is why 0045 concluded'
      ' this document "defers all of it". Page 91 prints the complete OpenSSL'
      ' config file and closes the single unknown 0059 left: "certificateTemplateName='
      ' 1.3.6.1.4.1.311.20.2". Subject is C/OU/O/CN; SN, UID, title,'
      ' registeredAddress and businessCategory travel in subjectAltName as a'
      ' dirName. Page 95''s sample CSR was transcribed and decoded with openssl'
      ' (self-checking: a bad transcription does not parse) and confirms'
      ' secp256k1 and ecdsa-with-SHA256. Implemented in zatca/csr.go and pinned'
      ' by csr_test.go, which verifies the emitted request with openssl rather'
      ' than against a golden value this repository generated itself. NOT'
      ' resolved: the manual contradicts itself three times — see migration 0060'
      ' and the csr.go header for each. release_blocker is deliberately'
      ' unchanged: the request can now be BUILT, not sent.'
WHERE rule_key = 'SA.ZATCA.CSR_SUBJECT_LAYOUT'
  AND verified_on IS NULL;

-- ---------------------------------------------------------------------------
-- Six of seven unknowns closed, and the rule stays open for the seventh
-- ---------------------------------------------------------------------------
--
-- SA.ZATCA.ONBOARDING_REQUEST_FORMAT was recorded with seven __VERIFY__
-- placeholders. The Sandbox screenshots and the compliance CSID response body
-- answer six of them:
--
--   http_methods    POST /compliance, POST /production/csids,
--                   PATCH /production/csids (the Sandbox endpoint listing)
--   csr_body_field  "csr" (the Postman screenshot, p.96)
--   csr_encoding    base64 OF THE PEM BLOCK — the value begins
--                   LS0tLS1CRUdJTiBDRVJUSUZJQ0FURSBSRVFVRVNU, which decodes to
--                   "-----BEGIN CERTIFICATE REQUEST". Not base64 of the DER,
--                   which is the natural assumption and produces a request that
--                   looks right and is refused.
--   response_schema requestID, tokenType, dispositionMessage,
--                   binarySecurityToken, secret
--   compliance_request_id_field  "requestID"
--   status_codes    200 on success, with dispositionMessage "ISSUED"
--
-- The seventh is untouched and is why this rule stays a blocker: the header or
-- field carrying the OTP is still unnamed. The manual shows it only as "insert
-- valid OTP" beside a picture of a form. SA.ZATCA.ONBOARDING_OTP already
-- records that there is no API for obtaining one and that a human reads it off
-- the Fatoora portal within the hour — but neither rule says what to call it on
-- the wire, and a wrong name fails in a way indistinguishable from a wrong OTP.
--
-- So zatca/api.go implements the production CSID call and deliberately does NOT
-- implement the compliance CSID call.

UPDATE regulatory_rule
SET payload = jsonb_build_object(
      'csr_encoding', 'base64 of the PEM block, including the BEGIN/END lines',
      'csr_body_field', 'csr',
      'http_methods', jsonb_build_object(
        'compliance_csid', 'POST /compliance',
        'compliance_invoices', 'POST /compliance/invoices',
        'production_csid_issue', 'POST /production/csids',
        'production_csid_renew', 'PATCH /production/csids'),
      'status_codes', jsonb_build_object('200', 'issued'),
      'response_schema', jsonb_build_array(
        'requestID', 'tokenType', 'dispositionMessage',
        'binarySecurityToken', 'secret'),
      'compliance_request_id_field', 'requestID',
      'otp_header_name', '__VERIFY__'
    ),
    source_document = 'Developer Portal Manual, API Integration Sandbox screenshots and §2.3.10',
    source_url = 'https://zatca.gov.sa/en/E-Invoicing/Introduction/Guidelines/Documents/DEVELOPER-PORTAL-MANUAL.pdf',
    notes = notes ||
      ' PARTIAL 2026-08-23, Developer Portal Manual. Six of the seven __VERIFY__'
      ' placeholders are now filled from the Integration Sandbox screenshots and'
      ' the printed 200 response body: the HTTP verbs, the "csr" body field, the'
      ' CSR encoding (base64 of the PEM BLOCK, not of the DER — the sample value'
      ' decodes to "-----BEGIN CERTIFICATE REQUEST"), the response schema'
      ' (requestID, tokenType, dispositionMessage, binarySecurityToken, secret)'
      ' and the request-id field. STILL OPEN, and the reason this stays a'
      ' blocker: the header carrying the OTP is still unnamed anywhere in the'
      ' official documents. zatca/api.go therefore implements the production'
      ' CSID call and deliberately omits the compliance CSID call rather than'
      ' inventing a header name whose failure looks like a bad OTP.'
WHERE rule_key = 'SA.ZATCA.ONBOARDING_REQUEST_FORMAT'
  AND notes NOT LIKE '%PARTIAL 2026-08-23%';

-- ---------------------------------------------------------------------------
-- A note against the sandbox template name this registry said not to infer
-- ---------------------------------------------------------------------------
--
-- SA.ZATCA.CSR_CERTIFICATE_TEMPLATE is verified for production
-- (ZATCA-Code-Signing) and simulation (PREZATCA-Code-Signing), and says the
-- Developer Portal Integration Sandbox value "is not published in any of the
-- four documents and must not be inferred from these two".
--
-- A third value now has a source: the sample CSR in the Developer Portal
-- Manual — the sandbox's own manual — decodes to TSTZATCA-Code-Signing. That is
-- evidence, not a rule, because the config file eleven pages earlier in the
-- SAME section prints ZATCA-Code-Signing. The manual shows two values for one
-- environment and assigns neither, so the payload is left alone and the
-- observation is recorded.

UPDATE regulatory_rule
SET notes = notes ||
      ' OBSERVED 2026-08-23: the sample CSR on p.95 of the Developer Portal'
      ' Manual decodes to certificateTemplateName "TSTZATCA-Code-Signing" — a'
      ' third value, from the Integration Sandbox''s own manual, against the gap'
      ' this rule records. It is NOT promoted into the payload: the config file'
      ' in the same section prints "ZATCA-Code-Signing", so the manual shows two'
      ' values for one environment and assigns neither. zatca.PublishedTemplateNames'
      ' carries all three and BuildCSR requires the caller to choose.'
WHERE rule_key = 'SA.ZATCA.CSR_CERTIFICATE_TEMPLATE'
  AND notes NOT LIKE '%OBSERVED 2026-08-23%';

-- ---------------------------------------------------------------------------
-- A correction against the key parameters
-- ---------------------------------------------------------------------------
--
-- SA.ZATCA.CSR_KEY_PARAMETERS records public_key_point_form = "compressed",
-- taken from "openssl ec -in PrivateKey.pem -pubout -conv_form compressed".
-- That command is real, but it produces a SEPARATE PublicKey.pem artefact used
-- elsewhere; it is not what lands in the CSR. Decoding ZATCA's own sample CSR
-- shows the subjectPublicKeyInfo carrying an UNCOMPRESSED point: 65 bytes
-- beginning 04.
--
-- The payload is not rewritten — the value is true of the artefact the command
-- names — but an implementer reading only that field would encode the wrong
-- point into the request, so the distinction is recorded here.

UPDATE regulatory_rule
SET notes = notes ||
      ' CLARIFIED 2026-08-23: public_key_point_form "compressed" describes the'
      ' PublicKey.pem produced by "openssl ec -pubout -conv_form compressed",'
      ' which is a separate artefact. The CSR itself carries an UNCOMPRESSED'
      ' point — decoding the sample CSR on p.95 of the Developer Portal Manual'
      ' shows 65 bytes beginning 04. zatca/csr.go encodes the uncompressed form'
      ' into subjectPublicKeyInfo on that evidence.'
WHERE rule_key = 'SA.ZATCA.CSR_KEY_PARAMETERS'
  AND notes NOT LIKE '%CLARIFIED 2026-08-23%';

ALTER TABLE regulatory_rule ENABLE TRIGGER regulatory_rule_frozen_fields;
