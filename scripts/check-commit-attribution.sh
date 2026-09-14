#!/usr/bin/env bash
# Git messages are read as data; none of their text becomes a shell command.
set -euo pipefail

if [[ $# != 2 || ! $1 =~ ^[0-9a-f]{40}$ || ! $2 =~ ^[0-9a-f]{40}$ ]]; then
  echo 'commit-attribution: two full commit SHAs are required' >&2
  exit 2
fi
base=$1
head=$2
git rev-parse --verify "$head^{commit}" >/dev/null
if [[ $base == 0000000000000000000000000000000000000000 ]]; then
  commits=$(git rev-list "$head")
else
  git rev-parse --verify "$base^{commit}" >/dev/null
  commits=$(git rev-list "$base..$head")
fi

status=0
attribution='co[ -](authored|committed)[ -]by[[:space:]]*:|(cursor|codex|claude|copilot|chatgpt)[[:space:]]+agent|cursoragent@|(written|generated|authored|assisted)[[:space:]-]+by[[:space:]:-]+(cursor|codex|claude|copilot|chatgpt|openai|anthropic)|app/cursor'
for sha in $commits; do
  message=$(git log -1 --format=%B "$sha")
  if printf '%s\n' "$message" | LC_ALL=C grep -iE "$attribution" >/dev/null; then
    echo "commit-attribution: forbidden attribution in $sha" >&2
    status=1
  else
    detector_status=$?
    if [[ $detector_status != 1 ]]; then
      echo 'commit-attribution: detector failed' >&2
      exit 2
    fi
  fi
done
if [[ $status != 0 ]]; then
  exit "$status"
fi
echo 'commit-attribution: PASS'
