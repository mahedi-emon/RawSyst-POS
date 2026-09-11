# Backups

The database is the only thing on this server that cannot be rebuilt from the
repository. Everything else — images, containers, configuration, the front end —
is a `git clone` and a `docker compose build` away.

So this document is about one file and what it takes to trust it.

Its companions: **[PREPRODUCTION.md](PREPRODUCTION.md)** is what to prove on the
real server before it carries a business, **[RECOVERY.md](RECOVERY.md)** is what
to do when something has gone wrong, **[MIGRATION.md](MIGRATION.md)** is moving
to another server without losing a day's trading, and
**[SECRETS.md](SECRETS.md)** is everything a dump does not contain — which is
the other half of being able to rebuild this server.

---

## The one sentence

**A backup that ran is not a backup that restores.**

Everything below follows from that. `backup_record` has kept the two claims in
separate columns since migration 0093 — `status` says a run finished,
`verified_at` says somebody proved it comes back — and the screen, the health
check and the retention policy all read the second one. The first is a more
comforting number and a less true one.

A snapshot is **VERIFIED** only after all of this:

```
dump  →  upload  →  manifest  →  COMPLETED marker
      →  download  →  size  →  checksum  →  decrypt
      →  restore into a temporary database
      →  schema version  →  every table by name  →  every row count
      →  every business's totals  →  every company's totals
      →  sequences  →  extensions  →  every RLS policy by name
      →  indexes, keys, constraints, functions, triggers
```

Nothing short of that is shown in green anywhere in this product.

---

## What is in a backup, and what is not

**The database is the whole of the persistent business data**, on every profile,
and that was checked rather than assumed. Every file this product stores is a
`bytea` column: `document.bytes` (migration 0096), `company_logo.bytes` (0054)
and `regulatory_source_document.content` (0133). The services that write them —
`internal/docs`, `internal/branding` — write SQL and nothing else.

`docker-compose.files.yml` starts a MinIO container and **does not move a single
document into it**: the API opens the object store, pings it, logs that it
connected, and then hands it to nothing except the backup. An earlier version of
this document said documents moved out of Postgres when that overlay was
layered on. That was never true, and it was a dangerous thing to believe in one
specific way — an operator who thinks documents live in the bucket may conclude
that losing the database loses only rows, or that the bucket needs a backup of
its own.

So the object store on this deployment holds **backups, and nothing else**. When
files do move out of Postgres, that changes: on that day the bucket stops being
only somewhere backups are kept and becomes something that needs backing up too,
and this section is where it has to be written down.

One dump therefore carries:

companies and tenants · users, roles and permissions · branches, stores and
warehouses · products, variants, categories, brands, units and barcodes ·
inventory, stock movements, adjustments, transfers and batches · purchases,
suppliers and supplier payments · sales, POS transactions, orders, invoices,
returns, refunds and delivery · customers, customer payments, loyalty and
wallets · expenses, income, cash, bank and treasury · accounting, journals,
ledgers and periods · payroll, employees and end-of-service · tax and the
regulatory registry **with the source documents it was read out of** ·
approvals, notifications, settings and integrations · the audit trail · the
platform's own records — subscriptions, feature flags, market configuration.

It is a **whole-database** dump. Nothing in this product maintains a list of
tables to back up: `pg_dump` takes the schema, and the manifest describes what
it took by asking the catalogue. A table added by a migration next month is in
the backup and in the verification without anybody remembering to add it, which
is the only arrangement that is still true a year from now.

Not in a backup, deliberately: images, containers, build caches, `node_modules`,
`.next`, the Go module cache, test artefacts. All reproducible, all large, none
of it business data.

**Not in a backup, and this one matters: secrets.** `.env` is not backed up.
Neither is the database password, the JWT signing secret, the object-storage
credentials or any device key. See *Secrets* below for what to do instead and
why.

---

## Before the first backup: the backup role

This is the step most likely to be skipped and the one that stops everything.

