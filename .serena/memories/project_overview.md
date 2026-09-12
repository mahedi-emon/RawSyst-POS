# Biz1core — Project Overview

## Product

**Biz1core** — *One Solution for Complete Business Management.*

A multi-tenant, offline-first retail ERP and point of sale. Not a POS with
reports bolted on: sales, purchase, stock, warehouse, customers, suppliers,
accounting, HR/payroll, online orders, delivery, CRM, analytics, legal/tax
compliance and multi-store are one system, and every module posts to Accounting
and Inventory automatically.

Saudi Arabia first (ZATCA e-invoicing, VAT, PDPL are architecture, not a later
module), configured rather than hard-coded for Bangladesh and the United States.

- Owner / Founder / Lead Developer: Mahedi Hasan Emon (mahedi.emon62@gmail.com)
- Website: https://mahedihasanemon.site/ · LinkedIn: https://www.linkedin.com/in/mahediemon/ · GitHub: https://github.com/mahedi-emon
- Repository: https://github.com/mahedi-emon/Biz1core

**The product was called RawSyst POS until 2026-09-12.** The rename changed
everything a person reads. It deliberately did NOT change the `RAWSYST_*`
environment variables, the PostgreSQL database/role names, the backup artefact
names and encryption magic, the Prometheus metric names, the session cookie
names, the `rawsyst.auth`/`rawsyst.live` wire identifiers, or the compose
project name. **Read `docs/BRANDING.md` before "fixing" any remaining
`rawsyst` you find** — most of what is left is load-bearing.

There is no company entity and no production domain. Do not invent one.

## Where things live

| Part | Stack | Path |
|---|---|---|
| API + workers | Go (module `github.com/mahedi-emon/Biz1core/backend`) | `backend/` |
| Back office (the product) | Next.js + TypeScript | `web-next/` |
| Till | Tauri + React | `pos/` |
| Shared screens + i18n catalogues | React + TypeScript, package `@biz1core/shared` | `shared/` |
| Previous back office — dead: not built, not deployed, not tested | Next.js | `web/` |
| Brand: constants, logo component, tokens | — | `shared/src/brand/` |
| Brand files (SVG, PNG, ICO), generated | — | `web-next/public/brand/`, `scripts/brand-assets.mjs` |
| Cloud DB (RLS per tenant), local DB, cache | PostgreSQL, SQLite, Redis | `deploy/postgres/`, `pos/src/offline/` |

One binary, many commands: `biz1core api|worker|migrate|bootstrap|regulatory|ingest|backup`.

## Non-negotiable architectural principles (blueprint Part A2)

1. ERP, not just POS — every module talks to Accounting and Inventory automatically.
2. Compliance (ZATCA, VAT, PDPL) is core architecture, not bolted on later.
3. Offline-first is a hard requirement — the till sells with zero internet and syncs later without duplicates.
4. Nothing hard-coded to Saudi — country/tax/currency/language are configuration.
5. Every financial transaction is fully audit-trailed (who/what/when/where/before/after).
6. Permissions enforced server-side, never only hidden in UI.
7. Finalized invoices are immutable — corrections only via Credit/Debit Notes or Returns.

## Specification

`Biz1core-Blueprint-v2.4-FINAL.md` (frozen), parts A–O:
A Platform Foundation (tenancy, RBAC, onboarding) · B Core Retail Ops · C Finance
& Ops (double-entry, expenses, HR, payroll, shifts) · D Intelligence &
Communication · E Saudi Legal/Tax/Payment Compliance · F Workflow Engine &
External Portals · G Internationalization · H Platform Engineering & Security ·
I Settings & Customization · J Technical Architecture · K Module Tree · L/M
Checklists · N Regulatory Source Hierarchy · O Requirements Traceability.

Also: `docs/system-design/`, `docs/ui-ux/`, `docs/PROJECT-STATUS.md`,
`docs/OPERATING-GUIDE.md`, `docs/BRANDING.md`.

## Commands

`make help` lists everything. The ones used most: `make verify` (typecheck,
lint, both suites, contract, reachability, build), `make test-backend` (needs
`RAWSYST_DB_DSN`), `make test-web`, `make fresh-db` / `make fresh-dev`.
