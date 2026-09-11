# Recovery

Something has gone wrong. This document is what to do about it.

**[BACKUP.md](BACKUP.md)** is how backups are taken and what makes one
trustworthy. **[MIGRATION.md](MIGRATION.md)** is a planned move to another
server, which is a different thing and is calmer.

---

## Read this first

Four rules. Every procedure below obeys them and none of them is negotiable.

1. **Nothing is deleted.** Not a database, not a snapshot, not a volume. Every
   destructive operation in this product renames the thing it replaces and
   leaves it on the server. If you find yourself typing `DROP`, stop.
2. **A backup that has not been restored is not a backup.** Do not plan a
   recovery around a file nobody has checked. Checking takes minutes; finding
   out during the recovery takes the business.
3. **The old server stays.** Until the new one has been running, verified, and
   trading for long enough that you would notice a problem. Days, not hours.
4. **Write down what you did, as you do it.** The snapshot id, the database
   names, the times. The product records most of it, and the part it cannot
   record is what you decided and why.

---

## Which situation is this?

| | Go to |
|---|---|
| The data is wrong and the server is fine | [A. Put a backup back](#a-put-a-backup-back) |
| The server is gone; you have the bucket | [B. Rebuild from the object store](#b-rebuild-from-the-object-store) |
| The server **and** the bucket are gone; you have three files | [C. Rebuild from a laptop](#c-rebuild-from-a-laptop) |
| A restore you just did was the wrong decision | [D. Roll it back](#d-roll-it-back) |
| Nothing is wrong; you want to know whether this works | [E. Drill](#e-drill) |

---

## A. Put a backup back

The server is running. The database is intact but wrong — a bad import, a
deletion, a migration that did something nobody wanted.

This is the only procedure that touches a live database, and it is written to be
as close to reversible as a database operation gets: **the database being
replaced is renamed aside, never dropped**, and rolling back is the same two
renames in the other order.

### Before you start

Turn the capability on. It is off by default, in the environment, where nobody
with a browser can change it:

```
RAWSYST_ALLOW_PRODUCTION_RESTORE=true
```

```bash
cd /opt/rawsyst
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d backup-agent
```

`RAWSYST_BACKUP_ADMIN_DSN` must name a **superuser**. The cutover refuses new
connections to a database before renaming it, and that is not something an
ordinary role may do.

### From the website

**Platform Admin → Backup & Recovery → History**

1. Choose the backup. Read its date, its size, its schema version and its
   verification report. This is the moment to be sure.
2. **Rehearse a restore.** It restores into a temporary database, compares
   every table, every business, every sequence and every policy against the
   manifest, and drops it. Production is not opened. The phase becomes
   **Rehearsed, ready to restore**.
3. **Replace the live database.** Type the backup's name into the confirmation.
   Nothing else is accepted.

What then happens, in order, and it stops at the first thing that fails:

```
take a backup of what is live NOW      ← so this is reversible
verify that backup                     ← an unproved safety copy is not one
restore the chosen snapshot into a NEW database beside production
compare it against its manifest        ← every table, every business
put the backup role's grants back      ← or the next nightly backup fails
close writes                           ← maintenance mode
refuse new connections to the live database
terminate what is connected
rename  rawsyst          →  rawsyst_pre_restore_<timestamp>
rename  rawsyst_restore_… →  rawsyst
allow connections to the old one again
open writes
```

If anything before the renames fails, **nothing has changed** and the message
says so. If the second rename fails, the first is undone before the error
returns; that is the only moment when the production name names nothing, and it
is one statement wide.

The cutover is seconds. The write freeze covers all of it, so nothing is
accepted that would then be lost.

### Afterwards

* The report names **`previous_database`**. Write it down. That is the rollback.
* A row in the newly-installed database records the restore — it has to be
  written there, because everything written before the cutover is in the
  database that was renamed aside, and the one now serving has never heard of
  the operation that installed it.
* Restart the application so it opens fresh connections:
  `docker compose … restart api worker`
* Work through [What to check](#what-to-check).

### If you would rather not use the website

```bash
C="docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup"

# Into a new database, beside the live one. Never into the live one.
$C run --rm backup restore -snapshot 20260910T033000Z-1 \
  -into 'postgres://rawsyst:…@db:5432/rawsyst_new?sslmode=disable'

$C run --rm migrate            # bring the schema up to this build
# then point RAWSYST_DB_DSN at rawsyst_new and restart
```

`restore` refuses a target that already holds tables. A restore goes **beside**
the live database and the application is switched to it, so that if anything
goes wrong the database you have is the database you had.

---

## B. Rebuild from the object store

The server is gone. The bucket is not.

You need: the bucket and its credentials, `.env` from your password manager,
and the encryption key if backups are sealed.

```bash
# 1. A new machine. Follow deploy/server/RUNBOOK.md up to the first
#    `docker compose up`, then stop.
git clone https://github.com/mahedi-emon/RawSyst-POS.git /opt/rawsyst
cd /opt/rawsyst

# 2. .env, from wherever you keep it. Not from the backup: it is not in one.
vi .env && chmod 600 .env

# 3. The database only. Nothing else yet.
C="docker compose -f docker-compose.yml -f docker-compose.server.yml"
$C up -d db

# 4. What is in the bucket.
$C --profile backup run --rm backup list

# 5. Prove one restores, before you build anything on it.
$C --profile backup run --rm backup verify -snapshot 20260910T033000Z-1

# 6. Create the database the application will use, and restore into it.
#
#    AS THE APPLICATION'S ROLE, never as postgres. pg_restore leaves every
#    object owned by whoever connected, so restoring as an administrator
#    produces an intact database the product cannot read a row of — it fails
#    at the first query with `permission denied for table schema_migration`.
#    Found by doing exactly that in a drill.
$C exec db psql -U postgres -c 'CREATE DATABASE rawsyst OWNER rawsyst'
$C --profile backup run --rm backup restore -snapshot 20260910T033000Z-1 \
  -into 'postgres://rawsyst:…@db:5432/rawsyst?sslmode=disable'

# 7. The schema this build expects. A no-op when the snapshot is current.
$C run --rm migrate

# 8. Up.
$C up -d
$C --profile backup run --rm backup health
```

Step 5 is not optional and is not the same as step 6. It restores into a
**temporary** database and compares everything, and it is how you find out that
the bucket lists but will not GET, or that the credentials are write-only, or
that the key you have is not the key it was sealed with — before you have built
a server on the assumption that they are fine.

Then [What to check](#what-to-check), then DNS.

---

## C. Rebuild from a laptop

The server is gone. **So is the bucket** — the account, the provider, the
credentials, whatever happened.

What is left is three files somebody downloaded, and a machine that has never
heard of the old one. This works, and the old server does not have to exist for
any of it.

```
RawSyst_Backup_20260910T033000Z-1.dump
RawSyst_Backup_20260910T033000Z-1.manifest.json
RawSyst_Backup_20260910T033000Z-1.sha256
```

### 1. Check the files, before you go anywhere near a server

On the laptop, with a tool that is not this product:

```bash
sha256sum -c RawSyst_Backup_20260910T033000Z-1.sha256
```

`OK` means the file is the file. Anything else means stop and find another copy.

### 2. Get them onto the new machine

Either **scp**:

```bash
scp RawSyst_Backup_20260910T033000Z-1.* root@new-server:/srv/incoming/
```

Or **through the website**, once the new server is up with an empty database:
Platform Admin → Backup & Recovery → **Upload a backup**. Choose the dump and
the manifest; the checksum file is optional and worth sending, because then the
two can be checked against each other.

Nothing uploaded is trusted. The filename is never used — the three names are
composed from the snapshot id inside the manifest, so nothing from the request
becomes a path. The bytes are streamed to disk and hashed on the way. The
manifest has to parse, be a version this build reads, and describe the file
beside it exactly. The file has to **begin** like a dump or like a sealed
RawSyst backup. And after all of that the record says **UPLOADED**, not
verified: nothing has been proved about whether it restores.

Backups larger than 8 GiB do not go through a browser. Use `scp` and the command
line below; there is no limit on that path because it does not go through a web
server.

### 3. Bring the new server up around it

```bash
git clone https://github.com/mahedi-emon/RawSyst-POS.git /opt/rawsyst
cd /opt/rawsyst
vi .env && chmod 600 .env       # from your password manager

C="docker compose -f docker-compose.yml -f docker-compose.server.yml"
$C up -d db
```

Create the backup role — [BACKUP.md](BACKUP.md#before-the-first-backup-the-backup-role)
has the six statements. A restore puts the grants back for you, but the role has
to exist.

### 4. Check it, restore it

```bash
B="$C --profile backup run --rm -v /srv/incoming:/incoming backup"

# Intact? No database, no bucket, no network.
$B check -dump /incoming/RawSyst_Backup_20260910T033000Z-1.dump

# Does it restore? Into a temporary database, compared, then dropped.
$B verify-file -dump /incoming/RawSyst_Backup_20260910T033000Z-1.dump

# Only now.
$C exec db psql -U postgres -c 'CREATE DATABASE rawsyst OWNER rawsyst'
$B restore-file -dump /incoming/RawSyst_Backup_20260910T033000Z-1.dump \
   -into 'postgres://rawsyst:…@db:5432/rawsyst?sslmode=disable'

$C run --rm migrate
$C up -d
```

If it was uploaded through the website instead, do steps 2 and 3 of
[A](#a-put-a-backup-back) from the screen: **Rehearse a restore**, then
**Replace the live database**. On a machine whose database is empty the safety
backup is skipped and the report says so — there is nothing there to protect,
and requiring a backup of an empty database would need an object store this
machine may not have yet.

### 5. Point the object store somewhere new

The old bucket is gone. Configure a new one in `.env` and take a backup **the
same day**:

```bash
$C --profile backup run --rm backup run
$C --profile backup run --rm backup verify
```

A recovered server with no backup arrangement is a server waiting to do this
again.

---

## D. Roll it back

The restore worked and was the wrong decision. The database it replaced is still
on the server.

```bash
C="docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup"

# Which one? The restore's report names it; so does the database list.
docker compose … exec db psql -U postgres -c '\l' | grep rawsyst

$C run --rm backup rollback -from rawsyst_pre_restore_20260910t150521
docker compose -f docker-compose.yml -f docker-compose.server.yml restart api worker
```

The same two renames in the other order. What was serving is renamed aside as
`rawsyst_rolledback_<timestamp>` and **not** dropped, so this is reversible too.

After a rollback the snapshot's phase is no longer `restore_ready` — the
register came back with the database. Rehearse again before restoring again.
That is correct rather than annoying: the state of the world has changed.

---

## E. Drill

```bash
$C run --rm backup rehearse
```

Take, upload, download to files, check those files against nothing but their own
checksums, restore into a temporary database, compare every table, every
business, every sequence and every policy, drop it. Production is never opened.

It runs weekly on its own (`rawsyst-drill.timer`), and the report is in the
journal:

```bash
journalctl -u rawsyst-drill -n 60
```

It reports the backup's size, how long each stage took, the total, the table
count, the row count, the business count, and PASS or FAIL. That is the document
to show somebody who asks whether this business could be brought back.

The drill proves the **software** recovers. It does not prove that a person who
has never done it can bring the business back under pressure. Walk section B on
a spare machine once, with a stopwatch, before you need it.

---

## What to check

After any recovery, before DNS and before telling anybody it is done.

```bash
C="docker compose -f docker-compose.yml -f docker-compose.server.yml"
$C ps                                  # everything up, api healthy
curl -fsS localhost:8080/readyz        # the database answers
$C --profile backup run --rm backup health
```

Then sign in and look, as a platform operator and then as a business owner:

- [ ] Every business is listed, and the count is what it was
- [ ] Sign-in works, and roles and permissions are intact
- [ ] Companies, branches, stores and warehouses
- [ ] Products, variants, categories and barcodes
- [ ] Stock on hand for a few known items
- [ ] Recent sales, and the POS opens and can take one
- [ ] Purchases, suppliers and outstanding bills
- [ ] Customers, their balances and their wallets
- [ ] Invoices — including the most recent one, by number
- [ ] Accounting: trial balance, and the current period is open
- [ ] Payroll: the last run, and employee records
- [ ] Tax and the regulatory registry, with its source documents
- [ ] Approvals waiting on somebody
- [ ] The audit trail, and that it now contains this recovery
- [ ] Settings, branding and integrations
- [ ] Reports run and produce the same figures as before

If sequences came back wrong you will see it here first, as a duplicate key on
the first write. The verification checks them for exactly that reason, so this
should not happen — but this is the list, and it is short enough to work
through.

---

## What must never be deleted

Not by a script, not by a cleanup, not by somebody tidying up.

* **`rawsyst_pre_restore_*`** — the database a restore replaced. It is the
  rollback.
* **`rawsyst_rolledback_*`** — the database a rollback replaced. Same reason.
* **The newest verified snapshot**, and **the newest completed snapshot**.
  Retention protects both; a human with a bucket console does not have to.
* **The encryption key**, if there is one. Nothing recovers a sealed backup
  without it.
* **`RAWSYST_DATA_ENCRYPTION_KEYS`.** Losing it does not lose the database; it
  loses the ZATCA credential, the payment gateway keys and the MFA secrets
  inside it.
* **The old server**, until the new one has traded for long enough that a
  problem would have shown itself.

`scripts/maintenance.sh` does not touch the object store, and nothing in this
repository runs `docker volume prune`. Keep it that way.

---

## When it will not work

**"The role cannot take a backup."** The backup role has no `BYPASSRLS`, or no
`SELECT`. See
[BACKUP.md](BACKUP.md#before-the-first-backup-the-backup-role).

**"This backup was sealed with key `a1b2…` and the configured key is `d4e5…`."**
You have the wrong key. The right one is whatever was in
`RAWSYST_BACKUP_ENCRYPTION_KEY` when the backup was taken.

**"…is encrypted and no key is configured."** Same, with none set at all.

**"has no completion marker."** The run that made it did not finish. It is not a
backup and this will not restore it. Use another.

**"the store returned N bytes for a snapshot the manifest says is M."** The
upload did not finish, or the object was replaced. Do not restore it.

**"does not match its manifest."** The bytes changed. Do not restore it.

**"already holds N tables."** You pointed a restore at a database that has
something in it. Restores go into an empty database, beside the live one.

**"has not passed a restore validation."** Rehearse it first. It takes about as
long as the real thing and it is the difference between finding a problem in a
temporary database and finding it in production.

**"Another backup operation is already queued or running."** One at a time, on
purpose. Two `pg_dump`s on two cores is an outage caused by the thing that is
supposed to prevent one. Wait, or look at Operations to see what it is.

**"Replacing the live database is switched off in this deployment."**
`RAWSYST_ALLOW_PRODUCTION_RESTORE=true`, then restart `backup-agent`.

---

## Checklist

Print this. Tick as you go.

**Before**

- [ ] I know which snapshot, and I have read its verification report
- [ ] I have `.env`, from the password manager
- [ ] I have `RAWSYST_DATA_ENCRYPTION_KEYS`
- [ ] I have the backup encryption key, if backups are sealed
- [ ] I know where the old database will end up, and that it is not deleted
- [ ] Somebody else knows I am doing this

**During**

- [ ] Checked the artifact — `sha256sum -c`, or `backup check`
- [ ] Rehearsed the restore, and it passed
- [ ] Restored
- [ ] Ran the migrator
- [ ] Started the application, and `/readyz` answers

**After**

- [ ] Worked through [What to check](#what-to-check)
- [ ] Took a fresh backup, and verified it
- [ ] `backup health` is GREEN
- [ ] Wrote down: the snapshot id, the previous database's name, the time
- [ ] The old server and the old database are both still there
