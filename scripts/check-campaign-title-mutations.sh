#!/usr/bin/env bash
# Runtime assertion evidence for optional campaign titles and refused drafts.
set -euo pipefail
cd "$(dirname "$0")/.."

campaign_title_proof_dir="${RUNNER_TEMP:-/tmp}/goen-campaign_title-mutations"
mkdir -p "$campaign_title_proof_dir"
campaign_title_backup_dir=$(mktemp -d)
campaign_title_paths=(internal/admin/campaign.go internal/ui/pages/admincampaign.templ internal/ui/pages/admincampaign_templ.go)
for i in "${!campaign_title_paths[@]}"; do
  cp "${campaign_title_paths[$i]}" "$campaign_title_backup_dir/$i"
done
restore_campaign_title_sources() {
  for i in "${!campaign_title_paths[@]}"; do
    cp "$campaign_title_backup_dir/$i" "${campaign_title_paths[$i]}"
    cmp "$campaign_title_backup_dir/$i" "${campaign_title_paths[$i]}"
  done
}
cleanup_campaign_title_sources() {
  restore_campaign_title_sources
  rm -rf "$campaign_title_backup_dir"
}
trap cleanup_campaign_title_sources EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git rev-parse HEAD > "$campaign_title_proof_dir/commit.txt"
printf '%s\n' "${GOEN_PROOF_HEAD:-unknown}" > "$campaign_title_proof_dir/pr-head.txt"
campaign_title_cases='TestCampaignEnglishTitleIsOptionalAndBounded|TestCampaignEnglishTitleReachesStorefront|TestCampaignEnglishTitleRefusalPreservesDraft'
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/admin -run "^($campaign_title_cases)$" > "$campaign_title_proof_dir/baseline.jsonl" 2>&1

for campaign_title_mutant in stored bound draft; do
  restore_campaign_title_sources
  python3 - "$campaign_title_mutant" <<'PY'
import sys
from pathlib import Path
changes = {
    'stored': ('internal/admin/campaign.go', 'Slug: f.Slug, Title: f.Title, TitleEn: f.TitleEn, Days: f.Days,', 'Slug: f.Slug, Title: f.Title, TitleEn: "", Days: f.Days,'),
    'bound': ('internal/admin/campaign.go', 'if utf8.RuneCountInString(f.TitleEn) > MaxCampaignTitleRunes {', 'if false && utf8.RuneCountInString(f.TitleEn) > MaxCampaignTitleRunes {'),
    'draft': ('internal/ui/pages/admincampaign.templ', 'value={ v.Draft.TitleEn }', 'value={ v.Draft.TitleEn + "ignored" }'),
}
name, before, after = changes[sys.argv[1]]
p = Path(name)
s = p.read_text()
if s.count(before) != 1:
    raise SystemExit('mutation target is no longer unique: ' + name)
p.write_text(s.replace(before, after, 1))
PY
  if [[ "$campaign_title_mutant" == draft ]]; then
    go tool templ generate -path internal/ui > "$campaign_title_proof_dir/draft-generation.log" 2>&1
    grep -F 'templ.ResolveAttributeValue(v.Draft.TitleEn + "ignored")' internal/ui/pages/admincampaign_templ.go > "$campaign_title_proof_dir/draft-generated-target.txt"
  fi
  git diff -- "${campaign_title_paths[@]}" > "$campaign_title_proof_dir/$campaign_title_mutant.patch"
  campaign_title_package=./internal/admin
  campaign_title_test=TestCampaignEnglishTitleReachesStorefront
  campaign_title_marker='English storefront title = '
  case "$campaign_title_mutant" in
    bound)
      campaign_title_test=TestCampaignEnglishTitleIsOptionalAndBounded
      campaign_title_marker='English title length 61: errors = '
      ;;
    draft)
      campaign_title_test=TestCampaignEnglishTitleRefusalPreservesDraft
      campaign_title_marker='refused form omits value='
      ;;
  esac
  campaign_title_exit=0
  go test -json -tags=integration -race -count=1 -timeout=5m "$campaign_title_package" -run "^$campaign_title_test$" > "$campaign_title_proof_dir/$campaign_title_mutant.jsonl" 2>&1 || campaign_title_exit=$?
  if [[ "$campaign_title_exit" == 0 ]]; then
    echo "mutation $campaign_title_mutant survived" >&2
    exit 1
  fi
  python3 - "$campaign_title_proof_dir/$campaign_title_mutant.jsonl" "$campaign_title_test" "$campaign_title_marker" <<'PY'
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

restore_campaign_title_sources
go test -json -tags=integration -race -count=1 -timeout=5m ./internal/admin -run "^($campaign_title_cases)$" > "$campaign_title_proof_dir/restored.jsonl" 2>&1
python3 - "$campaign_title_proof_dir" "$campaign_title_cases" <<'PY'
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
echo 'campaign_title verification sources restored; scoped tests passed'
