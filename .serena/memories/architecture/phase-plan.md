# RawSyst POS — Phase Plan & Build Order

Approved 2026-08-14. Full design written up front; **build proceeds in phases**. Solo developer.

## Product phases

| Phase | Contents | Gate |
|---|---|---|
| **1 — Sellable Saudi core** | Tenancy/auth/RBAC · Regulatory Rule Registry · Products + variants + inventory · **Offline POS with local ZATCA signing** · Sync engine · Posting engine + core postings · TB/P&L/BS · VAT return prep · Mada + cash + card + split tender · Shift X/Z · Numbering + receipt templates · Arabic RTL · core PDPL | **A Saudi shop can legally trade on it** |
| **2 — Complete retail ops** | Purchases (requisition→RFQ→PO→GRN→3-way match) · Suppliers · AR/AP · CRM + loyalty + store credit · full returns/exchange · promotions engine · barcode + label studio · transfers + stock count · bank reconciliation · payment settlement · report suite | Full retail ERP |
| **3 — HR & Saudi labour** | Employees · attendance · payroll · commission · **WPS/Mudad · GOSI · EOSB** · fixed assets | Employer-ready |
| **4 — Extended commerce** | Online orders + delivery · wholesale B2B · installments/EMI · warranty & service · customer + supplier portals · e-commerce law compliance | Omnichannel |
| **5 — Enterprise** | Workflow/approval engine · Approval Center · multi-company consolidation · analytics/forecasting · API + webhooks · migration wizard · advanced compliance monitoring | Enterprise deals |

**Note:** core PDPL is in Phase 1, not deferred — PDPL is in active enforcement, applies extraterritorially, and fines reach SAR 5M per violation. A simplified manager-PIN discount gate ships in Phase 1; the full configurable workflow engine waits for Phase 5.

## Backend build order (Phase 1) — strict dependency order

1. **Repo skeleton** — Go modules, config, structured logging, error model, migration tooling, Docker Compose, CI
2. **Tenancy + RLS + auth/JWT + RBAC** — nothing is safe to build before this exists
3. **Regulatory Rule Registry** — every legal value flows from here; building tax logic first would hard-code what must be data
4. **Chart of Accounts + journal engine + posting-rule table** — **PILLAR 2**
5. **Products, variants, inventory, costing engine**
6. **Sales/invoice domain + ZATCA engine** — **PILLAR 1**
7. **Sync API + local SQLite schema** — **PILLAR 3**
8. **Payments/tenders + shift management**
9. **Reports + VAT return preparation**

Frontends follow the API: **Tauri POS first** (the compliance-critical surface), then Next.js back-office, then PWA.

## The three pillars — must be right or everything gets rebuilt
Blueprint line 1658 names these explicitly. Each has a Part M acceptance gate that the design must satisfy on paper before code:
- **Pillar 1 — Invoice/ZATCA:** hash chain unbroken across 10,000+ invoices, ICV never resets or gaps, retry queue recovers after 24h outage (M2)
- **Pillar 2 — Posting engine:** trial balance always balances, sub-ledgers tie to control accounts, closed periods truly locked (M1)
- **Pillar 3 — Sync:** 500 invoices sold fully offline → zero duplicates, zero lost invoices, correct hash chain order (M3)

## Out of scope for v1
Full Manufacturing ERP — BOM, production orders, work orders, material issue, WIP, by-products, routing, capacity planning, variance analysis. Blueprint C3.1 rules it out explicitly. **Light production cost tracking stays in.** ML forecasting is architecture-ready only, not built.

## Blocked on the owner, not on engineering
Populating the Regulatory Rule Registry with **verified** values needs the Part N Tier 1 pass (ZATCA, SDAIA, GOSI, Mudad, SAMA, MoC official sources) plus review by a Saudi tax advisor and legal counsel. Blueprint: *"Do not let developers fill these in from assumption."* Design ships with placeholders + a verification checklist.

## Related
[[architecture/decisions]] · [[blueprint/part-e-saudi-compliance]] · [[blueprint/part-f-k-architecture]]
