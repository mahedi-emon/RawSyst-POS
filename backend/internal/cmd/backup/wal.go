// The command line for the write-ahead log archive and point-in-time recovery.
//
// # Two of these commands are not for people
//
// `wal archive` and `wal restore` are what PostgreSQL itself runs, once per
// segment, as `archive_command` and `restore_command`. They have three
// properties the others do not need:
//
//   - They run in the PostgreSQL container, which has no application database,
//     no JWT secret and no data encryption keys. So they must not load the
//     product's configuration — `config.Load` would refuse to start and every
//     segment would fail to archive with a message about a missing signing key.
//     They are dispatched before it, exactly as `check` is.
//
//   - Their EXIT STATUS is the entire interface. Zero from `wal archive` tells
//     PostgreSQL the segment is safe somewhere else and it may recycle it, so
//     zero is only returned once the object and its record are both in the
//     bucket. Non-zero makes PostgreSQL keep the segment and try again, which
//     is the safe direction: a disk filling up is visible and recoverable, and
//     a green archive with a hole in it is neither.
//
//   - They must be quiet. PostgreSQL writes whatever they print to its own log,
//     once per segment, for ever. So success says nothing at all.
//
// # And one of them has a special failure
//
// `wal restore` is asked for one segment past the end of the archive on every
// single recovery — that is how PostgreSQL discovers it has replayed
// everything. So "not in the archive" exits 1 with one quiet line, and
// everything else — a store that will not answer, a checksum that does not
// match, the wrong key — exits 2 with the reason. A recovery that treated the
// second as the first would stop early and call itself finished.
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mahedi-emon/Biz1core/backend/internal/backup"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/config"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// Exit codes for the two commands PostgreSQL runs.
const (
	exitArchiveMissing = 1 // the ordinary end of every recovery
	exitArchiveFailed  = 2 // something that must stop a recovery
)

// runHook dispatches the two commands PostgreSQL runs, before any
// configuration is loaded. The second return says whether it handled the call.
func runHook(args []string) (bool, error) {
	if len(args) < 2 || args[0] != "wal" {
		return false, nil
	}
	switch args[1] {
	case "archive":
		return true, doWALArchive(args[2:])
	case "restore":
		return true, doWALRestore(args[2:])
	}
	return false, nil
}

// doWALArchive is `archive_command`.
func doWALArchive(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf(
			"usage: biz1core backup wal archive <path> <name>  " +
				"(PostgreSQL supplies %%p and %%f)")
	}
	path, name := args[0], args[1]

	opts, err := backup.WALOptionsFromEnv()
	if err != nil {
		return err
	}
	res, err := backup.ArchiveFile(context.Background(), opts, path, name)
	if err != nil {
		// Printed to stderr, which PostgreSQL copies into its own log. The
		// message is this product's own sentence; nothing from the object
		// store's URL reaches here, because `blob` never puts a signed URL in
		// an error.
		fmt.Fprintf(os.Stderr, "biz1core wal archive: %s\n", err.Error())
		os.Exit(exitArchiveFailed)
	}

	// Silence on success. One line per segment is a line a minute for ever in
	// a log somebody has to read during an incident. The counts live in
	// `pg_stat_archiver`, which PostgreSQL keeps anyway, and the sizes live in
	// the sidecar beside each object.
	_ = res
	return nil
}

// doWALRestore is `restore_command`.
func doWALRestore(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf(
			"usage: biz1core backup wal restore <name> <path>  " +
				"(PostgreSQL supplies %%f and %%p)")
	}
	name, path := args[0], args[1]

	opts, err := backup.WALOptionsFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "biz1core wal restore: %s\n", err.Error())
		os.Exit(exitArchiveFailed)
	}
	if _, err := backup.FetchFile(context.Background(), opts, name, path); err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			// The ordinary end of a recovery. One quiet line, because
			// PostgreSQL logs this for the last segment of every single
			// recovery and a paragraph here would read as a failure.
			fmt.Fprintf(os.Stderr, "%s is not in the archive\n", name)
			os.Exit(exitArchiveMissing)
		}
		fmt.Fprintf(os.Stderr, "biz1core wal restore: %s\n", err.Error())
		os.Exit(exitArchiveFailed)
	}
	return nil
}

