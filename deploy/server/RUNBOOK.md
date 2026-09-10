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
    reverse_proxy 127.0.0.1:3000
}
```

The back office proxies `/api/v1` to the API itself, so only one upstream is
needed here. Reload with `sudo systemctl reload caddy`.

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

The database is the only thing on this machine that cannot be rebuilt from the
repository. Everything else — images, containers, configuration — is a `git
clone` and a `docker compose build` away.

```bash
#!/usr/bin/env bash
# /opt/rawsyst/backup.sh
set -euo pipefail
out="/var/backups/rawsyst"
mkdir -p "$out"
stamp=$(date -u +%Y%m%dT%H%M%SZ)

docker exec "$(docker ps -qf name=db)" \
  pg_dump -U rawsyst -d rawsyst --format=custom --compress=9 \
  > "$out/rawsyst-$stamp.dump"

# Keep a fortnight. A backup policy nobody prunes is a disk that fills.
find "$out" -name 'rawsyst-*.dump' -mtime +14 -delete
```

Two things make this a backup rather than a file:

1. **It leaves the machine.** A dump on the same disk as the database survives
   a mistake and not a disk. Copy it off — object storage, another host,
   anywhere that is not here.
2. **It is restored.** On a spare database, on a schedule, by somebody who
   watches it finish. An untested backup is a belief.

```bash
pg_restore -U rawsyst -d rawsyst_restore_test --clean --if-exists rawsyst-….dump
```

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