RawSyst forces row-level security on every tenant table. Forcing it means it
applies to the table's **owner** as well — that is the point, and it is what
keeps one business out of another's books. `pg_dump` turns row security off so
that it dumps every row, and PostgreSQL refuses that to any role which is not a
superuser and does not have `BYPASSRLS`.

So the application's role **cannot** take a backup, and **must not** be given
the attribute that would let it. Give the backup its own role.

### Do it with the product

```bash
RAWSYST_BACKUP_ROLE_PASSWORD="$(openssl rand -base64 24)" \
  $C run --rm backup role
```

That is the whole step. It is idempotent, so run it on every deploy; it repairs
an existing role rather than replacing one, and it never rotates a password
nobody asked it to rotate. `-dry-run` prints the statements and changes nothing.

Three things it will not do, and each is a refusal rather than a warning:

* It refuses to operate on the **application's own role**, whatever it is asked.
* It refuses to continue if the application's role **already** holds `BYPASSRLS`
  or `SUPERUSER`, because a server in that state has no tenant isolation and
  saying so is more urgent than finishing the job. (This is why the compose
  stack, where `POSTGRES_USER` is the container's superuser, is not a shape to
  run a real deployment in.)
* It refuses a role name that is not a plain identifier.

It ends by connecting **as the new role** and running the same check `backup
run` runs, so a green result means a backup would succeed rather than that some
SQL did not error. Then put the same password in `RAWSYST_BACKUP_DSN`.

Proved rather than asserted, on a database with 177 force-RLS tables owned by a
`NOSUPERUSER NOBYPASSRLS` role: `pg_dump` as the application role fails at
`query would be affected by row-level security policy for table "account"`; the
same dump as `rawsyst_backup` succeeds.

### Or by hand, if you would rather

```sql
-- As a superuser, connected to the RawSyst database.
CREATE ROLE rawsyst_backup LOGIN PASSWORD '…' BYPASSRLS
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;

GRANT CONNECT ON DATABASE rawsyst TO rawsyst_backup;
GRANT USAGE  ON SCHEMA public     TO rawsyst_backup;
GRANT SELECT ON ALL TABLES    IN SCHEMA public TO rawsyst_backup;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO rawsyst_backup;

-- The half that keeps it true. Without these two, the first table a future
-- migration adds is one the backup role cannot read.
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES    TO rawsyst_backup;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON SEQUENCES TO rawsyst_backup;
```

Then, in `.env`:

```
RAWSYST_BACKUP_DSN=postgres://rawsyst_backup:…@db:5432/rawsyst?sslmode=disable
```

`backup run` checks all of this **before** it spends a minute dumping, and names
what is wrong. Without the check the failure is a PostgreSQL message about a
policy on whichever table sorts first alphabetically, which is a true statement
about the twentieth thing anybody would look at.

A restore puts these grants back automatically — `grantBackupRole` in
`internal/backup/restore.go` — because a restore creates new objects and every
grant on the old ones goes with them. A business that recovered into being
unprotected is the shape of failure that finding this cost one cutover.

---

## Where backups go

**Not on this server.** A backup on the same disk as the database survives a
mistake and does not survive a disk, a VM, or the day somebody rebuilds the
host. `rawsyst backup run` refuses to start without an object store configured
rather than quietly writing to local disk, because a file on the same machine is
not a backup and calling it one is worse than having none.

Any S3-compatible store: Cloudflare R2, AWS S3, Backblaze B2, Wasabi, Hetzner,
a MinIO on a machine somewhere else. Configured entirely through environment:

```
RAWSYST_S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com
RAWSYST_S3_BUCKET=rawsyst-backups
RAWSYST_S3_REGION=auto                # R2 ignores it; keep it set
RAWSYST_S3_ACCESS_KEY_ID=…
RAWSYST_S3_SECRET_ACCESS_KEY=…
RAWSYST_S3_PATH_STYLE=true            # R2, MinIO, most non-AWS stores
RAWSYST_BACKUP_PREFIX=rawsyst         # namespace inside the bucket
```

