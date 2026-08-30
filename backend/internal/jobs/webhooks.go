package jobs

// Sending queued webhooks (blueprint H6).
//
// # Why this is a job rather than a goroutine on the API
//
// A webhook goes to somebody else's server, and somebody else's server can be
// slow, wrong or gone. Sending from the API process would mean a request
// handler waiting on a stranger, and the connection it holds is one the tills
// need. The worker is where waiting is free.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/integration"
)

// KindWebhookDispatch sends one tenant's due deliveries.
const KindWebhookDispatch = "webhook.dispatch"

// WebhookDispatcher drains the delivery queue.
type WebhookDispatcher struct {
	integrations *integration.Service
	client       *http.Client
}

// NewWebhookDispatcher builds the handler.
//
// One http.Client, reused. A client per delivery would open a new TCP and TLS
// connection to the same receiver every time, which for a shop sending a
// webhook per sale is a measurable cost on somebody else's server as well as
// this one.
func NewWebhookDispatcher(svc *integration.Service) *WebhookDispatcher {
	return &WebhookDispatcher{
		integrations: svc,
		client: &http.Client{
			// Ten seconds. A receiver that has not answered in ten is a
			// receiver that is not going to; the delivery is retried with
			// backoff rather than held open.
			Timeout: 10 * time.Second,
		},
	}
}

// Run sends what is due for the job's tenant.
func (d *WebhookDispatcher) Run(ctx context.Context, j Job) error {
	if j.TenantID == nil {
		return Permanent{Err: errors.New(
			"a webhook dispatch was queued with no tenant, and the delivery " +
				"queue can only be read in tenant context")}
	}
	// Fifty at a time. The job is enqueued every tick, so a backlog drains
	// across several runs rather than one run holding a worker for an hour —
	// which would starve every other job kind behind it.
	_, err := d.integrations.Dispatch(ctx, *j.TenantID, d.client, 50)
	return err
}

// enqueueWebhooks queues one dispatch per tenant.
//
// Per tenant, like the staleness sweep and for the same reason: the delivery
// queue is read in tenant context, and row-level security is what stops one
// tenant's dispatcher reading another's endpoints.
func (s *Scheduler) enqueueWebhooks(ctx context.Context) {
	tenants, err := s.tenants(ctx)
	if err != nil {
		s.log.Error("could not list tenants for webhook dispatch",
			slog.String("error", err.Error()))
		return
	}

	// The minute is in the key, like the staleness sweep: one dispatch per
	// tenant per tick, and the next tick gets its own. Without the minute a
	// dispatch would be queued once and never again; without a key at all, a
	// tick arriving while the previous run is still going would put two
	// workers on the same rows.
	minute := time.Now().UTC().Format("2006-01-02T15:04")

	for _, tenantID := range tenants {
		id := tenantID
		if err := s.queue.Enqueue(ctx, Spec{
			TenantID:    &id,
			Kind:        KindWebhookDispatch,
			Priority:    40,
			MaxAttempts: 3,
			DedupeKey:   KindWebhookDispatch + ":" + id.String() + ":" + minute,
		}); err != nil {
			s.log.Error("could not enqueue a webhook dispatch",
				slog.String("tenant", id.String()),
				slog.String("error", err.Error()))
		}
	}
}
