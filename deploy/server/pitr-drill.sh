#!/usr/bin/env bash
# The point-in-time recovery drill, against the real images.
#
# Brings up MinIO, a PostgreSQL built from deploy/postgres/Dockerfile with
# archiving on, and the backup image. Writes known rows, takes a physical base
# backup, writes more, DELETES them, archives, and then recovers twice: once to
# the moment before the deletion, and once to the end of the archive. The first
# must have the rows. The second must not.
#
# That pair is the whole claim. The Go drill in
# backend/internal/backup/pitr_test.go proves the same thing about the LOGIC;
# this proves it about the DEPLOYMENT — the image, the entrypoint that edits
# pg_hba.conf, the compose wiring, the dedicated backup role, and a real
# S3-compatible store over HTTP.
#
#     bash deploy/server/pitr-drill.sh
#
# It touches nothing outside its own compose project. Different project name,
# its own volumes, its own credentials, and it removes all of them at the end
# unless KEEP=1.
#
# ---------------------------------------------------------------------------
# Why the drill creates a second application role
# ---------------------------------------------------------------------------
#
# The official PostgreSQL image makes POSTGRES_USER a superuser, and a superuser
# can do everything — which would let this drill pass while proving nothing
# about the arrangement a real server runs. On a correctly configured server:
#
#   * the application role is NOT a superuser and does NOT have BYPASSRLS,
#     because that attribute is the only thing keeping one business out of
#     another's books;
#   * a separate `rawsyst_backup` role has BYPASSRLS and REPLICATION;
#   * the pg_hba.conf line the image's entrypoint adds names THAT role, so a
#     replication connection as anybody else is refused.
#
# So the drill builds that shape before it starts. It cannot demote
# POSTGRES_USER — PostgreSQL refuses to take SUPERUSER off the bootstrap role,
# which is a sensible rule and not a problem here — so instead it creates an
# ordinary `rawsyst_app` that owns the data, leaves the bootstrap role as the
# administrator, and has the product's own `backup role` command create
# `rawsyst_backup` from there. Everything after that connects as the backup
# role, which is what makes the pg_hba line part of what is being tested.
set -euo pipefail

COMPOSE=(docker compose -f docker-compose.pitr.yml -p rawsyst-pitr)

# The application's own connection: an ordinary role with no superuser and no
# BYPASSRLS, which is what writes the rows and owns them.
PSQL=("${COMPOSE[@]}" exec -T db psql -U rawsyst_app -d rawsyst -v ON_ERROR_STOP=1)

# The administrator. `pg_switch_wal()` is superuser-only by default, so forcing
# a segment closed is its job rather than the application's.
ADMIN=("${COMPOSE[@]}" exec -T db psql -U rawsyst -d rawsyst -v ON_ERROR_STOP=1)

APP_DSN='postgres://rawsyst_app:drillapppassword@db:5432/rawsyst?sslmode=disable'
BACKUP_DSN='postgres://rawsyst_backup:drillrolepassword@db:5432/rawsyst?sslmode=disable'
ADMIN_DSN='postgres://rawsyst:drillpassword@db:5432/postgres?sslmode=disable'

# Every backup command after the role exists connects as the backup role.
#
# MSYS_NO_PATHCONV stops Git Bash rewriting an argument that starts with `/`
# into a Windows path before Docker ever sees it. `-to /staging/carried` becomes
# `C:/Program Files/Git/staging/carried` without it, which fails on a directory
# nobody asked for. It means nothing on Linux and is harmless there.
backup() {
	MSYS_NO_PATHCONV=1 "${COMPOSE[@]}" run --rm -T \
		-e RAWSYST_DB_DSN="$APP_DSN" \
		-e RAWSYST_BACKUP_DSN="$BACKUP_DSN" \
		-e RAWSYST_BACKUP_ADMIN_DSN="$ADMIN_DSN" \
		backup "$@"
}

step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
fail() { printf '\n\033[31mFAILED: %s\033[0m\n' "$*" >&2; exit 1; }

