package devices

import (
	"sync"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// A fixed-window limiter for enrolment guesses, keyed by caller.
//
// It exists because a failed enrolment cannot be attributed to the code it was
// aiming at: the server compares a plaintext against every open hash, so a miss
// says nothing about which terminal was meant. Counting misses against the
// codes themselves — the first shape of this — meant one till guessing wrong
// could kill every outstanding code on the platform, which is a denial of
// service dressed as a security control.
//
// So the count is per CALLER. Somebody guessing locks out only themselves.
//
// In memory rather than in the database on purpose. It is a nuisance filter, not
// an accounting record: losing the counters on restart costs one more window of
// guesses against a code that expires in fifteen minutes anyway, and the
// alternative is a write on every failed request to a public endpoint, which is
// its own denial of service.
type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	seen   map[string]*window
}

type window struct {
	count   int
	resetAt time.Time
}

func newLimiter(limit int, w time.Duration) *limiter {
	return &limiter{limit: limit, window: w, seen: map[string]*window{}}
}

// allow refuses a caller who has already MISSED too many times.
//
// Only misses are counted, by miss() below. Charging successful enrolments too
// was the first version and it was wrong in a way that only shows up in a busy
// shop: opening six tills in an afternoon from one back-office network would
// have locked the sixth out for fifteen minutes, punishing exactly the person
// doing the right thing.
//
// An empty key — no usable client address — shares one bucket rather than being
// waved through. Behind a proxy that strips the address that makes the limit
// global, which is worse for a shop and much better against an attacker than
// the reverse mistake.
func (l *limiter) allow(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep()
	if w, ok := l.seen[key]; ok && w.count >= l.limit {
		return errs.New(errs.CodeRateLimited,
			"Too many enrolment attempts from this device. Wait a few minutes, "+
				"then ask for a fresh code in the back office.")
	}
	return nil
}

// miss records a failed guess against the caller who made it.
func (l *limiter) miss(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep()
	w, ok := l.seen[key]
	if !ok {
		w = &window{resetAt: time.Now().Add(l.window)}
		l.seen[key] = w
	}
	w.count++
}

// sweep drops expired windows. Called under the lock from both paths, so the
// map only grows while somebody is actually failing.
func (l *limiter) sweep() {
	now := time.Now()
	for k, w := range l.seen {
		if now.After(w.resetAt) {
			delete(l.seen, k)
		}
	}
}
