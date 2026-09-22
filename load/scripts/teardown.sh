#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LOAD_DIR="$ROOT/load"
ENV_FILE="${LOAD_ENV_FILE:-$LOAD_DIR/topology.env}"

bash "$LOAD_DIR/scripts/stop-instances.sh"

COMPOSE_ENV=()
if [[ -f "$ENV_FILE" ]]; then
  COMPOSE_ENV=(--env-file "$ENV_FILE")
fi
docker compose -f "$LOAD_DIR/compose.yml" "${COMPOSE_ENV[@]}" down -v
echo "load: topology removed"
