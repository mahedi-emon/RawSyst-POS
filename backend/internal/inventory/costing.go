// Package inventory owns stock movements and the costing engine.
//
// # Why costing decides accounting correctness
//
// Cost of goods sold is booked at the moment of sale (blueprint C13), so gross
// profit is a measurement rather than a month-end reconstruction. That makes
// the costing method part of the accounting path, not a reporting preference:
// whatever this package returns is what the journal debits, and what the
// inventory valuation must tie back to.
//
// The invariant C13 calls hard is that the valuation ties EXACTLY to the
// Inventory control account. "Exactly" is why a consumption that empties the
// stock charges out precisely what was held rather than a recomputed figure —
// the same remainder rule the invoice-discount allocation and the partial-
// return credit already follow.
//
// # Two storage models, because there are two ideas
//
// FIFO and standard costing consume identifiable receipts, so they read cost
// layers. Weighted average does not: the moment costs are averaged, which
// physical unit came from which receipt stops being knowable, so it reads a
// single pool. Forcing both onto layers is what broke the tie-out — see
// migration 0021 for the arithmetic.
package inventory

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// costScale is the precision costs are held at.
//
// Four decimals, not two. A unit cost is an intermediate value that gets
// multiplied by a quantity, and rounding it to two before that multiplication
// loses real money on any line of more than a few units.
const costScale int32 = 4

// moneyScale is the precision the LEDGER holds an amount at, mirrored from the
// posting engine. Costs are computed at costScale and become journal amounts at
// this one, and the gap between the two is what P34 was: the valuation reported
// four decimals of stock while the ledger carried two of money, and
// `inventory_gl_difference` compared them directly.
//
// Every figure this package hands a caller to post is therefore the DELTA OF
// THE ROUNDED VALUATION rather than the rounded cost — see `valuationDelta`.
const moneyScale int32 = 2

// valuationDelta is what a movement changes the reported valuation by.
//
// This is the whole of the P34 fix, in three lines. The valuation is a sum of
// amounts rounded to money precision, so the amount posted against a movement
// has to be the difference between the rounded value before it and the rounded
// value after — never the rounded difference, which is a different number.
//
// Charging the rounded cost instead leaves a residue every time a value does
// not land on a hallala, and the residues accumulate: three receipts at
// 33.3333, 16.6667 and 99.9999 value at 716.6663 against a ledger of 716.67,
// and two sales off that pool part the two by a further hallala. Taking the
// delta makes the postings telescope, so their sum is the valuation by
// construction rather than by luck.
//
// It is the rounding-remainder rule the rest of this product already applies in
// five places, stated for stock: when a whole is split, the parts must add back
// to it.
func valuationDelta(before, after decimal.Decimal) decimal.Decimal {
	return before.Round(moneyScale).Sub(after.Round(moneyScale))
}

// Method is how a company values its stock.
type Method string

const (
	// MethodWAC recomputes a running weighted average on every receipt.
	MethodWAC Method = "wac"

	// MethodFIFO consumes the oldest layers first.
	MethodFIFO Method = "fifo"

	// MethodStandard values at a fixed cost and books the difference to a
	// variance account, so an unexpected purchase price is visible rather than
	// quietly absorbed into margin.
	MethodStandard Method = "standard"
)

// Layer is one open receipt of stock.
type Layer struct {
	ID           uuid.UUID
	QtyRemaining decimal.Decimal
	UnitCost     decimal.Decimal
}

// Consumption says how much came out of one layer.
type Consumption struct {
	LayerID uuid.UUID
	Qty     decimal.Decimal
	Cost    decimal.Decimal
}

