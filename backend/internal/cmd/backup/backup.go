// The backup command: take one, prove it restores, carry it somewhere else,
// put it back, and remove the ones outside the retention policy.
//
//	rawsyst backup run
//	rawsyst backup list
//	rawsyst backup verify   [-snapshot ID]
//	rawsyst backup download [-snapshot ID] [-to DIR]
//	rawsyst backup check     -dump FILE [-manifest FILE]
//	rawsyst backup verify-file -dump FILE [-manifest FILE]
//	rawsyst backup restore  -snapshot ID -into DSN
//	rawsyst backup restore-file -dump FILE -into DSN
//	rawsyst backup prune    [-dry-run]
//	rawsyst backup rehearse
//	rawsyst backup agent    [-once]
//	rawsyst backup health
//
// # Why the nightly backup is a timer and not a loop
//
// A daily backup wants to be a timer, not a process. systemd already runs
// things on a schedule and already reports when they fail, so the runbook uses
// a timer and `run` exits when it is done.
//
// `agent` is a different thing and exists for a different reason: somebody
// pressing CREATE BACKUP on a website needs something to be listening. It
// claims tasks from `backup_task` and does them. It is not a scheduler and it
// does not decide when a backup happens.
//
// # Why this lives in the postgres image
//
// `pg_dump` and `pg_restore` are the right tools and this product is not going
// to reimplement them. The API image is `scratch` and has no room for them, so
// the backup service is built on `postgres:17-alpine` — already pulled, already
// on the server, and guaranteed to be the same major version as the database it
// is dumping, which is the one thing that actually has to match.
package backup

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/backup"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/build"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/maintenance"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/blob"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// Main dispatches the backup subcommands.
func Main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("say what to do")
	}
	action, rest := args[0], args[1:]

	fs := flag.NewFlagSet("backup "+action, flag.ExitOnError)
	snapshot := fs.String("snapshot", "",
		"which snapshot, by id. Defaults to the newest completed one")
	into := fs.String("into", "",
		"the database to restore INTO, as a connection string. Never the one "+
			"in use: restore beside it and switch")
	to := fs.String("to", ".", "where to put a downloaded backup")
	dumpFile := fs.String("dump", "", "a backup dump on this computer")
	manifestFile := fs.String("manifest", "",
		"its manifest. Defaults to the file beside the dump")
	dryRun := fs.Bool("dry-run", false,
		"say what would be removed and remove nothing")
	once := fs.Bool("once", false, "run at most one queued task, then stop")
	from := fs.String("from", "",
		"the database a production restore renamed aside, to put back")
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	// `check` needs nothing: no database, no bucket, no configuration. That is
	// the point of it — it runs on a laptop with a downloaded file and tells
	// you whether the file is intact. Handled before the config is loaded so a
	// missing RAWSYST_JWT_SECRET cannot stop somebody checking their backup.
	if action == "check" {
		return doCheck(*dumpFile, *manifestFile, *jsonOut)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	store := blob.Open(cfg.Storage)

	key, sealed, err := backup.KeyFromEnv(os.Getenv("RAWSYST_BACKUP_ENCRYPTION_KEY"))
	if err != nil {
		return err
	}

	opts := backup.Options{
		// A dedicated backup role where one is configured. `pg_dump` has to be
		// able to read past row-level security and the application's role must
		// NOT be able to; see `canReadEverything` in internal/backup.
		DSN:         env("RAWSYST_BACKUP_DSN", cfg.DB.DSN),
		AppDSN:      cfg.DB.DSN,
		Store:       store,
		Prefix:      env("RAWSYST_BACKUP_PREFIX", "rawsyst"),
		AppVersion:  build.Version,
		GitCommit:   os.Getenv("RAWSYST_GIT_COMMIT"),
		Environment: string(cfg.Env),
		SourceHost:  hostname(),
		Key:         key,
		TempDir:     os.Getenv("RAWSYST_BACKUP_TEMP_DIR"),
		Timeout:     duration("RAWSYST_BACKUP_TIMEOUT", 30*time.Minute),

		// Room to stage a dump, checked before one starts. 100 means "as much
		// free space as the database measures", which is pessimistic on
		// purpose: a dump is compressed and carries no indexes, so it is
		// reliably smaller, and the cost of this being too careful is a
		// message rather than a database that cannot write.
		MinFreePercent: intEnv("RAWSYST_BACKUP_MIN_FREE_PERCENT", 100),

		// Said on the terminal and in the journal. A check that could not be
		// performed is worth a line; it is not worth refusing to back up over.
		Warn: func(msg string) {
			fmt.Fprintf(os.Stderr, "backup: %s\n", msg)
		},
	}
	ctx := context.Background()

	// The register, so what happened is visible to a platform operator rather
	// than only to whoever read the terminal. Best-effort on purpose for the
	// commands that TAKE a backup: a database that cannot be written to must
	// not stop a backup from being taken — the backup is the thing that
	// matters, and the row about it is not.
	var register *backup.Register
	var tasks *backup.Tasks
	var maintenanceSvc *maintenance.Service
	if pool, err := db.Open(ctx, cfg.DB); err == nil {
		defer pool.Close()
		register = backup.NewRegister(pool)
		tasks = backup.NewTasks(pool)
		maintenanceSvc = maintenance.NewService(pool)
	}

	switch action {
	case "run":
		return doRun(ctx, opts, register, sealed, *jsonOut)
	case "list":
		return doList(ctx, opts, *jsonOut)
	case "verify":
		return doVerify(ctx, opts, register, *snapshot, *jsonOut)
	case "download":
		return doDownload(ctx, opts, *snapshot, *to, *jsonOut, false)
	case "verify-file":
		return doVerifyFile(ctx, opts, *dumpFile, *manifestFile, *jsonOut)
	case "restore":
		return doRestore(ctx, opts, *snapshot, *into)
	case "restore-file":
		return doRestoreFile(ctx, opts, *dumpFile, *manifestFile, *into)
	case "prune":
		return doPrune(ctx, opts, register, *dryRun)
	case "rehearse":
		return doRehearse(ctx, opts, *jsonOut)
	case "agent":
		return doAgent(ctx, opts, cfg, register, tasks, maintenanceSvc, *once)
	case "rollback":
		return doRollback(ctx, cfg, *from)
	case "health":
		return doHealth(ctx, register, *jsonOut)
	case "role":
		return doRole(ctx, cfg, opts, *dryRun, *jsonOut)
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("no such action %q", action)
	}
}

