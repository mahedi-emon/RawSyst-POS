//go:build integration

package identity

import (
	"context"
	"net"
	"testing"

	"github.com/jackc/pgx/v5"
)

// captures what was queued, so a test can read the code without a mailbox.
type captureQueue struct{ sent []NotifyPayload }

func (c *captureQueue) QueueNotification(
	_ context.Context, _ pgx.Tx, p NotifyPayload,
) error {
	c.sent = append(c.sent, p)
	return nil
}

// The whole flow, end to end. Blueprint A4.2 puts self-service ahead of the
// phone call to Super Admin, and this is what "self-service" has to mean.
func TestAForgottenPasswordCanBeRecoveredWithACode(t *testing.T) {
	svc, pool := testService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	userID, _ := seedUser(t, pool, email)

	q := &captureQueue{}
	if err := svc.RequestReset(ctx, email, net.ParseIP("203.0.113.9"), q); err != nil {
		t.Fatalf("requesting: %v", err)
	}
	if len(q.sent) != 1 {
		t.Fatalf("queued %d messages, want 1", len(q.sent))
	}

	code := q.sent[0].Code
	if len(code) != ResetCodeLength {
		t.Errorf("code %q is %d digits, want %d", code, len(code), ResetCodeLength)
	}

	const chosen = "a-password-they-chose-themselves-9"
	if err := svc.CompleteReset(ctx, email, code, chosen); err != nil {
		t.Fatalf("completing: %v", err)
	}

	// The point of the whole exercise.
	if _, err := svc.Login(ctx, Credentials{Email: email, Password: chosen}); err != nil {
		t.Fatalf("the recovered password does not work: %v", err)
	}

	// And they are NOT asked to change it again. They just chose it — unlike a
	// temporary password somebody else issued them.
	var mustChange bool
	if err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT must_change_password FROM app_user WHERE id = $1`, userID).
			Scan(&mustChange)
	}); err != nil {
		t.Fatalf("reading the flag: %v", err)
	}
	if mustChange {
		t.Error("somebody who just chose their own password is being asked to " +
			"choose again, which is the product not believing what just happened")
	}
}

// A code is single use. The row is kept rather than deleted precisely so a
// replay is distinguishable from a guess — see the migration.
func TestACodeCannotBeUsedTwice(t *testing.T) {
	svc, pool := testService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	seedUser(t, pool, email)
	q := &captureQueue{}
	if err := svc.RequestReset(ctx, email, nil, q); err != nil {
		t.Fatalf("requesting: %v", err)
	}
	code := q.sent[0].Code

	if err := svc.CompleteReset(ctx, email, code, "first-choice-password-77"); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := svc.CompleteReset(ctx, email, code, "second-choice-password-88"); err == nil {
		t.Fatal("the same code was accepted twice, so an intercepted code stays " +
			"live after its owner has used it")
	}
}

// Guessing is bounded. Five wrong answers kill the code, so a six-digit space
// cannot be walked inside its ten-minute life.
func TestGuessingBurnsTheCode(t *testing.T) {
	svc, pool := testService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	seedUser(t, pool, email)
	q := &captureQueue{}
	if err := svc.RequestReset(ctx, email, nil, q); err != nil {
		t.Fatalf("requesting: %v", err)
	}
	real := q.sent[0].Code

	wrong := "000000"
	if wrong == real {
		wrong = "111111"
	}
	for i := 0; i < MaxResetAttempts; i++ {
		if err := svc.CompleteReset(ctx, email, wrong, "whatever-password-11"); err == nil {
			t.Fatal("a wrong code was accepted")
		}
	}

	// The real one no longer works either. That is the design: the code is
	// dead, not the account, and asking for a new one is one click.
	if err := svc.CompleteReset(ctx, email, real, "too-late-password-22"); err == nil {
		t.Error("the code survived five wrong guesses, so the space can be walked")
	}
}

// Asking about an address that does not exist must be indistinguishable from
// asking about one that does. An endpoint that answers differently is a tool
// for confirming which of a leaked address list are customers.
func TestAskingAboutAnUnknownAddressSaysNothing(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()

	q := &captureQueue{}
	if err := svc.RequestReset(ctx, "nobody-here@example.test", nil, q); err != nil {
		t.Fatalf("an unknown address produced an error, which is an answer: %v", err)
	}
	if len(q.sent) != 0 {
		t.Error("a message was queued for an address with no account")
	}
}

// The hourly ceiling is per ACCOUNT, so changing IP address does not buy a
// fresh allowance to post mail at somebody.
func TestAnAccountCannotBeMailBombed(t *testing.T) {
	svc, pool := testService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	seedUser(t, pool, email)
	q := &captureQueue{}
	for i := 0; i < MaxResetRequestsPerHour+3; i++ {
		// A different source every time. The limit is not about the source.
		if err := svc.RequestReset(ctx, email, net.ParseIP("198.51.100.7"), q); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	if len(q.sent) > MaxResetRequestsPerHour {
		t.Errorf("%d messages were queued for one account in an hour, want at "+
			"most %d — this endpoint can be used to post mail at a stranger",
			len(q.sent), MaxResetRequestsPerHour)
	}
}

// A suspended person does not get a way back in. Somebody who has left the
// business must not be able to recover their own login; the person who
// suspended them is the one to ask.
func TestASuspendedAccountCannotRecoverItself(t *testing.T) {
	svc, pool := testService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	userID, _ := seedUser(t, pool, email)

	if err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE app_user SET status = 'disabled' WHERE id = $1`, userID)
		return e
	}); err != nil {
		t.Fatalf("suspending: %v", err)
	}

	q := &captureQueue{}
	if err := svc.RequestReset(ctx, email, nil, q); err != nil {
		t.Fatalf("requesting: %v", err)
	}
	if len(q.sent) != 0 {
		t.Error("a suspended account was sent a recovery code, so somebody who " +
			"has left can let themselves back in")
	}
}

// Codes are stored hashed, like passwords. For the ten minutes one is alive it
// is exactly as good as the password it replaces, and a leaked backup that
// yields live codes is a leaked backup that yields accounts.
func TestTheCodeIsNotStoredInTheClear(t *testing.T) {
	svc, pool := testService(t)
	ctx := context.Background()
	email := uniqueEmail(t)
	seedUser(t, pool, email)

	q := &captureQueue{}
	if err := svc.RequestReset(ctx, email, nil, q); err != nil {
		t.Fatalf("requesting: %v", err)
	}
	code := q.sent[0].Code

	var stored string
	if err := pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT r.code_hash FROM password_reset_request r
			JOIN app_user u ON u.id = r.user_id
			WHERE u.email = $1 ORDER BY r.requested_at DESC LIMIT 1`, email).
			Scan(&stored)
	}); err != nil {
		t.Fatalf("reading the row: %v", err)
	}

	if stored == code {
		t.Fatal("the reset code is stored in the clear")
	}
	// argon2id, the same as a password.
	if len(stored) < 20 || stored[:9] != "$argon2id" {
		t.Errorf("the stored code does not look like an argon2id hash: %q", stored)
	}
}
