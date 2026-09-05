-- Sending goods back to a supplier, and the debit note that claims the money.
--
-- Blueprint B5, under Purchasing: "Purchase Return (to Supplier): for
-- defective/excess stock — auto-generates a Debit Note and instantly deducts
-- inventory." The one purchasing document the product did not have. Thirty-one
-- routes and none of them sent anything back; the earlier sweep read the word
-- "returns" on the purchasing row and it meant payment REVERSAL, which is a
-- different fact about a different thing.
--
-- ---------------------------------------------------------------------------
-- Against the BILL, not the delivery
-- ---------------------------------------------------------------------------
--
-- Goods refused at the door are already handled: `grn_line.qty_rejected`
-- records what came off the lorry damaged, and it never enters stock, so there
-- is nothing to send back and nothing to claim. This is the other case — a
-- defect found a week later, after the invoice arrived and the stock was put
-- away.
--
-- That is also what makes it a debit note rather than a stock adjustment.
-- 0014's own comment: "A credit or debit note has no meaning without the
-- invoice it corrects." The bill is what the supplier will credit against, and
-- a return raised against a delivery with no bill behind it would be a claim
-- with nothing to claim from.
--
-- ---------------------------------------------------------------------------
-- Two figures, and they are allowed to differ
-- ---------------------------------------------------------------------------
--
-- What the SUPPLIER owes back is the price they billed: qty × the bill's unit
-- cost, plus the tax they charged at the rate on that line. That is the face of
-- the debit note and is not negotiable — it is their document, agreed when the
-- invoice was accepted.
--
-- What leaves STOCK is whatever the valuation says those units are worth. Under
-- FIFO that is the layer the engine picks; under weighted average it is the
-- pool; and either can differ from the bill because landed cost was added on
-- receipt (0034) or because the shop has bought the same item since at another
-- price.
--
-- The difference is a real number and has an account already: `cost_variance`,
-- which 0025 rule 11 and 0026 rule 11a were written for and 0048 repaired. So
-- the return posts the payable at the bill's value, takes the stock out at the
-- valuation's value, and books the gap to variance — rather than forcing one of
-- the two to be wrong so the entry balances.
--
-- ---------------------------------------------------------------------------
-- What cannot be returned twice
-- ---------------------------------------------------------------------------
--
-- Cumulative, per bill line, exactly as 0019 does for a customer return. A
-- clerk raising two returns for the same case of stock would claim twice from
-- the supplier and take the stock out twice; the guard is the sum of every
-- return line against that bill line, checked in the service and left visible
-- here as a view, because a rule nobody can query is a rule nobody can audit.

-- ---------------------------------------------------------------------------
-- The documents
-- ---------------------------------------------------------------------------

CREATE TABLE purchase_return (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- Client-assigned, so a clerk pressing the button twice on a bad connection
  -- claims once. The same discipline every other document here carries.
  uuid        uuid NOT NULL,
  return_no   text NOT NULL,

  bill_id     uuid NOT NULL REFERENCES purchase_bill(id) ON DELETE RESTRICT,
  supplier_id uuid NOT NULL REFERENCES supplier(id)      ON DELETE RESTRICT,

  -- Where the stock leaves from. A shop with a shop floor and a back room has
  -- to say which, for the same reason a sale does.
  warehouse_id uuid NOT NULL REFERENCES warehouse(id) ON DELETE RESTRICT,

  returned_on date NOT NULL,

  -- Required, and required to be meaningful. B10 says the same about a
  -- customer return and the reason is the same: an unexplained return is how a
  -- clerk and a supplier's driver split the value of a pallet.
  reason      text NOT NULL,

  currency    char(3) NOT NULL,

  subtotal_net    numeric(18,4) NOT NULL DEFAULT 0,
  tax_total       numeric(18,4) NOT NULL DEFAULT 0,
  total_inclusive numeric(18,4) NOT NULL DEFAULT 0,

  -- What the stock was actually carrying, which is not the same figure and is
  -- kept rather than recomputed: the layers it came out of are gone.
  stock_value     numeric(18,4) NOT NULL DEFAULT 0,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT purchase_return_reason_present CHECK (btrim(reason) <> ''),
  CONSTRAINT purchase_return_totals_sane CHECK (
    subtotal_net >= 0 AND tax_total >= 0 AND total_inclusive >= 0)
);

CREATE UNIQUE INDEX purchase_return_uuid_uq ON purchase_return (company_id, uuid);
CREATE UNIQUE INDEX purchase_return_no_uq   ON purchase_return (company_id, return_no);
CREATE INDEX purchase_return_bill_idx     ON purchase_return (bill_id);
CREATE INDEX purchase_return_supplier_idx ON purchase_return (supplier_id, returned_on DESC);
CREATE INDEX purchase_return_tenant_idx   ON purchase_return (tenant_id);

