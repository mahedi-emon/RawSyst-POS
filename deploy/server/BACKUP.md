# Backups

The database is the only thing on this server that cannot be rebuilt from the
repository. Everything else — images, containers, configuration, the front end —
is a `git clone` and a `docker compose build` away.

So this document is about one file and what it takes to trust it.

---

## What is in a backup, and what is not

On the profile this server runs, **the database is the whole of the persistent
business data**. Documents, logos and receipts are stored in Postgres unless
`docker-compose.files.yml` is layered on, which is correct for a shop and does
not scale. One dump therefore carries:

companies and tenants · users, roles and permissions · products, variants and
inventory · customers and suppliers · orders, invoices and payments ·
accounting, journals and periods · payroll and end-of-service · the regulatory
registry **with the source documents it was read out of** · the audit trail ·
the platform's own records.

Not in a backup, deliberately: images, containers, build caches, `node_modules`,
`.next`, the Go module cache, test artefacts. All reproducible, all large, none
of it business data.

**Not in a backup, and this one matters: secrets.** `.env` is not backed up.
Neither is the database password, the JWT signing secret, the object-storage
credentials or any device key. See *Secrets* below for what to do instead and
why.

---

## Where backups go

**Not on this server.** A backup on the same disk as the database survives a
mistake and does not survive a disk, a VM, or the day somebody rebuilds the
host. `rawsyst backup run` refuses to start without an object store configured
rather than quietly writing to local disk, because a file on the same machine is
not a backup and calling it one is worse than having none.

Any S3-compatible store: AWS S3, Backblaze B2, Cloudflare R2, Wasabi, Hetzner,
a MinIO on a machine somewhere else. Configured entirely through environment:

```
RAWSYST_S3_ENDPOINT=https://s3.eu-central-1.amazonaws.com
RAWSYST_S3_BUCKET=rawsyst-backups
RAWSYST_S3_REGION=eu-central-1
RAWSYST_S3_ACCESS_KEY_ID=…
RAWSYST_S3_SECRET_ACCESS_KEY=…
RAWSYST_S3_PATH_STYLE=true          # MinIO and most non-AWS stores
RAWSYST_BACKUP_PREFIX=rawsyst       # namespace inside the bucket
```

`https` is required outside development, and configuration refuses to start
otherwise. Turn on the bucket's own server-side encryption and its object-lock
or versioning if it has them: an attacker who reaches this server should not be
able to delete the backups from it. Give the credentials write and list on that
prefix and nothing else.

Nothing above is committed. `.env` holds them, is not in git, and is `chmod
600`.

---

## What a snapshot looks like

```
rawsyst/20260910T130047Z-1/
    database.dump      pg_dump custom format, compressed
    manifest.json      what it is, how big, what it hashes to
    COMPLETED          written last, and only if everything before it worked
```

The marker is the point. A dump that uploaded and a manifest that did not is a
snapshot that would restore into a database with no idea what it is, so a
restore refuses anything without a marker. That is what stops "the upload
returned 200" from being mistaken for "there is a backup".

The manifest records the snapshot id, when it was taken, the build that took it,
the schema version, the database name and size, the table count, the row counts
of ten tables that matter, and the dump's length and SHA-256. It records no
secret of any kind, and a test fails the build if a field is ever added whose
name suggests one.

---

## Commands

All of them through compose, so they inherit the same configuration the
application has:

```bash
cd /opt/rawsyst
C="docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup"

$C run --rm backup run       # take one
$C run --rm backup list      # what is in the store
$C run --rm backup verify    # prove the newest one restores
$C run --rm backup prune     # remove what is outside the policy
```

`verify` and `restore` take `-snapshot ID`; without it they use the newest
completed one. `prune` takes `-dry-run`.

The backup image is `postgres:17-alpine` with the RawSyst binary added — about
21 MB on top of an image the host already has, because `pg_dump` and
`pg_restore` are the right tools and taking them from the same image as the
database is what guarantees the versions match.

---

## Verification, which is the whole point

**A backup is not working because a file was created.** It is working when it
has been restored and the restored data is usable. `backup verify` does exactly
that, and it is a different command from `run` on purpose:

1. the completion marker exists
2. the manifest parses and is a version this build reads
3. the object is there and is the length the manifest says
4. it is downloaded and hashes to what the manifest recorded
5. **it is restored into a temporary database beside the real one**
6. the restored schema version matches
7. the restored table count matches
8. the restored row counts match, table by table
9. the temporary database is dropped

Production is never touched. The temporary database is named from the snapshot
id, created and dropped by the same command, and dropped even when a step fails.

Observed on this build: a single byte flipped in the stored dump, leaving its
length unchanged, is caught at step 4 and the command exits non-zero with

```
The snapshot does not match its manifest: expected 38ece40f…, the store
returned 6919a1b1…. Do not restore this.
```

Verification needs `RAWSYST_BACKUP_ADMIN_DSN`: a connection to a **different**
database on the same server, because a database cannot be created from inside
the one being created beside it. The compose file defaults it to `postgres` on
the same instance.

