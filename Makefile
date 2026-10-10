GOLANGCI_LINT_VERSION := 2.13.2
SQLC_VERSION := v1.31.1
KO_VERSION := v0.19.1
MIGRATE_VERSION := v4.19.1
GOVULNCHECK_VERSION := v1.7.0
SQUAWK_VERSION := 2.64.0
DEADCODE_VERSION := v0.50.0
ACTIONLINT_VERSION := v1.7.12
AXE_CORE_VERSION := 4.13.0

# The digest of axe.min.js at that version, as the npm registry tarball and
# unpkg both serve it. check-layout downloads the file and verifies this before
# evaluating it in the page, because the alternative — a <script src> at a CDN —
# lets a third party decide what runs inside the gate that decides what merges.
AXE_CORE_SHA256 := c24f097bd2f451d4f933e8bc7d8d539f8672a2ebcb5cc9f9f3eec8ca9470a0c1

# Tools that generate or inspect this module but are not part of it. `go run
# pkg@version` pins each as firmly as a require line without joining the module
# graph, so build tools stay out of go.mod.
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
MIGRATE := go run -tags='postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
DEADCODE := go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)
ACTIONLINT := go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

# Local development reads .env when it exists; deployment sets the environment
# itself. Nothing here invents a default database URL: a missing one must stop
# the binary rather than silently reach some other database.
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: build run test test-race test-integration production-build-check integration-build-check \
        image image-push lint fmt fmt-check vet deadcode gen templ-check vuln \
        sqlc sqlc-check squawk db-up db-down migrate-up migrate-down db-seed \
        db-repair-invoice-faq db-repair-refund-faq db-repair-payment-faq db-repair-shop-rules-faq \
        db-repair-hold-faq \
        demo-restore-check workflow-check verify verify-all check-layout check-layout-run db-reset clean

build: gen
	go build -o bin/goen ./cmd/goen

# GOEN_INSECURE_COOKIES is development only: the cart cookie carries Secure and
# the __Host- prefix by default, and a browser never sends such a cookie back
# over the plain http:// this serves on — the cart would appear to lose itself
# on every request. The default is secure, so forgetting this in a deployment
# fails safe.
# schema-drift is the existing catalogue comparison against migrations/; a dev
# database built before an amended 001 fails here instead of as a 500 later.
run: gen
	@$(MAKE) --no-print-directory schema-drift; status=$$?; \
		case $$status in \
		0) ;; \
		1) echo 'run: the development database does not match migrations/; back up anything you need, then run make db-reset' >&2; exit 1;; \
		*) echo "run: could not compare the development database with migrations/ (schema-drift exited $$status; its message is above). If the database is not running: make db-up" >&2; exit $$status;; \
		esac
	GOEN_INSECURE_COOKIES=1 go run ./cmd/goen

test: gen
	go test -count=1 -shuffle=on ./...

test-race: gen
	go test -race -count=1 -shuffle=on ./...

# Build the deployable program without test files. `go test` is not equivalent:
# files in package main's test compilation can accidentally provide a symbol
# that the production-only package does not have. Cross-building with cgo off
# also matches the static Linux container used in deployment.
production-build-check: gen
	@set -eu; \
	tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/goen-build.XXXXXX"); \
	trap 'rm -rf "$$tmp"' 0 HUP INT TERM; \
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$$tmp/goen" ./cmd/goen

# Requires Docker: these start a real PostgreSQL 18 through testcontainers.
# The database is never mocked — most of goen's data rules live in CHECK
# constraints and partial unique indexes, and a fake has none of them.
test-integration: gen
	@# -shuffle=on, because the integration suites share a database and a test
	@# that consumes stock another one assumes is there passes in file order and
	@# nothing else. That was true of internal/cart until the fixtures gave each
	@# test its own variant, and this flag is what stops it coming back.
	@# -race as well, because almost everything this suite covers is a
	@# concurrency guard: the deadlock in redeem_loyalty_points, the TOTP replay
	@# window, the lease on an outbox claim, two checkouts racing the last unit.
	@# Measured at 20s wall clock for the whole suite, which is not a reason to
	@# leave the detector off.
	go test -tags=integration -race -count=1 -shuffle=on ./...

