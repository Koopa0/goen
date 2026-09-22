#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
SCRIPT=load/k6/profiles/stock-contention.js
BACKUP="$(mktemp)"
cp "$SCRIPT" "$BACKUP"
trap 'cp "$BACKUP" "$SCRIPT"; rm -f "$BACKUP"' EXIT
# This mutation removes actual replay requests while preserving fresh placement.
python3 - <<'PY'
from pathlib import Path
path = Path('load/k6/profiles/stock-contention.js')
source = path.read_text()
needle = 'export function repeatSubmit(data) {'
if source.count(needle) != 1:
    raise SystemExit('replay mutation did not reach the scenario function')
source = source.replace(needle, 'export function repeatSubmit() {}\n\nfunction removedReplay(data) {')
path.write_text(source)
PY
export LOAD_RUN_ID="ci-no-replay-${GITHUB_RUN_ID:?}-${GITHUB_RUN_ATTEMPT:?}"
export LOAD_K6_VUS_MAX=4
status=0
bash load/scripts/run-profile.sh stock-contention >load/evidence/replay-negative.log 2>&1 || status=$?
if [[ "$status" -eq 0 ]]; then
  echo 'negative control falsely passed with replay removed' >&2
  exit 1
fi
# A broken setup is not evidence that the replay gate catches this mutation.
jq -es 'any(.[]; .kind == "anchor") and any(.[]; .kind == "placement") and ([.[] | select(.kind == "replay")] | length == 0)' \
  "load/evidence/stock-contention-${LOAD_RUN_ID}.jsonl" >/dev/null
grep -F 'replay evidence count 0, want 6' load/evidence/replay-negative.log
printf 'removed replay: runner exit %s; fresh anchor and competing order exist; oracle rejected zero replays\n' "$status"
