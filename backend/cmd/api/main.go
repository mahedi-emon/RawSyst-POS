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
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/catalog"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/devices"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/egs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/logging"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/provisioning"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/purchasing"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/receivables"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/registry"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sales"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/shift"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sync"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/vat"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
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

	// The hasher is the one part of ZATCA that is not yet implemented: the
	// byte-level UBL 2.1 XML and the QR TLV encoding are still unverified
	// against primary sources, so the seam is left explicit rather than filled
	// in by guesswork. In production it refuses; elsewhere it produces a
	// clearly-labelled placeholder so the rest of the system can be exercised.
	//
	// Signing never happens here in any environment — the key lives in the
	// terminal's OS keystore and never reaches this process.
	chain := zatca.NewChain(pool, zatca.HasherFor(cfg.Env.IsProduction()))
	submitter := zatca.SubmitterFor(cfg.Env.IsProduction())
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

	srv := api.NewServer(authSvc, mw, authz, provSvc, salesSvc, reports.NewService(pool), vat.NewService(pool, rules), catalog.NewService(pool, rules), syncEngine, purchasing.NewService(pool), receivables.NewService(pool), deviceSvc, egs.NewService(pool), shift.NewService(pool),
		func() error { return pool.Health(ctx) }, version)

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

// corsOrigins reads the browser origins allowed to call this API.
//
// An explicit allowlist with no wildcard fallback. The API is credentialed, so
// a wildcard would let any site act as a signed-in Owner.
func corsOrigins() []string {
	raw := os.Getenv("RAWSYST_CORS_ORIGINS")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
