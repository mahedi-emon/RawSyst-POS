# ZATCA CSR / CSID onboarding — what is verified and what is not

Desk verification 2026-08-19, recorded in migration `0045_zatca_onboarding_rules.sql`.
Sources were the four official PDFs downloaded from zatca.gov.sa:

- E-invoicing Detailed Technical Guideline, **V2, Nov 2022** (81 pp.) — "TG"
- FATOORA Portal User Manual, **V3** (32 pp.) — "FPM"
- Developer Portal Manual, **V3** (96 pp.) — "DPM"
- E-Invoicing Detailed Guidelines, **V2, May 2023** (66 pp.) — "DG"

URLs (all under `https://zatca.gov.sa/en/E-Invoicing/Introduction/Guidelines/Documents/`):
`E-invoicing-Detailed-Technical-Guideline.pdf`, `Fatoora_Portal_User_Manual_English.pdf`,
`DEVELOPER-PORTAL-MANUAL.pdf`, `E-Invoicing_Detailed__Guideline.pdf`.
They are **not** archived in the repo. Index page: `/en/E-Invoicing/Introduction/Guidelines/Pages/default.aspx`.

## VERIFIED — safe to build on

**Cryptography** (`SA.ZATCA.CSR_KEY_PARAMETERS`). TG p.57 prints ZATCA's own commands:

    openssl ecparam -name secp256k1 -genkey -noout -out PrivateKey.pem
    openssl ec -in PrivateKey.pem -pubout -conv_form compressed -out PublicKey.pem
    openssl req -new -sha256 -key privateKey.pem -extensions v3_req -config config.cnf -out taxpayer.csr

The curve is **secp256k1**, NOT NIST P-256. Corroborated twice in the same document:
the QR worked example on p.60 carries the public key as OID `1.2.840.10045.2.1`
(id-ecPublicKey) followed by `1.3.132.0.10` (secp256k1); p.63 instructs reading
`Signature Algorithm: ecdsa-with-SHA256` off the issued PCSID.

**Consequence: Go's standard library has no secp256k1**, so `crypto/elliptic` cannot
generate this key. That is consistent with decision E1.3 (LOCKED): signing is LOCAL on
the Tauri POS terminal, and Rust has the curve. ZATCA forbids exporting the private
stamping key (DG §5.4, §6.5), so the key must be generated where it will be used.

**Certificate template** (`SA.ZATCA.CSR_CERTIFICATE_TEMPLATE`), FPM p.31, verbatim:
- production: `certificateTemplateName = ASN1:PRINTABLESTRING:ZATCA-Code-Signing`
- simulation: `certificateTemplateName = ASN1:PRINTABLESTRING:PREZATCA-Code-Signing`
- sandbox value is **not published** — do not infer it from these two.

**Endpoints** (`SA.ZATCA.ONBOARDING_ENDPOINTS`), FPM pp.30-31. Swap `core` for
`simulation` for the simulation environment:
- `https://gw-fatoora.zatca.gov.sa/e-invoicing/core/compliance` — Compliance CSID from a CSR
- `.../core/compliance/invoices` — compliance checks
- `.../core/production/csids` — request **or renew** a Production CSID (one URL for both)

Auth (DPM p.68): `Authorization: Basic base64(CSID:Secret)` plus `accept-version: v2`.
The Compliance CSID response yields `binarySecurityToken` and `secret`, which become the
username and password for the later calls. Compliance checks must pass before a
Production CSID is issued; the call returns an invalid response until they do.

**OTP** (`SA.ZATCA.ONBOARDING_OTP`), TG pp.21-25, p.30, pp.76-77: exactly **six numeric
digits**, valid **1 hour**, up to **100 per request**, generated only on the Fatoora
portal — "There is no API for OTP" — and checked against the VAT number it was issued
for. So onboarding can never be a background job: a human reads a code and types it in.
Do not store it.

**The nine CSR inputs** were already verified in 0012 (`SA.ZATCA.CSR_FIELDS`) and the
official text at TG pp.26-29 matches what is recorded there, including the VAT-group
rule (11th digit of the organization identifier = 1 means the organization unit must be
the member's 10-digit TIN).

## UNVERIFIED — release blockers, do not guess

`SA.ZATCA.CSR_SUBJECT_LAYOUT` — how the nine inputs map onto X.509: which subject DN
attribute carries each, which live in subjectAltName, and the OID carrying
`certificateTemplateName`. TG p.57 invokes `-config config.cnf` and never prints the
file; DPM p.94 shows "Sample contents of the CSR" as a screenshot. **Get the official
Fatoora SDK**, which ships the configuration template.

`SA.ZATCA.ONBOARDING_REQUEST_FORMAT` — the HTTP verb for any of the three onboarding
endpoints (POST is stated only for reporting/clearance); the header name carrying the
OTP (TG p.75 says only that it travels "in the API header"); the JSON field holding the
CSR; whether the CSR is base64 or PEM and whether BEGIN/END lines are kept (base64 is
stated for invoice payloads only — do not carry it over); the compliance-request-ID
field; the onboarding response schema and status codes. DPM defers all of it: "Please
refer to the CSID API Swagger files for more details", reachable only from the
Integration Sandbox page. **Get the Swagger files.**

## Conflicts and traps found

- **Functionality map digits**: TG p.28 says `TSXY` with X and Y reserved and set to 0;
  DPM p.90 says `TSCZ` (buyer QR, seller QR in self-billing). Both Nov 2022. The three
  values we act on — 1000, 0100, 1100 — are identical either way.
- FPM p.30 calls the `/compliance` artefact a "pre-compliance CSID"; TG and DPM call it
  a "Compliance CSID". Same thing.
- Fatoora and Fatoora Simulation are independent environments with independent
  onboarding; a test CSID cannot be used against the core solution.
- Newly VAT-registered taxpayers must wait **2 business days** before onboarding (TG p.70).
- Sandbox skips the compliance checks entirely (DPM p.79), so passing there proves less
  than it appears to.

## Also now available (not yet acted on)

TG §6, pp.58-64 contains the **full QR TLV specification** — tags 1-9, one-byte tag and
length, UTF-8 values, base64 of the TLV stream, worked hex examples and the XPaths for
each tag. `SA.ZATCA.QR_TLV_FIELDS` is still an unverified release blocker and could be
closed from this section. It was deliberately left alone because QR was out of scope.
