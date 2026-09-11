#!/usr/bin/env bash
# Print the Stripe CLI test_mode_api_key from a config.toml.
# Handles both single-quoted ('...') and double-quoted ("...") TOML values.
set -euo pipefail
config_path="${1:-${HOME}/.config/stripe/config.toml}"
[ -f "$config_path" ] || exit 0
awk '
  /^[[:space:]]*test_mode_api_key[[:space:]]*=/ {
    i = index($0, "=")
    v = substr($0, i + 1)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", v)     # trim outer whitespace
    sub(/^["'"'"']/, "", v)                          # strip leading quote (either kind)
    sub(/["'"'"']$/, "", v)                          # strip trailing quote (either kind)
    print v
    exit
  }' "$config_path"
