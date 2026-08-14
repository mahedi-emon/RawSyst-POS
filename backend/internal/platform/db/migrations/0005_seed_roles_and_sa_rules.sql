-- 0005 — Seed: the twelve predefined role templates, and the Saudi regulatory
-- baseline as UNVERIFIED placeholders.
--
-- Every rule below ships with verified_on = NULL. They are usable in
-- development and fail the release gate, which is how the blueprint's
-- instruction is enforced mechanically rather than by memory:
--
--   "Do not let developers fill these in from assumption."
--
-- The values are the blueprint's own starting figures. They are a place to
-- begin, not an authority. Each must be confirmed against the Tier 1 source
-- named in source_document before this ships to a paying Saudi client.
-- See docs/system-design/90-regulatory-verification-checklist.md.

-- ---------------------------------------------------------------------------
-- Predefined role templates (blueprint A6.1)
-- ---------------------------------------------------------------------------

INSERT INTO role (tenant_id, key, name, name_ar, description, is_system) VALUES
  (NULL, 'owner',            'Owner',                  'المالك',            'Full control within the tenant.', true),
  (NULL, 'store_manager',    'Branch / Store Manager', 'مدير الفرع',        'Sales, stock, staff and approvals. Cannot see bank ledgers or true net profit.', true),
  (NULL, 'cashier',          'Cashier / POS Operator', 'أمين الصندوق',      'Billing, scanning, shift open and close, basic returns. Cost price and margins are always hidden.', true),
  (NULL, 'accountant',       'Accountant',             'المحاسب',           'Accounts, journals, expenses, VAT reports and bank transfers. Cannot edit inventory or products.', true),
  (NULL, 'inventory_keeper', 'Inventory / Warehouse Keeper', 'أمين المستودع', 'Goods receipt, transfers, wastage logging and barcode printing. No pricing or sales access.', true),
  (NULL, 'purchase_manager', 'Purchase Manager',       'مدير المشتريات',    'Purchase requests, orders and supplier communication.', true),
  (NULL, 'hr_manager',       'HR Manager',             'مدير الموارد البشرية', 'Employees, attendance and payroll setup. No sales or inventory access.', true),
  (NULL, 'sales_executive',  'Sales Executive',        'مندوب مبيعات',      'Quotations, orders and their own customer list.', true),
  (NULL, 'delivery_staff',   'Delivery Staff',         'موظف التوصيل',      'Assigned delivery orders only.', true),
  (NULL, 'online_manager',   'Online Order Manager',   'مدير الطلبات الإلكترونية', 'Web and app order queue, packing and dispatch.', true),
  (NULL, 'auditor',          'Auditor',                'المدقق',            'Read-only across everything. For external accountants and auditors.', true),
  (NULL, 'customer_service', 'Customer Service',       'خدمة العملاء',      'Customer profiles, tickets and returns. No financial data.', true);

