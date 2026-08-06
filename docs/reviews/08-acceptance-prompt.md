# Third-party acceptance: goen (round 8)

You are accepting a project you did not build. Everything below is written by the
builder, which means **it is a claim, not evidence** — your job is to turn the claims
you can into facts, and to say plainly which ones you could not.

Two acceptance rounds have already happened. This one is different from both, and the
difference is the point: **the last round changed a great deal of load-bearing code —
the database privilege model, the payment state machine, the refund state machine,
the checkout's concurrency, and three pieces of new concurrent code — and nobody
except the builder has read any of it.** The risk has moved. Where to look has moved
with it.

## What this is

`goen` — a Traditional-Chinese 3C storefront. One Go binary serves the storefront, the
customer account and `/admin`. Go 1.26.5, `net/http` (no framework, 163 routes),
`templ` for server-rendered HTML, PostgreSQL 18 via pgx + sqlc, Stripe hosted
Checkout, htmx only as progressive enhancement. 103 modules in the graph.

**It is a demo/portfolio project, not a live shop.** Calibrate severity accordingly: a
missing enterprise feature is not a finding; a way to lose somebody's money, take
stock that is not there, read another customer's data, or reach the back office is.

Read `CLAUDE.md` at the repository root first — it is the design record, and every
unusual decision is written there with the reasoning and, usually, the defect that
produced it. Then read `docs/reviews/07-codex-round6-dispositions.md`, which is the
disposition of the previous two rounds. **Read it AFTER you have formed your own
findings from the code, not before** — a disposition hands you its own frame, and this
project's own rules say so.

## Set it up

```bash
make db-up                 # PostgreSQL 18 in Docker on 127.0.0.1:5433
make migrate-up            # migrations/001_initial_schema.up.sql
make db-seed               # dev catalogue
make run                   # 127.0.0.1:9700
```

`GOEN_DATABASE_URL` is required and has no default. `.env` is read by the Makefile in
development. Stripe, SMTP and TOTP keys may be empty — the features then say so rather
than half-working, and **two of them now refuse to start** in a production posture (see
below).

## Run the gates yourself. Do not read the transcripts.

```bash
make verify            # fmt, templ, squawk, sqlc-check, vet, lint, race tests. No Docker.
make test-integration  # schema conformance + every feature's integration tests
make check-layout      # 109 viewport rows in a real headless Chrome; needs `make run` first
```

Run `make verify` **twice**. A gate that repairs what it checks passes on the second
run and not the first, and this repository has been bitten by exactly that.

The builder reports all three green. If any is not, that is your first finding.

## The one thing most likely to be wrong, and why no test here can see it

The database privilege model was tightened substantially. `store` and `admin` used to
hold whole-table `INSERT`/`UPDATE` on `users` and `sessions`; they now hold **column
lists**. Several tables were revoked from each role outright.

**This section said the wrong thing in round 8, and the correction is the lesson.**

It claimed a too-narrow grant "fails nowhere in this test suite" because "every suite
connects as the schema OWNER", and sent a reviewer to click through the whole
application as `store_svc`. That framing was false. `SET ROLE` binds ACLs even for a
superuser, and `EXPLAIN (GENERIC_PLAN)` plans a statement — resolving every table,
column and function privilege — without executing it and without bound parameters. The
reviewer wrote that check in about sixty lines, ran it over all generated queries in
seconds, and it found two live defects.

`TestEveryRoleCanRunItsOwnQueries` is that check, now in the suite. So:

- **Do not take "structurally impossible" from a builder.** This project's own history
  is that the impossible-sounding check was usually just unwritten. Ask what would make
  it possible before accepting the errand.
- Read that guard and try to get past it. Its weak point is the **pool map**: it maps
  PACKAGES to roles, and a package constructed on two pools (`internal/media`,
  `internal/newsletter`) needs a per-query exemption. Every exemption is a claim; check
  them.
- Then still exercise the real write paths as `store_svc` and `admin_svc`, because the
  guard proves a statement can be PLANNED, not that a feature works. Register, sign in,
  change a password, place an order as guest and customer, use a coupon, spend credit,
  cancel, return, subscribe. Then the back office: create a product through to publish,
  ship, refund, grant credit, adjust stock, add and revoke a colleague, send a
  newsletter.

Anything that returns 500 with `permission denied` is a finding.

The relevant grants are at the very end of `migrations/001_initial_schema.up.sql`, in a
section titled "The AUTHENTICATION and MERCHANDISING surfaces".

## The specific things the builder could NOT prove

These are the honest gaps. Start here after the privilege sweep.

