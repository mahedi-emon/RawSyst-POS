# Security, RBAC and frontend pass, 2026-08-28

Follows [[audit/business-logic-2026-08-28]]. Ten defects, all fixed at the root.

## The method that found them

**Walk the route table, not a list somebody remembered.** Every targeted
isolation test in this suite is good and every one of them had to be written by
hand; the route added next year is in none of them. Two new tests walk
`Server.Routes()` itself:

- `TestNoRouteHandsOverAnotherTenantsRecord` — every `{…ID}` route called by an
  Owner of one tenant with an id that REALLY EXISTS in another. Real ids
  matter: a random UUID is refused by a handler with no isolation at all.
- `TestAConfinedUserIsRefusedEveryRouteIntoAnotherCompany` — the same for the
  company dimension, which row-level security cannot see because its predicate
  is the tenant and both companies are inside it.

Each has a counter-test (`…StillWorkOnTheCallersOwnRecords`,
`AnUnconfinedUserStillReaches…`) so a handler that refuses EVERYTHING is not
mistaken for one that isolates.

**Look at the screens with data in them.** Every earlier browser walk ran
against empty tables, and an empty list has nothing to measure. Seeding a shop
that had actually traded immediately surfaced the row-open button at 32px.

## What was wrong

- **P79 company confinement**: `logoScope` (all seven branding/template routes)
  never called `CanAccessCompany` — a confined bookkeeper could rewrite another
  company's tax-invoice footer. `catalog/products/{id}/matrix` leaked a sister
  company's variant grid, prices and stock.
- **P80 `db.Translate`**: re-wrapped domain errors as `CodeInternal`. Every
  service has the shape `TxAsTenant(... errs.New ...)` then `Translate`, so any
  guard inside a transaction became a 500. Fixed once, in Translate.
- **P81**: four record-naming routes answered 200-with-empty for a foreign
  record — the wrong answer and the right answer were the same JSON.
- **P82 fonts**: Inter and JetBrains Mono were NAMED and never LOADED. Every
  screen fell through to Segoe UI + Consolas. Now self-hosted via @fontsource
  (offline-first POS; no CDN call from every till).
- **P83**: `:lang(ar) { font-size: 1.15em }` compounds — `:lang()` is inherited
  and `em` is parent-relative, so a table cell got 1.15⁴ and the type scale
  INVERTED. Applied once at the root.
- **P84**: dates were ISO, `.num`-classed (mono, end-aligned under start-aligned
  headers) and bidi-scrambled to "Aug 2026 28" in Arabic.
- **P85**: header two-column at 390px; pressing the CURRENT nav item did nothing
  (same state value → no remount → stuck on a form).
- **P86**: `.detail__rowbtn` 32–38px on touch.
- **P87**: unknown variant → FK violation → 500 at the till.
- **P88**: the leaked `sale.revenue` rule, superseded not deleted.

## Two things worth not re-learning

**A CSS override for `.detail__rowbtn`, `.segmented__btn` or `.attention__open`
must go in `dashboard.css`, not `design-system.css`.** dashboard.css is imported
second, so an equal-specificity rule in the system loses. The system file says so
next to `.segmented__btn`; I put the row-button bump in the wrong file first and
the audit still failed.

**Deleting from `posting_rule` needs `session_replication_role = replica`, which
disables EVERY trigger on the connection** — journal immutability, the period
lock, the write-once stamp. Never do it to tidy data. Supersede with a higher
version instead: `ResolveRule` takes the highest version in force at the
transaction date.

## Checked and found correct

- Every other company-scoped route (purchasing, receivables, reports,
  settlement, devices, e-invoicing, dashboard) already checks
  `CanAccessCompany`.
- ZATCA's live validator: standard invoice valid, simplified fails only for want
  of a certificate. Run with `ZATCA_VALIDATOR=1`.
- `govulncheck` clean; `npm audit` clean after moving vite 5 → 7 and vitest
  2 → 3 across both workspaces (the five advisories were all dev-server, all
  from one vite).

## Related
[[audit/business-logic-2026-08-28]] · [[code/module-status]] · [[design/index]]
