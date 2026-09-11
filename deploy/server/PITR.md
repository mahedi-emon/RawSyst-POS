# Point-in-time recovery

Getting the database back to a moment nobody photographed.

`BACKUP.md` covers the dumps: one a night, proved to restore, kept off this
server. This covers the other half — continuous archiving of the write-ahead
log, and the physical base backups it is replayed onto. Read `BACKUP.md` first;
this assumes it.

**Switching it on for the first time on a running server is
[PITR-ACTIVATION.md](PITR-ACTIVATION.md)**, which is ordered, has a rollback, and
says plainly which parts of this document describe something that has been done
and which describe something that has been built. Read that one before touching
a live machine.

---

## Why the dumps are not enough on their own

A `pg_dump` is a photograph. The timer takes one at 03:30, so a server lost at
22:00 loses the trading day. That is not a flaw in the dump — it is what a
photograph is — and `BACKUP.md` stated the 24-hour recovery point plainly rather
than softening it.

The write-ahead log is the film. PostgreSQL writes every change to it *before*
it writes the change, so a byte-level copy of the cluster plus every segment
since reconstructs any second in between.

The two protect against different things and neither replaces the other:

| | `pg_dump` snapshot | physical base backup + WAL |
|---|---|---|
| Recovery point | the moment of the dump | any second in the window |
| Portable across major versions | yes | **no** |
| Survives a corrupt cluster | yes — it is re-created from SQL | no — the corruption is copied |
| Restores one database | yes | the whole cluster |
| Readable on any machine | yes | only the same major version |

A corrupted page is inside the base backup and inside the log. It is not inside
the dump. Keep both.

---

## The architecture

```
   ┌──────────────────── the server ────────────────────┐
   │                                                    │
   │  db (rawsyst/postgres)          backup-agent       │
   │  ├─ postgres 17                 ├─ takes base      │
   │  ├─ archive_mode = on           │  backups         │
   │  └─ archive_command ────────┐   ├─ observes the    │
   │       /rawsyst backup wal   │   │  archive, once   │
   │       archive %p %f         │   │  a minute        │
   │                             │   └─ recovers into   │
   │                             │      a PostgreSQL    │
   │                             │      of its own      │
   └─────────────────────────────┼──────────────────────┘
                                 │
                                 ▼
                    S3-compatible object store
                    ├─ <prefix>/<snapshot-id>/     pg_dump snapshots
                    ├─ <prefix>/wal/<timeline>/    WAL segments + .meta
                    ├─ <prefix>/wal/history/       timeline history files
                    └─ <prefix>/basebackup/<id>/   base.tar.gz, pg_wal.tar.gz,
                                                   backup_manifest,
                                                   manifest.json, COMPLETED
```

Three things are worth stating outright because they are the decisions
everything else follows from.

**The archiver is this product's own binary, inside the PostgreSQL container.**
`archive_command` is a command PostgreSQL runs, so whatever it names has to
exist in that container. It could have been a shell script with a presigned URL
or a third-party tool added with `apk`; both are worse for the same reason. The
archiver has to encrypt with the same key and write the same sidecar as
everything else that reads this archive, and a second implementation of that is
a second thing to get wrong in the one place where being wrong is silent. So
`deploy/postgres/Dockerfile` is `postgres:17-alpine` plus `/rawsyst`, and the
database image is built from this repository.

**The archive has its own prefix, never mixed in with the dumps.** A snapshot
and a WAL segment have different lifetimes, different retention rules and
different consequences for being deleted early, and the one routine that deletes
things works by listing a prefix. Sharing a prefix would make a retention bug in
one of them a data-loss bug in the other.

**A failed upload keeps the segment.** When the store cannot be reached, the
archive command exits non-zero, PostgreSQL retains the segment and retries. That
is deliberate and it has a cost — see *When the object store is down*.

---

## The WAL archive flow

Per segment, once:

```
  PostgreSQL fills a 16 MiB segment (or archive_timeout closes a partial one)
    → archive_command: /rawsyst backup wal archive %p %f
        → validate the name: 24 upper-case hex characters, nothing else
        → gzip
        → AES-256-GCM, if RAWSYST_BACKUP_ENCRYPTION_KEY is set
        → PUT <prefix>/wal/<timeline>/<segment>
        → PUT <prefix>/wal/<timeline>/<segment>.meta
        → exit 0
    → PostgreSQL is now free to recycle the segment
```

