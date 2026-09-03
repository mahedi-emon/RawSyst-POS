-- 0114 — Realised currency gain and loss when a foreign bill is paid.
--
-- G2 asks for "currency gain/loss tracking". 0113 gave the product a rate; this
-- is what happens when the rate has MOVED between owing money and paying it.
--
-- # The difference is real money and has to land somewhere
--
-- A shop buys stock for 1,000 USD when a dollar is worth 3.70 riyals. The
-- payable is 3,700 riyals — that is what the books say the business owes, and
-- what the inventory cost. Two months later it pays the 1,000 USD when a dollar
-- costs 3.80, so 3,800 riyals leave the bank.
--
-- Debit payable 3,700, credit bank 3,800, and the entry does not balance. The
-- missing 100 is not a rounding error and not a new cost of goods: the stock
-- cost what it cost. It is a loss the business took by owing money in a
-- currency that moved, and it belongs in its own line so an owner can see it
-- for what it is rather than finding their margins mysteriously worse.
--
-- The other direction is a gain, and the same argument applies: it is not
-- revenue from trading and must not swell turnover.
--
-- # Two accounts, not one netted account
--
-- Kept apart for the same reason `fixed_assets` and `accumulated_depreciation`
-- are, and `disposal_gain` is separate from `sales_revenue`: a P&L that shows
-- both says what the business gained and what it lost, and one netted figure
-- throws the first away. A month with a 900 gain and an 800 loss is not the
-- same business as a month with a flat 100.
--
-- # Realised only
--
-- This is recognised when money actually moves. Revaluing an OPEN payable at
-- the month-end rate — unrealised gain and loss — is a period-close routine
-- with its own posting and reversal, and is deliberately not attempted here:
-- recognising a movement twice, once as unrealised and again as realised, is
-- the classic way to get this wrong.

-- ---------------------------------------------------------------------------
-- The accounts
-- ---------------------------------------------------------------------------
--
-- Created for every company that already has a chart of accounts, the same way
-- 0030 introduced exchange clearing. Companies provisioned after this migration
-- get them from provisioning.chartOfAccounts, which is the authority for a new
-- company; this only catches the ones that already exist.
--
-- # FORCE row-level security has to come off for this to do anything
--
-- `account` and `account_role_map` are FORCE RLS on `current_tenant_id()`, and
-- a migration connection carries no tenant. The SELECT below would therefore
-- read zero rows and the INSERT would quietly insert nothing — no error, no
-- accounts, and the first foreign-currency payment failing on an unmapped role
-- months later. That is exactly how 0103 was caught, and 0030 has the same
-- latent no-op in it for the same reason.
--
-- Lifted for this transaction only and put back below.

ALTER TABLE account          NO FORCE ROW LEVEL SECURITY;
ALTER TABLE account_role_map NO FORCE ROW LEVEL SECURITY;

INSERT INTO account (tenant_id, company_id, code, name, translations, type)
SELECT DISTINCT a.tenant_id, a.company_id, '4950', 'Foreign Exchange Gain',
       '{"ar": {"name": "أرباح فروق العملة"}}'::jsonb, 'revenue'
FROM account a
WHERE NOT EXISTS (
  SELECT 1 FROM account x
  WHERE x.company_id = a.company_id AND x.code = '4950'
);

INSERT INTO account (tenant_id, company_id, code, name, translations, type)
SELECT DISTINCT a.tenant_id, a.company_id, '5950', 'Foreign Exchange Loss',
       '{"ar": {"name": "خسائر فروق العملة"}}'::jsonb, 'expense'
FROM account a
WHERE NOT EXISTS (
  SELECT 1 FROM account x
  WHERE x.company_id = a.company_id AND x.code = '5950'
);

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, 'fx_gain', a.id
FROM account a WHERE a.code = '4950'
ON CONFLICT (company_id, role) DO NOTHING;

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, 'fx_loss', a.id
FROM account a WHERE a.code = '5950'
ON CONFLICT (company_id, role) DO NOTHING;

ALTER TABLE account          FORCE ROW LEVEL SECURITY;
ALTER TABLE account_role_map FORCE ROW LEVEL SECURITY;

-- ---------------------------------------------------------------------------
-- Rule 7, version 2
-- ---------------------------------------------------------------------------
--
-- The payable is relieved at the rate it was BOOKED at and the money leaves at
-- the rate on the day it left. Those are two different figures the moment a
-- rate moves, so the entry needs a third leg to balance.
--
-- # A new version, not an edit
--
-- `posting_rule.lines` is immutable by trigger, and rightly so: every journal
-- entry records the rule version that produced it, and rewriting a rule in
-- place would leave posted history explained by lines that were not the ones
-- used. So this is version 2, and version 1 stays exactly as it was for the
-- entries that cite it.
--
-- `ResolveRule` takes the highest version whose date window covers the
-- document, so version 2 supersedes version 1 from the same day version 1
-- started. That is deliberate: the Go side now always supplies the new shape,
-- and a payment dated last year being posted today must use it too.
--
-- `payable_relieved` replaces the old single `amount`: in a base-currency
-- payment the two are equal and the behaviour is exactly what it was.
--
-- The two FX legs are groups rather than plain lines so they can be EMPTY. A
-- group with no members expands to no lines at all, so a payment with no
-- difference posts the same two legs it always did — no zero-amount line for a
-- reader of the ledger to wonder about. A gain and a loss cannot both occur on
-- one payment; expressing them as two one-sided groups is what lets a fixed
-- side per template line carry a signed difference.

INSERT INTO posting_rule
  (rule_key, country, version, lines, description, effective_from)
VALUES
('payment.supplier', NULL, 2,
 '[{"role": "accounts_payable", "side": "debit",  "amount": "payable_relieved"},
   {"for_each": "payments",     "side": "credit"},
   {"for_each": "fx_loss",      "side": "debit"},
   {"for_each": "fx_gain",      "side": "credit"}]'::jsonb,
 'Payment to a supplier, settling accounts payable. Where the bill was raised '
 'in another currency, the payable is relieved at the rate it was booked at '
 'and the difference against the payment-day rate is realised as a gain or a '
 'loss.',
 '2020-01-01');

COMMENT ON COLUMN purchase_bill.fx_rate IS
  'Units of the company base currency one unit of the bill currency bought on '
  'the bill date, resolved from exchange_rate. The payable is carried at this '
  'rate for life; the difference against the rate on the day it is paid is '
  'realised through fx_gain / fx_loss (0114).';
