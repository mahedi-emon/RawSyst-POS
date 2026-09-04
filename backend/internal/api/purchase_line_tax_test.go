//go:build integration

package api

import (
	"testing"
)

// A purchase order line has to be able to say how it was taxed.
//
// `po_line` has stored `tax_treatment` and `tax_rate` since 0031 and
// CreateOrder has always written both, but `po_outstanding` returned neither —
// so `OrderLineView.TaxTreatment` was a field in the API contract that came
// back empty on every line of every order. Migration 0125 adds them.
//
// The consequence that made this worth fixing is not the display. `PUT
// /purchasing/orders/{id}` rewrites a draft's lines wholesale, so an editor has
// to send back what it read; reading an empty treatment and no rate, it would
// send an empty treatment and no rate, and the line would come out standard at
// zero per cent. Changing the delivery date would have changed the tax.
func TestAPurchaseLineReportsTheTaxItWasStoredWith(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	created := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(), "description": "Abaya",
				"qty": "10", "unit_cost": "20.00",
				"tax_treatment": "zero_rated", "tax_rate": "0",
			}},
		})
	if created.StatusCode != 201 {
		t.Fatalf("order: %s", readBody(t, created))
	}
	poID, _ := decodeJSON(t, created)["id"].(string)

	read := h.do(t, "GET", f.path("/api/v1/purchasing/orders/"+poID), f.token, nil)
	if read.StatusCode != 200 {
		t.Fatalf("read: %s", readBody(t, read))
	}
	lines, _ := decodeJSON(t, read)["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("want one line, got %d", len(lines))
	}
	line, _ := lines[0].(map[string]any)

	if line["tax_treatment"] != "zero_rated" {
		t.Errorf("the line came back taxed as %q, want zero_rated. An editor "+
			"reading this sends it straight back to PUT, and a zero-rated "+
			"import would be rewritten as a standard purchase.",
			line["tax_treatment"])
	}
	if _, ok := line["tax_rate"]; !ok {
		t.Error("the line carries no tax_rate at all, so a draft cannot be " +
			"edited without inventing one")
	}
}

// The rate comes from the register, not from whoever is calling.
//
// The expenses service states the rule on the same kind of document and states
// why: "a client that could state its own VAT rate could state what the return
// claims." Purchasing took one from the request body and defaulted it to zero,
// so a caller sending nothing raised orders with no tax on them at all -- and
// that tax is what the shop reclaims as input VAT.
func TestAPurchaseIsPricedFromTheRegisterNotTheRequest(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	// No tax_rate in the body at all.
	created := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(), "description": "Abaya",
				"qty": "10", "unit_cost": "20.00", "tax_treatment": "standard",
			}},
		})
	if created.StatusCode != 201 {
		t.Fatalf("order: %s", readBody(t, created))
	}
	body := decodeJSON(t, created)
	if body["tax_total"] != "30.0000" {
		t.Errorf("200 net came to %v in tax with no rate supplied, want 30.0000 "+
			"from the register. Zero here means every order raised without an "+
			"explicit rate carried no input VAT.", body["tax_total"])
	}

	// A rate that disagrees is refused rather than used or ignored.
	wrong := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(), "qty": "10",
				"unit_cost": "20.00", "tax_treatment": "standard",
				"tax_rate": "0.05",
			}},
		})
	if wrong.StatusCode != 400 {
		t.Errorf("a stated rate of 0.05 against a register holding 0.15 came "+
			"back %d, want 400. Ignoring it silently is how a caller keeps "+
			"sending last years rate and never learns.", wrong.StatusCode)
	}

	// A treatment the country does not use is refused, naming the country.
	odd := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(), "qty": "1",
				"unit_cost": "1.00", "tax_treatment": "not_a_treatment",
			}},
		})
	if odd.StatusCode != 400 {
		t.Errorf("an unknown treatment came back %d, want 400", odd.StatusCode)
	}
}

// A rate is a fraction everywhere in this system.
//
// `sales_line_rate_sane` has constrained a sales line to [0, 1) since 0018 and
// every purchasing test sends "0.15", but the purchasing tables carry no such
// constraint. "15" — the obvious thing to type for fifteen per cent — was
// accepted and multiplied: a 948 order came back as 15,168 with 14,220 of tax,
// and the buyer's next sight of that number is on an order the supplier can
// hold them to.
func TestAPurchaseRateIsAFractionNotAPercentage(t *testing.T) {
	h := newHarness(t)
	f := seedBuying(t, h)

	refused := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(), "description": "Abaya",
				"qty": "10", "unit_cost": "20.00", "tax_rate": "15",
			}},
		})
	if refused.StatusCode != 400 {
		t.Fatalf("a rate of 15 came back %d, want 400. At that rate a 200 "+
			"order is a 3,200 commitment and nothing said a word: %s",
			refused.StatusCode, readBody(t, refused))
	}

	// The fraction it was mistaken for still works, which is the point: the
	// guard is about the units, not about refusing tax.
	accepted := h.do(t, "POST", f.path("/api/v1/purchasing/orders"), f.token,
		map[string]any{
			"supplier_id": f.supplierID, "warehouse_id": f.warehouseID,
			"lines": []map[string]any{{
				"variant_id": f.variantID.String(), "description": "Abaya",
				"qty": "10", "unit_cost": "20.00", "tax_rate": "0.15",
			}},
		})
	if accepted.StatusCode != 201 {
		t.Fatalf("0.15 was refused as well: %s", readBody(t, accepted))
	}
	body := decodeJSON(t, accepted)
	if body["tax_total"] != "30.0000" {
		t.Errorf("200 net at fifteen per cent came to %v in tax, want 30.0000",
			body["tax_total"])
	}
}
