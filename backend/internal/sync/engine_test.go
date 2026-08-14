//go:build integration

// QA gate M3, mechanised: "Sell 500 invoices with internet fully disconnected.
// Reconnect. Verify zero duplicates, zero lost invoices, correct hash chain
// order, correct ZATCA submission."
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// --- a terminal that sells offline ------------------------------------

type offlineTerminal struct {
	deviceID uuid.UUID
	seq      int64
	icv      int64
	prevHash string
	queue    []Item
}

func newOfflineTerminal(deviceID uuid.UUID) *offlineTerminal {
	return &offlineTerminal{deviceID: deviceID, prevHash: zatca.GenesisPIH}
}

type invoicePayload struct {
	ICV      int64  `json:"icv"`
	PIH      string `json:"pih"`
	Hash     string `json:"hash"`
	Total    string `json:"total"`
	IssuedAt string `json:"issued_at"`
}

// sell rings up a sale with no connectivity: assign a UUID, take the next ICV,
// chain to the previous hash, sign, print, queue.
func (t *offlineTerminal) sell(total string) Item {
	t.seq++
	t.icv++

	entityUUID := uuid.New()
	hash := fmt.Sprintf("hash-%d-%s", t.icv, entityUUID.String()[:8])

	payload, _ := json.Marshal(invoicePayload{
		ICV: t.icv, PIH: t.prevHash, Hash: hash, Total: total,
		IssuedAt: time.Now().UTC().Format(time.RFC3339),
	})
	t.prevHash = hash

	item := Item{
		Seq: t.seq, EntityUUID: entityUUID,
		EntityType: "sales_invoice", Payload: payload,
	}
	t.queue = append(t.queue, item)
	return item
}

// --- the applier under test -------------------------------------------

type invoiceApplier struct {
	tenantID  uuid.UUID
	companyID uuid.UUID
	unitID    uuid.UUID
	chain     *zatca.Chain
}

func (a *invoiceApplier) Ordered() bool { return true }

func (a *invoiceApplier) Apply(
	ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, item Item,
) error {
	var p invoicePayload
	if err := json.Unmarshal(item.Payload, &p); err != nil {
		return errs.New(errs.CodeInvalidInput, "That invoice could not be read.")
	}

	// The entity UUID is the idempotency anchor: the same sale arriving twice
	// carries the same one.
	var existing int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM sales_invoice WHERE uuid = $1`, item.EntityUUID).
		Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return ErrAlreadyApplied
	}

	var invoiceID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO sales_invoice
		  (tenant_id, company_id, device_id, uuid, doc_type, issue_date, issued_at,
		   currency, total_inclusive, state)
		VALUES ($1,$2,$3,$4,'simplified',current_date,now(),'SAR',$5,'signed_pending_report')
		RETURNING id`,
		tenantID, a.companyID, deviceID, item.EntityUUID, p.Total).Scan(&invoiceID); err != nil {
		return err
	}

	return a.chain.RecordTerminalSigned(ctx, tx, invoiceID, tenantID, zatca.Link{
		EGSUnitID: a.unitID, ICV: p.ICV, PIH: p.PIH,
		InvoiceHash: p.Hash, SchemaVersion: "1.2",
	})
}

// --- fixture -----------------------------------------------------------

type fixture struct {
	pool      *db.Pool
	engine    *Engine
	chain     *zatca.Chain
	tenantID  uuid.UUID
	companyID uuid.UUID
	storeID   uuid.UUID
	deviceID  uuid.UUID
	unitID    uuid.UUID
}

type stubHasher struct{}

