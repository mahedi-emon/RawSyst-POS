// Getting back into an account you are locked out of.
//
// Blueprint A4.2 puts two routes in order: an Owner "first attempts normal
// self-service recovery (recovery email / OTP to registered phone)", and only
// "if self-service recovery fails or is unavailable" do they contact Super
// Admin. The second was built. The first was not, so a forgotten password was a
// phone call to the vendor — for every shop, at every hour.
//
// # Why asking never says whether the account exists
//
// `Request` returns the same thing for a real address and an invented one, and
// takes roughly the same time. An endpoint that answers "no such account" is a
// tool for confirming which of a leaked address list are customers of this
// product, and the shops on that list are then worth phishing. The cost is that
// somebody who mistypes their own address gets silence; the fix for that is the
// screen saying "if that address is on an account, a code is on its way",
// which is true and is what it says.
//
// # Why the code goes through the job queue
//
// Sending mail is I/O that fails: a provider is down, a mailbox is full, DNS is
// slow. Doing it inside the request would make the reset endpoint as reliable
// as the mail provider, and would hold a connection open while it happened. The
// queue already has retry with backoff, per-key serialisation and a dead
// letter — the same machinery ZATCA submission uses, for the same reason.
package identity

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

const (
	// How long a code is good for.
	//
	// Ten minutes is long enough to switch to a mail client and back on a slow
	// phone, and short enough that an intercepted code is usually already dead.
	// A4.2 gives no number, so this is chosen rather than derived, and it is
	// the kind of thing a Super Admin should eventually be able to set — A4
	// lists "session timeout, lockout thresholds" as global security policy.
	ResetCodeLifetime = 10 * time.Minute

	// Wrong guesses against one code before it is dead.
	//
	// A six-digit code has a million values. Five guesses is generous for
	// somebody reading it off a screen and useless for somebody searching the
	// space: at five per code, an attacker needs 200,000 requests, and the
	// hourly ceiling below caps those long before.
	MaxResetAttempts = 5

	// How many codes one account may be sent in an hour.
	//
	// This is the real defence. It bounds guessing, and it stops the endpoint
	// being used to post mail at somebody — an unlimited "email me a code" is a
	// button that sends a stranger a hundred emails.
	MaxResetRequestsPerHour = 3
)

// ResetCodeLength is six digits, which is what people expect from an OTP and
// what fits in a glance. The space is small, so everything above — expiry,
// attempt cap, request cap — is what makes it safe rather than the length.
const ResetCodeLength = 6

// NotifyPayload is what the worker is handed to send.
//
// The code travels in the job payload, in the clear, because it has to reach a
// mail body. It is alive for ten minutes and the job row is deleted by the
// pruner afterwards; the alternative — an encrypted payload the worker must
// decrypt — protects a ten-minute secret from somebody who already has the
// database.
type NotifyPayload struct {
	Kind     string `json:"kind"`
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Code     string `json:"code"`
	// ExpiresInMinutes so the mail can say it without doing arithmetic on a
	// timestamp in a template.
	ExpiresInMinutes int `json:"expires_in_minutes"`
}

// NotifyKindPasswordReset is the `notify.send` payload kind for a reset code.
const NotifyKindPasswordReset = "password_reset_code"

// Enqueuer is how recovery hands a message to the queue.
//
// An interface rather than the concrete queue, because `identity` must not
// import `jobs`: the queue already imports the database layer that identity
// sits on, and a cycle here would be a cycle between authentication and
// background work — two things that should be able to change independently.
type Enqueuer interface {
	// Queued inside the CALLER's transaction, so a code that is issued and a
	// message that will be sent commit together or not at all.
	QueueNotification(ctx context.Context, tx pgx.Tx, payload NotifyPayload) error
}