---

## Schedule

Daily, at 03:30, as a systemd timer. Not a resident container: a scheduler that
has to be running, watched and kept in memory to work for ninety seconds a day
is a poor trade on 3.7 GiB, and systemd already reports what failed.

```ini
# /etc/systemd/system/rawsyst-backup.service
[Unit]
Description=RawSyst backup
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
WorkingDirectory=/opt/rawsyst
TimeoutStartSec=45min
ExecStart=/usr/bin/docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup run --rm backup run
ExecStartPost=/usr/bin/docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup run --rm backup verify
ExecStartPost=/usr/bin/docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup run --rm backup prune
```

```ini
# /etc/systemd/system/rawsyst-backup.timer
[Unit]
Description=RawSyst backup, daily

[Timer]
OnCalendar=*-*-* 03:30:00
RandomizedDelaySec=10min
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
sudo systemctl enable --now rawsyst-backup.timer
systemctl list-timers rawsyst-backup.timer
journalctl -u rawsyst-backup.service --since '2 days ago'
```

`Persistent=true` runs a missed backup when the machine comes back. Verify runs
immediately after the backup, so the daily answer is not "a file was written"
but "a file was written and it restores". Prune runs last, and only after a
verified backup exists.

`RandomizedDelaySec` keeps the job off the same second as every other host with
the same crontab, which matters when the object store is shared.

**Failures are visible.** `ExecStartPost` failing fails the unit, the unit shows
in `systemctl --failed`, and `deploy/server/rawsyst-check.sh` reports it hourly.
A backup that fails silently is worse than one that never ran.

---

## Retention

```
RAWSYST_BACKUP_KEEP_DAILY=7
RAWSYST_BACKUP_KEEP_WEEKLY=4
RAWSYST_BACKUP_KEEP_MONTHLY=3
```

Seven days, four weeks, three months, and **always the newest completed
snapshot, whatever the policy says**. A retention rule that can empty the store
is a retention rule that will, and the day it does is the day somebody needs it.

Two further refusals, both deliberate:

- If no completed snapshot can be found, nothing is deleted at all. An empty or
  unreadable listing is a reason to stop, not a reason to start removing
  backups.
- A snapshot whose id this build cannot date is kept. Deleting something because
  it is not understood is how a bug becomes data loss.

`make cleanup` and `scripts/maintenance.sh` never touch the object store. They
clean local caches and Docker's reclaimable space; a low local disk is not a
reason to delete a remote backup.

---

## Secrets

**Secrets are not in backups.** Not the database password, not
`RAWSYST_JWT_SECRET`, not the object-storage credentials, not device keys.

The reason is that a backup is read by whoever can list the bucket, and a backup
that carries the credentials to the system it backs up turns one leaked object
into a full compromise. Restoring a database from a stolen dump is bad;
restoring it and being able to sign valid sessions against the running system is
worse.

So they travel separately, by hand, once:

1. Keep `.env` in a password manager, or in whatever secret store the
   organisation already has.
2. On a new server, write it before the first `docker compose up`.
3. `chmod 600 .env`. It is not in git and must not go in.

`RAWSYST_JWT_SECRET` is the one to think about at migration time. Restoring the
database onto a new server with a **different** JWT secret is safe and signs
everybody out; with the **same** secret, existing sessions keep working across
the move. Either is defensible — the first is tidier, the second is kinder — and
the choice belongs to whoever is doing the migration rather than to this file.

---

## Restoring

Onto a database that already exists and is **empty**:

```bash
$C run --rm backup restore -snapshot 20260910T130047Z-1 \
  -into 'postgres://rawsyst:…@db:5432/rawsyst_restored?sslmode=disable'
```

It refuses a target that already holds tables. A restore goes beside the live
database and the application is switched to it, so that if anything goes wrong
the database you have is the database you had.

Then run the migrator against it. A snapshot carries the schema it was taken at;
the migrator applies whatever the current build added since, and does nothing if
there is nothing to add.

The whole new-server sequence is `deploy/server/MIGRATION.md`.

---

## What this does not do, and what to do about it

**Point-in-time recovery.** A daily logical dump means the worst case is losing
up to a day of work — the recovery point objective is 24 hours, and after a
restore it is however long ago the last backup ran. Continuous WAL archiving
would take that to minutes. `wal_level=replica` is already set for it, and the
archive command belongs to whatever ships the segments off the box. It is not
configured here because it needs somewhere to ship to and a decision about cost,
and pretending otherwise would overstate what this protects against.

**Client-side encryption.** Transport is TLS and the store's own server-side
encryption should be on. The dump is not separately encrypted before upload,
which means the storage provider can read it. For a shop's own bucket that is
usually the right trade; where it is not, `pg_dump | age -r …` before upload is
the shape of the answer, and it needs a key-management decision this file cannot
make for you.

**Restore drills.** `verify` proves the dump restores, every night. It does not
prove that a person who has never done it can bring the business back up under
pressure. Walk `MIGRATION.md` on a spare machine once, before you need it.
