// Package fiscal owns the calendar the books are kept against (blueprint C10).
//
// # The blocker this package opens with
//
// `accounting.resolvePeriod` refuses every posting for a date no period covers,
// and told the reader to "ask an owner to open the period for that date". There
// was no way for an owner to do that: no route, no service, no screen, and the
// only INSERTs into `fiscal_period` in the repository were in test fixtures.
//
// The consequence was total. A journal entry needs a period, and everything
// this product does financially is a journal entry, so a tenant provisioned the
// real way could not ring up a sale, record an expense, receive a delivery,
// take a payment or close a till. Migration 0080 generates the calendar; this
// is the module that manages it afterwards.
//
// # Three states, and what each one means
//
//	open   — transactions may be posted
//	closed — no transaction may be created, edited or deleted in this period
//	locked — closed, and the year-end routine has run
//
// C10 calls the second one "what makes financial statements trustworthy". The
// database enforces it in a trigger; this package is the only thing that moves
// a period between the states, and every move is written to the audit trail
// before it commits.
//
// # Reopening is deliberately awkward
//
// C10: reopening "requires an explicit Owner-level permission plus a mandatory
// reason, and is permanently audit-logged". All three are here — the permission
// on the route, the reason as a CHECK constraint since 0015, and the audit
// entry inside the same transaction as the reopen.
//
// The reason is not metadata. Reopening a closed period changes figures
// somebody has already reported to a bank, a tax authority or a partner, and
// the person who does it should have to write down why in a place they cannot
// later edit.
package fiscal

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// MinReopenReason is the length 0015's CHECK requires. Stated here so a person
// is told before the round trip rather than by a constraint name after it.
const MinReopenReason = 10

// Service manages fiscal periods.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on behalf of which legal entity.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Period is one month of the books.
type Period struct {
	ID       uuid.UUID `json:"id"`
	Year     int       `json:"fiscal_year"`
	Number   int       `json:"period_no"`
	StartsOn string    `json:"starts_on"`
	EndsOn   string    `json:"ends_on"`
	State    string    `json:"state"`

	ClosedBy   string `json:"closed_by,omitempty"`
	ClosedAt   string `json:"closed_at,omitempty"`
	ReopenedBy string `json:"reopened_by,omitempty"`
	ReopenedAt string `json:"reopened_at,omitempty"`
	Reason     string `json:"reopen_reason,omitempty"`

	// Entries is how many journal entries this period holds. Shown because
	// closing an empty month and closing a month with four thousand entries in
	// it are different decisions, and a person about to press Close deserves to
	// know which one they are making.
	Entries int `json:"entries"`

	// Current marks the period today falls in.
	Current bool `json:"is_current,omitempty"`
}

// Year is one fiscal year and its twelve months.
type Year struct {
	Year    int      `json:"fiscal_year"`
	Periods []Period `json:"periods"`
}

// Calendar is every year the company has, newest first.
type Calendar struct {
	Years []Year `json:"years"`

	// StartMonth is the month the company's fiscal year begins in, 1–12. The
	// screen needs it to say "April 2026 to March 2027" rather than leaving a
	// reader to infer the shape of the year from the dates.
	StartMonth int `json:"fiscal_year_start_month"`
}

// Calendar reads the whole calendar.
func (s *Service) Calendar(ctx context.Context, scope Scope) (Calendar, error) {
	out := Calendar{Years: []Year{}}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT fiscal_year_start_month FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&out.StartMonth); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound,
					"That business is not one this account can act for.")
			}
			return e
		}

		rows, err := tx.Query(ctx, `
			SELECT p.id, p.fiscal_year, p.period_no,
			       to_char(p.starts_on, 'YYYY-MM-DD'),
			       to_char(p.ends_on, 'YYYY-MM-DD'),
			       p.state,
			       coalesce(cu.full_name, ''),
			       coalesce(to_char(p.closed_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
			       coalesce(ru.full_name, ''),
			       coalesce(to_char(p.reopened_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
			       coalesce(p.reopen_reason, ''),
			       (SELECT count(*) FROM journal_entry j WHERE j.period_id = p.id),
			       current_date BETWEEN p.starts_on AND p.ends_on
			FROM fiscal_period p
			LEFT JOIN app_user cu ON cu.id = p.closed_by
			LEFT JOIN app_user ru ON ru.id = p.reopened_by
			WHERE p.company_id = $1
			ORDER BY p.fiscal_year DESC, p.period_no`,
			scope.CompanyID)
		if err != nil {
			return err
		}
		defer rows.Close()

		byYear := map[int]int{} // fiscal year -> index into out.Years
		for rows.Next() {
			var p Period
			if err := rows.Scan(&p.ID, &p.Year, &p.Number, &p.StartsOn, &p.EndsOn,
				&p.State, &p.ClosedBy, &p.ClosedAt, &p.ReopenedBy, &p.ReopenedAt,
				&p.Reason, &p.Entries, &p.Current); err != nil {
				return err
			}
			i, seen := byYear[p.Year]
			if !seen {
				out.Years = append(out.Years, Year{Year: p.Year})
				i = len(out.Years) - 1
				byYear[p.Year] = i
			}
			out.Years[i].Periods = append(out.Years[i].Periods, p)
		}
		return rows.Err()
	})
	return out, err
}

