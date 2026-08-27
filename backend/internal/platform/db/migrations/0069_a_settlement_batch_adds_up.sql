-- 0069 — A settlement batch's header was never checked against its own lines.
--
-- 0057 put the comment on gross_amount that "the arithmetic can be checked
-- against the link table rather than trusted", and then nothing checked it.
-- Three figures are written by three separate statements inside Record —
-- the batch header, the link rows, and the per-tender fee shares on
-- sales_tender — and until now the only thing tying them together was that the
-- same Go function wrote all three.
--
-- # Why a constraint rather than a test
--
-- Two invariants matter, and each has already been broken once by a change that
-- looked obviously correct.
--
--   gross_amount = sum of the tenders in the batch
--
--     This is what the journal entry is posted for. If a tender is dropped from
--     the link table the clearing account is credited for money that is still
--     recorded as pending, and the account never reaches zero — the one thing
--     design 02 §8 says it must do.
--
--   fee_amount = sum of the fee shares on those tenders
--
--     The fee posts as one figure, so the ledger stays right whatever the
--     shares say, and the error surfaces only in a margin-by-payment-method
--     report that nobody reconciles daily. The per-tender cap that stops a
--     share exceeding its own tender passes the excess it withholds to the NEXT
--     tender; on the last tender there is no next one, and the batch quietly
--     allocated less than the acquirer charged.
--
-- Deferred to commit, like assert_invoice_balanced and assert_entry_balanced,
-- because the rows genuinely arrive one at a time and a batch is legitimately
-- unbalanced in between.

CREATE OR REPLACE FUNCTION assert_settlement_batch_adds_up() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  batch      record;
  line_gross numeric(18,4);
  line_count integer;
  share_sum  numeric(18,4);
BEGIN
  SELECT * INTO batch FROM settlement_batch WHERE id = NEW.batch_id;
  IF batch IS NULL THEN
    RETURN NULL;   -- rolled back within this transaction
  END IF;

  SELECT coalesce(sum(l.amount), 0), count(*)
  INTO line_gross, line_count
  FROM settlement_batch_tender l WHERE l.batch_id = NEW.batch_id;

  IF line_count = 0 THEN
    RAISE EXCEPTION
      'A deposit must say which payments it covered.' USING ERRCODE = 'P0001';
  END IF;

  IF batch.gross_amount <> line_gross THEN
    RAISE EXCEPTION
      'This deposit is recorded as covering % of payments and the payments in '
      'it add up to %. The clearing account would be credited for money that '
      'is still recorded as outstanding.',
      batch.gross_amount, line_gross
      USING ERRCODE = 'P0001';
  END IF;

  -- The fee shares, read from the tenders themselves rather than from anything
  -- the caller passed alongside them.
  SELECT coalesce(sum(coalesce(t.fee_amount, 0)), 0) INTO share_sum
  FROM settlement_batch_tender l
  JOIN sales_tender t ON t.id = l.tender_id
  WHERE l.batch_id = NEW.batch_id;

  IF batch.fee_amount <> share_sum THEN
    RAISE EXCEPTION
      'The acquirer charged % on this deposit and the payments in it carry % '
      'of fee between them. Every sale''s card cost has to add back up to the '
      'figure on the bank statement.',
      batch.fee_amount, share_sum
      USING ERRCODE = 'P0001';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER settlement_batch_tender_adds_up
  AFTER INSERT ON settlement_batch_tender
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_settlement_batch_adds_up();
