#!/usr/bin/env bash
# Exit 0 when $1 is a Stripe TEST-mode API key; exit 1 otherwise.
set -euo pipefail
case "${1:-}" in
sk_test_* | rk_test_* | rkcs_test_*) exit 0 ;;
*) exit 1 ;;
esac
