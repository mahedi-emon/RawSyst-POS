// Telling a shop it is about to run out.
//
// B4 asks for a "Minimum Stock Alert Engine: dashboard + notification when any
// product/variant crosses its reorder threshold". The dashboard half has been
// there since `reports.Overview`, which counts low variants. The notification
// half did not exist: `reorder_level` was stored, read once for that count and
// once by the forecaster, and nobody was ever told. A threshold nobody is told
// about is a field, not an alert.
//
// # The same definition the dashboard uses
//
// Quantities are summed ACROSS warehouses before being compared to the reorder
// level, exactly as `reports.inventory` does — comparing per warehouse would
// report a shop as low while a full box sat in the back room. `qty > 0` is
// carried across too: a variant at zero is out of stock, which is a different
// fact needing a different message, and this alert is the warning BEFORE that.
//
// Two places disagreeing about what "low" means would be worse than either
// answer, so the query is deliberately the dashboard's.
package jobs

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/notify"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// KindLowStockSweep is the scheduled scan for variants at their reorder level.
const KindLowStockSweep = "stock.low_sweep"

// LowStockSweeper announces every variant that has crossed its threshold.
//
// In tenant context, like the tie-out sweep and for the same reason: row-level
// security is what stops one tenant's sweep reading another's stock, and a job
// that ran as the platform would give that up for the convenience of one query.
type LowStockSweeper struct {
	pool   *db.Pool
	notify *notify.Service
}

// NewLowStockSweeper builds the handler.
func NewLowStockSweeper(pool *db.Pool, n *notify.Service) *LowStockSweeper {
	return &LowStockSweeper{pool: pool, notify: n}
}

type lowVariant struct {
	id      uuid.UUID
	sku     string
	name    string
	qty     string
	reorder string
}

// Run sweeps every company in the tenant.
func (s *LowStockSweeper) Run(ctx context.Context, j Job) error {
	if j.TenantID == nil {
		return Permanent{errs.New(errs.CodeInternal,
			"This low-stock sweep names no tenant.")}
	}
	tenantID := *j.TenantID

	// Read and announce are separated because Announce opens its own
	// transaction: holding the sweep's connection while it did so is the pool
	// deadlock this codebase has already met once.
	var found map[uuid.UUID][]lowVariant
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		companies, err := companiesIn(ctx, tx)
		if err != nil {
			return err
		}
		found = make(map[uuid.UUID][]lowVariant, len(companies))
		for _, companyID := range companies {
			low, err := s.lowIn(ctx, tx, companyID)
			if err != nil {
				return err
			}
			if len(low) > 0 {
				found[companyID] = low
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	for companyID, low := range found {
		for _, v := range low {
			// Announced one at a time rather than as a digest: each carries its
			// own subject id so tapping the notification reaches THAT product,
			// where a digest would point at a list somebody then has to search.
			id := v.id
			if e := s.notify.Announce(ctx, notify.Scope{
				TenantID: tenantID, CompanyID: companyID,
			}, notify.Fact{
				Kind:     notify.KindLowStock,
				Severity: "warning",
				Title:    v.name + " is running low",
				Body: fmt.Sprintf("%s on hand against a reorder level of %s (%s).",
					v.qty, v.reorder, v.sku),
				Subject:   "variant",
				SubjectID: &id,
			}); e != nil {
				return e
			}
		}
	}
	return nil
}

// lowIn is the dashboard's low-stock question, asked of one company.
func (s *LowStockSweeper) lowIn(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) ([]lowVariant, error) {
	rows, err := tx.Query(ctx, `
		WITH on_hand AS (
		  SELECT v.id, v.sku, v.product_id, v.reorder_level,
		         coalesce(sum(stock_on_hand(v.id, w.id)), 0) AS qty
		  FROM variant v
		  LEFT JOIN warehouse w ON w.company_id = v.company_id
		  WHERE v.company_id = $1 AND v.is_active
		  GROUP BY v.id, v.sku, v.product_id, v.reorder_level
		)
		SELECT o.id, o.sku, coalesce(p.name, o.sku),
		       o.qty::text, o.reorder_level::text
		FROM on_hand o
		LEFT JOIN product p ON p.id = o.product_id
		WHERE o.reorder_level IS NOT NULL
		  AND o.qty > 0
		  AND o.qty <= o.reorder_level
		ORDER BY o.sku`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []lowVariant
	for rows.Next() {
		var v lowVariant
		if err := rows.Scan(&v.id, &v.sku, &v.name, &v.qty, &v.reorder); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
