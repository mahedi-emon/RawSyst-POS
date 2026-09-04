// Expenses that fall due on a schedule (blueprint C3.1).
//
// Rent, a subscription, the cleaner's monthly fee. The shop agrees the amount
// once and it recurs; somebody re-typing it every month is how it gets
// forgotten in a busy month and entered twice in a slow one.
//
// # The schedule posts nothing
//
// A schedule describes an expense and says when the next one is due. `Generate`
// turns a due schedule into an ordinary expense by calling `Record` — the same
// path a person typing one takes — so the tax treatment, the posting rules, the
// approval thresholds, the numbering and the audit record are all the ones
// expenses already have. A second posting path would be a second set of rules
// to keep in step, and they would not stay in step.
//
// # Running it twice must not pay the rent twice
//
// The guard is a UNIQUE index on (schedule, due date) in
// `recurring_expense_run`, not a check this code performs. Two workers racing
// for the same period both try to insert that row and exactly one succeeds; the
// loser finds the expense the winner made and reports it rather than failing.
// A generator that avoided duplicates by looking first would be correct until
// the day two of them looked at the same moment.
//
// # Dates, and what is deliberately not clever
//
// `next_due_on` is stored, not derived. A monthly schedule starting on the 31st
// has no 31st in February, and every rule for "the same day next month" is a
// judgement somebody has to be able to see and override. So the date advances
// by one interval and is written down, clamped to the end of a short month, and
// an operator can move it.
//
// Missed periods are caught up one at a time rather than collapsed: a schedule
// that has not run for three months generates three expenses, because three
// months of rent were owed and one entry would understate the cost of two of
// them.
package expenses

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Recurring is a schedule as a screen reads it.
type Recurring struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	HeadID       uuid.UUID  `json:"head_id"`
	Head         string     `json:"head,omitempty"`
	StoreID      *uuid.UUID `json:"store_id,omitempty"`
	SupplierID   *uuid.UUID `json:"supplier_id,omitempty"`
	DepartmentID *uuid.UUID `json:"department_id,omitempty"`
	Amount       string     `json:"amount"`
	Currency     string     `json:"currency"`
	PaidFrom     string     `json:"paid_from"`
	Description  string     `json:"description,omitempty"`
	Frequency    string     `json:"frequency"`
	Interval     int        `json:"interval_count"`
	StartsOn     string     `json:"starts_on"`
	EndsOn       string     `json:"ends_on,omitempty"`
	NextDueOn    string     `json:"next_due_on"`
	IsActive     bool       `json:"is_active"`
}

// NewRecurring is a schedule being created.
type NewRecurring struct {
	Name         string
	HeadID       uuid.UUID
	StoreID      *uuid.UUID
	SupplierID   *uuid.UUID
	DepartmentID *uuid.UUID
	Amount       decimal.Decimal
	PaidFrom     string
	Description  string
	Frequency    string
	Interval     int
	StartsOn     time.Time
	EndsOn       *time.Time
}

