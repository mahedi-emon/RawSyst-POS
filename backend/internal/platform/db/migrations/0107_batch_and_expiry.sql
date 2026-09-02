-- 0107 — Batch / lot / expiry tracking (blueprint B4, B1).
--
-- B4 asks for "Batch / Lot / Expiry tracking (for cosmetics/grocery-type
-- inventory where applicable): batch number, manufacture date, expiry date,
-- quantity, supplier, cost; automatic Expiring Soon / Expired / Batch Recall
-- alerts". B1 makes it a per-product flag, beside serial tracking.
--
-- None of it existed. `stock_serial` (0088) has tracked individual units since
-- the after-sales work, and the lot-level equivalent — the one a grocery or a
-- pharmacy actually runs on — had no column, table, service or route.
--
-- # Two axes, deliberately kept apart
--
-- A batch answers WHICH PHYSICAL LOT left the shelf. Costing answers WHAT VALUE
-- left the books. They are not the same question and this product already has a
-- careful answer to the second: FIFO consumes identifiable `cost_layer` rows,
-- weighted average consumes a pooled `stock_valuation` row, and C13's tie-out
-- holds exactly for both.
--
-- So batches do NOT become a third costing method. `Consume` still values an
-- issue by the company's costing method, and the batch layer records which lot
-- the goods came out of. Making batch selection drive valuation would silently
-- convert every batch-tracked company to specific-identification costing —
-- changing their reported margin and breaking the tie-out the acceptance tests
-- prove.
--
-- The batch still carries its own `unit_cost` because B4 lists cost among the
-- things a batch records, and because a recall needs to know what the lot was
-- worth. It is recorded, not used for valuation.
--
-- # Selection is FEFO, not FIFO
--
-- First Expired, First Out. For perishable stock the received order is the
-- wrong order: a carton received today expiring next week must leave before one
-- received last month expiring next year, or the shop sells the wrong one and
-- writes off the other. Batches with no expiry date fall back to received
-- order, which is FIFO by another name.
--
-- # Why the split is recorded per movement
--
-- One issue can span several batches — FEFO takes what is left of one lot and
-- continues into the next — so a `batch_id` column on `stock_movement` could
-- not represent it. `stock_batch_movement` records the split, and that is what
-- makes a recall answerable: given a batch, which invoices took it, and
-- therefore which customers have to be telephoned.

ALTER TABLE variant
  ADD COLUMN tracks_batches boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN variant.tracks_batches IS
  'B1''s batch/expiry tracking flag. When true, every receipt of this variant '
  'must name a batch and every issue is allocated across batches by expiry.';

-- ---------------------------------------------------------------------------
-- The lot
-- ---------------------------------------------------------------------------

CREATE TABLE stock_batch (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)    ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id)   ON DELETE CASCADE,
  variant_id   uuid NOT NULL REFERENCES variant(id)   ON DELETE RESTRICT,
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  -- The supplier's own lot number, as printed on the carton. Never generated
  -- here: an invented lot number cannot be matched against a recall notice,
  -- which is the one moment this column has to work.
  batch_no     text NOT NULL,

  manufactured_on date,
  expires_on      date,

  qty_received  numeric(18,4) NOT NULL,
  qty_remaining numeric(18,4) NOT NULL,

  -- What the lot cost, per B4. Recorded for recall and for reporting; the
  -- company's costing method still decides what a sale is valued at. See the
  -- note at the top.
  unit_cost    numeric(18,4),

  supplier_id  uuid REFERENCES supplier(id) ON DELETE SET NULL,
  source_type  text,
  source_id    uuid,

  received_at  timestamptz NOT NULL DEFAULT now(),

  -- A recall takes the lot out of circulation without deleting it: the history
  -- of what was already sold from it is the whole point.
  recalled_at   timestamptz,
  recall_reason text,

  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT stock_batch_no_not_blank CHECK (btrim(batch_no) <> ''),
  CONSTRAINT stock_batch_qty_positive CHECK (qty_received > 0),
  -- Remaining never exceeds received and never goes below zero. A batch that
  -- issued more than it held would mean the FEFO allocator double-counted a
  -- lot, and the error belongs where it happens rather than in a later report.
  CONSTRAINT stock_batch_remaining_in_range
    CHECK (qty_remaining >= 0 AND qty_remaining <= qty_received),
  CONSTRAINT stock_batch_expiry_after_manufacture
    CHECK (expires_on IS NULL OR manufactured_on IS NULL
           OR expires_on > manufactured_on),
  CONSTRAINT stock_batch_recall_has_a_reason
    CHECK (recalled_at IS NULL OR btrim(coalesce(recall_reason, '')) <> '')
);

