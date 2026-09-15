#!/usr/bin/env bash
# Restore the demo database from the golden catalogue snapshot.
# Runs as a systemd oneshot; not invoked from the public site.
set -euo pipefail

SNAPSHOT=/var/lib/goen/snapshots/catalog.dump
SERVICE=goen.service

systemctl stop "$SERVICE"

pg_restore --clean --if-exists --no-owner --exit-on-error \
	-d "$GOEN_DATABASE_URL" "$SNAPSHOT"

systemctl start "$SERVICE"