`https` is required outside development, and configuration refuses to start
otherwise. Turn on the bucket's own server-side encryption and its object-lock
or versioning if it has them: an attacker who reaches this server should not be
able to delete the backups from it. Give the credentials write, read and list on
that prefix and nothing else.

Nothing above is committed. `.env` holds them, is not in git, and is `chmod
600`.

### On Cloudflare R2 in particular

R2's free allowance is a **billing** arrangement and not an application limit.
Nothing in this product knows about it, checks it, or behaves differently when
it is exceeded. Backups keep working and the bill starts. Watch the bucket's
size in Cloudflare's own dashboard and set the retention numbers below to the
size you are willing to pay for; that is the only lever, and it is a policy
decision rather than a code one.

Two R2 specifics worth knowing: the region is `auto` and is ignored, and R2
requires path-style addressing, which is why `RAWSYST_S3_PATH_STYLE=true` is
the default here.

---

## What a snapshot looks like

```
rawsyst/20260910T130047Z-1/
    database.dump      pg_dump custom format, compressed, optionally sealed
    manifest.json      what it is, how big, what it should contain
    COMPLETED          written last, and only if everything before it worked
```

The marker is the point. A dump that uploaded and a manifest that did not is a
snapshot that would restore into a database with no idea what it is, so a
restore refuses anything without a marker. That is what stops "the upload
returned 200" from being mistaken for "there is a backup".

### The manifest

Version 2. It records:

the snapshot id · when the run started and when it finished · the build that
took it and its git commit · the environment and the host · the schema version
from the migration ledger · the database name, size and PostgreSQL version · the
dump's length, SHA-256 and format · the encryption block when there is one ·
where it went, as a bucket, a prefix, an object key and an endpoint **host** ·
the retention class · and an inventory.

The inventory is what makes a verification mean something:

* every base table, by name, with its exact row count;
* every business's total row count and every company's, so a restore that
  brought one shop back and lost another is caught by a number rather than by
  that shop ringing up;
* every sequence and where it stands — a database whose sequences came back at 1
  issues a duplicate invoice number on its first write, quietly;
* every extension by name and version;
* every row-level-security policy by name, and how many tables force it;
* the counts of indexes, primary keys, foreign keys, unique constraints, check
  constraints, functions and triggers.

It records **no secret of any kind**, and a test fails the build if a field is
ever added whose name suggests one.

### One instant, for the dump and for the numbers

The manifest's numbers and the dump's bytes describe the same moment. A
repeatable-read transaction exports a snapshot, `pg_dump --snapshot` uses it,
and the inventory is taken inside that same transaction.

This used to be two reads on two connections, and every sale rung up between
them made the manifest disagree with the dump — which a verification reports as
a corrupt backup, on a backup that was fine.

---

## Commands

All of them through compose, so they inherit the same configuration the
application has:

```bash
cd /opt/rawsyst
C="docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup"

$C run --rm backup run          # take one
$C run --rm backup list         # what is in the store
$C run --rm backup verify       # prove the newest one restores
$C run --rm backup health       # is this installation actually protected
$C run --rm backup download -to /srv/out    # pull one onto this machine
$C run --rm backup check   -dump FILE       # check an artifact. No database, no bucket
$C run --rm backup verify-file -dump FILE   # restore an artifact and check it
$C run --rm backup restore      -snapshot ID -into DSN
$C run --rm backup restore-file -dump FILE  -into DSN
$C run --rm backup rollback     -from rawsyst_pre_restore_…
$C run --rm backup prune        # remove what is outside the policy
$C run --rm backup rehearse     # the whole recovery, end to end, disposably
```

`verify`, `download` and `restore` take `-snapshot ID`; without it they use the
newest completed one. `prune` takes `-dry-run`. Most take `-json`.

The backup image is `postgres:17-alpine` with the RawSyst binary added — about
21 MB on top of an image the host already has, because `pg_dump` and
`pg_restore` are the right tools and taking them from the same image as the
database is what guarantees the versions match.