-- One lot number per variant per warehouse. A second delivery of the same lot
-- adds to the batch rather than creating a rival row, which is what keeps
-- "how much of lot 24B is left" answerable with one number.
CREATE UNIQUE INDEX stock_batch_identity_uq
  ON stock_batch (variant_id, warehouse_id, batch_no);

-- The FEFO read: earliest expiry first, nulls last, then oldest first.
CREATE INDEX stock_batch_fefo_idx
  ON stock_batch (variant_id, warehouse_id, expires_on NULLS LAST, received_at)
  WHERE qty_remaining > 0;

CREATE INDEX stock_batch_expiring_idx
  ON stock_batch (company_id, expires_on)
  WHERE qty_remaining > 0 AND expires_on IS NOT NULL;

CREATE TRIGGER stock_batch_touch BEFORE UPDATE ON stock_batch
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE stock_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_batch FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_batch_isolation ON stock_batch
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Which lot each movement took
-- ---------------------------------------------------------------------------
--
-- The traceability record. Signed like the movement it belongs to: negative
-- when the lot left, positive when a return put some of it back.
--
-- This is what answers a recall. Given a batch, the movements that touched it
-- name their invoices, and those name their customers.
CREATE TABLE stock_batch_movement (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  batch_id    uuid NOT NULL REFERENCES stock_batch(id)    ON DELETE RESTRICT,
  movement_id uuid NOT NULL REFERENCES stock_movement(id) ON DELETE RESTRICT,

  qty numeric(18,4) NOT NULL,

  created_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT stock_batch_movement_qty_not_zero CHECK (qty <> 0)
);

CREATE INDEX stock_batch_movement_batch_idx ON stock_batch_movement (batch_id);
CREATE INDEX stock_batch_movement_movement_idx
  ON stock_batch_movement (movement_id);

ALTER TABLE stock_batch_movement ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_batch_movement FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_batch_movement_isolation ON stock_batch_movement
  USING (tenant_id = current_tenant_id());

-- A batch record is history once anything has moved against it, the same way a
-- stock movement is. Correcting a lot number is an adjustment, not an edit.
CREATE TRIGGER stock_batch_movement_no_delete
  BEFORE DELETE ON stock_batch_movement
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------
--
-- Reading a batch rides on inventory.view: a cashier asked "when does this
-- expire" needs the answer, and it is the same stock they can already see.
-- Recalling one is inventory.adjust_stock, because it takes sellable goods out
-- of circulation.
-- The templates first, so a tenant provisioned after this migration inherits
-- the verb from the clone.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',         'inventory.recall_batch'),
  ('store_manager', 'inventory.recall_batch')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

-- Then every tenant that already exists. 0090 records why this loop is needed:
-- a template grant reaches only the clones made after it, so tenants
-- provisioned before this migration would never receive the verb.
DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    INSERT INTO role_permission (role_id, permission)
    SELECT r.id, p.permission
    FROM role r
    JOIN role template ON template.id = r.cloned_from
    JOIN (VALUES
      ('owner',         'inventory.recall_batch'),
      ('store_manager', 'inventory.recall_batch')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;

INSERT INTO permission_catalogue
  (permission, section, label, label_ar, label_bn, caution, sort_order)
VALUES
  ('inventory.recall_batch', 'inventory',
   'Withdraw a batch from sale and see who bought it',
   'سحب دفعة من البيع ومعرفة من اشتراها',
   'একটি ব্যাচ বিক্রি থেকে প্রত্যাহার করা ও কে কিনেছে তা দেখা',
   'Takes sellable stock out of circulation.', 150)
ON CONFLICT DO NOTHING;
