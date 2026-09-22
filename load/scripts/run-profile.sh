#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <profile> [extra k6 args...]" >&2
  exit 2
fi

PROFILE="$1"
shift

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LOAD_DIR="$ROOT/load"
ENV_FILE="${LOAD_ENV_FILE:-$LOAD_DIR/topology.env}"

CLI_K6_DURATION="${LOAD_K6_DURATION:-}"
CLI_K6_VUS_MAX="${LOAD_K6_VUS_MAX:-}"
CLI_K6_ARRIVAL_RATE="${LOAD_K6_ARRIVAL_RATE:-}"
if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  set -a && source "$ENV_FILE" && set +a
elif [[ -f "$LOAD_DIR/topology.env.example" ]]; then
  # shellcheck disable=SC1091
  set -a && source "$LOAD_DIR/topology.env.example" && set +a
fi
LOAD_K6_DURATION="${CLI_K6_DURATION:-${LOAD_K6_DURATION:-2m}}"
LOAD_K6_VUS_MAX="${CLI_K6_VUS_MAX:-${LOAD_K6_VUS_MAX:-40}}"
LOAD_K6_ARRIVAL_RATE="${CLI_K6_ARRIVAL_RATE:-${LOAD_K6_ARRIVAL_RATE:-20}}"

LOAD_HTTP_PORT="${LOAD_HTTP_PORT:-19700}"
LOAD_SEED="${LOAD_SEED:-7292}"
export LOAD_RUN_ID="${LOAD_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$-${RANDOM}}"
export LOAD_STOCK_EVIDENCE="$LOAD_DIR/evidence/stock-contention-${LOAD_RUN_ID}.jsonl"
K6_IMAGE="${K6_IMAGE:-grafana/k6:0.54.0}"
SCRIPT="$LOAD_DIR/k6/profiles/${PROFILE}.js"

if [[ ! -f "$SCRIPT" ]]; then
  echo "unknown profile: $PROFILE" >&2
  exit 2
fi

mkdir -p "$LOAD_DIR/evidence"
STAFF_TOKEN=""
if [[ -f "$LOAD_DIR/evidence/staff-session.token" ]]; then
  STAFF_TOKEN="$(cat "$LOAD_DIR/evidence/staff-session.token")"
fi

COMPOSE_ENV=()
if [[ -f "$ENV_FILE" ]]; then
  COMPOSE_ENV=(--env-file "$ENV_FILE")
fi

if [[ "$PROFILE" == "dependency-failure" ]]; then
  IMPAIR="${LOAD_IMPAIRMENT:-valkey-down}"
  echo "load: impairing dependency (${IMPAIR})"
  case "$IMPAIR" in
    valkey-down)
      docker compose -f "$LOAD_DIR/compose.yml" "${COMPOSE_ENV[@]}" stop valkey
      ;;
    app-restart)
      docker compose -f "$LOAD_DIR/compose.yml" "${COMPOSE_ENV[@]}" restart goen-a goen-b
      ;;
    *)
      echo "unsupported impairment: $IMPAIR" >&2
      exit 2
      ;;
  esac
fi

echo "load: running profile ${PROFILE} (seed=${LOAD_SEED})"
mkdir -p "$LOAD_DIR/evidence"
chmod 777 "$LOAD_DIR/evidence"
K6_STATUS=0
docker run --rm -i \
  --network host \
  --user "$(id -u):$(id -g)" \
  -v "$LOAD_DIR:/load" \
  -w /load \
  -e LOAD_BASE_URL="http://127.0.0.1:${LOAD_HTTP_PORT}" \
  -e LOAD_SEED="$LOAD_SEED" \
  -e LOAD_RUN_ID="$LOAD_RUN_ID" \
  -e LOAD_FIXTURE_ID="${LOAD_FIXTURE_ID:-load-catalog-v1}" \
  -e LOAD_K6_VUS_MAX="${LOAD_K6_VUS_MAX:-40}" \
  -e LOAD_K6_DURATION="${LOAD_K6_DURATION:-2m}" \
  -e LOAD_K6_ARRIVAL_RATE="${LOAD_K6_ARRIVAL_RATE:-20}" \
  -e LOAD_STAFF_TOKEN="$STAFF_TOKEN" \
  -e LOAD_IMPAIRMENT="${LOAD_IMPAIRMENT:-valkey-down}" \
  -e LOAD_CACHE_MODE="${LOAD_CACHE_MODE:-warm}" \
  "$K6_IMAGE" run --log-format raw --console-output "/load/evidence/${PROFILE}-${LOAD_RUN_ID}.jsonl" "/load/k6/profiles/${PROFILE}.js" "$@" || K6_STATUS=$?

if [[ "$PROFILE" == "dependency-failure" && "${LOAD_IMPAIRMENT:-valkey-down}" == "valkey-down" ]]; then
  docker compose -f "$LOAD_DIR/compose.yml" "${COMPOSE_ENV[@]}" start valkey
fi

"$LOAD_DIR/scripts/collect-evidence.sh" "$PROFILE"
ORACLE_STATUS=0
"$LOAD_DIR/scripts/oracle-check.sh" "$PROFILE" || ORACLE_STATUS=$?
if [[ "$K6_STATUS" -ne 0 ]]; then exit "$K6_STATUS"; fi
exit "$ORACLE_STATUS"
