// Proving a backup restores, and saying exactly what was proved.
//
// # Why a checksum is not a verification
//
// A checksum says the bytes came back the way they went. It says nothing about
// whether those bytes are a database anybody can use — a truncated dump hashes
// perfectly once it has been truncated, and so does a dump of an empty schema
// taken while the application was pointed at the wrong DSN.
//
// So `Verify` restores. It pulls the snapshot down, checks the checksum against
// the manifest, decrypts it if it is sealed, restores it into a temporary
// database beside the real one, takes a complete inventory of what came back,
// compares that against the inventory the manifest recorded, and drops the
// temporary database again. That is the whole of what makes a backup a backup,
// and it is the reason `backup_record.verified_at` exists separately from
// `status`.
//
// # What "complete" means here
//
// Every base table, by name, with its row count. Every business's total, and
// every company's, so a restore that brought one shop back and lost another is
// caught by a number rather than by that shop ringing up. Every sequence and
// where it stands, because a database whose sequences came back at 1 issues a
// duplicate invoice number on its first write and does it quietly. Every
// extension, every row-level-security policy by name, and the counts of
// indexes, primary keys, foreign keys, unique constraints, check constraints,
// functions and triggers. A restore that lost the foreign keys accepts an order
// for a customer who does not exist.
//
// # It never touches production
//
// The temporary database is named from the snapshot id, it is created and
// dropped by this code, and the drop runs whatever happened. `Verify` is given
// an admin connection to a DIFFERENT database on the same server precisely so
// that it cannot be pointed at the live one by accident.
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

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// VerifyReport is what a verification found.
//
// Written into `backup_record.verify_report` and shown on the Backup & Recovery
// screen, so it is the document somebody reads when deciding whether to trust a
// backup with their business. Every field is a fact rather than a judgement,
// except `Passed`, which is the judgement and is false unless every check
// passed.
type VerifyReport struct {
	SnapshotID string `json:"snapshot_id"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Took       string `json:"took"`

	// Checked is what was actually done, in order. A person reading a report
	// should be able to see which checks ran, not only that something passed.
	Checked []string `json:"checked"`

	// Findings are the reasons it did not pass. Empty on a pass.
	Findings []string `json:"findings,omitempty"`

	Passed   bool `json:"passed"`
	Restored bool `json:"restored"`

	// Complete says whether the manifest carried a full inventory. A version 1
	// manifest does not, and a verification against one checks less. Recorded
	// rather than glossed over: a thinner check reported as a full one is the
	// same lie as no check at all.
	Complete bool `json:"complete_inventory"`

	Encrypted bool `json:"encrypted"`

	Bytes      int64 `json:"bytes"`
	PlainBytes int64 `json:"plaintext_bytes,omitempty"`

	// Expected and Restored inventories, side by side. Large, and worth it: an
	// operator asking "which table is short" should not have to run anything to
	// find out.
	Expected *Inventory `json:"expected,omitempty"`
	Found    *Inventory `json:"found,omitempty"`

	// The headline numbers, lifted out so a screen does not have to dig.
	Tables    int   `json:"tables_restored"`
	Schema    int   `json:"schema_version_restored"`
	Tenants   int   `json:"businesses_restored"`
	Companies int   `json:"companies_restored"`
	Rows      int64 `json:"rows_restored"`
}

// finding records a reason the verification did not pass.
func (r *VerifyReport) finding(format string, args ...any) {
	r.Findings = append(r.Findings, fmt.Sprintf(format, args...))
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
	report := VerifyReport{
		SnapshotID: id,
		StartedAt:  started.UTC().Format(time.RFC3339),
	}
	if !ValidSnapshotID(id) {
		return report, errs.New(errs.CodeInvalidInput, "That is not a snapshot id.")
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// 1. It has to claim to be finished.
	opts.Progress(StageChecking)
	if _, err := opts.Store.Exists(ctx,
		snapshotKey(opts.Prefix, id, completedMark)); err != nil {
		return report, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s has no completion marker, so whatever else is in it, "+
				"the run that made it did not finish. It will not be "+
				"restored.", id)
	}
	report.Checked = append(report.Checked, "completion marker")

	// 2. The manifest has to parse, name this snapshot, and be a version this
	//    build reads.
	manifest, err := ReadManifest(ctx, opts, id)
	if err != nil {
		return report, err
	}
	report.Checked = append(report.Checked, "manifest")
	report.Complete = manifest.Complete()
	report.Expected = manifest.Inventory
	report.Encrypted = manifest.Encryption != nil

	// 3. The dump has to be there and be the size the manifest says.
	key := snapshotKey(opts.Prefix, id, manifest.Database.Key)
	size, err := opts.Store.Exists(ctx, key)
	if err != nil {
		return report, err
	}
	if size != manifest.Database.Bytes {
		return report, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s says its dump is %d bytes and the store holds %d. "+
				"An upload that did not finish looks exactly like this.",
			id, manifest.Database.Bytes, size)
	}
	report.Bytes = size
	report.Checked = append(report.Checked, "object size")

	// 4. And hash to what the manifest recorded, which means pulling it down.
	opts.Progress(StageVerifying)
	dump, plain, err := fetch(ctx, opts, key, manifest)
	if err != nil {
		return report, err
	}
	defer os.Remove(dump)
	report.PlainBytes = plain
	report.Checked = append(report.Checked, "checksum")
	if manifest.Encryption != nil {
		report.Checked = append(report.Checked, "decryption and authentication")
	}

	// 5. The part that makes it a verification.
	opts.Progress(StageRestoring)
	found, err := restoreIntoScratch(ctx, opts, adminDSN, id, dump)
	if err != nil {
		return report, err
	}
	report.Restored = true
	report.Found = &found
	report.Tables = found.TableCount()
	report.Schema = found.SchemaVersion
	report.Tenants = len(found.TenantRows)
	report.Companies = len(found.CompanyRows)
	for _, n := range found.Rows {
		report.Rows += n
	}
	report.Checked = append(report.Checked, "restore into a temporary database")

	// 6. And what came back has to be what went in.
	opts.Progress(StageChecking)
	compare(manifest, found, &report)

	report.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	report.Took = time.Since(started).Round(time.Second).String()
	report.Passed = len(report.Findings) == 0

	if !report.Passed {
		return report, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s did not verify: %s", id,
			strings.Join(report.Findings, "; "))
	}
	return report, nil
}

// compare is the whole of what a verification checks about a restored copy.
//
// Everything it finds goes into `Findings` rather than returning at the first
// one. A report that stops at the first problem sends somebody round the loop
// once per problem, and each loop is a restore.
func compare(m Manifest, found Inventory, report *VerifyReport) {
	want := m.Inventory

	if found.SchemaVersion != m.SchemaVersion {
		report.finding(
			"the restored copy is at schema version %d and the manifest says %d",
			found.SchemaVersion, m.SchemaVersion)
	}
	report.Checked = append(report.Checked, "schema version")

	if m.Tables > 0 && found.TableCount() != m.Tables {
		report.finding("the restored copy has %d tables and the manifest says %d",
			found.TableCount(), m.Tables)
	}
	report.Checked = append(report.Checked, "table count")

	if want == nil {
		return
	}

	// Tables by name, both ways. A table that is missing and a table that
	// appeared are different findings and both matter: the first is lost data
	// and the second means this is not the database the manifest describes.
	missing, extra := difference(want.Tables, found.Tables)
	if len(missing) > 0 {
		report.finding("tables missing from the restored copy: %s",
			strings.Join(clip(missing, 20), ", "))
	}
	if len(extra) > 0 {
		report.finding("tables in the restored copy that the manifest does not "+
			"list: %s", strings.Join(clip(extra, 20), ", "))
	}
	if len(want.Tables) > 0 {
		report.Checked = append(report.Checked, "every table by name")
	}

	// Rows, table by table, for every table the manifest counted.
	var short []string
	for table, n := range want.Rows {
		got, present := found.Rows[table]
		if !present {
			short = append(short, fmt.Sprintf("%s: table missing, expected %d", table, n))
			continue
		}
		if got != n {
			short = append(short, fmt.Sprintf("%s: %d of %d", table, got, n))
		}
	}
	sort.Strings(short)
	if len(short) > 0 {
		report.finding("the restored copy does not hold what the manifest "+
			"recorded — %s", strings.Join(clip(short, 30), "; "))
	}
	if len(want.Rows) > 0 {
		report.Checked = append(report.Checked,
			fmt.Sprintf("row counts, all %d tables", len(want.Rows)))
	}

	// Multi-tenancy. The failure a total row count cannot see is one business
	// restored and another lost, and this is where it is caught.
	compareTotals(want.TenantRows, found.TenantRows, "business", report)
	compareTotals(want.CompanyRows, found.CompanyRows, "company", report)
	if len(want.TenantRows) > 0 {
		report.Checked = append(report.Checked,
			fmt.Sprintf("per-business row totals, all %d businesses",
				len(want.TenantRows)))
	}
	if len(want.CompanyRows) > 0 {
		report.Checked = append(report.Checked,
			fmt.Sprintf("per-company row totals, all %d companies",
				len(want.CompanyRows)))
	}

	// Sequences. A restore with the rows back and the sequences at 1 is a
	// database that issues a duplicate key on its first write.
	var sequences []string
	for name, at := range want.Sequences {
		got, present := found.Sequences[name]
		if !present {
			sequences = append(sequences, name+": missing")
			continue
		}
		if got != at {
			sequences = append(sequences,
				fmt.Sprintf("%s: at %d, expected %d", name, got, at))
		}
	}
	sort.Strings(sequences)
	if len(sequences) > 0 {
		report.finding("sequences did not come back where they were — %s",
			strings.Join(clip(sequences, 20), "; "))
	}
	if len(want.Sequences) > 0 {
		report.Checked = append(report.Checked, "sequence positions")
	}

	// Extensions by name. `pgcrypto` missing means nothing can be inserted.
	var extensions []string
	for name := range want.Extensions {
		if _, present := found.Extensions[name]; !present {
			extensions = append(extensions, name)
		}
	}
	sort.Strings(extensions)
	if len(extensions) > 0 {
		report.finding("extensions missing from the restored copy: %s",
			strings.Join(extensions, ", "))
	}
	if len(want.Extensions) > 0 {
		report.Checked = append(report.Checked, "extensions")
	}

	// Row-level security, by policy name. A missing policy is one business
	// able to read another's books, and a count would not say which.
	missingPolicies, _ := difference(want.Policies, found.Policies)
	if len(missingPolicies) > 0 {
		report.finding("row-level security policies missing from the restored "+
			"copy: %s", strings.Join(clip(missingPolicies, 20), ", "))
	}
	if want.RLSForced > 0 && found.RLSForced != want.RLSForced {
		report.finding("row-level security is forced on %d tables in the "+
			"restored copy and on %d in the manifest — a table where it is "+
			"not forced is one the application's own role reads across every "+
			"business", found.RLSForced, want.RLSForced)
	}
	if len(want.Policies) > 0 {
		report.Checked = append(report.Checked,
			fmt.Sprintf("row-level security, all %d policies by name",
				len(want.Policies)))
	}

	missingViews, _ := difference(want.Views, found.Views)
	if len(missingViews) > 0 {
		report.finding("views missing from the restored copy: %s",
			strings.Join(clip(missingViews, 20), ", "))
	}
	missingMat, _ := difference(want.MatViews, found.MatViews)
	if len(missingMat) > 0 {
		report.finding("materialized views missing from the restored copy: %s",
			strings.Join(clip(missingMat, 20), ", "))
	}

	// The shape of the schema. Counts rather than names, because a foreign key
	// has no name worth showing to a person and the count answers the question:
	// did the constraints come back.
	for _, c := range []struct {
		what       string
		want, have int
	}{
		{"indexes", want.Indexes, found.Indexes},
		{"primary keys", want.PrimaryKeys, found.PrimaryKeys},
		{"foreign keys", want.ForeignKeys, found.ForeignKeys},
		{"unique constraints", want.Uniques, found.Uniques},
		{"check constraints", want.Checks, found.Checks},
		{"functions", want.Functions, found.Functions},
		{"triggers", want.Triggers, found.Triggers},
	} {
		if c.want > 0 && c.have < c.want {
			report.finding("the restored copy has %d %s and the manifest says "+
				"%d", c.have, c.what, c.want)
		}
	}
	if want.Indexes > 0 {
		report.Checked = append(report.Checked,
			"indexes, keys, constraints, functions and triggers")
	}
}

// compareTotals checks a per-owner map both ways.
func compareTotals(want, found map[string]int64, what string, report *VerifyReport) {
	var lost, changed []string
	for id, n := range want {
		got, present := found[id]
		switch {
		case !present:
			lost = append(lost, id)
		case got != n:
			changed = append(changed,
				fmt.Sprintf("%s: %d rows of %d", id, got, n))
		}
	}
	sort.Strings(lost)
	sort.Strings(changed)
	if len(lost) > 0 {
		report.finding("%d %s records are in the manifest and not in the "+
			"restored copy: %s", len(lost), what,
			strings.Join(clip(lost, 10), ", "))
	}
	if len(changed) > 0 {
		report.finding("%s row totals do not match — %s", what,
			strings.Join(clip(changed, 10), "; "))
	}
}

// difference is what is in `want` and not in `have`, and the other way round.
func difference(want, have []string) (missing, extra []string) {
	inHave := make(map[string]bool, len(have))
	for _, s := range have {
		inHave[s] = true
	}
	inWant := make(map[string]bool, len(want))
	for _, s := range want {
		inWant[s] = true
		if !inHave[s] {
			missing = append(missing, s)
		}
	}
	for _, s := range have {
		if !inWant[s] {
			extra = append(extra, s)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

// clip bounds a list so one finding cannot become a page of text.
func clip(items []string, max int) []string {
	if len(items) <= max {
		return items
	}
	out := append([]string{}, items[:max]...)
	return append(out, fmt.Sprintf("and %d more", len(items)-max))
}

// restoreIntoScratch restores a dump into a temporary database and inventories
// it, then drops the database whatever happened.
func restoreIntoScratch(
	ctx context.Context, opts Options, adminDSN, id, dump string,
) (Inventory, error) {
	scratch := ScratchName(id)
	if err := createDatabase(
		ctx, adminDSN, scratch, userOf(opts.AppDSN)); err != nil {
		return Inventory{}, err
	}
	defer func() {
		// Dropped whatever happened. A verification that leaves databases
		// behind fills the disk it is protecting.
		_ = dropDatabase(context.WithoutCancel(ctx), adminDSN, scratch)
	}()

	// Restored as the APPLICATION's role, not the administrative one. That is
	// what a production restore does, so a verification that used a different
	// role would be proving something else — see `createDatabase`.
	if err := pgRestore(
		ctx, restoreDSN(opts, adminDSN, scratch), dump); err != nil {
		return Inventory{}, err
	}

	// Inventoried on the ADMINISTRATIVE connection, which is a superuser and
	// therefore sees past the row-level security the restore just re-enabled.
	// Counting on the application's own connection would count zero rows in
	// every tenant table and report a perfect match against another zero.
	conn, err := pgx.Connect(ctx, dsnFor(adminDSN, scratch))
	if err != nil {
		return Inventory{}, errs.Wrap(err, errs.CodeUnavailable,
			"The restored copy could not be opened.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	// Guard against having reached the wrong database. The name is built from
	// the snapshot id by this code; if what answered is not called that, the
	// admin DSN points somewhere unexpected and nothing further should run.
	var name string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return Inventory{}, errs.Wrap(err, errs.CodeInternal,
			"The restored copy would not say what it is called.")
	}
	if name != scratch {
		return Inventory{}, errs.Newf(errs.CodeInternal,
			"A verification opened %q when it created and restored into %q. "+
				"Nothing further will run against it.", name, scratch)
	}
	return takeInventory(ctx, conn)
}

// ScratchName is the temporary database a snapshot is verified into.
//
// Derived from the snapshot id, which this code generated and which
// `ValidSnapshotID` has already restricted to letters, digits, dash and
// underscore. The prefix is what makes one of these recognisable on a server:
// anything called `rawsyst_verify_…` is this code's and can be dropped.
func ScratchName(id string) string {
	name := "rawsyst_verify_" + strings.ToLower(
		strings.NewReplacer("-", "_", ":", "_", "T", "_", "Z", "").Replace(id))
	if len(name) > 60 {
		name = name[:60]
	}
	return name
}

// fetch downloads an object, checks its checksum, and decrypts it if it is
// sealed. It returns the path of a plaintext dump on disk and its size.
//
// Streamed and hashed in one pass. Nothing is held in memory and nothing is
// trusted: a download that does not hash to the manifest is deleted before it
// can be mistaken for a backup.
func fetch(
	ctx context.Context, opts Options, key string, m Manifest,
) (string, int64, error) {
	body, _, err := opts.Store.GetStream(ctx, key)
	if err != nil {
		return "", 0, err
	}
	defer body.Close()

	f, err := os.CreateTemp(opts.TempDir, "biz1core-fetch-*.bin")
	if err != nil {
		return "", 0, errs.Wrap(err, errs.CodeInternal,
			"A temporary file for the download could not be created.")
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hash), body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(f.Name())
		return "", 0, errs.Wrap(copyErr, errs.CodeUnavailable,
			"The snapshot could not be downloaded in full.")
	}
	if n != m.Database.Bytes {
		os.Remove(f.Name())
		return "", 0, errs.Newf(errs.CodeInvalidInput,
			"The store returned %d bytes for a snapshot the manifest says is "+
				"%d. Do not restore this.", n, m.Database.Bytes)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != m.Database.SHA256 {
		os.Remove(f.Name())
		return "", 0, errs.Newf(errs.CodeInvalidInput,
			"The snapshot does not match its manifest: expected %s, the "+
				"store returned %s. Do not restore this.",
			m.Database.SHA256, got)
	}
	if m.Encryption == nil {
		return f.Name(), n, nil
	}

	plain, size, err := Unseal(opts, f.Name(), m)
	os.Remove(f.Name())
	if err != nil {
		return "", 0, err
	}
	return plain, size, nil
}

// Unseal decrypts a sealed artifact into a new temporary file.
//
// Exported because the same thing has to happen to an artifact that arrived
// from an operator's laptop rather than from the store.
func Unseal(opts Options, sealed string, m Manifest) (string, int64, error) {
	if !opts.Key.Set() {
		return "", 0, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s is encrypted with key %s and no key is configured. "+
				"Set RAWSYST_BACKUP_ENCRYPTION_KEY to the key it was sealed "+
				"with. Without it this backup cannot be read by anybody, "+
				"including this product.",
			m.SnapshotID, m.Encryption.KeyFingerprint)
	}
	in, err := os.Open(sealed)
	if err != nil {
		return "", 0, errs.Wrap(err, errs.CodeInternal,
			"The sealed dump could not be opened.")
	}
	defer in.Close()

	out, err := os.CreateTemp(opts.TempDir, "biz1core-plain-*.pgdump")
	if err != nil {
		return "", 0, errs.Wrap(err, errs.CodeInternal,
			"A temporary file for the decrypted dump could not be created.")
	}
	hash := sha256.New()
	n, err := opts.Key.Open(io.MultiWriter(out, hash), in)
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		os.Remove(out.Name())
		if err == nil {
			err = errs.Wrap(closeErr, errs.CodeInternal,
				"The decrypted dump could not be written.")
		}
		return "", 0, err
	}
	// The plaintext checksum, where the manifest recorded one. This is what
	// says the thing inside the envelope is the dump that was put in it.
	if want := m.Database.PlainSHA256; want != "" {
		if got := hex.EncodeToString(hash.Sum(nil)); got != want {
			os.Remove(out.Name())
			return "", 0, errs.Newf(errs.CodeInvalidInput,
				"The decrypted dump hashes to %s and the manifest says %s.",
				got, want)
		}
	}
	if want := m.Database.PlainBytes; want > 0 && n != want {
		os.Remove(out.Name())
		return "", 0, errs.Newf(errs.CodeInvalidInput,
			"The decrypted dump is %d bytes and the manifest says %d.", n, want)
	}
	return out.Name(), n, nil
}

// --- restoring --------------------------------------------------------------

// Restore puts a snapshot into a database that already exists.
//
// Deliberately not "into production". The caller names the target, the target
// has to be empty of Biz1core tables, and the guard below refuses one that is
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

	if !ValidSnapshotID(id) {
		return errs.New(errs.CodeInvalidInput, "That is not a snapshot id.")
	}
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

	if err := requireEmpty(ctx, targetDSN); err != nil {
		return err
	}

	dump, _, err := fetch(ctx, opts,
		snapshotKey(opts.Prefix, id, manifest.Database.Key), manifest)
	if err != nil {
		return err
	}
	defer os.Remove(dump)

	if err := pgRestore(ctx, targetDSN, dump); err != nil {
		return err
	}
	// See `grantBackupRole`: a restore creates new objects and every GRANT on
	// the old ones is gone with them.
	return grantBackupRole(ctx, targetDSN, userOf(opts.DSN))
}

// requireEmpty refuses a target that already holds tables.
func requireEmpty(ctx context.Context, targetDSN string) error {
	conn, err := pgx.Connect(ctx, targetDSN)
	if err != nil {
		return errs.Wrap(err, errs.CodeUnavailable,
			"The target database could not be opened.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	var existing int
	var name string
	err = conn.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`).
		Scan(&existing)
	_ = conn.QueryRow(ctx, `SELECT current_database()`).Scan(&name)
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
	return nil
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

