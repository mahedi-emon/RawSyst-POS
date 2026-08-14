# 00 — Architecture Overview

**RawSyst POS** — Complete Retail ERP & POS for Saudi Arabia and international markets
Built by RawSyst IT · Founder: Mahedi Hasan Emon · rawsyst.com

| | |
|---|---|
| **Status** | System Design — Phase 1 detailed, Phases 2–5 outlined |
| **Source of truth for features** | `RawSyst-POS-Blueprint-v2.4-FINAL.md` (frozen) |
| **This document** | Architecture anchor. Read first. |
| **Last updated** | 2026-08-14 |

---

## 1. What we are building

A multi-tenant, offline-first Retail ERP + POS platform. Not a billing app — the promise is:

> Sell → Purchase → Stock → Warehouse → Customer → Supplier → Accounting → Employees → Payroll → Online Orders → Delivery → CRM → Analytics → Compliance → Multi-Store — everything in one platform, on any device, **working even when the internet doesn't**.

Saudi Arabia is the first, fully-built country configuration. It is a **configuration, not the architecture**.

### The ten principles that constrain every design decision

These come from blueprint A2 and are non-negotiable. Every doc in this set is checked against them.

1. **ERP, not POS** — every module posts to Accounting and Inventory automatically. No manual double entry anywhere.
2. **Compliance is core architecture**, not a plugin.
3. **Offline-first is a hard requirement** — sell with zero internet, sync later without duplicates.
4. **Nothing hard-coded to Saudi** — country, tax, currency, language are configuration.
5. **Every financial transaction is traceable** — who, what, when, where, before-value, after-value.
6. **Permissions enforced server-side.** A hidden button is never security.
7. **Finalized invoices are immutable.** Corrections only via Credit/Debit Note or Return.
8. **Heavy reports run async.** The POS never freezes waiting for a report.
9. **The POS must feel instant** — local-first scan → cart → payment → receipt.
10. **One click answers "where is my money going."**

### Wording discipline — applies to code, UI strings, docs, and marketing

| Never write | Always write |
|---|---|
| "ZATCA-certified" | "supports ZATCA requirements" |
| "certified compliant" | "built to support ZATCA and PDPL requirements" |
| "guaranteed compliant" | "WPS/Mudad-ready" |
| "never at legal risk" | — |

Passing ZATCA SDK validation is a **technical self-check, not ZATCA approval**. Final legal responsibility for WPS submission remains the employer's. This is enforced as a CI lint over `docs/`, UI locale files, and templates.

---

## 2. Who builds this, and what that means

**One developer.** This single fact drives more architectural decisions than any other:

- **A modular monolith, not microservices.** One Go binary, one deployment, one database. Bounded contexts are enforced by package boundaries and interfaces — not by network hops that one person then has to operate, monitor, and debug at 2am.
- **Docker Compose, not Kubernetes.** The blueprint's own stack table says exactly this: "Kubernetes only introduced later if genuinely needed at scale."
- **Boring, well-understood technology.** Chi over an exotic framework. Postgres over a distributed store. The blueprint's note on framework choice is explicit: prefer maintainability over raw benchmark speed, because this is a long-lived enterprise system.
- **The compliance-critical paths get disproportionate design effort.** Everything else can be refactored later. An invoice hash chain cannot.

---

## 3. System topology

