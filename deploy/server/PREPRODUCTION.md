# Pre-production validation

What to prove on the real server **before** it carries a business, and before
anything is switched to it.

Every step here is safe on a server that is not yet serving: nothing in this
document modifies a live database, and the two steps that could are marked and
come last. Run it top to bottom on the machine itself. Stop at the first thing
that does not do what it says.

Its companions: [RUNBOOK.md](RUNBOOK.md) builds the machine,
[BACKUP.md](BACKUP.md) explains the backup system,
[PITR.md](PITR.md) explains continuous archiving and recovery to a moment,
[SECRETS.md](SECRETS.md) is what a dump cannot carry, and
[RECOVERY.md](RECOVERY.md) is what to do when something has gone wrong.

---

## Before you start

- [ ] The machine is built to `RUNBOOK.md` sections 1 to 5.
- [ ] `.env` is written from the secret store, `chmod 600`, and `git status`
      does not list it.
- [ ] The object store exists, is **not** on this server, and its credentials
      are in `.env`.
- [ ] You have decided about encryption. See *the two decisions* in
      `SECRETS.md`. If it is going on, the key is generated and stored in two
      places **before** step 3 below.

Throughout:

```bash
cd /opt/biz1core
C="docker compose -f docker-compose.yml -f docker-compose.server.yml"
B="$C --profile backup run --rm backup"
```

---

## 1. The application role must not be able to read past row-level security

This is the check that matters most and takes ten seconds. A server where the
application's role holds `BYPASSRLS` or `SUPERUSER` has **no tenant isolation**:
one business can read another's books and no policy in the database will stop
it.

```bash
$C exec db psql -U postgres -tAc \
  "SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_setting('biz1core.appuser', true) OR rolname = 'rawsyst';"
```

- [ ] The application's role prints `f | f`.

If it prints `t` for either, **stop**. On the compose stack the container's
`POSTGRES_USER` is created as a superuser by default, which is convenient for
development and wrong for a deployment. Fix it before going further:

```sql
ALTER ROLE biz1core NOSUPERUSER NOBYPASSRLS;
```

`backup role` in the next step refuses to run at all while this is true, and
that refusal is deliberate.

---

## 2. Create the backup role, and prove it can read

```bash
RAWSYST_BACKUP_ROLE_PASSWORD="$(openssl rand -base64 24)" $B role
```

- [ ] It ends with `checked  the role can read every table and sequence`.
- [ ] Put the same password into `RAWSYST_BACKUP_DSN` in `.env`, as
      `postgres://rawsyst_backup:<password>@db:5432/<database>?sslmode=disable`.
- [ ] Store that password in the secret store alongside everything else.
- [ ] Re-run `$B role`. It must say `unchanged` and check green again. An
      operation that is not safe to repeat is not safe to put in a deploy.

Why a separate role at all: row-level security is forced on ~177 tables, which
applies to the table owner too. `pg_dump` turns row security off to dump every
row, and Postgres refuses that to any role without `BYPASSRLS`. So the
application cannot back itself up, and must never be given the attribute that
would let it.

Proof that the problem is real, if you want to see it fail first:

```bash
# As the APPLICATION role. Expect: query would be affected by row-level
# security policy for table "account"
$C exec db pg_dump -U rawsyst -d rawsyst -Fc -f /tmp/should-fail.dump
```

---

## 3. A backup, by hand, watched

```bash
$B run
```

- [ ] It prints a snapshot id, a size, a sha-256 and `businesses N`.
- [ ] If encryption is on, it prints `encrypted  yes, key <fingerprint>` and
      that fingerprint matches the key you stored.
- [ ] It says NOT yet verified. That is correct and is the next step.

If it refuses for want of disk space, that is the guard working: a dump is
staged on the database's own volume and one that fills it stops Postgres
writing. Free space rather than lowering the threshold.

---

## 4. It is really in the object store

```bash
$B list
```

- [ ] The snapshot is listed as `COMPLETE`.

And from outside the product, so that a bug in it cannot be what says yes —
use whatever client the provider gives, or `mc`:

```bash
mc ls --recursive <alias>/<bucket>/biz1core/<snapshot-id>/
```

