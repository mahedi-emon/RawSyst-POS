# Commission (C6 + C14 effect 7) — audit, 2026-09-03

Verified against code, not against prior claims. An earlier audit note said
"commission is never earned on a sale". That was **half right in a misleading
way**: `people.commissionFor` (internal/people/payroll.go:536) DOES compute
commission at payroll time by aggregating the period's sales, and
`computeSlip` calls it whenever `emp.commission_eligible` is set. The scheme
table `commission_rule` (0091) is rich: employee/store/category/brand/variant
scope, `basis` in (revenue, profit), flat `rate` or ascending `tiers` jsonb.

## The real finding: commission has never worked at all

`commissionFor` runs:

```sql
JOIN employee e ON e.user_id = i.created_by
```

**`sales_invoice` has no `created_by` column.** Its columns are: id, tenant_id,
company_id, store_id, device_id, uuid, doc_type, parent_invoice_id, issue_date,
issued_at, currency, fx_rate, subtotal_net, discount_total, tax_total,
total_inclusive, human_number, state, created_at, updated_at (0014), plus
cash_session_id / customer_id added later.

So the query errors and **payroll returns HTTP 500 for any commission-eligible
employee**. Not a wrong figure — a hard failure. It was never noticed because
every existing payroll test hires staff without `commission_eligible`, so
`commissionFor` is never reached.

Confirmed empirically: all 6 new tests in `internal/api/commission_test.go`
fail with `payroll: 500 internal`.

## Underlying gap: a sale does not record who made it

`sales.Sale.CashierID` is populated from the authenticated user in
`api/pos_handlers.go` (3 call sites) and used for journal `PostedBy` and audit,
but `writeInvoice` (internal/sales/finalize.go:669) never writes it to
`sales_invoice`. There is no column to write it to. Commission cannot be
attributed to a person until there is one.

## Three further defects in the same function, all reachable once it runs

1. **A return INCREASES commission.** The aggregation sums `sales_invoice_line`
   joined to `sales_invoice` with no `doc_type` filter. A credit note's lines
   carry POSITIVE `net_amount` (`returns.go:195` `credit.of(orig.NetAmount, ...)`)
   with direction held in the doc type. So selling 100 and refunding it in full
   pays commission on 200 — the exact inverse of C14 effect 7.
2. **Rule scope is ignored.** `store_id` is settable via
   `POST /api/v1/commission-rules` and is read only in the `ORDER BY` to rank
   candidate rules, never in a `WHERE` against the takings. A scheme given to
   one branch pays on the whole company. `category_id`/`brand_id`/`variant_id`
   exist in the table but no API writes them yet.
3. **Draft and cancelled invoices count.** No `state` filter. `sales_invoice`
   states include 'draft' and 'cancelled' (0014), so a sale that legally does
   not exist still earns commission.

## Plan

1. Migration: `sales_invoice.cashier_id uuid REFERENCES app_user(id)`, written
   by `writeInvoice` and `writeCreditNote` from `Sale.CashierID` / `Return`.
2. Rewrite `commissionFor`: join on the new column; net credit notes with a
   signed CASE on `doc_type`; filter by the rule's scope; exclude draft and
   cancelled.
3. Keep `rateFromTiers` as is — its "highest band reached applies to the whole
   amount" reading matches C6's example and is documented.

Tests live in `internal/api/commission_test.go` (6, currently all failing by
design).
