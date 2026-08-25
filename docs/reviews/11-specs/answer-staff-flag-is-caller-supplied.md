# answer-staff-flag-is-caller-supplied

**Verdict** PARTLY · **Severity** low · **Origin** external report (verified this round)

**Files** internal/product/question.go, internal/product/query.sql, internal/admin/question.go, cmd/goen/server.go, migrations/001_initial_schema.up.sql, internal/product/integration_test.go, internal/admin/integration_test.go, internal/ui/pages/product.templ, internal/ui/pages/adminquestion.templ, docs/roadmap.md


## Root cause

The `is_staff` decision is expressed twice, in two different grammars, and only one of them can be wrong.

`internal/admin/question.go:59-61` states the rule correctly and enforces it by CONSTRUCTION: "is_staff is true because of the ENDPOINT, never of who is signed in" — the value is a Go literal in the struct, so no caller can express anything else. `internal/product/question.go:46` states the same fact as a `staff bool` PARAMETER, which is the one shape that lets a caller express the other answer. The comment on it — "storing staff as the caller knows it now" — describes the read-time rule (correct, and CLAUDE.md's reasoning for it still holds) while quietly conceding that the write-time authority is whoever is calling.

Underneath, the column-privilege model was not extended to this column. `migrations/001_initial_schema.up.sql:4646-4649` narrows `store`'s INSERT on `product_answers` to a column list and then puts `is_staff` in it. CLAUDE.md's own rule for that pattern is "A DECISION column is not a table … `hidden_at`, `resolution`, `decided_at`, `staff_note` … are the SHOP's statements about what a customer wrote." `is_staff` is exactly such a column — it is the shop asserting that the shop said something — and it is granted to the storefront role anyway. Both column lists (store's at :4647 and admin's at :4703) are byte-identical, which is the tell that the split was never actually decided for this table.

And the deeper cause of the dead method: the customer-reply capability is described in CLAUDE.md and exercised by tests, but has no door. That is mistake #26 from the other side — a fixture (`internal/admin/integration_test.go:1996`, building a question with a customer reply and no shop reply to test `/admin/questions`' "answered means the SHOP answered") reaching a capability the application itself cannot perform.


## Reproduction — the evidence this rests on

EXECUTED, three ways.

1. READ. `internal/product/question.go:45-46`:
```go
// Answer records an answer, storing staff as the caller knows it now.
func (s *Store) Answer(ctx context.Context, questionID, userID, body string, staff bool) error {
```
and at :59-62 it passes it straight through: `db.AnswerQuestionParams{... Body: body, IsStaff: staff}`. The SQL (`internal/product/query.sql:276-280`) is a plain `INSERT INTO product_answers (question_id, user_id, body, is_staff) SELECT q.id, @user_id, @body::text, @is_staff::boolean ...` — no server-side derivation.

2. CALLER SWEEP + MUTATION (this is the decisive part). `grep -rn "\.Answer(" --include="*.go" --include="*.templ" .` returns SIX hits and every one is a test:
   - internal/product/integration_test.go:745 (`false`), :748 (`true`), :788 (`true`), :821 (`true`), :900 (`false`)
   - internal/admin/integration_test.go:1996 (`false`)
I then deleted the whole `Answer` method from question.go and ran the build:
```
$ go build ./...      →  BUILD_PASS_WITHOUT_ANSWER   (exit 0)
$ go vet -tags integration ./internal/product/ ./internal/admin/
vet: internal/product/integration_test.go:745:14: s.Answer undefined (type *product.Store has no field or method Answer)
vet: internal/admin/integration_test.go:1996:15: ps.Answer undefined (type *product.Store has no field or method Answer)
```
The mutation was seen to apply and production code compiles without the method at all. It has NO production caller. File restored; `git diff --quiet` passes.

3. ROUTES. The only registered answer route is `cmd/goen/server.go:304`: `mux.HandleFunc("POST /admin/questions/{id}", back.RequireStaff(back.AnswerQuestion))`, which reaches `internal/admin/question.go:59-83` where the flag is a literal, under a comment that already states the rule: "// AnswerQuestion posts the SHOP's answer; is_staff is true because of the ENDPOINT, never of who is signed in." The storefront's only question route is `cmd/goen/server.go:164` `POST /p/{slug}/questions` → `items.Ask`, which writes `product_questions` and cannot touch `product_answers`. No `.templ` renders a customer answer form (only `adminquestion.templ:46` posts to `q.AnswerAction()` = `/admin/questions/{id}`). curl against the running server: `POST /p/aurora-slate/answers` → 405 (no such pattern).

4. WHAT THE SCHEMA WOULD DO IF A ROUTE EXISTED — run against the live dev DB in a rolled-back transaction as the storefront role:
```
BEGIN; SET ROLE store;
SELECT has_column_privilege('store','product_answers','is_staff','INSERT');  -- t
INSERT INTO product_answers (question_id,user_id,body,is_staff)
SELECT q.id,NULL,'forged official answer',true FROM (SELECT id FROM product_questions WHERE hidden_at IS NULL LIMIT 1) q
RETURNING id,is_staff;   -- 01a031c0-...-449d014a0d89 | t   INSERT 0 1
ROLLBACK;
```
`migrations/001_initial_schema.up.sql:4646-4649` grants `store` `INSERT (id, question_id, user_id, body, is_staff, created_at)` — `is_staff` is INSIDE the column list. So there is no defence below the Go call site; the column-privilege pattern that the same file uses to keep `store` off `product_reviews.hidden_at` and `return_request_lines`' inspection columns was not applied here.

So: the reviewer's mechanism (authorization as a caller-supplied bool, with nothing underneath it) is REAL and reproduced. The stated consequence ("a customer-side caller could pass true") is FALSE — there is no customer-side caller, no route, and no template. The reviewer's own qualifier is correct and it is the whole story.


## Blast radius

Nobody is harmed today. There is no reachable path from any HTTP request to `product.Store.Answer`, so no customer can obtain the 店家回覆 badge (`internal/ui/pages/product.templ:102`, `ui-badge--brand`). Frequency of exploitation: zero, and it cannot change without somebody adding a route.

What survives is latent, and it has a name in this repository's own vocabulary: the only thing standing between a forged official badge and the storefront is that a handler was never written. Two facts make that more than theoretical:

- CLAUDE.md documents customer replies as a thing that happens — "burying it under three customer replies is the same as not having it" and "'Answered' there means the SHOP answered: three customer replies and no official one is still an unanswered question." So somebody will eventually build the storefront reply form, and the method they will reach for already takes the flag as an argument, sitting three lines under `Ask`, which the handler at internal/product/handler.go:196 already calls with request data.
- `internal/product/integration_test.go:748, 788, 821` already call `s.Answer(..., true)` — the storefront store is *already* used to mint official answers in test code. A future implementer greps for a caller, finds `true` passed against a customer id, and copies it.

Blast radius IF that happens: the badge is the shop's own voice on a 選品店's PDP — a planted "official" answer recommending or disparaging a product, visible to every visitor, and indistinguishable from a real staff reply because `is_staff` is stored and never re-derived (which is correct by design, and is exactly what makes a forged row permanent). It is also silent: no audit row, since `record_audit_event` is only on the admin path.

The mirror risk is the one worth stating plainly: this is dead exported code that reads as a supported capability. It is `Store.Remove`'s shape (shipped with a comment saying "another admin does this" and no caller) and `invoice.Store.Allowance`'s (written with the feature, no interface method, no route) — both of which this repository has already been bitten by.


## Fix

Minimum fix — make the signature unable to express the forgery. This is a compile-time lock, which is stronger than any test.

1. `internal/product/question.go:45-46` — drop the parameter:
```go
// Answer records a CUSTOMER's reply. is_staff is false because of the PACKAGE,
// never of who is signed in: the shop answers through admin.Store.AnswerQuestion,
// which is the only endpoint behind RequireStaff.
func (s *Store) Answer(ctx context.Context, questionID, userID, body string) error {
```
   and at :59-62 replace `IsStaff: staff` with the literal `IsStaff: false`. Do NOT change the SQL — `internal/product/query.sql:276-280` is shared with `internal/admin` through the same generated `db.AnswerQuestionParams`, and `internal/db` is never hand-edited. Nothing else in the function changes; the rune bound, the `uuid.Parse` guards and the `n == 0 → ErrNotFound` (which is what refuses an answer to a hidden question, tested at integration_test.go:821) all stay.

2. Update the six test call sites. Five drop the trailing argument. Three of them pass `true` and are asserting the SHOP's answer, so they must not simply become `false` — that would silently change what they test:
   - `internal/product/integration_test.go:748` — this is the sorting/demotion case (shop answer sorts first; `is_staff` survives the author being demoted to `role='customer'` at :753-756). It needs a genuinely official row.
   - `:788` / `:821` — the hidden-question case; here `is_staff` is incidental and `false` is fine.
   For :748, prefer calling `admin.Store.AnswerQuestion` (no import cycle: neither `internal/admin` nor `internal/product` imports the other, and `internal/admin/integration_test.go` already imports `product`). It needs an actor in context or it returns `ErrNoActor`, so follow the pattern already at `internal/admin/integration_test.go:1992`. If wiring an admin pool into `internal/product`'s suite is disproportionate, the acceptable fallback is a direct `pool.Exec` INSERT with `is_staff = true` and a comment naming why the application cannot produce it — but note that is mistake #26's shape and the better answer is (3).

3. Close the column grant, so the rule holds below Go as well. In `migrations/001_initial_schema.up.sql:4646-4649`, remove `is_staff` from `store`'s list (leave `admin`'s at :4702-4705 intact):
```sql
REVOKE INSERT, UPDATE ON product_answers FROM store;
-- is_staff is the SHOP's statement that the shop said this, like
-- product_reviews.hidden_at and return_request_lines' inspection columns. The
-- storefront writes a customer's reply and takes the DEFAULT false.
GRANT INSERT (id, question_id, user_id, body, created_at),
      UPDATE (id, question_id, user_id, body, created_at)
    ON product_answers TO store;
```
   `is_staff` is `boolean NOT NULL DEFAULT false` (migrations:1147), so omitting it from the INSERT column list is legal — but the sqlc query at query.sql:276 names `is_staff` explicitly and is shared by both roles, so `store` would then be refused. Two ways out, pick one and record it: either split into `AnswerQuestion` (admin, names `is_staff`) and `AnswerQuestionAsCustomer` (storefront, omits the column and takes the DEFAULT), or leave step 3 out and rely on step 1 alone. Step 1 is the fix; step 3 is defence in depth and must not be half-applied — CLAUDE.md records that exact failure ("BOTH verbs need the column list, and INSERT is the easier to forget"). If step 3 is done, re-run `make sqlc`, `make test-integration` (`TestEveryRoleCanRunItsOwnQueries` and `TestEveryRoleCanReadWhatItsQueriesRead` will both read the new grant), and note that `TestEveryColumnIsReadOrWritten` cuts GRANT lists from its corpus so removing the mention is safe.

