# Contributing to goen

goen has one maintainer, who reviews every submission before it lands.

## What goen is

goen is a full-stack e-commerce application in Go for a Taiwanese 3C shop. It
is one binary that serves the storefront, the customer account and the back
office, over PostgreSQL, paying at Stripe and filing 統一發票 through 綠界. It
is a demonstration and reference project, not a hosted service.

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
- **The design system is vendored, not authored here.** `assets/css/ds/` is
  fixed upstream and re-vendored; `assets/css/app/app.css` owns page composition
  only. There is no CSS build, no JavaScript build, and no client framework.

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

## Run the tests

```sh
make test              # unit and handler tests, race-enabled, shuffled
make lint              # golangci-lint at the version the Makefile pins
make test-integration  # the schema conformance suite, needs Docker
```

### The gate

`make verify` is what CI runs on every pull request, and `main` accepts nothing
that fails it. It formats, regenerates templ and sqlc and compares, vets, lints,
checks for unreachable code, builds under both build tags, and runs the race
tests. `make verify-all` adds the database suite and the vulnerability scan.

Two tools have to be on `PATH`, pinned at the top of the `Makefile`:
`golangci-lint` and `squawk`. Every other tool is fetched by `go run` at its
pinned version. `make check-layout` additionally needs Chrome and a running
server; it drives every route in a real browser and asks the accessibility
questions only a browser can answer.

Run the gate unpiped and report its exit status. A pipe reports the status of
its last command, which has read a red gate as green here before.

`main` is protected by `.github/branch-protection.json`: no force-push, no
deletion, a pull request with one review, and all three CI jobs green. GitHub
refuses to enforce a ruleset on a private free-plan repository, so until the
repository is public that file is the policy and the maintainer applies it by
hand.

## Change it

- Package by feature under `internal/<feature>/`: types, handlers, store,
  queries and tests together. There is no `services`, `repositories`, `handlers`
  or `models` directory, and a hook refuses to create one.
- `internal/db` and every `*_templ.go` are generated. Edit the `.sql` or
  `.templ` source and run `make sqlc` or `make gen`; the gate compares the
  output against a fresh generation.
- A test that guards a guarantee is a lock, and a lock nobody has watched fail
  is a hope. Plant the defect it exists to catch, watch it go red for the stated
  reason, restore, and say so in the pull request.
- Comments carry the reason a reader needs to not turn the line into a defect —
  a statute, a provider quirk, a lock ordering — and nothing else. No history.
- Commits and pull requests carry no AI attribution trailer.

## Report a security problem

Through [GitHub's private advisory form](https://github.com/Koopa0/goen/security/advisories/new),
never a public issue. See [SECURITY.md](.github/SECURITY.md).
