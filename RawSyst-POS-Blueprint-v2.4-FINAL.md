# RawSyst POS — Master Feature Blueprint

---

## 🏢 Company & Founder Information

| Field | Details |
|---|---|
| **Product Name** | RawSyst POS |
| **Company** | RawSyst IT |
| **Company Website** | https://rawsyst.com |
| **Founder & Owner** | Mahedi Hasan Emon |
| **Founder Portfolio** | https://mahedihasanemon.site |
| **Founder Role** | Founder, Owner & Lead Developer |
| **Founder Stack** | Go · Next.js · TypeScript · React · Django · PostgreSQL · Docker · REST API · AI/ML |
| **Education** | BSc in CSE — Daffodil International University (CGPA: 3.93/4.00) |
| **Location** | Dhaka, Bangladesh |
| **Email** | mahedi.emon62@gmail.com |
| **GitHub** | https://github.com/mahedi-emon |
| **LinkedIn** | https://linkedin.com/in/mahediemon |
| **Product Tagline** | Complete Retail ERP & POS — Saudi Arabia & International |
| **Backend** | Go (primary) |
| **Frontend Web** | Next.js + TypeScript |
| **Desktop POS** | Tauri + React |
| **Database (Cloud)** | PostgreSQL |
| **Database (Offline)** | SQLite |
| **Infrastructure** | Docker · Nginx · GitHub Actions |

---

> ✅ **Product name CONFIRMED: RawSyst POS — A RawSyst IT Product**
>
> **Branding must appear consistently in:**
> - Login screen → *"RawSyst POS — by RawSyst IT"*
> - Dashboard header → *"RawSyst POS"*
> - Invoice / Receipt footer → *"Powered by RawSyst POS | rawsyst.com"*
> - Email templates → *"RawSyst POS Team | RawSyst IT"*
> - Super Admin panel → *"RawSyst POS — Platform Admin | RawSyst IT"*
> - API response headers → `X-Powered-By: RawSyst-POS`
> - Error pages, documentation, support tickets
>
> Store ALL branding strings in a **single global config file** — product name, company name, website URL, support email — so any future update is a one-line change everywhere.

**Version:** 2.4 — FINAL / FROZEN (pre-System-Design)
**Supersedes:** v1.0, v2.0, v2.1, v2.2 and v2.3.
**v2.4 change:** Full company and founder profile added — RawSyst IT, Founder Mahedi Hasan Emon, rawsyst.com. Backend confirmed as **Go**. Branding specification expanded with exact copy per surface.
**v2.2 changes (corrections only, no new scope):** resolved the ZATCA feature-flag contradiction — legally-required compliance can no longer be switched off · narrowed the Phase 2 obligation wording to per-taxpayer/notification-driven · added **E8 Regulatory Rule Registry** so every legal rate, threshold, deadline and file format is versioned data with effective dates instead of hard-coded logic · made PDPL response SLAs explicit (30-day data-subject response, 72-hour breach notification) · drew a hard scope boundary between Light Production Cost Tracking and full Manufacturing ERP (out of scope for v1) · replaced "unlimited" claims with plan/infrastructure-dependent limits · added **Part O requirements traceability** mapping every original stated requirement to its specification · added product naming placeholder.
**v2.1 changes:** corrected section numbering across Parts G–J · fixed all internal cross-references · **locked the offline/ZATCA B2B business rules (E1.3) that were previously left open** · restructured Part N into a binding Tier-1 / contextual Tier-2 source hierarchy with a 12-point pre-coding verification list · tightened compliance wording from guarantee-style to support-style throughout. **No new features added — scope is frozen.**
**v2.0 added:** full double-entry accounting engine, fiscal period control, bank reconciliation, payment settlement, COGS engine, RFQ & three-way matching, configurable workflow engine, customer & supplier portals, multi-company consolidation, expanded Saudi tax module, complete Saudi payment method coverage, and data residency architecture.
**Prepared for:** Multi-tenant, offline-first Retail ERP + POS platform for Saudi Arabia and international clients
**Target stack (confirmed):** Go (backend) · Next.js/TypeScript (web) · Tauri + React (desktop POS) · PostgreSQL (cloud) · SQLite (offline/local) · Redis (cache/queue)
**Purpose of this document:** This is the single source of truth for *what the software must do*. Nothing here is architecture yet — the next step (system design: modules → sub-modules → roles → permissions → DB entities → APIs → workflows → screens) will be built on top of this document.

---

## How This Document Is Organized

| Part | Content |
|---|---|
| A | Platform Foundation — tenancy, Super Admin, Owner, RBAC, onboarding |
| B | Core Retail Operations — product, inventory, procurement, POS, sales, CRM |
| C | Finance & Operations — **full double-entry accounting**, expenses, HR, payroll, shifts |
| D | Intelligence & Communication — reports, analytics, notifications, audit |
| E | **Saudi Arabia Legal, Tax & Payment Compliance** — ZATCA, VAT, PDPL, e-commerce, WPS, payment methods |
| F | Workflow Engine & External Portals — approvals, customer portal, supplier portal, group consolidation |
| G | Internationalization — multi-country, multi-currency, multi-language |
| H | Platform Engineering & Security — offline sync, backup, SaaS billing, API |
| I | Settings & Customization |
| J | Technical Architecture & Stack |
| K | Final Navigation / Module Tree |
| L | Master "Nothing Missing" Checklist |
| M | QA & Launch Checklist |
| N | Regulatory Source Hierarchy (binding Tier 1 vs contextual Tier 2) |
| O | Original Requirements Traceability |

**Note on Part E:** You asked specifically to verify Saudi legal requirements online before finalizing. Part E has been researched and updated against current (August 2026) ZATCA, PDPL/SDAIA, Ministry of Commerce, and MHRSD/GOSI sources so the feature list reflects the *current* legal reality, not just general assumptions. Sources are listed in Part N. This is a compliance-oriented product specification, not legal advice — a Saudi lawyer/tax advisor should still validate the final implementation before go-live.

---

# PART A — PLATFORM FOUNDATION

## A1. Product Vision

**RawSyst POS** — built by **RawSyst IT** (Founder: Mahedi Hasan Emon) — is a single cloud platform that any retail business — from a one-branch shop to a multi-branch Saudi RMG (fashion/garment) chain — can subscribe to and get:

**Sell → Purchase → Stock → Warehouse → Customer → Supplier → Accounting → Employees → Payroll → Online Orders → Delivery → CRM → Analytics → Legal Compliance → Multi-Store — all in one place, on Web, Desktop, and Mobile.**

It must work **offline** in the showroom (billing never stops because internet stops), sync automatically to the cloud, and — for Saudi tenants — be **designed to support applicable ZATCA e-invoicing requirements and workflows**.

*Wording discipline (applies to this document, the product UI, and all sales material):* the software **supports** the client in meeting their obligations; it cannot **guarantee** their legal compliance. Final compliance always remains subject to official ZATCA validation, the client's own configuration and business practice, and their legal/tax review. Never write or say "ZATCA-certified," "guaranteed compliant," or "never at legal risk."

## A2. Guiding Principles (non-negotiable, apply to every module below)

1. **This is an ERP, not just a POS.** Every module must talk to Accounting and Inventory automatically — no manual double entry anywhere.
2. **Compliance is built-in from day one**, not bolted on later. ZATCA, VAT, and PDPL are core architecture, not a plugin.
3. **Offline-first is a hard requirement.** The POS must sell with zero internet and sync later without creating duplicates.
4. **Nothing is hard-coded to Saudi.** Country, tax, currency, and language are all configuration — Saudi is simply the first, most detailed configuration.
5. **Every financial transaction is traceable.** Full audit trail: who, what, when, where, before-value, after-value.
6. **Permissions are enforced on the server**, never only hidden in the UI.
7. **Finalized invoices are immutable.** Corrections happen only through Credit Notes / Debit Notes / Returns — never silent edits or deletes.
8. **Heavy reports run in the background** (async jobs) — the POS screen must never freeze waiting for a report.
9. **The POS must feel instant** — barcode scan to cart, cart to payment, payment to printed receipt, all near-instant, all local-first.
10. **One owner login should answer "where is my money going" in one click** — this was the very first requirement given, and it is treated as a first-class feature, not just a report (see C3).

---

## A3. Multi-Tenant SaaS Architecture

**What it does:** Every client (business) that buys the software becomes an isolated tenant. No tenant can ever see another tenant's data — products, sales, staff, accounting, everything is walled off at the database and API layer.

* Each tenant gets its own: Company profile, Owner account, Users, Stores/Branches, Warehouses, Products, Customers, Suppliers, Employees, Transactions, Accounting ledgers, Reports, Settings, Uploaded documents, Compliance configuration (country/tax rules).
* Tenant identity is attached to every single API request (via JWT/session) and enforced at the database query layer, not just the frontend.
* One platform serving many tenants; each tenant may have multiple branches, warehouses, and users.
* **Engineering note on "unlimited":** the product *vision* is unlimited, but the *specification* is not. Every quantity limit — tenants per cluster, branches, users, POS terminals, SKUs, held carts, custom roles, stored documents — must be defined as a **plan-dependent and infrastructure-dependent limit** with an explicit configured ceiling, even if that ceiling is set generously. Undefined limits create untestable performance expectations and unbounded storage cost. System design must assign a concrete default and maximum to every one of these.

## A4. Super Admin — Platform Control Plane (You, the platform owner — full access, nobody else)

**Purpose:** This is your control room over the entire SaaS business. Only you (the software owner/vendor) has this level of access — it sits *above* every tenant, including every Owner.

* **Tenant lifecycle management:** create a new business account, generate the Owner's first username/password, activate, suspend, or deactivate a tenant, delete a tenant (with data retention policy).
* **Service/subscription control:** assign a plan (Starter/Professional/Business/Enterprise), set custom limits per tenant (max branches, max users, max SKUs, max storage, max SMS credits), extend or shorten a subscription, apply discounts/custom pricing per client.
* **Feature flag control per tenant:** turn Installment Engine, Multi-Branch Sync, SMS Gateway, Wholesale, Payroll, Online Orders, Delivery, Warranty, and Advanced Analytics ON/OFF individually per client, matching what that client actually paid for.
* **HARD RULE — legally-required compliance capability can NEVER be feature-flagged off.** ZATCA e-invoicing, VAT calculation, invoice immutability, audit logging, and PDPL data-subject handling are **core platform capabilities**, not sellable modules. They activate automatically based on the tenant's country and obligation status:

```
Tenant country + VAT registration + ZATCA wave/obligation status
                        ↓
        Compliance Engine ENABLED automatically — cannot be switched off
```

  What *may* be sold as a premium tier: advanced compliance **monitoring** (multi-branch compliance analytics, extended audit-export tooling, proactive deadline dashboards, priority ZATCA failure alerting). The underlying legal capability itself is always on. Selling a Saudi client a plan that silently disables their legal invoicing capability would expose both them and you — this rule exists to make that structurally impossible.
* **Global configuration:** manage global list of countries, currencies, languages, tax templates (so new country configs can be added without code changes), global system branding, global email/SMS provider settings, global storage provider settings, global security policy (password rules, session timeout, lockout thresholds).
* **Platform-wide audit log:** every action taken by Super Admin, or on Super Admin's behalf (e.g., password recovery for an Owner), is permanently logged.
* **System health / telemetry:** live view of server CPU/RAM, active DB connections, per-tenant offline-sync-queue depth, failed background jobs, and — specifically — failed ZATCA API submissions across all tenants, so you can proactively catch a client's compliance problem before they notice it.
* **Support & ticketing:** central inbox for all tenant support tickets, bug reports, and feature requests, with status tracking.
* **Maintenance mode:** ability to put the whole platform, or a single tenant, into maintenance mode during upgrades.

### A4.1 Super Admin Credential Security & Recovery
* Super Admin login requires: strong password + mandatory MFA (authenticator app or SMS OTP), device/session management (see active sessions, revoke a session remotely), login history with location/IP, automatic lockout after repeated failed attempts, suspicious-login detection and alerting.
* **Password recovery for Super Admin:** secure recovery email + MFA-based recovery + one-time backup recovery codes generated at setup (printable/downloadable once) + a documented emergency recovery procedure that cannot be triggered by anyone but the verified platform owner.
* Super Admin's own password is never stored or displayed in plain text anywhere, including to Super Admin.

### A4.2 Owner Account Recovery (the exact workflow you described)
* If a business **Owner forgets their password**, they first attempt normal self-service recovery (recovery email / OTP to registered phone).
* If self-service recovery fails or is unavailable, the **Owner can contact you (Super Admin)**. After you verify their identity (registered phone/email/company details), **you trigger a secure password reset** — you issue a new one-time password, you do **not** view or restore their existing password (because it is stored as an irreversible hash, not plain text — this is a security requirement, not just a policy choice).
* Every such Super-Admin-assisted recovery is written to the permanent audit log (who requested it, who approved it, timestamp, IP).

---

## A5. Business Owner Account, Onboarding & Provisioning

**What it does:** When a client signs up for your service, you (Super Admin) create their tenant and their very first Owner login. From there, the Owner is fully self-sufficient.

* System auto-generates: Company/Tenant ID, Owner username/email, a temporary password (must be changed on first login), and default business configuration based on the plan purchased.
* **Guided Onboarding Wizard** (so a non-technical shop owner can set up the whole system alone):
  1. **Business Information** — company name, legal name, business type, country, address, phone, email, website, Tax/VAT number, Commercial Registration number, base currency, timezone.
  2. **Store Setup** — store name, code, address, phone, opening hours (repeatable for multi-branch).
  3. **Tax Configuration** — country-driven; if country = Saudi Arabia, the system automatically pre-loads Saudi VAT (15%), ZATCA invoice settings, and Arabic RTL defaults. Other countries get their own applicable tax defaults, editable.
  4. **Employees** — add Owner, Manager, Cashier, Accountant, Inventory staff with roles.
  5. **Hardware Setup** — detect/pair barcode scanner, thermal receipt printer, cash drawer, label printer.
  6. **Opening Balances** — opening cash, bank balance, opening inventory, opening investment, opening payables/receivables.
  7. **Finish** — system provisions the full working business environment automatically; Owner lands on their Dashboard.
* Owner has **full control within their own tenant** — every module in this document below "Owner level" is theirs to configure, use, and delegate via roles. Super Admin remains above the Owner only at the platform level (billing, feature flags, uptime) — Super Admin does **not** interfere in the Owner's day-to-day business data.

---

## A6. Role & Permission Management (RBAC) — Deep, Not Just Admin/Cashier

**Purpose:** The Owner decides exactly what every employee can see and do — down to individual actions and money limits, not just "role = manager."

### A6.1 Predefined (ready-to-use) Roles
| Role | Typical Access |
|---|---|
| Owner | Everything in their tenant, unrestricted |
| Branch/Store Manager | Sales, stock, staff, approvals — restricted from bank ledgers & true net profit |
| Cashier / POS Operator | Billing, barcode scan, shift open/close, basic return — cost price & margins always hidden |
| Accountant | Accounts, journals, expenses, VAT reports, bank transfers — restricted from editing inventory/products |
| Inventory / Warehouse Keeper | Stock-in (GRN), transfers, wastage logging, barcode printing — no pricing/sales access |
| Purchase Manager | Purchase requests, POs, supplier communication |
| HR Manager | Employees, attendance, payroll setup — no sales/inventory access |
| Sales Executive | Quotations, orders, own customer list |
| Delivery Staff | Assigned delivery orders only |
| Online Order Manager | Web/app order queue, packing, dispatch |
| Auditor (read-only) | View everything, edit nothing — for external accountants/auditors |
| Customer Service | Customer profile, ticket, returns — no financial data |

