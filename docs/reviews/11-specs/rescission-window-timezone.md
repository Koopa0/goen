# rescission-window-timezone

**Verdict** CONFIRMED · **Severity** high · **Origin** found in this round's own sweep (already established)

**Files** migrations/001_initial_schema.up.sql, internal/admin/query.sql, internal/warranty/query.sql, internal/loyalty/query.sql, internal/payment/query.sql, internal/db/query.sql.go, internal/admin/integration_test.go, internal/warranty/integration_test.go, internal/db/shopcalendar_test.go, CLAUDE.md, docs/roadmap.md


## Root cause

The shop's calendar day has no definition. Every deadline goen counts in DAYS — 消保法 §19's seven, a warranty term in months, a point's validity, an order number's business date — needs to know where a day starts, and only ONE of them says: `next_order_number()` writes `(now() AT TIME ZONE 'Asia/Taipei')::date` at migrations/001:1445. Everywhere else the answer is delegated to the session `TimeZone`, which is not a decision anybody in this repository made. It comes from whoever built the connection string, it can differ between the store pool, the admin pool, the maintenance pool, `migrate`, `psql` and testcontainers, and — the part that makes it invisible — `r.created_at::date` reads exactly the same whether the calendar is right or wrong. A reviewer looking straight at line 241 cannot tell.

This is the shape CLAUDE.md names over and over and has no guard for. It is `localized_name` before it existed: one rule ("which name does this reader get" / "when does a day start") re-derived at every call site, where the one that forgot would be whichever was written next. It is also mistake #30 — "a configuration surface sitting next to an invariant that depends on the configuration being narrow" — with the configuration surface being the DSN and the invariant being an unwaivable statutory window. And it is #31's reading exactly: the comment above line 241 argues correctly and at length about which two clocks to compare ("both database clocks") and never asks which CALENDAR, so the sentence describing the care taken sits directly above the line that does not take it.

The deeper error is that `+ 7` and `+ interval '24 months'` are calendar arithmetic performed on a value that is not a calendar day. `::date` on a timestamptz is a lossy projection whose result depends on ambient state; the code treats it as a total function.


## Reproduction — the evidence this rests on

EXECUTED, not reasoned. Every cited line still says what the summary claims, and the divergence reproduces in both directions.

1) The line, verbatim. `internal/admin/query.sql:241` (`sed -n 229,244p`):

    -- rescission_window: Consumer Protection Act §19 I runs seven days from RECEIPT,
    -- Civil Code §120 II excludes the day of receipt, and §19 IV fixes the moment on
    -- the customer's side — so created_at against delivered_at, both database
    -- clocks. Undelivered is neither answer, because the window has not started.
    -- name: ReturnQueue :many
    ...
        (CASE
             WHEN d.delivered_at IS NULL THEN 'undelivered'
             WHEN r.created_at::date <= d.delivered_at::date + 7 THEN 'within'
             ELSE 'after'
         END)::text AS rescission_window

Both operands are timestamptz (`return_requests.created_at`, migrations/001:2082; `max(order_shipments.delivered_at)` via the LATERAL at 246-249). Neither carries a zone.

2) The session zone is UTC, measured:

    $ psql "$GOEN_DATABASE_URL" -c "show timezone;"
     TimeZone
    ----------
     UTC

3) `Asia/Taipei` appears EXACTLY ONCE in the whole tree — grep over *.go, *.sql, *.templ excluding internal/db:

    migrations/001_initial_schema.up.sql:1445:    today := (now() AT TIME ZONE 'Asia/Taipei')::date;

That is `next_order_number()`. There is no TimeZone setting in any pool, DSN, Makefile or .env; grep for `TimeZone|LoadLocation|timezone` outside internal/db returns nothing else.

4) The divergence, run in a transaction and rolled back — the reviewer's exact case:

    delivered_at = 2026-08-25 07:00+08, created_at = 2026-09-01 12:00+08
     utc_session_answer | taipei_answer | tpe_delivered_day | tpe_request_day | utc_delivered_day | utc_request_day
     f                  | t             | 2026-08-25        | 2026-09-01      | 2026-08-24        | 2026-09-01

The shifting operand is the DELIVERY: 07:00 Taipei is 23:00 UTC the day before, so the window's start moves back a day and it closes on 31 Aug instead of 1 Sep. Counting the way §120 II requires (day of receipt excluded; 26 Aug … 1 Sep), the request is on the seventh day — inside an unwaivable right, reported as 已逾期.

