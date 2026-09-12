// What is actually in the archive, and what window it can recover to.
//
// # The only question that matters
//
// "Can I get back to 14:32 last Tuesday?" Everything in this file exists to
// answer that with a yes or a no and never with a maybe, because a maybe is
// what an operator finds out is a no after two hours of downloading.
//
// The answer has three parts and all three have to hold:
//
//   - a base backup that finished BEFORE 14:32 and is still in the store;
//   - every segment from that backup's ending position up to 14:32, with no
//     holes;
//   - the timeline history to follow, if the cluster has ever been promoted.
//
// A gap anywhere in the middle truncates the window at the gap. Not at the end
// of the archive: replay stops at the first missing segment, so everything
// after a hole is unreachable however many segments are sitting behind it. That
// is the single most expensive misunderstanding available here, and it is why
// `RecoveryWindow` reports the window as ending at the first gap rather than at
// the newest object.
//
// # Why this reads the store rather than a table
//
// The store is the thing a recovery will actually read. A table saying a
// segment was archived is a claim about the past; a listing is the present. On
// the day this matters, the server the table lived on may be the thing that
// died — and an archive that can only be understood with the help of the
// database it protects is not an archive.
//
// The database rows in `walstate.go` are a CACHE of this for a dashboard, and
// nothing here depends on them.
package backup

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// ArchivedSegment is one segment as the store holds it.
type ArchivedSegment struct {
	Name       string     `json:"name"`
	Segment    WALSegment `json:"-"`
	Bytes      int64      `json:"stored_bytes"`
	ArchivedAt time.Time  `json:"archived_at,omitempty"`
	Encrypted  bool       `json:"encrypted"`

	// Whole is false for an object with no sidecar beside it: an upload that
	// did not finish. It is listed rather than hidden, and it counts as a GAP
	// rather than as a segment, because replay cannot use it.
	Whole bool `json:"whole"`
}

// WALGap is a run of segments the archive does not have.
type WALGap struct {
	Timeline uint32 `json:"timeline"`
	From     string `json:"from_segment"`
	To       string `json:"to_segment"`
	Count    uint64 `json:"segments_missing"`
}

// TimelineArchive is everything the store holds for one timeline.
type TimelineArchive struct {
	Timeline uint32 `json:"timeline"`

	First string `json:"first_segment,omitempty"`
	Last  string `json:"last_segment,omitempty"`

	Segments int   `json:"segments"`
	Bytes    int64 `json:"stored_bytes"`

	FirstArchivedAt time.Time `json:"first_archived_at,omitempty"`
	LastArchivedAt  time.Time `json:"last_archived_at,omitempty"`

	// ContiguousTo is the last segment reachable from `First` without crossing
	// a hole. It is the segment replay would stop at, and it is the number the
	// recovery window is computed from — never `Last`.
	ContiguousTo string `json:"contiguous_to,omitempty"`

	// ContiguousAt is when that segment was archived, which is the true end of
	// the recovery window on this timeline. Read from its sidecar, and only
	// when there IS a gap — on an unbroken archive it is the same value as
	// `LastArchivedAt` and reading it twice would be a wasted request on the
	// path a dashboard polls.
	ContiguousAt time.Time `json:"contiguous_archived_at,omitempty"`

	Gaps []WALGap `json:"gaps,omitempty"`

	// Incomplete are objects with no sidecar: uploads that did not finish.
	Incomplete []string `json:"incomplete,omitempty"`
}

// ArchiveInventory is the whole archive, read from the store.
type ArchiveInventory struct {
	Prefix    string `json:"prefix"`
	Bucket    string `json:"bucket,omitempty"`
	ReadAt    string `json:"read_at"`
	Encrypted bool   `json:"encrypted"`

	SegmentSize int64 `json:"wal_segment_size_bytes"`

	Timelines []TimelineArchive `json:"timelines"`
	Histories []uint32          `json:"timeline_histories,omitempty"`

	Segments int   `json:"total_segments"`
	Bytes    int64 `json:"total_stored_bytes"`

	// Partial is true when the listing was cut short. A window computed from a
	// partial listing would report gaps that are not there, so every consumer
	// checks this before believing the rest.
	Partial bool `json:"listing_partial,omitempty"`
}