Compressed before it is encrypted, because ciphertext does not compress. A
mostly-idle 16 MiB segment lands in the bucket as a few kilobytes, which is what
makes a 60-second `archive_timeout` affordable.

**The sidecar is what makes a segment count.** Beside every archived file is a
small JSON `.meta` holding the plaintext length and checksum, the stored length
and checksum, the compression, whether it is sealed, and the key fingerprint. It
exists for three reasons:

- Verification without egress. Checking an archive otherwise means downloading
  every segment, which is charged by the gigabyte for a check that should be
  cheap enough to run hourly.
- Idempotence. PostgreSQL may re-archive a segment after a crash; comparing
  checksums answers "is this the same file" in one small read. Identical content
  succeeds. **Different** content under the same name is refused loudly — the
  only ways to produce it are two clusters sharing one prefix, or an archive
  something else has written to.
- Completeness. An object with no sidecar is an upload that did not finish, and
  it counts as a gap rather than as a segment. Writing the sidecar last is what
  makes "present" mean "whole", exactly as the `COMPLETED` marker does for a
  snapshot.

Names are validated before anything becomes a path. A segment name is
twenty-four upper-case hexadecimal characters; a history file is eight and
`.history`; a backup label is a segment name, a dot, an offset and `.backup`.
Anything else is refused rather than guessed at, because that name becomes a key
in somebody else's object store.

---

## The base backup process

```bash
docker compose run --rm backup basebackup
```

or the button on **Platform → Backup & Recovery → Recovery**.

Under it:

```
  pg_basebackup --format=tar --compress=gzip --wal-method=stream
                --checkpoint=fast --manifest-checksums=SHA256
    → base.tar.gz, pg_wal.tar.gz, backup_manifest in the staging volume
    → seal each with the same key the dumps use
    → stream each into the bucket
    → manifest.json  (ours: timeline, start and end LSN, segments, checksums)
    → COMPLETED
    → recover it to `immediate` and count what came back
```

Four things about that are deliberate.

**`--wal-method=stream`** makes `pg_basebackup` open a second connection and
stream the segments written *while it runs* into `pg_wal.tar.gz`. Those same
segments also go through `archive_command`, so this is a deliberate duplicate —
and it is the duplicate that makes a base backup self-consistent on its own. A
base backup taken during a window when archiving was broken would otherwise be
unable to reach even its own consistency point, and would not find that out
until somebody tried to recover from it. It needs `max_wal_senders >= 2`.

**`--checkpoint=fast`** forces the checkpoint instead of spreading it over
`checkpoint_completion_target`, which on a quiet shop server means waiting
several minutes before the copy begins. The I/O spike is worth the
predictability.

**SHA-256 per file** rather than the default CRC-32C. CRC catches a flipped bit;
it does not stand up to anything deliberate, and this manifest is what a
verification later trusts.

**Stored is not verified.** The run does not stop at the upload: it recovers the
backup it just took to `immediate` — the cheapest possible recovery, replaying
only the log the backup carries — and the row reaches `verified` only when that
produced a cluster something counted. A base backup nobody has recovered is a
file in a bucket, which is the exact thing this subsystem exists to stop a
backup from being.

### The role, and the pg_hba line

`pg_basebackup` copies the cluster over a **replication connection**, which is a
different thing from reading the tables. Two things have to be true:

```bash
docker compose run --rm backup role       # grants REPLICATION and pg_monitor
```

and `pg_hba.conf` must admit a replication connection:

```
host    replication    rawsyst_backup    all    scram-sha-256
```

The ordinary `host all all all scram-sha-256` line the official image appends
does **not** cover it: `all` in the second column means all *databases*, and
`replication` is not a database. `deploy/postgres/entrypoint.sh` adds the line on
every container start and is idempotent, so both new and existing clusters get
it. It names the role rather than `all` deliberately — `host replication all all`
would let every role that can log in stream a byte-level copy of every business
on the server.

