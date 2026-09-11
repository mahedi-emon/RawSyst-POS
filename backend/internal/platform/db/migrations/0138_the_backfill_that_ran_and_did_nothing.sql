-- Migration 0137 again, this time able to see the rows it was written for.
--
-- # What went wrong
--
-- 0137 backfills a `subscription` row for every tenant that has none. It ran,
-- it reported success, it was recorded in `schema_migration`, and it inserted
-- nothing at all.
--
-- Migrations run on an ordinary pool connection. `Pool.Migrate` sets no GUC,
-- because the schema changes it usually carries do not need one. Row-level
-- security on `tenant` is FORCED and its policy is
--
--     USING (id = current_tenant_id() OR is_platform_admin())
--
-- With neither `app.tenant_id` nor `app.platform_admin` set, both halves are
-- false for every row. So
--
--     INSERT INTO subscription ... SELECT ... FROM tenant t WHERE NOT EXISTS ...
--
-- selected from an empty table and inserted zero rows. No error, because
-- inserting nothing is not an error.
--
-- That is the whole failure mode worth naming: a data migration hidden behind
-- row-level security does not fail, it silently does nothing, and the evidence
-- that it "ran" is recorded either way. The schema version said 137 and the
-- table it was supposed to fill was untouched.
--
-- It was caught by reading `tenants_without_subscription` on the platform
-- dashboard immediately afterwards and seeing a number that should have been
-- zero. That figure was added in the same phase, for a different reason, and
-- it is the only thing that would have shown this at all.
--
-- # Why a new migration rather than a fix to 0137
--
-- Because 0137 is applied. `Pool.Migrate` hashes every migration and refuses to
-- start if one has changed since it was recorded -- "Add a new migration
-- instead of editing history", in its own words. Editing 0137 would break
-- every deployment that already has it, including this one.
--
-- # The pattern, which already existed
--
-- 0042 and a dozen migrations after it wrap tenant-scoped backfills in a DO
-- block that sets `app.platform_admin` locally and clears it at the end. That
-- is the established way to do this and 0137 simply did not use it. Local, so
-- the setting dies with the transaction rather than leaking into the pooled
-- connection that carries it.

DO $$
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  -- Identical to 0137's statement. See that migration for why each value is
  -- what it is: the columns say exactly what the read path was already
  -- coalescing them to, except started_on, which comes from the tenant's own
  -- created_at rather than claiming every existing client signed up today.
  --
  -- current_period_end stays NULL on purpose. The platform never recorded when
  -- these subscriptions run to, and inventing a date would be an invented
  -- commercial fact whose first act would be to expire somebody.
  INSERT INTO subscription (tenant_id, tier, cycle, price, currency, status,
                            started_on, current_period_end)
  SELECT t.id, t.plan_tier, 'monthly', 0, 'SAR', 'active',
         t.created_at::date, NULL
  FROM tenant t
  WHERE NOT EXISTS (
    SELECT 1 FROM subscription s WHERE s.tenant_id = t.id
  );

  PERFORM set_config('app.platform_admin', '', true);
END $$;
