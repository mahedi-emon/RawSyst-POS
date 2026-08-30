-- 0090 — Give existing businesses the sourcing and after-sales verbs.
--
-- 0087 (requisition, RFQ, award) and 0088 (delivery, serials, service,
-- instalments) both granted to the platform role TEMPLATES and then looped over
-- `tenant` to reach the clones. That loop is correct and it ran — but it ran
-- ONCE, at the moment those migrations were applied, and it can only reach
-- tenants that existed then.
--
-- The gap this closes is narrower than 0051's and has exactly the same effect.
-- A tenant provisioned from a template that already carried the verbs is fine.
-- A tenant whose roles were cloned BEFORE 0087 ran got them from the loop. The
-- one left short is a role restored, re-cloned or repaired between the two —
-- and, more importantly, any future migration that re-seeds a role from a
-- template will silently reintroduce the same shortfall unless the reconciling
-- statement is idempotent and re-runnable, which this one is.
--
-- Following 0051's rule exactly: this grants ONLY the verbs 0087 and 0088
-- introduced. It is not a general reconciliation of clones against templates —
-- a later verb still belongs to the migration that introduces it, and a
-- catch-all sync would quietly hand a Cashier whatever a future template gains.
--
-- The same caveat 0051 records applies here: re-granting cannot override a
-- tenant's own choice, because there is no surface through which a tenant can
-- make one. role_permission is written only by provisioning's clone and by
-- migrations. If a role editor is ever built, this must first learn to tell a
-- permission a tenant removed from one it never received.

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
      -- 0087: sourcing.
      ('owner',            'purchasing.request'),
      ('owner',            'purchasing.approve_request'),
      ('owner',            'purchasing.manage_rfq'),
      ('owner',            'purchasing.award_rfq'),
      ('store_manager',    'purchasing.request'),
      ('store_manager',    'purchasing.approve_request'),
      ('purchase_manager', 'purchasing.request'),
      ('purchase_manager', 'purchasing.manage_rfq'),
      ('inventory_keeper', 'purchasing.request'),
      ('accountant',       'purchasing.approve_request'),

      -- 0088: delivery, serials, service, instalments.
      ('owner',            'delivery.view'),    ('owner',            'delivery.manage'),
      ('owner',            'service.view'),     ('owner',            'service.manage'),
      ('owner',            'installment.view'), ('owner',            'installment.manage'),
      ('owner',            'serial.view'),      ('owner',            'serial.manage'),
      ('store_manager',    'delivery.view'),    ('store_manager',    'delivery.manage'),
      ('store_manager',    'service.view'),     ('store_manager',    'service.manage'),
      ('store_manager',    'installment.view'), ('store_manager',    'installment.manage'),
      ('store_manager',    'serial.view'),      ('store_manager',    'serial.manage'),
      ('delivery_staff',   'delivery.view'),    ('delivery_staff',   'delivery.deliver'),
      ('online_manager',   'delivery.view'),    ('online_manager',   'delivery.manage'),
      ('online_manager',   'serial.view'),
      ('cashier',          'serial.view'),
      ('cashier',          'service.view'),
      ('cashier',          'installment.view'),
      ('customer_service', 'service.view'),     ('customer_service', 'service.manage'),
      ('customer_service', 'serial.view'),
      ('customer_service', 'delivery.view'),
      ('customer_service', 'installment.view'),
      ('inventory_keeper', 'serial.view'),      ('inventory_keeper', 'serial.manage'),
      ('accountant',       'installment.view'), ('accountant',       'installment.manage'),
      ('accountant',       'delivery.view'),    ('accountant',       'service.view'),
      ('auditor',          'delivery.view'),    ('auditor',          'service.view'),
      ('auditor',          'installment.view'), ('auditor',          'serial.view'),

      -- 0089: everybody who runs deliveries may also close one.
      ('owner',            'delivery.deliver'),
      ('store_manager',    'delivery.deliver'),
      ('online_manager',   'delivery.deliver'),
      ('customer_service', 'delivery.deliver')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
