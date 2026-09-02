-- 0101 — Second-factor sign-in, the custom role builder, and a person's own
--        session list (blueprint H1, A6.2).
--
-- # The MFA columns have existed since 0003 and nothing used them
--
-- `app_user.mfa_enabled` and `app_user.mfa_secret_enc` were added with the
-- identity schema and never written to. What was missing is not a column: it is
-- the enrolment, the challenge on the sign-in path, and the way back in when
-- somebody loses their phone.
--
-- That last one is what this migration is mostly about. A second factor with no
-- recovery route is not security, it is a lockout waiting for a broken screen —
-- and the shop that loses its Owner account at nine on a Thursday morning does
-- not care that the design was theoretically sound.
--
-- # Recovery codes are hashed, single-use, and counted
--
-- Each is exactly as good as the password it stands in for, so it is stored the
-- way a password is: argon2id, never plain. `used_at` rather than DELETE, for
-- the reason 0076 gives about reset codes — a deleted row cannot tell "never
-- existed" from "already spent", and the second is what a replay looks like.
--
-- # A custom role is a role like any other
--
-- A6.2 asks for a role builder. The schema for it has been here since 0003:
-- `role` carries `tenant_id`, `is_system` and `cloned_from`, and
-- `role_permission` carries what it holds. A tenant's own role is simply one
-- with `tenant_id` set and `is_system` false.
--
-- So this migration adds no table for it. What it adds is the permission
-- CATALOGUE — the list a picker needs in order to be a picker rather than a
-- free-text box, and the descriptions that let an owner understand what they
-- are about to hand a cashier.
--
-- # Why the catalogue is a table and not a Go slice
--
-- Because the route registry already enumerates every permission the product
-- enforces, and a second list in Go would be a second thing to keep in step.
-- The table holds only what the registry cannot: the human sentence describing
-- each one, and which group it belongs to on the screen. A permission with no
-- row still works and still appears — ungrouped, described by its own key —
-- which is the honest failure for one somebody forgot to describe.

-- ---------------------------------------------------------------------------
-- MFA recovery codes (H1)
-- ---------------------------------------------------------------------------

CREATE TABLE mfa_recovery_code (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- Nullable, like app_user.tenant_id: a Super Admin has no tenant and needs a
  -- second factor more than anybody.
  tenant_id  uuid REFERENCES tenant(id) ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,

  -- argon2id. See the file note: for as long as it is unspent this is a
  -- password.
  code_hash  text NOT NULL,

  issued_at  timestamptz NOT NULL DEFAULT now(),
  used_at    timestamptz,
  -- Where it was spent, so somebody reviewing their account can see a code
  -- used from an address they do not recognise.
  used_ip    inet
);

CREATE INDEX mfa_recovery_code_user_idx
  ON mfa_recovery_code (user_id) WHERE used_at IS NULL;

ALTER TABLE mfa_recovery_code ENABLE ROW LEVEL SECURITY;
ALTER TABLE mfa_recovery_code FORCE  ROW LEVEL SECURITY;
-- Read on the platform plane during sign-in, before a tenant is known — which
-- is the same plane `app_user` itself is read on at that moment.
CREATE POLICY mfa_recovery_code_isolation ON mfa_recovery_code
  USING (tenant_id = current_tenant_id() OR current_setting(
    'app.platform_admin', true) = 'on');

-- When the second factor was turned on, so a screen can say "in use since
-- March" rather than a bare tick.
ALTER TABLE app_user ADD COLUMN mfa_enrolled_at timestamptz;

-- ---------------------------------------------------------------------------
-- The permission catalogue (A6.2)
-- ---------------------------------------------------------------------------

