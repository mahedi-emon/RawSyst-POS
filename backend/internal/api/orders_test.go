//go:build integration

// Quotations, sales orders and the warehouse documents, derived from B11 and
// B12 rather than from the code.
//
//	B11: "Sales Order lifecycle: Draft → Confirmed → Processing → Packed →
//	 Delivered → Completed" and "Delivery Challan ... itemized WITHOUT pricing
//	 (for logistics/proof-of-delivery use)."
//
//	B12: wholesale customers get "minimum order quantity rules".
//
// The delivery-note test is the one that matters most. That piece of paper is
// handled by a driver, by a courier and by whoever signs for the goods at the
// other end — including, at a shared loading bay, a competitor's warehouse
// staff. None of them is entitled to know what the customer paid.
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type orderFixture struct {
	*shopFixture
	token string
}

func seedOrders(t *testing.T, h *harness) *orderFixture {
	t.Helper()
	f := h.seedShop(t, "owner")
	return &orderFixture{shopFixture: f, token: f.token}
}

func (f *orderFixture) path(base string) string {
	sep := "?"
	if containsQuery(base) {
		sep = "&"
	}
	return base + sep + "company_id=" + f.companyID.String()
}

// --- B11: the lifecycle ---------------------------------------------------

// An order is raised as a quotation, whatever the caller asks for.
//
// Confirming is the customer's decision, and a route that could skip it would
// put "the customer agreed" into the hands of whoever typed the order.
func TestAnOrderIsRaisedAsAQuotation(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)

	body := decodeJSON(t, f.raise(t, h, "2", "100.00"))
	if body["state"] != "quotation" {
		t.Errorf("a new order should be a quotation, not %v", body["state"])
	}
	if body["order_no"] == "" || body["order_no"] == nil {
		t.Error("and should carry a number")
	}
	if body["total"] != "200.00" {
		t.Errorf("two at a hundred is 200, not %v", body["total"])
	}
}

// The lifecycle runs forward, one step at a time.
func TestAnOrderWalksTheLifecycleInOrder(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	id := f.orderID(t, h)

	for _, want := range []string{"confirmed", "processing", "packed", "delivered"} {
		resp := h.do(t, http.MethodPost,
			f.path("/api/v1/orders/"+id+"/advance"), f.token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("advance to %s: status %d — %s",
				want, resp.StatusCode, readBody(t, resp))
		}
		if got := decodeJSON(t, resp)["state"]; got != want {
			t.Fatalf("state is %v, want %s", got, want)
		}
	}
}

// An order is finished by being INVOICED, not by being marked so.
//
// A route that could set `completed` would produce an order recorded as sold
// with no invoice behind it — and the invoice is the tax document, the thing in
// the hash chain, and the only record of the sale that counts.
func TestAnOrderCannotBeCompletedWithoutAnInvoice(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	id := f.orderID(t, h)

	for i := 0; i < 4; i++ {
		h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/advance"),
			f.token, nil).Body.Close()
	}

	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/orders/"+id+"/advance"), f.token, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("advancing a delivered order should conflict, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// Cancelling needs a reason: somebody will ask why an order for four thousand
// riyals disappeared.
func TestCancellingAnOrderNeedsAReason(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	id := f.orderID(t, h)

	refused := h.do(t, http.MethodPost,
		f.path("/api/v1/orders/"+id+"/cancel"), f.token,
		map[string]any{"reason": ""})
	if refused.StatusCode < 400 {
		t.Fatalf("cancelling with no reason should be refused, got %d — %s",
			refused.StatusCode, readBody(t, refused))
	}

	ok := h.do(t, http.MethodPost,
		f.path("/api/v1/orders/"+id+"/cancel"), f.token,
		map[string]any{"reason": "The customer changed their mind."})
	if ok.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel: %s", readBody(t, ok))
	}
}

// --- picking and delivery -------------------------------------------------

// A short pick is an ordinary event: the shelf had four when the order wanted
// six. Recorded rather than refused, because the alternative is a picker who
// cannot tell anybody what happened.
func TestAShortPickIsRecordedRatherThanRefused(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	raised := decodeJSON(t, f.raise(t, h, "6", "10.00"))
	id, _ := raised["id"].(string)
	lineID := f.firstLineID(t, raised)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/pick"),
		f.token, map[string]any{
			"lines": []map[string]any{{"line_id": lineID, "qty": "4"}},
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pick: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	lines, _ := decodeJSON(t, resp)["lines"].([]any)
	line, _ := lines[0].(map[string]any)
	if line["qty_picked"] != "4" {
		t.Errorf("four of six should be recorded as picked, not %v",
			line["qty_picked"])
	}
}

// Goods cannot leave a warehouse without having been taken off a shelf.
func TestMoreCannotBeDeliveredThanWasPicked(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	raised := decodeJSON(t, f.raise(t, h, "6", "10.00"))
	id, _ := raised["id"].(string)
	lineID := f.firstLineID(t, raised)

	h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/pick"), f.token,
		map[string]any{"lines": []map[string]any{{"line_id": lineID, "qty": "4"}}}).
		Body.Close()

	resp := h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/deliver"),
		f.token, map[string]any{
			"lines": []map[string]any{{"line_id": lineID, "qty": "5"}},
		})
	if resp.StatusCode < 400 {
		t.Fatalf("delivering five of four picked should be refused, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func TestMoreCannotBePickedThanWasOrdered(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	raised := decodeJSON(t, f.raise(t, h, "2", "10.00"))
	id, _ := raised["id"].(string)
	lineID := f.firstLineID(t, raised)

	resp := h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/pick"),
		f.token, map[string]any{
			"lines": []map[string]any{{"line_id": lineID, "qty": "3"}},
		})
	if resp.StatusCode < 400 {
		t.Fatalf("picking three of two ordered should be refused, got %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
}

// --- B11: the delivery note -----------------------------------------------

// "itemized WITHOUT pricing (for logistics/proof-of-delivery use)"
//
// The test asserts it of the whole payload rather than of named fields,
// because the failure being guarded against is a field somebody ADDS later.
func TestADeliveryNoteCarriesNoPrices(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	raised := decodeJSON(t, f.raise(t, h, "3", "1234.56"))
	id, _ := raised["id"].(string)
	lineID := f.firstLineID(t, raised)

	h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/pick"), f.token,
		map[string]any{"lines": []map[string]any{{"line_id": lineID, "qty": "3"}}}).
		Body.Close()

	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/orders/"+id+"/documents/delivery"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delivery note: status %d — %s", resp.StatusCode, readBody(t, resp))
	}

	raw, err := json.Marshal(decodeJSON(t, resp))
	if err != nil {
		t.Fatalf("re-encode the note: %v", err)
	}
	note := string(raw)

	for _, forbidden := range []string{
		"1234.56", "3703.68", "unit_price", "line_total", "discount", "total",
	} {
		if strings.Contains(note, forbidden) {
			t.Fatalf("the delivery note contains %q. That paper is handled by "+
				"a driver, a courier and whoever signs for the goods — none of "+
				"whom is entitled to know what the customer paid.\n%s",
				forbidden, note)
		}
	}

	// And it does carry what it is for.
	if !strings.Contains(note, "\"qty\"") {
		t.Error("a delivery note with no quantities on it is not a delivery note")
	}
}

