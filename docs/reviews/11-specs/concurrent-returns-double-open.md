# concurrent-returns-double-open

**Verdict** PARTLY · **Severity** high · **Origin** external report (verified this round)

**Files** internal/returns/store.go, internal/returns/query.sql, migrations/001_initial_schema.up.sql, internal/admin/store.go, internal/db/cases_integration_test.go, internal/db/coverage_integration_test.go, internal/db/fixtures_integration_test.go, internal/returns/integration_test.go, internal/admin/integration_test.go, CLAUDE.md, README.md


## Root cause

`returns.Store.Open` asks "does this order already have an open request?" on the pool, then answers it again nowhere. The rule lives only in application code, where a second write path — a second concurrent request — walks past it; the schema, which is the one place with no second path, has an index for FINDING open requests (`return_requests_open_idx`) and none for LIMITING them.

`return_refundable_amount` then compounds it by answering an ORDER-level question ("is this a full rescission?") into a REQUEST-level figure. Its coverage test scans every non-rejected request on the order, so the moment two of them jointly complete the order each one individually reads as the request that completed it. That is sound exactly while at most one request is open at a time — an assumption it inherits from Go rather than from the schema.


## Reproduction — the evidence this rests on

EXECUTED, not reasoned. Two temporary integration tests (both deleted; `git status --porcelain` shows only two other agents' probes in internal/loyalty and internal/ui/pages, packages I never touched).

FINDING A — two open requests coexist: REPRODUCED.
`internal/returns/store.go:106-116` reads the guard on the POOL, before its own transaction opens:
    o, err := s.Order(ctx, number)      // HasOpenReturn on s.pool
    ...
    if o.HasOpen { return ErrAlreadyOpen }
    ...
    tx, err := s.pool.Begin(ctx)        // line 138
`internal/returns/query.sql:41-44` is `SELECT EXISTS (SELECT 1 FROM return_requests WHERE order_id=$1 AND status='requested')`, and `migrations/001_initial_schema.up.sql:2094-2097` carries only:
    CREATE INDEX return_requests_order_id_idx ON return_requests (order_id);
    CREATE UNIQUE INDEX return_requests_order_key ON return_requests (order_id, id);
    CREATE INDEX return_requests_requester_idx ON return_requests (requested_by_user_id);
    CREATE INDEX return_requests_open_idx ON return_requests (created_at) WHERE status = 'requested';
— no partial unique index on (order_id) WHERE status='requested'.

Probe (temporary `internal/returns/zz_probe_integration_test.go`, run under `-tags integration` against the testcontainer schema + dev seed): a fully shipped two-line order (2 x NT$1,000, shipping NT$150). T1 opened a pgx transaction and inserted request A + its line for line 1 via `db.Queries.WithTx` (the same statements Store.Open uses) and was HELD OPEN. A goroutine then called the real `returns.Store.Open` on the pool for line 2. Per rule #9 the overlap was proven rather than hoped for: a `select` asserted the goroutine had NOT returned after 800 ms (it was blocked inside its own transaction on `return_lines_within_purchase`'s `FOR UPDATE OF o`). T1 then committed and T2 returned nil.

    === RUN   TestProbeConcurrentOpen
        OPEN REQUESTS ON ONE ORDER: 2
        request 01a031bd-efdf-... reason=A refundable=115000
        request 01a031bd-efe9-... reason=B refundable=115000
        SUM REFUNDABLE=230000  ORDER TOTAL OWED=215000  shipping=15000
    --- PASS

So `ErrAlreadyOpen` is an invariant Go states and nothing enforces. The trigger's order-row lock serialises the two writers but only checks the per-LINE shipped ceiling (`migrations/001:2168-2175` excludes the row's own request), so two complementary requests both pass.

FINDING B — shipping fee allocated twice: REPRODUCED IN THE FUNCTION. `return_refundable_amount` (`migrations/001:3607-3616`) adds `o.shipping_cents` when NO order line is uncovered, counting coverage over ALL non-rejected requests on the order — a question about the ORDER, answered into EACH request. Above, both requests are valued at 115000 (goods 100000 + fee 15000); the pair claims 230000 against an order owing 215000.

