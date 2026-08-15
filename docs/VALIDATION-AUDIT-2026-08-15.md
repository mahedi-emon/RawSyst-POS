# RawSyst POS — Validation Audit

| | |
|---|---|
| **Date** | 2026-08-15 |
| **Commit audited** | `5094c7c` (`main`) |
| **Auditor** | Claude (automated audit, no architecture changes made) |
| **Method** | Live database interrogation, full test suite execution, static sweep of tracked files, prototype inspection, ZATCA source cross-check |

---

## 1. Overall validation status

# ⛔ **BLOCKER — audit cannot be completed as scoped**

**There is no frontend application to validate.**

The repository contains exactly two source trees:

```
backend/   Go 1.26 — 34 files, ~14,200 lines, 190 tests, 23 migrations
docs/      specifications, design system, and one static prototype
```

There is no `package.json`, no `next.config.*`, no `tauri.conf.json`, no
`src-tauri/`, no `.tsx`/`.jsx`/`.vue` file anywhere. The only HTML in the
repository is `docs/ui-ux/prototype.html` — a single-file static design
prototype with 988 lines and no build step, no router, and no data layer.

**Consequently, 9 of your 20 validation categories have no artifact to audit.**
They are reported below as `NOT-AUDITABLE` rather than given a pass or a fail.
Marking them PASS would be false; marking them FAIL would be misleading, since
nothing is broken — nothing exists.

This is not a new discovery. It is item **P6** in
[`PROJECT-STATUS.md`](PROJECT-STATUS.md), committed earlier the same day.

### What *was* audited, and its result

| Domain | Status |
|---|---|
| Multi-tenancy / Row-Level Security | ✅ **PASS** — strongest area in the codebase |
| Accounting / double-entry integrity | ✅ **PASS** |
| Data calculation correctness | ✅ **PASS** |
| Secrets handling & ZATCA key custody | ✅ **PASS** |
| Offline / sync idempotency (server side) | ✅ **PASS** |
| Regulatory rule registry discipline | ✅ **PASS** |
| Design prototype (RTL, theming, responsive) | ⚠️ **WARNING** — sound, with 4 defects |
| ZATCA invoice content (XML/QR) | ⛔ **BLOCKER** — stub, unverified |
| Everything requiring a running UI | ⬜ **NOT-AUDITABLE** |

---

## 2. Passed checks

### 2.1 Multi-tenancy and RLS — PASS

Audited by interrogating the **live database catalogue**, not by reading
migrations, so this reflects what Postgres actually enforces.

```
43 tables examined
 0 tenant-scoped tables with an isolation gap
```

Every table carrying a `tenant_id` has `ENABLE ROW LEVEL SECURITY`,
`FORCE ROW LEVEL SECURITY`, and exactly one isolation policy. Five tables have
no RLS, and all five are correctly global reference data:
`plan_tier_default`, `posting_rule`, `regulatory_authority`, `regulatory_rule`,
`schema_migration`.

Two tables have RLS but no `tenant_id` column — `tenant` and `role_permission` —
scoped by identity and by join respectively. `role_permission` is worth noting:
it previously had **no RLS at all**, a genuine cross-tenant leak that let any
tenant read another's role configuration. Fixed in migration `0008`.

Connection privileges:

```
connected as "rawsyst": superuser=false  bypassrls=false  createrole=false
```

This matters more than it looks. A superuser bypasses RLS **entirely** —
`FORCE` only constrains the table owner. There is a standing test,
`TestConnectionCannotBypassRowLevelSecurity`, that refuses such a connection and
names the cause. It was written after CI silently ran as a superuser and turned
every isolation test green for the wrong reason.

Cross-tenant attack tests that currently pass:

- `TestPlatformAdminHasNoBusinessDataAccess`
- `TestConsumeRefusesACompanyInAnotherTenant` — and asserts the victim's stock is untouched
- `TestPostingIntoAnotherTenantsCompanyIsRefused` — and asserts the victim's books stay empty

**Locked decision B (shared PostgreSQL + RLS, not database-per-tenant) is
correctly implemented.**

### 2.2 Accounting — PASS

- Balance is enforced by a `DEFERRABLE INITIALLY DEFERRED` constraint trigger at
  the database, not by application code. A background job, a migration or a
  support script hits the same wall.
