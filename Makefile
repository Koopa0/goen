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
        sqlc sqlc-check squawk db-up db-down migrate-up migrate-down db-seed \
        verify verify-all check-layout db-reset clean

build: gen
	go build -o bin/goen ./cmd/goen

# GOEN_INSECURE_COOKIES is development only: the cart cookie carries Secure and
# the __Host- prefix by default, and a browser never sends such a cookie back
# over the plain http:// this serves on — the cart would appear to lose itself
# on every request. The default is secure, so forgetting this in a deployment
# fails safe.
run: gen
	GOEN_INSECURE_COOKIES=1 go run ./cmd/goen

test: gen
	go test -count=1 -shuffle=on ./...

test-race: gen
	go test -race -count=1 -shuffle=on ./...

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
CHROME ?= /Applications/Google Chrome.app/Contents/MacOS/Google Chrome
check-layout:
	@test -x "$(CHROME)" || { echo 'Chrome not found; set CHROME=/path/to/chrome' >&2; exit 2; }
	@curl -sf -o /dev/null $${GOEN_URL:-http://127.0.0.1:9700/} \
		|| { echo 'no server on $${GOEN_URL:-http://127.0.0.1:9700/} — run `make run` first' >&2; exit 2; }
	@rm -rf .layout-chrome && mkdir -p .layout-chrome
	@"$(CHROME)" --headless --disable-gpu --no-first-run \
		--remote-debugging-port=$${CDP_PORT:-9222} \
		--user-data-dir=$(PWD)/.layout-chrome about:blank >/dev/null 2>&1 & echo $$! > .layout-chrome/pid
	@sleep 3
	@# The cart pages need a cart. Added through the site's own POST, so if
	@# add-to-cart is broken this check fails too — which is correct.
	@rm -f .layout-chrome/cookies
	@VARIANT=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock LIMIT 1"); \
		curl -s -o /dev/null -c .layout-chrome/cookies \
			-d "variant=$$VARIANT&quantity=1" $${GOEN_URL:-http://127.0.0.1:9700}/cart/items
	@# The two sessions every fixture below signs in with, minted HERE rather than
	@# at the end, because the back office's own forms are what seed the last three
	@# admin pages and they need a staff cookie to POST with.
	@#
	@# Only the SHA-256 is stored, which is what the application does; a row holding
	@# a raw token authenticates nothing.
	@#
	@# **Both inserts are verified, and that is a fix rather than tidying.** They
	@# used to end `>/dev/null 2>&1` with the user insert `|| true` beside them, so
	@# a missing user made the session INSERT ... SELECT write ZERO ROWS in silence
	@# and all 48 admin rows then failed with one message — "the staff session is
	@# not being accepted" — which names a rejected cookie and not an absent one.
	@# A fixture that fails quietly is CLAUDE.md #26; a check reporting four causes
	@# as one is #17. This target had both, on the same three lines.
	@psql "$$GOEN_DATABASE_URL" -qtAc "INSERT INTO users (email, role) VALUES ('layout-check@goen.invalid', 'admin') ON CONFLICT (lower(email)) DO NOTHING" >/dev/null
	@psql "$$GOEN_DATABASE_URL" -qtAc "INSERT INTO users (email, role, full_name, phone) VALUES ('layout-cust@goen.invalid', 'customer', '版面顧客', '0912345678') ON CONFLICT (lower(email)) DO NOTHING" >/dev/null
	@openssl rand -hex 32 > .layout-chrome/admin-token
	@openssl rand -hex 32 > .layout-chrome/cust-token
	@test "$$(psql "$$GOEN_DATABASE_URL" -qtAc "INSERT INTO sessions (token_hash, user_id, expires_at) SELECT sha256('$$(cat .layout-chrome/admin-token)'::bytea), id, now() + interval '1 hour' FROM users WHERE email = 'layout-check@goen.invalid' RETURNING 1")" = "1" \
		|| { echo 'the staff session fixture wrote no row — layout-check@goen.invalid does not exist, so every admin row below would fail as though the cookie were rejected' >&2; exit 2; }
	@test "$$(psql "$$GOEN_DATABASE_URL" -qtAc "INSERT INTO sessions (token_hash, user_id, expires_at) SELECT sha256('$$(cat .layout-chrome/cust-token)'::bytea), id, now() + interval '1 hour' FROM users WHERE email = 'layout-cust@goen.invalid' RETURNING 1")" = "1" \
		|| { echo 'the customer session fixture wrote no row — the question and return fixtures below cannot run' >&2; exit 2; }
	@# An order to measure the payment page against. Placed through the site's
	@# own checkout for the same reason the cart is: if placing an order is
	@# broken, this check should fail too.
	@SHIP=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT v.id FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id WHERE sm.is_active AND v.effective_at <= now() ORDER BY v.effective_at DESC LIMIT 1"); \
		curl -s -o /dev/null -b .layout-chrome/cookies -c .layout-chrome/cookies \
			-H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode 'email=layout@goen.invalid' --data-urlencode 'name=版面檢查' \
			--data-urlencode 'phone=0912345678' --data-urlencode 'postal_code=110' \
			--data-urlencode 'city=台北市' --data-urlencode 'district=信義區' \
			--data-urlencode 'street=松高路 1 號' --data-urlencode "shipping=$$SHIP" \
			--data-urlencode "idempotency=layout-$$$$" \
			$${GOEN_URL:-http://127.0.0.1:9700}/checkout
	@# Placing the order EMPTIES the cart, so the cart and checkout pages need
	@# it filled again. Without this they measure an empty cart and their own
	@# "no controls" guard fires — which is the guard working, and a check that
	@# proves nothing either way.
	@VARIANT=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock LIMIT 1"); \
		curl -s -o /dev/null -b .layout-chrome/cookies -c .layout-chrome/cookies \
			-d "variant=$$VARIANT&quantity=1" $${GOEN_URL:-http://127.0.0.1:9700}/cart/items
	@# The promotional strip is DATA — an empty promo_banners is a working site
	@# and a correct blank page, so its layout rows measured nothing at all
	@# until this fixture existed. The check's own marker guard is what said so.
	@#
	@# Still psql rather than the site's own form, and that is a deliberate
	@# exception rather than the old gap: /admin/home/banner exists now, but
	@# reaching it needs a staff session AND a 2FA step-up, which this target
	@# cannot produce before the server is up. The cart and the order are placed
	@# through the site precisely because they can be.
	@# With English copy, because the strip renders on every storefront page: a
	@# fixture with none makes every English page in the sweep look untranslated,
	@# and the sweep is how the last few gaps were found.
	@psql "$$GOEN_DATABASE_URL" -qtAc "INSERT INTO promo_banners (message, message_short, code, cta_label, cta_href, message_en, message_short_en, cta_label_en) SELECT '版面檢查用的促銷訊息,長度接近真實的一句文案', '版面檢查促銷', 'LAYOUT10', '看看', '/deals', 'A layout-check promotion, about as long as a real one', 'Layout promo', 'Look' WHERE NOT EXISTS (SELECT 1 FROM promo_banners)" >/dev/null 2>&1 || true
	@# The guest order above is given an owner, for the two /admin/customers rows:
	@# the search lists nothing until somebody searches, and the detail page of a
	@# customer with no orders measures its empty state.
	@#
	@# Targeted through its own GRANT rather than "the newest order", and that is
	@# load-bearing now: the return fixture below places a SECOND order, so the
	@# newest one stops being this one. The comment further down already warns that
	@# reading the order two different ways is how the cookie and the URL came to
	@# name two different things — this is the same hazard one row up.
	@psql "$$GOEN_DATABASE_URL" -qtAc "UPDATE orders o SET user_id = (SELECT id FROM users WHERE email = 'layout-cust@goen.invalid') FROM order_access_grants g WHERE g.order_id = o.id AND g.digest = sha256('$$(awk '/goen_placed/ {print $$7}' .layout-chrome/cookies)'::bytea)" >/dev/null
	@# A contact message, a product question and a return request, because
	@# /admin/messages, /admin/questions and /admin/returns each render an EMPTY
	@# STATE that carries the back-office chrome and nothing else. Their rows in the
	@# table measured 23 controls — the navigation — and reported a measured page.
	@# That is the finding this fixture closes, and it is CLAUDE.md #26 for the
	@# fourth time: a check over DATA needs that data seeded.
	@#
	@# All three go through the site's OWN forms. The point is not tidiness: if
	@# writing to the shop breaks, this check has to break with it, which a psql
	@# INSERT would hide. The sessions are the only thing reached past the
	@# application, and only because signing in needs an argon2 hash this file
	@# cannot cheaply produce.
	@curl -s -o /dev/null -w '' -H 'Sec-Fetch-Site: same-origin' \
		--data-urlencode 'name=版面檢查' --data-urlencode 'email=layout@goen.invalid' \
		--data-urlencode 'subject=訂單問題' --data-urlencode 'message=想確認一下出貨時間,謝謝。' \
		$${GOEN_URL:-http://127.0.0.1:9700}/contact
	@PRODUCT=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT slug FROM products WHERE status = 'active' ORDER BY slug LIMIT 1"); \
		curl -s -o /dev/null -b "goen_session=$$(cat .layout-chrome/cust-token)" \
			-H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode 'body=請問這款有支援快充嗎?盒裝裡面有附充電器嗎?' \
			$${GOEN_URL:-http://127.0.0.1:9700}/p/$$PRODUCT/questions
	@# The return needs an order that SHIPPED, which return_lines_within_purchase
	@# enforces in the database — so this cannot be one INSERT. It is the whole
	@# commercial path: the shop grants store credit, the customer spends it at
	@# checkout (which leaves the order owing nothing and therefore COMMITTED with
	@# no payment row at all — the zero-owed case order_is_committed exists for),
	@# the shop picks and ships it, and only then can the customer send it back.
	@#
	@# It is worth the six requests precisely because of that: five separate
	@# features have to work for the last one to produce a row, and none of them is
	@# covered by anything else here. It also means this target CONSUMES STOCK on
	@# every run, the same way the guest checkout already did; `make db-reset` is
	@# the reset.
	@#
	@# TWO values in this chain have to differ per run, and both were found the same
	@# way — by the shop REFUSING them, in the server log, while the check reported
	@# only "the fixture did not run". The tracking number is unique in
	@# order_shipments_tracking_key, so a fixed one ships exactly once ever.
	@#
	@# The chain now runs one step further, to DELIVERED, and then registers a
	@# warranty — because /admin/warranty lists nothing until somebody searches and
	@# a search finds nothing until somebody has registered. #26 again, and the
	@# variant is selected with `warranty_months IS NOT NULL` for the same reason:
	@# registration is REFUSED while the term is unset, and the seed deliberately
	@# leaves two accessories without one, so an unfiltered pick would produce a
	@# fixture that silently registers nothing on some runs and not others.
	@#
	@# Delivered rather than shipped, because cover starts when the parcel ARRIVES:
	@# a shipped order offers no registration form at all. That also gives
	@# /admin/returns' 消保法 §19 line a delivered order to compute a window from,
	@# instead of the 尚未送達 it measured before.
	@#
	@# The serial carries $$$$ for the reason the tracking number does:
	@# warranty_registrations_serial_key is unique, so a fixed one registers exactly
	@# once ever and every later run passes on the row the first left behind.
	@#
	@# The reason carries $$$$ because GrantCredit is idempotent on
	@# (customer, amount, reason) — a double-submitted form is one posting, which is
	@# correct and which made a FIXED reason fund only the very first run. Every run
	@# after it checked out an unfunded order, could not ship it, created no return,
	@# and passed anyway on the row the first run had left behind. A fixture that
	@# stops working and leaves its evidence lying around is worse than one that
	@# never worked; it was caught by shipping being REFUSED in the log, not by
	@# anything in the check.
	@CT=$$(cat .layout-chrome/cust-token); AT=$$(cat .layout-chrome/admin-token); \
		U=$${GOEN_URL:-http://127.0.0.1:9700}; \
		curl -s -o /dev/null -b "goen_session=$$AT" -H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode 'email=layout-cust@goen.invalid' --data-urlencode 'amount=99999' \
			--data-urlencode "reason=版面檢查用的退貨樣本 $$$$" $$U/admin/credit; \
		VARIANT=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock AND p.warranty_months IS NOT NULL LIMIT 1"); \
		rm -f .layout-chrome/cust-cookies; \
		curl -s -o /dev/null -c .layout-chrome/cust-cookies -b "goen_session=$$CT" \
			-d "variant=$$VARIANT&quantity=1" $$U/cart/items; \
		SHIP=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT v.id FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id WHERE sm.is_active AND v.effective_at <= now() ORDER BY v.effective_at DESC LIMIT 1"); \
		curl -s -o /dev/null -b .layout-chrome/cust-cookies -c .layout-chrome/cust-cookies \
			-b "goen_session=$$CT" -H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode 'email=layout-cust@goen.invalid' --data-urlencode 'name=版面顧客' \
			--data-urlencode 'phone=0912345678' --data-urlencode 'postal_code=110' \
			--data-urlencode 'city=台北市' --data-urlencode 'district=信義區' \
			--data-urlencode 'street=松高路 1 號' --data-urlencode "shipping=$$SHIP" \
			--data-urlencode "idempotency=layout-return-$$$$" $$U/checkout; \
		RN=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT o.order_number FROM orders o JOIN users u ON u.id = o.user_id WHERE u.email = 'layout-cust@goen.invalid' ORDER BY o.placed_at DESC LIMIT 1"); \
		curl -s -o /dev/null -b "goen_session=$$AT" -H 'Sec-Fetch-Site: same-origin' \
			-d 'status=picking' $$U/admin/orders/$$RN/status; \
		curl -s -o /dev/null -b "goen_session=$$AT" -H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode 'carrier=黑貓宅急便' --data-urlencode "tracking=LAYOUTCHECK$$$$" \
			--data-urlencode 'fee=80' $$U/admin/orders/$$RN/ship; \
		LINE=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT ol.id FROM order_lines ol JOIN orders o ON o.id = ol.order_id WHERE o.order_number = '$$RN' LIMIT 1"); \
		curl -s -o /dev/null -b "goen_session=$$AT" -H 'Sec-Fetch-Site: same-origin' \
			-d 'status=delivered' $$U/admin/orders/$$RN/status; \
		curl -s -o /dev/null -b "goen_session=$$CT" -H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode "line=$$LINE" --data-urlencode 'unit=1' \
			--data-urlencode "serial=LAYOUTSN$$$$" $$U/account/warranty/$$RN; \
		curl -s -o /dev/null -b "goen_session=$$CT" -H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode 'reason=尺寸不合,想換一個顏色' --data-urlencode "qty_$$LINE=1" \
			$$U/orders/$$RN/return
	@# /admin/health's two ALARM tables and the 折讓 claim row on an order page.
	@# Each renders only when there is something wrong, so the page a browser sees
	@# without them is the healthy one — chrome, a status list, and none of the
	@# markup added for the states an operator actually has to act on. That is the
	@# no-fixture trap this file already records for the promotional strip and for
	@# /admin/questions: a check over DATA needs the data seeded.
	@#
	@# The webhook row is money that arrived for an order goen had cancelled; the
	@# claim is a 折讓 the provider never answered, aged past the window that tells
	@# a stuck one from a call in flight.
	@psql "$$GOEN_DATABASE_URL" -qtAc "INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, unreconciled) SELECT 'stripe', 'evt_layout_check', 'checkout.session.completed', 'cs_layout_check', '{}'::jsonb, '版面檢查:款項落在已取消的訂單上' WHERE NOT EXISTS (SELECT 1 FROM payment_webhook_events WHERE event_id = 'evt_layout_check')" >/dev/null
	@psql "$$GOEN_DATABASE_URL" -qtAc "WITH inv AS (INSERT INTO invoice_documents (order_id, kind, number, amount_cents) SELECT o.id, 'invoice', 'GD-LAYOUT1', 100000 FROM orders o JOIN order_access_grants g ON g.order_id = o.id WHERE g.digest = sha256('$$(awk '/goen_placed/ {print $$7}' .layout-chrome/cookies)'::bytea) AND NOT EXISTS (SELECT 1 FROM invoice_documents WHERE number = 'GD-LAYOUT1') RETURNING id, order_id) INSERT INTO invoice_documents (order_id, kind, number, amount_cents, status, request_key, original_id, issued_at) SELECT inv.order_id, 'allowance', '', 50000, 'pending', 'allowance:layout-check', inv.id, now() - interval '1 hour' FROM inv" >/dev/null
	@# The placed-order cookie carries TOKENS, not order numbers. It used to carry
	@# the numbers and they were the proof — but a number comes off a per-day
	@# counter, so anybody could set the cookie by hand and increment into somebody
	@# else's address. Two different facts came out of that one value the day it
	@# changed: the COOKIE is a token, and the URL still needs the NUMBER. Lifting
	@# the cookie into both is what made every pay row measure a 404, and the
	@# marker guard is the only thing that said so.
	@#
	@# The number is read back through the GRANT the token names rather than as
	@# "the newest order", so the cookie and the URL cannot come to name two
	@# different orders — which is exactly the divergence being repaired here.
	@PLACED_TOKEN=$$(awk '/goen_placed/ {print $$7}' .layout-chrome/cookies); \
		PRODUCT_SLUG=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT slug FROM products WHERE status = 'active' ORDER BY slug LIMIT 1") \
		PICKUP_SHIP=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT v.id FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id WHERE sm.code = 'store_pickup' ORDER BY v.effective_at DESC LIMIT 1") \
		CART_TOKEN=$$(awk '/goen_cart/ {print $$7}' .layout-chrome/cookies) \
		PLACED_TOKEN=$$PLACED_TOKEN \
		PLACED_ORDER=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT o.order_number FROM orders o JOIN order_access_grants g ON g.order_id = o.id WHERE g.digest = sha256('$$PLACED_TOKEN'::bytea)") \
		CUSTOMER_ID=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT id FROM users WHERE email = 'layout-cust@goen.invalid'") \
		LAYOUT_SERIAL=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT w.serial_number FROM warranty_registrations w JOIN users u ON u.id = w.user_id WHERE u.email = 'layout-cust@goen.invalid' ORDER BY w.registered_at DESC LIMIT 1") \
		ADMIN_TOKEN=$$(cat .layout-chrome/admin-token) node scripts/check-layout.mjs; status=$$?; \
		kill $$(cat .layout-chrome/pid) 2>/dev/null; sleep 1; rm -rf .layout-chrome 2>/dev/null; \
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

