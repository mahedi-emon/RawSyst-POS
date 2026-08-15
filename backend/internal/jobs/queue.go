// Package jobs is the asynchronous side of the system.
//
// # What belongs here
//
// Design document 08 draws the line: anything that must be atomic with its
// trigger stays synchronous and inside the originating transaction — journal
// postings, stock movements, audit entries. What lands here is work that can
// retry independently and must survive a restart: ZATCA submission, reports,
// notifications, nightly integrity checks.
//
// The guarantee is at-least-once, not exactly-once. Handlers must therefore be
// idempotent, and the ones that touch money already are: posting is keyed on
// (source, source_id, rule_key) and a sale on its invoice UUID.
//
// # Why the queue is a table
//
// A job enqueued in the same transaction as its trigger cannot be orphaned by a
// crash between the two writes. An invoice that exists but was never queued for
// submission is a legal exposure nobody would notice until ZATCA did.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Job is a unit of queued work.
type Job struct {
	ID       uuid.UUID
	TenantID *uuid.UUID
	Kind     string
	Payload  json.RawMessage

	QueueKey    string
	Attempts    int
	MaxAttempts int
}

// Spec describes a job to enqueue.
type Spec struct {
	TenantID *uuid.UUID
	Kind     string
	Payload  any

	// QueueKey serialises jobs that must not run in parallel. Everything
	// sharing a key runs strictly in sequence, which is how ZATCA submission
	// keeps ICV order across any number of workers.
	QueueKey string

	Priority int

	// MaxAttempts of zero means unlimited, which ZATCA submission uses: an
	// unreported invoice does not stop being a legal exposure after
	// twenty-five tries.
	MaxAttempts int

	RunAfter time.Time

	// DedupeKey stops the same logical job being queued twice while one is
	// still outstanding.
	DedupeKey string
}

// Queue enqueues and claims work.
type Queue struct {
	pool *db.Pool
}

func NewQueue(pool *db.Pool) *Queue { return &Queue{pool: pool} }

// EnqueueIn adds a job inside the CALLER's transaction.
//
// This is the form that matters. Queuing a ZATCA submission in the same
// transaction that wrote the invoice means the two cannot disagree: either both
// exist or neither does. A queue in another process could not offer that.
func EnqueueIn(ctx context.Context, tx pgx.Tx, s Spec) error {
	payload, err := json.Marshal(s.Payload)
	if err != nil {
		return errs.Newf(errs.CodeInternal,
			"A %s job could not be prepared.", s.Kind)
	}
	if s.Payload == nil {
		payload = []byte("{}")
	}

	priority := s.Priority
	if priority == 0 {
		priority = 100
	}
	runAfter := s.RunAfter
	if runAfter.IsZero() {
		runAfter = time.Now().UTC()
	}

	// ON CONFLICT DO NOTHING against the dedupe index: a second enqueue while
	// one is outstanding is a no-op rather than an error, because the caller's
	// intent — "make sure this happens" — is already satisfied.
	_, err = tx.Exec(ctx, `
		INSERT INTO job
		  (tenant_id, kind, payload, queue_key, priority, max_attempts,
		   run_after, dedupe_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (dedupe_key)
		  WHERE dedupe_key IS NOT NULL AND state IN ('pending','running')
		  DO NOTHING`,
		s.TenantID, s.Kind, payload, nullText(s.QueueKey), priority,
		s.MaxAttempts, runAfter, nullText(s.DedupeKey))
	if err != nil {
		return db.Translate(err, "That background job could not be queued.")
	}
	return nil
}

// Enqueue adds a job in its own transaction, for callers with nothing to be
// atomic with — a scheduler, an operator.
func (q *Queue) Enqueue(ctx context.Context, s Spec) error {
	return q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return EnqueueIn(ctx, tx, s)
	})
}

// Claim takes the next runnable job, or returns false if there is none.
//
// Runs as the platform: the worker drains every tenant's queue and a job row
// carries only ids and a kind, never business content.
func (q *Queue) Claim(ctx context.Context, worker string) (Job, bool, error) {
	var j Job
	var found bool
	var queueKey *string

	err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, tenant_id, kind, payload, queue_key, attempts, max_attempts
			FROM claim_job($1)`, worker)

		err := row.Scan(&j.ID, &j.TenantID, &j.Kind, &j.Payload,
			&queueKey, &j.Attempts, &j.MaxAttempts)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return Job{}, false, err
	}
	if queueKey != nil {
		j.QueueKey = *queueKey
	}
	return j, found, err
}

// Complete marks a job done.
func (q *Queue) Complete(ctx context.Context, id uuid.UUID) error {
	return q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE job
			SET state = 'done', completed_at = now(),
			    locked_at = NULL, locked_by = NULL, last_error = NULL
			WHERE id = $1`, id)
		return err
	})
}

