//go:build integration

// The shift as the till screen drives it.
//
// shift_http_test.go proves the routes exist and refuse what they should. These
// prove the sequence the Tauri counter actually performs — the figures the
// count pad produces, the direction the movement form applies, and the
// agreement between what the Z report says and the sales behind it. A screen
// can be correct about every request it makes and still be wrong about what the
// numbers mean, and that is the half these cover.
package api

import (
	"net/http"
	"testing"
)

// The day the count pad drives, end to end.
//
// The screen never sends a denomination tally — it counts notes and coins into
// one decimal string and sends that. This proves the strings the pad produces
// are the strings the server reconciles against, using the exact figures
// pos/src/pos/shift.test.ts asserts the pad computes:
//
//	2×500 + 3×100 + 1×50 + 7×1 + 3×0.25 = 1357.75
//
// The pad is the one place the screen multiplies, and it does so in BigInt
// halalas because 19 × 0.05 is 0.9500000000000001 in float64. A drawer counted
// that way would close a hallala out with no cause anybody could find, which is
// indistinguishable from a till that is genuinely short.
func TestACountedDrawerFromTheDenominationPadReconciles(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopBeforeOpening(t, "cashier")

	// The float, counted in as 4 × 50.
	resp := h.do(t, "POST", "/api/v1/shifts", f.token, map[string]any{
		"opening_float": "200.00",
		"blind_close":   true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	sessionID, _ := decodeJSON(t, resp)["id"].(string)

	// A cash sale that lands the closing figure on a total the pad can produce
	// from real notes and coins.
	resp = h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "1157.75", "1157.75"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// 200 + 1157.75 = 1357.75, the pad's worked example.
	resp = h.do(t, "POST", "/api/v1/shifts/"+sessionID+"/close", f.token,
		map[string]any{"counted_cash": "1357.75", "note": "counted from the pad"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	z := decodeJSON(t, resp)

	if z["expected_cash"] != "1357.75" {
		t.Errorf("expected cash = %v, want 1357.75", z["expected_cash"])
	}
	if z["counted_cash"] != "1357.75" {
		t.Errorf("counted cash = %v, want 1357.75", z["counted_cash"])
	}
	// Exact. A pad that multiplied in float64 would land a hallala out here,
	// and the variance would report a difference nobody caused.
	if z["variance"] != "0" {
		t.Errorf("variance = %v, want 0 — the counted total did not reconcile", z["variance"])
	}
}

// Money moves both ways, and the direction is what the screen derives.
//
// A cashier types "100" and picks a reason; the sign comes from the reason,
// because asking them to type "-100" for a drop and "100" for a float is how a
// shift ends up two hundred out. This proves the two directions the screen
// sends do what the screen says they will: a drop lowers the expected drawer by
// exactly its amount, a float raises it, and the two net in cash_movements.
func TestCashMovesBothWaysAndTheDrawerFollows(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier") // opens with a 200.00 float, blind close
	id := f.sessionID.String()

	supervisor := h.seedUserIn(t, f, "store_manager")

	if got := expectedDrawer(t, h, supervisor, id); got != "200" {
		t.Fatalf("expected drawer before any movement = %s, want 200", got)
	}

	// safe_drop — the screen signs this negative.
	drop := h.do(t, "POST", "/api/v1/shifts/"+id+"/cash-drop", f.token, map[string]any{
		"amount": "-150.00", "reason": "safe_drop", "note": "midday drop to the safe",
	})
	if drop.StatusCode != http.StatusNoContent {
		t.Fatalf("safe drop: status %d — %s", drop.StatusCode, readBody(t, drop))
	}
	drop.Body.Close()

	if got := expectedDrawer(t, h, supervisor, id); got != "50" {
		t.Fatalf("expected drawer after a 150 drop = %s, want 50", got)
	}

	// float_in — the screen signs this positive.
	add := h.do(t, "POST", "/api/v1/shifts/"+id+"/cash-drop", f.token, map[string]any{
		"amount": "20.00", "reason": "float_in", "note": "change for the drawer",
	})
	if add.StatusCode != http.StatusNoContent {
		t.Fatalf("float in: status %d — %s", add.StatusCode, readBody(t, add))
	}
	add.Body.Close()

	if got := expectedDrawer(t, h, supervisor, id); got != "70" {
		t.Fatalf("expected drawer after a 20 float = %s, want 70", got)
	}

	// The cashier's own view nets the movements without ever revealing the
	// expected figure this blind-close till is withholding.
	resp := h.do(t, "GET", "/api/v1/shifts/"+id, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("peek: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	peek := decodeJSON(t, resp)
	if peek["cash_movements"] != "-130" {
		t.Errorf("cash movements = %v, want -130", peek["cash_movements"])
	}
	if _, shown := peek["expected_cash"]; shown {
		t.Error("a blind-close till showed the cashier the expected figure")
	}

	resp = h.do(t, "POST", "/api/v1/shifts/"+id+"/close", f.token,
		map[string]any{"counted_cash": "70.00", "note": "counted"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if z := decodeJSON(t, resp); z["variance"] != "0" {
		t.Errorf("variance = %v, want 0", z["variance"])
	}
}

// The Z report has to agree with the sales behind it.
//
// The screen shows takings and the drawer together, so a shift where the two
// disagreed would be visibly wrong to the cashier reading it. Cash and non-cash
// are asserted separately because only one of them is in the drawer: a card
// sale is money the shop has taken and money the drawer has not, and folding
// the two together is how a till appears short by the day's card takings.
func TestTheZReportAgreesWithTheSalesBehindIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	id := f.sessionID.String()

	for _, amount := range []string{"115.00", "230.00"} {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", amount, amount))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("cash sale %s: status %d — %s", amount, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}

	card := oneItemSale(f, newUUID(), "1", "345.00", "345.00")
	card["tenders"] = []map[string]any{{"method": "mada", "amount": "345.00"}}
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, card)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("card sale: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// 200 float + 345 taken in cash. The card money is takings and is NOT in
	// the drawer, which is the distinction the screen shows as two rows.
	resp = h.do(t, "POST", "/api/v1/shifts/"+id+"/close", f.token,
		map[string]any{"counted_cash": "545.00", "note": ""})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	z := decodeJSON(t, resp)

	if z["invoice_count"] != float64(3) {
		t.Errorf("invoice count = %v, want 3", z["invoice_count"])
	}
	if z["cash_takings"] != "345" {
		t.Errorf("cash takings = %v, want 345", z["cash_takings"])
	}
	if z["non_cash_takings"] != "345" {
		t.Errorf("non-cash takings = %v, want 345", z["non_cash_takings"])
	}
	if z["gross_sales"] != "690" {
		t.Errorf("gross sales = %v, want 690 — the three sales do not tie", z["gross_sales"])
	}
	if z["variance"] != "0" {
		t.Errorf("variance = %v, want 0; the drawer holds the cash only", z["variance"])
	}
}

// A sale rung up after the Z report is refused.
//
// The counter disables its pay button once the server says there is no open
// session, and this is the refusal behind that courtesy. Worth pinning because
// the till queues sales locally: one accepted here after a close would belong
// to no counted drawer and would surface as a failed queue item at the end of
// the day, with the customer long gone.
func TestASaleAfterTheZReportIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "POST", "/api/v1/shifts/"+f.sessionID.String()+"/close", f.token,
		map[string]any{"counted_cash": "200.00", "note": ""})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("sale after close: status %d, want 409 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// And the till reports itself closed, which is exactly what the counter
	// reads to disable its pay button.
	resp = h.do(t, "GET", "/api/v1/shifts/current", f.token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("current after close: status %d, want 404 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// A shift the screen opens without a blind close shows the cashier where they
// stand as they count.
//
// The running verdict on the close form is drawn from the expected figure the
// server sends, so on a till that does not close blind the cashier sees Short,
// Over or Exact updating as they enter denominations. That is the setting doing
// what it says; the test is here because the two paths through the same screen
// differ only by a field the server may or may not send.
func TestANonBlindTillShowsTheCashierTheExpectedFigure(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopBeforeOpening(t, "cashier")

	resp := h.do(t, "POST", "/api/v1/shifts", f.token, map[string]any{
		"opening_float": "150.00",
		"blind_close":   false,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	opened := decodeJSON(t, resp)
	sessionID, _ := opened["id"].(string)
	if opened["blind_close"] != false {
		t.Fatalf("blind_close = %v, want false", opened["blind_close"])
	}

	// The cashier's own view carries the expected figure here, which on a
	// blind till it would not.
	resp = h.do(t, "GET", "/api/v1/shifts/"+sessionID, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("peek: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["expected_cash"]; got != "150" {
		t.Fatalf("expected cash on a non-blind till = %v, want 150", got)
	}
}

// expectedDrawer reads the supervisor's X report and returns the expected cash.
func expectedDrawer(t *testing.T, h *harness, token, sessionID string) string {
	t.Helper()
	resp := h.do(t, "GET", "/api/v1/shifts/"+sessionID+"/x-report", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("x-report: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	value, _ := decodeJSON(t, resp)["expected_cash"].(string)
	return value
}
