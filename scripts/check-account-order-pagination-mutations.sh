#!/usr/bin/env bash
# Require an executed assertion from the mutated production path; unavailable
# infrastructure and compilation failures cannot establish test sensitivity.
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ "${CI:-}" != true || "${GITHUB_ACTIONS:-}" != true ]]; then
  echo 'This proof runs only in GitHub Actions.' >&2
  exit 2
fi

proof_dir="${RUNNER_TEMP:?}/goen-account-order-pagination-mutations"
mkdir -p "$proof_dir"
backup_dir=$(mktemp -d)
proof_paths=(internal/account/query.sql internal/ui/pages/account.templ)
# Both generators can rewrite their other tracked outputs, so restore those too.
while IFS= read -r generated_path; do
  proof_paths+=("$generated_path")
done < <(git ls-files internal/db/db.go internal/db/models.go internal/db/query.sql.go 'internal/ui/*_templ.go')
for i in "${!proof_paths[@]}"; do
  cp "${proof_paths[$i]}" "$backup_dir/$i"
done
restore_sources() {
  local restore_status=0
  for i in "${!proof_paths[@]}"; do
    cp "$backup_dir/$i" "${proof_paths[$i]}" || restore_status=1
    cmp "$backup_dir/$i" "${proof_paths[$i]}" || restore_status=1
  done
  return "$restore_status"
}
cleanup_sources() {
  local proof_status=$?
  trap - EXIT
  restore_sources || proof_status=1
  rm -rf "$backup_dir"
  exit "$proof_status"
}
trap cleanup_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$proof_dir/pr-head.txt"
integration_test='TestAccountOrdersReachEveryOlderOrderWithoutJavaScript'
integration_children=''
render_test='TestEmptyOlderOrdersPageOffersLatestOrders'
render_children=''
cursor_test='TestOrderCursorIsScopedAndKeepsTimestampPrecision'

assert_green() {
  python3 - "$1" "$integration_test" "$integration_children" "$render_test" "$render_children" "$cursor_test" <<'GREEN'
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
feature = 'github.com/koopa0/goen/internal/account'
pages = 'github.com/koopa0/goen/internal/ui/pages'
integration, children, render, render_children, cursor = sys.argv[2:]
for package, parent, names in ((feature, integration, children), (pages, render, render_children), (feature, cursor, '')):
    tests = [parent] + [parent + '/' + child for child in names.split('|') if child]
    for test in tests:
        for action in ('run', 'pass'):
            if not any(e.get('Package') == package and e.get('Test') == test and e.get('Action') == action for e in records):
                raise SystemExit(f'{sys.argv[1]} did not {action} {package}/{test}')
    if not any(e.get('Package') == package and not e.get('Test') and e.get('Action') == 'pass' for e in records):
        raise SystemExit('package did not pass: ' + package)
print('Named baseline/restored tests and their required subtests ran and passed.')
GREEN
}

run_green() {
  local phase=$1
  local test_status=0
  go test -json -tags=integration -race -count=1 -timeout=5m ./internal/account ./internal/ui/pages \
    -run "^($integration_test|$render_test|$cursor_test)$" > "$proof_dir/$phase.jsonl" 2>&1 || test_status=$?
  printf '%s\n' "$test_status" > "$proof_dir/$phase.exit-status.txt"
  if [[ "$test_status" != 0 ]]; then
    echo "$phase failed; no mutation conclusion can be drawn" >&2
    exit "$test_status"
  fi
  assert_green "$proof_dir/$phase.jsonl" > "$proof_dir/$phase.observed.txt"
}

run_green baseline
for mutant in cursor next first; do
  restore_sources
  python3 - "$mutant" <<'MUTATE'
import re
import sys
from pathlib import Path
mutant = sys.argv[1]
if mutant == 'cursor':
    p = Path('internal/account/query.sql')
    text = p.read_text()
    before = 'NOT @has_cursor::boolean'
    if text.count(before) != 1:
        raise SystemExit('cursor mutation target count changed')
    p.write_text(text.replace(before, '(@has_cursor::boolean OR true)'))
else:
    p = Path('internal/ui/pages/account.templ')
    text = p.read_text()
    field = 'v.OrdersNext' if mutant == 'next' else 'v.OrdersFirst'
    pattern = r'(?m)^([ \t]*)if ' + re.escape(field) + r' != "" \{$'
    changed, count = re.subn(pattern, lambda m: m[1] + 'if ' + field + ' != "" && false {', text)
    if count != 1:
        raise SystemExit('template mutation target is not unique')
    p.write_text(changed)
MUTATE
  if [[ "$mutant" == cursor ]]; then
    make sqlc > "$proof_dir/$mutant-generation.log" 2>&1
    grep -E '\(\$[0-9]+::boolean OR true\)' internal/db/query.sql.go > "$proof_dir/$mutant-generated-target.txt"
    [[ "$(wc -l < "$proof_dir/$mutant-generated-target.txt")" -eq 1 ]]
  else
    go tool templ generate -path internal/ui > "$proof_dir/$mutant-generation.log" 2>&1
    field='v.OrdersNext'
    if [[ "$mutant" == first ]]; then field='v.OrdersFirst'; fi
    grep -F "if $field != \"\" && false {" internal/ui/pages/account_templ.go > "$proof_dir/$mutant-generated-target.txt"
  fi
  git diff -- "${proof_paths[@]}" > "$proof_dir/$mutant.patch"
  test_package=account
  test_name=$integration_test
  test_children=$integration_children
  assertion='new order displaced the next page'
  if [[ "$mutant" == next ]]; then
    assertion='next page is not a plain link'
  elif [[ "$mutant" == first ]]; then
    test_package=ui/pages
    test_name=$render_test
    test_children=$render_children
    assertion='empty older page lost its return to latest orders'
  fi
  test_status=0
  go test -json -tags=integration -race -count=1 -timeout=5m "./internal/$test_package" \
    -run "^$test_name$" > "$proof_dir/$mutant.jsonl" 2>&1 || test_status=$?
  printf '%s\n' "$test_status" > "$proof_dir/$mutant.exit-status.txt"
  if [[ "$test_status" == 0 ]]; then
    echo "mutation $mutant survived" >&2
    exit 1
  fi
  python3 - "$proof_dir/$mutant.jsonl" "$test_package" "$test_name" "$test_children" "$assertion" > "$proof_dir/$mutant.observed.txt" <<'RED'
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
package = 'github.com/koopa0/goen/internal/' + sys.argv[2]
parent, children, marker = sys.argv[3:]
records = [e for e in records if e.get('Package') == package]
for action in ('run', 'fail'):
    if not any(e.get('Test') == parent and e.get('Action') == action for e in records):
        raise SystemExit('no executed failing parent test; build failures do not count')
tests = [parent + '/' + name for name in children.split('|') if name] or [parent]
for test in tests:
    for action in ('run', 'fail'):
        if not any(e.get('Test') == test and e.get('Action') == action for e in records):
            raise SystemExit(f'mutation did not {action} {test}')
    lines = [e.get('Output', '').strip() for e in records
             if e.get('Test') == test and e.get('Action') == 'output' and marker in e.get('Output', '')]
    if not lines:
        raise SystemExit('no matching runtime assertion on the failed test: ' + test)
    print('Observed runtime red for ' + test + ': ' + lines[0])
RED
  cat "$proof_dir/$mutant.observed.txt"
done

restore_sources
git diff --exit-code -- "${proof_paths[@]}" > "$proof_dir/restored-diff.txt"
run_green restored
echo 'account order pagination sources restored exactly; named scoped tests passed.'
