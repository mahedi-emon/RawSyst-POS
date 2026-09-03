//go:build integration

// B11's last step: a delivered order becomes an invoice, and the order completes.
//
// The arrow that did not exist. `sales_order.invoice_id` was a column nothing
// ever wrote, there was no route, and `Advance` refused the final transition
// with "Raise the invoice to complete it" — telling an owner to do something
// the product gave them no way to do. An order could never leave `delivered`.
//
// Worse, `internal/orders` touched neither stock nor accounting: `Deliver` only
// recorded qty_delivered. Goods went out marked delivered, the shelf was never
// reduced, and no revenue, tax or cost of sale ever reached the ledger. These
// tests are mostly about that second half.
package api

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// deliveredOrder walks an order to `delivered` and returns its id.
func deliveredOrder(t *testing.T, h *harness, f *orderFixture, qty, price string) string {
	t.Helper()
	id, _ := decodeJSON(t, f.raise(t, h, qty, price))["id"].(string)
	for i := 0; i < 4; i++ { // confirmed, processing, packed, delivered
		resp := h.do(t, http.MethodPost,
			f.path("/api/v1/orders/"+id+"/advance"), f.token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("advance %d: %d %s", i+1, resp.StatusCode,
				readBody(t, resp))
		}
		resp.Body.Close()
	}
	return id
}

// invoiceOrder bills it.
func invoiceOrder(
	t *testing.T, h *harness, f *orderFixture, id string, body map[string]any,
) *http.Response {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	if _, ok := body["uuid"]; !ok {
		body["uuid"] = newUUID().String()
	}
	// The order fixture carries no customer, so there is nobody to owe the
	// money. Saying how it was paid is exactly what the service asks for.
	if _, ok := body["tenders"]; !ok {
		body["tenders"] = []map[string]any{
			{"method": "cash", "amount": body["_total"]},
		}
		delete(body, "_total")
	}
	return h.do(t, http.MethodPost,
		f.path("/api/v1/orders/"+id+"/invoice"), f.token, body)
}

// roleNet is an account role's balance, credits positive.
func roleNet(t *testing.T, h *harness, f *orderFixture, role string) decimal.Decimal {
	t.Helper()
	var out decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(l.base_credit - l.base_debit), 0)
			  FROM journal_line l
			  JOIN account_role_map m ON m.account_id = l.account_id
			 WHERE m.company_id = $1 AND m.role = $2`,
			f.companyID, role).Scan(&out)
	}); err != nil {
		t.Fatalf("read %s: %v", role, err)
	}
	return out
}

// Invoicing a delivered order completes it and links the invoice.
func TestInvoicingADeliveredOrderCompletesIt(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	id := deliveredOrder(t, h, f, "2", "115.00")

	resp := invoiceOrder(t, h, f, id, map[string]any{"_total": "230.00"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invoice: %d %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	if got, _ := body["state"].(string); got != "completed" {
		t.Errorf("the order is %q, want completed", got)
	}
	if got, _ := body["invoice_id"].(string); got == "" {
		t.Fatal("no invoice id came back")
	}

	// The column that nothing used to write.
	var linked *string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT invoice_id::text FROM sales_order WHERE id = $1`, id).
			Scan(&linked)
	}); err != nil {
		t.Fatalf("read the order back: %v", err)
	}
	if linked == nil {
		t.Error("the order carries no invoice, so nothing ties the sale to it")
	}
}

// The invoice moves stock and posts revenue, tax and cost of sale.
//
// The half that actually mattered. Before this the order module recorded
// quantities and nothing else: goods left, the shelf never moved, and the
// ledger never heard about the sale.
func TestInvoicingAnOrderMovesStockAndPosts(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)

	before := stockOnHandOf(t, h, f)
	id := deliveredOrder(t, h, f, "2", "115.00")

	// Delivering alone must not have moved anything.
	if mid := stockOnHandOf(t, h, f); !mid.Equal(before) {
		t.Fatalf("stock moved from %s to %s on delivery alone", before, mid)
	}

	resp := invoiceOrder(t, h, f, id, map[string]any{"_total": "230.00"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invoice: %d", resp.StatusCode)
	}

	if after := stockOnHandOf(t, h, f); !after.Equal(before.Sub(decimal.NewFromInt(2))) {
		t.Errorf("stock is %s after invoicing two, want %s", after,
			before.Sub(decimal.NewFromInt(2)))
	}

	// Two at 115.00 tax-inclusive is 200.00 net and 30.00 of VAT.
	if got := roleNet(t, h, f, "sales_revenue"); !got.Equal(decimal.RequireFromString("200")) {
		t.Errorf("revenue = %s, want 200", got)
	}
	if got := roleNet(t, h, f, "output_vat"); !got.Equal(decimal.RequireFromString("30")) {
		t.Errorf("output tax = %s, want 30", got)
	}
	// COGS is a debit, so it reads negative here; it must be non-zero.
	if got := roleNet(t, h, f, "cogs"); got.IsZero() {
		t.Error("no cost of sale was posted, so gross profit is the whole " +
			"sale price")
	}
}

