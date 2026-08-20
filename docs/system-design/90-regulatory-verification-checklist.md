# 90 — Regulatory Verification Checklist

**For:** Mahedi Hasan Emon (founder) + Saudi tax advisor + legal counsel
**Not for:** developers to fill in

> Blueprint's instruction, verbatim: ***"Do not let developers fill these in from assumption."***
> Part N process rule: *"When a Tier 1 source contradicts this document, the Tier 1 source wins and this document is amended — with the change dated and noted."*

Every item below becomes a row in the Regulatory Rule Registry (`05-regulatory-rule-registry.md`). Until an item is verified, its rule carries `verified_on = NULL`. Items marked 🚫 **block release** of the phase named.

---

## How to use this

1. Open the **Tier 1 source** named in the row — the official regulator document, not a summary, not a consulting blog, not the blueprint.
2. Record the actual value, the document name and version/date, and the date you checked.
3. Enter it in Super Admin → Regulatory Registry, which stamps `verified_by` and `verified_on`.
4. If the official value **differs** from the blueprint's assumption, the official value wins — and note the discrepancy so the blueprint can be amended.

Re-verification cadence: **6 months** for tax and payroll rules, **12 months** for others.

---

## A. Pre-release blockers (E8.4)

The three most volatile items. Each blocks a phase from shipping.

| # | Item | Registry key | Tier 1 source | Blocks | Why it's dangerous |
|---|---|---|---|---|---|
| A1 | 🚫 **ZATCA XML / QR schema version** | `SA.ZATCA.QR_TAG_VALUE_ENCODING`, `SA.ZATCA.UBL_FIELD_SET`, `SA.ZATCA.XML_CANONICALIZATION` | ZATCA **XML Implementation Standard** + **Security Features Implementation Standard** | **Phase 1** | Must match the schema ZATCA currently accepts. Wrong bytes = invoices rejected at scale. The system records which version each archived invoice was signed under |

A1 was originally one row against `SA.ZATCA.XML_SCHEMA_VERSION` and `SA.ZATCA.HASH_ALGORITHM`. Migration 0012 verified part of each — that submission is XML only under standard v1.2, and that the hash is SHA-256 with half-up rounding — and in doing so stamped `verified_on` on both, which removed them from `registry.Health`'s blocking list along with the parts nobody had read. Migration 0044 split the unanswered halves back out under their own keys, which are the ones named above. The two partly-verified rules stay verified for what they actually establish.
| A2 | 🚫 **Mudad wage-file format** | `SA.WPS.WAGE_FILE_FORMAT_SPEC` | Mudad live specification (`mudad.com.sa`) | **Phase 3** | *"File layouts change without publicity."* Pull immediately before build **and re-check before every payroll release** |
| A3 | 🚫 **GOSI rate schedule** | `SA.GOSI.RATES` | GOSI current schedule (`gosi.gov.sa`) | **Phase 3** | On a legislated upward path. Must be a **complete dated schedule through 2028**, not one current figure |

---

## B. The 12 Part N claims

Blueprint Part N lists these as *"the specific operational assertions most likely to have changed, or to be stated imprecisely here."*

