package purchasing

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
)

// Landed cost, and the accrual.

// allocateLandedCost splits freight and duty across the lines that kept goods.
//
// `weights` is what each line is being apportioned by — value or quantity, as
// the receipt asked. `quantities` is how many units each line actually kept, and
// is only consulted when the weights cannot divide the cost at all.
//
// The parts must sum to exactly the whole. Three lines sharing 500 by thirds is
// 166.67 each and 500.01 total, and a cost that does not add up to what was paid
// puts the tie-out out by a hallala — which is the same amount of wrong as being
// out by a million, as far as the invariant is concerned.
//
// # Cumulative targets, not a remainder on the last line
//
// Rounding each line's own share and handing the difference to the last line
// makes the parts sum to the whole, but it lets the last share go NEGATIVE, and
// `grn_line_landed_alloc_sane` forbids that. The CHECK fires, the transaction
// aborts, and a delivery that genuinely arrived cannot be recorded at all —
// however many times the buyer retries.
//
// Seven lines and SAR 100 of freight is enough, if the last line was sent back:
// six lines of equal value each take round(100/6, 4) = 16.6667, which is 100.0002
// between them, and the seventh is handed 100 - 100.0002 = -0.0002.
//
// Taking each share as the difference between successive CUMULATIVE targets
// cannot go negative, because the targets only ever rise. It also bounds the
// rounding error at one ten-thousandth for the whole receipt rather than one per
// line, and because the final target is the total itself the shares still sum
// back to it exactly.
//
// This is the same defect and the same fix as `allocateFee` in settlement and
// the invoice-discount allocation in sales/pricing.go, both of which say in
// their own comments that the obvious approach is wrong. It was written a third
// time here anyway, which is the argument for stating the rule in each place it
// is relied on rather than trusting anyone to remember it.
//
// Import VAT is NOT here. E2.5: duty is inventory cost, import VAT is
// recoverable, and mixing them overstates stock while understating the reclaim.
func allocateLandedCost(
	total decimal.Decimal, weights, quantities []decimal.Decimal,
) []decimal.Decimal {
	out := make([]decimal.Decimal, len(weights))
	for i := range out {
		out[i] = decimal.Zero
	}
	if !total.IsPositive() || len(weights) == 0 {
		return out
	}

	sum := sumOf(weights)
	if !sum.IsPositive() {
		// Nothing to apportion by value: the goods came free, or every line was
		// sent back. Quantity still divides it correctly over free goods, so
		// that is tried before giving up.
		//
		// Putting the whole cost on the first line — which is what this used to
		// do — is wrong twice over. It does not spread a cost that belongs to
		// every unit, and the first line may be one of the REJECTED ones, in
		// which case the caller skips it for having no units to raise the cost
		// of and the freight silently disappears from the valuation while
		// grn_line still claims it was allocated.
		weights, sum = quantities, sumOf(quantities)
	}
	if !sum.IsPositive() {
		// Not one unit was kept. There is no stock to capitalise freight into,
		// and recording an allocation that no unit cost can carry would put
		// grn_line at odds with the valuation. Left at zero deliberately.
		return out
	}

	allocated := decimal.Zero
	cumulative := decimal.Zero
	for i, w := range weights {
		cumulative = cumulative.Add(w)

		// Rounded at cost precision, which is what cost_layer.unit_cost and
		// grn_line.landed_cost_alloc both carry.
		target := total.Mul(cumulative).Div(sum).Round(4)
		out[i] = target.Sub(allocated)
		allocated = allocated.Add(out[i])
	}
	return out
}

func sumOf(values []decimal.Decimal) decimal.Decimal {
	out := decimal.Zero
	for _, v := range values {
		out = out.Add(v)
	}
	return out
}

// postReceiptAccrual records that stock arrived and is not yet invoiced.
//
// Dr Inventory / Cr GRNI, through the seeded purchase.accrual rule. Without it
// the valuation runs ahead of the Inventory control account for the whole
// window between a delivery and its bill, which design 02 §6.6 forbids — the
// divergence is the full value of the receipt, and it is not small.
//
// No tax here. A supplier who has not invoiced has not charged tax, and there
// is nothing to reclaim until they do.
func (s *Service) postReceiptAccrual(
	ctx context.Context, tx pgx.Tx, scope Scope,
	grnID uuid.UUID, receivedOn time.Time, value decimal.Decimal, memo string,
) error {
	if value.IsZero() {
		// A delivery entirely rejected moves no value. Posting a zero entry
		// would leave a journal row that balances and says nothing.
		return nil
	}

	var country string
	if err := tx.QueryRow(ctx,
		`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
		Scan(&country); err != nil {
		return err
	}

	_, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date: receivedOn, SourceType: "goods_receipt", SourceID: grnID,
		PostedBy: &scope.UserID, RuleKey: "purchase.accrual",
		Memo: memo,
	}, country, accounting.Transaction{
		Amounts: map[string]decimal.Decimal{"net_amount": value},
	})
	return err
}

// postCostCorrection books what this delivery put right on earlier sales that
// sold below zero (C13).
//
// The amount is the difference between what those units really cost and the
// provisional figure already charged to cost of goods sold. It moves the
// Inventory account, because the arriving stock has just been drawn down again
// by units that were sold before it landed, and the difference is an expense
// that was mis-stated rather than a new one.
//
// Two rules, one per direction, and the amount is passed as its absolute value.
// A single rule taking a signed figure would write a negative debit where a
// credit belongs, and a trial balance carrying negative debits is one an
// accountant cannot read.
func (s *Service) postCostCorrection(
	ctx context.Context, tx pgx.Tx, scope Scope,
	grnID uuid.UUID, receivedOn time.Time, value decimal.Decimal, memo string,
) error {
	if value.IsZero() {
		// Either nothing was owed, or the goods arrived at exactly the price
		// the till guessed. Both are the ordinary case and neither is an entry.
		return nil
	}

	// Unfavourable: the goods cost more than the estimate, so the expense rises
	// and the stock just received is worth less than its invoice suggested.
	ruleKey := "inventory.variance"
	if value.IsNegative() {
		ruleKey = "inventory.variance_favourable"
	}

	var country string
	if err := tx.QueryRow(ctx,
		`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
		Scan(&country); err != nil {
		return err
	}

	_, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: scope.TenantID, CompanyID: scope.CompanyID,
		Date: receivedOn, SourceType: "goods_receipt", SourceID: grnID,
		PostedBy: &scope.UserID, RuleKey: ruleKey,
		Memo: memo,
	}, country, accounting.Transaction{
		Amounts: map[string]decimal.Decimal{"variance": value.Abs()},
	})
	return err
}
