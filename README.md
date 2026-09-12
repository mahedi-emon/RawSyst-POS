<p align="left">
  <img src="web-next/public/brand/biz1core-logo-tagline.svg" alt="Biz1core — One Solution for Complete Business Management." width="360">
</p>

**One Solution for Complete Business Management.**

Biz1core is a multi-tenant, offline-first retail ERP and point of sale. Not a
till with reports bolted on: selling, stock, buying, money and people are one
system, and every module posts to Accounting and Inventory automatically.

Built for Saudi Arabia first — ZATCA e-invoicing, VAT and PDPL are part of the
architecture rather than a later module — and configured, never hard-coded, for
Bangladesh and the United States.

---

## What it does

| | |
|---|---|
| **Selling** | Offline-first till, held and resumed sales, returns, exchanges, credit sales, shifts with X- and Z-reports, card settlement |
| **Stock** | Products and variants, batches and expiry, multi-store transfers, recalls, stock counts |
| **Buying** | Suppliers, purchase orders, goods receipts, bills, three-way match, payments, ageing |
| **Money** | Double-entry accounting, chart of accounts, journals, expenses, treasury, receivables, VAT return |
| **People** | Employees, HR records, payroll, commissions, approvals, role-based permissions enforced server-side |
| **Compliance** | ZATCA Phase 2 e-invoicing, signed documents, regulatory source register, full audit trail |
| **Oversight** | Owner dashboard with drill-through, scheduled report e-mail, notifications, backups and point-in-time recovery |

Seven non-negotiables shape all of it — ERP not POS, compliance as
architecture, offline-first, nothing hard-coded to one country, every financial
transaction audit-trailed, permissions enforced on the server, finalised
invoices immutable. They are stated in full in
[the blueprint](Biz1core-Blueprint-v2.4-FINAL.md).

## How it is built

| Part | Technology | Where |
|---|---|---|
| API and workers | Go | [`backend/`](backend/) |
| Back office (installable PWA) | Next.js + TypeScript | [`web-next/`](web-next/) |
| Till | Tauri + React | [`pos/`](pos/) |
| Screens both front ends share | React + TypeScript | [`shared/`](shared/) |
| Cloud database | PostgreSQL, row-level security per tenant | [`deploy/postgres/`](deploy/postgres/) |
| Till's local database | SQLite | [`pos/src/offline/`](pos/src/offline/) |
| Cache and queue | Redis | — |
| Infrastructure | Docker, Nginx, GitHub Actions | [`deploy/`](deploy/) |

`web/` is the previous back office. It is not built, not deployed and not
tested; `web-next` is the product.

## Running it

```sh
make help                 # every target, with a line each
```

Databases, from zero:

```sh
make fresh-db             # rebuild the TEST database from the migration chain
make fresh-dev            # rebuild and reseed the DEVELOPMENT database
```

Checks, in the order they are cheapest to run:

```sh
make verify               # typecheck, lint, both test suites, contract, reachability, build
make test-backend         # the Go suite against the test database
make test-web             # the front-end and shared suites
make typecheck            # TypeScript, every workspace
make build-web            # production build of the back office
```

The stack:

```sh
make up-small             # bring it up with the 8 GB profile
make down                 # stop it, keeping every volume
make images               # build every production image and report their sizes
```

Copy `.env.example` to `.env` first. Every variable in it is documented in
place; the ones without a default will stop the API starting rather than let it
run on a guess.

## Documentation

| | |
|---|---|
| [Blueprint](Biz1core-Blueprint-v2.4-FINAL.md) | The functional specification, parts A–O |
| [System design](docs/system-design/) | Architecture, data model, API conventions, running it |
| [UI/UX specification](docs/ui-ux/) | Every screen, and the design system behind it |
| [Operating guide](docs/OPERATING-GUIDE.md) | What to do when the product is live |
| [Project status](docs/PROJECT-STATUS.md) | What is built, what is proved, what is blocked |
| [Branding](docs/BRANDING.md) | The brand assets, and the old identifiers deliberately kept |
| [Deployment across domains](docs/DEPLOYMENT-SPLIT-DOMAINS.md) | Serving the console on its own hostname |
| [Backup and recovery](deploy/server/BACKUP.md) | Backups, verification, drills, point-in-time recovery |

## Built by

**Mahedi Hasan Emon** — Founder, Owner &amp; Lead Developer

- Website: <https://mahedihasanemon.site/>
- LinkedIn: <https://www.linkedin.com/in/mahediemon/>
- GitHub: <https://github.com/mahedi-emon>

Repository: <https://github.com/mahedi-emon/Biz1core>

&copy; 2026 Mahedi Hasan Emon. All rights reserved.
