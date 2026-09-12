# Product workflow audit — 2026-09-11

Against the business model as the owner stated it: a multi-tenant SaaS POS where
the software owner provisions businesses from a Super Admin panel, and each
business runs isolated with its own staff, branding and subscription.

## How much of this audit is evidence

Stated plainly, because an audit that overstates its own coverage is worse than
none.

**Traced end to end, with code read:** authentication, the middleware chain,
permission enforcement, super-admin separation, tenant isolation mechanics,
subscription entitlement, suspension, tenant creation, the mail path, and the
backup and point-in-time recovery subsystem.

**Verified by the existing test suite passing**, not by reading each one: the
cross-tenant walk, the access matrix, and the module-level suites. 533 routes,
110 permissions, 190 tables with row-level security forced on 181.

**Sampled, not exhaustively traced:** the roughly forty business features in
section 5. I confirmed routes, permissions and tests exist for each and read
several in full. I did **not** click through all forty workflows in a browser.
Where that matters, it is said in the row.

**Not audited at all:** actual rendering on a physical phone, printer hardware,
and anything that needs the production server.

The two Serena memories describing module status (`code/module-status`,
`code/pillars-status`) are **stale** — they describe migrations 0001–0021 and a
product with no HTTP endpoints. The repository is at migration 0136. They should
not be used to judge completeness and were not used here.

---

## 1. Executive summary

The platform is substantially built and the parts that are hardest to retrofit —
tenant isolation, permission enforcement, the invoice chain, double-entry
posting — are done properly and defended by tests.

**It is not complete against the stated business model.** Three things block it,
and one of them is a live security hole.

| | Finding |
|---|---|
| **Critical** | Suspending a business does nothing. `tenant.status` is written by dunning and **read by nothing**. A suspended or deactivated business keeps signing in and keeps using every API. Two separate code comments assert the sign-in path refuses it. It does not. |
| **Critical** | An **expired subscription does not restrict anything**. The entitlement check reads the plan tier and ignores subscription status and period end. |
| **Major** | **No mail is ever sent.** The `Mailer` interface has two implementations: one logs, one refuses. There is no SMTP or provider transport. Invitations and password resets are not delivered. |

Beyond those: the Super Admin panel is not on a separate URL, there is no route
to suspend a business by hand, and there is no impersonation or support-access
mechanism.

**Verdict: not production-ready for the stated model.** See section 17.

---

## 2. Actual current architecture

```
web-next (Next.js, :3000)          backend (Go, :8080)         PostgreSQL 17
├── (auth)     sign-in             /api/v1/...                 190 tables
├── (business) the POS product     ├── AccessPublic            181 RLS FORCED
├── (platform) the admin panel     ├── AccessAuthenticated     182 policies
├── (portal)   customer/supplier   ├── AccessPermission ─ 110 permissions
└── (pos)      till                └── AccessSuperAdmin ─ 404 to everyone else
```

One Next application serves the business product and the admin panel. `(platform)`
is a Next route group, so the admin panel is at `/platform/*` on the **same
origin and port** as the business app. One nginx server block proxies both.

Every route declares its access class in one table
(`backend/internal/api/router.go`), and `TestEveryRouteDeclaresItsAccess` fails
the build if one does not. That is a good arrangement and is why this audit could
be systematic.

Tenant isolation is enforced in the database, not in Go:
`db.Pool.TxAsTenant` sets `app.tenant_id`, `TxAsPlatform` sets
`app.platform_admin`, and row-level security is **forced** (which applies to the
table owner too). `TestConnectionCannotBypassRowLevelSecurity` refuses a
superuser or `BYPASSRLS` connection by name.

---

## 3. Super Admin workflow status

