# RawSyst POS — Project Status and Open Problems

| | |
|---|---|
| **Last updated** | 2026-08-21 |
| **Branch** | `main` @ `66ceef1` |
| **Backend** | 143 Go files, ~46,100 lines, 879 integration tests + 239 unit tests, 54 migrations |
| **HTTP routes live** | 96 — auth, branding (company logo upload/replace/remove/serve), onboarding, platform, POS (incl. signed-document upload), sync, shifts (open, current, peek, X-report, cash drop, Z-report close), statements, VAT return, catalogue (incl. offline snapshot), returnable lines, exchanges, dashboard + drill-through, purchasing (suppliers, orders, receipts, bills, three-way match, payments, payment reversal, ageing, supplier edit/retire), customers and receivables (accounts, credit limits, ledger, open invoices, receipts, receipt reversal, ageing, till snapshot), terminals (register, enrol, identity, branches, rename/reassign, pause, revoke), companies, reachability ping |
| **Binaries** | `api`, `worker`, `migrate`, `lintwording` |
| **Front ends** | Next.js back-office (dashboard, drill-through, buying) + Tauri POS: login, RBAC gating, counter, offline queue, local catalogue cache, connectivity monitor, hold/resume, returns, exchanges, receipt, Owner Dashboard with A8 drill-through, Buying, Customers (accounts, credit standing, ledger, receipt allocation, receipt reversal, ageing), POS customer picker and credit sales with an offline customer cache, Terminals management and POS pairing (383 tests across shared/pos) |

> Percentages below are estimates of **remaining engineering effort**, not of files
> written. They are deliberately conservative: the parts still missing (front ends,
> reports, HTTP surface) are large, and a status page that flatters the project is
> worse than no status page.

---

## 1. Where things stand

| Layer | Complete | Note |
|---|---:|---|
| **System design + UI/UX specification** | **100%** | 15 documents, design system, clickable prototype |
| **Phase 1 backend** | **~100%** | Sync replay and background jobs done. Everything that remains is blocked on P1 verification or belongs to Phase 2. |
| **Phase 1 front ends** (Tauri POS, Next.js back-office, PWA) | **0%** | Not started, but no longer blocked |
| **Phase 1 overall** | **~52%** | Backend is roughly 55% of Phase 1 effort |
| **Whole product (Phases 1–5)** | **~15%** | Phase 1 is roughly 35% of the total |

**Phase 1's definition of done:** a Saudi shop can legally trade on it.
That is not yet true — see §4.

---

## 2. What is built and proved

Each of these has integration tests that fail loudly if the guarantee breaks.