`pg_monitor` is granted best-effort. Without it the archive readout cannot call
`pg_ls_waldir()` and reports the local write-ahead log size as zero, which is a
health check reporting a comfortable number it could not measure.

---

## How the recovery window is calculated

Three things must all hold for a moment to be recoverable:

1. a base backup that **finished** before it and is still in the store;
2. every segment from that backup's ending position up to it, with no holes;
3. the timeline history to follow, if the cluster has ever been promoted.

So:

```
  window start = when the OLDEST retained base backup finished
  window end   = when the last CONTIGUOUS segment was archived
```

Two details do the work.

**The start is when the backup finished, not when it started.** During the copy
the cluster on disk is inconsistent, and a target inside that span is not a
target. PostgreSQL refuses it at startup with *recovery target time is before
the consistency point*, after the download.

**The end is the last contiguous segment, not the newest object.** Replay stops
at the first missing segment, so everything behind a hole is unreachable however
many objects are sitting there. This is the single most expensive
misunderstanding available here, and it is why the window is reported as
`truncated_by_gap` rather than as the newest thing in the bucket.

```bash
docker compose run --rm backup pitr -window
docker compose run --rm backup wal status
```

---

## Recovering to a timestamp

Always into an isolated PostgreSQL. Production is never opened by this and
cannot be reached from it.

```bash
# Is that moment even reachable? One listing, no download.
docker compose run --rm backup pitr -window

# Recover to just before the mistake, and leave it running to look at.
docker compose run --rm backup pitr \
  -target before_time -at 2026-09-11T14:32:00Z -keep
```

`before_time` stops **strictly before** the moment given, so a transaction that
committed exactly then is left out. That is almost always what somebody
recovering from a mistake wants: "the delete ran at 14:32" means the wanted state
is the one before 14:32. `time` is the inclusive variant.

The timestamp is written into the recovery configuration with an explicit UTC
offset. Without one PostgreSQL reads it in the *server's* `TimeZone`, which for
a Riyadh shop is three hours away from what an operator typed — three hours of
trading either recovered or lost, silently, with the recovery reporting success
either way.

What the run prints:

```
  asked for            the last transaction committed strictly before 2026-09-11T14:32:00Z
  base backup          20260911T031500Z-41 (2026-09-11T03:18:22Z)
  replay stopped at    2026-09-11T14:31:58Z  (0/9A000148)
  recovered            193 tables, schema 136, 4 businesses
```

**Read both lines.** A recovery asked for 14:32 that stopped at 14:31:58 because
that was the last committed transaction is a success. One that stopped at 09:00
because the archive had a hole is not, and the only way to tell them apart is to
print both.

With `-keep` the instance stays up and the command prints how to connect. On
anything but Windows it listens on a Unix socket inside its own staging
directory with `listen_addresses` empty — not on localhost, not on the compose
bridge, nowhere. A recovered cluster holds every business at an earlier moment,
so the right number of network interfaces for it is zero.

Stop it when finished:

```bash
pg_ctl -D /staging/pitr-<id>/data stop
```

### The other targets

| Target | What it means |
|---|---|
| `latest` | Replay everything the archive holds. The answer to losing the server. |
| `immediate` | Stop as soon as the copy is consistent. Proves a base backup is sound without waiting for a week of replay. |
| `time` | Stop at a moment, inclusive. |
| `before_time` | Stop strictly before a moment. |
| `lsn` | Stop at an exact log position, e.g. `1A/B2C30000`. |
| `name` | Stop at a restore point made earlier with `pg_create_restore_point`. |
| `xid` | Stop at a transaction id. |

`name` is the safest of them, because it names a moment somebody deliberately
marked rather than one read off a clock. Before a risky change:

```sql
SELECT pg_create_restore_point('before-the-price-import');
```

### Timelines

A recovery follows the **base backup's own timeline** by default, not
`latest`. PostgreSQL's default is `latest`, and `latest` is wrong for this:
after any previous recovery the archive may contain a newer timeline, and a
recovery aimed at last Tuesday would follow the branch created by the *last*
recovery and arrive somewhere nobody asked for. Naming the timeline makes the
result a function of the request. `-target ... ` with an explicit timeline
overrides it.

