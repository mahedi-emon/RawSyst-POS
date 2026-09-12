# Turning point-in-time recovery on

The activation checklist for a server that is already running.

`PITR.md` is what the system is and how to use it. This is the narrower thing:
the ordered steps to switch it on for the first time on a live machine, what to
check after each one, and how to undo it.

**Nothing here has been done on the production server.** The implementation is
complete and tested; the activation is not. Read *Where this stands* first.

---

## Where this stands

| | |
|---|---|
| Code complete | **Yes** |
| Tested locally | **Yes** |
| Production activation | **Pending** — everything below |
| Production activated | **No** |
| Production recovery verified | **No** |
| RTO | **Unknown until tested on the real server** |
| RPO | **Not confirmed until WAL archiving is activated on production** |

### Already implemented and tested

| | Proved by |
|---|---|
| Continuous archiving, encrypted, to S3-compatible storage | Both drills |
| A failed upload keeps the segment rather than losing it | `TestAnUnreachableStoreFailsTheArchiveSoPostgreSQLKeepsTheSegment` |
| Physical base backups with timeline, start/end LSN, checksums, encryption state | `TakeBaseBackup`, exercised in both drills |
| Recovery to a moment, to the latest point, and to the base backup itself | Both drills; the container one against the real 191-table schema |
| Gap detection, orphan detection, retention that refuses more than it deletes | `wal_test.go`, container drill |
| Corrupt segment, truncated segment, missing segment, wrong key, no key, incomplete base backup, unreachable store, invalid timestamp, timeline mismatch, interrupted recovery | `wal_test.go` and `pitr_test.go`, one test each |
| The database image builds, starts, and adds its own `pg_hba.conf` replication line | Container drill; verified again on the local stack |
| The backup agent starts and claims work against the migrated schema | Container drill; local stack, 5.2 MiB resident |
| A base backup can be downloaded, checksum-checked, and is refused a path outside its own prefix | `TestABaseBackupCanBeCarriedAwayAndStillChecksOut` |
| Backend suite, race detector, `govulncheck`, frontend suite, typecheck, lint, production build, API contract, route reachability | Run in full; all pass |

### Required before production activation

Everything in *The plan* below, steps 1 to 9. None of it has been done.

### Required after production activation

- Step 10, the first real base backup and the recovery timed against it.
- Step 11, watching the first 24 hours.
- Recording the measured RTO into `PITR.md`, which currently says it is unknown.

### Measured locally, and what that is worth

Taken on a development machine with the object store inside the test process.
**No byte crossed a network, so none of this is an RTO.** It is the shape of the
cost and the baseline the real measurement is compared against. Full tables in
`PITR.md`.

- **A base backup is linear in cluster size**, about 21 MB/s here, compressing
  1.8–2.1x on deliberately incompressible data.
- **A recovery is not linear in database size.** It is fetch-and-unpack, which
  scales with the backup, plus replay, which scales with the log written since
  it. 424 MB recovered in 27 s and 841 MB in 30 s.
- **`pg_wal` under archive failure** grew 0.94 GiB/hour idle and 52.5 GiB/hour
  under an artificial hammering. Eight gigabytes of headroom is nine hours
  idle.
- **An outage cost no recovery window.** 31 failed attempts, every segment kept,
  the backlog drained on its own in 37 s, 0 gaps in 58 segments.

### Not yet measured

These are unknown and are stated as unknown rather than estimated:

- **Base backup duration on the real database.** The local figures scale at
  21 MB/s of cluster; the real one is that rate against real hardware.
- **Recovery time objective on real data.** Dominated by the download from the
  object store over the shop's own connection, which nothing local exercised.
- **Archive lag over a real network.** 200–600 ms to a store on the same
  machine, 3–10 s under load. To Cloudflare R2 over a domestic line, seconds.
- **WAL generated per trading day**, and therefore the monthly storage and
  request cost of a seven-day window.
- **Whether `pg_monitor` can be granted.** It can on a self-hosted PostgreSQL.
  Step 3 reports it either way.

