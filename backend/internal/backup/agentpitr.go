// The four pieces of point-in-time recovery work the agent does.
//
// # Why these live beside the dump work and not in their own process
//
// They need the same three things `agent.go` already has: the PostgreSQL client
// tools, a staging volume with room for a copy of the cluster, and the queue
// that makes one heavy operation at a time a rule rather than a hope. A second
// resident process would need all three again and would have to coordinate with
// this one about which of them was allowed to fill the disk.
//
// # What each of them does, and what it costs
//
//	base_backup   pg_basebackup of the whole cluster, sealed and uploaded.
//	              Minutes to hours. Heavy: one at a time, and never beside a
//	              dump.
//	pitr_restore  a recovery to a moment, into a PostgreSQL of its own.
//	              Heavy for the same reason, and then some: it unpacks a
//	              cluster and starts a second postmaster.
//	wal_verify    a listing and some small reads. Cheap enough to run hourly.
//	wal_prune     a listing and some deletes. Cheap, and refuses far more
//	              often than it deletes.
//
// # Why a base backup verifies itself
//
// Taking one and not checking it produces a file in a bucket, which is the
// exact thing `register.go` was written to stop a dump from being. So a base
// backup is followed by a recovery of itself to `immediate` — the cheapest
// possible recovery, replaying only the log the backup carries — and the row
// only reaches `verified` when that produced a cluster somebody counted.
package backup

import (
	"context"
	"log/slog"
	"time"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// PITRSupport is what the agent needs to do any of this.
//
// Held separately from `Agent`'s other fields so that an agent built without it
// — an older deployment, a test that only cares about dumps — refuses the new
// task kinds with a sentence rather than a nil dereference.
type PITRSupport struct {
	// WAL is the store, the prefix and the key.
	WAL WALOptions

	// BaseDSN is the connection a base backup is taken over. The backup role,
	// which needs the REPLICATION attribute as well as BYPASSRLS.
	BaseDSN string

	// ObserveDSN is the connection the archive readout is taken over.
	ObserveDSN string

	// Register is the point-in-time half of the register.
	Register *WALRegister

	// StagingDir is where a base backup is written and a recovery is unpacked.
	StagingDir string

	// Policy is how much recoverable history this installation keeps.
	Policy PITRPolicy

	// MinFreePercent is the room a base backup requires before it starts.
	MinFreePercent int

	AppVersion  string
	GitCommit   string
	Environment string
	SourceHost  string

	// BinDir holds pg_basebackup, pg_ctl and postgres. Empty means the PATH,
	// which inside the backup image is the right major version by
	// construction.
	BinDir string

	// BaseTimeout and RestoreTimeout bound the two long operations.
	BaseTimeout    time.Duration
	RestoreTimeout time.Duration
}

// Configured reports whether this agent can do point-in-time work at all.
func (p *PITRSupport) Configured() bool {
	return p != nil && p.WAL.Configured()
}

func (a *Agent) pitrSupport() (*PITRSupport, error) {
	if !a.PITR.Configured() {
		return nil, errs.New(errs.CodeUnavailable,
			"This agent has no write-ahead log archive configured, so it "+
				"cannot take a physical base backup or recover to a moment. "+
				"Set RAWSYST_S3_ENDPOINT and RAWSYST_S3_BUCKET and turn "+
				"archiving on; see deploy/server/PITR.md.")
	}
	return a.PITR, nil
}

// --- taking a base backup ---------------------------------------------------

func (a *Agent) baseBackup(
	ctx context.Context, task Task,
) (any, error) {
	p, err := a.pitrSupport()
	if err != nil {
		return nil, err
	}

	id, _ := p.Register.StartBase(ctx, nil, task.RequestedBy)

	opts := BaseBackupOptions{
		DSN:            p.BaseDSN,
		WAL:            p.WAL,
		StagingDir:     p.StagingDir,
		MinFreePercent: p.MinFreePercent,
		Timeout:        p.BaseTimeout,
		AppVersion:     p.AppVersion,
		GitCommit:      p.GitCommit,
		Environment:    p.Environment,
		SourceHost:     p.SourceHost,
		Progress: func(stage string) {
			_ = a.Tasks.Stage(ctx, task.ID, stage)
		},
		Warn: func(msg string) {
			a.log().Warn("base backup", slog.String("note", msg))
		},
	}

	manifest, err := TakeBaseBackup(ctx, opts)
	if err != nil {
		_ = p.Register.BaseFailed(ctx, id, err.Error())
		return nil, err
	}
	if err := p.Register.BaseStored(ctx, id, manifest); err != nil {
		a.log().Error("a base backup was taken and could not be recorded",
			slog.String("base_backup", manifest.ID),
			slog.String("error", err.Error()))
	}

	// Stored is not verified. A recovery of it to `immediate` replays only the
	// log the backup carries, which is the cheapest proof available that the
	// copy is readable, that the key is right and that the cluster inside it
	// starts.
	_ = a.Tasks.Stage(ctx, task.ID, StageVerifying)
	report, verifyErr := a.recoverAndInspect(ctx, p, PITROptions{
		WAL:          p.WAL,
		BaseBackupID: manifest.ID,
		Target:       RecoveryTarget{Kind: TargetImmediate},
		WorkDir:      p.StagingDir,
		BinDir:       p.BinDir,
		Timeout:      p.RestoreTimeout,
		// A deployment recovering its own cluster: a result with no
		// migration ledger in it is a finding, not an observation.
		ExpectProduct: true,
		Progress: func(stage string) {
			_ = a.Tasks.Stage(ctx, task.ID, stage)
		},
		Log: a.log(),
	}, "drill", task.RequestedBy)

	if verifyErr != nil {
		_ = p.Register.BaseInvalid(ctx, manifest.ID, verifyErr.Error(), report)
		return map[string]any{
			"base_backup_id": manifest.ID,
			"manifest":       manifest,
			"verify":         report,
		}, verifyErr
	}
	if !report.Passed {
		reason := "The base backup was recovered and the result did not check out."
		if len(report.Findings) > 0 {
			reason = report.Findings[0]
		}
		_ = p.Register.BaseInvalid(ctx, manifest.ID, reason, report)
		return map[string]any{
			"base_backup_id": manifest.ID,
			"manifest":       manifest,
			"verify":         report,
		}, errs.New(errs.CodeInternal, reason)
	}
	_ = p.Register.BaseVerified(ctx, manifest.ID, report)

	return map[string]any{
		"base_backup_id": manifest.ID,
		"bytes":          manifest.TotalBytes(),
		"timeline":       manifest.Timeline,
		"start_lsn":      manifest.StartLSN,
		"end_lsn":        manifest.EndLSN,
		"manifest":       manifest,
		"verify":         report,
	}, nil
}

// --- recovering to a moment -------------------------------------------------

func (a *Agent) pitrRestore(ctx context.Context, task Task) (any, error) {
	p, err := a.pitrSupport()
	if err != nil {
		return nil, err
	}

	params, err := task.decodeParams()
	if err != nil {
		return nil, err
	}
	target := params.Target()
	if err := target.Validate(); err != nil {
		return nil, err
	}

	baseID := params.BaseBackupID
	if baseID == "" {
		baseID = task.SnapshotID
	}

	report, runErr := a.recoverAndInspect(ctx, p, PITROptions{
		WAL:           p.WAL,
		BaseBackupID:  baseID,
		Target:        target,
		WorkDir:       p.StagingDir,
		BinDir:        p.BinDir,
		Keep:          params.Keep,
		Timeout:       p.RestoreTimeout,
		ExpectProduct: true,
		Progress: func(stage string) {
			_ = a.Tasks.Stage(ctx, task.ID, stage)
		},
		Log: a.log(),
	}, "isolated", task.RequestedBy)

	if runErr != nil {
		return report, runErr
	}
	if !report.Passed {
		reason := "The recovery finished and the result did not check out."
		if len(report.Findings) > 0 {
			reason = report.Findings[0]
		}
		return report, errs.New(errs.CodeInternal, reason)
	}
	return report, nil
}

// recoverAndInspect runs one recovery and audits it, whatever asked for it.
//
// The audit row is opened BEFORE the recovery and closed after, including when
// the recovery failed and including when it was refused. A recovery produces a
// readable copy of every business on the platform at some earlier moment, so
// who asked for it and what moment they chose is exactly what an investigation
// would want — and a row written only on success would leave the refused ones,
// which are the interesting ones, unrecorded.
func (a *Agent) recoverAndInspect(
	ctx context.Context, p *PITRSupport,
	opts PITROptions, kind, by string,
) (PITRReport, error) {
	auditID, _ := p.Register.StartRecovery(
		ctx, kind, opts.BaseBackupID, opts.Target, nil, by)

	report, err := RunPITR(ctx, opts)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeInvalidInput {
			_ = p.Register.RefuseRecovery(ctx, auditID, err.Error())
		} else {
			_ = p.Register.FinishRecovery(ctx, auditID, report, err)
		}
		return report, err
	}
	_ = p.Register.FinishRecovery(ctx, auditID, report, nil)
	return report, nil
}

