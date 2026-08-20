# 10 — Catalog & Inventory (Phase 1)

**Blueprint:** B1–B4, C13. **Depends on:** `04` (RBAC), `05` (registry), `06` (data model).

---

## 1. Module boundary

Owns products, variants, barcodes, warehouses, stock movements and cost layers.
Publishes stock and cost events; subscribes to nothing. Accounting subscribes to
*it*, never the reverse — that direction keeps the dependency graph acyclic.

```
internal/catalog     product · variant · category · brand · unit · barcode
internal/inventory   warehouse · stock_movement · cost_layer · transfer · count
```

---

## 2. Entities

### product / variant

The split is the single most important decision in this module. Blueprint B2:
standard POS logic (one product = one price = one stock count) does not work for
clothing.

| | product | variant |
|---|---|---|
| Is | The parent concept | **The SKU** |
| Carries | Name, Arabic name, category, brand, tax category, tracking flags | Own SKU, barcode, 4 prices, cost, stock, weight, image |
| Referenced by | Nothing transactional | Every sale line, stock movement, cost layer |

A product with no variation still gets **exactly one** variant row. Treating
"simple products" as a separate case would double every path in inventory,
costing and sales, and the two paths would drift within months.

```sql
product
  company_id · sku · name · name_ar
  category_id · brand_id · unit_id
  tax_category       -- 7 treatments, from the registry
  tax_exemption_reason_code   -- ZATCA requires a reason for non-standard
  track_serial · track_batch · warranty_months
  lifecycle (active | inactive | discontinued)

variant
  product_id · sku · barcode
  attributes jsonb            -- {"size":"L","color":"Black","season":"Winter"}
  price_retail · price_wholesale · price_dealer · price_floor
  cost_standard · weight · image_url · reorder_level · max_level
  UNIQUE (company_id, sku)
  UNIQUE (company_id, barcode) WHERE barcode IS NOT NULL
```

`attributes` is JSONB, not fixed size/colour columns, because B2 permits
unlimited custom attributes (material, style, season, gender, model). GIN index
serves the variant-matrix query.

**`lifecycle` never deletes.** B1: a discontinued product must still display
correctly on old invoices and reports. There is no delete path.

### Price floor is enforced, not advisory

B1 calls `price_floor` "the lowest price a cashier is ever allowed to sell at,
**even after discount** — enforced by the system, not just policy."

Enforced in three places, deliberately redundant:
1. POS client — immediate feedback
2. `sales` service on finalize — the authoritative check
3. `CHECK` on the sale line — the backstop that survives a service bug

### Variant generation

```
Parent: "Executive Abaya"
Axes:   Size [S,M,L,XL,XXL] × Colour [Black,Navy,Maroon]
     → 15 variants, each with its own SKU, barcode, price, cost, stock
```

Generation is a preview-then-commit operation: the API returns the proposed set,
the user deselects unwanted combinations, then commits. Generating 15 rows and
asking the user to delete 4 is worse UX and leaves orphaned barcodes.

---

## 3. Barcode engine (B3)

Smart codes are **human-readable over a scannable value**, not instead of it:

```
M-WIN-BLK-XL   ← readable: Men / Winter / Black / XL
               ← encoded as Code128 underneath
```

| Concern | Design |
|---|---|
| Formats | Code128, EAN-13, EAN-8, UPC-A, QR / DataMatrix |
| Pattern | Per-company template from category/season/colour/size tokens |
| Manual override | Always allowed — factory-printed codes must be usable |
| Uniqueness | Enforced per company; a collision fails the generate |
| Bulk | 1,000 codes in one operation, as a background job with progress |

EAN-13 check digits are computed, never accepted from input. A wrong check digit
produces a barcode that scans as a different product.

---

## 4. Stock

### Movements, not levels