// --- the commands people run ------------------------------------------------

// walOptions builds the archive settings for an interactive command.
//
// Unlike `WALOptionsFromEnv`, this one has the loaded configuration to hand, so
// it takes the store from there — which is the same five variables, read by the
// code that also validates them.
func walOptions(cfg config.Config, opts backup.Options) backup.WALOptions {
	size := int64(backup.DefaultWALSegmentSize)
	if v := strings.TrimSpace(os.Getenv("RAWSYST_WAL_SEGMENT_SIZE")); v != "" {
		if n, err := backup.ParseByteSize(v); err == nil && n > 0 {
			size = n
		}
	}
	return backup.WALOptions{
		Store:      opts.Store,
		Prefix:     opts.Prefix,
		Key:        opts.Key,
		Layout:     backup.WALLayout{SegmentSize: size},
		Timeout:    duration("RAWSYST_WAL_ARCHIVE_TIMEOUT", 2*time.Minute),
		Retries:    intEnv("RAWSYST_WAL_ARCHIVE_RETRIES", 3),
		SourceHost: opts.SourceHost,
		AppVersion: opts.AppVersion,
		TempDir:    opts.TempDir,
	}
}

// doWAL dispatches everything under `backup wal` that a person runs.
func doWAL(
	ctx context.Context, cfg config.Config, opts backup.Options,
	register *backup.WALRegister, args []string,
) error {
	if len(args) == 0 {
		walUsage()
		return errors.New("say which wal command")
	}
	action, rest := args[0], args[1:]

	fs := flag.NewFlagSet("backup wal "+action, flag.ExitOnError)
	deep := fs.Int("deep", 0,
		"download and check this many segments as well as listing them")
	apply := fs.Bool("apply", false,
		"actually delete, rather than saying what would be deleted")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	wal := walOptions(cfg, opts)

	switch action {
	case "status":
		return doWALStatus(ctx, cfg, wal, register, *asJSON)
	case "verify":
		return doWALVerify(ctx, wal, *deep, *asJSON)
	case "gaps":
		return doWALGaps(ctx, wal, *asJSON)
	case "prune":
		return doWALPrune(ctx, wal, register, *apply, *asJSON)
	case "preflight":
		return doWALPreflight(ctx, cfg, wal, opts, *asJSON)
	case "list":
		return doWALList(ctx, wal, *asJSON)
	default:
		walUsage()
		return fmt.Errorf("no such wal command %q", action)
	}
}

func doWALStatus(
	ctx context.Context, cfg config.Config, wal backup.WALOptions,
	register *backup.WALRegister, asJSON bool,
) error {
	observer := &backup.ArchiveObserver{
		DSN: env("RAWSYST_BACKUP_DSN", cfg.DB.DSN),
		WAL: wal, Register: register,
	}
	status, err := observer.Observe(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return print(status)
	}

	fmt.Printf("  %s  %s\n\n", strings.ToUpper(status.Health), status.Summary)
	fmt.Printf("  archiving            %v (wal_level=%s, timeline %d)\n",
		status.Archiving, status.WALLevel, status.Timeline)
	fmt.Printf("  last archived        %s\n", orNone(status.LastArchivedWAL))
	fmt.Printf("  last archived at     %s\n", stampOf(status.LastArchivedAt))
	fmt.Printf("  archived / failed    %d / %d\n",
		status.ArchivedCount, status.FailedCount)
	fmt.Printf("  lag                  %d segment(s), %ds\n",
		status.LagSegments, status.LagSeconds)
	fmt.Printf("  local pg_wal         %s\n", human(status.PGWALBytes))
	fmt.Printf("  archive              %d segment(s), %s, %d gap(s)\n",
		status.ArchiveSegments, human(status.ArchiveBytes), status.ArchiveGaps)
	fmt.Printf("  base backups         %d (newest %s)\n",
		status.BaseBackups, orNone(status.LatestBaseBackup))
	fmt.Printf("  recovery window      %s  ->  %s\n",
		stampOf(status.Window.Start), stampOf(status.Window.End))
	if status.Window.Truncated {
		fmt.Println("                       (cut short by a gap; replay stops " +
			"at the first missing segment)")
	}
	for _, f := range status.Findings {
		fmt.Printf("\n  - %s\n", f)
	}

	// Non-zero when it is not green, so this can be a check in whatever watches
	// the machine — the same contract `backup health` already has.
	if status.Health != backup.ArchiveGreen {
		return fmt.Errorf("the write-ahead log archive is %s", status.Health)
	}
	return nil
}

