#!/usr/bin/env bash
# Run only in the prepared CI layout job, against its disposable database.
set -euo pipefail
cd "$(dirname "$0")/.."
constraint_proof_dir="${RUNNER_TEMP:-/tmp}/goen-checkout-constraints-mutations"
mkdir -p "$constraint_proof_dir"
constraint_scratch=$(mktemp -d)
constraint_paths=(assets/js/goen.js assets/css/app/app.css internal/ui/pages/cart.templ internal/ui/pages/cart_templ.go)
for i in "${!constraint_paths[@]}"; do cp "${constraint_paths[$i]}" "$constraint_scratch/source-$i"; done
constraint_server_pid=''
constraint_chrome_pid=''
restore_constraint_sources() {
  for i in "${!constraint_paths[@]}"; do
    cp "$constraint_scratch/source-$i" "${constraint_paths[$i]}"
    cmp "$constraint_scratch/source-$i" "${constraint_paths[$i]}"
  done
}
stop_constraint_processes() {
  if [[ -n "$constraint_chrome_pid" ]]; then kill "$constraint_chrome_pid" 2>/dev/null || true; wait "$constraint_chrome_pid" 2>/dev/null || true; constraint_chrome_pid=''; fi
  if [[ -n "$constraint_server_pid" ]]; then kill "$constraint_server_pid" 2>/dev/null || true; wait "$constraint_server_pid" 2>/dev/null || true; constraint_server_pid=''; fi
}
cleanup_constraint() { stop_constraint_processes; restore_constraint_sources; rm -rf "$constraint_scratch"; }
trap cleanup_constraint EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
git rev-parse HEAD > "$constraint_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$constraint_proof_dir/pr-head.txt"
constraint_chrome=$(scripts/resolve-chrome.sh)
constraint_variant=$(psql "$GOEN_DATABASE_URL" -tAc "SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock LIMIT 1")
test -n "$constraint_variant"
run_constraint_phase() {
  local phase=$1
  stop_constraint_processes
  go build -o "$constraint_scratch/goen" ./cmd/goen > "$constraint_proof_dir/$phase-build.log" 2>&1 || return
  GOEN_ADDR=127.0.0.1:9701 GOEN_BASE_URL=http://127.0.0.1:9701 GOEN_INSECURE_COOKIES=1 "$constraint_scratch/goen" > "$constraint_proof_dir/$phase-server.log" 2>&1 & constraint_server_pid=$!
  for _ in $(seq 1 60); do
    if curl -fsS -o /dev/null http://127.0.0.1:9701/readyz; then break; fi
    kill -0 "$constraint_server_pid" || return
    sleep 1
  done
  curl -fsS -o /dev/null http://127.0.0.1:9701/readyz || return
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' -c "$constraint_scratch/$phase-cookies" -d "variant=$constraint_variant&quantity=1" http://127.0.0.1:9701/cart/items) || return
  test "$code" = 303 || return
  local token
  token=$(awk '$6 == "goen_cart" {print $7}' "$constraint_scratch/$phase-cookies")
  test -n "$token" || return
  "$constraint_chrome" --headless --disable-gpu --no-first-run --remote-debugging-port=9223 --user-data-dir="$constraint_scratch/chrome-$phase" about:blank > "$constraint_proof_dir/$phase-chrome.log" 2>&1 & constraint_chrome_pid=$!
  for _ in $(seq 1 30); do
    if curl -fsS -o /dev/null http://127.0.0.1:9223/json/version; then break; fi
    kill -0 "$constraint_chrome_pid" || return
    sleep 1
  done
  curl -fsS -o /dev/null http://127.0.0.1:9223/json/version || return
  GOEN_URL=http://127.0.0.1:9701 CDP_PORT=9223 CART_TOKEN="$token" node scripts/check-checkout-constraints-browser.mjs > "$constraint_proof_dir/$phase.jsonl" 2>&1
}
run_constraint_phase baseline
for constraint_mutant in pattern aria visible; do
  restore_constraint_sources
  python3 - "$constraint_mutant" <<'MUTATION'
from pathlib import Path
import sys
changes = {
 'pattern': ('internal/ui/pages/cart.templ', 'pattern={ hints.Pattern }', 'pattern=".*"'),
 'aria': ('assets/js/goen.js', '  checkoutConstraints();', '  void checkoutConstraints;'),
 'visible': ('assets/css/app/app.css', '[data-checkout-constraint][aria-invalid="true"] ~ .goen-field__constraint {\n  display: block;', '[data-checkout-constraint][aria-invalid="true"] ~ .goen-field__constraint {\n  display: none;'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
s = p.read_text()
if s.count(before) != 1: raise SystemExit('constraint mutation target is no longer unique')
p.write_text(s.replace(before, after, 1))
MUTATION
  if [[ "$constraint_mutant" == pattern ]]; then
    make gen > "$constraint_proof_dir/pattern-generation.log" 2>&1
    grep -F 'pattern=\".*\"' internal/ui/pages/cart_templ.go > "$constraint_proof_dir/pattern-generated-target.txt"
  fi
  git diff -- "${constraint_paths[@]}" > "$constraint_proof_dir/$constraint_mutant.patch"
  constraint_exit=0
  run_constraint_phase "$constraint_mutant" || constraint_exit=$?
  test "$constraint_exit" != 0
  python3 - "$constraint_proof_dir/$constraint_mutant.jsonl" "$constraint_mutant" <<'ORACLE'
import json, sys
from pathlib import Path
expected = {'pattern': 'pattern_script_375', 'aria': 'aria_375', 'visible': 'visible_script_375'}[sys.argv[2]]
records = []
for line in Path(sys.argv[1]).read_text().splitlines():
    try: records.append(json.loads(line))
    except json.JSONDecodeError: pass
failed = [r for r in records if isinstance(r, dict) and r.get('name') == expected and r.get('status') == 'fail']
completed = any(isinstance(r, dict) and r.get('name') == 'probe_completed' and r.get('status') == 'pass' for r in records)
if not failed or not completed: raise SystemExit('no completed matching browser assertion; build/startup/probe errors are not mutation evidence')
print('Observed browser red: ' + json.dumps(failed[0]))
ORACLE
done
restore_constraint_sources
run_constraint_phase restored
python3 - "$constraint_proof_dir" <<'ORACLE'
import json, sys
from pathlib import Path
expected = ['served_js', 'served_css', 'probe_completed']
for width in (375, 1440):
    expected += [f'aria_{width}']
    for mode in ('script', 'native'):
        expected += [f'{name}_{mode}_{width}' for name in ('initial', 'pattern', 'visible', 'recovery', 'postal_bound', 'unicode_city', 'unicode_district')]
for phase in ('baseline', 'restored'):
    records = []
    for line in (Path(sys.argv[1]) / f'{phase}.jsonl').read_text().splitlines():
        try: records.append(json.loads(line))
        except json.JSONDecodeError: pass
    for name in expected:
        if not any(isinstance(r, dict) and r.get('name') == name and r.get('status') == 'pass' for r in records):
            raise SystemExit(phase + ' did not pass ' + name)
ORACLE
