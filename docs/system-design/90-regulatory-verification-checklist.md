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
| A1 | 🚫 **ZATCA XML / QR schema version** | `SA.ZATCA.XML_SCHEMA_VERSION`, `SA.ZATCA.QR_TLV_FIELDS` | ZATCA **XML Implementation Standard** + **Security Features Implementation Standard** | **Phase 1** | Must match the schema ZATCA currently accepts. Wrong bytes = invoices rejected at scale. The system records which version each archived invoice was signed under |
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
- [ ] 🚫 XML schema version currently accepted
- [ ] 🚫 QR TLV field set and byte layout
- [ ] 🚫 Hash algorithm and canonicalization method
- [ ] Simplified reporting window — **24 hours**
- [ ] **Whether any offline tolerance exists for Standard invoices** — blueprint E1.3 line 855 flags this as explicitly unverified and it constrains the B2B offline policy
- [ ] CSID renewal interval
- [ ] Current wave definitions and deadlines

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
| | | | | |

Keep this table filled in. It is the evidence that verification actually happened — useful in an audit, and the only way to know what is safe to rely on.
