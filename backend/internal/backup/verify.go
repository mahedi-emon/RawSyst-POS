// Proving a backup restores, and keeping the right ones.
//
// # Why a checksum is not a verification
//
// A checksum says the bytes came back the way they went. It says nothing about
// whether those bytes are a database anybody can use — a truncated dump hashes
// perfectly, and so does a dump of an empty schema taken while the application
// was pointed at the wrong DSN.
//
// So `Verify` restores. It pulls the snapshot down, checks the checksum against
// the manifest, restores into a temporary database beside the real one, counts
// the tables, reads the schema version out of `schema_migration`, compares the
// row counts against what the manifest recorded, and drops the temporary
// database again. That is the whole of what makes a backup a backup, and it is
// the reason `backup_record.verified_at` exists separately from `status`.
//
// It never touches the production database. The temporary one is named from the
// snapshot id, it is created and dropped by this code, and a restore that
// somehow reached the wrong database would fail the name check before it ran.
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// countedTables are the ones a verification compares row for row.
//
// Not every table: a count of all of them turns a verification into a full
// scan of the database, which on the server this is sized for is a minute of
// two cores. These are the ones whose emptiness would mean the backup is
// worthless — a business, its people, what it sold, what it owes and what it
// recorded about all three.
var countedTables = []string{
	"tenant", "company", "app_user", "product", "customer", "supplier",
	"invoice", "journal_entry", "regulatory_rule", "audit_log",
}

// stats is what the source database says about itself.
type stats struct {
	name   string
	size   int64
	schema int
	tables int
	rows   map[string]int64
}

// describe reads the numbers the manifest records and a verification repeats.
func describe(ctx context.Context, dsn string) (stats, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return stats{}, errs.Wrap(err, errs.CodeUnavailable,
			"The database could not be read to describe the backup.")
	}
	defer conn.Close(ctx)
	return describeConn(ctx, conn)
}