func doRun(
	ctx context.Context, opts backup.Options, register *backup.Register,
	sealed, asJSON bool,
) error {
	kind := "manual"
	if os.Getenv("INVOCATION_ID") != "" {
		// systemd sets this. A run from a timer is scheduled; a run somebody
		// typed is manual, and the register keeps them apart because "when did
		// a person last take one" is a different question from "is the timer
		// working".
		kind = "scheduled"
	}
	id, _ := register.Start(ctx, kind, "server", nil)

	res, err := backup.Run(ctx, opts)
	if err != nil {
		_ = register.Failed(ctx, id, err.Error())
		return err
	}
	_ = register.Succeeded(ctx, id, res, opts.Store.Bucket())
	if asJSON {
		return print(res.Manifest)
	}
	fmt.Printf("snapshot   %s\n", res.SnapshotID)
	fmt.Printf("location   %s\n", res.Location)
	fmt.Printf("size       %s\n", human(res.Bytes))
	fmt.Printf("sha-256    %s\n", res.SHA256)
	fmt.Printf("schema     %d\n", res.Manifest.SchemaVersion)
	fmt.Printf("tables     %d\n", res.Manifest.Tables)
	fmt.Printf("database   %s (%s)\n",
		res.Manifest.DatabaseName, human(res.Manifest.DatabaseSize))
	if inv := res.Manifest.Inventory; inv != nil {
		fmt.Printf("businesses %d, companies %d\n",
			len(inv.TenantRows), len(inv.CompanyRows))
	}
	if sealed {
		fmt.Printf("encrypted  yes, key %s — WITHOUT THAT KEY THIS BACKUP "+
			"CANNOT BE READ BY ANYBODY\n", opts.Key.Fingerprint())
	} else {
		fmt.Printf("encrypted  no. The store and whoever runs it can read " +
			"this dump; see deploy/server/BACKUP.md\n")
	}
	fmt.Printf("\nTaken, uploaded and marked complete. NOT yet verified — " +
		"run `rawsyst backup verify` to find out whether it restores, which " +
		"is the only thing that makes it a backup.\n")
	return nil
}

