-- 0122 — departments on an expense, and expenses that repeat (blueprint C3.1).
--
-- 0071 recorded what it deliberately left out: "recurring expenses, an approval
-- workflow with configurable thresholds, receipt-photo attachments,
-- departments, and per-production-batch cost allocation". The approval workflow
-- arrived in 0079/0080 and receipt attachments in 0096 — `document` lists
-- 'expense' among the things it attaches to, and D6 names "expense receipts"
-- outright. Two of that list are still missing, and this migration is both.
--
-- ---------------------------------------------------------------------------
-- Departments
-- ---------------------------------------------------------------------------
--
-- C3.1 line 485: an expense stores "date, amount, currency, expense type,
-- expense head, store/branch, department, payment account used, vendor,
-- description, file attachment, created-by, approved-by". Everything on that
-- list is already stored except the department.
--
-- # A table, not free text
--
-- `employee.department` is free text, which is the obvious precedent and the
-- wrong one to follow here. The requirement this serves is D1's — "see where
-- every cost is going, per day ... filterable by day / week / month / year /
-- custom range" — and a dimension you filter by cannot be free text: "Sales",
-- "sales" and "Sales " are three departments to a GROUP BY and one to the
-- person who typed them.
--
-- # Deactivated, never deleted
--
-- A department that has been used is part of the history of every expense
-- booked to it. `ON DELETE RESTRICT` on the expense's reference means a
-- department in use cannot be removed at all, and `is_active` is how one stops
-- being offered. Last year's expense keeps naming the department it was
-- actually booked to, which is what makes last year's report reproducible.

CREATE TABLE department (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  code        text NOT NULL,
  name        text NOT NULL,
  name_ar     text,

  is_active   boolean NOT NULL DEFAULT true,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT department_code_not_blank CHECK (btrim(code) <> ''),
  CONSTRAINT department_name_not_blank CHECK (btrim(name) <> '')
);

CREATE UNIQUE INDEX department_code_uq ON department (company_id, lower(code));
CREATE INDEX department_tenant_idx ON department (tenant_id);

ALTER TABLE department ENABLE ROW LEVEL SECURITY;
ALTER TABLE department FORCE  ROW LEVEL SECURITY;
CREATE POLICY department_isolation ON department
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER department_touch BEFORE UPDATE ON department
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- RESTRICT: a department that has been spent against cannot be deleted out from
-- under the history that names it.
ALTER TABLE expense
  ADD COLUMN department_id uuid REFERENCES department(id) ON DELETE RESTRICT;

CREATE INDEX expense_department_idx
  ON expense (company_id, department_id, expense_date DESC)
  WHERE department_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Recurring expenses
-- ---------------------------------------------------------------------------
--
-- Rent, a subscription, a cleaner's monthly fee. The shop agrees the amount
-- once and it falls due on a schedule, and somebody re-typing it every month is
-- how it gets forgotten in a busy month and typed twice in a slow one.
--
-- # A template, and a record of what it has produced
--
-- The schedule does not post anything by itself. It describes an expense and
-- says when the next one is due; a job (or a person) turns a due schedule into
-- an ordinary `expense`, which then behaves exactly like one somebody typed —
-- same posting, same approval rules, same audit.
--
-- # Running the job twice must not pay the rent twice
--
-- `recurring_expense_run` is the guard, and it is a UNIQUE constraint rather
-- than a check the generator performs: one row per (schedule, due date), so a
-- second attempt at the same period is refused by the database no matter how
-- many workers race for it. The generated expense's id is on that row, so a
-- retry can find what it already made.
--
-- # Dates
--
-- `next_due_on` is stored rather than derived. A monthly schedule starting on
-- the 31st has no 31st in February, and every rule for "the same day next
-- month" is a decision somebody has to be able to see and correct — so the
-- generator advances the date and writes it down, and an operator can move it.

