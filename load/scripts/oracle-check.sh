#!/usr/bin/env bash
set -euo pipefail

PROFILE="${1:-}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LOAD_DIR="$ROOT/load"
ENV_FILE="${LOAD_ENV_FILE:-$LOAD_DIR/topology.env}"

if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  set -a && source "$ENV_FILE" && set +a
fi

LOAD_DB_PORT="${LOAD_DB_PORT:-15433}"
LOAD_SEED="${LOAD_SEED:-7292}"
DB_URL="postgres://goen:goen@127.0.0.1:${LOAD_DB_PORT}/goen?sslmode=disable"
FLASH_VARIANT="a3330006-0000-4000-8000-000000000006"

ORACLE_ENV=()
if [[ "$PROFILE" == "dependency-failure" ]]; then
  K6_SUMMARY="$LOAD_DIR/evidence/${PROFILE}-${LOAD_SEED}.json"
  if [[ ! -f "$K6_SUMMARY" ]]; then
    echo "load: missing k6 summary for degraded-work oracle: $K6_SUMMARY" >&2
    exit 1
  fi
  if ! command -v jq >/dev/null 2>&1; then
    echo "load: jq is required to derive degraded-work counts from $K6_SUMMARY" >&2
    exit 1
  fi
  PASSES="$(jq -r '.metrics.checks.values.passes // empty' "$K6_SUMMARY")"
  FAILS="$(jq -r '.metrics.checks.values.fails // empty' "$K6_SUMMARY")"
  if [[ -z "$PASSES" || -z "$FAILS" ]]; then
    echo "load: k6 summary is missing check pass/fail counts: $K6_SUMMARY" >&2
    exit 1
  fi
  if ! [[ "$PASSES" =~ ^[0-9]+$ && "$FAILS" =~ ^[0-9]+$ ]]; then
    echo "load: k6 check counts must be integers (passes=${PASSES} fails=${FAILS})" >&2
    exit 1
  fi
  TOTAL=$((PASSES + FAILS))
  if [[ "$TOTAL" -le 0 ]]; then
    echo "load: degraded-work oracle requires recorded k6 checks, got total=0" >&2
    exit 1
  fi
  ORACLE_ENV=(
    "LOAD_ORACLE_SUCCESS_COUNT=${PASSES}"
    "LOAD_ORACLE_TOTAL_COUNT=${TOTAL}"
  )
fi

echo "load: running oracles for profile ${PROFILE}"
env "${ORACLE_ENV[@]}" GOEN_LOAD_DATABASE_URL="$DB_URL" go run "$ROOT/load/cmd/load-oracle" \
  --profile "$PROFILE" \
  --variant "$FLASH_VARIANT"
