package cache

import (
	"sync"
	"testing"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
)

// The in-memory implementation is not a stub, and this is what says so.
//
// The failure this package is written to avoid is the common one: an interface
// designed for Redis with an in-memory "fallback" that silently does nothing,
// so a single-process deployment quietly loses its rate limiting the day
// somebody sets the environment variable wrong. Every behaviour the Redis side
// promises is checked here.

func TestWhatIsStoredComesBack(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	c.Set(t.Context(), "k", []byte("value"), time.Minute)
	got, ok := c.Get(t.Context(), "k")
	if !ok {
		t.Fatal("a value written a moment ago was not found")
	}
	if string(got) != "value" {
		t.Fatalf("got %q, want value", got)
	}

	if _, ok := c.Get(t.Context(), "never-written"); ok {
		t.Fatal("a key that was never written was found")
	}
}

// TestAStoredValueCannotBeChangedFromOutside is the bug that appears once a
// fortnight and is never reproduced: a caller mutating the slice it was handed
// and changing what the next reader sees.
func TestAStoredValueCannotBeChangedFromOutside(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	original := []byte("value")
	c.Set(t.Context(), "k", original, time.Minute)
	original[0] = 'V'

	got, _ := c.Get(t.Context(), "k")
	if string(got) != "value" {
		t.Fatalf("the caller's slice reached the store: got %q", got)
	}

	got[0] = 'X'
	again, _ := c.Get(t.Context(), "k")
	if string(again) != "value" {
		t.Fatalf("a reader's slice reached the store: got %q", again)
	}
}

func TestAValuePastItsTimeIsGone(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	c.Set(t.Context(), "k", []byte("value"), 20*time.Millisecond)
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.Get(t.Context(), "k"); ok {
		t.Fatal("an expired value was still there")
	}
}

func TestDroppingByPrefixTakesTheWholeTenant(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	c.Set(t.Context(), "grants:t1:u1", []byte("a"), time.Minute)
	c.Set(t.Context(), "grants:t1:u2", []byte("b"), time.Minute)
	c.Set(t.Context(), "grants:t2:u1", []byte("c"), time.Minute)

	c.DropPrefix(t.Context(), "grants:t1:")

	if _, ok := c.Get(t.Context(), "grants:t1:u1"); ok {
		t.Fatal("a key under the dropped prefix survived")
	}
	if _, ok := c.Get(t.Context(), "grants:t1:u2"); ok {
		t.Fatal("a key under the dropped prefix survived")
	}
	if _, ok := c.Get(t.Context(), "grants:t2:u1"); !ok {
		t.Fatal("another tenant's key was dropped as well")
	}
}

// TestTheCounterCountsAndTheWindowDoesNotSlide is what the rate limiter rests
// on. A window that slid would never let anybody back in under sustained
// traffic, which is a lockout rather than a limit.
func TestTheCounterCountsAndTheWindowDoesNotSlide(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	for want := int64(1); want <= 5; want++ {
		got, err := c.Incr(t.Context(), "ip:1.2.3.4", 50*time.Millisecond)
		if err != nil {
			t.Fatalf("counting: %v", err)
		}
		if got != want {
			t.Fatalf("count = %d, want %d", got, want)
		}
	}

	// Past the window, the count starts again rather than carrying on.
	time.Sleep(70 * time.Millisecond)
	got, err := c.Incr(t.Context(), "ip:1.2.3.4", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if got != 1 {
		t.Fatalf("count after the window = %d, want 1", got)
	}
}

func TestCountersForDifferentCallersAreSeparate(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	for i := 0; i < 4; i++ {
		if _, err := c.Incr(t.Context(), "ip:a", time.Minute); err != nil {
			t.Fatalf("counting: %v", err)
		}
	}
	got, err := c.Incr(t.Context(), "ip:b", time.Minute)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if got != 1 {
		t.Fatalf("one caller's attempts counted against another: %d", got)
	}
}

func TestAMessageReachesEverySubscriber(t *testing.T) {
	c := NewMemory()
	defer c.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	seen := make(chan string, 2)
	for i := 0; i < 2; i++ {
		c.Subscribe(t.Context(), "rules", func(b []byte) {
			seen <- string(b)
			wg.Done()
		})
	}
	// The subscribers register in goroutines; give them a moment rather than
	// racing the publish.
	time.Sleep(20 * time.Millisecond)

	c.Publish(t.Context(), "rules", []byte("vat changed"))

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("only %d of 2 subscribers were told", len(seen))
	}
}

// TestAnInMemoryCacheSaysItIsNotShared: an operator running two replicas
// against this has a rate limit of twice what they configured, and the product
// has to be able to tell them so.
func TestAnInMemoryCacheSaysItIsNotShared(t *testing.T) {
	c := NewMemory()
	defer c.Close()
	if c.Shared() {
		t.Fatal("the in-memory cache claims to be shared")
	}
	if err := c.Ping(t.Context()); err != nil {
		t.Fatalf("pinging: %v", err)
	}
}

// TestNoRedisIsStillACache: Open with nothing configured is a supported
// deployment and must return something that works, never nil.
func TestNoRedisIsStillACache(t *testing.T) {
	c := Open(config.Redis{})
	if c == nil {
		t.Fatal("Open returned nil")
	}
	defer c.Close()

	c.Set(t.Context(), "k", []byte("v"), time.Minute)
	if _, ok := c.Get(t.Context(), "k"); !ok {
		t.Fatal("the fallback does not store anything")
	}
	if c.Shared() {
		t.Fatal("the fallback claims to be shared")
	}
}

func TestClosingTwiceIsNotAPanic(t *testing.T) {
	c := NewMemory()
	if err := c.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("closing again: %v", err)
	}
}