```
                        ┌─────────────────────────┐
                        │      Super Admin        │   Next.js (platform control plane)
                        └───────────┬─────────────┘
                                    │
   ┌────────────────┬───────────────┼───────────────┐
   │                │               │               │
┌──▼───────┐  ┌─────▼──────┐  ┌─────▼──────┐        │
│ Tauri    │  │ Next.js    │  │ PWA        │        │
│ POS      │  │ Back-office│  │ Owner app  │        │
│ (desktop)│  │ (web)      │  │ (mobile)   │        │
│          │  └─────┬──────┘  └─────┬──────┘        │
│ SQLite   │        │               │               │
│ CSID key │        │               │               │
└──┬───────┘        │               │               │
   │  sync + submit │               │               │
   └────────────────┴───────┬───────┴───────────────┘
                            │  REST + WebSocket
                   ┌────────▼─────────┐
                   │   Go API         │  modular monolith, one binary
                   │  ┌────────────┐  │
                   │  │ event bus  │  │  domain events → Accounting, Inventory
                   │  └────────────┘  │
                   └────┬────┬────┬───┘
                        │    │    │
          ┌─────────────┘    │    └──────────────┐
   ┌──────▼──────┐    ┌──────▼──────┐    ┌───────▼────────┐
   │ PostgreSQL  │    │   Redis     │    │ Object storage │
   │ (RLS)       │    │ queue/cache │    │ S3-compatible  │
   └─────────────┘    └─────────────┘    └────────────────┘
                             │
                    ┌────────▼─────────┐
                    │  Worker pool     │  ZATCA submit · reports · notifications
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │  ZATCA Fatoora   │
                    └──────────────────┘
```

**Key reading:** Web and POS are **two front ends of one product**, not two products. They share the Go API and one core logic layer. The only logic that exists solely on the POS is what *must* be local: offline sale capture, local stock decrement, and ZATCA signing.

---

## 4. Tenancy model

### 4.1 The hierarchy

```
Group            one Owner login may hold several legal companies
  └── Company    legal entity — OWN books, OWN VAT registration, OWN ZATCA sequence
        └── Store          branch / showroom
              └── Terminal POS device — OWN CSID, OWN ICV counter, OWN PIH chain
```

This shape is forced by two requirements that pull in different directions:

- **F4 group consolidation** needs one login across multiple companies, with consolidated group P&L and balance sheet, and inter-company elimination.
- **E1/E2 compliance** needs books, VAT registration, and the ZATCA invoice sequence to belong to a **single legal entity** — never to the group.

So: `Group` is an access and reporting construct. `Company` is the accounting and compliance boundary. `Terminal` is the ZATCA chain boundary.

### 4.2 Isolation — Row-Level Security

Blueprint A3 requires tenant identity to be enforced **"at the database query layer, not just the frontend."** We implement that literally.

Every tenant-scoped table carries `tenant_id UUID NOT NULL`, and RLS is enabled:

```sql
ALTER TABLE sales_invoice ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON sales_invoice
  USING (tenant_id = current_setting('app.tenant_id')::uuid);
```

The API sets `app.tenant_id` from the verified JWT claim at the start of every request transaction. A query that forgets a `WHERE tenant_id = …` returns zero rows rather than another tenant's data.

**Why this and not schema-per-tenant or database-per-tenant:**

| Approach | Verdict |
|---|---|
| DB-per-tenant | Rejected. Migrations across N databases, connection-pool exhaustion, and backup complexity are all work a solo operator cannot absorb. |
| Schema-per-tenant | Rejected. Same migration problem, less isolation benefit than it appears. |
| **Shared schema + RLS** | **Chosen.** One migration path. Isolation enforced by the database engine, not by developer discipline. Directly satisfies QA gate M8 ("cross-tenant access via manipulated API requests must fail in every case") by construction rather than by testing every endpoint. |

The trade-off is real: a bug in the `app.tenant_id` plumbing is a systemic breach rather than a local one. Mitigation — that plumbing lives in exactly one middleware, has its own test suite, and there is an integration test that runs every endpoint as tenant A while asserting tenant B's rows are unreachable.

### 4.3 Data residency

Blueprint E4.2: *"do not force every client onto one global database."*

`Tenant.data_region ∈ {sa, eu, asia, other}` exists in the schema from day one. **v1 deploys the `sa` region only.** A region is a complete, independent stack — its own Postgres, its own object storage, its own workers. Adding a region is a deployment exercise, not a code change.

This is deliberately *not* implemented as one global database with a region column. Cross-border personal-data transfer is exactly what SDAIA's Transfer Regulation governs; the only defensible answer to an enterprise client asking "where is my data?" is a physically separate stack.

