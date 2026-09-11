// The rows a screen reads: archive health, physical base backups, recoveries.
//
// # Why these are all best-effort
//
// Every write here is allowed to fail without failing the thing it describes. A
// base backup that was taken, uploaded and marked COMPLETED in the store is a
// base backup, whether or not a row about it was written; an archive that is
// shipping segments is healthy whether or not anybody recorded the observation.
//
// That ordering is deliberate and it is the same one `register.go` settled on
// for dumps. The artefact in the store is the thing that matters. The row is
// how somebody finds out about it, and a database that cannot be written to
// must never stop a backup being taken — least of all because the reason it
// cannot be written to is usually the reason somebody is about to need the
// backup.
package backup

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// WALRegister is the point-in-time recovery half of the register.
type WALRegister struct{ pool *db.Pool }

// NewWALRegister builds one.
func NewWALRegister(pool *db.Pool) *WALRegister { return &WALRegister{pool: pool} }

func (r *WALRegister) ok() bool { return r != nil && r.pool != nil }

// --- the archive state ------------------------------------------------------

// Observe writes what the last look at the archive found.
//
// One row, updated in place. Deliberately not an append-only history: a row per
// observation on a one-minute timer is half a million rows a year to answer a
// question whose only interesting answer is the current one, and every one of
// those rows is itself write-ahead log that has to be archived.
func (r *WALRegister) Observe(ctx context.Context, s ArchiveStatus) error {
	if !r.ok() {
		return nil
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE wal_archive_state SET
			  observed_at = $1,
			  archiving = $2, wal_level = $3, timeline = $4,
			  last_archived_wal = nullif($5,''), last_archived_at = $6,
			  archived_count = $7,
			  last_failed_wal = nullif($8,''), last_failed_at = $9,
			  failed_count = $10, stats_reset_at = $11,
			  lag_segments = $12, lag_seconds = $13,
			  current_wal = nullif($14,''),
			  pg_wal_bytes = $15,
			  archive_segments = $16, archive_bytes = $17, archive_gaps = $18,
			  oldest_segment = nullif($19,''), newest_segment = nullif($20,''),
			  store_reachable = $21, store_checked_at = $1,
			  store_error = nullif($22,''),
			  window_start = $23, window_end = $24,
			  health = $25, summary = $26
			WHERE only_row`,
			s.ObservedAt, s.Archiving, s.WALLevel, int32(s.Timeline),
			s.LastArchivedWAL, nullTime(s.LastArchivedAt), s.ArchivedCount,
			s.LastFailedWAL, nullTime(s.LastFailedAt), s.FailedCount,
			nullTime(s.StatsResetAt),
			s.LagSegments, s.LagSeconds, s.CurrentWAL, s.PGWALBytes,
			s.ArchiveSegments, s.ArchiveBytes, s.ArchiveGaps,
			s.OldestSegment, s.NewestSegment,
			s.StoreReachable, s.StoreError,
			nullTime(s.Window.Start), nullTime(s.Window.End),
			s.Health, clipText(s.Summary, 1000))
		return e
	})
	return db.Translate(err, "")
}

// RecordArchiveFailure keeps the history of attempts that did not work.
//
// Idempotent by (segment, time): the observer runs on a timer and would
// otherwise record the same failure once a minute for as long as it remained
// the most recent one, turning one outage into a thousand rows.
func (r *WALRegister) RecordArchiveFailure(
	ctx context.Context, segment string, at time.Time, reason string,
) error {
	if !r.ok() || segment == "" || at.IsZero() {
		return nil
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO wal_archive_failure (segment, failed_at, reason)
			VALUES ($1, $2, nullif($3,''))
			ON CONFLICT (segment, failed_at) DO NOTHING`,
			clipText(segment, 64), at.UTC(), clipText(reason, 1000))
		return e
	})
	return db.Translate(err, "")
}

