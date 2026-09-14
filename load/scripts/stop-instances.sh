#!/usr/bin/env bash
set -euo pipefail

LOAD_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RUN_DIR="$LOAD_DIR/.run"

for name in goen-a goen-b; do
  if [[ -f "$RUN_DIR/$name.pid" ]]; then
    pid="$(cat "$RUN_DIR/$name.pid")"
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
    rm -f "$RUN_DIR/$name.pid"
  fi
done
