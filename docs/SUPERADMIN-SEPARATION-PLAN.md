# Separating the Super Admin panel

**Phase 1 (inspection) and Phase 2 (origin separation) are done. Nothing has
been deployed.** Section 8 records what Phase 2 built and what it proved; the
rest is the Phase 1 report it was built from, unchanged.

Phase 1 was inspection only.

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
5. Optionally, an `X-Biz1core-Plane` style check so the API can refuse a
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

---

## 8. Phase 2 — what was built, and what it proved

**Implemented and verified locally. Not deployed, and inactive by default.**

### The choice, and why it differs from what Phase 1 proposed

Phase 1 recommended a second Next application. **Phase 2 did not build one**, and
the brief is why: *choose the smallest safe solution that provides real
origin-level separation*.

A second application would have meant extracting the shared UI primitives, a
second build and deploy, and reworking the reachability script — a large change
whose only advantage over what was built is separating the JavaScript bundle.

What was built instead: **one application, two origins, decided at the edge.**
The container serves the control plane on one hostname and the business
application on every other, by reading the `Host` header.

That still delivers the property the brief actually wants. **Session separation
comes from the cookie, not from the bundle.** The refresh cookie is host-only,
so an operator signing in at the console holds a session structurally unable to
reach the business hostname.

### What it does not do, stated plainly

It does not separate the JavaScript bundle. Both origins are built from one
application, so the platform route chunks exist in the deployment either way.
They are interface code; every byte of data behind them is guarded by the API's
404. If bundle separation is later judged necessary, the second application from
section 4 is still the answer and nothing here blocks it.

### Files changed

| File | Change |
|---|---|
| `web-next/src/lib/origin.ts` | **New.** The rule, as a pure function |
| `web-next/src/lib/origin.test.ts` | **New.** 11 tests |
| `web-next/src/proxy.ts` | **New.** Applies the rule per request |
| `web-next/src/app/page.tsx` | Comment only, on the platform redirect |
| `deploy/nginx/nginx.conf` | Console server block, **commented out** |
| `docker-compose.yml` | `RAWSYST_CONSOLE_HOST` passed at runtime |
| `.env.example` | The variable, with what setting it commits you to |

**No Go file changed.** The authorization model was already correct and was
reused exactly as Phase 1 said it should be.

### How it behaves

`RAWSYST_CONSOLE_HOST` unset — the default, and every existing deployment —
changes nothing. Both halves stay on one origin.

Set, verified against the running container:

| Path | business origin | console origin |
|---|---|---|
| `/` | 200 | **404** |
| `/dashboard` | 200 | **404** |
| `/platform` | **404** | 200 |
| `/platform/backups` | **404** | 200 |
| `/login` | 200 | 200 |
| `/api/v1/*` | 200 | 200 |

`/login` and `/api/v1/*` are served on both **deliberately**. The console signs
in through the same endpoint and must reach it on its own origin, or the
`SameSite=Strict` refresh cookie is never sent. That is what keeps the strict
cookie and avoids needing CORS — the trade Phase 1 warned against making.

### What the container test caught that the unit tests could not

The first cut read `request.nextUrl.hostname`. Inside a container that reports
the server's own origin regardless of how the request was addressed, so every
request looked like the business origin and the console 404'd its own pages.

The unit tests passed throughout — they are handed a hostname and cannot know
where a real one comes from. Only running it against the built image showed it.
It now reads the `Host` header, and the file says why.

A caller can therefore claim any hostname. That is acceptable and is not a hole:
a spoofed `Host` reveals the control plane's **interface** and nothing behind it,
because every `/api/v1/platform/*` call answers 404 without a platform token.
This decides which interface a hostname shows, never what anybody may do.

### nginx: prepared, not active

The console server block is written and **commented out**, per the instruction
not to activate a production restriction without approval. Turning it on needs a
DNS record, a certificate covering the name, and a decision about the optional
IP allow-list — which is the strongest cheap control available and also the one
that locks an operator out of their own platform from a hotel.

### Tests

11 new tests in `origin.test.ts`, covering both directions, the shared paths,
case and port handling, a hostname that merely contains the console name, and
the unset default. Against the running stack: the table above.

The brief's items 1–6 and 10–13 — business user or employee calling
`/api/v1/platform/*`, a contradictory token, rate limiting, audit logging — were
**already covered** by `backend/internal/api/access_test.go` and
`platform_backup_test.go`, which pass. They were verified, not rewritten.

### Still outstanding from the brief's Super Admin UI list

The panel has tenants, subscriptions, limits, features, invoices, dunning,
operators, support, audit and platform health. It still has **no single
dashboard landing view**, and **no activate/suspend/disable control** — the
latter because the underlying enforcement does not work yet, and a button that
appears to suspend a business while the business keeps trading would be worse
than no button. Both belong to Phase 3 and Phase 5.

---

## 9. Phase 3 — the control centre, and the subscription that was not there

Phase 3 asked for a Super Admin dashboard and a complete business onboarding
workflow. Most of both already existed. What did not exist was the thing
underneath them, and finding it changed what this phase was about.

### What was already built

The inspection found six of the eight objectives substantially present: the
landing page with live platform figures, the business list with server-side
search, filtering and keyset paging, the create-business form, a transactional
`CreateTenant`, one-time temporary passwords with forced change, a hashed
one-time expiring reset-token table with replay detection and a guess limit, an
honest three-way mailer seam, and audit writes inside the transaction of every
action they record.