-- Permissions for the roles that exist in Phase 1. The remaining templates are
-- populated as their modules ship, so a role never silently grants access to a
-- module that has not been security-reviewed.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  -- Owner: everything currently defined. Kept explicit rather than a wildcard,
  -- so adding a module is a deliberate grant rather than an automatic one.
  ('owner', 'catalog.view'), ('owner', 'catalog.create'), ('owner', 'catalog.edit'),
  ('owner', 'catalog.delete'), ('owner', 'catalog.view_cost_price'), ('owner', 'catalog.view_profit_margin'),
  ('owner', 'sales.view'), ('owner', 'sales.create'), ('owner', 'sales.refund'),
  ('owner', 'sales.discount'), ('owner', 'sales.hold'), ('owner', 'sales.exchange'),
  ('owner', 'sales.receive_payment'), ('owner', 'sales.void_draft'),
  ('owner', 'inventory.view'), ('owner', 'inventory.adjust_stock'), ('owner', 'inventory.transfer_stock'),
  ('owner', 'accounting.view'), ('owner', 'accounting.create'), ('owner', 'accounting.approve'),
  ('owner', 'accounting.close_period'), ('owner', 'accounting.reopen_period'),
  ('owner', 'identity.view'), ('owner', 'identity.create'), ('owner', 'identity.edit'),
  ('owner', 'identity.manage_roles'),
  ('owner', 'compliance.view'), ('owner', 'compliance.retry_submission'),
  ('owner', 'device.view'), ('owner', 'device.manage'),
  ('owner', 'report.view'), ('owner', 'report.export'),

  -- Cashier: deliberately narrow. Cost price and margin are absent, so the
  -- masking layer strips those fields from every payload this role receives.
  ('cashier', 'catalog.view'),
  ('cashier', 'sales.view'), ('cashier', 'sales.create'), ('cashier', 'sales.hold'),
  ('cashier', 'sales.exchange'), ('cashier', 'sales.refund'), ('cashier', 'sales.receive_payment'),
  ('cashier', 'sales.discount'),
  ('cashier', 'inventory.view'),

  -- Store manager: operations and approvals, but no bank ledger and no true
  -- net profit (blueprint A6.1).
  ('store_manager', 'catalog.view'), ('store_manager', 'catalog.edit'),
  ('store_manager', 'catalog.view_cost_price'),
  ('store_manager', 'sales.view'), ('store_manager', 'sales.create'),
  ('store_manager', 'sales.refund'), ('store_manager', 'sales.discount'),
  ('store_manager', 'sales.exchange'), ('store_manager', 'sales.receive_payment'),
  ('store_manager', 'inventory.view'), ('store_manager', 'inventory.adjust_stock'),
  ('store_manager', 'inventory.transfer_stock'),
  ('store_manager', 'compliance.view'),
  ('store_manager', 'device.view'),
  ('store_manager', 'report.view'),

  -- Accountant: the books, but not the catalogue (blueprint A6.1).
  ('accountant', 'catalog.view'), ('accountant', 'catalog.view_cost_price'),
  ('accountant', 'sales.view'),
  ('accountant', 'inventory.view'),
  ('accountant', 'accounting.view'), ('accountant', 'accounting.create'),
  ('accountant', 'accounting.close_period'),
  ('accountant', 'compliance.view'),
  ('accountant', 'report.view'), ('accountant', 'report.export'),

  -- Inventory keeper: stock only, no pricing or sales (blueprint A6.1).
  ('inventory_keeper', 'catalog.view'),
  ('inventory_keeper', 'inventory.view'), ('inventory_keeper', 'inventory.adjust_stock'),
  ('inventory_keeper', 'inventory.transfer_stock'),

  -- Auditor: sees everything, changes nothing.
  ('auditor', 'catalog.view'), ('auditor', 'catalog.view_cost_price'),
  ('auditor', 'catalog.view_profit_margin'),
  ('auditor', 'sales.view'), ('auditor', 'inventory.view'),
  ('auditor', 'accounting.view'), ('auditor', 'compliance.view'),
  ('auditor', 'identity.view'), ('auditor', 'device.view'),
  ('auditor', 'report.view'), ('auditor', 'report.export')
) AS p(role_key, permission) ON p.role_key = r.key
WHERE r.tenant_id IS NULL;

-- ---------------------------------------------------------------------------
-- Saudi regulatory baseline — ALL UNVERIFIED
-- ---------------------------------------------------------------------------

INSERT INTO regulatory_rule
  (rule_key, country, payload, effective_from, source_authority, source_document, source_url, release_blocker, notes)
VALUES

-- VAT ------------------------------------------------------------------------
('SA.VAT.STANDARD_RATE', 'sa', '{"rate": "0.15"}'::jsonb, '2020-07-01',
 'zatca', 'VAT Implementing Regulations', 'https://zatca.gov.sa', false,
 'Blueprint states 15%. Confirm the current standard rate and its effective date.'),

('SA.VAT.MANDATORY_REGISTRATION_THRESHOLD', 'sa', '{"amount": "375000", "currency": "SAR", "period": "annual"}'::jsonb, '2020-07-01',
 'zatca', 'VAT Implementing Regulations', 'https://zatca.gov.sa', false,
 'Blueprint states SAR 375,000 annual taxable turnover.'),

