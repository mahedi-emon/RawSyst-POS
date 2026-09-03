// Batch / lot / expiry tracking (blueprint B4).
//
// # Two axes, deliberately kept apart
//
// A batch answers WHICH PHYSICAL LOT left the shelf. Costing answers WHAT VALUE
// left the books. This product already has a careful answer to the second —
// FIFO consumes identifiable cost layers, weighted average consumes a pool, and
// C13's tie-out holds exactly for both — so batches do not become a third
// costing method. `Consume` still values an issue by the company's costing
// method; this file records which lot the goods came out of.
//
// Making batch selection drive valuation would silently convert every
// batch-tracked company to specific-identification costing, changing their
// reported margin and breaking the tie-out the acceptance tests prove.
//
// # Selection is FEFO
//
// First Expired, First Out. For perishable stock the received order is the
// wrong order: a carton received today expiring next week must leave before one
// received last month expiring next year, or the shop sells the wrong one and
// writes off the other. A batch with no expiry date sorts last and then by
// receipt, which is FIFO by another name.
package inventory

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// BatchInput is the lot a receipt arrived in.
//
// The batch number is the supplier's own, as printed on the carton. It is never
// generated here: an invented lot number cannot be matched against a recall
// notice, which is the one moment this field has to work.
type BatchInput struct {
	BatchNo        string
	ManufacturedOn *time.Time
	ExpiresOn      *time.Time
	SupplierID     *uuid.UUID
}

// TracksBatches reports whether a variant is lot-tracked (B1's flag).
func TracksBatches(
	ctx context.Context, tx pgx.Tx, variantID uuid.UUID,
) (bool, error) {
	var tracks bool
	err := tx.QueryRow(ctx,
		`SELECT tracks_batches FROM variant WHERE id = $1`, variantID).
		Scan(&tracks)
	if err == pgx.ErrNoRows {
		return false, errs.New(errs.CodeNotFound,
			"That item is not in this company's catalogue. Check the "+
				"barcode, or add the item first.")
	}
	return tracks, err
}

// receiveIntoBatch adds a receipt's quantity to its lot, creating it if new.
//
// A second delivery of the same lot number adds to the existing batch rather
// than creating a rival row, which is what keeps "how much of lot 24B is left"
// answerable with one number. The expiry and manufacture dates are taken from
// the FIRST delivery and not overwritten: the same lot number arriving with a
// different expiry is a supplier error worth seeing rather than silently
// resolving in either direction.
func receiveIntoBatch(
	ctx context.Context, tx pgx.Tx, r Receipt, movementID uuid.UUID,
) error {
	b := r.Batch
	var batchID uuid.UUID

	err := tx.QueryRow(ctx, `
		INSERT INTO stock_batch
		  (tenant_id, company_id, variant_id, warehouse_id, batch_no,
		   manufactured_on, expires_on, qty_received, qty_remaining,
		   unit_cost, supplier_id, source_type, source_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$9,$10,$11,$12)
		ON CONFLICT (variant_id, warehouse_id, batch_no) DO UPDATE
		  SET qty_received  = stock_batch.qty_received  + excluded.qty_received,
		      qty_remaining = stock_batch.qty_remaining + excluded.qty_remaining
		RETURNING id`,
		r.TenantID, r.CompanyID, r.VariantID, r.WarehouseID, b.BatchNo,
		b.ManufacturedOn, b.ExpiresOn, r.Qty, r.UnitCost, b.SupplierID,
		nullText(r.SourceType), r.SourceID).Scan(&batchID)
	if err != nil {
		return db.Translate(err, "That batch could not be recorded.")
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO stock_batch_movement (tenant_id, batch_id, movement_id, qty)
		VALUES ($1,$2,$3,$4)`,
		r.TenantID, batchID, movementID, r.Qty)
	return db.Translate(err, "")
}

// batchAllocation is one lot's share of an issue.
type batchAllocation struct {
	BatchID uuid.UUID
	BatchNo string
	Qty     decimal.Decimal
}

// allocateFEFO takes qty from the open lots, earliest expiry first.
//
// The rows are locked FOR UPDATE in the same order every caller reads them, so
// two tills selling the last of a lot at the same moment queue rather than both
// seeing it available. Without the lock the second would decrement a quantity
// the first had already taken, and `stock_batch_remaining_in_range` would turn
// that into a constraint violation at commit — correct, but reported as a
// database error rather than as "there is not enough left".
func allocateFEFO(
	ctx context.Context, tx pgx.Tx, variantID, warehouseID uuid.UUID,
	qty decimal.Decimal,
) ([]batchAllocation, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, batch_no, qty_remaining
		FROM stock_batch
		WHERE variant_id = $1 AND warehouse_id = $2
		  AND qty_remaining > 0
		  AND recalled_at IS NULL
		ORDER BY expires_on NULLS LAST, received_at
		FOR UPDATE`, variantID, warehouseID)
	if err != nil {
		return nil, db.Translate(err, "")
	}

	type open struct {
		id        uuid.UUID
		no        string
		remaining decimal.Decimal
	}
	var lots []open
	for rows.Next() {
		var o open
		if e := rows.Scan(&o.id, &o.no, &o.remaining); e != nil {
			rows.Close()
			return nil, e
		}
		lots = append(lots, o)
	}
	rows.Close()
	if e := rows.Err(); e != nil {
		return nil, e
	}

	var out []batchAllocation
	left := qty
	for _, o := range lots {
		if !left.IsPositive() {
			break
		}
		take := o.remaining
		if take.GreaterThan(left) {
			take = left
		}
		out = append(out, batchAllocation{BatchID: o.id, BatchNo: o.no, Qty: take})
		left = left.Sub(take)
	}

	if left.IsPositive() {
		// Refused rather than part-allocated. A batch-tracked issue that
		// covered only some of its quantity would leave stock that exists in
		// `stock_movement` and belongs to no lot — untraceable in exactly the
		// situation lot tracking is kept for.
		//
		// This is a different shortfall from the negative-stock policy, which
		// governs whether a shop may sell what it does not have at all. A
		// company that allows negative stock still cannot say which lot the
		// goods it does not have came from.
		return nil, errs.Newf(errs.CodeConflict,
			"There is not enough of this product in tracked batches to cover "+
				"%s. Batch-tracked stock cannot be issued from outside a lot; "+
				"receive the delivery against its batch first.", qty.String())
	}
	return out, nil
}

