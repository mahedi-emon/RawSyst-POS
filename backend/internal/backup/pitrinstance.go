// The throwaway PostgreSQL a recovery runs inside.
//
// # Why this exists at all
//
// Replaying the write-ahead log is something a postmaster does while it starts
// up. There is no library call for it and no way to do it inside another
// cluster, so a point-in-time recovery means starting a real PostgreSQL on the
// recovered data directory and letting it do the work. This file is the
// plumbing for that: where it listens, what it is allowed to do, how progress
// is watched, and how it is stopped afterwards.
//
// # What the recovered instance is not allowed to do
//
// It holds every business on the platform at some earlier moment, so the
// settings written here are as much about containment as about recovery:
//
//	archive_mode = off        it must not write into the archive it is reading
//	listen_addresses = ''     no network interface at all, where the platform
//	                          has Unix sockets
//	pg_hba.conf replaced      nothing but the local socket is admitted
//
// `archive_mode = off` is the one that would hurt most if it were forgotten.
// The data directory came from a server that had archiving on, so without this
// the recovered instance would begin archiving its own replayed WAL into the
// production archive under the SAME segment names, and the first thing anybody
// would know about it is a recovery six months later that stopped in the middle
// of a Tuesday.
//
// # fsync is off, deliberately
//
// This cluster exists for minutes and is deleted afterwards. Durability across
// a power cut buys nothing here and costs a large multiple on replay time,
// which is the number an operator is waiting on during an outage. If the
// machine dies mid-recovery the answer is to run the recovery again.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// instance is one recovered cluster.
type instance struct {
	dir     string // the staging root, holding data/ and the log
	dataDir string
	socket  string // the Unix socket directory, empty on Windows
	port    int    // only used where there are no Unix sockets
	binDir  string
	logPath string

	// superUser is the role to connect as. It has to be a role that exists in
	// the BACKUP: this cluster has production's roles, not this machine's.
	superUser string

	started bool
}

// prepareInstance writes everything PostgreSQL reads before it starts.
func prepareInstance(
	opts PITROptions, base BaseManifest, dir, dataDir string,
) (*instance, error) {
	inst := &instance{
		dir:       dir,
		dataDir:   dataDir,
		binDir:    opts.BinDir,
		logPath:   filepath.Join(dir, "recovery.log"),
		superUser: opts.SuperUser,
	}

	// A stale postmaster.pid is how a recovered cluster refuses to start with a
	// message about another server already running on a machine where nothing
	// is. pg_basebackup does not copy one, but a backup taken by other means
	// might, and removing it costs nothing.
	_ = os.Remove(filepath.Join(dataDir, "postmaster.pid"))

	// standby.signal and recovery.signal mean different things and the first
	// wins. A data directory that arrived with one would put this cluster into
	// standby mode, where it waits for a primary that does not exist instead of
	// recovering.
	_ = os.Remove(filepath.Join(dataDir, "standby.signal"))

	if runtime.GOOS == "windows" {
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		inst.port = port
	} else {
		inst.socket = filepath.Join(dir, "sock")
		if err := os.MkdirAll(inst.socket, 0o700); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternal,
				"A socket directory for the recovered cluster could not be "+
					"created.")
		}
	}

	if err := inst.writeHBA(); err != nil {
		return nil, err
	}
	if err := inst.writeRecoveryConfig(opts, base); err != nil {
		return nil, err
	}

	// The signal file LAST. It is what turns a data directory into a recovery,
	// so writing it after the settings means a failure in between leaves a
	// cluster that will not start rather than one that starts with no target
	// and replays straight past the moment somebody wanted.
	signal := filepath.Join(dataDir, "recovery.signal")
	if err := os.WriteFile(signal, []byte(""), 0o600); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternal,
			"The recovery signal file could not be written.")
	}
	return inst, nil
}

