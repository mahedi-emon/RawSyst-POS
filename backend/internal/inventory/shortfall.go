package inventory

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The other half of selling below zero.
//
// Blueprint C13 lets a company sell stock it does not have
// (negative_stock_policy = allow_warn) and is precise about the price of that
// permission: "cost is provisional and auto-corrected on the next receipt of
// that item". Consume does the first half — it charges the best estimate it has
// and records what it had to guess. This file does the second.
//
// # Why the correction cannot live in Consume
//
// The real cost of those units is not knowable at the sale. It becomes knowable
// when the goods arrive, which may be days later and at a price nobody
// predicted — which is the entire reason C13 calls the first figure
// provisional. So the correction is triggered by the receipt, not by the sale.
//
// # Why this returns the adjustment instead of posting it
//
// The same rule the rest of the package follows: the journal belongs to the
// accounting engine. A stock package that wrote journal lines would put the
// posting rules in two places, and the two would drift. The caller posts the
// figure through rule 11 (inventory.variance) or 11a
// (inventory.variance_favourable) inside the same transaction, which is what
// keeps C13's tie-out exact across the correction.

// Correction is one provisional cost put right.
type Correction struct {
	ShortfallID uuid.UUID

	// Qty is how many of the shortfall's units this receipt covered. Less than
	// the whole when the delivery was smaller than the hole it was filling.
	Qty decimal.Decimal

	// Provisional is what those units were charged at the sale, Actual is what
	// they turned out to cost.
	Provisional decimal.Decimal
	Actual      decimal.Decimal

	// Settled is true when this closed the shortfall rather than reducing it.
	Settled bool
}

// Adjustment is what the journal must move: actual less provisional. Positive
// means the goods cost more than the estimate, so cost of goods sold was
// understated and margin was flattered.
func (c Correction) Adjustment() decimal.Decimal {
	return c.Actual.Sub(c.Provisional)
}

// Settlement is everything one receipt corrected.
type Settlement struct {
	// Adjustment is the net across every correction, signed. The caller posts
	// its absolute value through whichever direction's rule matches the sign,
	// because a rule that took a negative amount would write a negative debit
	// rather than a credit.
	Adjustment decimal.Decimal

	// QtySettled is how many previously uncovered units this receipt covered.
	QtySettled decimal.Decimal

	// Corrections is the per-shortfall detail, so the exception report can show
	// which sale was re-costed rather than only that something was.
	Corrections []Correction
}

// Posted reports whether there is anything for the caller to post. A receipt
// that filled a hole at exactly the estimated cost settles the shortfall and
// moves no money, which is a success rather than a no-op.
func (s Settlement) Posted() bool {
	return !s.Adjustment.IsZero()
}

// SettleShortfalls corrects the provisional cost of earlier sales against stock
// that has now arrived, oldest shortfall first.
//
// Call it after Receive, in the same transaction, and post the adjustment it
// returns.
//
// # Oldest first, and against the oldest stock
//
// Both orderings matter and they are not the same choice. The oldest SHORTFALL
// is settled first because that is the sale that has been mis-costed longest.
// The stock it is settled against is drawn oldest-first through the ordinary
// costing engine, which handles a case that would otherwise be wrong: if the
// customer returned the goods before the supplier delivered, the returned layer
// is the oldest and is priced at exactly what was charged out, so the
// correction is correctly zero rather than a spurious variance against the new
// delivery's price.
//
// # It settles only what has actually arrived
//
// A shop 30 units short that receives 10 has corrected 10 of them. The rest
// stays open and waits for the next delivery, because there is no stock to cost
// the remainder against and inventing one would be the same mistake again.
func SettleShortfalls(
	ctx context.Context, tx pgx.Tx, companyID, variantID, warehouseID uuid.UUID,
) (Settlement, error) {
	method, err := companyMethod(ctx, tx, companyID)
	if err != nil {
		return Settlement{}, err
	}

	open, err := readOpenShortfalls(ctx, tx, variantID, warehouseID)
	if err != nil {
		return Settlement{}, err
	}

	var out Settlement
	for _, sf := range open {
		available, err := availableQty(ctx, tx, method, variantID, warehouseID)
		if err != nil {
			return Settlement{}, err
		}

		settleQty := decimal.Min(sf.outstanding, available)
		if !settleQty.IsPositive() {
			// Nothing on the shelf to cost against. Later shortfalls are newer,
			// so there is nothing they could be settled against either.
			break
		}

		actual, err := drawDown(ctx, tx, method, variantID, warehouseID, settleQty)
		if err != nil {
			return Settlement{}, err
		}

		c := Correction{
			ShortfallID: sf.id,
			Qty:         settleQty,
			Provisional: settleQty.Mul(sf.provisionalUnitCost).Round(costScale),
			Actual:      actual,
			Settled:     settleQty.Equal(sf.outstanding),
		}

		if _, err := tx.Exec(ctx, `
			UPDATE cost_shortfall SET
			  qty_settled = qty_settled + $2,
			  adjustment  = adjustment + $3,
			  settled_at  = CASE WHEN qty_settled + $2 >= qty THEN now() END
			WHERE id = $1`,
			sf.id, settleQty, c.Adjustment()); err != nil {
			return Settlement{}, err
		}

		out.Adjustment = out.Adjustment.Add(c.Adjustment())
		out.QtySettled = out.QtySettled.Add(settleQty)
		out.Corrections = append(out.Corrections, c)
	}

	return out, nil
}

