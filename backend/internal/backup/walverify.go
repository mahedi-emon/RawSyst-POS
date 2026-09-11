// Checking that the archive is what it says it is.
//
// # Two depths, because they cost different amounts
//
// The shallow check is a listing and a handful of small reads. It answers: is
// every segment accompanied by its record, is every object the length its
// record says, is the run of segments unbroken, and does the newest one date
// from recently. That is cheap enough to run every hour and it catches the
// failures that actually happen — an upload that died, a retention routine that
// took one too many, an archive that quietly stopped on Tuesday.
//
// The deep check downloads segments, decrypts them and compares checksums. It
// is the only check that proves the bytes are readable, and it is charged by
// the gigabyte, so it runs over a bounded sample: the newest segments, which
// are the ones a recovery to "now" would need first, plus the oldest, which are
// the ones nothing has read since they were written and are therefore where
// silent corruption hides.
//
// # What neither of them proves
//
// That the archive replays. Only a recovery proves that, which is what the
// drill in `pitr.go` is for, and the two are deliberately separate: a check
// cheap enough to run hourly and a rehearsal expensive enough to run nightly
// answer different questions and a product that conflated them would end up
// running neither often enough.
package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// WALVerifyReport is what a check of the archive found.
type WALVerifyReport struct {
	Passed bool `json:"passed"`
	Deep   bool `json:"deep"`

	Prefix string `json:"prefix"`
	Bucket string `json:"bucket,omitempty"`

	Segments  int   `json:"segments"`
	Bytes     int64 `json:"stored_bytes"`
	Timelines int   `json:"timelines"`
	Histories int   `json:"timeline_histories"`

	Gaps       []WALGap `json:"gaps,omitempty"`
	Incomplete []string `json:"incomplete_uploads,omitempty"`

	// WrongSize are objects whose length does not match the length their own
	// sidecar records. Free to detect — the listing carries both — and always
	// serious: the object has been replaced or truncated since it was written.
	WrongSize []string `json:"wrong_size,omitempty"`

	// Orphaned are sidecars with no object beside them. The archiver writes the
	// object first, so this state cannot be produced by an interrupted upload:
	// something removed the segment and left the record of it.
	OrphanedRecords []string `json:"orphaned_records,omitempty"`

	Sampled      int   `json:"deep_checked_segments,omitempty"`
	SampledBytes int64 `json:"deep_checked_bytes,omitempty"`

	Oldest         string    `json:"oldest_segment,omitempty"`
	Newest         string    `json:"newest_segment,omitempty"`
	NewestArchived time.Time `json:"newest_archived_at,omitempty"`

	Checked  []string `json:"checked,omitempty"`
	Findings []string `json:"findings,omitempty"`

	TookSeconds int `json:"took_seconds"`
}

func (r *WALVerifyReport) finding(format string, args ...any) {
	r.Findings = append(r.Findings, fmt.Sprintf(format, args...))
}

