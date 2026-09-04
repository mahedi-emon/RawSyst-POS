-- 0121 — journal entries written by hand (blueprint C10).
--
-- C10 asks for "accounting adjustments / manual journal entries —
-- permission-gated, reason-required, fully audit-logged", and the permission
-- has existed since 0101: `accounting.create`, described there as "Write a
-- journal entry by hand" and held by the Owner and the Accountant. It guarded
-- transfers and settlement batches and nothing that actually wrote a journal by
-- hand, so the verb was accurate and the thing it named did not exist.
--
-- The consequence was narrow and real. Every other entry in this ledger is
-- posted by a rule from a document — a sale, a bill, a payroll run — which is
-- the safer design and the reason it was built that way. But C10's year-end
-- routine says "post adjusting entries" before closing, and an accountant who
-- needed to accrue a bill that had not arrived, write off a balance, or correct
-- a misposting had nowhere to put it.
--
-- # The lines live in the ledger, not here
--
-- A manual journal is a REASON attached to an ordinary entry. The debits and
-- credits go in `journal_line` like every other entry's, so the trial balance,
-- the statements, the period lock and the tie-out see them without knowing they
-- were typed rather than derived. A second line table would be a second ledger.
--
-- What this table adds is what the ledger does not carry: who asked for it, why,
-- and under which number.
--
-- # Reason is not a memo
--
-- `journal_entry.memo` is a description and is optional. C10 wants a reason and
-- means it, so this column is NOT NULL with a non-empty check. An adjustment
-- with no stated reason is the one an auditor asks about, and "correction" is
-- not an answer somebody can act on a year later.

ALTER TABLE company ADD COLUMN next_journal_no bigint NOT NULL DEFAULT 1;

CREATE OR REPLACE FUNCTION claim_journal_no(p_company_id uuid)
RETURNS text
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  UPDATE company
  SET next_journal_no = next_journal_no + 1
  WHERE id = p_company_id
  RETURNING next_journal_no - 1 INTO claimed;

  IF claimed IS NULL THEN
    RAISE EXCEPTION 'no such company' USING ERRCODE = 'no_data_found';
  END IF;

  RETURN 'JV-' || to_char(claimed, 'FM000000');
END;
$$;

CREATE TABLE manual_journal (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- The client's own identifier for this journal, so a retry that lost its
  -- response finds the journal it already posted instead of posting a second
  -- one. The same device every other money path in this product uses.
  uuid        uuid NOT NULL,

  journal_no  text NOT NULL,
  entry_date  date NOT NULL,

  -- Required, and the point of the table. See the header.
  reason      text NOT NULL,
  memo        text,

  -- The entry this journal produced. NOT NULL because a manual journal that
  -- posted nothing is not a journal; the row and the entry are written in one
  -- transaction or neither is.
  journal_entry_id uuid NOT NULL REFERENCES journal_entry(id) ON DELETE RESTRICT,

  -- Set on a journal that exists to reverse another, pointing at the original.
  -- Corrections happen by posting the opposite, never by editing what posted.
  reverses_id uuid REFERENCES manual_journal(id) ON DELETE RESTRICT,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT manual_journal_reason_present
    CHECK (btrim(reason) <> ''),
  -- A journal cannot reverse itself.
  CONSTRAINT manual_journal_not_its_own_reversal
    CHECK (reverses_id IS NULL OR reverses_id <> id)
);

CREATE UNIQUE INDEX manual_journal_no_uq
  ON manual_journal (company_id, journal_no);

-- The idempotency key. A retry carries the same uuid and finds the journal it
-- already wrote.
CREATE UNIQUE INDEX manual_journal_uuid_uq
  ON manual_journal (company_id, uuid);

-- One reversal per journal. Two would credit back twice what was debited once.
CREATE UNIQUE INDEX manual_journal_one_reversal_uq
  ON manual_journal (reverses_id) WHERE reverses_id IS NOT NULL;

CREATE INDEX manual_journal_tenant_idx ON manual_journal (tenant_id);
CREATE INDEX manual_journal_company_date_idx
  ON manual_journal (company_id, entry_date DESC);

ALTER TABLE manual_journal ENABLE ROW LEVEL SECURITY;
ALTER TABLE manual_journal FORCE  ROW LEVEL SECURITY;
CREATE POLICY manual_journal_isolation ON manual_journal
  USING (tenant_id = current_tenant_id());

-- Posted history is not editable. A wrong journal is corrected by reversing it,
-- which is the same rule design 02 §2 puts on every other entry — and the
-- reason the reversal carries `reverses_id` rather than the original being
-- rewritten.
CREATE TRIGGER manual_journal_no_delete
  BEFORE DELETE ON manual_journal
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

CREATE TRIGGER manual_journal_frozen
  BEFORE UPDATE ON manual_journal
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'tenant_id', 'company_id', 'uuid', 'journal_no', 'entry_date',
    'reason', 'journal_entry_id', 'reverses_id', 'created_by');

COMMENT ON COLUMN manual_journal.reason IS
  'Why this adjustment was made. Required by blueprint C10 and by the auditor '
  'who reads it a year later; "correction" is not an answer somebody can act '
  'on.';
