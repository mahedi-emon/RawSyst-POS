// The nightly tie-out (design 08 §3 and §6, QA gate M1).
//
// # Why this exists
//
// Three invariants hold everywhere in this product and were, until now, checked
// only by tests:
//
//   - AR sub-ledger = AR control account          (blueprint C9.3)
//   - AP sub-ledger = AP control account          (blueprint C9.3)
//   - inventory valuation = Inventory GL balance  (blueprint C13)
//
// Every one of them is proved on every build. None of them was watched on a
// live tenant. `inventory.GLDifference`'s own comment says "the nightly job,
// the acceptance test and a support engineer looking at a live tenant all ask
// the same question and get the same answer" — and the nightly job did not
// exist, so a shop whose books drifted after go-live would have found out from
// its accountant months later rather than from us the next morning.
//
// C13 is explicit that this is not optional: "any divergence is flagged as an
// exception". Design 08 §6 puts it plainly — these jobs are how the QA gates
// become continuously-monitored properties rather than one-off checks.
//
// # What it does not do
//
// It does not correct anything. A divergence is a symptom whose cause is
// unknown at 4am, and a job that "fixed" a tie-out would destroy the evidence
// of whatever caused it while making the books agree with an error. It raises
// an alert and stops. Posted history stays immutable.
//
// It also invents no accounting rule. All three questions are asked through
// functions that already existed and are already proved by the suite.
package jobs

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
)

// KindAccountingTieOut reconciles each sub-ledger against its control account.
const KindAccountingTieOut = "accounting.tie_out"

// AlertTieOut is the alert kind a divergence raises.
//
// One kind for all three checks, with the detail naming which one moved. Three
// kinds would mean three alerts for a company whose books went wrong in a way
// that touched two of them, and the second alert tells an owner nothing the
// first did not.
const AlertTieOut = "accounting.tie_out"

// tieOutCheck is one invariant and the function that answers it.
//
// The answer comes from each module's own GLDifference rather than from SQL
// written here. Those three functions already existed to be the single way of
// asking their question — inventory.GLDifference's comment says so in as many
// words, naming "the nightly job" among the callers it was written for — and a
// job that re-derived the same figures would be a second definition of each
// invariant, free to drift from the one the acceptance tests prove.
type tieOutCheck struct {
	// label is what an owner reads. Not the function name: "Supplier balances"
	// means something to the person who has to act on it.
	label string
	ask   func(context.Context, pgx.Tx, uuid.UUID) (decimal.Decimal, error)
}

// The three of QA gate M1, in the order design 08 lists them.
var tieOutChecks = []tieOutCheck{
	{"Customer balances", receivables.GLDifference},
	{"Supplier balances", purchasing.GLDifference},
	{"Stock valuation", inventory.GLDifference},
}

// TieOutSweeper checks one company's sub-ledgers against its control accounts.
type TieOutSweeper struct{ pool *db.Pool }

// NewTieOutSweeper builds the handler.
func NewTieOutSweeper(pool *db.Pool) *TieOutSweeper { return &TieOutSweeper{pool: pool} }

// Run reconciles every company in the tenant.
//
// In tenant context, like the staleness sweep and for the same reason:
// row-level security is what stops one tenant's sweep reading another's
// ledgers, and a job that ran as the platform would have to give that up for
// the convenience of one query.
func (s *TieOutSweeper) Run(ctx context.Context, j Job) error {
	if j.TenantID == nil {
		return Permanent{errs.New(errs.CodeInternal,
			"This tie-out names no tenant.")}
	}
	tenantID := *j.TenantID

	return s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		companies, err := companiesIn(ctx, tx)
		if err != nil {
			return err
		}
		for _, companyID := range companies {
			if err := s.reconcile(ctx, tx, tenantID, companyID); err != nil {
				return err
			}
		}
		return nil
	})
}

// reconcile asks all three questions about one company and acts on the answers.
func (s *TieOutSweeper) reconcile(
	ctx context.Context, tx pgx.Tx, tenantID, companyID uuid.UUID,
) error {
	// A company with no chart has never posted anything, so every difference is
	// trivially zero and an alert about it would be noise. Skipping is not the
	// same as passing: there is nothing yet to reconcile.
	var posted bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM account WHERE company_id = $1)`,
		companyID).Scan(&posted); err != nil {
		return err
	}
	if !posted {
		return nil
	}

	var diverged []string
	for _, c := range tieOutChecks {
		difference, err := c.ask(ctx, tx, companyID)
		if err != nil {
			return err
		}
		if !difference.IsZero() {
			diverged = append(diverged,
				fmt.Sprintf("%s out by %s", c.label, difference.StringFixed(2)))
		}
	}

	if len(diverged) == 0 {
		// Tied. Clear any alert still standing from a previous night: an alert
		// that outlives the problem teaches people to ignore the next one.
		_, err := tx.Exec(ctx, `
			UPDATE compliance_alert SET cleared_at = now()
			WHERE company_id = $1 AND kind = $2 AND cleared_at IS NULL`,
			companyID, AlertTieOut)
		return err
	}

	// Critical, not warning. A sub-ledger that disagrees with its control
	// account means the figure on a VAT return or a balance sheet is wrong, and
	// blueprint C13 asks for an exception rather than a note in a log.
	return raiseAlert(ctx, tx, tenantID, companyID, AlertTieOut, "critical",
		"The books do not tie out: "+joinAnd(diverged)+
			". Nothing has been changed — this needs looking at before the "+
			"period is closed.")
}

// companiesIn lists the tenant's companies inside the caller's transaction.
func companiesIn(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM company`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// joinAnd renders a list the way a person would say it.
//
// The alert is read by a shop owner, not parsed by a machine. "Customer
// balances out by 5.00 and Stock valuation out by 0.01" is a sentence; a
// comma-separated list with a trailing comma is not.
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		out := ""
		for i, p := range parts[:len(parts)-1] {
			if i > 0 {
				out += ", "
			}
			out += p
		}
		return out + " and " + parts[len(parts)-1]
	}
}
