package identity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Lockout policy. Blueprint A4 lists "lockout thresholds" as configurable
// global security policy; these are the defaults.
//
// The window is deliberately short. Long lockouts turn a guessing attempt into
// a denial-of-service against a real cashier mid-shift, which in a shop is a
// worse outcome than the attack itself.
const (
	MaxFailedAttempts = 8
	LockoutDuration   = 15 * time.Minute
)

// Service handles sign-in, session lifecycle and account recovery.
type Service struct {
	pool   *db.Pool
	tokens *TokenService
}

func NewService(pool *db.Pool, tokens *TokenService) *Service {
	return &Service{pool: pool, tokens: tokens}
}

// Credentials is a sign-in attempt.
type Credentials struct {
	Email     string
	Password  string
	IP        string
	UserAgent string
	Device    string
}

// Session is what a successful sign-in returns.
type Session struct {
	AccessToken        string      `json:"access_token"`
	RefreshToken       string      `json:"refresh_token"`
	ExpiresAt          time.Time   `json:"expires_at"`
	MustChangePassword bool        `json:"must_change_password"`
	Actor              actor.Actor `json:"-"`
}

// errInvalidCredentials is returned for every sign-in failure, whatever the
// real cause.
//
// Distinguishing "no such user" from "wrong password" hands an attacker a user
// enumeration oracle: they learn which email addresses are real, which is the
// first step of a credential-stuffing run. The constant-time password compare
// exists for the same reason, and both are pointless without this.
var errInvalidCredentials = errs.New(errs.CodeUnauthenticated,
	"That email or password is not correct.")

// Login authenticates a user and opens a session.
//
// This runs on the platform plane rather than a tenant plane, because at this
// point we do not yet know which tenant the caller belongs to — that is what
// the lookup determines. The email index is unique per tenant, and globally
// unique for Super Admins.
func (s *Service) Login(ctx context.Context, c Credentials) (Session, error) {
	var (
		userID       uuid.UUID
		tenantID     *uuid.UUID
		passwordHash string
		status       string
		mustChange   bool
		failed       int
		lockedUntil  *time.Time
	)

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, tenant_id, password_hash, status,
			       must_change_password, failed_attempts, locked_until
			FROM app_user
			WHERE email = $1`, c.Email).
			Scan(&userID, &tenantID, &passwordHash, &status,
				&mustChange, &failed, &lockedUntil)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Hash a dummy password anyway. Returning immediately would make a
			// non-existent account measurably faster to reject than a real one,
			// reintroducing the enumeration this message is meant to prevent.
			_, _ = VerifyPassword(dummyHash, c.Password)
			return Session{}, errInvalidCredentials
		}
		return Session{}, db.Translate(err, "")
	}

	if lockedUntil != nil && lockedUntil.After(time.Now()) {
		return Session{}, errs.Newf(errs.CodeUnauthenticated,
			"This account is temporarily locked after too many failed sign-in "+
				"attempts. Try again in %d minutes, or ask your owner to reset it.",
			int(time.Until(*lockedUntil).Minutes())+1)
	}

	ok, err := VerifyPassword(passwordHash, c.Password)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		s.recordFailedAttempt(ctx, userID, failed+1)
		return Session{}, errInvalidCredentials
	}

	// A correct password on a disabled account still fails, but only after the
	// password check, so the account's existence is not revealed by timing.
	switch status {
	case "active":
	case "invited":
		// Permitted: the first sign-in with a temporary password. The
		// must_change_password flag forces the change before anything else.
	case "suspended", "disabled":
		return Session{}, errs.New(errs.CodeForbidden,
			"This account has been disabled. Contact your owner.")
	default:
		return Session{}, errInvalidCredentials
	}

	a := actor.Actor{UserID: userID, SessionID: uuid.New()}
	if tenantID != nil {
		a.TenantID = *tenantID
	} else {
		a.IsSuperAdmin = true
	}

	refreshToken, refreshHash, err := NewRefreshToken()
	if err != nil {
		return Session{}, err
	}
	accessToken, expiresAt, err := s.tokens.IssueAccess(a)
	if err != nil {
		return Session{}, err
	}

	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_session
			  (id, tenant_id, user_id, device_label, ip, user_agent, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			a.SessionID, tenantID, userID, nullIfEmpty(c.Device),
			nullIfEmpty(c.IP), nullIfEmpty(c.UserAgent),
			time.Now().Add(s.tokens.RefreshTTL())); err != nil {
			return err
		}
		// Generation 1 of this session's refresh chain. See migration 0007.
		if _, err := tx.Exec(ctx, `
			INSERT INTO session_refresh_token
			  (session_id, tenant_id, token_hash, expires_at, generation)
			VALUES ($1,$2,$3,$4,1)`,
			a.SessionID, tenantID, refreshHash,
			time.Now().Add(s.tokens.RefreshTTL())); err != nil {
			return err
		}

		// Clear the failure counter and, if the stored hash predates current
		// policy, upgrade it now that we hold the plaintext.
		newHash := passwordHash
		if NeedsRehash(passwordHash) {
			if h, hErr := HashPassword(c.Password); hErr == nil {
				newHash = h
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app_user
			SET failed_attempts = 0, locked_until = NULL,
			    last_login_at = now(), password_hash = $2,
			    status = CASE WHEN status = 'invited' THEN 'active'::user_status
			                  ELSE status END
			WHERE id = $1`, userID, newHash); err != nil {
			return err
		}

		return writeAudit(ctx, tx, auditEntry{
			TenantID: tenantID, ActorID: &userID, Action: "login",
			EntityType: "app_user", EntityID: &userID, IP: c.IP,
			Device: c.Device,
		})
	})
	if err != nil {
		return Session{}, db.Translate(err, "")
	}

	return Session{
		AccessToken:        accessToken,
		RefreshToken:       refreshToken,
		ExpiresAt:          expiresAt,
		MustChangePassword: mustChange,
		Actor:              a,
	}, nil
}

