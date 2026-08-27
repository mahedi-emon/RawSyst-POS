//go:build integration

// A Z report has to still be true a second after it is signed.
//
// The Z report is one comparison: what the drawer holds against what it should
// hold. Design 11 §9 calls it "the definitive daily reconciliation record", and
// blueprint C8 makes it the record the Owner relies on. Its whole value comes
// from the two sides being measured over the same set of events.
//
// cash_session freezes expected_cash at close and recomputes the takings live
// on every read. That is deliberate — the frozen figure is what somebody signed
// for, and a later correction to history must not silently change what was
// reconciled on the night — and it means anything that lands in the session
// AFTER the close appears on one side of the comparison and not the other.
//
// # What was wrong
//
// Nothing stopped it. Close took FOR UPDATE on the session row; the sale path
// and the cash-movement path read the state with no lock at all, from their own
// snapshot. A sale already in flight — a slow card authorisation, a batch of
// offline invoices being replayed by sync — read 'open', waited only on the
// foreign key's FOR KEY SHARE (which does not conflict with a plain UPDATE of a
// non-key column), and committed into a session that had been closed and
// reconciled in between.
//
// The Z report then showed the sale in cash_takings and not in expected_cash,
// and the drawer read over by exactly that sale. The cashier is the one asked
// to explain it.
//
// These tests drive the race directly rather than hoping to hit it: the close
// and the write are held open in two transactions against the real database, in
// the order that used to lose.
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// heldOpenSession starts a transaction that has closed the session and not yet
// committed, exactly as Close does. The returned function commits it.
//
// TxAsTenant runs its callback inside a transaction it owns, so the close is
// driven statement by statement here instead: the point is to hold the lock,
// which a helper that commits for you cannot do.
func heldOpenClose(
	t *testing.T, h *harness, f *shopFixture, sessionID string,
) (release func()) {
	t.Helper()
	ctx := context.Background()

	conn, err := h.pool.Raw().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Release()
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.tenant_id', $1, true)`, f.tenantID.String()); err != nil {
		t.Fatalf("scope the transaction to the tenant: %v", err)
	}

	var state string
	if err := tx.QueryRow(ctx,
		`SELECT state FROM cash_session WHERE id = $1 FOR UPDATE`, sessionID).
		Scan(&state); err != nil {
		t.Fatalf("lock the session: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE cash_session
		SET state = 'closed', closed_at = now(), closed_by = $2,
		    counted_cash = 200, expected_cash = cash_session_expected($1),
		    variance = 200 - cash_session_expected($1)
		WHERE id = $1`, sessionID, f.userID); err != nil {
		t.Fatalf("close the session: %v", err)
	}

	return func() {
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit the close: %v", err)
		}
		conn.Release()
	}
}

// waitUntilSomethingIsBlockedOnTheSession waits for another connection to be
// stuck on a lock, and fails the test if nothing ever is.
//
// Without it these tests prove nothing. Starting a goroutine and committing the
// close immediately is a race between the test and its own subject: the
// goroutine may not have reached its first statement, in which case it reads
// 'closed' from a committed row, is refused for the ordinary reason, and the
// test passes whether or not the defect is present. This waits for the other
// connection to actually be WAITING on the row before letting go of it, so the
// window under test is the one that used to lose.
func waitUntilSomethingIsBlockedOnTheSession(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var blocked int
		if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT count(*) FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND pid <> pg_backend_pid()`).Scan(&blocked)
		}); err != nil {
			t.Fatalf("looking for a blocked connection: %v", err)
		}
		if blocked > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("nothing ever waited on the closed session's row, so the write " +
		"under test never reached the window this is about: it either finished " +
		"before the close or has not started")
}

// THE DEFECT THIS FILE FOUND, on the cash-movement path.
//
// A supervisor takes cash to the safe at the same moment the cashier closes the
// till. Before the fix the movement landed in the closed session: the Z report
// listed it under cash_movements and the frozen expected_cash knew nothing
// about it, so the drawer read short by the whole drop and a cashier who had
// done nothing wrong was 500 down on paper.
func TestACashDropCannotLandInASessionThatIsBeingClosed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	id := f.sessionID.String()

	release := heldOpenClose(t, h, f, id)

	// The movement is attempted while the close is still uncommitted, so it
	// must block on the session row rather than reading a stale 'open'.
	type outcome struct{ err error }
	done := make(chan outcome, 1)
	go func() {
		done <- outcome{h.shift.RecordMovement(context.Background(),
			f.tenantID, f.sessionID, f.userID,
			dec("-500.00"), "safe_drop", "to the safe")}
	}()

	waitUntilSomethingIsBlockedOnTheSession(t, h)
	release()

	got := <-done
	if got.err == nil {
		t.Fatal("a cash drop landed in a session that had just been closed and " +
			"reconciled; its Z report now shows a movement the expected cash " +
			"knows nothing about")
	}
	if !strings.Contains(got.err.Error(), "closed") {
		t.Errorf("the refusal does not say the session is closed: %v", got.err)
	}

	assertTheZReportStillReconciles(t, h, f, id)
}

