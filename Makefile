GOLANGCI_LINT_VERSION := 2.12.2
SQLC_VERSION := v1.31.1
KO_VERSION := v0.19.1
MIGRATE_VERSION := v4.19.0
GOVULNCHECK_VERSION := v1.6.0
SQUAWK_VERSION := 2.60.0

# Tools that generate or inspect this module but are not part of it. `go run
# pkg@version` pins each as firmly as a require line without joining the module
# graph — see CLAUDE.md, "Build tools stay out of go.mod".
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
MIGRATE := go run -tags='postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

# Local development reads .env when it exists; deployment sets the environment
# itself. Nothing here invents a default database URL: a missing one must stop
# the binary rather than silently reach some other database.
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: build run test test-race test-integration integration-build-check \
        image image-push lint fmt fmt-check vet gen templ-check vuln \
        sqlc sqlc-check squawk db-up db-down migrate-up migrate-down \
        verify verify-all clean

build: gen
	go build -o bin/goen ./cmd/goen

run: gen
	go run ./cmd/goen

test: gen
	go test -count=1 -shuffle=on ./...

test-race: gen
	go test -race -count=1 -shuffle=on ./...

# Requires Docker: these start a real PostgreSQL 18 through testcontainers.
# The database is never mocked — most of goen's data rules live in CHECK
# constraints and partial unique indexes, and a fake has none of them.
test-integration: gen
	go test -tags=integration -count=1 ./...

# Type-check the integration tests on every ordinary run without executing
# them, so a build-tagged test cannot rot silently between the runs that need
# Docker.
#
# `go test -run='^$$'` looks like it would do this and does not: it still runs
# TestMain, which starts a container. go vet type-checks test files without
# running anything.
integration-build-check:
	go vet -tags=integration ./...

# -path must match templ-check's, or each run of one invalidates the other:
# generating from the repository root writes a different projection than
# generating from internal/ui, so verify passed and then failed on its own
# output.
gen:
	go tool templ generate -path internal/ui

# The committed *_templ.go must match the .templ sources. Nothing else checks
# it: `gen` runs before every other target and would quietly repair a stale
# projection, so a drifted generated file would only ever be noticed as a
# surprising diff.
templ-check:
	go tool templ generate -check -path internal/ui

# Vulnerability scan of the module graph and the code that actually reaches it.
# govulncheck reports only vulnerabilities on a call path from this binary,
# which is why it is worth running rather than reading a dependency list.
vuln:
	$(GOVULNCHECK) ./...

fmt:
	go tool templ fmt internal/ui
	golangci-lint fmt ./...

# Read-only: templ's own -fail mode rewrites the tree before reporting, which
# makes it useless as a gate. Compare each projection through a temp file.
fmt-check:
	@set -eu; \
	tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/goen-templ-fmt.XXXXXX"); \
	trap 'rm -rf "$$tmp"' 0 HUP INT TERM; \
	find internal/ui -type f -name '*.templ' -print | LC_ALL=C sort > "$$tmp/files"; \
	[ -s "$$tmp/files" ] || { echo 'templ source list is empty' >&2; exit 1; }; \
	while IFS= read -r file; do \
		go tool templ fmt -stdout "$$file" > "$$tmp/formatted"; \
		if ! cmp -s "$$file" "$$tmp/formatted"; then diff -u "$$file" "$$tmp/formatted"; exit 1; fi; \
	done < "$$tmp/files"
	golangci-lint fmt --diff ./...

vet: gen
	go vet ./...

lint: gen
	@version=$$(golangci-lint version); case "$$version" in *"version $(GOLANGCI_LINT_VERSION) "*) ;; *) echo 'golangci-lint $(GOLANGCI_LINT_VERSION) is required' >&2; exit 1;; esac
	golangci-lint run ./...

sqlc:
	$(SQLC) generate

