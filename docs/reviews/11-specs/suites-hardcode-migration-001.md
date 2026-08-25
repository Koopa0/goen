# suites-hardcode-migration-001

**Verdict** CONFIRMED · **Severity** medium · **Origin** external report (verified this round)

**Files** internal/db/dbtest/dbtest.go, cmd/goen/integration_test.go, internal/loyalty/integration_test.go, internal/media/integration_test.go, internal/newsletter/integration_test.go, internal/twofactor/integration_test.go, internal/warranty/integration_test.go, internal/contact/handler_integration_test.go, internal/db/writers_test.go, internal/db/coverage_integration_test.go, internal/db/reviews_test.go, docs/roadmap.md


## Root cause

A shared runner exists and is not the only door.

`internal/db/dbtest/dbtest.go` was written to answer this exactly once — `migrate()` at line 79 globs `*.up.sql` and applies in numeric order, and `Start`'s own doc comment at line 35 says "brings up PostgreSQL, applies **every** migration". Twelve suites go through it. Six re-implement its body inline and, in re-implementing it, replace the glob with a single literal filename.

The underlying design error is not the duplication — it is that the duplication is invisible to every gate the repository owns. `make verify` compiles and runs the six; `make test-integration` runs them green; `sqlc-check`, `squawk`, the schema conformance suite and all three completeness guards (`TestEveryTableHasAWriter`, `TestEveryTableIsRead`, `TestEveryColumnIsReadOrWritten`) ask questions about the SCHEMA and the APPLICATION, never about how a suite got its schema. There is no guard whose subject is the test harness.

