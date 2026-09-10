-- 0135 — a backup you can carry, and put back.
--
-- ---------------------------------------------------------------------------
-- What 0093 got right, and what it left out
-- ---------------------------------------------------------------------------
--
-- `backup_record` has held the distinction that matters since 0093: `status`
-- says a run finished and `verified_at` says somebody proved it restores, and
-- they are separate columns because they are separate claims. Nothing about
-- that changes here.
--
-- What it has no room for is the rest of the life of a backup. A snapshot in an
-- object store has an id; the row had nowhere to put it, so the only way back
-- from a row to the thing it describes was a `location` string parsed by hand.
-- A backup that came from somebody's laptop rather than from this server has a
-- different provenance and the same shape, and `kind` refused to say so. And
-- the states an operator actually watches — dumping, uploading, verifying,
-- restore-validating, restoring — collapsed into three words, of which one was
-- 'running'.
--
-- So: an id, a provenance, a phase, and the two documents a restore is decided
-- from — the manifest and the verification report — kept as written rather than
-- flattened into columns that would need a migration every time the manifest
-- learns a field.
--
-- ---------------------------------------------------------------------------
-- Why there is a task table and not a job row
-- ---------------------------------------------------------------------------
--
-- `job` already exists and is the right place for work that must be atomic with
-- its trigger. Backup work is not that. It is claimed by a DIFFERENT process
-- from the one that drains `job`: taking a dump needs `pg_dump`, `pg_dump` comes
-- from the postgres image, and the API and worker images are `scratch` and have
-- no room for it and no business carrying it. A second drainer on `job` would
-- claim ZATCA submissions it cannot run and fail them.
--
-- The other half is that these tasks are watched. An operator who presses
-- CREATE BACKUP is looking at a screen, and what they need is the truthful word
-- for what is happening now — Dumping, Uploading, Verifying — not a spinner
-- over a job row that says 'running' for four minutes. `stage` is that word,
-- and it is written by the agent as it goes.
--
-- ---------------------------------------------------------------------------
-- One heavy task at a time, enforced by the database
-- ---------------------------------------------------------------------------
--
-- This runs on two cores and 3.7 GiB. Two concurrent `pg_dump`s, or a dump and
-- a restore-into-scratch, is the shape of an outage during the hour somebody
-- was trying to protect themselves from one. A partial unique index makes the
-- second one impossible rather than merely discouraged, and it does it in the
-- database, where it holds however many agents are running and whatever any of
-- them believes.
--
-- ---------------------------------------------------------------------------
-- Maintenance mode
-- ---------------------------------------------------------------------------
--
-- A migration to another server loses whatever was written after the final
-- backup. The only way not to lose it is to stop writing, which means the
-- product has to be able to stop accepting writes — one row, read by the API,
-- and platform operators keep working so that the person doing the migration
-- can see what they are doing.

-- ---------------------------------------------------------------------------
-- backup_record grows the rest of a backup's life
-- ---------------------------------------------------------------------------

ALTER TABLE backup_record
  -- The id of the snapshot in the store, which is what every other operation
  -- takes as its argument. Text rather than uuid: it is a sortable timestamp by
  -- construction, and retention and "the newest" are lexical operations on it.
  ADD COLUMN snapshot_id     text,

  -- Where it came from.
  --   server — taken here, by the timer or by somebody pressing the button
  --   upload — carried in from a laptop, because the server it was taken on is
  --            gone. Provenance changes what may be assumed: an uploaded
  --            artifact has been out of this system's custody.
  ADD COLUMN source          text NOT NULL DEFAULT 'server',

  -- The word an operator is shown. `status` still answers "did the run
  -- finish"; this answers "what is true about this backup now", which is the
  -- question a screen asks and the one that has more than three answers.
  ADD COLUMN phase           text NOT NULL DEFAULT 'pending',

  ADD COLUMN app_version     text,
  ADD COLUMN schema_version  integer,

  -- The manifest and the verification report, as written. Kept whole rather
  -- than shredded into columns: they are documents a person reads when deciding
  -- whether to restore, they gain fields as the product learns, and a schema
  -- change per field would make the manifest the least changeable part of a
  -- system whose whole job is to outlive the build that wrote it.
  ADD COLUMN manifest        jsonb,
  ADD COLUMN verify_report   jsonb,

  ADD COLUMN retention_class text,

  -- Whether the artifact in the store is ciphertext. A restore that does not
  -- know this hands `pg_restore` an AES stream and reports a corrupt dump.
  ADD COLUMN encrypted       boolean NOT NULL DEFAULT false,

  -- The bucket, or 'staging' for something uploaded and not yet anywhere else.
  -- Never an endpoint with credentials in it.
  ADD COLUMN storage         text;

