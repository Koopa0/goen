# goen

English | [繁體中文](README.zh-TW.md)

[![verify](https://github.com/Koopa0/goen/actions/workflows/verify.yml/badge.svg)](https://github.com/Koopa0/goen/actions/workflows/verify.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/Koopa0/goen)](go.mod)
[![Apache-2.0](https://img.shields.io/badge/Apache--2.0-blue)](LICENSE)

goen is an online shop written in Go: a storefront for the people who buy, and a
back office for the people who run it. It is a reference project, not a shop.

The demo at [goen.koopa0.dev](https://goen.koopa0.dev) takes payment through
Stripe in test mode: use card **4242 4242 4242 4242**, any future expiry date and
any CVC. No real money moves.

![English storefront with product categories and recommended products](assets/readme/storefront.en.png)

## How it is built

- **HTML from the server.** Pages are templ components, and every write is a
  plain form that works with scripting off. htmx, one vendored file, changes what
  comes back, never whether the write happens. There is no JavaScript or CSS build.
- **The standard library first.** Routing is `net/http`. Queries are hand-written
  SQL that sqlc compiles to Go over pgx. There is no web framework and no ORM.
  Static files are embedded in the binary, each URL carrying a digest of its file.
- **The database guards money and stock.** Storefront and back-office requests
  run in separate pools, each as its own PostgreSQL role. Neither role may write
  payments, refunds, stock or ledgers directly; those change only inside
  `SECURITY DEFINER` functions that check their own rules.
- **Paid means Stripe said so.** Checkout is Stripe's hosted page. An order is
  paid on a signature-verified webhook, never on the browser coming back, because
  anyone can request that URL. The session expires with the stock hold.
- **Side effects are recorded first.** Mail is written to an outbox in the same
  transaction as the fact it reports, and delivered at least once. An e-invoice
  request is stored before it leaves for the provider.
- **Money and time each have one package.** Amounts are integer cents, printed by
  `internal/money` so a page and a letter cannot disagree. Times print on the
  shop's clock, Asia/Taipei, through `internal/shoptime`, because the shipped
  image sets no time zone; a test rejects a time formatted anywhere else.
- **Two languages, one place.** Every sentence goen says is declared once in
  `internal/i18n`, Traditional Chinese and English together, and a string missing
  either stops the program at start-up. Product copy stays as the shop wrote it.
- **Taiwan, as law and practice require.** Returns follow the seven-day right to
  cancel in 消保法 §19, counted from the day after delivery. 統一發票 are issued
  through ECPay. Convenience-store pickup is chosen on ECPay's store map.
- **One schema file.** `migrations/001` is the whole schema. It is amended in
  place while no real shop's data exists; from the first production deploy it is
  frozen, and every change becomes a new migration.

## Run it locally

You need Go 1.27, Docker and `psql`.

```sh
cp .env.example .env
make db-up        # PostgreSQL on 127.0.0.1:5433
make migrate-up
make db-seed      # the forty-product sample catalogue
make run          # http://127.0.0.1:9700
```

With `.env` as copied, mail goes to the log, and Stripe, ECPay and Google sign-in
are off. `.env.example` lists what each one needs. Testing and contributing are
in [CONTRIBUTING.md](CONTRIBUTING.md).

## Limits

The demo is not a shop: the catalogue is sample data, cards are test cards, and
nothing ships. goen is built to run as one process against one PostgreSQL; its
rate limits are kept in memory.

## License

[Apache License 2.0](LICENSE). Report a security problem through
[SECURITY.md](.github/SECURITY.md).
