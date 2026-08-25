# deadcode-guards-false-green

**Verdict** CONFIRMED · **Severity** medium · **Origin** external report (verified this round)

**Files** internal/db/exports_test.go, internal/db/callers_test.go, internal/admin/shipping.go, internal/admin/integration_test.go, internal/product/notify.go, internal/product/integration_test.go, internal/product/question.go, internal/loyalty/loyalty.go, internal/payment/query.sql, internal/contact/store.go, internal/i18n/catalogue.go, internal/media/media.go, internal/ui/pages/product.go, internal/ui/pages/tile.go, internal/ui/pages/tile.templ, internal/ui/pages/admin.go, internal/ui/pages/adminreview.go, internal/ui/pages/admintiers.go, internal/ui/pages/question.go, internal/ui/pages/adminquestion.go, cmd/goen/server.go, Makefile, CLAUDE.md

> **RESOLVED — two conflicts in this spec, both reported by the implementing
> agent and both confirmed by experiment. Where this block and the body
> disagree, this block wins.**
>
> **1. The corpus is `./...`, not `./cmd/goen`.** Proven with a throwaway module
> (cmd/app importing internal/used, plus internal/orphan imported by nothing):
>
> ```
> $ deadcode ./cmd/app   →  internal/used/used.go:5: unreachable func: DeadInUsed
> $ deadcode ./...       →  internal/orphan/orphan.go:3: unreachable func: WhollyDisconnected
>                           internal/used/used.go:5: unreachable func: DeadInUsed
> ```
>
> A pattern determines the LOAD SET; RTA roots are the main packages inside it.
> `./cmd/goen` cannot load a package nothing imports, so it cannot report one —
> and a wholly disconnected package is this repository's signature defect
> (`product_search_documents`: an entire table with no writer and no reader).
> The corpus that cannot see that class is the wrong corpus by construction.
> Run over `./...`, no `-test`, no integration tag. Today both report the same
> set only because every package currently sits in main's import graph; the gate
> exists for the day that stops being true.
>
> **2. `internal/product.Store.Answer` is NOT deleted and NOT wired now.** The
> security spec (`answer-staff-flag-is-caller-supplied.md:102`) forbids deleting
> it — the admin integration test needs it as a fixture, and the customer-reply
> route is queued by name in docs/roadmap.md. Wiring it now is wave-1-and-later
> work. Resolution: an allowlist entry whose reason NAMES the wave-1 spec and
> the queued route. The allowlist is BIDIRECTIONAL, so the day the route lands
> and Answer becomes reachable, the entry fails stale and is removed in that
> PR — which is `TestEveryTableHasAWriter`'s own allowlist pattern ("a queued
> item is a claim about the world, and the world moves"; all three of its
> entries grew a door and came off). The three test-only entries the body names
> stay beside it on the same terms.
>
> **3. The gate lands NOW, not in wave 4.** The published order deferred it
> because a gate written early "blocks later waves for reasons belonging to no
> PR" — a hazard of PARALLEL branches. Execution is sequential in one working
> tree, so an orphan left by a wave-1 finding surfaces in that finding's own
> verify, which is the correct attribution, not the wrong one. The deferral's
> reason expired; the findings document is corrected to match. Every subsequent
> finding keeps the gate green as part of its own definition of done.


