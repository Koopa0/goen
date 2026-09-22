#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LOAD_DIR="$ROOT/load"
ENV_FILE="${LOAD_ENV_FILE:-$LOAD_DIR/topology.env}"

if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  set -a && source "$ENV_FILE" && set +a
elif [[ -f "$LOAD_DIR/topology.env.example" ]]; then
  # shellcheck disable=SC1091
  set -a && source "$LOAD_DIR/topology.env.example" && set +a
fi

LOAD_DB_PORT="${LOAD_DB_PORT:-15433}"
LOAD_HTTP_PORT="${LOAD_HTTP_PORT:-19700}"
LOAD_SEED="${LOAD_SEED:-7292}"
IMAGE_TAG="${IMAGE_TAG:-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo local)}"
export LOAD_GOEN_IMAGE="${LOAD_GOEN_IMAGE:-host-binary:${IMAGE_TAG}}"

echo "load: starting dependency topology"
COMPOSE_ENV=()
if [[ -f "$ENV_FILE" ]]; then
  COMPOSE_ENV=(--env-file "$ENV_FILE")
fi
docker compose -f "$LOAD_DIR/compose.yml" "${COMPOSE_ENV[@]}" up -d db valkey mailpit

DB_URL="postgres://goen:goen@127.0.0.1:${LOAD_DB_PORT}/goen?sslmode=disable"
echo "load: waiting for database on ${LOAD_DB_PORT}"
for _ in $(seq 1 60); do
  if psql "$DB_URL" -c 'SELECT 1' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "load: resetting isolated database"
docker compose -f "$LOAD_DIR/compose.yml" "${COMPOSE_ENV[@]}" exec -T db \
  psql -U goen -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS goen WITH (FORCE)" \
  -c "CREATE DATABASE goen"

echo "load: migrating"
make -C "$ROOT" migrate-up GOEN_DATABASE_URL="$DB_URL"

echo "load: seeding pinned fixtures (${LOAD_FIXTURE_ID:-load-catalog-v1})"
psql "$DB_URL" -v ON_ERROR_STOP=1 -f "$LOAD_DIR/fixtures/catalog.sql"

bash "$LOAD_DIR/scripts/start-instances.sh"
docker compose -f "$LOAD_DIR/compose.yml" "${COMPOSE_ENV[@]}" up -d nginx

STAFF_TOKEN="$(openssl rand -hex 32)"
psql "$DB_URL" -qtAc "INSERT INTO users (email, role, full_name) VALUES ('load-staff@goen.invalid', 'admin', 'Load Staff') ON CONFLICT (lower(email)) DO NOTHING" >/dev/null
test "$(psql "$DB_URL" -qtAc "INSERT INTO sessions (token_hash, user_id, expires_at) SELECT sha256('${STAFF_TOKEN}'::bytea), id, now() + interval '6 hours' FROM users WHERE email = 'load-staff@goen.invalid' RETURNING 1")" = "1"

mkdir -p "$LOAD_DIR/evidence"
chmod 777 "$LOAD_DIR/evidence"
printf '%s\n' "$STAFF_TOKEN" > "$LOAD_DIR/evidence/staff-session.token"
chmod 600 "$LOAD_DIR/evidence/staff-session.token"

BASE_URL="http://127.0.0.1:${LOAD_HTTP_PORT}"
echo "load: waiting for storefront ${BASE_URL}/readyz"
for _ in $(seq 1 90); do
  if curl -sf "${BASE_URL}/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
curl -sf "${BASE_URL}/readyz" >/dev/null

cat >"$LOAD_DIR/evidence/topology.json" <<EOF
{
  "commit": "$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)",
  "image": "${LOAD_GOEN_IMAGE}",
  "fixture_id": "${LOAD_FIXTURE_ID:-load-catalog-v1}",
  "seed": ${LOAD_SEED},
  "instances": ${LOAD_INSTANCE_COUNT:-2},
  "store_max_conns_per_instance": ${LOAD_STORE_MAX_CONNS_PER_INSTANCE:-25},
  "admin_max_conns_per_instance": ${LOAD_ADMIN_MAX_CONNS_PER_INSTANCE:-10},
  "base_url": "${BASE_URL}",
  "database_url_host": "127.0.0.1:${LOAD_DB_PORT}",
  "valkey_port": ${LOAD_VALKEY_PORT:-16379},
  "providers_simulated": {
    "stripe": ${LOAD_STRIPE_SIMULATED:-0},
    "ecpay": ${LOAD_ECPAY_SIMULATED:-0}
  }
}
EOF

echo "load: provision complete — ${BASE_URL}"
