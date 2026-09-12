package identity

// Platform operators: the people who administer the platform itself.
//
// # The gap this closes
//
// A platform operator is a user with no tenant. Exactly one thing in the whole
// product could create one -- `cmd/bootstrap` -- and it refuses the moment one
// exists, which is what makes it safe to leave on a server. Its own error text
// said "use Super Admin to add another", and Super Admin had no such screen.
//
// So a deployment had precisely one administrator, for ever. There was no way
// to add a colleague, no way to take away access from somebody who left, and
// no way to correct the address the first one was created under -- which is a
// real thing that happens on the first day of a deployment and left the only
// remedy being SQL against production.
//
// # Why an operator is edited rather than replaced
//
// The obvious workaround was to create a second operator and stop using the
// first. That leaves two accounts with full platform authority where one
// person was intended, and the abandoned one keeps working: it is a live
// credential nobody is watching. Correcting the address on the account that
// exists is one privileged account, and the audit log carries the before and
// after so the change is visible to whoever reviews it.
//
// # Why disabling is guarded twice
//
// A platform with no active operator cannot be administered by anybody, and
// nothing in the product can create one on a running deployment: bootstrap
// refuses while the disabled row is still there.
//
// The refusal that does the work day to day is that nobody may disable
// themselves. With two administrators it is enough on its own -- whoever is
// doing the disabling is still active afterwards.
//
// The count of remaining active operators is a backstop for the case the first
// rule does not cover: a caller who holds platform authority without being one
// of the rows being counted. It costs one query inside a transaction that was
// happening anyway, and the alternative is a deployment nobody can administer
// and no supported way to recover it.

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/actor"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/audit"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// Operator is one platform administrator, as the screen reads them.
type Operator struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	FullName string    `json:"full_name"`
	Status   string    `json:"status"`

	// MustChangePassword is shown because an operator created by another
	// administrator, or by bootstrap, has not signed in yet. Without it a
	// pending account looks identical to a working one.
	MustChangePassword bool `json:"must_change_password"`

	// MFAEnabled is on the list rather than a detail screen: an account with
	// full platform authority and no second factor is the thing a reviewer is
	// looking for.
	MFAEnabled  bool    `json:"mfa_enabled"`
	LastLoginAt *string `json:"last_login_at"`
	CreatedAt   string  `json:"created_at"`

	// Self marks the caller's own row, so a screen can decline to offer the
	// actions that would lock them out rather than offering them and failing.
	Self bool `json:"self"`
}

