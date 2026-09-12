// Deciding which write-ahead log segments may be deleted, and deleting only
// those.
//
// # The one rule everything else follows from
//
// A base backup can only recover to a moment if every segment from its own
// ending position to that moment is present. So the segments that may be
// deleted are exactly those before the starting position of the OLDEST base
// backup still kept. Not the newest. Not the one from last night. The oldest,
// because that is the one that defines how far back the recovery window
// reaches, and deleting the log in front of it silently shortens the window to
// the second-oldest backup while every dashboard still says seven days.
//
// This is the relationship between the two retentions, and it only runs in one
// direction: base backup retention decides WAL retention, and WAL retention
// decides nothing. Raising the number of base backups kept costs a copy of the
// cluster each; raising the recovery window costs whatever WAL the shop
// generates in that time, which is far less. They are configured separately and
// `deploy/server/PITR.md` states the arithmetic.
//
// # Why this refuses more often than it deletes
//
// Every branch that cannot establish a horizon returns without deleting
// anything. No completed base backup, a manifest that cannot be read, a listing
// that came back short — each of those is a reason to leave the archive alone,
// because the cost of keeping a segment too long is a fraction of a penny and
// the cost of deleting one too early is a recovery window with a hole in the
// middle that nobody discovers until they need it.
//
// # Timeline history files are never deleted
//
// They are a few hundred bytes each, there is one per promotion, and without
// them a recovery cannot work out which branch a segment belongs to. The
// storage they occupy for ever is less than one segment.
package backup

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/blob"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// PITRPolicy is how much recoverable history this installation keeps.
type PITRPolicy struct {
	// Window is the recovery window this installation promises. WAL is kept
	// for at least this long, and base backups are kept until one older than
	// the window is no longer the oldest thing the window needs.
	Window time.Duration

	// KeepBaseBackups is the fewest base backups kept regardless of the window.
	// Two, by default, and the second one is not redundancy for its own sake:
	// pruning down to one means a corrupt newest base backup leaves no
	// point-in-time recovery at all, on the day that is the thing being used.
	KeepBaseBackups int

	// MaxBaseBackups caps how many are kept when they are being taken more
	// often than the window would remove them. Zero means no cap.
	MaxBaseBackups int
}

// DefaultPITRPolicy is a week of recovery window and three base backups.
//
// A week because that is the span over which somebody notices a mistake:
// "the stock counts have been wrong since the weekend" is a Tuesday sentence.
// A day would be cheaper and would not cover it; a month costs four times the
// storage to reach further back than anybody has ever asked for.
var DefaultPITRPolicy = PITRPolicy{
	Window:          7 * 24 * time.Hour,
	KeepBaseBackups: 2,
	MaxBaseBackups:  8,
}

// PITRPolicyFromEnv reads the policy a deployment configured.
func PITRPolicyFromEnv() PITRPolicy {
	p := DefaultPITRPolicy
	if days := envInt("RAWSYST_PITR_RETENTION_DAYS", 0); days > 0 {
		p.Window = time.Duration(days) * 24 * time.Hour
	}
	if n := envInt("RAWSYST_PITR_KEEP_BASE_BACKUPS", 0); n > 0 {
		p.KeepBaseBackups = n
	}
	if n := envInt("RAWSYST_PITR_MAX_BASE_BACKUPS", -1); n >= 0 {
		p.MaxBaseBackups = n
	}
	return p
}

// WALPruneReport is what a prune did, or would do.
type WALPruneReport struct {
	DryRun bool `json:"dry_run"`

	WindowDays      int `json:"retention_window_days"`
	BaseBackupsKept int `json:"base_backups_kept"`

	// Horizon is the oldest segment any retained base backup still needs.
	// Everything before it on that timeline is removable and nothing at or
	// after it is, and that one string is the whole of the decision.
	Horizon           string `json:"wal_horizon_segment,omitempty"`
	HorizonBaseBackup string `json:"wal_horizon_base_backup,omitempty"`
	HorizonTimeline   uint32 `json:"wal_horizon_timeline,omitempty"`

	RemovedBaseBackups []string `json:"removed_base_backups,omitempty"`
	RemovedSegments    int      `json:"removed_segments"`
	RemovedBytes       int64    `json:"removed_bytes"`
	KeptSegments       int      `json:"kept_segments"`
	KeptBytes          int64    `json:"kept_bytes"`

	// Orphans are segments on a timeline no retained base backup sits on.
	// Reported separately from the ordinary removals because they usually mean
	// something an operator wants to know about — a recovery that was promoted
	// and then abandoned, or two clusters sharing a prefix.
	Orphans        []string `json:"orphaned_segments,omitempty"`
	OrphanSegments int      `json:"orphaned_segment_count,omitempty"`

	// Refused is set when nothing was deleted and something should have been.
	// A prune that cannot establish a horizon reports this rather than
	// succeeding quietly, because a retention routine that silently does
	// nothing is indistinguishable from one that is working until the disk
	// fills.
	Refused string `json:"refused,omitempty"`

	Gaps []WALGap `json:"gaps,omitempty"`
}