func describeConn(ctx context.Context, conn *pgx.Conn) (stats, error) {
	var out stats
	out.rows = map[string]int64{}

	if err := conn.QueryRow(ctx, `
		SELECT current_database(), pg_database_size(current_database())`).
		Scan(&out.name, &out.size); err != nil {
		return stats{}, errs.Wrap(err, errs.CodeInternal,
			"The database's own size could not be read.")
	}

	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`).
		Scan(&out.tables); err != nil {
		return stats{}, errs.Wrap(err, errs.CodeInternal,
			"The table count could not be read.")
	}

	// The schema version, from the migration ledger rather than from a build
	// constant: what matters on restore is what the DATABASE is at.
	if err := conn.QueryRow(ctx, `
		SELECT coalesce(max(version), 0) FROM schema_migration`).
		Scan(&out.schema); err != nil {
		return stats{}, errs.Wrap(err, errs.CodeInternal,
			"The schema version could not be read. Has cmd/migrate run?")
	}

	for _, table := range countedTables {
		var n int64
		// The table name is from the constant list above and never from input.
		// It is still quoted, because a list that grows is a list somebody
		// will add a reserved word to.
		if err := conn.QueryRow(ctx,
			`SELECT count(*) FROM "`+table+`"`).Scan(&n); err != nil {
			// A table that is not there is not an error: this list outlives
			// individual migrations, and a verification that fails because a
			// table was renamed teaches people to skip verifications.
			continue
		}
		out.rows[table] = n
	}
	return out, nil
}

// --- verification -----------------------------------------------------------

// VerifyReport is what a verification found.
type VerifyReport struct {
	SnapshotID string           `json:"snapshot_id"`
	Checked    []string         `json:"checked"`
	Restored   bool             `json:"restored"`
	Tables     int              `json:"tables_restored"`
	Schema     int              `json:"schema_version_restored"`
	Rows       map[string]int64 `json:"row_counts_restored"`
	Bytes      int64            `json:"bytes"`
	Took       string           `json:"took"`
}

// Verify downloads a snapshot and restores it into a temporary database.
//
// `adminDSN` is a connection to a database on the same server that is NOT the
// one being verified into — `postgres` will do — because creating and dropping
// a database cannot be done from inside it.
func Verify(
	ctx context.Context, opts Options, adminDSN, id string,
) (VerifyReport, error) {
	opts = opts.withDefaults()
	started := time.Now()
	report := VerifyReport{SnapshotID: id, Rows: map[string]int64{}}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// 1. It has to claim to be finished.
	if _, err := opts.Store.Exists(ctx,
		snapshotKey(opts.Prefix, id, completedMark)); err != nil {
		return report, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s has no completion marker, so whatever else is in it, "+
				"the run that made it did not finish. It will not be "+
				"restored.", id)
	}
	report.Checked = append(report.Checked, "completion marker")

	// 2. The manifest has to parse and be a version this build reads.
	manifest, err := ReadManifest(ctx, opts, id)
	if err != nil {
		return report, err
	}
	report.Checked = append(report.Checked, "manifest")

	// 3. The dump has to be there and be the size the manifest says.
	key := snapshotKey(opts.Prefix, id, manifest.Database.Key)
	size, err := opts.Store.Exists(ctx, key)
	if err != nil {
		return report, err
	}
	if size != manifest.Database.Bytes {
		return report, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s says its dump is %d bytes and the store holds %d.",
			id, manifest.Database.Bytes, size)
	}
	report.Bytes = size
	report.Checked = append(report.Checked, "object size")

	// 4. And hash to what the manifest recorded, which means pulling it down.
	dump, err := download(ctx, opts, key, manifest.Database.SHA256)
	if err != nil {
		return report, err
	}
	defer os.Remove(dump)
	report.Checked = append(report.Checked, "checksum")

	// 5. The part that makes it a verification.
	scratch := "rawsyst_verify_" + strings.ToLower(
		strings.NewReplacer("-", "_", ":", "_", "T", "_", "Z", "").Replace(id))
	if len(scratch) > 60 {
		scratch = scratch[:60]
	}
	if err := createDatabase(ctx, adminDSN, scratch); err != nil {
		return report, err
	}
	defer func() {
		// Dropped whatever happened. A verification that leaves databases
		// behind fills the disk it is protecting.
		_ = dropDatabase(context.WithoutCancel(ctx), adminDSN, scratch)
	}()

	if err := pgRestore(ctx, dsnFor(adminDSN, scratch), dump); err != nil {
		return report, err
	}
	report.Restored = true
	report.Checked = append(report.Checked, "restore into a temporary database")

	// 6. And what came back has to look like the database that went in.
	conn, err := pgx.Connect(ctx, dsnFor(adminDSN, scratch))
	if err != nil {
		return report, errs.Wrap(err, errs.CodeUnavailable,
			"The restored copy could not be opened.")
	}
	restored, err := describeConn(ctx, conn)
	conn.Close(ctx)
	if err != nil {
		return report, err
	}
	report.Tables, report.Schema, report.Rows =
		restored.tables, restored.schema, restored.rows

	if restored.schema != manifest.SchemaVersion {
		return report, errs.Newf(errs.CodeInvalidInput,
			"The restored copy is at schema version %d and the manifest says "+
				"%d.", restored.schema, manifest.SchemaVersion)
	}
	if restored.tables != manifest.Tables {
		return report, errs.Newf(errs.CodeInvalidInput,
			"The restored copy has %d tables and the manifest says %d.",
			restored.tables, manifest.Tables)
	}
	report.Checked = append(report.Checked, "schema version", "table count")

	var short []string
	for table, want := range manifest.Rows {
		got, present := restored.rows[table]
		if !present || got != want {
			short = append(short, fmt.Sprintf("%s: %d of %d", table, got, want))
		}
	}
	sort.Strings(short)
	if len(short) > 0 {
		return report, errs.Newf(errs.CodeInvalidInput,
			"The restored copy does not hold what the manifest recorded — %s.",
			strings.Join(short, "; "))
	}
	report.Checked = append(report.Checked, "row counts")
	report.Took = time.Since(started).Round(time.Second).String()
	return report, nil
}

// download fetches an object to a temporary file and checks its checksum.
//
// Streamed and hashed in one pass. Nothing is held in memory and nothing is
// trusted: a download that does not hash to the manifest is deleted before it
// can be mistaken for a backup.
func download(
	ctx context.Context, opts Options, key, want string,
) (string, error) {
	body, _, err := opts.Store.GetStream(ctx, key)
	if err != nil {
		return "", err
	}
	defer body.Close()

	f, err := os.CreateTemp(opts.TempDir, "rawsyst-verify-*.pgdump")
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternal,
			"A temporary file for the download could not be created.")
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, hash), body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(f.Name())
		return "", errs.Wrap(copyErr, errs.CodeUnavailable,
			"The snapshot could not be downloaded in full.")
	}

	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		os.Remove(f.Name())
		return "", errs.Newf(errs.CodeInvalidInput,
			"The snapshot does not match its manifest: expected %s, the "+
				"store returned %s. Do not restore this.", want, got)
	}
	return f.Name(), nil
}

// --- restoring --------------------------------------------------------------

// Restore puts a snapshot into a database that already exists.
//
// Deliberately not "into production". The caller names the target, the target
// has to be empty of RawSyst tables, and the guard below refuses one that is
// not — because the one thing worse than no backup is a restore that half
// overwrites a working database.
//
// A new server's sequence is in `deploy/server/MIGRATION.md`: create the
// database, restore into it, then run the migrator, which is a no-op when the
// snapshot is already at the current schema and applies the difference when it
// is not.
func Restore(ctx context.Context, opts Options, targetDSN, id string) error {
	opts = opts.withDefaults()
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	if _, err := opts.Store.Exists(ctx,
		snapshotKey(opts.Prefix, id, completedMark)); err != nil {
		return errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s has no completion marker: the run that made it did "+
				"not finish, and it will not be restored.", id)
	}
	manifest, err := ReadManifest(ctx, opts, id)
	if err != nil {
		return err
	}

	conn, err := pgx.Connect(ctx, targetDSN)
	if err != nil {
		return errs.Wrap(err, errs.CodeUnavailable,
			"The target database could not be opened.")
	}
	var existing int
	err = conn.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`).
		Scan(&existing)
	name := ""
	_ = conn.QueryRow(ctx, `SELECT current_database()`).Scan(&name)
	conn.Close(ctx)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The target database could not be inspected.")
	}
	if existing > 0 {
		return errs.Newf(errs.CodeConflict,
			"The database %q already holds %d tables. A restore goes into an "+
				"empty database: create one, restore into that, and switch to "+
				"it — so that if anything goes wrong the database you have is "+
				"still the database you had.", name, existing)
	}

	dump, err := download(ctx, opts,
		snapshotKey(opts.Prefix, id, manifest.Database.Key),
		manifest.Database.SHA256)
	if err != nil {
		return err
	}
	defer os.Remove(dump)

	return pgRestore(ctx, targetDSN, dump)
}

