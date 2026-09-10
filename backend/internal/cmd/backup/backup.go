// The backup command: take one, list them, prove one restores, restore one,
// and remove the ones outside the retention policy.
//
//	rawsyst backup run
//	rawsyst backup list
//	rawsyst backup verify [-snapshot ID]
//	rawsyst backup restore -snapshot ID -into DSN
//	rawsyst backup prune [-dry-run]
//
// # Why this is a command and not a background loop
//
// A daily backup wants to be a timer, not a process. A resident scheduler on a
// 3.7 GiB server is a container that has to be running, watched, restarted and
// kept in memory in order to do something for ninety seconds a day. systemd
// already runs things on a schedule and already reports when they fail, so the
// runbook uses a timer and this exits when it is done.
//
// # Why it lives in the postgres image
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
	"os"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/backup"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/build"
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
	dryRun := fs.Bool("dry-run", false,
		"say what would be removed and remove nothing")
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	store := blob.Open(cfg.Storage)

	opts := backup.Options{
		DSN:        cfg.DB.DSN,
		Store:      store,
		Prefix:     env("RAWSYST_BACKUP_PREFIX", "rawsyst"),
		AppVersion: build.Version,
		TempDir:    os.Getenv("RAWSYST_BACKUP_TEMP_DIR"),
		Timeout:    duration("RAWSYST_BACKUP_TIMEOUT", 30*time.Minute),
	}
	ctx := context.Background()

	// The register, so what happened is visible to a platform operator rather
	// than only to whoever read the terminal. Best-effort on purpose: a
	// database that cannot be written to must not stop a backup from being
	// taken — the backup is the thing that matters, and the row about it is
	// not.
	var register *backup.Register
	if pool, err := db.Open(ctx, cfg.DB); err == nil {
		defer pool.Close()
		register = backup.NewRegister(pool)
	}

	switch action {
	case "run":
		return doRun(ctx, opts, register, *jsonOut)
	case "list":
		return doList(ctx, opts, *jsonOut)
	case "verify":
		return doVerify(ctx, opts, register, *snapshot, *jsonOut)
	case "restore":
		return doRestore(ctx, opts, *snapshot, *into)
	case "prune":
		return doPrune(ctx, opts, *dryRun)
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
	asJSON bool,
) error {
	kind := "manual"
	if os.Getenv("INVOCATION_ID") != "" {
		// systemd sets this. A run from a timer is scheduled; a run somebody
		// typed is manual, and the register keeps them apart because "when did
		// a person last take one" is a different question from "is the timer
		// working".
		kind = "scheduled"
	}
	id, _ := register.Start(ctx, kind)

	res, err := backup.Run(ctx, opts)
	if err != nil {
		_ = register.Finish(ctx, id, "", "", 0, err.Error())
		return err
	}
	_ = register.Finish(ctx, id, res.Location, res.SHA256, res.Bytes, "")
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
	admin := os.Getenv("RAWSYST_BACKUP_ADMIN_DSN")
	if strings.TrimSpace(admin) == "" {
		return fmt.Errorf("set RAWSYST_BACKUP_ADMIN_DSN to a connection on " +
			"the same server as the database, pointing at a DIFFERENT " +
			"database — `postgres` will do. A scratch database cannot be " +
			"created from inside the one being created beside")
	}

	// The row this verification is about, found by where the snapshot went.
	recordID, _ := register.FindByLocation(ctx,
		opts.Store.Bucket()+"/"+opts.Prefix+"/"+id+"/database.dump")

	report, err := backup.Verify(ctx, opts, admin, id)
	if err != nil {
		// Recorded as a failed verification, and `verified_at` stays NULL. A
		// broken backup that stamped itself verified would read as protection.
		_ = register.Verified(ctx, recordID, err.Error())
		if asJSON {
			_ = print(report)
		}
		return err
	}
	_ = register.Verified(ctx, recordID, "")
	if asJSON {
		return print(report)
	}
	fmt.Printf("snapshot   %s\n", report.SnapshotID)
	for _, c := range report.Checked {
		fmt.Printf("  ok       %s\n", c)
	}
	fmt.Printf("schema     %d\n", report.Schema)
	fmt.Printf("tables     %d\n", report.Tables)
	for _, t := range sortedKeys(report.Rows) {
		fmt.Printf("  %-22s %d\n", t, report.Rows[t])
	}
	fmt.Printf("size       %s\n", human(report.Bytes))
	fmt.Printf("took       %s\n", report.Took)
	fmt.Printf("\nVERIFIED. It was restored into a temporary database, checked " +
		"and dropped. Production was not touched.\n")
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
	fmt.Printf("restored %s\n\nNow run `rawsyst migrate` against it: the "+
		"snapshot carries the schema it was taken at, and the migrator "+
		"applies anything this build added since. Then check it before "+
		"pointing anything at it.\n", id)
	return nil
}

func doPrune(ctx context.Context, opts backup.Options, dryRun bool) error {
	policy := backup.Policy{
		Daily:   number("RAWSYST_BACKUP_KEEP_DAILY", backup.DefaultPolicy.Daily),
		Weekly:  number("RAWSYST_BACKUP_KEEP_WEEKLY", backup.DefaultPolicy.Weekly),
		Monthly: number("RAWSYST_BACKUP_KEEP_MONTHLY", backup.DefaultPolicy.Monthly),
	}
	removed, err := backup.Prune(ctx, opts, policy, dryRun)
	if err != nil {
		return err
	}
	fmt.Printf("keeping %d daily, %d weekly, %d monthly, and always the newest\n",
		policy.Daily, policy.Weekly, policy.Monthly)
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

// resolve turns an empty snapshot argument into the newest completed one.
func resolve(ctx context.Context, opts backup.Options, id string) (string, error) {
	if strings.TrimSpace(id) != "" {
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

func usage() {
	fmt.Fprint(os.Stderr, `usage: rawsyst backup <action> [flags]

  run        take a backup: dump, upload, manifest, completion marker
  list       what is in the store, newest first
  verify     restore a snapshot into a temporary database and check it
  restore    restore a snapshot into a database you name with -into
  prune      remove snapshots outside the retention policy

Environment:
  RAWSYST_DB_DSN                 the database to dump
  RAWSYST_S3_ENDPOINT/BUCKET     where snapshots go
  RAWSYST_S3_ACCESS_KEY_ID       and its secret
  RAWSYST_BACKUP_PREFIX          namespace inside the bucket (rawsyst)
  RAWSYST_BACKUP_ADMIN_DSN       a connection for creating the scratch
                                 database a verification restores into
  RAWSYST_BACKUP_KEEP_DAILY      7
  RAWSYST_BACKUP_KEEP_WEEKLY     4
  RAWSYST_BACKUP_KEEP_MONTHLY    3
  RAWSYST_BACKUP_TIMEOUT         30m
  RAWSYST_BACKUP_TEMP_DIR        where a dump is staged

See deploy/server/BACKUP.md.
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

func number(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 0 {
		return fallback
	}
	return n
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

func sortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
