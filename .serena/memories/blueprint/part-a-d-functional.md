# Blueprint Parts A–D — Functional Spec (distilled)

Source: `RawSyst-POS-Blueprint-v2.4-FINAL.md` lines 80–733. Read this instead of the doc.

## A2 — Ten non-negotiable principles
1. ERP not POS — every module auto-posts to Accounting + Inventory, no manual double entry.
2. Compliance (ZATCA/VAT/PDPL) is core architecture, not a plugin.
3. Offline-first is hard requirement — sell with zero internet, sync without duplicates.
4. Nothing hard-coded to Saudi — country/tax/currency/language are configuration.
5. Every financial txn traceable: who/what/when/where/before/after.
6. Permissions enforced server-side, never only hidden in UI.
7. Finalized invoices immutable — corrections only via Credit/Debit Note/Return.
8. Heavy reports run async — POS never freezes.
9. POS must feel instant — local-first scan→cart→payment→receipt.
10. Owner answers "where is my money going" in ONE click (C3).

**Wording constraint (UI strings + marketing):** software *supports* compliance, never *guarantees*. Never "ZATCA-certified" / "guaranteed compliant" / "never at legal risk".

## A3 — Tenancy
Tenant identity on every request via JWT, **enforced at the database query layer**, not just frontend. Spec does NOT mandate DB-per-tenant. Every quantity limit must have a **concrete default + maximum**, plan- AND infrastructure-scoped: tenants/cluster, branches, users, POS terminals, SKUs, held carts, custom roles, stored documents.

## A4 — Super Admin (platform control plane)
Tenant lifecycle · plan assignment (Starter/Professional/Business/Enterprise) · per-tenant feature flags (Installment, Multi-Branch Sync, SMS, Wholesale, Payroll, Online Orders, Delivery, Warranty, Advanced Analytics) · global config (countries/currencies/languages/tax templates/security policy) · platform audit log · health telemetry incl. per-tenant sync-queue depth + failed ZATCA submissions platform-wide · support tickets · maintenance mode.

**HARD RULE:** legally-required compliance can NEVER be feature-flagged off. ZATCA e-invoicing, VAT calculation, invoice immutability, audit logging, PDPL handling are **core, not sellable**. Activation is *derived*: `country + VAT registration + ZATCA wave → Compliance Engine ENABLED, cannot be switched off`. Only compliance **monitoring** (multi-branch analytics, extended audit export, deadline dashboards, priority alerting) is premium.

**A4.1** Super Admin: strong password + mandatory MFA, session list + remote revoke, login history, lockout, one-time backup recovery codes. Password never stored/displayed in plain text, even to Super Admin.
**A4.2** Owner recovery: self-service → else Super Admin verifies identity → issues one-time password → **never views/restores old password (irreversible hash)** → every assisted recovery permanently audit-logged.

## A5 — Onboarding wizard (7 steps)
Business Info → Store Setup → Tax Config (**Saudi ⇒ auto-load VAT 15% + ZATCA + Arabic RTL**) → Employees → Hardware Setup → Opening Balances → Finish (auto-provisions environment). Temp password, forced change on first login.

## A6 — RBAC
**12 predefined roles:** Owner · Branch/Store Manager (no bank ledger/net profit) · Cashier (cost price + margins always hidden) · Accountant (no inventory/product edit) · Inventory Keeper (no pricing/sales) · Purchase Manager · HR Manager · Sales Executive (own customers) · Delivery Staff (assigned only) · Online Order Manager · Auditor (read-only everything) · Customer Service (no financial data).

**Permission verbs:** View · Create · Edit · Delete · Approve · Export · Print · Refund · Apply Discount · Void · Adjust Stock · Transfer Stock · View Cost Price · View Profit Margin. NOTE: worked example also uses Hold/Exchange/Receive Payment ⇒ **verb set must be extensible per module, not a fixed enum**.

