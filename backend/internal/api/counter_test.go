//go:build integration

package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
)

// A shop outside Saudi Arabia can ring up a sale.
//
// It could not, until 0104. `sales.resolveTerminal` refused any terminal whose
// `egs_unit_id` was null and refused it in every market, so a Bangladeshi shop
// could be provisioned, set up, stocked, staffed and paired, and then met "this
// terminal has not been onboarded for e-invoicing yet" at the counter — an
// onboarding that does not exist outside the Kingdom and that its fields (a
// ZATCA CSR) could not have been filled in for.
//
// The assertion that matters is the one at the end: the sale went through AND
// no chain row was written. A sale that quietly acquired a chain would be worse
// than the refusal, because it would look like it worked.
func TestABangladeshiShopSellsWithoutAnEInvoicingUnit(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopInMarket(t, "owner", "bd", "BDT")

	invoiceUUID := newUUID()
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, invoiceUUID, "1", "115.00", "115.00"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.StatusCode, readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	invoiceID, _ := body["invoice_id"].(string)
	if invoiceID == "" {
		t.Fatalf("no invoice id in %v", body)
	}

	// No chain, because there is no obligation to be on one. `zatca_invoice`
	// having no row for this invoice IS the representation of "not on a chain";
	// a placeholder ICV would put a number there that looks like a position.
	var chained int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM zatca_invoice WHERE invoice_id = $1`,
			invoiceID).Scan(&chained)
	}); err != nil {
		t.Fatalf("count chain rows: %v", err)
	}
	if chained != 0 {
		t.Errorf("a Bangladeshi sale joined an e-invoicing chain (%d rows)", chained)
	}
}

// The Saudi refusal is unchanged.
//
// The other half of the same rule, and the one that would fail silently if
// somebody later "simplified" the market check away: where e-invoicing DOES
// apply, a terminal with no unit must still be refused before it consumes
// anything.
func TestASaudiTerminalWithNoUnitIsStillRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE device SET egs_unit_id = NULL WHERE id = $1`, f.deviceID)
		return e
	}); err != nil {
		t.Fatalf("clear the unit: %v", err)
	}

	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		t.Fatal("a Saudi terminal sold with no e-invoicing unit")
	}
}

// A person opens a counter, and the counter comes back in their token.
//
// This is the whole web-POS identity model in one test: the caller names a
// counter ONCE, against their own grants, and from then on every POS route
// reads the till from the token the server signed rather than from anything the
// request says.
func TestOpeningACounterBindsItToTheCallersOwnSession(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopInMarket(t, "owner", "bd", "BDT")

	// The person's ordinary session — no counter in it.
	userToken := h.tokenForUser(t, f)

	// A sale on it is refused, because a session with no counter is not a till.
	before := h.do(t, http.MethodPost, "/api/v1/pos/sales", userToken,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	before.Body.Close()
	if before.StatusCode == http.StatusCreated {
		t.Fatal("a session with no counter rang up a sale")
	}

	list := h.do(t, http.MethodGet, "/api/v1/pos/counters?company_id="+f.companyID.String(), userToken, nil)
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list counters = %d: %s", list.StatusCode, readBody(t, list))
	}

	open := h.do(t, http.MethodPost, "/api/v1/pos/counter-sessions", userToken,
		map[string]any{"device_id": f.deviceID.String()})
	defer open.Body.Close()
	if open.StatusCode != http.StatusOK {
		t.Fatalf("open counter = %d: %s", open.StatusCode, readBody(t, open))
	}
	body := decodeJSON(t, open)
	counterToken, _ := body["access_token"].(string)
	if counterToken == "" {
		t.Fatalf("no access token in %v", body)
	}
	// No refresh token: standing at a till is not a reason to extend how long
	// somebody stays signed in.
	if _, present := body["refresh_token"]; present {
		t.Error("opening a counter issued a refresh token")
	}

	// The same person, the same session, now able to sell.
	after := h.do(t, http.MethodPost, "/api/v1/pos/sales", counterToken,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	defer after.Body.Close()
	if after.StatusCode != http.StatusCreated {
		t.Fatalf("sale on the opened counter = %d: %s",
			after.StatusCode, readBody(t, after))
	}
}

// A counter paired to a machine does not open in a browser.
//
// The stronger authority is not overridable by the weaker one. A shop that
// deliberately locked a till to one machine has not also agreed that anybody
// with the selling permission may take that till from a laptop.
func TestAPairedCounterCannotBeOpenedFromABrowser(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopInMarket(t, "owner", "bd", "BDT")

	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE device SET binding = 'paired' WHERE id = $1`, f.deviceID)
		return e
	}); err != nil {
		t.Fatalf("pair the counter: %v", err)
	}

	userToken := h.tokenForUser(t, f)

	open := h.do(t, http.MethodPost, "/api/v1/pos/counter-sessions", userToken,
		map[string]any{"device_id": f.deviceID.String()})
	defer open.Body.Close()
	if open.StatusCode == http.StatusOK {
		t.Fatal("a browser opened a counter paired to one machine")
	}

	// And it is not offered as a choice either. An option that fails when
	// chosen teaches cashiers to keep trying things.
	list := h.do(t, http.MethodGet, "/api/v1/pos/counters?company_id="+f.companyID.String(), userToken, nil)
	defer list.Body.Close()
	if body := readBody(t, list); strings.Contains(body, f.deviceID.String()) {
		t.Errorf("a paired counter was offered as openable: %s", body)
	}
}

// Two counters in one shop sell at the same time and stay separate.
//
// The model is business -> shop -> many counters, and "many" has to mean they
// do not interfere: each keeps its own shift and its own takings. This rings a
// sale on each and checks the two landed in different cash sessions — the thing
// that decides whose drawer is short at the end of the day.
func TestTwoCountersInOneShopSellIndependently(t *testing.T) {
	h := newHarness(t)
	f := h.seedShopInMarket(t, "owner", "bd", "BDT")

	// A second counter in the same shop, opened by the same person.
	second := h.addCounter(t, f, "Till 2")

	firstSale := newUUID()
	r1 := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		oneItemSale(f, firstSale, "1", "115.00", "115.00"))
	defer r1.Body.Close()
	if r1.StatusCode != http.StatusCreated {
		t.Fatalf("till 1 sale = %d: %s", r1.StatusCode, readBody(t, r1))
	}

	secondSale := newUUID()
	r2 := h.do(t, http.MethodPost, "/api/v1/pos/sales", second.token,
		oneItemSale(f, secondSale, "1", "115.00", "115.00"))
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusCreated {
		t.Fatalf("till 2 sale = %d: %s", r2.StatusCode, readBody(t, r2))
	}

	// Different sessions, and each sale in its own. If both landed in one, a
	// Z report on either till would count the other's takings.
	var sessions int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT count(DISTINCT cash_session_id)
			FROM sales_invoice
			WHERE uuid IN ($1, $2)`, firstSale, secondSale).Scan(&sessions)
	}); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 2 {
		t.Errorf("two counters' sales share %d cash session(s), want 2", sessions)
	}
}

