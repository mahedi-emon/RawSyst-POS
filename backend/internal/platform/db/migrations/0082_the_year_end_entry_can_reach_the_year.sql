-- 0082 — The one entry a closed period must accept.
--
-- C10 asks for a year-end routine that closes revenue and expense into Retained
-- Earnings, and it asks for a closed period to accept nothing. Both are right,
-- and taken literally together they make the routine impossible: its entry is
-- dated on the last day of the year, that day is in December, and December has
-- to be closed before the figures it draws on are final.
--
-- The first attempt at the routine hit exactly that and was refused by
-- `assert_period_open` with "Period 12 of 2026 is closed and cannot accept new
-- entries" — which is the trigger doing its job.
--
-- # The three ways out, and why this one
--
-- Reopen December, post, close it again. Rejected: it puts a reopen in the
-- audit trail that nobody performed and that C10 says needs a written reason,
-- so the routine would have to invent one.
--
-- Post before closing December. Rejected: the figures would not be final. A
-- sale keyed on the last afternoon of the year would land after the closing
-- entry had already emptied the revenue account, and the year's profit would be
-- wrong by that sale with nothing saying so.
--
-- Let the closing entry itself through. Taken. The year-end entry is not a
-- transaction IN the period — it is part of the act of closing it, which is why
-- every accounting system has some form of this exemption, usually as a
-- thirteenth adjustment period.
--
-- # How narrow it is
--
-- Only `source_type = 'year_end_close'`, only into a period that is CLOSED and
-- not locked, and the date must still fall inside the period as before. A
-- locked year stays shut to everything, which is what makes locking mean
-- something: it is the state a year reaches once its closing entry has been
-- posted, and there is no second one.
--
-- `source_type` is written by the application rather than by the caller — no
-- route accepts it — so this cannot be reached by a request that names itself
-- a year-end close.

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

  -- The year-end entry, and nothing else, may reach a closed period. See the
  -- note at the top of 0082: it is not a transaction in the period, it is the
  -- act of closing it.
  IF s = 'closed' AND NEW.source_type = 'year_end_close' THEN
    NULL;
  ELSIF s <> 'open' THEN
    RAISE EXCEPTION
      'Period % of % is % and cannot accept new entries. Post this to an open period, or ask an owner to reopen it with a reason.',
      p, y, s
      USING ERRCODE = 'P0001';
  END IF;

  -- An entry dated outside its own period would appear in the wrong month's
  -- statements while counting toward a different period's close. This holds for
  -- the year-end entry too, which is dated on the last day of the year and so
  -- falls inside the December it closes.
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

COMMENT ON FUNCTION assert_period_open() IS
  'A closed period accepts nothing except the year-end closing entry, which is '
  'part of closing it rather than a transaction in it. A LOCKED period accepts '
  'nothing at all.';
