-- 0085 — Quotations, sales orders and delivery documents (blueprint B11), and
-- the wholesale pricing rules B12 asks for.
--
-- # One document, six states
--
-- B11 draws a sales order's lifecycle as Draft → Confirmed → Processing →
-- Packed → Delivered → Completed, and asks for a quotation that converts to
-- one "in one click". Two tables would mean two shapes of the same thing and a
-- conversion that copies rows between them — and a copy is a place where a
-- price can change without anybody meaning it to.
--
-- So a quotation IS a sales order, in a state called `quotation`, and
-- converting it is a state change. The customer, the lines and the prices are
-- the same rows throughout, which is what makes "the quote said 4,200" a
-- question with an answer.
--
-- # What this is not
--
-- Not an invoice. An order is a promise to sell; an invoice is the tax document
-- that records the sale, and E1 makes it immutable and part of a hash chain.
-- An order can be edited, cancelled and re-priced. Conflating the two is how a
-- product ends up editing invoices, which ZATCA does not permit and this
-- product refuses everywhere else.
--
-- The order carries the invoice it eventually produced, and stock leaves on the
-- INVOICE as it always has. Nothing here touches the ledger.

-- ---------------------------------------------------------------------------
-- The order
-- ---------------------------------------------------------------------------

CREATE TABLE sales_order (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  store_id    uuid REFERENCES store(id) ON DELETE RESTRICT,

  order_no    text NOT NULL,
  customer_id uuid REFERENCES customer(id) ON DELETE RESTRICT,

  -- quotation  — a price offered, not yet accepted
  -- confirmed  — the customer has accepted it
  -- processing — being picked
  -- packed     — ready to go
  -- delivered  — with the customer
  -- completed  — invoiced and finished
  -- cancelled  — abandoned at any point before completion
  state       text NOT NULL DEFAULT 'quotation',

  -- B11: a quotation has a "validity date". A quote with no expiry is a price
  -- a customer can hold a shop to for ever, which is a real commercial
  -- exposure on anything whose cost moves.
  valid_until date,

  -- Which channel it came from. B13 asks for own website, PWA, social media
  -- and marketplaces; B12 for wholesale kept separate "so retail reporting
  -- isn't distorted by bulk transactions". One column answers both.
  channel     text NOT NULL DEFAULT 'store',

  -- B11's sales region, for revenue and profit by territory.
  region      text,

  currency    char(3) NOT NULL,
  notes       text,

  -- Where it goes, for the delivery documents. Kept on the order rather than
  -- read from the customer, because a customer's registered address and where
  -- they want this one delivered are different facts.
  deliver_to  text,
  deliver_phone text,

  -- The invoice it became. Set when the order is completed, and the reason
  -- this table never needs to hold a tax total: the invoice is the tax
  -- document and this points at it.
  invoice_id  uuid REFERENCES sales_invoice(id) ON DELETE RESTRICT,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  confirmed_at timestamptz,
  delivered_at timestamptz,
  completed_at timestamptz,
  cancelled_at timestamptz,
  cancel_reason text,

  CONSTRAINT sales_order_state_valid CHECK (state IN (
    'quotation', 'confirmed', 'processing', 'packed',
    'delivered', 'completed', 'cancelled')),
  CONSTRAINT sales_order_channel_valid CHECK (channel IN (
    'store', 'wholesale', 'online', 'phone', 'marketplace')),
  CONSTRAINT sales_order_currency_upper CHECK (currency = upper(currency)),

  -- A completed order has an invoice, and an order with an invoice is
  -- completed. Either alone is a state nobody can explain.
  CONSTRAINT sales_order_completed_has_an_invoice CHECK (
    (state = 'completed') = (invoice_id IS NOT NULL)),

  -- Cancelling needs a reason, for the same reason reopening a period does:
  -- somebody will ask why an order for four thousand riyals disappeared.
  CONSTRAINT sales_order_cancelled_says_why CHECK (
    state <> 'cancelled'
    OR (cancel_reason IS NOT NULL AND length(btrim(cancel_reason)) >= 3)),

  -- Only a quotation has a validity date. A confirmed order does not expire.
  CONSTRAINT sales_order_validity_is_for_quotations CHECK (
    valid_until IS NULL OR state IN ('quotation', 'cancelled'))
);

CREATE UNIQUE INDEX sales_order_no_uq ON sales_order (company_id, order_no);
CREATE INDEX sales_order_tenant_idx ON sales_order (tenant_id);
CREATE INDEX sales_order_customer_idx ON sales_order (customer_id, created_at DESC);
-- The working view: everything somebody still has to do something about.
CREATE INDEX sales_order_open_idx ON sales_order (company_id, state, created_at DESC)
  WHERE state NOT IN ('completed', 'cancelled');

CREATE TRIGGER sales_order_touch BEFORE UPDATE ON sales_order
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE sales_order ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_order FORCE  ROW LEVEL SECURITY;
CREATE POLICY sales_order_isolation ON sales_order
  USING (tenant_id = current_tenant_id());

