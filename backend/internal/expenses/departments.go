// Cost centres an expense can be booked to (blueprint C3.1).
//
// C3.1 lists what every expense entry stores, and "department" is on that list
// beside the store, the head and the vendor. What it is for is D1's question —
// "see where every cost is going, per day", filterable by range — which means
// the department has to be a dimension somebody can group by.
//
// # Why a table when the employee's department is free text
//
// `employee.department` is text, and following that precedent here would have
// been the easy thing and the wrong one. A dimension you filter by cannot be
// free text: "Sales", "sales" and "Sales " are three departments to a GROUP BY
// and one to the person who typed them, and the report that results is wrong in
// a way nobody notices because it still adds up.
//
// # Deactivated, never deleted
//
// A department that has been spent against is part of the history of every
// expense booked to it. The expense's reference is ON DELETE RESTRICT, so one
// in use cannot be removed at all; `is_active` is how it stops being offered on
// new expenses. Last year's report still names the department last year's money
// actually went to, which is what makes it reproducible.
package expenses

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Department is a cost centre.
type Department struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	NameAr   string    `json:"name_ar,omitempty"`
	IsActive bool      `json:"is_active"`
}

// NewDepartment is one being created or amended.
type NewDepartment struct {
	Code   string
	Name   string
	NameAr string
}

// Departments lists them, active first.
func (s *Service) Departments(
	ctx context.Context, scope Scope, includeInactive bool,
) ([]Department, error) {
	out := []Department{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, code, name, coalesce(name_ar, ''), is_active
			FROM department
			WHERE company_id = $1 AND ($2 OR is_active)
			ORDER BY is_active DESC, code`,
			scope.CompanyID, includeInactive)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var d Department
			if e := rows.Scan(&d.ID, &d.Code, &d.Name, &d.NameAr,
				&d.IsActive); e != nil {
				return e
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// CreateDepartment adds one.
func (s *Service) CreateDepartment(
	ctx context.Context, scope Scope, in NewDepartment,
) (Department, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	if in.Code == "" || in.Name == "" {
		return Department{}, errs.Validation(
			"A department needs a code and a name.").
			WithField("code", "Short, and how it appears in reports.")
	}

	var out Department
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			INSERT INTO department
			  (tenant_id, company_id, code, name, name_ar, created_by)
			VALUES ($1,$2,$3,$4,nullif(btrim($5),''),$6)
			RETURNING id, code, name, coalesce(name_ar, ''), is_active`,
			scope.TenantID, scope.CompanyID, in.Code, in.Name, in.NameAr,
			scope.UserID).
			Scan(&out.ID, &out.Code, &out.Name, &out.NameAr, &out.IsActive)
		if e != nil {
			return db.Translate(e,
				"A department with that code already exists.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "department_created",
			EntityType: "department", EntityID: &out.ID,
			After: map[string]any{"code": out.Code, "name": out.Name},
		})
	})
	return out, db.Translate(err, "")
}

// UpdateDepartment renames one. The code is not changed: it is what reports
// already made of it.
func (s *Service) UpdateDepartment(
	ctx context.Context, scope Scope, id uuid.UUID, in NewDepartment,
) (Department, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Department{}, errs.Validation("A department needs a name.")
	}

	var out Department
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			UPDATE department
			SET name = $3, name_ar = nullif(btrim($4), '')
			WHERE id = $1 AND company_id = $2
			RETURNING id, code, name, coalesce(name_ar, ''), is_active`,
			id, scope.CompanyID, in.Name, in.NameAr).
			Scan(&out.ID, &out.Code, &out.Name, &out.NameAr, &out.IsActive)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That department was not found.")
		}
		return e
	})
	return out, db.Translate(err, "")
}

// SetDepartmentActive stops one being offered, or offers it again.
//
// Deactivating is the only way to retire a department. Deleting is refused by
// the expense's foreign key once anything has been booked to it, and that is
// the behaviour that keeps last year's report reproducible.
func (s *Service) SetDepartmentActive(
	ctx context.Context, scope Scope, id uuid.UUID, active bool,
) (Department, error) {
	var out Department
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			UPDATE department SET is_active = $3
			WHERE id = $1 AND company_id = $2
			RETURNING id, code, name, coalesce(name_ar, ''), is_active`,
			id, scope.CompanyID, active).
			Scan(&out.ID, &out.Code, &out.Name, &out.NameAr, &out.IsActive)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That department was not found.")
		}
		if e != nil {
			return e
		}
		action := "department_deactivated"
		if active {
			action = "department_reactivated"
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     action,
			EntityType: "department", EntityID: &out.ID,
			After: map[string]any{"code": out.Code, "is_active": active},
		})
	})
	return out, db.Translate(err, "")
}

// requireDepartment checks a department belongs to this company and can still
// be booked to.
//
// Caller-supplied, so it is checked the same way an account id is: another
// company's department sits in the same tenant, where row-level security sees
// nothing wrong with it.
func requireDepartment(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) error {
	var active bool
	err := tx.QueryRow(ctx,
		`SELECT is_active FROM department WHERE id = $1 AND company_id = $2`,
		id, companyID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeNotFound, "That department was not found.")
	}
	if err != nil {
		return err
	}
	if !active {
		return errs.New(errs.CodeConflict,
			"That department has been retired, so nothing new can be booked "+
				"to it. Reactivate it, or choose another.")
	}
	return nil
}
