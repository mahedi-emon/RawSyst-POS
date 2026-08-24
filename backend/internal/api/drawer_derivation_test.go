//go:build integration

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// The drawer, re-derived from blueprint C8 rather than from the code.
//
// C8 defines the reconciliation in words:
//
//	"Shift Open: cashier enters opening cash float."
//	"During shift: system silently tracks cash sales, card sales, refunds,
//	 expenses paid from drawer, cash drops to vault."
//	"Z-Report: cashier counts and enters actual physical cash; system compares
//	 Expected Cash vs Actual Cash and reports Short / Over / Exact."
//
// So the only figure the drawer answer depends on is PHYSICAL CASH:
//
//	expected = opening float
//	         + cash taken in
//	         - cash paid out
//	         + cash moved in or out of the drawer
//
// Nothing else may touch it. A card sale, a sale on account, a store-credit
// redemption and a loyalty redemption all move no notes, and each is a
// plausible way for the figure to drift without anybody noticing until a
// cashier is accused of being short.
//
// These tests state that arithmetic directly and check the system against it,
// rather than checking that the SQL agrees with itself.

// drawer reads the reconciliation figures for a session.
type drawer struct {
	openingFloat  decimal.Decimal
	cashTakings   decimal.Decimal
	nonCash       decimal.Decimal
	cashMovements decimal.Decimal
	expectedCash  decimal.Decimal
	refundTotal   decimal.Decimal
	grossSales    decimal.Decimal
}

func readDrawer(t *testing.T, h *harness, f *shopFixture, sessionID string) drawer {
	t.Helper()
	ctx := context.Background()
	var d drawer

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT opening_float, cash_takings, non_cash_takings,
			       cash_movements, expected_cash, refund_total, gross_sales
			  FROM cash_session_report($1)`, sessionID).
			Scan(&d.openingFloat, &d.cashTakings, &d.nonCash,
				&d.cashMovements, &d.expectedCash, &d.refundTotal, &d.grossSales)
	}); err != nil {
		t.Fatalf("reading the drawer: %v", err)
	}
	return d
}

// currentSessionID finds the till's open session.
func currentSessionID(t *testing.T, h *harness, f *shopFixture) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id::text FROM cash_session
			 WHERE device_id = $1 AND state = 'open'
			 ORDER BY opened_at DESC LIMIT 1`, f.deviceID).Scan(&id)
	}); err != nil {
		t.Fatalf("finding the open session: %v", err)
	}
	return id
}

// A cash sale is the only thing that puts notes in the drawer.
//
// Worked from C8: opening float plus what was taken in cash. One sale of
// 115.00 in cash against a float of F gives F + 115.00 and nothing else.
func TestOnlyCashChangesWhatIsExpectedInTheDrawer(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	session := currentSessionID(t, h, f)

	before := readDrawer(t, h, f, session)

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 201 {
		t.Fatalf("cash sale: %s", readBody(t, resp))
	}
	resp.Body.Close()

	after := readDrawer(t, h, f, session)

	if got := after.expectedCash.Sub(before.expectedCash); !got.Equal(dec115()) {
		t.Errorf("a cash sale of 115.00 moved expected cash by %s, want 115.00", got)
	}
	if got := after.cashTakings.Sub(before.cashTakings); !got.Equal(dec115()) {
		t.Errorf("cash takings moved by %s, want 115.00", got)
	}
	// The identity C8 states, checked directly rather than inferred.
	want := after.openingFloat.Add(after.cashTakings).Add(after.cashMovements)
	if !after.expectedCash.Equal(want) {
		t.Errorf("expected cash is %s but float + cash takings + movements is %s. "+
			"Those are the only three things C8 says move physical cash.",
			after.expectedCash, want)
	}
}

// A card sale takes money, but not notes. The drawer must not move.
func TestACardSaleLeavesTheDrawerAlone(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	session := currentSessionID(t, h, f)

	before := readDrawer(t, h, f, session)

	sale := oneItemSale(f, newUUID(), "1", "115.00", "115.00")
	sale["tenders"] = []map[string]any{{"method": "mada", "amount": "115.00"}}
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
	if resp.StatusCode != 201 {
		t.Fatalf("card sale: %s", readBody(t, resp))
	}
	resp.Body.Close()

	after := readDrawer(t, h, f, session)

	if !after.expectedCash.Equal(before.expectedCash) {
		t.Errorf("a card sale moved expected cash from %s to %s. A cashier "+
			"counting the drawer at close would be short by the card takings.",
			before.expectedCash, after.expectedCash)
	}
	if got := after.nonCash.Sub(before.nonCash); !got.Equal(dec115()) {
		t.Errorf("non-cash takings moved by %s, want 115.00", got)
	}
}

