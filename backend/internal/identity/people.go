// Staff, and who is allowed to create them.
//
// # Why this exists
//
// Blueprint A5 makes the Owner "fully self-sufficient" after Super Admin
// creates their first login, and A6 gives them a whole permission model to
// delegate with — twelve predefined roles, a custom role builder, and four
// scope dimensions. All of that was in the database and none of it had a route.
//
// So a shop could be onboarded and then had no way to create the cashier who
// works the till. Everything downstream assumes those users exist: the cashier
// role, shift ownership, blind close, and the "who counted this drawer" trail
// that makes a cash difference attributable to a person. A one-person shop
// could trade; a shop with a second person could not.
//
// # The two rules that matter
//
// **You cannot grant what you do not hold.** Somebody with
// `identity.manage_roles` may delegate, and delegation is not escalation: the
// role being assigned must be a subset of the assigner's own permissions. A
// store manager given the power to create staff must not be able to create an
// Owner and sign in as them. The check is on the permission SET rather than on
// a role hierarchy, because A6.2 lets an Owner build custom roles and no
// hierarchy would know about those.
//
// **You cannot lock yourself out.** Suspending your own account, or removing
// your own last role, leaves a tenant with a login that can do nothing and no
// way to fix it from inside. Both are refused, and the message says to ask
// another Owner or the platform operator — which is A4.2's route.
package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// PeopleScope is who is asking, and on whose behalf.
type PeopleScope struct {
	TenantID uuid.UUID
	ActorID  uuid.UUID

	// Holds is the caller's own permission set, resolved for this request. The
	// subset rule below is checked against it.
	Holds map[string]bool
}

// Person is one member of staff, with the roles they hold.
type Person struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	FullName string    `json:"full_name"`
	Phone    string    `json:"phone,omitempty"`
	Status   string    `json:"status"`

	// MustChangePassword is true for somebody who has been created and has not
	// signed in yet. The list shows it, because "I never got my password" and
	// "I have not used it yet" look identical otherwise.
	MustChangePassword bool `json:"must_change_password"`

	LastLoginAt *string `json:"last_login_at"`

	// Locked is set while a run of failed attempts is being served out. Shown
	// so an owner can tell a locked account from a suspended one — the first
	// clears itself, the second does not.
	Locked bool `json:"locked"`

	Roles []Assignment `json:"roles"`
}

// Assignment is one role a person holds, and where it applies.
type Assignment struct {
	ID        uuid.UUID  `json:"id"`
	RoleID    uuid.UUID  `json:"role_id"`
	RoleKey   string     `json:"role_key"`
	RoleName  string     `json:"role_name"`
	CompanyID *uuid.UUID `json:"company_id"`

	// StoreIDs and WarehouseIDs empty mean every store and every warehouse,
	// which is what the column default says and what the resolver reads.
	StoreIDs     []uuid.UUID `json:"store_ids"`
	WarehouseIDs []uuid.UUID `json:"warehouse_ids"`

	// AmountLimit is the transaction ceiling from A6.2. Empty means unlimited.
	AmountLimit string `json:"amount_limit,omitempty"`

	ValidFrom  *string `json:"valid_from"`
	ValidUntil *string `json:"valid_until"`
}

// RoleOption is a role that can be assigned, with what it can do.
type RoleOption struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`

	// Permissions is what the role carries. Sent so the screen can show an
	// owner what they are about to hand over, rather than a name alone.
	Permissions []string `json:"permissions"`

	// Assignable is false when the caller does not hold everything the role
	// does. Sent rather than filtered out, with `WithheldPermissions` saying
	// which — an owner who cannot see why a role is greyed out will assume the
	// product is broken.
	Assignable          bool     `json:"assignable"`
	WithheldPermissions []string `json:"withheld_permissions,omitempty"`
}

// NewPerson is somebody being added to the business.
type NewPerson struct {
	Email    string
	FullName string
	Phone    string

	// RoleID is required. A person with no role can sign in and do nothing,
	// which reads to them as a broken account rather than as a pending one.
	RoleID uuid.UUID

	CompanyID    *uuid.UUID
	StoreIDs     []uuid.UUID
	WarehouseIDs []uuid.UUID
	AmountLimit  string
	ValidFrom    *time.Time
	ValidUntil   *time.Time
}

// Created is a new member of staff and the one-time password to hand them.
type Created struct {
	Person Person `json:"person"`

	// TemporaryPassword is shown ONCE, in the response to the request that
	// created the account, and is never retrievable afterwards. It is stored
	// as an argon2id hash like every other — blueprint A4.2 calls the
	// irreversibility "a security requirement, not just a policy choice".
	TemporaryPassword string `json:"temporary_password"`
}

// ListRoles returns the roles this tenant can assign, marking the ones the
// caller may actually hand over.
func (s *Service) ListRoles(ctx context.Context, scope PeopleScope) ([]RoleOption, error) {
	out := []RoleOption{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT r.id, r.key, r.name, coalesce(r.description, ''),
			       coalesce(array_agg(rp.permission ORDER BY rp.permission)
			                FILTER (WHERE rp.permission IS NOT NULL), '{}')
			FROM role r
			LEFT JOIN role_permission rp ON rp.role_id = r.id
			WHERE r.tenant_id IS NULL OR r.tenant_id = current_tenant_id()
			GROUP BY r.id, r.key, r.name, r.description
			ORDER BY r.name`)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var o RoleOption
			if e := rows.Scan(&o.ID, &o.Key, &o.Name, &o.Description,
				&o.Permissions); e != nil {
				return e
			}
			o.WithheldPermissions = withheld(o.Permissions, scope.Holds)
			o.Assignable = len(o.WithheldPermissions) == 0
			out = append(out, o)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// withheld lists what a role carries that the caller does not.