('SA.VAT.VOLUNTARY_REGISTRATION_THRESHOLD', 'sa', '{"amount": "187500", "currency": "SAR", "period": "annual"}'::jsonb, '2020-07-01',
 'zatca', 'VAT Implementing Regulations', 'https://zatca.gov.sa', false,
 'Blueprint states SAR 187,500.'),

('SA.VAT.MONTHLY_FILING_THRESHOLD', 'sa', '{"amount": "40000000", "currency": "SAR", "period": "annual"}'::jsonb, '2020-07-01',
 'zatca', 'VAT guidance', 'https://zatca.gov.sa', false,
 'Above this, filing is monthly; below, quarterly. Changing filing period requires ZATCA approval.'),

('SA.VAT.FILING_DUE_RULE', 'sa', '{"rule": "last_day_of_month_following_period_end"}'::jsonb, '2020-07-01',
 'zatca', 'VAT guidance', 'https://zatca.gov.sa', false,
 'Return AND payment due by the last day of the month following period end.'),

('SA.VAT.RECORD_RETENTION', 'sa', '{"years": 6, "extended_asset_years": 11}'::jsonb, '2020-07-01',
 'zatca', 'VAT Implementing Regulations', 'https://zatca.gov.sa', false,
 'At least 6 years; longer for certain capital assets (blueprint suggests ~11 for real estate). Drives archive design and the legal-hold override of PDPL deletion.'),

('SA.VAT.TAX_TREATMENTS', 'sa',
 '{"treatments": ["standard", "zero_rated", "exempt", "out_of_scope", "export", "reverse_charge", "import"]}'::jsonb,
 '2020-07-01', 'zatca', 'VAT Implementing Regulations', 'https://zatca.gov.sa', false,
 'Seven treatments per product line. Exemption reason codes are required on the invoice for non-standard treatment.'),

-- ZATCA e-invoicing -----------------------------------------------------------
('SA.ZATCA.XML_SCHEMA_VERSION', 'sa', '{"version": "__VERIFY__"}'::jsonb, '2024-01-01',
 'zatca', 'E-Invoicing XML Implementation Standard', 'https://zatca.gov.sa', true,
 'RELEASE BLOCKER (E8.4 #3). Must match the schema version ZATCA currently accepts. Each archived invoice records the version it was signed under.'),

('SA.ZATCA.QR_TLV_FIELDS', 'sa', '{"fields": "__VERIFY__"}'::jsonb, '2024-01-01',
 'zatca', 'E-Invoicing Security Features Implementation Standard', 'https://zatca.gov.sa', true,
 'RELEASE BLOCKER. Exact TLV field set and byte layout. Wrong bytes mean invoices rejected at scale.'),

('SA.ZATCA.HASH_ALGORITHM', 'sa', '{"algorithm": "SHA-256", "canonicalization": "__VERIFY__"}'::jsonb, '2024-01-01',
 'zatca', 'E-Invoicing Security Features Implementation Standard', 'https://zatca.gov.sa', true,
 'RELEASE BLOCKER. Hash construction and canonicalization for the PIH chain.'),

('SA.ZATCA.REPORTING_WINDOW_HOURS', 'sa', '{"hours": 24}'::jsonb, '2024-01-01',
 'zatca', 'E-Invoicing Detailed Guideline', 'https://zatca.gov.sa', false,
 'Simplified (B2C) invoices are reported after issuance within this window. Drives the offline staleness alerting thresholds.'),

('SA.ZATCA.STANDARD_OFFLINE_TOLERANCE', 'sa', '{"tolerance": "none", "verified": false}'::jsonb, '2024-01-01',
 'zatca', 'E-Invoicing Detailed Guideline', 'https://zatca.gov.sa', false,
 'Blueprint E1.3 line 855 flags this as explicitly unverified. Whether ZATCA permits ANY offline tolerance for standard (B2B) invoices constrains the b2b_offline_policy default. Until confirmed, the safest reading applies: no tolerance.'),

('SA.ZATCA.CSID_RENEWAL_DAYS', 'sa', '{"days": "__VERIFY__"}'::jsonb, '2024-01-01',
 'zatca', 'ZATCA onboarding documentation', 'https://zatca.gov.sa', false,
 'Drives the CSID renewal reminder. CSID lifecycle is an ongoing in-system process, not a one-time manual setup.'),

