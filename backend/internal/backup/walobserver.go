// The loop that keeps the archive readout current.
//
// # Why the agent watches instead of the API asking
//
// The health readout needs three things: a query against PostgreSQL, a listing
// of the bucket, and a listing of the base backups. The middle one is a network
// round trip to another company. A route that did all three on demand would put
// a two-second object-store call on a dashboard poll, and a dashboard that is
// open in three tabs would make three of them.
//
// So the agent takes the reading on a timer and writes one row, and the route
// reads the row. The row says when it was taken and a reading older than
// fifteen minutes is marked stale rather than shown as though it were current,
// because a green from six hours ago describes a machine that may have stopped
// five hours ago.
//
// # The interval, and what it costs
//
// One minute. That is one small query and two listings per minute: on
// Cloudflare R2 a listing is a class-B operation, so this is about ninety
// thousand of them a month against a free allowance of ten million. On a
// metered store it is worth knowing about, and `RAWSYST_WAL_OBSERVE_INTERVAL`
// is how it is turned down.
//
// # It never fails the agent
//
// Every error here is logged and the loop continues. The observer exists to
// report on the archive; an observer that stopped the agent would take out the
// thing that does the archiving in order to complain that nothing is being
// archived.
package backup

import (
	"context"
	"log/slog"
	"time"
)

// ArchiveObserver keeps `wal_archive_state` current.
type ArchiveObserver struct {
	// DSN is the database to ask about archiving. The backup role, because
	// `pg_ls_waldir()` needs `pg_monitor` and the application role has neither
	// that nor any business with it.
	DSN string

	WAL      WALOptions
	Register *WALRegister

	// Interval is how often the reading is taken.
	Interval time.Duration

	Log *slog.Logger
}

// DefaultObserveInterval is one reading a minute.
const DefaultObserveInterval = time.Minute

// Run takes a reading on a timer until the context is cancelled.
func (o *ArchiveObserver) Run(ctx context.Context) {
	if o.Interval <= 0 {
		o.Interval = DefaultObserveInterval
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}

	// One immediately, so a freshly started agent does not show a blank
	// dashboard for a minute.
	o.once(ctx, log)

	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	trim := time.NewTicker(time.Hour)
	defer trim.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-trim.C:
			if err := o.Register.TrimArchiveFailures(ctx, 500); err != nil {
				log.Warn("the archive failure history could not be trimmed",
					slog.String("error", err.Error()))
			}
		case <-ticker.C:
			o.once(ctx, log)
		}
	}
}

// Observe takes one reading and returns it, without writing anything.
//
// Exported because the command line wants the same numbers without a database
// to cache them in — `rawsyst backup wal status` runs on a machine where the
// agent may not be running at all, and on the day it matters may be running
// beside a database that will not start.
func (o *ArchiveObserver) Observe(ctx context.Context) (ArchiveStatus, error) {
	stats, err := ReadArchiverStats(ctx, o.DSN)
	if err != nil {
		return ArchiveStatus{}, err
	}

	// The store, separately, because it failing is a finding rather than an
	// error: "the bucket could not be reached" is precisely what the readout
	// exists to say, and returning early would make it say nothing at all.
	inv, storeErr := ReadArchive(ctx, o.WAL)
	bases, baseErr := CompletedBaseBackups(ctx, o.WAL)
	if storeErr == nil && baseErr != nil {
		storeErr = baseErr
	}

	window := windowFrom(bases, inv, time.Now().UTC())
	return BuildArchiveStatus(
		stats, inv, storeErr, window, bases, time.Now().UTC()), nil
}

func (o *ArchiveObserver) once(ctx context.Context, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	status, err := o.Observe(ctx)
	if err != nil {
		log.Warn("the archive could not be observed",
			slog.String("error", err.Error()))
		return
	}
	if err := o.Register.Observe(ctx, status); err != nil {
		log.Warn("the archive reading could not be recorded",
			slog.String("error", err.Error()))
	}
	if status.LastFailedWAL != "" && !status.LastFailedAt.IsZero() {
		if err := o.Register.RecordArchiveFailure(ctx,
			status.LastFailedWAL, status.LastFailedAt,
			"PostgreSQL recorded a failed archive attempt for this segment. "+
				"The reason is in the PostgreSQL log; this product does not "+
				"copy it here, because an archive_command error can carry a "+
				"signed URL.",
		); err != nil {
			log.Warn("an archive failure could not be recorded",
				slog.String("error", err.Error()))
		}
	}

	// One line per reading would be a log entry a minute for ever. Only the
	// bad ones, and only when the judgement CHANGED, would need state; this is
	// the middle course: anything not green is worth a line, because anything
	// not green is a thing somebody should be looking at.
	if status.Health != ArchiveGreen {
		log.Warn("write-ahead log archive is not healthy",
			slog.String("health", status.Health),
			slog.String("summary", status.Summary),
			slog.Int("lag_segments", status.LagSegments),
			slog.Int64("pg_wal_bytes", status.PGWALBytes))
	}
}
