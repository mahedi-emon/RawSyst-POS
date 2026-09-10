//go:build integration

// The two acts that decide who can administer a deployment.
//
// Both run against the database directly rather than through a token, so
// neither is protected by anything the API enforces. What protects them is in
// this file: `create` refuses once an operator exists, and `-recover` refuses
// to create one at all.
package bootstrap

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

func testPool(t *testing.T) *db.Pool {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	pool, err := db.Open(context.Background(), config.DB{
		DSN: dsn, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// anOperator puts one on file and takes them off again.
//
// Disabled rather than deleted in the cleanup: `app_user` is referenced by
// audit rows, and a test that deletes its own actor leaves a trail naming
// nobody. Disabled is also what the product itself does to a departing
// administrator.
func anOperator(t *testing.T, pool *db.Pool) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	email := "recovery-" + id.String()[:8] + "@example.test"

	if err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO app_user
			  (id, tenant_id, email, full_name, password_hash, status)
			VALUES ($1, NULL, $2, 'Recovery Test', 'not-a-real-hash', 'active')`,
			id, email)
		return e
	}); err != nil {
		t.Fatalf("seed an operator: %v", err)
	}

	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, e := tx.Exec(context.Background(),
				`UPDATE app_user SET status = 'disabled' WHERE id = $1`, id)
			return e
		})
	})
	return id, email
}

// The refusal that separates a bootstrap from a back door.
func TestBootstrapRefusesOnceAnOperatorExists(t *testing.T) {
	pool := testPool(t)
	_, _ = anOperator(t, pool)

	err := create(context.Background(), pool, "somebody-else@example.test", "Somebody")
	if err == nil {
		t.Fatal("a second platform operator was created by bootstrap; a copy " +
			"of this binary on a live server would be a way to mint yourself " +
			"an administrator")
	}
	if !strings.Contains(err.Error(), "FIRST") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// Recovery is for somebody who is already here.
func TestRecoveryWillNotCreateAnAdministrator(t *testing.T) {
	pool := testPool(t)

	err := recoverIn(context.Background(), pool,
		"nobody-at-all-"+uuid.New().String()[:8]+"@example.invalid")
	if err == nil {
		t.Fatal("recovery accepted an address that is nobody, which would " +
			"make it a way to create an administrator rather than to let one " +
			"back in")
	}
	if !strings.Contains(err.Error(), "not a platform operator") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// And not for a business user, however real.
func TestRecoveryWillNotTakeOverABusinessAccount(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var email string
	_ = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT email FROM app_user
			WHERE tenant_id IS NOT NULL AND status = 'active' LIMIT 1`).
			Scan(&email)
	})
	if email == "" {
		t.Skip("this database holds no business user to check against")
	}

	if err := recoverIn(ctx, pool, email); err == nil {
		t.Fatal("recovery reset a BUSINESS user's password. It resets " +
			"platform operators; anything wider is a way into somebody's " +
			"shop from the machine that hosts it")
	}
}

func TestRecoveryIssuesAPasswordAndEndsEverySession(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id, email := anOperator(t, pool)

	// A session to be ended. 0007 moved the refresh token out of this table
	// into its own chain, so a row here is a session and nothing else.
	if err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			INSERT INTO user_session (user_id, expires_at)
			VALUES ($1, now() + interval '30 days')`, id)
		return e
	}); err != nil {
		t.Fatalf("open a session to be ended: %v", err)
	}

	if err := recoverIn(ctx, pool, email); err != nil {
		t.Fatalf("recovery: %v", err)
	}

	var mustChange bool
	var hash string
	var live int
	var audited int
	if err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `
			SELECT must_change_password, password_hash FROM app_user WHERE id = $1`,
			id).Scan(&mustChange, &hash); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `
			SELECT count(*) FROM user_session
			WHERE user_id = $1 AND revoked_at IS NULL`, id).Scan(&live); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM audit_log
			WHERE action = 'platform_operator_password_recovered'
			  AND entity_id = $1`, id).Scan(&audited)
	}); err != nil {
		t.Fatalf("read back: %v", err)
	}

	if !mustChange {
		t.Error("the recovered password does not have to be changed, and it " +
			"was printed to a terminal")
	}
	if hash == "not-a-real-hash" {
		t.Error("the password hash did not change")
	}
	if live != 0 {
		t.Errorf("%d session(s) survived. A lost password and a stolen one "+
			"arrive looking identical, so both end every session", live)
	}
	if audited == 0 {
		t.Error("nothing in the trail says an operator's password was " +
			"recovered from the machine hosting the deployment")
	}
}
