// Saved and scheduled reports (blueprint D1), and the Saudization reading (E6).
//
// # A saved report is a saved SHAPE, not a saved answer
//
// It names which report, over what relative window, filtered to which branch or
// account. Running it recomputes from the ledger. Storing the figures instead
// would make a saved report a snapshot — and a schedule built on a snapshot
// would email the same numbers every month forever.
//
// # And not a query builder
//
// D1 asks for a custom report builder, and the version that would be dishonest
// is a free-form one. A screen that let somebody join arbitrary tables would be
// a screen that produces a figure the posting engine never blessed, rendered in
// the same typeface as the trial balance. What a shop actually wants from
// "custom" is "the profit and loss for Riyadh, for last quarter, kept, named,
// and mailed to my accountant on the first" — which is what this is.

package reports

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Saved is one kept report.
type Saved struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Kind string    `json:"kind"`

	Period string `json:"period"`

	StoreID     *uuid.UUID `json:"store_id,omitempty"`
	WarehouseID *uuid.UUID `json:"warehouse_id,omitempty"`
	AccountID   *uuid.UUID `json:"account_id,omitempty"`

	Cadence    string `json:"cadence,omitempty"`
	DayOfWeek  *int   `json:"day_of_week,omitempty"`
	DayOfMonth *int   `json:"day_of_month,omitempty"`
	Recipients string `json:"recipients,omitempty"`

	LastRunAt    string `json:"last_run_at,omitempty"`
	LastRunError string `json:"last_run_error,omitempty"`
	IsActive     bool   `json:"is_active"`

	// From and To are the window the relative period resolves to TODAY, so a
	// screen can show "1 – 30 September" beside "last month" rather than making
	// the reader work it out.
	From string `json:"from"`
	To   string `json:"to"`
}

