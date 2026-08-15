-- 0021 — Weighted average needs a pool, not layers.
--
-- The tie-out test found this: under FIFO the valuation matched the Inventory
-- control account exactly, and under weighted average it was out by 25 on the
-- first sale. Not a rounding drift — a modelling error.
--
-- # What went wrong
--
-- Layers of 10 at 50 and 10 at 60 hold 1,100 across 20 units, an average of 55.
-- Selling 15 charges COGS of 15 × 55 = 825, leaving the ledger at 275. But the
-- layers were drawn down oldest-first, so what remained was 5 units of the 60
-- layer, valued at 300. The ledger and the stock report disagreed by 25 and
-- both were internally consistent.
--
-- The mistake was forcing one storage model on two costing methods. Under FIFO
-- the identity of each receipt matters and layers are exactly right. Under
-- weighted average it does not: all stock is held at ONE pooled cost, which is
-- what "average" means — you stop tracking which physical unit came from which
-- receipt the moment you average them.
--
-- So weighted average gets a pool. Valuation then reads whatever the company's
-- method actually uses, and the two can no longer disagree.

CREATE TABLE stock_valuation (
  tenant_id    uuid NOT NULL REFERENCES tenant(id)    ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id)   ON DELETE CASCADE,
  variant_id   uuid NOT NULL REFERENCES variant(id)   ON DELETE RESTRICT,
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  qty_on_hand  numeric(18,4) NOT NULL DEFAULT 0,

  -- Total value is authoritative and the average is derived from it, not the
  -- other way round. Holding the average and multiplying would round twice —
  -- once storing it, once using it — and the second rounding is what breaks
  -- the tie-out.
  total_value  numeric(18,4) NOT NULL DEFAULT 0,

  updated_at   timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (variant_id, warehouse_id),
  CONSTRAINT stock_valuation_value_sign CHECK (
    -- Value and quantity move together. Positive stock with negative value, or
    -- the reverse, is a sign the pool has been updated inconsistently.
    (qty_on_hand > 0 AND total_value >= 0)
    OR (qty_on_hand = 0 AND total_value = 0)
    OR (qty_on_hand < 0)
  )
);

CREATE INDEX stock_valuation_tenant_idx  ON stock_valuation (tenant_id);
CREATE INDEX stock_valuation_company_idx ON stock_valuation (company_id);

CREATE TRIGGER stock_valuation_touch BEFORE UPDATE ON stock_valuation
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE stock_valuation ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_valuation FORCE  ROW LEVEL SECURITY;
CREATE POLICY stock_valuation_isolation ON stock_valuation
  USING (tenant_id = current_tenant_id());

-- The current average, derived. NULL when there is no stock, because the
-- average of nothing is not zero — it is undefined, and returning zero would
-- let a sale from empty stock book a cost of nothing.
CREATE OR REPLACE FUNCTION weighted_average_cost(p_variant_id uuid, p_warehouse_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT CASE WHEN qty_on_hand > 0 THEN round(total_value / qty_on_hand, 4) END
  FROM stock_valuation
  WHERE variant_id = p_variant_id AND warehouse_id = p_warehouse_id
$$;

-- ---------------------------------------------------------------------------
-- Valuation now follows the company's method
-- ---------------------------------------------------------------------------

-- Replaces the layers-only version from 0020, which was only ever correct for
-- FIFO. Standard costing values at the layers too: its variance is booked at
-- issue, so what remains is held at what it actually cost.
CREATE OR REPLACE FUNCTION inventory_valuation(p_company_id uuid)
RETURNS numeric
LANGUAGE plpgsql STABLE AS $$
DECLARE
  method text;
  total  numeric(18,4);
BEGIN
  SELECT costing_method::text INTO method FROM company WHERE id = p_company_id;

  IF method = 'wac' THEN
    SELECT coalesce(sum(total_value), 0) INTO total
    FROM stock_valuation WHERE company_id = p_company_id;
  ELSE
    SELECT coalesce(sum(qty_remaining * unit_cost), 0) INTO total
    FROM cost_layer WHERE company_id = p_company_id AND qty_remaining > 0;
  END IF;

  RETURN total;
END;
$$;
