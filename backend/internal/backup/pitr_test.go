// The drill, performed rather than described.
//
// # What this actually does
//
// Builds a PostgreSQL cluster from nothing with `initdb`, turns on continuous
// archiving pointed at an in-process object store, writes known rows, takes a
// physical base backup, writes more rows, DELETES them, archives the log, and
// then recovers the cluster twice: once to the moment before the deletion, and
// once to the end of the archive. The first must have the rows. The second must
// not.
//
// That pair is the whole claim. Everything else in this subsystem — the
// sidecars, the gap arithmetic, the recovery window, the retention horizon — is
// in service of being able to do exactly this on a day when it matters, and a
// test that stopped short of doing it would be a test of the scaffolding.
//
// # Why it does not mock PostgreSQL
//
// Because the things that go wrong are PostgreSQL's semantics, not this
// product's: whether `recovery_target_time` is inclusive, whether a target
// before the consistency point is a refusal or a silent early stop, what
// happens when the archive runs out before the target is reached, which
// timeline a recovery follows by default. None of those can be learnt from a
// mock, and every one of them is a way to hand somebody the wrong Tuesday.
//
// # Why it skips rather than fails without PostgreSQL
//
// `initdb`, `pg_ctl`, `postgres` and `pg_basebackup` have to be on the PATH, of
// the same major version as each other. Inside the backup image they are, by
// construction. On a developer's machine they may not be, and a suite that
// could not run at all on a laptop would be a suite that stops being run.
// `make test-backend` inside the container is where this is expected to pass;
// the skip message says exactly what is missing.
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestArchiveHelperProcess is not a test. It is the program PostgreSQL runs as
// `archive_command` and `restore_command` during the drill below.
//
// # Why the test binary is its own archiver
//
// `archive_command` needs an executable on disk. Building one would mean a
// `go build` inside a test, a temporary directory, a different answer on
// Windows, and a binary that is not the code under test. Re-entering this
// binary costs none of that: the archiver that runs is compiled from the same
// source as the assertions, so a change that breaks archiving breaks the drill
// rather than passing against a stale copy.
//
// It exits rather than failing, because its exit status is the entire interface
// PostgreSQL reads. See the package note in walarchive.go.
func TestArchiveHelperProcess(t *testing.T) {
	if os.Getenv("RAWSYST_TEST_WAL_HELPER") != "1" {
		t.Skip("not a helper invocation")
	}
	args := helperArgs()
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "helper: mode, name and path are required")
		os.Exit(2)
	}
	opts, err := WALOptionsFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper: %v\n", err)
		os.Exit(2)
	}
	opts.Retries = 1

	switch args[0] {
	case "archive":
		// args: archive <%p> <%f>
		if _, err := ArchiveFile(context.Background(), opts, args[1], args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "helper archive: %v\n", err)
			os.Exit(1)
		}
	case "restore":
		// args: restore <%f> <%p>
		if _, err := FetchFile(context.Background(), opts, args[1], args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "helper restore: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "helper: no mode called %q\n", args[0])
		os.Exit(2)
	}
	os.Exit(0)
}

