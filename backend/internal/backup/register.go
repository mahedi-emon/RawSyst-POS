// Writing what happened into the register the product already has.
//
// `backup_record` has existed since 0093, with the distinction that matters
// already in it: `status` says whether a run finished, and `verified_at` says
// whether anybody proved it restores. Those are separate columns because they
// are separate claims, and the second is the one worth reading. 0135 added the
// rest of a backup's life — which snapshot it is, where it came from, what
// phase it is in, and the two documents somebody reads before trusting it.
//
// # Platform rows, not tenant rows
//
// A dump of this database is every tenant at once, so the row belongs to none
// of them and `tenant_id` is NULL. The policy on the table already reads
// `tenant_id = current_tenant_id() OR is_platform_admin()`, so a platform
// operator sees these and a business sees only its own. That is the right
// division: a shop should not be shown the platform's backup as though it were
// theirs, and should not be told it is unprotected because it cannot see one.
package backup

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// Phases, as the database constrains them and the screen shows them.
const (
	PhasePending           = "pending"
	PhaseCreating          = "creating"
	PhaseUploading         = "uploading"
	PhaseUploaded          = "uploaded"
	PhaseVerifying         = "verifying"
	PhaseVerified          = "verified"
	PhaseInvalid           = "invalid"
	PhaseFailed            = "failed"
	PhaseRestoreValidating = "restore_validating"
	PhaseRestoreReady      = "restore_ready"
	PhaseRestoring         = "restoring"
	PhaseRestored          = "restored"
	PhaseRestoreFailed     = "restore_failed"
)

// Register records runs and verifications against the platform.
type Register struct{ pool *db.Pool }

// NewRegister builds the register.
func NewRegister(pool *db.Pool) *Register { return &Register{pool: pool} }

// Record is one backup as the register holds it.
type Record struct {
	ID         uuid.UUID `json:"id"`
	SnapshotID string    `json:"snapshot_id,omitempty"`

	Kind   string `json:"kind"`
	Source string `json:"source"`
	Status string `json:"status"`
	Phase  string `json:"phase"`

	Location string `json:"location,omitempty"`
	Storage  string `json:"storage,omitempty"`
	Size     *int64 `json:"size_bytes,omitempty"`
	Checksum string `json:"checksum,omitempty"`

	AppVersion     string `json:"app_version,omitempty"`
	SchemaVersion  *int   `json:"schema_version,omitempty"`
	RetentionClass string `json:"retention_class,omitempty"`
	Encrypted      bool   `json:"encrypted"`

	VerifiedAt  string `json:"verified_at,omitempty"`
	VerifyError string `json:"verify_error,omitempty"`

	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`

	RequestedBy string `json:"requested_by,omitempty"`

	// Manifest and VerifyReport are only filled by `Get`. A history of thirty
	// backups, each carrying a full inventory, is a megabyte of JSON to render
	// a table of dates.
	Manifest     json.RawMessage `json:"manifest,omitempty"`
	VerifyReport json.RawMessage `json:"verify_report,omitempty"`
}

const recordColumns = `
	b.id, b.snapshot_id, b.kind, b.source, b.status, b.phase,
	b.location, b.storage, b.size_bytes, b.checksum,
	b.app_version, b.schema_version, b.retention_class, b.encrypted,
	b.verified_at, b.verify_error, b.started_at, b.finished_at, b.error,
	coalesce(u.full_name, u.email, '')`

const recordFrom = `
	FROM backup_record b
	LEFT JOIN app_user u ON u.id = b.requested_by
	WHERE b.tenant_id IS NULL`

func scanRecord(row pgx.Row) (Record, error) {
	var r Record
	var snapshot, location, storage, checksum, appVersion *string
	var retention, verifyError, failure *string
	var size *int64
	var schema *int
	var verifiedAt, finishedAt *time.Time
	var startedAt time.Time

	if err := row.Scan(&r.ID, &snapshot, &r.Kind, &r.Source, &r.Status, &r.Phase,
		&location, &storage, &size, &checksum,
		&appVersion, &schema, &retention, &r.Encrypted,
		&verifiedAt, &verifyError, &startedAt, &finishedAt, &failure,
		&r.RequestedBy); err != nil {
		return Record{}, err
	}

	r.SnapshotID = text(snapshot)
	r.Location = text(location)
	r.Storage = text(storage)
	r.Checksum = text(checksum)
	r.AppVersion = text(appVersion)
	r.RetentionClass = text(retention)
	r.VerifyError = text(verifyError)
	r.Error = text(failure)
	r.Size = size
	r.SchemaVersion = schema
	r.StartedAt = startedAt.UTC().Format(time.RFC3339)
	if verifiedAt != nil {
		r.VerifiedAt = verifiedAt.UTC().Format(time.RFC3339)
	}
	if finishedAt != nil {
		r.FinishedAt = finishedAt.UTC().Format(time.RFC3339)
	}
	return r, nil
}

func text(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Start opens a row before the work, and returns its id.
//
// Before rather than after, so a backup that dies halfway leaves something
// somebody can see. A record written only on success makes a crashed backup
// indistinguishable from one nobody ever scheduled — which is the failure this
// whole subsystem exists to make impossible.
func (r *Register) Start(
	ctx context.Context, kind, source string, by *uuid.UUID,
) (uuid.UUID, error) {
	if r == nil || r.pool == nil {
		return uuid.Nil, nil
	}
	switch kind {
	case "scheduled", "manual", "uploaded":
	default:
		kind = "manual"
	}
	if source != "upload" {
		source = "server"
	}
	var id uuid.UUID
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO backup_record
			  (tenant_id, kind, source, status, phase, requested_by)
			VALUES (NULL, $1, $2, 'running', 'creating', $3)
			RETURNING id`, kind, source, by).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, db.Translate(err, "That backup could not be recorded.")
	}
	return id, nil
}