- `accounting.Post` additionally refuses in Go **and states the difference**, so
  a developer gets an actionable message rather than a constraint name.
- Posted entries are immutable by trigger. Corrections are reversing entries.
- Fiscal period `open → closed → locked` enforced; reopening needs an owner and
  a written reason of at least 10 characters.
- Entry numbering is gapless and collision-free under concurrency — verified by
  12 concurrent goroutines each claiming a distinct number with no gaps.
- Idempotency key `(source_type, source_id, rule_key)` means a replayed sale
  cannot double-post.

**Locked decision D (double-entry mandatory, every transaction balanced) is
correctly implemented.**

### 2.3 Data calculations — PASS

All 190 tests pass, unit and integration. Coverage of the specific arithmetic
you listed:

| Calculation | Evidence |
|---|---|
| VAT-inclusive back-calculation | `TestVATInclusivePricingBackCalculates` |
| VAT-exclusive | `TestTaxExclusivePricingAddsTax` |
| Lines sum to total exactly | `TestLinesSumExactlyToTheTotal` |
| Discount allocation | `TestInvoiceDiscountAllocationSumsBackExactly` |
| Zero-rated / exempt | `TestNonStandardTreatmentsCarryNoTax`, `TestZeroRatedLineRefundsNoTax` |
| Mixed rates on one invoice | `TestMixedTreatmentsOnOneInvoice` |
| Decimal quantities | `TestFractionalQuantity` |
| Negative / invalid | `TestNegativeReturnQuantityIsRefused`, `TestDiscountLargerThanTheSaleIsRefused` |
| Split tender | `TestSplitTenderMustCoverTheTotalExactly` |
| COGS | `TestCOGSIsAppliedFromTheCostingEngine` |
| Inventory ↔ GL tie-out | `TestValuationTiesToTheLedger` (FIFO + WAC) |

**Decimal throughout** — `numeric(18,4)` in the database, `shopspring/decimal`
in Go, strings on the wire. No float64 anywhere on a money path.

**The rounding-remainder rule now appears in five places.** Whenever an amount
is split, the last part takes the remainder so the parts sum back to the whole:
invoice discount, partial-return credit, base-currency allocation, restored
stock layers, and the weighted-average pool emptying to zero. Each was a real
defect caught by a test before it shipped — the partial-return one was costing
a hallala on every three-way split.

### 2.4 Secrets and ZATCA key custody — PASS

Full sweep of tracked files for key material, credentials and API keys.
**Zero findings.** Every hit was CSID *metadata* — `csid_serial`,
`csid_issued_at`, `csid_expires_at`, `csid_status` — never key bytes.

- No `crypto/ecdsa`, `crypto/x509`, `ParsePKCS*` or any `Sign()` call exists
  server-side. The server genuinely cannot sign.
- `logging.go` redacts `jwt`, `refresh_token`, `private_key`, `csid`, `api_key`.
- `.env` is gitignored; only `.env.example` is tracked. `*.pem` and `*.key` are
  gitignored.
- Migration `0012` records `"export_of_stamping_private_key": "prohibited"` as a
  registry value with its source — the ZATCA §6.5 rule is *data*, not a comment.

**Locked decision A (terminal-side signing, key in OS keystore, never in cloud)
is correctly implemented — by construction, not by convention.**

### 2.5 Offline / sync — PASS (server side)

- **Locked decision C** — stock is stored as *movements with signed deltas*,
  never as an absolute level. There is deliberately no stored level column. The
  reasoning is in migration `0020`: three tills each selling 4 units produce −12
  in any arrival order, whereas three absolute levels overwrite each other and
  two of the three sales vanish.
- **Locked decision E** — no duplicate effects after sync, proved at three
  layers: invoice UUID assigned on the device (`TestTheSameSaleArrivingTwiceIsRungOnce`
  asserts one invoice, one ICV, one lot of revenue, **one stock movement**);
  journal idempotency key; and `TestTheSameReturnArrivingTwiceRefundsOnce`.
- QA gate **M3** passes: 500 offline invoices, zero duplicates.

### 2.6 Regulatory rule registry — PASS