**4 scope dimensions layered on verbs:** by store/branch · by warehouse · by transaction amount limit · by time window (expiring roles).
**Field-level data masking** separate from verbs: hide supplier cost, profit margin, other employees' salaries.

## A7 — Platforms (one shared business logic)
Next.js web (back-office) · Tauri+React+SQLite desktop POS (offline, direct hardware) · responsive PWA (owner mobile). **One design system across all three.**

## A8 — Owner Dashboard
Today's Sales/Profit, Gross+Net Profit, **Expenses with one-click category drill-down**, Cash per register, Bank per account, Receivables, Payables, Inventory Value, Low/Dead Stock, Top products/employees, Sales by store/category/brand/payment-method, trend charts, outstanding payables/dues, pending orders/approvals, investment summary. Date filters: Today/Yesterday/Week/Month/Year/Custom. **Identical on phone/laptop/iPad, one-click drill-through on every widget.**

## PART B — Retail operations (entities + rules)

- **B1 Product:** SKU, barcode, name **+ Arabic name**, category tree (unlimited depth), brand, UoM, cost, images. **4 price tiers: Retail/Wholesale/Dealer/Minimum Floor Price** — floor price enforced by system even after discount. Tax category: Standard/Zero-Rated/Exempt/Out-of-Scope. Min/max/reorder stock. Flags: warranty period, serial tracking, batch/expiry tracking. **Bundles** deduct components proportionally. Lifecycle `Active → Inactive → Discontinued` without breaking historical invoices.
- **B2 Variant matrix (fashion/RMG core):** Parent product → auto-generate child variants across Size/Color/Material/Style/Season/Gender + unlimited custom attributes. **Each child variant is the true SKU: own barcode, price, cost, stock, weight, image.** Variant-level stock reporting. POS shows quick size/color grid for one-tap.
- **B3 Barcode engine:** smart human-readable codes (`M-WIN-BLK-XL`) over Code128/EAN-13/EAN-8/UPC-A/QR. Manual override. **Bulk generator** (1000 in one click). Hang-tag designer (logo, EN+AR name, size, color, VAT-inclusive price, barcode). Thermal label studio (Xprinter/Zebra/TSC, 50×25mm/38×25mm/custom). A4 sheets 24/30/48 per page. Loyalty card barcodes.
- **B4 Inventory:** 8 stock states (Total/Available/**Reserved**/Damaged/Returned/**In-Transit**/Available-to-Sell). Stock-in: GRN, customer return, adjustment, transfer receive, opening. Stock-out: sale, supplier return, damage, adjustment, transfer dispatch, internal use. Wastage requires **mandatory reason + auto loss write-off to accounting**. Transfer state machine: `Request → Manager Approval → Dispatch (in-transit lock) → Receiving branch confirms & reconciles`, discrepancies auto-flagged. **Landed cost** = purchase + shipping + customs allocated by configurable rule. Batch/lot/expiry with Expiring Soon/Expired/Recall alerts. Serial/IMEI lifecycle: Supplier→Purchase→Inventory→Sale→Customer→Warranty→Return. Physical count: 5 modes, auto-generates signed Adjustment Voucher for variance.
- **B5 Purchasing:** Requisition → Manager Approval → PO → Supplier → **GRN (only GRN increases stock — a PO alone never inflates inventory)** → Purchase Invoice → Payment → Accounting. Purchase Return auto-generates Debit Note + deducts stock.
- **B5.1 RFQ:** RFQ → multiple supplier quotes → side-by-side comparison (unit price, total, VAT, lead time, payment terms, quality) → select winner **with reason recorded** → PO auto-generated. Quote validity/expiry/versioning, historical archive per supplier.
- **B5.2 Three-way match:** PO + GRN + Supplier Invoice matched on qty/price/VAT/discount/total before payment approval. **Configurable tolerance** (e.g. 2% or SAR 50). **Mismatch beyond tolerance BLOCKS payment** and routes to approver with discrepancy highlighted.
- **B6 Supplier:** VAT reg + CR number, payment terms (Cash/7/15/30/60/custom). Ledger: `Opening Due + Purchases − Payments ± Adjustments = Closing Due`. Aging payables.
- **B7 POS (speed-critical):** **global keystroke capture for scanner — no click into a field**. Search by SKU/name, browse category/brand, Favorites + Recently Sold grid, variant picker. Per-line discount within cashier limit, VAT auto per line tax category, price override permission-gated. **Full multi-tender split on one invoice** (Cash/Mada/Visa/MC/bank transfer/wallet/Apple Pay/STC Pay/BNPL/store credit/loyalty/customer due). Hold multiple carts (configurable ceiling) + instant resume. Gift/FOC items with dedicated zero-value entry tracked separately.
- **B8 Hardware:** USB scanner HID plug-and-play · **ESC/POS direct thermal print 80mm/58mm with NO browser print dialog** (drives Tauri choice) · **auto cash-drawer RJ11 kick on cash sale** · label printer · customer-facing display · scale (future) · card terminal ready. Setup wizard: Connect Scanner → Detect Printer → Test Print → Test Drawer → Configure Receipt → Finish.
- **B9 Promotions:** 12 types (%, fixed, BOGO, bundle/volume, category, brand, product, customer-type, coupon, seasonal, flash, employee). Conditions: date range, store, customer type, min purchase, product/category/brand. **Discount beyond cashier limit requires manager PIN, logged.** Campaign ROI analytics.
- **B10 Returns/Exchange:** original invoice **always scanned/linked, never re-typed**. Quick Exchange single screen: scan invoice → select items → pick new size → auto-calc difference → done. Refund to cash / store credit / original payment method, **always a Credit Note, never a silent invoice edit**.
- **B11 Quotation/SO/Delivery:** Quotation with validity → one-click convert to SO or Invoice. SO lifecycle `Draft → Confirmed → Processing → Packed → Delivered → Completed`. Delivery Challan (itemized, **no prices**). Picking slip by warehouse/aisle/shelf. Packing slip. Recurring invoices. Sales region tracking.
- **B12 Wholesale:** dealer price tier, MOQ rules, volume discounts, per-client credit limit. **Kept separate from retail so retail reporting isn't distorted.**
- **B13 Online orders:** `Order Placed → Payment → Stock Reservation → Picking → Packing → Delivery → Completed`. Reservation prevents two channels selling the last unit. Delivery pipeline: `Pending → Assigned → Picked Up → Out for Delivery → Delivered → Failed → Returned`.
- **B14 Installment/EMI:** tenure 3/6/12/24, down payment, markup rate → fixed monthly. Verification docs (ID/Iqama, employment, salary ref, guarantor) under **PDPL-controlled access**. Per-installment status Paid/Unpaid/Overdue/Partial + configurable penalty. Automated reminders.
- **B15 Warranty/Service:** warranty linked to serial + customer + purchase invoice. Claim: scan serial → verify → log → repair/replace → update. Replacement register (old/new serial, reason, approver). Work orders `Received → Under Inspection → Repaired → Delivered` tracking parts + labour cost.
- **B16 CRM:** customer type Retail/Wholesale/VIP, lifetime spend, due balance. **Fashion size & fitting history** (shirt L, pants 34, shoes 42, collar/sleeve/length/waist). Khata ledger with **credit limit enforcement — block/approve when exceeded**. Loyalty points (configurable accrual) + tiers Bronze/Silver/Gold/VIP. Store credit/gift card/wallet with expiry. 7 segments. Loyalty barcode cards.