// helperArgs is everything after the `--` the drill puts on the command line.
func helperArgs() []string {
	for i, a := range os.Args {
		if a == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

// --- the cluster the drill runs against -------------------------------------

// sourceCluster is a PostgreSQL built for one test and thrown away.
type sourceCluster struct {
	dir  string
	data string
	port int
	log  string
	env  []string
}

// needPostgres skips the test unless a complete set of tools is present.
func needPostgres(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"initdb", "pg_ctl", "postgres", "pg_basebackup"} {
		if _, err := exec.LookPath(toolPath("", tool)); err != nil {
			t.Skipf("point-in-time recovery cannot be exercised here: %s is "+
				"not on the PATH. Inside the backup image it is; on a "+
				"developer machine, install the PostgreSQL client and server "+
				"tools of the same major version as the database.", tool)
		}
	}
}

// startSource builds a cluster, turns archiving on, and starts it.
func startSource(t *testing.T, f *fakeStore, prefix string) *sourceCluster {
	t.Helper()

	dir := t.TempDir()
	data := filepath.Join(dir, "data")

	pwFile := filepath.Join(dir, "pw")
	if err := os.WriteFile(pwFile, []byte("drill\n"), 0o600); err != nil {
		t.Fatalf("writing the superuser password file: %v", err)
	}

	// --auth=trust and a loopback listener: this cluster exists for one test,
	// on one machine, and reachable only from it.
	out, err := exec.Command(toolPath("", "initdb"),
		"--pgdata="+data,
		"--username=postgres",
		"--auth=trust",
		"--encoding=UTF8",
		// The drill writes a few hundred rows. Waiting for them to reach a
		// platter costs more than the whole rest of the test.
		"--no-sync",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("initdb failed: %v\n%s", err, out)
	}

	c := &sourceCluster{
		dir:  dir,
		data: data,
		port: freeTestPort(t),
		log:  filepath.Join(dir, "source.log"),
	}

	// The archiver is this binary, re-entered. See TestArchiveHelperProcess.
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("this test binary does not know where it is: %v", err)
	}
	archive := fmt.Sprintf(
		`%q -test.run=TestArchiveHelperProcess -- archive %%p %%f`, self)

	conf := strings.Join([]string{
		"listen_addresses = '127.0.0.1'",
		"port = " + strconv.Itoa(c.port),
		"wal_level = replica",
		"archive_mode = on",
		"archive_command = " + quoteConf(archive),
		// Small, so the drill produces several segments without writing a
		// gigabyte. Two seconds so the log written between two assertions
		// reaches the archive while the test is still running.
		"archive_timeout = 2",
		"max_wal_senders = 4",
		"wal_keep_size = 64MB",
		"max_connections = 20",
		"shared_buffers = 32MB",
		"fsync = off",
		"full_page_writes = off",
		"synchronous_commit = off",
		"log_min_messages = 'warning'",
		"logging_collector = off",
	}, "\n") + "\n"

	if err := os.WriteFile(
		filepath.Join(data, "postgresql.auto.conf"), []byte(conf), 0o600,
	); err != nil {
		t.Fatalf("writing the source cluster settings: %v", err)
	}

	// The environment the archiver inherits. `pg_ctl` passes its own to the
	// postmaster, and the postmaster passes that to archive_command.
	c.env = append(os.Environ(),
		"RAWSYST_TEST_WAL_HELPER=1",
		"RAWSYST_S3_ENDPOINT="+f.server.URL,
		"RAWSYST_S3_BUCKET="+f.bucket,
		"RAWSYST_S3_REGION=us-east-1",
		"RAWSYST_S3_ACCESS_KEY_ID=test",
		"RAWSYST_S3_SECRET_ACCESS_KEY=test",
		"RAWSYST_S3_PATH_STYLE=true",
		"RAWSYST_BACKUP_PREFIX="+prefix,
		"RAWSYST_BACKUP_ENCRYPTION_KEY=",
	)

	start := exec.Command(toolPath("", "pg_ctl"),
		"-D", data, "-l", c.log, "-w", "-t", "120", "start")
	start.Env = c.env
	// Through a file, never a pipe. The postmaster inherits the handle and
	// outlives pg_ctl, so a pipe makes this wait for the DATABASE to exit.
	// See runDetaching, which exists because this test found that.
	startOut, startErr := runDetaching(
		start, filepath.Join(dir, "pg_ctl-start.out"))
	if startErr != nil {
		t.Fatalf("the source cluster would not start: %v\n%s\n%s",
			startErr, startOut, tailFile(c.log))
	}
	t.Cleanup(func() {
		stop := exec.Command(toolPath("", "pg_ctl"),
			"-D", data, "-m", "immediate", "-w", "-t", "60", "stop")
		stop.Env = c.env
		_ = stop.Run()
	})
	return c
}

func (c *sourceCluster) dsn() string {
	return fmt.Sprintf(
		"postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", c.port)
}

func (c *sourceCluster) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), c.dsn())
	if err != nil {
		t.Fatalf("connecting to the source cluster: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("on the source cluster: %v\n%s", err, sql)
	}
}

func (c *sourceCluster) now(t *testing.T) time.Time {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), c.dsn())
	if err != nil {
		t.Fatalf("connecting to the source cluster: %v", err)
	}
	defer conn.Close(context.Background())

	// The SERVER's clock, not this process's. They are the same machine here
	// and would not be in production, and a drill that quietly depended on
	// them agreeing would not catch the day they stopped.
	var at time.Time
	if err := conn.QueryRow(context.Background(),
		`SELECT clock_timestamp()`).Scan(&at); err != nil {
		t.Fatalf("reading the server clock: %v", err)
	}
	return at.UTC()
}

