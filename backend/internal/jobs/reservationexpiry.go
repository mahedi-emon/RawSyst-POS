// Letting go of stock that an abandoned order was holding (blueprint B13).
//
// B13 reserves against UNPAID online orders, and `stock_reservation.expires_at`
// exists so that an abandoned basket does not hold the last unit for ever.
// `aftersales.ExpireHolds` was written to act on it and nothing ever called it:
// no route, no job, no schedule. The deadline was recorded and never arrived.
//
// The consequence is the one the column was added to prevent. A customer fills
// a basket, never pays, and the shop's last unit is unsellable from then on —
// through every channel, indefinitely, with nothing anywhere saying why the
// shelf shows one and the till refuses to sell it.
package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/aftersales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// KindReservationExpirySweep is the scheduled release of lapsed holds.
const KindReservationExpirySweep = "stock.reservation_expiry_sweep"

// ReservationExpirySweeper releases holds whose deadline has passed.
//
// In tenant context, like the low-stock and batch-expiry sweeps and for the
// same reason: row-level security is what stops one tenant's sweep touching
// another's reservations, and a job running as the platform would give that up.
type ReservationExpirySweeper struct {
	pool  *db.Pool
	after *aftersales.Service
}

// NewReservationExpirySweeper builds the handler.
func NewReservationExpirySweeper(
	pool *db.Pool, a *aftersales.Service,
) *ReservationExpirySweeper {
	return &ReservationExpirySweeper{pool: pool, after: a}
}

// Run releases lapsed holds for every company in the tenant.
//
// Per company rather than in one statement, because `ExpireHolds` is scoped to
// a company and releasing a hold is a business act with an audit trail behind
// it — not a bulk UPDATE this package should be writing its own version of.
func (s *ReservationExpirySweeper) Run(ctx context.Context, j Job) error {
	if j.TenantID == nil {
		return Permanent{errs.New(errs.CodeInternal,
			"This reservation-expiry sweep names no tenant.")}
	}
	tenantID := *j.TenantID

	var companies []uuid.UUID
	if err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id FROM company`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				return e
			}
			companies = append(companies, id)
		}
		return rows.Err()
	}); err != nil {
		return db.Translate(err, "")
	}

	for _, companyID := range companies {
		if _, err := s.after.ExpireHolds(ctx, aftersales.Scope{
			TenantID: tenantID, CompanyID: companyID,
		}); err != nil {
			return err
		}
	}
	return nil
}
