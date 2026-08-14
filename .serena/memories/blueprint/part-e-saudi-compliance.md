# Blueprint Part E + N — Saudi Compliance (distilled)

Source: `RawSyst-POS-Blueprint-v2.4-FINAL.md` lines 734–1085 (Part E) + 1514–1560 (Part N). Read this instead of the doc. **Legally sensitive — accuracy matters.**

Part E must be built as a **dedicated compliance subsystem, not bolted onto POS logic at the end**.

---

## E1 — ZATCA Phase 2 E-Invoicing ("Fatoora")

### E1.0 Obligation model
Rollout in **waves by taxable revenue**. Wave 24: revenue > SAR 375,000 in 2022/2023/2024, integration deadline **30 June 2026** (same date penalty-waiver ended). **Obligation is per-taxpayer and ZATCA-notification-driven** — ZATCA notifies each taxpayer directly, generally ≥6 months ahead.

**The software must NEVER assume obligation status — capture it as configuration:**
```
Tenant Compliance Profile
├── VAT registered?          Yes/No
├── VAT registration number
├── ZATCA wave assigned      (as notified to the taxpayer)
├── Integration deadline     (as notified)
└── Onboarding status        Not started / Compliance CSID / Production CSID / Live
```
Mandated wording, verbatim: *"Saudi VAT-registered clients that fall within an applicable ZATCA Phase 2 wave must have the required integration capability in place by the deadline ZATCA notifies to them individually."*

### E1.1 Five distinct document types — model SEPARATELY, not variations of one "invoice"
| Document | Model |
|---|---|
| **Standard Tax Invoice (B2B)** | **Clearance** — cleared by ZATCA *before* given to buyer. Needs full buyer details incl. buyer VAT number |
| **Simplified Tax Invoice (B2C)** | **Reporting** — issued immediately, reported within **24 hours**. Main showroom POS flow |
| **Credit Note** | Follows type of invoice it corrects. **Must reference original** |
| **Debit Note** | Follows type of invoice it corrects. **Must reference original** |
| **Self-billing / third-party** | Configurable per tenant |

### E1.2 Technical requirements (mandatory, no exceptions)
1. **UBL 2.1 XML**, or PDF/A-3 with XML embedded. Plain PDFs/scans/word-processor docs explicitly non-compliant.
2. **UUID** — 128-bit, per invoice.
3. **ICV** — strictly sequential, **non-resetting**, **per device/EGS**. Makes deleted/missing invoices detectable.
4. **PIH** — each invoice embeds **SHA-256 hash of the previous one** ⇒ tamper-evident chain.
5. **Cryptographic Stamp** — **ECDSA** via **CSID**. Private keys in secure vault, never in code or plain `.env`.
6. **QR Code** — **Base64 TLV**: seller name, VAT number, timestamp, total with VAT, VAT amount + (Phase 2) invoice hash, cryptographic stamp, public key.
7. **CSID lifecycle** — compliance CSID → production CSID, renewal, revocation, handled in-system as ongoing process.
8. **Fatoora integration** — auth, submission (clearance or reporting), response parsing, status tracking, **retry queue**. A failed submission must NEVER be silently dropped — auto-retry + **critical alert**.
9. **Status pipeline:** `Draft → Generated → Signed/Stamped → Submitted → Cleared/Reported → Accepted`, with visible `Failed → Retry Queue → Retry`.
10. Multi-terminal/multi-branch: ZATCA supports centralized-server AND branch-based POS with **device-level CSID**.
11. **ZATCA SDK validation in dev+QA pipeline.** Passing SDK validation is a **technical self-check, NOT ZATCA approval**.

### E1.3 OFFLINE POS + ZATCA — **LOCKED BUSINESS RULES** (status: DECIDED, not an open question)

Locked because it determines DB schema, invoice state machine, and POS UI behaviour.

|  | **B2C Simplified** | **B2B Standard** |
|---|---|---|
| Model | Reporting (after issuance, within 24h) | Clearance (before handing to buyer) |
| Fully offline? | **Yes** | **No** — clearance needs connectivity |