// writeHBA replaces production's host-based authentication with one that admits
// only this machine, on the socket this instance owns.
//
// The original is kept beside it. It is a document about how production was
// configured and somebody reading a recovery may want it; it is simply not the
// file this instance should be answering to.
func (i *instance) writeHBA() error {
	path := filepath.Join(i.dataDir, "pg_hba.conf")
	if body, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".from-backup", body, 0o600)
	}

	// `trust` on a socket inside a 0700 directory owned by this process, with
	// no network listener at all, admits exactly the user this process already
	// runs as. Requiring a password instead would mean holding production's
	// password to read a recovery of production, which is a worse arrangement
	// and not a safer one.
	lines := []string{
		"# Written by RawSyst for one point-in-time recovery. Not production's.",
		"local   all   all                 trust",
	}
	if i.port != 0 {
		lines = append(lines,
			"host    all   all   127.0.0.1/32  trust",
			"host    all   all   ::1/128       trust")
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The recovered cluster's authentication file could not be written.")
	}
	return nil
}

// writeRecoveryConfig writes the settings the recovery is driven by.
//
// Into `postgresql.auto.conf`, which PostgreSQL reads LAST, so nothing in the
// backup's own `postgresql.conf` can override any of it. The file that came out
// of the backup is kept beside it under another name rather than merged:
// production's ALTER SYSTEM settings are about production, and a recovered
// cluster inheriting, say, an `archive_command` is the specific accident this
// whole file is arranged to prevent.
func (i *instance) writeRecoveryConfig(
	opts PITROptions, base BaseManifest,
) error {
	path := filepath.Join(i.dataDir, "postgresql.auto.conf")
	if body, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".from-backup", body, 0o600)
	}

	restore := opts.RestoreCommand
	if restore == "" {
		var err error
		restore, err = defaultRestoreCommand()
		if err != nil {
			return err
		}
	}

	lines := []string{
		"# Written by RawSyst for one point-in-time recovery.",
		"# The file this replaced is beside it as postgresql.auto.conf.from-backup.",
		"",
		"# Containment. This cluster must not touch the archive it is reading,",
		"# and must not be reachable by anything but the process that started it.",
		"archive_mode = off",
		"archive_command = ''",
	}
	if i.socket != "" {
		lines = append(lines,
			"listen_addresses = ''",
			"unix_socket_directories = "+quoteConf(i.socket),
			"unix_socket_permissions = 0700")
	} else {
		lines = append(lines,
			"listen_addresses = '127.0.0.1'",
			"port = "+strconv.Itoa(i.port))
	}

	lines = append(lines,
		"",
		"# Recovery.",
		"restore_command = "+quoteConf(restore),
		"recovery_target_action = 'promote'",

		// Connections during replay, so progress can be watched rather than
		// guessed at. Without this the only sign of life for an hour is a
		// process that has not exited.
		"hot_standby = on",
	)

	// Which history to follow. The base backup's own timeline, always, unless
	// somebody asks otherwise.
	//
	// PostgreSQL's default here is `latest`, and `latest` is wrong for this:
	// after any previous recovery the archive contains a newer timeline, and a
	// recovery aimed at last Tuesday would follow the branch created by the
	// LAST recovery and arrive somewhere nobody asked for. Naming the timeline
	// makes the result a function of the request.
	timeline := opts.Target.Timeline
	if timeline == 0 {
		timeline = base.Timeline
	}
	if timeline == 0 {
		lines = append(lines, "recovery_target_timeline = 'current'")
	} else {
		lines = append(lines,
			fmt.Sprintf("recovery_target_timeline = '%d'", timeline))
	}

	switch opts.Target.Kind {
	case TargetImmediate:
		lines = append(lines, "recovery_target = 'immediate'")
	case TargetTime:
		lines = append(lines,
			"recovery_target_time = "+quoteConf(pgTimestamp(opts.Target.At)),
			"recovery_target_inclusive = on")
	case TargetBeforeTime:
		lines = append(lines,
			"recovery_target_time = "+quoteConf(pgTimestamp(opts.Target.At)),
			"recovery_target_inclusive = off")
	case TargetLSN:
		lsn, _ := ParseLSN(opts.Target.Value)
		lines = append(lines,
			"recovery_target_lsn = "+quoteConf(FormatLSN(lsn)),
			"recovery_target_inclusive = on")
	case TargetName:
		lines = append(lines,
			"recovery_target_name = "+quoteConf(opts.Target.Value))
	case TargetXID:
		lines = append(lines,
			"recovery_target_xid = "+quoteConf(opts.Target.Value),
			"recovery_target_inclusive = on")
	}

	lines = append(lines,
		"",
		"# A cluster that exists for minutes and is then deleted. Durability",
		"# across a power cut buys nothing and costs a multiple on replay time,",
		"# which is the number somebody is waiting on during an outage.",
		"fsync = off",
		"full_page_writes = off",
		"synchronous_commit = off",
		"",
		"shared_buffers = 128MB",
		"maintenance_work_mem = 64MB",
		"log_min_messages = 'info'",
		"logging_collector = off",
	)

	// The five settings that must not be SMALLER than the server that wrote
	// the log.
	//
	// # Why these are read rather than chosen
	//
	// With `hot_standby = on`, PostgreSQL refuses to replay a log written by a
	// server that allowed more connections, workers, senders, prepared
	// transactions or locks than this one does — because a replayed record
	// could reference a slot this cluster does not have. It refuses with
	//
	//     FATAL: recovery aborted because of insufficient parameter settings
	//
	// which does not say which parameter, or what it should be, or that the
	// number came from a control file rather than from anything an operator
	// configured.
	//
	// This file used to set `max_connections = 20`, reasoning that a throwaway
	// instance needs few. That is true and it is the wrong axis: the number is
	// not about how many clients will connect, it is about how large the
	// primary's slot arrays were. Picking a generous constant instead would
	// work until somebody ran a server more generous still.
	//
	// So the values are read out of the restored control file with
	// `pg_controldata`, which records exactly what the primary had, and used
	// as they are. Found by the drill in deploy/server/pitr-drill.sh.
	if req, err := readControlRequirements(i.binDir, i.dataDir); err == nil {
		lines = append(lines,
			"",
			"# Read from the restored control file: these are what the server",
			"# that wrote this log was running with, and replay refuses",
			"# anything smaller.",
			fmt.Sprintf("max_connections = %d", req.connections),
			fmt.Sprintf("max_worker_processes = %d", req.workers),
			fmt.Sprintf("max_wal_senders = %d", req.senders),
			fmt.Sprintf("max_prepared_transactions = %d", req.preparedXacts),
			fmt.Sprintf("max_locks_per_transaction = %d", req.locksPerXact),
		)
	}

	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return errs.Wrap(err, errs.CodeInternal,
			"The recovery settings could not be written.")
	}
	return nil
}