---

## From the website

**Platform Admin → Backup & Recovery**, at `/platform/backups`. Only a platform
operator can reach it; every route behind it is `AccessSuperAdmin` and answers
404 to everybody else, including a business owner who has granted themselves
every permission their plan offers. A full dump is every business on this server
at once, and there is no tenant permission that could safely reach it.

The screen does four things:

* **Create backup** — queues a run. The agent takes it, uploads it, and goes
  straight on to verify it, because the moment somebody is definitely paying
  attention is the moment they pressed the button.
* **History** — every backup, its phase, and for a selected one the whole
  manifest and verification report. Download the dump, the manifest and the
  checksum file from here.
* **Upload a backup** — put an artifact from a laptop onto this server. See
  RECOVERY.md; this is what makes a dead server survivable.
* **Operations** — what has been asked for and what happened.

### The agent

The button needs something listening, and `pg_dump` lives in the postgres image
while the API is built from `scratch`. So `backup-agent` is a small resident
service on the backup image that claims work from `backup_task` and does it. It
is in the default compose profile and starts with everything else.

It is **not** a scheduler. The nightly backup is still a systemd timer, because
a timer that runs for ninety seconds a day beats a resident process that has to
be watched and restarted in order to do the same thing.

Only one heavy backup operation runs at a time, enforced by a unique index in
the database rather than by any process's belief. Two `pg_dump`s on two cores is
an outage caused by the thing that exists to prevent one.

---

## Verification, which is the whole point

```bash
$C run --rm backup verify
```

It downloads the snapshot, checks its length against the manifest, checks its
SHA-256, decrypts it if it is sealed, creates a temporary database beside the
real one, restores into that, takes a complete inventory of what came back,
compares it against the manifest, and drops the temporary database — whatever
happened.

**Production is never opened for writing.** The temporary database is named from
the snapshot id, this code creates it and drops it, and `dropDatabase` refuses
any name it did not build.

It needs a connection that may create a database:

```
RAWSYST_BACKUP_ADMIN_DSN=postgres://…@db:5432/postgres?sslmode=disable
```

A different database on the same server — `postgres` will do. A database cannot
be created from inside itself.

The restore runs as the **application's** role, not the administrative one, so
the restored objects are owned exactly as `migrate` leaves them. Restoring as
the admin role produces an intact database the product cannot read a row of.

### What a failure looks like

Every finding is collected rather than the first one being returned, because a
report that stops at the first problem sends somebody round the loop once per
problem and each loop is a restore. A failed verification leaves `verified_at`
NULL, sets the phase to `invalid`, records the reason, and leaves the artifact
in the store for investigation.

---

## Encryption

**Turn it on for production. It is off by default, and that default is about
key management rather than about whether encryption is wanted.**

The two sentences are not in tension. A default key would be no key at all, and
a product that generated one for you would have made a promise on your behalf
that only you can keep — so the software cannot switch this on, and the
deployment must. What the software can do is make the decision explicit, refuse
to guess, and say exactly what turning it on costs. That is this section.

The recommendation is unambiguous for any deployment holding real trading data:
a dump is every business on the server, in one file, in a bucket, and the people
who can list that bucket are not always the people who should be able to read a
shop's books. Set the key **before the first backup** — see *the two decisions*
in [SECRETS.md](SECRETS.md) for why later is worse than never having started.

If it is going to stay off, that should be a decision with a reason attached,
and there are only two good ones: a development or staging stack whose data is
disposable, or an object store the operator controls end to end where the
key-loss risk is judged worse than the disclosure risk. Both are defensible.
"We did not get round to it" is not, and this paragraph exists so that nobody
can tell themselves it was.

```
RAWSYST_BACKUP_ENCRYPTION_KEY=$(openssl rand -base64 32)
```