**RULE 1 — B2C offline: PERMITTED, sale completes normally.**
```
Sale rung up offline
 → ICV assigned locally (sequential, non-resetting, per device)
 → PIH chained to previous local invoice
 → Cryptographically stamped LOCALLY using the device's CSID
 → QR generated LOCALLY (contains hash + stamp)
 → Receipt printed and given to customer immediately ✔ legally deliverable
 → state = SIGNED_PENDING_REPORT
 → On reconnect: reported to Fatoora → state = REPORTED
```
*"The customer never waits, and the sale never blocks."* Must be the **fastest path in the entire application**.
**HARD ARCHITECTURAL REQUIREMENT:** the device's **CSID private key AND the local ICV/PIH chain must exist on the terminal itself, not only in the cloud. Signing is a LOCAL operation.** Explicitly stated for the Tauri desktop POS.

**RULE 2 — B2B offline: CANNOT be delivered until cleared.** Never print/email a "final" B2B tax invoice while offline. Three tenant-selectable behaviours:
- **Option A — Block (DEFAULT, safest):** refuse to finalize. Cashier message verbatim: *"Standard tax invoice requires internet connection. Save as Draft, or issue as Simplified Invoice if the buyer does not require a VAT invoice."*
- **Option B — Draft & Hold (RECOMMENDED):** saved as DRAFT, **no ICV consumed, no stamp, no legal document**. Goods released only against a clearly-labelled **"Delivery Note — NOT A TAX INVOICE"**. On reconnect → generate → clear → then issue to buyer.
- **Option C — Convert to Simplified:** one-tap option when buyer doesn't need a Standard invoice.

**RULE 3 — Delivery timing:** B2C online/offline → full valid simplified invoice with QR **immediately**. B2B online → cleared Standard invoice immediately. B2B offline Option B → **Delivery Note only at counter**, cleared invoice after reconnection. B2B offline A/C → no B2B document issued.

**RULE 4 — Never break the counter chain.**
- ICV consumed **ONLY** when a legally-issued invoice is generated. **Drafts never consume an ICV.** A gap is exactly what ZATCA's tamper-detection looks for.
- A ZATCA-**rejected** invoice must **NOT be deleted and its ICV NOT reused**. It **stays in the chain**, flagged `REJECTED`, raises a **critical alert**, corrected via **Credit Note**.
- **Hash chain must be submitted in ICV order. Sync engine must preserve ordering per device, NOT submit in arrival order.**

**RULE 5 — Multi-device chain isolation.** Each terminal has its **own ICV sequence, own PIH chain, own CSID**. Chains **NEVER merged across devices**, **never re-sequenced by the cloud**. A new terminal starts at **ICV 1**.

**RULE 6 — Extended outage.** **No cap on offline B2C invoice count** — must survive multi-day outage. **Escalating alerts as unsubmitted queue ages: >12h notice, >24h warning, >72h critical** — to Owner AND Super Admin compliance watch.

⚠️ **Flagged unverified (line 855):** exact clearance/reporting timing and whether any offline tolerance exists for Standard invoices must be confirmed against ZATCA's current **E-Invoicing Detailed Guideline** and **Security Features Implementation Standard** before the state machine is finalized. If ZATCA differs, **ZATCA wins**. But the software must still implement **one explicitly decided rule per scenario, never undefined behaviour**.

### E1.4 Immutability
Once finalized, an invoice can **NEVER be edited or deleted by anyone — not even Owner or Super Admin**. Corrections only via linked Credit/Debit Notes. ZATCA prohibits tampering with e-invoices/logs and **prohibits running multiple/parallel invoice sequences**.

### E1.5 Bilingual
Full **Arabic + English with correct RTL** on receipts, invoices, customer-facing screens — a legal expectation and market necessity.

---

## E2 — Saudi Tax Engine

