// Putting a backup back, without betting the business on it working.
//
// # Two operations, and only one of them is dangerous
//
// A RESTORE VALIDATION restores a snapshot into a temporary database, checks
// everything about it against the manifest, and drops the temporary database.
// It cannot touch production and it is what should be run first, every time.
//
// A PRODUCTION RESTORE replaces the live database. It is the dangerous one and
// it is written to be as close to reversible as a database operation gets.
//
// # Why the production restore is a rename and not a drop
//
// The obvious implementation is `DROP DATABASE rawsyst; CREATE DATABASE
// biz1core; pg_restore`. It is also the implementation where a failure halfway
// through leaves a business with neither the database it had nor the one it
// asked for. There is no undo for a dropped database.
//
// So nothing is dropped. The snapshot is restored into a NEW database beside
// the live one and checked there. Only then, and only after the current
// production database has a fresh backup that has itself been verified, is
// there a cutover: connections are refused, the live database is RENAMED out of
// the way, and the restored one is renamed into its place. The old database is
// still on the server under `rawsyst_pre_restore_<timestamp>`, and rolling back
// is the same two renames in the other order.
//
// The cutover window is the two renames. It is seconds, and the write freeze is
// on for the whole of it, so nothing is accepted that would then be lost.
//
// # What this refuses to do
//
//   - restore a snapshot with no completion marker;
//   - restore a snapshot that has not passed a restore validation;
//   - replace production without a fresh, VERIFIED backup of what production
//     currently holds;
//   - run at all unless the deployment has explicitly enabled it;
//   - drop anything, ever.
package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// ValidateReport is what a restore rehearsal found.
//
// The same document as a verification, because it is the same question asked at
// a different moment: does this snapshot come back as the database it says it
// is. Kept as its own type so the two are not confused in a record — a
// verification is what the nightly run does, a validation is what somebody
// pressed before a restore.
type ValidateReport struct {
	VerifyReport
	// Target is the temporary database it was rehearsed in, so an operator
	// reading a report knows nothing live was involved.
	Target string `json:"rehearsed_in"`
}

// ValidateRestore rehearses a restore into a temporary database.
//
// Identical in effect to `Verify` and named separately because the two are
// different acts to an operator: one is the nightly proof, the other is the
// thing somebody does immediately before deciding to replace production. The
// distinction is recorded because "when was this last proved" and "was this
// proved before we restored it" are questions with different answers.
func ValidateRestore(
	ctx context.Context, opts Options, adminDSN, id string,
) (ValidateReport, error) {
	report, err := Verify(ctx, opts, adminDSN, id)
	return ValidateReport{VerifyReport: report, Target: ScratchName(id)}, err
}

// ProductionOptions are the extra things a production restore needs.
type ProductionOptions struct {
	// AdminDSN connects to a database that is NOT the live one, as a role that
	// may rename databases and refuse connections to them. In practice a
	// superuser: `ALTER DATABASE … WITH ALLOW_CONNECTIONS false` is not
	// something an ordinary role may do, and doing it is what makes the rename
	// possible at all.
	AdminDSN string

	// LiveDatabase is the name production is served from — the database in
	// RAWSYST_DB_DSN. Named explicitly rather than parsed out of a DSN inside
	// this function, because the one thing that must not be guessed here is
	// which database gets renamed.
	LiveDatabase string

	// Enabled is the deployment's own switch. A production restore is the most
	// destructive thing this product can do and a deployment that does not want
	// the capability at all should be able to say so, in the environment, where
	// nobody with a browser can change it.
	Enabled bool

	// Confirm is what the operator typed. It has to be the snapshot id. A
	// confirmation that is a checkbox is a confirmation people click; one that
	// is the id of the thing about to replace their database is one they have
	// to look at.
	Confirm string

	// SafetyBackup is the id of the verified backup of what production holds
	// RIGHT NOW. Required. Taking it is the caller's job because taking a
	// backup is a whole operation with its own record and its own failures, and
	// this function must not be the place where two of those are nested.
	SafetyBackup string

	// StagedDir, when set, is the directory an UPLOADED artifact lives in, and
	// the snapshot is read from there instead of from the object store.
	//
	// This is the case that matters most and it is the one that is easy to
	// leave out: the new server has no object store yet, or the bucket the old
	// server used is gone with the account. What it has is three files
	// somebody carried, and this is how they become the live database.
	StagedDir string

	// Freeze puts the product into maintenance mode for the cutover and takes
	// it out again. Optional only in tests: without it, writes accepted during
	// the cutover would land in a database that is about to be renamed out of
	// the way, and be lost.
	Freeze   func(ctx context.Context, reason string) error
	Unfreeze func(ctx context.Context) error
}