> **RESOLVED (3) — the gate cannot see generated `db.Queries` methods, and the
> body's claim that RTA resolves plain sqlc calls is FALSE on this tree.**
> Reported by the implementing agent, replicated here, and decided.
>
> ```
> $ deadcode -generated -whylive=...internal/db.Queries.HasStockNotice ./...
> deadcode: ...Queries.HasStockNotice is reachable only through reflection
> $ deadcode -generated ./...        →  the 14, and ZERO internal/db symbols
> ```
>
> x/tools' RTA is conservative about reflection, so every `Queries` method is
> live to it. The gate therefore covers the HAND-WRITTEN layer only — and that
> is enough, because the two layers compose:
>
> - **The deadcode gate catches dead wrappers.** `Store.RemoveZonePrefix` and
>   `Store.WaitingForRestock` are both IN the 14. A dead chain is always flagged
>   at its wrapper.
> - **A replacement for `callers_test.go`'s question catches orphan queries** —
>   a generated method with ZERO production call expressions. Name it
>   `TestEveryGeneratedQueryHasAProductionCaller`: walk hand-written, non-test
>   production Go with the AST, collect `CallExpr` selector names (not raw
>   substrings — the old guard's `.Name(` over source matched prose in
>   comments), and require every sqlc method to appear. Corpus excludes
>   `_test.go` and generated files; keep the old guard's ≥100-methods tripwire.
> - **Each instrument's blind spot is the other's subject, and both are stated
>   limits in the test comments.** The new guard cannot catch a dead
>   wrapper→query chain — the body's own mutation evidence proved a wrapper is
>   permanently its own caller — and the gate catches exactly that at the
>   wrapper. The gate cannot see generated methods, and the new guard covers
>   exactly those.
>
> **No type-aware generated-reachability analyser is queued.** The pair covers
> the total-failure class end to end; a queued item nothing needs is the debt
> this repository keeps deleting.
>
> **Dispositions of the two chains, and they differ:**
>
> - **`Store.WaitingForRestock` + `Queries.HasStockNotice`: DELETE NOW**, with
>   the test block that calls it (internal/product/integration_test.go:452-458)
>   and the query at product/query.sql:195, then `make sqlc`. This is not a new
>   decision: CLAUDE.md's restock-notice section already rejected this exact
>   design — "A read-then-write guard would have been a race two visitors both
>   pass" — in favour of the partial unique index. The method is the rejected
>   design's leftover, and its deletion cites that line.
> - **`Store.RemoveZonePrefix` + `Queries.RemoveZonePrefix`: ALLOWLIST, owned by
>   wave 4.** `setzoneprefixes-appends.md` replaces the mechanism with a new
>   `RemoveZonePrefixesExcept` query and must also rework the integration
>   fixture at internal/admin/integration_test.go:4882 that uses the old method
>   to empty a zone. Deleting now would do half of wave 4's work out of order.
>   The entry's reason names that spec; the bidirectional gate fails it stale
>   the day wave 4 lands, which forces the deletion in the right PR.
>
> Mutation for the new guard, beyond M1-M3: **M4** — pick a singly-called
> generated method, delete its one production call site, run the guard, PASTE
> the failure naming that method, restore. The old guard stayed green through
> exactly this (its own body's evidence, lines below); the new one must not.

## Root cause

Both guards approximate REACHABILITY with TEXT/IDENTIFIER PRESENCE, over a corpus that includes the code that is not the program.

Three independent errors compose:
1. **Wrong question.** "Is this name mentioned somewhere?" is not "can main reach this function?". Presence is monotone in the corpus, so every file added — a test, a build-tagged fixture, a `.templ` — can only ever make the guard greener. A guard that can only weaken as the tree grows is not a guard.
2. **Wrong identity.** `map[string]string` keyed on `fn.Name.Name` (exports_test.go:50) throws away the package and the receiver, and `"."+name+"("` (callers_test.go:27) throws away the package. Go's identity for a function is (package, receiver, name); collapsing it to a bare string makes `ProductView.HasReviews` indistinguishable from `ProductTile.HasReviews` and `product.Store.Answer` indistinguishable from a struct field called `Answer`.
3. **Wrong corpus.** `_test.go` and `//go:build integration` files are in the USES corpus (`forEachSource(t, true, ...)`) — but a test is not the program. A function only a test calls is exactly the thing being looked for. And because `referenced()` counts identifiers inside the declaring function's own body, an `admin.Store.X` wrapping `db.Queries.X` is unconditionally its own caller.

The underlying design error is choosing an approximation whose failure direction is FALSE-NEGATIVE. A crude guard whose errors are false positives is annoying and self-correcting; one whose errors are false negatives reports coverage it does not have, which is the specific failure CLAUDE.md mistake #7 records, and it did it in the two tests written to enforce reachability.


## Reproduction — the evidence this rests on

EXECUTED, not reasoned. Working tree clean before and after (`git status --porcelain` empty).

=== What each guard actually searches ===

A) internal/db/exports_test.go — TestEveryExportedFunctionHasACaller
- Corpus of DECLARATIONS (`exportedFunctions`, line 41; `forEachSource(t,false,...)`, line 88): every `.go` file under the repo root EXCEPT dirs named `.git`, `db`, `cmd`, and except `*_templ.go` and `*_test.go`. Keyed on the BARE FUNCTION NAME with no package and no receiver, first-declaration-wins:
    internal/db/exports_test.go:50  `if _, seen := out[fn.Name.Name]; !seen {`
  So every same-named method across the module collapses to ONE map entry.
