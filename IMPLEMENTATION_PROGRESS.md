# RawSyst — Implementation Progress

Session-continuity file. **Long-form history lives in
[`docs/PROJECT-STATUS.md`](docs/PROJECT-STATUS.md)** — this file is the short
answer to "what is true right now and what do I do next", so the two do not
duplicate each other. Serena memories `audit/verified-2026-09-02-directive` and
`architecture/counter-and-market` carry the reasoning.

| | |
|---|---|
| **Last verified** | 2026-09-04 |
| **Branch** | `international-markets-and-counters` |
| **Scale** | 124 migrations · 463 routes · 102 permissions · 1,165 Go test functions |
| **Direction** | **Greenfield front end in `web-next/` — see section 0.** Web first. POS is a module inside the web app. **ZATCA skipped and isolated.** Tauri deferred. |

---

## 0. Frontend rebuild — `web-next/` (started 2026-09-04)

The greenfield Next.js 16 front end. `web/` is **frozen**, not deleted: it stays
as reference until `web-next` reaches parity, and nothing in it is the
foundation of anything here.

| | |
|---|---|
| **Location** | `web-next/` (npm workspace, port 3001) |
| **Stack** | Next.js 16.3.2 · React 19.2 · TypeScript 5.5 (strict, `noUncheckedIndexedAccess`) · Tailwind 4 · App Router |
| **Build** | ✅ clean, no warnings |
| **Typecheck** | ✅ clean |
| **Tests** | ✅ 57 frontend (money 17 · cart 18 · navigation/RBAC 18 · i18n/RTL 4) + 6 i18n coverage checks |
| **Contract** | ✅ 463 routes · **109 permissions** (102 route-gated + 7 action-level) · 28 plan-gated groups |
| **Live backend** | ✅ **validated** — see §0.2 |

### 0.1 The decision the rest of it rests on: the contract is generated

`web-next/src/lib/api/contract.generated.ts` is produced by
`scripts/generate-api-contract.mjs`, which parses `backend/internal/api/router.go`,
`entitlement.go` **and the seeded permission catalogue and role grants in
`db/migrations`**.

A hand-written TypeScript permission list would be a second answer to a question
the backend already answers, and the TypeScript one would be wrong first.

```
npm run gen:contract     # regenerate
npm run check:contract   # fails when it has drifted from the Go source
```

`navigation.test.ts` walks the whole business navigation tree and asserts every
permission it names exists in that generated list, **and** that no route guard
names a permission which gates no route.

### 0.2 GATE 1 — live backend validation ✅ DONE

Run against a real server: `cmd/api` built from source, Postgres 17 in Docker on
:5433, 124 migrations, `cmd/devseed` tenant "Demo Retail" (SA / SAR).

**The environment had to be corrected first.** The `postgres` image creates
`POSTGRES_USER` as a SUPERUSER, and a superuser ignores row-level security
entirely — so the first run had no tenant isolation at all and
`TestConsumeRefusesACompanyInAnotherTenant` failed. The repo's own
`TestConnectionCannotBypassRowLevelSecurity` documents this exact trap. The
schema was rebuilt owned by a `rawsyst_app` role created `NOSUPERUSER
NOBYPASSRLS`; both tests then passed, and **every validation below was performed
with RLS actually enforcing**.

#### Verified against the live API

| Contract | Result |
|---|---|
| `POST /auth/login` → `access_token` | ✅ |
| Cookies: `rawsyst_csrf` (path `/`, readable) + `rawsyst_refresh` (path `/api/v1/auth`, httpOnly) | ✅ exactly as the client assumes |
| `GET /auth/me` → `user_id`, `tenant_id`, `is_super_admin`, `permissions[]` | ✅ 109 permissions for an Owner |
| `GET /companies` → `{id, legal_name, trade_name, country, base_currency}` | ✅ (`country` arrives lowercase; `marketOf` uppercases) |
| `GET /subscription/entitlements` → `{data:[{feature, allowed, in_plan}]}` | ✅ |
| `GET /dashboard/overview` | ✅ every field of the `Overview` interface |
| `GET /catalog/products` → `{data, page:{cursor, has_more, limit}}` | ✅ |
| `GET /customers` → `{data:[…]}` (no `page` envelope) | ✅ |
| `GET /search` → `{kind, id, label, detail, amount, currency}` | ✅ requires `company_id` |
| `GET /permissions` → catalogue with `section`, `label`, `label_ar`, `label_bn`, `caution`, `holds` | ✅ |
| Error envelope `{error:{code,message,fields?,request_id}}` | ✅ |
| 400 / 401 / 403 / 404 / 409 / 422 codes and human messages | ✅ |
| 404 (not 403) for a foreign record | ✅ "That customer was not found." |
| `POST /auth/refresh` with CSRF header → 200; **without → 403** | ✅ |
| `POST /pos/counter-sessions` → counter-bound token carrying `did` | ✅ |
| `/pos/stock` and `/pos/sales` refuse a session with no `did` | ✅ |
| Shift gate: a sale before `POST /shifts` → 409 | ✅ |
| **A complete sale**: 125 tax-inclusive → net 108.70 + tax 16.30 @ 0.15, with `zatca{icv:1,pih,schema_version}` | ✅ **201** |
| `Idempotency-Key` replay → **200 + `Idempotency-Replayed: true`, same `invoice_id`, same ICV** | ✅ no second sale |
| Stock enforcement: selling below zero refused with a human sentence | ✅ |
| Compliance gate: a Saudi sale refused `compliance_blocked` until the branch National Address is complete, citing BR-KSA-09/37/66 | ✅ |

#### Mismatches found in the frontend — all fixed

| # | Finding | Fix |
|---|---|---|
| M1 | `GET /pos/counters` **requires `company_id`**; the picker sent none → 400 every time | `useCompanyScope()`, request deferred until it resolves |
| M2 | `POST /pos/sales` **requires `tax_treatment` per line**, and `GET /catalog/scan` does not return it — a till built on `scan` rings up all day and fails at payment | The till now loads **`GET /catalog/snapshot`** (barcode + price + `tax_treatment`) and scans locally. `CartLine.taxTreatment` made **required**, so the type system refuses a line that would be rejected |
| M3 | The sale response has **no `human_number`** | `CompletedSale` retyped from the live payload; the receipt shows the net/tax/total split the server computed |
| M4 | **A shift must be open before selling** (409) | `useShift()` + `OpenShift` screen: count the drawer, declare the float, optional blind close |
| M5 | Dashboard `attention[].link` returns **old-frontend routes** (`/inventory?filter=out`) → 404 | `routeFor()` maps them; mapped on this side because the API is shared with the Tauri till |
| M6 | `GET /search` also requires `company_id` | Recorded for FE-36 |
| M7 | An Owner holds **109** permissions; only **102** gate a route | Generator widened to the seeded catalogue; `ROUTE_PERMISSIONS` keeps the distinction, and a test forbids guarding a route with a non-route permission |

#### Backend defects found and fixed

Both in `estimateUnitCost` (`internal/stockops/adjust.go`), on the fallback path
that runs when a variant has no cost layer yet — the first adjustment a new shop
ever posts. Two bugs in six lines, so the path had never executed.

1. **`ORDER BY created_at` on `cost_layer`, which has no such column** → 500
   `SQLSTATE 42703`. The column is `received_at`, and `cost_layer_fifo_idx` is
   built on it.
2. **Three arguments passed to a two-placeholder query** → 500
   `expected 2 arguments, got 3`. Each query now carries its own argument list.

`TestFoundStockIsPricedForAVariantThatWasNeverReceived` pins both. It was
**verified to fail on the defective code and pass on the fix**, not merely
written. `POST /stock/adjustments` now returns 201.

#### Not verifiable this session

- MFA challenge — no account has a second factor enrolled.
- Multi-business tenant choice — `devseed` creates one tenant per email.
- Employee/cashier 403 against a live server — no employee account seeded.
- 429 rate limiting, 503.
- ZATCA onboarding beyond status — needs an `environment` query parameter, and a
  real CSID needs a Fatoora OTP, which must never be fabricated.

#### Found in the second pass (Customers and Sales)

| # | Finding |
|---|---|
| M11 | **There is no "list every invoice" route.** The sales surface is `GET /dashboard/sales?company_id&date&limit`, which answers with one day's invoices AND that day's totals. Building a scrolling all-time list would have meant inventing an endpoint. The screen is a day view with a date stepper, which is also how a shop reconciles. |
| M12 | `/customers` answers `{data}` with **no `page` envelope**, unlike `/catalog/products`. The shared list treats a missing envelope as "this is all of it" rather than as a fault. |
| M13 | A sale row's `issued_at` is the **time of day only** (`"13:22"`) — the date is the page's subject. |
| M14 | `/platform/*` answers **404, not 403**, to a business user. Deliberate: confirming a platform endpoint exists tells an attacker where to aim. An earlier expectation of 403 in the verification harness was wrong, not the product. |
| M15 | `cmd/devseed` leaves its EGS unit with an **empty VAT number**, so every Saudi sale fails the compliance gate until it is amended. Fixture data, not a product defect — but it is why a fresh dev database cannot sell until `PUT /einvoicing/units/{id}` is called. |

#### Other live findings that are UI requirements

- A **Saudi terminal requires an EGS unit** to register. A session-bound counter
  is created `active`; a paired one stays `pending` until a machine enrols. The
  device form must be market-aware.
- `POST /stock/adjustments`: `kind` ∈ {adjustment, wastage}; `reason` is an enum
  (`correction, data_entry, found, other`). Both must be selects, not free text.
- EGS `architecture` ∈ {`centralized_server`, `branch_server`, `smart_pos`}, and
  a central unit must **not** carry a `store_id`.

### 0.3 Architectural decisions

| # | Decision | Why |
|---|---|---|
| F1 | **The API is proxied through Next** (`rewrites` → `RAWSYST_API_ORIGIN`) | The refresh cookie is `SameSite=Strict` and the CSRF cookie must be readable by `document.cookie` on the page that echoes it. Also removes CORS entirely. |
| F2 | **Data fetching is client-side, not in Server Components** | The access token lives in memory by deliberate backend design; a Server Component cannot hold it, and copying it into a second cookie would undo the protection. |
| F3 | **The company is application state, not a screen filter** | `company_id` is required on every dashboard, report and POS-counter route. `useCompanyScope()` returns `null` until it resolves and `useApi` treats a null path as "not yet". |
| F4 | **The counter is exchanged for a token, and re-exchanged after reload** | POS routes read the till from the signed `did` claim. An ordinary refresh returns a token without it, so the counter id is kept in session storage. |
| F5 | **Money never becomes a `number`** | `formatMoney` walks the decimal string; the cart uses `decimal.js`. |
| F6 | **Tailwind + shadcn primitives adopted** — reopening `FRONTEND_TOOLBOX.md` §4 | Mandated by the rebuild brief. **Scope: `web-next/` only.** |
| F7 | **`shared/` is reused for data, never for UI** | `@rawsyst/shared/i18n/strings` is imported; no component, panel or stylesheet is. |
| F8 | **The till scans locally from the catalogue snapshot** | Forced by M2, and it is what the snapshot endpoint exists for. One network call per shift instead of one per beep. |

### 0.4 Design language

Tokens in `src/styles/globals.css`. One colour family: deep teal-green primary
`#0B6B58`, every neutral that green desaturated, ink `#0F1B18` as its darkest
value — not a tinted black. Brass `#C8A227` is focus and selection **only**.
IBM Plex Sans + IBM Plex Sans Arabic (a designed sibling) + Noto Sans Bengali,
self-hosted. Tabular figures on every money column. Tables are the hero; a total
row carries `rule-total`, the accounting double rule. No gradient washes, no
identical rounded cards, no ALL-CAPS eyebrows, no monospace data labels, no
scroll-triggered animation.

### 0.5 Frontend workstream items

Frontend **COMPLETE** means: real UI · real API integration · real workflow ·
correct permissions · route protection · loading + empty + error states ·
responsive · accessible · translated · RTL-safe · correct business context ·
validation. Nothing below is complete because a page renders.

**i18n is the one thing holding several items at IN PROGRESS**: the provider and
catalogue are in place but screen copy is still English literals, so no screen
can honestly be called COMPLETE on the i18n criterion. Items marked COMPLETE
below satisfy every other criterion and are listed with that exception stated.

| ID | Feature | Backend | Frontend | Route | Permissions | Notes |
|---|---|---|---|---|---|---|
| FE-01 | Design tokens + primitives | n/a | **COMPLETE** | — | — | Button, Field/Input/Select/Checkbox, Panel, PageHeader, Badge, Figure, DataTable, TableSkeleton, LoadMore, Empty/NoMatches/Error/AccessDenied/Skeleton |
| FE-02 | API client (errors, refresh, idempotency) | ✅ | **COMPLETE** | — | — | Single-flight refresh; verified live including the 403-without-CSRF path |
| FE-03 | Generated permission contract | ✅ | **COMPLETE** | — | — | 463 routes · 109 permissions · drift check |
| FE-04 | Sign-in (one login, no role picker) | ✅ | IN PROGRESS | `/login` | public | Verified live. Both challenges coded; neither reproducible on seeded data. i18n pending |
| FE-05 | Session + workspace resolution | ✅ | **COMPLETE** | — | — | Verified live under enforced RLS |
| FE-06 | Route + action guards, 403 | ✅ | IN PROGRESS | all | all | `RequireWorkspace`, `RequirePermission`, `Can`. Not yet exercised by a real employee account |
| FE-07 | Permission-aware navigation | ✅ | IN PROGRESS | — | all | 11 business sections, 3 platform; plan-gated items greyed. i18n pending |
| FE-08 | App shell (responsive) | n/a | IN PROGRESS | — | — | Sidebar ≥lg, drawer below, 44px touch targets. i18n pending |
| FE-09 | Company context + switcher | ✅ | **COMPLETE** | — | `identity.view` | Drives currency and market for every figure; verified live |
| FE-10 | i18n (en/ar/bn) + RTL | ✅ | **COMPLETE** | — | — | 480 `nx.*` keys in all three languages; every built screen through `t()`; navigation holds keys, not prose. Guarded by the shared coverage check and a physical-property check |
| FE-11 | Business dashboard | ✅ | IN PROGRESS | `/dashboard` | `sales.view` ∪ `accounting.view` ∪ `inventory.view` | Verified live. Attention links fixed (M5). i18n pending |
| FE-12 | Products list | ✅ | IN PROGRESS | `/products` | `catalog.view` | Verified live; now on the shared `ResourceList`. Create/edit not started |
| FE-13 | Web POS — counter session | ✅ | IN PROGRESS | `/pos` | `sales.create` | Verified live; `company_id` fixed (M1). i18n pending |
| FE-14 | Web POS — till | ✅ | IN PROGRESS | `/pos` | `sales.create` | **A real sale posted end to end.** Local scanning, shift gate, idempotent replay. i18n pending |
| FE-15 | Platform — service health | ✅ | IN PROGRESS | `/platform` | super-admin | Built; not yet exercised with a super-admin account. i18n pending |
| FE-16 | Product detail / variant matrix | ✅ | IN PROGRESS | `/products/{id}` | `catalog.view` | The matrix, with price, on-hand and reorder level per variant, and out/low marked. Verified live. Editing a variant not started |
| FE-17 | Sales — the trading day | ✅ | IN PROGRESS | `/sales` | `sales.view` | **A day, not an all-time list** — that is the capability the backend has (`GET /dashboard/sales?date=`) and the way a shop reconciles. Day totals, retail/wholesale split, ZATCA state per row, date stepper. Verified against two real invoices. Invoice DETAIL not started |
| FE-18 | Returns / exchanges | ✅ | ⬜ NOT STARTED | `/sales/returns` | `sales.refund` | |
| FE-19 | Shifts, cash drop, X/Z | ✅ | ⬜ NOT STARTED | `/shifts` | `sales.receive_payment` | Opening is done inside the till |
| FE-20 | Customers + ledger | ✅ | IN PROGRESS | `/customers`, `/customers/{id}` | `customers.view` | List and statement built and verified live. The khata carries a running balance and a double-ruled closing row; unpaid invoices flag overdue. Create/edit and the credit-limit control (its own permission, `customers.set_credit_limit`) not started |
| FE-21 | Stock on hand | ✅ | IN PROGRESS | `/stock` | `inventory.view` | Search, location filter, and the server's `low` filter. **Closes the dashboard's dead link.** Movements and counts not started |
| FE-22 | Stock transfers (approve/dispatch/receive) | ✅ | ⬜ NOT STARTED | `/stock/transfers` | `inventory.approve_transfer` | **No frontend caller** |
| FE-23 | Batch / expiry / recall | ✅ | ⬜ NOT STARTED | `/stock/batches` | `inventory.recall_batch` | **No frontend caller** |
| FE-24 | Production orders | ✅ | ⬜ NOT STARTED | `/stock/production` | `inventory.adjust_stock` | **No frontend caller** |
| FE-25 | Purchasing (31 routes) | ✅ | ⬜ NOT STARTED | `/buying/*` | `purchasing.*` | Largest single module |
| FE-26 | Expenses + departments + recurring | ✅ | ⬜ NOT STARTED | `/money/expenses` | `expense.view` | Departments and recurring have **no frontend caller** |
| FE-27 | Manual journals | ✅ | ⬜ NOT STARTED | `/money/journals` | `accounting.create` | **No frontend caller** |
| FE-28 | Treasury / reconciliation | ✅ | ⬜ NOT STARTED | `/money/accounts` | `accounting.reconcile` | |
| FE-29 | Payroll / employees / EOSB / WPS | ✅ | ⬜ NOT STARTED | `/people/*` | `payroll.*`, `hr.*` | Plan-gated `payroll` |
| FE-30 | Users, roles, permission builder | ✅ | ⬜ NOT STARTED | `/people/users` | `identity.manage_roles` | `GET /permissions` returns the catalogue with `holds` and `label_ar`/`label_bn` |
| FE-31 | Financial statements + VAT return | ✅ | ⬜ NOT STARTED | `/reports/*` | `accounting.view` | |
| FE-32 | Orders → invoice | ✅ | ⬜ NOT STARTED | `/orders` | `order.view` | `POST /orders/{id}/invoice` has **no frontend caller** |
| FE-33 | ZATCA / e-invoicing onboarding | ✅ | 🚧 BLOCKED | `/settings/einvoicing` | `einvoicing.onboard` | Direction says ZATCA is skipped and isolated |
| FE-34 | Platform: businesses, billing, dunning | ✅ | ⬜ NOT STARTED | `/platform/businesses` | super-admin | |
| FE-35 | Platform: rules, jurisdictions, tax rates | ✅ | ⬜ NOT STARTED | `/platform/rules` | super-admin | Imported → reviewed → activated → verified |
| FE-36 | Global search | ✅ | ⬜ NOT STARTED | (command palette) | authenticated | Payload validated live |
| FE-37 | Approvals | ✅ | ⬜ NOT STARTED | `/approvals` | `approval.view` | Plan-gated |
| FE-38 | Privacy (24 routes) | ✅ | ⬜ NOT STARTED | `/oversight/privacy` | `privacy.view` | |
| FE-39 | Business details / onboarding wizard | ✅ | ⬜ NOT STARTED | `/settings/business` | `identity.edit` | **Blocks Saudi selling** until the branch National Address is complete |
| FE-40 | Barcodes and label studio | ✅ | ⬜ NOT STARTED | `/products/labels` | `label.print` | Plan-gated |
| FE-41 | POS shift close, cash drop, X/Z | ✅ | ⬜ NOT STARTED | `/pos`, `/shifts` | `sales.receive_payment` | |
| FE-42 | Promotions | ✅ | ⬜ NOT STARTED | `/promotions` | `promotion.view` | Plan-gated |
| FE-43 | Deliveries | ✅ | ⬜ NOT STARTED | `/deliveries` | `delivery.view` | Plan-gated |
| FE-44 | Instalment plans | ✅ | ⬜ NOT STARTED | `/money/installments` | `installment.view` | Plan-gated |
| FE-45 | Service jobs / serials / warranty | ✅ | ⬜ NOT STARTED | `/aftersales/*` | `service.view` | Plan-gated |
| FE-46 | Loyalty, wallets, gift cards | ✅ | ⬜ NOT STARTED | `/customers/loyalty` | `loyalty.view` | Plan-gated |
| FE-47 | Investors | ✅ | ⬜ NOT STARTED | `/money/investors` | `investor.view` | Plan-gated |
| FE-48 | Fixed assets | ✅ | ⬜ NOT STARTED | `/money/assets` | `asset.view` | Plan-gated |
| FE-49 | Exchange rates / FX | ✅ | ⬜ NOT STARTED | `/money/accounts` | `accounting.view` | |
| FE-50 | Accounting periods / year-end | ✅ | ⬜ NOT STARTED | `/money/periods` | `accounting.close_period` | |
| FE-51 | Gateways + settlement | ✅ | ⬜ NOT STARTED | `/money/gateways` | `gateway.view` | |
| FE-52 | Analytics / forecast | ✅ | ⬜ NOT STARTED | `/reports/analytics` | `report.view` | Plan-gated |
| FE-53 | Notifications | ✅ | ⬜ NOT STARTED | (header) | authenticated | |
| FE-54 | Audit trail | ✅ | ⬜ NOT STARTED | `/oversight/audit` | `accounting.view` | |
| FE-55 | Documents | ✅ | ⬜ NOT STARTED | `/oversight/documents` | `document.view` | |
| FE-56 | Customer portal administration | ✅ | ⬜ NOT STARTED | `/customers/portal` | `portal.view` | |
| FE-57 | Compliance dashboard | ✅ | ⬜ NOT STARTED | `/oversight/compliance` | `compliance.view` | |
| FE-58 | Supplier portal administration | ✅ | ⬜ NOT STARTED | `/buying/suppliers` | `portal.manage` | 11 supplier-portal routes exist |
| FE-59 | Group companies / consolidation | ✅ | ⬜ NOT STARTED | `/oversight/groups` | `group.view` | Plan-gated |
| FE-60 | Tills and devices | ✅ | ⬜ NOT STARTED | `/settings/devices` | `devices.view` | Market-aware: SA needs an EGS unit |
| FE-61 | Backups | ✅ | ⬜ NOT STARTED | `/oversight/backups` | `backup.view` | |
| FE-62 | Plan and billing | ✅ | ⬜ NOT STARTED | `/settings/subscription` | `subscription.view` | |
| FE-63 | Integrations: API keys, webhooks | ✅ | ⬜ NOT STARTED | `/settings/integrations` | `integration.view` | |
| FE-64 | Import / export | ✅ | ⬜ NOT STARTED | `/settings/imports` | `data.import` | |
| FE-65 | Platform: failed jobs | ✅ | ⬜ NOT STARTED | `/platform/jobs` | super-admin | |
| FE-66 | Support tickets (both sides) | ✅ | ⬜ NOT STARTED | `/settings/support` | `support.raise` | |
| FE-67 | Receipt / invoice templates | ✅ | ⬜ NOT STARTED | `/settings/business` | `identity.edit` | |

### 0.6 GATE 3 — Blueprint traceability

Every top-level Blueprint section has a row. Nothing is collapsed: where one
screen serves several Blueprint IDs, each ID keeps its own row pointing at that
screen. Derived mechanically from the Blueprint's own headings, so no ID can be
lost by hand.

**88 sections · COMPLETE 9 · IN PROGRESS 14 · NOT STARTED 55 · BLOCKED 2 · N/A 8**

(The "77 named features" of the earlier backend sweep are a subset: this table
additionally carries the `J*` architecture sections and the `O*` restatement,
marked N/A or mapped, rather than dropping them.)

