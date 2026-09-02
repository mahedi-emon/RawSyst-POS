// Package live pushes changes to whoever is watching (design 03, design 00).
//
// # What this is for
//
// Design 03 is explicit about the limit: a till that is offline cannot be
// prevented from overselling, and the product chooses accurate DETECTION over
// false confidence. But while a till is online, a stock delta broadcast in
// near-real-time shrinks the window for a concurrent oversell from hours to
// seconds. That is the whole value, and it is a prevention layer rather than a
// correctness guarantee — nothing here is allowed to become one.
//
// It also carries what a back office wants without polling: a notification
// arriving, a shift closing, an approval waiting.
//
// # Nothing here is a source of truth
//
// A message is best-effort. A browser that missed one because its socket was
// reconnecting is not wrong, it is behind, and its next read of the actual
// endpoint corrects it. Anything that MUST arrive goes through the job queue
// and the database, which is why the sync protocol does not live here.
//
// This is why a slow client is dropped rather than waited for: one browser on
// hotel wifi must not hold a broadcast that thirty tills are waiting on.
//
// # Fan-out across processes
//
// One process holds its own sockets. Two processes hold half each, and a stock
// delta committed on one has to reach the tills connected to the other — so
// every broadcast also goes onto the cache's pub/sub channel, and each process
// delivers what arrives there to its own sockets. With the in-memory cache
// that is a local fan-out and still correct, because there is only one
// process.
//
// # Tenant isolation
//
// A socket belongs to exactly one tenant, fixed at the handshake from the
// caller's own token, and a message is addressed to a tenant. There is no way
// to subscribe to another one: the topic is not something the client sends.
package live

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/cache"
)

// channel is the cache pub/sub channel every process listens on.
const channel = "rawsyst.live"

// Message is what goes down a socket.
type Message struct {
	// Kind is what happened: stock.changed, notification.new, shift.closed.
	Kind string `json:"kind"`
	// Payload is the detail, already shaped for the client.
	//
	// Deliberately small. A stock delta says which variant and which store and
	// by how much; it does not carry the product, the price or the sale. A
	// client that wants more reads the endpoint, where row-level security
	// still applies — a push is a nudge, not an authorisation decision.
	Payload map[string]any `json:"payload,omitempty"`
	At      string         `json:"at"`
}

// envelope is Message with its address, for the cross-process hop.
type envelope struct {
	TenantID uuid.UUID `json:"tenant_id"`
	// CompanyID narrows it further; uuid.Nil means the whole tenant.
	CompanyID uuid.UUID `json:"company_id,omitempty"`
	Message   Message   `json:"message"`
}

// Hub holds the sockets this process is serving.
type Hub struct {
	cache cache.Cache
	log   *slog.Logger

	mu      sync.RWMutex
	clients map[*client]struct{}

	// stop ends the subscription and every writer.
	stop   chan struct{}
	closed sync.Once
}

type client struct {
	conn      *websocket.Conn
	tenantID  uuid.UUID
	companyID uuid.UUID
	// send is buffered. A client that cannot keep up fills it and is dropped;
	// see the package note on why waiting is not an option.
	send chan Message
}

// NewHub builds a hub and starts listening for messages from other processes.
func NewHub(ctx context.Context, c cache.Cache, log *slog.Logger) *Hub {
	h := &Hub{
		cache:   c,
		log:     log,
		clients: map[*client]struct{}{},
		stop:    make(chan struct{}),
	}
	c.Subscribe(ctx, channel, func(raw []byte) {
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return
		}
		h.deliver(env)
	})
	return h
}

// Publish sends a message to everybody watching a tenant.
//
// `companyID` may be uuid.Nil for the whole tenant. Called from a handler
// AFTER its transaction commits: a push about a sale that then rolled back is
// a till showing stock that did not move.
func (h *Hub) Publish(
	ctx context.Context, tenantID, companyID uuid.UUID, m Message,
) {
	if h == nil || tenantID == uuid.Nil {
		return
	}
	if m.At == "" {
		m.At = time.Now().UTC().Format(time.RFC3339)
	}
	env := envelope{TenantID: tenantID, CompanyID: companyID, Message: m}

	raw, err := json.Marshal(env)
	if err != nil {
		return
	}
	// Onto the bus rather than straight to the local sockets. With a shared
	// cache every process — including this one — receives it exactly once,
	// which keeps the delivery path identical whether there is one replica or
	// five. With the in-memory cache the publish is a local fan-out and the
	// same code runs.
	h.cache.Publish(ctx, channel, raw)
}

// deliver hands a message to the sockets this process holds.
func (h *Hub) deliver(env envelope) {
	h.mu.RLock()
	targets := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		if c.tenantID != env.TenantID {
			continue
		}
		// A socket bound to one company ignores another's traffic. A socket
		// bound to none sees the whole tenant, which is what an owner watching
		// several branches wants.
		if env.CompanyID != uuid.Nil && c.companyID != uuid.Nil &&
			c.companyID != env.CompanyID {
			continue
		}
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.send <- env.Message:
		default:
			// Full. The client is behind and will be closed by its writer; the
			// broadcast does not wait for it.
			h.drop(c)
		}
	}
}

