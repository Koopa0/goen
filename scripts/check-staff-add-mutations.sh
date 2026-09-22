#!/usr/bin/env bash
# Exercise the production rejection, HTTP response and generated error markup;
# a failing build or unavailable database must not count as an observed defect.
set -euo pipefail
cd "$(dirname "$0")/.."

staff_proof_dir="${RUNNER_TEMP:-/tmp}/goen-staff-add-mutations"
mkdir -p "$staff_proof_dir"
staff_backup_dir=$(mktemp -d)
staff_paths=(migrations/001_initial_schema.up.sql internal/twofactor/handler.go internal/ui/pages/adminstaff.templ internal/ui/pages/adminstaff_templ.go)
for i in "${!staff_paths[@]}"; do
  cp "${staff_paths[$i]}" "$staff_backup_dir/$i"
done
restore_staff_sources() {
  for i in "${!staff_paths[@]}"; do
    cp "$staff_backup_dir/$i" "${staff_paths[$i]}"
    cmp "$staff_backup_dir/$i" "${staff_paths[$i]}"
  done
}
cleanup_staff_sources() {
  restore_staff_sources
  rm -rf "$staff_backup_dir"
}
trap cleanup_staff_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$staff_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$staff_proof_dir/pr-head.txt"
staff_cases='TestAddingExistingStaffPreservesTheirAccount|TestConcurrentStaffAddsGrantOnce|TestDuplicateStaffFormRetainsInputAndExplainsRefusal'
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/twofactor -run "^($staff_cases)$" > "$staff_proof_dir/baseline.jsonl" 2>&1

for staff_mutant in roster http field; do
  restore_staff_sources
  python3 - "$staff_mutant" <<'PY'
import sys
from pathlib import Path
changes = {
    'roster': ('migrations/001_initial_schema.up.sql',
               "    WHERE users.role = 'customer'\n    RETURNING id INTO promoted_id;",
               '    RETURNING id INTO promoted_id;'),
    'http': ('internal/twofactor/handler.go',
             'if errors.Is(err, ErrAlreadyStaff) {',
             'if errors.Is(err, ErrSelf) {'),
    'field': ('internal/ui/pages/adminstaff.templ',
              'Invalid: v.AddError != "", Describes: "staff-email-error",',
              'Invalid: false, Describes: "staff-email-error",'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
s = p.read_text()
if s.count(before) != 1:
    raise SystemExit('mutation target is no longer unique: ' + name)
p.write_text(s.replace(before, after, 1))
PY
  if [[ "$staff_mutant" == field ]]; then
    go tool templ generate -path internal/ui > "$staff_proof_dir/field-generation.log" 2>&1
    grep -F 'Invalid: false, Describes: "staff-email-error"' internal/ui/pages/adminstaff_templ.go > "$staff_proof_dir/field-generated-target.txt"
  fi
  git diff -- "${staff_paths[@]}" > "$staff_proof_dir/$staff_mutant.patch"
  staff_test=TestDuplicateStaffFormRetainsInputAndExplainsRefusal
  staff_marker='duplicate POST = 500, want 422'
  if [[ "$staff_mutant" == roster ]]; then
    staff_test=TestAddingExistingStaffPreservesTheirAccount
    staff_marker='want false, ErrAlreadyStaff'
  elif [[ "$staff_mutant" == field ]]; then
    staff_marker='duplicate POST omitted "aria-invalid'
  fi
  staff_exit=0
  go test -json -tags=integration -race -count=1 -timeout=5m ./internal/twofactor -run "^$staff_test$" > "$staff_proof_dir/$staff_mutant.jsonl" 2>&1 || staff_exit=$?
  if [[ "$staff_exit" == 0 ]]; then
    echo "mutation $staff_mutant survived" >&2
    exit 1
  fi
  python3 - "$staff_proof_dir/$staff_mutant.jsonl" "$staff_test" "$staff_marker" <<'PY'
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
             if e.get('Test', '').startswith(test + '/') and e.get('Test') in failed_tests
             and marker in e.get('Output', '')]
if not (ran and failed and assertion):
    raise SystemExit('no matching runtime assertion; this failure is not mutation evidence')
print('Observed runtime red for ' + test + ': ' + assertion[0])
PY
done

restore_staff_sources
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/twofactor -run "^($staff_cases)$" > "$staff_proof_dir/restored.jsonl" 2>&1
python3 - "$staff_proof_dir" "$staff_cases" <<'PY'
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
echo 'staff-add production sources restored; scoped tests passed'