> ⚠️ The precise transfer conditions must be confirmed against the current SDAIA Transfer Regulation before a second region ships. See `90-regulatory-verification-checklist.md` item 10.

---

## 5. Backend structure

### 5.1 Package layout

```
cmd/
  api/            HTTP server entrypoint
  worker/         background job runner
  migrate/        migration CLI
internal/
  platform/       cross-cutting: config, logging, errors, db, auth, events, jobs
  registry/       Regulatory Rule Registry          ← everything legal reads from here
  identity/       tenants, companies, stores, users, roles, permissions, sessions
  catalog/        products, variants, categories, brands, units, barcodes
  inventory/      stock, movements, transfers, counts, batches, serials, costing
  sales/          carts, invoices, returns, credit/debit notes, quotations, orders
  zatca/          UBL builder, ICV/PIH chain, signing, Fatoora client, retry queue
  accounting/     COA, journal engine, posting rules, periods, statements
  payments/       tenders, gateway adapters, settlement, reconciliation
  purchasing/     requisitions, RFQ, PO, GRN, three-way match      [Phase 2]
  crm/            customers, loyalty, store credit, segments        [Phase 2]
  hr/             employees, attendance, payroll, WPS, GOSI, EOSB   [Phase 3]
  compliance/     PDPL, consent, DSR, retention, legal hold, breach
  reporting/      report definitions, builder, scheduling
  notify/         in-app, email, SMS, push, WhatsApp
  sync/           device sync protocol, queue, conflict resolution
pkg/              genuinely reusable, no domain knowledge
migrations/       numbered SQL migrations
```

Each `internal/<context>` package exposes a narrow `Service` interface and keeps its types private. Cross-context reads go through those interfaces; cross-context **writes** go through events.

### 5.2 The event bus, and why it exists

Principle #1 says every module must post to Accounting and Inventory automatically. Wiring that with direct calls produces a dependency knot — `sales` imports `accounting` imports `inventory` imports `sales` — that becomes unmaintainable within months.

Instead, domain modules emit events; Accounting and Inventory subscribe.

```go
// sales, on finalizing an invoice
bus.Publish(ctx, sales.InvoiceFinalized{
    TenantID: t, CompanyID: c, InvoiceID: id,
    Lines: lines, Tenders: tenders, OccurredAt: ts,
})
```

```go
// accounting subscribes
bus.Subscribe(func(ctx context.Context, e sales.InvoiceFinalized) error {
    return postingEngine.Post(ctx, "SALE_INVOICE", e)   // rule looked up as DATA
})
```

**The bus is synchronous and in-process, inside the same database transaction as the originating write.** This is the important detail. A sale and its journal entry and its stock movement either all commit or all roll back. An eventually-consistent bus here would let a sale exist without its accounting entry, which breaks the trial balance and fails QA gate M1.

Asynchronous work — ZATCA submission, report generation, emails — goes to the **job queue**, not the event bus. The distinction:

| | Event bus | Job queue |
|---|---|---|
| Timing | Same transaction, synchronous | Later, retried |
| Failure | Rolls back the originating write | Retries independently, alerts on exhaustion |
| Used for | Accounting postings, stock movements, audit entries | ZATCA submission, reports, notifications, backups |

---

## 6. The three pillars

Blueprint line 1658 is unusually direct about sequencing:

> **Design these three first, because every other module depends on them.** Get those three right and the remaining modules are conventional work. **Get them wrong and the entire system has to be rebuilt.**

| Pillar | Document | Must satisfy |
|---|---|---|
| **1 — Invoice state machine + ZATCA** | `01-invoice-zatca-engine.md` | M2: hash chain unbroken across 10,000+ invoices, ICV never resets or gaps, retry queue recovers after 24h outage |
| **2 — Double-entry posting engine** | `02-posting-engine.md` | M1: trial balance always balances, sub-ledgers tie to control accounts, closed periods truly locked |
| **3 — Sync & idempotency** | `03-sync-idempotency.md` | M3: 500 invoices sold fully offline → zero duplicates, zero lost invoices, correct hash chain order |

### 6.1 The signing-location decision