// CreateRecurring adds a schedule.
func (s *Service) CreateRecurring(
	ctx context.Context, scope Scope, in NewRecurring,
) (Recurring, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Recurring{}, errs.Validation("Give this schedule a name.").
			WithField("name", "What it is for — \"Shop rent\", \"Internet\".")
	}
	if !in.Amount.IsPositive() {
		return Recurring{}, errs.Validation(
			"A recurring expense needs an amount above zero.")
	}
	switch in.Frequency {
	case "weekly", "monthly", "yearly":
	default:
		return Recurring{}, errs.Validation(
			"A schedule repeats weekly, monthly or yearly.").
			WithField("frequency",
				"Use interval_count for anything else: monthly every 3 is "+
					"quarterly.")
	}
	if in.Interval <= 0 {
		in.Interval = 1
	}
	if in.StartsOn.IsZero() {
		return Recurring{}, errs.Validation("Say when this starts.")
	}
	if in.EndsOn != nil && in.EndsOn.Before(in.StartsOn) {
		return Recurring{}, errs.Validation(
			"A schedule cannot end before it starts.")
	}
	if in.PaidFrom != "cash" && in.PaidFrom != "bank" {
		return Recurring{}, errs.Validation(
			"Say whether this is paid from cash or from the bank.")
	}

	var out Recurring
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			return e
		}
		if in.DepartmentID != nil {
			if e := requireDepartment(ctx, tx, scope.CompanyID,
				*in.DepartmentID); e != nil {
				return e
			}
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO recurring_expense
			  (tenant_id, company_id, name, head_id, store_id, supplier_id,
			   department_id, amount, currency, paid_from, description,
			   frequency, interval_count, starts_on, ends_on, next_due_on,
			   created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,nullif(btrim($11),''),
			        $12,$13,$14::date,$15,$14::date,$16)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, in.Name, in.HeadID, in.StoreID,
			in.SupplierID, in.DepartmentID, in.Amount, currency, in.PaidFrom,
			in.Description, in.Frequency, in.Interval, in.StartsOn, in.EndsOn,
			scope.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That schedule could not be saved.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "recurring_expense_created",
			EntityType: "recurring_expense", EntityID: &id,
			After: map[string]any{
				"name": in.Name, "amount": in.Amount.String(),
				"frequency": in.Frequency, "interval": in.Interval,
			},
		}); e != nil {
			return e
		}

		var readErr error
		out, readErr = s.readRecurring(ctx, tx, scope.CompanyID, id)
		return readErr
	})
	return out, db.Translate(err, "")
}

// SetRecurringActive pauses a schedule or resumes it.
//
// Pausing rather than deleting: a schedule that produced expenses is the reason
// those expenses exist, and its runs point at it.
func (s *Service) SetRecurringActive(
	ctx context.Context, scope Scope, id uuid.UUID, active bool,
) (Recurring, error) {
	var out Recurring
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE recurring_expense SET is_active = $3
			WHERE id = $1 AND company_id = $2`, id, scope.CompanyID, active)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That schedule was not found.")
		}
		var readErr error
		out, readErr = s.readRecurring(ctx, tx, scope.CompanyID, id)
		return readErr
	})
	return out, db.Translate(err, "")
}

// RecurringList lists the schedules.
func (s *Service) RecurringList(
	ctx context.Context, scope Scope, includeInactive bool,
) ([]Recurring, error) {
	out := []Recurring{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id FROM recurring_expense
			WHERE company_id = $1 AND ($2 OR is_active)
			ORDER BY is_active DESC, next_due_on, name`,
			scope.CompanyID, includeInactive)
		if e != nil {
			return e
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}
		for _, id := range ids {
			r, e := s.readRecurring(ctx, tx, scope.CompanyID, id)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return nil
	})
	return out, db.Translate(err, "")
}

func (s *Service) readRecurring(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Recurring, error) {
	var r Recurring
	var endsOn *string
	var description *string
	err := tx.QueryRow(ctx, `
		SELECT r.id, r.name, r.head_id, h.name, r.store_id, r.supplier_id,
		       r.department_id, r.amount::text, r.currency, r.paid_from,
		       r.description, r.frequency, r.interval_count,
		       to_char(r.starts_on, 'YYYY-MM-DD'),
		       to_char(r.ends_on, 'YYYY-MM-DD'),
		       to_char(r.next_due_on, 'YYYY-MM-DD'), r.is_active
		FROM recurring_expense r
		JOIN expense_head h ON h.id = r.head_id
		WHERE r.id = $1 AND r.company_id = $2`, id, companyID).
		Scan(&r.ID, &r.Name, &r.HeadID, &r.Head, &r.StoreID, &r.SupplierID,
			&r.DepartmentID, &r.Amount, &r.Currency, &r.PaidFrom, &description,
			&r.Frequency, &r.Interval, &r.StartsOn, &endsOn, &r.NextDueOn,
			&r.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recurring{}, errs.New(errs.CodeNotFound,
			"That schedule was not found.")
	}
	if err != nil {
		return Recurring{}, err
	}
	if endsOn != nil {
		r.EndsOn = *endsOn
	}
	if description != nil {
		r.Description = *description
	}
	return r, nil
}