// Phase moves a record to a new phase without claiming anything else about it.
func (r *Register) Phase(ctx context.Context, id uuid.UUID, phase string) error {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE backup_record SET phase = $2 WHERE id = $1`, id, phase)
		return e
	})
	return db.Translate(err, "")
}

// Succeeded closes a row for a run that produced a snapshot.
//
// The phase it lands in is `uploaded`, not `verified`. The artifact is in the
// store and NOTHING has been proved about it: that is the distinction this
// whole file exists to keep, and the two words are one column apart so that a
// screen cannot accidentally show the more comforting one.
func (r *Register) Succeeded(
	ctx context.Context, id uuid.UUID, res Result, storage string,
) error {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil
	}
	manifest, err := json.Marshal(res.Manifest)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The manifest could not be recorded.")
	}
	encrypted := res.Manifest.Encryption != nil
	e := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE backup_record
			SET status = 'succeeded', phase = 'uploaded',
			    snapshot_id = $2, location = nullif($3,''),
			    checksum = nullif($4,''), size_bytes = nullif($5, 0),
			    app_version = nullif($6,''), schema_version = $7,
			    retention_class = nullif($8,''), encrypted = $9,
			    storage = nullif($10,''), manifest = $11,
			    error = NULL, finished_at = now()
			WHERE id = $1`,
			id, res.SnapshotID, res.Location, res.SHA256, res.Bytes,
			res.Manifest.AppVersion, res.Manifest.SchemaVersion,
			res.Manifest.RetentionClass, encrypted, storage, manifest)
		return err
	})
	return db.Translate(e, "That backup could not be closed.")
}