// archiveEverything forces the current segment closed and waits for the
// archive to catch up.
//
// Without this the log holding the last few statements is still open, and a
// recovery would stop just before them — which is a true result and not the one
// the drill is trying to establish.
func (c *sourceCluster) archiveEverything(t *testing.T) string {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), c.dsn())
	if err != nil {
		t.Fatalf("connecting to the source cluster: %v", err)
	}
	defer conn.Close(context.Background())

	var switched string
	if err := conn.QueryRow(context.Background(),
		`SELECT pg_walfile_name(pg_switch_wal())`).Scan(&switched); err != nil {
		t.Fatalf("switching the write-ahead log: %v", err)
	}

	began := time.Now()
	deadline := began.Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var last *string
		var failed int64
		if err := conn.QueryRow(context.Background(),
			`SELECT last_archived_wal, failed_count FROM pg_stat_archiver`).
			Scan(&last, &failed); err != nil {
			t.Fatalf("reading pg_stat_archiver: %v", err)
		}
		if last != nil && *last >= switched {
			// The archive lag, measured rather than asserted. It is the number
			// the recovery point objective is made of, and printing it is how
			// deploy/server/PITR.md gets a figure that came from a run rather
			// than from an estimate.
			t.Logf("MEASURED archive lag: %s reached the store %s after the "+
				"log was switched", *last,
				time.Since(began).Round(time.Millisecond))
			return *last
		}
		if failed > 0 {
			t.Fatalf("archiving failed %d time(s); PostgreSQL said:\n%s",
				failed, tailFile(c.log))
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("the archive never caught up to %s.\n%s", switched, tailFile(c.log))
	return ""
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func tailFile(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	const keep = 4 << 10
	if len(body) > keep {
		body = body[len(body)-keep:]
	}
	return string(body)
}

// restoreCommandFor is the same helper, in the other direction.
func restoreCommandFor(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("this test binary does not know where it is: %v", err)
	}
	return fmt.Sprintf(
		`%q -test.run=TestArchiveHelperProcess -- restore %%f %%p`, self)
}

// pitrEnvFor makes the recovered instance's restore_command able to reach the
// fake store, by putting the settings in THIS process's environment — which the
// recovered postmaster inherits, because it is started as a child of it.
func pitrEnvFor(t *testing.T, f *fakeStore, prefix string) {
	t.Helper()
	for k, v := range map[string]string{
		"RAWSYST_TEST_WAL_HELPER":      "1",
		"RAWSYST_S3_ENDPOINT":          f.server.URL,
		"RAWSYST_S3_BUCKET":            f.bucket,
		"RAWSYST_S3_REGION":            "us-east-1",
		"RAWSYST_S3_ACCESS_KEY_ID":     "test",
		"RAWSYST_S3_SECRET_ACCESS_KEY": "test",
		"RAWSYST_S3_PATH_STYLE":        "true",
		"RAWSYST_BACKUP_PREFIX":        prefix,
	} {
		t.Setenv(k, v)
	}
}

// --- the drill --------------------------------------------------------------

