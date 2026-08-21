//go:build integration

// The shift routes, exercised as a till actually reaches them.
//
// shift_test.go proves the reckoning by calling the service in-process. That is
// what let the module sit unmounted with ten passing tests: the arithmetic was
// never in doubt, the join to the API was simply never made, and no test of the
// component could notice. These go through the router — real requests, real
// tokens, real permissions — so the path a shop depends on is the path under
// test.
package api

import (
	"net/http"
	"testing"
)

// The whole day, over HTTP: count in, sell, drop cash to the vault, and
// reconcile at close.
//
// Written as one test rather than four because the value is in the sequence.
// Each step depends on the one before it — a sale is impossible without the
// open, and the Z figures are only meaningful if the sale reached the same
// session the open created — and four independent tests would each have to
// rebuild that state through the service, which is the shortcut this exists to
// stop taking.
func TestATillOpensASessionSellsAndReconcilesOverHTTP(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopBeforeOpening(t, "cashier")

	// Before the drawer is counted the till cannot sell. This is the refusal
	// that made the missing routes fatal rather than merely incomplete.
	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("sale with no session: status %d, want 409 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// --- open ---------------------------------------------------------
	resp = h.do(t, "POST", "/api/v1/shifts", f.token, map[string]any{
		"opening_float": "200.00",
		"blind_close":   false,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open shift: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	opened := decodeJSON(t, resp)
	sessionID, _ := opened["id"].(string)
	if sessionID == "" {
		t.Fatalf("open returned no session id: %v", opened)
	}
	// The session carries the column's scale — "200.0000" — while a Report
	// renders the same figure as "200". Both are strings and both parse to the
	// same decimal, which is what `07-api-conventions.md` §2 asks for; the
	// difference is only how the two shapes were written. Pinned as it is
	// rather than tidied, because changing an established response format is
	// not part of mounting a route.
	if opened["opening_float"] != "200.0000" {
		t.Errorf("opening float = %v, want 200.0000", opened["opening_float"])
	}
	if opened["state"] != "open" {
		t.Errorf("state = %v, want open", opened["state"])
	}

	// --- sell ---------------------------------------------------------
	resp = h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale after opening: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// --- cash drop ----------------------------------------------------
	//
	// Negative: money leaves the drawer for the vault, so the expected figure
	// falls by exactly that much. A drop that did not move the expectation
	// would show up at close as a shortfall nobody caused.
	resp = h.do(t, "POST", "/api/v1/shifts/"+sessionID+"/cash-drop", f.token,
		map[string]any{
			"amount": "-100.00",
			"reason": "safe_drop",
			"note":   "midday drop to the safe",
		})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cash drop: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// --- close --------------------------------------------------------
	//
	// 200 float + 115 cash sale - 100 dropped = 215 expected. Counting 215
	// closes exact, which is the only outcome that proves every step above
	// landed in the same session.
	resp = h.do(t, "POST", "/api/v1/shifts/"+sessionID+"/close", f.token,
		map[string]any{"counted_cash": "215.00", "note": "counted twice"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close shift: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	z := decodeJSON(t, resp)

	if z["state"] != "closed" {
		t.Errorf("state after close = %v, want closed", z["state"])
	}
	if z["expected_cash"] != "215" {
		t.Errorf("expected cash = %v, want 215", z["expected_cash"])
	}
	if z["counted_cash"] != "215" {
		t.Errorf("counted cash = %v, want 215", z["counted_cash"])
	}
	if z["variance"] != "0" {
		t.Errorf("variance = %v, want 0 — the drawer did not reconcile", z["variance"])
	}
	if z["invoice_count"] != float64(1) {
		t.Errorf("invoice count = %v, want 1", z["invoice_count"])
	}
	if z["cash_takings"] != "115" {
		t.Errorf("cash takings = %v, want 115", z["cash_takings"])
	}
	if z["cash_movements"] != "-100" {
		t.Errorf("cash movements = %v, want -100", z["cash_movements"])
	}
}

// A second Z report is refused. The first one stands as the till's declaration
// for that shift, and a second would either double-count the takings or
// overwrite a count somebody signed for.
func TestASecondZReportIsRefusedOverHTTP(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	path := "/api/v1/shifts/" + f.sessionID.String() + "/close"

	resp := h.do(t, "POST", path, f.token, map[string]any{"counted_cash": "200.00"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first close: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, "POST", path, f.token, map[string]any{"counted_cash": "500.00"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second close: status %d, want 409 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// Blueprint B7's blind close, enforced at the routes rather than only in the
// service.
//
// A cashier who can read the expected figure before counting can make the
// drawer agree with it, and then the variance — the only signal there is —
// reads zero on every shift. Two routes exist precisely so the permission can
// separate the two readers, and this is the test that says so: the cashier's
// own view withholds the figure, and the supervisor's X report is out of the
// cashier's reach entirely.
func TestACashierCannotReadTheExpectedDrawerBeforeCounting(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier") // seeded with blind_close = true
	id := f.sessionID.String()

	resp := h.do(t, "GET", "/api/v1/shifts/"+id, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cashier peek: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	peek := decodeJSON(t, resp)
	if v, present := peek["expected_cash"]; present {
		t.Errorf("the cashier was shown an expected figure of %v before counting", v)
	}
	// The rest is still visible; a cashier needs to see the shift's takings.
	if peek["opening_float"] != "200" {
		t.Errorf("opening float = %v, want 200", peek["opening_float"])
	}

	// The X report is not merely filtered for a cashier — it is refused. A
	// cashier holds sales.receive_payment and not report.view, which is the
	// whole reason the two routes carry different permissions.
	resp = h.do(t, "GET", "/api/v1/shifts/"+id+"/x-report", f.token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cashier X report: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// A supervisor in the same shop does see it.
	supervisor := h.seedUserIn(t, f, "store_manager")
	resp = h.do(t, "GET", "/api/v1/shifts/"+id+"/x-report", supervisor, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("supervisor X report: status %d — %s",
			resp.StatusCode, readBody(t, resp))
	}
	if x := decodeJSON(t, resp); x["expected_cash"] != "200" {
		t.Errorf("the supervisor's X report withheld the expected figure: %v",
			x["expected_cash"])
	}

	// And once the count is committed the cashier may see it, or nobody could
	// reconcile the variance they are being asked to explain.
	resp = h.do(t, "POST", "/api/v1/shifts/"+id+"/close", f.token,
		map[string]any{"counted_cash": "195.00", "note": "five short"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, "GET", "/api/v1/shifts/"+id, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("peek after close: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	after := decodeJSON(t, resp)
	if after["expected_cash"] != "200" || after["variance"] != "-5" {
		t.Errorf("after closing: expected %v variance %v, want 200 and -5",
			after["expected_cash"], after["variance"])
	}
}

// A till that restarts mid-shift has to find the session it is already in.
//
// The route takes no id, so it resolves from the terminal in the token and
// cannot be aimed at another till's drawer even by a caller who knows its id.
func TestCurrentReturnsTheTillsOwnSession(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "GET", "/api/v1/shifts/current", f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("current: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["id"]; got != f.sessionID.String() {
		t.Fatalf("current session = %v, want %s", got, f.sessionID)
	}

	// Another till in the same shop has no session of its own, and must be told
	// so rather than handed the first till's.
	other := h.seedShop(t, "cashier")
	resp = h.do(t, "GET", "/api/v1/shifts/current", other.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("other till current: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["id"]; got == f.sessionID.String() {
		t.Fatal("a till was handed another terminal's session")
	}
}

// Only a registered till opens a drawer.
//
// A back-office browser session carries no terminal, and a session opened
// without one would belong to no till, be counted by nobody, and still accept
// takings. The device comes from the token, so this cannot be worked around by
// naming one in the body — there is nowhere to name it.
func TestABrowserSessionCannotOpenATillSession(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// Same user, same permissions, signed in through the ordinary login route
	// rather than on the terminal — so the token carries no device.
	browser := h.login(t, f.email)

	resp := h.do(t, "POST", "/api/v1/shifts", browser,
		map[string]any{"opening_float": "200.00"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("browser open: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, "GET", "/api/v1/shifts/current", browser, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("browser current: status %d, want 403 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// QA gate M8 on the new surface: one shop cannot reach another's drawer by
// naming its session id.
//
// Row-level security is what refuses it, and the refusal is 404 rather than
// 403 — a shop should not learn that another shop's session exists by probing
// ids.
func TestOneShopCannotReachAnothersSessionOverHTTP(t *testing.T) {
	h := newHarness(t)
	a := h.seedShop(t, "cashier")
	b := h.seedShop(t, "cashier")

	id := b.sessionID.String()

	for _, c := range []struct {
		name, method, path string
		body               any
	}{
		{"peek", "GET", "/api/v1/shifts/" + id, nil},
		{"cash drop", "POST", "/api/v1/shifts/" + id + "/cash-drop",
			map[string]any{"amount": "-10.00", "reason": "safe_drop", "note": "not mine"}},
		{"close", "POST", "/api/v1/shifts/" + id + "/close",
			map[string]any{"counted_cash": "0.00"}},
	} {
		resp := h.do(t, c.method, c.path, a.token, c.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s against another shop's session: status %d, want 404 — %s",
				c.name, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}

	// Shop B's session is untouched by any of it.
	resp := h.do(t, "GET", "/api/v1/shifts/current", b.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shop B current: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if decodeJSON(t, resp)["state"] != "open" {
		t.Fatal("shop B's session was closed by shop A")
	}
}

// Money arrives as a string, and a malformed one is refused with a sentence
// naming the field rather than being silently read as zero — which on an
// opening float would start every shift with a baseline nobody counted.
func TestAMalformedAmountIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopBeforeOpening(t, "cashier")

	for _, body := range []map[string]any{
		{"opening_float": "two hundred"},
		{"opening_float": ""},
	} {
		resp := h.do(t, "POST", "/api/v1/shifts", f.token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("open with %v: status %d, want 400 — %s",
				body, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}

	// A negative float is a different refusal, and belongs to the service.
	resp := h.do(t, "POST", "/api/v1/shifts", f.token,
		map[string]any{"opening_float": "-1.00"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("negative float: status %d, want 400 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// And an unparseable session id is refused before anything is looked up.
	resp = h.do(t, "GET", "/api/v1/shifts/not-a-uuid", f.token, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad session id: status %d, want 400 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// `/shifts/current` must not be read as `/shifts/{sessionID}` with the literal
// id "current". Chi prefers the static segment, and this pins that: if the
// precedence ever flips, the till's own-session lookup would start returning a
// 400 for an unparseable uuid.
func TestCurrentIsNotMistakenForASessionID(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "GET", "/api/v1/shifts/current", f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("current: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["id"]; got != f.sessionID.String() {
		t.Fatalf("current resolved to %v, want %s", got, f.sessionID)
	}

	// The parameterised route still works alongside it.
	resp = h.do(t, "GET", "/api/v1/shifts/"+f.sessionID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("peek by id: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// A movement with no explanation is refused. An unexplained hand in the till is
// exactly what the cash movement record exists to make visible.
func TestACashMovementNeedsAnExplanationOverHTTP(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	path := "/api/v1/shifts/" + f.sessionID.String() + "/cash-drop"

	for _, body := range []map[string]any{
		{"amount": "-50.00", "reason": "safe_drop", "note": ""},
		{"amount": "0.00", "reason": "safe_drop", "note": "nothing moved"},
	} {
		resp := h.do(t, "POST", path, f.token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("cash drop %v: status %d, want 400 — %s",
				body, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
}