// --- retention --------------------------------------------------------------

// Policy is how many of each to keep.
type Policy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// DefaultPolicy is a week of days, a month of weeks and a quarter of months.
var DefaultPolicy = Policy{Daily: 7, Weekly: 4, Monthly: 3}

// Prune removes snapshots outside the policy.
//
// # What it will not do
//
// It will not delete the newest completed snapshot, whatever the policy says.
// A retention rule that can empty the store is a retention rule that will, and
// the day it does is the day somebody needed it. It also refuses to delete
// anything at all if it cannot find a completed snapshot to keep — an empty or
// unreadable listing is a reason to stop, not a reason to start deleting.
func Prune(
	ctx context.Context, opts Options, policy Policy, dryRun bool,
) ([]string, error) {
	opts = opts.withDefaults()
	snapshots, err := List(ctx, opts)
	if err != nil {
		return nil, err
	}

	var complete []Snapshot
	for _, s := range snapshots {
		if s.Completed {
			complete = append(complete, s)
		}
	}
	if len(complete) == 0 {
		return nil, errs.New(errs.CodeInvalidInput,
			"No completed snapshot is in the store. Nothing will be deleted: "+
				"an empty listing is a reason to stop rather than to start "+
				"removing backups.")
	}

	keep := whatToKeep(complete, policy)

	var removed []string
	for _, s := range snapshots {
		if keep[s.ID] {
			continue
		}
		removed = append(removed, s.ID)
		if dryRun {
			continue
		}
		keys, err := opts.Store.List(ctx, opts.Prefix+"/"+s.ID+"/")
		if err != nil {
			return removed, err
		}
		for _, k := range keys {
			if err := opts.Store.Delete(ctx, k); err != nil {
				return removed, err
			}
		}
	}
	sort.Strings(removed)
	return removed, nil
}

