// Package ops is backups (blueprint H4) and support tickets (H10).
//
// # A backup that cannot be restored is not a backup
//
// H4 says so in as many words, and it is the reason `verified_at` is a column
// of its own rather than a flag folded into `status`. A backup that ran and was
// never checked is not the same as one that was checked and works, and the
// dashboard reads the most recent VERIFIED one — because the number an owner
// actually needs is "how long ago was the last backup we know we could restore
// from", and reporting the most recent successful RUN answers a different and
// more comforting question.
//
// # This module records; it does not take the backup
//
// Taking a database dump is the operator's job — pg_dump, a managed snapshot,
// whatever the deployment uses — and this product would be wrong to pretend
// otherwise. What lives here is the record: when it ran, where it went, how
// big, whether anybody has proved it restores. That record is what H8 puts on
// the Super Admin dashboard and what makes a silent backup failure visible
// instead of discovered during a restore.
//
// # A ticket is a conversation, and both sides can see it
//
// H10 is the one place a tenant and the platform owner talk. Both policies let
// the platform read it, which is unusual in this schema and is the point of the
// module: a ticket holds a subject and a description, never the tenant's
// business records.
package ops

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service records backups and carries support conversations.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on whose books.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Backup is one run.
type Backup struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	Status   string    `json:"status"`
	Location string    `json:"location,omitempty"`
	Size     *int64    `json:"size_bytes,omitempty"`
	Checksum string    `json:"checksum,omitempty"`

	VerifiedAt  string `json:"verified_at,omitempty"`
	VerifyError string `json:"verify_error,omitempty"`

	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at,omitempty"`
	Error       string `json:"error,omitempty"`
	RequestedBy string `json:"requested_by,omitempty"`
}

// Health is what the dashboard asks: is this shop actually protected.
type Health struct {
	// LastVerified is the answer. See the package note: the most recent run is
	// a more comforting number and a less true one.
	LastVerified string `json:"last_verified_at,omitempty"`
	LastRun      string `json:"last_run_at,omitempty"`
	LastStatus   string `json:"last_status,omitempty"`

	// HoursSinceVerified is null when nothing has ever been verified, which is
	// different from zero and must not read as "just now".
	HoursSinceVerified *int `json:"hours_since_verified,omitempty"`

	// Failures in the last week. One failed backup is a bad night; four is a
	// broken arrangement, and the difference is what an owner needs to see.
	RecentFailures int `json:"recent_failures"`

	// AtRisk folds the whole thing into the one sentence a dashboard tile
	// shows, so the judgement lives here rather than being made again by every
	// screen that reads this.
	AtRisk bool   `json:"at_risk"`
	Says   string `json:"summary"`
}

// Ticket is one support conversation.
type Ticket struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"ticket_no"`
	Subject  string    `json:"subject"`
	Body     string    `json:"body"`
	Kind     string    `json:"kind"`
	Priority string    `json:"priority"`
	Status   string    `json:"status"`

	RaisedBy   string `json:"raised_by,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	ResolvedAt string `json:"resolved_at,omitempty"`

	// TenantName is filled only for the platform's own view. A tenant reading
	// their own tickets does not need to be told whose they are.
	TenantName string `json:"tenant,omitempty"`

	Messages []Message `json:"messages,omitempty"`
}

// Message is one reply on a ticket.
type Message struct {
	ID           uuid.UUID `json:"id"`
	Body         string    `json:"body"`
	FromPlatform bool      `json:"from_platform"`
	Author       string    `json:"author,omitempty"`
	CreatedAt    string    `json:"created_at"`
}

// --- backups --------------------------------------------------------------

// RecordBackup opens a record for a run that is starting.
//
// Opened before the work rather than written after it, so a backup that dies
// halfway leaves a `running` row somebody can see. A record written only on
// success would make a crashed backup indistinguishable from one nobody ever
// scheduled.
func (s *Service) RecordBackup(
	ctx context.Context, scope Scope, kind string,
) (Backup, error) {
	if kind != "scheduled" && kind != "manual" {
		kind = "manual"
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO backup_record (tenant_id, kind, requested_by)
			VALUES ($1,$2,$3) RETURNING id`,
			scope.TenantID, kind, scope.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That backup could not be recorded.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "backup_started",
			EntityType: "backup_record", EntityID: &id,
			After: map[string]any{"kind": kind},
		})
	})
	if err != nil {
		return Backup{}, err
	}
	return s.Backup(ctx, scope, id)
}

// FinishBackup closes a record, succeeded or failed.
func (s *Service) FinishBackup(
	ctx context.Context, scope Scope, id uuid.UUID,
	location, checksum string, size *int64, failure string,
) (Backup, error) {
	status := "succeeded"
	if strings.TrimSpace(failure) != "" {
		status = "failed"
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE backup_record
			SET status = $3, location = $4, checksum = $5, size_bytes = $6,
			    error = $7, finished_at = now()
			WHERE id = $1 AND tenant_id = $2 AND status = 'running'`,
			id, scope.TenantID, status, nullText(location), nullText(checksum),
			size, nullText(failure))
		if e != nil {
			return db.Translate(e, "That backup could not be closed.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeConflict,
				"That backup is not running, so it cannot be finished.")
		}
		return nil
	})
	if err != nil {
		return Backup{}, err
	}
	return s.Backup(ctx, scope, id)
}

