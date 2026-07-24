#!/usr/bin/env bash
# PostToolUse hook: Auto-format .go files after Write|Edit

# The runtime path is relative to this hook; the canonical gate also lints the helper.
# shellcheck disable=SC1091
source "$(dirname "$0")/parse-hook-input.sh"

input=$(cat)
file_path=$(parse_file_path "$input")

if [[ -z "$file_path" || "$file_path" != *.go || ! -f "$file_path" ]]; then
    exit 0
fi

# Try goimports first, fall back to gofmt
if command -v goimports &>/dev/null; then
    if ! goimports -w "$file_path" 2>&1; then
        echo "WARNING: goimports failed on $file_path" >&2
    fi
elif command -v gofmt &>/dev/null; then
    if ! gofmt -w "$file_path" 2>&1; then
        echo "WARNING: gofmt failed on $file_path" >&2
    fi
fi

exit 0
