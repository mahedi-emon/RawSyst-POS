// Package aftersales is what happens once the goods have left the counter:
// delivery (B13), serial/IMEI tracking and warranty (B15), service and repair
// work orders (B15), and instalment plans (B14).
//
// # Why these live together
//
// They share one subject: a physical unit with an identity, after it has been
// sold. A delivery carries it, a serial number names it, a warranty covers it,
// a repair fixes it, and an instalment plan is how it was paid for. Splitting
// them into four packages would put the serial lifecycle — the thing all four
// read — in one of them and make the other three import it.
//
// # What posts and what does not
//
// A reservation posts nothing: reserved goods are still owned, still on the
// shelf and still in the Inventory account, so a journal entry would take them
// out of a valuation they have not left.
//
// A delivery posts nothing by itself; the sale it belongs to already did.
//
// A warranty part posts, because stock genuinely leaves. A paid repair's part
// leaves through the ordinary sale that charges for it.
//
// An instalment plan posts only the finance charge, and only as it is earned.
// The sale itself already posted revenue, VAT and COGS at the till — booking
// it again here would count one sale twice, and booking the whole markup on
// day one would report profit the shop has not yet earned.
package aftersales

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// Service owns delivery, serials, service jobs and instalment plans.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is which books an after-sales record belongs to.
//
// Company rather than device, like purchasing: a service desk and a dispatch
// office work at a browser, not at a till, so there is no registered terminal
// to resolve the company from.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
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
	return fmt.Sprintf("%s-%06d", prefix, n), nil
}

func nullText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
