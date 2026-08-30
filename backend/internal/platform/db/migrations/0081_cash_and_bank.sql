-- 0081 — Cash and bank accounts, transfers between them, and the bank
-- reconciliation (blueprint C2 and C11).
--
-- The chart has carried one Cash account and one Bank account since
-- provisioning was written, and that is all a company has ever been able to
-- have. C2 asks for something quite different:
--
--   "Cash accounts: Main Cash (vault), per-Store Cash, Petty Cash, per-Cashier
--    Drawer. Bank accounts: multiple bank accounts, card-settlement accounts,
--    and payment-gateway settlement accounts — each with a live running
--    balance."
--
-- # Why a money account is not just a chart account
--
-- The chart already holds a balance per account, and a naive reading would say
-- that is enough. It is not, for three reasons that only apply to the accounts
-- money physically sits in:
--
--   * A bank account has an IBAN, a bank name and a statement to reconcile
--     against. None of that belongs on an account in a chart of accounts.
--   * Reconciliation needs to know which ledger lines have been SEEN on a
--     statement and which have not. That is a fact about a line's relationship
--     to a piece of paper, not about the line.
--   * C2's transfers are a document — "every transfer creates its own audit
--     entry and a printable transfer voucher" — and a document needs a number,
--     a date, a person and two ends.
--
-- So `money_account` is a thin record ATTACHED to a chart account rather than a
-- replacement for one. The balance still comes from the ledger, because a
-- second store of the same number is a second number that can disagree.

-- ---------------------------------------------------------------------------
-- Where money sits
-- ---------------------------------------------------------------------------

CREATE TABLE money_account (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- The chart account this money lands in. One-to-one: two money accounts
  -- pointing at one ledger account would make "the balance of the petty cash
  -- tin" unanswerable, which is the whole point of having them separately.
  account_id  uuid NOT NULL REFERENCES account(id) ON DELETE RESTRICT,

  -- Which branch's money this is. NULL for a company-wide account — the main
  -- vault, the bank.
  store_id    uuid REFERENCES store(id) ON DELETE RESTRICT,

  kind        text NOT NULL,
  name        text NOT NULL,
  name_ar     text,

  -- The currency the account is HELD in, which is not always the company's
  -- base currency: a Saudi company may hold a dollar account. Postings still
  -- reach the ledger in base currency through the existing FX fields on
  -- journal_entry; this is what the account itself is denominated in.
  currency    char(3) NOT NULL,

  -- Bank detail. Null on a cash account, and refused on one by the CHECK
  -- below: a petty cash tin with an IBAN is a data-entry mistake nobody would
  -- otherwise catch.
  bank_name       text,
  account_number  text,
  iban            text,
  swift           text,

  is_active   boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT money_account_kind_valid CHECK (kind IN (
    'cash',        -- a vault, a till float, a branch's cash
    'petty_cash',  -- the small tin, deliberately its own kind
    'bank',        -- a real bank account
    'card_settlement',   -- what an acquirer owes and has not yet paid
    'gateway')),         -- the same for an online payment gateway

  CONSTRAINT money_account_bank_detail_only_on_a_bank CHECK (
    kind IN ('bank', 'card_settlement', 'gateway')
    OR (bank_name IS NULL AND account_number IS NULL
        AND iban IS NULL AND swift IS NULL)),

  CONSTRAINT money_account_currency_upper CHECK (currency = upper(currency)),

  -- An IBAN is 15-34 characters, letters and digits, and this is a format
  -- check rather than a validity one: the country's own check digits are a
  -- regulatory value, and 0004's registry is where a rule like that belongs.
  CONSTRAINT money_account_iban_format CHECK (
    iban IS NULL OR iban ~ '^[A-Z]{2}[0-9A-Z]{13,32}$')
);

CREATE UNIQUE INDEX money_account_ledger_uq ON money_account (account_id);
CREATE INDEX money_account_company_idx ON money_account (company_id) WHERE is_active;
CREATE INDEX money_account_tenant_idx ON money_account (tenant_id);

CREATE TRIGGER money_account_touch BEFORE UPDATE ON money_account
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE money_account ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_account FORCE  ROW LEVEL SECURITY;
CREATE POLICY money_account_isolation ON money_account
  USING (tenant_id = current_tenant_id());

-- Every company's existing Cash and Bank accounts become money accounts, so a
-- shop that has been trading sees its own two rather than an empty screen and a
-- suggestion to create what it already has.
INSERT INTO money_account
  (tenant_id, company_id, account_id, kind, name, name_ar, currency)
SELECT a.tenant_id, a.company_id, a.id,
       CASE m.role WHEN 'cash' THEN 'cash'
                   WHEN 'bank' THEN 'bank'
                   WHEN 'card_clearing' THEN 'card_settlement' END,
       a.name,
       -- 0073 put an account's Arabic name in `translations`, not in a column.
       nullif(a.translations->>'ar', ''),
       c.base_currency