// THE SAME DEFECT on the selling path, which is the one that costs money.
//
// A sale committing a moment after the close attaches its cash tender to a
// session whose expected figure was fixed without it.
func TestASaleCannotLandInASessionThatIsBeingClosed(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	id := f.sessionID.String()

	release := heldOpenClose(t, h, f, id)

	done := make(chan int, 1)
	go func() {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
		resp.Body.Close()
		done <- resp.StatusCode
	}()

	waitUntilSomethingIsBlockedOnTheSession(t, h)
	release()

	if status := <-done; status == http.StatusCreated {
		t.Fatal("a sale landed in a session that had just been closed and " +
			"reconciled; the Z report shows its takings and not its cash")
	}

	assertTheZReportStillReconciles(t, h, f, id)
}

// The other order, which must not refuse anybody.
//
// A sale that is already in flight when the cashier reaches for the close
// button is a legitimate sale. It must be counted, not rejected — so the close
// waits for it and the expected figure includes it.
func TestASaleAlreadyInFlightIsCountedByTheZReportThatFollowsIt(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier") // 200.00 float

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sale: %s", readBody(t, resp))
	}
	resp.Body.Close()

	report, err := h.shift.Close(t.Context(), f.tenantID, f.sessionID, f.userID,
		dec("315.00"), "counted")
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if report.ExpectedCash != "315" {
		t.Errorf("expected cash = %q, want 315 — the float of 200 and the sale "+
			"of 115 that had already committed", report.ExpectedCash)
	}
	if report.Variance != "0" {
		t.Errorf("variance = %q, want 0", report.Variance)
	}
}

// The database refuses it too, not only the service.
//
// A lock protects the code paths that take it. A constraint protects the table,
// which is what a fixture, an import or a hand-written statement meets. A Z
// report that has been signed must not be falsifiable by anything.
func TestTheDatabaseItselfRefusesToWriteIntoAClosedSession(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := context.Background()

	if _, err := h.shift.Close(ctx, f.tenantID, f.sessionID, f.userID,
		dec("200.00"), "counted"); err != nil {
		t.Fatalf("close: %v", err)
	}

	err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO cash_movement
			  (tenant_id, session_id, amount, reason, note, recorded_by)
			VALUES ($1,$2,-50,'safe_drop','written straight to the table',$3)`,
			f.tenantID, f.sessionID, f.userID)
		return e
	})
	if err == nil {
		t.Fatal("SQL wrote a cash movement into a closed session, so a signed " +
			"Z report can be falsified after the fact")
	}

	err = h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var invoiceID string
		e := tx.QueryRow(ctx, `
			SELECT id::text FROM sales_invoice
			WHERE cash_session_id IS NULL LIMIT 1`).Scan(&invoiceID)
		if errors.Is(e, pgx.ErrNoRows) {
			// Nothing to move, and inventing one would prove less than the
			// movement case above already does.
			return nil
		}
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx,
			`UPDATE sales_invoice SET cash_session_id = $2 WHERE id = $1`,
			invoiceID, f.sessionID)
		if e == nil {
			return errors.New("an invoice was moved into a closed session by SQL")
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
}

// assertTheZReportStillReconciles checks the one thing the whole session is
// for: the two sides of the comparison describe the same events.
//
//	counted − expected == variance, and
//	expected == opening float + cash takings + cash movements
//
// The second is the definition of cash_session_expected. If a write slipped in
// after the close, the frozen expected figure and the live takings disagree and
// this is where it shows.
func assertTheZReportStillReconciles(
	t *testing.T, h *harness, f *shopFixture, sessionID string,
) {
	t.Helper()
	ctx := context.Background()

	var opening, cash, movements, expected, counted, variance decimal.Decimal
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT opening_float, cash_takings, cash_movements,
			       expected_cash, counted_cash, variance
			FROM cash_session_report($1)`, sessionID).
			Scan(&opening, &cash, &movements, &expected, &counted, &variance)
	}); err != nil {
		t.Fatalf("read the Z report: %v", err)
	}

	if derived := opening.Add(cash).Add(movements); !derived.Equal(expected) {
		t.Errorf("the Z report does not reconcile: float %s + takings %s + "+
			"movements %s = %s, and the signed expected figure is %s. Something "+
			"landed in the session after it was closed.",
			opening, cash, movements, derived, expected)
	}
	if !counted.Sub(expected).Equal(variance) {
		t.Errorf("counted %s − expected %s = %s, and the recorded variance is %s",
			counted, expected, counted.Sub(expected), variance)
	}
}
