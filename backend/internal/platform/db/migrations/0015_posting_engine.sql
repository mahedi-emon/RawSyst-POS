-- 0015 — The double-entry posting engine.
--
-- Pillar 2. Blueprint C9: "total debits must always equal total credits — an
-- unbalanced entry can never be saved", and entries are "immutable once posted;
-- corrections are made by reversing entries, never by editing history."
--
-- Both are enforced here rather than in Go. An application check is a promise;
-- a deferred constraint is a guarantee that holds against a background job, a
-- migration, a support script and a future developer who has not read C9.
--
-- # Why posting rules are data
--
-- C9.2 names twelve transaction types and says each needs "its own defined,
-- CONFIGURABLE posting rule". Hard-coding them would also make the product
-- untranslatable across markets: a Saudi sale debits Input VAT on purchase and
-- reclaims it, while a US sale has no input tax at all. Same code, different
-- rule rows.

-- ---------------------------------------------------------------------------
-- Chart of accounts
-- ---------------------------------------------------------------------------

CREATE TABLE account (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  code        text NOT NULL,
  name        text NOT NULL,
  translations jsonb NOT NULL DEFAULT '{}'::jsonb,

  type        text NOT NULL,
  parent_id   uuid REFERENCES account(id) ON DELETE RESTRICT,

  -- A control account is backed by a sub-ledger that must reconcile to it
  -- exactly. C9.3 makes three of these hard invariants: AR, AP and Inventory.
  is_control  boolean NOT NULL DEFAULT false,
  control_of  text,

  -- A posting account holds entries; a header account only groups its
  -- children. Posting to a header is how a chart of accounts silently stops
  -- adding up.
  is_postable boolean NOT NULL DEFAULT true,

  currency    char(3),          -- NULL: uses the company base currency
  is_active   boolean NOT NULL DEFAULT true,

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT account_type_valid CHECK (type IN (
    'asset', 'liability', 'equity', 'revenue', 'expense')),
  CONSTRAINT account_control_of_valid CHECK (
    control_of IS NULL OR control_of IN ('receivable', 'payable', 'inventory')),
  CONSTRAINT account_control_needs_subject CHECK (
    (is_control AND control_of IS NOT NULL) OR (NOT is_control AND control_of IS NULL)),
  CONSTRAINT account_code_format CHECK (code ~ '^[A-Za-z0-9._-]{1,32}$')
);

CREATE UNIQUE INDEX account_code_uq ON account (company_id, code);
CREATE INDEX account_tenant_idx ON account (tenant_id);
CREATE INDEX account_parent_idx ON account (company_id, parent_id);
CREATE UNIQUE INDEX account_control_uq
  ON account (company_id, control_of) WHERE is_control;

CREATE TRIGGER account_touch BEFORE UPDATE ON account
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- An account that has ever been posted to must survive, or historical
-- statements stop reconciling. Deactivate instead.
CREATE TRIGGER account_no_delete BEFORE DELETE ON account
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE account ENABLE ROW LEVEL SECURITY;
ALTER TABLE account FORCE  ROW LEVEL SECURITY;
CREATE POLICY account_isolation ON account USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Account roles: the bridge between a portable rule and a company's own chart
-- ---------------------------------------------------------------------------
--
-- A posting rule says "debit the cash account". Which account that is differs
-- per company, and a rule naming a concrete account id could not be shared.
-- The role is the stable name; the mapping is per company.
CREATE TABLE account_role_map (
  tenant_id  uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  role       text NOT NULL,
  account_id uuid NOT NULL REFERENCES account(id) ON DELETE RESTRICT,
  PRIMARY KEY (company_id, role),
  CONSTRAINT account_role_format CHECK (role ~ '^[a-z][a-z0-9_]*$')
);

CREATE INDEX account_role_map_tenant_idx ON account_role_map (tenant_id);

ALTER TABLE account_role_map ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_role_map FORCE  ROW LEVEL SECURITY;
CREATE POLICY account_role_map_isolation ON account_role_map
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Fiscal periods
-- ---------------------------------------------------------------------------