FROM account_role_map m
JOIN account a ON a.id = m.account_id
JOIN company c ON c.id = a.company_id
WHERE m.role IN ('cash', 'bank', 'card_clearing')
ON CONFLICT (account_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Moving money between them
-- ---------------------------------------------------------------------------
--
-- C2: "Fund transfers, fully tracked: Cash -> Bank, Bank -> Cash, Cash -> Cash
-- (branch to branch), Bank -> Bank — every transfer creates its own audit entry
-- and a printable transfer voucher."
--
-- Posting rule 9, `transfer.internal`, has been seeded since 0025 and has never
-- once been called. It names both ends from the transaction rather than from
-- the rule, which is exactly right for this and is why it could not be used
-- until something knew what the two ends were.

CREATE TABLE money_transfer (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  transfer_no text NOT NULL,

  from_account_id uuid NOT NULL REFERENCES money_account(id) ON DELETE RESTRICT,
  to_account_id   uuid NOT NULL REFERENCES money_account(id) ON DELETE RESTRICT,

  amount      numeric(18,4) NOT NULL,
  moved_on    date NOT NULL,
  reference   text,
  note        text,

  journal_entry_id uuid REFERENCES journal_entry(id) ON DELETE RESTRICT,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT money_transfer_positive CHECK (amount > 0),
  -- Moving money to where it already is is not a transfer; it is two ledger
  -- lines that cancel and a voucher that says nothing.
  CONSTRAINT money_transfer_goes_somewhere
    CHECK (from_account_id <> to_account_id)
);

CREATE UNIQUE INDEX money_transfer_no_uq ON money_transfer (company_id, transfer_no);
CREATE INDEX money_transfer_tenant_idx ON money_transfer (tenant_id);
CREATE INDEX money_transfer_account_idx
  ON money_transfer (from_account_id, moved_on DESC);

ALTER TABLE money_transfer ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_transfer FORCE  ROW LEVEL SECURITY;
CREATE POLICY money_transfer_isolation ON money_transfer
  USING (tenant_id = current_tenant_id());

-- A transfer that has posted is a record of money that moved. Correcting one
-- means transferring back, which leaves both facts visible — the same rule the
-- invoice, the journal entry, the Z-report and the stock voucher follow.
CREATE TRIGGER money_transfer_no_update
  BEFORE UPDATE OR DELETE ON money_transfer
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE company ADD COLUMN next_transfer_no bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_transfer_voucher_no(p_company_id uuid)
RETURNS text
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  UPDATE company
  SET next_transfer_no = next_transfer_no + 1
  WHERE id = p_company_id
  RETURNING next_transfer_no - 1 INTO claimed;

  IF claimed IS NULL THEN
    RAISE EXCEPTION 'no such company' USING ERRCODE = 'no_data_found';
  END IF;

  RETURN 'TFR-' || to_char(claimed, 'FM000000');
END;
$$;

-- ---------------------------------------------------------------------------
-- The bank statement, and reconciling against it
-- ---------------------------------------------------------------------------
--
-- C11 draws the flow: import, auto-match on amount/date/reference, manual match
-- for the remainder, then reconcile and lock, with an exception report for
-- whatever is left.
--
-- # What is being proved
--
-- "That what the software says is in the bank is actually what the bank says."
-- Which means the interesting output is not the matches — it is the two lists
-- of things that did NOT match, because those are the bank charges nobody
-- keyed, the cheque that never cleared, and the payment somebody recorded
-- twice.

CREATE TABLE bank_statement (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  account_id  uuid NOT NULL REFERENCES money_account(id) ON DELETE RESTRICT,

  -- The period the statement covers, and the balances the BANK states at each
  -- end. These are the figures the reconciliation is against; everything else
  -- on this table is working.
  starts_on   date NOT NULL,
  ends_on     date NOT NULL,
  opening_balance numeric(18,4) NOT NULL,
  closing_balance numeric(18,4) NOT NULL,

  reference   text,

  -- draft      — lines are being imported and matched
  -- reconciled — the difference is nil and it has been signed off
  status      text NOT NULL DEFAULT 'draft',

  reconciled_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  reconciled_at timestamptz,

  imported_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  imported_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT bank_statement_dates_ordered CHECK (ends_on >= starts_on),
  CONSTRAINT bank_statement_status_valid CHECK (status IN ('draft', 'reconciled')),
  CONSTRAINT bank_statement_reconciled_is_signed CHECK (
    status <> 'reconciled'
    OR (reconciled_by IS NOT NULL AND reconciled_at IS NOT NULL))
);

CREATE INDEX bank_statement_account_idx
  ON bank_statement (account_id, ends_on DESC);
CREATE INDEX bank_statement_tenant_idx ON bank_statement (tenant_id);

ALTER TABLE bank_statement ENABLE ROW LEVEL SECURITY;
ALTER TABLE bank_statement FORCE  ROW LEVEL SECURITY;
CREATE POLICY bank_statement_isolation ON bank_statement
  USING (tenant_id = current_tenant_id());

CREATE TABLE bank_statement_line (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  statement_id uuid NOT NULL REFERENCES bank_statement(id) ON DELETE CASCADE,

  -- The line as the BANK stated it, and never edited afterwards. A
  -- reconciliation whose evidence can be adjusted to fit is not a
  -- reconciliation.
  value_date   date NOT NULL,
  description  text NOT NULL,
  reference    text,

  -- Signed, from the bank's point of view: positive is money arriving in the
  -- account. Stored as one signed figure rather than a debit and a credit
  -- column because a statement line is only ever one or the other, and two
  -- columns invite a row that is both.
  amount       numeric(18,4) NOT NULL,

  -- The ledger line this was matched to, if any. NULL is the interesting
  -- state: it means the bank saw something the books do not have.
  journal_line_id uuid REFERENCES journal_line(id) ON DELETE RESTRICT,
  matched_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  matched_at   timestamptz,

  -- How it came to be matched, so an auditor can tell an automatic match from
  -- a person's judgement. C11's auto-match is a convenience; a human match is
  -- a decision.
  match_kind   text,

  CONSTRAINT bank_statement_line_non_zero CHECK (amount <> 0),
  CONSTRAINT bank_statement_line_match_kind_valid CHECK (
    match_kind IS NULL OR match_kind IN ('automatic', 'manual')),
  CONSTRAINT bank_statement_line_match_is_complete CHECK (
    (journal_line_id IS NULL AND match_kind IS NULL AND matched_at IS NULL)
    OR (journal_line_id IS NOT NULL AND match_kind IS NOT NULL
        AND matched_at IS NOT NULL))
);

CREATE INDEX bank_statement_line_statement_idx
  ON bank_statement_line (statement_id, value_date);
-- One ledger line cannot answer for two statement lines: a duplicated payment
-- in the books would otherwise reconcile against a single bank debit and the
-- duplicate would disappear, which is the exact error this whole module exists
-- to surface.
CREATE UNIQUE INDEX bank_statement_line_one_match_per_ledger_line
  ON bank_statement_line (journal_line_id)
  WHERE journal_line_id IS NOT NULL;
CREATE INDEX bank_statement_line_tenant_idx ON bank_statement_line (tenant_id);

ALTER TABLE bank_statement_line ENABLE ROW LEVEL SECURITY;
ALTER TABLE bank_statement_line FORCE  ROW LEVEL SECURITY;
CREATE POLICY bank_statement_line_isolation ON bank_statement_line
  USING (tenant_id = current_tenant_id());

-- A reconciled statement is frozen. Its lines are the evidence.
CREATE OR REPLACE FUNCTION reject_reconciled_statement_change()
RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  st text;
BEGIN
  SELECT status INTO st FROM bank_statement
  WHERE id = coalesce(NEW.statement_id, OLD.statement_id);

  IF st = 'reconciled' THEN
    RAISE EXCEPTION
      'A reconciled statement cannot be changed. Reopen it first, which is recorded.'
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN coalesce(NEW, OLD);
END;
$$;

CREATE TRIGGER bank_statement_line_frozen_once_reconciled
  BEFORE INSERT OR UPDATE OR DELETE ON bank_statement_line
  FOR EACH ROW EXECUTE FUNCTION reject_reconciled_statement_change();

-- ---------------------------------------------------------------------------
-- The permission
-- ---------------------------------------------------------------------------
--
-- `accounting.create` already covers posting a journal entry, and a transfer is
-- one. Reconciliation is a different act: it asserts that the books agree with
-- an outside party, which is the assertion an auditor relies on, so it gets its
-- own verb.
--
-- Owner and Accountant. Not the Store Manager, whose role 0005 describes as
-- unable to "see bank ledgers or true net profit" — a person who cannot see the
-- bank ledger cannot reconcile it.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',      'accounting.reconcile'),
  ('accountant', 'accounting.reconcile'),
  ('owner',      'accounting.manage_accounts'),
  ('accountant', 'accounting.manage_accounts'),
  ('auditor',    'accounting.reconcile')
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
      ('owner',      'accounting.reconcile'),
      ('accountant', 'accounting.reconcile'),
      ('owner',      'accounting.manage_accounts'),
      ('accountant', 'accounting.manage_accounts'),
      ('auditor',    'accounting.reconcile')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