### A6.2 Custom Role Builder
* Owner can build custom roles from scratch, or clone and edit a predefined role (count subject to the plan's configured ceiling — see A3).
* Permission granularity per module/feature: **View · Create · Edit · Delete · Approve · Export (Excel/PDF) · Print · Refund · Apply Discount · Void · Adjust Stock · Transfer Stock · View Cost Price · View Profit Margin.**
* Example (matches the exact model you described):
  ```
  Role: Senior Cashier
  Sales:      Create ✓  Hold ✓  Refund ✓  Exchange ✓
  Inventory:  View ✓    Adjust ✗
  Accounts:   Receive Payment ✓   View Profit ✗
  Reports:    Daily Sales ✓       P&L ✗
  ```
* **Scoped restrictions**, on top of the above:
  * By **store/branch** (a manager only sees their own branch).
  * By **warehouse**.
  * By **transaction amount limit** — e.g., Cashier can discount up to SAR 50, Manager up to SAR 500, Owner unlimited.
  * By **time window** (e.g., a temporary staff role that expires).
* **Data masking:** cashiers and general staff can be blocked from ever seeing supplier cost prices, profit margins, or other employees' salaries — even if they technically have "view stock" access.
* All permission checks are enforced **server-side** on every API call — a hidden button in the UI is never treated as real security.

---

## A7. Multi-Platform Client Access

The **same business logic** runs everywhere; only the interface changes per device.

| Platform | Technology | Primary Use |
|---|---|---|
| **Web (Owner/Admin Portal)** | Next.js + TypeScript | Full back-office: configuration, deep accounting, bulk operations, all reports, multi-store oversight |
| **Desktop POS** | Tauri + React + local SQLite | High-speed, offline-capable showroom billing counter with direct hardware access |
| **Mobile (Owner App)** | Responsive PWA (installable, app-like) | Live sales monitoring, expense approval, stock check, notifications — from phone, tablet, or iPad, anywhere |

* Website is **mobile-first and fast** — usable like a native phone app (installable PWA, push notifications where supported, offline caching for key screens).
* One design system shared across Web/Desktop/Mobile so the product feels identical everywhere.

---

## A8. Dashboard & KPI Center

**What it does:** Gives every role a dashboard tailored to what they're allowed to see, answering "how is my business doing right now" in one glance.

### A8.1 Owner Dashboard (the "everything, one click" view)
* Today's Sales, Today's Profit, Gross Profit, Net Profit
* Total Expenses (today/period) — **with drill-down by category in one click**, matching your original requirement to see "kothay ki cost jaitece" (where every cost is going) instantly
* Cash Balance (per register + total), Bank Balance (per account + total)
* Receivables (customer dues) and Payables (supplier dues)
* Inventory Value, Low Stock alerts, Dead Stock alerts
* Top-Selling Products, Top-Performing Employees
* Sales by Store, by Category, by Brand, by Payment Method
* Sales Trend, Expense Trend, Profit Trend (charts, selectable range)
* Outstanding Supplier Payments, Outstanding Customer Dues
* Pending Online Orders, Pending Purchase Approvals
* Investment summary (who invested what, when, and current position)
* Date filters: Today · Yesterday · This Week · This Month · This Year · Custom Range
* Accessible identically from **Phone, Laptop, or iPad** with one-click drill-through on every widget — exactly as originally requested.

### A8.2 Role-based Dashboards
* **Cashier:** POS shortcut, current shift status, today's own sales.
* **Manager:** branch sales, stock alerts, pending approvals.
* **Accountant:** cash/bank position, payables/receivables, VAT summary.
* **Super Admin:** platform-level dashboard (Section A4).

---

# PART B — CORE RETAIL OPERATIONS

## B1. Product & Catalog Management

**What it does:** The master record for everything sellable.

* Core fields: SKU, barcode, product name (+ Arabic name for Saudi/GCC), description, category, sub-category, brand, supplier, unit of measure (Pcs, Box, Set, Meter, Kg, Liter, etc.), cost price, and image(s).
* **Multi-tier pricing per product:** Retail price, Wholesale price, Dealer/Corporate price, and a **Minimum Floor Price** (the lowest price a cashier is ever allowed to sell at, even after discount — enforced by the system, not just policy).
* VAT/tax category per product: Standard, Zero-Rated, Exempt, Out-of-Scope.
* Stock control fields: minimum stock, maximum stock, reorder level, current stock (computed).
* Optional per product: warranty period, serial-number tracking flag, batch/expiry tracking flag.
* **Category / Sub-category / Brand hierarchy** — unlimited depth (e.g., Men → Shirts → Formal Shirts), used everywhere for filtering, reporting, and barcode logic.
* **Product Bundles / Combo Packages:** sell multiple SKUs as one package (e.g., "Suit + Shirt + Tie Combo") with automatic proportional stock deduction of each component on sale.
* **Product Lifecycle States:** Active → Inactive → Discontinued, without ever breaking historical sales records (a discontinued product still displays correctly on old invoices/reports).
* Bulk import/export of products via Excel/CSV (see H7).

## B2. Product Variant Matrix (critical for Fashion/RMG — Saudi showroom core need)

**What it does:** Standard POS logic (one product = one price = one stock count) does not work for clothing. This module handles **Parent → Child variants**.

* Define a **Parent product** (e.g., "Executive Abaya") once.
* Auto-generate **Child variants** across any combination of attributes: Size (S, M, L, XL, XXL), Color (Black, Navy, Maroon), and, if needed, Material, Style, Season, Gender, Model — unlimited custom attributes.
* Each child variant carries its **own** SKU, barcode, price, cost, stock count, weight, and image.
* Variant-level stock reporting (e.g., "Black / XL" out of stock while "Black / M" is overstocked) — this directly powers the Low Stock Alert and Dead Stock Analysis below.
* Variant-aware POS screen: cashier picks the parent product, then a quick size/color grid appears for one-tap selection.

## B3. Intelligent Barcode Engine & Label Studio

**What it does:** Fashion barcodes must be human-readable and instantly printable in bulk — not a generic grocery-store barcode.

* **Smart/meaningful barcode construction:** system auto-builds a readable code from category + season + color + size, e.g. `M-WIN-BLK-XL` = Men / Winter / Black / XL — while still being a scannable Code128/EAN/QR value underneath.
* **Manual barcode override** for products that need a specific existing code.
* **Formats supported:** Code128, EAN-13, EAN-8, UPC-A, QR / Data Matrix (2D).
* **Bulk Barcode Generator:** factory delivers 1,000 pieces → generate all 1,000 (or grouped) barcodes in one click.
* **Apparel Hang-Tag Designer:** print the tag that hangs on the garment itself — logo, English + Arabic product name, size, color, VAT-inclusive retail price, barcode.
* **Thermal Label / Sticker Studio:** direct printing to thermal label printers (Xprinter, Zebra, TSC) in standard sizes (50×25mm, 38×25mm, custom).
* **A4 Bulk Sheet Generator:** printable A4 PDF sheets (24/30/48 labels per sheet) for standard laser/inkjet printers when no thermal printer is available.
* **Customer Loyalty Card Barcode Generator** (links to CRM module, B16).
* **General Barcode Generator** for quick one-off codes and **Category-wise / Brand-wise bulk generation**.

## B4. Inventory & Warehouse Management

**What it does:** Real-time, provable stock truth across every store and warehouse.

* **Stock states tracked:** Total, Available, Reserved (against unpaid online orders), Damaged, Returned, In-Transit, Available-to-Sell.
* **Stock In** sources: Purchase/GRN, Customer Return, Stock Adjustment, Transfer Receive, Opening Stock.
* **Stock Out** sources: Sales, Supplier Return, Damage/Wastage, Stock Adjustment, Transfer Dispatch, Internal Use.
* **Wastage / Damage logging:** mandatory reason + category + automatic loss write-off posted to accounting.
* **Multi-Warehouse & Multi-Store views:** stock per branch, per central warehouse, per in-transit shipment, all in real time.
* **Inter-Branch / Warehouse Stock Transfer workflow:** Transfer Request → Manager Approval → Dispatch (In-Transit lock) → Receiving Branch Confirms & Reconciles. Discrepancies are auto-flagged.
* **Stock Views:** Category-wise, Sub-category-wise, Brand-wise, Supplier-wise, After-Sell Supplier Stock.
* **Reports:** Stock-In Report, Stock-Out Report, Category-wise Stock-In/Out, Brand-wise Stock-In/Out, Minimum-Stock-Out Report.
* **Stock Valuation:** both **FIFO (First-In, First-Out)** and **Weighted Average Cost** methods supported, selectable per business.
* **Landed Cost allocation:** purchase cost + shipping + customs + other acquisition costs are distributed across received products by a configurable rule, so P&L reflects true product cost (example: SAR 10,000 goods + SAR 500 shipping + SAR 300 customs = SAR 11,000 landed cost, spread across the batch).
* **Batch / Lot / Expiry tracking** (for cosmetics/grocery-type inventory where applicable): batch number, manufacture date, expiry date, quantity, supplier, cost; automatic "Expiring Soon" / "Expired" / "Batch Recall" alerts.
* **Serial / IMEI tracking** (for electronics-type inventory): full lifecycle Supplier → Purchase → Inventory → Sale → Customer → Warranty → Return.
* **Stock Audit / Physical Count:** full count, category count, brand count, location count, or random spot count — compares System Quantity vs Physical Quantity, auto-generates a signed Adjustment Voucher (with reason, user, approval, timestamp) for any variance.
* **Minimum Stock Alert Engine:** dashboard + notification when any product/variant crosses its reorder threshold.

## B5. Purchase & Procurement Management

**What it does:** Everything about bringing new stock into the business, and knowing exactly what it cost.

* **Purchase Requisition:** any authorized staff can request stock ("need 100 pcs Black T-Shirt"); routes to manager for approval.
* **Purchase Order (PO):** formal PO to a chosen supplier — products, quantities, agreed cost, tax, discount, expected delivery date; can be exported/printed/emailed as PDF.
* **Goods Receipt Note (GRN):** when goods physically arrive — verify quantity received vs ordered, log damaged/short items, accept or reject, and **only on GRN confirmation does stock actually increase** (so a PO alone never inflates inventory).
* **Purchase Invoice / Bill recording:** itemized tax breakdown, cash or credit settlement terms, freight/customs added to landed cost.
* **Purchase Return (to Supplier):** for defective/excess stock — auto-generates a Debit Note and instantly deducts inventory.
* **Reports:** Purchase Product Report, Daily Purchase Report, Supplier Return Report.
* **Purchase Approval Workflow** (configurable thresholds): Request → Manager Approval → PO → Supplier → GRN → Purchase Invoice → Payment → Accounting entry — each step logged.

## B5.1 RFQ — Request for Quotation & Supplier Comparison (NEW)

**What it does:** Before committing to a supplier, the business collects and compares competing quotes inside the system rather than over WhatsApp.

```
Purchase Requirement identified
        ↓
RFQ issued to multiple suppliers
        ↓
Supplier A quote │ Supplier B quote │ Supplier C quote
        ↓
Side-by-side comparison (unit price, total, VAT, lead time, payment terms, quality notes)
        ↓
Select winning supplier (with reason recorded)
        ↓
Purchase Order generated automatically from the winning quote
```
* Quote validity dates, automatic expiry, quote versioning if a supplier revises.
* Historical quote archive per supplier — useful for negotiating next time and for proving best-price sourcing during an audit.

## B5.2 Three-Way Matching (NEW — enterprise-grade purchase control)

Before any supplier invoice is approved for payment, the system automatically matches three documents:

```
Purchase Order  +  Goods Receipt Note (GRN)  +  Supplier Invoice
                          ↓
                   3-Way Match Check
```
Compared fields: ordered quantity vs received quantity vs invoiced quantity, agreed unit price vs invoiced unit price, VAT treatment, discount terms, and total value.

* **Tolerance thresholds are configurable** (e.g., allow up to 2% or SAR 50 variance automatically).
* Any mismatch beyond tolerance **blocks payment** and routes to an approver with the exact discrepancy highlighted.
* This is the single most effective control against supplier overbilling and internal fraud, and it is expected in any serious ERP.

## B6. Supplier Management

* Supplier profile: name, company, phone, email, address, tax/VAT registration number, Commercial Registration number, contact person, agreed payment terms (Cash / 7 / 15 / 30 / 60 days / custom).
* **Supplier Ledger:** Opening Due + Purchases − Payments +/− Adjustments = Closing Due, always live.
* **Supplier Payment Vouchers:** partial, advance, or full payments via cash/bank/cheque, with balance auto-adjustment.
* **Reports:** Payable Report, Paid Report, Purchase History per Supplier, Supplier Return Report, Aging Payables (how overdue each supplier balance is), due-date reminders.

## B7. Point of Sale (POS) & Billing — the Speed-Critical Screen

**What it does:** The counter screen cashiers use all day; must be fast, simple, and impossible to misuse outside their permissions.

* **Product selection:** barcode scan (no click into a field required — captures scanner input globally), SKU/name search, browse by category/brand, "Favorites" and "Recently Sold" quick-tap grid, variant size/color picker.
* **Cart controls:** quantity, per-line discount (within the cashier's allowed limit), VAT auto-calculated per line's tax category, manual price override (permission-gated), customer attach, order notes.
* **Payments — full multi-tender split support in a single invoice:** Cash, Mada, Visa, Mastercard, Bank Transfer, Mobile Wallet, Apple Pay/STC Pay, bKash/Nagad (for Bangladesh-style markets), Buy-Now-Pay-Later (Tabby/Tamara-style) where integrated, Store Credit/Gift Card, Loyalty Points redemption, Customer Due/Credit.
  * Example: Total 500 → Cash 200 + Card 300, on one receipt.
* **Suspended Sale / Hold Cart:** hold multiple in-progress carts (configurable per-terminal ceiling) and resume any of them instantly — no lost sale when a customer steps away.
* **Gift / FOC (Free of Charge) items:** bill complimentary items with a dedicated zero-value accounting entry, tracked separately from paid sales.
* **Vat, Due, and Discount — "easy selling system in one click,"** as originally required: a single, fast checkout screen that automatically applies VAT, allows Due (credit) sales to known customers, and applies configured discounts without extra steps.

## B8. Hardware Integration (Showroom Cash-Counter Reality)

* **USB Barcode Scanner:** native HID plug-and-play — scanning instantly adds to cart with no software configuration.
* **Direct Thermal Printing (ESC/POS):** receipts print straight to 80mm/58mm thermal printers **without a browser print dialog popping up** — a background/native print call from the desktop app.
* **Auto Cash-Drawer Kick:** on completing a cash sale, an RJ11 electrical pulse command is sent automatically to pop the drawer open — no manual key/button.
* **Label/Hang-Tag Printer support** (see B3).
* **Customer-Facing Display** (optional second screen showing running total).
* **Weighing Scale integration** (future-ready, for weight-based products like fabric-by-meter or food).
* **Card Terminal integration-ready architecture** (so a physical card machine can be linked to auto-confirm card payments later).
* **Hardware Setup Wizard** on first install: Connect Scanner → Detect Printer → Test Print → Test Cash Drawer → Configure Receipt Layout → Finish — no technical configuration expected from the shop staff.

## B9. Promotions, Discounts & Pricing Engine

* Percentage discount, fixed-amount discount, **Buy-X-Get-Y** (e.g., Buy 1 Get 1 free), **Bundle/Volume pricing** (e.g., 3 shirts for a flat SAR 100), Category-level discount, Brand-level discount, Product-specific discount, Customer-type-specific discount, Coupon/Promo codes, Seasonal campaigns, Flash sales, Employee discount.
* Manual discounts beyond a cashier's limit require **manager PIN authorization**, logged.
* Conditions engine: by date range, store, customer type, minimum purchase amount, specific product/category/brand.
* **Campaign Analytics:** sales generated per campaign, total discount cost, new customers acquired, profit impact/ROI (see D2).

## B10. Sales Returns, Exchange & Replacement

* **Full return, partial return, size exchange, color exchange, general product exchange/replacement** — original invoice is always scanned/linked, never re-typed.
* **Quick Exchange screen:** scan old invoice → select item(s) to return → pick new size/product → system auto-calculates the price difference → complete in one screen.
* **Fast Refund:** cash refund, store-credit refund, or refund-to-original-payment-method, generating a proper **Credit Note** (never a silent invoice edit — see E1/E-invoicing immutability rules).
* **Reports:** Sell Replace Report, Sell Return Report, Products Replacement Register, Replacement Report.

## B11. Sales Quotation, Sales Order & Delivery Documentation

* **Sales Quotation / Proforma Estimate:** customer, products, price, discount, VAT, validity date — convertible to a Sales Order or directly to an Invoice in one click.
* **Sales Order lifecycle:** Draft → Confirmed → Processing → Packed → Delivered → Completed. Supports store orders, online orders, wholesale orders, and pre-orders.
* **Delivery Challan:** generated from a Sales Invoice in one click, printable, itemized **without pricing** (for logistics/proof-of-delivery use).
* **Picking Slip:** tells warehouse staff exactly what to pull for one or multiple orders, organized by warehouse/aisle/shelf.
* **Packing Slip:** customer, items, quantities, package/box number.
* **Recurring Invoices:** for subscription-style or repeat B2B clients — system auto-generates the same invoice on schedule instead of manual monthly re-entry.
* **Sales Region tracking:** revenue and profit reporting broken down by geographic sales territory.

## B12. Wholesale / B2B Module

* Separate wholesale customer type with **Dealer/Wholesale pricing tier**, minimum order quantity rules, bulk-quantity discounts, and a **credit limit** per wholesale client.
* Wholesale-specific Quotation, Order, Invoice, Delivery, and Payment workflows, kept separate from retail so retail reporting isn't distorted by bulk transactions.
* Wholesale Customer Ledger (same structure as Supplier Ledger, mirrored for receivables).

## B13. Online Order & Delivery Management

* **Channels:** own website, PWA storefront, manually entered social-media orders, marketplace integration-ready.
* **Order workflow:** Order Placed → Payment → Stock Reservation (so two channels can't both sell the last unit) → Picking → Packing → Delivery → Completed.
* **Delivery order record:** customer, address, phone, items, assigned driver, delivery fee, payment status (prepaid/COD).
* **Delivery status pipeline:** Pending → Assigned → Picked Up → Out for Delivery → Delivered → Failed → Returned.
* Designed to plug into third-party delivery/courier APIs later without redesign.

## B14. Installment / EMI (কিস্তি) System

* **EMI Plan Generator:** configurable tenure (3/6/12/24 months), down payment, markup/profit rate, resulting fixed monthly installment amount.
* **Customer verification records:** National ID/Iqama copy, employment proof, salary reference, guarantor contact — stored under PDPL-compliant access controls (see E4).
* **Installment Collection Ledger:** per-installment status — Paid / Unpaid / Overdue / Partial — with configurable late-payment penalty calculation.
* **Automated reminders** (SMS/email) before due dates and on payment receipt.
* Example: Product 1,200 → Down payment 300 → Remaining 900 → 3 × 300 monthly.

## B15. Warranty, Serial/IMEI Tracking & Service/Repair

* **Warranty tracking:** period, start/end date, linked serial number, linked customer, linked purchase invoice.
* **Warranty Claim workflow:** verify warranty validity by scanning serial number → log claim → repair or replace → update claim status.
* **Product Replacement Register:** every replacement logged with old/new serial, reason, approver.
* **Service & Repair Work Orders** (in-house or vendor): Received → Under Inspection → Repaired → Delivered, tracking spare-parts cost and labor cost per job.

## B16. Customer Relationship Management (CRM) & Loyalty

* **Customer profile:** name, phone, email, address, country, customer type (Retail/Wholesale/VIP), purchase history, total lifetime spend, current due balance, last purchase date, favorite brands/categories.
* **Fashion Size & Fitting History** (fashion-specific, high-value feature): store each customer's confirmed sizes — e.g., Shirt → L, Pants → 34, Shoes → 42, Collar/Sleeve/Length/Waist measurements — so staff instantly know their size on the next visit.
* **Customer Due / Khata Ledger:** credit sales, payments received, returns, adjustments, live outstanding balance; **credit limit enforcement** (block/require-approval once a customer's due exceeds their configured limit).
* **Loyalty Points Program:** configurable accrual rule (e.g., 100 SAR spent = 1 point), redemption at checkout, and **Tiering** (Bronze/Silver/Gold/VIP) with tier-based perks.
* **Store Credit / Gift Card / Customer Wallet:** refund-to-wallet, gift balance issuance, expiry rules.
* **Customer Segmentation:** New, Returning, VIP, High-Value, Inactive/At-Risk, Wholesale, Retail — feeds targeted marketing and the analytics module.
* **Customer ID / Loyalty Barcode Card Generator.**

---

---

# PART C — FINANCE & OPERATIONS

## C1. Core Accounting (Chart of Accounts, Journal, Ledger)

**What it does:** Removes any need for a separate third-party accounting program — every sale, purchase, expense, and stock movement posts itself into proper double-entry accounting automatically, in the background.

* **Chart of Accounts (COA):** structured, multi-level ledger tree across the five standard groups:
  * **Assets** — Cash, Bank, Inventory, Accounts Receivable, Fixed Assets
  * **Liabilities** — Supplier Payables, Loans, Employee Payables, Tax Payables
  * **Equity** — Owner Capital, Investor Capital, Retained Earnings
  * **Revenue** — Sales, Other Income
  * **Expenses** — Operating Expenses, Cost of Goods Sold, Salary, Rent, Utilities, Marketing
* **Automated Journal Postings:** every sale, return, purchase, expense, payroll run, and inventory adjustment creates the correct debit/credit entry automatically — no manual bookkeeping.
* **Day Book / General Ledger** — every transaction, searchable and filterable by date, account, store, or user.

## C2. Cash & Bank Management

* **Cash accounts:** Main Cash (vault), per-Store Cash, Petty Cash, per-Cashier Drawer.
* **Bank accounts:** multiple bank accounts, card-settlement accounts, and payment-gateway settlement accounts (plan-dependent ceiling) — each with a live running balance.
* **Bank Cheque tracking** (issue, deposit, clear, bounce).
* **Fund transfers, fully tracked:** Cash → Bank, Bank → Cash, Cash → Cash (branch to branch), Bank → Bank — every transfer creates its own audit entry and a printable transfer voucher.
* **Bank Reconciliation:** match system bank ledger against actual bank statement, flag mismatches.

## C3. Expense & Investment Management — "One-Click, See Everything" (core owner requirement)

**This module directly answers the very first requirement of this project: the Owner must be able to see, in one click, exactly where every riyal/taka is going, on any device.**

### C3.1 Expense Tracking
* Record **any** business expense: shop rent, electricity, water, internet, transport, staff meals/tea, marketing, advertising, packaging, maintenance/repair, software subscriptions, delivery cost, customs, government fees, warehouse cost, and fully custom/miscellaneous categories.
* **Light Production Cost Tracking** (explicitly requested): purchase of raw materials for in-house production, stitching/tailoring labour cost, and packaging cost — recorded and allocated **per production batch**, so the true cost of a locally-made item is known and flows correctly into COGS and margin.
  * **Scope boundary — read this carefully:** this is *cost tracking*, **not a manufacturing module**. Full manufacturing ERP — Bill of Materials (BOM), Production Orders, Work Orders, Material Issue, WIP tracking, by-products, routing, capacity planning, production variance analysis — is **deliberately OUT OF SCOPE for v1**. A garment retailer who has items stitched locally is fully served by batch cost allocation. A genuine factory needs a real manufacturing module, which would roughly double this project's scope. **Decision: keep it out, ship the retail ERP, add manufacturing as a v2 module later if a client genuinely needs it.** Scope discipline here is what makes v1 shippable.
* **Client rent / recurring receivable-linked cost tracking:** track amounts owed *to* the business (e.g., renting out space/equipment to a client) alongside amounts owed *by* the business, so both directions of cash flow are visible from the same screen.
* Each expense entry stores: date, amount, currency, expense type, expense head, store/branch, department, payment account used, vendor, description, file attachment (receipt photo/PDF), created-by, approved-by.
* **Expense Categories & Custom Heads** (fully configurable tree, e.g., Operating Expense → Rent / Utilities / Salary / Marketing / Transport / Maintenance).
* **Recurring Expenses:** monthly rent, internet, software, insurance, subscriptions — auto-generated on schedule instead of manual monthly entry.
* **Expense Approval Workflow** (configurable): Employee creates → Manager approves → Accountant verifies → Payment released → Accounting entry posted. Approval thresholds configurable by amount.
* **One-click, customizable "Where is my money going" view** — filterable by day/week/month/year/custom range, by category, by store, by device (phone/laptop/iPad) — exactly as originally described.

### C3.2 Investment Management
* Track **Owner investment**, **Investor investment(s)**, capital injections, investment withdrawals, and each investor's proportional share.
* Investment activity is kept **fully separate from normal revenue** in the accounting model — never mixed with sales income, so P&L stays clean.
* **Investment Report** and **Investor statement** — each investor can (if given access) see only their own contribution/return history.

## C4. Accounts Receivable & Payable (AR/AP)

* Dedicated **aging summaries**: how much is owed to the business (customers) and by the business (suppliers), bucketed by how overdue (0–30 / 31–60 / 61–90 / 90+ days).
* Receivable Report, Received Report, Payable Report, Paid Report — all real-time.

## C5. Employee / HR Management

* **Employee Master Directory:** name, employee ID, phone, email, position, department, assigned branch, joining date, base salary, commission eligibility, employment status, Iqama/National ID + **expiry alerts** (important for Saudi expatriate staff compliance).
* **Attendance & Leave Tracking:** check-in/check-out, late arrivals, early leave, overtime hours, approved leave, unpaid absence — with a biometric-device integration path for later.
* **Salary Advances:** issue an advance and have it automatically deducted from the next payroll run.

## C6. Payroll, Commission & Saudi WPS Compliance

* **Payroll components:** basic salary, allowances, deductions, advances, bonus, commission, overtime, absence deduction → net salary, all computed automatically.
* **Dynamic Sales Commission Engine:** rules by employee, product, category, store, total revenue, or profit; flat percentage or **tiered thresholds** (e.g., 2% commission once an employee's sales exceed SAR 50,000/month).
* **Saudi Payroll Compliance (WPS via Mudad) — verified current requirements:**
  * Saudi Arabia's Wage Protection System runs on the **Mudad** platform (Ministry of Human Resources and Social Development), and it is **mandatory for every private-sector employer, regardless of headcount** — even a business with a single employee must comply.
  * The system should generate the required **electronic wage file** (Mudad's own XML-based format — distinct from the pipe-delimited SIF format used in some other GCC states) and support submission through Mudad **at least one business day before payday**, with salaries paid within the first ten days of the month for most employers.
  * Mudad **cross-references payroll data against GOSI** (social-insurance) records and **Qiwa** (employment contract registration) — the system should support keeping employee master data (Iqama/National ID, GOSI registration, Qiwa contract) reconciled so wage submissions aren't rejected for mismatches.
  * **GOSI contribution calculation** should be built in: for Saudi national employees, employer and employee both contribute (contribution rates have been rising under the new Social Insurance Law effective from July 2024 and are scheduled to keep increasing through 2028 — the engine should use a **configurable, updatable rate table** rather than a hard-coded percentage); for expatriate employees, only the employer contributes, covering occupational hazards.
  * Payroll module should be positioned as **"WPS/Mudad-ready"** — the software prepares and submits compliant wage data, but final legal responsibility for correct submission remains with the employer, per standard practice.
* **Payslip generation** (PDF, bilingual Arabic/English where relevant).
* **Month-wise Expense Report, Employee Salary Report.**

## C7. Fixed Asset Management

* **Asset Master Register:** POS hardware, computers, printers, furniture, AC units, delivery vehicles, equipment — with asset ID, purchase date, cost, location, responsible employee, warranty.
* **Depreciation:** automated monthly straight-line depreciation with journal postings to the general ledger.
* **Asset Disposal/Scrap:** record sale or write-off with automatic gain/loss-on-disposal calculation.

## C8. Shift Management & Cash Drawer Reconciliation (X/Z Report)

* **Shift Open:** cashier enters opening cash float.
* **During shift:** system silently tracks cash sales, card sales, refunds, expenses paid from drawer, cash drops to vault.
* **Mid-Shift Cash Drop:** move excess cash from register to vault safely during busy hours without closing the shift.
* **X-Report:** instant mid-shift snapshot — sales and payment-method breakdown — without closing anything.
* **Z-Report / Shift Close:** cashier counts and enters actual physical cash; system compares **Expected Cash vs Actual Cash** and reports Short / Over / Exact, generating a signed closing report. This becomes the definitive daily reconciliation record for the Owner.

---

## C9. Double-Entry Accounting Engine (NEW — the true ERP backbone)

**Why this is critical:** Without a real double-entry engine, this software is a "sales register with reports," not an ERP. Every financial event must automatically produce balanced journal entries — the Owner should never have to do bookkeeping twice.

### C9.1 Journal Engine
* Every transaction generates a **Journal Entry** with multiple **Journal Lines**, each line carrying account, debit amount, credit amount, currency, store, and reference to the source document.
* **Hard rule enforced by the system:** total debits must always equal total credits — an unbalanced entry can never be saved.
* Journal entries are **immutable once posted**; corrections are made by reversing entries, never by editing history.

### C9.2 Automatic Posting Rules (examples the system must handle without human input)

**Retail sale of SAR 1,150 (SAR 1,000 goods + SAR 150 VAT), cash:**
```
Dr  Cash                        1,150
    Cr  Sales Revenue                   1,000
    Cr  Output VAT Payable                150
```
Simultaneously, cost of goods must post:
```
Dr  Cost of Goods Sold            600
    Cr  Inventory                         600
```

**Purchase from supplier on credit:**
```
Dr  Inventory
Dr  Input VAT Receivable
    Cr  Accounts Payable
```

**Customer return (credit note):** all of the above reverse — revenue reversed, Output VAT reversed, inventory restored, COGS reversed, and either cash refunded or customer credit balance increased.

**Expense paid in cash:**
```
Dr  Expense Account (e.g. Rent)
Dr  Input VAT (where recoverable)
    Cr  Cash / Bank
```

**Salary payment, investment injection, asset purchase, depreciation, stock write-off, inter-account transfer** — each has its own defined, configurable posting rule.

### C9.3 Financial Statements Produced Automatically
* **General Ledger** (per account, with running balance)
* **Trial Balance** (all accounts, debit/credit totals must match)
* **Income Statement / Profit & Loss**
* **Balance Sheet** (Assets = Liabilities + Equity, always)
* **Cash Flow Statement**
* **Retained Earnings** roll-forward
* **Sub-ledgers** reconciled to control accounts (AR sub-ledger must equal Accounts Receivable control account; AP sub-ledger must equal Accounts Payable; Inventory sub-ledger must equal Inventory account)

### C9.4 Multi-Currency Accounting
* Transactions can be recorded in a foreign currency while the books are maintained in the base currency, with automatic **exchange gain/loss** posting on settlement.

## C10. Fiscal Period & Year-End Closing (NEW)

* **Fiscal year** definition (calendar year, or a custom fiscal year for international clients).
* **Accounting periods** (monthly) with explicit states: **Open → Closed → Locked**.
```
2026
├── Jan  ✓ Closed
├── Feb  ✓ Closed
├── Mar  ✓ Closed
├── ...
└── Aug  ● Open
```
* Once a period is closed, **no transaction can be created, edited, or deleted in that period** — this is what makes financial statements trustworthy.
* **Reopening a closed period** requires an explicit Owner-level permission plus a mandatory reason, and is permanently audit-logged.
* **Year-end closing routine:** verify trial balance, post adjusting entries, close revenue and expense accounts into Retained Earnings, roll balances forward, generate the closing financial statement pack, and lock the year.
* **Opening balance management** for a business's very first period, and for each new fiscal year.
* **Accounting adjustments / manual journal entries** — permission-gated, reason-required, fully audit-logged.

## C11. Bank Reconciliation (NEW)

**What it does:** Proves that what the software says is in the bank is actually what the bank says.

```
Bank Statement (CSV / Excel / OFX import, or bank API feed later)
        ↓
Import & Parse
        ↓
Auto-Match against system transactions (by amount, date, reference)
        ↓
Manual Match for the remainder
        ↓
Reconcile & Lock
        ↓
Difference / Exception Report
```
* Handles: unmatched system transactions, unmatched bank lines, bank fees and charges not yet recorded, interest received, card settlement batches, payment gateway settlement batches, bounced cheques.
* Produces a **Reconciliation Statement** per bank account per period, with a locked reconciled balance.

## C12. Payment Settlement & Gateway Reconciliation (NEW — critical for card-heavy Saudi retail)

**The problem this solves:** A customer pays SAR 1,000 by card, but the bank deposits only SAR 985 two days later, because the processor took SAR 15. Without this module, the books never balance and the Owner never knows their real card cost.

* System tracks each payment through its full lifecycle:
```
Gross Payment      = SAR 1,000
Processing Fee     = SAR    15   → posts to "Bank/Card Charges" expense
Net Settlement     = SAR   985   → posts to Bank on settlement date
Settlement Status  = Pending → Settled → Reconciled
```
* **Settlement batch reconciliation:** match a bank deposit covering many transactions back to the individual sales that produced it.
* Per-payment-method fee configuration, so the Owner can see the true cost of accepting Mada vs credit card vs BNPL, and the effect on net margin.
* Handles **chargebacks and refund reversals** as their own accounting events.
* Tracks **settlement timing** (T+1, T+2 etc.) so the Owner knows what money is "collected but not yet in the bank."

## C13. Inventory Costing & COGS Engine (NEW — links stock to accounting properly)

* Costing methods, configurable per tenant: **Weighted Average Cost (WAC)**, **FIFO**, **Standard Cost** (with variance tracking), plus **Landed Cost** allocation on top of any method.
* On every sale, the system computes **COGS at the moment of sale** using the chosen method and posts it immediately — so gross profit is accurate in real time, not calculated at month-end.
```
Sale recorded
   ↓
COGS calculated (WAC / FIFO)
   ↓
Inventory quantity + value reduced
   ↓
Gross Profit = Revenue − COGS (live)
```
* **Inventory valuation report** must always tie exactly to the Inventory account balance in the General Ledger — any divergence is flagged as an exception.
* **Cost adjustment / revaluation** entries (for write-downs, damaged goods, obsolete stock) with mandatory reason and approval.
* **Negative stock policy:** configurable — block the sale, or allow with a warning and auto-correct cost on the next receipt.

## C14. Accounting-Aware Returns, Exchanges & Credit Notes (NEW)

A return is never just "put the item back on the shelf." Every return must simultaneously:
1. Reverse inventory (stock quantity + value restored)
2. Reverse revenue
3. Reverse **Output VAT**
4. Reverse **COGS**
5. Settle the customer refund (cash, card reversal, store credit, or reduction of outstanding due)
6. Reverse or adjust any **loyalty points** earned on the original sale
7. Reverse any **sales commission** attributed to the original sale
8. Generate a properly linked **Credit Note** referencing the original invoice
9. Write the complete journal entry and audit record

Partial returns must handle proportional VAT, proportional discount allocation, and proportional COGS — this is a common place where cheaper POS software silently produces wrong numbers.


---

# PART D — INTELLIGENCE & COMMUNICATION

## D1. Reporting Suite

* **Financial:** Real-time Profit & Loss (gross + net), Balance Sheet, Trial Balance, Cash Flow Statement, Daily/Monthly/Yearly Expense Summary.
* **Sales:** Daily/Weekly/Monthly/Yearly/Hourly Sales, Store-wise, Employee-wise, Category-wise, Brand-wise, Product-wise, Customer-wise, Payment-method-wise, Auto Daily Sales Report (scheduled).
* **Tax/VAT:** Sell VAT Report, Yearly Sell VAT Report, Output VAT vs Input VAT, Taxable/Zero-Rated/Exempt Sales breakdown, VAT period summary matching ZATCA filing needs (see E2).
* **Inventory & Procurement:** Stock Valuation (FIFO & Weighted Average), Stock-In/Stock-Out, Minimum-Stock-Out Report, Purchase Reports, Supplier Return Reports.
* **Customer/Supplier:** Customer Ledger, Supplier Ledger, Receivable/Payable Reports.
* **HR/Payroll:** Employee Salary Report, Commission Report, Attendance Report.
* **User Log / Audit:** who did what, when — see D4.
* **Custom Report Builder:** Owner selects filters (date, store, category, brand, employee, customer, supplier, payment method) and chooses which columns to include; saved reports can be reused.
* **Automated/Scheduled Reports:** daily sales, daily expense, weekly/monthly P&L, stock, VAT, supplier payable, customer due — auto-delivered by email/in-app/PDF/Excel on a schedule.
* All heavy reports generate **asynchronously** in the background so the POS/dashboard never lags (see A2 principle #8).

## D2. Business Analytics & Forecasting

* **Fast-Moving Items** — products that sell out almost as soon as they arrive.
* **Slow-Moving & Dead Stock Analysis** — no sales in a configurable period (30/60/90/180 days), so it can be discounted and cleared deliberately.
* **Reorder Prediction** — estimated date stock will hit the reorder point, based on sales velocity.
* **Sales Forecast** — historical-sales-based demand estimate (architecture-ready for future ML models).
* **Category/Brand/Product Profitability** — graphical ranking of true net-profit contribution.
* **Business KPIs on the Owner dashboard:** Revenue, Gross Profit, Net Profit, Average Order Value, Units per Transaction, Gross Margin %, Inventory Turnover, Customer Lifetime Value, Repeat-Customer Rate, Sales per Employee, Sales per Store, Discount Ratio, Return Rate.
* **Promotional Campaign ROI Analytics** (ties to B9).

## D3. Notification Center

* **Triggers:** low stock, new online order, new purchase request pending approval, payment due, supplier payment due, customer due exceeding limit, expense pending approval, stock transfer status, **failed ZATCA submission (critical)**, backup failure, suspicious login, expiring warranty, expiring product batch, Iqama/ID expiry for staff.
* **Channels:** in-app, email, SMS, push notification (PWA), WhatsApp Business integration where technically/legally available.
* **SMS System:** invoice notification, payment-due reminders, order confirmation, delivery updates, promotional campaigns, OTP — with marketing messages gated by opt-in/consent (PDPL requirement, see E4).
* **Email System:** welcome email, password reset, invoice copy, payment receipt, order confirmation, approval notifications, scheduled reports, system alerts.

## D4. Audit Trail & Activity Log

* Every sensitive action is recorded: price changed, product deleted/deactivated, invoice created, refund issued, discount applied, stock adjusted, user permission changed, bank transfer made, expense created, employee salary changed, login/logout, Owner-recovery events.
* Each log entry captures: **Who, What, When, Where (IP/device), Before-value, After-value.**
* Logs are **append-only** — cannot be edited or deleted by any user, including Owner, to preserve evidentiary integrity.
* **Live Activity Feed:** a real-time human-readable stream on the Owner dashboard (e.g., "Ahmed created invoice #1023," "Sara changed a product price," "Omar refunded invoice #991").

## D5. Approval Center

* One central inbox for everything awaiting sign-off: Purchase requests, Expenses, Refunds, Manual discounts, Stock adjustments, Stock transfers, Salary changes, Permission changes.
* Owner or an authorized manager approves/rejects directly from this screen, with reason capture on rejection.

## D6. Document Management

* Central, searchable storage for attachments tied to: purchase invoices, expense receipts, supplier documents, customer documents (ID copies for installment sales — PDPL-controlled access), employee documents, warranty documents, asset documents.
* Supported formats: PDF, JPG, PNG, common document types.

## D7. Global Search & Command Center

* One search box finds anything the logged-in user is authorized to see: product, SKU, barcode, customer, supplier, invoice, order, employee, serial number, transaction ID.
* **Keyboard-first Command Menu** for power users/cashiers: quickly jump to "Create Sale," "Create Expense," "Open Reports," "Open Inventory," etc., without touching the mouse.

---

# PART E — SAUDI ARABIA LEGAL, TAX & PAYMENT COMPLIANCE

*Researched against current regulatory sources as of August 2026 (see Part N for the source hierarchy).*

* This is the section that makes the software genuinely sellable in Saudi Arabia — it must be built as a dedicated compliance subsystem, not bolted onto POS logic at the end.*

---

## E1. ZATCA Phase 2 E-Invoicing Engine ("Fatoora")

**Current status (critical for planning):** Saudi e-invoicing rolls out in successive "waves" defined by taxable revenue. **Wave 24 criteria covered taxpayers whose VAT-taxable revenue exceeded SAR 375,000 during 2022, 2023, or 2024**, with an integration deadline of 30 June 2026 — the same date the penalty-waiver initiative ended.

**Precise, legally-safe framing (use this wording, not a broader claim):**

> *Saudi VAT-registered clients that fall within an applicable ZATCA Phase 2 wave must have the required integration capability in place by the deadline ZATCA notifies to them individually.*

Obligation is **per-taxpayer and notification-driven** — ZATCA notifies each targeted taxpayer directly, generally at least six months ahead, and the taxpayer confirms their own wave in the E-Invoicing section of their ZATCA portal profile. The software must therefore **never assume** a tenant's obligation status; it must **capture it as configuration** during onboarding:

```
Tenant Compliance Profile
├── VAT registered?          Yes / No
├── VAT registration number
├── ZATCA wave assigned       (as notified to the taxpayer)
├── Integration deadline      (as notified)
└── Current onboarding status Not started / Compliance CSID / Production CSID / Live
```

Because the thresholds have descended to a level that captures most established retail businesses, Phase 2 must be built as a **day-one core capability** rather than a later add-on — but the *statement made to any individual client about their obligation* must come from their own ZATCA notification, never from this document or from a sales assumption.

### E1.1 Invoice Types the System Must Handle Separately
ZATCA treats these as distinct document types with different rules — the system must model them separately, not as variations of one "invoice":

| Document | Model | Notes |
|---|---|---|
| **Standard Tax Invoice** (B2B) | **Clearance** — must be sent to ZATCA and cleared *before* being given to the buyer | Requires full buyer details including buyer VAT number |
| **Simplified Tax Invoice** (B2C — the normal retail receipt) | **Reporting** — issued to customer immediately, reported to ZATCA within 24 hours | This is the main flow for a showroom POS |
| **Credit Note** | Follows the type of the invoice it corrects | Must reference the original invoice |
| **Debit Note** | Follows the type of the invoice it corrects | Must reference the original invoice |
| **Self-billing / third-party invoicing** | Special handling where applicable | Configurable per tenant |

### E1.2 Technical Requirements (mandatory, no exceptions)
* **XML format:** every e-invoice generated in **UBL 2.1 XML**, or **PDF/A-3 with the XML embedded**. Plain PDFs, scans, or documents produced by word processors are explicitly non-compliant.
* **UUID:** a 128-bit globally unique identifier per invoice.
* **ICV (Invoice Counter Value):** strictly sequential, **non-resetting** counter per device/EGS — makes deleted or missing invoices immediately detectable.
* **PIH (Previous Invoice Hash):** each invoice embeds the SHA-256 hash of the previous one, forming a tamper-evident **hash chain**.
* **Cryptographic Stamp:** ECDSA digital signing using **CSID** credentials obtained during ZATCA onboarding, private keys stored in a secure vault (never in code, never in a plain `.env`).
* **QR Code:** Base64 **TLV-encoded** QR containing seller name, VAT number, timestamp, total with VAT, VAT amount, and (Phase 2) invoice hash, cryptographic stamp, and public key.
* **CSID lifecycle:** compliance CSID → production CSID onboarding, renewal, and revocation handled inside the system as an ongoing process, not a one-time manual setup.
* **Fatoora integration:** authentication, submission (clearance or reporting), response parsing, status tracking, and a **retry queue** — a failed submission must never be silently dropped; it retries automatically and raises a **critical alert**.
* **Invoice status pipeline:** `Draft → Generated → Signed/Stamped → Submitted → Cleared/Reported → Accepted`, with a visible `Failed → Retry Queue → Retry` path.
* **Multi-terminal / multi-branch:** ZATCA's model supports both centralized-server and branch-based POS architectures with device-level CSID — matching this platform's multi-store, multi-terminal design.
* **ZATCA SDK validation** must be part of the development and QA pipeline. Important framing: passing SDK validation is a **technical self-check, not ZATCA approval** — the software should be described as "supporting ZATCA requirements," never as "ZATCA-certified," unless formally onboarded and approved.

### E1.3 Offline POS + ZATCA — LOCKED BUSINESS RULES

**Status: DECIDED. This is no longer an open system-design question.** The previous version left the interaction between offline selling and B2B clearance unresolved; that ambiguity is closed here, because it determines database schema, invoice state machine, and POS UI behaviour, and cannot be left to a developer at implementation time.

#### The core distinction
ZATCA treats the two invoice types differently, and so must the software:

| | **B2C — Simplified Tax Invoice** | **B2B — Standard Tax Invoice** |
|---|---|---|
| ZATCA model | **Reporting** — submit *after* issuance (within 24 hours) | **Clearance** — must be cleared by ZATCA *before* the document is handed to the buyer |
| Can it complete fully offline? | **Yes** | **No** — clearance requires connectivity |
| Typical use | Walk-in showroom customer | Corporate/VAT-registered buyer who needs the invoice for their own input VAT |

#### RULE 1 — B2C Simplified Invoice, offline: PERMITTED, sale completes normally
```
Sale rung up offline
  → ICV assigned locally (sequential, non-resetting, per device)
  → PIH chained to previous local invoice
  → Cryptographically stamped LOCALLY using the device's CSID
  → QR generated LOCALLY (contains hash + stamp)
  → Receipt printed and given to customer immediately ✔ legally deliverable
  → Invoice state = SIGNED_PENDING_REPORT
  → On reconnect: reported to Fatoora → state = REPORTED
```
This is fully permitted because the B2C model is *report after issuance*. **The customer never waits, and the sale never blocks.** This is the normal showroom flow and must be the fastest path in the entire application.

*Design consequence:* the device's CSID private key and the local ICV/PIH chain must exist on the terminal itself, not only in the cloud. Signing is a **local** operation. This is a hard architectural requirement for the Tauri desktop POS.

#### RULE 2 — B2B Standard Invoice, offline: CANNOT be delivered until cleared
A Standard Tax Invoice has no legal standing for the buyer until ZATCA clears it. The system must therefore **never print or email a "final" B2B tax invoice while offline**. Three permitted behaviours, selectable per tenant in settings:

**Option A — Block (default, safest):**
POS refuses to finalize a B2B Standard Invoice while offline. Cashier sees: *"Standard tax invoice requires internet connection. Save as Draft, or issue as Simplified Invoice if the buyer does not require a VAT invoice."*

**Option B — Draft & Hold (recommended for most retail clients):**
```
B2B sale created offline
  → saved as DRAFT (no ICV consumed, no stamp, no legal document produced)
  → goods may be released only if tenant policy allows, against a
    clearly-labelled "Delivery Note — NOT A TAX INVOICE" document
  → on reconnect: invoice generated → cleared by ZATCA → THEN issued to buyer
    (emailed/printed automatically, or collected later)
```

**Option C — Convert to Simplified:**
If the buyer does not actually need a Standard invoice (many walk-in purchases by small businesses do not), the cashier converts the sale to a Simplified Invoice and Rule 1 applies. The POS should offer this as a one-tap option rather than leaving the cashier stuck.

#### RULE 3 — Document delivery timing (explicit)
| Scenario | What the customer receives, and when |
|---|---|
| B2C, online or offline | Full valid simplified tax invoice with QR — **immediately** |
| B2B, online | Cleared Standard Tax Invoice — **immediately** (after clearance round-trip) |
| B2B, offline, Option B | Delivery Note only at the counter; **cleared tax invoice issued after reconnection** |
| B2B, offline, Option A/C | No B2B document issued; either blocked or converted to Simplified |

#### RULE 4 — Never break the counter chain
* ICV is consumed **only** when a legally-issued invoice is generated. **Drafts never consume an ICV.** This is what prevents gaps in the counter — a gap is exactly what ZATCA's tamper-detection looks for.
* If a signed offline invoice is later **rejected** by ZATCA, the system must **not** delete it or reuse its ICV. It stays in the chain, is flagged `REJECTED`, raises a critical alert, and is corrected via a Credit Note — same as any other correction.
* The hash chain must be submitted **in ICV order**. The sync engine must preserve ordering per device, not submit in arrival order.

#### RULE 5 — Multi-device chain isolation
Each terminal maintains its **own** ICV sequence and its **own** PIH chain, tied to its own CSID. Chains are **never** merged across devices, and a device's chain is never re-sequenced by the cloud. A new terminal starts a new chain at ICV 1 with its own CSID.

#### RULE 6 — Extended outage handling
* No cap on offline B2C invoice count — the POS must survive a multi-day outage.
* Escalating warnings as the unsubmitted queue ages (e.g. >12h notice, >24h warning, >72h critical), surfaced to the Owner **and** visible to you in the Super Admin compliance watch, since a client sitting on thousands of unreported invoices is a legal problem you want to catch before they do.
* Reporting deadline for simplified invoices is tight (currently within 24 hours), so the alerting thresholds above are deliberately conservative.

**Verification requirement:** the exact clearance/reporting timing rules, and whether any offline tolerance exists for Standard invoices, must be confirmed against ZATCA's current official **E-Invoicing Detailed Guideline** and the **Security Features Implementation Standard** before the invoice state machine is finalized in system design. If ZATCA's current rules differ from the assumptions above, **ZATCA's rules win and this section is amended** — but the software must still implement *one explicitly decided rule per scenario*, never an undefined behaviour.

### E1.4 Invoice Immutability
* Once finalized, an invoice can **never** be edited or deleted by anyone — not even the Owner or Super Admin.
* Corrections happen only through linked Credit/Debit Notes. ZATCA explicitly prohibits tampering with e-invoices or logs, and prohibits running multiple/parallel invoice sequences.

### E1.5 Bilingual & Presentation
* Full **Arabic + English** with correct **RTL layout** on receipts, invoices, and customer-facing screens — a legal expectation and a market necessity.

---

## E2. Saudi Tax Engine — Full Detail

### E2.1 VAT Registration Rules (drives onboarding logic)
* **Mandatory VAT registration:** annual taxable turnover above **SAR 375,000**.
* **Voluntary registration:** permitted between **SAR 187,500 and SAR 375,000** (lets the business reclaim input VAT).
* The onboarding wizard should ask for turnover and guide the client to the correct registration status, storing the VAT registration number (TRN) as a validated, required field for any VAT-registered tenant.

### E2.2 VAT Rates & Categories
* **Standard rate: 15%** — but stored as a **configurable, centrally-updatable value**, never hard-coded, so a future rate change is a configuration update rather than a code release across every tenant.
* Full support for tax treatments per product/line: **Standard-rated · Zero-rated · Exempt · Out-of-scope · Export · Reverse-charge · Import**.
* **Tax exemption reason codes** stored per transaction (ZATCA requires the reason to be identified on the invoice for non-standard treatment).
* **VAT-inclusive pricing as the default display mode** (standard Saudi retail practice — the shelf price already includes VAT), with the engine correctly back-calculating the net and tax components.

### E2.3 VAT Return Preparation & Filing Support
* **Filing frequency logic built in:** businesses with annual taxable supplies **above SAR 40 million file monthly**; smaller businesses file **quarterly**. The system should set the tenant's VAT period accordingly and allow it to be changed (noting that ZATCA approval is required to switch filing periods).
* **Deadline:** returns and payment are due by the **last day of the month following the end of the tax period** — the system should generate an automatic countdown/reminder for each tenant's next filing deadline.
* **Nil returns:** the system must be able to produce a return even for a period with no transactions, since filing is mandatory regardless of activity.
* **VAT Return Preparation Report** structured to map directly onto the ZATCA return form: Output VAT, Input VAT, taxable sales, zero-rated sales, exempt sales, imports, adjustments, net VAT payable or refundable.
* **Pre-filing reconciliation tool:** cross-check the VAT return against POS/sales reports, the general ledger, bank records, and submitted Fatoora data before filing — mismatches between these four is the most common source of ZATCA compliance notices.
* **Input VAT recoverability flags:** certain categories (entertainment, some vehicles, fuel etc.) have restricted input VAT recovery — the expense module should let each expense head be flagged as recoverable or non-recoverable so the return isn't overstated.
* **VAT adjustment entries** (bad debt relief, corrections, credit/debit note effects) as their own tracked transaction type.

### E2.4 Record Retention (a genuine architectural requirement, not a nice-to-have)
* Saudi law requires VAT records — tax invoices issued and received, simplified invoices, credit/debit notes, import/export and customs documents, accounting ledgers, and bank statements — to be retained for **at least 6 years**, with **longer periods for certain capital assets (up to around 11 years for real estate)**.
* Under the Law of Commercial Books, accounting records are generally expected to be maintained **in Arabic** and available for inspection.
* **What this means for the software:**
  * Invoice XML, PDF, and hash-chain data must be **archived immutably for the full retention period**, not purged with routine data cleanup.
  * The **data-retention engine (E4) must never delete a record that is under a tax retention obligation** — a "legal hold" concept must override normal retention/anonymization rules. This is a direct conflict point between PDPL deletion rights and tax retention duties, and the system must resolve it deliberately: personal data may be minimized/anonymized where legally possible, while the tax-relevant transaction record is preserved.
  * Archived records must remain **retrievable and tamper-evident** on demand for an audit — the "Audit Export Pack" below exists for exactly this.
* **Audit Export Pack:** one-click export of everything ZATCA would ask for in an audit for a chosen period — invoice XMLs, QR data, hash chain proof, ledgers, VAT returns, and bank records.

### E2.5 Other Saudi Taxes to Support
* **Zakat / Corporate Income Tax awareness:** Saudi-owned entities pay Zakat, foreign-owned entities pay corporate income tax, and mixed-ownership entities are apportioned. The software isn't a Zakat filing tool, but the Chart of Accounts and reporting must be able to **produce the financial statements a Zakat/CIT filing is built from**, and support separate ledger tracking where ownership is mixed.
* **Withholding Tax (WHT)** on certain payments to non-residents — configurable WHT rates per payment type, deducted and tracked at payment time, with a WHT report.
* **Customs duty & import VAT** on imported goods — captured through the landed-cost module (B4) and correctly split so import VAT flows to Input VAT while duty flows to inventory cost.
* **Digital marketplace VAT liability:** recent Saudi VAT amendments place VAT liability on marketplace/platform operators in defined circumstances — relevant if a tenant operates a marketplace-style storefront, so the tax engine should support "who is liable for VAT on this transaction" as a configurable attribute rather than always assuming the seller.
* **VAT grouping:** where a group of related companies registers as a single VAT group, the system should support consolidated VAT reporting across multiple tenants/entities under one VAT registration.

---

## E3. Saudi Payment Methods & Payment Compliance (FULL COVERAGE)

**Market context that drives this design:** electronic payments now account for roughly **85% of all retail transactions in Saudi Arabia** — a business that only takes cash is asking the large majority of customers to pay in a way they've largely abandoned. Payment method coverage is not a "nice extra" here; it directly determines whether the software is usable in the Saudi market at all.

### E3.1 In-Store / POS Payment Methods (must all be supported at checkout)
| Method | Notes for implementation |
|---|---|
| **Cash** | With drawer management, change calculation, denomination counting at shift close |
| **Mada** | Saudi Arabia's **national debit network** — carries the majority of domestic card transactions. **Non-negotiable.** Lower processing cost than credit cards, so must be tracked as its own tender type, not lumped into "card" |
| **Visa / Mastercard** | Credit and international cards, higher processing fee — tracked separately for true margin reporting |
| **American Express** | Where the merchant accepts it |
| **Apple Pay** | Very high adoption among Saudi iOS users; runs on underlying Mada or card rails |
| **STC Pay** | Saudi Arabia's largest digital wallet — expected by mobile-first customers |
| **Samsung Pay / other NFC wallets** | Where supported |
| **SADAD** | Saudi national bill-payment system — relevant for B2B invoice settlement |
| **Bank Transfer** | With reference/IBAN capture and reconciliation against the bank statement |
| **Cheque** | Issue, deposit, clearing, and bounce handling |
| **BNPL — Tabby / Tamara** | Buy-Now-Pay-Later carries a very large share of Saudi checkouts and materially raises average order value. Must be a first-class tender type with its own settlement and fee tracking |
| **Store Credit / Gift Card** | Internal balance, no external processor |
| **Loyalty Points redemption** | Internal, posts against the loyalty liability account |
| **Customer Due / Credit** | Posts to Accounts Receivable, subject to credit-limit rules |
| **Split payment across any combination of the above** | e.g. SAR 200 cash + SAR 300 Mada + SAR 500 Tabby on one invoice |

### E3.2 Online Checkout Payment Methods
Same list as above, minus cash, plus hosted-checkout and payment-link flows. The **minimum viable Saudi online checkout** is: **Mada + Apple Pay + STC Pay + Visa/Mastercard + at least one BNPL provider (Tabby or Tamara)**.

### E3.3 Payment Gateway Integration Architecture
* **Design principle: a gateway-agnostic payment abstraction layer.** The software must never be hard-wired to one processor — each tenant connects their own merchant account, and the platform routes through an adapter.
* **Adapters to build for the Saudi market** (all of these are SAMA-licensed providers commonly used in Saudi projects): **HyperPay, Moyasar, PayTabs, Tap Payments, Geidea, Amazon Payment Services, Checkout.com**, plus **Tabby** and **Tamara** for BNPL.
  * *Note for the build team:* provider licensing, product availability, and legal entity names change over time — the current **SAMA licensed-provider list** should be the verification source at integration time, not a blog post or this document.
* **Important compliance clarification:** the **SAMA licence sits with the payment service provider, not with the merchant and not with your software.** Your platform does not need a SAMA licence to integrate; it needs to *only integrate with SAMA-licensed providers*. This is worth stating clearly in your sales material because clients ask about it.
* **Stripe does not work as a primary Saudi gateway** (not SAMA-licensed for Mada acquiring) — the architecture must not assume a Stripe-style default.
* **PCI-DSS scope minimization:** prefer hosted checkout / tokenized flows so raw card data never touches your servers. Where a merchant demands an embedded checkout, the data flow and PCI obligations must be documented explicitly with the provider.
* **Mada sandbox testing:** every SAMA-licensed gateway provides Mada test cards that simulate Saudi-specific routing, authentication, and failure behaviour — Mada-specific testing must be a required item in the QA checklist (Part M), because Mada transactions behave differently from international card transactions.

### E3.4 Payment Terminal (Card Machine) Integration
* Support for **integrated card terminals** (amount pushed from POS to the terminal so the cashier can't mistype it, and the approval result returned automatically to close the sale).
* Support for **SoftPOS / tap-to-phone** for mobile or pop-up selling.
* Fallback **manual entry mode** where a terminal is standalone, with mandatory reference capture for reconciliation.

### E3.5 Payment Fees, Settlement & Reconciliation
Handled by the Payment Settlement module (C12), but noted here because it is Saudi-specific in practice:
* Different tenders carry very different costs (Mada is materially cheaper than international credit cards; BNPL carries a higher merchant fee) — the system must track the **fee per tender type** so the Owner sees the true net margin per payment method.
* Settlement timing differs per provider (T+1, T+2) — the Owner must be able to see "collected but not yet settled" as a distinct figure.
* Gateway settlement batches must reconcile back to individual sales (C11/C12).

---

## E4. PDPL — Privacy & Data Governance Module

**Current status:** PDPL is in **active enforcement**. The grace period ended 14 September 2024, and **SDAIA** has since issued dozens of confirmed violation decisions covering exactly the failures a badly-built POS/CRM causes: processing personal data without a valid legal basis, unauthorized disclosure, inadequate technical safeguards, and marketing messages sent without consent. Fines reach **up to SAR 5,000,000 per violation** (doubled for repeat offences), with criminal liability possible for intentional harmful disclosure of sensitive data. The law applies **extraterritorially** — any business anywhere processing the data of people in Saudi Arabia is in scope, which directly covers your SaaS platform even though you operate from outside the Kingdom.

### E4.1 Core Privacy Capabilities
* **Data minimization by design** — collect only what the business genuinely needs.
* **Lawful basis & consent tracking** — each personal-data field traceable to a legal basis (contract, consent, legal obligation, legitimate interest). **Marketing consent must be recorded separately from transactional consent**, with timestamp, channel, and proof — this is the single most commonly enforced violation.
* **Data classification** — every field tagged (Public / Internal / Personal / Sensitive Personal) so protection rules can be applied automatically.
* **Data Subject Rights workflow** — access, export, correction, deletion/anonymization, objection, and portability requests, with **enforced response SLAs**: the Implementing Regulations require controllers to act on a data-subject request within **30 days**, extendable by a further **30 days** where the request requires unusual additional effort or the subject has submitted multiple requests. The system must track the clock, warn before breach of the deadline, log every request and its outcome, and store the SLA values in the Regulatory Rule Registry (E8) rather than hard-coded.
* **Record of Processing Activities (RoPA)** — producible on short notice; SDAIA audits have specifically requested these.
* **Retention & destruction engine** — configurable retention per data category, with scheduled archive → anonymize/pseudonymize → destroy, and a permanent **destruction log** proving what was deleted and when.
* **Legal hold override** — as noted in E2.4, tax and commercial-record retention obligations (6+ years) **override** a routine deletion request; the system must resolve this conflict explicitly rather than blindly deleting.
* **Breach/incident management** — timestamped incident record, severity assessment, and a notification workflow enforcing the **72-hour deadline to notify SDAIA from the moment of becoming aware** of a breach that may harm personal data or data-subject rights, plus notification to affected data subjects without undue delay where the risk is high. Notification content must capture: nature of the incident and how it occurred, categories and estimated number of affected individuals, assessed consequences, and containment/remediation measures taken. **A 72-hour countdown timer starts automatically the moment an incident is logged** — this is a short window and cannot depend on someone remembering it.
* **DPO designation** — allow assigning an internal or external Data Protection Officer per tenant.
* **Processor records** — you (as platform operator) act as a **data processor** for every tenant. The platform therefore needs its own PDPL posture: data processing agreements with each tenant, documented security controls, and sub-processor records for any cloud/SMS/email vendor you use.

### E4.2 Data Residency & Cross-Border Transfer (NEW — affects your infrastructure decision directly)
Saudi rules place specific conditions on transferring personal data outside the Kingdom, and SDAIA publishes a dedicated Regulation on Personal Data Transfer Outside the Kingdom. Because of this, **do not force every client onto one global database**:

```
Tenant
  ↓
Data Region (Saudi Arabia │ Europe │ Asia │ Other)
  ↓
Database instance + Object storage in that region
```
* **Per-tenant data region configuration** set at onboarding, with Saudi-region hosting available for clients whose compliance strategy requires it.
* **Transfer safeguards** documented per region where data moves cross-border. Note that Saudi rules generally require either transfer to a jurisdiction SDAIA has determined provides an adequate level of protection, or an appropriate authorization/safeguard route — this must be confirmed against the current Transfer Regulation (Part N, item 10) before the hosting architecture is finalized.
* **SDAIA controller registration** — tenants may be required to register as data controllers on SDAIA's platform; the compliance settings should capture the tenant's registration reference so it can be evidenced during an audit.
* Commercially this is also a **selling point** — enterprise Saudi clients will ask "where is my data stored?", and "in-Kingdom, if you want it" is a far stronger answer than "somewhere in the cloud."

---

## E5. Saudi E-Commerce Law & Online Store Compliance

For the Online Order module (B13) to be legal in Saudi Arabia:
* **Registration:** any business selling online — website, app, or even a social media account — is an e-commerce "service provider" under the E-Commerce Law and must be registered with the **Ministry of Commerce**. This was historically done through the **Maroof** platform; more recent guidance points to registration/verification consolidating under the Ministry's **Business Platform (business.sa)** run by the Saudi Business Center. Because this channel has been actively transitioning, treat the registration identifier and verification badge as a **configurable per-tenant field**, so it can point to whichever channel is current without a code change.
* **Mandatory storefront disclosures** (must render automatically from tenant settings, in Arabic): Commercial Registration (CR) number, VAT number, business contact details, VAT-inclusive pricing, written return/refund policy, and delivery terms.
* **Consumer protection rules to enforce operationally:**
  * **14-day cooling-off / return right** for most categories (configurable per product/category, since some categories are legally exempt).
  * **No hidden fees** — delivery, VAT, and service charges all itemized before payment.
  * Accurate, non-misleading product descriptions including price, specification, warranty, and return terms.
* **Penalties are material** — reported ranges run into hundreds of thousands of riyals up to around SAR 1 million depending on the violation, plus possible suspension or blacklisting. This justifies making these disclosures a **required, validated onboarding step**, not an optional settings field a client can skip.

---

## E6. Saudi Labour & Payroll Compliance (expanded)

* **WPS via Mudad** — the Wage Protection Program operates through the Mudad platform under MHRSD, and coverage has expanded across private-sector establishments including small ones. The system generates the required electronic wage file and supports the submission timing and payment-window rules.
  * **Treatment in the build:** the **wage-file format/version, submission lead time, and salary payment window are Regulatory Rule Registry entries (E8), not hard-coded logic.** The values noted in earlier drafts (XML-based format, submission ahead of payday, payment within the early part of the month) are directionally correct but **must be locked against the live Mudad specification before the payroll module ships** — file formats in this area change without public announcement. Build the generator against a versioned format definition so a format change is a registry update plus a template swap, not a rewrite.
* **Cross-platform data consistency:** Mudad cross-references wage data against **GOSI** (social insurance) and **Qiwa** (contract registration). A mismatch between an employee's Qiwa contract, GOSI wage, and actual bank transfer gets flagged automatically and can freeze portal access. The employee master record must therefore hold Iqama/National ID, GOSI registration, Qiwa contract reference, and bank/IBAN details, with a built-in **consistency check** before file generation.
* **GOSI contribution engine** — rates differ between Saudi nationals and expatriates, differ between employees hired before vs after the new Social Insurance Law took effect in July 2024, and are on a legislated upward path. **All rates come from the Regulatory Rule Registry (E8) as a dated schedule — never hard-coded, never a single 'current' figure.** Payroll must calculate using the rate in force for the pay period being processed, so re-running an old month produces the historically correct figure.
* **End-of-Service Benefit (EOSB) accrual** — Saudi labour law entitles employees to end-of-service gratuity. The system should **accrue this liability monthly** rather than discovering it at termination, so the balance sheet is honest and the Owner isn't blindsided by a cash-flow shock.
* **Saudization / Nitaqat awareness** — payroll compliance feeds Nitaqat ratings; the system should be able to report the Saudi vs non-Saudi employee ratio.
* **Iqama and work permit expiry alerts** — already in C5, but repeated here because lapsed documents are a common source of penalties.
* **Framing:** position the module as **"WPS/Mudad-ready"** — the software prepares compliant wage data, but final legal responsibility for submission remains the employer's.

---

## E7. Compliance Monitoring Dashboard

A single **Compliance** screen answering "am I legally exposed right now?":
* ZATCA onboarding/CSID status per branch and per terminal
* Count of invoices pending / failed / retrying submission, with one-click drill-down
* Current VAT rate in effect and the tenant's next VAT filing deadline with countdown
* VAT return reconciliation status (do POS, ledger, bank, and Fatoora agree?)
* PDPL posture summary — consent coverage, pending data-subject requests, retention job status, open incidents
* E-commerce disclosure completeness (CR number, VAT number, return policy present or missing)
* WPS/Mudad submission status and next deadline
* Employee document expiry warnings (Iqama, work permits)
* Record retention/archive health (is the 6-year archive intact and retrievable?)

---

---

## E8. Regulatory Rule Registry — Every Legal Parameter Is Versioned Data, Never Code

**Why this exists:** Saudi regulatory parameters change on their own schedule — VAT rates, GOSI contribution rates, wage-file formats, filing thresholds, PDPL response deadlines. If any of these are written into source code, every change becomes an emergency release across every tenant. If they are versioned data with effective dates, a change is a configuration update Super Admin applies centrally.

**Hard rule: no legal figure, deadline, threshold, rate, or file format may be hard-coded anywhere in the codebase.** All of them live in this registry.

### E8.1 Registry Structure
Every rule is stored with an **effective date range** and a **source reference**, so the system can always compute what the law required *at the date of the transaction* — essential when re-generating a report or defending an audit covering an earlier period.

```
RegulatoryRule
├── rule_key            (e.g. "SA.VAT.STANDARD_RATE")
├── country
├── value / payload     (rate, threshold, format spec, SLA days)
├── effective_from
├── effective_to        (null = currently in force)
├── source_authority    (ZATCA / SDAIA / GOSI / MHRSD / SAMA / MoC)
├── source_document     (official doc name + version/date)
├── verified_on         (date a human last checked it against Tier 1)
└── verified_by
```

**Historical accuracy requirement:** a VAT report for March must use March's rate, even if the rate changed in June. Never apply the current value retroactively.

### E8.2 Rules That MUST Live in the Registry (not exhaustive)

| Domain | Registry entries |
|---|---|
| **VAT** | Standard rate (currently 15%) · mandatory registration threshold (SAR 375,000) · voluntary threshold (SAR 187,500) · monthly-vs-quarterly filing threshold (SAR 40M) · filing due-date rule · retention period (6 years, extended for defined assets) |
| **ZATCA** | Simplified-invoice reporting window (currently 24h) · Standard-invoice clearance rule · XML schema version · QR TLV field set · hash algorithm · CSID renewal interval · wave definitions |
| **GOSI** | Employer rate · employee rate · **split by Saudi national vs expatriate** · **split by pre- vs post-July-2024 hire (new Social Insurance Law)** · contribution wage cap · the scheduled step-increases running through 2028 — each as its own dated row |
| **WPS / Mudad** | **Wage-file format specification and version** · submission lead time before payday · salary payment window · required employee identifier fields |
| **PDPL** | **Data-subject request response deadline (30 days, extendable by a further 30 where the request requires unusual effort or the subject has submitted multiple requests)** · **breach notification deadline to SDAIA (72 hours from becoming aware of a breach that may harm personal data or data-subject rights)** · retention defaults per data category · cross-border transfer conditions |
| **Labour** | EOSB accrual formula · leave entitlements · overtime multipliers |
| **E-Commerce** | Cooling-off period (14 days) · category exemptions · required storefront disclosure fields |
| **Withholding Tax** | Rates by payment type and recipient status |

### E8.3 Governance Around the Registry
* **Super Admin only** may edit a registry entry; every change is permanently audit-logged with before/after values and the source document cited.
* **Effective-dated rollout:** a rate change entered today with an effective date of next month applies automatically on that date across all affected tenants — no deployment, no per-tenant work.
* **Staleness alerting:** each entry carries a `verified_on` date. If an entry has not been re-verified against its Tier 1 source within a configurable period (suggested: 6 months for tax and payroll, 12 months for others), Super Admin gets a **review reminder**. This is the operational mechanism that keeps the platform legally current instead of quietly drifting.
* **Per-tenant override with justification:** rare, but supported (e.g. a tenant with a ZATCA-approved special arrangement) — always requiring a written reason and audit entry.

### E8.4 Specific Flags Requiring Verification Before First Release
These three are singled out because they are the most volatile and the most damaging if wrong:
1. **Mudad wage-file format** — file layouts change without publicity. Must be pulled from the live Mudad specification immediately before build, and re-checked before each payroll-module release.
2. **GOSI rate schedule** — currently on a legislated upward path; must be entered as a **complete dated schedule**, not a single current figure.
3. **ZATCA XML/QR technical spec version** — must match the schema version ZATCA currently accepts, and the system should record which schema version each archived invoice was generated under.

---

**Important framing note (must be honoured in marketing and in the product):** Everything in Part E is a **product requirement list**. The actual byte-level implementation of ZATCA XML, hash chain, QR payload, and CSID handling must be validated at system-design time against ZATCA's official current **E-Invoicing XML Implementation Standard, Security Features Implementation Standard, and Data Dictionary** — those official documents, not this summary, are the binding source of truth. Likewise, PDPL implementation should be reviewed against SDAIA's official Implementing Regulation. The software should be described as **"built to support ZATCA and PDPL requirements,"** never as "certified compliant," and a Saudi tax advisor and legal counsel should review the implementation before go-live.


---

# PART F — CONFIGURABLE WORKFLOW ENGINE & EXTERNAL PORTALS (NEW)

## F1. Business Workflow / Approval Engine (NEW — makes the system enterprise-grade)

**Why this matters:** Hard-coding approval rules means every client with a slightly different process needs custom development. A configurable engine means one codebase serves a 3-person shop and a 300-person chain.

The Owner defines rules visually, without a developer:

```
IF  Expense > SAR 5,000
    → Manager Approval
    → Accountant Approval
    → Owner Approval
```
```
IF  Discount > 20%
    → Manager Approval (PIN at POS, or async approval)
```
```
IF  Stock Adjustment > 50 units
    → Inventory Manager
    → Owner
```
```
IF  Purchase Order > SAR 50,000
    → Three-Way Match required before payment release
    → Owner Approval
```

* **Triggers available:** transaction type, amount threshold, percentage threshold, product/category, store/branch, employee, customer credit exposure, time of day, quantity.
* **Actions available:** require approval (single or sequential multi-step), require a second-person PIN, block outright, allow with a logged warning, notify without blocking, escalate after a timeout.
* **Approval routing:** by role, by named person, or by hierarchy (employee's direct manager).
* **Escalation & delegation:** if an approver doesn't respond within X hours, escalate; approvers can delegate while on leave.
* Every workflow execution is recorded in the audit log with the full decision chain.
* All pending items surface in the **Approval Center** (D5).

## F2. Customer Self-Service Portal (NEW)

A lightweight, mobile-friendly portal where the tenant's own customers log in (phone + OTP):
* View and download past invoices (with ZATCA QR intact)
* View order status and delivery tracking
* Make payments against outstanding dues
* View loyalty point balance, tier, and redemption history
* View store credit / gift card balance
* Check warranty status by serial number
* Submit a return or exchange request (routes into the workflow engine for approval)
* Manage profile, saved addresses, and — importantly — their **saved sizes** for fashion retail
* Raise a support ticket
* **PDPL rights self-service:** view what data is held about them, request correction, request export, request deletion — this makes PDPL data-subject compliance largely self-serving rather than a manual burden on the shop owner

## F3. Supplier Portal (NEW)

A portal for the tenant's suppliers, which makes procurement dramatically more professional:
* View issued Purchase Orders, and **accept or reject** them with comments
* Submit quotations in response to an RFQ (feeds directly into the comparison screen, B5.1)
* Upload their invoice against a PO (feeds the three-way match, B5.2)
* View GRN records — what was actually received and accepted
* View payment status and payment history, and their current outstanding balance
* Track purchase returns raised against them
* Upload compliance documents (CR, VAT certificate, bank details) with expiry tracking

## F4. Multi-Company / Group Consolidation (NEW)

For clients who own several legal entities (common in Saudi group structures):
* One Owner login can hold multiple companies under a group.
* **Consolidated reporting** across companies (group P&L, group balance sheet), while each company keeps its own separate books, VAT registration, and ZATCA sequence.
* Inter-company transactions tracked and eliminated in consolidation.
* Supports the **VAT grouping** scenario noted in E2.5.


---

# PART G — INTERNATIONALIZATION (so the same product sells beyond Saudi Arabia)

## G1. Country Configuration Engine

**What it does:** Nothing about tax, invoicing, or language should be hard-coded to one country. Each country is a swappable configuration block:

```
Country
 ├── Currency
 ├── Tax Configuration (rate(s), invoice rules, compliance module if applicable)
 ├── Language / RTL-or-LTR
 ├── Number & Date Format
 ├── Accounting Rules
 └── Compliance Module (e.g., Saudi → ZATCA + PDPL; other countries → their own, or none yet)
```
* Saudi Arabia ships as the first, fully-built configuration (SAR, 15% VAT, ZATCA, Arabic RTL, PDPL). Bangladesh/other markets can be configured with their own tax and invoicing rules (e.g., NBR VAT rules for Bangladesh) without touching core code.
* This is what makes the platform genuinely sellable to "Saudi + international clients," as originally required — Saudi is a config, not the architecture.

## G2. Multi-Currency

* Base currency per tenant (SAR, BDT, USD, EUR, GBP, AED, others), transaction currency support, exchange-rate management, currency gain/loss tracking, currency-specific reports.

## G3. Multi-Language & RTL/LTR

* Arabic, English, Bengali at launch, with a translation-management system so more languages can be added without a code release.
* Full RTL layout mirroring for Arabic (menus, receipts, invoices, dashboards) — not just translated text sitting in an LTR layout.

## G4. Tax Templates Library

* A growing library of pre-built tax templates per country (starting with Saudi Arabia's VAT/ZATCA setup), so onboarding a client in a new country means picking a template and adjusting, not building tax logic from scratch.

---

# PART H — PLATFORM ENGINEERING & SECURITY

## H1. Security & Authentication

* **Authentication:** password + MFA/OTP, session management, device/session listing with remote revoke.
* **Authorization:** RBAC enforced server-side (Section A6), permission matrix, store-level scoping, amount-based approval limits.
* **Application security:** HTTPS everywhere, CSRF protection, rate limiting, input validation, SQL-injection and XSS protection, secure HTTP headers, signed/expiring API tokens.
* **Secrets management:** database credentials, API keys, and ZATCA CSID private keys are **never** stored in plain source code or unencrypted `.env` files in production — a proper Secret Manager/Vault/KMS is required, especially given ZATCA's cryptographic signing requirements in E1.

## H2. Offline-First Architecture & Sync Engine

**The single most important POS engineering rule: selling must never stop just because the internet stopped.**

* **Local storage (Tauri + SQLite):** products, current stock snapshot, customers, POS configuration, in-progress and completed transactions, payment records, and a secure sync queue — all available with zero internet.
* **Sync flow:** `Local SQLite transaction → Sync Queue → Go Sync Engine → Cloud PostgreSQL → (if Saudi tenant) ZATCA submission workflow`.
* **Idempotency & Deduplication:** every transaction carries a UUID generated at creation time; if that UUID already exists in the cloud, the sync engine **must not** create a duplicate — this is what makes "sell offline all day, sync at night" safe.
* **Conflict detection & resolution:** ordered events, offline timestamps, device identity, sync cursor per device, and a clearly defined resolution rule for the rare true conflict (e.g., last-write-wins with a flagged review, or manual reconciliation for financial records).
* **Failed sync queue:** anything that fails to sync is retried automatically and visible to Super Admin/Owner, never silently lost.

## H3. Device Management

* Each POS terminal registers as its own **Device**: Device ID, Store ID, Terminal ID, OS, app version, last sync time, last active time, assigned cashier, linked printer configuration.
* Owner can: activate, deactivate, revoke, rename, reassign to a different store, force an app update, and view a device's health/sync status remotely.

## H4. Backup & Disaster Recovery

* **Automatic daily encrypted backup** (AES-256) of every tenant's cloud database, with backup verification (a backup that can't be restored is not a real backup).
* **One-click manual backup** button, exactly as originally requested — usable from phone, laptop, or iPad.
* **Point-in-time recovery** where the underlying database supports it, plus a documented, periodically-tested restore procedure.
* **Backup Dashboard** for Super Admin/Owner: last backup time, status, size, any failed backup, and the most recent valid recovery point.

## H5. SaaS Subscription, Billing & Feature Flags

* **Plan tiers:** Starter / Professional / Business / Enterprise, each with configurable limits — number of stores, number of users, number of POS terminals/devices, storage quota, SMS credits, and access to advanced modules (Payroll, Wholesale, Online Orders, Advanced Analytics, ZATCA module, API access).
* **Feature Flags per tenant:** Super Admin toggles individual modules on/off per client independent of a fixed plan (e.g., a Starter client who wants Warranty Management added individually) — this is what makes the platform commercially flexible, matching the original requirement that features be selectable/customizable per client's paid service.
* Billing cycle management (monthly/yearly/lifetime), invoicing to tenants, dunning/suspension on non-payment (configurable grace period).

## H6. API & Integration Platform

* A proper **REST API + Webhooks** layer so the platform isn't a closed box.
* Integration-ready connectors for: payment gateways (SAMA-licensed, see E3.3), SMS providers, email providers, WhatsApp Business, shipping/courier providers, external accounting software, e-commerce platforms/marketplaces, banks, card terminals, and the ZATCA Fatoora platform itself.
* API keys, OAuth where applicable, rate limiting, and full API access logs.

## H7. Import / Export & Data Migration

* **Import:** Products, Customers, Suppliers, Opening Stock, Opening Balances, Employees — via CSV/Excel with field mapping and validation before commit.
* **Export:** Excel, CSV, PDF, JSON where structurally appropriate.
* **Migration wizard for clients switching from another POS:** Old system export → CSV/Excel → Field Mapping → Validation → Preview → Import → Error Report → Completed. This is a genuine sales-enablement feature — it removes the biggest reason a prospective client hesitates to switch.

## H8. System Health Monitoring (Super Admin view)

* API uptime, database health, CPU/RAM/disk usage, count of active tenants and active users, API latency, failed background jobs, failed ZATCA requests platform-wide, backup status platform-wide, sync failure counts, overall error rate.

## H9. Job / Queue System (Background Processing)

* Background workers handle: ZATCA submissions, heavy report generation, email/SMS sending, scheduled backups, recurring invoice/expense generation, notifications, data export, and sync processing — so none of these ever block a cashier's checkout screen.

## H10. Customer Support / Ticketing (Super Admin ↔ Tenant)

* Tenants can raise a support ticket (with screenshot attachment) directly from their portal; Super Admin manages a central queue with status tracking, bug/feature-request tagging, and system-wide announcements (e.g., planned maintenance).

---

# PART I — SETTINGS & CUSTOMIZATION

## I1. System / Owner Settings
* Company profile, branches/stores, tax configuration, receipt/invoice defaults, POS behavior defaults, business rules (e.g., minimum floor price enforcement on/off).

## I2. Receipt & Invoice Template Customization
* Logo, header, footer, return-policy text, tax numbers, Arabic/English content blocks, QR placement, payment information — customizable per template type: Thermal Receipt, A4 Invoice, Quotation, Purchase Order, Delivery Challan, Credit Note, Debit Note, Payment Receipt, Customer Statement.

## I3. Numbering Engine
* Configurable document numbering, separable by store, document type, country, and fiscal year, e.g. `INV-RYD-000001`, `PO-2026-000001`, `RET-2026-000001`. For any tenant under the Saudi ZATCA module, numbering must still respect ZATCA's own sequence/hash/counter requirements (Section E1) — the numbering engine and the ZATCA ICV are related but not the same thing, and the design must not let a "friendly" custom invoice number break the mandatory tamper-evident counter underneath it.

## I4. User Preferences
* Per-user: language, theme (light/dark), currency display, date format, notification preferences, dashboard widget layout, POS quick-button layout, default printer, default store.

## I5. Point / Station Settings
* Per-terminal configuration: default warehouse, linked printer/scanner/drawer, receipt template, keyboard shortcuts, default discount rule, whether customer selection is required before checkout.

---

# PART J — TECHNICAL ARCHITECTURE & STACK

## J1. Confirmed Technology Stack

| Layer | Technology | Notes |
|---|---|---|
| Web (Owner/Admin) | Next.js + TypeScript | React, Tailwind CSS, shadcn/ui |
| State/Data fetching | TanStack Query | |
| Validation | Zod | |
| Desktop POS | Tauri + React + TypeScript | Local SQLite, direct hardware access |
| Mobile | Responsive Next.js PWA | Installable, app-like; React Native/Expo only if native features become unavoidable later |
| Backend | **Go** | Framework: Chi or Echo preferred over a framework chosen purely for raw benchmark speed — this is a long-lived enterprise system |
| API style | REST + WebSocket (for live dashboard/notification updates) | |
| Primary database (cloud) | **PostgreSQL** | Tenant data, transactions, accounting, inventory, users, reports, ZATCA records |
| Local database (offline POS) | **SQLite** | Offline transaction queue, product cache, device config, sync state |
| Cache / Queue | Redis (used only where genuinely needed — sessions, rate limiting, background job queue) | |
| Object storage | S3-compatible storage abstraction | Product images, documents, invoice PDFs/XML, backups — provider-agnostic so the platform isn't locked in |
| Reverse proxy | Nginx | |
| Containers | Docker (Docker Compose initially; Kubernetes only introduced later if genuinely needed at scale) | |
| CI/CD | GitHub + GitHub Actions | Automated tests, migrations, security scanning, automated deployment |
| Monitoring | Prometheus/Grafana or equivalent | |
| Error tracking | Sentry or equivalent | |
| Secrets | KMS / Secret Manager / Vault | |
| Cryptography | Go's standard crypto libraries + the approved ZATCA signing workflow | |
| Barcode | Code128 / EAN / UPC / QR as applicable | |
| Printing | ESC/POS | |
| Production hosting | Saudi-region cloud infrastructure where a client's data-residency/PDPL strategy requires it | |
| i18n | Standard i18n framework + RTL layout support | |

## J2. High-Level Architecture

```
                              SaaS Platform
                                    │
                    ┌───────────────┴───────────────┐
                    │                                │
              Super Admin                          Tenants
                                                       │
                                     ┌─────────────────┼─────────────────┐
                                     │                 │                 │
                                   Owner            Stores            Users
                                     │
                          ┌──────────┴──────────┐
                          │                      │
                    Next.js Web             Tauri POS (Desktop)
                          │                      │
                          │                   SQLite (local)
                          │                      │
                          └──────────┬───────────┘
                                     │
                                  Go API
                                     │
             ┌───────────────────────┼───────────────────────┐
             │                       │                       │
        PostgreSQL                 Redis              Object Storage
             │
        Business Data
             │
   ┌─────────┼─────────┬────────────┬────────────┐
   │         │         │            │            │
 Sales   Inventory  Accounting     CRM      HR / Payroll
   │
   └──────────── ZATCA Compliance Engine
                        │
                    Fatoora (ZATCA)
```

## J3. Data Flow — Offline POS to Cloud to ZATCA

```
Cashier scans/sells (no internet needed)
        ↓
Local SQLite transaction (UUID assigned) + receipt printed + local stock updated
        ↓
Internet reconnects
        ↓
Sync Queue → Go Sync Engine → PostgreSQL (idempotent, UUID-checked)
        ↓
If Saudi tenant + ZATCA module enabled:
   Invoice Engine builds UBL 2.1 XML → UUID + ICV + PIH hash chain applied
        → Cryptographic stamp (CSID) → QR generated
        → Fatoora submission (Clearance for B2B / Reporting for B2C)
        → Status tracked; failure → retry queue + critical alert
```

## J4. Performance Targets

* Barcode scan → product in cart: near-instant, local lookup (no network round-trip required).
* Cart update: under 100ms locally.
* Payment completion: immediate, local-first transaction close.
* Standard dashboard/API queries: typically under 500ms.
* Heavy reports: always asynchronous, never blocking the UI.
* POS fully operational with zero internet connection at all times.

## J5. Testing Strategy

* **Unit tests** — business logic (pricing, tax, commission, discount rules).
* **Integration tests** — API + database behavior.
* **End-to-end tests** — full POS checkout workflows.
* **Offline-specific tests** — internet disconnected mid-sale, multiple offline transactions queued, reconnection behavior, duplicate-sync prevention, conflict scenarios.
* **ZATCA-specific tests** — XML structural validation, QR payload validation, hash-chain integrity, signature/security feature checks, ZATCA SDK verification pass, live sandbox API response handling.

---


---

# PART K — FINAL MODULE / NAVIGATION TREE

```text
SUPER ADMIN (You — platform owner only)
├── Platform Dashboard
├── Tenants (create / suspend / delete / data region)
├── Subscriptions & Plans
├── Feature Flags (per tenant)
├── Global Settings (countries, currencies, languages, tax templates, VAT rate)
├── Integrations (payment gateways, SMS, email, storage providers)
├── Support Tickets
├── System Health & Telemetry
├── Compliance Watch (failed ZATCA submissions across all tenants)
└── Platform Audit Log

OWNER / TENANT ERP
├── Dashboard
├── POS (Billing Counter)
│
├── Sales
│   └── Invoices │ Quotations │ Orders │ Returns │ Exchanges │ Credit Notes │ Debit Notes
│       │ Deliveries │ Recurring Invoices │ Sales Regions
│
├── Purchases
│   └── Requisitions │ RFQ & Quote Comparison │ Purchase Orders │ GRN
│       │ Purchase Invoices │ 3-Way Match │ Supplier Returns
│
├── Inventory
│   └── Products │ Variants │ Bundles │ Categories │ Brands │ Units
│       │ Stock │ Transfers │ Adjustments │ Stock Count │ Costing & Valuation
│       │ Barcode & Label Studio │ Warehouses │ Batch/Expiry │ Serial/IMEI
│
├── Customers (CRM)
│   └── Profiles & Size History │ Ledger/Due │ Loyalty │ Wallet/Gift Card │ Segments │ Portal Access
│
├── Suppliers
│   └── Suppliers │ Ledger │ Payables │ Payments │ Portal Access
│
├── Accounting
│   └── Chart of Accounts │ Journal Entries │ General Ledger │ Trial Balance
│       │ Balance Sheet │ P&L │ Cash Flow │ Fiscal Periods & Year-End Close
│       │ Cash │ Bank │ Bank Reconciliation │ Payment Settlement
│       │ Receivables │ Payables │ Expenses │ Investments │ Transfers
│
├── Employees (HR)
│   └── Directory │ Attendance │ Payroll │ WPS/Mudad Export │ GOSI │ EOSB Accrual │ Commission
│
├── Assets
├── Online Orders
├── Wholesale (B2B)
├── Warranty & Service
├── Installments (EMI)
├── Shift Management (X/Z Reports)
├── Reports
├── Analytics
├── Notifications
│
├── Compliance
│   └── ZATCA (invoices, CSID, retry queue) │ VAT Returns │ Tax Records Archive
│       │ PDPL / Privacy │ Data Residency │ E-Commerce Disclosures │ Audit Log
│
├── Workflows (approval rule builder)
├── Approval Center
│
├── Settings
│   └── Company/Branches │ Roles & Permissions │ Receipt Templates │ Numbering
│       │ Terminals │ Payment Methods & Gateways │ Users
│
└── Backup & Restore
```

---

# PART L — MASTER "NOTHING MISSING" CHECKLIST

**Foundation:** Multi-tenant isolation · Super Admin control plane · Owner account creation & Super-Admin-assisted recovery · Onboarding wizard · Predefined + custom roles · Server-enforced permissions · Store-level permission scoping · Amount-based approval limits · Data masking · Multi-store · Multi-warehouse · Multi-company group consolidation.

**Retail Core:** Product catalog · Variant matrix (size/color) · Product bundles/combos · Multi-tier pricing · Minimum floor price · Smart barcode engine · Bulk barcode generation · Hang-tag & thermal label printing · A4 barcode sheets · Stock in/out/transfer · Wastage logging · Stock audit & variance adjustment · Dead stock & fast-moving analysis · Landed cost · FIFO/WAC/Standard costing · COGS engine · Batch/expiry tracking · Serial/IMEI tracking · Negative stock policy.

**Purchasing:** Purchase requisition · **RFQ & supplier quote comparison** · Purchase order · GRN · Purchase invoice · **Three-way matching with tolerance** · Purchase return & debit note · Purchase approval workflow · Supplier ledger, payables & aging · **Supplier portal**.

**Selling:** Offline-capable POS · Barcode scanning · ESC/POS thermal printing · Cash drawer kick · Split/multi-tender payment · Hold cart · Gift/FOC items · Promotions engine (BOGO, bundle, coupon, seasonal) · Manager-PIN discount gating · Quick size exchange · Refund with credit note · Quotation → order → invoice · Delivery challan · Picking/packing slip · Recurring invoice · Sales region tracking.

**B2B & Extended:** Wholesale pricing & credit terms · Online order management · Delivery/courier workflow · Installment/EMI with guarantor records · **Customer self-service portal**.

**Post-Sale:** Warranty tracking · Replacement register · Service/repair work orders.

**CRM:** Customer profile & purchase history · Fashion size/fitting history · Due/credit ledger with credit limit · Loyalty points & tiers · Store credit/gift card · Customer segmentation · Loyalty barcode cards.

**Accounting (major expansion):** **Double-entry journal engine with enforced debit=credit** · Automatic posting rules for every transaction type · General Ledger · **Trial Balance** · **Balance Sheet** · Income Statement · **Cash Flow Statement** · Retained earnings · Sub-ledger reconciliation to control accounts · **Fiscal period open/close/lock** · **Year-end closing routine** · Opening balances · Manual journal entries (permission-gated) · **Bank reconciliation with statement import** · **Payment settlement & gateway fee tracking** · Multi-currency with FX gain/loss · Cash & bank management · Fund transfers · One-click expense tracking with drill-down · Recurring expenses · Expense approval workflow · Investment & investor tracking (separate from revenue) · AR/AP aging · **Accounting-aware returns (revenue, VAT, COGS, loyalty, commission all reversed)**.

**HR & Payroll:** Employee directory · Iqama/ID expiry alerts · Attendance & leave · Salary advance · Commission engine (flat & tiered) · Payroll processing · **WPS/Mudad wage file** · **GOSI configurable rate table with effective dates** · **EOSB monthly accrual** · Saudization/Nitaqat reporting · Payslips.

**Assets:** Asset register · Straight-line depreciation with GL posting · Disposal/scrap with gain-loss.

**Operations:** Shift open/close · Mid-shift cash drop · X-report · Z-report reconciliation with short/over.

**Intelligence:** Full report suite · Custom report builder · Scheduled/automated reports · Dead stock, fast-moving, reorder prediction · Sales forecasting · KPI dashboard · Campaign ROI.

**Communication:** Notification center (in-app/email/SMS/push/WhatsApp-ready) · SMS gateway · Email system.

**Governance:** Append-only audit trail (who/what/when/where/before/after) · Live activity feed · **Configurable workflow & approval engine** · Approval Center · Document management · Global search & command center.

**Saudi Compliance — E-Invoicing:** ZATCA Phase 2 (UUID · ICV non-resetting counter · PIH hash chain · ECDSA cryptographic stamp · CSID lifecycle · Base64 TLV QR · UBL 2.1 XML / PDF-A3) · Standard vs Simplified invoice handling · Clearance vs Reporting models · Credit/Debit notes linked to originals · Fatoora integration with retry queue & critical alerts · **Offline signing architecture (sign locally, transmit later)** · Invoice immutability · ZATCA SDK in QA pipeline · Arabic RTL invoices.

**Saudi Compliance — Tax:** VAT registration thresholds (SAR 375,000 mandatory / SAR 187,500 voluntary) · Configurable 15% rate · Standard/Zero-rated/Exempt/Out-of-scope/Export/Reverse-charge/Import treatments · Exemption reason codes · VAT-inclusive pricing · **Monthly vs quarterly filing logic (SAR 40M threshold)** · Filing deadline reminders · Nil returns · **VAT Return Preparation Report** · **Pre-filing 4-way reconciliation (POS ↔ ledger ↔ bank ↔ Fatoora)** · Input VAT recoverability flags · VAT adjustments · **6-year (up to 11-year) record retention with immutable archive** · **Legal hold overriding PDPL deletion** · Audit Export Pack · Zakat/CIT-ready financial statements · Withholding tax · Customs duty & import VAT · Marketplace VAT liability · VAT grouping.

**Saudi Compliance — Payments:** Mada (national debit, non-negotiable) · Visa/Mastercard/Amex · Apple Pay · STC Pay · Samsung Pay · SADAD · Bank transfer · Cheque · **Tabby & Tamara BNPL** · Store credit · Loyalty redemption · Customer due · Split payment · **Gateway-agnostic abstraction layer** · Adapters for HyperPay/Moyasar/PayTabs/Tap/Geidea/APS/Checkout.com · SAMA-licensed-provider-only policy · PCI-DSS scope minimization · Mada sandbox testing · Integrated card terminal & SoftPOS · Per-tender fee tracking & settlement timing.

**Saudi Compliance — Privacy & Labour:** PDPL (data minimization · lawful basis · **separate marketing consent** · data classification · data subject rights · RoPA · retention & destruction log · legal hold · breach workflow · DPO · processor records) · **Per-tenant data residency with Saudi-region option** · Cross-border transfer safeguards · E-commerce law (MoC/Maroof/business.sa registration field · CR & VAT display · Arabic disclosures · 14-day cooling-off · no hidden fees) · WPS/Mudad · GOSI · Qiwa consistency check · EOSB.

**Internationalization:** Country configuration engine · Multi-currency with FX · Multi-language (Arabic/English/Bengali+) · RTL/LTR mirroring · Reusable tax template library.

**Platform Engineering:** MFA & session/device management · Secrets vault (critical for CSID keys) · Offline-first POS · Idempotent UUID-based sync engine · Conflict resolution · Failed-sync queue · Device management · Encrypted automated + one-click backup · Point-in-time recovery · Subscription plans & billing · Per-tenant feature flags · REST API + webhooks · Import/export · Competitor POS migration wizard · System health monitoring · Background job/queue system · Support ticketing.

**Customization:** Company/branch settings · Receipt & invoice template designer · Configurable numbering per store/type/year (without breaking ZATCA ICV) · Per-user preferences · Per-terminal settings · Theme & color.

---

# PART M — QA & LAUNCH CHECKLIST (before selling to the first Saudi client)

1. **Accounting integrity:** Trial balance always balances · Balance Sheet balances · Sub-ledgers tie to control accounts · Closed periods truly locked.
2. **ZATCA:** SDK validation passes for Standard invoice, Simplified invoice, Credit Note, Debit Note · Hash chain unbroken across 10,000+ sequential test invoices · ICV never resets or gaps · QR scans correctly in ZATCA's verification app · Sandbox clearance and reporting both succeed · Retry queue recovers correctly after a simulated 24-hour outage.
3. **Offline:** Sell 500 invoices with internet fully disconnected · Reconnect · Verify zero duplicates, zero lost invoices, correct hash chain order, correct ZATCA submission.
4. **Payments:** Mada sandbox transactions pass · Split payment across 3 tenders posts correctly · Refund reverses correctly across all tenders · Settlement batch reconciles to individual sales.
5. **VAT:** VAT return figures reconcile against POS reports, general ledger, bank records, and submitted Fatoora data for the same period.
6. **Arabic/RTL:** Full receipt, invoice, and UI render correctly in Arabic RTL, including mixed Arabic/English product names and numerals.
7. **Permissions:** Attempt every restricted action via direct API call while logged in as a Cashier — all must be rejected server-side, not just hidden in the UI.
8. **Multi-tenancy:** Attempt cross-tenant data access via manipulated API requests — must fail in every case.
9. **Retention:** Verify a 6-year archive record is retrievable and its hash still validates.
10. **Legal review:** Saudi tax advisor reviews VAT/ZATCA implementation; legal counsel reviews PDPL posture and your tenant data processing agreement.

---

# PART N — REGULATORY SOURCE HIERARCHY

**This section was restructured in v2.1.** The previous version listed consulting firms, tax-advisory blogs, and vendor guides alongside each other as if they carried equal weight. They do not. Below, sources are split into two tiers, and **only Tier 1 is binding on implementation**.

## Tier 1 — BINDING OFFICIAL SOURCES (must be consulted before writing compliance code)

No compliance feature may be implemented from Tier 2 alone. Every technical detail — field formats, hash construction, QR encoding, wage-file layout, contribution rates, retention periods — must be taken from the official document.

| Domain | Official authority | Documents that govern implementation |
|---|---|---|
| **E-Invoicing** | **ZATCA** — `zatca.gov.sa` | E-Invoicing Detailed Guideline · **XML Implementation Standard** · **Security Features Implementation Standard** · Data Dictionary · Fatoora SDK & developer portal · onboarding/CSID documentation |
| **VAT** | **ZATCA** — `zatca.gov.sa` | VAT Implementing Regulations · VAT guidelines · taxpayer portal e-services (registration, filing-period change, returns) |
| **Data Protection** | **SDAIA** — `sdaia.gov.sa` / Data Governance Platform | **PDPL** · **Implementing Regulation** · **Regulation on Personal Data Transfer Outside the Kingdom** · privacy-policy, minimum-data, and controller/processor guidance · destruction & anonymization guidance |
| **Payroll / Wage Protection** | **MHRSD** + **Mudad** — `hrsd.gov.sa`, `mudad.com.sa` | Current WPS/Mudad **wage-file specification and format** · submission timing rules · establishment registration requirements |
| **Social Insurance** | **GOSI** — `gosi.gov.sa` | Current contribution rate schedule (rates are on a legislated upward path — must be read as a dated schedule, not a fixed number) |
| **Employment / Saudization** | **Qiwa** — `qiwa.sa` | Contract registration requirements · Nitaqat rules |
| **Payments** | **SAMA** — `sama.gov.sa` | **Current licensed Payment Service Provider list** (the authoritative check that a gateway is legally usable) · Payment Services Provider Regulations |
| **E-Commerce** | **Ministry of Commerce** — `mc.gov.sa`, `business.sa` | E-Commerce Law & Implementing Regulations · current store registration/verification channel (this has been transitioning — verify which channel is live at the client's onboarding date) |
| **Accounting Standards** | **SOCPA** | IFRS as adopted in Saudi Arabia (relevant to statement presentation in J-series reporting) |

## Tier 2 — SECONDARY / CONTEXTUAL SOURCES (used for orientation only)

These informed the *scoping* of this document — what to look for, what waves and deadlines exist, what the market expects — but are **not authoritative** and must never be cited as the basis for a technical decision. They include: EY, PwC, KPMG, DLA Piper, ICLG, and various Saudi tax-advisory, payments, and ERP-vendor publications consulted during August 2026 research (VATupdate, Sharayeh, Out2Sol, Flick Network, Qeemah, LookPOS, SynergyStrat, Analytix, ClearTax, Noble Core, TAS Outsourcing, HAL Simplify, SGC Consulting, Ampcus Cyber, Saudi Compliance Institute, Kiework, Multiplier, Setup in Saudi, Infura Group, Promenics, Safwa HR, Payleute, AZ Tech Training, Origami, Swissmena, IncorpMENA, CMARIX, SIRA, GulfSaasReview, Ijjad, LogioLegion, PaymentProviders.io).

## Claims in this document that MUST be re-verified against Tier 1 before coding

These are the specific operational assertions most likely to have changed, or to be stated imprecisely here. Each must be confirmed against its official source and corrected in place if it differs:

| # | Claim in this document | Verify against | Why it matters |
|---|---|---|---|
| 1 | Simplified invoices reported within 24 hours; Standard invoices cleared before delivery | ZATCA Detailed Guideline | Drives the entire offline state machine (E1.3) |
| 2 | Exact XML field set, hash construction, and TLV QR byte layout | ZATCA XML + Security Features Standards | Wrong bytes = rejected invoices at scale |
| 3 | Current wave thresholds and deadlines | ZATCA roll-out page | Determines which clients are already obligated |
| 4 | VAT standard rate 15%; registration thresholds SAR 375,000 / 187,500 | ZATCA VAT regulations | Configurable, but the default must be right |
| 5 | Monthly filing above SAR 40M, quarterly below; due last day of following month | ZATCA VAT guidance | Drives filing reminders and period logic |
| 6 | Record retention ≥6 years, up to ~11 years for certain assets | ZATCA VAT regulations | Drives archive design and legal-hold logic |
| 7 | **Mudad wage-file format, submission timing, and payment-window rules** | **Mudad current specification** | *Flagged in review: the exact format and timing claims in E6 are directionally right but must be locked against the live Mudad spec — file formats change without publicity* |
| 8 | GOSI contribution rates and the pre/post-July-2024 employee distinction | GOSI current schedule | Must be built as a dated rate table, never hard-coded |
| 9 | PDPL breach notification timeframe and data-subject response deadlines | SDAIA Implementing Regulation | Drives workflow SLAs in E4 |
| 10 | Cross-border transfer conditions and required safeguards | SDAIA Transfer Regulation | Determines hosting architecture (E4.2) |
| 11 | E-commerce registration channel (Maroof vs business.sa) and mandatory disclosures | Ministry of Commerce | Actively transitioned — treat as configurable (E5) |
| 12 | Which gateways are currently SAMA-licensed | SAMA licensed PSP list | Integrating an unlicensed provider is a real commercial risk |

**Process rule for the build team:** when a Tier 1 source contradicts this document, **the Tier 1 source wins and this document is amended** — with the change dated and noted, so the blueprint never silently drifts out of alignment with the law.

---

# PART O — ORIGINAL REQUIREMENTS TRACEABILITY

*Every requirement stated by the product owner is mapped here to the section that specifies it, so nothing stated at the beginning gets quietly lost by the end. If a requirement is not in this table, it was not requested.*

## O1. Access Hierarchy & Account Control

| Original requirement | Where it is specified | How it works |
|---|---|---|
| "Main Super Admin access stays with me — nobody else" | **A4** | Super Admin is a platform-level role above every tenant. Only the platform owner holds it. Protected by mandatory MFA, session/device management, login history, lockout, and suspicious-login detection. |
| "Whoever takes the service from me — I create the Owner account with username and password" | **A4, A5** | Super Admin provisions the tenant and generates the Owner's username + temporary password (forced change on first login). Company ID and initial configuration are created automatically. |
| "If I forget the Super Admin password, there must be a recovery option" | **A4.1** | Recovery email + MFA recovery + one-time backup recovery codes (generated and saved at setup) + a documented emergency procedure only the verified platform owner can trigger. |
| "If the Owner forgets theirs and tells me, I can give it to them" | **A4.2** | Owner first tries self-service recovery. If that fails, they contact you; after you verify their identity you **issue a secure password reset**. Technical note: you issue a *new* password — you cannot *view* their old one, because passwords are stored as irreversible hashes. This is deliberate: if you could read their password, so could anyone who breached your database. Every assisted recovery is permanently audit-logged. |
| "Owner will have full control over everything" | **A5** | Owner is the highest authority inside their own tenant — every module, setting, and user. Super Admin sits above only at platform level (billing, plan limits, uptime), not inside the Owner's business data. |

## O2. Employees, Roles & Permissions

| Original requirement | Where it is specified | How it works |
|---|---|---|
| "Owner can add employees" | **A6, C5** | Full employee directory with profile, branch assignment, and login credentials. |
| "Select a role and choose what access they get" | **A6.1** | 13 predefined roles ready to use — Manager, Cashier, Accountant, Inventory Keeper, Purchase Manager, HR, Sales Executive, Delivery, Auditor, and others. |
| "Roles will be pre-defined" | **A6.1** | Yes — each ships with sensible defaults (e.g. Cashier can never see cost price or profit margin). |
| "If wanted, create custom roles and give custom access" | **A6.2** | Custom role builder with per-action permissions: View · Create · Edit · Delete · Approve · Export · Print · Refund · Discount · Void · Adjust Stock · Transfer · View Cost · View Profit. |
| "Control everything" | **A6.2, F1** | Permissions additionally scoped by **branch**, **warehouse**, **amount limit** (e.g. Cashier discounts up to SAR 50, Manager up to SAR 500), and **time window**. Plus a configurable **workflow engine** so the Owner writes their own approval rules without a developer. |

**Security note that matters:** every permission is enforced **on the server**, on every API call. Hiding a button in the interface is not security — an employee who knows the URL could otherwise bypass it. This is verified explicitly in the QA checklist (Part M, item 7).

## O3. Platform — Website + Software + Mobile

| Original requirement | Where it is specified | How it works |
|---|---|---|
| "It will be website-based, and there will also be software" | **A7, J1** | **Web app** (Next.js) for the full back-office + **Desktop software** (Tauri) for the fast, offline-capable showroom POS. Same business logic, shared design system. |
| "Everything on the website will also be in the software" | **A7, J2** | One shared backend (Go API) and one shared core logic layer — the desktop app is a different *front end*, not a different *product*. Nothing exists in one and not the other, except hardware-specific POS functions that only make sense on the counter machine. |
| "The software and website will be responsive" | **A7, I4** | Fully responsive across phone, tablet, iPad, laptop, and desktop. |
| "The website will mainly be mobile-fast, usable like a phone app" | **A7** | Installable **PWA** — add to home screen, app-like navigation, push notifications, offline caching on key screens. Mobile performance is treated as a primary target, not an afterthought (Part J4). |

## O4. Expense & Money Tracking — "One Click, See Everything"

This was the **first requirement you gave**, and it is treated as a first-class feature, not just a report (see Guiding Principle #10 in A2).

| Original requirement | Where it is specified | How it works |
|---|---|---|
| "Owner can track all expenses in one click" | **C3.1, A8.1** | Dashboard expense widget with one-click drill-down by category, branch, and period. |
| "See where every cost is going, per day" | **C3.1, D1** | Every expense carries date, amount, category, expense head, branch, department, payment account, vendor, description, and an attached receipt image. Filterable by day / week / month / year / custom range. |
| "How much money invested where" | **C3.2** | Investment module tracks owner investment, investor investment, capital injection, withdrawal, and each investor's share — kept **completely separate from sales revenue** so profit figures stay honest. |
| "Raw material bought for production" | **C3.1** | Light Production Cost Tracking — raw material, stitching/labour, and packaging cost allocated per production batch, flowing into true COGS. *(Full manufacturing ERP is deliberately out of scope — see C3.1 scope boundary.)* |
| "Client's money, rent — keep account of everything" | **C3.1, C4, B16** | Rent and recurring costs via the recurring-expense engine; money owed **to** the business tracked in Accounts Receivable and the customer ledger; money owed **by** the business in Accounts Payable — both visible from the same dashboard. |
| "Customizable, by clicking a button" | **C3.1, D1** | Custom expense categories and heads, custom report builder, saved report views, and configurable dashboard widgets. |
| "From phone, laptop, iPad — all of them" | **A7, A8.1** | Identical drill-down capability on every device, since it is the same responsive application. |

## O5. Market Requirements

| Original requirement | Where it is specified |
|---|---|
| "Perfect for Saudi + international clients" | **Part E** (Saudi legal/tax/payment) + **Part G** (country configuration engine — Saudi is one configuration, not the architecture) |
| "Must be legal and perfect for Saudi" | **Part E** in full, **E8** regulatory rule registry, **Part M** QA checklist, **Part N** binding source hierarchy |
| "Complete business management — everything" | **Parts B, C, D, F** — POS, inventory, procurement, accounting, HR/payroll, CRM, wholesale, online orders, delivery, warranty, installments, assets, analytics |
| "So that everyone takes the software from me" | **A3** multi-tenant SaaS · **H5** subscription plans & feature flags · **H7** migration wizard from competitor systems · **Part E** compliance as the commercial differentiator |

---

**Closing note on scope:** every requirement above is specified. Nothing stated at the outset was dropped along the way. Anything *not* in this table was not requested — and per the freeze decision, should not be added before system design begins.

---

# FINAL PRODUCT POSITIONING

**RawSyst POS — Complete Retail ERP & POS for Saudi Arabia and International Markets.**

*Built by RawSyst IT | Founder: Mahedi Hasan Emon | rawsyst.com*

Not marketed as a billing app. The promise is:

**Sell → Purchase → Stock → Warehouse → Customer → Supplier → Accounting → Employees → Payroll → Online Orders → Delivery → CRM → Analytics → Compliance → Multi-Store — everything in one platform, on any device, working even when the internet doesn't.**

---

## STATUS: v2.2 — FEATURE LIST FROZEN, SYSTEM-DESIGN READY

This is the **complete, corrected, frozen requirements baseline**. It consolidates the original Bengali requirements, both earlier draft blueprints, the 15-point gap review, the v2.0 consistency review, and the v2.1 correctness review.

### Correction history
**v2.1 —** section numbering across Parts G–J corrected · all internal cross-references repaired · **offline/ZATCA B2B business rules locked (E1.3)** · Part N restructured into binding Tier-1 vs contextual Tier-2 sources with a 12-point pre-coding verification list · compliance wording changed from guarantee-style to support-style.

**v2.2 —** five corrections, zero new features:
1. **ZATCA feature-flag contradiction resolved (A4, E1)** — compliance capability is core and activates on obligation status; it can never be sold away or switched off. Only compliance *monitoring* tooling is premium.
2. **Phase 2 obligation wording narrowed (E1)** — obligation is per-taxpayer and ZATCA-notification-driven, captured as tenant configuration, never assumed by the software or asserted in sales material.
3. **E8 Regulatory Rule Registry added** — every rate, threshold, deadline, SLA, and file format is versioned data with effective dates and a source reference, so reports reproduce historically correct figures and legal changes are configuration, not code releases. Mudad wage-file format, GOSI rate schedule, and ZATCA schema version are flagged as the three highest-volatility entries.
4. **PDPL SLAs made explicit and enforced (E4)** — 30-day data-subject response (extendable 30), 72-hour SDAIA breach notification with an automatic countdown from incident logging.
5. **Scope boundaries hardened** — Light Production Cost Tracking is in; full Manufacturing ERP (BOM, work orders, WIP, variance) is explicitly **out of scope for v1**. All "unlimited" claims replaced with plan/infrastructure-dependent limits requiring concrete ceilings at design time.

Also added: **Part O — Requirements Traceability**, mapping every requirement originally stated by the product owner to the section that specifies it, so nothing requested at the start was lost by the end.

### Outstanding before/during system design
* **Product name and branding** — finalize after trademark/domain clearance (see header note).
* **Tier 1 verification pass** — work through the 12-item table in Part N against official ZATCA, SDAIA, Mudad, GOSI, SAMA, and MoC sources, and populate the E8 registry with verified, dated values. **Do not let developers fill these in from assumption.**
* **Legal review** — Saudi tax advisor on the VAT/ZATCA implementation; legal counsel on PDPL posture and your tenant data-processing agreement.

### Next step — System Design Specification
Built from this document in this exact sequence:

**Module → Submodule → Feature → User Role → Permission → Workflow → Database Entity → API Endpoint → UI Screen → Service → Background Job → Integration → Security Rule**

**Scope is closed.** Design these three first, because every other module depends on them:
1. **Invoice state machine** — including offline local signing, ICV/PIH chain integrity, and the B2B clearance rules in E1.3
2. **Double-entry posting engine** — every transaction type's journal rules, and the fiscal period lock
3. **Sync & idempotency model** — UUID deduplication, per-device ordering, conflict resolution

Get those three right and the remaining modules are conventional work. Get them wrong and the entire system has to be rebuilt.
