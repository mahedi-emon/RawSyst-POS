// Whether there is room to stage a dump, asked before one is started.
//
// # Why this is a precondition and not an error to handle
//
// A dump is staged on disk before it is uploaded. On the server this product is
// sized for, that disk is the same disk the database is on. A backup that runs
// out of space partway has not merely failed: it has spent the night filling
// the volume Postgres needs to write its WAL into, and the first symptom is not
// a failed backup, it is a database that cannot accept a sale.
//
// So the question is asked before `pg_dump` starts, while the answer is still
// free, and a refusal here costs one night of backup rather than a morning of
// outage.
//
// # The estimate
//
// `pg_database_size` is the on-disk size including indexes, and a custom-format
// dump is compressed and carries no indexes, so it is reliably much smaller —
// commonly a fifth to a half. Requiring the FULL database size as free space is
// therefore deliberately pessimistic, and that is the right direction to be
// wrong in: the cost of being too careful is a warning, and the cost of being
// too optimistic is the outage above.
//
// The margin is configurable for the deployment that knows better than this
// estimate, and the message says what to set.
package backup

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// MinFreeBytes is the floor under any staging directory, whatever the database
// size suggests. A dump of a nearly-empty database still needs somewhere to go,
// and a volume with 200MB on it is a volume about to cause a different problem.
const MinFreeBytes = 512 << 20

// roomReport is what the check found, for a log line and for a test.
type roomReport struct {
	DatabaseBytes uint64
	FreeBytes     uint64
	NeedBytes     uint64
	Dir           string
}

// requiredFree is how much room a dump of this database is asked to have.
//
// Split out so the arithmetic is testable without a filesystem or a database.
func requiredFree(databaseBytes uint64, factorPercent int) uint64 {
	if factorPercent <= 0 {
		factorPercent = 100
	}
	need := databaseBytes / 100 * uint64(factorPercent)
	// Integer division above loses the remainder; on a small database that is
	// noise, and on a large one it is a rounding error in the safe direction
	// only if we add it back. Cheaper than floats and exact enough.
	need += (databaseBytes % 100) * uint64(factorPercent) / 100
	if need < MinFreeBytes {
		need = MinFreeBytes
	}
	return need
}

// checkRoom refuses a backup that would probably fill the disk it stages on.
//
// A filesystem that will not answer is NOT treated as a failure: this check
// exists to prevent a specific accident, and refusing to back up because a
// statfs call is unavailable would turn a diagnostic into an outage of its own.
// It says so and continues.
func checkRoom(
	ctx context.Context, conn *pgx.Conn, dir string, factorPercent int,
	warn func(string),
) (*roomReport, error) {
	if dir == "" {
		dir = "."
	}

	var dbBytes int64
	if err := conn.QueryRow(ctx,
		`SELECT pg_database_size(current_database())`).Scan(&dbBytes); err != nil {
		// Not fatal for the same reason as below: this is a guard, not the job.
		if warn != nil {
			warn("the database would not say how big it is, so the check for " +
				"room to stage a dump was skipped")
		}
		return nil, nil
	}

	free, err := freeBytes(dir)
	if err != nil {
		if warn != nil {
			warn(fmt.Sprintf("could not measure free space on %s (%v), so the "+
				"check for room to stage a dump was skipped", dir, err))
		}
		return nil, nil
	}

	rep := &roomReport{
		DatabaseBytes: uint64(dbBytes),
		FreeBytes:     free,
		NeedBytes:     requiredFree(uint64(dbBytes), factorPercent),
		Dir:           dir,
	}
	if free >= rep.NeedBytes {
		return rep, nil
	}
	return rep, errs.New(errs.CodeInvalidInput, refusalMessage(rep))
}

// refusalMessage is what an operator reads at 03:31.
//
// Separate from the check so it can be held to a test: it has to name the
// directory, both figures and the two settings that change the answer, and it
// must never carry a connection string — this text reaches a journal.
func refusalMessage(rep *roomReport) string {
	return fmt.Sprintf(
		"There is not enough room to stage a backup. %s has %s free and this "+
			"asks for %s, because the database is %s and a dump is staged "+
			"there before it is uploaded. A dump that fills this volume "+
			"stops the database writing, so it is refused before it starts "+
			"rather than partway through. Free some space, point "+
			"RAWSYST_BACKUP_TEMP_DIR at a volume that has some, or lower "+
			"RAWSYST_BACKUP_MIN_FREE_PERCENT if you know this database dumps "+
			"much smaller than it measures.",
		rep.Dir, human(rep.FreeBytes), human(rep.NeedBytes),
		human(rep.DatabaseBytes))
}

// human is a size somebody can read in an error message.
func human(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
