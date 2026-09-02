// Package observe reports errors to Sentry.
//
// # What is sent, and what is not
//
// A crash and a 5xx. Nothing else. A 400 is the client being wrong, a 403 is
// the system working, a 404 is a record that is not there — reporting those
// fills an error tracker with normal Tuesdays and teaches everybody to ignore
// it.
//
// # What is scrubbed
//
// This product holds a shop's customers, its staff's salaries and its tax
// filings. An error tracker is a third party, usually in another jurisdiction,
// and PDPL does not stop applying because the data arrived in a stack trace.
//
// So the report carries the request id, the route PATTERN, the method, the
// status and the tenant id — enough to find the request in the logs, which
// stay on the shop's own infrastructure — and nothing else. No query string,
// no headers, no body, no cookies, no user email, no IP address. The tenant id
// is a uuid and identifies a business rather than a person.
//
// The scrubbing is done here rather than left to Sentry's own settings,
// because a setting somebody has to remember to configure is a setting that is
// wrong on the day it matters.
//
// # Optional
//
// No DSN means no reporting and no error at start-up. Most shops run one
// server and read the logs.
package observe

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
)

// Reporter sends errors somewhere they will be looked at.
//
// A nil *Reporter is valid and does nothing, so a caller never has to check.
type Reporter struct {
	enabled bool
	log     *slog.Logger
}

// Start initialises reporting. It never fails the process: an error tracker
// that cannot be reached must not stop a shop trading.
func Start(cfg config.Observability, env, release string, log *slog.Logger) *Reporter {
	if cfg.SentryDSN == "" {
		return &Reporter{enabled: false, log: log}
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.SentryDSN,
		Environment:      env,
		Release:          release,
		TracesSampleRate: cfg.SentrySampleRate,
		// Off. Sentry's own request integration attaches headers, cookies and
		// the query string, which is exactly the material this package exists
		// to keep out of a third party's index.
		SendDefaultPII: false,
		// The last gate. Anything that reaches here has already been shaped by
		// Capture below; this catches whatever the SDK adds on its own.
		BeforeSend: scrub,
	})
	if err != nil {
		log.Warn("error reporting could not be started; carrying on without it",
			slog.Any("error", err))
		return &Reporter{enabled: false, log: log}
	}
	log.Info("error reporting is on", slog.String("environment", env))
	return &Reporter{enabled: true, log: log}
}

// scrub strips everything that could identify a person.
func scrub(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event.Request != nil {
		event.Request.Cookies = ""
		event.Request.Headers = nil
		event.Request.QueryString = ""
		event.Request.Data = ""
		event.Request.Env = nil
	}
	// A user object here would be a staff member's id and email in somebody
	// else's database. The tenant tag is what an investigation actually needs.
	event.User = sentry.User{}
	return event
}

// Capture reports a server-side failure.
//
// `pattern` is the route pattern rather than the URL, for the same reason the
// metrics use it: an id in a path is a record, and a record is not something
// to hand to a third party.
func (r *Reporter) Capture(
	err error, requestID, method, pattern string,
	status int, tenantID uuid.UUID,
) {
	if r == nil || !r.enabled || err == nil {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		scope.SetTag("request_id", requestID)
		scope.SetTag("method", method)
		scope.SetTag("route", pattern)
		scope.SetTag("status", http.StatusText(status))
		if tenantID != uuid.Nil {
			// A business, not a person.
			scope.SetTag("tenant", tenantID.String())
		}
		sentry.CaptureException(err)
	})
}

// CaptureMessage reports something that is wrong without being an error value
// — a background job that gave up, a submission that will not be retried.
func (r *Reporter) CaptureMessage(message string, tags map[string]string) {
	if r == nil || !r.enabled {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelWarning)
		for k, v := range tags {
			scope.SetTag(k, v)
		}
		sentry.CaptureMessage(message)
	})
}

// Recover reports a panic and re-panics, so the existing recovery middleware
// still turns it into a 500.
//
// Deliberately not a replacement for that middleware: the response a client
// gets is this product's decision and must not depend on whether an error
// tracker is configured.
func (r *Reporter) Recover() {
	if r == nil || !r.enabled {
		return
	}
	if p := recover(); p != nil {
		sentry.CurrentHub().Recover(p)
		panic(p)
	}
}

// Flush waits for queued reports on the way out.
//
// Bounded, and short. A shutdown that hangs because an error tracker is slow
// is a deployment that looks stuck, and the reports are worth less than the
// restart.
func (r *Reporter) Flush(context.Context) {
	if r == nil || !r.enabled {
		return
	}
	sentry.Flush(2 * time.Second)
}

// Enabled says whether anything is being reported, for the start-up log and
// the readiness readout.
func (r *Reporter) Enabled() bool { return r != nil && r.enabled }
