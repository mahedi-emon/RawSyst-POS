# 09 — Running it: cache, storage, live push, and what watches it

The four pieces of infrastructure the architecture overview names, what each
one is actually for, and what a deployment loses by leaving it out.

Everything here is **optional**, and the product says at start-up which of them
it is running without. That is the important property: a shop with one server
and a hundred sales a day should not have to operate Redis, and a chain with
forty branches and three API replicas cannot do without it.

---

## 1. The rule that governs all of it

> Nothing in this document is a source of truth.

Every value in the cache can be recomputed from Postgres. Every file in object
storage is referenced from a row that knows where it is. Every message on the
live socket says *what changed* and is followed by the client re-reading a real
endpoint. Every metric is derived.

That is what makes each of them safe to lose. Redis going down costs latency,
not correctness. The object store going down means a document cannot be fetched
right now, not that it is gone. The socket dropping means a till is a few
seconds behind, not wrong.

The failure this rule exists to prevent is the ordinary one: a cache that ends
up holding the only copy of something is a database with no backups.

---

## 2. Redis — `internal/platform/cache`

**Off by default. `RAWSYST_REDIS_ADDR=` empty means an in-memory cache.**

### What it is for

Three things, all of them from design 04 and 05:

| Use | Why it matters |
|---|---|
| Cached permission resolution | Design 04 is explicit that permissions are **not** embedded in the JWT, because a permission revoked at 10:00 must not stay effective until a 15-minute token expires. Resolution is therefore per request, and a cache is what makes that affordable. |
| Rate limiting | The public recovery routes and the portal codes. |
| Invalidation pub/sub | Design 05: a regulatory rule edited on one replica has to be seen by the others without waiting for a TTL. |

### What it is **not** for

The job queue. Design 08 §2 chose Postgres deliberately — a job enqueued in the
same transaction as its trigger cannot be orphaned by a crash between two
writes, and a queue an accountant can query in SQL is worth more than a faster
broker. That decision stands.

### What a deployment without it gives up

Nothing, **if it runs one API process.** An in-memory map is faster than Redis
and a per-process limit of ten is a limit of ten.

At two processes it gives up three things at once:

- a permission revoked on one replica stays live on the other until that
  replica's own cache expires;
- a rate limit of ten becomes twenty, then thirty — and it rises every time
  somebody scales up to cope with the traffic the attack is generating;
- a rule edit is invisible to the other replica until its TTL turns over.

So the in-memory implementation is a **real** implementation, not a stub:
`cache_test.go` checks expiry, prefix drops, counter windows and pub/sub
fan-out against it. The failure being avoided is the common one — an interface
written for Redis with a fallback that silently does nothing, so a
single-process deployment quietly loses its rate limiting the day somebody
mistypes an environment variable.

`Shared()` reports which one is in use, and the API says so in its start-up
log.

---

## 3. Object storage — `internal/platform/blob`

**Off by default. Files stay in Postgres as `bytea`.**

### S3-compatible, not S3

The endpoint is configuration. MinIO on the shop's own server, Amazon,
Cloudflare R2, Wasabi, DigitalOcean Spaces, Ceph — all the same code. That is
not a nicety: a Saudi deployment under PDPL may be required to keep records
inside the Kingdom, and a product tied to one vendor's regions would be making
that decision on the shop's behalf.

### Written against the protocol

Four verbs — PUT, GET, DELETE and a presigned URL — signed with AWS Signature
Version 4, about two hundred lines. The alternative was a dependency tree with
credential chains, region resolvers, retry policies and an XML layer this
product uses none of.

Hand-written signing is worth exactly as much as its test, because a wrong
signature does not look wrong — it is sixty-four hex characters and the store
answers 403 without naming which of the eleven canonical fields was wrong. So
`blob_test.go` checks the canonical request against the SHA-256 Amazon
publishes for its own worked example, and the key chain against the vector in
"Deriving the signing key".

### What it buys

The read path, mainly. A 30MB report streamed through the API holds a
connection, a goroutine and a database handle for the length of the download; a
presigned URL hands the browser a link and the API is done in a millisecond.
And a million receipt PDFs are out of `pg_dump`.

