-- 0120 — imported tax rates could never be verified.
--
-- 0118 loaded CDTFA's 541 Californian locations, correctly unmarked: the
-- figures are the authority's but the conversion is ours, and the product
-- refuses an unverified rate rather than charging it.
--
-- There was then no way to ever verify them. `verified_on` appears in the
-- registry service only on INSERT paths — `RecordJurisdictionRate` writes a new
-- rate, `ImportRates` writes a batch — and neither can stamp a row that already
-- exists. Re-importing the same schedule does nothing at all: the supersession
-- UPDATE only closes rows starting BEFORE the new date, and the insert that
-- follows hits the no-overlap constraint and is swallowed by ON CONFLICT DO
-- NOTHING. So the shipped schedule was permanently stuck at "imported", and
-- every Californian shop was permanently unable to trade.
--
-- This is the same shape as the payroll run that could never be cancelled and
-- the tenant limit that could never be raised: a state the product enforces
-- everywhere, with no path for anyone to reach it.
--
-- # Four states, two people
--
--   imported  — the rows exist and nothing is stamped
--   reviewed  — somebody has checked them against the authority's publication
--   verified  — a SECOND person has signed them off for production use
--   active    — verified, in force on the date being priced, not superseded
--
-- Only the first three are stored. "Active" is a question about a date and is
-- answered by resolution, not by a column that would have to be maintained.
--
-- The two-person rule is the point of separating review from verification. One
-- person mistyping a decimal in a tax rate charges every customer of every shop
-- in that jurisdiction the wrong amount, and the shop remits the wrong amount
-- to the state. A second pair of eyes is the cheapest possible control on that,
-- and it is enforced in the service rather than here because it is a statement
-- about people, not about rows.

ALTER TABLE tax_jurisdiction_rate
  -- Who loaded it. Null for the rows a migration seeded, which nobody imported.
  ADD COLUMN imported_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  ADD COLUMN reviewed_on date,
  ADD COLUMN reviewed_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  -- What the reviewer says they checked, and against what. A review with no
  -- statement is a click.
  ADD COLUMN review_note text;

ALTER TABLE tax_jurisdiction_rate
  ADD CONSTRAINT tax_rate_review_coherent
  CHECK ((reviewed_on IS NULL) = (reviewed_by IS NULL));

ALTER TABLE tax_jurisdiction_rate
  ADD CONSTRAINT tax_rate_review_says_what
  CHECK (reviewed_on IS NULL OR btrim(coalesce(review_note, '')) <> '');

-- The figure cannot change under a verification.
--
-- Stamping a rate verified is an UPDATE, and without this nothing stopped that
-- UPDATE from also moving the rate, its dates or its provenance — which would
-- let "verification" silently alter the number being verified, and would let a
-- new import rewrite a historical rate that invoices were issued against.
--
-- `effective_to` stays mutable on purpose: closing a row is how a later
-- schedule supersedes an earlier one, and that is the supported correction.
-- The stamps and the note are mutable because writing them is the whole point.
CREATE TRIGGER tax_jurisdiction_rate_frozen_fields
  BEFORE UPDATE ON tax_jurisdiction_rate
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'jurisdiction_id', 'treatment', 'rate', 'effective_from',
    'source_authority', 'source_document');

-- What an operator opens the screen to find.
CREATE INDEX tax_jurisdiction_rate_unverified_idx
  ON tax_jurisdiction_rate (verified_on) WHERE verified_on IS NULL;

COMMENT ON COLUMN tax_jurisdiction_rate.reviewed_by IS
  'Who checked these figures against the authority''s publication. The person '
  'who verifies must not be this person: two people look at a tax rate before '
  'a customer is charged it.';