5) It is TWO-directional, which the summary does not say. Also executed and rolled back:

    delivered_at = 2026-08-25 12:00+08, created_at = 2026-09-02 06:00+08
     utc_answer | taipei_answer
     t          | f

06:00 Taipei is 22:00 UTC the previous day, so a request one day LATE reads 鑑賞期內. No constraint catches that either; it is a refund the shop was not obliged to give, on the same screen.

WHAT THE SUMMARY GOT WRONG / UNDERSTATED — two things:

(a) It is not the only site, and two of the others WRITE a permanently wrong date rather than merely rendering one. `internal/warranty/query.sql:40` computes `(delivered.at + make_interval(months => p.warranty_months))::date` into `warranty_registrations.expires_on` (date NOT NULL, migrations/001:2250), and `internal/payment/query.sql:106` passes `(current_date + @validity_days::integer)` into `award_loyalty_points(...)` writing `loyalty_entries.expires_on` — a table guarded by `loyalty_entries_append_only` (migrations/001:4168), so a day-short expiry can never be corrected. Full census in `fix`.

(b) There is currently NO test on the rescission window at all. `TestTheReturnQueueShowsWhatIsComingBack` (internal/admin/integration_test.go:2649) is the only ReturnQueue test and asserts SKU/name/quantity only; its fixture `returnedOrder` (line 541) never stamps `delivered_at`, so every row it produces reads `'undelivered'` and the CASE arm under discussion is not executed. Nothing to un-assert — but also nothing that would have gone red.

Working tree was clean before and after; verified with `git status --porcelain` (empty).


## Blast radius

WHO: every customer exercising 消保法 §19's unwaivable seven-day rescission whose parcel was handed over between 00:00 and 08:00 Taipei time — the morning delivery slot, which for 宅配 and 超商取貨 is a large share of handovers. For those, `/admin/returns` states the window closed a day before it does. The staff member reading 已逾期 is being told the shop is free to refuse, on the ONE screen the repository built to inform that decision, and CLAUDE.md already records what that costs: "The one screen built to inform an unwaivable-right decision was misinforming it, in the shop's favour."

Mirror direction: a request logged between 00:00 and 08:00 Taipei on the day AFTER the window shut reads 鑑賞期內, and the shop refunds something it was not obliged to.

HOW OFTEN: not a race and not rare. It is deterministic for any (delivery, request) pair where either moment falls in the 8-hour band 00:00–08:00 Taipei — roughly a third of the clock on each side. It has been true since the query was written.

SILENT: totally. No constraint, no trigger, no log, no test observes it. The value renders as a Chinese or English phrase (`pages.AdminReturn.WindowText`), and 鑑賞期內 / 已逾期 look equally plausible either way. `Decide` accepts any answer regardless (CLAUDE.md: "It INFORMS and does not restrict"), so a wrong label produces a lawful-looking refusal with no artefact anywhere saying the screen misread the calendar.

BEYOND THE ONE LINE — the same defect, same cause, different consequence:
- `warranty_registrations.expires_on` written a day short for any parcel delivered 00:00–08:00 Taipei. WRITTEN ONCE and permanent: the unique keys on (order_line_id, unit_no) and the partial serial key mean the customer cannot re-register to correct it. Every warranty claim from then on is decided against a stored date the shop cannot see is wrong.
- `loyalty_entries.expires_on` likewise, and that table is append-only by trigger, so there is no correcting write at all.
- `loyalty_balances` (migrations/001:4177) — the single definition of the spendable points balance — treats points as live for up to 8 hours past their Taipei expiry, in the customer's favour.
- Warranty `in_force` on both the customer's page and `/admin/warranty` shows 保固中 for up to 8 hours past expiry.

None of the secondary sites moves money incorrectly by more than one day's grace; the warranty and points WRITES are the ones that persist, and the rescission label is the one with a statute behind it.


## Fix

DECISION: fix in the SCHEMA, as one named function, and call it from every site. Not the pool, not a table.

