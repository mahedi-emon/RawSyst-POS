package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
)

// Scheduled reports, blueprint D1.
//
// # The sweep queues the send; it does not compute the report
//
// This handler finds the schedules whose turn it is and queues one `notify.send`
// per recipient. The figures are computed by the reports service when the
// notification is rendered — which is what keeps a scheduled report from being
// a snapshot taken at the moment the schedule was set.
//
// # A schedule that fails is stamped anyway
//
// `MarkRun` is called whether the send succeeded or not, with the reason. The
// alternative is a schedule that fails and is retried every minute forever,
// and an owner who is told nothing until they wonder why Monday's figures
// stopped arriving.

// KindReportSweep finds scheduled reports whose turn it is.
const KindReportSweep = "report.sweep"

// ReportSweeper queues scheduled reports.
type ReportSweeper struct {
	pool    *db.Pool
	reports *reports.Service
}

// NewReportSweeper builds the handler.
func NewReportSweeper(pool *db.Pool, r *reports.Service) *ReportSweeper {
	return &ReportSweeper{pool: pool, reports: r}
}

// Run queues one message per recipient of every schedule that is due.
func (s *ReportSweeper) Run(ctx context.Context, j Job) error {
	if j.TenantID == nil {
		return Permanent{errs.New(errs.CodeInternal,
			"This report sweep names no tenant.")}
	}
	tenantID := *j.TenantID
	now := time.Now().UTC()

	due, err := s.reports.Due(ctx, tenantID, now)
	if err != nil {
		return err
	}

	for _, r := range due {
		failure := ""

		for _, address := range splitRecipients(r.Recipients) {
			if e := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
				return EnqueueIn(ctx, tx, Spec{
					TenantID:    &tenantID,
					Kind:        "notify.send",
					Priority:    40,
					MaxAttempts: 3,
					// One message per recipient per occurrence. The key holds
					// the date so tomorrow's run is not deduped against
					// today's, and holds the recipient so two people on the
					// same schedule both get it.
					DedupeKey: "report:" + r.ID.String() + ":" + address +
						":" + now.Format("2006-01-02"),
					Payload: map[string]any{
						"kind":  "scheduled_report",
						"email": address,
						"report": map[string]any{
							"id":   r.ID.String(),
							"name": r.Name,
							"kind": r.Kind,
						},
					},
				})
			}); e != nil {
				failure = e.Error()
			}
		}

		// Stamped whether it went or not. See the file note.
		if e := s.reports.MarkRun(ctx, tenantID, r.ID, failure); e != nil {
			return e
		}
	}
	return nil
}

// splitRecipients turns the stored list into addresses.
//
// Commas or newlines, because a person pasting three addresses out of an email
// client gets one or the other and should not have to know which this field
// wanted.
func splitRecipients(raw string) []string {
	out := []string{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// enqueueReports queues one sweep per tenant.
//
// Per tenant rather than one global job, for the reason the staleness sweep
// gives: the sweep runs in tenant context and row-level security is what stops
// it reading another tenant's schedules.
func (s *Scheduler) enqueueReports(ctx context.Context) {
	tenants, err := s.tenants(ctx)
	if err != nil {
		return
	}

	// The key holds the HOUR rather than the minute. A scheduled report is a
	// daily, weekly or monthly thing; sweeping for it sixty times an hour
	// would be sixty queries per tenant per hour to find the same nothing.
	hour := time.Now().UTC().Format("2006-01-02T15")

	for _, tenantID := range tenants {
		id := tenantID
		if err := s.queue.Enqueue(ctx, Spec{
			TenantID:    &id,
			Kind:        KindReportSweep,
			Priority:    50,
			MaxAttempts: 3,
			DedupeKey: fmt.Sprintf("%s:%s:%s",
				KindReportSweep, id.String(), hour),
		}); err != nil {
			continue
		}
	}
}