Presigned URLs are short-lived on purpose: one is a bearer token in a query
string, it will end up in a browser history and a proxy log, and the mitigation
is that it stops working.

---

## 4. The live socket — `internal/platform/live`

**Always on, and nothing depends on it.**

### What it is for

Design 03 §1 is explicit about the limit: an offline till **cannot** be
prevented from overselling, and the product chooses accurate detection over
false confidence. While a till is online, a stock delta broadcast in
near-real-time shrinks the window for a concurrent oversell from hours to
seconds.

That is a **prevention layer**, not a correctness guarantee, and nothing may be
built so that it only works when the socket is up.

### The stock invariant

> The main stock ledger is the single source of truth. A till's local figure is
> a **cache** of it. `stock.moved` is the synchronisation event the server emits
> **after** an authoritative mutation.

Three consequences, and each of them is a rule something in the code enforces:

**1. Every authoritative mutation announces itself.** Not just an online sale —
an offline sale replayed by the sync engine, a goods receipt, an adjustment,
both halves of a transfer, a return, a part issued to a repair job.

The first version announced from the sale *handler*. That covered one path and
silently missed the rest, including the one the announcement exists for: a
day's offline trading arriving on reconnect. `inventory.Consume` alone has five
callers.

So the recording happens where the ledger is written — `Receive`, `Consume` and
`Restore` in `internal/inventory`, the only three functions that touch
`stock_movement` — onto a collector carried on the request context. The API
drains it after the response. Adding a route that moves stock now announces it
without anyone remembering to.

**2. Nothing is published from inside a transaction.** A push about stock that
then rolled back would never be corrected: there is no second event to say the
sale came undone. The middleware publishes only after a 2xx.

**3. A till's cache never refuses a sale.** It is stale the moment another till
sells something; it can be an hour old, belong to the wrong warehouse, or have
missed a delta while the socket reconnected. `StockCache.shortfall` returns
something to **say**, never a verdict, and there is no method on it that could
block one.

Two smaller rules fall out of the same reasoning:

- **A delta cannot create a row.** It says how much a quantity *moved*, not
  what it became, so applying one to a variant the till never pulled would
  invent a figure from nothing — and an invented quantity is worse than an
  absent one, because a screen shows it just as confidently.
- **Deltas accumulate as decimal strings**, never floats. A till adding
  `0.1 + 0.2` in a float64 drifts away from the ledger it is caching, slowly
  and in a direction nobody could reconstruct.

### How the till gets its figures

`GET /api/v1/pos/stock`, not `/api/v1/stock/on-hand`.

A terminal deliberately knows no company, no store and no warehouse — every POS
route resolves those from the **device**, because a till that could name its
own company could read another company's figures, and row-level security would
not catch it: the rows belong to the same tenant. So the till-scoped endpoint
resolves the warehouse the same way a sale does and returns it.

It is gated on `inventory.view` like every other reading of stock levels. A
cashier without it gets a refusal, the till holds no cached quantity, and it
says nothing about stock — exactly how it behaved before any of this existed.
That refusal is a legitimate answer, not a fault, and the cache treats it as
one.

It also carries what a back office wants without polling — a notification
arriving, a shift closing. A bell that polls every ten seconds makes eight and
a half thousand requests a day per open tab and is still ten seconds late.

### How a browser authenticates one

The WebSocket API cannot set request headers. There is no `Authorization` on
`new WebSocket(...)` and there never will be. That leaves three options:

| | Cost |
|---|---|
| Query string | Written into every access log, proxy log and browser history. An access token in a log file is a credential somebody will find. |
| Cookie | Reintroduces CSRF on the one endpoint the browser credentials automatically. |
| **Subprotocol** | A header the browser *does* set, not logged as a matter of course, not sent automatically cross-site. |

So the client offers two subprotocols — the literal marker `rawsyst.auth` and
the token — and the server reads the second. Same token, same verification,
same expiry. `websocketToken` reads it **only** on an actual upgrade request,
because the header is trivially settable and honouring it elsewhere would be a
second way into every route in the product.

The server echoes only the marker back. Echoing the token would put it in a
response header, which is more visible than the query string this avoids.

### Fan-out and isolation

