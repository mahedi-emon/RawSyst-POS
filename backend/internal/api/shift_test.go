//go:build integration

// Cash sessions and the X/Z reports drawn from them.
//
// Cash is the only tender that leaves no independent trace, so the one thing
// that makes it accountable is counting the drawer against what the system
// expected. These tests hold that comparison to account: that the expected
// figure includes everything it should and nothing it should not, that a blind
// close really is blind, and that a Z report can happen exactly once.
package api

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// The reckoning: opening float plus cash taken, less cash refunded, plus or
// minus anything else that moved through the drawer.
func TestZReportReconcilesTheDrawer(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	// Two cash sales and one on a card. The card money never enters the drawer.
	for _, amount := range []string{"115.00", "230.00"} {
		qty := "1"
		if amount == "230.00" {
			qty = "2"
		}
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), qty, "115.00", amount))
		if resp.StatusCode != 201 {
			t.Fatalf("cash sale: %s", readBody(t, resp))
		}
	}
	card := oneItemSale(f, newUUID(), "1", "115.00", "115.00")
	card["tenders"] = []map[string]any{{"method": "mada", "amount": "115.00"}}
	if resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, card); resp.StatusCode != 201 {
		t.Fatalf("card sale: %s", readBody(t, resp))
	}

	// A safe drop mid-shift: money out of the drawer that is not a refund.
	if err := h.shift.RecordMovement(ctx, f.tenantID, f.sessionID, f.userID,
		dec("-100.00"), "safe_drop", "moved to the safe at 4pm"); err != nil {
		t.Fatalf("safe drop: %v", err)
	}

	// Expected: 200 float + 345 cash sales − 100 drop = 445.
	report, err := h.shift.XReport(ctx, f.tenantID, f.sessionID)
	if err != nil {
		t.Fatalf("X report: %v", err)
	}
	if report.ExpectedCash != "445" {
		t.Errorf("expected cash = %s, want 445", report.ExpectedCash)
	}
	if report.CashTakings != "345" {
		t.Errorf("cash takings = %s, want 345", report.CashTakings)
	}
	if report.NonCashTakings != "115" {
		t.Errorf("non-cash takings = %s, want 115; card money must not be "+
			"expected in the drawer", report.NonCashTakings)
	}
	if report.InvoiceCount != 3 {
		t.Errorf("invoice count = %d, want 3", report.InvoiceCount)
	}

	// The cashier counts 445 exactly.
	z, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
		dec("445.00"), "counted twice")
	if err != nil {
		t.Fatalf("Z report: %v", err)
	}
	if z.State != "closed" {
		t.Errorf("state = %s after a Z report", z.State)
	}
	if z.Variance != "0" {
		t.Errorf("variance = %s, want 0", z.Variance)
	}
}

// A shortfall must show as a shortfall. This is the entire point of the
// exercise, and a system that quietly netted it away would be worse than one
// with no cash reconciliation at all.
func TestAShortDrawerShowsTheShortfall(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	if resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00")); resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}

	// Expected 315; only 295 in the drawer.
	z, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
		dec("295.00"), "twenty short")
	if err != nil {
		t.Fatalf("Z report: %v", err)
	}
	if z.ExpectedCash != "315" {
		t.Fatalf("expected = %s, want 315", z.ExpectedCash)
	}
	if z.Variance != "-20" {
		t.Errorf("variance = %s, want -20", z.Variance)
	}
}

