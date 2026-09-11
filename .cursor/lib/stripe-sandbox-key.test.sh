#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
test_key="${script_dir}/stripe-test-key.sh"
config_key="${script_dir}/stripe-config-key.sh"
sandbox="${repo_root}/.cursor/stripe-sandbox.sh"
listen="${repo_root}/.cursor/stripe-listen.sh"
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

assert_stripe_env_isolated() {
	local label="$1"
	local log_file="$2"
	local expected_api_key="$3"
	local saw_listen_argv=0
	if [ ! -f "$log_file" ]; then
		echo "FAIL: $label (stripe mock was not invoked)"
		failures=$((failures + 1))
		return
	fi
	local entry stripe_api_key argv
	while IFS= read -r entry || [ -n "$entry" ]; do
		case "$entry" in
		stripe_api_key=*)
			stripe_api_key="${entry#stripe_api_key=}"
			if [ -n "$stripe_api_key" ]; then
				echo "FAIL: $label (mock saw inherited STRIPE_API_KEY=$stripe_api_key)"
				failures=$((failures + 1))
				return
			fi
			;;
		argv=*)
			argv="${entry#argv=}"
			case "$argv" in
			listen\ --api-key\ *)
				if [ "${argv#*--api-key "$expected_api_key"}" = "$argv" ]; then
					echo "FAIL: $label (listen argv missing --api-key $expected_api_key: $argv)"
					failures=$((failures + 1))
					return
				fi
				saw_listen_argv=1
				;;
			esac
			;;
		esac
	done <"$log_file"
	if [ "$saw_listen_argv" -eq 0 ]; then
		echo "FAIL: $label (stripe listen --api-key was not recorded)"
		failures=$((failures + 1))
		return
	fi
	echo "PASS: $label"
}

assert_sandbox_unsets_inherited_stripe_api_key() {
	local tmpdir log_file
	tmpdir="$(mktemp -d)"
	log_file="${tmpdir}/stripe-log"
	mkdir -p "${tmpdir}/.config/stripe" "${tmpdir}/bin"
	cp "${script_dir}/testdata/config-single-quoted.toml" "${tmpdir}/.config/stripe/config.toml"
	cat >"${tmpdir}/bin/stripe" <<EOF
#!/usr/bin/env bash
{
	echo "stripe_api_key=\${STRIPE_API_KEY:-}"
	echo "argv=\$*"
} >>"${log_file}"
case "\$1" in
listen) exit 1 ;;
sandbox) exit 99 ;;
*) exit 99 ;;
esac
EOF
	chmod +x "${tmpdir}/bin/stripe"
	if HOME="${tmpdir}" PATH="${tmpdir}/bin:${PATH}" STRIPE_API_KEY=sk_live_fixture_must_not_reach_cli \
		bash "$sandbox" >/dev/null 2>&1; then
		echo "FAIL: sandbox with inherited STRIPE_API_KEY (expected non-zero exit)"
		failures=$((failures + 1))
		rm -rf "$tmpdir"
		return
	fi
	assert_stripe_env_isolated \
		"sandbox strips inherited STRIPE_API_KEY from every Stripe CLI call" \
		"$log_file" "rkcs_test_ABC123singlequoted"
	rm -rf "$tmpdir"
}

assert_listen_unsets_inherited_stripe_api_key() {
	local tmpdir log_file
	tmpdir="$(mktemp -d)"
	log_file="${tmpdir}/stripe-log"
	mkdir -p "${tmpdir}/bin"
	cat >"${tmpdir}/bin/stripe" <<EOF
#!/usr/bin/env bash
{
	echo "stripe_api_key=\${STRIPE_API_KEY:-}"
	echo "argv=\$*"
} >"${log_file}"
exit 0
EOF
	chmod +x "${tmpdir}/bin/stripe"
	GOEN_STRIPE_API_KEY=sk_test_fixture_must_be_used \
		STRIPE_API_KEY=sk_live_fixture_must_not_reach_cli \
		PATH="${tmpdir}/bin:${PATH}" \
		bash "$listen" >/dev/null 2>&1 || true
	assert_stripe_env_isolated \
		"stripe-listen strips inherited STRIPE_API_KEY before forwarding" \
		"$log_file" "sk_test_fixture_must_be_used"
	rm -rf "$tmpdir"
}

assert_sandbox_unsets_inherited_stripe_api_key
assert_listen_unsets_inherited_stripe_api_key

if [ "$failures" -eq 0 ]; then
	exit 0
fi
exit 1
