// Telling a shop before its stock goes out of date.
//
// B4 asks for "automatic Expiring Soon / Expired / Batch Recall alerts". The
// lot layer (0107) records the dates; without this nobody is told, and a date
// nobody is told about is a column rather than an alert.
//
// # Two facts, two messages
//
// Expiring soon is a warning: there is still time to sell it, discount it or
// send it back. Expired is a loss already taken, and the action is to get it
// off the shelf. They are different enough to need different words and
// different severities, and collapsing them into one "check your dates" would
// leave a shop unable to tell which lots it can still act on.
//
// # The horizon is the shop's own reorder judgement, not a legal rule
//
// Thirty days. This is an operational default, not a regulated one, and it is
// stated here rather than in the regulatory registry for that reason: the
// registry holds dated LEGAL values that carry evidence, and "warn me a month
// out" is neither dated nor legal. A shop that wants a different horizon needs
// a setting, which is a smaller change than moving this into the registry and
// pretending it is law.
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

// KindBatchExpirySweep is the scheduled scan of lot dates.
const KindBatchExpirySweep = "stock.batch_expiry_sweep"

// ExpiringSoonDays is how far ahead a warning looks.
const ExpiringSoonDays = 30

// BatchExpirySweeper warns about lots that are near or past their date.
type BatchExpirySweeper struct {
	pool   *db.Pool
	notify *notify.Service
}

// NewBatchExpirySweeper builds the handler.
func NewBatchExpirySweeper(pool *db.Pool, n *notify.Service) *BatchExpirySweeper {
	return &BatchExpirySweeper{pool: pool, notify: n}
}

type expiringBatch struct {
	batchID   uuid.UUID
	companyID uuid.UUID
	batchNo   string
	name      string
	qty       string
	expires   string
	expired   bool
}

// Run sweeps every company in the tenant.
func (s *BatchExpirySweeper) Run(ctx context.Context, j Job) error {
	if j.TenantID == nil {
		return Permanent{errs.New(errs.CodeInternal,
			"This batch-expiry sweep names no tenant.")}
	}
	tenantID := *j.TenantID

	// Read first, announce after: Announce opens its own transaction, and
	// holding this one while it did so is the pool deadlock this codebase has
	// already met once.
	var found []expiringBatch
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT b.id, b.company_id, b.batch_no,
			       coalesce(p.name, v.sku),
			       b.qty_remaining::text,
			       to_char(b.expires_on, 'YYYY-MM-DD'),
			       (b.expires_on < current_date) AS expired
			FROM stock_batch b
			JOIN variant v ON v.id = b.variant_id
			LEFT JOIN product p ON p.id = v.product_id
			WHERE b.qty_remaining > 0
			  AND b.expires_on IS NOT NULL
			  AND b.recalled_at IS NULL
			  AND b.expires_on < current_date + ($1::int * INTERVAL '1 day')
			ORDER BY b.expires_on, b.batch_no`, ExpiringSoonDays)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var b expiringBatch
			if e := rows.Scan(&b.batchID, &b.companyID, &b.batchNo, &b.name,
				&b.qty, &b.expires, &b.expired); e != nil {
				return e
			}
			found = append(found, b)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}

	for _, b := range found {
		id := b.batchID
		title, body, severity := b.name+" is expiring soon", fmt.Sprintf(
			"Batch %s, %s left, expires %s.", b.batchNo, b.qty, b.expires),
			"warning"
		if b.expired {
			// A different message, because the action is different: this stock
			// cannot be sold and has to come off the shelf.
			title = b.name + " has expired"
			body = fmt.Sprintf("Batch %s expired on %s with %s still in stock.",
				b.batchNo, b.expires, b.qty)
			severity = "critical"
		}

		if e := s.notify.Announce(ctx, notify.Scope{
			TenantID: tenantID, CompanyID: b.companyID,
		}, notify.Fact{
			Kind:     notify.KindBatchExpiring,
			Severity: severity,
			Title:    title,
			Body:     body,
			// The BATCH, not the product: a shop with three lots of the same
			// item needs to be sent to the one that is going out of date.
			Subject:   "stock_batch",
			SubjectID: &id,
		}); e != nil {
			return e
		}
	}
	return nil
}
