-- Two permissions the role builder could only show as raw keys.
--
-- `permission_catalogue` is what turns `sales.view` into "See what the shop
-- sold" — one sentence in the words an owner would use, in three languages,
-- with a warning where the permission deserves one. A6.2's role builder is
-- built entirely from it.
--
-- `catalog.edit` and `report.export` were never given a row. Both are enforced
-- by routes today — `catalog.edit` guards setting a variant's prices,
-- `report.export` guards `GET /reports/{kind}/export` — and both have been in
-- the seeded roles since 0005. The service falls back to
-- `{section: 'other', label: <the permission key>}` when a row is missing, so
-- an owner building a role read 101 sentences and then, under a heading called
-- "other", the words `catalog.edit` and `report.export`. In Arabic and Bangla
-- too, because a fallback has no translation to fall back to.
--
-- Found by rendering the permission list before building the screen for it.
--
-- # And a section with one member in it
--
-- `inventory.recall_batch` was filed under `inventory` by 0107. Every other
-- stock permission — including `inventory.adjust_stock`, `inventory.view` and
-- `inventory.transfer_stock` — is under `stock`. A grouped list therefore drew
-- a heading of its own for a single tick box, which reads as a section somebody
-- forgot to finish rather than as a deliberate grouping. Moved, so recalling a
-- batch sits with the rest of the stock permissions where somebody looking for
-- it would look.
--
-- The permission itself does not change. This is presentation, which is exactly
-- what 0101 says `section` is for: "free text rather than an enum: a group is a
-- presentation decision".

INSERT INTO permission_catalogue
  (permission, section, label, label_ar, label_bn, caution, sort_order)
VALUES
  -- Beside catalog.create ("Add and edit products") at 20 and catalog.delete
  -- at 30. This one is narrower and more dangerous than its name suggests: it
  -- is what lets somebody change a selling price, so it carries a warning that
  -- catalog.create does not.
  ('catalog.edit', 'stock',
   'Change selling prices',
   'تغيير أسعار البيع',
   'বিক্রয় মূল্য পরিবর্তন',
   'A price changed here is the price the till charges from the next sale.',
   25),

  -- Beside report.view at 10 and report.save at 20. Exporting takes figures
  -- out of the business, which is the same concern report.save carries about
  -- scheduling them.
  ('report.export', 'oversight',
   'Download reports as files',
   'تنزيل التقارير كملفات',
   'রিপোর্ট ফাইল হিসেবে নামানো',
   'An exported report leaves the business and cannot be recalled.',
   15)
ON CONFLICT (permission) DO NOTHING;

UPDATE permission_catalogue
SET section = 'stock'
WHERE permission = 'inventory.recall_batch' AND section = 'inventory';