`Service.gate()` refuses to serve an unverified value when `requireVerified` is
set, and the refusal names the rule and its source document. Values carry
`verified_on`, source document, version and section.
`TestVerifiedRulesCarryTheirEvidence` prevents stamping `verified_on` without
citing evidence. `make lint-wording` fails the build on any claim of
certification or guaranteed compliance — the exact prohibited phrases are
listed in `cmd/lintwording`. 110 files scanned, passing.

The guard proved itself during this audit: an earlier draft of this very
document quoted those phrases while describing the rule, and the build refused
it. A linter that exempts the document explaining the linter is not a linter.

---

## 3. Failed checks

| # | Check | Status | Detail |
|---|---|---|---|
| F1 | ZATCA XML / QR generation | ⛔ BLOCKER | `DocumentHasher` is a stub. No invoice can be signed or reported. |
| F2 | POS HTTP endpoints | ⛔ BLOCKER | Services exist and are tested; nothing exposes them. 14 routes live, all auth/onboarding/platform. |
| F3 | Posting rules as data | 🔴 FAIL | `posting_rule` table exists, is versioned and immutable — **zero code queries it**. Rules are Go literals. Contradicts C9.2. |
| F4 | Standard-costing variance posting | 🔴 FAIL | Variance is computed then discarded. Defeats the purpose of standard costing. |
| F5 | Stock movement traceability | 🟠 WARNING | Sale movements carry `source_type` but a null `source_id`. Cannot trace stock to its invoice. |
| F6 | Prototype: no `aria-live` | 🟠 WARNING | Offline/queue counter changes dynamically with no live region. Screen readers never hear it. |
| F7 | Prototype: unlabelled input | 🟠 WARNING | 1 `<input>`, 0 `<label>`. |
| F8 | Prototype: thin focus styling | 🟠 WARNING | A single `:focus-visible` rule for 28 buttons. |

---

## 4. Critical issues (BLOCKER)

### C1 — ZATCA invoice content is not implemented

The chain is real: ICV allocation, PIH hash chaining, replay/gap/linkage refusal,
verification across 10,000 invoices. **The content is not.** UBL 2.1 XML, the
SHA-256 over the canonical bytes, and the QR TLV encoding do not exist.

Blocked on `SA.ZATCA.QR_TLV_FIELDS`, still unverified against primary sources.
This was left as a **named seam** (`DocumentHasher`) rather than guessed at, and
that was the right call — but it means the compliance story is structurally
correct and empty.

**Nothing can be reported or cleared to ZATCA today.**

### C2 — No frontend exists

Blocks categories 1, 2, 3, 4, 6, 8, 14, 15, 19 entirely and 12 partially.

### C3 — No POS HTTP surface

No client could ring a sale even if a client existed.

---

## 5. High-priority issues

| # | Issue | Impact |
|---|---|---|
| H1 | **Posting rules hard-coded** (F3) | Only 4 of the 12 named rules exist. Every new transaction type becomes a code release; no tenant can vary its chart-of-accounts mapping. The account-role indirection is already built — the hard half is done. |
| H2 | **No shift / cash session** | X/Z reports, drawer counts, blind close — all Phase 1, all absent. A shop cannot reconcile its till. Cash handling is where retail theft happens. |
| H3 | **No financial reports** | Trial Balance, P&L, Balance Sheet, Cash Flow. Only invariant-check SQL functions exist. An accountant cannot use this. |
| H4 | **No VAT return preparation** | The single most important monthly task for a Saudi shop. |
| H5 | **Offline cost reconciliation absent** | Design §6.4 specifies provisional cost → server recompute → variance. Not built. Offline sale costs are permanently whatever the device cached. |
| H6 | **Negative-stock cost never self-corrects** | C13 says an `allow_warn` shortfall auto-corrects on next receipt. It does not. |

---

## 6. Medium / low-priority issues

| # | Issue |
|---|---|
| M1 | Credit notes always `signed_pending_report`; a B2B credit note should follow the clearance route (F5 in PROJECT-STATUS = P12). |
| M2 | `sales_invoice.human_number` never populated — numbering engine missing. |
| M3 | Tender `fee_amount` / `settlement_status` never populated; `card_clearing` will grow with nothing clearing it. |
| M4 | Loyalty and commission reversal (C14 effects 6 & 7) not built — **honestly reported** in `Refunded.Outstanding`, not hidden. |
| M5 | Two ways to write journal entries remain in *test* code, bypassing `claim_entry_no()`. Latent collision trap. |
| M6 | `make check -race` cannot run locally (32-bit gcc); CI is the only race detector. |
| M7 | Prototype hard-codes "VAT 15%" as display text. Correct as sample data, but must not be copied into implementation — the same file correctly documents the registry principle on line 888. |