This is the shape CLAUDE.md names as the one its whole guard family is blind to: "**every guard here asks about ABSENCE** and none can see two correct halves that disagree" (#13, #30, #31). Here the two halves are `dbtest.migrate`'s glob and six inline `os.ReadFile` calls — both correct today, disagreeing the instant a second file lands in `migrations/`.

And it is #34's variant, the cheapest to miss: each of the six was written correctly against the world as it stood. When they were authored `migrations/` held exactly one file, so "read 001" and "apply every migration" were the same statement. The claim stopped being true of the code the day a second file becomes possible, and nothing re-reads it — precisely CLAUDE.md's own question, "is this comment still describing the only implementation that was possible?"

The repository's stated doctrine settles the disposition without needing 002 to exist: a verification instrument that will go quietly stale is the defect, not the staleness. It is the same call as `TestThePendingListOnlyHoldsRealDebt` (refuses an entry that has gone clean so the list cannot quietly refill) and `TestThePrivacyPolicyNamesEveryCookie` (walks the source so a sixth cookie fails the build "the moment its constant is declared — a list somebody has to remember to extend is the failure this exists to catch"). Six copies of a filename are exactly such a list.


## Reproduction — the evidence this rests on

EXECUTED, not reasoned about.

Step 1 — census. Every `func TestMain` in the tree, classified by whether it calls `dbtest.*` or reads a migration filename literal:

    grep -rln "func TestMain" --include="*_test.go" . | sort | while read f; do ... done

    cmd/goen/integration_test.go                    HARDCODES-001
    internal/account/integration_test.go            USES-DBTEST
    internal/admin/integration_test.go              USES-DBTEST
    internal/cart/integration_test.go               USES-DBTEST
    internal/catalog/integration_test.go            USES-DBTEST
    internal/contact/handler_integration_test.go    USES-DBTEST
    internal/db/schema_integration_test.go          USES-DBTEST
    internal/home/handler_integration_test.go       USES-DBTEST
    internal/invoice/integration_test.go            USES-DBTEST
    internal/loyalty/integration_test.go            HARDCODES-001
    internal/media/integration_test.go              HARDCODES-001
    internal/newsletter/integration_test.go         HARDCODES-001
    internal/outbox/integration_test.go             USES-DBTEST
    internal/payment/integration_test.go            USES-DBTEST
    internal/product/integration_test.go            USES-DBTEST
    internal/returns/integration_test.go            USES-DBTEST
    internal/twofactor/integration_test.go          HARDCODES-001
    internal/warranty/integration_test.go           HARDCODES-001

It is SIX suites, not five. The claim's list is right as far as it goes and misses `cmd/goen/integration_test.go` — the suite that covers main's newsletter-drain wiring, whose own comment says "Nothing else in the tree exercises this handler: it is wiring, and wiring is where a rule goes to be deleted without a suite noticing."

The exact bypassing lines:

    cmd/goen/integration_test.go:40
    internal/loyalty/integration_test.go:42
    internal/media/integration_test.go:45
    internal/newsletter/integration_test.go:45
    internal/twofactor/integration_test.go:45
    internal/warranty/integration_test.go:39

each reading, verbatim:

    schema, err := os.ReadFile("../../migrations/001_initial_schema.up.sql")

against `internal/db/dbtest/dbtest.go:79-102`, which globs:

    files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
    ...
    // Glob returns sorted names; the numeric prefix is the applied order.
    for _, file := range files {

Step 2 — executed reproduction. I created a real second migration and a probe test in one suite of each class:

    migrations/002_probe.up.sql   →  CREATE TABLE probe_002 (id int PRIMARY KEY);
    migrations/002_probe.down.sql →  DROP TABLE probe_002;

Probe test (identical body) dropped into `internal/media` (hardcoder) and `internal/contact` (dbtest user), asserting `SELECT to_regclass('public.probe_002')` is non-NULL. Then:

    go test -tags integration -run 'TestProbe002Applied' -v ./internal/media/ ./internal/contact/

    === RUN   TestProbe002Applied
        probe_002_test.go:13: migration 002 NOT applied by this suite
    --- FAIL: TestProbe002Applied (0.00s)
    FAIL    github.com/koopa0/goen/internal/media    2.284s
    === RUN   TestProbe002Applied
        probe_002_test.go:15: migration 002 applied: probe_002
    --- PASS: TestProbe002Applied (0.00s)
    ok      github.com/koopa0/goen/internal/contact  2.278s

Step 3 — the silent half. With `migrations/002_probe.up.sql` still present, I deleted the probes and ran the media suite's own tests:

    go test -tags integration ./internal/media/
    ok      github.com/koopa0/goen/internal/media    3.784s

Green, against a schema missing a migration that exists on disk. Nothing warns, nothing skips, no output mentions 002. That is the claim's stated consequence, executed.

Step 4 — is any of the six entitled to its own path? No. Grepping all six for anything dbtest.Start cannot supply: every one uses the container only to build `dsn` and hands it straight to `pgxpool.New`. None reads the container object, none asserts on `current_database()`, none depends on the database NAME (dbtest names it `goen_test`, the six name it `goen`), none needs a different image or wait strategy — dbtest's `wait.ForLog(...).WithOccurrence(2).WithStartupTimeout(60*time.Second)` is the stricter of the two. `internal/newsletter/integration_test.go:458,479` does `SET ROLE store` / `SET ROLE admin` inside a transaction, which works identically on either pool. The six are byte-for-byte the same boilerplate as `dbtest.Start`, minus the glob.

Step 5 — not a recorded decision. `docs/roadmap.md:221` and CLAUDE.md say only "**`002` is not cut yet, deliberately, and `make schema-drift` is what makes that safe**" — that is about the DEPLOYED schema drifting from `migrations/`, and it names its own trigger ("schema 一旦到達任何不可丟棄的環境,這件事就停止,`002` 開始"). Nothing anywhere sanctions a test suite reading one migration by name. `make schema-drift` compares a live database to a reference built from `migrations/`; it does not look at test suites at all.

Cleanup: my four files (`migrations/002_probe.{up,down}.sql`, two `probe_002_test.go`) are deleted. NOTE — `git status --porcelain` is NOT empty, but the two files in it are not mine: `internal/admin/zz_probe_integration_test.go` and `internal/returns/zz_probe_integration_test.go`, timestamped 11:08/11:09, in packages unrelated to this claim, evidently a concurrent agent's in-flight probes. I left them rather than destroy another session's work.


## Blast radius

Latent today, total when it fires, and silent in both states.

Who is harmed: whoever cuts `002`. CLAUDE.md's escape clause is explicit that this happens the moment the schema reaches an environment that is not disposable — "The moment the schema reaches any environment that is not disposable, that stops and `002` begins." On that commit, six suites silently begin testing a schema that no longer exists anywhere else, while twelve suites test the real one.

How often: every `make test-integration` and every `make verify-all` run from then on, forever, with no output distinguishing the two classes.

Why silent: a suite whose feature is untouched by `002` stays green for the right reason; a suite whose feature `002` changes stays green for the WRONG one, and looks identical. The failure mode is not a red build to investigate — it is a green build that has stopped asking. Proven above: the media suite reported `ok` with a migration sitting unapplied on disk.

What is behind the six specifically: `internal/media` (the upload path — the re-encode invariant, the digest addressing, `product_images_storage_key_key`), `internal/warranty` (the `expires_on` computation off `delivered_at`, and its own recorded false-green history), `internal/loyalty` (`redeem_loyalty_points`' lock-order deadlock fix, the never-negative guard, expiry-on-read), `internal/newsletter` (double opt-in, the `sent_at IS NULL` statement guard, the `store`/`admin` privilege split at lines 458-479), `internal/twofactor` (the `last_step` replay guard proven with 8 goroutines behind a barrier), and `cmd/goen` (the only exercise of main's newsletter-drain wiring anywhere in the tree). Those are money, stock, consent, auth and privilege locks — the set CLAUDE.md says lives in the schema "precisely because it is the one place with no second write path."

The privilege dimension is the sharpest edge. If `002` adds a table, a GRANT or a REVOKE, `internal/newsletter`'s `SET ROLE store` / `SET ROLE admin` cases run against the OLD grant set and pass. That is trap #21 ("A GRANT written beside the thing it grants on") arriving through the test harness instead of through the migration.

Not currently exploitable, not customer-facing, no production impact today: `migrations/` holds exactly `001_initial_schema.{up,down}.sql`, so all eighteen suites presently converge on the same schema. This is a defect in a verification instrument, in the state where it still happens to be correct.


## Fix

Two parts. Part A consolidates the six onto dbtest. Part B is the lock that stops a seventh (below, in tests_required). Do not do A without B — A alone restores the same silence it removes, since nothing would notice the next suite author copying the old boilerplate.

PART A — six mechanical edits, no behaviour change.

In each of the six files, delete the whole container/schema/pool block inside `TestMain` and replace it with the shape twelve suites already use. The canonical body is `internal/contact/handler_integration_test.go:25-34`, quoted verbatim:

    func TestMain(m *testing.M) {
        p, stop, err := dbtest.Start(context.Background())
        if err != nil {
            slog.Error("start database", "error", err)
            os.Exit(1)
        }
        pool = p
        code := m.Run()
        stop()
        os.Exit(code)
    }

Files and the exact spans to replace (the body between `func TestMain(m *testing.M) {` and its closing brace):

  - cmd/goen/integration_test.go:23-53
  - internal/loyalty/integration_test.go:25-…
  - internal/media/integration_test.go:28-58
  - internal/newsletter/integration_test.go:28-…
  - internal/twofactor/integration_test.go:28-…
  - internal/warranty/integration_test.go:22-…

Each file keeps its package-level `var pool *pgxpool.Pool` unchanged; `dbtest.Start` returns `(*pgxpool.Pool, func(), error)`, which is exactly what these need and all they use the container for.

Import surgery per file: ADD `"log/slog"` and `"github.com/koopa0/goen/internal/db/dbtest"` (third group, per package-organization.md's three-group rule). DROP `"github.com/testcontainers/testcontainers-go"`, `".../modules/postgres"`, `".../wait"`, and DROP `"os"` only where nothing else in the file uses it (`os.Exit` survives in the new body, so `os` STAYS in all six — verify per file rather than assuming). Keep `"context"`. `"github.com/jackc/pgx/v5/pgxpool"` stays for the `var pool` declaration.

Behaviour deltas to accept deliberately, all benign and all verified above:
  - database name moves `goen` → `goen_test`. Nothing in the six reads `current_database()` or the DSN string; grep confirmed the DSN is used only as the argument to `pgxpool.New`.
  - wait strategy becomes dbtest's `wait.ForLog(...).WithOccurrence(2).WithStartupTimeout(60*time.Second)`, dropping the redundant `postgres.BasicWaitStrategies()`. Strictly the stricter of the two.
  - failure to start becomes `slog.Error` + `os.Exit(1)` instead of `panic(err)`. Matches the twelve.
  - `internal/media/integration_test.go:56` carries the comment "Not deferred: os.Exit does not run defers, and the container would leak." That reasoning is preserved by `dbtest.Start`'s returned `stop` being called before `os.Exit`; the comment goes with the code it annotates.

WHAT THIS FIX MUST NOT DO:
  - Do NOT touch `internal/db/dbtest/dbtest.go`. Its glob is already correct.
  - Do NOT touch `internal/db/writers_test.go:103-104` or `internal/db/coverage_integration_test.go:846`. Both read `001_initial_schema.up.sql` as TEXT for static analysis — `storedFunctionBodies` regexes `$$…$$` bodies, `TestNoGrantNamesARoleThatDoesNotExistYet` regexes `CREATE ROLE` against `GRANT`/`REVOKE` ordering. They are not applying a migration and `dbtest` cannot serve them. They are a REAL and DISTINCT staleness (a source parser reading one file of many) and must be filed as their own item rather than folded in here — conflating them would fix one thing under the name of another. Note for that separate item: both already `t.Fatalf` on an implausibly small parse result ("only %d roles found; the parser is not reading the schema", "found %d stored function bodies, want far more"), so they degrade loudly-ish rather than silently, which is why they are lower priority and not this defect.
  - Do NOT introduce a parameter to `dbtest.Start` for the database name or wait strategy. Nothing needs one, and per the repo's own rule an option nobody passes is an option one caller eventually passes wrong (`enqueueBulk` is a separate function rather than a parameter, for precisely this reason).
  - Do NOT add a test-only interface or seam; `.claude/rules` and `interfaces.md` forbid tests as a reason for an abstraction, and none is needed — these are direct calls.

Gate: `make verify` then `make test-integration` (needs Docker). Per mistake #5, run each as `cmd && echo PASS || echo FAIL`, never piped and never with `;`.


## The lock, and how to see it fail first

The lock is Part B of the fix, and it must be a Go test derived from the SOURCE, not a hand-maintained list — the `TestThePrivacyPolicyNamesEveryCookie` rule: "a list somebody has to remember to extend is the failure this exists to catch."

WHERE: a new file `internal/db/migrations_test.go`, package `db_test`, with NO build tag. Untagged is load-bearing — it must run in `make verify`'s `test-race` lane without Docker, or the guard on the integration harness would itself need the integration harness. The precedent is `internal/db/reviews_test.go`, which is untagged, in `db_test`, and walks the source tree. (Do NOT follow `writers_test.go`, which is tagged `integration` at line 1 despite only reading files — that is the same smell one level out.)

WHAT: `TestEveryIntegrationSuiteUsesTheMigrationRunner`.

  Corpus, derived: walk the repository root for `*_test.go`; keep every file whose contents contain `func TestMain(m *testing.M)`. That predicate is the discriminator that correctly SPARES `writers_test.go` and `coverage_integration_test.go` — neither declares a TestMain — so the guard cannot accidentally refuse the two legitimate text readers. Fail the test outright if the corpus is smaller than ~15 files ("only %d TestMains found; the walk is not reading the tree"), the `storedFunctionBodies` self-check pattern, so a broken walk cannot pass by finding nothing.

  Assertion, POSITIVE: each corpus file must contain `dbtest.Start(` (or `dbtest.Pool(`). Assert what must be present, never scan for a forbidden `"001_initial_schema"` substring — the `TestStatutoryTermsAreNotPending` lesson. A negative scan is defeated the moment someone writes `filepath.Join("..","..","migrations","001_initial_schema.up.sql")` or interpolates the name, and the positive form catches every one of those because none of them calls `dbtest.Start`.

  Message: name the file, and say the runner globs `*.up.sql` so a suite reading one migration by name goes stale the day `002` is cut. A guard whose message names the rule is what makes a failure actionable.

  Exemption mechanism: none, deliberately. Per Step 4 above, no suite has a demonstrated need. If one ever does, it takes an entry named with its reason at that point — the `TestEveryCategoryNameIsLocalized` allowlist shape, checked by IDENTITY rather than by count, since "the first version compared totals, so an entry naming a query that no longer exists passed."

PROVING IT RED — required, and the repo's own rule (#6, "A test nobody has watched fail", and `rules/testing.md` "Locks Are Proven by Mutation"). The mutation must be SEEN to apply, not assumed (false-green mode #3).

  Mutation 1 (the guard sees the defect): `git stash` Part A's edit to `internal/media/integration_test.go` alone, restoring its inline container + `os.ReadFile("../../migrations/001_initial_schema.up.sql")` block. Run `go test -run TestEveryIntegrationSuiteUsesTheMigrationRunner ./internal/db/`. It must fail naming `internal/media/integration_test.go` and nothing else. Restore, confirm green. Repeat for at least one more of the six — `cmd/goen/integration_test.go`, since it sits outside `internal/` and is the one the original claim missed, which also proves the walk is not scoped to `internal/` (the `cmd/` blind spot CLAUDE.md records for `TestNoChromeStringIsHardCoded`).

  Mutation 2 (the walk is real): point the walk at a directory with no test files and confirm the `len(corpus) < 15` guard fires rather than the test passing vacuously.

SECOND LOCK — the one that proves the fix actually fixed something, run once by hand and recorded, not committed:

  Re-run my Step 2 reproduction after Part A lands. Create `migrations/002_probe.up.sql` containing `CREATE TABLE probe_002 (id int PRIMARY KEY);` plus its `.down.sql`, drop the `to_regclass('public.probe_002')` probe into `internal/media` and `internal/contact`, and run

      go test -tags integration -run 'TestProbe002Applied' -v ./internal/media/ ./internal/contact/

  Both must now PASS, where media FAILED before the fix (output quoted in the reproduction field). Delete all four files afterwards and confirm `git status --porcelain` is clean. This is the before/after pair that turns "the suites were consolidated" from a claim into a fact — and it is deliberately NOT committed, because a permanent `002_probe` migration would be a fixture for a state the application cannot produce, which is #26 from the other side.