type openShortfall struct {
	id                  uuid.UUID
	outstanding         decimal.Decimal
	provisionalUnitCost decimal.Decimal
}

// readOpenShortfalls reads what is still uncovered, oldest first and locked.
//
// Locked because two deliveries of the same item arriving at once would
// otherwise both read the same shortfall as open and both settle it, correcting
// one hole twice and leaving the valuation short by the second correction.
func readOpenShortfalls(
	ctx context.Context, tx pgx.Tx, variantID, warehouseID uuid.UUID,
) ([]openShortfall, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, qty - qty_settled, provisional_unit_cost
		FROM cost_shortfall
		WHERE variant_id = $1 AND warehouse_id = $2 AND qty_settled < qty
		ORDER BY occurred_at, id
		FOR UPDATE`, variantID, warehouseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []openShortfall
	for rows.Next() {
		var s openShortfall
		if err := rows.Scan(&s.id, &s.outstanding, &s.provisionalUnitCost); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// availableQty is how much there is to settle against, read from whichever
// store the company's method keeps its costs in.
func availableQty(
	ctx context.Context, tx pgx.Tx, method Method, variantID, warehouseID uuid.UUID,
) (decimal.Decimal, error) {
	if method == MethodWAC {
		pool, err := readPool(ctx, tx, variantID, warehouseID)
		if err != nil {
			return decimal.Zero, err
		}
		return pool.QtyOnHand, nil
	}
	layers, err := readLayers(ctx, tx, variantID, warehouseID)
	if err != nil {
		return decimal.Zero, err
	}
	return OnHand(layers), nil
}

// drawDown takes the settled quantity out of the cost store and reports what it
// actually cost.
//
// No stock movement is written. The units left the building at the sale and
// that movement was recorded then, quantity and provisional value together;
// writing another would count the same goods out twice. What was wrong was the
// VALUE on that movement, and a movement cannot be edited — so the correction
// is carried on the shortfall row and in the journal entry the caller posts,
// which is the same shape as any other accounting correction.
//
// Standard costing draws at the layers' actual cost like FIFO rather than
// through ConsumeStandard. Its variance at issue was booked for the units it
// covered; these units were never covered, so what is wanted here is what they
// really cost, to compare against the standard already charged.
func drawDown(
	ctx context.Context, tx pgx.Tx, method Method,
	variantID, warehouseID uuid.UUID, qty decimal.Decimal,
) (decimal.Decimal, error) {
	if method == MethodWAC {
		pool, err := readPool(ctx, tx, variantID, warehouseID)
		if err != nil {
			return decimal.Zero, err
		}
		result, err := ConsumeWAC(pool, qty)
		if err != nil {
			return decimal.Zero, err
		}
		if result.ShortBy.IsPositive() {
			return decimal.Zero, errs.New(errs.CodeInternal,
				"A cost correction tried to settle more units than the pool holds.")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE stock_valuation SET qty_on_hand = $3, total_value = $4
			WHERE variant_id = $1 AND warehouse_id = $2`,
			variantID, warehouseID,
			result.PoolQtyAfter, result.PoolValueAfter); err != nil {
			return decimal.Zero, err
		}
		return result.TotalCost, nil
	}

	layers, err := readLayers(ctx, tx, variantID, warehouseID)
	if err != nil {
		return decimal.Zero, err
	}
	result, err := ConsumeFIFO(layers, qty)
	if err != nil {
		return decimal.Zero, err
	}
	if result.ShortBy.IsPositive() {
		return decimal.Zero, errs.New(errs.CodeInternal,
			"A cost correction tried to settle more units than the layers hold.")
	}
	for _, c := range result.Consumed {
		if _, err := tx.Exec(ctx, `
			UPDATE cost_layer SET qty_remaining = qty_remaining - $2
			WHERE id = $1`, c.LayerID, c.Qty); err != nil {
			return decimal.Zero, err
		}
	}
	return result.TotalCost, nil
}
