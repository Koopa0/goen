#!/usr/bin/env bash
# The schema job owns this disposable database; never run against developer data.
set -euo pipefail
[[ ${CI:-} == true ]] || { echo 'database transition probe requires CI' >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/scripts" "$tmp/migrations"
cp "$root/scripts/dev-schema.sh" "$tmp/scripts/"
cp "$root"/migrations/*.up.sql "$tmp/migrations/"
"$tmp/scripts/dev-schema.sh" check
migration=$(find "$tmp/migrations" -name '*.up.sql' -print -quit)
printf '\n-- fingerprint mismatch probe\n' >> "$migration"
if "$tmp/scripts/dev-schema.sh" check >"$tmp/output" 2>&1; then
    echo 'changed migration bytes accepted against recorded database fingerprint' >&2
    exit 1
fi
grep -q 'development schema is unknown or stale' "$tmp/output"
"$root/scripts/dev-schema.sh" check
echo 'real PostgreSQL fingerprint: matching bytes accepted, changed copy refused, original still accepted'

query() {
    psql "$GOEN_DATABASE_URL" -X -qAt -v ON_ERROR_STOP=1 -c "$1"
}
reject() {
    if "$@" >"$tmp/output" 2>&1; then
        echo "unexpected success: $*" >&2
        exit 1
    fi
    grep -q 'development schema is unknown or stale' "$tmp/output"
}
recorded=$(query 'SELECT digest FROM goen_dev.schema_fingerprint WHERE singleton')
make migrate-down
[[ "$(query 'SELECT digest FROM goen_dev.schema_fingerprint WHERE singleton')" == "$recorded" ]]
reject "$root/scripts/dev-schema.sh" check
echo 'real PostgreSQL migration-down: retained digest refused'
make migrate-up
"$root/scripts/dev-schema.sh" check
query 'UPDATE public.schema_migrations SET dirty = true'
reject "$root/scripts/dev-schema.sh" check
reject "$root/scripts/dev-schema.sh" apply touch "$tmp/incorrectly-invoked"
[[ ! -e "$tmp/incorrectly-invoked" ]]
[[ "$(query 'SELECT digest FROM goen_dev.schema_fingerprint WHERE singleton')" == "$recorded" ]]
echo 'real PostgreSQL dirty migration: check and apply refused without rebaseline'
query 'UPDATE public.schema_migrations SET dirty = false'
"$root/scripts/dev-schema.sh" check
echo 'real PostgreSQL migration state: restored clean state accepted'