- **E2.1 Registration:** mandatory above **SAR 375,000** turnover; voluntary between **SAR 187,500 and SAR 375,000**. Onboarding wizard asks turnover and guides to correct status. **VAT number (TRN) validated + required for any VAT-registered tenant.**
- **E2.2 Rates:** standard **15%**, stored **configurable and centrally updatable, never hard-coded**. **7 tax treatments per product/line:** Standard-rated · Zero-rated · Exempt · Out-of-scope · Export · Reverse-charge · Import. **Exemption reason codes stored per transaction.** **VAT-inclusive pricing is the DEFAULT display mode** (Saudi retail practice) with engine back-calculating net + tax.
- **E2.3 Returns:** **> SAR 40 million annual taxable supplies ⇒ MONTHLY filing**, else **QUARTERLY** (switching filing period needs ZATCA approval). Due by **last day of the month following period end** — automatic countdown per tenant. **Nil returns producible even with zero transactions.** VAT Return Preparation Report maps directly onto the ZATCA form (output VAT, input VAT, taxable/zero-rated/exempt sales, imports, adjustments, net payable/refundable). **Pre-filing 4-way reconciliation: POS ↔ general ledger ↔ bank ↔ submitted Fatoora data** — mismatch between these four is the most common source of ZATCA compliance notices. **Input VAT recoverability flags** per expense head (entertainment, some vehicles, fuel restricted). VAT adjustments (bad debt relief, corrections, credit/debit note effects) as own tracked transaction type.
- **E2.4 Retention — architectural, not nice-to-have:** **≥6 YEARS**, up to ~**11 years for certain capital assets (real estate)**. Scope: tax invoices issued+received, simplified invoices, credit/debit notes, import/export & customs docs, accounting ledgers, bank statements. Under the Law of Commercial Books records are generally expected in **Arabic** and available for inspection.
  - Invoice **XML, PDF, and hash-chain data archived immutably** for the full period, never purged by routine cleanup.
  - **The retention engine must NEVER delete a record under tax retention obligation — "legal hold" overrides PDPL deletion.** This is a **direct conflict between PDPL deletion rights and tax retention duties** that the system must resolve deliberately: minimize/anonymize personal data where legally possible while preserving the tax-relevant transaction record.
  - **Audit Export Pack:** one-click export of everything ZATCA would ask for in an audit — invoice XMLs, QR data, hash chain proof, ledgers, VAT returns, bank records.
- **E2.5 Other taxes:** Zakat (Saudi-owned) vs Corporate Income Tax (foreign-owned) vs apportioned (mixed) — not a filing tool, but COA + reporting must produce the statements a filing is built from, with separate ledger tracking where ownership is mixed. **Withholding Tax** configurable per payment type for non-residents, deducted at payment time + WHT report. **Customs duty & import VAT** via landed cost, **split so import VAT flows to Input VAT while duty flows to inventory cost**. **Digital marketplace VAT liability** — "who is liable for VAT on this transaction" as a configurable attribute. **VAT grouping** — consolidated reporting across multiple entities under one VAT registration.

---

## E3 — Payment Methods & Compliance

Context: electronic payments are ~**85% of Saudi retail transactions**. Coverage determines whether the software is usable in the market at all.

**E3.1 In-store tenders (all required):** Cash (drawer, change, denomination count at shift close) · **Mada** (national debit, majority of domestic card txns, **non-negotiable**, **lower fee ⇒ tracked as its OWN tender type, not lumped into "card"**) · Visa/Mastercard (higher fee, tracked separately for true margin) · Amex · Apple Pay (runs on Mada or card rails) · **STC Pay** (largest Saudi wallet) · Samsung Pay/NFC · **SADAD** (national bill payment, B2B settlement) · Bank Transfer (IBAN/reference capture) · Cheque (issue/deposit/clearing/bounce) · **BNPL Tabby & Tamara** (large share of Saudi checkouts, raises AOV, **first-class tender with own settlement + fee tracking**) · Store Credit/Gift Card · Loyalty redemption (posts against loyalty liability) · Customer Due (posts to AR, credit-limit rules) · **split payment across any combination** (e.g. SAR 200 cash + 300 Mada + 500 Tabby on one invoice).

**E3.2 Online minimum viable checkout:** **Mada + Apple Pay + STC Pay + Visa/MC + at least one BNPL**.

**E3.3 Gateway architecture:** **gateway-agnostic abstraction layer** — never hard-wired to one processor; **each tenant connects their own merchant account**, platform routes through an adapter. Adapters: HyperPay, Moyasar, PayTabs, Tap, Geidea, Amazon Payment Services, Checkout.com, + Tabby/Tamara.
- **The SAMA licence sits with the payment service provider — NOT the merchant, NOT your software.** The platform doesn't need a SAMA licence; it needs to **only integrate with SAMA-licensed providers**.
- **Stripe does NOT work as a primary Saudi gateway** (not SAMA-licensed for Mada acquiring) — architecture must not assume a Stripe-style default.
- **PCI-DSS scope minimization:** prefer hosted checkout / tokenized flows so raw card data never touches your servers.
- **Mada sandbox testing is a required QA item** — Mada behaves differently from international cards.

