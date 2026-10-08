# Contributing to goen

goen has one maintainer, who reviews every submission and merges it once CI is green.

## What goen is

goen is a full-stack e-commerce application in Go. It is one binary that
serves the storefront, the customer account and the back office, over
PostgreSQL, paying at Stripe and filing 統一發票 through 綠界. A public demo
runs at [goen.koopa0.dev](https://goen.koopa0.dev);
[deploy/demo](deploy/demo/README.md) is a reference deployment with a nightly
restore.

Four boundaries hold, and each is enforced in the tree rather than by
convention:

- **Every mutation is a plain form.** A write is a `<form method="post">` that
  works with scripting off, answers `303 See Other` so a reload cannot resubmit,
  and re-renders at `422` with the submitted values and `aria-invalid` on each
  control it refused. htmx changes what comes back, never whether the write
  happens. `TestEveryFormWorksWithScriptingOff` holds it.
- **The database enforces what it can.** Money, stock and ledgers are writable
  only through `SECURITY DEFINER` functions; the application roles hold no
  direct write on those tables, and the conformance suite derives what it must
  cover from the system catalogue. A rule added without a test fails the build.
- **Chrome follows the reader; content follows the shop.** Every sentence goen
  itself says lives in `internal/i18n`, declared once with both languages.
  Product copy is translated only where the shop has written a translation.
  `TestNoChromeStringIsHardCoded` refuses a Han literal anywhere else.
- **All of the CSS is authored here.** `assets/css/app/base.css` carries the
  tokens, the element defaults and the shared primitives; `assets/css/app/app.css`
  owns the storefront and `assets/css/app/admin.css` the back office, which does
  not link app.css; each is linked after base.css, so a value set in both is
  settled by source order. A rule for a class both surfaces render is kept in
  each of them. There is no CSS build, no JavaScript build, and no client
  framework.

## Build and run it

You need Go 1.27, Docker, and `psql`.

```sh
cp .env.example .env
make db-up        # PostgreSQL in Docker on 127.0.0.1:5433
make migrate-up
make db-seed      # without a catalogue the storefront is correct and empty
make run          # http://127.0.0.1:9700
```

`migrations/001` is amended in place while nothing is deployed, so after pulling
a change to it run `make db-reset`, which rebuilds and re-seeds. The symptom of
not doing so is a back-office page answering 500 for a column that exists in
the file and not in your database.

`make run` runs `make schema-drift` first and refuses to start against a
database that no longer matches `migrations/`.

## Run the tests

```sh
make test              # unit and handler tests, shuffled (make test-race adds the detector)
make lint              # golangci-lint at the version the Makefile pins
make test-integration  # the schema conformance suite, needs Docker
```

### The gate

`make verify` is what CI runs on every pull request, and `main` accepts nothing
that fails it. It formats, regenerates templ and sqlc and compares, vets, lints,
checks for unreachable code, builds under both build tags, and runs the race
tests. `make verify-all` adds the database suite and the vulnerability scan.

Three tools have to be on `PATH`: `golangci-lint` and `squawk`
(`npm i -g squawk-cli@<pinned>`), both pinned at the top of the `Makefile`, and
Node 24, the version CI installs, for the browser-script tests. Every other tool
is fetched by `go run` at its pinned version.

`make check-layout` drives every route in a real browser. It needs Node 22 or
newer, `curl`, `openssl`, network access to fetch axe-core, Chrome or Chromium
(set `CHROME` when it is not found) and a running server: `make run` in one
shell, `make check-layout` in another. CI runs it as the `layout` job.

To look at pages rather than measure them, run the `screenshots` workflow by
hand: `gh workflow run screenshots.yml --ref <branch> -f pages='/deals@375'`
shoots that branch, which has to contain the workflow (merge `main` into an older
one). The entry syntax is at the top of `scripts/screenshots.mjs`; fetch the
result with `gh run download <run id> -n screenshots`.

For a delivery refusal, use `data=layout` and
`pages='/admin/orders/{PLACED_ORDER}@320@delivery-refused'`. The explicit flag
submits the fixture's production address form with an invalid absent pickup
field and captures its actual localized HTTP 422 document. The recipe refuses
to capture when the alert is missing or the visible draft or saved summary
changes. Omit the flag for its baseline GET; add `@en` and `@text200` for English
and 200% text, or use `@1440` for desktop. This state needs the absent-control
refusal repair; a branch without it fails rather than substituting a normal GET.

goen targets WCAG 2.2 level AA. The pinned axe rules select `wcag2a`, `wcag2aa`,
`wcag21a`, `wcag21aa` and `wcag22aa`; serious or critical WCAG findings gate,
while best-practice findings remain advisory. `scripts/axe-baseline.json`
records accepted findings, and the run prints the replacement when they move.
The log lists executed WCAG rules once, selected rules that never ran, excluded
rules and incomplete checks for manual review. With axe-core 4.13, five
experimental rules (css-orientation-lock, label-content-name-mismatch,
p-as-heading, table-fake-caption and td-has-header) and two deprecated rules
(aria-roledescription and audio-caption) stay disabled. The two WCAG 2.1 rules
in that experimental set therefore need manual review; selecting WCAG 2.1 tags
does not enable them. Automated success does not establish complete conformance,
screen-reader acceptance or real Windows High Contrast behavior.

Every route is also measured at 320px and at 200% text (WCAG 1.4.4, 1.4.10):
no sideways scroll, and no text cut by a clip or the edge of the screen.
`scripts/reflow-baseline.json` lists the routes that still fail; like the axe
baseline it can only shrink.

Run the gate unpiped and report its exit status: a pipe reports the status of
its last command, which has read a red gate as green here before.

`main` is protected by a GitHub ruleset; `.github/branch-protection.json` is its
reviewed import artifact, and the live ruleset is the authority. The one
maintainer merges once the required checks pass.

## What goen assumes

**One instance.** goen is built to run as a single process against one
PostgreSQL. With more than one:

- The rate limiters are in memory, so each instance counts on its own and a
  client's allowance is multiplied by the number of instances.
- The background loops (retention sweeps, invoice reconciliation, the outbox
  worker) run in every process. The outbox claims with `SKIP LOCKED` and the
  co-purchase rebuild takes an advisory lock, so neither does its work twice;
  the sweeps only repeat idempotent deletes.
- The pools are constants sized for one process (25 + 10 + 2 connections, inside
  PostgreSQL's default `max_connections` of 100). Each extra instance adds that
  many again.

If a shop needed more than one, the first change would be a shared rate limiter,
then pool sizes read from the environment. Neither is built while goen runs as
one.

**Migration 001 is amended in place only while no real shop's data exists.**
The demo restores a snapshot nightly, so editing `001` costs nothing there. From
the first production deploy `001` is frozen: every change becomes a new numbered
migration, and a function it changes is re-created in that migration with
`CREATE OR REPLACE`. Until then, run `make db-reset` after pulling a change.

**Uploads live in PostgreSQL.** Product images are stored in `media_objects`,
re-encoded and capped at 8 MB each and 2400 px on the longest side. That keeps
one thing to back up and no object store to run, and it suits a small
catalogue. It does not suit a large one: every image adds to the database's size,
backups and restore time, and a page's images are read through the same pool as
its orders. A shop with thousands of photographs would move them to an object
store before anything else.

## Change it

- Package by feature under `internal/<feature>/`: types, handlers, store,
  queries and tests together. There is no `services`, `repositories`, `handlers`
  or `models` directory.
- `internal/db` and every `*_templ.go` are generated. Edit the `.sql` or
  `.templ` source and run `make sqlc` or `make gen`; the gate compares the
  output against a fresh generation.
- A test that guards a guarantee is a lock, and a lock nobody has watched fail
  is a hope. Plant the defect it exists to catch, watch it go red for the stated
  reason, restore, and say so in the pull request.
- Comments carry the reason a reader needs to not break the line (a statute, a
  provider quirk, a lock ordering) and nothing else. No history.
- Commits carry no `Co-authored-by` trailer.

## Changes that need the maintainer first

Open an issue before touching any of these; each one is a place where a small
edit becomes a payment, a privilege or a legal term:

- a `SECURITY DEFINER` function's privilege, a role's grant, and the `DO` block
  that ends `migrations/001`;
- the Stripe session's `payment_method_types` pin, its `ExpiresAt` bound to the
  stock hold, and webhook attribution from goen's own payment row;
- the statutory terms in `internal/site/policies.go` (消保法 §19);
- the shop's content: product names, descriptions, FAQ and policy prose.

## Report a security problem

Through [GitHub's private advisory form](https://github.com/Koopa0/goen/security/advisories/new),
never a public issue. See [SECURITY.md](.github/SECURITY.md).