| Requirement | Status | Evidence |
|---|---|---|
| Separate URL from the business app | **Partially implemented** | `/platform/*` on the same origin. `web-next/src/app/(platform)/` |
| Not in public navigation or sitemap | **Fully implemented** | Guarded by `RequireWorkspace workspace="platform"`; API answers 404 |
| Server-side authorization | **Fully implemented** | `identity/middleware.go:137` `RequireSuperAdmin` returns **404 not 403**, deliberately |
| Super Admin cannot act as a business | **Fully implemented** | `Require` refuses `IsSuperAdmin` with "Platform administrators cannot access tenant business data"; `TestSuperAdminIsRefusedOnTenantRoutes` |
| Create business accounts | **Fully implemented** | `POST /api/v1/platform/tenants` |
| View all businesses / owners | **Fully implemented** | `GET /api/v1/platform/tenants` |
| **Activate / suspend / disable a business** | **Missing** | No route. Only `/platform/operators/{id}/status`, which is for platform staff |
| Manage subscriptions, plans, dates | **Implemented** | `PUT /platform/tenants/{id}/subscription`, `/features`, `/limits` |
| Payment and billing information | **Implemented** | `/platform/tenants/{id}/invoices`, `/platform/invoices/{id}/settle`, `/platform/dunning` |
| Users and employees under each business | **Missing** | No platform route lists a tenant's staff |
| Reset business owner access | **Implemented** | `POST /platform/users/{userID}/reset-password` |
| Platform statistics | **Implemented** | `GET /platform/health` |
| Audit logs | **Implemented** | `audit.Platform(...)`, written by every platform write |
| **Impersonation / support access** | **Missing** | No mechanism anywhere. Grep for "impersonat" returns nothing |

The absence of impersonation is **not** a vulnerability — the super admin is
firmly blocked from tenant data, which is the safer default. It is a missing
capability: there is currently no auditable way to help a customer who reports a
problem inside their own books.

---

## 4. Business owner onboarding status

`POST /api/v1/platform/tenants` → `provisioning.CreateTenant`
(`backend/internal/provisioning/service.go:169`).

| Step | Status | Note |
|---|---|---|
| Tenant created | **Fully implemented** | `INSERT INTO tenant` |
| Limits from plan tier | **Fully implemented** | `tenant_limit` seeded from `plan_tier_default` |
| Owner user created and linked | **Fully implemented** | `app_user` with `must_change_password` |
| Owner role created in tenant | **Fully implemented** | |
| Onboarding wizard row | **Fully implemented** | `onboarding_progress` |
| Temporary password | **Implemented** | Returned once in the API response |
| **Subscription created at the same time** | **Missing** | Separate call to `PUT /platform/tenants/{id}/subscription`. A tenant created and not followed up has **no subscription row** |
| **Invitation email** | **Missing** | See section 12. Nothing is sent |
| Business owner reaches the right dashboard | **Fully implemented** | `RequireWorkspace` routing |
| Owner sees only their own data | **Fully implemented** | RLS + `TestNoRouteHandsOverAnotherTenantsRecord` |
| Plan restrictions enforced server-side | **Partially implemented** | Feature gate yes; expiry and suspension **no** |
| **What happens when a subscription expires** | **Missing** | Nothing. Access continues |
| **What happens when a business is suspended** | **Missing** | Nothing. Access continues |
| **Can a disabled business still call APIs** | **Yes — this is the hole** | See section 12 |
| Can an owner reach Super Admin routes | **No** | 404 |
| Can one owner read another's data by changing an ID | **No** | Systematically tested |

---

## 5. Business owner feature access status

Every module below has routes, a declared permission, and at least one
integration test; the backend suite passes against a database rebuilt from all
136 migrations. **Status is "implemented but not fully verified" for the ones I
did not personally click through.**

Fully implemented and verified by tests I read or ran: products and categories,
inventory and stock movements, costing (FIFO and weighted average, with a
tie-out test), sales and returns with the rounding-remainder rule, purchasing
and suppliers, customers and balances, payments, expenses, accounting and
double-entry posting, the ZATCA invoice chain, reports, devices and offline
sync, documents, roles and permissions, backups.

Implemented, tests exist, not manually exercised in this audit: POS counter
flow, barcode generation and printing, thermal printing, invoice and receipt
printing, discounts, taxes across SA/BD/US, dashboards.

**Known incomplete, from the repository's own records:** the ZATCA
`DocumentHasher` is deliberately not implemented — it is blocked on the
`SA.ZATCA.QR_TLV_FIELDS` regulatory value being verified against the primary
standard, and `freshcheck` reports one capability still awaiting a legal figure
(`SA.EOSB.ENTITLEMENT`). Both are honest blocks, correctly refusing by name at
the point of use rather than guessing.