# Layout conformance, measured in a real browser. Outside `verify` for the same
# reason test-integration is: it needs something the gate cannot assume — here a
# Chrome binary and a running server, rather than Docker.
#
# It drives Emulation.setDeviceMetricsOverride and reads boxes. A screenshot
# cannot do this job: Chrome on macOS will not open a window under ~500px, so a
# 375px capture is really a cropped 500 and the overflow it hides is the
# overflow that matters.
#
# Run `make run` in another shell first.
#
# Where the run keeps its browser profile, axe-core and fixture env; the scripts
# read it from the environment. A test points it at a temporary directory so
# nothing is written inside the repository tree.
LAYOUT_DIR ?= .layout-chrome
export LAYOUT_DIR

# LAYOUT_CHROME is a target variable so the resolved path survives GNU make's
# one-shell-per-recipe-line default. Quoted for the macOS app bundle path.
check-layout-run: LAYOUT_CHROME := $(if $(CHROME),$(CHROME),$(shell scripts/resolve-chrome.sh 2>/dev/null))
# The seed's photograph tagged with COLOUR_VALUE, which the colour probe expects
# to lead COLOUR_SLUG's gallery once that value is chosen.
check-layout-run: COLOUR_SLUG := pixelight-9-pro
check-layout-run: COLOUR_VALUE := 曜石黑
check-layout-run: COLOUR_KEY := pixelight-9-pro-02.webp
# Chrome is launched in the background several recipe lines in, and each line is
# its own shell, so no line can clean up after a later one that fails. This
# wrapper owns the cleanup: the browser and its profile are removed on every way
# out, success, a failed fixture, Ctrl-C and SIGTERM. The pid file is the only
# handle on a browser that outlives its shell.
check-layout:
	@trap 'if [ -f $(LAYOUT_DIR)/pid ]; then kill $$(cat $(LAYOUT_DIR)/pid) 2>/dev/null; fi; rm -rf $(LAYOUT_DIR)' EXIT; \
		trap 'exit 130' INT; trap 'exit 143' TERM; \
		$(MAKE) --no-print-directory check-layout-run