CREATE TABLE permission_catalogue (
  permission  text PRIMARY KEY,

  -- Which heading it sits under on the role builder. Free text rather than an
  -- enum: a group is a presentation decision and adding one should not be a
  -- migration.
  section     text NOT NULL,

  -- One sentence, in the words an owner would use. "See what the shop sold",
  -- not "sales.view".
  label       text NOT NULL,
  label_ar    text,
  label_bn    text,

  -- The warning, where there is one. A permission that lets somebody change a
  -- posted figure or read what people are paid deserves a sentence saying so
  -- next to the tick box.
  caution     text,

  -- Ordering within the section, so the dangerous ones do not land at the top
  -- by alphabetical accident.
  sort_order  smallint NOT NULL DEFAULT 100,

  CONSTRAINT permission_catalogue_label_not_blank CHECK (btrim(label) <> '')
);

-- Reference data, identical for every tenant.
ALTER TABLE permission_catalogue ENABLE ROW LEVEL SECURITY;
ALTER TABLE permission_catalogue FORCE  ROW LEVEL SECURITY;
CREATE POLICY permission_catalogue_readable ON permission_catalogue USING (true);

-- ---------------------------------------------------------------------------
-- What each permission means, in the words an owner would use
-- ---------------------------------------------------------------------------
--
-- Every permission the route registry enforces, described once. A permission
-- with no row here still works and still appears on the builder, labelled by
-- its own key and ungrouped -- which is the honest failure for one somebody
-- forgot to describe, and is what the wiring test checks for.

INSERT INTO permission_catalogue
  (permission, section, label, label_ar, label_bn, caution, sort_order)
