# 11 — POS & Sales (Phase 1)

**Blueprint:** B7, B8, B10, E1, E3, J4. **Depends on:** `01` (ZATCA), `02` (posting), `03` (sync), `10` (catalog).

This is the screen used thousands of times a day by the least-trained user, and
the only path in the system with a hard latency budget. It gets more design
attention than any other module.

---

## 1. The rule that shapes everything

> **Nothing in the checkout path touches the network.**

Products, prices, tax rates, the customer list, the ZATCA CSID key and the
invoice chain are all local. J4's budget — cart update **under 100 ms** — is not
achievable with a round trip, and blueprint A2 #3 makes offline operation a hard
requirement rather than a degraded mode.

The server is reached only by the sync engine, afterwards, in the background.

---

## 2. Sale lifecycle

```
  cart (local, ephemeral)
    │ add lines · scan · discount · attach customer
    ├──► hold  ──► resume
    │
    ▼ finalize()
  ┌──────────────────────────────────────────────┐
  │ 1. validate  price floor · permissions ·      │
  │              amount limits · period open      │
  │ 2. compute   VAT per line from dated registry │
  │ 3. tenders   must sum exactly to total        │
  │ 4. ZATCA     allocate ICV · chain PIH ·       │
  │              sign LOCALLY · build QR          │
  │ 5. stock     write movement deltas            │
  │ 6. COMMIT    one local SQLite transaction     │
  │ 7. print     receipt — legally deliverable    │
  │ 8. enqueue   sync + ZATCA submission          │
  └──────────────────────────────────────────────┘
```

Steps 1–6 are one transaction. **The receipt prints only after commit** — a
printed tax invoice the system has no record of is a compliance incident.

---

## 3. Cart

```sql
-- local SQLite only; never synced
held_cart      id · terminal_id · label · customer_id · created_at
held_cart_line cart_id · variant_id · qty · unit_price · line_discount · note
```

| Behaviour | Design |
|---|---|
| **Scanner input** | Captured **globally** — no field focus required (B7). A keystroke burst ending in Enter, faster than human typing, is treated as a scan |
| Search | SKU, name, Arabic name; trigram over the local snapshot |
| Quick grids | Favorites and Recently Sold, per terminal |
| Variant picker | Parent tap → size × colour grid → one tap (B2) |
| Line discount | Within the cashier's limit; beyond it requires manager PIN, logged |
| Price override | Permission-gated; **never below `price_floor`** |
| Hold / resume | Multiple carts up to the plan ceiling, instant resume |
| Gift / FOC | Zero-value line, tracked separately from paid sales, own accounting entry |

### Manager PIN

An over-limit discount does not fail — it prompts for a manager PIN inline. The
approving user id, the requested amount and the limit exceeded are all written
to the audit log. Blueprint B9 requires the authorisation; recording *who*
approved is what makes it useful afterwards.

---

## 4. VAT computation

The single most correctness-sensitive calculation in the product.

```
for each line:
    rate = registry.VATRate(country, invoice.issue_date, tenant)   ← DATED
    if company.prices_include_vat:            ← Saudi retail default (E2.2)
        net = gross / (1 + rate)
        vat = gross − net
    else:
        net = gross
        vat = net × rate
```

Four rules that are easy to get wrong and expensive to fix:

1. **The rate is resolved at the invoice's issue date**, never at "now". A
   reprint of a March invoice in June must show March's rate.
2. **VAT-inclusive pricing is the default** (E2.2) — the shelf price already
   includes VAT, and the engine back-calculates net.
3. **Rounding is per line, half-up, to 2 decimals**, then summed. Summing then
   rounding produces a total that disagrees with the printed lines.
4. **All arithmetic is `decimal`**, never float. 0.15 has no exact binary
   representation.

Non-standard tax treatments carry an **exemption reason code** on the line —
ZATCA requires the reason to appear on the invoice.

---

## 5. Tenders

Full multi-tender split on one invoice (B7, E3.1).

```sql
sales_tender
  invoice_id · method · amount · reference
  settlement_status (pending | settled | reconciled)
```

`method ∈ cash · mada · visa · mastercard · amex · apple_pay · stc_pay ·
samsung_pay · sadad · bank_transfer · cheque · tabby · tamara · store_credit ·
loyalty · customer_due`

