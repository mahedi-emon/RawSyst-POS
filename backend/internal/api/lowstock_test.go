//go:build integration

// B4's minimum-stock alert.
//
// The Blueprint asks for "dashboard + notification when any product/variant
// crosses its reorder threshold". The dashboard half has counted low variants
// since reports.Overview. The notification half did not exist: reorder_level
// was stored, read once for that count and once by the forecaster, and nobody
// was ever told.
package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/notify"
)

// setReorderLevel puts a threshold on the fixture's variant.
func setReorderLevel(t *testing.T, h *harness, f *shopFixture, level string) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE variant SET reorder_level = $2::numeric WHERE id = $1`,
			f.variantID, level)
		return e
	}); err != nil {
		t.Fatalf("set reorder level: %v", err)
	}
}

// lowStockNotices counts the alerts raised for this company's variant.
func lowStockNotices(t *testing.T, h *harness, f *shopFixture) int {
	t.Helper()
	var n int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM notification
			WHERE company_id = $1 AND kind = $2`,
			f.companyID, notify.KindLowStock).Scan(&n)
	}); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return n
}

// runSweep drives the handler directly, as the worker would.
func runSweep(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	tenant := f.tenantID
	sweeper := jobs.NewLowStockSweeper(h.pool, notify.NewService(h.pool))
	if err := sweeper.Run(context.Background(), jobs.Job{
		TenantID: &tenant, Kind: jobs.KindLowStockSweep,
	}); err != nil {
		t.Fatalf("low-stock sweep: %v", err)
	}
}

// A variant at or below its reorder level raises an alert naming it.
//
// The fixture stocks 10. A threshold of 10 means "at or below", which is what
// a reorder LEVEL means: the point at which you reorder, not the point after
// which you have already run out.
func TestAVariantAtItsReorderLevelRaisesAnAlert(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	setReorderLevel(t, h, f, "10")

	if n := lowStockNotices(t, h, f); n != 0 {
		t.Fatalf("there are already %d low-stock alerts", n)
	}

	runSweep(t, h, f)

	if n := lowStockNotices(t, h, f); n != 1 {
		t.Fatalf("alerts = %d, want 1", n)
	}

	// It has to point at the product, or tapping it goes nowhere useful.
	var subject string
	var subjectID *uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT subject, subject_id FROM notification
			WHERE company_id = $1 AND kind = $2`,
			f.companyID, notify.KindLowStock).Scan(&subject, &subjectID)
	}); err != nil {
		t.Fatalf("read the alert: %v", err)
	}
	if subject != "variant" || subjectID == nil || *subjectID != f.variantID {
		t.Errorf("the alert points at %s/%v, want variant/%s",
			subject, subjectID, f.variantID)
	}
}

// A well-stocked variant raises nothing.
//
// The half that matters most: an alert engine that fired on everything would be
// indistinguishable from one that fired on nothing, because both get ignored.
func TestAWellStockedVariantRaisesNoAlert(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	setReorderLevel(t, h, f, "2") // the fixture holds 10

	runSweep(t, h, f)

	if n := lowStockNotices(t, h, f); n != 0 {
		t.Errorf("a variant with 10 on hand against a level of 2 raised %d alerts", n)
	}
}

// A variant with no threshold set is never low.
//
// Most shops will never fill the field in. A null reorder level means "not
// set", and treating it as zero would alert on the entire catalogue.
func TestAVariantWithNoReorderLevelIsNeverLow(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	// Deliberately not setting one.

	runSweep(t, h, f)

	if n := lowStockNotices(t, h, f); n != 0 {
		t.Errorf("a variant with no reorder level raised %d alerts", n)
	}
}

// A variant that has run out is not reported as low.
//
// Out of stock is a different fact needing a different message, and the
// dashboard draws the same line with `qty > 0`. Two places disagreeing about
// what "low" means would be worse than either answer.
func TestAnEmptyVariantIsNotReportedAsMerelyLow(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	setReorderLevel(t, h, f, "10")

	// Sell the lot. on-hand is the SUM OF MOVEMENTS -- stock_on_hand() reads
	// stock_movement, not the valuation table -- so emptying the shelf means
	// booking a movement that takes it to zero, which is also how a real sale
	// would do it.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		var onHand string
		if e := tx.QueryRow(t.Context(),
			`SELECT coalesce(sum(delta),0)::text FROM stock_movement
			 WHERE variant_id = $1`, f.variantID).Scan(&onHand); e != nil {
			return e
		}
		_, e := tx.Exec(t.Context(), `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta,
			   reason, note, occurred_at)
			VALUES ($1,$2,$3,$4, -($5::numeric), 'adjustment', 'emptied by test', now())`,
			f.tenantID, f.companyID, f.variantID, f.warehouseID, onHand)
		return e
	}); err != nil {
		t.Fatalf("empty the stock: %v", err)
	}

	runSweep(t, h, f)

	if n := lowStockNotices(t, h, f); n != 0 {
		t.Errorf("an empty variant raised %d low-stock alerts", n)
	}
}

// One tenant's sweep does not reach another's stock.
func TestALowStockSweepStaysInsideItsTenant(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	setReorderLevel(t, h, mine, "10")
	setReorderLevel(t, h, theirs, "10")

	runSweep(t, h, mine)

	if n := lowStockNotices(t, h, mine); n != 1 {
		t.Errorf("own alerts = %d, want 1", n)
	}
	if n := lowStockNotices(t, h, theirs); n != 0 {
		t.Errorf("one tenant's sweep raised %d alerts in another tenant", n)
	}
}
