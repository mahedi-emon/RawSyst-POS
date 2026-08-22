-- 0057 — Clear the card clearing account (P15, design 02 §8, blueprint C12).
--
-- The blueprint states the problem plainly: "A customer pays SAR 1,000 by card,
-- but the bank deposits only SAR 985 two days later. Without this module the
-- books never balance and the Owner never knows their real card cost."
--
-- Half of it has been built since 0018. A card sale debits Card Settlement
-- Clearing for the gross, `sales_tender` carries `fee_amount` and a
-- `settlement_status` with its four states, and there is an index on the
-- pending ones. Nothing ever moved a tender out of `pending`. So the clearing
-- account only ever grew: money the shop had taken, could see on the balance
-- sheet, and could not spend, with no way to match it against what the acquirer
-- actually deposited. An owner asking "where is my money" got an answer that
-- was correct on the day of the sale and wronger every day after.
--
-- # The fee is a fact, not a calculation
--
-- This deliberately does NOT compute the fee from a configured rate. The
-- acquirer deposits a number; the fee is the difference between what was taken
-- and what arrived. Deriving it from a rate would invent a figure and then
-- disagree with the bank statement whenever the contract, the scheme mix or a
-- mid-month rate change said otherwise — and the disagreement would land in the
-- one account that is supposed to reconcile to zero.
--
-- So a batch records what the bank did: these tenders, this deposit, on this
-- date. Gross comes from the tenders, net comes from the statement, and the fee
-- is what is left. Per-tender fee CONFIGURATION, which design 02 §8 also
-- mentions, is a forecasting feature for margin-per-method and is a separate
-- thing from this; it is not needed to make the books true and is not here.
--
-- # The posting
--
--   Dr  Bank                     985   what actually arrived
--   Dr  Bank & Card Charges       15   what it cost to receive it
--       Cr  Card Clearing              1,000  what was taken at the counter
--
-- Exactly design 02 §8. The credit is the gross, which is what the sale
-- debited, so the clearing account returns to zero for those tenders rather
-- than to a residue nobody can explain.
--
-- # Chargebacks and reversals are not here
--
-- Design 02 §8: they "post as their own accounting events, never as edits".
-- `settlement_status` already carries a `chargeback` state for when that module
-- arrives. Nothing in this migration writes it, and nothing here edits a
-- settled tender.

-- ---------------------------------------------------------------------------
-- Rule 12 — payment settlement
-- ---------------------------------------------------------------------------
--
-- Three fixed lines rather than a for_each group over the tenders. The tenders
-- in a batch all clear through the same role — `card_clearing`, which is what
-- sales.accountRoleFor returns for every card, wallet and BNPL scheme — so one
-- credit for the gross is both correct and readable. Design 12 §1 does describe
-- a separate clearing account per scheme; when the chart grows those, this rule
-- becomes a for_each over a group and the engine already supports it.

INSERT INTO posting_rule
  (rule_key, country, version, lines, description, effective_from)
VALUES
('payment.settlement', NULL, 1,
 '[{"role": "bank",               "side": "debit",  "amount": "net"},
   {"role": "bank_card_charges",  "side": "debit",  "amount": "fee"},
   {"role": "card_clearing",      "side": "credit", "amount": "gross"}]'::jsonb,
 'An acquirer deposited the takings for a batch of card payments, less its fee.',
 '2020-01-01')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The batch
-- ---------------------------------------------------------------------------

CREATE TABLE settlement_batch (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- Client-assigned, like every other document in this system, so recording a
  -- batch twice because a response was lost gives back the first one rather
  -- than clearing the same tenders into two journal entries.
  uuid        uuid NOT NULL,

  -- What the bank statement calls this deposit. The whole point of the module
  -- is being able to point at a line on a statement and say which sales it is,
  -- so this is required rather than a note.
  reference   text NOT NULL,

  -- The date the money landed, which is the date it posts on. Not the date
  -- somebody got round to entering it: a deposit keyed a week late belongs in
  -- the period it arrived in, and a fiscal period that has since closed will
  -- refuse it, which is the correct outcome rather than a silent reassignment.
  deposited_on date NOT NULL,

  -- Gross is the sum of the tenders in the batch, held here as well so the row
  -- is readable on its own and so the arithmetic can be checked against the
  -- link table rather than trusted.
  gross_amount numeric(18,4) NOT NULL,
  fee_amount   numeric(18,4) NOT NULL,
  net_amount   numeric(18,4) NOT NULL,

  -- The entry this deposit posted. Carried on the row for the same reason
  -- customer_receipt carries one: an accountant looking at a deposit should be
  -- able to reach its journal entry without searching by source id.
  journal_entry_id uuid REFERENCES journal_entry(id),

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT settlement_batch_uuid_uq UNIQUE (company_id, uuid),

  -- A deposit of nothing is not a deposit. A negative one is a clawback, which
  -- design 02 §8 says is its own accounting event.
  CONSTRAINT settlement_batch_gross_positive CHECK (gross_amount > 0),
  CONSTRAINT settlement_batch_net_positive   CHECK (net_amount > 0),

  -- The fee cannot be negative: an acquirer paying MORE than was taken is not a
  -- fee, it is a different event, and absorbing it here would hide it in an
  -- expense account as a negative charge.
  CONSTRAINT settlement_batch_fee_non_negative CHECK (fee_amount >= 0),

  -- The identity the posting depends on. If these three ever disagreed the
  -- journal entry would not balance, and the deferred balance trigger would
  -- refuse it a long way from the cause.
  CONSTRAINT settlement_batch_adds_up CHECK (gross_amount = net_amount + fee_amount)
);

