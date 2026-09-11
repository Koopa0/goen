GOLANGCI_LINT_VERSION := 2.13.2
SQLC_VERSION := v1.31.1
KO_VERSION := v0.19.1
MIGRATE_VERSION := v4.19.1
GOVULNCHECK_VERSION := v1.7.0
SQUAWK_VERSION := 2.64.0
DEADCODE_VERSION := v0.49.0

# Tools that generate or inspect this module but are not part of it. `go run
# pkg@version` pins each as firmly as a require line without joining the module
# graph — see CLAUDE.md, "Build tools stay out of go.mod".
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
MIGRATE := go run -tags='postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
DEADCODE := go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION)

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
        cursor-scripts-check verify verify-all check-layout db-reset clean

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
	@U=$${GOEN_URL:-http://127.0.0.1:9700}; \
		PAGE=$$(curl -fsS -b .layout-chrome/cookies $$U/checkout); \
		QUOTE=$$(printf '%s' "$$PAGE" | grep -o 'name="checkout_quote" value="[^"]*"' | head -1 | cut -d'"' -f4); \
		ATTEMPT=$$(printf '%s' "$$PAGE" | grep -o 'name="idempotency" value="[^"]*"' | head -1 | cut -d'"' -f4); \
		SHIP=$$(printf '%s' "$$PAGE" | grep -o 'name="shipping" value="[^"]*" checked' | head -1 | cut -d'"' -f4); \
		test -n "$$QUOTE" -a -n "$$ATTEMPT" -a -n "$$SHIP" || { echo 'checkout fixture did not render its quote, attempt ID and selected shipping method' >&2; exit 2; }; \
		STATUS=$$(curl -sS -o /dev/null -w '%{http_code}' -b .layout-chrome/cookies -c .layout-chrome/cookies \
			-H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode 'email=layout@goen.invalid' --data-urlencode 'name=版面檢查' \
			--data-urlencode 'phone=0912345678' --data-urlencode 'postal_code=110' \
			--data-urlencode 'city=台北市' --data-urlencode 'district=信義區' \
			--data-urlencode 'street=松高路 1 號' --data-urlencode "shipping=$$SHIP" \
			--data-urlencode "checkout_quote=$$QUOTE" \
			--data-urlencode "idempotency=$$ATTEMPT" \
			$$U/checkout); \
		test "$$STATUS" = 303 || { echo "layout payment checkout answered $$STATUS, want 303" >&2; exit 2; }
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
	@# checkout (which leaves the order owing nothing — funded and still pending,
	@# with no payment row, the zero-owed case; committed_orders counts it only
	@# once fulfillment leaves pending, which this chain does at picking),
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
	@# GrantCredit is idempotent on the form's operation_id, not on
	@# (customer, amount, reason). The page mints that id; a POST without it is
	@# 303 to ?needs=1 and funds nothing. A curl that only sends email/amount/reason
	@# therefore checks out an unfunded order, cannot pick or ship it, creates no
	@# return, and leaves invoice_allowance_valid with a zero refund — the same
	@# silent-fixture failure a fixed reason used to cause. Read the id off the
	@# form and require ok=1, because a refused grant is also a 303.
	@#
	@# The reason still carries $$$$: the ledger is append-only and a repeated
	@# sentence makes two runs look like one posting when an operator reads it.
	@CT=$$(cat .layout-chrome/cust-token); AT=$$(cat .layout-chrome/admin-token); \
		U=$${GOEN_URL:-http://127.0.0.1:9700}; \
		CREDIT_PAGE=$$(curl -fsS -b "goen_session=$$AT" $$U/admin/credit); \
		OP=$$(printf '%s' "$$CREDIT_PAGE" | grep -o 'name="operation_id" value="[^"]*"' | head -1 | cut -d'"' -f4); \
		test -n "$$OP" || { echo 'credit grant form did not render an operation_id' >&2; exit 2; }; \
		GRANT=$$(curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' -b "goen_session=$$AT" \
			-H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode 'email=layout-cust@goen.invalid' --data-urlencode 'amount=99999' \
			--data-urlencode "reason=版面檢查用的退貨樣本 $$$$" \
			--data-urlencode "operation_id=$$OP" $$U/admin/credit); \
		test "$${GRANT%% *}" = 303 || { echo "credit grant answered $${GRANT%% *}, want 303" >&2; exit 2; }; \
		printf '%s' "$${GRANT#* }" | grep -q 'ok=1' \
			|| { echo "credit grant redirected to $${GRANT#* }, want ok=1" >&2; exit 2; }; \
		VARIANT=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock AND p.warranty_months IS NOT NULL LIMIT 1"); \
		rm -f .layout-chrome/cust-cookies; \
		curl -s -o /dev/null -c .layout-chrome/cust-cookies -b "goen_session=$$CT" \
			-d "variant=$$VARIANT&quantity=1" $$U/cart/items; \
		PAGE=$$(curl -fsS -b .layout-chrome/cust-cookies -b "goen_session=$$CT" $$U/checkout); \
		QUOTE=$$(printf '%s' "$$PAGE" | grep -o 'name="checkout_quote" value="[^"]*"' | head -1 | cut -d'"' -f4); \
		ATTEMPT=$$(printf '%s' "$$PAGE" | grep -o 'name="idempotency" value="[^"]*"' | head -1 | cut -d'"' -f4); \
		SHIP=$$(printf '%s' "$$PAGE" | grep -o 'name="shipping" value="[^"]*" checked' | head -1 | cut -d'"' -f4); \
		test -n "$$QUOTE" -a -n "$$ATTEMPT" -a -n "$$SHIP" || { echo 'return fixture did not render its quote, attempt ID and selected shipping method' >&2; exit 2; }; \
		STATUS=$$(curl -sS -o /dev/null -w '%{http_code}' -b .layout-chrome/cust-cookies -c .layout-chrome/cust-cookies \
			-b "goen_session=$$CT" -H 'Sec-Fetch-Site: same-origin' \
			--data-urlencode 'email=layout-cust@goen.invalid' --data-urlencode 'name=版面顧客' \
			--data-urlencode 'phone=0912345678' --data-urlencode 'postal_code=110' \
			--data-urlencode 'city=台北市' --data-urlencode 'district=信義區' \
			--data-urlencode 'street=松高路 1 號' --data-urlencode "shipping=$$SHIP" \
			--data-urlencode "checkout_quote=$$QUOTE" \
			--data-urlencode "idempotency=$$ATTEMPT" $$U/checkout); \
		test "$$STATUS" = 303 || { echo "return fixture checkout answered $$STATUS, want 303" >&2; exit 2; }; \
		RN=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT o.order_number FROM orders o JOIN users u ON u.id = o.user_id WHERE u.email = 'layout-cust@goen.invalid' ORDER BY o.placed_at DESC LIMIT 1"); \
		test -n "$$RN" || { echo 'return fixture checkout created no customer order' >&2; exit 2; }; \
		OWED=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT order_amount_owed(id)::text FROM orders WHERE order_number = '$$RN'"); \
		test "$$OWED" = "0" || { echo "return fixture checkout left $$RN owing $$OWED cents; pick will be refused" >&2; exit 2; }; \
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
			$$U/orders/$$RN/return; \
		RID=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT r.id FROM return_requests r JOIN orders o ON o.id = r.order_id WHERE o.order_number = '$$RN' AND r.status = 'requested' ORDER BY r.created_at DESC LIMIT 1"); \
		test -n "$$RID" || { echo 'return fixture created no return request' >&2; exit 2; }; \
		STATUS=$$(curl -sS -o /dev/null -w '%{http_code}' -b "goen_session=$$AT" -H 'Sec-Fetch-Site: same-origin' \
			-d 'decision=approved' --data-urlencode 'resolution=版面檢查同意退貨' \
			$$U/admin/returns/$$RID/decide); \
		test "$$STATUS" = 303 || { echo "return fixture decide answered $$STATUS, want 303" >&2; exit 2; }; \
		REFUNDED=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT (rf.card_cents + rf.credit_cents)::text FROM order_refunds rf JOIN orders o ON o.id = rf.order_id WHERE o.order_number = '$$RN'"); \
		test "$${REFUNDED:-0}" -ge 100 || { echo "return fixture refunded $${REFUNDED:-0} cents after decide, want at least 100 so invoice_allowance_valid has room" >&2; exit 2; }
	@# /admin/health's two ALARM tables and the 折讓 form on an order page.
	@# Each renders only when there is something wrong, so the page a browser sees
	@# without them is the healthy one — chrome, a status list, and none of the
	@# markup added for the states an operator actually has to act on. That is the
	@# no-fixture trap this file already records for the promotional strip and for
	@# /admin/questions: a check over DATA needs the data seeded.
	@#
	@# The webhook row is money that arrived for an order goen had cancelled.
	@#
	@# The 折讓 fixtures used to land on the unpaid guest order the payment page
	@# measures. invoice_allowance_valid now refuses a credit note larger than
	@# order_refunds, and that order has refunded nothing — a compensation posting
	@# is refused while it is still an open unpaid checkout. The return fixture
	@# above is decided through the shop's own form, which posts store credit
	@# through compensate_return_with_credit, so order_refunds has a real figure
	@# before any document is written.
	@#
	@# A pending invoice_documents row is no longer a tax document. The stranded
	@# claim — a 折讓 the provider never answered, aged past the window that
	@# tells a stuck one from a call in flight — lives on invoice_operations.
	@# The issued invoice and a partial issued allowance (always short of the
	@# refunded whole-dollar room) are what put the 折讓 form on
	@# /admin/orders/{number}: the form offers the remainder, and writing the
	@# allowance after the decide is what invoice_allowance_valid now requires.
	@psql "$$GOEN_DATABASE_URL" -qtAc "INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, unreconciled) SELECT 'stripe', 'evt_layout_check', 'checkout.session.completed', 'cs_layout_check', '{}'::jsonb, '版面檢查:款項落在已取消的訂單上' WHERE NOT EXISTS (SELECT 1 FROM payment_webhook_events WHERE event_id = 'evt_layout_check')" >/dev/null
	@psql "$$GOEN_DATABASE_URL" -qtAc "\
		WITH src AS ( \
		  SELECT o.id AS order_id, u.id AS actor_id, \
		         (rf.card_cents + rf.credit_cents) AS refunded \
		  FROM orders o \
		  JOIN return_requests r ON r.order_id = o.id \
		  JOIN order_refunds rf ON rf.order_id = o.id \
		  JOIN users u ON u.email = 'layout-check@goen.invalid' \
		  WHERE r.status IN ('approved', 'completed') \
		    AND o.user_id = (SELECT id FROM users WHERE email = 'layout-cust@goen.invalid') \
		    AND (rf.card_cents + rf.credit_cents) >= 100 \
		  ORDER BY o.placed_at DESC LIMIT 1 \
		), inv AS ( \
		  INSERT INTO invoice_documents (order_id, kind, number, amount_cents) \
		  SELECT order_id, 'invoice', 'GD-LAYOUT1', 100000 FROM src \
		  WHERE NOT EXISTS (SELECT 1 FROM invoice_documents WHERE number = 'GD-LAYOUT1') \
		  RETURNING id, order_id \
		), allowance AS ( \
		  INSERT INTO invoice_documents (order_id, kind, number, amount_cents, request_key, original_id) \
		  SELECT inv.order_id, 'allowance', 'IA-LAYOUT1', \
		         least(50000, greatest((src.refunded / 100) * 100 - 100, 100)), \
		         'allowance:layout-check', inv.id \
		  FROM inv JOIN src ON src.order_id = inv.order_id \
		  WHERE (src.refunded / 100) * 100 > 100 \
		    AND NOT EXISTS (SELECT 1 FROM invoice_documents WHERE number = 'IA-LAYOUT1') \
		  RETURNING id \
		) \
		INSERT INTO invoice_operations \
		    (order_id, kind, target_document_id, provider_key, amount_cents, \
		     request_payload, actor_user_id, actor_id_snapshot, request_id, \
		     send_attempts, last_send_at, last_error, created_at, updated_at) \
		SELECT inv.order_id, 'allowance', inv.id, 'GD-LAYOUT1', \
		       least(50000, (src.refunded / 100) * 100), \
		       jsonb_build_object( \
		           'invoice_number', 'GD-LAYOUT1', \
		           'invoice_date', to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD'), \
		           'customer_name', '版面顧客', 'email', 'layout-cust@goen.invalid', \
		           'amount_cents', least(50000, (src.refunded / 100) * 100), \
		           'lines', jsonb_build_array(jsonb_build_object( \
		               'description', '退貨折讓', 'quantity', 1, \
		               'unit_price_cents', least(50000, (src.refunded / 100) * 100), \
		               'amount_cents', least(50000, (src.refunded / 100) * 100)))), \
		       src.actor_id, src.actor_id, 'invoice-layout-check', \
		       1, now() - interval '1 hour', 'allowance_not_yet_visible', \
		       now() - interval '1 hour', now() - interval '1 hour' \
		FROM inv JOIN src ON src.order_id = inv.order_id \
		WHERE NOT EXISTS ( \
		  SELECT 1 FROM invoice_operations WHERE request_id = 'invoice-layout-check')" >/dev/null
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
		INVOICE_ORDER=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT o.order_number FROM orders o JOIN return_requests r ON r.order_id = o.id JOIN invoice_documents d ON d.order_id = o.id AND d.number = 'GD-LAYOUT1' WHERE r.status IN ('approved', 'completed') ORDER BY o.placed_at DESC LIMIT 1"); \
		test -n "$$INVOICE_ORDER" || { echo 'invoice fixture wrote no refunded order — /admin/orders/ would measure the list and call the 折讓 form covered' >&2; exit 2; }; \
		PRODUCT_SLUG=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT slug FROM products WHERE status = 'active' ORDER BY slug LIMIT 1") \
		PICKUP_SHIP=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT v.id FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id WHERE sm.code = 'store_pickup' ORDER BY v.effective_at DESC LIMIT 1") \
		CART_TOKEN=$$(awk '/goen_cart/ {print $$7}' .layout-chrome/cookies) \
		PLACED_TOKEN=$$PLACED_TOKEN \
		PLACED_ORDER=$$(psql "$$GOEN_DATABASE_URL" -tAc "SELECT o.order_number FROM orders o JOIN order_access_grants g ON g.order_id = o.id WHERE g.digest = sha256('$$PLACED_TOKEN'::bytea)") \
		INVOICE_ORDER=$$INVOICE_ORDER \
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
# Each half is recorded red in CLAUDE.md.
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

# Deterministic checks for the .cursor/ Cloud Agent environment scripts: a
# syntax pass over every script, then the config-key parser test against
# committed CLI-shaped fixtures and TEST-mode key-prefix guards. No network and
# no live Stripe, so a regression — dropping double-quoted TOML support or
# accepting a live key prefix — turns make verify red instead of merging green.
cursor-scripts-check:
	@for f in .cursor/*.sh .cursor/lib/*.sh; do bash -n "$$f" || exit 1; done
	@bash .cursor/lib/stripe-config-key.test.sh
	@bash .cursor/lib/stripe-sandbox-key.test.sh

# The single gate. Stop at the first failure — a passing later stage must never
# be able to bury an earlier red one.
verify: cursor-scripts-check fmt-check templ-check squawk sqlc-check vet deadcode lint production-build-check integration-build-check test-race
	@echo 'verify: PASS (unit tests only — make verify-all adds the database suite)'

# Everything verify runs plus the parts that need Docker and the network.
verify-all: verify test-integration vuln
	@echo 'verify-all: PASS' 

clean:
	rm -rf bin