Do NOT delete the method as dead code. The customer-reply door is a real gap (CLAUDE.md describes customer replies as existing) and deleting it would remove the fixture `internal/admin/integration_test.go:1996` needs to test `/admin/questions`' "answered means the SHOP answered" rule. Queue the storefront reply route by name in `docs/roadmap.md` §A instead; when it is built it must go through the parameterless `Answer`, and the write-face rule applies (plain `<form method="post">`, 303 on success, 422 with values intact).


## The lock, and how to see it fail first

Two locks, and the first one is free.

LOCK 1 — the signature (compile-time). After the fix, `s.Answer(ctx, qID, customer, "body", true)` does not compile: "too many arguments". This is the real lock, and it is proven by the mutation I already ran in reverse — restore the `staff bool` parameter and `go vet -tags integration ./internal/product/` goes from clean to reporting the call sites. Record it as such rather than writing a test that asserts a compiler error.

LOCK 2 — the behaviour (integration, `internal/product/integration_test.go`, package `product_test`, `//go:build integration`). Name it from the CONTRACT, not the implementation — CLAUDE.md records that three round-6 findings were each locked in by a test written from the implementation:

```go
func TestAStorefrontReplyIsNeverBadgedAsTheShop(t *testing.T) {
    ctx := t.Context()
    s := product.NewStore(pool)
    slug := anyActiveProduct(t)
    customer := newCustomer(t)

    if err := s.Ask(ctx, slug, customer, "這台有支援 PD 嗎?"); err != nil { t.Fatalf("ask: %v", err) }
    qID := latestQuestion(t)
    if err := s.Answer(ctx, qID, customer, "我自己實測是可以的。"); err != nil { t.Fatalf("answer: %v", err) }

    var isStaff bool
    if err := pool.QueryRow(ctx,
        `SELECT is_staff FROM product_answers WHERE question_id = $1 ORDER BY created_at DESC LIMIT 1`,
        uuid.MustParse(qID)).Scan(&isStaff); err != nil { t.Fatalf("read back: %v", err) }
    if isStaff {
        t.Error("a storefront reply was stored as the shop's own answer")
    }
}
```
It asserts the raw COLUMN, not the rendered badge and not `pages.Question.AnsweredByShop()` — CLAUDE.md's `delivered_at` lesson: a view-level assertion stayed green with the predicate deleted because the view formats the value away.

