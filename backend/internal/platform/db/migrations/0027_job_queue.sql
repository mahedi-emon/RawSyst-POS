-- 0027 — The job queue, per design document 08.
--
-- PostgreSQL-backed rather than Redis, and the table sits in the SAME database
-- as the business data. That is the point, not a compromise:
--
--   * A job enqueued in the same transaction as its trigger cannot be orphaned
--     by a crash between the two writes. An invoice that exists but was never
--     queued for submission is a legal exposure nobody would notice.
--   * Job state is queryable with SQL, which matters the moment an Owner asks
--     why an invoice has not reached ZATCA.
--   * One fewer moving part. Redis stays for sessions and caching, where losing
--     state is survivable.

CREATE TYPE job_state AS ENUM ('pending', 'running', 'done', 'failed', 'dead');

CREATE TABLE job (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  -- NULL for platform jobs. Deliberately NOT tenant-scoped by row-level
  -- security: the worker runs as the platform and must see every tenant's
  -- queue, and a job row carries no business content — only ids and a kind.
  tenant_id     uuid REFERENCES tenant(id) ON DELETE CASCADE,

  kind          text  NOT NULL,
  payload       jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- Ordering key. Jobs sharing a queue_key run strictly in sequence, never in
  -- parallel. ZATCA submission uses device:{id} because the hash chain must be
  -- submitted in ICV order (E1.3 RULE 4) — submitting 4,183 before 4,182 breaks
  -- the chain, which is the exact tamper signal ZATCA looks for.
  queue_key     text,

  state         job_state NOT NULL DEFAULT 'pending',
  priority      smallint  NOT NULL DEFAULT 100,   -- lower runs first
  attempts      integer   NOT NULL DEFAULT 0,

  -- Zero means unlimited. ZATCA submission uses it: an unreported invoice is a
  -- legal exposure that does not stop being one after twenty-five tries, so
  -- there is no dead-letter path that quietly discards it (E1.2).
  max_attempts  integer   NOT NULL DEFAULT 25,

  run_after     timestamptz NOT NULL DEFAULT now(),
  locked_at     timestamptz,
  locked_by     text,
  last_error    text,

  created_at    timestamptz NOT NULL DEFAULT now(),
  completed_at  timestamptz,

  -- The same logical job cannot be enqueued twice while one is outstanding.
  dedupe_key    text,

  CONSTRAINT job_kind_format CHECK (kind ~ '^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$'),
  CONSTRAINT job_attempts_non_negative CHECK (attempts >= 0 AND max_attempts >= 0),
  CONSTRAINT job_lock_is_complete CHECK (
    (locked_at IS NULL AND locked_by IS NULL)
    OR (locked_at IS NOT NULL AND locked_by IS NOT NULL))
);

CREATE UNIQUE INDEX job_dedupe_uq ON job (dedupe_key)
  WHERE dedupe_key IS NOT NULL AND state IN ('pending', 'running');

-- The claim query's index: ready work, best first.
CREATE INDEX job_claimable_idx ON job (priority, run_after)
  WHERE state = 'pending';
CREATE INDEX job_queue_key_idx ON job (queue_key)
  WHERE queue_key IS NOT NULL AND state IN ('pending', 'running');
CREATE INDEX job_tenant_idx ON job (tenant_id) WHERE tenant_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Claiming
-- ---------------------------------------------------------------------------

-- Claims one job for a worker.
--
-- FOR UPDATE SKIP LOCKED so several workers can drain the queue without
-- blocking each other or handing the same job to two of them.
--
-- The serialisation clause is what makes ordering real: a job whose queue_key
-- already has something RUNNING is not claimable, so everything sharing that
-- key runs strictly in sequence. For ZATCA that means one device's invoices are
-- submitted in ICV order even with a dozen workers running.
CREATE OR REPLACE FUNCTION claim_job(p_worker text)
RETURNS TABLE (
  id uuid, tenant_id uuid, kind text, payload jsonb,
  queue_key text, attempts integer, max_attempts integer
)
LANGUAGE sql VOLATILE AS $$
  WITH claimed AS (
    SELECT j.id
    FROM job j
    WHERE j.state = 'pending'
      AND j.run_after <= now()
      AND (
        j.queue_key IS NULL
        OR NOT EXISTS (
          SELECT 1 FROM job r
          WHERE r.queue_key = j.queue_key AND r.state = 'running'
        )
      )
    ORDER BY j.priority, j.run_after, j.created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
  )
  UPDATE job j
  SET state = 'running',
      attempts = j.attempts + 1,
      locked_at = now(),
      locked_by = p_worker
  FROM claimed c
  WHERE j.id = c.id
  RETURNING j.id, j.tenant_id, j.kind, j.payload,
            j.queue_key, j.attempts, j.max_attempts
$$;

-- Releases a job that a worker died holding.
--
-- Without this a crash mid-job leaves it 'running' forever, and because a
-- running job blocks its whole queue_key, one dead worker would silently stop
-- every invoice for that terminal from ever being submitted.
CREATE OR REPLACE FUNCTION reap_abandoned_jobs(p_older_than interval)
RETURNS integer
LANGUAGE sql VOLATILE AS $$
  WITH released AS (
    UPDATE job
    SET state = 'pending', locked_at = NULL, locked_by = NULL,
        last_error = 'The worker holding this job stopped without finishing it.'
    WHERE state = 'running' AND locked_at < now() - p_older_than
    RETURNING 1
  )
  SELECT count(*)::integer FROM released
