//go:build integration

// Batch / lot / expiry tracking, end to end (blueprint B4, B1).
//
// The lot-level equivalent of `stock_serial`, and the one a grocery or a
// pharmacy actually runs on. None of it existed before 0107.
//
// What these tests hold to:
//
//   - selection is FEFO, so the carton expiring soonest leaves first
//   - costing is UNCHANGED — the company's method still values the issue, and
//     C13's tie-out still holds
//   - a return goes back into the lot it left in, never into the one expiring
//     soonest, because those are the same physical units
//   - an untracked product is unaffected by any of it
package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/inventory"
)

// trackBatches turns B1's flag on for the fixture's variant.
func trackBatches(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE variant SET tracks_batches = true WHERE id = $1`, f.variantID)
		return e
	}); err != nil {
		t.Fatalf("turn on batch tracking: %v", err)
	}
}

// receiveBatch books a delivery of one lot.
func receiveBatch(
	t *testing.T, h *harness, f *shopFixture, batchNo string,
	qty, cost string, expires *time.Time,
) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := inventory.Receive(t.Context(), tx, inventory.Receipt{
			TenantID: f.tenantID, CompanyID: f.companyID,
			VariantID: f.variantID, WarehouseID: f.warehouseID,
			Qty:      decimal.RequireFromString(qty),
			UnitCost: decimal.RequireFromString(cost),
			Reason:   "grn",
			Batch: &inventory.BatchInput{
				BatchNo: batchNo, ExpiresOn: expires,
			},
		})
		return e
	}); err != nil {
		t.Fatalf("receive batch %s: %v", batchNo, err)
	}
}

func batchRemaining(t *testing.T, h *harness, f *shopFixture, batchNo string) string {
	t.Helper()
	var left string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT qty_remaining::text FROM stock_batch
			 WHERE variant_id = $1 AND batch_no = $2`,
			f.variantID, batchNo).Scan(&left)
	}); err != nil {
		t.Fatalf("read batch %s: %v", batchNo, err)
	}
	return left
}

func issue(t *testing.T, h *harness, f *shopFixture, qty string) error {
	t.Helper()
	return h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := inventory.Consume(t.Context(), tx, inventory.Issue{
			TenantID: f.tenantID, CompanyID: f.companyID,
			VariantID: f.variantID, WarehouseID: f.warehouseID,
			Qty:    decimal.RequireFromString(qty),
			Reason: "sale",
		})
		return e
	})
}

func expiryIn(offset int) *time.Time {
	d := time.Now().UTC().AddDate(0, 0, offset)
	return &d
}

// The lot expiring soonest leaves first, whatever order it arrived in.
//
// This is the whole reason FEFO exists rather than FIFO. The batch received
// SECOND expires FIRST, so it must go first — otherwise the shop sells the
// long-dated carton and writes off the short-dated one.
func TestStockLeavesInExpiryOrderNotReceiptOrder(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)

	receiveBatch(t, h, f, "OLD-LONG", "10", "5.00", expiryIn(365)) // first in, expires last
	receiveBatch(t, h, f, "NEW-SHORT", "10", "5.00", expiryIn(7))  // second in, expires first

	if err := issue(t, h, f, "6"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	// The short-dated lot gave up six; the long-dated one is untouched.
	if got := batchRemaining(t, h, f, "NEW-SHORT"); got != "4.0000" {
		t.Errorf("short-dated lot has %s left, want 4.0000", got)
	}
	if got := batchRemaining(t, h, f, "OLD-LONG"); got != "10.0000" {
		t.Errorf("long-dated lot has %s left, want 10.0000 — FEFO took from "+
			"the wrong lot", got)
	}
}

// An issue larger than one lot continues into the next, in expiry order.
func TestAnIssueSpanningTwoLotsTakesTheEarlierExpiryFirst(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)

	receiveBatch(t, h, f, "SHORT", "5", "5.00", expiryIn(7))
	receiveBatch(t, h, f, "LONG", "10", "5.00", expiryIn(365))

	if err := issue(t, h, f, "8"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	if got := batchRemaining(t, h, f, "SHORT"); got != "0.0000" {
		t.Errorf("short lot has %s left, want 0.0000", got)
	}
	if got := batchRemaining(t, h, f, "LONG"); got != "7.0000" {
		t.Errorf("long lot has %s left, want 7.0000", got)
	}
}

// A lot with no expiry sorts after dated ones, then by receipt.
//
// FIFO by another name, which is the right fallback: an undated lot says
// nothing about urgency, so the only remaining order is the order it arrived.
func TestAnUndatedLotIsUsedAfterDatedOnes(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)

	receiveBatch(t, h, f, "NODATE", "5", "5.00", nil)
	receiveBatch(t, h, f, "DATED", "5", "5.00", expiryIn(30))

	if err := issue(t, h, f, "5"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	if got := batchRemaining(t, h, f, "DATED"); got != "0.0000" {
		t.Errorf("the dated lot has %s left, want 0.0000 — an undated lot was "+
			"used before a dated one", got)
	}
	if got := batchRemaining(t, h, f, "NODATE"); got != "5.0000" {
		t.Errorf("the undated lot has %s left, want 5.0000", got)
	}
}