func doWALVerify(
	ctx context.Context, wal backup.WALOptions, deep int, asJSON bool,
) error {
	report, err := backup.VerifyArchive(ctx, wal, deep)
	if err != nil {
		return err
	}
	if asJSON {
		return print(report)
	}
	fmt.Printf("  %s\n\n", passFail(report.Passed))
	for _, c := range report.Checked {
		fmt.Printf("  checked   %s\n", c)
	}
	for _, f := range report.Findings {
		fmt.Printf("  FINDING   %s\n", f)
	}
	fmt.Printf("\n  %d segment(s), %s, in %ds\n",
		report.Segments, human(report.Bytes), report.TookSeconds)
	if !report.Passed {
		return errors.New("the archive did not check out")
	}
	return nil
}

func doWALGaps(ctx context.Context, wal backup.WALOptions, asJSON bool) error {
	inv, err := backup.ReadArchive(ctx, wal)
	if err != nil {
		return err
	}
	if asJSON {
		return print(inv)
	}
	for _, t := range inv.Timelines {
		fmt.Printf("  timeline %d   %d segment(s)  %s .. %s\n",
			t.Timeline, t.Segments, orNone(t.First), orNone(t.Last))
		if t.ContiguousTo != "" && t.ContiguousTo != t.Last {
			fmt.Printf("               replay can only reach %s\n",
				t.ContiguousTo)
		}
		for _, g := range t.Gaps {
			fmt.Printf("               GAP %s .. %s (%d missing)\n",
				g.From, g.To, g.Count)
		}
		for _, i := range t.Incomplete {
			fmt.Printf("               INCOMPLETE %s (no record beside it)\n", i)
		}
	}
	if inv.TotalGaps() > 0 {
		return fmt.Errorf("the archive has %d gap(s)", inv.TotalGaps())
	}
	fmt.Println("\n  No gaps.")
	return nil
}

func doWALPrune(
	ctx context.Context, wal backup.WALOptions,
	register *backup.WALRegister, apply, asJSON bool,
) error {
	report, err := backup.PruneWAL(ctx, wal, backup.PITRPolicyFromEnv(), !apply)
	if err != nil {
		return err
	}
	if apply && len(report.RemovedBaseBackups) > 0 && register != nil {
		_ = register.BaseExpired(ctx, report.RemovedBaseBackups)
	}
	if asJSON {
		return print(report)
	}
	if report.Refused != "" {
		fmt.Printf("  REFUSED   %s\n", report.Refused)
		return nil
	}
	what := "would remove"
	if apply {
		what = "removed"
	}
	fmt.Printf("  window               %d day(s)\n", report.WindowDays)
	fmt.Printf("  horizon              %s (from base backup %s)\n",
		report.Horizon, report.HorizonBaseBackup)
	fmt.Printf("  %-20s %d segment(s), %s\n",
		what, report.RemovedSegments, human(report.RemovedBytes))
	fmt.Printf("  kept                 %d segment(s), %s\n",
		report.KeptSegments, human(report.KeptBytes))
	if len(report.RemovedBaseBackups) > 0 {
		fmt.Printf("  base backups %s  %s\n",
			what, strings.Join(report.RemovedBaseBackups, ", "))
	}
	if report.OrphanSegments > 0 {
		fmt.Printf("  orphaned             %d segment(s) on a timeline no "+
			"retained base backup sits on\n", report.OrphanSegments)
	}
	if !apply {
		fmt.Println("\n  Nothing was deleted. Add -apply to do it.")
	}
	return nil
}

