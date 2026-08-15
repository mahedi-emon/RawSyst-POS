// Command worker runs RawSyst POS background jobs.
//
// A separate binary from the API, deliberately. The API answers a cashier
// waiting at a till and must stay responsive; the worker does slow, retrying
// work that nobody is watching. Sharing a process would let a stuck ZATCA
// submission consume request capacity, and scaling one would mean scaling both.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/logging"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
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
	log := logging.New(string(cfg.Env), cfg.ServiceName+"-worker", version)

	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	name, _ := os.Hostname()
	if name == "" {
		name = "worker"
	}
	name = fmt.Sprintf("%s/%d", name, os.Getpid())

	queue := jobs.NewQueue(pool)
	worker := jobs.NewWorker(queue, log, name)

	// The ZATCA client refuses in every environment until the document format
	// is verified. Registering it anyway is deliberate: invoices queue up, the
	// staleness sweep escalates, and an Owner sees a growing backlog with a
	// truthful reason — rather than the system looking healthy because nothing
	// was ever attempted.
	submitter := zatca.SubmitterFor(cfg.Env.IsProduction())
	worker.Register(jobs.KindZATCASubmit, jobs.NewZATCASubmitter(pool, submitter))
	worker.Register(jobs.KindZATCAStaleness, jobs.NewStalenessSweeper(pool))

	if !submitter.Available() {
		log.Warn("ZATCA submission is not available",
			slog.String("reason",
				"the document format has not been verified against ZATCA's "+
					"published standard"),
			slog.String("effect",
				"invoices will queue and escalate; none will be reported"))
	}

	scheduler := jobs.NewScheduler(queue, log)
	go scheduler.Run(ctx)

	err = worker.Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	// A short grace period so a job claimed just before the signal can finish
	// rather than being reaped and run twice.
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	<-shutdown.Done()

	log.Info("worker shutdown complete")
	return nil
}
