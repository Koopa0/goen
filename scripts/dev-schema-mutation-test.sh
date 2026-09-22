#!/usr/bin/env bash
# Mutate a copied production script so the working tree remains untouched.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/scripts"
cp "$root/scripts/dev-schema.sh" "$root/scripts/dev-schema-test.sh" "$tmp/scripts/"
python3 - "$tmp/scripts/dev-schema.sh" <<'PY'
import pathlib, sys
path = pathlib.Path(sys.argv[1])
source = path.read_text()
needle = '[[ "$stored" == "$digest" ]] || refuse'
if source.count(needle) != 1:
    raise SystemExit('fingerprint mutation did not match exactly once')
path.write_text(source.replace(needle, 'true'))
PY
if "$tmp/scripts/dev-schema-test.sh" >"$tmp/output" 2>&1; then
    echo 'fingerprint comparison mutation survived' >&2
    exit 1
fi
grep -q '^unexpected success: .*dev-schema.sh check$' "$tmp/output"
printf 'watched-red fingerprint comparison: '
cat "$tmp/output"
