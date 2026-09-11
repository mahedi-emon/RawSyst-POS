//go:build integration

// How often a platform operator may ask the backup system to do something.
//
// These routes are already super-admin only, so the thing being defended
// against is not the internet. It is a screen stuck in a render loop, a script
// retrying a failure, and the blast radius of a stolen operator session — and
// `download` in particular, which streams the entire database out of the object
// store every time it is called and is not a queued task the database can
// serialise.
//
// What is held here: the limit exists on every class, a 429 says how long to
// wait, one operator cannot throttle another, and a limit is never the thing
// that decides whether a destructive operation is allowed — the gates in front
// of a production restore still refuse first.
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

// exhaust calls one route until it is refused, or gives up.
//
// Returns how many calls got through. `over` is a ceiling on the loop so a
// limit that is missing fails as a clear number rather than as a test that runs
// for ever.
func exhaust(
	t *testing.T, h *harness, method, path, token string, body any, over int,
) (through int, last *http.Response) {
	t.Helper()
	for i := 0; i < over; i++ {
		res := h.do(t, method, path, token, body)
		if res.StatusCode == http.StatusTooManyRequests {
			return i, res
		}
		through = i + 1
		last = res
	}
	return through, last
}

func TestEveryBackupClassIsRateLimited(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	// One route per class, because the classes share a counter within
	// themselves and the point is that each class HAS one. The numbers are the
	// limits in platform_backup_limits.go plus enough headroom to prove the
	// loop stopped because of the limiter rather than because it ran out.
	for _, c := range []struct {
		name         string
		method, path string
		body         any
		expectAtMost int
	}{
		{
			name: "heavy", method: http.MethodPost,
			path: "/api/v1/platform/backups", body: map[string]any{},
			expectAtMost: 12,
		},
		{
			name: "transfer", method: http.MethodGet,
			path:         "/api/v1/platform/backups/20260301T100000Z-1/download/dump",
			expectAtMost: 12,
		},
		{
			name: "destructive", method: http.MethodPost,
			path: "/api/v1/platform/backups/prune", body: map[string]any{},
			expectAtMost: 6,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			through, res := exhaust(
				t, h, c.method, c.path, admin, c.body, c.expectAtMost+10)
			if res == nil || res.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("%s: %d calls got through with no 429; this class has "+
					"no rate limit", c.name, through)
			}
			if through > c.expectAtMost {
				t.Errorf("%s: %d calls got through, limit is %d",
					c.name, through, c.expectAtMost)
			}

			// A client that is looping is exactly the client that should be
			// told how long to wait rather than left to guess.
			ra := res.Header.Get("Retry-After")
			if ra == "" {
				t.Error("a 429 with no Retry-After leaves a client guessing")
			} else if n, err := strconv.Atoi(ra); err != nil || n <= 0 {
				t.Errorf("Retry-After is not a number of seconds: %q", ra)
			}
		})
	}
}

func TestOneOperatorCannotThrottleAnother(t *testing.T) {
	// Keyed on the operator, not the address. Two platform operators in one
	// office are behind one address, and a limiter that counted them together
	// would take the second one out for an hour because of the first one's
	// stuck tab.
	h := newHarness(t)
	first := h.login(t, h.seedSuperAdmin(t))
	second := h.login(t, h.seedSuperAdmin(t))

	through, res := exhaust(t, h, http.MethodPost,
		"/api/v1/platform/backups/prune", first, map[string]any{}, 16)
	if res == nil || res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the first operator was never limited after %d calls", through)
	}

	res = h.do(t, http.MethodPost,
		"/api/v1/platform/backups/prune", second, map[string]any{})
	if res.StatusCode == http.StatusTooManyRequests {
		t.Error("the second operator was refused because of the first one's " +
			"requests; the limit is keyed on the wrong thing")
	}
}

func TestReadsAreLimitedFarAboveWhatAScreenDoes(t *testing.T) {
	// The backup page polls while a task is running. A limit that a normal
	// page can reach is a limit that makes the product look broken, so this
	// one is set high — but it still has to exist, because the failure it
	// catches is a dependency array that is wrong by one render.
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	// Well past any plausible poll rate, and well under the limit.
	for i := 0; i < 60; i++ {
		res := h.do(t, http.MethodGet,
			"/api/v1/platform/backups/health", admin, nil)
		if res.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("a read was rate limited after %d calls, which a polling "+
				"screen would hit in normal use", i+1)
		}
	}
}

// The limit must never become the thing that decides whether a dangerous
// operation is allowed. It is in front of the gates, not instead of them.
func TestTheLimitDoesNotReplaceTheRestoreGates(t *testing.T) {
	h := newHarness(t)
	admin := h.login(t, h.seedSuperAdmin(t))

	// The first call is inside every limit and must still be refused, because
	// the confirmation is wrong.
	res := h.do(t, http.MethodPost,
		"/api/v1/platform/backups/20260301T100000Z-1/restore-production",
		admin, map[string]any{"confirm": "not-the-snapshot-id"})
	if res.StatusCode == http.StatusTooManyRequests {
		t.Fatal("the first production restore of the hour was rate limited; " +
			"the limit is in front of the gates, which makes them untestable")
	}
	if res.StatusCode < 400 {
		t.Errorf("a production restore with the wrong confirmation answered "+
			"%d", res.StatusCode)
	}
}

// A route added without a class is a route with no limit, and it would be
// invisible: everything still works, slightly too well.
func TestEveryBackupRouteRefusesEventually(t *testing.T) {
	h := newHarness(t)

	for _, rt := range everyBackupRoute {
		// A fresh operator per route so one route's counter cannot mask
		// another's missing one.
		admin := h.login(t, h.seedSuperAdmin(t))

		var body any
		if rt.method != http.MethodGet {
			body = map[string]any{}
		}

		// 700 is past the read limit, which is the highest of the four.
		through, res := exhaust(t, h, rt.method, rt.path, admin, body, 700)
		if res == nil || res.StatusCode != http.StatusTooManyRequests {
			t.Errorf("%s %s: %d calls and never refused, so this route has no "+
				"rate limit. Give it a class in platform_backup_limits.go",
				rt.method, rt.path, through)
			continue
		}
		fmt.Printf("  %-62s limited after %d\n",
			rt.method+" "+rt.path, through)
	}
}
