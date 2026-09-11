#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
test_key="${script_dir}/stripe-test-key.sh"
config_key="${script_dir}/stripe-config-key.sh"
sandbox="${repo_root}/.cursor/stripe-sandbox.sh"
failures=0

assert_test_key() {
	local label="$1"
	local key="$2"
	local expect_ok="$3"
	if [ "$expect_ok" = yes ]; then
		if bash "$test_key" "$key"; then
			echo "PASS: $label"
		else
			echo "FAIL: $label (expected TEST-mode acceptance)"
			failures=$((failures + 1))
		fi
	else
		if bash "$test_key" "$key"; then
			echo "FAIL: $label (expected rejection)"
			failures=$((failures + 1))
		else
			echo "PASS: $label"
		fi
	fi
}

assert_test_key "sk_test_ prefix" "sk_test_fixture" yes
assert_test_key "rk_test_ prefix" "rk_test_fixture" yes
assert_test_key "rkcs_test_ prefix" "rkcs_test_fixture" yes
assert_test_key "sk_live_ prefix" "sk_live_fixture" no
assert_test_key "unknown prefix" "pk_test_fixture" no
assert_test_key "empty key" "" no

assert_sandbox_rejects_config() {
	local label="$1"
	local fixture="$2"
	local tmpdir marker
	tmpdir="$(mktemp -d)"
	marker="${tmpdir}/stripe-called"
	mkdir -p "${tmpdir}/.config/stripe" "${tmpdir}/bin"
	cp "$fixture" "${tmpdir}/.config/stripe/config.toml"
	cat >"${tmpdir}/bin/stripe" <<EOF
#!/usr/bin/env bash
touch "${marker}"
exit 99
EOF
	chmod +x "${tmpdir}/bin/stripe"
	if HOME="${tmpdir}" PATH="${tmpdir}/bin:${PATH}" bash "$sandbox" >/dev/null 2>&1; then
		echo "FAIL: $label (expected non-zero exit)"
		failures=$((failures + 1))
		rm -rf "$tmpdir"
		return
	fi
	if [ -f "$marker" ]; then
		echo "FAIL: $label (stripe CLI was invoked before rejecting the key)"
		failures=$((failures + 1))
	else
		echo "PASS: $label"
	fi
	rm -rf "$tmpdir"
}

assert_sandbox_rejects_config "live key in CLI config" \
	"${script_dir}/testdata/config-live.toml"
assert_sandbox_rejects_config "unknown prefix in CLI config" \
	"${script_dir}/testdata/config-unknown-prefix.toml"

if [ "$failures" -eq 0 ]; then
	exit 0
fi
exit 1