- [ ] Three objects: `database.dump`, `manifest.json`, `COMPLETED`.
- [ ] The dump's size matches what step 3 printed.

---

## 5. It restores

This is the only step that turns a file into a backup.

```bash
$B verify
```

- [ ] Thirteen checks, all `ok`, ending `VERIFIED`.
- [ ] It reports the table count, the row count, and `businesses N` matching
      what you expect.
- [ ] It says production was not touched. It restored into a temporary
      database and dropped it.

Record how long it took. That is your **RTO for the database** on this
hardware, and it is the number to quote rather than any number in a document.

---

## 6. A person can carry it away

Disaster recovery fails on the day the bucket is also gone.

```bash
$B download -to /tmp/drill
sha256sum -c /tmp/drill/*.sha256
$B check -dump /tmp/drill/*.dump -manifest /tmp/drill/*.manifest.json
```

- [ ] Three files come down.
- [ ] The checksum file verifies with the ordinary system tool, with no part
      of this product involved.
- [ ] `check` says the three files agree.

Copy them somewhere off the server. That is the copy that survives the account
being closed.

---

## 7. The website agrees with the command line

Sign in as the platform operator.

- [ ] **Platform → Backup & Recovery** shows the snapshot as verified.
- [ ] Health is GREEN.
- [ ] Sign in as a business owner: every backup screen must 404. Not 403 — the
      routes do not confirm they exist.

---

## 8. The timers

```bash
sudo cp deploy/server/biz1core-backup.{service,timer} /etc/systemd/system/
sudo cp deploy/server/biz1core-drill.{service,timer}  /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now biz1core-backup.timer biz1core-drill.timer
systemctl list-timers 'biz1core-*'
```

- [ ] Both timers are listed with a next elapse.
- [ ] The next elapse is in the timezone you expect. `timedatectl` says what
      the machine thinks it is.

Then force one, rather than waiting for 03:30:

```bash
sudo systemctl start biz1core-backup.service
journalctl -u biz1core-backup.service -n 50 --no-pager
```

- [ ] It ran, verified and pruned, in that order.
- [ ] `$B health` is GREEN.

---

## 9. The application-level drill

**Do this before the server carries anything, because it is the only step that
proves the product works on a restored database rather than that the file
restores.**

On a spare machine, or on this one before it is switched to:

1. Restore the verified snapshot into a database **beside** the live one, as
   the **application's** role, never as an administrator:

   ```bash
   $C exec db psql -U postgres -c 'CREATE DATABASE rawsyst_drill OWNER rawsyst'
   $B restore -into 'postgres://rawsyst:<password>@db:5432/rawsyst_drill?sslmode=disable'
   ```

   Restoring as an administrator produces an intact database the product
   cannot read a row of, because `pg_restore` leaves objects owned by whoever
   connected. It fails at the first query with `permission denied for table
   schema_migration`.

2. Point a spare API at it and check, at minimum:

   - [ ] Sign in, as a business owner and as a platform operator.
   - [ ] A wrong password is still refused.
   - [ ] Products, customers, suppliers, stock on hand, orders, purchase
         orders, stock movements, journals and documents all read.
   - [ ] Profit and loss, balance sheet and the dashboard all compute.
   - [ ] **A second business cannot see the first one's data** by putting the
         first one's company id in the request.
   - [ ] Signed out reads nothing.
   - [ ] A company logo comes back — proof that files survived, since every
         file in this product is a column.
   - [ ] A write succeeds.
   - [ ] The worker starts against it.
   - [ ] **A new backup can be taken from the restored database, and
         verifies.** A business that recovers into being unable to protect
         itself has not recovered.

3. Drop the drill database.

Record the total elapsed time from "decide to restore" to "application
serving". That is your **RTO for the system**, and it is a bigger number than
the database restore alone.

---

## 9b. Point-in-time recovery, on this server's real data

For a server that is being built now, the steps below are enough and belong in
this order. For a server that is **already running and trading**, use
[PITR-ACTIVATION.md](PITR-ACTIVATION.md) instead: it does the same work in an
order that cannot fill the disk of a machine somebody is depending on, with a
write freeze around each restart and a rollback for each step.