CREATE TABLE fiscal_period (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  fiscal_year integer  NOT NULL,
  period_no   smallint NOT NULL,
  starts_on   date NOT NULL,
  ends_on     date NOT NULL,

  -- open   → transactions may be posted
  -- closed → no transaction may be created, edited or deleted in this period
  -- locked → closed, and the year-end routine has run
  state       text NOT NULL DEFAULT 'open',

  closed_at   timestamptz,
  closed_by   uuid REFERENCES app_user(id),
  reopened_at timestamptz,
  reopened_by uuid REFERENCES app_user(id),
  -- C10 requires Owner-level permission AND a mandatory reason to reopen. The
  -- reason is not optional metadata: reopening a closed period changes numbers
  -- someone has already reported.
  reopen_reason text,

  CONSTRAINT fiscal_period_state_valid CHECK (state IN ('open', 'closed', 'locked')),
  CONSTRAINT fiscal_period_dates_ordered CHECK (ends_on >= starts_on),
  CONSTRAINT fiscal_period_no_valid CHECK (period_no BETWEEN 1 AND 12),
  CONSTRAINT fiscal_period_reopen_needs_reason CHECK (
    reopened_at IS NULL OR (reopen_reason IS NOT NULL AND length(btrim(reopen_reason)) >= 10))
);

CREATE UNIQUE INDEX fiscal_period_uq ON fiscal_period (company_id, fiscal_year, period_no);
CREATE INDEX fiscal_period_tenant_idx ON fiscal_period (tenant_id);

-- Two periods covering the same day would make "which period does this
-- transaction belong to" ambiguous, and the answer decides which month's
-- statements move.
ALTER TABLE fiscal_period
  ADD CONSTRAINT fiscal_period_no_overlap
  EXCLUDE USING gist (
    company_id WITH =,
    daterange(starts_on, ends_on, '[]') WITH &&
  );

CREATE TRIGGER fiscal_period_no_delete BEFORE DELETE ON fiscal_period
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE fiscal_period ENABLE ROW LEVEL SECURITY;
ALTER TABLE fiscal_period FORCE  ROW LEVEL SECURITY;
CREATE POLICY fiscal_period_isolation ON fiscal_period
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Posting rules, as data
-- ---------------------------------------------------------------------------

CREATE TABLE posting_rule (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  rule_key    text NOT NULL,      -- 'sale.cash', 'sale.cogs', 'purchase.credit'
  country     char(2),            -- NULL: applies anywhere
  version     integer NOT NULL DEFAULT 1,

  -- [{"role":"cash","side":"debit","amount":"total_inclusive"}, ...]
  -- `amount` names a field the caller supplies, so the rule describes the
  -- shape of the entry while the transaction supplies the numbers.
  lines       jsonb NOT NULL,

  description text NOT NULL,
  effective_from date NOT NULL,
  effective_to   date,

  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT posting_rule_key_format CHECK (rule_key ~ '^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$'),
  CONSTRAINT posting_rule_period_ordered CHECK (effective_to IS NULL OR effective_to > effective_from),
  -- A rule producing fewer than two lines cannot balance.
  CONSTRAINT posting_rule_needs_two_lines CHECK (jsonb_array_length(lines) >= 2)
);

CREATE UNIQUE INDEX posting_rule_version_uq
  ON posting_rule (rule_key, coalesce(country, 'xx'), version);
CREATE INDEX posting_rule_lookup_idx ON posting_rule (rule_key, country, effective_from DESC);

-- Rules are versioned, never edited. An entry posted last March must remain
-- explainable by the rule that produced it.
CREATE TRIGGER posting_rule_no_delete BEFORE DELETE ON posting_rule
  FOR EACH ROW EXECUTE FUNCTION reject_delete();
CREATE TRIGGER posting_rule_frozen
  BEFORE UPDATE ON posting_rule
  FOR EACH ROW EXECUTE FUNCTION reject_column_change('rule_key', 'lines', 'version');

-- ---------------------------------------------------------------------------
-- Journal
-- ---------------------------------------------------------------------------

CREATE TABLE journal_entry (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  period_id   uuid NOT NULL REFERENCES fiscal_period(id) ON DELETE RESTRICT,

  entry_no    bigint NOT NULL,
  entry_date  date   NOT NULL,

  -- What caused this entry. Together with rule_key it makes posting idempotent:
  -- replaying a synced sale cannot post it twice.
  source_type text NOT NULL,
  source_id   uuid,
  rule_key    text,

  memo        text,

  -- A correction reverses; it never edits. This points at what is being undone.
  reverses_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,

  posted_at   timestamptz NOT NULL DEFAULT now(),
  posted_by   uuid REFERENCES app_user(id),

  CONSTRAINT journal_entry_source_type_format CHECK (source_type ~ '^[a-z][a-z0-9_]*$')
);

