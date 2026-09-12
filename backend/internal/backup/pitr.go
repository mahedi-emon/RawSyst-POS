// Point-in-time recovery: a base backup, the segments after it, and a second
// to stop at.
//
// # What this does, in one paragraph
//
// Downloads a physical base backup, unseals it, unpacks it into an empty
// directory, writes the recovery settings PostgreSQL reads at startup, starts a
// SEPARATE PostgreSQL on a socket nobody else can reach, and lets it replay the
// archive until it arrives at the moment it was asked for. Then it connects to
// the result, counts what is in it, writes down what it found, and stops it.
//
// # What it never does
//
// Touch production. Not by default, not with a flag, not at all: this file has
// no path that writes to a live cluster. A production replacement is a separate
// decision made in `restore.go`, it takes the OUTPUT of this — a recovered
// cluster that has been inspected — and it is gated on a rehearsal, a fresh
// verified backup, a write freeze and the snapshot id typed out. The reason
// they are separate files is that they are separate risks, and the dangerous
// one should not be reachable by getting an argument wrong in the safe one.
//
// # Why an isolated postmaster and not a scratch database
//
// A `pg_dump` verification restores into a temporary DATABASE beside the live
// one, which is cheap and correct for a dump. It is impossible for a physical
// backup: the write-ahead log describes changes to pages of a cluster, replay
// is a property of a postmaster starting up, and there is no way to replay one
// cluster inside another. So this starts a real PostgreSQL of its own, on its
// own data directory, on its own socket — which is also what makes it safe,
// because that postmaster has no route to the production one.
//
// # The socket, and why there is usually no port
//
// On anything but Windows the recovered instance listens on a Unix socket
// inside its own staging directory and `listen_addresses` is empty, so it is
// not on the network at all — not on localhost, not on the compose bridge,
// nowhere. A recovered cluster holds every business's data at some earlier
// moment and is momentarily protected only by whatever `pg_hba.conf` production
// happened to have, so the right number of network interfaces for it to be
// reachable on is zero. Windows has no Unix sockets, so there it binds
// 127.0.0.1 on an ephemeral port with a `pg_hba.conf` that admits nothing else.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// The kinds of recovery target an operator may ask for.
const (
	// TargetLatest replays everything the archive has. The ordinary answer to
	// "the server is gone, get it back".
	TargetLatest = "latest"

	// TargetImmediate stops the moment the cluster is consistent, which is the
	// base backup and nothing after it. Used to check that a base backup is
	// sound without waiting for a week of replay.
	TargetImmediate = "immediate"

	// TargetTime stops at a moment, including a transaction that committed
	// exactly then.
	TargetTime = "time"

	// TargetBeforeTime stops just BEFORE a moment. This is the one somebody
	// recovering from a mistake wants: "the delete ran at 14:32" means the
	// wanted state is the one before 14:32, not including it.
	TargetBeforeTime = "before_time"

	// TargetLSN stops at an exact position in the log.
	TargetLSN = "lsn"

	// TargetName stops at a named restore point, made earlier with
	// `pg_create_restore_point`. The safest target there is, because it names
	// a moment somebody deliberately marked rather than one read off a clock
	// that may not agree with the server's.
	TargetName = "name"

	// TargetXID stops at a transaction id.
	TargetXID = "xid"
)

// RecoveryTarget is where a recovery is asked to stop.
type RecoveryTarget struct {
	Kind string `json:"kind"`

	// At is the moment, for `time` and `before_time`.
	At time.Time `json:"at,omitempty"`

	// Value is the position, name or transaction id, for the other kinds.
	Value string `json:"value,omitempty"`

	// Timeline is which history to follow. Zero means the base backup's own,
	// which is the safe default — see `recoveryConfig`.
	Timeline uint32 `json:"timeline,omitempty"`
}

