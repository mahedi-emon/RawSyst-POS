// The physical base backup, which is the half of point-in-time recovery that
// is not the write-ahead log.
//
// # Why a dump is not a base backup, however good the dump is
//
// `pg_dump` produces SQL: a description of the database as it was at one
// instant, which recreates that instant by being replayed. The write-ahead log
// is not SQL. It is a record of changes to PAGES — block 37 of relation 16384
// became these bytes — and it can only be applied to the exact physical cluster
// those page numbers refer to.
//
// So WAL cannot be replayed onto a restored dump. Not slowly, not with effort:
// the relation file numbers are different, the pages are laid out differently,
// and the log is meaningless against them. Point-in-time recovery requires a
// byte-level copy of the data directory, and that is what `pg_basebackup`
// takes and what this file stores.
//
// This is an ADDITION to the dumps in `backup.go` and not a replacement for
// them, and the two protect against different things:
//
//	pg_dump       portable, readable on any server, survives a corrupt
//	              cluster, restores one database, recovery point is the dump
//	pg_basebackup byte-identical, replayable to any second, tied to this
//	              PostgreSQL major version and this architecture
//
// A corrupted page is in the base backup and in the WAL; it is not in the dump.
// Losing the server at 22:00 costs a trading day with only the dump. Keeping
// both is the answer, and neither is redundant.
//
// # What is stored, and in what order
//
//	pg_basebackup -> base.tar.gz, pg_wal.tar.gz, backup_manifest
//	  -> seal each -> upload each -> manifest.json -> COMPLETED
//
// COMPLETED last, for the same reason a snapshot has one: an upload that got
// three files of four in is not a base backup, and a recovery that started from
// one would fail on the missing piece after an hour of downloading. Nothing
// without the marker is ever offered as a recovery source.
//
// # Why the WAL is bundled as well as archived
//
// `--wal-method=stream` makes `pg_basebackup` open a second connection and
// stream the segments written WHILE it runs into `pg_wal.tar.gz`. Those same
// segments also go through `archive_command` into the archive, so this is a
// deliberate duplicate — and it is the duplicate that makes a base backup
// self-consistent on its own. Without it, a base backup taken during a window
// when archiving was broken is a backup that cannot even reach its own
// consistency point, and it would not find that out until somebody tried to
// recover from it.
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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The files inside one base backup. Fixed for ever once one has been written:
// a recovery three years from now has the id and these names and nothing else.
const (
	baseTarObject        = "base.tar.gz"
	baseWALTarObject     = "pg_wal.tar.gz"
	basePGManifestObject = "backup_manifest"
	baseManifestObject   = "manifest.json"
)

// BaseManifestVersion is the shape of base backup manifest this build writes.
const BaseManifestVersion = 1

// Stages a base backup reports, in the order they happen.
const (
	StageBaseBackup = "base_backup"
	StageFetching   = "fetching"
	StageExtracting = "extracting"
	StageRecovering = "recovering"
	StagePromoting  = "promoting"
)