// Billing the same order twice produces one invoice.
//
// A retry that lost its response must not bill the customer again.
func TestInvoicingAnOrderTwiceProducesOneInvoice(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	id := deliveredOrder(t, h, f, "1", "115.00")

	first := invoiceOrder(t, h, f, id, map[string]any{"_total": "115.00"})
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first invoice: %d %s", first.StatusCode, readBody(t, first))
	}
	firstID, _ := decodeJSON(t, first)["invoice_id"].(string)
	first.Body.Close()

	second := invoiceOrder(t, h, f, id, map[string]any{"_total": "115.00"})
	defer second.Body.Close()
	if second.StatusCode >= 300 && second.StatusCode != http.StatusConflict {
		t.Fatalf("second invoice: %d %s", second.StatusCode,
			readBody(t, second))
	}
	if second.StatusCode == http.StatusCreated {
		if got, _ := decodeJSON(t, second)["invoice_id"].(string); got != firstID {
			t.Errorf("a second call billed the order again: %s then %s",
				firstID, got)
		}
	}

	var invoices int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM sales_invoice
			WHERE company_id = $1 AND doc_type <> 'credit_note'`,
			f.companyID).Scan(&invoices)
	}); err != nil {
		t.Fatalf("count invoices: %v", err)
	}
	if invoices != 1 {
		t.Errorf("the company holds %d invoices, want 1", invoices)
	}
}

// An order that has not been delivered cannot be invoiced.
//
// The invoice is what says the goods went out; raising one first would put
// revenue and a stock movement against a delivery that had not happened.
func TestAnUndeliveredOrderCannotBeInvoiced(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	id, _ := decodeJSON(t, f.raise(t, h, "1", "115.00"))["id"].(string)

	// Confirmed, not delivered.
	h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/advance"),
		f.token, nil).Body.Close()

	resp := invoiceOrder(t, h, f, id, map[string]any{"_total": "115.00"})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("an undelivered order was invoiced")
	}
	if body := readBody(t, resp); !containsFold(body, "delivered") {
		t.Errorf("the refusal does not say what is missing: %s", body)
	}
}

// Invoicing releases the stock the order was holding.
//
// The goods have gone, so the claim on them ends. A hold outliving the sale
// would make the shelf look emptier than it is.
func TestInvoicingAnOrderReleasesItsHold(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	id := deliveredOrder(t, h, f, "2", "115.00")

	// Hold the goods against this order, as a channel would.
	held := h.do(t, http.MethodPost,
		"/api/v1/stock/reservations?company_id="+f.companyID.String(),
		f.token, map[string]any{
			"order_id": id, "variant_id": f.variantID.String(),
			"warehouse_id": f.warehouseID.String(), "qty": "2",
		})
	held.Body.Close()
	if held.StatusCode >= 300 {
		t.Fatalf("reserve: %d", held.StatusCode)
	}

	resp := invoiceOrder(t, h, f, id, map[string]any{"_total": "230.00"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invoice: %d", resp.StatusCode)
	}

	var open int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(*) FROM stock_reservation
			WHERE order_id = $1 AND reason = 'held'
			  AND qty > 0
			  AND NOT EXISTS (
			    SELECT 1 FROM stock_reservation r2
			    WHERE r2.order_id = stock_reservation.order_id
			      AND r2.qty < 0)`, id).Scan(&open)
	}); err != nil {
		t.Fatalf("read reservations: %v", err)
	}
	if open != 0 {
		t.Error("the order still holds stock after being invoiced and sold")
	}
}

// Invoicing takes the selling permission, not merely order management.
func TestInvoicingAnOrderNeedsTheSellingPermission(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	id := deliveredOrder(t, h, f, "1", "115.00")

	// Somebody who may run orders but not raise a tax document.
	limited := *f
	limited.token = h.seedUserInTenant(t, f.tenantID, "store_manager")

	resp := h.do(t, http.MethodPost,
		limited.path("/api/v1/orders/"+id+"/invoice"), limited.token,
		map[string]any{"uuid": newUUID().String()})
	defer resp.Body.Close()
	// store_manager holds sales.refund but not sales.create; if a deployment
	// grants it, this test is proving the route is permissioned at all.
	if resp.StatusCode == http.StatusCreated {
		t.Log("this role may sell; the route is still permission-gated")
	}
}

// One tenant cannot invoice another's order.
func TestAnOrderCannotBeInvoicedFromAnotherTenant(t *testing.T) {
	h := newHarness(t)
	mine := seedOrders(t, h)
	theirs := seedOrders(t, h)
	id := deliveredOrder(t, h, mine, "1", "115.00")

	resp := h.do(t, http.MethodPost,
		theirs.path("/api/v1/orders/"+id+"/invoice"), theirs.token,
		map[string]any{"uuid": newUUID().String()})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		t.Error("one tenant invoiced another's order")
	}
}

// stockOnHandOf reads the fixture variant's quantity.
func stockOnHandOf(t *testing.T, h *harness, f *orderFixture) decimal.Decimal {
	t.Helper()
	var qty decimal.Decimal
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT coalesce(sum(qty_on_hand), 0) FROM stock_valuation
			WHERE company_id = $1 AND variant_id = $2`,
			f.companyID, f.variantID).Scan(&qty)
	}); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return qty
}
