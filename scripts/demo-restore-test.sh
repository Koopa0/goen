#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
export RESTORE_TEST_TRACE="$tmp/trace" PATH="$tmp/bin:$PATH"
cat > "$tmp/bin/psql" <<'PSQL'
#!/usr/bin/env bash
set -euo pipefail
echo connect >> "$RESTORE_TEST_TRACE"
[[ ${CONNECT_FAIL:-0} == 0 ]] || exit 4
echo "${RESTORE_IDENTITY:-restore_operator}"
PSQL
cat > "$tmp/bin/pg_restore" <<'RESTORE'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == --list ]]; then
    echo inspect >> "$RESTORE_TEST_TRACE"
    exit "${INSPECT_FAIL:-0}"
fi
[[ " $* " == *' --single-transaction '* ]] || exit 8
[[ " $* " == *' -d postgres://restore_operator@localhost/goen '* ]] || exit 9
echo restore >> "$RESTORE_TEST_TRACE"
exit "${RESTORE_FAIL:-0}"
RESTORE
cat > "$tmp/bin/systemctl" <<'SYSTEMCTL'
#!/usr/bin/env bash
set -euo pipefail
echo "$1" >> "$RESTORE_TEST_TRACE"
if [[ "$1" == start ]]; then exit "${START_FAIL:-0}"; fi
SYSTEMCTL
chmod +x "$tmp/bin/psql" "$tmp/bin/pg_restore" "$tmp/bin/systemctl"
script="$root/deploy/demo/restore-demo-db.sh"
export GOEN_RESTORE_DATABASE_URL=postgres://restore_operator@localhost/goen
run() {
    local expected=$1 status=0
    shift
    : > "$RESTORE_TEST_TRACE"
    "$@" >"$tmp/output" 2>&1 || status=$?
    [[ "$status" == "$expected" ]] || { cat "$tmp/output" >&2; echo "exit $status, want $expected" >&2; exit 1; }
}
run 0 "$script"
printf 'connect\ninspect\nstop\nrestore\nstart\n' > "$tmp/expected"
cmp "$tmp/expected" "$RESTORE_TEST_TRACE"
run 2 env RESTORE_IDENTITY=store_svc "$script"
[[ $(cat "$RESTORE_TEST_TRACE") == connect ]]
run 4 env CONNECT_FAIL=1 "$script"
[[ $(cat "$RESTORE_TEST_TRACE") == connect ]]
run 5 env INSPECT_FAIL=5 "$script"
! grep -q '^stop$' "$RESTORE_TEST_TRACE"
run 6 env RESTORE_FAIL=6 "$script"
! grep -q '^start$' "$RESTORE_TEST_TRACE"
grep -q 'remains stopped pending operator recovery' "$tmp/output"
run 7 env START_FAIL=7 "$script"
grep -q 'restore completed but service start failed' "$tmp/output"
run 1 env -u GOEN_RESTORE_DATABASE_URL "$script"
[[ ! -s "$RESTORE_TEST_TRACE" ]]
echo 'demo restore orchestration: PASS (system and PostgreSQL commands simulated)'
