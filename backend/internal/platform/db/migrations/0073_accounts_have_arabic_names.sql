-- 0073 — the chart of accounts had no Arabic, so an Arabic shop read its own
-- books in English.
--
-- # What was wrong
--
-- `account.translations` has existed since 0015 and nothing ever wrote to it.
-- Every account was seeded with an English name and no other, so an
-- Arabic-speaking owner opening their own dashboard read "Stock Write-off",
-- "Cash Over/Short", "Bank & Card Charges" and "Marketing" in the middle of an
-- otherwise Arabic screen — on the one panel that tells them where their money
-- went today.
--
-- Found by looking at the Arabic dashboard. Nothing could have reported it: the
-- names are data, not strings in a component, so the translation coverage test
-- has no view of them and never will.
--
-- # What this does
--
-- Fills in the Arabic name for the accounts provisioning creates, matched by
-- code. `SeedChartOfAccounts` now writes the same names for a new company, so
-- this is the backfill for every company that already exists.
--
-- # What it deliberately does not do
--
-- It does not touch the English name, and it does not touch an account a shop
-- added or renamed itself. A chart is a shop's own document: a company that
-- has written its own Arabic for an account keeps it, because the merge below
-- only fills a key that is absent.
--
-- Matched on code AND on the English name. A shop that reused code 5200 for
-- something of its own would otherwise be given the Arabic for "Rent" over the
-- top of it — the codes are a default, not a reservation.

DO $$
DECLARE
  c record;
  a record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR c IN SELECT id, tenant_id FROM company LOOP
    -- The line 0071 was missing and 0072 had to add. `account` has FORCE ROW
    -- LEVEL SECURITY, so a statement with no tenant context matches no rows
    -- and reports no error.
    PERFORM set_config('app.tenant_id', c.tenant_id::text, true);

    FOR a IN
      SELECT * FROM (VALUES
        ('1100', 'Cash',                        'النقد'),
        ('1110', 'Bank',                        'البنك'),
        ('1150', 'Card Settlement Clearing',    'مقاصة تسوية البطاقات'),
        ('1200', 'Accounts Receivable',         'الذمم المدينة'),
        ('1400', 'Inventory',                   'المخزون'),
        ('2100', 'Accounts Payable',            'الذمم الدائنة'),
        ('2150', 'Goods Received Not Invoiced', 'بضاعة مستلمة ولم تُفوتر'),
        ('2200', 'Output VAT Payable',          'ضريبة القيمة المضافة المستحقة'),
        ('2210', 'Input VAT Recoverable',       'ضريبة القيمة المضافة القابلة للاسترداد'),
        ('2300', 'Store Credit Issued',         'رصيد المتجر الصادر'),
        ('2350', 'Exchange Clearing',           'مقاصة الاستبدال'),
        ('2400', 'Loyalty Points Liability',    'التزام نقاط الولاء'),
        ('3100', 'Owner Capital',               'رأس مال المالك'),
        ('3200', 'Retained Earnings',           'الأرباح المبقاة'),
        ('4100', 'Sales Revenue',               'إيرادات المبيعات'),
        ('4200', 'Sales Discounts',             'خصومات المبيعات'),
        ('5100', 'Cost of Goods Sold',          'تكلفة البضاعة المباعة'),
        ('5150', 'Inventory Cost Variance',     'انحراف تكلفة المخزون'),
        ('5200', 'Rent',                        'الإيجار'),
        ('5210', 'Utilities',                   'المرافق'),
        ('5220', 'Salaries',                    'الرواتب'),
        ('5230', 'Marketing',                   'التسويق'),
        ('5300', 'Bank & Card Charges',         'رسوم البنوك والبطاقات'),
        ('5400', 'Stock Write-off',             'إعدام المخزون'),
        ('5500', 'Cash Over/Short',             'زيادة/عجز النقد'),
        ('5900', 'Rounding Differences',        'فروق التقريب')
      ) AS v(code, name, name_ar)
    LOOP
      UPDATE account
         SET translations = translations || jsonb_build_object('ar', a.name_ar)
       WHERE company_id = c.id
         AND code = a.code
         AND name = a.name
         AND coalesce(translations->>'ar', '') = '';
    END LOOP;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END $$;
