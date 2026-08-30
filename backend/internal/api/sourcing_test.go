//go:build integration

// Requisition, RFQ, quote comparison and award (blueprint B5, B5.1).
package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// sourcingShop seeds a shop with two suppliers to compare.
func sourcingShop(t *testing.T, h *harness) (*shopFixture, string, string) {
	t.Helper()
	f := h.seedShop(t, "owner")

	mk := func(code, name string) string {
		resp := h.do(t, http.MethodPost,
			"/api/v1/purchasing/suppliers?company_id="+f.companyID.String(),
			f.token, map[string]any{
				"code": code, "legal_name": name, "payment_terms_days": 30,
			})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed supplier %s: %s", code, readBody(t, resp))
		}
		return decodeJSONFrom(t, resp)["id"].(string)
	}
	return f, mk("SUP-A", "Supplier A"), mk("SUP-B", "Supplier B")
}

func (h *harness) raiseRFQ(
	t *testing.T, f *shopFixture, supplierA, supplierB string,
) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodPost,
		"/api/v1/purchasing/rfqs?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"warehouse_id": f.warehouseID.String(),
			"supplier_ids": []string{supplierA, supplierB},
			"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "qty": "100"},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("raise rfq: %s", readBody(t, resp))
	}
	return decodeJSONFrom(t, resp)
}

// quote files a reply and returns the quote id.
func (h *harness) quote(
	t *testing.T, f *shopFixture, rfqID, lineID, supplierID, unitCost string,
	extra map[string]any,
) map[string]any {
	t.Helper()
	body := map[string]any{
		"supplier_id": supplierID,
		"lines": []map[string]any{
			{"rfq_line_id": lineID, "qty": "100", "unit_cost": unitCost,
				"tax_rate": "0.15"},
		},
	}
	for k, v := range extra {
		body[k] = v
	}
	resp := h.do(t, http.MethodPost,
		"/api/v1/purchasing/rfqs/"+rfqID+"/quotes?company_id="+
			f.companyID.String(), f.token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("record quote: %s", readBody(t, resp))
	}
	return decodeJSONFrom(t, resp)
}

func lineIDOf(t *testing.T, rfq map[string]any) string {
	t.Helper()
	lines, ok := rfq["lines"].([]any)
	if !ok || len(lines) == 0 {
		t.Fatalf("the rfq came back with no lines: %v", rfq)
	}
	return lines[0].(map[string]any)["id"].(string)
}

