//go:build integration

// Selling below zero, and the correction C13 promises for it.
//
// Blueprint C13 permits a company to sell stock it does not have and states the
// terms: "cost is provisional and auto-corrected on the next receipt of that
// item". Two things were wrong before this file existed.
//
// The correction did not happen. The costing engine charged its best estimate,
// reported the shortfall, and nothing recorded which units had been guessed at,
// so nothing could ever revisit them. A shop that habitually sells ahead of its
// paperwork carried a permanently wrong cost of goods sold.
//
// And the tie-out silently broke while stock was negative. Selling five units
// from a shelf holding two credits the Inventory account for all five — that is
// what went to cost of goods sold — while the layers empty and value nothing.
// The valuation said one thing and the ledger another, and C13 calls that
// divergence an exception. TestASaleBelowZeroStillTiesToTheLedger below is the
// test that failed.
package inventory

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// deliver does what the receiving path does: takes the stock in, settles
// whatever earlier sales had to guess at, and posts both — in ONE transaction.
//
// One transaction because that is the guarantee. A receipt that committed its
// stock while its correction rolled back would leave the valuation and the
// ledger apart by the adjustment, permanently, which is the failure the whole
// tie-out invariant exists to catch.
func (b *books) deliver(t *testing.T, qty, unitCost string) Settlement {
	t.Helper()
	ctx := context.Background()

	var settled Settlement
	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		// What the ENGINE says the valuation rose by. Multiplying it out here
		// instead would round a second time — 10 at 16.6667 is 166.667, which
		// the ledger holds as 166.67 — and the harness would tie out against
		// its own arithmetic rather than against the books.
		value, e := Receive(ctx, tx, Receipt{
			TenantID: b.tenantID, CompanyID: b.companyID,
			VariantID: b.variantID, WarehouseID: b.warehouseID,
			Qty: dec(qty), UnitCost: dec(unitCost), Reason: "grn",
		})
		if e != nil {
			return e
		}
		if e := b.entry(ctx, tx, "purchase",
			b.inventory, b.payable, value); e != nil {
			return e
		}

		settled, e = SettleShortfalls(ctx, tx,
			b.companyID, b.variantID, b.warehouseID)
		if e != nil {
			return e
		}
		if !settled.Posted() {
			return nil
		}

		// Rule 11 and its favourable twin, by hand: the amount is always
		// positive and the direction is chosen by the sign, because a negative
		// debit is not something a trial balance can be read with.
		debit, credit := b.variance, b.inventory
		if settled.Adjustment.IsNegative() {
			debit, credit = b.inventory, b.variance
		}
		return b.entry(ctx, tx, "goods_receipt",
			debit, credit, settled.Adjustment.Abs())
	})
	if err != nil {
		t.Fatalf("deliver %s at %s: %v", qty, unitCost, err)
	}
	return settled
}

// entry writes one balanced two-line journal entry.
func (b *books) entry(
	ctx context.Context, tx pgx.Tx, source string,
	debit, credit uuid.UUID, amount decimal.Decimal,
) error {
	var entryID uuid.UUID
	if e := tx.QueryRow(ctx, `
		INSERT INTO journal_entry
		  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
		VALUES ($1,$2,$3,claim_entry_no($2),'2026-08-15',$4) RETURNING id`,
		b.tenantID, b.companyID, b.periodID, source).Scan(&entryID); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, `
		INSERT INTO journal_line
		  (tenant_id, entry_id, line_no, account_id, currency,
		   debit, credit, base_debit, base_credit)
		VALUES ($1,$2,1,$3,'SAR',$4,0,$4,0)`,
		b.tenantID, entryID, debit, amount); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `
		INSERT INTO journal_line
		  (tenant_id, entry_id, line_no, account_id, currency,
		   debit, credit, base_debit, base_credit)
		VALUES ($1,$2,2,$3,'SAR',0,$4,0,$4)`,
		b.tenantID, entryID, credit, amount)
	return e
}

