# RawSyst POS — Binding Architecture Decisions

Decided 2026-08-14, approved by the owner. These are settled; do not re-litigate without a stated reason.

## Context that drives everything
- **ONE solo developer** (founder Mahedi Hasan Emon) builds and maintains this. Architecture must be realistic to run alone.
- Stack is **frozen** by blueprint J1 — Go backend, Next.js+TS web, Tauri+React desktop POS, PostgreSQL, SQLite local, Redis, S3-compatible storage, Docker Compose, GitHub Actions.
- Build order: **backend first**, then Tauri POS, then Next.js back-office, then PWA.

## Decisions

| # | Decision | Choice | Rationale |
|---|---|---|---|
| 1 | **Backend shape** | **Go modular monolith** — one deployable binary, bounded-context packages under `internal/` with explicit interfaces | Solo builder; J1 itself says Docker Compose now, "Kubernetes only later if genuinely needed" |
| 2 | **Tenancy enforcement** | Shared PostgreSQL schema, `tenant_id` on every table, enforced by **Row-Level Security** + JWT claim | A3 demands enforcement "at the database query layer, not just the frontend". RLS makes cross-tenant leakage structurally impossible ⇒ satisfies QA gate M8 by construction |
| 3 | **Org hierarchy** | `Group → Company → Store → Terminal` | F4: one Owner login, multiple legal companies, each with **separate books + separate VAT registration + separate ZATCA sequence**. ZATCA chains live at **Terminal** level |
| 4 | **Data residency** | Region-isolated deployment stacks (DB + object storage per region). **Saudi region only for v1**, but `region` is a tenant attribute from day one | E4.2. Keeps it pluggable without redesign |
| 5 | **ZATCA signing location** | **LOCAL on the POS terminal** — per-device CSID, per-device ICV/PIH chain, never merged, never re-sequenced by cloud | **E1.3 is marked "LOCKED — DECIDED"** and states the CSID key and chain "must exist on the terminal itself". **This contradicts the J3 flow diagram (line 1352) which shows cloud-side signing.** E1.3 is more specific and explicitly locked ⇒ **E1.3 wins; J3 is a simplification to be corrected in docs.** Part L independently confirms: "offline signing architecture (sign locally, transmit later)" |
| 6 | **CSID key custody** | OS-native secure store (Windows DPAPI / Credential Manager) via Tauri. Key never in source, `.env`, or plain SQLite. Cloud KMS holds only onboarding credentials, never the device signing key | Resolves the genuine conflict between E1.3 (key must be on device) and H1 (keys never in plain storage) |
| 7 | **Module coupling** | **Internal event bus** — domain modules emit events; Accounting and Inventory subscribe | A2 principle #1 requires every module to post to Accounting and Inventory automatically with no manual double entry. Direct calls would create a dependency knot |
| 8 | **Costing methods** | **WAC · FIFO · Standard Cost with variance**, + Landed Cost overlay on any method | C13 is the newer authoritative section. B4 and D1 list only FIFO+WAC — **C13 wins**, B4/D1 to be reconciled |
| 9 | **Permission model** | Extensible **per-module verb set**, not a fixed global enum | A6.2's enumerated list omits Hold/Exchange/Receive Payment, which appear in its own worked example |
| 10 | **Compliance feature flags** | Compliance capability is **derived state**, structurally not toggleable. Only compliance *monitoring* is premium | A4 HARD RULE + v2.2 correction #1. **Overrides H5's "ZATCA module" plan entitlement** |
| 11 | **Posting rules** | Defined **as data in a posting-rule table**, evaluated by an engine — not hard-coded posting code | C9.2 says rules 6–12 each need "its own **defined, configurable** posting rule" |
| 12 | **Immutability** | A **shared architectural primitive**, applied in 4 places: finalized invoices · posted journal entries · closed fiscal periods · append-only audit log | A2#7, C9.1, C10, D4 all demand it independently |
| 13 | **Values I set (blueprint left open)** | RPO/RTO targets; concrete plan-tier ceilings for stores, users, terminals, SKUs, held carts, storage, SMS credits | A3 requires every "unlimited" claim to become a concrete ceiling; H4 states no numeric RPO/RTO |

## Deliverable locations
- Design docs: `docs/system-design/` (numbered, build order = doc order)
- UI/UX: `docs/ui-ux/`
- Regulatory checklist for the owner's advisors: `docs/system-design/90-regulatory-verification-checklist.md`
- Plan file: `C:\Users\USER\.claude\plans\serena-run-koro-sob-eager-mochi.md`

## Related memories
[[blueprint/part-a-d-functional]] · [[blueprint/part-e-saudi-compliance]] · [[blueprint/part-f-k-architecture]] · [[architecture/phase-plan]]
