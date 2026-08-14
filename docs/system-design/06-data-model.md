# 06 — Data Model

Core entities, conventions, and the shared kernel every module builds on.
Detailed per-module schemas live in the `10-*` documents; this file defines what
they all agree on.

---

## 1. Conventions

These are not stylistic preferences. Each one prevents a specific class of bug
that is expensive to find later in a financial system.

| Rule | Why |
|---|---|
| `snake_case` singular table names (`sales_invoice`, not `SalesInvoices`) | Consistent with PostgreSQL's own catalogue; avoids quoting |
| `uuid` primary keys, generated at creation | The POS assigns ids **offline, before any network call**. A database sequence cannot serve that. |
| Every tenant-scoped table carries `tenant_id uuid NOT NULL` + RLS | Isolation enforced by the engine, not by developer discipline |
| Money is `numeric(18,4)`, **never** float | Binary floating point cannot represent 0.15. A VAT figure wrong in the last hallala is wrong on a tax return. |
| Quantities are `numeric(18,4)` | Fabric by the metre, weight by the kilo |
| Rates and percentages are `numeric(18,8)` | FX rates need more precision than money |
| Timestamps are `timestamptz`, always UTC | A tenant in Riyadh and a server elsewhere must agree on "today" |
| Dates that are legally a *date* use `date` | An invoice issue date is a calendar day, not an instant |
| Enums are PostgreSQL `ENUM` types | The database rejects an invalid state; a `text` column does not |
| Soft delete via `is_active`/status, never `deleted_at` on financial records | Blueprint B1: a discontinued product must still display correctly on old invoices |
| `created_at`, `updated_at` on mutable tables; `updated_at` maintained by trigger | Application code forgets |

### Money in Go

`shopspring/decimal` throughout. `float64` for money is banned and the ban is
enforced by review: a single `float64` amount that reaches the journal makes the
trial balance fail to balance by fractions, intermittently, which is the worst
possible failure mode because it looks like a rounding "quirk" rather than a bug.

---

## 2. The shared kernel

Concepts every module depends on. They live in `internal/platform` and
`internal/identity`, and no domain module may redefine them.

```
tenant ──┬── tenant_limit                       plan ceilings
         └── company ──┬── store ── device      the compliance boundary
                       ├── account              chart of accounts
                       └── fiscal_period        Open → Closed → Locked

app_user ── user_session
        └── user_role_assignment ── role ── role_permission

regulatory_rule (platform-wide, effective-dated)
regulatory_rule_override (per tenant)

audit_log (append-only)
```

### Why `company`, not `tenant`, owns the books

Blueprint F4 wants one Owner login across several legal companies with
consolidated group reporting. Blueprint E1/E2 require books, VAT registration
and the ZATCA sequence to belong to a single legal entity.

Both hold if `tenant` is an **access and reporting** construct and `company` is
the **accounting and compliance** boundary. Consequences that follow, and which
every module must respect:

- `account`, `journal_entry`, `fiscal_period` are keyed by `company_id`
- VAT registration, VAT returns and tax archives are per company
- ZATCA ICV/PIH chains are per **device**, under one company's registration
- Group consolidation is a computed view with inter-company elimination, never a
  shared ledger

---

## 3. Phase 1 entity map

```
                    ┌──────────┐
                    │ company  │
                    └────┬─────┘
        ┌────────────────┼────────────────┬──────────────┐
        │                │                │              │
   ┌────▼────┐     ┌─────▼──────┐   ┌─────▼─────┐  ┌─────▼──────┐
   │ account │     │fiscal_period│   │   store   │  │  product   │
   └────┬────┘     └─────┬──────┘   └─────┬─────┘  └─────┬──────┘
        │                │                 │              │
        │                │           ┌─────▼─────┐  ┌─────▼──────┐
        │                │           │  device   │  │  variant   │  ← the real SKU
        │                │           └─────┬─────┘  └─────┬──────┘
        │                │                 │              │
        │          ┌─────▼─────────────────▼──────┐ ┌─────▼────────┐
        └──────────┤       sales_invoice          │ │ stock_movement│
                   │  (+ line, tender, zatca_*)   │ │  (deltas)     │
                   └─────┬────────────────────────┘ └─────┬────────┘
                         │                                │
                   ┌─────▼─────┐                    ┌─────▼──────┐
                   │journal_   │◄───────────────────┤cost_layer  │
                   │entry+line │   COGS at sale     └────────────┘
                   └───────────┘
```