check-layout-run:
	@test -n "$(LAYOUT_CHROME)" && test -x "$(LAYOUT_CHROME)" || { echo 'Chrome not found; set CHROME=/path/to/chrome' >&2; exit 2; }
	@curl -sf -o /dev/null $${GOEN_URL:-http://127.0.0.1:9700/} \
		|| { echo 'no server on $${GOEN_URL:-http://127.0.0.1:9700/} — run `make run` first' >&2; exit 2; }
	@rm -rf $(LAYOUT_DIR) && mkdir -p $(LAYOUT_DIR)
	@"$(LAYOUT_CHROME)" --headless --disable-gpu --no-first-run \
		--remote-debugging-port=$${CDP_PORT:-9222} \
		--user-data-dir=$(abspath $(LAYOUT_DIR)) about:blank >chrome.log 2>&1 & echo $$! > $(LAYOUT_DIR)/pid
	@sleep 3
	@# axe-core, fetched at the pin above and checked against it. Downloaded
	@# AFTER the browser is launched so the wait for Chrome pays for the fetch,
	@# and into the throwaway profile directory so nothing survives the run.
	@# The script evaluates it over CDP rather than injecting a <script>: the
	@# site sends script-src 'self' and a gate that needs the page to relax its
	@# own CSP measures a page nobody visits.
	@curl -fsSL --retry 3 -o $(LAYOUT_DIR)/axe.min.js \
		https://unpkg.com/axe-core@$(AXE_CORE_VERSION)/axe.min.js \
		|| { echo 'could not fetch axe-core $(AXE_CORE_VERSION)' >&2; exit 2; }
	@# openssl rather than shasum or sha256sum: neither of those is on both macOS
	@# and a Linux runner. The last field, because OpenSSL 3 prints
	@# SHA2-256(file)= and LibreSSL prints SHA256(file)=.
	@digest=$$(openssl dgst -sha256 $(LAYOUT_DIR)/axe.min.js | awk '{print $$NF}'); \
		test "$$digest" = '$(AXE_CORE_SHA256)' \
		|| { echo "axe-core $(AXE_CORE_VERSION) hashes to $$digest, not AXE_CORE_SHA256" >&2; exit 2; }
	@# After the launch, which TestCheckLayoutLaunchesTheResolvedChrome observes
	@# against a database this line cannot reach.
	@psql "$$GOEN_DATABASE_URL" -X -q -v env=$(LAYOUT_DIR)/env -f scripts/check-layout.sql \
		|| { echo 'scripts/check-layout.sql was refused (psql named the statement above); no page was measured' >&2; exit 2; }
	@node --env-file=$(LAYOUT_DIR)/env scripts/reflow-check.mjs /@1024@en@text200 /@1024@text200 /@320@en@text200 /@320@text200 /@1024@en
	@node --env-file=$(LAYOUT_DIR)/env --test scripts/reflow-staff-check.test.mjs
	@node --env-file=$(LAYOUT_DIR)/env scripts/reflow-check.mjs --staff /@1024@en@text200 /@1024@text200 /@320@en@text200 /@320@text200 /@1024@en
	@REFLOW_REQUIRED_SELECTOR='.goen-line--order .ui-statline' node --env-file=$(LAYOUT_DIR)/env scripts/reflow-check.mjs '/account/orders/{RETURN_FORM_ORDER}@320@text200' '/account/orders/{RETURN_FORM_ORDER}@320@en@text200' '/account/orders/{RETURN_FORM_ORDER}@320' '/account/orders/{RETURN_FORM_ORDER}@375@text200' '/account/orders/{RETURN_FORM_ORDER}@375@en@text200'
	@node --env-file=$(LAYOUT_DIR)/env scripts/filter-feedback-check.mjs
	@COLOUR_SLUG='$(COLOUR_SLUG)' COLOUR_VALUE='$(COLOUR_VALUE)' COLOUR_KEY='$(COLOUR_KEY)' \
		node --env-file=$(LAYOUT_DIR)/env scripts/check-layout.mjs; status=$$?; \
		kill $$(cat $(LAYOUT_DIR)/pid) 2>/dev/null; sleep 1; rm -rf $(LAYOUT_DIR) 2>/dev/null; \
		exit $$status

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
	go tool templ generate -path internal/email

# The committed *_templ.go must match the .templ sources. Nothing else checks
# it: `gen` runs before every other target and would quietly repair a stale
# projection, so a drifted generated file would only ever be noticed as a
# surprising diff.
templ-check:
	go tool templ generate -check -path internal/ui
	go tool templ generate -check -path internal/email

# Vulnerability scan of the module graph and the code that actually reaches it.
# govulncheck reports only vulnerabilities on a call path from this binary,
# which is why it is worth running rather than reading a dependency list.
vuln:
	$(GOVULNCHECK) ./...

fmt:
	go tool templ fmt internal/ui internal/email
	golangci-lint fmt ./...

# Read-only: templ's own -fail mode rewrites the tree before reporting, which
# makes it useless as a gate. Compare each projection through a temp file.
fmt-check:
	@set -eu; \
	tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/goen-templ-fmt.XXXXXX"); \
	trap 'rm -rf "$$tmp"' 0 HUP INT TERM; \
	find internal/ui internal/email -type f -name '*.templ' -print | LC_ALL=C sort > "$$tmp/files"; \
	[ -s "$$tmp/files" ] || { echo 'templ source list is empty' >&2; exit 1; }; \
	while IFS= read -r file; do \
		go tool templ fmt -stdout "$$file" > "$$tmp/formatted"; \
		if ! cmp -s "$$file" "$$tmp/formatted"; then diff -u "$$file" "$$tmp/formatted"; exit 1; fi; \
	done < "$$tmp/files"
	golangci-lint fmt --diff ./...

vet: gen
	go vet ./...

# Reachability over every production package, including packages disconnected
# from cmd/goen. Tests and integration build tags are deliberately absent: a
# test-only caller is the dead-code defect this gate must expose. The exact
# x/tools version is pinned outside go.mod like the other inspection tools.
deadcode: gen
	@set -eu; \
	tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/goen-deadcode.XXXXXX"); \
	cleanup_deadcode() { \
		status=$$?; \
		trap - EXIT HUP INT TERM; \
		rm -rf "$$tmp"; \
		exit "$$status"; \
	}; \
	trap cleanup_deadcode EXIT; \
	trap 'exit 129' HUP; \
	trap 'exit 130' INT; \
	trap 'exit 143' TERM; \
	if $(DEADCODE) ./... > "$$tmp/report" 2> "$$tmp/tool.err"; then \
		:; \
	else \
		status=$$?; \
		echo "deadcode: FAIL — x/tools deadcode exited $$status" >&2; \
		cat "$$tmp/tool.err" >&2; \
		cat "$$tmp/report" >&2; \
		exit "$$status"; \
	fi; \
	if test -s "$$tmp/tool.err"; then cat "$$tmp/tool.err" >&2; fi; \
	sh scripts/deadcode-check.sh "$$tmp/report" .deadcode-allow

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

# --wait uses the compose healthcheck and fails when the container exits or
# never turns healthy, where an unbounded pg_isready loop hung forever on a
# database that had died, and CI's schema and layout jobs hung with it.
db-up:
	@docker compose up -d --wait db || { echo 'the database did not become healthy:' >&2; docker compose logs --tail=20 db >&2; exit 1; }
	@echo 'database ready on 127.0.0.1:5433'

db-down:
	docker compose down

migrate-up:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	$(MIGRATE) -path migrations -database "$$GOEN_DATABASE_URL" up

migrate-down:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	$(MIGRATE) -path migrations -database "$$GOEN_DATABASE_URL" down 1

# Load the development catalogue: brands, categories, 40 products with variants,
# images, specs and reviews. Runs as the owner (psql, not the app's store
# role), so it may write the tables store is barred from. Development only.
# seed/dev_catalog.sql is edited by hand.
db-seed:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	psql "$$GOEN_DATABASE_URL" -v ON_ERROR_STOP=1 -f seed/dev_catalog.sql

# Rewrite the published invoice FAQ on a database that already has one.
# db-seed cannot: the catalogue INSERT stops on the first kept brand.
# This file updates that one question and nothing else.
db-repair-invoice-faq:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	psql "$$GOEN_DATABASE_URL" -v ON_ERROR_STOP=1 -f seed/repair_invoice_faq.sql

# Rewrite the published refund FAQ on a database that already has one.
# db-seed cannot: the catalogue INSERT stops on the first kept brand.
# Each locale is matched on its own Stripe-only sentence so a shop-edited
# answer stays.
db-repair-refund-faq:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	psql "$$GOEN_DATABASE_URL" -v ON_ERROR_STOP=1 -f seed/repair_refund_faq.sql

# Update only the known card-only payment FAQ on a kept database.
db-repair-payment-faq:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	psql "$$GOEN_DATABASE_URL" -v ON_ERROR_STOP=1 -f seed/repair_payment_faq.sql

# Update only the known discount, membership and company-invoice FAQ answers on
# a kept database. Each locale is matched on its previous text so a shop-edited
# answer stays.
db-repair-shop-rules-faq:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	psql "$$GOEN_DATABASE_URL" -v ON_ERROR_STOP=1 -f seed/repair_shop_rules_faq.sql

# Update only the known stock-hold FAQ, which promised a lapsed order could be
# paid again, on a kept database.
db-repair-hold-faq:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	psql "$$GOEN_DATABASE_URL" -v ON_ERROR_STOP=1 -f seed/repair_hold_faq.sql

# Rebuild the development database from scratch.
#
# 001 is still amended in place rather than superseded (see CONTRIBUTING.md), so an
# edit to it does NOT reach a database already at version 1 — `migrate up`
# reports "no change" and the schema silently stays old. This is the documented
# way to pick the edit up, and it exists as a target because the alternative is
# a four-command incantation people get wrong.
#
# Development only, and it says so: it drops the database.
# What a database ACTUALLY has, as text a diff can read.
#
# Constraints, indexes and columns, sorted and normalised. Not pg_dump: its
# output carries ordering and formatting that differ between servers and say
# nothing about whether the rules agree.
#
# pg_get_constraintdef's pretty form is the round-trip-stable representation
# inside one PostgreSQL major version: meaning-bearing parentheses survive
# (`(a OR b) AND c` keeps them), a real 8000 -> 9999 change still differs, and
# it adds no newline noise (1 of 762 public constraints has a newline under
# both representations). Pretty output is display-oriented and is NOT promised
# stable across major versions. That is safe here only because schema-drift and
# restore-drill create their comparison databases in the same cluster as the
# database they inspect, so both sides are deparsed by the same server binary.
# This catalog must not be used to compare databases across PostgreSQL versions.
CATALOG_SQL := \
  "SELECT 'constraint\t'||c.conrelid::regclass||'\t'||c.conname||'\t'||pg_get_constraintdef(c.oid, true) \
     FROM pg_constraint c WHERE c.connamespace='public'::regnamespace \
   UNION ALL \
   SELECT 'index\t'||tablename||'\t'||indexname||'\t'||indexdef \
     FROM pg_indexes WHERE schemaname='public' \
   UNION ALL \
   SELECT 'column\t'||table_name||'\t'||column_name||'\t'||data_type||' '||is_nullable||' '||coalesce(column_default,'-') \
     FROM information_schema.columns WHERE table_schema='public' \
   UNION ALL \
   SELECT 'trigger\t'||event_object_table||'\t'||trigger_name||'\t'||action_statement \
     FROM information_schema.triggers WHERE trigger_schema='public' \
   ORDER BY 1"

# Exact per-table row counts, as text a diff can read.
#
# NOT n_live_tup: that is an ESTIMATE derived from reltuples, and a backup
# drill may not answer "did every row come back" with an estimate. It also
# needed an ANALYZE, which made the instrument write to the database it audits.
EXACT_COUNTS_SQL := \
  "SELECT c.relname||' '||(xpath('/row/c/text()', \
        query_to_xml(format('select count(*) as c from public.%I', c.relname), false, true, '')))[1]::text::bigint \
     FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace \
    WHERE n.nspname='public' AND c.relkind='r' \
    ORDER BY 1"

# Database-object privileges carried by pg_dump, normalised so an owner's
# implicit ACL and an explicit one compare equal. Schema, table/view, column
# and function ACLs live in four different catalogs, so all four are part of
# the question. Cluster-global roles and database-level GRANTs are not: those
# need a separate pg_dumpall --globals-only drill.
ACL_SQL := \
  "SELECT 'table'||chr(9)||c.relname||chr(9)||a::text \
     FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace, \
          LATERAL unnest(coalesce(c.relacl, acldefault('r', c.relowner))) a \
    WHERE n.nspname='public' AND c.relkind IN ('r','v') \
      AND split_part(a::text,'=',1) <> pg_get_userbyid(c.relowner) \
   UNION ALL \
   SELECT 'function'||chr(9)||p.proname||chr(9)||a::text \
     FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace, \
          LATERAL unnest(coalesce(p.proacl, acldefault('f', p.proowner))) a \
    WHERE n.nspname='public' \
      AND split_part(a::text,'=',1) <> pg_get_userbyid(p.proowner) \
   UNION ALL \
   SELECT 'column'||chr(9)||c.relname||'.'||attr.attname||chr(9)||a::text \
     FROM pg_attribute attr \
     JOIN pg_class c ON c.oid = attr.attrelid \
     JOIN pg_namespace n ON n.oid = c.relnamespace, \
          LATERAL unnest(attr.attacl) a \
    WHERE n.nspname='public' AND attr.attacl IS NOT NULL \
      AND split_part(a::text,'=',1) <> pg_get_userbyid(c.relowner) \
   UNION ALL \
   SELECT 'schema'||chr(9)||n.nspname||chr(9)||a::text \
     FROM pg_namespace n, LATERAL unnest(n.nspacl) a \
    WHERE n.nspname='public' AND n.nspacl IS NOT NULL \
      AND split_part(a::text,'=',1) <> pg_get_userbyid(n.nspowner) \
   ORDER BY 1"

# Does the deployed schema still match what migrations/ declares?
#
# PostgreSQL does not re-validate a CHECK that is amended in place, and this
# repository amends 001 rather than superseding it — a decision that holds only
# while no environment is undisposable. Nothing compared the two, so the first
# database that is not thrown away would disagree with the file silently: two
# correct halves, at the schema level, where none of this project's guards look.
#
# It builds a REFERENCE database from migrations/ beside the live one and diffs
# the catalogs. A difference is not automatically a defect — it is the signal
# that 001 has stopped being the whole truth, and therefore that 002 begins.
#
# Needs the dev container and a GOEN_DATABASE_URL pointing at the database to
# check. Exit 1 is DRIFT and nothing else: every other failure, a stopped
# container or a missing tool included, exits 2 or more, so `run` can tell a stale
# schema from an environment that was never ready. Outside `verify` for the reason test-integration is: it needs something
# the gate cannot assume.
.PHONY: schema-drift
schema-drift:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required: the database to CHECK' >&2; exit 2; }
	@set -eu; \
	ref=goen_schema_ref_$$$$; \
	tmp=$$(mktemp -d); drift=; \
	trap 'rc=$$?; docker compose exec -T db dropdb -U goen --if-exists --force "$$ref" >/dev/null 2>&1 || true; rm -rf "$$tmp"; if [ "$$rc" = 1 ] && [ -z "$$drift" ]; then exit 4; fi' 0 HUP INT TERM; \
	docker compose exec -T db true >/dev/null 2>&1 \
		|| { echo 'schema-drift: the compose db service is not running; start it with make db-up' >&2; exit 4; }; \
	docker compose exec -T db createdb -U goen "$$ref"; \
	base=$${GOEN_DATABASE_URL%%\?*}; \
	case "$$GOEN_DATABASE_URL" in *\?*) query="?$${GOEN_DATABASE_URL#*\?}";; *) query="";; esac; \
	refurl="$${base%/*}/$$ref$$query"; \
	$(MIGRATE) -path migrations -database "$$refurl" up >/dev/null; \
	:; \
	psql "$$refurl" -At -c $(CATALOG_SQL) | sort > "$$tmp"/ref.txt; \
	test -s "$$tmp"/ref.txt || { echo 'schema-drift: the reference database is EMPTY; it was not built' >&2; exit 3; }; \
	psql "$$refurl" -At -c "SELECT current_database()" | grep -qx "$$ref" \
		|| { echo 'schema-drift: the reference URL does not point at the reference database, so this would compare the live one with itself' >&2; exit 3; }; \
	psql "$$GOEN_DATABASE_URL" -At -c $(CATALOG_SQL) | sort > "$$tmp"/live.txt; \
	if diff -u "$$tmp"/ref.txt "$$tmp"/live.txt > "$$tmp"/drift.txt; then \
		echo 'schema-drift: PASS — the deployed schema matches migrations/'; \
	else \
		[ "$$?" = 1 ] || { echo 'schema-drift: diff itself failed' >&2; exit 4; }; \
		echo 'schema-drift: FAIL — the deployed schema and migrations/ disagree.'; \
		echo '  -  is what migrations/ declares; +  is what the database has.'; \
		echo '  An amended CHECK is not re-validated by PostgreSQL, so this is'; \
		echo '  where amend-in-place stops being safe and 002 begins.'; \
		cat "$$tmp"/drift.txt; \
		drift=1; exit 1; \
	fi

