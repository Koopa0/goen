#!/usr/bin/env bash
# Per-boot service reconciliation for the goen Cloud Agent environment.
#
# Brings up the two things every agent needs but that do not survive a fresh
# boot: the Docker daemon (nested inside the VM) and the PostgreSQL 18 database
# the storefront talks to. It is idempotent and returns once both are ready;
# the development server itself runs as a terminal, not here.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# GOEN_DATABASE_URL and friends. The Makefile also reads .env, but the psql
# probe below needs it exported into this shell.
if [ -f .env ]; then
	set -a
	# shellcheck disable=SC1091
	. ./.env
	set +a
fi

# ---------------------------------------------------------------------------
# 1. Docker daemon
#
# The VM is itself a container, so dockerd runs with the fuse-overlayfs storage
# driver and the kernel firewall left alone (see /etc/docker/daemon.json, baked
# into the base image). setsid detaches it so it outlives this script, and the
# socket is opened to the ubuntu user because the Makefile calls `docker`
# without sudo.
# ---------------------------------------------------------------------------
if ! docker info >/dev/null 2>&1; then
	echo "start: launching dockerd"
	sudo setsid dockerd >/tmp/dockerd.log 2>&1 </dev/null &
	for _ in $(seq 1 30); do
		if sudo docker info >/dev/null 2>&1; then
			break
		fi
		sleep 1
	done
fi
sudo chmod 666 /var/run/docker.sock 2>/dev/null || true

if ! docker info >/dev/null 2>&1; then
	echo "start: dockerd did not become ready" >&2
	tail -n 20 /tmp/dockerd.log >&2 || true
	exit 1
fi

# ---------------------------------------------------------------------------
# 2. Database: start PostgreSQL, apply migrations, seed once.
#
# db-up and migrate-up are safe to repeat; migrate reports "no change" on an
# already-current schema. The seed is not repeatable, so it runs only when the
# catalogue is empty — the first boot on a fresh volume.
# ---------------------------------------------------------------------------
make db-up
make migrate-up

catalogue_count="$(psql "${GOEN_DATABASE_URL}" -tAc 'SELECT count(*) FROM products' 2>/dev/null || echo 0)"
if [ "${catalogue_count:-0}" -eq 0 ]; then
	echo "start: seeding development catalogue"
	make db-seed
else
	echo "start: catalogue already present (${catalogue_count} products); skipping seed"
fi

# ---------------------------------------------------------------------------
# 3. Storefront server, detached so it outlives this script and serves on
#    127.0.0.1:9700 for the whole session. make run regenerates templ and runs
#    the binary with development cookie settings.
# ---------------------------------------------------------------------------
if curl -sf -o /dev/null http://127.0.0.1:9700/healthz 2>/dev/null; then
	echo "start: storefront already serving on 127.0.0.1:9700"
else
	echo "start: launching storefront server"
	setsid make run >/tmp/goen-server.log 2>&1 </dev/null &
	for _ in $(seq 1 60); do
		if curl -sf -o /dev/null http://127.0.0.1:9700/healthz 2>/dev/null; then
			break
		fi
		sleep 1
	done
	if curl -sf -o /dev/null http://127.0.0.1:9700/healthz 2>/dev/null; then
		echo "start: storefront ready on http://127.0.0.1:9700"
	else
		echo "start: storefront did not answer /healthz in time; see /tmp/goen-server.log" >&2
		tail -n 20 /tmp/goen-server.log >&2 || true
		exit 1
	fi
fi

# ---------------------------------------------------------------------------
# 4. Stripe webhook forwarding, detached. The script idles harmlessly unless a
#    TEST-mode GOEN_STRIPE_API_KEY is present, so it is always safe to launch.
# ---------------------------------------------------------------------------
if pgrep -f 'stripe-listen.sh' >/dev/null 2>&1 || pgrep -x stripe >/dev/null 2>&1; then
	echo "start: stripe webhook listener already running"
else
	setsid bash .cursor/stripe-listen.sh >/tmp/stripe-listen.log 2>&1 </dev/null &
	echo "start: stripe webhook listener started (idle unless a Stripe test key is set)"
fi

echo "start: goen ready — storefront http://127.0.0.1:9700, database 127.0.0.1:5433"
