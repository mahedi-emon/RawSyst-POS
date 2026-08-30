-- 0080 — A shop that cannot post anything.
--
-- `accounting.resolvePeriod` refuses every posting for a date no fiscal period
-- covers, with this message:
--
--   "No accounting period covers 30 August 2026, so this cannot be posted. Ask
--    an owner to open the period for that date."
--
-- There is no way for an owner to open a period. No route, no service, no
-- screen. The only INSERTs into `fiscal_period` in the whole repository are in
-- test fixtures — the same sentence that was true of `warehouse` before 0078,
-- and with a worse consequence: a warehouse only stopped the SALE, and this
-- stops every journal entry the product can make. A tenant provisioned the real
-- way could not ring up a sale, record an expense, receive a delivery, take a
-- payment or close a till.
--
-- Blueprint C10 describes the states and the year-end routine in detail and
-- assumes the periods exist. Nothing ever made them.
--
-- # Why the calendar is generated rather than asked for
--
-- Twelve monthly periods per year, from the company's own
-- `fiscal_year_start_month` — which has been on the table since 0002 and, like
-- the periods themselves, was never read by anything.
--
-- A wizard step asking a shopkeeper to define twelve accounting periods before
-- they can sell anything would be a wizard step nobody finishes. The periods a
-- calendar year needs are completely determined by the start month, so they are
-- computed.

-- ---------------------------------------------------------------------------
-- open_fiscal_year
-- ---------------------------------------------------------------------------
--
-- Twelve months from the company's fiscal start, labelled by the calendar year
-- the fiscal year BEGINS in. A company whose year starts in April has its
-- 2026 fiscal year running April 2026 to March 2027, which is the convention
-- every tax authority this product serves uses.
--
-- Idempotent. Called at provisioning, by the roll-forward that keeps a shop
-- trading on the first of January, and by an owner opening a year by hand — and
-- two of those three can happen on the same day.

CREATE OR REPLACE FUNCTION open_fiscal_year(p_company_id uuid, p_year integer)
RETURNS integer
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  v_tenant_id  uuid;
  v_start_month smallint;
  v_first      date;
  v_made       integer := 0;
  i            integer;
BEGIN
  SELECT tenant_id, fiscal_year_start_month
  INTO v_tenant_id, v_start_month
  FROM company WHERE id = p_company_id;

  IF v_tenant_id IS NULL THEN
    RAISE EXCEPTION 'no such company' USING ERRCODE = 'no_data_found';
  END IF;

  v_first := make_date(p_year, v_start_month, 1);

  FOR i IN 1..12 LOOP
    INSERT INTO fiscal_period
      (tenant_id, company_id, fiscal_year, period_no, starts_on, ends_on)
    VALUES (
      v_tenant_id, p_company_id, p_year, i,
      (v_first + make_interval(months => i - 1))::date,
      -- The last day of that month, whatever length it is. Adding a month and
      -- subtracting a day rather than naming 28, 30 or 31: February 2028 has
      -- 29 days and a hard-coded 28 would leave a day no period covered, on
      -- which nothing could be posted.
      ((v_first + make_interval(months => i) - interval '1 day'))::date
    )
    ON CONFLICT (company_id, fiscal_year, period_no) DO NOTHING;

    IF FOUND THEN
      v_made := v_made + 1;
    END IF;
  END LOOP;

  RETURN v_made;
END;
$$;

COMMENT ON FUNCTION open_fiscal_year(uuid, integer) IS
  'Creates the twelve monthly periods of one fiscal year, from the company''s '
  'own start month. Idempotent: a year that already exists is left alone.';

-- ---------------------------------------------------------------------------
-- fiscal_year_of
-- ---------------------------------------------------------------------------
--
-- Which fiscal year a date falls in, for a company. A calendar-year company in
-- August 2026 is in 2026; an April-start company in February 2027 is still in
-- its 2026 year.

CREATE OR REPLACE FUNCTION fiscal_year_of(p_company_id uuid, p_on date)
RETURNS integer
LANGUAGE sql STABLE AS $$
  SELECT CASE
           WHEN extract(month FROM p_on)::smallint >= c.fiscal_year_start_month
             THEN extract(year FROM p_on)::integer
           ELSE extract(year FROM p_on)::integer - 1
         END
  FROM company c WHERE c.id = p_company_id
