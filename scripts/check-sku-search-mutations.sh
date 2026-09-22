#!/usr/bin/env bash
# Runtime assertion evidence for receipt lookup ranking and public visibility.
set -euo pipefail
cd "$(dirname "$0")/.."

sku_proof_dir="${RUNNER_TEMP:-/tmp}/goen-sku-mutations"
mkdir -p "$sku_proof_dir"
sku_backup_dir=$(mktemp -d)
sku_paths=(internal/catalog/query.sql internal/db/query.sql.go)
for i in "${!sku_paths[@]}"; do
  cp "${sku_paths[$i]}" "$sku_backup_dir/$i"
done
restore_sku_sources() {
  for i in "${!sku_paths[@]}"; do
    cp "$sku_backup_dir/$i" "${sku_paths[$i]}"
    cmp "$sku_backup_dir/$i" "${sku_paths[$i]}"
  done
}
cleanup_sku_sources() {
  restore_sku_sources
  rm -rf "$sku_backup_dir"
}
trap cleanup_sku_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$sku_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$sku_proof_dir/pr-head.txt"
sku_cases='TestSearchSKUsRankAndPaginateWithoutDuplicateProducts'
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/catalog -run "^($sku_cases)$" > "$sku_proof_dir/baseline.jsonl" 2>&1

for sku_mutant in receipt exact count public; do
  restore_sku_sources
  python3 - "$sku_mutant" <<'PY'
import sys
from pathlib import Path
p = Path('internal/catalog/query.sql')
s = p.read_text()
start = s.index('-- name: SearchProducts :many')
end = s.index('-- "On sale"', start)
part = s[start:end]
mutant = sys.argv[1]
if mutant == 'receipt':
    for alias, count in [('sku_match', 2), ('exact_sku', 1), ('partial_sku', 1)]:
        before = alias + '.product_id = p.id'
        if part.count(before) != count:
            raise SystemExit('SKU predicate count changed')
        part = part.replace(before, before + ' AND ' + alias + '.is_active')
elif mutant == 'exact':
    before = 'ILIKE @exact_pattern::text'
    if part.count(before) != 1:
        raise SystemExit('exact SKU target changed')
    part = part.replace(before, "ILIKE (@exact_pattern::text || '%')")
elif mutant == 'count':
    at = part.index('-- name: SearchProductsCount :one')
    count_part = part[at:]
    before = 'WHERE sku_match.product_id = p.id'
    if count_part.count(before) != 1:
        raise SystemExit('count SKU target changed')
    part = part[:at] + count_part.replace(before, before + ' AND false')
elif mutant == 'public':
    before = "WHERE p.status = 'active'"
    if part.count(before) != 2:
        raise SystemExit('public product boundary changed')
    part = part.replace(before, "WHERE p.status IN ('active', 'draft')")
else:
    raise SystemExit('unknown mutation')
p.write_text(s[:start] + part + s[end:])
PY
  make sqlc > "$sku_proof_dir/$sku_mutant-generation.log" 2>&1
  sku_generated='sku_match.is_active'
  sku_package=./internal/catalog
  sku_test=TestSearchSKUsRankAndPaginateWithoutDuplicateProducts
  sku_marker='SKU pagination total=24'
  case "$sku_mutant" in
    exact) sku_generated="::text || '%')"; sku_marker='SKU/name ranking starts' ;;
    count) sku_generated='sku_match.product_id = p.id AND false'; sku_marker='SKU pagination total=1' ;;
    public) sku_generated="WHERE p.status IN ('active', 'draft')"; sku_marker='SKU search exposed a non-public product' ;;
  esac
  grep -F "$sku_generated" internal/db/query.sql.go > "$sku_proof_dir/$sku_mutant-generated-target.txt"
  git diff -- "${sku_paths[@]}" > "$sku_proof_dir/$sku_mutant.patch"
  sku_exit=0
  go test -json -tags=integration -race -count=1 -timeout=5m "$sku_package" -run "^$sku_test$" > "$sku_proof_dir/$sku_mutant.jsonl" 2>&1 || sku_exit=$?
  if [[ "$sku_exit" == 0 ]]; then
    echo "mutation $sku_mutant survived" >&2
    exit 1
  fi
  python3 - "$sku_proof_dir/$sku_mutant.jsonl" "$sku_test" "$sku_marker" <<'PY'
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

restore_sku_sources
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/catalog -run "^($sku_cases)$" > "$sku_proof_dir/restored.jsonl" 2>&1
python3 - "$sku_proof_dir" "$sku_cases" <<'PY'
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
echo 'sku verification sources restored; scoped tests passed'
