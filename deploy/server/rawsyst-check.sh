#!/usr/bin/env bash
#
# Is this server still healthy, and is anything about to fill up?
#
#   bash deploy/server/rawsyst-check.sh          # report, exit 0
#   bash deploy/server/rawsyst-check.sh --strict # exit 1 when over a threshold
#
# Meant for a timer. `--strict` makes it usable as one: a systemd
# OnFailure= or a cron job that only writes to the log when something is
# wrong. The runbook has the unit files.
#
# # What it will not do
#
# It does not clean anything up, restart anything or delete anything. A
# maintenance job that runs unattended and removes things is a maintenance job
# that removes the wrong thing at three in the morning, and the one command
# here that reclaims disk — `docker system prune` — is printed rather than run,
# because on this machine it is one flag away from deleting the database
# volume.
#
# It also does not touch the Go or npm caches. There are none on a server: this
# box runs images, it does not build them.

set -uo pipefail

strict=0
[ "${1:-}" = "--strict" ] && strict=1

# --- thresholds -------------------------------------------------------------
#
# Chosen for 3.7 GiB and 48 GB. Each is the point at which somebody should look,
# not the point at which something breaks — the gap between them is the whole
# value of checking.

readonly MEM_AVAIL_MIN_MIB=400     # below this a spike has nowhere to go
readonly DISK_FREE_MIN_GIB=8       # below this a restore will not fit
readonly DISK_PCT_MAX=85
readonly INODE_PCT_MAX=85
readonly LOAD_PER_CORE_MAX=2       # sustained, not a moment
readonly DOCKER_RECLAIM_MAX_MB=2048
readonly LOG_MAX_MB=256
readonly SWAP_USED_MAX_PCT=50      # swap in use is fine; swap thrashing is not
readonly PG_CONN_PCT_MAX=80
readonly BACKUP_MAX_AGE_HOURS=30      # a daily backup, plus slack for a slow night

over=0
line()  { printf '  %-34s %s\n' "$1" "$2"; }
flag()  { printf '  %-34s \033[31m%s\033[0m\n' "$1" "$2"; over=$((over + 1)); }
tip()   { printf '        \033[2m%s\033[0m\n' "$*"; }
have()  { command -v "$1" >/dev/null 2>&1; }

printf '\033[1mRawSyst server check\033[0m — %s\n\n' "$(date -u '+%Y-%m-%d %H:%M UTC')"

# --- memory -----------------------------------------------------------------

total=$(awk '/MemTotal/     {printf "%d", $2/1024}' /proc/meminfo)
avail=$(awk '/MemAvailable/ {printf "%d", $2/1024}' /proc/meminfo)
if [ "$avail" -ge "$MEM_AVAIL_MIN_MIB" ]; then
  line "memory available" "${avail} MiB of ${total}"
else
  flag "memory available" "${avail} MiB of ${total} (min ${MEM_AVAIL_MIN_MIB})"
  tip "docker stats --no-stream   # which container grew"
fi

swap_total=$(awk '/SwapTotal/ {printf "%d", $2/1024}' /proc/meminfo)
swap_free=$(awk '/SwapFree/   {printf "%d", $2/1024}' /proc/meminfo)
if [ "$swap_total" -eq 0 ]; then
  flag "swap" "none configured"
  tip "a memory spike is answered by killing a container; see deploy/server/RUNBOOK.md"
else
  used_pct=$(( (swap_total - swap_free) * 100 / swap_total ))
  if [ "$used_pct" -le "$SWAP_USED_MAX_PCT" ]; then
    line "swap used" "${used_pct}% of ${swap_total} MiB"
  else
    flag "swap used" "${used_pct}% of ${swap_total} MiB (max ${SWAP_USED_MAX_PCT}%)"
    tip "something is over its working set; check container limits"
  fi
fi

# --- disk -------------------------------------------------------------------

