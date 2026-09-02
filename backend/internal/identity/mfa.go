package identity

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The second factor (blueprint H1).
//
// # Enrolment is two steps, and the first one commits nothing
//
// `BeginMFA` generates a secret and hands back the QR payload. It does NOT turn
// the second factor on. `CompleteMFA` does, and only after the person has typed
// a code the secret actually produces — which is the only proof that the app on
// their phone holds the same secret the server does.
//
// A one-step enrolment is how somebody locks themselves out on the spot: they
// scan a code, the flag flips, the scan silently failed, and the next sign-in
// asks for a number nothing can generate.
//
// # Recovery codes are issued once and shown once
//
// Ten of them, at the moment the second factor is turned on. They are hashed
// immediately and the plaintext exists only in that one response — a screen
// that could show them again is a screen an attacker with a live session can
// use to mint a way back in after the password is changed.
//
// # A recovery code is spent, not merely accepted
//
// Stamped `used_at` in the same transaction that lets the person in, so two
// requests racing with the same code cannot both succeed. The row is kept for
// the reason 0076 gives about reset codes: a deleted one cannot tell a replay
// from a guess.

// recoveryCodeCount is how many are issued. Ten is enough for a person who
// keeps losing phones and few enough to fit on the piece of paper they will
// actually write them on.
const recoveryCodeCount = 10

// MFAEnrolment is what a person needs to set the second factor up.
type MFAEnrolment struct {
	// Secret in base32, for somebody who cannot scan and has to type it.
	Secret string `json:"secret"`
	// URI is the otpauth:// payload a QR code encodes.
	URI string `json:"uri"`
}

// MFAStatus is what a settings screen shows.
type MFAStatus struct {
	Enabled    bool   `json:"enabled"`
	EnrolledAt string `json:"enrolled_at,omitempty"`
	// RecoveryRemaining counts the codes still unspent, so somebody who has
	// used eight of ten is told before they use the ninth.
	RecoveryRemaining int `json:"recovery_remaining"`
}

// MFAStatus reads it.
func (s *Service) MFAStatus(
	ctx context.Context, userID uuid.UUID,
) (MFAStatus, error) {
	var out MFAStatus
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var enrolled *time.Time
		if e := tx.QueryRow(ctx, `
			SELECT mfa_enabled, mfa_enrolled_at FROM app_user WHERE id = $1`,
			userID).Scan(&out.Enabled, &enrolled); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound, "That account was not found.")
			}
			return e
		}
		if enrolled != nil {
			out.EnrolledAt = enrolled.UTC().Format(time.RFC3339)
		}
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM mfa_recovery_code
			 WHERE user_id = $1 AND used_at IS NULL`,
			userID).Scan(&out.RecoveryRemaining)
	})
	return out, db.Translate(err, "")
}

// BeginMFA generates a secret and returns what an authenticator app needs.
//
// The secret is stored sealed straight away, but `mfa_enabled` stays false: it
// is a pending enrolment until a code proves the phone has it. Beginning twice
// simply replaces the pending secret, which is what somebody who abandoned the
// first attempt expects.
func (s *Service) BeginMFA(
	ctx context.Context, userID uuid.UUID,
) (MFAEnrolment, error) {
	if s.cipher == nil {
		return MFAEnrolment{}, errs.New(errs.CodeUnavailable,
			"This installation has no encryption key, so a second factor "+
				"cannot be stored.")
	}

	secret, err := NewTOTPSecret()
	if err != nil {
		return MFAEnrolment{}, err
	}
	sealed, err := s.cipher.Seal([]byte(secret))
	if err != nil {
		return MFAEnrolment{}, err
	}

	var email string
	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			UPDATE app_user
			   SET mfa_secret_enc = $2, mfa_enabled = false,
			       mfa_enrolled_at = NULL
			 WHERE id = $1
			RETURNING email`, userID, sealed).Scan(&email)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That account was not found.")
		}
		return e
	})
	if err != nil {
		return MFAEnrolment{}, db.Translate(err, "")
	}

	return MFAEnrolment{
		Secret: secret,
		URI:    TOTPURI(secret, "RawSyst", email),
	}, nil
}

// CompleteMFA turns the second factor on, once a code proves the phone has the
// secret. It returns the recovery codes, which are shown once and never again.
func (s *Service) CompleteMFA(
	ctx context.Context, userID uuid.UUID, code string,
) ([]string, error) {
	if s.cipher == nil {
		return nil, errs.New(errs.CodeUnavailable,
			"This installation has no encryption key, so a second factor "+
				"cannot be stored.")
	}

	var codes []string
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var sealed []byte
		var tenantID *uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT mfa_secret_enc, tenant_id FROM app_user WHERE id = $1`,
			userID).Scan(&sealed, &tenantID)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That account was not found.")
		}
		if e != nil {
			return e
		}
		if len(sealed) == 0 {
			return errs.New(errs.CodeConflict,
				"Start setting up the second factor before confirming it.")
		}

		secret, e := s.cipher.Open(sealed)
		if e != nil {
			return e
		}
		if !VerifyTOTP(string(secret), code, time.Now()) {
			return errs.New(errs.CodeUnauthenticated,
				"That code is not right. Check the app is showing the current "+
					"one, and that the phone's clock is correct.")
		}

		if _, e := tx.Exec(ctx, `
			UPDATE app_user SET mfa_enabled = true, mfa_enrolled_at = now()
			 WHERE id = $1`, userID); e != nil {
			return e
		}

		// A fresh set. Any codes from a previous enrolment are spent, because
		// they were minted against a secret that is no longer the one in use.
		if _, e := tx.Exec(ctx, `
			UPDATE mfa_recovery_code SET used_at = now()
			 WHERE user_id = $1 AND used_at IS NULL`, userID); e != nil {
			return e
		}

		codes = make([]string, 0, recoveryCodeCount)
		for i := 0; i < recoveryCodeCount; i++ {
			plain, e := NewRecoveryCode()
			if e != nil {
				return e
			}
			hash, e := HashSecret(NormaliseRecoveryCode(plain))
			if e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO mfa_recovery_code (tenant_id, user_id, code_hash)
				VALUES ($1,$2,$3)`, tenantID, userID, hash); e != nil {
				return e
			}
			codes = append(codes, plain)
		}
		return nil
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return codes, nil
}