# Load the development catalogue: brands, categories, ~15 products with variants,
# images, specs and reviews. Runs as the owner (psql, not the app's store
# role), so it may write the tables store is barred from. Development only.
# Edit seed/gen_seed.py and re-run it to regenerate seed/dev_catalog.sql.
db-seed:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required' >&2; exit 2; }
	psql "$$GOEN_DATABASE_URL" -v ON_ERROR_STOP=1 -f seed/dev_catalog.sql

# Rebuild the development database from scratch.
#
# 001 is still amended in place rather than superseded (see CLAUDE.md), so an
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
CATALOG_SQL := \
  "SELECT 'constraint\t'||conrelid::regclass||'\t'||conname||'\t'||pg_get_constraintdef(oid) \
     FROM pg_constraint WHERE connamespace='public'::regnamespace \
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
# check. Outside `verify` for the reason test-integration is: it needs something
# the gate cannot assume.
.PHONY: schema-drift
schema-drift:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required: the database to CHECK' >&2; exit 2; }
	@set -eu; \
	ref=goen_schema_ref_$$$$; \
	trap 'docker compose exec -T db dropdb -U goen --if-exists --force "$$ref" >/dev/null 2>&1 || true' 0 HUP INT TERM; \
	docker compose exec -T db createdb -U goen "$$ref"; \
	base=$${GOEN_DATABASE_URL%%\?*}; \
	case "$$GOEN_DATABASE_URL" in *\?*) query="?$${GOEN_DATABASE_URL#*\?}";; *) query="";; esac; \
	refurl="$${base%/*}/$$ref$$query"; \
	$(MIGRATE) -path migrations -database "$$refurl" up >/dev/null; \
	:; \
	psql "$$refurl" -At -c $(CATALOG_SQL) | sort > /tmp/goen-schema-ref.txt; \
	test -s /tmp/goen-schema-ref.txt || { echo 'schema-drift: the reference database is EMPTY; it was not built' >&2; exit 3; }; \
	psql "$$refurl" -At -c "SELECT current_database()" | grep -qx "$$ref" \
		|| { echo 'schema-drift: the reference URL does not point at the reference database, so this would compare the live one with itself' >&2; exit 3; }; \
	psql "$$GOEN_DATABASE_URL" -At -c $(CATALOG_SQL) | sort > /tmp/goen-schema-live.txt; \
	if diff -u /tmp/goen-schema-ref.txt /tmp/goen-schema-live.txt > /tmp/goen-schema-drift.txt; then \
		echo 'schema-drift: PASS — the deployed schema matches migrations/'; \
	else \
		echo 'schema-drift: FAIL — the deployed schema and migrations/ disagree.'; \
		echo '  -  is what migrations/ declares; +  is what the database has.'; \
		echo '  An amended CHECK is not re-validated by PostgreSQL, so this is'; \
		echo '  where amend-in-place stops being safe and 002 begins.'; \
		cat /tmp/goen-schema-drift.txt; \
		exit 1; \
	fi

