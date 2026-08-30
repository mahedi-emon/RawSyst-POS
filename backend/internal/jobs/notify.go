// Sending a message, which is design 08's `notify.send`.
//
// The first thing that needs it is password recovery: blueprint A4.2 puts
// self-service ahead of the phone call to Super Admin, and self-service means a
// code reaching a mailbox.
//
// # Why a Mailer interface and not an SMTP client
//
// There is no mail provider configured for this product yet, and choosing one
// is a business decision — it costs money, it needs a domain with SPF and DKIM,
// and in Saudi Arabia it may need to be a provider with regional presence for
// E4.2's residency requirement. Writing an SMTP client now would be inventing
// that decision.
//
// So the seam is here, with two implementations that are both honest:
//
//   - `LogMailer` writes the message to the log. That is correct for
//     development and for a first deployment where the operator is watching:
//     the code is recoverable from `docker compose logs`, which is exactly how
//     somebody tests a recovery flow before wiring a provider.
//
//   - `RefusingMailer` fails the job. That is correct for production with no
//     provider configured: the job retries, escalates, and appears in the
//     failed-jobs view — which is the operator finding out that recovery does
//     not work BEFORE a shopkeeper does, rather than after.
//
// Neither pretends to have sent anything. A sender that silently succeeded
// would make a broken recovery flow indistinguishable from a working one, and
// the person who discovers the difference would be a shop owner locked out of
// their own business at nine on a Saturday morning.
package jobs

import (
	"github.com/jackc/pgx/v5"

	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Mailer delivers one message.
//
// Returning an error puts the job back on the queue with backoff, so an
// implementation should return one for anything transient — a provider
// timeout, a rate limit — and only succeed when the message has genuinely been
// handed over.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// LogMailer writes the message to the log instead of sending it.
//
// For development, and for a deployment whose operator is watching the logs
// while they set a provider up. It logs at WARN rather than INFO on purpose:
// this is not a normal state, and it should be visible in a log filtered to
// things that need attention.
type LogMailer struct{ Log *slog.Logger }

func (m LogMailer) Send(ctx context.Context, to, subject, body string) error {
	m.Log.Warn("no mail provider configured; message logged instead of sent",
		slog.String("to", to),
		slog.String("subject", subject),
		slog.String("body", body))
	return nil
}

// RefusingMailer fails every send.
//
// For production with no provider configured. The job retries, escalates and
// shows up as failed — which is the operator finding out that recovery is
// broken before a locked-out shopkeeper does.
type RefusingMailer struct{}

func (RefusingMailer) Send(context.Context, string, string, string) error {
	return errs.New(errs.CodeUnavailable,
		"No mail provider is configured, so this message cannot be sent. Set "+
			"one up, or recovery has to go through the platform operator.")
}

// NotifyHandler runs `notify.send`.
type NotifyHandler struct {
	Mailer Mailer
	Log    *slog.Logger
}

// Run delivers one queued notification.
func (h NotifyHandler) Run(ctx context.Context, j Job) error {
	var p identity.NotifyPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		// A payload that cannot be read will never become readable, so this is
		// permanent rather than transient. Returning a normal error would
		// retry it twenty-five times to no purpose.
		return Permanent{Err: fmt.Errorf("notification payload could not be read: %w", err)}
	}

	switch p.Kind {
	case identity.NotifyKindPasswordReset:
		subject, body := passwordResetMessage(p)
		return h.Mailer.Send(ctx, p.Email, subject, body)
	default:
		return Permanent{Err: fmt.Errorf("unknown notification kind %q", p.Kind)}
	}
}

// passwordResetMessage is the mail somebody gets when they ask for a code.
//
// Deliberately short, and deliberately says what to do if they did not ask.
// A recovery mail arriving unbidden is how somebody learns their address is
// being targeted, and a mail that does not explain that wastes the warning.
//
// Not translated, and that is a known gap rather than a decision: the catalogue
// is loaded in the browser, and the worker has no locale for a person it is
// mailing. Storing a preferred language on `app_user` is the fix and it is a
// schema change this does not need to make first.
func passwordResetMessage(p identity.NotifyPayload) (subject, body string) {
	subject = "Your RawSyst password reset code"
	body = fmt.Sprintf(
		"Hello %s,\n\n"+
			"Your password reset code is:\n\n    %s\n\n"+
			"It expires in %d minutes and can be used once.\n\n"+
			"If you did not ask for this, you can ignore this message — your "+
			"password has not changed. If it keeps happening, tell whoever "+
			"looks after your RawSyst account.\n",
		p.FullName, p.Code, p.ExpiresInMinutes)
	return subject, body
}

// QueueNotification enqueues a message inside the caller's transaction.
//
// This is what makes `identity.Enqueuer` satisfiable without `identity`
// importing `jobs`. The dependency runs the other way — jobs already knows
// about identity's payload shape — and a cycle here would tie authentication to
// background work, two things that should be able to change independently.
func (q *Queue) QueueNotification(
	ctx context.Context, tx pgx.Tx, payload identity.NotifyPayload,
) error {
	return EnqueueIn(ctx, tx, Spec{
		Kind:    "notify.send",
		Payload: payload,
		// Design 08 gives notify.send priority 40 and eight attempts. A mail
		// that has failed eight times over the backoff schedule is a mail whose
		// recipient has given up waiting; retrying it further sends a code that
		// has long expired.
		Priority:    40,
		MaxAttempts: 8,
	})
}
