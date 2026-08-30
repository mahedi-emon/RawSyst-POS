// Command api serves the RawSyst POS HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/api"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/assets"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/branding"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/devices"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/egs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/expenses"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/fiscal"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/orders"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/logging"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/secrets"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/promotions"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/settlement"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/shift"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/stockops"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sync"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/treasury"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/vat"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// version is set at build time via -ldflags.
var version = "dev"

// healthcheck asks the running server whether it is ready, and exits 0 or 1.
//
// The container image is `scratch`: no shell, no curl, no wget. That is
// deliberate — an image with no shell gives a command-injection bug nowhere to
// go, and there is nothing to patch when a CVE lands in one. The cost is that
// `docker healthcheck` has nothing to run, so the binary answers for itself.
//
// `/readyz` and not `/healthz`. The first says the database answers; the second
// says only that the process is alive, and a process that is alive with no
// database is exactly the one that must stop receiving traffic.
func healthcheck() error {
	addr := os.Getenv("RAWSYST_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	// A bare port needs a host to dial. Localhost, always: this runs inside the
	// same container as the server it is asking about.
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/readyz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready: %s", resp.Status)
	}
	return nil
}

func main() {
	// Before anything else, and before any configuration is read: a health
	// check must not need a JWT secret to tell you the server is up.
	for _, arg := range os.Args[1:] {
		if arg == "-healthcheck" || arg == "--healthcheck" {
			if err := healthcheck(); err != nil {
				fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "api: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logging.New(string(cfg.Env), cfg.ServiceName, version)

	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Migrations run as their own step (cmd/migrate) so several API replicas
	// starting at once cannot race each other. Refusing to serve on an
	// out-of-date schema is safer than serving against columns that do not
	// exist yet.
	if err := verifySchema(ctx, pool); err != nil {
		return err
	}

	tokens := identity.NewTokenService(cfg.Auth)
	authz := identity.NewAuthorizer(pool)
	authSvc := identity.NewService(pool, tokens)
	mw := identity.NewMiddleware(tokens, authz)

	// Outside development, an unverified legal value fails the request rather
	// than quietly computing tax from a placeholder.
	rules := registry.New(pool, cfg.Env.IsProduction())
	if err := reportRegistryHealth(ctx, rules, log, cfg.Env.IsProduction()); err != nil {
		return err
	}

	provSvc := provisioning.NewService(pool)

	// Signing never happens here in any environment — the key lives in the
	// terminal's OS keystore and never reaches this process. What this stack
	// holds is the credential ZATCA issues at onboarding, which authenticates
	// the reporting and clearance calls and cannot stamp anything.
	chain := zatca.NewChain(pool, zatca.HasherFor(cfg.Env.IsProduction()))
	// The keyring that protects the ZATCA credential at rest. Absent in
	// development, where there are no real credentials to protect; config
	// refuses to start without it in staging and production.
	var cipher *secrets.Cipher
	if len(cfg.Auth.DataEncryptionKeys) > 0 {
		var err error
		cipher, err = secrets.New(cfg.Auth.DataEncryptionKeys...)
		if err != nil {
			log.Error("the data encryption keyring is unusable", slog.Any("error", err))
			os.Exit(1)
		}
	}

	credentials := zatca.NewCredentialStore(pool, cipher)
	submitter := zatca.SubmitterFrom(credentials,
		zatca.Environment(cfg.ZATCAEnvironment))
	salesSvc := sales.NewService(chain).WithPool(pool).WithRegistry(rules).WithSubmitter(submitter)

	// The sync engine replays a terminal's offline queue through the SAME sale
	// path an online sale takes. Registering the applier here rather than
	// letting the engine construct one keeps the dependency pointing one way:
	// sales knows about sync, sync knows nothing about sales.
	syncEngine := sync.NewEngine(pool)
	syncEngine.Register("sales_invoice", sales.NewSaleApplier(salesSvc))

	deviceSvc := devices.NewService(pool)
	// Device-bound tokens become subject to the terminal's CURRENT status, so a
	// revoked till stops working immediately rather than when its token expires.
	mw = mw.WithDevices(deviceSvc)

	srv := api.NewServer(authSvc, mw, authz, provSvc, salesSvc, reports.NewService(pool), vat.NewService(pool, rules), catalog.NewService(pool, rules), syncEngine, purchasing.NewService(pool), receivables.NewService(pool), deviceSvc, egs.NewService(pool), branding.NewService(pool), shift.NewService(pool), settlement.NewService(pool), expenses.NewService(pool, rules), stockops.NewService(pool), fiscal.NewService(pool), treasury.NewService(pool), assets.NewService(pool), promotions.NewService(pool), orders.NewService(pool), audit.NewService(pool),
		func() error { return pool.Health(ctx) }, version).
		// Onboarding is only wired when this installation can hold the
		// credential ZATCA issues; without a key the routes say so rather than
		// silently missing.
		WithOnboarding(zatca.NewOnboarding(pool, credentials)).
		// Secure cookies everywhere but a developer's laptop. A browser
		// silently DROPS a Secure cookie sent over plain HTTP, which presents
		// as a sign-in that appears to succeed and then has no session.
		WithSecureCookies(cfg.Env != config.EnvDevelopment).
		// The queue, which is how a password-recovery code reaches a mailbox.
		// Sending inside the request would make the reset endpoint exactly as
		// available as the mail provider.
		WithQueue(jobs.NewQueue(pool))

	handler := srv.Handler(
		httpx.RequestID,
		httpx.Logger(log),
		httpx.Recover,
		httpx.SecurityHeaders,
		httpx.CORS(corsOrigins()),
		httpx.Timeout(cfg.HTTP.WriteTimeout-time.Second),
	)

	httpSrv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("http server listening",
			slog.String("addr", cfg.HTTP.Addr),
			slog.String("region", cfg.DataRegion))
		if err := httpSrv.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	// Drain in flight requests. A POS terminal syncing a day of offline sales
	// must not have the connection cut mid-batch: the batch would retry, which
	// is safe, but the delay is visible to a cashier waiting to close a shift.
	shutdownCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), cfg.HTTP.ShutdownTimeout)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("shutdown complete")
	return nil
}

// verifySchema refuses to start against a database that has not been migrated.
func verifySchema(ctx context.Context, pool *db.Pool) error {
	migrations, err := db.LoadMigrations()
	if err != nil {
		return err
	}
	want := 0
	for _, m := range migrations {
		if m.Version > want {
			want = m.Version
		}
	}

	var have int
	err = pool.Raw().QueryRow(ctx,
		`SELECT coalesce(max(version), 0) FROM schema_migration`).Scan(&have)
	if err != nil {
		return fmt.Errorf("read schema version (has cmd/migrate been run?): %w", err)
	}
	if have < want {
		return fmt.Errorf(
			"database is at schema version %d but this build needs %d; run cmd/migrate first",
			have, want)
	}
	return nil
}

// reportRegistryHealth surfaces regulatory verification state at startup.
//
// In production an unverified release-blocker refuses to start. Blueprint E8.4
// names three â€” the ZATCA schema version, the GOSI dated rate schedule and the
// Mudad wage-file format â€” and shipping with any of them still a placeholder
// means computing a legal figure from a guess. Failing to boot is a far cheaper
// failure than a wrong tax return.
func reportRegistryHealth(
	ctx context.Context, rules *registry.Service, log *slog.Logger, strict bool,
) error {
	rep, err := rules.Health(ctx)
	if err != nil {
		return fmt.Errorf("check regulatory registry: %w", err)
	}

	log.Info("regulatory registry",
		slog.Int("verified", rep.Verified),
		slog.Int("never_verified", rep.NeverVerified),
		slog.Int("stale_tax_payroll", rep.StaleTaxPayroll),
		slog.Int("stale_other", rep.StaleOther),
		slog.Int("blocking_release", len(rep.BlockingRelease)))

	if len(rep.BlockingRelease) > 0 {
		if strict {
			return fmt.Errorf(
				"refusing to start: these legal values have never been verified "+
					"against their official source: %s. "+
					"Verify them in Super Admin > Regulatory Registry first",
				strings.Join(rep.BlockingRelease, ", "))
		}
		log.Warn("unverified release-blocking regulatory rules",
			slog.String("rules", strings.Join(rep.BlockingRelease, ", ")),
			slog.String("note", "this would refuse to start in production"))
	}
	return nil
}

// tauriOrigins are the origins the POS presents from inside its own window.
//
// They are constants of the framework rather than choices a deployment makes:
// Tauri serves the app's embedded assets from a custom protocol whose origin is
// fixed, and it differs by platform — `http://tauri.localhost` on Windows,
// `tauri://localhost` on macOS and Linux.
//
// Always allowed, and NOT left to configuration, because leaving it to
// configuration is how a shop ends up with a till that cannot sign in. Found by
// driving the packaged application under tauri-driver: the preflight for
// `http://tauri.localhost` came back 204 with no allow-origin header, so every
// call the till made was blocked by the browser before it reached this API, and
// the screen said only "Sign-in did not complete." Neither the deployed
// configuration nor `.env.example` listed the origin, so a deployment following
// the documentation would have shipped exactly that.
//
// Safe to hard-code in a way a wildcard would not be. These are not addresses a
// website can be served from: no DNS name resolves to them and no browser will
// hand them to a page it loaded over the network. They name this product's own
// desktop shell and nothing else.
var tauriOrigins = []string{
	"http://tauri.localhost",
	"https://tauri.localhost",
	"tauri://localhost",
}

// corsOrigins reads the browser origins allowed to call this API.
//
// An explicit allowlist with no wildcard fallback. The API is credentialed, so
// a wildcard would let any site act as a signed-in Owner.
//
// The configured list is for BROWSERS — the back office, wherever it is hosted.
// The desktop till's origin is added unconditionally: see tauriOrigins.
func corsOrigins() []string {
	out := append([]string(nil), tauriOrigins...)

	raw := os.Getenv("RAWSYST_CORS_ORIGINS")
	if raw == "" {
		return out
	}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
