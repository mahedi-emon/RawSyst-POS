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

	// TieOutAt is the hour, UTC, at which the books are reconciled. Design 08
	// §7 says daily at 04:00 — after the day is over and before anybody is
	// looking at yesterday's figures.
	TieOutAt int
}

func NewScheduler(q *Queue, log *slog.Logger) *Scheduler {
	return &Scheduler{queue: q, log: log, StalenessEvery: time.Minute, TieOutAt: 4}
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
			// Checked on the same tick rather than on a timer of its own. The
			// dedupe key carries the date, so this enqueues once on the first
			// tick after 04:00 and does nothing on the other 1,439 — and a
			// worker that was down at 04:00 still reconciles when it comes
			// back, which a fire-once-at-04:00 timer would not.
			s.enqueueTieOut(ctx)
			// And the outbound webhooks. Same tick, same reasoning: a
			// receiver that was down when a sale happened gets the delivery
			// on the next pass rather than never.
			s.enqueueWebhooks(ctx)
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

// enqueueTieOut queues the nightly reconciliation, once per tenant per day.
//
// Design 08 §3 gives the job a queue key of company:{id}; the sweep itself
// walks the tenant's companies in one transaction, so the key here is the
// tenant. What matters is that two sweeps for the same tenant never run at
// once, which the dedupe key and the queue key both prevent.
func (s *Scheduler) enqueueTieOut(ctx context.Context) {
	now := time.Now().UTC()
	if now.Hour() != s.TieOutAt {
		return
	}

	tenants, err := s.tenants(ctx)
	if err != nil {
		s.log.Error("could not list tenants for the tie-out",
			slog.String("error", err.Error()))
		return
	}

	day := now.Format("2006-01-02")
	for _, tenantID := range tenants {
		id := tenantID
		if err := s.queue.Enqueue(ctx, Spec{
			TenantID: &id,
			Kind:     KindAccountingTieOut,
			QueueKey: "tenant:" + id.String(),
			// Design 08 §3: priority 70, three attempts. Lower priority than
			// ZATCA submission on purpose — a report that the books disagree
			// can wait behind an invoice that must reach the authority.
			Priority:    70,
			MaxAttempts: 3,
			DedupeKey:   KindAccountingTieOut + ":" + id.String() + ":" + day,
		}); err != nil {
			s.log.Error("could not enqueue a tie-out",
				slog.String("tenant", id.String()),
				slog.String("error", err.Error()))
		}
	}
}
