-- 0042 — Actually give existing businesses the new device permissions.
--
-- 0037 tried to, and silently did nothing. Migrations run with no tenant
-- context and no platform flag, so `role` — which has FORCE ROW LEVEL SECURITY
-- and a policy of `tenant_id = current_tenant_id()` — returned zero rows to the
-- backfill. The INSERT ... SELECT matched nothing and reported success, which is
-- the worst combination: an owner opened Devices and was told they lacked a
-- permission the migration claimed to have granted.
--
-- 0032 and 0033 carry the same latent defect. It has never bitten because every
-- tenant in existence was created AFTER those ran, so provisioning copied the
-- verbs from the template instead. That luck does not extend to any tenant
-- created before this one, and it is recorded in PROJECT-STATUS rather than
-- quietly fixed here — backfilling purchasing verbs is a separate grant that
-- deserves its own deliberate migration, not a side effect of this one.
--
-- The loop sets the tenant for each iteration so the policy is satisfied
-- honestly, rather than reaching for a platform escape that would widen what
-- Super Admin can see for the sake of one INSERT.
DO $$
DECLARE
  t record;
BEGIN
  -- `tenant` is readable here because 0006 gave it the platform predicate and
  -- this block sets it. Nothing else in this migration relies on it.
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    INSERT INTO role_permission (role_id, permission)
    SELECT r.id, p.permission
    FROM role r
    JOIN role template ON template.id = r.cloned_from
    JOIN (VALUES
      ('owner',         'devices.view'),
      ('owner',         'devices.manage'),
      ('store_manager', 'devices.view'),
      ('store_manager', 'devices.manage'),
      ('auditor',       'devices.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', '', true);
END $$;