1. **The column-level privilege guard covers only four tables.** `TestNoRoleHoldsAColumnWriteItsQueriesNeverMake`
   is scoped to `users`, `sessions`, `product_variants` and `payment_webhook_events`.
   Running the same question over every table surfaces a real and larger set — `store`
   can set `product_reviews.hidden_at`, `return_requests.resolution`,
   `orders.staff_note`, `contact_messages.handled_at` and others; `admin` can rewrite
   `contact_messages.message`, a customer's own words. These are **queued, not fixed**,
   with the reason written in the guard's own doc comment. Judge whether that call is
   right, and whether any of them is worse than the builder thinks.
2. **Cancelling an order still does not expire its Stripe session.** The window is
   bounded (≤30 minutes, because the session now expires with the stock hold) and loud
   (the capture is refused by name, the event is recorded, the handler logs what is
   needed to refund by hand). It is not closed. Try to take money for a cancelled
   order and see whether the outcome is as bounded as claimed.
3. **The Stripe HTTP surface has no test at all**, before or after this round. The
   session-resume logic that stops an order opening two Checkout Sessions is proven at
   the SQL layer and by an idempotency-key unit test; the five lines of handler wiring
   that call it are not covered. A stand-in would need an exported test-only seam on
   `NewGateway`, which the builder judged worse than the gap. Overrule that if you
   disagree — and either way, reason about `Handler.Start` by hand.
4. **Three behaviours are recorded as "mutation green"** — the constant-time compare in
   `internal/twofactor`, the clock-skew fix in `/admin/messages`, and the fact that a
   trigger reading an unlocked row could not be shown to lose a race. The previous
   reviewer accepted the first two and offered a source-derived test for the compare
   that was not taken up. Judge whether that is still the right call.
5. **No load test, no deployment, no second reader on any of this round's code.**

## Where the NEW code is

This is what changed last round. None of it has been read by anybody but the author,
and three pieces of it are concurrent.

- **`internal/media`** — a new bounded LRU rendition cache, `singleflight.DoChan`, and
  a `GOMAXPROCS` semaphore, added to stop an image endpoint being a CPU denial of
  service. New concurrent code on a **public, unauthenticated** endpoint. Look for
  unbounded memory, a cache key that can be forced to miss, a goroutine that outlives
  its request, and whether the semaphore can be starved.
- **`internal/ratelimit`** — new parsing of `X-Forwarded-For` against a trusted-proxy
  CIDR list (`GOEN_TRUSTED_PROXIES`). This reads an **attacker-controlled header**. The
  default (empty) must behave exactly as before. Try to buy yourself a fresh rate-limit
  bucket: spoofed hops, ports, IPv6 zones, v4-mapped addresses, multiple header lines,
  a trusted proxy that is also a client.
- **`internal/cart`** — `PlaceOrder` now takes `pg_advisory_xact_lock` on the checkout
  key as its first statement. Think about lock ordering against every other lock the
  checkout takes (`hold_inventory`, `store_credit_guard`, `redeem_coupon`), and about
  what a hash collision between two different keys costs.
- **`internal/admin`** — the refund state machine was rewritten: Stripe's status is
  propagated, an ambiguous transport error stays `pending` rather than terminal, and
  `RefundedSoFar` excludes its own request key so a stalled refund is retryable. Money
  moves here. Check the arithmetic against `refunds_guard` in the migration, and look
  for a way to refund one capture twice.
- **`internal/payment`** — session resume, an idempotency key carrying an attempt
  counter, session expiry derived from the stock hold, and two new webhook event types
  (`async_payment_succeeded`, `async_payment_failed`).
- **`internal/twofactor`** — moved to the ADMIN pool, enrolment refuses to replace a
  confirmed credential, and `AddStaff` refuses the actor's own address.
- **`cmd/goen`** — refuses to start without `GOEN_TOTP_KEY` or `GOEN_BASE_URL` when
  cookies are secure; `run()` was split into four functions.
- **`migrations/001`** — the privilege sections, `erase_user`, a capture guard for
  cancelled orders, a `coupons` trigger, and a deleted `orders.created_at` column.

## What NEITHER previous round looked at at all

The last reviewer wrote this list themselves and called it the most useful thing they
could give. It is still almost entirely unexamined, and it is where an untouched
defect is most likely to be sitting.

- **`internal/ui` — the entire view layer, ~40 `.templ` files.** No XSS review has ever
  been done. `templ.Raw` and `templ.SafeURL` are used in places; only the one JSON-LD
  block has a test.