**E3.4 Terminals:** integrated card terminal with **amount pushed from POS so cashier can't mistype**, approval returned automatically. SoftPOS/tap-to-phone. Fallback manual entry with **mandatory reference capture**.

**E3.5 Fees/settlement:** per-tender fee tracking (Mada cheaper, BNPL higher) for true net margin. Settlement timing **T+1/T+2** ⇒ "collected but not yet settled" as a distinct figure. Gateway settlement batches reconcile back to individual sales.

---

## E4 — PDPL Privacy & Data Governance

**Enforcement is active.** Grace period ended **14 Sept 2024**. SDAIA has issued dozens of violation decisions covering exactly what a badly-built POS/CRM causes: processing without legal basis, unauthorized disclosure, inadequate safeguards, **marketing without consent**. Fines up to **SAR 5,000,000 per violation, DOUBLED for repeat**, criminal liability possible for intentional harmful disclosure of sensitive data. **The law applies EXTRATERRITORIALLY — covers this SaaS platform even operating from outside the Kingdom.**

**E4.1 Ten capabilities:**
1. Data minimization by design.
2. **Lawful basis & consent tracking** — each field traceable to a basis (contract/consent/legal obligation/legitimate interest). **Marketing consent recorded SEPARATELY from transactional**, with timestamp, channel, proof. *The single most commonly enforced violation.*
3. **Data classification** — Public / Internal / Personal / Sensitive Personal, so protection rules apply automatically.
4. **Data Subject Rights workflow** — access, export, correction, deletion/anonymization, objection, portability. **SLA: act within 30 DAYS, extendable by a further 30** where unusual effort or multiple requests. System tracks the clock, warns before breach, logs every request + outcome. **SLA values live in the Regulatory Rule Registry, not hard-coded.**
5. **RoPA** producible on short notice — SDAIA audits have specifically requested these.
6. **Retention & destruction engine** — per-category config, scheduled archive → anonymize → destroy, **permanent destruction log**.
7. **Legal hold override** — tax retention (6+ yrs) overrides routine deletion requests.
8. **Breach management** — **72 HOURS to notify SDAIA from becoming aware** of a breach that may harm personal data or rights, plus notification to affected subjects without undue delay where risk is high. Content must capture: nature + how it occurred · categories + estimated number affected · assessed consequences · containment/remediation. **A 72-hour countdown starts AUTOMATICALLY the moment an incident is logged.**
9. **DPO designation** per tenant (internal or external).
10. **Processor records** — **the platform operator is a DATA PROCESSOR for every tenant.** Needs its own PDPL posture: data processing agreements with each tenant, documented security controls, sub-processor records for cloud/SMS/email vendors.

**E4.2 Data residency (affects infrastructure directly):** *"do not force every client onto one global database."*
```
Tenant → Data Region (Saudi Arabia │ Europe │ Asia │ Other)
       → Database instance + Object storage in that region
```
Per-tenant region set at onboarding, **Saudi-region hosting available**. Transfer safeguards documented per region. ⚠️ Saudi rules generally require transfer to a jurisdiction SDAIA deems adequate, or an appropriate authorization/safeguard route — **must be confirmed against the current Transfer Regulation before hosting architecture is finalized**. **SDAIA controller registration reference** captured per tenant for audit evidence.

---

## E5 — E-Commerce Law (applies to the Online Order module)
Any business selling online — website, app, **even a social media account** — is an e-commerce service provider and **must register with the Ministry of Commerce**. Historically **Maroof**; more recent guidance consolidates under **business.sa** (Saudi Business Center). ⚠️ Actively transitioning ⇒ **treat registration identifier + verification badge as a configurable per-tenant field**, changeable without a code change.

**Mandatory storefront disclosures, auto-rendered from tenant settings, in Arabic:** CR number · VAT number · business contact details · VAT-inclusive pricing · written return/refund policy · delivery terms.

