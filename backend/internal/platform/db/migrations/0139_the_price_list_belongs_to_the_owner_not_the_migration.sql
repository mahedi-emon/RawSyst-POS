-- Letting the software owner change what a plan includes.
--
-- # What was wrong
--
-- `plan_feature` says which modules a tier sells. `plan_tier_default` says what
-- ceilings a tier carries. Both are the commercial shape of the product, both
-- are things a software owner changes as they learn what sells — and both were
-- writable only by a migration.
--
-- So "put analytics in Professional" or "raise Starter to ten users" was a code
-- change, a review, a build and a deploy. For a decision that is a sentence.
--
-- The control plane already lets an operator override a feature for ONE client
-- (`tenant_feature`) and raise ONE client's ceilings (`tenant_limit`). What it
-- could not do was change the plan everybody is on, which is the more common
-- act and the one that scales.
--
-- # Why this is a policy change and not new tables
--
-- The tables are right. What was missing is permission to write them.
--
-- `plan_feature` had `USING (true)` and no `WITH CHECK`, which under FORCE row
-- level security reads to everybody and refuses every INSERT. `plan_tier_default`
-- had no row-level security at all, which is a different problem with the same
-- practical result: it is not reachable through the pooled application role in
-- any deliberate way.
--
-- Both become: readable by everybody, writable only by the platform.
--
-- # Why a tenant may still READ them
--
-- Because a business has to be told what its plan includes and what its
-- ceilings are — that is the subscription screen, and `GET /plans` is
-- deliberately public to any signed-in caller. A price list is not a secret.
-- What a tenant must never do is edit it, and that is what the write side
-- below confines to `is_platform_admin()`.

-- --------------------------------------------------------------------------
-- plan_feature: which modules a tier sells
-- --------------------------------------------------------------------------

-- The read policy stays exactly as it was. Replacing it rather than adding
-- beside it, because two permissive policies on one command is a rule nobody
-- can read later.
DROP POLICY IF EXISTS plan_feature_readable ON plan_feature;

CREATE POLICY plan_feature_readable ON plan_feature
  FOR SELECT USING (true);

-- Writes, platform only. Three separate policies rather than FOR ALL, so the
-- INSERT case carries its own WITH CHECK and is not inherited by accident.
CREATE POLICY plan_feature_platform_insert ON plan_feature
  FOR INSERT WITH CHECK (is_platform_admin());

CREATE POLICY plan_feature_platform_update ON plan_feature
  FOR UPDATE USING (is_platform_admin()) WITH CHECK (is_platform_admin());

CREATE POLICY plan_feature_platform_delete ON plan_feature
  FOR DELETE USING (is_platform_admin());

-- --------------------------------------------------------------------------
-- plan_tier_default: what ceilings a tier carries
-- --------------------------------------------------------------------------
--
-- This table had no row-level security at all. Enabling it is the change; the
-- policies below then restore reading for everybody, which is what it had
-- before, and confine writing to the platform, which is what it did not have.

ALTER TABLE plan_tier_default ENABLE ROW LEVEL SECURITY;
ALTER TABLE plan_tier_default FORCE  ROW LEVEL SECURITY;

CREATE POLICY plan_tier_default_readable ON plan_tier_default
  FOR SELECT USING (true);

CREATE POLICY plan_tier_default_platform_update ON plan_tier_default
  FOR UPDATE USING (is_platform_admin()) WITH CHECK (is_platform_admin());

-- No INSERT and no DELETE policy, deliberately.
--
-- The four tiers are the `plan_tier` enum. A fifth is a schema change and a
-- migration, because every tier needs seeded features, a place in the pricing
-- and a decision about what it contains — none of which a form can supply. A
-- DELETE would orphan every tenant on that tier.
--
-- So this table's rows are fixed and their VALUES are editable, which is
-- exactly the shape the product needs.