$$;

-- ---------------------------------------------------------------------------
-- Every company alive today
-- ---------------------------------------------------------------------------
--
-- The 0048 lesson again. This year and next, because the alternative is a shop
-- that trades all December and stops at midnight on the thirty-first — the
-- failure would arrive on the one night of the year nobody is watching, and it
-- would present as "the till says it cannot post" with no way to fix it before
-- morning.
--
-- The roll-forward in the worker keeps that one year of headroom from then on.

DO $$
DECLARE
  c record;
  y integer;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR c IN SELECT id, tenant_id FROM company LOOP
    PERFORM set_config('app.tenant_id', c.tenant_id::text, true);
    y := fiscal_year_of(c.id, current_date);
    PERFORM open_fiscal_year(c.id, y);
    PERFORM open_fiscal_year(c.id, y + 1);
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;

-- ---------------------------------------------------------------------------
-- roll_fiscal_calendar_forward
-- ---------------------------------------------------------------------------
--
-- Keeps a year of headroom in front of every company, so no shop discovers on
-- the first of January that it cannot post.
--
-- Called on the worker's maintenance ticker, beside the job and credential
-- pruning. The whole loop is in SQL rather than in Go for one reason: writing
-- to `fiscal_period` needs `app.tenant_id` set to each tenant in turn, because
-- the table is tenant-scoped with FORCE, and doing that from the application
-- would mean the application changing its own row-level-security context in a
-- loop. That is exactly the kind of code that is correct until somebody adds a
-- statement to the middle of it.
--
-- Returns how many periods it created, so the worker can log a number rather
-- than the fact that it ran.

CREATE OR REPLACE FUNCTION roll_fiscal_calendar_forward(p_days_ahead integer DEFAULT 90)
RETURNS integer
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  c      record;
  made   integer := 0;
  ends   date;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR c IN SELECT id, tenant_id FROM company LOOP
    PERFORM set_config('app.tenant_id', c.tenant_id::text, true);

    SELECT max(ends_on) INTO ends
    FROM fiscal_period WHERE company_id = c.id;

    -- No calendar at all, or one running out within the horizon. Both are the
    -- same repair: open the year after the last one there is.
    IF ends IS NULL THEN
      made := made + open_fiscal_year(c.id, fiscal_year_of(c.id, current_date));
      made := made + open_fiscal_year(c.id, fiscal_year_of(c.id, current_date) + 1);
    ELSIF ends < current_date + p_days_ahead THEN
      made := made + open_fiscal_year(c.id, fiscal_year_of(c.id, ends) + 1);
    END IF;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
  RETURN made;
END;
$$;

COMMENT ON FUNCTION roll_fiscal_calendar_forward(integer) IS
  'Opens the next fiscal year for any company whose calendar runs out within '
  'the horizon. Idempotent, and safe to call as often as the worker likes.';

-- ---------------------------------------------------------------------------
-- Closing is an act with a name on it
-- ---------------------------------------------------------------------------
--
-- 0015 gave `fiscal_period` columns for who closed it and who reopened it, and
-- a CHECK requiring a written reason on a reopen. Nothing ever set them,
-- because nothing ever closed a period.
--
-- This adds the half the original could not state as a constraint: a period
-- that is closed must say who closed it. C10 calls a closed period the thing
-- "that makes financial statements trustworthy", and a close with no name
-- against it is not something anybody can be asked to stand behind.

-- NOT VALID, which is not a weakening.
--
-- It binds every insert and every update from here on, which is every close the
-- product will ever perform. What it declines to do is make a claim about rows
-- that already exist — and any closed period in a database today was put there
-- by something other than this product, because nothing in this product could
-- close one. Validating against those rows would mean either failing the
-- migration on data nobody can explain, or inventing a name to satisfy the
-- check, and a fabricated signature is worse than an unsigned one.
ALTER TABLE fiscal_period
  ADD CONSTRAINT fiscal_period_closed_is_signed
  CHECK (state = 'open' OR (closed_at IS NOT NULL AND closed_by IS NOT NULL))
  NOT VALID;

CREATE INDEX fiscal_period_year_idx
  ON fiscal_period (company_id, fiscal_year, period_no);
