// The Approval Centre: the queue, the decisions and the rule editor
// (blueprint D5, F1).
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Request is one thing waiting for a decision.
type Request struct {
	ID        uuid.UUID `json:"id"`
	Subject   string    `json:"subject"`
	SubjectID uuid.UUID `json:"subject_id"`
	Summary   string    `json:"summary"`
	Amount    string    `json:"amount,omitempty"`
	Status    string    `json:"status"`
	Step      int       `json:"current_step"`
	// StepsTotal lets a screen say "2 of 3" rather than leaving somebody to
	// wonder whether their approval was the last one needed.
	StepsTotal  int    `json:"steps_total"`
	RuleName    string `json:"rule_name,omitempty"`
	RequestedBy string `json:"requested_by,omitempty"`
	RequestedAt string `json:"requested_at"`
	EscalateAt  string `json:"escalate_at,omitempty"`

	Decisions []Decision `json:"decisions,omitempty"`
}

// Decision is one step's answer.
type Decision struct {
	Step      int    `json:"step"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason,omitempty"`
	DecidedBy string `json:"decided_by,omitempty"`
	DecidedAt string `json:"decided_at"`
}

// Pending is D5's central inbox: everything awaiting sign-off.
func (s *Service) Pending(
	ctx context.Context, scope Scope, subject string,
) ([]Request, error) {
	out := []Request{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, requestSelect+`
			WHERE r.company_id = $1
			  AND r.status IN ('pending', 'escalated')
			  AND ($2 = '' OR r.subject = $2)
			ORDER BY r.requested_at
			LIMIT 500`, scope.CompanyID, subject)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			req, e := scanRequest(rows)
			if e != nil {
				return e
			}
			out = append(out, req)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Decide records one step's answer and moves the request on.
//
// A multi-step rule is granted only at its LAST step: an approval that stopped
// early would mean the Owner's signature on an expense the Accountant never
// saw, which is the opposite of what a sequential chain is for.
func (s *Service) Decide(
	ctx context.Context, scope Scope, id uuid.UUID, approve bool, reason string,
) (Request, error) {
	if !approve && strings.TrimSpace(reason) == "" {
		return Request{}, errs.Validation(
			"Say why this is being turned down.").
			WithField("reason",
				"The person who asked has to know what to change.")
	}

	var out Request
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status string
		var step int
		var ruleID *uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT status, current_step, rule_id FROM approval_request
			WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			id, scope.CompanyID).Scan(&status, &step, &ruleID)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That request was not found.")
		}
		if e != nil {
			return e
		}
		if status != "pending" && status != "escalated" {
			return errs.Newf(errs.CodeConflict,
				"That request is %s, so it cannot be decided again.", status)
		}

		total, e := stepCount(ctx, tx, ruleID)
		if e != nil {
			return e
		}

		// Whoever is deciding must be entitled to this step. A chain that let
		// anybody with approval.decide answer any step would collapse a
		// three-person sign-off into whoever clicked first.
		allowed, e := mayDecideStep(ctx, tx, scope, ruleID, step)
		if e != nil {
			return e
		}
		if !allowed {
			return errs.New(errs.CodeForbidden,
				"This step is routed to somebody else.")
		}

		decision := "approved"
		if !approve {
			decision = "rejected"
		}
		if _, e := tx.Exec(ctx, `
			INSERT INTO approval_decision
			  (tenant_id, request_id, step, decision, reason, decided_by)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			scope.TenantID, id, step, decision, nullText(reason),
			scope.UserID); e != nil {
			return e
		}

		switch {
		case !approve:
			// One rejection ends it. Carrying on up the chain after somebody
			// has said no would ask three people to consider something the
			// first has already refused.
			if _, e := tx.Exec(ctx, `
				UPDATE approval_request
				SET status = 'rejected', decided_at = now() WHERE id = $1`,
				id); e != nil {
				return e
			}
		case step >= total:
			if _, e := tx.Exec(ctx, `
				UPDATE approval_request
				SET status = 'approved', decided_at = now() WHERE id = $1`,
				id); e != nil {
				return e
			}
		default:
			if _, e := tx.Exec(ctx, `
				UPDATE approval_request
				SET current_step = current_step + 1 WHERE id = $1`,
				id); e != nil {
				return e
			}
		}

		read, e := s.read(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// mayDecideStep says whether the caller is routed this step.
//
// A step names a role or a person. A delegation in force redirects a person's
// steps to whoever is covering for them — F1's "approvers can delegate while
// on leave" — which is checked here rather than by rewriting the rule, so the
// rule still says who is really responsible.
func mayDecideStep(
	ctx context.Context, tx pgx.Tx, scope Scope, ruleID *uuid.UUID, step int,
) (bool, error) {
	if ruleID == nil {
		// A request with no rule behind it (the rule was deleted) falls back to
		// "anybody who may decide", because the alternative is a request nobody
		// can ever clear.
		return true, nil
	}

	var raw string
	if err := tx.QueryRow(ctx,
		`SELECT steps::text FROM approval_rule WHERE id = $1`,
		*ruleID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, err
	}

	var steps []struct {
		Role   string `json:"role"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		return false, errs.New(errs.CodeInternal,
			"An approval rule's routing could not be read.")
	}
	if step < 1 || step > len(steps) {
		return true, nil
	}
	s := steps[step-1]

	if s.UserID != "" {
		if s.UserID == scope.UserID.String() {
			return true, nil
		}
		// Or whoever is covering for them today.
		var covering bool
		err := tx.QueryRow(ctx, `
			SELECT true FROM approval_delegation
			WHERE company_id = $1 AND from_user_id = $2::uuid
			  AND to_user_id = $3
			  AND current_date BETWEEN starts_on AND ends_on`,
			scope.CompanyID, s.UserID, scope.UserID).Scan(&covering)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return covering, err
	}

	if s.Role != "" {
		var holds bool
		err := tx.QueryRow(ctx, `
			SELECT true FROM user_role_assignment ura
			JOIN role r ON r.id = ura.role_id
			JOIN role template ON template.id = coalesce(r.cloned_from, r.id)
			WHERE ura.user_id = $1 AND template.key = $2`,
			scope.UserID, s.Role).Scan(&holds)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return holds, err
	}

	return true, nil
}

