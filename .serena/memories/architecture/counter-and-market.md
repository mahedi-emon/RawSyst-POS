# The counter model, and how a market's obligations are asked

Added 2026-09-02 under the international directive. Migrations `0103`–`0105`.

## The model

```
business (tenant) -> company -> shop (store) -> COUNTER -> session -> sale
```

A counter **is** a `device` row. There is no second POS, no second table and no
second sale path: a till in a browser and a till in the desktop app sell through
the same service, take the same shift, move the same stock and land in the same
audit trail.

## `device.binding` (0104) — the only thing that differs

| binding | who opens a session | proves itself with |
|---|---|---|
| `session` | any user the RBAC scope allows | their own signed-in session |
| `paired` | the enrolled machine only | the device secret in its OS keystore |

- New counters default to `session` and are created **active** — there is no
  machine to wait for. `paired` ones stay `pending` until they enrol.
- **Enrolling moves a counter `session` -> `paired`**, in the same statement
  that writes the secret. `device_session_binding_holds_no_secret` makes the
  combination unrepresentable, so forgetting it is a failed write. This is the
  upgrade path: browsers today, a locked-down till on the busy counter later,
  same API, same counter, same history and chain.

**The trade-off, on the record:** a `session` counter is proved by a permission,
not by a machine. A stolen user session can open any counter that user's scope
allows, where before it could not sell at all. Everything else still applies —
tenant isolation, the route permission, company and store scope, an open shift,
and an audit trail naming user *and* counter. A browser is refused on a `paired`
counter by name; the weaker authority never overrides the stronger.

## Opening a counter

`POST /api/v1/pos/counter-sessions` `{device_id}` — re-issues the caller's OWN
access token with `did` set. Same session id, same user, same company scopes.

- **No refresh token.** Standing at a till is not a reason to stay signed in
  longer. When the access token expires every check runs again, which is how a
  counter paused or revoked in between takes effect.
- The company is resolved **from the counter**, never accepted from the caller.
- `GET /api/v1/pos/counters?company_id=` lists only what would work: active,
  session-bound, in a shop the caller's scope reaches. A paired counter is
  absent rather than shown-and-refused.

Every POS route still reads the till from `did` and never from a body — a
cashier who could name a counter could ring onto another counter's shift and
another shop's stock, inside their own tenant where RLS has no reason to object.

## `internal/market` — where a country's obligations are asked

`market.EInvoicingApplies(country)` — Saudi only, today. One function so
`country == "sa"` is not scattered. Two places had assumed it:

1. `sales.resolveTerminal` refused any terminal with no EGS unit, **in every
   market**. An EGS unit is a ZATCA CSR, so nothing outside the Kingdom had one.
2. `devices.Register` refused to create a counter without naming one.

Now both ask the market. Off a chain, `Finalize` and `Refund` skip
reserve/document/hash/record and the submission queue **whole** — no placeholder
ICV. A sale off a chain simply writes no `zatca_invoice` row, and that absence
is the representation.

**Do NOT put dated legal values here.** A rate, a threshold, a format belong in
the regulatory registry, resolved at the transaction date with evidence. This
package answers which code path exists at all.

## The VAT-rate key bug (0105)

`registry.VATRate` asked for the constant `SA.VAT.STANDARD_RATE` while faithfully
passing the caller's country beside it. A Bangladeshi sale therefore asked for
Saudi Arabia's rule filtered to Bangladesh and matched nothing. `vatRateKeyFor`
now derives the key from the country.

`BD.VAT.STANDARD_RATE` is seeded **unverified** (0105), NBR named. Bangladesh
applies several rates plus supplementary duty; one value does not express that,
and `sales.taxable` still charges `reduced` at the standard rate — see its
comment. The **US is deliberately absent**: sales tax needs a jurisdiction, not
a country, so a US sale still misses the registry honestly.

## The regressions this caused (and the lesson)

Full suite after the change: **green, 21 packages, 0 failures** (`internal/api`
944s, `platform/db` 625s — real database work).

It was not green on the first attempt. Nine failures, both causes mine:

- **8** — `registerTill` in `devices_test.go` asserted a new terminal is
  `pending`. Since 0104 a counter registered with no binding is `session`-bound
  and therefore **active immediately**. Every test using that helper is about the
  PAIRING lifecycle, so the fix was `"binding": "paired"` in the helper — which
  also required exposing `binding` on `POST /api/v1/devices`, needed anyway.
- **1** — a `POST /platform/tenants` call with no `market`.

**Lesson:** the five new counter tests proved the NEW behaviour and could not
prove the old behaviour survived. Only the full run does that. Run it as part of
a change to the sale or device path, not as confirmation afterwards.

## The production boot gate is market-aware

`registry.Health` splits unverified release-blockers into **BlockingRelease**
(markets this deployment serves — still refuses a production start) and
**DeferredBlockers** (markets nobody here trades in — named in the log, never
blocking). Served markets come from `SELECT DISTINCT market FROM tenant`, read
via `TxAsPlatform`.

**`tenant` is FORCE RLS — an unscoped read returns zero rows, silently**, which
would read as "serves no markets" and disable the gate for everyone. That is why
`servedMarkets` uses the platform plane, and why
`TestServedMarketsAreReadFromTenantData` fails on an empty result rather than
passing.

Why relaxing it is safe: `gate()` refuses **every** unverified rule at the point
of USE when `requireVerified` is set. The boot check is an early warning, not the
last line.

`healthFor(ctx, served)` is split out so the three market scenarios can be tested
against the real seeded registry without depending on whatever tenants a shared
test database happens to hold.

**Known limitation:** the gate runs at boot only. Provisioning a Saudi tenant
onto a running Bangladesh deployment does not re-run it — the per-use gate still
refuses, and the next restart blocks. Closing it means a provisioning-time check.

## Gotcha that cost time twice

`PricesIncludeTax` **defaults to true** when the field is omitted. Every fixture
calls `oneItemSale(f, uuid, "1", "115.00", "115.00")` — price and payment equal,
because the price already contains the tax.

## Related
[[audit/verified-2026-09-02-directive]] · [[code/module-status]] · [[architecture/target-markets]]
