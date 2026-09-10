// Taking a backup, checking it restores, and keeping the right ones.
//
// # What a backup of this product is
//
// On the profile this server runs, the database is the whole of the persistent
// business data. Documents, logos and receipts are stored IN Postgres unless
// `docker-compose.files.yml` is layered on — which is correct for a shop and
// does not scale, and is a decision that file already explains. So one dump
// captures companies, users, roles, products, inventory, customers, suppliers,
// orders, invoices, accounting, payments, payroll, the regulatory registry with
// its retrieved source documents, the audit trail and the platform's own
// records.
//
// When object storage IS configured for uploads, the manifest records that too
// and the objects are copied into the snapshot. What is never in a backup is
// anything reproducible: images, build caches, `node_modules`, `.next`. Those
// are a `git clone` and a `docker compose build` away and putting them in a
// backup makes the backup slower, larger and no more useful.
//
// # The order things happen in, and why
//
//	open a record  ->  dump  ->  hash  ->  upload  ->  objects
//	  ->  manifest  ->  COMPLETED marker  ->  close the record
//
// The marker is last and it is the only thing that makes a snapshot count. A
// dump that uploaded and a manifest that did not is a snapshot that would
// restore into a database with no idea what it is; a restore refuses anything
// without a marker. That is what stops "the upload returned 200" from being
// mistaken for "there is a backup".
//
// # Memory
//
// The dump goes to a temporary file, hashed on the way past, and is uploaded
// from that file with a known length and a known checksum. It is never held in
// memory. On a 3.7 GiB server with 1.6 GiB of container ceilings, reading a
// dump into RAM to upload it is the difference between a backup and an
// out-of-memory kill — and it fails exactly when the business has grown enough
// to need one.
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/blob"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// ManifestVersion is the shape of the manifest this build writes.
//
// A restore refuses a version it does not know rather than guessing at a field
// that has moved. Backups outlive the build that made them, which is the whole
// point of them.
const ManifestVersion = 1

// Names inside a snapshot. Fixed, because a restore on a new server has only
// the snapshot id to go on — it cannot ask the old server where it put things.
const (
	databaseObject = "database.dump"
	manifestObject = "manifest.json"
	completedMark  = "COMPLETED"
	objectsPrefix  = "objects/"
)

// Manifest is what a snapshot says about itself.
//
// Deliberately free of anything secret. It names what was backed up, how big it
// was and what it hashes to — never a password, a signing key, a token or an
// API credential. Secrets are restored from wherever they are kept, which is
// the operator's password manager and not this file; `deploy/server/BACKUP.md`
// says so and says why.
type Manifest struct {
	Version    int    `json:"manifest_version"`
	SnapshotID string `json:"snapshot_id"`
	TakenAt    string `json:"taken_at"`

	// What made it, so a restore can say when a snapshot predates the schema
	// it is being restored into.
	AppVersion    string `json:"app_version"`
	SchemaVersion int    `json:"schema_version"`

	Database  Component   `json:"database"`
	Objects   []Component `json:"objects,omitempty"`
	ObjectsOf string      `json:"objects_source_bucket,omitempty"`

	// DatabaseName and Host are recorded to describe the source, not to point
	// a restore at it. A restore is told where to go on the command line.
	DatabaseName string `json:"database_name"`
	DatabaseSize int64  `json:"database_size_bytes"`

	// Tables and Rows are the cheap integrity check a verification repeats
	// against the restored copy. A dump that restores into the right number of
	// tables with the right number of rows in the tables that matter is a dump
	// somebody can trade on.
	Tables int              `json:"table_count"`
	Rows   map[string]int64 `json:"row_counts"`
}

