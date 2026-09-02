//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/live"
)

// The socket through the REAL stack: the route table, the authentication
// middleware, the metrics wrapper and the hub.
//
// The unit tests in internal/platform/live prove the hub delivers and isolates.
// What only this can prove is that a browser can actually get one open — which
// depends on three things that live nowhere near the hub:
//
//   - the token arriving as a subprotocol, because a browser cannot set a
//     header on a WebSocket;
//   - the metrics middleware passing http.Hijacker through, because it wraps
//     every route including this one; and
//   - the request-timeout middleware exempting an upgrade, because otherwise
//     every socket would close thirty seconds in.
//
// Each of those failed silently in a way that looks like a network problem.

// dialLive opens the socket the way the browser client does.
func dialLive(
	t *testing.T, h *harness, token string, query string,
) (*websocket.Conn, error) {
	t.Helper()
	url := "ws" + h.server.URL[len("http"):] + "/api/v1/live" + query

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		// The marker, then the token. See websocketToken in identity.
		Subprotocols: []string{"rawsyst.auth", token},
	})
	return conn, err
}

func TestABrowserCanOpenTheLiveSocketWithItsToken(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	conn, err := dialLive(t, h, f.token, "")
	if err != nil {
		t.Fatalf("opening the socket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// And it carries what is published to that tenant.
	h.hub.Publish(t.Context(), f.tenantID, uuid.Nil, live.Message{
		Kind: "stock.moved",
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	var got live.Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the message is not JSON: %v", err)
	}
	if got.Kind != "stock.moved" {
		t.Fatalf("kind = %q, want stock.moved", got.Kind)
	}
}

// TestTheSocketRefusesWhoeverHasNoToken: the route is AccessAuthenticated, and
// this is what proves the middleware runs before the upgrade rather than after.
func TestTheSocketRefusesWhoeverHasNoToken(t *testing.T) {
	h := newHarness(t)

	if conn, err := dialLive(t, h, "", ""); err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("an unauthenticated caller opened the live socket")
	}
	if conn, err := dialLive(t, h, "not-a-real-token", ""); err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("an invented token opened the live socket")
	}
}

// TestNamingAnotherTenantsCompanyBuysNothing.
//
// The socket takes its tenant from the token. `company_id` is a narrowing
// filter and the only parameter a client may send, and the honest description
// of it is that naming somebody else's company is not REFUSED — for an
// unscoped owner `CanAccessCompany` answers yes to any id, and unlike every
// other route there is no following query for row-level security to refuse.
//
// What this proves is that it does not matter: the hub matches on tenant
// first, so the socket still receives its own tenant's traffic and could never
// be offered anybody else's.
func TestNamingAnotherTenantsCompanyBuysNothing(t *testing.T) {
	h := newHarness(t)
	mine := h.seedShop(t, "owner")
	theirs := h.seedShop(t, "owner")

	conn, err := dialLive(t, h, mine.token,
		"?company_id="+theirs.companyID.String())
	if err != nil {
		t.Fatalf("opening the socket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Published to the OTHER tenant, naming the very company that was asked
	// for. Then to this caller's own tenant.
	h.hub.Publish(t.Context(), theirs.tenantID, theirs.companyID,
		live.Message{Kind: "stock.moved"})
	h.hub.Publish(t.Context(), mine.tenantID, uuid.Nil,
		live.Message{Kind: "shift.closed"})

	// Delivery to one socket is a single channel and keeps its order, so the
	// first thing read tells the whole story.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("nothing arrived at all: %v", err)
	}
	var got live.Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the message is not JSON: %v", err)
	}
	if got.Kind != "shift.closed" {
		t.Fatalf("first message = %q, want shift.closed -- another tenant's "+
			"push was delivered", got.Kind)
	}
}

// TestASaleTellsTheOtherTills is design 03's prevention layer, end to end.
//
// A sale committed through the ordinary route reaches a watching socket as a
// stock delta. It is best-effort by design — nothing depends on it arriving —
// but a layer that never fires is not a layer.
func TestASaleTellsTheOtherTills(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	conn, err := dialLive(t, h, f.token, "")
	if err != nil {
		t.Fatalf("opening the socket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ringUpOneItem(t, h, f)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("no stock delta arrived: %v", err)
	}

	var got live.Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the message is not JSON: %v", err)
	}
	if got.Kind != "stock.moved" {
		t.Fatalf("kind = %q, want stock.moved", got.Kind)
	}
	if got.Payload["variant_id"] != f.variantID.String() {
		t.Fatalf("variant = %v, want %s",
			got.Payload["variant_id"], f.variantID)
	}
	// Negative: a sale takes stock away. A positive delta here would have every
	// till adding stock on every sale.
	delta, _ := got.Payload["delta"].(string)
	if d, err := decimal.NewFromString(delta); err != nil || !d.IsNegative() {
		t.Fatalf("delta = %q, want a negative quantity", delta)
	}
}

// ringUpOneItem sells a single unit through the ordinary route.
func ringUpOneItem(t *testing.T, h *harness, f *shopFixture) {
	t.Helper()
	resp := h.do(t, http.MethodPost, "/api/v1/pos/sales", f.token,
		map[string]any{
			"invoice_uuid": uuid.NewString(),
			"lines": []map[string]any{{
				"variant_id":    f.variantID.String(),
				"description":   "One item",
				"qty":           "1",
				"unit_price":    "115.00",
				"tax_treatment": "standard",
			}},
			"tenders": []map[string]any{{
				"method": "cash", "amount": "115.00",
			}},
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ringing up: %d %s", resp.StatusCode, readBody(t, resp))
	}
}