The blueprint contradicts itself here, and resolving it correctly is the single most consequential call in this design.

- **J3 (line 1352)** shows a flow where the invoice is signed **in the cloud, after sync**.
- **E1.3 RULE 1 (line 813)**, marked **"LOCKED — DECIDED"**, states: *"the device's CSID private key and the local ICV/PIH chain must exist on the terminal itself, not only in the cloud. **Signing is a LOCAL operation.**"*
- **Part L (line 1483)** independently mandates *"offline signing architecture (sign locally, transmit later)."*
- **Part M item 3** requires a correct, unbroken hash chain after **500 fully-offline sales** — which cloud signing cannot produce, because the chain would not exist until reconnection.

**Decision: signing is local.** E1.3 is explicitly locked, is far more specific, is corroborated by Part L, and is the only reading consistent with the Part M acceptance test. J3 is a simplified diagram and should be amended in the blueprint.

The consequence is a real security problem, which we solve rather than ignore: the ECDSA private key must live on a Windows desktop, while H1 forbids keys in source or plain `.env`. Custody design is in `01-invoice-zatca-engine.md` §Key custody — OS-native secure storage (DPAPI / Credential Manager) through Tauri, never in the SQLite file, never in application code.

---

## 7. Frontend architecture

| Surface | Tech | Role | Offline |
|---|---|---|---|
| **Tauri POS** | Tauri + React + TS + SQLite | Showroom billing counter. Direct hardware: HID scanner, ESC/POS printing with no browser dialog, RJ11 drawer kick | **Fully operational with zero internet** |
| **Next.js back-office** | Next.js + TS + Tailwind + shadcn/ui + TanStack Query + Zod | Configuration, deep accounting, bulk operations, all reports, multi-store oversight | Online |
| **PWA owner app** | Same Next.js build, installable | Live monitoring, approvals, stock check, notifications | Key screens cached |

**One design system across all three** (`docs/ui-ux/00-design-system.md`). The product must feel identical everywhere.

Full RTL mirroring for Arabic is a layout requirement, not a translation requirement — menus, receipts, invoices, and dashboards all mirror. Mixed Arabic/English product names and numerals must render correctly (QA gate M6).

---

## 8. Performance budget

From blueprint J4. These are design constraints, not aspirations:

| Operation | Budget | How the design meets it |
|---|---|---|
| Barcode scan → product in cart | Near-instant | Local SQLite lookup, **no network round-trip** |
| Cart update | **< 100 ms** | All cart maths local; no server call until finalize |
| Payment completion | Immediate | Local-first transaction close; sync is asynchronous |
| Dashboard / standard API query | **< 500 ms** | Indexed, tenant-scoped, paginated |
| Heavy reports | Async always | Job queue + notification on completion |
| POS availability | **100% offline-capable** | Everything the till needs is in local SQLite |

---

## 9. Cross-cutting invariants

Four things must hold everywhere. They are listed here because each is enforced in more than one module, and a reviewer should check all four on any change.

1. **Immutability** — finalized invoices, posted journal entries, closed fiscal periods, and audit log entries can never be edited or deleted **by anyone, including Owner and Super Admin**. Enforced at the database level with triggers, not only in application code.
2. **Compliance is derived, never toggled** — `country + VAT registration + ZATCA obligation → compliance engine ON`. There is no boolean a Super Admin can flip to disable ZATCA, VAT calculation, invoice immutability, audit logging, or PDPL handling. Only compliance *monitoring* is a paid feature.
3. **No legal value in code** — no rate, threshold, deadline, SLA, or file format is hard-coded anywhere. All resolve through the Regulatory Rule Registry **at the transaction's date**, so a March report uses March's VAT rate even if the rate changed in June.
4. **Server-side authorization on every endpoint** — no exceptions, no "internal" endpoints exempted. QA gate M7 tests this by calling every restricted action directly as a Cashier.

---

## 10. Deployment

