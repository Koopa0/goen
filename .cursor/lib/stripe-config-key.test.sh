#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
lib="${script_dir}/stripe-config-key.sh"
failures=0

assert_key() {
	local label="$1"
	local config_path="$2"
	local expected="$3"
	local actual
	actual="$(bash "$lib" "$config_path")"
	if [ "$actual" = "$expected" ]; then
		echo "PASS: $label"
	else
		echo "FAIL: $label (expected '$expected', got '$actual')"
		failures=$((failures + 1))
	fi
}

assert_key "single-quoted config" \
	"${script_dir}/testdata/config-single-quoted.toml" \
	"rkcs_test_ABC123singlequoted"

assert_key "double-quoted config" \
	"${script_dir}/testdata/config-double-quoted.toml" \
	"rkcs_test_XYZ789doublequoted"

actual="$(bash "$lib" "${script_dir}/testdata/does-not-exist.toml")"
if [ -z "$actual" ]; then
	echo "PASS: missing config file"
else
	echo "FAIL: missing config file (expected empty output, got '$actual')"
	failures=$((failures + 1))
fi

if [ "$failures" -eq 0 ]; then
	exit 0
fi
exit 1