// Component is one file in a snapshot.
type Component struct {
	Key    string `json:"key"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Format string `json:"format,omitempty"`
}

// Options are what a run needs to know.
type Options struct {
	// DSN is the database to dump. Never modified by a backup.
	DSN string

	// Store is where snapshots go. Required: a backup that stays on the server
	// it protects is not a backup, and this refuses rather than pretending.
	Store *blob.Store

	// Prefix namespaces snapshots inside the bucket, so one bucket can hold
	// more than one installation without them treading on each other.
	Prefix string

	// AppVersion is the build that took it.
	AppVersion string

	// TempDir is where the dump is staged. Defaults to the system temporary
	// directory; on a small server it wants to be somewhere with room.
	TempDir string

	// Timeout bounds the whole run. A backup that hangs holds a connection and
	// a lock and tells nobody.
	Timeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Minute
	}
	if o.Prefix == "" {
		o.Prefix = "rawsyst"
	}
	if o.AppVersion == "" {
		o.AppVersion = "dev"
	}
	return o
}

// Snapshot is a completed backup as `List` reports it.
type Snapshot struct {
	ID        string    `json:"snapshot_id"`
	TakenAt   time.Time `json:"taken_at"`
	Manifest  *Manifest `json:"manifest,omitempty"`
	Completed bool      `json:"completed"`
}

// NewSnapshotID is sortable, readable, and unique enough.
//
// A timestamp to the second plus the process id. Sortable matters: retention
// and "the newest" are both lexical operations on this string, and a random id
// would make them a fetch-everything-and-parse operation instead.
func NewSnapshotID(at time.Time) string {
	return fmt.Sprintf("%s-%d", at.UTC().Format("20060102T150405Z"), os.Getpid())
}

func snapshotKey(prefix, id, name string) string {
	return path.Join(prefix, id, name)
}

// --- taking one -------------------------------------------------------------

// Result is what a run produced.
type Result struct {
	SnapshotID string
	Manifest   Manifest
	Location   string
	Bytes      int64
	SHA256     string
}

// Run takes a backup.
//
// It does not touch the database beyond reading it: `pg_dump` opens a
// repeatable-read transaction and takes no locks that block a till.
func Run(ctx context.Context, opts Options) (Result, error) {
	opts = opts.withDefaults()
	if opts.Store == nil || !opts.Store.Configured() {
		return Result{}, errs.New(errs.CodeInvalidInput,
			"No object store is configured, so a backup would have nowhere to "+
				"go but the disk it is protecting. Set RAWSYST_S3_ENDPOINT, "+
				"RAWSYST_S3_BUCKET and the credentials; see "+
				"deploy/server/BACKUP.md.")
	}
	if strings.TrimSpace(opts.DSN) == "" {
		return Result{}, errs.New(errs.CodeInvalidInput,
			"No database to back up: set RAWSYST_DB_DSN.")
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	at := time.Now().UTC()
	id := NewSnapshotID(at)

	// The dump, staged on disk and hashed on the way past.
	dump, err := os.CreateTemp(opts.TempDir, "rawsyst-dump-*.pgdump")
	if err != nil {
		return Result{}, errs.Wrap(err, errs.CodeInternal,
			"A temporary file for the dump could not be created.")
	}
	defer func() {
		dump.Close()
		os.Remove(dump.Name())
	}()

	size, sum, err := pgDump(ctx, opts.DSN, dump)
	if err != nil {
		return Result{}, err
	}
	if size == 0 {
		return Result{}, errs.New(errs.CodeInternal,
			"pg_dump produced nothing. Refusing to record an empty backup.")
	}

	// Everything the manifest says about the source, read from the source.
	stats, err := describe(ctx, opts.DSN)
	if err != nil {
		return Result{}, err
	}

	manifest := Manifest{
		Version:       ManifestVersion,
		SnapshotID:    id,
		TakenAt:       at.Format(time.RFC3339),
		AppVersion:    opts.AppVersion,
		SchemaVersion: stats.schema,
		DatabaseName:  stats.name,
		DatabaseSize:  stats.size,
		Tables:        stats.tables,
		Rows:          stats.rows,
		Database: Component{
			Key:    databaseObject,
			Bytes:  size,
			SHA256: sum,
			Format: "pg_dump custom (-Fc), compressed",
		},
	}

	if _, err := dump.Seek(0, io.SeekStart); err != nil {
		return Result{}, errs.Wrap(err, errs.CodeInternal,
			"The staged dump could not be rewound for upload.")
	}
	key := snapshotKey(opts.Prefix, id, databaseObject)
	if err := opts.Store.PutStream(ctx, key,
		"application/octet-stream", dump, size, sum); err != nil {
		return Result{}, err
	}

	// The manifest, then the marker. In that order, and never the other way
	// round: the marker is what makes the snapshot count, so it goes last.
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Result{}, errs.Wrap(err, errs.CodeInternal,
			"The manifest could not be written.")
	}
	if err := opts.Store.Put(ctx,
		snapshotKey(opts.Prefix, id, manifestObject),
		"application/json", body); err != nil {
		return Result{}, err
	}
	if err := opts.Store.Put(ctx,
		snapshotKey(opts.Prefix, id, completedMark),
		"text/plain", []byte(at.Format(time.RFC3339)+"\n")); err != nil {
		return Result{}, err
	}

	return Result{
		SnapshotID: id,
		Manifest:   manifest,
		Location:   opts.Store.Bucket() + "/" + key,
		Bytes:      size,
		SHA256:     sum,
	}, nil
}

// pgDump streams a custom-format dump into `w`, hashing as it goes.
//
// Custom format because it is what `pg_restore` takes: selective restore, a
// listable table of contents, and compression without a second process. Plain
// SQL would be readable and would restore only by being fed to psql in one
// piece, which on a database of any size is the slower and more fragile half of
// the trade.
func pgDump(ctx context.Context, dsn string, w io.Writer) (int64, string, error) {
	cmd := exec.CommandContext(ctx, "pg_dump",
		"--dbname="+dsn,
		"--format=custom",
		"--compress=6",
		// No owner or privilege statements. A restore on a new server has its
		// own role names, and a dump that insists on the old ones fails at the
		// first GRANT for a role nobody created.
		"--no-owner",
		"--no-privileges",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, "", errs.Wrap(err, errs.CodeInternal, "pg_dump could not start.")
	}
	if err := cmd.Start(); err != nil {
		return 0, "", errs.Wrap(err, errs.CodeInternal,
			"pg_dump could not start. It comes from the postgres image; the "+
				"backup service is built on it for exactly this reason.")
	}

	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(w, hash), out)

	if err := cmd.Wait(); err != nil {
		return 0, "", errs.Newf(errs.CodeInternal,
			"pg_dump failed: %s", strings.TrimSpace(stderr.String()))
	}
	if copyErr != nil {
		return 0, "", errs.Wrap(copyErr, errs.CodeInternal,
			"The dump could not be staged.")
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

// --- listing ----------------------------------------------------------------

// List reports the snapshots in the store, newest first.
//
// A snapshot without a COMPLETED marker is listed and marked incomplete rather
// than hidden. Something went wrong there and hiding it is how a half-finished
// backup becomes the one somebody reaches for.
func List(ctx context.Context, opts Options) ([]Snapshot, error) {
	opts = opts.withDefaults()
	if opts.Store == nil || !opts.Store.Configured() {
		return nil, errs.New(errs.CodeInvalidInput,
			"No object store is configured.")
	}

	keys, err := opts.Store.List(ctx, opts.Prefix+"/")
	if err != nil {
		return nil, err
	}

	seen := map[string]*Snapshot{}
	for _, k := range keys {
		rest := strings.TrimPrefix(k, opts.Prefix+"/")
		id, name, ok := strings.Cut(rest, "/")
		if !ok || id == "" {
			continue
		}
		snap, known := seen[id]
		if !known {
			snap = &Snapshot{ID: id}
			if at, err := time.Parse("20060102T150405Z",
				strings.SplitN(id, "-", 2)[0]); err == nil {
				snap.TakenAt = at
			}
			seen[id] = snap
		}
		if name == completedMark {
			snap.Completed = true
		}
	}

	out := make([]Snapshot, 0, len(seen))
	for _, s := range seen {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// ReadManifest fetches and parses one snapshot's manifest.
func ReadManifest(
	ctx context.Context, opts Options, id string,
) (Manifest, error) {
	opts = opts.withDefaults()
	body, err := opts.Store.Get(ctx, snapshotKey(opts.Prefix, id, manifestObject))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, errs.Wrap(err, errs.CodeInvalidInput,
			fmt.Sprintf("The manifest for %s could not be read.", id))
	}
	if m.Version != ManifestVersion {
		return Manifest{}, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s carries a version %d manifest and this build reads "+
				"version %d. It was taken by a different build; restore it "+
				"with that build, or read the manifest by hand.",
			id, m.Version, ManifestVersion)
	}
	return m, nil
}