---

## 7. Broken pages / routes

**NOT-AUDITABLE.** No routes exist beyond the 14 backend endpoints, which were
verified present and correctly declared:

```
/healthz  /readyz  /api/v1/meta/version
/api/v1/auth/{login,refresh,logout,me,change-password}
/api/v1/onboarding{,/company,/steps/{step},/steps/{step}/complete}
/api/v1/platform/tenants
/api/v1/platform/users/{userID}/reset-password
```

`TestEveryRouteDeclaresItsAccess` and `TestPublicRoutesAreOnlyTheExpectedOnes`
guard this table — a new route cannot be added without declaring its access
level. **PASS** for what exists.

---

## 8–11. UX/UI, responsive, RTL, dark/light

**Implementation: NOT-AUDITABLE.** Prototype audited instead.

### Prototype findings — ⚠️ WARNING (sound foundation, 4 defects)

**RTL — PASS, and better than most production apps.** The stylesheet uses
**exclusively logical properties**:

| Physical (RTL-hostile) | Count | Logical (RTL-correct) | Count |
|---|---:|---|---:|
| `margin-left` / `margin-right` | **0** | `margin-inline` | 20 |
| `padding-left` / `padding-right` | **0** | `padding-inline` | 1 |
| `text-align: left` / `right` | **0** | `text-align: start` / `end` | 9 |
| `border-left` / `border-right` | **0** | `border-inline` | 3 |

Zero physical directional properties means RTL mirroring is automatic rather
than patched. The `dir`/`lang` toggle sets both attributes together, and an
Arabic font stack is bound to `:lang(ar), [dir="rtl"]`.

**Theming — PASS.** Correct three-state handling: tokens on bare `:root`,
`@media (prefers-color-scheme: dark)` guarded as `:root:not([data-theme="light"])`,
and `:root[data-theme="dark"]` so an explicit toggle wins in both directions.

**Responsive — PARTIAL.** Four layout breakpoints (1180 / 1100 / 1000 / 620 px)
plus `prefers-reduced-motion`. Covers phone, tablet and desktop. **No breakpoint
above 1180px**, so large-desktop behaviour is untested and unspecified.

**Accessibility — WARNING.** 28 real `<button>` elements (not clickable divs),
11 `aria-pressed`, 5 `aria-current`, 4 `aria-hidden`. But:
- **0 `aria-live`** — the offline banner and queue counter change dynamically and announce nothing
- **0 `<label>`** for the 1 `<input>`
- **1** `:focus-visible` rule total

**Arithmetic — PASS.** Displayed figures are internally consistent:
1,012.17 × 0.15 = 151.8255 → 151.83 ✓, totalling 1,164.00.

---

## 12. Offline / sync problems

Server-side: **PASS** (§2.5). Client-side: **NOT-AUDITABLE** — no local SQLite,
no queue UI, no reconnection handling, because there is no client.

The prototype *depicts* the intended UX (offline banner, queue counter, per-terminal
chain explanation) and the depiction is consistent with the architecture.

---

## 13. Security problems

| Check | Status | Note |
|---|---|---|
| SQL injection | ✅ PASS | Parameterised queries throughout (`pgx`); no string-concatenated SQL found |
| Secrets in Git / frontend / logs | ✅ PASS | Zero findings; logger redacts |
| Private key exposure | ✅ PASS | Server has no signing capability at all |
| Broken authorization | ✅ PASS | QA gate M7 — Cashier refused server-side on every restricted route |
| Session handling | ✅ PASS | Refresh rotation with reuse detection via a per-token chain table |
| Cross-tenant / IDOR | ✅ PASS | RLS is the final backstop; another tenant's row reads as *absent*, not *forbidden* — correct, since existence is not the caller's business |
| XSS / CSRF | ⬜ NOT-AUDITABLE | No browser surface |
| File upload | ⬜ NOT-AUDITABLE | No upload endpoint |
| API error leakage | ✅ PASS | `db.Translate` converts driver errors to user-facing sentences |

---

## 14. Multi-tenant / RLS problems

