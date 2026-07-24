#!/usr/bin/env bash
# PreToolUse hook: BLOCK writing secrets to disk before they can be committed.
#
# The git-workflow rule says "NEVER commit .env, credentials, or secrets" and
# config-management warns against hardcoded connection strings — but nothing
# ENFORCED either at write time. This closes that gap deterministically: it is
# the "enforce what Claude frequently violates" move applied to credential leaks.
#
# Two layers:
#   1. Filename — secret-bearing files by name/extension (high precision).
#   2. Content  — high-confidence secret patterns in Write/Edit content
#                 (private keys, cloud keys, VCS tokens, DSNs with a real pw).
#
# Accepted gaps (documented, not bugs): secrets written via Bash redirection
# (covered by the deny-list in settings.json + reviewers), base64-wrapped
# blobs, and novel token formats. Placeholder/example credentials are allowed
# on purpose so docs and tests keep working. Exit 2 + reason on stderr to block,
# matching the sibling PreToolUse hooks (check-anti-patterns.sh).

# The runtime path is relative to this hook; the canonical gate also lints the helper.
# shellcheck disable=SC1091
source "$(dirname "$0")/parse-hook-input.sh"

input=$(cat)
file_path=$(parse_file_path "$input")

[[ -z "$file_path" ]] && exit 0

base=$(basename "$file_path")

# Example/sample/template files are meant to ship placeholder values — allow.
if [[ "$base" == *example* || "$base" == *sample* || "$base" == *template* || "$base" == *.dist || "$base" == *.pub ]]; then
    name_allowed=1
else
    name_allowed=0
fi

# --- Layer 1: filename ---
if [[ "$name_allowed" == 0 ]]; then
    case "$base" in
    .env | .env.* | .netrc | .pgpass | credentials.json | id_rsa | id_dsa | id_ecdsa | id_ed25519 | *.pem | *.key | *.p12 | *.pfx | *.keystore | *.kdbx)
        echo "BLOCKED: '${base}' is a secret-bearing file — never write credentials into the repo. Use os.Getenv / a secret manager and add the file to .gitignore. For a committed template, name it '${base}.example' with placeholder values. See .claude/rules/git-workflow.md." >&2
        exit 2
        ;;
    esac
fi

# --- Layer 2: content ---
content=""
if command -v jq &>/dev/null; then
    content=$(printf '%s' "$input" | jq -r '.tool_input.content // .tool_input.new_string // empty' 2>/dev/null)
fi
[[ -z "$content" ]] && exit 0

# Test code legitimately holds fake/placeholder secrets (testdata/, any tests/
# dir, and *_test.go files). Filename blocking above still applies everywhere —
# only the content scan is skipped here. Matches absolute and relative paths.
if [[ "$file_path" == *testdata/* || "$file_path" == *tests/* || "$file_path" == *_test.go ]]; then
    exit 0
fi

emit() {
    echo "BLOCKED: $1 detected in '${base}'. Do not hardcode secrets — read them from the environment (os.Getenv) or a secret manager. See .claude/rules/git-workflow.md and /config-management." >&2
    exit 2
}

# Private key material (PEM / OpenSSH).
printf '%s' "$content" | grep -qE -- '-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----' && emit "a private key"
# Cloud / provider keys.
printf '%s' "$content" | grep -qE 'AKIA[0-9A-Z]{16}' && emit "an AWS access key id"
printf '%s' "$content" | grep -qE 'ASIA[0-9A-Z]{16}' && emit "an AWS temporary access key"
# VCS / chat tokens.
printf '%s' "$content" | grep -qE '(ghp|gho|ghu|ghs|ghr)_[0-9A-Za-z]{36,}' && emit "a GitHub token"
printf '%s' "$content" | grep -qE 'github_pat_[0-9A-Za-z_]{60,}' && emit "a GitHub fine-grained token"
printf '%s' "$content" | grep -qE 'xox[baprs]-[0-9A-Za-z-]{10,}' && emit "a Slack token"
# Database/connection URL with a *real* (non-placeholder) password.
if printf '%s' "$content" | grep -qE '(postgres|postgresql|mysql|mongodb|mongodb\+srv|redis|amqp)://[^:@/[:space:]]+:[^@/[:space:]]+@'; then
    if ! printf '%s' "$content" | grep -qE '(postgres|postgresql|mysql|mongodb|mongodb\+srv|redis|amqp)://[^:@/[:space:]]+:(password|passwd|pass|postgres|secret|changeme|example|root|admin|user|username|test|placeholder|x+|\*+|\$[A-Za-z_{(]|%[A-Za-z_]|\{\{)[^@/[:space:]]*@'; then
        emit "a database URL with an embedded password"
    fi
fi

exit 0