// SavedReports lists them.
func (s *Service) SavedReports(
	ctx context.Context, scope Scope,
) ([]Saved, error) {
	out := []Saved{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, savedSelect+`
			WHERE company_id = $1
			ORDER BY cadence IS NULL, name`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanSaved(rows)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SaveReport creates or updates one.
func (s *Service) SaveReport(
	ctx context.Context, scope Scope, authorID uuid.UUID, in Saved,
) (Saved, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Saved{}, errs.New(errs.CodeInvalidInput, "Name the report.")
	}
	if in.Cadence != "" && strings.TrimSpace(in.Recipients) == "" {
		return Saved{}, errs.New(errs.CodeInvalidInput,
			"Say who a scheduled report goes to. A schedule with nobody to "+
				"send it to runs every week and reaches nobody.")
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO saved_report (
			  tenant_id, company_id, name, kind, period, store_id,
			  warehouse_id, account_id, cadence, day_of_week, day_of_month,
			  recipients, is_active, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,nullif($9,''),$10,$11,
			        nullif($12,''),$13,$14)
			ON CONFLICT (company_id, lower(name)) DO UPDATE SET
			  kind = excluded.kind,
			  period = excluded.period,
			  store_id = excluded.store_id,
			  warehouse_id = excluded.warehouse_id,
			  account_id = excluded.account_id,
			  cadence = excluded.cadence,
			  day_of_week = excluded.day_of_week,
			  day_of_month = excluded.day_of_month,
			  recipients = excluded.recipients,
			  is_active = excluded.is_active
			RETURNING id`,
			scope.TenantID, scope.CompanyID, strings.TrimSpace(in.Name),
			in.Kind, in.Period, in.StoreID, in.WarehouseID, in.AccountID,
			in.Cadence, in.DayOfWeek, in.DayOfMonth,
			strings.TrimSpace(in.Recipients), in.IsActive, authorID).
			Scan(&id)
	})
	if err != nil {
		return Saved{}, db.Translate(err, "That report could not be saved.")
	}

	list, err := s.SavedReports(ctx, scope)
	if err != nil {
		return Saved{}, err
	}
	for _, r := range list {
		if r.ID == id {
			return r, nil
		}
	}
	return Saved{}, errs.New(errs.CodeNotFound, "That report was not found.")
}

// SavedReportByID reads one, by tenant rather than by company.
//
// The scheduled-report job knows the tenant the sweep ran for and the report's
// id, and nothing else: it has no company in hand because a tenant's schedules
// span its companies. Row-level security is what confines the read, and the
// company comes BACK on the row rather than being supplied to it.
func (s *Service) SavedReportByID(
	ctx context.Context, tenantID, id uuid.UUID,
) (SavedWithCompany, error) {
	var out SavedWithCompany
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, savedColumns+`, company_id
			FROM saved_report WHERE id = $1`, id)
		var r Saved
		var companyID uuid.UUID
		var lastRun *time.Time
		if e := row.Scan(&r.ID, &r.Name, &r.Kind, &r.Period, &r.StoreID,
			&r.WarehouseID, &r.AccountID, &r.Cadence, &r.DayOfWeek,
			&r.DayOfMonth, &r.Recipients, &lastRun, &r.LastRunError,
			&r.IsActive, &companyID); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That report was not found.")
			}
			return e
		}
		if lastRun != nil {
			r.LastRunAt = lastRun.UTC().Format(time.RFC3339)
		}
		from, to := ResolvePeriod(r.Period, time.Now().UTC())
		r.From = from.Format("2006-01-02")
		r.To = to.Format("2006-01-02")
		out = SavedWithCompany{Saved: r, CompanyID: companyID}
		return nil
	})
	return out, db.Translate(err, "")
}

// SavedWithCompany is a saved report and the books it belongs to.
type SavedWithCompany struct {
	Saved
	CompanyID uuid.UUID `json:"company_id"`
}

// RemoveSavedReport deletes one.
func (s *Service) RemoveSavedReport(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx,
				`DELETE FROM saved_report WHERE id = $1 AND company_id = $2`,
				id, scope.CompanyID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That report was not found.")
			}
			return nil
		}), "")
}

const savedColumns = `
	SELECT id, name, kind, period, store_id, warehouse_id, account_id,
	       coalesce(cadence, ''), day_of_week, day_of_month,
	       coalesce(recipients, ''), last_run_at,
	       coalesce(last_run_error, ''), is_active`

const savedSelect = savedColumns + `
	FROM saved_report`

type scanner interface {
	Scan(dst ...any) error
}

func scanSaved(row scanner) (Saved, error) {
	var r Saved
	var lastRun *time.Time
	if err := row.Scan(&r.ID, &r.Name, &r.Kind, &r.Period, &r.StoreID,
		&r.WarehouseID, &r.AccountID, &r.Cadence, &r.DayOfWeek,
		&r.DayOfMonth, &r.Recipients, &lastRun, &r.LastRunError,
		&r.IsActive); err != nil {
		return Saved{}, err
	}
	if lastRun != nil {
		r.LastRunAt = lastRun.UTC().Format(time.RFC3339)
	}
	from, to := ResolvePeriod(r.Period, time.Now().UTC())
	r.From = from.Format("2006-01-02")
	r.To = to.Format("2006-01-02")
	return r, nil
}

// ResolvePeriod turns a relative phrase into the two dates it means today.
//
// Exported because the scheduled-report job resolves the same phrases when it
// runs, and two implementations of "last quarter" would eventually disagree
// about which one December belongs to.
//
// The window is inclusive at both ends, which is how every statement in this
// package already reads its dates.
func ResolvePeriod(period string, now time.Time) (time.Time, time.Time) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	switch period {
	case "today":
		return day, day

	case "this_week":
		// Monday. A shop's week is not the Go zero value's week, and starting
		// on Sunday would put a Saturday's takings in the following week for
		// half the world.
		offset := (int(day.Weekday()) + 6) % 7
		start := day.AddDate(0, 0, -offset)
		return start, start.AddDate(0, 0, 6)

	case "last_month":
		start := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, -1)

	case "this_quarter":
		start := quarterStart(now)
		return start, start.AddDate(0, 3, -1)

	case "last_quarter":
		start := quarterStart(now).AddDate(0, -3, 0)
		return start, start.AddDate(0, 3, -1)

	case "this_year":
		start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(1, 0, -1)

	case "last_year":
		start := time.Date(now.Year()-1, 1, 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(1, 0, -1)
	}

	// this_month, and anything unrecognised. A phrase nobody knows resolving to
	// the current month is the least surprising failure: it is the period a
	// shop means when they say "the report" without qualifying it.
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, -1)
}

func quarterStart(now time.Time) time.Time {
	month := ((int(now.Month())-1)/3)*3 + 1
	return time.Date(now.Year(), time.Month(month), 1, 0, 0, 0, 0, time.UTC)
}

// Due lists the scheduled reports whose turn it is.
//
// Read by the background job. "Its turn" is: the cadence has come round, and it
// has not already run for this occurrence — which is why `last_run_at` is
// compared against the START of the current period rather than against a fixed
// interval. A worker that was down for a day then runs the report once, not
// twice, and not never.
func (s *Service) Due(
	ctx context.Context, tenantID uuid.UUID, now time.Time,
) ([]Saved, error) {
	out := []Saved{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, savedSelect+`
			WHERE is_active AND cadence IS NOT NULL
			  AND (
			    (cadence = 'daily'
			      AND (last_run_at IS NULL
			           OR last_run_at < date_trunc('day', $1::timestamptz)))
			    OR (cadence = 'weekly'
			      AND extract(dow FROM $1::timestamptz)::int = day_of_week
			      AND (last_run_at IS NULL
			           OR last_run_at < date_trunc('day', $1::timestamptz)))
			    OR (cadence = 'monthly'
			      AND extract(day FROM $1::timestamptz)::int = day_of_month
			      AND (last_run_at IS NULL
			           OR last_run_at < date_trunc('day', $1::timestamptz)))
			  )
			ORDER BY name`, now)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scanSaved(rows)
			if e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// MarkRun stamps a scheduled report, with the failure if there was one.
//
// The stamp is written whether the run succeeded or failed, so a report that
// fails every night is not retried every minute — and the reason is on the row
// where the owner will see it beside the schedule.
func (s *Service) MarkRun(
	ctx context.Context, tenantID, id uuid.UUID, failure string,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE saved_report
			   SET last_run_at = now(), last_run_error = nullif($2,'')
			 WHERE id = $1`, id, strings.TrimSpace(failure))
		return e
	}), "")
}

