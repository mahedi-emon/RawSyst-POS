// The queue between somebody pressing a button and something dumping a
// database.
//
// # Why this is not the job queue
//
// `internal/jobs` already exists and is the right place for work that must be
// atomic with its trigger. This is not that, for two reasons.
//
// The first is that backup work needs `pg_dump` and `pg_restore`, and those
// come from the postgres image. The API and the worker are built on `scratch`
// and are about twenty megabytes; putting the PostgreSQL client tools in them
// to run one command a day would be the wrong trade, and running the general
// worker in the postgres image would mean every ZATCA submission carried a
// hundred megabytes of unrelated software. So a separate small agent claims
// this work, and a second drainer of `job` would claim submissions it cannot
// run and fail them.
//
// The second is that this work is WATCHED. Somebody presses CREATE BACKUP and
// looks at a screen. What they need is the truthful name of what is happening
// now — dumping, uploading, verifying — and a job row's `running` does not say
// it. `stage` does, and the agent writes it as it goes.
//
// # One heavy task at a time
//
// Enforced by a partial unique index in 0135, not by this code. Two `pg_dump`s
// on two cores is an outage caused by the thing that exists to prevent one, and
// a rule the database keeps holds however many agents are running.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// The kinds of work the agent does.
const (
	TaskCreate            = "create"
	TaskVerify            = "verify"
	TaskRestoreValidate   = "restore_validate"
	TaskRestoreProduction = "restore_production"
	TaskPrune             = "prune"

	// The point-in-time recovery half, added in 0136. `base_backup` takes a
	// physical copy of the cluster; `pitr_restore` recovers one to a moment
	// inside an isolated PostgreSQL. Both are heavy and the database allows one
	// heavy task at a time across all of them.
	TaskBaseBackup  = "base_backup"
	TaskPITRRestore = "pitr_restore"

	// These two are listings and small reads, so they are deliberately NOT
	// heavy: blocking a retention run behind a two-hour base backup would mean
	// the disk fills while the policy waits.
	TaskWALVerify = "wal_verify"
	TaskWALPrune  = "wal_prune"
)

// Task states.
const (
	TaskQueued    = "queued"
	TaskRunning   = "running"
	TaskDone      = "done"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"
)

