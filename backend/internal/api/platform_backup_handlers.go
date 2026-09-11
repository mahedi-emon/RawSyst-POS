// Backup & Recovery, from the website.
//
// # Who may reach any of this
//
// Only the platform control plane. Every route in this file is
// `AccessSuperAdmin`, which is `RequireSuperAdmin`, which answers 404 to
// everybody else — a business owner, an accountant, a cashier, and a business
// owner who has given themselves every permission their own plan offers.
//
// That is not a nicety. A full database dump is every business on this server
// at once: their customers, their staff's pay, their bank details. A tenant
// permission that could reach it would be a permission that lets one shop
// download another's books, and no arrangement of `backup.view` and
// `backup.run` can make that safe. The tenant-scoped `/api/v1/backups` routes
// still exist and still show a business the record of ITS OWN backups; they
// cannot produce, verify or download an artifact, and this file is why.
//
// # Nothing here does the work
//
// These handlers queue a task and return. The work happens in the agent, which
// runs in the postgres image because that is where `pg_dump` is. An HTTP
// request that dumped a database would hold a connection for four minutes,
// die with the browser tab, and put the PostgreSQL client tools in an image
// built from `scratch`.
//
// The one exception is the download, which streams from the object store
// through this process, and the upload, which streams the other way onto a
// staging disk. Neither touches a database.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/backup"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// MaxUploadBytes bounds an uploaded artifact.
//
// Eight gibibytes. The number is a disk decision on a 48 GB server, not a
// judgement about how big a backup may be: an upload larger than this needs
// the operator to put the file on the server and use `rawsyst backup
// restore-file`, which `deploy/server/RECOVERY.md` documents and which has no
// limit at all because it does not go through a web server.
const MaxUploadBytes = 8 << 30

// --- reading ----------------------------------------------------------------

func (s *Server) handlePlatformBackupHealth(w http.ResponseWriter, r *http.Request) {
	// A read a screen polls. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	if s.backups == nil {
		httpx.Error(w, r, backupsNotWired())
		return
	}
	state, err := s.backups.State(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out := map[string]any{"health": state}

	// What the store is, so an operator can see at a glance whether the
	// backups are actually going off this machine. Never a credential and
	// never the configured URL — the host, which identifies the provider and
	// is safe to put on a screen.
	storage := map[string]any{"configured": s.backupStore.Configured()}
	if s.backupStore.Configured() {
		storage["endpoint_host"] = s.backupStore.EndpointHost()
		storage["bucket"] = s.backupStore.Bucket()
		storage["region"] = s.backupStore.Region()
		storage["prefix"] = s.backupPrefix
	}
	out["storage"] = storage

	policy := backup.PolicyFromEnv()
	out["retention"] = map[string]any{
		"daily": policy.Daily, "weekly": policy.Weekly, "monthly": policy.Monthly,
	}
	out["encryption"] = map[string]any{
		// Whether the SERVER is configured to seal new backups. The API image
		// does not hold the key — the agent does — so this reports what the
		// most recent backup actually says about itself rather than guessing.
		"described_by": "the manifest of each backup",
	}
	out["production_restore_enabled"] = strings.EqualFold(
		os.Getenv("RAWSYST_ALLOW_PRODUCTION_RESTORE"), "true")

	if s.maintenance != nil {
		if st, err := s.maintenance.Read(r.Context()); err == nil {
			out["maintenance"] = st
		}
	}
	if task, found, err := s.backupTasks.Active(r.Context()); err == nil && found {
		out["active_task"] = task
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (s *Server) handlePlatformListBackups(w http.ResponseWriter, r *http.Request) {
	// A read a screen polls. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	if s.backups == nil {
		httpx.Error(w, r, backupsNotWired())
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.backups.List(r.Context(), limit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (s *Server) handlePlatformBackupDetail(w http.ResponseWriter, r *http.Request) {
	// A read a screen polls. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	id, err := s.snapshotParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	record, err := s.backups.Detail(r.Context(), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, record)
}

func (s *Server) handlePlatformBackupTasks(w http.ResponseWriter, r *http.Request) {
	// A read a screen polls. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	if s.backupTasks == nil {
		httpx.Error(w, r, backupsNotWired())
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.backupTasks.List(r.Context(), limit)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// handlePlatformBackupTask is what a screen watching an operation polls.
func (s *Server) handlePlatformBackupTask(w http.ResponseWriter, r *http.Request) {
	// A read a screen polls. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	if s.backupTasks == nil {
		httpx.Error(w, r, backupsNotWired())
		return
	}
	id, err := parseUUID(chi.URLParam(r, "taskID"), "taskID")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	task, err := s.backupTasks.Get(r.Context(), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, task)
}

// --- asking for work --------------------------------------------------------

func (s *Server) handlePlatformCreateBackup(w http.ResponseWriter, r *http.Request) {
	// Queues a dump of the whole database. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupHeavy) {
		return
	}
	s.queueBackupTask(w, r, backup.TaskCreate, "", backup.Params{},
		"backup_requested")
}

func (s *Server) handlePlatformVerifyBackup(w http.ResponseWriter, r *http.Request) {
	// Queues a restore into a scratch database. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupHeavy) {
		return
	}
	id, err := s.snapshotParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	s.queueBackupTask(w, r, backup.TaskVerify, id, backup.Params{},
		"backup_verify_requested")
}

func (s *Server) handlePlatformValidateRestore(w http.ResponseWriter, r *http.Request) {
	// Queues a full rehearsal of a restore. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupHeavy) {
		return
	}
	id, err := s.snapshotParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	s.queueBackupTask(w, r, backup.TaskRestoreValidate, id, backup.Params{},
		"restore_validation_requested")
}

func (s *Server) handlePlatformPruneBackups(w http.ResponseWriter, r *http.Request) {
	// Deletes snapshots. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupDestructive) {
		return
	}
	s.queueBackupTask(w, r, backup.TaskPrune, "", backup.Params{},
		"backup_retention_requested")
}