- Corpus of USES (`referenced`, line 57; `forEachSource(t,true,...)`): the same walk but WITH `_test.go` files and WITH `cmd/`. It counts every `*ast.Ident` by name, anywhere, in any syntactic position — a selector's `Sel`, a struct FIELD name, a parameter, a type name, a local variable. `parser.ParseFile` ignores build constraints, so `//go:build integration` files are parsed and counted. The only exclusion is a `FuncDecl`'s own name (line 63-72). Comments are NOT counted (Walk reaches `*ast.Comment`, never an `Ident`) — the claim's "comments" is inaccurate for THIS guard.
- Third escape hatch, a bare SUBSTRING over concatenated `.templ` text:
    internal/db/exports_test.go:25  `if uses[name] > 0 || strings.Contains(templates, name) {`
  Unanchored, so `Keys` matches `MonKeys` and `URL` matches `SafeURL`.

B) internal/db/callers_test.go — TestEveryGeneratedQueryHasACaller
- Methods on `*Queries` parsed from `internal/db/query.sql.go`.
- Callers = every `.go` file in the module outside `internal/db`, concatenated into ONE STRING (`callerSources`, line 63) — `_test.go` included, `*_templ.go` included, build-tagged files included — matched with raw text:
    internal/db/callers_test.go:27  `if !strings.Contains(callers, "."+name+"(") {`
  This one DOES count comments and string literals (the claim is right here), and it is package-blind.

=== The real tool ===
`go run golang.org/x/tools/cmd/deadcode@latest ./...` (2026-08-24, does not join the module graph — `go list -m all` unchanged) prints exactly 14:

  internal/admin/shipping.go:369:17: unreachable func: Store.RemoveZonePrefix
  internal/contact/store.go:32:17: unreachable func: Store.WithTx
  internal/i18n/catalogue.go:55:6: unreachable func: Keys
  internal/i18n/catalogue.go:64:6: unreachable func: MessageFor
  internal/loyalty/loyalty.go:38:6: unreachable func: PointsFor
  internal/loyalty/loyalty.go:69:6: unreachable func: Days
  internal/media/media.go:50:17: unreachable func: Object.URL
  internal/product/notify.go:45:17: unreachable func: Store.WaitingForRestock
  internal/product/question.go:46:17: unreachable func: Store.Answer
  internal/ui/pages/admin.go:27:23: unreachable func: AdminVariant.Price
  internal/ui/pages/adminreview.go:34:27: unreachable func: AdminReviewsView.HiddenCount
  internal/ui/pages/admintiers.go:26:20: unreachable func: AdminTier.Translated
  internal/ui/pages/product.go:236:23: unreachable func: ProductView.HasReviews
  internal/ui/pages/question.go:49:19: unreachable func: Question.AnsweredByShop

while both guards are GREEN:
  $ go test ./internal/db/ -run 'TestEveryExportedFunctionHasACaller|TestEveryGeneratedQueryHasACaller' -count=1
  --- PASS: TestEveryExportedFunctionHasACaller (0.09s)
  --- PASS: TestEveryGeneratedQueryHasACaller (0.09s)