// Describe says what a target means, in a sentence a person can check.
func (t RecoveryTarget) Describe() string {
	switch t.Kind {
	case TargetImmediate:
		return "the moment the base backup itself is consistent, replaying nothing after it"
	case TargetTime:
		return "the last transaction committed at or before " + t.At.UTC().Format(time.RFC3339)
	case TargetBeforeTime:
		return "the last transaction committed strictly before " + t.At.UTC().Format(time.RFC3339)
	case TargetLSN:
		return "log position " + t.Value
	case TargetName:
		return "the restore point named " + strconv.Quote(t.Value)
	case TargetXID:
		return "transaction " + t.Value
	default:
		return "the latest moment the archive can reach"
	}
}

// Validate refuses a target that cannot be written into a configuration file.
//
// # Why this is strict rather than escaped
//
// These values end up as lines in `postgresql.auto.conf`, which PostgreSQL
// parses as settings. A newline in a restore point name is not a broken name,
// it is an extra setting — and the settings available at that point in startup
// include `archive_command`, which is a shell command run as the database user.
// So nothing is escaped and everything is checked against a small alphabet:
// escaping is a rule somebody has to get right every time, and a whitelist is a
// rule that is right by construction.
func (t RecoveryTarget) Validate() error {
	switch t.Kind {
	case "", TargetLatest, TargetImmediate:
		return nil

	case TargetTime, TargetBeforeTime:
		if t.At.IsZero() {
			return errs.New(errs.CodeInvalidInput,
				"A recovery to a moment has to say which moment.")
		}
		// A target in the future is always a recovery that runs out of log and
		// stops short, and PostgreSQL reports that as a fatal error after the
		// replay rather than before it. Refused here, where it costs nothing.
		if t.At.After(time.Now().Add(time.Minute)) {
			return errs.Newf(errs.CodeInvalidInput,
				"%s is in the future. A recovery cannot be asked to stop at a "+
					"moment that has not happened; it would replay everything "+
					"there is and then fail for not having arrived.",
				t.At.UTC().Format(time.RFC3339))
		}
		return nil

	case TargetLSN:
		if _, ok := ParseLSN(t.Value); !ok {
			return errs.Newf(errs.CodeInvalidInput,
				"%q is not a log position. They look like `1A/B2C30000`.",
				clipText(t.Value, 64))
		}
		return nil

	case TargetName:
		if !validRestorePointName(t.Value) {
			return errs.New(errs.CodeInvalidInput,
				"A restore point name may be up to 63 letters, digits, dots, "+
					"dashes and underscores. Anything else is refused: this "+
					"value is written into a PostgreSQL configuration file, "+
					"and a name with a newline in it is not a name, it is an "+
					"extra setting.")
		}
		return nil

	case TargetXID:
		if t.Value == "" || len(t.Value) > 20 {
			return errs.New(errs.CodeInvalidInput,
				"That is not a transaction id.")
		}
		for _, r := range t.Value {
			if r < '0' || r > '9' {
				return errs.New(errs.CodeInvalidInput,
					"A transaction id is digits.")
			}
		}
		return nil

	default:
		return errs.Newf(errs.CodeInvalidInput,
			"There is no recovery target called %q. The kinds are latest, "+
				"immediate, time, before_time, lsn, name and xid.",
			clipText(t.Kind, 32))
	}
}

// WantsMoment reports whether this target is a point on the clock.
func (t RecoveryTarget) WantsMoment() bool {
	return t.Kind == TargetTime || t.Kind == TargetBeforeTime
}

func validRestorePointName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// --- taking a cluster back to a moment --------------------------------------

