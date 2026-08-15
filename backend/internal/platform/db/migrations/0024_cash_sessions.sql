-- 0024 — Cash sessions, and the X/Z reports drawn from them.
--
-- A shop cannot trade without reconciling its till. Cash is the only tender
-- that leaves no trace of its own: a card sale is corroborated by the acquirer
-- and a transfer by the bank, but a banknote that never reaches the drawer is
-- invisible unless the drawer is counted against what the system expected.
-- Retail theft concentrates here for exactly that reason.
--
-- # Expected cash is derived, never stored
--
-- The expected balance is computed from the session's own movements — the
-- opening float, cash tenders, cash refunds, and any pay-in or pay-out. A
-- stored running total would drift the moment one contributing row was written
-- outside the code that maintained it, and the drift would be indistinguishable
-- from theft. Deriving it means the number always reflects what actually
-- happened.
--
-- # X and Z are different acts
--
-- An X report is a snapshot: read-only, repeatable, taken mid-shift by a
-- supervisor who wants to know where things stand. A Z report CLOSES the
-- session and may happen exactly once — it is the till's end-of-day
-- declaration, and a second one would either double-count the takings or
-- silently overwrite the first count.

CREATE TABLE cash_session (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  store_id    uuid NOT NULL REFERENCES store(id)   ON DELETE RESTRICT,
  device_id   uuid NOT NULL REFERENCES device(id)  ON DELETE RESTRICT,

  -- Sequential per device, so a cashier can refer to "till 2, session 47"
  -- without quoting a UUID.
  session_no  bigint NOT NULL,

  opened_by   uuid NOT NULL REFERENCES app_user(id) ON DELETE RESTRICT,
  opened_at   timestamptz NOT NULL DEFAULT now(),

  -- The float counted into the drawer at open. Declared, not assumed: a till
  -- that starts with an unverified float cannot produce a meaningful variance
  -- at close.
  opening_float numeric(18,4) NOT NULL,

  closed_by   uuid REFERENCES app_user(id) ON DELETE RESTRICT,
  closed_at   timestamptz,

  -- What the cashier physically counted. NULL until the Z report.
  counted_cash numeric(18,4),

  -- What the system expected, frozen at close. This one IS stored, because it
  -- is the figure the variance was calculated against and a later correction
  -- to history must not silently change what was reconciled on the night.
  expected_cash numeric(18,4),

  -- counted − expected. Positive is a surplus, negative a shortfall.
  variance     numeric(18,4),

  -- Blueprint B7: a blind close hides the expected figure until the count is
  -- committed, so a cashier cannot make the drawer agree with the screen.
  blind_close  boolean NOT NULL DEFAULT true,

  note         text,
  state        text NOT NULL DEFAULT 'open',

  CONSTRAINT cash_session_state_valid CHECK (state IN ('open', 'closed')),
  CONSTRAINT cash_session_float_non_negative CHECK (opening_float >= 0),

  -- A closed session must carry its whole reckoning; an open one must carry
  -- none of it. Half-closed is not a state that means anything.
  CONSTRAINT cash_session_close_is_complete CHECK (
    (state = 'open'
      AND closed_at IS NULL AND closed_by IS NULL
      AND counted_cash IS NULL AND expected_cash IS NULL AND variance IS NULL)
    OR
    (state = 'closed'
      AND closed_at IS NOT NULL AND closed_by IS NOT NULL
      AND counted_cash IS NOT NULL AND expected_cash IS NOT NULL
      AND variance IS NOT NULL)
  ),

  CONSTRAINT cash_session_variance_is_derived CHECK (
    variance IS NULL OR variance = counted_cash - expected_cash)
);

CREATE UNIQUE INDEX cash_session_no_uq ON cash_session (device_id, session_no);
CREATE INDEX cash_session_tenant_idx ON cash_session (tenant_id);
CREATE INDEX cash_session_store_idx  ON cash_session (store_id, opened_at DESC);

-- One open session per till. Two would make "what is in this drawer" have two
-- answers, and every sale would have to guess which one it belonged to.
CREATE UNIQUE INDEX cash_session_one_open_per_device
  ON cash_session (device_id) WHERE state = 'open';

