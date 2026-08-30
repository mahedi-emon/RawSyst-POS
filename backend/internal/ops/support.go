package ops

// Support tickets (blueprint H10).
//
// # The status moves because somebody spoke
//
// A tenant replying puts a ticket back to waiting_on_support; the platform
// replying puts it to waiting_on_customer. Neither side has to remember to set
// it, because a status somebody has to maintain by hand is a status that is
// wrong within a week — and the whole value of this field is a support queue
// that shows what is actually waiting on the platform.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Raise opens a ticket.
func (s *Service) Raise(
	ctx context.Context, scope Scope, subject, body, kind, priority string,
) (Ticket, error) {
	subject = strings.TrimSpace(subject)
	body = strings.TrimSpace(body)
	if subject == "" {
		return Ticket{}, errs.Validation("Give the ticket a subject.").
			WithField("subject", "It is what the support queue shows.")
	}
	if body == "" {
		return Ticket{}, errs.Validation("Describe what happened.").
			WithField("body",
				"A subject alone means the first reply has to be a question.")
	}
	if kind == "" {
		kind = "question"
	}
	if priority == "" {
		priority = "normal"
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var seq int64
		if e := tx.QueryRow(ctx,
			`SELECT nextval('support_ticket_seq')`).Scan(&seq); e != nil {
			return e
		}
		// Global across tenants, and deliberately: a person telephoning about
		// "ticket 1042" should be talking about exactly one ticket, and
		// per-tenant numbering would give the platform owner several.
		number := fmt.Sprintf("TKT-%d", seq)

		if e := tx.QueryRow(ctx, `
			INSERT INTO support_ticket
			  (tenant_id, company_id, ticket_no, subject, body, kind,
			   priority, raised_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			RETURNING id`,
			scope.TenantID, nullUUID(scope.CompanyID), number, subject, body,
			kind, priority, scope.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That ticket could not be raised.")
		}
		return nil
	})
	if err != nil {
		return Ticket{}, err
	}
	return s.Ticket(ctx, scope, id)
}

// Tickets lists a tenant's own conversations, unfinished ones first.
func (s *Service) Tickets(
	ctx context.Context, scope Scope, includeClosed bool,
) ([]Ticket, error) {
	out := []Ticket{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, ticketSelect+`
			WHERE t.tenant_id = $1
			  AND ($2 OR t.status NOT IN ('resolved', 'closed'))
			ORDER BY (t.status NOT IN ('resolved', 'closed')) DESC,
			         t.updated_at DESC
			LIMIT 200`, scope.TenantID, includeClosed)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			t, e := scanTicket(rows)
			if e != nil {
				return e
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Ticket reads one, with the conversation on it.
func (s *Service) Ticket(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Ticket, error) {
	var out Ticket
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, ticketSelect+`
			WHERE t.id = $1 AND t.tenant_id = $2`, id, scope.TenantID)
		t, e := scanTicket(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That ticket was not found.")
		}
		if e != nil {
			return e
		}
		messages, e := readMessages(ctx, tx, id)
		t.Messages = messages
		out = t
		return e
	})
	return out, db.Translate(err, "")
}

// Reply adds a message from the tenant's side.
func (s *Service) Reply(
	ctx context.Context, scope Scope, id uuid.UUID, body string,
) (Ticket, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Ticket{}, errs.New(errs.CodeInvalidInput,
			"An empty reply says nothing.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status string
		if e := tx.QueryRow(ctx, `
			SELECT status FROM support_ticket
			WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
			id, scope.TenantID).Scan(&status); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound, "That ticket was not found.")
			}
			return e
		}
		if status == "closed" {
			return errs.New(errs.CodeConflict,
				"That ticket is closed. Raise a new one and it will be linked "+
					"by its subject.")
		}

		var label string
		if e := tx.QueryRow(ctx,
			`SELECT coalesce(full_name, '') FROM app_user WHERE id = $1`,
			scope.UserID).Scan(&label); e != nil && e != pgx.ErrNoRows {
			return e
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO support_message
			  (tenant_id, ticket_id, body, from_platform, author_label, author_id)
			VALUES ($1,$2,$3,false,$4,$5)`,
			scope.TenantID, id, body, nullText(label), scope.UserID); e != nil {
			return db.Translate(e, "That reply could not be added.")
		}

		// See the file note: the status follows the conversation rather than
		// waiting for somebody to remember to move it. A resolved ticket the
		// customer replies to is open again, because they evidently disagree.
		_, e := tx.Exec(ctx, `
			UPDATE support_ticket
			SET status = 'waiting_on_support', resolved_at = NULL
			WHERE id = $1`, id)
		return e
	})
	if err != nil {
		return Ticket{}, db.Translate(err, "")
	}
	return s.Ticket(ctx, scope, id)
}