// Retry schedules another attempt.
//
// A job whose attempts have run out becomes 'dead' — except where max_attempts
// is zero, which means it never gives up. ZATCA submission uses that: there is
// no dead-letter path that discards an unreported invoice, because the exposure
// does not expire.
func (q *Queue) Retry(ctx context.Context, j Job, reason string, in time.Duration) error {
	exhausted := j.MaxAttempts > 0 && j.Attempts >= j.MaxAttempts

	return q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if exhausted {
			_, err := tx.Exec(ctx, `
				UPDATE job
				SET state = 'dead', last_error = $2, completed_at = now(),
				    locked_at = NULL, locked_by = NULL
				WHERE id = $1`, j.ID, reason)
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE job
			SET state = 'pending', run_after = now() + $3::interval,
			    last_error = $2, locked_at = NULL, locked_by = NULL
			WHERE id = $1`, j.ID, reason, in.String())
		return err
	})
}

// Fail marks a job permanently failed without retrying.
//
// For outcomes that retrying could never fix: a ZATCA business rejection, a
// payload this server version cannot read. Retrying those would burn attempts
// and, worse, keep the queue busy enough that the real alert goes unnoticed.
func (q *Queue) Fail(ctx context.Context, id uuid.UUID, reason string) error {
	return q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE job
			SET state = 'failed', last_error = $2, completed_at = now(),
			    locked_at = NULL, locked_by = NULL
			WHERE id = $1`, id, reason)
		return err
	})
}

// Reap releases jobs a dead worker was holding.
//
// Without it a crash mid-job leaves the row 'running' forever, and because a
// running job blocks its whole queue key, one dead worker would silently stop
// every invoice for that terminal from ever being submitted.
func (q *Queue) Reap(ctx context.Context, olderThan time.Duration) (int, error) {
	var released int
	err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT reap_abandoned_jobs($1::interval)`, olderThan.String()).
			Scan(&released)
	})
	return released, err
}

// Backoff is the retry schedule from design document 08 §4.
//
//	attempt 1  immediate
//	attempt 2  30s
//	attempt 3  2m
//	attempt 4  10m
//	attempt 5  1h
//	attempt 6+ 6h, indefinitely
//
// QA gate M2 requires recovery from a simulated 24-hour outage. This schedule
// produces roughly eight attempts across such an outage and then succeeds on
// reconnection, with nobody intervening.
func Backoff(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 0
	case attempt == 2:
		return 30 * time.Second
	case attempt == 3:
		return 2 * time.Minute
	case attempt == 4:
		return 10 * time.Minute
	case attempt == 5:
		return time.Hour
	default:
		return 6 * time.Hour
	}
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Stats is a snapshot of the queue, for the operator dashboard.
type Stats struct {
	Pending int `json:"pending"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
	Dead    int `json:"dead"`

	// OldestPendingAge is how long the most overdue job has been waiting. A
	// queue that is deep but moving is healthy; one where the oldest job keeps
	// getting older is not, and a count alone cannot tell them apart.
	OldestPendingAge time.Duration `json:"oldest_pending_age"`
}

func (q *Queue) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	var oldest *time.Time

	err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT
			  count(*) FILTER (WHERE state = 'pending')::int,
			  count(*) FILTER (WHERE state = 'running')::int,
			  count(*) FILTER (WHERE state = 'failed')::int,
			  count(*) FILTER (WHERE state = 'dead')::int,
			  min(run_after) FILTER (WHERE state = 'pending')
			FROM job`).
			Scan(&s.Pending, &s.Running, &s.Failed, &s.Dead, &oldest)
	})
	if err != nil {
		return Stats{}, err
	}
	if oldest != nil && oldest.Before(time.Now()) {
		s.OldestPendingAge = time.Since(*oldest)
	}
	return s, nil
}

// String renders a job for a log line.
func (j Job) String() string {
	return fmt.Sprintf("%s[%s] attempt %d", j.Kind, j.ID, j.Attempts)
}
