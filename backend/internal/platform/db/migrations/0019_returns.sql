-- 0019 — Returns and credit notes.
--
-- Blueprint C14: "A return is never just 'put the item back on the shelf'."
-- Nine things must happen together — inventory, revenue, output VAT, COGS, the
-- refund, loyalty points, sales commission, a linked credit note, and the
-- journal entry with its audit record.
--
-- A credit note is a ZATCA document in its own right: it takes its own position
-- on the chain, follows the type of the invoice it corrects, and must reference
-- that original. It is not an edit — blueprint E1.4 and A2 #7 both forbid
-- touching a finalized invoice, and deleting or amending one carries a fine
-- starting at SAR 10,000.

-- ---------------------------------------------------------------------------
-- A credit line points at the line it reverses
-- ---------------------------------------------------------------------------

ALTER TABLE sales_invoice_line
  ADD COLUMN reverses_line_id uuid REFERENCES sales_invoice_line(id) ON DELETE RESTRICT;

CREATE INDEX sales_line_reverses_idx
  ON sales_invoice_line (reverses_line_id) WHERE reverses_line_id IS NOT NULL;

COMMENT ON COLUMN sales_invoice_line.reverses_line_id IS
  'The original sale line this credit line reverses. Present on credit and '
  'debit note lines, null on a sale. Makes cumulative over-return detectable '
  'and lets a partial return derive its amounts from what was actually '
  'charged rather than recomputing them.';

-- ---------------------------------------------------------------------------
-- Over-return is unrepresentable
-- ---------------------------------------------------------------------------

-- Two partial returns of 3 and 4 against a line of 5 must fail on the second.
-- Without this a customer could be refunded more than they paid, one partial
-- return at a time, and each individual return would look correct.
CREATE OR REPLACE FUNCTION assert_return_within_original() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  original       record;
  already_returned numeric(18,4);
  this_return    numeric(18,4);
BEGIN
  IF NEW.reverses_line_id IS NULL THEN
    RETURN NEW;
  END IF;

  SELECT l.qty, l.net_amount, i.doc_type
  INTO original
  FROM sales_invoice_line l
  JOIN sales_invoice i ON i.id = l.invoice_id
  WHERE l.id = NEW.reverses_line_id;

  IF original IS NULL THEN
    RAISE EXCEPTION 'The original sale line does not exist.' USING ERRCODE = 'P0001';
  END IF;

  -- A credit note corrects a sale, never another credit note. Allowing that
  -- would let a chain of corrections drift arbitrarily far from what was sold.
  IF original.doc_type NOT IN ('standard', 'simplified') THEN
    RAISE EXCEPTION
      'A credit note can only reverse a sale, not another %.', original.doc_type
      USING ERRCODE = 'P0001';
  END IF;

  SELECT coalesce(sum(abs(qty)), 0) INTO already_returned
  FROM sales_invoice_line
  WHERE reverses_line_id = NEW.reverses_line_id AND id <> NEW.id;

  this_return := abs(NEW.qty);

  IF already_returned + this_return > abs(original.qty) THEN
    RAISE EXCEPTION
      'Only % of the original % can still be returned; % were already returned.',
      abs(original.qty) - already_returned, abs(original.qty), already_returned
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER sales_line_return_within_original
  BEFORE INSERT ON sales_invoice_line
  FOR EACH ROW EXECUTE FUNCTION assert_return_within_original();

-- ---------------------------------------------------------------------------
-- Refunds
-- ---------------------------------------------------------------------------

