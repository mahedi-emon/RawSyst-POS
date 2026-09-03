# RawSyst — Implementation Progress

Session-continuity file. **Long-form history lives in
[`docs/PROJECT-STATUS.md`](docs/PROJECT-STATUS.md)** — this file is the short
answer to "what is true right now and what do I do next", so the two do not
duplicate each other. Serena memories `audit/verified-2026-09-02-directive` and
`architecture/counter-and-market` carry the reasoning.

| | |
|---|---|
| **Last verified** | 2026-09-02 |
| **Branch** | `main` (working tree dirty — nothing committed this session) |
| **Scale** | 105 migrations · 426 routes · 1,165 Go test functions · 667 front-end tests |
| **Direction** | Web first. POS is a module inside the web app. **ZATCA skipped and isolated.** Tauri deferred. |

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
| B11 | Quotation → Order → Delivery | **COMPLETE** | `orders` 8 svc · 8 routes · 11 tests; `aftersales` delivery 4 routes |
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
| C5/C6 | HR & payroll | **CODE COMPLETE 2026-09-03 — BLOCKED on official Saudi data, by design** | `people` 23 svc · 23 routes. Everything is built and runs: directory, ID expiry alerts, attendance and leave, salary advances with recovery, payslips, the market gate (a Bangladeshi shop pays its staff without a GOSI line rather than not at all), and **commission, which used to 500 the whole run** — see the *Sales commission* section. What remains is not code: the **GOSI contribution rates** and the **Mudad wage-file format** are legal values, and `registry` refuses a GOSI calculation and a wage file until an operator has checked them against MHRSD/GOSI and stamped `verified_on`. That refusal is the correct behaviour and is tested (`TestPayrollSaysWhenGOSIIsNotVerifiedRatherThanGuessing`, `TestTheWageFileRefusesUntilTheFormatIsVerified`). Unblocking it is a data-entry task against official sources, not an implementation task |
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
| E1 | ZATCA | **DEFERRED** | as instructed |
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