// handlePlatformRestoreProduction is the one that replaces the live database.
//
// Everything about it is a refusal until it is not. The confirmation has to be
// the snapshot id typed out; the snapshot has to have passed a restore
// validation; the deployment has to have the capability switched on; and the
// agent will take and verify a backup of what production currently holds before
// it touches anything. None of those is in this handler except the first —
// they are in the agent, where they cannot be skipped by a second caller.
func (s *Server) handlePlatformRestoreProduction(
	w http.ResponseWriter, r *http.Request,
) {
	// Replaces the live database. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupDestructive) {
		return
	}
	id, err := s.snapshotParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if strings.TrimSpace(req.Confirm) != id {
		httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
			"To replace the live database with snapshot %s, type that "+
				"snapshot's id into the confirmation. This is the last thing "+
				"standing between a mis-click and every business on this "+
				"server being served older data.", id))
		return
	}
	s.queueBackupTask(w, r, backup.TaskRestoreProduction, id,
		backup.Params{Confirm: req.Confirm}, "production_restore_requested")
}

// queueBackupTask is the shape every request for work has.
func (s *Server) queueBackupTask(
	w http.ResponseWriter, r *http.Request,
	kind, snapshotID string, params backup.Params, action string,
) {
	if s.backupTasks == nil {
		httpx.Error(w, r, backupsNotWired())
		return
	}
	who, label := s.actorOf(r)
	task, err := s.backupTasks.Enqueue(
		r.Context(), kind, snapshotID, who, label, params)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	s.auditBackup(r, action, snapshotID, map[string]any{
		"task": task.ID.String(), "kind": kind,
	})
	httpx.JSON(w, http.StatusAccepted, task)
}

// --- download ---------------------------------------------------------------

