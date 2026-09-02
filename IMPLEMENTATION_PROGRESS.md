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
| B1b | **Bundles / kits** | **MISSING** | A `bundle_price` PROMOTION kind exists (`0084`), which prices several items together. B1 asks for something different: a product composed of component SKUs with **automatic proportional stock deduction of each component on sale**. No composition table, no component deduction |
| B2 | Variant matrix | **COMPLETE** | `matrix_test.go` 5 tests; regeneration adds only what is missing |
| B3 | Barcode & Label Studio | **COMPLETE** | `labels` 9 svc · 9 routes · 17 api tests (`studio_test`, `stationery_test`) |
| B4 | Inventory core | **COMPLETE** | `inventory`+`stockops` 21 svc · 23 routes · 46 pkg + 28 api tests. FIFO/WAC/standard, landed cost (`0034`), negative-stock policy, transfers with in-transit status, counts, adjustments, **GL tie-out exact**, concurrency tests |
| B4a | **Batch / Lot / Expiry** | **MISSING** | Blueprint B4 requires batch no, mfg/expiry date, qty, supplier, cost + Expiring-Soon / Expired / Recall alerts. **No column, table, service or route anywhere** |
| B4b | Minimum-stock alert engine | **COMPLETE 2026-09-03** | `jobs.LowStockSweeper` (`stock.low_sweep`), scheduled daily per tenant with a date dedupe key so a shop low for a week is told once a day. Announces `notify.KindLowStock` per variant with the variant as `subject_id`, so tapping it reaches the product. **Uses the dashboard's exact query** — summed across warehouses, `qty > 0` — because two places disagreeing about "low" is worse than either answer. 5 tests incl. tenant isolation and out-of-stock exclusion |
| B5 | Purchasing / procurement | **COMPLETE** | `purchasing` 32 svc · 31 routes · 9 pkg + 57 api tests: PO, partial receiving, GRNI (`0034`), bills, **three-way match**, payments + reversal, returns, supplier balances |
| B5.1 | RFQ & supplier comparison | **COMPLETE** | `0087`, 11 api tests (`sourcing_test`) |
| B6 | Suppliers | **COMPLETE** | covered by purchasing suite incl. `supplier_edit_test` |
| B7 | POS backend | **COMPLETE** | counters, session binding, shifts, tenders, returns, exchanges, X/Z, concurrency — see §1 |
| B8 | Hardware integration | **N/A (frontend)** | Printer/drawer/scanner are client concerns; `I5` per-terminal config partly in `device.printer_config` |
| B9 | Promotions & pricing | **PARTIAL — with a P0 defect** | `promotions` 5 svc · 4 routes · 16 pkg tests, 0 API tests. `Quote` is reachable; **`Redeem` is called by nothing**, so usage limits never bite and campaign cost is permanently zero. See the B9 section below the table |
| B10 | Returns / exchange | **COMPLETE** | `sales/refund.go`, `exchange.go`, C14 effects, `exchange_test` 9 tests |
| B11 | Quotation → Order → Delivery | **COMPLETE** | `orders` 8 svc · 8 routes · 11 tests; `aftersales` delivery 4 routes |
| B12 | **Wholesale / B2B** | **PARTIAL — 4 of 6 bullets complete** | Re-verified bullet by bullet 2026-09-02, against code rather than route counts. See the breakdown below the table |
| B12a | **`price_dealer` has no owner** | **BLOCKED (specification)** | B1 lists a Dealer/Corporate price tier; B16 lists customer types Retail / Wholesale / VIP. **No customer type selects the dealer price**, and VIP is a loyalty tier in B16, not a price tier. Wiring dealer to VIP, or adding a fourth customer type, would be inventing a rule. **Needs the owner's decision**, not code |
| B13 | Online orders | **PARTIAL** | `orders.Channel` defaults to `store`; delivery workflow exists. No storefront order intake beyond the customer portal |
| B14 | EMI / instalments | **COMPLETE** | `0088`, 7 routes, `aftersales_test` |
| B15 | Warranty / serial / service | **COMPLETE** | `0088`, serials + service-jobs routes, `TestASerialCarriesItsWarrantyFromTheSale` |
| B16 | CRM & loyalty | **COMPLETE** | `loyalty` + `wallet`, gift cards, store credit, 13 api tests (`crm_test`) |
| C1 | Core accounting | **COMPLETE** | `accounting` + `0015`/`0022`/`0025`, 47 pkg + 26 api tests; balanced-entry CONSTRAINT TRIGGER, immutability, period lock, gapless numbering under concurrency |
| C2 | Cash & bank | **COMPLETE** | `treasury` 11 svc · 10 routes · 10 tests |
| C3 | Expenses & investment | **COMPLETE** | `expenses` 9 svc · 8 routes · 13 tests; investors 4 routes |
| C4 | AR / AP | **COMPLETE** | `receivables` 15 svc · 15 routes · **42 api tests** incl. receipt reversal, ageing, credit standing |
| C5/C6 | HR & payroll | **PARTIAL — market gap FIXED 2026-09-03** | `people` 23 svc · 23 routes · 15 tests. **A Bangladeshi shop could not run payroll at all** — not "payroll without a GOSI line", but no payslips and no wages, because `SA.GOSI.RATES` found no rule for `bd` and the registry error killed the run. Each obligation is now asked of the market (`market.SocialInsuranceApplies` / `WageProtectionApplies` / `EndOfServiceApplies`), so a market without the scheme loses that ONE figure and still pays people. **No foreign equivalent is invented** — this product has Saudi's rules and no others. 2 tests, both directions. Saudi values remain unverified |
| C7 | Fixed assets | **COMPLETE** | `assets` 10 svc · 4 routes · 10 tests, depreciation |
| C8 | Shift & drawer | **COMPLETE** | `shift`, blind close, X/Z, `drawer_derivation_test`, `shift_close_race_test` |
| C9 | Double-entry engine | **COMPLETE** | all 12 posting rules as data, resolved at transaction date |
| C10 | Fiscal period / year-end | **COMPLETE** | `fiscal` 5 svc · 14 tests (`yearend_test`, `fiscal_test`) |
| C11 | Bank reconciliation | **COMPLETE** | `treasury`, `treasury_test` |
| C12 | Settlement & gateways | **COMPLETE** | `settlement`+`payments` 11 svc · 13 routes · 6 pkg + 35 api tests |
| C13 | Costing & COGS | **COMPLETE** | tie-out exact incl. negative-stock correction (`0047`/`0048`) |
| C14 | Accounting-aware returns | **PARTIAL — 8 of 9** | **Loyalty reversal IS built** — `sales.reverseLoyalty` takes points back in proportion and rounds *up*, so a partial return cannot leave a customer ahead. The P13 note calling it missing was stale. **Effect 7 (commission) is blocked for a deeper reason than reported: commission is never EARNED on a sale.** `commission_rule` exists; no commission entry is ever attributed to a salesperson, so there is nothing to reverse. Reported honestly in `Refunded.Outstanding` |
| D1 | Reporting suite | **COMPLETE** | `reports` 17 svc · 9 routes · 7 tests; TB, P&L, BS, cash flow |
| D2 | Analytics & forecasting | **PARTIAL** | `insight` 5 svc · 4 routes. **Thin test coverage** (touched only by `studio_test`) |
| D3 | Notifications | **COMPLETE** | `notify` 8 svc · 7 routes; 3 functional tests (preferences, in-app cannot be silenced, announcement reaches inbox) |
| D4 | Audit trail | **COMPLETE** | append-only, `TestAuditLogIsAppendOnly`, `audit.Write` with `actor_label` |
| D5 | Approval centre | **COMPLETE** | `workflow` 12 svc · 10 routes, wired into `expenses.Record` and `purchasing.IssueOrder`. **A P0 defect that made the whole module unusable was found and fixed 2026-09-03** — see below. **10 end-to-end tests**: decision path (6) plus escalation on an elapsed deadline, no escalation inside it, an escalated request still decidable, and delegation refusing backwards cover |
| D6 | Document management | **PARTIAL** | `docs` 7 svc · 4 routes. **Isolation-only test coverage** (`cross_tenant_walk`) |
| D7 | Global search | **PARTIAL** | 1 route, `insight/search.go`. Touched by `studio_test` only |
| E1 | ZATCA | **DEFERRED** | as instructed |
| E2 | Saudi tax engine | **COMPLETE** | multi-market treatments; rates now per treatment |
| E3 | Payment methods | **COMPLETE** | gateways, providers, attempts, settlement |
| E4 | PDPL / privacy | **PARTIAL** | `privacy` 29 svc · 25 routes — a large module with **isolation-only tests**. `SA.PDPL.*` is now gated on `market.PrivacyRegimeApplies` (2026-09-03), so a non-Saudi tenant is told there is no regime on file rather than meeting a registry error. **Functional tests still missing** |
| E7 | Compliance dashboard | **PARTIAL** | `compliance` 1 svc · 1 route · **0 tests** |
| E8 | Regulatory registry | **COMPLETE** | dated values, evidence required, market-aware boot + provisioning gates, 9 tests |
| F1 | Workflow engine | see D5 | |
| F2/F3 | Customer & supplier portals | **PARTIAL** | `portal` 26 svc · 24 routes · **only 4 tests** for a large external-facing surface |
| F4 | Group consolidation | **PARTIAL** | `group` 11 svc · 10 routes · **isolation-only tests** |
| G1 | Country configuration | **COMPLETE** | `tenant.market`, onboarding constraint, `internal/market` |
| G2 | Multi-currency | **PARTIAL** | `fx_rate` on invoices, base-currency allocation in journals. No FX revaluation, no rate source |
| G4 | Tax templates library | **PARTIAL** | treatments per country in registry; jurisdiction model added (`0106`), **no rates** |
| H1 | Security & auth | **COMPLETE** | argon2id, refresh rotation with reuse detection, MFA, httpOnly cookie, lockout |
| H2 | Offline sync | **COMPLETE** | `sync` + `replay.go`, M3 gate, 8 pkg + 12 api tests |
| H3 | Device management | **COMPLETE** | 10 routes, 26 tests, binding model (`0104`) |
| H4 | Backup & restore | **COMPLETE** | `ops` 14 svc · 5 routes; `TestABackupThatRanIsNotABackupThatRestores` |
| H5 | Subscription & billing | **PARTIAL** | **9 tests added 2026-09-02** covering entitlement resolution: plan tier decides, tenant override beats it in both directions, expired override falls back, unknown feature fails closed, no cross-tenant leak, Super-Admin-only mutation. **The resolver is correct and NOTHING CALLS IT** — see the critical finding below. Invoices and dunning remain untested |
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