free_gib=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
pct=$(df --output=pcent / | tail -1 | tr -dc '0-9')
if [ "${free_gib:-0}" -ge "$DISK_FREE_MIN_GIB" ] && [ "${pct:-100}" -le "$DISK_PCT_MAX" ]; then
  line "disk /" "${free_gib} GiB free, ${pct}% used"
else
  flag "disk /" "${free_gib} GiB free, ${pct}% used"
  tip "du -xh --max-depth=2 /var 2>/dev/null | sort -h | tail -20"
fi

# `df -i --output=ipcent` is what this wants and some builds of coreutils
# refuse the combination — "options -i and --output are mutually exclusive" —
# which silently produced an empty reading. The plain form and a column is less
# elegant and works everywhere.
inodes=$(df -i / 2>/dev/null | awk 'NR==2 {gsub(/%/,"",$5); print $5}')
case "${inodes:-}" in
  # Not every filesystem has a fixed inode count. btrfs, ZFS and anything
  # mounted from a foreign kernel report a dash, and a dash is not a finding.
  ''|*[!0-9]*) line "inodes /" "not applicable on this filesystem" ;;
  *)
    if [ "$inodes" -le "$INODE_PCT_MAX" ]; then
      line "inodes /" "${inodes}% used"
    else
      flag "inodes /" "${inodes}% used (max ${INODE_PCT_MAX}%)"
      tip "a disk with free bytes and no inodes is a full disk"
    fi ;;
esac

# --- load -------------------------------------------------------------------

cores=$(nproc)
load1=$(awk '{print $1}' /proc/loadavg)
max=$(awk -v c="$cores" -v m="$LOAD_PER_CORE_MAX" 'BEGIN{printf "%.2f", c*m}')
if awk -v l="$load1" -v m="$max" 'BEGIN{exit !(l<=m)}'; then
  line "load" "${load1} over ${cores} cores"
else
  flag "load" "${load1} over ${cores} cores (max ${max})"
fi

# --- docker -----------------------------------------------------------------