func TestPointInTimeRecoveryGetsBackTheMomentBeforeTheMistake(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("the drill builds two clusters and replays a log; not short")
	}

	f := newFakeStore(t)
	const prefix = "drill"
	pitrEnvFor(t, f, prefix)

	source := startSource(t, f, prefix)

	// Something to lose.
	source.exec(t, `CREATE TABLE sale (id int PRIMARY KEY, total numeric)`)
	source.exec(t, `INSERT INTO sale SELECT g, g * 10 FROM generate_series(1, 50) g`)
	source.archiveEverything(t)

	// The physical copy. Everything after this is replay.
	opts := f.walOptions()
	opts.Prefix = prefix
	baseBegan := time.Now()
	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN:            source.dsn(),
		WAL:            opts,
		StagingDir:     t.TempDir(),
		MinFreePercent: 1,
		Timeout:        10 * time.Minute,
		AppVersion:     "drill",
	})
	if err != nil {
		t.Fatalf("the base backup failed: %v", err)
	}
	if base.Timeline == 0 || base.EndSegment == "" {
		t.Fatalf("the base backup does not say where in the log it sits: %+v", base)
	}
	t.Logf("MEASURED base backup: %s of cluster stored as %d bytes in %s",
		humanBytes(base.ClusterSize), base.TotalBytes(),
		time.Since(baseBegan).Round(time.Millisecond))

	// A trading day.
	source.exec(t, `INSERT INTO sale SELECT g, g * 10 FROM generate_series(51, 120) g`)
	source.archiveEverything(t)

	// The moment everything was still right, read off the SERVER's clock.
	good := source.now(t)

	// Two seconds, because `recovery_target_time` resolves against commit
	// timestamps and a target inside the same second as the mistake is a
	// target that may or may not include it. A drill that was flaky about
	// exactly this would be worse than no drill.
	time.Sleep(2 * time.Second)

	// The mistake.
	source.exec(t, `DELETE FROM sale WHERE id > 100`)
	source.exec(t, `INSERT INTO sale VALUES (999, 0)`)
	source.archiveEverything(t)

	// --- the recovery window ------------------------------------------------

	window, err := ComputeRecoveryWindow(context.Background(), opts)
	if err != nil {
		t.Fatalf("computing the recovery window: %v", err)
	}
	if !window.Available {
		t.Fatalf("no recovery window after a base backup and an archive: %s",
			window.Because)
	}
	if !window.Covers(good) {
		t.Fatalf("the moment before the mistake (%s) is outside the window "+
			"%s..%s", good.Format(time.RFC3339),
			window.Start.Format(time.RFC3339), window.End.Format(time.RFC3339))
	}
	if len(window.Gaps) != 0 {
		t.Errorf("a freshly written archive has gaps in it: %+v", window.Gaps)
	}

	// --- back to just before the mistake ------------------------------------

	before, err := RunPITR(context.Background(), PITROptions{
		WAL:            opts,
		Target:         RecoveryTarget{Kind: TargetTime, At: good},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		Keep:           true,
		AppDatabase:    "postgres",
		SuperUser:      "postgres",
		Timeout:        10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("recovering to the moment before the mistake failed: %v", err)
	}
	t.Cleanup(func() { stopRecovered(before) })

	t.Logf("MEASURED recovery to a moment: %ds total — %ds fetching and "+
		"unpacking, %ds replaying; replay stopped at %s",
		before.TotalSeconds, before.DownloadSeconds, before.ReplaySeconds,
		before.ReachedTime)

	rows := countRows(t, before.DSN, `SELECT count(*) FROM sale`)
	if rows != 120 {
		t.Errorf("recovered to %s and found %d sales, want 120 — the "+
			"deletion should not have been replayed",
			before.ReachedTime, rows)
	}
	if countRows(t, before.DSN, `SELECT count(*) FROM sale WHERE id = 999`) != 0 {
		t.Error("a row written AFTER the recovery target is in the result")
	}
	if before.BaseBackupID != base.ID {
		t.Errorf("recovered from %s, want the base backup just taken (%s)",
			before.BaseBackupID, base.ID)
	}

	// --- and forward to everything there is ---------------------------------

	latest, err := RunPITR(context.Background(), PITROptions{
		WAL:            opts,
		Target:         RecoveryTarget{Kind: TargetLatest},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		Keep:           true,
		AppDatabase:    "postgres",
		SuperUser:      "postgres",
		Timeout:        10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("recovering to the latest point failed: %v", err)
	}
	t.Cleanup(func() { stopRecovered(latest) })

	if got := countRows(t, latest.DSN, `SELECT count(*) FROM sale`); got != 101 {
		t.Errorf("recovered to the latest point and found %d sales, want 101 "+
			"— the deletion and the later insert should both be there", got)
	}
	if countRows(t, latest.DSN, `SELECT count(*) FROM sale WHERE id = 999`) != 1 {
		t.Error("the row written after the deletion is missing from the " +
			"latest recovery")
	}
	t.Logf("MEASURED recovery to the latest point: %ds total — %ds fetching "+
		"and unpacking, %ds replaying",
		latest.TotalSeconds, latest.DownloadSeconds, latest.ReplaySeconds)
}

func TestRecoveringToTheBaseBackupItselfIsTheCheapestProofItWorks(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "immediate"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)

	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.exec(t, `INSERT INTO thing SELECT generate_series(1, 10)`)
	source.archiveEverything(t)

	opts := f.walOptions()
	opts.Prefix = prefix
	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("base backup: %v", err)
	}

	// Written after the backup, so it must NOT appear: `immediate` stops at
	// the consistency point and replays nothing after it.
	source.exec(t, `INSERT INTO thing VALUES (999)`)
	source.archiveEverything(t)

	report, err := RunPITR(context.Background(), PITROptions{
		WAL:            opts,
		BaseBackupID:   base.ID,
		Target:         RecoveryTarget{Kind: TargetImmediate},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		Keep:           true,
		AppDatabase:    "postgres",
		SuperUser:      "postgres",
		Timeout:        10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("recovering to the base backup itself failed: %v", err)
	}
	t.Cleanup(func() { stopRecovered(report) })

	if got := countRows(t, report.DSN, `SELECT count(*) FROM thing`); got != 10 {
		t.Errorf("an immediate recovery found %d rows, want 10", got)
	}
	if !report.Passed {
		t.Errorf("the recovery did not pass: %v", report.Findings)
	}
}