// CostResult is the outcome of taking stock out.
type CostResult struct {
	// TotalCost is what the journal debits to Cost of Goods Sold.
	TotalCost decimal.Decimal

	// Consumed records which layers were drawn down, so the caller can write
	// them back. Empty under standard costing, which does not draw on layers.
	Consumed []Consumption

	// Variance is the difference between the standard cost booked and what the
	// stock actually cost. Only standard costing produces it.
	Variance decimal.Decimal

	// ShortBy is set when there was less stock than the sale needed. The
	// caller decides what to do with it according to the company's negative
	// stock policy — it is not this package's business to block a sale.
	ShortBy decimal.Decimal

	// ShortUnitCost is what the uncovered units were charged at: the estimate
	// each method fell back on when it ran out of stock to cost against.
	//
	// Reported rather than left implicit because C13 calls that figure
	// PROVISIONAL and requires it to be corrected on the next receipt. The
	// correction is actual cost less this, so a caller that could not see it
	// would have nothing to compare against and the shortfall would stay
	// mis-costed forever, which is exactly what used to happen.
	ShortUnitCost decimal.Decimal

	// PoolQtyAfter and PoolValueAfter are what the weighted-average pool must
	// be written back as. Only weighted average produces them: it holds one
	// pooled cost rather than identifiable layers.
	//
	// They are returned rather than left for the caller to subtract, because a
	// caller computing the new value itself would round a second time and that
	// second rounding is precisely what stops the valuation tying to the
	// ledger.
	PoolQtyAfter   decimal.Decimal
	PoolValueAfter decimal.Decimal
}

// ConsumeFIFO takes quantity from the oldest layers first.
//
// Layers must already be ordered oldest-first; the caller reads them that way
// from the indexed query, and re-sorting here would hide a mistake in it.
func ConsumeFIFO(layers []Layer, qty decimal.Decimal) (CostResult, error) {
	if !qty.IsPositive() {
		return CostResult{}, errs.New(errs.CodeInternal,
			"A consumption quantity must be positive.")
	}

	var out CostResult
	remaining := qty

	for _, l := range layers {
		if !remaining.IsPositive() {
			break
		}
		if !l.QtyRemaining.IsPositive() {
			continue
		}

		take := decimal.Min(remaining, l.QtyRemaining)

		// The layer's rounded value before and after this draw. `qty_remaining
		// * unit_cost` is what the valuation reports for this layer, so the
		// cost charged is exactly what the valuation loses — which is not the
		// same as rounding `take * unit_cost`, and the difference is the drift.
		cost := valuationDelta(
			l.QtyRemaining.Mul(l.UnitCost),
			l.QtyRemaining.Sub(take).Mul(l.UnitCost),
		)

		out.Consumed = append(out.Consumed, Consumption{
			LayerID: l.ID, Qty: take, Cost: cost,
		})
		out.TotalCost = out.TotalCost.Add(cost)
		remaining = remaining.Sub(take)
	}

	if remaining.IsPositive() {
		// Sold more than the layers hold. The cost of the shortfall is valued at
		// the most recent known cost, which is the closest available estimate,
		// and the caller is told how much was uncovered so the company's
		// negative-stock policy can decide.
		//
		// Guessing zero here would understate COGS and overstate profit, which
		// is the more dangerous direction: it flatters the numbers.
		fallback := decimal.Zero
		if len(layers) > 0 {
			fallback = layers[len(layers)-1].UnitCost
		}
		// The uncovered units never touched a layer, so there is no valuation
		// to difference. They are charged at money precision directly, which is
		// what the shortfall record will later be settled against.
		out.TotalCost = out.TotalCost.Add(remaining.Mul(fallback).Round(moneyScale))
		out.ShortBy = remaining
		out.ShortUnitCost = fallback
	}

	return out, nil
}

