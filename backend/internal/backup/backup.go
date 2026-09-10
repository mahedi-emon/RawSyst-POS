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
// It is a whole-database dump, not a list of tables somebody maintains. Nothing
// here names a business table: `pg_dump` takes the schema, and the manifest
// describes what it took by asking the catalogue. A table added by a migration
// next month is in the backup and in the verification without anybody
// remembering to add it, which is the only arrangement that stays true.
//
// # The order things happen in, and why
//
//	open a record -> snapshot -> dump -> inventory -> hash -> seal
//	  -> upload -> manifest -> COMPLETED marker -> close the record
//
// The marker is last and it is the only thing that makes a snapshot count. A
// dump that uploaded and a manifest that did not is a snapshot that would
// restore into a database with no idea what it is; a restore refuses anything
// without a marker. That is what stops "the upload returned 200" from being
// mistaken for "there is a backup".
//
// # One snapshot, for the dump and for the numbers it is checked against
//
// This used to dump, then open a second connection and count. Those are two
// different moments, and every sale rung up between them made the manifest
// disagree with the dump — which a verification would report as a corrupt
// backup, on a backup that was fine. Worse, it could go the other way: a table
// that lost rows between the two reads would produce a manifest that agreed
// with a dump that was already short.
//
// So a repeatable-read transaction exports a snapshot, `pg_dump --snapshot`
// uses it, and the inventory is taken in that same transaction. The numbers in
// the manifest describe exactly the bytes in the dump, whatever the shop was
// doing at the time.
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

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/blob"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// ManifestVersion is the shape of the manifest this build writes.
//
// Version 2 added the inventory: every table counted rather than ten, the
// database objects, the per-business totals, and the encryption block. Version
// 1 manifests are still read — see `ReadManifest`, which upgrades one in memory
// rather than refusing it. A backup that becomes unreadable because the product
// moved on is the one failure this whole subsystem cannot have.
const ManifestVersion = 2

// Names inside a snapshot. Fixed, because a restore on a new server has only
// the snapshot id to go on — it cannot ask the old server where it put things.
const (
	databaseObject = "database.dump"
	manifestObject = "manifest.json"
	completedMark  = "COMPLETED"
)

// Stages, in the order they happen, as they are shown to whoever is watching.
//
// Truthful rather than proportional. `pg_dump` does not report progress and
// neither does an S3 PUT of a file, so there is no honest percentage to show;
// what there is is the name of the thing currently happening, and these are the
// names. A progress bar computed from a guess is worse than a word.
const (
	StagePreparing  = "preparing"
	StageDumping    = "dumping"
	StageInventory  = "inventory"
	StageSealing    = "sealing"
	StageUploading  = "uploading"
	StageManifest   = "manifest"
	StageVerifying  = "verifying"
	StageRestoring  = "restoring"
	StageChecking   = "checking"
	StageCleaningUp = "cleaning_up"
	StageDone       = "done"
)

// Manifest is what a snapshot says about itself.
//
// Deliberately free of anything secret. It names what was backed up, how big it
// was and what it hashes to — never a password, a signing key, a token, an API
// credential or an encryption key. Secrets are restored from wherever they are
// kept, which is the operator's password manager and not this file;
// `deploy/server/BACKUP.md` says so and says why.
type Manifest struct {
	Version    int    `json:"manifest_version"`
	SnapshotID string `json:"snapshot_id"`

	// TakenAt is when the run began and is the same value as StartedAt; it is
	// kept because version 1 manifests have it and because it is the field
	// everything sorts on.
	TakenAt     string `json:"taken_at"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`

	// What made it, so a restore can say when a snapshot predates the schema
	// it is being restored into.
	AppVersion  string `json:"app_version"`
	GitCommit   string `json:"git_commit,omitempty"`
	Environment string `json:"source_environment,omitempty"`
	SourceHost  string `json:"source_host,omitempty"`

	SchemaVersion int `json:"schema_version"`

	Database   Component       `json:"database"`
	Encryption *EncryptionInfo `json:"encryption,omitempty"`

	// DatabaseName and the sizes describe the source, not where to find it. A
	// restore is told where to go on the command line.
	DatabaseName    string `json:"database_name"`
	DatabaseSize    int64  `json:"database_size_bytes"`
	PostgresVersion string `json:"postgres_version,omitempty"`

	Tables int `json:"table_count"`

	// Rows is kept at the top level for a person reading the JSON and for
	// version 1 compatibility. The authority is `Inventory`, which has an entry
	// for every table rather than for ten of them.
	Rows map[string]int64 `json:"row_counts,omitempty"`

	// Inventory is what a verification actually compares. Absent on a version 1
	// manifest, where `ReadManifest` synthesises one from the fields above.
	Inventory *Inventory `json:"inventory,omitempty"`

	Storage        StorageRef `json:"storage"`
	RetentionClass string     `json:"retention_class,omitempty"`
}

