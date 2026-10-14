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
PGOPTIONS='-c goen.catalogue_seed=development' make db-seed
make run          # http://127.0.0.1:9700
```

Skip the catalogue seed to run an empty shop. The seed accepts only an explicit
development marker; it does not infer the deployment environment.

`migrations/001` is amended in place while nothing is deployed, so after pulling
a change to it run `PGOPTIONS='-c goen.catalogue_seed=development' make db-reset`, which rebuilds and re-seeds. The symptom of
not doing so is a back-office page answering 500 for a column that exists in
the file and not in your database.

`make run` runs `make schema-drift` first and refuses to start against a
database that no longer matches `migrations/`.

## Run the tests

```sh
make test              # unit and handler tests, shuffled (make test-race adds the detector)
make lint              # golangci-lint at the version the Makefile pins
make test-integration  # every package's integration tests on a real PostgreSQL, needs Docker
```

To run one test, name it: `go test -run '^TestPercentCouponDoesNotOverflowAnInRangeSubtotal$' ./internal/cart`
for a unit test, and add `-tags=integration` for one that needs the database
(Docker must be running; each package starts its own PostgreSQL). A browser test
skips unless Chrome is available; see Tests.

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
`pages='/admin/orders/{PLACED_ORDER}@1440@delivery-refused,/admin/orders/{PLACED_ORDER}@1440@en@delivery-refused,/admin/orders/{PLACED_ORDER}@320@text200@delivery-refused,/admin/orders/{PLACED_ORDER}@320@en@text200@delivery-refused'`.
A single entry such as `/admin/orders/{PLACED_ORDER}@320@delivery-refused` also
works. The explicit flag submits the fixture's production address form with an invalid absent pickup
field and captures its actual localized HTTP 422 document. The recipe refuses
to capture when the alert is missing or the visible draft or saved summary
changes. After saving the refusal screenshot, it checks a fresh order GET
restores the saved fields and records that recovery in the manifest. Omit the
flag for its baseline GET; add `@en` and `@text200` for English
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
`CREATE OR REPLACE`. The re-created function restates `SECURITY DEFINER` (when it
is one) and `SET search_path = pg_catalog, public, pg_temp` in its header, because
`CREATE OR REPLACE` resets whatever the header leaves out;
`internal/db/functionheaders_test.go` enforces it. Until then, run `PGOPTIONS='-c goen.catalogue_seed=development' make db-reset` after pulling a change.

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
- `internal/db/{db,models,query.sql,orders_export.sql}.go` and every
  `*_templ.go` are generated. Edit the `.sql` or `.templ` source and run
  `make sqlc` or `make gen`; the gate compares the output against a fresh
  generation. `tx.go`, `pgerror.go` and `like.go` in that package are
  hand-written. A package's first `query.sql` is also added to the `queries:`
  list in `sqlc.yaml`. A query is declared in the `query.sql` of the package
  that calls it; a query another package needs is reached through the owner, and
  `internal/db/queryowners_test.go` lists the remaining exceptions, which may
  only shrink.
- Package imports are policy, and `internal/repocheck/deps_test.go` is the whole
  of it: vocabulary packages such as `order`, `money` and `shipping` import no
  goen package; a back-office desk is imported only by `cmd/goen`, and reaches
  another desk through an interface it declares; nothing outside
  `internal/admin` and `cmd/goen` imports a back-office package. Each rule's
  reason is printed when it fails.
- Page bodies still live in `internal/ui/pages` and `internal/ui/pages/admin`
  while they move into the features that render them. A new package does not
  import either: put its `.templ` beside its handler, build on
  `internal/ui/layouts` and `internal/ui/components`, and have its store return
  domain values. `internal/repocheck/layering_test.go` lists the existing
  imports, and the list may only shrink.
- A test that guards a guarantee is a lock, and a lock nobody has watched fail
  is a hope. Plant the defect it exists to catch, watch it go red for the stated
  reason, restore, and say so in the pull request.
- Comments carry the reason a reader needs to not break the line (a statute, a
  provider quirk, a lock ordering) and nothing else. No history.
- Credit a human co-author with a `Co-authored-by: Name <email>` trailer on a
  line of its own. AI attribution is not accepted: no trailer naming an AI tool, no
  `Generated with` line, robot-emoji line or session link.
  `commit-attribution` refuses it in commit messages; the patterns are in
  `scripts/check-commit-attribution.sh`. It also refuses a trailer whose name
  begins with a tool's name, such as `Claude`, whoever the person is.
  Automation lines such as Dependabot's `Signed-off-by` are accepted.

### Tests

- Name a test after the unit it exercises: `TestParse` for a function, the
  type's name for a type, the type and the method joined by an underscore for a
  method ([Google decisions: underscores](https://google.github.io/styleguide/go/decisions#underscores)).
  `testing` itself asks only for `Test` and a name that does not start with a
  lower-case letter ([testing](https://pkg.go.dev/testing)); the rest is
  convention. A test of something that is not a Go identifier takes the name of
  that thing: a SQL object, or a repository rule such as `TestDependencies`.
- Cases that share logic are `t.Run` subtests or table rows, with short
  identifier-like names and no `/` ([subtest names](https://google.github.io/styleguide/go/decisions#subtest-names),
  [table-driven tests](https://go.dev/wiki/TableDrivenTests),
  [the blog post](https://go.dev/blog/subtests)). Cases that need different
  logic are separate functions, named after the unit plus one to three words of
  scenario
  ([table tests or several functions](https://go.dev/wiki/TestComments#table-driven-tests-vs-multiple-test-functions)).<!-- named-test-exempt: the link is a URL, not a test name -->
  Do not split a test only to give each assertion its own function. The
  sentence that says why goes first in the doc comment, or in a `desc` field the
  failure message prints, not in the name.
- Fail with `Func(input) = got, want want`, and keep going with `t.Error`
  ([useful test failures](https://google.github.io/styleguide/go/decisions#useful-test-failures),
  [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments#useful-test-failures)).
- `TestMigratedTestNames` in `internal/repocheck` holds the packages listed in
  `migratedTestPackages` to the naming rule: a name is at most 40 characters
  after `Test` and, where the package has non-test code, starts with the name of
  a function or type it declares. The 40 is goen's own limit, near the 99th
  percentile of the standard library's test names (42); no style guide gives a
  number. A package joins the list in the pull request that renames its tests,
  and a test that cannot follow the rule goes in `testNameExceptions` with a
  reason. That list only shrinks, and a row for a test that was renamed or now
  follows the rule fails. Other packages are not checked yet.
- Use the external test package (`package foo_test`) unless the test needs
  unexported names or an import cycle forbids it ([test package](https://google.github.io/styleguide/go/decisions#test-package)).
  That default is a goen rule: Google's guide defaults to the same package and
  the standard library uses both.
- Like the standard library, put tests for `foo.go` in `foo_test.go` and
  `foo_integration_test.go`. A topic file covers behavior spanning several source
  files and uses the package's domain words, never an issue or PR. Keep files
  small enough to find things in ([package size](https://google.github.io/styleguide/go/best-practices#package-size)).
- Every integration test file has both `//go:build integration` and the
  `_integration_test.go` suffix (a goen rule). Keep `TestMain` and shared setup
  in `main_integration_test.go`, with no tests; `internal/db`
  (`schema_integration_test.go`), `internal/contact` and `internal/home`
  (`handler_integration_test.go`) keep theirs beside the tests it serves.
