-- 0045 — What ZATCA publishes about CSR and CSID onboarding, and what it does not.
--
-- Z1 built the EGS unit and captured the nine CSR inputs. The next step is to
-- turn those inputs into a real CSR, signed by a real key, and exchange it for a
-- Compliance CSID and then a Production CSID. This migration records what was
-- read from the official documents on 2026-08-19, and — more importantly — gives
-- the two remaining gaps their own release blockers so the gate names them.
--
-- Documents read, all downloaded from zatca.gov.sa:
--   * E-invoicing Detailed Technical Guideline, Version 2, Nov 2022 (81 pp.)
--   * FATOORA Portal User Manual, Version 3 (32 pp.)
--   * Developer Portal Manual, Version 3 (96 pp.)
--   * E-Invoicing Detailed Guidelines, Version 2, May 2023 (66 pp.)
--
-- The split follows 0044's lesson: a rule that mixes an answered question with
-- an unanswered one hides the unanswered half. So the cryptography, the template
-- names, the endpoints and the OTP go in as verified, and the CSR subject layout
-- and the onboarding request format go in as blockers, unverified by construction.
--
-- Nothing here asserts a value that was not read from one of those four PDFs.

INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from, source_authority,
   source_document, source_url, verified_on, release_blocker, notes)
VALUES

-- The curve is the surprise, so it is recorded on its own.
('SA.ZATCA.CSR_KEY_PARAMETERS', 'sa',
 '{"key_type": "ec",
   "curve": "secp256k1",
   "csr_signature_algorithm": "ecdsa-with-SHA256",
   "public_key_point_form": "compressed",
   "csr_extensions_section": "v3_req"}'::jsonb,
 '2022-11-01', 'zatca',
 'E-invoicing Detailed Technical Guideline V2 p.57 (Openssl commands); corroborated p.60, p.63',
 'https://zatca.gov.sa', DATE '2026-08-19', false,
 'VERIFIED from ZATCA''s own worked example, which prints the commands: '
 '"openssl ecparam -name secp256k1 -genkey -noout -out PrivateKey.pem", '
 '"openssl ec -in PrivateKey.pem -pubout -conv_form compressed -out PublicKey.pem" and '
 '"openssl req -new -sha256 -key privateKey.pem -extensions v3_req -config config.cnf". '
 'The curve is secp256k1 — the Bitcoin curve — and NOT the NIST P-256 (prime256v1) '
 'that almost every crypto library reaches for by default. Two independent '
 'corroborations in the same document: the QR example on p.60 carries the public key '
 'as OID 1.2.840.10045.2.1 followed by 1.3.132.0.10, which is secp256k1; and p.63 '
 'instructs the reader to copy "Signature Algorithm: ecdsa-with-SHA256" out of the '
 'issued PCSID. Note for whoever implements this: the Go standard library does not '
 'implement secp256k1, so the key cannot be generated with crypto/elliptic. The '
 'signing terminal is the Tauri POS (architecture decision E1.3, LOCKED), and Rust '
 'has the curve, which is consistent — but it is a dependency decision, not an '
 'accident, and it belongs on the terminal because the key may not be exported.'),

-- Getting this wrong onboards the unit into the wrong environment.
('SA.ZATCA.CSR_CERTIFICATE_TEMPLATE', 'sa',
 '{"config_key": "certificateTemplateName",
   "production": "ASN1:PRINTABLESTRING:ZATCA-Code-Signing",
   "simulation": "ASN1:PRINTABLESTRING:PREZATCA-Code-Signing"}'::jsonb,
 '2023-05-01', 'zatca',
 'FATOORA Portal User Manual V3 §3.B p.31',
 'https://zatca.gov.sa', DATE '2026-08-19', false,
 'VERIFIED, and quoted exactly: the manual states "For FATOORA Portal ... '
 'certificateTemplateName = ASN1:PRINTABLESTRING:ZATCA-Code-Signing" and "For '
 'FATOORA Simulation Portal ... certificateTemplateName = '
 'ASN1:PRINTABLESTRING:PREZATCA-Code-Signing". SCOPE: production and simulation '
 'only. The value for the Developer Portal Integration Sandbox is not published in '
 'any of the four documents and must not be inferred from these two. Fatoora and '
 'Fatoora Simulation are stated to be independent environments with independent '
 'onboarding, so a unit onboarded with the wrong template name is onboarded into '
 'the wrong environment rather than failing loudly.'),