// TrimArchiveFailures keeps the failure history bounded.
//
// A table that grows while the archive is failing is a second problem arriving
// during the first one, on a disk that is already filling up because archiving
// stopped.
func (r *WALRegister) TrimArchiveFailures(ctx context.Context, keep int) error {
	if !r.ok() {
		return nil
	}
	if keep <= 0 {
		keep = 500
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			DELETE FROM wal_archive_failure WHERE id NOT IN (
			  SELECT id FROM wal_archive_failure
			   ORDER BY noticed_at DESC LIMIT $1)`, keep)
		return e
	})
	return db.Translate(err, "")
}

// ArchiveFailure is one recorded failure.
type ArchiveFailure struct {
	Segment  string `json:"segment,omitempty"`
	FailedAt string `json:"failed_at,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// RecentArchiveFailures reads the failure history, newest first.
func (r *WALRegister) RecentArchiveFailures(
	ctx context.Context, limit int,
) ([]ArchiveFailure, error) {
	if !r.ok() {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []ArchiveFailure
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT coalesce(segment,''), failed_at, coalesce(reason,'')
			  FROM wal_archive_failure
			 ORDER BY noticed_at DESC LIMIT $1`, limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var f ArchiveFailure
			var at *time.Time
			if e := rows.Scan(&f.Segment, &at, &f.Reason); e != nil {
				return e
			}
			if at != nil {
				f.FailedAt = at.UTC().Format(time.RFC3339)
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// StoredArchiveState is the cached readout, as a screen gets it.
type StoredArchiveState struct {
	ObservedAt string `json:"observed_at,omitempty"`
	Health     string `json:"health,omitempty"`
	Summary    string `json:"summary,omitempty"`

	Archiving bool   `json:"archiving"`
	WALLevel  string `json:"wal_level,omitempty"`
	Timeline  int    `json:"timeline,omitempty"`

	LastArchivedWAL string `json:"last_archived_segment,omitempty"`
	LastArchivedAt  string `json:"last_archived_at,omitempty"`
	ArchivedCount   int64  `json:"archived_total"`
	LastFailedWAL   string `json:"last_failed_segment,omitempty"`
	LastFailedAt    string `json:"last_failed_at,omitempty"`
	FailedCount     int64  `json:"failed_total"`

	LagSegments int `json:"lag_segments"`
	LagSeconds  int `json:"lag_seconds"`

	CurrentWAL string `json:"current_segment,omitempty"`
	PGWALBytes int64  `json:"pg_wal_bytes"`

	ArchiveSegments int    `json:"archive_segments"`
	ArchiveBytes    int64  `json:"archive_bytes"`
	ArchiveGaps     int    `json:"archive_gaps"`
	OldestSegment   string `json:"oldest_archived_segment,omitempty"`
	NewestSegment   string `json:"newest_archived_segment,omitempty"`
	StoreReachable  bool   `json:"store_reachable"`
	StoreError      string `json:"store_error,omitempty"`

	WindowStart string `json:"recovery_window_start,omitempty"`
	WindowEnd   string `json:"recovery_window_end,omitempty"`

	// Stale says the readout is old enough that it describes the past rather
	// than the present. A dashboard that showed a green from six hours ago
	// without saying so would be reporting the health of a machine that has
	// since stopped.
	Stale bool `json:"stale"`
}

// State reads the cached archive readout.
func (r *WALRegister) State(ctx context.Context) (StoredArchiveState, error) {
	var out StoredArchiveState
	if !r.ok() {
		return out, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var observed, lastArchived, lastFailed, windowStart, windowEnd *time.Time
		var health, summary, walLevel, lastWAL, failedWAL, current *string
		var oldest, newest, storeError *string
		var timeline, lagSeg, lagSec *int32
		var walBytes, archiveBytes *int64
		var archiveSegments, archiveGaps *int32

		if e := tx.QueryRow(ctx, `
			SELECT observed_at, archiving, wal_level, timeline,
			       last_archived_wal, last_archived_at, archived_count,
			       last_failed_wal, last_failed_at, failed_count,
			       lag_segments, lag_seconds, current_wal, pg_wal_bytes,
			       archive_segments, archive_bytes, archive_gaps,
			       oldest_segment, newest_segment,
			       store_reachable, store_error,
			       window_start, window_end, health, summary
			  FROM wal_archive_state WHERE only_row`).
			Scan(&observed, &out.Archiving, &walLevel, &timeline,
				&lastWAL, &lastArchived, &out.ArchivedCount,
				&failedWAL, &lastFailed, &out.FailedCount,
				&lagSeg, &lagSec, &current, &walBytes,
				&archiveSegments, &archiveBytes, &archiveGaps,
				&oldest, &newest,
				&out.StoreReachable, &storeError,
				&windowStart, &windowEnd, &health, &summary); e != nil {
			return e
		}

		out.ObservedAt = stamp(observed)
		out.LastArchivedAt = stamp(lastArchived)
		out.LastFailedAt = stamp(lastFailed)
		out.WindowStart = stamp(windowStart)
		out.WindowEnd = stamp(windowEnd)
		out.Health = text(health)
		out.Summary = text(summary)
		out.WALLevel = text(walLevel)
		out.LastArchivedWAL = text(lastWAL)
		out.LastFailedWAL = text(failedWAL)
		out.CurrentWAL = text(current)
		out.OldestSegment = text(oldest)
		out.NewestSegment = text(newest)
		out.StoreError = text(storeError)
		out.Timeline = int32p(timeline)
		out.LagSegments = int32p(lagSeg)
		out.LagSeconds = int32p(lagSec)
		out.ArchiveSegments = int32p(archiveSegments)
		out.ArchiveGaps = int32p(archiveGaps)
		if walBytes != nil {
			out.PGWALBytes = *walBytes
		}
		if archiveBytes != nil {
			out.ArchiveBytes = *archiveBytes
		}
		if observed != nil {
			out.Stale = time.Since(*observed) > 15*time.Minute
		} else {
			out.Stale = true
		}
		return nil
	})
	return out, db.Translate(err, "The archive status could not be read.")
}

// --- base backups -----------------------------------------------------------

// BaseRecord is a physical base backup as the register holds it.
type BaseRecord struct {
	ID     uuid.UUID `json:"id"`
	BaseID string    `json:"base_backup_id"`

	Status      string `json:"status"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`

	Timeline     int    `json:"timeline,omitempty"`
	StartLSN     string `json:"start_lsn,omitempty"`
	EndLSN       string `json:"end_lsn,omitempty"`
	StartSegment string `json:"start_wal_segment,omitempty"`
	EndSegment   string `json:"end_wal_segment,omitempty"`

	PGVersion string `json:"postgres_version,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Checksum  string `json:"checksum,omitempty"`

	Encrypted bool   `json:"encrypted"`
	KeyPrint  string `json:"key_fingerprint,omitempty"`

	Storage        string `json:"storage,omitempty"`
	RetentionClass string `json:"retention_class,omitempty"`
	AppVersion     string `json:"app_version,omitempty"`
	SourceHost     string `json:"source_host,omitempty"`

	VerifiedAt  string `json:"verified_at,omitempty"`
	Error       string `json:"error,omitempty"`
	RequestedBy string `json:"requested_by,omitempty"`

	// Only on the detail route. A listing of thirty with manifests attached is
	// a megabyte of JSON to draw a table of thirty rows.
	Manifest json.RawMessage `json:"manifest,omitempty"`
	Report   json.RawMessage `json:"verify_report,omitempty"`
}

// StartBase opens a row before the copy begins.
//
// Before, for the same reason a dump's row is opened before the dump: a base
// backup that dies halfway leaves something somebody can see, and a record
// written only on success makes a crashed two-hour copy indistinguishable from
// one nobody ever asked for.
func (r *WALRegister) StartBase(
	ctx context.Context, by *uuid.UUID, byLabel string,
) (uuid.UUID, error) {
	if !r.ok() {
		return uuid.Nil, nil
	}
	var id uuid.UUID
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO base_backup
			  (base_id, status, requested_by, requested_by_label)
			VALUES ('pending-' || gen_random_uuid()::text, 'running', $1,
			        nullif($2,''))
			RETURNING id`, by, clipText(byLabel, 200)).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, db.Translate(err,
			"That base backup could not be recorded.")
	}
	return id, nil
}

// BaseStored closes a row for a copy that reached the store.
//
// It lands in `stored`, never in `verified`. The bytes are there and NOTHING
// has been proved about them — the same distinction `register.go` keeps for a
// dump, and for the same reason: a screen must not be able to show the more
// comforting word by accident.
func (r *WALRegister) BaseStored(
	ctx context.Context, id uuid.UUID, m BaseManifest,
) error {
	if !r.ok() || id == uuid.Nil {
		return nil
	}
	body, err := json.Marshal(m)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"That base backup manifest could not be recorded.")
	}
	var checksum, keyPrint string
	if c, ok := m.Component(baseTarObject); ok {
		checksum = c.SHA256
	}
	if m.Encryption != nil {
		keyPrint = m.Encryption.KeyFingerprint
	}

	e := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE base_backup SET
			  base_id = $2, status = 'stored', completed_at = now(),
			  timeline = $3, start_lsn = $4, end_lsn = $5,
			  start_segment = $6, end_segment = $7,
			  pg_version = nullif($8,''), pg_version_num = $9,
			  size_bytes = $10, checksum = nullif($11,''),
			  encrypted = $12, key_fingerprint = nullif($13,''),
			  storage = nullif($14,''), storage_prefix = nullif($15,''),
			  app_version = nullif($16,''), source_host = nullif($17,''),
			  retention_class = nullif($18,''), manifest = $19
			WHERE id = $1`,
			id, m.ID, int32(m.Timeline), m.StartLSN, m.EndLSN,
			m.StartSegment, m.EndSegment,
			m.PostgresVersion, m.PostgresVersionNum,
			m.TotalBytes(), checksum,
			m.Encryption != nil, keyPrint,
			m.Storage.Bucket, m.Storage.Prefix,
			m.AppVersion, m.SourceHost, m.RetentionClass, body)
		return err
	})
	return db.Translate(e, "That base backup could not be recorded.")
}

