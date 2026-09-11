#!/usr/bin/env bash
# One command to turn on a local Stripe test sandbox for goen development.
#
# Uses the Stripe CLI's proof-of-work `sandbox create` to provision a
# TEST-mode account without any login and without touching a live account,
# derives the webhook signing secret, writes both into .env, and restarts the
# storefront + webhook listener so /webhooks/stripe is live end to end.
#
# Re-run it any time (the sandbox expires after 7 days); it reuses a still-valid
# sandbox and only provisions a new one when the current key no longer works.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

config_key() {
	bash "$(dirname "${BASH_SOURCE[0]}")/lib/stripe-config-key.sh" "${HOME}/.config/stripe/config.toml"
}

print_secret() {
	stripe listen --api-key "$1" --print-secret 2>/dev/null
}

# Stop whatever is listening on a TCP port, using only /proc (no lsof/ss/fuser
# dependency, so this works on a bare environment).
stop_port() {
	local port_hex inode pid
	port_hex=$(printf ':%04X' "$1")
	inode=$(awk -v p="$port_hex" '$4=="0A" && $2 ~ p"$" {print $10; exit}' /proc/net/tcp 2>/dev/null || true)
	[ -n "${inode:-}" ] || return 0
	for pid in /proc/[0-9]*; do
		if ls -l "${pid}/fd" 2>/dev/null | grep -q "socket:\[${inode}\]"; then
			kill "${pid##*/}" 2>/dev/null && echo "stripe-sandbox: stopped process on port $1"
			return 0
		fi
	done
}

echo "stripe-sandbox: provisioning a local Stripe TEST sandbox..."

key="$(config_key)"
whsec=""
if [ -n "$key" ]; then
	whsec="$(print_secret "$key")"
fi

if [ -z "$key" ] || [ -z "$whsec" ]; then
	echo "stripe-sandbox: creating a new sandbox (no valid key yet)"
	stripe sandbox create --from-git --non-interactive >/dev/null
	key="$(config_key)"
	whsec="$(print_secret "$key")"
fi

if [ -z "$key" ] || [ -z "$whsec" ]; then
	echo "stripe-sandbox: could not obtain a Stripe test key / webhook secret" >&2
	exit 1
fi

echo "stripe-sandbox: key ${key:0:12}...  webhook secret ${whsec:0:12}..."

# Write the credentials into .env (replacing any previous active Stripe lines).
[ -f .env ] || cp .env.example .env
grep -v -E '^GOEN_STRIPE_(API_KEY|WEBHOOK_SECRET)=' .env >.env.tmp && mv .env.tmp .env
cat >>.env <<EOF

# --- local Stripe sandbox (test mode; written by .cursor/stripe-sandbox.sh) ---
GOEN_STRIPE_API_KEY=$key
GOEN_STRIPE_WEBHOOK_SECRET=$whsec
EOF

# Restart the storefront (to pick up the new .env) and the listener (to forward
# with the matching secret). start.sh re-launches both.
stop_port 9700
for p in $(ps -eo pid,args | grep '[s]tripe-listen.sh' | awk '{print $1}'); do
	kill "$p" 2>/dev/null || true
done
for p in $(ps -eo pid,args | grep '[s]tripe listen --api-key' | awk '{print $1}'); do
	kill "$p" 2>/dev/null || true
done
sleep 2

echo "stripe-sandbox: restarting services via start.sh"
bash .cursor/start.sh

echo "stripe-sandbox: local Stripe is ON — forwarding to http://127.0.0.1:9700/webhooks/stripe"