func doWALList(ctx context.Context, wal backup.WALOptions, asJSON bool) error {
	bases, err := backup.CompletedBaseBackups(ctx, wal)
	if err != nil {
		return err
	}
	if asJSON {
		return print(bases)
	}
	if len(bases) == 0 {
		fmt.Println("  No base backups.")
		return nil
	}
	for _, b := range bases {
		fmt.Printf("  %s  tl %d  %s .. %s  %s  %s\n",
			b.ID, b.Timeline, b.StartSegment, b.EndSegment,
			human(b.TotalBytes()), b.CompletedAt)
	}
	return nil
}

// doWALPreflight says whether this server could do any of this, before it is
// asked to.
//
// # Why this exists
//
// Every one of the four things it checks fails at a different moment and with a
// different unhelpful message. `wal_level=minimal` fails when somebody tries to
// recover, months later. A missing `replication` line in `pg_hba.conf` fails
// twenty minutes into the first base backup with a message about a host
// address. A bucket that cannot be reached fails once per segment, into a log
// nobody reads. Checking them together, on demand, before anything is
// committed, is the difference between a configuration problem and an incident.
func doWALPreflight(
	ctx context.Context, cfg config.Config, wal backup.WALOptions,
	opts backup.Options, asJSON bool,
) error {
	type check struct {
		Name string `json:"check"`
		OK   bool   `json:"ok"`
		Says string `json:"says"`
	}
	var checks []check
	add := func(name string, ok bool, says string) {
		checks = append(checks, check{Name: name, OK: ok, Says: says})
	}

	dsn := env("RAWSYST_BACKUP_DSN", cfg.DB.DSN)
	ready, err := backup.CheckBaseBackupReady(ctx, dsn)
	if err != nil {
		add("physical base backup", false, err.Error())
	} else {
		add("physical base backup", true, fmt.Sprintf(
			"%s may replicate; wal_level=%s, max_wal_senders=%d",
			ready.Role, ready.WALLevel, ready.MaxWALSenders))
	}
	if ready.ArchiveMode == "on" || ready.ArchiveMode == "always" {
		add("archive_mode", true, "on")
	} else {
		add("archive_mode", false,
			"archive_mode is "+orNone(ready.ArchiveMode)+
				". Nothing is being shipped off this machine.")
	}
	if ready.ArchiveCommand == "set" {
		add("archive_command", true, "set")
	} else {
		add("archive_command", false,
			"archive_command is empty, so archiving cannot work even with "+
				"archive_mode on.")
	}

	if !wal.Configured() {
		add("object store", false,
			"No object store is configured. Set RAWSYST_S3_ENDPOINT and "+
				"RAWSYST_S3_BUCKET.")
	} else if err := opts.Store.Ping(ctx); err != nil {
		add("object store", false, err.Error())
	} else {
		add("object store", true, opts.Store.Bucket()+" answered")
	}

	if opts.Key.Set() {
		add("encryption", true, "on, key "+opts.Key.Fingerprint())
	} else {
		add("encryption", true,
			"off. The archive is protected in transit by TLS and at rest by "+
				"whatever the provider does. Set "+
				"RAWSYST_BACKUP_ENCRYPTION_KEY to seal it here instead.")
	}

	if asJSON {
		return print(checks)
	}
	failed := 0
	for _, c := range checks {
		mark := "ok  "
		if !c.OK {
			mark, failed = "FAIL", failed+1
		}
		fmt.Printf("  %s  %-24s %s\n", mark, c.Name, c.Says)
	}
	if failed > 0 {
		return fmt.Errorf("%d preflight check(s) failed", failed)
	}
	return nil
}

// --- base backups and recoveries --------------------------------------------