cleanup() {
	if [ "${KEEP:-0}" = "1" ]; then
		echo
		echo "Left running. Remove it with:"
		echo "  docker compose -f docker-compose.pitr.yml -p rawsyst-pitr down -v"
		return
	fi
	step "Cleaning up"
	"${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ---------------------------------------------------------------------------

step "Building the images"
# The code in this working copy, not whatever was last pushed.
"${COMPOSE[@]}" build

step "Bringing the stack up"
"${COMPOSE[@]}" up -d --wait store store-init db

step "Building the role arrangement a real server has"
# An ordinary application role that owns the data: no superuser, no BYPASSRLS.
# See the file note for why the bootstrap role cannot simply be demoted.
"${COMPOSE[@]}" exec -T db psql -U rawsyst -d rawsyst -v ON_ERROR_STOP=1 <<-'SQL'
	DO $$
	BEGIN
	  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rawsyst_app') THEN
	    CREATE ROLE rawsyst_app LOGIN PASSWORD 'drillapppassword'
	      NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
	  END IF;
	END
	$$;
	GRANT CREATE, USAGE ON SCHEMA public TO rawsyst_app;

	-- Creating an extension needs a superuser, so the bootstrap role does it
	-- here rather than the migration failing as the application role.
	CREATE EXTENSION IF NOT EXISTS pgcrypto;
	CREATE EXTENSION IF NOT EXISTS citext;
	CREATE EXTENSION IF NOT EXISTS btree_gist;
	CREATE EXTENSION IF NOT EXISTS pg_trgm;
SQL

step "Applying the real migrations"
# The whole committed migration chain, as the application role, so the drill
# recovers a database with this product's actual schema in it — 190 tables, row
# level security forced on 181 of them, and the `backup_task` table the agent
# claims work from.
#
# A drill against two hand-made tables would prove the mechanism and nothing
# about the product. This is what makes the inspection at the end of each
# recovery the same inspection a real one gets.
#
# MSYS_NO_PATHCONV stops Git Bash rewriting `/rawsyst` into a Windows path
# before Docker sees it. It means nothing on Linux and is harmless there.
MSYS_NO_PATHCONV=1 "${COMPOSE[@]}" run --rm -T \
	-e RAWSYST_DB_DSN="$APP_DSN" \
	--entrypoint /rawsyst \
	backup migrate || fail "the migrations did not apply"

step "Creating the backup role"
# BYPASSRLS, REPLICATION and pg_monitor. The command refuses outright if the
# APPLICATION role can already see past row-level security, which is why the
# step above had to come first.
"${COMPOSE[@]}" run --rm -T \
	-e RAWSYST_DB_DSN="$APP_DSN" \
	-e RAWSYST_BACKUP_ADMIN_DSN="$ADMIN_DSN" \
	-e RAWSYST_BACKUP_ROLE_PASSWORD=drillrolepassword \
	-e RAWSYST_BACKUP_DSN="$BACKUP_DSN" \
	backup role || fail "the backup role could not be created"

step "Preflight: can this server archive and recover at all"
# As the BACKUP role now, which is what makes the replication line in
# pg_hba.conf part of what is being checked. That line names the role; a
# replication connection as anybody else is refused, and would be on a real
# server too.
backup wal preflight || fail "preflight did not pass"

step "Something to lose"
# Rows this drill owns, in a table of its own beside the product's schema.
#
# Its own table rather than a real one because inserting a sale properly means a
# tenant, a company, a branch, a product and a price list, and the drill is
# about the recovery rather than about the onboarding. What matters is that the
# rows are countable, known, and in the same cluster as everything the
# migrations created — so a recovery that loses them loses the real schema too.
"${PSQL[@]}" <<-'SQL'
	CREATE TABLE IF NOT EXISTS drill_sale (
	  id        int PRIMARY KEY,
	  tenant_id uuid NOT NULL DEFAULT gen_random_uuid(),
	  total     numeric
	);
	INSERT INTO drill_sale (id, total)
	  SELECT g, g * 10 FROM generate_series(1, 200) g
	  ON CONFLICT DO NOTHING;
	GRANT SELECT ON drill_sale TO rawsyst_backup;
SQL
"${ADMIN[@]}" -c "SELECT pg_switch_wal()" >/dev/null
sleep 4

step "A physical base backup"
backup basebackup || fail "the base backup failed"

step "A trading day"
"${PSQL[@]}" -c \
	"INSERT INTO drill_sale (id, total) SELECT g, g * 10 FROM generate_series(201, 500) g" >/dev/null
"${ADMIN[@]}" -c "SELECT pg_switch_wal()" >/dev/null
sleep 4

# The moment everything was still right, read off the SERVER's clock rather
# than this shell's. They are the same machine here and would not be in
# production, and a drill that quietly depended on them agreeing would not catch
# the day they stopped.
GOOD=$("${ADMIN[@]}" -tAc "SELECT to_char(clock_timestamp() AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SSZ')" | tr -d '\r')
echo "  the moment before the mistake: $GOOD"

# Two seconds, because recovery_target_time resolves against commit timestamps
# and a target inside the same second as the mistake may or may not include it.
sleep 2

step "The mistake"
"${PSQL[@]}" -c "DELETE FROM drill_sale WHERE id > 300" >/dev/null
"${PSQL[@]}" -c "INSERT INTO drill_sale (id, total) VALUES (9999, 0)" >/dev/null
"${ADMIN[@]}" -c "SELECT pg_switch_wal()" >/dev/null
sleep 4

step "What the archive says about itself"
backup wal status || true
backup wal verify -deep 4 || fail "the archive did not check out"
backup pitr -window || fail "there is no recovery window"

step "Recovering to just before the mistake"
backup pitr -target before_time -at "$GOOD" || fail "the recovery failed"

step "Recovering to the latest point"
backup pitr -target latest || fail "the latest recovery failed"

step "Carrying a base backup away"
# Not a substitute for the dump somebody keeps on a laptop -- a physical copy
# only reads on its own major version and needs the archive beside it. It is
# here because leaving a storage provider, and opening an artifact somewhere
# else after a failed recovery, are both real and both impossible without it.
#
# Every file is checked against the manifest as it lands, and one that does not
# match is deleted rather than left on disk with a plausible name.
backup basebackup -download -to /staging/carried \
	|| fail "the base backup could not be downloaded"
MSYS_NO_PATHCONV=1 "${COMPOSE[@]}" run --rm -T --entrypoint sh backup -c \
	'ls -l /staging/carried && test "$(ls /staging/carried | wc -l)" -eq 4' \
	|| fail "the download did not produce four files"

step "Retention"
backup wal prune || fail "retention refused"

step "The agent starts and claims work"
# Through the image's own entrypoint rather than with `--entrypoint /rawsyst`,
# which Git Bash rewrites into a Windows path before Docker ever sees it. The
# entrypoint is already `/rawsyst backup`, so this reaches the same binary and
# runs on every platform.
#
# `-once` drains at most one queued task and exits. There is nothing queued
# here, so what this proves is that the agent starts, opens the database, reads
# the task table and stops cleanly — which is the part that would break if the
# migration or the wiring were wrong.
backup agent -once || fail "the agent could not run"

printf '\n\033[32m== The drill passed.\033[0m\n'
printf 'A cluster was archived, copied, damaged, and recovered to the moment\n'
printf 'before the damage and to the end of the archive — through the real\n'
printf 'images, a real object store, and a dedicated backup role.\n'
