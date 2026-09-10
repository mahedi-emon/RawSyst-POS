// Writing what happened into the register the product already has.
//
// `backup_record` has existed since 0093, with the distinction that matters
// already in it: `status` says whether a run finished, and `verified_at` says
// whether anybody proved it restores. Those are separate columns because they
// are separate claims, and the second is the one worth reading.
//
// What was missing was anything that filled them in. The comment on the routes
// said "this product records backups; it does not take them", which was true
// and was a reasonable position for a product deployed by somebody with their
// own backup operator. On a shop's own server there is no backup operator, so
// the product takes them — and writes down what it did, here.
//
// # Platform rows, not tenant rows
//
// A dump of this database is every tenant at once, so the row belongs to none
// of them and `tenant_id` is NULL. The policy on the table already reads
// `tenant_id = current_tenant_id() OR is_platform_admin()`, so a platform
// operator sees these and a business sees only its own. That is the right
// division: a shop should not be shown the platform's backup as though it were
// theirs, and should not be told it is unprotected because it cannot see one.
package backup

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Register records runs and verifications against the platform.
type Register struct{ pool *db.Pool }

func NewRegister(pool *db.Pool) *Register { return &Register{pool: pool} }

// Start opens a row before the work, and returns its id.
//
// Before rather than after, so a backup that dies halfway leaves something
// somebody can see. A record written only on success makes a crashed backup
// indistinguishable from one nobody ever scheduled — which is the failure this
// whole subsystem exists to make impossible.
func (r *Register) Start(ctx context.Context, kind string) (uuid.UUID, error) {
	if r == nil || r.pool == nil {
		return uuid.Nil, nil
	}
	if kind != "scheduled" && kind != "manual" {
		kind = "manual"
	}
	var id uuid.UUID
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO backup_record (tenant_id, kind, status)
			VALUES (NULL, $1, 'running') RETURNING id`, kind).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, db.Translate(err, "That backup could not be recorded.")
	}
	return id, nil
}

// Finish closes a row, succeeded or failed.
//
// `failure` empty means it worked. A failed row must say why — the table's own
// constraint insists on it — because a failure with no reason is a row that
// gets ignored.
func (r *Register) Finish(
	ctx context.Context, id uuid.UUID,
	location, checksum string, size int64, failure string,
) error {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil
	}
	status := "succeeded"
	if failure != "" {
		status = "failed"
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE backup_record
			SET status = $2, location = nullif($3,''), checksum = nullif($4,''),
			    size_bytes = nullif($5, 0), error = nullif($6,''),
			    finished_at = now()
			WHERE id = $1 AND status = 'running'`,
			id, status, location, checksum, size, failure)
		return e
	})
	return db.Translate(err, "That backup could not be closed.")
}

// Verified stamps a row that has actually been restored.
//
// A failed verification records the reason and leaves `verified_at` NULL, which
// is what keeps the dashboard honest: a broken backup that stamped itself
// verified would read as protection.
func (r *Register) Verified(
	ctx context.Context, id uuid.UUID, failure string,
) error {
	if r == nil || r.pool == nil || id == uuid.Nil {
		return nil
	}
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if failure != "" {
			_, e := tx.Exec(ctx, `
				UPDATE backup_record SET verify_error = $2, verified_at = NULL
				WHERE id = $1`, id, failure)
			return e
		}
		_, e := tx.Exec(ctx, `
			UPDATE backup_record SET verified_at = now(), verify_error = NULL
			WHERE id = $1`, id)
		return e
	})
	return db.Translate(err, "That verification could not be recorded.")
}

// FindByLocation returns the row for a snapshot, so a verification run
// separately from the backup can stamp the right one.
func (r *Register) FindByLocation(
	ctx context.Context, location string,
) (uuid.UUID, error) {
	if r == nil || r.pool == nil {
		return uuid.Nil, nil
	}
	var id uuid.UUID
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id FROM backup_record
			WHERE tenant_id IS NULL AND location = $1
			ORDER BY started_at DESC LIMIT 1`, location).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, nil // not found is not a failure; nothing to stamp
	}
	return id, nil
}

// LastVerified is what the hourly check and the admin screen both want to know.
type LastVerified struct {
	At       time.Time
	Location string
	Age      time.Duration
}

// Latest reads the most recent VERIFIED platform backup.
//
// Verified, not merely succeeded. The second is a more comforting number and a
// less true one, and this product has said so on the backup screen since H4.
func (r *Register) Latest(ctx context.Context) (LastVerified, error) {
	if r == nil || r.pool == nil {
		return LastVerified{}, errs.New(errs.CodeUnavailable,
			"No database connection.")
	}
	var out LastVerified
	var at *time.Time
	var location *string
	err := r.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT verified_at, location FROM backup_record
			WHERE tenant_id IS NULL AND verified_at IS NOT NULL
			ORDER BY verified_at DESC LIMIT 1`).Scan(&at, &location)
	})
	if err != nil {
		return LastVerified{}, db.Translate(err, "")
	}
	if at != nil {
		out.At, out.Age = *at, time.Since(*at)
	}
	if location != nil {
		out.Location = *location
	}
	return out, nil
}