func doBaseBackup(
	ctx context.Context, cfg config.Config, opts backup.Options,
	register *backup.WALRegister, args []string,
) error {
	fs := flag.NewFlagSet("backup basebackup", flag.ExitOnError)
	download := fs.Bool("download", false,
		"pull a base backup onto this computer instead of taking a new one")
	baseID := fs.String("base", "",
		"which base backup to download. Default: the newest completed one")
	to := fs.String("to", ".", "where to put a downloaded base backup")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	wal := walOptions(cfg, opts)
	if *download {
		return doBaseDownload(ctx, wal, *baseID, *to, *asJSON)
	}

	id, _ := register.StartBase(ctx, nil, "")

	manifest, err := backup.TakeBaseBackup(ctx, backup.BaseBackupOptions{
		DSN:            env("RAWSYST_BACKUP_DSN", cfg.DB.DSN),
		WAL:            wal,
		StagingDir:     env("RAWSYST_BACKUP_STAGING_DIR", opts.TempDir),
		MinFreePercent: intEnv("RAWSYST_BACKUP_MIN_FREE_PERCENT", 100),
		Timeout:        duration("RAWSYST_BASE_BACKUP_TIMEOUT", 2*time.Hour),
		AppVersion:     opts.AppVersion,
		GitCommit:      opts.GitCommit,
		Environment:    opts.Environment,
		SourceHost:     opts.SourceHost,
		Progress: func(stage string) {
			if !*asJSON {
				fmt.Printf("  %s\n", stage)
			}
		},
		Warn: func(msg string) {
			fmt.Fprintf(os.Stderr, "backup: %s\n", msg)
		},
	})
	if err != nil {
		_ = register.BaseFailed(ctx, id, err.Error())
		return err
	}
	_ = register.BaseStored(ctx, id, manifest)

	if *asJSON {
		return print(manifest)
	}
	fmt.Printf("\n  %s\n", manifest.ID)
	fmt.Printf("  timeline %d, %s .. %s\n",
		manifest.Timeline, manifest.StartLSN, manifest.EndLSN)
	fmt.Printf("  segments %s .. %s\n",
		manifest.StartSegment, manifest.EndSegment)
	fmt.Printf("  %s in %ds, encrypted %v\n",
		human(manifest.TotalBytes()), manifest.TookSeconds,
		manifest.Encryption != nil)
	fmt.Println("\n  Stored. NOT yet verified — `biz1core backup pitr " +
		"-target immediate -base " + manifest.ID + "` proves it recovers.")
	return nil
}

// doBaseDownload pulls a base backup onto this computer.
//
// # What this is for, and what it is not
//
// It is NOT the laptop copy. That is `backup download`, which fetches a
// `pg_dump` snapshot: portable across major versions, readable on any machine
// with a PostgreSQL, and the thing `RECOVERY.md` section C is built on. A
// physical base backup is none of those — it only reads on its own major
// version and it is useless without the write-ahead log archive beside it.
//
// It exists for the two situations where the alternative is being stuck:
// leaving a storage provider, and opening an artifact somewhere else when a
// recovery has failed on the server.
//
// # The files come down SEALED
//
// When encryption is on, what lands on disk is ciphertext, because that is what
// the store holds. Decrypting it needs the key from wherever the key is kept.
// The manifest that comes with it names the fingerprint, so there is no
// guessing about WHICH key — see `SECRETS.md`.
func doBaseDownload(
	ctx context.Context, wal backup.WALOptions, id, to string, asJSON bool,
) error {
	if id == "" {
		bases, err := backup.CompletedBaseBackups(ctx, wal)
		if err != nil {
			return err
		}
		if len(bases) == 0 {
			return errors.New(
				"there is no completed base backup to download")
		}
		id = bases[0].ID
	}

	// The manifest first, and not only to know the file names. It is what
	// proves the backup is COMPLETE: it is written after every component is in
	// the store. Downloading from one whose upload died would mean carrying a
	// truncated cluster away and discovering it later.
	manifest, err := backup.ReadBaseManifest(ctx, wal, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return fmt.Errorf("could not create %s: %w", to, err)
	}

	names := backup.BaseNamesFor(id)
	files := []struct{ object, local string }{
		{backup.BaseTarObject(), names.Base},
		{backup.BaseWALTarObject(), names.WAL},
		{backup.BasePGManifestObject(), names.PGManifest},
		{backup.BaseManifestObject(), names.Manifest},
	}

	var written []string
	var total int64
	for _, f := range files {
		if !asJSON {
			fmt.Printf("  %s\n", f.local)
		}
		n, err := fetchBaseFile(ctx, wal, id, f.object, filepath.Join(to, f.local))
		if err != nil {
			return err
		}
		written = append(written, f.local)
		total += n
	}

	if asJSON {
		return print(map[string]any{
			"base_backup_id": id,
			"files":          written,
			"bytes":          total,
			"encrypted":      manifest.Encryption != nil,
			"directory":      to,
		})
	}
	fmt.Printf("\n  %s, %s, into %s\n", id, human(total), to)
	if manifest.Encryption != nil {
		fmt.Printf("  SEALED with key %s. Without that key these files are "+
			"not a backup.\n", manifest.Encryption.KeyFingerprint)
	}
	fmt.Printf("  PostgreSQL %s. A physical copy only reads on its own major "+
		"version.\n", manifest.PostgresVersion)
	fmt.Println("  It also needs the write-ahead log archive to recover to " +
		"anything but its own consistency point. See deploy/server/PITR.md.")
	return nil
}

