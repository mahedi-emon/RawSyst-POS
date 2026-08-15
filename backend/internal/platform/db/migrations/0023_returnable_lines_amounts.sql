-- 0023 — returnable_lines() must report money, not only quantity.
--
-- The original returned quantities and the line's own figures, which is enough
-- to answer "how many are left" but not "how much is left". Those are different
-- questions once partial returns are involved.
--
-- A line of 3 with a net of 100.00 returned one at a time gives 33.33, 33.33
-- and — because whichever return exhausts the line takes the remainder — 33.34.
-- Computing the third from the quantity alone yields 33.33 and the business
-- quietly keeps a hallala on every three-way split. ComputeReturn already
-- applies the remainder rule, but it can only do so if it is told what has
-- already gone back in money terms, not just in units.
--
-- So the function now reports both sides, and the credit note lines are the
-- source for both. The same rows the trigger counts to refuse an over-return
-- are the rows counted here, which is what keeps the application and the
-- database from disagreeing about the same invoice.

DROP FUNCTION IF EXISTS returnable_lines(uuid);

CREATE FUNCTION returnable_lines(p_invoice_id uuid)
RETURNS TABLE (
  line_id                 uuid,
  line_no                 integer,
  variant_id              uuid,
  description             text,

  qty_sold                numeric,
  qty_returned            numeric,
  qty_returnable          numeric,

  unit_price              numeric,
  tax_treatment           text,
  tax_rate                numeric,

  -- What the line was sold for.
  net_amount              numeric,
  tax_amount              numeric,
  line_discount           numeric,
  invoice_discount_alloc  numeric,
  cogs_amount             numeric,

  -- What has already gone back, in money. Without these a later partial return
  -- cannot know what remainder it owes the customer.
  net_returned            numeric,
  tax_returned            numeric,
  discount_alloc_returned numeric,
  cogs_returned           numeric
)
LANGUAGE sql STABLE AS $$
  SELECT l.id, l.line_no, l.variant_id, l.description,
         abs(l.qty),
         coalesce(r.qty, 0),
         abs(l.qty) - coalesce(r.qty, 0),
         l.unit_price, l.tax_treatment, l.tax_rate,
         l.net_amount, l.tax_amount, l.line_discount, l.invoice_discount_alloc,
         coalesce(l.cogs_amount, 0),
         coalesce(r.net, 0),
         coalesce(r.tax, 0),
         coalesce(r.discount_alloc, 0),
         coalesce(r.cogs, 0)
  FROM sales_invoice_line l
  LEFT JOIN (
    SELECT reverses_line_id,
           sum(abs(qty))                     AS qty,
           sum(net_amount)                   AS net,
           sum(tax_amount)                   AS tax,
           sum(invoice_discount_alloc)       AS discount_alloc,
           sum(coalesce(cogs_amount, 0))     AS cogs
    FROM sales_invoice_line
    WHERE reverses_line_id IS NOT NULL
    GROUP BY reverses_line_id
  ) r ON r.reverses_line_id = l.id
  WHERE l.invoice_id = p_invoice_id
  ORDER BY l.line_no
$$;