FINDING B's STATED CONSEQUENCE — "the shipping fee can be refunded twice": REFUTED. Second temporary probe (`internal/admin/zz_probe_integration_test.go`) drove the real `admin.Store.Decide` over that exact state (order captured 215000 on the card, no discount, no credit):

    === RUN   TestProbeDoubleShipping
        decide A: refundable=115000 err=<nil>
        decide B: refundable=115000 err=admin: refused: this order captured 215000 on the card, 115000 is already refunded and 100000 remains; 0 of store credit was spent, 0 returned and 0 remains. Refunding 115000 does not fit across the two
        CAPTURED=215000 TOTAL REFUNDED=115000  returns: B=requested A=approved
    --- PASS

`splitRefund` (`internal/admin/store.go:934-985`) caps the card at `captured - alreadyRefunded` and credit at `spent - returned`, and refuses the whole claim when the two do not cover it; `refunds_within_capture` stands behind that. Money cannot leave twice. `tax_cents` is written as a literal 0 by `CreateOrder` (`internal/cart/query.sql:146-152`), so there is no headroom anywhere in the arithmetic for both fees to fit.

What survives instead is worse than untidy and different from the claim: the SECOND return becomes unpayable by any door. The customer sent both items back, was refunded 115000 of the 215000 they paid, and request B sits at 'requested' with a message naming no way out — approve refuses, and the only other move is to reject a return whose goods are on the shop's shelf.

Sequential returns are CORRECT and I checked that too: A alone (line 1) is valued 100000 because line 2 is still uncovered; B afterwards is valued 115000; the pair sums to exactly the capture. The over-allocation is reachable only through the missing open-request guard, which is why these are one defect and not two.


## Blast radius

A customer submitting the return form twice a few milliseconds apart with DIFFERENT line selections — two tabs, a re-submit after a slow response, a double-click on a form whose selection changed — is the whole trigger. `POST /orders/{number}/return` (cmd/goen/server.go:178) is open to the browser that placed the order and to its owner; nothing above it serialises per order.

Whichever request a staff member decides FIRST is paid its goods plus the entire delivery fee. The other is then permanently unpayable: `/admin/returns` shows it claiming a figure larger than what remains capturable, `Decide` refuses with the full arithmetic and no remedy, and the return stays open forever. The customer who returned everything is short one line's price with the goods already back at the shop. Nothing counts this anywhere — `/admin/health` measures the outbox, holds and the projection, not stuck returns — so the shop finds out when the customer asks, which is the same discovery path the unreconciled-webhook flag was added to remove.

Not silent to the operator (they see a refusal), but silent to every gate: every guard in this repository asks what is ABSENT, and this is two correct halves disagreeing — a Go pre-check and a schema that never learned about it, which is mistake #13/#30/#31's shape. Frequency is low (a millisecond-wide window on a customer-initiated form) and the money ceiling holds, which is why this is high and not critical.


## Fix

One rule, in the database, plus the error mapping and the bookkeeping the repository's own guards will demand.

