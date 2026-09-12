// Shipping one write-ahead log segment off the machine, and getting it back.
//
// # Where this runs
//
// In the PostgreSQL container, as `archive_command`, as the `postgres` user,
// once per segment. PostgreSQL hands it a path and a name and reads one thing
// from it: the exit status.
//
//	archive_command  = '/biz1core backup wal archive %p %f'
//	restore_command  = '/biz1core backup wal restore %f %p'
//
// That is the entire contract, and the whole of point-in-time recovery rests on
// one half of it: **exit zero means the segment is safe somewhere else**. The
// moment this returns zero, PostgreSQL is entitled to recycle the segment, and
// whatever was in it is gone from the machine. So nothing here reports success
// until the object and its sidecar are both in the bucket and the store has
// acknowledged them.
//
// The corollary is the behaviour under failure, and it is deliberate: when the
// store cannot be reached, this exits non-zero, PostgreSQL KEEPS the segment
// and retries. WAL then accumulates in `pg_wal` — which is a disk filling up,
// visible, alarmable and recoverable, and is strictly better than the
// alternative, which is a green archive with a hole in it that nobody finds
// until a recovery. `walhealth.go` is what makes the filling disk visible
// before it matters.
//
// # What leaves the building
//
//	segment -> gzip -> AES-256-GCM (if a key is configured) -> PUT
//
// Compressed first, because ciphertext does not compress and doing it the other
// way round would triple the bill for nothing. A mostly-idle segment is 16 MiB
// of zeroes and lands in the bucket as a few kilobytes, which is what makes
// `archive_timeout` affordable.
//
// # Why a sidecar object
//
// Beside every archived file is a small JSON `.meta` holding the plaintext
// length and checksum, the stored length and checksum, and whether it is
// sealed. Three things need it:
//
//   - Verification without egress. Checking that an archive is intact otherwise
//     means downloading every segment, which is charged by the gigabyte for a
//     check that should be cheap enough to run hourly.
//   - Idempotence. PostgreSQL may call `archive_command` again for a segment it
//     already archived, and the documented requirement is to refuse to replace
//     an existing file with DIFFERENT content. Comparing checksums answers that
//     in one small read.
//   - Completeness. An object with no sidecar is an upload that did not finish.
//     Writing the sidecar last is what makes "present" mean "whole", exactly as
//     the COMPLETED marker does for a snapshot.
package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/blob"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// WALOptions is what archiving and fetching need to know.
//
// Deliberately small and deliberately separate from `Options`: this is
// constructed in a container that has no application database, no JWT secret
// and no business knowing about either. See `WALOptionsFromEnv`.
type WALOptions struct {
	// Store is the bucket. Required — there is no local-only mode, because a
	// WAL archive on the disk being protected protects nothing.
	Store *blob.Store

	// Prefix namespaces this installation inside the bucket. The archive lives
	// under `<prefix>/wal/`, never mixed in with the dump snapshots.
	Prefix string

	// Key seals each file before it leaves. Zero means no key is configured.
	Key Key

	// Layout is the segment geometry, for gap arithmetic and for the sidecar.
	Layout WALLayout

	// Timeout bounds one archive or fetch. PostgreSQL has no timeout of its
	// own on `archive_command`: without one here, a hung TCP connection to the
	// store is an archiver process that never exits and a WAL directory that
	// grows for ever.
	Timeout time.Duration

	// Retries is how many times a transport failure is tried again before this
	// gives up and lets PostgreSQL keep the segment. Small on purpose: giving
	// up quickly and letting PostgreSQL retry is the same loop, with the
	// advantage that PostgreSQL is the one counting.
	Retries int

	SourceHost string
	AppVersion string

	// TempDir is where a fetched file is assembled before it is renamed into
	// place. Defaults to the directory the file is going to, so the rename is
	// on one filesystem and therefore atomic.
	TempDir string
}

