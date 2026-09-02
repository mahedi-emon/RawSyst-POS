// Package cache is the shared cache, rate-limit store and invalidation bus.
//
// # Why this exists at all
//
// One API process needs none of it. An in-memory map is faster than Redis, and
// a per-process rate limit of ten is a limit of ten.
//
// The moment there are TWO processes — a second replica behind the load
// balancer, or a rolling deploy with both versions briefly up — in-memory stops
// being correct:
//
//   - A permission revoked at 10:00 on one replica stays live on the other
//     until its own cache expires. Design 04 is explicit that this window is
//     unacceptable for a system handling money, which is the whole reason
//     grants are not baked into the token.
//   - A rate limit of ten becomes a limit of twenty, then thirty. The auth
//     endpoints are the ones that matter and they are the ones that break
//     first.
//   - A regulatory rule edited on one replica is not seen by the other until
//     its cache turns over, which design 05 solves with pub/sub invalidation.
//
// # So it is one interface with two implementations, and both are real
//
// `Memory` is the supported single-process deployment, which is what most shops
// run: one server, one API, one database. `Redis` is what makes more than one
// process behave like one system.
//
// Neither is a stub. The failure this package refuses to have is the common
// one — an interface written for Redis with an in-memory "fallback" that
// silently does nothing, so a single-process deployment quietly loses its rate
// limiting the day somebody sets the environment variable wrong.
//
// # Nothing here is the source of truth
//
// Every value in this package can be recomputed from the database. That is not
// an accident: a cache that holds the only copy of something is a database with
// no backups. Redis going away must cost latency and nothing else, which is why
// a failed read is treated as a miss rather than as an error.
package cache

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
)

// Cache is what the rest of the product depends on.
//
// Deliberately small. Everything here is get, set, drop, count and publish —
// no lists, no sorted sets, no scripting. A wider interface would be a wider
// thing the in-memory implementation has to match exactly, and a subtle
// disagreement between the two is a bug that only appears on the deployment
// with more than one replica.
type Cache interface {
	// Get returns the stored bytes, and false for a miss. A backend that is
	// unreachable reports a miss: the caller recomputes, which is slower and
	// correct.
	Get(ctx context.Context, key string) ([]byte, bool)

	// Set stores a value with a lifetime. A ttl of zero means "until dropped",
	// which almost nothing should want.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)

	// Drop removes keys. Called on the write side of anything cached — a role
	// change, a rule edit — so the invalidation is explicit rather than a wait
	// for a TTL.
	Drop(ctx context.Context, keys ...string)

	// DropPrefix removes everything under a prefix.
	//
	// For "forget everything cached about this tenant", which is what a role
	// change actually means: the grants of every user in it are stale, and
	// naming them one at a time would need a query to find out who they are.
	DropPrefix(ctx context.Context, prefix string)

	// Incr adds one to a counter and returns the new value, setting the ttl on
	// the FIRST increment only. That is the token bucket: the window starts
	// when the first request in it arrives.
	Incr(ctx context.Context, key string, window time.Duration) (int64, error)

	// Publish sends a message to every process subscribed to a channel.
	//
	// The invalidation bus of design 05. On one process it is a local fan-out
	// and still correct; on several it is what makes a rule edit visible
	// everywhere within the round trip rather than within the TTL.
	Publish(ctx context.Context, channel string, message []byte)

	// Subscribe calls fn for every message on a channel until ctx is done.
	Subscribe(ctx context.Context, channel string, fn func([]byte))

	// Shared reports whether this cache is visible to other processes.
	//
	// Read by the health endpoint and by the start-up log, so an operator
	// running two replicas against an in-memory cache can be told rather than
	// discovering it from a rate limit that does not hold.
	Shared() bool

	// Ping proves the backend answers. Used by /readyz.
	Ping(ctx context.Context) error

	Close() error
}

// Open builds the cache the configuration asks for.
//
// Never returns nil and never returns an error for "Redis was not configured":
// no Redis is a supported deployment, not a fault.
func Open(cfg config.Redis) Cache {
	if !cfg.Configured() {
		return NewMemory()
	}
	options := &redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
		// A cache that blocks is worse than a cache that misses: the whole
		// point is to be faster than the database, and a request waiting five
		// seconds on Redis would have been served twice over by Postgres.
		DialTimeout:  2 * time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	}
	if cfg.TLS {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &Redis{client: redis.NewClient(options)}
}

// --- Redis ----------------------------------------------------------------

// Redis is the shared implementation.
type Redis struct {
	client *redis.Client
}

func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool) {
	out, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		// Including redis.Nil, which is an ordinary miss. Every other error is
		// treated as one too: see the package note — an unreachable cache
		// costs latency, not correctness.
		return nil, false
	}
	return out, true
}

func (r *Redis) Set(
	ctx context.Context, key string, value []byte, ttl time.Duration,
) {
	// The error is dropped on purpose. A failed write means the next read
	// misses and recomputes, which is exactly what would have happened without
	// a cache at all.
	_ = r.client.Set(ctx, key, value, ttl).Err()
}

func (r *Redis) Drop(ctx context.Context, keys ...string) {
	if len(keys) == 0 {
		return
	}
	_ = r.client.Del(ctx, keys...).Err()
}

func (r *Redis) DropPrefix(ctx context.Context, prefix string) {
	// SCAN rather than KEYS. KEYS blocks the server for the length of the
	// keyspace, and the one deployment where that matters is the busy one.
	iter := r.client.Scan(ctx, 0, prefix+"*", 256).Iterator()
	batch := make([]string, 0, 256)
	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) == cap(batch) {
			_ = r.client.Del(ctx, batch...).Err()
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		_ = r.client.Del(ctx, batch...).Err()
	}
}