// Task is one piece of work.
type Task struct {
	ID         uuid.UUID  `json:"id"`
	Kind       string     `json:"kind"`
	SnapshotID string     `json:"snapshot_id,omitempty"`
	BackupID   *uuid.UUID `json:"backup_id,omitempty"`

	State string `json:"state"`
	Stage string `json:"stage"`

	RequestedBy string `json:"requested_by,omitempty"`
	RequestedAt string `json:"requested_at"`
	StartedAt   string `json:"started_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`

	Attempts int             `json:"attempts"`
	Params   json.RawMessage `json:"params,omitempty"`
	Report   json.RawMessage `json:"report,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// decodeParams reads back what the request asked for.
//
// A task row with unreadable parameters is a task this build cannot run, and
// saying so is better than running it with the zero value — which for a
// recovery would mean silently recovering to `latest` instead of to the moment
// somebody typed.
func (t Task) decodeParams() (Params, error) {
	var p Params
	if len(t.Params) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(t.Params, &p); err != nil {
		return p, errs.Wrap(err, errs.CodeInvalidInput,
			"That task was queued with parameters this build cannot read. It "+
				"was almost certainly queued by a different version.")
	}
	return p, nil
}

// Tasks is the queue.
type Tasks struct{ pool *db.Pool }

// NewTasks builds the queue.
func NewTasks(pool *db.Pool) *Tasks { return &Tasks{pool: pool} }

// Params are what a task needs beyond its kind.
//
// Never a credential. The agent reads its database and storage credentials from
// its own environment; a task row is readable by every platform operator and
// is written by an HTTP request.
type Params struct {
	// Confirm is what the operator typed to authorise a production restore.
	Confirm string `json:"confirm,omitempty"`

	// SafetyBackup is the verified backup of what production holds right now.
	// Filled by the agent when it takes one, so the report says which.
	SafetyBackup string `json:"safety_backup,omitempty"`

	// FromUpload marks a snapshot that is staged on disk rather than in the
	// object store, because it arrived from somebody's computer.
	FromUpload bool   `json:"from_upload,omitempty"`
	StagedAt   string `json:"staged_at,omitempty"`

	// --- point-in-time recovery -------------------------------------------

	// BaseBackupID is which physical copy a recovery starts from. Empty means
	// the agent picks the newest one that finished before the target, which is
	// almost always what somebody means and is always what they want when they
	// have not thought about it.
	BaseBackupID string `json:"base_backup_id,omitempty"`

	// The recovery target, as three fields rather than as the `RecoveryTarget`
	// struct. Deliberate: this JSON is written by an HTTP handler and read by
	// a different build of the agent, and three scalar fields with a validator
	// on each side survive that better than a nested object whose shape both
	// ends have to agree on.
	TargetKind  string `json:"target_kind,omitempty"`
	TargetValue string `json:"target_value,omitempty"`
	TargetAt    string `json:"target_at,omitempty"`
	Timeline    uint32 `json:"target_timeline,omitempty"`

	// Keep leaves the recovered cluster running so somebody can connect to it.
	// Never set by an ordinary drill.
	Keep bool `json:"keep_recovered,omitempty"`

	// DeepSample is how many segments an archive check downloads and reads.
	// Zero is the cheap check.
	DeepSample int `json:"deep_sample,omitempty"`

	// Apply turns a retention run from a report into a deletion. Absent means
	// a dry run, which is the right default for the only routine here that
	// removes the last copy of something.
	Apply bool `json:"apply,omitempty"`
}

// Target rebuilds the recovery target a request asked for.
//
// Validated by the caller: this only reassembles, so a malformed timestamp
// becomes a zero time and `RecoveryTarget.Validate` refuses it, rather than
// this silently picking a moment of its own.
func (p Params) Target() RecoveryTarget {
	t := RecoveryTarget{
		Kind:     p.TargetKind,
		Value:    p.TargetValue,
		Timeline: p.Timeline,
	}
	if t.Kind == "" {
		t.Kind = TargetLatest
	}
	if p.TargetAt != "" {
		if at, err := time.Parse(time.RFC3339, p.TargetAt); err == nil {
			t.At = at.UTC()
		}
	}
	return t
}

// Enqueue asks for work.
//
// The unique index in 0135 is what refuses a second heavy task, and the error
// it raises is turned into a sentence here rather than into a constraint name.
func (t *Tasks) Enqueue(
	ctx context.Context, kind, snapshotID string,
	by *uuid.UUID, byLabel string, params Params,
) (Task, error) {
	if t == nil || t.pool == nil {
		return Task{}, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	switch kind {
	case TaskCreate, TaskVerify, TaskRestoreValidate,
		TaskRestoreProduction, TaskPrune,
		TaskBaseBackup, TaskPITRRestore, TaskWALVerify, TaskWALPrune:
	default:
		return Task{}, errs.Newf(errs.CodeInvalidInput,
			"There is no backup operation called %q.", kind)
	}
	switch kind {
	case TaskCreate, TaskPrune, TaskBaseBackup, TaskWALVerify, TaskWALPrune:
		// About everything, or about something that does not exist yet.
	case TaskPITRRestore:
		// A recovery may name the base backup to start from and may leave the
		// choice to the agent, which picks the newest one that finished before
		// the target. An id that IS given has to be one.
		if snapshotID != "" && !ValidSnapshotID(snapshotID) {
			return Task{}, errs.New(errs.CodeInvalidInput,
				"That is not a base backup id.")
		}
	default:
		if !ValidSnapshotID(snapshotID) {
			return Task{}, errs.New(errs.CodeInvalidInput,
				"That is not a snapshot id.")
		}
	}

	body, err := json.Marshal(params)
	if err != nil {
		return Task{}, errs.Wrap(err, errs.CodeInternal,
			"That request could not be prepared.")
	}

	var out Task
	e := t.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO backup_task
			  (kind, snapshot_id, requested_by, requested_by_label, params,
			   backup_id)
			VALUES ($1, nullif($2,''), $3, nullif($4,''), $5,
			        (SELECT id FROM backup_record
			          WHERE tenant_id IS NULL AND snapshot_id = nullif($2,'')
			          ORDER BY started_at DESC LIMIT 1))
			RETURNING `+taskColumns, kind, snapshotID, by, byLabel, body)
		var e error
		out, e = scanTask(row)
		return e
	})
	if e != nil {
		var pg *pgconn.PgError
		if errors.As(e, &pg) && pg.Code == "23505" {
			return Task{}, errs.New(errs.CodeConflict,
				"Another backup operation is already queued or running. This "+
					"server runs one at a time on purpose: two dumps at once "+
					"on two cores is an outage caused by the thing that is "+
					"supposed to prevent one. Wait for it to finish.")
		}
		return Task{}, db.Translate(e, "That request could not be queued.")
	}
	return out, nil
}