**Consumer protection to enforce operationally:** **14-day cooling-off/return right** (configurable per product/category — some categories legally exempt) · **no hidden fees** (delivery, VAT, service charges itemized before payment) · accurate non-misleading descriptions incl. price, spec, warranty, return terms.

Penalties run into hundreds of thousands of riyals up to ~**SAR 1 million**, plus possible suspension/blacklisting ⇒ make these a **required, validated onboarding step, not an optional field**.

---

## E6 — Labour & Payroll (Saudi)
- **WPS via Mudad** (MHRSD), coverage expanded across private sector including small establishments. System generates the required electronic wage file and supports submission timing + payment-window rules.
  - ⚠️ **Wage-file format/version, submission lead time, salary payment window are Regulatory Rule Registry entries, NOT hard-coded.** Earlier draft values (XML-based, submit before payday, payment early in month) are **"directionally correct" but MUST be locked against the live Mudad spec before payroll ships** — *file formats in this area change without public announcement.* Build the generator against a **versioned format definition** so a change is a registry update + template swap, not a rewrite.
- **Cross-platform consistency:** Mudad cross-references **GOSI** and **Qiwa**. **A mismatch between Qiwa contract, GOSI wage, and actual bank transfer gets flagged automatically and can FREEZE PORTAL ACCESS.** Employee master must hold **Iqama/National ID · GOSI registration · Qiwa contract reference · bank/IBAN**, with a **consistency check before file generation**.
- **GOSI engine:** rates differ **Saudi vs expatriate**, differ **pre- vs post-July-2024 hire** (new Social Insurance Law), and are on a **legislated upward path**. **All rates from the registry as a dated schedule — never hard-coded, never a single "current" figure.** Payroll must calculate using **the rate in force for the pay period being processed**, so re-running an old month gives the historically correct figure.
- **EOSB accrued MONTHLY**, not discovered at termination.
- **Saudization/Nitaqat** — report Saudi vs non-Saudi ratio.
- **Iqama/work permit expiry alerts.**
- Positioning: **"WPS/Mudad-ready"** — software prepares compliant wage data; **final legal responsibility for submission remains the employer's**.

---

## E7 — Compliance Monitoring Dashboard
One screen answering *"am I legally exposed right now?"* — 9 panels: ZATCA onboarding/CSID status per branch+terminal · invoices pending/failed/retrying with drill-down · current VAT rate + next filing deadline countdown · **VAT return reconciliation status (do POS, ledger, bank, Fatoora agree?)** · PDPL posture (consent coverage, pending DSRs, retention job status, open incidents) · e-commerce disclosure completeness · WPS/Mudad submission status + next deadline · employee document expiry warnings · **record retention/archive health (is the 6-year archive intact and retrievable?)**.

---

## E8 — Regulatory Rule Registry

**HARD RULE, verbatim:** *"No legal figure, deadline, threshold, rate, or file format may be hard-coded anywhere in the codebase."*

### E8.1 Structure
```
RegulatoryRule
├── rule_key         (e.g. "SA.VAT.STANDARD_RATE")
├── country
├── value / payload  (rate, threshold, format spec, SLA days)
├── effective_from
├── effective_to     (null = currently in force)
├── source_authority (ZATCA / SDAIA / GOSI / MHRSD / SAMA / MoC)
├── source_document  (official doc name + version/date)
├── verified_on      (date a human last checked against Tier 1)
└── verified_by
```
**Historical accuracy:** *"A VAT report for March must use March's rate, even if the rate changed in June. NEVER apply the current value retroactively."*

### E8.2 What must live in the registry
- **VAT:** standard rate (15%) · mandatory threshold (375,000) · voluntary threshold (187,500) · monthly-vs-quarterly threshold (40M) · filing due-date rule · retention period (6 yrs, extended for defined assets)
- **ZATCA:** simplified reporting window (24h) · standard clearance rule · **XML schema version** · **QR TLV field set** · **hash algorithm** · CSID renewal interval · wave definitions
- **GOSI:** employer rate · employee rate · Saudi vs expat split · pre/post-July-2024 hire split · contribution wage cap · **scheduled step-increases through 2028, each as its own dated row**
- **WPS/Mudad:** wage-file format spec + version · submission lead time · salary payment window · required identifier fields
- **PDPL:** DSR response deadline (30 days + 30) · breach notification (72h) · retention defaults per category · cross-border transfer conditions
- **Labour:** EOSB accrual formula · leave entitlements · overtime multipliers
- **E-Commerce:** cooling-off (14 days) · category exemptions · required disclosure fields
- **WHT:** rates by payment type + recipient status

