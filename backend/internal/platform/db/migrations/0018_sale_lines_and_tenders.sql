-- 0018 — Invoice lines and tenders.
--
-- Where the three pillars meet: a line carries the tax the invoice engine will
-- sign, the COGS the posting engine will book, and the discount allocation a
-- future return will need. Getting the storage right here is what keeps the
-- other three honest.

-- ---------------------------------------------------------------------------
-- Line
-- ---------------------------------------------------------------------------

CREATE TABLE sales_invoice_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  invoice_id  uuid NOT NULL REFERENCES sales_invoice(id) ON DELETE RESTRICT,
  line_no     integer NOT NULL,

  variant_id  uuid REFERENCES variant(id) ON DELETE RESTRICT,

  -- The description is COPIED, not joined, so a five-year-old invoice still
  -- reads correctly after the product is renamed or discontinued. Blueprint B1
  -- requires exactly that, and a join would quietly rewrite history.
  description    text NOT NULL,
  description_ar text,

  qty         numeric(18,4) NOT NULL,
  unit_price  numeric(18,4) NOT NULL,

  -- Discount given on this line specifically.
  line_discount numeric(18,4) NOT NULL DEFAULT 0,

  -- This line's share of an invoice-level discount, allocated AT SALE TIME.
  --
  -- Blueprint C14 names proportional discount allocation as "a common place
  -- where cheaper POS software silently produces wrong numbers". The reason is
  -- that reconstructing the split during a partial return drifts on rounding:
  -- the shares no longer add back to the original discount. Storing it removes
  -- the arithmetic entirely.
  invoice_discount_alloc numeric(18,4) NOT NULL DEFAULT 0,

  -- Tax as computed and signed. The RATE is stored alongside the amount so a
  -- reprint years later shows what was charged, not what the rate is today.
  tax_treatment text NOT NULL,
  tax_rate      numeric(18,8) NOT NULL,
  tax_amount    numeric(18,4) NOT NULL,
  tax_exemption_reason_code text,

  net_amount   numeric(18,4) NOT NULL,
  gross_amount numeric(18,4) NOT NULL,

  -- Cost of goods sold, captured at the moment of sale (blueprint C13), so
  -- gross profit is real-time rather than a month-end reconstruction.
  cogs_amount  numeric(18,4),

  created_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT sales_line_qty_non_zero CHECK (qty <> 0),
  CONSTRAINT sales_line_amounts_non_negative CHECK (
    unit_price >= 0 AND line_discount >= 0 AND invoice_discount_alloc >= 0
    AND tax_amount >= 0 AND net_amount >= 0 AND gross_amount >= 0
    AND (cogs_amount IS NULL OR cogs_amount >= 0)),
  CONSTRAINT sales_line_rate_sane CHECK (tax_rate >= 0 AND tax_rate < 1),
  -- The line must add up. A stored total that disagrees with its parts is how
  -- an invoice passes every check and still shows the wrong number.
  CONSTRAINT sales_line_totals_consistent CHECK (
    gross_amount = net_amount + tax_amount)
);

CREATE UNIQUE INDEX sales_line_no_uq ON sales_invoice_line (invoice_id, line_no);
CREATE INDEX sales_line_tenant_idx  ON sales_invoice_line (tenant_id);
CREATE INDEX sales_line_variant_idx ON sales_invoice_line (variant_id);

CREATE TRIGGER sales_line_immutable
  BEFORE UPDATE OR DELETE ON sales_invoice_line
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE sales_invoice_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_invoice_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY sales_line_isolation ON sales_invoice_line
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Price floor
-- ---------------------------------------------------------------------------

-- Blueprint B1 calls the floor "the lowest price a cashier is ever allowed to
-- sell at, even after discount — enforced by the system, not just policy."
--
-- A CHECK constraint cannot see another table, so this is a trigger. It is the
-- backstop: the POS checks the floor for immediate feedback and the sales
-- service checks it again, but only this one survives a service bug, and an
-- illegal price on a signed tax invoice cannot be taken back.
CREATE OR REPLACE FUNCTION assert_price_floor() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  floor_price numeric(18,4);
  effective   numeric(18,4);
BEGIN
  IF NEW.variant_id IS NULL THEN
    RETURN NEW;
  END IF;

  SELECT price_floor INTO floor_price FROM variant WHERE id = NEW.variant_id;
  IF floor_price IS NULL THEN
    RETURN NEW;
  END IF;

  -- Compare per unit, after every discount. A 50% invoice-level discount
  -- breaches the floor exactly as a 50% line discount does, and a check that
  -- only saw one of them would be trivial to work around.
  effective := (NEW.net_amount) / NULLIF(abs(NEW.qty), 0);

  IF effective < floor_price THEN
    RAISE EXCEPTION
      'This price is % per unit after discount, below the minimum of % set for this product.',
      round(effective, 2), round(floor_price, 2)
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER sales_line_price_floor
  BEFORE INSERT ON sales_invoice_line
  FOR EACH ROW EXECUTE FUNCTION assert_price_floor();

-- ---------------------------------------------------------------------------
-- Tender
-- ---------------------------------------------------------------------------

