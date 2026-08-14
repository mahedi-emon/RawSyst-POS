# Backend — current state

Last verified 2026-08-15. **fmt · vet · build · 29 tests · wording lint — all green.**

## Environment (already set up, do not redo)
- **Go 1.26.5** at `C:\Program Files\Go` · **gopls 0.23.0** at `%USERPROFILE%\go\bin` · both on persistent user PATH
- **PostgreSQL 18.3** was already installed. Its `postgres` password was unknown, so it was reset via a temporary `trust` entry in `pg_hba.conf` (backed up to `pg_hba.conf.rawsyst-backup`, restored immediately after). **Write pg_hba with `[IO.File]::WriteAllText(..., UTF8Encoding($false))` — PowerShell 5.1's `Set-Content -Encoding utf8` adds a BOM that PostgreSQL cannot parse.**
- `postgres` password: `RawSyst_Dev_2026!pg`
- App role **`rawsyst`** — `NOSUPERUSER NOBYPASSRLS`, password `RawSyst_App_2026!dev`. **Must never be a superuser: superusers silently ignore RLS, which would disable tenant isolation with no error.**
- Databases `rawsyst_dev`, `rawsyst_test`
- `backend/.env` exists and is gitignored

## Commands
```
cd backend
go run ./cmd/migrate                     # apply migrations (idempotent)
go test -count=1 ./...                   # unit
go test -count=1 -tags=integration ./... # needs RAWSYST_DB_DSN
go run ./cmd/lintwording ..              # forbidden compliance claims
```
`-race` needs a 64-bit C toolchain absent on this Windows box; it is opt-in via `RACE=` and always on in CI.

## Built so far
| Package | Contains |
|---|---|
| `platform/config` | Env load, validates everything at once. Short/missing JWT secret is a hard failure — it is a silent auth bypass |
| `platform/errs` | Stable codes + user-facing messages; internal cause logged, never serialised |
| `platform/logging` | slog; masks secret-named fields; `Redact()` for PDPL-sensitive values |
| `platform/actor` | The authenticated caller. Drives both RLS scoping and authorization |
| `platform/db` | Pool + **the tenant-scoping contract**. `Tx` (tenant) · `TxAsPlatform` (Super Admin) · `TxAsTenant` (workers). Constraint violations translated to domain errors |
| `platform/db/migrate.go` | Embedded migrations, hash-verified, one transaction each |
| `platform/httpx` | JSON envelope, error shaping, strict decode (unknown fields rejected) |
| `identity/password.go` | argon2id, per-hash salt, constant-time compare, rehash detection |
| `identity/token.go` | HS256 JWT + opaque refresh tokens stored hashed |
| `registry` | Dated legal-value resolution. **`json.Number` throughout — no float touches a legal value** |
| `cmd/lintwording` | Separates product output (any hit fails) from docs stating the prohibition (`~~strikethrough~~` or ❌ marks a quoted phrase) |

## Migrations 0001–0006
1. Extensions, `current_tenant_id()`, `reject_always()`, `reject_delete()`, `reject_column_change()`, shared enums
2. Tenancy: tenant → company → store → device, all with RLS **ENABLE + FORCE**
3. Identity: users, sessions, roles, permissions, 4-dimension scoping, append-only audit log
4. Regulatory registry with a **GiST exclusion constraint** making overlapping date ranges unrepresentable
5. Seed: 12 role templates, 81 permissions, 21 Saudi rules — **all unverified**, 5 release-blockers
6. **Platform control plane** — added after the isolation tests proved no tenant could be created

## The bug the tests caught
Migration 0002 forced RLS everywhere, which made tenant provisioning impossible: a row with no tenant context satisfies no policy. Fixed not by weakening isolation but by modelling Super Admin as a separate plane (`is_platform_admin()`), granted **only** on tables A4 puts under Super Admin. **Business tables must never get that predicate** — `TestPlatformAdminHasNoBusinessDataAccess` fails if one does, so add new business tables freely and the guard holds.

## Next
Auth service (login/refresh/logout/lockout) → authorization middleware (QA gate M7) → HTTP server + `cmd/api` → tenant provisioning + onboarding.

## Related
[[design/index]] · [[architecture/decisions]] · [[tooling/serena-setup]]