| # | Claim as stated in the blueprint | Verify against | Impact if wrong |
|---|---|---|---|
| B1 | 🚫 Simplified invoices reported within **24 hours**; Standard invoices **cleared before delivery** | ZATCA E-Invoicing Detailed Guideline | **Drives the entire offline state machine.** Phase 1 blocker |
| B2 | 🚫 Exact **XML field set, hash construction, and TLV QR byte layout** | ZATCA XML + Security Features Standards | **Wrong bytes = rejected invoices at scale.** Phase 1 blocker |
| B3 | Current **wave thresholds and deadlines** (Wave 24: revenue > SAR 375,000 in 2022/23/24, deadline 30 June 2026) | ZATCA roll-out page | Determines which clients are already obligated |
| B4 | VAT standard rate **15%**; registration thresholds **SAR 375,000 / 187,500** | ZATCA VAT Implementing Regulations | Configurable, but the default must be right |
| B5 | Monthly filing **above SAR 40M**, quarterly below; due **last day of the following month** | ZATCA VAT guidance | Drives filing reminders and period logic |
| B6 | Record retention **≥ 6 years**, up to **~11 years** for certain assets | ZATCA VAT Implementing Regulations | Drives archive design and legal-hold logic |
| B7 | 🚫 **Mudad wage-file format, submission timing, payment window** | Mudad current specification | Blueprint itself says these are *"directionally correct"* only |
| B8 | 🚫 **GOSI rates** + the **pre/post-July-2024 hire distinction** | GOSI current schedule | Must be a dated table, never a fixed number |
| B9 | PDPL **breach notification (72 h)** and **DSR response (30 days, extendable 30)** | SDAIA Implementing Regulation | Drives enforced workflow SLAs |
| B10 | **Cross-border transfer conditions and required safeguards** | SDAIA Transfer Outside the Kingdom Regulation | **Determines hosting architecture.** Blocks a second data region |
| B11 | **E-commerce registration channel** — Maroof vs business.sa — and mandatory disclosures | Ministry of Commerce (`mc.gov.sa`, `business.sa`) | **Actively transitioning.** Treated as a configurable per-tenant field |
| B12 | **Which gateways are currently SAMA-licensed** | SAMA licensed PSP list (`sama.gov.sa`) | Integrating an unlicensed provider is a real commercial risk |

---

## C. Values shipping as unverified placeholders

Every one of these currently sits in the registry with `verified_on = NULL`.

### VAT
- [ ] Standard rate — blueprint says **15%**
- [ ] Mandatory registration threshold — **SAR 375,000**
- [ ] Voluntary registration threshold — **SAR 187,500**
- [ ] Monthly-vs-quarterly filing threshold — **SAR 40,000,000**
- [ ] Filing due-date rule — **last day of month following period end**
- [ ] Retention period — **6 years**, extended (**~11**) for defined assets
- [ ] The **7 tax treatments** — Standard, Zero-rated, Exempt, Out-of-scope, Export, Reverse-charge, Import
- [ ] Exemption **reason codes** required on invoices for non-standard treatment
- [ ] **Input VAT recoverability** restrictions — entertainment, certain vehicles, fuel

### ZATCA

A desk verification on **2026-08-15** closed several of these; see §F for what was
read and by whom. The boxes below are ticked only where the registry rule now
carries a `verified_on` AND the finding covers the whole of the item.

- [x] Submission format and standard version — XML only, v1.2; PDF/A-3 is not accepted for submission (`SA.ZATCA.XML_SCHEMA_VERSION`)
- [ ] 🚫 Mandatory UBL 2.1 field set, cardinality and business rules (`SA.ZATCA.UBL_FIELD_SET`)
- [x] QR TLV field set and byte layout — nine tags, one byte of tag, one byte of length counting UTF-8 **bytes**, no separators, whole stream base64 to at most 700 characters (`SA.ZATCA.QR_TLV_FIELDS`)
- [ ] 🚫 How tags 6 to 9 encode their values — the standard answers this two different ways (`SA.ZATCA.QR_TAG_VALUE_ENCODING`)
- [x] Hash algorithm and rounding — SHA-256, half-up on the third decimal (`SA.ZATCA.HASH_ALGORITHM`)
- [ ] 🚫 Canonicalization method applied before hashing (`SA.ZATCA.XML_CANONICALIZATION`)
- [x] Simplified reporting window — **24 hours** (`SA.ZATCA.REPORTING_WINDOW_HOURS`)
- [x] **Whether any offline tolerance exists for Standard invoices** — there is an official extended-outage route: an *uncleared* invoice, with three obligations attached (`SA.ZATCA.STANDARD_OFFLINE_TOLERANCE`, schema change in 0013)
- [x] CSID renewal interval — no fixed term is published; the certificate's own `NotAfter` governs, capped at 60 months (`SA.ZATCA.CSID_RENEWAL_DAYS`)
- [ ] Current wave definitions and deadlines

Onboarding was read on **2026-08-19** from the four official PDFs; migration 0045
records the result. The split is deliberate: what a CSR is made of is verified,
how it is laid out and sent is not.

