//go:build integration

// The shift register, and the store-credit liability whole.
//
// Both routes are new because both screens had nothing to call.
//
// The till routes are built for a till: `GET /shifts/current` needs a token
// bound to a terminal and `GET /shifts/{sessionID}` needs an id only the till
// that opened it has ever held. So the variance on last night's drawer — the
// single signal a blind close exists to produce — was reachable from nowhere
// but that till, and only while it was still standing there.
//
// Wallets were one customer at a time, so what a business owed in store credit
// could only be found by asking about every customer in turn.
package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func TestASupervisorCanReviewLastNightsTills(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// seedShop opens a session, so there is one to find. Before this route
	// existed an ordinary browser token could not reach it at all: the current
	// route answers "This session is not bound to a terminal, so it has no
	// till to report on."
	resp := h.do(t, http.MethodGet,
		"/api/v1/shifts?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the shift register answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
	rows := decodeJSONFrom(t, resp)["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("a shop with an open session reports no shifts")
	}

	row := rows[0].(map[string]any)
	for _, field := range []string{
		"id", "session_no", "state",
		// Who and where, not just an id. A supervisor asks "who was on number
		// two last night", never "what is my session id".
		"store", "device", "opened_by", "opened_at", "opening_float",
	} {
		if _, ok := row[field]; !ok {
			t.Errorf("a shift row says nothing about %q", field)
		}
	}
}

func TestAnOpenDrawerIsNotADrawerCountedAtNothing(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/shifts?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the shift register answered %d", resp.StatusCode)
	}
	for _, raw := range decodeJSONFrom(t, resp)["data"].([]any) {
		row := raw.(map[string]any)
		if row["state"] != "open" {
			continue
		}
		// Absent, not zero. The difference decides whether a supervisor goes
		// and looks at a drawer, and a counted figure of 0.00 on a session
		// nobody has counted would send them to every till in the shop.
		for _, field := range []string{"counted_cash", "expected_cash", "variance"} {
			if _, present := row[field]; present {
				t.Errorf("an open session reports %q, and nothing has been "+
					"counted yet: %v", field, row[field])
			}
		}
	}
}

func TestTheShiftRegisterIsBehindTheSamePermissionAsTheXReport(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	// A cashier holds sales.receive_payment and not report.view. The register
	// carries the expected figure and the variance beside it, and a cashier who
	// can read those can make tonight's drawer agree with the screen — which
	// leaves the variance reading zero on every shift and defeats the blind
	// close entirely.
	resp := h.do(t, http.MethodGet,
		"/api/v1/shifts?company_id="+f.companyID.String(), f.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a cashier reading the shift register answered %d, want 403: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

func TestWhatTheBusinessOwesInStoreCreditCanBeSeenWhole(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	owed := creditedCustomer(t, h, f, "Owed Something", "50.00")
	spent := creditedCustomer(t, h, f, "Spent It All", "0.00")

	resp := h.do(t, http.MethodGet,
		"/api/v1/wallets?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the wallet list answered %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
	rows := decodeJSONFrom(t, resp)["data"].([]any)

	found := map[string]string{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		found[row["customer_id"].(string)] = row["balance"].(string)
	}

	if found[owed.String()] != "50.00" {
		t.Errorf("a customer holding 50.00 of credit reports %q",
			found[owed.String()])
	}
	// A list of every customer with a zero beside most of them is not a list of
	// what is owed, and the empty rows are the ones nobody is looking for.
	if _, present := found[spent.String()]; present {
		t.Errorf("a customer with nothing on their wallet is on the list of "+
			"what the business owes: %q", found[spent.String()])
	}
}

// creditedCustomer makes a customer and puts an amount of store credit on them.
func creditedCustomer(
	t *testing.T, h *harness, f *shopFixture, name, amount string,
) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `
			INSERT INTO customer (tenant_id, company_id, code, name)
			VALUES ($1, $2, $3, $4) RETURNING id`,
			f.tenantID, f.companyID, "C"+uuid.NewString()[:8], name).
			Scan(&id); e != nil {
			return e
		}
		if decimal.RequireFromString(amount).IsZero() {
			return nil
		}
		_, e := tx.Exec(t.Context(), `
			INSERT INTO store_credit_entry
			  (tenant_id, company_id, customer_id, amount, currency, reason)
			VALUES ($1, $2, $3, $4,
			        (SELECT base_currency FROM company WHERE id = $2), 'issued')`,
			f.tenantID, f.companyID, id, decimal.RequireFromString(amount))
		return e
	}); err != nil {
		t.Fatalf("credit %s: %v", name, err)
	}
	return id
}