1. `migrations/001_initial_schema.up.sql`, beside the other `return_requests` indexes (immediately after line 2097, and well before the trailing privilege `DO` block, which stays last — mistake #19):

    -- A second request while one is undecided is refused HERE, not in Go.
    -- returns.Open reads HasOpenReturn on the pool before its own transaction
    -- begins, so two submissions milliseconds apart both read "none open" and
    -- both commit — and return_refundable_amount then allocates the delivery
    -- fee to each of them, because "is this a full rescission" is a question
    -- about the ORDER.
    CREATE UNIQUE INDEX return_requests_one_open
        ON return_requests (order_id) WHERE status = 'requested';

   This is also the serialisation: the loser's INSERT blocks on the winner's uncommitted key and then raises 23505, so the two cannot interleave past it. `001` is amended in place per the file's own escape clause ("goen has not been deployed"); `make db-reset` and `make schema-drift` afterwards.

2. `internal/returns/store.go`, in `Open`, around the `CreateReturnRequest` call at lines 145-151. On error, unwrap to `*pgconn.PgError` and return `ErrAlreadyOpen` when `ConstraintName == "return_requests_one_open"`; everything else keeps the current wrap. Bind on `PgError.ConstraintName` — NEVER `strings.Contains(err.Error(), …)`, which is mistake #32 and forbidden by `.claude/rules/error-handling.md`. Keep the existing pre-check at line 115: it is what renders the ordinary 422 with the form intact; the index is the authority, not the message. `internal/returns/handler.go:88` already maps `ErrAlreadyOpen`, so no handler change.

3. Do NOT touch `return_refundable_amount`. With at most one open request per order, its coverage test only ever counts DECIDED requests plus this one, and the sequential split rescission it then produces was measured correct (100000 then 115000, summing to the capture). Changing both would make it impossible to say which half held.

4. Bookkeeping the existing guards force, all of which fail the build if skipped:
   - `TestTheStatedSchemaTotalsAreTheRealOnes` reads both documents: bump "62 unique indexes" to 63 at `CLAUDE.md:1220` and `README.md:114`.
   - `TestEveryUniqueConstraintIsExercised` (`internal/db/coverage_integration_test.go:137`) derives its corpus from `pg_index`, so `uniqueCases` in `internal/db/cases_integration_test.go` (the list starts at line 1345) needs an entry for `return_requests_one_open`: `reject` inserts two requested rows on one order, `accept` inserts one. Pick an order id from `fixtures_integration_test.go` that has NO open return — order `66666666-…` already carries one (`fixtures_integration_test.go:172`).
   - That same fixture row breaks three EXISTING cases, which is the trap in this change: `cases_integration_test.go:1110`, `:1117` and `:1124` each insert a fresh `requested` return on order `66666666-…` and will now collide with the fixture's. Repoint them at an order with no open request, or give them a decided status where the case allows.
   - `internal/admin/integration_test.go:1201` inserts a SECOND return on an order whose first is already `approved`; the partial predicate spares it, so leave it alone.


## The lock, and how to see it fail first

Two locks, each proven RED before the fix. The mutation for lock 1 is not hypothetical — it is today's tree, and the output is quoted in the reproduction above.

LOCK 1 — `internal/returns/integration_test.go`, `TestOneOpenReturnPerOrder`.
Fixture: extend `shippedOrder` (line 45) or add a sibling writing a fully shipped TWO-line order; `order_lines` needs an explicit `position` (`order_lines_position_key` is unique on `(order_id, position)`) or the insert dies on a duplicate key.
Body:
  - `tx1 := pool.Begin(ctx)`; with `db.New(pool).WithTx(tx1)` insert request A and its line for line 1. Do not commit.
  - goroutine: `err := returns.NewStore(pool).Open(ctx, number, uuid.NullUUID{}, &returns.Request{Lines: map[string]int32{line2: 1}})` into a channel.
  - `select { case <-done: t.Fatal("T2 finished before T1 committed") ; case <-time.After(500*time.Millisecond): }`. This assertion IS the test's claim to be a concurrency test — without it two goroutines and a start channel finish microseconds apart and never overlap (mistake #9), and the whole thing goes green with the index dropped.
  - Commit T1. Assert `errors.Is(<-done, returns.ErrAlreadyOpen)` — the sentinel, not "an error happened" (#8) — and assert `SELECT count(*) FROM return_requests WHERE order_id=$1 AND status='requested'` equals 1.
Mutation, seen to apply (#20's lesson: the edit must be observed, not assumed): delete the `CREATE UNIQUE INDEX return_requests_one_open` line from `001` and re-run. RED, and already measured: 2 open requests and a nil error from `Open`.

LOCK 2 — `internal/db/cases_integration_test.go`, the `uniqueCases` entry for `return_requests_one_open`, which `TestUniqueConstraintsReject` (`coverage_integration_test.go:158`) runs. Its `reject` must insert TWICE — one insert cannot collide with itself, the note already on `loyalty_entries_idempotency_key` (line 1364) — and `TestUniqueConstraintsReject` binds the failure to the index name. `accept` must insert exactly one requested return on an order that has none, or it proves nothing (mistake #28: a statement both versions of the rule accept).

LOCK 3 (recommended, holds the arithmetic the index protects) — `internal/admin/integration_test.go`, `TestTheDeliveryFeeIsPaidBackOnce`. Two-line order, shipping NT$150, captured in full. Approve a return of line 1 (assert 100000, no fee — line 2 is still out there), then open and approve a return of line 2 (assert 115000), then assert the sum of `refunds.amount_cents` equals the capture exactly. Mutation: make `return_refundable_amount` add `o.shipping_cents` unconditionally — RED at the first assertion, 115000 instead of 100000, and red again on the sum. Do not assert only the total: a coarser assertion than the error it sits beside is not a weak lock but no lock (#34).