// controlRequirements are the five settings a recovery may not go below.
type controlRequirements struct {
	connections   int
	workers       int
	senders       int
	preparedXacts int
	locksPerXact  int
}

// readControlRequirements asks the restored control file what the server that
// wrote the log was running with.
//
// `pg_controldata` prints, among fifty other lines:
//
//	max_connections setting:              100
//	max_worker_processes setting:         8
//	max_wal_senders setting:              10
//	max_prepared_xacts setting:           0
//	max_locks_per_xact setting:           64
//
// Those five are the answer. Anything this file cannot parse is left out and
// the setting is simply not written, which falls back to whatever the restored
// `postgresql.conf` says — the primary's own file value, which is usually
// right and is never wildly wrong.
func readControlRequirements(
	binDir, dataDir string,
) (controlRequirements, error) {
	out, err := exec.Command(
		toolPath(binDir, "pg_controldata"), "-D", dataDir).Output()
	if err != nil {
		return controlRequirements{}, errs.Wrap(err, errs.CodeInternal,
			"The recovered cluster's control file could not be read.")
	}

	var req controlRequirements
	fields := map[string]*int{
		"max_connections setting":      &req.connections,
		"max_worker_processes setting": &req.workers,
		"max_wal_senders setting":      &req.senders,
		"max_prepared_xacts setting":   &req.preparedXacts,
		"max_locks_per_xact setting":   &req.locksPerXact,
	}
	for _, line := range strings.Split(string(out), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		target, wanted := fields[strings.TrimSpace(name)]
		if !wanted {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			*target = n
		}
	}
	if req.connections == 0 {
		return controlRequirements{}, errs.New(errs.CodeInternal,
			"The control file did not say how many connections the server "+
				"that wrote this log allowed.")
	}
	return req, nil
}

// pgTimestamp renders a moment the way `recovery_target_time` wants it.
//
// With an explicit UTC offset, always. Without one PostgreSQL reads the value
// in the SERVER's `TimeZone`, which for a Riyadh shop is three hours away from
// what an operator typed — three hours of trading either recovered or lost,
// silently, with the recovery reporting success either way.
func pgTimestamp(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04:05.999999-07:00")
}