- Put a helper shared by several packages in the area's helper package, such
  as `admintest` or `dbtest`.
- A test that drives a real browser has the `_browser_test.go` suffix
  (`_browser_integration_test.go` when it also needs the `integration` tag),
  learns that it may run only through `internal/ui/uitest` (`Browser`,
  `LocalChrome`), and is run by name from the Makefile's `check-layout-run` or
  a workflow. A test may read CSS or JS text only where the bytes are the
  contract, and `internal/repocheck/assetreaders_test.go` lists each one.

## Changes that need the maintainer first

Open an issue before touching any of these; each one is a place where a small
edit becomes a payment, a privilege or a legal term:

- a `SECURITY DEFINER` function's privilege, a role's grant, and the `DO` block
  that ends `migrations/001`;
- the Stripe session's `payment_method_types` pin, its `ExpiresAt` bound to the
  stock hold, and webhook attribution from goen's own payment row;
- the statutory terms in `internal/site/policies.go` (消保法 §19);
- the shop's content: product names, descriptions, FAQ and policy prose.

## Filing an issue

The tracker is for defects and wishes. File a new issue only if all of these
hold:

1. **It is a defect, not a preference.** Say who does what and the wrong result
   they get. Money, stock, tax, personal data and sign-in come first. "Could" is
   not a result.