```
┌──────────────────────── Saudi region (v1) ────────────────────────┐
│  Nginx  →  Go API (n replicas)  →  PostgreSQL (primary + replica) │
│              │                                                     │
│              ├──→  Redis (queue, cache, rate limit)                │
│              ├──→  Worker pool                                     │
│              └──→  S3-compatible object storage                    │
│                                                                    │
│  Prometheus + Grafana · Sentry · KMS/Vault                         │
└────────────────────────────────────────────────────────────────────┘
```

Docker Compose to start. GitHub Actions runs tests, migrations, security scanning, and deployment.

**Recovery objectives** — the blueprint states a daily backup cadence but no numeric targets, so they are set here:

| Objective | Target | Mechanism |
|---|---|---|
| **RPO** | **15 minutes** | Continuous WAL archiving to object storage + daily verified AES-256 base backup |
| **RTO** | **4 hours** | Documented, **quarterly-rehearsed** restore procedure |
| POS local data | **0** (no loss) | Sale is durable in local SQLite before the receipt prints |

The last row matters most in practice: a total cloud outage does not lose a single sale, because the terminal is the system of record until sync succeeds.

---

## 11. Plan tiers and ceilings

Blueprint A3 requires every "unlimited" claim to become a concrete, testable ceiling. Proposed defaults — tunable per tenant by Super Admin:

| Limit | Starter | Professional | Business | Enterprise |
|---|---|---|---|---|
| Stores | 1 | 3 | 10 | 50 |
| Users | 5 | 20 | 75 | 300 |
| POS terminals | 2 | 6 | 25 | 100 |
| SKUs (incl. variants) | 5,000 | 25,000 | 150,000 | 500,000 |
| Held carts per terminal | 10 | 20 | 20 | 20 |
| Custom roles | 2 | 10 | 30 | 100 |
| Storage | 2 GB | 20 GB | 100 GB | 500 GB |
| SMS credits / month | 100 | 1,000 | 5,000 | 25,000 |
| Companies in group | 1 | 1 | 3 | 20 |

Ceilings are enforced server-side and surfaced in the UI before the limit is hit, never as a hard failure at the moment of use.

---

## 12. Document map

| Doc | Contents |
|---|---|
| `00-architecture-overview.md` | **This document** |
| `01-invoice-zatca-engine.md` | Pillar 1 — invoice state machine, ICV/PIH, local signing, key custody, Fatoora |
| `02-posting-engine.md` | Pillar 2 — journal engine, posting rules as data, periods, costing, returns |
| `03-sync-idempotency.md` | Pillar 3 — sync protocol, idempotency, conflict policy, offline stock |
| `04-identity-tenancy-rbac.md` | Auth, MFA, roles, permission model, scoping, data masking |
| `05-regulatory-rule-registry.md` | Effective-dated legal values, staleness alerting, governance |
| `06-data-model.md` | Core ERD, shared kernel entities, naming conventions |
| `07-api-conventions.md` | REST shape, errors, pagination, idempotency headers, versioning |
| `08-background-jobs.md` | Queue design, job types, retry policy, alerting |
| `10-*.md` | Phase 1 modules in depth |
| `20-later-phases.md` | Phases 2–5 module boundaries and integration points |
| `90-regulatory-verification-checklist.md` | The Part N Tier 1 verification pass, for the owner's advisors |

---

## 13. Known open items

Tracked here so they are not silently forgotten.

| # | Item | Owner | Blocks |
|---|---|---|---|
| 1 | Tier 1 verification of all 12 Part N claims | Founder + Saudi tax advisor | Populating the registry with real values; ZATCA byte-level implementation |
| 2 | Mudad wage-file format, GOSI dated rate schedule, ZATCA XML/QR schema version | Founder | Phase 3 payroll; Phase 1 ZATCA XML |
| 3 | Legal review — PDPL posture + tenant data processing agreement | Legal counsel | Go-live |
| 4 | SDAIA cross-border transfer conditions | Legal counsel | Second data region |
| 5 | Product name / trademark clearance | Founder | Branding freeze |

**None of these block writing code.** The registry machinery, the state machines, and the module structure are all designed to accept verified values as data when they arrive.