// PITROptions is what a recovery needs.
type PITROptions struct {
	// WAL is the store, the prefix and the key: the archive and the base
	// backups are one recovery source and are configured as one thing.
	WAL WALOptions

	// BaseBackupID names which base backup to start from. Empty means the
	// newest one that finished before the target, which is almost always right
	// — see `BaseBackupFor`.
	BaseBackupID string

	Target RecoveryTarget

	// WorkDir is where the recovered cluster is assembled. It needs room for
	// an uncompressed copy of the cluster.
	WorkDir string

	// Keep leaves the recovered cluster on disk and running when the recovery
	// finishes, so somebody can connect to it and look. Off by default: a
	// running PostgreSQL holding every business at an earlier moment is not
	// something to leave behind by accident.
	Keep bool

	// RestoreCommand is what the recovered instance runs to fetch each segment.
	// Empty means this binary, in `wal restore` mode, which is what a
	// deployment wants; a test supplies its own.
	RestoreCommand string

	// BinDir holds `pg_ctl`, `postgres` and `pg_verifybackup`. Empty means the
	// PATH, which inside the backup image is the PostgreSQL of the right major
	// version by construction.
	BinDir string

	// AppDatabase is the database to inspect once recovery finishes. Empty
	// means whichever one the base backup recorded.
	AppDatabase string

	// SuperUser is the role to connect to the recovered cluster as. It has to
	// exist IN THE BACKUP, because the recovered cluster has the roles
	// production had. Empty means `postgres`.
	SuperUser string

	// ExpectProduct says the recovered database should be this product's, so a
	// missing migration ledger is a finding rather than an observation.
	//
	// True everywhere a deployment recovers its own cluster, which is every
	// path an operator can reach. False only for the drill, which builds a
	// cluster from `initdb` to prove the MECHANISM works and has no business
	// asserting anything about its contents — a recovery engine that could
	// only recover this product would be one nobody could test.
	ExpectProduct bool

	Timeout  time.Duration
	Progress func(stage string)
	Log      *slog.Logger
}

