#!/bin/sh
# One line of pg_hba.conf, and then the image's own entrypoint.
#
# ---------------------------------------------------------------------------
# Why this wrapper exists
# ---------------------------------------------------------------------------
#
# `pg_basebackup` copies the cluster over a REPLICATION connection, and a
# replication connection needs its own line in `pg_hba.conf`. The ordinary
#
#     host all all all scram-sha-256
#
# that the official image appends does NOT cover it: `all` in the second column
# means all DATABASES, and `replication` is not a database. So without the line
# below, the first base backup fails — twenty minutes in, with
#
#     FATAL: no pg_hba.conf entry for replication connection from host "172.18.0.5"
#
# which is a true sentence about the twentieth thing anybody would check.
#
# ---------------------------------------------------------------------------
# Why it is here and not only in /docker-entrypoint-initdb.d
# ---------------------------------------------------------------------------
#
# Scripts in that directory run exactly once, when the cluster is first
# created. Every deployment that already exists has a `$PGDATA` and would never
# run one — so turning point-in-time recovery on for an existing server would
# mean an operator editing a file inside a volume by hand, at the moment they
# are least able to check their work.
#
# This runs on every start and is idempotent: it looks for the marker comment
# and does nothing at all if it is there. The cost is one `grep` per container
# start.
#
# ---------------------------------------------------------------------------
# Why the line names the role
# ---------------------------------------------------------------------------
#
# `host replication all all` would admit every role that can log in to stream a
# byte-level copy of every business on this server. The backup role is the only
# one that needs it and it is the only one named. Change RAWSYST_BACKUP_ROLE
# and this follows it.
set -e

ROLE="${RAWSYST_BACKUP_ROLE:-rawsyst_backup}"
MARKER="# biz1core: replication for the backup role"
HBA="${PGDATA:-/var/lib/postgresql/data}/pg_hba.conf"

# A cluster that has not been created yet has no pg_hba.conf. That case is
# handled by 10-replication-hba.sh, which initdb runs after it writes one.
if [ -f "$HBA" ] && ! grep -qF "$MARKER" "$HBA"; then
	{
		echo ""
		echo "$MARKER"
		echo "# pg_basebackup connects to the pseudo-database 'replication',"
		echo "# which the ordinary 'host all all' line does not cover."
		echo "host    replication    $ROLE    all    scram-sha-256"
	} >>"$HBA"
	echo "biz1core: added a replication line to pg_hba.conf for $ROLE" >&2
fi

exec docker-entrypoint.sh "$@"
