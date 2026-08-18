# goen

goen is a Traditional-Chinese storefront for 3C goods (computers,
communications, and consumer electronics), built full-stack in Go. One Go binary
serves the site: the HTTP layer is `net/http`, the pages are rendered on the
server with [templ](https://templ.guide), [htmx](https://htmx.org) swaps
fragments, and PostgreSQL holds the data. There is no separate front-end
framework and no JavaScript runtime — the stack is Go from the request to the row.

The name has three layers. **Go** is the language behind it. **ご縁** (*go-en*)
is the Japanese word for the connection between people and things. **五円**, the
coin left at Japanese shrines, is its homophone and its good omen. All three say
the same thing: buying electronics should not be luck, it should be a meeting
someone arranged well.

## Status

The shop works end to end. A visitor can browse the catalogue, compare products,
put something in a cart, check out as a guest or as an account, pay at Stripe,
track the parcel, ask for a return, and register a warranty — and the shop can
run all of it from `/admin` behind a second factor.

What is not built, and what is deliberately refused, is in
[`docs/roadmap.md`](docs/roadmap.md) — one list, in one place, because a second
one drifts. This file has already carried a stale claim of absence: it said
issuing 統一發票 invoices was the one gap in the commerce path, months after
`internal/invoice` shipped against 綠界's published test environment.

This repository is a demonstration and reference project. It is not affiliated
with any company and is not an officially supported product.

## Features

### Application

The running server provides:

- **Storefront** — home, category listing and search with facets, product detail
  with variant selection, `/compare` for two to four products side by side,
  `/deals` and timed `/s/{slug}` campaigns, reviews and customer Q&A.
- **The pages around it** — 關於我們, 聯絡我們, `/faq` and the policy documents.
  `/faq` and `/shipping` are read from the same rows the back office edits and
  the till prices from; a page that restates a fee eventually contradicts it.
- **Cart and checkout** — a cookie cart that works for guests, coupons, store
  credit, delivery by address or 超商取貨 with 離島 surcharges, and an order
  placed in one transaction that also holds the stock.
- **Payment** — Stripe hosted Checkout. Only the signature-verified webhook marks
  an order paid; the customer's return to the success URL is a page, not a fact.
- **Account** — sign-in, registration, password reset, a proved email address, an
  address book, orders, returns, warranty registration, wishlist, points and
  membership tier. A guest who lost the cookie finds their order at
  `/orders/find`.
- **Back office** at `/admin`, guarded by TOTP step-up — catalogue, stock ledger,
  orders, shipping methods and zones, coupons, campaigns, store credit, reviews,
  questions, customer messages, the newsletter, staff, taxonomy, FAQ, reports,
  worker health, and an append-only audit trail of who did what.
- **Transactional email** through an outbox worker, so a message is never sent
  before its transaction commits or lost when the process dies mid-send. A
  double-opt-in newsletter sits on the same queue at a lower priority.
- **Traditional Chinese and English**, chosen by the visitor and stamped onto
  `<html lang>` before the first byte — in the back office as well as the shop.
  Chrome follows the reader; authored content follows what the shop has actually
  translated, and says so where it has not.
- Every mutation is a plain form that works with scripting off, answers
  `303 See Other` so a reload cannot resubmit, and re-renders at `422` with the
  submitted values and `aria-invalid` on each control it refused.
- Liveness (`/healthz`) and readiness (`/readyz`) probes, `/sitemap.xml`,
  `robots.txt`, and JSON-LD on the product page.
- Content-addressed static assets, and uploaded images re-encoded from pixels
  and served by digest.

### Data layer

The PostgreSQL schema models the full commerce domain, with integrity enforced
in the database rather than only in application code. Every table has a door in
both directions — a table the application writes and never reads, or reads and
never writes, fails the build.

- **Catalogue** — products, variants as the unit of stock (SKU), options and
  option values, images, specifications, brands, a category tree, reviews, and
  customer questions and answers.
- **Inventory** — an append-only movement ledger and a reservation lifecycle
  (hold, consume, release) with a safety-stock floor, so a hold means the stock
  is genuinely gone and an abandoned checkout returns it.
- **Cart and checkout** — carts (guest or account), cart items, and checkout
  attempts.
- **Orders** — orders carrying frozen line snapshots (name, SKU, unit price),
  delivery details separated for privacy, shipments and shipment lines, an event
  log, and a per-day order-number counter.
- **Payments** — the Stripe hosted-Checkout lifecycle with intended and captured
  amounts kept as distinct facts, refunds bounded to what was captured, and a
  webhook de-duplication ledger that is written in the same transaction as the
  effect it claims.
- **Promotions** — coupons and their redemptions, timed sale campaigns, the
  site-wide promotional strip, and the home page's hero queue.
- **Invoicing** — per-order 發票 preferences, and the 統一發票 and 折讓
  documents issued against them through 綠界's B2C API. Absent credentials issue
  nothing and say so; half a configuration refuses to start.
- **Returns and warranty** — return requests bounded by what SHIPPED and
  received line by line, with per-unit warranty registration bounded by what was
  DELIVERED: cover starts when the goods reach somebody, so a clock started at
  the warehouse door is short by the time in transit.
- **Store credit and loyalty** — two append-only ledgers, each read through a
  view so no page can compute a balance its own way.
- **Accounts** — users, sessions, password-reset tokens, email verifications, an
  address book, and staff TOTP credentials encrypted at rest.
- **Messaging** — an outbox with priorities and delivery attempts, newsletter
  subscribers and issues, restock notifications, and contact messages.
- **Audit** — an append-only record of who did what, writable only through a
  function that runs in the caller's own transaction.

### Integrity and safety

The schema carries 241 `CHECK` constraints, 80 foreign keys, 61 unique indexes,
and 39 rule triggers (beside 16 that only keep `updated_at` truthful), and it
holds several properties that application code alone cannot guarantee:

- **A single writer for money and stock.** A customer-facing request runs as
  `store` and the back office as `admin`; both have their direct writes to
  payments, refunds, stock, ledgers, and the audit log revoked. Those writes
  happen only through 19 `SECURITY DEFINER` functions, so there is no second path
  that can corrupt them. What each role may write is derived from the catalogue
  by a test, never from a list somebody keeps up to date.
- **Cross-row invariants are triggers that lock first.** A refund may not exceed
  its capture, stock may not oversell, an allowance may not exceed its invoice —
  each locks its aggregate root before it reads, so two concurrent writers cannot
  both pass a test the other is about to invalidate.
- **A conformance suite that cannot lie.** The integration tests run against a
  real PostgreSQL through testcontainers and derive what must be covered from the
  system catalogue, so a constraint added without a test fails the build. The
  concurrency guards are proven by deterministic race tests and by mutation
  testing.

## Technology stack

| Concern     | Choice                                                            |
| ----------- | ----------------------------------------------------------------- |
| Language    | Go 1.26                                                           |
| HTTP        | `net/http` with method-based routing; no web framework            |
| Templates   | templ (compiled, server-rendered)                                 |
| Enhancement | htmx, vendored; the only admitted client dependency               |
| Database    | PostgreSQL via `pgx/v5` and `pgxpool`                             |
| Queries     | sqlc-generated code from `.sql` files                             |
| Migrations  | golang-migrate, numbered SQL                                      |
| CSS         | A vendored design system in plain CSS with `oklch` tokens; no build step |
| Payments    | Stripe hosted Checkout via `stripe-go`; never Elements            |
| Container   | ko, base image pinned by digest                                  |
| Tests       | standard library, go-cmp, and testcontainers-go                   |

## Repository layout

```
cmd/goen/          Entry point and wiring: routes, middleware, configuration
assets/            Embedded static files and the versioned asset handler
internal/
  <feature>/       Feature packages: types, handlers, store, queries, tests
  db/              sqlc-generated code (never edited by hand)
  ui/              Document shell, page bodies, and inline icons
migrations/        Numbered SQL migrations
seed/              The development catalogue (make db-seed)
scripts/           check-layout.mjs, the browser conformance check
docs/decisions/    Read models and design approaches, with the measurements
docs/reviews/      Schema review prompts and their dispositions
```

The project is organised by feature, not by technical layer. There is no
`services`, `repositories`, `handlers`, or `models` directory.

## Getting started

### Prerequisites

- Go 1.26.6 or later
- Docker, for PostgreSQL and the integration tests
- `psql`, for the seed and the layout check's fixtures

`golang-migrate`, `sqlc`, `ko` and `govulncheck` are pinned in the `Makefile` and
run through `go run pkg@version`, so there is nothing to install: they are not
`tool` directives because none of them generates code this module compiles, and
adding one takes the module graph — and every vulnerability scan of it — with it.
Two things do have to be on `PATH`, and only for `make verify`: `golangci-lint`
and `squawk`, each version-checked by the target that uses it. `make check-layout`
additionally needs Chrome and Node.

### Configuration

Configuration comes from the environment only. Copy the example file and adjust:

```sh
cp .env.example .env
```

| Variable                     | Purpose                                                                | Default                        |
| ---------------------------- | ---------------------------------------------------------------------- | ------------------------------ |
| `GOEN_DATABASE_URL`          | The storefront pool. Connects, then `SET ROLE store`                   | required                       |
| `GOEN_ADMIN_DATABASE_URL`    | The back-office pool. Connects, then `SET ROLE admin`                  | `GOEN_DATABASE_URL`            |
| `GOEN_ADDR`                  | Listen address                                                         | `127.0.0.1:9700`               |
| `GOEN_BASE_URL`              | The origin Stripe returns to and email links point at                  | `http://` + `GOEN_ADDR`        |
| `GOEN_LOG_LEVEL`             | `debug`, `info`, `warn`, `error`                                       | `info`                         |
| `GOEN_INSECURE_COOKIES`      | `1` drops `Secure` and the `__Host-` prefix. Development only          | unset — cookies are secure     |
| `GOEN_STRIPE_SECRET_KEY`     | Empty still sells; the payment page says 金流尚未啟用                    | empty                          |
| `GOEN_STRIPE_WEBHOOK_SECRET` | Required whenever a secret key is set                                  | empty                          |
| `GOEN_TOTP_KEY`              | Encrypts staff second-factor secrets at rest. Empty disables enrolment | empty                          |
| `GOEN_SMTP_ADDR`             | `host:port`. Empty writes mail to the log instead of sending it        | empty                          |
| `GOEN_SMTP_FROM`             | Envelope sender                                                        | `goen <no-reply@goen.example>` |
| `GOEN_SMTP_USER`             | SMTP username, when the relay wants one                                | empty                          |
| `GOEN_SMTP_PASSWORD`         | SMTP password                                                          | empty                          |

Three of these fail in ways worth naming:

- `GOEN_DATABASE_URL` has no default on purpose — a missing one stops the binary
  rather than letting it reach some other database.
- A Stripe secret key **without** a webhook secret refuses to start. That
  combination would take money over an endpoint nothing authenticates.
- `GOEN_BASE_URL` falls back to `http://` plus the listen address, which is right
  in development and wrong the moment anything is deployed: Stripe would send the
  customer back to `127.0.0.1` and every emailed link would point there. Set it.

See `.env.example` for the connection-role guidance.

### Build and run

```sh
make db-up        # start PostgreSQL 18 in Docker on 127.0.0.1:5433
make migrate-up   # apply migrations
make db-seed      # load the development catalogue
make run          # generate templates, then serve on http://127.0.0.1:9700
```

`make db-seed` is worth running: without a catalogue the storefront is correct
and empty. Stripe is optional — with no key the site still sells and the payment
page says so.

### Common tasks

```sh
make verify       # the gate: fmt-check, sqlc-check, vet, lint, and race-enabled tests
make verify-all   # verify, plus the database conformance suite and govulncheck
make sqlc         # regenerate internal/db from the .sql files
make check-layout # layout and accessibility conformance in a real browser
make db-reset     # drop and rebuild the development database from 001, then seed
make image        # build a container image with ko
```

Run `make` targets from the repository root; the full list is in the `Makefile`.

## Testing

- `make test` and `make test-race` run the unit and handler tests.
- `make test-integration` runs the schema conformance suite against a real
  PostgreSQL through testcontainers. It needs Docker.
- `make check-layout` drives a headless Chrome over every page route at several
  widths and measures boxes — a screenshot cannot do this job, because Chrome on
  macOS will not open a window under ~500px. It also asks the questions only a
  browser can decide: one `h1` per page, no skipped heading level, an `alt`
  attribute on every image, an accessible name on every control. It needs Chrome
  and `make run` in another shell.
- Tests use the standard library and go-cmp. There is no mocking framework, and
  the database is never mocked.

## Design

Every write is a plain form that works with scripting disabled and redirects
after a successful POST; htmx only changes what comes back. The CSS is the
koopa.dev design system, vendored under `assets/css/ds/` as plain CSS with
`oklch` tokens and no build step, with page composition in
`assets/css/app/app.css`.

The conventions, the decisions behind them, and the deliberate departures from
the imported rules are documented in [CLAUDE.md](CLAUDE.md). The schema review
history and the line-by-line dispositions are under `docs/reviews/`.

## Contributing

Issues and pull requests are welcome. Before opening a pull request, run
`make verify-all` and make sure it passes. New database rules must come with a
conformance case; the suite fails otherwise.

## License

goen is released under the Apache License 2.0. See the [LICENSE](LICENSE) file
for the full text.