func (o WALOptions) withDefaults() WALOptions {
	if o.Prefix == "" {
		o.Prefix = "rawsyst"
	}
	if o.Layout.SegmentSize <= 0 {
		o.Layout = DefaultWALLayout()
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.Retries <= 0 {
		o.Retries = 3
	}
	if o.AppVersion == "" {
		o.AppVersion = "dev"
	}
	return o
}

// Configured reports whether there is anywhere to archive to.
func (o WALOptions) Configured() bool { return o.Store.Configured() }

// ArchiveResult is what one call to `archive_command` did.
type ArchiveResult struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Key  string `json:"object_key"`

	PlainBytes  int64 `json:"plaintext_bytes"`
	StoredBytes int64 `json:"stored_bytes"`

	Encrypted bool `json:"encrypted"`

	// AlreadyThere is true when the segment was already archived with
	// byte-identical content, which is a success and not a no-op worth hiding:
	// it is the normal outcome after a crash, and an operator reading the log
	// should be able to tell the two apart.
	AlreadyThere bool `json:"already_archived"`

	Took     string `json:"took"`
	Attempts int    `json:"attempts"`
}

// ArchiveFile puts one write-ahead log file into the archive.
//
// `src` is `%p` — a path relative to the data directory or an absolute one, as
// PostgreSQL gives it. `name` is `%f`, and it is the only part that becomes a
// path in the bucket, so it is validated before anything else happens.
func ArchiveFile(
	ctx context.Context, opts WALOptions, src, name string,
) (ArchiveResult, error) {
	opts = opts.withDefaults()
	started := time.Now()

	kind, ok := ArchivableName(name)
	if !ok {
		return ArchiveResult{}, errInvalidWALName(name)
	}
	if !opts.Configured() {
		return ArchiveResult{}, errs.New(errs.CodeUnavailable,
			"No object store is configured, so there is nowhere to archive "+
				"the write-ahead log to. Until one is, archive_mode must be "+
				"off: an archive_command that cannot archive stops PostgreSQL "+
				"recycling WAL and fills the disk. See deploy/server/PITR.md.")
	}

	key, err := WALKeyFor(opts.Prefix, name)
	if err != nil {
		return ArchiveResult{}, err
	}

	plain, err := os.ReadFile(src)
	if err != nil {
		return ArchiveResult{}, errs.Wrap(err, errs.CodeInternal, fmt.Sprintf(
			"The write-ahead log file %q could not be read to archive it.",
			clipText(name, 64)))
	}
	plainSum := sha256.Sum256(plain)
	plainHex := hex.EncodeToString(plainSum[:])

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// Already there? Two answers matter and they are not the same one.
	//
	// Identical content is a success: PostgreSQL re-archives after a crash and
	// the documented behaviour is to accept that. DIFFERENT content under the
	// same name is refused, loudly, because the only ways to produce it are two
	// clusters sharing one prefix or an archive that has been tampered with,
	// and quietly overwriting either would destroy a recovery point.
	if existing, found, err := readWALMeta(ctx, opts, key); err == nil && found {
		if existing.PlainSHA256 == plainHex {
			return ArchiveResult{
				Name: name, Kind: kind, Key: key,
				PlainBytes: int64(len(plain)), StoredBytes: existing.StoredBytes,
				Encrypted: existing.Encrypted, AlreadyThere: true,
				Took: time.Since(started).Round(time.Millisecond).String(),
			}, nil
		}
		return ArchiveResult{}, errs.Newf(errs.CodeConflict,
			"%q is already in the archive with different contents. Refusing "+
				"to replace it: PostgreSQL never reuses a segment name on one "+
				"timeline, so two different files under one name mean two "+
				"clusters are archiving to the same prefix, or the archive has "+
				"been written to by something else. Give each cluster its own "+
				"RAWSYST_BACKUP_PREFIX. Nothing has been changed.",
			clipText(name, 64))
	}

	body, err := encodeWALPayload(opts, plain)
	if err != nil {
		return ArchiveResult{}, err
	}
	storedSum := sha256.Sum256(body)
	storedHex := hex.EncodeToString(storedSum[:])

	meta := WALMeta{
		Version:      WALMetaVersion,
		Name:         name,
		Kind:         kind,
		PlainBytes:   int64(len(plain)),
		PlainSHA256:  plainHex,
		StoredBytes:  int64(len(body)),
		StoredSHA256: storedHex,
		Compression:  "gzip",
		Encrypted:    opts.Key.Set(),
		ArchivedAt:   time.Now().UTC().Format(time.RFC3339),
		SegmentSize:  opts.Layout.SegmentSize,
		SourceHost:   opts.SourceHost,
		AppVersion:   opts.AppVersion,
	}
	if opts.Key.Set() {
		meta.KeyPrint = opts.Key.Fingerprint()
	}
	if seg, ok := ParseWALSegment(name); ok {
		meta.Timeline = seg.Timeline
	} else if tl, ok := TimelineOfHistory(name); ok {
		meta.Timeline = tl
	}
	sidecar, err := json.Marshal(meta)
	if err != nil {
		return ArchiveResult{}, errs.Wrap(err, errs.CodeInternal,
			"The archive record for that segment could not be written.")
	}

	// The object, then the sidecar. In that order and never the other way
	// round: the sidecar is what makes the segment count, exactly as the
	// COMPLETED marker does for a snapshot.
	attempts, err := withWALRetries(ctx, opts, func(ctx context.Context) error {
		return opts.Store.Put(ctx, key, "application/octet-stream", body)
	})
	if err != nil {
		return ArchiveResult{}, err
	}
	if _, err := withWALRetries(ctx, opts, func(ctx context.Context) error {
		return opts.Store.Put(ctx, WALMetaKey(key), "application/json", sidecar)
	}); err != nil {
		return ArchiveResult{}, err
	}

	return ArchiveResult{
		Name: name, Kind: kind, Key: key,
		PlainBytes: meta.PlainBytes, StoredBytes: meta.StoredBytes,
		Encrypted: meta.Encrypted, Attempts: attempts,
		Took: time.Since(started).Round(time.Millisecond).String(),
	}, nil
}

