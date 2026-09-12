// The two measurements the drills deliberately do not make.
//
// # Why these are separate, and why they skip by default
//
// `pitr_test.go` proves the mechanism on a cluster of a few megabytes, and it
// runs on every `go test`. These two answer questions that need real bytes and
// real time:
//
//   - How long does a base backup and a recovery take, as the database grows?
//   - When archiving fails, how fast does the disk fill, and does the backlog
//     really ship itself when the store comes back?
//
// Both cost minutes and gigabytes, so both skip unless asked. They are tests
// rather than scripts because a measurement nobody can repeat is an anecdote,
// and because the thing being measured is the code in this package rather than
// a shell pipeline around it.
//
//	RAWSYST_PITR_SCALE_MB=512 go test ./internal/backup -run AtScale -v
//	RAWSYST_PITR_OUTAGE_SECONDS=60 go test ./internal/backup -run UnderArchiveFailure -v
//
// # What they still do not measure
//
// The network. Both use an object store in this process, so every byte moves
// over a loopback socket. On a real server the dominant term in a recovery is
// the download from the bucket over whatever connection the shop has, and
// nothing here can stand in for that. The numbers below are a floor: the real
// ones are this plus the wire.
//
// # And how large they can usefully go
//
// The fake store keeps objects in a map in this process, so a base backup of a
// gigabyte is a gigabyte of the test's own memory. That is fine to about 1 GB
// of cluster on an ordinary machine and is why these stop there. It is not a
// limit of the product — a real bucket does not care — it is a limit of how far
// a measurement like this can be taken without a real one.
//
// Beyond that size the thing to run is the container drill in
// `deploy/server/pitr-drill.sh` against a real store, and then the server
// itself, which is what `PITR-ACTIVATION.md` step 10 is for.
package backup

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestMeasurePITRAtScale times a base backup and a recovery against a database
// of a size somebody chooses.
func TestMeasurePITRAtScale(t *testing.T) {
	targetMB := envInt("RAWSYST_PITR_SCALE_MB", 0)
	if targetMB <= 0 {
		t.Skip("set RAWSYST_PITR_SCALE_MB to the database size to measure at")
	}
	needPostgres(t)

	f := newFakeStore(t)
	const prefix = "scale"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)

	// Data that does not compress like a synthetic benchmark.
	//
	// `repeat('x', 500)` would produce a base backup a hundred times smaller
	// than the database and a compression figure nobody could plan storage
	// from. Hex from md5 is close to incompressible, which errs the other way
	// and is the safe direction: a capacity estimate that is too pessimistic
	// costs disk, and one that is too optimistic costs an outage.
	t.Logf("seeding about %d MB ...", targetMB)
	seedBegan := time.Now()
	source.exec(t, `
		CREATE TABLE bulk (
		  id     bigserial PRIMARY KEY,
		  ref    text NOT NULL,
		  body   text NOT NULL,
		  amount numeric(14,2) NOT NULL,
		  at     timestamptz NOT NULL DEFAULT now()
		)`)

	// Roughly 300 bytes a row once the row header and the index are counted,
	// inserted in batches so one statement does not build a gigabyte of WAL in
	// a single transaction.
	const bytesPerRow = 300
	rows := int64(targetMB) * 1024 * 1024 / bytesPerRow
	const batch = 50_000
	for done := int64(0); done < rows; done += batch {
		n := batch
		if rows-done < batch {
			n = int(rows - done)
		}
		source.exec(t, `
			INSERT INTO bulk (ref, body, amount)
			SELECT md5(g::text),
			       md5(random()::text) || md5(random()::text) ||
			         md5(random()::text) || md5(random()::text),
			       (random() * 1000)::numeric(14,2)
			  FROM generate_series(1, $1) g`, n)
	}
	seeding := time.Since(seedBegan)

	var sizeBytes int64
	var sizePretty string
	conn, err := pgx.Connect(context.Background(), source.dsn())
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	if err := conn.QueryRow(context.Background(),
		`SELECT pg_database_size(current_database()),
		        pg_size_pretty(pg_database_size(current_database()))`).
		Scan(&sizeBytes, &sizePretty); err != nil {
		t.Fatalf("measuring the database: %v", err)
	}
	conn.Close(context.Background())
	source.archiveEverything(t)

	t.Logf("MEASURED seeding: %s in %s", sizePretty,
		seeding.Round(time.Second))

	// --- the base backup ----------------------------------------------------

	opts := f.walOptions()
	opts.Prefix = prefix

	baseBegan := time.Now()
	base, err := TakeBaseBackup(context.Background(), BaseBackupOptions{
		DSN: source.dsn(), WAL: opts, StagingDir: t.TempDir(),
		MinFreePercent: 1, Timeout: 2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("base backup: %v", err)
	}
	baseTook := time.Since(baseBegan)

	stored := base.TotalBytes()
	ratio := float64(sizeBytes) / float64(max64(stored, 1))
	t.Logf("MEASURED base backup: %s cluster -> %s stored in %s "+
		"(%.1fx smaller, %.1f MB/s of cluster)",
		sizePretty, humanBytes(stored), baseTook.Round(time.Second), ratio,
		float64(sizeBytes)/(1<<20)/maxF(baseTook.Seconds(), 0.001))

	// --- a trading day on top of it -----------------------------------------

	source.exec(t, `
		INSERT INTO bulk (ref, body, amount)
		SELECT md5(g::text), md5(random()::text) || md5(random()::text),
		       (random() * 1000)::numeric(14,2)
		  FROM generate_series(1, 20000) g`)
	source.exec(t, `UPDATE bulk SET amount = amount + 1 WHERE id % 7 = 0`)
	source.archiveEverything(t)

	// --- the recovery -------------------------------------------------------

	recoverBegan := time.Now()
	report, err := RunPITR(context.Background(), PITROptions{
		WAL:            opts,
		BaseBackupID:   base.ID,
		Target:         RecoveryTarget{Kind: TargetLatest},
		WorkDir:        t.TempDir(),
		RestoreCommand: restoreCommandFor(t),
		Keep:           true,
		AppDatabase:    "postgres",
		SuperUser:      "postgres",
		Timeout:        2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	t.Cleanup(func() { stopRecovered(report) })
	recoverTook := time.Since(recoverBegan)

	// It has to be the right database, or the timing is a measurement of
	// something else entirely.
	got := countRows(t, report.DSN, `SELECT count(*) FROM bulk`)
	if got < rows {
		t.Errorf("the recovered database has %d rows and at least %d were "+
			"written", got, rows)
	}

	t.Logf("MEASURED recovery to latest: %s total "+
		"(%ds fetching and unpacking, %ds replaying), %d rows back",
		recoverTook.Round(time.Second),
		report.DownloadSeconds, report.ReplaySeconds, got)

	t.Logf("MEASURED summary at %s: base backup %s, recovery %s. "+
		"Object store is in-process, so the wire is NOT in these numbers.",
		sizePretty, baseTook.Round(time.Second),
		recoverTook.Round(time.Second))
}

// TestMeasureDiskUnderArchiveFailure answers the question the rollback section
// of PITR.md turns on: when the store is unreachable, how long is there before
// the disk fills, and does the backlog really ship itself afterwards?
//
// The second half is the one that matters. `PITR.md` tells an operator that the
// right first response to a failing archive is to fix the store and wait,
// because PostgreSQL ships the backlog on its own and nothing is lost. That is
// a claim about behaviour under failure, and a claim like that should be
// demonstrated rather than asserted from the documentation.
func TestMeasureDiskUnderArchiveFailure(t *testing.T) {
	outage := envInt("RAWSYST_PITR_OUTAGE_SECONDS", 0)
	if outage <= 0 {
		t.Skip("set RAWSYST_PITR_OUTAGE_SECONDS to how long to break the store for")
	}
	needPostgres(t)

	f := newFakeStore(t)
	const prefix = "outage"
	pitrEnvFor(t, f, prefix)
	source := startSource(t, f, prefix)

	source.exec(t, `
		CREATE TABLE churn (
		  id   bigserial PRIMARY KEY,
		  body text NOT NULL
		)`)
	source.archiveEverything(t)

	walBytes := func() int64 {
		t.Helper()
		conn, err := pgx.Connect(context.Background(), source.dsn())
		if err != nil {
			t.Fatalf("connecting: %v", err)
		}
		defer conn.Close(context.Background())
		var n int64
		if err := conn.QueryRow(context.Background(),
			`SELECT coalesce(sum(size), 0)::bigint FROM pg_ls_waldir()`).
			Scan(&n); err != nil {
			t.Fatalf("measuring pg_wal: %v", err)
		}
		return n
	}

	before := walBytes()
	t.Logf("pg_wal before the outage: %s", humanBytes(before))

	// --- the store goes away -------------------------------------------------

	f.setDown(true)
	began := time.Now()
	deadline := began.Add(time.Duration(outage) * time.Second)

	written := 0
	for time.Now().Before(deadline) {
		source.exec(t, `
			INSERT INTO churn (body)
			SELECT md5(random()::text) || md5(random()::text)
			  FROM generate_series(1, 5000)`)
		written += 5000
	}
	elapsed := time.Since(began)
	during := walBytes()

	grew := during - before
	perHour := float64(grew) / elapsed.Hours()
	t.Logf("MEASURED pg_wal under archive failure: grew %s in %s "+
		"while %d rows were written -- about %s per hour at this write rate",
		humanBytes(grew), elapsed.Round(time.Second), written,
		humanBytes(int64(perHour)))

	// The floor is arithmetic rather than measured, and is the number that
	// matters on a quiet night: archive_timeout forces a whole segment to be
	// closed however little is in it, so an idle server still accumulates.
	t.Logf("MEASURED floor: at the production archive_timeout of 60s, an IDLE " +
		"server forces one 16 MiB segment a minute, which is 0.94 GiB per " +
		"hour of retained log with nowhere to ship it")

	// The number an operator actually acts on: how long there is before the
	// disk is gone. Reported at both ends — the hammering rate above and the
	// idle floor — because the truth for a shop is between them and much
	// closer to the floor.
	//
	// Eight gigabytes because that is DISK_FREE_MIN_GIB in
	// deploy/server/biz1core-check.sh: the point at which the machine check
	// already flags the disk, so it is the headroom to assume rather than the
	// whole volume.
	const idlePerHour = float64(16<<20) * 60
	const headroom = float64(8 << 30)
	t.Logf("MEASURED time to exhaustion with 8 GiB of headroom: "+
		"%.1f hours at the write rate above, %.0f hours on an idle server",
		headroom/maxF(perHour, 1), headroom/idlePerHour)

	// It must have kept everything rather than losing any of it.
	var failed int64
	conn, err := pgx.Connect(context.Background(), source.dsn())
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	if err := conn.QueryRow(context.Background(),
		`SELECT failed_count FROM pg_stat_archiver`).Scan(&failed); err != nil {
		t.Fatalf("reading pg_stat_archiver: %v", err)
	}
	conn.Close(context.Background())
	if failed == 0 {
		t.Fatal("the store was down and PostgreSQL recorded no failed archive " +
			"attempts, so the archive command is not reporting failure")
	}
	t.Logf("PostgreSQL recorded %d failed archive attempts and kept the "+
		"segments", failed)

	// --- the store comes back ------------------------------------------------

	f.setDown(false)
	caughtUp := time.Now()

	// PostgreSQL retries on its own schedule. Give it long enough to drain the
	// backlog and confirm it did, which is the claim PITR.md makes.
	last := source.archiveEverything(t)
	t.Logf("MEASURED recovery from the outage: the backlog drained on its own "+
		"and reached %s, %s after the store came back",
		last, time.Since(caughtUp).Round(time.Second))

	after := walBytes()
	t.Logf("pg_wal after the backlog shipped: %s (was %s at the peak)",
		humanBytes(after), humanBytes(during))

	// And the archive has no hole in it. This is the whole point of keeping
	// the segments rather than dropping them: an outage costs disk and time,
	// and does not cost a recovery window.
	opts := f.walOptions()
	opts.Prefix = prefix
	report, err := VerifyArchive(context.Background(), opts, 0)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.Passed {
		t.Errorf("the archive did not check out after the outage: %v",
			report.Findings)
	}
	if len(report.Gaps) != 0 {
		t.Errorf("the outage left %d gap(s) in the archive: %+v",
			len(report.Gaps), report.Gaps)
	}
	t.Logf("MEASURED result: %d segments archived, %d gaps. An outage cost "+
		"disk and time and did not cost a recovery window.",
		report.Segments, len(report.Gaps))
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