// --- generating --------------------------------------------------------------

// GenerateResult is what one pass over the schedules did.
type GenerateResult struct {
	Created  int      `json:"created"`
	Skipped  int      `json:"skipped"`
	Expenses []string `json:"expenses"`

	// Failed carries the schedules that could not be booked and why, rather
	// than the whole pass failing on the first one. A closed period on one
	// schedule must not stop the rent being booked on another.
	Failed []string `json:"failed,omitempty"`
}

// Generate turns every schedule that has fallen due into an expense.
//
// Catches up one period at a time: a schedule not run for three months produces
// three expenses, because three months of rent were owed and one entry would
// understate two of them. Bounded per schedule so a schedule that starts years
// in the past cannot produce an unbounded run in a single request.
func (s *Service) Generate(
	ctx context.Context, scope Scope, upTo time.Time,
) (GenerateResult, error) {
	if upTo.IsZero() {
		upTo = time.Now().UTC()
	}
	var out GenerateResult

	// Read the due schedules first, in their own transaction. Each expense is
	// then recorded in its own, so one schedule failing — a closed period, a
	// retired head — does not roll back the ones that already worked.
	var due []uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id FROM recurring_expense
			WHERE company_id = $1 AND is_active
			  AND next_due_on <= $2::date
			  AND (ends_on IS NULL OR next_due_on <= ends_on)
			ORDER BY next_due_on`, scope.CompanyID, upTo)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				return e
			}
			due = append(due, id)
		}
		return rows.Err()
	})
	if err != nil {
		return GenerateResult{}, db.Translate(err, "")
	}

	for _, id := range due {
		// At most this many periods caught up per schedule per pass. A schedule
		// that has been dormant for years catches up over several runs rather
		// than holding one request open for hundreds of postings.
		const maxCatchUp = 24
		for i := 0; i < maxCatchUp; i++ {
			made, more, e := s.generateOne(ctx, scope, id, upTo)
			if e != nil {
				// One schedule failing must not stop the others. A period that
				// is closed or was never opened, a head that has been retired
				// — each is a problem with THAT schedule, and aborting the
				// pass would mean one bad row silently stopped the rent from
				// being booked. Recorded and moved past.
				out.Failed = append(out.Failed, e.Error())
				break
			}
			if made != "" {
				out.Created++
				out.Expenses = append(out.Expenses, made)
			} else {
				out.Skipped++
			}
			if !more {
				break
			}
		}
	}
	return out, nil
}

// generateOne produces the next expense for one schedule, if it is due.
//
// Returns the expense number it made (empty when the period had already been
// generated), and whether the schedule is still due after advancing.
func (s *Service) generateOne(
	ctx context.Context, scope Scope, id uuid.UUID, upTo time.Time,
) (string, bool, error) {
	var (
		dueOn       time.Time
		frequency   string
		interval    int
		endsOn      *time.Time
		amount      decimal.Decimal
		paidFrom    string
		headID      uuid.UUID
		storeID     *uuid.UUID
		supplierID  *uuid.UUID
		department  *uuid.UUID
		description *string
		name        string
		treatment   string
		startsOn    time.Time
	)

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// FOR UPDATE: two workers reaching the same schedule queue here rather
		// than both reading the same due date.
		e := tx.QueryRow(ctx, `
			SELECT r.next_due_on, r.frequency, r.interval_count, r.ends_on,
			       r.amount, r.paid_from, r.head_id, r.store_id, r.supplier_id,
			       r.department_id, r.description, r.name, r.starts_on,
			       CASE WHEN h.input_vat_recoverable THEN 'standard'
			            ELSE 'exempt' END
			FROM recurring_expense r
			JOIN expense_head h ON h.id = r.head_id
			WHERE r.id = $1 AND r.company_id = $2 AND r.is_active
			FOR UPDATE OF r`, id, scope.CompanyID).
			Scan(&dueOn, &frequency, &interval, &endsOn, &amount, &paidFrom,
				&headID, &storeID, &supplierID, &department, &description,
				&name, &startsOn, &treatment)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That schedule was not found.")
		}
		return e
	})
	if err != nil {
		return "", false, db.Translate(err, "")
	}
	if dueOn.After(upTo) || (endsOn != nil && dueOn.After(*endsOn)) {
		return "", false, nil
	}

	desc := name
	if description != nil && strings.TrimSpace(*description) != "" {
		desc = strings.TrimSpace(*description)
	}

	// Through Record, the same path a person typing one takes: the tax
	// treatment, posting rules, approval thresholds, numbering and audit are
	// the ones expenses already have.
	made, err := s.Record(ctx, scope, NewExpense{
		UUID:         uuid.New(),
		Date:         dueOn,
		StoreID:      storeID,
		SupplierID:   supplierID,
		DepartmentID: department,
		Description:  desc,
		PaidFrom:     paidFrom,
		Lines: []NewLine{{
			HeadID: headID, Description: desc,
			Net: amount, TaxTreatment: treatment,
		}},
	})
	if err != nil {
		return "", false, err
	}

	next := advance(dueOn, frequency, interval, startsOn.Day())
	claimed := false
	if err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The guard. One row per (schedule, due date): a second attempt at the
		// same period violates the unique index and is refused, whichever
		// worker gets there first.
		tag, e := tx.Exec(ctx, `
			INSERT INTO recurring_expense_run
			  (tenant_id, recurring_expense_id, due_on, expense_id)
			VALUES ($1,$2,$3::date,$4)
			ON CONFLICT (recurring_expense_id, due_on) DO NOTHING`,
			scope.TenantID, id, dueOn, made.ID)
		if e != nil {
			return e
		}
		claimed = tag.RowsAffected() == 1
		if !claimed {
			return nil
		}
		_, e = tx.Exec(ctx, `
			UPDATE recurring_expense SET next_due_on = $2::date
			WHERE id = $1`, id, next)
		return e
	}); err != nil {
		return "", false, db.Translate(err, "")
	}

	if !claimed {
		// Somebody else generated this period between the read and here. The
		// expense just recorded is the duplicate, and it is reported rather
		// than hidden: it exists in the books and somebody has to see it.
		return "", false, errs.Newf(errs.CodeConflict,
			"%s had already been generated for %s by another run, and the "+
				"expense recorded here (%s) is a duplicate that needs "+
				"reversing.", name, dueOn.Format("2 January 2006"),
			made.ExpenseNo)
	}

	stillDue := !next.After(upTo) && (endsOn == nil || !next.After(*endsOn))
	return made.ExpenseNo, stillDue, nil
}

// advance moves a due date on by one interval.
//
// Month arithmetic is clamped rather than allowed to roll over: Go's AddDate
// turns 31 January plus a month into 3 March, which would quietly move a
// schedule's day-of-month every short month until it drifted to the 3rd.
func advance(from time.Time, frequency string, interval, anchorDay int) time.Time {
	if interval <= 0 {
		interval = 1
	}
	if anchorDay <= 0 {
		anchorDay = from.Day()
	}
	switch frequency {
	case "weekly":
		return from.AddDate(0, 0, 7*interval)
	case "yearly":
		return onDay(from.Year()+interval, from.Month(), anchorDay)
	default:
		y, m := from.Year(), int(from.Month())+interval
		y += (m - 1) / 12
		m = (m-1)%12 + 1
		return onDay(y, time.Month(m), anchorDay)
	}
}

// onDay puts a date on the anchor day of a month, or on its last day when the
// month is too short.
//
// The anchor is the day the SCHEDULE starts on, never the day the last one
// landed on. Advancing from the landed date is the drift bug this exists to
// avoid: a schedule anchored on the 31st is clamped to the 28th in February,
// and advancing from the 28th would put March on the 28th and leave it there
// for good.
func onDay(year int, month time.Month, day int) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