// dummyHash is a real argon2id hash of a random value, used to equalise timing
// when no account matches. Verifying against it costs the same as a genuine
// check, so response time reveals nothing.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=4$" +
	"c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhcw"

// recordFailedAttempt increments the counter and locks the account at the
// threshold. Failures here are logged, never surfaced: a database problem
// while recording a bad password must not become a different error message
// that distinguishes one failure from another.
func (s *Service) recordFailedAttempt(ctx context.Context, userID uuid.UUID, attempts int) {
	_ = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var lockUntil any
		if attempts >= MaxFailedAttempts {
			lockUntil = time.Now().Add(LockoutDuration)
		}
		_, err := tx.Exec(ctx, `
			UPDATE app_user SET failed_attempts = $2, locked_until = $3
			WHERE id = $1`, userID, attempts, lockUntil)
		return err
	})
}

// Refresh exchanges a refresh token for a new pair.
//
// Rotation is unconditional: the presented token is revoked and a new one
// issued. A token presented twice therefore means it was captured, so the whole
// session family is revoked rather than merely refused — refusing the replay
// alone would leave the thief's copy working.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	hash := HashRefreshToken(refreshToken)

	var (
		tokenID    uuid.UUID
		sessionID  uuid.UUID
		userID     uuid.UUID
		tenantID   *uuid.UUID
		generation int
		tokenExp   time.Time
		usedAt     *time.Time
		revokedAt  *time.Time
	)

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT t.id, t.session_id, s.user_id, s.tenant_id, t.generation,
			       t.expires_at, t.used_at, s.revoked_at
			FROM session_refresh_token t
			JOIN user_session s ON s.id = t.session_id
			WHERE t.token_hash = $1`, hash).
			Scan(&tokenID, &sessionID, &userID, &tenantID, &generation,
				&tokenExp, &usedAt, &revokedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A token that never existed. Noise — a typo or a stale client.
			return Session{}, errs.New(errs.CodeUnauthenticated,
				"Your session has expired. Please sign in again.")
		}
		return Session{}, db.Translate(err, "")
	}

	// A token that DID exist and has already been exchanged is different in
	// kind. The legitimate client discards its token the instant it rotates, so
	// only a captured copy is ever presented twice. Refusing just this request
	// would leave the thief's newer token working, so the whole family goes.
	if usedAt != nil {
		_ = s.revokeFamily(ctx, sessionID, userID, tenantID, generation)
		return Session{}, errs.New(errs.CodeUnauthenticated,
			"Your session was ended for security reasons. Please sign in again.")
	}

	if revokedAt != nil || tokenExp.Before(time.Now()) {
		return Session{}, errs.New(errs.CodeUnauthenticated,
			"Your session has expired. Please sign in again.")
	}

	a := actor.Actor{UserID: userID, SessionID: sessionID}
	if tenantID != nil {
		a.TenantID = *tenantID
	} else {
		a.IsSuperAdmin = true
	}

	newToken, newHash, err := NewRefreshToken()
	if err != nil {
		return Session{}, err
	}
	accessToken, accessExp, err := s.tokens.IssueAccess(a)
	if err != nil {
		return Session{}, err
	}

	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Conditional on used_at still being NULL, so two concurrent refreshes
		// with the same token cannot both succeed. The loser affects zero rows
		// and is refused.
		tag, execErr := tx.Exec(ctx, `
			UPDATE session_refresh_token SET used_at = now()
			WHERE id = $1 AND used_at IS NULL`, tokenID)
		if execErr != nil {
			return execErr
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeUnauthenticated,
				"Your session was ended for security reasons. Please sign in again.")
		}

		if _, execErr := tx.Exec(ctx, `
			INSERT INTO session_refresh_token
			  (session_id, tenant_id, token_hash, expires_at, generation)
			VALUES ($1,$2,$3,$4,$5)`,
			sessionID, tenantID, newHash,
			time.Now().Add(s.tokens.RefreshTTL()), generation+1); execErr != nil {
			return execErr
		}

		_, execErr = tx.Exec(ctx, `
			UPDATE user_session SET last_seen_at = now(), expires_at = $2
			WHERE id = $1`, sessionID, time.Now().Add(s.tokens.RefreshTTL()))
		return execErr
	})
	if err != nil {
		if errs.As(err) != nil {
			return Session{}, err
		}
		return Session{}, db.Translate(err, "")
	}

	return Session{
		AccessToken:  accessToken,
		RefreshToken: newToken,
		ExpiresAt:    accessExp,
		Actor:        a,
	}, nil
}

// revokeFamily ends every session the user holds after a token replay.
//
// The audit entry records the generation at which reuse was noticed, which is
// the one fact that makes the incident investigable afterwards: it says how
// many rotations the legitimate client completed before the stolen copy
// surfaced, and therefore roughly when the capture happened.
func (s *Service) revokeFamily(
	ctx context.Context, sessionID, userID uuid.UUID, tenantID *uuid.UUID, generation int,
) error {
	return s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE user_session
			SET revoked_at = now(), revoked_reason = 'refresh token reuse detected'
			WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
			return err
		}
		// Spend every outstanding token so the thief's newer copy dies too.
		if _, err := tx.Exec(ctx, `
			UPDATE session_refresh_token SET used_at = coalesce(used_at, now())
			WHERE session_id IN (SELECT id FROM user_session WHERE user_id = $1)`,
			userID); err != nil {
			return err
		}
		return writeAudit(ctx, tx, auditEntry{
			TenantID: tenantID, ActorID: &userID,
			Action:     "session_revoked_token_reuse",
			EntityType: "user_session", EntityID: &sessionID,
			After: map[string]any{
				"reason":            "a refresh token was presented twice",
				"reused_generation": generation,
			},
		})
	})
}

// Logout revokes the current session.
func (s *Service) Logout(ctx context.Context, sessionID uuid.UUID) error {
	return s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE user_session
			SET revoked_at = now(), revoked_reason = 'signed out'
			WHERE id = $1 AND revoked_at IS NULL`, sessionID)
		return err
	})
}

