-- 0052 — Put the drawer variance in the ledger, where design 11 §9 says it goes.
--
-- The Z report has always computed the variance and written it onto
-- cash_session, and stopped there. `shift.Close` called nothing in accounting
-- and no `cash_over_short` role existed anywhere in the schema, so a shop could
-- run short every day for a month and the accounts would never show it: Cash
-- carried a balance the drawer had never held, and the loss appeared nowhere in
-- the P&L. Design 11 §9 is explicit that the variance "posts to a Cash
-- Over/Short account rather than being absorbed silently — an unexplained
-- drawer difference is information".
--
-- # The account
--
-- 5500 Cash Over/Short, as design 12 §1 lists it, in the expense range where
-- that document puts it. Added to the seeded chart AND mapped for every company
-- that already exists, in the same migration. That pairing is the whole lesson
-- of 0048: rules 11 and 11a named `cost_variance` while the chart mapped
-- `inventory_variance`, so the engine failed on an unresolvable role in every
-- company created through the product, and the test that covered the rule
-- mapped the role by hand and never saw it. A role without a mapping is not a
-- half-finished feature, it is a rule that throws.
--
-- # Two rules, not one signed rule
--
-- Exactly the arrangement 0025 and 0026 established for the costing variance: a
-- positive amount either way, with the sides swapped, because a single rule
-- taking a signed figure writes a negative debit where a credit belongs and a
-- trial balance carrying negative debits is one an accountant cannot read.
--
--   shortage — counted less than expected. The cash is not there, so the asset
--              comes down and the shop wears the difference as an expense.
--              Dr Cash Over/Short / Cr Cash
--
--   overage  — counted more than expected. Cash the books did not know about,
--              so the asset goes up and the same account carries the other
--              side. It is not styled as income anywhere: an unexplained
--              surplus is as much a control failure as a shortfall, and posting
--              it to Other Income would flatter the month it happened in.
--              Dr Cash / Cr Cash Over/Short
--
-- An exact drawer posts nothing. There is no entry to make, and a run of
-- zero-value journal entries would bury the shifts that actually went wrong.

-- ---------------------------------------------------------------------------
-- The rules
-- ---------------------------------------------------------------------------

INSERT INTO posting_rule
  (rule_key, country, version, lines, description, effective_from)
VALUES
('cash.shortage', NULL, 1,
 '[{"role": "cash_over_short", "side": "debit",  "amount": "variance"},
   {"role": "cash",            "side": "credit", "amount": "variance"}]'::jsonb,
 'A till closed with less cash in the drawer than the shift expected.',
 '2020-01-01'),

('cash.overage', NULL, 1,
 '[{"role": "cash",            "side": "debit",  "amount": "variance"},
   {"role": "cash_over_short", "side": "credit", "amount": "variance"}]'::jsonb,
 'A till closed with more cash in the drawer than the shift expected.',
 '2020-01-01')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The account, for every company that already has a chart
-- ---------------------------------------------------------------------------
--
-- Per tenant, for the reason 0042 spells out and 0048 repeats: `account` and
-- `account_role_map` have FORCE ROW LEVEL SECURITY with a predicate of
-- tenant_id = current_tenant_id(), so a bare INSERT in a migration matches
-- nothing and reports success. That is how 0037 managed to grant no permissions
-- at all while appearing to work.
--
-- The loop sets the tenant per iteration so the policy is satisfied honestly,
-- rather than reaching for a platform escape that would widen what Super Admin
-- can see for the sake of one INSERT.
DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    -- Only companies that already have a chart. A company with no accounts at
    -- all has not been through provisioning yet and will get 5500 from the
    -- seed when it is, so inventing a lone account here would leave it with a
    -- chart of one.
    INSERT INTO account (tenant_id, company_id, code, name, type, is_control)
    SELECT t.id, c.id, '5500', 'Cash Over/Short', 'expense', false
    FROM company c
    WHERE c.tenant_id = t.id
      AND EXISTS (SELECT 1 FROM account a WHERE a.company_id = c.id)
    ON CONFLICT (company_id, code) DO NOTHING;

    -- A company that has already mapped this role keeps its mapping. Repointing
    -- a role that carries a balance at a different account would split that
    -- balance mid-year, which is the trap 0048 had to write around.
    INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
    SELECT t.id, a.company_id, 'cash_over_short', a.id
    FROM account a
    WHERE a.tenant_id = t.id
      AND a.code = '5500'
      AND NOT EXISTS (
        SELECT 1 FROM account_role_map m
        WHERE m.company_id = a.company_id
          AND m.role = 'cash_over_short'
      )
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', '', true);
END $$;
