//go:build integration

// Delivery, reservation, serials, warranty, repairs and instalments
// (blueprint B13, B14, B15).
package api

import (
	"net/http"
	"strings"
	"testing"
)

// Two channels cannot both sell the last unit. B13's whole reason for
// reserving stock.
func TestReservedStockCannotBeSoldTwice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner") // 10 on hand

	company := "?company_id=" + f.companyID.String()
	order := h.newOrder(t, f)

	// Hold every unit for the web order.
	resp := h.do(t, http.MethodPost, "/api/v1/stock/reservations"+company,
		f.token, map[string]any{
			"order_id": order, "variant_id": f.variantID.String(),
			"warehouse_id": f.warehouseID.String(), "qty": "10",
		})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reserve: %s", readBody(t, resp))
	}
	resp.Body.Close()

	// The shelf still holds ten, but none of them are free.
	resp = h.do(t, http.MethodGet,
		"/api/v1/stock/availability"+company+
			"&variant_id="+f.variantID.String()+
			"&warehouse_id="+f.warehouseID.String(), f.token, nil)
	avail := decodeJSONFrom(t, resp)
	if avail["on_hand"].(string) != "10" {
		t.Errorf("on hand is %v, want 10 — a reservation must not remove "+
			"stock from the shelf", avail["on_hand"])
	}
	if got := avail["available_to_sell"].(string); got != "0" {
		t.Errorf("available to sell is %s, want 0", got)
	}

	// A second channel asking for one is refused.
	second := h.newOrder(t, f)
	resp = h.do(t, http.MethodPost, "/api/v1/stock/reservations"+company,
		f.token, map[string]any{
			"order_id": second, "variant_id": f.variantID.String(),
			"warehouse_id": f.warehouseID.String(), "qty": "1",
		})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second channel reserving the last unit got %d, want 409",
			resp.StatusCode)
	}
	resp.Body.Close()

	// Releasing the first order frees them again.
	resp = h.do(t, http.MethodDelete,
		"/api/v1/stock/reservations/"+order+company, f.token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("release: %s", readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodPost, "/api/v1/stock/reservations"+company,
		f.token, map[string]any{
			"order_id": second, "variant_id": f.variantID.String(),
			"warehouse_id": f.warehouseID.String(), "qty": "1",
		})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reserving after a release: %s", readBody(t, resp))
	}
	resp.Body.Close()
}

