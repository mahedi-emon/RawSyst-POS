-- 0068 — A closed till session could still be written to.
--
-- # What was wrong
--
-- 0024 made a closed session final against UPDATE and DELETE, and left both
-- doors that actually matter open: a cash_movement could be inserted against a
-- closed session, and a sales_invoice could name one. Nothing in the schema
-- said otherwise, and the Go checks that did were unlocked reads.
--
-- The race is ordinary, not exotic. Close takes FOR UPDATE, computes
-- cash_session_expected and freezes it in one transaction. A movement or a sale
-- that began before that read 'open' from its own snapshot, waited on the
-- foreign key's own FOR KEY SHARE lock — which does NOT conflict with anything
-- the close was doing until it committed — and then inserted happily into a
-- session that had been closed and reconciled in between.
--
-- The result is a Z report that cannot reconcile. cash_session_report reads the
-- frozen expected_cash for a closed session but recomputes the takings live, so
-- a sale that landed a moment late appears in cash_takings and not in
-- expected_cash. The variance was measured against a figure that no longer
-- describes the session, and it is the cashier who is asked to explain the
-- difference.
--
-- # Two locks and two triggers, which are not the same thing
--
-- The Go side now takes FOR SHARE on the session row before either write, so
-- the close waits for work already in flight and later work sees 'closed' and
-- is refused with a message that says what to do. That is the good outcome and
-- it is what a cashier will actually meet.
--
-- These triggers are the guarantee. A lock protects the code paths that take
-- it; a constraint protects the table. Any future writer — a report, a fixture,
-- an import, a hand-written UPDATE at three in the morning — meets the same
-- rule, and a Z report cannot be quietly falsified after it has been signed.

CREATE OR REPLACE FUNCTION reject_write_to_closed_session() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  target uuid;
  s      record;
BEGIN
  IF TG_TABLE_NAME = 'sales_invoice' THEN
    target := NEW.cash_session_id;
  ELSE
    target := NEW.session_id;
  END IF;

  IF target IS NULL THEN
    RETURN NEW;
  END IF;

  -- Locked, not merely read. Without this the check has exactly the race it
  -- exists to close: a concurrent Z report commits between this SELECT and the
  -- INSERT, and the row is admitted anyway.
  SELECT state, closed_at INTO s
  FROM cash_session WHERE id = target FOR SHARE;

  IF s.state = 'closed' THEN
    RAISE EXCEPTION
      'That till session was closed on % and its Z report has been signed. '
      'Record this against the session that is open now.',
      to_char(s.closed_at, 'DD Mon YYYY at HH24:MI')
      USING ERRCODE = 'raise_exception';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER cash_movement_needs_an_open_session
  BEFORE INSERT ON cash_movement
  FOR EACH ROW EXECUTE FUNCTION reject_write_to_closed_session();

-- INSERT OR UPDATE on the invoice: a sale cannot be created against a closed
-- session, and an existing one cannot be moved into a closed session later,
-- which would falsify a Z report that had already been signed.
CREATE TRIGGER sales_invoice_needs_an_open_session
  BEFORE INSERT OR UPDATE OF cash_session_id ON sales_invoice
  FOR EACH ROW EXECUTE FUNCTION reject_write_to_closed_session();