// BaseFailed closes a row for a copy that did not finish.
func (r *WALRegister) BaseFailed(
	ctx context.Context, id uuid.UUID, reason string,
) error {
	if !r.ok() || id == uuid.Nil {
		return nil
	}
	if reason == "" {
		reason = "It failed and said nothing about why."
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE base_backup
			   SET status = 'failed', completed_at = now(), error = $2,
			       base_id = coalesce(nullif(base_id,''), 'failed-' || id::text)
			 WHERE id = $1`, id, clipText(reason, 2000))
		return e
	})
	return db.Translate(err, "")
}

// BaseVerified records that a recovery of this base backup was performed and
// the result was counted.
func (r *WALRegister) BaseVerified(
	ctx context.Context, baseID string, report any,
) error {
	if !r.ok() || baseID == "" {
		return nil
	}
	body, err := json.Marshal(report)
	if err != nil {
		body = []byte("{}")
	}
	e := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE base_backup
			   SET status = 'verified', verified_at = now(), verify_report = $2
			 WHERE base_id = $1 AND status IN ('stored', 'verified', 'invalid')`,
			baseID, body)
		return err
	})
	return db.Translate(e, "")
}

// BaseInvalid records that a recovery of this base backup did not check out.
func (r *WALRegister) BaseInvalid(
	ctx context.Context, baseID, reason string, report any,
) error {
	if !r.ok() || baseID == "" {
		return nil
	}
	body, err := json.Marshal(report)
	if err != nil {
		body = []byte("{}")
	}
	e := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE base_backup
			   SET status = 'invalid', verify_report = $2, error = $3
			 WHERE base_id = $1`, baseID, body, clipText(reason, 2000))
		return err
	})
	return db.Translate(e, "")
}

// BaseExpired marks rows whose backups retention has removed.
//
// The row stays. A recovery window that got shorter is something an operator
// may have to explain, and deleting the evidence that a base backup existed
// makes that impossible.
func (r *WALRegister) BaseExpired(ctx context.Context, ids []string) error {
	if !r.ok() || len(ids) == 0 {
		return nil
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE base_backup SET status = 'expired'
			 WHERE base_id = ANY($1) AND status <> 'expired'`, ids)
		return e
	})
	return db.Translate(err, "")
}