// Failed closes a row that did not produce a usable snapshot.
//
// A failed row must say why — the table's own constraint insists on it —
// because a failure with no reason is a row that gets ignored.
func (r *Register) Failed(ctx context.Context, id uuid.UUID, reason string) error {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil
	}
	if reason == "" {
		reason = "The run failed and gave no reason, which is itself the fault."
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE backup_record
			SET status = 'failed', phase = 'failed', error = $2,
			    finished_at = now()
			WHERE id = $1`, id, clipText(reason, 4000))
		return e
	})
	return db.Translate(err, "That backup could not be closed.")
}

// Uploaded records a backup that arrived from somebody's computer.
//
// It exists as its own call because the provenance is different in a way that
// matters: nothing here was taken by this server, nothing about it has been
// proved, and the phase it lands in says so.
func (r *Register) Uploaded(
	ctx context.Context, snapshotID, location, checksum string,
	size int64, manifest []byte, encrypted bool, by *uuid.UUID,
) (uuid.UUID, error) {
	if r == nil || r.pool == nil {
		return uuid.Nil, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	var id uuid.UUID
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// A re-upload of something already uploaded replaces it, which is what
		// somebody retrying a failed transfer wants. A snapshot this server
		// TOOK is never overwritten by an upload claiming the same id: the
		// server's own copy is the one whose provenance is known, and letting
		// an upload displace it would make a record about a file in a bucket
		// point at a file on a disk.
		err := tx.QueryRow(ctx, `
			INSERT INTO backup_record
			  (tenant_id, kind, source, status, phase, snapshot_id, location,
			   checksum, size_bytes, manifest, encrypted, storage,
			   requested_by, finished_at)
			VALUES (NULL, 'uploaded', 'upload', 'succeeded', 'uploaded',
			        $1, $2, $3, $4, $5, $6, 'staging', $7, now())
			ON CONFLICT (snapshot_id) WHERE snapshot_id IS NOT NULL
			DO UPDATE SET location = EXCLUDED.location,
			              checksum = EXCLUDED.checksum,
			              size_bytes = EXCLUDED.size_bytes,
			              manifest = EXCLUDED.manifest,
			              encrypted = EXCLUDED.encrypted,
			              phase = 'uploaded', status = 'succeeded',
			              verified_at = NULL, verify_error = NULL,
			              error = NULL, finished_at = now()
			WHERE backup_record.source = 'upload'
			RETURNING id`,
			snapshotID, location, checksum, size, manifest, encrypted, by).
			Scan(&id)
		if err == pgx.ErrNoRows {
			return errs.Newf(errs.CodeConflict,
				"This server already holds a backup called %s that it took "+
					"itself, and an upload will not displace it. If you meant "+
					"to restore from that one, it is already here; if this is "+
					"a different backup, it is from a different installation "+
					"and its manifest says so.", snapshotID)
		}
		return err
	})
	if err != nil {
		return uuid.Nil, db.Translate(err, "That upload could not be recorded.")
	}
	return id, nil
}

// Verified stamps a row that has actually been restored, or records why not.
//
// A failed verification records the reason, leaves `verified_at` NULL and puts
// the row in `invalid`. A broken backup that stamped itself verified would read
// as protection, which is worse than no backup at all because nobody would look
// for one.
func (r *Register) Verified(
	ctx context.Context, id uuid.UUID, report any, failure string,
) error {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil
	}
	body, err := json.Marshal(report)
	if err != nil {
		body = []byte("null")
	}
	e := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if failure != "" {
			_, err := tx.Exec(ctx, `
				UPDATE backup_record
				SET verify_error = $2, verified_at = NULL, phase = 'invalid',
				    verify_report = $3
				WHERE id = $1`, id, clipText(failure, 4000), body)
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE backup_record
			SET verified_at = now(), verify_error = NULL, phase = 'verified',
			    verify_report = $2
			WHERE id = $1`, id, body)
		return err
	})
	return db.Translate(e, "That verification could not be recorded.")
}

// Validated records a restore rehearsal.
//
// A pass moves the row to `restore_ready`, which is the phase a production
// restore requires. A failure moves it to `invalid`: a snapshot that would not
// come back in a rehearsal is not one to try on the live database.
func (r *Register) Validated(
	ctx context.Context, id uuid.UUID, report any, failure string,
) error {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil
	}
	body, err := json.Marshal(report)
	if err != nil {
		body = []byte("null")
	}
	e := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if failure != "" {
			_, err := tx.Exec(ctx, `
				UPDATE backup_record
				SET phase = 'invalid', verify_error = $2, verified_at = NULL,
				    verify_report = $3
				WHERE id = $1`, id, clipText(failure, 4000), body)
			return err
		}
		// A rehearsal that passes IS a verification: it downloaded the
		// snapshot, restored it and compared it. Stamping `verified_at` here
		// is not a shortcut, it is the same evidence.
		_, err := tx.Exec(ctx, `
			UPDATE backup_record
			SET phase = 'restore_ready', verified_at = now(),
			    verify_error = NULL, verify_report = $2
			WHERE id = $1`, id, body)
		return err
	})
	return db.Translate(e, "That validation could not be recorded.")
}