=== Why each of the 14 passes (measured with a throwaway program in /tmp, since deleted, that re-implemented `referenced()` and printed the saving file) ===
- TEST-ONLY caller, incl. `//go:build integration`: `WaitingForRestock` (1 ref, internal/product/integration_test.go), `Keys` (2, both `_test.go`), `MessageFor` (2, both `_test.go`), `PointsFor` (1, loyalty_test.go), `HiddenCount` (1, admin/integration_test.go).
- BARE-NAME COLLISION: `WithTx` (22 idents from live `account/admin/cart/payment/invoice/returns` stores), `Days` (48, all `admin`/`pages` fields and params), `Object.URL` (136), `AdminVariant.Price` (13), `AdminTier.Translated` (10), `product.Store.Answer` (37 — mostly `Answer` FIELDS in internal/admin/faq.go and internal/site/policy.go), `Question.AnsweredByShop` (saved by the `AnsweredByShop` struct FIELD at internal/ui/pages/adminquestion.go:19 — a field, not a call).
- TEMPL SUBSTRING: `ProductView.HasReviews` — its ONLY reference in the whole module is `t.HasReviews()` at internal/ui/pages/tile.templ:62, which belongs to a DIFFERENT type, `ProductTile.HasReviews` (internal/ui/pages/tile.go:70). deadcode reports `ProductView.HasReviews` and does NOT report `ProductTile.HasReviews` — precisely the discrimination text matching cannot make.