func stepCount(
	ctx context.Context, tx pgx.Tx, ruleID *uuid.UUID,
) (int, error) {
	if ruleID == nil {
		return 1, nil
	}
	var n int
	err := tx.QueryRow(ctx,
		`SELECT greatest(jsonb_array_length(steps), 1) FROM approval_rule
		 WHERE id = $1`, *ruleID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, nil
	}
	return n, err
}

// Escalate moves requests nobody answered in time.
//
// Run from the job queue. F1: "if an approver doesn't respond within X hours,
// escalate". Escalating marks the request rather than silently approving it —
// a timeout is not consent.
func (s *Service) Escalate(ctx context.Context, scope Scope) (int, error) {
	var moved int
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE approval_request
			SET status = 'escalated'
			WHERE company_id = $1 AND status = 'pending'
			  AND escalate_at IS NOT NULL AND escalate_at < now()`,
			scope.CompanyID)
		if e != nil {
			return e
		}
		moved = int(tag.RowsAffected())
		return nil
	})
	return moved, db.Translate(err, "")
}

// --- Rules ---------------------------------------------------------------

// SaveRule creates or replaces a rule.
func (s *Service) SaveRule(
	ctx context.Context, scope Scope, in Rule,
) (Rule, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Rule{}, errs.Validation("Give the rule a name.").
			WithField("name",
				"It is what somebody is told when the rule stops them.")
	}
	if strings.TrimSpace(in.Subject) == "" {
		return Rule{}, errs.Validation("Say what the rule watches.").
			WithField("subject", "An expense, a purchase order, a discount.")
	}
	if in.Condition == "" {
		in.Condition = "{}"
	}
	if in.Steps == "" {
		in.Steps = "[]"
	}
	if in.Action == "" {
		in.Action = "require_approval"
	}

	var out Rule
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO approval_rule
			  (tenant_id, company_id, name, subject, condition, action, steps,
			   escalate_after_hours, priority, created_by)
			VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7::jsonb,$8,$9,$10)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, strings.TrimSpace(in.Name),
			strings.TrimSpace(in.Subject), in.Condition, in.Action, in.Steps,
			in.Escalate, in.Priority, scope.UserID).Scan(&id); e != nil {
			return db.Translate(e,
				"That rule could not be saved. A rule that requires approval "+
					"must route to somebody.")
		}
		read, e := s.readRule(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// Rules lists what is configured.
func (s *Service) Rules(
	ctx context.Context, scope Scope, subject string,
) ([]Rule, error) {
	out := []Rule{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, ruleSelect+`
			WHERE a.company_id = $1 AND ($2 = '' OR a.subject = $2)
			ORDER BY a.subject, a.priority DESC, a.name`,
			scope.CompanyID, subject)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanRule(rows)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SetRuleActive turns a rule on or off.
func (s *Service) SetRuleActive(
	ctx context.Context, scope Scope, id uuid.UUID, active bool,
) error {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE approval_rule SET is_active = $3
			WHERE id = $1 AND company_id = $2`, id, scope.CompanyID, active)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That rule was not found.")
		}
		return nil
	})
	return db.Translate(err, "")
}

