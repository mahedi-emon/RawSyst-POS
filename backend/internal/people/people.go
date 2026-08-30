// Package people is the employee directory, attendance, leave, advances,
// payroll, commission, GOSI, end-of-service and the WPS wage file
// (blueprint C5, C6, E6).
//
// # No legal value is in this code
//
// C6 says the GOSI rates "have been rising under the new Social Insurance Law
// effective from July 2024 and are scheduled to keep increasing through 2028",
// and that the engine must use "a configurable, updatable rate table rather
// than a hard-coded percentage". E8 generalises that to every rate, threshold,
// deadline and file format.
//
// So there is no rate in this file, and no wage-file layout. Both resolve from
// the regulatory registry AT THE PAY PERIOD'S DATE, which is what makes
// re-running an old month produce the figure that was correct then rather than
// the figure that is correct now.
//
// `SA.GOSI.RATES` and `SA.WPS.WAGE_FILE_FORMAT` are release blockers still
// carrying `__VERIFY__`. Until somebody verifies them against the Tier 1 source
// and stamps `verified_on`, a GOSI calculation and a wage file REFUSE, naming
// the rule key. That refusal is the feature, not a gap: a payroll run that
// guessed would be wrong in a way nobody notices until Mudad rejects the file
// or GOSI reassesses the year.
//
// Everything that is not a legal value runs: the directory, attendance, leave,
// advances, commission, gross pay, overtime, absence, net pay, the journal
// entries and the payslips. A shop outside Saudi Arabia, or one whose rates a
// person has verified, gets the whole module.
//
// # Who may see a wage
//
// A6.2 requires that staff can be blocked from seeing "other employees'
// salaries". Seeing the DIRECTORY and seeing the PAY are therefore separate
// permissions, and the split is enforced by omission — a caller without
// `hr.view_pay` gets an Employee with the pay fields empty, rather than a
// filtered screen over a full payload.
package people

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// Service owns the people modules.
type Service struct {
	pool  *db.Pool
	rules *registry.Service
}

// NewService builds the service.
//
// The registry is required rather than optional: every payroll figure that
// touches Saudi law resolves through it, and a service built without one could
// only work by inventing rates.
func NewService(pool *db.Pool, rules *registry.Service) *Service {
	return &Service{pool: pool, rules: rules}
}

// Scope is which books a person belongs to.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID

	// MaySeePay carries A6.2's data masking into the service, so the decision
	// is made once at the boundary rather than in each query. False means the
	// pay fields come back empty.
	MaySeePay bool
}

// claimNo takes the next number for a document kind.
func claimNo(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, kind, prefix string,
) (string, error) {
	var n int64
	if err := tx.QueryRow(ctx,
		`SELECT claim_document_no($1, $2)`, companyID, kind).Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%05d", prefix, n), nil
}

func nullText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// scanner is satisfied by both pgx.Row and pgx.Rows.
type scanner interface{ Scan(dest ...any) error }