func doList(ctx context.Context, opts backup.Options, asJSON bool) error {
	snapshots, err := backup.List(ctx, opts)
	if err != nil {
		return err
	}
	if asJSON {
		return print(snapshots)
	}
	if len(snapshots) == 0 {
		fmt.Println("no snapshots in " + opts.Store.Bucket() + "/" + opts.Prefix)
		return nil
	}
	for _, s := range snapshots {
		state := "COMPLETE"
		if !s.Completed {
			// Listed rather than hidden. Something went wrong there, and a
			// half-finished backup nobody can see is one somebody reaches for.
			state = "incomplete — the run that made it did not finish"
		}
		fmt.Printf("%-28s %s\n", s.ID, state)
	}
	return nil
}

func doVerify(
	ctx context.Context, opts backup.Options, register *backup.Register,
	id string, asJSON bool,
) error {
	id, err := resolve(ctx, opts, id)
	if err != nil {
		return err
	}
	admin, err := adminDSN()
	if err != nil {
		return err
	}

	var recordID uuid.UUID
	if rec, err := register.FindBySnapshot(ctx, id); err == nil {
		recordID = rec.ID
	}

	report, verifyErr := backup.Verify(ctx, opts, admin, id)
	_ = register.Verified(ctx, recordID, report, reasonOf(verifyErr))
	if asJSON {
		_ = print(report)
	}
	if verifyErr != nil {
		return verifyErr
	}
	if asJSON {
		return nil
	}
	printReport(report)
	fmt.Printf("\nVERIFIED. It was restored into a temporary database, checked " +
		"and dropped. Production was not touched.\n")
	return nil
}

func printReport(report backup.VerifyReport) {
	fmt.Printf("snapshot   %s\n", report.SnapshotID)
	for _, c := range report.Checked {
		fmt.Printf("  ok       %s\n", c)
	}
	fmt.Printf("schema     %d\n", report.Schema)
	fmt.Printf("tables     %d\n", report.Tables)
	fmt.Printf("rows       %d\n", report.Rows)
	fmt.Printf("businesses %d\n", report.Tenants)
	fmt.Printf("companies  %d\n", report.Companies)
	fmt.Printf("size       %s\n", human(report.Bytes))
	fmt.Printf("took       %s\n", report.Took)
	if !report.Complete {
		fmt.Printf("\nNOTE: this snapshot carries an older manifest with only " +
			"a partial inventory, so fewer things could be compared than for " +
			"a backup taken by this build.\n")
	}
	for _, f := range report.Findings {
		fmt.Printf("  FOUND    %s\n", f)
	}
}

