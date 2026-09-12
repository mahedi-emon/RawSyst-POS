// Whether the archive is working, answered from what PostgreSQL already knows.
//
// # Why almost none of this is counted by this product
//
// PostgreSQL maintains `pg_stat_archiver`: how many segments it has archived,
// which one was last, when, how many attempts failed, which one failed last and
// when. That is nearly the whole of the question, kept by the process that
// actually calls `archive_command`, and counting it a second time here would
// produce a number that disagrees with the database at every restart.
//
// So this reads it, adds the three things PostgreSQL does not know — how far
// behind the archive is, what the bucket actually contains, and how much local
// WAL has piled up — and writes one row a dashboard can read.
//
// # The number that turns into an outage
//
// `pg_wal_bytes`. When archiving fails, PostgreSQL does the right thing and
// KEEPS every segment, which is why a failed upload is safe. The cost is that
// the disk fills, and on a small server the disk that fills is the one the
// database is on. Watching that number is the whole of the early warning, and
// it is the reason `archiving is broken` is amber and `archiving is broken and
// pg_wal has grown past its ceiling` is red.
//
// # Green means something specific
//
// It means a recovery to a moment inside the window would work, as far as
// anything short of performing one can say: the archive is on, the last segment
// arrived recently, the store answered, there are no gaps, and a base backup
// exists to replay onto. Anything less is amber or red with a sentence saying
// which of those is missing. An amber that says "waiting" is worth far more
// than a green that means "nothing has obviously broken".
package backup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// The three words, and nothing in between.
const (
	ArchiveGreen = "green"
	ArchiveAmber = "amber"
	ArchiveRed   = "red"
)

// StaleArchiveAfter is how long the archive may go quiet before it is amber.
//
// Deliberately longer than `archive_timeout`, and by a wide margin. A shop is
// closed at night and a closed shop writes nothing, so a threshold near the
// timeout would put every dashboard into amber at two in the morning and teach
// whoever reads it that amber means nothing.
const StaleArchiveAfter = 30 * time.Minute

// ArchiveStatus is the whole readout.
type ArchiveStatus struct {
	Health  string `json:"health"`
	Summary string `json:"summary"`

	ObservedAt time.Time `json:"observed_at"`

	Archiving bool   `json:"archiving"`
	WALLevel  string `json:"wal_level"`
	Timeline  uint32 `json:"timeline,omitempty"`

	// Straight from pg_stat_archiver.
	LastArchivedWAL string    `json:"last_archived_segment,omitempty"`
	LastArchivedAt  time.Time `json:"last_archived_at,omitempty"`
	ArchivedCount   int64     `json:"archived_total"`
	LastFailedWAL   string    `json:"last_failed_segment,omitempty"`
	LastFailedAt    time.Time `json:"last_failed_at,omitempty"`
	FailedCount     int64     `json:"failed_total"`
	StatsResetAt    time.Time `json:"stats_reset_at,omitempty"`

	CurrentWAL  string `json:"current_segment,omitempty"`
	LagSegments int    `json:"lag_segments"`
	LagSeconds  int    `json:"lag_seconds"`

	PGWALBytes int64 `json:"pg_wal_bytes"`

	// What the store holds. Zero and `StoreReachable` false when the bucket
	// could not be listed, which is reported rather than smoothed over: an
	// archive nobody can list is an archive nobody can recover from.
	StoreReachable  bool     `json:"store_reachable"`
	StoreError      string   `json:"store_error,omitempty"`
	ArchiveSegments int      `json:"archive_segments"`
	ArchiveBytes    int64    `json:"archive_bytes"`
	ArchiveGaps     int      `json:"archive_gaps"`
	Gaps            []WALGap `json:"gaps,omitempty"`
	OldestSegment   string   `json:"oldest_archived_segment,omitempty"`
	NewestSegment   string   `json:"newest_archived_segment,omitempty"`

	BaseBackups        int       `json:"base_backups"`
	LatestBaseBackup   string    `json:"latest_base_backup,omitempty"`
	LatestBaseBackupAt time.Time `json:"latest_base_backup_at,omitempty"`

	Window RecoveryWindow `json:"recovery_window"`

	// Findings are the individual reasons the health is not green, in the
	// order they were noticed. The summary is the first of them.
	Findings []string `json:"findings,omitempty"`
}