**None found.** See §2.1. This is the strongest area of the codebase.

---

## 15. Accounting / data-calculation problems

**None found in what is implemented.** See §2.2–2.3.

Gaps are of *absence*, not incorrectness: F3 (rules not data), F4 (variance
discarded), H3/H4 (no reports), H5/H6 (no cost reconciliation).

---

## 16. ZATCA / compliance problems

| Element | Backend | UI |
|---|---|---|
| Invoice type (standard/simplified/credit/debit) | ✅ modelled + constrained | ⬜ none |
| UUID (device-assigned) | ✅ | ⬜ |
| ICV (non-resetting, gapless) | ✅ proved to 10,000 | ⬜ |
| PIH (SHA-256 chain) | ✅ | ⬜ |
| Chain verification / break detection | ✅ `zatca_chain_breaks()` | ⬜ |
| Credit note on its own chain position | ✅ | ⬜ |
| **XML / QR content** | ⛔ **stub** | ⬜ |
| CSID / EGS lifecycle states | ✅ modelled | ⬜ |
| Reporting vs clearance route | ⚠️ sale correct; credit note always reporting (M1) | ⬜ |
| Submission / retry / error status | ⬜ not built | ⬜ |

### Your ZATCA process list — checked against ZATCA sources

Your 10-step list is **correct and complete in sequence**. Three refinements
from the current sources:

1. **CSID is per EGS Unit, not per taxpayer.** ZATCA defines it as identifying
   "an Invoice Generation Solution Unit associated with a Taxpayer." Your
   schema already models it at `egs_unit`, and migration `0013` explicitly
   deprecates the older `device.csid_serial`. ✅ Already right.
2. **Onboarding starts with an OTP generated in the FATOORA portal** per EGS
   Unit — worth adding to your step 6.
3. **The Sandbox is driven by swagger files** that simulate onboarding, test
   CSID issuance, and both B2B and B2C submission — so steps 5–6 can be
   rehearsed end-to-end before touching production.

**Your most important point is confirmed by ZATCA itself:** passing the SDK does
**not** make software "approved" or "certified." Integration Phase requirements
must be met separately. This is exactly why `make lint-wording` fails the build
on any certification claim — that guard is correct and should stay.

**Wave timing:** Phase 2 is rolled out by wave, not on one date. Wave 24
taxpayers must integrate **by 30 June 2026** — already past. Which wave your
first client falls in determines their actual deadline, and that is a question
for the client's ZATCA correspondence, not for the codebase.

---

## 17. Performance problems

**NOT-AUDITABLE** for frontend. Backend observations, not measurements:

- Indexes exist on every hot path (FIFO layer lookup, chain lookup, tenant scoping, sync cursor).
- Full integration suite runs in ~160s including database round-trips.
- The `zatca` package takes 47s — dominated by the 10,000-invoice chain test, which is intentional.
- No N+1 pattern found: `resolveRoles` batches with `= ANY($2)`.
- **Not yet measured:** API latency under load, connection-pool sizing under concurrency, large-list pagination (no list endpoints exist).

---

## 18. Missing implementation vs approved requirements

Phase 1 = "a Saudi shop can legally trade on it." Against that bar:

| Phase 1 requirement | Status |
|---|---|
| Tenancy / auth / RBAC | ✅ Done |
| Regulatory Rule Registry | ✅ Done |
| Products + variants + inventory | ⚠️ Schema + costing done; **CRUD service and variant matrix absent** |
| Offline POS with local ZATCA signing | ⛔ Chain done server-side; **XML/QR stub; no terminal app** |
| Sync engine | ⚠️ Server done; **no local SQLite** |
| Posting engine + core postings | ⚠️ Engine done; **4 of 12 rules, hard-coded** |
| TB / P&L / BS | ❌ Absent |
| VAT return prep | ❌ Absent |
| Mada + cash + card + split tender | ✅ Done |
| Shift X/Z | ❌ Absent |
| Numbering + receipt templates | ❌ Absent |
| Arabic RTL | ⚠️ Design + prototype only |
| Core PDPL | ⚠️ Audit log + RLS only; consent, DSAR, retention absent |

---

## 19. Recommended fixes

**Do not start frontend work first.** Build the HTTP surface, or every screen
gets built against an imagined API and rewritten.