func (stubHasher) SchemaVersion() string { return "1.2" }
func (stubHasher) Hash(context.Context, zatca.Document) (string, error) {
	return "unused-in-terminal-signed-path", nil
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()

	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	f := &fixture{pool: pool, chain: zatca.NewChain(pool, stubHasher{})}

	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenant (name) VALUES ('Sync Test') RETURNING id`).
			Scan(&f.tenantID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO company (tenant_id, legal_name, country, base_currency)
			VALUES ($1,'Sync Test Co','sa','SAR') RETURNING id`,
			f.tenantID).Scan(&f.companyID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO store (tenant_id, company_id, code, name)
			VALUES ($1,$2,'MAIN','Main') RETURNING id`,
			f.tenantID, f.companyID).Scan(&f.storeID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO egs_unit (tenant_id, company_id, store_id, label, architecture)
			VALUES ($1,$2,$3,'till-1','smart_pos') RETURNING id`,
			f.tenantID, f.companyID, f.storeID).Scan(&f.unitID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO device (tenant_id, company_id, store_id, terminal_label,
			                    status, egs_unit_id)
			VALUES ($1,$2,$3,'Till 1','active',$4) RETURNING id`,
			f.tenantID, f.companyID, f.storeID, f.unitID).Scan(&f.deviceID)
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, f.tenantID)
			return err
		})
	})

	f.engine = NewEngine(pool)
	f.engine.Register("sales_invoice", &invoiceApplier{
		tenantID: f.tenantID, companyID: f.companyID,
		unitID: f.unitID, chain: f.chain,
	})
	return f
}

func (f *fixture) countInvoices(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.TxAsTenant(context.Background(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM sales_invoice WHERE company_id = $1`, f.companyID).Scan(&n)
	}); err != nil {
		t.Fatalf("count invoices: %v", err)
	}
	return n
}

// --- the gate ----------------------------------------------------------