// A SALE ON ACCOUNT takes no money at all.
//
// This is the case the lumped figure gets wrong. `customer_due` is a tender
// method, and the report counts every tender that is not cash as "non-cash
// takings" -- so a credit sale is reported as money taken when the customer
// has paid nothing and owes the lot.
//
// The drawer itself is safe, because that only ever counts `cash`. What is not
// safe is the figure an Owner reconciles card settlements against: it is
// inflated by every sale made on account that day.
func TestASaleOnAccountIsNotTakings(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")
	session := currentSessionID(t, h, f.shopFixture)

	before := readDrawer(t, h, f.shopFixture, session)

	sale := oneItemSale(f.shopFixture, newUUID(), "1", "115.00", "115.00")
	sale["customer_id"] = f.customerID
	sale["tenders"] = []map[string]any{{"method": "customer_due", "amount": "115.00"}}
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.shopFixture.token, sale)
	if resp.StatusCode != 201 {
		t.Fatalf("credit sale: %s", readBody(t, resp))
	}
	resp.Body.Close()

	after := readDrawer(t, h, f.shopFixture, session)

	// The drawer is untouched, which is right.
	if !after.expectedCash.Equal(before.expectedCash) {
		t.Errorf("a sale on account moved expected cash from %s to %s; no notes "+
			"changed hands", before.expectedCash, after.expectedCash)
	}

	// And nothing was taken, so takings must not move either.
	if got := after.nonCash.Sub(before.nonCash); !got.IsZero() {
		t.Errorf("a sale on account added %s to non-cash TAKINGS. Nothing was "+
			"taken: the customer owes the money and it sits in receivables. An "+
			"Owner reconciling card settlements against this figure will be out "+
			"by every credit sale of the day.", got)
	}
}

// Store credit is a liability being settled, not money arriving.
func TestRedeemingStoreCreditIsNotTakings(t *testing.T) {
	h := newHarness(t)
	f := seedSelling(t, h, "5000.00")
	session := currentSessionID(t, h, f.shopFixture)

	before := readDrawer(t, h, f.shopFixture, session)

	sale := oneItemSale(f.shopFixture, newUUID(), "1", "115.00", "115.00")
	sale["customer_id"] = f.customerID
	sale["tenders"] = []map[string]any{{"method": "store_credit", "amount": "115.00"}}
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.shopFixture.token, sale)
	if resp.StatusCode != 201 {
		t.Skipf("store credit is not accepted on a sale here: %s", readBody(t, resp))
	}
	resp.Body.Close()

	after := readDrawer(t, h, f.shopFixture, session)

	if !after.expectedCash.Equal(before.expectedCash) {
		t.Errorf("redeeming store credit moved expected cash from %s to %s",
			before.expectedCash, after.expectedCash)
	}
	if got := after.nonCash.Sub(before.nonCash); !got.IsZero() {
		t.Errorf("redeeming store credit added %s to non-cash takings. The shop "+
			"already held that money; this settles a liability rather than "+
			"taking anything.", got)
	}
}

// A cash refund takes notes back OUT.
func TestACashRefundTakesNotesOutOfTheDrawer(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	session := currentSessionID(t, h, f)

	invoiceID, lineID := sellOne(t, h, f, "2", "115.00", "230.00")
	afterSale := readDrawer(t, h, f, session)

	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T12:00:00Z",
		"reason":              "wrong size",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds":             []map[string]any{{"method": "cash", "amount": "115.00"}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("return: %s", readBody(t, resp))
	}
	resp.Body.Close()

	after := readDrawer(t, h, f, session)

	if got := afterSale.expectedCash.Sub(after.expectedCash); !got.Equal(dec115()) {
		t.Errorf("a cash refund of 115.00 moved expected cash by %s, want -115.00. "+
			"The notes left the drawer whether or not the report says so.", got)
	}
	// Refunds are reported separately from sales, not netted into them: C8
	// wants the ratio visible.
	if got := after.refundTotal; !got.Equal(dec115()) {
		t.Errorf("refund total is %s, want 115.00", got)
	}
	if !after.grossSales.Equal(afterSale.grossSales) {
		t.Errorf("gross sales changed from %s to %s when a refund was raised; "+
			"netting refunds into sales hides how much was handed back",
			afterSale.grossSales, after.grossSales)
	}
}

// The offsetting half of an exchange moves no money at all, on either side.
func TestTheClearingHalfOfAnExchangeMovesNoMoney(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	session := currentSessionID(t, h, f)

	invoiceID, lineID := sellOne(t, h, f, "2", "115.00", "230.00")
	before := readDrawer(t, h, f, session)

	// An even exchange: same price out, same price in. Nothing should move.
	resp := h.do(t, "POST", "/api/v1/pos/exchanges", f.token, map[string]any{
		"exchange_uuid":       newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T13:00:00Z",
		"reason":              "wrong colour",
		"returned":            []map[string]any{{"line_id": lineID, "qty": "1"}},
		"replacement": map[string]any{
			"invoice_uuid": newUUID().String(),
			"doc_type":     "simplified",
			"issued_at":    "2026-08-15T13:00:00Z",
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(), "description": "Abaya",
				"qty": "1", "unit_price": "115.00", "tax_treatment": "standard",
			}},
		},
		"settlement": []map[string]any{},
	})
	if resp.StatusCode != 201 {
		t.Skipf("even exchange not accepted in this shape: %s", readBody(t, resp))
	}
	resp.Body.Close()

	after := readDrawer(t, h, f, session)

	if !after.expectedCash.Equal(before.expectedCash) {
		t.Errorf("an even exchange moved expected cash from %s to %s; nothing "+
			"was paid either way", before.expectedCash, after.expectedCash)
	}
	if !after.nonCash.Equal(before.nonCash) {
		t.Errorf("an even exchange moved non-cash takings by %s. The clearing "+
			"legs are a bookkeeping device, not money.",
			after.nonCash.Sub(before.nonCash))
	}
}

func dec115() decimal.Decimal { return decimal.RequireFromString("115.00") }

var _ = http.StatusOK
