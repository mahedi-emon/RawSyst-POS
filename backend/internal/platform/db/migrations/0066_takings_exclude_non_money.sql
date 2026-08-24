-- 0066 — "Non-cash takings" was counting money nobody took.
--
-- # What was wrong
--
-- cash_session_report computed non_cash_takings as every tender whose method
-- is not 'cash', less every refund whose method is not 'cash'. That treats
-- "not cash" as a synonym for "money that arrived by another route", and three
-- of the permitted tender methods are not money arriving at all:
--
--   customer_due     the customer paid NOTHING. The sale went on account and
--                    the amount sits in receivables until they settle it.
--   store_credit     the shop already held this money. Redeeming it settles a
--                    liability; nothing new comes in.
--   loyalty_points   the same, against a different liability.
--
-- A fourth, exchange_clearing, is a bookkeeping device rather than a payment.
-- It happens to net to zero within a session -- the offsetting leg appears as a
-- tender on the replacement sale and a refund on the credit note -- so it never
-- moved the total. It is excluded anyway, because relying on two errors
-- cancelling is not the same as not making them.
--
-- # Why it matters
--
-- Blueprint C8 calls the Z report "the definitive daily reconciliation record
-- for the Owner". An Owner reconciles card settlements against the non-cash
-- figure. Inflated by every credit sale and every store-credit redemption of
-- the day, it can never be made to tie, and the natural conclusion is that the
-- acquirer has underpaid.
--
-- # What is deliberately NOT changed
--
-- expected_cash. That has always counted `method = 'cash'` alone and was
-- correct: a sale on account moves no notes, and the drawer never claimed it
-- did. The cashier counting up at close was never at risk; the Owner
-- reconciling the bank was.
--
-- # Why exclusion rather than an allow-list
--
-- Both directions can go wrong when a payment method is added later: an
-- allow-list silently drops a new card type out of takings, an exclusion list
-- silently counts a new liability-settling method as takings.
--
-- The list is closed here AND a test walks the method CHECK constraint and
-- fails on any method it has never been told how to classify. So adding one is
-- a decision somebody has to make rather than a default they inherit.

CREATE OR REPLACE FUNCTION cash_session_report(p_session_id uuid)
RETURNS TABLE (
  session_no        bigint,
  state             text,
  opened_at         timestamptz,
  closed_at         timestamptz,
  opening_float     numeric,
  invoice_count     bigint,
  gross_sales       numeric,
  net_sales         numeric,
  tax_total         numeric,
  refund_total      numeric,
  cash_takings      numeric,
  non_cash_takings  numeric,
  cash_movements    numeric,
  expected_cash     numeric,
  counted_cash      numeric,
  variance          numeric
)
LANGUAGE sql STABLE AS $$
  SELECT
    s.session_no,
    s.state,
    s.opened_at,
    s.closed_at,
    s.opening_float,

    coalesce((SELECT count(*) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type <> 'credit_note'), 0),

    -- Sales figures exclude credit notes, which are reported separately as
    -- refunds. Netting them into "sales" hides how much was handed back, and
    -- that ratio is the single most useful number on a Z report.
    coalesce((SELECT sum(i.total_inclusive) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type <> 'credit_note'), 0),
    coalesce((SELECT sum(i.subtotal_net) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type <> 'credit_note'), 0),
    coalesce((SELECT sum(i.tax_total) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type <> 'credit_note'), 0),
    coalesce((SELECT sum(i.total_inclusive) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type = 'credit_note'), 0),

    cash_session_cash_in(s.id) - cash_session_cash_out(s.id),

    -- Money that actually arrived by some route other than notes. See the note
    -- at the top for why the four excluded methods are not takings.
    coalesce((SELECT sum(t.amount)
              FROM sales_tender t JOIN sales_invoice i ON i.id = t.invoice_id
              WHERE i.cash_session_id = s.id
                AND i.doc_type <> 'credit_note'
                AND t.method NOT IN ('cash', 'customer_due', 'store_credit',
                                     'loyalty_points', 'exchange_clearing')), 0)
    - coalesce((SELECT sum(r.amount)
              FROM sales_refund r JOIN sales_invoice i ON i.id = r.credit_note_id
              WHERE i.cash_session_id = s.id
                AND r.method NOT IN ('cash', 'customer_due', 'store_credit',
                                     'loyalty_points', 'exchange_clearing')), 0),

    coalesce((SELECT sum(m.amount) FROM cash_movement m
              WHERE m.session_id = s.id), 0),

    -- An open session reports what is expected NOW; a closed one reports what
    -- was expected at the moment it was reconciled.
    CASE WHEN s.state = 'closed' THEN s.expected_cash
         ELSE cash_session_expected(s.id) END,
    s.counted_cash,
    s.variance
  FROM cash_session s
  WHERE s.id = p_session_id
$$;

COMMENT ON FUNCTION cash_session_report(uuid) IS
  'X/Z figures for one till session. non_cash_takings counts money that '
  'actually arrived: customer_due, store_credit, loyalty_points and '
  'exchange_clearing are excluded because none of them is a payment being '
  'received. expected_cash counts physical notes only.';