if have docker && docker info >/dev/null 2>&1; then
  expected="db api web worker"
  for name in $expected; do
    id=$(docker ps -q --filter "name=${name}" | head -1)
    if [ -z "$id" ]; then
      flag "container ${name}" "not running"
      continue
    fi
    health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$id" 2>/dev/null)
    mem=$(docker stats --no-stream --format '{{.MemUsage}} ({{.MemPerc}})' "$id" 2>/dev/null)
    case "$health" in
      healthy|none) line "container ${name}" "${mem}${health:+  ${health}}" ;;
      *)            flag "container ${name}" "${mem}  ${health}"
                    tip "docker logs --tail 50 ${id}" ;;
    esac
  done

  reclaim_mb=$(docker system df --format '{{.Reclaimable}}' 2>/dev/null |
    head -1 | awk '{v=$1; u=$2;
      if (u ~ /GB/) v=v*1024; else if (u ~ /kB/) v=v/1024;
      printf "%d", v}')
  # The unit sometimes comes attached to the number rather than separated.
  [ -z "$reclaim_mb" ] && reclaim_mb=0
  if [ "$reclaim_mb" -le "$DOCKER_RECLAIM_MAX_MB" ]; then
    line "docker reclaimable" "${reclaim_mb} MB"
  else
    flag "docker reclaimable" "${reclaim_mb} MB (max ${DOCKER_RECLAIM_MAX_MB})"
    tip "docker image prune -a --filter until=168h   # images only, never volumes"
  fi

  logs_mb=$(du -sm /var/lib/docker/containers 2>/dev/null | awk '{print $1}')
  if [ -n "$logs_mb" ]; then
    if [ "$logs_mb" -le "$LOG_MAX_MB" ]; then
      line "container logs" "${logs_mb} MB"
    else
      flag "container logs" "${logs_mb} MB (max ${LOG_MAX_MB})"
      tip "the compose profile caps these; something is running outside it"
    fi
  fi

  # The database's own view of itself. Connections are the thing that runs out
  # first on a small box, and the container is where the answer lives.
  db=$(docker ps -q --filter "name=db" | head -1)
  if [ -n "$db" ]; then
    read -r used limit < <(docker exec "$db" psql -U "${POSTGRES_USER:-rawsyst}" \
      -d "${POSTGRES_DB:-rawsyst}" -tAc \
      "SELECT count(*), current_setting('max_connections') FROM pg_stat_activity" \
      2>/dev/null | tr '|' ' ')
    if [ -n "${used:-}" ] && [ -n "${limit:-}" ]; then
      pctc=$(( used * 100 / limit ))
      if [ "$pctc" -le "$PG_CONN_PCT_MAX" ]; then
        line "database connections" "${used} of ${limit}"
      else
        flag "database connections" "${used} of ${limit} (${pctc}%)"
        tip "check RAWSYST_DB_MAX_CONNS against max_connections"
      fi
    fi

    size=$(docker exec "$db" psql -U "${POSTGRES_USER:-rawsyst}" \
      -d "${POSTGRES_DB:-rawsyst}" -tAc \
      "SELECT pg_size_pretty(pg_database_size(current_database()))" 2>/dev/null)
    [ -n "$size" ] && line "database size" "$size"

    # The backup, and specifically the VERIFIED one.
    #
    # `status = 'succeeded'` says a file was written. `verified_at` says
    # somebody restored it and it was the database it claimed to be. Only the
    # second is protection, and reporting the first would be the comforting
    # number rather than the true one.
    read -r age loc < <(docker exec "$db" psql -U "${POSTGRES_USER:-rawsyst}" \
      -d "${POSTGRES_DB:-rawsyst}" -tAc \
      "SELECT round(extract(epoch from now() - verified_at) / 3600),
              coalesce(location, '-')
         FROM backup_record
        WHERE tenant_id IS NULL AND verified_at IS NOT NULL
        ORDER BY verified_at DESC LIMIT 1" 2>/dev/null | tr '|' ' ')

    if [ -z "${age:-}" ]; then
      flag "verified backup" "none on record"
      tip "docker compose -f docker-compose.yml -f docker-compose.server.yml --profile backup run --rm backup run"
      tip "then the same with 'verify'. See deploy/server/BACKUP.md"
    elif [ "${age%.*}" -le "$BACKUP_MAX_AGE_HOURS" ]; then
      line "verified backup" "${age%.*}h ago"
    else
      flag "verified backup" "${age%.*}h ago (max ${BACKUP_MAX_AGE_HOURS}h)"
      tip "systemctl status rawsyst-backup.timer"
      tip "journalctl -u rawsyst-backup.service --since '3 days ago'"
    fi

    # And whether the last ATTEMPT failed, which is a different question. A
    # week-old verified backup with three failed runs since is a system that
    # looks protected and is not.
    failed=$(docker exec "$db" psql -U "${POSTGRES_USER:-rawsyst}" \
      -d "${POSTGRES_DB:-rawsyst}" -tAc \
      "SELECT coalesce(left(error, 90), 'ok') FROM backup_record
        WHERE tenant_id IS NULL AND status = 'failed'
          AND started_at > now() - interval '3 days'
        ORDER BY started_at DESC LIMIT 1" 2>/dev/null)
    if [ -n "${failed:-}" ] && [ "$failed" != "ok" ]; then
      flag "last backup failure" "$failed"
    fi
  fi
else
  flag "docker" "not answering"
fi

printf '\n'
if [ "$over" -eq 0 ]; then
  printf '\033[32mAll within thresholds.\033[0m\n'
  exit 0
fi
printf '\033[31m%d over threshold.\033[0m Nothing was changed.\n' "$over"
[ "$strict" -eq 1 ] && exit 1
exit 0
