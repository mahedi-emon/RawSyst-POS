// The process that does the work somebody asked for on a screen.
//
// # Where it runs
//
// In the postgres image, beside the database, because `pg_dump` and
// `pg_restore` live there and this product is not going to reimplement them.
// The API image is `scratch` and has neither, which is correct: an API server
// carrying the PostgreSQL client tools to run one command a day would be paying
// for them on every deploy and every pull.
//
// # What it does
//
// Claims one task at a time from `backup_task`, runs it, writes down what
// happened, sleeps. That is all. It has no HTTP surface, no schedule of its own
// and nothing to configure beyond the environment its commands already need —
// the nightly backup is still a systemd timer, because a timer that runs for
// ninety seconds a day beats a resident scheduler on a server with two cores.
//
// # Why it writes a stage
//
// Somebody is watching. A backup takes minutes and the difference between
// "Dumping" and "Uploading" is the difference between a screen that is
// informative and a spinner. None of the stages are percentages: `pg_dump` does
// not report progress and inventing one would be a lie with a progress bar
// around it.
package backup

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/maintenance"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Agent runs queued backup work.
type Agent struct {
	Tasks    *Tasks
	Register *Register
	Options  Options

	// AdminDSN is a connection to a database that is NOT the live one, for
	// creating and dropping the temporary databases a verification restores
	// into.
	AdminDSN string

	// Production is what a production restore needs, including the switch that
	// says whether it is allowed at all.
	Production ProductionOptions

	// StagingDir is where an uploaded artifact waits. Shared with the API,
	// which is what puts files there.
	StagingDir string

	// Maintenance is the write freeze, held on for the cutover of a production
	// restore. Without it, a sale rung up during the two renames would land in
	// the database being renamed out of the way and be lost — which is the
	// exact failure this whole subsystem exists to prevent, arriving during
	// the recovery.
	Maintenance *maintenance.Service

	Log  *slog.Logger
	Name string

	// Poll is how often an idle agent asks for work. Five seconds: long enough
	// that an idle server does nothing measurable, short enough that pressing
	// CREATE BACKUP feels like it did something.
	Poll time.Duration

	// Abandoned is how long a running task may go without reporting before it
	// is failed. Must be longer than the slowest legitimate task, because a
	// working four-hour restore declared abandoned would be released and run
	// again beside itself.
	Abandoned time.Duration
}

// Run drains the queue until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	if a.Poll <= 0 {
		a.Poll = 5 * time.Second
	}
	if a.Abandoned <= 0 {
		a.Abandoned = 6 * time.Hour
	}
	a.Name = a.worker()
	a.Log = a.log()
	a.Log.Info("backup agent started", slog.String("name", a.Name))

	reap := time.NewTicker(a.Abandoned / 4)
	defer reap.Stop()

	for {
		select {
		case <-ctx.Done():
			a.Log.Info("backup agent stopping")
			return nil
		case <-reap.C:
			if n, err := a.Tasks.Reap(ctx, a.Abandoned); err == nil && n > 0 {
				a.Log.Warn("failed abandoned backup tasks", slog.Int("count", n))
			}
		default:
		}

		worked, err := a.Step(ctx)
		if err != nil {
			a.Log.Error("could not claim work", slog.String("error", err.Error()))
		}
		if !worked {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(a.Poll):
			}
		}
	}
}

// log is the logger, defaulted here rather than only in `Run`.
//
// `Step` is called directly by `agent -once` and by tests, which do not go
// through `Run`. Defaulting in one place and reading in another is how a
// backup agent panics on its first claimed task, which is exactly what it did.
func (a *Agent) log() *slog.Logger {
	if a.Log == nil {
		return slog.Default()
	}
	return a.Log
}

// worker is the name this agent claims work under.
func (a *Agent) worker() string {
	if a.Name != "" {
		return a.Name
	}
	host, _ := os.Hostname()
	return "backup-agent@" + host
}

// Step claims and runs at most one task. Exported so a test can drive it
// without a loop, and so `rawsyst backup agent -once` can drain one.
func (a *Agent) Step(ctx context.Context) (bool, error) {
	task, found, err := a.Tasks.Claim(ctx, a.worker())
	if err != nil || !found {
		return false, err
	}
	a.log().Info("backup task claimed",
		slog.String("kind", task.Kind),
		slog.String("snapshot", task.SnapshotID))

	report, runErr := a.run(ctx, task)
	reason := ""
	if runErr != nil {
		reason = runErr.Error()
		a.log().Error("backup task failed",
			slog.String("kind", task.Kind),
			slog.String("error", reason))
	}
	if err := a.Tasks.Finish(ctx, task.ID, report, reason); err != nil {
		a.log().Error("could not close a backup task",
			slog.String("error", err.Error()))
	}
	return true, nil
}