| Area | Package / migration | Gate proved |
|---|---|---|
| Tenancy + row-level security | `0001`–`0008`, `platform/db` | **M8** — cross-tenant access impossible, superuser connection refused |
| Auth, JWT, refresh rotation, RBAC | `identity`, `api` | **M7** — Cashier refused server-side on every restricted route |
| Tenant provisioning + onboarding | `provisioning` | Owner creation, plan-tier ceilings |
| Regulatory Rule Registry | `registry`, `0009`–`0012` | Dated values, evidence required for `verified_on` |
| Catalog + multi-market tax (SA/BD/US) | `catalog`, `0013` | USA modelled as sales tax, not VAT |
| **Pillar 1 — ZATCA invoice chain** | `zatca`, `0014` | **M2** — 10,000 invoices, no reset, no gap, replay refused |
| **Pillar 2 — double-entry posting** | `accounting`, `0015`, `0022` | **M1** — balance, immutability, period lock, gapless numbering under concurrency |
| **Pillar 3 — sync + idempotency** | `sync`, `0016`–`0017` | **M3** — 500 offline invoices, zero duplicates |
| Sale lines, tenders, price floor | `0018` | Header must agree with its lines |
| Returns + credit notes | `sales/returns.go`, `0019`, `0023` | C14 arithmetic, remainder rule |
| Inventory movements + costing | `inventory`, `0020`–`0021` | **C13 tie-out exact** for FIFO and weighted average |
| **Negative-stock cost correction** | `inventory/shortfall.go`, `0047`–`0048` | **C13's provisional cost** settled on the next receipt; tie-out exact *while stock is negative* |
| **Sale service** | `sales/finalize.go` | All three pillars in one atomic call |
| **Return service** | `sales/refund.go` | C14's nine effects, atomically |
| **POS HTTP surface** | `api/pos_handlers.go` | A till cannot name its own company, store or EGS unit |
| **Cash sessions + X/Z** | `shift`, `0024` | Expected cash derived; blind close real; a Z happens once |
| **Financial statements** | `reports` | Balance sheet balances **including** current earnings |
| **Sync replay** | `sales/replay.go` | Offline sales go through the SAME finalizer as online ones |
| **Background jobs** | `jobs`, `cmd/worker`, `0027`–`0028` | Retry, per-terminal ordering, 12/24/72h escalation |
| **VAT return** | `vat` | Reconciles to the Output VAT account; declares what it omits |
| **Posting rules as data** | `accounting/rules.go`, `0025` | All twelve C9.2 rules; resolved at the transaction date |
| **Customer receipt reversal** | `receivables/reversing.go`, `0049` | New reversing document through `payment.customer` with sides flipped; original frozen; C9.3 holds |
| **Catalogue + variant matrix** | `catalog` | Regeneration adds only what is missing |

### The rounding-remainder rule

It has now appeared in **five** places. Whenever an amount is split, the last
part takes the remainder so the parts sum back to the whole.

1. Invoice discount allocation
2. Partial-return credit
3. Base-currency allocation in a journal entry
4. Restored stock split into cost layers
5. Weighted-average pool emptying to exactly zero

**Any future module that splits an amount must apply it** — commission across
lines, landed cost across a receipt, a settlement batch across sales.

---

## 3. Open problems

Ordered by severity. Each says what is wrong, why it matters, and roughly what
fixing it involves.

### 🔴 Blockers — cannot ship to a paying Saudi shop without these

