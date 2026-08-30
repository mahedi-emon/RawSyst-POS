-- 0091 — Employees, attendance, payroll, commission, GOSI and end-of-service
--        (blueprint C5, C6, and E6).
--
-- # An employee is not a user
--
-- `app_user` is somebody who can sign in. An employee is somebody the business
-- pays. They overlap and they are not the same set: a warehouse hand who never
-- touches the system is on the payroll and has no login, and an external
-- accountant may have a login and never be paid a salary. So `employee` is its
-- own table with an OPTIONAL link to a user.
--
-- Merging them would mean either giving every warehouse hand a password, or
-- putting a salary column on the table that authenticates people — and the
-- second is how a payroll figure ends up readable by anything that reads a
-- session.
--
-- # Every legal number is resolved, never stored in code
--
-- C6 is explicit that GOSI rates "have been rising under the new Social
-- Insurance Law effective from July 2024 and are scheduled to keep increasing
-- through 2028", and that the engine must use "a configurable, updatable rate
-- table rather than a hard-coded percentage". E8 generalises that to every
-- legal value.
--
-- So this migration contains NO rates, NO thresholds and NO file layout. It
-- contains the shapes those values are applied to. `SA.GOSI.RATES` and
-- `SA.WPS.WAGE_FILE_FORMAT` already exist in the registry as release blockers
-- carrying `__VERIFY__`, and the engine resolves them at the PAY PERIOD's date
-- — so re-running an old month produces the figure that was correct then, not
-- the figure that is correct now. Until a person verifies them against the
-- Tier 1 source and stamps `verified_on`, a GOSI calculation and a wage file
-- refuse with the rule key in the message. That refusal is the feature: a
-- payroll run that guessed would be wrong in a way nobody would notice until
-- GOSI rejected the submission.
--
-- # End of service is accrued monthly, not discovered at termination
--
-- Design 20 fixes this: `eosb_accrual` is monthly. A shop that only computes an
-- end-of-service benefit when somebody resigns has an unrecorded liability
-- growing on its balance sheet for years, and finds out how large it is on the
-- day it has to pay it.
--
-- The ACCRUAL is arithmetic on a wage and a length of service, which the
-- business decides. The ENTITLEMENT — how many days per year, how resignation
-- differs from dismissal, what counts as service — is Saudi labour law and
-- belongs to the registry like everything else in E6.

-- ---------------------------------------------------------------------------
-- Employees
-- ---------------------------------------------------------------------------

CREATE TABLE employee (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  employee_no  text NOT NULL,
  -- The login, when they have one. Nullable on purpose: see the header.
  user_id      uuid REFERENCES app_user(id) ON DELETE SET NULL,

  full_name    text NOT NULL,
  name_ar      text,
  phone        text,
  email        text,

  position     text,
  department   text,
  store_id     uuid REFERENCES store(id) ON DELETE SET NULL,

  -- C5 wants the identity documents AND their expiry, because an Iqama that
  -- lapses stops somebody working. Design 20 adds that Mudad cross-references
  -- GOSI and Qiwa, so a mismatch between these is caught locally rather than
  -- by a rejected wage file.
  national_id  text,
  iqama_no     text,
  id_expires_on date,
  gosi_number  text,
  qiwa_contract_no text,
  -- Nationality decides which GOSI rate applies — Saudi nationals and
  -- expatriates contribute differently — so it is a payroll input, not a
  -- demographic note.
  nationality  text,
  is_saudi     boolean NOT NULL DEFAULT false,

  -- Where the wage is paid. WPS submits against it, so a wrong IBAN is a
  -- rejected file rather than a late payment.
  iban         text,
  bank_name    text,

  joined_on    date NOT NULL,
  left_on      date,
  -- active, on_leave, suspended, left
  status       text NOT NULL DEFAULT 'active',

  -- C6's payroll components. Basic and allowances are kept apart because
  -- Saudi end-of-service and GOSI are computed on different bases, and a
  -- single "salary" column cannot answer either question afterwards.
  basic_salary numeric(18,4) NOT NULL DEFAULT 0,
  housing_allowance numeric(18,4) NOT NULL DEFAULT 0,
  transport_allowance numeric(18,4) NOT NULL DEFAULT 0,
  other_allowance numeric(18,4) NOT NULL DEFAULT 0,

  currency     text NOT NULL,
  -- Whether this person earns commission at all. The RULES are separate rows;
  -- this is the switch that decides whether to look for any.
  commission_eligible boolean NOT NULL DEFAULT false,

  notes        text,
  created_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT employee_status_valid CHECK (status IN (
    'active', 'on_leave', 'suspended', 'left')),
  CONSTRAINT employee_currency_upper CHECK (currency = upper(currency)),
  CONSTRAINT employee_pay_non_negative CHECK (
    basic_salary >= 0 AND housing_allowance >= 0
    AND transport_allowance >= 0 AND other_allowance >= 0),
  CONSTRAINT employee_name_not_blank CHECK (btrim(full_name) <> ''),
  -- Somebody who has left has a date; somebody who has not, has not. Without
  -- this, "left" and a null date reads as still employed to every report.
  CONSTRAINT employee_leaving_has_a_date CHECK (
    (status = 'left') = (left_on IS NOT NULL)),
  CONSTRAINT employee_left_after_joining CHECK (
    left_on IS NULL OR left_on >= joined_on)
);