const baseColumns = `
	id, base_id, status, started_at, completed_at,
	timeline, coalesce(start_lsn,''), coalesce(end_lsn,''),
	coalesce(start_segment,''), coalesce(end_segment,''),
	coalesce(pg_version,''), coalesce(size_bytes,0), coalesce(checksum,''),
	encrypted, coalesce(key_fingerprint,''),
	coalesce(storage,''), coalesce(retention_class,''),
	coalesce(app_version,''), coalesce(source_host,''),
	verified_at, coalesce(error,''), coalesce(requested_by_label,'')`

func scanBase(row pgx.Row) (BaseRecord, error) {
	var b BaseRecord
	var started time.Time
	var completed, verified *time.Time
	var timeline *int32
	if err := row.Scan(&b.ID, &b.BaseID, &b.Status, &started, &completed,
		&timeline, &b.StartLSN, &b.EndLSN, &b.StartSegment, &b.EndSegment,
		&b.PGVersion, &b.SizeBytes, &b.Checksum,
		&b.Encrypted, &b.KeyPrint, &b.Storage, &b.RetentionClass,
		&b.AppVersion, &b.SourceHost, &verified, &b.Error,
		&b.RequestedBy); err != nil {
		return BaseRecord{}, err
	}
	b.StartedAt = started.UTC().Format(time.RFC3339)
	b.CompletedAt = stamp(completed)
	b.VerifiedAt = stamp(verified)
	b.Timeline = int32p(timeline)
	return b, nil
}