| # | Problem | Why it matters | Fix |
|---|---|---|---|
| **P1** | **ZATCA `DocumentHasher` is a stub.** The invoice chain is real but the byte-level UBL 2.1 XML, the SHA-256 over it, and the QR TLV encoding are not implemented. Blocked on three registry rules, all unverified against primary sources: `SA.ZATCA.QR_TAG_VALUE_ENCODING`, `SA.ZATCA.UBL_FIELD_SET` and `SA.ZATCA.XML_CANONICALIZATION`. The QR **framing** is no longer among them — 0046 verified the nine tags and the TLV byte layout from Technical Guideline §6, and `zatca.EncodeQR` reproduces both of ZATCA's published payloads byte for byte; what remains open is only how tags 6 to 9 encode their values, which the standard answers two different ways. The other two were split out by 0044 — 0012 had answered part of two rules and stamped `verified_on` on the whole of each, which took the unanswered halves off `registry.Health`'s blocking list. | Without it, no invoice can actually be signed or reported. The chain is correct in structure and empty in content. | Verify the XML Implementation Standard field set, the canonicalisation and the QR TLV field order against ZATCA's published spec, then implement behind the existing `DocumentHasher` seam. The seam was left deliberately. |
| **P31** | **No EGS unit can be onboarded, and the gap is documentary rather than technical.** A desk verification on 2026-08-19 read the four official ZATCA PDFs and recorded what they publish in 0045: the key is EC on **secp256k1** with the CSR signed `ecdsa-with-SHA256` and the public key in compressed form; the certificate template names for production and simulation; the three onboarding URLs per environment with `Basic base64(CSID:Secret)` and `accept-version: v2`; and the OTP rules. Two things are **not** published anywhere in those documents and are now release blockers: `SA.ZATCA.CSR_SUBJECT_LAYOUT` (which X.509 attribute carries each of the nine CSR inputs, the SAN entries, and the OID carrying `certificateTemplateName` — p.57 invokes `-config config.cnf` and never prints the file) and `SA.ZATCA.ONBOARDING_REQUEST_FORMAT` (HTTP verbs, the OTP header name, the CSR body field and its encoding, response schema and status codes — all deferred to Swagger files reachable only from the Integration Sandbox). | A unit can be created and its nine CSR fields captured, and then nothing. No CSR can be built, so no CSID can be obtained, so no invoice can be stamped. This is upstream of P1: even a correct XML builder would have nothing to sign with. | Obtain the official **Fatoora SDK** (for the CSR configuration template) and the **CSID API Swagger files** from the Developer Portal, both of which need portal credentials against an Active VAT registration. Neither can be substituted from vendor blogs — blueprint Part N forbids implementing a compliance feature from Tier 2 alone. Once they are in hand the work is mechanical, and the key must be generated on the terminal because ZATCA forbids exporting it. |
| ~~P2~~ | ~~**No HTTP endpoints for POS.**~~ **DONE** (`0c63d14`). Three routes: `POST /api/v1/pos/sales`, `POST /api/v1/pos/returns`, `GET /api/v1/pos/sales/{id}`. A till never names its company, store or EGS unit — all resolved from the registered device — and the VAT rate and currency are resolved server-side from the registry at the transaction date. | — | — |
| ~~P3~~ | ~~**No shift / cash session management.**~~ **SERVICE DONE** (`98b26d3`), **REACHABLE SINCE** the shift routes landed. Cash sessions, X/Z reports, blind close and cash movements were all built and correct, and **mounted nowhere**: `NewServer` did not take the service, no route called it, and `shift.NewService` appeared once in the repository — in the test harness. Because `sales.resolveTerminal` refuses a till with no open session and opening one was unreachable over HTTP, a paired, signed-in, EGS-bound terminal could not ring up a single sale against the real API. All ten shift tests passed throughout, by calling the service in-process. Six routes now expose it per design 11 §10, and `TestEveryServiceTheServerHoldsIsReachableFromARoute` parses the package to prove every service the Server holds is reached from a mounted handler — the guard that was missing while `TestEveryRouteDeclaresItsAccess` checked only the other direction. **The till screens are now built too.** The Tauri POS has the full C8 workflow: open the till with a denomination count, the shift so far, a supervisor X-report behind `report.view`, cash in and out of the drawer with the direction derived from the reason, and a Z-report close showing Short / Over / Exact in display type per UI spec §7. The count pad totals in BigInt minor units against a per-currency denomination table read from the company, so a Dhaka shop is not offered Saudi notes. The counter reads `GET /shifts/current` and refuses to finish a sale when the server says there is no open session — previously that sale was taken, queued, and rejected at sync with the customer long gone. | — | — |
| ~~P4~~ | ~~**No financial reports.**~~ **DONE** (`69371d8`). Trial Balance, P&L, Balance Sheet and Cash Flow over four routes, gated on `accounting.view`. Balance sheet includes current earnings; cash flow is direct-method and says so. | — | — |
| ~~P5~~ | ~~**No VAT return preparation.**~~ **DONE**. Totals by treatment over a period, reconciled against the Output VAT account. Preparation only: the official form layout is unverified, so nothing is mapped to numbered boxes, and input tax is reported as absent rather than zero. | — | — |
| **P6** | **Front ends.** Tauri POS now trades genuinely offline: login, permission gating, cache-first scan, cart, tenders, durable local queue, sync push, local catalogue cache and a connectivity monitor that triggers the drain on reconnect. Hold/resume, returns, exchanges and a printable receipt are in. The A5 setup wizard is built; PWA not started. | A shop can start and finish a sale with the network down, park and resume carts, refund or exchange against an invoice, and print. | The Owner Dashboard. |