// FetchFile is `restore_command`: put one archived file back on disk.
//
// # Why "not found" is a normal answer
//
// Recovery ends when `restore_command` cannot supply the next segment.
// PostgreSQL asks for one segment past the end of the archive every single
// time, and a non-zero exit is how it is told it has reached it. So a missing
// object is reported as `not_found` and the caller turns that into a quiet
// exit code — not into a stack trace and not into a retry loop, which would
// turn the ordinary end of every recovery into a two-minute hang.
//
// Everything else — a store that cannot be reached, a checksum that does not
// match, the wrong key — is loud, because each of those is a recovery that
// would otherwise stop early and call itself finished.
func FetchFile(
	ctx context.Context, opts WALOptions, name, dest string,
) (WALMeta, error) {
	opts = opts.withDefaults()
	if _, ok := ArchivableName(name); !ok {
		return WALMeta{}, errInvalidWALName(name)
	}
	if !opts.Configured() {
		return WALMeta{}, errs.New(errs.CodeUnavailable,
			"No object store is configured, so there is no write-ahead log "+
				"archive to recover from.")
	}
	key, err := WALKeyFor(opts.Prefix, name)
	if err != nil {
		return WALMeta{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	meta, found, err := readWALMeta(ctx, opts, key)
	if err != nil {
		return WALMeta{}, err
	}
	if !found {
		return WALMeta{}, errs.Newf(errs.CodeNotFound,
			"%q is not in the archive.", clipText(name, 64))
	}

	body, err := opts.Store.Get(ctx, key)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			// A sidecar with no object beside it. Never produced by this
			// code — the object is written first — so it means something
			// deleted the object and left the record of it, which is the
			// single most misleading state an archive can be in and is worth
			// saying plainly rather than reporting as an ordinary miss.
			return WALMeta{}, errs.Newf(errs.CodeInternal,
				"The archive has a record of %q but not the segment itself. "+
					"Something removed the object and left its sidecar. The "+
					"recovery window no longer reaches past this point; see "+
					"deploy/server/PITR.md, Missing WAL.", clipText(name, 64))
		}
		return WALMeta{}, err
	}

	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != meta.StoredSHA256 {
		return WALMeta{}, errs.Newf(errs.CodeInternal,
			"%q came back from the archive with the wrong checksum. The "+
				"object has been corrupted or truncated in the store; it "+
				"cannot be used and recovery must not continue past it.",
			clipText(name, 64))
	}

	plain, err := decodeWALPayload(opts, body, meta)
	if err != nil {
		return WALMeta{}, err
	}
	if sum := sha256.Sum256(plain); hex.EncodeToString(sum[:]) != meta.PlainSHA256 {
		return WALMeta{}, errs.Newf(errs.CodeInternal,
			"%q did not survive being unpacked: the segment does not match "+
				"the checksum recorded when it was archived.", clipText(name, 64))
	}

	if err := writeFileAtomically(opts, dest, plain); err != nil {
		return WALMeta{}, err
	}
	return meta, nil
}