// doDownload pulls a snapshot onto this computer as the three files a person
// carries: the dump, its manifest and a checksum file `sha256sum` can read.
func doDownload(
	ctx context.Context, opts backup.Options, id, dir string,
	asJSON, quiet bool,
) error {
	id, err := resolve(ctx, opts, id)
	if err != nil {
		return err
	}
	manifest, err := backup.ReadManifest(ctx, opts, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	names := backup.NamesFor(id)

	body, _, err := opts.Store.GetStream(ctx,
		backup.SnapshotKey(opts.Prefix, id, backup.DatabaseObject()))
	if err != nil {
		return err
	}
	defer body.Close()

	dumpPath := filepath.Join(dir, names.Dump)
	f, err := os.Create(dumpPath)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(dumpPath)
		return fmt.Errorf("the download did not finish: %w", copyErr)
	}

	pretty, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(
		filepath.Join(dir, names.Manifest), pretty, 0o640); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, names.Checksum),
		backup.ChecksumFile(manifest.Database.SHA256, names.Dump),
		0o640); err != nil {
		return err
	}

	// Checked here rather than left to the operator. A download that silently
	// lost bytes is the ordinary failure, and the whole point of the checksum
	// is to catch it before somebody carries the file across the world.
	check, err := backup.CheckLocal(dumpPath, "", "")
	if err != nil {
		return err
	}
	if quiet {
		if !check.Passed {
			return fmt.Errorf("the downloaded artifact did not check out: %s",
				strings.Join(check.Findings, "; "))
		}
		return nil
	}
	if asJSON {
		return print(check)
	}
	fmt.Printf("%s\n%s\n%s\n\n",
		filepath.Join(dir, names.Dump),
		filepath.Join(dir, names.Manifest),
		filepath.Join(dir, names.Checksum))
	fmt.Printf("size       %s\n", human(written))
	fmt.Printf("sha-256    %s\n", check.SHA256)
	if check.Encrypted {
		fmt.Printf("encrypted  yes, key %s — keep the key somewhere this "+
			"file is not\n", check.KeyFingerprint)
	}
	if !check.Passed {
		for _, f := range check.Findings {
			fmt.Printf("  FOUND    %s\n", f)
		}
		return fmt.Errorf("the downloaded artifact did not check out")
	}
	// The filename is an argument rather than part of the format, so a
	// snapshot id can never be read as a verb. It cannot contain a percent
	// sign — ValidSnapshotID sees to that — and relying on it not to would be
	// the kind of reasoning that stops being true.
	fmt.Printf("\nThe three files agree. Check it again anywhere with:\n"+
		"  sha256sum -c %s\n", names.Checksum)
	return nil
}

func doCheck(dumpPath, manifestPath string, asJSON bool) error {
	if strings.TrimSpace(dumpPath) == "" {
		return fmt.Errorf("name the dump with -dump")
	}
	check, err := backup.CheckLocal(dumpPath, manifestPath, "")
	if err != nil {
		return err
	}
	if asJSON {
		return print(check)
	}
	fmt.Printf("snapshot   %s\n", check.SnapshotID)
	fmt.Printf("taken      %s\n", check.TakenAt)
	fmt.Printf("built by   %s\n", check.AppVersion)
	fmt.Printf("schema     %d\n", check.SchemaVersion)
	fmt.Printf("tables     %d\n", check.Tables)
	fmt.Printf("size       %s\n", human(check.Bytes))
	fmt.Printf("sha-256    %s\n", check.SHA256)
	if check.Encrypted {
		fmt.Printf("encrypted  yes, key %s\n", check.KeyFingerprint)
	}
	for _, f := range check.Findings {
		fmt.Printf("  FOUND    %s\n", f)
	}
	if !check.Passed {
		return fmt.Errorf("this artifact did not check out")
	}
	fmt.Printf("\nThe dump, the manifest and the checksum agree. That says the " +
		"file is intact. It does NOT say it restores — `verify-file` does " +
		"that, and it needs a PostgreSQL to restore into.\n")
	return nil
}

func doVerifyFile(
	ctx context.Context, opts backup.Options, dumpPath, manifestPath string,
	asJSON bool,
) error {
	if strings.TrimSpace(dumpPath) == "" {
		return fmt.Errorf("name the dump with -dump")
	}
	admin, err := adminDSN()
	if err != nil {
		return err
	}
	report, err := backup.VerifyLocal(ctx, opts, admin, dumpPath, manifestPath)
	if asJSON {
		_ = print(report)
	}
	if err != nil {
		return err
	}
	if asJSON {
		return nil
	}
	printReport(report)
	fmt.Printf("\nVERIFIED. This file was restored into a temporary database " +
		"on this server, checked against its manifest, and the temporary " +
		"database was dropped. Nothing else was touched.\n")
	return nil
}

