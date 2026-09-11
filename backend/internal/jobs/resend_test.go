// Whether a failed message is tried again.
//
// That is the only decision this mailer makes, and both ways of getting it
// wrong are expensive: a permanent failure retried for hours hides a
// configuration error behind a queue that looks busy, and a transient one
// marked permanent throws away a password reset somebody is standing there
// waiting for.
//
// No network. A test server stands in for Resend, which is what lets the 429
// and the 500 be exercised at all — they are the cases that matter and the ones
// nobody can produce on demand against the real thing.

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resendStub answers with a chosen status and records what it was sent.
func resendStub(t *testing.T, status int, body string) (*httptest.Server, *resendRequest, *string) {
	t.Helper()
	var got resendRequest
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	t.Cleanup(srv.Close)
	return srv, &got, &auth
}

// sendVia points the mailer at the stub. The endpoint is a constant in the
// product on purpose — a deployment aiming it elsewhere is not using Resend —
// so the test supplies a client whose transport rewrites the destination.
func sendVia(srv *httptest.Server, m ResendMailer, to, subject, body string) error {
	m.Client = srv.Client()
	m.Client.Transport = rewriteHost{to: srv.URL, next: srv.Client().Transport}
	return m.Send(context.Background(), to, subject, body)
}

type rewriteHost struct {
	to   string
	next http.RoundTripper
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL
	stub, err := req.URL.Parse(r.to)
	if err != nil {
		return nil, err
	}
	u.Scheme, u.Host = stub.Scheme, stub.Host
	return r.next.RoundTrip(req)
}

func TestAnAcceptedMessageIsDone(t *testing.T) {
	srv, got, auth := resendStub(t, http.StatusOK, `{"id":"abc"}`)
	err := sendVia(srv, ResendMailer{APIKey: "re_secret", From: "shop@example.com"},
		"somebody@example.test", "Your code", "It is 123456.")
	if err != nil {
		t.Fatalf("a 200 was treated as a failure: %v", err)
	}

	if got.From != "shop@example.com" {
		t.Errorf("from = %q", got.From)
	}
	if len(got.To) != 1 || got.To[0] != "somebody@example.test" {
		t.Errorf("to = %v", got.To)
	}
	if got.Subject != "Your code" || got.Text != "It is 123456." {
		t.Errorf("subject/text = %q / %q", got.Subject, got.Text)
	}
	if *auth != "Bearer re_secret" {
		t.Errorf("the key was not presented as a bearer token: %q", *auth)
	}
}

// A bad minute at the provider. The message is fine and should be tried again.
func TestAProviderHavingABadMinuteIsRetried(t *testing.T) {
	for _, status := range []int{
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	} {
		srv, _, _ := resendStub(t, status, `{"message":"later"}`)
		err := sendVia(srv,
			ResendMailer{APIKey: "re_secret", From: "shop@example.com"},
			"somebody@example.test", "s", "b")
		if err == nil {
			t.Errorf("%d was treated as success", status)
			continue
		}
		var permanent Permanent
		if errors.As(err, &permanent) {
			t.Errorf("%d was marked permanent, so a message that would have "+
				"gone through on the next attempt is thrown away", status)
		}
	}
}

// This message is wrong, and will be wrong for ever.
//
// An unverified sender domain is the one somebody will actually hit, and
// retrying it twenty-five times over a backoff schedule hides the fact that a
// DNS record is missing.
func TestAMessageTheProviderRefusesIsNotRetried(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusUnprocessableEntity,
	} {
		srv, _, _ := resendStub(t, status,
			`{"message":"The from address domain is not verified"}`)
		err := sendVia(srv,
			ResendMailer{APIKey: "re_secret", From: "shop@unverified.test"},
			"somebody@example.test", "s", "b")

		var permanent Permanent
		if !errors.As(err, &permanent) {
			t.Errorf("%d was retried; a domain nobody has verified does not "+
				"become verified by waiting", status)
		}
		// And the refusal says what the provider said, so the DNS record that
		// is missing is named rather than guessed at.
		if err != nil && !strings.Contains(err.Error(), "not verified") {
			t.Errorf("%d: the reason was lost: %v", status, err)
		}
	}
}

// Half-configured is refused with a sentence naming what is missing.
func TestAHalfConfiguredMailerSaysWhichHalf(t *testing.T) {
	for _, c := range []struct {
		name   string
		mailer ResendMailer
		expect string
	}{
		{"no key", ResendMailer{From: "shop@example.com"}, "API key"},
		{"no sender", ResendMailer{APIKey: "re_secret"}, "RAWSYST_MAIL_FROM"},
	} {
		err := c.mailer.Send(context.Background(), "a@b.test", "s", "b")
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		var permanent Permanent
		if !errors.As(err, &permanent) {
			t.Errorf("%s: retried; configuration does not fix itself", c.name)
		}
		if !strings.Contains(err.Error(), c.expect) {
			t.Errorf("%s: does not name what is missing: %v", c.name, err)
		}
	}
}

// The key never appears in an error, which is where errors end up: a log, a
// job row, an error reporter.
func TestTheKeyIsNeverInAnError(t *testing.T) {
	const key = "re_a_very_secret_value"
	srv, _, _ := resendStub(t, http.StatusBadRequest, `{"message":"nope"}`)
	err := sendVia(srv, ResendMailer{APIKey: key, From: "shop@example.com"},
		"somebody@example.test", "s", "b")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("the API key is in the error text: %v", err)
	}
}

// A provider that answers with a great deal of HTML does not put all of it in
// a job's error column.
func TestAVeryLongRefusalIsTrimmed(t *testing.T) {
	srv, _, _ := resendStub(t, http.StatusBadRequest, strings.Repeat("x", 64_000))
	err := sendVia(srv, ResendMailer{APIKey: "re_secret", From: "shop@example.com"},
		"somebody@example.test", "s", "b")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if len(err.Error()) > 4096 {
		t.Errorf("the error is %d bytes; a bad day at the provider should not "+
			"fill the jobs table", len(err.Error()))
	}
}