### E8.3 Governance
**Super Admin ONLY** may edit; every change permanently audit-logged with before/after + source document cited. **Effective-dated rollout** — a change entered today with next-month effective date applies automatically across all tenants, no deployment. **Staleness alerting** via `verified_on` — suggested re-verify every **6 months for tax/payroll, 12 months for others**. **Per-tenant override** supported but rare, always with written reason + audit entry.

### E8.4 ⚠️ Three pre-first-release blockers (most volatile, most damaging if wrong)
1. **Mudad wage-file format** — layouts change without publicity; pull from live spec immediately before build, re-check before each payroll release.
2. **GOSI rate schedule** — enter as a **COMPLETE DATED SCHEDULE**, not one current figure.
3. **ZATCA XML/QR schema version** — must match what ZATCA currently accepts; **record which schema version each archived invoice was generated under**.

---

## PART N — Regulatory Source Hierarchy

**Gate rule:** *"No compliance feature may be implemented from Tier 2 alone."* Every technical detail — field formats, hash construction, QR encoding, wage-file layout, contribution rates, retention periods — must come from the official document.

**Tier 1 binding sources:** ZATCA `zatca.gov.sa` (E-Invoicing Detailed Guideline, **XML Implementation Standard**, **Security Features Implementation Standard**, Data Dictionary, Fatoora SDK + developer portal, CSID onboarding docs; VAT Implementing Regulations) · SDAIA `sdaia.gov.sa` (PDPL, Implementing Regulation, **Transfer Outside the Kingdom Regulation**, destruction/anonymization guidance) · MHRSD + Mudad (`hrsd.gov.sa`, `mudad.com.sa`) · GOSI `gosi.gov.sa` · Qiwa `qiwa.sa` · SAMA `sama.gov.sa` (**licensed PSP list**) · Ministry of Commerce `mc.gov.sa` / `business.sa` · SOCPA (IFRS as adopted in Saudi Arabia).

**Tier 2 (orientation only, never a basis for a technical decision):** EY, PwC, KPMG, DLA Piper, ICLG and assorted tax-advisory/payments/ERP-vendor publications.

### The 12 claims that MUST be re-verified before coding
1. Simplified reported within 24h; Standard cleared before delivery → **drives the entire offline state machine**
2. Exact XML field set, hash construction, TLV QR byte layout → **wrong bytes = rejected invoices at scale**
3. Current wave thresholds + deadlines
4. VAT 15%; thresholds 375,000 / 187,500
5. Monthly above SAR 40M, quarterly below; due last day of following month
6. Retention ≥6 years, up to ~11 for certain assets
7. **Mudad wage-file format, submission timing, payment window** (directionally right only)
8. **GOSI rates + pre/post-July-2024 distinction** (must be a dated table)
9. PDPL breach + DSR deadlines
10. **Cross-border transfer conditions** → determines hosting architecture
11. E-commerce registration channel (Maroof vs business.sa) + disclosures
12. **Which gateways are currently SAMA-licensed**

**Process rule:** *"When a Tier 1 source contradicts this document, the Tier 1 source wins and this document is amended — with the change dated and noted."*

---

## Mandatory wording discipline (UI, docs, marketing)
✅ "supporting ZATCA requirements" · "built to support ZATCA and PDPL requirements" · "WPS/Mudad-ready"
❌ "ZATCA-certified" · "certified compliant" · "guaranteed compliant" · "never at legal risk"
A Saudi tax advisor and legal counsel should review the implementation before go-live.

## Two hard cross-module conflicts the design must resolve explicitly
1. **Tax retention (≥6 yrs) vs PDPL deletion rights** — legal hold overrides the destruction engine; anonymize personal data where possible while preserving the tax record.
2. **ZATCA immutability + non-resetting ICV chain vs any delete/void/edit affordance** — no actor including Super Admin may edit or delete a finalized invoice; rejected invoices stay in the chain flagged REJECTED, corrected only by Credit Note.