-- Endpoints and auth are published; the shape of the request is not (see below).
('SA.ZATCA.ONBOARDING_ENDPOINTS', 'sa',
 '{"auth_scheme": "Basic base64(CSID:secret)",
   "version_header": "accept-version: v2",
   "core": {
     "compliance_csid": "https://gw-fatoora.zatca.gov.sa/e-invoicing/core/compliance",
     "compliance_invoices": "https://gw-fatoora.zatca.gov.sa/e-invoicing/core/compliance/invoices",
     "production_csid": "https://gw-fatoora.zatca.gov.sa/e-invoicing/core/production/csids"},
   "simulation": {
     "compliance_csid": "https://gw-fatoora.zatca.gov.sa/e-invoicing/simulation/compliance",
     "compliance_invoices": "https://gw-fatoora.zatca.gov.sa/e-invoicing/simulation/compliance/invoices",
     "production_csid": "https://gw-fatoora.zatca.gov.sa/e-invoicing/simulation/production/csids"},
   "compliance_checks_precede_production_csid": true}'::jsonb,
 '2023-05-01', 'zatca',
 'FATOORA Portal User Manual V3 §3.A-3.B pp.30-31; Developer Portal Manual V3 §3 p.68',
 'https://zatca.gov.sa', DATE '2026-08-19', false,
 'VERIFIED. The three onboarding URLs are listed verbatim for both environments, '
 'and the Developer Portal Manual states the scheme: "ZATCA uses Basic '
 'Authentication ... {Base64 Encoded String} = a script containing the CSID, a '
 'Colon and the Secret encoded with Base64 (CSID:Secret)", plus "An additional '
 'accept-version: v2 header must be added to V2 API calls". The Compliance CSID '
 'call returns binarySecurityToken and secret, which are then the username and '
 'password for the compliance-check and Production CSID calls. Two things to carry '
 'forward: issuance and renewal of a Production CSID share one URL and the '
 'wire-level discriminator between them is NOT published; and the compliance checks '
 'must pass before a Production CSID is issued, the request returning "an invalid '
 'response until these compliance checks are completed". The Integration Sandbox '
 'API base URL is not published in these documents either.'),

-- Onboarding cannot be automated end to end, and that shapes the UI.
('SA.ZATCA.ONBOARDING_OTP', 'sa',
 '{"digits": 6,
   "numeric_only": true,
   "validity_minutes": 60,
   "issued_by": "fatoora_portal_only",
   "api_available": false,
   "max_per_request": 100,
   "bound_to": "vat_registration_number"}'::jsonb,
 '2022-11-01', 'zatca',
 'E-invoicing Detailed Technical Guideline V2 §3.3.2 pp.21-25, §3.3.3 p.30, FAQ pp.76-77',
 'https://zatca.gov.sa', DATE '2026-08-19', false,
 'VERIFIED. The portal generates codes "valid for 1 hour", up to 100 per request, '
 'and the failure list for a CSR submission begins "Invalid OTP/OTC (not exactly '
 'six digits, not numeric)" — which is where the six-digit numeric format comes '
 'from. The FAQ is explicit that "OTP generation is managed by the FATOORA portal '
 'and must be taken from the portal itself, no need for any API" and "There is no '
 'API for OTP". PRODUCT CONSEQUENCE: onboarding can never be a background job. A '
 'human logs into the Fatoora portal, reads a code, and types it into the EGS unit '
 'within the hour, and the code is checked against the VAT number it was issued '
 'for. Whatever screen we build has to accept a typed code and use it immediately; '
 'it must not be stored, because it is worthless in an hour and is a credential '
 'until then.');

-- ---------------------------------------------------------------------------
-- The two gaps. Both are unverified by construction: every payload value is a
-- __VERIFY__ placeholder, and neither carries verified_on.
-- ---------------------------------------------------------------------------

INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from,
   source_authority, source_document, source_url, release_blocker, notes)
VALUES