- [x] CSR key parameters — EC on **secp256k1**, CSR signed `ecdsa-with-SHA256`, public key in **compressed** point form, extensions from `v3_req` (`SA.ZATCA.CSR_KEY_PARAMETERS`)
- [x] Certificate template name — `ZATCA-Code-Signing` for production, `PREZATCA-Code-Signing` for simulation; the sandbox value is unpublished (`SA.ZATCA.CSR_CERTIFICATE_TEMPLATE`)
- [x] Onboarding endpoints and authentication — three URLs per environment, `Authorization: Basic base64(CSID:Secret)`, `accept-version: v2` (`SA.ZATCA.ONBOARDING_ENDPOINTS`)
- [x] OTP — exactly six numeric digits, valid one hour, portal-only with no API, up to 100 per request, bound to the VAT number (`SA.ZATCA.ONBOARDING_OTP`)
- [ ] 🚫 CSR subject layout — which X.509 attribute carries each of the nine inputs, the SAN entries, and the OID carrying `certificateTemplateName` (`SA.ZATCA.CSR_SUBJECT_LAYOUT`)
- [ ] 🚫 Onboarding request format — HTTP verbs, the OTP header name, the CSR body field and its encoding, the compliance-request-ID field, response schema and status codes (`SA.ZATCA.ONBOARDING_REQUEST_FORMAT`)

### PDPL
- [ ] DSR response deadline — **30 days, extendable by a further 30**
- [ ] Breach notification to SDAIA — **72 hours from becoming aware**
- [ ] Required breach notification **content** fields
- [ ] Retention defaults per data category
- [ ] 🚫 Cross-border transfer conditions (blocks second region)
- [ ] Whether tenants must register as data controllers with SDAIA, and the reference format

### Payroll & Labour
- [ ] 🚫 Mudad wage-file format specification and version
- [ ] 🚫 GOSI complete dated rate schedule through 2028, split Saudi/expat and pre/post-July-2024
- [ ] GOSI contribution wage cap
- [ ] WPS submission lead time before payday
- [ ] Salary payment window
- [ ] Required employee identifier fields for the wage file
- [ ] EOSB accrual formula
- [ ] Leave entitlements and overtime multipliers

### E-Commerce
- [ ] Current registration channel — Maroof or business.sa
- [ ] Required storefront disclosure fields
- [ ] Cooling-off period — **14 days** — and category exemptions

### Payments
- [ ] Current SAMA-licensed PSP list — confirm each planned adapter (HyperPay, Moyasar, PayTabs, Tap, Geidea, Amazon Payment Services, Checkout.com, Tabby, Tamara) is licensed **at integration time**

### Withholding Tax
- [ ] Rates by payment type and recipient status

---

## D. Professional review — required before go-live

Blueprint Part M item 10 makes these launch gates, not optional.

| # | Review | Reviewer | Covers |
|---|---|---|---|
| D1 | **VAT / ZATCA implementation** | Saudi tax advisor | Tax treatment logic, VAT return mapping, invoice types, retention, the pre-filing 4-way reconciliation |
| D2 | **PDPL posture** | Legal counsel | Consent model, DSR workflow, retention vs legal hold conflict, breach procedure, data classification |
| D3 | **Tenant data processing agreement** | Legal counsel | The platform operates as a **data processor** for every tenant and needs its own PDPL posture: DPAs, documented controls, sub-processor records for every cloud/SMS/email vendor |
| D4 | **Cross-border transfer strategy** | Legal counsel | Only if a non-Saudi region is planned |

---

## E. Standing wording audit

Applies to code, UI strings, documentation, sales material, and this repository. CI lints for the banned phrases.

| ❌ Never | ✅ Always |
|---|---|
| ~~"ZATCA-certified"~~ | "supports ZATCA requirements" |
| ~~"certified compliant"~~ | "built to support ZATCA and PDPL requirements" |
| ~~"guaranteed compliant"~~ | "WPS/Mudad-ready" |
| ~~"never at legal risk"~~ | — |

Three specific framings the blueprint mandates:

1. Passing **ZATCA SDK validation is a technical self-check, not ZATCA approval.**
2. For WPS: the software **prepares** compliant wage data; **final legal responsibility for submission remains the employer's.**
3. A tenant's ZATCA obligation status comes **only from that taxpayer's own ZATCA notification** — never asserted by the software, never assumed by sales.

---

## F. Sign-off log

