//go:build integration

// Replaying an offline queue.
//
// A terminal that traded for a day with no network pushes everything it
// recorded. What must be true afterwards is exactly what would have been true
// had every sale gone through online: the same invoices, the same chain, the
// same stock, the same books — and nothing twice.
//
// These tests drive the real HTTP endpoint against the real engine and the real
// sale service. Nothing is stubbed, because the thing worth proving is that
// there is only ONE sale implementation and the offline path uses it.
package api

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
)

// offlineItem is one queued sale as a terminal records it.
func offlineItem(f *shopFixture, seq int64, invoiceUUID uuid.UUID, qty, price, paid string) map[string]any {
	return map[string]any{
		"seq":         seq,
		"entity_uuid": invoiceUUID.String(),
		"entity_type": "sales_invoice",
		"payload": map[string]any{
			"invoice_uuid": invoiceUUID.String(),
			"doc_type":     "simplified",
			"issued_at":    fmt.Sprintf("2026-08-%02dT09:%02d:00Z", 10+seq%5, seq%60),
			"cashier_id":   f.userID.String(),
			"lines": []map[string]any{{
				"variant_id":    f.variantID.String(),
				"description":   "Abaya",
				"qty":           qty,
				"unit_price":    price,
				"tax_treatment": "standard",
			}},
			"tenders": []map[string]any{{"method": "cash", "amount": paid}},
		},
	}
}

func (h *harness) push(t *testing.T, f *shopFixture, key string, items ...map[string]any) map[string]any {
	t.Helper()
	resp := h.do(t, "POST", "/api/v1/sync/push", f.token, map[string]any{
		"idempotency_key": key,
		"items":           items,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("push: %d %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

// A day's offline trading lands as real invoices, with real chain positions and
// real books.
func TestAnOfflineQueueReplaysIntoRealSales(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	items := []map[string]any{}
	for seq := int64(1); seq <= 4; seq++ {
		items = append(items, offlineItem(f, seq, newUUID(), "1", "115.00", "115.00"))
	}

	out := h.push(t, f, "batch-1", items...)

	if applied := out["applied"].(float64); applied != 4 {
		t.Fatalf("applied = %v, want 4: %v", applied, out)
	}
	if out["cursor"].(float64) != 4 {
		t.Errorf("cursor = %v, want 4", out["cursor"])
	}

	ctx := t.Context()
	var invoices, entries int
	var onHand, tbDiff, stockDiff decimal.Decimal
	var maxICV int64
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM sales_invoice`).
			Scan(&invoices); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM journal_entry WHERE source_type='sales_invoice'`).
			Scan(&entries); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `SELECT stock_on_hand($1,$2)`,
			f.variantID, f.warehouseID).Scan(&onHand); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `SELECT coalesce(max(icv),0) FROM zatca_invoice`).
			Scan(&maxICV); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `SELECT trial_balance_difference($1)`,
			f.companyID).Scan(&tbDiff); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `SELECT inventory_gl_difference($1)`,
			f.companyID).Scan(&stockDiff)
	}); err != nil {
		t.Fatalf("read state: %v", err)
	}

	if invoices != 4 {
		t.Errorf("%d invoices from 4 offline sales", invoices)
	}
	// Two entries per sale — revenue and cost of sale — which is what an online
	// sale produces. Anything else means the replay took a different path.
	if entries != 8 {
		t.Errorf("%d journal entries, want 8 (revenue and COGS for each sale)", entries)
	}
	if !onHand.Equal(decimal.NewFromInt(6)) {
		t.Errorf("stock on hand = %s, want 6", onHand)
	}
	if maxICV != 4 {
		t.Errorf("highest ICV = %d, want 4", maxICV)
	}
	if !tbDiff.IsZero() {
		t.Errorf("trial balance is out by %s after a replay", tbDiff)
	}
	if !stockDiff.IsZero() {
		t.Errorf("stock valuation and the Inventory account are out by %s", stockDiff)
	}
}