// ArchiverStats is what PostgreSQL says about its own archiving.
type ArchiverStats struct {
	Archiving bool
	WALLevel  string
	Timeline  uint32

	LastArchivedWAL string
	LastArchivedAt  time.Time
	ArchivedCount   int64
	LastFailedWAL   string
	LastFailedAt    time.Time
	FailedCount     int64
	StatsResetAt    time.Time

	CurrentWAL string
	PGWALBytes int64

	// SegmentSize is read rather than assumed, so the lag arithmetic is done
	// with the geometry this cluster was actually initialised with.
	SegmentSize int64
}

// ReadArchiverStats asks the database how its archiving is going.
//
// # Why pg_wal's size is asked for separately and may be missing
//
// `pg_ls_waldir()` is restricted: an ordinary role cannot list the write-ahead
// log directory. The backup role is granted `pg_monitor`, which admits it. On
// an installation where that grant has not been made, the size comes back as
// zero and the readout says so rather than claiming the disk is empty — a
// health check that reports a comfortable number it could not measure is worse
// than one that reports nothing.
func ReadArchiverStats(
	ctx context.Context, dsn string,
) (ArchiverStats, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return ArchiverStats{}, errs.Wrap(err, errs.CodeUnavailable,
			"The database could not be asked how its archiving is going.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	var s ArchiverStats
	var archiveMode, segmentSize string
	var lastWAL, failedWAL *string
	var lastAt, failedAt, resetAt *time.Time
	var timeline *int32

	if err := conn.QueryRow(ctx, `
		SELECT current_setting('archive_mode'),
		       current_setting('wal_level'),
		       current_setting('wal_segment_size'),
		       a.last_archived_wal, a.last_archived_time, a.archived_count,
		       a.last_failed_wal,   a.last_failed_time,   a.failed_count,
		       a.stats_reset,
		       (SELECT timeline_id FROM pg_control_checkpoint())
		  FROM pg_stat_archiver a`).
		Scan(&archiveMode, &s.WALLevel, &segmentSize,
			&lastWAL, &lastAt, &s.ArchivedCount,
			&failedWAL, &failedAt, &s.FailedCount,
			&resetAt, &timeline); err != nil {
		return ArchiverStats{}, errs.Wrap(err, errs.CodeInternal,
			"The database would not say how its archiving is going.")
	}

	s.Archiving = archiveMode == "on" || archiveMode == "always"
	if n, err := parseByteSize(segmentSize); err == nil && n > 0 {
		s.SegmentSize = n
	} else {
		s.SegmentSize = DefaultWALSegmentSize
	}
	if lastWAL != nil {
		s.LastArchivedWAL = *lastWAL
	}
	if failedWAL != nil {
		s.LastFailedWAL = *failedWAL
	}
	if lastAt != nil {
		s.LastArchivedAt = lastAt.UTC()
	}
	if failedAt != nil {
		s.LastFailedAt = failedAt.UTC()
	}
	if resetAt != nil {
		s.StatsResetAt = resetAt.UTC()
	}
	if timeline != nil {
		s.Timeline = uint32(*timeline)
	}

	// The current segment. On a standby `pg_current_wal_lsn()` raises rather
	// than returning null, so the failure is swallowed: a readout that could
	// not name the current segment is still a useful readout.
	var current *string
	if err := conn.QueryRow(ctx,
		`SELECT pg_walfile_name(pg_current_wal_lsn())`).Scan(&current); err == nil &&
		current != nil {
		s.CurrentWAL = *current
	}

	// The local directory. Best effort, for the reason in the doc comment.
	var bytes *int64
	if err := conn.QueryRow(ctx,
		`SELECT sum(size)::bigint FROM pg_ls_waldir()`).Scan(&bytes); err == nil &&
		bytes != nil {
		s.PGWALBytes = *bytes
	}
	return s, nil
}

// BuildArchiveStatus turns the three readings into one opinion.
//
// Separated from everything that does I/O so that each branch of the judgement
// can be tested against a table of cases rather than against a bucket, a
// database and a clock.
func BuildArchiveStatus(
	stats ArchiverStats, inv ArchiveInventory, storeErr error,
	window RecoveryWindow, bases []BaseManifest, now time.Time,
) ArchiveStatus {
	now = now.UTC()
	out := ArchiveStatus{
		ObservedAt:      now,
		Archiving:       stats.Archiving,
		WALLevel:        stats.WALLevel,
		Timeline:        stats.Timeline,
		LastArchivedWAL: stats.LastArchivedWAL,
		LastArchivedAt:  stats.LastArchivedAt,
		ArchivedCount:   stats.ArchivedCount,
		LastFailedWAL:   stats.LastFailedWAL,
		LastFailedAt:    stats.LastFailedAt,
		FailedCount:     stats.FailedCount,
		StatsResetAt:    stats.StatsResetAt,
		CurrentWAL:      stats.CurrentWAL,
		PGWALBytes:      stats.PGWALBytes,
		Window:          window,
		BaseBackups:     len(bases),
	}
	if len(bases) > 0 {
		out.LatestBaseBackup = bases[0].ID
		if at, ok := bases[0].CompletedTime(); ok {
			out.LatestBaseBackupAt = at
		}
	}

	layout := WALLayout{SegmentSize: stats.SegmentSize}
	if cur, ok := ParseWALSegment(stats.CurrentWAL); ok {
		if last, ok := ParseWALSegment(stats.LastArchivedWAL); ok &&
			last.Timeline == cur.Timeline {
			if cur.Ordinal(layout) > last.Ordinal(layout) {
				out.LagSegments = int(cur.Ordinal(layout) - last.Ordinal(layout))
			}
		} else if stats.LastArchivedWAL == "" {
			// Nothing has ever been archived, so every segment this cluster
			// has written is outstanding. Counting that as lag would be a
			// number nobody can act on; the finding below says the real thing.
			out.LagSegments = 0
		}
	}
	if !stats.LastArchivedAt.IsZero() {
		out.LagSeconds = int(now.Sub(stats.LastArchivedAt).Seconds())
		if out.LagSeconds < 0 {
			out.LagSeconds = 0
		}
	}

	if storeErr != nil {
		out.StoreError = redactStoreError(storeErr)
	} else {
		out.StoreReachable = true
		out.ArchiveSegments = inv.Segments
		out.ArchiveBytes = inv.Bytes
		out.ArchiveGaps = inv.TotalGaps()
		for _, t := range inv.Timelines {
			out.Gaps = append(out.Gaps, t.Gaps...)
			if t.Timeline == stats.Timeline {
				out.OldestSegment, out.NewestSegment = t.First, t.Last
			}
		}
	}

	// The judgement. Ordered from most to least serious, and the FIRST finding
	// becomes the summary — so whatever an operator reads first is the thing
	// that matters most rather than whatever was noticed first.
	red := func(format string, args ...any) {
		out.Health = ArchiveRed
		out.Findings = append(out.Findings, fmt.Sprintf(format, args...))
	}
	amber := func(format string, args ...any) {
		if out.Health != ArchiveRed {
			out.Health = ArchiveAmber
		}
		out.Findings = append(out.Findings, fmt.Sprintf(format, args...))
	}

	switch {
	case stats.WALLevel == "minimal":
		red("wal_level is `minimal`, which does not write enough to the log " +
			"for it to be replayed anywhere else. There is no point-in-time " +
			"recovery on this server and nothing else here can change that.")
	case !stats.Archiving:
		red("archive_mode is off. Nothing is being shipped off this machine, " +
			"so the recovery point is whatever the last dump was.")
	}

	if len(bases) == 0 {
		red("No physical base backup has been taken. The archive on its own " +
			"recovers nothing: the log describes changes to pages and there " +
			"are no pages to replay them onto.")
	}

	if storeErr != nil {
		red("The object store could not be reached, so what the archive "+
			"contains is unknown. %s", out.StoreError)
	} else if out.ArchiveGaps > 0 {
		red("The archive has %d gap(s). Replay stops at the first missing "+
			"segment, so everything after one is unreachable however much of "+
			"it is there.", out.ArchiveGaps)
	}

	if stats.Archiving && stats.LastArchivedWAL == "" && stats.ArchivedCount == 0 {
		if stats.FailedCount > 0 {
			red("archive_mode is on and nothing has ever been archived "+
				"successfully; %d attempts have failed. The write-ahead log "+
				"is accumulating on this machine.", stats.FailedCount)
		} else {
			amber("Archiving is on and nothing has been archived yet. This is " +
				"what a server looks like for the first few minutes.")
		}
	}

	if stats.FailedCount > 0 && !stats.LastFailedAt.IsZero() &&
		stats.LastFailedAt.After(stats.LastArchivedAt) {
		red("The most recent archive attempt failed (%s, at %s) and nothing "+
			"has succeeded since. PostgreSQL is keeping every segment, so the "+
			"local write-ahead log is growing.",
			clipText(stats.LastFailedWAL, 32),
			stats.LastFailedAt.Format(time.RFC3339))
	}

	if stats.Archiving && !stats.LastArchivedAt.IsZero() &&
		now.Sub(stats.LastArchivedAt) > StaleArchiveAfter {
		amber("Nothing has been archived for %s. On a shop that is closed "+
			"this is ordinary; during trading hours it is not.",
			roundDuration(now.Sub(stats.LastArchivedAt)))
	}

	// Local WAL piling up. The threshold is deliberately generous — a base
	// backup and a bulk import both make this spike legitimately — and the
	// point of the amber is to be read before the red.
	if stats.PGWALBytes > walPressureRed {
		red("The local write-ahead log directory is %s. That is a disk filling "+
			"up, and the disk it is on is the database's.",
			humanBytes(stats.PGWALBytes))
	} else if stats.PGWALBytes > walPressureAmber {
		amber("The local write-ahead log directory is %s and growing.",
			humanBytes(stats.PGWALBytes))
	}

	if out.Health == "" {
		out.Health = ArchiveGreen
		out.Summary = fmt.Sprintf(
			"Archiving. %s was shipped %s ago; the window reaches back to %s.",
			clipText(stats.LastArchivedWAL, 32),
			roundDuration(time.Duration(out.LagSeconds)*time.Second),
			window.Start.Format(time.RFC3339))
		return out
	}
	out.Summary = out.Findings[0]
	return out
}

// The two thresholds for local write-ahead log pressure.
//
// `max_wal_size` on this deployment is 1 GiB, so anything approaching that is
// PostgreSQL doing its job. Four gigabytes is beyond any legitimate spike on a
// shop's database and is the point at which the disk on the server this product
// is sized for starts to matter.
const (
	walPressureAmber = 2 << 30
	walPressureRed   = 4 << 30
)

func roundDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(time.Second).String()
	case d < time.Hour:
		return d.Round(time.Minute).String()
	default:
		return d.Round(time.Hour).String()
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/float64(1<<20))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// redactStoreError keeps a credential out of a row every operator can read.
//
// An object store error can carry the URL it was made against, and a URL is the
// kind of string that carries a signature or a token. The message is kept and
// anything that looks like a query string is removed, because "the store said
// 403" is the useful half and the other half is a secret with a timestamp on
// it.
func redactStoreError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for {
		i := strings.Index(msg, "?")
		if i < 0 {
			break
		}
		end := strings.IndexAny(msg[i:], " \t\n\"')")
		if end < 0 {
			msg = msg[:i] + "?[redacted]"
			break
		}
		msg = msg[:i] + "?[redacted]" + msg[i+end:]
	}
	for _, secret := range []string{
		"X-Amz-Signature", "X-Amz-Credential", "AWS4-HMAC-SHA256",
		"Authorization",
	} {
		if idx := strings.Index(msg, secret); idx >= 0 {
			msg = msg[:idx] + "[redacted]"
			break
		}
	}
	return clipText(strings.TrimSpace(msg), 300)
}
