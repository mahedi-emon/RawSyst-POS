-- 0136 — the log leaves the building before the building burns.
--
-- ---------------------------------------------------------------------------
-- What 0135 protects against, and what it does not
-- ---------------------------------------------------------------------------
--
-- 0135 gave a `pg_dump` snapshot a life: an id, a phase, a manifest, a
-- verification, a rehearsal and a production restore with a rollback. All of
-- that is intact and none of it changes here.
--
-- What a dump cannot do is give back a moment that was not photographed. The
-- timer runs at 03:30; a server lost at 22:00 loses the trading day, and no
-- amount of care about the dump changes that number. BACKUP.md said so plainly
-- and said point-in-time recovery was not implemented. This is it.
--
-- ---------------------------------------------------------------------------
-- Why there are two kinds of backup now, and neither is redundant
-- ---------------------------------------------------------------------------
--
-- The write-ahead log is a record of changes to PAGES — block 37 of relation
-- 16384 became these bytes. It cannot be replayed onto a restored dump,
-- because a restored dump has different relation files with different pages in
-- them. Replaying it requires a byte-level copy of the data directory, which is
-- what `pg_basebackup` takes and what `base_backup` records here.
--
--   backup_record   a pg_dump snapshot. Portable, readable on any server,
--                   survives a corrupt cluster. Recovery point: the dump.
--   base_backup     a physical copy. Tied to this PostgreSQL major version.
--                   Recovery point: any second covered by the archive.
--
-- A corrupted page is inside the base backup and inside the WAL; it is not
-- inside the dump. Keeping both is the answer and neither table replaces the
-- other.
--
-- ---------------------------------------------------------------------------
-- Why the archive's health is a CACHE and not the record
-- ---------------------------------------------------------------------------
--
-- `wal_archive_state` holds one row that a dashboard reads. It is written by
-- the agent from `pg_stat_archiver`, which PostgreSQL maintains itself, and
-- from a listing of the bucket.
--
-- It is deliberately not the authority, and nothing in a recovery reads it. On
-- the day this matters the server this table lives on may be the thing that
-- died, and an archive that can only be understood with the help of the
-- database it was protecting is not an archive. The authority is the objects in
-- the store and the sidecar beside each one.
--
-- The second reason is a loop. Writing a row per archived segment would mean
-- the act of archiving generated write-ahead log, which would be archived,
-- which would write a row. One row updated on a timer has no such property.
--
-- ---------------------------------------------------------------------------
-- Why a recovery is audited even when it changes nothing
-- ---------------------------------------------------------------------------
--
-- A point-in-time recovery into an isolated instance touches no production
-- data, so there is a reading on which it does not need an audit row. That
-- reading is wrong: the recovery produced a readable copy of every business on
-- the platform at some earlier moment, and who asked for that, when, and what
-- moment they chose is exactly the thing an investigation would need. So
-- `pitr_recovery` records every one — drill, rehearsal and production alike.

-- ---------------------------------------------------------------------------
-- base_backup — the physical copies
-- ---------------------------------------------------------------------------

