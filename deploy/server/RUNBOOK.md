# Putting RawSyst on a two-core, 3.7 GiB server

Written for the machine it was written on: Ubuntu 24.04, 2 vCPU, 3.7 GiB RAM,
48 GB disk, one public address, nothing else running. Every number below comes
from that shape and should be re-derived if the shape changes.

Nothing here runs itself. Each step is a command a person types, in order, on a
machine they can see.

---

## 0. Look before touching

```bash
sudo bash deploy/server/preflight.sh
```

It reads and reports; it installs and changes nothing. Work through what it
marks `FIX` before going further. On a fresh Ubuntu box that will usually be:
Docker missing, no swap, no firewall, and pending updates.

The reboot, if one is pending, happens **now** — not after the stack is up. A
kernel change mid-service is an outage nobody chose.

---

## 1. The operating system

```bash
sudo apt-get update && sudo apt-get upgrade -y
sudo reboot          # if /var/run/reboot-required exists
```

### Swap

The box has none, and that is the single most consequential gap. Without swap
the kernel's answer to a moment of memory pressure is to kill the largest
process, which here is Postgres. Two gigabytes is enough to absorb a spike and
small enough not to invite the machine to live in it.

```bash
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab

echo 'vm.swappiness=10'          | sudo tee  /etc/sysctl.d/99-rawsyst.conf
echo 'vm.overcommit_memory=0'    | sudo tee -a /etc/sysctl.d/99-rawsyst.conf
sudo sysctl --system
```

`swappiness=10` keeps the database's pages in memory and leaves swap for
emergencies. It is not a place to run from; it is a place to survive in for the
minute it takes somebody to look.

### The journal

Uncapped, `journald` will take whatever `/var` has.

```bash
sudo sed -i 's/^#\?SystemMaxUse=.*/SystemMaxUse=200M/' /etc/systemd/journald.conf
sudo systemctl restart systemd-journald
```

---

## 2. Firewall

Only three ports belong on the public interface. RawSyst's own 8080 and 3000
are **not** among them: they go behind a reverse proxy, on loopback.

```bash
sudo apt-get install -y ufw
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status numbered
```

### SSH

```bash
sudo sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config
sudo sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/'  /etc/ssh/sshd_config
sudo sshd -t && sudo systemctl reload ssh
```

Add your key **before** turning password authentication off, and keep the
current session open until a second one connects. Locking yourself out of a
server you can only reach over SSH is a slow afternoon.

---

## 3. Docker

```bash
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER"       # log out and back in for this to apply
sudo cp deploy/server/daemon.json /etc/docker/daemon.json
sudo systemctl restart docker
sudo systemctl enable docker
```

`daemon.json` caps container logs for anything started outside compose, keeps
containers running across a daemon restart, and pins the address pool so Docker
does not pick a subnet that collides with the private network the host is on.

---

## 4. Configuration

```bash
cp .env.example .env
```

Then set, at minimum:

| Variable | What it is |
|---|---|
| `POSTGRES_PASSWORD` | The database password. Generate it; do not choose it. |
| `RAWSYST_JWT_SECRET` | Session signing. Generate it. |
| `RAWSYST_ENV` | `production` |
| `RAWSYST_PLATFORM_EMAIL` | The first operator's sign-in address |

```bash
openssl rand -base64 36        # for each secret, separately
chmod 600 .env
```

`.env` is not in git and must not go in. If a secret has ever been in a
terminal that somebody else can scroll back through, rotate it.

---

## 5. Bring it up

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml build
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d
docker compose -f docker-compose.yml -f docker-compose.server.yml ps
```

Pass **both** `-f` flags to every compose command that touches a service,
including `run`. With only the base file, `docker compose run` recreates the
database container on the base file's ceilings and silently discards this
profile — and a merged config being right is not the same as the running
container being right. Check rather than assume:

```bash
docker exec -it "$(docker ps -qf name=db)" \
  psql -U rawsyst -d rawsyst -c \
  "SELECT name, setting FROM pg_settings
    WHERE name IN ('shared_buffers','max_connections','max_parallel_workers_per_gather')"
```

`shared_buffers` should be 24576 (192 MB in 8 kB pages), `max_connections` 24,
and `max_parallel_workers_per_gather` 1.

### The first operator

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml \
  --profile setup run --rm bootstrap
```

It prints a one-time password once and stores nothing readable. It refuses to
run a second time — that is what separates a bootstrap from a back door.

### The legal values

```bash
docker compose -f docker-compose.yml -f docker-compose.server.yml \
  --profile setup run --rm ingest -rule SA.EOSB.ENTITLEMENT

docker compose -f docker-compose.yml -f docker-compose.server.yml \
  --profile setup run --rm ingest -rule SA.EOSB.ENTITLEMENT \
  -from 2005-09-27 -apply -verified you@example.com
```