// The whole point of Pillar 3: a retried batch must change nothing.
func TestReplayingTheSameBatchTwiceChangesNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	items := []map[string]any{
		offlineItem(f, 1, newUUID(), "1", "115.00", "115.00"),
		offlineItem(f, 2, newUUID(), "2", "115.00", "230.00"),
	}

	first := h.push(t, f, "same-key", items...)
	second := h.push(t, f, "same-key", items...)

	if first["applied"].(float64) != 2 {
		t.Fatalf("first push applied %v, want 2", first["applied"])
	}
	// Recognised at the BATCH level: the same idempotency key returns the
	// original verdicts rather than reprocessing.
	if second["replayed"] != true {
		t.Errorf("the retried batch was not recognised as a replay: %v", second)
	}
	if second["applied"].(float64) != 2 {
		t.Errorf("the replay reported %v applied, want the original 2",
			second["applied"])
	}

	// Two sales of 1 and 2 units: 3 off a shelf of 10.
	assertOneSaleEach(t, h, f, 2, 7)
}

// A retry with a DIFFERENT batch key still must not duplicate the sales. The
// batch key is transport bookkeeping; the invoice UUID is the durable identity,
// and it is what actually stops a customer being charged twice.
func TestTheSameSalesUnderANewBatchKeyAreRecognised(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	items := []map[string]any{
		offlineItem(f, 1, newUUID(), "1", "115.00", "115.00"),
		offlineItem(f, 2, newUUID(), "1", "115.00", "115.00"),
	}

	h.push(t, f, "key-a", items...)
	second := h.push(t, f, "key-b", items...)

	if second["applied"].(float64) != 0 {
		t.Errorf("%v sales were applied a second time under a new batch key",
			second["applied"])
	}
	if second["duplicates"].(float64) != 2 {
		t.Errorf("duplicates = %v, want 2", second["duplicates"])
	}

	assertOneSaleEach(t, h, f, 2, 8)
}

// assertOneSaleEach checks nothing was applied twice.
func assertOneSaleEach(t *testing.T, h *harness, f *shopFixture, sales int, wantOnHand int64) {
	t.Helper()
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

	if invoices != sales {
		t.Errorf("%d invoices, want %d; a sale was applied twice", invoices, sales)
	}
	if movements != sales {
		t.Errorf("%d stock movements, want %d", movements, sales)
	}
	if !onHand.Equal(decimal.NewFromInt(wantOnHand)) {
		t.Errorf("stock on hand = %s, want %d", onHand, wantOnHand)
	}
}

