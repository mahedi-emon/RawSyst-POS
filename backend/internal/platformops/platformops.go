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
	"strings"
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

	// The commercial shape of the platform, which is the half of H8 this
	// dashboard did not have. Everything above counts machinery; these count
	// the business.
	//
	// SuspendedTenants and DeactivatedTenants read `tenant.status`, which is
	// written and -- as of this phase -- still enforced by nothing. They are
	// counted here anyway because an operator needs to see what the record
	// says before anything starts acting on it, and because a count that
	// stayed at zero would hide the gap rather than show it.
	SuspendedTenants   int `json:"suspended_tenants"`
	DeactivatedTenants int `json:"deactivated_tenants"`

	// Owners is the number of businesses that have at least one account, which
	// is as close to "business owners" as the platform plane can honestly
	// count: it cannot read the Owner role (see Tenant.OwnerName). A tenant
	// with no account at all is unreachable and worth noticing.
	Owners int `json:"business_owners"`

	// ActiveSubscriptions counts the record, not an entitlement. Nothing yet
	// stops an expired subscription from trading -- that is the next phase --
	// so "expired" here means "the date has passed", never "has been cut off".
	ActiveSubscriptions  int `json:"active_subscriptions"`
	ExpiredSubscriptions int `json:"expired_subscriptions"`
	TrialSubscriptions   int `json:"trial_subscriptions"`
	// ExpiringSoon is inside thirty days, which is the window in which an
	// operator can still do something about it.
	ExpiringSoon int `json:"subscriptions_expiring_30d"`
	// NoSubscription should be zero after migration 0137. If it is not, a
	// business exists with no commercial terms recorded at all.
	NoSubscription int `json:"tenants_without_subscription"`

	// SignupsThisWeek is new businesses in the last seven days.
	SignupsThisWeek int `json:"signups_7d"`

	CheckedAt string `json:"checked_at"`
}

