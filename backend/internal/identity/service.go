package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/secrets"
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

	// cipher seals the TOTP secret. Optional: an installation with no data
	// encryption key cannot hold a second factor and says so when somebody
	// tries to set one up, rather than storing the secret in the clear.
	cipher *secrets.Cipher
}

func NewService(pool *db.Pool, tokens *TokenService) *Service {
	return &Service{pool: pool, tokens: tokens}
}

// WithCipher supplies the keyring that seals second-factor secrets.
//
// A setter rather than a constructor argument, the way the server takes its
// queue: every existing call site keeps working, and an installation without a
// key refuses MFA enrolment instead of failing to start.
func (s *Service) WithCipher(c *secrets.Cipher) *Service {
	s.cipher = c
	return s
}

// Credentials is a sign-in attempt.
type Credentials struct {
	Email     string
	Password  string
	IP        string
	UserAgent string
	Device    string

	// TenantID names which account to sign in to, when one email belongs to
	// more than one.
	//
	// It is a FILTER on the lookup, never a grant. The account is still found
	// by (tenant_id, email) and its own password still has to verify, so a
	// caller naming a tenant they have no account in gets the same refusal as
	// a caller naming one that does not exist. Nothing here trusts the value
	// beyond narrowing a query.
	TenantID *uuid.UUID

	// MFACode is the six-digit code from the authenticator, or a recovery
	// code. Empty on the first attempt: the caller does not know a second
	// factor is wanted until the challenge comes back.
	MFACode string

	// DeviceID binds this session to the terminal it was opened on.
	//
	// Resolved by the handler from the terminal's own secret, never taken from
	// the request body — a cashier must not be able to name a till they are not
	// standing at. Present only when signing in on a paired terminal, and it is
	// what lets a sale record which till rang it up and which chain it belongs
	// to.
	DeviceID uuid.UUID
}

// Session is what a successful sign-in returns.
type Session struct {
	AccessToken        string      `json:"access_token"`
	RefreshToken       string      `json:"refresh_token"`
	ExpiresAt          time.Time   `json:"expires_at"`
	MustChangePassword bool        `json:"must_change_password"`
	Actor              actor.Actor `json:"-"`

	// Choices is non-empty when the email and password matched accounts in
	// more than one business and the caller has to say which. No tokens are
	// issued in that case: this is a challenge, not a session.
	//
	// Only businesses where the password ACTUALLY VERIFIED appear here.
	// Listing every tenant holding the address would hand an attacker a map of
	// which organisations a person belongs to, in exchange for a password they
	// do not have.
	Choices []TenantChoice `json:"tenants,omitempty"`

	// MFARequired is true when the password was right and the account has a
	// second factor. No tokens are issued: like Choices, this is a challenge.
	//
	// It is set only AFTER the password verified, which is deliberate. Telling
	// somebody an account has MFA before they have proved they know its
	// password would be telling them the account exists.
	MFARequired bool `json:"mfa_required,omitempty"`
}

// TenantChoice is one business a signed-in email can belong to.
type TenantChoice struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Name     string    `json:"name"`
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

// candidate is one account an email could refer to.
type candidate struct {
	userID       uuid.UUID
	tenantID     *uuid.UUID
	tenantName   string
	passwordHash string
	status       string
	mustChange   bool
	failed       int
	lockedUntil  *time.Time
}

// maxCandidates caps how many accounts one email may be checked against.
//
// Each check is a deliberately slow password hash, so an address planted in
// many tenants would otherwise be a cheap way to make one request cost seconds
// of CPU. Ten is far beyond any real person's number of employers and cheap
// enough to be harmless.
const maxCandidates = 10