const taskColumns = `
	id, kind, coalesce(snapshot_id,''), backup_id, state, stage,
	coalesce(requested_by_label,''), requested_at, started_at, finished_at,
	attempts, params, report, coalesce(error,'')`

func scanTask(row pgx.Row) (Task, error) {
	var t Task
	var started, finished *time.Time
	var requested time.Time
	var params, report []byte
	if err := row.Scan(&t.ID, &t.Kind, &t.SnapshotID, &t.BackupID, &t.State,
		&t.Stage, &t.RequestedBy, &requested, &started, &finished,
		&t.Attempts, &params, &report, &t.Error); err != nil {
		return Task{}, err
	}
	t.RequestedAt = requested.UTC().Format(time.RFC3339)
	if started != nil {
		t.StartedAt = started.UTC().Format(time.RFC3339)
	}
	if finished != nil {
		t.FinishedAt = finished.UTC().Format(time.RFC3339)
	}
	t.Params = params
	t.Report = report
	return t, nil
}

// Claim takes the oldest queued task, if there is one.
//
// `FOR UPDATE SKIP LOCKED` so two agents cannot take the same row, which is a
// property worth having even though only one agent should be running: "should"
// is doing a lot of work in that sentence during a deployment.
func (t *Tasks) Claim(ctx context.Context, worker string) (Task, bool, error) {
	if t == nil || t.pool == nil {
		return Task{}, false, errs.New(errs.CodeUnavailable,
			"No database connection.")
	}
	var out Task
	var found bool
	err := t.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE backup_task SET
			  state = 'running', claimed_by = $1, claimed_at = now(),
			  started_at = now(), heartbeat_at = now(),
			  attempts = attempts + 1, stage = 'preparing'
			WHERE id = (
			  SELECT id FROM backup_task WHERE state = 'queued'
			  ORDER BY requested_at LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING `+taskColumns, worker)
		var e error
		out, e = scanTask(row)
		if e == pgx.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		found = true
		return nil
	})
	if err != nil {
		return Task{}, false, db.Translate(err, "")
	}
	return out, found, nil
}

// Stage records the truthful word for what is happening now.
//
// Also a heartbeat: a task whose stage has not moved in a long time is one
// whose agent has gone, and `Reap` uses that.
func (t *Tasks) Stage(ctx context.Context, id uuid.UUID, stage string) error {
	if t == nil || t.pool == nil || id == uuid.Nil {
		return nil
	}
	err := t.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE backup_task SET stage = $2, heartbeat_at = now()
			WHERE id = $1 AND state = 'running'`, id, stage)
		return e
	})
	return db.Translate(err, "")
}

