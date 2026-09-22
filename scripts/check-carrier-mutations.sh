#!/usr/bin/env bash
# Runtime assertion evidence for checkout's auxiliary carrier verification.
set -euo pipefail
cd "$(dirname "$0")/.."

carrier_proof_dir="${RUNNER_TEMP:-/tmp}/goen-carrier-mutations"
mkdir -p "$carrier_proof_dir"
carrier_backup_dir=$(mktemp -d)
carrier_paths=(internal/invoice/carrier.go internal/cart/carrier.go internal/cart/handler.go internal/ui/pages/cart.templ internal/ui/pages/cart_templ.go)
for i in "${!carrier_paths[@]}"; do
  cp "${carrier_paths[$i]}" "$carrier_backup_dir/$i"
done
restore_carrier_sources() {
  for i in "${!carrier_paths[@]}"; do
    cp "$carrier_backup_dir/$i" "${carrier_paths[$i]}"
    cmp "$carrier_backup_dir/$i" "${carrier_paths[$i]}"
  done
}
cleanup_carrier_sources() {
  restore_carrier_sources
  rm -rf "$carrier_backup_dir"
}
trap cleanup_carrier_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$carrier_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$carrier_proof_dir/pr-head.txt"
carrier_cases='TestCarrierCheckRequiresAnExistenceVerdict|TestKnownMissingCarrierCannotBeOverridden|TestUnknownCarrierRequiresAFreshChoiceAndCheck|TestUnknownCarrierChoiceCannotBypassANewMissingVerdict|TestCarrierCheckRunsOnlyAfterLocalPlacementValidation|TestCarrierCheckRateLimitDoesNotCountAsProviderUnavailability|TestAnExistingCarrierIsFrozenAfterTheCheck'
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/invoice ./internal/cart -run "^($carrier_cases)$" > "$carrier_proof_dir/baseline.jsonl" 2>&1

for carrier_mutant in verdict hook known unknown control; do
  restore_carrier_sources
  python3 - "$carrier_mutant" <<'PY'
import sys
from pathlib import Path
changes = {
    'verdict': ('internal/invoice/carrier.go', 'case "N":\n\t\treturn CarrierMissing, nil', 'case "N":\n\t\treturn CarrierExists, nil'),
    'hook': ('internal/cart/handler.go', 'if !h.checkMobileCarrier(w, r, cartID, submission) {', 'if false && !h.checkMobileCarrier(w, r, cartID, submission) {'),
    'known': ('internal/cart/carrier.go', 'case invoice.CarrierMissing:\n', 'case invoice.CarrierMissing:\n\t\t\tif r.PostFormValue("invoice_carrier_continue") == "1" { return true }\n'),
    'unknown': ('internal/cart/carrier.go', 'if r.Context().Err() == nil && r.PostFormValue("invoice_carrier_continue") == "1" {', 'if r.Context().Err() == nil {'),
    'control': ('internal/ui/pages/cart.templ', '"name": "invoice_carrier_continue"', '"name": "invoice_carrier_unconfirmed"'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
s = p.read_text()
if s.count(before) != 1:
    raise SystemExit('mutation target is no longer unique: ' + name)
p.write_text(s.replace(before, after, 1))
PY
  if [[ "$carrier_mutant" == control ]]; then
    go tool templ generate -path internal/ui > "$carrier_proof_dir/control-generation.log" 2>&1
    grep -F '"name": "invoice_carrier_unconfirmed"' internal/ui/pages/cart_templ.go > "$carrier_proof_dir/control-generated-target.txt"
  fi
  git diff -- "${carrier_paths[@]}" > "$carrier_proof_dir/$carrier_mutant.patch"
  carrier_package=./internal/cart
  carrier_test=TestKnownMissingCarrierCannotBeOverridden
  carrier_marker='missing carrier: status=303'
  case "$carrier_mutant" in
    verdict)
      carrier_package=./internal/invoice
      carrier_test=TestCarrierCheckRequiresAnExistenceVerdict
      carrier_marker='CheckBarcode = 1, <nil>; want 2'
      ;;
    unknown)
      carrier_test=TestUnknownCarrierRequiresAFreshChoiceAndCheck
      carrier_marker='unknown response = 303'
      ;;
    control)
      carrier_test=TestUnknownCarrierRequiresAFreshChoiceAndCheck
      carrier_marker='unknown state lost'
      ;;
  esac
  carrier_exit=0
  go test -json -tags=integration -race -count=1 -timeout=5m "$carrier_package" -run "^$carrier_test$" > "$carrier_proof_dir/$carrier_mutant.jsonl" 2>&1 || carrier_exit=$?
  if [[ "$carrier_exit" == 0 ]]; then
    echo "mutation $carrier_mutant survived" >&2
    exit 1
  fi
  python3 - "$carrier_proof_dir/$carrier_mutant.jsonl" "$carrier_test" "$carrier_marker" <<'PY'
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
assertions = [e.get('Output', '').strip() for e in records
              if (e.get('Test') == test or e.get('Test', '').startswith(test + '/'))
              and e.get('Test') in failed_tests and marker in e.get('Output', '')]
if not (ran and failed and assertions):
    raise SystemExit('no matching failed-test assertion; build and startup failures are not mutation evidence')
print('Observed runtime red for ' + test + ': ' + assertions[0])
PY
done

restore_carrier_sources
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/invoice ./internal/cart -run "^($carrier_cases)$" > "$carrier_proof_dir/restored.jsonl" 2>&1
python3 - "$carrier_proof_dir" "$carrier_cases" <<'PY'
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
echo 'carrier verification sources restored; scoped tests passed'