// ListBases reads the base backup history, newest first.
func (r *WALRegister) ListBases(
	ctx context.Context, limit int,
) ([]BaseRecord, error) {
	if !r.ok() {
		return nil, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	var out []BaseRecord
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx,
			`SELECT `+baseColumns+` FROM base_backup
			  ORDER BY started_at DESC LIMIT $1`, limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			b, e := scanBase(rows)
			if e != nil {
				return e
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "The base backups could not be listed.")
}

// --- recoveries -------------------------------------------------------------

// StartRecovery opens the audit row before the recovery.
func (r *WALRegister) StartRecovery(
	ctx context.Context, kind, baseID string, target RecoveryTarget,
	by *uuid.UUID, byLabel string,
) (uuid.UUID, error) {
	if !r.ok() {
		return uuid.Nil, nil
	}
	value := target.Value
	if target.WantsMoment() {
		value = target.At.UTC().Format(time.RFC3339)
	}
	kindOf := target.Kind
	if kindOf == "" {
		kindOf = TargetLatest
	}
	var id uuid.UUID
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO pitr_recovery
			  (kind, base_id, target_kind, target_value, status,
			   requested_by, requested_by_label)
			VALUES ($1, nullif($2,''), $3, nullif($4,''), 'running', $5,
			        nullif($6,''))
			RETURNING id`,
			kind, baseID, kindOf, value, by, clipText(byLabel, 200)).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, db.Translate(err,
			"That recovery could not be recorded.")
	}
	return id, nil
}

// FinishRecovery closes the audit row.
func (r *WALRegister) FinishRecovery(
	ctx context.Context, id uuid.UUID, report PITRReport, runErr error,
) error {
	if !r.ok() || id == uuid.Nil {
		return nil
	}
	status := "succeeded"
	reason := ""
	if runErr != nil {
		status = "failed"
		reason = clipText(runErr.Error(), 2000)
	} else if !report.Passed {
		status = "failed"
		reason = "The recovery completed and the result did not check out. " +
			"See the report."
		if len(report.Findings) > 0 {
			reason = clipText(report.Findings[0], 2000)
		}
	}
	body, err := json.Marshal(report)
	if err != nil {
		body = []byte("{}")
	}
	var reached *time.Time
	if report.ReachedTime != "" {
		if t, e := time.Parse(time.RFC3339, report.ReachedTime); e == nil {
			u := t.UTC()
			reached = &u
		}
	}
	e := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE pitr_recovery SET
			  status = $2, finished_at = now(),
			  base_id = coalesce(nullif($3,''), base_id),
			  reached_lsn = nullif($4,''), reached_at = $5, timeline = $6,
			  report = $7, error = nullif($8,'')
			WHERE id = $1`,
			id, status, report.BaseBackupID, report.ReachedLSN, reached,
			int32(report.Timeline), body, reason)
		return err
	})
	return db.Translate(e, "")
}

