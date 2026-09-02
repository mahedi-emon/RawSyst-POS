package inventory

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Watching the stock ledger change.
//
// # Why this is here and not at the call sites
//
// The main stock ledger is the single source of truth, and a till's local
// figure is a cache of it. That makes "the ledger moved" an event worth
// announcing — and an event that must be announced EVERY time it happens, or
// the caches drift and nobody finds out until a count.
//
// `Receive`, `Consume` and `Restore` are the only three functions that write
// `stock_movement`. Everything else goes through them: an online sale, an
// offline sale replayed by the sync engine, a goods receipt, an adjustment,
// both halves of a transfer, a return, a part issued to a repair job. So this
// records the movement HERE, once, rather than at each of the dozen places
// that call them.
//
// The first version of this announced stock from the sale handler. That was
// wrong in the way that does not show up in a demo: it covered the online sale
// and silently missed every offline sale the moment a till reconnected, which
// is precisely the case the announcement exists for.
//
// # Nothing is published from inside the transaction
//
// A movement is recorded on the CONTEXT while the transaction is open and
// published by whoever owns the request, after it commits. A push about stock
// that then rolled back is a till showing a quantity nobody sold, and it would
// never be corrected — there is no second event to say the sale came undone.
//
// # Recording is not publishing
//
// This package knows nothing about sockets. It collects facts; something
// further out decides whether anybody is listening. That keeps `inventory`
// free of a transport dependency and makes the collector usable by anything
// else that wants to know what moved — a test, an audit, a future outbox.

// Movement is one authoritative change to the stock ledger.
//
// Deliberately narrow: which variant, in which warehouse, by how much. No
// cost, no price, no source document. A push travels to every socket in the
// tenant and what a client may SEE is decided by the endpoint it reads next,
// so nothing here may be anything a permission would gate — and the cost of a
// line is exactly that.
type Movement struct {
	TenantID    uuid.UUID
	CompanyID   uuid.UUID
	VariantID   uuid.UUID
	WarehouseID uuid.UUID
	// Delta is signed: negative for stock leaving.
	Delta decimal.Decimal
}

// Collector gathers the movements made under one context.
type Collector struct {
	mu   sync.Mutex
	seen []Movement
}

type ctxKey struct{}

// Collecting returns a context that records every ledger movement made under
// it, and the collector to drain afterwards.
//
// Optional by design. Code that runs without one — a test, a migration, a
// background job nobody is watching — records nothing and behaves identically.
// That is what keeps `record` below free of a nil check at every call site and
// keeps this from becoming something the ledger depends on.
func Collecting(ctx context.Context) (context.Context, *Collector) {
	c := &Collector{}
	return context.WithValue(ctx, ctxKey{}, c), c
}

// Drain returns what was recorded and empties the collector.
//
// Emptied so a caller that drains twice does not publish twice. The second
// call returns nothing, which is the honest answer.
func (c *Collector) Drain() []Movement {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.seen
	c.seen = nil
	return out
}

// Len is how many movements are waiting, for a caller that wants to know
// whether anything happened without taking them.
func (c *Collector) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}

// record notes a movement if anybody is collecting.
//
// Locked because one request can legitimately move stock from several
// goroutines — the sync engine applies a batch, and a future parallel replay
// would too. An unsynchronised append there is a data race that appears as a
// lost push once a month.
func record(ctx context.Context, m Movement) {
	c, ok := ctx.Value(ctxKey{}).(*Collector)
	if !ok || c == nil {
		return
	}
	// A movement of nothing is not a movement. Filtered here rather than at
	// the receiver so no consumer has to think about it.
	if m.Delta.IsZero() {
		return
	}
	c.mu.Lock()
	c.seen = append(c.seen, m)
	c.mu.Unlock()
}