// The whole of B5.1 in one pass: ask two suppliers, compare, award with a
// reason, and get a purchase order carrying the winning quote's prices.
func TestAwardingAQuoteRaisesThePurchaseOrder(t *testing.T) {
	h := newHarness(t)
	f, supA, supB := sourcingShop(t, h)

	rfq := h.raiseRFQ(t, f, supA, supB)
	rfqID := rfq["id"].(string)
	lineID := lineIDOf(t, rfq)

	h.quote(t, f, rfqID, lineID, supA, "12.00", nil)
	dearer := h.quote(t, f, rfqID, lineID, supB, "15.00", nil)

	// The comparison shows both, cheapest first, and names the lowest without
	// choosing it.
	resp := h.do(t, http.MethodGet,
		"/api/v1/purchasing/rfqs/"+rfqID+"/comparison?company_id="+
			f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("comparison: %s", readBody(t, resp))
	}
	cmp := decodeJSONFrom(t, resp)
	quotes := cmp["quotes"].([]any)
	if len(quotes) != 2 {
		t.Fatalf("comparison shows %d quotes, want 2", len(quotes))
	}
	if got := quotes[0].(map[string]any)["total_inclusive"].(string); got != "1380.00" {
		t.Errorf("cheapest total is %s, want 1380.00 (100 x 12 + 15%%)", got)
	}

	// Award the DEARER one, with a reason. B5.1 exists because cheapest does
	// not always win, and the system must not resist that.
	resp = h.do(t, http.MethodPost,
		"/api/v1/purchasing/rfqs/"+rfqID+"/award?company_id="+
			f.companyID.String(), f.token, map[string]any{
			"quote_id": dearer["id"].(string),
			"reason":   "Delivers in three days; A quoted six weeks.",
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("award: %s", readBody(t, resp))
	}
	awarded := decodeJSONFrom(t, resp)

	order := awarded["order"].(map[string]any)
	if got := order["total_inclusive"].(string); got != "1725.0000" {
		t.Errorf("the order totals %s, want 1725.0000 — it must carry the "+
			"WINNING quote's price, not the cheapest", got)
	}
	if got := order["status"].(string); got != "draft" {
		t.Errorf("the order is %q, want draft: an award proposes an order, "+
			"issuing it is still a deliberate act", got)
	}
	if order["supplier_id"].(string) != supB {
		t.Error("the order went to the wrong supplier")
	}

	// The reason is on the record, which is the point of the module.
	if got := awarded["rfq"].(map[string]any)["award_reason"].(string); !strings.Contains(got, "three days") {
		t.Errorf("the award reason was not kept: %q", got)
	}
}

// A losing quote is kept and marked, never deleted. B5.1 wants the archive for
// the next negotiation and for proving best-price sourcing at audit.
func TestLosingQuotesAreKeptAndNeverBecomeOrders(t *testing.T) {
	h := newHarness(t)
	f, supA, supB := sourcingShop(t, h)

	rfq := h.raiseRFQ(t, f, supA, supB)
	rfqID := rfq["id"].(string)
	lineID := lineIDOf(t, rfq)

	loser := h.quote(t, f, rfqID, lineID, supA, "12.00", nil)
	winner := h.quote(t, f, rfqID, lineID, supB, "15.00", nil)

	resp := h.do(t, http.MethodPost,
		"/api/v1/purchasing/rfqs/"+rfqID+"/award?company_id="+
			f.companyID.String(), f.token, map[string]any{
			"quote_id": winner["id"].(string), "reason": "Stock available now.",
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("award: %s", readBody(t, resp))
	}

	// Exactly one purchase order exists, for the winner.
	resp = h.do(t, http.MethodGet,
		"/api/v1/purchasing/orders?company_id="+f.companyID.String(), f.token, nil)
	orders := decodeJSONFrom(t, resp)["data"].([]any)
	if len(orders) != 1 {
		t.Fatalf("%d purchase orders exist after one award, want 1", len(orders))
	}

	// The loser is still readable through the supplier archive, marked as
	// rejected rather than removed.
	resp = h.do(t, http.MethodGet,
		"/api/v1/purchasing/suppliers/"+supA+"/quotes?company_id="+
			f.companyID.String(), f.token, nil)
	history := decodeJSONFrom(t, resp)["quotes"].([]any)
	if len(history) != 1 {
		t.Fatalf("supplier A has %d quotes on file, want 1 — a losing quote "+
			"must survive for the archive B5.1 asks for", len(history))
	}
	got := history[0].(map[string]any)
	if got["id"].(string) != loser["id"].(string) {
		t.Error("the archived quote is not the one that lost")
	}
	if got["status"].(string) != "rejected" {
		t.Errorf("the losing quote is %q, want rejected", got["status"])
	}
}

// B5.1: "quote versioning if a supplier revises". A second reply supersedes
// the first and both survive — the earlier price is exactly what an audit
// wants to see.
func TestARevisedQuoteSupersedesRatherThanOverwrites(t *testing.T) {
	h := newHarness(t)
	f, supA, supB := sourcingShop(t, h)

	rfq := h.raiseRFQ(t, f, supA, supB)
	rfqID := rfq["id"].(string)
	lineID := lineIDOf(t, rfq)

	first := h.quote(t, f, rfqID, lineID, supA, "15.00", nil)
	second := h.quote(t, f, rfqID, lineID, supA, "11.00", nil)

	if got := second["revision"].(float64); got != 2 {
		t.Errorf("the revised quote is revision %v, want 2", got)
	}

	// The comparison shows the supplier once, at the new price.
	resp := h.do(t, http.MethodGet,
		"/api/v1/purchasing/rfqs/"+rfqID+"/comparison?company_id="+
			f.companyID.String(), f.token, nil)
	quotes := decodeJSONFrom(t, resp)["quotes"].([]any)
	if len(quotes) != 1 {
		t.Fatalf("the comparison shows %d quotes, want 1 — a supplier who "+
			"revises must not appear twice", len(quotes))
	}
	if got := quotes[0].(map[string]any)["id"].(string); got != second["id"].(string) {
		t.Error("the comparison is showing the superseded quote")
	}

	// The original survives in the archive.
	resp = h.do(t, http.MethodGet,
		"/api/v1/purchasing/suppliers/"+supA+"/quotes?company_id="+
			f.companyID.String(), f.token, nil)
	history := decodeJSONFrom(t, resp)["quotes"].([]any)
	if len(history) != 2 {
		t.Fatalf("the archive holds %d quotes, want 2 — the price the "+
			"supplier first asked for is the history that matters", len(history))
	}
	var sawSuperseded bool
	for _, q := range history {
		m := q.(map[string]any)
		if m["id"].(string) == first["id"].(string) {
			sawSuperseded = m["status"].(string) == "superseded"
		}
	}
	if !sawSuperseded {
		t.Error("the first quote was not marked superseded")
	}
}

// An award without a reason is refused. This is the one field B5.1 exists to
// capture, and a blank one makes the whole record unable to answer the
// question an auditor asks.
func TestAwardingWithoutAReasonIsRefused(t *testing.T) {
	h := newHarness(t)
	f, supA, supB := sourcingShop(t, h)

	rfq := h.raiseRFQ(t, f, supA, supB)
	rfqID := rfq["id"].(string)
	lineID := lineIDOf(t, rfq)
	q := h.quote(t, f, rfqID, lineID, supA, "12.00", nil)
	_ = supB

	resp := h.do(t, http.MethodPost,
		"/api/v1/purchasing/rfqs/"+rfqID+"/award?company_id="+
			f.companyID.String(), f.token,
		map[string]any{"quote_id": q["id"].(string), "reason": "   "})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an award with a blank reason returned %d, want 400",
			resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, "award_reason") {
		t.Errorf("the refusal does not name the field: %s", body)
	}
}

// Awarding twice is refused. Without this a second award would raise a second
// purchase order for goods the shop has already committed to buy once.
func TestAnRFQCannotBeAwardedTwice(t *testing.T) {
	h := newHarness(t)
	f, supA, supB := sourcingShop(t, h)

	rfq := h.raiseRFQ(t, f, supA, supB)
	rfqID := rfq["id"].(string)
	lineID := lineIDOf(t, rfq)
	q := h.quote(t, f, rfqID, lineID, supA, "12.00", nil)
	h.quote(t, f, rfqID, lineID, supB, "13.00", nil)

	award := map[string]any{
		"quote_id": q["id"].(string), "reason": "Best price and terms.",
	}
	path := "/api/v1/purchasing/rfqs/" + rfqID + "/award?company_id=" +
		f.companyID.String()

	if first := h.do(t, http.MethodPost, path, f.token, award); first.StatusCode != http.StatusCreated {
		t.Fatalf("first award: %s", readBody(t, first))
	}
	second := h.do(t, http.MethodPost, path, f.token, award)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("a second award returned %d, want 409", second.StatusCode)
	}

	// And still exactly one order.
	resp := h.do(t, http.MethodGet,
		"/api/v1/purchasing/orders?company_id="+f.companyID.String(), f.token, nil)
	if orders := decodeJSONFrom(t, resp)["data"].([]any); len(orders) != 1 {
		t.Errorf("%d orders exist after a refused second award, want 1",
			len(orders))
	}
}

