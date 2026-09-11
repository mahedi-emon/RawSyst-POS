# Access boundaries

What separates a platform operator from a business owner from a member of
staff, where each boundary is enforced, and what proves it.

Written during Phase 4, whose question was not "is there authorization" — there
is, and a great deal of it — but whether the boundaries hold when somebody
pushes on them from a real, signed-in account using curl rather than the
interface.

## The three levels

| Level | How the product knows | Where authority comes from |
|---|---|---|
| Platform operator | `app_user.tenant_id IS NULL`, and a signed `sa` claim on the token | The claim itself. Operators hold no permissions and need none. |
| Business owner | A tenant session holding every permission the product seeds | Role assignments in their own tenant |
| Employee | A tenant session holding a subset | Role assignments in their own tenant |

A token claiming both super admin and a tenant is refused as contradictory:
the platform plane belongs to no tenant, and guessing either way silently
widens or narrows access.

## Where each boundary is enforced

Nothing here relies on a hidden button. Every layer below refuses
independently, and the ones nearest the data refuse last.

| Boundary | Enforced by | Layer |
|---|---|---|
| Signed in at all | `Authenticate` | Middleware |
| Account still usable | `Authorizer.Resolve` → `requireAccountIsUsable` | Middleware, per request |
| Holds the permission | `Require(permission)` | Middleware |
| Platform routes | `RequireSuperAdmin`, answering **404** | Middleware |
| Super admin on tenant routes | `Require` refuses them | Middleware |
| Another tenant's rows | Row-level security, FORCED on 181 tables | Database |
| Platform operator reading business data | Migration 0006's predicate, table by table | Database |

The 404 on the platform plane is deliberate and is not obscurity standing in
for a control. A 403 would confirm the route exists, which tells somebody
probing exactly where to keep pushing; the authorization is what refuses them,
and the status code only declines to help.

## The test matrix

Every row is a test that runs in CI, not a manual check.

| Caller | Platform routes | Tenant routes they hold | Tenant routes they do not | Another tenant's records |
|---|---|---|---|---|
| Unauthenticated | 401/404 | 401 | 401 | 401 |
| Platform operator | 200 | Refused | Refused | No business data at all |
| Business owner | **404** | 200 | n/a — holds everything | 404 |
| Employee, full role | **404** | 200 | 403 | 404 |
| Employee, view-only | **404** | 200 on reads | 403 on writes | 404 |
| Disabled employee | 401 | **401** | 401 | 401 |

Which test proves which:

| Claim | Test |
|---|---|
| Every route refuses an unauthenticated caller | `TestUnauthenticatedIsRefusedEverywhere` |
| Every guarded route refuses a signed-in user without its permission | `TestEveryGuardedRouteRefusesAUserWithoutThePermission` |
| Every route naming a record refuses another tenant's real id | `TestNoRouteHandsOverAnotherTenantsRecord` |
| The same routes still work on the caller's own records | `TestTheSameRoutesStillWorkOnTheCallersOwnRecords` |
| A super admin is refused every tenant route | `TestSuperAdminIsRefusedOnTenantRoutes` |
| An owner is refused all 68 platform routes | `TestAnOwnerHoldsNothingOnThePlatformPlane` |
| An owner cannot declare themselves a super admin | `TestAnOwnerCannotDeclareThemselvesSuperAdmin` |
| A cashier cannot reach the control plane | `TestCashierCannotReachPlatformControlPlane` |
| No mutation hides behind a reading permission | `TestNoMutationHidesBehindAReadingPermission` |
| A disabled employee stops working immediately | `TestADisabledEmployeeStopsWorking` |
| A disabled platform operator stops working immediately | `TestADisabledPlatformOperatorStopsWorking` |
| An invited owner can still change their one-time password | `TestAnInvitedOwnerCanStillChangeTheirPassword` |
| Every door shuts on a dismissed employee, in order | `TestEveryDoorShutsOnADismissedEmployee` |
| The revocation window is five seconds and no more | `TestRevocationIsBoundedByTheGrantsCache` |
| Asking for leave means your own leave | `TestAskingForLeaveMeansYourOwnLeave` |
| Row-level security cannot be bypassed at the connection | `TestConnectionCannotBypassRowLevelSecurity` |
| Cross-tenant read, read-by-id and write are impossible | `TestCrossTenantReadIsImpossible` and siblings |
| A platform operator sees no business data | `TestPlatformAdminHasNoBusinessDataAccess` |
| A platform operator cannot read a tenant's roles | `TestPlatformAdminCannotSeeTenantRoles` |
| The audit trail cannot be edited or deleted | `TestAuditLogIsAppendOnly` |
| A menu item does not lead to a screen the person cannot load | `nav/gates.test.ts` |

