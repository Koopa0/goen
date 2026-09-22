#!/usr/bin/env bash
# Accept only a rendered-navigation assertion as evidence; compilation failures
# must never satisfy a permission regression check.
set -euo pipefail
cd "$(dirname "$0")/.."

nav_proof_dir="${RUNNER_TEMP:-/tmp}/goen-staff-navigation-mutations"
mkdir -p "$nav_proof_dir"
nav_backup_dir=$(mktemp -d)
nav_paths=(internal/admin/handler.go internal/ui/layouts/admin.templ internal/ui/layouts/admin_templ.go)
for i in "${!nav_paths[@]}"; do
  cp "${nav_paths[$i]}" "$nav_backup_dir/$i"
done
restore_nav_sources() {
  for i in "${!nav_paths[@]}"; do
    cp "$nav_backup_dir/$i" "${nav_paths[$i]}"
    cmp "$nav_backup_dir/$i" "${nav_paths[$i]}"
  done
}
cleanup_nav_sources() {
  restore_nav_sources
  rm -rf "$nav_backup_dir"
}
trap cleanup_nav_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$nav_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$nav_proof_dir/pr-head.txt"
nav_test=TestStaffNavigationOnlyOmitsTheAdminOnlyDestination
go test -json -race -count=1 -timeout=3m ./internal/admin -run "^$nav_test$" > "$nav_proof_dir/baseline.jsonl" 2>&1

for nav_mutant in template context; do
  restore_nav_sources
  python3 - "$nav_mutant" <<'PY'
import sys
from pathlib import Path

changes = {
    'template': ('internal/ui/layouts/admin.templ',
                 'if isAdmin(ctx) {', 'if true {'),
    'context': ('internal/admin/handler.go',
                'next(w, r.WithContext(layouts.WithAdmin(r.Context(), u.IsAdmin())))',
                'next(w, r.WithContext(layouts.WithAdmin(r.Context(), false)))'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
source = p.read_text()
if source.count(before) != 1:
    raise SystemExit('mutation target is no longer unique: ' + name)
p.write_text(source.replace(before, after, 1))
PY
  nav_marker='admin staff-management link=false'
  if [[ "$nav_mutant" == template ]]; then
    go tool templ generate -path internal/ui > "$nav_proof_dir/template-generation.log" 2>&1
    grep -F -A 1 'if true {' internal/ui/layouts/admin_templ.go > "$nav_proof_dir/template-generated-target.txt"
    grep -F '/admin/staff' "$nav_proof_dir/template-generated-target.txt"
    nav_marker='staff staff-management link=true'
  fi
  git diff -- "${nav_paths[@]}" > "$nav_proof_dir/$nav_mutant.patch"
  nav_exit=0
  go test -json -race -count=1 -timeout=3m ./internal/admin -run "^$nav_test$" > "$nav_proof_dir/$nav_mutant.jsonl" 2>&1 || nav_exit=$?
  printf '%s\n' "$nav_exit" > "$nav_proof_dir/$nav_mutant.exit"
  if [[ "$nav_exit" == 0 ]]; then
    echo "mutation $nav_mutant survived" >&2
    exit 1
  fi
  python3 - "$nav_proof_dir/$nav_mutant.jsonl" "$nav_test" "$nav_marker" <<'PY'
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
              if e.get('Test', '').startswith(test + '/') and e.get('Test') in failed_tests
              and marker in e.get('Output', '')]
if not (ran and failed and assertions):
    raise SystemExit('no matching runtime assertion; this failure is not mutation evidence')
print('Observed runtime red for ' + test + ': ' + assertions[0])
PY
done

restore_nav_sources
go test -json -race -count=1 -timeout=3m ./internal/admin -run "^$nav_test$" > "$nav_proof_dir/restored.jsonl" 2>&1
python3 - "$nav_proof_dir" "$nav_test" <<'PY'
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
    for action in ('run', 'pass'):
        if not any(e.get('Test') == sys.argv[2] and e.get('Action') == action for e in records):
            raise SystemExit(name + ' did not ' + action + ' ' + sys.argv[2])
PY
echo 'staff navigation production sources restored; scoped test passed'