// A refund takes cash back out of the drawer, so the expected balance must fall
// by what was handed over.
func TestARefundReducesTheExpectedDrawer(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	created := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if created.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, created))
	}
	invoiceID := decodeJSON(t, created)["invoice_id"].(string)

	var lineID string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id FROM sales_invoice_line WHERE invoice_id = $1`, invoiceID).
			Scan(&lineID)
	}); err != nil {
		t.Fatalf("read line: %v", err)
	}

	resp := h.do(t, "POST", "/api/v1/pos/returns", f.token, map[string]any{
		"credit_note_uuid":    newUUID().String(),
		"original_invoice_id": invoiceID,
		"issued_at":           "2026-08-15T11:00:00Z",
		"reason":              "wrong size",
		"lines":               []map[string]any{{"line_id": lineID, "qty": "1"}},
		"refunds":             []map[string]any{{"method": "cash", "amount": "115.00"}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("return: %s", readBody(t, resp))
	}

	report, err := h.shift.XReport(ctx, f.tenantID, f.sessionID)
	if err != nil {
		t.Fatalf("X report: %v", err)
	}
	// 200 float + 115 in − 115 back = 200.
	if report.ExpectedCash != "200" {
		t.Errorf("expected cash = %s, want 200", report.ExpectedCash)
	}
	if report.RefundTotal != "115" {
		t.Errorf("refunds = %s, want 115 reported separately from sales",
			report.RefundTotal)
	}
	// Refunds are reported apart from sales. Netting them would hide how much
	// was handed back, and that ratio is the most useful number on a Z report.
	if report.GrossSales != "115" {
		t.Errorf("gross sales = %s, want 115 (not netted against the refund)",
			report.GrossSales)
	}
}

// Blueprint B7: a blind close hides the expected figure from the cashier until
// the count is committed. A cashier who can see the target can make the drawer
// agree with it, and then the variance reads zero on every shift and the only
// signal there is goes dead.
func TestABlindCloseHidesTheExpectedFigure(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier") // seeded with blind_close = true
	ctx := t.Context()

	peek, err := h.shift.Peek(ctx, f.tenantID, f.sessionID)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if peek.ExpectedCash != "" {
		t.Errorf("the cashier was shown an expected figure of %s before counting",
			peek.ExpectedCash)
	}
	// The rest is still visible — a cashier needs to see the shift's takings.
	if peek.OpeningFloat != "200" {
		t.Errorf("opening float = %s, want 200", peek.OpeningFloat)
	}

	// A supervisor's X report does show it; the permission on that route is what
	// separates the two.
	x, err := h.shift.XReport(ctx, f.tenantID, f.sessionID)
	if err != nil {
		t.Fatalf("X report: %v", err)
	}
	if x.ExpectedCash != "200" {
		t.Errorf("the supervisor's X report withheld the expected figure: %q",
			x.ExpectedCash)
	}

	// After the count is committed the figure is revealed, or nobody could
	// reconcile the variance they are being asked about.
	if _, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
		dec("195.00"), "five short"); err != nil {
		t.Fatalf("close: %v", err)
	}
	after, err := h.shift.Peek(ctx, f.tenantID, f.sessionID)
	if err != nil {
		t.Fatalf("peek after close: %v", err)
	}
	if after.ExpectedCash != "200" || after.Variance != "-5" {
		t.Errorf("after closing: expected %q variance %q, want 200 and -5",
			after.ExpectedCash, after.Variance)
	}
}

// A Z report happens exactly once. A second would either double-count the
// takings or overwrite a count someone signed for.
func TestASecondZReportIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	if _, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
		dec("200.00"), "all present"); err != nil {
		t.Fatalf("first Z: %v", err)
	}

	_, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
		dec("999.00"), "trying again")
	if err == nil {
		t.Fatal("a till session was closed twice")
	}
	if !strings.Contains(err.Error(), "already been closed") {
		t.Errorf("unhelpful refusal: %v", err)
	}

	// The original count stands.
	report, err := h.shift.Peek(ctx, f.tenantID, f.sessionID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if report.CountedCash != "200" {
		t.Errorf("counted cash is now %s; the second attempt overwrote the "+
			"signed count", report.CountedCash)
	}
}

// One open session per till. Two would make "what is in this drawer" have two
// answers and every sale would have to guess which it belonged to.
func TestATillCannotHaveTwoOpenSessions(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	_, err := h.shift.Open(ctx, f.tenantID, f.deviceID, f.userID, dec("50.00"), true)
	if err == nil {
		t.Fatal("a second session was opened on a till that already had one")
	}
	if !strings.Contains(err.Error(), "already has an open session") {
		t.Errorf("unhelpful refusal: %v", err)
	}
}

// Selling with no open session must be refused: there would be no drawer anyone
// had counted into, and a difference found later could not be attributed.
func TestSellingWithNoOpenSessionIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	if _, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
		dec("200.00"), "closed for the night"); err != nil {
		t.Fatalf("close: %v", err)
	}

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 409 {
		t.Fatalf("status = %d, want 409: %s", resp.StatusCode, readBody(t, resp))
	}
	if msg := readBody(t, resp); !strings.Contains(msg, "no open session") {
		t.Errorf("the refusal does not explain what to do: %s", msg)
	}
}

// Every cash movement is explained. An unexplained hand in the till is exactly
// what this record exists to make visible.
func TestACashMovementNeedsAnExplanation(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	if err := h.shift.RecordMovement(ctx, f.tenantID, f.sessionID, f.userID,
		dec("-50.00"), "petty_cash", ""); err == nil {
		t.Error("an unexplained cash withdrawal was accepted")
	}
	if err := h.shift.RecordMovement(ctx, f.tenantID, f.sessionID, f.userID,
		decimal.Zero, "float_in", "nothing at all"); err == nil {
		t.Error("a cash movement of zero was accepted")
	}
	if err := h.shift.RecordMovement(ctx, f.tenantID, f.sessionID, f.userID,
		dec("-50.00"), "petty_cash", "taxi fare for a delivery"); err != nil {
		t.Errorf("an explained withdrawal was refused: %v", err)
	}
}

// A closed session is final. Correcting it means a new session and an
// adjustment, exactly as correcting a journal entry means a reversal.
func TestAClosedSessionCannotBeEditedEvenBySQL(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	if _, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
		dec("200.00"), "done"); err != nil {
		t.Fatalf("close: %v", err)
	}

	err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE cash_session SET counted_cash = 9999 WHERE id = $1`, f.sessionID)
		return e
	})
	if err == nil {
		t.Fatal("a closed session's count was rewritten by direct SQL")
	}

	err = h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM cash_session WHERE id = $1`, f.sessionID)
		return e
	})
	if err == nil {
		t.Fatal("a closed session was deleted")
	}
}

// One tenant cannot read another's till session.
func TestOneTenantCannotReadAnothersSession(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "cashier")
	theirs := h.seedShop(t, "cashier")

	if _, err := h.shift.XReport(t.Context(), mine.tenantID, theirs.sessionID); err == nil {
		t.Fatal("one tenant read another tenant's till session")
	}
}