VALUES
  ('sales.view', 'selling', 'See what the shop has sold', 'الاطلاع على المبيعات', 'দোকান কী বিক্রি করেছে দেখা', NULL, 10),
  ('sales.create', 'selling', 'Ring up a sale', 'تسجيل عملية بيع', 'বিক্রি করা', NULL, 20),
  ('sales.refund', 'selling', 'Give money back', 'رد الأموال', 'টাকা ফেরত দেওয়া', 'Reverses a posted sale, its tax and its stock.', 30),
  ('sales.exchange', 'selling', 'Swap an item for another', 'استبدال صنف بآخر', 'পণ্য বদল করা', NULL, 40),
  ('sales.receive_payment', 'selling', 'Take payment against an account', 'استلام دفعة على الحساب', 'বাকির বিপরীতে টাকা নেওয়া', NULL, 50),
  ('order.view', 'selling', 'See sales orders', 'الاطلاع على أوامر البيع', 'বিক্রয় আদেশ দেখা', NULL, 60),
  ('order.manage', 'selling', 'Raise and change sales orders', 'إنشاء وتعديل أوامر البيع', 'বিক্রয় আদেশ তৈরি ও পরিবর্তন', NULL, 70),
  ('promotion.view', 'selling', 'See offers and discounts', 'الاطلاع على العروض', 'অফার ও ছাড় দেখা', NULL, 80),
  ('promotion.manage', 'selling', 'Create offers and discounts', 'إنشاء العروض والخصومات', 'অফার ও ছাড় তৈরি', 'Changes what every till charges.', 90),
  ('catalog.view', 'stock', 'See the product list', 'الاطلاع على قائمة المنتجات', 'পণ্যের তালিকা দেখা', NULL, 10),
  ('catalog.create', 'stock', 'Add and edit products', 'إضافة وتعديل المنتجات', 'পণ্য যোগ ও সম্পাদনা', NULL, 20),
  ('catalog.delete', 'stock', 'Remove products', 'حذف المنتجات', 'পণ্য মুছে ফেলা', NULL, 30),
  ('inventory.view', 'stock', 'See stock levels', 'الاطلاع على المخزون', 'মজুত দেখা', NULL, 40),
  ('inventory.adjust_stock', 'stock', 'Correct a stock figure', 'تصحيح كمية المخزون', 'মজুতের সংখ্যা সংশোধন', 'Writes stock off or on, and posts the difference to the books.', 50),
  ('inventory.transfer_stock', 'stock', 'Move stock between places', 'نقل المخزون بين المواقع', 'স্থানের মধ্যে মজুত সরানো', NULL, 60),
  ('inventory.approve_transfer', 'stock', 'Approve a stock transfer', 'اعتماد نقل المخزون', 'মজুত স্থানান্তর অনুমোদন', NULL, 70),
  ('label.print', 'stock', 'Print labels and tags', 'طباعة الملصقات', 'লেবেল ছাপা', NULL, 80),
  ('label.manage', 'stock', 'Design labels and barcodes', 'تصميم الملصقات والباركود', 'লেবেল ও বারকোড নকশা', NULL, 90),
  ('serial.view', 'stock', 'See serial numbers', 'الاطلاع على الأرقام التسلسلية', 'সিরিয়াল নম্বর দেখা', NULL, 100),
  ('serial.manage', 'stock', 'Record and move serial numbers', 'تسجيل ونقل الأرقام التسلسلية', 'সিরিয়াল নম্বর লেখা ও সরানো', NULL, 110),
  ('purchasing.view', 'buying', 'See purchases and suppliers', 'الاطلاع على المشتريات والموردين', 'ক্রয় ও সরবরাহকারী দেখা', NULL, 10),
  ('purchasing.manage_suppliers', 'buying', 'Add and edit suppliers', 'إضافة وتعديل الموردين', 'সরবরাহকারী যোগ ও সম্পাদনা', NULL, 20),
  ('purchasing.request', 'buying', 'Ask for something to be bought', 'طلب شراء', 'কিছু কেনার অনুরোধ', NULL, 30),
  ('purchasing.approve_request', 'buying', 'Approve a purchase request', 'اعتماد طلب الشراء', 'ক্রয় অনুরোধ অনুমোদন', NULL, 40),
  ('purchasing.manage_rfq', 'buying', 'Ask suppliers to quote', 'طلب عروض أسعار', 'সরবরাহকারীর কাছে দর চাওয়া', NULL, 50),
  ('purchasing.award_rfq', 'buying', 'Choose the winning quote', 'اختيار العرض الفائز', 'বিজয়ী দর বেছে নেওয়া', NULL, 60),
  ('purchasing.create_order', 'buying', 'Write a purchase order', 'إنشاء أمر شراء', 'ক্রয় আদেশ লেখা', NULL, 70),
  ('purchasing.issue_order', 'buying', 'Send an order to the supplier', 'إرسال الأمر إلى المورد', 'সরবরাহকারীকে আদেশ পাঠানো', 'Commits the shop to the amount on the order.', 80),
  ('purchasing.receive_goods', 'buying', 'Take delivery of goods', 'استلام البضاعة', 'পণ্য গ্রহণ', 'Adds stock and posts what it cost.', 90),
  ('purchasing.record_bill', 'buying', 'Enter a supplier bill', 'إدخال فاتورة مورد', 'সরবরাহকারীর বিল লেখা', NULL, 100),
  ('purchasing.approve_bill', 'buying', 'Approve a bill for payment', 'اعتماد الفاتورة للدفع', 'পরিশোধের জন্য বিল অনুমোদন', NULL, 110),
  ('purchasing.pay_supplier', 'buying', 'Pay a supplier', 'دفع مستحقات مورد', 'সরবরাহকারীকে পরিশোধ', 'Money leaves the business.', 120),
  ('customers.view', 'customers', 'See customers', 'الاطلاع على العملاء', 'গ্রাহক দেখা', NULL, 10),
  ('customers.manage', 'customers', 'Add and edit customers', 'إضافة وتعديل العملاء', 'গ্রাহক যোগ ও সম্পাদনা', NULL, 20),
  ('customers.set_credit_limit', 'customers', 'Set how much credit a customer gets', 'تحديد حد ائتمان العميل', 'গ্রাহকের বাকির সীমা নির্ধারণ', 'Decides how much the shop can be owed.', 30),
  ('loyalty.view', 'customers', 'See points and members', 'الاطلاع على النقاط والأعضاء', 'পয়েন্ট ও সদস্য দেখা', NULL, 40),
  ('loyalty.manage', 'customers', 'Run the loyalty scheme', 'إدارة برنامج الولاء', 'লয়্যালটি স্কিম চালানো', NULL, 50),
  ('wallet.view', 'customers', 'See store credit and gift cards', 'الاطلاع على الرصيد وبطاقات الهدايا', 'জমা ও উপহার কার্ড দেখা', NULL, 60),
  ('wallet.manage', 'customers', 'Issue store credit and gift cards', 'إصدار الرصيد وبطاقات الهدايا', 'জমা ও উপহার কার্ড ইস্যু', 'Creates something that can be spent like money.', 70),
  ('delivery.view', 'aftersales', 'See deliveries', 'الاطلاع على التوصيل', 'ডেলিভারি দেখা', NULL, 10),
  ('delivery.manage', 'aftersales', 'Arrange deliveries', 'تنظيم التوصيل', 'ডেলিভারি ব্যবস্থা', NULL, 20),
  ('delivery.deliver', 'aftersales', 'Mark a delivery done', 'تأكيد التسليم', 'ডেলিভারি সম্পন্ন চিহ্নিত', NULL, 30),
  ('installment.view', 'aftersales', 'See instalment plans', 'الاطلاع على خطط التقسيط', 'কিস্তির পরিকল্পনা দেখা', NULL, 40),
  ('installment.manage', 'aftersales', 'Set up instalment plans', 'إعداد خطط التقسيط', 'কিস্তির পরিকল্পনা তৈরি', NULL, 50),
  ('service.view', 'aftersales', 'See repair jobs', 'الاطلاع على أوامر الإصلاح', 'মেরামতের কাজ দেখা', NULL, 60),
  ('service.manage', 'aftersales', 'Book and close repair jobs', 'إنشاء وإغلاق أوامر الإصلاح', 'মেরামতের কাজ খোলা ও বন্ধ', NULL, 70),
  ('portal.view', 'aftersales', 'See portal requests and logins', 'الاطلاع على طلبات البوابة', 'পোর্টালের অনুরোধ ও লগইন দেখা', NULL, 80),
  ('portal.manage', 'aftersales', 'Answer customers and invite suppliers', 'الرد على العملاء ودعوة الموردين', 'গ্রাহকদের উত্তর ও সরবরাহকারী আমন্ত্রণ', NULL, 90),
  ('accounting.view', 'money', 'See the books', 'الاطلاع على الدفاتر', 'খাতা দেখা', NULL, 10),
  ('accounting.create', 'money', 'Write a journal entry by hand', 'إنشاء قيد يدوي', 'হাতে জার্নাল এন্ট্রি লেখা', 'Posts straight to the ledger, past every other screen.', 20),
  ('accounting.manage_accounts', 'money', 'Change the chart of accounts', 'تعديل دليل الحسابات', 'হিসাবের তালিকা পরিবর্তন', 'Changes where every future transaction lands.', 30),
  ('accounting.reconcile', 'money', 'Reconcile the bank', 'تسوية البنك', 'ব্যাংক মিলানো', NULL, 40),
  ('accounting.close_period', 'money', 'Close a month', 'إقفال الفترة', 'মাস বন্ধ করা', 'Nothing can be posted into a closed month.', 50),
  ('accounting.reopen_period', 'money', 'Reopen a closed month', 'إعادة فتح فترة مقفلة', 'বন্ধ মাস আবার খোলা', 'Changes figures somebody has already reported.', 60),
  ('expense.view', 'money', 'See spending', 'الاطلاع على المصروفات', 'খরচ দেখা', NULL, 70),
  ('expense.record', 'money', 'Record spending', 'تسجيل المصروفات', 'খরচ লেখা', NULL, 80),
  ('expense.manage_heads', 'money', 'Set up expense categories', 'إعداد فئات المصروفات', 'খরচের শ্রেণি তৈরি', NULL, 90),
  ('asset.view', 'money', 'See equipment and assets', 'الاطلاع على الأصول', 'সরঞ্জাম ও সম্পদ দেখা', NULL, 100),
  ('asset.manage', 'money', 'Register and dispose of assets', 'تسجيل الأصول والتخلص منها', 'সম্পদ নিবন্ধন ও নিষ্পত্তি', NULL, 110),
  ('investor.view', 'money', 'See investor contributions', 'الاطلاع على مساهمات المستثمرين', 'বিনিয়োগকারীর অবদান দেখা', NULL, 120),
  ('investor.manage', 'money', 'Record investor contributions', 'تسجيل مساهمات المستثمرين', 'বিনিয়োগকারীর অবদান লেখা', NULL, 130),
  ('subscription.view', 'money', 'See the plan this shop is on', 'الاطلاع على باقة المتجر', 'এই দোকান কোন প্ল্যানে আছে দেখা', NULL, 140),
  ('hr.view', 'staff', 'See the staff list', 'الاطلاع على قائمة الموظفين', 'কর্মীর তালিকা দেখা', NULL, 10),
  ('hr.manage', 'staff', 'Add and edit staff records', 'إضافة وتعديل بيانات الموظفين', 'কর্মীর তথ্য যোগ ও সম্পাদনা', NULL, 20),
  ('hr.view_pay', 'staff', 'See what people are paid', 'الاطلاع على الرواتب', 'কে কত বেতন পায় দেখা', 'Every salary is visible to anybody holding this.', 30),
  ('payroll.view', 'staff', 'See payroll runs', 'الاطلاع على مسيرات الرواتب', 'বেতন চালানো দেখা', NULL, 40),
  ('payroll.run', 'staff', 'Prepare a payroll run', 'إعداد مسير الرواتب', 'বেতন চালানো প্রস্তুত', NULL, 50),
  ('payroll.approve', 'staff', 'Approve a payroll run', 'اعتماد مسير الرواتب', 'বেতন চালানো অনুমোদন', 'Approves what leaves the bank on payday.', 60),
  ('identity.view', 'staff', 'See who can sign in', 'الاطلاع على المستخدمين', 'কে সাইন ইন করতে পারে দেখা', NULL, 70),
  ('identity.create', 'staff', 'Invite somebody to sign in', 'دعوة مستخدم جديد', 'কাউকে সাইন ইন করতে আমন্ত্রণ', NULL, 80),
  ('identity.edit', 'staff', 'Edit and disable accounts', 'تعديل وتعطيل الحسابات', 'অ্যাকাউন্ট সম্পাদনা ও বন্ধ', NULL, 90),
  ('identity.manage_roles', 'staff', 'Build roles and decide what they can do', 'إنشاء الأدوار وتحديد صلاحياتها', 'ভূমিকা তৈরি ও তাদের ক্ষমতা নির্ধারণ', 'Anybody holding this can grant everything they hold themselves.', 100),
  ('report.view', 'oversight', 'See reports', 'الاطلاع على التقارير', 'রিপোর্ট দেখা', NULL, 10),
  ('report.save', 'oversight', 'Keep and schedule reports', 'حفظ وجدولة التقارير', 'রিপোর্ট রাখা ও সময়সূচি', 'A scheduled report emails figures out of the business.', 20),
  ('group.view', 'oversight', 'See group statements', 'الاطلاع على قوائم المجموعة', 'গ্রুপ বিবরণী দেখা', 'Reads every company in the group at once.', 30),
  ('group.manage', 'oversight', 'Set up the group', 'إعداد المجموعة', 'গ্রুপ তৈরি', NULL, 40),
  ('approval.view', 'oversight', 'See what is waiting for sign-off', 'الاطلاع على طلبات الاعتماد', 'কী অনুমোদনের অপেক্ষায় দেখা', NULL, 50),
  ('approval.decide', 'oversight', 'Approve or refuse', 'الاعتماد أو الرفض', 'অনুমোদন বা প্রত্যাখ্যান', NULL, 60),
  ('approval.manage_rules', 'oversight', 'Write the sign-off rules', 'كتابة قواعد الاعتماد', 'অনুমোদনের নিয়ম লেখা', 'Decides what everybody else has to get approved.', 70),
  ('compliance.view', 'oversight', 'See where the shop stands legally', 'الاطلاع على الموقف النظامي', 'দোকানের আইনি অবস্থান দেখা', NULL, 80),
  ('privacy.view', 'oversight', 'See the privacy register', 'الاطلاع على سجل الخصوصية', 'গোপনীয়তা নিবন্ধ দেখা', NULL, 90),
  ('privacy.manage', 'oversight', 'Answer privacy requests', 'الرد على طلبات الخصوصية', 'গোপনীয়তার অনুরোধের উত্তর', 'Includes agreeing to erase what is held about somebody.', 100),
  ('document.view', 'oversight', 'Open stored documents', 'فتح المستندات المحفوظة', 'সংরক্ষিত নথি খোলা', 'Includes scans of identity papers.', 110),
  ('document.manage', 'oversight', 'Attach and remove documents', 'إرفاق وحذف المستندات', 'নথি সংযুক্ত ও অপসারণ', NULL, 120),
  ('devices.view', 'system', 'See the tills', 'الاطلاع على نقاط البيع', 'টিল দেখা', NULL, 10),
  ('devices.manage', 'system', 'Pair and revoke tills', 'ربط وإلغاء نقاط البيع', 'টিল যুক্ত ও বাতিল', NULL, 20),
  ('einvoicing.view', 'system', 'See electronic invoicing status', 'الاطلاع على حالة الفوترة', 'ইলেকট্রনিক চালানের অবস্থা দেখা', NULL, 30),
  ('einvoicing.onboard', 'system', 'Onboard a till for e-invoicing', 'تسجيل نقطة بيع للفوترة', 'ই-চালানে টিল নিবন্ধন', NULL, 40),
  ('einvoicing.manage', 'system', 'Manage e-invoicing credentials', 'إدارة بيانات الفوترة', 'ই-চালানের ক্রেডেনশিয়াল ব্যবস্থাপনা', 'Holds the credential that signs the shop invoices.', 50),
  ('integration.view', 'system', 'See connected systems', 'الاطلاع على الأنظمة المتصلة', 'সংযুক্ত সিস্টেম দেখা', NULL, 60),
  ('integration.manage', 'system', 'Connect other systems', 'ربط أنظمة أخرى', 'অন্য সিস্টেম সংযুক্ত', 'API keys and webhooks send data out of the business.', 70),
  ('data.export', 'system', 'Export data', 'تصدير البيانات', 'তথ্য রপ্তানি', 'Takes a copy of the business out of the system.', 80),
  ('data.import', 'system', 'Import data', 'استيراد البيانات', 'তথ্য আমদানি', 'Rewrites master data in bulk.', 90),
  ('backup.view', 'system', 'See backups', 'الاطلاع على النسخ الاحتياطية', 'ব্যাকআপ দেখা', NULL, 100),
  ('backup.run', 'system', 'Record a backup', 'تسجيل نسخة احتياطية', 'ব্যাকআপ লেখা', NULL, 110),
  ('notification.manage', 'system', 'Send announcements', 'إرسال الإشعارات', 'ঘোষণা পাঠানো', NULL, 120),
  ('support.raise', 'system', 'Raise a support ticket', 'فتح تذكرة دعم', 'সাপোর্ট টিকিট খোলা', NULL, 130)
ON CONFLICT (permission) DO UPDATE SET
  section = excluded.section,
  label = excluded.label,
  label_ar = excluded.label_ar,
  label_bn = excluded.label_bn,
  caution = excluded.caution,
  sort_order = excluded.sort_order;