// run dispatches one task and returns whatever it should be remembered by.
func (a *Agent) run(ctx context.Context, task Task) (any, error) {
	opts := a.Options
	opts.Progress = func(stage string) {
		_ = a.Tasks.Stage(ctx, task.ID, stage)
	}

	switch task.Kind {
	case TaskCreate:
		return a.create(ctx, opts, task)
	case TaskVerify:
		return a.verify(ctx, opts, task)
	case TaskRestoreValidate:
		return a.validate(ctx, opts, task)
	case TaskRestoreProduction:
		return a.restoreProduction(ctx, opts, task)
	case TaskPrune:
		return a.prune(ctx, opts)
	default:
		return nil, errs.Newf(errs.CodeInvalidInput,
			"This agent does not know how to %q. It is running a different "+
				"build from whatever queued it.", task.Kind)
	}
}

func (a *Agent) create(
	ctx context.Context, opts Options, task Task,
) (any, error) {
	kind := "manual"
	if task.RequestedBy == "" {
		kind = "scheduled"
	}
	id, _ := a.Register.Start(ctx, kind, "server", nil)

	res, err := Run(ctx, opts)
	if err != nil {
		_ = a.Register.Failed(ctx, id, err.Error())
		return nil, err
	}
	if err := a.Register.Succeeded(ctx, id, res, opts.Store.Bucket()); err != nil {
		a.log().Error("a backup was taken and could not be recorded",
			slog.String("snapshot", res.SnapshotID),
			slog.String("error", err.Error()))
	}

	// Taken is not verified. The run continues straight into a verification
	// rather than leaving the operator to press a second button, because a
	// backup nobody has proved is a file — and because the one moment somebody
	// is definitely paying attention is the moment they pressed the button.
	report, verifyErr := Verify(ctx, opts, a.AdminDSN, res.SnapshotID)
	_ = a.Register.Verified(ctx, id, report, reasonOf(verifyErr))
	if verifyErr != nil {
		return report, verifyErr
	}
	return map[string]any{
		"snapshot_id": res.SnapshotID,
		"location":    res.Location,
		"bytes":       res.Bytes,
		"sha256":      res.SHA256,
		"manifest":    res.Manifest,
		"verify":      report,
	}, nil
}

func (a *Agent) verify(
	ctx context.Context, opts Options, task Task,
) (any, error) {
	record, err := a.Register.FindBySnapshot(ctx, task.SnapshotID)
	if err != nil {
		return nil, err
	}
	_ = a.Register.Phase(ctx, record.ID, PhaseVerifying)

	report, verifyErr := a.verifyWhereverItIs(ctx, opts, task, record)
	_ = a.Register.Verified(ctx, record.ID, report, reasonOf(verifyErr))
	return report, verifyErr
}

func (a *Agent) validate(
	ctx context.Context, opts Options, task Task,
) (any, error) {
	record, err := a.Register.FindBySnapshot(ctx, task.SnapshotID)
	if err != nil {
		return nil, err
	}
	_ = a.Register.Phase(ctx, record.ID, PhaseRestoreValidating)

	report, validateErr := a.verifyWhereverItIs(ctx, opts, task, record)
	_ = a.Register.Validated(ctx, record.ID, report, reasonOf(validateErr))
	return report, validateErr
}

// verifyWhereverItIs runs the same verification against the object store or
// against a staged upload, depending on where the artifact actually is.
//
// A backup that arrived on a USB stick is verified exactly as one from the
// bucket is: restored into a temporary database and compared with its manifest.
// The only difference is which bytes are opened, which is what this decides.
func (a *Agent) verifyWhereverItIs(
	ctx context.Context, opts Options, task Task, record Record,
) (VerifyReport, error) {
	if record.Source != "upload" {
		return Verify(ctx, opts, a.AdminDSN, task.SnapshotID)
	}
	dir := stagedDir(a.StagingDir, task.SnapshotID)
	names := NamesFor(task.SnapshotID)
	return VerifyLocal(ctx, opts, a.AdminDSN,
		dir+"/"+names.Dump, dir+"/"+names.Manifest)
}

