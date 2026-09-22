#!/usr/bin/env bash
# Runtime assertion evidence for field precedence and stable relevance ties.
set -euo pipefail
cd "$(dirname "$0")/.."

relevance_proof_dir="${RUNNER_TEMP:-/tmp}/goen-relevance-mutations"
mkdir -p "$relevance_proof_dir"
relevance_backup_dir=$(mktemp -d)
relevance_paths=(internal/catalog/query.sql internal/db/query.sql.go)
for i in "${!relevance_paths[@]}"; do
  cp "${relevance_paths[$i]}" "$relevance_backup_dir/$i"
done
restore_relevance_sources() {
  for i in "${!relevance_paths[@]}"; do
    cp "$relevance_backup_dir/$i" "${relevance_paths[$i]}"
    cmp "$relevance_backup_dir/$i" "${relevance_paths[$i]}"
  done
}
cleanup_relevance_sources() {
  restore_relevance_sources
  rm -rf "$relevance_backup_dir"
}
trap cleanup_relevance_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$relevance_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$relevance_proof_dir/pr-head.txt"
relevance_cases='TestSearchOrdersExplicitFieldRelevanceBeforeRecency|TestSearchRelevanceTiesUsePublicationThenIdentity'
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/catalog -run "^($relevance_cases)$" > "$relevance_proof_dir/baseline.jsonl" 2>&1

for relevance_mutant in exact field date identity; do
  restore_relevance_sources
  python3 - "$relevance_mutant" <<'PY'
import sys
from pathlib import Path
p = Path('internal/catalog/query.sql')
s = p.read_text()
start = s.index('-- name: SearchProducts :many')
end = s.index('-- name: SearchProductsCount :one', start)
part = s[start:end]
changes = {
    'exact': ('ILIKE @exact_pattern::text THEN 4', 'ILIKE @exact_pattern::text THEN 3'),
    'field': ('WHEN b.name ILIKE @pattern::text THEN 2', 'WHEN b.name ILIKE @pattern::text THEN 0'),
    'date': ('p.published_at DESC, p.id DESC', 'p.published_at ASC, p.id DESC'),
    'identity': ('p.published_at DESC, p.id DESC', 'p.published_at DESC, p.id ASC'),
}
before, after = changes[sys.argv[1]]
if part.count(before) != 1:
    raise SystemExit('relevance mutation target is no longer unique')
part = part.replace(before, after, 1)
p.write_text(s[:start] + part + s[end:])
PY
  make sqlc > "$relevance_proof_dir/$relevance_mutant-generation.log" 2>&1
  relevance_package=./internal/catalog
  relevance_test=TestSearchOrdersExplicitFieldRelevanceBeforeRecency
  relevance_marker='rank 0 = '
  relevance_generated='::text THEN 3'
  case "$relevance_mutant" in
    field) relevance_generated='::text THEN 0'; relevance_marker='rank 2 = ' ;;
    date)
      relevance_generated='p.published_at ASC, p.id DESC'
      relevance_test=TestSearchRelevanceTiesUsePublicationThenIdentity
      relevance_marker='tie order 0 = '
      ;;
    identity)
      relevance_generated='p.published_at DESC, p.id ASC'
      relevance_test=TestSearchRelevanceTiesUsePublicationThenIdentity
      relevance_marker='tie order 0 = '
      ;;
  esac
  grep -F "$relevance_generated" internal/db/query.sql.go > "$relevance_proof_dir/$relevance_mutant-generated-target.txt"
  git diff -- "${relevance_paths[@]}" > "$relevance_proof_dir/$relevance_mutant.patch"
  relevance_exit=0
  go test -json -tags=integration -race -count=1 -timeout=5m "$relevance_package" -run "^$relevance_test$" > "$relevance_proof_dir/$relevance_mutant.jsonl" 2>&1 || relevance_exit=$?
  if [[ "$relevance_exit" == 0 ]]; then
    echo "mutation $relevance_mutant survived" >&2
    exit 1
  fi
  python3 - "$relevance_proof_dir/$relevance_mutant.jsonl" "$relevance_test" "$relevance_marker" <<'PY'
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

restore_relevance_sources
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/catalog -run "^($relevance_cases)$" > "$relevance_proof_dir/restored.jsonl" 2>&1
python3 - "$relevance_proof_dir" "$relevance_cases" <<'PY'
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
echo 'relevance verification sources restored; scoped tests passed'