### 🟠 Correctness gaps — real defects, currently latent

| # | Problem | Why it matters | Fix |
|---|---|---|---|
| ~~P7~~ | ~~**Posting rules are hard-coded in Go.**~~ **DONE**. All twelve C9.2 rules seeded as data; sale and return post through the engine. Rules resolve at the transaction date and `journal_entry.rule_version` records which version produced each entry. A rule wanting a figure nobody supplied fails and names it. | — | — |
| ~~P8~~ | ~~**Stock movements carry no `source_id`.**~~ **DONE**. The invoice id is generated in Go before costing runs, so movements point at their sale while a sale that cannot be costed still fails before consuming an ICV. | — | — |
| ~~P9~~ | ~~**Standard-costing variance is discarded.**~~ **DONE**, and **it could not actually post until 0048**. Rule 11 and its favourable twin resolve the `cost_variance` role; `SeedChartOfAccounts` mapped account 5150 to `inventory_variance`, so in every company whose chart came from provisioning — which is every real one — the engine failed on an unresolvable role. The integration test covering rule 11 created its own variance account and mapped the role by hand before selling, proving the rule and the engine and stepping over the join between them. 0048 renames the mapping, and `TestEveryRoleThePostingRulesNameIsInTheChart` now asks the rules which roles they need instead of a person. | — | — |
| **P10** | **The offline costing reconciliation is not built, and design §6.4 no longer describes what was built.** §6.4 assumes the POS sends a provisional cost which the server then corrects by posting the difference. It does not: the till sends quantities, and `sales.Finalize` — which replay uses too, so an offline sale takes the identical path — costs every line itself through `inventory.Consume` against the real layers. **The books are therefore already authoritative, and the previous entry here saying an offline sale's cost is "permanently whatever the device cached" was wrong.** Implementing §6.4 literally would now be a defect: the difference would be posted on top of a COGS figure that is already correct, double-counting it. | Nothing is mis-costed. What is missing is *visibility*: the terminal's own estimate is never reported, so a till whose cached costs have drifted badly from reality cannot be spotted, and there is no exception report to spot it on. | Decide whether §6.4 is retired or reduced to a divergence signal — the device's estimate carried on the sale for comparison only, never posted. Either way it is a documentation decision before it is a coding one, and it is now the smaller half of the problem. |
| ~~P11~~ | ~~**Negative-stock cost does not self-correct.**~~ **DONE** (0047, 0048). An `allow_warn` shortfall is recorded in `cost_shortfall` with the estimate it was charged at, and the next receipt of that variant settles it oldest-first through the ordinary costing engine: the units are drawn out of the arriving stock at what they really cost and the difference posts through rule 11 or 11a as its own entry on the goods receipt, reported to the storeman in words. Partial deliveries settle what they cover and leave the rest open. Selling something never received — provisional cost zero, the case that overstates margin most — is corrected in full when it lands. **A second defect surfaced underneath it:** the valuation ignored uncovered units, so C13's tie-out silently broke by the full value of any sale below zero — the ledger carried the cost of five units while the layers valued zero. `inventory_valuation` now deducts open shortfalls, and settling one moves the valuation by exactly the variance the journal posts, so the invariant holds throughout. | — | — |
| ~~P12~~ | ~~**Credit notes are always `signed_pending_report`.**~~ **DONE**. A note follows the route of the invoice it corrects. | — | — |
| **P13** | **Loyalty and commission reversal (C14 effects 6 and 7) are not built.** | A customer keeps points for goods they returned; a salesperson keeps commission on a refunded sale. | Phase 2 modules. Currently reported honestly in `Refunded.Outstanding` rather than silently skipped — so this is *tracked*, not hidden. |
| ~~P14~~ | ~~**Invoice `human_number` is never populated.**~~ **DONE**. Per store, per year, own series for credit notes, deliberately separate from the ICV. | — | — |
| ~~P23~~ | ~~**A terminal cannot pair itself.**~~ **BACKEND DONE**. An Owner registers a till, the back office issues a single-use code that expires in 15 minutes, and the till redeems it for a secret held in the OS keystore. Signing in on a paired till binds the session to it, and the terminal status is re-read on EVERY request so revoking one takes effect at once rather than when a token expires. The two operator screens are not built — see P24. | — | — |
| ~~P24~~ | ~~**Terminal management has no UI.**~~ **DONE**. Back-office Terminals screen (register, code with a live countdown, rename/move, pause/resume, type-the-name revoke) and a POS first-run pairing screen that gates the till before sign-in. The device secret is held by the Tauri shell in the OS keystore and never enters the web layer. | — | — |
| ~~P26~~ | ~~**The Rust custody layer is written but never compiled.**~~ **DONE**. Rust 1.97.1 (GNU toolchain), `cargo check`, `cargo build`, `cargo clippy -D warnings` and `cargo fmt` all clean; 4 unit tests pass including a real round-trip through the Windows Credential Manager. The whole flow was driven through the actual commands against a live API: pair → keystore → identity → device-bound sign-in (token carries `did`) → authenticated `sync/push` 200 → revoke → immediate refusal. | — | — |
| ~~P27~~ | ~~**The POS has only ever been built for `x86_64-pc-windows-gnu`.**~~ **DONE**. VS Build Tools 2022 (MSVC 14.44, Windows SDK 22621) installed to `D:\VS\BuildTools`; the crate now overrides to `stable-x86_64-pc-windows-msvc`. `cargo check`/`test`/`clippy -D warnings`/`fmt` all clean, `tauri build` produces an MSI and an NSIS setup, the app installs to `%LOCALAPPDATA%\RawSyst POS` and its window opens. Both GNU artefacts disappeared as predicted. The pairing, keystore, device-binding and revocation states were all verified in the running installed application. | — | — |
| **P29** | **The app's GUI is verified by screenshot, not by automation.** `SendKeys` cannot reach WebView2 content, so keystrokes and clicks inside the window are not scripted; each screen was confirmed by driving state from outside and photographing the result. | A UI regression inside the webview would not be caught automatically. | `tauri-driver` + `msedgedriver` gives real WebDriver control of the window. Worth doing once the screens stop changing. |
| **P28** | **The app icon is a generated placeholder.** `pos/src-tauri/icons/icon.ico` is a solid accent-green mark with a white band, written by a script because `tauri-build` cannot link a Windows resource without one. | The till and its installer carry a placeholder rather than a designed identity. | A designer supplies a real mark; `cargo tauri icon` generates the set from it. |
| ~~P30~~ | ~~**A terminal registered through the product could never sell.**~~ **DONE**. 0013 moved the CSID onto an EGS unit and made `device.egs_unit_id` nullable; nothing in the product ever set it, so `sales.resolveTerminal` refused every sale a paired till attempted, and the setup path had said nothing. There is now an E-invoicing screen and four routes behind `einvoicing.view` / `einvoicing.manage` (0043) that create a unit and capture the nine CSR fields, registering a terminal requires naming its unit, and amending one is the repair path for terminals registered before this. `lifecycle.go` reads the CSID from the unit rather than from the deprecated columns on `device`. Nothing here onboards: the CSID columns stay read-only until P1 is verified. | — | — |
| **P32** | **`expense.cash` names a role no chart can map — HALF DONE (0053).** `equity.contribution` is resolved: design 12 §1 names account 3100 "Owner Capital" and the seeded chart had drifted to `owners_equity`, a spelling nothing else in the codebase read, so 0053 renamed the mapping the way 0048 did — the rule keeps its name because a posting rule is versioned data an auditor can be shown. Rule 12 now posts end to end through a provisioned chart. **`expense.cash` is not a drift and is not a missing mapping.** Design 02 rule 5 debits "Expense Account", meaning whichever head the transaction is for, and design 12 §1 offers Rent, Utilities, Salaries, Marketing and Bank Charges as separate heads with no generic account among them. A fixed `role` cannot name the one a user picked, so the rule needs a `for_each` over an expense head — a rule change requiring the expense-head model (with its `input_vat_recoverable` flag, design 02 rule 5) that is not built. Choosing one account for every cash expense would be inventing an accounting rule. | Nothing today: the rule is seeded and never called. It fails on the first day a cash-expense module posts, exactly as rule 11 did. | Belongs to the milestone that builds cash expenses, which must define the expense-head model first. Pinned by `TestEverySeededRuleResolvesAgainstAProvisionedChart`, which asserts the failure rather than skipping it, so whoever fixes the rule is told to update the expectation. |
| ~~P25~~ | ~~**0032 and 0033 never backfilled existing tenants.**~~ **DONE** (0051). **The diagnosis recorded here was wrong and is corrected rather than deleted, because the wrong cause produces a wrong fix.** 0032 and 0033 did not return nothing: they wrote to the platform role *templates* — `WHERE r.tenant_id IS NULL` — and `role_isolation` is `tenant_id = current_tenant_id() OR tenant_id IS NULL`, so templates are visible to a migration carrying no context, and the templates hold the verbs today (checked against the database, not assumed). What they never did was reach the clones tenants already held. That is a different failure from 0037's, which really did match zero rows by selecting over tenant-owned roles. The effect is the same either way: a shop provisioned before 0032 has an Owner cloned from a template that lacked `purchasing.*`, and Purchasing refuses it. 0051 replays **both** grants through 0042's per-tenant loop, so an old tenant ends up with exactly the set a new tenant clones — 0033's `catalog.view` included, or a Purchase Manager could raise an order and not choose a line for it. Nothing outside those two migrations is granted; this is not a general reconciliation of clones against templates. Five integration tests load the migration body out of the embedded set rather than restating it, and were checked against a deliberately broken copy in both directions. The isolation test writes the *careless* form on purpose — with the platform flag on and tenant A selected, an INSERT with no tenant filter still cannot see tenant B, because 0006 kept `role` off the platform plane. | — | — |
| ~~P21~~ | ~~**A receipt cannot be corrected once taken.**~~ **DONE** (0049). `POST /api/v1/receivables/receipts/{id}/reverse` posts a new receipt through `payment.customer` with the sides flipped and `journal_entry.reverses_id` pointing at the original entry. The original row is frozen (facts, allocations, no delete). Open invoices net reversing allocations so the invoice is owed again and C9.3 still ties. Full reversal only — one reversing document per original, a retry with the same client UUID is the original reversal, a second UUID is refused, reversing a reversal is refused. The statement shows the sale, the payment and the reversal together. **A defect on the client:** `reversePayment` minted a fresh UUID on every call, so a timeout retry could not be recognised. The UUID is now assigned when the clerk confirms and reused on retry. The supplier-side mirror is still not built. | — | — |
| **P22** | **The customer credit limit is per company, not per group.** A customer trading with two companies in one group has two independent limits. | A group can be exposed to twice what it agreed. | F4 keeps books separate per company by design, so a group-wide limit needs an explicit group-level record rather than a change here. Phase 5, with consolidation. |
| ~~P33~~ | ~~**A drawer variance is recorded but never posted.**~~ **DONE** (0052). Design 11 §9 says the Z-report variance "posts to a Cash Over/Short account rather than being absorbed silently", and it did not: `shift.Close` wrote `variance` onto `cash_session` and called nothing in `accounting`, so a shop could run short every day for a month while Cash carried a balance the drawer had never held and the loss appeared nowhere in the P&L. Two rules — `cash.shortage` (Dr 5500 / Cr 1100) and `cash.overage` (Dr 1100 / Cr 5500) — with a positive amount and the sides swapped, exactly as 0025/0026 arranged the costing variance; a single signed rule writes a negative debit where a credit belongs. Account 5500 Cash Over/Short is added to the seeded chart **and** mapped for every existing company in the same migration, which is the whole lesson of 0048. Posted inside the transaction that freezes the count, so a close into a locked period is refused whole and the session stays open. An exact drawer posts nothing. An overage deliberately does not go to Other Income: an unexplained surplus is as much a control failure as a shortfall. | — | — |
| **P15** | **Tender fees and settlement are not populated.** `fee_amount` and `settlement_status` default and stay there. | Card settlement reconciliation — matching the acquirer's payout against the day's card takings — cannot happen. The `card_clearing` account will grow forever with nothing clearing it. | Phase 2 settlement module. The clearing account is already correct, which is the part that would have been painful to retrofit. |

