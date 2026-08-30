package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Handler runs one kind of job.
//
// Returning nil completes it. Returning a Permanent error fails it without
// retrying. Anything else is retried on the backoff schedule.
type Handler interface {
	Run(ctx context.Context, j Job) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, j Job) error

func (f HandlerFunc) Run(ctx context.Context, j Job) error { return f(ctx, j) }

// Permanent wraps an error that retrying could never fix.
//
// The distinction is the whole of E1.2's retry policy: transport failures
// retry, business rejections do not. A rejected invoice retried forever would
// never succeed AND would keep the queue busy enough that the critical alert
// goes unread.
type Permanent struct{ Err error }

func (p Permanent) Error() string { return p.Err.Error() }
func (p Permanent) Unwrap() error { return p.Err }

// IsPermanent reports whether retrying is pointless.
func IsPermanent(err error) bool {
	var p Permanent
	return errors.As(err, &p)
}

// Worker drains the queue.
type Worker struct {
	queue    *Queue
	log      *slog.Logger
	name     string
	handlers map[string]Handler

	// IdleWait is how long to pause when the queue is empty. Short enough that
	// a ZATCA retry sweep feels immediate, long enough not to hammer the
	// database with claim queries all night.
	IdleWait time.Duration

	// ReapAfter is how long a running job may hold its lock before it is
	// assumed abandoned. Longer than the slowest handler, or a job still
	// working would be released and run twice.
	ReapAfter time.Duration

	// KeepJobsFor is how long a finished job is kept before it is deleted.
	// CredentialGrace is how far past expiry a refresh token is kept, which is
	// how long replay detection can still recognise a stolen one.
	// PruneEvery is how often both are swept.
	KeepJobsFor     time.Duration
	CredentialGrace time.Duration
	PruneEvery      time.Duration

	// CalendarHorizon is how far ahead every company's accounting calendar is
	// kept open. Swept on the same ticker as the pruning, because both are
	// housekeeping that has to happen and neither is urgent to the minute.
	//
	// A year. Shorter would work -- the calendar only has to outlast the gap
	// between two sweeps -- but a year means a database restored from an old
	// backup, or a worker that has been down for a month, still has periods
	// to post into while somebody works out what happened.
	CalendarHorizon time.Duration
}

func NewWorker(q *Queue, log *slog.Logger, name string) *Worker {
	return &Worker{
		queue:     q,
		log:       log,
		name:      name,
		handlers:  map[string]Handler{},
		IdleWait:  2 * time.Second,
		ReapAfter: 15 * time.Minute,

		// Retention. Both are deliberately generous.
		//
		// A job that failed is worth reading after a weekend — the whole point
		// of recording `last_error` is that somebody looks at it on Monday —
		// and a job that succeeded is kept for the same window so the two can
		// be compared. "Did this ever work" is answered by its neighbours.
		//
		// The credential grace is what keeps replay detection working. Reuse
		// is found by recognising a token that was already rotated; deleting
		// the family the moment it expires turns "this was stolen and
		// replayed" into "this is unknown" — the same refusal with the alarm
		// removed.
		KeepJobsFor:     7 * 24 * time.Hour,
		CredentialGrace: 30 * 24 * time.Hour,
		PruneEvery:      6 * time.Hour,
		CalendarHorizon: 365 * 24 * time.Hour,
	}
}

// Register binds a handler to a job kind.
func (w *Worker) Register(kind string, h Handler) { w.handlers[kind] = h }

// Run drains the queue until the context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("worker started", slog.String("name", w.name))

	reapTicker := time.NewTicker(w.ReapAfter / 3)
	defer reapTicker.Stop()

	// Housekeeping on its own, much slower clock. Deleting a week of finished
	// jobs is not urgent and is a write; running it on the reaper's five-minute
	// tick would put a delete storm in front of the queue every five minutes
	// for no benefit.
	pruneTicker := time.NewTicker(w.PruneEvery)
	defer pruneTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("worker stopping", slog.String("name", w.name))
			return nil
		case <-reapTicker.C:
			w.reap(ctx)
		case <-pruneTicker.C:
			w.prune(ctx)
			w.rollCalendar(ctx)
		default:
		}

		worked, err := w.step(ctx)
		if err != nil {
			// A claim failure is usually the database being briefly
			// unavailable. Logging and pausing beats exiting: a worker that
			// dies on a blip stops every terminal's submissions.
			w.log.Error("claim failed", slog.String("error", err.Error()))
			if !sleep(ctx, w.IdleWait) {
				return nil
			}
			continue
		}
		if !worked {
			if !sleep(ctx, w.IdleWait) {
				return nil
			}
		}
	}
}

