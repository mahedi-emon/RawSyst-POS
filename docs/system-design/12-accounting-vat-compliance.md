# 12 — Accounting, VAT & Compliance (Phase 1)

**Blueprint:** C1–C4, C8–C14, E2, E4, E7. **Depends on:** `02` (posting engine), `05` (registry).

The posting mechanics live in `02-posting-engine.md`. This document covers what
Phase 1 ships on top of it: cash and bank, the VAT return, PDPL, and the
compliance screen.

---

## 1. Chart of accounts

Seeded per company from a country template, editable afterwards. Saudi template
follows the five groups in C1 with the named defaults, plus the accounts the
Saudi tender mix requires:

```
1000 Assets
  1100 Cash            1110 Main Cash · 1120 Store Cash · 1130 Petty · 1140 Drawer
  1200 Bank
  1250 Card Clearing   1251 Mada · 1252 Visa/MC · 1253 Amex
  1260 Wallet Clearing 1261 STC Pay · 1262 Apple Pay
  1270 BNPL Clearing   1271 Tabby · 1272 Tamara
  1300 Accounts Receivable            [control]
  1400 Inventory                      [control]
  1450 Input VAT Receivable
  1500 Fixed Assets · 1590 Accumulated Depreciation
2000 Liabilities
  2100 Accounts Payable               [control]
  2200 Output VAT Payable
  2300 GOSI Payable · 2310 Employee Payables
  2400 Loyalty Liability
  2500 Store Credit Liability
3000 Equity
  3100 Owner Capital · 3200 Investor Capital · 3900 Retained Earnings
4000 Revenue
  4100 Sales · 4200 Other Income
5000 Expenses
  5100 Cost of Goods Sold
  5200 Rent · 5210 Utilities · 5220 Salaries · 5230 Marketing
  5300 Bank & Card Charges
  5400 Inventory Write-off · 5410 Cost Variance
  5500 Cash Over/Short
  5600 FX Gain/Loss
```

**Each clearing account is separate per tender.** Mada, Visa/MC and BNPL carry
materially different fees and settlement timings, and E3.5 requires the Owner to
see true net margin per payment method. One "card clearing" account destroys that.

---

## 2. Cash & bank (C2)

```sql
cash_account   company_id · store_id · kind (main|store|petty|drawer) · account_id
bank_account   company_id · bank_name · iban · currency · account_id
cheque         direction · number · amount · due_date
               state (issued | deposited | cleared | bounced)
fund_transfer  from_account_id · to_account_id · amount · reference · voucher_no
```

Four transfer directions (cash→bank, bank→cash, cash→cash between branches,
bank→bank), each producing its own audit entry and a printable voucher.

A **bounced** cheque is a distinct accounting event that reverses the original
receipt and reinstates the receivable — never a silent status flip.

---

## 3. VAT return (E2.3)

### Filing period

Derived, not typed. Annual taxable supplies above the registry's
`MONTHLY_FILING_THRESHOLD` → monthly; otherwise quarterly. The threshold is a
dated registry value, so a change is configuration.

Due date comes from `SA.VAT.FILING_DUE_RULE` — currently the last day of the
month following period end — and drives a visible countdown per company.

**Nil returns are producible for a period with no transactions**, because filing
is mandatory regardless of activity.

### The report

Structured to map directly onto the ZATCA return form: output VAT, input VAT,
taxable sales, zero-rated, exempt, imports, adjustments, net payable or
refundable.

### The four-way reconciliation

E2.3 calls mismatch between these four "the most common source of ZATCA
compliance notices", so the return cannot be filed until they agree:

```
POS / sales reports  ┐
General ledger       ├─ must all agree for the period
Bank records         │
Submitted Fatoora    ┘
```

The usual cause of a gap is invoices still queued on a terminal. The screen says
so explicitly and links to the queue, rather than reporting an unexplained
number.

### Input VAT recoverability

Certain categories — entertainment, some vehicles, fuel — have **restricted**
input VAT recovery (E2.3). Each expense head carries
`input_vat_recoverable boolean`. When false, the VAT is absorbed into the expense
rather than claimed, so the return is not overstated.

### VAT adjustments

Bad-debt relief, corrections, and credit/debit note effects are their own tracked
transaction type, not edits to prior figures.

---

## 4. Record retention & legal hold (E2.4 vs E4)

The sharpest conflict in the product, and it must be resolved deliberately rather
than by whichever job runs last.

| Obligation | Requires |
|---|---|
| Tax retention (E2.4) | Keep invoices, notes, ledgers, bank records **≥ 6 years** (longer for certain assets) |
| PDPL erasure (E4.1) | Delete personal data on a valid data-subject request |

**Resolution: legal hold wins over deletion, but only for the tax-relevant
record.**

```sql
legal_hold  entity_type · entity_id · reason · held_from · held_until
```

The retention job:

1. Skips anything under legal hold — never deletes it.
2. **Anonymises the personal fields** where the law allows: customer name, phone
   and national ID are replaced; the invoice, its amounts, its VAT and its hash
   chain survive intact.
3. Writes a permanent destruction-log entry recording what was anonymised and
   when.

An anonymised invoice still validates against its ZATCA hash, because the hash
covers the signed document, not the mutable customer record.

---

## 5. PDPL (E4) — Phase 1 scope