// FindBySnapshot returns the row for a snapshot id.
func (r *Register) FindBySnapshot(
	ctx context.Context, snapshotID string,
) (Record, error) {
	if r == nil || r.pool == nil {
		return Record{}, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	var out Record
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+recordColumns+recordFrom+
			` AND b.snapshot_id = $1 ORDER BY b.started_at DESC LIMIT 1`,
			snapshotID)
		var e error
		out, e = scanRecord(row)
		if e == pgx.ErrNoRows {
			return errs.Newf(errs.CodeNotFound,
				"No backup called %s is on record here.", snapshotID)
		}
		return e
	})
	if err != nil {
		return Record{}, db.Translate(err, "")
	}
	return out, nil
}

// Detail is `FindBySnapshot` with the manifest and verification report.
func (r *Register) Detail(
	ctx context.Context, snapshotID string,
) (Record, error) {
	out, err := r.FindBySnapshot(ctx, snapshotID)
	if err != nil {
		return Record{}, err
	}
	err = r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var manifest, report []byte
		if e := tx.QueryRow(ctx, `
			SELECT manifest, verify_report FROM backup_record WHERE id = $1`,
			out.ID).Scan(&manifest, &report); e != nil {
			return e
		}
		out.Manifest = manifest
		out.VerifyReport = report
		return nil
	})
	if err != nil {
		return Record{}, db.Translate(err, "")
	}
	return out, nil
}

// List reports the platform's backups, newest first.
func (r *Register) List(ctx context.Context, limit int) ([]Record, error) {
	if r == nil || r.pool == nil {
		return nil, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := []Record{}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT `+recordColumns+recordFrom+
			` ORDER BY b.started_at DESC LIMIT $1`, limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			rec, e := scanRecord(rows)
			if e != nil {
				return e
			}
			out = append(out, rec)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// Health is the one paragraph the Backup & Recovery screen leads with.
type Health struct {
	// State is GREEN, AMBER or RED, decided here so every screen that shows it
	// shows the same judgement.
	State string `json:"state"`
	Says  string `json:"summary"`

	LastVerified   string `json:"last_verified_at,omitempty"`
	LastVerifiedID string `json:"last_verified_snapshot,omitempty"`
	LastRun        string `json:"last_run_at,omitempty"`
	LastRunStatus  string `json:"last_run_status,omitempty"`
	LastFailure    string `json:"last_failure_at,omitempty"`
	LastFailureWhy string `json:"last_failure_reason,omitempty"`

	// HoursSinceVerified is absent when nothing has ever been verified, which
	// is different from zero and must never read as "just now".
	HoursSinceVerified *int `json:"hours_since_verified,omitempty"`

	Verified   int `json:"verified_count"`
	Unverified int `json:"unverified_count"`
	Failed     int `json:"failed_last_week"`
}

// States, in the order a screen colours them.
const (
	HealthGreen = "green"
	HealthAmber = "amber"
	HealthRed   = "red"
)

// StaleAfterHours is when a verified backup stops counting as current.
//
// Thirty hours rather than twenty-four: a nightly timer that runs at 03:30 and
// takes twenty minutes should not put the dashboard into amber every morning
// because the clock moved. It is a tolerance on a daily schedule, not a claim
// that a thirty-hour-old backup is as good as a fresh one.
const StaleAfterHours = 30

// State reads the register and decides.
func (r *Register) State(ctx context.Context) (Health, error) {
	if r == nil || r.pool == nil {
		return Health{}, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	var h Health
	var lastVerified, lastRun, lastFailure *time.Time
	var lastVerifiedID, lastRunStatus, lastFailureWhy *string

	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT
			  (SELECT max(verified_at) FROM backup_record
			    WHERE tenant_id IS NULL AND verified_at IS NOT NULL),
			  (SELECT snapshot_id FROM backup_record
			    WHERE tenant_id IS NULL AND verified_at IS NOT NULL
			    ORDER BY verified_at DESC LIMIT 1),
			  (SELECT max(started_at) FROM backup_record WHERE tenant_id IS NULL),
			  (SELECT status FROM backup_record WHERE tenant_id IS NULL
			    ORDER BY started_at DESC LIMIT 1),
			  (SELECT max(finished_at) FROM backup_record
			    WHERE tenant_id IS NULL AND status = 'failed'),
			  (SELECT error FROM backup_record
			    WHERE tenant_id IS NULL AND status = 'failed'
			    ORDER BY finished_at DESC NULLS LAST LIMIT 1),
			  (SELECT count(*)::int FROM backup_record
			    WHERE tenant_id IS NULL AND verified_at IS NOT NULL),
			  (SELECT count(*)::int FROM backup_record
			    WHERE tenant_id IS NULL AND status = 'succeeded'
			      AND verified_at IS NULL),
			  (SELECT count(*)::int FROM backup_record
			    WHERE tenant_id IS NULL AND status = 'failed'
			      AND started_at > now() - interval '7 days')`).
			Scan(&lastVerified, &lastVerifiedID, &lastRun, &lastRunStatus,
				&lastFailure, &lastFailureWhy,
				&h.Verified, &h.Unverified, &h.Failed)
	})
	if err != nil {
		return Health{}, db.Translate(err, "")
	}

	if lastVerified != nil {
		h.LastVerified = lastVerified.UTC().Format(time.RFC3339)
		hours := int(time.Since(*lastVerified).Hours())
		h.HoursSinceVerified = &hours
	}
	h.LastVerifiedID = text(lastVerifiedID)
	if lastRun != nil {
		h.LastRun = lastRun.UTC().Format(time.RFC3339)
	}
	h.LastRunStatus = text(lastRunStatus)
	if lastFailure != nil {
		h.LastFailure = lastFailure.UTC().Format(time.RFC3339)
	}
	h.LastFailureWhy = clipText(text(lastFailureWhy), 400)

	// The judgement, made once, here. Every screen that shows a backup state
	// shows this one, so the product has one opinion rather than one per page.
	switch {
	case h.HoursSinceVerified == nil:
		h.State = HealthRed
		h.Says = "No backup has ever been proved to restore. Until one has, " +
			"nobody knows whether this business could be brought back."
	case *h.HoursSinceVerified > StaleAfterHours*7:
		h.State = HealthRed
		h.Says = "The last backup proved to restore is more than a week old."
	case *h.HoursSinceVerified > StaleAfterHours:
		h.State = HealthAmber
		h.Says = "The last backup proved to restore is more than a day old."
	case h.Failed > 0:
		h.State = HealthAmber
		h.Says = "There is a current verified backup, and runs have been " +
			"failing: something is wrong with the arrangement even though " +
			"the last one worked."
	default:
		h.State = HealthGreen
		h.Says = "The most recent backup has been restored into a temporary " +
			"database and checked. It is the one a recovery would start from."
	}
	return h, nil
}

