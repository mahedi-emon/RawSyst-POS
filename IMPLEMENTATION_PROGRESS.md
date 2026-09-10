# RawSyst — Implementation Progress

Session-continuity file. **Long-form history lives in
[`docs/PROJECT-STATUS.md`](docs/PROJECT-STATUS.md)** — this file is the short
answer to "what is true right now and what do I do next", so the two do not
duplicate each other. Serena memories `audit/verified-2026-09-02-directive` and
`architecture/counter-and-market` carry the reasoning.

| | |
|---|---|
| **Last verified** | 2026-09-10 |
| **Branch** | `main`. The feature branch was merged and deleted on 2026-09-10; work lands on `main`. |
| **Scale** | 134 migrations · 509 routes · 110 permissions · 1,230+ Go test functions |
| **Software development** | **COMPLETE.** No feature is unbuilt, disabled, unreachable, ungated or waiting for a developer. Zero `TODO`, `FIXME`, `not implemented` or `coming soon` in `backend`, `web-next/src`, `shared/src`, `pos/src` or `scripts`. Zero reachability gaps. |
| **Regulatory data** | **Ingested and active.** `SA.EOSB.ENTITLEMENT` holds the figures Articles 84 and 85 state, read out of the Ministry of Human Resources and Social Development's own publication of the Labour Law, retrieved and hashed by the product, with the sentence behind every value on record. Nothing is awaiting a figure. |
| **External dependency** | The Fatoora one-time password. ZATCA issues a compliance certificate only against a password the taxpayer reads from their own portal. The whole workflow around it is built. |
| **Deployment** | Sized for the production server it goes on: 2 vCPU, 3.7 GiB, 48 GB, Ubuntu 24.04. `docker-compose.server.yml` plus `deploy/server/`. Images 296 MB for both services, down from 438 MB. **Not yet deployed — this repository has no access to that machine.** |
| **Backups** | Off-server, to any S3-compatible store, verified by restoring into a temporary database every night. `deploy/server/BACKUP.md` and `MIGRATION.md`. |
| **Direction** | **Greenfield front end in `web-next/` — see section 0.** Web first. POS is a module inside the web app. **ZATCA skipped and isolated.** Tauri deferred. |

**Two categories, deliberately kept apart.** Software completeness and
regulatory-data availability are different conditions with different remedies,
and this document ran them together until 2026-09-10. Calling missing data a
software blocker made a finished engine look unbuilt and stopped a deployment
that had nothing wrong with it; calling missing software a data problem would
leave a hole nobody was looking for. The last section of this file, *END OF
SERVICE: THE SOFTWARE IS FINISHED*, is where they were separated and what was
genuinely unfinished got closed.

**Earlier sections are a chronological log and are not corrected in place.**
Where one of them describes end of service, social insurance or the wage file as
outstanding software, the last section is the current answer. Two claims in
particular have been overtaken: the *Genuine blockers* section names
`SA.WPS.WAGE_FILE_FORMAT` as unverified, and 0116 recorded the Ministry's
published wage-file layout; and the *EOSB — unchanged, and deliberately* section
was accurate on the day it was written and is not now.

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
| FE-18 | Returns / exchanges | ✅ | IN PROGRESS | `/pos/returns` | `sales.refund` | |
| FE-19 | Shifts, cash drop, X/Z | ✅ | IN PROGRESS | `/shifts` | `sales.receive_payment` | Opening is done inside the till |
| FE-20 | Customers + ledger | ✅ | IN PROGRESS | `/customers`, `/customers/{id}` | `customers.view` | List and statement built and verified live. The khata carries a running balance and a double-ruled closing row; unpaid invoices flag overdue. Create/edit and the credit-limit control (its own permission, `customers.set_credit_limit`) not started |
| FE-21 | Stock on hand | ✅ | IN PROGRESS | `/stock` | `inventory.view` | Search, location filter, and the server's `low` filter. **Closes the dashboard's dead link.** Movements and counts not started |
| FE-22 | Stock transfers (approve/dispatch/receive) | ✅ | IN PROGRESS | `/stock/transfers` | `inventory.approve_transfer` | **No frontend caller** |
| FE-23 | Batch / expiry / recall | ✅ | IN PROGRESS | `/stock/batches` | `inventory.recall_batch` | **No frontend caller** |
| FE-24 | Production orders | ✅ | IN PROGRESS | `/stock/production` | `inventory.adjust_stock` | **No frontend caller** |
| FE-25 | Purchasing (31 routes) | ✅ | IN PROGRESS | `/buying/*` | `purchasing.*` | Largest single module |
| FE-26 | Expenses + departments + recurring | ✅ | IN PROGRESS | `/money/expenses` | `expense.view` | Departments and recurring have **no frontend caller** |
| FE-27 | Manual journals | ✅ | IN PROGRESS | `/money/journals` | `accounting.create` | **No frontend caller** |
| FE-28 | Treasury / reconciliation | ✅ | IN PROGRESS | `/money/accounts` | `accounting.reconcile` | |
| FE-29 | Payroll / employees / EOSB / WPS | ✅ | IN PROGRESS | `/people/*` | `payroll.*`, `hr.*` | Plan-gated `payroll` |
| FE-30 | Users, roles, permission builder | ✅ | IN PROGRESS | `/people/users` | `identity.manage_roles` | `GET /permissions` returns the catalogue with `holds` and `label_ar`/`label_bn` |
| FE-31 | Financial statements + VAT return | ✅ | IN PROGRESS | `/reports/*` | `accounting.view` | |
| FE-32 | Orders → invoice | ✅ | IN PROGRESS | `/orders` | `order.view` | `POST /orders/{id}/invoice` has **no frontend caller** |
| FE-33 | ZATCA / e-invoicing onboarding | ✅ | 🚧 BLOCKED | `/settings/einvoicing` | `einvoicing.onboard` | Direction says ZATCA is skipped and isolated |
| FE-34 | Platform: businesses, billing, dunning | ✅ | IN PROGRESS | `/platform/businesses` | super-admin | |
| FE-35 | Platform: rules, jurisdictions, tax rates | ✅ | IN PROGRESS | `/platform/rules` | super-admin | Imported → reviewed → activated → verified |
| FE-36 | Global search | ✅ | IN PROGRESS | `/search` + header box | authenticated | Built. Grouped by the seven kinds the route returns; each is gated by the permission guarding the thing it finds, so the empty state says "nothing you can see", not "no results" |
| FE-37 | Approvals | ✅ | IN PROGRESS | `/approvals` | `approval.view` | Plan-gated |
| FE-38 | Privacy (24 routes) | ✅ | **COMPLETE** | `/oversight/privacy` | `privacy.view` | §0.108. Six registers behind tabs, led by the two statutory clocks. `days_left` and `hours_left` are the server's and are never recomputed. |
| FE-39 | Business details / onboarding wizard | ✅ | IN PROGRESS | `/settings/business` | `identity.edit` | **Blocks Saudi selling** until the branch National Address is complete |
| FE-40 | Barcodes and label studio | ✅ | **COMPLETE** | `/products/labels` | `label.print` | Plan-gated. §0.107. The nav asked for print-or-manage; a manage-only role would have been refused on the screen's first read. |
| FE-41 | POS shift close, cash drop, X/Z | ✅ | IN PROGRESS | `/pos`, `/shifts` | `sales.receive_payment` | |
| FE-42 | Promotions | ✅ | IN PROGRESS | `/promotions` | `promotion.view` | Plan-gated |
| FE-43 | Deliveries | ✅ | IN PROGRESS | `/deliveries` | `delivery.view` | Plan-gated |
| FE-44 | Instalment plans | ✅ | IN PROGRESS | `/money/installments` | `installment.view` | Plan-gated |
| FE-45 | Service jobs / serials / warranty | ✅ | IN PROGRESS | `/aftersales/*`, `/stock/serials` | `service.view`, `serial.view` | Plan-gated. Serials and warranty COMPLETE (§0.107): four warranty states, the server's `under_warranty` never recomputed. Service jobs are a separate lane. |
| FE-46 | Loyalty, wallets, gift cards | ✅ | IN PROGRESS | `/customers/loyalty` | `loyalty.view` | Plan-gated |
| FE-47 | Investors | ✅ | IN PROGRESS | `/money/investors` | `investor.view` | Plan-gated |
| FE-48 | Fixed assets | ✅ | IN PROGRESS | `/money/assets` | `asset.view` | Plan-gated |
| FE-49 | Exchange rates / FX | ✅ | IN PROGRESS | `/money/accounts` | `accounting.view` | |
| FE-50 | Accounting periods / year-end | ✅ | IN PROGRESS | `/money/periods` | `accounting.close_period` | |
| FE-51 | Gateways + settlement | ✅ | **COMPLETE** | `/money/gateways` | `gateway.view` + `accounting.view` | §0.108. Two permissions on one screen: the connections are gateway.view, the deposits accounting.view. Never-checked, answering and not-answering are three states, not two. |
| FE-52 | Analytics / forecast | ✅ | IN PROGRESS | `/reports/analytics` | `report.view` | Plan-gated |
| FE-53 | Notifications | ✅ | IN PROGRESS | `/notifications` | authenticated | Built. Six routes had no caller. Company-scoped: `notifyScope` resolves a company although the route is merely authenticated |
| FE-54 | Audit trail | ✅ | **COMPLETE** | `/oversight/audit` | `accounting.view` | §0.108. The filter is built from the verbs the trail returns with the rows, so it cannot go stale. before/after shown as recorded. |
| FE-55 | Documents | ✅ | **COMPLETE** | `/oversight/documents` | `document.view` | §0.108. A business could not file its own papers at all until this pass; two attachment kinds the schema permits were unreachable. |
| FE-56 | Portal access (supplier logins; customers self-serve by code) | ✅ | IN PROGRESS | `/customers/portal` | `portal.view` | |
| FE-57 | Compliance dashboard | ✅ | IN PROGRESS | `/oversight/compliance` | `compliance.view` | |
| FE-58 | Supplier portal administration | ✅ | IN PROGRESS | `/buying/suppliers` | `portal.manage` | 11 supplier-portal routes exist |
| FE-59 | Group companies / consolidation | ✅ | **COMPLETE** | `/oversight/groups` | `group.view` | Plan-gated, and §0.108 is mostly about that: the dev tenant's plan answers 402, so the commercial refusal is the state that is proved live and the populated screen is built from the service's types. |
| FE-60 | Tills and devices | ✅ | **COMPLETE** | `/settings/devices` | `devices.view` | Market-aware: SA needs an EGS unit. §0.107. `pending` is read against `binding`, so a normal paired till is not reported as a fault. |
| FE-61 | Backups | ✅ | **COMPLETE** | `/oversight/backups` | `backup.view` | §0.108. "Finished" and "verified" are kept apart, and the risk sentence is the server's own. |
| FE-62 | Plan and billing | ✅ | IN PROGRESS | `/settings/subscription` | `subscription.view` | |
| FE-63 | Integrations: API keys, webhooks | ✅ | IN PROGRESS | `/settings/integrations` | `integration.view` | Built. A key is readable once, on mint; the list carries a prefix and never the secret. The permission picker offers only what the caller holds, because an over-grant returns 201 with the excess SILENTLY dropped |
| FE-64 | Import / export | ✅ | **COMPLETE** | `/settings/imports` | `data.import` | §0.108. Staged, checked and committed as three visible acts; the columns come from `/imports/shapes`. |
| FE-65 | Platform: failed jobs | ✅ | IN PROGRESS | `/platform/jobs` | super-admin | |
| FE-66 | Support tickets (both sides) | ✅ | IN PROGRESS | `/settings/support` | `support.raise` | |
| FE-67 | Receipt / invoice templates | ✅ | IN PROGRESS | `/settings/business` | `identity.edit` | |
| FE-68 | Account recovery (forgot / reset password) | ✅ | IN PROGRESS | `/forgot-password` | public | Built. `POST /auth/forgot-password` and `/auth/reset-password` were live and uncalled, and **sign-in had linked here with no page behind it** — a locked-out owner met a dead link. The confirmation is worded conditionally, because the route answers 204 whether or not the address is on file |
| FE-69 | Two-step sign-in and your own sessions | ✅ | IN PROGRESS | `/settings/security` | authenticated | Built. Signing in with a second factor already worked; nothing could enrol one, show recovery codes or end a session. The secret and the codes are each shown once. No user parameter anywhere, so an administrator cannot reach somebody else from here |

### 0.6 GATE 3 — Blueprint traceability

Every top-level Blueprint section has a row. Nothing is collapsed: where one
screen serves several Blueprint IDs, each ID keeps its own row pointing at that
screen. Derived mechanically from the Blueprint's own headings, so no ID can be
lost by hand.

**88 sections · COMPLETE 9 · IN PROGRESS 14 · NOT STARTED 55 · BLOCKED 2 · N/A 8**

(The "77 named features" of the earlier backend sweep are a subset: this table
additionally carries the `J*` architecture sections and the `O*` restatement,
marked N/A or mapped, rather than dropping them.)

**On the status column in this table and in §0.5.** Both drifted badly: a
reconcile pass against `page.tsx` found **61 rows reading NOT STARTED with the
screen sitting on disk** — 34 here in §0.6 and 27 in §0.5 — while the `built`
flags in `navigation.ts` showed zero discrepancies in either direction. The map
was holding and the prose was not.

Those 61 are now IN PROGRESS rather than COMPLETE, deliberately. §0.5 defines
COMPLETE against twelve criteria and "a page renders" is explicitly not one of
them; what has actually been checked for these rows is that a screen exists at
the route. **The completeness judgement lives in one place — *Frontend
reconciliation against the original 77 features* at the end of this document —
which assesses all 77 with the evidence for each.** Keeping a second judgement
in these tables is how the three of them drifted apart, so they are now a map of
what exists and the reconciliation is the assessment of what is done.