// Delegate records cover while somebody is away.
func (s *Service) Delegate(
	ctx context.Context, scope Scope, from, to uuid.UUID,
	starts, ends time.Time, note string,
) error {
	if from == to {
		return errs.New(errs.CodeInvalidInput,
			"Delegating to yourself changes nothing.")
	}
	if ends.Before(starts) {
		return errs.New(errs.CodeInvalidInput,
			"The cover cannot end before it starts.")
	}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO approval_delegation
			  (tenant_id, company_id, from_user_id, to_user_id, starts_on,
			   ends_on, note)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			scope.TenantID, scope.CompanyID, from, to, starts, ends,
			nullText(note))
		return e
	})
	return db.Translate(err, "")
}

// --- reads ---------------------------------------------------------------

const requestSelect = `
	SELECT r.id, r.subject, r.subject_id, r.summary, r.amount, r.status,
	       r.current_step,
	       coalesce(greatest(jsonb_array_length(ar.steps), 1), 1),
	       coalesce(ar.name, ''), coalesce(u.full_name, ''),
	       r.requested_at, r.escalate_at
	FROM approval_request r
	LEFT JOIN approval_rule ar ON ar.id = r.rule_id
	LEFT JOIN app_user u ON u.id = r.requested_by`

func (s *Service) read(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Request, error) {
	row := tx.QueryRow(ctx, requestSelect+`
		WHERE r.id = $1 AND r.company_id = $2`, id, companyID)
	out, err := scanRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, errs.New(errs.CodeNotFound,
			"That request was not found.")
	}
	if err != nil {
		return Request{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT d.step, d.decision, coalesce(d.reason, ''),
		       coalesce(u.full_name, ''), d.decided_at
		FROM approval_decision d
		LEFT JOIN app_user u ON u.id = d.decided_by
		WHERE d.request_id = $1 ORDER BY d.step, d.decided_at`, id)
	if err != nil {
		return Request{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Decision
		var at time.Time
		if e := rows.Scan(&d.Step, &d.Decision, &d.Reason, &d.DecidedBy,
			&at); e != nil {
			return Request{}, e
		}
		d.DecidedAt = at.UTC().Format(time.RFC3339)
		out.Decisions = append(out.Decisions, d)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanRequest(row scanner) (Request, error) {
	var r Request
	var amount *decimal.Decimal
	var requested time.Time
	var escalate *time.Time
	if err := row.Scan(&r.ID, &r.Subject, &r.SubjectID, &r.Summary, &amount,
		&r.Status, &r.Step, &r.StepsTotal, &r.RuleName, &r.RequestedBy,
		&requested, &escalate); err != nil {
		return Request{}, err
	}
	if amount != nil {
		r.Amount = amount.StringFixed(2)
	}
	r.RequestedAt = requested.UTC().Format(time.RFC3339)
	if escalate != nil {
		r.EscalateAt = escalate.UTC().Format(time.RFC3339)
	}
	r.Decisions = []Decision{}
	return r, nil
}

const ruleSelect = `
	SELECT a.id, a.name, a.is_active, a.subject, a.condition::text, a.action,
	       a.steps::text, a.escalate_after_hours, a.priority
	FROM approval_rule a`

func (s *Service) readRule(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Rule, error) {
	row := tx.QueryRow(ctx, ruleSelect+`
		WHERE a.id = $1 AND a.company_id = $2`, id, companyID)
	out, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, errs.New(errs.CodeNotFound, "That rule was not found.")
	}
	return out, err
}

func scanRule(row scanner) (Rule, error) {
	var r Rule
	err := row.Scan(&r.ID, &r.Name, &r.IsActive, &r.Subject, &r.Condition,
		&r.Action, &r.Steps, &r.Escalate, &r.Priority)
	return r, err
}

func nullText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// Request reads one request and every decision taken on it.
//
// Behind `approval.view`, which a requester holds: somebody who asked for
// something has to be able to watch it without being able to grant it.
func (s *Service) Request(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Request, error) {
	var out Request
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.read(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// Mine is what the caller themselves asked for, decided or is still waiting on.
//
// A separate list from Pending rather than a filter on it, because they answer
// different questions. Pending is "what is waiting for somebody"; this is "what
// happened to the thing I asked for", and a person checking on their own
// request should not have to read past everybody else's.
func (s *Service) Mine(
	ctx context.Context, scope Scope, includeSettled bool,
) ([]Request, error) {
	out := []Request{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, requestSelect+`
			WHERE r.company_id = $1 AND r.requested_by = $2
			  AND ($3 OR r.status IN ('pending', 'escalated'))
			ORDER BY r.requested_at DESC
			LIMIT 200`, scope.CompanyID, scope.UserID, includeSettled)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			req, e := scanRequest(rows)
			if e != nil {
				return e
			}
			out = append(out, req)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Delegations lists the cover currently arranged.
func (s *Service) Delegations(ctx context.Context, scope Scope) ([]Cover, error) {
	out := []Cover{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT d.id, coalesce(f.full_name, ''), coalesce(t.full_name, ''),
			       d.from_user_id, d.to_user_id,
			       to_char(d.starts_on, 'YYYY-MM-DD'),
			       to_char(d.ends_on, 'YYYY-MM-DD'),
			       coalesce(d.note, ''),
			       d.starts_on <= now() AND d.ends_on >= now()
			FROM approval_delegation d
			LEFT JOIN app_user f ON f.id = d.from_user_id
			LEFT JOIN app_user t ON t.id = d.to_user_id
			WHERE d.company_id = $1 AND d.ends_on >= now() - interval '30 days'
			ORDER BY d.starts_on DESC
			LIMIT 200`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var c Cover
			if e := rows.Scan(&c.ID, &c.From, &c.To, &c.FromID, &c.ToID,
				&c.Starts, &c.Ends, &c.Note, &c.Live); e != nil {
				return e
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Cover is one arrangement of who decides while somebody is away.
type Cover struct {
	ID     uuid.UUID `json:"id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	FromID uuid.UUID `json:"from_user_id"`
	ToID   uuid.UUID `json:"to_user_id"`
	Starts string    `json:"starts_on"`
	Ends   string    `json:"ends_on"`
	Note   string    `json:"note,omitempty"`
	// Live says the cover is in force right now, so a screen does not leave
	// somebody to compare two dates against today in their head.
	Live bool `json:"is_live"`
}
