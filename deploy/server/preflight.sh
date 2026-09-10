#!/usr/bin/env bash
#
# What this machine looks like, before RawSyst is put on it.
#
#   sudo bash deploy/server/preflight.sh
#
# # What it does and does not do
#
# It reads. It does not install, configure, enable, restart or delete anything,
# and it never will: a script that fixes a server it has not been allowed to
# look at first is how a working box becomes a broken one. Where something
# needs changing it prints the command, and a person decides.
#
# Exit status is 0 when nothing is wrong, 1 when something needs a decision
# before deploying, and 2 when the script could not do its job.
#
# # Why the thresholds are what they are
#
# They are read from the compose profile this repository ships for a small
# server: `docker-compose.server.yml` sets ceilings adding to 1.63 GiB, and the
# checks below ask whether this machine can carry that with the operating
# system's own needs on top. Change the profile and change these together.

set -uo pipefail

# --- what the deployment expects -------------------------------------------

# The sum of the container memory ceilings in docker-compose.server.yml.
readonly WANT_LIMITS_MIB=1664
# Below this, the ceilings do not fit with room for the page cache.
readonly MIN_RAM_MIB=3000
# Below this, a build or a restore has nowhere to go.
readonly MIN_DISK_GIB=15
# Two cores is what the profile is tuned for; one will run but will queue.
readonly MIN_CORES=2

readonly PORTS_EXPECTED="22 80 443"

problems=0
notes=0

say()   { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok()    { printf '  \033[32mok\033[0m    %s\n' "$*"; }
warn()  { printf '  \033[33mnote\033[0m  %s\n' "$*"; notes=$((notes + 1)); }
bad()   { printf '  \033[31mFIX\033[0m   %s\n' "$*"; problems=$((problems + 1)); }
fix()   { printf '        \033[2m%s\033[0m\n' "$*"; }

have()  { command -v "$1" >/dev/null 2>&1; }

printf '\033[1mRawSyst server preflight\033[0m — %s\n' "$(date -u '+%Y-%m-%d %H:%M UTC')"
printf 'host %s · %s\n' "$(hostname)" "$(. /etc/os-release 2>/dev/null && echo "${PRETTY_NAME:-unknown}")"

# --- the machine ------------------------------------------------------------

say "Processor and memory"

cores=$(nproc 2>/dev/null || echo 0)
if [ "$cores" -ge "$MIN_CORES" ]; then
  ok "$cores cores"
else
  warn "$cores core: the profile assumes $MIN_CORES. It will run and it will queue."
fi

ram_mib=$(awk '/MemTotal/ {printf "%d", $2/1024}' /proc/meminfo)
avail_mib=$(awk '/MemAvailable/ {printf "%d", $2/1024}' /proc/meminfo)
if [ "$ram_mib" -ge "$MIN_RAM_MIB" ]; then
  ok "${ram_mib} MiB total, ${avail_mib} MiB available"
else
  bad "${ram_mib} MiB total; the ceilings alone are ${WANT_LIMITS_MIB} MiB"
  fix "lower the limits in docker-compose.server.yml, or add memory"
fi

headroom=$((avail_mib - WANT_LIMITS_MIB))
if [ "$headroom" -ge 512 ]; then
  ok "${headroom} MiB left over the container ceilings, for the page cache"
elif [ "$headroom" -ge 0 ]; then
  warn "only ${headroom} MiB over the ceilings; the database will have little page cache"
else
  bad "the ceilings exceed available memory by $(( -headroom )) MiB"
fi

# Swap. Not a performance feature — a spike absorber. Without it the kernel's
# only answer to a moment of pressure is to kill the largest thing running,
# which on this box is Postgres.
swap_mib=$(awk '/SwapTotal/ {printf "%d", $2/1024}' /proc/meminfo)
if [ "$swap_mib" -ge 1024 ]; then
  ok "${swap_mib} MiB swap"
  swappiness=$(cat /proc/sys/vm/swappiness 2>/dev/null || echo "?")
  if [ "$swappiness" != "?" ] && [ "$swappiness" -gt 20 ]; then
    warn "vm.swappiness is $swappiness; 10 keeps the database in RAM and swap for emergencies"
    fix "echo 'vm.swappiness=10' | sudo tee /etc/sysctl.d/99-rawsyst.conf && sudo sysctl --system"
  fi
else
  bad "no swap: a memory spike is answered by killing a container, usually the database"
  fix "sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile && sudo mkswap /swapfile"
  fix "sudo swapon /swapfile && echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab"
  fix "echo 'vm.swappiness=10' | sudo tee /etc/sysctl.d/99-rawsyst.conf && sudo sysctl --system"
fi

# --- storage ----------------------------------------------------------------

say "Storage"

root_avail_gib=$(df -BG --output=avail / 2>/dev/null | tail -1 | tr -dc '0-9')
root_pct=$(df --output=pcent / 2>/dev/null | tail -1 | tr -dc '0-9')
if [ "${root_avail_gib:-0}" -ge "$MIN_DISK_GIB" ]; then
  ok "${root_avail_gib} GiB free on /, ${root_pct}% used"
else
  bad "${root_avail_gib:-?} GiB free on /: a build, a backup or a restore needs room"
fi

# /var is where Docker and the journal live, and it is usually the thing that
# fills. Reported separately when it is its own filesystem.
if [ "$(findmnt -no TARGET --target /var 2>/dev/null)" = "/var" ]; then
  var_avail=$(df -BG --output=avail /var | tail -1 | tr -dc '0-9')
  if [ "${var_avail:-0}" -ge 10 ]; then
    ok "/var is its own filesystem, ${var_avail} GiB free"
  else
    bad "/var has ${var_avail:-?} GiB free and holds every image, volume and log"
  fi
else
  ok "/var is on the root filesystem"
fi

inodes_pct=$(df -i --output=ipcent / 2>/dev/null | tail -1 | tr -dc '0-9')
if [ "${inodes_pct:-0}" -lt 80 ]; then
  ok "inodes ${inodes_pct}% used"
else
  bad "inodes ${inodes_pct}% used: a disk with free bytes and no inodes is a full disk"
fi

if have journalctl; then
  journal=$(journalctl --disk-usage 2>/dev/null | grep -o '[0-9.]*[MG]' | tail -1)
  if [ -n "$journal" ]; then
    case "$journal" in
      *G) warn "the systemd journal is using $journal"
          fix "sudo journalctl --vacuum-size=200M"
          fix "and cap it: SystemMaxUse=200M in /etc/systemd/journald.conf" ;;
      *)  ok "systemd journal using $journal" ;;
    esac
  fi