CREATE UNIQUE INDEX employee_no_uq ON employee (company_id, employee_no);
-- One employee record per login. Two would mean two salaries for one person.
CREATE UNIQUE INDEX employee_user_uq ON employee (user_id)
  WHERE user_id IS NOT NULL;
CREATE INDEX employee_active_idx ON employee (company_id, status);
CREATE INDEX employee_store_idx ON employee (store_id) WHERE store_id IS NOT NULL;
-- The index C5's "Iqama/National ID + expiry alerts" reads.
CREATE INDEX employee_id_expiry_idx ON employee (company_id, id_expires_on)
  WHERE id_expires_on IS NOT NULL AND status <> 'left';
CREATE INDEX employee_tenant_idx ON employee (tenant_id);

CREATE TRIGGER employee_touch BEFORE UPDATE ON employee
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE employee ENABLE ROW LEVEL SECURITY;
ALTER TABLE employee FORCE  ROW LEVEL SECURITY;
CREATE POLICY employee_isolation ON employee
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Attendance and leave
-- ---------------------------------------------------------------------------

CREATE TABLE attendance (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  employee_id uuid NOT NULL REFERENCES employee(id) ON DELETE CASCADE,

  on_date     date NOT NULL,
  checked_in  timestamptz,
  checked_out timestamptz,

  -- Derived from the clock where there is one, entered by hand where there is
  -- not. Stored rather than computed because a shop with no biometric device
  -- types these in directly, and C5 puts a device integration "path for later"
  -- rather than a requirement.
  hours_worked   numeric(9,4) NOT NULL DEFAULT 0,
  overtime_hours numeric(9,4) NOT NULL DEFAULT 0,
  late_minutes   integer NOT NULL DEFAULT 0,

  -- present, absent, leave, holiday, rest_day
  status      text NOT NULL DEFAULT 'present',
  note        text,

  recorded_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT attendance_status_valid CHECK (status IN (
    'present', 'absent', 'leave', 'holiday', 'rest_day')),
  CONSTRAINT attendance_hours_sane CHECK (
    hours_worked >= 0 AND overtime_hours >= 0 AND late_minutes >= 0),
  CONSTRAINT attendance_out_after_in CHECK (
    checked_out IS NULL OR checked_in IS NULL OR checked_out >= checked_in)
);

-- One row per person per day. Two would double-count the hours.
CREATE UNIQUE INDEX attendance_day_uq ON attendance (employee_id, on_date);
CREATE INDEX attendance_period_idx ON attendance (company_id, on_date);
CREATE INDEX attendance_tenant_idx ON attendance (tenant_id);

CREATE TRIGGER attendance_touch BEFORE UPDATE ON attendance
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE attendance ENABLE ROW LEVEL SECURITY;
ALTER TABLE attendance FORCE  ROW LEVEL SECURITY;
CREATE POLICY attendance_isolation ON attendance
  USING (tenant_id = current_tenant_id());

