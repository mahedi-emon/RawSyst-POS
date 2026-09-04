-- A goods-in clerk could not be told which lines need a lot number.
--
-- `variant.tracks_batches` has existed since 0107 and is read by exactly one
-- query, in internal/inventory/batch.go. No API payload carries it. So a
-- receiving screen has no way to know that line four is a batch-tracked
-- product and lines one to three are not.
--
-- `inventory.Receive` requires a batch number for a tracked variant and
-- refuses one for anything else, which is right — but the only way a screen
-- can discover which is which is to submit the delivery and read the error.
-- That means typing a whole pallet, pressing save, and being told. The comment
-- on ReceiptLine.BatchNo says a clerk is "told at the point of receiving
-- rather than discovering it when the stock will not sell", and that is true;
-- this is what lets them be told at the point of TYPING instead.
--
-- Added to po_outstanding because the receiving screen iterates purchase order
-- lines: it is the one place that already asks "what is still due on this
-- order", and the answer is more useful when it also says what has to be
-- recorded about it.

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
  gross_amount   numeric,
  tracks_batches boolean
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
    l.net_amount, l.tax_amount, l.gross_amount,
    coalesce(v.tracks_batches, false)
  FROM po_line l
  LEFT JOIN variant v ON v.id = l.variant_id
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
