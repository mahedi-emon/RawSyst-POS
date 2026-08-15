# Module status — what is built

Last verified 2026-08-15. **136 tests, CI green, migrations 0001–0019.**

## Three pillars — all done, all gated
| Pillar | Package | Gate |
|---|---|---|
| Invoice chain (ZATCA) | `internal/zatca` | **M2** — 10,000 invoices, no reset, no gap |
| Double-entry posting | migration 0015 + `internal/accounting` | **M1** — balance, immutability, period lock |
| Sync & idempotency | `internal/sync` | **M3** — 500 offline invoices, zero duplicates |

Also passing: **M7** (Cashier refused on every restricted route), **M8** (cross-tenant impossible).

## Built on top
- `internal/identity` — auth, JWT, refresh rotation with reuse detection, RBAC with 4 scope dimensions, Super-Admin-assisted recovery
- `internal/provisioning` — tenant + Owner creation, 7-step onboarding wizard, plan-tier ceilings
- `internal/catalog` — schema + **multi-market tax engine (SA/BD/US)**
- `internal/sales` — pricing (`pricing.go`) and returns (`returns.go`)
- `internal/api` — router with an enforced permission registry
- `internal/registry` — dated legal values, 15 verified against ZATCA primary sources

## The rounding-remainder rule (applies in two places, will apply again)
Whenever an amount is split into parts, **the last part takes the remainder** rather than being computed independently. Otherwise the parts do not sum back to the whole and money quietly goes missing.

1. **Invoice discount allocation** (`Compute`) — last line takes what is left.
2. **Partial returns** (`ComputeReturn`) — whichever return exhausts a line credits the remainder. Missing this cost a hallala on every 3-way split: 33.33 × 3 = 99.99 against 100.00 charged.

Anywhere a future module splits an amount — commission across lines, landed cost across a receipt, a settlement batch across sales — apply the same rule.

## Standing guards that fail on careless changes
- `TestPlatformAdminHasNoBusinessDataAccess` — a new table granting Super Admin access must be justified in the allowlist
- `TestConnectionCannotBypassRowLevelSecurity` — refuses a superuser/BYPASSRLS connection, naming the cause
- `TestEveryRouteDeclaresItsAccess` / `TestPublicRoutesAreOnlyTheExpectedOnes`
- `TestVerifiedRulesCarryTheirEvidence` — no stamping `verified_on` without citing document, version and section
- `make lint-wording` — forbidden compliance claims fail the build

## Not built yet
Catalog CRUD service and variant matrix · inventory movements and costing engine · POS HTTP endpoints · customers/CRM · purchasing · the ZATCA `DocumentHasher` (blocked on `SA.ZATCA.QR_TLV_FIELDS`, still unverified) · web and Tauri front ends.

## Related
[[code/pillars-status]] · [[code/backend-state]] · [[design/index]] · [[architecture/target-markets]]