func withheld(rolePermissions []string, holds map[string]bool) []string {
	var out []string
	for _, p := range rolePermissions {
		if !holds[p] {
			out = append(out, p)
		}
	}
	return out
}

// ListPeople returns everybody in the tenant with the roles they hold.
func (s *Service) ListPeople(
	ctx context.Context, scope PeopleScope, includeInactive bool,
) ([]Person, error) {
	out := []Person{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT u.id, u.email, u.full_name, coalesce(u.phone, ''),
			       u.status::text, u.must_change_password,
			       u.last_login_at,
			       (u.locked_until IS NOT NULL AND u.locked_until > now())
			FROM app_user u
			WHERE u.tenant_id = current_tenant_id()
			  AND ($1 OR u.status <> 'disabled')
			ORDER BY u.full_name`, includeInactive)
		if e != nil {
			return e
		}
		defer rows.Close()

		byID := map[uuid.UUID]int{}
		for rows.Next() {
			var p Person
			var lastLogin *time.Time
			if e := rows.Scan(&p.ID, &p.Email, &p.FullName, &p.Phone,
				&p.Status, &p.MustChangePassword, &lastLogin, &p.Locked); e != nil {
				return e
			}
			if lastLogin != nil {
				at := lastLogin.Format(time.RFC3339)
				p.LastLoginAt = &at
			}
			p.Roles = []Assignment{}
			byID[p.ID] = len(out)
			out = append(out, p)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		if len(out) == 0 {
			return nil
		}

		// The assignments in one further query rather than one per person.
		arows, e := tx.Query(ctx, `
			SELECT a.id, a.user_id, a.role_id, r.key, r.name, a.company_id,
			       a.store_ids, a.warehouse_ids, a.amount_limit,
			       a.valid_from, a.valid_until
			FROM user_role_assignment a
			JOIN role r ON r.id = a.role_id
			WHERE a.tenant_id = current_tenant_id()
			ORDER BY r.name`)
		if e != nil {
			return e
		}
		defer arows.Close()

		for arows.Next() {
			var (
				a      Assignment
				userID uuid.UUID
				limit  *decimal.Decimal
				from   *time.Time
				until  *time.Time
			)
			if e := arows.Scan(&a.ID, &userID, &a.RoleID, &a.RoleKey, &a.RoleName,
				&a.CompanyID, &a.StoreIDs, &a.WarehouseIDs, &limit,
				&from, &until); e != nil {
				return e
			}
			if limit != nil {
				a.AmountLimit = limit.StringFixed(2)
			}
			if from != nil {
				v := from.Format(time.RFC3339)
				a.ValidFrom = &v
			}
			if until != nil {
				v := until.Format(time.RFC3339)
				a.ValidUntil = &v
			}
			if i, ok := byID[userID]; ok {
				out[i].Roles = append(out[i].Roles, a)
			}
		}
		return arows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// CreatePerson adds a member of staff and issues their first password.
func (s *Service) CreatePerson(
	ctx context.Context, scope PeopleScope, in NewPerson,
) (Created, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	in.FullName = strings.TrimSpace(in.FullName)
	in.Phone = strings.TrimSpace(in.Phone)

	if err := validatePerson(in); err != nil {
		return Created{}, err
	}

	temporary, err := GenerateTemporaryPassword()
	if err != nil {
		return Created{}, err
	}
	hash, err := HashPassword(temporary)
	if err != nil {
		return Created{}, err
	}

	var out Created
	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := s.checkUserCeiling(ctx, tx, scope.TenantID); e != nil {
			return e
		}
		if e := s.checkRoleIsGrantable(ctx, tx, scope, in.RoleID); e != nil {
			return e
		}

		var userID uuid.UUID
		e := tx.QueryRow(ctx, `
			INSERT INTO app_user
			  (tenant_id, email, full_name, phone, password_hash, status,
			   must_change_password)
			VALUES (current_tenant_id(), $1, $2, nullif($3,''), $4, 'invited', true)
			RETURNING id`,
			in.Email, in.FullName, in.Phone, hash).Scan(&userID)
		if isUniqueViolation(e) {
			return errs.Newf(errs.CodeConflict,
				"Somebody in this business already uses %s. Every person signs "+
					"in with their own address, so two people cannot share one.",
				in.Email)
		}
		if e != nil {
			return e
		}

		if e := insertAssignment(ctx, tx, scope, userID, RoleGrant{
			RoleID:       in.RoleID,
			CompanyID:    in.CompanyID,
			StoreIDs:     in.StoreIDs,
			WarehouseIDs: in.WarehouseIDs,
			AmountLimit:  in.AmountLimit,
			ValidFrom:    in.ValidFrom,
			ValidUntil:   in.ValidUntil,
		}); e != nil {
			return e
		}

		out.Person = Person{
			ID: userID, Email: in.Email, FullName: in.FullName,
			Phone: in.Phone, Status: "invited", MustChangePassword: true,
		}
		return nil
	})
	if err != nil {
		return Created{}, db.Translate(err, "")
	}
	out.TemporaryPassword = temporary
	return out, nil
}

func validatePerson(in NewPerson) error {
	e := errs.Validation("Some details are missing.")
	bad := false
	if in.FullName == "" {
		e.WithField("full_name", "Enter this person's name.")
		bad = true
	}
	if !strings.Contains(in.Email, "@") || len(in.Email) < 3 {
		e.WithField("email",
			"Enter the email address this person will sign in with.")
		bad = true
	}
	if in.RoleID == uuid.Nil {
		// A person with no role can sign in and do nothing, which reads to
		// them as a broken account rather than a pending one.
		e.WithField("role_id", "Choose what this person is allowed to do.")
		bad = true
	}
	if in.AmountLimit != "" {
		if v, err := decimal.NewFromString(in.AmountLimit); err != nil || v.IsNegative() {
			e.WithField("amount_limit",
				"A transaction limit is an amount, or empty for no limit.")
			bad = true
		}
	}
	if in.ValidFrom != nil && in.ValidUntil != nil &&
		!in.ValidUntil.After(*in.ValidFrom) {
		e.WithField("valid_until", "The end of the window is before its start.")
		bad = true
	}
	if bad {
		return e
	}
	return nil
}

// checkUserCeiling enforces the plan's `max_users`.
//
// Blueprint A3 requires every "unlimited" claim to become a concrete, testable
// ceiling. The row is per tenant and Super Admin can raise it, so this refuses
// with the number rather than with a generic limit message.
func (s *Service) checkUserCeiling(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID,
) error {
	var ceiling, used int
	if err := tx.QueryRow(ctx, `
		SELECT l.max_users,
		       (SELECT count(*) FROM app_user u
		         WHERE u.tenant_id = $1 AND u.status <> 'disabled')
		FROM tenant_limit l WHERE l.tenant_id = $1`, tenantID).
		Scan(&ceiling, &used); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No row means no ceiling was provisioned, which is a provisioning
			// fault rather than a licence the caller has earned.
			return errs.New(errs.CodeInternal,
				"This business has no user allowance on record.")
		}
		return err
	}
	if used >= ceiling {
		return errs.Newf(errs.CodeLimitReached,
			"Your plan allows %d people and you already have %d. Ask your "+
				"platform operator to raise it, or disable somebody who has left.",
			ceiling, used)
	}
	return nil
}

// checkRoleIsGrantable refuses to hand over more than the caller holds.
//
// Delegation is not escalation. Somebody with `identity.manage_roles` may give
// away what they have; giving away what they do not have is how a store
// manager becomes an Owner. Checked on the permission SET rather than on a
// hierarchy of role names, because A6.2 lets an Owner build custom roles and no
// hierarchy would know what those contain.
func (s *Service) checkRoleIsGrantable(
	ctx context.Context, tx pgx.Tx, scope PeopleScope, roleID uuid.UUID,
) error {
	var (
		name        string
		permissions []string
	)
	err := tx.QueryRow(ctx, `
		SELECT r.name,
		       coalesce(array_agg(rp.permission ORDER BY rp.permission)
		                FILTER (WHERE rp.permission IS NOT NULL), '{}')
		FROM role r
		LEFT JOIN role_permission rp ON rp.role_id = r.id
		WHERE r.id = $1 AND (r.tenant_id IS NULL OR r.tenant_id = current_tenant_id())
		GROUP BY r.name`, roleID).Scan(&name, &permissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, "That role was not found.")
	}
	if err != nil {
		return err
	}

	missing := withheld(permissions, scope.Holds)
	if len(missing) == 0 {
		return nil
	}
	return errs.Newf(errs.CodeForbidden,
		"You cannot give somebody %s, because it includes %s and your own role "+
			"does not. Ask an owner to make this assignment.",
		name, strings.Join(missing, ", "))
}

// RoleGrant is a role being given to somebody, and where it applies.
//
// Exported because `AssignRoleTo` takes it and the HTTP layer builds it. An
// unexported parameter type on an exported method is a method no other package
// can call, which is a signature that compiles and cannot be used.
type RoleGrant struct {
	RoleID       uuid.UUID
	CompanyID    *uuid.UUID
	StoreIDs     []uuid.UUID
	WarehouseIDs []uuid.UUID
	AmountLimit  string
	ValidFrom    *time.Time
	ValidUntil   *time.Time
}

func insertAssignment(
	ctx context.Context, tx pgx.Tx, scope PeopleScope,
	userID uuid.UUID, in RoleGrant,
) error {
	var limit *decimal.Decimal
	if in.AmountLimit != "" {
		v, err := decimal.NewFromString(in.AmountLimit)
		if err != nil {
			return errs.New(errs.CodeInvalidInput,
				"A transaction limit is an amount, or empty for no limit.")
		}
		limit = &v
	}
	if in.StoreIDs == nil {
		in.StoreIDs = []uuid.UUID{}
	}
	if in.WarehouseIDs == nil {
		in.WarehouseIDs = []uuid.UUID{}
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO user_role_assignment
		  (tenant_id, user_id, role_id, company_id, store_ids, warehouse_ids,
		   amount_limit, valid_from, valid_until, created_by)
		VALUES (current_tenant_id(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		userID, in.RoleID, in.CompanyID, in.StoreIDs, in.WarehouseIDs,
		limit, in.ValidFrom, in.ValidUntil, scope.ActorID)
	return err
}

// AssignRole gives somebody another role, or a narrower one.
func (s *Service) AssignRoleTo(
	ctx context.Context, scope PeopleScope, userID uuid.UUID, in RoleGrant,
) error {
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requirePersonExists(ctx, tx, userID); e != nil {
			return e
		}
		if e := s.checkRoleIsGrantable(ctx, tx, scope, in.RoleID); e != nil {
			return e
		}
		return insertAssignment(ctx, tx, scope, userID, in)
	})
	if err != nil {
		return db.Translate(err, "")
	}
	// A change to what somebody may do takes effect on their next request, not
	// on their next sign-in: their existing access token still carries the old
	// grants until it expires.
	return s.RevokeAllForUser(ctx, userID, "roles changed")
}

// RemoveAssignment takes a role away.
func (s *Service) RemoveAssignment(
	ctx context.Context, scope PeopleScope, assignmentID uuid.UUID,
) error {
	var userID uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT user_id FROM user_role_assignment
			WHERE id = $1 AND tenant_id = current_tenant_id()`, assignmentID).
			Scan(&userID)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That assignment was not found.")
		}
		if e != nil {
			return e
		}

		// Removing your own last role leaves you signed in and able to do
		// nothing, with no way to put it back.
		if userID == scope.ActorID {
			var left int
			if e := tx.QueryRow(ctx, `
				SELECT count(*) FROM user_role_assignment
				WHERE user_id = $1 AND id <> $2
				  AND tenant_id = current_tenant_id()`,
				userID, assignmentID).Scan(&left); e != nil {
				return e
			}
			if left == 0 {
				return errs.New(errs.CodeConflict,
					"This is your own last role, and removing it would leave you "+
						"signed in and unable to do anything. Ask another owner to "+
						"change it, or your platform operator.")
			}
		}

		_, e = tx.Exec(ctx, `
			DELETE FROM user_role_assignment
			WHERE id = $1 AND tenant_id = current_tenant_id()`, assignmentID)
		return e
	})
	if err != nil {
		return db.Translate(err, "")
	}
	return s.RevokeAllForUser(ctx, userID, "roles changed")
}