-- PDPL ------------------------------------------------------------------------
('SA.PDPL.DSR_RESPONSE_DAYS', 'sa', '{"days": 30, "extension_days": 30}'::jsonb, '2024-09-14',
 'sdaia', 'PDPL Implementing Regulation', 'https://sdaia.gov.sa', false,
 'Act on a data-subject request within 30 days, extendable by a further 30 where the request requires unusual effort or the subject has submitted multiple requests.'),

('SA.PDPL.BREACH_NOTIFICATION_HOURS', 'sa', '{"hours": 72}'::jsonb, '2024-09-14',
 'sdaia', 'PDPL Implementing Regulation', 'https://sdaia.gov.sa', false,
 'Notify SDAIA within 72 hours of BECOMING AWARE of a breach that may harm personal data or data-subject rights. A countdown starts automatically the moment an incident is logged.'),

('SA.PDPL.CROSS_BORDER_TRANSFER', 'sa', '{"conditions": "__VERIFY__"}'::jsonb, '2024-09-14',
 'sdaia', 'Regulation on Personal Data Transfer Outside the Kingdom', 'https://sdaia.gov.sa', false,
 'Determines hosting architecture. Blocks deploying a second data region until confirmed.'),

-- GOSI ------------------------------------------------------------------------
('SA.GOSI.RATES', 'sa',
 '{"saudi_post_jul2024": {"employer": "__VERIFY__", "employee": "__VERIFY__"},
   "saudi_pre_jul2024":  {"employer": "__VERIFY__", "employee": "__VERIFY__"},
   "expatriate":         {"employer": "__VERIFY__", "employee": "0"},
   "wage_cap": "__VERIFY__"}'::jsonb,
 '2026-01-01', 'gosi', 'GOSI contribution rate schedule', 'https://gosi.gov.sa', true,
 'RELEASE BLOCKER (E8.4 #2). Rates differ by nationality AND by hire date relative to the July 2024 Social Insurance Law, and step upward on a legislated schedule through 2028. Must be entered as a COMPLETE DATED SCHEDULE — one row per period — not a single current figure. Payroll resolves at the pay period being processed so re-running an old month gives the historically correct result.'),

-- WPS / Mudad -----------------------------------------------------------------
('SA.WPS.WAGE_FILE_FORMAT', 'sa', '{"format": "__VERIFY__", "version": "__VERIFY__"}'::jsonb, '2026-01-01',
 'mhrsd', 'Mudad wage-file specification', 'https://mudad.com.sa', true,
 'RELEASE BLOCKER (E8.4 #1). File layouts change without publicity. Pull from the live Mudad specification immediately before build AND re-check before every payroll-module release. The generator is built against a versioned format definition so a change is a registry update plus a template swap, not a rewrite.'),

('SA.WPS.SUBMISSION_TIMING', 'sa',
 '{"lead_time_business_days": "__VERIFY__", "payment_window_days": "__VERIFY__"}'::jsonb,
 '2026-01-01', 'mhrsd', 'Mudad submission rules', 'https://mudad.com.sa', false,
 'Blueprint values are directionally correct only. Mudad cross-references GOSI and Qiwa; a mismatch can freeze portal access.'),

-- E-Commerce ------------------------------------------------------------------
('SA.ECOMMERCE.COOLING_OFF_DAYS', 'sa', '{"days": 14, "category_exemptions": "__VERIFY__"}'::jsonb, '2020-01-01',
 'moc', 'E-Commerce Law and Implementing Regulations', 'https://mc.gov.sa', false,
 'Some categories are legally exempt from the return right; configurable per product/category.'),

('SA.ECOMMERCE.REGISTRATION_CHANNEL', 'sa', '{"channel": "__VERIFY__"}'::jsonb, '2020-01-01',
 'moc', 'Ministry of Commerce registration guidance', 'https://business.sa', false,
 'Actively transitioning between Maroof and business.sa. Held as a configurable per-tenant field so it can point at whichever channel is live without a code change.');
