//go:build integration

// Wholesale kept out of retail's figures.
//
// B12: wholesale workflows are "kept separate from retail so retail reporting
// isn't distorted by bulk transactions". They were not. `channel` lives on
// `sales_order` and not on `sales_invoice`, and no report split on it or on the
// customer — so one bulk order swamped a day's retail figures and an owner
// comparing one week to the next was comparing noise.
//
// Derived from the customer rather than a new column: the fact already exists
// on `customer.customer_type`, and a second copy on every invoice is a second
// thing to keep in step.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// saleTo rings up one sale, optionally against a customer.
func saleTo(t *testing.T, h *harness, f *shopFixture, customer *uuid.UUID, paid string) {
	t.Helper()
	sale := oneItemSale(f, newUUID(), "1", paid, paid)
	if customer != nil {
		sale["customer_id"] = customer.String()
	}
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token, sale)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %d %s", resp.StatusCode, readBody(t, resp))
	}
}

func salesDetail(t *testing.T, h *harness, f *shopFixture, day string) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		"/api/v1/dashboard/sales?company_id="+f.companyID.String()+"&date="+day,
		f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sales detail: %d %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

// A wholesale sale is reported apart from the retail ones.
//
// The whole point: an owner looking at the day can see what the shop took over
// the counter without a single bulk order rewriting the picture.
func TestWholesaleSalesAreReportedApartFromRetail(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	wholesale := customerOfType(t, h, f, "wholesale")

	// One over the counter, one to a trade customer.
	saleTo(t, h, f, nil, "115.00")
	saleTo(t, h, f, &wholesale, "115.00")

	// The fixture's sales are issued today.
	body := salesDetail(t, h, f, "2026-08-15")

	retailCount, _ := body["retail_count"].(float64)
	wholesaleCount, _ := body["wholesale_count"].(float64)

	if int(retailCount) != 1 {
		t.Errorf("retail_count = %v, want 1", retailCount)
	}
	if int(wholesaleCount) != 1 {
		t.Errorf("wholesale_count = %v, want 1", wholesaleCount)
	}
	if body["retail_total"] == body["sales_total"] {
		t.Error("retail_total equals the whole day's sales; the split is not applied")
	}
}

// A walk-in with no customer counts as retail.
//
// Most sales have no customer on them. Treating "no customer" as anything but
// retail would put the bulk of a shop's trade in neither column.
func TestAWalkInCountsAsRetail(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	saleTo(t, h, f, nil, "115.00")

	body := salesDetail(t, h, f, "2026-08-15")
	retailCount, _ := body["retail_count"].(float64)
	wholesaleCount, _ := body["wholesale_count"].(float64)

	if int(retailCount) != 1 {
		t.Errorf("retail_count = %v, want 1 — a walk-in was not counted as retail",
			retailCount)
	}
	if int(wholesaleCount) != 0 {
		t.Errorf("wholesale_count = %v, want 0", wholesaleCount)
	}
}

// A retail-typed customer is retail, not "has a customer therefore wholesale".
func TestANamedRetailCustomerIsStillRetail(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	retail := customerOfType(t, h, f, "retail")
	saleTo(t, h, f, &retail, "115.00")

	body := salesDetail(t, h, f, "2026-08-15")
	if n, _ := body["wholesale_count"].(float64); int(n) != 0 {
		t.Errorf("wholesale_count = %v for a retail customer, want 0", n)
	}
	if n, _ := body["retail_count"].(float64); int(n) != 1 {
		t.Errorf("retail_count = %v, want 1", n)
	}
}