func TestAMomentOutsideTheArchiveIsRefusedBeforeAnythingIsDownloaded(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "outside"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)
	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.archiveEverything(t)

	opts := f.walOptions()
	opts.Prefix = prefix
	if _, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	}); err != nil {
		t.Fatalf("base backup: %v", err)
	}

	// A year ago: before any base backup exists, so there is nothing to replay
	// onto. This must be a refusal in a second, not an hour of downloading.
	began := time.Now()
	_, err := RunPITR(context.Background(), PITROptions{
		WAL: opts,
		Target: RecoveryTarget{
			Kind: TargetTime, At: time.Now().Add(-365 * 24 * time.Hour),
		},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		Timeout:        10 * time.Minute,
	})
	if err == nil {
		t.Fatal("a moment before the recovery window was accepted")
	}
	if !strings.Contains(err.Error(), "before the recovery window") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if took := time.Since(began); took > 30*time.Second {
		t.Errorf("the refusal took %s; it should not have downloaded anything",
			took)
	}
}

func TestAMissingSegmentStopsARecoveryRatherThanShorteningItSilently(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "hole"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)

	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.archiveEverything(t)

	opts := f.walOptions()
	opts.Prefix = prefix
	if _, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	}); err != nil {
		t.Fatalf("base backup: %v", err)
	}

	source.exec(t, `INSERT INTO thing SELECT generate_series(1, 100)`)
	source.archiveEverything(t)
	source.exec(t, `INSERT INTO thing SELECT generate_series(101, 200)`)
	source.archiveEverything(t)
	source.exec(t, `INSERT INTO thing SELECT generate_series(201, 300)`)
	source.archiveEverything(t)
	target := source.now(t)

	// Take a segment out of the MIDDLE of the run, which is what a retention
	// bug or a fat-fingered delete produces.
	//
	// The middle matters. Removing the LAST segment leaves an archive that is
	// merely shorter — there is no hole between two segments and nothing is
	// unreachable that was reachable before. Removing one from the middle
	// strands everything behind it, and that is the failure worth proving is
	// detected.
	archived := archivedSegments(t, f, prefix, 1)
	if len(archived) < 3 {
		t.Fatalf("only %d segments were archived; the drill needs three to "+
			"have a middle: %v", len(archived), archived)
	}
	hole := archived[len(archived)/2]
	key, err := WALSegmentKey(prefix, hole)
	if err != nil {
		t.Fatal(err)
	}
	f.remove(key)
	f.remove(WALMetaKey(key))

	// The cheap check must see it, from the listing alone.
	report, err := VerifyArchive(context.Background(), opts, 0)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Passed {
		t.Fatalf("an archive with %s missing from the middle passed", hole)
	}
	if len(report.Gaps) != 1 || report.Gaps[0].From != hole {
		t.Errorf("the gap was reported as %+v, want one at %s",
			report.Gaps, hole)
	}

	// And the window must stop at the hole rather than at the newest object.
	window, err := ComputeRecoveryWindow(context.Background(), opts)
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if !window.Truncated {
		t.Error("the recovery window was not reported as cut short by the gap")
	}

	// And a recovery aimed past the hole must FAIL rather than stop early and
	// call itself finished.
	_, err = RunPITR(context.Background(), PITROptions{
		WAL:            opts,
		Target:         RecoveryTarget{Kind: TargetTime, At: target},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		AppDatabase:    "postgres",
		SuperUser:      "postgres",
		Timeout:        10 * time.Minute,
	})
	if err == nil {
		t.Fatal("a recovery past a missing segment reported success")
	}
}

