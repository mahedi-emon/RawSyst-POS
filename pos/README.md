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

## The four rules this client is built around

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

## What is not built yet

- The catalogue is not cached locally, so **scanning needs the network**. A sale
  already in the cart is safe; starting a new one offline is not yet possible.
- Returns, hold/resume, and the receipt itself.
- The local ICV/PIH chain. The table exists in the local schema and is unused:
  the server allocates the counter today, and the terminal cannot extend a chain
  it cannot sign.