// consumeFromBatches applies an allocation and records the split.
func consumeFromBatches(
	ctx context.Context, tx pgx.Tx, tenantID, movementID uuid.UUID,
	allocations []batchAllocation,
) error {
	for _, a := range allocations {
		if _, err := tx.Exec(ctx, `
			UPDATE stock_batch
			SET qty_remaining = qty_remaining - $2
			WHERE id = $1`, a.BatchID, a.Qty); err != nil {
			return db.Translate(err,
				"That issue could not be taken from batch "+a.BatchNo+".")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_batch_movement
			  (tenant_id, batch_id, movement_id, qty)
			VALUES ($1,$2,$3,$4)`,
			tenantID, a.BatchID, movementID, a.Qty.Neg()); err != nil {
			return db.Translate(err, "")
		}
	}
	return nil
}

// restoreToBatches puts returned goods back into the lots they came from.
//
// # Why the original movement decides, and not FEFO
//
// A return is not a receipt. The goods coming back are the same physical units
// that went out, so they belong to the lot they left in — and putting them into
// whichever lot expires soonest would quietly relabel them, which is the one
// thing lot tracking exists to prevent. A carton returned from lot 24A must not
// come back as lot 24B.
//
// Where the original issue cannot be identified — a return with no source
// invoice — nothing is restored to a batch and the caller is told, rather than
// guessing a lot.
func restoreToBatches(
	ctx context.Context, tx pgx.Tx, tenantID uuid.UUID,
	originalSourceType string, originalSourceID *uuid.UUID,
	variantID, warehouseID uuid.UUID, qty decimal.Decimal, movementID uuid.UUID,
) error {
	if originalSourceID == nil {
		return errs.New(errs.CodeInvalidInput,
			"A batch-tracked return has to name the sale it is reversing, so "+
				"the goods go back into the lot they left in.")
	}

	// What that sale took, newest lot last so the return unwinds in the order
	// the issue was allocated.
	rows, err := tx.Query(ctx, `
		SELECT bm.batch_id, b.batch_no, -bm.qty
		FROM stock_batch_movement bm
		JOIN stock_batch b     ON b.id = bm.batch_id
		JOIN stock_movement m  ON m.id = bm.movement_id
		WHERE m.source_type = $1 AND m.source_id = $2
		  AND m.variant_id = $3 AND m.warehouse_id = $4
		  AND bm.qty < 0
		ORDER BY bm.created_at`,
		originalSourceType, originalSourceID, variantID, warehouseID)
	if err != nil {
		return db.Translate(err, "")
	}

	type taken struct {
		id  uuid.UUID
		no  string
		qty decimal.Decimal
	}
	var lots []taken
	for rows.Next() {
		var t taken
		if e := rows.Scan(&t.id, &t.no, &t.qty); e != nil {
			rows.Close()
			return e
		}
		lots = append(lots, t)
	}
	rows.Close()
	if e := rows.Err(); e != nil {
		return e
	}
	if len(lots) == 0 {
		return errs.New(errs.CodeNotFound,
			"The sale this return reverses did not draw from a tracked batch, "+
				"so the goods cannot be put back into one.")
	}

	left := qty
	for _, l := range lots {
		if !left.IsPositive() {
			break
		}
		give := l.qty
		if give.GreaterThan(left) {
			give = left
		}
		if _, err := tx.Exec(ctx, `
			UPDATE stock_batch
			SET qty_remaining = qty_remaining + $2
			WHERE id = $1`, l.id, give); err != nil {
			return db.Translate(err,
				"That return could not be put back into batch "+l.no+".")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_batch_movement
			  (tenant_id, batch_id, movement_id, qty)
			VALUES ($1,$2,$3,$4)`,
			tenantID, l.id, movementID, give); err != nil {
			return db.Translate(err, "")
		}
		left = left.Sub(give)
	}

	if left.IsPositive() {
		// More is coming back than went out on that sale. Refused rather than
		// invented into a lot: the quantity is wrong, and creating stock in a
		// batch nobody issued would make the lot's history untrue.
		return errs.Newf(errs.CodeConflict,
			"This return is for more than the sale took from tracked batches, "+
				"by %s.", left.String())
	}
	return nil
}