func (o PITROptions) withDefaults() PITROptions {
	o.WAL = o.WAL.withDefaults()
	if o.Timeout <= 0 {
		o.Timeout = 4 * time.Hour
	}
	if o.Progress == nil {
		o.Progress = func(string) {}
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.SuperUser == "" {
		o.SuperUser = "postgres"
	}
	return o
}

// PITRReport is what a recovery found, and the document somebody reads before
// deciding to act on it.
type PITRReport struct {
	Passed bool `json:"passed"`

	BaseBackupID   string          `json:"base_backup_id"`
	BaseBackupAt   string          `json:"base_backup_completed_at,omitempty"`
	Target         RecoveryTarget  `json:"target"`
	TargetMeaning  string          `json:"target_meaning"`
	RecoveryWindow *RecoveryWindow `json:"recovery_window,omitempty"`

	// Where replay actually stopped. The important pair: a recovery that was
	// asked for 14:32 and stopped at 14:29 because that was the last committed
	// transaction is a SUCCESS, and one that stopped at 09:00 because the
	// archive had a hole is not — and the only way to tell them apart is to
	// print both.
	ReachedLSN  string `json:"reached_lsn,omitempty"`
	ReachedTime string `json:"last_transaction_replayed_at,omitempty"`
	Timeline    uint32 `json:"recovered_timeline,omitempty"`

	SegmentsReplayed int `json:"segments_replayed,omitempty"`

	// What is in the result.
	Inventory *Inventory `json:"inventory,omitempty"`

	Checked  []string `json:"checked,omitempty"`
	Findings []string `json:"findings,omitempty"`

	DownloadSeconds int `json:"download_seconds"`
	ExtractSeconds  int `json:"extract_seconds"`
	ReplaySeconds   int `json:"replay_seconds"`
	TotalSeconds    int `json:"total_seconds"`

	Bytes int64 `json:"base_backup_bytes,omitempty"`

	// DataDir is set only when `Keep` was asked for. Otherwise the cluster is
	// gone by the time anybody reads this, which is the point.
	DataDir string `json:"data_directory,omitempty"`
	DSN     string `json:"connection,omitempty"`
}

func (r *PITRReport) finding(format string, args ...any) {
	r.Findings = append(r.Findings, fmt.Sprintf(format, args...))
}

func (r *PITRReport) checked(what string) {
	r.Checked = append(r.Checked, what)
}

// RunPITR recovers a cluster to a moment, in isolation, and reports what it
// found there.
func RunPITR(ctx context.Context, opts PITROptions) (PITRReport, error) {
	opts = opts.withDefaults()
	began := time.Now()

	report := PITRReport{Target: opts.Target}
	if err := opts.Target.Validate(); err != nil {
		return report, err
	}
	report.TargetMeaning = opts.Target.Describe()

	if !opts.WAL.Configured() {
		return report, errs.New(errs.CodeUnavailable,
			"No object store is configured, so there is nothing to recover "+
				"from.")
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	opts.Progress(StagePreparing)

	// The window first, before a byte is downloaded. A target outside it is a
	// no, and it costs one listing to say so instead of an hour.
	window, err := ComputeRecoveryWindow(ctx, opts.WAL)
	if err != nil {
		return report, err
	}
	report.RecoveryWindow = &window
	if opts.Target.WantsMoment() {
		if err := ExplainTarget(window, opts.Target.At); err != nil {
			return report, err
		}
	} else if !window.Available {
		return report, errs.New(errs.CodeInvalidInput, window.Because)
	}

	base, err := chooseBaseBackup(ctx, opts, window)
	if err != nil {
		return report, err
	}
	report.BaseBackupID = base.ID
	report.BaseBackupAt = base.CompletedAt
	report.Bytes = base.TotalBytes()

	// Who to connect to the result as.
	//
	// The recovered cluster has the roles the SOURCE had, not the ones this
	// machine has. Defaulting to `postgres` is wrong the moment anybody runs
	// the official image with POSTGRES_USER set to something else — which this
	// product's own compose file does, so it was wrong here. The backup
	// records the bootstrap superuser; use it.
	//
	// Found by the drill in deploy/server/pitr-drill.sh, where it presented as
	// a recovery that replayed correctly and then sat for ten minutes failing
	// to connect.
	if opts.SuperUser == "" || opts.SuperUser == "postgres" {
		if base.BootstrapRole != "" {
			opts.SuperUser = base.BootstrapRole
		}
	}
	if opts.SuperUser == "" {
		opts.SuperUser = "postgres"
	}

	if err := checkSameMajorVersion(opts, base); err != nil {
		return report, err
	}

	dir := filepath.Join(stagingRoot(opts.WorkDir), "pitr-"+base.ID+"-"+
		strconv.FormatInt(time.Now().UnixNano(), 36))
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return report, errs.Wrap(err, errs.CodeInternal,
			"A directory for the recovered cluster could not be created.")
	}
	// `Keep` means "leave the recovered cluster for me to look at", and that is
	// only ever a request about a recovery that WORKED. A failed one that was
	// left running would be a postmaster holding every business at an earlier
	// moment on the staging volume, kept alive by an operator asking to inspect
	// a result that does not exist. So `kept` is set at the very end and
	// nothing before it survives.
	kept := false
	defer func() {
		if !kept {
			_ = os.RemoveAll(dir)
		}
	}()

	// Fetch and unpack. Streamed straight from the store through the decrypter
	// and the decompressor into the tar reader: a base backup is the size of
	// the database and nothing here holds one, or even a compressed one.
	opts.Progress(StageFetching)
	downloadBegan := time.Now()
	if err := unpackBaseBackup(ctx, opts, base, dataDir); err != nil {
		return report, err
	}
	report.DownloadSeconds = int(time.Since(downloadBegan).Seconds())
	report.ExtractSeconds = report.DownloadSeconds

	// The recovery settings, then the signal file. In that order: the signal
	// is what turns a data directory into a recovery, so a crash between the
	// two leaves a cluster that will not start rather than one that starts
	// without a target and replays past it.
	opts.Progress(StagePreparing)
	inst, err := prepareInstance(opts, base, dir, dataDir)
	if err != nil {
		return report, err
	}

	opts.Progress(StageRecovering)
	replayBegan := time.Now()
	sample, err := inst.startAndRecover(ctx, opts)
	report.ReplaySeconds = int(time.Since(replayBegan).Seconds())
	if err != nil {
		return report, err
	}
	report.ReachedLSN = sample.lsn
	if !sample.at.IsZero() {
		report.ReachedTime = sample.at.UTC().Format(time.RFC3339)
	}
	report.Timeline = sample.timeline
	report.SegmentsReplayed = sample.segments

	defer func() {
		if kept {
			return
		}
		if err := inst.stop(); err != nil {
			opts.Log.Warn("the recovered instance would not stop",
				slog.String("error", err.Error()))
		}
	}()

	opts.Progress(StageChecking)
	if err := inspectRecovered(ctx, opts, base, inst, &report); err != nil {
		return report, err
	}

	if opts.Target.WantsMoment() && !sample.at.IsZero() {
		if sample.at.After(opts.Target.At.Add(time.Second)) {
			report.finding(
				"Replay stopped at %s, which is AFTER the requested %s. The "+
					"recovery overshot its target and the result must not be "+
					"used.",
				sample.at.UTC().Format(time.RFC3339),
				opts.Target.At.UTC().Format(time.RFC3339))
		} else {
			report.checked(fmt.Sprintf(
				"replay stopped at %s, at or before the requested %s",
				sample.at.UTC().Format(time.RFC3339),
				opts.Target.At.UTC().Format(time.RFC3339)))
		}
	}

	report.Passed = len(report.Findings) == 0
	report.TotalSeconds = int(time.Since(began).Seconds())
	if opts.Keep {
		kept = true
		report.DataDir = dataDir
		report.DSN = inst.dsn(opts, "postgres")
	}
	opts.Progress(StageDone)
	return report, nil
}

func chooseBaseBackup(
	ctx context.Context, opts PITROptions, window RecoveryWindow,
) (BaseManifest, error) {
	if opts.BaseBackupID != "" {
		return ReadBaseManifest(ctx, opts.WAL, opts.BaseBackupID)
	}
	bases, err := CompletedBaseBackups(ctx, opts.WAL)
	if err != nil {
		return BaseManifest{}, err
	}
	if len(bases) == 0 {
		return BaseManifest{}, errs.New(errs.CodeInvalidInput,
			"There is no completed base backup to recover from.")
	}
	if opts.Target.WantsMoment() {
		return BaseBackupFor(bases, opts.Target.At)
	}
	// Newest first.
	return bases[0], nil
}

// checkSameMajorVersion refuses a recovery this build cannot perform.
//
// A physical backup is only readable by the major version that wrote it. The
// error PostgreSQL gives otherwise is `database files are incompatible with
// server`, at the end of a download, and it does not say which two versions.
func checkSameMajorVersion(opts PITROptions, base BaseManifest) error {
	have, err := postgresMajor(opts.BinDir)
	if err != nil {
		return err
	}
	want := base.PostgresVersionNum / 10000
	if want == 0 || have == 0 || want == have {
		return nil
	}
	return errs.Newf(errs.CodeInvalidInput,
		"That base backup was taken by PostgreSQL %d and the tools here are "+
			"PostgreSQL %d. A physical backup is a byte-level copy of a data "+
			"directory and can only be read by its own major version; it does "+
			"not upgrade. Recover it with a PostgreSQL %d, then upgrade the "+
			"result.", want, have, want)
}

func postgresMajor(binDir string) (int, error) {
	out, err := exec.Command(toolPath(binDir, "postgres"), "--version").Output()
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeUnavailable,
			"The PostgreSQL server binary could not be run. A point-in-time "+
				"recovery starts a PostgreSQL of its own, so this has to be "+
				"somewhere it can be found.")
	}
	// `postgres (PostgreSQL) 17.4`
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, nil
	}
	version := fields[len(fields)-1]
	major, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(strings.TrimSpace(major))
	if err != nil {
		return 0, nil
	}
	return n, nil
}