('SA.ZATCA.CSR_SUBJECT_LAYOUT', 'sa',
 '{"subject_dn_attributes": "__VERIFY__",
   "subject_alternative_name": "__VERIFY__",
   "certificate_template_oid": "__VERIFY__"}'::jsonb,
 '2024-01-01', 'zatca',
 'Official Fatoora SDK CSR configuration template (config.cnf)',
 'https://zatca.gov.sa', true,
 'RELEASE BLOCKER, and the one thing standing between us and a real CSR. '
 'SA.ZATCA.CSR_FIELDS verified WHAT goes into a CSR — the nine inputs and their '
 'formats. This rule is HOW they are laid out, and that is not published in any of '
 'the four guideline PDFs. Specifically unknown: which X.509 subject DN attribute '
 'carries each of the nine inputs, which of them instead live in subjectAltName, '
 'and the OID under which certificateTemplateName is carried as an extension. '
 'p.57 of the Technical Guideline invokes "-config config.cnf" and never prints '
 'the file; the Developer Portal Manual has a heading "Sample contents of the CSR" '
 'followed by a screenshot rather than text. The values circulating in vendor blogs '
 'and community repositories are Tier 2 at best, and blueprint Part N forbids '
 'implementing a compliance feature from Tier 2 alone. Read it from the official '
 'Fatoora SDK, which ships the configuration template. Until then a CSR cannot be '
 'built: a wrong distinguished name is rejected at onboarding, so this fails on a '
 'taxpayer''s first attempt rather than in our tests.'),

('SA.ZATCA.ONBOARDING_REQUEST_FORMAT', 'sa',
 '{"http_methods": "__VERIFY__",
   "otp_header_name": "__VERIFY__",
   "csr_body_field": "__VERIFY__",
   "csr_encoding": "__VERIFY__",
   "compliance_request_id_field": "__VERIFY__",
   "response_schema": "__VERIFY__",
   "status_codes": "__VERIFY__"}'::jsonb,
 '2024-01-01', 'zatca',
 'CSID API Swagger files, ZATCA Developer Portal (Integration Sandbox)',
 'https://zatca.gov.sa', true,
 'RELEASE BLOCKER. SA.ZATCA.ONBOARDING_ENDPOINTS verified where to send an '
 'onboarding request and how to authenticate it. It did not establish what the '
 'request looks like, and the difference matters because every unknown here is a '
 'silent 400. Not published: the HTTP verb for any of the three onboarding '
 'endpoints (POST is stated only for reporting and clearance); the header name that '
 'carries the OTP, where the Technical Guideline confirms only that "the OTP is '
 'provided in the API header"; the JSON field holding the CSR; whether the CSR '
 'travels as base64 or as PEM, and whether the BEGIN/END lines are kept — base64 is '
 'stated for invoice payloads and never for the CSR, so it must not be carried '
 'over; the field holding the compliance request ID; and the response schema and '
 'status codes for the onboarding calls, where only binarySecurityToken and secret '
 'are named. The Developer Portal Manual defers all of it: "Please refer to the '
 'CSID API Swagger files for more details", reachable only from the Integration '
 'Sandbox page. Obtain the Swagger files before writing the client.');

-- 0044 established the habit of saying what a verified rule does NOT cover.
-- SA.ZATCA.CSR_FIELDS covers the inputs; it never covered their encoding, and a
-- reader who found "VERIFIED with exact formats" could reasonably have assumed
-- it did. It also has a genuine conflict between two official documents.
UPDATE regulatory_rule SET
  notes = notes || ' SCOPE: the nine inputs and their formats only. How they map '
    'onto the X.509 subject and extensions is a separate, unverified rule — '
    'SA.ZATCA.CSR_SUBJECT_LAYOUT. CONFLICT FOUND 2026-08-19: Technical Guideline '
    'V2 p.28 maps the functionality-map digits to "TSXY" and says X and Y are '
    'reserved and set to 0, while Developer Portal Manual V3 p.90 maps the same '
    'four digits to "TSCZ" — buyer QR code and seller QR code in self-billing. '
    'Both are Nov 2022. The three values we act on (1000, 0100, 1100) are '
    'identical under either reading, so the conflict does not block onboarding, '
    'but it must not be resolved by guessing if a third or fourth digit is ever '
    'set.'
WHERE rule_key = 'SA.ZATCA.CSR_FIELDS'
  AND notes NOT LIKE '%SA.ZATCA.CSR_SUBJECT_LAYOUT%';