CREATE TABLE leave_request (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  employee_id uuid NOT NULL REFERENCES employee(id) ON DELETE CASCADE,

  -- annual, sick, unpaid, hajj, maternity, bereavement, other. Free-ish
  -- because entitlement differs by country and by contract, and the ENGINE
  -- only needs to know whether it is paid.
  kind        text NOT NULL,
  is_paid     boolean NOT NULL DEFAULT true,

  starts_on   date NOT NULL,
  ends_on     date NOT NULL,
  days        numeric(9,2) NOT NULL,

  -- requested, approved, rejected, cancelled
  status      text NOT NULL DEFAULT 'requested',
  reason      text,
  decision_note text,

  requested_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  decided_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  decided_at   timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT leave_status_valid CHECK (status IN (
    'requested', 'approved', 'rejected', 'cancelled')),
  CONSTRAINT leave_dates_ordered CHECK (ends_on >= starts_on),
  CONSTRAINT leave_days_positive CHECK (days > 0),
  CONSTRAINT leave_rejection_says_why CHECK (
    status <> 'rejected' OR btrim(coalesce(decision_note, '')) <> '')
);

CREATE INDEX leave_employee_idx ON leave_request (employee_id, starts_on DESC);
CREATE INDEX leave_open_idx ON leave_request (company_id, status)
  WHERE status = 'requested';
CREATE INDEX leave_tenant_idx ON leave_request (tenant_id);

CREATE TRIGGER leave_request_touch BEFORE UPDATE ON leave_request
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE leave_request ENABLE ROW LEVEL SECURITY;
ALTER TABLE leave_request FORCE  ROW LEVEL SECURITY;
CREATE POLICY leave_request_isolation ON leave_request
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Salary advances
-- ---------------------------------------------------------------------------
--
-- C5: "issue an advance and have it automatically deducted from the next
-- payroll run". An advance is a LOAN, so it posts: cash leaves and the
-- employee owes it. Recovering it in payroll is not an expense — the wage
-- expense is the whole wage, and the advance simply reduces what is paid out.

CREATE TABLE salary_advance (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  employee_id uuid NOT NULL REFERENCES employee(id) ON DELETE RESTRICT,

  advance_no  text NOT NULL,
  amount      numeric(18,4) NOT NULL,
  currency    text NOT NULL,

  -- Recovered over this many runs. One means all of it next month.
  installments integer NOT NULL DEFAULT 1,
  -- Summed from the deductions, never stored: a second copy of a balance is a
  -- balance that can disagree with the payslips that produced it.
  issued_on   date NOT NULL DEFAULT current_date,
  reason      text,

  money_account_id uuid REFERENCES money_account(id) ON DELETE RESTRICT,
  journal_entry_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,

  approved_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT salary_advance_amount_positive CHECK (amount > 0),
  CONSTRAINT salary_advance_installments_positive CHECK (installments > 0),
  CONSTRAINT salary_advance_currency_upper CHECK (currency = upper(currency))
);

CREATE UNIQUE INDEX salary_advance_no_uq ON salary_advance (company_id, advance_no);
CREATE INDEX salary_advance_employee_idx ON salary_advance (employee_id);
CREATE INDEX salary_advance_tenant_idx ON salary_advance (tenant_id);

ALTER TABLE salary_advance ENABLE ROW LEVEL SECURITY;
ALTER TABLE salary_advance FORCE  ROW LEVEL SECURITY;
CREATE POLICY salary_advance_isolation ON salary_advance
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Commission rules
-- ---------------------------------------------------------------------------
--
-- C6 wants "rules by employee, product, category, store, total revenue, or
-- profit; flat percentage or tiered thresholds". Tiers are jsonb for the same
-- reason loyalty tiers are: a set read as a whole, changed as a whole, never
-- joined to.