### On cross-tenant testing

The brief for this phase listed the identifiers to try changing: tenant, user,
product, customer, order, invoice, inventory, purchase, supplier, employee,
file. `cross_tenant_walk_test.go` does not work from that list — it walks the
**route table**, calling every pattern that carries an id placeholder as the
legitimate owner of one tenant, using ids that really exist in another.

That is deliberate and is stronger than the list. A list has to be remembered;
the route added next year is not on it. Real ids matter too: a random UUID is
refused by any handler, including one with no isolation at all, so a random id
proves a route can say "not found" while only a real one proves it cannot say
"here it is".

## What Phase 4 found

### A disabled account kept working — fixed

An access token is a signed statement about the past. `TokenService.Verify`
checks the signature, the issuer, the expiry and the claim shape, and touches
no database at all. So a token minted for somebody in good standing stayed
cryptographically perfect after they were dismissed.

Disabling somebody *did* revoke their sessions, so they could not refresh and
could not sign in again. But the token already in their browser worked until it
expired — up to fifteen minutes by default. Long enough to ring up sales, move
stock, or read the books after being dismissed.

Worse for a platform operator: `Resolve` returned immediately on the super
admin claim without reading anything, so the account behind the
highest-privilege token in the product was never consulted at all.

Fixed in `Authorizer.Resolve`, which is the one thing that already reads the
database on every request. The file had already argued for why that is worth
doing — `grantsCacheTTL` says a revocation "must take effect now", which is the
reason permissions are resolved per request rather than baked into the token. A
revoked *permission* took effect in five seconds; a revoked *account* took up to
fifteen minutes. That was never a decision, it was the account simply never
being looked at.

`invited` is allowed through deliberately: somebody holding a one-time password
has to reach the change-password screen, and refusing them would make a new
business impossible to activate.

### The refresh path let them straight back in — fixed

Found by testing the sequence rather than the parts. A dismissed employee was
refused at every door above and then **refreshed their session and was issued a
brand new access token**, which undoes all of it.

`Refresh` checked `user_session.revoked_at` and nothing about the account.
Disabling somebody through the staff screen revokes their sessions, so in
practice the check caught them — but the two mechanisms are independent, and
anything that disables an account without also revoking sessions left this path
minting fresh credentials.

It now reads the account's status in the same query that loads the session, and
refuses on the same rule the sign-in path uses. That is the principle the
function already stated for scopes — "re-read on refresh rather than copied from
the old token, so narrowing somebody's scope takes effect at the next refresh" —
applied to the account rather than only to its scopes.

The lesson is in the shape of the test, not the fix: each refusal passed on its
own, and the one that mattered was only visible when they were run in the order
a real person meets them.

### A menu item nobody could load — fixed

`/pos/exchanges` was offered on `sales.exchange`, and the screen opens by
looking a receipt up and reading its returnable lines. Both of those routes are
`sales.refund`. Somebody holding only `sales.exchange` saw the link and
collected a 403 from the screen's first request.

