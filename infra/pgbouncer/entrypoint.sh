#!/bin/sh
set -eu
envsubst < /etc/pgbouncer/pgbouncer.ini.template > /tmp/pgbouncer.ini
printf '"%s" "%s"\n' "$POSTGRES_USER" "$POSTGRES_PASSWORD" > /tmp/userlist.txt
exec pgbouncer /tmp/pgbouncer.ini