ALTER TABLE purchase_return ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_return FORCE  ROW LEVEL SECURITY;
CREATE POLICY purchase_return_isolation ON purchase_return
  USING (tenant_id = current_tenant_id());

-- Posted the moment it is written: there is no draft, because the stock has
-- already left the building. Correcting one means receiving the goods back,
-- not editing the claim.
CREATE TRIGGER purchase_return_immutable
  BEFORE UPDATE OR DELETE ON purchase_return
  FOR EACH ROW EXECUTE FUNCTION reject_always();

CREATE TABLE purchase_return_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  return_id   uuid NOT NULL REFERENCES purchase_return(id) ON DELETE CASCADE,

  -- The bill line this claims against. Not nullable: a claim that names no
  -- line on the supplier's invoice is a claim they cannot check.
  bill_line_id uuid NOT NULL REFERENCES bill_line(id) ON DELETE RESTRICT,
  variant_id   uuid REFERENCES variant(id) ON DELETE RESTRICT,

  line_no     integer NOT NULL,
  description text NOT NULL,

  qty         numeric(18,4) NOT NULL,

  -- The BILL's price and the BILL's rate, copied rather than referenced. What
  -- the supplier owes back is what they charged, and a later correction to the
  -- bill must not silently restate a claim already sent.
  unit_cost     numeric(18,4) NOT NULL,
  tax_treatment text NOT NULL DEFAULT 'standard',
  tax_rate      numeric(9,6) NOT NULL DEFAULT 0,

  net_amount   numeric(18,4) NOT NULL,
  tax_amount   numeric(18,4) NOT NULL,
  gross_amount numeric(18,4) NOT NULL,

  -- What the units were carrying in stock, from the costing engine.
  stock_value  numeric(18,4) NOT NULL DEFAULT 0,

  CONSTRAINT purchase_return_line_qty_positive CHECK (qty > 0),
  CONSTRAINT purchase_return_line_cost_positive CHECK (unit_cost >= 0)
);

CREATE UNIQUE INDEX purchase_return_line_no_uq
  ON purchase_return_line (return_id, line_no);
CREATE INDEX purchase_return_line_bill_line_idx
  ON purchase_return_line (bill_line_id);
CREATE INDEX purchase_return_line_tenant_idx ON purchase_return_line (tenant_id);

ALTER TABLE purchase_return_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_return_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY purchase_return_line_isolation ON purchase_return_line
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER purchase_return_line_immutable
  BEFORE UPDATE OR DELETE ON purchase_return_line
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- What is left to send back
-- ---------------------------------------------------------------------------
--
-- Per bill line: billed, already returned, and the difference. The screen reads
-- this rather than working it out, for the same reason the till reads
-- `returnable` rather than counting credit notes — earlier returns are on the
-- server and a client that computed this would eventually claim twice.
CREATE OR REPLACE VIEW bill_line_returnable AS
  SELECT
    bl.id            AS bill_line_id,
    bl.bill_id,
    bl.tenant_id,
    bl.line_no,
    bl.description,
    bl.variant_id,
    bl.qty_billed,
    bl.unit_cost,
    bl.tax_treatment,
    bl.tax_rate,
    coalesce(sum(rl.qty), 0)              AS qty_returned,
    bl.qty_billed - coalesce(sum(rl.qty), 0) AS qty_returnable
  FROM bill_line bl
  LEFT JOIN purchase_return_line rl ON rl.bill_line_id = bl.id
  GROUP BY bl.id;

-- ---------------------------------------------------------------------------
-- Numbering
-- ---------------------------------------------------------------------------

ALTER TABLE company
  ADD COLUMN IF NOT EXISTS next_purchase_return_no bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_purchase_no(
  p_company_id uuid, p_kind text
) RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE
  v_next bigint;