CREATE TABLE base_backup (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  -- The id in the store, which is what every other operation takes as its
  -- argument. The same sortable timestamp shape a snapshot id has, so
  -- retention and "the newest" stay lexical operations.
  base_id       text NOT NULL,

  started_at    timestamptz NOT NULL DEFAULT now(),
  completed_at  timestamptz,

  --   running    pg_basebackup is copying
  --   uploading  the copy is going to the store
  --   stored     it is there and has a COMPLETED marker
  --   verified   a recovery of it reached its own consistency point and the
  --              result was counted
  --   invalid    it did not, and the reason is recorded
  --   failed     the run itself failed
  --   expired    retention removed it; the row stays as the record that it
  --              existed, because a recovery window that shortened is a thing
  --              somebody may need to explain
  status        text NOT NULL DEFAULT 'running',

  -- Where in the write-ahead log it sits. The ending position is the earliest
  -- moment it can recover to: before that, the cluster on disk was being
  -- copied and is not consistent. A recovery target earlier than this is
  -- refused rather than attempted.
  timeline      integer,
  start_lsn     text,
  end_lsn       text,
  start_segment text,
  end_segment   text,

  -- The PostgreSQL that wrote it. A physical backup can only be read by its own
  -- major version — it does not upgrade — so a restore checks this before it
  -- spends an hour downloading.
  pg_version     text,
  pg_version_num integer,

  size_bytes    bigint,
  checksum      text,

  encrypted     boolean NOT NULL DEFAULT false,
  -- WHICH key, never the key. A hash, from which nothing can be recovered, so
  -- a restore can say "you have the wrong key" instead of "decryption failed".
  key_fingerprint text,

  -- The bucket and the prefix. Never an endpoint with credentials in it.
  storage       text,
  storage_prefix text,

  app_version   text,
  source_host   text,

  retention_class text,

  -- The manifest as written, rather than shredded into columns: it is a
  -- document a person reads when deciding whether to recover from this, it
  -- gains fields as the product learns, and a schema change per field would
  -- make it the least changeable part of a system whose whole job is to outlive
  -- the build that wrote it.
  manifest      jsonb,

  verified_at   timestamptz,
  verify_report jsonb,
  error         text,

  requested_by       uuid REFERENCES app_user(id) ON DELETE SET NULL,
  requested_by_label text,

  CONSTRAINT base_backup_status_valid CHECK (status IN (
    'running', 'uploading', 'stored', 'verified', 'invalid', 'failed',
    'expired')),

  -- A verified base backup must have something to point at, for the same
  -- reason a verified snapshot must: a green word on a screen over a claim
  -- nobody made is the single failure this subsystem exists to prevent.
  CONSTRAINT base_backup_verified_is_stamped CHECK (
    status <> 'verified' OR verified_at IS NOT NULL),

  CONSTRAINT base_backup_failure_says_why CHECK (
    status <> 'failed' OR btrim(coalesce(error, '')) <> ''),

  -- A stored backup must say where in the log it sits, or it is a copy of a
  -- data directory and not a recovery source.
  CONSTRAINT base_backup_stored_knows_its_place CHECK (
    status NOT IN ('stored', 'verified')
    OR (timeline IS NOT NULL AND btrim(coalesce(end_segment, '')) <> ''))
);

CREATE UNIQUE INDEX base_backup_base_id_idx ON base_backup (base_id);
CREATE INDEX base_backup_recent_idx ON base_backup (started_at DESC);
CREATE INDEX base_backup_usable_idx ON base_backup (completed_at DESC)
  WHERE status IN ('stored', 'verified');

-- No tenant_id, and that is the point: a physical copy of this cluster is every
-- tenant at once, so it belongs to none of them. RLS is on anyway with a policy
-- that names the platform, as defence against a future route that reaches this
-- table on a tenant connection by accident.
ALTER TABLE base_backup ENABLE ROW LEVEL SECURITY;
ALTER TABLE base_backup FORCE  ROW LEVEL SECURITY;
CREATE POLICY base_backup_platform_only ON base_backup
  USING (current_setting('app.platform_admin', true) = 'on')
  WITH CHECK (current_setting('app.platform_admin', true) = 'on');

-- ---------------------------------------------------------------------------
-- wal_archive_state — one row, for a screen
-- ---------------------------------------------------------------------------
--
-- One row, forced by a primary key that can hold one value, for the same reason
-- `platform_maintenance` has one: a settings table with a key column allows two
-- rows disagreeing about whether the archive is healthy.