// Operators lists every platform administrator.
func (s *Service) Operators(ctx context.Context) ([]Operator, error) {
	a := actor.From(ctx)
	if !a.IsSuperAdmin {
		return nil, errs.New(errs.CodeForbidden,
			"Only a platform administrator can see this.")
	}

	out := []Operator{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, email, full_name, status::text, must_change_password,
			       mfa_enabled,
			       to_char(last_login_at AT TIME ZONE 'UTC',
			               'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
			       to_char(created_at AT TIME ZONE 'UTC',
			               'YYYY-MM-DD"T"HH24:MI:SS"Z"')
			FROM app_user
			WHERE tenant_id IS NULL
			ORDER BY created_at`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var op Operator
			if e := rows.Scan(&op.ID, &op.Email, &op.FullName, &op.Status,
				&op.MustChangePassword, &op.MFAEnabled, &op.LastLoginAt,
				&op.CreatedAt); e != nil {
				return e
			}
			op.Self = op.ID == a.UserID
			out = append(out, op)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// NewOperator is the result of adding one: the account, and its one-time
// password.
type NewOperator struct {
	Operator          Operator `json:"operator"`
	TemporaryPassword string   `json:"temporary_password"`
}

// AddOperator creates another platform administrator.
//
// The password is generated here and shown once, exactly as bootstrap does it,
// and `must_change_password` is set: this one travels through whatever channel
// the administrator uses to pass it on, so the account must not keep working
// on it.
func (s *Service) AddOperator(
	ctx context.Context, email, fullName string,
) (NewOperator, error) {
	a := actor.From(ctx)
	if !a.IsSuperAdmin {
		return NewOperator{}, errs.New(errs.CodeForbidden,
			"Only a platform administrator can add another.")
	}

	email, err := operatorEmail(email)
	if err != nil {
		return NewOperator{}, err
	}
	fullName = strings.TrimSpace(fullName)
	if fullName == "" {
		return NewOperator{}, errs.New(errs.CodeInvalidInput,
			"Give this administrator a name. It is what the audit log will "+
				"show against everything they do.")
	}

	temp, err := GenerateTemporaryPassword()
	if err != nil {
		return NewOperator{}, err
	}
	hash, err := HashPassword(temp)
	if err != nil {
		return NewOperator{}, err
	}

	var op Operator
	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			INSERT INTO app_user
			  (tenant_id, email, full_name, password_hash,
			   must_change_password, status)
			VALUES (NULL, $1, $2, $3, true, 'active')
			RETURNING id, email, full_name, status::text, must_change_password,
			          mfa_enabled,
			          to_char(created_at AT TIME ZONE 'UTC',
			                  'YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
			email, fullName, hash).
			Scan(&op.ID, &op.Email, &op.FullName, &op.Status,
				&op.MustChangePassword, &op.MFAEnabled, &op.CreatedAt); e != nil {
			return e
		}
		return writeAudit(ctx, tx, auditEntry{
			ActorID: &a.UserID, ActorLabel: audit.LabelFor(ctx, tx, a.UserID),
			Action:     "platform_operator_added",
			EntityType: "app_user", EntityID: &op.ID,
			After: map[string]any{"email": op.Email, "full_name": op.FullName},
		})
	})
	if err != nil {
		return NewOperator{}, db.Translate(err,
			"A platform administrator already uses that email address.")
	}

	return NewOperator{Operator: op, TemporaryPassword: temp}, nil
}

// ChangeOperatorEmail corrects the address a platform administrator signs in
// with.
//
// Sessions are left alone deliberately. An email is an identifier, not a
// credential: the password is unchanged, and signing somebody out mid-task
// because their address was corrected would be a surprise with no security
// value behind it.
func (s *Service) ChangeOperatorEmail(
	ctx context.Context, targetID uuid.UUID, email string,
) (Operator, error) {
	a := actor.From(ctx)
	if !a.IsSuperAdmin {
		return Operator{}, errs.New(errs.CodeForbidden,
			"Only a platform administrator can change this.")
	}

	email, err := operatorEmail(email)
	if err != nil {
		return Operator{}, err
	}

	var op Operator
	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Read first, and refuse a tenant user here rather than updating one.
		// This route is reachable only by a platform administrator, and a
		// business's staff are managed by that business: an operator editing
		// them from the control plane is exactly the interference the actor
		// model exists to prevent.
		var before string
		var tenantID *uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT email, tenant_id FROM app_user WHERE id = $1`, targetID).
			Scan(&before, &tenantID); e != nil {
			return e
		}
		if tenantID != nil {
			return errs.New(errs.CodeInvalidInput,
				"That account belongs to a business, not to the platform. "+
					"Its own administrators manage it.")
		}

		if _, e := tx.Exec(ctx,
			`UPDATE app_user SET email = $2, updated_at = now() WHERE id = $1`,
			targetID, email); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			SELECT id, email, full_name, status::text, must_change_password,
			       mfa_enabled,
			       to_char(last_login_at AT TIME ZONE 'UTC',
			               'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
			       to_char(created_at AT TIME ZONE 'UTC',
			               'YYYY-MM-DD"T"HH24:MI:SS"Z"')
			FROM app_user WHERE id = $1`, targetID).
			Scan(&op.ID, &op.Email, &op.FullName, &op.Status,
				&op.MustChangePassword, &op.MFAEnabled, &op.LastLoginAt,
				&op.CreatedAt); e != nil {
			return e
		}
		return writeAudit(ctx, tx, auditEntry{
			ActorID: &a.UserID, ActorLabel: audit.LabelFor(ctx, tx, a.UserID),
			Action:     "platform_operator_email_changed",
			EntityType: "app_user", EntityID: &targetID,
			Before: map[string]any{"email": before},
			After:  map[string]any{"email": email},
		})
	})
	if err != nil {
		return Operator{}, db.Translate(err,
			"A platform administrator already uses that email address.")
	}
	op.Self = op.ID == a.UserID
	return op, nil
}

// SetOperatorStatus enables or disables a platform administrator.
//
// Disabling is how access is taken away: the row stays, because every audit
// entry they ever wrote points at it, and deleting the user would leave the
// trail naming nobody.
func (s *Service) SetOperatorStatus(
	ctx context.Context, targetID uuid.UUID, status string,
) (Operator, error) {
	a := actor.From(ctx)
	if !a.IsSuperAdmin {
		return Operator{}, errs.New(errs.CodeForbidden,
			"Only a platform administrator can change this.")
	}

	status = strings.TrimSpace(strings.ToLower(status))
	if status != "active" && status != "disabled" {
		return Operator{}, errs.New(errs.CodeInvalidInput,
			"A platform administrator is either active or disabled.")
	}
	if status == "disabled" && targetID == a.UserID {
		return Operator{}, errs.New(errs.CodeInvalidInput,
			"You cannot disable your own administrator account. Ask another "+
				"administrator to do it, so somebody is always left who can "+
				"undo it.")
	}

	var op Operator
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var before string
		var tenantID *uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT status::text, tenant_id FROM app_user WHERE id = $1`, targetID).
			Scan(&before, &tenantID); e != nil {
			return e
		}
		if tenantID != nil {
			return errs.New(errs.CodeInvalidInput,
				"That account belongs to a business, not to the platform.")
		}

		// The backstop, counted inside this transaction so it cannot race.
		//
		// Refusing to disable yourself already covers the ordinary case; this
		// covers a caller holding platform authority who is not one of the
		// rows being counted, and it is the only thing standing between that
		// caller and a deployment with no administrator at all.
		if status == "disabled" {
			var active int
			if e := tx.QueryRow(ctx, `
				SELECT count(*) FROM app_user
				WHERE tenant_id IS NULL AND status = 'active' AND id <> $1`,
				targetID).Scan(&active); e != nil {
				return e
			}
			if active == 0 {
				return errs.New(errs.CodeInvalidInput,
					"This is the last active platform administrator. "+
						"Disabling it would leave nobody able to administer "+
						"the platform, and nothing can create a replacement "+
						"on a running deployment. Add another administrator "+
						"first.")
			}
		}

		// `failed_attempts` and `locked_until` are cleared when re-enabling:
		// an account disabled while locked out would otherwise come back
		// still locked, which reads as the re-enabling not having worked.
		if _, e := tx.Exec(ctx, `
			UPDATE app_user
			SET status = $2::user_status, updated_at = now(),
			    failed_attempts = CASE WHEN $2 = 'active' THEN 0 ELSE failed_attempts END,
			    locked_until = CASE WHEN $2 = 'active' THEN NULL ELSE locked_until END
			WHERE id = $1`, targetID, status); e != nil {
			return e
		}

		// Every session goes when access is taken away. A disabled account
		// with a live token is access that has not actually been taken away.
		if status == "disabled" {
			if _, e := tx.Exec(ctx, `
				UPDATE user_session
				SET revoked_at = now(),
				    revoked_reason = 'platform administrator disabled'
				WHERE user_id = $1 AND revoked_at IS NULL`, targetID); e != nil {
				return e
			}
		}

		if e := tx.QueryRow(ctx, `
			SELECT id, email, full_name, status::text, must_change_password,
			       mfa_enabled,
			       to_char(last_login_at AT TIME ZONE 'UTC',
			               'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
			       to_char(created_at AT TIME ZONE 'UTC',
			               'YYYY-MM-DD"T"HH24:MI:SS"Z"')
			FROM app_user WHERE id = $1`, targetID).
			Scan(&op.ID, &op.Email, &op.FullName, &op.Status,
				&op.MustChangePassword, &op.MFAEnabled, &op.LastLoginAt,
				&op.CreatedAt); e != nil {
			return e
		}
		return writeAudit(ctx, tx, auditEntry{
			ActorID: &a.UserID, ActorLabel: audit.LabelFor(ctx, tx, a.UserID),
			Action:     "platform_operator_status_changed",
			EntityType: "app_user", EntityID: &targetID,
			Before: map[string]any{"status": before},
			After:  map[string]any{"status": status},
		})
	})
	if err != nil {
		return Operator{}, db.Translate(err, "That administrator does not exist.")
	}
	op.Self = op.ID == a.UserID
	return op, nil
}

// operatorEmail normalises and checks a sign-in address.
//
// Lower-cased because the column is `citext` and a stored address that differs
// from what somebody types by a capital letter is a support call nobody can
// diagnose from a screenshot.
func operatorEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.Index(email, "@")
	if at < 1 || at == len(email)-1 || strings.Contains(email, " ") {
		return "", errs.New(errs.CodeInvalidInput,
			fmt.Sprintf("%q is not an email address.", email))
	}
	return email, nil
}
