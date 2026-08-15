# RawSyst POS — Project Status and Open Problems

| | |
|---|---|
| **Last updated** | 2026-08-15 |
| **Branch** | `main` @ `7fb0689` |
| **Backend** | 63 Go files, ~24,800 lines, 311 tests, 29 migrations |
| **HTTP routes live** | 31 — auth, onboarding, platform, POS (incl. signed-document upload), sync, statements, VAT return, catalogue |
| **Binaries** | `api`, `worker`, `migrate`, `lintwording` |
| **Front ends** | Tauri POS: login, RBAC gating, counter, offline queue (24 tests) |

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
| **Sale service** | `sales/finalize.go` | All three pillars in one atomic call |
| **Return service** | `sales/refund.go` | C14's nine effects, atomically |
| **POS HTTP surface** | `api/pos_handlers.go` | A till cannot name its own company, store or EGS unit |
| **Cash sessions + X/Z** | `shift`, `0024` | Expected cash derived; blind close real; a Z happens once |
| **Financial statements** | `reports` | Balance sheet balances **including** current earnings |
| **Sync replay** | `sales/replay.go` | Offline sales go through the SAME finalizer as online ones |
| **Background jobs** | `jobs`, `cmd/worker`, `0027`–`0028` | Retry, per-terminal ordering, 12/24/72h escalation |
| **VAT return** | `vat` | Reconciles to the Output VAT account; declares what it omits |
| **Posting rules as data** | `accounting/rules.go`, `0025` | All twelve C9.2 rules; resolved at the transaction date |
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
| **P1** | **ZATCA `DocumentHasher` is a stub.** The invoice chain is real but the byte-level UBL 2.1 XML, the SHA-256 over it, and the QR TLV encoding are not implemented. Blocked on `SA.ZATCA.QR_TLV_FIELDS`, still unverified against primary sources. | Without it, no invoice can actually be signed or reported. The chain is correct in structure and empty in content. | Verify the XML Implementation Standard and QR TLV field order against ZATCA's published spec, then implement behind the existing `DocumentHasher` seam. The seam was left deliberately. |
| ~~P2~~ | ~~**No HTTP endpoints for POS.**~~ **DONE** (`0c63d14`). Three routes: `POST /api/v1/pos/sales`, `POST /api/v1/pos/returns`, `GET /api/v1/pos/sales/{id}`. A till never names its company, store or EGS unit — all resolved from the registered device — and the VAT rate and currency are resolved server-side from the registry at the transaction date. | — | — |
| ~~P3~~ | ~~**No shift / cash session management.**~~ **DONE** (`98b26d3`). Cash sessions, X/Z reports, blind close, cash movements. Sales belong to a session by foreign key, not a time window; a till with no open session cannot sell. | — | — |
| ~~P4~~ | ~~**No financial reports.**~~ **DONE** (`69371d8`). Trial Balance, P&L, Balance Sheet and Cash Flow over four routes, gated on `accounting.view`. Balance sheet includes current earnings; cash flow is direct-method and says so. | — | — |
| ~~P5~~ | ~~**No VAT return preparation.**~~ **DONE**. Totals by treatment over a period, reconciled against the Output VAT account. Preparation only: the official form layout is unverified, so nothing is mapped to numbered boxes, and input tax is reported as absent rather than zero. | — | — |
| **P6** | **Front ends.** Tauri POS now trades offline-first: login, permission gating, scan, cart, tenders, durable local queue, sync push. Local catalogue cache, returns and the receipt remain; Next.js back-office and PWA not started. | A shop can ring up sales on it; it still needs the network to look a barcode up. | Cache the catalogue locally, then returns and receipts. |

### 🟠 Correctness gaps — real defects, currently latent

| # | Problem | Why it matters | Fix |
|---|---|---|---|
| ~~P7~~ | ~~**Posting rules are hard-coded in Go.**~~ **DONE**. All twelve C9.2 rules seeded as data; sale and return post through the engine. Rules resolve at the transaction date and `journal_entry.rule_version` records which version produced each entry. A rule wanting a figure nobody supplied fails and names it. | — | — |
| ~~P8~~ | ~~**Stock movements carry no `source_id`.**~~ **DONE**. The invoice id is generated in Go before costing runs, so movements point at their sale while a sale that cannot be costed still fails before consuming an ICV. | — | — |
| ~~P9~~ | ~~**Standard-costing variance is discarded.**~~ **DONE**. Posted through rule 11, with a second rule for the favourable direction so the amount stays positive. | — | — |
| **P10** | **The offline costing reconciliation is not built.** Design §6.4 specifies: the POS records a provisional cost, and on sync the server recomputes COGS against the real layers and posts the difference to a variance account. | Without it, an offline sale's cost is whatever the device's cached snapshot said, permanently. | Needs the sync path to re-cost and post a `cost_variance` adjustment. Depends on P7 being done first, ideally. |
| **P11** | **Negative-stock cost does not self-correct.** C13 says an `allow_warn` shortfall's cost is provisional and auto-corrects on the next receipt. It does not. FIFO values the shortfall at the last known cost, weighted average at the pool's average, and neither is revisited. | A shop that regularly sells ahead of its paperwork carries a permanently wrong COGS. | Record the shortfall as a pending cost adjustment and settle it on the next receipt of that variant. |
| ~~P12~~ | ~~**Credit notes are always `signed_pending_report`.**~~ **DONE**. A note follows the route of the invoice it corrects. | — | — |
| **P13** | **Loyalty and commission reversal (C14 effects 6 and 7) are not built.** | A customer keeps points for goods they returned; a salesperson keeps commission on a refunded sale. | Phase 2 modules. Currently reported honestly in `Refunded.Outstanding` rather than silently skipped — so this is *tracked*, not hidden. |
| ~~P14~~ | ~~**Invoice `human_number` is never populated.**~~ **DONE**. Per store, per year, own series for credit notes, deliberately separate from the ICV. | — | — |
| **P15** | **Tender fees and settlement are not populated.** `fee_amount` and `settlement_status` default and stay there. | Card settlement reconciliation — matching the acquirer's payout against the day's card takings — cannot happen. The `card_clearing` account will grow forever with nothing clearing it. | Phase 2 settlement module. The clearing account is already correct, which is the part that would have been painful to retrofit. |

### 🟡 Environment and hygiene

| # | Problem | Why it matters | Fix |
|---|---|---|---|
| **P16** | **`make check` cannot run `-race` locally.** The installed gcc is 32-bit, so cgo fails. CI on Linux runs it fine. | Race conditions are only caught in CI, not before pushing. | Install a 64-bit MinGW-w64 toolchain, or accept CI as the authority and document it. |
| **P17** | **Two ways to write journal entries still exist in test code.** `accounting/posting_test.go` and `inventory/tieout_test.go` write `journal_entry` directly with their own `entry_no`, bypassing `claim_entry_no()`. | They currently use separate companies so nothing collides, but a future test that mixes both would fail confusingly. Production code correctly uses `accounting.Post` only. | Move those harnesses onto `accounting.Post`. Low urgency, real trap. |
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