CREATE TABLE sales_tender (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  invoice_id  uuid NOT NULL REFERENCES sales_invoice(id) ON DELETE RESTRICT,
  tender_no   integer NOT NULL,

  -- Every method is its own value. Blueprint E3.1 is explicit that Mada must
  -- not be folded into a generic "card": its processing fee is materially lower
  -- than international credit, so merging them misstates margin per sale — and
  -- margin per payment method is precisely what the Owner asked to see.
  method      text NOT NULL,
  amount      numeric(18,4) NOT NULL,
  reference   text,

  -- Gross minus fee equals net settlement, which lands days later. Blueprint
  -- C12: the Owner needs "collected but not yet in the bank" as a real figure.
  fee_amount  numeric(18,4),
  settlement_status text NOT NULL DEFAULT 'pending',
  settled_at  timestamptz,

  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT sales_tender_amount_positive CHECK (amount > 0),
  CONSTRAINT sales_tender_fee_non_negative CHECK (fee_amount IS NULL OR fee_amount >= 0),
  CONSTRAINT sales_tender_method_valid CHECK (method IN (
    'cash',
    'mada',                -- Saudi national debit. Non-negotiable, own fee band.
    'visa', 'mastercard', 'amex',
    'apple_pay', 'stc_pay', 'samsung_pay',
    'sadad',               -- national bill payment, B2B settlement
    'bank_transfer', 'cheque',
    'tabby', 'tamara',     -- BNPL, higher merchant fee, own settlement timing
    'bkash', 'nagad',      -- Bangladesh mobile financial services
    'store_credit', 'loyalty_points',
    'customer_due'         -- posts to AR, subject to the credit limit
  )),
  CONSTRAINT sales_tender_settlement_valid CHECK (settlement_status IN (
    'pending', 'settled', 'reconciled', 'chargeback'))
);

CREATE UNIQUE INDEX sales_tender_no_uq ON sales_tender (invoice_id, tender_no);
CREATE INDEX sales_tender_tenant_idx ON sales_tender (tenant_id);
CREATE INDEX sales_tender_unsettled_idx
  ON sales_tender (settlement_status) WHERE settlement_status = 'pending';

CREATE TRIGGER sales_tender_immutable
  BEFORE DELETE ON sales_tender
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

-- Settlement status advances; the money does not move.
CREATE TRIGGER sales_tender_frozen_amount
  BEFORE UPDATE ON sales_tender
  FOR EACH ROW EXECUTE FUNCTION reject_column_change('invoice_id', 'method', 'amount');

ALTER TABLE sales_tender ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_tender FORCE  ROW LEVEL SECURITY;
CREATE POLICY sales_tender_isolation ON sales_tender
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The invoice must add up
-- ---------------------------------------------------------------------------

-- Lines sum to the invoice header, and tenders sum to the total. Deferred to
-- commit for the same reason the journal balance check is: rows arrive one at
-- a time and the invoice is legitimately unbalanced in between.
--
-- Once ZATCA has an invoice it cannot be corrected in place, so an unbalanced
-- one is not a bug to fix later — it is a credit note and a new document.
CREATE OR REPLACE FUNCTION assert_invoice_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  inv          record;
  line_net     numeric(18,4);
  line_tax     numeric(18,4);
  line_gross   numeric(18,4);
  tender_total numeric(18,4);
  line_count   integer;
BEGIN
  SELECT * INTO inv FROM sales_invoice WHERE id = NEW.invoice_id;
  IF inv IS NULL THEN
    RETURN NULL;   -- rolled back within this transaction
  END IF;

  -- A draft is still being built; it consumes no ICV and is not a document.
  IF inv.state = 'draft' THEN
    RETURN NULL;
  END IF;

  SELECT coalesce(sum(net_amount), 0), coalesce(sum(tax_amount), 0),
         coalesce(sum(gross_amount), 0), count(*)
  INTO line_net, line_tax, line_gross, line_count
  FROM sales_invoice_line WHERE invoice_id = NEW.invoice_id;

  IF line_count = 0 THEN
    RAISE EXCEPTION 'An invoice must have at least one line.' USING ERRCODE = 'P0001';
  END IF;

  IF inv.subtotal_net <> line_net THEN
    RAISE EXCEPTION
      'Invoice net total is % but its lines add up to %.', inv.subtotal_net, line_net
      USING ERRCODE = 'P0001';
  END IF;
  IF inv.tax_total <> line_tax THEN
    RAISE EXCEPTION
      'Invoice tax total is % but its lines add up to %.', inv.tax_total, line_tax
      USING ERRCODE = 'P0001';
  END IF;
  IF inv.total_inclusive <> line_gross THEN
    RAISE EXCEPTION
      'Invoice total is % but its lines add up to %.', inv.total_inclusive, line_gross
      USING ERRCODE = 'P0001';
  END IF;

  -- Tenders must cover the invoice exactly. A credit note is settled by the
  -- refund it produces, so it is exempt from this check.
  IF inv.doc_type NOT IN ('credit_note', 'debit_note') THEN
    SELECT coalesce(sum(amount), 0) INTO tender_total
    FROM sales_tender WHERE invoice_id = NEW.invoice_id;

    IF tender_total <> inv.total_inclusive THEN
      RAISE EXCEPTION
        'Payments total % but the invoice is %. A sale must be paid in full before it is issued.',
        tender_total, inv.total_inclusive
        USING ERRCODE = 'P0001';
    END IF;
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER sales_line_invoice_balanced
  AFTER INSERT ON sales_invoice_line
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_invoice_balanced();

CREATE CONSTRAINT TRIGGER sales_tender_invoice_balanced
  AFTER INSERT ON sales_tender
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_invoice_balanced();
