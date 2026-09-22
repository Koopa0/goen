#!/usr/bin/env bash
# A compiler or database startup failure cannot prove that the payment return
# protects a customer; each mutation must reach its specific runtime assertion.
set -euo pipefail
cd "$(dirname "$0")/.."

return_proof_dir="${RUNNER_TEMP:-/tmp}/goen-payment-return-mutations"
mkdir -p "$return_proof_dir"
return_backup_dir=$(mktemp -d)
return_paths=(internal/cart/handler.go internal/ui/pages/cart.templ internal/ui/pages/cart_templ.go)
for i in "${!return_paths[@]}"; do
  cp "${return_paths[$i]}" "$return_backup_dir/$i"
done
restore_return_sources() {
  for i in "${!return_paths[@]}"; do
    cp "$return_backup_dir/$i" "${return_paths[$i]}"
    cmp "$return_backup_dir/$i" "${return_paths[$i]}"
  done
}
cleanup_return_sources() {
  restore_return_sources
  rm -rf "$return_backup_dir"
}
trap cleanup_return_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Require test events as well as the process status, so a renamed or skipped
# test cannot turn an empty run into proof of the restored production code.
check_return_events() {
  python3 - "$@" <<'PY'
import json
import sys
from pathlib import Path

log, mode, *expected = sys.argv[1:]
records = []
for line in Path(log).read_text().splitlines():
    try:
        event = json.loads(line)
    except json.JSONDecodeError:
        continue
    if isinstance(event, dict):
        records.append(event)

def has(action, test):
    return any(e.get('Action') == action and e.get('Test') == test for e in records)

if mode == 'green':
    if not all(has('run', test) and has('pass', test) for test in expected):
        raise SystemExit('required tests did not run and pass')
    if any(e.get('Action') in ('fail', 'build-fail', 'skip') for e in records):
        raise SystemExit('a skipped or failed run is not baseline/restored evidence')
else:
    test, marker = expected
    assertions = [e for e in records
                  if e.get('Test', '').startswith(test + '/')
                  and marker in e.get('Output', '')
                  and has('run', e['Test']) and has('fail', e['Test'])]
    if not (has('run', test) and has('fail', test) and assertions):
        raise SystemExit('no matching runtime assertion in a failed subtest')
    if any(e.get('Action') == 'build-fail'
           or 'panic:' in e.get('Output', '')
           or '[build failed]' in e.get('Output', '') for e in records):
        raise SystemExit('build failure or panic cannot count as mutation evidence')
    print('Observed runtime red: ' + assertions[0]['Output'].strip())
PY
}

git rev-parse HEAD > "$return_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$return_proof_dir/pr-head.txt"
return_test=TestPaymentReturnHintExpiresWithoutChangingTheOrder
return_state_test=TestPaymentReturnNeverOverridesTerminalOrFundedState
return_cases="^($return_test|$return_state_test)$"
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/cart -run "$return_cases" > "$return_proof_dir/baseline.jsonl" 2>&1
check_return_events "$return_proof_dir/baseline.jsonl" green "$return_test" "$return_state_test"

for return_mutant in hint bound database pay cancel; do
  restore_return_sources
  python3 - "$return_mutant" <<'PY'
import sys
from pathlib import Path

changes = {
    'hint': ('internal/cart/handler.go',
             'view.PaymentRefreshURL = paymentReturnRefresh(r, &view)',
             'view.PaymentRefreshURL = ""'),
    'bound': ('internal/cart/handler.go',
              'if attempt < 2 {',
              'if attempt < 3 {'),
    'database': ('internal/cart/handler.go',
                 'if !view.AwaitingPayment() || r.URL.Query().Get("paid") != "1" {',
                 'if r.URL.Query().Get("paid") != "1" {'),
    'pay': ('internal/ui/pages/cart.templ',
            '} else if v.AwaitingPayment() {',
            '}\n\t\t\tif v.AwaitingPayment() {'),
    'cancel': ('internal/ui/pages/cart.templ',
               'if v.CanCancel() && v.PaymentRefreshURL == "" {',
               'if v.CanCancel() {'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
s = p.read_text()
if s.count(before) != 1:
    raise SystemExit('mutation target is no longer unique: ' + name)
p.write_text(s.replace(before, after, 1))
PY
  if [[ "$return_mutant" == pay || "$return_mutant" == cancel ]]; then
    go tool templ generate -path internal/ui > "$return_proof_dir/$return_mutant-generation.log" 2>&1
    if [[ "$return_mutant" == pay ]]; then
      grep -E '^[[:space:]]*if v\.AwaitingPayment\(\) \{' internal/ui/pages/cart_templ.go > "$return_proof_dir/pay-generated-target.txt"
    else
      grep -F 'if v.CanCancel() {' internal/ui/pages/cart_templ.go > "$return_proof_dir/cancel-generated-target.txt"
    fi
  fi
  git diff -- "${return_paths[@]}" > "$return_proof_dir/$return_mutant.patch"
  case "$return_mutant" in
    hint) return_marker='check 0 missing "id=\"order-payment-processing\""' ;;
    bound) return_marker='hint did not end after three checks:' ;;
    database) return_marker='confirmed order keeps refreshing' ;;
    pay) return_marker='check 0 offers "id=\"order-unpaid\"" before confirmation' ;;
    cancel) return_marker='/cancel\"" before confirmation' ;;
  esac
  return_exit=0
  go test -json -tags=integration -race -count=1 -timeout=5m ./internal/cart -run "^$return_test$" > "$return_proof_dir/$return_mutant.jsonl" 2>&1 || return_exit=$?
  printf '%s\n' "$return_exit" > "$return_proof_dir/$return_mutant-exit.txt"
  if [[ "$return_exit" == 0 ]]; then
    echo "mutation $return_mutant survived" >&2
    exit 1
  fi
  check_return_events "$return_proof_dir/$return_mutant.jsonl" red "$return_test" "$return_marker" > "$return_proof_dir/$return_mutant-assertion.txt"
  cat "$return_proof_dir/$return_mutant-assertion.txt"
done

restore_return_sources
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/cart -run "$return_cases" > "$return_proof_dir/restored.jsonl" 2>&1
check_return_events "$return_proof_dir/restored.jsonl" green "$return_test" "$return_state_test"
echo 'payment-return production sources restored; scoped tests passed'