func doRestore(ctx context.Context, opts backup.Options, id, into string) error {
	if strings.TrimSpace(into) == "" {
		return fmt.Errorf("name the database to restore INTO with -into. It " +
			"must be empty, and it must not be the one currently in use: " +
			"restore beside the live database and switch to it, so that if " +
			"anything goes wrong the database you have is the one you had")
	}
	id, err := resolve(ctx, opts, id)
	if err != nil {
		return err
	}
	if err := backup.Restore(ctx, opts, into, id); err != nil {
		return err
	}
	fmt.Printf("restored %s\n\n%s", id, afterRestore())
	return nil
}

func doRestoreFile(
	ctx context.Context, opts backup.Options, dumpPath, manifestPath, into string,
) error {
	if strings.TrimSpace(dumpPath) == "" {
		return fmt.Errorf("name the dump with -dump")
	}
	if strings.TrimSpace(into) == "" {
		return fmt.Errorf("name the database to restore INTO with -into")
	}
	if err := backup.RestoreLocal(
		ctx, opts, into, dumpPath, manifestPath); err != nil {
		return err
	}
	fmt.Printf("restored %s\n\n%s", dumpPath, afterRestore())
	return nil
}

func afterRestore() string {
	return "Now run `rawsyst migrate` against it: the snapshot carries the " +
		"schema it was taken at, and the migrator applies anything this build " +
		"added since. Then check it before pointing anything at it — " +
		"deploy/server/RECOVERY.md lists what to check.\n"
}

func doPrune(
	ctx context.Context, opts backup.Options, register *backup.Register,
	dryRun bool,
) error {
	policy := backup.PolicyFromEnv()
	protected, _ := register.Protected(ctx)
	removed, err := backup.Prune(ctx, opts, policy, protected, dryRun)
	if err != nil {
		return err
	}
	fmt.Printf("keeping %d daily, %d weekly, %d monthly, always the newest, "+
		"and %d protected by name\n",
		policy.Daily, policy.Weekly, policy.Monthly, len(protected))
	for _, id := range protected {
		fmt.Printf("  keeping  %s (verified)\n", id)
	}
	if len(removed) == 0 {
		fmt.Println("nothing outside the policy")
		return nil
	}
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	for _, id := range removed {
		fmt.Printf("%s %s\n", verb, id)
	}
	return nil
}

