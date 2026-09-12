# Biz1core product workflow audit — 12 September 2026

Does the product do what the business model says it does?

This supersedes `PRODUCT-WORKFLOW-AUDIT-2026-09-11.md`, which was written before
the five phases of work that followed it. That document remains as the record of
what was true then; this one is what is true now.

## 1. Executive summary

**The workflow is implemented end to end.** A software owner creates a business
from a separate control plane, the owner signs in to the business application
with a one-time password, gets everything their plan sells, creates staff with
roles, and cannot see or be seen by any other business. Subscription state is
enforced by the server on every write.

Three things are outstanding and **none of them is code**:

| Outstanding | Why it is not done | Who can do it |
|---|---|---|
| Console hostname | No domain chosen | The software owner |
| Email delivery | No mail provider chosen | The software owner |
| Point-in-time recovery activation | Needs the production server | The software owner |

Two capability gaps were found in this audit and closed the same day: the
control plane could not list a business's staff, and could not change what a
plan includes.

## 2. Architecture as built

One Go API, one Next application, one PostgreSQL database. The two audiences are
separated by **hostname**, not by a second build:

```
https://app.example.com       the business application
https://console.example.com   the platform control plane
```

Both tiers enforce a half of that split. The web tier decides which pages a
hostname serves (`web-next/src/proxy.ts`); the API decides which routes
(`backend/internal/api/host_policy.go`). A split enforced only at the edge is one
a direct API call walks past.

Isolation is row-level security, FORCED on 181 of 190 tables, with the platform
plane admitted table by table in migration 0006 and nowhere else.

## 3. Super Admin workflow

| Capability | Status | Where |
|---|---|---|
| Separate panel and URL | Built, **off by default** | `RAWSYST_CONSOLE_HOST` |
| Create business accounts | Fully implemented and verified | `POST /platform/tenants` |
| View all businesses | Fully implemented and verified | `GET /platform/tenants` |
| View owner information | Fully implemented and verified | owner name and address on the list |
| Activate, suspend, disable | Fully implemented and verified | `PUT /platform/tenants/{id}/standing` |
| Subscriptions and status | Fully implemented and verified | `GET`/`PUT .../subscription` |
| Start and expiry dates | Fully implemented and verified | `billing.ResolvePlanDates` |
| Payment and billing | Fully implemented and verified | `.../invoices`, `/platform/invoices/{id}/settle` |
| Per-client limits | Fully implemented and verified | `PUT .../limits` |
| **View staff of a business** | **Added in this audit** | `GET /platform/tenants/{id}/users` |
| Reset owner access | Fully implemented and verified | `POST /platform/users/{id}/reset-password` |
| Platform statistics | Fully implemented and verified | `GET /platform/health` |
| **Manage plans and features** | **Added in this audit** | `GET`/`PUT /platform/plans/...` |
| Audit log | Fully implemented and verified | `GET /platform/audit` |
| Platform settings | Fully implemented and verified | maintenance, sub-processors, regulatory registry |
| Impersonation | **Deliberately absent** | see below |

**Impersonation** is not built. The business model says the Super Admin must not
operate as a business owner unless there is an explicitly designed and audited
mechanism. There is none, and the platform plane cannot read business data at
all, so the requirement is met by construction rather than by restraint.

## 4. Business owner onboarding

Verified end to end against a live server, not only in tests.

Creating a business writes the tenant, its ceilings, the owner account, the
Owner role, the onboarding record and the subscription **in one transaction**. A
refused request leaves nothing behind, and a test asserts no orphan rows.

The owner receives a one-time password shown to the operator once. It is never
stored in readable form, never queued, and never written to the audit trail;
tests assert all three.

**Email is the honest gap.** The mailer seam is complete with three
implementations, and none of them pretends: development logs the message,
anything else refuses the job so the failure is visible in the failed-jobs view.
The API reports one of four words and **none of them is "sent"**. Until a
provider is chosen, the operator hands the credential over themselves.

## 5. Business owner feature access

Fully implemented: dashboard, POS, billing, sales, orders, products, categories,
inventory, stock adjustments, purchases, suppliers, customers, balances,
payments, expenses, reports, barcode generation and printing, invoice and
receipt printing, pricing, discounts, taxes, returns, exchanges, settings,
document customisation, logo, profile, staff, roles, subscription, backups.

The chains are tested rather than assumed: concurrent sales cannot oversell the
last unit, stock leaves in expiry order, a sale records who made it, a later tax
schedule does not change an invoice already issued, and a generated barcode is
readable.

## 6. Employee and role-based access

Fully implemented and verified. Every permission-guarded route is walked by a
signed-in cashier who lacks its permission and must answer 403. Separately,
every mutating route is checked against being gated on a reading permission;
five are, each deliberate and recorded, and one defect found that way was fixed
(asking for leave meant asking for anyone's leave).

A disabled employee stops working immediately rather than at token expiry, on
both the access-token and refresh paths.

## 7. Multi-tenant isolation

Fully implemented and verified, by enumeration rather than by a list. Every
route carrying an id placeholder is called by the owner of one tenant using ids
that really exist in another. A random id proves a route can say "not found";
only a real one proves it cannot say "here it is".

## 8. Subscription enforcement

Fully implemented and verified. Expiry is computed from the period end against
the calendar on every read, so a subscription lapses at midnight with no job
running. Five states are read-only rather than locked out, because a business
that has stopped paying is exactly the one entitled to export its data.

## 9. Branding

Fully implemented and verified, including that one business's logo cannot appear
in another's documents.

## 10. Mobile-first

**Implemented but not fully verified.** The evidence is structural and strong:
the till stacks on phones and goes side-by-side from `lg`, tables hide secondary
columns below `md` inside a horizontal-scroll container, the sidebar becomes a
drawer with body-scroll lock, and the codebase contains exactly one fixed pixel
width, behind a desktop breakpoint.

Nobody has driven the POS on a real phone at 320px. That is the remaining work,
and it is measurement rather than construction.

## 11. Backup and disaster recovery

Unchanged and honest:

| | |
|---|---|
| Code complete | Yes |
| Local drill, including recovery | Yes |
| Physical base backup download | Yes |
| Production activation | **Pending** |
| Production archiving | **Not active** |
| Production recovery verification | **Pending** |
| Production RTO | **Unknown until measured on the real server** |
| Production RPO | **Not confirmed until archiving is live** |

Local MinIO testing is not production readiness and is not presented as such.

## 12. Security posture

No known privilege-escalation path. Verified: the platform plane is 404 to every
business account including an owner holding every permission; a contradictory
token is refused; no request field can grant platform authority; the audit trail
cannot be edited or deleted; the platform cannot read business data.

Known, documented limitations: revocation is bounded by a five-second grants
cache, subscription standing by a five-second cache of its own, and a `Host`
header is not proof of anything — the split reduces surface and the 404 is what
refuses people.

## 13. Missing or incomplete

1. Email delivery — needs a provider.
2. Mobile verification on a real device.
3. Impersonation — deliberately absent.

## 14. Production blockers

1. Console hostname, DNS record and certificate.
2. Point-in-time recovery activation on the VPS, which needs two restarts.

## 15. Verdict

**Biz1core is complete for this business model as software.** What remains is a
domain, a mail provider, and an afternoon on the production server — three
decisions and one maintenance window, none of which is development work.
