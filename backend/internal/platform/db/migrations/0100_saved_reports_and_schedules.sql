-- 0100 — Saved reports, scheduled reports, and the Saudization reading
--        (blueprint D1, E6).
--
-- # What a "custom report builder" honestly is here
--
-- D1 asks for one. The version that would be dishonest is a free-form query
-- builder: a screen that lets somebody join arbitrary tables would be a screen
-- that can produce a figure the accounting engine never blessed, presented in
-- the same typeface as the trial balance.
--
-- So a saved report is a SAVED SHAPE of a report the product already computes:
-- which one, over what window, filtered to which store or account. The reports
-- themselves stay in `internal/reports` where the sign conventions and the
-- period rules live, and this table is the thing an owner names, keeps and
-- comes back to.
--
-- That is the difference between "run the profit and loss for the Riyadh branch
-- for last quarter, and call it Riyadh Q3" and "SELECT whatever you like".
--
-- # A schedule is a saved report with a clock
--
-- Not a second table: the same row carries an optional cadence and the people
-- it goes to. A shop that wants the same figures every Monday is describing a
-- saved report, and splitting the two would mean a schedule could point at a
-- report definition that had been edited underneath it.
--
-- # Saudization needs no table at all
--
-- `employee.is_saudi` has existed since 0091. What was missing is anything that
-- READS it. The ratio is a query over the employee list, and storing it would
-- mean storing a number that goes stale the moment somebody is hired.

CREATE TABLE saved_report (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name         text NOT NULL,

  -- Which report the product already computes. A CHECK rather than an enum:
  -- adding a report should be an INSERT, and the constraint is here to catch a
  -- typo rather than to enumerate the roadmap.
  kind         text NOT NULL,

  -- The window, as a RELATIVE phrase rather than two dates.
  --
  -- "Last month" run in October means September; the same saved report run in
  -- November means October. Storing two dates would make a saved report a
  -- snapshot, which is the one thing a saved report must not be — and a
  -- schedule built on stored dates would email the same figures forever.
  period       text NOT NULL DEFAULT 'this_month',

  -- Narrowing, all optional. A report for one branch, one warehouse, or one
  -- account is the same report with a filter, and this is that filter.
  store_id     uuid REFERENCES store(id)     ON DELETE CASCADE,
  warehouse_id uuid REFERENCES warehouse(id) ON DELETE CASCADE,
  account_id   uuid REFERENCES account(id)   ON DELETE CASCADE,

  -- The schedule, when there is one. NULL cadence means a report somebody runs
  -- by hand, which is most of them.
  cadence      text,
  -- For a weekly schedule, which day; for a monthly one, which date. Both
  -- nullable because a daily schedule needs neither.
  day_of_week  smallint,
  day_of_month smallint,
  -- Where it goes. Free text rather than a join to app_user: a shop's
  -- accountant is often not a user of the system, and the person who wants the
  -- monthly figures by email is very often exactly that person.
  recipients   text,

  last_run_at  timestamptz,
  last_run_error text,

  is_active    boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  created_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT saved_report_name_not_blank CHECK (btrim(name) <> ''),
  CONSTRAINT saved_report_kind_valid CHECK (kind IN (
    'trial_balance', 'profit_and_loss', 'balance_sheet', 'cash_flow',
    'sales', 'expenses', 'stock', 'vat_return', 'receivables', 'payables',
    'movers', 'compliance')),
  CONSTRAINT saved_report_period_valid CHECK (period IN (
    'today', 'this_week', 'this_month', 'last_month', 'this_quarter',
    'last_quarter', 'this_year', 'last_year')),
  CONSTRAINT saved_report_cadence_valid CHECK (cadence IS NULL OR cadence IN (
    'daily', 'weekly', 'monthly')),
  CONSTRAINT saved_report_weekly_names_a_day CHECK (
    cadence <> 'weekly' OR day_of_week BETWEEN 0 AND 6),
  CONSTRAINT saved_report_monthly_names_a_date CHECK (
    cadence <> 'monthly' OR day_of_month BETWEEN 1 AND 28),
  -- 28 rather than 31 deliberately: a schedule set for the 31st would skip
  -- February entirely, and a shop that asked for monthly figures would quietly
  -- get eleven of them.
  CONSTRAINT saved_report_schedule_has_recipients CHECK (
    cadence IS NULL OR btrim(coalesce(recipients, '')) <> '')
);

CREATE UNIQUE INDEX saved_report_name_uq
  ON saved_report (company_id, lower(name));
CREATE INDEX saved_report_due_idx
  ON saved_report (cadence, last_run_at)
  WHERE cadence IS NOT NULL AND is_active;
CREATE INDEX saved_report_tenant_idx ON saved_report (tenant_id);

CREATE TRIGGER saved_report_touch BEFORE UPDATE ON saved_report
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE saved_report ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_report FORCE  ROW LEVEL SECURITY;
CREATE POLICY saved_report_isolation ON saved_report
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- Saving a report is not the same as reading one. `report.view` opens the
-- reports; `report.save` keeps one and schedules it, which sends figures out of
-- the building by email and is therefore a narrower thing to grant.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',      'report.save'),
  ('accountant', 'report.save')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

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
      ('owner',      'report.save'),
      ('accountant', 'report.save')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
