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

### Why it cannot be skipped

```
10:00   backup taken
10:01   a till rings up a sale
10:05   DNS switched to the new server
```

That sale is on the old server and nowhere else. When the old server is turned
off, it is gone — and nobody finds out until the day's cash disagrees with the
till totals. There is no clever way round it. The only way not to lose writes
made after the final backup is **not to accept them**.

### Turning it on

**Platform Admin → Backup & Recovery → Overview → Write freeze.** Type what
people should be told, and close writes:

> RawSyst is closed for about ten minutes while its data is moved.

Every business route that writes answers 503 with that sentence. **Reads stay
open** — a cashier looking at yesterday's totals writes nothing and loses
nothing, and locking them out of a screen they are reading turns a planned ten
minutes into a support call. **Platform operators keep working**, which is what
lets you finish the migration through this product rather than through a
database client.

It takes up to two seconds to reach every request: the state is one row and it
is cached for that long, because a query in front of every sale to check a flag
that changes twice a year is the wrong trade. That is the whole delay.

Tell whoever is at a till first. A minute of "the system is down" that somebody
was warned about is an inconvenience; the same minute unannounced is an
incident.

### The heavier version

Stopping the services closes everything, reads included:

```bash
# on the OLD server
docker compose -f docker-compose.yml -f docker-compose.server.yml stop web api worker
```

The database stays up, because the final backup has to read it. Use this if you
want the certainty that nothing at all is talking to the database; use the write
freeze if you would rather people could still look things up. Both give the same
guarantee about writes.

If you stop the services, the write freeze cannot be turned off from the website
afterwards — there is no website. Turn it off before you stop them, or turn it
off on the **new** server after the restore, where the flag arrives with the
database.

---

## 5. Final backup

```bash
# on the OLD server
$C run --rm backup run
$C run --rm backup verify
```

Verified, not merely taken. If it does not verify, open writes again and work
out why — the migration can wait, the business cannot.

**Write down the snapshot id.** It is the one thing the next four steps all
refer to, and the one thing that is annoying to look up in a hurry.

### Take a copy off both machines

Before going any further, put the final backup somewhere that is neither
server:

```bash
$C run --rm backup download -to /srv/final
sha256sum -c /srv/final/RawSyst_Backup_<FINAL ID>.sha256
```

Then copy those three files to a laptop. This costs two minutes and it is the
difference between a migration that can go wrong and one that cannot: with them,
the business can be rebuilt on any machine even if the old server, the new
server and the bucket all fail on the same afternoon. `RECOVERY.md` section C is
that procedure.

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

---

## Checklists

Print these. Tick as you go. The point of a checklist is that it survives being
tired.

### Before the day

Nothing here touches production. All of it can be done a week ahead.

- [ ] Backups have run **and verified** for seven consecutive nights
- [ ] `backup health` on the old server is GREEN
- [ ] The weekly drill has passed at least once: `journalctl -u rawsyst-drill`
- [ ] I have `.env` from the password manager, and it is complete
- [ ] I have `RAWSYST_DATA_ENCRYPTION_KEYS`
- [ ] I have the backup encryption key, if backups are sealed, and I have
      checked its fingerprint against a manifest
- [ ] The new server exists, and section 2 has been walked on it once, from a
      real backup
- [ ] The new server's own backups run and verify, to its own bucket or prefix
- [ ] DNS time-to-live has been lowered, so the switch is minutes not hours
- [ ] Whoever is at a till knows which day and roughly which hour
- [ ] Somebody other than me knows this is happening and what to do if I stop

### On the day

In order. Do not skip step 3 because step 2 went well.

- [ ] 1. Old server: backup, verify, note the snapshot id
- [ ] 2. New server: build from it, migrate, start
- [ ] 3. New server: smoke test — sign in, read a sale, read stock, run a report
- [ ] 4. Old server: **write freeze on.** Tell the tills
- [ ] 5. Old server: final backup. Verify it. Note the id
- [ ] 6. Old server: download the final backup to a laptop and `sha256sum -c` it
- [ ] 7. New server: restore the final snapshot beside the rehearsal database
- [ ] 8. New server: migrate, start, smoke test again
- [ ] 9. New server: check the counts against what the old one reported
- [ ] 10. Switch DNS
- [ ] 11. Watch the new server answer real traffic for ten minutes
- [ ] 12. Old server: stop everything. **Do not destroy it**
- [ ] 13. New server: write freeze off, if it travelled with the database

If anything between 4 and 10 goes wrong: open writes on the old server, switch
nothing, and stop. The old server is untouched and still has every row. That is
the whole reason the order is this way round.

### After

- [ ] The full [What to check](RECOVERY.md#what-to-check) list, on the new server
- [ ] A fresh backup on the new server, verified
- [ ] `backup health` on the new server is GREEN
- [ ] `rawsyst-backup.timer` and `rawsyst-drill.timer` are enabled and listed
- [ ] The hourly check is clean: `journalctl -u rawsyst-check`
- [ ] The final snapshot is in the object store, and on a laptop
- [ ] The old server is off, intact, and will stay that way
- [ ] Written down: both snapshot ids, both server addresses, the times, and
      who did it

Then wait a week and read *Decommission* above.