CREATE TABLE commission_rule (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name        text NOT NULL,
  is_active   boolean NOT NULL DEFAULT true,

  -- What it is measured on: revenue or profit. Profit needs the cost, which
  -- the costing engine already computes at the moment of sale.
  basis       text NOT NULL DEFAULT 'revenue',

  -- Who and what it applies to. All nullable: a null means "any".
  employee_id uuid REFERENCES employee(id) ON DELETE CASCADE,
  store_id    uuid REFERENCES store(id)    ON DELETE CASCADE,
  category_id uuid REFERENCES category(id) ON DELETE CASCADE,
  brand_id    uuid REFERENCES brand(id)    ON DELETE CASCADE,
  variant_id  uuid REFERENCES variant(id)  ON DELETE CASCADE,

  -- A flat rate, used when tiers is empty.
  rate        numeric(9,6) NOT NULL DEFAULT 0,
  -- Tiered thresholds, ascending:
  --   [{"from": "0", "rate": "0.01"}, {"from": "50000", "rate": "0.02"}]
  -- C6's example: 2% once an employee's sales exceed SAR 50,000 in a month.
  tiers       jsonb NOT NULL DEFAULT '[]'::jsonb,

  effective_from date NOT NULL DEFAULT current_date,
  effective_to   date,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT commission_basis_valid CHECK (basis IN ('revenue', 'profit')),
  CONSTRAINT commission_rate_sane CHECK (rate >= 0 AND rate <= 1),
  CONSTRAINT commission_tiers_is_a_list CHECK (jsonb_typeof(tiers) = 'array'),
  CONSTRAINT commission_dates_ordered CHECK (
    effective_to IS NULL OR effective_to >= effective_from)
);

CREATE INDEX commission_rule_company_idx ON commission_rule (company_id, is_active);
CREATE INDEX commission_rule_employee_idx ON commission_rule (employee_id)
  WHERE employee_id IS NOT NULL;
CREATE INDEX commission_rule_tenant_idx ON commission_rule (tenant_id);

CREATE TRIGGER commission_rule_touch BEFORE UPDATE ON commission_rule
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE commission_rule ENABLE ROW LEVEL SECURITY;
ALTER TABLE commission_rule FORCE  ROW LEVEL SECURITY;
CREATE POLICY commission_rule_isolation ON commission_rule
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Payroll runs and payslips
-- ---------------------------------------------------------------------------

CREATE TABLE payroll_run (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  run_no      text NOT NULL,
  -- The month being paid, as its first day. The pay PERIOD, not the pay date:
  -- every legal value resolves at this date, so re-running March next year
  -- produces March's figures.
  period      date NOT NULL,
  pay_date    date,

  -- draft — being computed and reviewed
  -- approved — signed off, ready to pay
  -- paid — the money has gone and the entry is posted
  -- cancelled — abandoned before payment
  status      text NOT NULL DEFAULT 'draft',

  currency    text NOT NULL,

  gross_total     numeric(18,4) NOT NULL DEFAULT 0,
  deduction_total numeric(18,4) NOT NULL DEFAULT 0,
  net_total       numeric(18,4) NOT NULL DEFAULT 0,
  -- The employer's own GOSI cost, which is not deducted from anybody — it is
  -- an expense on top of the wage. Kept separate because an owner asking what
  -- staff cost needs the whole figure, not the payslip total.
  employer_gosi   numeric(18,4) NOT NULL DEFAULT 0,

  journal_entry_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,
  money_account_id uuid REFERENCES money_account(id) ON DELETE RESTRICT,

  note        text,
  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  approved_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  approved_at timestamptz,
  paid_at     timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT payroll_run_status_valid CHECK (status IN (
    'draft', 'approved', 'paid', 'cancelled')),
  CONSTRAINT payroll_run_currency_upper CHECK (currency = upper(currency)),
  CONSTRAINT payroll_run_totals_sane CHECK (
    gross_total >= 0 AND deduction_total >= 0 AND employer_gosi >= 0),
  CONSTRAINT payroll_run_period_is_a_month CHECK (
    period = date_trunc('month', period)::date),
  CONSTRAINT payroll_run_paid_has_an_entry CHECK (
    status <> 'paid' OR journal_entry_id IS NOT NULL)
);

-- One run per month per company. A second would pay everybody twice.
CREATE UNIQUE INDEX payroll_run_period_uq ON payroll_run (company_id, period)
  WHERE status <> 'cancelled';
CREATE UNIQUE INDEX payroll_run_no_uq ON payroll_run (company_id, run_no);
CREATE INDEX payroll_run_tenant_idx ON payroll_run (tenant_id);

