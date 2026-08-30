//go:build integration

// Concurrency, against the real database.
//
// A POS is concurrent by nature: several tills in one shop, several shops in
// one company, and a sync worker replaying a day of offline sales while all of
// them are trading. Every guarantee elsewhere in this suite is proved with one
// caller at a time, which is exactly the condition under which a lost update
// never appears.
//
// These tests run the same operation from many goroutines at once and assert
// what must still be true afterwards.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// concurrently runs fn n times at once and returns every error, nil or not.
//
// A barrier rather than a plain loop: goroutines started in sequence tend to
// finish in sequence, and a race that needs two callers inside the same window
// would simply not happen.
func concurrently(n int, fn func(i int) error) []error {
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(n)

	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			start.Wait()
			errs[i] = fn(i)
		}(i)
	}
	start.Done()
	done.Wait()
	return errs
}

// Selling the same item from several tills at once must consume stock exactly
// once per sale.
//
// This is the classic lost update. Under weighted average the pool is read,
// a new value computed, and the result written back — so two callers that read
// the same figure both write the same answer, and one sale's stock movement
// simply vanishes from the valuation while its journal entry survives.
func TestConcurrentSalesOfTheSameItemDoNotLoseStock(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier") // 10 on hand, valued 600

	const tills = 6
	errs := concurrently(tills, func(i int) error {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
		defer resp.Body.Close()
		if resp.StatusCode != 201 {
			return errFromStatus(resp.StatusCode)
		}
		return nil
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("till %d could not sell: %v", i, err)
		}
	}

	ctx := t.Context()
	var onHand, valuation, tieOut decimal.Decimal
	var invoices int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `SELECT stock_on_hand($1,$2)`,
			f.variantID, f.warehouseID).Scan(&onHand); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `SELECT inventory_valuation($1)`,
			f.companyID).Scan(&valuation); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `SELECT inventory_gl_difference($1)`,
			f.companyID).Scan(&tieOut); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM sales_invoice`).Scan(&invoices)
	}); err != nil {
		t.Fatalf("read state: %v", err)
	}

	if invoices != tills {
		t.Fatalf("%d invoices for %d sales", invoices, tills)
	}
	if !onHand.Equal(decimal.NewFromInt(10 - tills)) {
		t.Errorf("stock on hand = %s after %d sales of one unit, want %d",
			onHand, tills, 10-tills)
	}
	// The valuation is the figure a lost update corrupts: the movements are
	// separate rows and always add up, while the pool is a single row that a
	// blind write can clobber.
	if !valuation.Equal(decimal.NewFromInt(int64(60 * (10 - tills)))) {
		t.Errorf("valuation = %s after %d sales, want %d; a concurrent sale's "+
			"stock was lost", valuation, tills, 60*(10-tills))
	}
	if !tieOut.IsZero() {
		t.Errorf("the stock valuation and the Inventory account are out by %s "+
			"after concurrent selling", tieOut)
	}
}

// The last unit can be sold once. Six tills racing for one item must produce
// one sale and five refusals, not six sales and negative stock.
func TestConcurrentSalesCannotOversellTheLastUnit(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	// Reduce the shelf to a single unit, ledger included.
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `
			INSERT INTO stock_movement
			  (tenant_id, company_id, variant_id, warehouse_id, delta, reason, note, value_delta)
			VALUES ($1,$2,$3,$4,-9,'adjustment','reduced for a concurrency test',-540)`,
			f.tenantID, f.companyID, f.variantID, f.warehouseID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			UPDATE stock_valuation SET qty_on_hand = 1, total_value = 60
			WHERE variant_id = $1 AND warehouse_id = $2`,
			f.variantID, f.warehouseID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `
			UPDATE cost_layer SET qty_remaining = 1
			WHERE variant_id = $1 AND warehouse_id = $2`,
			f.variantID, f.warehouseID)
		return e
	}); err != nil {
		t.Fatalf("reduce stock: %v", err)
	}

	// The company blocks selling below zero, which is the policy a shop uses
	// for anything it cannot afford to promise twice. It is read from the
	// company and cannot be named by the request.
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE company SET negative_stock_policy = 'block' WHERE id = $1`,
			f.companyID)
		return e
	}); err != nil {
		t.Fatalf("set the policy: %v", err)
	}

	const tills = 6
	statuses := make([]int, tills)
	concurrently(tills, func(i int) error {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
		defer resp.Body.Close()
		statuses[i] = resp.StatusCode
		return nil
	})

	var onHand decimal.Decimal
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT stock_on_hand($1,$2)`,
			f.variantID, f.warehouseID).Scan(&onHand)
	}); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	if onHand.IsNegative() {
		t.Fatalf("stock on hand is %s: the shop sold goods it did not have", onHand)
	}

	sold := 0
	for _, s := range statuses {
		if s == 201 {
			sold++
		}
	}
	t.Logf("%d of %d tills sold the last unit; stock left %s", sold, tills, onHand)
	if sold != 1 {
		t.Errorf("%d tills sold the last unit, want exactly 1", sold)
	}
	if !onHand.IsZero() {
		t.Errorf("stock on hand = %s after the last unit was sold, want 0", onHand)
	}
}

