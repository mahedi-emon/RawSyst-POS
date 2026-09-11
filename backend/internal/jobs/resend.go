// Sending mail through Resend.
//
// # Why a provider at last, and why this one
//
// `notify.go` has carried a Mailer seam with two honest implementations since
// the recovery flow was built, and its comment said choosing a provider was a
// business decision: it costs money, it needs a domain with SPF and DKIM, and
// under E4.2's residency rules it may need regional presence. The decision has
// been made, and this is it.
//
// Nothing else changes. The seam is the same, the two existing implementations
// stay for development and for a deployment with no key, and the queue, the
// retry schedule and the failed-jobs view all behave exactly as before.
//
// # What a failure means here
//
// A mailer's error decides whether the message is tried again, so the two kinds
// are separated deliberately:
//
//   - A 5xx, a 429 or a dropped connection is the provider having a bad
//     minute. That returns an ordinary error, the job goes back on the queue
//     with backoff, and it will very likely succeed.
//   - A 4xx is this message being wrong — an address that is not an address, a
//     sender domain that is not verified, a key that is not a key. Retrying
//     twenty-five times changes nothing, so that returns `Permanent` and the
//     job stops and becomes visible.
//
// Getting that the wrong way round is expensive in both directions: a permanent
// failure retried for hours hides a configuration error, and a transient one
// marked permanent throws away a password reset somebody is waiting for.
//
// # The key is never logged
//
// Not in an error, not in a debug line, not on the way to Sentry. The only
// thing this file ever says about the key is whether one is configured.

package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// resendEndpoint is Resend's send-one-message API.
//
// A constant rather than configuration: a deployment that needs to point this
// somewhere else is not using Resend, and should have its own Mailer rather
// than this one aimed at a different host.
const resendEndpoint = "https://api.resend.com/emails"

// ResendMailer sends through Resend.
type ResendMailer struct {
	// APIKey authenticates to Resend. Never logged.
	APIKey string

	// From is the sender, and it must be an address on a domain verified in
	// the Resend dashboard. Resend refuses anything else with a 4xx, which
	// this correctly treats as permanent: an unverified domain does not become
	// verified by waiting.
	From string

	// Client is the HTTP client. Optional: a caller that does not set one gets
	// a client with a timeout, because the default `http.Client` has none and
	// a hung connection would hold a worker slot until the process restarted.
	Client *http.Client
}

// resendRequest is the wire shape, with only the fields this product sends.
//
// Text, not HTML. Every message this product sends is a short piece of prose
// written for somebody to read, and a plain-text mail arrives readable in every
// client, is never quarantined for having a tracking pixel, and cannot leak the
// layout bugs an HTML template would.
type resendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

// Send hands one message to Resend.
func (m ResendMailer) Send(ctx context.Context, to, subject, body string) error {
	if strings.TrimSpace(m.APIKey) == "" {
		// Not retryable: a key does not appear by waiting. This is the same
		// refusal RefusingMailer gives, kept here so a half-configured
		// deployment fails with a sentence naming what is missing.
		return Permanent{Err: fmt.Errorf(
			"no Resend API key is configured, so nothing can be sent")}
	}
	if strings.TrimSpace(m.From) == "" {
		return Permanent{Err: fmt.Errorf(
			"no sender address is configured, so Resend will refuse every " +
				"message; set RAWSYST_MAIL_FROM to an address on a domain " +
				"verified in Resend")}
	}

	payload, err := json.Marshal(resendRequest{
		From: m.From, To: []string{to}, Subject: subject, Text: body,
	})
	if err != nil {
		return Permanent{Err: err}
	}

	// A deadline of its own, so a provider that accepts the connection and then
	// says nothing cannot hold a worker slot indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, resendEndpoint, bytes.NewReader(payload))
	if err != nil {
		return Permanent{Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		// The network, not the message. Worth trying again.
		return fmt.Errorf("sending through Resend: %w", err)
	}
	defer resp.Body.Close()

	// Bounded: a provider having a very bad day should not put a megabyte of
	// HTML into a job's error column.
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil

	case resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= 500:
		// Their problem, and a temporary one. Back on the queue.
		return fmt.Errorf("Resend answered %d: %s",
			resp.StatusCode, strings.TrimSpace(string(detail)))

	default:
		// Ours. An address that is not an address, a sender domain that is not
		// verified, a key that is not a key. Retrying changes none of it, and
		// a job that stops is a job somebody sees.
		return Permanent{Err: fmt.Errorf("Resend refused the message (%d): %s",
			resp.StatusCode, strings.TrimSpace(string(detail)))}
	}
}