$$;

-- ---------------------------------------------------------------------------
-- ZATCA submission tracking
-- ---------------------------------------------------------------------------
--
-- Every attempt is recorded, successful or not. E1.2 requires that a failed
-- submission is never silently dropped, and "never silently" means there has to
-- be somewhere the attempt is written down — an Owner asking why an invoice is
-- three days late needs to see what happened each time, not just the latest
-- error.
CREATE TABLE zatca_submission_attempt (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  invoice_id   uuid NOT NULL REFERENCES sales_invoice(id) ON DELETE RESTRICT,

  attempt_no   integer NOT NULL,

  -- 'reporting' for B2C, 'clearance' for B2B. Recorded because the two are
  -- different endpoints with different acceptance rules, and an invoice sent
  -- down the wrong one is rejected for a reason that looks like a data problem.
  route        text NOT NULL,

  outcome      text NOT NULL,
  http_status  integer,

  -- What ZATCA said, kept verbatim. A warning on a 202 has to reach the Owner
  -- unaltered: paraphrasing a compliance notice is how its meaning gets lost.
  response     jsonb,
  error        text,

  attempted_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT zatca_attempt_route_valid CHECK (route IN ('reporting', 'clearance')),
  CONSTRAINT zatca_attempt_outcome_valid CHECK (outcome IN (
    'accepted',
    'accepted_with_warnings',
    -- A business rejection. Retrying would never succeed and would mask the
    -- alert, so it does not retry (E1.2).
    'rejected',
    -- Anything that might succeed later: a timeout, a 5xx, a DNS failure.
    'transport_failure',
    -- The integration is not verified, so nothing was sent. Distinct from a
    -- transport failure because no request left this machine.
    'not_attempted'
  )),
  CONSTRAINT zatca_attempt_no_positive CHECK (attempt_no > 0)
);

CREATE INDEX zatca_attempt_invoice_idx ON zatca_submission_attempt (invoice_id, attempt_no);
CREATE INDEX zatca_attempt_tenant_idx  ON zatca_submission_attempt (tenant_id);

-- An attempt is a fact about what happened. Correcting one means recording
-- another, exactly as correcting a journal entry means a reversal.
CREATE TRIGGER zatca_attempt_immutable
  BEFORE UPDATE OR DELETE ON zatca_submission_attempt
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE zatca_submission_attempt ENABLE ROW LEVEL SECURITY;
ALTER TABLE zatca_submission_attempt FORCE  ROW LEVEL SECURITY;
CREATE POLICY zatca_attempt_isolation ON zatca_submission_attempt
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Staleness (E1.3 RULE 6)
-- ---------------------------------------------------------------------------

-- The age of the oldest invoice that has not reached ZATCA, per company.
--
-- Thresholds are NOT encoded here. 12, 24 and 72 hours come from design
-- document 08 §5 and are applied in the evaluator, so a change to them is a
-- change in one place rather than a migration.
CREATE OR REPLACE FUNCTION oldest_unsubmitted_invoice(p_company_id uuid)
RETURNS TABLE (
  invoice_id uuid, issued_at timestamptz, age interval, unsubmitted_count bigint
)
LANGUAGE sql STABLE AS $$
  SELECT i.id, i.issued_at, now() - i.issued_at,
         (SELECT count(*) FROM sales_invoice c
          WHERE c.company_id = p_company_id
            AND c.state IN ('signed_pending_report', 'signed_pending_clear',
                            'uncleared_issued', 'submitted'))
  FROM sales_invoice i
  WHERE i.company_id = p_company_id
    AND i.state IN ('signed_pending_report', 'signed_pending_clear',
                    'uncleared_issued', 'submitted')
  ORDER BY i.issued_at
  LIMIT 1
$$;

-- Raised alerts, so an escalation fires once per level rather than every minute
-- the sweep runs. An Owner who receives the same critical alert sixty times an
-- hour stops reading them, which defeats the alert.
CREATE TABLE compliance_alert (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  kind        text NOT NULL,
  level       text NOT NULL,
  detail      text NOT NULL,

  raised_at   timestamptz NOT NULL DEFAULT now(),
  cleared_at  timestamptz,

  CONSTRAINT compliance_alert_level_valid CHECK (level IN ('notice', 'warning', 'critical')),
  CONSTRAINT compliance_alert_kind_format CHECK (kind ~ '^[a-z][a-z0-9_.]*$')
);

-- One open alert per kind and level per company.
CREATE UNIQUE INDEX compliance_alert_open_uq
  ON compliance_alert (company_id, kind, level) WHERE cleared_at IS NULL;
CREATE INDEX compliance_alert_tenant_idx ON compliance_alert (tenant_id);

ALTER TABLE compliance_alert ENABLE ROW LEVEL SECURITY;
ALTER TABLE compliance_alert FORCE  ROW LEVEL SECURITY;
CREATE POLICY compliance_alert_isolation ON compliance_alert
  USING (tenant_id = current_tenant_id());
