package inventory

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Every function here takes a pgx.Tx rather than opening its own.
//
// A sale must move stock, allocate an invoice counter, write the invoice and
// post the journal as one atomic act — blueprint C9.1. A store that opened its
// own transaction could not participate in that, and a sale that committed its
// stock movement but rolled back its journal would leave the valuation and the
// ledger permanently apart. Composability here is a correctness requirement,
// not a style preference.
//
// The caller holds the tenant context (db.Pool.TxAsTenant), so row-level
// security is already in force on every statement below.

// Receipt is stock arriving: a purchase, an opening balance, a transfer in.
type Receipt struct {
	TenantID    uuid.UUID
	CompanyID   uuid.UUID
	VariantID   uuid.UUID
	WarehouseID uuid.UUID

	Qty decimal.Decimal

	// UnitCost INCLUDES allocated landed cost — purchase plus shipping plus
	// customs (blueprint B4). Import VAT is deliberately excluded: E2.5 puts
	// duty in inventory cost and import VAT in recoverable input tax, and
	// mixing them overstates stock while understating the reclaim.
	UnitCost decimal.Decimal

	Reason     string
	SourceType string
	SourceID   *uuid.UUID
	Note       string
}

// Issue is stock leaving: a sale, wastage, a transfer out.
type Issue struct {
	TenantID    uuid.UUID
	CompanyID   uuid.UUID
	VariantID   uuid.UUID
	WarehouseID uuid.UUID

	Qty decimal.Decimal

	Reason     string
	SourceType string
	SourceID   *uuid.UUID
	DeviceID   *uuid.UUID
	Note       string

	// StandardCost is read only under standard costing.
	StandardCost decimal.Decimal
}

