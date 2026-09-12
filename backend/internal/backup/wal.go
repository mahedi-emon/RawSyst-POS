// The write-ahead log, named, addressed and reasoned about.
//
// # What this file is for
//
// A `pg_dump` is a photograph of a moment. Everything between two photographs
// is gone, and on a daily timer that is the trading day. The write-ahead log is
// the other half: PostgreSQL writes every change to it before it writes the
// change, so a base backup plus the segments that follow it reconstructs any
// moment in between. That is point-in-time recovery, and this file holds the
// vocabulary the rest of it is written in.
//
// Nothing here talks to a database or to a bucket. It is names, arithmetic and
// validation — the parts that have to be right before anything is uploaded, and
// the parts a test can hold still.
//
// # The names
//
// A segment file name is twenty-four hexadecimal characters and nothing else:
//
//	00000001 00000000 00000003
//	timeline    log id   segment within that log id
//
// PostgreSQL generates them and PostgreSQL consumes them, so this product's
// only job is to refuse anything that is not one. It refuses hard, because a
// segment name becomes a path inside a bucket: `../../` in one is the
// difference between an archive and an arbitrary-write primitive pointed at
// somebody else's objects.
//
// A timeline history file is eight hexadecimal characters and `.history`. It is
// written when a recovery promotes, it is tiny, and it is the thing that makes
// a second recovery on the same archive possible — so it is archived with the
// same care as a segment and never expired while the timeline it describes is
// still reachable.
//
// # Why the segment number is arithmetic and not a string
//
// Gap detection is the question "is every segment between A and B present", and
// the answer requires knowing what comes after `0000000100000000000000FF`. It
// is `000000010000000100000000` — the low field wraps at the number of segments
// in a log id, which is a function of the segment size chosen at initdb. Doing
// that with string comparison produces an archive that looks contiguous across
// every wrap and is not.
package backup

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// DefaultWALSegmentSize is what initdb uses unless told otherwise, and what
// every deployment of this product uses.
//
// It is not assumed silently: `WALLayout` carries the size, the archiver reads
// `wal_segment_size` from the server it is archiving for, and a gap report says
// which size it was computed with. A cluster initialised with a different one
// and read back with this one would report gaps that are not there, which is
// the kind of false alarm that teaches an operator to ignore the real one.
const DefaultWALSegmentSize = 16 << 20

// WALSegmentNameLen is fixed by the on-disk format.
const WALSegmentNameLen = 24

// WALMetaVersion is the shape of sidecar this build writes.
const WALMetaVersion = 1

// WALLayout is the segment geometry of one cluster.
type WALLayout struct {
	// SegmentSize is `wal_segment_size`, in bytes.
	SegmentSize int64
}

// DefaultWALLayout is the geometry of a cluster initialised with the defaults.
func DefaultWALLayout() WALLayout {
	return WALLayout{SegmentSize: DefaultWALSegmentSize}
}

// SegmentsPerLogID is how many segments share one log id before it advances.
//
// 2^32 divided by the segment size — 256 at the default 16 MiB. This is the
// number the wrap in `Next` turns on, and getting it wrong is how an archive
// reports a gap at every wrap or, worse, misses a real one.
func (l WALLayout) SegmentsPerLogID() uint64 {
	size := l.SegmentSize
	if size <= 0 {
		size = DefaultWALSegmentSize
	}
	return uint64(1<<32) / uint64(size)
}

// WALSegment is one parsed segment name.
type WALSegment struct {
	Timeline uint32
	LogID    uint32
	Segment  uint32
}

// ParseWALSegment reads a segment file name.
//
// Strict on purpose. The second return is false for anything that is not
// exactly twenty-four upper-case hexadecimal characters, and every caller that
// turns a name into an object key comes through here first. A permissive parser
// here would be a path-traversal bug three files away.
func ParseWALSegment(name string) (WALSegment, bool) {
	if len(name) != WALSegmentNameLen {
		return WALSegment{}, false
	}
	for _, r := range name {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F':
		default:
			return WALSegment{}, false
		}
	}
	tl, err := strconv.ParseUint(name[0:8], 16, 32)
	if err != nil {
		return WALSegment{}, false
	}
	logID, err := strconv.ParseUint(name[8:16], 16, 32)
	if err != nil {
		return WALSegment{}, false
	}
	seg, err := strconv.ParseUint(name[16:24], 16, 32)
	if err != nil {
		return WALSegment{}, false
	}
	return WALSegment{
		Timeline: uint32(tl), LogID: uint32(logID), Segment: uint32(seg),
	}, true
}

