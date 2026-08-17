//go:build integration

package api

import (
	"strings"
	"testing"
)

// Editing a supplier, and taking one off the list.
//
// The properties worth protecting are both about history: a code that appears on
// an issued purchase order must not change under it, and a supplier referenced
// by orders, receipts, bills and payments must never be deletable.

func TestASuppliersDetailsCanBeCorrected(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	updated := h.do(t, "PUT",
		f.path("/api/v1/purchasing/suppliers/"+f.supplierID), f.token,
		map[string]any{
			"legal_name": "Acme Textiles LLC", "payment_terms_days": 60,
			"phone": "+966500000000", "vat_number": "311111111111113",
		})
	if updated.StatusCode != 200 {
		t.Fatalf("update: %s", readBody(t, updated))
	}

	body := decodeJSON(t, updated)
	if body["legal_name"] != "Acme Textiles LLC" {
		t.Errorf("name is %v after the correction", body["legal_name"])
	}
	if terms, _ := body["payment_terms_days"].(float64); int(terms) != 60 {
		t.Errorf("terms are %v, want 60", body["payment_terms_days"])
	}

	// And it stuck, rather than only being echoed back.
	list := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/suppliers"), f.token, nil))
	rows, _ := list["data"].([]any)
	var found bool
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["id"] == f.supplierID {
			found = true
			if row["legal_name"] != "Acme Textiles LLC" {
				t.Errorf("the list still shows %v", row["legal_name"])
			}
		}
	}
	if !found {
		t.Error("the supplier vanished from the list after being edited")
	}
}

// The code appears on purchase orders already issued. Renaming it would silently
// change what those documents refer to.
func TestASuppliersCodeCannotBeChanged(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	updated := h.do(t, "PUT",
		f.path("/api/v1/purchasing/suppliers/"+f.supplierID), f.token,
		map[string]any{
			"code": "RENAMED", "legal_name": "Acme Textiles",
			"payment_terms_days": 30,
		})
	if updated.StatusCode != 200 {
		t.Fatalf("update: %s", readBody(t, updated))
	}

	if code, _ := decodeJSON(t, updated)["code"].(string); code != "ACME" {
		t.Errorf("the code became %q; it is on issued orders and must not move", code)
	}
}

// Changing terms must not retrospectively make an old invoice overdue.
func TestChangingTermsLeavesExistingBillsAlone(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID,
			"supplier_ref": "TERMS-1", "bill_date": "2026-08-01",
			"lines": []map[string]any{{
				"description": "Stock", "qty": "1", "unit_cost": "1000.00",
			}},
		})
	if billed.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, billed))
	}
	// Decoded once: a response body can only be read through the wire the one
	// time, and reading it twice fails on a closed reader rather than returning
	// the same thing again.
	bill := decodeJSON(t, billed)
	dueBefore, _ := bill["due_date"].(string)
	billID, _ := bill["id"].(string)

	// Renegotiate to 5 days.
	if resp := h.do(t, "PUT",
		f.path("/api/v1/purchasing/suppliers/"+f.supplierID), f.token,
		map[string]any{
			"legal_name": "Acme Textiles", "payment_terms_days": 5,
		}); resp.StatusCode != 200 {
		t.Fatalf("update: %s", readBody(t, resp))
	}

	after := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/bills/"+billID), f.token, nil))
	if dueAfter, _ := after["due_date"].(string); dueAfter != dueBefore {
		t.Errorf("the existing bill's due date moved from %s to %s when the "+
			"terms were renegotiated", dueBefore, dueAfter)
	}
}

// --- Deactivating --------------------------------------------------------

func TestASupplierCanBeTakenOffTheList(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	off := h.do(t, "POST",
		f.path("/api/v1/purchasing/suppliers/"+f.supplierID+"/active"), f.token,
		map[string]any{"is_active": false})
	if off.StatusCode != 200 {
		t.Fatalf("deactivate: %s", readBody(t, off))
	}
	if active, _ := decodeJSON(t, off)["is_active"].(bool); active {
		t.Error("the supplier is still active after being deactivated")
	}

	// Gone from the default list, which is what a buyer picks from.
	list := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/suppliers"), f.token, nil))
	rows, _ := list["data"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["id"] == f.supplierID {
			t.Error("a deactivated supplier still appears in the buyer's list")
		}
	}

	// But still findable, because history references them.
	all := decodeJSON(t, h.do(t, "GET",
		f.path("/api/v1/purchasing/suppliers?include_inactive=true"), f.token, nil))
	allRows, _ := all["data"].([]any)
	var found bool
	for _, raw := range allRows {
		row, _ := raw.(map[string]any)
		if row["id"] == f.supplierID {
			found = true
		}
	}
	if !found {
		t.Error("a deactivated supplier cannot be found at all, so the orders " +
			"and bills referring to them point at nothing")
	}

	// And can come back.
	on := h.do(t, "POST",
		f.path("/api/v1/purchasing/suppliers/"+f.supplierID+"/active"), f.token,
		map[string]any{"is_active": true})
	if active, _ := decodeJSON(t, on)["is_active"].(bool); !active {
		t.Error("a supplier could not be reactivated")
	}
}