# Prove the backup can be restored. Not that one exists — that a dump of this
# database comes back as this database.
#
# goen's images live IN PostgreSQL (the recorded internal/media decision), so
# the database is the only copy of the catalogue's photography as well as its
# data. A backup nobody has restored is a belief.
#
# It dumps, restores into a throwaway, and asks THREE questions of the copy
# against the database it was dumped from: does the CATALOG agree (the same
# CATALOG_SQL schema-drift reads — constraints, indexes, columns, triggers), do
# the database-object GRANTs carried by pg_dump agree, and did every table come
# back with the same number of rows. Cluster-global roles and database-level
# GRANTs need pg_dumpall --globals-only and are not this drill's subject.
# Whether the live schema matches migrations/ is schema-drift's question; the
# two compose.
#
# Rows alone passes on a restore that lost an index, a CHECK or a GRANT. Schema
# alone passes on a dump that lost every row — the mutation is `pg_dump -s`.
#
# The counts are EXACT and are read inside the DUMP'S OWN exported snapshot,
# so a write landing during the drill cannot make it red. A drill that goes red
# on ordinary traffic is a drill people re-run until it is green.
.PHONY: restore-drill
restore-drill:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required: the database to back up' >&2; exit 2; }
	@set -eu; \
	copy=goen_restore_drill_$$$$; \
	work=$$(mktemp -d); \
	snapper=; \
	cleanup_restore_drill() { \
		status=$$?; \
		trap - EXIT HUP INT TERM; \
		exec 9>&- 2>/dev/null || true; \
		if test -n "$$snapper"; then \
			kill "$$snapper" 2>/dev/null || true; \
			wait "$$snapper" 2>/dev/null || true; \
		fi; \
		docker compose exec -T db dropdb -U goen --if-exists --force "$$copy" >/dev/null 2>&1 || true; \
		rm -rf "$$work"; \
		exit "$$status"; \
	}; \
	trap cleanup_restore_drill EXIT; \
	trap 'exit 129' HUP; \
	trap 'exit 130' INT; \
	trap 'exit 143' TERM; \
	mkfifo "$$work/hold"; \
	base=$${GOEN_DATABASE_URL%%\?*}; \
	case "$$GOEN_DATABASE_URL" in *\?*) query="?$${GOEN_DATABASE_URL#*\?}";; *) query="";; esac; \
	copyurl="$${base%/*}/$$copy$$query"; \
	echo 'restore-drill: dumping...'; \
	psql "$$GOEN_DATABASE_URL" -At -q -v ON_ERROR_STOP=1 -f "$$work/hold" > "$$work/held.txt" & \
	snapper=$$!; \
	exec 9>"$$work/hold"; \
	printf "SET idle_in_transaction_session_timeout='10min';\nBEGIN ISOLATION LEVEL REPEATABLE READ;\nSELECT pg_export_snapshot();\n" >&9; \
	snap=; \
	for i in $$(seq 1 100); do \
		snap=$$(head -n 1 "$$work/held.txt"); \
		test -z "$$snap" || break; \
		sleep 0.1; \
	done; \
	test -n "$$snap" || { echo 'restore-drill: could not open a snapshot on the live database' >&2; exit 3; }; \
	pg_dump "$$GOEN_DATABASE_URL" --snapshot="$$snap" -Fc -f "$$work/goen.dump"; \
	printf '%s;\nCOMMIT;\n' $(EXACT_COUNTS_SQL) >&9; \
	exec 9>&-; \
	wait "$$snapper"; \
	snapper=; \
	tail -n +2 "$$work/held.txt" > "$$work/rows-live.raw"; \
	sort "$$work/rows-live.raw" > "$$work/rows-live.txt"; \
	test -s "$$work/rows-live.txt" || { echo 'restore-drill: no live row counts were read' >&2; exit 3; }; \
	docker compose exec -T db createdb -U goen "$$copy"; \
	echo 'restore-drill: restoring into a throwaway...'; \
	pg_restore -d "$$copyurl" --no-owner --exit-on-error "$$work/goen.dump" > "$$work/restore.log" 2>&1 \
		|| { echo 'restore-drill: FAIL — pg_restore refused the dump.' >&2; cat "$$work/restore.log" >&2; exit 1; }; \
	if test -s "$$work/restore.log"; then \
		echo 'restore-drill: pg_restore said:'; \
		cat "$$work/restore.log"; \
	fi; \
	actual_copy=$$(psql "$$copyurl" -v ON_ERROR_STOP=1 -At -c "SELECT current_database()"); \
	test "$$actual_copy" = "$$copy" \
		|| { echo 'restore-drill: the copy URL does not point at the throwaway, so this would compare the live database with itself' >&2; exit 3; }; \
	fail=0; \
	psql "$$GOEN_DATABASE_URL" -v ON_ERROR_STOP=1 -At -c $(CATALOG_SQL) > "$$work/schema-live.raw"; \
	sort "$$work/schema-live.raw" > "$$work/schema-live.txt"; \
	psql "$$copyurl" -v ON_ERROR_STOP=1 -At -c $(CATALOG_SQL) > "$$work/schema-copy.raw"; \
	sort "$$work/schema-copy.raw" > "$$work/schema-copy.txt"; \
	test -s "$$work/schema-live.txt" || { echo 'restore-drill: the live catalog came back EMPTY; nothing was compared' >&2; exit 3; }; \
	if diff -u "$$work/schema-live.txt" "$$work/schema-copy.txt" > "$$work/schema.diff"; then \
		echo 'restore-drill: SCHEMA: PASS'; \
	else \
		fail=1; \
		echo 'restore-drill: FAIL — the restored SCHEMA is not the schema that was dumped.'; \
		echo '  -  is the live database; +  is what came back.'; \
		cat "$$work/schema.diff"; \
	fi; \
	psql "$$GOEN_DATABASE_URL" -v ON_ERROR_STOP=1 -At -c $(ACL_SQL) > "$$work/acl-live.raw"; \
	sort "$$work/acl-live.raw" > "$$work/acl-live.txt"; \
	psql "$$copyurl" -v ON_ERROR_STOP=1 -At -c $(ACL_SQL) > "$$work/acl-copy.raw"; \
	sort "$$work/acl-copy.raw" > "$$work/acl-copy.txt"; \
	test -s "$$work/acl-live.txt" || { echo 'restore-drill: no live dump-carried GRANTs were read; nothing was compared' >&2; exit 3; }; \
	if diff -u "$$work/acl-live.txt" "$$work/acl-copy.txt" > "$$work/acl.diff"; then \
		echo 'restore-drill: dump-carried GRANTs: PASS'; \
	else \
		fail=1; \
		echo 'restore-drill: FAIL — the restored copy does not carry the same database-object GRANTs.'; \
		echo '  A restore that changes an application role'"'"'s privileges can break or widen the site,'; \
		echo '  and no suite can see it because every suite connects as the OWNER.'; \
		cat "$$work/acl.diff"; \
	fi; \
	psql "$$copyurl" -v ON_ERROR_STOP=1 -At -c $(EXACT_COUNTS_SQL) > "$$work/rows-copy.raw"; \
	sort "$$work/rows-copy.raw" > "$$work/rows-copy.txt"; \
	if diff -u "$$work/rows-live.txt" "$$work/rows-copy.txt" > "$$work/rows.diff"; then \
		echo 'restore-drill: exact row counts: PASS'; \
	else \
		fail=1; \
		echo 'restore-drill: FAIL — the restored copy does not hold the exact row counts that were dumped.'; \
		echo '  -  is the live database at the dump'"'"'s snapshot; +  is what came back.'; \
		cat "$$work/rows.diff"; \
	fi; \
	test "$$fail" -eq 0 || exit 1; \
	echo 'restore-drill: PASS — same catalog, same dump-carried grants, same exact row counts'