-- A closed session is a declaration someone signed. Correcting it means a new
-- session and an adjustment, exactly as correcting a journal entry means a
-- reversal.
CREATE OR REPLACE FUNCTION reject_closed_session_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.state = 'closed' THEN
    RAISE EXCEPTION
      'This till session was closed on % and cannot be changed. Record a cash '
      'adjustment against the current session instead.',
      to_char(OLD.closed_at, 'DD Mon YYYY at HH24:MI')
      USING ERRCODE = 'raise_exception';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER cash_session_closed_is_final
  BEFORE UPDATE ON cash_session
  FOR EACH ROW EXECUTE FUNCTION reject_closed_session_change();

CREATE TRIGGER cash_session_no_delete BEFORE DELETE ON cash_session
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

ALTER TABLE cash_session ENABLE ROW LEVEL SECURITY;
ALTER TABLE cash_session FORCE  ROW LEVEL SECURITY;
CREATE POLICY cash_session_isolation ON cash_session
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Cash in and out, other than sales
-- ---------------------------------------------------------------------------
--
-- A drawer is not only fed by sales. A supplier paid in cash, a float top-up, a
-- safe drop mid-shift — each moves money and each must appear in the expected
-- balance, or the variance blames the cashier for a movement someone else
-- authorised.
CREATE TABLE cash_movement (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  session_id  uuid NOT NULL REFERENCES cash_session(id) ON DELETE RESTRICT,

  -- Signed. Positive puts money in the drawer, negative takes it out.
  amount      numeric(18,4) NOT NULL,

  reason      text NOT NULL,
  note        text NOT NULL,
  recorded_by uuid NOT NULL REFERENCES app_user(id) ON DELETE RESTRICT,
  recorded_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT cash_movement_non_zero CHECK (amount <> 0),
  CONSTRAINT cash_movement_reason_valid CHECK (reason IN (
    'float_in',      -- topping the drawer up
    'safe_drop',     -- removing cash to the safe mid-shift
    'petty_cash',    -- a small expense paid from the till
    'supplier_paid', -- a delivery paid in cash
    'correction')),
  -- Every movement is explained. An unexplained hand in the till is precisely
  -- what this table exists to make visible.
  CONSTRAINT cash_movement_explained CHECK (length(btrim(note)) >= 3)
);

CREATE INDEX cash_movement_session_idx ON cash_movement (session_id);
CREATE INDEX cash_movement_tenant_idx  ON cash_movement (tenant_id);

CREATE TRIGGER cash_movement_immutable
  BEFORE UPDATE OR DELETE ON cash_movement
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE cash_movement ENABLE ROW LEVEL SECURITY;
ALTER TABLE cash_movement FORCE  ROW LEVEL SECURITY;
CREATE POLICY cash_movement_isolation ON cash_movement
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Sales belong to a session
-- ---------------------------------------------------------------------------
--
-- A foreign key rather than a time window. Matching sales to a shift by
-- timestamp looks equivalent and is not: a terminal clock that drifts, a sale
-- rung at 23:59:58 and committed at 00:00:01, or two shifts overlapping during
-- a handover all put takings in the wrong shift — and the cashier who is short
-- has no way to prove it.
ALTER TABLE sales_invoice
  ADD COLUMN cash_session_id uuid REFERENCES cash_session(id) ON DELETE RESTRICT;

CREATE INDEX sales_invoice_session_idx ON sales_invoice (cash_session_id)
  WHERE cash_session_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- The reckoning
-- ---------------------------------------------------------------------------

-- What the drawer should hold right now.
--
-- Only CASH counts. Card and wallet takings never enter the drawer — they are
-- owed by the acquirer and settle days later — so including them would show a
-- shortfall on every shift equal to the day's card sales.
-- Money OUT of the drawer lives in sales_refund, not sales_tender. A credit
-- note has no tender rows at all — nobody paid, the shop paid back — so a
-- reckoning that read only tenders would count every cash refund as if the
-- money were still in the till, and blame the cashier for the difference.
CREATE OR REPLACE FUNCTION cash_session_cash_in(p_session_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(t.amount), 0)
  FROM sales_tender t
  JOIN sales_invoice i ON i.id = t.invoice_id
  WHERE i.cash_session_id = p_session_id
    AND i.doc_type <> 'credit_note'
    AND t.method = 'cash'