// An expired quote cannot be awarded: the supplier is no longer offering that
// price, and an order against it is a commitment they never made.
func TestAnExpiredQuoteCannotBeAwarded(t *testing.T) {
	h := newHarness(t)
	f, supA, supB := sourcingShop(t, h)

	rfq := h.raiseRFQ(t, f, supA, supB)
	rfqID := rfq["id"].(string)
	lineID := lineIDOf(t, rfq)

	stale := h.quote(t, f, rfqID, lineID, supA, "12.00",
		map[string]any{"valid_until": "2020-01-01"})
	if !stale["expired"].(bool) {
		t.Fatal("a quote valid until 2020 is not reported as expired")
	}

	resp := h.do(t, http.MethodPost,
		"/api/v1/purchasing/rfqs/"+rfqID+"/award?company_id="+
			f.companyID.String(), f.token, map[string]any{
			"quote_id": stale["id"].(string), "reason": "Cheapest on file.",
		})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("awarding an expired quote returned %d, want 409",
			resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, "expired") {
		t.Errorf("the refusal does not explain why: %s", body)
	}
}

// A supplier nobody asked cannot quote. An uninvited quote in the comparison
// is how a buyer ends up choosing between offers the shop never solicited.
func TestAnUninvitedSupplierCannotQuote(t *testing.T) {
	h := newHarness(t)
	f, supA, supB := sourcingShop(t, h)

	// A third supplier, deliberately not invited.
	resp := h.do(t, http.MethodPost,
		"/api/v1/purchasing/suppliers?company_id="+f.companyID.String(), f.token,
		map[string]any{"code": "SUP-C", "legal_name": "Supplier C",
			"payment_terms_days": 30})
	outsider := decodeJSONFrom(t, resp)["id"].(string)

	rfq := h.raiseRFQ(t, f, supA, supB)
	rfqID := rfq["id"].(string)
	lineID := lineIDOf(t, rfq)

	resp = h.do(t, http.MethodPost,
		"/api/v1/purchasing/rfqs/"+rfqID+"/quotes?company_id="+
			f.companyID.String(), f.token, map[string]any{
			"supplier_id": outsider,
			"lines": []map[string]any{
				{"rfq_line_id": lineID, "qty": "100", "unit_cost": "1.00"},
			},
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an uninvited quote returned %d, want 400", resp.StatusCode)
	}
}

// One supplier is not a comparison. B5.1 is a sourcing control, and a
// single-supplier RFQ produces a record implying a choice that never happened.
func TestAnRFQNeedsAtLeastTwoSuppliers(t *testing.T) {
	h := newHarness(t)
	f, supA, _ := sourcingShop(t, h)

	resp := h.do(t, http.MethodPost,
		"/api/v1/purchasing/rfqs?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"warehouse_id": f.warehouseID.String(),
			"supplier_ids": []string{supA},
			"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "qty": "10"},
			},
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a one-supplier rfq returned %d, want 400", resp.StatusCode)
	}
}