ALTER TABLE settlement_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE settlement_batch FORCE  ROW LEVEL SECURITY;

CREATE POLICY settlement_batch_isolation ON settlement_batch
  USING (tenant_id = current_tenant_id());

CREATE INDEX settlement_batch_company_idx ON settlement_batch (company_id, deposited_on);

-- Posted history is never edited or removed.
CREATE TRIGGER settlement_batch_immutable
  BEFORE DELETE ON settlement_batch
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

-- ---------------------------------------------------------------------------
-- Which payments the deposit covered
-- ---------------------------------------------------------------------------
--
-- The many-to-one link design 02 §8 asks for, and QA gate M4's reason for
-- wanting it: "the batch reconciles back to individual sales" should be a
-- query, not a forensic exercise.

CREATE TABLE settlement_batch_tender (
  tenant_id  uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  batch_id   uuid NOT NULL REFERENCES settlement_batch(id) ON DELETE CASCADE,
  tender_id  uuid NOT NULL REFERENCES sales_tender(id),

  -- What this individual payment contributed, so a batch can be taken apart
  -- again without re-reading every invoice.
  amount     numeric(18,4) NOT NULL,

  -- One tender settles exactly once. Without this a payment could be included
  -- in two deposits and the clearing account would go negative — the one thing
  -- this module exists to prevent.
  PRIMARY KEY (tender_id),

  CONSTRAINT settlement_batch_tender_amount_positive CHECK (amount > 0)
);

ALTER TABLE settlement_batch_tender ENABLE ROW LEVEL SECURITY;
ALTER TABLE settlement_batch_tender FORCE  ROW LEVEL SECURITY;

CREATE POLICY settlement_batch_tender_isolation ON settlement_batch_tender
  USING (tenant_id = current_tenant_id());

CREATE INDEX settlement_batch_tender_batch_idx ON settlement_batch_tender (batch_id);

CREATE TRIGGER settlement_batch_tender_immutable
  BEFORE DELETE ON settlement_batch_tender
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

COMMENT ON TABLE settlement_batch IS
  'One deposit an acquirer made against a set of card payments (C12).';
COMMENT ON TABLE settlement_batch_tender IS
  'Which individual payments a deposit covered, so a batch reconciles to sales.';

-- ---------------------------------------------------------------------------
-- 5300 Bank & Card Charges, for every company that already has a chart
-- ---------------------------------------------------------------------------
--
-- Added to the seeded chart in Go AND mapped for existing companies here, in
-- the same change. That pairing is the lesson of 0048 and 0052: a rule naming a
-- role no chart maps is not a half-built feature, it is a rule that throws — and
-- the test that covers the rule maps the role by hand and never sees it.
--
-- Per tenant, because `account` and `account_role_map` are FORCE ROW LEVEL
-- SECURITY with a `tenant_id = current_tenant_id()` predicate: a bare INSERT in
-- a migration matches nothing and reports success, which is how 0037 granted no
-- permissions at all while appearing to work.

DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    -- Only companies that already have a chart. One that has never been
    -- through provisioning gets 5300 from the seed when it is, and inventing a
    -- lone account here would leave it with a chart of one.
    INSERT INTO account (tenant_id, company_id, code, name, type, is_control)
    SELECT t.id, c.id, '5300', 'Bank & Card Charges', 'expense', false
    FROM company c
    WHERE c.tenant_id = t.id
      AND EXISTS (SELECT 1 FROM account a WHERE a.company_id = c.id)
    ON CONFLICT (company_id, code) DO NOTHING;

    -- A company that has already mapped this role keeps its mapping.
    -- Repointing a role that carries a balance would split that balance
    -- mid-year, which is the trap 0048 had to write around.
    INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
    SELECT t.id, a.company_id, 'bank_card_charges', a.id
    FROM account a
    WHERE a.tenant_id = t.id
      AND a.code = '5300'
      AND NOT EXISTS (
        SELECT 1 FROM account_role_map m
        WHERE m.company_id = a.company_id
          AND m.role = 'bank_card_charges'
      )
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', '', true);
END $$;