// One EGS unit, many tills: the ZATCA counter must issue every number exactly
// once with no gaps. A duplicate or a gap is what tamper detection looks for.
func TestConcurrentSalesTakeDistinctChainPositions(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	const tills = 8
	icvs := make([]int64, tills)
	errs := concurrently(tills, func(i int) error {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
		defer resp.Body.Close()
		if resp.StatusCode != 201 {
			return errFromStatus(resp.StatusCode)
		}
		body := decodeJSONFrom(t, resp)
		chain := body["zatca"].(map[string]any)
		icvs[i] = int64(chain["icv"].(float64))
		return nil
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("till %d: %v", i, err)
		}
	}

	seen := map[int64]bool{}
	for _, icv := range icvs {
		if seen[icv] {
			t.Fatalf("counter %d was issued twice; the chain has a duplicate", icv)
		}
		seen[icv] = true
	}
	for want := int64(1); want <= tills; want++ {
		if !seen[want] {
			t.Errorf("counter %d was never issued; the chain has a gap", want)
		}
	}
}

// A request must not hold two database connections at once.
//
// This is a deadlock test wearing the clothes of a throughput test. Ringing up
// a sale opens a transaction, which holds one connection for as long as the
// sale takes. If anything inside that transaction goes back to the pool for a
// SECOND connection — resolving the VAT rate from the regulatory registry was
// the real case — then with the pool exhausted by sales in flight, every one of
// them waits for a connection that only another of them could release.
//
// Nothing recovers from that. Acquiring from the pool has no deadline, so there
// is no error, no timeout and no log line: every till in the shop stops at
// once, mid-sale, and stays stopped. The first symptom is a phone call.
//
// The shape of the test is the shape of the bug. It fills the pool exactly —
// one sale per available connection, all starting together, each holding its
// connection until it commits — so a second acquisition anywhere in the path
// has nothing left to take. Under the fix it finishes in well under a second;
// before it, it hung until the suite's own timeout killed it.
//
// It is deliberately indifferent to what a sale MEANS. Chain positions, stock
// and invoice numbers are asserted by the tests around it; this one asserts
// only that the requests come back at all.
func TestASaleDoesNotHoldTwoConnections(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// The registry cache is what usually hides this: a warm cache answers
	// without touching the database, so the second connection is never asked
	// for. Cold is the honest state — it is what a fresh deployment, a registry
	// write, or the first sale of a new month all produce, and each of those
	// arrives at every till simultaneously.
	h.rules.Invalidate()

	errs := concurrently(harnessPoolConns, func(i int) error {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
		defer resp.Body.Close()
		if resp.StatusCode != 201 {
			return errFromStatus(resp.StatusCode)
		}
		return nil
	})

	for i, err := range errs {
		if err != nil {
			t.Errorf("till %d: %v", i, err)
		}
	}
}