func toolPath(binDir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if strings.TrimSpace(binDir) == "" {
		return name
	}
	return filepath.Join(binDir, name)
}

// --- getting the bytes onto disk --------------------------------------------

// unpackBaseBackup streams the two tarballs out of the store into a data
// directory.
//
// Nothing is staged: the object is read, decrypted, decompressed and untarred
// in one pass. A base backup that had to be written to disk twice would need
// twice the room on a volume sized for the cluster once.
func unpackBaseBackup(
	ctx context.Context, opts PITROptions, base BaseManifest, dataDir string,
) error {
	main, ok := base.Component(baseTarObject)
	if !ok {
		return errs.Newf(errs.CodeInternal,
			"Base backup %s does not say where its cluster copy is. Its "+
				"manifest is incomplete and it cannot be recovered from.",
			clipText(base.ID, 64))
	}
	wal, hasWAL := base.Component(baseWALTarObject)
	if !hasWAL {
		return errs.Newf(errs.CodeInternal,
			"Base backup %s has no pg_wal.tar.gz, so it does not carry the "+
				"write-ahead log written while it was taken and cannot reach "+
				"its own consistency point. It is not a recovery source.",
			clipText(base.ID, 64))
	}

	if err := streamComponent(ctx, opts, base, main, dataDir); err != nil {
		return err
	}
	return streamComponent(ctx, opts, base, wal, filepath.Join(dataDir, "pg_wal"))
}

