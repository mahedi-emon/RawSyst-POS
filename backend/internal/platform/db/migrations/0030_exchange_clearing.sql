-- Exchanges: the instrument that lets a credit note and an invoice settle
-- against each other without pretending money moved.
--
-- A customer swapping a 100 item for a 150 one hands over 50. They do not hand
-- over 150 and receive 100 back, and the books must not say they did — the
-- drawer would then be expected to hold cash that never passed through it, and
-- the blind Z-count at close would show a variance with no cause.
--
-- So the offsetting portion of an exchange settles on both documents through
-- `exchange_clearing`, and only the genuine difference moves real money. The
-- account rises on the credit note and falls on the invoice inside one
-- transaction, netting to zero. A balance left on it is therefore a bug with a
-- name, findable by looking at one account rather than by reconciling a day of
-- takings.
--
-- It is deliberately NOT store_credit. Store credit is a real balance a real
-- customer can come back and spend; using it as an internal mechanism would
-- inflate every report of credit issued and outstanding, and an owner would be
-- told their liability had grown when nothing of the sort had happened.

-- ---------------------------------------------------------------------------
-- The method is allowed on both sides of the pair
-- ---------------------------------------------------------------------------

ALTER TABLE sales_tender DROP CONSTRAINT sales_tender_method_valid;
ALTER TABLE sales_tender ADD CONSTRAINT sales_tender_method_valid CHECK (method IN (
  'cash',
  'mada',                -- Saudi national debit. Non-negotiable, own fee band.
  'visa', 'mastercard', 'amex',
  'apple_pay', 'stc_pay', 'samsung_pay',
  'sadad',               -- national bill payment, B2B settlement
  'bank_transfer', 'cheque',
  'tabby', 'tamara',     -- buy-now-pay-later
  'bkash', 'nagad',      -- Bangladesh mobile money
  'store_credit',
  'loyalty_points',
  'customer_due',        -- sold on account; settles into receivables
  'exchange_clearing'    -- the offsetting half of an exchange; never cash
));

ALTER TABLE sales_refund DROP CONSTRAINT sales_refund_method_valid;
ALTER TABLE sales_refund ADD CONSTRAINT sales_refund_method_valid CHECK (method IN (
  'cash', 'mada', 'visa', 'mastercard', 'amex',
  'apple_pay', 'stc_pay', 'samsung_pay', 'sadad',
  'bank_transfer', 'cheque', 'tabby', 'tamara',
  'bkash', 'nagad',
  'store_credit',        -- to the customer's wallet
  'customer_due',        -- reduces what they owe rather than paying out
  'exchange_clearing'    -- the offsetting half of an exchange; never cash
));

-- ---------------------------------------------------------------------------
-- The account behind the role
-- ---------------------------------------------------------------------------

-- Created for every company that already has a chart of accounts, so an
-- exchange works on day one rather than failing the first time somebody tries
-- one. Companies with no accounts yet are skipped: they will be given the role
-- when their chart is set up, and an exchange before that point has nowhere to
-- post either half of itself anyway.
--
-- A liability, not an asset. Between exchanges it is zero; during one it holds
-- what the shop owes the customer for goods already taken back but not yet
-- swapped, which is exactly what a liability is.
INSERT INTO account (tenant_id, company_id, code, name, type)
SELECT DISTINCT a.tenant_id, a.company_id, '2350', 'Exchange Clearing', 'liability'
FROM account a
WHERE NOT EXISTS (
  SELECT 1 FROM account x
  WHERE x.company_id = a.company_id AND x.code = '2350'
);

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, 'exchange_clearing', a.id
FROM account a
WHERE a.code = '2350'
ON CONFLICT (company_id, role) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Proving it nets to zero
-- ---------------------------------------------------------------------------

-- The invariant the whole mechanism rests on, available as a query rather than
-- only as an assertion in Go. An owner, an auditor or a support engineer can
-- ask this directly, and the answer for a healthy company is always zero.
--
-- Non-zero means an exchange settled one half and not the other, which the
-- atomic transaction is supposed to make impossible — so a row here is a real
-- finding, not noise to be filtered.
CREATE OR REPLACE FUNCTION exchange_clearing_balance(p_company_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  -- Base currency, like the trial balance. An exchange settled across two
  -- currencies would otherwise appear unbalanced when it was not.
  SELECT coalesce(sum(l.base_debit), 0) - coalesce(sum(l.base_credit), 0)
  FROM journal_line l
  JOIN journal_entry e ON e.id = l.entry_id
  JOIN account_role_map m
    ON m.account_id = l.account_id AND m.role = 'exchange_clearing'
  WHERE e.company_id = p_company_id
$$;
