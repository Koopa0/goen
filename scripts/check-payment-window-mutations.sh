#!/usr/bin/env bash
# Exercise closed-window recovery, deadlines and existing-session continuation;
# a failing build or unavailable database must not count as an observed defect.
set -euo pipefail
cd "$(dirname "$0")/.."

window_proof_dir="${RUNNER_TEMP:-/tmp}/goen-payment-window-mutations"
mkdir -p "$window_proof_dir"
window_backup_dir=$(mktemp -d)
window_paths=(internal/payment/handler.go internal/ui/pages/pay.templ internal/ui/pages/pay_templ.go)
for i in "${!window_paths[@]}"; do
  cp "${window_paths[$i]}" "$window_backup_dir/$i"
done
restore_window_sources() {
  for i in "${!window_paths[@]}"; do
    cp "$window_backup_dir/$i" "${window_paths[$i]}"
    cmp "$window_backup_dir/$i" "${window_paths[$i]}"
  done
}
cleanup_window_sources() {
  restore_window_sources
  rm -rf "$window_backup_dir"
}
trap cleanup_window_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$window_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$window_proof_dir/pr-head.txt"
window_cases='TestPaymentWindowPageAndPostOfferRecovery|TestExistingPaymentSessionResumesAfterStartWindowCloses'
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/payment -run "^($window_cases)$" > "$window_proof_dir/baseline.jsonl" 2>&1

for window_mutant in surface post resume deadline; do
  restore_window_sources
  python3 - "$window_mutant" <<'PY'
import sys
from pathlib import Path
changes = {
    'surface': ('internal/ui/pages/pay.templ',
                'if v.WindowClosed {\n\t\t\t\t\t@components.Notice',
                'if false {\n\t\t\t\t\t@components.Notice'),
    'post': ('internal/payment/handler.go',
             'h.renderPay(w, r, o, false, http.StatusConflict)',
             'h.paymentConflict(w, r)'),
    'resume': ('internal/payment/handler.go',
               'h.renderPay(w, r, o, attempt.SessionID != "", http.StatusOK)',
               'h.renderPay(w, r, o, false, http.StatusOK)'),
    'deadline': ('internal/payment/handler.go',
                 'if !hasSession && o.holdCoversSession {',
                 'if false {'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
s = p.read_text()
if s.count(before) != 1:
    raise SystemExit('mutation target is no longer unique: ' + name)
p.write_text(s.replace(before, after, 1))
PY
  if [[ "$window_mutant" == surface ]]; then
    go tool templ generate -path internal/ui > "$window_proof_dir/surface-generation.log" 2>&1
    grep -F -A 3 'if false {' internal/ui/pages/pay_templ.go > "$window_proof_dir/surface-generated-target.txt"
  fi
  git diff -- "${window_paths[@]}" > "$window_proof_dir/$window_mutant.patch"
  window_test=TestPaymentWindowPageAndPostOfferRecovery
  case "$window_mutant" in
    surface) window_marker='closed window still offers a payment form' ;;
    post) window_marker='closed window omitted recovery' ;;
    resume)
      window_test=TestExistingPaymentSessionResumesAfterStartWindowCloses
      window_marker='existing session lost payment form'
      ;;
    deadline) window_marker='open window omitted payment form or deadline' ;;
  esac
  window_exit=0
  go test -json -tags=integration -race -count=1 -timeout=5m ./internal/payment -run "^$window_test$" > "$window_proof_dir/$window_mutant.jsonl" 2>&1 || window_exit=$?
  if [[ "$window_exit" == 0 ]]; then
    echo "mutation $window_mutant survived" >&2
    exit 1
  fi
  python3 - "$window_proof_dir/$window_mutant.jsonl" "$window_test" "$window_marker" <<'PY'
import json
import sys
from pathlib import Path
records = []
for line in Path(sys.argv[1]).read_text().splitlines():
    try:
        event = json.loads(line)
    except json.JSONDecodeError:
        continue
    if isinstance(event, dict):
        records.append(event)
test, marker = sys.argv[2:]
ran = any(e.get('Action') == 'run' and e.get('Test') == test for e in records)
failed = any(e.get('Action') == 'fail' and e.get('Test') == test for e in records)
failed_tests = {e.get('Test') for e in records if e.get('Action') == 'fail'}
assertion = [e.get('Output', '').strip() for e in records
             if (e.get('Test') == test or e.get('Test', '').startswith(test + '/'))
             and e.get('Test') in failed_tests
             and marker in e.get('Output', '')]
if not (ran and failed and assertion):
    raise SystemExit('no matching runtime assertion; this failure is not mutation evidence')
print('Observed runtime red for ' + test + ': ' + assertion[0])
PY
done

restore_window_sources
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/payment -run "^($window_cases)$" > "$window_proof_dir/restored.jsonl" 2>&1
python3 - "$window_proof_dir" "$window_cases" <<'PY'
import json
import sys
from pathlib import Path
for name in ('baseline', 'restored'):
    records = []
    for line in (Path(sys.argv[1]) / (name + '.jsonl')).read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(event, dict):
            records.append(event)
    for test in sys.argv[2].split('|'):
        for action in ('run', 'pass'):
            if not any(e.get('Test') == test and e.get('Action') == action for e in records):
                raise SystemExit(name + ' did not ' + action + ' ' + test)
PY
echo 'payment-window production sources restored; scoped tests passed'