// PruneWAL removes the archive nothing can still need.
//
// `dryRun` is what the API offers by default and what the CLI prints without
// `-apply`: this is a routine that deletes the only copy of something, so
// saying what it would do is the ordinary mode and doing it is the one that has
// to be asked for.
func PruneWAL(
	ctx context.Context, opts WALOptions, policy PITRPolicy, dryRun bool,
) (WALPruneReport, error) {
	opts = opts.withDefaults()
	if policy.Window <= 0 {
		policy = DefaultPITRPolicy
	}
	report := WALPruneReport{
		DryRun:     dryRun,
		WindowDays: int(policy.Window / (24 * time.Hour)),
	}
	if !opts.Configured() {
		return report, errs.New(errs.CodeUnavailable,
			"No object store is configured.")
	}

	bases, err := CompletedBaseBackups(ctx, opts)
	if err != nil {
		return report, err
	}
	if len(bases) == 0 {
		report.Refused = "There is no completed base backup, so there is no " +
			"way to say which segments are still needed. Nothing was removed."
		return report, nil
	}

	keep, drop := planBaseBackups(bases, policy, time.Now().UTC())
	report.BaseBackupsKept = len(keep)
	if len(keep) == 0 {
		report.Refused = "The retention policy would keep no base backup at " +
			"all. Refusing: that is not retention, it is deletion. Nothing " +
			"was removed."
		return report, nil
	}

	// The oldest kept base backup decides the horizon.
	oldest := keep[len(keep)-1]
	horizon, ok := ParseWALSegment(oldest.StartSegment)
	if !ok {
		report.Refused = "The oldest base backup kept (" + oldest.ID +
			") does not record which segment it starts at, so the earliest " +
			"segment still needed cannot be established. Nothing was removed."
		return report, nil
	}
	report.Horizon = horizon.String()
	report.HorizonBaseBackup = oldest.ID
	report.HorizonTimeline = horizon.Timeline

	inv, err := ReadArchive(ctx, opts)
	if err != nil {
		return report, err
	}
	for _, t := range inv.Timelines {
		report.Gaps = append(report.Gaps, t.Gaps...)
	}

	objects, err := opts.Store.ListDetailed(ctx, WALPrefix(opts.Prefix))
	if err != nil {
		return report, err
	}
	cutoff := time.Now().UTC().Add(-policy.Window)

	removable, kept, keptBytes, removeBytes, orphans :=
		classifySegments(objects, opts, horizon, cutoff)

	report.KeptSegments = kept
	report.KeptBytes = keptBytes
	report.OrphanSegments = len(orphans)
	report.Orphans = clip(orphans, 20)

	if dryRun {
		report.RemovedSegments = len(removable) / 2
		report.RemovedBytes = removeBytes
		for _, b := range drop {
			report.RemovedBaseBackups = append(report.RemovedBaseBackups, b.ID)
		}
		return report, nil
	}

	// Base backups first, then their WAL. In that order on purpose: a base
	// backup whose segments were deleted first is a backup that claims to be
	// recoverable and is not, and this routine can be interrupted at any point
	// by a container being stopped.
	for _, b := range drop {
		if _, err := DeleteBaseBackup(ctx, opts, b.ID); err != nil {
			return report, err
		}
		report.RemovedBaseBackups = append(report.RemovedBaseBackups, b.ID)
	}
	for _, key := range removable {
		if err := opts.Store.Delete(ctx, key); err != nil {
			if errs.CodeOf(err) == errs.CodeNotFound {
				continue
			}
			return report, err
		}
	}
	report.RemovedSegments = len(removable) / 2
	report.RemovedBytes = removeBytes
	return report, nil
}