// openShortfallQty is what the company still owes an explanation for.
func (b *books) openShortfallQty(t *testing.T) decimal.Decimal {
	t.Helper()
	var qty decimal.Decimal
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT coalesce(sum(qty - qty_settled), 0) FROM cost_shortfall
			WHERE company_id = $1 AND qty_settled < qty`, b.companyID).Scan(&qty)
	}); err != nil {
		t.Fatalf("read open shortfalls: %v", err)
	}
	return qty
}

// layersOnHand is what the cost store thinks is there, which after a sale below
// zero is NOT the same as what the movements say — layers cannot go negative.
func (b *books) layersOnHand(t *testing.T) decimal.Decimal {
	t.Helper()
	var qty decimal.Decimal
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		if b.method == MethodWAC {
			return tx.QueryRow(context.Background(), `
				SELECT coalesce(sum(qty_on_hand), 0) FROM stock_valuation
				WHERE company_id = $1`, b.companyID).Scan(&qty)
		}
		return tx.QueryRow(context.Background(), `
			SELECT coalesce(sum(qty_remaining), 0) FROM cost_layer
			WHERE company_id = $1`, b.companyID).Scan(&qty)
	}); err != nil {
		t.Fatalf("read layers: %v", err)
	}
	return qty
}

// The invariant that was broken. C13 requires the valuation to tie EXACTLY to
// the Inventory control account, and says nothing about that holding only while
// stock is positive — a shop running allow_warn is doing what the policy allows.
//
// Before the shortfall was recorded this failed by the full value of the
// uncovered units: the ledger carried the cost of five and the layers valued
// zero, and no report could explain the gap.
func TestASaleBelowZeroStillTiesToTheLedger(t *testing.T) {
	for _, method := range []Method{MethodFIFO, MethodWAC} {
		t.Run(string(method), func(t *testing.T) {
			b := newBooks(t, method)

			b.receive(t, "2", "50.00")
			got := b.sell(t, "5")

			if !got.ShortBy.Equal(dec("3")) {
				t.Fatalf("short by %s, want 3", got.ShortBy)
			}
			if !got.ShortUnitCost.Equal(dec("50")) {
				t.Errorf("the uncovered units were charged at %s, want 50",
					got.ShortUnitCost)
			}
			if diff := b.tieOutDifference(t); !diff.IsZero() {
				t.Fatalf("after selling 3 more than it had, the valuation is out "+
					"by %s; the balance sheet and the stock report disagree", diff)
			}
			if qty := b.openShortfallQty(t); !qty.Equal(dec("3")) {
				t.Errorf("%s units are recorded as provisionally costed, want 3", qty)
			}
		})
	}
}

// C13's promise, mechanised: the next receipt corrects the guess.
//
// Two units at 50 were on the shelf and five were sold, so three were charged
// at 50 apiece on the strength of the last known cost. They actually arrived at
// 60, so 30 of cost of goods sold was never recognised. After the delivery the
// correction is posted, the shortfall is closed, and the layers agree with the
// movements again — the delivery of ten leaves seven, not ten, because three of
// them were sold before they landed.
func TestTheNextReceiptCorrectsAProvisionalCost(t *testing.T) {
	for _, method := range []Method{MethodFIFO, MethodWAC} {
		t.Run(string(method), func(t *testing.T) {
			b := newBooks(t, method)

			b.receive(t, "2", "50.00")
			b.sell(t, "5")

			settled := b.deliver(t, "10", "60.00")

			if !settled.QtySettled.Equal(dec("3")) {
				t.Fatalf("settled %s units, want 3", settled.QtySettled)
			}
			if !settled.Adjustment.Equal(dec("30")) {
				t.Errorf("corrected the cost by %s, want 30 — three units that "+
					"were charged at 50 and cost 60", settled.Adjustment)
			}
			if qty := b.openShortfallQty(t); !qty.IsZero() {
				t.Errorf("%s units are still provisionally costed after the "+
					"delivery that covered them", qty)
			}
			if qty := b.layersOnHand(t); !qty.Equal(dec("7")) {
				t.Errorf("the cost store holds %s units, want 7: three of the "+
					"ten delivered were already sold", qty)
			}
			if diff := b.tieOutDifference(t); !diff.IsZero() {
				t.Fatalf("after the correction the valuation is out by %s", diff)
			}
		})
	}
}

// The case that costs the most and is easiest to get wrong: selling an item the
// system has never received.
//
// There is no last known cost and no pool average, so the engine charges
// nothing — cost of goods sold is zero and the sale looks like pure margin.
// That is the honest answer at the till and a badly wrong one on the P&L, and
// it is the whole reason the correction has to happen.
func TestSellingSomethingNeverReceivedCostsNothingUntilItArrives(t *testing.T) {
	for _, method := range []Method{MethodFIFO, MethodWAC} {
		t.Run(string(method), func(t *testing.T) {
			b := newBooks(t, method)

			got := b.sell(t, "3")

			if !got.TotalCost.IsZero() {
				t.Fatalf("charged %s for goods it never had, want 0", got.TotalCost)
			}
			if !got.ShortBy.Equal(dec("3")) {
				t.Fatalf("short by %s, want 3", got.ShortBy)
			}
			if diff := b.tieOutDifference(t); !diff.IsZero() {
				t.Fatalf("valuation out by %s on a sale from nothing", diff)
			}

			settled := b.deliver(t, "5", "20.00")

			if !settled.Adjustment.Equal(dec("60")) {
				t.Errorf("corrected by %s, want 60 — the entire cost of three "+
					"units at 20, none of which was ever recognised",
					settled.Adjustment)
			}
			if diff := b.tieOutDifference(t); !diff.IsZero() {
				t.Fatalf("after the correction the valuation is out by %s", diff)
			}
		})
	}
}

// A delivery smaller than the hole corrects only the part it covers.
//
// Ten units were sold uncovered and four arrive. Four are corrected and six
// stay provisional, because there is no stock to cost the remainder against and
// estimating one again would repeat the original mistake.
func TestAPartialDeliveryCorrectsOnlyWhatArrived(t *testing.T) {
	b := newBooks(t, MethodFIFO)

	b.receive(t, "2", "50.00")
	b.sell(t, "12")

	settled := b.deliver(t, "4", "60.00")

	if !settled.QtySettled.Equal(dec("4")) {
		t.Fatalf("settled %s units, want 4", settled.QtySettled)
	}
	if !settled.Adjustment.Equal(dec("40")) {
		t.Errorf("corrected by %s, want 40", settled.Adjustment)
	}
	if qty := b.openShortfallQty(t); !qty.Equal(dec("6")) {
		t.Errorf("%s units are still provisional, want 6", qty)
	}
	if diff := b.tieOutDifference(t); !diff.IsZero() {
		t.Fatalf("valuation out by %s after a partial correction", diff)
	}

	// The rest of the delivery arrives and finishes the job.
	rest := b.deliver(t, "6", "70.00")

	if !rest.Adjustment.Equal(dec("120")) {
		t.Errorf("corrected by %s, want 120 — six units charged at 50 that "+
			"cost 70", rest.Adjustment)
	}
	if qty := b.openShortfallQty(t); !qty.IsZero() {
		t.Errorf("%s units still provisional after both deliveries", qty)
	}
	if diff := b.tieOutDifference(t); !diff.IsZero() {
		t.Fatalf("valuation out by %s after the second correction", diff)
	}
}

// Goods that turn out CHEAPER than the estimate correct the other way.
//
// The direction matters: posted as a negative debit rather than a credit, the
// amount would still balance and a trial balance would become unreadable. The
// engine reports the sign and the caller chooses the rule.
func TestAnOverstatedCostIsCorrectedDownwards(t *testing.T) {
	b := newBooks(t, MethodFIFO)

	b.receive(t, "2", "50.00")
	b.sell(t, "5")

	settled := b.deliver(t, "10", "40.00")

	if !settled.Adjustment.Equal(dec("-30")) {
		t.Errorf("corrected by %s, want −30 — three units charged at 50 that "+
			"cost 40", settled.Adjustment)
	}
	if diff := b.tieOutDifference(t); !diff.IsZero() {
		t.Fatalf("valuation out by %s after a favourable correction", diff)
	}
}

// A shortfall is corrected once.
//
// The second delivery finds nothing to settle, so it posts nothing. Without the
// closing flag the same hole would be corrected against every subsequent
// receipt and cost of goods sold would drift further with each one.
func TestAnAlreadyCorrectedShortfallIsLeftAlone(t *testing.T) {
	b := newBooks(t, MethodFIFO)

	b.receive(t, "2", "50.00")
	b.sell(t, "5")
	b.deliver(t, "10", "60.00")

	again := b.deliver(t, "10", "90.00")

	if again.Posted() {
		t.Errorf("a second delivery corrected an already settled shortfall by %s",
			again.Adjustment)
	}
	if !again.QtySettled.IsZero() {
		t.Errorf("settled %s units the second time, want 0", again.QtySettled)
	}
	if diff := b.tieOutDifference(t); !diff.IsZero() {
		t.Fatalf("valuation out by %s after the second delivery", diff)
	}
}

// If the customer brings the goods back before the supplier delivers, there is
// nothing to correct.
//
// This is why the settlement draws stock oldest-first through the ordinary
// costing engine rather than pricing itself at the arriving delivery. A return
// is restored at exactly the value it was charged out at, so it becomes the
// oldest layer and settles the shortfall for precisely what the sale charged.
// Pricing the correction at the new delivery instead would invent a variance
// out of a purchase price that had nothing to do with those units.
func TestAReturnBeforeTheDeliveryLeavesNothingToCorrect(t *testing.T) {
	b := newBooks(t, MethodFIFO)

	b.receive(t, "2", "50.00")
	b.sell(t, "5")

	// Three come back, at the value they left at.
	ctx := context.Background()
	if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		if e := Restore(ctx, tx, Restoration{
			TenantID: b.tenantID, CompanyID: b.companyID,
			VariantID: b.variantID, WarehouseID: b.warehouseID,
			Qty: dec("3"), Value: dec("150"), Reason: "return",
		}); e != nil {
			return e
		}
		return b.entry(ctx, tx, "return",
			b.inventory, b.cogs, dec("150"))
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if diff := b.tieOutDifference(t); !diff.IsZero() {
		t.Fatalf("valuation out by %s after the return", diff)
	}

	settled := b.deliver(t, "10", "90.00")

	if !settled.QtySettled.Equal(dec("3")) {
		t.Fatalf("settled %s units, want 3", settled.QtySettled)
	}
	if settled.Posted() {
		t.Errorf("corrected by %s against a delivery that had nothing to do "+
			"with those units; the returned stock already covered them at cost",
			settled.Adjustment)
	}
	if diff := b.tieOutDifference(t); !diff.IsZero() {
		t.Fatalf("valuation out by %s after settling against the return", diff)
	}
}

// The deduction is really being applied, and this test exists so the tie-out
// tests above cannot pass vacuously.
//
// inventory_valuation before 0047 was the bare cost store — the sum of the open
// layers, or the pool. If the shortfall deduction were dropped tomorrow the
// tie-out assertions would still pass in every case where stock stayed
// positive, and quietly stop covering the one case they were written for. So
// this compares the function against what it used to return and requires them
// to differ by exactly the uncovered value.
func TestTheValuationDeductsWhatWasSoldAndNeverHeld(t *testing.T) {
	b := newBooks(t, MethodFIFO)

	b.receive(t, "2", "50.00")
	b.sell(t, "5")

	var reported, rawLayers decimal.Decimal
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		ctx := context.Background()
		if e := tx.QueryRow(ctx,
			`SELECT inventory_valuation($1)`, b.companyID).Scan(&reported); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT coalesce(sum(qty_remaining * unit_cost), 0) FROM cost_layer
			WHERE company_id = $1 AND qty_remaining > 0`,
			b.companyID).Scan(&rawLayers)
	}); err != nil {
		t.Fatalf("read valuations: %v", err)
	}

	if !rawLayers.IsZero() {
		t.Fatalf("the layers hold %s after selling everything and more", rawLayers)
	}
	// Three units charged out at 50 that the shelf never held.
	if !reported.Equal(dec("-150")) {
		t.Errorf("the valuation reports %s, want −150: the ledger was credited "+
			"for three units of stock that did not exist, and a valuation that "+
			"ignores them cannot tie to it", reported)
	}
}

