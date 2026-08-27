//go:build integration

// The blind close, re-derived from what "blind" has to mean.
//
// Blueprint B7 asks for a blind close and design 11 §9 draws the shift as
// "sales tracked silently". The control they describe is a single comparison:
// the cashier says what is in the drawer WITHOUT being told what should be, and
// the difference between the two is the only independent evidence that cash was
// handled honestly. Cash is the one tender nothing else corroborates — a card
// sale has an acquirer behind it and a transfer has a bank — so if that
// comparison is compromised there is nothing else to fall back on.
//
// A cashier who is short by 200 and can see that 3,412.50 is expected puts 200
// of their own money in, or takes 200 fewer notes than they meant to, and the
// variance reads zero. It reads zero on every shift after that too, and a
// number that is always zero carries no information at all. So the test that
// matters is not "is the total hidden" but "can the total be worked out from
// what is shown", and those are different questions.
//
// # What was wrong
//
// Only expected_cash was withheld. cash_session_expected is defined as
//
//	expected = opening_float + cash_takings + cash_movements
//
// and all three addends were returned to the cashier by the same response — the
// POS shift panel listed them one under the other as "Opening float", "Cash
// takings" and "Cash moved", under a comment explaining that the expected
// drawer was deliberately not shown. Three numbers on a screen and one addition
// is not a blind close.
//
// non_cash_takings closed the second route to the same figure: gross sales less
// refunds less non-cash takings is the cash takings exactly, for any shop that
// sells only for cash and card.
//
// # How these tests are written
//
// They assert against the DEFINITION rather than against a list of field names.
// The definition is read out of the database — cash_session_expected is the
// function the Z report is measured with — and the cashier's response is
// searched for any combination of what it returned that reproduces it. A test
// naming the three fields would pass again the day a fourth is added.
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// peekAsCashier is the cashier's own view of their shift: GET /shifts/{id},
// which carries sales.receive_payment, the permission a cashier holds.
func peekAsCashier(t *testing.T, h *harness, f *shopFixture, sessionID string) map[string]any {
	t.Helper()
	resp := h.do(t, "GET", "/api/v1/shifts/"+sessionID, f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cashier peek: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	return decodeJSON(t, resp)
}

// expectedFromTheDatabase is the figure the Z report will actually be measured
// against, taken from the function that defines it rather than from a response.
func expectedFromTheDatabase(
	t *testing.T, h *harness, f *shopFixture, sessionID string,
) decimal.Decimal {
	t.Helper()
	var expected decimal.Decimal
	if err := h.pool.TxAsTenant(context.Background(), f.tenantID,
		func(tx pgx.Tx) error {
			return tx.QueryRow(context.Background(),
				`SELECT cash_session_expected($1)`, sessionID).Scan(&expected)
		}); err != nil {
		t.Fatalf("reading the expected drawer: %v", err)
	}
	return expected
}

// moneyFieldsOf pulls every decimal-looking value out of a response.
//
// By value rather than by name, because the question is what a cashier can
// compute and a cashier does not care what a field is called.
func moneyFieldsOf(t *testing.T, body map[string]any) map[string]decimal.Decimal {
	t.Helper()
	out := map[string]decimal.Decimal{}
	for key, raw := range body {
		text, ok := raw.(string)
		if !ok {
			continue
		}
		value, err := decimal.NewFromString(text)
		if err != nil {
			continue // a date, a state, an identifier
		}
		out[key] = value
	}
	return out
}

// THE DEFECT THIS FILE FOUND.
//
// A shift with a float, cash sales, a card sale and a safe drop — an ordinary
// afternoon, and enough that the expected drawer is not something a cashier
// could guess. Every subset of the figures the response hands back is then
// added up and compared with what the Z report will use.
//
// Before the fix the subset {opening_float, cash_takings, cash_movements} hit
// it exactly, and so did {gross_sales, −refund_total, −non_cash_takings,
// opening_float}. The response was withholding a total while publishing its
// parts.
func TestACashierCannotAddUpTheDrawerTheyAreAboutToCountBlind(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier") // 200.00 float, blind_close = true
	id := f.sessionID.String()

	// Two cash sales, so the takings are not simply one invoice total.
	for _, paid := range []string{"115.00", "57.50"} {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", paid, paid))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("cash sale of %s: %s", paid, readBody(t, resp))
		}
		resp.Body.Close()
	}

	// A card sale, which is takings and is not in the drawer.
	card := oneItemSale(f, newUUID(), "1", "230.00", "230.00")
	card["tenders"] = []map[string]any{{"method": "mada", "amount": "230.00"}}
	if resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, card); resp.StatusCode !=
		http.StatusCreated {
		t.Fatalf("card sale: %s", readBody(t, resp))
	}

	drop := h.do(t, "POST", "/api/v1/shifts/"+id+"/cash-drop", f.token,
		map[string]any{
			"amount": "-100.00", "reason": "safe_drop", "note": "afternoon drop",
		})
	if drop.StatusCode != http.StatusNoContent {
		t.Fatalf("safe drop: status %d — %s", drop.StatusCode, readBody(t, drop))
	}
	drop.Body.Close()

	expected := expectedFromTheDatabase(t, h, f, id)
	// 200 float + 115 + 57.50 cash − 100 dropped. Stated by hand so a change in
	// the SQL that made the function agree with a broken response would not
	// make this test agree with both.
	if want := dec("272.50"); !expected.Equal(want) {
		t.Fatalf("the drawer expects %s, and this shift was built to expect %s; "+
			"the arithmetic under test has moved", expected, want)
	}

	peek := peekAsCashier(t, h, f, id)
	if _, shown := peek["expected_cash"]; shown {
		t.Fatalf("the cashier was handed the expected drawer outright: %v",
			peek["expected_cash"])
	}

	fields := moneyFieldsOf(t, peek)
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}

	// Every figure is left out, added or subtracted: 3^n combinations, walked
	// as base-3 digits. Subtraction is as available to a cashier as addition,
	// and the second route to the drawer needed one.
	//
	// The bound is asserted rather than assumed. A blind peek returns five
	// figures today, which is 243 combinations; the walk is exponential, so a
	// response that grew to twenty fields would turn this test into a hang that
	// somebody would eventually delete instead of read.
	if len(names) > 12 {
		t.Fatalf("the cashier's view now returns %d money figures (%v), which "+
			"is too many to search exhaustively. Withhold more, or narrow this "+
			"walk deliberately rather than by accident.", len(names), names)
	}

	combinations := 1
	for range names {
		combinations *= 3
	}
	for c := 0; c < combinations; c++ {
		sum := decimal.Zero
		used := []string{}
		n := c
		for _, name := range names {
			switch n % 3 {
			case 1:
				sum = sum.Add(fields[name])
				used = append(used, "+"+name)
			case 2:
				sum = sum.Sub(fields[name])
				used = append(used, "-"+name)
			}
			n /= 3
		}
		if len(used) == 0 {
			continue // the empty sum is zero and proves nothing
		}
		if sum.Equal(expected) {
			t.Fatalf("a cashier on a blind-close till can reach the expected "+
				"drawer of %s from the figures they were shown: %v. The close "+
				"is not blind, and the variance it produces is worthless.",
				expected, used)
		}
	}
}