// ProductionReport is what a production restore did.
type ProductionReport struct {
	SnapshotID   string `json:"snapshot_id"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	Took         string `json:"took"`
	SafetyBackup string `json:"safety_backup_snapshot_id"`

	// RestoredInto is the database the snapshot was restored into before the
	// cutover, and which is now serving.
	RestoredInto string `json:"restored_into"`

	// PreviousDatabase is where the database that WAS production now lives. It
	// is not dropped, and rolling back is renaming it back. This is the single
	// most important line in the report.
	PreviousDatabase string `json:"previous_database"`

	Validation *VerifyReport `json:"validation,omitempty"`

	CutoverAt string `json:"cutover_at,omitempty"`
	Completed bool   `json:"completed"`
}

// RestoreToProduction replaces the live database with a snapshot.
//
// Read the package note before changing anything in here.
func RestoreToProduction(
	ctx context.Context, opts Options, prod ProductionOptions, id string,
) (ProductionReport, error) {
	opts = opts.withDefaults()
	started := time.Now()
	report := ProductionReport{
		SnapshotID:   id,
		SafetyBackup: prod.SafetyBackup,
		StartedAt:    started.UTC().Format(time.RFC3339),
	}

	if err := checkProductionPreconditions(prod, id); err != nil {
		return report, err
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// The snapshot has to be a snapshot: marker, manifest, size, checksum.
	opts.Progress(StageChecking)
	manifest, dump, cleanup, err := obtain(ctx, opts, prod.StagedDir, id)
	if err != nil {
		return report, err
	}
	defer cleanup()

	// Restored beside production, into a database created for it.
	target := restoreTargetName(started)
	report.RestoredInto = target

	opts.Progress(StageRestoring)
	if err := createDatabase(
		ctx, prod.AdminDSN, target, userOf(opts.AppDSN)); err != nil {
		return report, err
	}
	// From here on a failure leaves `target` behind. It is deliberately NOT
	// dropped: it is either the restore that half-worked, which somebody will
	// want to look at, or it is a working copy of the business's data that
	// happens not to have been switched to. Deleting either would be this code
	// choosing tidiness over the data.

	targetDSN := restoreDSN(opts, prod.AdminDSN, target)
	if err := pgRestore(ctx, targetDSN, dump); err != nil {
		return report, err
	}

	// The backup role's access, put back before the cutover rather than after.
	// A restore that succeeded and left the next backup unable to read a table
	// is a business that recovered into being unprotected.
	if err := grantBackupRole(ctx, targetDSN, userOf(opts.DSN)); err != nil {
		return report, err
	}

	// Checked in place, before anything is switched. This is the same
	// comparison a verification makes, against the database that is about to
	// become production.
	opts.Progress(StageChecking)
	conn, err := pgx.Connect(ctx, dsnFor(prod.AdminDSN, target))
	if err != nil {
		return report, errs.Wrap(err, errs.CodeUnavailable,
			"The restored database could not be opened to check it.")
	}
	found, err := takeInventory(ctx, conn)
	conn.Close(context.WithoutCancel(ctx))
	if err != nil {
		return report, err
	}

	validation := VerifyReport{
		SnapshotID: id,
		StartedAt:  started.UTC().Format(time.RFC3339),
		Restored:   true,
		Complete:   manifest.Complete(),
		Expected:   manifest.Inventory,
		Found:      &found,
		Tables:     found.TableCount(),
		Schema:     found.SchemaVersion,
		Tenants:    len(found.TenantRows),
		Companies:  len(found.CompanyRows),
	}
	for _, n := range found.Rows {
		validation.Rows += n
	}
	compare(manifest, found, &validation)
	validation.Passed = len(validation.Findings) == 0
	report.Validation = &validation
	if !validation.Passed {
		return report, errs.Newf(errs.CodeInvalidInput,
			"The snapshot restored into %s but it is not what the manifest "+
				"describes, so production has NOT been touched: %s. The "+
				"restored copy is still on the server under that name if you "+
				"want to look at it.",
			target, strings.Join(validation.Findings, "; "))
	}

	// The cutover. Everything above this line is reversible by doing nothing.
	if prod.Freeze != nil {
		if err := prod.Freeze(ctx,
			"Restoring the database from backup "+id); err != nil {
			return report, err
		}
		defer func() {
			if prod.Unfreeze != nil {
				_ = prod.Unfreeze(context.WithoutCancel(ctx))
			}
		}()
	}

	previous, err := cutover(ctx, prod.AdminDSN, prod.LiveDatabase, target)
	report.PreviousDatabase = previous
	if err != nil {
		return report, err
	}

	report.CutoverAt = time.Now().UTC().Format(time.RFC3339)
	report.FinishedAt = report.CutoverAt
	report.Took = time.Since(started).Round(time.Second).String()
	report.Completed = true
	opts.Progress(StageDone)
	return report, nil
}

func checkProductionPreconditions(prod ProductionOptions, id string) error {
	if !ValidSnapshotID(id) {
		return errs.New(errs.CodeInvalidInput, "That is not a snapshot id.")
	}
	if !prod.Enabled {
		return errs.New(errs.CodeInvalidInput,
			"Replacing the live database from a backup is switched off in this "+
				"deployment. It is off by default. Set "+
				"RAWSYST_ALLOW_PRODUCTION_RESTORE=true on the backup agent, "+
				"and read deploy/server/RECOVERY.md before you do.")
	}
	if strings.TrimSpace(prod.Confirm) != id {
		return errs.Newf(errs.CodeInvalidInput,
			"To replace the live database with snapshot %s, the confirmation "+
				"has to be that snapshot's id typed out. This is the last "+
				"thing standing between a mis-click and every business on "+
				"this server being served yesterday's data.", id)
	}
	if strings.TrimSpace(prod.SafetyBackup) == "" {
		return errs.New(errs.CodeInvalidInput,
			"There is no verified backup of what the live database holds right "+
				"now, so a restore would replace data that exists nowhere "+
				"else. Take one and verify it first — that is what makes this "+
				"reversible.")
	}
	if strings.TrimSpace(prod.LiveDatabase) == "" {
		return errs.New(errs.CodeInvalidInput,
			"The live database was not named, and this will not guess which "+
				"database to rename.")
	}
	if strings.TrimSpace(prod.AdminDSN) == "" {
		return errs.New(errs.CodeInvalidInput,
			"No administrative connection is configured, so the cutover cannot "+
				"be performed. Set RAWSYST_BACKUP_ADMIN_DSN.")
	}
	if strings.HasPrefix(prod.LiveDatabase, "rawsyst_verify_") ||
		strings.HasPrefix(prod.LiveDatabase, "rawsyst_restore_") ||
		strings.HasPrefix(prod.LiveDatabase, "rawsyst_pre_restore_") {
		return errs.Newf(errs.CodeInvalidInput,
			"%q is a name this product gives its own temporary databases and "+
				"will not be treated as the live one.", prod.LiveDatabase)
	}
	return nil
}

func restoreTargetName(at time.Time) string {
	return "rawsyst_restore_" + at.UTC().Format("20060102t150405")
}

// cutover swaps two databases by name, and never drops either.
//
// The order is the whole of it:
//
//  1. refuse new connections to the live database, so the application cannot
//     reconnect between the terminate and the rename and hold the rename off
//     for ever;
//  2. terminate what is connected;
//  3. rename the live database out of the way;
//  4. rename the restored one into its place;
//  5. allow connections to the old one again, so it can be read, checked and
//     rolled back to.
//
// If step 4 fails, step 3 is undone before returning. That is the only window
// where the server has no database under the production name, and it is one
// statement wide.
func cutover(
	ctx context.Context, adminDSN, live, restored string,
) (string, error) {
	previous := "rawsyst_pre_restore_" + time.Now().UTC().Format("20060102t150405")

	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeUnavailable,
			"The server could not be reached to perform the cutover.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	// Refuse to run from inside the database about to be renamed: it would be
	// this connection that blocks the rename.
	var here string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&here); err != nil {
		return "", errs.Wrap(err, errs.CodeInternal,
			"The administrative connection would not say what it is connected to.")
	}
	if here == live || here == restored {
		return "", errs.Newf(errs.CodeInvalidInput,
			"The administrative connection is to %q, which is one of the two "+
				"databases being renamed. Point RAWSYST_BACKUP_ADMIN_DSN at "+
				"another database on the same server — `postgres` will do.", here)
	}

	if _, err := conn.Exec(ctx,
		`ALTER DATABASE "`+live+`" WITH ALLOW_CONNECTIONS false`); err != nil {
		return "", errs.Wrap(err, errs.CodeInternal,
			"Connections to the live database could not be held off, so the "+
				"cutover did not start. Nothing has changed. The role in "+
				"RAWSYST_BACKUP_ADMIN_DSN needs to be a superuser for this.")
	}
	// Whatever happens next, connections are allowed again: a failure that
	// left the live database refusing connections would be an outage caused by
	// the recovery.
	allowAgain := func(name string) {
		_, _ = conn.Exec(context.WithoutCancel(ctx),
			`ALTER DATABASE "`+name+`" WITH ALLOW_CONNECTIONS true`)
	}

	if _, err := conn.Exec(ctx, `
		SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()`, live); err != nil {
		allowAgain(live)
		return "", errs.Wrap(err, errs.CodeInternal,
			"The connections to the live database could not be closed, so the "+
				"cutover did not start. Nothing has changed.")
	}

	if _, err := conn.Exec(ctx,
		`ALTER DATABASE "`+live+`" RENAME TO "`+previous+`"`); err != nil {
		allowAgain(live)
		return "", errs.Wrap(err, errs.CodeInternal,
			"The live database could not be renamed, so the cutover did not "+
				"happen. Nothing has changed and the application is still "+
				"pointed at the database it was.")
	}

	if _, err := conn.Exec(ctx,
		`ALTER DATABASE "`+restored+`" RENAME TO "`+live+`"`); err != nil {
		// Put it back. This is the one window where the production name names
		// nothing, and it closes here.
		if _, undo := conn.Exec(context.WithoutCancel(ctx),
			`ALTER DATABASE "`+previous+`" RENAME TO "`+live+`"`); undo != nil {
			return previous, errs.Newf(errs.CodeInternal,
				"The restored database could not be renamed into place AND the "+
					"live database could not be renamed back. The data is "+
					"safe and is in %q; rename it to %q by hand and start the "+
					"application. Original failure: %v", previous, live, err)
		}
		allowAgain(live)
		return "", errs.Wrap(err, errs.CodeInternal,
			"The restored database could not be renamed into place. The live "+
				"database has been renamed back and nothing has changed.")
	}

	// The old one, readable again so it can be checked and rolled back to.
	allowAgain(previous)
	return previous, nil
}

// Rollback puts back the database a production restore renamed out of the way.
//
// The same two renames in the other order. Exposed as its own operation
// because the moment somebody needs it is the moment nobody wants to be
// composing SQL, and because a documented rollback that has never been run is
// a rollback nobody trusts.
func Rollback(ctx context.Context, adminDSN, live, previous string) error {
	if !strings.HasPrefix(previous, "rawsyst_pre_restore_") {
		return errs.Newf(errs.CodeInvalidInput,
			"%q is not a database this product renamed out of the way, and "+
				"this will not rename something it does not recognise into "+
				"the live position.", previous)
	}
	aside := "rawsyst_rolledback_" + time.Now().UTC().Format("20060102t150405")
	_, err := cutoverNamed(ctx, adminDSN, live, previous, aside)
	return err
}

// cutoverNamed is `cutover` with the name for the displaced database given
// rather than generated, which is what a rollback needs.
func cutoverNamed(
	ctx context.Context, adminDSN, live, incoming, aside string,
) (string, error) {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return "", errs.Wrap(err, errs.CodeUnavailable,
			"The server could not be reached.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	// Connections are refused BEFORE they are terminated, and in that order:
	// terminating first leaves a window in which the application reconnects
	// and holds the rename off for ever.
	if _, err := conn.Exec(ctx,
		fmt.Sprintf(`ALTER DATABASE %q WITH ALLOW_CONNECTIONS false`, live)); err != nil {
		return "", errs.Wrap(err, errs.CodeInternal,
			"Connections to the live database could not be held off. Nothing "+
				"has changed.")
	}
	if _, err := conn.Exec(ctx, `
		SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()`, live); err != nil {
		_, _ = conn.Exec(context.WithoutCancel(ctx),
			fmt.Sprintf(`ALTER DATABASE %q WITH ALLOW_CONNECTIONS true`, live))
		return "", errs.Wrap(err, errs.CodeInternal,
			"The connections to the live database could not be closed. "+
				"Nothing has changed.")
	}
	for _, s := range []string{
		fmt.Sprintf(`ALTER DATABASE %q RENAME TO %q`, live, aside),
		fmt.Sprintf(`ALTER DATABASE %q RENAME TO %q`, incoming, live),
		fmt.Sprintf(`ALTER DATABASE %q WITH ALLOW_CONNECTIONS true`, aside),
	} {
		if _, err := conn.Exec(ctx, s); err != nil {
			return aside, errs.Wrap(err, errs.CodeInternal,
				"The rollback did not complete. List the databases on the "+
					"server before doing anything else: the data is in one of "+
					"them and nothing has been dropped.")
		}
	}
	return aside, nil
}

// obtain gets a snapshot's manifest and a plaintext dump on disk, from
// wherever it actually is.
//
// The object store when the backup was taken here; a staging directory when it
// was carried in from somewhere else. Both paths do the same checks — marker
// or files present, manifest parses and names this snapshot, size, checksum,
// and decryption where it is sealed — because "where the bytes came from" must
// not change how carefully they are examined.
//
// The returned cleanup removes anything temporary. It never removes a staged
// artifact: that one is the operator's copy and deleting it would be this code
// disposing of the only remaining copy of a business.
func obtain(
	ctx context.Context, opts Options, stagedDir, id string,
) (Manifest, string, func(), error) {
	noop := func() {}

	if stagedDir != "" {
		names := NamesFor(id)
		dumpPath := filepath.Join(stagedDir, names.Dump)
		manifestPath := filepath.Join(stagedDir, names.Manifest)

		check, err := CheckLocal(dumpPath, manifestPath, "")
		if err != nil {
			return Manifest{}, "", noop, err
		}
		if !check.Passed {
			return Manifest{}, "", noop, errs.Newf(errs.CodeInvalidInput,
				"The staged artifact for %s did not check out and will not be "+
					"restored: %s", id, strings.Join(check.Findings, "; "))
		}
		body, err := os.ReadFile(manifestPath)
		if err != nil {
			return Manifest{}, "", noop, errs.Wrap(err, errs.CodeInvalidInput,
				"The staged manifest could not be read.")
		}
		manifest, err := ParseManifest(body, "")
		if err != nil {
			return Manifest{}, "", noop, err
		}
		if manifest.Encryption == nil {
			return manifest, dumpPath, noop, nil
		}
		plain, _, err := Unseal(opts, dumpPath, manifest)
		if err != nil {
			return Manifest{}, "", noop, err
		}
		return manifest, plain, func() { os.Remove(plain) }, nil
	}

	if _, err := opts.Store.Exists(ctx,
		snapshotKey(opts.Prefix, id, completedMark)); err != nil {
		return Manifest{}, "", noop, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s has no completion marker: the run that made it did "+
				"not finish, and it will not be restored.", id)
	}
	manifest, err := ReadManifest(ctx, opts, id)
	if err != nil {
		return Manifest{}, "", noop, err
	}
	dump, _, err := fetch(ctx, opts,
		snapshotKey(opts.Prefix, id, manifest.Database.Key), manifest)
	if err != nil {
		return Manifest{}, "", noop, err
	}
	return manifest, dump, func() { os.Remove(dump) }, nil
}

// LooksEmpty reports whether a database holds no Biz1core tables at all.
//
// Used to decide whether a production restore needs a safety backup of what is
// there now. On a server that has been trading, it always does. On a server
// built an hour ago to recover on to, there is nothing to protect, and
// insisting on a backup of an empty database would block the recovery it exists
// to make possible — while also requiring an object store the new machine may
// not have yet.
//
// The check is deliberately "no tables", not "no rows". A database with tables
// and no rows is a migrated-but-unused installation, and it is also exactly
// what a half-finished restore looks like; treating that as empty would let a
// second restore proceed without a safety copy of the first one's wreckage.
func LooksEmpty(ctx context.Context, dsn string) (bool, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return false, errs.Wrap(err, errs.CodeUnavailable,
			"The live database could not be opened to see what is in it.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	var tables int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`).
		Scan(&tables); err != nil {
		return false, errs.Wrap(err, errs.CodeInternal,
			"The live database could not be inspected.")
	}
	return tables == 0, nil
}