// LastVerified is what the hourly check and the admin screen both want to know.
type LastVerified struct {
	At       time.Time
	Location string
	Snapshot string
	Age      time.Duration
}

// Latest reads the most recent VERIFIED platform backup.
//
// Verified, not merely succeeded. The second is a more comforting number and a
// less true one, and this product has said so on the backup screen since H4.
func (r *Register) Latest(ctx context.Context) (LastVerified, error) {
	if r == nil || r.pool == nil {
		return LastVerified{}, errs.New(errs.CodeUnavailable,
			"No database connection.")
	}
	var out LastVerified
	var at *time.Time
	var location, snapshot *string
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT verified_at, location, snapshot_id FROM backup_record
			WHERE tenant_id IS NULL AND verified_at IS NOT NULL
			ORDER BY verified_at DESC LIMIT 1`).Scan(&at, &location, &snapshot)
	})
	if err != nil {
		return LastVerified{}, db.Translate(err, "")
	}
	if at != nil {
		out.At, out.Age = *at, time.Since(*at)
	}
	out.Location = text(location)
	out.Snapshot = text(snapshot)
	return out, nil
}

// Protected is the set of snapshot ids retention must never delete.
//
// The newest verified one, and the newest that is ready to restore. Retention
// works on the store and knows nothing about verification, which is recorded
// here; this is how the one fact reaches the other.
func (r *Register) Protected(ctx context.Context) ([]string, error) {
	if r == nil || r.pool == nil {
		return nil, nil
	}
	out := []string{}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			(SELECT snapshot_id FROM backup_record
			  WHERE tenant_id IS NULL AND snapshot_id IS NOT NULL
			    AND verified_at IS NOT NULL
			  ORDER BY verified_at DESC LIMIT 1)
			UNION
			(SELECT snapshot_id FROM backup_record
			  WHERE tenant_id IS NULL AND snapshot_id IS NOT NULL
			    AND phase IN ('restore_ready', 'restored')
			  ORDER BY started_at DESC LIMIT 1)`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if e := rows.Scan(&id); e != nil {
				return e
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// FreshSafetyBackup returns the snapshot id of a verified backup of what
// production holds right now, if there is one recent enough to count.
//
// "Recent enough" is a window the caller gives, because the answer depends on
// what it is for: a production restore wants one taken since the operator
// started, and a nightly report wants one taken since yesterday.
func (r *Register) FreshSafetyBackup(
	ctx context.Context, within time.Duration,
) (string, bool) {
	if r == nil || r.pool == nil {
		return "", false
	}
	var id *string
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT snapshot_id FROM backup_record
			WHERE tenant_id IS NULL AND source = 'server'
			  AND snapshot_id IS NOT NULL AND verified_at IS NOT NULL
			  AND started_at > now() - $1::interval
			ORDER BY verified_at DESC LIMIT 1`,
			within.String()).Scan(&id)
	})
	if err != nil || id == nil {
		return "", false
	}
	return *id, true
}

func clipText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// RecordRestore writes down that this database was replaced, into the database
// that replaced it.
//
// # Why this is not just an UPDATE
//
// A production restore ends by renaming the live database aside and renaming
// the restored one into its place. Everything written about the restore up to
// that moment — the task row, the phase changes, the audit entries — is in the
// database that has just been renamed aside. The database now serving is a copy
// of a backup, and it has never heard of the operation that installed it.
//
// So the first thing that happens after a cutover is this: a row in the new
// database saying what it is, where it came from, and where the database it
// replaced can be found. Without it, an operator looking at a freshly restored
// system sees no evidence that a restore ever happened, which is the worst
// possible state for a system somebody has just intervened in.
//
// The name of the previous database is the most important field here. It is the
// rollback.
func (r *Register) RecordRestore(
	ctx context.Context, snapshotID string, report any, by *uuid.UUID,
) error {
	if r == nil || r.pool == nil {
		return nil
	}
	body, err := json.Marshal(report)
	if err != nil {
		body = []byte("null")
	}
	e := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// A restored database carries whatever was in flight when the backup
		// was taken. Any task it thinks is queued or running belongs to a
		// previous life of this database and nothing is working on it — and
		// one of them holds the index that allows a single heavy task, so
		// leaving it there means no backup can ever be taken again on the
		// machine that has just been recovered.
		if _, err := tx.Exec(ctx, `
			UPDATE backup_task
			SET state = 'failed', stage = 'failed', finished_at = now(),
			    error = 'This came back with a restored database. Whatever '
			         || 'was running when the backup was taken stopped when '
			         || 'the backup was taken; nothing is working on it.'
			WHERE state IN ('queued', 'running')`); err != nil {
			return err
		}

		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO backup_record
			  (tenant_id, kind, source, status, phase, snapshot_id,
			   verify_report, requested_by, finished_at)
			VALUES (NULL, 'manual', 'server', 'succeeded', 'restored', $1, $2,
			        $3, now())
			ON CONFLICT (snapshot_id) WHERE snapshot_id IS NOT NULL
			DO UPDATE SET phase = 'restored', verify_report = EXCLUDED.verify_report,
			              finished_at = now()
			RETURNING id`, snapshotID, body, by).Scan(&id); err != nil {
			return err
		}
		// A task row too, so the operation appears in the history the screen
		// reads rather than only in the audit trail.
		_, err := tx.Exec(ctx, `
			INSERT INTO backup_task
			  (kind, snapshot_id, backup_id, state, stage, requested_by,
			   requested_by_label, started_at, finished_at, report)
			VALUES ('restore_production', $1, $2, 'done', 'done', $3,
			        'recorded after the cutover', now(), now(), $4)`,
			snapshotID, id, by, body)
		return err
	})
	return db.Translate(e, "The restore could not be recorded.")
}
