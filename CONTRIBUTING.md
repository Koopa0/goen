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

Configuration comes from the make command line first, then your environment,
then `.env`: `GOEN_DATABASE_URL=<url> make db-reset` acts on `<url>`, not on the
database in `.env`. The Makefile reads `.env` line by line, `NAME=VALUE` only, so
it is not a shell script and its `$` is literal. A test runs the four cases
(`internal/repocheck`, `TestMakefileDotEnvNeverOverridesTheCaller`).

In development mail never leaves your machine: with no `GOEN_SMTP_ADDR` goen
logs each mail in full, links included, and a loopback Mailpit is accepted
without TLS. Neither holds outside development. See "Reading local mail" in the
README, which also covers the owner's first sign-in.

Skip the catalogue seed to run an empty shop. The seed accepts only an explicit
development marker; it does not infer the deployment environment.

After pulling a new migration, run `make migrate-up`. A database built from an
edition of `migrations/001` from before it was frozen cannot be brought forward
by any migration: rebuild it with
`PGOPTIONS='-c goen.catalogue_seed=development' make db-reset`, which drops,
migrates and re-seeds it. Until the database matches, `/readyz` answers 503 and
goen warns at startup that the database is not the schema it was built with
(outside development it refuses to start).

`make run` runs `make schema-drift` first and refuses to start against a
database that no longer matches `migrations/`.

## Run the tests

```sh
make test              # unit and handler tests, shuffled (make test-race adds the detector)
make lint              # golangci-lint at the version the Makefile pins
make test-integration  # every package's integration tests on a real PostgreSQL, needs Docker
```

To run one test, name it: `go test -run '^TestPercentCouponDoesNotOverflowAnInRangeSubtotal$' ./internal/coupon`
for a unit test, and add `-tags=integration` for one that needs the database
(Docker must be running; each package starts its own PostgreSQL). A file
marked `//go:build integration`, such as the Google sign-in callback tests in
`internal/auth`, runs only with that tag: `make test` and `make verify` do not
run it (`make verify` only vets it), and CI runs it in the pull request's
`make test-integration` step. A browser
test skips unless Chrome is available; see Tests.

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

### Packages

