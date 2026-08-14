# Blueprint Parts F–M — Architecture & Platform (distilled)

Source: `RawSyst-POS-Blueprint-v2.4-FINAL.md` lines 1086–1513 + 1625–1668. Read this instead of the doc.

## F1 — Workflow / Approval Engine
Owner defines rules **visually, without a developer**. One codebase serves a 3-person shop and a 300-person chain.

| Element | Options |
|---|---|
| **Triggers** | transaction type · amount threshold · percentage threshold · product/category · store/branch · employee · customer credit exposure · time of day · quantity |
| **Actions** | require approval (single or **sequential multi-step**) · require second-person PIN · block outright · allow with logged warning · notify without blocking · **escalate after timeout** |
| **Routing** | by role · by named person · by **hierarchy** (employee's direct manager) |
| **Escalation** | no response within X hours → escalate; approvers can **delegate while on leave** |

Every execution recorded in audit log **with the full decision chain**. All pending items surface in Approval Center (D5).
Needs: rule-definition store, runtime evaluator hooked into every transaction commit path, pending-approval inbox, timer/escalation jobs, delegation records.

## F2 — Customer Portal
Mobile-friendly, customer logs in via **phone + OTP**. View/download past invoices **with ZATCA QR intact** · order status + delivery tracking · pay outstanding dues · loyalty balance/tier/redemption · store credit & gift card balance · **warranty status by serial number** · submit return/exchange request **routed into the workflow engine** · profile, saved addresses, **saved sizes for fashion** · support tickets · **PDPL rights self-service** (view data held, correct, export, delete) — makes DSR compliance largely self-serving instead of a manual burden on the shop owner.

## F3 — Supplier Portal
View POs and **accept/reject with comments** · submit quotations against an RFQ **feeding the comparison screen (B5.1)** · upload invoice against a PO **feeding three-way match (B5.2)** · view GRN records · payment status/history/outstanding · track purchase returns · upload compliance docs (CR, VAT cert, bank details) **with expiry tracking**.

## F4 — Multi-Company / Group Consolidation
**One Owner login can hold multiple companies under a group.** Consolidated group P&L and balance sheet, **while each company keeps its own separate books, VAT registration, and ZATCA sequence.** Inter-company transactions tracked and **eliminated** in consolidation. Supports the VAT grouping scenario (E2.5).
⇒ Tenancy must support **group → company → store**; ZATCA ICV/PIH chains and VAT registrations are **per company, never per group**.

## G — Internationalization
**Core rule:** *"Nothing about tax, invoicing, or language should be hard-coded to one country. Each country is a swappable configuration block."* — **"Saudi is a config, not the architecture."**
```
Country ├── Currency ├── Tax Configuration ├── Language / RTL-or-LTR
        ├── Number & Date Format ├── Accounting Rules └── Compliance Module
```
Saudi ships as the first fully-built config (SAR, 15% VAT, ZATCA, Arabic RTL, PDPL).
**G2** base currency per tenant, transaction currency, FX rate management, **currency gain/loss tracking**.
**G3** Arabic + English + Bengali at launch, **translation-management system so new languages need no code release**. **Full RTL layout mirroring** for Arabic (menus, receipts, invoices, dashboards) — *"not just translated text sitting in an LTR layout."*
**G4** growing library of pre-built tax templates per country.

## H1 — Security
Password + **MFA/OTP** · session management · **device/session list with remote revoke** · **RBAC enforced server-side** with store-level scoping and amount-based approval limits · HTTPS, CSRF, rate limiting, input validation, SQLi/XSS protection, secure headers, signed/expiring API tokens · **Secrets: DB credentials, API keys, and ZATCA CSID private keys NEVER in plain source or unencrypted `.env` in production — Secret Manager/Vault/KMS required.**
*(No explicit password-complexity policy stated; only "temporary password, forced change on first login" + irreversible hashing.)*

## H2 — Offline-First Sync Engine
> *"The single most important POS engineering rule: selling must never stop just because the internet stopped."*

**Local SQLite holds (all usable with zero internet):** products · current stock snapshot · customers · POS config · in-progress and completed transactions · payment records · **a secure sync queue**.

**Flow:** `Local SQLite transaction → Sync Queue → Go Sync Engine → Cloud PostgreSQL → (if Saudi tenant) ZATCA submission workflow`

**Idempotency:** every transaction carries a **UUID generated at creation time**; if that UUID already exists in the cloud the sync engine **must not create a duplicate** — *"this is what makes 'sell offline all day, sync at night' safe."*

**Five conflict mechanisms:** ordered events · offline timestamps · device identity · **sync cursor per device** · a defined resolution rule for true conflicts (last-write-wins with flagged review, **or manual reconciliation for financial records**).

**Failed sync queue:** retried automatically, **visible to Super Admin/Owner, never silently lost**.

## H3 — Device Management
Each POS terminal registers as a Device: `Device ID · Store ID · Terminal ID · OS · app version · last sync time · last active time · assigned cashier · linked printer config`. Owner can activate/deactivate/revoke/rename/reassign to another store/force app update/view health + sync status remotely.

## H4 — Backup & DR
**Automatic daily encrypted backup (AES-256) per tenant, with backup verification** — *"a backup that can't be restored is not a real backup."* **One-click manual backup** from phone/laptop/iPad. **Point-in-time recovery** where supported + documented, periodically-tested restore procedure. Backup dashboard: last backup time, status, size, failures, **most recent valid recovery point**.
⚠️ **No numeric RPO/RTO stated anywhere — open design decision.** (Related: Part M requires ZATCA retry queue to recover after a simulated **24-hour outage**.)

## H5 — SaaS Subscription, Billing, Feature Flags
Tiers **Starter / Professional / Business / Enterprise**, each with configurable limits: stores · users · POS terminals · storage quota · SMS credits · advanced modules (Payroll, Wholesale, Online Orders, Advanced Analytics, ZATCA module, API access). **Per-tenant feature flags** independent of plan. Billing monthly/yearly/lifetime, tenant invoicing, dunning/suspension with configurable grace period.
⚠️ **v2.2 correction overrides this:** ZATCA compliance **is core and activates on obligation status; it can never be sold away or switched off.** Only compliance *monitoring* is premium.

## H6–H10
- **H6 API:** REST + Webhooks. Connectors for payment gateways (SAMA-licensed), SMS, email, WhatsApp Business, shipping/courier, external accounting, e-commerce/marketplaces, banks, card terminals, ZATCA Fatoora. API keys, OAuth where applicable, rate limiting, full access logs.
- **H7 Import/Export:** import Products/Customers/Suppliers/Opening Stock/Opening Balances/Employees via CSV/Excel **with field mapping and validation before commit**. Export Excel/CSV/PDF/JSON. **Migration wizard:** `Old system export → CSV/Excel → Field Mapping → Validation → Preview → Import → Error Report → Completed` — *"removes the biggest reason a prospective client hesitates to switch."*
- **H8 Health monitoring:** API uptime · DB health · CPU/RAM/disk · active tenants + users · API latency · failed background jobs · **failed ZATCA requests platform-wide** · backup status · sync failure counts · error rate.
- **H9 Job queue:** ZATCA submissions · heavy reports · email/SMS · scheduled backups · recurring invoice/expense generation · notifications · data export · sync processing — *"so none of these ever block a cashier's checkout screen."*
- **H10 Support:** tenant tickets with screenshot attachment, central Super Admin queue, bug/feature tagging, system-wide announcements.

## I — Settings
- **I1** company profile, branches, tax config, receipt/invoice defaults, POS behaviour defaults, business rules (e.g. minimum floor price enforcement on/off)
- **I2** template customization per type — **9 template types:** Thermal Receipt · A4 Invoice · Quotation · Purchase Order · Delivery Challan · Credit Note · Debit Note · Payment Receipt · Customer Statement. Logo, header, footer, return-policy text, tax numbers, Arabic/English blocks, QR placement, payment info.
- **I3 Numbering engine** — configurable, separable by store/document type/country/fiscal year (`INV-RYD-000001`). ⚠️ **Critical:** for Saudi tenants numbering must still respect ZATCA's own sequence/hash/counter requirements — *"the numbering engine and the ZATCA ICV are related but not the same thing, and the design must not let a 'friendly' custom invoice number break the mandatory tamper-evident counter underneath it."*
- **I4 Per-user:** language · theme · currency display · date format · notifications · **dashboard widget layout** · **POS quick-button layout** · default printer · default store
- **I5 Per-terminal:** default warehouse · linked printer/scanner/drawer · receipt template · keyboard shortcuts · default discount rule · **whether customer selection is required before checkout**

## J1 — Confirmed Stack
| Layer | Technology |
|---|---|
| Web | Next.js + TypeScript, React, **Tailwind, shadcn/ui** |
| State/data | **TanStack Query** |
| Validation | **Zod** |
| Desktop POS | **Tauri + React + TypeScript**, local SQLite, direct hardware access |
| Mobile | Responsive **Next.js PWA** (installable). React Native only if native features become unavoidable |
| Backend | **Go** — Chi or Echo preferred over a framework chosen purely for raw benchmark speed ("long-lived enterprise system") |
| API | REST + WebSocket (live dashboard/notifications) |
| Cloud DB | **PostgreSQL** |
| Local DB | **SQLite** |
| Cache/Queue | Redis (only where genuinely needed — sessions, rate limiting, job queue) |
| Object storage | **S3-compatible abstraction**, provider-agnostic |
| Proxy | Nginx |
| Containers | **Docker Compose initially; Kubernetes only later if genuinely needed at scale** |
| CI/CD | GitHub Actions — tests, migrations, security scanning, deployment |
| Monitoring | Prometheus/Grafana · Sentry |
| Secrets | **KMS / Secret Manager / Vault** |
| Crypto | Go stdlib crypto + approved ZATCA signing workflow |
| Barcode | Code128 / EAN / UPC / QR |
| Printing | **ESC/POS** |
| Hosting | **Saudi-region cloud where data-residency/PDPL requires** |

## J2 — High-Level Architecture
**One shared Go API + one shared core logic layer.** Next.js Web and Tauri POS are **two front ends of one product, not two products.**
```
Super Admin ─┐
             ├─ Tenants → Owner → Stores → Users
Next.js Web ─┴─ Tauri POS (SQLite local)
        └────────── Go API ──────────┐
                     ├── PostgreSQL (Sales·Inventory·Accounting·CRM·HR/Payroll)
                     ├── Redis
                     └── Object Storage
        Sales └── ZATCA Compliance Engine → Fatoora
```

## J3 — Data Flow (offline POS → cloud → ZATCA)
```
Cashier sells (no internet) → Local SQLite txn (UUID assigned) + receipt printed + local stock updated
 → Internet reconnects → Sync Queue → Go Sync Engine → PostgreSQL (idempotent, UUID-checked)
 → If Saudi + ZATCA enabled: UBL 2.1 XML → UUID + ICV + PIH chain → ECDSA stamp (CSID) → QR
 → Fatoora submission (Clearance B2B / Reporting B2C) → status tracked; failure → retry queue + critical alert
```
⚠️ **CONFLICT:** J3 shows signing **cloud-side after sync**, but **E1.3 RULE 1 (marked LOCKED) requires signing LOCALLY on the terminal**, and Part L mandates "offline signing architecture (sign locally, transmit later)". **E1.3 wins — J3 is a simplification and should be corrected.**

## J4 — Performance Targets
| Target | Value |
|---|---|
| Barcode scan → product in cart | Near-instant, **local lookup, no network round-trip** |
| Cart update | **Under 100 ms locally** |
| Payment completion | Immediate, local-first transaction close |
| Standard dashboard/API queries | **Typically under 500 ms** |
| Heavy reports | **Always asynchronous, never blocking UI** |
| POS availability | **Fully operational with zero internet at all times** |

## J5 — Testing Strategy
Unit (pricing, tax, commission, discount) · integration (API + DB) · E2E (full POS checkout) · **offline-specific** (disconnect mid-sale, multiple queued txns, reconnection, **duplicate-sync prevention**, conflict scenarios) · **ZATCA-specific** (XML structural validation, QR payload, **hash-chain integrity**, signature checks, **ZATCA SDK verification pass**, live sandbox responses).

## K — Module / Navigation Tree
**Super Admin (10):** Platform Dashboard · Tenants · Subscriptions & Plans · Feature Flags · Global Settings · Integrations · Support Tickets · System Health · **Compliance Watch (failed ZATCA across all tenants)** · Platform Audit Log

**Owner/Tenant ERP (24 top-level):** Dashboard · **POS** · Sales (Invoices│Quotations│Orders│Returns│Exchanges│Credit Notes│Debit Notes│Deliveries│Recurring│Regions) · Purchases (Requisitions│RFQ & Comparison│POs│GRN│Purchase Invoices│3-Way Match│Supplier Returns) · Inventory (Products│Variants│Bundles│Categories│Brands│Units│Stock│Transfers│Adjustments│Count│Costing│Barcode Studio│Warehouses│Batch-Expiry│Serial-IMEI) · Customers CRM · Suppliers · Accounting (COA│Journals│GL│TB│BS│P&L│Cash Flow│Fiscal Periods│Cash│Bank│Reconciliation│Settlement│Receivables│Payables│Expenses│Investments│Transfers) · Employees HR (Directory│Attendance│Payroll│WPS-Mudad│GOSI│EOSB│Commission) · Assets · Online Orders · Wholesale · Warranty & Service · Installments · Shift Management · Reports · Analytics · Notifications · Compliance (ZATCA│VAT Returns│Tax Archive│PDPL│Data Residency│E-Commerce Disclosures│Audit Log) · **Workflows** · **Approval Center** · Settings · **Backup & Restore**

*Workflows, Approval Center, and Backup & Restore are deliberately top-level, not nested under Settings.*

## M — QA & Launch Gates (acceptance criteria that constrain architecture)
1. **Accounting integrity:** TB always balances · BS balances · **sub-ledgers tie to control accounts** · **closed periods truly locked**
2. **ZATCA:** SDK validation passes for Standard/Simplified/Credit/Debit · **hash chain unbroken across 10,000+ sequential invoices** · **ICV never resets or gaps** · QR scans in ZATCA's verification app · sandbox clearance AND reporting succeed · **retry queue recovers after simulated 24-hour outage**
3. **Offline:** **sell 500 invoices fully disconnected** → reconnect → **zero duplicates, zero lost invoices, correct hash chain order, correct ZATCA submission**
4. **Payments:** Mada sandbox passes · **split across 3 tenders posts correctly** · **refund reverses across all tenders** · **settlement batch reconciles to individual sales**
5. **VAT:** return figures reconcile against POS reports, GL, bank records, and submitted Fatoora data for the same period
6. **Arabic/RTL:** receipt, invoice, UI render correctly **including mixed Arabic/English product names and numerals**
7. **Permissions:** every restricted action attempted **via direct API call** as a Cashier must be **rejected server-side**
8. **Multi-tenancy:** cross-tenant access via **manipulated API requests must fail in every case**
9. **Retention:** a 6-year archive record is retrievable and **its hash still validates**
10. **Legal review:** Saudi tax advisor on VAT/ZATCA; legal counsel on PDPL + tenant DPA

## Outstanding before/during system design (blueprint's own list)
- Product name/branding — finalize after trademark/domain clearance
- **Tier 1 verification pass** — work the 12-item Part N table against official sources and populate the E8 registry with verified dated values. ***"Do not let developers fill these in from assumption."***
- **Legal review** — Saudi tax advisor (VAT/ZATCA) + legal counsel (PDPL, tenant data-processing agreement)
- **Concrete ceilings** for every previously-"unlimited" limit — stores, users, terminals, storage, SMS credits per tier

## Next step the blueprint prescribes (line 1658)
> Sequence: **Module → Submodule → Feature → User Role → Permission → Workflow → Database Entity → API Endpoint → UI Screen → Service → Background Job → Integration → Security Rule**
>
> **Scope is closed. Design these three first, because every other module depends on them:**
> 1. **Invoice state machine** — offline local signing, ICV/PIH chain integrity, B2B clearance rules (E1.3)
> 2. **Double-entry posting engine** — every transaction type's journal rules, fiscal period lock
> 3. **Sync & idempotency model** — UUID dedup, per-device ordering, conflict resolution
>
> *"Get those three right and the remaining modules are conventional work. Get them wrong and the entire system has to be rebuilt."*

## v2.2 corrections binding on design
1. ZATCA compliance is core, **never feature-flagged off**; only monitoring is premium.
2. Phase 2 obligation is **per-taxpayer, notification-driven, captured as tenant config** — never assumed.
3. **E8 Regulatory Rule Registry** — every rate/threshold/deadline/SLA/format is versioned data with effective dates + source reference. Mudad format, GOSI schedule, ZATCA schema version are the three highest-volatility entries.
4. **PDPL SLAs enforced** — 30-day DSR response (extendable 30), 72-hour SDAIA breach notification with automatic countdown from incident logging.
5. **Scope hardened** — Light Production Cost Tracking IN; full Manufacturing ERP (BOM, work orders, WIP, variance) explicitly **OUT for v1**. All "unlimited" claims need concrete ceilings at design time.