// handlePlatformDownloadBackup streams one file of a snapshot to the browser.
//
// # Streamed, never buffered
//
// The body is copied from the object store to the response as it arrives. A
// handler that read a multi-gigabyte dump into memory to send it would kill
// the API container on the first real backup, and would do it on the day the
// business had grown enough to need one.
//
// # What may be downloaded, and by whom
//
// A platform operator, and nobody else — see the file note. By default only a
// VERIFIED snapshot: handing somebody a file nobody has proved restores, to
// carry away as their disaster copy, is how a business ends up with a folder
// full of things that are not backups. `?unverified=true` overrides it,
// because an operator investigating a failed backup needs the artifact, and
// that override is audited by name.
//
// # There is no path here
//
// The object key is composed from the configured prefix, a snapshot id that
// `ValidSnapshotID` has restricted to letters, digits, dash and underscore,
// and one of three fixed names chosen by a switch. Nothing from the request
// reaches the store as a path.
func (s *Server) handlePlatformDownloadBackup(
	w http.ResponseWriter, r *http.Request,
) {
	// Streams the whole database out of the object store. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupTransfer) {
		return
	}
	id, err := s.snapshotParam(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !s.backupStore.Configured() {
		httpx.Error(w, r, errs.New(errs.CodeUnavailable,
			"No object store is configured, so there is nothing to download "+
				"from."))
		return
	}

	record, err := s.backups.FindBySnapshot(r.Context(), id)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	unverified := r.URL.Query().Get("unverified") == "true"
	if record.VerifiedAt == "" && !unverified {
		httpx.Error(w, r, errs.Newf(errs.CodeConflict,
			"Snapshot %s has not been proved to restore, so it is not offered "+
				"as a download. Verify it first. If you need the file anyway "+
				"to investigate a failure, ask for it with unverified=true — "+
				"that is recorded against your name.", id))
		return
	}
	if record.Source == "upload" {
		httpx.Error(w, r, errs.New(errs.CodeConflict,
			"This backup was uploaded to this server rather than taken by it, "+
				"and it is staged on disk rather than in the object store. "+
				"You already have the copy it came from."))
		return
	}

	names := backup.NamesFor(id)
	var object, filename, contentType string
	switch chi.URLParam(r, "what") {
	case "", "dump":
		object, filename = backup.DatabaseObject(), names.Dump
		contentType = "application/octet-stream"
	case "manifest":
		object, filename = backup.ManifestObject(), names.Manifest
		contentType = "application/json"
	case "checksum":
		// Composed here rather than fetched: it is one line derived from the
		// manifest, and a file the store does not hold cannot be corrupted in
		// the store.
		manifest, err := backup.ReadManifest(r.Context(), s.backupOptions(), id)
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
		body := backup.ChecksumFile(manifest.Database.SHA256, names.Dump)
		s.auditBackup(r, "backup_downloaded", id,
			map[string]any{"part": "checksum"})
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s"`, names.Checksum))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	default:
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"A backup has three parts: dump, manifest and checksum."))
		return
	}

	key := backup.SnapshotKey(s.backupPrefix, id, object)
	body, size, err := s.backupStore.GetStream(r.Context(), key)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	defer body.Close()

	// Audited BEFORE the bytes leave, because a download that is interrupted
	// halfway still took the data as far as the wire. A trail that only
	// records completed downloads is a trail that misses the interesting ones.
	s.auditBackup(r, "backup_downloaded", id, map[string]any{
		"part": object, "bytes": size, "unverified_override": unverified,
		"checksum": record.Checksum,
	})

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	// The checksum, in a header, so whoever is downloading can check the file
	// without opening the manifest.
	if record.Checksum != "" && object == backup.DatabaseObject() {
		w.Header().Set("X-RawSyst-SHA256", record.Checksum)
	}
	w.WriteHeader(http.StatusOK)

	// Nothing useful can be sent after this point: the status line has gone.
	// A failure here ends the download short, which is what every interrupted
	// download looks like, and the checksum is what tells the operator.
	_, _ = io.Copy(w, body)
}

// --- upload -----------------------------------------------------------------