// --- fixtures --------------------------------------------------------------

// seedShopInMarket is seedShop in a named market: stocked, mid-shift, with a
// counter whose token is already device-bound.
func (h *harness) seedShopInMarket(
	t *testing.T, roleKey, country, currency string,
) *shopFixture {
	t.Helper()
	f := h.seedShopBeforeOpeningIn(t, roleKey, country, currency)
	h.openTill(t, f)
	f.token = h.tokenForDevice(t, f)
	return f
}

// openTill counts a drawer in, which a counter needs before it can sell.
func (h *harness) openTill(t *testing.T, f *shopFixture) {
	t.Helper()
	session, err := h.shift.Open(t.Context(), f.tenantID, f.deviceID, f.userID,
		decimal.RequireFromString("200.00"), true)
	if err != nil {
		t.Fatalf("open till session: %v", err)
	}
	f.sessionID = session.ID
}

// tokenForUser is the caller's ORDINARY session: a person, no counter.
//
// The distinction this whole file is about. A browser holds one of these, and
// it cannot ring up a sale until it has asked for a counter.
func (h *harness) tokenForUser(t *testing.T, f *shopFixture) string {
	t.Helper()
	token, _, err := h.tokens.IssueAccess(actor.Actor{
		UserID:   f.userID,
		TenantID: f.tenantID,
	})
	if err != nil {
		t.Fatalf("issue user token: %v", err)
	}
	return token
}

// addCounter registers a second till in the same shop over the real route and
// opens its drawer, so the two can be used side by side.
//
// Through HTTP rather than the service, because registering a counter with no
// e-invoicing unit is exactly what used to be refused, and the refusal was on
// the way in.
func (h *harness) addCounter(t *testing.T, f *shopFixture, label string) *shopFixture {
	t.Helper()

	resp := h.do(t, http.MethodPost, "/api/v1/devices?company_id="+f.companyID.String(), h.tokenForUser(t, f),
		map[string]any{
			"store_id":       f.storeID.String(),
			"terminal_label": label,
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("register %s = %d: %s", label, resp.StatusCode, readBody(t, resp))
	}

	created := decodeJSON(t, resp)
	id, _ := created["id"].(string)
	deviceID, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("bad device id %q in %v", id, created)
	}

	// A copy of the fixture pointing at the new counter. Everything else — the
	// shop, the stock, the chart, the person — is shared, which is the point:
	// two counters in ONE shop.
	second := *f
	second.deviceID = deviceID
	h.openTill(t, &second)
	second.token = h.tokenForDevice(t, &second)
	return &second
}