// A shortfall belongs to the tenant that sold short, and to nobody else. It
// names a variant, a till and a cost, which is trading information.
func TestAShortfallIsInvisibleToAnotherTenant(t *testing.T) {
	seller := newBooks(t, MethodFIFO)
	other := newBooks(t, MethodFIFO)

	seller.receive(t, "2", "50.00")
	seller.sell(t, "5")

	var visible int
	if err := other.pool.TxAsTenant(context.Background(), other.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(),
				`SELECT count(*) FROM cost_shortfall`).Scan(&visible)
		}); err != nil {
		t.Fatalf("read as the other tenant: %v", err)
	}
	if visible != 0 {
		t.Errorf("another tenant can see %d shortfall rows", visible)
	}
}

// A shortfall is a fact about a sale. Settling it moves the settled quantity
// and the adjustment; what happened does not change, for the same reason a
// stock movement is immutable and a journal entry is reversed rather than
// edited.
func TestTheFactsOfAShortfallCannotBeRewritten(t *testing.T) {
	b := newBooks(t, MethodFIFO)

	b.receive(t, "2", "50.00")
	b.sell(t, "5")

	ctx := context.Background()
	for _, attempt := range []struct {
		what string
		sql  string
	}{
		{"the quantity", `UPDATE cost_shortfall SET qty = 99`},
		{"the provisional cost", `UPDATE cost_shortfall SET provisional_unit_cost = 1`},
		{"the variant", `UPDATE cost_shortfall SET variant_id = gen_random_uuid()`},
	} {
		err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, attempt.sql)
			return e
		})
		if err == nil {
			t.Errorf("%s of a recorded shortfall could be rewritten", attempt.what)
		}
	}

	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM cost_shortfall`)
		return e
	})
	if err == nil {
		t.Error("a recorded shortfall could be deleted")
	}
}

// A row cannot claim to be closed while it still owes units, nor stay open
// having settled them all. Either way the correction would run against it a
// second time, or stop deducting from the valuation while still uncovered.
func TestAShortfallCannotBeHalfClosed(t *testing.T) {
	b := newBooks(t, MethodFIFO)

	b.receive(t, "2", "50.00")
	b.sell(t, "5")

	ctx := context.Background()
	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE cost_shortfall SET qty_settled = 1, settled_at = now()`)
		return e
	})
	if err == nil {
		t.Error("a shortfall was stamped settled with units still uncovered")
	}
}