// RevokeAllForUser ends every session a user holds. Used on password change,
// on suspected token theft, and by the Owner revoking a device remotely.
func (s *Service) RevokeAllForUser(ctx context.Context, userID uuid.UUID, reason string) error {
	return s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE user_session
			SET revoked_at = now(), revoked_reason = $2
			WHERE user_id = $1 AND revoked_at IS NULL`, userID, reason)
		return err
	})
}

// ChangePassword sets a new password for the signed-in user.
//
// Every other session is revoked. A password change is usually a response to a
// suspected compromise, and leaving the attacker's session alive would defeat
// the point of changing it.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, current, next string) error {
	var stored string
	var tenantID *uuid.UUID

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT password_hash, tenant_id FROM app_user WHERE id = $1`, userID).
			Scan(&stored, &tenantID)
	})
	if err != nil {
		return db.Translate(err, "That account does not exist.")
	}

	ok, err := VerifyPassword(stored, current)
	if err != nil {
		return err
	}
	if !ok {
		return errs.New(errs.CodeUnauthenticated, "Your current password is not correct.")
	}
	if current == next {
		return errs.New(errs.CodeInvalidInput,
			"Your new password must be different from your current one.")
	}

	hash, err := HashPassword(next)
	if err != nil {
		return err
	}

	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE app_user
			SET password_hash = $2, must_change_password = false,
			    failed_attempts = 0, locked_until = NULL,
			    status = CASE WHEN status = 'invited' THEN 'active'::user_status
			                  ELSE status END
			WHERE id = $1`, userID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE user_session
			SET revoked_at = now(), revoked_reason = 'password changed'
			WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
			return err
		}
		return writeAudit(ctx, tx, auditEntry{
			TenantID: tenantID, ActorID: &userID, Action: "password_changed",
			EntityType: "app_user", EntityID: &userID,
		})
	})
	return db.Translate(err, "")
}

// --- Super-Admin-assisted recovery (blueprint A4.2) --------------------

// RecoveryResult carries the one-time password back to the Super Admin, who
// passes it to the Owner through a channel they have already verified.
type RecoveryResult struct {
	TemporaryPassword string `json:"temporary_password"`
}

// ResetPasswordAsSuperAdmin issues a new one-time password for a user.
//
// Blueprint A4.2 fixes the shape of this flow, and one property of it is
// structural rather than procedural: the Super Admin never sees the existing
// password, because the column holds an irreversible hash. There is no
// decryption function to call even with full database access. That is why the
// only possible outcome is a NEW password.
//
// Every assisted recovery is permanently audit-logged with who requested it,
// who approved it, and when.
func (s *Service) ResetPasswordAsSuperAdmin(
	ctx context.Context, targetUserID uuid.UUID, reason string,
) (RecoveryResult, error) {
	a := actor.From(ctx)
	if !a.IsSuperAdmin {
		return RecoveryResult{}, errs.New(errs.CodeForbidden,
			"Only a platform administrator can reset an account this way.")
	}
	if len(reason) < 10 {
		return RecoveryResult{}, errs.New(errs.CodeInvalidInput,
			"Record how you verified this person's identity before resetting their password.")
	}

	temp, err := GenerateTemporaryPassword()
	if err != nil {
		return RecoveryResult{}, err
	}
	hash, err := HashPassword(temp)
	if err != nil {
		return RecoveryResult{}, err
	}

	var tenantID *uuid.UUID
	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT tenant_id FROM app_user WHERE id = $1`, targetUserID).
			Scan(&tenantID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app_user
			SET password_hash = $2, must_change_password = true,
			    failed_attempts = 0, locked_until = NULL
			WHERE id = $1`, targetUserID, hash); err != nil {
			return err
		}
		// Any session opened with the old password is no longer trustworthy.
		if _, err := tx.Exec(ctx, `
			UPDATE user_session
			SET revoked_at = now(), revoked_reason = 'administrator password reset'
			WHERE user_id = $1 AND revoked_at IS NULL`, targetUserID); err != nil {
			return err
		}
		return writeAudit(ctx, tx, auditEntry{
			TenantID: tenantID, ActorID: &a.UserID,
			Action:     "password_reset_by_super_admin",
			EntityType: "app_user", EntityID: &targetUserID,
			After: map[string]any{"reason": reason, "requires_change": true},
		})
	})
	if err != nil {
		return RecoveryResult{}, db.Translate(err, "That account does not exist.")
	}

	return RecoveryResult{TemporaryPassword: temp}, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
