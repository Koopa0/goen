#!/usr/bin/env bash
# Sourced, not run. load_env FILE exports the assignments in a .env file.
#
# .env is MAKE syntax: the Makefile reads it with `include .env`. Sourcing it as
# bash breaks on a value make accepts, such as GOEN_SMTP_FROM=goen <no-reply@goen.example>,
# which is a bash syntax error. This reads it the way make does instead: NAME=value
# with the value taken literally (no quote handling), a `#` starting a comment,
# `$$` meaning a single `$`, and surrounding blanks dropped.
load_env() {
	local file="$1" line name value
	while IFS= read -r line || [ -n "$line" ]; do
		case "$line" in
		'' | '#'*) continue ;;
		esac
		name="${line%%=*}"
		value="${line#*=}"
		name="${name//[[:space:]]/}"
		[[ "$name" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
		value="${value%%#*}"
		value="${value//\$\$/\$}"
		value="${value#"${value%%[![:space:]]*}"}"
		value="${value%"${value##*[![:space:]]}"}"
		export "$name=$value"
	done <"$file"
}
