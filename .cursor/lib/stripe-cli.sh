#!/usr/bin/env bash
# Invoke the Stripe CLI without an inherited STRIPE_API_KEY shadowing --api-key.
# Stripe CLI v1.50.x still prefers STRIPE_API_KEY over --api-key (stripe/stripe-cli#1581).
set -euo pipefail
exec env -u STRIPE_API_KEY stripe "$@"