// Timeline finds one timeline's archive.
func (a ArchiveInventory) Timeline(tl uint32) (TimelineArchive, bool) {
	for _, t := range a.Timelines {
		if t.Timeline == tl {
			return t, true
		}
	}
	return TimelineArchive{}, false
}

// TotalGaps counts the holes across every timeline.
func (a ArchiveInventory) TotalGaps() int {
	n := 0
	for _, t := range a.Timelines {
		n += len(t.Gaps)
	}
	return n
}

// ReadArchive lists what the store holds and works out where the holes are.
//
// One listing per timeline plus one for the history files. Sidecars are read
// only for the first and last segment of each timeline and for the boundaries
// of each gap — reading every one would be a request per segment, which for a
// week of archive is thousands of round trips to answer a question a dashboard
// asks every minute.
func ReadArchive(
	ctx context.Context, opts WALOptions,
) (ArchiveInventory, error) {
	opts = opts.withDefaults()
	if !opts.Configured() {
		return ArchiveInventory{}, errs.New(errs.CodeUnavailable,
			"No object store is configured, so there is no write-ahead log "+
				"archive to read.")
	}

	inv := ArchiveInventory{
		Prefix:      cleanPrefix(opts.Prefix),
		Bucket:      opts.Store.Bucket(),
		ReadAt:      time.Now().UTC().Format(time.RFC3339),
		Encrypted:   opts.Key.Set(),
		SegmentSize: opts.Layout.SegmentSize,
	}

	keys, err := opts.Store.List(ctx, WALPrefix(opts.Prefix))
	if err != nil {
		return ArchiveInventory{}, err
	}

	// Objects and sidecars are separate keys. Group first, then decide what is
	// whole: an object with no sidecar is an unfinished upload, and a sidecar
	// with no object is the more alarming inverse — something deleted the
	// segment and left the record of it.
	type entry struct{ object, meta bool }
	byTimeline := map[uint32]map[string]*entry{}
	histories := map[uint32]bool{}
	prefix := WALPrefix(opts.Prefix)

	for _, key := range keys {
		rest := strings.TrimPrefix(key, prefix)
		dir, name, ok := strings.Cut(rest, "/")
		if !ok || strings.Contains(name, "/") {
			continue
		}
		isMeta := strings.HasSuffix(name, walMetaSuffix)
		bare := strings.TrimSuffix(name, walMetaSuffix)

		if dir == walHistoryPart {
			if tl, ok := TimelineOfHistory(bare); ok && !isMeta {
				histories[tl] = true
			}
			continue
		}
		seg, ok := ParseWALSegment(bare)
		if !ok {
			// `.backup` label files live here too and are not segments. They
			// are never required for a recovery, so they are neither counted
			// nor treated as a gap.
			continue
		}
		if byTimeline[seg.Timeline] == nil {
			byTimeline[seg.Timeline] = map[string]*entry{}
		}
		e := byTimeline[seg.Timeline][bare]
		if e == nil {
			e = &entry{}
			byTimeline[seg.Timeline][bare] = e
		}
		if isMeta {
			e.meta = true
		} else {
			e.object = true
		}
	}

	for tl, entries := range byTimeline {
		names := make([]string, 0, len(entries))
		var incomplete []string
		for name, e := range entries {
			if e.object && e.meta {
				names = append(names, name)
				continue
			}
			incomplete = append(incomplete, name)
		}
		sort.Strings(names)
		sort.Strings(incomplete)

		arc := TimelineArchive{
			Timeline:   tl,
			Segments:   len(names),
			Incomplete: incomplete,
		}
		if len(names) > 0 {
			arc.First, arc.Last = names[0], names[len(names)-1]
			arc.Gaps, arc.ContiguousTo = findGaps(names, opts.Layout)

			// Two sidecar reads per timeline, for the two timestamps a window
			// is made of. Everything else in this listing is free.
			if m, ok, err := ReadWALMeta(ctx, opts, arc.First); err == nil && ok {
				arc.FirstArchivedAt, _ = m.ArchivedTime()
			}
			if m, ok, err := ReadWALMeta(ctx, opts, arc.Last); err == nil && ok {
				arc.LastArchivedAt, _ = m.ArchivedTime()
			}
			// A third read, only when a gap has cut the reachable run short of
			// the newest object. That is the moment the window actually ends,
			// and without it the window would be reported as ending at a
			// segment replay can never arrive at.
			if arc.ContiguousTo != "" && arc.ContiguousTo != arc.Last {
				if m, ok, err := ReadWALMeta(
					ctx, opts, arc.ContiguousTo,
				); err == nil && ok {
					arc.ContiguousAt, _ = m.ArchivedTime()
				}
			} else {
				arc.ContiguousAt = arc.LastArchivedAt
			}
		}
		inv.Timelines = append(inv.Timelines, arc)
		inv.Segments += arc.Segments
	}
	sort.Slice(inv.Timelines, func(i, j int) bool {
		return inv.Timelines[i].Timeline < inv.Timelines[j].Timeline
	})

	for tl := range histories {
		inv.Histories = append(inv.Histories, tl)
	}
	sort.Slice(inv.Histories, func(i, j int) bool {
		return inv.Histories[i] < inv.Histories[j]
	})
	return inv, nil
}

