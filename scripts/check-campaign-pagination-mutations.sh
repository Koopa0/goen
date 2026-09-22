#!/usr/bin/env bash
# Runtime assertion evidence for campaign offsets, request state and navigation.
set -euo pipefail
cd "$(dirname "$0")/.."

campaign_proof_dir="${RUNNER_TEMP:-/tmp}/goen-campaign-mutations"
mkdir -p "$campaign_proof_dir"
campaign_backup_dir=$(mktemp -d)
campaign_paths=(internal/catalog/campaign.go internal/catalog/handler.go internal/ui/pages/deals.templ internal/ui/pages/deals_templ.go)
for i in "${!campaign_paths[@]}"; do
  cp "${campaign_paths[$i]}" "$campaign_backup_dir/$i"
done
restore_campaign_sources() {
  for i in "${!campaign_paths[@]}"; do
    cp "$campaign_backup_dir/$i" "${campaign_paths[$i]}"
    cmp "$campaign_backup_dir/$i" "${campaign_paths[$i]}"
  done
}
cleanup_campaign_sources() {
  restore_campaign_sources
  rm -rf "$campaign_backup_dir"
}
trap cleanup_campaign_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$campaign_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$campaign_proof_dir/pr-head.txt"
campaign_cases='TestRunningCampaignsExposeTheSeventhCampaign|TestCampaignPaginationKeepsBothBrowsingPositions'
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/catalog ./internal/ui/pages -run "^($campaign_cases)$" > "$campaign_proof_dir/baseline.jsonl" 2>&1

for campaign_mutant in offset request navigation; do
  restore_campaign_sources
  python3 - "$campaign_mutant" <<'PY'
import sys
from pathlib import Path
changes = {
    'offset': ('internal/catalog/campaign.go', 'PageOffset: int32((page - 1) * CampaignPageSize)', 'PageOffset: 0'),
    'request': ('internal/catalog/handler.go', 'ParsePage(r.URL.Query().Get("campaign_page"))', 'ParsePage(r.URL.Query().Get("page"))'),
    'navigation': ('internal/ui/pages/deals.templ', 'v.CampaignPageHref(v.Campaigns.Page + 1)', 'v.PageHref(v.Campaigns.Page + 1)'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
s = p.read_text()
if s.count(before) != 1:
    raise SystemExit('mutation target is no longer unique: ' + name)
p.write_text(s.replace(before, after, 1))
PY
  if [[ "$campaign_mutant" == navigation ]]; then
    go tool templ generate -path internal/ui > "$campaign_proof_dir/navigation-generation.log" 2>&1
    grep -F 'v.PageHref(v.Campaigns.Page + 1)' internal/ui/pages/deals_templ.go > "$campaign_proof_dir/navigation-generated-target.txt"
  fi
  git diff -- "${campaign_paths[@]}" > "$campaign_proof_dir/$campaign_mutant.patch"
  campaign_package=./internal/catalog
  campaign_test=TestRunningCampaignsExposeTheSeventhCampaign
  campaign_marker='second page does not expose seventh campaign'
  case "$campaign_mutant" in
    request) campaign_marker='HTTP campaign page two omitted the seventh campaign or repeated page one' ;;
    navigation)
      campaign_package=./internal/ui/pages
      campaign_test=TestCampaignPaginationKeepsBothBrowsingPositions
      campaign_marker='campaign pager omits'
      ;;
  esac
  campaign_exit=0
  go test -json -tags=integration -race -count=1 -timeout=5m "$campaign_package" -run "^$campaign_test$" > "$campaign_proof_dir/$campaign_mutant.jsonl" 2>&1 || campaign_exit=$?
  if [[ "$campaign_exit" == 0 ]]; then
    echo "mutation $campaign_mutant survived" >&2
    exit 1
  fi
  python3 - "$campaign_proof_dir/$campaign_mutant.jsonl" "$campaign_test" "$campaign_marker" <<'PY'
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

restore_campaign_sources
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/catalog ./internal/ui/pages -run "^($campaign_cases)$" > "$campaign_proof_dir/restored.jsonl" 2>&1
python3 - "$campaign_proof_dir" "$campaign_cases" <<'PY'
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
echo 'campaign verification sources restored; scoped tests passed'
