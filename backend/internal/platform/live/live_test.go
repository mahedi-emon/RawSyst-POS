package live

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/cache"
)

// The three properties a push channel has to have.
//
//   - What is published reaches whoever is watching.
//   - It reaches ONLY the tenant it is addressed to.
//   - A client that stops reading is dropped rather than waited for.

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// dial opens a socket against a hub and returns it with a reader.
func dial(
	t *testing.T, h *Hub, tenantID, companyID uuid.UUID,
) (*websocket.Conn, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			h.Serve(w, r, tenantID, companyID)
		}))

	conn, _, err := websocket.Dial(t.Context(),
		"ws"+server.URL[len("http"):], nil)
	if err != nil {
		server.Close()
		t.Fatalf("dialling: %v", err)
	}
	return conn, func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		server.Close()
	}
}

// waitFor spins until the hub is holding n sockets, so a test does not race
// the goroutine that registers one.
func waitFor(t *testing.T, h *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.Open() == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the hub holds %d sockets, want %d", h.Open(), n)
}

func TestWhatIsPublishedReachesWhoeverIsWatching(t *testing.T) {
	hub := NewHub(t.Context(), cache.NewMemory(), quiet())
	t.Cleanup(func() { _ = hub.Close() })

	tenant := uuid.New()
	conn, done := dial(t, hub, tenant, uuid.Nil)
	defer done()
	waitFor(t, hub, 1)

	hub.Publish(t.Context(), tenant, uuid.Nil, Message{
		Kind:    "stock.changed",
		Payload: map[string]any{"variant_id": "abc", "delta": -1},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	var got Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the message is not JSON: %v", err)
	}
	if got.Kind != "stock.changed" {
		t.Fatalf("kind = %q, want stock.changed", got.Kind)
	}
	if got.At == "" {
		t.Fatal("the message carries no time")
	}
}

// TestOneTenantsPushDoesNotReachAnother is the isolation this channel needs.
//
// A socket is bound to a tenant at the handshake, from the caller's own token.
// There is no parameter that could name another, and this proves the binding
// is actually enforced on the way out.
func TestOneTenantsPushDoesNotReachAnother(t *testing.T) {
	hub := NewHub(t.Context(), cache.NewMemory(), quiet())
	t.Cleanup(func() { _ = hub.Close() })

	mine, theirs := uuid.New(), uuid.New()
	watcher, done := dial(t, hub, theirs, uuid.Nil)
	defer done()
	waitFor(t, hub, 1)

	hub.Publish(t.Context(), mine, uuid.Nil, Message{Kind: "stock.changed"})

	// Nothing should arrive. Half a second is long enough: the delivery path
	// is a channel send in the same process.
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if _, raw, err := watcher.Read(ctx); err == nil {
		t.Fatalf("another tenant's message arrived: %s", raw)
	}
}

// TestASocketBoundToOneCompanyIgnoresAnother: an owner watching all branches
// binds to none and sees everything; a till binds to its own and does not.
func TestASocketBoundToOneCompanyIgnoresAnother(t *testing.T) {
	hub := NewHub(t.Context(), cache.NewMemory(), quiet())
	t.Cleanup(func() { _ = hub.Close() })

	tenant := uuid.New()
	branch, other := uuid.New(), uuid.New()

	conn, done := dial(t, hub, tenant, branch)
	defer done()
	waitFor(t, hub, 1)

	// Both, in this order. Delivery to one socket is a single channel and so
	// keeps its order, which means the FIRST thing read tells the whole story:
	// if the other branch's message got through, it is what arrives.
	//
	// Written this way rather than as a read that is expected to time out,
	// because a failed read closes the connection — so the two assertions
	// cannot both be made on one socket.
	hub.Publish(t.Context(), tenant, other, Message{Kind: "stock.changed"})
	hub.Publish(t.Context(), tenant, uuid.Nil, Message{Kind: "shift.closed"})

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("nothing arrived at all: %v", err)
	}
	var got Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the message is not JSON: %v", err)
	}
	if got.Kind != "shift.closed" {
		t.Fatalf("first message = %q, want shift.closed -- the other "+
			"company's push was delivered", got.Kind)
	}
}

// TestClosingASocketLetsItGo: a hub that kept every socket it had ever served
// would be a memory leak measured in a shop's opening hours.
func TestClosingASocketLetsItGo(t *testing.T) {
	hub := NewHub(t.Context(), cache.NewMemory(), quiet())
	t.Cleanup(func() { _ = hub.Close() })

	tenant := uuid.New()
	conn, done := dial(t, hub, tenant, uuid.Nil)
	waitFor(t, hub, 1)

	_ = conn.Close(websocket.StatusNormalClosure, "")
	done()
	waitFor(t, hub, 0)
}

// TestPublishingWithNoWatchersIsNotAFailure: the common case at four in the
// morning, and it must not block or panic.
func TestPublishingWithNoWatchersIsNotAFailure(t *testing.T) {
	hub := NewHub(t.Context(), cache.NewMemory(), quiet())
	t.Cleanup(func() { _ = hub.Close() })
	hub.Publish(t.Context(), uuid.New(), uuid.Nil, Message{Kind: "x"})

	// And a hub that was never built at all, which is an installation with
	// live push switched off.
	var none *Hub
	none.Publish(t.Context(), uuid.New(), uuid.Nil, Message{Kind: "x"})
	if none.Open() != 0 {
		t.Fatal("a nil hub reported open sockets")
	}
	if err := none.Close(); err != nil {
		t.Fatalf("closing a nil hub: %v", err)
	}
}
