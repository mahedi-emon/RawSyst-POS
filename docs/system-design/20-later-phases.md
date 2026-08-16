# 20 — Phases 2–5 Outline

Module boundaries, entities and integration points. Deliberately **not**
detailed to Phase 1's depth: designing a module in full a year before building
it means designing it twice, because Phase 1 will teach us things. Each becomes
its own `1x-` document when its phase begins.

What *is* fixed here: where each module's seams sit, and which decisions already
made constrain it. Those do not change.

---

## Phase 2 — Complete retail operations

### Purchasing (B5, B5.1, B5.2)

> **Partly implemented.** `backend/internal/purchasing/`, migrations `0031`–`0032`,
> and `pos/src/purchasing/`. The spine ships: supplier → PO → GRN → Bill →
> three-way match → Payment, with AP ageing.
>
> **Requisition, RFQ and quote comparison are NOT built.** They are an approval
> workflow that feeds a purchase order and that nothing downstream depends on, so
> they are absent rather than half-present. `debit_note` on supplier return is
> also not built; a supplier return today is handled by rejecting goods at the
> point of receipt, which keeps them out of stock but raises no document.
>
> Seven permissions rather than one, because B5.2's control depends on
> separating them: the person who records a bill must not necessarily be the one
> who approves its discrepancy, and the seeded roles reflect that — a Purchase
> Manager can order, receive and bill but cannot approve or pay, while an
> Accountant can approve and pay but cannot order or receive.

```
Requisition → Approval → RFQ → Quote comparison → PO → GRN → Bill → 3-way match → Payment
```

| Entity | Note |
|---|---|
| `purchase_requisition` | Any authorised staff; routes to manager |
| `rfq`, `supplier_quote` | Versioned; quotes expire; archived per supplier for audit |
| `purchase_order`, `po_line` | |
| `goods_receipt`, `grn_line` | **Only GRN increases stock** — a PO alone never inflates inventory |
| `purchase_bill` | |
| `three_way_match` | PO + GRN + Bill on qty, price, VAT, discount, total |
| `debit_note` | Auto-generated on supplier return |

**Three-way match blocks payment beyond tolerance** (configurable, e.g. 2% or
SAR 50) and routes to an approver with the discrepancy highlighted. B5.2 calls
it "the single most effective control against supplier overbilling and internal
fraud."

*Integrations:* GRN creates cost layers (`10`) and posts Rule 3 (`02`). Landed
cost allocates here. Import VAT splits to Input VAT, duty to inventory cost.

### Suppliers & AP (B6, C4)

`supplier` (VAT + CR number, payment terms), `supplier_ledger`, `payment_voucher`.
Ledger: `Opening + Purchases − Payments ± Adjustments = Closing`. Aging buckets
0–30 / 31–60 / 61–90 / 90+.

### CRM & loyalty (B16)

| Entity | Note |
|---|---|
| `customer` | Type retail/wholesale/VIP, lifetime spend, due balance |
| `size_profile` | **Fashion fitting history** — shirt L, waist 34, collar/sleeve |
| `customer_ledger` | Khata. **Credit limit enforced**: block or require approval |
| `loyalty_account`, `loyalty_txn` | Configurable accrual, tiers Bronze→VIP |
| `store_credit` | Wallet and gift card with expiry |
| `customer_segment` | 7 segments incl. inactive/at-risk |

*Integration:* loyalty accrual and redemption post to Loyalty Liability; returns
reverse points (C14 effect 6, already designed).

### Promotions (B9)

`promotion_rule` with 12 types, conditions on date/store/customer-type/minimum/
product/category/brand. Manager PIN beyond the cashier's limit — already built
in Phase 1's POS. Campaign ROI analytics.

### Bank reconciliation & settlement (C11, C12)

```
Statement import (CSV/Excel/OFX) → auto-match on amount+date+reference
                                 → manual match → reconcile & lock → exceptions
```

Must handle bank fees, interest, **card settlement batches**, **gateway
settlement batches**, bounced cheques.

Settlement batch → many sales is a link table. QA gate M4 requires the batch to
reconcile back to individual sales; Phase 1 already writes
`sales_tender.settlement_status`, so this closes the loop.

### Reports (D1, D2)

