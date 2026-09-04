-- 0123 — light production cost tracking (blueprint C3.1).
--
-- A garment retailer buys cloth, has it stitched, packs it, and sells a shirt.
-- Without this the cloth leaves stock as a write-off, the stitching is an
-- expense, the packaging is another expense, and the shirt appears in stock at
-- a cost somebody guessed — so the margin on every locally-made item is wrong
-- and nobody can say by how much.
--
-- C3.1: "purchase of raw materials for in-house production, stitching/tailoring
-- labour cost, and packaging cost — recorded and allocated per production
-- batch, so the true cost of a locally-made item is known and flows correctly
-- into COGS and margin."
--
-- # The scope boundary, quoted because it is the whole design
--
-- C3.1 again: "this is COST TRACKING, not a manufacturing module. Full
-- manufacturing ERP — Bill of Materials, Production Orders, Work Orders,
-- Material Issue, WIP tracking, by-products, routing, capacity planning,
-- production variance analysis — is deliberately OUT OF SCOPE for v1."
--
-- So there is no BOM here, no work order, no routing and no WIP account. A
-- batch names what went in and what came out, and the arithmetic between them
-- is a division. Anything more would be the module the Blueprint says not to
-- build.
--
-- # What a batch is
--
--   inputs   — stock consumed, costed by the company's own method
--   costs    — stitching, packaging: money, not stock
--   output   — finished units, received into stock at
--              (input cost + added cost) / quantity
--
-- The value already in inventory moves; the added cost enters it. So the
-- Inventory account rises by exactly the labour and packaging, which is the
-- true statement about what the shop now owns.

-- ---------------------------------------------------------------------------
-- Two new movement reasons
-- ---------------------------------------------------------------------------
--
-- 0020's list has no word for this. Components leaving to be made into
-- something are not 'wastage' and not 'internal_use' — they became another
-- item, and the stock card has to say so or a shrinkage investigation starts by
-- explaining away every production run. Finished units arriving are not 'grn'
-- either: nobody delivered them.
--
-- So the movement carries its own reason at each end, which is what makes the
-- stock card readable: "20 m cloth out — production PRD-000001" against
-- "10 shirts in — production PRD-000001".

ALTER TABLE stock_movement DROP CONSTRAINT stock_movement_reason_valid;
ALTER TABLE stock_movement
  ADD CONSTRAINT stock_movement_reason_valid CHECK (reason IN (
    'sale', 'return', 'grn', 'adjustment', 'transfer_in', 'transfer_out',
    'wastage', 'opening', 'count', 'internal_use',
    'production_in', 'production_out'));

CREATE TABLE production_batch (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  uuid        uuid NOT NULL,
  batch_no    text NOT NULL,

  -- What is being made, and where the stock moves.
  variant_id   uuid NOT NULL REFERENCES variant(id)   ON DELETE RESTRICT,
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  qty_produced numeric(18,4) NOT NULL,

  -- The three figures C3.1 names, kept apart because "what did the stitching
  -- cost us" is a question an owner asks and a single total cannot answer.
  material_cost  numeric(18,4) NOT NULL DEFAULT 0,
  labour_cost    numeric(18,4) NOT NULL DEFAULT 0,
  packaging_cost numeric(18,4) NOT NULL DEFAULT 0,
  total_cost     numeric(18,4) NOT NULL DEFAULT 0,
  unit_cost      numeric(18,4) NOT NULL DEFAULT 0,

  currency    text NOT NULL,

  -- Where the labour and packaging were paid from. The same two roles expenses
  -- use, for the same reason: every company has one of each and the chart
  -- already maps them.
  paid_from   text NOT NULL,

  produced_on date NOT NULL,
  note        text,

  journal_entry_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT production_batch_qty_positive CHECK (qty_produced > 0),
  CONSTRAINT production_batch_costs_non_negative CHECK (
    material_cost >= 0 AND labour_cost >= 0 AND packaging_cost >= 0
    AND total_cost >= 0 AND unit_cost >= 0),
  -- The arithmetic, enforced rather than trusted: a batch whose parts do not
  -- add to its total is a batch whose unit cost is wrong, and the unit cost is
  -- the whole reason this table exists.
  CONSTRAINT production_batch_total_adds_up CHECK (
    total_cost = material_cost + labour_cost + packaging_cost),
  CONSTRAINT production_batch_paid_from_known
    CHECK (paid_from IN ('cash', 'bank')),
  CONSTRAINT production_batch_currency_upper CHECK (currency = upper(currency))
);