// findGaps walks a sorted run of segment names looking for holes.
//
// Returns the holes and the last segment reachable from the first without
// crossing one. The second is the important return: replay stops at the FIRST
// hole, so an archive of a thousand segments with one missing at position ten
// can recover to position nine and no further, however many objects are sitting
// behind the hole.
func findGaps(names []string, layout WALLayout) ([]WALGap, string) {
	if len(names) == 0 {
		return nil, ""
	}
	var gaps []WALGap
	contiguous := names[0]
	sawGap := false

	prev, ok := ParseWALSegment(names[0])
	if !ok {
		return nil, ""
	}
	for _, name := range names[1:] {
		cur, ok := ParseWALSegment(name)
		if !ok {
			continue
		}
		want := prev.Next(layout)
		if cur != want {
			missing := uint64(0)
			if cur.Timeline == want.Timeline && want.Ordinal(layout) <= cur.Ordinal(layout) {
				missing = cur.Ordinal(layout) - want.Ordinal(layout)
			}
			gaps = append(gaps, WALGap{
				Timeline: prev.Timeline,
				From:     want.String(),
				To:       prevSegment(cur, layout).String(),
				Count:    missing,
			})
			sawGap = true
		} else if !sawGap {
			contiguous = name
		}
		prev = cur
	}
	return gaps, contiguous
}

// prevSegment is the segment before this one, for naming the far end of a gap.
func prevSegment(s WALSegment, l WALLayout) WALSegment {
	if s.Segment == 0 {
		if s.LogID == 0 {
			return s
		}
		return WALSegment{
			Timeline: s.Timeline,
			LogID:    s.LogID - 1,
			Segment:  uint32(l.SegmentsPerLogID() - 1),
		}
	}
	return WALSegment{Timeline: s.Timeline, LogID: s.LogID, Segment: s.Segment - 1}
}

// --- the window -------------------------------------------------------------