`PITR.md` is the reference; this is the checklist, and it is the one that turns
the measured numbers in that document from "on a laptop" into "on this machine".
Everything here is safe: nothing below opens the live database for writing.

- [ ] **Preflight.** One command says whether this server is configured to
      archive and to recover at all.
      ```bash
      $C run --rm backup wal preflight
      ```
      Every line must say `ok`. The two that fail on a fresh server are
      `archive_mode` (set `POSTGRES_ARCHIVE_MODE=on` and restart `db`) and the
      replication line in `pg_hba.conf` (the db image adds it on every start —
      if it is missing, the image is stale, so rebuild it).

- [ ] **Segments are actually arriving.** Force one and watch it land.
      ```bash
      $C exec db psql -U rawsyst -c "SELECT pg_switch_wal()"
      sleep 5
      $C run --rm backup wal status
      ```
      `last archived` must name a segment and `last archived at` must be
      seconds ago. **Write down the archive lag.** That number plus
      `POSTGRES_ARCHIVE_TIMEOUT` is this installation's recovery point.

- [ ] **A base backup, watched.** This is the one that takes real time on real
      data.
      ```bash
      time $C run --rm backup basebackup
      ```
      **Write down the duration and the stored size.** Confirm it says
      `encrypted true` if a key is configured; if it says false and you expected
      true, stop and fix the key before anything else.

- [ ] **It recovers.** The cheapest proof, replaying only the log the backup
      carries.
      ```bash
      time $C run --rm backup pitr -target immediate
      ```
      Must end `PASSED`, and must report the table count and schema version this
      server actually has.

- [ ] **The window is real.** After the base backup and a few minutes of
      archiving:
      ```bash
      $C run --rm backup pitr -window
      $C run --rm backup wal verify -deep 8
      ```
      The window must span from the base backup to within a minute or two of
      now, with no gaps, and the deep check must pass — it downloads and
      decrypts eight segments, which is the only check that proves the bytes are
      readable.

- [ ] **Recovery to a moment, with a stopwatch.** Pick a moment a few minutes
      ago, inside the window.
      ```bash
      time $C run --rm backup pitr -target before_time -at <moment> -keep
      ```
      **Write down the total, the fetch time and the replay time.** Connect with
      the printed connection string, count something, then stop it:
      ```bash
      $C run --rm backup -- pg_ctl -D <the printed data directory> stop
      ```
      This is the **RTO for a point-in-time recovery on this server**, and it is
      the number `PITR.md` says is not yet measured. Once you have it, put it
      there.

- [ ] **Retention says something sensible.**
      ```bash
      $C run --rm backup wal prune
      ```
      On a server with one base backup it will report a horizon and remove
      nothing, which is correct. It must not say `REFUSED`.

- [ ] **The website agrees.** Sign in as the platform operator, open
      **Backup & Recovery → Recovery**. The health word, the window and the base
      backup must match what the command line just said. If the screen says
      "stale", the agent is not running.

---

## 10. The last two, and only when the rest passed

- [ ] `RAWSYST_ALLOW_PRODUCTION_RESTORE` stays `false` until there is a reason.
      Turning it on is not a prerequisite for going live; it is a decision to
      take when a recovery is actually needed, and `RECOVERY.md` is what to
      read first.
- [ ] Take one more backup, after the final configuration, and verify it. The
      snapshot the business starts from should be one taken from the server as
      it will actually run.

---

## What this cannot tell you

It cannot tell you that a **person** can bring the business back under
pressure, at night, without this document open in front of them. Only walking
`RECOVERY.md` on a spare machine with a stopwatch does that, and until somebody
has, "we have backups" is a statement about files rather than about the
business.

It also cannot shorten the recovery point on its own. What shortens it is
continuous archiving, which section 9b above validates: with it on and a base
backup taken, the window is about a minute rather than a day. With it off, up to
a day of trading is on this server only. See *Recovery point and recovery time*
in `BACKUP.md` and *RPO and RTO* in `PITR.md`, which state the window
rather than softening it.