// Login authenticates a user and opens a session.
//
// This runs on the platform plane rather than a tenant plane, because at this
// point we do not yet know which tenant the caller belongs to — that is what
// the lookup determines.
//
// # One email can belong to several businesses
//
// Email is unique WITHIN a tenant, which is correct: a bookkeeper serving two
// shops, an owner with two companies and a shared ops address are all ordinary.
// This used to look the account up with a bare `WHERE email = $1` and take
// whichever row came back, which meant one of those people could never sign in
// and the refusal said their password was wrong. That was P20.
//
// So every account for the address is now a candidate, and which one signs in
// is resolved as follows:
//
//   - The password is checked against each. Different tenants may hold
//     different passwords for the same person, and only the ones that verify
//     are theirs.
//   - Exactly one verifies: they are signed in, with no extra step. This is the
//     overwhelmingly common case, including every single-tenant deployment, and
//     its behaviour is byte-for-byte what it was before.
//   - Several verify: no tokens are issued and the businesses are returned for
//     the caller to choose between. The caller then repeats the sign-in naming
//     one, and the whole check runs again from scratch against that tenant.
//   - None verify: the same generic refusal as always.
func (s *Service) Login(ctx context.Context, c Credentials) (Session, error) {
	candidates, err := s.candidatesFor(ctx, c.Email, c.TenantID)
	if err != nil {
		return Session{}, err
	}

	if len(candidates) == 0 {
		// Hash a dummy password anyway. Returning immediately would make a
		// non-existent account measurably faster to reject than a real one,
		// reintroducing the enumeration the generic message exists to prevent.
		// This also covers a tenant_id the caller has no account in, so naming
		// somebody else's business is indistinguishable from guessing wrong.
		_, _ = VerifyPassword(dummyHash, c.Password)
		return Session{}, errInvalidCredentials
	}

	// Which of them the password actually opens.
	matched := make([]candidate, 0, len(candidates))
	for _, cand := range candidates {
		ok, vErr := VerifyPassword(cand.passwordHash, c.Password)
		if vErr != nil {
			return Session{}, vErr
		}
		if ok {
			matched = append(matched, cand)
			continue
		}
		// Counted per account, so a wrong password does not lock the person
		// out of a different business they are also in.
		s.recordFailedAttempt(ctx, cand.userID, cand.failed+1)
	}

	if len(matched) == 0 {
		return Session{}, errInvalidCredentials
	}

	if len(matched) > 1 {
		choices := make([]TenantChoice, 0, len(matched))
		for _, cand := range matched {
			// A Super Admin has no tenant and cannot be one of several: the
			// platform email index is globally unique. Guarded anyway rather
			// than dereferenced on faith.
			if cand.tenantID == nil {
				continue
			}
			choices = append(choices, TenantChoice{
				TenantID: *cand.tenantID, Name: cand.tenantName,
			})
		}
		if len(choices) > 1 {
			return Session{Choices: choices}, nil
		}
	}

	chosen := matched[0]

	// The lockout is checked AFTER the password, and only on the account being
	// entered. Checking it first would tell an attacker that an address exists
	// in a given tenant without knowing the password.
	if chosen.lockedUntil != nil && chosen.lockedUntil.After(time.Now()) {
		return Session{}, errs.Newf(errs.CodeUnauthenticated,
			"This account is temporarily locked after too many failed sign-in "+
				"attempts. Try again in %d minutes, or ask your owner to reset it.",
			int(time.Until(*chosen.lockedUntil).Minutes())+1)
	}

	userID := chosen.userID
	tenantID := chosen.tenantID
	passwordHash := chosen.passwordHash
	status := chosen.status
	mustChange := chosen.mustChange
	var lockedUntil *time.Time
	if lockedUntil != nil && lockedUntil.After(time.Now()) {
		return Session{}, errs.Newf(errs.CodeUnauthenticated,
			"This account is temporarily locked after too many failed sign-in "+
				"attempts. Try again in %d minutes, or ask your owner to reset it.",
			int(time.Until(*lockedUntil).Minutes())+1)
	}

	// The password was already verified against this candidate above, which is
	// what selected it. Re-checking here would double the cost of the slowest
	// operation in the request for no additional assurance.
	_ = lockedUntil

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

	// The second factor, after the password and after the status check.
	//
	// Both orderings matter. After the password, so a challenge cannot be used
	// to discover that an account exists; after the status check, so a
	// disabled account is refused as disabled rather than asked for a code it
	// will never be allowed to use.
	if err := s.requireSecondFactor(ctx, userID, c.MFACode, c.IP); err != nil {
		if errors.Is(err, errMFARequired) {
			return Session{MFARequired: true}, nil
		}
		return Session{}, err
	}

	a := actor.Actor{UserID: userID, SessionID: uuid.New()}
	if tenantID != nil {
		a.TenantID = *tenantID
	} else {
		a.IsSuperAdmin = true
	}
	a.DeviceID = c.DeviceID

	// The companies this user is confined to, carried in the token so every
	// later request can be checked against them without a database round trip.
	//
	// Nothing filled this in before, and the consequence was quiet: the claim
	// existed, the parser read it back, and CanAccessCompany consulted it --
	// but an empty list means "every company in the tenant", so the check
	// always passed. A user scoped to one company could act in another.
	scopes, err := s.companyScopesFor(ctx, a.TenantID, userID)
	if err != nil {
		return Session{}, err
	}
	a.CompanyIDs = scopes

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
			  (id, tenant_id, user_id, device_label, ip, user_agent, expires_at,
			   device_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			a.SessionID, tenantID, userID, nullIfEmpty(c.Device),
			nullIfEmpty(c.IP), nullIfEmpty(c.UserAgent),
			time.Now().Add(s.tokens.RefreshTTL()),
			nullDevice(c.DeviceID)); err != nil {
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
		// The terminal this session was opened on, so a refresh mid-shift keeps
		// the binding rather than silently handing the till a token with no
		// terminal on it.
		deviceID *uuid.UUID
	)

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT t.id, t.session_id, s.user_id, s.tenant_id, t.generation,
			       t.expires_at, t.used_at, s.revoked_at, s.device_id
			FROM session_refresh_token t
			JOIN user_session s ON s.id = t.session_id
			WHERE t.token_hash = $1`, hash).
			Scan(&tokenID, &sessionID, &userID, &tenantID, &generation,
				&tokenExp, &usedAt, &revokedAt, &deviceID)
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
	if deviceID != nil {
		a.DeviceID = *deviceID
	}

	// Re-read on refresh rather than copied from the old token, so narrowing
	// somebody's scope takes effect at the next refresh instead of lasting as
	// long as they keep the session alive.
	scopes, err := s.companyScopesFor(ctx, a.TenantID, userID)
	if err != nil {
		return Session{}, err
	}
	a.CompanyIDs = scopes

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

// companyScopesFor returns the legal entities a user is confined to.
//
// nil means unconfined -- every company in their tenant -- which is both the
// common case and what CanAccessCompany reads an empty list as.
//
// The union rule matches the authorizer's: an unscoped assignment WINS. Someone
// holding "manager of Alpha Trading" and "auditor, all companies" is an auditor
// of all companies, because the wider grant was given deliberately. Intersecting
// instead would silently take away access somebody was meant to have.
//
// Expired and future-dated assignments are excluded here for the same reason
// the resolver excludes them: a seasonal role should stop granting scope when it
// stops granting permissions, not carry a stale company along with it.
func (s *Service) companyScopesFor(
	ctx context.Context, tenantID uuid.UUID, userID uuid.UUID,
) ([]uuid.UUID, error) {
	// A platform administrator belongs to no tenant, so there are no company
	// assignments to read. What they may reach is limited by migration 0006 to
	// administration tables, not by this dimension.
	if tenantID == uuid.Nil {
		return nil, nil
	}

	var scoped []uuid.UUID
	unscoped := false

	// As the TENANT, not as the platform. user_role_assignment carries
	// `USING (tenant_id = current_tenant_id())` and no is_platform_admin()
	// escape, so a platform transaction matches no rows at all -- and zero rows
	// reads as "no confinement", which is the fail-OPEN direction. The first cut
	// of this made exactly that mistake and the scope test caught it.
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT company_id
			  FROM user_role_assignment
			 WHERE user_id = $1
			   AND (valid_from  IS NULL OR valid_from  <= now())
			   AND (valid_until IS NULL OR valid_until  > now())`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()

		seen := map[uuid.UUID]bool{}
		for rows.Next() {
			var companyID *uuid.UUID
			if err := rows.Scan(&companyID); err != nil {
				return err
			}
			if companyID == nil {
				unscoped = true
				continue
			}
			if !seen[*companyID] {
				seen[*companyID] = true
				scoped = append(scoped, *companyID)
			}
		}
		return rows.Err()
	})
	if err != nil {
		// Refused rather than defaulted to unscoped. Failing open here would
		// turn a database blip into a silent widening of access, which is the
		// one direction an authorization failure must never take.
		return nil, db.Translate(err, "Your access could not be resolved.")
	}

	if unscoped {
		return nil, nil
	}
	return scoped, nil
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

