// Package logging provides structured logging with request correlation.
//
// Two rules govern what may be logged:
//
//  1. Never log a secret — passwords, tokens, JWT contents, signing keys.
//  2. Never log personal data casually. Saudi PDPL applies extraterritorially
//     and treats logs as processing. Customer names, phone numbers, national
//     IDs and Iqama numbers must be referenced by identifier, not value.
//
// Both rules are enforced by review and by the Redact helper below, which
// should be used for any field whose value might carry personal data.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey int

const (
	ctxKeyLogger ctxKey = iota
	ctxKeyRequestID
)

// New builds the root logger. Production emits JSON for ingestion; development
// emits text for human reading.
func New(env, service, version string) *slog.Logger {
	level := slog.LevelInfo
	if strings.EqualFold(env, "development") {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// Defence in depth: even if a secret reaches the logger by mistake,
			// a conventionally-named field is masked rather than written out.
			switch strings.ToLower(a.Key) {
			case "password", "secret", "token", "authorization",
				"jwt", "refresh_token", "private_key", "csid", "api_key":
				return slog.String(a.Key, "[redacted]")
			}
			return a
		},
	}

	var h slog.Handler
	if strings.EqualFold(env, "development") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(h).With(
		slog.String("service", service),
		slog.String("version", version),
		slog.String("env", env),
	)
}

// Into stores a logger on the context.
func Into(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKeyLogger, l)
}

// From retrieves the request logger, falling back to the default.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKeyLogger).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithRequestID stores a request correlation id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestID returns the request correlation id, or "".
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return id
	}
	return ""
}

// Redact masks a value that may contain personal data, keeping just enough to
// correlate log lines without storing the data itself.
//
//	Redact("+966501234567") => "+9665…567"
func Redact(v string) string {
	const keep = 3
	if v == "" {
		return ""
	}
	r := []rune(v)
	if len(r) <= keep*2 {
		return "…"
	}
	return string(r[:keep]) + "…" + string(r[len(r)-keep:])
}
