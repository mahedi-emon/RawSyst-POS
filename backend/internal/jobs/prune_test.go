//go:build integration

package jobs

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
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
func TestPruneNeverTouchesTheAuditLog(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	var before, after int
	count := func(into *int) {
		if err := q.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*)::int FROM audit_log`).Scan(into)
		}); err != nil {
			t.Fatalf("counting the audit log: %v", err)
		}
	}

	count(&before)
	if _, _, err := q.Prune(ctx, 0, 0); err != nil {
		t.Fatalf("prune: %v", err)
	}
	count(&after)

	// Deliberately run with a zero retention, so every row in the database is
	// "old". If the pruner were ever going to reach the audit log, this is the
	// call that would do it.
	if after != before {
		t.Errorf("the audit log went from %d rows to %d. It is evidence, not "+
			"churn, and A4 calls it permanent.", before, after)
	}
}