### Variant is the stockkeeping unit, not product

Blueprint B2 is emphatic: standard POS logic (one product = one price = one
stock count) does not work for clothing. So:

- `product` is the parent — name, category, brand, tax category, base attributes
- `variant` carries **its own SKU, barcode, price, cost, stock, weight, image**
- Every stock movement, cost layer, sale line and barcode references `variant_id`

A product with no variants still gets exactly one variant row. Treating "simple
products" as a separate case would double every code path in inventory and
costing, and the two paths would drift.

---

## 4. Core Phase 1 tables

Abbreviated — full DDL lives in the migrations. Common columns
(`id`, `tenant_id`, `created_at`, `updated_at`) are omitted for readability.

### Catalogue

```sql
product
  company_id · sku · name · name_ar · category_id · brand_id · unit_id
  tax_category (standard|zero_rated|exempt|out_of_scope|export|reverse_charge|import)
  tax_exemption_reason_code            -- ZATCA requires a reason for non-standard
  track_serial · track_batch · warranty_months
  lifecycle (active|inactive|discontinued)

variant                                -- the SKU
  product_id · sku · barcode
  attributes jsonb                     -- {"size":"L","color":"Black"}
  price_retail · price_wholesale · price_dealer · price_floor
  cost_standard · weight · image_url
  reorder_level · max_level
  UNIQUE (company_id, sku)
  UNIQUE (company_id, barcode) WHERE barcode IS NOT NULL
```

`price_floor` is enforced at the POS, not merely displayed. Blueprint B1: "the
lowest price a cashier is ever allowed to sell at, **even after discount** —
enforced by the system, not just policy."

`attributes` is JSONB rather than a fixed size/colour pair because blueprint B2
allows unlimited custom attributes (material, style, season, gender, model). A
GIN index supports the variant-matrix query.

### Inventory

```sql
warehouse            company_id · store_id · code · name · is_active

stock_movement       -- deltas, never levels. See 03-sync-idempotency §6.
  variant_id · warehouse_id
  delta numeric(18,4)                  -- −4, not "6"
  reason (sale|return|grn|adjust|transfer_in|transfer_out|wastage|opening|count)
  source_type · source_id · device_id
  occurred_at (device clock, ordering only) · recorded_at (server, authoritative)

cost_layer           -- FIFO layers; also carries WAC running state
  variant_id · warehouse_id · received_at
  qty_received · qty_remaining · unit_cost   -- unit_cost includes landed cost
  source_id
```

Stock level is **always** `SUM(delta)`, never a stored mutable number. Deltas
commute, so movements from several offline terminals produce the same total in
any arrival order.

### Sales

```sql
sales_invoice
  company_id · store_id · device_id
  uuid                                  -- assigned on the DEVICE at creation
  doc_type (standard|simplified|credit_note|debit_note)
  parent_invoice_id                     -- required for credit/debit notes
  customer_id · issued_at · issue_date
  currency · fx_rate
  subtotal_net · discount_total · vat_total · total_inclusive
  state                                 -- see 01-invoice-zatca-engine §3
  human_number                          -- INV-RYD-000123, separate from ICV
  UNIQUE (company_id, human_number)

sales_invoice_line
  invoice_id · line_no · variant_id
  qty · unit_price · line_discount
  invoice_discount_alloc                -- allocated AT SALE TIME, see below
  tax_category · vat_rate · vat_amount
  net_amount · cogs_amount              -- COGS captured at the moment of sale

sales_tender                            -- multi-tender split
  invoice_id · method (cash|mada|visa|mastercard|amex|apple_pay|stc_pay|
                       sadad|bank_transfer|cheque|tabby|tamara|
                       store_credit|loyalty|customer_due)
  amount · reference · settlement_status (pending|settled|reconciled)
```

**`invoice_discount_alloc` is written at sale time, not reconstructed at return
time.** Blueprint C14 names proportional discount allocation as "a common place
where cheaper POS software silently produces wrong numbers". Reconstructing the
allocation during a partial return drifts on rounding; storing it does not.

**Mada is its own tender value**, never folded into a generic "card". Blueprint
E3.1 requires it, because Mada's processing fee is materially lower and merging
them misstates margin per sale.

### ZATCA