Base64 of exactly 32 bytes. Not a passphrase: deriving a key from one needs a
KDF, a work factor and a salt, and the failure mode of getting those wrong is a
backup that looks encrypted.

With a key set, the dump is sealed **before it leaves this server** with
AES-256-GCM in 4 MiB chunks. Chunked because a database does not fit in memory;
authenticated because a tampered backup should fail to decrypt rather than
restore quietly wrong. Three things are authenticated alongside each chunk: its
number, so chunks cannot be reordered; a per-stream random prefix, so a chunk
cannot be moved from one backup into another; and a marker on the last chunk, so
a **truncated** stream fails instead of decrypting into a shorter,
valid-looking dump. That last one is the property people leave out, and
truncation is exactly what a half-finished upload looks like.

The manifest records the algorithm, the chunk size, and a **fingerprint** of the
key — sixteen hex characters, from which the key cannot be recovered. Its whole
job is to let a restore say "this was sealed with key `a1b2…` and you have given
me `d4e5…`" instead of "decryption failed".

### What this commits you to

> **A lost key is a lost backup. Permanently. For every business on this
> server.** No part of this product, and nobody who wrote it, can recover a
> sealed dump without its key.

So: keep the key in a password manager or a secret store, **not** on this server
and **not** in the same place as the downloaded backups. Write down which
fingerprint is current. Test that somebody other than you can find it.

### Without a key

The dump is uploaded as `pg_dump` wrote it. Transport is TLS and the store's own
server-side encryption should be on, but both of those protect the bytes from
everyone **except the storage provider**. For a shop's own bucket that is
usually the right trade. Where it is not, set a key.

A checksum is not encryption. SHA-256 says the bytes came back unchanged; it
says nothing about who can read them. This product never calls one the other.

---

## How often the website may ask

Every backup route is `AccessSuperAdmin` and answers 404 to everybody else, so
the limits below are not a defence against the internet. They are a defence
against three things that do happen to authenticated surfaces: a screen stuck
in a render loop, a script retrying a failure, and the blast radius of a stolen
operator session.

Counted **per operator**, not per address, so two operators in one office
cannot throttle each other.

| Class | Routes | Limit |
|---|---|---|
| Read | health, list, detail, tasks, maintenance | 600 a minute |
| Heavy | create, verify, validate-restore | 12 an hour |
| Transfer | download, upload | 12 an hour |
| Destructive | prune, restore-production, set maintenance | 6 an hour |

A refusal is `429` with `Retry-After`, because a client that is looping is
exactly the client that should be told how long to wait rather than left to
guess.

Two things these limits deliberately are not:

**They are not the concurrency guard.** That is a partial unique index in
migration 0135, and it is the more important of the two: the database itself
refuses a second heavy task while one is queued or running, so two dumps can
never overlap however many requests arrive. The limits bound what is *asked
for*; the index bounds what *runs*.

**They are not a gate on anything dangerous.** They sit in front of the gates,
never instead of them. The first production restore of the hour is inside every
limit and is still refused unless the deployment enabled the capability, a
verified safety backup of the live database exists, and the snapshot id was
typed out. A test holds that, so the limits can never quietly become the thing
deciding whether a restore is allowed.

The cache backs the counters, and a cache that will not answer **allows** the
request. A Redis outage must not be the reason a business cannot recover.

---

## What is checked before a dump starts

Three preconditions, all of them cheap, all of them asked while the answer is
still free. Each one exists because the failure it prevents is expensive and
arrives at 03:30 when nobody is reading.

**The role can see past row-level security**, and can read every table *and
every sequence*. A sequence the role cannot read stops `pg_dump` exactly as dead
as a table it cannot read, with a message about `audit_log_id_seq` that reads as
a permissions puzzle rather than as a missing grant — which is how it was found.

