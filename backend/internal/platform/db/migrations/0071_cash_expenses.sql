-- 0071 — Cash expenses, and the expense-head model design 02 rule 5 needs.
--
-- Blueprint C3 opens with the sentence this module answers: "the Owner must be
-- able to see, in one click, exactly where every riyal is going". Until now
-- they could not, because there was no way to record an expense at all. Rent,
-- electricity, staff tea and the delivery man were invisible to the books
-- unless a supplier happened to raise a bill for them.
--
-- # Why this took a model rather than one more posting rule
--
-- `expense.cash` has been seeded since 0025 and has never been callable. P32
-- recorded exactly why, and the diagnosis was right:
--
--   Design 02 rule 5 debits "Expense Account", meaning whichever head the
--   transaction is for, and design 12 §1 offers Rent, Utilities, Salaries and
--   Marketing as separate accounts with no generic one among them. A fixed
--   `role` cannot name the one a user picked, so the rule needs a `for_each`
--   over an expense head — a rule change requiring the expense-head model,
--   with its `input_vat_recoverable` flag, that is not built. Choosing one
--   account for every cash expense would be inventing an accounting rule.
--
-- So the model is built here, from the documents, and the rule follows it. No
-- account is chosen on anybody's behalf: a head names its own account, and a
-- company with no heads records no expenses until somebody sets one up.
--
-- # Input VAT recoverability is the reason the head is a table
--
-- Blueprint E2.3 and design 02 rule 5: entertainment, some vehicles and fuel
-- have RESTRICTED input VAT recovery. Each head carries
-- `input_vat_recoverable boolean`; when false the VAT is absorbed into the
-- expense rather than claimed, "so the VAT return is not overstated".
--
-- Absorbed, not discarded. The whole gross still leaves the bank, so the
-- expense account has to carry the part the tax authority will not refund or
-- the entry does not balance. Both halves are stored on the line, because a
-- year later "why is this expense 115 when the invoice says 100" has to have an
-- answer that is not a recomputation.
--
-- # What this migration deliberately does NOT build
--
-- Blueprint C3.1 also asks for recurring expenses, an approval workflow with
-- configurable thresholds, receipt-photo attachments, departments, and
-- per-production-batch cost allocation. None of them are here. Each is a
-- feature in its own right rather than part of recording an expense, and
-- shipping a half-built approval chain — one that records an approver but
-- enforces nothing — would be worse than shipping none: it would look like a
-- control.

-- ---------------------------------------------------------------------------
-- The expense accounts design 12 §1 names
-- ---------------------------------------------------------------------------
--
-- 5200 Rent · 5210 Utilities · 5220 Salaries · 5230 Marketing.
--
-- 5200 was taken. The seeded chart put Stock Write-off there, and design 12
-- puts Inventory Write-off at 5400 — a drift, in the code rather than in the
-- document. Renumbering it is a RELABEL and nothing more: the account keeps its
-- id, so every journal line already pointing at it still does, and no balance
-- moves. Guarded on 5400 being free in that company, because a shop that has
-- customised its chart may already have put something there, and a code
-- collision is not worth a failed migration.

UPDATE account a
SET code = '5400'
WHERE a.code = '5200'
  AND a.name = 'Stock Write-off'
  AND NOT EXISTS (
    SELECT 1 FROM account other
    WHERE other.company_id = a.company_id AND other.code = '5400');

-- The four heads' accounts, for every company that already has a chart.
--
-- Roles as well as accounts, so the seeded heads below can find them by name
-- rather than by a code somebody may have changed. Nothing resolves these roles
-- today — an expense head names its ACCOUNT, which is the whole point of the
-- model — but a chart account without a role is unreachable from anything that
-- comes later, and rule 6 (salary) will want `expense_salaries` when payroll
-- is built.
DO $$
DECLARE
  c record;
  a record;
  acct uuid;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR c IN SELECT id, tenant_id FROM company LOOP
    PERFORM set_config('app.tenant_id', c.tenant_id::text, true);

    -- Only companies that have been given a chart. One that has not is either
    -- brand new or imported, and either way inventing five accounts inside it
    -- is not this migration's business.
    CONTINUE WHEN NOT EXISTS (
      SELECT 1 FROM account WHERE company_id = c.id);

    FOR a IN
      SELECT * FROM (VALUES
        ('5200', 'Rent',       'expense_rent'),
        ('5210', 'Utilities',  'expense_utilities'),
        ('5220', 'Salaries',   'expense_salaries'),
        ('5230', 'Marketing',  'expense_marketing')
      ) AS v(code, name, role)
    LOOP
      INSERT INTO account (tenant_id, company_id, code, name, type)
      VALUES (c.tenant_id, c.id, a.code, a.name, 'expense')
      ON CONFLICT (company_id, code) DO UPDATE SET code = EXCLUDED.code
      RETURNING id INTO acct;

      INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
      VALUES (c.tenant_id, c.id, a.role, acct)
      ON CONFLICT (company_id, role) DO NOTHING;
    END LOOP;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END $$;