CREATE TABLE wal_archive_state (
  only_row    boolean PRIMARY KEY DEFAULT true CHECK (only_row),

  -- When the agent last looked. A dashboard reading a row nobody has refreshed
  -- for six hours is reading history, and it needs to be able to say so.
  observed_at timestamptz,

  archiving   boolean NOT NULL DEFAULT false,
  wal_level   text,

  -- Straight out of pg_stat_archiver, which PostgreSQL maintains itself. These
  -- are the only numbers here that are not inferred from something else.
  last_archived_wal   text,
  last_archived_at    timestamptz,
  archived_count      bigint NOT NULL DEFAULT 0,
  last_failed_wal     text,
  last_failed_at      timestamptz,
  failed_count        bigint NOT NULL DEFAULT 0,
  stats_reset_at      timestamptz,

  -- How far behind the archive is, in segments and in seconds. The first is the
  -- honest measure — a busy shop writes segments quickly and a quiet one writes
  -- none for an hour, so seconds alone reads as an outage every night.
  lag_segments integer,
  lag_seconds  integer,

  current_wal  text,
  timeline     integer,

  -- The local write-ahead log directory. This is the number that turns into an
  -- outage: when archiving stops, PostgreSQL keeps every segment, and the disk
  -- fills. Watching it is the whole of the early warning.
  pg_wal_bytes bigint,

  -- The archive as the store holds it.
  archive_segments      integer,
  archive_bytes         bigint,
  archive_gaps          integer,
  oldest_segment        text,
  newest_segment        text,
  store_reachable       boolean NOT NULL DEFAULT false,
  store_checked_at      timestamptz,
  store_error           text,

  -- The window, cached for a dashboard. Never read by a recovery.
  window_start timestamptz,
  window_end   timestamptz,

  -- What the last look concluded, in one word: green, amber or red. Computed
  -- in one place so the product has one opinion — the command line, the
  -- website and whatever watches the machine all read this.
  health       text,
  summary      text,

  CONSTRAINT wal_archive_state_health_valid CHECK (
    health IS NULL OR health IN ('green', 'amber', 'red'))
);

INSERT INTO wal_archive_state (only_row) VALUES (true);

ALTER TABLE wal_archive_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE wal_archive_state FORCE  ROW LEVEL SECURITY;
CREATE POLICY wal_archive_state_platform_only ON wal_archive_state
  USING (current_setting('app.platform_admin', true) = 'on')
  WITH CHECK (current_setting('app.platform_admin', true) = 'on');

-- ---------------------------------------------------------------------------
-- wal_archive_failure — the attempts that did not work
-- ---------------------------------------------------------------------------
--
-- `wal_archive_state` carries the LAST failure and a count. That is enough to
-- raise an alarm and not enough to answer "was it one outage or forty". This is
-- the history, bounded: the agent keeps the most recent few hundred and deletes
-- the rest, because a table that grows with a failing archive is a second
-- problem arriving during the first one.

CREATE TABLE wal_archive_failure (
  id          bigserial PRIMARY KEY,
  noticed_at  timestamptz NOT NULL DEFAULT now(),
  segment     text,
  failed_at   timestamptz,
  -- The reason, as PostgreSQL recorded it. Redacted before it is written: an
  -- archive command that failed on a signed URL must not put the signature in
  -- a table every platform operator can read.
  reason      text
);

CREATE INDEX wal_archive_failure_recent_idx
  ON wal_archive_failure (noticed_at DESC);
CREATE UNIQUE INDEX wal_archive_failure_once_idx
  ON wal_archive_failure (segment, failed_at)
  WHERE segment IS NOT NULL AND failed_at IS NOT NULL;

ALTER TABLE wal_archive_failure ENABLE ROW LEVEL SECURITY;
ALTER TABLE wal_archive_failure FORCE  ROW LEVEL SECURITY;
CREATE POLICY wal_archive_failure_platform_only ON wal_archive_failure
  USING (current_setting('app.platform_admin', true) = 'on')
  WITH CHECK (current_setting('app.platform_admin', true) = 'on');

-- ---------------------------------------------------------------------------
-- pitr_recovery — who asked for which moment, and what came back
-- ---------------------------------------------------------------------------

CREATE TABLE pitr_recovery (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  --   drill      the scheduled rehearsal, nobody asked
  --   isolated   an operator recovered to a moment to look at it
  --   production the result was put in front of the business
  kind        text NOT NULL DEFAULT 'isolated',

  base_id     text,

  -- What was asked for. `target_value` is the timestamp, position, name or
  -- transaction id, as text, because the five kinds are five types and a
  -- column per kind would be four nulls per row.
  target_kind  text NOT NULL,
  target_value text,

  -- Where replay actually stopped. The pair matters: a recovery asked for
  -- 14:32 that stopped at 14:29 because that was the last committed
  -- transaction is a success, and one that stopped at 09:00 because the
  -- archive had a hole is not, and only printing both tells them apart.
  reached_lsn  text,
  reached_at   timestamptz,
  timeline     integer,

  status      text NOT NULL DEFAULT 'running',

  started_at  timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,

  requested_by       uuid REFERENCES app_user(id) ON DELETE SET NULL,
  -- Denormalised for the same reason the audit trail's is: it has to survive
  -- the user row being deleted, or the record of who recovered every
  -- business's data to an earlier moment becomes a missing person.
  requested_by_label text,

  report      jsonb,
  error       text,

  CONSTRAINT pitr_recovery_kind_valid CHECK (
    kind IN ('drill', 'isolated', 'production')),
  CONSTRAINT pitr_recovery_status_valid CHECK (
    status IN ('running', 'succeeded', 'failed', 'refused')),
  CONSTRAINT pitr_recovery_target_valid CHECK (
    target_kind IN ('latest', 'immediate', 'time', 'before_time', 'lsn',
                    'name', 'xid')),
  CONSTRAINT pitr_recovery_failure_says_why CHECK (
    status NOT IN ('failed', 'refused') OR btrim(coalesce(error, '')) <> '')
);