// Tenant is one customer of the platform, as the control plane sees them.
type Tenant struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Plan   string    `json:"plan_tier,omitempty"`
	Status string    `json:"status,omitempty"`

	// Market is the country this account was sold into (0103). Shown in the
	// list because it is the question an operator asks about a tenant they do
	// not recognise, and answering it otherwise means opening the account.
	Market    string `json:"market,omitempty"`
	Companies int    `json:"companies"`
	Users     int    `json:"users"`
	CreatedAt string `json:"created_at"`

	// LastActivity is the most recent sale anywhere in the tenant. The one
	// figure that separates a customer from a signup, and it is a timestamp
	// rather than a document.
	LastActivity string `json:"last_activity,omitempty"`

	// BackupVerified is when this tenant last proved it could restore. Empty
	// is the answer an operator has to act on.
	BackupVerified string `json:"backup_verified_at,omitempty"`

	// Who to ring. An operator looking at a list of businesses is usually
	// about to contact one of them, and a list that makes them open the
	// account to find a name is a list that costs a click every time.
	//
	// This is the tenant's FIRST user, not "the user holding the Owner role" --
	// because `role` and `user_role_assignment` are deliberately not readable
	// from the platform plane (migration 0006 says why, and widening that to
	// prettify a list would be a poor trade). Provisioning creates the owner
	// before any other account exists, so for a business created through the
	// product these are the same person. For one whose first account was later
	// deleted, they are not, and the column is a best effort rather than an
	// authority.
	OwnerName  string `json:"owner_name,omitempty"`
	OwnerEmail string `json:"owner_email,omitempty"`

	// The commercial relationship. Empty across the board means no
	// subscription row exists at all, which after migration 0137 should be
	// impossible -- and if it is ever seen again, it is the thing to
	// investigate rather than something to render as a blank cell.
	SubStatus  string `json:"subscription_status,omitempty"`
	SubStarted string `json:"subscription_started_on,omitempty"`
	// SubExpires is the last day paid for. Empty is a lifetime subscription OR
	// one whose end was never recorded, and those are not the same thing --
	// which is why SubCycle is here to tell them apart.
	SubExpires string `json:"subscription_expires_on,omitempty"`
	SubCycle   string `json:"subscription_cycle,omitempty"`

	// Onboarding is 'complete', or the step the owner is stuck on. A client
	// who signed up three weeks ago and is still on step two is a support call
	// that has not happened yet.
	Onboarding string `json:"onboarding,omitempty"`
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
			   WHERE status IN ('open', 'waiting_on_support')),

			  -- The commercial half. tenant.status first: what the record
			  -- says, which is not yet what the product enforces.
			  (SELECT count(*)::int FROM tenant WHERE status = 'suspended'),
			  (SELECT count(*)::int FROM tenant WHERE status = 'deactivated'),
			  (SELECT count(DISTINCT u.tenant_id)::int FROM app_user u
			   WHERE u.tenant_id IS NOT NULL),

			  -- Active means the row says active AND the date has not passed.
			  -- A subscription marked active whose period ended last March is
			  -- not an active subscription; counting it as one is how the
			  -- number an operator trusts stops matching the money.
			  (SELECT count(*)::int FROM subscription
			   WHERE status = 'active'
			     AND (current_period_end IS NULL
			          OR current_period_end >= current_date)),
			  (SELECT count(*)::int FROM subscription
			   WHERE current_period_end IS NOT NULL
			     AND current_period_end < current_date
			     AND status <> 'cancelled'),
			  (SELECT count(*)::int FROM subscription WHERE status = 'trialing'),
			  (SELECT count(*)::int FROM subscription
			   WHERE current_period_end IS NOT NULL
			     AND current_period_end >= current_date
			     AND current_period_end < current_date + 30),
			  (SELECT count(*)::int FROM tenant t
			   WHERE NOT EXISTS (
			     SELECT 1 FROM subscription s WHERE s.tenant_id = t.id)),

			  (SELECT count(*)::int FROM tenant
			   WHERE created_at > now() - interval '7 days')`).
			Scan(&h.Tenants, &h.ActiveTenants, &h.Companies, &h.Users,
				&h.ActiveUsers, &h.Terminals,
				&h.JobsQueued, &h.JobsRunning, &h.JobsFailed, &h.JobsDead,
				&h.SubmissionsPending, &h.SubmissionsFailed,
				&h.TenantsBackedUp, &h.TenantsUnprotected,
				&h.SyncFailures, &h.TicketsOpen, &h.TicketsWaiting,
				&h.SuspendedTenants, &h.DeactivatedTenants, &h.Owners,
				&h.ActiveSubscriptions, &h.ExpiredSubscriptions,
				&h.TrialSubscriptions, &h.ExpiringSoon, &h.NoSubscription,
				&h.SignupsThisWeek)
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
// TenantFilter narrows and pages the tenant list.
//
// It exists because the list used to be `ORDER BY created_at DESC LIMIT 500`
// with no arguments at all, and the screen above it filtered the rows it had
// received in the browser. On a platform with more than five hundred accounts —
// the development database alone holds nine and a half thousand — that meant an
// operator searching for a client who signed up earlier than the most recent
// five hundred was told there were no matches. "No matches" and "not in the
// half of the table I sent you" are different answers, and only one of them was
// true.
//
// Every field is optional; the zero value lists the newest accounts.
type TenantFilter struct {
	// Search matches the business name, case-insensitively, anywhere in it.
	// Not the id: nobody types a uuid from memory.
	Search string
	Market string
	Status string
	Plan   string

	// Limit defaults to 50 and is capped at 200, like every other list.
	Limit int
	// After is the id of the last row of the previous page.
	After *uuid.UUID
}

func (s *Service) Tenants(ctx context.Context, f TenantFilter) ([]Tenant, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}

	out := []Tenant{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT t.id, t.name, t.plan_tier::text, t.status::text, t.market,
			       (SELECT count(*)::int FROM company c WHERE c.tenant_id = t.id),
			       (SELECT count(*)::int FROM app_user u
			        WHERE u.tenant_id = t.id AND u.status = 'active'),
			       t.created_at,
			       (SELECT max(i.issued_at) FROM sales_invoice i
			        WHERE i.tenant_id = t.id),
			       (SELECT max(b.verified_at) FROM backup_record b
			        WHERE b.tenant_id = t.id),
			       o.full_name, o.email,
			       s.status, s.started_on, s.current_period_end, s.cycle,
			       CASE WHEN p.completed_at IS NOT NULL THEN 'complete'
			            ELSE p.current_step::text END
			FROM tenant t
			LEFT JOIN subscription s ON s.tenant_id = t.id
			LEFT JOIN onboarding_progress p ON p.tenant_id = t.id
			-- The tenant's first account. See the comment on Tenant.OwnerName
			-- for why this is not a join through the Owner role.
			LEFT JOIN LATERAL (
			  SELECT u.full_name, u.email FROM app_user u
			  WHERE u.tenant_id = t.id
			  ORDER BY u.created_at, u.id
			  LIMIT 1
			) o ON true
			WHERE ($1::text IS NULL OR t.name ILIKE '%' || $1 || '%')
			  AND ($2::text IS NULL OR t.market = $2)
			  AND ($3::text IS NULL OR t.status::text = $3)
			  AND ($4::text IS NULL OR t.plan_tier::text = $4)
			  -- Keyset on the pair the list is ordered by, not on the id alone:
			  -- ordering by id would put the newest account somewhere in the
			  -- middle, and an operator scanning for a signup from this morning
			  -- would not find it near the top.
			  AND ($5::uuid IS NULL OR (t.created_at, t.id) <
			       (SELECT a.created_at, a.id FROM tenant a WHERE a.id = $5::uuid))
			ORDER BY t.created_at DESC, t.id DESC
			LIMIT $6`,
			nullText(f.Search), nullText(f.Market), nullText(f.Status),
			nullText(f.Plan), f.After, f.Limit)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var t Tenant
			var created time.Time
			var lastActivity, verified *time.Time
			var ownerName, ownerEmail, subStatus, subCycle, onboarding *string
			var subStarted, subExpires *time.Time
			if e := rows.Scan(&t.ID, &t.Name, &t.Plan, &t.Status, &t.Market, &t.Companies,
				&t.Users, &created, &lastActivity, &verified,
				&ownerName, &ownerEmail,
				&subStatus, &subStarted, &subExpires, &subCycle,
				&onboarding); e != nil {
				return e
			}
			t.CreatedAt = created.UTC().Format(time.RFC3339)
			if lastActivity != nil {
				t.LastActivity = lastActivity.UTC().Format(time.RFC3339)
			}
			if verified != nil {
				t.BackupVerified = verified.UTC().Format(time.RFC3339)
			}
			t.OwnerName = text(ownerName)
			t.OwnerEmail = text(ownerEmail)
			t.SubStatus = text(subStatus)
			t.SubCycle = text(subCycle)
			t.Onboarding = text(onboarding)
			// Dates, not timestamps. A subscription runs in whole days and
			// rendering a midnight on the end of one is an invitation to read
			// a time zone into a commercial term that has none.
			t.SubStarted = day(subStarted)
			t.SubExpires = day(subExpires)
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// text is a nullable column as a string, with absent reading as empty.
func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// day is a nullable date column, written as a date and not a timestamp.
func day(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// Action is one thing an operator did, for the platform's own trail.
type Action struct {
	At string `json:"at"`
	// Actor is the label recorded at the time, which survives the account
	// being deleted. Empty for something the system did on its own.
	Actor  string `json:"actor,omitempty"`
	Action string `json:"action"`
	// TenantName rather than only the id: an operator reading their own trail
	// is checking what they did to a named client, and a list of uuids is a
	// list nobody audits.
	TenantID   *uuid.UUID `json:"tenant_id,omitempty"`
	TenantName string     `json:"tenant_name,omitempty"`
	EntityType string     `json:"entity_type,omitempty"`
}

// RecentActions is the platform's own audit trail, newest first.
//
// # Why this route had to exist
//
// Super Admin actions have been audited since provisioning was built --
// `tenant_provisioned`, `subscription_set`, operator changes, every one of
// them written inside the transaction of the thing it records. Nothing could
// read them. The only audit route in the product is `GET /api/v1/audit`, which
// is tenant-scoped behind `accounting.view`, and a Super Admin is refused every
// tenant route by design. So the trail was write-only: complete, permanent,
// and visible to nobody without database access.
//
// An audit trail nobody can read is a compliance artefact rather than a
// control. This is the read side.
//
// # What it deliberately does not return
//
// The `before` and `after` payloads. They are the detail of a change and they
// are where a careless entry would put something sensitive; this is a "what has
// been happening" list, not an investigation tool, and the columns it returns
// are the four an operator scans. Anything deeper is a question for the row
// itself, asked by somebody with the access to ask it.
func (s *Service) RecentActions(ctx context.Context, limit int) ([]Action, error) {
	if limit <= 0 || limit > 200 {
		limit = 25
	}

	out := []Action{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Row-level security does the confining, not this WHERE clause: the
		// audit policy is `tenant_id = current_tenant_id() OR
		// is_platform_admin()`, and this runs on the platform plane. A tenant
		// asking the same question through their own route sees only their own
		// rows, from the same table, without this code knowing the difference.
		rows, e := tx.Query(ctx, `
			SELECT a.occurred_at, coalesce(a.actor_label, ''), a.action,
			       a.tenant_id, coalesce(t.name, ''), a.entity_type
			FROM audit_log a
			LEFT JOIN tenant t ON t.id = a.tenant_id
			ORDER BY a.occurred_at DESC, a.id DESC
			LIMIT $1`, limit)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var a Action
			var at time.Time
			if e := rows.Scan(&at, &a.Actor, &a.Action, &a.TenantID,
				&a.TenantName, &a.EntityType); e != nil {
				return e
			}
			a.At = at.UTC().Format(time.RFC3339)
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// nullText turns an absent filter into SQL NULL, so one query serves the
// filtered and the unfiltered case rather than two being assembled.
//
// Trimmed, because a search box that has been typed into and cleared leaves a
// space behind, and a space is not a search for a business whose name contains
// one.
func nullText(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
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
