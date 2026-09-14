#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LOAD_DIR="$ROOT/load"
RUN_DIR="$LOAD_DIR/.run"
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
BIN="${LOAD_GOEN_BIN:-$ROOT/bin/goen-load}"
IMAGE_TAG="${IMAGE_TAG:-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo local)}"

mkdir -p "$RUN_DIR"

stop_one() {
  local name="$1"
  if [[ -f "$RUN_DIR/$name.pid" ]]; then
    local pid
    pid="$(cat "$RUN_DIR/$name.pid")"
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
    rm -f "$RUN_DIR/$name.pid"
  fi
}

stop_one goen-a
stop_one goen-b

make -C "$ROOT" build
cp "$ROOT/bin/goen" "$BIN"

shared_env=(
  "GOEN_DATABASE_URL=postgres://goen:goen@127.0.0.1:${LOAD_DB_PORT}/goen?sslmode=disable"
  "GOEN_ADMIN_DATABASE_URL=postgres://goen:goen@127.0.0.1:${LOAD_DB_PORT}/goen?sslmode=disable"
  "GOEN_MAINTENANCE_DATABASE_URL=postgres://goen:goen@127.0.0.1:${LOAD_DB_PORT}/goen?sslmode=disable"
  "GOEN_INSECURE_COOKIES=1"
  "GOEN_LOG_LEVEL=info"
  "GOEN_BASE_URL=http://127.0.0.1:${LOAD_HTTP_PORT}"
  "GOEN_SMTP_ADDR=127.0.0.1:1025"
  "GOEN_SMTP_FROM=goen load <load@goen.invalid>"
  "GOEN_VALKEY_URL=redis://127.0.0.1:${LOAD_VALKEY_PORT:-16379}/0"
)

start_one() {
  local name="$1"
  local addr="$2"
  local log="$RUN_DIR/$name.log"
  env "${shared_env[@]}" GOEN_INSTANCE="$name" GOEN_ADDR="$addr" \
    "$BIN" >"$log" 2>&1 &
  echo $! >"$RUN_DIR/$name.pid"
}

start_one goen-a "0.0.0.0:19701"
start_one goen-b "0.0.0.0:19702"

echo "load: started host instances on 19701 and 19702 (commit ${IMAGE_TAG})"