2. **You checked it against the current `main`.** Give the reproduction output,
   or quote the `file:line` that makes the result unavoidable. With neither,
   put `UNVERIFIED` in the title; it is looked at last.
3. **You searched first**, open and closed issues, including titles that start
   `noise:` or `duplicate:`. New evidence for a known defect is a comment on it.
4. **It is one defect.**
5. **It does not reverse a decision** recorded in this file or in a closed
   issue. To reverse one, file a wish.

Do not file wording or tone preferences that leave the meaning, the promise and
the behaviour unchanged; defence in depth with no way in; performance work with
no measurement; refactors or package-name taste with no behaviour or coupling
cost; a guard that catches no bug class; features for a stage the shop is not at;
or a law cited without a code path.

A claim about a law or standard needs four lines: the obligation, with article
and paragraph; why it binds this shop (seller type, tax status, who the
customers are); the code path that breaches it, with `file:line` and the input
that triggers it; and the cheapest compliant alternative, such as copy or policy
text instead of schema. Without the third line it is a question for the
maintainer, filed as a wish. For WCAG, name the success criterion that fails and
whether it blocks a task.

A small change you can verify needs no issue: a typo, a dead CSS rule, a log at
the wrong level. Open the pull request and put the evidence in its body. For
anything larger, open an issue first.

Labels the maintainer uses: `bug`, `enhancement` and `documentation` for the
kind; `needs-repro`, `needs-owner` and `blocked` for what it waits on;
`lane-schema` and `lane-surface` for the part of the tree it touches;
`polish` for real but cosmetic; `brand` for the brand rollout; `decision` for a
closed issue that records a ruling; `duplicate`; `dependencies`; and
`good first issue`.

## Licensing of contributions

Contributions are accepted under the Apache License 2.0, as its section 5
provides. There is no CLA. `Signed-off-by` is welcome and optional.

## Tools, including AI

You may use any tool, AI included. You are the author and answer for every
line: be able to explain it, and answer review questions in your own words. If a
tool generated a substantial part of a pull request, issue or report, say which
tool and which part in its description. Do not name tools in commit messages or
trailers: the pull request records the disclosure, and the commit names the
people accountable for it. Every issue, pull request and report needs a person
who checked it and will answer for it; one that no person answers for is closed
without detailed review.

The maintainer uses AI tools too, including agents that draft issues, reviews
and code, and is accountable for everything merged.

## How issues are handled

One maintainer, best effort. A pull request that is ready for review usually
gets a first reply within a few days. If a week passes with nothing, comment to
ask. A `needs-repro` issue with no new evidence is closed after 30 days and
reopened when the evidence arrives. From the first release, history on `main` is
not rewritten; mistakes are fixed forward, the only exception being the removal
of personal data.

## Report a security problem

Through [GitHub's private advisory form](https://github.com/Koopa0/goen/security/advisories/new),
never a public issue. See [SECURITY.md](.github/SECURITY.md).
