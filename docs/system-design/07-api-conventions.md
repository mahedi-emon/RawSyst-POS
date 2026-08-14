# 07 — API Conventions

One contract, three clients: the Tauri POS, the Next.js back office, and the
PWA. Blueprint J2 is explicit that these are **two front ends of one product,
not two products**, so there is one API and one set of rules.

---

## 1. Shape

```
https://api.rawsyst.com/api/v1/<resource>
```

Versioned in the path. A POS terminal in a showroom may run an older build for
weeks — Device Management can force an update, but not instantly, and never
mid-shift. The server must therefore serve two versions during a rollout.

| Method | Use |
|---|---|
| `GET` | Read. Never changes state. |
| `POST` | Create, or a named action (`/invoices/{id}/refund`) |
| `PATCH` | Partial update |
| `PUT` | Full replace — rare; most resources are partially updated |
| `DELETE` | Only where deletion is legal. Never for invoices, journals, audit rows. |

Named actions get a POST sub-resource rather than a `PATCH` with a magic status
field. `POST /invoices/{id}/finalize` states the intent; `PATCH {"state":
"finalized"}` invites a client to invent transitions the state machine forbids.

---

## 2. Success envelope

A single resource is returned bare:

```json
{ "id": "…", "human_number": "INV-RYD-000123", "total_inclusive": "1164.0000" }
```

A collection is wrapped, because pagination metadata has to live somewhere:

```json
{
  "data": [ … ],
  "page": { "cursor": "eyJpZCI6…", "has_more": true, "limit": 50 }
}
```

**Money and quantities are JSON strings, not numbers.** `1164.0000`, not
`1164.0`. JavaScript's `number` is a float64 and cannot hold every decimal
exactly; a client that parses `0.15` and multiplies will eventually disagree
with the server's `numeric`. Sending strings makes the client's decimal library
the only thing that touches the value.

---

## 3. Error envelope

```json
{
  "error": {
    "code": "amount_limit_exceeded",
    "message": "This discount is SAR 120.00, above your limit of SAR 50.00. Ask a manager to approve it.",
    "fields": { "discount": "Exceeds your approval limit." },
    "request_id": "01JC8Z…"
  }
}
```

- `code` is stable and machine-readable. Clients branch on it. Codes never change
  meaning once released; new situations get new codes.
- `message` is written for the person reading it: what happened, why, what to do.
  Never a bare code, never a stack trace, never SQL.
- `request_id` correlates with server logs so support can find the incident.

### Codes and statuses

| Code | HTTP | Meaning |
|---|---|---|
| `invalid_input` | 400 | Malformed or failed validation |
| `unauthenticated` | 401 | No valid token |
| `forbidden` | 403 | Authenticated, not permitted |
| `amount_limit_exceeded` | 403 | Over the actor's approval ceiling |
| `not_found` | 404 | No such record **in your tenant** |
| `conflict` | 409 | Version conflict or duplicate |
| `immutable` | 409 | Finalized invoice, posted entry, audit row |
| `period_closed` | 409 | Fiscal period closed or locked |
| `plan_limit_reached` | 409 | Plan ceiling would be exceeded |
| `compliance_blocked` | 422 | Refused for a legal reason |
| `unverified_regulatory_rule` | 422 | A required legal value is unverified |
| `rate_limited` | 429 | Too many requests |
| `internal` | 500 | Our fault |
| `unavailable` | 503 | Dependency down |

**404, not 403, for another tenant's record.** A 403 confirms the record exists,
which leaks across the tenant boundary. RLS produces this naturally: the row is
simply not there.

`compliance_blocked` and `unverified_regulatory_rule` are 422 rather than 400
because the request is well-formed — it is the legal situation that refuses it.
The client should show the reason, not a validation hint.

---

## 4. Idempotency

Any state-changing request may carry:

```
Idempotency-Key: <uuid>
```

**Required** on `POST /sync/push` and on any endpoint the POS calls, because the
POS retries after network failure and must not double-post a sale.

The server stores the key with its response for 24 hours. A replay returns the
original response with `Idempotency-Replayed: true`. A replay with a *different*
body returns 409 — that is a client bug, not a retry.

This is the transport layer of the three-layer protection in
`03-sync-idempotency.md` §3.2. The other two — the `uuid` primary key and the
journal's `UNIQUE (source_type, source_id, rule_key)` — mean that even a bug
here cannot corrupt the ledger.

---

## 5. Pagination

Cursor-based, not offset.

```
GET /api/v1/invoices?limit=50&cursor=eyJpZCI6…
```

Offset pagination skips or repeats rows when the underlying set changes between
pages, and an invoice list changes constantly during trading hours. `limit`
defaults to 50, caps at 200.

---

## 6. Filtering, sorting, sparse fields

