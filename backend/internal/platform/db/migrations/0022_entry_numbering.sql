-- 0022 — Journal entry numbering, allocated the same way the ICV is.
--
-- journal_entry.entry_no is UNIQUE per company, but nothing allocated it. The
-- obvious approach — SELECT max(entry_no) + 1 — is wrong under concurrency:
-- two tills committing at the same moment both read the same maximum, both
-- claim the same number, and one loses on the unique index. Under load that is
-- not a rare collision but a routine one, and the sale that loses is a customer
-- standing at a counter.
--
-- A sequence would not do either. Sequences are not transactional, so a rolled
-- back sale would burn a number and leave a permanent gap in the journal. A
-- gap in numbered accounting records is precisely what an auditor asks about.
--
-- So the counter lives on the company row and is claimed by UPDATE ... RETURNING,
-- which takes a row lock for the rest of the transaction. This is the same
-- mechanism egs_unit.last_icv already uses for the ZATCA counter, and for the
-- same reason: the number must be gapless, unique, and safe to claim from many
-- terminals at once.

ALTER TABLE company
  ADD COLUMN next_entry_no bigint NOT NULL DEFAULT 1,
  ADD CONSTRAINT company_entry_no_positive CHECK (next_entry_no >= 1);

-- Existing books keep their numbering. A company that has already posted
-- entries must not restart at 1 and collide with its own history.
UPDATE company c
SET next_entry_no = coalesce((
  SELECT max(e.entry_no) + 1 FROM journal_entry e WHERE e.company_id = c.id
), 1);

-- Claims the next number for one company.
--
-- SECURITY INVOKER, so row-level security still applies: a caller cannot number
-- an entry for a company in another tenant, because the UPDATE finds no row.
CREATE OR REPLACE FUNCTION claim_entry_no(p_company_id uuid)
RETURNS bigint
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  UPDATE company
  SET next_entry_no = next_entry_no + 1
  WHERE id = p_company_id
  RETURNING next_entry_no - 1 INTO claimed;

  IF claimed IS NULL THEN
    RAISE EXCEPTION 'no such company'
      USING ERRCODE = 'no_data_found';
  END IF;

  RETURN claimed;
END;
$$;
