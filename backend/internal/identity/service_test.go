//go:build integration

package identity

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

const testPassword = "a reasonably long passphrase"

func testService(t *testing.T) (*Service, *db.Pool) {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()

	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tokens := NewTokenService(config.Auth{
		JWTSecret:       []byte("integration-test-secret-at-least-32-bytes"),
		Issuer:          "rawsyst-test",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 720 * time.Hour,
	})
	return NewService(pool, tokens), pool
}

// seedUser provisions a tenant and one active user, returning both ids.
func seedUser(t *testing.T, pool *db.Pool, email string) (userID, tenantID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	hash, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenant (name) VALUES ($1) RETURNING id`, email).
			Scan(&tenantID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO app_user (tenant_id, email, full_name, password_hash,
			                      status, must_change_password)
			VALUES ($1, $2, 'Test User', $3, 'active', false)
			RETURNING id`, tenantID, email, hash).Scan(&userID)
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, tenantID)
			return err
		})
	})
	return userID, tenantID
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return "u" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] + "@example.test"
}

func TestLoginSucceedsAndBindsTenant(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	userID, tenantID := seedUser(t, pool, email)

	s, err := svc.Login(context.Background(), Credentials{
		Email: email, Password: testPassword, IP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if s.AccessToken == "" || s.RefreshToken == "" {
		t.Fatal("login returned an empty token")
	}
	if s.Actor.UserID != userID || s.Actor.TenantID != tenantID {
		t.Fatalf("actor = %+v, want user %s tenant %s", s.Actor, userID, tenantID)
	}
	if s.Actor.IsSuperAdmin {
		t.Fatal("a tenant user was marked as a platform Super Admin")
	}
}

// The single most important property of a login endpoint: a wrong password and
// an unknown account must be indistinguishable. Otherwise the endpoint is a
// user-enumeration oracle and the first step of credential stuffing.
func TestLoginDoesNotRevealWhetherAccountExists(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	seedUser(t, pool, email)
	ctx := context.Background()

	_, errWrongPassword := svc.Login(ctx, Credentials{
		Email: email, Password: "definitely not the password",
	})
	_, errNoSuchUser := svc.Login(ctx, Credentials{
		Email: uniqueEmail(t), Password: "definitely not the password",
	})

	if errWrongPassword == nil || errNoSuchUser == nil {
		t.Fatal("a bad sign-in attempt succeeded")
	}
	if errWrongPassword.Error() != errNoSuchUser.Error() {
		t.Fatalf("error messages differ, which reveals whether the account exists:\n"+
			"  wrong password: %v\n  unknown account: %v", errWrongPassword, errNoSuchUser)
	}
	if errs.CodeOf(errWrongPassword) != errs.CodeUnauthenticated {
		t.Fatalf("code = %q, want %q", errs.CodeOf(errWrongPassword), errs.CodeUnauthenticated)
	}
}

func TestLoginLocksAccountAfterRepeatedFailures(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	seedUser(t, pool, email)
	ctx := context.Background()

	for i := 0; i < MaxFailedAttempts; i++ {
		if _, err := svc.Login(ctx, Credentials{Email: email, Password: "wrong"}); err == nil {
			t.Fatalf("attempt %d with a wrong password succeeded", i+1)
		}
	}

	// The correct password must now be refused, and the message must say why —
	// a locked-out cashier needs to know it is temporary, not that they have
	// forgotten their password.
	_, err := svc.Login(ctx, Credentials{Email: email, Password: testPassword})
	if err == nil {
		t.Fatal("account was not locked after repeated failures")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("lockout message does not explain the lockout: %v", err)
	}
}

func TestRefreshRotatesTheToken(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	seedUser(t, pool, email)
	ctx := context.Background()

	first, err := svc.Login(ctx, Credentials{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	second, err := svc.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh returned the same token; rotation is not happening")
	}
	if second.Actor.UserID != first.Actor.UserID {
		t.Fatal("refresh changed the user identity")
	}
}

// A refresh token presented twice means a copy was captured. Refusing only the
// replay would leave the thief's copy working, so the whole session family is
// revoked instead.
func TestRefreshReuseRevokesEverySession(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	seedUser(t, pool, email)
	ctx := context.Background()

	first, err := svc.Login(ctx, Credentials{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	second, err := svc.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Replay the token that was already rotated away.
	if _, err := svc.Refresh(ctx, first.RefreshToken); err == nil {
		t.Fatal("a reused refresh token was accepted")
	}

	// The legitimate successor must also now be dead.
	if _, err := svc.Refresh(ctx, second.RefreshToken); err == nil {
		t.Fatal("after detecting reuse, the session family was not revoked")
	}
}

func TestChangePasswordRevokesAllSessions(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	userID, _ := seedUser(t, pool, email)
	ctx := context.Background()

	s, err := svc.Login(ctx, Credentials{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	const newPassword = "an entirely different long passphrase"
	if err := svc.ChangePassword(ctx, userID, testPassword, newPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// A password change is usually a response to suspected compromise; leaving
	// the attacker's session alive would defeat the purpose.
	if _, err := svc.Refresh(ctx, s.RefreshToken); err == nil {
		t.Fatal("an existing session survived a password change")
	}
	if _, err := svc.Login(ctx, Credentials{Email: email, Password: testPassword}); err == nil {
		t.Fatal("the old password still works")
	}
	if _, err := svc.Login(ctx, Credentials{Email: email, Password: newPassword}); err != nil {
		t.Fatalf("the new password does not work: %v", err)
	}
}

func TestChangePasswordRejectsWrongCurrentPassword(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	userID, _ := seedUser(t, pool, email)

	err := svc.ChangePassword(context.Background(), userID,
		"not the current password", "an entirely different long passphrase")
	if err == nil {
		t.Fatal("password was changed without knowing the current one")
	}
}

// Blueprint A4.2. The Super Admin issues a NEW password; there is no path that
// reveals the old one, because the column holds an irreversible hash.
func TestSuperAdminResetIssuesNewPasswordAndRevokesSessions(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	userID, tenantID := seedUser(t, pool, email)
	ctx := context.Background()

	existing, err := svc.Login(ctx, Credentials{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	adminCtx := actor.Into(ctx, actor.Actor{UserID: uuid.New(), IsSuperAdmin: true})
	res, err := svc.ResetPasswordAsSuperAdmin(adminCtx, userID,
		"verified caller against registered phone number on file")
	if err != nil {
		t.Fatalf("ResetPasswordAsSuperAdmin: %v", err)
	}

	if res.TemporaryPassword == "" {
		t.Fatal("no temporary password was issued")
	}
	if res.TemporaryPassword == testPassword {
		t.Fatal("the reset returned the user's existing password")
	}
	if err := ValidatePasswordStrength(strings.ReplaceAll(res.TemporaryPassword, "-", "")); err != nil {
		t.Fatalf("the generated password fails our own policy: %v", err)
	}

	// Sessions opened with the old password are no longer trustworthy.
	if _, err := svc.Refresh(ctx, existing.RefreshToken); err == nil {
		t.Fatal("a session survived an administrator password reset")
	}

	// The temporary password works and forces a change.
	s, err := svc.Login(ctx, Credentials{Email: email, Password: res.TemporaryPassword})
	if err != nil {
		t.Fatalf("temporary password does not work: %v", err)
	}
	if !s.MustChangePassword {
		t.Fatal("a reset password did not force a change on first use")
	}

	// The reset must be permanently recorded, with the stated reason.
	var count int
	var after *string
	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*), max(after_value::text) FROM audit_log
			WHERE tenant_id = $1 AND entity_id = $2
			  AND action = 'password_reset_by_super_admin'`,
			tenantID, userID).Scan(&count, &after)
	})
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit rows for the reset = %d, want 1", count)
	}
	if after == nil || !strings.Contains(*after, "registered phone number") {
		t.Fatal("the audit record does not carry the identity-verification reason")
	}
}

// A reset without a recorded reason must be refused: blueprint A4.2 requires
// the Super Admin to verify identity first, and an unexplained reset is
// indistinguishable from an account takeover by the platform operator.
func TestSuperAdminResetRequiresAReason(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	userID, _ := seedUser(t, pool, email)

	adminCtx := actor.Into(context.Background(),
		actor.Actor{UserID: uuid.New(), IsSuperAdmin: true})

	if _, err := svc.ResetPasswordAsSuperAdmin(adminCtx, userID, "ok"); err == nil {
		t.Fatal("a password reset was allowed with no recorded justification")
	}
}

// Only a Super Admin may use the assisted-recovery path. A tenant user calling
// it must be refused even for their own account.
func TestAssistedResetRequiresSuperAdmin(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	userID, tenantID := seedUser(t, pool, email)

	tenantCtx := actor.Into(context.Background(),
		actor.Actor{UserID: userID, TenantID: tenantID})

	_, err := svc.ResetPasswordAsSuperAdmin(tenantCtx, userID,
		"trying to reset my own password through the admin path")
	if err == nil {
		t.Fatal("a tenant user performed a Super-Admin-only password reset")
	}
	if errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeForbidden)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	seedUser(t, pool, email)
	ctx := context.Background()

	s, err := svc.Login(ctx, Credentials{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := svc.Logout(ctx, s.Actor.SessionID); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := svc.Refresh(ctx, s.RefreshToken); err == nil {
		t.Fatal("a refresh token still worked after sign-out")
	}
}

// A successful sign-in is one of blueprint D4's recorded actions.
func TestLoginIsAudited(t *testing.T) {
	svc, pool := testService(t)
	email := uniqueEmail(t)
	userID, tenantID := seedUser(t, pool, email)
	ctx := context.Background()

	if _, err := svc.Login(ctx, Credentials{
		Email: email, Password: testPassword, IP: "203.0.113.9",
	}); err != nil {
		t.Fatalf("Login: %v", err)
	}

	var count int
	var ip *string
	err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*), max(host(ip)) FROM audit_log
			WHERE tenant_id = $1 AND actor_id = $2 AND action = 'login'`,
			tenantID, userID).Scan(&count, &ip)
	})
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if count != 1 {
		t.Fatalf("login audit rows = %d, want 1", count)
	}
	if ip == nil || *ip != "203.0.113.9" {
		t.Fatalf("audit did not record where the sign-in came from: %v", ip)
	}
}

// The temporary password must be unpredictable and readable aloud, since the
// recovery flow ends with a Super Admin reading it to an Owner.
func TestTemporaryPasswordIsRandomAndUnambiguous(t *testing.T) {
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		p, err := generateTemporaryPassword()
		if err != nil {
			t.Fatalf("generateTemporaryPassword: %v", err)
		}
		if seen[p] {
			t.Fatalf("generated a duplicate temporary password after %d draws", i)
		}
		seen[p] = true

		for _, ambiguous := range []string{"0", "O", "1", "l", "I", "5", "S", "8", "B"} {
			if strings.Contains(p, ambiguous) {
				t.Fatalf("temporary password %q contains %q, which is misread when "+
					"spoken or copied", p, ambiguous)
			}
		}
	}
}