// RequestReset issues a code and queues it for sending.
//
// Always succeeds from the caller's point of view. See the file comment: an
// endpoint that distinguishes a real address from an invented one is a tool for
// confirming which of a leaked list are customers.
func (s *Service) RequestReset(
	ctx context.Context, email string, from net.IP, enqueue Enqueuer,
) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !strings.Contains(email, "@") {
		// Nothing to look up, and nothing to say about it.
		return nil
	}

	code, err := generateResetCode()
	if err != nil {
		return err
	}
	// `HashSecret`, not `HashPassword`. A six-digit code is not a human
	// password and would be rejected by a rule written for one — "at least 12
	// characters, a phrase you can remember" is good advice about a password
	// and nonsense about an OTP. What it shares with a password is that a
	// database copy must not yield a working credential, which is what the
	// hash is for; what makes it safe is the expiry, the attempt cap and the
	// hourly ceiling, not its length.
	hash, err := HashSecret(code)
	if err != nil {
		return err
	}

	return s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var (
			userID   uuid.UUID
			tenantID uuid.UUID
			fullName string
			status   string
		)
		err := tx.QueryRow(ctx, `
			SELECT id, tenant_id, full_name, status::text
			FROM app_user
			WHERE email = $1 AND tenant_id IS NOT NULL`, email).
			Scan(&userID, &tenantID, &fullName, &status)

		if errors.Is(err, pgx.ErrNoRows) {
			// No such account. The caller is told nothing, which is the point.
			return nil
		}
		if err != nil {
			return err
		}

		// A disabled account does not get a way back in. Somebody who has left
		// the business must not be able to recover their own login, and the
		// person who suspended them is the one who should be asked.
		if status == "disabled" {
			return nil
		}

		// The hourly ceiling. Checked here rather than at the handler because
		// it is a property of the ACCOUNT — an attacker changing IP address
		// should not get a fresh allowance to post mail at somebody.
		var recent int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)::int FROM password_reset_request
			WHERE user_id = $1 AND requested_at > now() - interval '1 hour'`,
			userID).Scan(&recent); err != nil {
			return err
		}
		if recent >= MaxResetRequestsPerHour {
			// Silently. Telling the caller they have hit a limit tells them
			// the address is real, which is the one thing this must not do.
			return nil
		}

		// Any earlier live code is spent. Two valid codes at once doubles the
		// guessing surface for no benefit — somebody who asked twice is
		// reading the newer mail.
		if _, err := tx.Exec(ctx, `
			UPDATE password_reset_request SET used_at = now()
			WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
			return err
		}

		var ip *string
		if from != nil {
			v := from.String()
			ip = &v
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO password_reset_request
			  (tenant_id, user_id, code_hash, expires_at, requested_ip)
			VALUES ($1, $2, $3, now() + $4::interval, $5::inet)`,
			tenantID, userID, hash,
			fmt.Sprintf("%d seconds", int(ResetCodeLifetime.Seconds())), ip,
		); err != nil {
			return err
		}

		// Queued INSIDE this transaction. If the send cannot be queued the code
		// is not issued either, which is the correct pairing: a code nobody can
		// be told about is a code that only burns the account's hourly
		// allowance.
		payload := NotifyPayload{
			Kind:             NotifyKindPasswordReset,
			Email:            email,
			FullName:         fullName,
			Code:             code,
			ExpiresInMinutes: int(ResetCodeLifetime.Minutes()),
		}
		return enqueue.QueueNotification(ctx, tx, payload)
	})
}

// CompleteReset exchanges a code for a new password.
func (s *Service) CompleteReset(
	ctx context.Context, email, code, newPassword string,
) error {
	email = strings.TrimSpace(strings.ToLower(email))
	code = strings.TrimSpace(code)

	// The same rules a chosen password is held to anywhere else. A recovery
	// route that accepted a weaker password than the change-password screen
	// would be the way round the rules rather than the way back in.
	if err := ValidatePasswordStrength(newPassword); err != nil {
		return err
	}

	// One refusal for every way this can fail.
	//
	// Wrong code, expired code, already-used code, unknown address: all of them
	// answer the same. Distinguishing them tells somebody holding a list of
	// addresses which are real, and tells somebody guessing a code whether they
	// are close.
	refuse := errs.New(errs.CodeUnauthenticated,
		"That code is not valid, or it has expired. Ask for a new one.")

	var userID uuid.UUID
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}

	// Whether the code was wrong, decided inside the transaction and acted on
	// outside it.
	//
	// This was a `return refuse` from inside the closure, and it was a hole:
	// returning an error rolls the transaction back, and the rollback took the
	// attempt counter with it. Five wrong guesses recorded nothing, the cap
	// never fired, and a six-digit code could be walked at whatever rate the
	// caller could manage — which is the entire defence, gone, while the code
	// that was supposed to provide it sat there looking correct.
	//
	// So the transaction COMMITS on a wrong code, having spent the guess, and
	// the refusal is returned afterwards.
	var wrongCode bool

	err = s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var (
			requestID uuid.UUID
			codeHash  string
			attempts  int
		)
		// The newest live request for this address, locked so two exchanges of
		// the same code cannot both succeed.
		e := tx.QueryRow(ctx, `
			SELECT r.id, r.user_id, r.code_hash, r.attempts
			FROM password_reset_request r
			JOIN app_user u ON u.id = r.user_id
			WHERE u.email = $1
			  AND u.tenant_id IS NOT NULL
			  AND r.used_at IS NULL
			  AND r.expires_at > now()
			  AND r.attempts < $2
			ORDER BY r.requested_at DESC
			LIMIT 1
			FOR UPDATE OF r`, email, MaxResetAttempts).
			Scan(&requestID, &userID, &codeHash, &attempts)

		if errors.Is(e, pgx.ErrNoRows) {
			return refuse
		}
		if e != nil {
			return e
		}

		ok, e := VerifyPassword(codeHash, code)
		if e != nil {
			return e
		}
		if !ok {
			// Recorded, and the transaction allowed to commit. See above: a
			// refusal returned from here would roll this write back and the
			// guess would be free.
			if _, e := tx.Exec(ctx, `
				UPDATE password_reset_request SET attempts = attempts + 1
				WHERE id = $1`, requestID); e != nil {
				return e
			}
			wrongCode = true
			return nil
		}

		if _, e := tx.Exec(ctx, `
			UPDATE password_reset_request SET used_at = now() WHERE id = $1`,
			requestID); e != nil {
			return e
		}

		// `must_change_password` is false: they have just chosen this one, and
		// asking them to choose again immediately is the product not believing
		// what just happened. Unlike an issued temporary password, which they
		// did not choose.
		_, e = tx.Exec(ctx, `
			UPDATE app_user
			   SET password_hash = $2,
			       must_change_password = false,
			       status = CASE WHEN status = 'invited'
			                     THEN 'active'::user_status ELSE status END,
			       failed_attempts = 0,
			       locked_until = NULL
			 WHERE id = $1`, userID, hash)
		return e
	})
	if err != nil {
		return err
	}
	if wrongCode {
		return refuse
	}

	// Every session ends. If the reset was a recovery, the old sessions are
	// stale; if it was an account takeover being undone, they are the
	// attacker's — and there is no way to tell which from here.
	return s.RevokeAllForUser(ctx, userID, "password reset by code")
}

// generateResetCode returns a uniformly random six-digit code.
//
// `crypto/rand` and rejection-free modular reduction over the exact range, not
// `rand.Intn` — a predictable reset code is a password with extra steps, and
// `math/rand` seeded from the clock is predictable to anybody who knows roughly
// when the request was made.
func generateResetCode() (string, error) {
	max := big.NewInt(1)
	for i := 0; i < ResetCodeLength; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	// Left-padded: "000123" is as valid a code as "912345", and dropping the
	// leading zeros would both shorten it and make it guessable-shorter.
	return fmt.Sprintf("%0*d", ResetCodeLength, n), nil
}