// RecoveryWindow is the span of time this installation can actually recover to.
//
// Every field is a claim that can be checked, and the one an operator reads is
// `Available`. False means there is no point-in-time recovery right now, and
// `Because` says which of the three preconditions is missing rather than
// leaving somebody to work it out from the other fields.
type RecoveryWindow struct {
	Available bool   `json:"available"`
	Because   string `json:"unavailable_because,omitempty"`

	// Start is the earliest moment recoverable, which is when the OLDEST
	// retained base backup finished. Not when it started: during the copy the
	// cluster on disk is inconsistent, and a target inside that span is not a
	// target.
	Start time.Time `json:"start,omitempty"`

	// End is the latest moment recoverable. The archive's last CONTIGUOUS
	// segment, not its last object — see `findGaps`.
	End time.Time `json:"end,omitempty"`

	Timeline uint32 `json:"timeline,omitempty"`

	EarliestBaseBackup string    `json:"earliest_base_backup,omitempty"`
	LatestBaseBackup   string    `json:"latest_base_backup,omitempty"`
	LatestBaseBackupAt time.Time `json:"latest_base_backup_at,omitempty"`
	BaseBackups        int       `json:"base_backups"`

	LastSegment    string    `json:"last_contiguous_segment,omitempty"`
	LastArchivedAt time.Time `json:"last_archived_at,omitempty"`

	// Gaps are reported even when the window is available: a hole between two
	// older base backups does not stop a recovery to yesterday, and an operator
	// still needs to know the archive is not what they think it is.
	Gaps []WALGap `json:"gaps,omitempty"`

	// Truncated is true when a gap cut the window short of the newest thing in
	// the archive. It is the difference between "we have seven days" and "we
	// have seven days of objects and two days of recovery".
	Truncated bool `json:"truncated_by_gap,omitempty"`

	ReadAt time.Time `json:"read_at"`
}

// Covers reports whether a moment is inside the window.
func (w RecoveryWindow) Covers(at time.Time) bool {
	if !w.Available {
		return false
	}
	at = at.UTC()
	return !at.Before(w.Start) && !at.After(w.End)
}

// ComputeRecoveryWindow reads the store and says what can be recovered to.
func ComputeRecoveryWindow(
	ctx context.Context, opts WALOptions,
) (RecoveryWindow, error) {
	opts = opts.withDefaults()
	window := RecoveryWindow{ReadAt: time.Now().UTC()}

	bases, err := CompletedBaseBackups(ctx, opts)
	if err != nil {
		return window, err
	}
	inv, err := ReadArchive(ctx, opts)
	if err != nil {
		return window, err
	}
	return windowFrom(bases, inv, window.ReadAt), nil
}

// windowFrom is the arithmetic, separated from the two listings that feed it so
// that every branch of it can be tested without a bucket.
func windowFrom(
	bases []BaseManifest, inv ArchiveInventory, readAt time.Time,
) RecoveryWindow {
	w := RecoveryWindow{ReadAt: readAt.UTC(), BaseBackups: len(bases)}
	for _, t := range inv.Timelines {
		w.Gaps = append(w.Gaps, t.Gaps...)
	}

	if len(bases) == 0 {
		w.Because = "No physical base backup has been taken. The write-ahead " +
			"log describes changes to pages and cannot be replayed without " +
			"the pages to replay them onto, so an archive on its own recovers " +
			"nothing."
		return w
	}

	// Newest first, which is how CompletedBaseBackups orders them.
	newest, oldest := bases[0], bases[len(bases)-1]
	w.LatestBaseBackup, w.EarliestBaseBackup = newest.ID, oldest.ID
	w.Timeline = newest.Timeline
	if at, ok := newest.CompletedTime(); ok {
		w.LatestBaseBackupAt = at
	}
	start, ok := oldest.CompletedTime()
	if !ok {
		w.Because = "The oldest base backup does not say when it finished, so " +
			"the earliest recoverable moment cannot be established."
		return w
	}
	w.Start = start

	arc, found := inv.Timeline(newest.Timeline)
	if !found || arc.Segments == 0 {
		// A base backup with no archive behind it still recovers to exactly
		// one moment — its own consistency point — because it carries the WAL
		// written while it ran. Worth saying, because it is the state a new
		// installation is in for the first hour and it is not a failure.
		if at, ok := newest.CompletedTime(); ok {
			w.Available = true
			w.Start, w.End = start, at
			w.Because = ""
			return w
		}
		w.Because = "Nothing has been archived on the current timeline yet."
		return w
	}

	w.LastSegment = arc.ContiguousTo
	if w.LastSegment == "" {
		w.LastSegment = arc.Last
	}
	w.LastArchivedAt = arc.LastArchivedAt
	w.Truncated = w.LastSegment != arc.Last

	// A gap cuts the window at the gap, never at the newest object. Replay
	// stops at the first missing segment, so the end of the window is when the
	// last REACHABLE segment was archived.
	w.End = arc.ContiguousAt
	if !w.Truncated && w.End.IsZero() {
		w.End = arc.LastArchivedAt
	}

	// And when that is not known — an archive whose sidecar could not be read,
	// or a gap this build could not date — the window ends at the newest base
	// backup, which is the last moment that is CERTAINLY reachable because the
	// backup carries the log it needs to get there.
	//
	// Being pessimistic is the only safe direction here. Understating the
	// window offers an operator less choice; overstating it produces a recovery
	// that stops silently short of the moment they asked for.
	if w.End.IsZero() || w.Truncated && w.End.After(arc.LastArchivedAt) {
		if at, ok := newest.CompletedTime(); ok {
			w.End = at
		}
	}
	if w.End.Before(w.Start) {
		w.End = w.Start
	}
	w.Available = true
	return w
}