func TestACorruptedBaseBackupIsRefusedRatherThanRecovered(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "corrupt"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)
	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.archiveEverything(t)

	opts := f.walOptions()
	opts.Prefix = prefix
	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("base backup: %v", err)
	}

	key, err := BaseBackupKey(prefix, base.ID, baseTarObject)
	if err != nil {
		t.Fatal(err)
	}
	f.corrupt(key)

	_, err = RunPITR(context.Background(), PITROptions{
		WAL:            opts,
		BaseBackupID:   base.ID,
		Target:         RecoveryTarget{Kind: TargetImmediate},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		AppDatabase:    "postgres",
		SuperUser:      "postgres",
		Timeout:        10 * time.Minute,
	})
	if err == nil {
		t.Fatal("a corrupted base backup was recovered from")
	}
}

func TestAnIncompleteBaseBackupIsNotOfferedAsARecoverySource(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "incomplete"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)
	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.archiveEverything(t)

	opts := f.walOptions()
	opts.Prefix = prefix
	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("base backup: %v", err)
	}

	// Remove the completion marker, which is what an interrupted upload leaves
	// behind. The bytes may all be there and it is still not a backup.
	marker, err := BaseBackupKey(prefix, base.ID, completedMark)
	if err != nil {
		t.Fatal(err)
	}
	f.remove(marker)

	completed, err := CompletedBaseBackups(context.Background(), opts)
	if err != nil {
		t.Fatalf("listing base backups: %v", err)
	}
	for _, b := range completed {
		if b.ID == base.ID {
			t.Fatal("a base backup with no completion marker was offered as a " +
				"recovery source")
		}
	}

	window, err := ComputeRecoveryWindow(context.Background(), opts)
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if window.Available {
		t.Error("a recovery window was offered with no completed base backup")
	}
}

func TestABaseBackupCanBeCarriedAwayAndStillChecksOut(t *testing.T) {
	// The download path, end to end: every component addressable by name, every
	// one coming back byte for byte, and the manifest able to prove it.
	//
	// A physical copy is deliberately the LESS portable half — it reads only on
	// its own major version and is useless without the archive — so what this
	// holds to is narrower than the dump equivalent. It is that an operator
	// leaving a storage provider, or opening an artifact somewhere else after a
	// recovery failed, gets the real bytes and a way to tell.
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "carry"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)
	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.exec(t, `INSERT INTO thing SELECT generate_series(1, 40)`)
	source.archiveEverything(t)

	opts := f.walOptions()
	opts.Prefix = prefix
	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("base backup: %v", err)
	}

	// Every component the manifest lists must be addressable and must come back
	// matching the checksum recorded for it.
	for _, name := range []string{
		BaseTarObject(), BaseWALTarObject(), BasePGManifestObject(),
	} {
		key, err := BaseBackupKey(prefix, base.ID, name)
		if err != nil {
			t.Fatalf("%s has no object key: %v", name, err)
		}
		body, err := opts.Store.Get(context.Background(), key)
		if err != nil {
			t.Fatalf("%s could not be downloaded: %v", name, err)
		}
		component, ok := base.Component(name)
		if !ok {
			t.Fatalf("the manifest does not describe %s", name)
		}
		if got := hexSHA256(body); got != component.SHA256 {
			t.Errorf("%s came down as %s, the manifest says %s",
				name, got, component.SHA256)
		}
		if int64(len(body)) != component.Bytes {
			t.Errorf("%s is %d bytes, the manifest says %d",
				name, len(body), component.Bytes)
		}
	}

	// The manifest itself is downloadable too, and is the one thing that is
	// never encrypted: it is what tells somebody holding the other files WHICH
	// key they need, so sealing it would make it useless for that.
	key, err := BaseBackupKey(prefix, base.ID, BaseManifestObject())
	if err != nil {
		t.Fatal(err)
	}
	body, err := opts.Store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("the manifest could not be downloaded: %v", err)
	}
	if IsSealed(body) {
		t.Error("the manifest is encrypted, so nobody holding the files can " +
			"find out which key opens them")
	}

	// And nothing else is addressable. The four names are a fixed switch; a
	// request for anything else must not become a path in somebody's bucket.
	for _, bad := range []string{
		"../../etc/passwd", "postgresql.conf", "", "..",
		"base.tar.gz/../../../secret",
	} {
		if _, err := BaseBackupKey(prefix, base.ID, bad); err == nil {
			t.Errorf("%q was turned into an object key", bad)
		}
	}
	for _, badID := range []string{"../other", "", "a/b"} {
		if _, err := BaseBackupKey(prefix, badID, BaseTarObject()); err == nil {
			t.Errorf("base backup id %q was turned into an object key", badID)
		}
	}

	// The local file names say what each file is and which backup it came from,
	// so a folder holding several does not become a puzzle.
	names := BaseNamesFor(base.ID)
	for _, n := range []string{
		names.Base, names.WAL, names.Manifest, names.PGManifest,
	} {
		if !strings.Contains(n, base.ID) {
			t.Errorf("%q does not name the backup it came from", n)
		}
	}
}