CREATE TRIGGER payroll_run_touch BEFORE UPDATE ON payroll_run
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE payroll_run ENABLE ROW LEVEL SECURITY;
ALTER TABLE payroll_run FORCE  ROW LEVEL SECURITY;
CREATE POLICY payroll_run_isolation ON payroll_run
  USING (tenant_id = current_tenant_id());

CREATE TABLE payslip (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  run_id      uuid NOT NULL REFERENCES payroll_run(id) ON DELETE CASCADE,
  employee_id uuid NOT NULL REFERENCES employee(id) ON DELETE RESTRICT,

  -- Earnings.
  basic       numeric(18,4) NOT NULL DEFAULT 0,
  housing     numeric(18,4) NOT NULL DEFAULT 0,
  transport   numeric(18,4) NOT NULL DEFAULT 0,
  other_allowance numeric(18,4) NOT NULL DEFAULT 0,
  overtime    numeric(18,4) NOT NULL DEFAULT 0,
  commission  numeric(18,4) NOT NULL DEFAULT 0,
  bonus       numeric(18,4) NOT NULL DEFAULT 0,
  gross       numeric(18,4) NOT NULL DEFAULT 0,

  -- Deductions.
  absence_deduction numeric(18,4) NOT NULL DEFAULT 0,
  gosi_employee     numeric(18,4) NOT NULL DEFAULT 0,
  advance_recovery  numeric(18,4) NOT NULL DEFAULT 0,
  other_deduction   numeric(18,4) NOT NULL DEFAULT 0,
  deductions        numeric(18,4) NOT NULL DEFAULT 0,

  net         numeric(18,4) NOT NULL DEFAULT 0,

  -- The employer's share, carried per payslip so the WPS file and the GOSI
  -- return can both be produced from one place.
  gosi_employer numeric(18,4) NOT NULL DEFAULT 0,

  -- What the arithmetic was actually done with. A payslip must stay
  -- explainable after the rates change, and "the rate in force in March" is
  -- not recoverable from a rate table that has moved on twice since.
  gosi_rule_version integer,
  note        text,

  CONSTRAINT payslip_amounts_non_negative CHECK (
    basic >= 0 AND housing >= 0 AND transport >= 0 AND other_allowance >= 0
    AND overtime >= 0 AND commission >= 0 AND bonus >= 0 AND gross >= 0
    AND absence_deduction >= 0 AND gosi_employee >= 0
    AND advance_recovery >= 0 AND other_deduction >= 0 AND deductions >= 0
    AND gosi_employer >= 0),
  -- The two figures a person checks first. Enforced rather than trusted: a
  -- payslip whose parts do not add to its total is the one thing an employee
  -- will always notice.
  CONSTRAINT payslip_gross_adds_up CHECK (
    gross = basic + housing + transport + other_allowance + overtime
          + commission + bonus),
  CONSTRAINT payslip_deductions_add_up CHECK (
    deductions = absence_deduction + gosi_employee + advance_recovery
               + other_deduction),
  CONSTRAINT payslip_net_adds_up CHECK (net = gross - deductions)
);

CREATE UNIQUE INDEX payslip_employee_uq ON payslip (run_id, employee_id);
CREATE INDEX payslip_employee_idx ON payslip (employee_id);
CREATE INDEX payslip_tenant_idx ON payslip (tenant_id);

ALTER TABLE payslip ENABLE ROW LEVEL SECURITY;
ALTER TABLE payslip FORCE  ROW LEVEL SECURITY;
CREATE POLICY payslip_isolation ON payslip
  USING (tenant_id = current_tenant_id());

-- What each payslip recovered against which advance.
CREATE TABLE advance_recovery (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  advance_id  uuid NOT NULL REFERENCES salary_advance(id) ON DELETE CASCADE,
  payslip_id  uuid NOT NULL REFERENCES payslip(id) ON DELETE CASCADE,

  amount      numeric(18,4) NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT advance_recovery_amount_positive CHECK (amount > 0)
);

CREATE UNIQUE INDEX advance_recovery_uq
  ON advance_recovery (advance_id, payslip_id);
CREATE INDEX advance_recovery_tenant_idx ON advance_recovery (tenant_id);