## PART C — Finance

- **C1 COA:** multi-level tree, 5 groups (Assets/Liabilities/Equity/Revenue/Expenses) with named defaults incl. COGS.
- **C2 Cash & Bank:** 4 cash account types (Main vault / per-Store / Petty / per-Cashier drawer). Bank + **card settlement + gateway settlement accounts**. Cheque state machine issue→deposit→clear→**bounce**. 4 transfer directions, each with audit entry + printable voucher.
- **C3.1 Expenses:** full category tree, 12 entry fields incl. **receipt attachment**, recurring schedules, approval chain `Employee creates → Manager approves → Accountant verifies → Payment released → Posted`. **Light Production Cost Tracking** (raw material, stitching labour, packaging allocated per batch) — **full Manufacturing ERP (BOM/work orders/WIP/routing/variance) explicitly OUT OF SCOPE for v1**. Client rent receivable tracked alongside.
- **C3.2 Investment:** owner + investor capital, injections, withdrawals, proportional shares. **Kept fully separate from revenue — never mixed with sales income.** Investor sees only their own statement (row-level scoping).
- **C4 AR/AP:** aging buckets 0–30/31–60/61–90/90+.
- **C5 HR:** employee master incl. **Iqama/National ID + expiry alerts**. Attendance, leave, overtime, salary advances auto-deducted next payroll.
- **C6 Payroll/WPS:** components → net salary auto. **Commission engine** by employee/product/category/store/revenue/profit, flat or **tiered**. WPS via **Mudad** (MHRSD), mandatory for every private employer regardless of headcount, **XML-based format distinct from pipe-delimited SIF**, submit ≥1 business day before payday, salaries within first 10 days. Mudad cross-references **GOSI + Qiwa** — mismatch can freeze portal access. **GOSI: Saudis both employer+employee contribute; expats employer only. Rates rising July 2024 → 2028 ⇒ configurable dated rate table, never hard-coded.** Bilingual payslips. Positioning: **"WPS/Mudad-ready"**, employer retains legal responsibility.
- **C7 Assets:** register, **automated monthly straight-line depreciation with GL postings**, disposal with gain/loss.
- **C8 Shift/X-Z:** open with float → silent tracking → mid-shift cash drop to vault → **X-report (mid-shift snapshot, closes nothing)** → **Z-report: counted cash vs expected, Short/Over/Exact, signed closing record**.
- **C9 Double-entry engine:** JournalEntry + JournalLines (account, debit, credit, currency, store, source-doc ref). **Debits must equal credits — unbalanced entry can never be saved.** **Immutable once posted; corrections by reversing entry only.**
  - Posting rules given: (1) cash sale → Dr Cash / Cr Sales Revenue / Cr Output VAT Payable; (2) **COGS simultaneous with every sale** → Dr COGS / Cr Inventory; (3) credit purchase → Dr Inventory + Dr Input VAT / Cr AP; (4) return reverses all; (5) expense → Dr Expense + Dr Input VAT (where recoverable) / Cr Cash-Bank. Rules 6–12 each need "its own **defined, configurable** posting rule": salary, investment injection, asset purchase, depreciation, stock write-off, inter-account transfer. ⇒ **posting-rule table/engine, not hard-coded code.**
  - Statements auto: GL with running balance, Trial Balance, P&L, Balance Sheet (**Assets = Liabilities + Equity always**), Cash Flow, Retained Earnings roll-forward.
  - **Hard invariants:** AR sub-ledger = AR control account; AP sub-ledger = AP control; **Inventory sub-ledger = Inventory GL account**.
  - Multi-currency with automatic FX gain/loss on settlement.