// One bad sale must not let the ones behind it jump the queue.
//
// Each terminal keeps its own ICV chain and a chain is only meaningful in
// order. Applying sale 4 while 2 is stuck would leave a gap, which is precisely
// the signal ZATCA tamper detection looks for.
func TestAFailedSaleBlocksTheRestOfThatTerminalsQueue(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	bad := offlineItem(f, 2, newUUID(), "1", "115.00", "115.00")
	// Underpaid by a hallala: the finalizer refuses it, as it would online.
	bad["payload"].(map[string]any)["tenders"] =
		[]map[string]any{{"method": "cash", "amount": "114.99"}}

	out := h.push(t, f,
		"batch-with-a-bad-one",
		offlineItem(f, 1, newUUID(), "1", "115.00", "115.00"),
		bad,
		offlineItem(f, 3, newUUID(), "1", "115.00", "115.00"),
		offlineItem(f, 4, newUUID(), "1", "115.00", "115.00"),
	)

	if out["applied"].(float64) != 1 {
		t.Errorf("applied = %v, want 1 (only the sale before the failure)",
			out["applied"])
	}
	if out["failed"].(float64) != 1 {
		t.Errorf("failed = %v, want 1", out["failed"])
	}
	if out["blocked"].(float64) != 2 {
		t.Errorf("blocked = %v, want 2; the sales behind the failure jumped the "+
			"queue and left a gap in the chain", out["blocked"])
	}
	// The cursor may not advance past the failure, or the device would discard
	// sales the server never accepted.
	if out["cursor"].(float64) != 1 {
		t.Errorf("cursor = %v, want 1", out["cursor"])
	}

	ctx := t.Context()
	var maxICV int64
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT coalesce(max(icv),0) FROM zatca_invoice`).
			Scan(&maxICV)
	}); err != nil {
		t.Fatalf("read chain: %v", err)
	}
	if maxICV != 1 {
		t.Errorf("the chain reached %d; only the first sale should have taken a "+
			"position", maxICV)
	}
}

// Once the bad sale is corrected the queue drains, and the chain is unbroken.
func TestTheQueueRecoversOnceTheBadSaleIsFixed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	badUUID := newUUID()
	bad := offlineItem(f, 2, badUUID, "1", "115.00", "115.00")
	bad["payload"].(map[string]any)["tenders"] =
		[]map[string]any{{"method": "cash", "amount": "114.99"}}

	h.push(t, f, "attempt-1",
		offlineItem(f, 1, newUUID(), "1", "115.00", "115.00"),
		bad,
		offlineItem(f, 3, newUUID(), "1", "115.00", "115.00"),
	)

	// The terminal corrects the payment and pushes the rest again. Sale 1 is
	// already in and comes back as a duplicate rather than an error.
	fixed := offlineItem(f, 2, badUUID, "1", "115.00", "115.00")
	out := h.push(t, f, "attempt-2", fixed,
		offlineItem(f, 3, newUUID(), "1", "115.00", "115.00"))

	if out["applied"].(float64) != 2 {
		t.Errorf("applied = %v on recovery, want 2: %v", out["applied"], out)
	}
	if out["cursor"].(float64) != 3 {
		t.Errorf("cursor = %v after recovery, want 3", out["cursor"])
	}

	ctx := t.Context()
	var breaks int
	var maxICV int64
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var egsUnitID uuid.UUID
		if e := tx.QueryRow(ctx, `SELECT id FROM egs_unit LIMIT 1`).
			Scan(&egsUnitID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM zatca_chain_breaks($1)`, egsUnitID).
			Scan(&breaks); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `SELECT coalesce(max(icv),0) FROM zatca_invoice`).
			Scan(&maxICV)
	}); err != nil {
		t.Fatalf("verify chain: %v", err)
	}

	if breaks != 0 {
		t.Errorf("%d breaks in the chain after a failure and recovery", breaks)
	}
	if maxICV != 3 {
		t.Errorf("chain reached %d, want 3", maxICV)
	}
}

// Ordering is per TERMINAL, not global. One till's stuck queue must not stop
// another till's takings from landing — they are separate chains and separate
// drawers.
func TestOneTerminalsFailureDoesNotBlockAnother(t *testing.T) {
	h := newHarness(t)
	first := h.seedShop(t, "cashier")
	second := h.seedShop(t, "cashier")

	broken := offlineItem(first, 1, newUUID(), "1", "115.00", "115.00")
	broken["payload"].(map[string]any)["tenders"] =
		[]map[string]any{{"method": "cash", "amount": "1.00"}}

	stuck := h.push(t, first, "till-1-batch", broken,
		offlineItem(first, 2, newUUID(), "1", "115.00", "115.00"))

	if stuck["applied"].(float64) != 0 || stuck["blocked"].(float64) != 1 {
		t.Fatalf("the first till did not stall as expected: %v", stuck)
	}

	// The second till, a different tenant and a different chain, is unaffected.
	fine := h.push(t, second, "till-2-batch",
		offlineItem(second, 1, newUUID(), "1", "115.00", "115.00"),
		offlineItem(second, 2, newUUID(), "1", "115.00", "115.00"))

	if fine["applied"].(float64) != 2 {
		t.Errorf("the second till applied %v of its 2 sales while the first was "+
			"stalled", fine["applied"])
	}
	if fine["cursor"].(float64) != 2 {
		t.Errorf("the second till's cursor is %v, want 2", fine["cursor"])
	}
}