CREATE UNIQUE INDEX production_batch_no_uq
  ON production_batch (company_id, batch_no);
-- The idempotency key: a retry finds the batch it already made.
CREATE UNIQUE INDEX production_batch_uuid_uq
  ON production_batch (company_id, uuid);
CREATE INDEX production_batch_tenant_idx ON production_batch (tenant_id);
CREATE INDEX production_batch_variant_idx
  ON production_batch (company_id, variant_id, produced_on DESC);

ALTER TABLE production_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE production_batch FORCE  ROW LEVEL SECURITY;
CREATE POLICY production_batch_isolation ON production_batch
  USING (tenant_id = current_tenant_id());

-- A batch that has been costed is history: its unit cost is carried by every
-- unit it put into stock, and changing it afterwards would restate the margin
-- on items already sold.
CREATE TRIGGER production_batch_no_delete
  BEFORE DELETE ON production_batch
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

CREATE TRIGGER production_batch_frozen
  BEFORE UPDATE ON production_batch
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'tenant_id', 'company_id', 'uuid', 'batch_no', 'variant_id',
    'warehouse_id', 'qty_produced', 'material_cost', 'labour_cost',
    'packaging_cost', 'total_cost', 'unit_cost', 'produced_on',
    'journal_entry_id');

-- What went in. One row per component consumed, with what it actually cost —
-- taken from the costing engine, never from what somebody typed, so the value
-- leaving inventory is the value inventory says it held.
CREATE TABLE production_batch_input (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  batch_id    uuid NOT NULL REFERENCES production_batch(id) ON DELETE CASCADE,

  variant_id  uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,
  qty         numeric(18,4) NOT NULL,
  cost        numeric(18,4) NOT NULL,

  CONSTRAINT production_input_qty_positive CHECK (qty > 0),
  CONSTRAINT production_input_cost_non_negative CHECK (cost >= 0)
);

CREATE INDEX production_batch_input_batch_idx
  ON production_batch_input (batch_id);
CREATE INDEX production_batch_input_tenant_idx
  ON production_batch_input (tenant_id);

ALTER TABLE production_batch_input ENABLE ROW LEVEL SECURITY;
ALTER TABLE production_batch_input FORCE  ROW LEVEL SECURITY;
CREATE POLICY production_batch_input_isolation ON production_batch_input
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER production_batch_input_no_delete
  BEFORE DELETE ON production_batch_input
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

-- ---------------------------------------------------------------------------
-- Posting
-- ---------------------------------------------------------------------------
--
-- Three legs, and the shape says what production is:
--
--   debit  Inventory   with the finished value   (materials + labour + packaging)
--   credit Inventory   with the material cost    (the cloth left raw stock)
--   credit cash/bank   with the added cost       (the stitching was paid for)
--
-- Inventory therefore rises by exactly the labour and packaging, which is the
-- true statement: the shop owns the same cloth, now worth more because work
-- was done to it. No work-in-progress account, because nothing is in progress —
-- a batch is recorded when it is finished.

INSERT INTO posting_rule (rule_key, country, version, lines, description, effective_from)
VALUES
('production.batch', NULL, 1,
 '[{"role": "inventory",          "side": "debit",  "amount": "output"},
   {"role": "inventory",          "side": "credit", "amount": "materials"},
   {"for_each": "payment_account", "side": "credit"}]'::jsonb,
 'A production batch: finished value into inventory, raw material value out of '
 'it, and the labour and packaging paid for. Inventory rises by exactly the '
 'work done, which is what the shop now owns that it did not before.',
 DATE '2020-01-01');

COMMENT ON COLUMN production_batch.unit_cost IS
  'What one finished unit cost to make. Carried by every unit this batch put '
  'into stock, so the margin on a locally-made item is a real number rather '
  'than a guess.';