$$;

CREATE OR REPLACE FUNCTION cash_session_cash_out(p_session_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(sum(r.amount), 0)
  FROM sales_refund r
  JOIN sales_invoice i ON i.id = r.credit_note_id
  WHERE i.cash_session_id = p_session_id AND r.method = 'cash'
$$;

CREATE OR REPLACE FUNCTION cash_session_expected(p_session_id uuid)
RETURNS numeric
LANGUAGE sql STABLE AS $$
  SELECT coalesce(s.opening_float, 0)
       + cash_session_cash_in(p_session_id)
       - cash_session_cash_out(p_session_id)
       + coalesce((
           SELECT sum(m.amount) FROM cash_movement m
           WHERE m.session_id = p_session_id
         ), 0)
  FROM cash_session s
  WHERE s.id = p_session_id
$$;

-- The X/Z figures, in one query so a mid-shift snapshot and an end-of-day
-- close can never disagree about the same session.
CREATE OR REPLACE FUNCTION cash_session_report(p_session_id uuid)
RETURNS TABLE (
  session_no        bigint,
  state             text,
  opened_at         timestamptz,
  closed_at         timestamptz,
  opening_float     numeric,
  invoice_count     bigint,
  gross_sales       numeric,
  net_sales         numeric,
  tax_total         numeric,
  refund_total      numeric,
  cash_takings      numeric,
  non_cash_takings  numeric,
  cash_movements    numeric,
  expected_cash     numeric,
  counted_cash      numeric,
  variance          numeric
)
LANGUAGE sql STABLE AS $$
  SELECT
    s.session_no,
    s.state,
    s.opened_at,
    s.closed_at,
    s.opening_float,

    coalesce((SELECT count(*) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type <> 'credit_note'), 0),

    -- Sales figures exclude credit notes, which are reported separately as
    -- refunds. Netting them into "sales" hides how much was handed back, and
    -- that ratio is the single most useful number on a Z report.
    coalesce((SELECT sum(i.total_inclusive) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type <> 'credit_note'), 0),
    coalesce((SELECT sum(i.subtotal_net) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type <> 'credit_note'), 0),
    coalesce((SELECT sum(i.tax_total) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type <> 'credit_note'), 0),
    coalesce((SELECT sum(i.total_inclusive) FROM sales_invoice i
              WHERE i.cash_session_id = s.id AND i.doc_type = 'credit_note'), 0),

    cash_session_cash_in(s.id) - cash_session_cash_out(s.id),
    coalesce((SELECT sum(t.amount)
              FROM sales_tender t JOIN sales_invoice i ON i.id = t.invoice_id
              WHERE i.cash_session_id = s.id
                AND i.doc_type <> 'credit_note' AND t.method <> 'cash'), 0)
    - coalesce((SELECT sum(r.amount)
              FROM sales_refund r JOIN sales_invoice i ON i.id = r.credit_note_id
              WHERE i.cash_session_id = s.id AND r.method <> 'cash'), 0),

    coalesce((SELECT sum(m.amount) FROM cash_movement m
              WHERE m.session_id = s.id), 0),

    -- An open session reports what is expected NOW; a closed one reports what
    -- was expected at the moment it was reconciled.
    CASE WHEN s.state = 'closed' THEN s.expected_cash
         ELSE cash_session_expected(s.id) END,
    s.counted_cash,
    s.variance
  FROM cash_session s
  WHERE s.id = p_session_id
$$;

-- Claims the next session number for a till, the same row-locking mechanism
-- egs_unit.last_icv and company.next_entry_no already use.
ALTER TABLE device
  ADD COLUMN next_session_no bigint NOT NULL DEFAULT 1,
  ADD CONSTRAINT device_session_no_positive CHECK (next_session_no >= 1);

CREATE OR REPLACE FUNCTION claim_session_no(p_device_id uuid)
RETURNS bigint
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  UPDATE device
  SET next_session_no = next_session_no + 1
  WHERE id = p_device_id
  RETURNING next_session_no - 1 INTO claimed;

  IF claimed IS NULL THEN
    RAISE EXCEPTION 'no such terminal' USING ERRCODE = 'no_data_found';
  END IF;

  RETURN claimed;
END;
$$;
