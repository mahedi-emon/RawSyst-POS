// Package platformops is the Super Admin control plane (blueprint H8, H10).
//
// # It reads about tenants, never inside them
//
// H8 asks for uptime, database health, active tenants and users, failed jobs,
// backup status platform-wide and error rates. Every one of those is METADATA:
// how many, how recent, how many failed. None of it is a tenant's sales, stock
// or customers, and the guard test that walks this schema looking for tables
// the platform may read exists to keep it that way.
//
// The one place that line is deliberately crossed is a support ticket, and that
// is the whole point of H10 — a ticket carries a subject and a description
// somebody wrote in order to be read.
//
// # Counts, not lists
//
// The dashboard says "412 active users across 9 tenants", not who they are. A
// platform operator troubleshooting a slow deployment needs the shape of the
// load; the names would be a privacy exposure that answers no operational
// question.
package platformops

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service answers the Super Admin's questions.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Health is H8's dashboard.
type Health struct {
	// The database answered, and how long it took. The first number an
	// operator looks at, and the only one here that is measured rather than
	// counted.
	DatabaseOK      bool `json:"database_ok"`
	DatabaseLatency int  `json:"database_latency_ms"`

	Tenants       int `json:"tenants"`
	ActiveTenants int `json:"active_tenants"`
	Companies     int `json:"companies"`
	Users         int `json:"users"`
	ActiveUsers   int `json:"active_users_30d"`
	Terminals     int `json:"terminals"`

	// The queue. A backlog that is growing and a queue that is failing are
	// different problems and the dashboard has to tell them apart.
	JobsQueued  int `json:"jobs_queued"`
	JobsRunning int `json:"jobs_running"`
	JobsFailed  int `json:"jobs_failed_24h"`
	JobsDead    int `json:"jobs_dead"`

	// Compliance, platform-wide. Not what any tenant owes, only how many
	// documents are stuck — which is the operator's problem rather than the
	// tenant's until it is not.
	SubmissionsPending int `json:"submissions_pending"`
	SubmissionsFailed  int `json:"submissions_failed"`

	// Backups, which H8 asks for across all tenants. Verified rather than
	// merely taken, for the reason H4 gives.
	TenantsBackedUp    int `json:"tenants_with_verified_backup"`
	TenantsUnprotected int `json:"tenants_without_verified_backup"`

	SyncFailures int `json:"sync_failures_24h"`

	// Tickets waiting on the platform. The queue an operator personally owes
	// an answer to.
	TicketsOpen    int `json:"tickets_open"`
	TicketsWaiting int `json:"tickets_waiting_on_support"`

	CheckedAt string `json:"checked_at"`
}

// Tenant is one customer of the platform, as the control plane sees them.
type Tenant struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Plan      string    `json:"plan_tier,omitempty"`
	Status    string    `json:"status,omitempty"`
	Companies int       `json:"companies"`
	Users     int       `json:"users"`
	CreatedAt string    `json:"created_at"`

	// LastActivity is the most recent sale anywhere in the tenant. The one
	// figure that separates a customer from a signup, and it is a timestamp
	// rather than a document.
	LastActivity string `json:"last_activity,omitempty"`

	// BackupVerified is when this tenant last proved it could restore. Empty
	// is the answer an operator has to act on.
	BackupVerified string `json:"backup_verified_at,omitempty"`
}

