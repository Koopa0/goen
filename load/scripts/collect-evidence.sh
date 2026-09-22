#!/usr/bin/env bash
set -euo pipefail

PROFILE="${1:-unknown}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
LOAD_DIR="$ROOT/load"
ENV_FILE="${LOAD_ENV_FILE:-$LOAD_DIR/topology.env}"

if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  set -a && source "$ENV_FILE" && set +a
fi

LOAD_DB_PORT="${LOAD_DB_PORT:-15433}"
LOAD_SEED="${LOAD_SEED:-7292}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="$LOAD_DIR/evidence/${PROFILE}-${STAMP}"
mkdir -p "$OUT"

DB_URL="postgres://goen:goen@127.0.0.1:${LOAD_DB_PORT}/goen?sslmode=disable"

psql "$DB_URL" -Atc "
  SELECT json_build_object(
    'active_connections', (SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()),
    'checkout_attempts', (SELECT count(*) FROM checkout_attempts),
    'orders', (SELECT count(*) FROM orders),
    'flash_variant_stock', (
      SELECT stock_quantity FROM product_variants WHERE sku = 'LOAD-FLASH-001'
    )
  )" >"$OUT/db-snapshot.json"

if command -v docker >/dev/null 2>&1; then
  docker stats --no-stream --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}' \
    $(docker compose -f "$LOAD_DIR/compose.yml" ps -q 2>/dev/null || true) \
    >"$OUT/container-stats.txt" 2>/dev/null || true
fi

if [[ -f "$LOAD_DIR/evidence/topology.json" ]]; then
  cp "$LOAD_DIR/evidence/topology.json" "$OUT/topology.json"
fi
SUMMARY_ID="${LOAD_SEED}"
if [[ "$PROFILE" == "stock-contention" ]]; then SUMMARY_ID="${LOAD_RUN_ID:?}"; fi
if [[ -f "$LOAD_DIR/evidence/${PROFILE}-${SUMMARY_ID}.json" ]]; then
  cp "$LOAD_DIR/evidence/${PROFILE}-${SUMMARY_ID}.json" "$OUT/k6-summary.json"
fi

cat >"$OUT/summary.txt" <<EOF
profile=${PROFILE}
seed=${LOAD_SEED}
collected_at=${STAMP}
commit=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)
EOF

ln -sfn "$OUT" "$LOAD_DIR/evidence/latest-${PROFILE}"
echo "load: evidence written to $OUT"
