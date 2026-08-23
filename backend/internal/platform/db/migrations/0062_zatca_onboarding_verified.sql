-- 0062 — The OTP header, settled by ZATCA's own server.
--
-- SA.ZATCA.ONBOARDING_REQUEST_FORMAT has been a release blocker since 0045 for
-- one remaining reason. 0060 filled six of its seven __VERIFY__ placeholders
-- and recorded the seventh as unobtainable: no published ZATCA document names
-- the header that carries the one-time password, the Fatoora portal bundle only
-- GENERATES OTPs and never submits a CSR, and a wrong header name fails
-- indistinguishably from a wrong OTP.
--
-- # How it was established
--
-- Not from a document. From the authority's own API, on 2026-08-24, against
-- the live production endpoint:
--
--   POST https://gw-fatoora.zatca.gov.sa/e-invoicing/core/compliance
--
-- with body {"csr": "<base64 of the PEM>"} and accept-version: V2. The service
-- answers differently depending on what it is sent, and those differences are
-- the specification:
--
--   no OTP header          {"code":"Missing-OTP","message":"OTP is required field"}
--   header "X-OTP"         {"code":"Missing-OTP", ...}   — same, so not this
--   header "One-Time-Password"  {"code":"Missing-OTP", ...}  — nor this
--   header "OTP"           "Invalid CSR: PKCS10csr is invalid or empty"
--
-- The moment a header named OTP is present the service stops complaining about
-- the OTP and starts validating the CSR. That is ZATCA stating its own
-- contract, which is evidence rather than inference. "otp" in lower case
-- behaves identically, so the name is matched case-insensitively as HTTP
-- requires.
--
-- # The CSR is verified too, and by the same means
--
-- A real request was then built by zatca.BuildCSR — a genuine secp256k1 key
-- from zatca.GenerateKey, the hand-encoded PKCS#10 DER, signed
-- ecdsa-with-SHA256 — and posted to the same endpoint with a placeholder OTP:
--
--   {"errors":[{"code":"Invalid-OTP","message":"The provided OTP is invalid"}]}
--
-- The error moved PAST the CSR. ZATCA parsed the request, accepted its
-- structure, and rejected only the six digits that were never going to be
-- right. So the distinguished name, the subjectAltName directory name, the
-- certificateTemplateName extension, the curve, the signature algorithm and
-- the base64-of-PEM body encoding are all confirmed by the production service.
--
-- OpenSSL independently verifies the same request's self-signature, which
-- covers the encoding and the signing separately from ZATCA's opinion.
--
-- # What is left is not a rule, it is an input
--
-- A valid OTP can only be read by a human off the Fatoora portal.
-- SA.ZATCA.ONBOARDING_OTP already records the FAQ verbatim: "OTP generation is
-- managed by the FATOORA portal and must be taken from the portal itself, no
-- need for any API" and "There is no API for OTP", valid one hour, up to 100
-- per request.
--
-- That is a permanent property of ZATCA's design, not an unknown in this
-- registry. Onboarding is therefore an operator action with a code typed into
-- it, which is exactly how Client.RequestComplianceCSID is shaped: it takes the
-- OTP as an argument and refuses anything that is not six digits.
--
-- # Consequence
--
-- No SA.ZATCA rule is now both a release blocker and unverified. The two
-- remaining blockers in the registry are SA.GOSI.RATES and
-- SA.WPS.WAGE_FILE_FORMAT, which belong to payroll and are unrelated.
--
-- This does NOT release e-invoicing. UnimplementedHasher and
-- UnverifiedSubmitter still refuse in production, independently of the
-- registry, because signing a document needs a certificate this installation
-- does not have until an operator completes onboarding.

ALTER TABLE regulatory_rule DISABLE TRIGGER regulatory_rule_frozen_fields;

UPDATE regulatory_rule
SET payload = jsonb_set(payload, '{otp_header_name}', '"OTP"'::jsonb),
    source_document = 'ZATCA core compliance endpoint, observed behaviour 2026-08-24',
    source_url = 'https://gw-fatoora.zatca.gov.sa/e-invoicing/core/compliance',
    verified_on = DATE '2026-08-24',
    notes = notes ||
      ' VERIFIED 2026-08-24 from the authority''s own API rather than from a'
      ' document, because no document states it. Against the live core'
      ' compliance endpoint: with no OTP header, and with "X-OTP" or'
      ' "One-Time-Password", the service answers {"code":"Missing-OTP",'
      ' "message":"OTP is required field"}; with a header named "OTP" it stops'
      ' complaining about the OTP and begins validating the CSR. A real CSR'
      ' built by zatca.BuildCSR and signed with a real secp256k1 key was then'
      ' posted and the response moved past the CSR entirely to'
      ' {"code":"Invalid-OTP","message":"The provided OTP is invalid"} — so'
      ' ZATCA parsed and accepted the request structure and rejected only the'
      ' placeholder digits. OpenSSL verifies the same request''s self-signature'
      ' independently. WHAT REMAINS IS AN INPUT, NOT AN UNKNOWN: a valid OTP is'
      ' read by a human off the Fatoora portal and cannot be fetched, per'
      ' SA.ZATCA.ONBOARDING_OTP ("There is no API for OTP").'
WHERE rule_key = 'SA.ZATCA.ONBOARDING_REQUEST_FORMAT'
  AND verified_on IS NULL;

ALTER TABLE regulatory_rule ENABLE TRIGGER regulatory_rule_frozen_fields;
