-- The privacy register names the software. The software was renamed.
--
-- # What this is
--
-- `processing_activity` is the PDPL record of processing activities (E4.1).
-- Its `system_name` column names the system that does the processing, and a
-- shop can be asked to produce that register by a regulator. 0096 seeded four
-- activities per company -- sales and invoicing, loyalty, payroll, suppliers --
-- and wrote "RawSyst POS" in all four, because that is what the product was
-- called.
--
-- It is called Biz1core now. A register naming a system that no longer exists
-- under that name is a register that does not describe the installation it
-- belongs to.
--
-- # Why a new migration and not an edit to 0096
--
-- Because 0096 is applied. `Pool.Migrate` hashes every migration and refuses
-- to start if one has changed since it was recorded -- "Add a new migration
-- instead of editing history", in its own words. The rename sweep did edit
-- 0096, every accounting test failed on that checksum within the minute, and
-- the edit was reverted. This is the same change made the way the migrator
-- allows.
--
-- 0056 also mentions the old name, in a SQL comment describing a default
-- document template. A comment is not data and nothing reads it, so it stays
-- as it is: the alternative is breaking the checksum of a second applied
-- migration to reword a sentence nobody sees. Both are listed in
-- docs/BRANDING.md.
--
-- # Why the loop
--
-- `processing_activity` is FORCE row level security under
--
--     USING (tenant_id = current_tenant_id())
--
-- and -- unlike `tenant` -- that policy has NO `is_platform_admin()` arm. So
-- setting `app.platform_admin` alone would not make one row visible, and an
-- UPDATE from a migration would report success having changed nothing, which
-- is precisely the silent failure 0138 exists to document.
--
-- Instead `app.tenant_id` is set for each tenant in turn, which is what the
-- policy actually asks for. It is the same shape 0096 used to write these rows
-- in the first place. Both settings are local, so they die with the
-- transaction rather than leaking into the pooled connection that carries it.
--
-- # Why the WHERE clause is narrow
--
-- Only rows still carrying a value the seed wrote are touched. `system_name`
-- is editable -- a shop may have replaced it with the name of a different
-- system entirely, or written their own note in it -- and a rebrand has no
-- business overwriting what somebody typed.

DO $$
DECLARE
  t record;
  renamed integer := 0;
  n integer;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    UPDATE processing_activity
       SET system_name = 'Biz1core'
     WHERE tenant_id = t.id
       AND system_name IN ('RawSyst POS', 'RawSyst');

    GET DIAGNOSTICS n = ROW_COUNT;
    renamed := renamed + n;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', '', true);

  -- Said out loud, because 0138's lesson is that a data migration behind row
  -- level security does not fail when it cannot see its rows -- it succeeds
  -- and does nothing. A zero here on a fresh database is correct; a zero on a
  -- database that has companies is the bug.
  RAISE NOTICE 'processing_activity: % activities renamed to Biz1core', renamed;
END $$;