// Several retries arriving at once must still produce one set of sales. A
// flaky connection retries in parallel, not politely in sequence.
func TestConcurrentPushesOfTheSameBatchApplyOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	items := []map[string]any{
		offlineItem(f, 1, newUUID(), "1", "115.00", "115.00"),
		offlineItem(f, 2, newUUID(), "1", "115.00", "115.00"),
		offlineItem(f, 3, newUUID(), "1", "115.00", "115.00"),
	}

	const retries = 5
	var mu sync.Mutex
	statuses := map[int]int{}
	concurrently(retries, func(i int) error {
		resp := h.do(t, "POST", "/api/v1/sync/push", f.token, map[string]any{
			"idempotency_key": "racing-key",
			"items":           items,
		})
		defer resp.Body.Close()
		mu.Lock()
		statuses[resp.StatusCode]++
		mu.Unlock()
		return nil
	})

	t.Logf("statuses across %d concurrent pushes: %v", retries, statuses)
	assertOneSaleEach(t, h, f, 3, 7)

	ctx := t.Context()
	var entries int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM journal_entry WHERE source_type='sales_invoice'`).
			Scan(&entries)
	}); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if entries != 6 {
		t.Errorf("%d journal entries for 3 sales, want 6; a concurrent retry "+
			"double-posted", entries)
	}
}

// The payload's invoice id and the queue's must agree, or the engine
// deduplicates on one and the finalizer on the other — and a sale could be
// applied twice while every layer believed it had checked.
func TestAMismatchedInvoiceIdentifierIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	item := offlineItem(f, 1, newUUID(), "1", "115.00", "115.00")
	item["payload"].(map[string]any)["invoice_uuid"] = newUUID().String()

	out := h.push(t, f, "mismatch", item)
	if out["failed"].(float64) != 1 {
		t.Fatalf("a sale whose identifiers disagree was accepted: %v", out)
	}
	if !strings.Contains(fmt.Sprint(out["items"]), "does not match") {
		t.Errorf("the refusal does not explain the mismatch: %v", out["items"])
	}
}

// A sale attributed to somebody outside this tenant is refused. A device
// offline for a week is the least trustworthy input the system takes.
func TestASaleAttributedToAnotherTenantsUserIsRefused(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")

	item := offlineItem(mine, 1, newUUID(), "1", "115.00", "115.00")
	item["payload"].(map[string]any)["cashier_id"] = theirs.userID.String()

	out := h.push(t, mine, "foreign-cashier", item)
	if out["failed"].(float64) != 1 {
		t.Fatalf("a sale naming another tenant's user was applied: %v", out)
	}

	var invoices int
	if err := h.pool.TxAsTenant(t.Context(), mine.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM sales_invoice`).Scan(&invoices)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if invoices != 0 {
		t.Errorf("%d invoices survived", invoices)
	}
}

// A push from a session with no terminal behind it is refused: there is no
// chain to write to and no drawer to attribute takings against.
func TestAPushWithoutATerminalIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	token, _, err := h.tokens.IssueAccess(actorWithoutDevice(f))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	resp := h.do(t, "POST", "/api/v1/sync/push", token, map[string]any{
		"idempotency_key": "no-device",
		"items":           []map[string]any{offlineItem(f, 1, newUUID(), "1", "115.00", "115.00")},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// A terminal can ask what it still has outstanding. A cashier closing a till
// needs to know whether the day's takings reached the server.
func TestATerminalCanSeeWhatIsOutstanding(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	broken := offlineItem(f, 1, newUUID(), "1", "115.00", "115.00")
	broken["payload"].(map[string]any)["tenders"] =
		[]map[string]any{{"method": "cash", "amount": "1.00"}}
	h.push(t, f, "health-batch", broken,
		offlineItem(f, 2, newUUID(), "1", "115.00", "115.00"))

	resp := h.do(t, "GET", "/api/v1/sync/health", f.token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("health: %d %s", resp.StatusCode, readBody(t, resp))
	}
	health := decodeJSON(t, resp)

	if health["failed"].(float64) != 1 {
		t.Errorf("failed = %v, want 1", health["failed"])
	}
	if health["blocked"].(float64) != 1 {
		t.Errorf("blocked = %v, want 1", health["blocked"])
	}
}

// A batch with no idempotency key is refused: a retry would be
// indistinguishable from a second push and the device would be told its
// takings landed twice.
func TestAPushWithoutAnIdempotencyKeyIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "POST", "/api/v1/sync/push", f.token, map[string]any{
		"items": []map[string]any{offlineItem(f, 1, newUUID(), "1", "115.00", "115.00")},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// actorWithoutDevice is the same person on a browser session: same tenant,
// same permissions, no terminal behind them.
func actorWithoutDevice(f *shopFixture) actor.Actor {
	return actor.Actor{UserID: f.userID, TenantID: f.tenantID}
}
