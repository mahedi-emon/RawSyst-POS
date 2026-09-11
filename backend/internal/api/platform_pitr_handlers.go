// Point-in-time recovery, over HTTP.
//
// # Who may reach any of this
//
// A platform operator, and nobody else. Every route here is `AccessSuperAdmin`
// in the router, and that is the whole security model rather than a starting
// point: a physical copy of this cluster is every business on the server at
// once, and a recovery of it produces a readable copy of all of them at some
// earlier moment. There is no tenant permission that could safely reach it,
// because a permission a business owner could grant themselves would be a
// permission that lets one shop recover another's books.
//
// The tables behind these routes carry platform-only row-level security as
// well. That is the second lock, not the first.
//
// # What is never in a response
//
// Storage credentials, the encryption key, presigned URLs, and object paths. A
// manifest carries the bucket NAME and the key FINGERPRINT, which identify
// things without being them; a recovery window carries times and segment names.
// Nothing here hands back something that could be used to reach the archive
// from outside this server — the archive is reached by the agent, server-side,
// with credentials that live in its environment.
//
// # Why every write queues rather than does
//
// A base backup takes minutes to hours and a recovery unpacks a cluster and
// starts a PostgreSQL. A request that did either would die with the browser
// tab, holding a staging volume and a replication slot. So these enqueue, the
// agent claims, and the screen polls the task — the same arrangement the dump
// routes already use, and the reason `backup_task` exists.
package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/backup"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// --- reading ----------------------------------------------------------------

// handlePITRStatus is what the Recovery screen reads.
//
// Served from the row the agent keeps rather than by asking the object store,
// because this is polled while a screen is open and a listing is a round trip
// to another company. The row says when it was taken and the response carries
// that through: a reading older than fifteen minutes is marked stale, so a
// green from six hours ago is not shown as though it described now.
func (s *Server) handlePITRStatus(w http.ResponseWriter, r *http.Request) {
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	if s.walBackups == nil {
		httpx.Error(w, r, pitrNotWired())
		return
	}
	state, err := s.walBackups.State(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	bases, err := s.walBackups.ListBases(r.Context(), 10)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The two numbers a person actually asks for, computed here rather than on
	// the screen: whether point-in-time recovery is available at all, and how
	// wide the window is. A front end that worked those out itself would be a
	// second opinion about the most consequential claim this product makes.
	available := state.Archiving &&
		state.WindowStart != "" && state.WindowEnd != "" &&
		state.ArchiveGaps == 0 && usableBase(bases)

	httpx.JSON(w, http.StatusOK, map[string]any{
		"available":    available,
		"archive":      state,
		"base_backups": bases,
		"retention": map[string]any{
			"window_days":       int(backup.PITRPolicyFromEnv().Window / (24 * time.Hour)),
			"keep_base_backups": backup.PITRPolicyFromEnv().KeepBaseBackups,
		},
	})
}

// usableBase reports whether any base backup could be recovered from.
//
// `stored` counts as well as `verified`: a copy that is in the store and has
// not yet been recovered from is still the only thing standing between a
// business and a lost week, and refusing to say so would understate what is
// there. The screen shows the two differently, which is where the distinction
// belongs.
func usableBase(bases []backup.BaseRecord) bool {
	for _, b := range bases {
		if b.Status == "stored" || b.Status == "verified" {
			return true
		}
	}
	return false
}

// handlePITRWindow answers "can I get back to this moment".
//
// Reads the object store rather than the cached row, deliberately: this is the
// question asked immediately before somebody commits to a recovery, and an
// answer from a row written up to a minute ago is an answer about a window that
// may have moved. One extra listing at the moment of decision is the right
// price.
func (s *Server) handlePITRWindow(w http.ResponseWriter, r *http.Request) {
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	opts, err := s.walOptions()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	window, err := backup.ComputeRecoveryWindow(r.Context(), opts)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out := map[string]any{"window": window}

	// An `at` in the query turns this from "what is available" into "is this
	// particular moment available", which is what the screen asks as somebody
	// picks a time. The answer is a sentence, not a boolean, because "no"
	// without a reason sends an operator looking in the wrong place.
	if raw := strings.TrimSpace(r.URL.Query().Get("at")); raw != "" {
		at, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"That is not a moment. Times are RFC 3339, like "+
					"2026-09-11T14:32:00Z."))
			return
		}
		if err := backup.ExplainTarget(window, at.UTC()); err != nil {
			out["recoverable"] = false
			out["because"] = userMessage(err)
		} else {
			out["recoverable"] = true
			base, err := s.baseFor(r, opts, at.UTC())
			if err == nil {
				out["base_backup"] = base
			}
		}
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) baseFor(
	r *http.Request, opts backup.WALOptions, at time.Time,
) (string, error) {
	bases, err := backup.CompletedBaseBackups(r.Context(), opts)
	if err != nil {
		return "", err
	}
	chosen, err := backup.BaseBackupFor(bases, at)
	if err != nil {
		return "", err
	}
	return chosen.ID, nil
}

