-- 0043 — Permissions for managing EGS units.
--
-- 0013 made the EGS unit its own entity and left nobody able to create one:
-- every unit in existence came from a dev seed or a test fixture, so a terminal
-- enrolled through the product had `egs_unit_id IS NULL` and was refused at the
-- till by sales.resolveTerminal. These verbs are what closes that.
--
-- # Why not devices.manage
--
-- Pairing a till and onboarding a signing identity are separate acts, and
-- devices.go says so in as many words. An EGS unit carries the VAT registration
-- the invoice chain hangs from and the organization identifier that goes into a
-- CSR; a store manager who must be able to revoke a stolen till at midnight has
-- no business creating one. So the same split C14 uses for
-- customers.set_credit_limit applies here: managing is the Owner's, seeing is
-- everyone's who already reads compliance state.
--
-- einvoicing.view   — see the units, their architecture and their CSID state
-- einvoicing.manage — create a unit, correct its CSR details, point tills at it
--
-- Nothing here grants the right to assert a CSID. There is no verb for that
-- because there is no such operation: the CSID columns are read-only until
-- onboarding is built against a verified ZATCA source.

-- Template roles first. `role_isolation` is
-- `tenant_id = current_tenant_id() OR tenant_id IS NULL`, so the templates are
-- visible with no tenant set and this INSERT does what it says.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',         'einvoicing.view'),
  ('owner',         'einvoicing.manage'),

  -- An accountant answers for the VAT return these invoices roll up into, so
  -- they read the registration details behind them. They do not create units:
  -- that decides how many invoice chains the business has.
  ('accountant',    'einvoicing.view'),

  -- A store manager sees which unit their tills sign under, because that is the
  -- first thing to check when a till refuses a sale.
  ('store_manager', 'einvoicing.view'),

  ('auditor',       'einvoicing.view')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

-- Existing tenants, one at a time, for the reason 0042 records: `role` has
-- FORCE ROW LEVEL SECURITY and a tenant predicate, so an unqualified backfill
-- matches nothing and reports success. Setting the tenant per iteration
-- satisfies the policy honestly rather than reaching for the platform escape.
DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    INSERT INTO role_permission (role_id, permission)
    SELECT r.id, p.permission
    FROM role r
    JOIN role template ON template.id = r.cloned_from
    JOIN (VALUES
      ('owner',         'einvoicing.view'),
      ('owner',         'einvoicing.manage'),
      ('accountant',    'einvoicing.view'),
      ('store_manager', 'einvoicing.view'),
      ('auditor',       'einvoicing.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', '', true);
END $$;