BEGIN
  IF p_kind = 'po' THEN
    UPDATE company SET next_po_no = next_po_no + 1
    WHERE id = p_company_id RETURNING next_po_no - 1 INTO v_next;
  ELSIF p_kind = 'grn' THEN
    UPDATE company SET next_grn_no = next_grn_no + 1
    WHERE id = p_company_id RETURNING next_grn_no - 1 INTO v_next;
  ELSIF p_kind = 'payment' THEN
    UPDATE company SET next_payment_no = next_payment_no + 1
    WHERE id = p_company_id RETURNING next_payment_no - 1 INTO v_next;
  ELSIF p_kind = 'requisition' THEN
    UPDATE company SET next_requisition_no = next_requisition_no + 1
    WHERE id = p_company_id RETURNING next_requisition_no - 1 INTO v_next;
  ELSIF p_kind = 'rfq' THEN
    UPDATE company SET next_rfq_no = next_rfq_no + 1
    WHERE id = p_company_id RETURNING next_rfq_no - 1 INTO v_next;
  ELSIF p_kind = 'purchase_return' THEN
    UPDATE company SET next_purchase_return_no = next_purchase_return_no + 1
    WHERE id = p_company_id RETURNING next_purchase_return_no - 1 INTO v_next;
  ELSE
    RAISE EXCEPTION 'unknown purchase document kind: %', p_kind;
  END IF;

  IF v_next IS NULL THEN
    RAISE EXCEPTION 'company % not found', p_company_id;
  END IF;
  RETURN v_next;
END;
$$;

-- ---------------------------------------------------------------------------
-- The stock movement
-- ---------------------------------------------------------------------------
--
-- Its own reason, not the existing 'return'. That one means a CUSTOMER return,
-- which moves stock the other way; sharing it would put goods leaving for a
-- supplier into every report that reads "return" as stock coming back, and a
-- stock card would read "10 in — return" beside "10 out — return".
ALTER TABLE stock_movement DROP CONSTRAINT stock_movement_reason_valid;
ALTER TABLE stock_movement
  ADD CONSTRAINT stock_movement_reason_valid CHECK (reason IN (
    'sale', 'return', 'grn', 'adjustment', 'transfer_in', 'transfer_out',
    'wastage', 'opening', 'count', 'internal_use',
    'production_in', 'production_out', 'purchase_return'));

-- ---------------------------------------------------------------------------
-- The posting rule
-- ---------------------------------------------------------------------------
--
-- The mirror of `purchase.credit`, and separated the same way: the payable
-- falls by what the supplier will credit, the input tax the shop claimed is
-- given back, and the stock leaves at what it was carrying.
--
-- Three amounts rather than two, because the payable and the stock are not the
-- same figure and pretending they are is how a balance sheet stops agreeing
-- with a stock report. `variance` is whatever is left over; the service passes
-- zero when there is none, and a zero line contributes nothing to either side.
INSERT INTO posting_rule
  (rule_key, country, version, lines, description, effective_from)
VALUES
  ('purchase.return', NULL, 1,
   '[{"role": "accounts_payable", "side": "debit",  "amount": "total_inclusive"},
     {"role": "cost_variance",    "side": "debit",  "amount": "variance_debit"},
     {"role": "inventory",        "side": "credit", "amount": "stock_value"},
     {"role": "input_vat",        "side": "credit", "amount": "tax_amount"},
     {"role": "cost_variance",    "side": "credit", "amount": "variance_credit"}]'::jsonb,
   'Goods returned to a supplier: payable reduced by the debit note, stock out at cost, input tax given back.',
   '2020-01-01')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The permission
-- ---------------------------------------------------------------------------
--
-- Its own verb, and not `purchasing.receive_goods`. Taking a delivery in is a
-- warehouse act; sending one back reduces what the business owes and produces
-- a document the supplier will argue with, which is a commercial act. A
-- storeman who may unload a lorry has no business deciding what the shop
-- claims back from the people who sent it.
INSERT INTO permission_catalogue
  (permission, section, label, label_ar, label_bn, caution, sort_order)
VALUES
  ('purchasing.return_goods', 'buying',
   'Send goods back to a supplier',
   'إرجاع بضاعة إلى مورد',
   'সরবরাহকারীকে পণ্য ফেরত পাঠানো',
   'Takes the stock out and claims the money back from the supplier.',
   95)
ON CONFLICT (permission) DO NOTHING;

-- Owner and Purchase Manager. Deliberately not the Store Manager: 0032 gives
-- them `receive_goods` and not `record_bill`, and a return is a claim against a
-- bill they cannot see.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',            'purchasing.return_goods'),
  ('purchase_manager', 'purchasing.return_goods')
) AS p(role_key, permission) ON p.role_key = r.key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

-- And the clones tenants already hold. 0051 records why this is needed and why
-- it is a loop: writing to `role_permission` for a tenant-owned role means
-- satisfying `role_isolation`, and the honest way to do that is to set the
-- tenant per iteration rather than to widen what a platform admin can see.
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
      ('owner',            'purchasing.return_goods'),
      ('purchase_manager', 'purchasing.return_goods')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', '', true);
END $$;