// The one that matters. An inactive supplier drops out of the lists a buyer
// works from, so an outstanding balance nobody can see is a bill that never
// gets paid.
func TestASupplierWhoIsStillOwedMoneyCannotBeHidden(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	if resp := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID,
			"supplier_ref": "OWED-1",
			"lines": []map[string]any{{
				"description": "Stock", "qty": "1", "unit_cost": "1000.00",
			}},
		}); resp.StatusCode != 201 {
		t.Fatalf("bill: %s", readBody(t, resp))
	}

	off := h.do(t, "POST",
		f.path("/api/v1/purchasing/suppliers/"+f.supplierID+"/active"), f.token,
		map[string]any{"is_active": false})
	if off.StatusCode != 409 {
		t.Fatalf("deactivating a supplier still owed money returned %d, want 409",
			off.StatusCode)
	}

	// The refusal names the amount, so the person reading it knows what to do.
	if body := readBody(t, off); !strings.Contains(body, "1000") {
		t.Errorf("the refusal does not say how much is owed: %s", body)
	}
}

// Once settled, they can be retired.
func TestASettledSupplierCanBeRetired(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	billed := h.do(t, "POST", f.path("/api/v1/purchasing/bills"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID,
			"supplier_ref": "SETTLED-1",
			"lines": []map[string]any{{
				"description": "Stock", "qty": "1", "unit_cost": "1000.00",
			}},
		})
	billID, _ := decodeJSON(t, billed)["id"].(string)

	if resp := h.do(t, "POST", f.path("/api/v1/purchasing/payments"), f.token,
		map[string]any{
			"uuid": newUUID(), "supplier_id": f.supplierID, "method": "bank",
			"allocations": []map[string]any{{"bill_id": billID, "amount": "1000.00"}},
		}); resp.StatusCode != 201 {
		t.Fatalf("payment: %s", readBody(t, resp))
	}

	if resp := h.do(t, "POST",
		f.path("/api/v1/purchasing/suppliers/"+f.supplierID+"/active"), f.token,
		map[string]any{"is_active": false}); resp.StatusCode != 200 {
		t.Errorf("a fully settled supplier could not be retired: %s",
			readBody(t, resp))
	}
}

// --- RBAC and tenancy ----------------------------------------------------

func TestEditingASupplierNeedsTheSupplierPermission(t *testing.T) {
	want := map[string]string{
		"/api/v1/purchasing/suppliers/{supplierID}":        "purchasing.manage_suppliers",
		"/api/v1/purchasing/suppliers/{supplierID}/active": "purchasing.manage_suppliers",
	}

	found := map[string]bool{}
	for _, rt := range (&Server{}).Routes() {
		expected, watched := want[rt.Pattern]
		if !watched || rt.Method == "GET" {
			continue
		}
		found[rt.Pattern] = true
		if rt.Permission != expected {
			t.Errorf("%s %s is gated on %q, want %q",
				rt.Method, rt.Pattern, rt.Permission, expected)
		}
	}
	for pattern := range want {
		if !found[pattern] {
			t.Errorf("%s is not registered", pattern)
		}
	}
}

func TestASupplierCannotBeEditedAcrossTenants(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := seedBuying(t, h)

	resp := h.do(t, "PUT",
		"/api/v1/purchasing/suppliers/"+theirs.supplierID+
			"?company_id="+mine.companyID.String(), mine.token,
		map[string]any{"legal_name": "Hijacked", "payment_terms_days": 0})
	if resp.StatusCode == 200 {
		t.Fatal("a supplier in another tenant was edited")
	}
	if resp.StatusCode != 404 && resp.StatusCode != 403 {
		t.Errorf("got %d, want 404 or 403", resp.StatusCode)
	}
}