ALTER TABLE advance_recovery ENABLE ROW LEVEL SECURITY;
ALTER TABLE advance_recovery FORCE  ROW LEVEL SECURITY;
CREATE POLICY advance_recovery_isolation ON advance_recovery
  USING (tenant_id = current_tenant_id());

-- What is still owed on an advance. Summed, like every other balance here.
CREATE OR REPLACE FUNCTION advance_outstanding(p_advance_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT a.amount - coalesce(
    (SELECT sum(r.amount) FROM advance_recovery r
     WHERE r.advance_id = a.id), 0)
  FROM salary_advance a WHERE a.id = p_advance_id;
$$;

-- ---------------------------------------------------------------------------
-- End-of-service benefit accrual
-- ---------------------------------------------------------------------------

CREATE TABLE eosb_accrual (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  employee_id uuid NOT NULL REFERENCES employee(id) ON DELETE CASCADE,

  period      date NOT NULL,
  -- What this month added to what the business will owe this person if they
  -- leave. Signed: a month is normally positive, and a settlement writes the
  -- accrued balance back out with a negative row rather than deleting history.
  amount      numeric(18,4) NOT NULL,
  -- The wage the accrual was computed on, kept so the figure stays explainable
  -- after a pay rise.
  wage_basis  numeric(18,4) NOT NULL,
  months_of_service numeric(9,2) NOT NULL,

  journal_entry_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,
  note        text,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT eosb_period_is_a_month CHECK (
    period = date_trunc('month', period)::date),
  CONSTRAINT eosb_amount_not_zero CHECK (amount <> 0),
  CONSTRAINT eosb_wage_non_negative CHECK (wage_basis >= 0)
);

-- One accrual per person per month. A run that happened twice would double the
-- liability with nothing looking wrong — the same shape of error the asset
-- depreciation index prevents.
CREATE UNIQUE INDEX eosb_month_uq ON eosb_accrual (employee_id, period)
  WHERE amount > 0;
CREATE INDEX eosb_employee_idx ON eosb_accrual (employee_id, period);
CREATE INDEX eosb_tenant_idx ON eosb_accrual (tenant_id);

ALTER TABLE eosb_accrual ENABLE ROW LEVEL SECURITY;
ALTER TABLE eosb_accrual FORCE  ROW LEVEL SECURITY;
CREATE POLICY eosb_accrual_isolation ON eosb_accrual
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER eosb_accrual_no_change
  BEFORE UPDATE OR DELETE ON eosb_accrual
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- WPS wage files
-- ---------------------------------------------------------------------------
--
-- The FILE is generated against a format that lives in the registry and is
-- still `__VERIFY__`. This table records the attempt and its outcome whatever
-- the format turns out to be, so the audit trail does not wait on the
-- verification.

CREATE TABLE wps_file (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  run_id      uuid NOT NULL REFERENCES payroll_run(id) ON DELETE RESTRICT,

  -- generated — built and downloadable
  -- submitted — handed to Mudad by a person
  -- accepted / rejected — what Mudad said
  status      text NOT NULL DEFAULT 'generated',

  -- Which version of the format definition produced it. A file built last
  -- March must stay explainable by the layout that actually made it.
  format_version integer,
  employee_count integer NOT NULL DEFAULT 0,
  total_amount   numeric(18,4) NOT NULL DEFAULT 0,

  -- The generated payload. Text rather than a file reference: a wage file is
  -- small, and the point of keeping it is that the exact bytes submitted stay
  -- recoverable.
  content     text,
  checksum    text,

  rejection_reason text,
  generated_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  generated_at timestamptz NOT NULL DEFAULT now(),
  submitted_at timestamptz,

  CONSTRAINT wps_file_status_valid CHECK (status IN (
    'generated', 'submitted', 'accepted', 'rejected')),
  CONSTRAINT wps_file_rejection_says_why CHECK (
    status <> 'rejected' OR btrim(coalesce(rejection_reason, '')) <> '')
);

CREATE INDEX wps_file_run_idx ON wps_file (run_id);
CREATE INDEX wps_file_company_idx ON wps_file (company_id, generated_at DESC);
CREATE INDEX wps_file_tenant_idx ON wps_file (tenant_id);

ALTER TABLE wps_file ENABLE ROW LEVEL SECURITY;
ALTER TABLE wps_file FORCE  ROW LEVEL SECURITY;
CREATE POLICY wps_file_isolation ON wps_file
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The accounts payroll posts to
-- ---------------------------------------------------------------------------

INSERT INTO account (tenant_id, company_id, code, name, translations, type)
SELECT c.tenant_id, c.id, v.code, v.name,
       jsonb_build_object('ar', v.name_ar), v.kind
FROM company c
CROSS JOIN (VALUES
  ('2600', 'Wages Payable',            'الأجور المستحقة',        'liability'),
  ('2610', 'GOSI Payable',             'التأمينات الاجتماعية المستحقة', 'liability'),
  ('2620', 'End of Service Provision', 'مخصص نهاية الخدمة',      'liability'),
  ('1250', 'Employee Advances',        'سلف الموظفين',           'asset'),
  ('5240', 'Employer GOSI',            'حصة صاحب العمل بالتأمينات', 'expense'),
  ('5250', 'End of Service Cost',      'تكلفة نهاية الخدمة',     'expense'),
  ('5260', 'Sales Commission',         'عمولات المبيعات',        'expense')
) AS v(code, name, name_ar, kind)
ON CONFLICT (company_id, code) DO NOTHING;

INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
SELECT a.tenant_id, a.company_id, v.role, a.id
FROM account a
JOIN (VALUES
  ('2600', 'wages_payable'),
  ('2610', 'gosi_payable'),
  ('2620', 'eosb_provision'),
  ('1250', 'employee_advances'),
  ('5240', 'employer_gosi'),
  ('5250', 'eosb_expense'),
  ('5260', 'commission_expense')
) AS v(code, role) ON v.code = a.code
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The posting rules
-- ---------------------------------------------------------------------------
--
-- Design 02 rule 6 is "salary payment". It is split into three events here
-- because they happen on different days and a single rule cannot express that:
-- the wage is EARNED in the month worked, PAID some days later, and the
-- employer's own social insurance is a separate cost that is never deducted
-- from anybody.

INSERT INTO posting_rule (rule_key, country, version, lines, description, effective_from)
VALUES

-- The month's wages, owed. Gross to expense, and the two things withheld from
-- it become liabilities of their own rather than reducing the expense: the
-- business's cost of employing somebody is what they earned, not what reached
-- their bank.
('payroll.accrue', NULL, 1,
 '[{"role": "expense_salaries",  "side": "debit",  "amount": "gross"},
   {"role": "gosi_payable",      "side": "credit", "amount": "gosi_employee"},
   {"role": "employee_advances", "side": "credit", "amount": "advance_recovery"},
   {"role": "wages_payable",     "side": "credit", "amount": "net"}]'::jsonb,
 'Wages earned for the period: the whole cost to the business, and what is withheld from it.',
 '2020-01-01'),

