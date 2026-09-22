#!/usr/bin/env bash
# This fingerprint belongs to disposable development databases, never startup.
set -euo pipefail
cd "$(dirname "$0")/.."
: "${GOEN_DATABASE_URL:?GOEN_DATABASE_URL is required}"

fingerprint() {
    for migration in migrations/*.up.sql; do
        printf '%s\n' "$migration"
        cat "$migration"
    done | openssl dgst -sha256 | awk '{print $NF}'
}

query() {
    psql "$GOEN_DATABASE_URL" -X -qAt -v ON_ERROR_STOP=1 "$@"
}

refuse() {
    echo 'development schema is unknown or stale; preserve any needed data, then run make db-reset' >&2
    exit 1
}

check_applied_state() {
    local exists applied
    exists=$(query -c "SELECT to_regclass('public.schema_migrations') IS NOT NULL")
    [[ "$exists" == t ]] || refuse
    applied=$(query -c "SELECT version::text || ':' || dirty::text FROM public.schema_migrations")
    [[ "$applied" == "$expected_version:false" ]] || refuse
}

check() {
    local exists stored
    check_applied_state
    exists=$(query -c "SELECT to_regclass('goen_dev.schema_fingerprint') IS NOT NULL")
    [[ "$exists" == t ]] || refuse
    stored=$(query -c 'SELECT digest FROM goen_dev.schema_fingerprint WHERE singleton')
    [[ "$stored" == "$digest" ]] || refuse
}

expected_version=0
for migration in migrations/*.up.sql; do
    version=${migration##*/}
    version=${version%%[_.]*}
    version=$((10#$version))
    (( version <= expected_version )) || expected_version=$version
done
digest=$(fingerprint)
case "${1:-}" in
    check)
        check
        ;;
    apply)
        shift
        [[ $# -gt 0 ]] || { echo 'apply requires a migration command' >&2; exit 2; }
        exists=$(query -c "SELECT to_regclass('public.schema_migrations') IS NOT NULL")
        if [[ "$exists" == t ]]; then
            applied=$(query -c 'SELECT count(*) FROM public.schema_migrations')
            [[ "$applied" == 0 ]] || check
        fi
        "$@"
        check_applied_state
        [[ "$(fingerprint)" == "$digest" ]] || { echo 'migration files changed during application; fingerprint not recorded' >&2; exit 1; }
        query -v digest="$digest" <<'SQL'
CREATE SCHEMA IF NOT EXISTS goen_dev;
CREATE TABLE IF NOT EXISTS goen_dev.schema_fingerprint (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    digest text NOT NULL
);
INSERT INTO goen_dev.schema_fingerprint (digest) VALUES (:'digest')
ON CONFLICT (singleton) DO UPDATE SET digest = EXCLUDED.digest;
SQL
        ;;
    *)
        echo 'usage: scripts/dev-schema.sh check | apply <migration command...>' >&2
        exit 2
        ;;
esac