// A tracked product cannot be received without naming its lot.
//
// The flag is the shop's statement that it needs to know which carton is which.
// Accepting a delivery without one would create stock the lot layer cannot
// account for, and the drift would never be visible.
func TestATrackedProductCannotBeReceivedWithoutALot(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)

	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := inventory.Receive(t.Context(), tx, inventory.Receipt{
			TenantID: f.tenantID, CompanyID: f.companyID,
			VariantID: f.variantID, WarehouseID: f.warehouseID,
			Qty:      decimal.RequireFromString("5"),
			UnitCost: decimal.RequireFromString("5.00"),
			Reason:   "grn",
		})
		return e
	})
	if err == nil {
		t.Error("a batch-tracked product was received with no lot named")
	}
}

// An untracked product refuses a lot.
//
// The other direction. A batch recorded against untracked stock would never be
// drawn from, and its quantity would drift from the movements for ever.
func TestAnUntrackedProductRefusesALot(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner") // tracking deliberately left off

	err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := inventory.Receive(t.Context(), tx, inventory.Receipt{
			TenantID: f.tenantID, CompanyID: f.companyID,
			VariantID: f.variantID, WarehouseID: f.warehouseID,
			Qty:      decimal.RequireFromString("5"),
			UnitCost: decimal.RequireFromString("5.00"),
			Reason:   "grn",
			Batch:    &inventory.BatchInput{BatchNo: "X1"},
		})
		return e
	})
	if err == nil {
		t.Error("an untracked product accepted a lot number")
	}
}

// An issue with not enough in tracked lots is refused, not part-allocated.
//
// Stock that exists in `stock_movement` and belongs to no lot is untraceable in
// exactly the situation lot tracking is kept for.
func TestAnIssueBeyondTheTrackedLotsIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)

	receiveBatch(t, h, f, "ONLY", "3", "5.00", expiryIn(30))

	if err := issue(t, h, f, "5"); err == nil {
		t.Error("an issue larger than the tracked lots was allowed")
	}
	// And nothing was taken: a refused issue must leave the lot whole.
	if got := batchRemaining(t, h, f, "ONLY"); got != "3.0000" {
		t.Errorf("the lot has %s left after a refused issue, want 3.0000", got)
	}
}

// A recalled lot is not sold.
//
// The point of a recall: the stock is still on the shelf and must stop leaving
// it. The lot keeps its history — what was already sold from it is the whole
// reason to record it — so it is withdrawn rather than deleted.
func TestARecalledLotIsNotIssued(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)

	receiveBatch(t, h, f, "BAD", "10", "5.00", expiryIn(7))   // would be first by FEFO
	receiveBatch(t, h, f, "GOOD", "10", "5.00", expiryIn(90)) //

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			UPDATE stock_batch
			SET recalled_at = now(), recall_reason = 'supplier notice'
			WHERE variant_id = $1 AND batch_no = 'BAD'`, f.variantID)
		return e
	}); err != nil {
		t.Fatalf("recall: %v", err)
	}

	if err := issue(t, h, f, "4"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	if got := batchRemaining(t, h, f, "BAD"); got != "10.0000" {
		t.Errorf("the recalled lot gave up stock: %s left, want 10.0000", got)
	}
	if got := batchRemaining(t, h, f, "GOOD"); got != "6.0000" {
		t.Errorf("the good lot has %s left, want 6.0000", got)
	}
}

// Batch tracking does not change what a sale costs.
//
// The invariant this whole design rests on: a batch says which carton left, the
// company's costing method says what it was worth. If turning tracking on moved
// the valuation, every batch-tracked company would silently become
// specific-identification costed and C13's tie-out would stop holding.
func TestTurningOnBatchTrackingDoesNotChangeTheCostOfASale(t *testing.T) {
	h := newHarness(t)

	// Two identical shops; one tracks batches.
	plain := h.seedShop(t, "owner")
	tracked := h.seedShop(t, "owner")
	trackBatches(t, h, tracked)

	// Same two receipts at different costs, so FIFO has something to choose.
	for _, s := range []struct {
		f    *shopFixture
		lots bool
	}{{plain, false}, {tracked, true}} {
		if err := h.pool.TxAsTenant(t.Context(), s.f.tenantID, func(tx pgx.Tx) error {
			for i, c := range []string{"4.00", "6.00"} {
				r := inventory.Receipt{
					TenantID: s.f.tenantID, CompanyID: s.f.companyID,
					VariantID: s.f.variantID, WarehouseID: s.f.warehouseID,
					Qty:      decimal.RequireFromString("10"),
					UnitCost: decimal.RequireFromString(c),
					Reason:   "grn",
				}
				if s.lots {
					r.Batch = &inventory.BatchInput{
						BatchNo:   "L" + uuid.NewString()[:4],
						ExpiresOn: expiryIn(30 + i*30),
					}
				}
				if _, e := inventory.Receive(t.Context(), tx, r); e != nil {
					return e
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("receive: %v", err)
		}
	}

	cost := func(f *shopFixture) decimal.Decimal {
		var got decimal.Decimal
		if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
			res, e := inventory.Consume(t.Context(), tx, inventory.Issue{
				TenantID: f.tenantID, CompanyID: f.companyID,
				VariantID: f.variantID, WarehouseID: f.warehouseID,
				Qty:    decimal.RequireFromString("12"),
				Reason: "sale",
			})
			got = res.TotalCost
			return e
		}); err != nil {
			t.Fatalf("consume: %v", err)
		}
		return got
	}

	if a, b := cost(plain), cost(tracked); !a.Equal(b) {
		t.Errorf("cost of the same sale is %s untracked and %s batch-tracked; "+
			"batch selection must not change valuation", a, b)
	}
}