// handlePlatformUploadBackup takes an artifact from an operator's computer.
//
// # This is the route that makes a dead server survivable
//
// The old machine is gone. What is left is three files somebody downloaded,
// and a new server that has never heard of the old one. This is how they get
// on to it through a browser, and `deploy/server/RECOVERY.md` documents the
// same thing over `scp` for a file too big for one.
//
// # Nothing uploaded is trusted
//
//   - the filename is never used: the three names are composed from the
//     snapshot id in the manifest, so nothing from the request becomes a path;
//   - the body is streamed to disk, hashed on the way, and never held in
//     memory;
//   - the size is bounded before a byte is read;
//   - the manifest has to parse, be a version this build reads, and name the
//     same snapshot the dump hashes to;
//   - the checksum has to match;
//   - the file has to BEGIN like a dump or like a sealed RawSyst backup;
//   - and after all of that the record says UPLOADED, not verified. Nothing
//     has been proved about it until it has been restored, which is a separate
//     act on a separate button.
func (s *Server) handlePlatformUploadBackup(
	w http.ResponseWriter, r *http.Request,
) {
	// Brings a whole database back in, up to 8 GiB. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupTransfer) {
		return
	}
	if s.backups == nil || s.backupStaging == "" {
		httpx.Error(w, r, errs.New(errs.CodeUnavailable,
			"This server has nowhere to stage an uploaded backup. Set "+
				"RAWSYST_BACKUP_STAGING_DIR to a directory with room on it."))
		return
	}

	// Bounded before anything is read. A body larger than this is refused
	// while it is still arriving rather than after it has filled the disk.
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes+(1<<20))

	reader, err := r.MultipartReader()
	if err != nil {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"Send the backup as a multipart form with a `dump` part and a "+
				"`manifest` part."))
		return
	}

	// Staged under a name this code chooses, in a directory it creates, and
	// moved into place only once everything checks out. A partial upload never
	// appears under a snapshot id.
	temp, err := os.MkdirTemp(s.backupStaging, "incoming-*")
	if err != nil {
		httpx.Error(w, r, errs.Wrap(err, errs.CodeInternal,
			"A place to put the upload could not be made."))
		return
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(temp)
		}
	}()

	var dumpPath string
	var dumpBytes int64
	var dumpSum string
	var manifestBody []byte
	var statedSum string

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
				"The upload ended before it was complete."))
			return
		}
		switch part.FormName() {
		case "dump":
			dumpPath = filepath.Join(temp, "database.dump")
			dumpBytes, dumpSum, err = streamToFile(part, dumpPath, MaxUploadBytes)
		case "manifest":
			manifestBody, err = io.ReadAll(io.LimitReader(part, 8<<20))
		case "checksum":
			var raw []byte
			raw, err = io.ReadAll(io.LimitReader(part, 4<<10))
			if fields := strings.Fields(string(raw)); len(fields) > 0 {
				statedSum = strings.ToLower(fields[0])
			}
		default:
			// Ignored rather than refused: a browser form may carry a token or
			// a field this route does not need, and failing on an unknown part
			// would make the route brittle for no gain.
			_, err = io.Copy(io.Discard, io.LimitReader(part, 1<<20))
		}
		part.Close()
		if err != nil {
			httpx.Error(w, r, err)
			return
		}
	}

	if dumpPath == "" || dumpBytes == 0 {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"No dump was uploaded, or it was empty."))
		return
	}
	if len(manifestBody) == 0 {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"No manifest was uploaded. A dump without its manifest cannot be "+
				"checked and will not be restored: there is nothing to say "+
				"what it should contain."))
		return
	}

	manifest, err := backup.ParseManifest(manifestBody, "")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !backup.ValidSnapshotID(manifest.SnapshotID) {
		httpx.Error(w, r, errs.New(errs.CodeInvalidInput,
			"The manifest names a snapshot with characters a snapshot id "+
				"cannot contain. This is not a manifest this product wrote."))
		return
	}
	if dumpBytes != manifest.Database.Bytes {
		httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
			"The uploaded dump is %d bytes and its manifest says %d. An "+
				"upload that did not finish looks exactly like this.",
			dumpBytes, manifest.Database.Bytes))
		return
	}
	if dumpSum != manifest.Database.SHA256 {
		httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
			"The uploaded dump hashes to %s and its manifest says %s. It is "+
				"not the file the manifest describes and it has not been kept.",
			dumpSum, manifest.Database.SHA256))
		return
	}
	if statedSum != "" && statedSum != manifest.Database.SHA256 {
		httpx.Error(w, r, errs.Newf(errs.CodeInvalidInput,
			"The .sha256 file says %s and the manifest says %s. These "+
				"describe the same dump and one of them has been changed.",
			statedSum, manifest.Database.SHA256))
		return
	}

	// What it starts with. A sealed artifact begins with this product's magic;
	// a custom-format dump begins with PGDMP. Anything else is not something
	// `pg_restore` will read, and finding out now is better than finding out
	// during a recovery.
	if err := looksLikeADump(dumpPath, manifest.Encryption != nil); err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Into place, under names this product composed.
	names := backup.NamesFor(manifest.SnapshotID)
	dir := backup.StagedDir(s.backupStaging, manifest.SnapshotID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		httpx.Error(w, r, errs.Wrap(err, errs.CodeInternal,
			"The staging directory could not be made."))
		return
	}
	if err := os.Rename(dumpPath, filepath.Join(dir, names.Dump)); err != nil {
		httpx.Error(w, r, errs.Wrap(err, errs.CodeInternal,
			"The upload could not be moved into place."))
		return
	}
	if err := os.WriteFile(
		filepath.Join(dir, names.Manifest), manifestBody, 0o640); err != nil {
		httpx.Error(w, r, errs.Wrap(err, errs.CodeInternal,
			"The manifest could not be written."))
		return
	}
	if err := os.WriteFile(filepath.Join(dir, names.Checksum),
		backup.ChecksumFile(manifest.Database.SHA256, names.Dump),
		0o640); err != nil {
		httpx.Error(w, r, errs.Wrap(err, errs.CodeInternal,
			"The checksum file could not be written."))
		return
	}
	committed = true

	who, _ := s.actorOf(r)
	record, err := s.backups.Uploaded(r.Context(), manifest.SnapshotID,
		dir, manifest.Database.SHA256, dumpBytes, manifestBody,
		manifest.Encryption != nil, who)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	s.auditBackup(r, "backup_uploaded", manifest.SnapshotID, map[string]any{
		"bytes": dumpBytes, "checksum": manifest.Database.SHA256,
		"schema_version": manifest.SchemaVersion,
		"taken_at":       manifest.TakenAt,
	})

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id":          record,
		"snapshot_id": manifest.SnapshotID,
		"phase":       backup.PhaseUploaded,
		"bytes":       dumpBytes,
		"sha256":      manifest.Database.SHA256,
		"encrypted":   manifest.Encryption != nil,
		"says": "The upload is intact and matches its manifest. NOTHING has " +
			"been proved about whether it restores. Verify it before you " +
			"rely on it.",
	})
}