// restoreProduction is the dangerous one.
//
// Its first act is to take a backup of what production currently holds, and to
// verify it. Not because a backup is due, but because the operation about to
// happen replaces data that exists nowhere else — and a safety backup that has
// not itself been proved is not a safety backup.
func (a *Agent) restoreProduction(
	ctx context.Context, opts Options, task Task,
) (any, error) {
	prod := a.Production
	if !prod.Enabled {
		return nil, errs.New(errs.CodeInvalidInput,
			"Replacing the live database from a backup is switched off in "+
				"this deployment. Set RAWSYST_ALLOW_PRODUCTION_RESTORE=true "+
				"on the backup agent, and read deploy/server/RECOVERY.md "+
				"before you do.")
	}

	var params Params
	if len(task.Params) > 0 {
		_ = json.Unmarshal(task.Params, &params)
	}
	prod.Confirm = params.Confirm

	// The snapshot has to have been rehearsed. A production restore is not the
	// place to find out whether a backup comes back.
	record, err := a.Register.FindBySnapshot(ctx, task.SnapshotID)
	if err != nil {
		return nil, err
	}
	if record.Phase != PhaseRestoreReady {
		return nil, errs.Newf(errs.CodeInvalidInput,
			"Snapshot %s has not passed a restore validation, so it will not "+
				"be put into production. Run Validate Restore on it first: it "+
				"restores into a temporary database and checks it, and takes "+
				"about as long as this would.", task.SnapshotID)
	}
	// A backup carried in from somewhere else is read from the staging
	// directory, because there may be no object store on this server at all —
	// which is the ordinary case when the old one is gone.
	if record.Source == "upload" {
		prod.StagedDir = stagedDir(a.StagingDir, task.SnapshotID)
	}

	// The safety backup. Taken now, of what production holds now — unless
	// production holds nothing, which is the case on a machine built an hour
	// ago to recover on to. There is no data to protect there, and requiring a
	// backup of an empty database would need an object store the new server
	// may not have yet and would block the recovery this exists for.
	opts.Progress(StagePreparing)
	empty, err := LooksEmpty(ctx, LiveDSN(prod.AdminDSN, prod.LiveDatabase))
	if err != nil {
		return nil, err
	}
	if empty {
		prod.SafetyBackup = "not needed: the live database holds no tables"
		params.SafetyBackup = prod.SafetyBackup
		_ = a.Tasks.SetParams(ctx, task.ID, params)
	} else {
		safetyID, _ := a.Register.Start(ctx, "manual", "server", nil)
		safety, err := Run(ctx, opts)
		if err != nil {
			_ = a.Register.Failed(ctx, safetyID, err.Error())
			return nil, errs.Newf(errs.CodeInternal,
				"A backup of what the live database holds right now could not "+
					"be taken, so the restore has not started and nothing has "+
					"changed: %v", err)
		}
		_ = a.Register.Succeeded(ctx, safetyID, safety, opts.Store.Bucket())

		safetyReport, err := Verify(ctx, opts, a.AdminDSN, safety.SnapshotID)
		_ = a.Register.Verified(ctx, safetyID, safetyReport, reasonOf(err))
		if err != nil {
			return nil, errs.Newf(errs.CodeInternal,
				"The safety backup of the live database was taken and did not "+
					"verify, so the restore has not started and nothing has "+
					"changed: %v", err)
		}
		prod.SafetyBackup = safety.SnapshotID
		params.SafetyBackup = safety.SnapshotID
		_ = a.Tasks.SetParams(ctx, task.ID, params)
	}

	// The freeze, for the cutover only. Held from just before the two renames
	// to just after, which is seconds — long enough that nothing is accepted
	// into a database about to be moved, short enough that a shop does not
	// notice a rollback happening.
	if a.Maintenance != nil {
		prod.Freeze = func(ctx context.Context, reason string) error {
			_, err := a.Maintenance.Begin(ctx, reason, true, nil,
				"the backup agent")
			return err
		}
		prod.Unfreeze = func(ctx context.Context) error {
			_, err := a.Maintenance.End(ctx)
			return err
		}
	}

	_ = a.Register.Phase(ctx, record.ID, PhaseRestoring)
	report, err := RestoreToProduction(ctx, opts, prod, task.SnapshotID)
	if err != nil {
		_ = a.Register.Phase(ctx, record.ID, PhaseRestoreFailed)
		return report, err
	}

	// From here on, "the database" is a different database. Everything written
	// above is in the one that was renamed aside, and the one now serving has
	// never heard of this operation — so the first thing to do is tell it, in
	// a row that names where the previous database went. That row is the
	// rollback instructions.
	if err := a.Register.RecordRestore(
		ctx, task.SnapshotID, report, nil); err != nil {
		a.log().Error("the restore succeeded and could not be recorded in the "+
			"database it installed",
			slog.String("snapshot", task.SnapshotID),
			slog.String("previous_database", report.PreviousDatabase),
			slog.String("error", err.Error()))
	}
	return report, nil
}

func (a *Agent) prune(ctx context.Context, opts Options) (any, error) {
	protected, err := a.Register.Protected(ctx)
	if err != nil {
		return nil, err
	}
	removed, err := Prune(ctx, opts, PolicyFromEnv(), protected, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"removed":   removed,
		"protected": protected,
	}, nil
}

// stagedDir is where an uploaded artifact lives.
//
// Composed from a snapshot id that `ValidSnapshotID` has already restricted to
// letters, digits, dash and underscore, which is what stops a request naming a
// directory outside the staging area. Nothing here trusts a filename from an
// upload: the three names are this product's own.
func stagedDir(root, id string) string {
	return strings.TrimRight(root, "/") + "/uploads/" + id
}

// StagedDir is `stagedDir` for callers outside this package — the upload
// handler, which has to put the files there.
func StagedDir(root, id string) string { return stagedDir(root, id) }

func reasonOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
