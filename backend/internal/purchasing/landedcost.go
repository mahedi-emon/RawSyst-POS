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

// allocateLandedCost splits freight and duty across the lines by value.
//
// The remainder rule, for the sixth time in this codebase: the last line
// absorbs the difference so the parts sum to exactly the whole. Three lines
// sharing 500 by thirds is 166.67 each and 500.01 total, and a cost that does
// not add up to what was paid puts the tie-out out by a hallala — which is the
// same amount of wrong as being out by a million, as far as the invariant is
// concerned.
//
// Import VAT is NOT here. E2.5: duty is inventory cost, import VAT is
// recoverable, and mixing them overstates stock while understating the reclaim.
func allocateLandedCost(
	total decimal.Decimal, basis string, weights []decimal.Decimal,
) []decimal.Decimal {
	out := make([]decimal.Decimal, len(weights))
	for i := range out {
		out[i] = decimal.Zero
	}
	if !total.IsPositive() || len(weights) == 0 {
		return out
	}

	sum := decimal.Zero
	for _, w := range weights {
		sum = sum.Add(w)
	}
	if !sum.IsPositive() {
		// Nothing to apportion against — every line is worth nothing, or the
		// quantities are zero. The whole cost goes on the first line rather
		// than being silently dropped, because it was really paid.
		out[0] = total
		return out
	}

	running := decimal.Zero
	for i := 0; i < len(weights)-1; i++ {
		share := total.Mul(weights[i]).Div(sum).Round(4)
		out[i] = share
		running = running.Add(share)
	}
	// The last line takes what is left, whatever the rounding did.
	out[len(weights)-1] = total.Sub(running)
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