| Blueprint | Feature | Frontend item | Route(s) | Backend API | Permissions | Frontend status | Notes |
|---|---|---|---|---|---|---|---|
| A1 | Product Vision | — | — | — | — | N/A | Product vision. Not a screen. |
| A2 | Guiding Principles (non-negotiable, apply to every module below) | — | — | — | — | N/A | Guiding principles. Not a screen. |
| A3 | Multi-Tenant SaaS Architecture | FE-05, FE-09 | (all) | GET /auth/me, GET /companies | — | COMPLETE | Tenancy is resolved from the session; company scope is application state. |
| A4 | Super Admin | FE-15, FE-34, FE-35 | /platform/* | 27 SuperAdmin routes | super-admin | IN PROGRESS | Health done. Businesses, billing, regulatory not started. |
| A5 | Business Owner Account, Onboarding & Provisioning | FE-39 | /settings/business | GET/PUT /onboarding/* | identity.edit | IN PROGRESS | The 7-step wizard. Live validation showed the branch National Address blocks Saudi sales until complete. |
| A6 | Role & Permission Management (RBAC) | FE-06, FE-07, FE-30 | /people/users | GET /permissions, /roles, /people | identity.manage_roles | IN PROGRESS | Guards and permission-aware nav COMPLETE. The role builder screen is not started. |
| A7 | Multi-Platform Client Access | FE-08 | (all) | — | — | COMPLETE | One responsive web app; POS is a module inside it. |
| A8 | Dashboard & KPI Center | FE-11 | /dashboard | GET /dashboard/overview | sales.view ∪ accounting.view ∪ inventory.view | COMPLETE | Attention list first, four drill-through figures. Verified live. |
| B1 | Product & Catalog Management | FE-12 | /products | GET/POST /catalog/products | catalog.view / catalog.create | IN PROGRESS | List verified live and on the shared list component. Create/edit not started. |
| B2 | Product Variant Matrix (critical for Fashion/RMG | FE-16 | /products/{id} | GET/POST /catalog/products/{id}/matrix | catalog.view | IN PROGRESS | The grid is built and verified live; generating a matrix is not |  |
| B3 | Intelligent Barcode Engine & Label Studio | FE-40 | /products/labels | 9 /labels/* routes | label.print / label.manage | COMPLETE | Plan-gated: label_studio. §0.107. The screen prints the server's own `example` rather than assembling a code; a roll reports no per-sheet count. |
| B4 | Inventory & Warehouse Management | FE-21, FE-22, FE-23 | /stock/* | 28 /stock/* routes | inventory.* | IN PROGRESS | Live validation: adjustment kind ∈ {adjustment, wastage}; reason is an enum. Two backend defects fixed here. |
| B5 | Purchase & Procurement Management | FE-25 | /buying/* | 35 /purchasing/* routes | purchasing.* | COMPLETE | §0.88–0.95 and §0.102. Purchase RETURN was missing from the backend entirely — built here: migration 0127, the service, three routes and a new permission. |
| B5.1 | RFQ | FE-25 | /buying/quotes | /purchasing/rfqs/* | purchasing.manage_rfq, purchasing.award_rfq | COMPLETE | §0.95. The four-way split proved live. |
| B5.2 | Three-Way Matching | FE-25 | /buying/bills | /purchasing/bills/* | purchasing.approve_bill | COMPLETE | §0.93. All four dimensions shown, including the ones that passed. |
| B6 | Supplier Management | FE-25 | /buying/suppliers | /purchasing/suppliers | purchasing.manage_suppliers | COMPLETE | §0.88. Retiring one is refused while money is owed. |
| B7 | Point of Sale (POS) & Billing | FE-13, FE-14, FE-41 | /pos | 12 /pos/* routes, /shifts | sales.create | COMPLETE | A real sale posted end to end: counter token, shift, catalogue snapshot, tax split, idempotent replay. |
| B8 | Hardware Integration (Showroom Cash-Counter Reality) | — | /pos | — | — | BLOCKED | Cash drawer, pole display and scales need the desktop till. A browser cannot reach them; scanners work as keyboards and do. |
| B9 | Promotions, Discounts & Pricing Engine | FE-42 | /promotions | /promotions/*, /promotions/quote | promotion.view / promotion.manage | IN PROGRESS | Cart carries promotion_id so redemption is recorded; the quote call is not wired yet. |
| B10 | Sales Returns, Exchange & Replacement | FE-18 | /pos/returns, /pos/exchanges | /pos/returns, /pos/exchanges, /pos/sales/{id}/returnable | sales.refund, sales.exchange | COMPLETE | §0.101. Returns were built in §0.91; exchanges are new. Three defects found live: the till never named a stock location, an idempotent replay came back hollow, and the sidebar offered exchanges to a screen that refused them. |
| B11 | Sales Quotation, Sales Order & Delivery Documentation | FE-32 | /orders | 9 /orders/* routes | order.view / order.manage | IN PROGRESS | POST /orders/{id}/invoice still has no frontend caller. |
| B12 | Wholesale / B2B Module | FE-20 | /customers | /customers, /dashboard/sales | customers.view | IN PROGRESS | Wholesale pricing flows at the till, the customer list marks wholesale accounts, and the sales day splits retail from wholesale so bulk orders do not distort retail figures. |
| B13 | Online Order & Delivery Management | FE-32, FE-43 | /orders, /deliveries | /orders/*, /deliveries/* | order.view, delivery.view | IN PROGRESS | Plan-gated: online_orders. |
| B14 | Installment / EMI (কিস্তি) System | FE-44 | /money/installments | 7 /installments/* routes | installment.view / installment.manage | IN PROGRESS | Plan-gated: installments. |
| B15 | Warranty, Serial/IMEI Tracking & Service/Repair | FE-45 | /aftersales/service, /stock/serials | /service-jobs/*, /serials/* | service.view, serial.view | IN PROGRESS | Plan-gated: warranty. Serial and warranty half COMPLETE (§0.107) — lookup-first, four states, an unknown serial is a plain 404. Service/repair is a separate lane. |
| B16 | Customer Relationship Management (CRM) & Loyalty | FE-20, FE-46 | /customers, /customers/loyalty | 12 /customers/*, 6 /loyalty/* | customers.view, loyalty.view | IN PROGRESS | List and statement built. Loyalty not started |  |
| C1 | Core Accounting (Chart of Accounts, Journal, Ledger) | FE-27 | /money/chart, /money/journals | /accounting/chart, /accounting/journals/* | accounting.view / accounting.create | COMPLETE | §0.104. The chart had NO route at all — added here. Journals: write, read, reverse, all live. |
| C2 | Cash & Bank Management | FE-28 | /money/accounts, /money/transfers | 10 /treasury/* routes | accounting.view / manage_accounts | COMPLETE | §0.98. Accounts with the five kinds, transfers, and the unmatched count that leads here. |
| C3 | Expense & Investment Management | FE-26 | /money/expenses, /money/expenses/setup | 16 /expenses/* routes | expense.view / expense.record / expense.manage_heads | IN PROGRESS | Expenses and the configuration behind them are done (§0.98, §0.99): period, voucher, recording, categories, departments, standing costs. Investors (C3.2) not started. |
| C4 | Accounts Receivable & Payable (AR/AP) | FE-20, FE-25 | /customers/{id}, /customers/ageing | /customers/{id}/ledger, /open-invoices | customers.view | IN PROGRESS | The customer statement and unpaid-invoice list are built; the ageing reports are not |  |
| C5 | Employee / HR Management | FE-29 | /people/employees | /employees/*, /attendance, /leave | hr.view / hr.manage | IN PROGRESS | Plan-gated: payroll. |
| C6 | Payroll, Commission & Saudi WPS Compliance | FE-29 | /people/payroll | /payroll/*, /commission-rules, /eosb | payroll.view / run / approve | IN PROGRESS | Includes the Saudi WPS wage file. |
| C7 | Fixed Asset Management | FE-48 | /money/assets | /assets/* | asset.view / asset.manage | IN PROGRESS |  |
| C8 | Shift Management & Cash Drawer Reconciliation (X/Z Report) | FE-19, FE-41 | /shifts, /pos | 6 /shifts/* routes | sales.receive_payment, report.view | IN PROGRESS | Opening a session is COMPLETE in the till (validated live). Cash drop, close and X/Z are not. |
| C9 | Double-Entry Accounting Engine | FE-27 | /money/journals | /accounting/journals | accounting.view | IN PROGRESS |  |
| C10 | Fiscal Period & Year-End Closing | FE-50 | /money/periods | /accounting/periods/*, /accounting/year-end | accounting.close_period / reopen_period | IN PROGRESS |  |
| C11 | Bank Reconciliation | FE-28 | /money/reconcile, /money/reconcile/{id} | /treasury/statements/*, /treasury/lines/{id}/match | accounting.reconcile | COMPLETE | §0.100. Import, auto-match, match by hand, undo, sign-off refused while anything is unexplained. One backend defect fixed: the frozen-statement refusal arrived as a 500. |
| C12 | Payment Settlement & Gateway Reconciliation | FE-51 | /money/gateways | /settlement/*, /payment-gateways/* | accounting.view, gateway.view | COMPLETE | §0.108. Two payments defects fixed here: switching on an unchecked live connection said "That value is not allowed", and a failed check stored its error code in the shopkeeper's sentence. |
| C13 | Inventory Costing & COGS Engine | FE-21 | /stock | GET /stock/on-hand | inventory.view | IN PROGRESS | Costing is a backend concern; the frontend shows value at cost on the dashboard already. |
| C14 | Accounting-Aware Returns, Exchanges & Credit Notes | FE-18 | /pos/returns | /pos/returns | sales.refund | IN PROGRESS |  |
| D1 | Reporting Suite | FE-31 | /reports/* | 10 /reports/* routes | report.view / report.export | IN PROGRESS |  |
| D2 | Business Analytics & Forecasting | FE-52 | /reports/analytics | /analytics/kpis, /movers, /forecast, /profitability | report.view | IN PROGRESS | Plan-gated: analytics. |
| D3 | Notification Center | FE-53 | /notifications | /notifications/* | authenticated | IN PROGRESS |  |
| D4 | Audit Trail & Activity Log | FE-54 | /oversight/audit | GET /audit | accounting.view | COMPLETE | §0.108. Append-only, and the screen says which fields moved without rewriting either side. |
| D5 | Approval Center | FE-37 | /approvals | /approvals/*, /approval-rules, /approval-delegations | approval.view / approval.decide | IN PROGRESS | Plan-gated: approvals. |
| D6 | Document Management | FE-55 | /oversight/documents | /documents/* | document.view / document.manage | COMPLETE | §0.108. `company` and `warranty` were permitted by the schema and refused by the service; both fixed, with a test that reads the CHECK rather than repeating it. |
| D7 | Global Search & Command Center | FE-36 | /search + header box | GET /search | authenticated | IN PROGRESS | Validated live: requires company_id; returns {kind,id,label,detail,amount,currency}. |
| E1 | ZATCA Phase 2 E-Invoicing Engine ("Fatoora") | FE-33 | /settings/einvoicing | 8 /einvoicing/* routes | einvoicing.view / einvoicing.onboard | BLOCKED | Direction says ZATCA is skipped and isolated. The one genuinely external dependency is the Fatoora OTP, which must never be fabricated. |
| E2 | Saudi Tax Engine | FE-31 | /reports/tax | GET /reports/vat-return | accounting.view | IN PROGRESS |  |
| E3 | Saudi Payment Methods & Payment Compliance (FULL COVERAGE) | FE-14, FE-51 | /pos, /money/gateways | /payment-gateways/*, /payment-attempts | gateway.view | IN PROGRESS | Four tenders live at the till; gateway administration is not built. |
| E4 | PDPL | FE-38 | /oversight/privacy | 25 /privacy/* routes | privacy.view / privacy.manage | COMPLETE | §0.107–108. Subject requests, breaches, consent, the processing register, retention and holds, and the published notice. /privacy/subprocessors is deliberately open to anyone signed in, and that is asserted. |
| E5 | Saudi E-Commerce Law & Online Store Compliance | FE-56 | /customers/portal | /portal/* | portal.view | IN PROGRESS |  |
| E6 | Saudi Labour & Payroll Compliance (expanded) | FE-29 | /people/payroll | /payroll/{id}/wage-file, /eosb | payroll.approve | IN PROGRESS |  |
| E7 | Compliance Monitoring Dashboard | FE-57 | /oversight/compliance | GET /compliance | compliance.view | IN PROGRESS |  |
| E8 | Regulatory Rule Registry | FE-35 | /platform/rules | /platform/rules | super-admin | IN PROGRESS |  |
| F1 | Business Workflow / Approval Engine | FE-37 | /approvals | /approvals/* | approval.view / decide | IN PROGRESS |  |
| F2 | Customer Self-Service Portal | FE-56 | /customers/portal | /portal/contacts, /portal/return-requests | portal.view / portal.manage | IN PROGRESS |  |
| F3 | Supplier Portal | FE-58 | /buying/suppliers | /portal/supplier/* | portal.manage | IN PROGRESS |  |
| F4 | Multi-Company / Group Consolidation | FE-59 | /oversight/groups | 10 /groups/* routes | group.view / group.manage | COMPLETE | §0.108. Plan-gated: 402 is treated as a commercial refusal distinct from a permission one, and a caller without group.view still gets 403 so nobody learns the plan's contents. |
| G1 | Country Configuration Engine | FE-09 | (all) | GET /companies | — | COMPLETE | country + base_currency drive market, grouping and precision. Validated live: country arrives lowercase. |
| G2 | Multi-Currency | FE-09, FE-49 | (all) | /exchange-rates | accounting.view | IN PROGRESS | Per-company currency COMPLETE. FX rate management not started. |
| G3 | Multi-Language & RTL/LTR | FE-10 | (all) | — | — | **COMPLETE** | 480 keys in en/ar/bn, every built screen translated, navigation holds keys. Two RTL defects fixed. Two tests keep it true. |
| G4 | Tax Templates Library | FE-35 | /platform/rates | /platform/jurisdictions/* | super-admin | IN PROGRESS |  |
| H1 | Security & Authentication | FE-02, FE-04, FE-05, FE-68, FE-69 | /login, /change-password, /forgot-password, /settings/security | /auth/* | public / authenticated | IN PROGRESS | Refresh rotation, CSRF double-submit and both login challenges verified live. |
| H2 | Offline-First Architecture & Sync Engine | FE-13 | /pos | /catalog/snapshot, /sync/push | sales.create | IN PROGRESS | The till holds the catalogue in memory. Queued offline sales are a desktop-till concern. |
| H3 | Device Management | FE-60 | /settings/devices | 12 /devices/* routes | devices.view / devices.manage | COMPLETE | §0.107. Live: a Saudi terminal needs an EGS unit; session counters register active, paired ones pending — and the screen distinguishes those two pendings. An enrolment code is `devices.manage`, asserted. |
| H4 | Backup & Disaster Recovery | FE-61 | /oversight/backups | /backups/* | backup.view / backup.run | COMPLETE | §0.108. A backup that ran is not a backup that restores, and the screen keeps the two apart. |
| H5 | SaaS Subscription, Billing & Feature Flags | FE-07, FE-62 | /settings/subscription | /subscription/*, /plans | subscription.view | IN PROGRESS | Entitlements drive navigation already; the billing screen is not built. |
| H6 | API & Integration Platform | FE-63 | /settings/integrations | /api-keys/*, /webhooks/* | integration.view / manage | IN PROGRESS | Keys and callbacks. https enforced by the database; an endpoint is switched off rather than deleted so the delivery history survives. |
| H7 | Import / Export & Data Migration | FE-64 | /settings/imports | 7 /imports/* routes, /exports/{kind} | data.import / data.export | COMPLETE | §0.108. Nothing is written until the person commits, and a partial import says how many rows would be left behind. |
| H8 | System Health Monitoring (Super Admin view) | FE-15 | /platform | GET /platform/health | super-admin | COMPLETE |  |
| H9 | Job / Queue System (Background Processing) | FE-65 | /platform/jobs | /platform/jobs/failed, /{id}/retry | super-admin | IN PROGRESS |  |
| H10 | Customer Support / Ticketing (Super Admin ↔ Tenant) | FE-66 | /settings/support, /platform/support | /support/*, /platform/support | support.raise / super-admin | IN PROGRESS |  |
| I1 | System / Owner Settings | FE-39 | /settings/business | /companies/{id}/* | identity.edit | IN PROGRESS |  |
| I2 | Receipt & Invoice Template Customization | FE-67 | /settings/business | /companies/{id}/templates/{docType} | identity.edit | IN PROGRESS |  |
| I3 | Numbering Engine | — | — | — | — | N/A | Numbering is a backend engine. |
| I4 | User Preferences | FE-10 | (header) | — | — | IN PROGRESS | Language preference persists per device. |
| I5 | Point / Station Settings | FE-60 | /settings/devices | /devices/{id}/settings | devices.manage | COMPLETE | §0.107. Saved as a partial diff, so a field nobody touched is not written back. |
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

### 0.103 A design pass, and what the tools actually changed

Run against the screens built in §0.99–§0.102 rather than as a sweep, because a
checklist applied to a finished product finds nothing and a checklist applied to
what was just written finds things.

| tool | what it was asked | what came back | what changed |
|---|---|---|---|
| `ui-ux-pro-max --domain ux` | error summary validation form | **High**: "move focus to its heading after failed submit; **link each item to its invalid field**; retain inline errors" | The first two were already right. The third was not — see below. |
| `ui-ux-pro-max --domain ux` | quantity stepper numeric input touch | **Medium**: 44×44 minimum, 8px between targets | The returns screen's quantity box was 40px; the exchange screen's was 44. Matched. |
| `ui-ux-pro-max --domain ux` | data table dense financial figures | Medium: horizontal scroll rather than overflow | Already: `DataTable` owns its scroll container so a wide table never scrolls the page. |
| `vercel-react-best-practices` | `rerender-derived-state-no-effect` | derive during render, never in an effect | Already: `claim`, `settlement` and `readiness` are all computed in render. |
| `vercel-react-best-practices` | `rendering-conditional-render` | ternary, never `&&` | Already, in every new screen. Two `&&` in `FormError` fixed while it was open. |
| `vercel-react-best-practices` | `client-swr-dedup` | one request across instances | Already: `useSellFrom` and `LocationPicker` both read `/stock/locations` and React Query serves one. |

#### The one real gap: a summary that named four problems and reached none of them

`FormError` already took focus on a refusal and already kept the inline errors
beside their fields — both halves of the guideline. What it did not do was let
somebody GET to the field: the items were plain text, and the control ids come
from `useId()`, which is unique and addressable by nothing.

So `Field` gained an optional `name`, which is the API's own name for the field
and becomes the control's id — `field-account_id`. `FormError` renders each item
as a control that focuses and scrolls to it.

A button rather than an `<a href>`: the target is a form control on this page,
not a document location, and an anchor to an id that turns out not to exist is a
broken link. Where the field was not named it simply does nothing and the item
still reads as text, which is exactly what it did before.

Wired through the five forms written this session. The difference is between
telling somebody their VAT number is wrong and putting them in front of it.

#### What the component MCPs were not used for

shadcn, 21st.dev, Stitch, Skiper and UIverse were consulted as reference and
nothing was installed from any of them. The brief is explicit that the product
must not look copied from them, and this codebase already has its own
primitives — `Panel`, `DataTable`, `Field`, `Button`, and now `Tabs` — written
against its own tokens. Pulling in a component would have meant a second design
language beside the first, and a dependency added to claim a tool was used is
the thing the brief names as forbidden.

### 0.104 The ledger by hand — and a chart nothing could list

Two screens and one route that did not exist.

#### 🔴 There was no way to list the chart of accounts

Every posting path in the product resolves accounts by ROLE — `inventory`,
`input_vat`, `accounts_payable` — which is exactly right for a rule and useless
to a person writing an adjustment. `POST /accounting/journals` takes an
`account_id` per line, and **nothing anywhere answered "which accounts are
there"**. A manual journal screen was not buildable.

`/expenses/accounts` exists and is not this: it returns postable EXPENSE
accounts only, and is gated on `expense.manage_heads` because choosing what a
spending category posts to is a configuration decision. A journal touches any
account in the chart and is read by anybody who may read the ledger.

So `GET /accounting/chart`, carrying four things a list of names could not:

- **`is_postable`** — a header groups its children and holds nothing. The
  journal picker offers only the rest, because posting to a header is how a
  chart silently stops adding up.
- **`is_control`** with `control_of` — C9.3 makes receivable, payable and
  inventory hard invariants, and a difference between one and its sub-ledger is
  a real error rather than a rounding.
- **`role`** — what the posting rules call it. This is what makes the chart
  legible as a system rather than as a list: an owner asking "where does VAT go"
  is asking which account holds `output_vat`.
- **`balance`** — signed, and deliberately not normalised by type. A liability
  showing a debit balance and an asset showing a credit one are both worth
  seeing at a glance, and flipping the sign per type would hide exactly those.

Verified live against 45 seeded accounts, three of them control accounts.

#### The difference, not the two totals

The server refuses an unbalanced entry and says it as one number — *"debits come
to 100.00 and credits to 60.00, a difference of 40.00"* — because the figure a
person has to close is the difference. The screen shows the same figure while
they type, so the refusal is prevented rather than explained: an entry that does
not balance is the ordinary state of one half-written.

`lib/accounting/journals.ts` is 25 tests, including the one that matters —
two lines of `0.10` and `0.20` against a credit of `0.30` balance, because
this is the ledger and `0.1 + 0.2` in binary floating point is not `0.3`.

#### There is no edit button, and there never will be

Design 02 §111: *"Corrections happen only by posting a reversing entry with
reverses_id set. There is no code path — and no database permission — that edits
posted history."* So the detail screen offers the opposite entry instead, and
says why.

Driving it found that the reverse route wants **its own reason**, and the
refusal explains it better than a comment could: *"The ledger will carry an
opposite entry, and this is the only place that says what it was for."* The
screen was sending an empty body. It now asks — a reason for the reversal, not
this entry's, because "why it was undone" is not the same sentence as "why it
was written".

#### 🔴 A replayed journal said it had created something

`POST /accounting/journals` answered **201 either way**. The body was right —
the same journal, one row in the table — but the status says "created" of an
entry that already existed, and every other idempotent path in the product
answers 200 with `Idempotency-Replayed`: a sale, an exchange, a purchase
return. A caller branching on the status to tell "posted" from "already posted"
was told wrongly.

`Journal` now carries `already_recorded`, and
`TestTheSameJournalArrivingTwiceIsPostedOnce` — which asserted 201 and so could
never have caught it — now asserts the replay.

#### Verified live

```
ok GET /accounting/chart
ok a control account says what it controls (receivable)
ok the chart says what the posting rules call an account
ok a journal with no reason is refused, and the field is named
ok an unbalanced journal is refused, and the refusal says by how much
ok a line cannot carry a debit and a credit at once
ok the same journal arriving twice is posted once, and says so
ok a reversal with no reason on it is refused
ok a reversal is the opposite of what was posted, and links to it
ok the original says it has been reversed
```

And the boundary the permission exists for:

```
ok auditor:        GET  /accounting/chart    -> 200
ok auditor:        GET  /accounting/journals -> 200
ok auditor:        POST /accounting/journals -> 403
ok branch manager: GET  /accounting/chart    -> 403
ok accountant:     POST /accounting/journals -> 201, past the gate
```

The Auditor is the case `accounting.create` exists for: they read everything and
reconcile the bank, and cannot post — because somebody who could correct the
books they are checking is not checking them.

### 0.105 Receivables — the same report, pointed the other way

What suppliers are owed and what customers owe are one report in two
directions. The five buckets, the em dashes, the weighting and the total are
identical; the first column and the words are not.

So the table came out into `components/money/ageing.tsx` and both screens use
it. The alternative was two hundred lines written twice, and the second copy is
where the em dash rule quietly stops matching.

**A counter sale is never on this screen.** It was paid when it was rung up.
Everything here is an invoice on account, which is the only kind that can be
late — and the empty state says so, because "nobody owes anything" read as
"nothing was sold" would be alarming for no reason.

**The allocation is explicit, as it is on the supplier side.** A customer paying
is usually paying particular invoices, and guessing which produces a statement
they dispute. One press settles everything and every figure stays editable.

**`credited` is its own column.** Goods brought back through a return, kept
apart from money paid, because a customer querying their balance has to tell
the two apart — and the API separates them for exactly that reason.

#### Verified live, end to end

The dev data had no debtor, so the first run reported honestly that it had
exercised nothing. Rather than leave it there, a driver put one on account —
a standard invoice settled to `customer_due` — and the whole path ran:

```
POST /pos/sales (on account)            -> 200.00 receivable
GET  /receivables/ageing                -> not_due 200.00
POST /receivables/receipts (over)       -> 400 "has 200.00 outstanding, less than the 999999.00 allocated"
POST /receivables/receipts (unallocated)-> 400 "Say which invoices this payment settles"
POST /receivables/receipts              -> RCT-2026-000001, settled 100.00, 100.00 left
retry                                   -> 200, already_taken, same number
GET  /receivables/ageing                -> not_due 100.00
```

`verify:api` now covers all of it, and still reports rather than passes when a
company has no debtor to exercise it with.

### 0.106 The statements, and a tax return that says why it cannot be filed

Four statements on one screen and the return on another.

**They are read together, so they are one screen.** An accountant asks "how did
we do", then "so what do we own", then "where did the cash go" — for the same
period, in that order. Four sidebar entries would make that three navigations
and lose the period each time. Tabs, with the period and the tab in the URL.

**Two stand at a date and two cover a range.** A balance sheet says what the
business owns on the 31st; a profit and loss says what it earned between two
dates. So the from-date is offered only where it changes something, and a
balance sheet is asked for `as_of` rather than being sent a parameter it has no
use for.

**Nothing on either screen adds anything up.** The server draws every figure
from the ledger and says whether the balance sheet balances. The one exception
is the cash flow's net movement, which is `closing` less `opening` — two figures
the server states — because the in and out lists carry no totals and summing
them would be a second answer that could disagree the moment the server groups a
line differently.

A loss is called a loss, and a refund a refund. "Net profit: −11,385" makes
somebody read the minus sign to find out, and the minus sign is the thing that
gets missed.

#### The tax return leads with what is stopping it

The route reports `outstanding`, and on a Saudi company today it holds three
sentences — the input tax on bills does not match the Input VAT account, tax
held behind the three-way match is not included, and:

> *"the official return form layout has not been verified against the tax
> authority, so these totals are not mapped to numbered boxes"*

A screen that showed the totals and left those in a footnote would be presenting
an unfiled draft as a filing. So they come first, in a panel of their own, in
**the server's own words** — rewording a regulatory refusal is how it stops
meaning what it said.

`readyToFile` is three conditions and all of them the server's: it reconciles,
nothing is outstanding, and it has not been filed. Deciding a return was ready
would be inventing a regulatory confirmation, which is the one thing this
product must never do. Ten tests cover it, including the one that matters —
`outstanding` is `omitempty` on the wire, and reading its absence as "unknown"
would have blocked every clean return.

Nothing on the screen says "VAT" and no rate appears anywhere. `model` and
`country` come off the payload; the totals are the ledger's, and the ledger got
them from the register.

#### Verified live

```
ok GET /reports/trial-balance
ok   trial balance row
ok GET /reports/profit-and-loss
ok GET /reports/balance-sheet
ok the balance sheet balances
ok the balance sheet carries the profit and loss's own figure
ok GET /reports/cash-flow
ok GET /reports/vat-return
ok   taxable supply
ok the return says what stops it being filed (3 reasons)
```

The cross-check between the two statements is worth keeping: a balance sheet
whose `current_earnings` disagreed with the profit and loss's `net_profit` would
mean the two were describing different books, and neither would say so on its
own.

### 0.107 Tills, labels and serial numbers — and a permission that let a role in through a door it could not open

Three screens: `/settings/devices`, `/products/labels`, `/stock/serials`.

#### "Pending" means two different things, and only one of them is a fault

A till reports `status` and `binding` separately, and the pair is the whole
screen. On a **paired** binding, `pending` is the ordinary state between
registering a counter and the machine enrolling itself — nothing is wrong and
nobody should be sent to fix it. On a **session** binding, `pending` should not
outlast the session that created it, so the same word means something has gone
wrong.

`terminalState` therefore answers `awaiting_machine` or `awaiting_registration`,
never "pending". Collapsing them into one badge would send a manager to a till
that is working correctly, which is worse than saying nothing.

`pending_code` is read for the same reason: a screen that cannot tell whether a
code is outstanding offers "get a code" to somebody who already has one, and the
second code silently retires the first.

`needsSigningUnit(market)` is `sa` only, and it is asked of the market rather
than assumed. A Saudi till needs its unit before it can invoice; a Bangladeshi
one does not, and showing that requirement everywhere would be inventing a
regulatory obligation for markets that do not have it.

#### The warranty answer is the server's, and there are four of them

`under_warranty` is derived from the date on each request, never stored. A
stored flag would be wrong every morning until a job ran, and the warranty desk
is precisely where a stale answer costs the shop money. Nothing on the screen
recomputes it from `warranty_until`: a second answer free to disagree with the
first is the one thing a counter must not be given.

Four states, not two. **Covered** and **expired** are the obvious pair;
**sold with no warranty** and **never sold** look identical to a screen that
only asks "is it in warranty", and they lead to completely different
conversations with a customer. The lookup leads the screen and answers on its
own, because somebody standing at a counter has a number in their hand and one
question.

A serial nobody has on file answers 404, and the screen says so in a sentence
rather than showing an error. It is an ordinary answer at a counter — a unit
this shop never sold.

#### A roll is not a sheet

`perSheet` returns null for a thermal roll rather than 1. "1 per sheet" invites
somebody to work out how many sheets a roll needs, and the answer to that
question does not exist.

#### The defect: the labels link opened a screen its holder could not read

The catalogue nav offered `/products/labels` to anyone holding
**`label.print` or `label.manage`**. Every read that screen makes — the barcode
scheme and the layouts — is gated on `label.print`. `label.manage` unlocks
editing once you are inside; it is not a way in.

No seeded role holds manage without print, which is why source reading never
surfaced it. The role builder can make one, and that role would have seen the
link, opened the screen, and been refused by the server before anything drew.
The nav and the page guard now both ask for `label.print` alone, and the
boundary is asserted rather than described:

```
ok label.print: GET /labels/scheme -> 200
ok label.print: PUT /labels/scheme -> 403
ok label.manage alone: GET /labels/scheme -> 403
ok devices.view: GET /devices -> 200
ok devices.view: POST an enrolment code -> 403
ok serial.view: GET /serials -> 200
ok serial.view: POST /serials -> 403
```

The three refusals are the ones worth keeping. An enrolment code is a
credential — it lets an unknown machine become this shop's till — so seeing a
till must not carry pairing one.

#### A verification that was checking the wrong person

Six assertions in this section reported 403 for the owner. The owner holds all
110 permissions and every one of these is in `permission_catalogue`, so the
grant was never the problem.

`verify-against-api.mjs` signs in as a platform operator for its last section.
That operator holds **zero** permissions by design — the file says so — and
`token` is module level, so every request made after that section authenticates
as the wrong person. The section had simply been appended below it.

Fixed twice over: this section now runs before PLATFORM, and PLATFORM hands the
tenant token back when it finishes. The restore is a no-op today, which is
exactly why it is worth writing — three sessions append to the end of that file
and none of them should have to know this.

#### Two housekeeping repairs in the checking tools themselves

`verify:rbac` builds two throwaway roles to prove the label split, and could not
remove them: a **disabled account still holds its role**, rightly, so the server
refuses with *"1 people still hold that role"* until the assignment itself is
taken away. The teardown now releases the assignment, then retires the account,
then removes the role, and reports how many it could not.

Eleven `verify-*` accounts from runs predating the retirement fix were still
active in the dev database; ten seats have been reclaimed. The fix itself was
sound — deactivation answers 204 and the person leaves the default `/people`
listing, which is why they looked un-retired.

#### Verified live

```
ok GET /devices
ok   till
ok 2 tills; 0 awaiting a machine, 1 pending on a session binding
ok GET /devices/stores
ok GET /devices/{id}/settings
ok GET /labels/scheme
ok the scheme states its own next code (MEN-BLA-XL)
ok GET /labels/templates
ok   label layout
ok 1 rolls report no per-sheet count
ok GET /serials
ok   serial
ok no unsold unit claims a warranty
ok an unknown serial is a plain not-found
```

Two of those shapes were being skipped — the database had no label layout and
nothing tracked by serial, so the assertions never ran and the section still
printed green. A roll and two unsold units were seeded **out of band**, not from
the verification script: a checking tool that writes grows the database by a row
per run, which is how the seat limit was reached the first time.

The scheme's `example` is asserted non-empty because the screen prints the
server's own next code rather than building one. A screen that assembled it
would be a second implementation of the rule that mints barcodes, free to
disagree with it.

### 0.108 The last six screens, and five defects underneath them

`/oversight/privacy`, `/oversight/audit`, `/oversight/documents`,
`/oversight/backups`, `/money/gateways` and `/settings/imports`. **Nav is 82 of
82 built.**

#### Two clocks that are not ours

PDPL gives a subject request a statutory deadline and a personal-data breach a
notification window. Both are counted by the server — `days_left` and
`hours_left` — and read here. A deadline this product worked out itself would
be a second answer to a regulatory question, free to disagree with the register
the request was filed against.

They are never converted into each other. An incident with four hours left and
a request with four days left are both urgent; expressing one in the other's
unit would invent a precision the deadline does not have.

`waiting_on_subject` is separated from the rest because the clock does not stop
when the shop is waiting for the person to answer, and a queue mixing the two
has somebody chasing work that is not theirs to do. It can still be overdue —
waiting is not an excuse the deadline recognises.

#### A backup that ran is not a backup that restores

The backend pins that sentence with a test of the same name. So "finished" and
"verified" are different words on the screen, and a run that finished without
being checked is reported as work still to do. A verification that ran and
*failed* is worse than none — it is a file known to be unreadable — so it reads
as a failure rather than as unverified.

The risk sentence is the server's own, printed as written. Recomposing it from
the parts would produce a second opinion on a question that needs one answer.

#### The filter that cannot go stale

`GET /audit` returns the verbs actually present in this tenant's trail alongside
the rows. The filter is built from that, so it never offers one the log cannot
contain and never omits one a new module started writing. The verb itself is
printed **as recorded** — an auditor comparing the screen against an export
needs the same string in both, and a friendlier rendering would put a word in
the record that nobody wrote.

`before` and `after` come over raw, deliberately, so the reader sees the record
rather than a rendering of it. The screen adds only *which* fields moved.

#### A plan refusal is not a permission refusal

`GET /groups` answers **402 `feature_not_in_plan`** to a caller holding
`group.view` perfectly well: this tenant's plan does not sell consolidation.
This is H5's 402-vs-403 distinction finally surfacing in a screen, and it
matters because the two have different remedies. "You may not do that" sends
somebody to their manager to ask for a permission they already hold, and the
manager cannot grant what was never sold.

So the refusal has its own state, in the server's words, saying outright that
this is not about permission.

`verify:rbac` pins the ordering too: a caller **without** `group.view` is
refused 403, not 402 — otherwise somebody with no business knowing it learns
what the plan contains.

**Dependency, stated rather than worked around.** The populated group screen
could not be driven live: the dev tenant's plan excludes the module, and
granting it (`PUT /platform/tenants/{id}/features`) was denied by this session's
permissions. The refusal path is proved live; the populated path is built from
the service's own types and remains unexercised.

#### Five defects, all found by driving

**A business could not file its own papers.** `document_entity_valid` permits
sixteen kinds of record; `entityTables` listed fourteen. `company` and
`warranty` were accepted by the database and refused by the service — and
`company` is the commercial registration, the licences, the municipality
permit, the documents a shop is asked for at short notice. The map's own
comment claimed `company` was "checked by a different predicate below". There
was no such predicate.

**A mistyped sensitivity was a 500.** `classification` is the `data_class`
enum, so an unknown value reached the insert and returned SQLSTATE 22P02. Now a
400 naming the four allowed values. That one also corrected this screen: the
field had been built as a document *kind* (licence, contract, receipt) and
`data_class` is how sensitive the **content** is — what the retention regime
and the erasure path read.

**Every download in the product answered 401.** A plain `<a href="/api/v1/…">`
cannot authenticate: the rewrite makes the API same-origin so the browser sends
the refresh cookie, but the API reads the bearer header and the access token
lives in memory. `api.download` fetches with the token, keeps the same silent
retry every other call has, and takes the filename from `Content-Disposition`.
`/reports/saved` was the broken one.

**Switching on a card connection said nothing useful.**
`payment_gateway_live_was_checked` refuses to activate a live connection that
has never answered — correctly — but the violation reached the caller as "That
value is not allowed". It now names the sequence, and the screen offers one
step at a time rather than a toggle that refuses.

**A failed check stored its error code.** `note = e.Error()` renders as
"code: message", so a shopkeeper read *"unavailable: The card machine did not
answer"*. It is the message alone now.

#### And one rotting assertion in the checking tool itself

`verify:api` asserted `201` on a POS exchange unconditionally, and began failing
because that section rings up a real sale on every run and had sold the dev
shop's replacement variant down to zero. *"There are 1 fewer Abaya, Black in
stock than this needs"* is the server being right. A check that reads correct
behaviour as a mismatch teaches people to ignore it. It now picks a replacement
that is actually on the shelf and says so when none is.

Same family as the seat leak: **a verification that writes eventually breaks
itself.** Every fixture these six screens needed was seeded out of band for that
reason, and no credential was invented — the card machine fixture is an address
in the documentation range, which honestly does not answer.

#### Verified live

```
ok every one of 200 entries uses one of the 31 verbs the trail offers
ok 1 documents that never expire report no countdown
ok every document carries one of the four sensitivities
ok a mistyped sensitivity is refused, not reported as our fault
ok it says where it stands: "Backed up and verified."
ok 1 of 2 finished backups are proved readable, 1 never checked
ok 8 providers each name the fields they need
ok no connection carries a key back
ok nothing is live and switched on without having answered
ok switching one on too early is refused, and says why
ok 6 import kinds each name the columns they need
ok group consolidation is refused commercially, not by permission
```

The gateway assertion worth keeping above the others is that no listing carries
a secret under any name: a payload that grew one would hand every key to
anybody who can read the list.

#### Not verified, and why

`document_attachment_test.go` pins the invariant that every kind the CHECK
permits is reachable. It has **not been run.** The dev database records a stale
hash for migration `0103_tenant_market` — applied on 4 September from a
pre-commit draft, while the committed file is the only version in git history —
so the schema matches and the recorded hash does not, and the test harness
refuses to migrate. Correcting that one row was denied by this session's
permissions, and routing it through another session would have been laundering
a permission decision. **It blocks every backend integration test, for every
session, not just this one.** The guard is behaving correctly: it cannot tell a
pre-commit draft from somebody editing applied history, and that is the failure
it exists to catch.

The fixes behind that test were driven against the running API instead: filing a
company document succeeded, another business's id answered 404, and a mistyped
class answered 400.

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

Core accounting and manual journals are done (§0.104), including a chart of
accounts route that did not exist.

Receivables are done (§0.105) and payables were done in §0.94 — bills, the
three-way match, supplier payments and the ageing.

The financial statements and the tax return are done (§0.106).

Next: **payroll and employees**, then roles with granular permissions, and the
business reports.

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

Every row below is the **backend** position, and it was true when written.

**The line that used to stand here — "Frontend is NOT STARTED across the board"
— is no longer true and has not been for a long time.** The frontend column now
lives in *Frontend reconciliation against the original 77 features* at the end
of this document: 64 COMPLETE, 3 PARTIAL, 7 NOT STARTED across six screens, 3
N/A. This table is deliberately left as the backend record rather than widened,
so that the two columns cannot drift into each other.

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

# C5 / C6 — Employees, attendance, leave and payroll: COMPLETE

Modules 10 and 11 of the ordered list. The backend for both had existed since
0091; neither had a screen, and building the screens is what found that two of
its routes had never worked.

| | |
|---|---|
| **Status** | COMPLETE (backend fix + frontend) |
| **Screens** | `/people/employees`, `/people/employees/new`, `/people/employees/[employeeID]`, `/people/attendance`, `/people/payroll`, `/people/payroll/[runID]` |
| **Migration** | `0129_a_day_not_worked_is_pay_not_earned.sql` |
| **Permissions** | `hr.view`, `hr.manage`, `hr.view_pay`, `payroll.view`, `payroll.run`, `payroll.approve` — all **existing** |
| **New account** | `2630 Staff Deductions Payable` → role `staff_deductions`, added to the provisioning chart and backfilled |
| **i18n** | 210 keys × en/ar/bn |
| **Tests** | 3 backend (absence), 3 backend (expiry), 39 frontend unit |

## A month with an absence in it could not be approved

Found by running a real month against the running server, not by reading source.

`payroll.accrue` version 1 debited the gross and credited social insurance, the
advance recovery and the net. A payslip has **five** deductions, not three:
`absence_deduction` and `other_deduction` are subtracted to reach that net as
well. So the entry was short by exactly their sum:

    POST /payroll/{id}/approve -> 500
    "This entry does not balance: debits total 16532.26 against credits
     of 16370.97, a difference of 161.29."

161.29 was one employee's one absent day. Mark anybody away for a single day and
the month could not be booked, could not be paid, and had no way forward but
deleting attendance that had been recorded correctly.

Every existing payroll test pays people who were never away, which is why this
stood. `TestAMonthWithAnAbsenceInItCanBeApproved` was proven to fail against the
old rule — version 2 was expired in the database and the test reproduced the
same 500, with the difference equal to the absence.

### The two figures are not the same kind of thing

A day not worked is pay that was never **earned**. Nobody is owed it and nobody
holds it, so it credits the wage expense — the same account the gross debits.
The net charge is then what staff actually earned, and both figures stay on the
entry, so the payroll register still reconciles to the ledger line by line.
Debiting a smaller number would have balanced equally well and lost the absence.

`other_deduction` is the opposite: money the employee **earned** that the
business keeps back. It is a liability, and 2630 was created for it. Nothing
populates that field yet; it got a rule line anyway, because the day something
does, the failure would be this same unbalanced entry again.

The rule was **superseded, not edited** — 0015 forbids editing a posting rule,
because an entry posted last March must stay explainable by the rule that made
it.

## `GET /employees/expiring` had never once answered

C5 names an Iqama and ID expiry alert. The route was 500 on every call it had
ever received: `current_date + ($2 || ' days')::interval` with `$2` bound as an
int, which Postgres cannot plan. Nothing caught it because nothing called it —
no screen, so no test and no person. Fixed to `make_interval(days => $2)` and
covered by three tests, the first proven to fail on the old query.

## The pay boundary is absence, not zero

A6.2 requires staff to be blockable from "other employees' salaries", and the
seeded Store Manager holds `hr.view` and `hr.manage` and **not** `hr.view_pay`:
they roster their branch without learning what it is paid.

The server enforces it by **omitting** the pay fields. That distinction is the
whole design: a regression here would not 500 and would not 403 — it would
quietly start sending every salary to somebody who may not see one, while every
screen carried on working. So `verify:rbac` now asserts absence field by field
on a 200 payload, and asserts the same route carries pay for an HR Manager, so
the omission is proved to be the permission rather than the route having
stopped sending pay at all.

The frontend mirrors it in `maySeePay`, which asks whether the field **arrived**
and never whether it is non-zero — a commission-only salesperson genuinely earns
a basic of nothing, and must read as `0.00` to somebody entitled to see it and
as nothing at all to somebody who is not. The salary column disappears rather
than filling with dashes, and the hire form does not draw pay boxes it would
then discard.

## Nothing regulatory is invented

* Social insurance rates come from `SA.GOSI.RATES`, resolved **at the period**
  so re-running an old month gives the historically correct figure.
* A run that could not compute it reports `gosi_unavailable` with the server's
  own reason. The screen leads with that warning; showing `0.00` would state a
  liability of nil for the month.
* `totalCost` returns null rather than adding a zero, for the same reason.
* End-of-service refuses on `SA.EOSB.ENTITLEMENT` still being a placeholder.
* The wage file refuses while the Mudad layout is unverified. The button is
  still offered, because everything around the file — every employee has an
  IBAN, a social insurance number, an ID — is checked and worth checking; what
  comes back is shown in the server's words.

## Three smaller things the same pass found

* **`Number('')` is 0, not NaN.** Caught by two of my own failing tests. Every
  blank money field would have read as a genuine zero — the same trap as
  `decimal.js` reporting zero as positive. `numberOf` returns null for blank.
* **"has no a GOSI registration number".** The wage-file refusal supplied "no"
  and each item supplied its own article. Shown to a payroll clerk, so it has to
  read like English.
* **`verify:rbac` leaked an account per role per run.** Sixty-five had
  accumulated and the dev tenant was one account from its 75-seat plan limit.
  Worse, `staff()` returned null on any failure and every caller printed "no
  such role seeded" — five boundaries had silently stopped being checked while
  the run still ended "EVERY BOUNDARY HELD". It now says why it could not seed,
  and retires its own accounts at the end of the run.

## Verified live

    ALL SCREEN CONTRACTS VERIFIED     (verify:api, incl. a payslip that adds up)
    EVERY BOUNDARY HELD               (verify:rbac, incl. the hr.view_pay split)
    contract up to date: 468 routes, 110 permissions (103 route-gated)
    957 internal/api tests, and every other backend package: green
    277 web-next tests, 482 shared tests: green

## Remaining

`bonus` and `other_deduction` are computed as zero by every path today: nothing
writes to them. The columns, the payslip lines and now the posting rule all
carry them, so whatever populates them lands in a ledger that already balances.
Recorded as unused rather than removed.

# A6.2 — Roles and granular permissions: COMPLETE

Module 12. The role builder had a backend since 0101 and no screen. Building the
screen found that the list it draws from could not answer two of the questions
the screen has to ask, and that one of its refusals said something untrue.

| | |
|---|---|
| **Status** | COMPLETE (backend fixes + frontend) |
| **Screens** | `/people/users`, `/people/roles`, `/people/roles/new`, `/people/roles/[roleID]` |
| **Migration** | `0130_every_permission_says_what_it_lets_somebody_do.sql` |
| **Permissions** | `identity.view`, `identity.create`, `identity.manage_roles` — all **existing** |
| **i18n** | 101 keys × en/ar/bn |
| **Tests** | 5 backend (role list, refusals), 2 backend (permission catalogue), 23 frontend unit |

## Two permissions had never said what they let somebody do

`permission_catalogue` is what turns `sales.view` into "See what the shop sold"
— one sentence in an owner's words, in three languages, with a warning where the
permission deserves one. The builder is made of nothing else.

`catalog.edit` and `report.export` were never given a row. Both are enforced by
routes and both have been in the seeded roles since 0005. The service falls
through to `{section: "other", label: <the permission key>}`, which is the right
fallback — a permission that lost its description should stay grantable rather
than vanish from a role somebody is editing — but it is not a place to live. An
owner building a role read 101 sentences and then, under a heading called
"other", the words `catalog.edit` and `report.export`. In Arabic and Bangla too,
because a fallback has nothing to translate.

Found by rendering the list before building the screen for it.
`TestEveryPermissionSaysWhatItLetsSomebodyDo` stops the next one arriving the
same way, and `TestNoPermissionSectionHoldsASingleTickBox` caught the other half:
`inventory.recall_batch` sat alone under a heading called `inventory` while every
other stock permission was under `stock`, so the builder drew a section for one
tick box.

## The role list could not say which roles are the product's own

Thirteen roles, all built-in, and `GET /people/roles` said so about none of them.
A screen therefore had two options: ask `GET /roles/{id}` thirteen times to draw
one table, or offer Edit on everything and let the server refuse. The same gap
hid `in_use`, without which Remove is a button that cannot say whether it is
available.

`RoleOption` gained both. `is_system` is `r.is_system OR r.tenant_id IS NULL`,
because a template belongs to no tenant and `SaveRole` already treated the two
the same.

## A refusal that said the role did not exist

`DELETE /roles/{id}` scoped its lookup to the caller's tenant. A built-in
template has no tenant, so the row never matched and the answer was 404 *"That
role was not found"* — for a role sitting in the list the caller had just read,
where the same id on PUT answered *"That is one of the built-in roles. Copy it
and edit the copy."*

An owner told a visible role does not exist reloads the page. The truth is that
it cannot be deleted. `RemoveRole` now looks the role up by id the way `SaveRole`
does and checks ownership afterwards, so a built-in refuses as a built-in and
another business's role is still, correctly, not found —
`TestARoleSomebodyElseOwnsIsStillNotFound` exists to stop that fix turning every
miss into a 403.

## Delegation must not become escalation

`identity.manage_roles` lets somebody put into a role anything they hold
**themselves**, and the server's subset rule is the only thing between that and
"anybody who can build a role can build themselves an Owner". The failure would
be silent and total: a manager builds "Evening cover", quietly ticks
`accounting.close_period` and `hr.view_pay`, assigns it to themselves, and the
business has no owner-only anything. Nothing 500s. Nothing looks wrong.

`verify:rbac` now proves it live — an HR Manager is refused the builder outright,
and a role holding something the caller does not is refused with the permission
named. The picker mirrors the same rule so an ungrantable box is drawn disabled
and explained rather than ticked and rejected on save, and `copyOf` keeps only
what the copier can grant, so copying the Owner role produces a saveable draft
instead of a refusal naming permissions the person never chose.

## What the screens say that a table of tick boxes would not

* **103 boxes is a list nobody reads**, so they are grouped by the server's own
  sections, collapsible, each saying how many inside it are ticked — a collapsed
  section still tells you whether anything in it is on.
* **A caution is not decoration.** Twenty-seven permissions carry a sentence
  from the server saying what makes them dangerous. They sit beside the box, in
  the server's words.
* **Four sign-in states, not two.** Suspended is an administrator's decision,
  locked is the sign-in system's after too many failures, and a one-time
  password is an account that works and has not been collected. Collapsing them
  into "inactive" loses the only one an owner can act on.
* **A one-time password is shown once.** A4.2 calls the irreversibility "a
  security requirement, not just a policy choice", so the panel says plainly
  that closing it loses the password rather than implying it can be found again.
* **Nobody is deleted.** Suspending stops them signing in and leaves the record,
  the same reasoning that keeps a departed employee's payslips readable.

## Verified live

    ALL SCREEN CONTRACTS VERIFIED     (103 permissions, all described, all translated)
    EVERY BOUNDARY HELD               (incl. the subset rule and the built-in refusal)
    contract up to date: 468 routes, 110 permissions (103 route-gated)
    957 internal/api tests + internal/identity: green
    300 web-next tests, 482 shared tests: green

# Tax management and E7's compliance dashboard: COMPLETE

Module 13. Three screens, and the route behind the biggest of them had never
once answered.

| | |
|---|---|
| **Status** | COMPLETE (backend fixes + frontend) |
| **Screens** | `/settings/tax`, `/settings/einvoicing`, `/oversight/compliance` |
| **Migration** | none — both fixes are in Go |
| **Permissions** | `accounting.view`, `accounting.create`, `compliance.view`, `einvoicing.view`, `einvoicing.onboard` — all **existing** |
| **i18n** | 130 keys × en/ar/bn |
| **Tests** | 4 backend, 20 frontend unit |

## `GET /compliance` answered 500 for every business that had paid anybody

`max(period)` on `payroll_run.period` is a DATE, scanned into a `*string`. pgx
refuses to put a date into a string in binary format.

An empty table hands back a NULL and nothing has to be converted — which is
exactly why the fault survived. It worked until the month a business first ran
payroll, and never again after. E7's whole dashboard — invoicing, VAT, privacy,
storefront, payroll, documents, archive health — was unreachable from that day
on. Nothing caught it because nothing called it: no screen.

Formatted in SQL now, and as a month rather than a day: the rest of the product
speaks of a run as `2026-08`, and `2026-08-01` invites somebody to wonder what
happened on the first.

## And the people reading was blind to the case it exists for

Its own comment says *"E7 names Iqama and work permits specifically"*, and it
counted rows in the `document` table only. An employee whose residency permit
expires next month — the exact thing `GET /employees/expiring` lists by name —
did not appear. The dashboard told an owner nothing was expiring while their
cashier was weeks from being unable to work legally.

It now counts both, with the staff figure reported separately as well as inside
the total: two different screens fix them, so a dashboard that lumps them
together gives an owner a number and not somewhere to go. Somebody who has left
is excluded — their permit lapsing is not this business's exposure, and counting
it would leave a permanent reading nobody can clear.

Sixty days, the same window the staff alert uses. Two windows would let the
dashboard and the staff screen disagree about the same person.

## Not one tax rate is typed anywhere on these screens

The standard rate, the filing deadline and the record-retention period are read
off `GET /compliance`, which reads them from the regulatory register. There is
no control on `/settings/tax` that sets a rate and there will not be: a rate
somebody typed is a rate nobody verified, and every invoice the business issues
would be computed from it.

What IS typed there is an exchange rate, and the distinction is the point — a
rate between two currencies is a market fact a business records, not a legal
value a government sets. The server refuses to guess one, so somebody has to
enter it.

## "We do not know" never renders as "nothing to do"

* A filing deadline the register cannot compute reads as **unknown**, not as
  settled — `filingUrgency` returns `unknown` rather than `settled` when the
  server sends no date, and there is a test for exactly that.
* An e-invoicing chain nobody has onboarded reads as **not started**, not as
  healthy.
* `totalCost`-style silence elsewhere: an archive nobody has proved a restore
  from says so, because that is the only honest answer this product can give
  about a backup.
* **Unverified is not broken.** `unverified_rules` counts legal values nobody
  has checked against a primary source; `blocking_rules` counts the subset that
  stop something working. Twelve unverified with one blocker is a business
  trading normally with one thing to chase, and reporting twelve problems would
  be as wrong as reporting none.

## The dashboard leads with what to do, not with nine equal panels

Nine panels of equal weight tell an owner nothing, because nothing on the screen
is more important than anything else. `needsAttention` orders the outstanding
readings — critical, then caution, then unknown — and every line links to the
screen where that thing is fixed. A dashboard that names a problem and leaves
you hunting for the page is one people stop opening.

A rejection outranks a failure, and expired outranks expiring, for the same
reason in both cases: one will fix itself on retry and the other will not, and
an expired permit is not a warning about next month.

## The Fatoora one-time password, said plainly

ZATCA issues a certificate only against a password the taxpayer reads from their
own portal. Software cannot generate it and fabricating one would be forging a
credential. `/settings/einvoicing` says that at the top, once, as a property of
the obligation rather than a fault in the product — and everything around it is
built: the unit, its nine certificate-request fields, the environment choice,
the stage it has reached, and renewal.

The password field is `type="password"` and is cleared the moment the request
returns, whichever way it went. A one-time password left in an input on a shop
counter is the same exposure as writing it on paper.

The three environments are an explicit choice with no default beyond the URL,
because onboarding into the wrong one produces a till that appears to work and
reports nothing — and the status route refuses without one, which
`verify:api` now asserts.

## Verified live

    ALL SCREEN CONTRACTS VERIFIED
    EVERY BOUNDARY HELD    (einvoicing.view vs einvoicing.onboard;
                            accounting.view vs accounting.create;
                            compliance.view standing on its own)
    contract up to date: 468 routes, 110 permissions (103 route-gated)
    957 internal/api tests: green
    320 web-next tests, 482 shared tests: green

# D1 / D2 — Business reports, analytics and exports: COMPLETE

Items 14 and 15. The tax return screen was already built; what was missing was
everything around reporting — the analytics, the reports somebody keeps, and
the two statements that could be read and never saved.

| | |
|---|---|
| **Status** | COMPLETE (backend additions + frontend) |
| **Screens** | `/reports/analytics`, `/reports/saved` |
| **Migration** | none |
| **Permissions** | `report.view`, `report.save`, `report.export` — all **existing** |
| **i18n** | 113 keys × en/ar/bn |
| **Tests** | 4 backend, 19 frontend unit |

## Two statements could be read and not saved

`GET /reports/{kind}/export` took six reports away as CSV. The cash flow and the
tax return had no export at all — and the tax return is the one statement a
tax-registered business actually sits down with before filing, and the one most
likely to be wanted as a file. Asking for it answered:

    There is no "vat-return" report to export.

for a report the saved-report table explicitly allows keeping.

The return is prepared by a different service from the one that writes the
exports, and neither package knew about the other — correctly, since a trial
balance has nothing to do with a tax return. So `reports` declares a one-method
interface with a shape of its own, `internal/api` holds the conversion, and the
wiring happens where both are already arguments. The day the return gains a
field, the adapter is what stops compiling, which is the right place to be asked
whether the file should carry it.

### The caveats go above the figures

`Outstanding` names what a return does not include and why. In the file those
lines come BEFORE the totals, not in a footer: a spreadsheet is scrolled,
printed and forwarded, and a caveat under the figures is one somebody files
without reading. The file also states, in itself, that nothing has been
submitted — a CSV called "Tax return" that says nothing else invites exactly
that assumption.

The cash-flow export states its method for the same reason. A file labelled only
"Cash flow" is read as an IAS 7 indirect statement by anybody who opens it in a
spreadsheet, and this one is direct.

## A blank figure is a question the shop cannot answer yet

Three of D2's thirteen KPIs come back as an empty string on a young business:
stock turnover needs a period of purchase history; repeat-customer share and
customer value need customers who have come back. The server sends `""` rather
than `0`, and the difference is the whole point — a shop told its repeat-customer
rate is 0% concludes nobody returns, when the truth is that nobody has been given
time to.

`stated()` asks whether a figure arrived, never whether it is non-zero, and the
screen renders an unstated one as a dash with a sentence. `"0.0"` stays a real
answer: a month with no returns has a return rate of zero, and hiding it would
hide a fact.

The same distinction runs through the movers: `days_since_sold` is **-1** for
something that has never sold, not 0, which would mean "sold today" — and a line
that never sold reports no days of cover at all, because dividing a shelf by a
sales rate of nothing is arithmetic on nothing. `verify:api` asserts both.

## The forecast says what it is, next to the number

`basis` comes back as *"sales over the last 90 days, repeated"* and is shown
verbatim beside the figures. An owner ordering stock against a forecast has to
know it is arithmetic on the past rather than a prediction; a forecast that hides
its method gets trusted more than it deserves.

## A saved report is a question, not a photograph

The window is stored as a relative phrase — "last month" — never as two dates.
Run in October it means September; the same report run in November means October.
The screen shows the phrase AND what it resolves to today, because "last month"
is unambiguous to whoever wrote it and not to whoever inherits it.

The schedule rules are the database's, mirrored so the refusal never has to
happen: a cadence with nobody to send to *"runs every week and reaches nobody"*,
a weekly one needs a day, and a monthly one stops at the 28th because a schedule
set for the 31st skips February and a shop that asked for monthly figures quietly
gets eleven.

### `Number('')` is 0, caught again

A weekly schedule with no day chosen passed every range check as Sunday and would
have been saved to send on a day nobody picked. Zero is a real answer here —
Sunday — which is exactly why blank cannot be it. Found by a test written for the
empty case before the code was.

## Export offered only where it exists

Twelve report kinds can be kept and eight can be exported, and the two lists
spell themselves differently — the table writes `trial_balance`, the route takes
`trial-balance`. `exportKindOf` returns null for the four with no export, so the
screen shows no button rather than one that answers 400.

## Verified live

    ALL SCREEN CONTRACTS VERIFIED   (incl. all eight exports producing a file)
    EVERY BOUNDARY HELD             (report.view vs report.save vs report.export)
    contract up to date: 468 routes, 110 permissions (103 route-gated)
    957 internal/api tests: green
    339 web-next tests, 482 shared tests: green

# B7 / B16 — Tills, discounts, points and store credit: COMPLETE

Item 16 of the ordered list. Four screens, and two routes that had to be built
because the navigation had promised screens the API could not feed.

| | |
|---|---|
| **Status** | COMPLETE (backend routes + frontend) |
| **Screens** | `/shifts`, `/promotions`, `/customers/loyalty`, `/customers/wallets` |
| **New routes** | `GET /api/v1/shifts` (`report.view`), `GET /api/v1/wallets` (`wallet.view`) |
| **Migration** | none |
| **Permissions** | all **existing** |
| **i18n** | 97 keys × en/ar/bn |
| **Tests** | 4 backend, 17 frontend unit |

## The variance was reachable only from the till that produced it

A blind close asks a cashier to count the drawer without being told what the
system expects. The difference is the only signal the practice produces — and
there was nowhere to read it. `GET /shifts/current` needs a token bound to a
terminal; `GET /shifts/{sessionID}` needs an id only the till that opened it has
ever held. So last night's variance was unreachable from the back office, and a
supervisor reviewing in the morning had no route at all.

`GET /shifts` is that route. Behind `report.view` for exactly the reason the X
report is: it carries the expected figure beside the counted one, and a cashier
who can read the target can make tonight's drawer agree with it, after which
every variance reads zero and the blind close signals nothing.

### A nav entry that offered it to the wrong person

The `shifts` entry was gated on `sales.receive_payment` and pointed at a screen
that did not exist, and `navigation.test.ts` asserted that a cashier could see
it. Both were written when the link led nowhere. The entry is now `report.view`
and the test asserts the opposite, with the reason recorded in it.

### An open drawer shows nothing, not zero

`counted_cash`, `expected_cash` and `variance` are absent on a session still
running. A drawer nobody has counted is not a drawer counted at nothing, and the
difference decides whether a supervisor walks over to it. `verify:api` asserts
the absence.

## What the business owes in credit could not be seen whole

`GET /wallets/{customerID}` answered one customer at a time, so the store-credit
liability could only be found by asking about every customer in turn — and a shop
with two thousand customers could not ask at all.

`GET /wallets` lists only customers holding something: a page of names with zero
beside most of them is not a list of what is owed, and the empty rows are the
ones nobody is looking for. The screen adds its total from those rows rather than
fetching it separately, because a total that could disagree with the list under
it is the worst kind of disagreement on a liability.

## Four states, not on and off

"Inactive" covers a promotion somebody switched off, one that has not started,
and one that finished last month. An owner asking why a discount is not applying
at the till needs to know which. `promotionState` separates them, and compares
calendar days from the local date so a promotion that ends today still runs
today.

A loyalty scheme that does not exist shows as absent rather than as a form of
zeros — `exists: false` arrives with empty rates, and a form full of defaults
reads as a scheme somebody configured. A shop would start handing out points
nobody can spend.

## Verified

    ALL SCREEN CONTRACTS VERIFIED   (shift, promotion, loyalty, wallet shapes)
    EVERY BOUNDARY HELD             (cashier refused /shifts, /wallets credit,
                                     /loyalty/expire and /promotions)
    contract: 471 routes, 110 permissions (103 route-gated)
    internal/api [S-T] and [A-B] chunks: green; the four new tests: green

---

## Correction found while opening item 17: three branch dropdowns pointed at nothing

Driving the settings routes before building item 17 turned up a defect in THIS
session's own work. `GET /stores` does not exist, and three screens built earlier
in the session call it:

* `/people/employees/new` — the branch somebody is hired into
* `/people/employees/[employeeID]` — the branch on their record
* `/shifts` — the branch filter

All three would have rendered an empty select and a failed query, on screens
where naming a branch is most of the point.

Every branch list in the product belonged to another module: `/devices/stores` is
"the branches a terminal can be registered in" behind `devices.view`;
`/stock/locations` carries them as a side payload behind an inventory
permission; `/onboarding/stores` creates them during setup. There was no general
answer to "which branches does this business have".

`GET /api/v1/stores` is that answer, as `reports.StoresIn` beside
`reports.CompaniesFor` — the same question one level down. **Merely
authenticated**, deliberately and for the reason `GET /companies` already is: a
branch's name is not a secret from somebody signed into that company, RLS
confines it to their tenant, and every candidate permission is wrong for
somebody (`identity.view` is held only by the Owner and the Auditor in the base
seed, so an HR Manager could not fill the dropdown on the screen where they
assign a branch). A permission that must be granted to every tenant's cloned
roles before a dropdown works is the trap 0032 and 0033 fell into.

Closed branches are listed rather than filtered, sorted below the open ones. A
shift worked in a branch that has since closed still happened there, and
dropping it would show that shift with no branch at all.

Two further corrections came out of writing the tests, both mine:

* An explicit `CanAccessCompany` check in the handler was **redundant** —
  `companyFromRequestOrDevice` already refuses an out-of-scope company with "that
  company was not found". Removed rather than left as a second copy of a rule
  with one home.
* The cross-tenant test asserted a 404. That is the wrong mechanism: across
  tenants the guard is **row-level security**, not the token's company scope, so
  the id passes the scope check and the query simply matches nothing. The test
  now asserts the property that actually holds — no branch of theirs comes back.

4 backend tests. `verify:api` extended. Contract now **471 routes**.

# SESSION CHECKPOINT

**Written 2026-09-05.** The session before this one was interrupted mid-shutdown
and never wrote a checkpoint; this one recovers the state and records it.

## Last completed module

**Item 16 — remaining POS workflows** (tills, discounts, points, store credit).
Commit `e81ac5d`.

## Last completed screen/workflow

`/customers/wallets` — the store-credit liability, whole, with gift cards beside
it.

## Everything completed in THIS working session (items 10–16)

| # | Module | Commit |
|---|---|---|
| 10–11 | Payroll and Employees (C5/C6) | `0655869` |
| 12 | Roles and granular permissions (A6.2) | `10c892b` |
| 13 | Tax management and E7 compliance dashboard | `4c93b12` |
| 14–15 | Tax reports, business reports, analytics, exports (D1/D2) | `b1173a1` |
| 16 | Tills, discounts, points, store credit (B7/B16) | `e81ac5d` |

### Screens added this session (17)

`/people/employees`, `/people/employees/new`, `/people/employees/[employeeID]`,
`/people/attendance`, `/people/payroll`, `/people/payroll/[runID]`,
`/people/users`, `/people/roles`, `/people/roles/new`, `/people/roles/[roleID]`,
`/settings/tax`, `/settings/einvoicing`, `/oversight/compliance`,
`/reports/analytics`, `/reports/saved`, `/shifts`, `/promotions`,
`/customers/loyalty`, `/customers/wallets`

### Backend defects found by driving the running server, and fixed

Nine. Every one was found by calling the API, not by reading source.

1. **`payroll.accrue` could not balance a month with an absence.** The rule
   credited three of the five payslip deductions, so approving a month in which
   anybody was away answered 500 and the month could not be booked or paid.
   Migration `0129`, superseding rather than editing the rule.
2. **`GET /employees/expiring` had never once answered.** `current_date + ($2 ||
   ' days')::interval` with `$2` bound as an int. C5's Iqama alert was 500 on
   every call it ever received.
3. **`GET /compliance` answered 500 for every business that had run payroll.**
   `max(period)` on a DATE column scanned into a `*string`.
4. **E7's people reading was blind to residency permits.** It counted the
   document shelf only, so the dashboard said nothing was expiring while a
   cashier was weeks from being unable to work legally.
5. **`DELETE /roles/{id}` said a built-in role did not exist.** The lookup was
   tenant-scoped and a template belongs to no tenant.
6. **`GET /people/roles` could not say which roles are built-in or who holds
   them.** A screen had to ask thirteen times to draw one table.
7. **`catalog.edit` and `report.export` had no description at all**, so the role
   builder showed raw identifiers under a heading called "other", in three
   languages. Migration `0130`.
8. **The cash flow and the tax return could not be exported.** The two
   statements a business most wants as a file.
9. **No route listed shifts, and none listed wallets.** Added this session.

Plus: `verify:rbac` leaked an account per role per run until the dev tenant was
one seat from its 75-person plan limit, while reporting the failure as "no such
role seeded" — five boundaries had silently stopped being checked while the run
still ended EVERY BOUNDARY HELD. Fixed to say why, and to retire its own
accounts.

## Current verified state

| | |
|---|---|
| **Frontend routes (built pages)** | **88** (Next.js build output) |
| **Nav entries marked built** | **53 of 82** |
| **Frontend tests** | **356 passed / 23 files** (web-next) |
| **Shared tests** | **482 passed / 29 files** (includes i18n coverage, locale, RTL) |
| **Backend routes** | **471** |
| **Permissions** | **110** (103 route-gated) |
| **Migrations** | through **0130** |

## Status by check

* **Build** — `next build` compiles clean, 71 static pages generated, 88 routes.
* **Typecheck** — `tsc --noEmit` clean.
* **API contract** — `check:contract` clean at 470 routes / 110 permissions.
* **i18n** — three catalogues in step; ~651 keys added this session
  (210 + 101 + 130 + 113 + 97) across en/ar/bn. `coverage.test.ts` and
  `locale.test.ts` both green — no English prose in screens, no untranslated
  Bangla.
* **RTL** — `rtl.test.ts` green. New screens use logical properties throughout;
  the permission picker's chevron rotates rather than swapping glyph so the
  direction reads correctly in Arabic.
* **RBAC** — `verify:rbac` last run: **EVERY BOUNDARY HELD**, including the
  `hr.view_pay` omission, the role-builder subset rule, `einvoicing.onboard`,
  `report.save`/`report.export`, and the cashier refused the shift register.
* **Live API** — `verify:api` last run: **ALL SCREEN CONTRACTS VERIFIED**.
* **Backend tests** — 957 `internal/api` tests plus every other package were
  green as of the item-15 commit; the `[S-T]` and `[A-B]` chunks and the four
  new item-16 tests were green before shutdown.

### Environment note, and the re-verification

At the start of this recovery session the dev Postgres and Docker were both down
after the shutdown, so nothing backend could be checked. Docker Desktop turned
out to be installed under `C:\Users\USER\AppData\Local\Programs\DockerDesktop\`,
not the default `C:\Program Files\Docker\` — worth remembering, because the
obvious path is wrong on this machine. The database is the `rawsyst-dev-db`
container mapping 5433 to 5432; `docker start rawsyst-dev-db` brings it back.

With it up, **everything was re-run and is green**:

    item 16's four backend tests          ok
    internal/api ^Test[U-Z]               ok
    verify:api    ALL SCREEN CONTRACTS VERIFIED
    verify:rbac   EVERY BOUNDARY HELD

So nothing in this checkpoint rests on pre-shutdown results any more.

## Blueprint status

Both trackers are maintained, as required — the original 77-feature traceability
and the grouped tracker. Neither has been replaced by the other.

### The 23-item ordered module list

| # | Module | Status |
|---|---|---|
| 1 | Expense configuration (heads, departments, recurring) | ✅ COMPLETE (earlier session) |
| 2 | Bank reconciliation / treasury statements | ✅ COMPLETE |
| 3 | Sales returns and exchanges | ✅ COMPLETE |
| 4 | Purchase returns | ✅ COMPLETE |
| 5 | Complete accounting | ✅ COMPLETE |
| 6 | Manual journals | ✅ COMPLETE |
| 7 | Receivables | ✅ COMPLETE |
| 8 | Payables | ✅ COMPLETE |
| 9 | Financial statements | ✅ COMPLETE |
| 10 | Payroll | ✅ COMPLETE |
| 11 | Employees | ✅ COMPLETE |
| 12 | Roles + granular permissions | ✅ COMPLETE |
| 13 | Tax management | ✅ COMPLETE |
| 14 | Tax reports | ✅ COMPLETE |
| 15 | Business reports | ✅ COMPLETE |
| 16 | Remaining POS workflows | ✅ COMPLETE |
| 17 | **Business settings** | ⏳ **NEXT** |
| 18 | Hardware / device configuration | ⏳ not started |
| 19 | Integrations | ⏳ not started |
| 20 | Compliance / regulatory UI | ⏳ partly (dashboard done) |
| 21 | Complete Platform Admin | ⏳ not started |
| 22 | Any remaining Blueprint feature | ⏳ not started |
| 23 | Final end-to-end integration pass | ⏳ not started |

### Remaining unbuilt screens (29)

**Settings (item 17):** `/settings/business`, `/money/periods`
**Devices (18):** `/settings/devices`, `/products/labels`, `/stock/serials`
**Integrations (19):** `/settings/integrations`, `/settings/imports`,
`/money/gateways`
**Compliance (20):** `/oversight/audit`, `/oversight/documents`,
`/oversight/privacy`, `/oversight/backups`, `/oversight/groups`
**Platform Admin (21):** `/platform/businesses/new`, `/platform/billing`,
`/platform/rules`, `/platform/jurisdictions`, `/platform/rates`,
`/platform/subprocessors`
**Remaining Blueprint (22):** `/approvals`, `/deliveries`, `/customers/portal`,
`/money/installments`, `/money/assets`, `/money/investors`,
`/aftersales/service`, `/aftersales/requests`, `/settings/subscription`,
`/settings/support`

## IN PROGRESS

Nothing is half-built. Item 16 is committed complete; item 17 has not been
started.

## Genuine blockers

**One, and it is external.** The Fatoora one-time password: ZATCA issues a
compliance certificate only against a password the taxpayer reads from their own
portal. Software cannot generate it and fabricating one would be forging a
credential. The entire workflow around it is built (`/settings/einvoicing`), and
the screen says plainly what the user must fetch.

Nothing else is blocked. Two regulatory values remain unverified in the register
(`SA.EOSB.ENTITLEMENT`, `SA.WPS.WAGE_FILE_FORMAT`); both refuse rather than
guess, both have complete UI around them, and neither stops a business trading.

## Deliberate decisions worth remembering

* **`Number('')` is 0, not NaN.** Caught three separate times this session — in
  money fields, in a schedule day, and in rate parsing. Any blank-vs-zero
  distinction needs an explicit emptiness check.
* **Absence is not zero** is the recurring contract discovery of the whole
  session: omitted pay fields (`hr.view_pay`), unstated KPIs on a young business,
  an uncounted drawer, a loyalty scheme that does not exist, a sales tax with no
  input side. In every case the server omits rather than zeroes, and the screen
  must ask whether the field ARRIVED.
* **`bonus` and `other_deduction`** are computed as zero by every path today;
  nothing writes to them. Columns, payslip lines and the posting rule all carry
  them. Recorded as unused rather than removed.
* **`accounting.approve`** stays unused, and stays recorded as unused.
* Four files are `gofmt`-unclean and were before this session
  (`internal/catalog/service.go`, `internal/purchasing/billing.go`,
  `internal/settlement/settlement.go`, `internal/workflow/workflow.go`).
  Untouched — out of scope.

## Backend/frontend contract discoveries this session

* `POST /people` answers `{data: {person: {...}, temporary_password}}` — the id
  is under `person`.
* `POST /roles` and `PUT /roles/{id}` answer `{role: {...}}`, not the role.
* Payroll and advances resolve a **`money_account` id**, not a chart-account id.
  A chart id 404s with the same message.
* `GET /einvoicing/units/{id}/onboarding` **requires** `?environment=`; it will
  not default.
* `PUT /exchange-rates` takes `as_of`, not `on_date`.
* Saved-report kinds use underscores (`trial_balance`); export kinds use hyphens
  (`trial-balance`). Four savable kinds have no export.
* The dev tenant has a **75-person plan limit**; `POST /people` 409s at the
  ceiling with `plan_limit_reached`.

## Tool / skill usage this session

| Tool | Purpose | Where | Result |
|---|---|---|---|
| **Serena** MCP | Symbol overview and content replacement | `web-next/src/lib/purchasing/returns.ts`, `navigation.ts` | Used for structured reads; bulk edits fell back to scripted Python, which handled CRLF and non-ASCII more reliably |
| **Live API driving** (scratchpad Node scripts) | Discovery before building each screen | `drive-hr`, `drive-roles`, `drive-tax`, `drive-analytics`, `drive-pos`, `probe-*` | **The single highest-value technique.** All nine backend defects came from here; none from reading source |
| **`verify:api` / `verify:rbac`** | Contract and boundary assertions | extended in all five modules | Both green; the RBAC script's own account leak was found and fixed |
| **frontend-design / ui-ux-pro-max** (guidelines carried from earlier) | Restraint, hierarchy, no card walls | every new screen | Applied; nothing installed |
| **shadcn / 21st.dev / Stitch MCPs** | Consulted as reference only | — | **Nothing installed**, per the standing brief not to add dependencies to claim usage |

## Exact next task

**Item 17 — Business settings.**

Build `/settings/business` (company identity, market, addresses, branding,
document templates, storefront disclosures — the `storefront.missing` list the
compliance dashboard already reports) and `/money/periods` (accounting period
lock and close, which the compliance dashboard's `open_ended_periods` reading
already points at).

Start by driving the live routes: `GET/PUT /companies/{companyID}`,
`/companies/{companyID}/logo`, `/companies/{companyID}/templates/{docType}`,
`GET/PUT /privacy/disclosure`, and the fiscal-period routes.

**Note on the previously stated next task.** The plan carried into this session
named "Expense heads → Departments → Recurring expenses" as next. That work is
**already complete** — see *C3.1 — Departments and recurring expenses: COMPLETE*
above, and item 1 of the ordered list. It should not be redone.

## Next intended sequence after item 17

18 Hardware / device configuration → 19 Integrations → 20 Compliance and
regulatory UI (audit, documents, privacy, backups, groups) → 21 complete Platform
Admin → 22 remaining Blueprint features → 23 final end-to-end integration and
Blueprint reconciliation.

## Commits in this session

    e81ac5d  Let a supervisor read last night's drawer            (item 16)
    b1173a1  Let the tax return be taken away as a file           (items 14–15)
    4c93b12  Answer the compliance dashboard, and count the permits it is about  (item 13)
    10c892b  Say which roles are the product's own, and stop pretending one is missing  (item 12)
    0655869  Pay a month in which somebody was away               (items 10–11)

Branch: `international-markets-and-counters`.

---

# Frontend reconciliation against the original 77 features

**Written after item 22.** The matrix under *The matrix* above carries a backend
column and nothing else — it was written during a backend programme and says so
in its own header: "Frontend is NOT STARTED across the board." That sentence has
been false for a long time and the table never caught up, which was the single
largest inaccuracy in this document.

This section adds the frontend column. It is written from the route list on disk
and from what each screen actually calls, not from the nav flags — a flag is a
claim, and `navigation.built.test.ts` exists because claims drift.

## What "frontend COMPLETE" means in this table

A page existing is not it. A row is COMPLETE only where the screen reads and
writes the real routes, is gated by the permission the backend enforces, carries
loading, empty and error states, is keyed in all three catalogues, and uses
logical properties so Arabic mirrors. Where a screen covers part of a feature the
row says which part.

## The matrix, frontend column

| ID | Feature | Frontend | Where |
|---|---|---|---|
| A4 | Super Admin control plane | COMPLETE | `/platform`, `/platform/businesses`, `/jobs`, `/support`, `/billing`, `/rules`, `/jurisdictions`, `/rates`, `/subprocessors` |
| A4.1 | Super Admin credential security | COMPLETE | `/login` handles the MFA challenge; `/settings/security` enrols, disables, reissues recovery codes |
| A4.2 | Owner account recovery | COMPLETE | `/forgot-password` — built this pass; sign-in had linked to it with no page behind it |
| A5 | Business onboarding and provisioning | COMPLETE | `/platform/businesses/new`, with the one-time credential handover |
| A6 | RBAC and custom role builder | COMPLETE | `/people/roles`, `/roles/new`, `/roles/[roleID]`, `/people/users` |
| A7 | Multi-platform access | COMPLETE | `/settings/devices` |
| A8 | Dashboard and KPI | COMPLETE | `/dashboard`; every tile drills through |
| B1 | Product and catalog | COMPLETE | `/products`, `/products/[productId]` |
| B2 | Variant matrix | COMPLETE | `/products/[productId]` |
| B3 | Barcode engine and label studio | COMPLETE | `/products/labels` |
| B4 | Inventory and warehouse | COMPLETE | `/stock` and its eight children |
| B5 | Purchase and procurement | COMPLETE | `/buying/orders`, `/receipts`, `/requisitions` |
| B5.1 | RFQ and supplier comparison | COMPLETE | `/buying/quotes`, `/quotes/[rfqID]` |
| B5.2 | Three-way matching | COMPLETE | `/buying/bills/[billID]` |
| B6 | Supplier management | COMPLETE | `/buying/suppliers`, `/ageing`, `/payments` |
| B7 | POS and billing | COMPLETE | `/pos`, `/shifts` |
| B8 | Hardware integration | COMPLETE (architecture) | `/settings/devices`; physical drivers stay client-side |
| B9 | Promotions and pricing | COMPLETE | `/promotions` |
| B10 | Returns, exchange, replacement | COMPLETE | `/pos/returns`, `/pos/exchanges`, `/buying/returns` |
| B11 | Quotation to order to delivery | COMPLETE | `/orders`, `/orders/[orderID]`, `/deliveries` |
| B12 | Wholesale and B2B | COMPLETE | `/orders`, `/customers`; MOQ and credit-limit refusals surface from the server |
| B13 | Online order and delivery | COMPLETE | `/orders`, `/deliveries` — the status ladder and cash on delivery |
| B14 | Instalment and EMI | COMPLETE, one documented gap | `/money/installments`; the receipt picker, below |
| B15 | Warranty, serial, service | COMPLETE | `/aftersales/service`, `/stock/serials` |
| B16 | CRM and loyalty | COMPLETE | `/customers`, `/customers/loyalty`, `/customers/wallets` |
| C1 | Core accounting and ledger | COMPLETE | `/money/chart`, `/money/journals` |
| C2 | Cash and bank | COMPLETE | `/money/accounts`, `/transfers`, `/receipts` |
| C3.1 | Expense tracking | COMPLETE | `/money/expenses`, `/expenses/setup` |
| C3.2 | Investment management | COMPLETE | `/money/investors` |
| C4 | Receivables and payables | COMPLETE | `/customers/ageing`, `/buying/ageing` |
| C5 | Employee and HR | COMPLETE | `/people/employees`, `/people/attendance` |
| C6 | Payroll, commission, WPS | COMPLETE | `/people/payroll`, `/payroll/[runID]` |
| C7 | Fixed assets | COMPLETE | `/money/assets` |
| C8 | Shift and X/Z reports | COMPLETE | `/shifts` |
| C9 | Posting engine | N/A | An engine. It has no screen and should not have one. |
| C10 | Fiscal period and year-end | COMPLETE | `/money/periods`, `/money/journals` |
| C11 | Bank reconciliation | COMPLETE | `/money/reconcile`, `/reconcile/[statementID]` |
| C12 | Settlement and gateway | COMPLETE | `/money/gateways` — settlement batches and card connections on one screen, split by their two permissions |
| C13 | Costing and COGS | COMPLETE | `/reports/financials`, `/stock` |
| C14 | Accounting-aware returns | COMPLETE | `/pos/returns` |
| D1 | Reporting suite | COMPLETE | `/reports/financials`, `/reports/tax`, `/reports/saved` |
| D2 | Analytics | COMPLETE | `/reports/analytics` |
| D3 | Notification centre | COMPLETE | `/notifications` — built this pass; six routes had no caller |
| D4 | Audit trail | COMPLETE | `/oversight/audit` |
| D5 | Approval centre | COMPLETE | `/approvals` — both queues, decide, escalate |
| D6 | Document management | COMPLETE | `/oversight/documents`; building it found two document kinds the database accepted and the service refused |
| D7 | Global search | COMPLETE | `/search` and the header box — built this pass; the route had no caller |
| E1 | ZATCA e-invoicing | COMPLETE, external dependency | `/settings/einvoicing`; the Fatoora OTP is the taxpayer's to fetch |
| E1.3 | Offline B2B rules 2 and 6 | N/A | Optional; the till issues simplified invoices only |
| E2 | Saudi tax and VAT return | COMPLETE | `/settings/tax`, `/reports/tax` |
| E3 | Saudi payment methods | COMPLETE | `/money/gateways` |
| E4 | PDPL privacy | COMPLETE | `/oversight/privacy`, plus the storefront disclosures on `/settings/business`. Both regulatory clocks come from the server — 30 days on a subject request, 72 hours on a breach — and neither is recomputed here |
| E5 | E-commerce law and storefront | COMPLETE | `/settings/business` disclosures, `/customers/portal` |
| E6 | Saudi labour and payroll | COMPLETE | `/people/payroll` |
| E7 | Compliance dashboard | COMPLETE | `/oversight/compliance` |
| E8 | Regulatory rule registry | COMPLETE | `/platform/rules`, `/jurisdictions`, `/rates` |
| F1 | Workflow and approval engine | COMPLETE | `/approvals` |
| F2 | Customer self-service portal | COMPLETE (staff side) | `/customers/portal`, `/aftersales/requests`. The customer-facing portal is a separate surface reached with a portal session — see the correction below |
| F3 | Supplier portal | COMPLETE (staff side) | `/customers/portal` invites and revokes supplier contacts |
| F4 | Multi-company and group | COMPLETE | `/oversight/groups` and the company switch in the shell. A caller holding `group.view` gets 402 when the plan excludes it and 403 when they do not hold it, and the screen keeps those apart |
| G1 | Country configuration | COMPLETE | `/settings/business`; the market is shown and settled with its reason |
| G2 | Multi-currency | COMPLETE | `formatMoney` throughout; rates on `/settings/tax` |
| G3 | Multi-language and RTL | COMPLETE | three catalogues, logical properties, `rtl.test.ts` |
| G4 | Tax templates library | COMPLETE | `/settings/tax`, `/platform/rules` |
| H1 | Security and authentication | COMPLETE | `/login`, `/change-password`, `/forgot-password`, `/settings/security` |
| H2 | Offline-first and sync | COMPLETE | `/pos` |
| H3 | Device management | COMPLETE | `/settings/devices` |
| H4 | Backup and DR | COMPLETE | `/oversight/backups` |
| H5 | Plans, entitlements, limits | COMPLETE | `/settings/subscription`, `/platform/billing` |
| H6 | API and integration platform | COMPLETE | `/settings/integrations` — a key readable once, callbacks https-only |
| H7 | Import and export | COMPLETE | `/settings/imports` and the report exports |
| H8 | System health | COMPLETE | `/platform` |
| H9 | Job and queue | COMPLETE | `/platform/jobs` |
| H10 | Support ticketing | COMPLETE | `/settings/support`, `/platform/support` |
| I1 | System and owner settings | COMPLETE | `/settings/business` |
| I2 | Receipt and invoice templates | COMPLETE | `/settings/business` |
| I3 | Numbering engine | N/A | An engine. The counters are deliberately not shown. |
| I4 | User preferences | COMPLETE | language in the user menu; notification channels on `/notifications` |
| I5 | Point and station settings | COMPLETE | `/settings/devices` |

## Count

| | |
|---|---|
| COMPLETE | 74 |
| PARTIAL | 0 |
| NOT STARTED | 0 |
| N/A, an engine or optional | 3 — C9, I3, E1.3 |

**Nav coverage is 82 of 82.** Every entry in `BUSINESS_NAV` and `PLATFORM_NAV`
points at a screen that exists, and `navigation.built.test.ts` asserts that in
both directions against `page.tsx`.

The three N/A rows are not unfinished. C9 is the posting engine and I3 the
numbering engine — both are machinery with no screen, and giving the document
counters a screen would invite somebody to set one. E1.3 is the offline B2B
rule set, which becomes required only if the product ever issues standard
invoices at an offline terminal; today the till issues simplified ones.

**One row carries an external dependency rather than unfinished work.** E1,
ZATCA e-invoicing, is complete on both sides, and the Fatoora one-time password
is the taxpayer's to fetch from their own portal. Software cannot generate it
and fabricating one would be forging a credential, so `/settings/einvoicing`
carries the whole workflow around it and says plainly what the user must go and
get. That is the only genuinely external blocker in the product.

## Four features that the nav count could not see

A feature with no nav entry cannot show up as unbuilt in it, so the flagged list
was never the whole truth. Four backend-complete features had no frontend at all
and no entry:

* **Account recovery.** `POST /auth/forgot-password` and `/auth/reset-password`
  were live and uncalled. Sign-in had always carried a "forgot your password"
  link and there was no page behind it, so an owner locked out of their own
  business met a dead link.
* **MFA setup and sessions.** Signing in with a second factor worked, because
  the login screen handles the challenge. Nothing could turn one on, show
  recovery codes, or list and end sessions. Five routes, unreachable.
* **The notification centre.** Six routes and no reference anywhere in
  `web-next`.
* **Global search.** `GET /search`, and no box.

All four are built. They live in the user menu and the header rather than the
sidebar: every route behind them resolves the caller from their own token and
takes no user parameter, so there is no permission to name — and
`navigation.test.ts` is right to require one of every sidebar item, because an
item with no permission renders for somebody holding nothing. They are
registered in `navigation.built.test.ts` as reached-otherwise, each with the
place it is reached from.

## Corrections this pass owes the tracker

* **FE-56, "Customer portal administration", describes a screen that cannot
  exist.** `GET /portal/contacts` is `supplier_portal_user` — suppliers only.
  Customers sign in with a phone and a one-time code, and `POST /portal/code`
  answers identically whether or not the number is on file, "so the portal
  cannot be used to ask a shop who its customers are." There is deliberately no
  customer portal account for staff to create, disable or reset. The screen
  covers both halves and says so; the nav label read "Customer portal" and now
  reads "Portal access". The Blueprint row needs the same correction — renaming
  the label alone would leave the tracker claiming a feature the API refuses on
  purpose.
* **The nav flags are accurate and the prose rows were not. Now corrected.** A
  reconcile pass found **61 rows reading NOT STARTED with the screen on disk** —
  34 in §0.6 and 27 in §0.5 — while the `built` flags checked against `page.tsx`
  in both directions showed zero discrepancies. The map was holding and the
  prose was not, which is the argument for `navigation.built.test.ts` and
  against another proofread.

  All 61 are now IN PROGRESS rather than COMPLETE, deliberately: §0.5 defines
  COMPLETE against twelve criteria and "a page renders" is not one of them, and
  what was actually checked is that a screen exists at the route. The
  completeness judgement now lives only in this section. Three of the 61 were
  not simple flips — FE-18 and C14 named `/sales/returns`, which has never
  existed because returns live at the counter (`/pos/returns`), so the route
  cell was wrong rather than the screen missing; and FE-56's NAME was the untrue
  part, now "Portal access (supplier logins; customers self-serve by code)".

* **Four more rows a route-resolving check could not see.** FE-36 / D7 (global
  search) and FE-53 / D3 (notifications) read NOT STARTED because their route
  cell said "(command palette)" and "(header)" rather than a path, so nothing
  walking `page.tsx` could match them. Both are built. And account recovery and
  MFA setup had **no row at all** while H1 read COMPLETE against `/login` alone
  — an over-claim at the time it was written, since signing in with a second
  factor worked and nothing could enrol one or reset a forgotten password. They
  are now FE-68 and FE-69, and H1 names all four auth screens and reads IN
  PROGRESS.

  **Nothing now reads NOT STARTED in either table.** The last two were FE-63 and
  H6, integrations, closed when that screen landed.

## Gaps found and deliberately left open

* **There is no `GET /receivables/receipts`.** `POST /installments/{id}/collect`
  takes a `receipt_id`; receipts can be created and reversed but not listed. So
  the instalments screen cannot offer a picker — it asks for the reference and
  says where it comes from. One absent endpoint, not a UI decision.
* **`POST /onboarding/stores` counts an upsert as an addition.** Its ceiling
  check is `existing + len(payload) > ceiling` and it upserts by code, so a shop
  at its plan ceiling re-submitting the branches it already has is refused. The
  per-branch route added in `e0b467f` compares rather than sums and has a test
  for amending at the ceiling; the wizard path still has the old arithmetic.

## Verification at the time of writing

    typecheck            clean
    web-next tests       488 passed / 30 files
    shared tests         482 passed / 29 files
    build                clean, 121 routes, 104 static pages
    check:contract       476 routes, 110 permissions (103 route-gated)
    verify:api           ALL SCREEN CONTRACTS VERIFIED
    verify:rbac          EVERY BOUNDARY HELD
    nav coverage         82 of 82 built

**Backend integration tests could not be run, and this is not a code failure.**
The development database records a hash for migration `0103_tenant_market`
taken from a pre-commit draft applied on 4 September; the committed file is the
only version in git history, so the schema matches and the recorded hash does
not. The harness refuses:

    migration 0103_tenant_market was modified after it was applied
    (recorded e4acb998ac28, found 80bb1773bf45)

The guard is behaving correctly — it cannot tell a pre-commit draft from
somebody editing applied history, which is the failure it exists to catch, and
whatever is decided about the row should not be a change to the guard. Every
backend test in the repository is blocked by it, including fourteen written this
pass that were green earlier in the session. What was verified instead was
driven against the running API, which is recorded per screen above.

---

# AUDIT SESSION — 2026-09-08

**Brief:** do not trust any COMPLETE label; verify the implementation and the
end-to-end behaviour independently, and finish whatever is not finished.

This section records what was actually checked, what turned out to be wrong,
and what is deliberately still absent. Nothing below is claimed on the strength
of a previous session's note.

## The blocker that was not a migration problem

Every backend integration test in the repository had been unrunnable since
4 September. The migration harness refused with

    migration 0103_tenant_market was modified after it was applied
    (recorded e4acb998ac28, found 80bb1773bf45)

The previous note treated this as a question about migration `0103`. It was not.
The cause was that **integration tests were being run against `rawsyst_dev`** —
a long-lived database that had had migrations applied by hand for months — while
the repository already carries a disposable one, `rawsyst_test`, and a binary
whose whole job is to rebuild a database from nothing (`cmd/freshcheck`).

Resolved without touching the guard, without editing `schema_migration`, and
without editing an applied migration:

    RAWSYST_DB_DSN=...rawsyst_test go run -tags=freshcheck ./cmd/freshcheck

    public schema dropped and recreated; nothing is left
    all migrations applied in 1.836s
    migrations recorded: 130 (highest 130)
    tables: 183, with RLS forced: 175, policies: 175
    regulatory rules seeded: 44
    CDTFA rates seeded: 542, active: 542
    Saudi onboarding blockers outstanding: 0
    fresh database came up clean

That is simultaneously the fix and §23's clean-migration test. The development
database was rebuilt the same way and reseeded, so both are now reproducible
from the committed chain.

The suite went from **entirely blocked** to **entirely green**, and from
~2,500s to ~300s for `internal/api` — the old figure was mostly the accumulated
weight of the development database, not the tests.

**Correction to the standing note:** there is no `rawsyst-dev-db` Docker
container on this machine and Docker is not needed for backend work. Both
databases are in the native `postgresql-x64-18` service on port 5432, which is
what `backend/.env` has always said.

## Defects found, and how each was found

Five, and **not one came from reading source**. Four came from driving the
running API through `verify:api` against a database built from zero, and the
fifth from making a regulatory rule resolvable so the code behind it ran for the
first time.

### 1. The end-of-service award read one of its rule's six fields

`SA.EOSB.ENTITLEMENT` carries a wage basis and two service bands. The accrual
read `days_per_year_first_five` and applied it to every year of everybody's
service, on a wage hard-coded in Go as basic-plus-housing while the rule had a
field naming which wage to use.

Saudi labour law awards less for each of the first five years than for each year
after them, so reading the first band alone **understates the liability, and
understates it most for the long-serving people whose award is largest**. It
would have done so silently for years and surfaced on the day somebody with
fifteen years resigned.

Both bands are read now, and the band a month belongs to is decided by service
at that month, so crossing five years starts the higher rate from the month it
is crossed and nothing already posted moves. The wage basis is read from the
rule against a closed vocabulary (`basic`, `basic_plus_housing`,
`basic_plus_all_allowances`); a basis this product cannot compute is refused by
name rather than approximated.

### 2. The end-of-service accrual could never once have succeeded

Making the rule resolvable ran the code behind it for the first time, and it
failed immediately: it inserted the accrual row, posted the journal entry, then
`UPDATE`d the row to stamp the entry on it — and `eosb_accrual` carries a
`reject_always` trigger on UPDATE, because an accrual is history.

Nobody had ever seen it. The entitlement has always been a placeholder, so the
accrual refused for want of a rule long before reaching that line. The id is
minted in Go now, the entry posts first, and the row is written once with both.

### 3. Three of the four kinds of promotion answered 500

`promotion.value`, `buy_qty`, `get_qty` and `min_purchase` are all nullable, and
each kind fills a different subset. Both the campaigns list and the single read
selected all four raw and scanned them into non-null decimals. So creating the
most ordinary promotion a shop can run — a percentage off — answered 500, and
once one existed **the whole campaigns list answered 500 from then on**.

Nothing caught it because every fixture in the repository used `buy_x_get_y`,
which is the one kind that fills the columns the other three leave empty.

### 4. A wholesale order billed to an account was refused

`orders.Invoice` built the `customer_due` tender itself by adding the line
amounts up, which is the NET figure; the sale's total is the tax-inclusive one,
computed inside the sales engine after the tax profile is applied from the
registry. So an order quoted net of VAT and put on account came back with

    The payments come to 200 against a total of 230, a difference of -30

naming payments the caller never sent, and short by exactly the tax. Wholesale
is normally quoted net, so this was the ordinary path for the customers B12 is
about.

`Sale.OnAccount` now says the customer owes the whole thing and the engine
states the figure, after `Compute` and before the tender check. A caller that
states its own tenders is untouched.

### 5. The "cover is in force" badge never rendered

`GET /approval-delegations` answers `is_live`; `/settings/approvals` read
`live`. A manager covering for an away owner looked, on the screen, like a
manager covering for nobody.

It could not have been noticed before: a shop with one user cannot hold a
delegation at all, because delegating to yourself changes nothing and the
service says so. That is why the fixture mattered.

## The verification that was a memory rather than a check

`verify:api` asserts the FIELDS each screen reads, not only the status — but it
can only do that against a row. Against a database built from zero it reported
**54 payload shapes as "not exercised"** and moved on. Every one of those
contracts had only ever been confirmed against a development database somebody
had typed into months earlier.

`cmd/devseed` now builds a shop that has actually traded. Everything goes
through the services the API calls, so a seeded record is made the way a real
one is — hired, numbered, posted, and refused by the same validation.

| | |
|---|---|
| Unexercised shapes, before | **54** |
| Unexercised shapes, after | **5** |

What the seed now writes: two employees (one whose residency permit expires
inside the window the alert asks about, because an empty alert list describes
nothing), a worked day each, an undecided leave request, an advance, a prepared
payroll run; the quotation walked forward one state at a time to delivered,
invoiced on account and part-paid; an issued purchase order open for receiving
and a second supplier bill left unpaid; a nested department, a label layout, a
running campaign, an approval rule, a delegation, a commission scheme, an
exchange rate, a bank transfer, an expense, a standing cost, an asset and a
shareholder; a delivery, an instalment plan against a real invoice, two
serial-numbered units and a repair booked against one; a supplier portal
contact, a saved report, a filed document, an API key, a callback, a notice, a
support ticket with a reply, a backup, a consent, a subject access request, a
processing activity, a legal hold, a card gateway, an import batch and the
platform's sub-processor register; and a second business in Bangladesh.

Approving the payroll and settling the invoice are deliberately left undone.
They are decisions a person makes, and a fixture that made them would be
recording that the owner agreed.

### The demo shop could not sell

Found while closing the last of those: the seeded Saudi shop's counter had an
EGS unit, but the unit carried no registered name or VAT number and the branch
had no National Address, so **every sale answered "this shop is not set up for
e-invoicing yet"**. That is the correct refusal — BR-KSA-09, -37 and -66 require
all of it on the face of an invoice — and an incomplete fixture. The POS half of
the product could not be used in development at all.

With that fixed the whole exchange path is checked for the first time: the
settlement figure, the refusal with no reason, the credit note, and the retry
replaying the same documents and the same figures.

`verify:api` also had a defect of its own: it reused one variable for two
questions — whether the till must SAY where it is selling from (only when a
branch has more than one stock location) and where to ASK about stock — so it
skipped the exchange in every single-location shop, which is most shops.

### The five that remain, and why

| Shape | Why |
|---|---|
| A recorded data breach | Starts a 72-hour regulatory clock. Seeding one would put a fiction into a screen whose whole value is that it is believed. **Refused on purpose.** |
| A failed background job | An incident an operator is meant to act on. Same reason. **Refused on purpose.** |
| A customer's return request | Needs a customer portal session, which is an act by a member of the public rather than by the shop. Covered by backend tests. |
| A delivered webhook | Needs a real outbound HTTP call to a third party. Covered by backend tests. |
| A branch with no country of its own | A fallback for a branch that has not set one; the seeded branch has. |

## Regulatory state — SA.EOSB.ENTITLEMENT

Unchanged, and correctly so. It is the one release-blocking rule that has never
been verified against its official source, and **it is not this session's to
verify**: recording it is an assertion by a person that they read the figure in
the Labour Law, and `RecordRule` requires the official document to be named.
Fabricating that would be forging a legal record.

What was owed on the software side, and is now done:

* Both service bands and the wage basis are read from the rule (was: one field).
* The accrual behind it works at all (was: an impossible UPDATE).
* The refusal is specific — an unfilled band, an unfilled basis and a basis the
  product cannot compute each say a different thing, and each names what to do.
* Recording the verified value is a complete workflow on `/platform/rules`: a
  correction supersedes rather than overwrites, `__VERIFY__` cannot be written
  back, an unverified value must carry a note, and the verifier is stamped.

A deployment serving Saudi tenants refuses to START in production while it is
unverified, and the value refuses at the point of use regardless. Neither was
weakened.

## What was verified, and how

    clean migration from zero    130 migrations, 183 tables, 175 forced RLS,
                                 175 policies                          PASS
    backend, all packages        integration tags, fresh test database PASS
    backend, internal/api        292s                                  PASS
    go vet / go vet -tags=integration                                  PASS
    gofmt -s -l                  clean (two files were not, and are now)
    lint-wording                 1,413 files                           PASS
    frontend typecheck           tsc --noEmit                          PASS
    frontend tests               491 passed / 30 files                 PASS
    shared tests                 482 passed / 29 files                 PASS
    frontend build               137 routes, 120 static pages          PASS
    check:contract               492 routes, 110 permissions (103 gated) PASS
    verify:api                   ALL SCREEN CONTRACTS VERIFIED, from a
                                 database built from zero              PASS
    verify:rbac                  EVERY BOUNDARY HELD                   PASS

`verify:api` and `verify:rbac` were both run against a database rebuilt from
the committed migration chain and reseeded, not against an accumulated one.
That is the difference between this run and every previous one.

## One thing in the brief that the product deliberately does not do

The brief says employees must not be able to change their own password "if the
product rule says credentials are owner-managed". **The Blueprint's rule is the
opposite**, and the implementation follows it: a temporary password is issued by
whoever creates the account and **must be changed on first login** (A5, and the
traceability row for "I create the Owner account with username and password").
Forbidding self-change would make that mandatory first change impossible.

What is enforced: the current password is re-verified, every session including
the caller's own is revoked, an owner-initiated reset requires a written reason
and is permanently audit-logged, and no password is ever readable by anybody.

## Blueprint reconciliation

The 77-feature reconciliation stands as written in the previous section, with
four rows corrected rather than moved: **B9** (promotions) was COMPLETE with
three of its four campaign kinds returning 500; **B11/B12** (order to invoice,
wholesale) was COMPLETE with the on-account path refusing every tax-exclusive
order; **E6** (Saudi labour and payroll) was COMPLETE with an end-of-service
accrual that had never run and would have understated the award when it did;
**F1** (workflow and approval) was COMPLETE with a badge that never rendered.

All four are now what the label said. No row changed status; four rows changed
from claimed to true.

    COMPLETE                     74
    N/A, an engine or optional    3  — C9 posting engine, I3 numbering
                                      engine, E1.3 offline B2B rules
    PARTIAL                       0
    NOT STARTED                   0

---

# PRODUCTION HARDENING — 2026-09-08 (second session of the day)

**Brief:** finish anything still missing, make the product run well on 8GB of
RAM and 40GB of disk, and build a maintenance strategy rather than clearing a
cache once.

Everything below was measured on the machine, not estimated.

## The deployment shipped the wrong application

`docker-compose.yml` built `web/Dockerfile`. `web/` is the first front end: two
routes, a sign-in shell and a portal stub. The product is `web-next` — 137
routes, every screen the Blueprint asks for — and it had **no Dockerfile at
all**.

So `docker compose up` deployed a two-page application, and looked exactly like
a working deployment: both directories build, and a build that succeeds looks
like a build that is right. The old Dockerfile now says NOT WHAT DEPLOYS at the
top, because the way this goes unnoticed is that nothing is obviously wrong.

Building the real one found the next thing. npm selects Tailwind v4's native
Rust binary by platform and architecture and **not** by C library
(npm/cli#4828), so `npm ci` inside alpine installs `oxide-linux-x64-gnu` and
the build dies looking for the musl one. Verified by listing
`node_modules/@tailwindcss` in the alpine deps stage rather than inferred.

| | |
|---|---|
| Back office on `node:22-slim` | 451 MB |
| Back office on distroless | **328 MB** |
| Application inside it | 93 MB |
| Backend (`scratch`, three binaries) | **62.7 MB** |

The runtime is `gcr.io/distroless/nodejs22-debian12`: a Node runtime and
nothing else — no shell, no package manager, running as `nonroot`. The same
reasoning that puts the Go image on `scratch`.

Driven rather than assumed: the container serves `/login`, the manifest and the
icons, reports **healthy**, and idles at **45.6 MiB** of its 384 MB ceiling.

`web-next` also had no icons and no manifest while the front end it replaced had
both, so the product a customer runs showed a default globe in the browser tab
and could not be installed at all. Carried over.

## Eight capabilities the server had and nothing could reach

Found by taking every route in the contract and asking which the front end
never names. The first pass over-reported — a screen builds `/orders/${id}/
${verb}`, so a literal search misses it — and each candidate was then checked
by hand. What survived:

| Capability | Route | Why it mattered |
|---|---|---|
| **Set a price** | `PUT /catalog/variants/{id}/prices` | A retail product where nobody can change a price is not a retail product. All four tiers, and the route's own comment says the point of it was that "a shop could not price its trade customers through the product screens" |
| **Retire a variant** | `DELETE /catalog/variants/{id}` | `catalog.delete`, not `catalog.edit`: taking a line out of the catalogue is not repricing it |
| **Set a credit limit** | `POST /customers/{id}/credit-limit` | The till refuses a sale that would breach a limit — so the product enforced a number nobody could set |
| **Recall a batch** | `POST /stock/batches/{id}/recall` | A shop told its supplier had a contamination problem had no way to act on it. The route answers who bought from the lot, with telephone numbers |
| **Close a year** | `POST /accounting/year-end` | C10 could close twelve months and never the year they belong to |
| **Find a gift card** | `GET /gift-cards/by-code/{code}` | The number is printed on the card in the cashier's hand |
| **Void a gift card** | `POST /gift-cards/{id}/void` | Lost, stolen or issued by mistake, the balance stayed spendable |
| **Fit a part to a repair** | `POST /service-jobs/{id}/parts` | B15's whole point about what warranty work really costs. The screen declared a `ServicePart` type and rendered none |
| **A client's modules** | `PUT /platform/tenants/{id}/features` | H5's commercial flexibility, live and uncalled — and with no GET beside it, so the operator who may grant a module could not see which modules that client had. The read was added for this |

Three backend changes were needed to support them: the variant grid returned
only the retail price (a form offering to edit a wholesale price it had never
been told would send back a blank, and a blank clears it); `EntitlementsOf` is
the platform's read of any tenant, sharing one query with the tenant-scoped
version so the two planes cannot disagree; and `GET /platform/tenants/{id}/
features` did not exist.

## Two defects that only driving could find

Both screens compile and typecheck. Both were wrong.

**Closing a year that does not exist answered 500.** `max(ends_on)` over no
rows is NULL and the scan took it into a `time.Time`, so the request died three
lines above the refusal already written to say "there is no accounting year N
to close". An owner who mistyped a year was told the server had broken. Now
404, with the year named, and a test holds it.

**A repair's parts list would never have populated.** `GET /service-jobs`
answers up to 500 rows and does not carry parts — correctly. `GET
/service-jobs/{id}` is the route that returns them, and the editor reads that
instead.

The 204 from the prices route turned out to be the product behaving and the
driver being wrong: the API client already returns `undefined` on 204.

## What a working copy costs, and what now watches it

The Go build cache reached **6,693 MB** through ordinary work. Nothing was
wrong — that is what a build cache does. What was missing was anything that
ever looked.

`scripts/maintenance.sh` reports, checks against configurable thresholds, and
cleans only what is over one. `make doctor`, `make resource-check`, `make
maintenance`, `make cleanup`.

It touches disposable things only: never a volume, never a running container,
never the module cache unless asked, and **nothing at all when `RAWSYST_ENV`
says production**. There is deliberately no cleanup at application startup — a
build cache emptied on every run is a build cache that never helps.

`du -sm` over that cache **did not finish in ten minutes** on Windows: 24,000
small files through Git Bash's POSIX emulation. The same walk through .NET's
directory enumerator takes **4.7 seconds**. Measured both ways on the same
directory; sizing goes through PowerShell there.

| | Before | After |
|---|---|---|
| Go build cache | 6,693 MB | 0, then 303 MB after one build |
| Stale `web/.next` (a directory nothing deploys) | 645 MB | removed |
| Unused `postgres:16` image | 642 MB | removed |
| npm cache | 710 MB | 710 MB (under threshold) |
| Docker reclaimable | 642 MB | 0 |

A cold `go build ./...` after the clean took **1m13s**, which is the price of
the reclaim and the reason it is threshold-driven rather than routine.

## Memory, measured

| | |
|---|---|
| API serving, resident | **211 MB** |
| PostgreSQL, 13 backends | **175 MB** total |
| Back office container, idle | **45.6 MiB** |
| Free RAM with the editor, the agent and the API all up | ~1.0 GB |

Two real ceilings were missing rather than merely generous:

* **Container logs were unbounded.** The json-file driver has no limit, so a
  service logging a line per request fills a small server's disk and takes the
  database with it — and that arrives as "no space left on device" from
  Postgres, pointing at the wrong thing. Ten megabytes, three files, every
  service.
* **Postgres had no `max_connections`,** which means 100. Each is a process,
  `work_mem` is charged per SORT, and 100 × 8MB is a theoretical 800MB of sort
  memory in a container limited to 1GB. The API asks for at most 20. Fifty.

`docker-compose.small.yml` is the 8GB profile: half the ceilings, Postgres told
to match (64MB buffers, `work_mem` 4MB, 20 connections), `GOMEMLIMIT` so the
collector works harder rather than the container being killed, and a Node heap
sized from the 45.6 MiB that was measured. About 1.1GB in total against the base
file's 2.2GB.

It changes **no durability setting**. No `fsync=off`, no
`synchronous_commit=off`: a development database that corrupts on a power cut
teaches a developer that RawSyst corrupts on a power cut.

## Audited and found already sound

Reported because "we looked" is the useful outcome, not only "we changed":

* **Unbounded queries.** 131 `LIMIT` clauses. Every list over a table that
  grows without bound — audit log, notifications, stock movements, journals —
  is bounded. The queries without one are over sets that are small by nature: a
  journal's own lines, a chart of accounts, a company's investors.
* **The worker.** Single job at a time by construction, with a reaper, a
  pruner, retry limits, exponential backoff, dead-lettering and a ten-second
  grace period on shutdown. Nothing to bound that was not already bounded.
* **Logging.** Info in production and Debug only in development, with a
  `ReplaceAttr` that redacts `password`, `secret`, `token`, `authorization`,
  `jwt`, `refresh_token`, `private_key`, `csid` and `api_key` even if one
  reaches the logger by mistake. No secret is logged anywhere.
* **Graceful shutdown.** `signal.NotifyContext` and a bounded
  `httpSrv.Shutdown` in the API; the worker finishes its claimed job.
* **The Go image.** 62.7 MB is three static binaries (44 MB), zone data
  (1.55 MB) and CA certificates (197 KB) on `scratch`. Nothing to trim without
  splitting it into three images, which its own comment argues against.

## A note the next session will need

**The `rawsyst-design-system` skill describes `web/`, not `web-next`.** It says
"plain CSS custom properties and class primitives — no Tailwind, no CSS-in-JS,
no component library. Do not add one." That is true of the front end that no
longer ships. `web-next` is Tailwind v4 with its own `components/ui`
primitives, `class-variance-authority` and `tailwind-merge`. Following the
skill while working on the product would produce classes that render bare.

## Verification, all of it

    clean migration from zero    130 migrations, 183 tables, 175 forced RLS,
                                 175 policies, 44 rules, 542 CDTFA rates   PASS
    backend, every package       integration tags, fresh test database     PASS
    backend, internal/api        373s                                      PASS
    go vet / vet -tags=integration                                         PASS
    gofmt -s -l                  clean
    lint-wording                 1,418 files                               PASS
    typecheck                    tsc --noEmit                              PASS
    web-next tests               491 passed / 30 files                     PASS
    shared tests                 482 passed / 29 files                     PASS
    production build             137 routes, 121 static pages              PASS
    check:contract               493 routes, 110 permissions (103 gated)   PASS
    verify:api                   ALL SCREEN CONTRACTS VERIFIED, from a
                                 database built from zero                  PASS
    verify:rbac                  EVERY BOUNDARY HELD                       PASS
    the eight new workflows      driven against a running server           PASS
    back-office image            builds, serves, healthy, non-root         PASS

`verify:api` also gained a real check: it sent a delivery with no lot number,
which passed only because no seeded variant was batch-tracked. One is now, and
the check was correctly refused. `tracks_batches` is on the PO line so a
receiving screen knows before it submits; the checker reads it, and the tracked
half of receiving is exercised for the first time.

Unexercised payload shapes against a fresh database: **5**, from 54 at the start
of the day. Two are refused on purpose (a data breach starts a 72-hour
regulatory clock; a failed background job is an incident an operator must act
on), and three need an act this fixture cannot honestly perform — a member of
the public asking for a return, an outbound webhook to a third party, and a
branch that has not set its own country.

## Still backend-only, and why each is left

| Route | Why |
|---|---|
| `POST /store-credit/expire` | A sweep, not a screen. It belongs to the scheduler beside the other expiry jobs |
| `POST /backups/{id}/finish` | Called by the backup process when it completes, machine to machine. `verify` — the human act — is on the screen |
| `POST /payment-gateways/{id}/charge`, `POST /payment-attempts/{id}/refund` | Taking a card payment is a till action, and the till is the Tauri surface. The back office configures the gateway and reads the attempts, which it does |
| `GET/PUT/DELETE /customers/{id}/sizes` | A clothing shop's record of a customer's sizes. Real, small, and genuinely unbuilt |
| `GET /groups/{id}/intercompany`, `POST /groups/intercompany` | F4's intercompany annotation. The group screen covers membership and the consolidated statement; marking an entry as intercompany is not there |
| `POST /notifications/announce` | An owner broadcasting a notice. The centre reads and marks read; it does not send |
| `GET /plans` | The plan catalogue. `/settings/subscription` shows the client's own plan and its entitlements, which is the question a business asks |

The last four are the honest remainder of this phase: each is a route with no
screen, each is small, and none of them blocks a business from trading.

---

# FINAL COMPLETION — 2026-09-09

**Brief:** audit the four remaining screenless routes against the Blueprint,
build UI only where one is genuinely required, hunt for anything else
unreachable, keep the EOSB gate honest, harden for 8GB, and leave the stack
running.

## The four screenless routes

Audited against the Blueprint before anything was written. Three needed a
screen; one needed to be wired somewhere it already belonged.

| Route | Verdict | Why |
|---|---|---|
| **Customer sizes** `GET/PUT/DELETE /customers/{id}/sizes` | **UI built** | Blueprint §442 names it exactly: "store each customer's confirmed sizes ... so staff instantly know their size on the next visit". That is only true if it is on the screen somebody opens when the customer walks in. Three routes live and uncalled since B16 landed; the assistant asked every time |
| **Inter-company marking** `GET /groups/{id}/intercompany`, `POST /groups/intercompany` | **UI built** | F4 says inter-company trade is "tracked and eliminated in consolidation". Elimination was built — the group P&L excludes any entry in `intercompany_entry` and reports what it removed — and nothing could mark. So nothing was ever eliminated and a group that invoiced itself reported the internal sale as group revenue |
| **Announcing a notice** `POST /notifications/announce` | **UI built** | `notification.manage` was a grantable permission with nothing to exercise it: an owner could hand somebody the right to announce and there was no way to announce |
| **Plan catalogue** `GET /plans` | **No new screen, wired instead** | It is the price list, the same for every client — the route says so. H5 asks for tiers and per-tenant flags, not a customer-facing upsell, and `/settings/subscription` already answers "what do I have". Its real consumer was the Platform Owner's tier picker, which was **four strings hard-coded into the page**: adding or renaming a tier would have left an operator moving a client onto a tier the product does not have |

The intercompany form **picks** the entry from the adjustment register rather
than taking a uuid. `intercompany_entry` references the ledger entry, which is
`journal_entry_id` — a different uuid from the journal's own `id` on the same
response. Asking somebody to copy the right one of two between screens is how
the wrong entry gets eliminated.

## What the reachability audit found after that

A route counts as reached when every literal segment of it appears in the front
end, whether written after a slash or passed as a bare string. The naive
version reports every `act('pick')` call as dead and buries the real findings;
this one went 21 → 10 → 9 candidates, each then checked by hand.

**Three real gaps, all of the same shape — something recorded that never
arrived:**

* **The shop's logo.** `/settings/business` carried a "print the logo on
  documents" switch and four routes for the logo itself, and called none of
  them. A shop could promise its mark on every receipt and had no way to supply
  one. Built: upload, replace, remove, with the preview re-read from the served
  file so it cannot show an image the server rejected.
* **Stock reservations never expired.** `ReservationExpirySweeper` was written
  and **registered on the worker**, and the scheduler never enqueued it — the
  exact failure its own comment describes. An abandoned basket held the last
  unit for ever, through every channel. Enqueued hourly.
* **Store credit never expired.** `wallet.ExpireCredit` existed and
  `POST /store-credit/expire` exposed it; no screen, no handler, no schedule. A
  credit note with a twelve-month expiry stayed spendable in year three and the
  liability was a figure nobody could retire. Handler written, enqueued daily.

Writing that sweeper found why it could never have run from a job: the
write-back posted with `PostedBy: &scope.UserID`, and a scheduled job has no
user, so the zero uuid violated `journal_entry_posted_by_fkey`. The column is
nullable for exactly this reason; NULL is honest and uuid-zero is a name
belonging to nobody.

**Six left, each documented rather than built:**

| Route | Why it stays screenless |
|---|---|
| `/pos/stationery`, `/pos/sales/{id}/reprint`, `/pos/sales/{id}/signed-document` | The till's own surface. The back office is not the client |
| `POST /stock/reservations`, `DELETE /stock/reservations/{orderID}` | B13's second sales channel calls these over the API — H6's integration surface. Covered by `TestReservedStockCannotBeSoldTwice` |
| `POST /backups/{id}/finish` | Called by the backup process when it completes, machine to machine. `verify`, the human act, is on the screen |
| `POST /payment-gateways/{id}/charge`, `POST /payment-attempts/{id}/refund` | Taking a card payment is a till action. The back office configures the gateway and reads the attempts, which it does |

**Pages nothing links to: 0**, of 137.

## Deployment: three defects, each found by running it

`docker compose up` produced something that looked healthy and could not be
used.

1. **It could not start at all.** The optional services declared secrets with
   `${VAR:?message}`, and compose interpolates the whole file **before** it
   filters by profile — so the command this file's own header advertises failed
   on three variables belonging to services it was not going to start. Profiles
   cannot fix that. Redis, nginx, minio and prometheus moved to override files
   that declare and validate their own secrets; the base file is the four
   services the product needs.
2. **The back office could not reach the API.** Every proxied request 500'd
   against `http://localhost:8080` inside the web container. Next resolves
   `rewrites()` at **build** time and freezes it into
   `required-server-files.json`, so the runtime variable compose set was read by
   nothing. It is a build argument now, defaulting to the compose service
   rather than to localhost.
3. **Nobody could sign in.** A fresh deployment migrates cleanly, comes up
   healthy and has no user: a platform operator is a user with no tenant, and
   every route that could create one sits behind the guard it would be needed to
   pass. `devseed` refuses in production and would invent a demo shop anyway. So
   the product was correct and unusable, and the first actor in its own business
   model could not get in.

   **`cmd/bootstrap`** creates that operator and nothing else — no tenant, no
   business data — prints a generated password once, requires it changed at
   first sign-in, and **refuses if any platform operator already exists**,
   counted inside the inserting transaction. That refusal is what separates a
   bootstrap from a back door.

## EOSB — what is implemented and what is not

Everything RawSyst owns is done and was verified this session:

| | |
|---|---|
| Both service bands | read from the rule; the band is decided by service at the month charged |
| Wage basis | read from the rule, closed vocabulary, unknown basis refused **by name** |
| Accrual | posts, one charge per person per month, append-only |
| Rule resolution | dated, per-tenant override honoured, cache invalidated on write |
| Refusal while unverified | `Decimal()` refuses on `__VERIFY__` regardless of strict mode |
| Recording flow | `/platform/rules`, supersedes rather than overwrites, `__VERIFY__` cannot be written back, an unverified value must carry a note |
| Audit | `verified_by`/`verified_on` stamped; history preserved by superseding |
| Point-of-use gate | every use refused while unverified |
| Production boot gate | a deployment serving that market refuses to start |

**Neither gate was weakened.** What changed is that the screen now says what to
do: it names each blocking rule, lists which payload fields still hold
`__VERIFY__`, links the published document, says why RawSyst cannot supply the
figure, and states what happens once it is recorded. The record button seeds the
form from the rule being replaced so the operator types the **figure** rather
than re-entering the key, country, authority and document already on record.

**The only external input in the product** is a person reading the end-of-service
award in the Saudi Labour Law and putting their name to having read it.

## Resources, measured on the running stack

| Container | Resident | Ceiling |
|---|---|---|
| `db` | 18.5 MiB | 384 MiB |
| `api` | 70.2 MiB | 256 MiB |
| `web` | 47.6 MiB | 256 MiB |
| `worker` | 4.2 MiB | 128 MiB |
| **total** | **~140 MiB** | **1,024 MiB** |

Database connections inside the stack: **8 of 20**.

| Image | Size |
|---|---|
| `rawsyst/backend` | **76.2 MB** (`scratch`, four static binaries, non-root) |
| `rawsyst/web` | **329 MB** (distroless, no shell, non-root) |
| `postgres:17-alpine` | 424 MB |

The backend grew 62.8 → 76.2 MB when `bootstrap` was added, which is the price
of a deployment anybody can sign into.

Docker build cache reached **6,641 MB** across the session's image builds and
was reclaimed by `make maintenance` — **with all four containers left running
and healthy, and no volume touched.**

Fixing that cleanup found a defect in the tool itself: `docker system df` puts
the value and its unit in one field (`6.364GB`, with `(13%)` as the second), and
the parser tested the unit against the percentage. It never matched GB, read
everything as megabytes, and reported 6.4 GB as 6 MB — then said nothing was
over threshold, which is the worst failure available to a tool whose job is to
notice.

## What maintenance now reports and does

`make doctor` · `make resource-check` · `make maintenance` · `make cleanup`

Reports: Go build and module caches, npm cache, Next output, build artefacts,
logs, Docker reclaimable and this project's image sizes, free disk, **RAM in use
against total**, **this project's own processes by name with their working
set**, and **database connections against `max_connections`** — every one a
server-side process, which is the RAM figure a misconfigured pool moves.

Judges two things and refuses a third: it says to run the heavy suites one at a
time below a free-memory floor, warns when more than one API process is running
(the second cannot have bound :8080), and **never kills anything** — an editor's
language server is also a node process.

Cleans only what is over a configurable threshold. Never a volume, never a
running container, never the module cache unless asked, and **nothing at all**
when `RAWSYST_ENV` says production. There is no cleanup at startup: a build
cache emptied on every run never helps.

## Verification

    clean migration from zero    130 migrations, 183 tables, 175 forced RLS,
                                 175 policies, 44 rules, 542 CDTFA rates    PASS
    backend, every package       integration tags, fresh test database      PASS
    backend, internal/api        335s                                       PASS
    go vet / vet -tags=integration                                          PASS
    gofmt -s -l                  clean
    lint-wording                 1,423 files                                PASS
    typecheck                    shared and web-next                        PASS
    web-next tests               491 passed / 30 files                      PASS
    shared tests                 482 passed / 29 files                      PASS
    production build             137 routes, 121 static pages               PASS
    check:contract               493 routes, 110 permissions (103 gated)    PASS
    verify:api                   ALL SCREEN CONTRACTS VERIFIED, fresh DB    PASS
    verify:rbac                  EVERY BOUNDARY HELD                        PASS
    the four routes, driven      sizes recorded/corrected/forgotten/refused
                                 cross-tenant; notice refused empty then
                                 delivered; price list four tiers           PASS
    intercompany, tested         marking eliminates, unmarking restores,
                                 a cashier is refused                       PASS
    credit expiry, tested        lapsed retired, in-date left, twice is once,
                                 no tenant fails permanently                PASS
    docker images rebuilt        backend 76.2MB, web 329MB                  PASS
    docker compose up            four containers healthy                    PASS
    bootstrap                    operator created, second attempt refused   PASS
    sign-in through :3000        200, proxied to the API container          PASS

Unexercised payload shapes against a fresh database: **6**. Two are refused on
purpose — a data breach starts a 72-hour regulatory clock and a failed
background job is an incident an operator must act on — and four need an act
this fixture cannot honestly perform: a member of the public asking for a
return, an outbound webhook to a third party, a branch that has not set its own
country, and a shift outside the register's window.

## Blueprint

    COMPLETE                     74
    N/A, an engine or optional    3  — C9 posting engine, I3 numbering
                                      engine, E1.3 offline B2B rules
    PARTIAL                       0
    NOT STARTED                   0

B16 (fitting history) and F4 (inter-company) were COMPLETE against their
backends and are now complete against a user. I1/I2 gained the logo the
document template already promised.

---

# COMPLETION PASS — 2026-09-09 (second session of the day)

**Brief:** audit the whole repository independently, do not trust the previous
report, remove the software-side EOSB blocker, make the development Platform
Owner `mahedi.emon62@gmail.com`, fix every unreachable capability, verify, and
leave the stack running.

## The audit found the previous reachability figure was measured wrongly

The first session of 2026-09-09 reported **six** screenless routes. That number
came from searching `web-next/src`, `shared/src` and `pos/src` together for each
route's path.

**`web-next` imports nothing from `shared/src/api`.** It uses `shared` for the
i18n strings and for nothing else — three files, all `@rawsyst/shared/i18n`.
`shared/src/api/*` is the *frozen* front end's client layer and the till's. So
every route that had a client function in `shared/src/api` counted as reached
while no screen in the deployed back office could call it.

Measured against `web-next/src` alone, of **498** routes:

| | before this pass | after |
|---|---|---|
| path written out in a screen | 422 | 434 |
| reached with the last segment computed (`/orders/${id}/${what}`) | 49 | 44 |
| **nothing reaches** | **27** | **20** |

The 20 are itemised below, each with a verdict. Seven were closed by building
the screen; nothing was closed by widening the measurement.

## EOSB — the software-side dependency is gone

### What was investigated

Whether the entitlement figures can be obtained and checked programmatically.
They cannot, and the reason is a fact about the source rather than about this
product: Saudi primary legislation is published as prose. The Bureau of Experts
serves it as HTML only, not in bulk and not as data; the Open Data Platform
carries labour datasets but not the statute's parameters. A ministry
knowledge-centre page states the award in English prose, and scraping a summary
page into `days_per_year_first_five` would present a parsed sentence as a figure
the product had established.

So the figures stay outside. **What was software's to fix is that they could
only get in through a browser.**

### What was built

`cmd/regulatory` and `internal/registry/attest.go` — an attested regulatory
source file.

    regulatory                                  what is still outstanding; exits
                                                non-zero while one blocks release
    regulatory -template -country sa > s.json   the file to fill in, generated
                                                from what THIS installation wants
    regulatory -check  -file s.json             validates, writes nothing
    regulatory -apply  -file s.json             records it

One person reads the cited articles once, writes the figures and their name in a
file, and every environment after that consumes the same file with no human in
the loop. The template carries the citation, the articles, each field's label,
unit and meaning, and the date rule — everything software can legitimately
establish.

**Five refusals keep it from becoming a way to invent law:**

* only rules the embedded source pack describes; an unknown key is refused
* only fields that currently hold `__VERIFY__`; correcting a recorded figure
  stays on the screen where a person sees what they are replacing
* every value checked against what the pack says it is — a choice must be one of
  the choices, a fraction must lie between 0 and 1 (`33` for a third is refused
  by name, because the arithmetic would be valid and would pay thirty-three
  awards)
* `read_by` must resolve to an active platform operator **on this installation**
* `__VERIFY__` cannot be written back

Idempotent: the same file applied twice records once. Supersession, the audit
entry and the cache invalidation are the screen's, because it calls `RecordRule`
rather than reimplementing it.

**The effective date moves forward and cannot move back.** A rule resolves at
the date of the document being processed, so a period the product refused to
compute must go on refusing when a report is re-run. An attestation therefore
takes effect *after* the placeholder it replaces; the article's own date belongs
in the citation, and the template says so.

Shipped in the image as `/regulatory` and as a compose `setup` service, because
the API refuses to start while a blocker is open and the screen that could clear
it is served by that API. That circle is what the file breaks.

### Two defects found while building it

1. **`RecordRule` wrote no audit entry.** The neighbouring rate workflow has had
   audited review, verification and activation since 0120. Recording the legal
   value itself — the most consequential act a platform operator performs, since
   every business in the market computes from it — was recorded nowhere, and for
   an *unverified* record `verified_by` is NULL so nobody was named at all.
2. **The resolver's cache keyed by MONTH.** Sound while every value was seeded by
   a migration or superseded on the first of something. An attestation's
   effective date is the day somebody records it, almost never the first — so a
   placeholder in force until the 9th and a verified figure from the 9th shared
   one key, and whichever a process resolved first governed the whole month *for
   that process*. Two API instances could disagree about one payroll run with
   nothing reporting a conflict. Keyed by the exact date now, bounded at 4,096
   entries by clearing rather than by adding an LRU and a second lock.

### Status

**Software-complete.** The remaining input is one person reading Articles 84 and
85 and putting their name to what they read. Both gates are unchanged: every
point of use refuses while unverified, and a production deployment serving that
market refuses to start.

## Platform Owner

`mahedi.emon62@gmail.com`, one account, moved rather than duplicated.

* **Development database** — already this address. `cmd/devseed` moves the
  existing operator rather than adding a second, and reads
  `RAWSYST_PLATFORM_EMAIL` from `backend/.env`.
* **Compose deployment** — was `owner@rawsyst.test`. Recovered, then moved
  through the product's own `PUT /platform/operators/{id}/email`, so the trail
  carries `platform_operator_email_changed` rather than a hand-written UPDATE.
  One operator, `must_change_password` true.
* Credentials live in `.env` files, both gitignored. Nothing is in source.

### `cmd/bootstrap -recover`, and why it is not a back door

A deployment with ONE operator who has lost their password had no way back in.
`bootstrap` refuses because an operator exists — the refusal that makes it safe
to ship. The forgotten-password flow needs mail a fresh deployment may not have.
`POST /platform/users/{id}/reset-password` needs the Super Admin session that has
been lost. The only remedy was hand-written SQL against production.

`-recover` issues a one-time password for an operator who **already exists**,
refuses to create one, refuses a business user, revokes every session the account
holds, and is audited. It adds no authority: whoever can run it already holds the
database credentials, and anybody holding those can insert an operator by hand.
Four tests pin exactly those properties.

### Four audit entries named nobody

`actor_label` is denormalised precisely so the trail survives a user row being
deleted — the audit package's own comment says so, and names the defect it was
written to stop. All four entries in `internal/identity` were committing it:
signing in, changing a password, a Super Admin resetting one, and a refresh token
presented twice. The last two are what an incident review reads first. Fixed,
with `TestTheTrailNamesWhoActed` and `TestChangingAPasswordNamesTheActor` holding
it.

## Seven unreachable capabilities, built

| Capability | Routes | Why it mattered |
|---|---|---|
| **Open and amend a branch** | `POST`/`PUT /companies/{id}/branches` | The table was read-only. A Saudi branch cannot invoice without its National Address, the till refuses the sale citing BR-KSA-09/37/66, and the screen showing that refusal offered no form to answer it. A shop could be blocked from trading by four empty boxes |
| **Amend a person; give and take away a role** | `PUT /people/{id}`, `POST /people/{id}/roles`, `DELETE /people/roles/{id}` | `identity.manage_roles` was grantable with nothing to exercise it beyond the create form. Promoting a cashier, moving somebody between branches, taking an approval limit off a leaver: none possible, and the product refuses to delete a person because their name is on the invoices they rang up |
| **Reset a platform operator's password** | `POST /platform/users/{id}/reset-password` | `cmd/bootstrap` says to do this "in Super Admin" and there was nowhere |
| **Adjust a customer's points** | `GET`/`POST /loyalty/members/{id}[/adjust]` | `loyalty.manage` had no per-customer workflow. The alternative to it is ringing up a fake sale, which puts revenue and tax on a month that did not earn them |
| **Give store credit** | `GET /wallets/{id}`, `POST /wallets/{id}/credit` | The same, for `wallet.manage` |
| **Group members** | `POST /groups/{id}/members`, `DELETE .../{memberID}` | `group.manage` could create a group and nothing else, so every group was empty, the consolidated statement had nothing to consolidate, and F4's inter-company elimination had no members whose trade it could eliminate |
| **Tax authorities and rates** | `POST /platform/jurisdictions`, `.../{id}/rates`, `.../import` | E8's own note: "the operation could only be performed with a SQL client against production". The only rates the product has are a migration's; no operator could add a county, correct a rate, or bring in next quarter's schedule |

Two defects in that last screen were found by driving it against a live server
rather than by reading it: it offered a jurisdiction level the CHECK constraint
forbids (`special`), and it let a non-country level be saved with no parent —
`tax_jurisdiction_root_is_country` refuses that, and a parentless county is a
county whose state's share silently never applies. Both are the form's own rule
now, and `parseSchedule` moved to `src/lib/tax/schedule.ts` with eight tests,
including a name with a comma in it (`Rancho Santa Fe, unincorporated` is a real
CDTFA row, and splitting on the comma shifts every column right).

## The twenty that remain, each with a verdict

**Correctly screenless — infrastructure, machine-to-machine, or the till (10):**

| Route | Category |
|---|---|
| `/healthz`, `/readyz`, `/metrics`, `/meta/version` | E — infrastructure |
| `/meta/ping` | B — a terminal asking whether it can sync |
| `/live` | B — the websocket a screen opens, not a screen |
| `POST /store-credit/expire` | D — enqueued daily by the worker |
| `DELETE /stock/reservations/{orderID}` | C — B13's second sales channel, over the API |
| `POST /payment-attempts/{id}/refund` | B — a till action |
| `POST /eosb/accrue` | D — a monthly charge, run by the payroll job so the liability is never discovered at termination |

**Genuinely missing, and NOT built in this pass (10):**

| Route | What is missing |
|---|---|
| `PUT /onboarding/steps/{step}`, `POST /onboarding/steps/{step}/complete` | A5's setup wizard has no front end in `web-next`. `shared/src/api/onboarding.ts` is the frozen front end's client. A business is usable without it — the platform provisions the tenant and `/settings/business` configures it — but the wizard the backend supports is unreachable |
| `POST /purchasing/payments/{id}/reverse` | A supplier payment cannot be reversed from a screen |
| `POST /receivables/receipts/{id}/reverse` | Nor a customer receipt |
| `POST /settlement/batches`, `GET /settlement/batches/{id}` | Recording a card settlement, and reading one back |
| `GET /investors/{id}/statement` | One investor's statement; the register shows movements only |
| `PUT /labels/barcodes/{variantID}` | B3's manual barcode override |
| `PUT`/`DELETE /labels/templates/{id}` | A label template can be created and listed, not edited or removed |
| `DELETE /privacy/activities/{id}`, `GET /privacy/destructions` | Removing a processing activity; the destruction record |
| `GET /reports/workforce` | The head count and ratio |
| `GET /notifications/unread` | The bell's own count |

These were **recorded, not resolved** by that pass. All ten are built and
verified in the FINAL FRONTEND COMPLETION PASS below, along with twenty-three
more that a corrected, method-aware audit found. The figure of twenty in the
table above is itself superseded: it counted a PATH rather than a route, so
`GET` and `POST` on one path read as a single entry.

## Verification

    clean migration from zero    130 migrations, 183 tables, 175 forced RLS,
                                 175 policies, 44 rules, 542 CDTFA rates    PASS
    backend, every package       integration tags, fresh test database      PASS
    backend, internal/api        330s                                       PASS
    go vet / vet -tags=integration                                          PASS
    gofmt -s -l                  clean
    lint-wording                 1,445 files                                PASS
    typecheck                    shared and web-next                        PASS
    web-next tests               499 passed / 31 files                      PASS
    shared tests                 482 passed / 29 files                      PASS
    production build             122 static pages, no warnings              PASS
    check:contract               498 routes, 110 permissions (103 gated)    PASS
    verify:api                   ALL SCREEN CONTRACTS VERIFIED              PASS
    verify:rbac                  EVERY BOUNDARY HELD                        PASS
    regulatory, in the container report, template, and a check that refuses
                                 an unattested file by name                 PASS
    bootstrap                    refuses a second operator                  PASS
    bootstrap -recover           issues, forces a change, ends every
                                 session, audits; refuses a non-operator
                                 and refuses a business user                PASS
    branches, driven             opened, amended, closed                    PASS
    people, driven               amended, role given, role taken away       PASS
    rewards, driven              wallet read, credit given, card read,
                                 points adjusted                            PASS
    tax admin, driven            authority added under its country, rate
                                 recorded, schedule imported                PASS
    docker compose up            four containers healthy                    PASS
    sign-in through the stack    operator recovered, moved, signed in       PASS

Not exercised: the group-member routes need a plan that sells group
consolidation and the demo tenant's does not — the screen's own 402 state covers
that case. The backend suite exercises the routes.

## Resources, measured on the running stack

| Container | Resident | Ceiling |
|---|---|---|
| `db` | 17.6 MiB | 384 MiB |
| `api` | 6.7 MiB | 256 MiB |
| `web` | 47.1 MiB | 256 MiB |
| `worker` | 4.6 MiB | 128 MiB |
| **total** | **~76 MiB** | **1,024 MiB** |

Database connections: **8 of 20**. `shared_buffers` 80 MB, `work_mem` 4 MB,
`effective_cache_size` 240 MB. Durability untouched: `fsync`,
`full_page_writes` and `synchronous_commit` all on, data checksums on.

**An operational trap worth knowing.** The database container was running with
the BASE file's 1 GiB ceiling although `up -d --build` had been given both files;
the merged config was correct and the running container was not.
`docker compose … up -d --force-recreate db` applied it. After changing a
resource limit, force-recreate the service rather than trusting `up`.

| Image | Size |
|---|---|
| `rawsyst/backend` | **89.7 MB** (`scratch`, five static binaries, non-root) |
| `rawsyst/web` | **329 MB** (distroless, no shell, non-root) |
| `postgres:17-alpine` | 424 MB |

The backend grew 76.2 to 89.7 MB when `regulatory` was added, which is the price
of a deployment that can record its own legal values without a shell.

Caches: Go build 1,507 MB, Go modules 610 MB, npm 710 MB, Next output 652 MB,
Docker reclaimable 3,855 MB — every one under its threshold, so `make
maintenance` cleans nothing. 12 GB free on the volume.

## Markers

`TODO`, `FIXME`, `not implemented`, `coming soon`: **none** in `backend`,
`web-next/src`, `shared/src`, `pos/src` or `scripts`. The only matches in the
tree are inside `web-next/.next`, which is Next.js's own generated output.

`__VERIFY__`: **five rules**, one of them release-blocking
(`SA.EOSB.ENTITLEMENT`). All five are described by the source pack, so all five
can be recorded from a file. That is the mechanism, not a gap.

# FINAL FRONTEND COMPLETION PASS — 2026-09-09 (third session of the day)

The ten capabilities the previous pass recorded and did not close are closed.
So are twenty-three more that a corrected audit found while closing them.

## The ten, each verified end to end

Every one was driven against a running API before being marked complete —
`scripts/reachability.mjs` proves the route is reached from a screen, the Go
suite proves the service, and a live drive proves the two meet.

| Capability | Screen | Routes it reaches | Permission |
|---|---|---|---|
| **Setup Wizard — COMPLETE** | `/setup`, new | `GET /onboarding`, `PUT /onboarding/steps/{step}`, `POST /onboarding/steps/{step}/complete`, `POST /onboarding/company`, `POST /onboarding/stores` | `identity.view` to read, `identity.edit` to act |
| **Reverse Supplier Payment — COMPLETE** | `/buying/payments`, second half | `GET /purchasing/payments` (new), `POST /purchasing/payments/{id}/reverse` | `purchasing.view` reads, `purchasing.pay_supplier` acts |
| **Reverse Customer Receipt — COMPLETE** | `/money/receipts`, second half | `GET /receivables/receipts`, `POST /receivables/receipts/{id}/reverse` | `customers.view` reads, `sales.receive_payment` acts |
| **Settlement Batches — COMPLETE** | `/money/settlement`, new | `GET /settlement/pending`, `GET /settlement/batches` (new), `POST /settlement/batches`, `GET /settlement/batches/{id}` | `accounting.view` reads, `accounting.create` records |
| **Investor Statement — COMPLETE** | `/money/investors/[investorID]`, new | `GET /investors/{id}/statement` | `investor.view`; the service confines an investor to their own |
| **Barcode Override — COMPLETE** | `/products/labels`, print-run panel | `PUT /labels/barcodes/{variantID}` | `label.manage` |
| **Label Template Editor — COMPLETE** | `/products/labels`, editor panel | `PUT`/`DELETE /labels/templates/{id}` | `label.manage`; `label.print` opens the screen |
| **Privacy Destruction Record — COMPLETE** | `/oversight/privacy?on=destructions` | `GET /privacy/destructions`, `DELETE /privacy/activities/{id}` | `privacy.view` reads, `privacy.manage` removes |
| **Workforce Report — COMPLETE** | `/reports/workforce`, new | `GET /reports/workforce` | `report.view` |
| **Notification Count — COMPLETE** | the bell in the shell, every screen | `GET /notifications/unread` | authenticated; the query names the caller and nobody else |

## Six defects the work found, and what each would have cost

1. **`GET /purchasing/payments` did not exist.** `POST .../reverse` had been
   live since payables landed, was tested, and was reachable from no screen at
   all — a reversal names a payment id and nothing in the product could tell
   you one. The route was not missing UI; it was missing its other half.
2. **`GET /settlement/batches` did not exist** either. Recording a deposit
   answered with an id once, in a response, and nothing could ever find it
   again. A settlement module you can only write to is one nobody finishes a
   month with.
3. **A recorded deposit stated no currency.** `readBatch` reads
   `base_currency` and the create path built its struct by hand without it, so
   recording a deposit answered `""` and recording the SAME deposit twice
   answered `SAR`. On the one screen whose whole job is matching a figure to a
   bank statement.
4. **Four money-moving acts named nobody.** Reversing a supplier payment,
   reversing a customer receipt, recording a settlement and overriding a
   barcode wrote no audit entry. The journal said the ledger moved; nothing
   said who decided it. All four are audited now, and the two reversals and the
   override carry a REASON — written into the trail, not onto the document,
   because a payment is a fact about money and carries no editorial.
5. **A label promised a line it could not print.** The loyalty card seeded by
   0095 carries a `tier` field the renderer has no case for and the print run
   has no data for, so it printed a blank. Migration 0131 removes it, the
   provisioning seed no longer writes it, and `labels.PrintableFields` now
   refuses a field the printer cannot fill — the failure it prevents is silent:
   the layout is right, a line is absent, and nobody finds out until nine
   hundred tags are on a rail.
6. **The investor statement was a list, not a statement.** It answered the
   movements inside a window and no balances, so the closing figure could be
   checked against nothing. It now carries the opening (everything before the
   window), the two totals inside it, and the closing — the arithmetic a paper
   capital account does — and refuses a period that ends before it starts
   rather than silently widening to all time.

## Twenty-three more gaps, found by measuring properly and fixed

The previous audit counted 20 unreachable routes. A method-aware audit counts
differently: `GET /settlement/batches` and `POST /settlement/batches` are two
routes, and a screen that only records a deposit has not reached the one that
lists them. Measured that way the figure was 52, of which 20 are intentional.

| Fixed | Screen |
|---|---|
| `GET`/`PUT /devices/{id}/settings` | `/settings/devices` — a till could be registered and never told which warehouse it sells out of. `settingsChange` in `lib/devices/hardware.ts` had been written, tested and never called |
| `GET`/`PUT /einvoicing/units/{unitID}` | `/settings/einvoicing` — a signing unit with a typo in its serial number could never be corrected, and those nine fields are what ZATCA signs against |
| `POST /customers/{id}/active` | `/customers/[customerId]` — retiring a customer, refused by the server while they owe money |
| `GET`/`PUT`/`DELETE /groups/{groupID}` | `/oversight/groups` — a group could be created and never renamed or removed |
| `DELETE /documents/{documentID}` | `/oversight/documents` |
| `DELETE /companies/{id}/templates/{docType}` | `/settings/business` — putting a document back on the product default, which is not the same as saving an empty header |
| `POST /purchasing/rfqs/{id}/cancel` | `/buying/quotes/[rfqID]` — a request sent to four suppliers by mistake stayed open for ever |
| `GET /purchasing/suppliers/{id}/quotes` | `/buying/suppliers` — B5.1's archive, so the next negotiation starts from a fact |
| `POST /pos/sales/{invoiceID}/reprint` | `/sales` — the CONTROL rather than the printing; a second copy of a tax invoice in circulation is what an inspector asks about |
| `GET /shifts/{sessionID}/x-report` | `/shifts` — the figure a drawer is checked against, `report.view`, which a cashier deliberately does not hold |
| `GET /dashboard/expenses`, `/dashboard/compliance`, `/dashboard/stock` | `/dashboard` — three drill-throughs behind figures a reader could not open. The compliance one states the P1 gate honestly rather than offering a retry that cannot work |
| `GET /deliveries/{deliveryID}` | `/deliveries` — every step a consignment has been through, which is the answer to "where is my order" |
| `GET /gift-cards/{cardID}` | `/customers/wallets` — reading the one card back after voiding it |
| `GET /stock/availability` | `/stock` — on hand, reserved, and free to sell; the on-hand column alone over-promises |
| `POST /promotions/quote` | the till — "check for offers", asked for rather than applied on its own, because a price that moved between the scan and the total is the one thing a cashier cannot explain |
| `POST /platform/tenants/{id}/invoices`, `POST /platform/dunning` | `/platform/billing` — the screen could mark an invoice paid and could not raise one, so every subscription invoice this product ever billed was inserted by hand |
| `GET /companies/{id}/logo/image` | already reached by an `<img src>`; the AUDIT could not see it, which was the audit's defect rather than the product's |

## The reachability audit is a checked-in script now

`web-next/scripts/reachability.mjs`, run as `npm run check:reach`. It failed
this measurement twice by hand and the methodology is now source rather than
memory:

* **`web-next/src` only.** `shared/src/api/*` is the frozen `web/` front end's
  client layer and the Tauri till's; `web-next` imports from `shared` in
  exactly three files, all `@rawsyst/shared/i18n/strings`. Counting the three
  trees together reported 6 unreachable routes where counting the deployed one
  reports 52.
* **`contract.generated.ts` excluded**, because it lists every route pattern in
  the product and scanning it reports everything as reached.
* **Method-aware.** `GET` and `POST` on one path are two routes.
* **Three matching buckets**: the path written out, a path with interpolated
  segments, and a pattern parameter matching either. An exact-string test
  reports every templated path dead; a segments-appear-anywhere test reports
  nothing dead.
* **Comments stripped before the loose pass.** This codebase names routes while
  explaining itself — `client.ts` mentions `/sync/push` in a comment about
  idempotency keys — and counting prose as a call reported the offline queue as
  reachable from the back office.
* **A one-segment interpolation matches nothing.** `/${kind}` was matching
  `/healthz`, `/readyz`, `/metrics` and `/live` at once, so four infrastructure
  probes read as reached from a screen.

Two buckets are reported separately and honestly: 414 routes are reached by a
call this scanner can attribute a VERB to, and 66 by a path a screen writes
through a local wrapper (`act('/payroll/${id}/approve')` calls `api.post` two
lines up, and no regex follows that without a type checker). `--verbose` lists
the second bucket.

## The twenty intentionally screenless routes

Each carries its reason in `SCREENLESS` in the script, so an exemption cannot
become a silent one. A stale exemption fails the run too: a route somebody
later builds a screen for is reported as an exemption nobody needs.

**Infrastructure (4)** — `GET /healthz`, `GET /readyz`, `GET /metrics`,
`GET /api/v1/meta/version`. Read by an orchestrator, a scraper and a support
engineer.

**The installed terminal (8)** — `GET /meta/ping`, `GET /catalog/scan`,
`GET /pos/stock`, `GET /pos/stationery`,
`PUT /pos/sales/{invoiceID}/signed-document`, `POST /sync/push`,
`GET /sync/health`, `GET /live`. The Tauri till is a different application: it
holds a device secret, an offline catalogue and a signing key, and every one of
these is resolved from the DEVICE rather than from a company a browser names. A
back-office session has no device secret and would be refused, so a screen for
one here would be a control that cannot work.

**A machine enrolling itself (2)** — `POST /devices/enrol`,
`GET /devices/identity`. Before it has a session.

**Jobs and agents (3)** — `POST /store-credit/expire` and `POST /eosb/accrue`
are enqueued by the worker; a person expiring credit by hand is the bug, and a
liability discovered at termination is what the monthly accrual exists to
prevent. `POST /backups/{id}/finish` is the backup agent reporting where it put
the file and what its checksum is — a person typing a checksum would be
attesting to something they did not compute.

**The second sales channel (2)** — `POST /stock/reservations` and
`DELETE /stock/reservations/{orderID}`. B13's channel holds and releases stock
over the API.

**The counter (1)** — `POST /payment-attempts/{attemptID}/refund`, taken at the
till with the customer present.

## Verification

    clean migration from zero    131 migrations, 183 tables, 175 forced RLS,
                                 175 policies, 44 rules, 542 CDTFA rates    PASS
    backend, every package       integration tags, fresh test database      PASS
    backend, internal/api        384s                                       PASS
    go vet / vet -tags=integration                                          PASS
    gofmt -s -l                  clean
    lint-wording                 1,482 files                                PASS
    typecheck                    shared and web-next                        PASS
    web-next tests               598 passed / 37 files                      PASS
    shared tests                 482 passed / 29 files                      PASS
    production build             125 static pages, no warnings              PASS
    check:contract               500 routes, 110 permissions (103 gated)    PASS
    check:reach                  500 routes, 414 attributed, 66 via a
                                 wrapper, 20 intentional, 0 gaps            PASS
    verify:api                   ALL SCREEN CONTRACTS VERIFIED              PASS
    verify:rbac                  EVERY BOUNDARY HELD                        PASS
    the ten, driven live         against a seeded database                  PASS

### What the live drive proved that a test could not

A test builds its own fixtures. This drove the ten screens' own requests against
the seeded development database:

* a supplier payment reversed, the replay recognised as the same reversal, a
  second reversal refused, and the picker no longer offering what it cannot undo
* a customer receipt reversed, and pressing twice reversing once
* an investor statement opening at nothing, echoing its period, and refusing a
  backwards one
* a label created, edited to a 40mm roll, refused a field the printer cannot
  fill, and removed
* a barcode overridden, appearing in the next print run, and SCANNING — which
  is the whole point of a barcode and the one thing a saved code does not prove
* the destruction log read, and a processing activity added and removed
* the workforce report, its departments accounting for everybody
* the bell's count agreeing with the list's
* and a whole tenant provisioned, taken through all six setup steps, refused a
  country it was not sold into, refused a branch with no National Address, and
  finishing with a company, a branch and a stock location — a shop that can
  record a sale

Settlement's read path and empty state were driven; the record-and-read-back
path has no card takings in the seeded database and is covered by
`TestARecordedDepositCanBeFoundAgain`.

### One test was order-dependent and is not any more

`TestVerifiedRulesCarryTheirEvidence` walks every verified regulatory rule and
demands a citable source document. `internal/registry`'s attestation tests seed
a fixture rule under country `zz` — ISO 3166's "unknown country" — and
`regulatory_rule` is append-only by trigger, so the fixture cannot be cleaned up
and stays in whatever database the suite ran against. The invariant now excludes
`zz` and only `zz`: demanding a legal citation for a rule whose country is
"unknown" is demanding evidence for something no authority published. It is
unchanged for SA, BD and US.

## EOSB — unchanged, and deliberately

Both service bands, the wage basis, the accrual, dated rule resolution, the
audit entry, point-of-use enforcement and the production gate are all
implemented. `SA.EOSB.ENTITLEMENT` remains unverified and release-blocking, and
`make regulatory` still reports five outstanding values with one blocking.
Nothing in this pass touched it, and nothing hard-codes a guessed figure. The
remaining input is one person reading Articles 84 and 85 and putting their name
to what they read, through `make regulatory-template` / `-check` / `-apply`.

## Markers

`TODO`, `FIXME`, `not implemented`, `coming soon`: **none** in `backend`,
`web-next/src`, `shared/src`, `pos/src` or `scripts`. Every match for
`placeholder`, `stub`, `mock` and `fake` is prose in a comment explaining why
something is NOT one — `cache.go`'s "Neither is a stub", `finalize.go`'s "Not
stubbed", `templates.tsx`'s "A preview, not a mock editor". `__VERIFY__` is the
regulatory mechanism, described above.

# END OF SERVICE: THE SOFTWARE IS FINISHED — 2026-09-10

Two things were true of `SA.EOSB.ENTITLEMENT` at the start of this pass, and
every previous entry in this document ran them together:

* a figure in the Saudi Labour Law that nobody has read and put their name to
* software that was not finished

The first is still true and cannot be fixed by writing code. The second was also
true, in four specific ways nobody had named, and all four are closed here.

The distinction matters because getting it wrong has a cost in both directions.
Calling missing data a software blocker makes a finished engine look unbuilt and
stops a deployment that has nothing wrong with it. Calling missing software a
data problem leaves a hole nobody is looking for.

## What was actually unfinished

### 1. Three legal figures were collected and never used

`SA.EOSB.ENTITLEMENT` has carried three resignation fractions since 0092. They
are required by the rule, validated on import, refused if they exceed 1, carried
in the payload, shown on the registry screen — and read by nothing.

Article 85 reduces the award for somebody who RESIGNS, banded by length of
service. Nothing in this product applied it, because nothing in this product
could compute an award on leaving at all. `GET /eosb` answers the PROVISION: the
sum of the monthly charges. The employee screen showed that figure under the
heading "What the business would owe if this person left today", which is a
different sum and was not what was on the screen.

So a shop could accrue for a leaver for eleven years and, on the day they
resigned, had no way to find out what they were owed.

### 2. Article 85 has a fourth band and the rule had three fields

Under two years, two to five, five to ten — and then nothing. A person who
resigns after eleven years is in a band the rule could not express, so the
software would have had to assume a figure.

`0132` adds `resignation_fraction_over_ten_years` as a fourth `__VERIFY__`. The
count of unfilled fields goes UP, which is the correct direction: the rule was
previously incomplete in a way that would have surfaced as a wrong number rather
than as a refusal. Nothing is filled in and nothing is guessed.

The payload is edited in place rather than superseded, the way 0044, 0046, 0059
to 0062 correct a shape. Superseding is for a FIGURE changing, because a report
re-run for last March must still give March's answer. No figure changes here:
every value in this rule is a placeholder, on this date and on every date since
0092, and what the product believed throughout is "nobody has read the article".

### 3. A month of service was counted before it was worked

`monthsBetween` counted calendar months and ignored the day, so somebody who
joined on the 20th had a full month of service on the 1st, twelve days later.

It was invisible while the only caller was the accrual, which charges on the
first of each month and where one month too many changes only which band a
charge lands in. The settlement made it visible and made it money: a person who
joined on the 20th and resigned on the 5th of their second anniversary month was
moved into the next Article 85 band by a fortnight they had not worked.

Fixing it exposed a second defect immediately, which is the useful thing about
building the other half of a calculation. The accrual measured service at the
START of the month it was charging. Read correctly, that meant somebody who
joined on the 10th of June was charged nothing for July — a month they worked in
full — and the accrual ran permanently one month behind the settlement for
everybody who did not join on the 1st. It now measures at the END of the month
being charged, which is also how `daysFor` already described the band question:
a person who crosses five years mid-month is into their sixth by the end of it.

`TestASettlementAgreesWithWhatWasAccruedOnAnUnchangedWage` holds the two
together. On a wage that never moved the provision and the award are the same
sum charged two ways, and a difference means the two formulas disagree about the
entitlement itself — which would understate a liability every month for years
and surface on the day somebody leaves.

### 4. The accrual moved the ledger and named nobody

Every charge posts a journal entry naming who posted it, and every charge writes
an append-only `eosb_accrual` row. Neither answers "who ran the accrual for
August, and on which reading of the entitlement" — and a month that was never
run leaves no journal entry to ask. Every other act in this product that moves
the ledger writes an `audit_log` entry; this did not.

`eosb_accrued` now records the period, the number of people charged, and the
entitlement in force when they were. Correcting the rule later does not rewrite
months already posted, so the trail has to say what was in force at the time or
a re-reading of the law cannot be reconciled against the provision it produced.

## The award on leaving

`GET /eosb/settlement/{employeeID}` — `payroll.view`, reached from
`/people/employees/[employeeID]`.

A read, not a record. It is the figure the last payslip has to carry, and it is
normally wanted BEFORE the departure is entered, so making it a side effect of
recording one would put it after the thing it exists to prepare for.

Two parameters, and neither is guessed:

* **`reason`** has no default at all. `Leave` writes the reason into a free-text
  note, and reading a leaver's intent out of prose is a guess with their money
  on the end of it. `resignation` or `termination`; anything else is refused,
  and the refusal names what it will accept.
* **`on`** defaults to a recorded leaving date, or to today for somebody still
  employed. A date before they joined is refused rather than answered with a
  zero award — which is exactly what somebody typing 2025 for 2026 would be paid.

It answers the whole working, not a total:

| Field | What it is |
|---|---|
| `wage`, `wage_basis` | the pay the award is computed on, and which of the three bases the rule named |
| `first_band_months`, `first_band_days_per_year` | the first five years, at the rate the rule states for them |
| `after_band_months`, `after_band_days_per_year` | everything after, at its own rate |
| `full_award` | Article 84, before any reduction |
| `resignation_fraction` | Article 85's share — `1` on a dismissal, stated rather than omitted |
| `award` | what is owed |
| `provision`, `shortfall` | what has been set aside, and the gap |
| `rule_verified_on` | the entitlement rule's own verification date, or absent |

A final settlement is a number somebody has to be able to argue with. The person
leaving is entitled to see which wage it was computed on, how their service
split across the two bands, and what fraction was applied. A single figure with
no working is a figure nobody can check.

The two figures differ legitimately and the screen says so. Each accrual was
charged on the wage in force that month; Article 84 settles on the LAST wage. A
person whose pay rose is owed more than has been provided for, and the shortfall
is what reports it — which is the whole reason a business accrues monthly
instead of finding out at the door.

Driven live against the seeded database:

    Imran Qureshi   resignation  15m  5000.00 basic_plus_housing
                    15@10 + 0@40   full 2083.33  x 0.5   = 1041.67
                    termination                  x 1     = 2083.33

    Nadia Haddad    resignation  74m  8750.00 basic_plus_housing
                    60@10 + 14@40  full 28194.44 x 0.2   = 5638.89
                    termination                  x 1     = 28194.44

    no reason              -> 400, and names the two it accepts
    date before joining    -> 400, and gives both dates

Those bands and fractions are the DEVELOPMENT figures — ten days and forty, a
half, a quarter, a fifth, a tenth. No labour law says any of them, deliberately:
`cmd/devregulatory`'s whole claim is that its figures are visibly not the law,
and two of them were 0.3333 and 0.6667, which are a third and two thirds and
therefore look exactly like a real reading of Article 85. They are a descending
half, quarter, fifth, tenth now, which nobody can mistake for a statute.

## The boot gate no longer reports missing data as a broken build

An unverified legal value means one of two things, and `cmd/api` treated them as
one:

* **Nothing in that market can trade without it.** A Saudi till cannot issue an
  invoice without ZATCA's XML and QR formats. A deployment serving Saudi Arabia
  with either still a placeholder is serving something broken, and refusing to
  start is a far cheaper failure than a wrong tax return.
* **One capability cannot be COMPUTED until the figure is on record.** The rest
  of the product is untouched and the capability refuses itself by name the
  moment somebody asks for it.

`0124` recorded which of the two a blocker is — `blocks` is `onboarding` or
`feature` — and the provisioning gate took it up. **The boot gate never did.**
So a complete end-of-service engine waiting on Articles 84 and 85 could stop a
whole Saudi deployment from starting: a coffee shop that will never process a
leaver could not open its till, and no amount of development would fix it,
because what was missing was a number in a statute.

`healthFor` now reads `blocks`. `BlockingRelease` holds onboarding blockers for
served markets and is the only set that refuses a start. A new `AwaitingData`
holds the feature-level ones, and they are named at every start rather than
counted:

    level=INFO  msg="regulatory registry"  served_markets="bd, sa"
                blocking_release=0  awaiting_data=1  deferred_blockers=0
    level=WARN  msg="capabilities awaiting a regulatory figure"
                rules=SA.EOSB.ENTITLEMENT  served_markets="bd, sa"
                note="the software for each is complete and the figure is not
                      on record. Each capability refuses by name where it is
                      used; nothing else is affected. Record them with
                      `regulatory -template -country sa` then
                      `regulatory -apply -file …`, or in Super Admin >
                      Regulatory Registry"

Nothing is loosened. `gate()` still refuses every unverified rule at the point
of use, so an end-of-service calculation on this deployment still fails, by
name, with the command that fixes it. That was always the protection that
mattered; the boot refusal was a second, coarser copy of it, and the coarseness
was the defect.

`TestAnOnboardingBlockerStillRefusesAProductionStart` holds the other half shut.
Nothing real is unverified at onboarding level any more, so it seeds a fixture
under `zz` — ISO 3166's "unknown country", already this suite's fixture market
for the same reason: `regulatory_rule` is append-only by trigger, so a rule a
test writes cannot be cleaned up. `TestEveryUnrecordedLegalValueSaysWhereItComesFrom`
excludes `zz` and only `zz`, exactly as `TestVerifiedRulesCarryTheirEvidence`
already did, because demanding a legal citation for a rule whose country is
"unknown" is demanding evidence for something no authority published.

### Everything downstream of the gate says the same thing

* **`cmd/regulatory`** printed one count and exited non-zero on both kinds, so a
  deployment pipeline failed a release over an unread end-of-service band while
  printing "a deployment will refuse to start", which was false for that rule.
  Two marks now — `!` stops a market trading, `~` holds back one capability —
  and the exit code is non-zero only for `!`.
* It also told a deployment serving Bangladesh and Saudi Arabia to run
  `regulatory -template -country bd` for a rule whose key begins `SA.`, which
  writes an empty file. `countryOfRules` reads the market off the rule key,
  which `regulatory_rule_key_format` guarantees is the first two letters.
* **`cmd/freshcheck`** reported Saudi onboarding blockers and nothing else, so a
  fresh database left the second condition invisible. It now prints both:

      Saudi onboarding blockers outstanding: 0
      capabilities awaiting a regulatory figure: 1 (SA.EOSB.ENTITLEMENT)
        each is implemented and refuses by name at the point of use;
        record with `regulatory -template` then `-apply`

* **`requireMarketIsUsable`**'s comment claimed it guarded against serving a
  Saudi client "on placeholder GOSI, EOSB and WPS values". It has not done that
  since 0124 and should not; corrected to say what it reads and why.

## The ten capabilities the brief asked for

| # | Asked | Where |
|---|---|---|
| 1 | Resolve the EOSB rule through the regulatory-rule system | `people.eosbEntitlement` builds a `registry.Query` and resolves; no figure is in Go |
| 2 | Store jurisdiction, effective date, provenance, version, fields, units, validation, audit history | `regulatory_rule` (0004): country, `effective_from`/`effective_to`, authority + document + URL, append-only with a frozen-column trigger so a version is a row and history cannot be rewritten; the source pack carries fields, kinds, units, articles and help |
| 3 | Import/update through a deterministic workflow | `regulatory -template` → `-check` → `-apply`, one file, no browser, no shell in the container; or `/platform/rules` |
| 4 | Validate the imported rule automatically | `checkValues` against the pack: a choice must be one of the choices, a fraction must lie in [0,1], a count of days may not be negative, every field must arrive together |
| 5 | Prevent malformed values automatically | the same, plus `read_by` must resolve to a platform operator on this installation, plus the effective date must fall after the placeholder it replaces |
| 6 | Resolve on country, jurisdiction, effective date, service date, version | `registry.Query{Key, Country, AsOf, TenantID}` — the settlement resolves AS OF the leaving date, so re-running it next year gives the same answer |
| 7 | Calculate automatically | the accrual and the settlement, both bands, the wage basis, all four Article 85 fractions |
| 8 | Test automatically | 12 integration tests across `internal/api` and `internal/registry`, plus the accrual-equals-settlement invariant |
| 9 | Audit automatically | `eosb_accrued` in `audit_log`, the journal entry per charge, the append-only accrual row |
| 10 | Prevent unsupported calculations automatically | a wage basis this product cannot compute is refused and named; a fraction above 1 is refused at the point of use as well as at import; a market with no rule declines rather than applying Saudi bands to a foreign contract |

Two of those needed a second door. The source file validates a fraction on the
way in, but the registry screen writes a payload directly and an override is
written per tenant, and neither goes through the file's validation — so a
fraction of 33 instead of 0.33 would have paid somebody thirty-three times their
award with perfectly valid arithmetic. It is refused where the calculation reads
it, which is the one place every path passes through.

## No code change is needed to record the figure

The whole point, stated plainly. Somebody who reads Articles 84 and 85 records
what they read through a workflow that already exists:

    regulatory -template -country sa > sources.json     # writes the six fields,
                                                        # each with its article,
                                                        # unit, and what it means
    regulatory -check  -file sources.json               # validates, records nothing
    regulatory -apply  -file sources.json               # records, with the
                                                        # attestor's name

or on a running deployment, in Super Admin > Regulatory Registry, where the
guided form asks the same six questions with the same help text. No migration,
no SQL, no rebuild, no developer.

## Verification

    clean migration from zero    132 migrations, 183 tables, 175 forced RLS,
                                 175 policies, 44 rules, 542 CDTFA rates    PASS
    backend, every package       integration tags, fresh test database      PASS
    backend, internal/api        324s, on a database built from zero                                       PASS
    go vet / vet -tags=integration                                          PASS
    gofmt -s -l                  clean
    lint-wording                 1,483 files                                PASS
    typecheck                    shared and web-next                        PASS
    web-next tests               598 passed / 37 files                      PASS
    shared tests                 482 passed / 29 files                      PASS
    production build             125 static pages, no warnings              PASS
    check:contract               501 routes, 110 permissions (103 gated)    PASS
    check:reach                  501 routes, 415 attributed, 66 via a
                                 wrapper, 20 intentional, 0 gaps            PASS
    resource-check               every cache and the disk under threshold   PASS
    docker compose up            four containers healthy, small profile     PASS
    verify:api                   ALL SCREEN CONTRACTS VERIFIED, including
                                 the settlement's sixteen fields and its
                                 refusal to guess                           PASS
    verify:rbac                  EVERY BOUNDARY HELD, including the
                                 settlement at payroll.view                 PASS
    regulatory report            1 outstanding capability, exit 0           PASS
    settlement, driven live      two people, both reasons, both refusals    PASS

`npm run lint` in `web-next` was a dead script and had been since the Next 16
upgrade: `next lint` was removed from the framework, so the command always
failed. There is no ESLint configuration in this repository and never has been
— strict TypeScript and `cmd/lintwording` are what it lints with — so the script
was a leftover from `create-next-app` pointing at a tool the project does not
use. It now runs the wording check, which is the lint that exists.

## Proved on a production deployment, not only in tests

The compose stack runs `RAWSYST_ENV=production`, which is where the boot gate
and `requireVerified` are both live. It had no tenants, so it served no market
and blocked on nothing — which is exactly the condition under which the old gate
looked fine. A Saudi business was provisioned into it through the API and the
API restarted:

1. **The operator signed in.** `bootstrap -recover` reissued the credential for
   `mahedi.emon62@gmail.com`, forced a change at first sign-in and ended every
   session that account held. No second operator was created; `bootstrap`
   refuses one by design and said so when asked.
2. **`POST /platform/tenants` created a Saudi business — 201.** The provisioning
   gate did not refuse it. That is 0124's promise, and it had never been
   exercised against a real production process.
3. **The setup wizard's own routes made the company — 201.** Business
   information saved, step completed, `POST /onboarding/company` answered a
   company id. A Saudi shop can be opened while the end-of-service figure is
   outstanding.
4. **The API restarted and came up healthy.** This is the change:

       env=production  served_markets="sa"
       blocking_release=0  awaiting_data=1  deferred_blockers=0
       msg="capabilities awaiting a regulatory figure" rules=SA.EOSB.ENTITLEMENT
       msg="http server listening" addr=":8080"

   Before this pass the same deployment would have refused to start.
5. **And the calculation still refuses.**

       POST /eosb/accrue  ->  422
         The legal value "SA.EOSB.ENTITLEMENT" has not been verified against
         its official source (Saudi Labour Law — end of service award), so this
         operation cannot proceed. Verification is recorded in Super Admin >
         Regulatory Registry.

       GET  /eosb         ->  200   the provision reads; it needs no rule

   Which is the whole argument in five lines: the deployment works, the shop
   trades, and the one calculation that needs a figure nobody has read says so
   by name to the one person who asked for it.

Payroll had to be enabled on that tenant first — `PUT /platform/tenants/{id}/features`
from the platform screen, because the plan it was provisioned on does not
include payroll and the module gate answers 402 before the regulatory one is
reached. Two gates, two different refusals, in the right order.

## The import workflow, run in the container

`docker compose --profile setup run --rm regulatory` — the same static binary
the API is built from, in an image with no shell.

`-template -country sa` writes the file. For end of service it carries the
citation (`mhrsd`, the Labour Law, the Bureau of Experts URL, articles 84 and
85), the reading of those articles, and all seven fields with their unit, their
article, and what each one means. `wage_basis` carries its three choices.

`-check` records nothing and refuses precisely. Five deliberately invalid files,
five different refusals, none of which required knowing the law:

| What was wrong | What it said |
|---|---|
| `wage_basis` of `basic_plus_the_company_car` | names the three it understands, and why choosing one is a legal assertion |
| a resignation fraction of `33` | "it is a fraction of the award — somewhere between 0 and 1. A third is 0.3333, not 33" |
| the fourth band left out | names the missing field and its label, and says why every field is recorded together |
| `read_by` naming somebody who is not here | "is not an active platform operator on this installation" |
| effective from 2020 | "is not after 2026-01-01, the day the placeholder it replaces took effect… a period the product had no verified figure for has to go on refusing when a report is re-run" |

Nothing was applied. Applying would mean inventing figures, and the point of the
exercise is that the machinery is complete without them.

The same seven fields appear on `/platform/rules` with the same help text, the
same choices and the same units, because that screen renders
`GET /platform/rules/sources` rather than a list of its own. Adding the fourth
band to the source pack put it on the screen with no frontend change.

## Resources, measured on the running stack

| Container | Resident | Ceiling |
|---|---|---|
| `db` | 17.1 MiB | 384 MiB |
| `api` | 66.6 MiB | 256 MiB |
| `web` | 46.5 MiB | 256 MiB |
| `worker` | 15.3 MiB | 128 MiB |
| **total** | **~145 MiB** | **1,024 MiB** |

Database connections **8 of 20**. `shared_buffers` 64 MB, `work_mem` 4 MB,
`effective_cache_size` 192 MB. Durability untouched: `fsync`,
`full_page_writes` and `synchronous_commit` all on, data checksums on.

**The operational trap from the last pass happened again, and is worth
restating.** `docker compose --profile setup run` was given only the base file
while running `bootstrap` and `regulatory`, and it RECREATED the database
container on the base file's 1 GiB ceiling and 256 MB `shared_buffers`.
Force-recreating `db` with both files put it back. Any compose invocation that
touches a service — `run` included, not only `up` — must carry every file, and
the ceiling must be checked afterwards rather than assumed.

| Image | Size |
|---|---|
| `rawsyst/backend:dev` | **89.8 MB** (`scratch`, five static binaries, non-root) |
| `rawsyst/web:dev` | **330 MB** (distroless, no shell, non-root) |
| `postgres:17-alpine` | 424 MB |

Two stale tags, `rawsyst/backend:local` and `rawsyst/web:local`, were left by an
earlier build and referenced by no compose file and no container. Removed —
419 MB, and one less pair of images somebody could deploy by accident.

Caches: Go build 2,372 MB (max 3,000), Go modules 610 MB, npm 710 MB, Next
output 716 MB, Docker reclaimable 3,820 MB (max 4,000). Every one under its
threshold, so `make maintenance` cleans nothing — which is the point of the
thresholds. 12 GB free on the volume, against a 10 GB floor.

Host memory read LOW at 357 MB free while the compose stack, a second API and a
Next build were all up at once. That is the 8 GB profile behaving exactly as
`make verify` documents: the suites are staged rather than parallel, because
running the Go tests, the Next build and a database together is how a process
gets killed by the OOM reaper and reads as a flaky test.

Duplicate processes: one. A second API on `:8090`, started to run `verify:api`
and `verify:rbac` against the seeded development database while the compose
stack held `:8080`. Stopped when the verification finished.

## Where this leaves end of service

**Software: complete.** The rule resolves through the registry at the date of
the document being processed. Both Article 84 bands, the wage basis and all four
Article 85 fractions are read from data and none of them is in Go. The accrual
charges monthly and is audited. The settlement answers what is owed on leaving,
with its whole working, and refuses to guess how the service ended. Twelve
integration tests cover it, including the invariant that the accrual and the
settlement agree. Nothing is waiting for a developer, a migration, a SQL
statement or a rebuild.

**Regulatory data: an external source is required.** Articles 84 and 85 of the
Saudi Labour Law are published as prose. Nobody has read them and put their name
to what they say on this installation, so all seven fields are `__VERIFY__` and
every calculation that needs them refuses by name. Recording them is one person,
one file, three commands — or one form in Super Admin.

Those two sentences are about different things and this document has run them
together before. It does not any more.

## Markers

`TODO`, `FIXME`, `XXX`, `HACK`, `not implemented`, `coming soon`,
`unimplemented`: **none** in `backend`, `web-next/src`, `shared/src`, `pos/src`
or `scripts`.

Every EOSB-related match for `placeholder`, `stub`, `mock`, `fake`, `temporary`,
`hard-coded` is now either prose explaining why something is NOT one, or the
`__VERIFY__` mechanism itself. Two stale ones were corrected in
`internal/provisioning`, where a comment still claimed the gate guarded against
serving a client on placeholder EOSB values.

`__VERIFY__` is **five rules**, one of them release-blocking at FEATURE level
(`SA.EOSB.ENTITLEMENT`, seven unfilled fields). All five are
described by the source pack, so all five can be recorded from a file with no
developer involved. That is the mechanism working, not a gap in it.

---

# THE TWO DOORS INTO THE REGISTRY (2026-09-10, second pass)

The previous pass established that end of service is finished software waiting
on published law, and separated the two categories in this document. This pass
audited the regulatory architecture around that claim and found three places
where it was not yet true, and closed them.

None of the three was end-of-service arithmetic. All three were the registry
workflow the claim rests on — which is the point: "the figure can be recorded
without a developer" is only true if every route into the registry is as strict
as the strictest one, and if the person who has to record it can see what is
outstanding without a shell.

## What was wrong

**1. The screen was the permissive door.** A legal value reaches this product
two ways. `cmd/regulatory` applies an attestation file, and it has validated
every figure against the source pack since it was written: field names, units,
choices, signs, and the rule that a fraction of an award lies between 0 and 1.
`POST /platform/rules` — the Super Admin screen, and the route a platform
operator actually uses — refused an empty payload and a payload still holding
`__VERIFY__`, and wrote anything else.

So the two doors disagreed about what a legal value is. Through the screen,
`days_per_year_first_five` could be recorded as `"fifteen"`, a resignation
fraction as `33` where Article 85 says a third, a wage basis this product cannot
assemble, or a field name the calculation never reads. `eosbEntitlement` catches
some of it at the point of use and says so in its own comment — *the registry
screen writes a payload directly, and neither goes through the file's
validation*. Catching it there is too late: the wrong figure is already on
record as the law, somebody's name is against it in the audit trail, and what
surfaces is a payroll run failing rather than a form field saying which box is
wrong.

**2. What this installation is waiting for was answerable only from a shell.**
`registry.Outstanding` has produced that report since the attestation workflow
was written, and its only caller was a binary run on the host with the database
password. The screen approximated it by scanning payloads it had already fetched
for the literal `__VERIFY__` — which finds the unfilled fields but cannot say
whether the source pack describes the rule, and therefore cannot say whether the
operator gets labelled boxes or a JSON textarea.

**3. The screen still called every unverified blocker a closed market.** 0124
taught the database the difference between a value that closes a market to new
business and one that refuses a single capability, and the startup gate has read
it since. `RuleRow` did not carry the column, `NewRule` could not set it, and the
screen's heading was *Markets that cannot take a new client*. So the screen
reported `SA.EOSB.ENTITLEMENT` as a reason Saudi Arabia could not be sold into —
the exact category error the boot gate was fixed to stop making, still being made
one layer up, to the one person who could act on it.

## What was done

**`registry.ValidatePayload`** applies the source pack's own rules to a payload,
and `RecordRule` calls it. The screen now gets the refusals the file gets, and
`checkValues` attaches the offending field to each one so the guided form points
at the box rather than at the form. A rule key the pack does not describe is
still recordable: the pack covers the values somebody has to go and read out of a
published document, and refusing everything else would close the registry to
anything the pack has not caught up with.

**`GET /api/v1/platform/rules/outstanding`** (Super Admin) returns what
`cmd/regulatory` prints: the rule, the market, what it blocks, the fields still
unfilled, and whether the pack describes it. The screen reads it.

**`blocks` is carried end to end.** It is on the list row, settable when
recording, refused if it is neither `onboarding` nor `feature`, and defaulted to
`feature` — the narrower answer, because mistaking a capability blocker for an
onboarding one shuts a market that could trade. The screen splits the two, badges
each rule with what it actually stops, and says the right thing about production
for each: an onboarding blocker refuses a production start, a feature blocker
does not.

Eleven integration tests, all passing against a database rebuilt from the
committed chain. Six of them drive `SA.EOSB.ENTITLEMENT` and every one asserts a
**refusal** — no test records a Saudi labour-law figure, because a test that did
would be inventing one.

The startup warning and `freshcheck` now name Super Admin before the command
line, since the screen answers the question they raise.

## What was NOT done, and why

No legal value was invented. `SA.EOSB.ENTITLEMENT` still holds `__VERIFY__` in
all seven fields and still refuses by name. Nothing about the validation added
here makes a figure easier to fabricate; it makes a wrong one harder to record.

No safety was removed. The production gate is unchanged, the placeholder refusal
is unchanged, and the point-of-use checks in `eosbEntitlement` stay where they
are — they are the second door for a tenant-scoped override, which does not pass
through `RecordRule` at all.

## Verification

Every check below was run to completion on this pass. Nothing was skipped to
make a result green.

| Check | Result |
|---|---|
| Fresh migration from zero (`freshcheck`) | 132 migrations, 183 tables, 175 RLS-forced, 44 rules seeded |
| Backend, every package but `internal/api` | pass |
| Backend, `internal/api` | pass, 318s |
| `go vet`, and again with `-tags integration` | clean |
| `gofmt` | clean |
| `lint-wording` | passed, 1,484 files |
| Frontend tests | 598 passing, 37 files |
| Typecheck | clean |
| Production build | clean |
| Contract | up to date, 502 routes, 110 permissions |
| `verify:api` | ALL SCREEN CONTRACTS VERIFIED |
| `verify:rbac` | EVERY BOUNDARY HELD |
| Reachability | **0 genuine gaps**, 20 intentionally screenless |
| Live refusal of a malformed figure | `POST /platform/rules` with a fraction of 33 refused, field-scoped |
| Live outstanding list | five rules, all `described`, `SA.EOSB.ENTITLEMENT` as `feature` |

## The stack, rebuilt and measured

Images rebuilt from the changed source and the stack recreated with **every**
`-f` passed, so the database came back on the 8 GB profile rather than the base
file's ceiling. Verified rather than assumed: `shared_buffers` is 8192 pages and
`max_connections` is 20, which is the small profile.

| Container | Memory | Limit |
|---|---|---|
| `web` | 43.9 MiB | 256 MiB |
| `api` | 19.0 MiB | 256 MiB |
| `db` | 29.0 MiB | 384 MiB |
| `worker` | 3.3 MiB | 128 MiB |

Images: `rawsyst/web:dev` 330 MB, `rawsyst/backend:dev` 89.9 MB,
`postgres:17-alpine` 424 MB.

Database connections in use: 10 of a ceiling of 20. No durability setting was
touched.

`scripts/maintenance.sh check` reported one threshold breach — Docker
reclaimable at 5,155 MB against a 4,000 MB limit — and `clean` took it to 240 MB.
It cleans only what is over its threshold, so the Go build cache (2,959 MB of
3,000), the npm cache (710 MB of 1,500), the Next build output (732 MB of 1,500)
and the 610 MB Go module cache were all left alone. Deleting those on every pass
buys nothing and costs a full rebuild.

Free disk on the working volume: 12 GB against a 10 GB floor. Duplicate
processes: none left. The spare API on `:8090`, started to run `verify:api` and
`verify:rbac` against the seeded development database while the compose stack
held `:8080`, was stopped when the verification finished.

## The gate, observed doing the right thing in production

The compose API runs with `env=production`, and this is what it logged on this
rebuild:

    "msg":"regulatory registry","env":"production","served_markets":"sa",
    "blocking_release":0,"awaiting_data":1,"deferred_blockers":0

    "level":"WARN","msg":"capabilities awaiting a regulatory figure",
    "rules":"SA.EOSB.ENTITLEMENT"

    "msg":"http server listening","env":"production","addr":":8080"

A production deployment serving Saudi Arabia **started**, with end of service
unrecorded, and warned by name about the one capability that cannot compute. It
did not refuse, because nothing about it is broken. That is the distinction this
work exists to hold, observed rather than argued.

## Where this leaves the two categories

**Software development: COMPLETE.** Every route into the regulatory registry
validates to the same standard, the report of what is outstanding is in the
application, and the screen tells the truth about what each unrecorded value
blocks. Nothing in the end-of-service path — resolution, storage, provenance,
versioning, effective dating, import, validation, calculation, accrual,
settlement, audit, cache invalidation, refusal — waits for a developer, a
migration, a SQL statement or a rebuild.

**Regulatory data: an external source is required.** Articles 84 and 85 of the
Saudi Labour Law are published as prose. Nobody has read them and put their name
to what they say on this installation. That is data provenance. It is not a
software blocker and this document does not call it one.

---

# THE DOCUMENT, RETRIEVED (2026-09-10, third pass)

The previous two passes said the end-of-service software was finished and the
figures were a data-availability question. The software was finished. The second
half was wrong, and this pass proves it by fetching the document.

The Ministry of Human Resources and Social Development publishes the Labour Law
as a PDF on its own site. It is reachable, it is machine-readable, and it states
Articles 84 and 85 in full. What was missing was not the data. It was a piece of
software: something that retrieves a publication, keeps it, reads it, and shows
a person the sentence behind every figure before they put their name to it.

That is what this pass builds, and the outcome is that
`SA.EOSB.ENTITLEMENT` is recorded, verified and in force — on the development
database and on the production compose stack, both through the application, with
no code change, no migration and no SQL.

## The source

| | |
|---|---|
| **Authority** | Ministry of Human Resources and Social Development (`mhrsd`) |
| **Document** | Labor Law, Royal Decree No. M/51 of 27 September 2005 |
| **Address** | `https://www.hrsd.gov.sa/sites/default/files/2023-02/Labor.pdf` |
| **SHA-256** | `2d2830afaf63c78cd7b3484d2e82f525d520d44917d29dc2feb5ef642c6a3e2a` |
| **Size** | 337,855 bytes, `application/pdf` |
| **Articles** | 84 and 85, with the definitions of Wage, Actual Wage and Month |

The Bureau of Experts at the Council of Ministers publishes the same law at
`laws.boe.gov.sa` and that address is not reachable from this deployment. The
source pack now names the Ministry's, which is the authority the pack already
credited and the one that answers.

## What the reading produced, and what it read it out of

Seven fields, each with the article and the sentence:

| Field | Value | Article |
|---|---|---|
| `days_per_year_first_five` | 15 | 84 |
| `days_per_year_after_five` | 30 | 84 |
| `wage_basis` | `basic_plus_all_allowances` | 84 |
| `resignation_fraction_under_two_years` | 0 | 85 |
| `resignation_fraction_two_to_five_years` | 1/3 | 85 |
| `resignation_fraction_five_to_ten_years` | 2/3 | 85 |
| `resignation_fraction_over_ten_years` | 1 | 85 |

Three of those are not printed in the article in the form the registry stores,
and each says so on the screen rather than appearing as a figure the document
states:

- **15 and 30 days.** Article 84 says "a half-month wage" and "a one-month
  wage". The law's own definitions say "Month: 30 days", and the reading quotes
  that sentence beside the arithmetic.
- **The wage basis.** Article 84 computes on "the last wage" and does not say
  what a wage is. The definitions do, in two steps: "Wage: actual wage", and
  "Actual Wage: The basic wage plus all other due increments". The reading
  follows that chain and quotes both.
- **Zero below two years.** Article 85 confers a share only "after service of
  not less than two consecutive years". Below that it confers none. That is the
  article's silence at the threshold, labelled as an inference rather than
  presented as a printed figure.

## Fractions are recorded as the article states them

Article 85 says "one third". Recorded as 0.3333, a third of a 30,000 award pays
9,999.00 and the person owed 10,000.00 is short a riyal — not because anything
computed wrongly, but because a figure the law states exactly was written down
inexactly, in the registry, on purpose. There is no number of threes that is a
third.

So the registry accepts `1/3`, the validator checks it as a fraction between
nought and one, a new `Fraction` accessor resolves it at a precision that rounds
correctly wherever money is computed, and the settlement quotes the literal —
`1/3` is what a person can check against Article 85, where a twenty-eight-digit
decimal is the same number and no help at all.

## A correctness fix the ingestion surfaced

`resignationFraction` read the five-year boundary as `LessThan`. With the
article's sentence sitting next to the code, it is plainly the wrong way round:

> "…one third of the award after service of not less than two consecutive years
> and **not more than five years**, to two thirds if his service is **in excess
> of five** consecutive years…"

Two years is a floor the lower band includes and ten years is a floor the top
band includes, but five years belongs to the LOWER band — "not more than five"
takes it and "in excess of five" does not. The middle boundary is the mirror of
the other two, and the code had all three the same shape.

The cost was one day per career. Somebody resigning on their fifth anniversary,
on 20,000 a month, was settled at 16,666.67 where the article gives 8,333.33.
The day before and the day after were both already right.

## The workflow, built

    fetch or upload -> decode -> normalise -> read -> validate
       -> preview -> apply -> audit -> the engine computes with it

**Retrieval** is bounded and it is not free text. The caller does not choose the
host: the source pack records where each rule is published and a fetch must land
on that authority's site. Without that, "fetch the official source" is
server-side request forgery with a ministry's name on it — a Super Admin
session, or anything reaching one, could point it at cloud metadata, an internal
admin port or a machine on the same network and have the response stored in the
database and rendered on a screen. Thirteen cases are tested, including the
lookalike hosts a substring check would accept.

**Storage** keeps the artefact, not a citation. `regulatory_source_document`
(0133) holds the bytes, their SHA-256, the address, the moment, who retrieved
it, and the reading. The row is immutable: the bytes, the hash, the address and
the retrieval date cannot be edited and the row cannot be deleted, because
evidence that can be edited is an assertion with extra steps.

**Reading** is deterministic patterns against the article text, never a
language model, because a regulatory trail has to be reproducible and the reason
a figure came out has to be inspectable. Every value is quoted or derived from
something quoted; a pattern that does not match is a refusal naming the field
and the sentence it looked for, never a zero and never a fallback.

**Applying** goes through `RecordRule`, so a value imported from a document is
validated, superseded by date, audited and cache-invalidated exactly as a
hand-typed one is. It is a person's act and stays one: what changed is that they
are now checking a reading against sentences shown beside it rather than
transcribing figures out of a PDF into a form.

## Three things that had to be fixed to make the chain hold

**A PDF breaks words in half.** The Ministry's typesetter kerned two letters
apart and the text layer records the gap as a space: "the ful l award", "due to
the worker's resignat ion". Six of Article 85's seven figures read correctly
against patterns written with spaces and the seventh did not. Whitespace
collapsing cannot fix it — the space is between two letters of one word and
looks exactly like the space between two words — and guessing would mean
rewriting a statute before reading it. So a passage is held twice: as a person
reads it, and with every space removed. Patterns match the spaceless form, and
every match maps back so the evidence quoted is a sentence.

**A reading is a function of the document AND the build.** The first version of
the Saudi reader found six fields. Fixing it had to reach the document already
retrieved, and could not arrive by re-fetching, because the same bytes are the
same document and the table holds one row per document on purpose. So the
artefact is the frozen thing and the reading is derived from it on every read.
Nothing evidential is lost: what was read at the moment of applying is the
rule's own payload and the audit entry beside it.

**A placeholder is an absence with a date on it.** `SA.EOSB.ENTITLEMENT` was
seeded from 2026-01-01; Articles 84 and 85 have been in force since 2005. The
real figures could not be recorded from the date the law took effect, because
that collides with the placeholder's range, and could not be recorded from the
placeholder's own date either, because superseding only closes a row that starts
strictly earlier. The correction workflow could not correct the one row it
exists for. 0134 permits deleting a rule that still holds `__VERIFY__`, has never
been verified, and produced nothing — and nothing else, ever. A figure remains
undeletable. The narrowness is the point: this is not a delete capability, it is
the recognition that an absence was never a record.

## The screen

**Platform → Regulatory sources.** For each rule the pack describes: the
document, the articles, the address, and two buttons — *Fetch official source*
and *Upload official source*. For each retrieved document: the title, the
authority, the address, the retrieval time and who did it, the media type and
size, the SHA-256, a button that hands back the artefact itself, the rule in
force beside it, the status, whether the reading validates, and then the reading
— field, value, article, and the sentence, shown rather than hidden behind a
link, because confirming a figure you cannot see the evidence for is an act of
faith.

Applying asks for the date the article came into force and a tick that says the
operator read the sentences against the document. Rejecting asks why, and does
not delete: the retrieval happened.

Upload is not the lesser half. A ministry may block automated requests, publish
behind a portal, or hand the document out at a counter. The bytes arrive and are
hashed, read, validated and applied by the same code; the only thing an upload
cannot do is claim an address it did not come from.

## Re-checking, and what it will not do

`regulatory.source.refresh`, daily at 03:00, one request per applied source with
the same thirty-second ceiling and the same authority restriction as a fetch.
Unchanged bytes write nothing. Changed bytes file a CANDIDATE and stop.

It applies nothing — not when the new reading validates, not when it is
identical to the figure in force. A legal value carries the name of whoever put
it there and the date they did, and a job can supply neither. An amended statute
is also the moment a machine reading is least trustworthy, so the case where
automation would save the most work is the case where it should do the least.

There are two applied sources on a full installation. This is not a crawler and
the ceiling on its cost is the number of laws this product implements.

## Development and production do not share regulatory data

`cmd/ingest` performs the same three service calls the screen makes, so a
machine can be brought to a known state without a browser:

    go run ./cmd/ingest -rule SA.EOSB.ENTITLEMENT -from 2005-09-27 \
      -apply -verified <operator@example>

It refuses to apply without `-apply`, and refuses to mark anything verified
without an address that resolves to an account on that installation. The binary
ships in the image and is offered as a compose service, so a production
deployment activates a rule the same way and inherits nothing from development.

## What was NOT done

No legal value was invented. Every figure recorded was read out of a document
this product retrieved and hashed, and the sentence behind each one is stored
beside it. No safety was weakened: the placeholder refusal, the production gate,
the source-pack validation and the point-of-use checks are all unchanged, and
the fraction validator got stricter rather than looser.

RawSyst does not claim any legal certification. Recording a figure here is one
person's assertion that they read a document, which is what the registry has
always meant by "verified" and still means.

## Cross-check against the Ministry's calculator

The Ministry publishes an End of Service Benefit Calculator. It is a JavaScript
application on the ministry portal; it exposes no documented API, and this pass
did not drive it as one — scraping a form and calling the result an
authoritative comparison would be inventing a corroboration.

What was checked instead is the articles themselves, which is the authoritative
text the calculator implements. `eosb_official_figures_test.go` works every
expected amount out from the article in the comment above it, so a reader can
check the assertion against the law rather than against this product. No
difference between RawSyst's reading and the published articles was found.

## The calculations, end to end

Twelve integration tests on a 12,000 wage, which gives a daily wage of exactly
400 on the statutory 30-day month.

| Case | Service | Reason | Award |
|---|---|---|---|
| A | 1 year | dismissal | 6,000.00 |
| B | 5 years | dismissal | 30,000.00 |
| C | 5 years 6 months | dismissal | 36,000.00 |
| D | 6 years | dismissal | 42,000.00 |
| E | 9 years | dismissal | 78,000.00 |
| F | 10 years | dismissal | 90,000.00 |
| G | 2 years | resignation | 4,000.00 |
| H | 5 years exactly | resignation | 10,000.00 |
| I | 6 years | resignation | 28,000.00 |
| J | 10 years | resignation | 90,000.00 |
| K | 2 years 3 months | dismissal | 13,500.00 |
| L | 18 months | resignation | 0.00 |

Plus: three wage packets on the statutory basis; a third computed exactly on
wages that do not divide (10,000 → 3,333.33; 8,888 → 2,962.67); the fifth
anniversary and the days either side of it; and the accrual and the settlement
agreeing on the same rule. Every amount is decimal throughout — no money in this
product passes through binary floating point.

And one test drives the whole chain: a document is uploaded, applied, and a
settlement of 28,000.00 comes out the other end.

## Verification

Every check ran to completion. Nothing was skipped to make a result green.

| Check | Result |
|---|---|
| Fresh migration from zero | 134 migrations, 184 tables, 175 RLS-forced |
| Regulatory retrieval, live | 337,855 bytes from hrsd.gov.sa, hash recorded |
| Reading and validation | 7 of 7 fields, validation passed |
| Rule activation | verified 2026-09-10, in force from 2005-09-27 |
| Backend, every package but `internal/api` | pass |
| Backend, `internal/api` | pass, 321s |
| `go vet`, and again with `-tags integration` | clean |
| `gofmt` | clean |
| `lint-wording` | passed, 1,496 files |
| Frontend tests | 598 passing |
| Typecheck | clean |
| Production build | clean, `/platform/regulatory-sources` prerendered |
| Contract | 509 routes, 110 permissions |
| `verify:api` | ALL SCREEN CONTRACTS VERIFIED |
| `verify:rbac` | EVERY BOUNDARY HELD |
| Reachability | **0 genuine gaps** |
| Saudi onboarding | tenant created in the `sa` market |
| Production boot | `env=production`, `awaiting_data=0`, listening |
| Docker rebuild and compose | all four containers healthy |

## Where this leaves the two categories

**Software development: COMPLETE.** Retrieval, storage, provenance, hashing,
decoding, normalisation, reading, validation, preview, application, audit,
versioning, effective dating, cache invalidation, calculation, accrual,
settlement, refusal, re-checking and the screens over all of it.

**Regulatory data: INGESTED AND ACTIVE.** `SA.EOSB.ENTITLEMENT` holds the
figures Articles 84 and 85 state, read out of the Ministry's own publication,
with the document, its checksum and the sentence behind every value on record.
Nothing is awaiting a figure. There is no external data source outstanding for
end of service.

---

# THE MACHINE IT ACTUALLY RUNS ON (2026-09-10, fourth pass)

The production server is two virtual cores, 3.7 GiB of memory and 48 GB of
disk, with no swap and no Docker on it yet. Everything in this repository was
sized for "a small server" in the abstract or for an 8 GB developer's laptop.
This pass sizes it for that machine, and makes the images small enough that
putting it there is a short download rather than a long one.

## The images

| | before | after | |
|---|---|---|---|
| `rawsyst/backend` | 107 MB | **32.9 MB** | −69% |
| `rawsyst/web` | 331 MB | **263 MB** | −21% |
| both together | 438 MB | **296 MB** | −32% |

`postgres:17-alpine` is 424 MB and stays. A slimmer base exists; changing the
Postgres image changes its ICU and locale build, and a collation change under
an existing database is a data-integrity problem that is not worth 150 MB.

### Six binaries were one binary all along

The backend image carried the API, the worker, the migrator, the bootstrap, the
regulatory recorder and the source ingester as six separate executables — 22,
12, 11, 10, 10 and 11 MB. They share a module, a config loader, a database
layer and a registry, so the image paid for that six times.

Compiled together they are **21.5 MB**: one binary about the size of the
largest, because the linker keeps one copy of what they have in common.

The six now live in `internal/cmd/<name>` with a `Main()` each, `cmd/rawsyst`
dispatches on the first argument, and `cmd/<name>/main.go` is a four-line
wrapper so that `go run ./cmd/api` still works — which is what the Makefile,
the tests and every runbook here say to type. Compose entrypoints became
`["/rawsyst", "api"]` and so on.

Two things this broke, both caught by things that exist to catch them:

- A guard in `internal/jobs` reads the worker's source to check every job kind
  is registered. It was reading `cmd/worker/main.go`, which is now a wrapper,
  so it reported every kind unregistered. Repointed. A file move that quietly
  stops a check from checking is exactly what that guard is for.
- The mechanical rename of the `version` identifier reached three places it
  should not have: `SELECT max(version) FROM schema_migration` became
  `max(build.Version)`, and two error messages with it. The API refused to
  start with `missing FROM-clause entry for table "build"`, which is how it was
  found — by running the stack rather than by reading the diff.

### An image optimizer with no images

Nothing in this product uses `next/image`. The back office draws its icons as
inline SVG and its one uploaded asset, a business's logo, is served by an
authenticated API route the optimizer could not fetch if it were asked to.

Next traced `sharp` into the standalone output anyway, because the code path
exists in the server bundle whether or not a route reaches it — and `sharp`
brings a libvips build per platform **and** per C library, plus a WebAssembly
fallback. 44.3 MB of a 92.7 MB application layer, for a feature with no caller.

`images: { unoptimized: true }` in the config, a `rm -rf` of `@img` and `sharp`
in the Dockerfile, and `image-optimizer.test.ts` holding the two together: turn
the optimizer back on and the test fails and says what else has to change,
rather than the deployment failing on its first optimized image with a
module-not-found for a package somebody deleted eighteen months earlier.

Application layer: 92.7 MB → **45.5 MB**.

## The profile

`docker-compose.server.yml`, and the memory budget is in the file:

| | ceiling | measured at rest |
|---|---|---|
| database | 768M | 31 MB |
| API | 384M | 7 MB |
| back office | 320M | 44 MB |
| worker | 192M | 3 MB |
| **total** | **1.63 GiB** | **86 MB** |

3.7 GiB less about 0.5 for the operating system leaves roughly 1.6 GiB over the
ceilings, and that is not spare — it is the page cache, which is where a
database this size keeps its working set. Hence `effective_cache_size=1GB`,
which tells the planner to expect it. Giving that memory to `shared_buffers`
instead would cache the same pages twice.

Two cores is the constraint the old profiles could not express:

- `max_parallel_workers_per_gather=1`. The default of 2 lets one report take
  both cores, and the other thing wanting a core is the checkout.
- A CPU ceiling per service, so no container can hold both cores while the
  API's health check times out and it is restarted for a fault it does not
  have. The ceilings sum to more than two deliberately: they limit any one
  service rather than partitioning the machine, so at three in the morning the
  worker can use what the till is not.
- The worker is capped hardest, at half a core. Its work is a sweep; the till
  is not.

Verified on the running stack rather than assumed:

    shared_buffers                  24576   (192 MB)
    effective_cache_size           131072   (1 GB)
    work_mem                         4096   (4 MB)
    maintenance_work_mem            98304   (96 MB)
    max_connections                    24
    max_parallel_workers_per_gather     1
    fsync, synchronous_commit, full_page_writes, data_checksums    all on

That last line is the point of the profile as much as the first five. A machine
this small is exactly where somebody is tempted to trade durability for speed,
and exactly the one with no replica to recover from. Nothing here does.

## The server, and what this repository can honestly say about it

`deploy/server/preflight.sh` reads the machine and reports: cores, memory,
headroom over the container ceilings, swap and swappiness, disk and `/var` and
inodes, journal size, Docker and its storage driver and log defaults, the
firewall, every listening port against the three that belong on a public
interface, `PermitRootLogin` and `PasswordAuthentication`, failed units, pending
updates and security updates, and whether a reboot is owed.

It installs nothing, changes nothing and never will. Where something needs
deciding it prints the command and a person runs it. A script that fixes a
server it has not been allowed to look at first is how a working box becomes a
broken one.

`deploy/server/rawsyst-check.sh` is the same idea afterwards, against
thresholds chosen for this machine: memory, swap use, disk, inodes, load per
core, every container's health and memory, Docker's reclaimable space, log size,
database connections as a percentage of `max_connections`, and database size.
`--strict` exits non-zero so a systemd timer fails visibly instead of writing to
a log nobody opens.

`deploy/server/RUNBOOK.md` is the deployment, in order, for that machine: the
updates and the reboot first, then a 2 GiB swapfile at `vm.swappiness=10`,
a capped journal, ufw with three ports, SSH keys before passwords are turned
off, Docker with `deploy/server/daemon.json`, the compose profile, the first
operator, the legal values, a reverse proxy with the API and the back office
bound to loopback, the hourly check as a timer, and a database backup that
leaves the machine and gets restored.

Three things it deliberately does not do, each with the reason in the file: no
monitoring stack, because Prometheus and Grafana together are larger than
everything RawSyst runs; no automatic cleanup, because `docker system prune` is
one flag away from the volume the database lives in; and no tuning that trades
durability.

**I have not touched that server.** Everything above is configuration and
scripts in this repository, verified on this machine against the same images
and the same compose files. The runbook is a sequence for somebody with the
credentials to run it.

## A fetch that survives a hostile network

Retrieving the Labour Law failed twice in a row mid-pass: the connection was
reset after the TLS handshake, over IPv6, from a host whose IPv4 path to the
same address worked. Government file servers and the networks in front of them
do this.

`fetchDocument` now makes a second attempt pinned to IPv4, and stops there. Not
a retry loop: an importer that keeps trying is one that gets a deployment
blocked at a ministry's edge. Anything the server actually answered — a 404, a
redirect loop, a body over the ceiling — is not asked twice. When both fail the
refusal says so and points at the upload, which is not a consolation prize: the
bytes are hashed, read, validated and applied by the same code either way.

Confirmed by the failure itself. The retry recovered a fetch that had failed
twice, on the same network, minutes apart.

## Verification

| Check | Result |
|---|---|
| Fresh migration from zero | 134 migrations, 184 tables |
| Backend, every package but `internal/api` | pass |
| Backend, `internal/api` | pass, 344s |
| `go vet`, and again with `-tags integration` | clean |
| `gofmt` | clean |
| `lint-wording` | passed, 1,509 files |
| Frontend tests | 600 passing, 38 files |
| Typecheck | clean |
| Production build | clean |
| Contract | up to date, 509 routes |
| `verify:api` | ALL SCREEN CONTRACTS VERIFIED |
| `verify:rbac` | EVERY BOUNDARY HELD |
| Reachability | 0 genuine gaps |
| Server profile, live | four containers healthy, settings confirmed in `pg_settings` |
| Regulatory ingest | retrieved, read, applied, `awaiting_data=0` |
| Shell scripts | `bash -n` clean; `daemon.json` parses |

---

# A BACKUP IS A RESTORE (2026-09-10, fifth pass)

Before RawSyst goes on a server it is going to be moved off again, and the thing
that has to survive that move is the only thing on the machine that cannot be
rebuilt from this repository: the database.

## What existed, and what did not

The register did. `backup_record` has been there since 0093 with the right
distinction already in it — `status` says a run finished, `verified_at` says
somebody proved it restores, and they are separate columns because they are
separate claims. There were routes, a service and a screen that leads with "a
backup that ran is not a backup that restores".

What was missing was anything that filled them in. The routes' own comment said
so: *this product records backups; it does not take them*. That is a reasonable
position for a product deployed by somebody with a backup operator. A shop's own
server does not have one.

## What a snapshot is

```
rawsyst/20260910T130723Z-1/
    database.dump      pg_dump custom format, compressed
    manifest.json      what it is, how big, what it hashes to
    COMPLETED          written last, and only if everything before it worked
```

The marker is the point. A dump that uploaded and a manifest that did not is a
snapshot that would restore into a database with no idea what it is, so a
restore refuses anything without one. That is what stops "the upload returned
200" from being mistaken for "there is a backup".

On this profile the database **is** the persistent data — documents and logos
live in Postgres unless the files override is layered on — so one dump carries
companies, users, roles, products, inventory, customers, suppliers, orders,
invoices, accounting, payroll, the regulatory registry with the source documents
it was read out of, and the audit trail. Nothing reproducible is in it: no
images, no caches, no `node_modules`.

**No secrets are in it.** Not the database password, not the JWT secret, not the
storage credentials. A backup is read by whoever can list the bucket, and one
that carries the credentials to the system it protects turns a leaked object
into a full compromise. A test fails the build if a manifest field is ever added
whose name suggests one.

## Off the server, or not at all

`backup run` refuses to start without an object store configured, rather than
quietly writing to the disk it is protecting. Any S3-compatible store, entirely
through environment, nothing committed. `https` is required outside development
and configuration already refused otherwise — which is how the first local test
run failed, correctly.

`blob.Store` gained `PutStream`, `GetStream`, `Exists` and `List`. `Put` takes a
`[]byte`, which is right for a logo and wrong for a dump: on 3.7 GiB with 1.6 of
container ceilings, reading a dump into memory to upload it is the difference
between a backup and an out-of-memory kill, and it fails exactly when the
business has grown enough to need one. The dump is staged on disk, hashed on the
way past, and uploaded from the file with a known length and a known checksum —
one hash, one pass, three uses, because SigV4 signs the payload hash and the
manifest and the restore both want the same value.

## Verification, which is the whole point

`verify` is a different command from `run` on purpose, and it does nine things:
the completion marker exists, the manifest parses and is a version this build
reads, the object is the length the manifest says, it downloads and hashes to
what was recorded, **it is restored into a temporary database beside the real
one**, the schema version matches, the table count matches, the row counts match
table by table, and the temporary database is dropped — whatever happened.

Production is never touched.

Proved rather than asserted, on the running stack:

| | |
|---|---|
| backup | 1.6 MB dump of a 24.7 MB database, schema 134, 184 tables |
| verify | restored, 184 tables, schema 134, row counts matched, 3s |
| a byte appended | caught at the size check |
| **one byte flipped, same length** | **caught at the checksum, and named both hashes** |
| register | `succeeded`, size, checksum, `verified = t` |

That last row is the one that matters. A corrupted backup that hashes correctly
would be a backup system that lies, and the tamper test is how you find out
whether yours does.

## Retention that cannot empty the store

Seven daily, four weekly, three monthly, all configurable — and **always the
newest completed snapshot, whatever the policy says**. A retention rule that can
empty the store is a retention rule that will, and the day it does is the day
somebody needs it.

Two further refusals: if no completed snapshot can be found, nothing is deleted
at all, because an empty or unreadable listing is a reason to stop rather than
to start removing backups; and a snapshot whose id this build cannot date is
kept, because deleting something because it is not understood is how a bug
becomes data loss. Both have tests.

`make cleanup` and `scripts/maintenance.sh` never touch the object store. A low
local disk is not a reason to delete a remote backup.

## Where it runs

A `backup` target on `postgres:17-alpine` with the RawSyst binary added: 21 MB
on top of an image the host already has. `pg_dump` and `pg_restore` are the
right tools and this product is not going to reimplement them; taking them from
the same image as the database is what makes the version match impossible to get
wrong. The API image stays `scratch` at 33 MB.

A systemd timer at 03:30 takes a backup, verifies it, then prunes — in that
order, and prune only runs if verify passed. `Persistent=true`, so a machine
that was off backs up when it returns. A failed unit shows in `systemctl
--failed`, and `rawsyst-check.sh` now reports the age of the last VERIFIED
backup hourly and flags the last failure reason.

## Documentation

- `deploy/server/BACKUP.md` — what is in a snapshot, where it goes, the
  commands, verification, the schedule, retention, secrets, restoring, and what
  this does **not** protect against: no point-in-time recovery (the RPO is 24
  hours and WAL archiving is the answer, unconfigured because it needs a
  destination and a cost decision), no client-side encryption, and no substitute
  for walking a restore once before you need it.
- `deploy/server/MIGRATION.md` — old server to new, with the rehearsal first:
  build the new server from a real backup and smoke-test it while the old one is
  still serving, so the only downtime is the final backup and the switch.
  Rollback is that the old server is untouched and off, and the window closes on
  the first write to the new one. Nothing is automated, and the file says why.

## Not done, and I cannot do it

**RawSyst has not been deployed to 40.160.14.32.** I have no access to that
machine — no credentials, no SSH, no way to run `preflight.sh` on it. Everything
above is code and configuration in this repository, verified here against a real
Postgres, a real S3-compatible store and the real images.

What is ready for whoever does have access is the sequence in `RUNBOOK.md`,
which now begins with the audit and ends with a verified backup **before** the
application takes its first order.