-- The employer's own contribution. An expense on top of the wage, owed to the
-- same authority.
('payroll.employer_gosi', NULL, 1,
 '[{"role": "employer_gosi", "side": "debit",  "amount": "amount"},
   {"role": "gosi_payable",  "side": "credit", "amount": "amount"}]'::jsonb,
 'The employer''s social insurance contribution: a cost of employing somebody, not a deduction from them.',
 '2020-01-01'),

-- Paying it out. No expense here — that happened when the wage was earned.
('payroll.pay', NULL, 1,
 '[{"role": "wages_payable",     "side": "debit",  "amount": "amount"},
   {"for_each": "payment_account", "side": "credit"}]'::jsonb,
 'Wages paid: the liability settles and the money leaves.',
 '2020-01-01'),

-- An advance is a loan to the employee, not a cost.
('payroll.advance', NULL, 1,
 '[{"role": "employee_advances", "side": "debit",  "amount": "amount"},
   {"for_each": "payment_account", "side": "credit"}]'::jsonb,
 'A salary advance: money out, and money owed back.',
 '2020-01-01'),

-- What this month added to the end-of-service obligation. E6 makes this a real
-- liability; accruing monthly is what stops it being discovered at termination.
('payroll.eosb_accrue', NULL, 1,
 '[{"role": "eosb_expense",   "side": "debit",  "amount": "amount"},
   {"role": "eosb_provision", "side": "credit", "amount": "amount"}]'::jsonb,
 'End-of-service benefit earned this month.',
 '2020-01-01')

ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------
--
-- A6.1 gives the HR Manager "employees, attendance, payroll setup — no
-- sales/inventory access", and A6.2 requires that staff can be blocked from
-- seeing "other employees' salaries". So seeing the DIRECTORY and seeing the
-- PAY are separate permissions, and a Store Manager gets the first without the
-- second: they roster their branch without learning what the branch earns.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',         'hr.view'),        ('owner',         'hr.manage'),
  ('owner',         'hr.view_pay'),    ('owner',         'payroll.run'),
  ('owner',         'payroll.approve'),('owner',         'payroll.view'),

  ('hr_manager',    'hr.view'),        ('hr_manager',    'hr.manage'),
  ('hr_manager',    'hr.view_pay'),    ('hr_manager',    'payroll.run'),
  ('hr_manager',    'payroll.view'),

  -- Rosters and attendance, not wages.
  ('store_manager', 'hr.view'),        ('store_manager', 'hr.manage'),

  ('accountant',    'hr.view'),        ('accountant',    'hr.view_pay'),
  ('accountant',    'payroll.view'),   ('accountant',    'payroll.approve'),

  ('auditor',       'hr.view'),        ('auditor',       'payroll.view')
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
      ('owner',         'hr.view'),        ('owner',         'hr.manage'),
      ('owner',         'hr.view_pay'),    ('owner',         'payroll.run'),
      ('owner',         'payroll.approve'),('owner',         'payroll.view'),
      ('hr_manager',    'hr.view'),        ('hr_manager',    'hr.manage'),
      ('hr_manager',    'hr.view_pay'),    ('hr_manager',    'payroll.run'),
      ('hr_manager',    'payroll.view'),
      ('store_manager', 'hr.view'),        ('store_manager', 'hr.manage'),
      ('accountant',    'hr.view'),        ('accountant',    'hr.view_pay'),
      ('accountant',    'payroll.view'),   ('accountant',    'payroll.approve'),
      ('auditor',       'hr.view'),        ('auditor',       'payroll.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;

-- ---------------------------------------------------------------------------
-- Numbering
-- ---------------------------------------------------------------------------

ALTER TABLE company
  ADD COLUMN IF NOT EXISTS next_employee_no bigint NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS next_payroll_no  bigint NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS next_advance_no  bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_document_no(
  p_company_id uuid, p_kind text
) RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE
  v_next bigint;
BEGIN
  IF p_kind = 'delivery' THEN
    UPDATE company SET next_delivery_no = next_delivery_no + 1
    WHERE id = p_company_id RETURNING next_delivery_no - 1 INTO v_next;
  ELSIF p_kind = 'service' THEN
    UPDATE company SET next_service_no = next_service_no + 1
    WHERE id = p_company_id RETURNING next_service_no - 1 INTO v_next;
  ELSIF p_kind = 'installment' THEN
    UPDATE company SET next_installment_no = next_installment_no + 1
    WHERE id = p_company_id RETURNING next_installment_no - 1 INTO v_next;
  ELSIF p_kind = 'employee' THEN
    UPDATE company SET next_employee_no = next_employee_no + 1
    WHERE id = p_company_id RETURNING next_employee_no - 1 INTO v_next;
  ELSIF p_kind = 'payroll' THEN
    UPDATE company SET next_payroll_no = next_payroll_no + 1
    WHERE id = p_company_id RETURNING next_payroll_no - 1 INTO v_next;
  ELSIF p_kind = 'advance' THEN
    UPDATE company SET next_advance_no = next_advance_no + 1
    WHERE id = p_company_id RETURNING next_advance_no - 1 INTO v_next;
  ELSE
    RAISE EXCEPTION 'unknown document kind: %', p_kind;
  END IF;

  IF v_next IS NULL THEN
    RAISE EXCEPTION 'company % not found', p_company_id;
  END IF;
  RETURN v_next;
END;
$$;