// Overview is H8's dashboard, in one read.
func (s *Service) Overview(ctx context.Context) (Health, error) {
	var h Health
	started := time.Now()

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT
			  (SELECT count(*)::int FROM tenant),
			  -- Active means somebody sold something in the last thirty days.
			  -- A tenant that signed up and never traded is a signup, and
			  -- counting it as active is how a platform tells itself a story.
			  (SELECT count(DISTINCT i.tenant_id)::int FROM sales_invoice i
			   WHERE i.issued_at > now() - interval '30 days'),
			  (SELECT count(*)::int FROM company),
			  (SELECT count(*)::int FROM app_user WHERE status = 'active'),
			  (SELECT count(*)::int FROM app_user
			   WHERE last_login_at > now() - interval '30 days'),
			  (SELECT count(*)::int FROM device WHERE status = 'active'),

			  (SELECT count(*)::int FROM job WHERE state = 'pending'),
			  (SELECT count(*)::int FROM job WHERE state = 'running'),
			  (SELECT count(*)::int FROM job
			   WHERE state = 'failed' AND created_at > now() - interval '24 hours'),
			  -- Dead is not a bigger number of failed. A failed job is still
			  -- being retried; a dead one has stopped, and the platform will
			  -- not touch it again unless somebody does.
			  (SELECT count(*)::int FROM job WHERE state = 'dead'),

			  -- An invoice that was chained but never reported. This is the
			  -- exposure E1.2 exists to prevent, counted platform-wide.
			  (SELECT count(*)::int FROM zatca_invoice
			   WHERE submitted_at IS NULL),
			  (SELECT count(*)::int FROM zatca_invoice
			   WHERE reject_reason IS NOT NULL),

			  (SELECT count(DISTINCT b.tenant_id)::int FROM backup_record b
			   WHERE b.verified_at > now() - interval '7 days'),
			  (SELECT count(*)::int FROM tenant t
			   WHERE NOT EXISTS (
			     SELECT 1 FROM backup_record b
			     WHERE b.tenant_id = t.id
			       AND b.verified_at > now() - interval '7 days')),

			  -- Items a device sent that could not be applied. Counted from
			  -- the batch's own tally rather than by joining the items, which
			  -- is the same figure and one table cheaper.
			  (SELECT coalesce(sum(failed), 0)::int FROM sync_batch
			   WHERE received_at > now() - interval '24 hours'),

			  (SELECT count(*)::int FROM support_ticket
			   WHERE status NOT IN ('resolved', 'closed')),
			  (SELECT count(*)::int FROM support_ticket
			   WHERE status IN ('open', 'waiting_on_support'))`).
			Scan(&h.Tenants, &h.ActiveTenants, &h.Companies, &h.Users,
				&h.ActiveUsers, &h.Terminals,
				&h.JobsQueued, &h.JobsRunning, &h.JobsFailed, &h.JobsDead,
				&h.SubmissionsPending, &h.SubmissionsFailed,
				&h.TenantsBackedUp, &h.TenantsUnprotected,
				&h.SyncFailures, &h.TicketsOpen, &h.TicketsWaiting)
	})

	// Measured whether or not the query succeeded, because the interesting
	// case is the one where it did not: a dashboard that reports no latency
	// alongside "database_ok: false" has thrown away the number that says
	// whether it was slow or gone.
	h.DatabaseLatency = int(time.Since(started).Milliseconds())
	h.DatabaseOK = err == nil
	h.CheckedAt = time.Now().UTC().Format(time.RFC3339)

	if err != nil {
		return h, db.Translate(err, "")
	}
	return h, nil
}

// Tenants lists the platform's customers.
func (s *Service) Tenants(ctx context.Context) ([]Tenant, error) {
	out := []Tenant{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT t.id, t.name, t.plan_tier::text, t.status::text,
			       (SELECT count(*)::int FROM company c WHERE c.tenant_id = t.id),
			       (SELECT count(*)::int FROM app_user u
			        WHERE u.tenant_id = t.id AND u.status = 'active'),
			       t.created_at,
			       (SELECT max(i.issued_at) FROM sales_invoice i
			        WHERE i.tenant_id = t.id),
			       (SELECT max(b.verified_at) FROM backup_record b
			        WHERE b.tenant_id = t.id)
			FROM tenant t
			ORDER BY t.created_at DESC
			LIMIT 500`)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var t Tenant
			var created time.Time
			var lastActivity, verified *time.Time
			if e := rows.Scan(&t.ID, &t.Name, &t.Plan, &t.Status, &t.Companies,
				&t.Users, &created, &lastActivity, &verified); e != nil {
				return e
			}
			t.CreatedAt = created.UTC().Format(time.RFC3339)
			if lastActivity != nil {
				t.LastActivity = lastActivity.UTC().Format(time.RFC3339)
			}
			if verified != nil {
				t.BackupVerified = verified.UTC().Format(time.RFC3339)
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// FailedJob is one thing the queue could not do.
type FailedJob struct {
	ID       uuid.UUID  `json:"id"`
	TenantID *uuid.UUID `json:"tenant_id,omitempty"`
	Tenant   string     `json:"tenant,omitempty"`
	Kind     string     `json:"kind"`
	Status   string     `json:"status"`
	Attempts int        `json:"attempts"`
	// LastError is the operator's actual question. Truncated at the database
	// rather than in the browser, because a stack trace in a list is a list
	// nobody can read.
	LastError string `json:"last_error,omitempty"`
	FailedAt  string `json:"failed_at"`
}

// FailedJobs is H8's failed background jobs, newest first.
func (s *Service) FailedJobs(ctx context.Context) ([]FailedJob, error) {
	out := []FailedJob{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT j.id, j.tenant_id, coalesce(t.name, ''), j.kind, j.state::text,
			       j.attempts, left(coalesce(j.last_error, ''), 400),
			       coalesce(j.completed_at, j.created_at)
			FROM job j
			LEFT JOIN tenant t ON t.id = j.tenant_id
			WHERE j.state IN ('failed', 'dead')
			ORDER BY coalesce(j.completed_at, j.created_at) DESC
			LIMIT 200`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var j FailedJob
			var at time.Time
			if e := rows.Scan(&j.ID, &j.TenantID, &j.Tenant, &j.Kind, &j.Status,
				&j.Attempts, &j.LastError, &at); e != nil {
				return e
			}
			j.FailedAt = at.UTC().Format(time.RFC3339)
			out = append(out, j)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// RetryJob puts a failed job back on the queue.
//
// A dead job is deliberately NOT retriable from here. Dead means the queue
// exhausted its attempts on something a retry could never fix, and a button
// that put it back would let an operator loop it forever against a permanent
// rejection while the real problem stayed unfixed.
func (s *Service) RetryJob(ctx context.Context, id uuid.UUID) error {
	return db.Translate(s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE job
			SET state = 'pending', run_after = now(), last_error = NULL
			WHERE id = $1 AND state = 'failed'`, id)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeConflict,
				"That job was not found, or is not in a state a retry could "+
					"change. A dead job exhausted its attempts on something "+
					"retrying will not fix.")
		}
		return nil
	}), "")
}