// --- checking and pruning the archive ---------------------------------------

func (a *Agent) walVerify(ctx context.Context, task Task) (any, error) {
	p, err := a.pitrSupport()
	if err != nil {
		return nil, err
	}
	params, err := task.decodeParams()
	if err != nil {
		return nil, err
	}
	_ = a.Tasks.Stage(ctx, task.ID, StageChecking)

	report, err := VerifyArchive(ctx, p.WAL, params.DeepSample)
	if err != nil {
		return nil, err
	}
	if !report.Passed {
		return report, errs.New(errs.CodeInternal, report.Findings[0])
	}
	return report, nil
}

func (a *Agent) walPrune(ctx context.Context, task Task) (any, error) {
	p, err := a.pitrSupport()
	if err != nil {
		return nil, err
	}
	params, err := task.decodeParams()
	if err != nil {
		return nil, err
	}
	_ = a.Tasks.Stage(ctx, task.ID, StageCleaningUp)

	report, err := PruneWAL(ctx, p.WAL, p.Policy, !params.Apply)
	if err != nil {
		return nil, err
	}
	if params.Apply && len(report.RemovedBaseBackups) > 0 {
		// The rows stay and are marked expired. A recovery window that got
		// shorter is a thing an operator may have to explain, and deleting the
		// evidence that a base backup existed makes that impossible.
		if err := p.Register.BaseExpired(ctx, report.RemovedBaseBackups); err != nil {
			a.log().Warn("expired base backups could not be marked",
				slog.String("error", err.Error()))
		}
	}
	return report, nil
}
