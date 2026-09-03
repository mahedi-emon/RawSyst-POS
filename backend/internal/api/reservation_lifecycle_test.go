//go:build integration

// The rest of B13's stock reservation ledger.
//
// `TestReservedStockCannotBeSoldTwice` proved a hold takes effect. The other
// half of the ledger — releasing, consuming, expiring, and holding under
// concurrency — had nothing behind it, and one of those turned out to be
// missing outright: `ExpireHolds` was written, correct, and called by nothing.
// No route, no job, no schedule. `stock_reservation.expires_at` was recorded
// and the deadline never arrived, so an abandoned unpaid basket held the shop's
// last unit for ever, through every channel, with nothing saying why.
package api

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/aftersales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
)

// hold puts stock aside for an order, optionally with a deadline in minutes.
func hold(
	t *testing.T, h *harness, f *shopFixture, orderID, qty string, expiresIn int,
) *http.Response {
	t.Helper()
	body := map[string]any{
		"order_id": orderID, "variant_id": f.variantID.String(),
		"warehouse_id": f.warehouseID.String(), "qty": qty,
	}
	if expiresIn != 0 {
		body["expires_in_minutes"] = expiresIn
	}
	return h.do(t, http.MethodPost,
		"/api/v1/stock/reservations?company_id="+f.companyID.String(),
		f.token, body)
}

// freeToSell is what a channel may sell right now.
func freeToSell(t *testing.T, h *harness, f *shopFixture) string {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		"/api/v1/stock/availability?company_id="+f.companyID.String()+
			"&variant_id="+f.variantID.String()+
			"&warehouse_id="+f.warehouseID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("availability: %d %s", resp.StatusCode, readBody(t, resp))
	}
	got, _ := decodeJSON(t, resp)["available_to_sell"].(string)
	return got
}

// Releasing a hold puts the stock back on sale.
func TestReleasingAHoldPutsTheStockBackOnSale(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner") // 10 on hand
	order := h.newOrder(t, f)

	hold(t, h, f, order, "10", 0).Body.Close()
	if got := freeToSell(t, h, f); got != "0" {
		t.Fatalf("free to sell is %s after holding everything, want 0", got)
	}

	released := h.do(t, http.MethodDelete,
		"/api/v1/stock/reservations/"+order+"?company_id="+f.companyID.String(),
		f.token, nil)
	released.Body.Close()
	if released.StatusCode >= 300 {
		t.Fatalf("release: %d", released.StatusCode)
	}

	if got := freeToSell(t, h, f); got != "10" {
		t.Errorf("free to sell is %s after releasing, want 10 back", got)
	}
}

// A lapsed hold is let go by the sweep.
//
// The defect this closes. `expires_at` was recorded and nothing ever acted on
// it, so a basket abandoned at midnight held the last unit until somebody
// noticed by hand — which, for a hold nobody can see, means never.
func TestALapsedHoldIsReleasedBySweep(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	order := h.newOrder(t, f)

	// A hold that expired an hour ago. Written directly because the ledger is
	// append-only — `stock_reservation` refuses UPDATE and DELETE by trigger,
	// which is right for a record of who held what and when — and because the
	// route only accepts a deadline in the future.
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			INSERT INTO stock_reservation
			  (tenant_id, company_id, variant_id, warehouse_id, qty, reason,
			   order_id, expires_at)
			VALUES ($1,$2,$3,$4,10,'held',$5, now() - interval '1 hour')`,
			f.tenantID, f.companyID, f.variantID, f.warehouseID, order)
		return e
	}); err != nil {
		t.Fatalf("seed a lapsed hold: %v", err)
	}

	if got := freeToSell(t, h, f); got != "0" {
		t.Fatalf("free to sell is %s before the sweep, want 0", got)
	}

	runReservationSweep(t, h, f)

	if got := freeToSell(t, h, f); got != "10" {
		t.Errorf("free to sell is %s after the sweep, want 10 — an abandoned "+
			"basket is still holding the shop's stock", got)
	}
}

// A hold that has not lapsed survives the sweep.
//
// The other half, so a sweep that released everything would not pass the test
// above by being broken.
func TestAnUnexpiredHoldSurvivesTheSweep(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	order := h.newOrder(t, f)

	hold(t, h, f, order, "10", 60).Body.Close() // an hour from now
	runReservationSweep(t, h, f)

	if got := freeToSell(t, h, f); got != "0" {
		t.Errorf("free to sell is %s, want 0 — the sweep let go of a hold "+
			"that had not expired", got)
	}
}

// A hold with no deadline is never swept.
//
// Null means "held until the order resolves", which is what a paid order's
// hold is. A sweep that let those go would release stock the customer has
// already paid for.
func TestAHoldWithNoDeadlineIsNeverSwept(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	order := h.newOrder(t, f)

	hold(t, h, f, order, "10", 0).Body.Close() // no expiry
	runReservationSweep(t, h, f)

	if got := freeToSell(t, h, f); got != "0" {
		t.Errorf("free to sell is %s, want 0 — a hold with no deadline was "+
			"swept away", got)
	}
}

// Concurrent holds cannot oversell.
//
// `Reserve` takes a transaction-scoped advisory lock on the variant and
// warehouse before reading availability, because reading then writing is a
// check-then-act and the row it would otherwise lock does not exist before the
// first reservation. Eight channels asking for all ten units must yield one.
func TestConcurrentHoldsCannotOversell(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	orders := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		orders = append(orders, h.newOrder(t, f))
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	held := 0
	for _, order := range orders {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			resp := hold(t, h, f, id, "10", 0)
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent ||
				resp.StatusCode == http.StatusOK {
				mu.Lock()
				held++
				mu.Unlock()
			}
		}(order)
	}
	wg.Wait()

	if held != 1 {
		t.Errorf("%d of 8 channels each held all 10 units, want exactly 1",
			held)
	}
	if got := freeToSell(t, h, f); got != "0" {
		t.Errorf("free to sell is %s after the race, want 0", got)
	}
}

// One tenant's holds do not consume another's stock.
func TestHoldsDoNotCrossTenants(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	order := h.newOrder(t, mine)
	hold(t, h, mine, order, "10", 0).Body.Close()

	if got := freeToSell(t, h, theirs); got != "10" {
		t.Errorf("the other tenant has %s free to sell, want 10 — one "+
			"tenant's hold reached another's shelf", got)
	}
}

// Holding stock takes the order permission.
func TestHoldingStockNeedsItsPermission(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	order := h.newOrder(t, f)

	// The same shop, seen by somebody who may sell but not commit stock to an
	// order.
	cashier := *f
	cashier.token = h.seedUserInTenant(t, f.tenantID, "cashier")

	resp := hold(t, h, &cashier, order, "1", 0)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		t.Error("a cashier reserved stock against an order")
	}
}

// runReservationSweep drives the handler directly, as the worker would.
func runReservationSweep(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	tenant := f.tenantID
	sweeper := jobs.NewReservationExpirySweeper(h.pool,
		aftersales.NewService(h.pool))
	if err := sweeper.Run(context.Background(), jobs.Job{
		TenantID: &tenant, Kind: jobs.KindReservationExpirySweep,
	}); err != nil {
		t.Fatalf("reservation expiry sweep: %v", err)
	}
}