**There is room to stage the dump.** A dump is written to disk before it is
uploaded, and on this server that disk is usually the database's disk. A backup
that runs out of space partway has not merely failed: it has spent the night
filling the volume Postgres writes its WAL into, and the first symptom is not a
failed backup but a till that cannot ring up a sale. So the run asks for as much
free space as the database measures — `RAWSYST_BACKUP_MIN_FREE_PERCENT`, 100 by
default — and refuses before starting if it is not there. That is deliberately
pessimistic: a custom-format dump is compressed and carries no indexes, so it is
reliably much smaller, and the cost of being too careful here is one message
while the cost of being too optimistic is an outage. A filesystem that will not
answer is a line in the log, not a refusal.

**There is somewhere off this server to put it.** No object store, no backup.

---

## Schedule

Two timers. Copy both pairs of units to `/etc/systemd/system` and enable them:

```bash
sudo cp deploy/server/rawsyst-backup.{service,timer} /etc/systemd/system/
sudo cp deploy/server/rawsyst-drill.{service,timer}  /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now rawsyst-backup.timer rawsyst-drill.timer
sudo systemctl list-timers 'rawsyst-*'
```

**`rawsyst-backup`, nightly at 03:30** — takes a backup, verifies it, then
prunes. In that order, and prune only if verify succeeded, so nothing is ever
deleted on a night when the new backup could not be proved.

**`rawsyst-drill`, Sundays at 04:30** — the whole recovery, end to end, against
disposable resources: take, upload, download to files, check those files against
nothing but their own checksums, restore into a temporary database, compare
everything, drop it. It catches what a verification cannot, because it never
leaves the machine: credentials with write and no read, a bucket that lists but
will not GET, a disk with no room for the downloaded copy.

`rawsyst-check.sh` runs hourly and reports the age of the last **verified**
backup, and names the last failure.

Nothing about the schedule is in the application. The agent does what it is
asked; the timers decide when.

---

## Retention

```
RAWSYST_BACKUP_KEEP_DAILY=7
RAWSYST_BACKUP_KEEP_WEEKLY=4
RAWSYST_BACKUP_KEEP_MONTHLY=3
```

A wider baseline for a business-management system is 14 / 8 / 12, and it is a
disk and billing decision rather than a code one. Set what you are willing to
pay to store.

Every line of the retention code is about deleting a backup, so it is written as
guards first and arithmetic second:

* the newest completed snapshot is kept, whatever the policy says;
* the newest **verified** snapshot is kept, whatever the policy says — that fact
  lives in the database and is passed in, because retention runs against the
  store and knows nothing about verification;
* **nothing at all** is deleted if no completed snapshot can be found: an empty
  or unreadable listing is a reason to stop, not a reason to start;
* a snapshot whose id this build cannot date is kept — deleting something
  because it is not understood is how a bug becomes data loss;
* deleting a snapshot removes its `COMPLETED` marker first, so a delete that
  dies halfway leaves something every restore already refuses rather than
  something that still claims to be complete.

`scripts/maintenance.sh` never touches the object store. Local disk pressure is
not a reason to delete a remote backup, and a `docker volume prune` on this
server must never be able to reach one.

---

## Secrets

**Secrets are not in backups.** Not the database password, not
`RAWSYST_JWT_SECRET`, not the object-storage credentials, not device keys, and
not the backup encryption key.

The reason is that a backup is read by whoever can list the bucket, and a backup
that carries the credentials to the system it backs up turns one leaked object
into a full compromise. Restoring a database from a stolen dump is bad;
restoring it and being able to sign valid sessions against the running system is
worse.

**[SECRETS.md](SECRETS.md) is the checklist** — every value, what breaks without
it, where to keep it, how to rotate it, and the order to bring a new server up
in. The short version:

1. Keep `.env` in a password manager, or in whatever secret store the
   organisation already has.
2. On a new server, write it before the first `docker compose up`.
3. `chmod 600 .env`. It is not in git and must not go in.

`RAWSYST_JWT_SECRET` is the one to think about at migration time. Restoring the
database onto a new server with a **different** JWT secret is safe and signs
everybody out; with the **same** secret, existing sessions keep working across
the move. Either is defensible — the first is tidier, the second is kinder — and
the choice belongs to whoever is doing the migration.