A package is judged by its name and by what its callers use together, not by
its size or by layer. The ruling is
[#2184](https://github.com/Koopa0/goen/issues/2184).

1. **The name test.** If one name cannot honestly describe every file in a
   package, the boundary is wrong; if it can, a long file list is not a defect.
   "If you cannot come up with a package name that's a meaningful prefix for the
   package's contents, the package abstraction boundary may be wrong."
   ([Package names](https://go.dev/blog/package-names))
2. **Do not split a package to shorten a directory listing.**
3. **What a caller must use together stays together.** For one feature, the
   handler, store, SQL, view model and templates are one package: "if the user
   must import both packages in order to use either in any meaningful way,
   combining them together is usually the right thing to do"
   ([package size](https://google.github.io/styleguide/go/best-practices#package-size)).
   "Consolidated packages are often preferred over too many too small packages
   that fail to stand on their own."
   ([designing effective interfaces](https://google.github.io/styleguide/go/best-practices#designing-effective-interfaces))
4. **Split out only a concept a caller can use alone, and name it for that
   concept.** "When something is conceptually distinct, giving it its own small
   package can make it easier to use." (package size). Never give it a layer
   name such as `model`, `types`, `views`, `ui`, `util`, `api` or `interfaces`.
   The Go blog: "Packages named util, common, or misc provide clients with no
   sense of what the package contains", and putting every interface into one
   package "named api, types, or interfaces" "is a mistake". Google: "Avoid
   uninformative package names like util, utility, common, helper, model,
   testhelper, and so on"
   ([package names](https://google.github.io/styleguide/go/decisions#package-names)).
5. **No `<feature>/ui` sub-package for the sake of flatness.** Whether templates
   should see only their view model at compile time is a separate question, and
   needs evidence that a template reaches past its view model today.
6. **Where tests and generated files live is convenience, not package design.**
   Decide it separately if it hurts.

No source gives a limit on files or lines. Google calls Go style "flexible
about file size" and offers a rule of thumb, not a number: "it is usually not a
good idea to have a single file with many thousands of lines in it, or having
many tiny files" (package size).

So the large feature packages (`cart`, `admin/orders`, `auth`, `payment`,
`catalog`, `order/buyer`) are not a defect by their size. Once page bodies have
finished moving into their features (the templ/Go move, D13), one pass reads
them against the name test, and each split it finds is filed as its own issue;
#2184 tracks the pass. Nothing moves for it before then. Which package may
import which is a separate rule, held by `internal/repocheck/deps_test.go` as
described above.

#### Naming

A package name is read in the files that import it, so it is chosen for them.
These rules settle the names in the [glossary](#glossary); a new name follows
them too.

1. **A name is a noun for the concept**, lower case, one word or an
   established compound (`cookiejar`, `httptest`). "They are often simple
   nouns" ([Package names](https://go.dev/blog/package-names)); "short,
   concise, evocative" ([Effective Go](https://go.dev/doc/effective_go#package-names)).
   A verb is fine for a package that does that one action, as `sort` and
   `go/format` are (`compress`, `respond`).
2. **No layer name** (rule 4 above), and no name for a kind of declaration:
   every templ function is a `templ.Component`, so `components` is true of every
   page package and becomes `ui/widget`.
3. **Packages used together have distinct names.** "While packages in different
   directories may have the same name, packages that are frequently used
   together should have distinct names. This reduces confusion and the need for
   local renaming in client code." ([Package names](https://go.dev/blog/package-names))
   Count the files that import both. A pair that few files, and no production
   file but the wiring, import together may share a name, as `runtime/pprof` and
   `net/http/pprof` do, and a package imports another of its own name bare, as
   `net/http/pprof` imports `runtime/pprof`. Names a letter apart or synonyms
   count as one name when the same files import both (`formtest`/`formtext`,
   `htmltest`/`httptest`). A compound that names a role over another package's
   noun is a distinct name (`httptest` beside `http`, `cookiejar` beside
   `http`). Where two names share a referent on purpose (`site` and `siteurl`),
   each package comment states the line between them.
4. **Do not take a word the importing code uses.** "Don't steal good names from
   the user. Avoid giving a package a name that is commonly used in client code.
   For example, the buffered I/O package is called bufio, not buf, since buf is
   a good variable name for a buffer." ([Package names](https://go.dev/blog/package-names))
   "For example, usercount is a better package name than count, since count is a
   commonly used variable name."
   ([Google: package names](https://google.github.io/styleguide/go/decisions#package-names))
   Count in the files that import the package, or will, tests included: locals,
   parameters and results of that name, and package-level declarations of it in
   the importing packages. A package-level `func render` stops compiling once
   its package imports a package `render`, which is why the rest of `web` is
   `respond`. A package that is the noun's own type keeps the noun, as `net/url`
   (`url.URL`), `os/user` and `go/token` do; Google names `url` as a common
   local and gives the client's remedy, "the pkg suffix (e.g. urlpkg)", needed
   "only … if the package still needs to be used when such a variable is in
   scope". A package narrower than a generic word takes a compound that says
   what it is: `bufio`, not `buf`; `cartitem`, not `item`.
5. **The package comment is the definition.** "For a package comment, that
   means the first sentence begins with “Package ”."
   ([Go Doc Comments](https://go.dev/doc/comment#package)) Here that sentence
   reads `Package x is …; it holds …; it does not hold …`, and the last clause
   names the nearest sibling a reader would otherwise look in. The glossary row
   carries the same sentence. A package that cannot be given one fails the name
   test. Not every existing comment has this form yet; each package in the
   glossary takes it when it is created or moved.
6. **An exported name does not repeat its package.** "if you are in package
   chubby, you don't need type ChubbyFile, which clients will write as
   chubby.ChubbyFile. Instead, name the type File"
   ([Code Review Comments](https://go.dev/wiki/CodeReviewComments#package-names));
   "redundant information between the two should be reduced or eliminated"
   ([Google: repetition](https://google.github.io/styleguide/go/decisions#repetitive-with-package)).
   So `contact.Form`, not `contact.ContactForm`. The one type a package is named
   for may take its name (`ring.Ring`, `user.User`, `coupon.Coupon`), and a
   compound package's type may repeat its last part (`cookiejar.Jar`,
   `tabwriter.Writer`, `cartitem.Item`). Say "repetitive", not "stutter"
   (Google's editors ask for this).

**A domain package is singular; the back-office desk over it is the plural of
the records staff work there.** `order` is the vocabulary of an order and
`admin/orders` the desk where staff work orders; so too `refund`/`refunds`,
`coupon`/`coupons`, `product`/`products`, `credit`/`credits`,
`newsletter`/`newsletters`, `warranty`/`warranties`. A desk with no domain
package beside it takes the same form (`admin/reviews`, `admin/tiers`); a record
with no plural takes its collective noun (`faq`).

No Go or Google style text speaks to singular or plural package names, and
where "the style guide has nothing to say about a particular point of style",
the code nearby decides
([Google: local consistency](https://google.github.io/styleguide/go/guide#local-consistency)).
The texts that do apply choose the form. A bare noun under `admin/` would make
the files that import a desk with its domain package (18 today, one of them
production: `cmd/goen/server.go`) rename one, against "packages that are
frequently used together should have distinct names"
([Package names](https://go.dev/blog/package-names)) and "good package names
should not require renaming"
([Code Review Comments](https://go.dev/wiki/CodeReviewComments#imports)); the
plural is distinct and short. The Go tree uses the same shape:
`internal/godebug` imports `internal/godebugs`, "a table of known GODEBUG
settings", and `cmd/asm/main.go` imports both `flag` and
`cmd/asm/internal/flags`. The public standard library has no such pair. Reopen
if a non-test file other than `cmd/goen` imports both halves of a pair.

- A desk may share a domain package's name only as rule 3 allows:
  `admin/returns` beside `returns`, and `admin/shipping` beside `shipping`.
  Inside the desk the domain package is imported bare, as `net/http/pprof`
  imports `runtime/pprof`; a file that imports both aliases the desk
  `<noun>desk` (`returndesk`), the same in every such file.

**Import aliases.** "Package imports shouldn’t normally be renamed"; "Try to be
consistent by always using the same local name for the same imported package";
"(A corollary of this is that good package names should not require
renaming.)" ([Google: import renaming](https://google.github.io/styleguide/go/decisions#import-renaming))

- Choose names that need no alias.
- Rename an import only for a collision ("Avoid renaming imports except to
  avoid a name collision",
  [Code Review Comments](https://go.dev/wiki/CodeReviewComments#imports)), and
  use the same local name in every file (Google). The `importas` lint with
  `no-extra-aliases`, which lists each alias with its reason, arrives in a later
  pull request; until then review holds the rule.
- A clash with a local is solved by renaming the local. Only where the local is
  the better word for its whole scope does the package take Google's "pkg suffix
  (e.g. urlpkg)", registered like any alias. `invoicepkg`, six files behind
  one `invoice` parameter, went when the checkout move renamed the parameter.
- An alias follows the package-name rules and is never a layer word (`domain`).

#### Glossary

The packages the naming pass of #2184 decided, and the existing ones it
affects. `planned` means the package does not exist yet, and the second column
says where its code is today. Each definition is the package comment's first
sentence, given here as "x is …; it holds …".

The desks that are renamed take their routes with them in the same pull
request, with no redirect: `/admin/newsletter` becomes `/admin/newsletters`,
`/admin/warranty` becomes `/admin/warranties`, `/admin/home` becomes
`/admin/slides` and `/admin/home/banner` becomes `/admin/banners`, and
`/admin/credit` becomes `/admin/credits`. Every one is behind `RequireStaff`,
and none of these URLs is stored or mailed, so a redirect would be dead code.
Reopen if a staff URL is ever stored or mailed.

| Package | Status, from | Definition | Does not hold | Why this noun, not the two closest |
| --- | --- | --- | --- | --- |
| `catalog` | exists; its campaign page and `/deals` leave for `campaign` | the shop's products as a shopper browses them; it holds listings, search, compare and each product's page with everything shown and posted on it: its reviews, questions and the back-in-stock form | a campaign (`campaign`), saved products (`wishlist`), the alert itself (`backinstock`) | not `review` and `question` leaves: each has one production caller, and a refused review re-renders the product page, so a split would cycle; not `feedback`: the word the back office drops as dishonest |
| `order/lapse` | exists, from `internal/cart/sweeper.go` | an unpaid order's stock hold running out; it holds the sweeper that releases expired holds of uncommitted orders and cancels the orders whose last hold lapsed unpaid | what a cancellation settles (`order/cancellation`), the checkout sessions of a lapsed order (`payment`) | not `order/cancellation`: half the sweep releases holds of orders that stay pending; not `hold`: misses the cancelling half |
| `shipping/storemap` | exists, from `internal/cart/ecpaymap.go`; its type is `storemap.Client` | ECPay's convenience-store map, where a shopper picks the store to collect a parcel from; it holds the map client, the nonce cookie, the callback checks, `PostedStore` and the two hop pages | the pickup method, chains, carriers and store-code shape (`shipping`) | not `pickup`: already the whole delivery method in `shipping`; not `cvsmap`: "CVS" reads as a US pharmacy. The type is `Client`, so `Map` never reads as a map of `Store`s |
| `backinstock` | exists; renamed `internal/restock` and took the request from `internal/catalog` | a shopper's request to be emailed when a sold-out variant is back in stock; it holds the request and the retention sweep of answered requests | receiving stock (`admin/stock`), putting returned units back (`admin/returns`), safety-stock warnings and days of cover (`inventory`, `ui/stockrisk`) | not `restock`: here that means putting units back (`restocked_quantity`); not `stockalert`: reads as the staff low-stock alert. "Back in stock" is what the shopper and the email read |
| `copurchase` | exists; renamed `internal/recommend` | the worker that keeps the co-purchase projection (`product_copurchases`) current; it holds the call to `refresh_copurchases()` and its interval | the "bought together" read and every other recommendation a shopper sees (`catalog`) | not `recommend`: it keeps the projection current and recommends nothing; the stored word is `copurchase` (`refresh_copurchases()`, `copurchase_refreshes`) |
| `formtext` | exists, from `internal/web`; `ErrUnstorable` was `web.ErrFormText`, `CheckStorable` was `web.CheckFormText` and `RefuseUnstorable` was `web.RefuseUnstorableText` | the text a person typed into a form, read on the server; it holds the bounded parse, the storable-text check, width folding, control characters, typed counts, the search term and per-field refusals | what the browser is told a field accepts (`ui/fieldrule`), the drawn field (`ui/widget`) | not `form`: the importing files already use `form` as a local (rule 4); not merged into `fieldrule`: `internal/ui` may import that but never request reading |
| `ui/fieldrule` | exists | the one statement of what a form field accepts, in the form a browser can check while the shopper fills the form in | what was posted and how the server reads it (`formtext`), the drawn field (`ui/widget`) | stays apart from `formtext` for the deps rule above; a merged `form` would also take the importers' local |
| `domtest` | planned, from `AssertEmailLink` in `internal/ui/uitest` and `AssertRefusedInput` in `internal/admin/admintest` | assertions about the HTML document a handler rendered: find by id, attributes, the refused-input check, the emailed-link page | running a browser (`Browser`, `LocalChrome` in `ui/uitest`) | not `htmltest`: a letter from `httptest`, which the same tests import; not `formtest`: a letter from `formtext`, and not only about forms |
| `respond` | planned, what is left of `internal/web` | writing a templ component as the HTTP answer, whole or as an htmx fragment, and the same-site reload hop page | reading input (`formtext`), judging URLs (`siteurl`) | not `render`: nine importing packages declare a `func render` (rule 4); not `page`: reads alike with `ui/pages`; not `templhttp`: names a dependency, not the job |
| `ui/widget` | planned, renames `internal/ui/components` after its domain items leave | an element placed inside a page that a person reads or operates, used by two or more packages; it holds buttons, fields, the stepper, badges, notices and flash, the in-page `Announce` and `RefusalSummary`, cards, disclosure, breadcrumb and pager, stat lines, photos and empty art | the page's surround (`ui/frame`), drawings to scale (`ui/chart`), glyphs (`ui/icons`), any stored vocabulary or domain wording (`tone`, `hero`, `order`, `refund`) | not `components` (rule 2); not a six-way split (`control`, `status`, `stat` …): most importers use several groups together |
| `ui/frame` | planned, renames `internal/ui/layouts` | what surrounds every page's content; it holds the document head, the storefront and back-office frames, header, footer, the drawn promo strip, the chrome context, and the pages that are only the frame plus a message (404 and 400, missing record, emailed-link landing) | anything placed inside a page (`ui/widget`) | not `layouts`: there is one storefront frame and one back-office frame, and "layout" is also CSS layout; not `shell`: reads as a command shell. The move renames the `frame` locals in its importers |
| `credit` | exists; replaced the posting-kind wording in `internal/admin/loyalty` and `internal/admin/orders` | store credit, a customer's balance at the shop; it holds the closed set of posting kinds (`store_credit_entries.kind`) and how each reads | points (`loyalty`), money back to a card (`refund`), a code discount (`coupon`) | not `storecredit`: credit means store credit wherever this code says it, so the qualifier repeats what is clear (Google guide, Naming); not desk-only: leaves today's two copies of the posting-kind switch |
| `admin/credits` | exists, from `internal/admin/loyalty` | the staff desk for store credit; it holds the recent postings, one customer's ledger and the grant form | membership tiers (`admin/tiers`) | not `admin/credit`: the leaf's base name; not `admin/loyalty`: credit is not points |
| `admin/tiers` | exists, what was left of `internal/admin/loyalty` | the staff desk that edits the membership tiers and their earn multipliers; it holds the bands, their spend thresholds and how many customers each holds | store credit (`admin/credits`) | not `admin/loyalty`: the base name of `loyalty`; "tier" means only membership tiers in this code, so it is not ambiguous |
| `admin/slides` | exists | the staff desk for the home carousel's scheduled slides; it holds creating, scheduling, switching on or off and promoting a slide, with a preview of the carousel as shoppers see it | the campaigns and departments that also fill the carousel (`admin/campaigns`, `admin/taxonomy`), the site-wide strip (`admin/banners`) | not `admin/banners` for both: the slide code says "slide", never "banner"; not `admin/carousel`: staff edit one of its three sources. `admin/home` would take `home`'s base name |
| `admin/banners` | exists | the staff desk for the site-wide promotional strip; it holds writing a strip in both languages, its run and switching it on or off | the home page (`home`), the carousel's slides (`admin/slides`) | not `admin/home`: the strip is site-wide, and the base name is `home`'s; not `admin/homepage`: reads alike with `home` |
| `admin/reviews` | exists | the staff desk where product reviews are hidden or shown; it holds the list of every review, hidden ones included, its three-stars-and-below filter, and hiding and showing one | questions (`admin/questions`), the review form and rules (`catalog`) | not `admin/feedback`: it also held questions and contact messages, so it named none honestly |
| `admin/questions` | exists | the staff desk where product questions are answered or hidden; it holds the visible and hidden queues, the shop's answer, hiding and showing a question and withdrawing an answer | reviews (`admin/reviews`), contact messages (`admin/messages`) | as `admin/reviews` |
| `admin/messages` | exists | the staff queue of contact messages customers sent; it holds the list, handle and reopen | outgoing mail (`outbox`, `letter`), catalogue messages (`i18n`) | not `admin/inbox`: "inbox" is already the customer's mailbox (`i18n.KeyCheckInbox`), and beside `outbox` it reads as the inbox pattern; `admin/enquiries` is the fallback if the overload of "message" bites |
| `admin/newsletters` | exists | the staff desk that composes and sends newsletter issues; it holds the composer, the list of issues, each issue's one send through the outbox and the mailing list's counts | the subscriber list and the sign-up (`newsletter`) | not `admin/newsletter`: the leaf's base name; not `admin/issues`: collides with invoice issuing |
| `admin/faq` | exists | the staff desk that edits the FAQ's categories and entries; it holds both languages of each and their order | the storefront FAQ page (`site`) | "FAQ" is a collective noun with no plural, and no domain package is named `faq` |
| `admin/warranties` | exists | the staff search over warranty registrations by serial or order number; it holds the `/admin/warranties` page, its query and its rows | recording cover (`warranty`) | not `admin/warranty`: the leaf's base name |
| `cart/cartitem` | exists; renamed `internal/cart/item` | what the cart reports about its items to a feature that shows them elsewhere; it holds `Item`, a variant in a cart and how many of it, and `AddOutcome`, what adding one came to | the cart, its prices or its store (`cart`) | not `item`: the generic local, declared twice in an importer (rule 4); not `cartline`: the schema says `cart_items`. `cartitem.Item` repeats only the compound's last part, as `cookiejar.Jar` and `tabwriter.Writer` do |
| `admin` | planned, from `internal/admin/access` (`New` becomes `NewControl`); today the directory holds only `admin_test` files | who reaches a back-office handler, and the back office's 404 and 500 answers | any desk's pages or a feature (`admin/<desk>`) | not `admin/access`: its most-called function is the back office's 500 page; not `guard`: same |
| `admin/accesstest` | planned, moves `internal/admin/access/accesstest` | the check every back-office feature runs on its own routes: that each one is behind the access guard | the back office's integration fixtures (`admintest`) | named for its subject, route access, as `iotest` and `fstest` are; not folded into `admintest`: that package imports desks whose own route tests would then import it |
| `checkout` | exists, from `internal/cart`; `Checkout`, `CheckoutView`, `CheckoutMeta`, `CheckoutAddress`, `CheckoutInvoice` and `CheckoutProfile` dropped their prefix (`Page`, `View`, `Meta`, `Address`, `InvoiceView`, `Profile`), the shipping `Quote` became `ShippingQuote`, and the hold and pay windows went to `payment` (`HoldTTL`, `PayWindow`) | turning a shopper's cart into a placed order; it holds the checkout page with its store-pickup round trip, placement, the quote, attempts, drafts, the invoice preference, and coupon and credit spending | the cart itself (`cart`), the placed order afterwards (`order`, `order/buyer`), taking the money (`payment`) | not `order/place`: a verb, and "place" is also a location; not `placement`: jargon that hides the page. The move renamed the `checkout` locals in its importers; `cart` kept the cart page and its own names lost their prefix too (`Page`, `View`, `Meta`) |
| `coupon` | exists; gained the rule and the code shape from `internal/cart/coupon.go`, and the code shape `internal/admin/coupons` restated | what a coupon takes off an order and the shape of its code; it holds the kinds, `Coupon`, `Apply` and its refusals, `Normalise` and `ValidCode` | the lookup and redemption that run inside checkout's transaction (`checkout`), a campaign's price strike (`campaign`), store credit (`credit`) | not `discount`: a campaign's price strike is a discount too; not `promotion`: reads alike with the promo strip and covers campaigns. "Coupon" is the schema's noun (`coupons`) |
| `campaign` | exists, from `internal/catalog`; `State`, `StateAt`, `NewSchedule`, `Schedule`, `End`, `NewEnd`, `EndStat` and `EndsOn` dropped their `Campaign` prefix; its page and `/deals` follow once the product tile has left `ui/pages` | a time-boxed sale on chosen products; it holds its state, the price-strike rule `OnSale` and how its window is told (`Schedule`, `End`) | a coupon code (`coupon`), sales figures (`sales`), the desk that edits campaigns (`admin/campaigns`) | not `sale`: reads alike with `sales`; not `promotion`: reads alike with the promo strip and covers coupons too. The move renamed the `campaign` locals in its importers |
| `policy` | exists, from `internal/site`; `Rules` was `site.ShopRules`, `internal/returns/policy.go` became `decision.go` and the CSP `policy` parameter in `cmd/goen` became `csp` | what the shop states publicly about how it works; it holds the policy documents, the shipping page, the stated windows and thresholds (`Rules`) and the retention texts | the rules that decide a return (`returns`), the FAQ (`site`), the CSP (`cmd/goen`) | not `terms`: one document, and reads as search terms; not `shoprules`: restates the shop |
| `auth/google` | exists | Google's OAuth and OpenID Connect sign-in client; it holds the authorize URL, the code exchange and the identity it returns | goen's sessions and its state cookie (`auth`) | not `oidc`: names the protocol, and the client is Google's; not `oauth`: goen's sign-in is not OAuth in general; as `log/syslog`: a client named for its counterpart, under the area that uses it |
| `wishlist` | planned, from `internal/account` | the products a customer saved to buy later; it holds the page, save, remove and `Has` | the account overview (`account`), the cart (`cart`), the back-in-stock request (`backinstock`) | not `saved`: an adjective; not `favourites`: a second word for the same page |
| `compress` | exists, from `internal/web`; `Handler` was `web.Compress` and `Skip` was `web.NoCompress` | gzip on dynamic HTTP responses; it holds the middleware, the opt-out and Accept-Encoding negotiation | build-time compression of static files (`assets`) | not `gzip`: shadows `compress/gzip`, which it imports; not `encoding`: a standard-library directory of codecs |
| `requestid` | exists, from `internal/web` and `cmd/goen`; `With` was `web.WithRequestID`, `From` was `web.RequestID` and `Handler` was `withRequestID` in `cmd/goen` | the identifier every log line of one request carries; it holds minting, validating and carrying it in the context | the audit event it stamps (`admin/audit`) | not `reqid`: an abbreviation; not `trace`: overclaims, and reads as `runtime/trace` |
| `siteurl` | planned, from `internal/web`, with `ValidSlug` | the shape of a URL on goen's own site; it holds the same-site path check, the configured origin and the slug shape | any page (`site`), the response (`respond`) | not `origin`: shadowed by the `origin` locals at its own call sites; not `href` or `link`: common locals. A tolerated read-alike of `site`; reopen if `site` is renamed |
| `picture` | planned, from `assets` (`Image`) and the `/media` URL scheme | an image a page shows, from either source, at the widths it exists in; it holds `Image`, its `srcset` and the `/media` URL scheme with its width allowlist | the uploaded files (`media`), the files built into the binary (`assets`) | not `image`: the standard library's; not `photo`: a fourth word for an image, and empty-state art is not a photo |
| `clientip` | exists, from `internal/ratelimit`; `From` was `ratelimit.ClientIP` and `Normalise` was its unexported `normalise` | the client's address as seen through goen's trusted proxies; it holds the proxy list and the `X-Forwarded-For` resolution | limiter keys (`ratelimit`) | not `realip`: names nginx's module; not `proxy`: reads as `httputil.ReverseProxy` |
| `hero` | planned, from `internal/ui/components` | a slide of the home page's carousel; it holds `Slide`, `CTA`, `Layout` and `Source`, which `home` produces and `admin/slides` lists | the home page (`home`), the promo strip (`site`, `ui/frame`), the editor (`admin/slides`) | not `carousel`: names the container, not the slide; not `slide` as a leaf: too generic. The schema says `hero_slides` |
| `ui/stockrisk` | planned, from `internal/ui/pages/admin` | the list of SKUs ranked by how soon they run out, as three desks show it; it holds `Row`, `Rows` and `Section` | the estimate itself (`inventory`) | not `cover`: reads as a cover image, and `warranty` already says "cover"; not `runway`: a third word beside "days of cover" and "risk" |
| `tone` | planned, from `internal/ui/components` | the stored colour mood a category or campaign wears; it holds the closed set, parse and labels | what a status colour means (`widget.Intent`), a page's image and tone (`catalog.Theme`) | not `palette`: the set, not one value; not `theme`: already `catalog.Theme`. The move renames the `tone` locals in its importers |

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
  as `admintest` or `dbtest`. A helper that builds one package for other
  packages' tests lives beside it, named for it with `test` appended
  (`admin/products/productstest`, as `net/http/httptest` serves `net/http`;
  [creating test helper packages](https://google.github.io/styleguide/go/best-practices#naming-doubles-helper-package)).
- A test that needs a database of its own calls `dbtest.Pool`: a copy of the
  migrated template on the package's one server, made in milliseconds.
  `dbtest.Database` adds a pool per application role, assumed with `SET ROLE`
  as `cmd/goen` does. Roles belong to the server and so to every test of the
  package: a test that creates one gives it a unique name and drops it.
- A test that drives a real browser has the `_browser_test.go` suffix
  (`_browser_integration_test.go` when it also needs the `integration` tag),
  learns that it may run only through `internal/ui/uitest` (`Browser`,
  `LocalChrome`), and is run by name from the Makefile's `check-layout-run` or
  a workflow. A test may read CSS or JS text only where the bytes are the
  contract, and `internal/repocheck/assetreaders_test.go` lists each one.

### Migrations

`migrations/001` is frozen, and so is every migration once it merges:
`TestFrozenMigrationsAreUnchanged` in `migrations` holds each file to the
SHA-256 it merged with. golang-migrate records only the version a database has
reached, so an edit to a migration never reaches a database already past it.
A change is a new migration:

1. `make migrate-create NAME=add_gift_wrap` writes `002_add_gift_wrap.up.sql`
   and `002_add_gift_wrap.down.sql`, numbered after the last migration. Add
   both files' SHA-256 to `frozen` in `migrations/migrations_test.go` in the
   same pull request; the test prints them.
2. The up file starts with the timeouts 001 sets, `SET lock_timeout = '3s';`
   and `SET statement_timeout = '120s';`, and ends with
   `CALL harden_schema();`, which puts what it created under 001's privilege
   posture. A function it creates or re-creates states `SECURITY DEFINER` (when
   it is one) and `SET search_path = pg_catalog, public, pg_temp` in its header,
   because `CREATE OR REPLACE` resets whatever the header leaves out
   (`internal/db/functionheaders_test.go`). A new table, view or function is
   granted to no role: grant each role what it needs.
3. The down file undoes the up file, and CI's schema job runs every down
   migration and then every up migration again. A down migration is a
   development tool: it drops what its up file created, rows included. 001's
   refuses a database that holds any row, so no one migrates a shop below 001.
4. `make squawk` lints every up file with every rule on (`.squawk.toml`). The
   file-wide ignores at the top of 001 hold because it creates its tables
   empty; a later migration alters tables that hold rows and gets none of them.
   Add a CHECK or foreign key `NOT VALID` and `VALIDATE CONSTRAINT` it in the next
   migration. Create an index `CONCURRENTLY`: golang-migrate sends a file as one
   implicit transaction, in which PostgreSQL refuses `CREATE INDEX
   CONCURRENTLY`, so that index is a migration of its own holding that one
   statement (the "Multi-statement mode" section of golang-migrate's PostgreSQL
   driver README). It cannot set its timeouts first, so it carries
   `-- squawk-ignore require-statement-timeout, require-lock-timeout` with that
   reason, and it does not call `harden_schema()`: an index has nothing for it
   to change.
5. The migration is applied before the binary that needs it is deployed. A
   binary refuses a database behind its newest migration and accepts a newer
   one (`migrations.Schema.Check`), so the binary already running must keep
   working on the new schema: expand in one migration, deploy, and contract in
   a later one. A function's signature changes by adding the new overload,
   deploying the binary that calls it, and dropping the old one in the next
   migration. A constraint name Go matches (`db.IsConstraint`,
   `db.ConstraintName`) is never renamed, since the running binary still
   matches the old name.
6. The demo snapshot is rebuilt from the new checkout:
   `deploy/demo/restore-demo-db.sh` refuses one behind `migrations/`.

## Changes that need the maintainer first

Open an issue before touching any of these; each one is a place where a small
edit becomes a payment, a privilege or a legal term:

- a `SECURITY DEFINER` function's privilege, a role's grant, and
  `harden_schema()`, the procedure a migration ends with;
- the Stripe session's `payment_method_types` pin, its `ExpiresAt` bound to the
  stock hold, and webhook attribution from goen's own payment row;
- the statutory terms in `internal/policy/policies.go` (消保法 §19);
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
