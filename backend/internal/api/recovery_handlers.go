// Self-service password recovery, over HTTP.
//
// Two public routes, which is unusual enough here to be worth stating: nearly
// everything in this product requires a token, and these cannot — the whole
// point is that the caller has lost the way to get one.
//
// # What makes two unauthenticated endpoints safe
//
// Neither says whether an account exists. `forgot-password` returns 204 for a
// real address and an invented one; `reset-password` returns the same refusal
// for a wrong code, an expired code, a spent code and an unknown address. An
// endpoint that distinguishes them is a tool for confirming which of a leaked
// address list are customers of this product, and those shops are then worth
// phishing.
//
// Guessing is bounded three ways, and the layers do different jobs: five wrong
// guesses kill one code, three requests an hour cap one account, and the
// per-caller limiter below caps one source across all accounts. The third is
// what stops somebody walking an address list; the first two are per-account
// and would let them.
package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/cache"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// recoveryLimiter caps how often one caller may use the public recovery routes.
//
// # Counted in the cache, which is what makes it hold across replicas
//
// It used to be a map in this process, and that was defensible while there
// was one process: losing the counters on restart costs one more window of
// attempts against codes that expire in ten minutes, and a database write on
// every request to a PUBLIC endpoint is its own denial of service.
//
// It stopped being defensible the moment a deployment could have two. A limit
// of ten per replica is a limit of twenty behind two, thirty behind three, and
// it rises every time somebody scales up to cope with the traffic the attack
// is generating. So the counter lives in `cache.Cache`: Redis where one is
// configured, and an in-memory store with the same behaviour where none is —
// which is correct, because a deployment without Redis has one process.
//
// The account-level ceiling in `identity.RequestReset` still holds regardless,
// and it is the one that stops a mailbox being flooded. This one is what stops
// somebody walking an address list.
type recoveryLimiter struct {
	cache  cache.Cache
	limit  int64
	window time.Duration
}

func newRecoveryLimiter(
	c cache.Cache, limit int64, window time.Duration,
) *recoveryLimiter {
	if c == nil {
		c = cache.NewMemory()
	}
	return &recoveryLimiter{cache: c, limit: limit, window: window}
}

func (l *recoveryLimiter) allow(ctx context.Context, key string) error {
	n, err := l.cache.Incr(ctx, "recovery:"+key, l.window)
	if err != nil {
		// The cache is unreachable. Allowed rather than refused: this is a
		// nuisance filter, and a Redis outage must not lock every shop out of
		// its own password recovery. The account-level ceiling still applies.
		return nil
	}
	if n > l.limit {
		return errs.New(errs.CodeRateLimited,
			"Too many attempts from here. Wait a few minutes and try again.")
	}
	return nil
}

// callerIP is the address a request came from.
//
// `RemoteAddr` and not `X-Forwarded-For`. A header a client sets is a limiter a
// client can defeat by changing it on every request, which is worse than no
// limiter because it looks like one. A deployment behind a proxy must have the
// proxy set `RemoteAddr` — which every reverse proxy in ordinary use does — and
// trusting a header would require knowing which hop to trust, which is
// configuration this does not have.
func callerIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.TrimSpace(host))
}

// --- POST /api/v1/auth/forgot-password ---------------------------------

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	if err := s.recoveryLimit.allow(r.Context(), r.RemoteAddr); err != nil {
		httpx.Error(w, r, err)
		return
	}

	// No queue, no send. Reported plainly rather than pretended past: a
	// recovery flow that accepts the request and silently drops it is
	// indistinguishable from one that works, and the person who finds the
	// difference is locked out of their own shop.
	if s.queue == nil {
		httpx.Error(w, r, errs.New(errs.CodeUnavailable,
			"Password recovery by email is not set up on this installation. "+
				"Ask your platform operator to reset it for you."))
		return
	}

	// The error is deliberately logged rather than returned.
	//
	// Every outcome that depends on whether the address exists — no such
	// account, a suspended one, an hourly ceiling already reached — is
	// swallowed inside RequestReset and reaches here as nil. What could reach
	// here as an error is a database fault, and answering 500 to that would
	// still be an answer that varies with the input in a way an attacker could
	// provoke. So it is logged and the caller is told the same thing either way.
	if err := s.auth.RequestReset(
		r.Context(), req.Email, callerIP(r), s.queue,
	); err != nil {
		slog.ErrorContext(r.Context(), "password reset request failed",
			slog.String("error", err.Error()))
	}

	// 204 always. See the file comment.
	w.WriteHeader(http.StatusNoContent)
}

// --- POST /api/v1/auth/reset-password ----------------------------------

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	if err := s.recoveryLimit.allow(r.Context(), r.RemoteAddr); err != nil {
		httpx.Error(w, r, err)
		return
	}

	if err := s.auth.CompleteReset(
		r.Context(), req.Email, req.Code, req.NewPassword,
	); err != nil {
		httpx.Error(w, r, err)
		return
	}

	// No session is issued. Somebody who has just proved control of a mailbox
	// has proved exactly that, and signing them straight in would turn a
	// ten-minute code into a session — which is a longer-lived credential than
	// the one they used to get it. They sign in, with the password they chose.
	w.WriteHeader(http.StatusNoContent)
}
