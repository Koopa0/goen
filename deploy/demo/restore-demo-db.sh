#!/usr/bin/env bash
# The restore identity belongs only to this operator-run oneshot.
set -euo pipefail

: "${GOEN_RESTORE_DATABASE_URL:?GOEN_RESTORE_DATABASE_URL is required; never use storefront credentials}"
SNAPSHOT=${GOEN_RESTORE_SNAPSHOT:-/var/lib/goen/snapshots/catalog.dump}
SERVICE=goen.service
stopped=false
restored=false

finish() {
    status=$?
    if [[ "$status" -ne 0 && "$stopped" == true ]]; then
        if [[ "$restored" == true ]]; then
            echo 'restore completed but service start failed; operator recovery required' >&2
        else
            # A failed single-transaction restore changed nothing, so the
            # previous data is intact and the storefront goes back up on it.
            echo "restore failed and rolled back; restarting $SERVICE on the previous data" >&2
            if ! systemctl start "$SERVICE"; then
                echo 'restore failed and the restart also failed; operator recovery required' >&2
            fi
        fi
    fi
    exit "$status"
}
trap finish EXIT

# These checks happen before taking the storefront down. A separately named
# variable cannot make a storefront login into a restore identity.
identity=$(psql "$GOEN_RESTORE_DATABASE_URL" -X -qAt -v ON_ERROR_STOP=1 \
    -c 'SELECT session_user')
case "$identity" in
    ''|store|store_svc|admin|admin_svc|maintenance|maintenance_svc|reporting|reporting_svc)
        echo 'restore requires a dedicated operator identity, not an application role' >&2
        exit 2
        ;;
esac
pg_restore --list "$SNAPSHOT" >/dev/null

systemctl stop "$SERVICE"
stopped=true
# One transaction: a failed statement rolls everything back, which is what makes
# restarting on the old data safe. --role makes the owner (goen) the creator and
# dropper of every object, so no object changes owner; never add --no-owner,
# which would hand every SECURITY DEFINER function to the login.
pg_restore --single-transaction --clean --if-exists --role=goen --exit-on-error \
    -d "$GOEN_RESTORE_DATABASE_URL" "$SNAPSHOT"
restored=true
systemctl start "$SERVICE"
stopped=false