# Prove the backup can be restored. Not that one exists — that a dump of this
# database comes back as this database.
#
# goen's images live IN PostgreSQL (the recorded internal/media decision), so
# the database is the only copy of the catalogue's photography as well as its
# data. A backup nobody has restored is a belief, and the first time anybody
# finds out is the worst possible time.
#
# It dumps, restores into a throwaway, and then asks two questions: does the
# restored SCHEMA match migrations/, and did every table come back with the same
# number of rows. Schema alone would pass on a dump that lost every row.
.PHONY: restore-drill
restore-drill:
	@test -n "$${GOEN_DATABASE_URL:-}" || { echo 'GOEN_DATABASE_URL is required: the database to back up' >&2; exit 2; }
	@set -eu; \
	copy=goen_restore_drill_$$$$; \
	trap 'docker compose exec -T db dropdb -U goen --if-exists --force "$$copy" >/dev/null 2>&1 || true' 0 HUP INT TERM; \
	base=$${GOEN_DATABASE_URL%%\?*}; \
	case "$$GOEN_DATABASE_URL" in *\?*) query="?$${GOEN_DATABASE_URL#*\?}";; *) query="";; esac; \
	copyurl="$${base%/*}/$$copy$$query"; \
	echo 'restore-drill: dumping...'; \
	pg_dump "$$GOEN_DATABASE_URL" -Fc -f /tmp/goen-drill.dump; \
	docker compose exec -T db createdb -U goen "$$copy"; \
	echo 'restore-drill: restoring into a throwaway...'; \
	pg_restore -d "$$copyurl" --no-owner --no-privileges /tmp/goen-drill.dump >/dev/null 2>&1 || true; \
	psql "$$copyurl" -At -c "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relkind='r'" \
		| grep -qv '^0$$' || { echo 'restore-drill: the restored copy has no tables' >&2; exit 1; }; \
	echo 'restore-drill: comparing row counts...'; \
	counts="SELECT relname||' '||n_live_tup FROM pg_stat_user_tables ORDER BY relname"; \
	psql "$$GOEN_DATABASE_URL" -At -c "ANALYZE" >/dev/null; psql "$$copyurl" -At -c "ANALYZE" >/dev/null; \
	psql "$$GOEN_DATABASE_URL" -At -c "$$counts" | sort > /tmp/goen-drill-live.txt; \
	psql "$$copyurl" -At -c "$$counts" | sort > /tmp/goen-drill-copy.txt; \
	if diff -u /tmp/goen-drill-live.txt /tmp/goen-drill-copy.txt > /tmp/goen-drill-diff.txt; then \
		echo 'restore-drill: PASS — the dump restores to the same schema and the same rows'; \
	else \
		echo 'restore-drill: FAIL — the restored copy is not what was dumped.'; \
		echo '  -  is the live database; +  is what came back.'; \
		cat /tmp/goen-drill-diff.txt; \
		exit 1; \
	fi

db-reset:
	docker compose exec -T db dropdb -U goen --if-exists --force goen
	docker compose exec -T db createdb -U goen goen
	$(MAKE) migrate-up
	$(MAKE) db-seed
	@echo 'database rebuilt from migrations/ and seeded'

# The single gate. Stop at the first failure — a passing later stage must never
# be able to bury an earlier red one.
verify: fmt-check templ-check squawk sqlc-check vet lint integration-build-check test-race
	@echo 'verify: PASS (unit tests only — make verify-all adds the database suite)'

# Everything verify runs plus the parts that need Docker and the network.
verify-all: verify test-integration vuln
	@echo 'verify-all: PASS' 

clean:
	rm -rf bin
