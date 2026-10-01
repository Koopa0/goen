#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "${script_dir}/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
failures=0

# .env.example with every commented example switched on, as an operator who
# follows its instructions would have it.
sed -E 's/^# (GOEN_[A-Z0-9_]+=)/\1/' "${repo}/.env.example" >"${tmp}/all.env"

expect() {
	local label="$1" name="$2" want="$3" file="$4" got
	got="$(bash -c '. "$1"; load_env "$2"; printf %s "${!3-}"' _ "${script_dir}/load-env.sh" "$file" "$name")"
	if [ "$got" = "$want" ]; then
		echo "PASS: $label"
	else
		echo "FAIL: $label (expected '$want', got '$got')"
		failures=$((failures + 1))
	fi
}

expect "the documented sender, uncommented" GOEN_SMTP_FROM 'goen <no-reply@goen.example>' "${tmp}/all.env"
expect "a value with an equals sign" GOEN_DATABASE_URL \
	'postgres://goen:goen@127.0.0.1:5433/goen?sslmode=disable' "${tmp}/all.env"

printf 'A=one two\nB=a$$b # not part of it\n# C=hidden\n  D = spaced  \n' >"${tmp}/mk.env"
expect "a value with a space" A 'one two' "${tmp}/mk.env"
expect "make's \$\$ and its comment" B 'a$b' "${tmp}/mk.env"
expect "a commented assignment" C '' "${tmp}/mk.env"
expect "blanks around name and value" D 'spaced' "${tmp}/mk.env"

if [ "$failures" -eq 0 ]; then
	exit 0
fi
exit 1
