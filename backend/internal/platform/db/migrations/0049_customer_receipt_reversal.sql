-- 0049 — reverse a customer receipt without editing it.
--
-- A receipt allocated to the wrong invoice used to stay there. There was no
-- document that could put the money back on the account, so a clerk who
-- mis-keyed had no route back and the customer's statement stayed wrong.
--
-- Blueprint C9.1 and design 02 §2: journal entries are immutable; corrections
-- happen only by posting a reversing entry. The same shape applies here. The
-- original receipt is a fact about money that arrived. Putting it right means
-- a NEW receipt that reverses it — same rule, sides flipped — never an UPDATE
-- or a DELETE of the original, Owner and Super Admin included.
--
-- # Why the reversing document keeps a positive amount
--
-- customer_receipt_positive and customer_allocation_positive both require
-- amount > 0. That is load-bearing: a payment of nothing is not a payment, and
-- a negative allocation would look like the original taking money out. The
-- sign lives on the JOURNAL (debit AR, credit cash) and on how the open-
-- invoice view COUNTS the reversing allocations, not on the document itself.
--
-- # Why received is signed in the view
--
-- customer_open_invoices summed every allocation as money in. A reversing
-- receipt copies the original's allocations so the statement can name which
-- invoices were put back. Without the sign, those copies would look like a
-- second payment and the receivable would go more negative — the opposite of
-- a reversal, and C9.3's tie-out would fail by twice the amount.

ALTER TABLE customer_receipt
  ADD COLUMN reverses_id uuid REFERENCES customer_receipt(id) ON DELETE RESTRICT;

-- One reversal per original. NULL on every live receipt, so a unique
-- constraint (which allows many NULLs) is the right shape.
CREATE UNIQUE INDEX customer_receipt_reverses_uq
  ON customer_receipt (reverses_id)
  WHERE reverses_id IS NOT NULL;

ALTER TABLE customer_receipt
  ADD CONSTRAINT customer_receipt_not_self_reversal
  CHECK (reverses_id IS DISTINCT FROM id);

-- The facts of a receipt do not change. journal_entry_id is written once, on
-- the insert that posts it; everything describing what arrived is frozen.
-- Settling a reversal means another row, not an edit of this one.
CREATE TRIGGER customer_receipt_frozen_facts
  BEFORE UPDATE ON customer_receipt
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'tenant_id', 'company_id', 'customer_id', 'receipt_number', 'uuid',
    'received_on', 'method', 'reference', 'amount', 'currency', 'reverses_id',
    'created_by');

CREATE TRIGGER customer_receipt_no_delete BEFORE DELETE ON customer_receipt
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

CREATE TRIGGER customer_receipt_allocation_immutable
  BEFORE UPDATE OR DELETE ON customer_receipt_allocation
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- Open invoices now net reversing receipts out of what was received
-- ---------------------------------------------------------------------------
--
-- Signature unchanged, so the functions that read this one do not need to be
-- dropped. The body is the same as 0036 except the received sum: a reversing
-- receipt's allocations count as money OUT, which is how the invoice they
-- settled becomes open again.

CREATE OR REPLACE FUNCTION customer_open_invoices(p_company_id uuid)
RETURNS TABLE (
  invoice_id   uuid,
  customer_id  uuid,
  human_number text,
  issue_date   date,
  due_date     date,
  on_account   numeric,
  credited     numeric,
  received     numeric,
  outstanding  numeric
)
LANGUAGE sql STABLE AS $$
  SELECT
    i.id, i.customer_id, coalesce(i.human_number, ''),
    i.issue_date::date,
    (i.issue_date + make_interval(days => coalesce(c.payment_terms_days, 0)))::date,
    d.on_account,
    coalesce(n.credited, 0),
    coalesce(r.received, 0),
    d.on_account - coalesce(n.credited, 0) - coalesce(r.received, 0)
  FROM sales_invoice i
  JOIN customer c ON c.id = i.customer_id
  CROSS JOIN LATERAL (
    SELECT coalesce(sum(t.amount), 0) AS on_account
    FROM sales_tender t
    WHERE t.invoice_id = i.id AND t.method = 'customer_due'
  ) d
  LEFT JOIN (
    SELECT cn.parent_invoice_id AS invoice_id, sum(f.amount) AS credited
    FROM sales_invoice cn
    JOIN sales_refund f ON f.credit_note_id = cn.id
    WHERE cn.doc_type = 'credit_note'
      AND cn.parent_invoice_id IS NOT NULL
      AND f.method = 'customer_due'
    GROUP BY cn.parent_invoice_id
  ) n ON n.invoice_id = i.id
  LEFT JOIN (
    SELECT a.invoice_id,
           sum(CASE WHEN cr.reverses_id IS NULL THEN a.amount ELSE -a.amount END)
             AS received
    FROM customer_receipt_allocation a
    JOIN customer_receipt cr ON cr.id = a.receipt_id
    GROUP BY a.invoice_id
  ) r ON r.invoice_id = i.id
  WHERE i.company_id = p_company_id
    AND i.customer_id IS NOT NULL
    AND i.doc_type <> 'credit_note'
    AND d.on_account > 0
$$;
