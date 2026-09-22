#!/usr/bin/env bash
# Run only in the prepared CI layout job, against its disposable database.
set -euo pipefail
cd "$(dirname "$0")/.."
feedback_proof_dir="${RUNNER_TEMP:-/tmp}/goen-request-feedback-mutations"
mkdir -p "$feedback_proof_dir"
feedback_scratch=$(mktemp -d)
feedback_paths=(assets/js/goen.js assets/css/app/app.css)
for i in "${!feedback_paths[@]}"; do cp "${feedback_paths[$i]}" "$feedback_scratch/source-$i"; done
feedback_server_pid=''
feedback_chrome_pid=''
restore_feedback_sources() {
  for i in "${!feedback_paths[@]}"; do
    cp "$feedback_scratch/source-$i" "${feedback_paths[$i]}"
    cmp "$feedback_scratch/source-$i" "${feedback_paths[$i]}"
  done
}
stop_feedback_processes() {
  if [[ -n "$feedback_chrome_pid" ]]; then kill "$feedback_chrome_pid" 2>/dev/null || true; wait "$feedback_chrome_pid" 2>/dev/null || true; feedback_chrome_pid=''; fi
  if [[ -n "$feedback_server_pid" ]]; then kill "$feedback_server_pid" 2>/dev/null || true; wait "$feedback_server_pid" 2>/dev/null || true; feedback_server_pid=''; fi
}
cleanup_feedback() { stop_feedback_processes; restore_feedback_sources; rm -rf "$feedback_scratch"; }
trap cleanup_feedback EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
git rev-parse HEAD > "$feedback_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$feedback_proof_dir/pr-head.txt"
feedback_chrome=$(scripts/resolve-chrome.sh)
feedback_variant=$(psql "$GOEN_DATABASE_URL" -tAc "SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock LIMIT 1")
test -n "$feedback_variant"
run_feedback_phase() {
  local phase=$1
  stop_feedback_processes
  go build -o "$feedback_scratch/goen" ./cmd/goen > "$feedback_proof_dir/$phase-build.log" 2>&1 || return
  GOEN_ADDR=127.0.0.1:9701 GOEN_BASE_URL=http://127.0.0.1:9701 GOEN_INSECURE_COOKIES=1 "$feedback_scratch/goen" > "$feedback_proof_dir/$phase-server.log" 2>&1 & feedback_server_pid=$!
  for _ in $(seq 1 60); do
    if curl -fsS -o /dev/null http://127.0.0.1:9701/readyz; then break; fi
    kill -0 "$feedback_server_pid" || return
    sleep 1
  done
  curl -fsS -o /dev/null http://127.0.0.1:9701/readyz || return
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' -c "$feedback_scratch/$phase-cookies" -d "variant=$feedback_variant&quantity=1" http://127.0.0.1:9701/cart/items) || return
  test "$code" = 303 || return
  local token
  token=$(awk '$6 == "goen_cart" {print $7}' "$feedback_scratch/$phase-cookies")
  test -n "$token" || return
  "$feedback_chrome" --headless --disable-gpu --no-first-run --remote-debugging-port=9223 --user-data-dir="$feedback_scratch/chrome-$phase" about:blank > "$feedback_proof_dir/$phase-chrome.log" 2>&1 & feedback_chrome_pid=$!
  for _ in $(seq 1 30); do
    if curl -fsS -o /dev/null http://127.0.0.1:9223/json/version; then break; fi
    kill -0 "$feedback_chrome_pid" || return
    sleep 1
  done
  curl -fsS -o /dev/null http://127.0.0.1:9223/json/version || return
  GOEN_URL=http://127.0.0.1:9701 CDP_PORT=9223 CART_TOKEN="$token" node scripts/check-request-feedback-browser.mjs > "$feedback_proof_dir/$phase.jsonl" 2>&1
}
run_feedback_phase baseline
for feedback_mutant in pending cleanup reduced; do
  restore_feedback_sources
  python3 - "$feedback_mutant" <<'PY'
from pathlib import Path
import sys
changes = {
 'pending': ('assets/js/goen.js', '  requestFeedback();', '  void requestFeedback;'),
 'cleanup': ('assets/js/goen.js', '        finish(form);\n        source.removeEventListener', '        void form;\n        source.removeEventListener'),
 'reduced': ('assets/css/app/app.css', '.goen-notice,\n  .goen-header__menu::details-content {\n    transition: none;', '.goen-notice,\n  .goen-header__menu::details-content {\n    transition-duration: var(--dur-base);'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
s = p.read_text()
if s.count(before) != 1: raise SystemExit('feedback mutation target is no longer unique')
p.write_text(s.replace(before, after, 1))
PY
  git diff -- "${feedback_paths[@]}" > "$feedback_proof_dir/$feedback_mutant.patch"
  feedback_exit=0
  run_feedback_phase "$feedback_mutant" || feedback_exit=$?
  test "$feedback_exit" != 0
  python3 - "$feedback_proof_dir/$feedback_mutant.jsonl" "$feedback_mutant" <<'PY'
import json, sys
from pathlib import Path
expected = {'pending': 'pending_375', 'cleanup': 'cleanup_375', 'reduced': 'menu_reduce_375'}[sys.argv[2]]
records = []
for line in Path(sys.argv[1]).read_text().splitlines():
    try: records.append(json.loads(line))
    except json.JSONDecodeError: pass
failed = [r for r in records if isinstance(r, dict) and r.get('name') == expected and r.get('status') == 'fail']
completed = any(isinstance(r, dict) and r.get('name') == 'probe_completed' and r.get('status') == 'pass' for r in records)
if not failed or not completed: raise SystemExit('no completed matching browser assertion; build/startup/probe errors are not mutation evidence')
print('Observed browser red: ' + json.dumps(failed[0]))
PY
done
restore_feedback_sources
run_feedback_phase restored
python3 - "$feedback_proof_dir" <<'PY'
import json, sys
from pathlib import Path
expected = ['served_js', 'served_css', 'probe_completed']
for width in (375, 1440):
    expected += [f'pending_{width}', f'cleanup_{width}', f'noscript_{width}']
    for motion in ('normal', 'reduce'):
        expected += [f'transition_{motion}_{width}']
        if width == 375:
            expected += [f'menu_{motion}_{width}', f'focus_{motion}_{width}']
        else:
            expected += [f'desktop_menu_hidden_{motion}']
for phase in ('baseline', 'restored'):
    records = []
    for line in (Path(sys.argv[1]) / f'{phase}.jsonl').read_text().splitlines():
        try: records.append(json.loads(line))
        except json.JSONDecodeError: pass
    for name in expected:
        if not any(isinstance(r, dict) and r.get('name') == name and r.get('status') == 'pass' for r in records):
            raise SystemExit(phase + ' did not pass ' + name)
PY