// Close is the tenant saying they no longer need help.
//
// Only the person's own side: there is no route by which a tenant can mark a
// ticket resolved on the platform's behalf, and no route by which the platform
// closes one the customer has not agreed is finished.
func (s *Service) Close(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Ticket, error) {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE support_ticket
			SET status = 'closed', resolved_at = coalesce(resolved_at, now())
			WHERE id = $1 AND tenant_id = $2 AND status <> 'closed'`,
			id, scope.TenantID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeConflict,
				"That ticket was not found, or is already closed.")
		}
		return nil
	})
	if err != nil {
		return Ticket{}, db.Translate(err, "")
	}
	return s.Ticket(ctx, scope, id)
}

// --- the platform's side --------------------------------------------------

// Queue is every open ticket across every tenant.
//
// Runs on the platform plane, which is the only plane that can see more than
// one tenant's rows. Reachable only from the Super Admin routes.
func (s *Service) Queue(
	ctx context.Context, includeClosed bool,
) ([]Ticket, error) {
	out := []Ticket{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, ticketSelect+`
			WHERE ($1 OR t.status NOT IN ('resolved', 'closed'))
			ORDER BY
			  CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1
			                  WHEN 'normal' THEN 2 ELSE 3 END,
			  t.updated_at DESC
			LIMIT 500`, includeClosed)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			t, e := scanTicket(rows)
			if e != nil {
				return e
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Answer adds a message from the platform's side.
func (s *Service) Answer(
	ctx context.Context, id uuid.UUID, body, author, status string,
) (Ticket, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Ticket{}, errs.New(errs.CodeInvalidInput,
			"An empty reply says nothing.")
	}

	var tenantID uuid.UUID
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			SELECT tenant_id FROM support_ticket WHERE id = $1 FOR UPDATE`,
			id).Scan(&tenantID); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound, "That ticket was not found.")
			}
			return e
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO support_message
			  (tenant_id, ticket_id, body, from_platform, author_label)
			VALUES ($1,$2,$3,true,$4)`,
			tenantID, id, body, nullText(author)); e != nil {
			return db.Translate(e, "That reply could not be added.")
		}

		next := strings.TrimSpace(status)
		if next == "" {
			next = "waiting_on_customer"
		}
		resolved := "NULL"
		if next == "resolved" {
			resolved = "now()"
		}
		_, e := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE support_ticket SET status = $2, resolved_at = %s
			WHERE id = $1`, resolved), id, next)
		return db.Translate(e, "That ticket could not be updated.")
	})
	if err != nil {
		return Ticket{}, err
	}
	return s.PlatformTicket(ctx, id)
}

// PlatformTicket reads one ticket from the platform's side.
func (s *Service) PlatformTicket(
	ctx context.Context, id uuid.UUID,
) (Ticket, error) {
	var out Ticket
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, ticketSelect+` WHERE t.id = $1`, id)
		t, e := scanTicket(row)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That ticket was not found.")
		}
		if e != nil {
			return e
		}
		messages, e := readMessages(ctx, tx, id)
		t.Messages = messages
		out = t
		return e
	})
	return out, db.Translate(err, "")
}

const ticketSelect = `
	SELECT t.id, t.ticket_no, t.subject, t.body, t.kind, t.priority, t.status,
	       coalesce(u.full_name, ''), t.created_at, t.updated_at, t.resolved_at,
	       coalesce(n.name, '')
	FROM support_ticket t
	LEFT JOIN app_user u ON u.id = t.raised_by
	LEFT JOIN tenant n ON n.id = t.tenant_id`

func scanTicket(row scanner) (Ticket, error) {
	var t Ticket
	var created, updated time.Time
	var resolved *time.Time
	if err := row.Scan(&t.ID, &t.Number, &t.Subject, &t.Body, &t.Kind,
		&t.Priority, &t.Status, &t.RaisedBy, &created, &updated, &resolved,
		&t.TenantName); err != nil {
		return Ticket{}, err
	}
	t.CreatedAt = created.UTC().Format(time.RFC3339)
	t.UpdatedAt = updated.UTC().Format(time.RFC3339)
	if resolved != nil {
		t.ResolvedAt = resolved.UTC().Format(time.RFC3339)
	}
	t.Messages = []Message{}
	return t, nil
}

func readMessages(
	ctx context.Context, tx pgx.Tx, ticketID uuid.UUID,
) ([]Message, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, body, from_platform, coalesce(author_label, ''), created_at
		FROM support_message WHERE ticket_id = $1
		ORDER BY created_at`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Message{}
	for rows.Next() {
		var m Message
		var created time.Time
		if err := rows.Scan(&m.ID, &m.Body, &m.FromPlatform, &m.Author,
			&created); err != nil {
			return nil, err
		}
		m.CreatedAt = created.UTC().Format(time.RFC3339)
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
