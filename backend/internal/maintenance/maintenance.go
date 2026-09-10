// Stopping the product from accepting writes, on purpose, for a few minutes.
//
// # Why a product needs this
//
// A migration to another server copies the database at one instant. Everything
// written after that instant and before the switch is on the old server and
// nowhere else, and when the old server is turned off it is gone. A shop that
// rang up eleven sales during the move loses eleven sales, and nobody finds out
// until the till totals disagree with the day's cash.
//
// There is no clever way round it. The only way not to lose those writes is not
// to accept them, which means the product has to be able to say "closed for a
// few minutes" — and to say it in a way a cashier understands rather than by
// timing out.
//
// # Reads stay open, and that is deliberate
//
// A cashier looking at yesterday's totals writes nothing and loses nothing.
// Locking them out of a screen they are reading turns a planned ten minutes
// into a support call, so by default reads continue and only writes are
// refused. An operator who wants everything closed can say so.
//
// # Platform operators keep working
//
// Somebody is performing the migration and they are doing it through this
// product. Freezing them out would mean the only way to turn the freeze off was
// a database client, which is exactly the wrong thing to need at that moment.
//
// # Cached, because every request asks
//
// The state is one row. Reading it on every request would put a query in front
// of every sale for the sake of a flag that changes twice a year, so it is held
// for two seconds. That means a freeze takes up to two seconds to take hold —
// which is fine for something whose purpose is measured in minutes, and is
// stated here so nobody later reads the delay as a bug.
package maintenance

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// State is what the product is currently accepting.
type State struct {
	Active bool `json:"active"`

	// Reason is shown to whoever is refused. Written by the operator, so it is
	// shown verbatim rather than translated: it is a sentence about this
	// particular afternoon.
	Reason string `json:"reason,omitempty"`

	AllowReads bool `json:"allow_reads"`

	StartedAt string `json:"started_at,omitempty"`
	StartedBy string `json:"started_by,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
}

// Service reads and writes the freeze.
type Service struct {
	pool *db.Pool

	mu       sync.RWMutex
	cached   State
	cachedAt time.Time
	// TTL is how stale the cached answer may be. See the package note.
	TTL time.Duration
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service {
	return &Service{pool: pool, TTL: 2 * time.Second}
}

// Current reads the state, from the cache when it is fresh enough.
//
// A read failure returns the LAST KNOWN state rather than an error or a
// default. The alternatives are both wrong: erroring puts a database blip in
// front of every request, and defaulting to "open" would quietly lift a freeze
// during a migration, which is the one moment it must not lift.
func (s *Service) Current(ctx context.Context) State {
	if s == nil || s.pool == nil {
		return State{AllowReads: true}
	}
	s.mu.RLock()
	if time.Since(s.cachedAt) < s.TTL {
		out := s.cached
		s.mu.RUnlock()
		return out
	}
	last := s.cached
	s.mu.RUnlock()

	fresh, err := s.read(ctx)
	if err != nil {
		return last
	}
	s.mu.Lock()
	s.cached, s.cachedAt = fresh, time.Now()
	s.mu.Unlock()
	return fresh
}

// Read goes to the database, skipping the cache. Used by the screen that shows
// the state, which should not be two seconds behind the button it just pressed.
func (s *Service) Read(ctx context.Context) (State, error) {
	if s == nil || s.pool == nil {
		return State{AllowReads: true}, nil
	}
	fresh, err := s.read(ctx)
	if err != nil {
		return State{}, err
	}
	s.mu.Lock()
	s.cached, s.cachedAt = fresh, time.Now()
	s.mu.Unlock()
	return fresh, nil
}

func (s *Service) read(ctx context.Context) (State, error) {
	var out State
	var reason, by *string
	var started, ended *time.Time
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT active, reason, allow_reads, started_at, started_by_label,
			       ended_at
			FROM platform_maintenance WHERE only_row`).
			Scan(&out.Active, &reason, &out.AllowReads, &started, &by, &ended)
	})
	if err != nil {
		return State{}, db.Translate(err, "")
	}
	if reason != nil {
		out.Reason = *reason
	}
	if by != nil {
		out.StartedBy = *by
	}
	if started != nil {
		out.StartedAt = started.UTC().Format(time.RFC3339)
	}
	if ended != nil {
		out.EndedAt = ended.UTC().Format(time.RFC3339)
	}
	return out, nil
}

// Begin puts the product into maintenance.
func (s *Service) Begin(
	ctx context.Context, reason string, allowReads bool,
	by *uuid.UUID, byLabel string,
) (State, error) {
	if s == nil || s.pool == nil {
		return State{}, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	if reason == "" {
		reason = "RawSyst is briefly closed for maintenance."
	}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE platform_maintenance
			SET active = true, reason = $1, allow_reads = $2,
			    started_at = now(), started_by = $3,
			    started_by_label = nullif($4,''), ended_at = NULL
			WHERE only_row`, reason, allowReads, by, byLabel)
		return e
	})
	if err != nil {
		return State{}, db.Translate(err, "Maintenance could not be started.")
	}
	s.invalidate()
	return s.Read(ctx)
}

// End takes the product back out of maintenance.
func (s *Service) End(ctx context.Context) (State, error) {
	if s == nil || s.pool == nil {
		return State{}, errs.New(errs.CodeUnavailable, "No database connection.")
	}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE platform_maintenance
			SET active = false, ended_at = now() WHERE only_row`)
		return e
	})
	if err != nil {
		return State{}, db.Translate(err, "Maintenance could not be ended.")
	}
	s.invalidate()
	return s.Read(ctx)
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

// Refused is the error a frozen request gets.
//
// `CodeUnavailable` so it maps to 503 rather than 403: this is a temporary
// condition and a client that retries later is doing the right thing, which is
// exactly what a 503 means and a 403 does not.
func Refused(st State) error {
	reason := st.Reason
	if reason == "" {
		reason = "RawSyst is briefly closed while its data is moved."
	}
	return errs.New(errs.CodeUnavailable, reason+
		" Nothing you have already saved is affected. Anything entered now "+
		"would be lost, which is why it is not being accepted.")
}