| Date | Item(s) | Verified by | Source document + version | Notes / discrepancies found |
|---|---|---|---|---|
| 2026-08-15 | ZATCA submission format and standard version; hash algorithm and rounding; CSID validity; simplified reporting window; standard-invoice offline route; VAT record retention; mandatory registration threshold | Founder desk verification. **Not** a tax advisor or legal counsel — §D remains open | XML Implementation Standard v1.2 (2023-05-19) §7.3; Detailed Guideline V2 §4.1.2(b), §4.2.2(c), §6.5, §10; Technical Guideline V2 §3.2.2, §3.3.3, §3.5, §4.3; Security Features Implementation Standard v1.2 (2023-05-19) §2.2.2 | Six design assumptions were wrong and were corrected: PDF/A-3 is not accepted for submission; the CSID belongs to an EGS unit and not to a device (schema change, 0013); there is an official uncleared-invoice route for an extended B2B outage (0013); no fixed CSID term is published; unit price carries no decimal limit while line totals do; the CSR requires nine specific fields. Recorded in migration 0012 and in `ZATCA E-Invoicing Phase 2.md`. The primary PDFs are **not** archived in this repository, so the citations are re-checkable only against ZATCA's published copies |
| 2026-08-19 | Correction to the above, not a new verification | Founder | — | 0012 stamped `verified_on` on `SA.ZATCA.XML_SCHEMA_VERSION` and `SA.ZATCA.HASH_ALGORITHM` while answering only part of each, which silently removed both from `registry.Health`'s blocking list. Migration 0044 split the unanswered halves into `SA.ZATCA.UBL_FIELD_SET` and `SA.ZATCA.XML_CANONICALIZATION`, both unverified blockers. Nothing was verified on this date |
| 2026-08-19 | QR TLV field set and byte layout | Founder desk verification. **Not** a tax advisor or legal counsel — §D remains open | Technical Guideline V2 §6 pp.58-64, including both worked payloads | The nine tags, the one-byte tag and length, the "no padding or separators", the 700-character base64 ceiling, and that the length counts **bytes** of the UTF-8 value rather than characters — §6.4 names Arabic text as the common mistake here. Both published payloads are now golden tests and are reproduced byte for byte. Two problems found in the source and recorded rather than smoothed over: the length table on p.60 disagrees with its own payload on five of nine tags (6/45/192/48/144 against 7/5/96/88/72) and states tag 5 as `114.90` where the payload says `144.9`; and §6.1 says a value is the UTF-8 encoding of the field value while the payload carries tags 8 and 9 as raw DER, which is not UTF-8. The second is now `SA.ZATCA.QR_TAG_VALUE_ENCODING`, an open blocker |
| 2026-08-19 | CSR key parameters; certificate template names; onboarding endpoints and authentication; onboarding OTP | Founder desk verification. **Not** a tax advisor or legal counsel — §D remains open | Technical Guideline V2 (Nov 2022) p.57 for the OpenSSL commands, pp.26–29 for the CSR inputs, pp.21–25 and p.30 and pp.76–77 for the OTP, p.60 and p.63 for corroboration; FATOORA Portal User Manual V3 §3.A–3.B pp.30–31; Developer Portal Manual V3 §3 p.68 | The curve is **secp256k1**, not the NIST P-256 that most libraries default to, corroborated twice inside the same document — the QR worked example on p.60 carries OID `1.3.132.0.10`, and p.63 reads `ecdsa-with-SHA256` off the issued PCSID. Consequence: Go's standard library cannot generate this key. Two gaps opened as blockers in 0045 rather than guessed: the CSR **subject layout** (the guideline invokes `-config config.cnf` and never prints the file; the Developer Portal Manual shows the sample CSR as a screenshot) and the onboarding **request format** (verbs, OTP header name, body field names and CSR encoding, all deferred to Swagger files reachable only from the Integration Sandbox). One conflict between two official documents: Technical Guideline p.28 maps the functionality-map digits to `TSXY`, Developer Portal Manual p.90 maps them to `TSCZ`; the three values we act on are identical under either reading. The four primary PDFs are **not** archived in this repository |

Keep this table filled in. It is the evidence that verification actually happened — useful in an audit, and the only way to know what is safe to rely on.

A row here is not professional sign-off. §D is a separate gate and no row in this table closes it: reading a standard establishes what it says, and a Saudi tax advisor accepting responsibility for the reading is a different act.