// RefuseRecovery closes the audit row for one that was never attempted.
//
// Recorded, because "somebody asked to recover every business to last Tuesday
// and was refused" is as much a thing an investigation needs as a recovery that
// happened.
func (r *WALRegister) RefuseRecovery(
	ctx context.Context, id uuid.UUID, reason string,
) error {
	if !r.ok() || id == uuid.Nil {
		return nil
	}
	if reason == "" {
		reason = "It was refused and no reason was recorded."
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE pitr_recovery
			   SET status = 'refused', finished_at = now(), error = $2
			 WHERE id = $1`, id, clipText(reason, 2000))
		return e
	})
	return db.Translate(err, "")
}

// RecoveryRecord is one audited recovery.
type RecoveryRecord struct {
	ID          uuid.UUID `json:"id"`
	Kind        string    `json:"kind"`
	BaseID      string    `json:"base_backup_id,omitempty"`
	TargetKind  string    `json:"target_kind"`
	TargetValue string    `json:"target_value,omitempty"`

	ReachedLSN string `json:"reached_lsn,omitempty"`
	ReachedAt  string `json:"reached_at,omitempty"`
	Timeline   int    `json:"timeline,omitempty"`

	Status      string `json:"status"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at,omitempty"`
	RequestedBy string `json:"requested_by,omitempty"`
	Error       string `json:"error,omitempty"`

	Report json.RawMessage `json:"report,omitempty"`
}

// ListRecoveries reads the recovery history, newest first.
func (r *WALRegister) ListRecoveries(
	ctx context.Context, limit int,
) ([]RecoveryRecord, error) {
	if !r.ok() {
		return nil, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	var out []RecoveryRecord
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, kind, coalesce(base_id,''), target_kind,
			       coalesce(target_value,''), coalesce(reached_lsn,''),
			       reached_at, timeline, status, started_at, finished_at,
			       coalesce(requested_by_label,''), coalesce(error,'')
			  FROM pitr_recovery ORDER BY started_at DESC LIMIT $1`, limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var rec RecoveryRecord
			var started time.Time
			var reached, finished *time.Time
			var timeline *int32
			if e := rows.Scan(&rec.ID, &rec.Kind, &rec.BaseID, &rec.TargetKind,
				&rec.TargetValue, &rec.ReachedLSN, &reached, &timeline,
				&rec.Status, &started, &finished, &rec.RequestedBy,
				&rec.Error); e != nil {
				return e
			}
			rec.StartedAt = started.UTC().Format(time.RFC3339)
			rec.ReachedAt = stamp(reached)
			rec.FinishedAt = stamp(finished)
			rec.Timeline = int32p(timeline)
			out = append(out, rec)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "The recoveries could not be listed.")
}

// ActiveRecovery reports whether one is already running.
//
// What stops a screen queueing a second recovery while the first is still
// going. The database refuses it anyway — 0136 extends the one-heavy-task
// index to cover this — but a refusal an operator can read before they press
// the button is worth more than a constraint violation after it.
func (r *WALRegister) ActiveRecovery(ctx context.Context) (bool, error) {
	if !r.ok() {
		return false, nil
	}
	var found bool
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM pitr_recovery
			                WHERE status = 'running'
			                  AND started_at > now() - interval '6 hours')`).
			Scan(&found)
	})
	return found, db.Translate(err, "")
}

// --- small conversions ------------------------------------------------------

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func int32p(v *int32) int {
	if v == nil {
		return 0
	}
	return int(*v)
}
