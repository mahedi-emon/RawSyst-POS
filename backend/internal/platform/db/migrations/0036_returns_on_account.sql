-- 0036 — A return taken off the account has to come off the receivable too.
--
-- 0035 derived what a customer owes from the customer_due TENDERS on their
-- invoices, less the receipts allocated against them. That is right as far as it
-- goes and it misses one thing: 0019 allows a refund method of `customer_due`,
-- described there as "reduces what they owe rather than paying out".
--
-- Such a refund CREDITS the accounts receivable control account. Nothing in
-- 0035's derivation credited the customer, so C9.3 —
--
--     SUM(customer open balances) == Accounts Receivable control balance
--
-- — would have failed by exactly the refunded amount the first time somebody
-- brought something back on account. The same class of break as the GRNI
-- divergence in 0034, and found the same way: by asking what the invariant says
-- rather than what the happy path does.
--
-- The credit note already names the invoice it corrects, through
-- parent_invoice_id, so the credit lands against the very invoice it relates to
-- rather than floating loose on the account.
--
-- The return signature gains a `credited` column, which CREATE OR REPLACE cannot
-- do, so the four functions are dropped and rebuilt together. Dropping
-- customer_open_invoices alone would fail: the other three read it.

DROP FUNCTION IF EXISTS customer_ageing(uuid, date);
DROP FUNCTION IF EXISTS customer_balance(uuid);
DROP FUNCTION IF EXISTS receivable_gl_difference(uuid);
DROP FUNCTION IF EXISTS customer_open_invoices(uuid);

-- Per invoice: what was sold on account, what has been credited back, what has
-- been received against it, and what is therefore still owed.
--
-- Only the ON-ACCOUNT part of an invoice counts. A sale settled half in cash and
-- half on account owes only the second half, so the receivable is the sum of the
-- customer_due TENDERS rather than the invoice total — otherwise every split
-- payment would show the whole amount as outstanding and the tie-out would fail
-- on the first one.
CREATE FUNCTION customer_open_invoices(p_company_id uuid)
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
    -- Refunds taken off the account, grouped onto the invoice each credit note
    -- corrects.
    SELECT cn.parent_invoice_id AS invoice_id, sum(f.amount) AS credited
    FROM sales_invoice cn
    JOIN sales_refund f ON f.credit_note_id = cn.id
    WHERE cn.doc_type = 'credit_note'
      AND cn.parent_invoice_id IS NOT NULL
      AND f.method = 'customer_due'
    GROUP BY cn.parent_invoice_id
  ) n ON n.invoice_id = i.id
  LEFT JOIN (
    SELECT a.invoice_id, sum(a.amount) AS received
    FROM customer_receipt_allocation a
    GROUP BY a.invoice_id
  ) r ON r.invoice_id = i.id
  WHERE i.company_id = p_company_id
    AND i.customer_id IS NOT NULL
    AND i.doc_type <> 'credit_note'
    AND d.on_account > 0
$$;

-- The invariant of C9.3, as a number. Zero, or the exception to raise.
--
-- Same shape as inventory_gl_difference so the nightly job, the acceptance test
-- and a support engineer all ask one question and get one answer.
CREATE FUNCTION receivable_gl_difference(p_company_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce((
    SELECT sum(o.outstanding) FROM customer_open_invoices(p_company_id) o
  ), 0) - coalesce((
    SELECT sum(l.base_debit - l.base_credit)
    FROM journal_line l
    JOIN account a ON a.id = l.account_id
    WHERE a.company_id = p_company_id
      AND a.is_control AND a.control_of = 'receivable'
  ), 0)
$$;

-- What one customer owes right now, for the credit-limit check.
CREATE FUNCTION customer_balance(p_customer_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(o.outstanding), 0)
  FROM customer c
  CROSS JOIN LATERAL customer_open_invoices(c.company_id) o
  WHERE c.id = p_customer_id AND o.customer_id = p_customer_id
$$;

-- Ageing, measured from the DUE date exactly as the supplier side is.
--
-- A 30-day-terms invoice raised 20 days ago is not overdue, and ageing it from
-- issue would put it in a chasing queue it does not belong in.
CREATE FUNCTION customer_ageing(p_company_id uuid, p_as_of date)
RETURNS TABLE (
  customer_id   uuid,
  customer_name text,
  not_due       numeric,
  days_0_30     numeric,
  days_31_60    numeric,
  days_61_90    numeric,
  days_90_plus  numeric,
  total         numeric
)
LANGUAGE sql STABLE AS $$
  SELECT
    c.id, c.name,
    coalesce(sum(o.outstanding) FILTER (WHERE o.due_date >= p_as_of), 0),
    coalesce(sum(o.outstanding) FILTER (
      WHERE o.due_date < p_as_of AND p_as_of - o.due_date <= 30), 0),
    coalesce(sum(o.outstanding) FILTER (
      WHERE p_as_of - o.due_date BETWEEN 31 AND 60), 0),
    coalesce(sum(o.outstanding) FILTER (
      WHERE p_as_of - o.due_date BETWEEN 61 AND 90), 0),
    coalesce(sum(o.outstanding) FILTER (WHERE p_as_of - o.due_date > 90), 0),
    coalesce(sum(o.outstanding), 0)
  FROM customer c
  JOIN customer_open_invoices(p_company_id) o ON o.customer_id = c.id
  WHERE c.company_id = p_company_id
  GROUP BY c.id, c.name
  HAVING coalesce(sum(o.outstanding), 0) <> 0
  ORDER BY c.name
$$;