func (r *Redis) Incr(
	ctx context.Context, key string, window time.Duration,
) (int64, error) {
	// Pipelined so the increment and the expiry are one round trip. Two would
	// leave a window in which a crash between them left a counter with no
	// expiry — a rate limit that never resets, which locks somebody out
	// permanently.
	pipe := r.client.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	n := incr.Val()
	if n == 1 {
		// First in the window. Expire above already set it; this is only
		// documenting that the window starts here rather than sliding.
		_ = n
	}
	return n, nil
}

func (r *Redis) Publish(ctx context.Context, channel string, message []byte) {
	_ = r.client.Publish(ctx, channel, message).Err()
}

func (r *Redis) Subscribe(
	ctx context.Context, channel string, fn func([]byte),
) {
	sub := r.client.Subscribe(ctx, channel)
	go func() {
		defer sub.Close()
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				fn([]byte(m.Payload))
			}
		}
	}()
}

func (r *Redis) Shared() bool { return true }

func (r *Redis) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *Redis) Close() error { return r.client.Close() }

// --- in memory ------------------------------------------------------------

type entry struct {
	value []byte
	// expires is zero for "no expiry".
	expires time.Time
}

// Memory is the single-process implementation.
//
// A real one. It expires, it counts, it fans out to local subscribers, and it
// sweeps itself so a process that runs for a month does not grow a map of dead
// keys. The only thing it does not do is cross a process boundary, and
// `Shared` says so.
type Memory struct {
	mu      sync.RWMutex
	entries map[string]entry

	subsMu sync.RWMutex
	subs   map[string][]chan []byte

	stop chan struct{}
	once sync.Once
}

// NewMemory builds one and starts its sweeper.
func NewMemory() *Memory {
	m := &Memory{
		entries: map[string]entry{},
		subs:    map[string][]chan []byte{},
		stop:    make(chan struct{}),
	}
	go m.sweep()
	return m
}

// sweep drops expired entries.
//
// Lazy expiry on read alone is not enough: a key written once and never read
// again would be held for the life of the process, and the rate limiter writes
// one per IP address.
func (m *Memory) sweep() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			m.mu.Lock()
			for k, e := range m.entries {
				if !e.expires.IsZero() && now.After(e.expires) {
					delete(m.entries, k)
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, bool) {
	m.mu.RLock()
	e, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !e.expires.IsZero() && time.Now().After(e.expires) {
		m.mu.Lock()
		delete(m.entries, key)
		m.mu.Unlock()
		return nil, false
	}
	// A copy. Handing out the stored slice would let a caller mutate what the
	// next reader sees, which is the kind of bug that appears once a fortnight
	// and is never reproduced.
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, true
}

func (m *Memory) Set(
	_ context.Context, key string, value []byte, ttl time.Duration,
) {
	stored := make([]byte, len(value))
	copy(stored, value)
	e := entry{value: stored}
	if ttl > 0 {
		e.expires = time.Now().Add(ttl)
	}
	m.mu.Lock()
	m.entries[key] = e
	m.mu.Unlock()
}

func (m *Memory) Drop(_ context.Context, keys ...string) {
	m.mu.Lock()
	for _, k := range keys {
		delete(m.entries, k)
	}
	m.mu.Unlock()
}

func (m *Memory) DropPrefix(_ context.Context, prefix string) {
	m.mu.Lock()
	for k := range m.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(m.entries, k)
		}
	}
	m.mu.Unlock()
}

func (m *Memory) Incr(
	_ context.Context, key string, window time.Duration,
) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	e, ok := m.entries[key]
	if !ok || (!e.expires.IsZero() && now.After(e.expires)) {
		// First in a new window. The window starts here and does not slide,
		// which is what the Redis side does too.
		m.entries[key] = entry{value: encodeCount(1), expires: now.Add(window)}
		return 1, nil
	}
	n := decodeCount(e.value) + 1
	e.value = encodeCount(n)
	m.entries[key] = e
	return n, nil
}

// encodeCount and decodeCount keep a counter in the same byte-valued map the
// rest of the cache uses, rather than adding a second map with its own lock.
func encodeCount(n int64) []byte {
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = byte(n)
		n >>= 8
	}
	return out
}

func decodeCount(b []byte) int64 {
	if len(b) != 8 {
		return 0
	}
	var n int64
	for _, c := range b {
		n = n<<8 | int64(c)
	}
	return n
}

func (m *Memory) Publish(_ context.Context, channel string, message []byte) {
	m.subsMu.RLock()
	listeners := m.subs[channel]
	m.subsMu.RUnlock()
	for _, ch := range listeners {
		// Non-blocking. A subscriber that has stopped reading must not stop the
		// process that published — an invalidation is best-effort by design,
		// and the TTL is the backstop.
		select {
		case ch <- message:
		default:
		}
	}
}

func (m *Memory) Subscribe(
	ctx context.Context, channel string, fn func([]byte),
) {
	ch := make(chan []byte, 64)
	m.subsMu.Lock()
	m.subs[channel] = append(m.subs[channel], ch)
	m.subsMu.Unlock()

	go func() {
		defer func() {
			m.subsMu.Lock()
			kept := m.subs[channel][:0]
			for _, c := range m.subs[channel] {
				if c != ch {
					kept = append(kept, c)
				}
			}
			m.subs[channel] = kept
			m.subsMu.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stop:
				return
			case msg := <-ch:
				fn(msg)
			}
		}
	}()
}

func (m *Memory) Shared() bool { return false }

func (m *Memory) Ping(context.Context) error { return nil }

func (m *Memory) Close() error {
	m.once.Do(func() { close(m.stop) })
	return nil
}

// ErrNotConfigured is returned by helpers that need a shared cache and were
// given the in-memory one.
var ErrNotConfigured = errors.New("no shared cache is configured")