func TestAnEncryptedArchiveRecoversWithTheKeyAndNotWithout(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "sealed"
	pitrEnvFor(t, f, prefix)
	t.Setenv("RAWSYST_BACKUP_ENCRYPTION_KEY", testKeyB64(9))

	source := startSource(t, f, prefix)
	// The source's archiver reads the key from the environment it was started
	// with, which `startSource` copied from this process AFTER the Setenv
	// above.
	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.exec(t, `INSERT INTO thing SELECT generate_series(1, 25)`)
	source.archiveEverything(t)

	key, err := ParseKey(testKeyB64(9))
	if err != nil {
		t.Fatal(err)
	}
	opts := f.walOptions()
	opts.Prefix = prefix
	opts.Key = key

	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("base backup: %v", err)
	}
	if base.Encryption == nil {
		t.Fatal("the base backup was not sealed even though a key was configured")
	}

	// The wrong key is a different sentence from a corrupt backup.
	wrong := opts
	other, err := ParseKey(testKeyB64(8))
	if err != nil {
		t.Fatal(err)
	}
	wrong.Key = other
	_, err = RunPITR(context.Background(), PITROptions{
		WAL: wrong, BaseBackupID: base.ID,
		Target:         RecoveryTarget{Kind: TargetImmediate},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		AppDatabase:    "postgres", SuperUser: "postgres",
		Timeout: 10 * time.Minute,
	})
	if err == nil {
		t.Fatal("a sealed base backup was opened with the wrong key")
	}
	if !strings.Contains(err.Error(), "wrong key") {
		t.Errorf("the refusal says %v", err)
	}

	// And with the right one it recovers.
	report, err := RunPITR(context.Background(), PITROptions{
		WAL: opts, BaseBackupID: base.ID,
		Target:         RecoveryTarget{Kind: TargetImmediate},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		Keep:           true,
		AppDatabase:    "postgres", SuperUser: "postgres",
		Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("recovering a sealed base backup with the right key: %v", err)
	}
	t.Cleanup(func() { stopRecovered(report) })

	if got := countRows(t, report.DSN, `SELECT count(*) FROM thing`); got != 25 {
		t.Errorf("found %d rows in the recovered cluster, want 25", got)
	}
}

func TestAnUnreachableStoreStopsARecoveryRatherThanProducingAnEmptyOne(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "down"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)
	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.archiveEverything(t)

	opts := f.walOptions()
	opts.Prefix = prefix
	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("base backup: %v", err)
	}

	f.setDown(true)
	t.Cleanup(func() { f.setDown(false) })

	_, err = RunPITR(context.Background(), PITROptions{
		WAL: opts, BaseBackupID: base.ID,
		Target:         RecoveryTarget{Kind: TargetImmediate},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		AppDatabase:    "postgres", SuperUser: "postgres",
		Timeout: 2 * time.Minute,
	})
	if err == nil {
		t.Fatal("a recovery succeeded with the object store unreachable")
	}
}

