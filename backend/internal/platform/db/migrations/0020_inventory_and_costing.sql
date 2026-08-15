-- 0020 — Inventory movements and the costing engine.
--
-- Closes the loop on a sale: until now an invoice posted revenue and a COGS
-- figure the caller supplied, while stock never moved. Two things follow from
-- that gap, and both are correctness problems rather than missing features.
--
--   * C13's hard invariant — "inventory valuation report must always tie
--     exactly to the Inventory account balance in the General Ledger" — could
--     not even be evaluated, because there was no valuation.
--   * COGS was whatever the till claimed. Gross profit was therefore an
--     assertion, not a measurement.

-- ---------------------------------------------------------------------------
-- Warehouse
-- ---------------------------------------------------------------------------

CREATE TABLE warehouse (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  -- NULL for a central warehouse serving every branch.
  store_id    uuid REFERENCES store(id) ON DELETE RESTRICT,

  code        text NOT NULL,
  name        text NOT NULL,
  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT warehouse_code_format CHECK (code ~ '^[A-Z0-9-]{1,16}$')
);

CREATE UNIQUE INDEX warehouse_code_uq ON warehouse (company_id, code);
CREATE INDEX warehouse_tenant_idx ON warehouse (tenant_id);

ALTER TABLE warehouse ENABLE ROW LEVEL SECURITY;
ALTER TABLE warehouse FORCE  ROW LEVEL SECURITY;
CREATE POLICY warehouse_isolation ON warehouse USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Movements, never levels
-- ---------------------------------------------------------------------------
--
-- Stock is the sum of its movements. There is deliberately no stored level
-- column, because a level loses information when several offline terminals
-- sync out of order: three tills each selling 4 units produce −12 in any
-- arrival order, whereas three absolute levels overwrite each other and two of
-- the three sales vanish.
CREATE TABLE stock_movement (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)    ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id)   ON DELETE CASCADE,
  variant_id   uuid NOT NULL REFERENCES variant(id)   ON DELETE RESTRICT,
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  -- Signed. −4 for a sale of four, +4 for its return.
  delta        numeric(18,4) NOT NULL,

  reason       text NOT NULL,
  source_type  text,
  source_id    uuid,
  device_id    uuid REFERENCES device(id) ON DELETE SET NULL,

  -- The value this movement carried, so the ledger and the stock agree without
  -- recomputing history. Positive for a receipt, negative for an issue.
  value_delta  numeric(18,4),

  -- The device clock orders movements from one terminal; the server clock is
  -- what any report uses, because terminal clocks drift and can be wrong by
  -- hours after a power cut.
  occurred_at  timestamptz NOT NULL DEFAULT now(),
  recorded_at  timestamptz NOT NULL DEFAULT now(),

  note         text,

  CONSTRAINT stock_movement_delta_non_zero CHECK (delta <> 0),
  CONSTRAINT stock_movement_reason_valid CHECK (reason IN (
    'sale', 'return', 'grn', 'adjustment', 'transfer_in', 'transfer_out',
    'wastage', 'opening', 'count', 'internal_use')),
  -- Blueprint B4 requires a mandatory reason on wastage, and an adjustment
  -- without an explanation is how shrinkage gets buried.
  CONSTRAINT stock_movement_explained CHECK (
    reason NOT IN ('wastage', 'adjustment') OR (note IS NOT NULL AND length(btrim(note)) >= 3))
);

CREATE INDEX stock_movement_level_idx ON stock_movement (variant_id, warehouse_id);
CREATE INDEX stock_movement_tenant_idx ON stock_movement (tenant_id);
CREATE INDEX stock_movement_source_idx ON stock_movement (source_type, source_id)
  WHERE source_id IS NOT NULL;

-- A movement is a fact about what happened. Correcting one means a new,
-- opposite movement, exactly as correcting a journal entry means a reversal.
CREATE TRIGGER stock_movement_immutable
  BEFORE UPDATE OR DELETE ON stock_movement
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE stock_movement ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_movement FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_movement_isolation ON stock_movement
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Cost layers
-- ---------------------------------------------------------------------------
--
-- FIFO consumes these oldest-first. Weighted average reads them as a whole.
-- Standard cost ignores them for valuation and books the difference to
-- variance. One table serves all three so switching method does not mean
-- migrating data.
CREATE TABLE cost_layer (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)    ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id)   ON DELETE CASCADE,
  variant_id   uuid NOT NULL REFERENCES variant(id)   ON DELETE RESTRICT,
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  received_at  timestamptz NOT NULL DEFAULT now(),
  qty_received numeric(18,4) NOT NULL,
  qty_remaining numeric(18,4) NOT NULL,

  -- INCLUDES allocated landed cost. Blueprint B4: purchase plus shipping plus
  -- customs, so P&L reflects true product cost. Import VAT is deliberately not
  -- in here — E2.5 puts duty in inventory cost and import VAT in recoverable
  -- input tax, and mixing them overstates stock while understating the reclaim.
  unit_cost    numeric(18,4) NOT NULL,

  source_type  text,
  source_id    uuid,

  CONSTRAINT cost_layer_qty_positive CHECK (qty_received > 0),
  CONSTRAINT cost_layer_remaining_sane CHECK (
    qty_remaining >= 0 AND qty_remaining <= qty_received),
  CONSTRAINT cost_layer_cost_non_negative CHECK (unit_cost >= 0)
);

-- The consumption query: oldest open layer first.
CREATE INDEX cost_layer_fifo_idx
  ON cost_layer (variant_id, warehouse_id, received_at)
  WHERE qty_remaining > 0;
CREATE INDEX cost_layer_tenant_idx ON cost_layer (tenant_id);

CREATE TRIGGER cost_layer_no_delete BEFORE DELETE ON cost_layer
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

-- qty_remaining moves as stock is consumed; everything describing the receipt
-- itself is fixed.
CREATE TRIGGER cost_layer_frozen_receipt
  BEFORE UPDATE ON cost_layer
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'variant_id', 'warehouse_id', 'received_at', 'qty_received', 'unit_cost');

ALTER TABLE cost_layer ENABLE ROW LEVEL SECURITY;
ALTER TABLE cost_layer FORCE  ROW LEVEL SECURITY;
CREATE POLICY cost_layer_isolation ON cost_layer
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Reading stock
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION stock_on_hand(p_variant_id uuid, p_warehouse_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(delta), 0)
  FROM stock_movement
  WHERE variant_id = p_variant_id AND warehouse_id = p_warehouse_id
$$;

-- Valuation from the open layers. This is the figure C13 requires to tie
-- exactly to the Inventory control account.
CREATE OR REPLACE FUNCTION inventory_valuation(p_company_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(qty_remaining * unit_cost), 0)
  FROM cost_layer
  WHERE company_id = p_company_id AND qty_remaining > 0
$$;

-- The invariant, as one query. C13: "any divergence is flagged as an
-- exception" — so the nightly job, the acceptance test and a support engineer
-- all ask it the same way.
CREATE OR REPLACE FUNCTION inventory_gl_difference(p_company_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT inventory_valuation(p_company_id) - coalesce((
    SELECT sum(l.base_debit - l.base_credit)
    FROM journal_line l
    JOIN account a ON a.id = l.account_id
    WHERE a.company_id = p_company_id AND a.is_control AND a.control_of = 'inventory'
  ), 0)
$$;