// The withholding is only while the count is outstanding.
//
// Once the drawer is counted and the Z report signed, hiding the figures would
// stop the cashier reconciling the very variance they are being asked to
// explain — and there is nothing left to game, because the count is committed.
func TestOnceTheCountIsCommittedTheCashierSeesEverything(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	id := f.sessionID.String()

	resp := h.do(t, "POST", "/api/v1/shifts/"+id+"/close", f.token,
		map[string]any{"counted_cash": "195.00", "note": "five short"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	after := peekAsCashier(t, h, f, id)
	for _, field := range []string{
		"expected_cash", "cash_takings", "non_cash_takings", "cash_movements",
	} {
		if _, shown := after[field]; !shown {
			t.Errorf("after the Z report the cashier still cannot see %q, so "+
				"they cannot reconcile the variance they are being asked about",
				field)
		}
	}
	if after["variance"] != "-5" {
		t.Errorf("variance = %v, want -5", after["variance"])
	}
}

// A till whose owner turned the blind close OFF sees everything, always.
//
// Blind close is a shop's policy rather than this product's opinion. A small
// shop where the owner is the cashier gains nothing from hiding a number from
// themselves, and the flag exists so they need not.
func TestATillThatIsNotBlindShowsTheCashierTheirTakings(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopBeforeOpening(t, "cashier")

	session, err := h.shift.Open(t.Context(), f.tenantID, f.deviceID, f.userID,
		dec("200.00"), false)
	if err != nil {
		t.Fatalf("open a till that closes in the open: %v", err)
	}

	peek := peekAsCashier(t, h, f, session.ID.String())
	for _, field := range []string{
		"expected_cash", "cash_takings", "non_cash_takings", "cash_movements",
	} {
		if _, shown := peek[field]; !shown {
			t.Errorf("a till with the blind close switched off withheld %q", field)
		}
	}
}

// A supervisor's X report is never filtered.
//
// The two routes carry different permissions for exactly this reason:
// sales.receive_payment gets the cashier's view, report.view gets the
// reckoning. Filtering the supervisor's copy as well would leave nobody able
// to check a till mid-shift, which is what an X report is for.
func TestTheSupervisorsXReportIsNotWithheldFromEver(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	supervisor := h.seedUserIn(t, f, "store_manager")

	resp := h.do(t, "GET",
		"/api/v1/shifts/"+f.sessionID.String()+"/x-report", supervisor, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("X report: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	x := decodeJSON(t, resp)
	for _, field := range []string{
		"expected_cash", "cash_takings", "non_cash_takings", "cash_movements",
	} {
		if _, shown := x[field]; !shown {
			t.Errorf("the supervisor's X report withheld %q", field)
		}
	}
	if x["expected_cash"] != "200" {
		t.Errorf("expected_cash on the X report = %v, want 200", x["expected_cash"])
	}
}
