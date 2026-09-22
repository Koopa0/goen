#!/usr/bin/env bash
# Runtime assertion evidence for personal comparison state and shared snapshots.
set -euo pipefail
cd "$(dirname "$0")/.."

comparison_proof_dir="${RUNNER_TEMP:-/tmp}/goen-comparison-mutations"
mkdir -p "$comparison_proof_dir"
comparison_backup_dir=$(mktemp -d)
comparison_paths=(internal/comparison/selection.go internal/catalog/comparison_selection.go internal/catalog/handler.go)
for i in "${!comparison_paths[@]}"; do
  cp "${comparison_paths[$i]}" "$comparison_backup_dir/$i"
done
restore_comparison_sources() {
  for i in "${!comparison_paths[@]}"; do
    cp "$comparison_backup_dir/$i" "${comparison_paths[$i]}"
    cmp "$comparison_backup_dir/$i" "${comparison_paths[$i]}"
  done
}
cleanup_comparison_sources() {
  restore_comparison_sources
  rm -rf "$comparison_backup_dir"
}
trap cleanup_comparison_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$comparison_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$comparison_proof_dir/pr-head.txt"
comparison_cases='TestComparisonCookieIsExplicitBoundedAndSessionOnly|TestComparisonBoundsReportOverflow|TestComparisonSelectionPOSTsAndReadOnlySnapshots|TestComparisonBuilderSearchesAndRefusesUnavailableProducts'
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/comparison ./internal/catalog -run "^($comparison_cases)$" > "$comparison_proof_dir/baseline.jsonl" 2>&1

for comparison_mutant in session cap snapshot persist; do
  restore_comparison_sources
  python3 - "$comparison_mutant" <<'PY'
import sys
from pathlib import Path
changes = {
    'session': ('internal/comparison/selection.go', '\tif len(selected) == 0 {', '\tcookie.MaxAge = 86400\n\tif len(selected) == 0 {'),
    'cap': ('internal/catalog/comparison_selection.go', 'case len(selected) == comparison.Max:', 'case false:'),
    'snapshot': ('internal/catalog/handler.go', '\tview.Snapshot = snapshot', '\tif snapshot { comparison.Write(w, raw, h.secureComparison) }\n\tview.Snapshot = snapshot'),
    'persist': ('internal/catalog/comparison_selection.go', 'if outcome != "unavailable" && outcome != "full" {', 'if false && outcome != "unavailable" && outcome != "full" {'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
s = p.read_text()
if s.count(before) != 1:
    raise SystemExit('mutation target is no longer unique: ' + name)
p.write_text(s.replace(before, after, 1))
PY
  git diff -- "${comparison_paths[@]}" > "$comparison_proof_dir/$comparison_mutant.patch"
  comparison_package=./internal/catalog
  comparison_test=TestComparisonSelectionPOSTsAndReadOnlySnapshots
  comparison_marker='fifth product silently changed the selection'
  case "$comparison_mutant" in
    session)
      comparison_package=./internal/comparison
      comparison_test=TestComparisonCookieIsExplicitBoundedAndSessionOnly
      comparison_marker='cookie = '
      ;;
    snapshot) comparison_marker='shared GET changed personal state' ;;
    persist) comparison_marker='selection action did not write a comparison cookie' ;;
  esac
  comparison_exit=0
  go test -json -tags=integration -race -count=1 -timeout=5m "$comparison_package" -run "^$comparison_test$" > "$comparison_proof_dir/$comparison_mutant.jsonl" 2>&1 || comparison_exit=$?
  if [[ "$comparison_exit" == 0 ]]; then
    echo "mutation $comparison_mutant survived" >&2
    exit 1
  fi
  python3 - "$comparison_proof_dir/$comparison_mutant.jsonl" "$comparison_test" "$comparison_marker" <<'PY'
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

restore_comparison_sources
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/comparison ./internal/catalog -run "^($comparison_cases)$" > "$comparison_proof_dir/restored.jsonl" 2>&1
python3 - "$comparison_proof_dir" "$comparison_cases" <<'PY'
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
echo 'comparison verification sources restored; scoped tests passed'