// handleListBaseBackups is the physical copies, newest first.
func (s *Server) handleListBaseBackups(w http.ResponseWriter, r *http.Request) {
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	if s.walBackups == nil {
		httpx.Error(w, r, pitrNotWired())
		return
	}
	list, err := s.walBackups.ListBases(r.Context(), 30)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

// handleDownloadBaseBackup streams one file of a physical base backup out.
//
// # Why this exists, given that a base backup is the less portable half
//
// A `pg_dump` snapshot is the thing to carry on a laptop: it restores onto any
// machine with a PostgreSQL on it, it survives a corrupt cluster, and
// `RECOVERY.md` section C is a whole procedure built on having three files and
// nothing else. A base backup is none of those things — it only reads on its
// own major version, it carries any page corruption with it, and on its own it
// recovers exactly one moment.
//
// It is offered anyway, for two situations that are real:
//
//   - The bucket is being migrated, or its provider is being left. A base
//     backup that can only be copied by the account that owns the bucket is a
//     backup held hostage by a billing relationship.
//   - An investigation. When a recovery fails on the server, being able to take
//     the artifact somewhere else and open it is the difference between a
//     diagnosis and a guess.
//
// # What comes out
//
// The bytes exactly as the store holds them, which means STILL SEALED when
// encryption is on. The API does not hold the key and does not decrypt — see
// `walOptions`. An operator who downloads this needs the key from wherever the
// key is kept, which is the arrangement `SECRETS.md` describes and not an
// inconvenience to be designed away.
//
// # There is no path here
//
// The object key is composed from the configured prefix, an id that
// `ValidSnapshotID` has restricted to letters, digits, dash and underscore, and
// one of four fixed names chosen by a switch. `BaseBackupKey` refuses anything
// else. Nothing from the request reaches the store as a path.
func (s *Server) handleDownloadBaseBackup(w http.ResponseWriter, r *http.Request) {
	// Streams a copy of the whole cluster out of the object store. The same
	// class as a dump download, for the same reason. See
	// platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupTransfer) {
		return
	}
	opts, err := s.walOptions()
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	id := chi.URLParam(r, "baseID")
	if !backup.ValidSnapshotID(id) {
		httpx.Error(w, r, errs.New(errs.CodeNotFound, "No such base backup."))
		return
	}

	// The manifest first, and not only to name the file.
	//
	// It is what proves the backup is COMPLETE: a manifest is written after
	// every component is in the store and before the marker. Handing somebody
	// a `base.tar.gz` from a backup whose upload died would be handing them a
	// truncated cluster to carry away and discover later.
	manifest, err := backup.ReadBaseManifest(r.Context(), opts, id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	names := backup.BaseNamesFor(id)
	var object, filename, contentType string
	switch chi.URLParam(r, "what") {
	case "", "base":
		object, filename = backup.BaseTarObject(), names.Base
		contentType = "application/octet-stream"
	case "wal":
		object, filename = backup.BaseWALTarObject(), names.WAL
		contentType = "application/octet-stream"
	case "manifest":
		object, filename = backup.BaseManifestObject(), names.Manifest
		contentType = "application/json"
	case "pg-manifest":
		object, filename = backup.BasePGManifestObject(), names.PGManifest
		contentType = "application/json"
	default:
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"A base backup has four parts: base, wal, manifest and "+
				"pg-manifest."))
		return
	}

	key, err := backup.BaseBackupKey(opts.Prefix, id, object)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	body, size, err := s.backupStore.GetStream(r.Context(), key)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	defer body.Close()

	// Audited BEFORE the bytes leave, because a download that is interrupted
	// halfway still took the data as far as the wire. A trail that recorded
	// only completed downloads would miss the interesting ones.
	//
	// The key fingerprint is in the entry and the key is not. Knowing WHICH
	// key a carried-away artifact needs is what somebody investigating wants;
	// the key itself has no business in a row every operator can read.
	sealed := manifest.Encryption != nil
	fingerprint := ""
	if sealed {
		fingerprint = manifest.Encryption.KeyFingerprint
	}
	s.auditBackup(r, "base_backup_downloaded", id, map[string]any{
		"part": object, "bytes": size,
		"encrypted": sealed, "key_fingerprint": fingerprint,
	})

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	// The checksum of the object as stored, so whoever downloads it can check
	// the file without opening the manifest — and, when it is sealed, the
	// fingerprint of the key they will need. Neither is secret.
	if c, ok := manifest.Component(object); ok && c.SHA256 != "" {
		w.Header().Set("X-RawSyst-SHA256", c.SHA256)
	}
	if sealed {
		w.Header().Set("X-RawSyst-Encrypted", "aes-256-gcm")
		w.Header().Set("X-RawSyst-Key-Fingerprint", fingerprint)
	}
	w.WriteHeader(http.StatusOK)

	// Nothing useful can be sent after this point: the status line has gone. A
	// failure here ends the download short, which is what every interrupted
	// download looks like, and the checksum is what tells the operator.
	_, _ = io.Copy(w, body)
}