| Blueprint | Feature | Frontend item | Route(s) | Backend API | Permissions | Frontend status | Notes |
|---|---|---|---|---|---|---|---|
| A1 | Product Vision | — | — | — | — | N/A | Product vision. Not a screen. |
| A2 | Guiding Principles (non-negotiable, apply to every module below) | — | — | — | — | N/A | Guiding principles. Not a screen. |
| A3 | Multi-Tenant SaaS Architecture | FE-05, FE-09 | (all) | GET /auth/me, GET /companies | — | COMPLETE | Tenancy is resolved from the session; company scope is application state. |
| A4 | Super Admin | FE-15, FE-34, FE-35 | /platform/* | 27 SuperAdmin routes | super-admin | IN PROGRESS | Health done. Businesses, billing, regulatory not started. |
| A5 | Business Owner Account, Onboarding & Provisioning | FE-39 | /settings/business | GET/PUT /onboarding/* | identity.edit | NOT STARTED | The 7-step wizard. Live validation showed the branch National Address blocks Saudi sales until complete. |
| A6 | Role & Permission Management (RBAC) | FE-06, FE-07, FE-30 | /people/users | GET /permissions, /roles, /people | identity.manage_roles | IN PROGRESS | Guards and permission-aware nav COMPLETE. The role builder screen is not started. |
| A7 | Multi-Platform Client Access | FE-08 | (all) | — | — | COMPLETE | One responsive web app; POS is a module inside it. |
| A8 | Dashboard & KPI Center | FE-11 | /dashboard | GET /dashboard/overview | sales.view ∪ accounting.view ∪ inventory.view | COMPLETE | Attention list first, four drill-through figures. Verified live. |
| B1 | Product & Catalog Management | FE-12 | /products | GET/POST /catalog/products | catalog.view / catalog.create | IN PROGRESS | List verified live and on the shared list component. Create/edit not started. |
| B2 | Product Variant Matrix (critical for Fashion/RMG | FE-16 | /products/{id} | GET/POST /catalog/products/{id}/matrix | catalog.view | IN PROGRESS | The grid is built and verified live; generating a matrix is not |  |
| B3 | Intelligent Barcode Engine & Label Studio | FE-40 | /products/labels | 9 /labels/* routes | label.print / label.manage | NOT STARTED | Plan-gated: label_studio. |
| B4 | Inventory & Warehouse Management | FE-21, FE-22, FE-23 | /stock/* | 28 /stock/* routes | inventory.* | NOT STARTED | Live validation: adjustment kind ∈ {adjustment, wastage}; reason is an enum. Two backend defects fixed here. |
| B5 | Purchase & Procurement Management | FE-25 | /buying/* | 35 /purchasing/* routes | purchasing.* | COMPLETE | §0.88–0.95 and §0.102. Purchase RETURN was missing from the backend entirely — built here: migration 0127, the service, three routes and a new permission. |
| B5.1 | RFQ | FE-25 | /buying/quotes | /purchasing/rfqs/* | purchasing.manage_rfq, purchasing.award_rfq | COMPLETE | §0.95. The four-way split proved live. |
| B5.2 | Three-Way Matching | FE-25 | /buying/bills | /purchasing/bills/* | purchasing.approve_bill | COMPLETE | §0.93. All four dimensions shown, including the ones that passed. |
| B6 | Supplier Management | FE-25 | /buying/suppliers | /purchasing/suppliers | purchasing.manage_suppliers | COMPLETE | §0.88. Retiring one is refused while money is owed. |
| B7 | Point of Sale (POS) & Billing | FE-13, FE-14, FE-41 | /pos | 12 /pos/* routes, /shifts | sales.create | COMPLETE | A real sale posted end to end: counter token, shift, catalogue snapshot, tax split, idempotent replay. |
| B8 | Hardware Integration (Showroom Cash-Counter Reality) | — | /pos | — | — | BLOCKED | Cash drawer, pole display and scales need the desktop till. A browser cannot reach them; scanners work as keyboards and do. |
| B9 | Promotions, Discounts & Pricing Engine | FE-42 | /promotions | /promotions/*, /promotions/quote | promotion.view / promotion.manage | NOT STARTED | Cart carries promotion_id so redemption is recorded; the quote call is not wired yet. |
| B10 | Sales Returns, Exchange & Replacement | FE-18 | /pos/returns, /pos/exchanges | /pos/returns, /pos/exchanges, /pos/sales/{id}/returnable | sales.refund, sales.exchange | COMPLETE | §0.101. Returns were built in §0.91; exchanges are new. Three defects found live: the till never named a stock location, an idempotent replay came back hollow, and the sidebar offered exchanges to a screen that refused them. |
| B11 | Sales Quotation, Sales Order & Delivery Documentation | FE-32 | /orders | 9 /orders/* routes | order.view / order.manage | NOT STARTED | POST /orders/{id}/invoice still has no frontend caller. |
| B12 | Wholesale / B2B Module | FE-20 | /customers | /customers, /dashboard/sales | customers.view | IN PROGRESS | Wholesale pricing flows at the till, the customer list marks wholesale accounts, and the sales day splits retail from wholesale so bulk orders do not distort retail figures. |
| B13 | Online Order & Delivery Management | FE-32, FE-43 | /orders, /deliveries | /orders/*, /deliveries/* | order.view, delivery.view | NOT STARTED | Plan-gated: online_orders. |
| B14 | Installment / EMI (কিস্তি) System | FE-44 | /money/installments | 7 /installments/* routes | installment.view / installment.manage | NOT STARTED | Plan-gated: installments. |
| B15 | Warranty, Serial/IMEI Tracking & Service/Repair | FE-45 | /aftersales/service, /stock/serials | /service-jobs/*, /serials/* | service.view, serial.view | NOT STARTED | Plan-gated: warranty. |
| B16 | Customer Relationship Management (CRM) & Loyalty | FE-20, FE-46 | /customers, /customers/loyalty | 12 /customers/*, 6 /loyalty/* | customers.view, loyalty.view | IN PROGRESS | List and statement built. Loyalty not started |  |
| C1 | Core Accounting (Chart of Accounts, Journal, Ledger) | FE-27 | /money/journals | /accounting/journals/* | accounting.view / accounting.create | NOT STARTED | Manual journals still have no frontend caller. |
| C2 | Cash & Bank Management | FE-28 | /money/accounts, /money/transfers | 10 /treasury/* routes | accounting.view / manage_accounts | COMPLETE | §0.98. Accounts with the five kinds, transfers, and the unmatched count that leads here. |
| C3 | Expense & Investment Management | FE-26 | /money/expenses, /money/expenses/setup | 16 /expenses/* routes | expense.view / expense.record / expense.manage_heads | IN PROGRESS | Expenses and the configuration behind them are done (§0.98, §0.99): period, voucher, recording, categories, departments, standing costs. Investors (C3.2) not started. |
| C4 | Accounts Receivable & Payable (AR/AP) | FE-20, FE-25 | /customers/{id}, /customers/ageing | /customers/{id}/ledger, /open-invoices | customers.view | IN PROGRESS | The customer statement and unpaid-invoice list are built; the ageing reports are not |  |
| C5 | Employee / HR Management | FE-29 | /people/employees | /employees/*, /attendance, /leave | hr.view / hr.manage | NOT STARTED | Plan-gated: payroll. |
| C6 | Payroll, Commission & Saudi WPS Compliance | FE-29 | /people/payroll | /payroll/*, /commission-rules, /eosb | payroll.view / run / approve | NOT STARTED | Includes the Saudi WPS wage file. |
| C7 | Fixed Asset Management | FE-48 | /money/assets | /assets/* | asset.view / asset.manage | NOT STARTED |  |
| C8 | Shift Management & Cash Drawer Reconciliation (X/Z Report) | FE-19, FE-41 | /shifts, /pos | 6 /shifts/* routes | sales.receive_payment, report.view | IN PROGRESS | Opening a session is COMPLETE in the till (validated live). Cash drop, close and X/Z are not. |
| C9 | Double-Entry Accounting Engine | FE-27 | /money/journals | /accounting/journals | accounting.view | NOT STARTED |  |
| C10 | Fiscal Period & Year-End Closing | FE-50 | /money/periods | /accounting/periods/*, /accounting/year-end | accounting.close_period / reopen_period | NOT STARTED |  |
| C11 | Bank Reconciliation | FE-28 | /money/reconcile, /money/reconcile/{id} | /treasury/statements/*, /treasury/lines/{id}/match | accounting.reconcile | COMPLETE | §0.100. Import, auto-match, match by hand, undo, sign-off refused while anything is unexplained. One backend defect fixed: the frozen-statement refusal arrived as a 500. |
| C12 | Payment Settlement & Gateway Reconciliation | FE-51 | /money/gateways | /settlement/*, /payment-gateways/* | accounting.view, gateway.view | NOT STARTED |  |
| C13 | Inventory Costing & COGS Engine | FE-21 | /stock | GET /stock/on-hand | inventory.view | NOT STARTED | Costing is a backend concern; the frontend shows value at cost on the dashboard already. |
| C14 | Accounting-Aware Returns, Exchanges & Credit Notes | FE-18 | /sales/returns | /pos/returns | sales.refund | NOT STARTED |  |
| D1 | Reporting Suite | FE-31 | /reports/* | 10 /reports/* routes | report.view / report.export | NOT STARTED |  |
| D2 | Business Analytics & Forecasting | FE-52 | /reports/analytics | /analytics/kpis, /movers, /forecast, /profitability | report.view | NOT STARTED | Plan-gated: analytics. |
| D3 | Notification Center | FE-53 | (header) | /notifications/* | authenticated | NOT STARTED |  |
| D4 | Audit Trail & Activity Log | FE-54 | /oversight/audit | GET /audit | accounting.view | NOT STARTED |  |
| D5 | Approval Center | FE-37 | /approvals | /approvals/*, /approval-rules, /approval-delegations | approval.view / approval.decide | NOT STARTED | Plan-gated: approvals. |
| D6 | Document Management | FE-55 | /oversight/documents | /documents/* | document.view / document.manage | NOT STARTED |  |
| D7 | Global Search & Command Center | FE-36 | (command palette) | GET /search | authenticated | NOT STARTED | Validated live: requires company_id; returns {kind,id,label,detail,amount,currency}. |
| E1 | ZATCA Phase 2 E-Invoicing Engine ("Fatoora") | FE-33 | /settings/einvoicing | 8 /einvoicing/* routes | einvoicing.view / einvoicing.onboard | BLOCKED | Direction says ZATCA is skipped and isolated. The one genuinely external dependency is the Fatoora OTP, which must never be fabricated. |
| E2 | Saudi Tax Engine | FE-31 | /reports/tax | GET /reports/vat-return | accounting.view | NOT STARTED |  |
| E3 | Saudi Payment Methods & Payment Compliance (FULL COVERAGE) | FE-14, FE-51 | /pos, /money/gateways | /payment-gateways/*, /payment-attempts | gateway.view | IN PROGRESS | Four tenders live at the till; gateway administration is not built. |
| E4 | PDPL | FE-38 | /oversight/privacy | 24 /privacy/* routes | privacy.view / privacy.manage | NOT STARTED |  |
| E5 | Saudi E-Commerce Law & Online Store Compliance | FE-56 | /customers/portal | /portal/* | portal.view | NOT STARTED |  |
| E6 | Saudi Labour & Payroll Compliance (expanded) | FE-29 | /people/payroll | /payroll/{id}/wage-file, /eosb | payroll.approve | NOT STARTED |  |
| E7 | Compliance Monitoring Dashboard | FE-57 | /oversight/compliance | GET /compliance | compliance.view | NOT STARTED |  |
| E8 | Regulatory Rule Registry | FE-35 | /platform/rules | /platform/rules | super-admin | NOT STARTED |  |
| F1 | Business Workflow / Approval Engine | FE-37 | /approvals | /approvals/* | approval.view / decide | NOT STARTED |  |
| F2 | Customer Self-Service Portal | FE-56 | /customers/portal | /portal/contacts, /portal/return-requests | portal.view / portal.manage | NOT STARTED |  |
| F3 | Supplier Portal | FE-58 | /buying/suppliers | /portal/supplier/* | portal.manage | NOT STARTED |  |
| F4 | Multi-Company / Group Consolidation | FE-59 | /oversight/groups | 10 /groups/* routes | group.view / group.manage | NOT STARTED | Plan-gated: consolidation. |
| G1 | Country Configuration Engine | FE-09 | (all) | GET /companies | — | COMPLETE | country + base_currency drive market, grouping and precision. Validated live: country arrives lowercase. |
| G2 | Multi-Currency | FE-09, FE-49 | (all) | /exchange-rates | accounting.view | IN PROGRESS | Per-company currency COMPLETE. FX rate management not started. |
| G3 | Multi-Language & RTL/LTR | FE-10 | (all) | — | — | **COMPLETE** | 480 keys in en/ar/bn, every built screen translated, navigation holds keys. Two RTL defects fixed. Two tests keep it true. |
| G4 | Tax Templates Library | FE-35 | /platform/rates | /platform/jurisdictions/* | super-admin | NOT STARTED |  |
| H1 | Security & Authentication | FE-02, FE-04, FE-05 | /login | /auth/* | public | COMPLETE | Refresh rotation, CSRF double-submit and both login challenges verified live. |
| H2 | Offline-First Architecture & Sync Engine | FE-13 | /pos | /catalog/snapshot, /sync/push | sales.create | IN PROGRESS | The till holds the catalogue in memory. Queued offline sales are a desktop-till concern. |
| H3 | Device Management | FE-60 | /settings/devices | 12 /devices/* routes | devices.view / devices.manage | NOT STARTED | Live: a Saudi terminal needs an EGS unit; session counters register active, paired ones pending. |
| H4 | Backup & Disaster Recovery | FE-61 | /oversight/backups | /backups/* | backup.view / backup.run | NOT STARTED |  |
| H5 | SaaS Subscription, Billing & Feature Flags | FE-07, FE-62 | /settings/subscription | /subscription/*, /plans | subscription.view | IN PROGRESS | Entitlements drive navigation already; the billing screen is not built. |
| H6 | API & Integration Platform | FE-63 | /settings/integrations | /api-keys/*, /webhooks/* | integration.view / manage | NOT STARTED |  |
| H7 | Import / Export & Data Migration | FE-64 | /settings/imports | 7 /imports/* routes, /exports/{kind} | data.import / data.export | NOT STARTED |  |
| H8 | System Health Monitoring (Super Admin view) | FE-15 | /platform | GET /platform/health | super-admin | COMPLETE |  |
| H9 | Job / Queue System (Background Processing) | FE-65 | /platform/jobs | /platform/jobs/failed, /{id}/retry | super-admin | NOT STARTED |  |
| H10 | Customer Support / Ticketing (Super Admin ↔ Tenant) | FE-66 | /settings/support, /platform/support | /support/*, /platform/support | support.raise / super-admin | NOT STARTED |  |
| I1 | System / Owner Settings | FE-39 | /settings/business | /companies/{id}/* | identity.edit | NOT STARTED |  |
| I2 | Receipt & Invoice Template Customization | FE-67 | /settings/business | /companies/{id}/templates/{docType} | identity.edit | NOT STARTED |  |
| I3 | Numbering Engine | — | — | — | — | N/A | Numbering is a backend engine. |
| I4 | User Preferences | FE-10 | (header) | — | — | IN PROGRESS | Language preference persists per device. |
| I5 | Point / Station Settings | FE-60 | /settings/devices | /devices/{id}/settings | devices.manage | NOT STARTED |  |
| J1 | Confirmed Technology Stack | — | — | — | — | N/A | Technology stack. Next.js 16 + TS + Tailwind chosen accordingly. |
| J2 | High-Level Architecture | — | — | — | — | N/A | Architecture. |
| J3 | Data Flow | — | — | — | — | N/A | Data flow. |
| J4 | Performance Targets | — | — | — | — | N/A | Performance targets. |
| J5 | Testing Strategy | — | — | — | — | N/A | Testing strategy. |
| O1 | Access Hierarchy & Account Control | FE-05, FE-06 | (all) | /auth/me | — | COMPLETE | One login, no role picker; workspace resolved from the session. |
| O2 | Employees, Roles & Permissions | FE-06, FE-07, FE-30 | /people/users | /people/*, /roles/* | identity.* | IN PROGRESS |  |
| O3 | Platform | FE-08 | (all) | — | — | IN PROGRESS | One responsive website. No native surface. |
| O4 | Expense & Money Tracking | FE-26 | /money/expenses | /expenses/* | expense.view | NOT STARTED |  |
| O5 | Market Requirements | FE-09 | (all) | GET /companies | — | COMPLETE | BD / SA / US / International, with per-market grouping and precision. |

### 0.7 Gate status

| Gate | Status |
|---|---|
| 1 — Live backend validation | ✅ **DONE**. 7 frontend mismatches fixed, 2 backend defects fixed, environment corrected for RLS |
| 2 — i18n application | ✅ **DONE** — 480 keys in en/ar/bn, every screen through `t()`, two real RTL defects fixed, guarded by two tests |
| 3 — Blueprint traceability | ✅ **DONE** — §0.6, all 88 IDs mapped |
| 4 — RBAC integrity | ✅ **DONE** — contract widened to 109, `ROUTE_PERMISSIONS` separated, two tests pin it |
| 5 — Business context | ✅ **DONE** — `useCompanyScope()`; no request fires before it resolves |
| 6 — Money as decimal strings | ✅ **DONE** — 17 formatter tests + 18 cart tests; verified against a real invoice |
| 7 — Design quality | ✅ own identity, not shadcn default |
| 8 — Real UX | ✅ for built screens |
| 9 — Responsive | ✅ for built screens; not yet exercised on a real device |
| — | **`npm run verify:api`** now checks every endpoint the screens call, and the FIELDS each reads, against a running server |
| 10 — Customers → Sales | ✅ **DONE** — both built on real APIs and verified live |
| 16 — Platform Admin | 🟡 three screens live-verified as an operator (§0.86); onboarding, billing and the regulatory group remain |

### 0.86 Platform Admin, validated as an operator

Three screens, built and then checked against a running server as a user with
no tenant. Every design decision below was a live finding, not a reading of the
Go source -- the source said most of it, and the source was checked, but four
things were only visible with the thing running.

**`/platform/businesses`** -- `GET /platform/tenants`. The route takes no
search parameter and no cursor: it answers with every tenant, up to 500,
ordered newest first. So the search box filters in the browser, through a new
`filterRow` prop on `ResourceList`. A box that typed and did nothing would be
worse than no box, and since every row is already there, filtering locally is
not a shortcut -- it is where the data is. `last_activity` and
`backup_verified_at` are `omitempty` and genuinely absent on a fresh seed,
which is why those columns say "Never" in words rather than showing an empty
cell somebody has to interpret.

**`/platform/jobs`** -- `GET /platform/jobs/failed`, `POST .../retry`.
Verified both halves against seeded rows: retrying a `failed` job answers 204
and the row leaves the list; retrying a `dead` one answers **409** with "a dead
job exhausted its attempts on something retrying will not fix". The screen
therefore has no retry button on a dead row rather than one that gets refused.
Three corrections came out of running it:

- The kind is shown **verbatim**. Real kinds are `zatca.submit`,
  `stock.low_sweep`, `accounting.tie_out`. Opening the underscores rendered
  `stock.low sweep`, a string that appears in no log and cannot be searched
  for. An identifier is more useful accurate than pretty.
- `tenant_id` is null for the platform's own sweeps, and the row said "--".
  It now says so, because whose job it is decides who fixes it.
- A retry had no error path. The dead rows are excluded, but a job the worker
  has just picked up, or one another operator already queued, still fails --
  and the button looked pressed and did nothing. It now reports.

**`/platform/support`** -- `GET /platform/support`, `POST .../reply`. Four
findings, all of which changed the screen:

- The queue carries **no messages**. `Queue` selects the ticket columns only;
  `readMessages` runs in `PlatformTicket`, and the platform side has no route
  for one ticket. The screen had a message count that could never render. The
  reply response *is* the updated ticket with its thread, so the thread appears
  after replying, and the screen says why it was not there before.
- Replying is a **state change**. With no status in the body the ticket moves to
  `waiting_on_customer`; with `"status":"resolved"` it closes and stamps
  `resolved_at`. Confirmed both. That is not a side effect to discover
  afterwards, so there are two buttons and a line saying what each does.
- `include_closed=true` exists and was not offered. Open queue 3, with the flag
  4. It is a checkbox now.
- Priorities are `low | normal | high | urgent` -- there is no `critical`, which
  the screen had a branch for, and `normal` was falling through to the default.
  All four now map explicitly, as do the five statuses and five kinds, from the
  table's own CHECK constraints.

**A platform operator holds zero permissions.** `/auth/me` answers
`{"is_super_admin":true,"permissions":[]}`. This is the whole reason
`PLATFORM_NAV` names no permission strings and `RequireWorkspace` gates on the
claim: a sidebar that asked for a permission here would render empty for the
person who runs the service. `verify:api` now asserts it.

**`cmd/devseed` gained `-platform-email`.** A platform operator is a user with
`tenant_id IS NULL` -- that is the entire model, per `identity.Login`. No
screen can create one, because every route that could is itself behind the
guard it would be needed to pass. Without this flag the platform workspace was
unreachable in development and the screens above could not have been checked at
all. Idempotent by email, reuses the owner's password when one was given.

**`verify:api` now signs in twice** -- as the owner for the business contracts,
then as the operator for the platform ones -- and asserts the fields each
platform screen reads. A database with no operator is reported loudly rather
than skipped, with the command to fix it.

Two things it learned to survive on the way:

- A **login challenge is not a crash.** `owner@example.test` in more than one
  business answers 200 with no token and `tenant_choice_required`; the script
  died on `undefined`. It now prints each business and the
  `RAWSYST_DEV_TENANT=` line that picks it.
- Which surfaced a **real defect in the sign-in screen**: the picker rendered
  `name` alone, and three businesses called "Demo Retail" were three identical
  buttons. The choice payload carries only an id and a name, so the id is the
  only thing that separates them -- eight characters of it now show under the
  rows that collide, and only those, with the reference in the accessible name
  too. Found by running the product, not by reading it.

### 0.87 The drawer, verified at a counter

FE-19 was built and had never been checked against a running server, because it
cannot be: every shift route refuses a token that does not name a terminal --
"Only a registered till can open a session. Sign in on the terminal itself
rather than in a browser." A browser sign-in reaches none of it. `verify:api`
now binds a counter through `POST /pos/counter-sessions` first, exactly as the
till does, and drives the whole path with the token that comes back.

What it proves, on a session it opens itself:

| | |
|---|---|
| `GET /shifts/current` | 404 between shifts, and a session when there is one -- a till restarted mid-shift finds its own session here rather than from the open response, which is the only other copy of the id |
| `POST /shifts` | opens blind, with a declared float |
| `GET /shifts/{id}` | **withholds `expected_cash`, `cash_takings`, `non_cash_takings` and `cash_movements`** from the cashier |
| `GET /shifts/{id}/x-report` | the same session, as the owner, carries all four |
| `POST .../cash-drop` | 204 |
| `POST .../close` | 200 float less a 50 drop, counted 150: **expected 150, variance 0** |

The withholding rule is the one worth having a test for. Hiding the expected
figure alone was not enough -- `gross_sales` less `refund_total` less
`non_cash_takings` is the cash takings exactly for a shop that sells for cash
and card, which is most shops -- so the server withholds all four, and the run
now fails if any of them appears in the cashier's view of a blind session. A
cashier who can see what the drawer should hold can make it agree, and then the
variance reads zero on every shift and the whole reconciliation is theatre.

`ShiftReport` in `lib/pos/shift.ts` already types all four as optional, which
is now confirmed rather than assumed.

Two smaller things the run does deliberately:

- It sends an `Idempotency-Key` on every POST, because the client does, and a
  run that omitted it would not be exercising the request the product makes.
- It closes **only** the session it opened. A shift that was already running
  belongs to somebody counting a real drawer, and the run says so and leaves it
  alone -- along with a line saying the withholding rule was not exercised,
  because a run that quietly skipped it reads exactly like one that checked it.

### 0.88 Purchasing, first pass — and two defects the source did not show

FE-25 is the largest module in the Blueprint: 31 routes. Three screens are
built and verified live — suppliers, the order list, and one order.

**`/buying/suppliers`.** There is no `GET /purchasing/suppliers/{id}`. The
list carries every field the form needs, so a detail route would fetch the
whole list to render one row of it; the form opens beside the list instead and
which supplier is open lives in the URL, so a link still opens the right one
and the back button closes it. That needed one addition to `ResourceList`, an
`onRows` callback, which is the honest way for a screen to use rows the list
already has rather than asking again. The code field is disabled when editing,
because `PUT` says plainly that it is on orders already issued — disabled
rather than accepted and ignored, which looks like a save that did not stick.
Deactivating is refused while money is owed, so it is a button with an error
path rather than a silent toggle, and the amount owed is shown beside it.

**`/buying/orders`** filters on status through the route's own parameter and
searches in the browser, because `ListOrders` takes a status and a limit and no
search term. **`/buying/orders/{id}`** shows four quantities per line —
ordered, arrived, still due, invoiced — because they answer different
questions: a buyer chasing a delivery reads the third, somebody checking an
invoice reads the fourth, and "12 of 24" answers neither.

Issuing is the one irreversible act in the module, and the only place in the
product with a confirmation step. It names the amount and the supplier rather
than asking "are you sure", because the number is the thing to be sure about.
After it, `PUT` answers 400 and a second issue answers 409 — both confirmed —
so the screen offers no edit rather than one that fails.

#### Two backend defects, found by running it

**A purchase line could not say how it was taxed.** `po_line` has stored
`tax_treatment` and `tax_rate` since 0031 and `CreateOrder` writes both, but
`po_outstanding` returned neither and it is the only read behind
`GET /purchasing/orders/{id}`. So `OrderLineView.TaxTreatment` was a field in
the contract that came back empty on every line of every order.

The display was the smaller half. `PUT` rewrites a draft's lines wholesale, so
an editor has to send back what it read — and reading an empty treatment and no
rate, it would send an empty treatment and no rate, and `CreateOrder`'s own
default would turn every line into a standard one at zero per cent. **Changing
a delivery date would have changed the tax.** Migration 0125 returns both.

**A tax rate of 15 was accepted as 1500%.** `sales_line_rate_sane` has
constrained a sales line to `[0, 1)` since 0018 and every purchasing test sends
`"0.15"`, but the purchasing tables carry no such constraint. Typing `15` for
fifteen per cent produced this, live:

| | sent 15 | sent 0.15 |
|---|---|---|
| net | 948.0000 | 444.0000 |
| tax | **14,220.0000** | 66.6000 |
| total | **15,168.0000** | 510.6000 |

Nothing refused it and nothing said anything, and the buyer's next sight of that
number is on an order the supplier can hold them to. Refused at the API
boundary rather than by a CHECK constraint: a constraint would have to be
validated against rows that already exist, and this is about what the API takes
from here on. Both pinned by tests that fail without the fixes.

#### Still to build in FE-25

Receiving (`POST /purchasing/receipts`, the only route that increases stock
through a purchase), bills and the three-way match, supplier payments and
reversals, the ageing screen, and the sourcing half — requisitions, RFQs, the
comparison and the award. Raising an order from the screen rather than from
curl is the next one, and it needs a line editor with product search.

### 0.89 Receiving — the only screen that puts stock on a shelf

`/buying/receipts`. B5 forbids a purchase order increasing inventory, and
`POST /purchasing/receipts` is the one route in the product that does it, so
the counting happens here and nowhere else.

**The delivery id is minted in the browser.** The route takes a client-assigned
`uuid` and answers with the ORIGINAL receipt and `already_received: true` if it
has seen it before. Confirmed live: the same uuid twice returned
`GRN-2026-000001` both times, and the second call created nothing. So the screen
mints one id when the order is chosen and discards it only when the form is
cleared — a clerk on a bad connection who presses the button twice books one
delivery, and is told that is what happened rather than left to wonder.

**Accepted and sent back are two columns.** `qty_received` and `qty_rejected`
go separately on the wire. A case that arrived broken did arrive: the supplier
delivered it and will invoice it, and netting them off in the browser would
lose the argument about who pays for the breakage. A rejected quantity with no
reason is refused in the form, because the supplier has to be told something.

**`qty_received` is net of rejections, and the column now says so.** Twenty
delivered with two rejected reads eighteen — `po_outstanding` sums
`qty_received - qty_rejected` — and the two rejected stay outstanding, because
the shop is still owed them. The column was labelled "Arrived", which is the
wrong word for that number; it says **Accepted**. (Migration 0067's own comment
calls it "the TOTAL that ever arrived", which the SQL beneath it has never
been. The code is right and the comment is loose.)

**Duty and import VAT are two fields.** Freight, duty and handling are spread
across the lines and into the cost layers. Import VAT is reclaimed and must
never touch the cost of the stock (E2.5). One field would invite adding them
together, which is the mistake the split exists to prevent. The spread defaults
to value rather than quantity, for the reason the Go comment gives: quantity is
wrong the moment a carton of scarves and a carton of gold share a container.

**A cost correction is shown when there is one.** The response carries
`cost_correction` and `units_recosted` — what this delivery put right on sales
that went out before it arrived, priced on an estimate (C13). Nobody asks for
it, and somebody reading last week's margin needs to know it moved, so the
screen says so when it is not zero and stays quiet when it is.

#### The third defect: a clerk could not be told which lines need a lot

`variant.tracks_batches` has existed since 0107 and is read by exactly one
query, in `internal/inventory/batch.go`. **No API payload carried it.**
`inventory.Receive` requires a batch number for a tracked variant and refuses
one for anything else — which is right — but the only way a screen could
discover which is which was to submit the delivery and read the error. That
means typing a whole pallet, pressing save, and being told.

Migration 0126 returns `tracks_batches` on the purchase order line, because
the receiving screen iterates those lines: it is already the one place asking
"what is still due on this order", and the answer is more useful when it also
says what has to be recorded about it. The lot, made-on and use-by fields now
appear on exactly the lines that need them, and nowhere else — where the route
would refuse them, a field would be a trap.

#### A dead column, removed

The receipts table on one order had a **Note**. A receipt carries no note: the
fields are `id, grn_number, po_id, po_number, received_on, lines,
already_received, order_status, cost_correction, units_recosted`. It would
have been an em dash on every row for ever, and is now the number of lines on
the delivery.

`verify:api` drives the whole thing — books a delivery, replays the uuid, and
fails if the replay creates a second receipt.

### 0.90 Raising an order, and a gap that is not mine to close quietly

`/buying/orders/new`. The picker opens on `/stock/on-hand?low=true` rather
than on an empty search box, because the reason anybody raises an order is that
something is running out — and the dashboard has been pointing at low stock
since it was built. This is where that pointing finally leads.

`lib/purchasing/draft-order.ts` computes what the order comes to before the
server sees it, in `decimal.js`, to four places, summing the ROUNDED lines
rather than rounding the sum — which is the order `CreateOrder` does it in.
Ten tests. The first pins the arithmetic against a real answer: 24 at 18.50 at
fifteen per cent came back from the server as net 444.0000, tax 66.6000, total
510.6000, and the screen has to agree or a buyer approves one number and commits
to another. Confirmed again on a two-line order: 544.0000 / 66.6000 / 610.6000,
line for line.

#### ~~🟠 OPEN FINDING — purchasing asks the client for a tax rate~~ ✅ RESOLVED, §0.92

Not fixed, because fixing it properly changes tax arithmetic, and that is the
most correctness-sensitive code in the product. Recorded with the evidence so it
can be decided rather than discovered.

`applyTaxProfile` in `internal/sales/terminal.go` is explicit — it "fills in
the values the till is not allowed to choose". A sale's rate is resolved from
the regulatory register at the invoice's issue date, and `pricing.go` **refuses
the sale** if no rate is on file:

> No tax rate is on file for %q in this market, so this sale cannot be priced.

`CreateOrder` does the opposite. It takes `tax_rate` from the request body and
defaults it to **zero**. And no business-facing route exposes what the rate
should be: the register is readable only through
`GET /platform/jurisdictions/rates`, which is `AccessSuperAdmin`.

So a purchasing screen has three options, and all three are bad:

| | |
|---|---|
| Send nothing | Every order is raised at 0% tax, silently |
| Hardcode 0.15 | A Saudi assumption in a product that sells into SA, BD and US |
| Ask the buyer | What this screen does |

It asks, converts per cent to the fraction the API wants, carries the previous
line's rate down the order so it is typed once, and says in a panel why it is
asking at all. That is honest, and it is still worse than the product already
knows how to be.

**The fix, when somebody wants it:** resolve the rate in `CreateOrder` and
`UpdateOrder` from the line's treatment and the company's country at
`ordered_on`, exactly as `applyTaxProfile` does at `issued_at`, and ignore a
client-supplied rate. `catalog.TaxRulesFor` already does the lookup and is
already called from three packages. The existing purchasing tests send
`"0.15"` and would pass unchanged in a Saudi fixture, so the change is testable
without rewriting them. It is left alone here because it changes what a
documented endpoint does with a field callers may be sending, and that is a
decision rather than a defect fix.

### 0.91 R6 CLOSED — a real cashier, against the real backend

The largest untested claim in the product was that its permission model works.
It has now been driven with an account created the way the product creates one.

```
Owner → POST /people (role: Cashier / POS Operator)
      → one-time password issued
      → sign in → GET /auth/me → 19 permissions, is_super_admin false
```

Every boundary, measured rather than reasoned about:

| Request | Answer |
|---|---|
| `GET /catalog/products` | 200 |
| `GET /customers` | 200 |
| `GET /catalog/snapshot` | 200 |
| `GET /pos/counters` | 200 |
| `GET /purchasing/suppliers` | **403** |
| `GET /purchasing/orders` | **403** |
| `GET /purchasing/ageing` | **403** |
| `GET /people` | **403** |
| `GET /employees` | **403** |
| `GET /permissions` | **403** |
| `GET /dashboard/overview` | **403** |
| `GET /reports/vat-return` | **403** |
| `GET /platform/health` | **404**, by design — confirming a platform route exists is itself a leak |

The backend is the boundary and it holds. Thirteen refusals, no leaks, and the
platform routes answer 404 rather than 403 to the same account.

#### Four frontend defects, all found by doing it rather than reading it

**1. A cashier landed on a screen the API refuses them.** Every signed-in
person was sent to `/dashboard`, and that screen reads
`GET /dashboard/overview` — which is `accounting.view`. A Cashier holds
`sales.view` and `inventory.view` and not that one. So did Branch / Store
Manager and Inventory / Warehouse Keeper: **three of thirteen seeded roles
signed in and hit a 403 on the first screen they saw.**

Fixed twice over. The dashboard nav item now names `accounting.view` alone,
and `landingFor()` sends somebody to the first item they can actually open. A
cashier now lands on **`/pos`**, which is where a cashier should start anyway.

**2. Five nav links led to a 403.** Nav permissions are ANY-of, so every
permission listed has to be sufficient on its own. The dashboard listed three
where one was required; `/buying/receipts`, `/buying/payments`,
`/buying/requisitions` and `/buying/quotes` each listed an action permission
beside the list permission their screen actually reads. Two were reachable by
seeded roles today (dashboard, requisitions); three needed a custom role.

Now pinned: `navigation.test.ts` maps each built screen to the route it reads
first and fails if any listed permission is not the one that route requires.
Reverting the dashboard fix fails two tests.

**3. A one-time password was permanent.** `POST /people` issues one and
sign-in answers `must_change_password: true`. The client parsed that flag —
`mustChangePassword` in `client.ts` — and **no screen acted on it.** An
employee signed in with the password their manager had just read off a screen,
went straight to work, and it stayed valid. The person who issued it could sign
in as them indefinitely, and every sale that account rang up carried their name.

`/change-password` now stands between them and the product. Verified live: the
change succeeds, the old password answers 401, the new sign-in reports
`must_change_password: false`.

**4. Fifty-six links to screens that do not exist.** The architecture describes
73 items and 15 are built. A cashier's sidebar offered 23 links and 17 of them
went to a not-found page, which reads as a broken product rather than an
unfinished one. `visibleNavigation()` offers what exists;
`navigation.built.test.ts` reads the app directory and fails if a `built` flag
is wrong **in either direction** — a flag on a missing page is a dead link, and
a page missing its flag is work nobody can reach, which is worse for being
silent.

Kept separate from `resolveNavigation` on purpose: a permission mistake shows
somebody a screen they are refused, an unbuilt link shows them a not-found
page, and folding the two together made six unrelated tests fail for a reason
that had nothing to do with what they check.

#### What each person now gets

| | lands on | sidebar |
|---|---|---|
| Owner | `/dashboard` | 11 screens |
| Cashier | `/pos` | 6 screens |

Every link leads somewhere that exists, and to something that account can open.

#### One characteristic, recorded rather than changed

An access token issued before a password change **keeps working until it
expires**. `ChangePassword` revokes every `user_session` row, and the handler
says "Please sign in again on all your devices" — but access tokens are
stateless JWTs and the middleware does not read the session row per request.
Measured: `/auth/me` on the old token answered 200 after the change.

That is a deliberate trade-off, not a defect: checking revocation per request
is a database read per request, which is the cost stateless tokens exist to
avoid, and `RAWSYST_ACCESS_TOKEN_TTL` bounds the window at 15 minutes. The
frontend closes its own half — `/change-password` calls `signOut()` before
redirecting, so the browser holds nothing. The residual is a token captured
elsewhere, for at most fifteen minutes. Recorded so nobody reads the handler's
message as a stronger promise than the system makes.

### 0.92 The purchasing tax rate — resolved, with the evidence

§0.90 left this open rather than guessing. The investigation the brief asked
for found an answer already written down in this repository, on the same kind
of document, by the same author.

#### The rule the product already states

`internal/expenses/record.go`, pricing a supplier's expense:

> The RATE comes from the registry at the expense date, never from the caller:
> a client that could state its own VAT rate could state what the return
> claims. The TREATMENT comes from the caller, because only they know whether
> the supplier charged VAT — but it is checked against the treatments the
> country allows on that date.

`internal/sales/terminal.go`, on the sale path:

> applyTaxProfile fills in the values the till is not allowed to choose.

An expense is a purchase. A bill is a purchase. Purchasing was the only path in
the product taking a rate from whoever was calling — and defaulting it to
**zero** when it was absent, so an order raised without one carried no input
VAT at all. That is the number the shop reclaims.

So this was not a case where purchasing legitimately needs an explicit rate. It
was an inconsistency, and the correct behaviour was already implemented twice.

#### The change, and why it is the smallest one

`registry.Service.TaxRate(ctx, tx, country, treatment, asOf, tenantID)` already
existed — it is what the sale path asks, it is per-treatment rather than
per-country, and it **refuses rather than defaults**. Purchasing now calls it.

- The **treatment** stays the caller's, checked against `catalog.TaxRulesFor`
  for the market and the date. A treatment the country does not use is refused,
  naming the country.
- The **rate** is the register's, at the order date.
- `zero_rated` and `exempt` resolve to zero without a lookup, because there is
  no rate to record and no source to cite for "this is not taxed".
- The **United States** is refused by `registry.TaxRate` in its own words: tax
  there is set by state, county and city, so it cannot be resolved from a
  country and a date. Nothing is defaulted and nothing is invented — which is
  what the brief asked for, and it is the product's existing position rather
  than a new one.

#### `tax_rate` in the body: an assertion, not an input

Neither removing the field nor ignoring it would do.

Removing it breaks every caller at once, and **fifty-four assertions in this
repository's own tests** send exactly the rate the register holds. Ignoring it
silently is how a caller keeps sending last year's rate for a year and never
learns: their totals would quietly become right while their own screen kept
showing the old figure.

So it is now checked. Send it and it must agree; omit it and the register
answers. Disagreement is refused, naming both numbers:

> Line 1 states a tax rate of 0.05 and the rate on file is 0.15. The rate comes
> from the regulatory register, so either leave it out or send the one in force.

Zero counts as "not stated", because JSON cannot distinguish an absent rate from
a zero one and a zero-rated line resolves to zero anyway. The route's own
description in `router.go` now says all of this, so it reaches the generated
contract rather than living only here.

**Nothing was silently changed, and no existing test needed editing.** The
purchasing half of `internal/api` passes unchanged, which is the evidence that
the register and the tests already agreed.

#### Verified live

| | |
|---|---|
| `standard`, no `tax_rate` sent | net 200.0000, **tax 30.0000** — the register answered |
| `zero_rated`, no rate | tax 0.0000, line rate 0.000000 |
| `standard` with `tax_rate: 0.05` | **400**, naming both numbers |
| `tax_treatment: "reverse_charge_moon"` | **400**, "not a tax treatment SA uses" |

`verify:api` now asserts all four, and fails if an order raised without a rate
comes back carrying no tax — which is the silent failure the old behaviour
produced.

#### The screen no longer asks

`/buying/orders/new` had a **Tax %** box with a panel underneath explaining why
the product could not fill it in. Both are gone. The buyer picks a treatment —
standard, zero-rated, exempt — which is a fact they read off the supplier's
invoice, and the summary says **Before tax** and means it.

`draft-order.ts` computes net only, and its test says why: showing an estimated
tax would be worse than showing none, because it is a number the buyer reads,
remembers, and then finds different on the order they just raised. The full
breakdown is on the order the moment it exists, one redirect away.

### 0.93 Bills and the three-way match

`/buying/bills` and `/buying/bills/{id}`. The match is the best-designed
control in the module and the screen’s job was to get out of its way.

**The evidence is the screen.** The backend KEEPS what the match found rather
than recomputing it — "a control that leaves no record cannot be audited, and
recomputing later would give a different answer once someone amends the order,
which is exactly when somebody would want to check what it originally said." So
all four dimensions are rendered, **including the ones that passed**. A table of
only the breaches answers "what is wrong" and not "what was checked", and the
second is the question an auditor asks.

**The server explains itself and the screen does not paraphrase.** Each
comparison carries a `detail` written by the backend — *"Earlier invoices have
already billed 21 of what was received on this line, so only 3 is still
outstanding."* Rewriting that here would be two explanations of one control,
drifting apart. Shown as sent, under the table rather than squeezed into a
numeric column, because these are sentences.

**Accepting is somebody putting their name to it.** `ApproveBill` refuses an
empty reason — "Say why this discrepancy is being accepted. It is recorded
against your name" — and only a `blocked` bill can be accepted at all; anything
else answers 409. So the reason is required in the form, the button says what
pressing it does to the ledger and that it cannot be undone, and the panel does
not appear on a bill that has nothing to accept.

**Held back is not the same as unpaid, and the screen says which.** A blocked
bill is recorded and deliberately outside the ledger, so `posted` is a badge of
its own beside the status — nothing is owed on it until the difference is
accepted.

Verified live against a bill deliberately made to fail. Billing 40 against 24
received (21 already invoiced) at 24.00 against 18.50 agreed produced four
comparisons: quantity **breach**, price **breach** at 29.73%, tax **pass**
("charged less than agreed, which is in your favour"), total **breach**. Status
`blocked`, `posted: false`.

Bills are priced from the register too, at the **bill date** rather than the
order date: a supplier invoicing in March for goods ordered in January is taxed
at March’s rate, and that is the rate the shop reclaims. Same rule as §0.92,
same assertion check on a stated rate.
### 0.94 Paying a supplier

`/buying/payments`. There is no route that LISTS payments — `POST /payments`
and `POST .../reverse` are the only two — so this is not a ledger of payments.
It is the act of making one, and it starts where the money is owed.

**The allocation is explicit, and the Go comment says why:** *"a shop paying a
supplier is usually paying specific invoices they have agreed, and guessing
which ones would produce a remittance the supplier disputes — which is the
thing that turns a payment into a week of emails."* So the screen offers
"settle everything" as one press and leaves every figure editable, rather than
quietly applying oldest-first.

**A held-back invoice takes no amount.** A blocked bill is deliberately outside
the ledger; its row says so instead of accepting a figure the server refuses,
and "settle everything" skips it.

**Over-allocation is caught beside the box.** It is the one mistake somebody
makes with a keyboard rather than with intent — a digit too many — and the
whole payment is refused for it. Under-paying is not flagged: a part payment is
an ordinary thing to make. Ten tests in `allocate.test.ts`, all in decimal.js,
because this figure gets reconciled against a bank statement.

**The method is a select, from the product’s own vocabulary.** The column has
no enum, so a free box would have been defensible — and a method typed four
ways is four methods in a report. The four offered are drawn from
`sales_tender_method_valid`, narrowed to what a business paying a supplier
uses; mada and Apple Pay are retail tenders.

Verified live: 446.7750 paid in full produced `PAY-2026-000001`, the bill went
to `paid` with `0.00` outstanding, and replaying the uuid returned the same
payment with `already_paid: true` — pressing the button twice on a bad
connection pays once. `verify:api` now drives all of it and fails if a replay
creates a second payment.

**A blocked bill DOES appear in ageing, and that is deliberate.** The
`supplier_ageing` function names three statuses explicitly — `matched`,
`blocked`, `approved` — so somebody decided a disputed invoice still belongs in
a cash-planning figure even though it carries no ledger liability. Verified and
left alone rather than changed; the ageing row carries no blocked breakdown, so
the screen does not invent a distinction the data cannot support.
### 0.95 Sourcing — FE-25 complete, and the four-way split proved

Requisitions, RFQs, the side-by-side comparison and the award. Six screens,
eleven routes, and the whole chain driven against the running server before a
line of UI was written.

#### The split B5.1 exists for, measured

`npm run verify:rbac` is new. It creates staff through `POST /people`, signs in
as them with the password it issues, and drives the chain. Not a unit test over
a permission list — real accounts against the real server.

| | ask | approve | compare | award |
|---|---|---|---|---|
| Inventory Keeper | **201** | 403 | 403 | — |
| Purchase Manager | 201 | **403** | **200** | **403** |
| Owner | 201 | **200** | 200 | **201** |

The seeded Purchase Manager holds eight purchasing permissions and **neither**
`purchasing.approve_request` **nor** `purchasing.award_rfq`. So the buyer who
runs the comparison genuinely cannot sign it off, which is the control, and it
is enforced by the backend rather than by the sidebar.

The same script proves the cashier boundary from §0.91 — four routes at 200,
eight at 403, `/platform/health` at 404 — so both halves of the RBAC claim are
now one command.

#### What driving it first found

**An RFQ to one supplier is refused**, and the message is the reasoning: *"A
quotation from a single supplier is not a comparison. Raise a purchase order
directly instead."* The screen had asked for at least one. It now asks for two
and says why while the buyer is choosing, rather than after they press send.

**Three list routes answer under their own name** — `{requisitions: []}`,
`{rfqs: []}`, `{quotes: []}` — where everything else in the product answers
`{data: []}`. The inconsistency is in the API, and changing three documented
responses to tidy it would break every caller for a cosmetic gain.
`useNamedList` reads whichever it is told to and hands back the ordinary
shape, so nothing else in the app notices.

#### The comparison

`lowest_quote_id` is documented as *"a convenience for the eye, NOT a
recommendation"*, so the badge says **Lowest total** and the line under it says
that lead time and payment terms routinely outweigh price. A screen that said
"recommended" would be making the decision the control exists to record.

Every quote carries what actually decides it — lead time, payment terms, how
long the price holds, and whatever the supplier said about the goods — because
a comparison of totals alone pushes every buyer to the cheapest and makes the
required reason a formality. Cards side by side rather than a numeric grid, for
the same reason: those things are durations and sentences. Beneath them, one
column per supplier line by line, in its own scroll container.

**A supplier who said no is on the screen.** The route records a decline
because *"a missing quote cannot tell you"* the difference between a supplier
who refused and one who never replied — three asked with one quote and one
refusal still has somebody to chase.

**An expired quote cannot be awarded** and says so: the supplier is no longer
offering that price, and an order raised against it would be a commitment the
shop has no grounds to expect them to honour.

#### Requisitions

No prices anywhere on the request screens. `purchasing.request` deliberately
does not carry `catalog.view_cost_price`, and a cost column there would be a
permission leak wearing a form label. There is no draft either —
`RaiseRequisition` creates it `submitted`, because *"a draft that nobody can
see is a request that never reaches an approver, and the shelf stays empty
while the requester believes they have asked"* — so the button says send.

The decision panel is **absent** without `purchasing.approve_request` rather
than disabled: a control the requester can see but not move is an invitation to
ask why. A rejection requires a note and an approval does not, because the
requester has to be able to act on the answer.
### 0.96 Inventory — movements, adjustments, counts, transfers, batches

Nine screens across five documents, driven live before anything was drawn.

**Movements is a ledger, not a balance.** `/stock/on-hand` says where you
are; this says how you got there, and it is the screen somebody opens when
those two disagree with the shelf. Nothing is aggregated — two deliveries of
the same thing on the same day are two lines, because the question is which
one was wrong. The delta carries its own sign and its own colour, because a
column of unsigned numbers with a separate in/out column makes the reader do
the arithmetic.

**Adjustments is a control.** The route note is explicit: *"reading what was
written off is how a manager notices somebody writing too much off; gating it
behind the verb that DOES the writing would hide it from exactly the person
checking."* So the list is `inventory.view`, raising one is
`inventory.adjust_stock`, and the value column is the point of the screen
rather than a detail on it. The reason is a chosen list — a free box gives
"damaged", "Damaged" and "brokn" in one report — with a note beside it for
what the list cannot say.

The form asks **change by**, not count to. The route takes a `delta`, and a
screen that asked for the new total would have to subtract — wrongly, the
moment somebody sold one while the form was open. Asking for the change asks
for the thing that is actually known: two were dropped.

**Blank is not zero on a count.** Counting nothing and counting none of
something are different claims, and the second is a write-off of everything on
the shelf. `differenceOf` returns null for an empty box and the row says "not
counted yet"; nine tests, including that a lot dated today is still good today
and that `1.` half-typed shows nothing rather than flickering to a difference
that was never true.

**A transfer has one next step, so it shows one button.** Requested waits for
an approver, approved waits for whoever packs it, in transit waits for whoever
unpacks it. Three buttons with two greyed out would ask the reader to work out
which is live.

And the server enforces a segregation inside it: *"You raised TRF-000001, so
somebody else has to approve it."* That is on the screen beside the button
rather than arriving as an error after a press — the frontend cannot know who
raised it any better than by comparing names, so it states the rule and leaves
the boundary where it belongs.

**Batches read FEFO**, soonest to expire first, which is the order they get
used. A date that has passed and one that is close both need somebody and they
need different somebodies, so they are separate states rather than one
"attention" colour. Compared at day resolution in the viewer’s own timezone:
a lot is good until the end of the day printed on it, and comparing instants
would mark it expired for anybody west of the shop. Eight tests.

#### Two more nav links that led to a 403

Found by adding these screens to the map the nav test checks.

**Counts and adjustments** pointed at `/stock/counts` and asked for
`inventory.adjust_stock`. A count IS an adjustment — the same document with
`kind: 'count'` — so they share a list, and that list is `inventory.view`. The
old pairing showed the link to somebody the list refuses and hid it from the
manager who reads it to check what was written off, which is the one person it
exists for.

**Transfers** listed `inventory.view` OR `inventory.transfer_stock`. The list
route is the first; holding only the second reached a link the list refuses.

### 0.97 Orders — quotation to invoice, and the three warehouse documents

Five screens: the list, raising one, one order with every step on it, and the
three documents B11 draws.

**It always starts as a quotation.** There is no route that creates a confirmed
order, and the reason is on the route: *"confirming is the customer’s decision,
and a route that could skip it would put ‘the customer agreed’ in the hands of
whoever typed the order."* So the button says quotation, and the line above the
confirm button says what pressing it claims.

**Seven states, one path.** `forward` in the Go is a map rather than a switch
"so the whole graph is one thing a reader can see, and so Advance cannot grow a
branch that quietly allows a step backwards". The same map is in
`lib/orders/orders.ts` for the same reason, and the screen offers the one step
that is live rather than three with two greyed out.

**Picking holds the stock**, and the screen says so: it is the difference
between picking as paperwork and picking as a promise that another channel
cannot sell the same unit.

**A quotation past its date is not cancelled.** `expired` is derived rather
than stored — "a quote does not become a different row at midnight" — so it
reads as out of date, and the price simply stops being one the shop has
promised.

**No tax on the draft, and the screen says why.** An order is taxed when it is
INVOICED, at the rate on file for that date; a quotation raised in March and
invoiced in April is taxed in April. An estimate here would be a number the
customer reads on the quotation and does not find on their invoice. Nine tests
on `orderTotals`, including that a discount larger than the line shows as a
negative rather than being clamped — that is how somebody sees they typed it
into the wrong box, and the server is the one that refuses it.

#### The three documents

A picking slip says what to take off the shelves and where each is kept; a
packing slip is what to check before the box is sealed; a delivery note goes in
the box. Three jobs, not three names for one printout, so each says which.

They render as a page rather than linking at the route, because the route
answers JSON and a link straight to it shows a customer raw JSON. Printing is
handed to the browser, which is also what makes it work on a warehouse tablet
with no printer driver. `order.view` rather than `order.manage`, per the route:
a picker and a driver both need one and neither should be able to change a
price.

`verify:api` fetches all three and **fails if any line carries a price** — B11
forbids it and the type has no fields for it, and that is the kind of invariant
that stops being true the day somebody adds a convenience field. It also checks
that a kind this product does not print is refused by name rather than drawn as
something else.

#### What driving it first found

The document kinds are `picking`, `packing`, `delivery` — the screen had
`delivery_note`, which the route refuses by name. And `advance` takes no body
at all; the 400 that looked like a body problem turned out to be a state I had
put an order into by picking it while it was still a quotation.
### 0.98 Cash, bank and what was spent

Five screens: money accounts, moving money between them, the expense period,
recording one, and one voucher.

**A till has no IBAN.** Five account kinds, and only three can carry bank
detail — the same three the schema allows it on. The form follows that rather
than showing every field for every kind, and sends the bank fields only for the
kinds that can hold them, so a till never acquires an empty IBAN it did not ask
for.

**Unmatched statement lines are on the account list.** `unreconciled` is the
count of statement lines nobody has tied to a transaction, and it belongs where
somebody sees it rather than inside the reconciliation screen it is the reason
to open.

**A transfer is neither income nor a cost.** Cash taken to the bank has not been
earned and has not been spent; it has moved. A list of dated amounts looks
exactly like a list of takings, so the screen says which it is.

**Two tax figures on the expense period, and the second one is money gone.**
E2.3 restricts input VAT recovery by CATEGORY — entertainment, some vehicles,
fuel — and that tax is absorbed into the expense "so the VAT return is not
overstated". One combined tax figure would hide the half that is a real cost.
The form says so as soon as a category is chosen, rather than letting somebody
discover it on the return.

#### Four things driving it first corrected

| assumed | actually |
|---|---|
| `/expenses` is a list | a **period**: totals for a date range with the expenses inside |
| `paid_from` is an account id | **`cash` or `bank`** — a role, and an id is refused |
| transfers need no id | a **`uuid`**, and it is money: a retry must not bank the takings twice |
| `date`, `net`, `total`, `gross` | `expense_date`, `subtotal_net`, `total_inclusive`, `charge_amount` |

The `paid_from` one is the most interesting, and the service says why: *"a role
rather than an account id because these two ARE configuration: every company has
exactly one of each and the chart already maps them."* A treasury-account picker
would have offered a choice the route does not have. `verify:api` now asserts
that an account id there is refused, which is what makes the two-option select
correct rather than a simplification.

`charge_amount` is the other one worth knowing: net plus whatever tax was
absorbed, which is what actually lands in the expense account. That is the whole
point of the split, and a screen showing `net` there would understate every
restricted expense.

#### A duplicate nav id, and a test for it

Adding a **Moving money** entry (C2 covers inter-account movement) collided with
the stock `transfers` item, and the nav test caught it — as a money transfer
being checked against the STOCK transfer’s permission. Ids key that map, and
they are React keys and i18n suffixes too, so a collision is three bugs wearing
one hat. Renamed, and now pinned per workspace.
### 0.99 Expense configuration — categories, departments, standing costs

One screen at `/money/expenses/setup`, three views, behind
`expense.manage_heads`. It is the configuration every expense depends on, and
it holds the one field in the module that is a tax position rather than a label.

**`input_vat_recoverable` is asked as a question, not offered as a checkbox.**
E2.3 restricts input VAT recovery by CATEGORY — entertainment, some vehicles,
fuel — all of it or none of it, never apportioned within one. The API refuses a
request that omits the field and says exactly why:

> *"Defaulting either way is wrong: false silently stops a shop reclaiming VAT
> it is entitled to, true silently claims VAT on entertainment. E2.3 makes this
> a decision, so the request has to carry one."*

A checkbox has a default. A select whose first option is empty does not, so the
form is a select with two full answers — "Yes, it goes to input VAT" and "No, it
becomes part of the cost" — and `verify:api` asserts that omitting it comes back
400 with the field named.

**Quarterly is monthly, three at a time.** The service accepts `weekly`,
`monthly` and `yearly` and puts the answer in its own refusal: *"Use
interval_count for anything else: monthly every 3 is quarterly."* Nobody signs a
lease "monthly, every three", so the form offers the five cadences a business
actually agrees to — weekly, fortnightly, monthly, quarterly, yearly — and sends
the `frequency` + `interval_count` pair each one means. `describeCadence` reads
a stored schedule back the same way, and falls through to "Every 4 months" for
an interval no preset covers. There is no plural machinery in that fallback and
none is needed: an interval of one is always a preset, so the number is never 1.

**A schedule posts nothing.** Booking is `POST /expenses/recurring/generate`,
which calls the same `Record` path a person typing an expense takes — so the
tax treatment, numbering and audit record are the ones expenses already have.
That is also why it is gated on `expense.record` and not on the permission that
opens the screen: somebody who may not record an expense must not be able to
make a schedule do it for them. The button appears only for somebody holding it,
and running it twice is safe because the guard is a unique index on
(schedule, due date) rather than a check the client performs.

#### What driving it first corrected

The frontend types were written from the migration and were wrong in five ways.
All five were found by `scratchpad/drive-expcfg.mjs` against the running server,
before a screen existed.

| assumed | actually |
|---|---|
| `cadence`, `next_due` | `frequency`, `interval_count`, `next_due_on` |
| `quarterly` is a frequency | refused — it is `monthly` with `interval_count: 3` |
| a schedule needs no name | `name` is required: *"What it is for — \"Shop rent\", \"Internet\"."* |
| both `active` toggles answer alike | a **category** answers 204 with no body; a **department** answers 200 with the row |
| a category code can be edited | the UPDATE statement does not touch it, so the field is disabled rather than accepted and ignored |

The last one is the kind that only shows up live: `PUT` with a different code
returns 200 and the original code, which looks exactly like a successful save
until somebody reloads. `verify:api` now pins it.

#### Verified live

```
ok GET /expenses/accounts
ok a category with no VAT decision is refused, and the field is named
ok a category code is fixed once saved, and an edit cannot move it
ok a category is retired rather than deleted, and comes back on request
ok a department answers its toggle with the row, unlike a category
ok quarterly is refused as a frequency, and the refusal says what to send
ok quarterly is monthly every three, and stores as one
ok a standing cost is paused rather than deleted
ok booking what is due twice books nothing the second time
```

And the split itself, through a restricted category: `tax_recoverable "0.00"`,
`tax_absorbed "30.00"`, `charge_amount "230.00"` on a net of 200 — which is
E2.3 behaving, measured rather than assumed.

#### The RBAC boundary, with real accounts

`verify:rbac` gained a third section. The **Branch / Store Manager** role is the
exact case: it holds `expense.view` and neither `expense.manage_heads` nor
`expense.record`.

```
ok branch manager: GET /expenses            -> 200
ok branch manager: GET /expenses/heads      -> 200
ok branch manager: GET /expenses/accounts   -> 403
ok branch manager: PUT /expenses/heads/{id} -> 403
ok branch manager: POST /expenses/recurring/generate -> 403
ok accountant:     GET /expenses/accounts   -> 200
ok accountant:     POST /expenses/recurring/generate -> 200
```

`GET /expenses/accounts` being 403 for a branch manager is why the sidebar entry
names `expense.manage_heads`: an entry shown on `expense.view` would be a link
somebody follows into a refusal. The nav test pins that too — `expense-setup` is
mapped to `/api/v1/expenses/accounts` in `PRIMARY_READ`, so the permission on
the link and the permission on the route cannot drift apart.

#### A tabs primitive, built once

`components/ui/tabs.tsx`: a real `role="tablist"` with one tab stop and the
arrow keys moving between tabs, mirrored in Arabic by the component rather than
by renaming the key. `role="tab"` announces a promise about the keyboard, and
the cheapest way to break it is to use the role without keeping it. Selection is
carried by an underline rather than colour alone. The value lives in the URL
(`?on=categories`), so the view can be sent to a colleague and Back undoes it.

#### One pre-existing defect, fixed in passing

The Bangla catalogue had `IBAN` and `SWIFT` untranslated — added with the money
accounts screen in §0.98 and caught by `locale.test.ts` once this module's keys
were inserted. Arabic transliterates both; Bangla now does too
(`আইব্যান`, `সুইফট`).

### 0.100 Bank reconciliation — and a refusal that arrived as a crash

Two screens: the statements brought in, and the working screen where a person
pairs the bank's lines with the books and signs the result off.

C11 opens with the sentence the module serves — *"Proves that what the software
says is in the bank is actually what the bank says"* — and the service states
the arithmetic that claim reduces to:

    closing balance
      - the ledger balance on that account at that date
      = the unmatched items      <- and nothing else

**Signing off is refused while anything is unexplained, and that refusal is the
feature.** The service is blunt about why: a reconciliation that can be signed
with a difference nobody accounts for *"is a piece of paper, and the auditor who
relies on it has been misled by a screen."* So the button is present and refused
rather than hidden, because the refusal names the amount and is the most useful
sentence on the screen.

**A rule's guess and a person's decision are different claims.** The importer
auto-matches on exact amount within three days, and says what that is: *"It is
usually right and it is occasionally very wrong — two identical supplier
payments on the same day are indistinguishable to any rule."* Every row says
which kind of match it carries, and either can be undone.

#### Signed off and balancing are two questions, and they can disagree

`status` is whether a person put their name to it. `difference` is recomputed
from today's books every time the row is read. Driving it found a live case: a
statement whose `status` was `reconciled` came back with `reconciled: false`,
because a second statement imported afterwards changed the cumulative
arithmetic underneath it.

Conflating those two fields would either hide a real change or claim a sign-off
nobody made, so the badge reads `status` and the figure reads `difference`, and
a signed-off statement whose arithmetic has since moved says so.

#### 🔴 Backend defect: a deliberate refusal reached the caller as a 500

Found by driving the screens, not by reading the source.

`POST /treasury/lines/{id}/match` on a signed-off statement answered **500 —
"Something went wrong on our side."** The trigger that freezes a reconciled
statement was working perfectly and had written the reason for the reader:

> *"A reconciled statement cannot be changed. Reopen it first, which is
> recorded."*

The message never got out. The trigger raises with
`USING ERRCODE = 'restrict_violation'` — SQLSTATE `23001` — and
`db.Translate` knew only `P0001`, the default RAISE code. Everything else fell
through to `CodeInternal`.

That is worse than an unhelpful message. A 500 says the fault is ours, invites a
retry that will fail identically, pages whoever watches the error rate, and
hides a refusal working exactly as intended.

**Two changes, both small:**

| file | change |
|---|---|
| `internal/platform/db/db.go` | `Translate` routes `23001` through `classifyRaise`, exactly as it already did `P0001` |
| `internal/treasury/reconcile.go` | `Unmatch` translates its driver error instead of returning it raw |

**Two other triggers raise the same way** and had the same fault: a posted stock
voucher (`0079`) and an invoiced order (`0085`). Both now surface their own
sentence as a 409.

`TestAReconciledStatementIsFrozen` already existed and did not catch this — it
inserts straight into the table, so it proves the trigger fires and says nothing
about what an HTTP caller is told. Its new pair,
`TestAFrozenStatementRefusesRatherThanFailing`, goes through the route. It fails
on the old code with the exact 500 and passes on the new.

#### Pasting a statement, rather than retyping one

There is no upload route and inventing one would be inventing a backend. What a
bank sends is a CSV and what a person does with it is open it, tidy it and copy
the rows — so the form takes the rows.

Four fixed columns, `date, description, reference, amount`, stated above the
box. Not a heuristic that finds the date column on its own: banks differ, and a
heuristic right nine times in ten files a March charge in April on the tenth.

**ISO dates only.** `03/04/2026` is the third of April in Dhaka and the fourth
of March in California, and nothing in the row says which. Guessing would be
silently wrong for half the markets this product sells into, so a non-ISO date
is refused per row, naming the row.

The arithmetic is checked beside the box as the paste lands. The API refuses a
statement whose lines do not reach its own closing figure — *"Check that every
line was imported"* — and that is a truncated paste nine times in ten. Finding
it while pasting beats finding it in a 400.

`parseStatement` and its friends are 22 tests in `lib/money/statement.test.ts`,
including the one that matters: two lines of `0.10` and `0.20` total `0.30`,
because this is a figure somebody will reconcile against a bank.

#### Verified live

```
ok a till has no statement, and the import is refused
ok a statement that does not reach its closing balance is refused
ok a statement with no lines on it proves nothing, and is refused
ok GET /treasury/statements/{id}
ok   statement line
ok   entry the bank has not seen
ok a line can be paired by hand and unpaired again
ok signing off is refused while something is unexplained, and says how much
```

#### Four tests that had a tax rate hidden inside them

Running the full backend regression for this module turned up four failures in
`internal/api` that had nothing to do with reconciliation, and everything to do
with §0.92. They were a committed regression, and they had been sitting there
because **migrations 0125 and 0126 had never been applied** — the API process
still running was an older build, so every check since had been passing against
a database one schema behind.

Since §0.92 a bill line with no `tax_treatment` is standard-rated and priced
from the regulatory register. So `1 × 1000.00` is not a 1000.00 bill in Saudi
Arabia. Three tests said it was:

| test | said | means |
|---|---|---|
| `TestASupplierWhoIsStillOwedMoneyCannotBeHidden` | the refusal contains `"1000"` | the refusal names the amount owed |
| `TestASettledSupplierCanBeRetired` | pay `1000.00` | pay the bill off |
| `TestPayingIsIdempotent` | outstanding is `600.00` | the retry paid once |
| `TestAgeingMeasuresFromTheDueDate` | the bucket holds `1000.00` | the money landed in the 31–60 bucket |

Each now reads `total_inclusive` back from the bill and works from that. The
tests say what they mean, and they no longer carry a tax rate that a market can
change — which is the rule for the product's own code and had no business being
broken in its tests.

#### The RBAC boundary, and the Auditor

```
ok branch manager: GET /treasury/accounts   -> 403
ok branch manager: GET /treasury/statements -> 403
ok branch manager: POST /treasury/statements/{id}/reconcile -> 403
ok auditor:        GET /treasury/statements -> 200
ok auditor:        POST /treasury/transfers -> 403
ok auditor:        POST /treasury/accounts  -> 403
```

The Branch Manager assertion was written expecting `accounting.view` and was
**wrong**: the seeded role holds no `accounting.*` at all. Role 0005 describes a
store manager as unable to *"see bank ledgers or true net profit"*, and the seed
enforces that completely rather than partially. The test now says what is true,
which is also why the nav entry names `accounting.reconcile` rather than
`accounting.view`.

The **Auditor** is the sharper case and the reason the permission exists at all:
they may reconcile and may not post. Somebody who could correct the books they
are checking is not checking them.

### 0.101 Exchanges — and three defects that only a running server shows

`/pos/exchanges`. Scan the receipt, pick what is coming back, scan what is
going out, settle the difference. One screen and one request, because the
service puts both halves through a single transaction: *"a till that issued the
credit note and then failed to place the sale would have given the goods away;
one that placed the sale and failed to credit would have charged twice."*

**Only the difference goes through the drawer.** A customer swapping a 100 item
for a 150 one hands over 50; the offsetting 100 goes through a clearing account
and is never a tender. The service says why: a drawer expected to hold cash that
never moved through it shows a variance at close with no cause. The screen shows
the two totals and then the one figure that matters, with the direction in
words — "The customer pays" or "You hand back".

**The difference is settled exactly, and one press does it.** The server states
the figure and refuses anything else, because *"an overpayment is change owed,
and treating it as part of the sale overstates takings and the VAT on them."* So
the tender buttons carry the amount rather than opening a keypad: it is not the
cashier's number to choose.

The arithmetic is 24 tests in `lib/pos/exchange.test.ts`. The client's figures
are an estimate and the file says so — the credit is pro-rata from
`returnable`, which agrees with the server for a whole line and for any line
without an allocated discount. Where it disagrees, the server refuses with the
exact amount it settles at, and that sentence is what gets shown.

#### 🔴 The till could not sell at all in a two-location shop

`POST /pos/sales` answered **400** on the first attempt to drive one:

> *"This branch has more than one stock location, so the sale must say which one
> it is selling from."*

The till was sending no `warehouse_id` at all. A shop with a shop floor and a
back room — which is an ordinary shop, and is what the dev tenant became during
§0.96's inventory work — could not ring up a sale, take a return or make an
exchange. Nothing in the source showed it: the field is optional in the request
and the server only refuses when the branch is ambiguous.

The setting exists (I5's default warehouse, migration 0009), but
`GET /devices/{id}/settings` is `devices.view` — a manager's permission. **A
cashier cannot read their own till's configuration.** So the answer is given
where the person is: a one-time choice at the counter, kept in session storage
beside the counter id and sent on every sale, return and exchange.

Asked **only** when there is something to ask. One location resolves silently
and the till opens straight away, because adding a screen to every shift for a
question with one answer is worse than the bug.

#### 🔴 An idempotent replay came back hollow

The property held — a retry created no second credit note and no second invoice,
and burned no ICV. But the body it answered with was empty:

| | first call | retry |
|---|---|---|
| `credit_note_id` | 996e44c6… | 996e44c6… ✓ |
| `human_number` | CRN-MAIN-2026-000004 | **""** |
| `total_inclusive` | 100 | **0** |
| `difference` | 25 | **0** |

`alreadyRefunded` and `alreadyRung` loaded the id and the ZATCA link and
nothing else. So a till doing exactly what it is built to do — pressing again
when the first answer never arrived — showed the cashier a completed exchange
worth nothing, with a blank number to read to the customer, while the books said
25 had changed hands.

The same mistake `CreditNoteNumber`'s own comment records having been found
once before by photographing a screen after a refund, made again on the other
path. Both queries now load the totals and the number.

`TestRetryingAnExchangeDoesNotSellTwice` existed and did not catch it: it
compared ids. **Matching ids is not the same as replaying.** It now compares
`human_number`, `total_inclusive`, `credit_applied`, `difference` and
`customer_paid`, and fails on the old code with all six.

#### 🔴 Three roles were being shown links into a refusal

The Returns entry was shown on `sales.refund` **or** `sales.exchange`, and the
screen behind it is guarded on refund alone — so anybody holding only exchange
saw a link and got "you do not have permission". Splitting it into two entries
fixed that one, and a new test looks for the whole class:

`navigation.built.test.ts` now reads each page's own `RequirePermission` out of
the source and asserts that every permission which SHOWS an item is one the
guard ACCEPTS. It immediately found two more, both live:

```
goods-receipts:     shown on purchasing.view, but /buying/receipts accepts purchasing.receive_goods
supplier-payments:  shown on purchasing.view, but /buying/payments accepts purchasing.pay_supplier
```

The comment on the first one said *"BOTH, not either"* — and `permissions` is
any-of, so it could not mean that. An **Auditor** was offered Goods receipts; a
**Branch Manager** and a **Purchase Manager** were offered Supplier payments.

So `NavItem` gained `alsoNeeds`: the act goes in `permissions` (any-of, and
what the guard checks), the reads the screen cannot work without go in
`alsoNeeds` (all-of). The comment is now something the resolver can act on.

#### Verified live

```
ok a branch with two stock locations refuses a sale that does not name one
ok GET /pos/sales/{id}/returnable
ok an exchange settles at the amount the server states, and says what it is
ok an exchange with no reason on it is refused
ok POST /pos/exchanges
ok   credit note
ok a retried exchange replays the same documents AND the same figures
```

And the boundary, with real accounts:

```
ok somebody with neither verb: POST /pos/returns      -> 403
ok somebody with neither verb: POST /pos/exchanges    -> 403
ok somebody with neither verb: GET /pos/sales/lookup  -> 403
```

The seeded roles grant refund and exchange together, which the run reports
rather than hides. The split still matters: the permissions are separate, a
role built by hand can hold one without the other, and an exchange writes an
invoice as well as a credit note — it puts goods out of the shop, which taking
a return does not.

#### A trap worth naming: `decimal.js`'s `isPositive()` is true for zero

Four of the exchange library's own tests failed on the first run, all from one
cause: `new Decimal(0).isPositive()` is `true`, because the method reads the
SIGN and zero's sign is positive. Written the obvious way, an empty quantity box
counted as a line to return, a returnable quantity of nothing was divided into
a total and produced `NaN` on screen, and "there is nothing coming back, so
this is a sale rather than an exchange" never fired at all. Every comparison
now asks `greaterThan(0)`, and the file says why.

### 0.102 Purchase returns — a Blueprint feature the backend did not have

Blueprint B5: *"Purchase Return (to Supplier): for defective/excess stock —
auto-generates a Debit Note and instantly deducts inventory."*

**It did not exist.** Thirty-one purchasing routes and none of them sent
anything back. The backend sweep's row for B5 says "returns" and means
`reversing.go`, which reverses a PAYMENT — a different fact about a different
thing. So this was not a frontend task at all: migration 0127, a service, three
routes, a permission, and only then a screen.

#### Where the answers came from, rather than from me

Every decision has a precedent in the repository, which is how a module this
close to money and stock can be added without inventing accounting.

| question | the repo's own answer |
|---|---|
| Against a receipt or a bill? | 0014: *"A credit or debit note has no meaning without the invoice it corrects."* Goods refused at the door are already `grn_line.qty_rejected` and never entered stock. |
| How does it post? | The mirror of `purchase.credit` (0025 rule 3), which separates input tax from inventory value because *"merging them overstates stock while understating the reclaim."* |
| Where does the difference go? | `cost_variance` — the account 0025 rule 11 was written for and 0048 repaired. |
| Can it be returned twice? | Cumulative per line, as 0019 does for a customer return, exposed as a view so the rule is queryable. |
| Its own permission? | Yes. 0032 gives the Store Manager `receive_goods` and not `record_bill`; a return is a claim against a bill they cannot read. |

#### Two figures, kept apart on purpose

The **supplier** is claimed what they billed: their price, their tax rate. That
is their document and what they will argue with.

The **stock** leaves at what the valuation says those units were worth — the
costing method's answer, which differs whenever freight was added on receipt or
a cheaper batch has been bought since. The integration test found this
immediately: a return of 3 × 100.00 claimed 300.00 and released 240.00, because
FIFO sent back the older, cheaper layer.

Forcing them together would mean either claiming the wrong amount or parting the
stock report from the balance sheet, so both post as they are and the gap goes
to variance. `TestAReturnBooksTheGapBetweenTheClaimAndTheCost` puts freight on a
delivery, returns one unit, and asserts the trial balance is still zero — which
is the assertion that proves the variance line carried it rather than nothing.

#### 🔴 Two bugs of my own, both found by running it

**The header could not be written.** `purchase_return` is immutable from the
moment it exists — the stock has left the building, so there is no state in
which the document is half-written and correctable. Inserting it and then
filling in the totals is an UPDATE, and the trigger refused it. The id is now
minted in Go and the header written once, complete; the stock movements carry
that id in `source_id`, which has no foreign key and so can be written first.

**A return from an empty shelf silently claimed the money.**
`inventory.Consume` REPORTS a shortfall rather than refusing one, because
whether stock may go below zero is the company's policy and not the stock
package's business. Skipping `CheckAvailability` meant a return raised against a
back room that had never held the item took nothing out, valued the goods at
zero, and still claimed the full amount from the supplier — posting the whole
claim to variance while reading, on screen, as a successful return.

Found by driving one against a location with no stock. `CheckAvailability` was
also reworded: it said *"than this sale needs"*, which had no production caller
at all and would have read as nonsense on a debit note.

#### 🔴 And a third, found by the verification suite

`verify:api` failed after 0127 with **"A payment of nothing is not a payment"**,
raised on a bill whose outstanding had gone below zero.

0127's service reduced a bill by writing the claim into `amount_paid`, because
that is the column payables subtracts from. It works arithmetically and it is
wrong twice: it tells the supplier portal and the ageing report that goods taken
back were paid for, and on a bill paid BEFORE the return it pushes
`total - paid` negative, which the payment screen then offers as something to
settle.

Migration 0128 gives a credit a column of its own, moves whatever 0127 put in
the wrong one, and floors what is owed at zero — because a supplier who has been
paid and then handed goods back owes the shop money, which is a debit balance on
the supplier rather than a negative payable on one invoice.

#### Verified live

```
ok GET /purchasing/bills/{id}/returnable
ok   returnable line
ok a return with no reason on it is refused
ok a return that does not say which shelf the goods left is refused
ok more than the bill carried cannot be sent back
ok POST /purchasing/returns
ok GET /purchasing/returns/{id}
ok a retried return claims once and replays the whole claim
ok what may go back falls by what went back
```

And the boundary, with the seeded roles printed rather than assumed:

```
-  Store Manager:    receive=true  return=false bill=false
-  Purchase Manager: receive=true  return=true  bill=true
ok store manager:    GET  /purchasing/returns -> 200
ok store manager:    POST /purchasing/returns -> 403
ok purchase manager: POST /purchasing/returns -> 400, past the gate
```

Eight integration tests, including the trial-balance tie-out on both the
ordinary case and the freight case, and the one that proves a paid bill cannot
be owed backwards.

### 0.8 Exact next task

**FE-25 purchasing is COMPLETE** — suppliers, orders, receiving, bills with
the three-way match, supplier payments, ageing, and the sourcing half with the
four-way permission split proved live (§0.88 – §0.95). Twelve screens,
thirty-one routes, `verify:api` and `verify:rbac` both green.

Inventory is done (§0.96): movements, adjustments, counts, transfers and
batches, nine screens with the contracts pinned in `verify:api`.

Stock and orders are done (§0.96, §0.97).

Cash, bank and expenses are done (§0.98).

Expense configuration is done (§0.99): categories with the input-VAT decision,
departments, and standing costs with the generate pass.

Bank reconciliation is done (§0.100), and fixing one backend defect on the way:
a deliberate refusal was reaching callers as a 500 on three different triggers.

Sales returns and exchanges are done (§0.101), with three live defects fixed on
the way — including one that stopped the till selling at all in a shop with two
stock locations.

Purchase returns are done (§0.102) — and were a backend gap, not a frontend
one: the feature did not exist and was built, with two migrations and eight
tests.

Next: **complete accounting and manual journals**, then receivables, payables
and the financial statements.

FE-16 and FE-21 are done: both closed links the product was already offering
-- the products list opened a row at `/products/{id}` that did not exist, and
the dashboard has been pointing at `/stock` since it was built. A dead link in
the one place the product says what needs attention is worse than a module that
has not started, which is why those two came before larger modules.

The next three, in order, each for a reason:

1. **FE-18 — returns and exchanges** (`/pos/returns`, `/pos/exchanges`,
   `GET /pos/sales/{id}/returnable`). The sales day now lists invoices; a return
   is what somebody does with one, and `returnable` says what may go back.
2. **FE-19 — closing a shift** (`/shifts/{id}/close`, `/cash-drop`, `/x-report`).
   The till can open a session and cannot close one, so a counter opened in the
   product cannot be reconciled in it.
3. **FE-25 — purchasing.** 31 routes and the largest module with nothing at all;
   stock on hand now shows what is running out and offers no way to order more.

Before each, add its endpoints to `npm run verify:api` the way the built
screens are covered. R6 is closed: see §0.91. A live Cashier account now proves the boundary, and
closing it found four frontend defects.

### 0.85 Running it locally

What Gate 1 actually needed, so the next person does not rediscover it.

```bash
# 1. A throwaway Postgres. Published on 5433 so it cannot collide with a
#    native one already holding 5432.
docker run -d --name rawsyst-dev-db -p 5433:5432 \
  -e POSTGRES_DB=rawsyst -e POSTGRES_USER=rawsyst -e POSTGRES_PASSWORD=rawsystdev \
  postgres:17-alpine

# 2. An application role that is NOT a superuser.
#
#    This step is the one that matters. The postgres image makes POSTGRES_USER a
#    SUPERUSER; superusers ignore row-level security completely; and a
#    deployment connecting as one has no tenant isolation at all while every
#    policy still sits in the catalogue looking correct.
#    TestConnectionCannotBypassRowLevelSecurity exists to catch exactly this,
#    and its comment records that CI ran for days against a superuser once.
psql -h localhost -p 5433 -U rawsyst -d rawsyst <<'SQL'
CREATE ROLE rawsyst_app LOGIN PASSWORD 'rawsystapp' NOSUPERUSER NOBYPASSRLS;
-- Extensions need the superuser, so they are created before handing over.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
GRANT ALL ON SCHEMA public TO rawsyst_app;
SQL

# 3. Migrate and seed AS THE APP ROLE, so it owns the tables and FORCE RLS
#    applies to it.
export RAWSYST_ENV=development
export RAWSYST_DB_DSN="postgres://rawsyst_app:rawsystapp@localhost:5433/rawsyst?sslmode=disable"
export RAWSYST_JWT_SECRET=dev-only-secret-not-for-any-real-deployment
export RAWSYST_DATA_ENCRYPTION_KEYS=1:MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
export RAWSYST_METRICS_ENABLED=false

cd backend
go run ./cmd/migrate
# -platform-email creates the operator as well. Without it the whole Platform
# workspace is unreachable: an operator is a user with no tenant, and no screen
# in the product can make one.
go run ./cmd/devseed -email owner@example.test -password 'DevPassw0rd!2026' \
  -platform-email ops@example.test
go run ./cmd/api                      # :8080

cd ../web-next
npm run dev                           # :3001, proxying /api/v1 to :8080
npm run verify:api                    # every screen contract, against the server
                                      # signs in twice: owner, then operator
```

**Run `devseed` once per owner email.** A second run with the same
`-email` creates a SECOND business rather than reusing the first, and sign-in
then stops on the picker for every tool that expects a token straight back.
`verify:api` reports the choices and the `RAWSYST_DEV_TENANT=` line that picks
one, rather than failing on an undefined.

The same environment runs the Go integration tests:
`go test -tags integration ./internal/...`. Without `RAWSYST_DB_DSN` every
database test skips and still prints `ok`, so a green run means nothing until
the DSN is set.

**A fresh seed cannot make a Saudi sale until two fixtures are corrected.**
`devseed` leaves its EGS unit with an empty VAT number and the branch with no
National Address, so the ZATCA compliance gate refuses every sale with
`compliance_blocked`. Amend the unit through `PUT /einvoicing/units/{id}` with a
valid non-group VAT — 15 digits, first and last a 3 — and fill in the branch
address. See risk R7.

### 0.9 Risks

| | |
|---|---|
| **R1 — i18n, now guarded rather than owed** | Every built screen is translated, and two tests keep it that way: the shared coverage check reads the components rather than the catalogue, and a physical-property check refuses `ml-`/`text-right` so Arabic keeps mirroring from `dir` alone. A new screen that skips `t()` fails CI. |
| **R2 — ZATCA is deliberately isolated** | FE-33 is listed for completeness. The only genuinely external dependency is the taxpayer's own Fatoora OTP, which must never be fabricated. |
| **R3 — `bn` catalogue is partial** | Complete `en` and `ar`, partial `bn`; the provider falls back per key to English, which is honest but not finished. |
| **R4 — React types pinned by `paths`** | The workspace root hoists `@types/react` 18 for `web/` and `pos/`. Remove the mapping only when `web/` is gone. |
| **R5 — RLS depends on the connection role** | A superuser connection silently disables every tenant policy. Development must use a `NOSUPERUSER NOBYPASSRLS` role; `TestConnectionCannotBypassRowLevelSecurity` is the guard. |
| ~~**R6 — Not validated with a non-owner account**~~ **CLOSED, §0.91.** A Cashier was created through `POST /people`, signed in with the one-time password it issued, and driven against the real API: 4 authorised routes answered 200, 9 unauthorised answered 403, and `/platform/health` answered 404. Closing it found four frontend defects, three of which a seeded role hit on sign-in. |
| **R7 — A fresh dev database cannot sell** | `cmd/devseed` creates an EGS unit with an empty VAT number, so the Saudi compliance gate refuses every sale until it is amended and the branch National Address is filled in. Worth fixing in devseed; recorded here so the next person does not lose an hour to it. |

### 0.10 Tool and skill decision log

Nothing is listed here that was not actually run. Where a tool was inspected and
rejected, the reason is recorded — a tool used to satisfy a checklist is worse
than one left alone, because it drags a second design language into a product
that has one.

#### Used, and what it changed

| Tool / skill | Why it was relevant | Where | Result |
|---|---|---|---|
| **Serena** | Repository, API and permission tracing; safe edits | Throughout, both sessions | `find_symbol` on `Overview`, `Company`, `VariantSummary`, `Entitlements`, `Terminal`, `Health`, `ReturnableLine`, `Report`; `get_symbols_overview`; 5 project memories; `replace_content` for every Go and TS edit. **Every payload type in `web-next` was read out of Go source rather than guessed** — which is what made the live-validation pass a check rather than a discovery |
| **`frontend-design`** | Product visual direction | Global shell, tokens, dashboard | Its calibration list of AI-design tells is directly why the palette is not cream-and-terracotta, why there are no ALL-CAPS eyebrows, no `→` in button text and no monospace data labels. The green-family identity and the ledger double-rule came from reasoning about the subject, which is what the skill asks for |
| **`vercel-react-best-practices`** | Bundle and re-render performance | `nav-tree.tsx`, audit of all screens | Found `import * as icons from 'lucide-react'` — a barrel import that defeats tree-shaking entirely, because the bundler cannot know which of ~1,500 icons a runtime string selects. Replaced with an explicit map (`nav-icons.ts`). Audited the rest against `rendering-conditional-render`, `rerender-no-inline-components` and `js-set-map-lookups`: no other violations |
| **`web-design-guidelines`** | Interface quality review | Design tokens + every screen | Fetched the live rules and applied seven: `touch-action: manipulation` globally (a 300ms double-tap delay on a till is the difference between immediate and broken), an intentional tap-highlight colour, `text-wrap: balance`/`pretty`, `overscroll-behavior: contain` on every dialog and drawer, `content-visibility` on table rows, and `spellCheck={false}`/`autoCorrect="off"` on the barcode and code fields — autocorrect on a barcode field changes a scan into something not in the catalogue |
| **`ui-ux-pro-max`** | UX patterns for forms and tables | `form-error.tsx`, table audit, purchasing | Four `--domain ux` searches across two sessions, per its own query contract (a design system already exists; regenerating one would have produced a second visual language). Forms returned a genuine gap: a **focusable error summary** — a keyboard or screen-reader user pressed Save, the form refused, and nothing told them. `FormError` now takes focus on the transition into an error and is used by sign-in and the till. The table search returned four rules the product already satisfied, which is a result worth recording rather than a change. **Purchasing, third session:** the first query ("editable line items table keyboard entry") came back matching *line* typographically -- line height, line length, line balance -- which is a miss, and the skill’s own contract says retry once, narrower. The retry ("inline validation destructive confirmation data entry") returned four applicable rules: **Confirmation Dialogs** shaped the one confirm step in purchasing, on issuing an order, which is the only irreversible act in the module; **Redundant Entry** ("auto-populate prior values") is why a receiving line carries the ordered quantity into "everything arrived" rather than asking for it again; **Error Placement** and **Focusable Error Summary** the product already satisfied through `Field` and `FormError`, verified rather than changed |
| **21st.dev MCP** | Interaction research | Cash-drawer count | Searched for a denomination counter. Everything on offer is a generic number pad or an animated currency ticker — nothing for "how many 500 notes, how many 100s", and a rolling animated figure on a number somebody is reconciling would be actively wrong. **Looked, found nothing suitable, built it by hand.** That is the honest outcome of design research, and it is why nothing was installed |
| **Stitch MCP** | Existing design exploration | Audit only | `list_projects` found "Modern POS Interface" (2026-08-14) carrying a full RawSyst design system: blue primary, Inter, JetBrains Mono for data labels, ALL-CAPS `label-caps`. That is the **superseded** direction — the current brief bans monospace data labels and caps eyebrows, and the identity is now the green family. Recorded so nobody re-imports it. Not used for generation |
| **shadcn** | Accessible primitives | `button.tsx`, all variants | `@radix-ui/react-slot` for `asChild`, and the CVA variant pattern. `shadcn add` was never run: every primitive in `components/ui/` is written against RawSyst tokens |
| **Docker · Go toolchain · psql · curl** | Gate 1, and every gate since | §0.2, §0.85, §0.86-0.89 | A throwaway Postgres, `cmd/migrate`, `cmd/devseed`, `cmd/api`, `go test -tags integration`, and the contract sweep that became `npm run verify:api` |

#### Inspected and deliberately not used

| Tool / skill | Why not |
|---|---|
| **`design-taste-frontend` / `taste-skill`** | Its bundled skills are editorial and marketing-site directions — brutalist grids, cinematic brand boards, hero image generation. RawSyst is a ledger read under fluorescent light by somebody with a queue; adopting any of them would replace a working identity with a louder one |
| **`gsap-master`, `motion-framer`** | The product has one motion rule: a 120ms colour transition, a spinner, and nothing else, with `prefers-reduced-motion` honoured globally. An animation library would be a dependency in service of nothing. Kept as knowledge, not installed — which is also what `FRONTEND_TOOLBOX.md` §7 already says |
| **`convex`** | The Go service is the backend and the security boundary. Adding a second one is not a design decision, it is a rewrite |
| **`vercel-react-native-skills`** | There is no native surface. The brief itself says not to force React Native patterns into the web app |
| **Skiper UI, Magic UI, UIverse** | Component sources. Importing from any of them brings its own idiom — spacing, radii, motion, colour — into a product whose whole point is that every screen belongs to the same one. Nothing was taken |

#### What running it has cost and returned

Nine defects have now been found by driving the real server rather than reading
its source, and none of them was visible in the Go: seven payload mismatches in
Gate 1, two in `estimateUnitCost`, and three in purchasing (`tax_treatment`
never read back, a tax rate of 15 accepted as 1500%, `tracks_batches` exposed
nowhere). Two of the three purchasing ones are silent -- an editor would have
reset a line's tax by changing a delivery date, and a 948 order would have gone
to a supplier as 15,168. Neither has a UI symptom until somebody is owed money.

That is the argument for `verify:api` being a script rather than a session: it
asserts the FIELDS each screen reads, it drives the writes as well as the reads,
and it runs in a minute.

#### The synthesis rule, in practice

The tools supplied research and rules. Not one component was installed from any
catalogue. Every primitive in `web-next/src/components/ui/` is written against
`globals.css` tokens, so a change to the palette, the radius scale or the type
scale moves the whole product at once — which is the test of whether a design
system exists or whether the screens merely resemble each other.


---

## 1. Verified state

Everything below was checked against the code in this session, not against a
previous report. Nothing is marked complete on the strength of a file existing.

| Area | Status |
|---|---|
| Multi-shop / multi-counter model | **COMPLETE** |
| Concurrent multi-counter sales | **COMPLETE** |
| RBAC + tenant isolation | **COMPLETE** |
| Inventory / stock (server-side source of truth) | **COMPLETE** |
| Sales · returns · shifts · cash sessions · audit | **COMPLETE** |
| ZATCA / EGS isolation from the sale path | **COMPLETE** |
| Web POS session lifecycle | **PARTIAL** — online only, no front end |
| Market behaviour (SA / BD / US) | **PARTIAL** — US cannot sell |
| Production boot gate (market-aware) | **COMPLETE** |
| Hard-coded Saudi assumptions | **PARTIAL** — HR/privacy/compliance remain |
| Placeholder / unverified legal values | **PARTIAL** — 17 of 42 unverified |
| Migrations + full test suite | **COMPLETE** — full suite green, 21 packages, 0 failures |

### The counter model (this session's main work)

```
business (tenant) -> company -> shop (store) -> counter -> session -> sale
```

A counter **is** a `device` row. No second POS, no second table, one sale path.
`device.binding` (0104) records only how a session on it is authorised:
`session` (any user the RBAC scope allows, created active — the web counter) or
`paired` (the enrolled machine, proved by its OS-keystore secret). **Enrolling
moves a counter `session` -> `paired`** in the same statement that writes the
secret, which is the forward path to the Tauri app on the same API.

`POST /api/v1/pos/counter-sessions` re-issues the caller's OWN access token with
`did` set — same session, same user, 15-minute TTL, **no refresh token**, company
resolved from the counter. The middleware re-checks the device is active on every
request, so a pause or revoke takes effect immediately.

**Accepted trade-off:** a `session` counter is proved by a permission, not by a
machine. Tenant isolation, route permissions, company/store scope, the open-shift
requirement and an audit trail naming user *and* counter all still apply.

---

## 1b. Full Blueprint audit — 2026-09-02

Every area below was checked for: migration · service · routes · tests. Where a
status is PARTIAL or worse, the exact missing piece is named. **Nothing is
COMPLETE on the strength of a table or route existing.**

Evidence notation: `svc` = exported `Service` methods, `routes` = live route
declarations, `tests` = functional tests (isolation-only coverage is called out).

| # | Area | Status | Evidence / exact gap |
|---|---|---|---|
| A | Platform & multi-tenancy | **COMPLETE** | `0001`–`0008`, RLS FORCE, `TestCrossTenantRead/Write`, `TestPlatformAdminHasNoBusinessDataAccess`, `TestConnectionCannotBypassRowLevelSecurity` |
| B1 | Catalog & variants | **COMPLETE** | `catalog` 10 svc · 7 routes · 8 pkg + 21 api tests (`catalog_test`, `matrix_test`) |
| B1b | **Bundles / kits** | **COMPLETE 2026-09-03** | `0108` adds `variant.is_bundle` and `bundle_component`, with a trigger refusing components on an ordinary product and refusing a bundle inside a bundle. Selling a kit issues and costs its components; a kit short of one component is refused; an empty kit cannot be sold. 7 tests including concurrent sales sharing a component |
| B2 | Variant matrix | **COMPLETE** | `matrix_test.go` 5 tests; regeneration adds only what is missing |
| B3 | Barcode & Label Studio | **COMPLETE — manual half now tested 2026-09-03** | The bulk generator had 3 tests; the **manual override had none**, leaving the half of B3 that meets the outside world unproven. 2 added: a hand-assigned manufacturer EAN is what the till scans, and one code cannot be given to two products — refused as a correctable mistake rather than a 500 |
| B4 | Inventory core | **COMPLETE** | `inventory`+`stockops` 21 svc · 23 routes · 46 pkg + 28 api tests. FIFO/WAC/standard, landed cost (`0034`), negative-stock policy, transfers with in-transit status, counts, adjustments, **GL tie-out exact**, concurrency tests |
| B4a | **Batch / Lot / Expiry** | **CORE COMPLETE 2026-09-03** | `0107`: `variant.tracks_batches` (B1's flag), `stock_batch` (lot no, mfg/expiry date, qty, supplier, cost, recall), `stock_batch_movement` (the per-movement split, which is what makes a recall answerable). `inventory` receives into a lot, issues **FEFO** — earliest expiry first, undated last, recalled lots skipped — and returns go back into **the lot they left in**, never the soonest-expiring, because they are the same physical units. 8 tests. **Costing is provably unchanged**: `TestTurningOnBatchTrackingDoesNotChangeTheCostOfASale` runs the same sale in a tracked and an untracked shop and requires the same cost, so no company silently becomes specific-identification costed and C13's tie-out still holds. **Alerts:** `jobs.BatchExpirySweeper` (`stock.batch_expiry_sweep`), daily per tenant, warns 30 days out and raises a *critical* for a lot already past its date — two facts needing two messages, because one can still be sold and the other has to come off the shelf. Subject is the BATCH, so a shop with three lots of one item is sent to the right one. **Routes:** `GET /stock/batches` (soonest-expiring first, `expiring_within_days` filter, server-computed `days_left` so a till in another timezone cannot disagree about expiry) and `POST /stock/batches/{id}/recall` behind a new `inventory.recall_batch` verb, which withdraws the lot and **returns the customers who bought from it**. **Receiving:** GRN lines carry `batch_no` / `manufactured_on` / `expires_on` through to `inventory.Receive`, so a goods-in clerk is told at the loading bay rather than when the stock will not sell. The 30-day horizon is an operational default, deliberately NOT in the regulatory registry: that holds dated legal values carrying evidence, and "warn me a month out" is neither dated nor law |
| B4b | Minimum-stock alert engine | **COMPLETE 2026-09-03** | `jobs.LowStockSweeper` (`stock.low_sweep`), scheduled daily per tenant with a date dedupe key so a shop low for a week is told once a day. Announces `notify.KindLowStock` per variant with the variant as `subject_id`, so tapping it reaches the product. **Uses the dashboard's exact query** — summed across warehouses, `qty > 0` — because two places disagreeing about "low" is worse than either answer. 5 tests incl. tenant isolation and out-of-stock exclusion |
| B5 | Purchasing / procurement | **COMPLETE** | `purchasing` 32 svc · 31 routes · 9 pkg + 57 api tests: PO, partial receiving, GRNI (`0034`), bills, **three-way match**, payments + reversal, returns, supplier balances |
| B5.1 | RFQ & supplier comparison | **COMPLETE** | `0087`, 11 api tests (`sourcing_test`) |
| B6 | Suppliers | **COMPLETE** | covered by purchasing suite incl. `supplier_edit_test` |
| B7 | POS backend | **COMPLETE** | counters, session binding, shifts, tenders, returns, exchanges, X/Z, concurrency — see §1 |
| B8 | Hardware integration | **N/A (frontend)** | Printer/drawer/scanner are client concerns; `I5` per-terminal config partly in `device.printer_config` |
| B9 | Promotions & pricing | **COMPLETE 2026-09-03** | `Redeem` is now called on every finalised sale AND enforces the caps itself. `Quote` filtered on `max_uses` by counting redemptions, which is right for showing a cashier what applies and is no control at all: two tills quoting the same last-use coupon both saw it available and both redeemed it. The campaign row is now locked `FOR UPDATE` before its redemptions are counted, the same shape as the credit limit. 9 tests incl. **8 concurrent tills redeeming a one-use coupon exactly once**, per-customer caps, and another company’s campaign refused |
| B10 | Returns / exchange | **COMPLETE** | `sales/refund.go`, `exchange.go`, C14 effects, `exchange_test` 9 tests |
| B11 | Quotation → Order → Delivery | **COMPLETE 2026-09-03 — the last step was missing entirely** | See the *Order invoicing* section below |
| B12 | **Wholesale / B2B** | **COMPLETE 2026-09-03** | The one open bullet was bulk-quantity discounts, which depended on B9: promotions were quote-only, so a quantity break was expressible and never redeemed. `Redeem` is now called on every finalised sale and enforces its own caps. The other five bullets were already complete |
| B12a | **`price_dealer` has no owner** | **RESOLVED 2026-09-03 — was never a specification gap** | B12 line 412 names the tier a customer type resolves to as the **"Dealer/Wholesale pricing tier"** — one tier written with both its names, and there is no dealer or corporate CUSTOMER type anywhere in the Blueprint. So `coalesce(price_dealer, price_wholesale, price_retail)`, plus `PUT /catalog/variants/{id}/prices`, which did not exist. 9 tests |
| B13 | Online orders | **COMPLETE (backend) — re-verified 2026-09-03** | Re-checked bullet by bullet against `0088`. **Stock reservation exists and works**: `stock_reservation` is a SIGNED ledger (positive holds, negative releases) with `held`/`released`/`consumed` reasons and an `expires_at` so an abandoned basket cannot hold the last unit for ever, exposed as `POST/DELETE /stock/reservations` and `GET /stock/availability` (`on_hand` vs `available_to_sell`), and pinned by `TestReservedStockCannotBeSoldTwice`. A reservation deliberately writes no stock movement, so the C13 tie-out is unaffected. Channels are `store/wholesale/online/phone/marketplace`; the delivery pipeline is B13’s exactly (pending → assigned → picked_up → out_for_delivery → delivered → failed → returned) with driver, address, fee and COD. **The one thing absent is a PUBLIC anonymous storefront checkout API** — a signed-in customer orders through the portal and staff record phone and marketplace orders through the authenticated route. That is the web front end the Blueprint calls "own website, PWA storefront", and is out of scope for the backend pass |
| B14 | EMI / instalments | **COMPLETE** | `0088`, 7 routes, `aftersales_test` |
| B15 | Warranty / serial / service | **COMPLETE** | `0088`, serials + service-jobs routes, `TestASerialCarriesItsWarrantyFromTheSale` |
| B16 | CRM & loyalty | **COMPLETE** | `loyalty` + `wallet`, gift cards, store credit, 13 api tests (`crm_test`) |
| C1 | Core accounting | **COMPLETE** | `accounting` + `0015`/`0022`/`0025`, 47 pkg + 26 api tests; balanced-entry CONSTRAINT TRIGGER, immutability, period lock, gapless numbering under concurrency |
| C2 | Cash & bank | **COMPLETE** | `treasury` 11 svc · 10 routes · 10 tests |
| C3 | Expenses & investment | **COMPLETE** | `expenses` 9 svc · 8 routes · 13 tests; investors 4 routes |
| C4 | AR / AP | **COMPLETE** | `receivables` 15 svc · 15 routes · **42 api tests** incl. receipt reversal, ageing, credit standing |
| C5/C6 | HR & payroll | **COMPLETE 2026-09-03** | GOSI rates and the WPS wage-file layout are now recorded from the authorities’ own documents — see the *Saudi payroll* section below. Directory, ID expiry, attendance, leave, advances, payslips, commission and the market gate were already complete |
| C7 | Fixed assets | **COMPLETE** | `assets` 10 svc · 4 routes · 10 tests, depreciation |
| C8 | Shift & drawer | **COMPLETE** | `shift`, blind close, X/Z, `drawer_derivation_test`, `shift_close_race_test` |
| C9 | Double-entry engine | **COMPLETE** | all 12 posting rules as data, resolved at transaction date |
| C10 | Fiscal period / year-end | **COMPLETE** | `fiscal` 5 svc · 14 tests (`yearend_test`, `fiscal_test`) |
| C11 | Bank reconciliation | **COMPLETE** | `treasury`, `treasury_test` |
| C12 | Settlement & gateways | **COMPLETE** | `settlement`+`payments` 11 svc · 13 routes · 6 pkg + 35 api tests |
| C13 | Costing & COGS | **COMPLETE** | tie-out exact incl. negative-stock correction (`0047`/`0048`) |
| C14 | Accounting-aware returns | **COMPLETE — 9 of 9, 2026-09-03** | Effect 7 was the last one and was blocked on something deeper than reported: commission was not merely un-reversed, it **500’d the payroll run**. Fixed — see the *Sales commission* section. A return now reverses inventory, revenue, output tax, COGS, the refund, loyalty points and commission, links the credit note and writes the journal and audit record |
| D1 | Reporting suite | **COMPLETE** | `reports` 17 svc · 9 routes · 7 tests; TB, P&L, BS, cash flow |
| D2 | Analytics & forecasting | **COMPLETE 2026-09-03** | `insight` 5 svc · 4 routes · **2 tests added**. All four analytics reads (kpis, movers, forecast, profitability) answer for a shop with a sale on the books, and all four take `report.view` |
| D3 | Notifications | **COMPLETE** | `notify` 8 svc · 7 routes; 3 functional tests (preferences, in-app cannot be silenced, announcement reaches inbox) |
| D4 | Audit trail | **COMPLETE** | append-only, `TestAuditLogIsAppendOnly`, `audit.Write` with `actor_label` |
| D5 | Approval centre | **COMPLETE** | `workflow` 12 svc · 10 routes, wired into `expenses.Record` and `purchasing.IssueOrder`. **A P0 defect that made the whole module unusable was found and fixed 2026-09-03** — see below. **10 end-to-end tests**: decision path (6) plus escalation on an elapsed deadline, no escalation inside it, an escalated request still decidable, and delegation refusing backwards cover |
| D6 | Document management | **COMPLETE 2026-09-03** | `docs` 7 svc · 4 routes · **2 tests** (was isolation-only). Register lists, filing takes `document.manage`, and a cross-tenant read returns no rows |
| D7 | Global search | **COMPLETE 2026-09-03** | `insight/search.go` · **3 tests**. Finds the fixture’s own product by name, answers empty for a miss rather than failing, and does not return another tenant’s catalogue |
| E1 | ZATCA | **DEFERRED — PRODUCTION PHASE** | Not blocked, not blocking. Untouched this pass; no other market depends on it |
| E2 | Saudi tax engine | **COMPLETE** | multi-market treatments; rates now per treatment |
| E3 | Payment methods | **COMPLETE** | gateways, providers, attempts, settlement |
| E4 | PDPL / privacy | **COMPLETE 2026-09-03** | `privacy` 29 svc · 25 routes · **6 functional tests** (was isolation-only). Consent recorded → listed → withdrawn with `withdrawn_at` stamped; DSR opened → closed and dropping out of the `?open=true` list; breach incident logged and registered; ROPA read; permission enforced on all four registers. No defect found |
| E7 | Compliance dashboard | **COMPLETE 2026-09-03** | `compliance` 1 svc · 1 route · **5 tests** (was 0). All eight aggregations assemble for a Saudi shop and for a Bangladeshi one, an un-onboarded shop reads as not-started rather than clear, scoping and permission hold. No defect found — the module was sound and simply unproven |
| E8 | Regulatory registry | **COMPLETE** | dated values, evidence required, market-aware boot + provisioning gates, 9 tests |
| F1 | Workflow engine | see D5 | |
| F2/F3 | Customer & supplier portals | **COMPLETE 2026-09-03** | `portal` 26 svc · 24 routes · **10 tests** (was 4, all of them door-locked checks). Now proves there is something behind the door: a customer sees their own invoice, a customer who bought nothing sees none, every read answers, an address saves and reads back, sign-out ends the session. A near-miss worth recording — the first draft of the isolation test read the wrong response key and would have passed no matter what the portal returned; the positive control is what caught it |
| F4 | Group consolidation | **COMPLETE 2026-09-03** | `group` 11 svc · 10 routes · **4 tests** (was isolation-only). Group created → member added → read back; consolidated statement and intercompany view both answer; cross-tenant refused; creating one takes `group.manage`. Membership is dated, so a statement for a period before a company joined correctly finds nothing to consolidate |
| G1 | Country configuration | **COMPLETE** | `tenant.market`, onboarding constraint, `internal/market` |
| G2 | Multi-currency | **COMPLETE (realised) 2026-09-03; unrealised revaluation deliberately not attempted** | See the *Multi-currency* section below |
| G4 | Tax templates / US jurisdiction tax | **COMPLETE (engine), DATA IS AN OPERATIONS TASK 2026-09-03** | See the *US sales tax* section below. |
| H1 | Security & auth | **COMPLETE** | argon2id, refresh rotation with reuse detection, MFA, httpOnly cookie, lockout |
| H2 | Offline sync | **COMPLETE** | `sync` + `replay.go`, M3 gate, 8 pkg + 12 api tests |
| H3 | Device management | **COMPLETE** | 10 routes, 26 tests, binding model (`0104`) |
| H4 | Backup & restore | **COMPLETE** | `ops` 14 svc · 5 routes; `TestABackupThatRanIsNotABackupThatRestores` |
| H5 | Subscription & billing | **COMPLETE (entitlement) 2026-09-03** | **The gate is enforced.** `featureOfRoute` maps 28 route families to the 14 gateable modules `plan_feature` sells; `requireFeature` middleware refuses with **402 Payment Required** — deliberately not 403, because the caller holds the permission and what is missing is commercial, and the two have different remedies. Wrapped inside the auth middleware so an unauthenticated caller gets 401 rather than being told what a plan contains. 9 gate tests: starter refused, business allowed, core product never gated, tenant grant opens, expired grant closes, withdrawn module closes on a tier that includes it, no cross-tenant leak, Super Admin ungated, and a guard that every gated feature is one the plans actually sell. **`wholesale` and `multi_company` are deliberately not route-gated**: wholesale is a customer type and price tier with no endpoint of its own, and multi-company is a CEILING already enforced by `tenant_limit.max_companies` — gating a route would refuse the FIRST company on every plan. Test fixtures moved to `enterprise`, since a fixture exists to test its module and not the subscription in front of it. Invoices and dunning remain untested |
| H5-old | (superseded note) | | **9 tests added 2026-09-02** covering entitlement resolution: plan tier decides, tenant override beats it in both directions, expired override falls back, unknown feature fails closed, no cross-tenant leak, Super-Admin-only mutation. **The resolver is correct and NOTHING CALLS IT** — see the critical finding below. Invoices and dunning remain untested |
| H6 | API & integrations | **COMPLETE** | API keys (3 tests), webhooks (2 tests), `jobs/webhooks.go` dispatch with retry. Delivery not tested end-to-end |
| H7 | Import / export | **COMPLETE** | `portability` 7 svc · 8 routes · 5 functional tests (stage→check→commit, refusal reasons, duplicate detection) |
| H8 | Health monitoring | **COMPLETE** | `platformops`, 18 api tests |
| H9 | Job queue | **COMPLETE** | `jobs` 9 files, retry, per-terminal ordering, escalation, reaping, 6 pkg + 12 tests |
| H10 | Support ticketing | **COMPLETE** | 5 routes, `TestReplyingToATicketPutsItBackOnSupport` |
| I2 | Receipt templates | **COMPLETE** | `branding` 9 svc, `template_test` + `branding_test` 12 tests |
| I3 | Numbering engine | **COMPLETE** | per-document series, gapless under concurrency, `TestACreditNoteHasItsOwnNumberSeries` |
| N | Super Admin control plane | **COMPLETE** | 15 routes, tenant creation + market, plans, features, invoices, dunning, sub-processors |
| Q | RBAC & isolation | **COMPLETE** | 426 routes access-declared; route-authz, company-confinement and cross-tenant walks |

### B12 Wholesale / B2B — bullet-by-bullet re-verification

The first audit pass judged this area partly from route counts and got two
things wrong in opposite directions. Re-verified against the code:

| B12 requirement | Status | Evidence |
|---|---|---|
| Wholesale customer type with a **pricing tier** | **COMPLETE** | `customer.customer_type` → `variant.price_wholesale` in `catalog.FindByBarcode`; 5 tests in `tier_pricing_test.go` incl. retail unchanged, null fallback, cross-company refusal. Fixed 2026-09-02 (`0f8c7da`) |
| **Minimum order quantity** rules | **COMPLETE** | `orders.checkWholesaleMinimums` enforces `variant.min_wholesale_qty` where `sales_order.channel = 'wholesale'`; `TestAWholesaleOrderBelowTheMinimumCannotBeConfirmed`, `TestARetailOrderIgnoresTheWholesaleMinimum`. **The first audit wrongly called this missing** |
| **Bulk-quantity discounts** | **PARTIAL** | Expressible: `0084` carries `bundle_price` ("flat price for `buy_qty` of them"), `buy_x_get_y`, `min_purchase`, and `customer_type` targeting. But promotions are **quote-only** — see the B9 defect below |
| **Credit limit** per wholesale client | **COMPLETE** | `customer.credit_limit`, read `FOR UPDATE` in `receivables/collecting.go` so it holds under concurrency; 12 tests in `credit_sale_test.go` |
| Wholesale workflows **kept separate so retail reporting is not distorted** | **COMPLETE 2026-09-03** | `reports.SalesFor` now returns `retail_total` / `wholesale_total` and their counts, derived from `customer.customer_type` through `sales_invoice.customer_id` — the fact already exists, so no second copy on every invoice to keep in step. A walk-in has no customer and counts as retail, the same answer the pricing tier gives. 3 tests |
| **Wholesale customer ledger** | **COMPLETE** | `receivables.LedgerFor`, ageing, receipts, reversal — 42 api tests |

**Also unreconciled:** wholesale is signalled two different ways —
`customer.customer_type` drives pricing, `sales_order.channel` drives MOQ — and
nothing connects them. A customer marked `wholesale` can place a `store`-channel
order and skip the minimums.

### 🔴🔴 F1 defect found while writing the approval tests — FIXED 2026-09-03

**The approval workflow was completely unusable, and looked like it worked.**

`workflow.Evaluate` inserted the approval request using the CALLER'S
transaction. Every caller then returned an error to refuse the work — which
rolled the insert back with it. So an expense over the threshold was refused
with *"it is waiting in the approval centre"*, the approval centre returned
`{"data":[]}`, and **no request existed in any status**. The person was told to
wait for something that had never been written and could never arrive. Same for
a purchase order over its limit.

Nothing caught it because only rule CRUD had tests; the decision path had none.

**Fix:** the refusal and the request are two facts with two lifetimes — the work
must NOT commit, the request MUST. `Evaluate` now assesses inside the caller's
transaction and returns a `workflow.Pending`; the caller raises it through
`workflow.Raise` **after** its transaction has rolled back. Applied at both wired
call sites, `expenses.Record` and `purchasing.IssueOrder`.

6 tests: threshold held / not held, approve then refuse-to-decide-twice
(`FOR UPDATE` under concurrency), refusal needs a reason, deciding needs more
than the permission to look, and no cross-company decisions.

### ~~🔴 B9 defect — promotions never redeem~~ FIXED 2026-09-03

`sales.Finalize` now records redemptions in the sale's own transaction. A line
carries `promotion_id`; the offline queue carries it too, so a replayed sale
spends a campaign's budget against its limit like an online one. 4 tests,
including **a one-per-customer coupon that stops after the first sale** — the
assertion the whole change exists for.

Note `0084` constrains usage caps to coupon promotions: "an automatic promotion
is not redeemed, it just applies". Redemptions are still recorded for automatic
campaigns, because campaign COST is a separate question from usage limits.

### The original finding, for context — promotions never redeem

`promotions.Redeem` writes `promotion_redemption` and its own comment says it is
"called inside the transaction that finalises the sale". **Nothing calls it.**
`sales.Finalize` contains no promotion code at all; the only reachable promotion
entry point is `POST /api/v1/promotions/quote`, which the till asks and then
applies as an ordinary discount.

Two consequences, both silent:

1. **Usage limits never bite.** `Quote` enforces `max_uses` and
   `max_uses_per_customer` by counting rows in `promotion_redemption`
   (`promotions.go:385-390`). That count is permanently zero, so a coupon
   limited to one use per customer can be used without limit.
2. **Campaign cost is permanently zero.** `manage.List` reports redemption count
   and total discount from the same empty table, so nobody can see what a
   campaign cost.

Same shape as the entitlement gap: a control that looks implemented, is
correct in isolation, and is wired to nothing. **P0** — it is a financial
control, not a reporting nicety. Not fixed in this pass; scope was the B12
re-verification.

### Counts

**COMPLETE 40 · PARTIAL 16 · MISSING 2 · BLOCKED 3 · DEFERRED 1**

### Critical findings from this audit

1. ~~**`variant.price_dealer` is dead.**~~ **HALF FIXED 2026-09-02.** Tier
   pricing now applies: a `wholesale` customer is charged `price_wholesale`,
   with a documented fallback to retail when none is set, and the join is scoped
   to the variant's own company so another company's customer cannot reprice a
   scan. **`price_dealer` remains unread** because no customer type in B16
   selects it — a specification gap for the owner, not a coding one.
2. **🔴 Feature entitlement is resolved and never enforced.** `billing.Allows`
   is written to be asked "on the request path in front of a handler" — its own
   comment says so — and **the only caller in the repository is its test**. No
   middleware, no handler, no service refuses anything on the strength of it.
   `Entitlements` merely *reports*.

   The effect: **plan tiers gate nothing**. A `starter` tenant, whose plan sets
   `payroll`, `loyalty`, `api_access`, `webhooks`, `analytics`, `approvals`,
   `wholesale`, `online_orders`, `installments`, `warranty`, `assets`,
   `multi_company` and `consolidation` to false, reaches every one of those
   modules. `TestEntitlementIsResolvedButNotYetEnforced` proves it against the
   live payroll route and is written to **fail the moment somebody wires the
   gate**, so the hole cannot be closed silently or forgotten.

   This is access control, not billing: the tier decides what a tenant may DO.
   Plan *ceilings* (`tenant_limit`: max companies, stores, users, terminals) are
   a separate mechanism and ARE enforced — `CommitBusinessInfo` checks
   `max_companies` — which is likely why the gap went unnoticed.
3. **The approval engine's decision path is untested** even though it is wired
   into expenses and purchasing approvals.
4. **Batch/Lot/Expiry is absent entirely** — required by B4 for
   cosmetics/grocery inventory, with recall alerts.
5. **Privacy/PDPL (29 svc) and the portals (26 svc) have almost no functional
   tests** relative to their size and external exposure.

## 2. What is NOT done

### ✅ CLOSED — the production boot gate is market-aware

**Was:** `reportRegistryHealth` refused a production start while *any*
release-blocker was unverified, and all three unverified ones are Saudi HR rules
(`SA.EOSB.ENTITLEMENT`, `SA.GOSI.RATES`, `SA.WPS.WAGE_FILE_FORMAT`). A
Bangladesh-only deployment was fixed at the till and still could not boot.

**Now:** `registry.Health` reads the markets this deployment actually serves
from `tenant.market` (0103) on the platform plane, and splits the blockers:
`BlockingRelease` (served markets — still refuses, unchanged) and
`DeferredBlockers` (markets nobody here trades in — reported by name, never
blocking). No Saudi rule was marked verified; all three are still unverified and
still visible.

**Why relaxing the boot gate is safe:** it was never the thing standing between
a placeholder and a tax return. `registry.gate()` refuses **every** unverified
rule at the point of use whenever `requireVerified` is set, so a Saudi payroll
run fails on `SA.GOSI.RATES` regardless of what happened at startup. The boot
check is an early, loud warning.

**Read from data, not config:** a deployment's markets are a fact about the
tenants it holds. A config flag would be a hand-maintained second copy whose
failure mode is silent in the worst direction — onboard a Saudi client, forget
the flag, keep booting on placeholders.

**Known limitation (discovered, not fixed — out of this pass's scope):** the
gate runs at boot only, so a market can become served *after* the process
starts. Provisioning a Saudi tenant onto a running Bangladesh deployment does
not re-run it. The per-use `gate()` still refuses, and the next restart blocks.
Closing it properly means refusing to provision a tenant into a market with
unverified release-blockers — a provisioning-time check, and a separate task.

### ✅ CLOSED — provisioning-time market gate

The boot gate's companion. `provisioning.CreateTenant` now refuses to create a
business in a market whose release-blocking legal values are unverified, before
anything is written, wherever the deployment requires verification
(`registry.RequiresVerification()` — the same flag `gate()` uses). A development
machine still creates tenants in any market; a production one may not take on a
Saudi client while GOSI, EOSB and WPS are placeholders. There is deliberately no
override flag: one would be used.

Closes the window this file recorded last pass — a Bangladesh deployment being
handed a Saudi client at 10:00 and serving it on placeholders until somebody
restarted.

### ✅ PARTLY CLOSED — tax is resolved per treatment, not per country

**The `reduced`-rate defect is fixed.** `SaleInput.TaxRate` (one decimal) became
`SaleInput.TaxRates` (a rate per treatment). `taxable()` accepted `reduced`
while only one rate existed, so a reduced-rate line was charged the **standard**
rate — silently overcharging the customer and overstating the return.
`resolveRates` now resolves a rate for each treatment the sale actually uses,
and a taxable treatment with no rate on file **refuses the sale** rather than
defaulting. Non-taxable treatments need no rate and are answered from the
treatment list.

**Jurisdiction architecture exists (0106), with no rates.** `tax_jurisdiction`
(country → state → county → city → district, `parent_id` chain) and
`tax_jurisdiction_rate` (per treatment, dated, `source_authority` /
`source_document` / `verified_on` / `verified_by`, GiST exclusion against
overlapping ranges). `registry.JurisdictionRate` walks a jurisdiction to its
root and sums the shares in force on the date, returning the parts as well as
the total because remittance is per authority.

**Not one rate is seeded, and a test enforces that.** Every rate is a legal
value that must arrive with evidence.

### 🔴 BLOCKED — highest priority

**US / International cannot sell.** The architecture is now in place and the
DATA is not: no US jurisdiction or rate has been verified against any state or
city authority, and none may be invented. What is still missing in CODE is the
link from a sale to a jurisdiction — see §3. There is no `US.VAT.STANDARD_RATE`, and
there should not be one: US sales tax is set per state, county and city, so
resolution needs a jurisdiction rather than a country. `US.SALESTAX.TAX_TREATMENTS`
is seeded; the rate is not. A US company provisions, sets up, and then fails at
the counter.

### 🟠 PARTIAL

- **Saudi-only rule keys outside the sale path.** `people` (GOSI/WPS/EOSB),
  `privacy` (PDPL) and `compliance` (`SA.VAT.FILING_DUE_RULE`,
  `SA.VAT.RECORD_RETENTION`, `SA.ECOMMERCE.COOLING_OFF_DAYS`) resolve `SA.` keys
  unconditionally. Phase 3/5 modules — they error for a BD tenant rather than
  degrade.
- **`reduced` is charged at the standard rate.** `sales.taxable` accepts the
  treatment while `SaleInput` carries a single rate. Only Bangladesh lists it.
  Silently overcharges; documented in the function's own comment.
- **17 of 42 registry rules unverified, 7 payloads still `__VERIFY__`.**
  `BD.VAT.STANDARD_RATE` is deliberately among them (0105, NBR named).
- **Web POS is online-only** — no offline queue, no device pairing in a browser.
  Owner's decision on 2026-09-02; offline custody is a separate later call.

### ⬜ MISSING

- Front end for the web POS counter. **Do not start the frontend redesign yet.**
- Offline sync path for browser counters.

---

## 2b. Recommended implementation order (from the 1b audit)

Ordered by risk, not by size. Everything here is backend; the frontend is still
out of scope.

**P0 — correctness and data integrity**
1. ~~**Wholesale tier pricing**~~ **DONE 2026-09-02.** Awaiting the owner's
   decision on who pays `price_dealer` before that column is either wired or
   dropped.
2. ~~**Tests for billing/feature entitlement**~~ **DONE 2026-09-02** (9 tests),
   and it uncovered a bigger problem: **enforce the entitlement gate.**
   `billing.Allows` is correct and unreachable, so plan tiers currently gate
   nothing. The work is a middleware or route-level check mapping each H5
   feature to the routes it covers, plus a refusal that names the plan. Deleting
   `TestEntitlementIsResolvedButNotYetEnforced` is part of the task.
3. ~~**Call `promotions.Redeem` from `sales.Finalize`**~~ **DONE 2026-09-03**, 4
   tests, online and offline paths.
4. ~~**Tests for the approval decision path**~~ **DONE 2026-09-03**, 6 tests —
   and they found F1 was unusable end to end. Fixed. `Escalate` and `Delegate`
   remain untested.

**P1 — core business functionality**
4. **Wire a sale to its tax jurisdiction** (see §3 — still the top single task).
5. **Batch / Lot / Expiry tracking** (B4), including recall alerts.
6. **Minimum-stock alert engine** (B4) — the reorder level is already stored.
7. **Market-aware HR/privacy/compliance rule keys** so a non-Saudi tenant
   degrades instead of erroring.

**P2 — remaining Blueprint backend features**
8. Wholesale MOQ and bulk-discount tiers (B12).
9. Bundles / kits (B1).
10. Loyalty and commission reversal — C14 effects 6 and 7 (P13).
11. Functional tests for privacy/PDPL, portals, group consolidation, documents.

**P3 — utilities**
12. Promotions end-to-end test through a POS sale.
13. Analytics and global-search test coverage.
14. FX revaluation and a rate source (G2).

## 3. The single highest-priority next backend task

**Wire a sale to its tax jurisdiction.**

The jurisdiction model and its resolution exist (0106,
`registry.JurisdictionRate`, 5 tests). What does not exist is the link from a
sale to a jurisdiction, so nothing calls it yet:

1. **`store.tax_jurisdiction_id`** — nullable, referencing `tax_jurisdiction`.
   Deliberately not added in 0106 because nothing read it, and a column nothing
   reads drifts. Required only where the market taxes by jurisdiction.
2. **`resolveRates` gains the jurisdiction branch** — where
   `rateKeyFor(country, treatment)` returns false (today: the US `taxable`
   treatment), resolve through `JurisdictionRate` using the store's jurisdiction
   at the transaction date instead of refusing.
3. **Sourcing is a real decision, not a default.** Origin- vs
   destination-based changes WHICH jurisdiction applies — the shop's or the
   customer's. `tax_jurisdiction.is_origin_based` records the fact and nothing
   reads it. Do not pick one silently.
4. **A CRUD surface for jurisdictions and rates**, Super-Admin scoped, so a
   verified rate can be entered with its source. Without it the tables can only
   be filled by SQL.
5. **Store the shares on the invoice line.** `sales_invoice_line.tax_rate` holds
   the combined rate today; a US filing needs the per-authority breakdown, and
   reconstructing it later from rates that have since changed is not possible.

**External information still required:** every US rate, per jurisdiction, from
the relevant state/county/city authority — plus nexus rules (whether the seller
must collect at all) and exemption-certificate handling. None of it may be
inferred.

Then: the `reduced`-rate registry keys (`{CC}.VAT.REDUCED_RATE`) for any market
that charges one — Bangladesh does, and the value is unknown; a reduced-rate
line is refused until the NBR figure is recorded.

<details><summary>Superseded plan (kept for context)</summary>

**US / International tax resolution — make it jurisdiction-based.**

This is now the only thing keeping a market from trading. `US.SALESTAX.TAX_TREATMENTS`
is seeded; there is no rate, and there must not be a national one: US sales tax
is set per state, county and city, sourcing may be origin- or destination-based,
and exemption is held per customer as a certificate. A US company today
provisions, completes setup, and fails at the counter.

The shape this needs, before any code:

1. **A jurisdiction on the sale.** `registry.VATRate(country, asOf)` cannot
   answer a US question — rate resolution needs a jurisdiction resolved from the
   transaction (store address for origin-based, customer address for
   destination-based). That argument does not exist in `applyTaxProfile` today.
2. **A rate model that is not one number.** `SaleInput.TaxRate` is a single
   decimal. US needs a combined rate assembled from overlapping jurisdictions,
   and the same change would fix the `reduced`-rate defect below.
3. **Do not invent the rates.** They are legal values: registry rows with
   sources and verification, never constants in Go. Seeding a plausible national
   US rate would be exactly the thing Part N forbids.

Because (2) is shared, doing this also closes the `reduced`-treatment defect —
`sales.taxable` accepts `reduced` while only one rate exists, so a Bangladeshi
reduced-rate line is silently overcharged at the standard rate.

After that: the provisioning-time market gate noted above, then the web POS
front end.

</details>

---

## 4. State of the tests

- `go build ./...` · `go vet ./...` — clean.
- **`go test -count=1 -tags=integration ./...` — GREEN.** 21 packages, zero
  failures, confirmed 2026-09-02 after the counter model landed. `internal/api`
  ran 944s and `platform/db` 625s, so this is real database work and not a suite
  that skipped.
- That run followed 9 regressions this session caused and fixed: 8 from
  `registerTill` asserting a new terminal is `pending` (since 0104 a counter with
  no binding is `session`-bound and therefore active immediately — those tests
  are about the PAIRING lifecycle, so the helper now registers
  `"binding": "paired"`), and 1 from a `POST /platform/tenants` call with no
  `market`.
- Front end: 667 tests pass (`shared` 482, `pos` 185), `tsc --noEmit` clean.

**Lesson worth keeping:** the 5 new counter tests proved the new behaviour and
could not prove old behaviour was intact. Only the full run does that, so it
belongs inside the change rather than after it.

**Running the backend suite properly:** load the DSN first —
`cd backend && set -a && . ./.env && set +a` — or every database test skips and
still prints `ok`.

---

## 5. Session log

**2026-09-02 (later)** — made the production boot gate market-aware.
`registry.Health` now reads served markets from `tenant.market` on the platform
plane and splits `BlockingRelease` from `DeferredBlockers`; `cmd/api` names the
deferred rules and the served markets in its log and in the refusal. Five new
integration tests in `internal/registry/health_test.go` cover Bangladesh-only,
Saudi-only, mixed, no-tenants, and that the market set is genuinely read from
tenant data through the platform plane. **No Saudi rule was marked verified.**

**2026-09-02** — verification pass under the international directive; `tenant.market`
chosen by the platform operator (0103) with the create-business UI that had no
caller; POS pure logic moved to `shared/src/pos`; the counter model (0104), ZATCA
decoupled from selling via `internal/market`, and the country-derived VAT-rate key
with Bangladesh seeded (0105). Nothing committed — the working tree carries it all.

---

## US sales tax — production readiness (G1 / G4)

**Status: the engine is complete and safe. The rate DATA is an operations task,
not a code gap, and the product refuses to sell rather than guess at it.**

### What a US sale does now

`store.tax_jurisdiction_id` (0109) places a shop — on the STORE, because a chain
is taxed differently in each city. `sales.resolveRates` asks the registry for a
national rate first; where the market has none (the US), it falls through to
`registry.JurisdictionRate`, which walks the shop's jurisdiction to its country
root and sums each authority's share in force **on the invoice date**.

Each authority's share is then written to `sales_invoice_tax_share` (0111). A
shop files a return with the state and another with the city, each for its own
share, and `sales_invoice.tax_total` alone cannot answer either. The breakdown
is stored at the time of sale rather than recomputed at filing time, because
rerunning last quarter's invoices through today's rates would reapportion tax
that was already charged and collected under the old ones.

The shares are apportioned proportionally and sum to the invoice tax exactly.
The leftover penny goes to the authority with the **largest rate**, not to the
last part as this codebase's usual rounding-remainder rule would have it: the
walk ends at the country root, which in the US levies zero, so the ordinary rule
would file a stray penny — negative, in the tested case — with an authority that
charges nothing and is owed nothing.

### The undercharge guarantee

A US sale cannot be priced on incomplete data. Four refusals, each tested:

| Situation | What happens |
|---|---|
| Shop has no jurisdiction set | Refused, naming the missing setup step |
| Jurisdiction has no rates at all | Refused — a zero rate is a legal claim nobody made |
| **An authority in the chain has no rate on file** | **Refused, naming that authority** |
| Any share is unverified (production gate on) | Refused, naming the authority |

The third was a real defect found and fixed during this pass. The chain walk
skipped an authority with no rate row, so a shop whose city rate was loaded and
whose state rate was not would have sold all day at the city's 2% — printed on
the receipt as the tax due, posted to the tax account, and under-remitted to the
state, with nothing anywhere looking wrong.

The fix makes **absence and zero different facts**. An authority that genuinely
levies nothing gets an explicit `0.000000` row with its source, which somebody
has to look up and write down. An authority nobody has loaded yet gets a
refusal that names it. 0110 records the one such statement the product ships:
the United States levies no federal sales or use tax.

### Data architecture: RawSyst maintains the datasets. This was NOT a business decision to escalate.

The Blueprint settles it in two places, so no provider choice needed isolating:

* **A4, Super Admin global configuration** — *"manage global list of countries,
  currencies, languages, **tax templates** (so new country configs can be added
  without code changes)"*. The Platform Owner curates tax data as platform data.
* **G4** — *"A growing library of pre-built **tax templates per country**"*.

And decisively, **H6's connector list does not include a tax provider**: payment
gateways, SMS, email, WhatsApp, shipping, external accounting, e-commerce,
banks, card terminals, and ZATCA. Tax is not an integration in this product; it
is configuration the platform owns. The `tax_jurisdiction` / `tax_jurisdiction_rate`
tables already have the shape that requires — `source_authority`,
`source_document`, `source_url`, `verified_on`, `verified_by`, and a GiST
exclusion forbidding overlapping date ranges — which a provider integration
would not need and could not populate.

This also fits the existing provider precedent rather than contradicting it.
0102's payment gateways are *credentials the client types in*, because a shop
has its own acquirer relationship. No shop has its own relationship with a rate
dataset, and per-tenant tax rates would be a correctness hazard, not a feature.

### What ships, and what an operator must do

0109 seeds California's **state share only**, `verified_on` NULL on purpose.
**Official source:** [CDTFA — Sales & Use Tax Rates](https://www.cdtfa.ca.gov/taxes-and-fees/sales-use-tax-rates.htm),
read 2026-09-03: *"The statewide tax rate is 7.25%"*, and on the same page *"In
most areas of California, local jurisdictions have added district taxes ...
those district tax rates range from 0.10% to 2.00%"*, with sellers directed to
look the combined rate up **by address**.

A web search for consolidated state-rate tables returned only Tax Foundation and
similar aggregators, which Part N of the Blueprint classifies as **Tier 2,
orientation only**. They were not used as data. Every rate must come from the
levying authority itself.

Before a US shop can trade, an operator records the district/county/city
jurisdictions for its address with their sources and verifies them. Until then
the sale is refused, loudly and by name.

### Still genuinely undecided (documented, not invented)

**Origin versus destination sourcing.** Some US states tax where a sale
originates and some where it is delivered. `tax_jurisdiction.is_origin_based`
records the fact per authority and nothing reads it yet; the shop's own
jurisdiction is used, which is correct for a customer at the counter and is the
starting point for the delivery case. Wiring delivery addresses into rate
selection needs the sourcing rules per state, and choosing one silently would be
inventing a rule.

### Returns

A credit note is credited against the authorities that were paid on the sale it
corrects, apportioned from **the original invoice's own shares** rather than
resolved afresh — a rate that changed between the sale and the return would
otherwise credit the state for tax the customer never paid it. Without this the
breakdown would have been correct only until somebody brought something back,
and the shop would have remitted tax it had already refunded.

### Tests (16, all passing)

`internal/api/us_tax_test.go` — multi-authority sum; no jurisdiction; unverified
rate; the shipped California row; a Saudi sale unaffected; **an authority with
no rate refuses and is named**; an authority levying zero sells; **a rate change
applies from its effective date** (two sales ten days apart in one accounting
period, 5% then 8%); **one shop's jurisdiction does not tax another's sale**;
**tax reaches the ledger** (output tax credited 8.25, revenue 100); **each
authority's share is recorded and the shares sum to the invoice tax**; **an
authority levying zero is apportioned exactly zero** (6.25% + 1.25% on 55.55,
where the split leaves a penny over); **a full return credits each authority the
6.25 and 2.00 it was paid**; a Saudi sale records no shares.

`internal/registry/jurisdiction_test.go` — the combined-rate walk, an unverified
share, a jurisdiction with no rates, resolution at the transaction date, a
partly loaded chain refused by name, an authority levying zero, and every
shipped rate proven to name a real authority and to be unverified.

---

## Sales commission (C6) and C14 effect 7

**Status: was broken outright, now works end to end. 12 tests.**

### Root cause

`people.commissionFor` attributed a month's takings with:

```sql
JOIN employee e ON e.user_id = i.created_by
```

and **`sales_invoice` has no `created_by` column** — it never has. The query
errored, so `POST /api/v1/payroll` returned **HTTP 500 for any employee marked
`commission_eligible`**. Not a wrong figure: a hard failure, on the payroll run
itself. It went unnoticed because every payroll test hired staff who were not
commission-eligible, so the function was never reached.

Underneath that sat a real gap: **a sale did not record who made it.**
`sales.Sale.CashierID` was populated from the authenticated user on every POS
path and used for the journal's `posted_by`, but `writeInvoice` had nowhere to
put it.

Correcting an earlier note in this document: commission was *not* "never earned
on a sale". `commissionFor` had always computed it at payroll time by
aggregating the period, and `computeSlip` had always called it. The design was
sound; the wiring was broken.

### What changed

`0112_sale_cashier.sql` adds `sales_invoice.cashier_id` (nullable, `ON DELETE
SET NULL`, partial index on `company_id, cashier_id, issued_at`). Not
backfilled: invoices written before it was captured cannot honestly be given an
attribution, so they stay null and a period containing them is short rather
than invented. The same column is what E-reporting's "Employee-wise" sales
report and the cashier dashboard's "today's own sales" will read.

`writeInvoice` and `writeCreditNote` now persist it. The name follows the
Blueprint — A6 calls the role "Cashier / POS Operator" and C6 measures
commission per employee; there is no separate salesperson concept to invent.

`commissionFor` was rewritten to fix four defects, each of which was reachable
the moment the 500 was fixed:

| Defect | Effect before |
|---|---|
| Joined on a column that does not exist | Payroll 500 for any eligible employee |
| No `doc_type` filter | A credit note's positive line amounts were **added** — selling 100 and refunding it in full paid commission on 200 |
| Rule scope read only for ranking | A scheme written for one branch paid on the whole company |
| No `state` filter | `draft` and `cancelled` invoices earned commission |

**A credit note is attributed to whoever made the original sale**, resolved
through `parent_invoice_id`, not to whoever stood at the till for the refund.
C14 effect 7 says to reverse the commission attributed to *the original sale*;
docking the refunding cashier would penalise the wrong person and leave the
seller paid for goods that came back.

Commission is never negative: a month whose returns exceed its sales pays zero,
because taking money off a salary is a deduction nobody has authorised.

`rateFromTiers` is untouched. Its reading — the highest band reached applies to
the whole amount — matches C6's worked example and is now pinned by a test at
the Blueprint's own numbers.

### Base, and C14 coverage

The base is the Blueprint's own: C6 says "total revenue, or profit", which is
`sales_invoice_line.net_amount` and `cogs_amount`. Nothing invented.

**Exchanges need no special case.** `ProcessExchange` is `ProcessReturn` plus
`Finalize` — a credit note and a new sale through the same tables — so signed
netting covers it with no double count. Voids are covered by the state filter.

### Tests (12, all passing) — `internal/api/commission_test.go`

Sale earns at the scheme's rate · **attribution is persisted on the invoice** ·
credit note reverses it · **reversal follows the original seller, not the
refunding cashier** · draft earns nothing · cancelled earns nothing ·
store-scoped scheme does not pay on another store's sales · store-scoped scheme
does pay on its own · profit basis differs from revenue basis · tenant
isolation · **C6's worked example** (400.00 net at 1%, then 60,400.00 net
crossing SAR 50,000 → 1,208.00 at 2% on the whole) · **20 concurrent sales
count exactly 20**.

Payroll integration verified end to end: `computeSlip` → `commissionFor` →
`payslip.commission` → `gross`, through the real `POST /api/v1/payroll` route,
which no longer 500s.

---

## Defects found and fixed while verifying the above

Three failures in the full suite predated this pass and had been hidden by a
truncated log tail:

* **`stockops/batches.go` selected `s.name` from `supplier`**, whose column is
  `legal_name`. `GET /api/v1/stock/batches` had never worked — it 500'd on
  every call. Fixed.
* **Two batch-lifecycle tests tendered 115.00 for a sale of two at 115.00.**
  The tests were wrong, not the product. Fixed to 230.00.
* **An unknown item was refused with "That product was not found."** The bundle
  lookup added in the kits pass runs first on the sale path and had taken over
  the refusal from the stock layer, whose wording tells a cashier what to do.
  Both lookups now say the item is not in this company's catalogue and to check
  the barcode or add it.

One failure was caused by this pass and is fixed: enforcing H5's entitlement
gate invalidated `TestEntitlementIsResolvedButNotYetEnforced`, whose own comment
said to delete it the moment somebody wired the gate. Deleted; its replacement
is `entitlement_gate_test.go`. Four sibling tests now provision the starter tier
explicitly rather than relying on the shared fixture, which sits on the top tier
so that a module test is not accidentally a subscription test.

---

## Backend completion pass — 2026-09-03

Seven modules were PARTIAL for the same reason: they had isolation-only or no
functional coverage. Tests proving one tenant cannot read another's rows, and
nothing proving the rows can be read at all. For a reporting surface that is
the weaker half — a query naming a column that does not exist isolates
perfectly and answers 500 to everybody equally, which is exactly how the
batches route shipped broken.

**One real defect found, in promotions.** `Redeem` inserted unconditionally;
`max_uses` and `max_uses_per_customer` were enforced only in `Quote`, by
counting the redemption table outside the transaction that spends against it.
A coupon issued for one use was good for as many as there were counters, and
nothing failed or was logged — the campaign simply cost more than it was
authorised to. The campaign row is now locked before its redemptions are
counted. Pinned by 8 concurrent tills redeeming a one-use coupon exactly once.

**The other six were sound and merely unproven**, and are now proven:
compliance, privacy, portals, group consolidation, documents, global search and
analytics. That is worth stating plainly rather than implying every gap is a
bug — the earlier passes in this session found a defect behind almost every
untested module, and these did not.

**A near-miss in my own test.** The portal isolation test first read
`["invoices"]` where the handler returns `["data"]`, so it counted zero rows no
matter what the portal did and would have passed against a portal that leaked
everything. The positive control — a customer who *should* see an invoice — is
what exposed it. An isolation test with no positive counterpart is not evidence.

### Verified on request: inventory, barcodes, business management, costing

* **Inventory (B4/B4a)** — 46 package tests across `costing_test.go` (23),
  `tieout_test.go` (12) and `shortfall_test.go` (11), plus batch/lot/expiry with
  FEFO. The batches list route was 500ing on `supplier.name` and is fixed.
* **Barcodes (B3)** — auto (bulk generator, idempotent, symbology-aware) and
  manual (hand-assigned EAN, uniqueness enforced) both covered.
* **Financial tracking / costing (C1–C13)** — FIFO, weighted average and
  standard costing with variance; the C13 tie-out holds exactly, pinned by
  `TestTheBooksBalanceAfterARealDay`, `TestStockAgreesWithItsMovementsAfterARealDay`,
  `TestTheCustomerLedgerAgreesWithTheControlAccount` and
  `TestAReturnReversesRevenueTaxCostAndStockTogether`.

---

## Multi-currency and realised FX (G2)

**What was actually wrong was worse than "no revaluation".** Multi-currency was
structural only. `sales_invoice`, `purchase_bill` and `purchase_order` all
carried `currency` and `fx_rate`, and **every caller in the repository passed
`decimal.NewFromInt(1)`**. `RecordBill` and `Collect` both overwrote the
caller's currency with the company's base. No foreign-currency document could
exist, so no gain or loss could arise and nothing was there to revalue.

### What now happens

`0113` makes a rate a recorded fact: a pair, a day, a rate and the source
whoever entered it named. `internal/fx` resolves the rate in force on a
document's own date (the latest not after it), derives the inverse rather than
demanding both directions — a book whose USD→SAR and SAR→USD disagree does not
balance — and **refuses a pair with no rate rather than defaulting to 1**. Which
feed a business books at is its own decision, so there is deliberately no
"fetch today's rates" route; what the product insists on is that a figure has a
source.

`0114` adds `4950 Foreign Exchange Gain` and `5950 Foreign Exchange Loss`, and
version 2 of posting rule 7. A bill is carried at the rate it was booked at for
life; when it is paid, the payable is relieved at that rate, the money leaves at
the payment-day rate, and the difference is realised on a third leg.

Partial and repeated settlements are correct by construction: the difference is
computed **per allocation, against that bill's own rate**, so each settlement
recognises its own share and no other. Four quarter-payments come to exactly
what one full payment would have.

### Two defects found on the way, neither in the brief

* **The migration would have silently done nothing.** `account` and
  `account_role_map` are FORCE row-level security on `current_tenant_id()`, and
  a migration connection carries no tenant — the `INSERT … SELECT` would have
  read zero rows and created no accounts, with the first foreign payment failing
  on an unmapped role months later. This is the trap `0103` hit; `0030` still
  contains the same latent no-op. `0114` lifts FORCE for its own transaction.
* **Payment reversal re-derived its lines from the posting rule.** That looks
  equivalent and is not: a settlement carrying a realised gain has a leg whose
  size came from two rates on two days, and no rule evaluation at reversal time
  recovers it — the reversal would have undone the payment and left the gain
  standing. `accounting.LinesOf` now reads the entry that was actually posted
  and flips it, which is both simpler and right in every case.

Also worth recording: `posting_rule.lines` is immutable by trigger, so a rule is
**versioned, not edited**. Every entry cites the version that produced it, and
rewriting one in place would leave posted history explained by lines that were
never used. Rule 7 version 1 stays exactly as it was.

### Deliberately not done

**Unrealised revaluation of open balances at period end.** That is a
period-close routine with its own posting and reversal, and attempting it
alongside realised recognition is the classic way to count one movement twice.
It is a distinct feature, not a missing half of this one.

### Tests (19)

Rate management (10): recorded and read back; a missing pair refuses with
`unverified_rule` rather than assuming par; a currency against itself is one;
the rate in force is the latest not after the date and does not reach back
before the first; re-recording a day corrects rather than duplicates; the
inverse is derived; a rate must name its source; a non-positive rate is
refused; rates do not cross tenants; setting one takes the bookkeeping verb.

Realised gain/loss (9): **loss**, **gain** (and proven not to reach sales
revenue), **settlement at the booked rate realises nothing**, **partial
settlement realises only its share**, **four settlements do not double-recognise**,
**the journal balances** at 3,800 debits against 3,800 credits, **eight
concurrent payments of one bill settle it once**, a foreign bill needs its own
tenant's rate, and a currency with no rate is refused by name.

---

## Business and financial management — verified audit, 2026-09-03

Driven against the ledger rather than against route counts. The headline is
that this area was **already substantially built and correct**; what it lacked
was proof, and one genuine gap (multi-currency) which is now closed.

### What an owner can already answer, and where it comes from

| Question | Route | Tied out by |
|---|---|---|
| How much came from sales | `/dashboard/sales`, `/reports/profit-and-loss` | dashboard revenue proven equal to P&L revenue |
| Revenue, COGS, gross profit | `/dashboard/overview` | `gross = revenue − cost` asserted |
| Net profit / loss | `/reports/profit-and-loss` | balance sheet's current earnings |
| Financial position | `/reports/balance-sheet` | **assets = liabilities + equity + current earnings** |
| Cash flow and cash position | `/reports/cash-flow` (direct method) | **opening + net = closing**, and closing = the cash account |
| Every account's balance | `/reports/trial-balance` | **each row equals that account's net in the journal** |
| Who owes the business | `/receivables/ageing` | already tied to the AR control account |
| What the business owes | `/purchasing/ageing` | **now tied to the AP control account** |
| What was spent, and on what | `/expenses` with `/expenses/heads` | expense heads map to accounts |
| Owner capital and withdrawals | `/investors`, `/investors/movements` | **capital never reaches the P&L** |
| Inventory value and movement | stock valuation, `stock_movement` | C13 tie-out, four end-of-day tests |
| Payroll cost | `/payroll` | commission now feeds it correctly |
| Product and shop performance | `/analytics/*`, `/reports/*` | analytics reads proven to answer |

### Money in and money out

Every inflow and outflow named in the brief resolves to a posting rule and a
journal entry: sales receipts and customer collections (rule 8), supplier
payments (rule 7, now with realised FX), expenses (rule 6), payroll, asset
purchases and disposals, refunds and credit notes (rule 4), owner capital in
and out (rule 12 and its mirror), and cash/bank transfers through treasury.
There is no operational module that moves money without a journal entry, and
the drawer, the bank and the ledger reconcile through the cash session and
bank-reconciliation modules.

**Investment is not revenue, and this is enforced rather than intended.**
`assets/investors.go` posts capital through `equity.contribution` /
`equity.withdrawal`, neither of which touches a revenue or expense account, and
`TestCapitalNeverReachesTheProfitAndLoss` and
`TestAWithdrawalReducesCapitalAndNotProfit` hold it there.

### What was actually missing

* **Multi-currency** — see the *Multi-currency and realised FX* section. This
  was the one real hole in the financial model: no foreign-currency document
  could exist and every rate was 1.
* **The three financial statements had no functional tests.** Trial balance,
  balance sheet and cash flow were reachable only from the permission walks —
  tests that check who may call a route and never look at the answer. They are
  now tied out against the journal, per account. All three were already
  correct; nobody had shown it.
* **The payables ageing was not tied to its control account.** Receivables
  already was. C9.3 makes both hard invariants.

### Deliberately not attempted, with the reason

* **Unrealised FX revaluation at period end** — a distinct period-close routine
  with its own posting and reversal. Doing it alongside realised recognition is
  how a movement gets counted twice.
* **Indirect-method cash flow** — needs every account classified as operating,
  investing or financing, which this chart of accounts does not carry.
  `reports.go` says so in place: inventing the classification would produce a
  statement that looks authoritative and is wrong. The direct method needs no
  classification and is what ships.
* **Budgeting and cost centres** — the Blueprint does not define a budget model
  or a cost-centre dimension, and `store_id` on every journal line already
  answers "which shop spent it". Inventing a budgeting module would be
  inventing product.

### Tests added (6)

`internal/api/statements_test.go` — the trial balance balances, is not empty,
and every row equals its account's net in the journal; the balance sheet
balances including current earnings; cash flow's opening plus movement is its
closing, and that closing is the cash account; the payables ageing agrees with
the AP control account; statements are drawn from the journal rather than from
document state; and all four statements take `accounting.view`.

---

## Final completion check — 2026-09-03

Evidence-based sweep of the whole backend rather than a re-audit of proven
modules. Mechanical checks first: no duplicate migration versions, `0115` (a
duplicate reservation table I created and reverted) is gone, no `.env` or key
material tracked, and every one of the 18 `TODO`/`FIXME`/`placeholder` matches
in non-test code is **prose describing the refuse-on-placeholder design**, not
an actual placeholder. There is no `panic("not implemented")` anywhere.

Client-supplied identity: `cashier_id` comes from the authenticated user,
`company_id` is checked by `CanAccessCompany` at 23 call sites and walked over
the whole route table by `company_confinement_walk_test.go`. The one
`employee_id` read from a query string is a FILTER inside an already-scoped
tenant and company, not a trust boundary.

### One genuine defect found and fixed

**`aftersales.ExpireHolds` was called by nothing.** No route, no job, no
schedule — the function was written, correct, and unreachable.
`stock_reservation.expires_at` was recorded on every hold and the deadline never
arrived. B13 reserves against UNPAID online orders precisely so an abandoned
basket cannot hold the last unit for ever, and that is exactly what happened:
the unit became unsellable through every channel, permanently, with nothing
anywhere saying why the shelf showed one and the till refused to sell it.

Fixed by giving it the path it was written for — `jobs.ReservationExpirySweeper`,
following the existing `LowStockSweeper` / `BatchExpirySweeper` pattern exactly
and registered in the worker beside them. No new mechanism, no second
reservation system.

Fixing it exposed a second, smaller one: `releaseOrderHolds` wrote
`created_by = scope.UserID` unconditionally, so a release with no human behind
it violated the foreign key on the zero uuid. A sweep releasing a lapsed hold
now writes NULL, which is what the nullable column is for — "the system, on a
deadline" — rather than naming somebody who did not do it.

### Reservation ledger, now proven end to end (8 tests)

Holding takes effect · **releasing puts stock back on sale** · **a lapsed hold
is released by the sweep** · **an unexpired hold survives it** · **a hold with
no deadline is never swept** (null means "until the order resolves", which is a
paid order's hold) · **eight concurrent channels each asking for all ten units
yield exactly one** · holds do not cross tenants · holding takes
`order.manage`.

`Reserve` was already correct: it takes a transaction-scoped advisory lock on
the variant and warehouse before reading availability, with a documented reason
for advisory over `SELECT … FOR UPDATE` — there is nothing to lock before the
first reservation, so two callers would both find nothing. The ledger is
append-only by trigger, signed, and writes no stock movement, so the C13
tie-out is unaffected.

---

## Saudi payroll: GOSI and the WPS wage file, 2026-09-03

Both were recorded as external blockers. Both turned out to be reachable, and
one of them exposed invented code that had been sitting behind the gate.

### The registry had no write path at all

A4 gives the Platform Owner "tax templates" and E8 built the registry they live
in — payload, effective dates, source authority, source document, source URL,
`verified_on`, `verified_by`. **Nothing exposed any of it.** There was no route
and no service method that could write a rule, add a tax jurisdiction, or record
a rate, so calling these "operations tasks" was wrong: the only way to perform
one was a SQL client against production.

`internal/registry/admin.go` and five Super-Admin routes close that. A
correction **supersedes by date rather than overwriting**, because a payroll run
resolves the rule in force on the month being processed and editing in place
would restate months already computed. A payload still containing `__VERIFY__`
is refused outright. `verified` remains a person's assertion with their id
recorded, not a flag. 14 tests.

### WPS — the specification is published, and the old formats were invented

I had reported the Mudad format as unavailable. That was wrong. MHRSD publishes
**"WPS Wages File Specification"**, 21 pages, retrieved 2026-09-03 from
`https://www.hrsd.gov.sa/sites/default/files/2017-06/WPS%20Wages%20File%20Technical%20Specification.pdf`.

Worse, the product already carried two wage-file formats — `mudad_xml` and
`sif` — and **neither appears in any Ministry document**. They were invented.
They never reached a bank only because the format rule was unverified, so the
generator refused before it could write one.

`internal/people/wpsfile.go` implements the real layout: TAB-delimited text
(§1.7), a 10-field Header Group (table 2), a 14-field Content Group (table 4),
SAR only, `-` terminating the data. `[32A-AMT]` is summed from the rows because
the receiving bank validates it and rejects the whole file on a mismatch. The
bank-only fields — `[FILE-REJCDE]`, `[RET-CODE]`, `[TRN-REF]`, `[TRN-STATUS]`,
`[TRN-DATE]` — are written empty, because an establishment filling them would
be claiming a payment had executed. `[D-DATE]` and `[70-DET]` are optional and
left empty rather than guessed.

`0115` adds the four establishment identifiers the Header Group needs, which the
company table did not have. A business without them is refused **by name**,
because a file rejected by the bank comes back days after payday.

### GOSI — the contradictions resolved, the escalation deliberately not invented

Two GOSI pages appeared to disagree, and both resolve:

* **Occupational Hazards 2% vs 1.5%.** GOSI's Employer FAQ states the employer's
  share as *"9% for Annuities Branch and 2% for Occupational Hazard Branch"*.
  That page is GOSI telling employers what they pay, which is the question a
  payroll engine asks. 2% taken; the discrepancy is recorded in the rule's notes
  so it is re-checked rather than forgotten.
* **Minimum wage SR 400 vs SR 1,500.** Not a conflict — the minimum differs *by
  branch*. Only the maximum, SR 45,000, is common, and that is what `wage_cap`
  means.

Recorded: Saudi 11.75% employer (9% Annuities + 0.75% SANED + 2% Hazards) and
9.75% employee; non-Saudi 2% employer only, since the Annuities Branch and SANED
are Saudi-only.

**What is deliberately not claimed:** GOSI publishes one Annuities rate and no
year-by-year escalation for post-July-2024 entrants. Commentary describing one
is Tier 2, which Blueprint Part N forbids as a basis for a compliance figure.
Both Saudi bands therefore carry the published rate, and the rule's notes name
exactly what to re-check annually and that any change becomes a **new version**
rather than an edit.

### Two obsolete tests replaced, for the same reason as the entitlement one

`TestPayrollSaysWhenGOSIIsNotVerifiedRatherThanGuessing` and
`TestTheWageFileRefusesUntilTheFormatIsVerified` asserted refusals that were
correct only while the values were `__VERIFY__`. They now assert the truth:
GOSI is deducted (780.00 from the employee and 940.00 from the employer on an
8,000 wage) and a wage file cannot be drawn from an unapproved run. Resurrecting
the old assertions would have required mutating a **global** registry rule
underneath every other test in the package; the guarantee they protected is kept
by `TestAPlaceholderPayloadIsRefused` and the production verification gate.

---

## B11's last step: invoicing an order, 2026-09-03

Found by forensic audit, not by a failing test — nothing tested it because
nothing could reach it.

### What was wrong

`sales_order.invoice_id` was a column **nothing in the codebase ever wrote**.
There was no route to invoice an order, and `orders.Advance` refused the final
transition with *"Raise the invoice to complete it — an order is finished by
being invoiced, not by being marked so"*. The product told an owner to do
something it gave them no way to do, and `TestAnOrderCannotBeCompletedWithoutAnInvoice`
locked that refusal in as correct behaviour. An order could never leave
`delivered`.

Worse than an unreachable state: **`internal/orders` touched neither stock nor
accounting.** `Deliver` calls `recordQuantities`, which writes `qty_delivered`
and nothing else. There is no stock movement and no journal anywhere in the
package. So a business using the order flow to sell would mark goods delivered,
never reduce the shelf, and never see revenue, tax or cost of sale reach the
ledger.

To be exact about severity: nothing posted, so nothing posted was *wrong*, and
the reservation ledger stopped the stock being double-sold. It was an
unreachable workflow rather than bad numbers — but B11's lifecycle
("Draft → Confirmed → Processing → Packed → Delivered → Completed") could not
complete, and the Blueprint requires a quotation to be "convertible to a Sales
Order or directly to an Invoice in one click".

### What it does now

`POST /api/v1/orders/{orderID}/invoice`, gated on `sales.create` because it
raises a tax document rather than merely managing an order.

It **reuses `sales.Finalize`** — the till's own engine — rather than writing a
second invoice path. That engine already writes the invoice, moves the stock,
costs it under the company's method, posts revenue, tax and COGS, records
loyalty and promotions and writes the audit trail. A second implementation
would be a second definition of what a sale is worth, and they would drift.

The terminal it builds has no device, no cash session and **no EGS unit**, so
`Terminal.OnAChain()` is false and the e-invoicing chain is untouched — ZATCA
stays deferred.

Everything commits in **one transaction**: the invoice, the order's completion,
and the release of its stock hold. Any split leaves a state somebody has to
reconcile by hand — an invoice with no order pointing at it can be raised
twice, and an order marked completed with no invoice is a sale nobody can find.

Prices come from the order, not from today's price list: a customer who
negotiated a price in March must not find the invoice charging April's. With no
tenders supplied the sale goes on account as `customer_due`, which needs a
customer to owe it; without one the caller is asked how it was paid rather than
having a method guessed for them.

### Tests (7)

Completes the order and links the invoice · **moves stock and posts revenue
200.00, tax 30.00 and a non-zero COGS**, having first proved delivery alone
moved nothing · billing twice yields one invoice · an undelivered order is
refused by name · invoicing releases the stock hold · the route is
permission-gated · one tenant cannot invoice another's order.

---

## Forensic audit findings, 2026-09-03

Three defects found by reading the code rather than by a failing test. None had
a failing test, because in each case nothing could reach the feature to fail.
They are the same shape: **something the product enforces, with no path for
anyone to change or complete it.**

### 1. B11 could not complete — fixed

See the *Order invoicing* section. `sales_order.invoice_id` was written by
nothing, no route existed, and `internal/orders` touched neither stock nor
accounting.

### 2. The registry could not be written — fixed

See the *Saudi payroll* section. Every unverified legal value was described as
"an operations task" and the operation could only be performed with a SQL client
against production.

Fixing it exposed a hole in the fix: `RecordRule` allowed an unverified rule
with **no note**, which is exactly what `TestUnverifiedRulesAreNotDisguised`
forbids the database to contain. The write path could create the state the
invariant exists to prevent. It now refuses.

### 3. Tenant limits were enforced and unwritable — fixed

`tenant_limit` gates companies, shops, users, terminals, SKUs, custom roles,
storage and SMS credits. It is read by provisioning (which refuses a second
company on a one-company plan), by identity (which refuses the sixth user on a
plan selling five) and by the entitlement gate. It was written **once at
signup** and then unreachable, so a tenant who upgraded could not be given the
headroom they had paid for.

`GET`/`PUT /api/v1/platform/tenants/{tenantID}/limits`, Super Admin only.

Every field is optional, because a plan upgrade moves one or two numbers and
requiring the whole set is how a storage limit gets reset by somebody adding a
till. Lowering a limit below what is already in use is refused and names the
figure: a limit gates the next thing created and does not delete what exists, so
setting max_stores to 2 for a business running 5 would leave a state the product
cannot express.

4 tests, including that a business owner cannot raise their own allowances —
which would be buying capacity without paying for it.

### Also checked, and found sound

* **Approvals genuinely block.** `Blocked` refuses with 403; `NeedsApproval`
  rolls the transaction back, writes the request in its own transaction so it
  survives the refusal, and returns `CodeComplianceBlocked`. Wired into expenses
  and purchase-order issue.
* **Idempotency on every money path**: sales (`alreadyRung`), customer receipts
  (`alreadyTaken`), purchase bills, supplier payments, expenses, settlement,
  stock movements, treasury transfers, and order invoicing.
* **No real placeholders.** Four `panic(` calls in non-test code: two are
  correct re-panics in recovery middleware, two are ZATCA compile-time constant
  assertions. Every `TODO`/`placeholder` match is prose describing the
  refuse-on-placeholder design.
* **Migrations**: 120 files, 0001–0120, no duplicates, no gaps, strictly
  increasing. The duplicate reservation migration created and reverted earlier
  in this session is gone; 0088's ledger is the only reservation system.
* **No secrets tracked.**

### A note on searching

Two findings in this session came from bad searches rather than bad code. A
`grep … | head -3` hid the existing reservation system and led to a duplicate
being built; a package-reference heuristic reported all 46 packages as
unreachable and was discarded. A truncated search is not a search.

---

# Regulatory completion: GOSI, US tax, ZATCA

Four things can be true of a regulatory feature, and they are not the same
thing. This section says which is true of each, because "done" hides the
difference:

* **CODE COMPLETE** — the product implements it and tests hold it.
* **DATA INGESTED** — the authority's own published figures are on file.
* **OFFICIAL SOURCE VERIFIED** — a named person has checked those figures
  against the authority and stamped them, which is what lets the product
  charge them.
* **PRODUCTION CREDENTIAL REQUIRED** — nothing further can be done here; it
  needs a secret only the taxpayer can obtain.

## US sales tax — CODE COMPLETE, DATA INGESTED, awaiting verification

### What CDTFA publishes, and why it could not just be loaded

The California Department of Tax and Fee Administration issues "California City
& County Sales & Use Tax Rates" as a spreadsheet each quarter — there is no API,
and the filename carries the effective date. The latest published file is
`SalesTaxRates07-01-26.xlsx`, effective 1 July 2026; the following quarter's URL
still returns an error page, so July is the schedule in force.

Each row is a location and its **combined** rate. Alameda reads 10.75%, and
that figure already contains California's 7.25% statewide rate and every
district applying at that address. This product sums a jurisdiction chain
instead, so loading 10.75% under a state already holding 7.25% would have
charged 18%.

So each location's stored share is the published combined rate **less** the
statewide rate, and the chain adds back to exactly what CDTFA printed — which is
the number a shop is audited against. That is arithmetic on two official
figures, not a judgement about what any district levies.

Locations hang off the **state**, never off their county: CDTFA's city figure
already includes any county district, so a city nested under its county would
have counted that district twice. The county rows are locations in their own
right — the rate for the unincorporated parts — and sit beside the cities.

### `cmd/cdtfaimport`

The conversion is a step somebody performs each quarter, so it is written down
and repeatable rather than done by hand. It reads the authority's workbook and
emits the payload `POST /api/v1/platform/jurisdictions/import` accepts. It
decides nothing: output is unverified, and a person still records it.

Three things it refuses or reports rather than swallowing:

* **A location below the statewide base** aborts the run. That means the state
  rate has changed and the file is being read against a stale base; a clamped
  negative share would hide it.
* **Five counties CDTFA publishes no rate for** — Del Norte, Kern, Monterey,
  Santa Cruz and Yuba — are named on the way out, not counted. Their Rate cell
  is empty and a note directs the reader to the city or the unincorporated
  area, each of which CDTFA does publish. A county-wide figure invented for
  them, or a zero, would undercharge every sale in them.
* **Cells are placed by their spreadsheet reference**, not by document order. A
  workbook omits empty cells rather than writing them, so appending in order
  shifted every later column left on exactly those five rows — putting the
  county name where the rate belongs.

One more trap, worth recording because it aborted the first run: the workbook
stores rates as IEEE-754 doubles, and 7.25% is not exactly representable. Alpine
County arrives as `0.072499999999999995`. Every rate CDTFA publishes is exact to
five decimal places, so rounding to six recovers the published figure and
discards only the storage error.

### 0118

541 locations, each with its own share, effective 2026-07-01, sourced to the
file and the page. California's own 7.25% row is untouched — it has stood since
2017 and this schedule does not change it.

`verified_on` is NULL on every row, and that is deliberate. The figures are the
authority's own, but the conversion from combined rates to shares is this
product's arithmetic and nobody has put their name to it. **The product refuses
an unverified rate rather than charging it**, so this does not yet let a
Californian shop trade — it means the operator confirming these rates is
checking 541 rows that are already filled in instead of typing them.

### Tests

`internal/api/cdtfa_test.go` and `internal/registry/cdtfa_test.go`, every figure
transcribed from the authority's file:

* Every shipped share plus the statewide rate equals what CDTFA published, for
  a deliberate spread — Adelanto 7.75%, Alameda city 10.75% against Alameda
  County 10.25%, Bakersfield 8.25%, La Cañada Flintridge 10.50%, Los Angeles
  9.75%, Santa Fe Springs 11.00% (the highest in the state), Alpine County
  7.25%. Also that each hangs off California and takes effect on 2026-07-01.
* Alpine County is recorded at **zero**, not left out. Absence and zero are
  different facts, and the resolver refuses a chain with an unanswered
  authority — so omitting a county that genuinely levies nothing would block
  every sale in it.
* The five rateless counties are absent while their unincorporated areas are
  present.
* Nothing in the shipped schedule is marked verified.
* A production deployment (`requireVerified`) refuses to price a sale on it,
  and once verified charges exactly 10.75% in Alameda with the state's 0.0725
  and the city's 0.035 nameable separately. Both run inside a transaction that
  is rolled back, so the test cannot mark anything verified for anybody else.
* A till given the real figures charges CDTFA's published rate on $100 for
  eight named locations.
* **An invoice already issued is not rewritten by a later schedule** — imported
  a second schedule covering the sale's own date and the invoice keeps its
  10.75% and its recorded 0.035 city share.

No national US rate is invented anywhere: 0110 records the country root at an
explicit **0**, which is a fact about the federation, not a guess.

## GOSI — CODE COMPLETE, OFFICIAL SOURCE VERIFIED

Re-checked against GOSI's own pages on 2026-09-04 (`FAQ_Employer`,
`FAQ_Contributor`). They state Annuities 18% split 9/9, Occupational Hazards 2%
payable by the employer, SANED 1.5% shared equally, contributory wage maximum
SR 45,000 — which is the 11.75% employer / 9.75% employee that 0117 records.

**The July 2024 law still has no published rate schedule.** GOSI's pages state
the Annuities Branch flatly at 18% with no hire-date distinction and publish no
year-by-year escalation. The Council of Ministers announcement covers who the
new law applies to, retirement age and eligibility — not rates. So both Saudi
bands stay at the published rate, which is what GOSI says an employer pays today
for either, and the rule's note carries the re-check. If GOSI publishes a dated
schedule it becomes a further version with its own `effective_from`, which is
what the registry's dating is for.

One figure worth recording that the pages added: Occupational Hazards can be
**doubled to 4%** for non-compliance with safety regulations. That is a penalty
rate, not the standard one, and the product correctly uses 2%.

### Effective-date boundary, now tested

0117 takes effect 2026-02-01 and closes the placeholder that stood before it. A
run for January 2026 resolves the placeholder and reports social insurance as
uncalculable; February resolves the recorded rates and deducts 780.00 from
8,000. If resolution used today's date instead, every historical month would be
restated at whatever the rule says now. Also tested: the same month run twice
resolves the same rule.

## Payroll could never be corrected — fixed

Found while writing the reversal tests, and the same pattern as the rest of this
document: **something the schema models, that nothing could reach.**

0091 gave `payroll_run` four states and built the month's uniqueness around the
fourth:

```sql
CREATE UNIQUE INDEX payroll_run_period_uq ON payroll_run (company_id, period)
  WHERE status <> 'cancelled';
```

The index is partial precisely so a cancelled run releases its month for a
corrected one. **No code in the product could set `cancelled`.** Approve posts
two journal entries and Pay posts a third, and from there a run was final: a
month approved on the wrong attendance, the wrong advance recovery or the wrong
GOSI band stayed wrong, the entries stayed in the ledger, and the month could
never be run again because the index still counted the bad run.

`0119` adds who cancelled it, when and why — required, on the same terms as a
rejected leave request. `people.Cancel` and
`POST /api/v1/payroll/{runID}/cancel` (behind `payroll.approve`, the same
authority that posted the entries) unwind it:

* **Reversed, not deleted.** A posted month is a fact and correcting it is a
  second fact. The lines come from the entry and are flipped, never re-derived
  from the posting rule — the rule may have been amended since. Each reversal
  takes its own `source_type`, because the journal's idempotency key is
  `(source_type, source_id, rule_key)` and reusing the original's triple would
  find the original entry and post nothing at all.
* **Whatever is there.** A draft posted nothing, an approved run has two
  entries, a paid one has three.
* **Advances go back to being owed.** `advance_outstanding()` sums the recovery
  rows, so leaving them would show a loan as partly repaid out of a month that
  was never paid.
* **A wage file already submitted to the bank refuses the cancellation.** The
  product can reverse its own ledger; it cannot recall a transfer somebody has
  instructed, and pretending otherwise would leave the books saying the month
  never happened while the money was on its way.

12 tests. The reversal assertion is not a count of entries but the run's whole
ledger footprint: every account touched by any entry carrying the run's id nets
to zero afterwards.

## ZATCA — CODE COMPLETE, PRODUCTION CREDENTIAL REQUIRED

The bug flagged for this session — `stamp` wrongly associated with `SignedXML`
in `internal/jobs/zatcasubmit.go` — **is already fixed**, and the fix carries a
comment recording the mistake: `SignedXML` takes the document and `Stamp` is
sent beside it, because sending the stamp as the document would post a signature
with nothing attached to it.

26 files and 146 tests cover canonicalisation, XAdES, the CSR (checked with
OpenSSL), certificates, credential sealing and key rotation, the ICV/PIH chain
across 10,000 invoices with per-unit isolation and immutability, the QR payload
(reproducing ZATCA's own worked Phase-1 example), onboarding and renewal, and
submission. Validation is checked against ZATCA's own validator. It is wired
into `cmd/worker` and gated by market, so a shop off the chain never touches it.

What remains is genuinely external: **the OTP from the taxpayer's own Fatoora
portal**. No certificate is fabricated, no API response is faked, and production
onboarding is not marked complete.

---

# Final completion pass: verification workflow, export, isolation

The three items carried as ⚠️ were GOSI's post-July-2024 schedule, the imported
CDTFA rates being unverified, and ZATCA's production credential. Working
through them turned up two things that were not on the list and mattered more
than two of the three.

## The imported rates could never be verified — fixed

0118 loaded CDTFA's 541 Californian locations and marked none of them verified,
which was right. There was then **no way to ever verify them**.

Every `verified_on` write in the registry is on an INSERT path:
`RecordJurisdictionRate` writes a new rate, `ImportRates` writes a batch, and
neither can stamp a row that already exists. Re-importing the same schedule
does nothing at all — the supersession UPDATE only closes rows starting BEFORE
the new date, and the insert that follows hits the no-overlap constraint and is
swallowed by `ON CONFLICT DO NOTHING`.

So the shipped schedule was permanently stuck at "imported", every Californian
shop was permanently unable to trade, and "0 rates verified" was not an
operations task waiting to be done — it was an operations task that could not
be done. Same shape as the payroll run that could never be cancelled and the
tenant limit that could never be raised.

### Four states, two people

`0120` adds `imported_by`, `reviewed_on`, `reviewed_by` and `review_note`, and
`registry.VerifyRates` is the route out:

    imported  — the rows exist and nothing is stamped
    reviewed  — somebody has checked them against the authority's publication
    verified  — a SECOND person has signed them off for production use
    active    — verified, in force on the date being priced, not superseded

Only the first three are stored; "active" is a question about a date and is
answered by resolution rather than by a column somebody has to maintain.

Two refusals carry the weight. An unreviewed schedule cannot be verified, and
**the reviewer cannot be the one who verifies**. One person mistyping a decimal
in a tax rate charges every customer of every shop in that jurisdiction the
wrong amount, and the shop remits the wrong amount to the state; a second pair
of eyes is the cheapest available control on that.

A batch is `(country, source_document, treatment, effective_from)` — what an
authority actually publishes. Verifying 541 rows one at a time is not a safer
version of the same thing, it is the same thing performed 541 times, which is
how the 300th one stops being read.

### The figure cannot move under a verification

Stamping a rate verified is an UPDATE, and nothing stopped that UPDATE from
also moving the rate, its dates or its provenance. `0120` puts the same
frozen-column trigger on `tax_jurisdiction_rate` that `regulatory_rule` has
since 0004: `jurisdiction_id`, `treatment`, `rate`, `effective_from`,
`source_authority` and `source_document` are immutable once written.
`effective_to` stays mutable, because closing a row is how a later schedule
supersedes an earlier one.

Three routes, Super Admin only: `GET /api/v1/platform/jurisdictions/rates`
lists every schedule and how far through review it is;
`POST .../rates/review` and `POST .../rates/verify` move it. Both write an
audit entry. 9 tests.

## Reports could not be exported — fixed

`report.export` had been seeded on the Owner and Accountant roles since the
permissions were written and guarded no route. The verb existed, the roles held
it, and there was nothing to hold: an owner who wanted the day's takings in a
spreadsheet could read them on a screen and retype them.

`GET /api/v1/reports/{kind}/export` now returns CSV for sales, expenses, stock,
trial balance, profit and loss, and the balance sheet.

The property that makes it worth having is that **every export calls the same
service method the screen calls**. It does not re-query. An export with its own
SQL is one that disagrees with the page it was taken from, eventually and
quietly, and the person who finds out is reconciling to a bank. A test asserts
the exported trial balance equals the screen's, figure for figure.

The file opens with a UTF-8 byte order mark, because Excel on Windows otherwise
reads it as the system codepage and turns every Arabic account name into
mojibake. Each export names its currency: a column of money with no currency on
it is a page of numbers, and this product sells into three markets.

`report.export` is removed from the permission ledger in
`TestSeededPermissionsWithNoRoute`, which fails if an entry there does guard a
route.

## Tenant isolation is now an invariant, not a sample

The isolation tests prove isolation by doing it — write as one tenant, read as
another, check nothing comes back. That is the right way to test the mechanism,
and it tests the tables those tests happen to touch. It cannot catch the next
table: a migration that adds `tenant_id` and forgets `ENABLE ROW LEVEL
SECURITY`, or enables it without `FORCE`, or forces it with no policy, produces
a table readable across every business on the platform, and every existing test
still passes because none of them knows it exists.

`TestEveryTenantScopedTableIsIsolated` asks the catalogue directly. **163 tables
carry a `tenant_id`; 162 have RLS enabled, forced, and at least one policy.**

The one exception is `job`, and it is deliberate — 0027 says so in the table's
own comment. It was checked rather than taken on trust: every access in
`internal/jobs` goes through `TxAsPlatform`, including `EnqueueIn`, so no tenant
connection reads or writes it, and a row carries an invoice id and a kind rather
than business content. It is recorded in the test with that reason, so a future
table cannot join it silently.

A second test pins the other half: the application role is neither `SUPERUSER`
nor `BYPASSRLS`, without which every guarantee above would be advisory.

## GOSI — the question behind the missing schedule

Re-checked against GOSI's own pages: Annuities 18% split 9/9, Occupational
Hazards 2% employer, SANED 1.5% shared equally, ceiling SR 45,000 — the 11.75%
/ 9.75% that 0117 records. **No post-July-2024 escalation is published**, and
the Council of Ministers announcement covers who the new law applies to,
retirement age and eligibility, not rates.

The question that leaves open is whether the product could accept such a
schedule when it appears. It can, and that is now tested rather than asserted:
an escalation IS a series of dated rules, and `TestAPublishedGOSIEscalation-
NeedsNoCodeChange` stages a future version, shows March 2027 resolving it while
August 2026 still resolves 0117's, and shows the cohort the July 2024 law did
not move staying where it was. The wage ceiling is read from the rule too, so a
change to it is a new version rather than a release.

What would need code is a new *dimension* — a rate that depended on years of
contribution rather than hire date. A rate change, a cohort change or a ceiling
change does not.

### Two fabricated Saudi rules were live, and three tests were green because of them

Found while writing those tests, and the most serious thing in this pass.

Two rows written by earlier test runs sat open-ended from 2027-01-01, both
unverified, both labelled "written by a test run and left behind. Not a
confirmed value":

* **`SA.GOSI.RATES` at an employer rate of 12.75%**, a figure no authority
  published.
* **`SA.WPS.WAGE_FILE_FORMAT`**, sourced to a "reported revision awaiting
  confirmation".

A label is not a date range. Each had CLOSED the verified version to make room
for itself, so from 2027 the product would have resolved a made-up contribution
rate and no confirmed wage-file layout at all.

Worse, they were holding three registry health tests and one provisioning gate
test green. Those tests assert that the Saudi release-blockers are unverified
and block a Saudi deployment — and they passed because of fabricated rows,
having stopped being true when 0116 and 0117 recorded the real figures. Green
for the wrong reason is worse than red.

Both rows are removed, the verified versions reopened, and the registry is now
exactly what the migrations alone produce:

    SA.EOSB.ENTITLEMENT       2026-01-01 -> open   verified=false
    SA.GOSI.RATES             2026-02-01 -> open   verified=true
    SA.WPS.WAGE_FILE_FORMAT   2026-02-01 -> open   verified=true

`saudiHRBlockers` and the provisioning gate's expected list now name only
**SA.EOSB.ENTITLEMENT**, which is the one genuinely outstanding: the
entitlement is days of wage per year of service and nobody has confirmed the
bands against the Labour Law. The gate itself is unchanged — it still refuses a
Saudi business and still names what is missing; there is one rule left to
confirm rather than three.

The test that caught it is kept: `TestStagingAGOSIScheduleLeavesNothingBehind`
fails if a staged version is ever committed.

## ZATCA

Unchanged and re-confirmed. The `stamp`/`SignedXML` bug is fixed, market gating
runs through `market.EInvoicingApplies` in three places with a Bangladeshi shop
test holding it, and `QueueSubmission` enqueues inside the sale's own
transaction via `EnqueueIn` — so an invoice cannot exist without the obligation
to report it, which is the exposure E1.2 names. The OTP from the taxpayer's
Fatoora portal remains the only genuinely external dependency.

## Still awaited, honestly

`accounting.approve` is seeded on the Accountant role and guards no route,
recorded in the permission ledger as awaiting Phase 2. It awaits a module that
does not exist: **there is no manual journal entry anywhere in the product**.
Every journal entry is posted by rule from a business document — a sale, a
purchase, a payroll run, an expense — which is a deliberate and safer design
than free-form journals. Adding manual entries plus their approval is a feature
decision, not a defect, and it is left as one rather than half-built.

`compliance.retry_submission` remains deliberately unoffered: submission is
automatic and ordered, and there is no dead-letter path that discards an
unreported invoice.

---

# Forensic audit: money, stock, orders, reports, search, integrations

The previous pass said plainly that sections 6–8 and 10–12 had not been audited
line by line. This is that audit. It found one real defect, disproved one that
looked real, and confirmed the rest with evidence rather than with the fact that
tests exist.

## The defect: a receipt reversal rebuilt itself from the rule

`receivables/reversing.go` reversed a customer receipt by resolving the
`payment.customer` posting rule **at today's date** and rebuilding the lines
from it:

```go
rule, _ := accounting.ResolveRule(ctx, tx, "payment.customer", country, receivedOn)
lines, _ := rule.Build(...)          // ← rebuilt
Lines: accounting.FlipSides(lines),
```

The supplier side does the opposite and says why:

```go
lines, _ := accounting.LinesOf(ctx, tx, *orig.entryID)   // ← read from the entry
```

Two ways the rebuild is wrong. A rule amended between the receipt and its
reversal produces a reversal shaped differently from the entry it claims to
undo, so the receipt's journal never nets to zero. And a receipt that settled a
foreign-currency invoice carries a realised exchange difference whose size
depended on two rates on two days — no rule evaluation at reversal time
recovers it, so the gain would stand while the receipt that produced it was
undone.

Fixed to read the posted entry, exactly as purchasing does. The two sides of the
ledger now behave alike.

**Why the existing tests missed it.**
`TestAReversingReceiptFlipsTheOriginalJournal` checks the cash and receivable
legs of a plain domestic receipt, and a rebuilt reversal passes it — on that
entry the rebuild happens to produce the same two lines. The new test compares
the whole line set and the net footprint across every account touched, so any
leg the rebuild would drop, add or size differently fails it.

## The one that looked real and was not

The over-return limit is enforced by 0019's `assert_return_within_original()`,
which sums what has already gone back. That aggregate cannot see a credit-note
line another transaction has inserted and not committed, and the application
check in `ComputeReturn` reads the same figures a moment earlier. Neither is a
lock. On paper, six cashiers returning the same item at once each see nothing
returned and all six succeed.

They do not. `claim_invoice_number` takes the credit note's number with an
`INSERT ... ON CONFLICT DO UPDATE` on the per-store counter — which holds a row
lock until commit — and it runs **before** the invoice and its lines are
written. Every document in a store's series queues there, so by the time the
second return reaches the trigger the first is committed and visible. Six
concurrent returns of one item produce one credit note.

A migration adding an explicit lock was written, tested, and then **deleted**:
it fixed nothing, and shipping a migration on a false premise is worse than
shipping none. The test is kept, because the protection is real but
*incidental* — it comes from document numbering rather than from the rule being
enforced, and would disappear quietly if numbering moved to a sequence or
claimed its number after the lines were written.

## What was verified, and how

**Oversell (7).** Traced to the SQL. `readPool` and `readLayers` both take
`FOR UPDATE`, and `LockStock` takes every variant a sale touches — bundle
components included — in sorted order, so two sales sharing two items queue
instead of deadlocking. It is genuinely called: `sales/finalize.go:563` and four
places in `stockops`. The order in the sale is lock (563) → consume under the
lock (616) → availability check (620), so the check reads state nobody else can
be changing. `TestConcurrentSalesCannotOversellTheLastUnit` puts six tills on
one unit and expects one sale.

**Order quantities (8).** Enforced by CHECK constraints rather than by
application code: `qty_picked <= qty` and `qty_delivered <= qty_picked` make
over-delivery unrepresentable. Order invoicing takes `FOR UPDATE` on the order
and returns the existing invoice when one is already recorded, so a retry that
lost its response gets the same answer instead of a second invoice.

**Reports (10).** No join fan-out: the day's line count is a correlated
subquery, not a join, and the fourteen-day trend is a `generate_series` LEFT
JOIN grouped by day with the company filter in the JOIN rather than the WHERE —
which is what keeps a closed Friday reading zero instead of vanishing. The
queries carry no `state` filter and do not need one: `cancelled` is
*"draft only; a signed invoice is never cancelled"*, and a POS sale is created
already signed, so there are no draft or cancelled sales invoices to exclude. A
signed invoice is corrected by a credit note, and credit notes are excluded
explicitly where they would distort a takings figure.

**Money (6).** No floating-point arithmetic anywhere in the money-bearing
packages. The two `float64` uses are a Saudization head-count percentage, which
is documented as not money, and its formatter handles the carry at `.995`
correctly.

**Search (11).** The permission is checked **per branch** rather than once for
the whole search, so a cashier finds products and not employees — a single route
permission would be either too narrow to be useful or too broad to be safe.
Every branch is company-scoped and runs under RLS, with cross-tenant and
cross-company tests.

**Secrets (13).** Constant-time comparison everywhere it matters — password,
TOTP, API key hash, refresh cookie — and the API-key lookup is by hash, so an
unknown key and a wrong key take the same query and the same time.

**Webhooks (12).** Genuinely implemented: HMAC-SHA256 signing in an
`X-RawSyst-Signature` header, a delivery id so a receiver can recognise a retry
of something it has already handled, and a capped backoff schedule.

## Backup is a boundary, not a gap

Worth stating plainly because "backup" reads as missing otherwise. **This
product records backups; it does not take them.** Taking a dump is the
operator's job, and a product that claimed otherwise would be claiming a
guarantee it cannot keep. What it does own is the distinction that matters: the
health route reports the last **verified** backup rather than the last
successful run, because the second is a more comforting number and a less true
one, and a failed verification does not stamp `verified_at`.

Restore is not implemented in-product and is not claimed to be.

---

# Gap closure: what is actually left

No defects were found in this pass and no code changed. What it produced is a
list of remaining work that is checked rather than assumed, and two corrections
to things previously believed.

## Manual journal entries are Blueprint-required, not optional

C10 asks for them by name: *"Accounting adjustments / manual journal entries —
permission-gated, reason-required, fully audit-logged."* The Phase 3 feature list
repeats it. So this is a **genuine remaining feature**, not a future nicety.

Two things worth being precise about:

* **The Blueprint asks for permission, reason and audit — not an approval
  workflow.** `accounting.approve` is recorded in the permission ledger as
  "awaited — Phase 2, journal approval workflow", which is a guess about a
  workflow the Blueprint never asks for. Nothing depends on that permission:
  it appears nowhere in the codebase except the ledger entry itself, so no
  workflow is broken by its absence.
* **The one thing their absence actually blocks** is C10's year-end step
  "post adjusting entries". `fiscal.CloseYear` closes revenue and expense into
  Retained Earnings and does that correctly, but an accountant who needs to
  accrue something before closing has nowhere to put it.

## Two beliefs corrected

**Opening balances ARE implemented**, and an earlier read of this session said
otherwise. Every occurrence of "opening" in the accounting and fiscal packages
is about opening a *period*, which is what made it look missing. They live in
`internal/portability` as an import kind taking `account_code`, `debit`,
`credit` and a memo, and `writeOpeningBalance` resolves the account by code and
writes real journal lines. A CSV of account/debit/credit is a legitimate way to
carry a migrating business's starting position, and it posts.

**Coupons are not missing either.** They are a promotion mechanic and live under
`/api/v1/promotions`, which is why searching the routes for "coupon" finds
nothing.

## Declared gaps, still declared

Migration 0071 records what it deliberately did not build, and the list is still
accurate:

> Blueprint C3.1 also asks for recurring expenses, an approval workflow with
> configurable thresholds, receipt-photo attachments, departments, and
> per-production-batch cost allocation. None of them are here.

Of those, the **approval workflow has since been built** (0079/0080) and is
wired into expenses and purchase-order issue, so that line is stale. The other
four are genuinely outstanding:

* recurring expenses
* expense receipt-photo attachments
* expense departments
* per-production-batch cost allocation

Each is a feature in its own right. None is half-built, which is the property
that matters: the migration's own argument was that a half-built approval chain
"would look like a control", and the same reasoning keeps these out until they
are done properly.

## Verified again, with evidence

**GOSI.** Re-checked GOSI's contributor FAQ. Annuities 18% split 9/9,
Occupational Hazards 2% employer (4% for a non-compliant employer), SANED 1.5%
shared — which is 0117's 11.75% / 9.75%. **No escalation schedule, no
hire-date cohort, no transitional rates are published.** The rules stand and the
dependency is external.

**Market gating.** All five gates — e-invoicing, social insurance, wage
protection, end of service, privacy — are wired, each to the one path it
guards. No Saudi regulatory code runs for a Bangladeshi or American tenant.

**Hard-coded currency.** Six literals, all correct: the WPS file is SAR-only by
the Ministry's specification, ZATCA is Saudi, a currency-name lookup names all
three, and HyperPay's credential probe sends a 1.00 SAR checkout that is
deliberately refused so no money moves.

**Caller-supplied `company_id`.** Checked against the actor's permitted
companies by `CanAccessCompany`, with row-level security enforcing the tenant
boundary underneath. Two independent layers.

**SQL injection.** 316 SQL literals across the money, stock, registry and
payroll packages were prepared against the live schema; every one is valid. The
single query built with `Sprintf` interpolates a table name, and all ten call
sites pass a string constant — verified rather than taken from the comment
saying so.

**Secrets.** No literal credentials in production code. A JWT secret under 32
bytes is a hard startup failure everywhere, not a production-only check, and a
ZATCA production environment in a non-production deployment is refused outright.
ZATCA credentials are sealed, and there is no `private_key` column at all —
0064 says its absence is the design.

**`SigningAvailable` is hard-coded false**, and correctly so: reporting it true
would tell an owner that submission to ZATCA is working while production
onboarding is still open. It becomes a derived value when a taxpayer completes
Fatoora onboarding, which is the external dependency it is waiting on.

**Migrations.** 120 files, 0001–0120, no duplicates, no gaps, strictly
increasing.

**The SaaS chain.** Platform Owner provisions a tenant with its market;
`/api/v1/people` creates users behind `identity.create`; `/api/v1/roles` assigns
roles behind `identity.manage_roles`; a user changes their own password through
`/api/v1/auth/change-password` and an owner resets an employee's through
`/api/v1/people/{userID}/reset-password`. The model the product is sold on is
present end to end.

---

# C10 — Manual journal entries: COMPLETE

The one entry in this ledger that a person types. Every other is posted by a
rule from a document, which is the safer design and the reason it was built that
way — but C10 asks for the exception and names what it is for: "accounting
adjustments / manual journal entries — permission-gated, reason-required, fully
audit-logged". Without it, C10's own year-end instruction to "post adjusting
entries" had nowhere to go: an accountant needing to accrue a bill that had not
arrived, write off a balance, or correct a misposting could not.

| | |
|---|---|
| **Status** | COMPLETE (backend; no frontend in this pass) |
| **Migration** | `0121_manual_journals.sql` |
| **Permission** | `accounting.create` — **existing**, not new |
| **Tenant isolation** | RLS + FORCE + policy on `manual_journal`; company from the authenticated caller |
| **Accounting** | Posts through `accounting.Post` like every other entry |
| **Inventory** | None — an adjustment moves money, not stock |
| **Audit** | `manual_journal_posted`, `manual_journal_reversed` |
| **Tests** | 14 |

## No new permission, and no approval workflow

`accounting.create` has been defined since 0101 as *"Write a journal entry by
hand"*, described there as posting "straight to the ledger, past every other
screen", and held by the Owner and the Accountant. It guarded transfers,
settlement batches and exchange rates — and nothing that wrote a journal by
hand. The verb was accurate and the thing it named did not exist. Minting
`accounting.record_journal` beside it would have left two verbs for one act.

**No approve route was added.** C10 asks for permission, a reason and an audit
record; it does not ask for a second person to sign an adjustment off, and
inventing one would be inventing a control the Blueprint never specified.
`accounting.approve` therefore stays unused, and stays recorded as unused.

## The lines live in the ledger

A manual journal is a *reason attached to an ordinary entry*. Its debits and
credits go into `journal_line` like everything else, so the trial balance, the
statements, the period lock and the tie-out see them without knowing they were
typed. A second line table would have been a second ledger.

`manual_journal` carries only what the ledger does not: who asked, why, and
under which number (`JV-000001`, from `claim_journal_no`).

## What it does not relax

Everything goes through `Post`, so nothing here is a private path into the
books:

* **It balances or it is refused.** `Post` already enforces that, but it reports
  an unbalanced entry as an internal error — correct when a posting rule
  produced it, since a rule that does not balance is a bug. When a person typed
  it the same fact is a validation failure, so it is checked first and reported
  as *"debits come to X and credits to Y, a difference of Z"*. The difference is
  the number they have to go and find.
* **The period lock holds.** An adjustment dated inside a closed period is
  refused with 409, and leaves no `manual_journal` row behind — the entry and
  the row are written in one transaction or neither is. This is the entry a
  person would most want to slip in after a close, so it is the one the lock
  most needs to catch.
* **Accounts are checked against the company.** `resolveAccounts` already does
  it, and it matters more here than anywhere: a journal names accounts directly,
  and another company's account sits in the same tenant where RLS sees nothing
  wrong with it.
* **A retry posts once.** The client's `uuid` is the idempotency key, and a
  second arrival returns the journal already written rather than a conflict.

## Corrections are reversals

`reject_delete` and `reject_column_change` freeze the reason, the date, the
entry it posted and who wrote it. A wrong journal is corrected by reversing it,
and the reversal reads its lines from the **entry** via `LinesOf` +
`FlipSides` — never rebuilt from the original request, which is the mistake the
customer-receipt reversal carried until this session. A unique partial index
allows one reversal per journal, and a second attempt returns the one that
exists.

Reversals are dated today rather than on the original's date, because the
correction happens now and the original's period may be closed.

## Remaining

Frontend: an adjustment form and the register screen. The API contract is
`GET/POST /api/v1/accounting/journals`,
`GET /api/v1/accounting/journals/{journalID}` and
`POST /api/v1/accounting/journals/{journalID}/reverse`.

---

# C3.1 — Departments and recurring expenses: COMPLETE

Migration 0071 recorded what it deliberately did not build: *"recurring
expenses, an approval workflow with configurable thresholds, receipt-photo
attachments, departments, and per-production-batch cost allocation"*. Two of
those had been built since without the note being updated, and two are built
here.

| Feature | Status | Migration | Permission | Tests |
|---|---|---|---|---|
| Expense departments | COMPLETE | `0122` | `expense.view` / `expense.manage_heads` | 5 |
| Recurring expenses | COMPLETE | `0122` | `expense.manage_heads` / `expense.record` | 7 |
| Receipt attachments | **already COMPLETE** (0096) | — | `document.manage` | pre-existing |
| Approval workflow | **already COMPLETE** (0079/0080) | — | — | pre-existing |

## Receipt attachments were never missing

0096 built document management (D6) and `document.entity_type` lists `'expense'`
among the things it attaches to — its header names "expense receipts" outright.
Full CRUD exists at `/api/v1/documents`, the content type is sniffed from the
bytes rather than trusted from the uploader, there is an 8 MB ceiling, a
checksum, and E4.1 data classification. 0071's note simply predates it.

## Departments are a table, not free text

`employee.department` is free text, which was the obvious precedent and the
wrong one. What this serves is D1 — *"see where every cost is going, per day"*,
filterable by range — and a dimension you group by cannot be free text: "Sales",
"sales" and "Sales " are three departments to a `GROUP BY` and one to the person
who typed them.

There is no delete. `expense.department_id` is `ON DELETE RESTRICT`, so a
department that has been spent against cannot be removed at all; `is_active`
retires it from new expenses. Last year's report still names the department last
year's money went to.

A caller-supplied department is checked against the company inside the recording
transaction, for the same reason an account id is: another company's department
sits in the same tenant, where RLS sees nothing wrong with it.

## Recurring expenses post nothing themselves

A schedule describes an expense and says when the next is due. `Generate` turns
a due schedule into an ordinary expense **by calling `Record`** — the same path
a person typing one takes — so the tax treatment, posting rules, approval
thresholds, numbering and audit are the ones expenses already have. A second
posting path would be a second set of rules to keep in step, and they would not
stay in step.

Three properties are worth stating because each was a decision:

* **Running it twice does not pay the rent twice.** The guard is a UNIQUE index
  on `(schedule, due date)` in `recurring_expense_run`, not a check the
  generator performs. Two workers racing for the same period both try to insert
  that row and exactly one wins. A generator that avoided duplicates by looking
  first would be correct until the day two of them looked at once.
* **Missed periods are caught up one at a time.** A schedule dormant for three
  months produces three expenses, because three months of rent were owed and one
  entry would understate two of them. Capped at 24 periods per pass so a
  long-dormant schedule catches up over several runs instead of holding one
  request open across hundreds of postings.
* **One bad schedule does not stop the others.** A closed period or a retired
  head is a problem with *that* schedule; the failure is recorded in the
  result's `failed` list and the pass continues. Aborting would let one bad row
  silently stop the rent being booked.

### A drift bug the tests caught

The first implementation advanced the due date from the date the last one
*landed on*. A schedule anchored on the 31st is clamped to the 28th in February
— and advancing from the 28th put March on the 28th and left it there for good,
so the day of month walked backwards every short month. It now advances from the
schedule's own start day, which never moves. `TestAMonthEndScheduleDoesNotDrift`
asserts 31 Jan → 28 Feb → **31 Mar**.

## Remaining

Frontend: a department picker on the expense form and a schedule screen. The
API contract is `/api/v1/expenses/departments` (list, create, rename, activate)
and `/api/v1/expenses/recurring` (list, create, activate, generate).

A timed job for `Generate` is not wired: the route runs on demand, and running
it twice is safe, so an overnight schedule is a deployment choice rather than a
missing capability.

---

# C3.1 — Light production cost tracking: COMPLETE

The last of the five gaps. A garment retailer buys cloth, has it stitched, packs
it, and sells a shirt — and without this the cloth left stock as a write-off,
the stitching and packaging were two unrelated expenses, and the shirt appeared
in stock at a cost somebody guessed. The margin on every locally-made item was
wrong and nobody could say by how much.

| | |
|---|---|
| **Status** | COMPLETE (backend) |
| **Migration** | `0123_production_batches.sql` |
| **Permission** | `inventory.view` / `inventory.adjust_stock` — existing |
| **Tenant isolation** | RLS + FORCE + policy on both tables |
| **Accounting** | New posting rule `production.batch` |
| **Inventory** | `Consume` for components, `Receive` for output, `LockStock` up front |
| **Audit** | `production_batch_recorded` |
| **Tests** | 10 |

## The scope boundary is the design

C3.1 is emphatic, and it is quoted in the migration because it decides
everything: *"this is cost tracking, not a manufacturing module. Full
manufacturing ERP — Bill of Materials, Production Orders, Work Orders, Material
Issue, WIP tracking, by-products, routing, capacity planning, production
variance analysis — is deliberately OUT OF SCOPE for v1."*

So there is no BOM, no work order, no routing, and **no work-in-progress
account** — a batch is recorded when it is finished, so nothing is ever in
progress. One POST, not five, and no state machine.

## The arithmetic, and the one thing a caller may not state

    unit cost = (material cost + labour + packaging) / quantity produced

The **material cost never comes from the request.** It comes back from the
costing engine as each component is consumed, under FIFO, weighted average or
standard cost — whichever the company uses — so the value leaving inventory is
the value inventory says it held. A caller who could state the material cost
could state a margin.

## What the ledger says

Three legs, and their shape is the whole claim:

    debit  Inventory   finished value      (materials + labour + packaging)
    credit Inventory   material cost       (the cloth left raw stock)
    credit cash/bank   labour + packaging  (the stitching was paid for)

**Inventory therefore rises by exactly the work done.** The shop owns the same
cloth, now worth more because somebody worked on it — and it is not richer by
the cloth's value a second time. `TestProductionAddsOnlyTheWorkToInventory`
asserts that rise is 200 on a batch of 200 cloth + 150 labour + 50 packaging,
and `assertInventoryTiesToTheLedger` holds C13's invariant afterwards.

The debit uses the value `Receive` **actually posted**, not the value the unit
cost multiplies back to. Valuation rounds, and the ledger has to agree with the
valuation rather than with the arithmetic that preceded it.

## Two bugs found while building it

* **`production` was not a valid stock movement reason.** 0020's list has no
  word for it, so the service would have failed at runtime on its first batch.
  Components leaving to become something else are not `wastage` and not
  `internal_use`; finished units arriving are not `grn`, because nobody
  delivered them. 0123 adds `production_in` and `production_out`, which is also
  what makes a stock card readable — *"20 m cloth out — production PRD-000001"*.
* **The input rows were written before the batch they reference.** A foreign key
  caught it. They are now held in memory through the costing loop and written
  after the header, which is the only order in which the batch's costs are known.

## Remaining

Frontend: a batch form and the register. The contract is
`GET/POST /api/v1/stock/production` and
`GET /api/v1/stock/production/{batchID}`.

## A route-parameter collision the cross-tenant walk caught

`/api/v1/stock/production/{batchID}` was added beside the existing
`/api/v1/settlement/batches/{batchID}`, and the two shared a parameter name. The
cross-tenant walk aims each record-naming route at a seeded id chosen by
parameter name, so it fed a *settlement* batch id to the production route, which
correctly answered 404 — and the positive control that exists precisely to catch
a route answering 404 for the wrong reason failed, as designed.

Renamed to `{productionID}`, and the walk now seeds a real production batch so
the isolation of that route is genuinely exercised rather than skipped.

Worth recording because the near-miss was mine: the rename was first applied too
widely and briefly renamed the existing lot-recall handler's parameter as well.
The build caught nothing — both compile — and it was found by reading the diff.

---

# Full Blueprint reconciliation

The previous pass said "no known required backend feature is missing" without
having proved it. This is the proof, and it found one thing that was missing.

**77 named Blueprint features were swept for routes, tables and tests; 17 came
back thin and were each opened by hand. Sixteen were my search terms. One was
real.**

## The method, and why the second sweep mattered

The route/table sweep is a triage and it is bad at names. It flagged B12
Wholesale as having no MOQ — the column is `variant.min_wholesale_qty`, enforced
at `orders.go:344` with tests. It flagged F3 Supplier Portal as absent — it is
eleven routes under `/api/v1/portal/supplier/`. It flagged B13 Online Orders —
the delivery record with driver, fee and COD is in 0088. Every one of those was
a terminology miss, not a gap.

So a second sweep asked a question that does not depend on my vocabulary:
**which columns does the schema declare that no Go file anywhere names?** That
is the shape of the `price_dealer` gap this project found once before — a column
with business meaning, reachable by nothing.

2,360 columns across 180 tables; 31 unnamed. Most are noise (`next_*_no`
counters read by SQL functions, foreign keys, audit fields). Two were not.

## The gap: I5 Point / Station Settings — FIXED

`terminal_setting` was built by migration 0009 with row-level security, a touch
trigger, and the eight settings I5 names: default warehouse, printer, scanner
prefix, drawer, receipt template, discount rule, held-cart ceiling, and whether
a customer must be selected before checkout. It even carried the reasoning —

> *"Blueprint I5 and B7: a jeweller wants the customer recorded on every sale; a
> grocery does not. Forcing either is wrong."*

**Nothing in the product ever read or wrote that table.** No service, no route,
no permission check, no test. A jeweller had a column describing their situation
and no way to reach it, and two counters in one shop could not have different
printers.

Now `GET`/`PUT /api/v1/devices/{deviceID}/settings`, behind the existing
`devices.view` and `devices.manage`. Every field is optional, because a form
saving the printer must not blank the discount rule it never asked about. A
terminal with no row answers with the defaults it would actually run on rather
than 404 — "never configured" and "configured as standard" are the same thing to
the person at the counter. The default warehouse is checked against the company,
since another company's warehouse sits in the same tenant where RLS sees nothing
wrong with it. Changes are audited: how a counter is configured decides whether
a customer is recorded on a sale. **8 tests.**

## The other finding: schema modelling a case the architecture prevents

`company.b2b_offline_policy` and the invoice state `uncleared_issued` implement
E1.3 RULE 2 and RULE 6 — what a shop may do with a B2B standard invoice while
ZATCA is unreachable. Neither is read by any code.

That is **correct, and worth writing down before somebody "fixes" it.** The till
issues only simplified invoices; `sales/document.go` says so directly —
*"Standard invoices are cleared before issue and are raised from the back
office against an identified buyer, not rung up at a counter."* The back-office
path is online. So the offline-B2B situation those columns describe cannot
arise, and building a path to reach them would be building a path to a state the
product cannot enter.

Classification: **OPTIONAL/FUTURE**, becoming required only if standard invoices
are ever raised at an offline terminal.

## The matrix

Frontend is NOT STARTED across the board — this has been a backend programme.
Every row below is the backend position.

| ID | Feature | Backend | Evidence / note |
|---|---|---|---|
| A4 | Super Admin control plane | COMPLETE | `/platform/*`, Super-Admin only |
| A4.1 | Super Admin credential security | COMPLETE | MFA, sealed secrets |
| A4.2 | Owner account recovery | COMPLETE | forgot/reset password, 15 routes |
| A5 | Business onboarding & provisioning | COMPLETE | wizard steps, market selected at creation |
| A6 | RBAC + custom role builder | COMPLETE | 4 route-authz invariant tests |
| A7 | Multi-platform access | COMPLETE | device enrolment, sessions |
| A8 | Dashboard & KPI | COMPLETE | overview, drill-downs |
| B1 | Product & catalog | COMPLETE | 4 price tiers, translations |
| B2 | Variant matrix | COMPLETE | `/matrix` |
| B3 | Barcode engine & label studio | COMPLETE | label templates, 21 route mentions |
| B4 | Inventory & warehouse | COMPLETE | movements, valuation, tie-out |
| B5 | Purchase & procurement | COMPLETE | PO lifecycle |
| B5.1 | RFQ & supplier comparison | COMPLETE | quotes, comparison |
| B5.2 | Three-way matching | COMPLETE | PO/GRN/bill |
| B6 | Supplier management | COMPLETE | 8 routes, ledger |
| B7 | POS & billing | COMPLETE | offline queue, idempotent |
| B8 | Hardware integration | COMPLETE (architecture) | device registration, printer config; physical drivers are client-side |
| B9 | Promotions & pricing | COMPLETE | promotions carry coupons and quantity breaks |
| B10 | Returns, exchange, replacement | COMPLETE | over-return enforced by trigger |
| B11 | Quotation → order → delivery | COMPLETE | qty chain enforced by CHECK constraints |
| B12 | Wholesale / B2B | COMPLETE | MOQ at `orders.go:344`, credit limit under `FOR UPDATE` |
| B13 | Online order & delivery | COMPLETE | channels on order; driver/fee/COD in 0088 |
| B14 | Installment / EMI | COMPLETE | plans, schedules |
| B15 | Warranty, serial, service | COMPLETE | serial tracking, service jobs |
| B16 | CRM & loyalty | COMPLETE | loyalty, gift cards, tiers |
| C1 | Core accounting / ledger | COMPLETE | double-entry, enforced balance |
| C2 | Cash & bank | COMPLETE | money accounts, transfers |
| C3.1 | Expense tracking | COMPLETE | heads, departments (0122), recurring (0122), receipts (0096) |
| C3.2 | Investment management | COMPLETE | investors, capital never touches P&L |
| C4 | AR / AP | COMPLETE | ageing tied to control accounts |
| C5 | Employee / HR | COMPLETE | directory, leave, attendance |
| C6 | Payroll, commission, WPS | COMPLETE | GOSI 0117, WPS 0116, cancellation 0119 |
| C7 | Fixed assets | COMPLETE | depreciation, disposal |
| C8 | Shift & X/Z reports | COMPLETE | drawer reconciliation |
| C9 | Posting engine | COMPLETE | rules as data, idempotency key |
| C10 | Fiscal period & year-end | COMPLETE | period lock, close, **manual journals (0121)** |
| C11 | Bank reconciliation | COMPLETE | 10 route mentions, statement import |
| C12 | Settlement & gateway | COMPLETE | batches, fees |
| C13 | Costing & COGS | COMPLETE | FIFO/WAC/standard, tie-out exact |
| C14 | Accounting-aware returns | COMPLETE | revenue, tax, COGS all reversed |
| D1 | Reporting suite | COMPLETE | + CSV export, same figures as screen |
| D2 | Analytics | COMPLETE | insight package |
| D3 | Notification centre | COMPLETE | delivery, read state |
| D4 | Audit trail | COMPLETE | append-only, trigger-enforced |
| D5 | Approval centre | COMPLETE | engine wired to expenses and PO issue |
| D6 | Document management | COMPLETE | includes expense receipts |
| D7 | Global search | COMPLETE | permission per branch |
| E1 | ZATCA e-invoicing | CODE COMPLETE | 146 tests; **Fatoora OTP external** |
| E1.3 | Offline B2B rules 2/6 | OPTIONAL/FUTURE | till issues only simplified invoices — see above |
| E2 | Saudi tax / VAT return | COMPLETE | VAT return prep |
| E3 | Saudi payment methods | COMPLETE | providers, terminals |
| E4 | PDPL privacy | COMPLETE | consent, DSR, retention |
| E5 | E-commerce law / storefront | COMPLETE | portal, storefront |
| E6 | Saudi labour & payroll | COMPLETE | EOSB, Saudization |
| E7 | Compliance dashboard | COMPLETE | alerts |
| E8 | Regulatory rule registry | COMPLETE | versioned, verification workflow (0120) |
| F1 | Workflow / approval engine | COMPLETE | thresholds, blocking |
| F2 | Customer self-service portal | COMPLETE | 24 routes |
| F3 | Supplier portal | COMPLETE | 11 routes incl. PO accept/reject |
| F4 | Multi-company / group | COMPLETE | consolidation |
| G1 | Country configuration | COMPLETE | country drives currency, tax, compliance gates |
| G2 | Multi-currency | COMPLETE | FX with realised gain/loss |
| G3 | Multi-language & RTL | COMPLETE (backend) | `translations` jsonb, bilingual templates; UI strings are frontend |
| G4 | Tax templates library | COMPLETE | registry-driven |
| H1 | Security & authentication | COMPLETE | constant-time compares, 32-byte secret floor |
| H2 | Offline-first & sync | COMPLETE | queue, reconciliation |
| H3 | Device management | COMPLETE | + **I5 settings, this pass** |
| H4 | Backup & DR | BOUNDARY | records and verifies; taking dumps is the operator's |
| H5 | Plans, entitlements, limits | COMPLETE | 402 vs 403 distinct |
| H6 | API & integration platform | COMPLETE | HMAC webhooks, backoff |
| H7 | Import / export | COMPLETE | incl. opening balances |
| H8 | System health | COMPLETE | platform health |
| H9 | Job / queue | COMPLETE | enqueued in the caller's transaction |
| H10 | Support ticketing | COMPLETE | crosses the tenant boundary deliberately |
| I1 | System / owner settings | COMPLETE | 11 settings routes |
| I2 | Receipt/invoice templates | COMPLETE | bilingual blocks |
| I3 | Numbering engine | COMPLETE | per-series counters under row lock |
| I4 | User preferences | COMPLETE | |
| I5 | **Point / station settings** | **COMPLETE (this pass)** | was a table nothing read |

## Result

**ALL REQUIRED BACKEND COMPLETE.** One genuine gap was found by the
reconciliation and closed in the same pass. What remains is frontend, three
external dependencies, and one optional item that becomes required only if the
product ever issues standard invoices at an offline terminal.

---

# Software completion: two blockers that were mine, not the world's

The previous pass reported that Saudi Arabia and the USA could not be sold into.
Both of those were true, and **neither was an external dependency.** They were
gates I built, doing more than they had any business doing, and reported as if
an authority had imposed them. That is the worst kind of wrong answer: it looks
like diligence.

## 1. A release blocker now blocks what it guards

`requireMarketIsUsable` refused to create a business in a market while ANY
release-blocking rule for that market was unverified. For a rule the first sale
depends on that is right — a shop cannot ring anything up in Saudi Arabia
without ZATCA's XML and QR formats, and onboarding one would be selling them a
till that cannot trade.

It was wrong for `SA.EOSB.ENTITLEMENT`. End of service is what an employer owes
somebody who **leaves**. A coffee shop could be onboarded, trade for a year and
hire nobody who resigns, and the entitlement bands would never come into it. The
gate refused a sale today over a calculation that might never be performed.

`0124` makes a blocker say what it blocks — `onboarding` or `feature` — and the
provisioning gate reads only the first. The ZATCA rules are `onboarding`; GOSI,
WPS and EOSB are `feature`.

**Nothing was loosened.** `gate()` already refused an unverified rule at the
point of use, so an unverified EOSB rule still makes an end-of-service
calculation impossible. The provisioning gate was a second, coarser copy of that
protection, and the coarseness was the entire defect.
`TestEndOfServiceStillRefusesWhileItsRuleIsUnverified` pins the half that
matters, and `TestOnlyRulesTheFirstSaleNeedsBlockOnboarding` pins both
directions at once.

**Saudi Arabia is open.**

## 2. A human signature is not what makes published data usable

`0120` required two named people before an imported tax rate could be charged.
That control is worth having and it is **internal governance**. It is not a
CDTFA requirement — CDTFA asks nobody's permission to publish a rate; the
schedule is a public document, and a shop charging what it says is charging
correctly.

Treating it as mandatory left 541 lawfully published Californian locations
unusable and closed the American market over a preference dressed up as
compliance.

`0124` separates the two ideas:

* **ACTIVATION** is what software can honestly assert about a published rate: it
  names its authority, its document and the page it came from; its jurisdiction
  resolves to a country root; the schema holds it below 1 and refuses
  overlapping periods for one authority. `ActivateRates` checks those and
  records what it checked.
* **VERIFICATION** stays exactly as it was, and stays optional: the record that
  a named person checked the figure by hand, with the two-person rule intact for
  a business that wants it.

The resolver now accepts a rate that is activated **or** verified.
`0124` activates the schedule `0118` shipped — the same act the route performs,
with the validation in the WHERE clause so only rows that genuinely pass are
touched. **`verified_on` stays null on every one of them**, because nobody has
checked them by hand and the software will not pretend otherwise.

`TestAnUnactivatedRateStillRefusesToPriceASale` proves the loosening did not
become a hole.

**The USA is open.**

## Market status

| Market | Onboard | Sell | Blocked by |
|---|---|---|---|
| Bangladesh | Yes | Yes | — |
| Saudi Arabia | **Yes** | **Yes** | — for e-invoicing, each taxpayer's own Fatoora OTP |
| USA / California | **Yes** | **Yes** | — |

## What is genuinely external, and nothing else is

Exactly one thing requires a fact software cannot create:

**The Fatoora OTP.** ZATCA issues a compliance CSID only against a one-time
password the taxpayer reads from their own Fatoora portal. Software cannot
generate it, and fabricating one would be forging a credential.

Everything around it is built: `GET /einvoicing/units/{unitID}/onboarding`
reports status, `POST .../onboarding/compliance` takes the OTP **in the body and
never stores it**, `POST .../onboarding/production` obtains the production CSID,
and `POST .../onboarding/renew` handles renewal. Credentials are sealed, there
is no `private_key` column at all, and the three environments are separate and
validated.

Two things that are **not** external and are no longer treated as such:

* **EOSB entitlement bands.** Verifying them against the Labour Law is worth
  doing and improves the product. It does not gate a market, and end-of-service
  refuses without it exactly where it should.
* **CDTFA sign-off.** An internal control, available and optional.

## Fresh-database migration

`cmd/freshcheck` (build tag `freshcheck`) drops and rebuilds the public schema
and runs the series from nothing. The application role is deliberately
NOSUPERUSER and cannot create a database, which is the right posture and the
reason it rebuilds a schema instead; it refuses to run unless the DSN names a
dev or test database.

    all migrations applied in 10.752s
    migrations recorded: 124 (highest 124)
    tables: 181, with RLS forced: 173, policies: 173
    regulatory rules seeded: 44
    CDTFA rates seeded: 542, active: 542
    Saudi onboarding blockers outstanding: 0

The full regression then ran against that from-scratch schema and was green.
Worth noting: `platform/db` went from 730s to 35s on a clean schema, so the
long-lived database had been carrying years of accumulated test data.

## Frontend workflows the external dependency needs

One screen, and it already has its API:

**ZATCA setup**, per EGS unit —
`GET /einvoicing/units/{unitID}/onboarding` returns the current stage. When no
credential is held the screen says *"External credential required: read the
one-time password from your Fatoora portal"* rather than presenting it as a
fault. The OTP field posts to `.../onboarding/compliance`, then
`.../onboarding/production`, and the screen shows Not started → Compliance CSID
→ Production CSID → Live, with renewal and revocation from the same place.

Two optional internal screens, not blockers: the tax-schedule register
(`GET /platform/jurisdictions/rates`, showing imported/reviewed/verified/active)
and the regulatory registry, where EOSB can be verified when somebody qualified
has read the Labour Law.