// HasWALFile reports whether something is in the archive, whole.
//
// Reads the sidecar rather than the object, because an object without one is an
// upload that did not finish and answering true for it would put a hole in a
// gap report.
func HasWALFile(ctx context.Context, opts WALOptions, name string) (bool, error) {
	opts = opts.withDefaults()
	key, err := WALKeyFor(opts.Prefix, name)
	if err != nil {
		return false, err
	}
	_, found, err := readWALMeta(ctx, opts, key)
	return found, err
}

// ReadWALMeta reads one archived file's sidecar.
func ReadWALMeta(
	ctx context.Context, opts WALOptions, name string,
) (WALMeta, bool, error) {
	opts = opts.withDefaults()
	key, err := WALKeyFor(opts.Prefix, name)
	if err != nil {
		return WALMeta{}, false, err
	}
	return readWALMeta(ctx, opts, key)
}

func readWALMeta(
	ctx context.Context, opts WALOptions, key string,
) (WALMeta, bool, error) {
	body, err := opts.Store.Get(ctx, WALMetaKey(key))
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return WALMeta{}, false, nil
		}
		return WALMeta{}, false, err
	}
	var meta WALMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return WALMeta{}, false, errs.Wrap(err, errs.CodeInternal,
			"An archive record could not be read. The object store holds "+
				"something under that name that this build does not "+
				"understand.")
	}
	return meta, true, nil
}

// --- the encoding -----------------------------------------------------------

func encodeWALPayload(opts WALOptions, plain []byte) ([]byte, error) {
	var zipped bytes.Buffer
	zw, err := gzip.NewWriterLevel(&zipped, gzip.DefaultCompression)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal,
			"The segment could not be compressed.")
	}
	if _, err := zw.Write(plain); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal,
			"The segment could not be compressed.")
	}
	if err := zw.Close(); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal,
			"The segment could not be compressed.")
	}
	if !opts.Key.Set() {
		return zipped.Bytes(), nil
	}
	var sealed bytes.Buffer
	if _, err := opts.Key.Seal(&sealed, bytes.NewReader(zipped.Bytes())); err != nil {
		return nil, err
	}
	return sealed.Bytes(), nil
}

func decodeWALPayload(
	opts WALOptions, body []byte, meta WALMeta,
) ([]byte, error) {
	if IsSealed(body) {
		if !opts.Key.Set() {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"%q is encrypted and no backup encryption key is configured. "+
					"Set RAWSYST_BACKUP_ENCRYPTION_KEY to the key this "+
					"archive was written with (fingerprint %s). Without it "+
					"the archive cannot be read by anyone, including this "+
					"product.", clipText(meta.Name, 64), meta.KeyPrint)
		}
		if meta.KeyPrint != "" && meta.KeyPrint != opts.Key.Fingerprint() {
			return nil, errs.Newf(errs.CodeInvalidInput,
				"%q was sealed with key %s and the configured key is %s. "+
					"This is the wrong key, not a corrupt archive.",
				clipText(meta.Name, 64), meta.KeyPrint, opts.Key.Fingerprint())
		}
		var opened bytes.Buffer
		if _, err := opts.Key.Open(&opened, bytes.NewReader(body)); err != nil {
			return nil, err
		}
		body = opened.Bytes()
	} else if meta.Encrypted {
		return nil, errs.Newf(errs.CodeInternal,
			"The archive says %q is encrypted and the object is not. The "+
				"record and the object disagree; neither can be trusted.",
			clipText(meta.Name, 64))
	}

	if meta.Compression == "" || meta.Compression == "none" {
		return body, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal, fmt.Sprintf(
			"%q could not be decompressed.", clipText(meta.Name, 64)))
	}
	defer zr.Close()
	// Bounded: a decompression with no ceiling is how a crafted object turns a
	// 16 MiB segment into a container killed by the memory reaper. Four times
	// a segment is far more than any real one and far less than a bomb.
	limit := opts.Layout.SegmentSize * 4
	if limit <= 0 {
		limit = DefaultWALSegmentSize * 4
	}
	out, err := io.ReadAll(io.LimitReader(zr, limit+1))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal, fmt.Sprintf(
			"%q could not be decompressed.", clipText(meta.Name, 64)))
	}
	if int64(len(out)) > limit {
		return nil, errs.Newf(errs.CodeInternal,
			"%q unpacks to more than %d bytes, which no write-ahead log file "+
				"does. Refusing to read it.", clipText(meta.Name, 64), limit)
	}
	return out, nil
}