-- The idempotency key. A sale replayed by a sync retry finds this row already
-- present and does not post again. C9's "every transaction posts automatically"
-- is only safe if "every transaction" means exactly once.
CREATE UNIQUE INDEX journal_entry_source_uq
  ON journal_entry (source_type, source_id, coalesce(rule_key, ''))
  WHERE source_id IS NOT NULL;

CREATE UNIQUE INDEX journal_entry_no_uq ON journal_entry (company_id, entry_no);
CREATE INDEX journal_entry_tenant_idx ON journal_entry (tenant_id);
CREATE INDEX journal_entry_period_idx ON journal_entry (period_id);
CREATE INDEX journal_entry_date_idx   ON journal_entry (company_id, entry_date DESC);

CREATE TRIGGER journal_entry_immutable
  BEFORE UPDATE OR DELETE ON journal_entry
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE journal_entry ENABLE ROW LEVEL SECURITY;
ALTER TABLE journal_entry FORCE  ROW LEVEL SECURITY;
CREATE POLICY journal_entry_isolation ON journal_entry
  USING (tenant_id = current_tenant_id());

CREATE TABLE journal_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  entry_id    uuid NOT NULL REFERENCES journal_entry(id) ON DELETE RESTRICT,
  line_no     integer NOT NULL,

  account_id  uuid NOT NULL REFERENCES account(id) ON DELETE RESTRICT,
  store_id    uuid REFERENCES store(id) ON DELETE RESTRICT,

  -- Transaction currency.
  currency    char(3) NOT NULL,
  fx_rate     numeric(18,8) NOT NULL DEFAULT 1,
  debit       numeric(18,4) NOT NULL DEFAULT 0,
  credit      numeric(18,4) NOT NULL DEFAULT 0,

  -- Company base currency. The trial balance is drawn in base currency, so
  -- these are the amounts that must balance.
  base_debit  numeric(18,4) NOT NULL DEFAULT 0,
  base_credit numeric(18,4) NOT NULL DEFAULT 0,

  -- Ties a line to the sub-ledger row it represents, so AR and AP can be
  -- reconciled to their control accounts without guessing.
  subledger_type text,
  subledger_id   uuid,

  memo        text,

  -- A line is a debit or a credit, never both and never neither. Allowing both
  -- lets a net-zero line hide a real movement.
  CONSTRAINT journal_line_one_side CHECK (
    (debit > 0 AND credit = 0) OR (credit > 0 AND debit = 0)),
  CONSTRAINT journal_line_base_one_side CHECK (
    (base_debit > 0 AND base_credit = 0) OR (base_credit > 0 AND base_debit = 0)),
  CONSTRAINT journal_line_amounts_non_negative CHECK (
    debit >= 0 AND credit >= 0 AND base_debit >= 0 AND base_credit >= 0),
  -- A debit in transaction currency must remain a debit in base currency.
  CONSTRAINT journal_line_sides_agree CHECK (
    (debit > 0) = (base_debit > 0)),
  CONSTRAINT journal_line_fx_positive CHECK (fx_rate > 0),
  CONSTRAINT journal_line_currency_upper CHECK (currency = upper(currency))
);

CREATE UNIQUE INDEX journal_line_no_uq ON journal_line (entry_id, line_no);
CREATE INDEX journal_line_account_idx ON journal_line (account_id);
CREATE INDEX journal_line_tenant_idx  ON journal_line (tenant_id);
CREATE INDEX journal_line_subledger_idx
  ON journal_line (subledger_type, subledger_id) WHERE subledger_id IS NOT NULL;

CREATE TRIGGER journal_line_immutable
  BEFORE UPDATE OR DELETE ON journal_line
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE journal_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE journal_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY journal_line_isolation ON journal_line
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The two guarantees
-- ---------------------------------------------------------------------------