- **`internal/media`'s UPLOAD path, END TO END through HTTP.** A previous version of
  this list said the three claimed security properties had never been tested with a
  malformed file. **That was wrong** — `internal/media/media_test.go` tests a polyglot
  trailer, rejects HTML, PHP, SVG, a bare PNG magic number, a truncated PNG and a zip,
  and asserts a decompression bomb is refused with `ErrTooLarge` specifically, with the
  mutation showing the weaker assertion stayed green. They are good tests, and the
  false claim scoped a whole review round. What remains genuinely untested is the path
  through the HTTP handler as `admin_svc`: the multipart parse, `MaxBytesReader`, the
  rendition width allowlist, and what a caller sees on refusal.
- **`internal/catalog`** — listing, search, facets, `/deals`. The one-variant facet
  rule and the `status = 'active'` literal-vs-parameter index decision are both
  documented and unverified.
- **Most of `internal/admin`** — products, variants, specs, taxonomy, shipping,
  campaigns, coupons, credit grants, staff, FAQ, tiers, reports, questions, reviews,
  newsletter send. Including **every `record_audit_event` call site except one**.
- **CSRF.** `http.NewCrossOriginProtection` is wired; no cross-site POST has ever been
  fired at a mutation endpoint.
- **The CSP, the security headers, and the cookie flags in production mode.**
  Everything either reviewer ran was under `GOEN_INSECURE_COOKIES=1`.
- **`migrations/001_initial_schema.down.sql`** — never read, never run.
- **The constraint suite's own completeness claim.** The schema holds 230 CHECKs, 80
  foreign keys, 60 unique indexes and 39 rule triggers (measured from `pg_constraint`,
  `pg_index` and `pg_trigger` — the figures in `CLAUDE.md` had drifted to roughly half
  that until last round). `TestEveryCheckConstraintIsExercised` and its siblings claim
  every one is covered, deriving the requirement from the catalog rather than a list.
  **That claim has never been verified by deleting a constraint and watching a test go
  red** — which is exactly how the first schema suite was found to be reporting 48
  green subtests over 65 constraints, 48 of which could be removed with no failure.
- **`assets/` and the versioned asset handler**, the seed generator, `.golangci.yml`,
  `.claude/hooks/`, `.squawk.toml`, `make image`/ko, `govulncheck`.
- **Any performance claim.** Every number in `CLAUDE.md` (106 ms → 7.7 ms for the
  `committed_orders` view, 136 ms for co-purchase, 60 ms at 10k products) is unverified.

## The four failure modes this repository actually has

Every round has found at least one of each. Assume they are still present.

1. **A test that asserts the defect.** Three were found last round — the TOTP
   re-enrolment, the staff promotion, and the failed refund each had a test encoding
   the bug as the expected result, so each fix had to change a passing test. When you
   find suspicious behaviour, **check whether a test blesses it** before assuming it is
   unintended.
2. **A guard satisfied by something other than what it checks.** Two of the mechanised
   guards were matching a bare identifier against a whole-repo corpus, so a dead
   `order_access_grants.note` passed on the strength of `order_events.note` — 31 of 511
   columns passed that way. There are ~30 more guards (`internal/db/*_test.go`,
   `internal/i18n/hardcoded_test.go`, `internal/ui/pages/fields_test.go`, the
   accessibility rules in `scripts/check-layout.mjs`). **Two of two audited by mutation
   were broken.** Assume more are. The ones matching identifiers against a whole-repo
   corpus are the ones to check first.
3. **A comment that outran its code.** Found so far: a note claiming
   `product_search_documents` had exactly one writer (it had none), `Store.Remove`
   claiming "another admin does this" (no caller), `BaseURL` documented as having no
   default while defaulting, a two-connection pool comment that never applied because
   `Config()` returns a copy, "the result is cached for a year" describing the client
   cache, and a claim that `admin` is a member of `store` that `pg_auth_members`
   refutes. **A claim of enforcement is not enforcement.**
4. **Every allowlist entry is a claim that somebody looked.** There are several, each
   with written reasons. Pick the ones whose reasoning you find least convincing and
   check whether the entry is still true.

## How to report

Follow `.claude/rules/review-process.md`, which this project holds itself to:

- **Form your findings from the code before reading any summary or disposition.**
- Every finding gets a **file:line**, a **concrete failure scenario** (inputs → wrong
  output), and a severity.
- If a finding's mechanism is wrong but the point stands, say both.
- Rank most severe first. An empty list is a legitimate result; padding it is not.
- If you mutate a test to check whether it is a real lock, **record the transcript** —
  the mutation, that it provably applied, the red output, the restore. A mutation that
  fails to compile is not a red test.

Explicitly tell me at the end:

1. What you **verified by running** (and what the command printed).
2. What you **read but could not verify**.
3. What you **did not look at at all**.

That third list is the most useful thing you can give me, and it is the one a review
usually leaves out. The list in this document came from the last reviewer answering
it honestly, and it is what made this round's scope obvious.