---

## Recovering to the latest available point

This is the "the server is gone" path, not the "undo a mistake" path.

```bash
docker compose run --rm backup pitr -target latest -keep
```

Replay runs until the archive supplies no more segments, then promotes. Note
what this gives you: **everything that was archived**, which is not the same as
everything that was written. Whatever was in the open segment when the machine
died was never shipped and is gone. That is the RPO, and it is stated below
rather than rounded to zero.

For a full server rebuild the order is: install the stack, restore the *latest
dump* to get a working database quickly, then decide whether the extra minutes
of a point-in-time recovery are worth having. `RECOVERY.md` has the full
sequence; `MIGRATION.md` has the planned-move version where a write freeze makes
the question moot.

---

## Missing WAL: troubleshooting

```bash
docker compose run --rm backup wal gaps
```

prints every timeline, every hole, and how far replay can actually reach.

A gap means one of four things:

**Retention took it.** The window in `RAWSYST_PITR_RETENTION_DAYS` was shortened,
or `RAWSYST_PITR_KEEP_BASE_BACKUPS` was lowered, and the horizon moved past
segments an older base backup still needed. The retention routine refuses to do
this — it deletes only what sits before the *oldest kept* base backup — so if it
has happened, something deleted objects outside this product.

**An upload did not finish.** The object is there and the sidecar is not.
Reported separately as `incomplete_uploads`, because it is a different failure
from a missing object and has a different fix: nothing. PostgreSQL will not
re-archive a segment it has already recycled. The window ends at the hole.

**The archive was off for a while.** `archive_mode` was off, or
`archive_command` was empty, and segments were recycled without being shipped.
`pg_stat_archiver.stats_reset` and the local PostgreSQL log say when.

**Something removed the object and left the sidecar.** The archiver writes the
object first, so this state cannot be produced by an interrupted upload. It is
reported as `orphaned_records` and is the most misleading state an archive can
be in, which is why it is named rather than counted as an ordinary miss.

In every case the honest response is the same: the recovery window now ends at
the segment before the hole. **Take a new base backup immediately** — that moves
the window start forward past the gap and restores a contiguous window from now
on.

---

## When the object store is down

This is the failure mode with a deliberate, uncomfortable design.

A segment that cannot be shipped makes `archive_command` exit non-zero.
PostgreSQL then **keeps** the segment and retries, for ever. The archive stays
whole and the disk fills up.

The alternative — an archive command that succeeded anyway — would let
PostgreSQL recycle segments that were never shipped, producing a green archive
with a hole in the middle that nobody discovers until a recovery. That is worse
and it is worse quietly, so this product takes the visible failure.

What that means operationally:

1. The dashboard goes red within a minute: *the most recent archive attempt
   failed and nothing has succeeded since*.
2. `pg_wal` grows. The readout watches it and goes amber at 2 GiB and red at
   4 GiB, well before the disk on the server this product is sized for matters.
3. When the store comes back, PostgreSQL ships the backlog on its own. Nothing
   needs to be done.

If the store will not come back and the disk is filling:

```bash
# What is actually happening.
docker compose run --rm backup wal status
docker compose logs db | tail -50

# The last resort, and it PERMANENTLY ends the recovery window at this point.
# Do this only with the decision written down somewhere.
docker compose exec db psql -U rawsyst -c "ALTER SYSTEM SET archive_mode = off"
docker compose restart db
```

Turning archiving off lets PostgreSQL recycle the backlog and saves the
database. It also ends point-in-time recovery from that moment until a new base
backup is taken with a working store. Take that base backup the moment the store
is back.

---

## Encryption, and recovering a key

The archive and the base backups are sealed with **the same key as the dumps**:
`RAWSYST_BACKUP_ENCRYPTION_KEY`. There is deliberately no second key — two keys
is two things to lose, and losing either loses half of the recovery.

```
  segment → gzip → AES-256-GCM → object store
```

Chunked GCM, 4 MiB of plaintext per chunk, with the chunk number, a per-stream
random prefix and a last-chunk marker all authenticated. See `crypt.go`: the
third of those is the one people leave out and the one that matters, because
truncation is exactly what a half-finished upload looks like.