fi

# --- docker -----------------------------------------------------------------

say "Docker"

if have docker; then
  ok "docker $(docker --version 2>/dev/null | awk '{print $3}' | tr -d ,)"
  if docker compose version >/dev/null 2>&1; then
    ok "compose plugin $(docker compose version --short 2>/dev/null)"
  else
    bad "the compose plugin is missing; this deployment is a compose file"
    fix "sudo apt-get install -y docker-compose-plugin"
  fi

  if docker info >/dev/null 2>&1; then
    driver=$(docker info --format '{{.Driver}}' 2>/dev/null)
    if [ "$driver" = "overlay2" ]; then
      ok "storage driver overlay2"
    else
      warn "storage driver is $driver; overlay2 is what this is tested on"
    fi

    # A default log driver with no cap will fill /var eventually. The compose
    # file caps every service it defines, but anything run outside it inherits
    # the daemon's default.
    if [ -f /etc/docker/daemon.json ] && grep -q 'max-size' /etc/docker/daemon.json 2>/dev/null; then
      ok "the daemon caps container logs by default"
    else
      warn "the daemon has no default log cap; containers started outside compose can fill /var"
      fix "see deploy/server/daemon.json — copy it to /etc/docker/daemon.json and restart docker"
    fi

    reclaimable=$(docker system df --format '{{.Reclaimable}}' 2>/dev/null | head -1)
    [ -n "$reclaimable" ] && ok "docker images: ${reclaimable} reclaimable"
  else
    bad "docker is installed and the daemon is not answering"
    fix "sudo systemctl enable --now docker"
  fi
else
  bad "docker is not installed"
  fix "curl -fsSL https://get.docker.com | sudo sh    # or the distribution's package"
  fix "sudo usermod -aG docker \$USER   # then log out and back in"
fi

# --- the network the box presents ------------------------------------------

say "Firewall and open ports"

if have ufw; then
  if ufw status 2>/dev/null | grep -qi '^Status: active'; then
    ok "ufw is active"
    ufw status numbered 2>/dev/null | sed -n '3,12p' | sed 's/^/        /'
  else
    bad "ufw is installed and inactive"
    fix "sudo ufw allow OpenSSH && sudo ufw allow 80/tcp && sudo ufw allow 443/tcp"
    fix "sudo ufw enable"
  fi
elif have nft && nft list ruleset 2>/dev/null | grep -q 'policy drop'; then
  ok "nftables has a drop policy"
