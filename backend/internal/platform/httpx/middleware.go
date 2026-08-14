package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/logging"
)

// RequestID attaches a correlation id to every request.
//
// A client-supplied id is honoured so a POS terminal can correlate its own
// retry with the server's record of it, but it is validated first: an
// unvalidated header ends up in log files and error payloads, where a crafted
// value could forge log lines or inject markup into a support tool.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !isSafeRequestID(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
	})
}

func isSafeRequestID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// Logger attaches a request-scoped logger and records the outcome.
func Logger(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			log := base.With(
				slog.String("request_id", logging.RequestID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)
			ctx := logging.Into(r.Context(), log)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			// The query string is deliberately absent: filters routinely carry a
			// customer phone number or national id, and PDPL treats a log as
			// processing. The path alone identifies the endpoint.
			log.Info("request",
				slog.Int("status", rec.status),
				slog.Duration("took", time.Since(start)),
				slog.Int("bytes", rec.written))
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.written += n
	return n, err
}

// Recover turns a panic into a 500 rather than a dropped connection.
//
// A panic in one handler must not take down the process: on a POS backend that
// would end every cashier's session across every tenant. The stack is logged,
// never returned.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// A client that disconnects mid-write produces this; it is not a
				// fault and should not page anyone.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				logging.From(r.Context()).Error("panic recovered",
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())))
				Error(w, r, errs.New(errs.CodeInternal,
					"Something went wrong on our side. Please try again."))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders sets conservative defaults.
//
// The API serves JSON only, so the CSP forbids everything. If a response is
// ever mis-served as HTML, there is nothing for a browser to execute.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

// Timeout bounds how long a handler may run.
//
// Reports are explicitly excluded from this: blueprint A2 #8 requires heavy
// reports to run as background jobs, so any request reaching this timeout is a
// bug rather than a slow-but-valid operation.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CORS allows the configured browser origins.
//
// An explicit allowlist, never a reflected origin or a wildcard. The API is
// credentialed, and a wildcard with credentials would let any site on the
// internet act as a signed-in Owner.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[strings.ToLower(strings.TrimSpace(o))] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowed[strings.ToLower(origin)]; ok {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", origin)
					h.Set("Access-Control-Allow-Credentials", "true")
					h.Set("Access-Control-Allow-Headers",
						"Authorization, Content-Type, X-Request-Id, Idempotency-Key, If-Match")
					h.Set("Access-Control-Allow-Methods",
						"GET, POST, PATCH, PUT, DELETE, OPTIONS")
					h.Set("Access-Control-Expose-Headers",
						"X-Request-Id, ETag, Idempotency-Replayed, Retry-After")
					h.Set("Access-Control-Max-Age", "600")
					h.Add("Vary", "Origin")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