The end-to-end chain the owner asked about — create a product, sell it in POS,
stock decrements, invoice produced, payment updates the customer balance,
reports reflect it — **was proved end to end in a previous session's
application-level drill** (29 checks against a restored database, all passing).
That is evidence, and it is not the same as me re-running it today.

---

## 6. Employee and role-permission status

**Fully implemented**, and this is one of the stronger parts of the system.

- 110 permissions, 103 of them gating a route, enforced by
  `Middleware.Require` in `identity/middleware.go`.
- Custom roles: migration `0101_mfa_custom_roles_and_sessions.sql`.
- Permissions resolved **per request** rather than trusted from the token.
- Every permission carries a sentence saying what it lets somebody do
  (migration `0130`).
- Device-bound tokens re-check the device is active on every request.
- Suspending a person revokes their sessions —
  `identity/people.go:683` `RevokeAllForUser(... "account suspended")`. This is
  the thing that is correctly done for *users* and missing for *tenants*.
- An owner cannot suspend their own account.
- `TestEveryRouteDeclaresItsAccess` and the access matrix test walk every route.

One gap worth naming: I did not find a test proving an employee cannot edit
**their own** role assignment. The permission `people.roles.assign` gates it, so
an employee without it is refused — but "cannot escalate by assigning yourself a
role you already have permission to assign" is a subtler case and is untested.

---

## 7. Multi-tenant isolation audit

**Fully implemented and verified.** This is the best-defended part of the system.

- Row-level security **FORCED** on 181 of 190 tables, 182 policies. Forced
  matters: it applies to the table owner, so the application's own role cannot
  bypass it.
- `TestConnectionCannotBypassRowLevelSecurity` refuses a superuser or
  `BYPASSRLS` connection and names the cause.
- `TestPlatformAdminHasNoBusinessDataAccess` requires any new table granting
  platform access to be justified in an allowlist.
- `cross_tenant_walk_test.go` → `TestNoRouteHandsOverAnotherTenantsRecord`
  systematically attempts another tenant's record on every route.
- Per-module isolation tests: catalogue, batches, dashboards, devices,
  commission, documents, FX, purchasing, drill-downs.
- The backup role has `BYPASSRLS` and the application role explicitly must not;
  `biz1core backup role` refuses to continue if the application role has it.

The one caveat is structural rather than a finding: isolation depends on every
query going through `TxAsTenant`. A future handler using `pool.Raw()` would
bypass it. That pattern has bitten this codebase before — `Chain.Verify` once
used `pool.Raw()` and reported every chain intact while seeing nothing. There is
no lint rule preventing it.

---

## 8. Subscription enforcement audit

**Partially implemented, with a hole in the middle.**

What works: `requireFeature` (`backend/internal/api/entitlement.go:36`) wraps
each route belonging to a sellable module, resolves the tenant's entitlement
server-side, and answers `402 feature_not_in_plan`. It is wrapped **inside** the
auth middleware so an unauthenticated caller gets 401 rather than learning what
a plan contains. Per-tenant overrides with their own expiry are supported. Plan
limits (users, products, branches) are enforced in `provisioning` and `identity`
with `CodeLimitReached`.

What does not work — `billing.Allows` (`billing.go:157`):

```sql
SELECT coalesce(
  (SELECT tf.enabled FROM tenant_feature tf WHERE ... expires_on >= current_date),
  (SELECT pf.included FROM plan_feature pf WHERE pf.tier = coalesce(s.tier, t.plan_tier)),
  false)
FROM tenant t LEFT JOIN subscription s ON s.tenant_id = t.id
```

It reads `s.tier`. It never reads `s.status` (`trialing`, `active`, `past_due`,
`suspended`, `cancelled`) and never reads `s.current_period_end`. **An expired,
cancelled or suspended subscription still grants every feature of its tier.**

Grace periods and dunning are implemented and do move `tenant.status` — which,
per section 12, nothing reads.

---

## 9. Branding and customization audit

