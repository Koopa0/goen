#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/scripts" "$tmp/migrations" "$tmp/bin" "$tmp/state"
cp "$root/scripts/dev-schema.sh" "$tmp/scripts/"
printf 'CREATE TABLE example (id integer);\n' > "$tmp/migrations/001.up.sql"
export SCHEMA_TEST_STATE="$tmp/state" GOEN_DATABASE_URL=fixture
export PATH="$tmp/bin:$PATH"
cat > "$tmp/bin/psql" <<'PSQL'
#!/usr/bin/env bash
set -euo pipefail
while [[ $# -gt 0 ]]; do
    case "$1" in
        -c) sql=$2; shift 2 ;;
        -v) [[ "$2" != digest=* ]] || digest=${2#digest=}; shift 2 ;;
        *) shift ;;
    esac
done
if [[ -n ${digest:-} ]]; then
    cat >/dev/null
    printf '%s\n' "$digest" > "$SCHEMA_TEST_STATE/digest"
    exit 0
fi
case "$sql" in
    *"to_regclass('goen_dev.schema_fingerprint')"*) [[ ! -f "$SCHEMA_TEST_STATE/digest" ]] && echo f || echo t ;;
    *"to_regclass('public.schema_migrations')"*) [[ ! -f "$SCHEMA_TEST_STATE/applied" ]] && echo f || echo t ;;
    'SELECT count(*) FROM public.schema_migrations') echo 1 ;;
    "SELECT version::text || ':' || dirty::text FROM public.schema_migrations") cat "$SCHEMA_TEST_STATE/applied" ;;
    'SELECT digest FROM goen_dev.schema_fingerprint WHERE singleton') cat "$SCHEMA_TEST_STATE/digest" ;;
    *) echo "unexpected SQL: $sql" >&2; exit 2 ;;
esac
PSQL
cat > "$tmp/bin/migrate-fixture" <<'MIGRATE'
#!/usr/bin/env bash
set -euo pipefail
touch "$SCHEMA_TEST_STATE/invoked"
[[ ! -e "$SCHEMA_TEST_STATE/fail" ]]
printf '1:false\n' > "$SCHEMA_TEST_STATE/applied"
MIGRATE
chmod +x "$tmp/bin/psql" "$tmp/bin/migrate-fixture"
check="$tmp/scripts/dev-schema.sh"
reject() {
    if "$@" >"$tmp/output" 2>&1; then
        echo "unexpected success: $*" >&2
        exit 1
    fi
}
reject "$check" check
grep -q 'make db-reset' "$tmp/output"
touch "$SCHEMA_TEST_STATE/fail"
reject "$check" apply migrate-fixture
[[ ! -e "$SCHEMA_TEST_STATE/digest" ]]
rm "$SCHEMA_TEST_STATE/fail"
"$check" apply migrate-fixture
"$check" check
rm "$SCHEMA_TEST_STATE/invoked"
"$check" apply migrate-fixture
[[ -f "$SCHEMA_TEST_STATE/invoked" ]]
rm "$SCHEMA_TEST_STATE/invoked"
for applied in '' '1:true' '2:false'; do
    printf '%s\n' "$applied" > "$SCHEMA_TEST_STATE/applied"
    reject "$check" check
done
printf '1:true\n' > "$SCHEMA_TEST_STATE/applied"
reject "$check" apply migrate-fixture
[[ ! -e "$SCHEMA_TEST_STATE/invoked" ]]
printf '1:false\n' > "$SCHEMA_TEST_STATE/applied"
printf '\nALTER TABLE example ADD COLUMN label text;\n' >> "$tmp/migrations/001.up.sql"
reject "$check" check
reject "$check" apply migrate-fixture
[[ ! -e "$SCHEMA_TEST_STATE/invoked" ]]
rm "$SCHEMA_TEST_STATE/digest"
reject "$check" apply migrate-fixture
[[ ! -e "$SCHEMA_TEST_STATE/invoked" ]]
echo 'dev schema orchestration: PASS (SQL adapter simulated)'
