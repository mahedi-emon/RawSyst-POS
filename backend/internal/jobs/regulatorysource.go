package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
)

// Checking whether the law has changed under a figure this product computes
// with.
//
// # What it does
//
// Once a day, for each rule whose figures came out of a retrieved document, it
// asks the authority for that document again. If the bytes hash the same,
// nothing happens and nothing is written: an unchanged source is the expected
// answer on all but a handful of days in a decade.
//
// If the bytes differ, the new document is recorded as a CANDIDATE, read, and
// left there. A platform operator finds it on the regulatory sources screen
// with the reading beside the reading that is currently in force, and decides.
//
// # What it deliberately does not do
//
// It does not apply anything. Not even when the new reading validates, not even
// when it is identical to the one in force. A legal value in this registry
// carries somebody's name and the date they put it there, and a job cannot
// supply either. An amended statute is also exactly the moment when a machine
// reading is least trustworthy — the amendment may add an article, change a
// threshold, or reword a sentence in a way the reader half-matches — so the one
// case where automation would save the most work is the case where it should
// do the least.
//
// # Why it is cheap
//
// One HTTP request per applied source per day, with a thirty-second ceiling,
// no retries of its own and no state. There are two applied sources on a full
// installation. This is not a crawler and must never become one: the ceiling on
// what it costs is the number of legal values this product holds, which is
// bounded by the number of laws it implements.

// KindRegulatorySourceRefresh re-checks the documents legal values came from.
const KindRegulatorySourceRefresh = "regulatory.source.refresh"

// RegulatorySourceRefresher re-retrieves applied sources and files a candidate
// when one has changed.
type RegulatorySourceRefresher struct {
	rules *registry.Service
	log   *slog.Logger
}

func NewRegulatorySourceRefresher(
	rules *registry.Service, log *slog.Logger,
) *RegulatorySourceRefresher {
	return &RegulatorySourceRefresher{rules: rules, log: log}
}

// Handle performs one pass.
//
// A failure to reach one authority is logged and the pass continues: a ministry
// being down for an afternoon is not a reason to skip the other rules, and it
// is certainly not a reason to fail a job and have the queue retry a government
// website every few minutes.
func (r *RegulatorySourceRefresher) Run(ctx context.Context, job Job) error {
	outcomes, err := r.rules.RefreshSources(ctx, uuid.Nil)
	if err != nil {
		return err
	}
	for _, o := range outcomes {
		switch {
		case o.Err != "":
			r.log.Warn("a regulatory source could not be re-checked",
				slog.String("rule", o.RuleKey),
				slog.String("url", o.URL),
				slog.String("error", o.Err),
				slog.String("note", "the figure in force is unaffected; this "+
					"is a check that the published document has not changed"))
		case o.Changed:
			r.log.Warn("a regulatory source has changed since it was applied",
				slog.String("rule", o.RuleKey),
				slog.String("url", o.URL),
				slog.String("was", o.PreviousSHA256),
				slog.String("now", o.SHA256),
				slog.String("candidate", o.DocumentID),
				slog.String("note", "recorded as a candidate and NOT applied. "+
					"Review it in Super Admin > Regulatory Sources: an "+
					"amended statute is where a machine reading is least "+
					"trustworthy, so applying one is a person's act"))
		default:
			r.log.Info("regulatory source unchanged",
				slog.String("rule", o.RuleKey),
				slog.String("sha256", o.SHA256))
		}
	}
	return nil
}

// RefreshAt is the hour, UTC, at which sources are re-checked.
//
// 03:00, an hour before the books are reconciled and well outside the working
// day in either market. Nothing depends on the answer arriving at a particular
// time; what matters is that it is once a day rather than once a tick.
const RefreshAt = 3

// enqueueRegulatoryRefresh queues the daily re-check.
//
// One job for the whole platform, not one per tenant: the Saudi Labour Law is
// the same document for every business, and retrieving it once per tenant would
// turn a courtesy into a small denial of service.
//
// The dedupe key carries the date, so the pass runs once on the first tick
// after 03:00 and does nothing on the other 1,439 — and an installation that
// was down at 03:00 still checks when it comes back.
func (s *Scheduler) enqueueRegulatoryRefresh(ctx context.Context) {
	now := time.Now().UTC()
	if now.Hour() < RefreshAt {
		return
	}
	if err := s.queue.Enqueue(ctx, Spec{
		Kind:        KindRegulatorySourceRefresh,
		Priority:    80,
		MaxAttempts: 2,
		DedupeKey: "regulatory.source.refresh:" +
			now.Format("2006-01-02"),
	}); err != nil {
		s.log.Error("could not enqueue the regulatory source re-check",
			slog.String("error", err.Error()))
	}
}
