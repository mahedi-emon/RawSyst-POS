-- 0053 — Give equity.contribution the account it has always asked for.
--
-- Rule 12 (0025) credits the role `owner_capital`. The seeded chart offered
-- `owners_equity` on account 3100 and called it "Owner's Equity". The two names
-- never met, so in any company created through the product the posting engine
-- could not resolve the role and a capital contribution would fail on the first
-- day the module owning it posted — exactly how rule 11 behaved for eight
-- migrations until 0048 found it.
--
-- The chart is what drifted, not the rule. Design 12 §1 lists the account as
-- **3100 Owner Capital**; `chart_of_accounts.go` seeded it as "Owner's Equity"
-- with a role to match, and that spelling appears nowhere else in the codebase
-- — no Go file reads it, no other rule names it, and no other migration
-- mentions it. So this is a rename with nothing hanging off it.
--
-- The mapping moves and the rule keeps its name, which is 0048's rule and worth
-- restating: a posting rule is versioned data an auditor can be shown — "this
-- rule produced that entry" — and rewriting the payload of a live rule to fix a
-- chart mistake would edit that history for no reason. The role mapping is
-- per-company configuration and is the thing that was wrong.
--
-- # What this does NOT resolve
--
-- Two things are deliberately left alone, both recorded in PROJECT-STATUS.
--
-- The rule's own description says "owner or investor", while design 02 rule 7
-- and blueprint C3.2 keep investment separate from owner capital — design 12 §1
-- lists 3200 Investor Capital for it. The seeded rule has one role and one
-- credit line, so no mapping can express that distinction; splitting it is a
-- rule change and belongs to whichever milestone builds capital contributions.
--
-- `expense.cash` still names an unmapped `expense` role. That one is not a
-- drift: design 02 rule 5 says "Dr Expense Account", meaning whichever head the
-- transaction is for, and design 12 §1 offers Rent, Utilities, Salaries,
-- Marketing and Bank Charges as separate heads with no generic account among
-- them. A fixed role cannot name "the one the user picked", so mapping it to
-- any single account would be inventing an accounting rule rather than
-- recording one.

-- ---------------------------------------------------------------------------
-- Existing companies
-- ---------------------------------------------------------------------------
--
-- Per tenant, for the reason 0042 spells out and 0048 and 0052 repeat: `account`
-- and `account_role_map` have FORCE ROW LEVEL SECURITY with a predicate of
-- tenant_id = current_tenant_id(), so a bare UPDATE in a migration matches
-- nothing and reports success. That is how 0037 managed to grant no permissions
-- at all while appearing to work.
DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    UPDATE account_role_map SET role = 'owner_capital'
    WHERE role = 'owners_equity'
      -- A company that already has owner_capital mapped keeps it. Renaming into
      -- an existing key would violate the primary key, and repointing a role
      -- already carrying a balance at a different account would split that
      -- balance mid-year.
      AND NOT EXISTS (
        SELECT 1 FROM account_role_map m
        WHERE m.company_id = account_role_map.company_id
          AND m.role = 'owner_capital'
      );

    -- The account's own label, brought into line with the name design 12 §1
    -- gives it. Cosmetic, and done here so a company created before this reads
    -- the same as one created after it rather than the two drifting apart on
    -- screen.
    UPDATE account SET name = 'Owner Capital'
    WHERE code = '3100' AND name = 'Owner''s Equity';
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', '', true);
END $$;