// streamToFile writes a part to disk, hashing it, and refuses to exceed a cap.
func streamToFile(
	part *multipart.Part, path string, max int64,
) (int64, string, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, "", errs.Wrap(err, errs.CodeInternal,
			"The upload could not be written to disk.")
	}
	defer f.Close()

	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash),
		io.LimitReader(part, max+1))
	if err != nil {
		return 0, "", errs.Wrap(err, errs.CodeInvalidInput,
			"The upload did not arrive in full.")
	}
	if n > max {
		return 0, "", errs.Newf(errs.CodeInvalidInput,
			"That backup is larger than this route accepts (%d bytes). Put "+
				"the file on the server and use `rawsyst backup restore-file`; "+
				"deploy/server/RECOVERY.md has the sequence.", max)
	}
	return n, hex.EncodeToString(hash.Sum(nil)), nil
}

// looksLikeADump reads the first eight bytes and says what they are.
func looksLikeADump(path string, expectSealed bool) error {
	f, err := os.Open(path)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal, "The upload could not be read.")
	}
	defer f.Close()

	head := make([]byte, 8)
	if _, err := io.ReadFull(f, head); err != nil {
		return errs.New(errs.CodeInvalidInput,
			"That file is too short to be a backup.")
	}
	sealed := backup.IsSealed(head)
	plain := string(head[:5]) == "PGDMP"

	switch {
	case sealed && !expectSealed:
		return errs.New(errs.CodeInvalidInput,
			"That file is encrypted and its manifest does not say so.")
	case plain && expectSealed:
		return errs.New(errs.CodeInvalidInput,
			"The manifest says that backup is encrypted and the file is a "+
				"plain dump.")
	case !sealed && !plain:
		return errs.New(errs.CodeInvalidInput,
			"That file does not begin like a PostgreSQL custom-format dump or "+
				"an encrypted RawSyst backup. Whatever it is, it is not "+
				"something this product wrote.")
	}
	return nil
}