// candidatesFor returns every account the email could mean.
//
// Ordered by tenant so a picker is stable between attempts — a list that
// reshuffled would have somebody choosing the wrong business by muscle memory.
//
// A tenant filter, when supplied, is applied in SQL. That is the only thing the
// client's tenant_id does: it cannot widen the result, only narrow it, and the
// password still has to verify against whatever comes back.
func (s *Service) candidatesFor(
	ctx context.Context, email string, tenantID *uuid.UUID,
) ([]candidate, error) {
	var out []candidate

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT u.id, u.tenant_id, coalesce(t.name, ''), u.password_hash,
			       u.status, u.must_change_password, u.failed_attempts,
			       u.locked_until
			FROM app_user u
			LEFT JOIN tenant t ON t.id = u.tenant_id
			WHERE u.email = $1
			  AND ($2::uuid IS NULL OR u.tenant_id = $2::uuid)
			ORDER BY t.name, u.tenant_id
			LIMIT $3`, email, tenantID, maxCandidates)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.userID, &c.tenantID, &c.tenantName,
				&c.passwordHash, &c.status, &c.mustChange, &c.failed,
				&c.lockedUntil); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return out, nil
}

// nullDevice keeps "signed in from a browser" distinct from "signed in on the
// zero terminal", which is not a terminal at all.
func nullDevice(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// errMFARequired is the sentinel that turns into a challenge rather than a
// failure. Not an errs.Error: it never reaches a caller as one.
var errMFARequired = errors.New("second factor required")

// requireSecondFactor refuses unless the account either has no second factor or
// the code given satisfies it.
//
// Runs on the platform plane, in one transaction, because a recovery code is
// SPENT by the check and that write must not be separable from the decision it
// made.
func (s *Service) requireSecondFactor(
	ctx context.Context, userID uuid.UUID, code, ip string,
) error {
	return s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var enabled bool
		if err := tx.QueryRow(ctx,
			`SELECT mfa_enabled FROM app_user WHERE id = $1`,
			userID).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		if strings.TrimSpace(code) == "" {
			return errMFARequired
		}

		ok, err := s.checkSecondFactor(ctx, tx, userID, code, ip)
		if err != nil {
			return err
		}
		if !ok {
			// The same generic refusal as a wrong password, and for the same
			// reason: a message distinguishing "wrong code" from "wrong
			// recovery code" tells an attacker which of the two they are
			// closer to guessing.
			return errInvalidCredentials
		}
		return nil
	})
}