func TestARecoveryOntoATimelineThatDoesNotExistIsRefused(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "timeline"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)
	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.archiveEverything(t)

	opts := f.walOptions()
	opts.Prefix = prefix
	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("base backup: %v", err)
	}
	if base.Timeline != 1 {
		t.Fatalf("a fresh cluster is on timeline %d, not 1", base.Timeline)
	}

	// Timeline 7 has never existed. PostgreSQL refuses at startup with
	// "requested timeline 7 is not a child of this server's history", and the
	// recovery must surface that rather than quietly following timeline 1 and
	// handing back a result from a different branch.
	_, err = RunPITR(context.Background(), PITROptions{
		WAL:          opts,
		BaseBackupID: base.ID,
		Target: RecoveryTarget{
			Kind: TargetImmediate, Timeline: 7,
		},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		AppDatabase:    "postgres", SuperUser: "postgres",
		Timeout: 3 * time.Minute,
	})
	if err == nil {
		t.Fatal("a recovery onto a timeline that never existed succeeded")
	}
	t.Logf("refused, as it should be: %v", err)
}

func TestAnInterruptedRecoveryLeavesNothingBehind(t *testing.T) {
	needPostgres(t)
	if testing.Short() {
		t.Skip("builds a cluster")
	}

	f := newFakeStore(t)
	const prefix = "interrupted"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)
	source.exec(t, `CREATE TABLE thing (id int PRIMARY KEY)`)
	source.exec(t, `INSERT INTO thing SELECT generate_series(1, 500)`)
	source.archiveEverything(t)

	opts := f.walOptions()
	opts.Prefix = prefix
	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("base backup: %v", err)
	}

	work := t.TempDir()

	// Cancelled while it is downloading and unpacking. The two things that
	// matter afterwards are that it says so rather than returning a half-built
	// cluster, and that it does not leave a postmaster running on a directory
	// nobody is going to clean up.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	report, err := RunPITR(ctx, PITROptions{
		WAL:          opts,
		BaseBackupID: base.ID,
		Target:       RecoveryTarget{Kind: TargetImmediate},
		WorkDir:      work,
		// Keep asked for deliberately: an interrupted recovery must clean up
		// even when the caller said to leave the result behind, because there
		// is no result to leave.
		Keep:           true,
		RestoreCommand: restoreCommandFor(t),
		AppDatabase:    "postgres", SuperUser: "postgres",
		Timeout: 3 * time.Minute,
	})
	if err == nil {
		t.Fatal("an interrupted recovery reported success")
	}
	if report.Passed {
		t.Error("an interrupted recovery reported that it passed")
	}
	if report.DataDir != "" || report.DSN != "" {
		t.Errorf("an interrupted recovery handed back a cluster to connect "+
			"to: %q %q", report.DataDir, report.DSN)
	}

	// And the staging directory is empty again. A recovery that left an
	// unpacked copy of the cluster behind on every interruption would fill the
	// volume the database is on.
	left, err := os.ReadDir(work)
	if err != nil {
		t.Fatalf("reading the staging directory: %v", err)
	}
	for _, e := range left {
		if strings.HasPrefix(e.Name(), "pitr-") {
			t.Errorf("an interrupted recovery left %s behind", e.Name())
		}
	}
}

// --- helpers ----------------------------------------------------------------

// hexSHA256 is the checksum in the form a manifest records it.
func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// archivedSegments is every whole segment the store holds for one timeline,
// in order.
func archivedSegments(
	t *testing.T, f *fakeStore, prefix string, timeline uint32,
) []string {
	t.Helper()
	dir := WALTimelinePrefix(prefix, timeline)
	var names []string
	for _, key := range f.keysUnder(dir) {
		name := strings.TrimPrefix(key, dir)
		if ValidWALSegmentName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func countRows(t *testing.T, dsn, sql string) int64 {
	t.Helper()
	if dsn == "" {
		t.Fatal("the recovered cluster reported no connection string")
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connecting to the recovered cluster: %v", err)
	}
	defer conn.Close(context.Background())

	var n int64
	if err := conn.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatalf("on the recovered cluster: %v\n%s", err, sql)
	}
	return n
}

// stopRecovered shuts down an instance a test asked to keep.
//
// Best effort: the directory is inside a `t.TempDir()` and goes away either
// way, and on Windows a data directory whose postmaster is still running
// cannot be removed — which would turn a passing test into a cleanup failure.
func stopRecovered(report PITRReport) {
	if report.DataDir == "" {
		return
	}
	cmd := exec.Command(toolPath("", "pg_ctl"),
		"-D", report.DataDir, "-m", "immediate", "-w", "-t", "60", "stop")
	_ = cmd.Run()
	if runtime.GOOS == "windows" {
		// Windows releases the files a moment after the process exits.
		time.Sleep(500 * time.Millisecond)
	}
}