// StorageRef says where a snapshot went, in terms that can be written down.
//
// The endpoint is recorded as a HOST, never as the configured URL: a URL is the
// kind of string people put credentials in, and a manifest is readable by
// anybody who can read the bucket.
type StorageRef struct {
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint_host,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	Key      string `json:"object_key,omitempty"`
}

// Component is one file in a snapshot.
//
// When the snapshot is sealed, `Bytes` and `SHA256` describe the CIPHERTEXT —
// what the store holds, and therefore what can be checked without the key —
// and the two plaintext fields describe the dump inside it, which is what a
// restore checks after decrypting.
type Component struct {
	Key    string `json:"key"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Format string `json:"format,omitempty"`

	PlainBytes  int64  `json:"plaintext_bytes,omitempty"`
	PlainSHA256 string `json:"plaintext_sha256,omitempty"`
}

// Options are what a run needs to know.
type Options struct {
	// DSN is the database to dump. Never modified by a backup.
	//
	// The role it names must be able to read every row: `pg_dump` turns
	// `row_security` off and fails outright if the role cannot, which on this
	// product is every role except a superuser or one with BYPASSRLS. `Run`
	// checks that before it starts rather than after four minutes of dumping.
	DSN string

	// AppDSN is the application's own connection — the one in RAWSYST_DB_DSN.
	//
	// Never used to read the database. It is used for two things and both are
	// about a RESTORE: the role a restore connects as, so restored objects end
	// up owned by the application exactly as `migrate` leaves them, and the
	// owner of any database created to restore into. Restoring as the
	// administrative role instead produces an intact database the product
	// cannot read a row of.
	AppDSN string

	// Store is where snapshots go. Required: a backup that stays on the server
	// it protects is not a backup, and this refuses rather than pretending.
	Store *blob.Store

	// Prefix namespaces snapshots inside the bucket, so one bucket can hold
	// more than one installation without them treading on each other.
	Prefix string

	// AppVersion, GitCommit and Environment describe the build and the
	// deployment that took it.
	AppVersion  string
	GitCommit   string
	Environment string
	SourceHost  string

	// Key seals the dump before it leaves the server. Zero means no key is
	// configured and the dump is uploaded as it is; the manifest says which.
	Key Key

	// TempDir is where the dump is staged. Defaults to the system temporary
	// directory; on a small server it wants to be somewhere with room.
	TempDir string

	// Timeout bounds the whole run. A backup that hangs holds a connection and
	// a lock and tells nobody.
	Timeout time.Duration

	// Progress is called with each stage as it begins. Optional: the command
	// line ignores it and the agent writes it into the task row, which is what
	// a screen watching a backup reads.
	Progress func(stage string)

	// MinFreePercent is how much of the database's on-disk size must be free
	// where the dump is staged, as a percentage. 100 by default and
	// deliberately pessimistic; see room.go for why the safe direction to be
	// wrong in is this one.
	MinFreePercent int

	// Warn reports something worth saying that is not a reason to stop — a
	// check that could not be performed, rather than a check that failed.
	// Optional.
	Warn func(string)
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
	if o.Progress == nil {
		o.Progress = func(string) {}
	}
	if o.MinFreePercent <= 0 {
		o.MinFreePercent = 100
	}
	if o.Warn == nil {
		o.Warn = func(string) {}
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

// ParseSnapshotID reads the time back out of a snapshot id.
//
// The second return says whether it could be read. A snapshot whose id this
// build does not understand is never deleted by retention and never treated as
// stale by the health readout: not understanding something is a reason to leave
// it alone.
func ParseSnapshotID(id string) (time.Time, bool) {
	at, err := time.Parse("20060102T150405Z", strings.SplitN(id, "-", 2)[0])
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// ValidSnapshotID reports whether a string is safe to use as a snapshot id.
//
// Every route that takes a snapshot id from a request runs it through this
// first. An id becomes a path inside a bucket and the name of a temporary
// database, so `../` or a quote in one is the difference between a listing and
// an object somewhere it should not be. Letters, digits, dash and underscore,
// bounded — nothing else, and no judgement calls about escaping later.
func ValidSnapshotID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func snapshotKey(prefix, id, name string) string {
	return path.Join(prefix, id, name)
}

// SnapshotKey is the object key of one file in a snapshot, for a caller that
// needs to name it — a download, a presigned URL, a manifest read.
func SnapshotKey(prefix, id, name string) string {
	return snapshotKey(prefix, id, name)
}

// DatabaseObject is the name of the dump inside a snapshot.
func DatabaseObject() string { return databaseObject }

// ManifestObject is the name of the manifest inside a snapshot.
func ManifestObject() string { return manifestObject }

// CompletedMark is the name of the marker that makes a snapshot count.
func CompletedMark() string { return completedMark }

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
// It does not touch the database beyond reading it: the export transaction is
// read-only and `pg_dump` takes no locks that block a till.
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

	opts.Progress(StagePreparing)
	at := time.Now().UTC()
	id := NewSnapshotID(at)

	// The export transaction. Everything the dump and the manifest say comes
	// out of this one snapshot of the database.
	conn, err := pgx.Connect(ctx, opts.DSN)
	if err != nil {
		return Result{}, errs.Wrap(err, errs.CodeUnavailable,
			"The database could not be opened to back it up.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	if err := canReadEverything(ctx, conn); err != nil {
		return Result{}, err
	}

	// Room to stage the dump, asked before pg_dump starts. See room.go: on
	// this server the staging volume and the database's volume are usually the
	// same one, so a dump that runs out of space takes the database down with
	// it rather than merely failing.
	if _, err := checkRoom(
		ctx, conn, opts.TempDir, opts.MinFreePercent, opts.Warn,
	); err != nil {
		return Result{}, err
	}

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Result{}, errs.Wrap(err, errs.CodeInternal,
			"A consistent read of the database could not be opened.")
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	var exported string
	if err := tx.QueryRow(ctx, `SELECT pg_export_snapshot()`).
		Scan(&exported); err != nil {
		return Result{}, errs.Wrap(err, errs.CodeInternal,
			"A consistent snapshot could not be exported. Without one the "+
				"dump and the numbers recorded about it would describe two "+
				"different moments.")
	}

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

	opts.Progress(StageDumping)
	size, sum, err := pgDump(ctx, opts.DSN, exported, dump)
	if err != nil {
		return Result{}, err
	}
	if size == 0 {
		return Result{}, errs.New(errs.CodeInternal,
			"pg_dump produced nothing. Refusing to record an empty backup.")
	}

	opts.Progress(StageInventory)
	inventory, err := takeInventory(ctx, tx)
	if err != nil {
		return Result{}, err
	}

	component := Component{
		Key:    databaseObject,
		Bytes:  size,
		SHA256: sum,
		Format: "pg_dump custom (-Fc), compressed",
	}

	// Sealed, if there is a key. The staged plaintext is replaced by a staged
	// ciphertext rather than being held in memory, for the same reason the dump
	// is staged at all.
	upload, uploadName := dump, dump.Name()
	var encryption *EncryptionInfo
	if opts.Key.Set() {
		opts.Progress(StageSealing)
		sealed, sealedSize, sealedSum, err := seal(opts, dump)
		if err != nil {
			return Result{}, err
		}
		defer func() {
			sealed.Close()
			os.Remove(sealed.Name())
		}()
		component.PlainBytes, component.PlainSHA256 = size, sum
		component.Bytes, component.SHA256 = sealedSize, sealedSum
		component.Format = "pg_dump custom (-Fc), compressed, then AES-256-GCM"
		info := opts.Key.Info()
		encryption = &info
		upload, uploadName = sealed, sealed.Name()
		size, sum = sealedSize, sealedSum
	}
	_ = uploadName

	completedAt := time.Now().UTC()
	manifest := Manifest{
		Version:         ManifestVersion,
		SnapshotID:      id,
		TakenAt:         at.Format(time.RFC3339),
		StartedAt:       at.Format(time.RFC3339),
		CompletedAt:     completedAt.Format(time.RFC3339),
		AppVersion:      opts.AppVersion,
		GitCommit:       opts.GitCommit,
		Environment:     opts.Environment,
		SourceHost:      opts.SourceHost,
		SchemaVersion:   inventory.SchemaVersion,
		DatabaseName:    inventory.Database,
		DatabaseSize:    inventory.Size,
		PostgresVersion: inventory.ServerVersion,
		Tables:          inventory.TableCount(),
		Rows:            inventory.Rows,
		Inventory:       &inventory,
		Database:        component,
		Encryption:      encryption,
		Storage: StorageRef{
			Provider: "s3-compatible",
			Endpoint: opts.Store.EndpointHost(),
			Bucket:   opts.Store.Bucket(),
			Prefix:   opts.Prefix,
			Key:      snapshotKey(opts.Prefix, id, databaseObject),
		},
		RetentionClass: RetentionClassOf(at),
	}

	if _, err := upload.Seek(0, io.SeekStart); err != nil {
		return Result{}, errs.Wrap(err, errs.CodeInternal,
			"The staged dump could not be rewound for upload.")
	}
	opts.Progress(StageUploading)
	key := snapshotKey(opts.Prefix, id, databaseObject)
	if err := opts.Store.PutStream(ctx, key,
		"application/octet-stream", upload, size, sum); err != nil {
		return Result{}, err
	}

	// The manifest, then the marker. In that order, and never the other way
	// round: the marker is what makes the snapshot count, so it goes last.
	opts.Progress(StageManifest)
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
		"text/plain", []byte(completedAt.Format(time.RFC3339)+"\n")); err != nil {
		return Result{}, err
	}

	opts.Progress(StageDone)
	return Result{
		SnapshotID: id,
		Manifest:   manifest,
		Location:   opts.Store.Bucket() + "/" + key,
		Bytes:      size,
		SHA256:     sum,
	}, nil
}

// canReadEverything refuses a backup the connecting role cannot actually take.
//
// This product forces row-level security on every tenant table, which applies
// to the table's OWNER as well — that is the point of forcing it. `pg_dump`
// turns `row_security` off so that it dumps every row, and Postgres refuses
// that to any role which is not a superuser and does not have BYPASSRLS. The
// dump then fails partway with a message about a policy on whichever table came
// first alphabetically, which is a true statement about the twentieth thing
// somebody would think to check.
//
// So it is checked here, in one query, before anything is spent — and the
// message says what to do about it. The answer is a role for backups: the
// application's own role must NOT have BYPASSRLS, because that is the guarantee
// keeping one shop out of another's books.
func canReadEverything(ctx context.Context, conn *pgx.Conn) error {
	var user string
	var super, bypass bool
	if err := conn.QueryRow(ctx, `
		SELECT current_user, rolsuper, rolbypassrls
		FROM pg_roles WHERE rolname = current_user`).
		Scan(&user, &super, &bypass); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The database would not say what this connection may read.")
	}
	if !super && !bypass {
		return errs.Newf(errs.CodeInvalidInput,
			"The role %q cannot take a backup: row-level security is forced on "+
				"this database and pg_dump refuses to run as a role that "+
				"cannot see past it. The dump would fail partway with a "+
				"message about a policy on whichever table came first "+
				"alphabetically. Give the backup its OWN role with BYPASSRLS "+
				"and point RAWSYST_BACKUP_DSN at it; deploy/server/BACKUP.md "+
				"has the four statements. Do NOT give BYPASSRLS to the "+
				"application's role: that attribute is the only thing keeping "+
				"one business out of another's books.", user)
	}

	// Seeing past the policies is not the same as being allowed to read the
	// tables. A role with BYPASSRLS and no SELECT grant produces `permission
	// denied for table X` after pg_dump has already taken a lock on every
	// table in the database — which is a true statement about the least
	// interesting thing that went wrong.
	//
	// This is the check that matters when a migration adds a table and nobody
	// re-ran the grants. `ALTER DEFAULT PRIVILEGES` is what stops it happening
	// again, and the message says so.
	// Tables AND sequences. A sequence the role cannot read stops `pg_dump`
	// just as dead as a table it cannot read, with a message about
	// `audit_log_id_seq` that reads as a permissions puzzle rather than as a
	// missing grant — which is how it was found.
	var missing []string
	rows, err := conn.Query(ctx, `
		SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'S')
		  AND NOT has_table_privilege(current_user, c.oid, 'SELECT')
		ORDER BY c.relname`)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The database would not say which tables this connection may read.")
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return errs.Wrap(err, errs.CodeInternal,
				"The database would not say which tables this connection may "+
					"read.")
		}
		missing = append(missing, name)
	}
	if err := rows.Err(); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The database would not say which tables this connection may read.")
	}
	if len(missing) > 0 {
		return errs.Newf(errs.CodeInvalidInput,
			"The role %q may not read %d of this database's tables and "+
				"sequences, so a backup taken as it would be missing them: "+
				"%s. Run GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s and "+
				"GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO %s, then "+
				"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON "+
				"TABLES TO %s (and the same for SEQUENCES) so the next "+
				"migration does not put this back. "+
				"deploy/server/BACKUP.md has all of it in one block.",
			user, len(missing), strings.Join(clip(missing, 8), ", "),
			user, user, user)
	}
	return nil
}