-- How the money went back. Separate from sales_tender because a refund is not
-- a payment: it may go to a different method than the sale used (cash back on a
-- card sale), and blueprint C12 treats a card reversal as its own accounting
-- event with its own settlement timing.
CREATE TABLE sales_refund (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  credit_note_id uuid NOT NULL REFERENCES sales_invoice(id) ON DELETE RESTRICT,
  refund_no     integer NOT NULL,

  method        text NOT NULL,
  amount        numeric(18,4) NOT NULL,
  reference     text,

  -- Set when refunding to the card the sale was paid with, which is the
  -- default a customer expects and the only route that reverses the original
  -- processing fee.
  reverses_tender_id uuid REFERENCES sales_tender(id) ON DELETE RESTRICT,

  settlement_status text NOT NULL DEFAULT 'pending',
  created_at    timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT sales_refund_amount_positive CHECK (amount > 0),
  CONSTRAINT sales_refund_method_valid CHECK (method IN (
    'cash', 'mada', 'visa', 'mastercard', 'amex',
    'apple_pay', 'stc_pay', 'samsung_pay', 'sadad',
    'bank_transfer', 'cheque', 'tabby', 'tamara',
    'bkash', 'nagad',
    'store_credit',        -- to the customer's wallet
    'customer_due'         -- reduces what they owe rather than paying out
  )),
  CONSTRAINT sales_refund_settlement_valid CHECK (settlement_status IN (
    'pending', 'settled', 'reconciled'))
);

CREATE UNIQUE INDEX sales_refund_no_uq ON sales_refund (credit_note_id, refund_no);
CREATE INDEX sales_refund_tenant_idx ON sales_refund (tenant_id);

CREATE TRIGGER sales_refund_no_delete BEFORE DELETE ON sales_refund
  FOR EACH ROW EXECUTE FUNCTION reject_delete();
CREATE TRIGGER sales_refund_frozen
  BEFORE UPDATE ON sales_refund
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'credit_note_id', 'method', 'amount');

ALTER TABLE sales_refund ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_refund FORCE  ROW LEVEL SECURITY;
CREATE POLICY sales_refund_isolation ON sales_refund
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The refund must match the credit note
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION assert_credit_note_settled() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  note        record;
  refund_total numeric(18,4);
BEGIN
  SELECT * INTO note FROM sales_invoice WHERE id = NEW.credit_note_id;
  IF note IS NULL OR note.state = 'draft' THEN
    RETURN NULL;
  END IF;

  IF note.doc_type <> 'credit_note' THEN
    RAISE EXCEPTION 'A refund must belong to a credit note.' USING ERRCODE = 'P0001';
  END IF;

  SELECT coalesce(sum(amount), 0) INTO refund_total
  FROM sales_refund WHERE credit_note_id = NEW.credit_note_id;

  IF refund_total <> note.total_inclusive THEN
    RAISE EXCEPTION
      'Refunds total % but the credit note is %. A credit note must be settled in full.',
      refund_total, note.total_inclusive
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER sales_refund_settles_note
  AFTER INSERT ON sales_refund
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_credit_note_settled();

-- ---------------------------------------------------------------------------
-- What is still returnable
-- ---------------------------------------------------------------------------

-- Drives the returns screen: scan the original invoice, see what is left.
-- Blueprint B10 requires the original to be scanned and linked, never re-typed,
-- and this is what that screen reads.
CREATE OR REPLACE FUNCTION returnable_lines(p_invoice_id uuid)
RETURNS TABLE (
  line_id           uuid,
  line_no           integer,
  description       text,
  qty_sold          numeric,
  qty_returned      numeric,
  qty_returnable    numeric,
  unit_net          numeric,
  net_amount        numeric,
  tax_amount        numeric,
  cogs_amount       numeric
)
LANGUAGE sql STABLE AS $$
  SELECT l.id, l.line_no, l.description,
         abs(l.qty),
         coalesce(r.returned, 0),
         abs(l.qty) - coalesce(r.returned, 0),
         CASE WHEN l.qty <> 0 THEN round(l.net_amount / abs(l.qty), 4) ELSE 0 END,
         l.net_amount, l.tax_amount, coalesce(l.cogs_amount, 0)
  FROM sales_invoice_line l
  LEFT JOIN (
    SELECT reverses_line_id, sum(abs(qty)) AS returned
    FROM sales_invoice_line
    WHERE reverses_line_id IS NOT NULL
    GROUP BY reverses_line_id
  ) r ON r.reverses_line_id = l.id
  WHERE l.invoice_id = p_invoice_id
  ORDER BY l.line_no
$$;