`RAWSYST_DATA_ENCRYPTION_KEYS` is not optional at restore time. Values sealed
with it — the ZATCA credential, payment gateway keys, MFA secrets — are in the
dump as ciphertext and are unreadable without it. Losing that key does not lose
the database; it loses those columns.

---

## Downloading one

From the website: **Backup & Recovery → History → a verified backup →
Download**. Three files:

```
RawSyst_Backup_<id>.dump           what pg_restore reads
RawSyst_Backup_<id>.manifest.json  what it is, and what it should contain
RawSyst_Backup_<id>.sha256         one line, in the format sha256sum reads
```

Or on the server:

```bash
$C run --rm backup download -to /srv/rawsyst-out
```

Only a backup that has been **proved to restore** is offered. Handing somebody a
file nobody has checked, to keep as their last copy, is how a business ends up
with a folder of things that are not backups. An operator investigating a failed
backup can ask for it anyway with `?unverified=true`, and that override is
recorded against their name.

The download streams from the object store straight through the API to the
browser. Nothing is buffered: a handler that read a multi-gigabyte dump into
memory to send it would kill the container on the first real backup, on the day
the business had grown enough to need one.

Every download is written to the audit trail **before** the bytes leave, because
a download interrupted halfway still took the data as far as the wire.

### Checking one without this product

```bash
sha256sum -c RawSyst_Backup_<id>.sha256
```

That is the point of the third file. The manifest carries the same hash, and a
check somebody can run with a tool they already have and trust more than this
software is worth more than one they cannot.

To check all three against each other, with no database and no network:

```bash
$C run --rm backup check -dump /srv/out/RawSyst_Backup_<id>.dump
```

It reads the manifest, hashes the dump, compares both against the `.sha256`
file, and confirms the file begins the way a PostgreSQL custom-format dump or a
sealed RawSyst backup begins. It says whether the artifact is **intact**. It
does not say whether it **restores** — `verify-file` does that, and it needs a
PostgreSQL to restore into.

---

## Recovery point and recovery time

Measured, not asserted. These are from the drill; run it and read your own.

| | |
|---|---|
| **RPO** | Up to 24 hours, plus however long since 03:30. The daily dump is a point in time and everything after it is on this server only. |
| **RTO, database** | Minutes for a small database. Measured below. |
| **RTO, whole server** | Hours, dominated by provisioning and DNS, not by the restore. RECOVERY.md walks it. |

### Measured, on a real drill

Run 2026-09-11 against an isolated PostgreSQL 18 and an S3-compatible store, on
a 27.4 MB database with 186 tables, 178 policies, two businesses and one
company. The application was started against the restored database and driven
through 29 checks.

| Step | Time |
|---|---|
| Take a backup: dump, upload, manifest, marker | 5s |
| Verify it: download, checksum, restore to scratch, compare, drop | 11s |
| Restore into a database beside the live one | 8s |
| `migrate` against the restored database | 0s, a no-op |
| API accepting requests against it | under 1s |
| New backup taken FROM the restored database, verified | 10s |

**Database RTO on that hardware: about 20 seconds**, from deciding to restore
to the application serving. That is a small database and the figure scales with
size; take your own on the real server, which is what
[PREPRODUCTION.md](PREPRODUCTION.md) step 5 is for.

These numbers describe the software. They do not describe a real recovery,
which is dominated by a person working out what happened, finding the secret
store, and DNS. Hours, not seconds.

### What the drill proved, beyond "the file restores"

All 29 checks passed on the restored database:

sign-in for two businesses and a platform operator · a wrong password still
refused · the permission catalogue at 103 permissions and 13 roles · products,
customers, suppliers, categories, stock on hand, orders, purchase orders, stock
movements, journals and documents all readable · profit and loss, balance sheet
and the dashboard all computing · **a second business unable to read the first
one's data when it puts the first one's company id in the request** · signed
out reading nothing · the company logo coming back · a write succeeding · the
worker starting · a platform operator reaching backup health while a business
owner still gets 404.