// --- maintenance ------------------------------------------------------------

func (s *Server) handlePlatformMaintenance(w http.ResponseWriter, r *http.Request) {
	// A read a screen polls. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupRead) {
		return
	}
	if s.maintenance == nil {
		httpx.Error(w, r, backupsNotWired())
		return
	}
	state, err := s.maintenance.Read(r.Context())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, state)
}

// handlePlatformSetMaintenance is the write freeze a migration needs.
//
// Turning it ON is what stops a sale being rung up after the final backup and
// lost when the old server goes away. It is a platform act, and platform
// operators are exempt from it — otherwise the only way to turn it off would be
// a database client, which is the wrong thing to need at that moment.
func (s *Server) handlePlatformSetMaintenance(
	w http.ResponseWriter, r *http.Request,
) {
	// Freezes or unfreezes writes for every business on this server. See platform_backup_limits.go.
	if !s.limitBackup(w, r, rateBackupDestructive) {
		return
	}
	if s.maintenance == nil {
		httpx.Error(w, r, backupsNotWired())
		return
	}
	var req struct {
		Active     bool   `json:"active"`
		Reason     string `json:"reason"`
		AllowReads *bool  `json:"allow_reads"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	who, label := s.actorOf(r)
	var state any
	var err error
	if req.Active {
		allowReads := true
		if req.AllowReads != nil {
			allowReads = *req.AllowReads
		}
		state, err = s.maintenance.Begin(
			r.Context(), strings.TrimSpace(req.Reason), allowReads, who, label)
	} else {
		state, err = s.maintenance.End(r.Context())
	}
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	action := "maintenance_ended"
	if req.Active {
		action = "maintenance_started"
	}
	s.auditBackup(r, action, "", map[string]any{"reason": req.Reason})
	httpx.JSON(w, http.StatusOK, state)
}

// --- shared -----------------------------------------------------------------

// snapshotParam reads and validates the snapshot id in the path.
//
// The one place a snapshot id crosses from a request into this subsystem. It
// becomes an object key and the name of a temporary database, so it is checked
// here against a character set rather than escaped later by whoever remembers.
func (s *Server) snapshotParam(r *http.Request) (string, error) {
	if s.backups == nil {
		return "", backupsNotWired()
	}
	id := chi.URLParam(r, "snapshotID")
	if !backup.ValidSnapshotID(id) {
		return "", errs.New(errs.CodeNotFound, "No such backup.")
	}
	return id, nil
}

func (s *Server) backupOptions() backup.Options {
	return backup.Options{Store: s.backupStore, Prefix: s.backupPrefix}
}

func backupsNotWired() error {
	return errs.New(errs.CodeUnavailable,
		"Backup and recovery is not wired into this installation.")
}

// actorOf is who is asking, for a record that has to survive them leaving.
//
// The label is denormalised for the same reason the audit trail's is: it has to
// outlive the user row, or the record of who ordered a production restore
// becomes a missing person.
func (s *Server) actorOf(r *http.Request) (*uuid.UUID, string) {
	a := actor.From(r.Context())
	if a.UserID == uuid.Nil {
		return nil, ""
	}
	id := a.UserID
	label := id.String()
	if s.audit != nil {
		if name := s.audit.LabelForUser(r.Context(), id); name != "" {
			label = name
		}
	}
	return &id, label
}

// auditBackup writes one entry into the trail the product already has.
//
// Never the artifact, never a credential, never a key: an id, a size, a
// checksum, and who did it. `After` carries only what a person investigating
// would need, and a reviewer can see from here that nothing else is in it.
func (s *Server) auditBackup(
	r *http.Request, action, snapshotID string, detail map[string]any,
) {
	if s.audit == nil {
		return
	}
	id, label := s.actorOf(r)
	after := map[string]any{}
	for k, v := range detail {
		after[k] = v
	}
	if snapshotID != "" {
		after["snapshot_id"] = snapshotID
	}
	after["at"] = time.Now().UTC().Format(time.RFC3339)

	s.audit.Platform(r.Context(), audit.Entry{
		ActorID:    id,
		ActorLabel: label,
		Action:     action,
		EntityType: "backup",
		IP:         clientIP(r),
		After:      after,
	})
}
