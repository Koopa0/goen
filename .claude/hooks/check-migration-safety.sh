#!/usr/bin/env bash
# PreToolUse hook: Block dangerous migration patterns.
# Checks Write/Edit to migrations/*.sql for destructive operations.
# Exit 2 to block, exit 0 to allow.

# The runtime path is relative to this hook; the canonical gate also lints the helper.
# shellcheck disable=SC1091
source "$(dirname "$0")/parse-hook-input.sh"

input=$(cat)
file_path=$(parse_file_path "$input")

# Only check migration files
[[ "$file_path" != *migrations/*.sql ]] && exit 0

# Extract the content being written (new_string for Edit, content for Write)
content=""
if command -v jq &>/dev/null; then
    content=$(echo "$input" | jq -r '.tool_input.content // .tool_input.new_string // empty' 2>/dev/null)
else
    content=$(echo "$input" | grep -o '"content"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/"content"[[:space:]]*:[[:space:]]*"//;s/"$//')
fi

[ -z "$content" ] && exit 0

# Block: DROP TABLE without IF EXISTS
if echo "$content" | grep -iqE 'DROP\s+TABLE' && ! echo "$content" | grep -iqE 'DROP\s+TABLE\s+IF\s+EXISTS'; then
    echo "BLOCKED: DROP TABLE without IF EXISTS in migration. Use 'DROP TABLE IF EXISTS <table>;'" >&2
    exit 2
fi

# Block: DROP COLUMN (always dangerous)
if echo "$content" | grep -iqE 'DROP\s+COLUMN'; then
    echo "BLOCKED: DROP COLUMN in migration. This is irreversible. Add '-- approved: drop column' comment to proceed." >&2
    # Allow if explicitly approved
    echo "$content" | grep -q '-- approved: drop column' && exit 0
    exit 2
fi

# Block: TRUNCATE
if echo "$content" | grep -iqE '^\s*TRUNCATE'; then
    echo "BLOCKED: TRUNCATE in migration. This deletes all data. Use DELETE with WHERE if needed." >&2
    exit 2
fi

# Warn: ALTER TYPE (table rewrite)
if echo "$content" | grep -iqE 'ALTER\s+COLUMN.*TYPE'; then
    echo "WARNING: ALTER COLUMN TYPE causes table rewrite on large tables. Consider add-new-column + backfill + drop-old pattern." >&2
fi

# Warn: RENAME TABLE / RENAME TO (breaks sqlc, application references)
if echo "$content" | grep -iqE 'RENAME\s+(TABLE|TO)'; then
    echo "WARNING: RENAME TABLE/TO breaks sqlc-generated code and application references. Use expand-contract pattern." >&2
fi

# Warn: CREATE INDEX without CONCURRENTLY (locks table on large tables)
if echo "$content" | grep -iqE 'CREATE\s+INDEX' && ! echo "$content" | grep -iqE 'CREATE\s+INDEX\s+CONCURRENTLY'; then
    echo "WARNING: CREATE INDEX without CONCURRENTLY locks the table. Use CREATE INDEX CONCURRENTLY for large tables." >&2
fi

exit 0
