// Putting a payroll run right when it was wrong.
//
// 0091 modelled a `cancelled` run and built the month's uniqueness around it —
// `payroll_run_period_uq` is partial, `WHERE status <> 'cancelled'`, precisely
// so that a cancelled run releases its month for a corrected one. No code ever
// set that status. A month approved on the wrong attendance, the wrong advance
// recovery or the wrong GOSI band was final: the entries stayed in the ledger,
// and the index still counted the bad run, so the month could not be run again.
//
// # Reversed, not deleted
//
// A posted month is a fact. Correcting it is a second fact. So the entries are
// flipped and posted back rather than removed, and both stand in the journal
// with the reversal pointing at what it undoes — the same shape purchasing
// uses for a supplier payment.
//
// The lines come from the ENTRY, never re-derived from the posting rule. The
// rule may have been amended since, and re-deriving would post a reversal that
// does not match what it claims to undo.
package people

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// runEntries are the entries a run may have put in the ledger, in the order
// they were posted. A cancellation unwinds them in reverse.
//
// Each reversal takes its own source_type so it gets its own row: the journal's
// idempotency key is (source_type, source_id, rule_key), and reusing the
// original's triple would find the original entry and post nothing at all.
var runEntries = []struct{ sourceType, ruleKey, memo string }{
	{"payroll_payment", "payroll.pay", "Reversal of wages paid"},
	{"payroll_run_employer", "payroll.employer_gosi",
		"Reversal of employer social insurance"},
	{"payroll_run", "payroll.accrue", "Reversal of payroll"},
}

// Cancel abandons a run and unwinds everything it did.
//
// Allowed from any state but `cancelled`. A draft has posted nothing and only
// its advance recoveries need releasing; an approved run has two entries and a
// paid one has three, and each is reversed if it is there.
func (s *Service) Cancel(
	ctx context.Context, scope Scope, runID uuid.UUID, reason string,
) (Run, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Run{}, errs.Validation("Say why this run is being cancelled.").
			WithField("reason",
				"The ledger will carry a reversing entry, and this is the "+
					"only place that says what it was for.")
	}

	var out Run
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var status, country string
		e := tx.QueryRow(ctx, `
			SELECT r.status, c.country
			FROM payroll_run r
			JOIN company c ON c.id = r.company_id
			WHERE r.id = $1 AND r.company_id = $2 FOR UPDATE OF r`,
			runID, scope.CompanyID).Scan(&status, &country)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That payroll run was not found.")
		}
		if e != nil {
			return e
		}
		if status == "cancelled" {
			return errs.New(errs.CodeConflict,
				"That run has already been cancelled.")
		}

		// A wage file that has gone to the bank cannot be taken back by this
		// product. Cancelling the run behind it would leave the ledger saying
		// the month never happened while the transfer was already instructed.
		var sent string
		e = tx.QueryRow(ctx, `
			SELECT status FROM wps_file
			WHERE run_id = $1 AND status IN ('submitted', 'accepted')
			LIMIT 1`, runID).Scan(&sent)
		if e == nil {
			return errs.Newf(errs.CodeConflict,
				"This run's wage file has been %s, so the payment has "+
					"already been instructed. Cancelling here would not "+
					"recall it.", sent)
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		// Dated today, not the month worked: the correction happens now, and
		// the period the run belongs to may well be closed.
		on := time.Now().UTC()
		for _, p := range runEntries {
			if e := s.reverseRunEntry(ctx, tx, scope, runID, p.sourceType,
				p.ruleKey, country, p.memo, on); e != nil {
				return e
			}
		}

		// The advances go back to being owed. `advance_outstanding()` sums
		// these rows, so leaving them would show an employee's loan as partly
		// repaid out of a month that was never paid.
		if _, e := tx.Exec(ctx, `
			DELETE FROM advance_recovery
			WHERE payslip_id IN (SELECT id FROM payslip WHERE run_id = $1)`,
			runID); e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			UPDATE payroll_run
			SET status = 'cancelled', cancelled_at = now(), cancelled_by = $2,
			    cancel_reason = $3
			WHERE id = $1`, runID, scope.UserID, reason); e != nil {
			return e
		}

		read, e := s.readRun(ctx, tx, scope.CompanyID, runID)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// reverseRunEntry flips one of a run's entries, if the run ever posted it.
func (s *Service) reverseRunEntry(
	ctx context.Context, tx pgx.Tx, scope Scope, runID uuid.UUID,
	sourceType, ruleKey, country, memo string, on time.Time,
) error {
	var entryID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM journal_entry
		WHERE source_type = $1 AND source_id = $2
		  AND coalesce(rule_key, '') = $3`,
		sourceType, runID, ruleKey).Scan(&entryID)
	if errors.Is(err, pgx.ErrNoRows) {
		// A draft never accrued, and a run approved but not paid never made a
		// payment entry. Nothing to undo is not an error.
		return nil
	}
	if err != nil {
		return err
	}

	lines, err := accounting.LinesOf(ctx, tx, entryID)
	if err != nil {
		return err
	}
	rule, err := accounting.ResolveRule(ctx, tx, ruleKey, country, on)
	if err != nil {
		return err
	}

	_, err = accounting.Post(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date:        on,
		SourceType:  sourceType + "_reversal",
		SourceID:    runID,
		RuleKey:     ruleKey,
		RuleVersion: rule.Version,
		Memo:        memo,
		PostedBy:    &scope.UserID,
		ReversesID:  &entryID,
		Lines:       accounting.FlipSides(lines),
	})
	return err
}
