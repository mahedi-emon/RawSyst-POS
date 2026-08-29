-- Delete what has finished and means nothing, and nothing else.
--
-- # The problem
--
-- Three tables grow forever and are read by nobody once their rows are spent:
-- finished jobs, expired refresh tokens, and expired sessions. A busy shop with
-- twenty tills produces a job per invoice submission and a token rotation per
-- refresh — on a fifteen-minute access token that is roughly a hundred rows per
-- till per day, forever, plus a row per sale.
--
-- None of it is a correctness problem. All of it is a storage and a planning
-- problem: the indexes stop fitting in memory, `SELECT ... FOR UPDATE SKIP
-- LOCKED` on the job queue walks a table that is 99% tombstones, and the
-- nightly backup grows without carrying anything.
--
-- # What is deliberately NOT deleted, and why
--
-- **`audit_log`.** Blueprint A4 says every Super Admin action is "permanently
-- logged", and D4 makes the trail six fields that answer who, what, when,
-- where, before and after. A retention window on an audit log is not
-- housekeeping, it is the deletion of evidence — and the table carries an
-- append-only trigger precisely so that a future migration cannot casually do
-- this.
--
-- **`zatca_submission_attempt`.** E1.2 requires that a failed submission is
-- never silently dropped, and "never silently" means the attempts stay
-- readable. An Owner asking why an invoice is three days late needs each
-- attempt, not the latest error.
--
-- **Anything posted.** Invoices, journal entries, stock movements and cash
-- sessions are the business's books. They carry `reject_delete()` triggers and
-- this does not go near them.
--
-- # Why a function rather than a scheduled job row
--
-- The worker already runs a reaper on a ticker (`reap_abandoned_jobs`). This is
-- the same shape and the same schedule, which means one thing to operate rather
-- than two, and no queue row that could itself become the thing that never gets
-- cleaned up.

-- How long a finished job is kept.
--
-- Seven days rather than one: a job that failed is worth reading after a
-- weekend, and the whole point of recording `last_error` is that somebody looks
-- at it on Monday. A job that succeeded is kept for the same window so the two
-- can be compared — "did this ever work" is answered by its neighbours.
CREATE OR REPLACE FUNCTION prune_finished_jobs(p_older_than interval)
RETURNS integer
LANGUAGE sql VOLATILE AS $$
  WITH gone AS (
    DELETE FROM job
    -- `done` and `dead` only. A pending job is waiting, a running one is
    -- being worked, and a failed one is between retries — deleting any of
    -- those would drop work rather than tidy after it.
    WHERE state IN ('done', 'dead')
      AND coalesce(completed_at, created_at) < now() - p_older_than
    RETURNING 1
  )
  SELECT count(*)::integer FROM gone
$$;

-- Expired credentials.
--
-- A refresh token past its expiry cannot be exchanged for anything: the chain
-- check reads `expires_at` before it reads the hash. Keeping it proves nothing
-- and is a row holding a hash of a credential, which is a thing worth having
-- fewer of.
--
-- The grace period matters. Reuse detection works by finding a token that was
-- already rotated, so deleting a family the moment it expires would turn "this
-- token was stolen and replayed" into "this token is unknown" — the same
-- refusal with the alarm removed. A window past expiry keeps the evidence for
-- as long as a replay could plausibly arrive.
CREATE OR REPLACE FUNCTION prune_expired_credentials(p_grace interval)
RETURNS integer
LANGUAGE sql VOLATILE AS $$
  WITH tokens AS (
    DELETE FROM session_refresh_token
    WHERE expires_at < now() - p_grace
    RETURNING 1
  ), sessions AS (
    -- A session with no tokens left and past its own expiry is closed. The
    -- cascade from `user_session` would take the tokens with it, so this runs
    -- second and only reaches sessions the step above has already emptied.
    DELETE FROM user_session s
    WHERE s.expires_at < now() - p_grace
      AND NOT EXISTS (
        SELECT 1 FROM session_refresh_token t WHERE t.session_id = s.id)
    RETURNING 1
  )
  SELECT (SELECT count(*) FROM tokens) + (SELECT count(*) FROM sessions)
$$;

COMMENT ON FUNCTION prune_finished_jobs(interval) IS
  'Deletes done and dead jobs older than the interval. Never touches pending, '
  'running or failed work.';

COMMENT ON FUNCTION prune_expired_credentials(interval) IS
  'Deletes refresh tokens and sessions past expiry plus a grace period. The '
  'grace exists so replay detection still has the evidence it reads.';