```
GET /api/v1/invoices?store_id=…&issue_date.gte=2026-08-01&issue_date.lt=2026-09-01
                    &sort=-issue_date&fields=id,human_number,total_inclusive
```

Operators are suffixed: `.gte`, `.gt`, `.lte`, `.lt`, `.ne`, `.in`, `.like`.
Sorting uses `-` for descending. Only whitelisted fields are filterable or
sortable per endpoint — an open filter surface is an index-planning problem and
an injection surface.

---

## 7. Authentication

```
Authorization: Bearer <access-token>
```

| Token | Lifetime | Carries |
|---|---|---|
| Access (JWT) | 15 min | `sub`, `tenant_id`, `company_ids`, `session_id` |
| Refresh (opaque) | 30 days, rotating | Stored hashed server-side |
| Device token | Long-lived, device-bound | `device_id`, `terminal_id`, scoped to `/sync/*` |

**Permissions are not in the token.** They are resolved server-side per request.
A permission revoked at 10:00 must not stay effective until a 15-minute token
expires; for a system handling money, that window is unacceptable.

Refresh rotates: using a refresh token invalidates it and issues a new one.
A reused token means it was stolen, so the entire session family is revoked.

---

## 8. Authorization

Every route declares its requirement. There is no "internal" exemption.

```go
r.With(auth.Require("sales.refund"), auth.AmountLimit()).
  Post("/api/v1/invoices/{id}/refund", h.Refund)
```

QA gate M7 is automated against the route table: every route is called as a
Cashier and must return 403 unless the Cashier role grants it. **A route
registered without a permission declaration fails CI**, so the gate cannot be
bypassed by forgetting rather than by deciding.

Masked fields are **absent** from the payload, not null:

```json
// Owner
{ "sku": "M-ABY-BLK-L", "price_retail": "449.0000", "cost_price": "268.5000" }
// Cashier — cost_price is not present at all
{ "sku": "M-ABY-BLK-L", "price_retail": "449.0000" }
```

Null would confirm the field exists and that the caller is blocked from it.
Absence reveals nothing, and cannot be recovered from a cached response.

---

## 9. Concurrency

Mutable resources carry a version, returned as an ETag:

```
GET  → ETag: "7"
PATCH  If-Match: "7"    → 409 if it has moved on
```

Optimistic, not locking: two managers editing the same product should not block
each other, but the second must be told rather than silently overwriting.

Immutable resources need none of this.

---

## 10. Rate limiting

```
X-RateLimit-Limit / Remaining / Reset
Retry-After            (on 429)
```

| Endpoint class | Limit |
|---|---|
| Authentication | 10 / minute / IP |
| Sync push | 60 / minute / device |
| Reads | 600 / minute / user |
| Writes | 120 / minute / user |
| Reports | 10 / minute / user |

Sync is limited per device rather than per user because a busy store drains a
large offline queue in bursts, and throttling that would delay ZATCA reporting.

---

## 11. Dates, money, language

| Field | Format | Example |
|---|---|---|
| Timestamp | RFC 3339, UTC | `2026-08-15T11:04:22Z` |
| Date | ISO 8601 | `2026-08-15` |
| Money | Decimal **string**, 4 dp | `"1164.0000"` |
| Currency | ISO 4217 | `"SAR"` |
| Country | ISO 3166-1 alpha-2, lower | `"sa"` |

`Accept-Language: ar` selects Arabic for server-generated text. Product names
return both `name` and `name_ar` — the client chooses, since a receipt shows
both regardless of interface language.

---

## 12. Health and observability

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | Liveness. No dependencies. |
| `GET /readyz` | Readiness. Checks the database. |
| `GET /api/v1/meta/version` | Build version, for support |

Every response carries `X-Request-Id`, echoed from the client's if supplied.
It appears in logs and in the error envelope, so a screenshot of an error is
enough to find the incident.

---

## 13. Webhooks (Phase 5)

Outbound events are signed:

```
X-RawSyst-Signature: t=1755253200,v1=<hmac-sha256>
```

Timestamped and HMAC-signed so a receiver can reject replays. At-least-once
delivery with exponential backoff; receivers must be idempotent on `event_id`.

---

## 14. Deliberate omissions

| Not doing | Why |
|---|---|
| GraphQL | One team, three known clients. GraphQL's flexibility buys nothing here and costs query-cost analysis, caching complexity, and an N+1 surface. |
| Offset pagination | Skips and repeats rows in a list that changes while it is read |
| Numeric JSON money | JavaScript floats cannot represent every decimal exactly |
| 403 for cross-tenant | Confirms existence; 404 leaks nothing |
| Permissions in the JWT | A revoked permission must take effect immediately |
| Bulk endpoints in Phase 1 | `/sync/push` already batches; a second batching path is duplicated risk |