// ExplainTarget says whether a requested moment can be recovered to, in the
// words an operator needs rather than as a boolean.
//
// Called before a restore is queued and again before it runs. The first call is
// what stops somebody waiting an hour to be told no; the second is what stops a
// window that moved in between from being acted on.
func ExplainTarget(w RecoveryWindow, at time.Time) error {
	at = at.UTC()
	if !w.Available {
		reason := w.Because
		if reason == "" {
			reason = "There is no recovery window."
		}
		return errs.New(errs.CodeInvalidInput, reason)
	}
	if at.Before(w.Start) {
		return errs.Newf(errs.CodeInvalidInput,
			"%s is before the recovery window, which starts at %s. The oldest "+
				"base backup still kept (%s) finished then, and nothing can be "+
				"replayed onto a cluster that does not exist. Recovering to an "+
				"earlier moment would need a base backup from before it, and "+
				"retention has removed them.",
			at.Format(time.RFC3339), w.Start.Format(time.RFC3339),
			w.EarliestBaseBackup)
	}
	if at.After(w.End) {
		extra := ""
		if w.Truncated {
			extra = " The archive holds segments past that point, but there " +
				"is a gap before them and replay stops at the first hole."
		}
		return errs.Newf(errs.CodeInvalidInput,
			"%s is after the recovery window, which ends at %s.%s",
			at.Format(time.RFC3339), w.End.Format(time.RFC3339), extra)
	}
	return nil
}

// BaseBackupFor picks the base backup a recovery to a moment should start from.
//
// The NEWEST one that finished at or before the target. Newest, because every
// segment between the base backup and the target has to be fetched and
// replayed, and starting from a week earlier is a recovery that takes a week's
// worth of replaying to arrive at the same place.
func BaseBackupFor(bases []BaseManifest, at time.Time) (BaseManifest, error) {
	at = at.UTC()
	var best BaseManifest
	found := false
	for _, b := range bases {
		done, ok := b.CompletedTime()
		if !ok || done.After(at) {
			continue
		}
		if !found || done.After(mustTime(best.CompletedTime())) {
			best, found = b, true
		}
	}
	if !found {
		return BaseManifest{}, errs.Newf(errs.CodeInvalidInput,
			"No base backup finished before %s, so there is nothing to replay "+
				"the write-ahead log onto. The earliest recoverable moment is "+
				"when the oldest retained base backup finished.",
			at.Format(time.RFC3339))
	}
	return best, nil
}

func mustTime(t time.Time, _ bool) time.Time { return t }