-- ---------------------------------------------------------------------------
-- Expense heads
-- ---------------------------------------------------------------------------
--
-- "Expense Categories & Custom Heads (fully configurable tree, e.g. Operating
-- Expense → Rent / Utilities / Salary / Marketing / Transport / Maintenance)"
-- — blueprint C3.1.
--
-- The tree is the chart of accounts, which already has `parent_id`. A head is
-- the thin layer the chart does not carry: the name a shopkeeper uses for it,
-- and the one fact the tax authority cares about.

CREATE TABLE expense_head (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- Short and typed by a person, like a supplier code or a customer code.
  code        text NOT NULL,
  name        text NOT NULL,
  -- The Law of Commercial Books expects records to be available in Arabic, and
  -- an expense report is a record. Optional, like every other Arabic name in
  -- this schema: a shop can add it later and the interface falls back.
  name_ar     text,

  -- Where it posts. RESTRICT rather than CASCADE: deleting an account that
  -- expenses have been booked to would orphan the head and, with it, the
  -- explanation of where a year of rent went.
  account_id  uuid NOT NULL REFERENCES account(id) ON DELETE RESTRICT,

  -- E2.3. False for entertainment, some vehicles and fuel; true for ordinary
  -- business costs. Deliberately NOT NULL and with no default: a head created
  -- without somebody deciding this is a head that will silently claim VAT.
  input_vat_recoverable boolean NOT NULL,

  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT expense_head_code_present CHECK (length(btrim(code)) > 0),
  CONSTRAINT expense_head_name_present CHECK (length(btrim(name)) > 0)
);

-- Case-insensitively unique, like every other human-typed code in this schema:
-- "RENT" and "rent" are one head to a shopkeeper and two to a database.
CREATE UNIQUE INDEX expense_head_code_uq
  ON expense_head (company_id, upper(btrim(code)));
CREATE INDEX expense_head_tenant_idx  ON expense_head (tenant_id);
CREATE INDEX expense_head_account_idx ON expense_head (account_id);

-- A head must point at an expense account of its OWN company, and one that can
-- be posted to.
--
-- A foreign key cannot say any of that: account_id references account(id) and
-- stops there, so a head could name a sister company's Rent — both are in the
-- tenant, so row-level security sees nothing wrong — or the "5000 Expenses"
-- heading, which is not postable, or Cash, which is not an expense at all.
-- Every one of those produces a journal entry that balances and is wrong.
CREATE OR REPLACE FUNCTION assert_expense_head_account() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  a record;
BEGIN
  SELECT company_id, type, is_postable INTO a
  FROM account WHERE id = NEW.account_id;

  IF a IS NULL OR a.company_id <> NEW.company_id THEN
    RAISE EXCEPTION
      'That account belongs to another business, so an expense head here '
      'cannot post to it.' USING ERRCODE = 'raise_exception';
  END IF;
  IF a.type <> 'expense' THEN
    RAISE EXCEPTION
      'An expense head must post to an expense account. That one is a % '
      'account.', a.type USING ERRCODE = 'raise_exception';
  END IF;
  IF NOT a.is_postable THEN
    RAISE EXCEPTION
      'That account is a heading rather than one entries can be posted to. '
      'Pick the account underneath it.' USING ERRCODE = 'raise_exception';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER expense_head_account_is_usable
  BEFORE INSERT OR UPDATE OF account_id, company_id ON expense_head
  FOR EACH ROW EXECUTE FUNCTION assert_expense_head_account();

ALTER TABLE expense_head ENABLE ROW LEVEL SECURITY;
ALTER TABLE expense_head FORCE  ROW LEVEL SECURITY;
CREATE POLICY expense_head_isolation ON expense_head
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- The expense itself
-- ---------------------------------------------------------------------------

