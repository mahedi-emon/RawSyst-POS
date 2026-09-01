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

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/integration"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/logging"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/secrets"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
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
	worker.Register(jobs.KindZATCASubmit, jobs.NewZATCASubmitter(pool, submitter))
	worker.Register(jobs.KindZATCAStaleness, jobs.NewStalenessSweeper(pool))

	// QA gate M1 as a running property rather than a build-time one. Until this
	// was registered, the three tie-out invariants were proved on every build
	// and watched on no live tenant — a shop whose books drifted after go-live
	// would have heard it from its accountant months later.
	worker.Register(jobs.KindAccountingTieOut, jobs.NewTieOutSweeper(pool))
	// D1's scheduled reports. The sweep finds the schedules whose turn it is
	// and queues the sends; the figures are computed when each is rendered.
	worker.Register(jobs.KindReportSweep,
		jobs.NewReportSweeper(pool, reports.NewService(pool)))
	// Outbound webhooks (H6). Sent from here rather than from the API, because
	// a delivery waits on somebody else's server and the connection an API
	// handler would hold while waiting is one the tills need.
	worker.Register(jobs.KindWebhookDispatch,
		jobs.NewWebhookDispatcher(integration.NewService(pool, cipher)))

	// Sending a message, which for now means password-recovery codes.
	//
	// There is no mail provider configured for this product, and choosing one
	// is a business decision — it costs money, needs a domain with SPF and
	// DKIM, and under E4.2's residency requirement may need regional presence.
	// So the seam is a Mailer with two honest implementations and no third that
	// pretends.
	//
	// In development the message is logged, which is how somebody tests the
	// recovery flow before wiring a provider: the code is in
	// `docker compose logs`. Anywhere else the job FAILS, retries, escalates
	// and shows up in the failed-jobs view — which is an operator discovering
	// that recovery does not work before a locked-out shopkeeper does.
	var mailer jobs.Mailer = jobs.RefusingMailer{}
	if cfg.Env == config.EnvDevelopment {
		mailer = jobs.LogMailer{Log: log}
	}
	// And the SMS side, on exactly the same terms: a portal sign-in code goes
	// to a phone, and a deployment with no provider must FAIL those jobs
	// rather than discard them — an operator finding out that customers
	// cannot sign in before a customer does.
	var texter jobs.Texter = jobs.RefusingTexter{}
	if cfg.Env == config.EnvDevelopment {
		texter = jobs.LogTexter{Log: log}
	}

	// The reports service is handed to the notifier as well as the sweep: a
	// scheduled report is COMPUTED when it is sent, which is what keeps a
	// schedule from being a snapshot of the day it was set up.
	worker.Register("notify.send", jobs.NotifyHandler{
		Mailer: mailer, Texter: texter, Log: log,
		Reports: reports.NewService(pool),
	})

	// Registering the submitter even when it cannot submit is deliberate:
	// invoices queue up, the staleness sweep escalates, and an Owner sees a
	// growing backlog with a truthful reason — rather than the system looking
	// healthy because nothing was ever attempted.
	if !submitter.Available() {
		log.Warn("ZATCA submission is not available",
			slog.String("reason",
				"this installation cannot hold the credential ZATCA issues; "+
					"set RAWSYST_DATA_ENCRYPTION_KEYS"),
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