=== MUTATION 1 — the guard stays GREEN with ZERO callers anywhere ===
Removed the sole caller of `admin.Store.RemoveZonePrefix` (internal/admin/integration_test.go:4882-4884), leaving these as the only occurrences in the module outside internal/db:
  internal/admin/shipping.go:368  // RemoveZonePrefix takes one prefix out of one zone.
  internal/admin/shipping.go:369  func (s *Store) RemoveZonePrefix(ctx context.Context, id, prefix string) error {
  internal/admin/shipping.go:380      n, execErr := q.RemoveZonePrefix(ctx, db.RemoveZonePrefixParams{
Result: `go test ./internal/db/ -run TestEveryExportedFunctionHasACaller -count=1` → `ok`. GREEN with no caller at all.
Cause: line 380 is inside the dead method's OWN BODY. `q.RemoveZonePrefix(...)` is a `SelectorExpr` whose `Sel` is an `Ident` named `RemoveZonePrefix`, and `countIdents` (exports_test.go:79) counts it. **A wrapper method whose name matches its sqlc query is permanently its own caller** — a class this guard can never report, whatever the corpus. (File restored byte-for-byte.)

=== MUTATION 2 — causal proof the build-tagged test is the only thing holding a symbol green ===
Removed the `s.WaitingForRestock(...)` block at internal/product/integration_test.go:452-458 (file is `//go:build integration`, excluded from `make verify`'s `test-race`). Result:
  --- FAIL: TestEveryExportedFunctionHasACaller (0.08s)
      exports_test.go:33: 1 exported functions are named nowhere but their own declaration:
            WaitingForRestock  (../../internal/product/notify.go)
So a single call in a file that `make verify` never compiles is the whole difference between red and green. (File restored.)

=== callers_test.go is false-green too, and the claim understates it ===
I scripted every `*Queries` method against a production-only corpus: no query is test-only, so that mechanism is unexercised today. But the guard asks for a MENTION, not reachability, so:
- `HasStockNotice` — only occurrence in the module is internal/product/notify.go:50, inside the unreachable `WaitingForRestock`.
- `RemoveZonePrefix` (the query) — only occurrence is internal/admin/shipping.go:380, inside the unreachable `Store.RemoveZonePrefix`.
Two generated queries no reachable code calls, both green.

Final state: `git status --porcelain` empty, `git diff --stat` empty, both guards green again, deadcode still 14.


## Blast radius

No runtime defect and no customer-visible failure — this is a guard-integrity defect, CLAUDE.md mistake #7 ("A suite that reports coverage it does not have") in the two tests whose entire purpose is to prevent mistake #35 ("A feature every guard could see, that no path could reach"). Both run inside `make verify` (Makefile:542 → `test-race`), so every commit for the life of these files has been told they hold.

Silent: yes, and structurally so. The guards cannot go red for the three commonest shapes of dead export in this tree (test-only caller, bare-name collision, self-referencing sqlc wrapper), so their green is not evidence about anything. Two of the 14 are the exact defect class this repository keeps finding, hidden by the guard built to find it:

1. **`internal/product/question.go:46 Store.Answer` is a customer-facing door that does not exist.** It takes `staff bool` and writes `product_answers`. The only route into that table is `POST /admin/questions/{id}` (cmd/goen/server.go:304 → `admin.Store.AnswerQuestion`, always staff). CLAUDE.md describes the other half — "burying it under three customer replies is the same as not having it", "`is_staff` is stored at the moment an answer is written, never derived" — and internal/ui/pages/product.templ:96-102 RENDERS customer answers with a staff badge beside them. Six integration-test call sites exercise it, one passing `staff=false` (internal/product/integration_test.go:745), a state no HTTP request can produce. `TestEveryTableHasAWriter` passes because admin writes the table; only reachability could see this.

2. **`internal/loyalty/loyalty.go:38 PointsFor` is a second Go copy of a money rule the database owns, and it already disagrees.** Points are awarded in SQL — internal/payment/query.sql:94-108 `AwardOrderPoints` computes `(subtotal - discount + shipping + tax)/10000 * tier_multiplier_bp/10000` inside `award_loyalty_points`. `PointsFor` computes `totalCents/10000*PointsPerHundred` with NO tier multiplier, and both carry a comment stating the same "integer division so a NT$50 order earns nothing" rule in different words. That is CLAUDE.md mistake #13's shape ("two correct halves that disagree", which "every guard in this repository is blind to") sitting in the loyalty ledger, kept alive by one line in loyalty_test.go.

3. Smaller, real gaps the guard hid: `admin.Store.RemoveZonePrefix` — `/admin/shipping` has no control to take a postal prefix out of a zone, and CLAUDE.md documents the UPSERT half of that feature at length; `product.Store.WaitingForRestock` + its `HasStockNotice` query — the PDP cannot tell a visitor they are already on the restock list; `contact.Store.WithTx` — a transactional seam nothing joins.

Frequency: every `make verify` run, permanently, until the guard is replaced.


## Fix

DELETE `internal/db/exports_test.go` and `internal/db/callers_test.go` entirely. Do not keep them "as a backstop": a guard that cannot fail for the three commonest shapes is not a weaker lock, it is no lock, and leaving it green next to a real gate makes the real gate's findings look like duplicates.

Replace them with ONE Makefile gate driving `golang.org/x/tools/cmd/deadcode`, not a Go test.

WHY a Makefile target and not a test: CLAUDE.md "Build tools stay out of go.mod" — "Before adding a `tool` directive, run `go list -m all | wc -l` before and after. If the tool does not generate code this module compiles, it does not belong there." A test importing `golang.org/x/tools/go/packages` + `go/ssa` + `go/callgraph/rta` joins the module graph and lands inside every govulncheck/Trivy/Dependabot run. `go run pkg@version` pins it just as firmly without joining the graph — I verified `go run golang.org/x/tools/cmd/deadcode@latest ./...` leaves `go list -m all` unchanged. So: do NOT write a go/packages+RTA test; adopt the tool from outside the module.

1. Makefile — add near the other pinned tools, and pin an exact version, never `@latest`:

       DEADCODE_VERSION ?= v0.38.0   # resolve the current tag; @latest in a gate is unpinned
       .PHONY: deadcode
       deadcode: gen
           @go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VERSION) ./cmd/goen > $(TMP)/deadcode.out 2>&1 || \
               { echo 'deadcode: FAIL (tool error)'; cat $(TMP)/deadcode.out; exit 1; }
           @scripts/deadcode-check.sh $(TMP)/deadcode.out .deadcode-allow

   - `deadcode: gen` is required: templ output is what carries the `.templ` call sites, so the gate must run after `templ generate`, exactly as `test-race: gen` (Makefile:42) already does.
   - Root at `./cmd/goen` (the module's single main) rather than `./...`; do NOT pass `-test`, and do NOT pass `-tags integration` — a test is not the program, and that is the whole correction.
   - Add `deadcode` to the `verify` chain at Makefile:542, after `vet` and before `lint`.
   - Add `deadcode` to the `.PHONY` list at Makefile:23.

2. `scripts/deadcode-check.sh` — compare the tool's `pkg/path.go:LINE:COL: unreachable func: Recv.Name` lines against a checked-in `.deadcode-allow`, keyed on the symbol (`internal/loyalty.Days`), never the line number. It must fail in BOTH directions, which is the rule CLAUDE.md already states for `TestEveryCategoryNameIsLocalized`: "Its allowlist is checked by IDENTITY rather than by count: the first version compared totals, so an entry naming a query that no longer exists passed."
   - any reported symbol not in the allowlist → FAIL, printing file:line and the wording the deleted guard used ("Wire it or delete it. Nothing here is public API…");
   - any allowlist entry the tool no longer reports → FAIL ("this entry names a function that is reachable now; delete the line").
   Report from a command that cannot lie about its exit status (CLAUDE.md mistake #5: never a piped `| head`).

3. `.deadcode-allow` — one symbol per line, each REQUIRING a `# reason:` on the same line, the shape CLAUDE.md demands of every other exclusion ("excluded by CATEGORY with a reason … Never string by string"). Legitimate entries and their reasons:
       internal/i18n.Keys         # reason: read by TestEveryKeyIsTranslatedInEveryLocale / TestEveryKeyIsRendered only
       internal/i18n.MessageFor   # reason: same catalogue guards
       internal/loyalty.Days      # reason: read by the three MembershipWindowDays mirror tests, which are why that constant may be duplicated (internal/account/window_test.go:15, internal/payment/payment_test.go:443, internal/admin/integration_test.go:2791)
   Everything else in the 14 is DELETED or WIRED, and each is a separate decision the PR must state:
   - `internal/product.Store.Answer` (question.go:46) — the customer-answer door. Either build `POST /p/{slug}/questions/{id}/answers` (plain form, `303`, `422` with values intact and `aria-invalid` + `aria-describedby`, per the write-face rule; `staff=false` from a customer session) or delete the method, its `AnswerQuestion` customer path and the six integration call sites. Queue it BY NAME in docs/roadmap.md if it is not built in this PR — `.claude/rules/review-process.md` admits no third state.
   - `internal/loyalty.PointsFor` (loyalty.go:38) — DELETE. The earning rule is `award_loyalty_points` via internal/payment/query.sql:94-108 and already includes the tier multiplier this copy lacks; a second Go statement of a money rule the database owns is mistake #13 waiting. Move `TestPointsFor`'s cases to the integration suite against the SQL if the arithmetic is worth locking.
   - `internal/admin.Store.RemoveZonePrefix` — wire a remove control into `/admin/shipping`'s zone form (it is already `audited` and already `:execrows` with an `ErrNotFound` on zero), or delete the method AND the `RemoveZonePrefix` query from `internal/admin/query.sql` and re-run `make sqlc`.
   - `internal/product.Store.WaitingForRestock` — wire into the PDP's sold-out panel, or delete it AND the `HasStockNotice` query from `internal/product/query.sql` + `make sqlc`.
   - `internal/contact.Store.WithTx`, `internal/media.Object.URL`, `internal/ui/pages.{AdminVariant.Price, AdminReviewsView.HiddenCount, AdminTier.Translated, ProductView.HasReviews, Question.AnsweredByShop}` — delete.
   NEVER hand-edit `internal/db`: a dead generated query is deleted from the feature's `query.sql` and regenerated (`make sqlc`), which `check-generated-code.sh` already enforces.

4. TEMPL AND SQLC FALSE POSITIVES — this is why the crude version existed, and the tool does not need help with either:
   - templ: `*_templ.go` is ordinary compiled Go inside the same package, so SSA/RTA sees `t.HasReviews()` as a real call. MEASURED: `pages.ProductTile.HasReviews`, whose only call site is `internal/ui/pages/tile.templ:62`, is ABSENT from the 14, while `pages.ProductView.HasReviews` is present. The one requirement is that generation has run — hence `deadcode: gen`. Do NOT add a templ allowlist; that would reintroduce the substring hole.
   - sqlc: `internal/db` methods are plain Go called by plain Go and RTA resolves them. A `*Queries` method with no reachable caller SHOULD be reported — that is what `callers_test.go` was trying to say and could not.
   - The one place to watch is dynamic dispatch through the consumer-defined interfaces this repo uses (`admin.Invoicer`, `payment.Refunder`, `returns`' order-access interface). RTA is sound here: a method is reachable if its concrete type is instantiated on a reachable path, so an interface method wired in `cmd/goen/server.go` is never reported. If a future entry looks like an interface false positive, it is far likelier the real finding — `admin.Invoicer` declaring only `Documents/Issue/Void` while `invoice.Store.Allowance` had no door is the precedent CLAUDE.md already records.

5. Record the replacement in CLAUDE.md beside the other completeness guards, and delete any sentence crediting the two removed tests: this file's own rule is that "a number described as measured is not thereby kept measured; only something that re-reads it is."


## The lock, and how to see it fail first

The lock is the `make deadcode` gate; it is proven the way this repository proves every lock — RED first, and the mutation seen to apply (CLAUDE.md mistake #6, and false-green mode #3: "The mutation must be seen to apply, not assumed").

RED-FIRST (must be run and pasted into the PR before any of the 14 are dispositioned):
- With `.deadcode-allow` EMPTY, `make deadcode` must exit non-zero and name exactly the 14 symbols listed in the reproduction, with file:line. This is the gate failing on the tree as it stands today, and it is the direct counterpart to `go test ./internal/db/ -run 'TestEveryExportedFunctionHasACaller|TestEveryGeneratedQueryHasACaller'` passing on that same tree. Paste both transcripts side by side.

MUTATION 1 — the gate catches what the old guard could not (test-only caller):
- Restore `internal/product/notify.go`'s `WaitingForRestock` if it was deleted, or use `admin.Store.RemoveZonePrefix`. Delete its only production caller, leaving only the `//go:build integration` call. Run `make deadcode` → must FAIL naming the symbol. `git diff` must show the deletion applied before the run. Restore.
- The old guard's behaviour on this exact input is already on record above: GREEN with zero callers anywhere.

MUTATION 2 — the gate catches the bare-name collision:
- Reachable control: delete the single call at `internal/admin/handler.go:1900` (`h.store.AnswerQuestion(...)`). `make deadcode` must FAIL naming `internal/admin.Store.AnswerQuestion` — even though `AnswerQuestion` is a live identifier in internal/product, internal/db and three integration tests, i.e. even though the old guard would stay green. Confirm the old guard's blindness by running it on the same mutated tree BEFORE deleting the two test files (it must still pass). Restore.

MUTATION 3 — the gate catches the self-referencing sqlc wrapper, which the old guard structurally cannot:
- On a tree where `admin.Store.RemoveZonePrefix` has been wired to a handler, delete the handler's call. `make deadcode` must FAIL. The old guard is green here by construction (internal/admin/shipping.go:380 counts as its own caller). Restore.

MUTATION 4 — the allowlist fails in BOTH directions (this is the half that rots):
- Add a line naming a reachable function, e.g. `internal/i18n.T   # reason: bogus`. `make deadcode` must FAIL with "names a function that is reachable now", not pass. Without this the allowlist becomes the hand-written list CLAUDE.md refuses.
- Delete a `# reason:` from a legitimate entry → must FAIL. An entry with no reason is the per-string allowlist that "grows to the size of the debt and then says nothing".

MUTATION 5 — templ generation is a precondition, not an assumption:
- Delete the `if t.HasReviews() { … }` block from `internal/ui/pages/tile.templ`, run `make gen`, run `make deadcode` → must now ALSO report `pages.ProductTile.HasReviews`. This proves the gate reads real templ call sites rather than being blind to that package, and proves the `deadcode: gen` dependency is load-bearing. Restore and regenerate.
- Negative control from today's run, to be recorded rather than re-derived: `pages.ProductTile.HasReviews` is absent from the 14 while `pages.ProductView.HasReviews` is present — the discrimination the deleted guard could not make.

NOT REQUIRED, and say so rather than dressing it up: no test locks `deadcode`'s own RTA soundness. That is a property of x/tools, the same call CLAUDE.md makes for `subtle.ConstantTimeCompare` and the `/admin/messages` single-clock query — pin the version and record the reason instead of pretending a behavioural test proves it.

Finally, per `.claude/rules/review-process.md`, "A fix made in response to a finding is new work: run /self-review on it again" — and the specific thing to ask of this fix is what it made newly reachable: deleting `PointsFor` must not delete the only statement of a rule nobody else states, and wiring `product.Store.Answer` opens a new customer write face that needs its own write-face and `aria-describedby` checks.