// VerifyArchive checks the archive.
//
// `sample` is how many segments the deep check reads; zero or negative means
// the shallow check only.
func VerifyArchive(
	ctx context.Context, opts WALOptions, sample int,
) (WALVerifyReport, error) {
	opts = opts.withDefaults()
	began := time.Now()
	report := WALVerifyReport{
		Prefix: cleanPrefix(opts.Prefix),
		Deep:   sample > 0,
	}
	if !opts.Configured() {
		return report, errs.New(errs.CodeUnavailable,
			"No object store is configured, so there is no archive to check.")
	}
	report.Bucket = opts.Store.Bucket()

	objects, err := opts.Store.ListDetailed(ctx, WALPrefix(opts.Prefix))
	if err != nil {
		return report, err
	}

	// One pass over the listing, building both halves of every pair. The
	// sidecar carries the length the object should be and the listing carries
	// the length it is, so the comparison costs nothing beyond this loop.
	type pair struct {
		objectSize int64
		hasObject  bool
		metaSize   int64
		hasMeta    bool
		timeline   uint32
	}
	pairs := map[string]*pair{}
	names := []string{}
	prefix := WALPrefix(opts.Prefix)
	histories := 0

	for _, o := range objects {
		rest := strings.TrimPrefix(o.Key, prefix)
		dir, name, ok := strings.Cut(rest, "/")
		if !ok || strings.Contains(name, "/") {
			continue
		}
		isMeta := strings.HasSuffix(name, walMetaSuffix)
		bare := strings.TrimSuffix(name, walMetaSuffix)
		if dir == walHistoryPart {
			if !isMeta && ValidTimelineHistoryName(bare) {
				histories++
			}
			continue
		}
		seg, ok := ParseWALSegment(bare)
		if !ok {
			continue
		}
		p := pairs[bare]
		if p == nil {
			p = &pair{timeline: seg.Timeline}
			pairs[bare] = p
			names = append(names, bare)
		}
		if isMeta {
			p.hasMeta, p.metaSize = true, o.Size
		} else {
			p.hasObject, p.objectSize = true, o.Size
		}
	}
	sort.Strings(names)
	report.Histories = histories

	timelines := map[uint32]bool{}
	whole := []string{}
	for _, name := range names {
		p := pairs[name]
		timelines[p.timeline] = true
		switch {
		case p.hasObject && p.hasMeta:
			whole = append(whole, name)
			report.Segments++
			report.Bytes += p.objectSize
		case p.hasObject:
			report.Incomplete = append(report.Incomplete, name)
		case p.hasMeta:
			report.OrphanedRecords = append(report.OrphanedRecords, name)
		}
	}
	report.Timelines = len(timelines)
	if len(whole) > 0 {
		report.Oldest, report.Newest = whole[0], whole[len(whole)-1]
	}
	report.Checked = append(report.Checked, fmt.Sprintf(
		"%d segments across %d timeline(s), %d timeline history file(s)",
		report.Segments, report.Timelines, report.Histories))

	if len(report.Incomplete) > 0 {
		report.finding(
			"%d segment(s) are in the store without the record that says what "+
				"is in them, which means an upload did not finish. They cannot "+
				"be used and they count as gaps: %s",
			len(report.Incomplete), strings.Join(clip(report.Incomplete, 5), ", "))
	}
	if len(report.OrphanedRecords) > 0 {
		report.finding(
			"%d record(s) describe segments that are not in the store. The "+
				"archiver writes the segment first, so this cannot be an "+
				"interrupted upload: something removed the objects: %s",
			len(report.OrphanedRecords),
			strings.Join(clip(report.OrphanedRecords, 5), ", "))
	}

	// Gaps, per timeline, using the geometry the archive was written under.
	byTimeline := map[uint32][]string{}
	for _, name := range whole {
		seg, _ := ParseWALSegment(name)
		byTimeline[seg.Timeline] = append(byTimeline[seg.Timeline], name)
	}
	for tl := range byTimeline {
		gaps, _ := findGaps(byTimeline[tl], opts.Layout)
		report.Gaps = append(report.Gaps, gaps...)
	}
	sort.Slice(report.Gaps, func(i, j int) bool {
		return report.Gaps[i].From < report.Gaps[j].From
	})
	if len(report.Gaps) > 0 {
		report.finding(
			"The archive has %d gap(s). Replay stops at the first missing "+
				"segment, so a recovery cannot reach past %s.",
			len(report.Gaps), report.Gaps[0].From)
	} else if report.Segments > 0 {
		report.Checked = append(report.Checked,
			"the run of segments is unbroken on every timeline")
	}

	// Sizes against the sidecars. One small read per segment, which for a
	// week's archive is a few hundred; bounded so a very large archive does
	// not turn an hourly check into a thousand requests.
	checkedSizes := 0
	for _, name := range whole {
		if checkedSizes >= sizeCheckLimit {
			break
		}
		checkedSizes++
		meta, found, err := ReadWALMeta(ctx, opts, name)
		if err != nil || !found {
			continue
		}
		if meta.StoredBytes > 0 && meta.StoredBytes != pairs[name].objectSize {
			report.WrongSize = append(report.WrongSize, name)
		}
		if name == report.Newest {
			if at, ok := meta.ArchivedTime(); ok {
				report.NewestArchived = at
			}
		}
	}
	if len(report.WrongSize) > 0 {
		report.finding(
			"%d segment(s) are not the length their own record says. They have "+
				"been truncated or replaced in the store: %s",
			len(report.WrongSize), strings.Join(clip(report.WrongSize, 5), ", "))
	} else if checkedSizes > 0 {
		report.Checked = append(report.Checked, fmt.Sprintf(
			"%d segment(s) are the length their record says", checkedSizes))
	}

	if sample > 0 && len(whole) > 0 {
		if err := deepCheck(ctx, opts, whole, sample, &report); err != nil {
			return report, err
		}
	}

	report.Passed = len(report.Findings) == 0
	report.TookSeconds = int(time.Since(began).Seconds())
	return report, nil
}

// sizeCheckLimit bounds the cheap half.
//
// Four hundred small reads is a few seconds and covers well over a week of a
// shop's archive. Beyond that the check is still useful and the cost stops
// growing, which matters because this is the one meant to run hourly.
const sizeCheckLimit = 400

// deepCheck downloads segments and proves they can be read.
//
// Split between the newest and the oldest. The newest because they are what a
// recovery to now needs first; the oldest because nothing has read them since
// they were written, which is where silent corruption in a store accumulates
// without anybody noticing.
func deepCheck(
	ctx context.Context, opts WALOptions,
	whole []string, sample int, report *WALVerifyReport,
) error {
	if sample > len(whole) {
		sample = len(whole)
	}
	half := sample / 2
	if half == 0 {
		half = 1
	}
	picked := map[string]bool{}
	chosen := []string{}
	add := func(name string) {
		if !picked[name] {
			picked[name] = true
			chosen = append(chosen, name)
		}
	}
	for i := 0; i < half && i < len(whole); i++ {
		add(whole[i])
	}
	for i := 0; i < sample-half && i < len(whole); i++ {
		add(whole[len(whole)-1-i])
	}

	dir, err := os.MkdirTemp(stagingRoot(opts.TempDir), "rawsyst-walcheck-*")
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"A directory to check segments in could not be created.")
	}
	defer os.RemoveAll(dir)

	for _, name := range chosen {
		dest := filepath.Join(dir, name)
		meta, err := FetchFile(ctx, opts, name, dest)
		if err != nil {
			report.finding("%s could not be read back out of the archive: %s",
				name, err.Error())
			continue
		}
		report.Sampled++
		report.SampledBytes += meta.StoredBytes
		_ = os.Remove(dest)
	}
	if report.Sampled > 0 {
		report.Checked = append(report.Checked, fmt.Sprintf(
			"%d segment(s) were downloaded, decrypted and checked against "+
				"their recorded checksums", report.Sampled))
	}
	return nil
}