db-reset:
	docker compose exec -T db dropdb -U goen --if-exists --force goen
	docker compose exec -T db createdb -U goen goen
	$(MAKE) migrate-up
	$(MAKE) db-seed
	@echo 'database rebuilt from migrations/ and seeded'

demo-restore-check:
	bash -n deploy/demo/restore-demo-db.sh scripts/demo-restore-test.sh
	scripts/demo-restore-test.sh

workflow-check:
	$(ACTIONLINT) -shellcheck=
	go test ./internal/db -run '^TestCI' -count=1
	go test ./internal/db -run '^TestCommitAttribution' -count=1

.PHONY: test-filter-feedback
test-filter-feedback:
	node --test scripts/filter-feedback.test.mjs scripts/filter-feedback-check.test.mjs

.PHONY: test-navigation-pending
test-navigation-pending:
	node --test scripts/navigation-pending.test.mjs

.PHONY: test-screenshots
test-screenshots:
	node --test scripts/screenshot-route.test.mjs scripts/screenshot-entry.test.mjs scripts/screenshot-reflow.test.mjs scripts/screenshot-delivery.test.mjs

# The single gate. Stop at the first failure — a passing later stage must never
# be able to bury an earlier red one.
verify: demo-restore-check workflow-check fmt-check templ-check squawk sqlc-check vet deadcode lint production-build-check integration-build-check test-race test-filter-feedback test-navigation-pending test-screenshots
	@echo 'verify: PASS (unit tests only — make verify-all adds the database suite)'

# Everything verify runs plus the parts that need Docker and the network.
verify-all: verify test-integration vuln
	@echo 'verify-all: PASS' 

clean:
	rm -rf bin
