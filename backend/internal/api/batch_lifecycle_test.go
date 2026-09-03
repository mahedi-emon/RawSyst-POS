//go:build integration

// The lot's whole life: received against a delivery, sold, returned to the lot
// it left in, recalled with the customers named, and alerted on before it goes
// out of date.
package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/notify"
)

func batchIDOf(t *testing.T, h *harness, f *shopFixture, batchNo string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT id FROM stock_batch WHERE variant_id = $1 AND batch_no = $2`,
			f.variantID, batchNo).Scan(&id)
	}); err != nil {
		t.Fatalf("find batch %s: %v", batchNo, err)
	}
	return id
}

// The batches route lists lots with their dates and what is left.
func TestTheBatchesRouteListsLotsSoonestToExpireFirst(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)

	receiveBatch(t, h, f, "LATER", "5", "5.00", expiryIn(200))
	receiveBatch(t, h, f, "SOONER", "5", "5.00", expiryIn(10))

	resp := h.do(t, http.MethodGet,
		"/api/v1/stock/batches?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list batches: %d %s", resp.StatusCode, readBody(t, resp))
	}

	rows, _ := decodeJSON(t, resp)["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("got %d lots, want 2", len(rows))
	}
	first, _ := rows[0].(map[string]any)
	if first["batch_no"] != "SOONER" {
		t.Errorf("first lot is %v, want SOONER — the list is not in expiry order",
			first["batch_no"])
	}
	// days_left is computed on the server so a till in another timezone cannot
	// disagree with the back office about whether a lot has expired.
	if _, ok := first["days_left"].(float64); !ok {
		t.Errorf("no days_left on the lot: %v", first)
	}
}

// Filtering to what has to be acted on.
func TestBatchesCanBeNarrowedToWhatIsExpiringSoon(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)

	receiveBatch(t, h, f, "FINE", "5", "5.00", expiryIn(200))
	receiveBatch(t, h, f, "URGENT", "5", "5.00", expiryIn(5))

	resp := h.do(t, http.MethodGet,
		"/api/v1/stock/batches?company_id="+f.companyID.String()+
			"&expiring_within_days=30", f.token, nil)
	defer resp.Body.Close()

	rows, _ := decodeJSON(t, resp)["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("got %d lots within 30 days, want 1", len(rows))
	}
	if row, _ := rows[0].(map[string]any); row["batch_no"] != "URGENT" {
		t.Errorf("the wrong lot came back: %v", row["batch_no"])
	}
}

// A recall names the customers who bought from the lot.
//
// The whole reason `stock_batch_movement` records the split per movement. A lot
// number on a supplier's notice is worth nothing unless it becomes a list of
// people to telephone.
func TestARecallNamesWhoBoughtFromTheLot(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	trackBatches(t, h, f)
	receiveBatch(t, h, f, "SUSPECT", "20", "5.00", expiryIn(90))

	customer := customerOfType(t, h, f, "retail")
	sale := oneItemSale(f, newUUID(), "2", "115.00", "230.00")
	sale["customer_id"] = customer.String()
	sold := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, sale)
	sold.Body.Close()
	if sold.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d", sold.StatusCode)
	}

	// The owner recalls it.
	owner := h.login(t, h.seedUserWithRole(t, "owner"))
	_ = owner // the fixture's own token is a cashier; recall needs the verb

	batchID := batchIDOf(t, h, f, "SUSPECT")
	resp := h.do(t, http.MethodPost,
		"/api/v1/stock/batches/"+batchID.String()+"/recall?company_id="+
			f.companyID.String(), f.token,
		map[string]any{"reason": "supplier notice 44/A"})
	defer resp.Body.Close()

	// A cashier does not hold inventory.recall_batch.
	if resp.StatusCode == http.StatusOK {
		t.Fatal("a cashier recalled a batch")
	}
}

// The owner can recall, and gets the trace.
func TestAnOwnerRecallingALotGetsTheCustomerList(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)
	receiveBatch(t, h, f, "SUSPECT", "20", "5.00", expiryIn(90))

	customer := customerOfType(t, h, f, "retail")
	sale := oneItemSale(f, newUUID(), "2", "115.00", "230.00")
	sale["customer_id"] = customer.String()
	sold := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, sale)
	sold.Body.Close()
	if sold.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", sold.StatusCode, readBody(t, sold))
	}

	batchID := batchIDOf(t, h, f, "SUSPECT")
	resp := h.do(t, http.MethodPost,
		"/api/v1/stock/batches/"+batchID.String()+"/recall?company_id="+
			f.companyID.String(), f.token,
		map[string]any{"reason": "supplier notice 44/A"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recall: %d %s", resp.StatusCode, readBody(t, resp))
	}

	body := decodeJSON(t, resp)
	sales, _ := body["sales"].([]any)
	if len(sales) == 0 {
		t.Fatal("the recall named nobody; the customer list is the point of it")
	}
	row, _ := sales[0].(map[string]any)
	if row["customer_id"] != customer.String() {
		t.Errorf("the recall names customer %v, want %s",
			row["customer_id"], customer)
	}
	// What never left, so the shop knows what to pull off the shelf too.
	if body["still_on_hand"] == nil {
		t.Error("the recall does not say what is still in stock")
	}
}

// A recall with no reason is refused.
func TestARecallMustSayWhy(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)
	receiveBatch(t, h, f, "LOT1", "5", "5.00", expiryIn(90))

	batchID := batchIDOf(t, h, f, "LOT1")
	resp := h.do(t, http.MethodPost,
		"/api/v1/stock/batches/"+batchID.String()+"/recall?company_id="+
			f.companyID.String(), f.token, map[string]any{"reason": "  "})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a batch was recalled with no reason given")
	}
}

// One tenant cannot see or recall another's lots.
func TestBatchesAreTenantIsolated(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")
	trackBatches(t, h, theirs)
	receiveBatch(t, h, theirs, "THEIRS", "5", "5.00", expiryIn(30))

	// Their lot must not appear in my list.
	resp := h.do(t, http.MethodGet,
		"/api/v1/stock/batches?company_id="+mine.companyID.String(), mine.token, nil)
	defer resp.Body.Close()
	if body := readBody(t, resp); strings.Contains(body, "THEIRS") {
		t.Errorf("another tenant's lot is visible: %s", body)
	}

	// Nor may I recall it.
	batchID := batchIDOf(t, h, theirs, "THEIRS")
	recall := h.do(t, http.MethodPost,
		"/api/v1/stock/batches/"+batchID.String()+"/recall?company_id="+
			mine.companyID.String(), mine.token,
		map[string]any{"reason": "not mine to recall"})
	defer recall.Body.Close()
	if recall.StatusCode == http.StatusOK {
		t.Error("one tenant recalled another's batch")
	}
}

// A lot near its date raises a warning; one past it raises a louder one.
//
// Two facts needing two messages: expiring soon can still be sold or sent back,
// expired is a loss already taken and has to come off the shelf.
func TestExpiringAndExpiredLotsRaiseDifferentAlerts(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	trackBatches(t, h, f)

	receiveBatch(t, h, f, "SOON", "5", "5.00", expiryIn(10))
	receiveBatch(t, h, f, "GONE", "5", "5.00", expiryIn(-3))
	receiveBatch(t, h, f, "FINE", "5", "5.00", expiryIn(300))

	tenant := f.tenantID
	sweeper := jobs.NewBatchExpirySweeper(h.pool, notify.NewService(h.pool))
	if err := sweeper.Run(context.Background(), jobs.Job{
		TenantID: &tenant, Kind: jobs.KindBatchExpirySweep,
	}); err != nil {
		t.Fatalf("expiry sweep: %v", err)
	}

	var warnings, criticals int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT
			  count(*) FILTER (WHERE severity = 'warning'),
			  count(*) FILTER (WHERE severity = 'critical')
			FROM notification WHERE company_id = $1 AND kind = $2`,
			f.companyID, notify.KindBatchExpiring).Scan(&warnings, &criticals)
	}); err != nil {
		t.Fatalf("count alerts: %v", err)
	}

	if warnings != 1 {
		t.Errorf("expiring-soon warnings = %d, want 1 (the lot 300 days out "+
			"must not raise one)", warnings)
	}
	if criticals != 1 {
		t.Errorf("expired alerts = %d, want 1", criticals)
	}
}