// fetchBaseFile streams one component onto disk, checking it on the way.
//
// Streamed and hashed as it goes: a base backup is the size of the database and
// nothing here holds one in memory. The checksum is compared against the
// manifest afterwards and a mismatch removes the file, because a corrupt
// artifact left on disk with a plausible name is worse than no artifact.
func fetchBaseFile(
	ctx context.Context, wal backup.WALOptions, id, object, dest string,
) (int64, error) {
	key, err := backup.BaseBackupKey(wal.Prefix, id, object)
	if err != nil {
		return 0, err
	}
	body, _, err := wal.Store.GetStream(ctx, key)
	if err != nil {
		return 0, err
	}
	defer body.Close()

	f, err := os.Create(dest)
	if err != nil {
		return 0, fmt.Errorf("could not write %s: %w", dest, err)
	}
	defer f.Close()

	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), body)
	if err != nil {
		os.Remove(dest)
		return 0, fmt.Errorf("could not write %s: %w", dest, err)
	}

	// The manifest describes the three components it lists. It does not
	// describe itself, so there is nothing to compare that one against.
	manifest, err := backup.ReadBaseManifest(ctx, wal, id)
	if err == nil {
		if c, ok := manifest.Component(object); ok && c.SHA256 != "" {
			if got := hex.EncodeToString(sum.Sum(nil)); got != c.SHA256 {
				os.Remove(dest)
				return 0, fmt.Errorf(
					"%s came down with the wrong checksum (%s, expected %s); "+
						"the file has been removed", object, got, c.SHA256)
			}
		}
	}
	return n, nil
}