Not deferred. PDPL is in active enforcement, applies extraterritorially, and
fines reach SAR 5,000,000 per violation, doubled for repeat offences.

```sql
consent_record
  subject_type · subject_id · purpose (transactional | marketing)
  granted · channel · granted_at · proof · withdrawn_at

data_subject_request
  subject_id · kind (access|export|correct|delete|object|portability)
  received_at · due_at            -- computed from the registry SLA
  state · completed_at · outcome

privacy_incident
  detected_at · severity · description
  sdaia_deadline_at              -- countdown starts on insert
  notified_at · affected_count · containment
```

| Requirement | Implementation |
|---|---|
| **Marketing consent separate from transactional** | Distinct `purpose` values. E4.1 calls this "the single most commonly enforced violation" |
| DSR response SLA | `due_at` computed from `SA.PDPL.DSR_RESPONSE_DAYS`; a job warns before it lapses |
| Breach notification | `sdaia_deadline_at` set on insert from `SA.PDPL.BREACH_NOTIFICATION_HOURS`. **The countdown starts automatically** — it cannot depend on someone remembering |
| Data classification | Every personal column tagged Public / Internal / Personal / Sensitive |
| RoPA | Generated from the classification, producible on short notice |

An SMS or email marketing send **checks consent at send time**, not at list build
time. A subject who withdrew consent yesterday must not receive today's campaign
from a list compiled last week.

---

## 6. Compliance screen (E7)

One screen answering *"am I legally exposed right now?"* — nine panels:

1. ZATCA onboarding / CSID status per branch and terminal
2. Invoices pending / failed / retrying, with drill-down
3. Current VAT rate and next filing deadline countdown
4. **Four-way reconciliation status**
5. PDPL posture — consent coverage, open DSRs, retention job, open incidents
6. E-commerce disclosure completeness
7. WPS/Mudad status *(Phase 3 — shown as "not configured")*
8. Employee document expiry *(Phase 3)*
9. Archive health — is the six-year archive intact and retrievable?

Panels for later phases appear greyed with "not configured" rather than hidden.
An Owner should be able to see the full compliance surface and know what is not
yet covered, rather than discovering a gap at audit.

---

## 7. Audit export pack (E2.4)

One-click export of everything ZATCA would ask for over a chosen period:
invoice XMLs, QR data, hash-chain proof, ledgers, VAT returns, bank records.
Runs as a background job, delivered as a signed archive to object storage.

---

## 8. API

```
GET  /api/v1/accounts                      ?type=&is_control=
POST /api/v1/accounts
GET  /api/v1/journal-entries               ?account_id=&from=&to=
POST /api/v1/journal-entries               manual JE — permission-gated, reason required

GET  /api/v1/periods
POST /api/v1/periods/{id}/close
POST /api/v1/periods/{id}/reopen           Owner-level + mandatory reason + audited
POST /api/v1/periods/year-end              six-step routine

GET  /api/v1/reports/trial-balance         ?as_of=
GET  /api/v1/reports/profit-loss           ?from=&to=
GET  /api/v1/reports/balance-sheet         ?as_of=
GET  /api/v1/reports/cash-flow             ?from=&to=

GET  /api/v1/vat/return                    ?period=          preparation report
GET  /api/v1/vat/reconciliation            ?period=          the four-way check
POST /api/v1/vat/return/{period}/finalise

GET  /api/v1/compliance/status                               the nine panels
POST /api/v1/compliance/audit-export       → background job

GET  /api/v1/privacy/requests
POST /api/v1/privacy/requests
POST /api/v1/privacy/incidents             starts the 72-hour countdown
GET  /api/v1/privacy/ropa
```

| Route | Permission |
|---|---|
| Read accounting | `accounting.view` |
| Manual journal entry | `accounting.create` + reason |
| Close period | `accounting.close_period` |
| **Reopen period** | `accounting.reopen_period` — Owner-level only |
| Compliance | `compliance.view` |

---

## 9. Background jobs

| Job | Cadence | Purpose |
|---|---|---|
| `accounting.tie_out` | Nightly | AR, AP and Inventory sub-ledgers vs control accounts |
| `vat.deadline_countdown` | Daily | Filing reminder per company |
| `vat.reconcile` | Daily | Refresh the four-way status |
| `pdpl.retention` | Daily 03:00 | Anonymise, respecting legal hold |
| `pdpl.dsr_deadline` | Daily | Warn before the SLA lapses |
| `pdpl.breach_countdown` | Hourly | Escalate toward the 72-hour deadline |
| `archive.verify` | Weekly | Sampled six-year record retrievable, hash still valid |

---

## 10. Judgment calls

| Call | Rejected alternative | Why |
|---|---|---|
| Separate clearing account per tender | One "card clearing" | E3.5 requires true net margin per method; merging destroys it |
| Legal hold blocks deletion, personal fields anonymised | Delete, or refuse the DSR | Both obligations are real; only anonymisation satisfies each |
| Consent checked at send time | Checked at list build | A withdrawal must take effect immediately |
| Breach countdown starts on insert | Started manually | 72 hours is short and cannot depend on memory |
| Later-phase compliance panels shown greyed | Hidden until built | The Owner should see the whole surface, including gaps |
| Bounced cheque is its own event | Status flip | It reverses a receipt and reinstates a receivable |
| Nil return producible | Skip empty periods | Filing is mandatory regardless of activity |