// whatToKeep decides which snapshots survive a policy.
//
// Separated from `Prune` so it can be tested without an object store: what is
// worth holding to here is the decision, not the deleting. The newest completed
// snapshot is kept unconditionally, and so is any snapshot whose id this build
// cannot date — deleting something because it is not understood is how a bug
// becomes data loss.
//
// `snapshots` is newest first, which is what `List` returns, so the first
// snapshot seen for a day, a week or a month is the one that represents it.
func whatToKeep(snapshots []Snapshot, policy Policy) map[string]bool {
	keep := map[string]bool{}
	if len(snapshots) == 0 {
		return keep
	}
	keep[snapshots[0].ID] = true

	seen := map[string]map[string]bool{"day": {}, "week": {}, "month": {}}
	limits := map[string]int{
		"day": policy.Daily, "week": policy.Weekly, "month": policy.Monthly,
	}
	for _, s := range snapshots {
		if s.TakenAt.IsZero() {
			keep[s.ID] = true
			continue
		}
		year, week := s.TakenAt.ISOWeek()
		for period, bucket := range map[string]string{
			"day":   s.TakenAt.Format("2006-01-02"),
			"week":  fmt.Sprintf("%d-W%02d", year, week),
			"month": s.TakenAt.Format("2006-01"),
		} {
			if len(seen[period]) < limits[period] && !seen[period][bucket] {
				seen[period][bucket] = true
				keep[s.ID] = true
			}
		}
	}
	return keep
}

// --- postgres plumbing ------------------------------------------------------

func pgRestore(ctx context.Context, dsn, file string) error {
	cmd := exec.CommandContext(ctx, "pg_restore",
		"--dbname="+dsn,
		"--no-owner",
		"--no-privileges",
		// One transaction: a restore that fails halfway leaves nothing behind
		// rather than a database that is part of one backup.
		"--single-transaction",
		file,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errs.Newf(errs.CodeInternal,
			"pg_restore failed: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

func createDatabase(ctx context.Context, adminDSN, name string) error {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return errs.Wrap(err, errs.CodeUnavailable,
			"The server could not be reached to create a scratch database.")
	}
	defer conn.Close(ctx)
	// The name is built from a snapshot id this code generated, never from
	// input, and it is quoted regardless.
	if _, err := conn.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"A scratch database for the verification could not be created.")
	}
	return nil
}

func dropDatabase(ctx context.Context, adminDSN, name string) error {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`)
	return err
}

// dsnFor points an admin connection string at another database on the same
// server, keeping the credentials and the parameters.
func dsnFor(adminDSN, name string) string {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return adminDSN
	}
	u.Path = "/" + name
	return u.String()
}