**What is in the sidecar and the manifest:** the algorithm, the chunk size, and
a *fingerprint* of the key — a hash, sixteen hex characters, from which the key
cannot be recovered. Its whole job is to let a restore say *this was sealed with
key a1b2c3… and you have given me d4e5f6…* instead of *decryption failed*, which
is the difference between a five-minute fix and an afternoon.

**If the key is lost, the archive is lost.** Permanently, for every business on
the server. Nothing in this product can recover it and nothing in the object
store holds a copy. `SECRETS.md` says where it belongs: a password manager, and
a second copy somewhere that is not this server and not the same account as the
bucket.

**Rotating it** does not re-encrypt what is already there. Old segments stay
readable only with the old key, so a rotation means keeping both keys and taking
a fresh base backup immediately, after which the old key covers only history
outside the window. There is no command for this because it is a decision with
consequences, not a button.

If the key was never set, the archive is protected in transit by TLS and at rest
by whatever the provider does. That is a real level of protection and it is not
the same one; `BACKUP.md` says what turning encryption on commits you to.

---

## Production recovery

**A point-in-time recovery never replaces production.** There is no code path
from it to the live cluster. What it produces is an isolated PostgreSQL holding
the state at the chosen moment, which somebody then inspects.

Replacing production is a separate decision with its own gate, and it is the
existing one: `RECOVERY.md`, the restore-production flow, which requires

- `RAWSYST_ALLOW_PRODUCTION_RESTORE=true` in the server's environment, where
  nobody with a browser can change it;
- a **rehearsal that passed** on the snapshot being restored;
- a **fresh verified backup** of what production currently holds, taken and
  verified by the agent before it touches anything;
- **maintenance mode** — the write freeze — held across the cutover, or a sale
  rung up during the two renames lands in the database being renamed aside;
- the snapshot id **typed out** as the confirmation;
- the old database **renamed aside, never dropped**.

To put a point-in-time recovery in front of the business, the sequence is:

1. Recover to the moment, with `-keep`. Inspect it. Count what matters.
2. Take a `pg_dump` **of the recovered cluster**:
   ```bash
   pg_dump -Fc -d "<the -keep connection string>" -f /staging/recovered.pgdump
   ```
3. Upload that dump as an ordinary snapshot, verify it, rehearse it.
4. Use the ordinary production restore, which now has a verified snapshot, a
   rehearsal, a safety backup and a write freeze.

That looks indirect and it is the point: it routes a point-in-time recovery
through the one path that already has every safety control, rather than growing
a second path that would need all of them again.

Every recovery — including the refused ones — is written to `pitr_recovery` and
to the platform audit trail: who asked, which moment they chose, where replay
actually stopped.

---

## Rollback

**From a point-in-time recovery:** nothing to roll back. Production was never
touched. Stop the instance and delete the directory.

**From a production restore that used one:** the ordinary rollback, because it
went through the ordinary path.

```bash
docker compose run --rm backup rollback -from rawsyst_before_20260911T1432Z
```

The old database was renamed aside rather than dropped, which is what makes this
a command rather than a second recovery. `RECOVERY.md` has the full procedure and
the checks to run afterwards.

---

## RPO and RTO

Measured, not estimated. There are two drills and they measure different things.

**The Go drill** (`backend/internal/backup/pitr_test.go`) builds a cluster with
`initdb`, archives to an in-process object store, and recovers twice. It
measures the *logic*. Taken on a development machine, Windows, PostgreSQL 18, an
8 MiB cluster:

| | Measured |
|---|---|
| Archive lag | 201–404 ms from `pg_switch_wal()` to the object and its sidecar being in the store |
| Base backup | 1.8 s for 8 MiB → 3.5 MB stored, compressed and sealed |
| Recovery to a moment | 4 s total (0 s fetch, 2 s replay) |
| Recovery to the latest point | 3 s total |

**The container drill** (`deploy/server/pitr-drill.sh`) runs the real images
against MinIO with the whole committed migration chain applied, so the recovered
cluster is this product's actual schema. It measures the *deployment*:

| | Measured |
|---|---|
| Archive lag | under a second; the readout reported 1 segment and 0–6 s behind throughout |
| Base backup | under 1 s, 5.6 MB stored, encrypted |
| Recovery to a moment | 1 s total (0 s fetch, 1 s replay) |
| Recovery to the latest point | 1 s total |
| What came back | 191 tables, schema version 136, row-level security still **forced** on 181 of them, 7 sequences at their positions |

**Neither of these durations generalises to a real database.** Both clusters are
a few megabytes and both object stores are on the same machine. They are
recorded to show the shape rather than the size, and to be the baseline a real
measurement is compared against.

### The recovery point objective

**The honest number is `archive_timeout` plus the archive lag: about 61
seconds** with the shipped defaults, on a server whose store is reachable.

That is the bound on an idle or lightly-used server, which is the case that
matters: a busy database fills segments on its own in far less than a minute. It
is what can be lost if the machine is destroyed at an arbitrary instant, because
whatever is in the currently open segment has not been shipped.

This is **not zero data loss** and this product does not claim it. Zero would
require synchronous replication to a second machine, which is a different
architecture with a different bill and a latency cost on every commit at the
counter. What it is, is roughly **1,400 times better than the 24 hours the
dumps alone gave**.

Raising `POSTGRES_ARCHIVE_TIMEOUT` to 300 trades a five-minute window on quiet
nights for about a fifth of the idle disk writes. Lowering it below 60 buys
little: the archive lag is already a fraction of a second and the segment write
is 16 MiB either way.

### The recovery time objective

**Not yet measured on production-sized data, and that is stated rather than
estimated.** It is dominated by three things, none of which the drill exercises
at scale:

- downloading the base backup from the object store — the shop's connection,
  not the server;
- decompressing and unpacking it — the disk;
- replaying the segments since — roughly linear in how much was written.

The measurement belongs on the target server, against a real base backup, and it
is the first item in the pre-production plan in `PREPRODUCTION.md`. Until it is
taken, the honest statement is: **the mechanism is proved, the duration is not.**

The drill runs nightly against disposable resources (`rawsyst-drill.timer`), so
the duration on this installation's real data becomes a number that is tracked
rather than guessed at.

---

## Retention: base backups and WAL

They are related in one direction only. **Base backup retention decides WAL
retention. WAL retention decides nothing.**

```
  keep = the newest base backup taken BEFORE the window starts,
         plus everything newer,
         and never fewer than RAWSYST_PITR_KEEP_BASE_BACKUPS

  the WAL horizon = the START segment of the OLDEST kept base backup
  everything before that segment may be deleted; nothing at or after it may
```

The **oldest** kept base backup, not the newest. Deleting the log in front of
the newest one would silently shorten the window to a few hours while every
dashboard still said seven days.

One subtlety in the first line: a seven-day window needs the newest base backup
taken **before** seven days ago, not merely every one taken inside the window.
That older backup is what the far end of the window replays onto.

| Dial | Costs | Default |
|---|---|---|
| `RAWSYST_PITR_RETENTION_DAYS` | the WAL a shop generates in that time — well under a gigabyte a week compressed | 7 |
| `RAWSYST_PITR_KEEP_BASE_BACKUPS` | a compressed copy of the database each | 2 |
| `RAWSYST_PITR_MAX_BASE_BACKUPS` | — | 8 |

So the window is the cheap dial and the copies are the expensive one.

```bash
docker compose run --rm backup wal prune            # says what it would remove
docker compose run --rm backup wal prune -apply     # removes it
```

A dry run is the default, which is the opposite of the convention elsewhere in
this product and is deliberate: this is the only routine that removes the last
copy of something. It refuses outright — removing nothing — when it cannot
establish a horizon: no completed base backup, a manifest it cannot read, a
listing that came back short. The cost of keeping a segment too long is a
fraction of a penny.

**Timeline history files are never deleted.** A few hundred bytes each, one per
promotion, and without them a recovery cannot work out which branch a segment
belongs to.

**Orphans** — segments on a timeline no retained base backup sits on — are
reported separately and removed only once they are outside the window. They
usually mean a recovery that was promoted and then abandoned, or two clusters
sharing a prefix, and both are worth an operator knowing about.

---

## Monitoring

