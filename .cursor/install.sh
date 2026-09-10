#!/usr/bin/env bash
# Repository bootstrap for the goen Cloud Agent environment.
#
# Runs after the source tree is checked out. It must be idempotent: the
# base image already carries Go 1.27, Docker, psql, golangci-lint, squawk and
# Chrome, so this script only refreshes repository-derived state. Per-boot
# services (the Docker daemon and the database) live in start.sh instead.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The Makefile reads .env when present; .env.example is the committed default.
if [ ! -f .env ]; then
	cp .env.example .env
fi

# Module cache and the generated + compiled binary. `make build` runs
# `templ generate` first, so the committed *_templ.go projection is refreshed
# from source rather than assumed.
go mod download
make build

echo "install: goen bootstrap complete"
