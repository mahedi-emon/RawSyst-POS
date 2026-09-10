# Moving RawSyst to another server

The whole of the business lives in one Postgres database, and this is how it
gets from one machine to another without losing any of it.

Read `BACKUP.md` first. This document assumes backups are running and verifying;
if they are not, the migration starts by making them, because the migration
**is** a restore and a restore you have never tested is a hope.

---

## What must survive, unchanged

Every identifier. Companies, users, products, orders, invoices, journal entries,
inventory movements, audit rows, regulatory rules and the documents they were
read out of — all of them keep the ids they have.

That is not an aspiration, it is a property of how this works: `pg_dump` and
`pg_restore` move rows, and a row's primary key is one of its columns. Nothing
in the restore path generates an identifier. `devseed` does, which is why it
must never be near a production database, and `MIGRATION.md` says so twice.

---

## The shape of it

```
OLD SERVER still serving
        │
        ├─ 1. take and verify a backup             (nothing changes yet)
        ├─ 2. build the NEW SERVER from it         (old one still serving)
        ├─ 3. smoke test the new one               (old one still serving)
        │
        ├─ 4. maintenance mode on the old one      ← the only downtime
        ├─ 5. final backup, verified
        ├─ 6. restore it onto the new one
        ├─ 7. smoke test again
        ├─ 8. switch DNS
        │
NEW SERVER serving, OLD SERVER untouched and off
        │
        └─ 9. wait. Then decommission.
```

Steps 1 to 3 are a rehearsal with no consequences. By the time anything is
switched, the new server has already been built once from a real backup and
looked at. The window in step 4 is minutes, and its length is the size of the
database, not the length of this document.

---

## 1. Take and verify a backup, on the old server

```bash
cd /opt/rawsyst
C="docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup"
$C run --rm backup run
$C run --rm backup verify
$C run --rm backup list
```

Write down the snapshot id. If `verify` does not end in `VERIFIED`, stop: there
is nothing to migrate with and the rest of this document is meaningless.

Note the schema version it printed. The new server will be checked against it.

---

## 2. Build the new server

Follow `RUNBOOK.md` steps 1 to 4: updates, swap, journal cap, firewall, SSH,
Docker, `daemon.json`. Then:

```bash
sudo mkdir -p /opt/rawsyst && sudo chown "$USER" /opt/rawsyst
git clone https://github.com/mahedi-emon/RawSyst-POS.git /opt/rawsyst
cd /opt/rawsyst
git checkout <the commit the old server is running>
```

The old server's commit, not `main`. A migration changes one thing at a time,
and moving machine and moving version at once means a fault has two possible
causes. Upgrade afterwards, separately, with a backup in hand.

### Secrets

By hand, from wherever they are kept. Never from a backup — see *Secrets* in
`BACKUP.md`.

```bash
cp .env.example .env
chmod 600 .env
```

`POSTGRES_PASSWORD` may be new; nothing in the dump depends on it.
`RAWSYST_JWT_SECRET` is a decision: the same value keeps everybody signed in
across the move, a new one signs everybody out. Both are defensible. Choose
deliberately rather than by accident.

The object-storage credentials must be the same, or the new server cannot read
the backups.

### Bring up the database only

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml build
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d db
```

Not the whole stack. The API would run migrations against an empty database and
create a schema for the restore to collide with.

### Restore

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml exec db \
  psql -U rawsyst -d postgres -c 'CREATE DATABASE rawsyst_restored OWNER rawsyst'

$C run --rm backup restore -snapshot <ID> \
  -into 'postgres://rawsyst:'"$POSTGRES_PASSWORD"'@db:5432/rawsyst_restored?sslmode=disable'
```

Into a database of its own, not over the one compose created. Then look at it
before trusting it:

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml exec db \
  psql -U rawsyst -d rawsyst_restored -c \
  "SELECT
     (SELECT max(version) FROM schema_migration)          AS schema,
     (SELECT count(*) FROM tenant)                        AS tenants,
     (SELECT count(*) FROM company)                       AS companies,
     (SELECT count(*) FROM app_user)                      AS users,
     (SELECT count(*) FROM product)                       AS products,
     (SELECT count(*) FROM invoice)                       AS invoices,
     (SELECT count(*) FROM journal_entry)                 AS journals,
     (SELECT count(*) FROM audit_log)                     AS audit_rows,
     (SELECT count(*) FROM regulatory_rule)               AS rules"
```

Every number matches what the manifest recorded. `verify` already checked that
mechanically; this is the same check by hand, because on migration day a person
should look at the numbers themselves.

### Point the application at it

In `.env`, set the database name to `rawsyst_restored` — or rename:

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml stop api worker web
docker compose -f docker-compose.yml -f docker-compose.server.yml exec db psql -U rawsyst -d postgres \
  -c 'ALTER DATABASE rawsyst RENAME TO rawsyst_empty' \
  -c 'ALTER DATABASE rawsyst_restored RENAME TO rawsyst'
```

`rawsyst_empty` is kept, not dropped. It costs nothing and it is the thing to
look at if something is wrong.