The recovery POINT does not depend on any of these. It is
`archive_timeout` plus the archive lag, so about **61 seconds** at the shipped
defaults once step 6 is done.

---

## Before you start

- [ ] A bucket exists, on an account that is **not** this server's, and its
      credentials are in the secret store.
- [ ] You have decided about encryption. If it is on,
      `RAWSYST_BACKUP_ENCRYPTION_KEY` is already set and already backed up in
      two places — see `SECRETS.md`. **The archive uses the same key**, so a
      key lost after activation loses the archive as well as the dumps.
- [ ] The nightly dump timer is green: `$C run --rm backup health`.
- [ ] You have an hour, out of trading hours, and nobody is waiting on you.

Throughout, and as in `BACKUP.md`:

```bash
C="docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup"
```

### The order matters, and here is why

There are **two restarts**, and they are not one restart done twice.

`archive_mode` is a `postmaster` setting: it cannot be reloaded, only restarted
into. And turning it on while the bucket is misconfigured is the one change here
that damages the server — PostgreSQL keeps every segment it cannot ship, and the
disk fills.

So the bucket is proved to work **while archiving is still off**, and only then
is it switched on. The first restart costs nothing if the storage settings are
wrong; the second one is taken when they are known to be right.

`archive_command`, `archive_timeout` and `wal_keep_size` are `sighup` settings
and can be changed with a reload afterwards. `wal_level` is already `replica` on
this server and does not change at all.

---

## The plan

### 1. Storage settings, with archiving still off

Add to `/opt/biz1core/.env`. `.env.example` documents each one at length.

```bash
RAWSYST_S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com
RAWSYST_S3_BUCKET=<bucket>
RAWSYST_S3_REGION=auto
RAWSYST_S3_ACCESS_KEY_ID=<id>
RAWSYST_S3_SECRET_ACCESS_KEY=<secret>
RAWSYST_S3_PATH_STYLE=true

# NOT YET. This line goes in at step 6, after the bucket is proved.
# POSTGRES_ARCHIVE_MODE=on
```

If the dumps already go to a bucket, these are already set and this step is
reading them rather than writing them. **Use the same bucket.** The archive gets
its own prefix inside it (`<prefix>/wal/`) and cannot collide with the
snapshots.

- [ ] `chmod 600 .env`, and `git status` does not list it.
- [ ] `POSTGRES_ARCHIVE_MODE` is absent or `off`.

### 2. Rebuild the database image and restart into it

The `db` service is now built from this repository: `postgres:17-alpine` plus
the archiver binary and an entrypoint that adds one line to `pg_hba.conf`. See
`deploy/postgres/Dockerfile`.

**The data directory is not touched.** The wrapper `exec`s the official
entrypoint, so initdb, the `POSTGRES_*` variables, the ownership fixing and the
drop to the postgres user all happen exactly as before.

Take the write freeze first. The restart is seconds, and a sale rung up into a
connection that is being torn down is the kind of thing that is only obvious
afterwards.

```bash
# Maintenance mode on: Platform > Backup & Recovery, or
#   PUT /api/v1/platform/maintenance  {"active": true}

cd /opt/biz1core && git pull
$C build db
$C up -d db

# Watch it come back.
$C ps db
$C logs db | tail -20
```

- [ ] `$C ps db` says healthy.
- [ ] The log contains `biz1core: added a replication line to pg_hba.conf`, or
      the line is already there from a previous start:
      ```bash
      $C exec db grep -A2 'biz1core: replication' /var/lib/postgresql/data/pg_hba.conf
      ```
- [ ] Archiving is still off, which is correct at this point:
      ```bash
      $C exec db psql -U rawsyst -d rawsyst -c \
        "SELECT name, setting FROM pg_settings WHERE name IN ('wal_level','archive_mode','max_wal_senders')"
      ```
      Expect `replica`, `off`, and at least 2.
