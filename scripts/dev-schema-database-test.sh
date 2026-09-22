#!/usr/bin/env bash
# Read-only probe against the database just migrated by the CI schema job.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
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