CREATE TABLE sales_order_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  order_id    uuid NOT NULL REFERENCES sales_order(id) ON DELETE CASCADE,
  variant_id  uuid NOT NULL REFERENCES variant(id) ON DELETE RESTRICT,

  line_no     integer NOT NULL,
  description text,

  qty         numeric(18,4) NOT NULL,
  unit_price  numeric(18,4) NOT NULL,
  discount    numeric(18,4) NOT NULL DEFAULT 0,

  -- What has been picked and what has been delivered, so a partial delivery is
  -- a fact on the line rather than a state of the whole order. B11's picking
  -- and packing slips are drawn from these.
  qty_picked    numeric(18,4) NOT NULL DEFAULT 0,
  qty_delivered numeric(18,4) NOT NULL DEFAULT 0,

  CONSTRAINT sales_order_line_qty_positive CHECK (qty > 0),
  CONSTRAINT sales_order_line_price_non_negative CHECK (unit_price >= 0),
  CONSTRAINT sales_order_line_discount_sane CHECK (
    discount >= 0 AND discount <= qty * unit_price),
  CONSTRAINT sales_order_line_picked_sane CHECK (
    qty_picked >= 0 AND qty_picked <= qty),
  -- Delivered can never exceed picked: goods cannot leave a warehouse without
  -- having been taken off a shelf, and an order showing four delivered of two
  -- picked is a data-entry error nothing else would catch.
  CONSTRAINT sales_order_line_delivered_sane CHECK (
    qty_delivered >= 0 AND qty_delivered <= qty_picked)
);

CREATE UNIQUE INDEX sales_order_line_no_uq ON sales_order_line (order_id, line_no);
CREATE INDEX sales_order_line_tenant_idx ON sales_order_line (tenant_id);
CREATE INDEX sales_order_line_variant_idx ON sales_order_line (variant_id);

ALTER TABLE sales_order_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_order_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY sales_order_line_isolation ON sales_order_line
  USING (tenant_id = current_tenant_id());

-- Lines are frozen once the order has an invoice. Before that an order is a
-- working document and editing it is the point; afterwards it is the record of
-- what was actually sold, and the invoice is immutable.
CREATE OR REPLACE FUNCTION reject_completed_order_change()
RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  st text;
BEGIN
  SELECT state INTO st FROM sales_order
  WHERE id = coalesce(NEW.order_id, OLD.order_id);

  IF st = 'completed' THEN
    RAISE EXCEPTION
      'An order that has been invoiced cannot be changed. The invoice is the record of what was sold.'
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN coalesce(NEW, OLD);
END;
$$;

CREATE TRIGGER sales_order_line_frozen_once_invoiced
  BEFORE INSERT OR UPDATE OR DELETE ON sales_order_line
  FOR EACH ROW EXECUTE FUNCTION reject_completed_order_change();

ALTER TABLE company ADD COLUMN next_order_no bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_order_no(p_company_id uuid)
RETURNS text
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  UPDATE company SET next_order_no = next_order_no + 1
  WHERE id = p_company_id
  RETURNING next_order_no - 1 INTO claimed;

  IF claimed IS NULL THEN
    RAISE EXCEPTION 'no such company' USING ERRCODE = 'no_data_found';
  END IF;

  RETURN 'SO-' || to_char(claimed, 'FM000000');
END;
$$;

-- ---------------------------------------------------------------------------
-- Wholesale rules (B12)
-- ---------------------------------------------------------------------------
--
-- B12 asks for a wholesale customer type with dealer pricing, minimum order
-- quantities and bulk-quantity discounts. Three of those four already exist:
-- `customer.customer_type` carries the type, `variant.price_wholesale` and
-- `price_dealer` carry the tiers, and 0084's promotions carry bulk discounts as
-- a bundle or a quantity break.
--
-- The one that does not is the minimum order quantity, which is a property of
-- what a shop will sell wholesale rather than of any one order.

ALTER TABLE variant
  ADD COLUMN min_wholesale_qty numeric(18,4);

ALTER TABLE variant
  ADD CONSTRAINT variant_min_wholesale_positive
  CHECK (min_wholesale_qty IS NULL OR min_wholesale_qty > 0);

COMMENT ON COLUMN variant.min_wholesale_qty IS
  'B12: the fewest a wholesale customer may order. NULL for a product with no '
  'minimum. Checked when an order is confirmed, not when a line is added, so '
  'somebody building a quote is not interrupted mid-sentence.';

-- ---------------------------------------------------------------------------
-- The permissions
-- ---------------------------------------------------------------------------
--
-- `sales.view` and `sales.create` already exist for the till. An order is not
-- a sale: it is a promise to sell, it can be edited, and the person who raises
-- one is often a sales executive rather than a cashier. 0005 already describes
-- that role as handling "quotations, orders and their own customer list" — and
-- gave it no permission that could reach one.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',            'order.view'), ('owner',            'order.manage'),
  ('store_manager',    'order.view'), ('store_manager',    'order.manage'),
  ('sales_executive',  'order.view'), ('sales_executive',  'order.manage'),
  ('online_manager',   'order.view'), ('online_manager',   'order.manage'),
  ('delivery_staff',   'order.view'),
  ('inventory_keeper', 'order.view'),
  ('cashier',          'order.view'),
  ('accountant',       'order.view'),
  ('auditor',          'order.view'),
  -- The sales executive and the online manager need the catalogue to build an
  -- order out of, and the customer list to raise one for. 0005 gave them
  -- neither, which is the same widow 0033 found for the purchase manager.
  ('sales_executive',  'catalog.view'),
  ('sales_executive',  'customers.view'),
  ('online_manager',   'catalog.view'),
  ('online_manager',   'customers.view')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

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
      ('owner',            'order.view'), ('owner',            'order.manage'),
      ('store_manager',    'order.view'), ('store_manager',    'order.manage'),
      ('sales_executive',  'order.view'), ('sales_executive',  'order.manage'),
      ('online_manager',   'order.view'), ('online_manager',   'order.manage'),
      ('delivery_staff',   'order.view'),
      ('inventory_keeper', 'order.view'),
      ('cashier',          'order.view'),
      ('accountant',       'order.view'),
      ('auditor',          'order.view'),
      ('sales_executive',  'catalog.view'),
      ('sales_executive',  'customers.view'),
      ('online_manager',   'catalog.view'),
      ('online_manager',   'customers.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