// BaseManifest is what a base backup says about itself.
//
// Free of anything secret, like a snapshot manifest and for the same reason: it
// sits beside the backup and is readable by anybody who can read the bucket.
type BaseManifest struct {
	Version int    `json:"manifest_version"`
	ID      string `json:"base_backup_id"`

	TakenAt     string `json:"taken_at"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	TookSeconds int    `json:"took_seconds"`

	AppVersion  string `json:"app_version"`
	GitCommit   string `json:"git_commit,omitempty"`
	Environment string `json:"source_environment,omitempty"`
	SourceHost  string `json:"source_host,omitempty"`

	// PostgresVersion is the part that decides whether this backup is usable at
	// all. A physical backup can only be read by the SAME major version that
	// wrote it: restoring a 17 cluster onto an 18 binary does not migrate, it
	// refuses. Recorded so a restore can say that before it spends an hour
	// downloading.
	PostgresVersion    string `json:"postgres_version"`
	PostgresVersionNum int    `json:"postgres_version_num"`

	DatabaseName string `json:"database_name,omitempty"`
	ClusterSize  int64  `json:"cluster_size_bytes,omitempty"`

	// BootstrapRole is the superuser this cluster was created with.
	//
	// Recorded because a recovered cluster has the roles the SOURCE had, not
	// the ones the machine recovering it has, and something has to connect to
	// the result to count it. Assuming `postgres` is wrong the moment anybody
	// runs the official image with POSTGRES_USER set to anything else — which
	// this product's own compose file does. Read from OID 10, which is the
	// bootstrap superuser by definition and cannot be renamed out from under
	// this.
	BootstrapRole string `json:"bootstrap_role,omitempty"`

	// Where in the write-ahead log this backup sits. These four are what make
	// it a point-in-time recovery source rather than a file: the starting
	// position is where replay begins, and the ending position is the earliest
	// moment the restored cluster is CONSISTENT — a recovery target before it
	// is not a valid target and is refused rather than attempted.
	Timeline     uint32 `json:"timeline"`
	StartLSN     string `json:"start_lsn"`
	EndLSN       string `json:"end_lsn"`
	StartSegment string `json:"start_wal_segment"`
	EndSegment   string `json:"end_wal_segment"`

	WALSegmentSize int64 `json:"wal_segment_size_bytes"`

	Components []Component     `json:"components"`
	Encryption *EncryptionInfo `json:"encryption,omitempty"`

	Storage        StorageRef `json:"storage"`
	RetentionClass string     `json:"retention_class,omitempty"`
}

// TotalBytes is what the base backup occupies in the store.
func (m BaseManifest) TotalBytes() int64 {
	var n int64
	for _, c := range m.Components {
		n += c.Bytes
	}
	return n
}

// Component returns one named component of the backup.
func (m BaseManifest) Component(name string) (Component, bool) {
	for _, c := range m.Components {
		if c.Key == name {
			return c, true
		}
	}
	return Component{}, false
}

// StartedTime reads when the backup began.
func (m BaseManifest) StartedTime() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, m.StartedAt)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// CompletedTime reads when the backup finished, which is the EARLIEST moment
// it can recover to. Everything before that is inside the backup window, when
// the cluster on disk was inconsistent.
func (m BaseManifest) CompletedTime() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, m.CompletedAt)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// BaseBackupOptions is what taking one needs.
type BaseBackupOptions struct {
	// DSN is the cluster to copy. The role it names needs the REPLICATION
	// attribute, and `pg_hba.conf` has to admit it to the `replication`
	// database — which is a SEPARATE entry from the ordinary one and is the
	// single most common reason a first base backup fails. See
	// `CheckBaseBackupReady`, which says so before anything is spent.
	DSN string

	// WAL carries the store, the prefix and the key. A base backup and the
	// segments that follow it are one recovery source and they are configured
	// as one thing.
	WAL WALOptions

	// StagingDir is where `pg_basebackup` writes before anything is uploaded.
	// It needs room for a compressed copy of the whole cluster.
	StagingDir string

	// MinFreePercent is how much of the cluster's size must be free there, as
	// a percentage. The same pessimism as a dump, for the same reason: on this
	// server the staging volume and the data volume are often the same one, so
	// running out of room takes the database down rather than merely failing.
	MinFreePercent int

	Timeout time.Duration

	AppVersion  string
	GitCommit   string
	Environment string
	SourceHost  string

	Progress func(stage string)
	Warn     func(string)
}

func (o BaseBackupOptions) withDefaults() BaseBackupOptions {
	o.WAL = o.WAL.withDefaults()
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Hour
	}
	if o.MinFreePercent <= 0 {
		o.MinFreePercent = 100
	}
	if o.Progress == nil {
		o.Progress = func(string) {}
	}
	if o.Warn == nil {
		o.Warn = func(string) {}
	}
	if o.AppVersion == "" {
		o.AppVersion = "dev"
	}
	return o
}

// --- taking one -------------------------------------------------------------

// TakeBaseBackup copies the cluster and puts it in the store.
func TakeBaseBackup(
	ctx context.Context, opts BaseBackupOptions,
) (BaseManifest, error) {
	opts = opts.withDefaults()
	if !opts.WAL.Configured() {
		return BaseManifest{}, errs.New(errs.CodeInvalidInput,
			"No object store is configured, so a base backup would have "+
				"nowhere to go but the disk it is protecting.")
	}
	if strings.TrimSpace(opts.DSN) == "" {
		return BaseManifest{}, errs.New(errs.CodeInvalidInput,
			"No database to take a base backup of.")
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	opts.Progress(StagePreparing)
	started := time.Now().UTC()
	id := NewSnapshotID(started)

	ready, err := CheckBaseBackupReady(ctx, opts.DSN)
	if err != nil {
		return BaseManifest{}, err
	}

	// Room, asked before pg_basebackup starts rather than discovered after
	// forty minutes of copying.
	conn, err := pgx.Connect(ctx, opts.DSN)
	if err != nil {
		return BaseManifest{}, errs.Wrap(err, errs.CodeUnavailable,
			"The database could not be opened to take a base backup of it.")
	}
	if _, err := checkRoom(
		ctx, conn, opts.StagingDir, opts.MinFreePercent, opts.Warn,
	); err != nil {
		conn.Close(context.WithoutCancel(ctx))
		return BaseManifest{}, err
	}
	conn.Close(context.WithoutCancel(ctx))

	dir := filepath.Join(stagingRoot(opts.StagingDir), "basebackup-"+id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BaseManifest{}, errs.Wrap(err, errs.CodeInternal,
			"A staging directory for the base backup could not be created.")
	}
	defer os.RemoveAll(dir)

	opts.Progress(StageBaseBackup)
	if err := runPGBaseBackup(ctx, opts, dir); err != nil {
		return BaseManifest{}, err
	}

	pgManifest, err := readPGBackupManifest(filepath.Join(dir, basePGManifestObject))
	if err != nil {
		return BaseManifest{}, err
	}

	layout := opts.WAL.Layout
	startLSN, _ := ParseLSN(pgManifest.StartLSN)
	endLSN, _ := ParseLSN(pgManifest.EndLSN)

	manifest := BaseManifest{
		Version:            BaseManifestVersion,
		ID:                 id,
		TakenAt:            started.Format(time.RFC3339),
		StartedAt:          started.Format(time.RFC3339),
		AppVersion:         opts.AppVersion,
		GitCommit:          opts.GitCommit,
		Environment:        opts.Environment,
		SourceHost:         opts.SourceHost,
		PostgresVersion:    ready.ServerVersion,
		PostgresVersionNum: ready.VersionNum,
		DatabaseName:       ready.Database,
		ClusterSize:        ready.ClusterSize,
		BootstrapRole:      ready.BootstrapRole,
		Timeline:           pgManifest.Timeline,
		StartLSN:           FormatLSN(startLSN),
		EndLSN:             FormatLSN(endLSN),
		StartSegment:       SegmentForLSN(pgManifest.Timeline, startLSN, layout).String(),
		EndSegment:         SegmentForLSN(pgManifest.Timeline, endLSN, layout).String(),
		WALSegmentSize:     layout.SegmentSize,
		RetentionClass:     RetentionClassOf(started),
		Storage: StorageRef{
			Provider: "s3-compatible",
			Endpoint: opts.WAL.Store.EndpointHost(),
			Bucket:   opts.WAL.Store.Bucket(),
			Prefix:   cleanPrefix(opts.WAL.Prefix),
			Key:      strings.TrimSuffix(BaseBackupPrefix(opts.WAL.Prefix), "/") + "/" + id,
		},
	}
	if opts.WAL.Key.Set() {
		info := opts.WAL.Key.Info()
		manifest.Encryption = &info
	}

	// Seal and upload each piece. Streamed from disk with a known length and a
	// known checksum, never held in memory: a base backup is the size of the
	// database, and this container has a few hundred megabytes.
	for _, name := range []string{
		baseTarObject, baseWALTarObject, basePGManifestObject,
	} {
		src := filepath.Join(dir, name)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) && name == baseWALTarObject {
				return BaseManifest{}, errs.New(errs.CodeInternal,
					"pg_basebackup produced no pg_wal.tar.gz. Without the "+
						"write-ahead log written during the copy, this backup "+
						"cannot reach its own consistency point and is not a "+
						"recovery source. Refusing to record it.")
			}
			return BaseManifest{}, errs.Wrap(err, errs.CodeInternal, fmt.Sprintf(
				"pg_basebackup produced no %s.", name))
		}

		opts.Progress(StageSealing)
		component, upload, err := prepareBaseComponent(opts, dir, name)
		if err != nil {
			return BaseManifest{}, err
		}

		opts.Progress(StageUploading)
		if err := uploadFile(
			ctx, opts.WAL, upload, component,
			mustBaseKey(opts.WAL.Prefix, id, name),
		); err != nil {
			return BaseManifest{}, err
		}
		manifest.Components = append(manifest.Components, component)
	}

	completed := time.Now().UTC()
	manifest.CompletedAt = completed.Format(time.RFC3339)
	manifest.TookSeconds = int(completed.Sub(started).Seconds())

	opts.Progress(StageManifest)
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BaseManifest{}, errs.Wrap(err, errs.CodeInternal,
			"The base backup manifest could not be written.")
	}
	if err := opts.WAL.Store.Put(ctx,
		mustBaseKey(opts.WAL.Prefix, id, baseManifestObject),
		"application/json", body); err != nil {
		return BaseManifest{}, err
	}
	if err := opts.WAL.Store.Put(ctx,
		mustBaseKey(opts.WAL.Prefix, id, completedMark),
		"text/plain", []byte(manifest.CompletedAt+"\n")); err != nil {
		return BaseManifest{}, err
	}

	opts.Progress(StageDone)
	return manifest, nil
}

// prepareBaseComponent seals one staged file if there is a key, and describes
// what will be uploaded.
func prepareBaseComponent(
	opts BaseBackupOptions, dir, name string,
) (Component, string, error) {
	src := filepath.Join(dir, name)
	plainSize, plainSum, err := hashFile(src)
	if err != nil {
		return Component{}, "", err
	}
	component := Component{
		Key: name, Bytes: plainSize, SHA256: plainSum,
		Format: "gzip tar, as pg_basebackup wrote it",
	}
	if name == basePGManifestObject {
		component.Format = "the PostgreSQL backup manifest, as written"
	}
	if !opts.WAL.Key.Set() {
		return component, src, nil
	}

	sealedPath := src + ".enc"
	size, sum, err := sealFile(opts.WAL.Key, src, sealedPath)
	if err != nil {
		return Component{}, "", err
	}
	component.PlainBytes, component.PlainSHA256 = plainSize, plainSum
	component.Bytes, component.SHA256 = size, sum
	component.Format += ", then AES-256-GCM"
	return component, sealedPath, nil
}

func uploadFile(
	ctx context.Context, opts WALOptions, path string, c Component, key string,
) error {
	f, err := os.Open(path)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"A staged base backup file could not be reopened for upload.")
	}
	defer f.Close()
	return opts.Store.PutStream(
		ctx, key, "application/octet-stream", f, c.Bytes, c.SHA256)
}

// mustBaseKey builds a base backup object key.
//
// The id comes from `NewSnapshotID` and the name from the fixed list above, so
// the validation inside `BaseBackupKey` cannot fail here. It is called anyway,
// and a failure is turned into a key that cannot resolve rather than being
// ignored, so that a future caller passing something else does not quietly get
// a path outside the prefix.
func mustBaseKey(prefix, id, name string) string {
	key, err := BaseBackupKey(prefix, id, name)
	if err != nil {
		return ""
	}
	return key
}

// --- running the tool -------------------------------------------------------

func runPGBaseBackup(ctx context.Context, opts BaseBackupOptions, dir string) error {
	args := []string{
		"--pgdata=" + dir,
		"--format=tar",
		"--compress=gzip",

		// The write-ahead log written while the copy runs, streamed on a
		// second connection into pg_wal.tar.gz. See the package note: this is
		// what makes the backup able to reach its own consistency point
		// without the archive.
		"--wal-method=stream",

		// An immediate checkpoint. The alternative spreads the checkpoint over
		// `checkpoint_completion_target` to be gentle on the disk, which on a
		// quiet shop server means waiting several minutes before the copy even
		// begins. The I/O spike is worth the predictability.
		"--checkpoint=fast",

		// SHA-256 per file rather than the default CRC-32C. CRC catches a
		// flipped bit; it does not stand up to anything deliberate, and this
		// manifest is what `pg_verifybackup` later trusts.
		"--manifest-checksums=SHA256",

		"--no-password",
		"--verbose",
		"--dbname=" + opts.DSN,
	}
	cmd := exec.CommandContext(ctx, "pg_basebackup", args...)
	cmd.Env = append(os.Environ(), "PGAPPNAME=rawsyst-basebackup")

	var errOut strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &limitedWriter{w: &errOut, n: 16 << 10}

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errOut.String())
		if ctx.Err() != nil {
			return errs.New(errs.CodeUnavailable,
				"The base backup ran out of time and was stopped. Nothing "+
					"was recorded; the cluster is untouched.")
		}
		return errs.Wrap(err, errs.CodeInternal, fmt.Sprintf(
			"pg_basebackup failed. %s", firstUsefulLine(detail)))
	}
	return nil
}

// pgBackupManifest is the part of PostgreSQL's own manifest this reads.
//
// Only three fields, out of a document that lists every file in the cluster
// with a checksum. Decoding the whole `Files` array would be tens of megabytes
// of allocation to learn two log positions; the rest of the document is left on
// disk, uploaded as written, and handed to `pg_verifybackup` at recovery time,
// which is the tool that actually knows how to read it.
type pgBackupManifest struct {
	Timeline uint32
	StartLSN string
	EndLSN   string
}

func readPGBackupManifest(path string) (pgBackupManifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return pgBackupManifest{}, errs.Wrap(err, errs.CodeInternal,
			"pg_basebackup produced no backup manifest, so where this backup "+
				"sits in the write-ahead log is unknown. Without that it "+
				"cannot be used as a point-in-time recovery source.")
	}
	defer f.Close()

	var doc struct {
		Version   int `json:"PostgreSQL-Backup-Manifest-Version"`
		WALRanges []struct {
			Timeline uint32 `json:"Timeline"`
			StartLSN string `json:"Start-LSN"`
			EndLSN   string `json:"End-LSN"`
		} `json:"WAL-Ranges"`
	}
	if err := json.NewDecoder(f).Decode(&doc); err != nil {
		return pgBackupManifest{}, errs.Wrap(err, errs.CodeInternal,
			"The PostgreSQL backup manifest could not be read.")
	}
	if len(doc.WALRanges) == 0 {
		return pgBackupManifest{}, errs.New(errs.CodeInternal,
			"The PostgreSQL backup manifest names no write-ahead log range, "+
				"so there is no way to say where replay should begin.")
	}

	// The first range starts the backup and the last ends it. On a cluster
	// that has never been recovered there is exactly one; after a recovery
	// there can be several, one per timeline the backup spans.
	first, last := doc.WALRanges[0], doc.WALRanges[len(doc.WALRanges)-1]
	return pgBackupManifest{
		Timeline: last.Timeline,
		StartLSN: first.StartLSN,
		EndLSN:   last.EndLSN,
	}, nil
}

// --- can this server even take one ------------------------------------------

// BaseBackupReadiness is what a base backup needs from the server, checked.
type BaseBackupReadiness struct {
	Role        string `json:"role"`
	Database    string `json:"database"`
	Replication bool   `json:"role_may_replicate"`

	WALLevel       string `json:"wal_level"`
	ArchiveMode    string `json:"archive_mode"`
	ArchiveCommand string `json:"archive_command_set"`
	MaxWALSenders  int    `json:"max_wal_senders"`
	WALSegmentSize int64  `json:"wal_segment_size_bytes"`

	ServerVersion string `json:"server_version"`
	VersionNum    int    `json:"server_version_num"`
	ClusterSize   int64  `json:"cluster_size_bytes"`

	// BootstrapRole is the superuser the cluster was created with, from OID
	// 10. See `BaseManifest.BootstrapRole`.
	BootstrapRole string `json:"bootstrap_role"`
}

// CheckBaseBackupReady refuses a base backup this server cannot take, before
// anything is spent on it.
//
// # Why this is its own check and not a nicer error from pg_basebackup
//
// Because pg_basebackup's error is `FATAL: no pg_hba.conf entry for
// replication connection from host "172.18.0.5"`, which is accurate and tells
// an operator nothing about the line to add or where. This is the same class of
// problem as the row-level-security refusal in `canReadEverything`: the fix is
// four words long and the diagnostic is twenty minutes.
func CheckBaseBackupReady(
	ctx context.Context, dsn string,
) (BaseBackupReadiness, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return BaseBackupReadiness{}, baseBackupConnectionError(err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	var out BaseBackupReadiness
	var segmentSize string
	if err := conn.QueryRow(ctx, `
		SELECT current_user,
		       current_database(),
		       (SELECT rolreplication OR rolsuper FROM pg_roles
		         WHERE rolname = current_user),
		       current_setting('wal_level'),
		       current_setting('archive_mode'),
		       CASE WHEN btrim(current_setting('archive_command')) = ''
		            THEN 'unset' ELSE 'set' END,
		       current_setting('max_wal_senders')::int,
		       current_setting('wal_segment_size'),
		       current_setting('server_version'),
		       current_setting('server_version_num')::int,
		       pg_database_size(current_database()),
		       coalesce((SELECT rolname FROM pg_roles WHERE oid = 10), '')`).
		Scan(&out.Role, &out.Database, &out.Replication, &out.WALLevel,
			&out.ArchiveMode, &out.ArchiveCommand, &out.MaxWALSenders,
			&segmentSize, &out.ServerVersion, &out.VersionNum,
			&out.ClusterSize, &out.BootstrapRole); err != nil {
		return BaseBackupReadiness{}, errs.Wrap(err, errs.CodeInternal,
			"The database would not say how it is configured.")
	}
	if n, err := parseByteSize(segmentSize); err == nil {
		out.WALSegmentSize = n
	} else {
		out.WALSegmentSize = DefaultWALSegmentSize
	}

	if !out.Replication {
		return out, errs.Newf(errs.CodeInvalidInput,
			"The role %q cannot take a physical base backup: it does not have "+
				"the REPLICATION attribute. pg_basebackup copies the cluster "+
				"over a replication connection, which is a different thing "+
				"from reading the tables. Run `rawsyst backup role` to grant "+
				"it, and add a `host replication %s all scram-sha-256` line to "+
				"pg_hba.conf — the ordinary `host all all` line does NOT cover "+
				"replication. deploy/server/PITR.md has both.",
			out.Role, out.Role)
	}
	if out.WALLevel == "minimal" {
		return out, errs.New(errs.CodeInvalidInput,
			"wal_level is `minimal`, which does not write enough information "+
				"to the write-ahead log for it to be replayed anywhere else. "+
				"Set it to `replica` and restart PostgreSQL. Until then there "+
				"is no point-in-time recovery to have.")
	}
	if out.MaxWALSenders < 2 {
		return out, errs.Newf(errs.CodeInvalidInput,
			"max_wal_senders is %d and a base backup that streams its own "+
				"write-ahead log needs two connections. Raise it to at least "+
				"two and restart PostgreSQL.", out.MaxWALSenders)
	}
	return out, nil
}

func baseBackupConnectionError(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "pg_hba.conf") {
		return errs.Wrap(err, errs.CodeInvalidInput,
			"PostgreSQL refused the connection because pg_hba.conf has no "+
				"entry for it. If this is the base backup, note that a "+
				"replication connection needs its OWN line: `host replication "+
				"<role> all scram-sha-256`. The ordinary `host all all` line "+
				"does not cover it. See deploy/server/PITR.md.")
	}
	return errs.Wrap(err, errs.CodeUnavailable,
		"The database could not be opened to take a base backup of it.")
}

// --- reading them back ------------------------------------------------------

// BaseBackup is one stored base backup, as a listing reports it.
type BaseBackup struct {
	ID        string        `json:"base_backup_id"`
	TakenAt   time.Time     `json:"taken_at"`
	Completed bool          `json:"completed"`
	Manifest  *BaseManifest `json:"manifest,omitempty"`
}

// ListBaseBackups reads what is in the store.
//
// Incomplete ones are listed and MARKED incomplete rather than hidden. A base
// backup whose upload died is an object that costs money and a state somebody
// has to be able to see to clean up; hiding it would leave an operator
// wondering why the bucket is larger than the list of backups.
func ListBaseBackups(
	ctx context.Context, opts WALOptions,
) ([]BaseBackup, error) {
	opts = opts.withDefaults()
	if !opts.Configured() {
		return nil, errs.New(errs.CodeUnavailable,
			"No object store is configured.")
	}
	prefix := BaseBackupPrefix(opts.Prefix)
	keys, err := opts.Store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	seen := map[string]*BaseBackup{}
	order := []string{}
	for _, key := range keys {
		rest := strings.TrimPrefix(key, prefix)
		id, name, ok := strings.Cut(rest, "/")
		if !ok || !ValidSnapshotID(id) {
			continue
		}
		entry := seen[id]
		if entry == nil {
			at, _ := ParseSnapshotID(id)
			entry = &BaseBackup{ID: id, TakenAt: at}
			seen[id] = entry
			order = append(order, id)
		}
		if name == completedMark {
			entry.Completed = true
		}
	}

	out := make([]BaseBackup, 0, len(order))
	for _, id := range order {
		out = append(out, *seen[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// ReadBaseManifest reads one base backup's manifest.
func ReadBaseManifest(
	ctx context.Context, opts WALOptions, id string,
) (BaseManifest, error) {
	opts = opts.withDefaults()
	key, err := BaseBackupKey(opts.Prefix, id, baseManifestObject)
	if err != nil {
		return BaseManifest{}, err
	}
	body, err := opts.Store.Get(ctx, key)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return BaseManifest{}, errs.Newf(errs.CodeNotFound,
				"There is no base backup called %q.", clipText(id, 64))
		}
		return BaseManifest{}, err
	}
	var m BaseManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return BaseManifest{}, errs.Wrap(err, errs.CodeInternal,
			"That base backup manifest could not be read.")
	}
	return m, nil
}

// CompletedBaseBackups is the listing with the manifests attached, newest
// first, and only the ones that finished.
//
// This is what a recovery window is computed from and what an operator is
// offered, so it deliberately answers a shorter question than `ListBaseBackups`:
// only backups that can actually be recovered from appear here.
func CompletedBaseBackups(
	ctx context.Context, opts WALOptions,
) ([]BaseManifest, error) {
	listed, err := ListBaseBackups(ctx, opts)
	if err != nil {
		return nil, err
	}
	out := make([]BaseManifest, 0, len(listed))
	for _, b := range listed {
		if !b.Completed {
			continue
		}
		m, err := ReadBaseManifest(ctx, opts, b.ID)
		if err != nil {
			// A marker with no manifest beside it is not a recovery source and
			// is not a reason to fail the whole listing: the other backups are
			// still there and are still what somebody is asking about.
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// DeleteBaseBackup removes one, marker first.
//
// The marker goes first on purpose: everything between the first delete and the
// last is a partially deleted backup, and a partially deleted backup that still
// claims to be complete is one a recovery could choose. Removing the claim
// before removing the bytes means the worst intermediate state is an incomplete
// backup nobody will pick, rather than a complete-looking one that is missing
// its data.
func DeleteBaseBackup(
	ctx context.Context, opts WALOptions, id string,
) (int, error) {
	opts = opts.withDefaults()
	if !ValidSnapshotID(id) {
		return 0, errs.New(errs.CodeInvalidInput, "That is not a base backup id.")
	}
	removed := 0
	for _, name := range []string{
		completedMark, baseManifestObject, basePGManifestObject,
		baseWALTarObject, baseTarObject,
	} {
		key, err := BaseBackupKey(opts.Prefix, id, name)
		if err != nil {
			continue
		}
		if err := opts.Store.Delete(ctx, key); err != nil {
			if errs.CodeOf(err) == errs.CodeNotFound {
				continue
			}
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// --- small file helpers -----------------------------------------------------

func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", errs.Wrap(err, errs.CodeInternal,
			"A staged file could not be read.")
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", errs.Wrap(err, errs.CodeInternal,
			"A staged file could not be read.")
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// sealFile encrypts one staged file into another, and reports what came out.
//
// The plaintext is removed once the ciphertext is written. It is the same bytes
// twice on a staging volume sized for one copy of a compressed cluster, and the
// second copy is only needed for as long as it takes to write.
func sealFile(key Key, src, dst string) (int64, string, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, "", errs.Wrap(err, errs.CodeInternal,
			"A staged file could not be read to encrypt it.")
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, "", errs.Wrap(err, errs.CodeInternal,
			"A file for the encrypted copy could not be created.")
	}
	defer out.Close()

	h := sha256.New()
	n, err := key.Seal(io.MultiWriter(out, h), in)
	if err != nil {
		return 0, "", err
	}
	if err := out.Sync(); err != nil {
		return 0, "", errs.Wrap(err, errs.CodeInternal,
			"The encrypted copy could not be flushed to disk.")
	}
	in.Close()
	_ = os.Remove(src)
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func stagingRoot(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return os.TempDir()
	}
	return dir
}

// limitedWriter keeps a subprocess's diagnostics bounded.
//
// `pg_basebackup --verbose` prints a line per progress step, and a failure
// after forty minutes would otherwise put forty minutes of progress into an
// error message that is stored in a database column and shown on a screen.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	l.n -= len(p)
	written, err := l.w.Write(p)
	if err != nil {
		return written, err
	}
	return len(p), nil
}

// firstUsefulLine picks the sentence worth showing out of a tool's output.
//
// PostgreSQL client tools print progress to stderr and then the reason they
// failed, so the LAST error line is the one that matters and the preceding
// forty are noise.
func firstUsefulLine(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") ||
			strings.Contains(lower, "could not") {
			return clipText(line, 400)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return clipText(line, 400)
		}
	}
	return "It printed nothing to say why."
}