-- 'uploaded' is a third provenance for `kind`, alongside the timer and the
-- button. The old constraint has to go to make room; it is replaced, not
-- loosened.
ALTER TABLE backup_record DROP CONSTRAINT backup_record_kind_valid;
ALTER TABLE backup_record ADD CONSTRAINT backup_record_kind_valid
  CHECK (kind IN ('scheduled', 'manual', 'uploaded'));

ALTER TABLE backup_record ADD CONSTRAINT backup_record_source_valid
  CHECK (source IN ('server', 'upload'));

-- The phases, in the order they happen. Named for what is true rather than for
-- what the code is doing, because these words are shown to a person.
--
--   pending            queued, nothing has happened yet
--   creating           pg_dump is running
--   uploading          the dump is going to the store
--   uploaded           the artifact is there; NOTHING has been proved about it
--   verifying          it is being restored into a temporary database
--   verified           it restored and everything checked out
--   invalid            it did not, and the reason is recorded
--   failed             the run itself failed
--   restore_validating a restore of it is being rehearsed
--   restore_ready      the rehearsal passed; production restore is now allowed
--   restoring          a production restore is in progress
--   restored           it is what production now holds
--   restore_failed     it is not, and production is what it was
ALTER TABLE backup_record ADD CONSTRAINT backup_record_phase_valid
  CHECK (phase IN (
    'pending', 'creating', 'uploading', 'uploaded', 'verifying', 'verified',
    'invalid', 'failed', 'restore_validating', 'restore_ready', 'restoring',
    'restored', 'restore_failed'));

-- A verified phase must have something to point at. Reaching 'verified' with
-- `verified_at` NULL would put a green word on a screen over a claim nobody
-- made, which is the single failure this whole subsystem exists to prevent.
ALTER TABLE backup_record ADD CONSTRAINT backup_record_verified_phase_is_stamped
  CHECK (phase <> 'verified' OR verified_at IS NOT NULL);

-- One row per snapshot. Two rows claiming the same snapshot id would make
-- "which of these is the record of that backup" unanswerable, and every
-- operation here takes a snapshot id as its argument.
CREATE UNIQUE INDEX backup_record_snapshot_idx
  ON backup_record (snapshot_id) WHERE snapshot_id IS NOT NULL;

CREATE INDEX backup_record_phase_idx ON backup_record (phase, started_at DESC);

-- Existing rows get the phase their columns already imply. Written as a single
-- CASE rather than three updates so no row can be missed by the ordering.
UPDATE backup_record SET phase = CASE
  WHEN status = 'running'   THEN 'creating'
  WHEN status = 'failed'    THEN 'failed'
  WHEN verified_at IS NOT NULL THEN 'verified'
  WHEN verify_error IS NOT NULL AND btrim(verify_error) <> '' THEN 'invalid'
  ELSE 'uploaded'
END;

-- ---------------------------------------------------------------------------
-- backup_task — the work a screen can ask for
-- ---------------------------------------------------------------------------

CREATE TABLE backup_task (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  --   create             take a backup
  --   verify             prove one restores, into a temporary database
  --   restore_validate   rehearse a restore of one, into a temporary database
  --   restore_production replace the live database with one, keeping the old
  --   prune              remove snapshots outside the retention policy
  kind        text NOT NULL,

  -- Which snapshot. NULL for `create`, which does not have one until it has
  -- finished making it, and for `prune`, which is about all of them.
  snapshot_id text,
  backup_id   uuid REFERENCES backup_record(id) ON DELETE SET NULL,

  state       text NOT NULL DEFAULT 'queued',

  -- The truthful word for what is happening now. Written by the agent as it
  -- goes, read by the screen. Never a percentage this code cannot compute:
  -- `pg_dump` does not report progress and inventing one would be a lie with a
  -- progress bar around it.
  stage       text NOT NULL DEFAULT 'pending',

  -- Who asked. The label is denormalised for the same reason the audit trail's
  -- is: it has to survive the user row being deleted, or the record of who
  -- ordered a production restore becomes a missing person.
  requested_by       uuid REFERENCES app_user(id) ON DELETE SET NULL,
  requested_by_label text,

  requested_at timestamptz NOT NULL DEFAULT now(),
  claimed_by   text,
  claimed_at   timestamptz,
  started_at   timestamptz,
  finished_at  timestamptz,
  heartbeat_at timestamptz,

  attempts    integer NOT NULL DEFAULT 0,

  -- What the task was asked to do, beyond its kind. Never a credential: the
  -- agent reads storage and database credentials from its own environment, and
  -- a task row is readable by every platform operator.
  params      jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- What it found. A verification report, a validation report, the id of the
  -- snapshot a `create` produced.
  report      jsonb,
  error       text,

  CONSTRAINT backup_task_kind_valid CHECK (kind IN (
    'create', 'verify', 'restore_validate', 'restore_production', 'prune')),
  CONSTRAINT backup_task_state_valid CHECK (state IN (
    'queued', 'running', 'done', 'failed', 'cancelled')),
  CONSTRAINT backup_task_failure_says_why CHECK (
    state <> 'failed' OR btrim(coalesce(error, '')) <> ''),
  -- Everything but `create` and `prune` is about a particular snapshot, and a
  -- restore with no snapshot named is a restore of nothing.
  CONSTRAINT backup_task_names_its_snapshot CHECK (
    kind IN ('create', 'prune') OR btrim(coalesce(snapshot_id, '')) <> '')
);