Why not `TimeZone` on the pool (via DSN param or `ConnConfig.RuntimeParams`):
 - It puts a statutory rule in a configuration surface an operator edits, which is mistake #30 by name. `GOEN_DATABASE_URL` is environment-only; a deployment appending its own `?timezone=` silently reopens the finding.
 - It must be repeated for the store pool, the admin pool, the maintenance pool, `make migrate-up`, testcontainers' `TestMain`, and the `psql` seeds in `make check-layout` — six places, and the one that forgets is whichever is written next. That is the argument `committed_orders` and `store_credit_balances` are views for, applied to a setting instead of a predicate.
 - Worst: it is invisible at the call site. Line 241 reads identically before and after, so nothing in the SQL states the rule and no reviewer can check it. `next_order_number()` already chose the opposite and was right to.
Why not a shop-timezone table: a one-row table read by a subquery in every date expression, editable from a back office, and unusable in an IMMUTABLE context (no CHECK or expression index could ever call it). There is no second shop; this is the table-with-no-door shape in reverse.

── A. The definition (migrations/001_initial_schema.up.sql) ─────────────────
`001` is amended in place — CLAUDE.md's escape clause, "goen has not been deployed". Insert immediately ABOVE `CREATE TABLE order_number_counters` (currently line 1432, so `next_order_number()` at 1439 can call it), following `localized_name`'s template at line 128 verbatim in style:

    -- ---------------------------------------------------------------------------
    -- The shop's calendar. Every deadline goen counts in DAYS is the same
    -- question — 消保法 §19's seven days, a warranty term, a point's validity,
    -- an order number's business date — and the session TimeZone is not an
    -- answer to it: it is set by whoever built the connection string, it can
    -- differ between the store pool, the admin pool, migrate, psql and
    -- testcontainers, and `at::date` reads identically whichever calendar is in
    -- force, so nobody reviewing a call site can tell. The zone is written HERE,
    -- once, for the reason committed_orders and store_credit_balances are views.
    --
    -- 台灣 has kept no DST since 1979 and timezone(text, timestamptz) is itself
    -- marked IMMUTABLE by PostgreSQL, so this is too.
    CREATE FUNCTION shop_day(at timestamptz)
    RETURNS date
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
    SET search_path = pg_catalog, public, pg_temp
    AS $$
        SELECT (at AT TIME ZONE 'Asia/Taipei')::date;
    $$;

    COMMENT ON FUNCTION shop_day(timestamptz) IS
        'The calendar day a moment falls on for this shop. One definition: a '
        'statutory window counted in one zone and a warranty expiry written in '
        'another is the failure this prevents.';

    CREATE FUNCTION shop_today()
    RETURNS date
    LANGUAGE sql
    STABLE
    PARALLEL SAFE
    SET search_path = pg_catalog, public, pg_temp
    AS $$
        SELECT shop_day(now());
    $$;

    COMMENT ON FUNCTION shop_today() IS
        'Today, on the shop''s calendar. Replaces current_date wherever a '
        'deadline is read or written; current_date answers in the session '
        'TimeZone, which no deployment here states.';

    -- Their GRANTs are far below: admin and reporting do not exist yet here, and
    -- a GRANT naming a role the file has not created fails the migration.

STABLE, not IMMUTABLE, for `shop_today` — it reads `now()`. Neither is SECURITY DEFINER: they read no table, and the README's SECURITY DEFINER count is asserted by `TestTheStatedSchemaTotalsAreTheRealOnes`; marking them would break it for nothing. The DO block at the foot of the file will `REVOKE EXECUTE ... FROM PUBLIC` on both, which is correct and is why B2 exists.

── B. Make the one already-correct site the first caller ────────────────────
B1. migrations/001:1445, inside `next_order_number()`:
      today := (now() AT TIME ZONE 'Asia/Taipei')::date;
    →
      today := shop_today();
    This is the point of the whole change. Leaving the literal there keeps a second copy, which is the drift being removed.

B2. Beside `localized_name`'s grant at migrations/001:3991 (after all roles exist), with the same comment that sits above it:
      GRANT EXECUTE ON FUNCTION shop_day(timestamptz) TO store, admin, reporting;
      GRANT EXECUTE ON FUNCTION shop_today() TO store, admin, reporting;
    `maintenance` is omitted deliberately — `refresh_copurchases` touches no date. If `TestEveryRoleCanRunItsOwnQueries` reports a 42501 for maintenance, add it there rather than guessing now.