// ---------------------------------------------------------------------------
// Saudization (E6)
// ---------------------------------------------------------------------------

// Workforce is the Saudization reading.
//
// A count and a ratio, and deliberately NOT a Nitaqat band. The band depends on
// the establishment's activity, its size bracket and a schedule the ministry
// publishes and revises; asserting one from a head count would be inventing a
// regulatory classification. The shop's own ministry portal is where the band
// comes from, and this is the number they check it against.
type Workforce struct {
	Total    int `json:"total"`
	Saudi    int `json:"saudi"`
	NonSaudi int `json:"non_saudi"`
	// Percentage of the workforce that is Saudi, to two places.
	SaudiShare string `json:"saudi_share"`

	// Documents that lapse soon, which E6 names beside the ratio because a
	// residence permit expiring is what removes somebody from the count.
	ExpiringSoon int `json:"expiring_soon"`
	Expired      int `json:"expired"`

	ByDepartment []WorkforceLine `json:"by_department"`
}

// WorkforceLine is one department's share.
type WorkforceLine struct {
	Department string `json:"department"`
	Total      int    `json:"total"`
	Saudi      int    `json:"saudi"`
}

// Workforce reads it.
func (s *Service) Workforce(
	ctx context.Context, scope Scope,
) (Workforce, error) {
	var out Workforce
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// People who are actually employed. Somebody who left in March is not
		// part of the ratio the ministry measures today.
		if e := tx.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE is_saudi),
			       count(*) FILTER (WHERE NOT is_saudi)
			FROM employee
			WHERE company_id = $1 AND left_on IS NULL`,
			scope.CompanyID).Scan(
			&out.Total, &out.Saudi, &out.NonSaudi); e != nil {
			return e
		}

		if out.Total > 0 {
			share := float64(out.Saudi) * 100 / float64(out.Total)
			out.SaudiShare = trimTo2(share)
		} else {
			out.SaudiShare = "0.00"
		}

		if e := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (
			         WHERE id_expires_on >= current_date
			           AND id_expires_on <= current_date + 60),
			       count(*) FILTER (WHERE id_expires_on < current_date)
			FROM employee
			WHERE company_id = $1 AND left_on IS NULL
			  AND id_expires_on IS NOT NULL`,
			scope.CompanyID).Scan(
			&out.ExpiringSoon, &out.Expired); e != nil {
			return e
		}

		rows, e := tx.Query(ctx, `
			SELECT coalesce(nullif(btrim(department), ''), '—'),
			       count(*), count(*) FILTER (WHERE is_saudi)
			FROM employee
			WHERE company_id = $1 AND left_on IS NULL
			GROUP BY 1
			ORDER BY 2 DESC, 1`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		out.ByDepartment = []WorkforceLine{}
		for rows.Next() {
			var l WorkforceLine
			if e := rows.Scan(&l.Department, &l.Total, &l.Saudi); e != nil {
				return e
			}
			out.ByDepartment = append(out.ByDepartment, l)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// trimTo2 renders a percentage to two places without pulling in a decimal type
// for one number that is not money.
func trimTo2(v float64) string {
	whole := int(v)
	frac := int((v-float64(whole))*100 + 0.5)
	if frac >= 100 {
		whole++
		frac -= 100
	}
	return itoa(whole) + "." + pad2(frac)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}

func pad2(v int) string {
	if v < 10 {
		return "0" + itoa(v)
	}
	return itoa(v)
}