-- 1. Debits equal credits.
--
-- Deferred to commit, because lines are inserted one at a time and an entry is
-- necessarily unbalanced between the first line and the last. Checking
-- immediately would make a correct entry impossible to write.
CREATE OR REPLACE FUNCTION assert_entry_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  d numeric(18,4);
  c numeric(18,4);
  n integer;
BEGIN
  SELECT coalesce(sum(base_debit), 0), coalesce(sum(base_credit), 0), count(*)
  INTO d, c, n
  FROM journal_line WHERE entry_id = NEW.entry_id;

  -- The entry may have been rolled back inside this transaction.
  IF n = 0 THEN
    RETURN NULL;
  END IF;

  IF n < 2 THEN
    RAISE EXCEPTION
      'A journal entry needs at least two lines; this one has %.', n
      USING ERRCODE = 'P0001';
  END IF;

  IF d <> c THEN
    RAISE EXCEPTION
      'This entry does not balance: debits total % and credits total % (a difference of %). An unbalanced entry cannot be saved.',
      d, c, abs(d - c)
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER journal_line_balanced
  AFTER INSERT ON journal_line
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_entry_balanced();

-- 2. A closed period accepts nothing.
--
-- C10: "Once a period is closed, no transaction can be created, edited, or
-- deleted in that period — this is what makes financial statements
-- trustworthy." Enforced on write, so a background job posting late cannot
-- reopen last month by accident.
CREATE OR REPLACE FUNCTION assert_period_open() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  s text;
  y integer;
  p smallint;
BEGIN
  SELECT state, fiscal_year, period_no INTO s, y, p
  FROM fiscal_period WHERE id = NEW.period_id;

  IF s IS NULL THEN
    RAISE EXCEPTION 'That accounting period does not exist.' USING ERRCODE = 'P0001';
  END IF;

  IF s <> 'open' THEN
    RAISE EXCEPTION
      'Period % of % is % and cannot accept new entries. Post this to an open period, or ask an owner to reopen it with a reason.',
      p, y, s
      USING ERRCODE = 'P0001';
  END IF;

  -- An entry dated outside its own period would appear in the wrong month's
  -- statements while counting toward a different period's close.
  IF NOT EXISTS (
    SELECT 1 FROM fiscal_period
    WHERE id = NEW.period_id AND NEW.entry_date BETWEEN starts_on AND ends_on
  ) THEN
    RAISE EXCEPTION
      'Entry date % falls outside period % of %.', NEW.entry_date, p, y
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER journal_entry_period_open
  BEFORE INSERT ON journal_entry
  FOR EACH ROW EXECUTE FUNCTION assert_period_open();

-- ---------------------------------------------------------------------------
-- Reconciliation: the invariants C9.3 calls hard
-- ---------------------------------------------------------------------------

-- Returns the difference between each control account and its sub-ledger.
-- Nightly job territory; C13 requires any divergence to be flagged as an
-- exception rather than absorbed.
CREATE OR REPLACE FUNCTION control_account_differences(p_company_id uuid)
RETURNS TABLE (control_of text, control_balance numeric, subledger_balance numeric, difference numeric)
LANGUAGE sql STABLE AS $$
  SELECT a.control_of,
         coalesce(sum(l.base_debit - l.base_credit), 0) AS control_balance,
         coalesce(sum(l.base_debit - l.base_credit) FILTER (WHERE l.subledger_id IS NOT NULL), 0)
           AS subledger_balance,
         coalesce(sum(l.base_debit - l.base_credit), 0)
           - coalesce(sum(l.base_debit - l.base_credit) FILTER (WHERE l.subledger_id IS NOT NULL), 0)
           AS difference
  FROM account a
  LEFT JOIN journal_line l ON l.account_id = a.id
  WHERE a.company_id = p_company_id AND a.is_control
  GROUP BY a.control_of
$$;

-- The trial balance must balance. If this ever returns a non-zero difference
-- the deferred constraint has been bypassed, which is a far more serious
-- finding than an ordinary reconciliation gap.
CREATE OR REPLACE FUNCTION trial_balance_difference(p_company_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(l.base_debit), 0) - coalesce(sum(l.base_credit), 0)
  FROM journal_line l
  JOIN journal_entry e ON e.id = l.entry_id
  WHERE e.company_id = p_company_id
$$;