A socket is bound to one tenant at the handshake, from the caller's own token.
There is no parameter naming a tenant and no way to subscribe to another one.

Broadcasts go onto the cache's pub/sub channel rather than straight to local
sockets, so the delivery path is identical whether there is one replica or
five. With the in-memory cache that is a local fan-out, which is correct
because there is one process — and it is the reason worker-raised pushes reach
browsers only when Redis is configured.

A slow client is **dropped**, not waited for. One browser on hotel wifi must
not hold a broadcast thirty tills are waiting on.

---

## 5. Metrics — `internal/platform/metrics`

**On by default; the token is required outside development.**

### Cardinality is the whole design

The way a metrics endpoint kills a service is not CPU — it is a label whose
values are unbounded. A `path` label carrying the raw URL means one series per
invoice id, and after a week the endpoint takes a minute to render and the
scraper's storage is full.

So requests are labelled by **route pattern** — the string the router matched,
not the string the client sent — and the number of patterns is fixed by the
route table. Status is a class, not a code. There is no tenant label and no
user label: both are unbounded by definition, and "which tenant is slow" is a
question for the logs, which carry the id already.

### No business metrics

Sales per hour, invoices submitted, cash variance: none of them are here. They
belong in the database where they are exact, per tenant and queryable — and a
shop's turnover is not something to publish on a scrape endpoint. This measures
the **service**.

### Two locks on the endpoint

A bearer token in the handler, which config refuses to start without outside
development, **and** nginx answering `/metrics` with 404 from outside. A
scraper reaches it over the internal network.

---

## 6. Error reporting — `internal/platform/observe`

**Off by default.**

Only 5xx and panics. A 400 is the client being wrong, a 403 is the system
working, a 404 is a record that is not there — reporting those fills a tracker
with normal Tuesdays and teaches everybody to ignore it.

### What is scrubbed, and why it is scrubbed here

This product holds a shop's customers, its staff's salaries and its tax
filings. An error tracker is a third party, usually in another jurisdiction,
and PDPL does not stop applying because the data arrived in a stack trace.

A report carries the request id, the route **pattern**, the method, the status
and the tenant uuid — enough to find the request in logs that stay on the
shop's own infrastructure. No query string, no headers, no body, no cookies, no
email, no IP address. The tenant id names a business, not a person.

The scrubbing is in this package rather than in Sentry's own settings, because
a setting somebody has to remember to configure is a setting that is wrong on
the day it matters.

---

## 7. Nginx — `deploy/nginx/`

**Not a security boundary.** Every rule in that file is a courtesy.
Authentication, tenant isolation, permissions and the recovery-route limits are
enforced in the API and are enforced whether or not the proxy exists — running
the API directly on a port is a supported deployment. Nothing there may become
the only thing stopping something.

What it does do:

- terminates TLS, so a certificate renewal reloads a proxy instead of
  restarting an API holding sync sessions from forty tills;
- puts the API and the back office on one hostname, which is one fewer CORS
  problem and one fewer thing to configure;
- refuses traffic before it costs a database connection — 30 r/s for the API,
  **1 r/s for sign-in and password recovery**, because the only thing that
  makes many sign-in attempts from one address is somebody trying passwords;
- holds the live socket open for an hour rather than a minute, since the
  socket's own ping is what notices a dead peer.

Note the interaction worth knowing about: the API's limiter reads `RemoteAddr`
and never `X-Forwarded-For`, because a header a client sets is a limiter a
client defeats. Behind a proxy that means the API sees the proxy's address and
its per-caller limit becomes a global one — which is exactly why the nginx rate
zones are keyed on `$binary_remote_addr`.

`proxy_next_upstream` deliberately excludes `non_idempotent`: retrying a POST
that may already have been applied is how a customer gets charged twice, and
the API's idempotency keys are what make a retry safe when the *client* chooses
it.

---

## 8. Bringing it up

```
docker compose up --build                     one server, no extras
docker compose --profile scale up --build     + redis + nginx
docker compose --profile files up --build     + object storage
docker compose --profile watch up --build     + prometheus
```

Profiles rather than always-on, because every one of them is genuinely optional
and a small shop needs none. See `.env.example` for what each expects.
