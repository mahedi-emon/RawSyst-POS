# Separating the Super Admin panel — Phase 1 report

Inspection only. No code has been changed and nothing has been deployed.

The question this answers: what exists today, what can be reused, and how a
separate Super Admin application is introduced **without breaking the business
app or weakening what already works**.

---

## 1. Does a Super Admin panel already exist?

**Yes, and it is a true platform-level panel — not a business-owner admin page.**

This matters because the brief warns against assuming a route named "admin" is
Super Admin. It is not an assumption here. The distinction is enforced in three
independent places:

**Identity.** A Super Admin is a row in `app_user` with `tenant_id IS NULL`.
There is no "super admin" flag a tenant user could acquire, and no role that
grants it.

**Token.** `identity/token.go:141`

```go
// A token claiming both Super Admin and a tenant is contradictory: the
// platform plane belongs to no tenant. Refuse rather than guess.
if a.IsSuperAdmin && a.TenantID != uuid.Nil { ...refuse... }
if !a.IsSuperAdmin && a.TenantID == uuid.Nil { ...refuse... }
```

The claim is set at login from the database (`service.go:289`), carried in a
signed JWT, and a contradictory token is refused rather than interpreted.

**Direction of travel is blocked both ways.** `RequireSuperAdmin` answers **404**
to a tenant user (`identity/middleware.go:137`). And `Require` refuses a Super
Admin on tenant routes: *"Platform administrators cannot access tenant business
data."* A Super Admin cannot accidentally operate as a business owner, which is
one of the brief's explicit requirements and is already satisfied.

So the requirement *"a normal business user must not become Super Admin by
changing a URL, body, role field, or token"* is **already structurally
impossible**, not merely checked.

What the existing panel is missing is **separation**, not authorization.

---

## 2. What exists today, by the Phase 1 checklist

| Item | What is there |
|---|---|
| Super Admin panel | `web-next/src/app/(platform)/platform/*` — 20+ screens |
| Is it platform-level? | Yes. `tenant_id IS NULL` identity, `/api/v1/platform/*` API |
| Auth routes | 14 under `/api/v1/auth/*`: login, logout, refresh, me, MFA (begin/complete/disable/recovery-codes), forgot-password, reset-password, change-password, sessions |
| Authorization middleware | `Authenticate` → `Require(permission)` / `RequireSuperAdmin` / `frozen`, plus `requireFeature` for plan gating |
| User roles | 110 permissions, system + custom roles (migration 0101). Roles are tenant-scoped (`r.tenant_id IS NULL OR r.tenant_id = current_tenant_id()`) |
| Tenant tables | `tenant` (status enum active/suspended/deactivated, plan_tier, market, data_region), `tenant_limit`, `tenant_feature`, `onboarding_progress` |
| Subscription tables | `subscription` (tier, status trialing/active/past_due/suspended/cancelled, current_period_end, trial_ends_on, grace_days), `plan_feature`, `plan_tier_default`, `subscription_invoice` |
| Business-owner link | `app_user.tenant_id` → `tenant.id`; Owner role created per tenant at provisioning |
| Admin routes | ~60 under `/api/v1/platform/*` — tenants, subscription, features, limits, invoices, dunning, operators, support, jobs, health, regulatory, backups, PITR, maintenance |
| Frontend route groups | `(auth)`, `(business)`, `(platform)`, `(portal)`, `(pos)` — each with its own layout except `(auth)` |
| Navigation | `BUSINESS_NAV` and `PLATFORM_NAV` are **already separate constants** (`lib/nav/navigation.ts:86` and `:932`) |
| Session behaviour | Access token **in memory**; refresh token in an **HttpOnly, SameSite=Strict** cookie; CSRF cookie echoed in a header; `/api/v1/*` **rewritten same-origin** by `next.config.mjs` so no CORS exists |
| Audit log | `audit_log` (migration 0003) — actor, action, entity, before/after, IP, immutable, `tenant_id` NULL for platform actions. `s.audit.Platform(...)` is used by every platform write |
| Brute force | nginx `limit_req zone=auth rate=1r/s burst=5` on login/forgot/reset; plus per-account `failed_attempts` and `locked_until` |
| Invitation / email | `app_user.status = 'invited'` + `must_change_password` exist and work. **Delivery does not.** `jobs.Mailer` has only `LogMailer` and `RefusingMailer`; production selects the refusing one |

---

## 3. The one finding that shapes the design

**The session design is better than I expected, and it is what makes real
separation cheap.**

```
access token   → in memory only, never persisted
refresh token  → HttpOnly cookie, SameSite=Strict
CSRF token     → readable cookie, echoed as a header
/api/v1/*      → rewritten by Next to the Go service, so the browser
                 believes the API is this origin. No CORS anywhere.
```

A cookie scoped to an origin is **not sent to a different origin**. So if the
admin panel is served from its own origin, an operator's refresh cookie is
structurally unable to reach the business app, and a business user's cookie is
unable to reach the admin app.