// quoteConf renders a value as a PostgreSQL configuration string.
//
// Single quotes doubled, which is the escaping PostgreSQL's own parser expects.
// Every value that reaches here has already been through
// `RecoveryTarget.Validate`, which admits no quotes and no newlines at all;
// this is the second of the two, kept because the first one being removed by
// accident should not be a configuration-injection bug.
func quoteConf(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// defaultRestoreCommand is this binary, asked to fetch one segment.
//
// `os.Executable` rather than a configured path: the recovered instance runs as
// a child of this process on this machine, so the binary that knows how to read
// this archive is by definition the one already running.
func defaultRestoreCommand() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInternal,
			"This program could not work out where it is on disk, so it "+
				"cannot tell the recovered cluster how to fetch a segment.")
	}
	return fmt.Sprintf("%q backup wal restore %%f %%p", self), nil
}

// freePort asks the kernel for one and gives it straight back.
//
// Racy in principle: something else could take it in between. In practice this
// runs once per recovery on a machine that is not handing out ports quickly,
// and a collision is a start-up failure with a clear message rather than
// anything silent.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternal,
			"No free port could be found for the recovered cluster.")
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// dsn is how to connect to this instance.
func (i *instance) dsn(opts PITROptions, database string) string {
	if database == "" {
		database = "postgres"
	}
	if i.socket != "" {
		return fmt.Sprintf("postgres://%s@/%s?host=%s",
			i.superUser, database, i.socket)
	}
	return fmt.Sprintf("postgres://%s@127.0.0.1:%d/%s?sslmode=disable",
		i.superUser, i.port, database)
}

// --- starting it and watching it ---------------------------------------------

// replaySample is where replay had got to the last time anybody looked.
type replaySample struct {
	lsn      string
	at       time.Time
	timeline uint32
	segments int
}

// startAndRecover starts the instance and waits for replay to finish.
//
// Returns the last position seen WHILE it was still recovering. That is the
// only moment those numbers exist: once PostgreSQL promotes,
// `pg_last_wal_replay_lsn()` and `pg_last_xact_replay_timestamp()` go null, and
// a recovery that reported nothing about where it stopped would be a recovery
// nobody could check.
func (i *instance) startAndRecover(
	ctx context.Context, opts PITROptions,
) (replaySample, error) {
	var sample replaySample

	// -w waits for the server to accept connections, which during a recovery
	// means waiting for the consistency point. A target BEFORE that point is
	// refused by PostgreSQL at startup, so a failure here is usually the most
	// informative one available and its log is worth reading out.
	args := []string{
		"-D", i.dataDir,
		"-l", i.logPath,
		"-w",
		"-t", strconv.Itoa(int(startupWait(opts).Seconds())),
		"start",
	}
	cmd := exec.CommandContext(ctx, toolPath(i.binDir, "pg_ctl"), args...)
	cmd.Env = append(os.Environ(), "PGAPPNAME=rawsyst-pitr")

	out, err := runDetaching(cmd, filepath.Join(i.dir, "pg_ctl-start.out"))
	if err != nil {
		return sample, i.startupFailure(err, out)
	}
	i.started = true

	// Poll. Connections are cheap and a recovery is minutes to hours, so the
	// interval is about how often a screen should move rather than about load.
	deadline := time.Now().Add(opts.Timeout)

	// A running postmaster that will not accept a connection is its own
	// failure and is not the same as a slow one.
	//
	// The case that matters: the recovered cluster has the SOURCE's roles, so
	// connecting as a role that did not exist on the source fails on every
	// attempt, for ever, with the postmaster perfectly healthy. Without this
	// the recovery replays correctly and then sits until the four-hour timeout
	// saying "recovering", which is the least useful failure available.
	//
	// Ninety seconds, because reaching the consistency point on a large
	// cluster legitimately takes a while and connections are refused until it
	// does.
	const refusedGraceSeconds = 90
	refusedSince := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return sample, errs.New(errs.CodeUnavailable,
				"The recovery was interrupted before replay finished. The "+
					"recovered cluster has been stopped and removed; nothing "+
					"else was touched.")
		case <-time.After(time.Second):
		}
		if time.Now().After(deadline) {
			return sample, errs.New(errs.CodeUnavailable,
				"The recovery ran out of time before replay finished.")
		}

		inRecovery, s, err := i.sample(ctx, opts)
		if err != nil {
			// A connection that has stopped working usually means the
			// postmaster went away, and it goes away for one interesting
			// reason: it replayed everything the archive had without reaching
			// the target, which PostgreSQL reports as fatal and shuts down on.
			if !i.running() {
				return sample, i.recoveryEnded(opts)
			}
			if refusedSince.IsZero() {
				refusedSince = time.Now()
			}
			if time.Since(refusedSince) > refusedGraceSeconds*time.Second {
				return sample, errs.Newf(errs.CodeInternal,
					"The recovered cluster started and has refused every "+
						"connection for %ds. Connecting as %q; the recovered "+
						"cluster has the roles the BACKED-UP server had, so a "+
						"role that did not exist there does not exist here. "+
						"PostgreSQL said: %s",
					refusedGraceSeconds, opts.SuperUser,
					firstUsefulLine(i.tailLog()))
			}
			continue
		}
		refusedSince = time.Time{}
		if s.lsn != "" {
			sample.lsn = s.lsn
		}
		if !s.at.IsZero() {
			sample.at = s.at
		}
		if s.timeline != 0 {
			sample.timeline = s.timeline
		}
		if !inRecovery {
			if s.timeline != 0 {
				sample.timeline = s.timeline
			}
			return sample, nil
		}
	}
}