// planBaseBackups decides which base backups are kept and which go.
//
// Newest first in, newest first out. Three rules, applied in this order, and
// the order is the point: the floor wins over the window, and the window wins
// over the cap.
//
//  1. Always keep at least `KeepBaseBackups`, whatever their age.
//  2. Keep everything whose window has not expired — which is every base
//     backup that is still needed to reach the START of the recovery window,
//     not merely every one taken inside it. The distinction matters: a window
//     of seven days needs the newest base backup taken BEFORE seven days ago,
//     because that is the one the oldest recoverable moment replays onto.
//  3. Then apply the cap, from the oldest end.
func planBaseBackups(
	bases []BaseManifest, policy PITRPolicy, now time.Time,
) (keep, drop []BaseManifest) {
	sorted := append([]BaseManifest(nil), bases...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID > sorted[j].ID })

	floor := policy.KeepBaseBackups
	if floor < 1 {
		floor = 1
	}
	cutoff := now.Add(-policy.Window)

	// Rule 2, expressed as an index: the first base backup at or before the
	// cutoff is the last one the window needs. Everything older than THAT is
	// beyond the window.
	needed := len(sorted)
	for i, b := range sorted {
		at, ok := b.CompletedTime()
		if !ok {
			continue
		}
		if !at.After(cutoff) {
			needed = i + 1
			break
		}
	}
	if needed < floor {
		needed = floor
	}
	if needed > len(sorted) {
		needed = len(sorted)
	}
	if policy.MaxBaseBackups > 0 && needed > policy.MaxBaseBackups {
		needed = policy.MaxBaseBackups
	}
	return sorted[:needed], sorted[needed:]
}

// classifySegments sorts every archived object into keep, remove or orphan.
//
// Returns the keys to delete — the object AND its sidecar, which is why the
// removed count is half the length — and the counts for the report.
func classifySegments(
	objects []blob.Object, opts WALOptions,
	horizon WALSegment, cutoff time.Time,
) (removable []string, kept int, keptBytes, removeBytes int64, orphans []string) {
	prefix := WALPrefix(opts.Prefix)
	byName := map[string]int64{}
	storedAt := map[string]time.Time{}
	names := []string{}

	for _, o := range objects {
		rest := strings.TrimPrefix(o.Key, prefix)
		dir, name, ok := strings.Cut(rest, "/")
		if !ok || strings.Contains(name, "/") {
			continue
		}
		// History files are never removed. See the package note.
		if dir == walHistoryPart {
			continue
		}
		bare := strings.TrimSuffix(name, walMetaSuffix)
		if !ValidWALSegmentName(bare) {
			// `.backup` labels. Left alone: they are tiny, they are never
			// required, and deleting one on the strength of arithmetic meant
			// for segments is a risk with no upside.
			continue
		}
		if _, seen := byName[bare]; !seen {
			names = append(names, bare)
		}
		byName[bare] += o.Size
		// The store's own timestamp for the object, which is when this server
		// uploaded it. Free — it came back in the listing — and it is the only
		// date available for a segment on an abandoned timeline, whose sidecar
		// would otherwise have to be fetched one request at a time.
		if !o.LastModified.IsZero() && o.LastModified.After(storedAt[bare]) {
			storedAt[bare] = o.LastModified
		}
	}
	sort.Strings(names)

	for _, name := range names {
		seg, _ := ParseWALSegment(name)
		size := byName[name]

		switch {
		case seg.Timeline == horizon.Timeline:
			if seg.Ordinal(opts.Layout) < horizon.Ordinal(opts.Layout) {
				removable = appendSegmentKeys(removable, opts, name)
				removeBytes += size
			} else {
				kept++
				keptBytes += size
			}

		case seg.Timeline > horizon.Timeline:
			// A timeline NEWER than the horizon's exists only because
			// something was promoted after the oldest kept base backup was
			// taken. It may well be the live one. Kept.
			kept++
			keptBytes += size

		default:
			// An older timeline. No retained base backup sits on it, so
			// nothing can replay it — but it is reported as an orphan and
			// removed only once it is outside the window, because "nothing can
			// use this" is exactly the reasoning that deletes the archive of a
			// cluster somebody is about to ask about.
			orphans = append(orphans, name)
			if at := storedAt[name]; !at.IsZero() && at.Before(cutoff) {
				removable = appendSegmentKeys(removable, opts, name)
				removeBytes += size
			} else {
				kept++
				keptBytes += size
			}
		}
	}
	return removable, kept, keptBytes, removeBytes, orphans
}

func appendSegmentKeys(into []string, opts WALOptions, name string) []string {
	key, err := WALSegmentKey(opts.Prefix, name)
	if err != nil {
		return into
	}
	return append(into, key, WALMetaKey(key))
}
