-- 0047 — the cost of selling stock that was not there, and its correction.
--
-- Blueprint C13 allows a company to sell below zero (negative_stock_policy =
-- allow_warn) and says what that costs: "cost is provisional and auto-corrected
-- on the next receipt of that item". The first half was built — the costing
-- engine values the uncovered units at the best estimate it has and reports the
-- shortfall — and the second half was not. Nothing recorded which units were
-- provisional, so nothing could ever revisit them. A shop that regularly sells
-- ahead of its paperwork carried a permanently wrong cost of goods sold.
--
-- # Why a table rather than a negative layer
--
-- The obvious modelling is a cost layer with a negative qty_remaining. It is
-- forbidden by cost_layer_remaining_sane, and deliberately: a layer is a
-- receipt, and a receipt of minus three units is not a fact about anything that
-- happened. It would also put negative rows in the path of every FIFO
-- consumption query, which reads qty_remaining > 0 and would silently skip
-- them.
--
-- So the uncovered quantity is its own record, which is also what the exception
-- report C13 asks for needs to read.
--
-- # Why the valuation has to change with it
--
-- This is the part that was quietly broken. C13's hard invariant is that the
-- stock valuation ties EXACTLY to the Inventory control account. Sell 5 units
-- from a shelf holding 2 at 50 and the ledger is credited 250 — all five units,
-- because that is what was charged to cost of goods sold — while the layers
-- drop to zero and value nothing. The valuation said 0, the ledger said −150,
-- and inventory_gl_difference reported a divergence of 150 that no report could
-- explain and no later sale would clear.
--
-- Carrying the uncovered units as a negative holding at the cost they were
-- charged out at makes the two agree again, and keeps them agreeing through the
-- correction: settling a shortfall removes its deduction and draws the same
-- quantity out of the new layer, so the valuation moves by exactly the variance
-- the journal posts. See SettleShortfalls in the inventory package.

CREATE TABLE cost_shortfall (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)    ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id)   ON DELETE CASCADE,
  variant_id   uuid NOT NULL REFERENCES variant(id)   ON DELETE RESTRICT,
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  occurred_at  timestamptz NOT NULL DEFAULT now(),

  -- The units the sale took that no layer and no pool covered.
  qty          numeric(18,4) NOT NULL,

  -- How many of those have since been settled against a receipt. Partial
  -- settlement is the normal case: a shop 30 units short that receives 10 has
  -- corrected 10 of them and still owes 20.
  qty_settled  numeric(18,4) NOT NULL DEFAULT 0,

  -- What the engine charged for the uncovered units: the last known layer cost
  -- under FIFO, the pool's average under weighted average, the standard cost
  -- under standard costing. Held per unit rather than as a total because the
  -- settlement is per unit and a total would have to be re-divided.
  provisional_unit_cost numeric(18,4) NOT NULL,

  -- Which sale sold short, and from which till, so the exception report can
  -- name both rather than only the variant.
  source_type  text,
  source_id    uuid,
  device_id    uuid REFERENCES device(id) ON DELETE SET NULL,

  -- Set only when the last uncovered unit has been settled.
  settled_at   timestamptz,

  -- The cumulative correction posted for this shortfall: actual cost less
  -- provisional cost, over the units settled so far. Positive means the goods
  -- turned out to cost more than the estimate and margin was overstated.
  adjustment   numeric(18,4) NOT NULL DEFAULT 0,

  CONSTRAINT cost_shortfall_qty_positive CHECK (qty > 0),
  CONSTRAINT cost_shortfall_settled_sane CHECK (
    qty_settled >= 0 AND qty_settled <= qty),
  CONSTRAINT cost_shortfall_cost_non_negative CHECK (provisional_unit_cost >= 0),
  -- Closed exactly when nothing is left uncovered. Without this a row could be
  -- stamped settled while still deducting from the valuation, or fully settled
  -- and still open, and either way the correction would run twice.
  CONSTRAINT cost_shortfall_closed_when_settled CHECK (
    (qty_settled < qty AND settled_at IS NULL)
    OR (qty_settled = qty AND settled_at IS NOT NULL))
);

-- The settlement query: the open shortfalls for one variant in one warehouse,
-- oldest first, because the oldest uncovered sale is the one a receipt corrects
-- first.
CREATE INDEX cost_shortfall_open_idx
  ON cost_shortfall (variant_id, warehouse_id, occurred_at)
  WHERE qty_settled < qty;
CREATE INDEX cost_shortfall_tenant_idx  ON cost_shortfall (tenant_id);
CREATE INDEX cost_shortfall_company_idx ON cost_shortfall (company_id);

-- A shortfall is a fact about a sale. Settling it moves qty_settled, settled_at
-- and adjustment; everything describing what happened is fixed, for the same
-- reason a stock movement is immutable.
CREATE TRIGGER cost_shortfall_frozen_facts
  BEFORE UPDATE ON cost_shortfall
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'tenant_id', 'company_id', 'variant_id', 'warehouse_id', 'occurred_at',
    'qty', 'provisional_unit_cost', 'source_type', 'source_id');

CREATE TRIGGER cost_shortfall_no_delete BEFORE DELETE ON cost_shortfall
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE cost_shortfall ENABLE ROW LEVEL SECURITY;
ALTER TABLE cost_shortfall FORCE  ROW LEVEL SECURITY;
CREATE POLICY cost_shortfall_isolation ON cost_shortfall
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The valuation now carries the negative position
-- ---------------------------------------------------------------------------
--
-- Replaces 0021's version, which was correct for every case except the one
-- C13 explicitly permits. The method branch is unchanged: what is new is that
-- units sold but never held are deducted at the cost they were charged out at,
-- which is precisely the amount by which the ledger was credited for stock
-- that did not exist.
CREATE OR REPLACE FUNCTION inventory_valuation(p_company_id uuid)
RETURNS numeric
LANGUAGE plpgsql STABLE AS $$
DECLARE
  method    text;
  total     numeric(18,4);
  uncovered numeric(18,4);
BEGIN
  SELECT costing_method::text INTO method FROM company WHERE id = p_company_id;

  IF method = 'wac' THEN
    SELECT coalesce(sum(total_value), 0) INTO total
    FROM stock_valuation WHERE company_id = p_company_id;
  ELSE
    SELECT coalesce(sum(qty_remaining * unit_cost), 0) INTO total
    FROM cost_layer WHERE company_id = p_company_id AND qty_remaining > 0;
  END IF;

  SELECT coalesce(sum((qty - qty_settled) * provisional_unit_cost), 0)
    INTO uncovered
  FROM cost_shortfall
  WHERE company_id = p_company_id AND qty_settled < qty;

  RETURN total - uncovered;
END;
$$;