func startupWait(opts PITROptions) time.Duration {
	// Reaching the consistency point means replaying the WAL bundled with the
	// base backup, which on a large cluster is minutes. The default of 60
	// seconds is a recovery that reports failure while it is working.
	d := opts.Timeout / 4
	if d < 5*time.Minute {
		d = 5 * time.Minute
	}
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

// sample asks the recovering instance where it has got to.
func (i *instance) sample(
	ctx context.Context, opts PITROptions,
) (bool, replaySample, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, i.dsn(opts, "postgres"))
	if err != nil {
		return false, replaySample{}, err
	}
	defer conn.Close(context.WithoutCancel(ctx))

	var inRecovery bool
	var lsn, at *string
	var timeline *int32
	err = conn.QueryRow(ctx, `
		SELECT pg_is_in_recovery(),
		       pg_last_wal_replay_lsn()::text,
		       to_char(pg_last_xact_replay_timestamp() AT TIME ZONE 'UTC',
		               'YYYY-MM-DD"T"HH24:MI:SSZ'),
		       (SELECT timeline_id FROM pg_control_checkpoint())`).
		Scan(&inRecovery, &lsn, &at, &timeline)
	if err != nil {
		return false, replaySample{}, err
	}

	var s replaySample
	if lsn != nil {
		s.lsn = *lsn
	}
	if at != nil {
		if t, e := time.Parse("2006-01-02T15:04:05Z", *at); e == nil {
			s.at = t.UTC()
		}
	}
	if timeline != nil {
		s.timeline = uint32(*timeline)
	}
	return inRecovery, s, nil
}

// running reports whether the postmaster is still there.
func (i *instance) running() bool {
	cmd := exec.Command(toolPath(i.binDir, "pg_ctl"), "-D", i.dataDir, "status")
	return cmd.Run() == nil
}

// stop shuts the instance down, promptly.
//
// `-m fast` rather than smart: this cluster has no clients worth waiting for
// and no work worth finishing. Immediate would be faster still and would leave
// a data directory needing crash recovery, which matters when `Keep` was asked
// for and somebody is about to connect to it.
func (i *instance) stop() error {
	if !i.started {
		return nil
	}
	i.started = false
	cmd := exec.Command(toolPath(i.binDir, "pg_ctl"),
		"-D", i.dataDir, "-m", "fast", "-w", "-t", "120", "stop")
	out, err := runDetaching(cmd, filepath.Join(i.dir, "pg_ctl-stop.out"))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal, fmt.Sprintf(
			"The recovered cluster would not stop. %s", firstUsefulLine(out)))
	}
	return nil
}