// doRehearse is the whole recovery, end to end, against disposable resources.
//
// Take a backup, upload it, download it to files, check those files with
// nothing but their own checksums, restore them into a temporary database,
// compare everything, and drop the temporary database. It is the rehearsal
// `deploy/server/RECOVERY.md` says to run, and it exists as a command because
// a rehearsal nobody can run in one line is a rehearsal nobody runs.
func doRehearse(
	ctx context.Context, opts backup.Options, asJSON bool,
) error {
	admin, err := adminDSN()
	if err != nil {
		return err
	}
	started := time.Now()
	stages := []map[string]any{}
	note := func(what string, since time.Time, extra map[string]any) {
		row := map[string]any{
			"stage": what, "seconds": time.Since(since).Round(time.Second).Seconds(),
		}
		for k, v := range extra {
			row[k] = v
		}
		stages = append(stages, row)
		if !asJSON {
			fmt.Printf("  %-28s %6.0fs %v\n", what,
				time.Since(since).Seconds(), extra)
		}
	}

	if !asJSON {
		fmt.Println("Disaster recovery rehearsal. Production is not touched.")
	}

	t := time.Now()
	res, err := backup.Run(ctx, opts)
	if err != nil {
		return err
	}
	note("backup taken and uploaded", t, map[string]any{
		"snapshot": res.SnapshotID, "bytes": res.Bytes})

	dir, err := os.MkdirTemp(opts.TempDir, "rawsyst-rehearsal-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	t = time.Now()
	if err := doDownload(ctx, opts, res.SnapshotID, dir, false, true); err != nil {
		return err
	}
	// The checksum of what came back down, which is the evidence: it is the
	// number an operator can compare against the manifest with a tool that is
	// not this product.
	names := backup.NamesFor(res.SnapshotID)
	downloaded, err := backup.CheckLocal(filepath.Join(dir, names.Dump), "", "")
	if err != nil {
		return err
	}
	note("downloaded and checksummed", t, map[string]any{
		"sha256": downloaded.SHA256, "bytes": downloaded.Bytes})

	t = time.Now()
	report, err := backup.VerifyLocal(ctx, opts, admin,
		filepath.Join(dir, names.Dump), filepath.Join(dir, names.Manifest))
	if err != nil {
		if asJSON {
			_ = print(map[string]any{"stages": stages, "report": report,
				"passed": false, "error": err.Error()})
		}
		return err
	}
	note("restored and verified", t, map[string]any{
		"tables": report.Tables, "rows": report.Rows,
		"businesses": report.Tenants, "companies": report.Companies})

	out := map[string]any{
		"passed":        true,
		"snapshot":      res.SnapshotID,
		"bytes":         res.Bytes,
		"total_seconds": time.Since(started).Round(time.Second).Seconds(),
		"stages":        stages,
		"report":        report,
	}
	if asJSON {
		return print(out)
	}
	fmt.Printf("\nPASS. Recovery from nothing but a bucket took %s.\n",
		time.Since(started).Round(time.Second))
	fmt.Printf("The temporary database has been dropped and production was " +
		"never opened for writing.\n")
	return nil
}

func doAgent(
	ctx context.Context, opts backup.Options, cfg config.Config,
	register *backup.Register, tasks *backup.Tasks,
	maintenanceSvc *maintenance.Service, once bool,
) error {
	if register == nil || tasks == nil {
		return fmt.Errorf("the agent needs a database connection: it takes " +
			"its work from backup_task and writes down what it did")
	}
	admin, err := adminDSN()
	if err != nil {
		return err
	}

	live, err := databaseNameOf(cfg.DB.DSN)
	if err != nil {
		return err
	}

	agent := &backup.Agent{
		Tasks:       tasks,
		Register:    register,
		Options:     opts,
		AdminDSN:    admin,
		StagingDir:  env("RAWSYST_BACKUP_STAGING_DIR", "/staging"),
		Maintenance: maintenanceSvc,
		Production: backup.ProductionOptions{
			AdminDSN:     admin,
			LiveDatabase: live,
			Enabled: strings.EqualFold(
				os.Getenv("RAWSYST_ALLOW_PRODUCTION_RESTORE"), "true"),
		},
		Poll:      duration("RAWSYST_BACKUP_AGENT_POLL", 5*time.Second),
		Abandoned: duration("RAWSYST_BACKUP_AGENT_ABANDONED", 6*time.Hour),
	}

	if once {
		worked, err := agent.Step(ctx)
		if err != nil {
			return err
		}
		if !worked {
			fmt.Println("nothing queued")
		}
		return nil
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return agent.Run(ctx)
}

// doRollback puts back the database a production restore renamed aside.
//
// The one command somebody types when a restore turned out to be the wrong
// decision, and the reason `RestoreToProduction` renames rather than drops. It
// is a command rather than a paragraph in a runbook because the moment it is
// needed is the moment nobody wants to be composing SQL.
func doRollback(ctx context.Context, cfg config.Config, previous string) error {
	if strings.TrimSpace(previous) == "" {
		return fmt.Errorf("name the database to put back with -from. It is " +
			"the one called rawsyst_pre_restore_<timestamp>; the report from " +
			"the restore names it, and the psql list-databases command " +
			"shows them")
	}
	admin, err := adminDSN()
	if err != nil {
		return err
	}
	live, err := databaseNameOf(cfg.DB.DSN)
	if err != nil {
		return err
	}
	if err := backup.Rollback(ctx, admin, live, previous); err != nil {
		return err
	}
	fmt.Printf(rollbackDone, previous, live)
	return nil
}

func doHealth(
	ctx context.Context, register *backup.Register, asJSON bool,
) error {
	if register == nil {
		return fmt.Errorf("no database connection")
	}
	state, err := register.State(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return print(state)
	}
	fmt.Printf("%s  %s\n", strings.ToUpper(state.State), state.Says)
	if state.LastVerified != "" {
		fmt.Printf("last verified  %s (%s)\n",
			state.LastVerified, state.LastVerifiedID)
	}
	if state.LastRun != "" {
		fmt.Printf("last run       %s (%s)\n", state.LastRun, state.LastRunStatus)
	}
	fmt.Printf("verified       %d\nnever checked  %d\nfailed (7d)    %d\n",
		state.Verified, state.Unverified, state.Failed)
	if state.LastFailureWhy != "" {
		fmt.Printf("last failure   %s\n", state.LastFailureWhy)
	}
	if state.State != backup.HealthGreen {
		return fmt.Errorf("backups are not healthy")
	}
	return nil
}

// doRole creates or repairs the role that takes the backup.
//
// Run on every deploy. It is idempotent, it never rotates a password nobody
// asked it to rotate, and it ends by connecting as the role and running the
// same precondition check `backup run` runs — so a green result means a backup
// would succeed, rather than that some SQL did not error.
func doRole(
	ctx context.Context, cfg config.Config, opts backup.Options,
	dryRun, asJSON bool,
) error {
	// The DSN the backup will really use, so the check at the end tests the
	// deployment's own configuration. Empty means this server has not been
	// told about a backup role yet, and the report says the check was skipped
	// rather than quietly passing.
	verify := os.Getenv("RAWSYST_BACKUP_DSN")

	report, err := backup.EnsureRole(ctx, backup.RoleOptions{
		AdminDSN:  os.Getenv("RAWSYST_BACKUP_ADMIN_DSN"),
		AppDSN:    cfg.DB.DSN,
		Role:      env("RAWSYST_BACKUP_ROLE", backup.DefaultBackupRole),
		Password:  os.Getenv("RAWSYST_BACKUP_ROLE_PASSWORD"),
		VerifyDSN: verify,
		DryRun:    dryRun,
	})
	if report != nil && asJSON {
		if perr := print(report); perr != nil {
			return perr
		}
		return err
	}
	if report != nil {
		what := "unchanged"
		switch {
		case report.DryRun:
			what = "would change"
		case report.Created:
			what = "created"
		case report.GrantedBypass || report.PasswordSet:
			what = "repaired"
		}
		fmt.Printf("role %s on %s: %s\n", report.Role, report.Database, what)
		if report.Owner != "" {
			fmt.Printf("owner          %s (unchanged, and must stay NOBYPASSRLS)\n",
				report.Owner)
		}
		for _, s := range report.Statements {
			fmt.Printf("  %s\n", s)
		}
		if report.VerifyNote != "" {
			fmt.Printf("note           %s\n", report.VerifyNote)
		}
		switch {
		case report.DryRun:
			fmt.Println("nothing was changed")
		case report.Verified != nil && *report.Verified:
			fmt.Println("checked        the role can read every table and sequence")
		case report.Verified != nil:
			fmt.Println("checked        FAILED, see the error below")
		default:
			fmt.Println("checked        skipped: RAWSYST_BACKUP_DSN is not set, " +
				"so there is nothing to check the role against")
		}
	}
	return err
}

// resolve turns an empty snapshot argument into the newest completed one.
func resolve(ctx context.Context, opts backup.Options, id string) (string, error) {
	if strings.TrimSpace(id) != "" {
		if !backup.ValidSnapshotID(id) {
			return "", fmt.Errorf("%q is not a snapshot id", id)
		}
		return id, nil
	}
	snapshots, err := backup.List(ctx, opts)
	if err != nil {
		return "", err
	}
	for _, s := range snapshots {
		if s.Completed {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("no completed snapshot in %s/%s",
		opts.Store.Bucket(), opts.Prefix)
}

func adminDSN() (string, error) {
	admin := strings.TrimSpace(os.Getenv("RAWSYST_BACKUP_ADMIN_DSN"))
	if admin == "" {
		return "", fmt.Errorf("set RAWSYST_BACKUP_ADMIN_DSN to a connection " +
			"on the same server as the database, pointing at a DIFFERENT " +
			"database — `postgres` will do. A scratch database cannot be " +
			"created from inside the one being created beside")
	}
	return admin, nil
}

// databaseNameOf is the database a connection string names.
//
// Used only to tell the agent which database a production restore would rename,
// and it is read from the DSN rather than guessed because that is the one thing
// in this subsystem that must not be guessed.
func databaseNameOf(dsn string) (string, error) {
	cut := strings.SplitN(dsn, "://", 2)
	if len(cut) != 2 {
		return "", fmt.Errorf("RAWSYST_DB_DSN is not a connection URL, so the " +
			"live database cannot be named")
	}
	rest := cut[1]
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[i+1:]
	} else {
		return "", fmt.Errorf("RAWSYST_DB_DSN does not name a database")
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return "", fmt.Errorf("RAWSYST_DB_DSN does not name a database")
	}
	return rest, nil
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: rawsyst backup <action> [flags]

  run           take a backup: dump, upload, manifest, completion marker
  list          what is in the store, newest first
  verify        restore a snapshot into a temporary database and check it
  download      pull a snapshot onto this computer as three files
  check         check a downloaded artifact. No database, no bucket, no network
  verify-file   restore a downloaded artifact into a temporary database
  restore       restore a snapshot into a database you name with -into
  restore-file  restore a downloaded artifact into a database you name
  prune         remove snapshots outside the retention policy
  rehearse      the whole recovery, end to end, against disposable resources
  rollback      put back the database a production restore renamed aside
  agent         do the work the website queued. Runs until stopped
  health        is this installation actually protected
  role          create or repair the role that takes the backup, and prove it
                can read. Idempotent; -dry-run says what it would do

Environment:
  RAWSYST_DB_DSN                 the database
  RAWSYST_BACKUP_DSN             a role that may read past row-level security.
                                 Defaults to RAWSYST_DB_DSN, which on a
                                 correctly-configured server cannot
  RAWSYST_S3_ENDPOINT/BUCKET     where snapshots go
  RAWSYST_S3_ACCESS_KEY_ID       and its secret
  RAWSYST_BACKUP_PREFIX          namespace inside the bucket (rawsyst)
  RAWSYST_BACKUP_ADMIN_DSN       a connection for creating the scratch
                                 database a verification restores into, and
                                 the one "role" creates the role with
  RAWSYST_BACKUP_ROLE            the role "role" creates (rawsyst_backup)
  RAWSYST_BACKUP_ROLE_PASSWORD   its password, used only when creating it or
                                 when a rotation is deliberately asked for.
                                 Put the same value in RAWSYST_BACKUP_DSN
  RAWSYST_BACKUP_ENCRYPTION_KEY  base64 of 32 bytes. Optional, and a lost key
                                 is a lost backup
  RAWSYST_ALLOW_PRODUCTION_RESTORE  must be "true" before the agent will
                                 replace the live database
  RAWSYST_BACKUP_KEEP_DAILY      7
  RAWSYST_BACKUP_KEEP_WEEKLY     4
  RAWSYST_BACKUP_KEEP_MONTHLY    3
  RAWSYST_BACKUP_TIMEOUT         30m
  RAWSYST_BACKUP_TEMP_DIR        where a dump is staged
  RAWSYST_BACKUP_STAGING_DIR     where an uploaded artifact waits

See deploy/server/BACKUP.md, MIGRATION.md and RECOVERY.md.
`)
}

func print(v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(body))
	return nil
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func duration(name string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// intEnv is a positive whole number from the environment.
//
// A value that does not parse falls back rather than failing the command: this
// configures how careful a precondition is, and a typo in it must not be the
// reason a server stops taking backups.
func intEnv(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func reasonOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// rollbackDone is what an operator reads immediately after putting a database
// back, and the second sentence is the one that matters: nothing was dropped.
const rollbackDone = "%s is now %s.\n\n" +
	"The database that was serving has been renamed aside and NOT dropped. " +
	"Restart the application so it opens fresh connections, then check it.\n"