// createDatabase makes a database for a restore, owned by the application.
//
// # Why the owner matters, and why finding this out cost a cutover
//
// `pg_restore --no-owner` leaves every restored object owned by the role that
// connected. Restore as the administrative role and the tables belong to it —
// so after a cutover the application connects to its own database and is told
// `permission denied for table backup_task`, which is a true statement and a
// completely unusable one. The database is intact and the product cannot read
// a row of it.
//
// So the database is created OWNED BY the application's role and the restore
// runs AS that role, which reproduces exactly the state a fresh `migrate`
// leaves behind. An empty `owner` falls back to whoever is connecting, which is
// the right behaviour for a scratch database nobody will ever connect to as
// anybody else.
func createDatabase(ctx context.Context, adminDSN, name, owner string) error {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return errs.Wrap(err, errs.CodeUnavailable,
			"The server could not be reached to create a scratch database.")
	}
	defer conn.Close(context.WithoutCancel(ctx))

	// The name is built from a snapshot id this code generated, never from
	// input, and it is quoted regardless. The owner comes from a connection
	// string in the environment and is quoted for the same reason.
	stmt := `CREATE DATABASE "` + name + `"`
	if owner != "" {
		stmt += ` OWNER "` + owner + `"`
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"A database for the restore could not be created. The role in "+
				"RAWSYST_BACKUP_ADMIN_DSN needs CREATEDB, and needs to be a "+
				"member of the role that will own it.")
	}
	return nil
}

// restoreDSN is the connection a restore runs on: the application's role,
// pointed at the database being restored into.
//
// Falls back to the administrative connection when no application DSN is
// configured, which keeps a bare `biz1core backup verify` working on a machine
// where only the admin connection is set.
func restoreDSN(opts Options, adminDSN, database string) string {
	if strings.TrimSpace(opts.AppDSN) != "" {
		return dsnFor(opts.AppDSN, database)
	}
	return dsnFor(adminDSN, database)
}

// userOf is the role a connection string signs in as.
func userOf(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return ""
	}
	return u.User.Username()
}

func dropDatabase(ctx context.Context, adminDSN, name string) error {
	// The guard that makes this safe: nothing is dropped whose name this code
	// did not build. A bug that reached here with a production database name
	// would find it refused rather than executed.
	if !strings.HasPrefix(name, "rawsyst_verify_") &&
		!strings.HasPrefix(name, "rawsyst_restore_") {
		return errs.Newf(errs.CodeInvalidInput,
			"Refusing to drop %q: this only drops databases it created.", name)
	}
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))
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
