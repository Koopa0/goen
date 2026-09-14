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
DB_URL="postgres://goen:goen@127.0.0.1:${LOAD_DB_PORT}/goen?sslmode=disable"
FLASH_VARIANT="a3330006-0000-4000-8000-000000000006"

echo "load: running oracles for profile ${PROFILE}"
GOEN_LOAD_DATABASE_URL="$DB_URL" go run "$ROOT/load/cmd/load-oracle" \
  --profile "$PROFILE" \
  --variant "$FLASH_VARIANT"