// DisableMFA turns the second factor off.
//
// A current code or a recovery code is required, and that is not ceremony: an
// attacker who has stolen a live session should not be able to remove the thing
// standing between them and the password they do not have.
func (s *Service) DisableMFA(
	ctx context.Context, userID uuid.UUID, code string,
) error {
	return db.Translate(s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		ok, e := s.checkSecondFactor(ctx, tx, userID, code, "")
		if e != nil {
			return e
		}
		if !ok {
			return errs.New(errs.CodeUnauthenticated,
				"That code is not right.")
		}
		if _, e := tx.Exec(ctx, `
			UPDATE app_user
			   SET mfa_enabled = false, mfa_secret_enc = NULL,
			       mfa_enrolled_at = NULL
			 WHERE id = $1`, userID); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `
			UPDATE mfa_recovery_code SET used_at = now()
			 WHERE user_id = $1 AND used_at IS NULL`, userID)
		return e
	}), "")
}

// RegenerateRecoveryCodes issues a fresh set and spends the old ones.
func (s *Service) RegenerateRecoveryCodes(
	ctx context.Context, userID uuid.UUID, code string,
) ([]string, error) {
	var codes []string
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		ok, e := s.checkSecondFactor(ctx, tx, userID, code, "")
		if e != nil {
			return e
		}
		if !ok {
			return errs.New(errs.CodeUnauthenticated,
				"That code is not right.")
		}

		var tenantID *uuid.UUID
		if e := tx.QueryRow(ctx,
			`SELECT tenant_id FROM app_user WHERE id = $1`,
			userID).Scan(&tenantID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `
			UPDATE mfa_recovery_code SET used_at = now()
			 WHERE user_id = $1 AND used_at IS NULL`, userID); e != nil {
			return e
		}

		codes = make([]string, 0, recoveryCodeCount)
		for i := 0; i < recoveryCodeCount; i++ {
			plain, e := NewRecoveryCode()
			if e != nil {
				return e
			}
			hash, e := HashSecret(NormaliseRecoveryCode(plain))
			if e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO mfa_recovery_code (tenant_id, user_id, code_hash)
				VALUES ($1,$2,$3)`, tenantID, userID, hash); e != nil {
				return e
			}
			codes = append(codes, plain)
		}
		return nil
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return codes, nil
}

// checkSecondFactor accepts either a TOTP code or an unspent recovery code.
//
// A recovery code that verifies is SPENT here, inside the caller's transaction,
// so two requests racing with the same one cannot both succeed.
//
// Returns true when the account has no second factor at all — the caller is
// responsible for deciding whether that is allowed, and every caller that must
// not allow it checks `mfa_enabled` first.
func (s *Service) checkSecondFactor(
	ctx context.Context, tx pgx.Tx, userID uuid.UUID, code, ip string,
) (bool, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return false, nil
	}

	var enabled bool
	var sealed []byte
	if err := tx.QueryRow(ctx,
		`SELECT mfa_enabled, mfa_secret_enc FROM app_user WHERE id = $1`,
		userID).Scan(&enabled, &sealed); err != nil {
		return false, err
	}

	// The authenticator first, because it is what somebody uses every day.
	if len(sealed) > 0 && s.cipher != nil {
		secret, err := s.cipher.Open(sealed)
		if err != nil {
			return false, err
		}
		if VerifyTOTP(string(secret), code, time.Now()) {
			return true, nil
		}
	}

	// Then the recovery codes. Every unspent one is checked, because the hash
	// is per-code and there is no index to look one up by.
	normalised := NormaliseRecoveryCode(code)
	rows, err := tx.Query(ctx, `
		SELECT id, code_hash FROM mfa_recovery_code
		 WHERE user_id = $1 AND used_at IS NULL`, userID)
	if err != nil {
		return false, err
	}
	type held struct {
		id   uuid.UUID
		hash string
	}
	var candidates []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.id, &h.hash); err != nil {
			rows.Close()
			return false, err
		}
		candidates = append(candidates, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	for _, h := range candidates {
		ok, err := VerifyPassword(h.hash, normalised)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		// Spent in the same transaction that accepts it.
		tag, err := tx.Exec(ctx, `
			UPDATE mfa_recovery_code
			   SET used_at = now(), used_ip = nullif($2,'')::inet
			 WHERE id = $1 AND used_at IS NULL`, h.id, ip)
		if err != nil {
			return false, err
		}
		// Zero rows means another request spent it between the read and the
		// write. That is a race, not a valid code, and it must not let both in.
		return tag.RowsAffected() == 1, nil
	}
	return false, nil
}
