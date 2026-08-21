-- 0051 — Actually give existing businesses the purchasing verbs.
--
-- 0032 seeded the purchasing permissions and 0033 added the catalogue read a
-- buyer needs to fill in an order. Both wrote to the platform ROLE TEMPLATES
-- only — `WHERE r.tenant_id IS NULL` — and stopped there. Nothing backfilled
-- the copies tenants already held.
--
-- That is a different failure from the one 0042 fixed, and worth stating
-- precisely because the two are easy to conflate. 0037's backfill matched zero
-- rows: it selected over tenant-owned roles from a migration that carries no
-- tenant context, and `role` has FORCE ROW LEVEL SECURITY with a predicate of
-- `tenant_id = current_tenant_id()`, so the policy hid every row and the INSERT
-- reported success having done nothing. 0032 and 0033 by contrast worked
-- exactly as written — `role_isolation` is `tenant_id = current_tenant_id() OR
-- tenant_id IS NULL`, so templates are visible with no context at all, and the
-- templates carry the verbs today. What they never did was reach the clones.
--
-- The effect on a tenant provisioned before 0032 ran is the same either way:
-- its Owner role was copied from a template that did not yet have
-- purchasing.*, so opening Purchasing tells them they lack a permission the
-- module assumes every Owner has. A tenant provisioned afterwards copied the
-- finished template and is unaffected, which is why this has stayed invisible.
--
-- Both grants are replayed here, not just 0032's. The test of this migration is
-- that an old tenant ends up with the permission set a new tenant would get,
-- and a new tenant's clone carries 0033's catalog.view as well. Backfilling one
-- without the other would leave a Purchase Manager able to raise an order and
-- unable to choose a line for it, which is the precise state 0033 existed to
-- prevent.
--
-- Nothing outside those two migrations is granted. In particular this does not
-- reconcile clones against their templates in general: a later verb belongs to
-- the migration that introduces it, the way 0043 handled its own.
--
-- Re-granting cannot override a tenant's own choice, because there is no way
-- for a tenant to have made one — role_permission is written only by
-- provisioning's clone and by migrations, and no role-editing surface exists.
-- If one is ever added, a backfill like this one must first learn to tell a
-- permission a tenant removed from one it never received.
--
-- The loop sets the tenant per iteration so the policy is satisfied honestly,
-- rather than reaching for a platform escape that would widen what Super Admin
-- can see for the sake of one INSERT. ON CONFLICT DO NOTHING makes it idempotent
-- and makes a tenant that already holds a verb a no-op rather than an error.
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
      -- 0032, verbatim. The Owner holds the whole cycle; the Purchase Manager
      -- runs it without approving their own discrepancy or releasing the money;
      -- the Accountant is the counterweight and cannot raise or receive; the
      -- Store Manager receives at the branch; the Auditor reads.
      ('owner',            'purchasing.view'),
      ('owner',            'purchasing.manage_suppliers'),
      ('owner',            'purchasing.create_order'),
      ('owner',            'purchasing.issue_order'),
      ('owner',            'purchasing.receive_goods'),
      ('owner',            'purchasing.record_bill'),
      ('owner',            'purchasing.approve_bill'),
      ('owner',            'purchasing.pay_supplier'),

      ('purchase_manager', 'purchasing.view'),
      ('purchase_manager', 'purchasing.manage_suppliers'),
      ('purchase_manager', 'purchasing.create_order'),
      ('purchase_manager', 'purchasing.issue_order'),
      ('purchase_manager', 'purchasing.receive_goods'),
      ('purchase_manager', 'purchasing.record_bill'),

      ('store_manager',    'purchasing.view'),
      ('store_manager',    'purchasing.receive_goods'),

      ('accountant',       'purchasing.view'),
      ('accountant',       'purchasing.record_bill'),
      ('accountant',       'purchasing.approve_bill'),
      ('accountant',       'purchasing.pay_supplier'),

      ('auditor',          'purchasing.view'),

      -- 0033, verbatim. View only, and deliberately not
      -- catalog.view_cost_price: an order carries the cost the buyer
      -- negotiates, which is a different thing from what every other item in
      -- the shop cost. The Store Manager line is a no-op — 0005 already gave
      -- that role catalog.view — and is kept so this reads as the replay of
      -- 0033 that it is, rather than as a judgement about who needs the read.
      ('purchase_manager', 'catalog.view'),
      ('store_manager',    'catalog.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', '', true);
END $$;