// writeFileAtomically puts the bytes where PostgreSQL asked for them.
//
// Written to a neighbouring temporary file and renamed, because PostgreSQL
// starts reading `%p` as soon as `restore_command` exits and a partially
// written segment is a recovery that stops in the middle of a record. The
// temporary file is in the SAME directory for the same reason: a rename across
// filesystems is a copy, and a copy is not atomic.
func writeFileAtomically(opts WALOptions, dest string, body []byte) error {
	dir := opts.TempDir
	if dir == "" {
		dir = filepath.Dir(dest)
	}
	tmp, err := os.CreateTemp(dir, ".biz1core-wal-*")
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"A temporary file for the restored segment could not be created.")
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return errs.Wrap(err, errs.CodeInternal,
			"The restored segment could not be written to disk.")
	}
	// Flushed before the rename. Without this the rename is durable and the
	// contents are not, which after a power cut during a recovery leaves a
	// zero-length segment that PostgreSQL reads as the end of the archive.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return errs.Wrap(err, errs.CodeInternal,
			"The restored segment could not be flushed to disk.")
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The restored segment could not be closed.")
	}
	if err := os.Rename(name, dest); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The restored segment could not be put in place.")
	}
	return nil
}

// withWALRetries tries a store operation a few times before giving up.
//
// Only worth doing for transport failures. A refusal — the wrong credentials, a
// bucket that does not exist, a path the policy forbids — is not going to
// become true on the third attempt, and retrying it only delays the moment
// PostgreSQL is told to keep the segment.
func withWALRetries(
	ctx context.Context, opts WALOptions, fn func(context.Context) error,
) (int, error) {
	var last error
	for attempt := 1; attempt <= opts.Retries; attempt++ {
		if err := fn(ctx); err == nil {
			return attempt, nil
		} else {
			last = err
			if errs.CodeOf(err) != errs.CodeUnavailable {
				return attempt, err
			}
		}
		select {
		case <-ctx.Done():
			return attempt, errors.Join(last, ctx.Err())
		case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
		}
	}
	return opts.Retries, last
}

// --- configuration, read from an environment that has almost nothing in it ---

// WALOptionsFromEnv builds the archiver from the environment.
//
// # Why this does not use config.Load
//
// `archive_command` runs inside the PostgreSQL container. That container has no
// application database connection, no JWT secret and no data encryption keys,
// and it should not: it is the database, not the product. `config.Load`
// validates all of those and would refuse to start, which would mean every
// segment failing to archive with a message about a missing signing key.
//
// So this reads the six variables the archiver actually needs and nothing else,
// and every one of them is named in `.env.example` beside the compose file that
// passes them through.
func WALOptionsFromEnv() (WALOptions, error) {
	cfg := storageFromEnv()
	key, _, err := KeyFromEnv(os.Getenv("RAWSYST_BACKUP_ENCRYPTION_KEY"))
	if err != nil {
		return WALOptions{}, err
	}
	opts := WALOptions{
		Store:      blob.Open(cfg),
		Prefix:     envOr("RAWSYST_BACKUP_PREFIX", "rawsyst"),
		Key:        key,
		Layout:     WALLayout{SegmentSize: int64(envBytes("RAWSYST_WAL_SEGMENT_SIZE", DefaultWALSegmentSize))},
		Timeout:    envDuration("RAWSYST_WAL_ARCHIVE_TIMEOUT", 2*time.Minute),
		Retries:    envInt("RAWSYST_WAL_ARCHIVE_RETRIES", 3),
		SourceHost: envOr("RAWSYST_WAL_SOURCE_HOST", shortHostname()),
		AppVersion: buildVersion(),
	}
	return opts.withDefaults(), nil
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

func envBytes(name string, fallback int64) int64 {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := parseByteSize(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func shortHostname() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return host
}
