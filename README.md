# goen

A Traditional-Chinese 3C storefront, built as one Go binary: `net/http`,
server-rendered [templ](https://templ.guide), PostgreSQL, and htmx for
fragment updates.

The name has three layers. **Go** is the language behind it. **ご縁** (*go-en*)
is the Japanese word for the connection between people and things. **五円**,
the coin left at Japanese shrines, is its homophone and its good omen. All
three say the same thing: buying電子產品 should not be luck, it should be a
meeting someone arranged well.

## Status

Early. The 關於我們 and 聯絡我們 pages, the site header and footer, the contact
form, and the newsletter signup are implemented. The storefront itself — home,
listing, product pages, cart, checkout — is not yet built, and the routes the
navigation points at answer with the not-found page until they are.

## Running it

Requires Go 1.26.5, Docker, and
[golang-migrate](https://github.com/golang-migrate/migrate).

```sh
cp .env.example .env
make db-up        # PostgreSQL 18 on 127.0.0.1:5433
make migrate-up
make run          # http://127.0.0.1:9700
```

`/about` and `/contact` are the pages worth looking at.

Configuration is environment-only:

| Variable | Purpose | Default |
|---|---|---|
| `GOEN_DATABASE_URL` | PostgreSQL connection string | required |
| `GOEN_ADDR` | Listen address | `127.0.0.1:9700` |
| `GOEN_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |

There is no default database URL on purpose: a missing one stops the binary
rather than letting it reach some other database.

## Verifying it

```sh
make verify    # fmt-check → sqlc-check → vet → lint → test-race
```

A pass covers what those stages actually inspect. Browser behaviour, layout at
each breakpoint, and anything that needs a real PostgreSQL are checked
separately and are not claimed by this gate.

## How it is built

Package by feature under `internal/`. Every write is a plain form that works
with scripting disabled and redirects after a successful POST; htmx only
changes what comes back. The CSS is the koopa.dev design system vendored under
`assets/css/ds/` — plain CSS with `oklch` tokens, no build step — with page
composition in `assets/css/app/app.css`.

Conventions, the decisions behind them, and the mistakes worth not repeating
are in [CLAUDE.md](CLAUDE.md).

## License

Not yet chosen.