// ConsumeWAC takes quantity at the current weighted average.
//
// It reads a pool, not layers, and that distinction is the whole correctness
// argument. Averaging the layers to get a cost while draining them at their own
// costs moves the ledger and the valuation by different amounts: 10 at 50 plus
// 10 at 60, selling 15, charges 825 but releases 800 of layer value, and the
// balance sheet parts company with the stock report by 25 on the very first
// sale. Under weighted average there is conceptually one pool at one cost, so
// that is what is stored.
func ConsumeWAC(pool Pool, qty decimal.Decimal) (CostResult, error) {
	if !qty.IsPositive() {
		return CostResult{}, errs.New(errs.CodeInternal,
			"A consumption quantity must be positive.")
	}

	var out CostResult

	if !pool.QtyOnHand.IsPositive() {
		// The average of nothing is undefined, not zero. Costing this at zero
		// would book a sale with no cost and overstate profit, so the shortfall
		// is reported and the caller's negative-stock policy decides.
		out.ShortBy = qty
		return out, nil
	}

	if qty.GreaterThanOrEqual(pool.QtyOnHand) {
		// Taking everything: charge exactly what the pool is REPORTED to hold,
		// so it empties to precisely zero on both sides rather than leaving a
		// rounding residue sitting on the balance sheet as stock that does not
		// exist.
		out.TotalCost = valuationDelta(pool.TotalValue, decimal.Zero)
		if qty.GreaterThan(pool.QtyOnHand) {
			out.ShortBy = qty.Sub(pool.QtyOnHand)
			// The uncovered units are valued at the pool's own average, which is
			// the best estimate available. They never sat in the pool, so there
			// is no valuation to difference and they are charged at money
			// precision directly.
			average := pool.TotalValue.Div(pool.QtyOnHand).Round(costScale)
			out.TotalCost = out.TotalCost.Add(out.ShortBy.Mul(average).Round(moneyScale))
			out.ShortUnitCost = average
		}
		out.PoolQtyAfter = decimal.Zero
		out.PoolValueAfter = decimal.Zero
		return out, nil
	}

	average := pool.TotalValue.Div(pool.QtyOnHand).Round(costScale)

	// The pool moves by the cost at COST precision, because that is what keeps
	// the average honest for the next sale — design 02 §335: the total is
	// authoritative and the average is derived from it, so the total must not
	// be nudged to make a journal amount convenient.
	drawn := qty.Mul(average).Round(costScale)
	out.PoolQtyAfter = pool.QtyOnHand.Sub(qty)
	out.PoolValueAfter = pool.TotalValue.Sub(drawn)

	// The LEDGER moves by what the valuation actually lost, which is the
	// difference between the rounded value before and after. The two differ by
	// under a hallala per sale and never accumulate, because the next sale
	// differences from the same rounded figure this one left behind.
	out.TotalCost = valuationDelta(pool.TotalValue, out.PoolValueAfter)

	return out, nil
}

// Pool is the weighted-average position for one variant in one warehouse.
//
// Total value is authoritative and the average is derived from it. Storing the
// average instead would round twice — once on the way in, once on use — and the
// second rounding is exactly what makes a valuation stop tying to the ledger.
type Pool struct {
	QtyOnHand  decimal.Decimal
	TotalValue decimal.Decimal
}

// Average is the current weighted average, or zero when there is no stock.
func (p Pool) Average() decimal.Decimal {
	if !p.QtyOnHand.IsPositive() {
		return decimal.Zero
	}
	return p.TotalValue.Div(p.QtyOnHand).Round(costScale)
}

// ReceiveIntoPool folds a receipt into the weighted average.
//
// The new average falls out of the totals rather than being blended from the
// old average and the new cost, which would compound the previous rounding into
// every subsequent one.
func ReceiveIntoPool(pool Pool, qty, unitCost decimal.Decimal) (Pool, error) {
	if !qty.IsPositive() {
		return Pool{}, errs.New(errs.CodeInvalidInput,
			"A receipt quantity must be positive.")
	}
	if unitCost.IsNegative() {
		return Pool{}, errs.New(errs.CodeInvalidInput,
			"A receipt cost cannot be negative.")
	}
	return Pool{
		QtyOnHand:  pool.QtyOnHand.Add(qty),
		TotalValue: pool.TotalValue.Add(qty.Mul(unitCost)),
	}, nil
}