Authorization needed no change at all. `RequireSuperAdmin` answering 404,
`Require` refusing a super admin on tenant routes, the mutual-exclusion token
invariant, forced row-level security — all intact, all still passing, and no Go
file in `identity` was touched.

### The defect this phase actually fixed

A business was created with **no `subscription` row at all**. The read path
then left-joined the absence and coalesced it:

```sql
coalesce(s.tier, t.plan_tier), coalesce(s.status, 'active'), ...
```

So `GET /platform/tenants/{id}/subscription` answered with a complete,
plausible, **active** subscription for a client who had none. Every business
ever created through the product was in that state.

The wrong counting is the smaller half. The serious half is that
`current_period_end` was NULL and coalesced to nothing, so **there was no date
for anything to compare against**. A subscription with no end cannot expire.
Phase 5's enforcement would have swept the table for subscriptions past their
period end, found none — for every client, for ever — passed every test written
against it, and enforced nothing.

Migration 0137 backfills the missing rows. It writes exactly what the coalesce
was already asserting and not one thing more, except `started_on`, which comes
from the tenant's own `created_at` rather than claiming every existing client
signed up today. `current_period_end` is left NULL, because the platform never
recorded it and inventing a date would expire somebody.

### The migration that ran and did nothing

Migration 0137 reported success and inserted zero rows.

Migrations run on an ordinary pool connection. `Pool.Migrate` sets no GUC,
because the schema changes it usually carries do not need one. Row-level
security on `tenant` is FORCED and its policy is `id = current_tenant_id() OR
is_platform_admin()`. With neither set, both halves are false for every row, so
`SELECT ... FROM tenant` read an empty table. **Inserting nothing is not an
error**, so nothing failed. `schema_migration` recorded version 137 and the
table was untouched.

It was caught by reading `tenants_without_subscription` on the dashboard
immediately afterwards and seeing a number that should have been zero. That
figure was added in the same phase for an unrelated reason and is the only
thing that would have shown this.

Migration 0138 redoes it inside the `DO` block that sets `app.platform_admin`
locally — the pattern a dozen migrations from 0042 onward already use. 0137 was
left alone: `Pool.Migrate` hashes every migration and refuses to start if one
changed after it was applied.

The regression guard is `TestATenantBackfillSetsThePlatformFlag`, which reads
the migration files: a migration that reads rows `FROM tenant` in a data
statement must set the flag. Every one of the thirty migrations that does this
already followed the rule — 0137 was the only one that did not.

It is a source check rather than a behaviour test on purpose. A migration runs
once, and on a fresh database a broken one produces the same silent nothing as a
correct one with no rows to work on. The whole-table invariant is not available
either: test fixtures insert tenants directly, never through provisioning, so a
test database legitimately holds hundreds with no subscription. "Every tenant
has one" would be a statement about fixtures, not about the product.

It was checked by adding a deliberately bad migration and watching it fail.

### The button that never worked

The platform billing screen's **Change plan** button sent `{ tier }` alone.
`SetPlan` refuses a subscription with no cycle and no price, so it returned 400
every time it was pressed, for as long as it has existed. Nothing on that screen
was ever editable. It now sends the whole plan.

### What else was added

| Gap | Now |
|---|---|
| No start or expiry control | `ResolvePlanDates` — a pure, tested resolver; start, expiry and trial settable, derived from the start rather than the clock when left blank |
| List had no owner or commercial columns | Owner name and address, subscription status, expiry with a tone, onboarding step |
| Dashboard counted machinery only | Active, expired, trialing, expiring within 30 days, businesses with no terms at all, suspended, deactivated, signups this week |
| Audit trail readable by nothing | `GET /api/v1/platform/audit`, and a recent-activity panel |
| Handover showed only a password | Plan, validity, login URL, and an honest delivery status |
| Double submit created two businesses | Refused in the transaction on name plus owner address |

### Email, stated precisely

- **Code implemented.** A new `owner_invitation` message kind, queued inside the
  creation transaction, rendered by the worker.
- **Local delivery verified.** Run against a live stack, the worker logs the
  message at WARN with the correct body, naming the login URL, the username, the
  plan and the validity.
- **Production email configuration pending.** No provider is wired. In
  development the worker logs; anywhere else it refuses the job, which retries,
  escalates and appears in failed jobs.

The API reports one of four words and **none of them is "sent"**:
`queued_for_logging`, `queued_no_provider`, `queued`, `not_configured`. Three of
the four mean the owner will be told nothing, and the handover screen says so in
a sentence rather than leaving the operator to infer it.

The message deliberately carries **no password**. The temporary credential is
shown to the operator once, on screen, and handed over by them; putting it in a
queued message would write it to the jobs table in readable form and from there
to whatever a mail provider logs. A test asserts it appears in neither the queue
nor the audit trail.

### Still deliberately missing

No activate, suspend or disable control. `tenant.status` is written and read by
nothing, and a button that appears to suspend a business while the business
keeps trading would be worse than no button. Both the dashboard and the billing
screen now say this in plain words where the figures are, rather than letting an
operator assume otherwise. It is Phase 5's work.