else
  bad "no firewall found: every published port is open to the internet"
  fix "sudo apt-get install -y ufw   # then the allow/enable above"
fi

if have ss; then
  listening=$(ss -tlnH 2>/dev/null | awk '{print $4}' | sed 's/.*://' | sort -un | tr '\n' ' ')
  ok "listening: ${listening:-none}"
  for p in $listening; do
    case " $PORTS_EXPECTED " in
      *" $p "*) ;;
      *)
        # Anything bound only to loopback is not exposed and is not a finding.
        if ss -tlnH "sport = :$p" 2>/dev/null | awk '{print $4}' | grep -qvE '^(127\.|\[::1\])'; then
          warn "port $p listens on a public address and is not one of: $PORTS_EXPECTED"
        fi ;;
    esac
  done
fi

# RawSyst publishes 8080 and 3000 in the compose file. On a server they belong
# behind a reverse proxy on 443, not on the open internet.
if have ss && ss -tlnH 2>/dev/null | grep -qE '0\.0\.0\.0:(8080|3000)'; then
  warn "8080 or 3000 is published on all interfaces"
  fix "bind them to 127.0.0.1 in compose and put a TLS reverse proxy in front"
fi

# --- ssh --------------------------------------------------------------------

say "SSH"

sshd_config() {
  if have sshd; then sshd -T 2>/dev/null | grep -i "^$1 " | awk '{print $2}'; fi
}
if have sshd; then
  root_login=$(sshd_config permitrootlogin)
  case "$root_login" in
    no|prohibit-password|forced-commands-only) ok "PermitRootLogin $root_login" ;;
    "") warn "could not read the effective sshd configuration" ;;
    *)  bad "PermitRootLogin $root_login"
        fix "set PermitRootLogin prohibit-password in /etc/ssh/sshd_config" ;;
  esac

  pw_auth=$(sshd_config passwordauthentication)
  case "$pw_auth" in
    no) ok "password authentication is off" ;;
    "") warn "could not read the effective sshd configuration" ;;
    *)  bad "password authentication is on: this host is on the public internet"
        fix "add a key, then set PasswordAuthentication no and reload sshd" ;;
  esac
else
  warn "sshd is not installed here; skipping"
fi

# --- the operating system's own health -------------------------------------

say "System"

if have systemctl; then
  failed=$(systemctl list-units --state=failed --no-legend --plain 2>/dev/null | wc -l)
  if [ "$failed" -eq 0 ]; then
    ok "no failed units"
  else
    bad "$failed failed systemd units"
    systemctl list-units --state=failed --no-legend --plain 2>/dev/null | sed 's/^/        /'
  fi
fi

if [ -f /var/run/reboot-required ]; then
  bad "a reboot is pending"
  fix "reboot before deploying, not after: a kernel change mid-service is an outage nobody chose"
else
  ok "no reboot pending"
fi

if have apt-get; then
  updates=$(apt-get -s upgrade 2>/dev/null | grep -c '^Inst ')
  security=$(apt-get -s upgrade 2>/dev/null | grep '^Inst ' | grep -ci security)
  if [ "$security" -gt 0 ]; then
    bad "$updates updates pending, $security of them security"
    fix "sudo apt-get update && sudo apt-get upgrade -y"
  elif [ "$updates" -gt 0 ]; then
    warn "$updates updates pending, none security"
  else
    ok "no pending updates"
  fi
fi

load=$(awk '{print $1}' /proc/loadavg)
ok "load ${load} over ${cores} cores"

# --- what is running now ----------------------------------------------------

if have docker && docker info >/dev/null 2>&1; then
  running=$(docker ps --format '{{.Names}}' 2>/dev/null | wc -l)
  if [ "$running" -gt 0 ]; then
    say "Containers"
    docker stats --no-stream --format '  {{.Name}}  {{.MemUsage}}  {{.MemPerc}}  cpu {{.CPUPerc}}' 2>/dev/null
  fi
fi

# --- verdict ----------------------------------------------------------------

printf '\n'
if [ "$problems" -eq 0 ] && [ "$notes" -eq 0 ]; then
  printf '\033[32mReady.\033[0m Nothing needs a decision before deploying.\n'
  exit 0
fi
if [ "$problems" -eq 0 ]; then
  printf '\033[33m%d note(s), nothing blocking.\033[0m\n' "$notes"
  exit 0
fi
printf '\033[31m%d thing(s) to decide, %d note(s).\033[0m\n' "$problems" "$notes"
printf 'Nothing above was changed. Each FIX line is a command somebody runs.\n'
exit 1