- [ ] The API reconnected: `$C logs api | tail -20` shows no continuing errors.
- [ ] Turn maintenance mode **off** and confirm a till can sell.

Rebuild the rest at the same time if the deploy is a normal one; nothing else
changed behaviourally. If `biz1core/backend` comes out at around 450 MB rather
than 34 MB, the build took the wrong stage — pull again, because that fix is in
this change.

### 3. The backup role: REPLICATION and pg_monitor

`pg_basebackup` copies the cluster over a **replication connection**, which is a
different thing from reading the tables and is refused to a role without the
attribute. The role also wants `pg_monitor` so the health readout can measure
how much write-ahead log has piled up locally.

```bash
$C run --rm backup role
```

Idempotent. It refuses outright if the **application's** role can see past
row-level security, which is the check worth failing over.

- [ ] The statement list includes `ALTER ROLE "rawsyst_backup" REPLICATION` (or
      the role was created with it) and `GRANT pg_monitor`.
- [ ] It ends `checked  the role can read every table and sequence`.
- [ ] If it prints a `monitor_note`, `pg_monitor` could not be granted. Grant it
      by hand as a superuser, or accept that the local `pg_wal` figure will read
      as zero — **and write that down**, because that figure is the early
      warning for a failing archive.

### 4. Prove the bucket, still with archiving off

This is the step that makes the second restart safe.

```bash
$C run --rm backup wal preflight
```

Expect exactly this shape:

```
  ok    physical base backup     rawsyst_backup may replicate; wal_level=replica, max_wal_senders=10
  FAIL  archive_mode             archive_mode is off. Nothing is being shipped off this machine.
  FAIL  archive_command          archive_command is empty, ...
  ok    object store             <bucket> answered
  ok    encryption               on, key <fingerprint>
```

The two `FAIL` lines are **expected here** and are what step 6 fixes. The line
that matters now is `object store`.

- [ ] `physical base backup` is `ok`. If it is not, step 3 did not take.
- [ ] `object store` is `ok`. If it is not, stop — this is exactly the
      misconfiguration that would fill the disk, and it costs nothing to find
      here.
- [ ] `encryption` says what you expect. If you intended it on and it says off,
      stop: segments written now would not be sealed and there is no way to
      seal them later.

Then prove a real write and read, rather than just a listing:

```bash
$C run --rm backup run       # a dump, written to the same bucket
$C run --rm backup verify    # pulled back, restored, counted
```

- [ ] `verify` passes. The archive uses the same credentials, the same signing
      and the same encryption, so a dump that round-trips is evidence about the
      archive.

### 5. Storage room for what is coming

A seven-day archive plus two base backups is roughly two compressed copies of
the database plus a week of log. Nobody knows what a week of log is on this
server yet — see *Not yet measured* — so the check is on the local disk rather
than on the bucket.

- [ ] `bash deploy/server/biz1core-check.sh` reports disk within threshold
      (at least 8 GiB free and under 85% used).
- [ ] The bucket has no lifecycle rule that deletes objects. Retention here
      deletes only what nothing can still need; a provider-side rule deleting on
      age would remove segments a retained base backup requires, and the first
      symptom would be a gap.

### 6. Turn archiving on

```bash
# In /opt/biz1core/.env
POSTGRES_ARCHIVE_MODE=on
```

`archive_mode` is a postmaster setting, so this needs a restart rather than a
reload. Maintenance mode again, for the same reason as step 2.

```bash
$C up -d db
$C exec db psql -U rawsyst -d rawsyst -c \
  "SELECT name, setting FROM pg_settings WHERE name LIKE 'archive%'"
```

- [ ] `archive_mode` is `on` and `archive_command` is
      `/biz1core backup wal archive %p %f`.
- [ ] `archive_timeout` is 60.

### 7. Prove segments are actually arriving

```bash
$C exec db psql -U rawsyst -d rawsyst -c "SELECT pg_switch_wal()"
sleep 10
$C run --rm backup wal status
```