The first retrieves the Ministry's publication, hashes it and prints what it
read out of each article. Read that against the document before running the
second. The same thing is available in the application under **Platform →
Regulatory sources**, and applying it there is the ordinary route.

---

## 6. TLS, and not publishing the API

The compose file publishes 8080 and 3000. On a server they belong on loopback
with a proxy in front. Add a small override rather than editing the base file:

```yaml
# docker-compose.public.yml
services:
  api:
    ports: ["127.0.0.1:8080:8080"]
  web:
    ports: ["127.0.0.1:3000:3000"]
```

Then Caddy, which gets a certificate without being asked twice:

```bash
sudo apt-get install -y caddy
```

```caddyfile
# /etc/caddy/Caddyfile
rawsyst.example.com {
    encode zstd gzip

    # The API, addressed directly.
    #
    # This route is not optional on a two-core box, and leaving it out is the
    # easy mistake: the back office DOES proxy `/api/v1` to the API, so the
    # product works without it and every API call a browser or a till makes
    # travels browser -> Caddy -> Node -> Go instead of browser -> Caddy -> Go.
    # Next resolves that rewrite in its own process, which is capped at one
    # core and 320 MB in `docker-compose.server.yml`, and which exists to serve
    # prerendered pages rather than to be a proxy. On a busy counter it is the
    # web container that saturates first, for traffic that never needed it.
    #
    # Same origin either way, so nothing about CORS or cookies changes, and the
    # rewrite stays in `next.config.mjs` as the fallback for a deployment with
    # no proxy at all.
    handle /api/* {
        reverse_proxy 127.0.0.1:8080
    }

    # Everything else is the back office.
    handle {
        reverse_proxy 127.0.0.1:3000
    }
}
```

`encode zstd gzip` is where response compression lives; the API does not
compress and should not, because doing it in both places wastes the CPU this
machine has least of. It is worth what it costs: measured against the
development API, `GET /api/v1/permissions` is 24.8 KB and gzips to 5.6 KB, and
`GET /api/v1/people/roles` is 11.0 KB and gzips to 2.1 KB. Both are fetched
when somebody signs in.

Reload with `sudo systemctl reload caddy`.

---

## 7. Watching it

```bash
bash deploy/server/rawsyst-check.sh
```

Memory, swap, disk, inodes, load, every container's health and memory, Docker's
reclaimable space, log size, database connections and database size — each
against a threshold chosen for this machine. It changes nothing.

As a timer:

```ini
# /etc/systemd/system/rawsyst-check.service
[Unit]
Description=RawSyst resource check

[Service]
Type=oneshot
WorkingDirectory=/opt/rawsyst
ExecStart=/usr/bin/env bash deploy/server/rawsyst-check.sh --strict
```

```ini
# /etc/systemd/system/rawsyst-check.timer
[Unit]
Description=RawSyst resource check, hourly

[Timer]
OnCalendar=hourly
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
sudo systemctl enable --now rawsyst-check.timer
journalctl -u rawsyst-check.service --since today
```

`--strict` exits non-zero when something is over, so the unit fails and the
failure is visible in `systemctl --failed` rather than buried in a log nobody
opens.

---

## 8. Backups

The database is the only thing here that cannot be rebuilt from the repository.
**`deploy/server/BACKUP.md` is the whole of it** — what is in a snapshot, where
it goes, how it is proved, what is kept and what happens to secrets. The short
version:

```bash
cd /opt/rawsyst
C="docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup"

$C run --rm backup run       # take one
$C run --rm backup verify    # prove it restores, into a temporary database
$C run --rm backup list
```

It refuses to run without an object store configured, because a backup on the
same disk as the database is not a backup. Set `RAWSYST_S3_ENDPOINT`,
`RAWSYST_S3_BUCKET` and the credentials in `.env` before the first run.

**And a role that can actually read the database.** RawSyst forces row-level
security on every tenant table, which applies to the table's owner too, so the
application's role cannot dump and must not be given the attribute that would
let it. `BACKUP.md` has the six statements that create `rawsyst_backup`; set
`RAWSYST_BACKUP_DSN` to it. `backup run` checks this before it starts and names
what is wrong.

Then the timers. One takes a backup, verifies it and prunes, every night; one
takes a PHYSICAL copy of the cluster weekly and proves it recovers; the third
rehearses a whole recovery once a week, against disposable resources:

```bash
sudo cp deploy/server/rawsyst-backup.{service,timer}     /etc/systemd/system/
sudo cp deploy/server/rawsyst-basebackup.{service,timer} /etc/systemd/system/
sudo cp deploy/server/rawsyst-drill.{service,timer}      /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now   rawsyst-backup.timer rawsyst-basebackup.timer rawsyst-drill.timer
systemctl list-timers 'rawsyst-*'
```

Turning archiving on for a server that is **already trading** is
[PITR-ACTIVATION.md](PITR-ACTIVATION.md) rather than this section: the order
there is arranged so that a wrong bucket is found before it can fill the disk.

**The base backup timer is the one that makes the write-ahead log worth
archiving.** The log describes changes to pages and can only be replayed onto a
physical copy; without one, archiving ships segments that nothing can ever use.
`PITR.md` is the whole story, and

```bash
docker compose $C run --rm backup wal preflight
```

says in one command whether this server is set up to archive and to recover.

Three things worth saying plainly here, because they are the ones people get
wrong:

**A backup is not working because a file was created.** `verify` downloads the
snapshot, checks it against its manifest, restores it into a temporary database,
compares the schema version, every table by name, every row count, every
business's totals, the sequences, the extensions and every row-level-security
policy, and drops the temporary database. That is what the timer runs every
night, and it is the only thing that makes the word "backup" true.

**The button on the website needs the agent.** `backup-agent` is in the default
compose profile and comes up with everything else. Without it, Platform Admin →
Backup & Recovery queues work that nothing performs. `docker compose ps` should
list it.

**Do this before the business has any data in it.** A backup system first tested
on the day it is needed is a backup system nobody has tested. Run
`$C run --rm backup rehearse` once, now, and read what it says.

---

## 9. Before it carries a business

**[PREPRODUCTION.md](PREPRODUCTION.md), top to bottom, on this machine.**

Sections 1 to 8 above build a server that runs. That document is the separate
question of whether it can be recovered, and it is not the same question: a
server that serves perfectly and cannot be restored is a server that will one
day lose a shop's year.

It is ten steps and safe on a machine that is not yet serving. The ones that
are not optional:

- The application's database role must print `f | f` for superuser and
  bypassrls. If it does not, this server has no tenant isolation at all, and
  everything else is moot.
- `backup role` must end green, and must be re-runnable.
- A backup must be taken, listed **with the provider's own client** rather than
  only with this product, verified, and downloaded to somewhere off the server.
- The application-level drill in step 9: restore into a database beside the
  live one, start the product against it, and check that a second business
  still cannot see the first one's data — then take a new backup **from the
  restored database** and verify that too.

Record the elapsed times. They are your RPO and RTO, and a number you measured
on your own hardware is worth more than any number in a document.

### Production prerequisites, as a list

- [ ] `.env` complete, `chmod 600`, and stored in a secret store off this
      machine. [SECRETS.md](SECRETS.md) is the inventory.
- [ ] `RAWSYST_ENV=production`.
- [ ] Application role `NOSUPERUSER NOBYPASSRLS`.
- [ ] `rawsyst_backup` created and checked green.
- [ ] `RAWSYST_BACKUP_DSN` set, and its password in the secret store.
- [ ] An object store that is **not** this server, reachable, with a bucket.
- [ ] `RAWSYST_BACKUP_ENCRYPTION_KEY` decided: set and stored in two places,
      or deliberately left off with a reason. See [BACKUP.md](BACKUP.md).
- [ ] `RAWSYST_DATA_ENCRYPTION_KEYS` stored in two places. Losing it does not
      lose the database; it loses the sealed columns for ever.
- [ ] One backup taken, verified, and downloaded off the server.
- [ ] Both systemd timers enabled, and one backup forced by hand rather than
      waited for.
- [ ] `backup health` GREEN.
- [ ] The application-level drill done, with its times written down.
- [ ] `RAWSYST_ALLOW_PRODUCTION_RESTORE` left `false`.
- [ ] TLS terminating in front, and 8080 and 3000 on loopback only.

---

## 10. Moving to another server

`deploy/server/MIGRATION.md`, when the time comes. Read it before you need it:
the rehearsal it opens with is the difference between a ten-minute cutover and
an afternoon.

---

## What is deliberately not here

**No monitoring stack.** Prometheus, Grafana and a log shipper are together
larger than everything RawSyst runs, on a machine with 3.7 GiB. The API exposes
`/metrics` for the day there is somewhere to send it; until then an hourly
threshold check and `docker stats` are the right size.

**No automatic cleanup.** `docker system prune` is one flag away from deleting
the volume the database lives in. The check script prints the command; a person
runs it.

**No tuning that trades durability.** `fsync`, `synchronous_commit`,
`full_page_writes` and the data checksums set at initdb are all on and stay on.
A small machine is exactly where somebody is tempted, and exactly the one with
no replica to recover from.