// Serve upgrades a request and runs the socket until it closes.
//
// The caller has already authenticated: `tenantID` and `companyID` come from
// the token, never from the request. A client cannot ask to watch a tenant.
func (h *Hub) Serve(
	w http.ResponseWriter, r *http.Request, tenantID, companyID uuid.UUID,
) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The browser is served from a different origin than the API in every
		// real deployment, and the token in the Authorization header is what
		// authorises this — not the origin. Cross-origin is therefore allowed
		// here and refused by the API's own CORS layer for everything else.
		InsecureSkipVerify: true,
		// Compression off. The messages are a few hundred bytes and per-message
		// deflate costs a context per connection, which for a shop with forty
		// tills is memory spent to save nothing.
		CompressionMode: websocket.CompressionDisabled,
		// The marker half of the pair the browser sent its token in. Accepting
		// it is what completes the handshake -- a server that names no
		// subprotocol when the client offered one makes some browsers close
		// the connection immediately.
		//
		// Only the marker is echoed. Echoing the token would put it in a
		// RESPONSE header, which is the one place it is more visible than the
		// query string this scheme exists to avoid.
		Subprotocols: []string{"rawsyst.auth"},
	})
	if err != nil {
		// Accept has already written a response.
		return
	}

	c := &client{
		conn:      conn,
		tenantID:  tenantID,
		companyID: companyID,
		// Sixty-four. Deep enough to ride out a burst of stock deltas from a
		// goods receipt, shallow enough that a stalled client is noticed in
		// seconds rather than after it has bought a megabyte of memory.
		send: make(chan Message, 64),
	}

	h.mu.Lock()
	h.clients[c] = struct{}{}
	count := len(h.clients)
	h.mu.Unlock()
	h.log.Debug("live socket opened", slog.Int("open", count))

	ctx := r.Context()
	// A reader is required by the protocol even though this direction carries
	// nothing: without one, the library never processes the ping and pong
	// frames that keep the connection alive, and a close from the client is
	// never noticed.
	go h.read(ctx, c)
	h.write(ctx, c)
}

func (h *Hub) read(ctx context.Context, c *client) {
	defer h.drop(c)
	for {
		// The payload is discarded. Nothing a client sends is trusted or acted
		// on: a socket that could ask for things would be a second, unaudited
		// API surface beside the route table.
		if _, _, err := c.conn.Read(ctx); err != nil {
			return
		}
	}
}

func (h *Hub) write(ctx context.Context, c *client) {
	defer h.drop(c)

	// Every twenty seconds, well inside the sixty a load balancer usually
	// allows an idle connection. A shop's till can sit quiet for an hour
	// between customers and its socket must survive that.
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stop:
			return
		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		case m := <-c.send:
			raw, err := json.Marshal(m)
			if err != nil {
				continue
			}
			// Bounded. A client whose TCP window has closed must not hold this
			// goroutine for the length of its timeout.
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = c.conn.Write(writeCtx, websocket.MessageText, raw)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (h *Hub) drop(c *client) {
	h.mu.Lock()
	_, present := h.clients[c]
	delete(h.clients, c)
	count := len(h.clients)
	h.mu.Unlock()
	if !present {
		return
	}
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	h.log.Debug("live socket closed", slog.Int("open", count))
}

// Open is how many sockets this process is holding, for /metrics.
func (h *Hub) Open() int {
	if h == nil {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Close ends every socket.
func (h *Hub) Close() error {
	if h == nil {
		return nil
	}
	h.closed.Do(func() { close(h.stop) })

	h.mu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = map[*client]struct{}{}
	h.mu.Unlock()

	for _, c := range clients {
		_ = c.conn.Close(websocket.StatusGoingAway, "shutting down")
	}
	return nil
}

// ErrNoHub is what a caller gets when live push is not wired.
var ErrNoHub = errors.New("live push is not configured")

// Notifier adapts a hub to the narrow interface other packages want.
//
// `notify` needs one verb — "something of this kind happened for this company"
// — and giving it the whole hub would let it publish arbitrary payloads from
// inside a domain package. The kind is all it has to say; the client reads the
// endpoint for the rest.
type Notifier struct{ hub *Hub }

// Notifications wraps a hub for the notify package. A nil hub is fine.
func Notifications(h *Hub) *Notifier { return &Notifier{hub: h} }

// Publish sends a bare kind with no payload.
func (n *Notifier) Publish(
	ctx context.Context, tenantID, companyID uuid.UUID, kind string,
) {
	if n == nil || n.hub == nil {
		return
	}
	n.hub.Publish(ctx, tenantID, companyID, Message{Kind: kind})
}