```sql
zatca_invoice
  invoice_id · device_id · icv · pih · hash · stamp · qr_tlv · xml
  schema_version                        -- which ZATCA schema signed this
  state · signed_at · submitted_at · zatca_uuid · reject_reason
  retry_count · next_retry_at
  UNIQUE (device_id, icv)               -- gap and duplicate detection
```

### Accounting

Full design in `02-posting-engine.md`.

```sql
account          company_id · code · name · type · parent_id
                 is_control · control_of (AR|AP|INVENTORY)
fiscal_period    company_id · fiscal_year · period_no · state (open|closed|locked)
posting_rule     rule_key · version · lines jsonb · effective_from
journal_entry    company_id · period_id · entry_no · entry_date
                 source_type · source_id · rule_key · reverses_id
                 UNIQUE (source_type, source_id, rule_key)   -- idempotency
journal_line     entry_id · account_id · debit · credit · base_debit · base_credit
                 store_id · subledger_type · subledger_id
```

`UNIQUE (source_type, source_id, rule_key)` is what makes a sync retry safe: the
same invoice replayed cannot post twice.

---

## 5. Indexing

Driven by the access patterns that actually occur, not by guessing.

| Query | Index |
|---|---|
| Barcode scan at POS | `variant (company_id, barcode)` — but the POS reads **local SQLite**, so this serves the back office |
| Product search | `variant (company_id, sku)`, trigram on `product.name`, `product.name_ar` |
| Variant matrix | GIN on `variant.attributes` |
| Stock level | `stock_movement (variant_id, warehouse_id)` |
| FIFO consumption | `cost_layer (variant_id, warehouse_id, received_at) WHERE qty_remaining > 0` |
| Invoice list | `sales_invoice (company_id, issue_date DESC)` |
| ZATCA submit queue | `zatca_invoice (device_id, icv) WHERE state IN (...)` |
| Ledger | `journal_line (account_id)`, `journal_entry (company_id, entry_date)` |
| Audit | `audit_log (tenant_id, occurred_at DESC)`, `(entity_type, entity_id)` |

Every index carries `tenant_id` or a tenant-scoped ancestor as its leading
column where the table is large, so RLS filtering stays index-assisted rather
than falling back to a filter after the scan.

---

## 6. Local SQLite schema (POS terminal)

A deliberate subset. The terminal holds what it needs to sell with **zero
internet**, and nothing more.

```
product, variant           pulled snapshot (price, barcode, tax category)
stock_snapshot             pulled level + locally applied deltas
customer                   pulled, plus locally created walk-ins
pos_config, terminal_setting
regulatory_cache           rules WITH their effective-date ranges
local_invoice              authoritative until synced
local_invoice_line, local_tender
zatca_chain                terminal_id · last_icv · last_hash   ← the legal chain
sync_queue                 seq · uuid · payload · state
held_cart                  never synced
```

`regulatory_cache` stores full date ranges, not current values. An offline
terminal selling on 3 March must apply March's VAT rate even if its cache was
populated in January.

The CSID private key is **not** in this schema. It lives in the OS keystore.

---

## 7. Naming the things users see

| Concept | Column | Note |
|---|---|---|
| Human invoice number | `human_number` | Configurable per store/type/year |
| ZATCA counter | `icv` | Per device, never resets, never reused |
| Product name | `name`, `name_ar` | Both required for Saudi tenants |

Blueprint I3 warns precisely against confusing the first two: "the numbering
engine and the ZATCA ICV are related but not the same thing, and the design must
not let a 'friendly' custom invoice number break the mandatory tamper-evident
counter underneath it." They are separate columns produced by separate
components, and the numbering engine has no access to ICV allocation.

---

## 8. What is deliberately absent

| Not modelled | Why |
|---|---|
| `deleted_at` on invoices, journal entries, audit rows | Immutable by law. There is no delete path to soft-delete around. |
| A stored `stock_level` column | Levels are derived from movements; a stored level loses information when terminals sync out of order |
| `is_super_admin` on `app_user` | Super Admin is `tenant_id IS NULL` — a different plane, not a flag |
| A `settings` key-value bag | Settings with behaviour get typed columns and constraints. A bag defers the schema decision forever and cannot be validated. |
| BOM, work orders, WIP | Blueprint C3.1 puts full Manufacturing ERP explicitly out of scope for v1 |