- **C10 Fiscal periods:** monthly `Open → Closed → Locked`. **Closed period: no transaction created/edited/deleted.** Reopen needs Owner permission + mandatory reason + permanent audit log. Year-end: verify TB → adjusting entries → close revenue/expense into Retained Earnings → roll forward → closing pack → lock year.
- **C11 Bank reconciliation:** import CSV/Excel/OFX → auto-match on **amount+date+reference** → manual match remainder → reconcile & lock → exception report. Must handle 7 cases incl. bank fees, interest, **card settlement batches, gateway settlement batches**, bounced cheques.
- **C12 Settlement:** `Gross − Processing Fee = Net Settlement`, fee posts to Bank/Card Charges expense. **Status machine `Pending → Settled → Reconciled`.** Settlement batch (one bank deposit) reconciles back to **many individual sales** ⇒ many-to-one linkage entity. Per-method fee config so Owner sees true cost of Mada vs credit vs BNPL. Chargebacks/refund reversals are own accounting events. T+1/T+2 timing ⇒ "collected but not yet in bank" figure.
- **C13 Costing:** **WAC · FIFO · Standard Cost with variance**, per tenant, + **Landed Cost overlay on any method**. **COGS computed at moment of sale and posted immediately** (real-time gross profit). **Hard invariant: inventory valuation report must tie exactly to Inventory GL balance; divergence flagged as exception.** Revaluation needs reason + approval. **Negative stock policy configurable: block sale, OR allow with warning + auto-correct cost on next receipt.**
  - *Conflict:* B4 and D1 list only FIFO+WAC. C13 is newer/authoritative — Standard Cost is in scope.