// VerifyBackup records the outcome of actually trying to restore one.
//
// Separate from finishing it, because they are separate facts and the gap
// between them is where H4's warning lives. `verify_error` on its own means
// somebody tried and it did not restore, which is the most important row in
// this table and must never read as untested.
func (s *Service) VerifyBackup(
	ctx context.Context, scope Scope, id uuid.UUID, failure string,
) (Backup, error) {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status string
		if e := tx.QueryRow(ctx, `
			SELECT status FROM backup_record
			WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
			id, scope.TenantID).Scan(&status); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound, "That backup was not found.")
			}
			return e
		}
		if status != "succeeded" {
			return errs.New(errs.CodeConflict,
				"A backup that did not succeed cannot be verified.")
		}

		// A failed verification does not stamp verified_at. The column means
		// "we have proved this restores", and stamping it alongside an error
		// would let the dashboard read a broken backup as protection.
		if strings.TrimSpace(failure) != "" {
			_, e := tx.Exec(ctx, `
				UPDATE backup_record
				SET verified_at = NULL, verify_error = $2 WHERE id = $1`,
				id, strings.TrimSpace(failure))
			return e
		}
		_, e := tx.Exec(ctx, `
			UPDATE backup_record
			SET verified_at = now(), verify_error = NULL WHERE id = $1`, id)
		return e
	})
	if err != nil {
		return Backup{}, db.Translate(err, "")
	}
	return s.Backup(ctx, scope, id)
}

// Backups lists the runs, newest first.
func (s *Service) Backups(ctx context.Context, scope Scope) ([]Backup, error) {
	out := []Backup{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, backupSelect+`
			WHERE b.tenant_id = $1
			ORDER BY b.started_at DESC
			LIMIT 100`, scope.TenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			b, e := scanBackup(rows)
			if e != nil {
				return e
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Backup reads one.
func (s *Service) Backup(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Backup, error) {
	var out Backup
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, backupSelect+`
			WHERE b.id = $1 AND b.tenant_id = $2`, id, scope.TenantID)
		b, e := scanBackup(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That backup was not found.")
		}
		out = b
		return e
	})
	return out, db.Translate(err, "")
}

// BackupHealth is the dashboard's question, answered once.
func (s *Service) BackupHealth(ctx context.Context, scope Scope) (Health, error) {
	var h Health
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var verified, run *time.Time
		var status *string
		if e := tx.QueryRow(ctx, `
			SELECT (SELECT max(verified_at) FROM backup_record
			        WHERE tenant_id = $1),
			       (SELECT max(started_at) FROM backup_record
			        WHERE tenant_id = $1),
			       (SELECT status FROM backup_record WHERE tenant_id = $1
			        ORDER BY started_at DESC LIMIT 1),
			       (SELECT count(*)::int FROM backup_record
			        WHERE tenant_id = $1 AND status = 'failed'
			          AND started_at > now() - interval '7 days')`,
			scope.TenantID).Scan(&verified, &run, &status,
			&h.RecentFailures); e != nil {
			return e
		}

		if run != nil {
			h.LastRun = run.UTC().Format(time.RFC3339)
		}
		if status != nil {
			h.LastStatus = *status
		}
		if verified != nil {
			h.LastVerified = verified.UTC().Format(time.RFC3339)
			hours := int(time.Since(*verified).Hours())
			h.HoursSinceVerified = &hours
		}

		switch {
		case verified == nil:
			h.AtRisk = true
			h.Says = "No backup has ever been verified, so nobody knows " +
				"whether this shop's data could be restored."
		case *h.HoursSinceVerified > 24*7:
			h.AtRisk = true
			h.Says = fmt.Sprintf(
				"The last verified backup is %d days old.",
				*h.HoursSinceVerified/24)
		case h.RecentFailures > 0:
			h.AtRisk = true
			h.Says = fmt.Sprintf(
				"%d backups failed this week, though an older one is verified.",
				h.RecentFailures)
		default:
			h.Says = "Backed up and verified."
		}
		return nil
	})
	return h, db.Translate(err, "")
}

const backupSelect = `
	SELECT b.id, b.kind, b.status, coalesce(b.location, ''), b.size_bytes,
	       coalesce(b.checksum, ''), b.verified_at, coalesce(b.verify_error, ''),
	       b.started_at, b.finished_at, coalesce(b.error, ''),
	       coalesce(u.full_name, '')
	FROM backup_record b
	LEFT JOIN app_user u ON u.id = b.requested_by`

type scanner interface{ Scan(dest ...any) error }

func scanBackup(row scanner) (Backup, error) {
	var b Backup
	var verified, finished *time.Time
	var started time.Time
	if err := row.Scan(&b.ID, &b.Kind, &b.Status, &b.Location, &b.Size,
		&b.Checksum, &verified, &b.VerifyError, &started, &finished,
		&b.Error, &b.RequestedBy); err != nil {
		return Backup{}, err
	}
	if verified != nil {
		b.VerifiedAt = verified.UTC().Format(time.RFC3339)
	}
	b.StartedAt = started.UTC().Format(time.RFC3339)
	if finished != nil {
		b.FinishedAt = finished.UTC().Format(time.RFC3339)
	}
	return b, nil
}

func nullText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}
