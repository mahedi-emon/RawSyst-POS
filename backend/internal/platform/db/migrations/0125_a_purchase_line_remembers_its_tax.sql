-- A purchase order line could not say how it was taxed.
--
-- `po_line` has stored `tax_treatment` and `tax_rate` since 0031, and
-- `CreateOrder` writes both — defaulting an empty treatment to 'standard'. But
-- `po_outstanding` never returned either, and it is the only read behind
-- `GET /purchasing/orders/{id}`. So `OrderLineView.TaxTreatment` was a field in
-- the API contract that was empty on every line of every order, always.
--
-- Two things break on that, and the second one silently.
--
-- A zero-rated or exempt line looks exactly like a standard one, on the order
-- and on the receiving screen — which is a question somebody asks about a
-- purchase from abroad, and the answer was not there to give.
--
-- Worse: `PUT /purchasing/orders/{id}` rewrites a draft's lines wholesale, so
-- an editor has to send back what it read. Reading an empty treatment and no
-- rate, it would send an empty treatment and no rate, and CreateOrder's own
-- default would quietly turn every line of an edited draft into a standard one
-- at zero per cent. Editing the delivery date would have changed the tax.
--
-- The signature changes, so this is a drop and a create rather than a replace.
-- Nothing else calls it: `outstandingLines` in internal/purchasing is the only
-- reader, and it is updated alongside this.

DROP FUNCTION IF EXISTS po_outstanding(uuid);

CREATE FUNCTION po_outstanding(p_po_id uuid)
RETURNS TABLE (
  po_line_id     uuid,
  line_no        integer,
  variant_id     uuid,
  description    text,
  qty_ordered    numeric,
  qty_received   numeric,
  qty_outstanding numeric,
  qty_billed     numeric,
  unit_cost      numeric,
  tax_treatment  text,
  tax_rate       numeric,
  net_amount     numeric,
  tax_amount     numeric,
  gross_amount   numeric
)
LANGUAGE sql STABLE AS $$
  SELECT
    l.id, l.line_no, l.variant_id, l.description,
    l.qty_ordered,
    coalesce(r.received, 0),
    -- Never negative. Over-receiving is a real event — a supplier sends a
    -- bonus case — and reporting "-2 outstanding" would be arithmetic rather
    -- than an answer to the question the receiving screen is asking.
    greatest(l.qty_ordered - coalesce(r.received, 0), 0),
    coalesce(b.billed, 0),
    l.unit_cost, l.tax_treatment, l.tax_rate,
    l.net_amount, l.tax_amount, l.gross_amount
  FROM po_line l
  LEFT JOIN (
    SELECT gl.po_line_id, sum(gl.qty_received - gl.qty_rejected) AS received
    FROM grn_line gl GROUP BY gl.po_line_id
  ) r ON r.po_line_id = l.id
  LEFT JOIN (
    SELECT bl.po_line_id, sum(bl.qty_billed) AS billed
    FROM bill_line bl
    JOIN purchase_bill pb ON pb.id = bl.bill_id AND pb.status <> 'cancelled'
    WHERE bl.po_line_id IS NOT NULL
    GROUP BY bl.po_line_id
  ) b ON b.po_line_id = l.id
  WHERE l.po_id = p_po_id
  ORDER BY l.line_no;
$$;