### Migrate and start

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml run --rm migrate
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d
```

The migrator applies whatever this build added since the snapshot, and does
nothing when the answer is nothing. It is safe to run twice; CI proves that on
every commit.

**Do not run `devseed`.** It is for a developer's machine. It creates tenants,
users and demo data, and against a restored production database it would put
test businesses beside real ones.

**Do not run `freshcheck`.** It drops the schema. It refuses a DSN that does not
name a development or test database, and that refusal is the only thing between
a mistyped command and the end of the business.

---

## 3. Smoke test, while the old server is still serving

Nothing has switched yet. The new server is answering on its own address.

```bash
curl -s http://localhost:8080/readyz
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:3000/
docker compose -f docker-compose.yml -f docker-compose.server.yml ps
bash deploy/server/rawsyst-check.sh
```

Then, in a browser against the new server's address, by hand:

- sign in as a real user, with a real password
- open a company that has data in it and check the name is right
- open a customer, an invoice and a product, and check they are the ones you know
- open **Oversight → Backups** and check the last verified backup is the one you restored from
- open **Platform → Regulatory sources** and check the end-of-service rule is in force
- take a backup on the NEW server and verify it

The last one matters more than it looks. A new server that cannot back itself up
is a new server you cannot migrate off, and the time to find that out is now.

---

## 4. Maintenance mode on the old server

This is the only downtime, and it starts here.

```bash
# on the OLD server
docker compose -f docker-compose.yml -f docker-compose.server.yml stop web api worker
```

The database stays up: the final backup has to read it. With the API and the
back office stopped, nothing can write, so the dump that follows is the last
state of the business and nothing is lost between the dump and the switch.

Tell whoever is at a till first. A minute of "the system is down" that somebody
was warned about is an inconvenience; the same minute unannounced is an
incident.

---

## 5. Final backup

```bash
# on the OLD server
$C run --rm backup run
$C run --rm backup verify
```

Verified, not merely taken. If it does not verify, start the old server again
and work out why — the migration can wait, the business cannot.

---

## 6, 7. Restore it onto the new server and test again

The same steps as section 2, with the new snapshot id. The database is already
there, so it is:

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml stop api worker web
docker compose -f docker-compose.yml -f docker-compose.server.yml exec db psql -U rawsyst -d postgres \
  -c 'ALTER DATABASE rawsyst RENAME TO rawsyst_rehearsal' \
  -c 'CREATE DATABASE rawsyst_final OWNER rawsyst'

$C run --rm backup restore -snapshot <FINAL ID> \
  -into 'postgres://rawsyst:'"$POSTGRES_PASSWORD"'@db:5432/rawsyst_final?sslmode=disable'

docker compose -f docker-compose.yml -f docker-compose.server.yml exec db psql -U rawsyst -d postgres \
  -c 'ALTER DATABASE rawsyst_final RENAME TO rawsyst'

docker compose -f docker-compose.yml -f docker-compose.server.yml run --rm migrate
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d
```

Run the same row counts and the same by-hand checks. This is the copy that goes
live.

---

## 8. Switch

Lower the DNS time-to-live to 60 seconds **a day before** the migration, not on
the day. A record with an hour's TTL cached across the internet is an hour of
some customers reaching a server in maintenance mode.

Then point the record at the new address and watch:

```bash
watch -n5 'curl -s -o /dev/null -w "%{http_code}\n" https://rawsyst.example.com/'
```

Certificates: if a reverse proxy is issuing them, it needs the DNS to have moved
before it can. Give it a minute and check.

---

## 9. Rollback, if it comes to that

The old server is untouched and off. That is the whole rollback plan, and it is
why nothing was deleted:

```bash
# on the OLD server
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d
```

Then point DNS back. The old database has every row it had when it was stopped,
because it was stopped before the final dump and nothing has written to it
since.

**The window closes when somebody writes to the new server.** After the first
sale, rolling back means losing it. So the decision to keep going is made in the
first minutes, on the evidence of the smoke tests, and not a week later.

Keep the old server for **at least a week**, powered off, nothing deleted. It
costs a few days of a small VM and it is the only copy of the situation you were
in before.

---

## Decommission

Not before all of these:

- [ ] the new server has been serving for a week with no data complaint
- [ ] backups on the new server have run and **verified** for seven consecutive nights
- [ ] a restore from a new-server backup has been tested onto a scratch machine
- [ ] the old server's final snapshot is still in the object store and still verifies
- [ ] `rawsyst-check.sh` has been clean on the new server for a week

Then take one last backup of the old server, verify it, keep it outside the
retention policy, and only then destroy the machine.

---

## What is not automated, and why

There is no `make migrate-server`. Every step above is a person typing a command
and looking at what came back.

That is deliberate. A migration is a handful of commands run once, with a
business on the other end of them, and the failure mode of automating it is a
script that gets halfway and leaves two servers in an unclear state at the exact
moment nobody wants to debug a script. The parts that benefit from automation —
taking a backup, checking it restores, keeping the right ones — are automated,
tested, and run every night.