| Order | Fix | Rationale |
|---:|---|---|
| 1 | **POS HTTP endpoints** (C3) | Unblocks all frontend work. Everything behind it is built and tested. |
| 2 | **Start the ZATCA Tier-1 verification pass** (C1) | Long lead time — begin in parallel, not after. |
| 3 | **Posting rules as data** (H1) | Gets harder with every rule added as a Go literal. |
| 4 | **Shift management + X/Z** (H2) | Self-contained; no dependencies. |
| 5 | **Reports + VAT return** (H3, H4) | The posting engine already holds the data. |
| 6 | **Tauri POS**, then back-office, then PWA (C2) | POS first — compliance-critical surface. |
| 7 | F4, F5, M1, M2 | Each small; batch them. |
| 8 | Prototype a11y: `aria-live`, `<label>`, focus styles, >1180px breakpoint | Fix in the prototype **now** so the defects are not inherited by every screen built from it. |

---

## 20. Final readiness score

| Dimension | Score | Basis |
|---|---:|---|
| **Backend correctness** (what exists) | **9 / 10** | 190 tests, all green. Every invariant enforced at the database, not just in code. Five separate rounding defects found and fixed by tests before shipping. |
| **Security & tenant isolation** | **9 / 10** | Zero RLS gaps across 43 tables; no secrets; server structurally incapable of signing. −1 because no browser surface has been exercised. |
| **Backend completeness vs Phase 1** | **6 / 10** | Engines done; reports, shift, HTTP surface, catalog CRUD absent. |
| **ZATCA readiness** | **3 / 10** | Chain excellent, content absent, verification pass not started. |
| **Frontend** | **0 / 10** | Does not exist. |
| **Design readiness** | **8 / 10** | Specification and prototype strong; 4 defects to fix before they propagate. |

# Production readiness: **28%** — ⛔ **NOT READY**

**The engine room is in very good order. There is no ship around it yet.**

The backend's correctness discipline is genuinely above average — invariants
enforced at the database rather than trusted to application code, and a pattern
of writing the test that asserts the guarantee and letting it find the defect.
That pattern found the WAC tie-out divergence, the `role_permission` leak, the
dead `Chain.Verify`, the CI superuser, and the partial-return hallala.

What stands between here and a shop that can legally trade: **the ZATCA XML/QR
content, an HTTP surface, and an entire frontend.**

---

## Appendix — audit method

| Claim | How it was verified |
|---|---|
| No frontend | Recursive glob for `*.tsx,*.jsx,*.vue,*.svelte,package.json,next.config.*,tauri.conf.json,index.html,*.html` across the whole repo |
| RLS coverage | Live query against `pg_class` / `pg_policies` / `information_schema.columns` |
| Connection privileges | Live query against `pg_roles` for `current_user` |
| No secrets | `git grep -E` over all tracked files for key headers, credentials, AWS keys, assigned passwords |
| No server-side signing | Source sweep for `crypto/ecdsa`, `crypto/x509`, `ParsePKCS`, `Sign(`, `PrivateKey` |
| Test results | `go test -count=1 ./...` and `-tags=integration ./...`, full output |
| Route table | Static extraction of the declared route table in `internal/api` |
| Prototype RTL | Counted physical vs logical CSS properties |
| ZATCA process | ZATCA published guidance (see below) |

**Sources consulted:**
[ZATCA E-Invoicing](https://zatca.gov.sa/en/E-Invoicing/Pages/default.aspx) ·
[Phase 2 requirements FAQ](https://zatca.gov.sa/en/E-Invoicing/Introduction/FAQ/Pages/FAQ_016.aspx) ·
[Roll-out phases](https://zatca.gov.sa/en/E-Invoicing/Introduction/Pages/Roll-out-phases.aspx) ·
[Detailed Technical Guidelines](https://zatca.gov.sa/en/E-Invoicing/Introduction/Guidelines/Documents/E-invoicing-Detailed-Technical-Guideline.pdf) ·
[Developer Portal Manual](https://zatca.gov.sa/en/E-Invoicing/SystemsDevelopers/ComplianceEnablementToolbox/Documents/Developer%20Portal%20User%20Manual.pdf) ·
[Wave 24 criteria](https://zatca.gov.sa/en/Pages/news_1426.aspx)

**No architecture was changed and no code was modified during this audit.**
