-- 0048 — point the variance account at the role the posting rules actually ask
-- for.
--
-- Posting rules name account ROLES, and 0025's rule 11 (inventory.variance)
-- with 0026's rule 11a (inventory.variance_favourable) both name
-- `cost_variance`. The chart of accounts seeded by provisioning mapped account
-- 5150 "Inventory Cost Variance" to `inventory_variance` instead. The two names
-- never met, so in any company created through the product the posting engine
-- could not resolve the role and every variance failed to post.
--
-- It was invisible for the usual reason: the integration test covering rule 11
-- created its own variance account and mapped `cost_variance` by hand before
-- selling, so it proved the rule and the engine while stepping over the wiring
-- between them. The problem only appears in a company whose chart came from
-- SeedChartOfAccounts, which is every real one.
--
-- The rules keep their name and the mapping moves, rather than the other way
-- round. A posting rule is versioned data an auditor can be shown — "this rule
-- produced that entry" — and rewriting the payload of a live rule to fix a
-- mapping mistake would edit that history for no reason. The role mapping is
-- per-company configuration and is the thing that was wrong.
--
-- Renamed rather than added alongside, so a company cannot end up with a
-- variance split across two accounts depending on which name a future caller
-- used.

-- Per tenant, for the reason 0042 spells out: account_role_map has FORCE ROW
-- LEVEL SECURITY and a policy of tenant_id = current_tenant_id(), so a bare
-- UPDATE in a migration matches nothing and reports success. That is how 0037
-- managed to grant no permissions at all while appearing to work.
DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    UPDATE account_role_map SET role = 'cost_variance'
    WHERE role = 'inventory_variance'
      -- A company that already has cost_variance mapped keeps it. Renaming into
      -- an existing key would violate the primary key, and repointing a role
      -- already carrying a balance at a different account would split that
      -- balance mid-year.
      AND NOT EXISTS (
        SELECT 1 FROM account_role_map m
        WHERE m.company_id = account_role_map.company_id
          AND m.role = 'cost_variance'
      );
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', '', true);
END $$;