CREATE INDEX pitr_recovery_recent_idx ON pitr_recovery (started_at DESC);

ALTER TABLE pitr_recovery ENABLE ROW LEVEL SECURITY;
ALTER TABLE pitr_recovery FORCE  ROW LEVEL SECURITY;
CREATE POLICY pitr_recovery_platform_only ON pitr_recovery
  USING (current_setting('app.platform_admin', true) = 'on')
  WITH CHECK (current_setting('app.platform_admin', true) = 'on');

-- ---------------------------------------------------------------------------
-- backup_task learns four more kinds of work
-- ---------------------------------------------------------------------------
--
--   base_backup   take a physical copy of the cluster
--   wal_verify    check the archive without downloading it, from the sidecars
--   wal_prune     remove the segments and base backups nothing can still need
--   pitr_restore  recover to a moment, into an isolated PostgreSQL
--
-- The constraints are replaced rather than loosened, and the one that says a
-- task names its snapshot has to learn that three of these are about the whole
-- archive and one is about a BASE backup rather than a snapshot.

ALTER TABLE backup_task DROP CONSTRAINT backup_task_kind_valid;
ALTER TABLE backup_task ADD CONSTRAINT backup_task_kind_valid CHECK (kind IN (
  'create', 'verify', 'restore_validate', 'restore_production', 'prune',
  'base_backup', 'wal_verify', 'wal_prune', 'pitr_restore'));

ALTER TABLE backup_task DROP CONSTRAINT backup_task_names_its_snapshot;
-- `pitr_restore` puts the BASE BACKUP id in `snapshot_id`. The two ids have the
-- same shape by construction — both come from the same generator — and a
-- second column that was null on every other row would make the listing screen
-- carry two nearly identical fields and pick between them per row.
ALTER TABLE backup_task ADD CONSTRAINT backup_task_names_its_snapshot CHECK (
  kind IN ('create', 'prune', 'base_backup', 'wal_verify', 'wal_prune')
  OR btrim(coalesce(snapshot_id, '')) <> '');

-- One heavy task at a time, for the whole installation, now including the four
-- new ones that are heavy.
--
-- A base backup copies the cluster and a point-in-time recovery unpacks one and
-- starts a second PostgreSQL. Either of those running beside a pg_dump on two
-- cores is an outage caused by the things that exist to prevent one.
--
-- `wal_verify` and `wal_prune` are excluded for the same reason `prune` is:
-- they are listings and some small reads, and blocking them behind a two-hour
-- base backup would mean the disk fills while the policy waits.
DROP INDEX backup_task_one_heavy_at_a_time;
CREATE UNIQUE INDEX backup_task_one_heavy_at_a_time ON backup_task ((true))
  WHERE state IN ('queued', 'running')
    AND kind IN ('create', 'verify', 'restore_validate', 'restore_production',
                 'base_backup', 'pitr_restore');

-- ---------------------------------------------------------------------------
-- Who may ask for any of this
-- ---------------------------------------------------------------------------
--
-- Nobody with a tenant permission, which is the whole security model and is the
-- same one 0135 settled on. A physical copy of this cluster is every business
-- at once; there is no permission a business owner could be granted that would
-- safely reach it, because such a permission would let one shop recover
-- another's books. Every route is platform-operator only, in the router, and
-- the RLS policies above are the second lock rather than the first.
--
-- The tenant-facing `backup.view` permission still shows a business the record
-- of its own dumps. It does not reach any table created here.
