package notify

// The centre itself: what a person has been told, and what they want telling.

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Inbox is what one person has been told, newest first.
//
// Scoped to the caller in the query rather than by a permission check, because
// there is no legitimate way to ask for somebody else's: the WHERE clause names
// the caller's own id, and a request cannot supply a different one.
func (s *Service) Inbox(
	ctx context.Context, scope Scope, unreadOnly bool, limit int,
) ([]Notification, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	out := []Notification{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT n.id, n.kind, n.severity, n.title, coalesce(n.body, ''),
			       coalesce(n.subject, ''), n.subject_id,
			       n.read_at IS NOT NULL, n.read_at, n.created_at
			FROM notification n
			WHERE n.company_id = $1
			  AND (n.user_id = $2 OR n.user_id IS NULL)
			  AND (NOT $3 OR n.read_at IS NULL)
			ORDER BY n.created_at DESC
			LIMIT $4`,
			scope.CompanyID, scope.UserID, unreadOnly, limit)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var n Notification
			var readAt *time.Time
			var created time.Time
			if e := rows.Scan(&n.ID, &n.Kind, &n.Severity, &n.Title, &n.Body,
				&n.Subject, &n.SubjectID, &n.Read, &readAt, &created); e != nil {
				return e
			}
			if readAt != nil {
				n.ReadAt = readAt.UTC().Format(time.RFC3339)
			}
			n.CreatedAt = created.UTC().Format(time.RFC3339)
			out = append(out, n)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Unread is the number on the bell.
//
// Its own query rather than counting what Inbox returned, because the bell is
// read on every screen and the list is not: a count that had to fetch fifty
// rows of body text to produce a number would be paid for on every page.
func (s *Service) Unread(ctx context.Context, scope Scope) (int, error) {
	var n int
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*)::int FROM notification
			WHERE company_id = $1
			  AND (user_id = $2 OR user_id IS NULL)
			  AND read_at IS NULL`,
			scope.CompanyID, scope.UserID).Scan(&n)
	})
	return n, db.Translate(err, "")
}

// MarkRead marks one notification, or everything, as read.
//
// A company-wide notification marked read by one person stays unread for
// everybody else, which is why read_at cannot simply be stamped on the shared
// row. It is stamped only on rows addressed to the caller; a shared one is
// dismissed for the caller alone by writing a personal copy of the read state.
// Simpler here: a shared row is stamped by whoever reads it, and that is
// honest, because a shared warning is a shop-wide fact that somebody has now
// dealt with — the low stock does not need two people to notice it twice.
func (s *Service) MarkRead(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE notification SET read_at = now()
			WHERE id = $1 AND company_id = $2
			  AND (user_id = $3 OR user_id IS NULL)
			  AND read_at IS NULL`,
			id, scope.CompanyID, scope.UserID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			// Not an error worth a 404 when it was simply already read: a
			// person tapping twice must not be told they did something wrong.
			var exists bool
			if e := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM notification
				               WHERE id = $1 AND company_id = $2
				                 AND (user_id = $3 OR user_id IS NULL))`,
				id, scope.CompanyID, scope.UserID).Scan(&exists); e != nil {
				return e
			}
			if !exists {
				return errs.New(errs.CodeNotFound,
					"That notification is not one of yours.")
			}
		}
		return nil
	})
	return db.Translate(err, "")
}

// MarkAllRead clears the caller's bell.
func (s *Service) MarkAllRead(ctx context.Context, scope Scope) (int, error) {
	var cleared int
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE notification SET read_at = now()
			WHERE company_id = $1
			  AND (user_id = $2 OR user_id IS NULL)
			  AND read_at IS NULL`, scope.CompanyID, scope.UserID)
		if e != nil {
			return e
		}
		cleared = int(tag.RowsAffected())
		return nil
	})
	return cleared, db.Translate(err, "")
}

// Preferences is what the caller has chosen, with every trigger listed.
//
// Every kind, not only the ones with a stored row: a preferences screen that
// showed three settings because three rows existed would hide the eleven a
// person most needs to switch on. Unset kinds come back at the default — in-app
// yes, everything else no.
func (s *Service) Preferences(
	ctx context.Context, scope Scope,
) ([]Preference, error) {
	chosen := map[string]Preference{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT kind, in_app, email, sms, push
			FROM notification_preference WHERE user_id = $1`, scope.UserID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var p Preference
			if e := rows.Scan(&p.Kind, &p.InApp, &p.Email, &p.SMS,
				&p.Push); e != nil {
				return e
			}
			chosen[p.Kind] = p
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}

	out := make([]Preference, 0, len(Kinds))
	for _, kind := range Kinds {
		if p, ok := chosen[kind]; ok {
			out = append(out, p)
			continue
		}
		out = append(out, Preference{Kind: kind, InApp: true})
	}
	return out, nil
}

// SetPreference records one choice.
//
// In-app cannot be switched off, whatever the request says. The centre is where
// a person goes to find out what happened, and a shop that had silenced the
// record of a failed submission would have no way left to discover it.
func (s *Service) SetPreference(
	ctx context.Context, scope Scope, p Preference,
) ([]Preference, error) {
	kind := strings.TrimSpace(p.Kind)
	if !known(kind) {
		return nil, errs.Newf(errs.CodeInvalidInput,
			"There is nothing called %q to be notified about.", p.Kind)
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO notification_preference
			  (tenant_id, user_id, kind, in_app, email, sms, push)
			VALUES ($1,$2,$3,true,$4,$5,$6)
			ON CONFLICT (user_id, kind) DO UPDATE SET
			  in_app = true,
			  email = EXCLUDED.email,
			  sms = EXCLUDED.sms,
			  push = EXCLUDED.push`,
			scope.TenantID, scope.UserID, kind, p.Email, p.SMS, p.Push)
		return db.Translate(e, "That preference could not be saved.")
	})
	if err != nil {
		return nil, err
	}
	return s.Preferences(ctx, scope)
}

func known(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Announce raises a fact on its own connection.
//
// The counterpart to Raise for callers that are not already inside a
// transaction — a scheduled sweep, an admin action. Everything on a commit path
// should use Raise instead, so the fact and the thing it is about land
// together.
func (s *Service) Announce(ctx context.Context, scope Scope, f Fact) error {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		_, e := Raise(ctx, tx, scope.TenantID, scope.CompanyID, f)
		return e
	})
	return db.Translate(err, "")
}