// handleListRecoveries is the audit trail of who recovered what to when.
func (s *Server) handleListRecoveries(w http.ResponseWriter, r *http.Request) {
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	if s.walBackups == nil {
		httpx.Error(w, r, pitrNotWired())
		return
	}
	list, err := s.walBackups.ListRecoveries(r.Context(), 30)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

// handleArchiveFailures is the failed archive attempts, newest first.
func (s *Server) handleArchiveFailures(w http.ResponseWriter, r *http.Request) {
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	if s.walBackups == nil {
		httpx.Error(w, r, pitrNotWired())
		return
	}
	list, err := s.walBackups.RecentArchiveFailures(r.Context(), 50)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, list)
}

// --- asking for work --------------------------------------------------------

// handleCreateBaseBackup queues a physical copy of the cluster.
func (s *Server) handleCreateBaseBackup(w http.ResponseWriter, r *http.Request) {
	// Copies the whole cluster. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupHeavy) {
		return
	}
	s.queueBackupTask(w, r, backup.TaskBaseBackup, "", backup.Params{},
		"base_backup_requested")
}

// handleVerifyArchive queues a check of the archive.
func (s *Server) handleVerifyArchive(w http.ResponseWriter, r *http.Request) {
	if !s.limitBackup(w, r, rateBackupHeavy) {
		return
	}
	var req struct {
		Deep int `json:"deep_sample"`
	}
	_ = httpx.Decode(r, &req)
	if req.Deep < 0 {
		req.Deep = 0
	}
	if req.Deep > 64 {
		// The deep check downloads segments and is charged by the gigabyte.
		// Sixty-four is already a gigabyte of egress; a request asking for a
		// thousand is a request that would produce a bill rather than an
		// answer.
		req.Deep = 64
	}
	s.queueBackupTask(w, r, backup.TaskWALVerify, "",
		backup.Params{DeepSample: req.Deep}, "wal_verify_requested")
}

// handlePruneArchive queues a retention run over the archive.
//
// Defaults to a dry run and requires `apply` to actually delete, which is the
// opposite of the convention everywhere else in this API and is deliberate:
// this is the only route that removes the last copy of something.
func (s *Server) handlePruneArchive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Apply bool `json:"apply"`
	}
	_ = httpx.Decode(r, &req)

	rate := rateBackupHeavy
	if req.Apply {
		rate = rateBackupDestructive
	}
	if !s.limitBackup(w, r, rate) {
		return
	}
	action := "wal_retention_previewed"
	if req.Apply {
		action = "wal_retention_applied"
	}
	s.queueBackupTask(w, r, backup.TaskWALPrune, "",
		backup.Params{Apply: req.Apply}, action)
}