- **C14 Returns — every return must do ALL 9 simultaneously:** reverse inventory (qty+value) · reverse revenue · reverse Output VAT · reverse COGS · settle refund · **reverse/adjust loyalty points earned** · **reverse sales commission attributed** · generate linked Credit Note referencing original · write journal entry + audit record. **Partial returns need proportional VAT, proportional discount allocation, proportional COGS.**

## PART D — Intelligence

- **D1 Reports:** financial (P&L gross+net, BS, TB, cash flow, expense summaries) · sales (daily/weekly/monthly/yearly/**hourly**, by store/employee/category/brand/product/customer/payment-method) · VAT (output vs input, taxable/zero/exempt breakdown, period summary matching ZATCA filing) · inventory (valuation, stock in/out, min-stock-out) · customer/supplier ledgers · HR · audit. **Custom report builder** (pick filters + columns, save & reuse). **Scheduled reports** auto-delivered email/PDF/Excel. **All heavy reports async.**
- **D2 Analytics:** fast-moving · slow/dead stock (configurable 30/60/90/180 days) · reorder prediction from sales velocity · sales forecast (**architecture-ready for ML, no ML in v1**) · profitability ranking · **13 KPIs** (Revenue, Gross Profit, Net Profit, AOV, Units/Transaction, Gross Margin %, Inventory Turnover, CLV, Repeat Rate, Sales/Employee, Sales/Store, Discount Ratio, Return Rate) · campaign ROI.
- **D3 Notifications:** 15 triggers incl. **failed ZATCA submission (critical)**, backup failure, suspicious login, Iqama expiry, customer due over limit. Channels: in-app, email, SMS, PWA push, WhatsApp Business where available. **Marketing messages gated by opt-in consent (PDPL).**
- **D4 Audit trail:** 12 sensitive actions logged. **Exactly 6 fields: Who · What · When · Where (IP/device) · Before-value · After-value.** **Append-only — cannot be edited or deleted by any user including Owner.** Live activity feed on Owner dashboard.
- **D5 Approval Center:** one inbox for 8 approvable types (purchase requests, expenses, refunds, manual discounts, stock adjustments, transfers, salary changes, permission changes), reason capture on rejection.
- **D6 Documents:** searchable central storage; customer ID copies for installments are **PDPL-controlled**.
- **D7 Global search:** one box, **permission-filtered** — product/SKU/barcode/customer/supplier/invoice/order/employee/serial/transaction. **Keyboard-first command menu** ("Create Sale", "Open Reports") without touching mouse.

## Cross-cutting flags for design
1. Costing conflict — C13 wins over B4/D1.
2. Permission verbs must be extensible, not fixed enum.
3. Feature flags structurally cannot disable compliance; enablement is derived state.
4. **Immutability is a shared architectural primitive** in 4 places: finalized invoices (A2#7), posted journal entries (C9.1), closed periods (C10), append-only audit log (D4).
5. Offline sync queue with duplicate-free reconciliation is an explicit testable requirement.
6. Every quantity limit needs a concrete default + maximum.