That delivers the brief's *"Super Admin sessions must be properly separated"*
requirement as a **property of the deployment**, not as code anyone has to
remember to write. It also closes SEC-4 from the audit: an XSS in the business
app could no longer reach an operator session.

The catch, and it is the reason this needs care rather than a quick move: the
same-origin rewrite is load-bearing. Moving `/platform` to another host **without**
giving it its own rewrite would force `SameSite=None` cookies and CORS, which
trades a real CSRF defence for a URL change. That would be a net loss.

---

## 4. What must be created, reused, and kept unreachable

### Reuse unchanged — do not touch

- The whole `/api/v1/platform/*` namespace. It is already a protected Super
  Admin API with 404 semantics. **It does not need renaming or rebuilding.**
- `RequireSuperAdmin`, the token invariant, the `tenant_id IS NULL` identity.
- `audit_log` and `audit.Platform`.
- `PLATFORM_NAV`, already separate from `BUSINESS_NAV`.
- The auth routes. A Super Admin signs in through the same `/api/v1/auth/login`
  and gets a super-admin token because their row has no tenant. **A separate
  login endpoint would be a second authentication path to keep correct, and
  a second place to get MFA, lockout and rate limiting wrong.** Separate login
  *page*, shared login *endpoint*.
- nginx rate limiting and per-account lockout.

### Create

1. **A second Next application** — `admin-next/`, a sibling of `web-next/`,
   serving only the platform screens, with its own `next.config.mjs` rewriting
   `/api/v1/*` to the same Go API. Own origin, own cookie scope, own layout, own
   login page, own navigation.
2. **A compose service and an nginx server block** for it, on a separate host
   or port.
3. **Shared UI extraction.** `web-next` and `admin-next` both need the panel,
   table, field, button and i18n primitives. Today they live in
   `web-next/src/components/ui`. They move to the existing `shared/` workspace,
   or `admin-next` imports them via a path alias.
4. **A Super Admin dashboard** — the platform screens exist individually; there
   is no single landing view.
5. Optionally, an `X-RawSyst-Plane` style check so the API can refuse a
   super-admin token presented to the business origin and vice versa. Belt and
   braces on top of cookie scoping.

### Must remain unreachable to business users

- Every `/api/v1/platform/*` route — already 404.
- The admin origin itself — nginx should additionally allow-list it by IP or
  VPN. **Not as the security mechanism**, but because the brief asks for defence
  in depth and it costs five lines.
- `PLATFORM_NAV` must never be rendered by the business app. It currently is not,
  and after the split it will not even be in that bundle.

---

## 5. Two options, and my recommendation

**Option A — separate Next app on its own origin.** True session separation,
true bundle separation, closes SEC-4. Costs: a second build and deploy, and the
shared-UI extraction, which is the real work. Roughly a day or two.

**Option B — keep one app, restrict `/platform` at nginx.** An hour. Gets network
restriction and nothing else: same origin, same cookie, same bundle. It does not
satisfy *"separate URL"* or *"sessions separated"* as the brief means them.

**I recommend Option A**, because it is what the brief actually asks for and
because the session design already rewards it. I recommend doing **Option B
first, today**, as a stopgap while A is built.

---

## 6. Files and modules that will change

**New:** `admin-next/` (app, layout, login page, dashboard, platform screens
moved from `web-next`), `deploy/nginx` server block, a compose service.

**Moved:** `web-next/src/app/(platform)/**` → `admin-next`;
`web-next/src/lib/platform/**` → `admin-next`;
`web-next/src/components/ui/**` → shared.

**Edited:** `web-next/src/lib/nav/navigation.ts` (drop `PLATFORM_NAV`),
`web-next/src/components/auth/guard.tsx` (the `platform` workspace case),
`docker-compose*.yml`, `deploy/nginx/nginx.conf`, `Makefile`,
`web-next/scripts/reachability.mjs` — **this one matters**: it currently measures
which routes the back office reaches, and after the split it must measure both
apps or it will report every platform route as unreached.

**Untouched:** the entire Go backend. This is a frontend and deployment change.
That is the strongest argument that the current architecture is sound — the
separation the brief asks for does not require rewriting a single authorization
check.

---

## 7. What I need you to decide before Phase 2

1. **Option A, or B first then A?** My recommendation is B today, A next.
2. **Where does the admin app live** — a subdomain (`admin.yourdomain`), a
   separate port, or a path on a different host? A subdomain is cleanest for
   cookie scoping.
3. **Shared UI**: move the primitives into `shared/`, or duplicate a small set
   into `admin-next`? Moving is correct and touches more files.
4. **Do you want the IP or VPN allow-list** on the admin origin?

One caution I would be wrong not to state. The audit's two critical findings —
a suspended business keeps working, and an expired subscription grants
everything — are **live today** and are unaffected by this reordering. Phase 5
is the right place to fix them properly, and that is your call to make. But
until then the commercial controls do not work, and moving the panel to a new
URL does not change that.