// seal writes an encrypted copy of the staged dump and returns it.
func seal(opts Options, dump *os.File) (*os.File, int64, string, error) {
	if _, err := dump.Seek(0, io.SeekStart); err != nil {
		return nil, 0, "", errs.Wrap(err, errs.CodeInternal,
			"The staged dump could not be rewound to seal it.")
	}
	sealed, err := os.CreateTemp(opts.TempDir, "rawsyst-sealed-*.enc")
	if err != nil {
		return nil, 0, "", errs.Wrap(err, errs.CodeInternal,
			"A temporary file for the sealed dump could not be created.")
	}
	hash := sha256.New()
	n, err := opts.Key.Seal(io.MultiWriter(sealed, hash), dump)
	if err != nil {
		sealed.Close()
		os.Remove(sealed.Name())
		return nil, 0, "", err
	}
	return sealed, n, hex.EncodeToString(hash.Sum(nil)), nil
}

// pgDump streams a custom-format dump into `w`, hashing as it goes.
//
// Custom format because it is what `pg_restore` takes: selective restore, a
// listable table of contents, and compression without a second process. Plain
// SQL would be readable and would restore only by being fed to psql in one
// piece, which on a database of any size is the slower and more fragile half of
// the trade.
//
// `--snapshot` ties it to the transaction the caller is holding open, so the
// dump and the inventory taken beside it describe the same instant.
func pgDump(
	ctx context.Context, dsn, snapshot string, w io.Writer,
) (int64, string, error) {
	args := []string{
		"--dbname=" + dsn,
		"--format=custom",
		"--compress=6",
		// No owner or privilege statements. A restore on a new server has its
		// own role names, and a dump that insists on the old ones fails at the
		// first GRANT for a role nobody created.
		"--no-owner",
		"--no-privileges",
	}
	if snapshot != "" {
		args = append(args, "--snapshot="+snapshot)
	}
	cmd := exec.CommandContext(ctx, "pg_dump", args...)
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
	// pg_dump can exit 0 having written a diagnostic. A dump that reported a
	// problem is not one to record as clean, whatever its exit code said.
	if msg := strings.TrimSpace(stderr.String()); strings.Contains(msg, "error:") {
		return 0, "", errs.Newf(errs.CodeInternal, "pg_dump reported: %s", msg)
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
			if at, ok := ParseSnapshotID(id); ok {
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
	if !ValidSnapshotID(id) {
		return Manifest{}, errs.New(errs.CodeInvalidInput,
			"That is not a snapshot id.")
	}
	body, err := opts.Store.Get(ctx, snapshotKey(opts.Prefix, id, manifestObject))
	if err != nil {
		return Manifest{}, err
	}
	return ParseManifest(body, id)
}

// ParseManifest reads a manifest from bytes, wherever they came from.
//
// Separated from the fetch because a manifest also arrives from an operator's
// laptop, in a package uploaded to a new server that has never seen the store
// the backup came from. The checks are the same in both directions.
//
// # Older manifests are upgraded, never refused
//
// A version 1 manifest has ten row counts and no inventory. Refusing it would
// mean a build could not restore the backups the build before it took, which
// makes an upgrade a moment when the business is unprotected. So it is read and
// an inventory is synthesised from what it does say, and a verification of it
// checks what it can — recorded in the report, so nobody mistakes a thinner
// check for a full one.
func ParseManifest(body []byte, id string) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, errs.Newf(errs.CodeInvalidInput,
			"The manifest for %s is not readable JSON. Whatever it is, it is "+
				"not a manifest this product wrote.", id)
	}
	switch {
	case m.Version <= 0:
		return Manifest{}, errs.Newf(errs.CodeInvalidInput,
			"The manifest for %s does not say which version it is.", id)
	case m.Version > ManifestVersion:
		return Manifest{}, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s carries a version %d manifest and this build reads "+
				"up to version %d. It was taken by a NEWER build than this "+
				"one; restore it with that build.", id, m.Version, ManifestVersion)
	}
	if strings.TrimSpace(m.SnapshotID) == "" {
		return Manifest{}, errs.Newf(errs.CodeInvalidInput,
			"The manifest for %s does not name a snapshot.", id)
	}
	if id != "" && m.SnapshotID != id {
		return Manifest{}, errs.Newf(errs.CodeInvalidInput,
			"This manifest says it belongs to snapshot %s and it was found "+
				"under %s. One of them is not what it claims to be, and "+
				"neither will be restored.", m.SnapshotID, id)
	}
	if strings.TrimSpace(m.Database.SHA256) == "" || m.Database.Bytes <= 0 {
		return Manifest{}, errs.Newf(errs.CodeInvalidInput,
			"The manifest for %s does not record a size and checksum for the "+
				"dump, so nothing about the dump can be checked.", m.SnapshotID)
	}
	if m.Inventory == nil {
		m.Inventory = inventoryFromV1(m)
	}
	return m, nil
}

// inventoryFromV1 makes an older manifest comparable.
//
// It knows the schema version, the table count and ten row counts. That is
// less than a version 2 manifest and it is what there is; a verification
// against it says so in its report rather than implying it checked more.
func inventoryFromV1(m Manifest) *Inventory {
	inv := &Inventory{
		Database:      m.DatabaseName,
		Size:          m.DatabaseSize,
		SchemaVersion: m.SchemaVersion,
		Rows:          map[string]int64{},
		TenantRows:    map[string]int64{},
		CompanyRows:   map[string]int64{},
		Sequences:     map[string]int64{},
		Extensions:    map[string]string{},
	}
	for table, n := range m.Rows {
		inv.Rows[table] = n
		inv.Tables = append(inv.Tables, table)
	}
	sort.Strings(inv.Tables)
	return inv
}

// Complete reports whether a manifest carries a full inventory.
//
// A version 1 manifest does not, and a verification against one is thinner than
// a verification against a version 2. Both are honest; only one of them is a
// complete check of the database, and the difference is recorded rather than
// glossed over.
func (m Manifest) Complete() bool {
	return m.Version >= 2 && m.Inventory != nil && len(m.Inventory.Tables) > 0
}
