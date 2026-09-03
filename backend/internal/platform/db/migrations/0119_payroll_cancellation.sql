-- 0119 — a payroll run that was wrong can be put right.
--
-- 0091 modelled four states for a run — draft, approved, paid, cancelled — and
-- built the month's uniqueness around the fourth:
--
--   CREATE UNIQUE INDEX payroll_run_period_uq ON payroll_run (company_id, period)
--     WHERE status <> 'cancelled';
--
-- so that a cancelled run releases its month and a corrected one can take its
-- place. Nothing in the product could ever set that status. Approve posts two
-- journal entries and Pay posts a third, and from there the run was final: a
-- month approved with the wrong attendance, the wrong advance recovery or the
-- wrong GOSI band stayed wrong, the ledger kept the entries, and the month
-- could never be run again because the index still counted the bad run.
--
-- This adds what the state needed to be reachable: who cancelled it, when, and
-- why. The reversal itself is people.Cancel, which flips the entries that were
-- actually posted rather than deleting them — a posted month is a fact, and
-- correcting it is a second fact, not an erasure of the first.

ALTER TABLE payroll_run
  ADD COLUMN cancelled_at  timestamptz,
  ADD COLUMN cancelled_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  ADD COLUMN cancel_reason text;

-- A cancelled run says why, on the same terms as a rejected leave request
-- (0091) and a rejected wage file. Somebody reading the ledger a year later
-- finds two entries that cancel out and needs to know what happened; "the
-- payroll was cancelled" is not an answer.
ALTER TABLE payroll_run
  ADD CONSTRAINT payroll_run_cancel_says_why
  CHECK (status <> 'cancelled' OR btrim(coalesce(cancel_reason, '')) <> '');

-- Cancellation is recorded with the same coherence rule the rest of the schema
-- uses for verified_on/verified_by: the three facts arrive together or not at
-- all.
ALTER TABLE payroll_run
  ADD CONSTRAINT payroll_run_cancellation_coherent
  CHECK ((cancelled_at IS NULL) = (cancel_reason IS NULL));

COMMENT ON COLUMN payroll_run.cancel_reason IS
  'Why the run was cancelled. Required once status is cancelled: the ledger '
  'carries the reversing entries, and this is the only place that says what '
  'they were for.';
