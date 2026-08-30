-- 0089 — Everybody who runs deliveries can also mark one done.
--
-- 0088 gave `delivery.deliver` to Delivery Staff alone, on the reasoning that
-- marking a consignment delivered is the driver's act. That is true and it is
-- not the whole truth: an Owner is unrestricted within their own tenant by
-- A6.1, a Store Manager covers for a driver who has gone home, and an Online
-- Order Manager closing out the day's dispatches is the ordinary case in a
-- shop with no dedicated drivers at all.
--
-- Left as it was, the smallest shops — the ones where the owner IS the driver —
-- could book a delivery and then not be allowed to say it arrived.
--
-- Delivery Staff keep `delivery.deliver` WITHOUT `delivery.manage`, which is
-- the split that matters: a driver moves consignments along the pipeline and
-- still cannot book new ones, reassign them, or list anybody else's.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, 'delivery.deliver'
FROM role r
WHERE r.tenant_id IS NULL
  AND r.key IN ('owner', 'store_manager', 'online_manager', 'customer_service')
ON CONFLICT DO NOTHING;

DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    INSERT INTO role_permission (role_id, permission)
    SELECT r.id, 'delivery.deliver'
    FROM role r
    JOIN role template ON template.id = r.cloned_from
    WHERE r.tenant_id = t.id
      AND template.key IN ('owner', 'store_manager', 'online_manager',
                           'customer_service')
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;

-- ---------------------------------------------------------------------------
-- The four accounts 0088 introduced, for companies that already existed
-- ---------------------------------------------------------------------------
--
-- 0088 inserted them for every company present WHEN IT RAN, and
-- SeedChartOfAccounts now carries them for every company created afterwards.
-- The gap is a company created between the two, which is a real window on a
-- running installation: a shop provisioned this morning would reach its first
-- warranty repair and be told there is no account mapped to warranty_cost.
--
-- Both statements are idempotent, so this is safe to re-run and safe for a
-- company that already has them.

INSERT INTO account (tenant_id, company_id, code, name, translations, type)
SELECT c.tenant_id, c.id, v.code, v.name,
       jsonb_build_object('ar', v.name_ar), v.kind
FROM company c
CROSS JOIN (VALUES
  ('2500', 'Deferred Finance Income', 'إيرادات تمويل مؤجلة', 'liability'),
  ('4300', 'Finance Income',          'إيرادات التمويل',     'revenue'),
  ('4400', 'Delivery Income',         'إيرادات التوصيل',     'revenue'),
  ('5450', 'Warranty & Service Cost', 'تكلفة الضمان والصيانة', 'expense')
) AS v(code, name, name_ar, kind)
ON CONFLICT (company_id, code) DO NOTHING;

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, v.role, a.id
FROM account a
JOIN (VALUES
  ('2500', 'deferred_finance_income'),
  ('4300', 'finance_income'),
  ('4400', 'delivery_income'),
  ('5450', 'warranty_cost')
) AS v(code, role) ON v.code = a.code
ON CONFLICT DO NOTHING;
