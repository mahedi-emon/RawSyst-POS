// How often one operator may ask the backup system to do something.
//
// # Why these routes need a limit at all
//
// Every route here is AccessSuperAdmin and answers 404 to everybody else, so
// this is not a defence against the internet. It is a defence against three
// things that do happen to authenticated surfaces:
//
//   - A screen with a polling bug. The backup page refetches after every
//     action and polls while a task is running; a dependency array that is
//     wrong by one render turns that into a request per frame, and the symptom
//     is a database under load at 2am with nobody at a keyboard.
//   - A script, or a person with a script, retrying a failing operation in a
//     loop. `download` streams the whole database out of the object store on
//     every call, and the bill for that is real.
//   - A compromised or careless operator session. The blast radius of a stolen
//     platform token should not include "queue a thousand restores".
//
// # Why the concurrency guard is not enough on its own
//
// It is the more important of the two and it already exists: a partial unique
// index in migration 0135 means the database itself refuses a second heavy
// task while one is queued or running, so two dumps can never overlap however
// many requests arrive. But that guard makes each EXTRA request cheap, not
// free — it is still a round trip, an insert attempt and a constraint
// violation, and it does nothing at all for `download`, which is not a task.
//
// So the two are complementary and both are wanted: the index bounds what runs
// at once, and this bounds what is asked for.
//
// # Fail open, deliberately
//
// A cache that will not answer allows the request. This is defence in depth on
// a surface that already requires a platform operator's session, and the
// operation it most protects — replacing the live database — has three
// independent gates in front of it that are not rate limits: an environment
// flag off by default, a verified safety backup of what is live, and the
// snapshot id typed out by hand. A Redis outage must not be the reason a
// business cannot recover.
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/cache"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// backupRate is one class of operation and what it costs.
type backupRate struct {
	// name is the cache key prefix and the word in the message.
	name string
	// limit is how many are allowed in window, per operator.
	limit int64
	// window is the period the count resets over.
	window time.Duration
	// says is the sentence an operator reads, in their terms rather than in
	// the limiter's.
	says string
}

// The four classes, in order of what one request costs the server.
//
// The numbers are deliberately generous for a person and useless for a loop.
// Nobody takes eleven backups in an hour on purpose; a broken effect takes
// eleven in a second.
var (
	// Reads a screen makes. The backup page polls while a task runs, so this
	// has to be comfortably above a poll every few seconds for several
	// operators at once, and still catch a render loop.
	rateBackupRead = backupRate{
		name: "read", limit: 600, window: time.Minute,
		says: "Too many requests for backup status. This is almost always a " +
			"page left open in a loop rather than a person; reload it.",
	}

	// Queues work that takes minutes: a dump, a verification, a rehearsal.
	// The database already refuses a second one while one is running.
	rateBackupHeavy = backupRate{
		name: "heavy", limit: 12, window: time.Hour,
		says: "Too many backup operations asked for in the last hour. Each one " +
			"dumps or restores the whole database, and this server runs one at " +
			"a time. Wait for the one in progress, or look at Operations to " +
			"see what is queued.",
	}

	// Moves the whole database over the wire, in either direction.
	rateBackupTransfer = backupRate{
		name: "transfer", limit: 12, window: time.Hour,
		says: "Too many backup transfers in the last hour. A download streams " +
			"the entire database out of the object store and an upload brings " +
			"one back, so both are rate limited separately from everything else.",
	}

	// Deletes, or replaces the live database.
	rateBackupDestructive = backupRate{
		name: "destructive", limit: 6, window: time.Hour,
		says: "Too many destructive backup operations in the last hour. " +
			"Pruning and restoring over the live database are limited hard on " +
			"purpose. Nothing about this limit protects you from doing it " +
			"once, correctly — read deploy/server/RECOVERY.md first.",
	}
)

// backupLimiter counts operations per operator.
type backupLimiter struct {
	cache cache.Cache
}

func newBackupLimiter(c cache.Cache) *backupLimiter {
	if c == nil {
		c = cache.NewMemory()
	}
	return &backupLimiter{cache: c}
}

// allow counts one operation and says whether it may proceed.
func (l *backupLimiter) allow(
	r *http.Request, who string, rate backupRate,
) error {
	if l == nil || l.cache == nil {
		return nil
	}
	n, err := l.cache.Incr(
		r.Context(), "backuprate:"+rate.name+":"+who, rate.window)
	if err != nil {
		// See the note at the top of this file: a cache outage allows.
		return nil
	}
	if n > rate.limit {
		return errs.New(errs.CodeRateLimited, rate.says)
	}
	return nil
}

// limitBackup is the one line each handler runs before it does anything.
//
// Keyed on the OPERATOR rather than on the address. These routes are reached
// only with a platform session, so the operator is always known, and it is the
// right key for two reasons: two operators in one office behind one address
// must not throttle each other, and one operator on a laptop and a phone is
// still one person asking.
//
// The address is the fallback and should be unreachable — a request with no
// actor does not get past the access check — but a limiter that silently
// counts every anonymous caller under the same empty key would be a limiter
// that stops working the day something changes upstream.
func (s *Server) limitBackup(
	w http.ResponseWriter, r *http.Request, rate backupRate,
) bool {
	who := ""
	if a := actor.From(r.Context()); a.UserID != uuid.Nil {
		who = a.UserID.String()
	}
	if who == "" {
		if ip := callerIP(r); ip != nil {
			who = "ip:" + ip.String()
		} else {
			who = "ip:unknown"
		}
	}

	if err := s.backupLimit.allow(r, who, rate); err != nil {
		// Retry-After, because a client that is looping is exactly the client
		// that should be told how long to wait rather than left to guess. The
		// window rather than the remaining time: the count is a fixed window
		// and the honest upper bound is the whole of it.
		w.Header().Set("Retry-After",
			strconv.Itoa(int(rate.window/time.Second)))
		httpx.Error(w, r, err)
		return false
	}
	return true
}
