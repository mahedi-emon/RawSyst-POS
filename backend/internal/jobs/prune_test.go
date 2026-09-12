//go:build integration

package jobs

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/config"
	"github.com/mahedi-emon/Biz1core/backend/internal/platform/db"
)

// newTestQueue opens a queue against the test database, skipping when there is
// none. Matches the shape every other database-backed test in this repository
// uses.
func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()

	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewQueue(pool)
}

// Housekeeping that deletes the wrong row is worse than housekeeping that never
// runs. These two tests are the boundary: what goes, and what must not.
func TestPruneDeletesFinishedWorkAndNothingElse(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	// One job in each state, all old enough to be swept if the state allowed
	// it. The age is what makes the test about STATE rather than about time.
	old := time.Now().Add(-30 * 24 * time.Hour)
	states := []string{"pending", "running", "done", "failed", "dead"}
	ids := map[string]string{}

	for _, state := range states {
		var id string
		if err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				INSERT INTO job (kind, payload, state, created_at, completed_at,
				                 locked_at, locked_by)
				VALUES ('test.prune', '{}'::jsonb, $1::job_state, $2::timestamptz,
				        CASE WHEN $1::job_state IN ('done','dead')
				             THEN $2::timestamptz ELSE NULL END,
				        -- job_lock_is_complete requires both halves of the lock
				        -- or neither, which is the constraint doing its job: a
				        -- row claiming to be held by nobody is a row the reaper
				        -- cannot reason about.
				        CASE WHEN $1::job_state = 'running'
				             THEN $2::timestamptz ELSE NULL END,
				        CASE WHEN $1::job_state = 'running'
				             THEN 'prune-test' ELSE NULL END)
				RETURNING id::text`, state, old).Scan(&id)
		}); err != nil {
			t.Fatalf("seeding a %s job: %v", state, err)
		}
		ids[state] = id
		t.Cleanup(func() {
			_ = q.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
				_, e := tx.Exec(context.Background(),
					`DELETE FROM job WHERE id = $1`, id)
				return e
			})
		})
	}

	if _, _, err := q.Prune(ctx, 7*24*time.Hour, 30*24*time.Hour); err != nil {
		t.Fatalf("prune: %v", err)
	}

	for _, state := range states {
		var present bool
		if err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM job WHERE id = $1)`, ids[state]).
				Scan(&present)
		}); err != nil {
			t.Fatalf("checking the %s job: %v", state, err)
		}

		// `done` and `dead` are finished. `pending` is waiting, `running` is
		// being worked, and `failed` is between retries — deleting any of those
		// drops work rather than tidying after it.
		shouldBeGone := state == "done" || state == "dead"
		if shouldBeGone && present {
			t.Errorf("a %s job survived the prune, so the table still grows", state)
		}
		if !shouldBeGone && !present {
			t.Errorf("a %s job was deleted, which is not tidying up — it is "+
				"dropping work the queue was going to do", state)
		}
	}
}

// A job that finished five minutes ago is not churn. The window is what makes
// `last_error` readable on Monday.
func TestPruneKeepsRecentlyFinishedWork(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	var id string
	if err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO job (kind, payload, state, created_at, completed_at)
			VALUES ('test.prune', '{}'::jsonb, 'done', now(), now())
			RETURNING id::text`).Scan(&id)
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	t.Cleanup(func() {
		_ = q.pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(), `DELETE FROM job WHERE id = $1`, id)
			return e
		})
	})

	if _, _, err := q.Prune(ctx, 7*24*time.Hour, 30*24*time.Hour); err != nil {
		t.Fatalf("prune: %v", err)
	}

	var present bool
	if err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM job WHERE id = $1)`, id).Scan(&present)
	}); err != nil {
		t.Fatalf("checking: %v", err)
	}
	if !present {
		t.Error("a job that finished moments ago was deleted, so nobody can " +
			"read what happened to it")
	}
}

// The audit log is evidence, not churn.
//
// Blueprint A4 says every Super Admin action is "permanently logged" and D4
// makes the trail six fields answering who, what, when, where, before and
// after. A retention window on it is the deletion of evidence — so this asserts
// the pruner does not touch it, which is a guarantee somebody could plausibly
// break while trying to make the database smaller.
// TestPruneNeverTouchesTheAuditLog proves the pruner deletes no audit row.
//
// # Why it names rows instead of counting them
//
// It used to take `count(*) FROM audit_log` before and after and require the
// two to be equal. That is an assertion about a NUMBER, and the number belongs
// to the whole database rather than to this test.
//
// `make test-backend` runs the packages in parallel against ONE test database,
// and this test reads through `TxAsPlatform`, which sees every tenant's rows
// rather than one tenant's. So any other package that wrote a single audit row
// between the two counts made `after != before`, and the failure blamed the
// pruner for a row somebody else had INSERTED. Observed once in three full
// runs; green 3/3 when the package ran alone, which is the signature of a race
// rather than a defect.
//
// Naming the rows removes the race instead of hiding it. The claim being made
// is "every audit row that existed before the prune still exists after it",
// and that is what is now checked: the set of ids is captured, the pruner runs,
// and the database is asked which of those ids have gone. Rows inserted
// concurrently are simply not in the set, whenever they commit -- which a
// count, or a `max(id)` watermark, cannot say, because a concurrent
// transaction can hold an id below the watermark and commit after the snapshot.
//
// It is also STRONGER than the count it replaces: a pruner that deleted one row
// and inserted another kept the count equal and would have passed.
func TestPruneNeverTouchesTheAuditLog(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	// A row this test owns, so the assertion means something on an empty
	// database too. Without it a fresh installation proves only that zero rows
	// survived, which is true of any pruner at all.
	marker := uuid.New()
	if err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO audit_log (action, entity_type, entity_id, occurred_at)
			VALUES ('test.prune.marker', 'test', $1, now() - interval '400 days')`,
			marker)
		return e
	}); err != nil {
		t.Fatalf("seeding a marker audit row: %v", err)
	}

	var ids []int64
	if err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id FROM audit_log`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if e := rows.Scan(&id); e != nil {
				return e
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}); err != nil {
		t.Fatalf("reading the audit log: %v", err)
	}

	// Deliberately run with a zero retention, so every row in the database is
	// "old". If the pruner were ever going to reach the audit log, this is the
	// call that would do it.
	if _, _, err := q.Prune(ctx, 0, 0); err != nil {
		t.Fatalf("prune: %v", err)
	}

	var gone int
	var markerGone bool
	if err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			SELECT count(*)::int
			FROM unnest($1::bigint[]) AS captured(id)
			WHERE NOT EXISTS (
				SELECT 1 FROM audit_log a WHERE a.id = captured.id
			)`, ids).Scan(&gone); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT NOT EXISTS (
				SELECT 1 FROM audit_log WHERE entity_id = $1
			)`, marker).Scan(&markerGone)
	}); err != nil {
		t.Fatalf("checking the audit log: %v", err)
	}

	if gone != 0 {
		t.Errorf("the pruner deleted %d of the %d audit rows that existed "+
			"before it ran. The audit log is evidence, not churn, and A4 calls "+
			"it permanent.", gone, len(ids))
	}
	if markerGone {
		t.Error("the pruner deleted a 400-day-old audit row. Age is not a " +
			"reason to delete evidence.")
	}
}