```sql
stock_movement
  variant_id · warehouse_id
  delta numeric(18,4)        -- −4, never "6"
  reason (sale|return|grn|adjust|transfer_in|transfer_out|wastage|opening|count)
  source_type · source_id · device_id
  occurred_at   -- device clock, ordering only
  recorded_at   -- server clock, authoritative
```

Level is **always** `SUM(delta)`. Reasons this is not negotiable are in
`03-sync-idempotency.md` §6: deltas commute, so three offline terminals each
selling 4 units produce −12 regardless of sync order. An absolute level would
lose two of the three writes.

### The eight tracked states (B4)

`Total · Available · Reserved · Damaged · Returned · In-Transit · Available-to-Sell`
are **derived views** over movements plus reservations, not stored columns.

### Transfers

```
Request → Manager Approval → Dispatch (in-transit) → Receiving branch confirms
```

Dispatch writes `transfer_out` at the source **and** an in-transit reservation.
Receipt writes `transfer_in` at the destination. A discrepancy between dispatched
and received quantity is auto-flagged and posts to a variance account — it is
never silently absorbed.

### Wastage

Requires a **mandatory reason** and posts an automatic loss write-off to
accounting (B4). No free-text-only path: reason is a controlled list plus
optional notes, because free text cannot be reported on.

### Physical count

Five modes (full, category, brand, location, spot). Compares system vs counted
and **auto-generates a signed Adjustment Voucher** carrying reason, user,
approval and timestamp for every variance.

---

## 5. Costing (C13)

| Method | Implementation |
|---|---|
| **WAC** | Running average per variant per warehouse |
| **FIFO** | `cost_layer` rows consumed oldest-first |
| **Standard** | Fixed cost, difference posts to a variance account |
| **Landed cost** | Overlay on any of the above |

```sql
cost_layer
  variant_id · warehouse_id · received_at
  qty_received · qty_remaining
  unit_cost          -- INCLUDES allocated landed cost
  source_id          -- GRN line
INDEX (variant_id, warehouse_id, received_at) WHERE qty_remaining > 0
```

### Landed cost allocation

```
Goods    SAR 10,000
Shipping SAR    500
Customs  SAR    300
       = SAR 11,000 spread across the receipt
```

Allocation basis is configurable (value, quantity, weight). **Import VAT is
excluded from the allocation** and flows to Input VAT instead — blueprint E2.5 is
explicit that duty goes to inventory cost while import VAT is recoverable. Mixing
them overstates inventory and understates the VAT reclaim.

### Offline costing

The terminal cannot compute exact FIFO while disconnected, so it records a
**provisional cost** from its cached snapshot. On sync the server recomputes
against real layers and posts the authoritative figure; any difference goes to
Cost Variance and appears in the exception report.

The alternative — blocking offline sales until cost is known — violates the hard
offline-first requirement. Provisional-then-reconciled keeps the till running and
the books exact, at the cost of an auditable variance line.

> **As built, this is not what happens, and the difference matters.** The till
> sends quantities, not costs. `sales.Finalize` costs every line itself against
> the real layers, and replay puts an offline sale through that same finalizer,
> so the authoritative figure is the only one ever posted and there is no
> difference to book. Implementing the paragraph above literally would now
> double-count: the difference would be posted on top of a cost of goods sold
> that is already correct. What is genuinely missing is visibility of how far a
> terminal's cached costs have drifted. Tracked as P10.

### Negative stock

`BLOCK` refuses the sale. `ALLOW_WARN` proceeds with a warning and auto-corrects
cost on the next receipt (C13).

The correction is built. The uncovered units are recorded in `cost_shortfall`
with the estimate they were charged at, and the next receipt of that variant
settles them oldest-first at what the arriving stock actually cost, posting the
difference to Cost Variance as its own entry on the goods receipt. The stock
valuation deducts open shortfalls, so C13's tie-out holds while a shop is
trading below zero rather than only when its stock is positive. See
`02-posting-engine.md` §6.5 for the mechanism and why a negative cost layer was
not the answer.

### The tie-out invariant

Nightly, per company per warehouse:

```
SUM(cost_layer.qty_remaining × unit_cost)  ==  Inventory GL control balance
```

C13: "any divergence is flagged as an exception." This is QA gate M1's
sub-ledger tie, running continuously rather than once at launch.

---

## 6. API

```
GET    /api/v1/products                    ?category_id=&brand_id=&q=&lifecycle=
POST   /api/v1/products
GET    /api/v1/products/{id}
PATCH  /api/v1/products/{id}
POST   /api/v1/products/{id}/variants:preview     generate axes → proposed set
POST   /api/v1/products/{id}/variants             commit selected

GET    /api/v1/variants/{id}
PATCH  /api/v1/variants/{id}
GET    /api/v1/variants/by-barcode/{barcode}      POS lookup (back office only;
                                                  the terminal reads local SQLite)

POST   /api/v1/barcodes:generate                  bulk → background job
GET    /api/v1/labels/templates

GET    /api/v1/stock                       ?variant_id=&warehouse_id=
POST   /api/v1/stock/adjustments                  reason mandatory
POST   /api/v1/stock/transfers
POST   /api/v1/stock/transfers/{id}/dispatch
POST   /api/v1/stock/transfers/{id}/receive
POST   /api/v1/stock/counts
POST   /api/v1/stock/counts/{id}/submit           → adjustment voucher

GET    /api/v1/costing/valuation           ?warehouse_id=&as_of=
```

### Permissions

| Route class | Permission |
|---|---|
| Read catalogue | `catalog.view` |
| Create / edit | `catalog.create` / `catalog.edit` |
| **See cost or margin** | `catalog.view_cost_price` / `catalog.view_profit_margin` |
| Adjust stock | `inventory.adjust_stock` |
| Transfer | `inventory.transfer_stock` |

`cost_standard` and any margin field are **omitted from the payload** for callers
without the permission — not nulled. A cashier holding `catalog.view` receives a
response in which the field does not exist.

---

## 7. Events published

| Event | Consumed by |
|---|---|
| `inventory.StockMoved` | Accounting (valuation), Reporting |
| `inventory.CostLayerConsumed` | Accounting (COGS posting) |
| `inventory.BelowReorderLevel` | Notifications |
| `inventory.WastageRecorded` | Accounting (loss write-off) |
| `catalog.PriceChanged` | Audit, POS sync |

---

## 8. Background jobs

| Job | Cadence |
|---|---|
| `inventory.tie_out` | Nightly — valuation vs GL |
| `inventory.reorder_scan` | Hourly — below-reorder alerts |
| `inventory.dead_stock_scan` | Daily — no sale in a configurable window |
| `catalog.barcode_bulk` | On demand |

---

## 9. Screens

| Screen | Notes |
|---|---|
| Product list | Filter, search (Arabic + Latin), lifecycle chips |
| Product editor | Tabs: details, variants, pricing, tax, tracking |
| **Variant matrix** | Size × colour grid with stock, low/out/dead colouring — the fashion differentiator |
| Barcode & label studio | Template designer, bulk generate, thermal + A4 |
| Stock overview | By warehouse, by variant, in-transit |
| Transfer workflow | Request → approve → dispatch → receive |
| Physical count | Five modes, variance review, voucher generation |

---

## 10. Judgment calls

| Call | Rejected alternative | Why |
|---|---|---|
| Variant always exists, even for simple products | Separate simple-product path | Doubling every inventory and costing path guarantees drift |
| `attributes` as JSONB | Fixed size/colour columns | B2 permits unlimited custom attributes |
| Movements, never stored levels | `stock_level` column | Levels lose writes when offline terminals sync out of order |
| Price floor checked in three layers | Service check only | A service bug must not permit an illegal price on a tax invoice |
| Provisional cost offline | Block the sale | Blocking violates the hard offline-first requirement |
| Import VAT excluded from landed cost | Allocate everything | E2.5: duty is inventory cost, import VAT is recoverable |