func streamComponent(
	ctx context.Context, opts PITROptions,
	base BaseManifest, c Component, dest string,
) error {
	key, err := BaseBackupKey(opts.WAL.Prefix, base.ID, c.Key)
	if err != nil {
		return err
	}
	body, size, err := opts.WAL.Store.GetStream(ctx, key)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return errs.Newf(errs.CodeNotFound,
				"Base backup %s is missing %s. Its manifest lists it and the "+
					"store does not have it, so the backup is incomplete and "+
					"cannot be recovered from.",
				clipText(base.ID, 64), c.Key)
		}
		return err
	}
	defer body.Close()

	if size > 0 && c.Bytes > 0 && size != c.Bytes {
		return errs.Newf(errs.CodeInternal,
			"%s of base backup %s is %d bytes and its manifest says %d. It "+
				"has been truncated or replaced.",
			c.Key, clipText(base.ID, 64), size, c.Bytes)
	}

	// Hashed as it goes past, so a corrupted object is found by the time the
	// stream ends rather than by whatever it produced.
	hash := sha256.New()
	reader := io.Reader(io.TeeReader(body, hash))

	if base.Encryption != nil {
		if !opts.WAL.Key.Set() {
			return errs.Newf(errs.CodeInvalidInput,
				"Base backup %s is encrypted and no key is configured. Set "+
					"RAWSYST_BACKUP_ENCRYPTION_KEY to the key it was sealed "+
					"with (fingerprint %s).",
				clipText(base.ID, 64), base.Encryption.KeyFingerprint)
		}
		if base.Encryption.KeyFingerprint != "" &&
			base.Encryption.KeyFingerprint != opts.WAL.Key.Fingerprint() {
			return errs.Newf(errs.CodeInvalidInput,
				"Base backup %s was sealed with key %s and the configured key "+
					"is %s. This is the wrong key, not a corrupt backup.",
				clipText(base.ID, 64), base.Encryption.KeyFingerprint,
				opts.WAL.Key.Fingerprint())
		}
		opened, err := openStream(opts.WAL.Key, reader)
		if err != nil {
			return err
		}
		defer opened.Close()
		reader = opened
	}

	if err := extractTarGz(reader, dest); err != nil {
		return err
	}

	// Drain whatever the tar reader did not, so the checksum covers the whole
	// object rather than however much the archive happened to need.
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return errs.Wrap(err, errs.CodeInternal, fmt.Sprintf(
			"%s of the base backup could not be read to the end.", c.Key))
	}
	if c.SHA256 != "" {
		if sum := hex.EncodeToString(hash.Sum(nil)); sum != c.SHA256 {
			return errs.Newf(errs.CodeInternal,
				"%s of base backup %s does not match the checksum in its "+
					"manifest. The object in the store has been corrupted; "+
					"the recovery must not be trusted and has been stopped.",
				c.Key, clipText(base.ID, 64))
		}
	}
	return nil
}