// ValidWALSegmentName reports whether a string is a segment file name.
func ValidWALSegmentName(name string) bool {
	_, ok := ParseWALSegment(name)
	return ok
}

// String renders a segment back to its file name.
func (s WALSegment) String() string {
	return fmt.Sprintf("%08X%08X%08X", s.Timeline, s.LogID, s.Segment)
}

// Next is the segment PostgreSQL fills after this one, on the same timeline.
//
// The low field wraps at `SegmentsPerLogID` rather than at 2^32, which is the
// whole reason this is a method on a layout and not a string increment.
func (s WALSegment) Next(l WALLayout) WALSegment {
	per := l.SegmentsPerLogID()
	seg := uint64(s.Segment) + 1
	if seg >= per {
		return WALSegment{Timeline: s.Timeline, LogID: s.LogID + 1, Segment: 0}
	}
	return WALSegment{Timeline: s.Timeline, LogID: s.LogID, Segment: uint32(seg)}
}

// Ordinal is the segment's position in its timeline, counting from zero.
//
// Used to count how many segments lie between two names without walking them,
// which is what turns "there is a gap" into "there are 412 segments missing".
func (s WALSegment) Ordinal(l WALLayout) uint64 {
	return uint64(s.LogID)*l.SegmentsPerLogID() + uint64(s.Segment)
}

// Before reports whether this segment comes before another on the same
// timeline. Segments on different timelines are not ordered by this, and it
// says so by answering false in both directions.
func (s WALSegment) Before(other WALSegment, l WALLayout) bool {
	if s.Timeline != other.Timeline {
		return false
	}
	return s.Ordinal(l) < other.Ordinal(l)
}

// SegmentForLSN is the segment that holds a log-sequence number.
//
// `pg_basebackup` reports its start and end as LSNs, retention has to know
// which segments those require, and a person reading a recovery window is shown
// segments rather than LSNs. This is the conversion between the two.
func SegmentForLSN(timeline uint32, lsn uint64, l WALLayout) WALSegment {
	size := uint64(l.SegmentSize)
	if size == 0 {
		size = DefaultWALSegmentSize
	}
	total := lsn / size
	per := l.SegmentsPerLogID()
	return WALSegment{
		Timeline: timeline,
		LogID:    uint32(total / per),
		Segment:  uint32(total % per),
	}
}

// ParseLSN reads the `XXXXXXXX/XXXXXXXX` form.
//
// Returned by `pg_current_wal_lsn()`, printed in a backup manifest, and typed
// by an operator recovering to an exact position. The second return is false
// for anything that is not one: a recovery target that silently became zero
// would recover to the beginning of time and report success.
func ParseLSN(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	hi, lo, ok := strings.Cut(s, "/")
	if !ok || hi == "" || lo == "" || len(hi) > 8 || len(lo) > 8 {
		return 0, false
	}
	h, err := strconv.ParseUint(hi, 16, 32)
	if err != nil {
		return 0, false
	}
	l, err := strconv.ParseUint(lo, 16, 32)
	if err != nil {
		return 0, false
	}
	return h<<32 | l, true
}

// FormatLSN renders a log-sequence number the way PostgreSQL prints one.
func FormatLSN(lsn uint64) string {
	return fmt.Sprintf("%X/%08X", lsn>>32, lsn&0xFFFFFFFF)
}

// --- timeline history -------------------------------------------------------

