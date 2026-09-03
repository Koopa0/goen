# goen

A full-stack e-commerce application written in Go.

goen serves the storefront, the customer account and the back office from a
single binary: `net/http` for routing, [templ](https://templ.guide) for
server-rendered pages, [htmx](https://htmx.org) for fragment swaps, and
PostgreSQL for the data. There is no front-end framework and no JavaScript
runtime — the stack is Go from the request to the row.

It is built for a Taiwanese 3C shop, so it carries what that means in practice:
Traditional Chinese and English throughout, 超商取貨 alongside home delivery,
離島 surcharges, 統一發票 filing, and the Consumer Protection Act's rescission
window enforced in the schema rather than in a policy page.

## Status

The shop works end to end: browse, compare, cart, guest or account checkout,
pay at Stripe, track the parcel, request a return, register a warranty — and run
all of it from `/admin` behind a second factor.

Observability, delayed payment methods and a search projection for short CJK
queries are not built. Each is a decision rather than an omission.

## Getting started

You need Go 1.27, Docker, and `psql`.

```sh
cp .env.example .env
make db-up        # PostgreSQL in Docker on 127.0.0.1:5433
make migrate-up
make db-seed      # without a catalogue the storefront is correct and empty
make run          # http://127.0.0.1:9700
```

Stripe is optional. With no key the site still sells and the payment page says
so.

## Configuration

Configuration is environment-only; `.env.example` documents every variable.
`GOEN_DATABASE_URL` is required and has no default, so a missing one stops the
binary rather than letting it reach some other database.

Four combinations refuse to start, each because the alternative is worse than
not running:

| Setting | Refused when |
| --- | --- |
| `GOEN_STRIPE_API_KEY` | set without `GOEN_STRIPE_WEBHOOK_SECRET` — money over an endpoint nothing authenticates |
| `GOEN_TOTP_KEY` | not exactly 32 bytes of hex or base64 — it is a key, not a passphrase |
| `GOEN_SMTP_ADDR` | empty while cookies are Secure — resets would be marked sent and never leave |
| `GOEN_BASE_URL` | not a root HTTPS origin in a production posture — a cleartext reset link leaks its token |

## Development

```sh
make verify       # format, generate, vet, lint, build and race tests
make verify-all   # verify, plus the database suite and govulncheck
make test-integration
make check-layout # layout and accessibility in a real browser
```

`make verify` needs `golangci-lint` and `squawk` on `PATH`; every other tool is
pinned in the `Makefile` and fetched by `go run`. The integration suite runs
against a real PostgreSQL through testcontainers and needs Docker.

## Design

The project is organised by feature. `cmd/goen` is wiring, `internal/<feature>`
holds a feature's types, handlers, queries and tests together, and there is no
`services`, `repositories` or `models` directory.

Three decisions shape most of the code:

- **Every mutation is a plain form that works with scripting off.** It answers
  `303 See Other` so a reload cannot resubmit, and re-renders at `422` with the
  submitted values intact. htmx only changes what comes back, never whether the
  write happens.
- **The database enforces what it can.** 316 `CHECK` constraints, 90 foreign
  keys, 75 unique indexes and 46 rule triggers, with money and stock writable
  only through `SECURITY DEFINER` functions the application role may execute but
  not bypass. The conformance suite derives what it must cover from the system
  catalogue, so a constraint added without a test fails the build.
- **Chrome follows the reader; content follows the shop.** Navigation, labels
  and validation are translated. Product copy is translated only where the shop
  has actually written it, and the site says which is which.

## Contributing

Issues and pull requests are welcome. Run `make verify-all` before opening one.
A new database rule needs a conformance case; the suite fails without it.

## License

Apache License 2.0. See [LICENSE](LICENSE).

## Disclaimer

This is a demonstration and reference project. It is not affiliated with any
company and is not an officially supported product.