// handlePITRRestore queues a recovery to a moment, into an isolated PostgreSQL.
//
// # Why this is not the dangerous route
//
// It cannot touch production. The agent unpacks a base backup into a directory
// of its own and starts a second PostgreSQL on a socket nothing else can
// reach; there is no code path from here to the live cluster. Replacing
// production remains `POST /platform/backups/{id}/restore-production`, which
// takes a dump, requires the id typed out, requires a rehearsal that passed,
// and requires the deployment to have the capability switched on.
//
// # Why it is still confirmed
//
// Because the result is a readable copy of every business on this server at
// some earlier moment, sitting on the staging volume. That is not a production
// incident and it is not nothing, and the confirmation is what makes it a
// decision rather than a mis-click.
func (s *Server) handlePITRRestore(w http.ResponseWriter, r *http.Request) {
	// Unpacks a cluster and starts a second PostgreSQL. See
	// platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupHeavy) {
		return
	}
	if s.walBackups == nil {
		httpx.Error(w, r, pitrNotWired())
		return
	}

	var req struct {
		BaseBackupID string `json:"base_backup_id"`
		Target       string `json:"target"`
		At           string `json:"at"`
		Value        string `json:"value"`
		Timeline     uint32 `json:"timeline"`
		Confirm      string `json:"confirm"`
		Keep         bool   `json:"keep"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	target := backup.RecoveryTarget{
		Kind:     strings.TrimSpace(req.Target),
		Value:    strings.TrimSpace(req.Value),
		Timeline: req.Timeline,
	}
	if target.Kind == "" {
		target.Kind = backup.TargetLatest
	}
	if raw := strings.TrimSpace(req.At); raw != "" {
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"That is not a moment. Times are RFC 3339, like "+
					"2026-09-11T14:32:00Z."))
			return
		}
		target.At = at.UTC()
	}
	if err := target.Validate(); err != nil {
		httpx.Error(w, r, err)
		return
	}

	if strings.TrimSpace(req.Confirm) != "RECOVER" {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"A point-in-time recovery produces a readable copy of every "+
				"business on this server as they were at the chosen moment. "+
				"Production is not touched and cannot be by this operation. "+
				"Type RECOVER to confirm."))
		return
	}

	if req.BaseBackupID != "" && !backup.ValidSnapshotID(req.BaseBackupID) {
		httpx.Error(w, r, errs.New(errs.CodeNotFound, "No such base backup."))
		return
	}

	// A second recovery while one is running would fill the staging volume and
	// start a second postmaster on a server with two cores. The database
	// refuses it anyway — 0136 extends the one-heavy-task index — and this is
	// the refusal an operator can read before they press the button rather
	// than a constraint violation after it.
	if running, err := s.walBackups.ActiveRecovery(r.Context()); err == nil && running {
		httpx.Error(w, r, errs.New(errs.CodeConflict,
			"A recovery is already running. This server does one at a time: "+
				"each unpacks a copy of the cluster and starts a PostgreSQL of "+
				"its own. Watch the one in progress under Operations."))
		return
	}

	// The window, checked here so an operator is told no in a second rather
	// than in an hour. The agent checks it again when it starts, because the
	// window moves and the one that matters is the one at the moment of the
	// recovery.
	if target.WantsMoment() {
		opts, err := s.walOptions()
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		window, err := backup.ComputeRecoveryWindow(r.Context(), opts)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		if err := backup.ExplainTarget(window, target.At); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}

	params := backup.Params{
		BaseBackupID: req.BaseBackupID,
		TargetKind:   target.Kind,
		TargetValue:  target.Value,
		Timeline:     target.Timeline,
		Keep:         req.Keep,
	}
	if target.WantsMoment() {
		params.TargetAt = target.At.Format(time.RFC3339)
	}

	s.queueBackupTask(w, r, backup.TaskPITRRestore, req.BaseBackupID, params,
		"pitr_restore_requested")
}

// --- wiring -----------------------------------------------------------------

// walOptions is the archive as this server sees it.
//
// The key is deliberately NOT here. The API never decrypts anything: it lists,
// it reads manifests, and it queues work for the agent, which holds the key in
// its own environment. An API process that could open a base backup would be an
// API process worth attacking for it.
func (s *Server) walOptions() (backup.WALOptions, error) {
	if s.backupStore == nil || !s.backupStore.Configured() {
		return backup.WALOptions{}, pitrNotWired()
	}
	return backup.WALOptions{
		Store:  s.backupStore,
		Prefix: s.backupPrefix,
		Layout: backup.DefaultWALLayout(),
	}, nil
}

// userMessage is the sentence out of an error, without the code in front of it.
//
// Used in one place: the body of a "can I recover to this moment" answer, where
// the reason is DATA rather than a failure — the request succeeded and the
// answer is no. `err.Error()` would put `invalid_input: ` in front of a
// sentence somebody reads on a screen.
func userMessage(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

func pitrNotWired() error {
	return errs.New(errs.CodeUnavailable,
		"Point-in-time recovery is not configured on this installation. It "+
			"needs an object store for the write-ahead log archive and "+
			"archive_mode on; see deploy/server/PITR.md.")
}