// step claims and runs at most one job. Exported behaviour is tested through
// RunOnce, which is this without the loop.
func (w *Worker) step(ctx context.Context) (bool, error) {
	job, found, err := w.queue.Claim(ctx, w.name)
	if err != nil || !found {
		return false, err
	}

	handler, known := w.handlers[job.Kind]
	if !known {
		// Not retried: another deploy might understand it, but silently
		// retrying forever would hide that this worker is running the wrong
		// build.
		_ = w.queue.Fail(ctx, job.ID,
			"No handler is registered for this job kind on this worker.")
		w.log.Error("no handler", slog.String("kind", job.Kind))
		return true, nil
	}

	err = w.runGuarded(ctx, handler, job)
	switch {
	case err == nil:
		if e := w.queue.Complete(ctx, job.ID); e != nil {
			w.log.Error("could not complete job",
				slog.String("job", job.String()), slog.String("error", e.Error()))
		}

	case IsPermanent(err):
		if e := w.queue.Fail(ctx, job.ID, reasonOf(err)); e != nil {
			w.log.Error("could not fail job", slog.String("error", e.Error()))
		}
		w.log.Warn("job failed permanently",
			slog.String("job", job.String()),
			slog.String("reason", reasonOf(err)))

	default:
		wait := Backoff(job.Attempts + 1)
		if e := w.queue.Retry(ctx, job, reasonOf(err), wait); e != nil {
			w.log.Error("could not reschedule job", slog.String("error", e.Error()))
		}
		w.log.Warn("job will retry",
			slog.String("job", job.String()),
			slog.Duration("in", wait),
			slog.String("reason", reasonOf(err)))
	}
	return true, nil
}

// RunOnce claims and runs a single job, for tests and for a one-shot drain.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) { return w.step(ctx) }

// Drain runs jobs until the queue has nothing ready, up to a cap.
//
// The cap is not a nicety. A handler that re-enqueues its own kind would
// otherwise spin here forever, and in a test that looks like a hang rather
// than a bug.
func (w *Worker) Drain(ctx context.Context, max int) (int, error) {
	for i := 0; i < max; i++ {
		worked, err := w.step(ctx)
		if err != nil {
			return i, err
		}
		if !worked {
			return i, nil
		}
	}
	return max, nil
}

// runGuarded stops a panicking handler from taking the worker down with it.
func (w *Worker) runGuarded(ctx context.Context, h Handler, j Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			// Retried rather than failed: a panic is a bug in this build, and
			// the next deploy may well fix it. Discarding the job would lose
			// work that was never anybody's fault.
			err = errs.Newf(errs.CodeInternal,
				"This job stopped unexpectedly and will be tried again. (%v)", r)
			w.log.Error("handler panicked",
				slog.String("job", j.String()),
				slog.Any("panic", r))
		}
	}()
	return h.Run(ctx, j)
}

// prune deletes finished jobs and expired credentials.
//
// Logged at INFO with the counts, because "the database stopped growing" is
// something an operator should be able to see happening rather than infer. A
// failure is logged and swallowed: housekeeping that cannot run is not a reason
// to stop processing work, and the next tick will try again.
// rollCalendar opens the next accounting year for any company running out.
//
// Logged only when it did something. A sweep that finds every company
// already has a year in hand is the ordinary case, four times a day, and a
// line saying so is a line that trains people to skim the worker's log.
func (w *Worker) rollCalendar(ctx context.Context) {
	made, err := w.queue.RollCalendarForward(ctx, w.CalendarHorizon)
	if err != nil {
		w.log.Error("rolling the accounting calendar forward failed",
			slog.String("error", err.Error()))
		return
	}
	if made > 0 {
		w.log.Info("accounting periods opened", slog.Int("periods", made))
	}
}

func (w *Worker) prune(ctx context.Context) {
	jobs, credentials, err := w.queue.Prune(ctx, w.KeepJobsFor, w.CredentialGrace)
	if err != nil {
		w.log.Error("prune failed", slog.String("error", err.Error()))
		return
	}
	if jobs > 0 || credentials > 0 {
		w.log.Info("pruned",
			slog.Int("finished_jobs", jobs),
			slog.Int("expired_credentials", credentials))
	}
}

func (w *Worker) reap(ctx context.Context) {
	released, err := w.queue.Reap(ctx, w.ReapAfter)
	if err != nil {
		w.log.Error("reap failed", slog.String("error", err.Error()))
		return
	}
	if released > 0 {
		w.log.Warn("released abandoned jobs", slog.Int("count", released))
	}
}

// reasonOf extracts a message safe to store on the job row.
//
// A raw driver error can carry a query fragment, and a job row is read by an
// operator dashboard rather than a developer. The typed message is written for
// a person; anything else is reduced to a generic line.
func reasonOf(err error) string {
	if e := errs.As(err); e != nil {
		return e.Message
	}
	if IsPermanent(err) {
		var p Permanent
		if errors.As(err, &p) {
			if e := errs.As(p.Err); e != nil {
				return e.Message
			}
		}
	}
	return "This job did not complete. The details are in the server log."
}

// sleep waits, returning false if the context ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