── C. The full census, and what each one gets ───────────────────────────────
Derived by grepping `::date`, `current_date`, `CURRENT_DATE`, `date_trunc`, `AT TIME ZONE` across migrations/ and every query.sql in sqlc.yaml (internal/db excluded — generated). This is every one:

 1. migrations/001:1445 `next_order_number()` — COMMERCIAL (the business date in every order number). Already correct; becomes `shop_today()` per B1.
 2. internal/admin/query.sql:241 `ReturnQueue` — **LEGAL** (消保法 §19 I, 民法 §120 II). THE FINDING.
 3. internal/warranty/query.sql:40 `RegisterWarranty` — COMMERCIAL DEADLINE, **WRITTEN AND PERMANENT** into `warranty_registrations.expires_on`.
 4. internal/warranty/query.sql:60 `MyWarranties` — commercial deadline, read (`in_force`).
 5. internal/admin/query.sql:1396 `AdminSearchWarranties` — same, back-office read.
 6. internal/payment/query.sql:106 `AwardOrderPoints` — COMMERCIAL DEADLINE, **WRITTEN AND PERMANENT** into `loyalty_entries.expires_on` (append-only trigger: uncorrectable).
 7. internal/loyalty/query.sql:19 `PointsHistory` — CHROME (an "expired" badge).
 8. internal/loyalty/query.sql:32,38,39 `PointsExpiringSoon` — CHROME (a "expiring soon" warning).
 9. migrations/001:4177, view `loyalty_balances` — COMMERCIAL: the single definition of the spendable balance.
10. internal/warranty/integration_test.go:280-281 — a LOCK that currently agrees with the defect; moves with #3 (see tests_required).

Not affected, checked and named so nobody re-opens them: every `now() ± interval` / `make_interval(days => N)` in internal/outbox, internal/cart, internal/account, internal/twofactor, internal/media and the `/admin/reports` windows is DURATION arithmetic on timestamptz and is zone-independent; so is `/admin/messages`' `floor(extract(epoch FROM now() - created_at) / 86400)` at internal/admin/query.sql:1063, which CLAUDE.md documents as elapsed-time-over-86400 on purpose.