Two findings worth keeping:

**Files came back byte for byte.** Every file in this product is a column, so
`company_logo.bytes` and `document.bytes` were compared by checksum before and
after: identical. This is the concrete form of the claim that the database dump
is the whole of the business data.

**The restored database could protect itself immediately.** A new backup was
taken from it and verified with no re-granting needed — `pg_dump` carries the
grants as ACLs and `pg_restore` applies them, leaving 192 tables and sequences
readable by the backup role. A business that recovers into being unable to back
itself up has not recovered, so this is checked rather than assumed.

One thing the drill got wrong first, which is why the step exists: restoring as
an **administrator** rather than as the application's role produced an intact
database the product could not read a row of, failing at `permission denied for
table schema_migration`. `pg_restore` leaves objects owned by whoever connected.
RECOVERY.md step 6 and PREPRODUCTION.md both say to restore as the application
role, and now say why.

A 24-hour RPO means a business that loses this server at 22:00 loses its
trading day. That is the honest number for a daily logical dump and it is stated
here rather than softened. It is also no longer the number this installation
runs on — see the section below, and `deploy/server/PITR.md` for the measured
one.

**To do better, take backups more often.** The timer is a systemd unit and
`OnCalendar=*-*-* 03,15:30:00` is two a day. Each one costs a dump, a verify and
its storage.

### Point-in-time recovery, which is the other half

**It is implemented.** `deploy/server/PITR.md` is the whole of it.

The short version: `wal_level=replica` was always the precondition, and what was
missing was everything else — somewhere to ship the segments, a retention
decision for them, a physical base backup for them to be replayed onto, and a
restore procedure that had been rehearsed. Half of a setup like that is worse
than none, because it looks like protection. All four are now there.

What it changes about the numbers on this page:

| | Dumps alone | With continuous archiving |
|---|---|---|
| Recovery point | up to 24 hours | about 61 seconds |
| Recovery to an arbitrary moment | no | yes, inside a seven-day window |
| Needs the same major version | no | yes, for the physical half |

The dumps on this page do not go away and are not reduced in importance. They
are the half that survives a corrupt cluster, that restores one database rather
than the whole server, and that can be read on any machine with a PostgreSQL on
it. A corrupted page is inside a base backup and inside the log; it is not
inside a dump.

Two commands worth knowing from here:

```bash
docker compose run --rm backup wal preflight   # can this server archive at all
docker compose run --rm backup wal status      # is it, and how far back
```

---

## Health

```bash
$C run --rm backup health
```

GREEN, AMBER or RED, one sentence, and the numbers behind it. The same judgement
the website shows, made in one place so the product has one opinion.

| | |
|---|---|
| **GREEN** | The most recent backup has been restored into a temporary database and checked. |
| **AMBER** | The last proved backup is more than 30 hours old, or runs have been failing even though the last one worked. |
| **RED** | Nothing has ever been proved to restore, or the last one that was is more than a week old. |

Thirty hours rather than twenty-four: a nightly timer at 03:30 that takes twenty
minutes should not put the dashboard into amber every morning because the clock
moved.

`backup health` exits non-zero when it is not GREEN, so it can be a check in
whatever watches this machine.

---

## What is not automated, and why

**The key.** If encryption is on, the key is not on this server and cannot be
recovered by anything here. That is the arrangement, not a gap.

**Point-in-time recovery.** Implemented; see above and PITR.md. What is not
automated there is the same thing that is not automated here: the key, and the
decision to recover.

**The decision to restore.** Every destructive operation needs a person: the
snapshot id typed out, a rehearsal that passed, and a deployment that has
explicitly enabled the capability. RECOVERY.md is what that person reads.

**Proving a person can do it.** The drill proves the software recovers. It does
not prove that somebody who has never done it can bring the business back under
pressure. Walk RECOVERY.md on a spare machine once, before you need it.
