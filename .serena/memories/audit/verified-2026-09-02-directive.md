# Verification pass — 2026-09-02 (Master Implementation Directive)

Ran against the real code, not the reports. **Everything green.**

## What was actually executed
| Check | Result |
|---|---|
| `go build ./...` · `go vet ./...` | clean |
| `go test -count=1 -tags=integration ./...` with `RAWSYST_DB_DSN` set | **all pass**, 20 packages with tests. `internal/api` ran **1034s**, `platform/db` **836s**, `zatca` 394s, `identity` 206s — real Postgres work, not skips |
| `shared` vitest | 438 pass |
| `pos` vitest | 229 pass |
| `shared` `tsc --noEmit` | clean |

**The DSN matters**: without `RAWSYST_DB_DSN` every DB test skips and still prints `ok`. Load `backend/.env` with `set -a; . ./.env; set +a` first. See [[project_backend_tests_need_dsn]].

## The status doc UNDERSTATES the code
`docs/PROJECT-STATUS.md` is behind the repository, in the direction of more being done than it claims:
- Says **70 migrations**; there are **102**.
- Says 1,285 + 425 tests; frontend alone is **667**.
- **P44 is stale.** It says `inventory.adjust_stock` / `inventory.transfer_stock` are "seeded permissions with no route" and stock counts/transfers are Phase 2. They are **built**: `internal/stockops` (adjust · count · transfer · read) with ~14 live routes under `/api/v1/stock/*` including counts open/save/post/cancel and transfers create/approve/dispatch.

Lesson: verify before believing either direction. The reports here err toward pessimism.

## Real gaps against the directive (not defects — scope)
1. **No POS in the web app.** `web/components/BackOffice.tsx` has ~30 destinations and none is POS. The counter (`pos/src/pos/PosCounter.tsx`, 848 lines) lives in the Vite/Tauri app only.
2. **Platform Owner cannot create a business from the UI.** `POST /api/v1/platform/tenants` → `handleCreateTenant` exists and is Super-Admin gated; **no frontend calls it**. `shared/src/api/admin.ts` only does the GET list.
3. **Country/market is not chosen at business creation.** `provisioning.NewTenant` carries `Name · DataRegion · PlanTier · OwnerEmail · OwnerName` — no country. Country is set later by the Business Owner in onboarding step `business_info`. The directive puts that choice with the Platform Owner.

Markets already supported end to end: `sa` · `bd` · `us` (`supportedCountries` in `provisioning/onboarding.go`), currencies SAR · BDT · USD.

## Tauri coupling is narrow (good news for a web POS)
Only **three** files import `@tauri-apps`: `offline/sqlite.ts`, `offline/credential.ts`, `pos/terminal.ts`. The offline layer is already written against interfaces — `QueueStore`, `CatalogueStore`, `CustomerStore`, `HeldCartStore` — so SQLite is one implementation, not the only possible one. `PosCounter.tsx` imports no Tauri API directly.

**Custody constraint:** `credential.ts` refuses to run outside Tauri on purpose — the device secret lives in the OS keystore and a localStorage fallback is called out in the source as how a secret reaches production. A browser has no keystore.

**Owner's decision (2026-09-02): build the web POS online-only first** — `/api/v1/pos/*`, no offline queue, no device pairing. Offline custody is a separate later decision.

## Related
[[code/module-status]] · [[code/backend-state]] · [[architecture/phase-plan]]