// QA gate M3, end to end.
func TestFiveHundredOfflineInvoicesSyncWithoutDuplicatesOrLoss(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	const sales = 500

	// A full day with no connectivity.
	terminal := newOfflineTerminal(f.deviceID)
	for i := 0; i < sales; i++ {
		terminal.sell(fmt.Sprintf("%d.00", 100+i))
	}
	if len(terminal.queue) != sales {
		t.Fatalf("terminal queued %d sales, want %d", len(terminal.queue), sales)
	}

	// Reconnect. A real terminal drains in batches rather than one huge push.
	const batchSize = 50
	totalApplied := 0

	for start := 0; start < sales; start += batchSize {
		end := min(start+batchSize, sales)

		res, err := f.engine.Push(ctx, f.tenantID, Batch{
			DeviceID:       f.deviceID,
			IdempotencyKey: fmt.Sprintf("drain-%d", start),
			Items:          terminal.queue[start:end],
		})
		if err != nil {
			t.Fatalf("push batch starting at %d: %v", start, err)
		}
		if res.Failed != 0 || res.Blocked != 0 {
			for _, it := range res.Items {
				if it.State == "failed" || it.State == "blocked" {
					t.Fatalf("item seq %d was %s: %s", it.Seq, it.State, it.Error)
				}
			}
		}
		totalApplied += res.Applied
	}

	// Zero lost.
	if totalApplied != sales {
		t.Fatalf("applied %d of %d invoices", totalApplied, sales)
	}
	if got := f.countInvoices(t); got != sales {
		t.Fatalf("%d invoices in the database, want %d", got, sales)
	}

	// Zero duplicates, and the chain in order.
	breaks, err := f.chain.Verify(ctx, f.tenantID, f.unitID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(breaks) != 0 {
		t.Fatalf("chain is broken after syncing %d offline invoices: %s",
			sales, zatca.Describe(breaks))
	}

	var chainLen int
	if err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM zatca_invoice WHERE egs_unit_id = $1`, f.unitID).Scan(&chainLen)
	}); err != nil {
		t.Fatalf("count chain: %v", err)
	}
	if chainLen != sales {
		t.Fatalf("chain holds %d invoices, want %d", chainLen, sales)
	}

	// The cursor has caught up, so the terminal may clear its queue.
	health, err := f.engine.HealthFor(ctx, f.tenantID, f.deviceID)
	if err != nil {
		t.Fatalf("HealthFor: %v", err)
	}
	if health.Pending != 0 || health.Blocked != 0 || health.Failed != 0 {
		t.Fatalf("after a full drain the device still shows pending=%d blocked=%d failed=%d",
			health.Pending, health.Blocked, health.Failed)
	}
	if health.GapSize != 0 {
		t.Fatalf("the device shows a gap of %d after a complete sync", health.GapSize)
	}
}

// The realistic failure: the connection drops after the server committed but
// before the terminal saw the response, so the terminal resends.
func TestResendingAnEntireBatchCreatesNoDuplicates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	terminal := newOfflineTerminal(f.deviceID)
	for i := 0; i < 20; i++ {
		terminal.sell("115.00")
	}

	batch := Batch{
		DeviceID: f.deviceID, IdempotencyKey: "drain-1", Items: terminal.queue,
	}

	first, err := f.engine.Push(ctx, f.tenantID, batch)
	if err != nil {
		t.Fatalf("first push: %v", err)
	}
	if first.Applied != 20 {
		t.Fatalf("first push applied %d of 20", first.Applied)
	}
	if first.Replayed {
		t.Fatal("a first push was reported as a replay")
	}

	// The terminal never saw the response and sends the same batch again.
	second, err := f.engine.Push(ctx, f.tenantID, batch)
	if err != nil {
		t.Fatalf("resend: %v", err)
	}
	if !second.Replayed {
		t.Fatal("a resent batch was not recognised as a replay")
	}
	if second.Applied != first.Applied || second.BatchID != first.BatchID {
		t.Fatal("a resent batch was processed again instead of returning its " +
			"original outcome")
	}

	if got := f.countInvoices(t); got != 20 {
		t.Fatalf("%d invoices after resending the same batch, want 20", got)
	}
}

// A device that lost its own bookkeeping resends old items under a NEW batch
// key. The batch-level check cannot help; the entity UUID must.
func TestResendUnderANewKeyIsStillDeduplicated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	terminal := newOfflineTerminal(f.deviceID)
	for i := 0; i < 10; i++ {
		terminal.sell("115.00")
	}

	if _, err := f.engine.Push(ctx, f.tenantID, Batch{
		DeviceID: f.deviceID, IdempotencyKey: "first", Items: terminal.queue,
	}); err != nil {
		t.Fatalf("first push: %v", err)
	}

	res, err := f.engine.Push(ctx, f.tenantID, Batch{
		DeviceID: f.deviceID, IdempotencyKey: "second-key-same-items",
		Items: terminal.queue,
	})
	if err != nil {
		t.Fatalf("second push: %v", err)
	}

	if res.Applied != 0 {
		t.Fatalf("%d items were applied a second time under a new batch key", res.Applied)
	}
	if res.Duplicates != 10 {
		t.Fatalf("duplicates = %d, want 10 — a resend should be recognised, "+
			"not treated as new work", res.Duplicates)
	}
	if got := f.countInvoices(t); got != 10 {
		t.Fatalf("%d invoices after a resend under a new key, want 10", got)
	}
}

// Duplicates are the expected outcome of a healthy retry and must not be
// reported as failures — a device seeing failures retries forever.
func TestDuplicatesAreNotFailures(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	terminal := newOfflineTerminal(f.deviceID)
	terminal.sell("115.00")

	if _, err := f.engine.Push(ctx, f.tenantID, Batch{
		DeviceID: f.deviceID, IdempotencyKey: "a", Items: terminal.queue,
	}); err != nil {
		t.Fatalf("push: %v", err)
	}

	res, err := f.engine.Push(ctx, f.tenantID, Batch{
		DeviceID: f.deviceID, IdempotencyKey: "b", Items: terminal.queue,
	})
	if err != nil {
		t.Fatalf("resend: %v", err)
	}
	if res.Failed != 0 {
		t.Fatalf("a resend produced %d failures; a device would retry these forever",
			res.Failed)
	}
	for _, it := range res.Items {
		if it.State != "duplicate" {
			t.Fatalf("item state = %q, want duplicate", it.State)
		}
	}
}

// Items arriving out of order must still be applied in sequence, or the ZATCA
// chain would be submitted out of ICV order.
func TestItemsAreAppliedInSequenceOrder(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	terminal := newOfflineTerminal(f.deviceID)
	for i := 0; i < 5; i++ {
		terminal.sell("115.00")
	}

	// Shuffle: send them back to front.
	shuffled := make([]Item, len(terminal.queue))
	for i, it := range terminal.queue {
		shuffled[len(terminal.queue)-1-i] = it
	}

	res, err := f.engine.Push(ctx, f.tenantID, Batch{
		DeviceID: f.deviceID, IdempotencyKey: "reversed", Items: shuffled,
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if res.Applied != 5 {
		for _, it := range res.Items {
			if it.State != "applied" {
				t.Logf("seq %d: %s — %s", it.Seq, it.State, it.Error)
			}
		}
		t.Fatalf("applied %d of 5 when the batch arrived in reverse order", res.Applied)
	}

	breaks, err := f.chain.Verify(ctx, f.tenantID, f.unitID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(breaks) != 0 {
		t.Fatalf("chain broken after a reversed batch: %s", zatca.Describe(breaks))
	}
}

// One bad item must not take down the batch around it. A terminal that made 500
// sales and one unreadable record must land the 500.
func TestUnknownEntityTypeFailsOnlyItself(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	terminal := newOfflineTerminal(f.deviceID)
	terminal.sell("115.00")
	terminal.sell("115.00")

	items := append([]Item(nil), terminal.queue...)
	// Something a newer terminal build sends that this server does not know.
	items = append(items, Item{
		Seq: 99, EntityUUID: uuid.New(),
		EntityType: "loyalty_adjustment", Payload: json.RawMessage(`{}`),
	})

	res, err := f.engine.Push(ctx, f.tenantID, Batch{
		DeviceID: f.deviceID, IdempotencyKey: "mixed", Items: items,
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if res.Applied != 2 {
		t.Fatalf("applied %d of 2 valid invoices; one unknown item took others down",
			res.Applied)
	}
	if res.Failed != 1 {
		t.Fatalf("failed = %d, want 1", res.Failed)
	}

	// The message must point at the likely cause rather than just refusing.
	for _, it := range res.Items {
		if it.State == "failed" && !strings.Contains(it.Error, "version") {
			t.Errorf("the refusal does not suggest a version mismatch: %s", it.Error)
		}
	}
}

// The cursor reports the highest CONTIGUOUS sequence. Reporting the highest
// would tell a terminal it may discard an invoice the server never received —
// which is exactly the "zero lost invoices" clause of gate M3.
func TestCursorStopsAtTheFirstHole(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	terminal := newOfflineTerminal(f.deviceID)
	for i := 0; i < 5; i++ {
		terminal.sell("115.00")
	}

	// Send 1, 2, then 4 and 5, holding back 3 — as if that one item is still
	// being retried.
	partial := []Item{terminal.queue[0], terminal.queue[1],
		terminal.queue[3], terminal.queue[4]}

	res, err := f.engine.Push(ctx, f.tenantID, Batch{
		DeviceID: f.deviceID, IdempotencyKey: "with-hole", Items: partial,
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	if res.Cursor != 2 {
		t.Fatalf("cursor = %d, want 2. Anything higher tells the terminal it may "+
			"discard invoice 3, which the server does not have.", res.Cursor)
	}

	health, err := f.engine.HealthFor(ctx, f.tenantID, f.deviceID)
	if err != nil {
		t.Fatalf("HealthFor: %v", err)
	}
	if health.GapSize == 0 {
		t.Fatal("the device shows no gap while invoice 3 is missing")
	}
}

// A sync item is evidence of what a device sent. It is never deleted, so a
// failure stays visible rather than being silently lost.
func TestSyncItemsCannotBeDeleted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	terminal := newOfflineTerminal(f.deviceID)
	terminal.sell("115.00")
	if _, err := f.engine.Push(ctx, f.tenantID, Batch{
		DeviceID: f.deviceID, IdempotencyKey: "x", Items: terminal.queue,
	}); err != nil {
		t.Fatalf("push: %v", err)
	}

	err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM sync_item WHERE device_id = $1`, f.deviceID)
		return e
	})
	if err == nil {
		t.Fatal("a sync item was deleted; failed work must stay visible")
	}
}
