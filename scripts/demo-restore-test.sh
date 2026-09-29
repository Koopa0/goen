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
[[ " $* " == *' --role=goen '* ]] || exit 10
[[ " $* " != *' --no-owner '* ]] || exit 11
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
printf 'connect\ninspect\nstop\nrestore\nstart\n' > "$tmp/expected"
cmp "$tmp/expected" "$RESTORE_TEST_TRACE"
grep -q 'restarting goen.service on the previous data' "$tmp/output"
run 6 env RESTORE_FAIL=6 START_FAIL=7 "$script"
grep -q 'the restart also failed' "$tmp/output"
run 7 env START_FAIL=7 "$script"
grep -q 'restore completed but service start failed' "$tmp/output"
run 1 env -u GOEN_RESTORE_DATABASE_URL "$script"
[[ ! -s "$RESTORE_TEST_TRACE" ]]
echo 'demo restore orchestration: PASS (system and PostgreSQL commands simulated)'

# The same script against a real PostgreSQL: object owners must survive a
# restore, and a corrupt snapshot must leave the old data and restart the service.
if ! docker info >/dev/null 2>&1; then
    [[ "${CI:-}" != true ]] || { echo 'docker is required in CI' >&2; exit 1; }
    echo 'demo restore ownership: SKIP (no docker)'
    exit 0
fi
image=$(awk '/image:/ {print $2; exit}' "$root/docker-compose.yml")
c=goen-restore-test-$$
trap 'docker rm -f -v "$c" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT
# The compose image's POSTGRES_USER is the schema owner, as after make migrate-up.
docker run -d --name "$c" --network none -e POSTGRES_USER=goen -e POSTGRES_DB=goen \
    -e POSTGRES_HOST_AUTH_METHOD=trust "$image" >/dev/null
for _ in $(seq 60); do
    docker exec "$c" pg_isready -h 127.0.0.1 -U goen -d goen >/dev/null 2>&1 && break
    sleep 1
done
sql() { docker exec -i "$c" psql -X -qAt -v ON_ERROR_STOP=1 -U goen -d goen "$@"; }
sql -c "CREATE ROLE restore_operator LOGIN NOSUPERUSER IN ROLE goen"
sql -1 < "$root/migrations/001_initial_schema.up.sql" >/dev/null
sql -c "INSERT INTO brands (slug, name) VALUES ('restore-fixture', 'snapshot')"
owners() {
    sql -c "SELECT 'rel', n.nspname, c.relname, pg_get_userbyid(c.relowner)
              FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
               AND c.relkind IN ('r', 'p', 'S', 'v', 'm', 'f')
            UNION ALL
            SELECT 'fn' || CASE WHEN p.prosecdef THEN '-definer' ELSE '' END, n.nspname,
                   p.oid::regprocedure::text, pg_get_userbyid(p.proowner)
              FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
             WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
             ORDER BY 1, 2, 3"
}
owners > "$tmp/owners.before"
tables=$(grep -c '^rel|' "$tmp/owners.before") definers=$(grep -c '^fn-definer|' "$tmp/owners.before")
(( tables > 60 && definers > 10 )) || { echo "owner listing too small: $tables tables, $definers definers" >&2; exit 1; }
[[ -z $(grep -v '|goen$' "$tmp/owners.before") ]] || { echo 'migration objects not all owned by goen' >&2; exit 1; }
docker exec "$c" pg_dump -U goen -d goen --format=custom --file=/tmp/good.dump
# pg_dump's custom format puts the table of contents first and the data blocks
# after it, so the END of the file is always data. Zeroing there leaves the
# archive listing fine (the preflight passes and the service stops) and makes a
# data block fail at restore time. The middle of the file is not safe: it moves
# with the schema and lands in the table of contents as tables are added.
docker exec "$c" sh -c 'cp /tmp/good.dump /tmp/corrupt.dump
    dd if=/dev/zero of=/tmp/corrupt.dump bs=1 count=64 seek=$(( $(wc -c < /tmp/good.dump) - 64 )) conv=notrunc 2>/dev/null'
# The drill proves nothing unless the corruption sits past the table of contents
# and inside data; check both, so it goes red when it stops exercising that path.
docker exec "$c" pg_restore --list /tmp/corrupt.dump >/dev/null \
    || { echo 'corrupt snapshot no longer lists: the corruption reached the table of contents, so the preflight would refuse it before the service stops' >&2; exit 1; }
! docker exec "$c" cmp -s /tmp/good.dump /tmp/corrupt.dump \
    || { echo 'corrupt snapshot is identical to the good one' >&2; exit 1; }
if docker exec "$c" sh -c 'pg_restore -f /dev/null /tmp/corrupt.dump' >/dev/null 2>&1; then
    echo 'corrupt snapshot reads back cleanly: the corruption is not in a data block, so a restore would not fail' >&2
    exit 1
fi
docker cp "$script" "$c:/tmp/restore-demo-db.sh"
docker exec -i "$c" sh -c 'mkdir -p /tmp/bin; cat > /tmp/bin/systemctl; chmod +x /tmp/bin/systemctl' <<'STUB'
#!/bin/sh
echo "$1" >> /tmp/service-events
STUB
real_restore() {
    docker exec "$c" sh -c ': > /tmp/service-events'
    docker exec -e PATH=/tmp/bin:/usr/local/bin:/usr/bin:/bin \
        -e GOEN_RESTORE_DATABASE_URL='postgresql:///goen?user=restore_operator' \
        -e "GOEN_RESTORE_SNAPSHOT=/tmp/$1" "$c" bash /tmp/restore-demo-db.sh >"$tmp/output" 2>&1
}
brand() { sql -c "SELECT name FROM brands WHERE slug = 'restore-fixture'"; }
same_owners() {
    owners > "$tmp/owners.after"
    cmp -s "$tmp/owners.before" "$tmp/owners.after" || { diff "$tmp/owners.before" "$tmp/owners.after" | head >&2; echo "$1: object owners changed" >&2; exit 1; }
}

sql -c "UPDATE brands SET name = 'live' WHERE slug = 'restore-fixture'"
real_restore good.dump || { cat "$tmp/output" >&2; echo 'restore of a good snapshot failed' >&2; exit 1; }
[[ $(brand) == snapshot ]] || { echo 'good restore did not replace the data' >&2; exit 1; }
same_owners 'after restore'

sql -c "UPDATE brands SET name = 'live' WHERE slug = 'restore-fixture'"
if real_restore corrupt.dump; then echo 'corrupt snapshot restored without error' >&2; exit 1; fi
grep -q 'restarting goen.service on the previous data' "$tmp/output" || { cat "$tmp/output" >&2; echo 'corrupt snapshot did not fail after the service stopped' >&2; exit 1; }
[[ $(docker exec "$c" cat /tmp/service-events) == $'stop\nstart' ]] || { echo 'failed restore did not restart the service' >&2; exit 1; }
[[ $(brand) == live ]] || { echo 'failed restore changed the previous data' >&2; exit 1; }
same_owners 'after failed restore'
echo "demo restore ownership: PASS ($tables tables, $definers SECURITY DEFINER functions keep owner goen; failed restore restarted on old data)"