// runDetaching runs a command whose child may outlive it, and captures its
// output through a FILE rather than a pipe.
//
// # The bug this exists to prevent
//
// `cmd.Output()` and a `cmd.Stdout` that is not an `*os.File` make Go create a
// pipe and wait for it to reach end-of-file before `Wait` returns. `pg_ctl
// start` launches a postmaster that inherits that pipe and then stays running
// for hours — so `Wait` does not return until the DATABASE exits, and a
// recovery that should take four minutes hangs for ever with no diagnostic at
// all.
//
// Handing the command a real file gives Go nothing to wait on beyond the direct
// child, which is the process that actually exits. The output is read back
// afterwards.
//
// This is not a Windows quirk; it behaves the same way everywhere. It was found
// by the drill in pitr_test.go, which hung.
func runDetaching(cmd *exec.Cmd, logPath string) (string, error) {
	f, err := os.Create(logPath)
	if err != nil {
		// Without somewhere to put the output, run without capturing it rather
		// than refusing: the command matters and its diagnostics do not.
		if runErr := cmd.Run(); runErr != nil {
			return "", runErr
		}
		return "", nil
	}
	cmd.Stdout = f
	cmd.Stderr = f
	runErr := cmd.Run()
	f.Close()

	body, readErr := os.ReadFile(logPath)
	if readErr != nil {
		return "", runErr
	}
	const keep = 8 << 10
	if len(body) > keep {
		body = body[len(body)-keep:]
	}
	return string(body), runErr
}

// startupFailure turns a refusal to start into the sentence that explains it.
//
// PostgreSQL writes the reason to the log file and `pg_ctl` prints "could not
// start server". The reason is always more useful than the summary, and the
// three reasons that actually happen are worth naming outright because each of
// them has a different fix.
func (i *instance) startupFailure(err error, out string) error {
	log := i.tailLog()
	combined := out + "\n" + log
	lower := strings.ToLower(combined)

	switch {
	case strings.Contains(lower, "before the backup was completed") ||
		strings.Contains(lower, "before consistent recovery point"),
		strings.Contains(lower, "requested recovery stop point is before"):
		return errs.Newf(errs.CodeInvalidInput,
			"The recovery target is before the moment this base backup "+
				"becomes consistent, so there is nothing to stop at. Choose a "+
				"target after the base backup finished, or an older base "+
				"backup. PostgreSQL said: %s", firstUsefulLine(log))
	case strings.Contains(lower, "incompatible with server"):
		return errs.Newf(errs.CodeInvalidInput,
			"The base backup was written by a different PostgreSQL major "+
				"version from the one here, and a physical backup cannot be "+
				"read across versions. PostgreSQL said: %s",
			firstUsefulLine(log))
	case strings.Contains(lower, "invalid checkpoint record") ||
		strings.Contains(lower, "could not locate a valid checkpoint record"):
		return errs.Newf(errs.CodeInternal,
			"The recovered data directory has no valid checkpoint, which "+
				"means the base backup is incomplete or was corrupted in the "+
				"store. It is not a recovery source. PostgreSQL said: %s",
			firstUsefulLine(log))
	}
	return errs.Wrap(err, errs.CodeInternal, fmt.Sprintf(
		"The recovered cluster would not start. %s", firstUsefulLine(combined)))
}

// recoveryEnded explains a postmaster that went away part-way through.
//
// There is one common cause and it is the one that matters: the archive ran out
// of segments before the target was reached. A recovery that ended there and
// said "finished" would be a recovery that silently returned an earlier moment
// than the one asked for.
func (i *instance) recoveryEnded(opts PITROptions) error {
	log := i.tailLog()
	lower := strings.ToLower(log)
	if strings.Contains(lower, "recovery ended before configured recovery target") {
		return errs.Newf(errs.CodeInvalidInput,
			"Replay reached the end of the archive without arriving at %s. "+
				"The segments needed to go further are not in the archive — "+
				"either they were never written, or they have been removed by "+
				"retention, or there is a gap. `rawsyst backup wal gaps` says "+
				"which. Nothing has been recovered.",
			opts.Target.Describe())
	}
	return errs.Newf(errs.CodeInternal,
		"The recovering cluster stopped before replay finished. PostgreSQL "+
			"said: %s", firstUsefulLine(log))
}

// tailLog reads the end of the recovery log.
func (i *instance) tailLog() string {
	body, err := os.ReadFile(i.logPath)
	if err != nil {
		return ""
	}
	const keep = 8 << 10
	if len(body) > keep {
		body = body[len(body)-keep:]
	}
	return string(body)
}