- [ ] `last archived` names a segment and `last archived at` is seconds ago.
- [ ] `archived / failed` shows failures at **0**.
- [ ] **Write down the archive lag.** That number plus `archive_timeout` is this
      installation's recovery point, and it is one of the things listed as not
      yet measured.

If `failed` is climbing:

```bash
$C logs db | grep -i archive | tail -20
```

The archiver prints this product's own sentence and never a credential. The
commonest causes are a bucket name that does not exist and a key without write
permission. **Go to *Rollback*, section A** — do not leave it failing while you
investigate, because the disk is filling while you do.

### 8. The first base backup

Until this exists, the archive can be replayed onto nothing.

```bash
time $C run --rm backup basebackup
```

- [ ] It ends `Stored. NOT yet verified`.
- [ ] It says `encrypted true` if a key is configured.
- [ ] **Write down the duration and the stored size.** This is the first of the
      two unmeasured numbers.
- [ ] Pull it down once, to prove the copy is retrievable and that you are not
      locked in to the storage provider:
      ```bash
      $C run --rm backup basebackup -download -to /srv/out
      ```
      Four files, each checked against the manifest as it lands. They come down
      **sealed** if encryption is on; the manifest names the key fingerprint.
      This is not a substitute for the dump you carry on a laptop — see
      *Carrying a base backup away* in `PITR.md` for why.

Then prove it recovers, which is the cheapest possible recovery — it replays
only the log the backup carries:

```bash
time $C run --rm backup pitr -target immediate
```

- [ ] `PASSED`.
- [ ] It reports the table count and schema version this server actually has,
      and `row-level security is still forced on N tables`.

### 9. The timers

```bash
sudo cp deploy/server/biz1core-basebackup.{service,timer} /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now biz1core-basebackup.timer
systemctl list-timers 'biz1core-*'
```

- [ ] `biz1core-basebackup.timer` is listed, next run Sunday 02:00.
- [ ] Run it once by hand rather than waiting a week:
      ```bash
      sudo systemctl start biz1core-basebackup.service
      journalctl -u biz1core-basebackup.service -f
      ```
      It takes a base backup, recovers it to `immediate`, then prunes. All three
      must succeed; `ExecStartPost` stops at the first failure, so nothing is
      deleted on a night the new copy could not be proved.

### 10. After activation: the recovery window and the real RTO

Wait until there are a few minutes of archived log, then:

```bash
$C run --rm backup pitr -window
$C run --rm backup wal verify -deep 8
```

- [ ] The window spans from the base backup to within a minute or two of now.
- [ ] No gaps.
- [ ] The deep check passes. It downloads and decrypts eight segments, which is
      the only check that proves the bytes are readable.

Then the measurement this whole plan exists to obtain:

```bash
# A moment a few minutes ago, inside the window.
time $C run --rm backup pitr -target before_time -at 2026-09-11T14:32:00Z -keep
```

- [ ] `PASSED`, and `replay stopped at` is at or before the moment asked for.
- [ ] Connect with the printed connection string, count something you recognise,
      then stop it:
      ```bash
      $C run --rm backup -- pg_ctl -D <the printed data directory> stop
      ```
- [ ] **Write down the total, the fetch time and the replay time.** That is the
      RTO for a point-in-time recovery on this server. Put it into the *RPO and
      RTO* section of `PITR.md`, replacing the sentence that says it is not
      measured.

### 11. After activation: the first day

- [ ] **One hour in.** `$C run --rm backup wal status` is GREEN. `pg_wal` is not
      growing: the readout's `local pg_wal` should sit near `max_wal_size` plus
      `wal_keep_size`, not climb.
- [ ] **Overnight.** `archive_timeout` forces a 16 MiB segment every 60 seconds
      on an idle server, so a closed shop writes about 23 GiB to local disk
      overnight and a few megabytes to the bucket. Check the morning after that
      the disk is where you left it. If the local write volume is a problem on
      this disk, raise `POSTGRES_ARCHIVE_TIMEOUT` to 300 and reload — it is a
      `sighup` setting, so no restart:
      ```bash
      $C exec db psql -U rawsyst -c "SELECT pg_reload_conf()"
      ```
      That trades a five-minute recovery point on quiet nights for a fifth of
      the writes.