// The picking slip is about what to PULL, so it uses the ordered quantity; the
// packing slip is about what was pulled.
func TestAPickingSlipListsWhatToPullAndAPackingSlipWhatWasPulled(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)
	raised := decodeJSON(t, f.raise(t, h, "6", "10.00"))
	id, _ := raised["id"].(string)
	lineID := f.firstLineID(t, raised)

	h.do(t, http.MethodPost, f.path("/api/v1/orders/"+id+"/pick"), f.token,
		map[string]any{"lines": []map[string]any{{"line_id": lineID, "qty": "4"}}}).
		Body.Close()

	for _, c := range []struct{ kind, qty string }{
		{"picking", "6"},
		{"packing", "4"},
	} {
		resp := h.do(t, http.MethodGet,
			f.path("/api/v1/orders/"+id+"/documents/"+c.kind), f.token, nil)
		lines, _ := decodeJSON(t, resp)["lines"].([]any)
		if len(lines) == 0 {
			t.Fatalf("%s slip has no lines", c.kind)
		}
		line, _ := lines[0].(map[string]any)
		if line["qty"] != c.qty {
			t.Errorf("%s slip says %v, want %s", c.kind, line["qty"], c.qty)
		}
	}
}

// --- B12: the wholesale minimum -------------------------------------------

// Enforced at confirmation, not when a line is added.
//
// Somebody building a quote types a quantity, then a price, then changes the
// quantity — and a rule that refused every intermediate state would interrupt
// them three times before they finished one line.
func TestAWholesaleOrderBelowTheMinimumCannotBeConfirmed(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE variant SET min_wholesale_qty = 10 WHERE id = $1`, f.variantID)
		return e
	}); err != nil {
		t.Fatalf("set a wholesale minimum: %v", err)
	}

	resp := h.do(t, http.MethodPost, f.path("/api/v1/orders"), f.token,
		map[string]any{
			"channel": "wholesale",
			"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "qty": "4", "unit_price": "10.00"},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the QUOTE should be accepted; the minimum bites on "+
			"confirmation: %s", readBody(t, resp))
	}
	id, _ := decodeJSON(t, resp)["id"].(string)

	refused := h.do(t, http.MethodPost,
		f.path("/api/v1/orders/"+id+"/advance"), f.token, nil)
	if refused.StatusCode != http.StatusConflict {
		t.Fatalf("confirming below the wholesale minimum should conflict, "+
			"got %d — %s", refused.StatusCode, readBody(t, refused))
	}
}

// A retail order is not subject to the wholesale minimum.
func TestARetailOrderIgnoresTheWholesaleMinimum(t *testing.T) {
	h := newHarness(t)
	f := seedOrders(t, h)

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE variant SET min_wholesale_qty = 10 WHERE id = $1`, f.variantID)
		return e
	}); err != nil {
		t.Fatalf("set a wholesale minimum: %v", err)
	}

	id := f.orderID(t, h)
	resp := h.do(t, http.MethodPost,
		f.path("/api/v1/orders/"+id+"/advance"), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a retail order of one should confirm freely: %s",
			readBody(t, resp))
	}
}

// --- helpers --------------------------------------------------------------

func (f *orderFixture) raise(
	t *testing.T, h *harness, qty, price string,
) *http.Response {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/orders"), f.token,
		map[string]any{
			"channel": "store",
			"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "qty": qty,
					"unit_price": price},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("raise an order: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	return resp
}

func (f *orderFixture) orderID(t *testing.T, h *harness) string {
	t.Helper()
	id, _ := decodeJSON(t, f.raise(t, h, "1", "50.00"))["id"].(string)
	return id
}

func (f *orderFixture) firstLineID(t *testing.T, order map[string]any) string {
	t.Helper()
	lines, _ := order["lines"].([]any)
	if len(lines) == 0 {
		t.Fatal("the order came back with no lines")
	}
	line, _ := lines[0].(map[string]any)
	id, _ := line["id"].(string)
	if id == "" {
		t.Fatal("the line has no id, so nothing can be picked against it")
	}
	return id
}

var _ = uuid.Nil
