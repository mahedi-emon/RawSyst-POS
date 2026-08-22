-- 0058 — The payables half of the tie-out (design 08 §3, QA gate M1).
--
-- Design 08 makes `accounting.tie_out` check three things nightly: "AR
-- sub-ledger = AR control · AP = AP control · inventory valuation = Inventory
-- GL balance". Two of the three already had a function to ask the question
-- with — `receivable_gl_difference` (0035) and `inventory_gl_difference` (0020,
-- restated by 0055). The payables one was never written, so the job could not
-- have been built without inventing a way to ask.
--
-- This is deliberately NOT a new accounting rule. The sub-ledger definition is
-- the one `supplier_ageing` (0031) already uses and the purchasing tests
-- already prove: a bill is outstanding when it is matched, blocked or approved
-- and more is owed on it than has been paid. Restating that predicate here
-- would create a second definition of "what a supplier is owed", and the two
-- would drift — so this reads the ageing function rather than re-deriving it.
-- The same argument `receivable_gl_difference` makes by calling
-- `customer_open_invoices`, and the same reason its comment gives: one
-- question, one answer.
--
-- # The sign
--
-- Payables are a liability, so the control account is credit-normal, while
-- receivables are an asset and debit-normal. `receivable_gl_difference`
-- subtracts (debit − credit); this subtracts (credit − debit), so both return
-- "sub-ledger minus control" as a positive number when the sub-ledger is ahead.
-- Getting this backwards would report every healthy company as diverged by
-- twice its payables, which is the kind of alert that gets switched off.
--
-- # As-of
--
-- `supplier_ageing` takes a date only to place each bill in an ageing bucket.
-- Its `total` column is the sum of everything outstanding regardless of bucket,
-- so the date does not affect what this returns; current_date is passed because
-- the signature requires one.

CREATE OR REPLACE FUNCTION payable_gl_difference(p_company_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce((
    SELECT sum(a.total) FROM supplier_ageing(p_company_id, current_date) a
  ), 0) - coalesce((
    SELECT sum(l.base_credit - l.base_debit)
    FROM journal_line l
    JOIN account acc ON acc.id = l.account_id
    WHERE acc.company_id = p_company_id
      AND acc.is_control AND acc.control_of = 'payable'
  ), 0)
$$;

COMMENT ON FUNCTION payable_gl_difference(uuid) IS
  'C9.3 for payables: supplier sub-ledger less the AP control account. Zero, or there is a problem.';