Edits:
 C2 internal/admin/query.sql:241 →
      WHEN shop_day(r.created_at) <= shop_day(d.delivered_at) + 7 THEN 'within'
    KEEP the `+ 7`. §120 II excludes the day of receipt, so receipt-day + 7 IS the last day; that arithmetic was never wrong and a "fix" to `+ 6` would be a new defect.
    Extend the comment at lines 229-232, whose closing clause is now false by omission. Replace "…so created_at against delivered_at, both database clocks." with: "…so created_at against delivered_at, both database clocks — and both on the SHOP's calendar, through shop_day. `::date` answers in the session TimeZone, which is UTC here and stated nowhere: a parcel handed over at 07:00 Taipei is the previous day in UTC, which closes an unwaivable window a day early."
 C3 internal/warranty/query.sql:40 →
      (shop_day(delivered.at) + make_interval(months => p.warranty_months))::date
    `date + interval` yields a zone-free `timestamp`, so the trailing `::date` has nothing left to shift. Add to the comment above it (lines 36-38) that the term runs from the shop's calendar day of delivery.
 C4 internal/warranty/query.sql:60 and internal/admin/query.sql:1396 →
      (w.expires_on >= shop_today())::boolean AS in_force
 C5 internal/payment/query.sql:106 →
      (shop_today() + @validity_days::integer)
 C6 internal/loyalty/query.sql — all four `current_date` at lines 19, 32, 38, 39 → `shop_today()`.
 C7 migrations/001:4177, inside the `loyalty_balances` view →
                   WHERE e.points < 0 OR e.expires_on >= (SELECT shop_today())), 0)::bigint AS points
    The `(SELECT ...)` wrapper is deliberate and needs its own comment line: the DO block at the foot of this file puts `SET search_path` on every function, and a LANGUAGE sql body carrying a SET clause cannot be inlined, so a bare `shop_today()` in this predicate is one call per LEDGER ROW over an unbounded table. A scalar subquery becomes an InitPlan and is evaluated once. VERIFY that claim rather than writing it: `EXPLAIN (ANALYZE, BUFFERS) SELECT * FROM loyalty_balances` before and after, and if the plan does not show an InitPlan, correct the comment to say what it does show. The other eight sites are bounded (a 50-row queue, one order's warranties) and need no wrapper.

── D. Regenerate, do not hand-edit ─────────────────────────────────────────
`internal/db` is sqlc output and is never edited by hand. Run `make sqlc`, then `make sqlc-check` — and note mistake #12: `sqlc-check` used to repair what it checked, so run `make verify` TWICE and believe the second (mistake #22). No struct changes are expected: `rescission_window` stays `::text`, `expires_on` stays `date`, `in_force` stays `::boolean`, `soonest` stays `coalesce(...)::date`. If sqlc emits a nullability or type change anywhere, a cast was dropped — fix the .sql, never internal/db.

── E. Dev database and drift ───────────────────────────────────────────────
`make db-reset` (or `DROP DATABASE goen` + `make migrate-up`) — amending `001` means PostgreSQL does not re-validate. Then `make schema-drift` if any live DB exists. `make test-integration` rebuilds from `001` every run, so nothing else is needed there.

── F. Document it ──────────────────────────────────────────────────────────
Add to CLAUDE.md, in the paragraph that already argues `committed_orders`, `store_credit_balances`, `localized_name` and `visible_reviews` are single definitions: `shop_day`/`shop_today` are the shop's calendar, `current_date` and `at::date` are forbidden in application SQL, and the reason — a legal deadline counted in a zone nobody stated. Name the guard from tests_required beside it. Do NOT state a new function count anywhere; `TestTheStatedSchemaTotalsAreTheRealOnes` asserts CHECKs, FKs, unique indexes, rule triggers, set_updated_at triggers and SECURITY DEFINER functions, none of which this changes.

── G. Queued by name, NOT in this fix ──────────────────────────────────────
`time.Time.Format("2006-01-02 15:04")` at ~25 Go call sites (internal/admin/store.go:126,153,209,221,370,794,1347,1358,1390; health.go:57,72,90,107; audit.go:188; review.go:25; message.go:26; warranty.go:36,37; catalog/campaign.go:43,61) renders in the SERVER PROCESS's local zone. A container running UTC shows a Taipei shop every timestamp eight hours out. It is display only — no decision derives from it — but it is the same unstated calendar. File it in docs/roadmap.md as a named item ("back-office timestamps render in the process zone, not the shop's"), per .claude/rules/review-process.md: queued by name, never a mental note.


## The lock, and how to see it fail first

Three locks. Each is proven by mutation, and each mutation must be SEEN to apply (CLAUDE.md false-green mode #3: an edit that matched nothing once reported as applied by a `grep -c` on the wrong pattern).

═══ LOCK 1 — the finding. internal/admin/integration_test.go ═══
New `TestTheRescissionWindowIsCountedOnTheShopsCalendar`, both directions as subtests.

FIXTURE. `returnedOrder` (line 541) never stamps `delivered_at`, so every row it makes reads 'undelivered' and the CASE arm under test does not execute — reusing it as-is is a test that cannot fail. Add a sibling helper `returnedOrderAt(t *testing.T, delivered, requested time.Time) uuid.UUID` that does what `returnedOrder` does at lines 552-601 and additionally:
  - `INSERT INTO order_shipments (..., delivered_at) VALUES (..., $delivered)`. The fixture writes it directly; `applyStatusEffects` is the production writer and this test is about the READ.
  - inserts `return_requests` with an explicit `created_at = $requested`. Legal and verified: `return_requests_check_initial` (migrations/001:2226) guards only `status`, `return_requests_recount` is `BEFORE UPDATE OF status`, and there is no freeze trigger on the table. The suite connects as the owner.
  - tracking number must stay unique per run (`order_shipments_tracking_key`) — CLAUDE.md mistake #26: a fixed tracking number ships exactly once ever and every later run passes on the first run's row. Suffix with the order number as `returnedOrder` already does.

THE TWO CASES, and these exact hours are the test's whole value:
  "a parcel handed over in the Taipei morning": delivered `2026-08-25 07:00:00+08`, requested `2026-09-01 12:00:00+08` → Window must be "within" and `found.Rescission()` true. Under the defect: "after".
  "a request made in the Taipei small hours, one day late": delivered `2026-08-25 12:00:00+08`, requested `2026-09-02 06:00:00+08` → Window must be "after". Under the defect: "within".
Read the row back through `admin.NewStore(pool, fakeRefunder{}, nil).Returns(ctx)` and match on the request id, the way `TestTheReturnQueueShowsWhatIsComingBack` does at line 2649 — assert the view model's `Window`, not the raw SQL, so the store mapping at internal/admin/store.go:797 is inside the lock.

**THE FALSE-GREEN TO AVOID — write this into the test, next to the constants.** A fixture whose two timestamps both sit in the middle of a Taipei day (12:00 and 12:00, say) has `shop_day(x) = x::date` for both operands, so the assertion is satisfied identically by the defect and by the fix and the test proves nothing. 07:00 Taipei is 23:00 UTC the previous day and 06:00 Taipei is 22:00 UTC the previous day; those are the only reason either case discriminates. Anyone moving either hour into daylight disarms the test silently. Say so in the comment, and say which UTC instant each constant is.

The second case exists because a one-directional test passes on a "fix" that shifts everything the same way. Both must be present or the pair is not a lock.

MUTATIONS, both must go RED:
  M1 (the whole fix): restore `internal/admin/query.sql:241` to `r.created_at::date <= d.delivered_at::date + 7`, run `make sqlc`, and confirm `git diff --stat internal/db/query.sql.go` shows the change — this is the seen-to-apply step, do not assume it. Then `go test -tags integration -run TestTheRescissionWindowIsCountedOnTheShopsCalendar ./internal/admin/`. BOTH subtests must fail. Restore, regenerate, re-run green.
  M2 (the half-fix, which is what an implementer actually ships by accident): apply `shop_day` to the `created_at` side only, leave `d.delivered_at::date`. Regenerate. The "Taipei morning" subtest must fail. Record both mutations in the PR body.

═══ LOCK 2 — the permanent write. internal/warranty/integration_test.go ═══
Lines 280-281 currently compute the expectation as `s.delivered_at::date + interval '24 months'` — the assertion agrees with the defect, so it would stay green through the fix and through its absence. That is the shape CLAUDE.md records three times over ("a test written from the implementation asserts what the code does").

Rewrite: stamp the fixture's `delivered_at` at `07:00+08` (two days after `shipped_at`, which the existing fixture already separates for the reason recorded in CLAUDE.md — keep that), and assert:
  positive: `w.expires_on = (shop_day(s.delivered_at) + interval '24 months')::date`
  negative 1 (KEEP — CLAUDE.md records why it exists): `w.expires_on <> (shop_day(s.shipped_at) + interval '24 months')::date`
  negative 2 (NEW): `w.expires_on <> (s.delivered_at::date + interval '24 months')::date` — the defect's own answer, which the `07:00+08` stamp makes a different date.
Without negative 2 the test cannot distinguish the fix from the defect at all.
MUTATION: revert internal/warranty/query.sql:40, `make sqlc`, confirm the generated line changed, run the test, watch it go red on negative 2.

═══ LOCK 3 — the census, so it cannot regrow ═══
New unit test (no Docker), `internal/db/shopcalendar_test.go`, deriving its corpus from the files the way `TestEveryCategoryNameIsLocalized` and `TestEveryCreditBalanceReadsTheOneView` do — never from a hand-written list.

`TestEveryCalendarDayGoesThroughTheShopsCalendar` walks `migrations/*.up.sql` and every `query.sql` named in sqlc.yaml, strips SQL comments FIRST (the `splitQueries` lesson: a comment carries the next query's introduction, and comments here legitimately discuss `current_date`), and refuses:
  - any `current_date` / `CURRENT_DATE`;
  - any `::date` applied to an identifier ending `_at` or named `at` — coarse and true beats precise and unwritten, and the limit goes in the test's own doc comment: it will not catch a timestamptz column named otherwise;
  - `AT TIME ZONE 'Asia/Taipei'` anywhere outside `shop_day`'s own body — the literal IS the definition and a second copy is exactly the drift being closed.
Report file and line. The allowlist is checked by IDENTITY with a reason string per entry, never by count — comparing totals passes an entry naming a query that no longer exists, which is how `TestEveryCategoryNameIsLocalized` shipped two entries added on a guess. It must be EMPTY when this lands.
MUTATION: put `current_date` back at internal/loyalty/query.sql:38 and confirm the failure names that file and that line, not just a count.

═══ Gates ═══
`make verify` twice (mistake #22), then `make test-integration` — which shuffles (mistake #23), so the new fixture must create everything it needs and share nothing.