**Implemented but not fully verified.** Per-company settings exist including
logo (`company_logo`, migration 0054), stationery and receipt configuration
(`/api/v1/pos/stationery`, described in the router as "device-resolved so a till
cannot print another company's stationery"), tax and invoice settings, and
currency and market settings per tenant.

Files are stored as `bytea` columns inside PostgreSQL rather than in object
storage, so they inherit row-level security automatically — file isolation is
the same mechanism as row isolation, which is a genuinely good property and is
why `backup verify` can claim to check everything.

I did not visually confirm a logo rendering on a printed invoice, and I did not
test that Business A's logo cannot appear on Business B's document beyond
relying on RLS.

---

## 10. Mobile-first audit

**Implemented but not verified.** 114 of 144 page components carry responsive
Tailwind breakpoints; the shared table primitive has `overflow-x-auto` with
`overscroll-x-contain`; there is a documented design system with RTL and Bangla
support.

I have **not** tested actual usability at 320px, touch target sizes, drawer and
modal behaviour, or the POS counter on a phone. The audit brief explicitly says
not to mark this complete because media queries exist, and I am not marking it
complete. This needs a browser and a device.

---

## 11. Backup and disaster recovery status

Unchanged from the previous session and restated honestly:

| | |
|---|---|
| Logical dump backup | **Fully implemented and verified** |
| Backup download / offline restore | **Fully implemented and verified** |
| Physical base backup | **Fully implemented and verified** (locally) |
| Base backup download | **Fully implemented and verified** |
| WAL archiving | **Code complete, locally verified** |
| Point-in-time recovery | **Code complete, locally verified** |
| Retention | **Fully implemented and verified** |
| Recovery verification | **Fully implemented** (two drills) |
| **Production activation** | **Blocked by production configuration** — pending |
| **Production archiving active** | **No** |
| **Production base backup** | **No** |
| **Production recovery verified** | **No** |
| **Production RTO** | **Unknown until measured on the real server** |
| **Production RPO** | **Not confirmed until archiving is active on production** |

Local MinIO and container testing is not production readiness, and
`deploy/server/PITR-ACTIVATION.md` says so at the top.

---

## 12. Security vulnerabilities and privilege-escalation risks

### SEC-1 — Suspending a business has no effect (critical)

**What:** `billing.go:927` runs `UPDATE tenant SET status = 'suspended'` when a
client passes their grace period. Nothing reads `tenant.status`. A grep across
the whole backend finds writes and no gating read.

The sign-in path checks **`app_user.status`**, not `tenant.status`:

```go
// identity/service.go:864 — the candidate query
SELECT u.id, u.tenant_id, coalesce(t.name, ''), u.password_hash,
       u.status, ...
FROM app_user u LEFT JOIN tenant t ON t.id = u.tenant_id
```

`tenant` is joined for the **name only**.

**Why it matters:** a business that has not paid, or that you have deliberately
disabled, keeps trading. Every user of that tenant signs in normally and every
API works. This is the commercial control of the SaaS business and it is absent.

**It is worse than a simple omission** because two comments assert it is
handled — `billing.go:27` ("the sign-in path already refuses a tenant that is
not active") and migration `0097` line 123 ("`tenant.status`, which is what the
sign-in path reads"). Anyone reading the code would conclude the control exists.

**Where to fix:** `identity/service.go` (refuse at sign-in) **and** a middleware
in the request path, because refusing only at sign-in leaves existing access
tokens working until they expire.

### SEC-2 — Expired subscription grants full access (critical)

**What:** `billing.Allows` ignores `subscription.status` and
`subscription.current_period_end`. See section 8.

**Why it matters:** the same commercial control, by a second route. Even after
SEC-1 is fixed, a subscription that lapses without dunning running leaves the
tenant fully entitled.

**Where to fix:** `backend/internal/billing/billing.go:157`.

### SEC-3 — No mail transport (major, availability and security)

**What:** `jobs.Mailer` has `LogMailer` (logs the message body at WARN) and
`RefusingMailer` (fails). `worker.go:147` selects `RefusingMailer` in production.
There is no SMTP or provider implementation.

**Why it matters:** password reset and account recovery cannot complete. Every
locked-out shopkeeper becomes a manual platform-operator task. In development
`LogMailer` writes reset tokens into the log in plaintext, which is fine for
development and must never reach production — it currently cannot, which is
correct.

### SEC-4 — Shared origin between admin panel and business app (moderate)

**What:** `/platform/*` and the business app share an origin, so they share
cookie scope. A cross-site scripting flaw anywhere in the business app could
reach a platform operator's session.

**Why it matters:** defence in depth for the highest-privilege account in the
system. The 404 guard is good and is not a substitute for origin isolation.

### SEC-5 — No lint rule preventing `pool.Raw()` in a tenant path (low)

Isolation depends on every query using `TxAsTenant`. Nothing stops a future
handler bypassing it, and this codebase has had exactly that bug before.

### Not vulnerabilities, verified

Cross-tenant access, super admin reading tenant data, employees reaching
platform APIs, permission bypass by direct API call, IDOR by changing an ID,
and disabled-user sessions surviving — all correctly refused and tested.

---

## 13. Missing or incomplete workflows

1. Suspend / activate / disable a business — no route, no enforcement.
2. Subscription status and expiry enforcement.
3. Mail delivery.
4. Subscription creation as part of tenant creation.
5. Platform view of a tenant's users and employees.
6. Impersonation / audited support access.
7. Separate origin for the admin panel.
8. Mobile usability verification.
9. Production PITR activation.

---

## 14. Production activation blockers

| Blocker | Needs production access |
|---|---|
| SEC-1 suspension enforcement | No — local |
| SEC-2 subscription expiry | No — local |
| SEC-3 mail provider | No to implement, yes to configure |
| Business suspend route | No — local |
| PITR activation | **Yes** |
| RTO / RPO measurement | **Yes** |
| Mobile verification | No — needs a browser |

---

## 15. Exact implementation tasks

**Tranche A — the commercial controls (critical, all local)**

1. `identity/service.go` — select `t.status` in `candidatesFor`, refuse
   non-active tenants at sign-in with a distinct message.
2. New middleware in `identity/middleware.go`, wired in `router.go` for
   `AccessAuthenticated` and `AccessPermission` — refuse a request whose tenant
   is not active, so live tokens stop working immediately. Must exempt the
   platform plane and the maintenance and sign-out routes.
3. `billing/billing.go:157` — add `s.status IN ('trialing','active')` and
   `s.current_period_end >= current_date` to `Allows`, with the grace period
   respected.
4. `POST /api/v1/platform/tenants/{id}/status` — suspend, activate, deactivate,
   audited, with a confirmation.
5. Correct the two false comments in `billing.go` and migration `0097`.

**Tranche B — onboarding (local)**

6. An SMTP `Mailer` implementation behind existing configuration.
7. Create the subscription inside `CreateTenant` in the same transaction.
8. `GET /api/v1/platform/tenants/{id}/users`.

**Tranche C — hardening**

9. Audited impersonation with a time-boxed, logged support session.
10. nginx restriction or a separate origin for `/platform`.
11. A lint rule or test forbidding `pool.Raw()` outside the allowlist.

**Tranche D — verification**

12. Mobile audit at 320px across the listed screens.
13. PITR production activation.

---

## 16. Missing test cases

- A suspended tenant cannot sign in.
- A suspended tenant's **existing token** is refused.
- An expired subscription is refused on a gated route.
- A tenant past its grace period is refused; inside it, permitted.
- An employee cannot assign themselves a higher role.
- A logo uploaded by Business A never renders on Business B's invoice.
- Mail: a reset for a tenant with no provider fails loudly rather than silently.
- 320px layout snapshots for POS, products, reports.

---

## 17. Final verdict

**Biz1core is not complete against the stated business workflow.**

What is complete and genuinely well built: multi-tenant isolation, role and
permission enforcement, the accounting and invoice-chain pillars, the business
feature set, and the backup and recovery subsystem.

What is missing is the **commercial layer that makes it a SaaS**: you cannot
suspend a business, an expired subscription restricts nothing, and no email ever
reaches a customer. A platform where the owner cannot cut off a non-paying
client is not finished, however good the POS is.

The two critical findings are small fixes — a join and a WHERE clause, plus a
middleware — and both are local work needing no production access. I would do
Tranche A before anything else, including before the PITR activation, because
they are cheap and they are the difference between a product and a demo.
