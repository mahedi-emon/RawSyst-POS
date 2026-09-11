#!/bin/sh
# The same line, for a cluster being created right now.
#
# `entrypoint.sh` adds it on every start and covers servers that already exist.
# This covers the first start, where `$PGDATA/pg_hba.conf` did not exist when
# the wrapper looked — initdb writes it after that and before this runs.
#
# Both are idempotent and both look for the same marker, so whichever gets
# there first is the one that writes it.
set -e

ROLE="${RAWSYST_BACKUP_ROLE:-rawsyst_backup}"
MARKER="# rawsyst: replication for the backup role"
HBA="${PGDATA:-/var/lib/postgresql/data}/pg_hba.conf"

if [ -f "$HBA" ] && ! grep -qF "$MARKER" "$HBA"; then
	{
		echo ""
		echo "$MARKER"
		echo "# pg_basebackup connects to the pseudo-database 'replication',"
		echo "# which the ordinary 'host all all' line does not cover."
		echo "host    replication    $ROLE    all    scram-sha-256"
	} >>"$HBA"
fi