func doPITR(
	ctx context.Context, cfg config.Config, opts backup.Options,
	register *backup.WALRegister, args []string,
) error {
	fs := flag.NewFlagSet("backup pitr", flag.ExitOnError)
	targetKind := fs.String("target", "latest",
		"latest, immediate, time, before_time, lsn, name or xid")
	at := fs.String("at", "",
		"the moment, as RFC3339 — 2026-09-11T14:32:00Z")
	value := fs.String("value", "",
		"the log position, restore point name or transaction id")
	baseID := fs.String("base", "",
		"which base backup to start from. Default: the newest that finished "+
			"before the target")
	keep := fs.Bool("keep", false,
		"leave the recovered cluster running so it can be connected to")
	window := fs.Bool("window", false,
		"print the recovery window and do nothing else")
	jsonFlag := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	asJSON := *jsonFlag

	wal := walOptions(cfg, opts)

	if *window {
		w, err := backup.ComputeRecoveryWindow(ctx, wal)
		if err != nil {
			return err
		}
		if asJSON {
			return print(w)
		}
		if !w.Available {
			fmt.Printf("  No recovery window.\n\n  %s\n", w.Because)
			return errors.New("no recovery window")
		}
		fmt.Printf("  %s  ->  %s\n", w.Start.Format(time.RFC3339),
			w.End.Format(time.RFC3339))
		fmt.Printf("  timeline %d, %d base backup(s), newest %s\n",
			w.Timeline, w.BaseBackups, w.LatestBaseBackup)
		if w.Truncated {
			fmt.Println("  The archive holds newer segments, and a gap before " +
				"them means replay cannot reach them.")
		}
		return nil
	}

	target := backup.RecoveryTarget{Kind: *targetKind, Value: *value}
	if *at != "" {
		moment, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			return fmt.Errorf(
				"-at must be RFC3339, like 2026-09-11T14:32:00Z: %w", err)
		}
		target.At = moment.UTC()
	}
	if err := target.Validate(); err != nil {
		return err
	}

	auditID, _ := register.StartRecovery(
		ctx, "isolated", *baseID, target, nil, "")

	report, err := backup.RunPITR(ctx, backup.PITROptions{
		WAL:          wal,
		BaseBackupID: *baseID,
		Target:       target,
		WorkDir:      env("RAWSYST_BACKUP_STAGING_DIR", opts.TempDir),
		BinDir:       os.Getenv("RAWSYST_POSTGRES_BIN"),
		Keep:         *keep,
		Timeout:      duration("RAWSYST_PITR_TIMEOUT", 4*time.Hour),
		// This command runs on a server recovering its own cluster.
		ExpectProduct: true,
		Progress: func(stage string) {
			if !asJSON {
				fmt.Printf("  %s\n", stage)
			}
		},
	})
	if err != nil {
		if errs.CodeOf(err) == errs.CodeInvalidInput {
			_ = register.RefuseRecovery(ctx, auditID, err.Error())
		} else {
			_ = register.FinishRecovery(ctx, auditID, report, err)
		}
		return err
	}
	_ = register.FinishRecovery(ctx, auditID, report, nil)

	if asJSON {
		return print(report)
	}
	fmt.Printf("\n  %s\n\n", passFail(report.Passed))
	fmt.Printf("  asked for            %s\n", report.TargetMeaning)
	fmt.Printf("  base backup          %s (%s)\n",
		report.BaseBackupID, report.BaseBackupAt)
	fmt.Printf("  replay stopped at    %s  (%s)\n",
		orNone(report.ReachedTime), orNone(report.ReachedLSN))
	fmt.Printf("  timeline             %d\n", report.Timeline)
	if report.Inventory != nil {
		fmt.Printf("  recovered            %d tables, schema %d, %d business(es)\n",
			report.Inventory.TableCount(), report.Inventory.SchemaVersion,
			len(report.Inventory.TenantRows))
	}
	fmt.Printf("  took                 %ds (download %ds, replay %ds)\n",
		report.TotalSeconds, report.DownloadSeconds, report.ReplaySeconds)
	for _, c := range report.Checked {
		fmt.Printf("  checked   %s\n", c)
	}
	for _, f := range report.Findings {
		fmt.Printf("  FINDING   %s\n", f)
	}
	if report.DSN != "" {
		fmt.Printf("\n  Still running. Connect with:\n    psql %q\n", report.DSN)
		fmt.Println("  Stop it with `pg_ctl -D " + report.DataDir + " stop`.")
	}
	if !report.Passed {
		return errors.New("the recovery did not check out")
	}
	return nil
}

// --- small printers ---------------------------------------------------------

func walUsage() {
	fmt.Fprint(os.Stderr, `
  biz1core backup wal <command>

    status      whether the archive is working, and the recovery window
    verify      check the archive; -deep N also downloads N segments
    gaps        every timeline, every hole
    prune       remove what nothing can still need; -apply to do it
    preflight   whether this server could archive and recover at all
    list        the physical base backups

    archive <path> <name>   run by PostgreSQL as archive_command
    restore <name> <path>   run by PostgreSQL as restore_command

`)
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

func stampOf(t time.Time) string {
	if t.IsZero() {
		return "(never)"
	}
	return t.UTC().Format(time.RFC3339)
}

func passFail(ok bool) string {
	if ok {
		return "PASSED"
	}
	return "FAILED"
}

// jsonOf is used by the tests that check these commands print machine-readable
// output; keeping it here rather than inlining `json.Marshal` means one place
// decides the shape.
func jsonOf(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