Not a security failure — the server refused correctly, which is the point — but
the security model leaking into the interface as an error nobody can act on.
The `alsoNeeds` mechanism exists for exactly this and four items already carried
it. This was the fifth.

No seeded role is in that position, so nobody has hit it. A custom role holding
"may exchange" and not "may refund" is an ordinary thing for an owner to build.

### Five mutations behind reading permissions — reviewed, kept, pinned

`TestEveryGuardedRouteRefusesAUserWithoutThePermission` skips any route whose
permission the test cashier *holds*, which is correct — those are legitimately
allowed — but means a route that changes data while gated on a `.view`
permission is never called by it at all.

Five such routes exist. Each is deliberate and carries its reasoning in the
route table:

| Route | Why |
|---|---|
| `POST /installments/quote` | Previews a schedule, creates nothing |
| `POST /promotions/quote` | Prices a cart as it is built; nothing is redeemed until the sale is finalised |
| `POST /pos/sales/{id}/reprint` | Reprinting is looking a sale up again |
| `POST /leave` | Asking for time off is not granting it; the decision is `hr.manage`. **Now constrained to your own record** unless you hold `hr.manage` — see below |
| `POST /service-jobs` | A counter that can see repair jobs books them in; it is one conversation with the customer |

The last two genuinely create a record, and reviewing them found a defect in one.

### Asking for leave meant asking for anyone's leave — fixed

`POST /leave` took the employee from the **request body** and checked nothing.
The route's own note says "anybody who can see the directory can ask", and
asking *for yourself* is what that meant — but anybody holding `hr.view` could
file leave in anybody else's name, including the owner's.

It was never a way to take unapproved time off: `requested_by` records who
really asked, the request lands as `requested`, and it cannot become attendance
without somebody holding `hr.manage` deciding it. It was a way to put rows in
another person's record that they did not put there, and to make a manager deal
with them.

Now: without `hr.manage`, the employee named must be your own record. Somebody
with no login of their own — a cleaner paid in cash, somebody on a paper rota —
still has their leave entered by whoever keeps the records, which is what
`hr.manage` is for.

This is the case the guarded-route walk cannot reach by construction. The caller
*holds* the permission the route asks for, so the walk skips it as legitimately
allowed. The authorization that was missing was about the argument, not the
route.

`POST /service-jobs` was reviewed on the same terms and kept. It is tenant- and
company-scoped, names no other user, and creates a work item rather than a
financial record; requiring `service.manage` instead would also let counter
staff change diagnoses and fit parts, which is broader rather than safer.

## Known limitations

**Revocation is bounded by five seconds, not instant.** `Authorizer` caches
grants for `grantsCacheTTL`. A disabled account, a removed permission and a
changed role all take effect within that window rather than on the next request.
`Invalidate` and `InvalidateAll` exist to close it to zero and **nothing in the
running product calls them** — their comments claimed otherwise until Phase 4
and have been corrected. Wiring them into the role-change paths is a deliberate
improvement somebody should make on purpose.

**Both halves of disablement are now independent.** The refresh path checks the
account's status itself, and so does every authenticated request. A future code
path that disables an account without revoking its sessions is still caught by
both. `SetPersonStatus` continues to do both as well.

**Subscription state enforces nothing.** `tenant.status` is written and read by
nothing, and `billing.Allows` ignores the subscription status and period end. A
suspended business can still sign in and trade, and an expired subscription
stops nothing. This is Phase 5's work and is deliberately not started. The
platform dashboard and the billing screen both say so in plain words next to the
figures rather than letting an operator assume otherwise.

**Nav gating is a list, not a derivation.** `nav/gates.test.ts` checks pairs a
person wrote down. Deriving them by parsing every page and resolving its calls
against the route table was tried; it produced five false positives before it
produced the one real finding, because it matched the wrong page file for an
href and could not see `alsoNeeds`. The reviewed list is the artifact; the
script was scaffolding.