// The same sale arriving from several retries at once must be rung up once.
// Sync delivers at least once and a flaky connection retries in parallel.
func TestTheSameSaleArrivingConcurrentlyIsRungOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	body := oneItemSale(f, newUUID(), "1", "115.00", "115.00")

	const retries = 6
	created := 0
	var mu sync.Mutex
	concurrently(retries, func(i int) error {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, body)
		defer resp.Body.Close()
		mu.Lock()
		if resp.StatusCode == 201 {
			created++
		}
		mu.Unlock()
		return nil
	})

	ctx := t.Context()
	var invoices, movements int
	var onHand decimal.Decimal
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM sales_invoice`).
			Scan(&invoices); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM stock_movement WHERE reason='sale'`).
			Scan(&movements); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `SELECT stock_on_hand($1,$2)`,
			f.variantID, f.warehouseID).Scan(&onHand)
	}); err != nil {
		t.Fatalf("read state: %v", err)
	}

	if invoices != 1 {
		t.Errorf("%d invoices for one sale retried %d times; the customer was "+
			"charged more than once", invoices, retries)
	}
	if movements != 1 {
		t.Errorf("%d stock movements for one sale", movements)
	}
	if !onHand.Equal(decimal.NewFromInt(9)) {
		t.Errorf("stock on hand = %s, want 9", onHand)
	}
}

// Invoice numbers are claimed per store per year and must be unique and
// gapless under contention, for the same reason journal entry numbers are.
func TestConcurrentSalesGetDistinctInvoiceNumbers(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	const tills = 6
	concurrently(tills, func(i int) error {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
		defer resp.Body.Close()
		return nil
	})

	ctx := t.Context()
	numbers := map[string]bool{}
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx,
			`SELECT human_number FROM sales_invoice WHERE human_number IS NOT NULL`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var n string
			if e := rows.Scan(&n); e != nil {
				return e
			}
			if numbers[n] {
				t.Errorf("invoice number %q was issued twice", n)
			}
			numbers[n] = true
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("read numbers: %v", err)
	}

	if len(numbers) != tills {
		t.Errorf("%d distinct invoice numbers for %d sales", len(numbers), tills)
	}
}

// Two cashiers opening a session on one till at the same instant: exactly one
// wins. Two open drawers would make "what is in this till" have two answers.
func TestConcurrentSessionOpensProduceOne(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	// The fixture already opened one; close it so the race is a real one.
	if _, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
		dec("200.00"), "closing to race the reopen"); err != nil {
		t.Fatalf("close: %v", err)
	}

	const cashiers = 5
	opened := 0
	var mu sync.Mutex
	concurrently(cashiers, func(i int) error {
		_, err := h.shift.Open(ctx, f.tenantID, f.deviceID, f.userID,
			dec("100.00"), true)
		if err == nil {
			mu.Lock()
			opened++
			mu.Unlock()
		}
		return nil
	})

	var open int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM cash_session WHERE device_id=$1 AND state='open'`,
			f.deviceID).Scan(&open)
	}); err != nil {
		t.Fatalf("count sessions: %v", err)
	}

	if open != 1 {
		t.Fatalf("%d open sessions on one till; the drawer has %d answers", open, open)
	}
	if opened != 1 {
		t.Errorf("%d cashiers were told they opened a session, want 1", opened)
	}
}

// Two Z reports on one session at the same instant: one closes it, the other
// is refused. Both succeeding would overwrite a count someone signed for.
func TestConcurrentZReportsCloseOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	const attempts = 5
	closed := 0
	var mu sync.Mutex
	concurrently(attempts, func(i int) error {
		_, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
			dec("200.00"), "racing")
		if err == nil {
			mu.Lock()
			closed++
			mu.Unlock()
		}
		return nil
	})

	if closed != 1 {
		t.Errorf("%d Z reports succeeded on one session, want 1", closed)
	}
}

// errFromStatus turns an unexpected status into an error a test can report.
func errFromStatus(code int) error {
	return fmt.Errorf("unexpected status %d", code)
}

// decodeJSONFrom reads a body without the deferred close decodeJSON does, so a
// caller can close it itself.
func decodeJSONFrom(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}