| **P35** | **I2 template customization is a logo store, not yet a template system.** Blueprint I2 makes the logo, header, footer, return-policy text, content blocks and QR placement customizable across nine template types. What exists is the storage half: `company_logo` (0054), four routes and a Back Office Branding panel, so a client sets their own mark without anybody editing source. **Nothing renders it.** The only document surface in the product is `receipt.ts` — 42 columns of plain text, deliberately not ESC/POS so it prints on every counter printer — which cannot carry an image, and there is no A4, HTML or PDF surface at all. No template model exists for any of the nine types. | A client can set their branding and see it stored, and the panel says plainly that it does not reach the till receipt. Nothing is claimed that is not true. | A document surface that can hold a picture, which is where the rest of I2 lives too. UI spec §5 invoice detail is the natural first one. J1's S3-compatible object store is also still unbuilt; a logo sits in Postgres for now, which is defensible at 512 KB and one row per company and is a few hundred kilobytes per tenant to move when the object store lands. |
### 🟡 Environment and hygiene

| # | Problem | Why it matters | Fix |
|---|---|---|---|
| ~~P20~~ | ~~**Sign-in is ambiguous when one email exists in two tenants.**~~ **DONE**. Every account for the address is now a candidate; the password is checked against each, and only the ones it opens are the person's. One match signs straight in, so every single-tenant deployment behaves exactly as before. Several returns the businesses for the caller to choose between, with no tokens issued. A supplied `tenant_id` is only ever a filter on the lookup — naming a business you have no account in is refused identically to a wrong password, and a wrong password discloses no businesses at all. | — | — |
| **P16** | **`make check` cannot run `-race` locally.** The installed gcc is 32-bit, so cgo fails. CI on Linux runs it fine. | Race conditions are only caught in CI, not before pushing. | Install a 64-bit MinGW-w64 toolchain, or accept CI as the authority and document it. |
| ~~P17~~ | ~~**Two ways to write journal entries still exist in test code.**~~ **DONE.** Three harnesses (`accounting/posting_test.go`, `inventory/tieout_test.go`, `inventory/shortfall_test.go`) kept entry counters of their own — one incrementing from 1, others with literals like 500, 600 and 900. Nothing collided only because each ran against its own company; `journal_entry` is UNIQUE on (company_id, entry_no), so the first test to post into a company another harness had touched would have failed on a unique violation far from its cause. **The fix was not to route them through `accounting.Post`.** Several of those tests exist to prove a DATABASE guarantee — an unbalanced entry refused by the deferred trigger, a closed period refused by another — and posting them through Go would have the application refuse them first, leaving the test green and the trigger unexercised, which is what that file's header warns against. Raw SQL stays; the NUMBER now comes from `claim_entry_no()`, the counter production uses. `TestNoTestPicksItsOwnEntryNumber` walks the source tree and fails on any test that picks one by hand. | — | — |
| **P34** | **C13's tie-out cannot hold while the ledger and the valuation round at different aggregation levels.** `cost_layer.unit_cost` is `numeric(18,4)` and the weighted-average pool carries `total_value` at the same precision, but `accounting.Post` rounds every base amount to 2 dp and `inventory_gl_difference` compares the two. Receipts at 33.3333 / 16.6667 / 99.9999 value at **716.6663** against a ledger of **716.67**. Reachable in production — `finalize.go` posts `COGSTotal` through that engine. Found by P17, because the tie-out harness had been writing its own journal lines at 4 dp and so compared the valuation against numbers no production path would ever store. **Rounding the valuation to 2 dp was tried and does not fix it.** The ledger is `SUM(round(each posting))` while a rounded valuation is `round(SUM(...))`, and those are not equal: under WAC, two sales of 2 units off that pool give a rounded valuation of 525.56 against a ledger of 525.55. It converts a sub-hallala drift into a whole-hallala one. The attempt was reverted rather than shipped. | C13 says the valuation must tie EXACTLY and any divergence is an exception. A shop on weighted average with awkward purchase prices accrues a difference per sale that never clears. | **The postings must sum back to the valuation** — the project's own rounding-remainder rule (§2), already applied in five places. Post the DELTA of the rounded valuation rather than the rounded delta, so the ledger equals the valuation by construction. That changes the costing-to-posting contract (`Consume` / `finalize.go`) and is an accounting decision, not a test-architecture one. |
| **P18** | **Unverified registry values.** `SA.GOSI.RATES` and `SA.WPS.WAGE_FILE_FORMAT` are placeholders. | Phase 3 payroll cannot be correct without them. | Part N Tier 1 verification pass. **Not a developer's call** — the blueprint is explicit that these must not be filled in from assumption. |
| **P19** | **No PDPL-specific implementation.** Audit logging and RLS exist; consent records, data-subject access/erasure, and the retention schedule do not. | Required before a Saudi go-live. | Depends on legal review (§ open questions in `00-architecture-overview.md`). |