// ConsumeStandard values at a fixed cost and reports the variance.
func ConsumeStandard(
	layers []Layer, qty, standardCost decimal.Decimal,
) (CostResult, error) {
	if !qty.IsPositive() {
		return CostResult{}, errs.New(errs.CodeInternal,
			"A consumption quantity must be positive.")
	}
	if standardCost.IsNegative() {
		return CostResult{}, errs.New(errs.CodeInvalidInput,
			"A standard cost cannot be negative.")
	}

	// What the stock actually cost, so the difference is visible rather than
	// absorbed silently into margin.
	actual, err := ConsumeFIFO(layers, qty)
	if err != nil {
		return CostResult{}, err
	}

	out := CostResult{
		TotalCost: qty.Mul(standardCost).Round(costScale),
		Consumed:  actual.Consumed,
		ShortBy:   actual.ShortBy,
	}
	if out.ShortBy.IsPositive() {
		// Standard costing charges every unit at standard, uncovered ones
		// included, so that — not the FIFO fallback — is what the correction
		// has to be measured against.
		out.ShortUnitCost = standardCost
	}
	out.Variance = actual.TotalCost.Sub(out.TotalCost)
	return out, nil
}

// Request is everything a consumption needs, whichever method is in force.
//
// The methods genuinely need different inputs — FIFO and standard read layers,
// weighted average reads the pool — so the caller loads both and Consume takes
// only what its method uses. Passing the unused one is free; guessing which to
// load is how a caller ends up costing a sale against an empty slice.
type Request struct {
	Method       Method
	Qty          decimal.Decimal
	Layers       []Layer
	Pool         Pool
	StandardCost decimal.Decimal
}

// Compute dispatches on the company's configured method.
func Compute(req Request) (CostResult, error) {
	switch req.Method {
	case MethodFIFO:
		return ConsumeFIFO(req.Layers, req.Qty)
	case MethodWAC:
		return ConsumeWAC(req.Pool, req.Qty)
	case MethodStandard:
		return ConsumeStandard(req.Layers, req.Qty, req.StandardCost)
	default:
		return CostResult{}, errs.Newf(errs.CodeInternal,
			"%q is not a costing method this system knows.", req.Method)
	}
}

// Valuation is the value of the open layers, which is what FIFO and standard
// costing hold. Weighted average is valued from its pool instead — see
// Pool.TotalValue — because averaging the layers is exactly what stopped the
// two agreeing.
func Valuation(layers []Layer) decimal.Decimal {
	total := decimal.Zero
	for _, l := range layers {
		if l.QtyRemaining.IsPositive() {
			total = total.Add(l.QtyRemaining.Mul(l.UnitCost))
		}
	}
	return total
}

// OnHand is the quantity across the open layers.
func OnHand(layers []Layer) decimal.Decimal {
	total := decimal.Zero
	for _, l := range layers {
		if l.QtyRemaining.IsPositive() {
			total = total.Add(l.QtyRemaining)
		}
	}
	return total
}

// NegativeStockPolicy is what a company does when a sale exceeds stock.
type NegativeStockPolicy string

const (
	// PolicyBlock refuses the sale. Correct for serialised or high-value goods
	// where selling something absent is a real problem.
	PolicyBlock NegativeStockPolicy = "block"

	// PolicyAllowWarn lets the sale complete and flags it. Correct for a busy
	// shop where the stock record lags reality and refusing a customer standing
	// at the till is worse than a correction later.
	PolicyAllowWarn NegativeStockPolicy = "allow_warn"
)

// CheckAvailability applies the policy to a shortfall.
//
// Returns nil when the sale may proceed. Under allow_warn a shortfall is not an
// error at all — blueprint C13 says the cost self-corrects on the next receipt,
// and the exception report is what surfaces it.
func CheckAvailability(
	policy NegativeStockPolicy, result CostResult, description string,
) error {
	if !result.ShortBy.IsPositive() {
		return nil
	}
	if policy == PolicyAllowWarn {
		return nil
	}
	// Worded for whatever is taking the stock, not for a sale.
	//
	// A purchase return uses this too -- sending back goods the shelf does not
	// hold is the same shortfall wearing a different hat -- and "than this sale
	// needs" would have read as nonsense on a debit note screen.
	return errs.Newf(errs.CodeConflict,
		"There are %s fewer %s in stock than this needs. Count the shelf and "+
			"adjust the stock, or ask an owner to allow stock to go below zero.",
		result.ShortBy.String(), description)
}