// LiveDSN is the connection string for the live database, built from the
// administrative one so it carries the same credentials.
func LiveDSN(adminDSN, live string) string { return dsnFor(adminDSN, live) }

// grantBackupRole puts the backup role's read access back after a restore.
//
// # The failure this prevents
//
// `GRANT SELECT ON ALL TABLES` grants on the objects that exist when it runs. A
// restore creates NEW objects in a NEW database, so every one of those grants
// is gone — and the role that takes the nightly backup can no longer read a
// single table. Nothing notices until the next backup, which fails; or worse,
// nothing notices at all, and the business is unprotected from the moment it
// recovered from being unprotected.
//
// It was found by running a restore and then a backup, in that order, and the
// precondition check in `canReadEverything` named all 186 tables. That check is
// what turns this from a silent gap into a sentence; this function is what
// stops it happening.
//
// `ALTER DEFAULT PRIVILEGES` is the half that matters longest: it is what keeps
// the grant true for tables a future migration adds.
func grantBackupRole(ctx context.Context, ownerDSN, backupRole string) error {
	backupRole = strings.TrimSpace(backupRole)
	owner := userOf(ownerDSN)
	if backupRole == "" || owner == "" || backupRole == owner {
		// One role doing both jobs needs no grant: it owns the tables. That is
		// the wrong arrangement for other reasons — see `canReadEverything` —
		// and it is not this function's business to say so twice.
		return nil
	}

	conn, err := pgx.Connect(ctx, ownerDSN)
	if err != nil {
		return errs.Wrap(err, errs.CodeUnavailable,
			"The restored database could not be opened to restore the backup "+
				"role's access to it.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	// Both names come from connection strings in the environment, never from a
	// request. Quoted regardless.
	role := `"` + backupRole + `"`
	for _, stmt := range []string{
		`GRANT USAGE ON SCHEMA public TO ` + role,
		`GRANT SELECT ON ALL TABLES IN SCHEMA public TO ` + role,
		`GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO ` + role,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO ` + role,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON SEQUENCES TO ` + role,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return errs.Wrap(err, errs.CodeInternal,
				"The restored database could not be made readable by the "+
					"backup role, so the next backup would fail: "+stmt)
		}
	}
	return nil
}
