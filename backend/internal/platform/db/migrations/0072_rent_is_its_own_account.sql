-- 0072 — 0071's account relabel silently did nothing, and Rent ended up
-- pointing at Stock Write-off.
--
-- # What went wrong
--
-- 0071 opens with a bare statement:
--
--   UPDATE account SET code = '5400' WHERE code = '5200' AND name = 'Stock
--   Write-off' AND NOT EXISTS (...)
--
-- `account` has FORCE ROW LEVEL SECURITY with a `tenant_id = current_tenant_id()`
-- predicate. That statement runs with no tenant context set, so the predicate is
-- false for every row and it updated NOTHING — quietly, with no error, because
-- an UPDATE that matches nothing is not a failure.
--
-- This is the exact trap 0042 recorded and that 0071 itself quotes, three
-- sections further down, as the reason its permission backfill loops tenant by
-- tenant. The loop was written correctly. The statement above it was not.
--
-- # What it left behind
--
-- With 5200 still occupied, 0071's next step —
--
--   INSERT INTO account (... code '5200' 'Rent' ...)
--   ON CONFLICT (company_id, code) DO UPDATE SET code = EXCLUDED.code
--   RETURNING id
--
-- did not create Rent. It found the existing row at 5200, returned ITS id, and
-- mapped the `expense_rent` role to Stock Write-off. The seeded Rent expense
-- head then pointed at the same place, so a shop recording its rent debited
-- Stock Write-off — an account whose balance an accountant reads as damaged
-- stock.
--
-- Only companies that existed BEFORE 0071 are affected. One created since gets
-- its chart from provisioning.SeedChartOfAccounts, which runs inside a tenant
-- transaction and already has Rent at 5200 and Stock Write-off at 5400.
--
-- # What this does NOT do
--
-- It does not move journal lines. An entry already posted to Stock Write-off
-- stays there: a posted entry is immutable by design, and rewriting history to
-- tidy a mapping is the one thing these books do not allow. Correcting one is a
-- reversal and a re-posting, which is a decision for whoever keeps the books.
-- In practice there is nothing to correct outside a development database —
-- expenses and this bug ship in the same release.

DO $$
DECLARE
  c record;
  a record;
  writeoff uuid;
  rent uuid;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR c IN SELECT id, tenant_id FROM company LOOP
    -- The line 0071 was missing. Everything below depends on it.
    PERFORM set_config('app.tenant_id', c.tenant_id::text, true);

    CONTINUE WHEN NOT EXISTS (SELECT 1 FROM account WHERE company_id = c.id);

    -- Free 5200, if Stock Write-off is still sitting in it. A relabel: the row
    -- keeps its id, so every journal line already pointing at it still does and
    -- no balance moves.
    SELECT id INTO writeoff FROM account
    WHERE company_id = c.id AND code = '5200' AND name = 'Stock Write-off';

    IF writeoff IS NOT NULL AND NOT EXISTS (
      SELECT 1 FROM account WHERE company_id = c.id AND code = '5400')
    THEN
      UPDATE account SET code = '5400' WHERE id = writeoff;

      -- Rent, in the code design 12 §1 gives it.
      INSERT INTO account (tenant_id, company_id, code, name, type)
      VALUES (c.tenant_id, c.id, '5200', 'Rent', 'expense')
      RETURNING id INTO rent;

      -- Repoint the role 0071 mapped to the wrong account, and the head that
      -- followed it there. Only the head 0071 seeded: one somebody created by
      -- hand and deliberately pointed at Stock Write-off is their decision.
      UPDATE account_role_map SET account_id = rent
      WHERE company_id = c.id AND role = 'expense_rent' AND account_id = writeoff;

      UPDATE expense_head SET account_id = rent
      WHERE company_id = c.id AND code = 'RENT' AND account_id = writeoff;
    END IF;

    -- And the three that were never at risk, in case a company was created in
    -- the window where 0071 had run and the Go seed had not been deployed.
    FOR a IN
      SELECT * FROM (VALUES
        ('5210', 'Utilities',  'expense_utilities'),
        ('5220', 'Salaries',   'expense_salaries'),
        ('5230', 'Marketing',  'expense_marketing')
      ) AS v(code, name, role)
    LOOP
      INSERT INTO account (tenant_id, company_id, code, name, type)
      VALUES (c.tenant_id, c.id, a.code, a.name, 'expense')
      ON CONFLICT (company_id, code) DO NOTHING;

      INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
      SELECT c.tenant_id, c.id, a.role, id
      FROM account WHERE company_id = c.id AND code = a.code
      ON CONFLICT (company_id, role) DO NOTHING;
    END LOOP;

    -- The Rent head, for a company that had none because 0071's insert found
    -- no `expense_rent` mapping to hang one off.
    INSERT INTO expense_head
      (tenant_id, company_id, code, name, name_ar, account_id,
       input_vat_recoverable)
    SELECT c.tenant_id, c.id, 'RENT', 'Rent', 'الإيجار', m.account_id, true
    FROM account_role_map m
    WHERE m.company_id = c.id AND m.role = 'expense_rent'
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END $$;