CREATE TABLE expense (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- Client-assigned, like every other document here, so a lost response does
  -- not pay the electricity bill twice.
  uuid        uuid NOT NULL,

  -- Sequential per company, so somebody can say "expense 214" out loud.
  expense_no  text NOT NULL,

  -- The date it belongs to, which is the date it posts on. Not today: a receipt
  -- keyed a week late belongs in the period it was incurred, and a fiscal
  -- period that has since closed refuses it, which is the correct outcome
  -- rather than a silent reassignment.
  expense_date date NOT NULL,

  -- Blueprint C3.1 asks the entry to store the branch and the vendor. Both
  -- optional: head-office rent belongs to no branch, and the man who fixed the
  -- door is not a supplier anybody wants a record for.
  store_id    uuid REFERENCES store(id)    ON DELETE RESTRICT,
  supplier_id uuid REFERENCES supplier(id) ON DELETE RESTRICT,

  -- The supplier's own document number, where there is one. Not unique: a taxi
  -- receipt has no number and two of them are not a duplicate.
  reference   text,
  description text,

  -- Which account the money left. Cash or bank — the two rule 5 names. A role
  -- rather than an account id, because these two ARE configuration: every
  -- company has exactly one of each and the chart already maps them.
  paid_from   text NOT NULL,

  currency    text NOT NULL,

  -- The reckoning, all of it stored rather than derived.
  --
  -- tax_recoverable and tax_absorbed exist separately because a year later
  -- "why is this rent 115 when the invoice says 100" needs an answer that is
  -- not a recomputation against whatever the head's flag says TODAY. The flag
  -- can be changed; what was claimed cannot.
  subtotal_net    numeric(18,4) NOT NULL DEFAULT 0,
  tax_total       numeric(18,4) NOT NULL DEFAULT 0,
  tax_recoverable numeric(18,4) NOT NULL DEFAULT 0,
  tax_absorbed    numeric(18,4) NOT NULL DEFAULT 0,
  total_inclusive numeric(18,4) NOT NULL DEFAULT 0,

  journal_entry_id uuid REFERENCES journal_entry(id),

  created_at  timestamptz NOT NULL DEFAULT now(),
  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT expense_uuid_uq UNIQUE (company_id, uuid),
  CONSTRAINT expense_no_uq   UNIQUE (company_id, expense_no),

  CONSTRAINT expense_paid_from_valid CHECK (paid_from IN ('cash', 'bank')),
  CONSTRAINT expense_currency_upper  CHECK (currency = upper(currency)),

  -- A negative expense is a credit note from a supplier, which is a different
  -- document with a different posting. Refused rather than absorbed.
  CONSTRAINT expense_amounts_non_negative CHECK (
    subtotal_net >= 0 AND tax_total >= 0 AND tax_recoverable >= 0
    AND tax_absorbed >= 0 AND total_inclusive > 0),

  -- The two identities the posting depends on. If either could drift the
  -- journal entry would not balance, and the deferred balance trigger would
  -- refuse it a long way from the cause.
  CONSTRAINT expense_tax_splits CHECK (tax_total = tax_recoverable + tax_absorbed),
  CONSTRAINT expense_adds_up    CHECK (total_inclusive = subtotal_net + tax_total)
);

CREATE INDEX expense_tenant_idx  ON expense (tenant_id);
CREATE INDEX expense_company_idx ON expense (company_id, expense_date DESC);
CREATE INDEX expense_store_idx   ON expense (store_id) WHERE store_id IS NOT NULL;

-- A posted expense is a journal entry somebody can be shown. Corrected by a
-- reversal, like everything else in these books, not by an edit.
CREATE TRIGGER expense_immutable
  BEFORE DELETE ON expense
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

CREATE TRIGGER expense_frozen
  BEFORE UPDATE ON expense
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'company_id', 'uuid', 'expense_no', 'expense_date', 'paid_from',
    'subtotal_net', 'tax_total', 'tax_recoverable', 'tax_absorbed',
    'total_inclusive');

ALTER TABLE expense ENABLE ROW LEVEL SECURITY;
ALTER TABLE expense FORCE  ROW LEVEL SECURITY;
CREATE POLICY expense_isolation ON expense
  USING (tenant_id = current_tenant_id());