// UpdatePerson changes a name, a phone number or an email.
func (s *Service) UpdatePerson(
	ctx context.Context, scope PeopleScope, userID uuid.UUID,
	fullName, phone, email string,
) error {
	fullName = strings.TrimSpace(fullName)
	email = strings.TrimSpace(strings.ToLower(email))
	if fullName == "" {
		return errs.Validation("Some details are missing.").
			WithField("full_name", "Enter this person's name.")
	}
	if !strings.Contains(email, "@") || len(email) < 3 {
		return errs.Validation("Some details are missing.").
			WithField("email", "Enter the email address this person signs in with.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE app_user
			   SET full_name = $2, phone = nullif($3,''), email = $4
			 WHERE id = $1 AND tenant_id = current_tenant_id()`,
			userID, fullName, strings.TrimSpace(phone), email)
		if isUniqueViolation(e) {
			return errs.Newf(errs.CodeConflict,
				"Somebody in this business already uses %s.", email)
		}
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That person was not found.")
		}
		return nil
	})
	return db.Translate(err, "")
}

// SetPersonStatus suspends or restores somebody.
//
// `disabled` rather than deleted, always. A person who has rung up sales is
// named on those invoices, on the shift they counted and in the audit log;
// deleting the row would leave a trail of missing people, which is precisely
// what an audit trail exists to prevent.
func (s *Service) SetPersonStatus(
	ctx context.Context, scope PeopleScope, userID uuid.UUID, active bool,
) error {
	if userID == scope.ActorID && !active {
		return errs.New(errs.CodeConflict,
			"You cannot suspend your own account. Ask another owner to do it, "+
				"or your platform operator.")
	}

	next := "disabled"
	if active {
		next = "active"
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requirePersonExists(ctx, tx, userID); e != nil {
			return e
		}
		// Reactivating somebody who never signed in puts them back to
		// `invited`, not to `active`: their password is still the one-time
		// one, and `active` would say they had used it.
		_, e := tx.Exec(ctx, `
			UPDATE app_user
			   SET status = CASE
			         WHEN $2::text = 'disabled' THEN 'disabled'::user_status
			         WHEN must_change_password  THEN 'invited'::user_status
			         ELSE 'active'::user_status
			       END,
			       failed_attempts = 0,
			       locked_until = NULL
			 WHERE id = $1 AND tenant_id = current_tenant_id()`, userID, next)
		return e
	})
	if err != nil {
		return db.Translate(err, "")
	}
	if !active {
		// A suspended person holding a live access token can keep working
		// until it expires. Ending their sessions is the point of suspending.
		return s.RevokeAllForUser(ctx, userID, "account suspended")
	}
	return nil
}

// ResetPersonPassword issues a new one-time password for a member of staff.
//
// The Owner-level twin of A4.2's Super-Admin-assisted recovery, and the same
// shape: a new one-time password is issued, the old one is never revealed
// because it was never stored, every session is ended, and the next sign-in
// must change it.
func (s *Service) ResetPersonPassword(
	ctx context.Context, scope PeopleScope, userID uuid.UUID,
) (string, error) {
	if userID == scope.ActorID {
		// Resetting your own password here would hand you a temporary one and
		// end your session, which is a worse route to the same place than the
		// change-password screen you are already signed in to use.
		return "", errs.New(errs.CodeInvalidInput,
			"To change your own password, use Change password.")
	}

	temporary, err := GenerateTemporaryPassword()
	if err != nil {
		return "", err
	}
	hash, err := HashPassword(temporary)
	if err != nil {
		return "", err
	}

	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE app_user
			   SET password_hash = $2, must_change_password = true,
			       status = CASE WHEN status = 'disabled'
			                     THEN status ELSE 'invited'::user_status END,
			       failed_attempts = 0, locked_until = NULL
			 WHERE id = $1 AND tenant_id = current_tenant_id()`, userID, hash)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That person was not found.")
		}
		return nil
	})
	if err != nil {
		return "", db.Translate(err, "")
	}
	if err := s.RevokeAllForUser(ctx, userID, "password reset by an owner"); err != nil {
		return "", err
	}
	return temporary, nil
}

func requirePersonExists(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM app_user
			 WHERE id = $1 AND tenant_id = current_tenant_id())`,
		userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errs.New(errs.CodeNotFound, "That person was not found.")
	}
	return nil
}

// isUniqueViolation reports whether an error is Postgres' 23505.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLSTATE 23505")
}