CREATE INDEX backup_task_ready_idx
  ON backup_task (requested_at) WHERE state = 'queued';
CREATE INDEX backup_task_recent_idx ON backup_task (requested_at DESC);

-- One heavy task at a time, for the whole installation.
--
-- Two `pg_dump`s on two cores, or a dump running while a restore fills the
-- staging disk, is an outage caused by the thing that exists to prevent one.
-- Expressed as a unique index on a constant so the database refuses the second
-- one however many agents are running and whatever any of them believes.
--
-- `prune` is excluded: it is a listing and some DELETEs, and blocking it behind
-- a four-minute verification would mean the disk fills while the policy waits.
CREATE UNIQUE INDEX backup_task_one_heavy_at_a_time ON backup_task ((true))
  WHERE state IN ('queued', 'running')
    AND kind IN ('create', 'verify', 'restore_validate', 'restore_production');

-- No tenant_id, and that is the point: a dump of this database is every tenant
-- at once, so a task about one belongs to none of them. RLS is on anyway, with
-- a policy that names the platform explicitly — defence in depth against a
-- future route that reaches this table on a tenant connection by accident.
ALTER TABLE backup_task ENABLE ROW LEVEL SECURITY;
ALTER TABLE backup_task FORCE  ROW LEVEL SECURITY;
CREATE POLICY backup_task_platform_only ON backup_task
  USING (current_setting('app.platform_admin', true) = 'on')
  WITH CHECK (current_setting('app.platform_admin', true) = 'on');

-- ---------------------------------------------------------------------------
-- platform_maintenance — the write freeze
-- ---------------------------------------------------------------------------
--
-- PART of a migration that cannot be skipped: writes that happen after the
-- final backup do not travel to the new server. The only honest way to prevent
-- that is to stop accepting them, and the only place that can be enforced for
-- every route at once is in front of every route.
--
-- One row, forced by a primary key that can only hold one value. A settings
-- table with a key column would allow two rows disagreeing about whether the
-- business is open.
CREATE TABLE platform_maintenance (
  only_row    boolean PRIMARY KEY DEFAULT true CHECK (only_row),

  active      boolean NOT NULL DEFAULT false,

  -- Shown to whoever hits the wall, in their own language where the client
  -- knows the key, and verbatim where the operator wrote a sentence.
  reason      text,

  -- Platform operators keep working while this is on, or the person performing
  -- the migration cannot watch it happen. Reads are also still allowed: a
  -- cashier being shown yesterday's totals writes nothing, and locking them out
  -- of a screen they are reading turns a planned ten minutes into a support
  -- call.
  allow_reads boolean NOT NULL DEFAULT true,

  started_at  timestamptz,
  started_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  started_by_label text,
  ended_at    timestamptz,

  CONSTRAINT platform_maintenance_active_says_when CHECK (
    NOT active OR started_at IS NOT NULL)
);

INSERT INTO platform_maintenance (only_row, active) VALUES (true, false);

ALTER TABLE platform_maintenance ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_maintenance FORCE  ROW LEVEL SECURITY;
-- Readable by anyone signed in, because every request has to know; writable
-- only by the platform. Split into two policies rather than one with a
-- permissive USING, so the difference is visible in the catalogue.
CREATE POLICY platform_maintenance_read ON platform_maintenance
  FOR SELECT USING (true);
CREATE POLICY platform_maintenance_write ON platform_maintenance
  FOR UPDATE USING (current_setting('app.platform_admin', true) = 'on')
  WITH CHECK (current_setting('app.platform_admin', true) = 'on');
