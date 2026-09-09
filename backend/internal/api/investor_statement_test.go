//go:build integration

// One investor's capital account, over a period.
//
// C3.2 asks for a statement by name. What the route returned was a bare list
// of movements, which is not a statement: a reader who asks for March gets
// three rows and no way to check the closing figure against anything, because
// what the account stood at on the first of March is only recoverable by
// fetching the whole history and adding it up themselves.
//
// So the statement carries its own opening and closing balances, and these
// tests hold the arithmetic that makes them mean something.
package api

import (
	"net/http"
	"testing"
)

// moveOn records one movement on a named day.
//
// The fixture's own `move` fixes every movement to 1 February, which is fine
// for arithmetic that ignores dates and useless for a statement, where the
// whole question is which side of the window each one falls.
func moveOn(
	t *testing.T, h *harness, f *assetFixture,
	investorID, direction, amount, on string,
) {
	t.Helper()
	resp := h.do(t, http.MethodPost, f.path("/api/v1/investors/movements"),
		f.token, map[string]any{
			"uuid": newUUID(), "investor_id": investorID,
			"direction": direction, "amount": amount,
			"moved_on": on, "money_account_id": f.cashID.String(),
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%s on %s: status %d — %s", direction, on,
			resp.StatusCode, readBody(t, resp))
	}
}

// investorStatement reads one investor's account over a window.
func investorStatement(
	t *testing.T, h *harness, f *assetFixture, token, investorID, window string,
) map[string]any {
	t.Helper()
	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/investors/"+investorID+"/statement")+window, token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("statement: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	out, _ := decodeJSON(t, resp)["data"].(map[string]any)
	if out == nil {
		t.Fatal("the statement came back as something other than an object")
	}
	return out
}

// The opening balance is everything before the window; the closing is the
// opening plus what happened inside it.
//
// Worked by hand: 100,000 in during January, 20,000 out in February, 5,000 in
// during March. A March statement opens at 80,000, takes 5,000 and closes at
// 85,000 — which is the same figure the register reports as `net`.
func TestAnInvestorStatementOpensAndClosesOnItsPeriod(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	investorID := f.addInvestor(t, h, "A partner", "investor")

	moveOn(t, h, f, investorID, "contribution", "100000.00", "2026-01-15")
	moveOn(t, h, f, investorID, "withdrawal", "20000.00", "2026-02-10")
	moveOn(t, h, f, investorID, "contribution", "5000.00", "2026-03-05")

	march := investorStatement(t, h, f, f.token, investorID,
		"&from=2026-03-01&to=2026-03-31")

	if march["opening"] != "80000.00" {
		t.Errorf("opening = %v, want 80000.00 — 100,000 in less 20,000 out "+
			"before the window", march["opening"])
	}
	if march["contributed"] != "5000.00" || march["withdrawn"] != "0.00" {
		t.Errorf("inside the window: contributed %v, withdrawn %v; want 5000.00 and 0.00",
			march["contributed"], march["withdrawn"])
	}
	if march["closing"] != "85000.00" {
		t.Errorf("closing = %v, want 85000.00", march["closing"])
	}

	movements, _ := march["movements"].([]any)
	if len(movements) != 1 {
		t.Fatalf("%d movements inside March, want 1", len(movements))
	}
	row, _ := movements[0].(map[string]any)
	if row["moved_on"] != "2026-03-05" {
		t.Errorf("the movement inside March is dated %v", row["moved_on"])
	}

	// And the whole history opens at nothing, because there is nothing before
	// the beginning.
	all := investorStatement(t, h, f, f.token, investorID, "")
	if all["opening"] != "0.00" {
		t.Errorf("an unbounded statement opens at %v, want 0.00", all["opening"])
	}
	if all["closing"] != "85000.00" {
		t.Errorf("an unbounded statement closes at %v, want 85000.00", all["closing"])
	}
	if all["currency"] == nil || all["currency"] == "" {
		t.Error("a statement of money states no currency")
	}
	if all["investor"] != "A partner" {
		t.Errorf("the statement names %v rather than the investor", all["investor"])
	}
}

// A window that ends before it starts is refused, not silently widened.
//
// A statement quietly covering all time because two dates were typed the wrong
// way round is the wrong figure presented as the right one, on a document
// somebody signs.
func TestAnInvestorStatementRefusesABackwardsPeriod(t *testing.T) {
	h := newHarness(t)
	f := seedAssets(t, h)
	investorID := f.addInvestor(t, h, "A partner", "investor")

	resp := h.do(t, http.MethodGet,
		f.path("/api/v1/investors/"+investorID+"/statement")+
			"&from=2026-03-31&to=2026-03-01", f.token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest &&
		resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a backwards period answered %d, want a refusal", resp.StatusCode)
	}

	bad := h.do(t, http.MethodGet,
		f.path("/api/v1/investors/"+investorID+"/statement")+"&from=March",
		f.token, nil)
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest &&
		bad.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an unreadable date answered %d, want a refusal", bad.StatusCode)
	}
}

// Another business's investor is not found, rather than empty.
func TestAnInvestorStatementIsConfinedToItsOwnCompany(t *testing.T) {
	h := newHarness(t)
	mine := seedAssets(t, h)
	theirs := seedAssets(t, h)
	theirInvestor := theirs.addInvestor(t, h, "Their partner", "investor")

	resp := h.do(t, http.MethodGet,
		mine.path("/api/v1/investors/"+theirInvestor+"/statement"),
		mine.token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("reading another business's investor answered %d, want 404",
			resp.StatusCode)
	}
}
