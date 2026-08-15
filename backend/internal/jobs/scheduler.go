package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The scheduler enqueues recurring work, per design document 08 §7.
//
// It only ENQUEUES. Running the work is the worker's job, which means a
// schedule that fires while every worker is busy simply queues, and a schedule
// that fires twice is harmless because the dedupe key collapses it. A scheduler
// that executed directly would have neither property.

// Scheduler enqueues periodic jobs.
type Scheduler struct {
	queue *Queue
	log   *slog.Logger

	// StalenessEvery is how often unsubmitted invoices are re-evaluated.
	// Design 08 §7 says every minute: an invoice crossing 72 hours should raise
	// its critical alert within a minute, not at the next nightly sweep.
	StalenessEvery time.Duration
}

func NewScheduler(q *Queue, log *slog.Logger) *Scheduler {
	return &Scheduler{queue: q, log: log, StalenessEvery: time.Minute}
}

// Run enqueues on schedule until the context ends.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.StalenessEvery)
	defer ticker.Stop()

	// Once at startup, so a worker restarted after an outage evaluates
	// immediately rather than waiting out a full interval while invoices sit
	// unreported.
	s.enqueueStaleness(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.enqueueStaleness(ctx)
		}
	}
}

// enqueueStaleness queues one sweep per tenant.
//
// Per tenant rather than one global job, because the sweep runs in tenant
// context: row-level security is what stops a sweep for one tenant reading
// another's invoices, and a single job would have to bypass it.
func (s *Scheduler) enqueueStaleness(ctx context.Context) {
	tenants, err := s.tenants(ctx)
	if err != nil {
		s.log.Error("could not list tenants for the staleness sweep",
			slog.String("error", err.Error()))
		return
	}

	// The dedupe key holds the minute, so a sweep queued this minute is not
	// queued again — but the next minute gets its own. A key without the
	// minute would enqueue once and never again.
	minute := time.Now().UTC().Format("2006-01-02T15:04")

	for _, tenantID := range tenants {
		id := tenantID
		if err := s.queue.Enqueue(ctx, Spec{
			TenantID:    &id,
			Kind:        KindZATCAStaleness,
			Priority:    30,
			MaxAttempts: 3,
			DedupeKey:   "zatca.staleness:" + id.String() + ":" + minute,
		}); err != nil {
			s.log.Error("could not enqueue a staleness sweep",
				slog.String("tenant", id.String()),
				slog.String("error", err.Error()))
		}
	}
}

func (s *Scheduler) tenants(ctx context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := s.queue.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM tenant`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	return out, err
}