**Mada is its own value, never folded into "card"** (E3.1). Its processing fee is
materially lower than international credit, so merging them misstates margin per
sale — which is the whole reason the Owner asked for payment-method reporting.

Invariant: `SUM(tender.amount) == invoice.total_inclusive`, exactly, in decimal.
Enforced by a `CHECK` via a deferred constraint trigger, not only in the service.

`customer_due` posts to AR and is refused when it would breach the customer's
credit limit (B16).

---

## 6. ZATCA at the counter

Full design in `01-invoice-zatca-engine.md`. What the POS does:

| Document | Offline | Behaviour |
|---|---|---|
| **Simplified (B2C)** | ✅ Yes | Sign locally, print immediately, report later. The normal showroom flow and the fastest path in the app. |
| **Standard (B2B)** | ❌ No | Per `company.b2b_offline_policy`: **block** (default), **draft & hold** with a "Delivery Note — NOT A TAX INVOICE", or **one-tap convert to Simplified** |

The B2B block message is fixed by the blueprint and rendered verbatim:

> *"Standard tax invoice requires internet connection. Save as Draft, or issue as
> Simplified Invoice if the buyer does not require a VAT invoice."*

Offline is displayed as a **neutral operating state**, never an error. Red is
reserved for things actually wrong, so cashiers do not learn to ignore it.

---

## 7. Returns, exchanges, credit notes

The original invoice is **always scanned or linked, never re-typed** (B10).

### Quick exchange — one screen

```
scan invoice → select line(s) → pick replacement variant
             → system computes the difference → settle → done
```

**Implemented.** `POST /api/v1/pos/exchanges`, gated on `sales.exchange`. One
request, one transaction, two documents: a credit note against the original and
a new invoice for the replacement. There is deliberately no third document type
— inventing one would mean inventing its ZATCA treatment, and E1 has none to
invent. The credit note takes the earlier ICV on the terminal's chain, fixed
rather than incidental, so the sequence does not depend on execution order.

#### How the two settle against each other

A customer swapping a 115 item for a 230 one hands over 115. They do not hand
over 230 and receive 115 back, and the books must not record that they did: the
drawer would be expected to hold cash that never passed through it, and the
blind Z-count at close would show a variance with no cause.

So the offsetting portion — `min(credit, replacement)` — settles on **both**
documents through an `exchange_clearing` tender, and only the genuine difference
moves real money. The account rises on the credit note and falls on the invoice
inside the same transaction, netting to zero.

| | Credit note | Replacement invoice | Cash |
|---|---|---|---|
| Swap up (115 → 230) | clearing 115 | clearing 115 + cash 115 | **+115** |
| Swap down (230 → 115) | clearing 115 + cash 115 | clearing 115 | **−115** |
| Even swap | clearing 115 | clearing 115 | **0** |

`exchange_clearing` is deliberately **not** `store_credit`. Store credit is a
real balance a real customer can come back and spend; borrowing it as an
internal mechanism would inflate every report of credit issued and outstanding.
`cash_session_cash_in` filters on `method = 'cash'`, so the clearing half never
reaches the drawer.

The invariant is asserted in Go before commit and queryable afterwards as
`exchange_clearing_balance(company_id)`, which is **always zero** for a healthy
company. A balance on it is a bug with a name, findable by looking at one
account rather than by reconciling a day of takings.

The company must have an account mapped to the `exchange_clearing` role, like
`cash` or `cogs`. Migration 0030 creates one (code 2350, a liability) for every
company that already has a chart of accounts; a company without the mapping is
refused with a message naming exactly what to set.

**The settlement is computed by the server, never accepted from the till.** A
terminal stating how an exchange settles could quietly undercharge, and the
figure is derivable from two totals the server already owns — the original
invoice and the registry rate at the transaction date. The POS shows an estimate
so the cashier can speak to the customer before committing, labelled as an
estimate, and the server refuses the exchange if the two disagree.

Idempotency covers the **pair**. Both halves carry device-assigned UUIDs and
both must be present for a retry to be recognised; deduplicating each half alone
would let a retry sell the replacement a second time.

### The nine effects (C14)

Every return performs all nine in **one transaction**:

1. Reverse inventory — quantity **and value**
2. Reverse revenue
3. Reverse Output VAT — at the **original invoice's** rate
4. Reverse COGS — at the **original** cost
5. Settle the refund (cash · card reversal · store credit · reduce due)
6. Reverse or adjust loyalty points earned
7. Reverse the sales commission attributed
8. Generate a linked **Credit Note** referencing the original
9. Write journal entry and audit record

