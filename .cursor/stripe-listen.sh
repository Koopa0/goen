#!/usr/bin/env bash
# Local Stripe webhook forwarding for the goen Cloud Agent environment.
#
# Runs as a terminal. When a Stripe TEST-mode key is available it forwards
# Stripe's test webhook events to the running storefront's /webhooks/stripe
# endpoint via the Stripe CLI; otherwise it idles with a hint so the terminal
# stays available without doing anything unsafe.
#
# To enable, add these as environment secrets (never commit them):
#   GOEN_STRIPE_API_KEY          a TEST-mode key: sk_test_... or rk_test_...
#   GOEN_STRIPE_WEBHOOK_SECRET   from `stripe listen --print-secret` (stable per
#                                account); the storefront verifies signatures
#                                against it, and the CLI signs with the same one.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

lib_dir="$(dirname "${BASH_SOURCE[0]}")/lib"

# Prefer an injected secret; fall back to .env for a locally-set key.
if [ -z "${GOEN_STRIPE_API_KEY:-}" ] && [ -f .env ]; then
	set -a
	# shellcheck disable=SC1091
	. ./.env
	set +a
fi

forward_url="http://127.0.0.1:9700/webhooks/stripe"

if [ -z "${GOEN_STRIPE_API_KEY:-}" ]; then
	echo "stripe-listen: idle — no GOEN_STRIPE_API_KEY set."
	echo "stripe-listen: run 'bash .cursor/stripe-sandbox.sh' to turn on a local test sandbox,"
	echo "stripe-listen: or add your own Stripe TEST-mode key + GOEN_STRIPE_WEBHOOK_SECRET as secrets."
	# Stay alive but detectable (no exec) so start.sh does not relaunch a copy.
	while true; do sleep 86400; done
fi

if bash "${lib_dir}/stripe-test-key.sh" "${GOEN_STRIPE_API_KEY}"; then
	echo "stripe-listen: forwarding Stripe TEST events to ${forward_url}"
	exec stripe listen --api-key "${GOEN_STRIPE_API_KEY}" --forward-to "${forward_url}"
fi

echo "stripe-listen: refusing to run — GOEN_STRIPE_API_KEY is not a TEST-mode key (sk_test_/rk_test_/rkcs_test_)." >&2
echo "stripe-listen: a development environment must never forward against a live Stripe account." >&2
while true; do sleep 86400; done