Custom report builder (pick filters and columns, save and reuse), scheduled
delivery, 13 KPIs, dead-stock and reorder prediction. All async (A2 #8).

---

## Phase 3 — HR, payroll & Saudi labour

⚠️ **Blocked on registry verification.** Two of the three E8.4 release blockers
live here: the Mudad wage-file format and the GOSI dated rate schedule. Neither
may be filled in from assumption.

| Entity | Note |
|---|---|
| `employee` | **Iqama/National ID · GOSI registration · Qiwa contract · IBAN** — all four, because Mudad cross-references them |
| `attendance`, `leave`, `overtime` | Biometric integration path later |
| `salary_advance` | Auto-deducted next payroll |
| `payroll_run`, `payslip` | Bilingual payslips |
| `commission_rule` | Flat or **tiered**, by employee/product/category/store/revenue/profit |
| `wps_file` | Generated against a **versioned format definition** |
| `gosi_contribution` | Rate resolved **at the pay period**, from the dated schedule |
| `eosb_accrual` | **Monthly**, not discovered at termination |

Two constraints already fixed:

- **A consistency check runs before file generation.** A mismatch between the
  Qiwa contract, the GOSI wage and the actual bank transfer can freeze portal
  access, so it is caught locally first.
- **Re-running an old month must produce the historically correct figure.** GOSI
  rates differ by nationality, by hire date relative to July 2024, and step
  upward through 2028 — resolution is always at the pay period.

Positioning is **"WPS/Mudad-ready"**: the software prepares compliant wage data;
final legal responsibility for submission remains the employer's.

Also: `fixed_asset` with monthly straight-line depreciation posting to the GL,
and disposal with gain/loss.

---

## Phase 4 — Extended commerce

| Module | Notes |
|---|---|
| **Online orders** (B13) | `Placed → Payment → Reservation → Picking → Packing → Delivery → Completed`. **Stock reservation** prevents two channels selling the last unit |
| **Delivery** | 7-state pipeline; third-party courier APIs plug in without redesign |
| **Wholesale** (B12) | Dealer tier, MOQ, volume discounts, credit limit. **Kept separate from retail so retail reporting is not distorted** |
| **Installments** (B14) | Tenure/down-payment/markup → schedule. Verification documents under **PDPL-controlled access** |
| **Warranty & service** (B15) | Serial-linked warranty; work orders `Received → Inspection → Repaired → Delivered` with parts and labour cost |
| **Customer portal** (F2) | Phone + OTP. Invoices **with ZATCA QR intact**, order tracking, payments, loyalty, warranty by serial, **PDPL self-service** |
| **Supplier portal** (F3) | Accept/reject POs, submit quotes into the comparison screen, upload invoices into three-way match, compliance docs with expiry |

**E-commerce law (E5) gates the online store.** Mandatory Arabic disclosures —
CR number, VAT number, contact, VAT-inclusive pricing, return policy, delivery
terms — render from tenant settings and are a **required, validated onboarding
step**, not an optional field. The 14-day cooling-off right is configurable per
category, since some are legally exempt. The registration channel (Maroof vs
business.sa) is a configurable per-tenant field because it has been actively
transitioning.

---

## Phase 5 — Enterprise

### Workflow & approval engine (F1)

The Owner defines rules **visually, without a developer**.

| Element | Options |
|---|---|
| Triggers | Transaction type · amount · percentage · product/category · store · employee · customer credit exposure · time of day · quantity |
| Actions | Require approval (single or **sequential multi-step**) · second-person PIN · block · warn · notify · **escalate after timeout** |
| Routing | By role · by named person · **by hierarchy** (direct manager) |
| Escalation | Timeout escalation; **delegation while on leave** |

Needs a rule store, a runtime evaluator on every transaction commit path, a
pending inbox (`approval_center`), timer jobs, and delegation records. Every
execution is audit-logged **with the full decision chain**.

Phase 1's manager-PIN discount gate is the simplified precursor; it becomes one
configured rule.

### Multi-company consolidation (F4)

Group P&L and balance sheet with **inter-company elimination**, while each
company keeps its own books, VAT registration and ZATCA sequence. The
`Group → Company` hierarchy already exists from Phase 1 precisely so this needs
no migration.

### API platform (H6)

REST + webhooks, API keys, OAuth where applicable, rate limiting, full access
logs. Webhook signing designed in `07`.

### Migration wizard (H7)

`Export → CSV/Excel → Field mapping → Validation → Preview → Import → Error report`.
H7 calls it "a genuine sales-enablement feature — it removes the biggest reason
a prospective client hesitates to switch."

### Advanced analytics (D2)

Forecasting is **architecture-ready for ML but not ML in v1**. Sales velocity,
reorder prediction and profitability ranking are statistical.

---

## Cross-phase constraints that never change

These were settled in Phase 1 and every later module inherits them:

1. **No business table gets the platform predicate.** Super Admin never reads a
   tenant's invoices, journals, customers or payroll. A test enforces it.
2. **No legal value in code.** Every rate, threshold, deadline and file format
   resolves through the registry at the transaction's date.
3. **Immutability is universal.** Finalized invoices, posted journal entries,
   closed periods and audit rows cannot be modified by anyone.
4. **Every new endpoint declares a permission**, or CI fails.
5. **Money is decimal end to end** — `numeric(18,4)` in the database,
   `shopspring/decimal` in Go, strings on the wire.
6. **Every module posts to Accounting and Inventory via events**, never by
   direct call.
7. **Nothing claims certification.** The wording lint runs over every file.

---

## Sequencing note

Phase 3 is gated on regulatory verification, not on engineering. Phases 2 and 4
are not. If the Tier 1 verification pass is slow, **build Phase 4 before Phase
3** rather than idling — the dependency is on data, and only Phase 3 needs it.