// Receive records stock arriving and updates both cost stores.
//
// Both stores are written whichever method is in force. A company that switches
// from FIFO to weighted average must not find its new valuation empty, and one
// cannot be back-filled from the other afterwards — layers no longer hold the
// costs a pool would need, and a pool never held the receipt identities layers
// need.
func Receive(ctx context.Context, tx pgx.Tx, r Receipt) error {
	if !r.Qty.IsPositive() {
		return errs.New(errs.CodeInvalidInput,
			"A stock receipt must be for a positive quantity.")
	}
	if r.UnitCost.IsNegative() {
		return errs.New(errs.CodeInvalidInput,
			"A stock receipt cannot have a negative cost.")
	}

	value := r.Qty.Mul(r.UnitCost).Round(costScale)

	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_movement
		  (tenant_id, company_id, variant_id, warehouse_id, delta, reason,
		   source_type, source_id, value_delta, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		r.TenantID, r.CompanyID, r.VariantID, r.WarehouseID, r.Qty, r.Reason,
		nullText(r.SourceType), r.SourceID, value, nullText(r.Note)); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO cost_layer
		  (tenant_id, company_id, variant_id, warehouse_id,
		   qty_received, qty_remaining, unit_cost, source_type, source_id)
		VALUES ($1,$2,$3,$4,$5,$5,$6,$7,$8)`,
		r.TenantID, r.CompanyID, r.VariantID, r.WarehouseID,
		r.Qty, r.UnitCost, nullText(r.SourceType), r.SourceID); err != nil {
		return err
	}

	// The pool takes the receipt's total value, not its unit cost. Blending
	// averages instead of summing totals compounds each receipt's rounding into
	// every later one, and the drift can never be reconciled back to anything.
	_, err := tx.Exec(ctx, `
		INSERT INTO stock_valuation
		  (tenant_id, company_id, variant_id, warehouse_id, qty_on_hand, total_value)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (variant_id, warehouse_id) DO UPDATE SET
		  qty_on_hand = stock_valuation.qty_on_hand + EXCLUDED.qty_on_hand,
		  total_value = stock_valuation.total_value + EXCLUDED.total_value`,
		r.TenantID, r.CompanyID, r.VariantID, r.WarehouseID, r.Qty, value)
	return err
}

// Restoration is stock coming back: a customer return, a cancelled transfer.
//
// It carries a total VALUE rather than a unit cost, and that is the whole point.
// What comes back must be worth exactly what was charged out, because the same
// figure is credited to the Inventory account in the journal. Restoring at
// today's cost instead would move the valuation and the ledger by different
// amounts and break C13's tie-out on the first return after a price change.
type Restoration struct {
	TenantID    uuid.UUID
	CompanyID   uuid.UUID
	VariantID   uuid.UUID
	WarehouseID uuid.UUID

	Qty   decimal.Decimal
	Value decimal.Decimal

	Reason     string
	SourceType string
	SourceID   *uuid.UUID
	DeviceID   *uuid.UUID
	Note       string
}

// Restore puts stock back at exactly the value it left at.
func Restore(ctx context.Context, tx pgx.Tx, r Restoration) error {
	if !r.Qty.IsPositive() {
		return errs.New(errs.CodeInvalidInput,
			"A stock restoration must be for a positive quantity.")
	}
	if r.Value.IsNegative() {
		return errs.New(errs.CodeInvalidInput,
			"Stock cannot come back worth less than nothing.")
	}

	method, err := companyMethod(ctx, tx, r.CompanyID)
	if err != nil {
		return err
	}

	if method == MethodWAC {
		// The pool takes quantity and value directly, so it is exact by
		// construction.
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_valuation
			  (tenant_id, company_id, variant_id, warehouse_id, qty_on_hand, total_value)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (variant_id, warehouse_id) DO UPDATE SET
			  qty_on_hand = stock_valuation.qty_on_hand + EXCLUDED.qty_on_hand,
			  total_value = stock_valuation.total_value + EXCLUDED.total_value`,
			r.TenantID, r.CompanyID, r.VariantID, r.WarehouseID,
			r.Qty, r.Value); err != nil {
			return err
		}
	} else {
		for _, part := range splitIntoLayers(r.Qty, r.Value) {
			if _, err := tx.Exec(ctx, `
				INSERT INTO cost_layer
				  (tenant_id, company_id, variant_id, warehouse_id,
				   qty_received, qty_remaining, unit_cost, source_type, source_id)
				VALUES ($1,$2,$3,$4,$5,$5,$6,$7,$8)`,
				r.TenantID, r.CompanyID, r.VariantID, r.WarehouseID,
				part.qty, part.unitCost, nullText(r.SourceType), r.SourceID); err != nil {
				return err
			}
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO stock_movement
		  (tenant_id, company_id, variant_id, warehouse_id, delta, reason,
		   source_type, source_id, device_id, value_delta, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.TenantID, r.CompanyID, r.VariantID, r.WarehouseID,
		r.Qty, r.Reason, nullText(r.SourceType), r.SourceID, r.DeviceID,
		r.Value, nullText(r.Note))
	return err
}

type layerPart struct{ qty, unitCost decimal.Decimal }

// splitIntoLayers turns a quantity and a total value into layers whose
// quantities times their unit costs sum back to that value EXACTLY.
//
// A layer is valued as qty_remaining × unit_cost, so one layer at value/qty is
// only exact when the division comes out even. Three units worth 199.99 give a
// unit cost of 66.6633, which multiplies back to 199.9899 — a hundredth of a
// hallala adrift, and enough to fail a tie-out that must be exact.
//
// So the odd unit is split off and takes the remainder, the same rule the
// invoice discount, the partial-return credit and the base-currency allocation
// already follow. Whichever part carries the leftover, the parts sum to the
// whole.
func splitIntoLayers(qty, value decimal.Decimal) []layerPart {
	unit := value.Div(qty).Round(costScale)
	if unit.Mul(qty).Equal(value) {
		return []layerPart{{qty: qty, unitCost: unit}}
	}

	// One unit is set aside to absorb the difference. Quantities need not be
	// whole — a shop sells 2.5 metres of cloth — so the bulk is everything but
	// a single unit, and a fractional total below one unit is handled by the
	// even case above or falls through to a single remainder layer.
	one := decimal.NewFromInt(1)
	if qty.LessThanOrEqual(one) {
		return []layerPart{{qty: qty, unitCost: value.Div(qty)}}
	}

	bulkQty := qty.Sub(one)
	bulkValue := unit.Mul(bulkQty)

	return []layerPart{
		{qty: bulkQty, unitCost: unit},
		{qty: one, unitCost: value.Sub(bulkValue)},
	}
}

// Consume costs stock out by the company's method, draws down whichever store
// that method uses, and records the movement.
//
// It returns the cost the journal must debit to Cost of Goods Sold. The caller
// posts that figure inside this same transaction — that is what keeps C13's
// tie-out exact, and why this function will not post it itself: the journal
// belongs to the accounting engine, and a stock package that wrote journal
// lines would put the posting rules in two places.
//
// A shortfall is reported rather than refused. Whether selling below zero is
// allowed is the company's negative-stock policy (C13), which the caller
// applies with CheckAvailability.
func Consume(ctx context.Context, tx pgx.Tx, iss Issue) (CostResult, error) {
	if !iss.Qty.IsPositive() {
		return CostResult{}, errs.New(errs.CodeInvalidInput,
			"A stock issue must be for a positive quantity.")
	}

	method, err := companyMethod(ctx, tx, iss.CompanyID)
	if err != nil {
		return CostResult{}, err
	}

	req := Request{Method: method, Qty: iss.Qty, StandardCost: iss.StandardCost}

	// Only load what the method reads. Loading both would be harmless but would
	// hide a mistake in the method dispatch, because a wrong branch would still
	// find data to work from.
	switch method {
	case MethodWAC:
		if req.Pool, err = readPool(ctx, tx, iss.VariantID, iss.WarehouseID); err != nil {
			return CostResult{}, err
		}
	default:
		if req.Layers, err = readLayers(ctx, tx, iss.VariantID, iss.WarehouseID); err != nil {
			return CostResult{}, err
		}
	}

	result, err := Compute(req)
	if err != nil {
		return CostResult{}, err
	}

	if method == MethodWAC {
		// Written as the engine computed it, never recomputed here. Subtracting
		// at this point would round a second time, and the second rounding is
		// exactly what stops a valuation tying to the ledger.
		if _, err := tx.Exec(ctx, `
			UPDATE stock_valuation SET qty_on_hand = $3, total_value = $4
			WHERE variant_id = $1 AND warehouse_id = $2`,
			iss.VariantID, iss.WarehouseID,
			result.PoolQtyAfter, result.PoolValueAfter); err != nil {
			return CostResult{}, err
		}
	} else {
		for _, c := range result.Consumed {
			if _, err := tx.Exec(ctx, `
				UPDATE cost_layer SET qty_remaining = qty_remaining - $2
				WHERE id = $1`, c.LayerID, c.Qty); err != nil {
				return CostResult{}, err
			}
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_movement
		  (tenant_id, company_id, variant_id, warehouse_id, delta, reason,
		   source_type, source_id, device_id, value_delta, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		iss.TenantID, iss.CompanyID, iss.VariantID, iss.WarehouseID,
		iss.Qty.Neg(), iss.Reason, nullText(iss.SourceType), iss.SourceID,
		iss.DeviceID, result.TotalCost.Neg(), nullText(iss.Note)); err != nil {
		return CostResult{}, err
	}

	return result, nil
}

func companyMethod(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (Method, error) {
	var m string
	err := tx.QueryRow(ctx,
		`SELECT costing_method::text FROM company WHERE id = $1`, companyID).Scan(&m)
	if errors.Is(err, pgx.ErrNoRows) {
		// Under row-level security a company in another tenant reads as absent,
		// which is the intended answer: it does not exist as far as this caller
		// is concerned.
		return "", errs.New(errs.CodeNotFound, "That company was not found.")
	}
	if err != nil {
		return "", err
	}
	return Method(m), nil
}

// readLayers reads the open layers oldest-first, which is the order FIFO
// consumption requires. Sorting in Go afterwards would mask a mistake here.
func readLayers(ctx context.Context, tx pgx.Tx, variantID, warehouseID uuid.UUID) ([]Layer, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, qty_remaining, unit_cost FROM cost_layer
		WHERE variant_id = $1 AND warehouse_id = $2 AND qty_remaining > 0
		ORDER BY received_at, id`, variantID, warehouseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Layer
	for rows.Next() {
		var l Layer
		if err := rows.Scan(&l.ID, &l.QtyRemaining, &l.UnitCost); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// readPool reads the weighted-average position. A variant never received is an
// empty pool, not an error — Consume reports it as a shortfall so the company's
// negative-stock policy decides.
func readPool(ctx context.Context, tx pgx.Tx, variantID, warehouseID uuid.UUID) (Pool, error) {
	var p Pool
	err := tx.QueryRow(ctx, `
		SELECT qty_on_hand, total_value FROM stock_valuation
		WHERE variant_id = $1 AND warehouse_id = $2`,
		variantID, warehouseID).Scan(&p.QtyOnHand, &p.TotalValue)
	if errors.Is(err, pgx.ErrNoRows) {
		return Pool{}, nil
	}
	return p, err
}

// OnHandAt is the quantity of one variant in one warehouse.
func OnHandAt(ctx context.Context, tx pgx.Tx, variantID, warehouseID uuid.UUID) (decimal.Decimal, error) {
	var qty decimal.Decimal
	err := tx.QueryRow(ctx, `SELECT stock_on_hand($1,$2)`,
		variantID, warehouseID).Scan(&qty)
	return qty, err
}

// ValuationOf is the company's stock valuation, read by whichever method that
// company uses.
func ValuationOf(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (decimal.Decimal, error) {
	var v decimal.Decimal
	err := tx.QueryRow(ctx, `SELECT inventory_valuation($1)`, companyID).Scan(&v)
	return v, err
}

// GLDifference is C13's hard invariant as a number: the valuation less the
// Inventory control account balance, which must be zero.
//
// C13 says "any divergence is flagged as an exception", so the nightly job, the
// acceptance test and a support engineer looking at a live tenant all ask the
// same question and get the same answer.
func GLDifference(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (decimal.Decimal, error) {
	var d decimal.Decimal
	err := tx.QueryRow(ctx, `SELECT inventory_gl_difference($1)`, companyID).Scan(&d)
	return d, err
}

// nullText keeps optional text out of the database as NULL rather than as an
// empty string, so "not recorded" and "recorded as blank" stay distinguishable.
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
