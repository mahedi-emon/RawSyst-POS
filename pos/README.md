# RawSyst POS — the terminal

A Tauri shell around a React interface. The shell exists for the three things a
browser cannot do and a till must: hold the CSID private key in the OS secure
store, sign locally, and keep a durable queue so a shop can trade with no
network.

```
pos/
  src-tauri/        Rust shell
    src/main.rs     the commands the web layer may call — a short list
    src/signing.rs  local ZATCA signing: the seam, and the gate
    src/keystore.rs CSID custody. Has no getter, deliberately.
  src/
    api/            client + POS calls, against the existing contracts
    auth/           login, session restore, permission gating
    offline/        the queue, its SQLite store, and the flush timer
    pos/            cart arithmetic and the counter
    ui/             banners and status
```

## Running it

```bash
npm install
npm run tauri dev      # needs the API at VITE_API_BASE_URL (default :8080)
npm test               # cart arithmetic and queue behaviour
npm run typecheck
```

## The five rules this client is built around

**Login is the first screen and there is no way past it.** No development
bypass, no default user. A till that could be opened without signing in would
attribute every sale to nobody, and the cash session it belongs to could never
be reconciled against a person. A stored session is re-checked against
`/auth/me` on launch rather than trusted.

**Permissions shape what is shown and secure nothing.** The counter renders for
`sales.create`, the discount control for `sales.discount`. Every one of those
checks is a courtesy — the server refuses a restricted action whatever the
screen offered, and QA gate M7 proves it. Blueprint A6.2: a hidden button is
never treated as real security.

**A finished sale is durable before the network is touched.** `Finish` writes to
local SQLite and returns; the push happens afterwards and its outcome changes
nothing about whether the sale happened. Doing it the other way round — try the
server, fall back to local — looks equivalent and is not: it makes every sale
wait on a timeout when the network is merely slow, and loses the sale entirely
if the process dies between a successful send and the local write.

**Scanning reads the local catalogue first, the network second.** Not "local if
offline" — asking the server first would make every scan wait out a timeout
whenever the connection is merely slow, which is the common case in a shop with
poor signal and far worse for a queue of customers than a price that is a day
old. The cache is a cache: the server reprices every line on replay, so a stale
row costs a corrected receipt, never a wrong invoice or a wrong journal. The
network is still tried on a miss, which covers a product added since the last
pull.

**No business logic lives here.** Pricing, costing, the invoice chain, stock and
the journal are all the server's, reached through the same sale service an
online sale uses. The terminal states only what it alone knows: which items, at
what prices, paid how, and when. It sends no company, store, warehouse, VAT rate
or currency — the server resolves every one of those from the registered device
and the regulatory registry.

## Signing is gated, and says so

`signing.rs` refuses in every environment, exactly as the server's
`DocumentHasher` and `Submitter` do. The byte-level UBL, the canonicalisation
and the QR TLV layout are still marked `__VERIFY__` in the registry, and a
terminal emitting plausible-looking documents would have invoices rejected at
scale — each one having already consumed an ICV that cannot be given back.

A permanent banner says so on the terminal, and says what *is* safe: the sale,
the stock and the books are all recorded correctly and only the reporting is
outstanding. "E-invoicing unavailable" on its own reads as "stop selling", which
would be the wrong reaction.

`keystore.rs` has `key_presence()` and **no getter**. A caller can ask whether
this terminal is onboarded; it cannot ask for the key. ZATCA §6.5 forbids the
export affordance existing at all — easy to satisfy by not writing the function,
easy to violate later by adding one "just for debugging", so the absence is
documented rather than left to be noticed.

## Money is a string

decimal.js from the wire to the screen, never `parseFloat`. JavaScript's
`number` is a float64 and cannot hold 0.15 exactly; a till that re-rendered a
total through one would eventually print a receipt disagreeing with the invoice
by a hallala, and the customer holds the receipt.

## The catalogue on the terminal

`GET /api/v1/catalog/snapshot` is cursored on `(updated_at, id)`. The till
stores the last pair it saw, so the first sync downloads everything and every
later one downloads only what changed — a terminal that has been off for a week
pulls the difference, not the catalogue. Paging by offset instead would silently
skip or repeat rows when the catalogue changed between pages, and on a till a
skipped row is a product that cannot be scanned.

Withdrawn variants arrive in that delta rather than being filtered out
server-side. A row silently omitted would stay in the local cache forever and
the cashier would keep selling something taken off sale: the absence of news is
not the same as news of an absence. The counter can then say "withdrawn from
sale", which is a different message from "unknown barcode" and a different
mistake by the cashier.

No cost price and no margin are cached. A Cashier holds `catalog.view` and is
denied `catalog.view_cost_price`, so a cache that carried cost would put it on
every till in the shop and defeat the masking the permission exists to provide.
A backend test asserts the payload never mentions either.

## Knowing whether the server is reachable

`navigator.onLine` answers "does this machine have a network interface that
thinks it is up". A till on shop wifi whose uplink is dead, or behind a mall's
captive portal, reports `true` throughout — and both are ordinary Saturday
conditions in retail. So the terminal asks the server directly, on
`GET /api/v1/meta/ping`.

That route is **authenticated on purpose**. What a till needs to know before it
drains a day of takings is not "is there a network" but "can I sync right now",
and those differ exactly when it matters: a probe that only opened a socket
would report online while holding an expired token, and the terminal would
discover the truth by failing to sync. The response is 204 with no body, and the
client checks for exactly that — a captive portal answers everything with 200
and a login page, so "the request did not throw" is evidence of nothing.

- **30s** between probes while reachable; **5s** doubling to a **5m** ceiling
  while not. An hour of outage costs this till under 30 requests, against 3,600
  for the once-a-second polling the design rules out.
- The OS `online` event is a **hint to probe now**, never an answer. It fires
  when an interface comes up, which routinely precedes the uplink being usable,
  and never fires at all when the uplink dies while the interface stays up.
- Restoration triggers the queue drain **immediately**, not at the next flush
  tick. A till that reconnects at 17:58 should not still be holding the day's
  takings at closing time.
- Interval, timeout and backoff are all injectable (`ConnectivityConfig`).

**Network status and queue status are reported separately**, because they are
different questions. "Nothing waiting" with no connection is fine; "23 waiting"
with a good connection is a drain in progress; "23 waiting" with no connection
is the one worth noticing. A single traffic light would make the first and the
last look identical.

Nothing in the monitor is on the selling path. `record` writes to SQLite and
returns, the flush it starts is not awaited, and the monitor is never consulted
before a sale is recorded. `selling.test.ts` composes the real queue, catalogue
and monitor, hangs every network call they make, and rings up twenty sales.

## What is not built yet

- Returns, hold/resume, and the receipt itself.
- The local ICV/PIH chain. The table exists in the local schema and is unused:
  the server allocates the counter today, and the terminal cannot extend a chain
  it cannot sign.