CREATE TABLE expense_line (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  expense_id  uuid NOT NULL REFERENCES expense(id) ON DELETE CASCADE,
  line_no     integer NOT NULL,

  head_id     uuid NOT NULL REFERENCES expense_head(id) ON DELETE RESTRICT,
  description text,

  net_amount    numeric(18,4) NOT NULL,
  tax_treatment text NOT NULL,
  tax_rate      numeric(9,6) NOT NULL,
  tax_amount    numeric(18,4) NOT NULL,

  -- The split, per line, at the moment it was recorded.
  tax_recoverable numeric(18,4) NOT NULL,
  tax_absorbed    numeric(18,4) NOT NULL,

  -- What the expense account is actually debited: the net, plus whatever VAT
  -- the shop cannot reclaim. This is the figure a "where is my money going"
  -- report has to sum, and storing it means that report cannot disagree with
  -- the ledger about what rent cost.
  charge_amount numeric(18,4) NOT NULL,

  CONSTRAINT expense_line_net_positive CHECK (net_amount > 0),
  CONSTRAINT expense_line_tax_non_negative CHECK (
    tax_amount >= 0 AND tax_recoverable >= 0 AND tax_absorbed >= 0),
  CONSTRAINT expense_line_tax_splits CHECK (
    tax_amount = tax_recoverable + tax_absorbed),
  CONSTRAINT expense_line_charge CHECK (charge_amount = net_amount + tax_absorbed)
);

CREATE UNIQUE INDEX expense_line_no_uq ON expense_line (expense_id, line_no);
CREATE INDEX expense_line_tenant_idx ON expense_line (tenant_id);
CREATE INDEX expense_line_head_idx   ON expense_line (head_id);

CREATE TRIGGER expense_line_immutable
  BEFORE UPDATE OR DELETE ON expense_line
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE expense_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE expense_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY expense_line_isolation ON expense_line
  USING (tenant_id = current_tenant_id());

-- The lines must add up to the header, deferred to commit for the same reason
-- assert_invoice_balanced is: the rows arrive one at a time and the document is
-- legitimately unbalanced in between.
CREATE OR REPLACE FUNCTION assert_expense_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  e   record;
  net numeric(18,4);
  tax numeric(18,4);
  rec numeric(18,4);
  abs_ numeric(18,4);
  n   integer;
BEGIN
  SELECT * INTO e FROM expense WHERE id = NEW.expense_id;
  IF e IS NULL THEN
    RETURN NULL;   -- rolled back within this transaction
  END IF;

  SELECT coalesce(sum(net_amount), 0), coalesce(sum(tax_amount), 0),
         coalesce(sum(tax_recoverable), 0), coalesce(sum(tax_absorbed), 0),
         count(*)
  INTO net, tax, rec, abs_, n
  FROM expense_line WHERE expense_id = NEW.expense_id;

  IF n = 0 THEN
    RAISE EXCEPTION 'An expense must have at least one line.'
      USING ERRCODE = 'P0001';
  END IF;
  IF e.subtotal_net <> net THEN
    RAISE EXCEPTION 'This expense is recorded as % net and its lines come to %.',
      e.subtotal_net, net USING ERRCODE = 'P0001';
  END IF;
  IF e.tax_total <> tax OR e.tax_recoverable <> rec OR e.tax_absorbed <> abs_ THEN
    RAISE EXCEPTION
      'This expense claims % of VAT (% recoverable, % absorbed) and its lines '
      'carry % (% recoverable, % absorbed). What the VAT return claims has to '
      'be what the lines say.',
      e.tax_total, e.tax_recoverable, e.tax_absorbed, tax, rec, abs_
      USING ERRCODE = 'P0001';
  END IF;
  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER expense_line_adds_up
  AFTER INSERT ON expense_line
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_expense_balanced();

-- Numbering, the same row-locking mechanism the invoice and receipt series use.
ALTER TABLE company ADD COLUMN next_expense_no bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_expense_no(p_company_id uuid)
RETURNS text
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  UPDATE company
  SET next_expense_no = next_expense_no + 1
  WHERE id = p_company_id
  RETURNING next_expense_no - 1 INTO claimed;

  IF claimed IS NULL THEN
    RAISE EXCEPTION 'no such company' USING ERRCODE = 'no_data_found';
  END IF;

  RETURN 'EXP-' || to_char(claimed, 'FM000000');
END;
$$;

-- ---------------------------------------------------------------------------
-- Rule 5, at last callable
-- ---------------------------------------------------------------------------
--
--   Dr  Expense Account          net + VAT that cannot be reclaimed
--   Dr  Input VAT Receivable     only the part that can
--       Cr  Cash / Bank          the whole gross, which is what left the bank
--
-- Version 2 rather than an edit. A posting rule is versioned data an auditor
-- can be shown, and version 1 is what the books would have used had anything
-- ever called it. Effective from the same date so nothing is left resolving to
-- a rule that cannot post.
--
-- Both debits and the credit are `for_each` groups now:
--
--   `expense_lines` — one line per head, naming the head's ACCOUNT. This is
--     what version 1 could not express and what P32 correctly refused to fake
--     by picking a single account for everything.
--   `payments`      — unchanged from version 1: the money left cash or bank.
--
-- The recoverable VAT stays a single role line. Input VAT Receivable is one
-- account per company whatever the expense was for, so there is nothing to
-- repeat.