- [ ] **The screen agrees with the command line.** Sign in as the platform
      operator, open **Backup & Recovery → Recovery**. The health word, the
      window, the last archived segment and the base backup must match what
      `wal status` says. The agent writes that reading once a minute; if the
      screen says the reading is stale, the agent is not running:
      `$C ps backup-agent`.
- [ ] **The machine's own check knows about it.** `bash
      deploy/server/biz1core-check.sh` now reports a `wal archive` line and flags
      a `local pg_wal` over 2 GiB. Confirm the line appears and is green.
- [ ] **A week in.** `$C run --rm backup wal prune` reports a horizon and would
      remove something. If it says `REFUSED`, read the reason: it refuses rather
      than guessing, and every refusal names what it could not establish.

---

## Rollback

Three situations, and they are not the same.

### A. Archiving is failing and the disk is filling

**Do not empty `archive_command`.** With `archive_mode` on and the command
empty, PostgreSQL disables archiving *temporarily* and keeps accumulating
segments in the expectation that a command will be supplied. That is the
documented behaviour and it makes the problem worse.

In order of preference:

1. **Fix the store.** A wrong key, a bucket that does not exist, an expired
   credential. When it works, PostgreSQL ships the backlog by itself and nothing
   is lost. This is almost always the right answer and it needs no restart.

2. **If the disk is the emergency**, turn archiving off and restart:
   ```bash
   # In /opt/biz1core/.env
   POSTGRES_ARCHIVE_MODE=off

   $C up -d db
   ```
   PostgreSQL recycles the backlog at the next checkpoint and the database is
   safe. **This permanently ends the recovery window at this moment**: the
   segments recycled here were never shipped and cannot be. Point-in-time
   recovery is off until archiving is back on *and* a new base backup has been
   taken. Write down that you did it and when.

3. When the store is back, redo steps 6, 7 and 8. The new base backup is what
   restarts the window.

### B. Something about the new database image is wrong

The image change is the archiver binary, an entrypoint wrapper and an initdb
script. It does not touch `PGDATA`, so reverting is a restart rather than a
restore.

**Order matters.** Turn archiving off *first*, then revert the image. Reverting
while `archive_mode` is on leaves `archive_command` pointing at a binary that is
no longer in the container, and every segment is retained — situation A, caused
by the rollback.

```bash
# 1. archiving off, in .env
POSTGRES_ARCHIVE_MODE=off

# 2. the previous image
git checkout <previous-commit> -- docker-compose.yml docker-compose.server.yml
$C up -d db
```

The `pg_hba.conf` line the entrypoint added stays behind. It is inert — it
admits one role to a connection type nothing is making — and can be left.

### C. The activation is fine but you want it off

Steps 1 to 5 change nothing about how the server runs: they add environment
variables, rebuild an image, and grant two attributes to a role that already
exists. Leaving them in place is harmless.

Only step 6 changes behaviour. Undo it the same way: `POSTGRES_ARCHIVE_MODE=off`
and restart. The dumps continue exactly as before and the recovery point returns
to 24 hours, which `BACKUP.md` states.

---

## What this plan does not make true

**It is not production readiness.** Every item above is either a step nobody has
taken or a number nobody has. The implementation is tested — two drills, one
against the real images and the real migration chain — and the activation is
untested by definition, because it can only be tested on the server it activates.

**The recovery point becomes about 61 seconds after step 6, and not before.**
Until then this server's recovery point is whatever the nightly dump gives,
which is up to 24 hours.

**The recovery time is unknown until step 10.** Anybody quoting one before that
measurement is quoting a guess.

**A drill proves the software recovers. It does not prove a person can.** Walk
`PITR.md` on a spare machine once, with a stopwatch, before the day it matters.