---

## 4. What Phase 1 still needs, in build order

The order matters: each item is easier once the one above it exists.

1. ~~**P2** — POS HTTP endpoints~~ **done**; front-end work is unblocked
2. ~~**P3** — shift management + X/Z reports~~ **done**
3. ~~**P7** — posting rules as data~~ **done**, and the remaining 8 of 12 rules
4. ~~**P4**, **P5**~~ **done**
5. **P14** — invoice numbering engine + receipt templates
6. **P1** — ZATCA XML/QR *(needs the verification pass first — start that now, it has a lead time)*
7. **P6** — Tauri POS, then Next.js back-office, then PWA
8. **P8**, **P9**, **P11**, **P12** — the correctness gaps, each small on its own
9. **P19** — PDPL, gated on legal review

Catalog CRUD and the variant matrix are **done**.

---

## 5. Things that are right and should not be re-litigated

Written down because they were expensive to get right and look arbitrary from
outside.

- **Weighted average uses a pool, not cost layers.** Forcing both methods onto
  layers broke C13's tie-out by 25 on the first sale. See migration `0021`.
- **The application database role is never a superuser and never has `BYPASSRLS`.**
  A superuser bypasses row-level security entirely; `FORCE` only covers the table
  owner. There is a test that refuses such a connection by name.
- **Entry numbers come from a counter on the company row, not `max()+1` and not a
  sequence.** `max()+1` collides under load; sequences are not transactional and
  leave permanent gaps in numbered accounting records.
- **Only cash debits Cash.** Card, wallet and BNPL money debits a clearing
  account, because it is owed by the acquirer and arrives days later minus a fee.
- **Cost of sale comes from the costing engine, never from the till.** Gross
  profit has to be a measurement, and a measurement cannot come from the party
  being measured.
- **Compliance capability is derived state, never a toggle.** Blueprint A4 hard
  rule.
- **No document, UI string or comment may claim certification.** Enforced by
  `make lint-wording`, which fails the build.