// ValidTimelineHistoryName reports whether a string is a history file name.
//
// Eight upper-case hexadecimal characters and `.history`. Timeline 1 never has
// one — it is where a cluster starts — so `00000001.history` is legitimate to
// ask for and legitimate not to find.
func ValidTimelineHistoryName(name string) bool {
	head, ok := strings.CutSuffix(name, ".history")
	if !ok || len(head) != 8 {
		return false
	}
	for _, r := range head {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// TimelineOfHistory reads the timeline a history file describes.
func TimelineOfHistory(name string) (uint32, bool) {
	if !ValidTimelineHistoryName(name) {
		return 0, false
	}
	tl, err := strconv.ParseUint(strings.TrimSuffix(name, ".history"), 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(tl), true
}

// TimelineHistoryName is the file name for a timeline.
func TimelineHistoryName(timeline uint32) string {
	return fmt.Sprintf("%08X.history", timeline)
}

// --- what the archiver may be asked for -------------------------------------

// The three kinds of file that travel through the archive.
const (
	WALKindSegment     = "segment"
	WALKindHistory     = "history"
	WALKindBackupLabel = "backup_label"
)

// ArchivableName reports whether PostgreSQL may legitimately ask for a file
// with this name to be archived, and says what kind it is.
//
// `archive_command` is called with `%f`, and PostgreSQL puts three kinds of
// thing through it: segments, timeline history files, and the `.backup` label
// files a base backup leaves behind. Anything else arriving here is either a
// PostgreSQL this build does not understand or somebody probing, and both are
// refused rather than guessed at.
func ArchivableName(name string) (kind string, ok bool) {
	switch {
	case ValidWALSegmentName(name):
		return WALKindSegment, true
	case ValidTimelineHistoryName(name):
		return WALKindHistory, true
	case validBackupLabelName(name):
		return WALKindBackupLabel, true
	default:
		return "", false
	}
}

// validBackupLabelName reports whether a name is a base backup's label file.
//
// `000000010000000000000002.00000028.backup` — a segment name, a dot, the
// offset within it in hexadecimal, and `.backup`. Archived because
// `pg_basebackup` leaves one and PostgreSQL expects the archive to take it;
// never required for a recovery, which is why a missing one is not a gap.
func validBackupLabelName(name string) bool {
	head, ok := strings.CutSuffix(name, ".backup")
	if !ok {
		return false
	}
	seg, off, ok := strings.Cut(head, ".")
	if !ok || !ValidWALSegmentName(seg) || len(off) == 0 || len(off) > 16 {
		return false
	}
	for _, r := range off {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// --- where things live in the bucket ----------------------------------------

// The archive is a sibling of the snapshots, never mixed in with them.
//
// A `pg_dump` snapshot and a WAL segment have different lifetimes, different
// retention rules and different consequences for being deleted early, and the
// one routine that deletes things works by listing a prefix. Sharing a prefix
// would make a retention bug in one of them a data-loss bug in the other.
const (
	walPrefixPart        = "wal"
	walHistoryPart       = "history"
	basebackupPrefixPart = "basebackup"

	// The sidecar that makes an archive checkable without downloading it.
	walMetaSuffix = ".meta"
)

// WALPrefix is everything the archive owns, for a listing or a bulk delete.
func WALPrefix(prefix string) string {
	return path.Join(cleanPrefix(prefix), walPrefixPart) + "/"
}

// WALTimelinePrefix is one timeline's segments.
//
// Sharded by timeline so a listing is bounded by what a single timeline has
// produced rather than by everything the installation has ever written. It is
// not sharded further: at a shop's volume a seven-day window is a few hundred
// segments, and a scheme that needs explaining before it can be listed is a
// scheme somebody gets wrong at three in the morning.
func WALTimelinePrefix(prefix string, timeline uint32) string {
	return path.Join(cleanPrefix(prefix), walPrefixPart,
		fmt.Sprintf("%08X", timeline)) + "/"
}

// WALSegmentKey is where one segment lives.
//
// Returns an error rather than a bad key for a name that is not a segment.
// Every path this product builds out of something PostgreSQL handed it is built
// here, so there is exactly one place traversal has to be refused.
func WALSegmentKey(prefix, name string) (string, error) {
	seg, ok := ParseWALSegment(name)
	if !ok {
		return "", errInvalidWALName(name)
	}
	return path.Join(cleanPrefix(prefix), walPrefixPart,
		fmt.Sprintf("%08X", seg.Timeline), name), nil
}

// WALHistoryKey is where a timeline history file lives.
//
// Its own directory rather than the timeline's: a history file describes a
// timeline that may have no segments of its own yet, and putting it under that
// timeline would make an empty timeline look like one with a stray object in
// it.
func WALHistoryKey(prefix, name string) (string, error) {
	if !ValidTimelineHistoryName(name) {
		return "", errInvalidWALName(name)
	}
	return path.Join(cleanPrefix(prefix), walPrefixPart, walHistoryPart, name), nil
}

// WALHistoryPrefix is every history file.
func WALHistoryPrefix(prefix string) string {
	return path.Join(cleanPrefix(prefix), walPrefixPart, walHistoryPart) + "/"
}

// WALBackupLabelKey is where a base backup's label file lives.
func WALBackupLabelKey(prefix, name string) (string, error) {
	if !validBackupLabelName(name) {
		return "", errInvalidWALName(name)
	}
	seg, _ := ParseWALSegment(strings.SplitN(name, ".", 2)[0])
	return path.Join(cleanPrefix(prefix), walPrefixPart,
		fmt.Sprintf("%08X", seg.Timeline), name), nil
}

// WALKeyFor is the object key for anything the archiver accepts.
func WALKeyFor(prefix, name string) (string, error) {
	kind, ok := ArchivableName(name)
	if !ok {
		return "", errInvalidWALName(name)
	}
	switch kind {
	case WALKindSegment:
		return WALSegmentKey(prefix, name)
	case WALKindHistory:
		return WALHistoryKey(prefix, name)
	default:
		return WALBackupLabelKey(prefix, name)
	}
}

// WALMetaKey is the sidecar that says what is in the object beside it.
func WALMetaKey(objectKey string) string { return objectKey + walMetaSuffix }

// BaseBackupPrefix is everything the physical base backups own.
func BaseBackupPrefix(prefix string) string {
	return path.Join(cleanPrefix(prefix), basebackupPrefixPart) + "/"
}

// BaseBackupKey is one file inside one base backup.
func BaseBackupKey(prefix, id, name string) (string, error) {
	if !ValidSnapshotID(id) {
		return "", errs.Newf(errs.CodeInvalidInput,
			"%q is not a base backup id.", clipText(id, 64))
	}
	switch name {
	case baseTarObject, baseWALTarObject, baseManifestObject,
		basePGManifestObject, completedMark:
	default:
		return "", errs.Newf(errs.CodeInvalidInput,
			"A base backup has no file called %q.", clipText(name, 64))
	}
	return path.Join(cleanPrefix(prefix), basebackupPrefixPart, id, name), nil
}

// cleanPrefix keeps an operator's configured prefix from being a path.
//
// `RAWSYST_BACKUP_PREFIX` is read from the environment of a server, which is
// not a hostile source — but it is a source, and every other path in this file
// is validated. A prefix of `../` would put the archive outside the namespace
// every retention routine lists, which is a way to lose an archive quietly.
func cleanPrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "rawsyst"
	}
	parts := strings.Split(prefix, "/")
	kept := parts[:0]
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		return "rawsyst"
	}
	return strings.Join(kept, "/")
}

func errInvalidWALName(name string) error {
	return errs.Newf(errs.CodeInvalidInput,
		"%q is not the name of a write-ahead log file. PostgreSQL archives "+
			"segments, timeline history files and base backup labels, and "+
			"this archive stores nothing else.", clipText(name, 64))
}

// --- the sidecar ------------------------------------------------------------

// WALMeta describes one archived file without anybody downloading it.
//
// # Why it exists
//
// Verifying an archive otherwise means pulling every segment back out of the
// bucket, which is egress charged by the gigabyte for a check that should be
// cheap enough to run hourly. With this, "is the archive intact" is a listing
// and a set of small reads.
//
// # What is deliberately not in it
//
// The key. The key FINGERPRINT is here for the same reason it is in a
// snapshot's manifest — "you have the wrong key" beats "decryption failed" —
// and it is a hash from which nothing can be recovered.
type WALMeta struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`

	// PlainBytes and PlainSHA256 describe the file as PostgreSQL wrote it.
	// This is what a restore checks after it has undone everything below.
	PlainBytes  int64  `json:"plaintext_bytes"`
	PlainSHA256 string `json:"plaintext_sha256"`

	// StoredBytes and StoredSHA256 describe the object in the bucket. This is
	// what a verification checks WITHOUT the key, which is the check an
	// unattended health probe can make.
	StoredBytes  int64  `json:"stored_bytes"`
	StoredSHA256 string `json:"stored_sha256"`

	Compression string `json:"compression,omitempty"`
	Encrypted   bool   `json:"encrypted"`
	KeyPrint    string `json:"key_fingerprint,omitempty"`

	Timeline uint32 `json:"timeline,omitempty"`

	// ArchivedAt is when this server finished putting it there. It is the clock
	// the recovery window's end is read off, and it is deliberately the
	// archiver's clock rather than the store's: the store's is not visible
	// without a second request per object.
	ArchivedAt string `json:"archived_at"`

	// SegmentSize is the geometry this was written under, so a gap report
	// computed years later is computed with the right arithmetic.
	SegmentSize int64 `json:"segment_size_bytes,omitempty"`

	SourceHost string `json:"source_host,omitempty"`
	AppVersion string `json:"app_version,omitempty"`
}

// ArchivedTime reads the sidecar's timestamp.
func (m WALMeta) ArchivedTime() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, m.ArchivedAt)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}