### Partial returns

```
proportion        = returned_qty / original_qty
revenue_reversed  = line_net              × proportion
vat_reversed      = line_vat              × proportion
discount_reversed = invoice_discount_alloc × proportion   ← stored at sale time
cogs_reversed     = line_cogs             × proportion
```

`invoice_discount_alloc` is written when the sale is made, not reconstructed
during the return. C14 names proportional discount allocation as "a common place
where cheaper POS software silently produces wrong numbers", and reconstruction
is exactly how that happens — the rounding drifts.

**There is no edit and no void of a finalized invoice.** The UI explains why and
offers the legal path:

> **This invoice cannot be edited or deleted.**
> Finalized tax invoices are immutable under ZATCA rules. To correct it, issue a
> **Credit Note**.  `[ Issue Credit Note ]`

---

## 8. Hardware (B8)

| Device | Integration |
|---|---|
| Barcode scanner | USB HID, plug-and-play, **global keystroke capture** |
| Receipt printer | **ESC/POS direct, 80mm/58mm, no browser print dialog** — this requirement is why the POS is Tauri and not a web page |
| Cash drawer | RJ11 pulse fired **automatically** on a cash sale |
| Label printer | Xprinter / Zebra / TSC |
| Customer display | Optional second screen with running total |
| Card terminal | Amount pushed from POS so the cashier cannot mistype; result returned automatically |

Setup wizard: Connect Scanner → Detect Printer → Test Print → Test Drawer →
Configure Receipt → Finish. No technical configuration is expected from shop
staff.

---

## 9. Shift & cash drawer (C8)

```
open (float counted) → sales tracked silently → mid-shift cash drop
                     → X-report (snapshot, closes nothing)
                     → Z-report (count actual → Short / Over / Exact → signed)
```

The Z-report is the definitive daily reconciliation record. Variance posts to a
Cash Over/Short account rather than being absorbed silently — an unexplained
drawer difference is information, not noise.

---

## 10. API

```
POST /api/v1/sync/push                      the POS's primary write path
GET  /api/v1/sync/pull?since=

GET  /api/v1/invoices                       ?store_id=&issue_date.gte=&state=
GET  /api/v1/invoices/{id}
POST /api/v1/invoices/{id}/credit-note
POST /api/v1/invoices/{id}/reprint          logged; not a new document

POST /api/v1/shifts                         open
POST /api/v1/shifts/{id}/cash-drop
GET  /api/v1/shifts/{id}/x-report
POST /api/v1/shifts/{id}/close              Z-report
```

Note what is **absent**: there is no `POST /invoices`. Sales originate on the
terminal and arrive through `/sync/push`. A second creation path would need its
own ICV allocation and would be a second place to get the chain wrong.

| Route | Permission |
|---|---|
| Sync push | Device token, scoped to `/sync/*` |
| Credit note | `sales.refund` |
| Reprint | `sales.view` + logged |
| Shift close | `sales.receive_payment` |

---

## 11. Performance budget (J4)

| Step | Budget | How |
|---|---|---|
| Scan → line in cart | Near-instant | Local SQLite index on barcode |
| Cart recalculation | **< 100 ms** | Decimal arithmetic in memory; no I/O |
| Finalize → receipt | Immediate | One local transaction, then print |
| Hold / resume | Instant | Local rows |

The ZATCA signature is the heaviest local step. ECDSA over a canonicalised
document is single-digit milliseconds — well inside budget, and the reason
signing locally is viable at all.

---

## 12. Judgment calls

| Call | Rejected alternative | Why |
|---|---|---|
| No `POST /invoices` | Server-side sale creation | A second ICV allocation path is a second way to break the chain |
| Receipt prints after commit | Print then persist | A delivered tax invoice with no record is a compliance incident |
| Tender sum enforced by constraint | Service check only | An unbalanced invoice is unrecoverable once ZATCA has it |
| Discount allocation stored at sale | Recompute on return | Reconstruction drifts on rounding — C14's named failure |
| Offline shown as neutral | Warning styling | Training cashiers to ignore warnings makes real ones invisible |
| Held carts never sync | Cross-terminal resume | Adds conflict surface for an ephemeral convenience |
| Per-line rounding, then sum | Sum then round | The printed lines must add up to the printed total |