CREATE TABLE recurring_expense (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name        text NOT NULL,

  -- What each generated expense looks like. The same shape an expense carries,
  -- because that is what this produces.
  head_id     uuid NOT NULL REFERENCES expense_head(id) ON DELETE RESTRICT,
  store_id    uuid REFERENCES store(id)      ON DELETE RESTRICT,
  supplier_id uuid REFERENCES supplier(id)   ON DELETE RESTRICT,
  department_id uuid REFERENCES department(id) ON DELETE RESTRICT,

  amount      numeric(18,4) NOT NULL,
  currency    text NOT NULL,
  paid_from   text NOT NULL,
  description text,

  -- 'monthly', 'weekly', 'yearly'. Text with a CHECK for the same reason
  -- `document.entity_type` is: a new cadence should not need a type migration.
  frequency   text NOT NULL,
  -- How many periods apart. 1 is every month; 3 with 'monthly' is quarterly,
  -- which is why there is no separate 'quarterly'.
  interval_count integer NOT NULL DEFAULT 1,

  starts_on   date NOT NULL,
  ends_on     date,
  next_due_on date NOT NULL,

  is_active   boolean NOT NULL DEFAULT true,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT recurring_expense_name_not_blank CHECK (btrim(name) <> ''),
  CONSTRAINT recurring_expense_amount_positive CHECK (amount > 0),
  CONSTRAINT recurring_expense_currency_upper CHECK (currency = upper(currency)),
  CONSTRAINT recurring_expense_frequency_known
    CHECK (frequency IN ('weekly', 'monthly', 'yearly')),
  CONSTRAINT recurring_expense_interval_positive
    CHECK (interval_count > 0 AND interval_count <= 60),
  CONSTRAINT recurring_expense_ends_after_start
    CHECK (ends_on IS NULL OR ends_on >= starts_on),
  CONSTRAINT recurring_expense_due_within_window
    CHECK (next_due_on >= starts_on)
);

CREATE INDEX recurring_expense_tenant_idx ON recurring_expense (tenant_id);
-- What the generator asks for: everything active and due.
CREATE INDEX recurring_expense_due_idx
  ON recurring_expense (next_due_on) WHERE is_active;

ALTER TABLE recurring_expense ENABLE ROW LEVEL SECURITY;
ALTER TABLE recurring_expense FORCE  ROW LEVEL SECURITY;
CREATE POLICY recurring_expense_isolation ON recurring_expense
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER recurring_expense_touch BEFORE UPDATE ON recurring_expense
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- One expense per schedule per due date, enforced by the database.
CREATE TABLE recurring_expense_run (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  recurring_expense_id uuid NOT NULL
    REFERENCES recurring_expense(id) ON DELETE CASCADE,

  due_on      date NOT NULL,
  expense_id  uuid NOT NULL REFERENCES expense(id) ON DELETE RESTRICT,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- The guard. A second run for the same period is refused here rather than
-- avoided by a check the generator remembers to perform.
CREATE UNIQUE INDEX recurring_expense_run_uq
  ON recurring_expense_run (recurring_expense_id, due_on);
CREATE INDEX recurring_expense_run_tenant_idx
  ON recurring_expense_run (tenant_id);

ALTER TABLE recurring_expense_run ENABLE ROW LEVEL SECURITY;
ALTER TABLE recurring_expense_run FORCE  ROW LEVEL SECURITY;
CREATE POLICY recurring_expense_run_isolation ON recurring_expense_run
  USING (tenant_id = current_tenant_id());

-- A generated expense is history like any other. The link to what produced it
-- is not editable afterwards.
CREATE TRIGGER recurring_expense_run_no_delete
  BEFORE DELETE ON recurring_expense_run
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

COMMENT ON COLUMN recurring_expense.next_due_on IS
  'Stored rather than derived: a monthly schedule starting on the 31st has no '
  '31st in February, and the rule for that is a decision an operator must be '
  'able to see and move.';