// SetParams writes back what the agent learned — chiefly the safety backup it
// took before a production restore, so the report says which one.
func (t *Tasks) SetParams(
	ctx context.Context, id uuid.UUID, params Params,
) error {
	if t == nil || t.pool == nil || id == uuid.Nil {
		return nil
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	e := t.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE backup_task SET params = $2 WHERE id = $1`, id, body)
		return err
	})
	return db.Translate(e, "")
}

// Finish closes a task, succeeded or failed.
func (t *Tasks) Finish(
	ctx context.Context, id uuid.UUID, report any, failure string,
) error {
	if t == nil || t.pool == nil || id == uuid.Nil {
		return nil
	}
	body, err := json.Marshal(report)
	if err != nil {
		body = []byte("null")
	}
	state, stage := TaskDone, StageDone
	if failure != "" {
		state, stage = TaskFailed, "failed"
	}
	if failure == "" {
		failure = ""
	}
	e := t.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE backup_task
			SET state = $2, stage = $3, report = $4, error = nullif($5,''),
			    finished_at = now(), heartbeat_at = now()
			WHERE id = $1`, id, state, stage, body, clipText(failure, 4000))
		return err
	})
	return db.Translate(e, "That task could not be closed.")
}

// Reap fails tasks whose agent has stopped reporting.
//
// A task stuck in `running` for ever holds the one-at-a-time index and blocks
// every future backup, which turns a crashed agent into a business with no
// backups at all. `after` must be longer than the slowest legitimate task or a
// working backup would be declared abandoned while it is still going.
func (t *Tasks) Reap(ctx context.Context, after time.Duration) (int, error) {
	if t == nil || t.pool == nil {
		return 0, nil
	}
	var n int
	err := t.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE backup_task
			SET state = 'failed', stage = 'failed', finished_at = now(),
			    error = 'The agent running this stopped reporting. Whatever '
			         || 'it was doing did not finish, and nothing about it '
			         || 'should be treated as complete.'
			WHERE state = 'running'
			  AND coalesce(heartbeat_at, started_at, requested_at)
			      < now() - $1::interval`, after.String())
		if e != nil {
			return e
		}
		n = int(tag.RowsAffected())
		return nil
	})
	return n, db.Translate(err, "")
}

// List reports recent tasks, newest first.
func (t *Tasks) List(ctx context.Context, limit int) ([]Task, error) {
	if t == nil || t.pool == nil {
		return nil, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []Task{}
	err := t.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT `+taskColumns+
			` FROM backup_task ORDER BY requested_at DESC LIMIT $1`, limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			task, e := scanTask(rows)
			if e != nil {
				return e
			}
			out = append(out, task)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// Get reads one task, which is what a screen watching a backup polls.
func (t *Tasks) Get(ctx context.Context, id uuid.UUID) (Task, error) {
	if t == nil || t.pool == nil {
		return Task{}, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	var out Task
	err := t.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+taskColumns+` FROM backup_task WHERE id = $1`, id)
		var e error
		out, e = scanTask(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "No such operation.")
		}
		return e
	})
	if err != nil {
		return Task{}, db.Translate(err, "")
	}
	return out, nil
}

// Active is the heavy task currently queued or running, if there is one.
//
// The screen uses it to say "a backup is already running" instead of offering a
// button that will answer 409.
func (t *Tasks) Active(ctx context.Context) (Task, bool, error) {
	if t == nil || t.pool == nil {
		return Task{}, false, nil
	}
	var out Task
	var found bool
	err := t.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+taskColumns+` FROM backup_task
			WHERE state IN ('queued','running')
			ORDER BY requested_at LIMIT 1`)
		var e error
		out, e = scanTask(row)
		if e == pgx.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		found = true
		return nil
	})
	if err != nil {
		return Task{}, false, db.Translate(err, "")
	}
	return out, found, nil
}