// OpenYear creates the twelve periods of a fiscal year.
//
// Idempotent, and it says how many it actually made: opening a year that is
// already open is not an error — two people can press it on the same morning —
// but "0 new periods" and "12 new periods" are different answers and the screen
// should be able to tell the difference.
func (s *Service) OpenYear(
	ctx context.Context, scope Scope, year int,
) (int, error) {
	// A century either way. Not a business rule — it is the range in which a
	// typo is distinguishable from an intention, and 20226 is a typo.
	now := time.Now().UTC().Year()
	if year < now-100 || year > now+100 {
		return 0, errs.Newf(errs.CodeInvalidInput,
			"%d does not look like a year this business trades in.", year)
	}

	var made int
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `SELECT open_fiscal_year($1, $2)`,
			scope.CompanyID, year).Scan(&made); e != nil {
			return db.Translate(e, "That accounting year could not be opened.")
		}
		if made == 0 {
			return nil
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "fiscal_year_opened",
			EntityType: "company", EntityID: &scope.CompanyID,
			After: map[string]any{"fiscal_year": year, "periods_created": made},
		})
	})
	return made, err
}

// Close ends a period.
//
// Refused while an earlier period of the same year is still open. Closing
// August while July is open would produce a set of statements nobody can
// reconcile: the year-to-date figures would include a month that is still
// moving, and a person reading the August accounts has no way to know.
func (s *Service) Close(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Period, error) {
	var out Period
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		p, e := s.periodForWrite(ctx, tx, scope, id)
		if e != nil {
			return e
		}
		if p.State != "open" {
			return errs.Newf(errs.CodeConflict,
				"%s is already %s.", monthLabel(p), p.State)
		}

		var earlier string
		e = tx.QueryRow(ctx, `
			SELECT to_char(starts_on, 'FMMonth YYYY')
			FROM fiscal_period
			WHERE company_id = $1 AND state = 'open'
			  AND (fiscal_year, period_no) < ($2, $3)
			ORDER BY fiscal_year, period_no
			LIMIT 1`, scope.CompanyID, p.Year, p.Number).Scan(&earlier)
		if e == nil {
			return errs.Newf(errs.CodeConflict,
				"%s is still open, and periods close in order. Closing a later "+
					"month first would leave the year-to-date figures moving "+
					"underneath the statements for this one.",
				strings.TrimSpace(earlier))
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		if _, e := tx.Exec(ctx, `
			UPDATE fiscal_period
			SET state = 'closed', closed_at = now(), closed_by = $2
			WHERE id = $1`, id, scope.UserID); e != nil {
			return db.Translate(e, "That period could not be closed.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "period_closed",
			EntityType: "fiscal_period", EntityID: &id,
			Before: map[string]any{"state": "open"},
			After: map[string]any{
				"state": "closed", "period": monthLabel(p), "entries": p.Entries,
			},
		}); e != nil {
			return e
		}

		read, e := s.readPeriod(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// Reopen puts a closed period back, with a reason.
//
// C10's three requirements, all of them enforced somewhere different: the
// Owner-level permission on the route, the written reason here and in the CHECK
// constraint 0015 added, and the audit entry in this transaction.
//
// A LOCKED period is not reopened by this. Locked means the year-end routine
// has run and the revenue and expense accounts have been closed into retained
// earnings; putting a transaction back into such a period would leave the
// closing entries wrong and nothing would say so.
func (s *Service) Reopen(
	ctx context.Context, scope Scope, id uuid.UUID, reason string,
) (Period, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < MinReopenReason {
		return Period{}, errs.Validation(
			"Say why this period is being reopened.").
			WithField("reason",
				"Reopening changes figures somebody has already reported. "+
					"Write down what happened, in a sentence.")
	}

	var out Period
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		p, e := s.periodForWrite(ctx, tx, scope, id)
		if e != nil {
			return e
		}
		switch p.State {
		case "open":
			return errs.Newf(errs.CodeConflict, "%s is already open.", monthLabel(p))
		case "locked":
			return errs.Newf(errs.CodeConflict,
				"%s is locked: the year-end routine has run and its revenue and "+
					"expense accounts have been closed into retained earnings. "+
					"Post a correcting entry in an open period instead.",
				monthLabel(p))
		}

		if _, e := tx.Exec(ctx, `
			UPDATE fiscal_period
			SET state = 'open', reopened_at = now(), reopened_by = $2,
			    reopen_reason = $3
			WHERE id = $1`, id, scope.UserID, reason); e != nil {
			return db.Translate(e, "That period could not be reopened.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "period_reopened",
			EntityType: "fiscal_period", EntityID: &id,
			Before: map[string]any{"state": "closed", "closed_by": p.ClosedBy},
			After: map[string]any{
				"state": "open", "period": monthLabel(p), "reason": reason,
			},
		}); e != nil {
			return e
		}

		read, e := s.readPeriod(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// --- reading --------------------------------------------------------------

func (s *Service) periodForWrite(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Period, error) {
	var p Period
	err := tx.QueryRow(ctx, `
		SELECT p.fiscal_year, p.period_no,
		       to_char(p.starts_on, 'YYYY-MM-DD'),
		       to_char(p.ends_on, 'YYYY-MM-DD'),
		       p.state, coalesce(cu.full_name, ''),
		       (SELECT count(*) FROM journal_entry j WHERE j.period_id = p.id)
		FROM fiscal_period p
		LEFT JOIN app_user cu ON cu.id = p.closed_by
		WHERE p.id = $1 AND p.company_id = $2
		FOR UPDATE OF p`, id, scope.CompanyID).
		Scan(&p.Year, &p.Number, &p.StartsOn, &p.EndsOn, &p.State,
			&p.ClosedBy, &p.Entries)
	if errors.Is(err, pgx.ErrNoRows) {
		return Period{}, errs.New(errs.CodeNotFound,
			"That accounting period is not this business's.")
	}
	p.ID = id
	return p, err
}

func (s *Service) readPeriod(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Period, error) {
	var p Period
	err := tx.QueryRow(ctx, `
		SELECT p.id, p.fiscal_year, p.period_no,
		       to_char(p.starts_on, 'YYYY-MM-DD'),
		       to_char(p.ends_on, 'YYYY-MM-DD'),
		       p.state,
		       coalesce(cu.full_name, ''),
		       coalesce(to_char(p.closed_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
		       coalesce(ru.full_name, ''),
		       coalesce(to_char(p.reopened_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
		       coalesce(p.reopen_reason, ''),
		       (SELECT count(*) FROM journal_entry j WHERE j.period_id = p.id),
		       current_date BETWEEN p.starts_on AND p.ends_on
		FROM fiscal_period p
		LEFT JOIN app_user cu ON cu.id = p.closed_by
		LEFT JOIN app_user ru ON ru.id = p.reopened_by
		WHERE p.id = $1 AND p.company_id = $2`, id, scope.CompanyID).
		Scan(&p.ID, &p.Year, &p.Number, &p.StartsOn, &p.EndsOn, &p.State,
			&p.ClosedBy, &p.ClosedAt, &p.ReopenedBy, &p.ReopenedAt, &p.Reason,
			&p.Entries, &p.Current)
	return p, err
}

// monthLabel names a period the way a person would say it.
//
// From the dates rather than from the period number, because "period 5" means
// May at one company and August at another, and the sentence it appears in is
// usually a refusal somebody has to act on.
func monthLabel(p Period) string {
	t, err := time.Parse("2006-01-02", p.StartsOn)
	if err != nil {
		return p.StartsOn
	}
	return t.Format("January 2006")
}