// A quote may not answer a line belonging to a different request. Without the
// check the comparison would silently pair unrelated items.
func TestAQuoteCannotAnswerAnotherRequestsLine(t *testing.T) {
	h := newHarness(t)
	f, supA, supB := sourcingShop(t, h)

	first := h.raiseRFQ(t, f, supA, supB)
	second := h.raiseRFQ(t, f, supA, supB)

	resp := h.do(t, http.MethodPost,
		"/api/v1/purchasing/rfqs/"+second["id"].(string)+"/quotes?company_id="+
			f.companyID.String(), f.token, map[string]any{
			"supplier_id": supA,
			"lines": []map[string]any{
				// A line from the FIRST request.
				{"rfq_line_id": lineIDOf(t, first), "qty": "1",
					"unit_cost": "1.00"},
			},
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("quoting another request's line returned %d, want 400",
			resp.StatusCode)
	}
}

// A requisition turned down must say why: "rejected" alone leaves the person
// who asked with nothing to act on.
func TestARejectedRequisitionMustSayWhy(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPost,
		"/api/v1/purchasing/requisitions?company_id="+f.companyID.String(),
		f.token, map[string]any{
			"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "qty": "50"},
			},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("raise requisition: %s", readBody(t, resp))
	}
	id := decodeJSONFrom(t, resp)["id"].(string)

	path := fmt.Sprintf(
		"/api/v1/purchasing/requisitions/%s/decision?company_id=%s",
		id, f.companyID.String())

	bare := h.do(t, http.MethodPost, path, f.token,
		map[string]any{"approve": false})
	if bare.StatusCode != http.StatusBadRequest {
		t.Fatalf("a bare rejection returned %d, want 400", bare.StatusCode)
	}

	ok := h.do(t, http.MethodPost, path, f.token,
		map[string]any{"approve": false, "note": "Ordered last week already."})
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("a reasoned rejection: %s", readBody(t, ok))
	}
	if got := decodeJSONFrom(t, ok)["status"].(string); got != "rejected" {
		t.Errorf("status is %q, want rejected", got)
	}
}

// A cashier may not ask for stock, approve a request, or award a contract.
// Blueprint Part M item 7: refused server-side, not hidden in the UI.
func TestACashierCannotReachSourcing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	cashier := h.seedUserIn(t, f, "cashier")

	company := "?company_id=" + f.companyID.String()
	for _, c := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPost, "/api/v1/purchasing/requisitions" + company,
			map[string]any{"lines": []map[string]any{
				{"variant_id": f.variantID.String(), "qty": "1"}}}},
		{http.MethodPost, "/api/v1/purchasing/rfqs" + company,
			map[string]any{"warehouse_id": f.warehouseID.String()}},
	} {
		resp := h.do(t, c.method, c.path, cashier, c.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as a cashier: status %d, want 403",
				c.method, c.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