```bash
docker compose run --rm backup wal status     # exits non-zero unless GREEN
docker compose run --rm backup wal verify     # listing and small reads
docker compose run --rm backup wal verify -deep 16   # downloads and decrypts 16
docker compose run --rm backup wal preflight  # can this server archive at all
```

The agent writes a reading to `wal_archive_state` once a minute and the website
reads that row. Everything in it is either straight from `pg_stat_archiver`,
which PostgreSQL maintains itself, or from a listing of the bucket.

| | Where it comes from |
|---|---|
| Last archived segment, archived count, failed count | `pg_stat_archiver` |
| Archive lag, in segments and seconds | current segment vs last archived |
| Local `pg_wal` size | `pg_ls_waldir()`, needs `pg_monitor` |
| Segments in the archive, bytes, gaps | one listing of the bucket |
| Object store reachable | whether that listing answered |
| Recovery window start and end | base backups + the contiguous run |

Green means something specific: **a recovery to a moment inside the window would
work**, as far as anything short of performing one can say. Archiving is on, the
last segment arrived recently, the store answered, there are no gaps, and a base
backup exists to replay onto. Anything less is amber or red with a sentence
saying which of those is missing.

A reading older than fifteen minutes is marked **stale** rather than shown as
current. A green from six hours ago describes a machine that may have stopped
five hours ago.

---

## Server migration

`MIGRATION.md` covers the planned move and is unchanged: a write freeze, a final
dump, restore on the new server. Point-in-time recovery adds one thing to it and
removes one worry.

**Adds:** the new server needs its own base backup as soon as it is up. Until it
has one, the archive it inherits is the *old* server's and nothing on the new
one can be replayed onto it.

**Removes:** the "what was written after the final backup" worry, if the old
server is still reachable. Archive its last segment, and the new server can be
recovered to the second the old one stopped rather than to the final dump.

Give the new server a **different `RAWSYST_BACKUP_PREFIX`** during a migration
if both are running. Two clusters archiving into one prefix produce segment names
that collide, and the archiver refuses the second one loudly rather than
overwriting — which is correct, and is an alarm nobody wants during a migration.

---

## Emergency incident procedure

Somebody has deleted, overwritten or corrupted data and it is in production now.

1. **Stop the bleeding.** Turn maintenance mode on:
   `PUT /api/v1/platform/maintenance` with `active: true`, or the toggle on the
   Backup & Recovery screen. Writes stop; reads and platform operators continue.
   Every minute of continued writing is a minute of work that a recovery will
   throw away.

2. **Establish when.** The audit trail is the fastest route: Platform →
   Oversight, filtered to the entity. Write the timestamp down.

3. **Check the window before anything else.**
   ```bash
   docker compose run --rm backup pitr -window
   ```
   If the moment is outside it, stop and read *Missing WAL* above. Nothing else
   in this list will help.

4. **Recover to just before it, in isolation.**
   ```bash
   docker compose run --rm backup pitr \
     -target before_time -at <the moment> -keep
   ```
   This does not touch production. It takes minutes to hours.

5. **Look at the result.** Connect with the printed connection string. Confirm
   the data is there and that nothing *after* the incident that mattered has
   been lost — a recovery to before the mistake also undoes every legitimate
   sale since.

6. **Decide, with that in hand.** The two real options are usually:
   - **Repair in place.** Export just the affected rows from the recovered
     cluster and put them back into production with a script. Nothing else is
     lost. This is almost always the right answer for a deletion.
   - **Replace production.** Everything since the incident is lost. Follow
     *Production recovery* above, which routes through the existing gates.

7. **Turn maintenance mode off**, and write down what happened, when it was
   noticed, what was chosen and what it cost. That record is what makes the next
   one shorter.

---

## What is not automated, and why

**The key.** Not on this server, not recoverable by anything here. That is the
arrangement, not a gap.

**The decision to recover.** Every destructive operation needs a person, and
this document is what that person reads.

**The duration on real data.** Measured on the target server before it is
trusted, not estimated here. See *RPO and RTO*.

**Proving a person can do it.** The drill proves the software recovers. It does
not prove somebody who has never done it can bring a business back under
pressure. Walk this document on a spare machine once, before you need it.