// --- looking at what came back ----------------------------------------------

// inspectRecovered counts what is in the recovered cluster.
//
// The same inventory a `pg_dump` verification takes, on the same code path, so
// the two kinds of recovery are described in the same terms and can be compared
// with each other. A recovery that produced a cluster nobody counted is a
// recovery nobody has any reason to believe.
func inspectRecovered(
	ctx context.Context, opts PITROptions,
	base BaseManifest, inst *instance, report *PITRReport,
) error {
	database := opts.AppDatabase
	if database == "" {
		database = base.DatabaseName
	}
	if database == "" {
		database = "postgres"
	}

	conn, err := pgx.Connect(ctx, inst.dsn(opts, database))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternal, fmt.Sprintf(
			"The recovered cluster replayed to its target and then could not "+
				"be opened at database %q to check what is in it.",
			clipText(database, 64)))
	}
	defer conn.Close(context.WithoutCancel(ctx))

	var inRecovery bool
	if err := conn.QueryRow(ctx, `SELECT pg_is_in_recovery()`).
		Scan(&inRecovery); err == nil && inRecovery {
		report.finding(
			"The recovered cluster is still in recovery after replay was " +
				"reported as finished. It has not been promoted and cannot " +
				"be written to.")
	} else {
		report.checked("the recovered cluster left recovery and accepts writes")
	}

	inventory, err := takeInventory(ctx, conn)
	if err != nil {
		// A recovered cluster that is not this product's is two different
		// things depending on who asked.
		//
		// For a deployment recovering ITS OWN cluster it is serious: the bytes
		// replayed into something with no migration ledger, which means the
		// recovery produced a database nobody should put in front of anybody.
		//
		// For the drill it is expected. The drill builds a cluster from
		// `initdb` to prove that archiving, replay and timing work, and it has
		// no business containing this product's tables. A recovery engine that
		// could only recover RawSyst would be one nobody could test without a
		// RawSyst.
		if opts.ExpectProduct {
			report.finding(
				"The recovered database could not be counted: %s. It replayed "+
					"into something that is not this product, so nothing can "+
					"be said about what is in it.", err.Error())
			return nil
		}
		report.checked(
			"the recovered cluster is not a RawSyst database, so its contents " +
				"were not counted; the recovery itself completed")
		return nil
	}
	report.Inventory = &inventory
	report.checked(fmt.Sprintf(
		"%d tables, schema version %d, %d businesses",
		inventory.TableCount(), inventory.SchemaVersion,
		len(inventory.TenantRows)))

	// The two claims worth checking on any restored copy of this product, for
	// the same reasons `verify.go` checks them on a dump. Both are about the
	// CONTENTS rather than about the recovery, so they only apply where the
	// contents were expected to be this product's.
	if !opts.ExpectProduct {
		return nil
	}
	if inventory.TableCount() == 0 {
		report.finding(
			"The recovered database has no tables. Whatever replayed, it was " +
				"not this product.")
	}
	if inventory.RLSForced == 0 && inventory.TableCount() > 0 {
		report.finding(
			"Row-level security is not FORCED on any table in the recovered " +
				"database. That attribute is the only thing keeping one " +
				"business out of another's books, and a recovery that lost it " +
				"must not be put in front of anybody.")
	} else if inventory.RLSForced > 0 {
		report.checked(fmt.Sprintf(
			"row-level security is still forced on %d tables",
			inventory.RLSForced))
	}
	if inventory.SchemaVersion == 0 {
		report.finding(
			"The recovered database has no migration ledger, so there is no " +
				"way to say which schema it is at.")
	}

	// Sequences. A recovery that brought the rows back and left the sequences
	// behind issues a duplicate invoice number on its first write, which is
	// worse than a visible failure because it happens after everyone has gone
	// home believing it worked.
	if len(inventory.Sequences) > 0 {
		report.checked(fmt.Sprintf("%d sequences carried their positions",
			len(inventory.Sequences)))
	}

	if opts.Log != nil {
		opts.Log.Info("point-in-time recovery inspected",
			slog.String("base_backup", base.ID),
			slog.Int("tables", inventory.TableCount()),
			slog.Int("findings", len(report.Findings)))
	}
	return nil
}