// openStream decrypts a sealed stream lazily, through a pipe.
//
// `Key.Open` writes to a writer and this needs a reader, and the thing in
// between must not be a buffer: the plaintext here is the whole cluster.
func openStream(key Key, r io.Reader) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		_, err := key.Open(pw, r)
		pw.CloseWithError(err)
	}()
	return pr, nil
}

// extractTarGz unpacks one of pg_basebackup's tarballs.
//
// # Why this is written out rather than shelling to tar
//
// Because every entry in the archive is a path, and the archive was read from
// an object store. `tar` would happily write `../../etc/cron.d/anything` if an
// archive said so, and the archive is exactly the thing this product cannot
// assume is unmodified — it is the one part of the system that lives on
// somebody else's computer. Every name is resolved and checked against the
// destination here, and a link that points outside it stops the recovery.
func extractTarGz(r io.Reader, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"A directory for the recovered cluster could not be created.")
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The recovery directory could not be resolved.")
	}

	zr, err := gzip.NewReader(r)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The base backup is not readable as a gzip archive. If it is "+
				"encrypted, the key is wrong; otherwise the object is corrupt.")
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	for {
		head, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errs.Wrap(err, errs.CodeInternal,
				"The base backup archive ended unexpectedly. It is truncated, "+
					"which means the upload did not finish and it is not a "+
					"recovery source.")
		}
		target, err := safeJoin(root, head.Name)
		if err != nil {
			return err
		}
		switch head.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return errs.Wrap(err, errs.CodeInternal,
					"A directory in the base backup could not be created.")
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return errs.Wrap(err, errs.CodeInternal,
					"A directory in the base backup could not be created.")
			}
			mode := os.FileMode(head.Mode).Perm()
			if mode == 0 {
				mode = 0o600
			}
			f, err := os.OpenFile(target,
				os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return errs.Wrap(err, errs.CodeInternal,
					"A file in the base backup could not be written.")
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return errs.Wrap(err, errs.CodeInternal,
					"A file in the base backup could not be written.")
			}
			if err := f.Close(); err != nil {
				return errs.Wrap(err, errs.CodeInternal,
					"A file in the base backup could not be closed.")
			}
		case tar.TypeSymlink, tar.TypeLink:
			// The only symlinks pg_basebackup writes are tablespace links, and
			// a tablespace restored to the path it had on the source server
			// would write outside this directory — possibly over the live
			// cluster. This product creates no tablespaces, so finding one is
			// a cluster this code was not written for, and saying so is better
			// than following the link.
			return errs.Newf(errs.CodeInvalidInput,
				"The base backup contains a link (%s). That means the cluster "+
					"has tablespaces outside its data directory, which this "+
					"recovery does not handle: restoring them needs an "+
					"explicit mapping, and following the link would write "+
					"wherever the source server pointed it.",
				clipText(head.Name, 128))
		default:
			// Devices, fifos and the rest have no business in a base backup.
			continue
		}
	}
}

// safeJoin resolves an archive entry inside the destination, or refuses.
func safeJoin(root, name string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(name, "./")))
	if cleaned == "." {
		return root, nil
	}
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", errs.Newf(errs.CodeInvalidInput,
			"The base backup contains an entry named %q, which would be "+
				"written outside the recovery directory. Refusing to unpack "+
				"it: the archive is not what pg_basebackup produced.",
			clipText(name, 128))
	}
	target := filepath.Join(root, cleaned)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errs.Newf(errs.CodeInvalidInput,
			"The base backup contains an entry named %q, which would be "+
				"written outside the recovery directory.", clipText(name, 128))
	}
	return target, nil
}