INSERT INTO posting_rule
  (rule_key, country, version, lines, description, effective_from)
SELECT
  'expense.cash', NULL, max(version) + 1,
  '[{"side": "debit",  "for_each": "expense_lines"},
    {"side": "debit",  "role": "input_vat", "amount": "recoverable_tax"},
    {"side": "credit", "for_each": "payments"}]'::jsonb,
  'Expense paid from cash or bank. Each line debits its own head''s account, '
  'carrying any VAT the head cannot reclaim; only recoverable VAT is claimed.',
  min(effective_from)
FROM posting_rule
WHERE rule_key = 'expense.cash';

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------
--
-- Three verbs, split the way 0065 argues for: by the SIZE of the decision.
--
--   expense.view          reading what the shop has spent. Everyone who
--                         already reads figures, including the auditor.
--   expense.record        entering one. A daily clerical act.
--   expense.manage_heads  deciding that fuel VAT cannot be reclaimed. That is
--                         a tax position, not data entry: get it wrong and
--                         every future VAT return is overstated, which is a
--                         matter for the tax authority rather than a typo.
--
-- Blueprint A4 gives the Accountant "Accounts, journals, expenses, VAT
-- reports", so they hold all three. The Store Manager may see what their branch
-- spends and may not decide the shop's tax positions.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',         'expense.view'),
  ('owner',         'expense.record'),
  ('owner',         'expense.manage_heads'),
  ('accountant',    'expense.view'),
  ('accountant',    'expense.record'),
  ('accountant',    'expense.manage_heads'),
  ('store_manager', 'expense.view'),
  ('auditor',       'expense.view')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

-- Existing tenants, one at a time, for the reason 0042 records: `role` has
-- FORCE ROW LEVEL SECURITY and a tenant predicate, so an unqualified backfill
-- would silently do nothing. Joined through cloned_from rather than r.key,
-- matching 0043 and 0065: a tenant may have renamed their Owner role, and
-- matching on the key would skip the very role that needs the grant.
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
      ('owner',         'expense.view'),
      ('owner',         'expense.record'),
      ('owner',         'expense.manage_heads'),
      ('accountant',    'expense.view'),
      ('accountant',    'expense.record'),
      ('accountant',    'expense.manage_heads'),
      ('store_manager', 'expense.view'),
      ('auditor',       'expense.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END $$;

-- ---------------------------------------------------------------------------
-- The heads design 12 §1's chart implies
-- ---------------------------------------------------------------------------
--
-- One per expense account the document names, so a shop can record rent on the
-- day it installs the product rather than configuring a chart first.
--
-- Every one of them is recoverable, and that is not a default — it is what the
-- documents say. E2.3 restricts entertainment, some vehicles and fuel; none of
-- Rent, Utilities, Salaries or Marketing is on that list. Heads for the
-- restricted categories are deliberately NOT seeded: design 12's chart has no
-- accounts for them, and inventing an "Entertainment" account so that a flag
-- has something to demonstrate itself on would be exactly the kind of invention
-- P32 refused.

DO $$
DECLARE
  c record;
  h record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR c IN SELECT id, tenant_id FROM company LOOP
    PERFORM set_config('app.tenant_id', c.tenant_id::text, true);

    FOR h IN
      SELECT * FROM (VALUES
        ('RENT',      'Rent',       'الإيجار',        'expense_rent'),
        ('UTILITIES', 'Utilities',  'المرافق',        'expense_utilities'),
        ('SALARIES',  'Salaries',   'الرواتب',        'expense_salaries'),
        ('MARKETING', 'Marketing',  'التسويق',        'expense_marketing'),
        ('BANKFEES',  'Bank charges', 'رسوم بنكية',   'bank_card_charges')
      ) AS v(code, name, name_ar, role)
    LOOP
      INSERT INTO expense_head
        (tenant_id, company_id, code, name, name_ar, account_id,
         input_vat_recoverable)
      SELECT c.tenant_id, c.id, h.code, h.name, h.name_ar, m.account_id, true
      FROM account_role_map m
      WHERE m.company_id = c.id AND m.role = h.role
      ON CONFLICT DO NOTHING;
    END LOOP;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END $$;