HOW TO SEE IT GO RED (the mutation, and it must be SEEN to apply — false-green mode #3): edit `internal/product/question.go:61` from `IsStaff: false` to `IsStaff: true`, confirm with `git diff internal/product/question.go` that the line actually changed (a `grep -c` on a guessed pattern is how the last one lied), then `make test-integration` → this test FAILS with "a storefront reply was stored as the shop's own answer". Revert and confirm green. Do not accept the test until the red run has been observed.

IF step 3 of the fix (the column grant) is taken, add the privilege lock too, in the conformance suite beside `TestNoRoleCanWriteStockDirectly`, which is the existing shape for this:
```
SET ROLE store;
INSERT INTO product_answers (question_id, user_id, body, is_staff) VALUES (..., true);
```
must be refused with SQLSTATE 42501, and the assertion must be bound to the error CODE, not its text (mistake #8/#32 — a `PgError` renders as severity + message + SQLSTATE and `strings.Contains` on it is forbidden by `.claude/rules/error-handling.md`). Prove it by mutation: restore `is_staff` to `store`'s GRANT column list, rebuild the container schema, and watch the case go green — i.e. watch the guard stop guarding.

NOT required: a browser check. `make check-layout` measures GETs only, and no page it visits renders a customer reply form — asserting this there would be mistake #26 in the act of being written.