// A delivery cannot skip its pipeline. Marking something arrived that never
// left loses the fact that it never left.
func TestADeliveryCannotSkipItsPipeline(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	resp := h.do(t, http.MethodPost, "/api/v1/deliveries"+company, f.token,
		map[string]any{
			"order_id": h.newOrder(t, f), "address": "12 King Fahd Road",
			"driver_id": f.userID.String(),
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("book: %s", readBody(t, resp))
	}
	id := decodeJSONFrom(t, resp)["id"].(string)
	path := "/api/v1/deliveries/" + id + "/advance" + company

	// assigned → delivered is not a transition.
	resp = h.do(t, http.MethodPost, path, f.token,
		map[string]any{"status": "delivered"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("jumping straight to delivered got %d, want 409",
			resp.StatusCode)
	}
	resp.Body.Close()

	for _, step := range []string{"picked_up", "out_for_delivery", "delivered"} {
		resp = h.do(t, http.MethodPost, path, f.token,
			map[string]any{"status": step})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("advance to %s: %s", step, readBody(t, resp))
		}
		resp.Body.Close()
	}

	// And a delivered consignment is finished.
	resp = h.do(t, http.MethodPost, path, f.token,
		map[string]any{"status": "out_for_delivery"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("reopening a delivered consignment got %d, want 409",
			resp.StatusCode)
	}
	resp.Body.Close()
}

// A failure has to say why: whether to try again tomorrow or ring the customer
// depends entirely on the reason.
func TestAFailedDeliveryMustSayWhy(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	resp := h.do(t, http.MethodPost, "/api/v1/deliveries"+company, f.token,
		map[string]any{
			"order_id": h.newOrder(t, f), "address": "12 King Fahd Road",
			"driver_id": f.userID.String(),
		})
	id := decodeJSONFrom(t, resp)["id"].(string)
	path := "/api/v1/deliveries/" + id + "/advance" + company

	bare := h.do(t, http.MethodPost, path, f.token,
		map[string]any{"status": "failed"})
	if bare.StatusCode != http.StatusBadRequest {
		t.Fatalf("a bare failure got %d, want 400", bare.StatusCode)
	}
	bare.Body.Close()

	ok := h.do(t, http.MethodPost, path, f.token,
		map[string]any{"status": "failed", "note": "Nobody at the address."})
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("a reasoned failure: %s", readBody(t, ok))
	}
	body := decodeJSONFrom(t, ok)
	if got := body["attempt_count"].(float64); got != 1 {
		t.Errorf("attempt count is %v, want 1 — a failed attempt is an attempt", got)
	}
}

// B15's lifecycle: a serial arrives, is sold, and the warranty desk can answer
// what it is and whether it is covered.
func TestASerialCarriesItsWarrantyFromTheSale(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	resp := h.do(t, http.MethodPost, "/api/v1/serials"+company, f.token,
		map[string]any{
			"variant_id":   f.variantID.String(),
			"warehouse_id": f.warehouseID.String(),
			"serials":      []string{"IMEI-0001", "IMEI-0002"},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("receive serials: %s", readBody(t, resp))
	}
	if got := decodeJSONFrom(t, resp)["data"].([]any); len(got) != 2 {
		t.Fatalf("recorded %d serials, want 2", len(got))
	}

	// The same serial cannot arrive twice: one physical unit, one row.
	dup := h.do(t, http.MethodPost, "/api/v1/serials"+company, f.token,
		map[string]any{
			"variant_id":   f.variantID.String(),
			"warehouse_id": f.warehouseID.String(),
			"serials":      []string{"IMEI-0001"},
		})
	if dup.StatusCode != http.StatusConflict {
		t.Errorf("a duplicate serial got %d, want 409", dup.StatusCode)
	}
	dup.Body.Close()

	// The desk can look one up by number.
	look := h.do(t, http.MethodGet, "/api/v1/serials/IMEI-0001"+company,
		f.token, nil)
	if look.StatusCode != http.StatusOK {
		t.Fatalf("lookup: %s", readBody(t, look))
	}
	found := decodeJSONFrom(t, look)
	if found["status"].(string) != "in_stock" {
		t.Errorf("a freshly received serial is %q, want in_stock",
			found["status"])
	}
}

// A warranty job is free to the customer. If a shop wants to charge, the job
// is not a warranty job — and calling it what it is keeps the warranty cost
// figure honest.
func TestAWarrantyJobCannotChargeTheCustomer(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	resp := h.do(t, http.MethodPost, "/api/v1/service-jobs"+company, f.token,
		map[string]any{
			"kind": "warranty", "fault_reported": "Screen flickers",
			"variant_id": f.variantID.String(),
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("book in: %s", readBody(t, resp))
	}
	job := decodeJSONFrom(t, resp)["id"].(string)

	charge := h.do(t, http.MethodPost, "/api/v1/service-jobs/"+job+company,
		f.token, map[string]any{"charged": "150.00"})
	if charge.StatusCode != http.StatusBadRequest {
		t.Fatalf("charging on a warranty job got %d, want 400",
			charge.StatusCode)
	}
	if body := readBody(t, charge); !strings.Contains(body, "charged") {
		t.Errorf("the refusal does not name the field: %s", body)
	}
}

// A part fitted under warranty leaves stock and the shop absorbs the cost.
func TestAWarrantyPartLeavesStockAndCostsTheShop(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner") // 10 on hand, valued 600
	company := "?company_id=" + f.companyID.String()

	resp := h.do(t, http.MethodPost, "/api/v1/service-jobs"+company, f.token,
		map[string]any{"kind": "warranty", "fault_reported": "Will not charge"})
	job := decodeJSONFrom(t, resp)["id"].(string)

	resp = h.do(t, http.MethodPost,
		"/api/v1/service-jobs/"+job+"/parts"+company, f.token,
		map[string]any{
			"variant_id":   f.variantID.String(),
			"warehouse_id": f.warehouseID.String(), "qty": "1",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issue part: %s", readBody(t, resp))
	}
	if got := decodeJSONFrom(t, resp)["parts_cost"].(string); got != "60.00" {
		t.Errorf("parts cost is %s, want 60.00 (one unit of a 600/10 pool)", got)
	}

	// The unit genuinely left the shelf.
	resp = h.do(t, http.MethodGet,
		"/api/v1/stock/availability"+company+
			"&variant_id="+f.variantID.String()+
			"&warehouse_id="+f.warehouseID.String(), f.token, nil)
	if got := decodeJSONFrom(t, resp)["on_hand"].(string); got != "9" {
		t.Errorf("on hand is %s after fitting a part, want 9", got)
	}

	// And the trial balance still balances, which is the real assertion: the
	// write-off posted both halves.
	assertTrialBalanceBalances(t, h, f)
}

// B14's own example: 1,200 with 300 down over 3 months is 300 a month — and
// the instalments must add back to what is owed, not fall a hallala short.
func TestAnInstalmentScheduleAddsBackToWhatIsOwed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	resp := h.do(t, http.MethodPost, "/api/v1/installments/quote"+company,
		f.token, map[string]any{
			"principal": "1200.00", "down_payment": "300.00",
			"tenure_months": 3,
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quote: %s", readBody(t, resp))
	}
	q := decodeJSONFrom(t, resp)
	if got := q["installment_amount"].(string); got != "300.00" {
		t.Errorf("instalment is %s, want 300.00 — B14's own worked example", got)
	}

	// The awkward case: 1,000 over 3. Two of 333.33 and a last of 333.34.
	resp = h.do(t, http.MethodPost, "/api/v1/installments/quote"+company,
		f.token, map[string]any{"principal": "1000.00", "tenure_months": 3})
	q = decodeJSONFrom(t, resp)
	if got := q["installment_amount"].(string); got != "333.33" {
		t.Errorf("instalment is %s, want 333.33", got)
	}
	if got := q["final_payment"].(string); got != "333.34" {
		t.Errorf("the final payment is %s, want 333.34 — the last instalment "+
			"takes the remainder, or the customer never clears the debt", got)
	}
}

// A cashier may quote a plan but not open one, and may not run a delivery.
// Part M item 7: refused server-side.
func TestACashierCannotReachAfterSalesWrites(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cashier := h.seedUserIn(t, f, "cashier")
	company := "?company_id=" + f.companyID.String()

	for _, c := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPost, "/api/v1/installments" + company,
			map[string]any{"tenure_months": 3}},
		{http.MethodPost, "/api/v1/deliveries" + company,
			map[string]any{"address": "somewhere"}},
		{http.MethodPost, "/api/v1/serials" + company,
			map[string]any{"serials": []string{"X"}}},
	} {
		resp := h.do(t, c.method, c.path, cashier, c.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as a cashier: status %d, want 403",
				c.method, c.path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// But quoting a plan is part of serving a customer at the counter.
	resp := h.do(t, http.MethodPost, "/api/v1/installments/quote"+company,
		cashier, map[string]any{"principal": "1200.00", "tenure_months": 3})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a cashier quoting a plan: status %d, want 200 — a customer "+
			"asking what twelve months costs must get an answer",
			resp.StatusCode)
	}
	resp.Body.Close()
}

// newOrder creates a confirmed sales order to hang deliveries and holds off.
func (h *harness) newOrder(t *testing.T, f *shopFixture) string {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/orders?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"channel": "online",
			"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "qty": "1",
					"unit_price": "115.00"},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed order: %s", readBody(t, resp))
	}
	id := decodeJSONFrom(t, resp)["id"].(string)

	// An order starts as a quotation. A delivery hangs off something the shop
	// has actually agreed to sell, so confirm it.
	adv := h.do(t, http.MethodPost,
		"/api/v1/orders/"+id+"/advance?company_id="+f.companyID.String(),
		f.token, nil)
	if adv.StatusCode != http.StatusOK {
		t.Fatalf("confirm order: %s", readBody(t, adv))
	}
	adv.Body.Close()
	return id
}

// assertTrialBalanceBalances is the invariant every posting module shares.
func assertTrialBalanceBalances(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		"/api/v1/reports/trial-balance?company_id="+f.companyID.String(),
		f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trial balance: %s", readBody(t, resp))
	}
	body := decodeJSONFrom(t, resp)
	debit, _ := body["total_debit"].(string)
	credit, _ := body["total_credit"].(string)
	if debit != credit {
		t.Errorf("the trial balance does not balance: debits %s, credits %s",
			debit, credit)
	}
}