# Read-only: keep the committed projection aside, regenerate, compare, and put
# the committed one back whatever the result.
#
# The previous version claimed to do this and did the opposite — it regenerated
# over internal/db and then diffed, so by the time it reported a stale tree it
# had already repaired it, and a second run passed. A gate that fixes what it
# is checking cannot fail twice.
sqlc-check:
	@set -eu; \
	tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/goen-sqlc.XXXXXX"); \
	trap 'rm -rf "$$tmp"; if [ -d "$$tmp.committed" ]; then rm -rf internal/db; mv "$$tmp.committed" internal/db; fi' \
		0 HUP INT TERM; \
	cp -R internal/db "$$tmp.committed"; \
	$(SQLC) generate; \
	if ! diff -ru "$$tmp.committed" internal/db >"$$tmp/diff"; then \
		echo 'internal/db does not match the schema and queries; run make sqlc' >&2; \
		head -40 "$$tmp/diff" >&2; \
		exit 1; \
	fi

# Container image. ko builds straight from the module — there is no Dockerfile
# because there is nothing to copy in: the binary embeds its own assets.
# ko.local keeps the image on this machine; set KO_DOCKER_REPO to push.
KO_DOCKER_REPO ?= ko.local
# A local build loads into the Docker daemon, which holds one platform at a
# time; a pushed build carries both. Override for an arm64 host that wants to
# run the image without emulation.
KO_PLATFORM ?= linux/amd64
# Without a tag every build lands on :latest and overwrites the one before it,
# so a rollback has nothing to roll back to. Falls back to a timestamp outside
# a git checkout.
IMAGE_TAG ?= $(shell git rev-parse --short HEAD 2>/dev/null || date -u +%Y%m%d%H%M%S)

# `go run pkg@version` resolves and caches ko without touching this module.
# A `tool` directive would have been tidier to invoke, but ko carries the AWS,
# Azure, GCP and Kubernetes clients it needs for registry auth, and putting it
# in go.mod took the module graph from 104 modules to 528 — none of which reach
# the binary, all of which would sit inside every vulnerability scan of this
# repository forever.
image: gen
	KO_DOCKER_REPO=$(KO_DOCKER_REPO) go run github.com/google/ko@$(KO_VERSION) \
		build --bare --platform=$(KO_PLATFORM) --tags=$(IMAGE_TAG) ./cmd/goen

# --sbom=spdx attaches the bill of materials to the pushed image. It has nowhere
# to go on a local daemon load, which is why it appears only here.
image-push: gen
	@test "$(KO_DOCKER_REPO)" != "ko.local" || { echo 'set KO_DOCKER_REPO to a real registry' >&2; exit 2; }
	KO_DOCKER_REPO=$(KO_DOCKER_REPO) go run github.com/google/ko@$(KO_VERSION) \
		build --bare --sbom=spdx --platform=linux/amd64,linux/arm64 \
		--tags=$(IMAGE_TAG),latest --push ./cmd/goen

# Squawk reads .squawk.toml. Every rule is on; a statement that genuinely does
# not apply carries an inline `-- squawk-ignore` with its reason written above.
squawk:
	@command -v squawk >/dev/null || { echo 'squawk is required: npm i -g squawk-cli@$(SQUAWK_VERSION)' >&2; exit 1; }
	@version=$$(squawk --version); case "$$version" in *"$(SQUAWK_VERSION)"*) ;; *) echo "squawk $(SQUAWK_VERSION) is required, found $$version" >&2; exit 1;; esac
	squawk migrations/*.sql

db-up:
	docker compose up -d db
	@until docker compose exec -T db pg_isready -U goen -d goen >/dev/null 2>&1; do sleep 1; done
	@echo 'database ready on 127.0.0.1:5433'

db-down:
	docker compose down

migrate-up:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	$(MIGRATE) -path migrations -database "$$GOEN_DATABASE_URL" up

migrate-down:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	$(MIGRATE) -path migrations -database "$$GOEN_DATABASE_URL" down 1

# The single gate. Stop at the first failure — a passing later stage must never
# be able to bury an earlier red one.
verify: fmt-check templ-check squawk sqlc-check vet lint integration-build-check test-race
	@echo 'verify: PASS (unit tests only — make verify-all adds the database suite)'

# Everything verify runs plus the parts that need Docker and the network.
verify-all: verify test-integration vuln
	@echo 'verify-all: PASS' 

clean:
	rm -rf bin
