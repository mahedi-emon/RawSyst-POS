-- 0070 — Repair a posting rule left behind by a test, without weakening
-- anything that protects a real one.
--
-- # What is wrong, and where
--
-- A development database picked up a second version of `sale.revenue`:
--
--   version 2, effective_from 2026-09-01, description
--   'Later shape, for testing effective dating', with a line naming the
--   account role `deferred_revenue`.
--
-- It is in no migration. It was written by an earlier form of
-- TestARuleIsResolvedAtTheTransactionDate, which edited a REAL rule to prove
-- that resolution is dated. That test has since been rewritten to use a
-- throwaway `test.*` key and says why in its own comment — posting_rule is
-- platform-global and has no delete, so a test that edits a real rule leaves it
-- edited for every later run and for every other test.
--
-- The row it left behind is not cosmetic. ResolveRule takes the highest version
-- in force at the transaction date, so from 1 September 2026 every sale in that
-- database resolves to a rule naming a role no chart of accounts maps, and no
-- sale can be posted at all. TestEverySeededRuleResolvesAgainstAProvisionedChart
-- is what found it, which is the guard working.
--
-- # Why this supersedes rather than deletes
--
-- posting_rule carries `reject_delete()` and a `posting_rule_frozen` trigger
-- over rule_key, lines and version, and those are not obstacles to work around
-- — they are why an accountant can trust that the rule an entry was posted
-- under still says what it said. Deleting the row needs a superuser and
-- `session_replication_role = replica`, which disables EVERY trigger on the
-- connection: the immutability of journal entries, the period lock, the
-- write-once stamp. Turning all of that off to tidy one row is a far worse
-- trade than the row.
--
-- So this uses the mechanism the registry is built on instead. A version 3,
-- carrying the same lines as the version 1 that has been correct since 2020,
-- effective from the same date the bad one starts. Highest version in force
-- wins, so from 1 September the correct shape is what resolves, and the bad row
-- stays exactly where it is as the historical record of what happened.
--
-- # It does nothing at all on a clean database
--
-- Guarded on the bad row being present. A database that never ran that test —
-- which is every production database, and every database created from these
-- migrations — gets no new rule.

DO $$
DECLARE
  correct jsonb;
  starts  date;
  nextver integer;
BEGIN
  SELECT effective_from INTO starts
  FROM posting_rule
  WHERE rule_key = 'sale.revenue'
    AND lines::text LIKE '%deferred_revenue%'
  ORDER BY effective_from
  LIMIT 1;

  IF starts IS NULL THEN
    RETURN;   -- nothing to repair
  END IF;

  -- The shape that has been right since the chart of accounts was seeded.
  SELECT lines INTO correct
  FROM posting_rule
  WHERE rule_key = 'sale.revenue'
    AND lines::text NOT LIKE '%deferred_revenue%'
  ORDER BY version DESC
  LIMIT 1;

  IF correct IS NULL THEN
    RAISE EXCEPTION
      'sale.revenue has no version that maps to a provisioned chart, so there '
      'is nothing to supersede the leaked one with. Investigate rather than '
      'letting this migration invent a rule.';
  END IF;

  SELECT max(version) + 1 INTO nextver
  FROM posting_rule WHERE rule_key = 'sale.revenue';

  INSERT INTO posting_rule
    (rule_key, country, version, lines, description, effective_from)
  VALUES
    ('sale.revenue', NULL, nextver, correct,
     'Supersedes a test rule left behind in a development database (0070). '
     'Same shape as the version in force before it.',
     starts);
END $$;
