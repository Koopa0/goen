# stripe-session-cancel-race

**Verdict** CONFIRMED · **Severity** high · **Origin** external report (verified this round)

**Files** internal/payment/handler.go, internal/payment/store.go, internal/payment/payment.go, internal/payment/integration_test.go, internal/cart/cancel.go, internal/cart/handler.go, internal/cart/query.sql, internal/admin/store.go, migrations/001_initial_schema.up.sql


## Root cause

`open_payment` is the only writer of a live Checkout Session reference and it asks nothing about the order it is attaching to. The cancellation path's correctness rests entirely on `OpenSessionsForOrder` seeing every session that can still take money — but that query reads `payments`, and a session can be payable at Stripe for a whole network round trip before any `payments` row exists. So the set the cancelling transaction "saw" is not the set of payable sessions; it is the set of sessions goen has finished recording.

Nothing serialises the two paths. `Start` runs entirely outside a transaction and takes no lock on the order; `Cancel` locks the order row but reads the session list from a table the other path has not written yet. This is CLAUDE.md's own recurring shape — two correct halves that disagree, invisible to every guard here because they all ask what is ABSENT — and it is specifically the "claim before the effect" rule (#15) inverted: the effect (a payable session at Stripe) is created before the claim (the `payments` row) that makes it visible to everything else.

The trigger that states the rule cannot reach the INSERT: `payments_require_complete_order` short-circuits on `NEW.status <> 'succeeded'`, so `payments_refuse_cancelled_order` is a rule about CAPTURE only, and its comment ("Otherwise the two-tab sequence goes through: start a payment, cancel in the other tab, pay at Stripe") describes exactly one of the two orderings — the one where the row already exists.


## Reproduction — the evidence this rests on

EXECUTED against the live dev database (psql, both runs inside a transaction that was ROLLED BACK). The interleaving is the one the claim states: Cancel commits between `Start`'s Stripe call and its `OpenPayment` write.

Step 1 — the cancel's own statements, then `open_payment`, run as role `store`:

```
BEGIN; SET ROLE store;
UPDATE orders SET fulfillment_status='cancelled', cancelled_at=now()
WHERE order_number='GO-260824-000003' AND fulfillment_status='pending'
  AND id NOT IN (SELECT id FROM committed_orders);          -- UPDATE 1
-- what internal/cart/cancel.go:56 OpenSessionsForOrder would hand back:
SELECT count(*) FROM payments p JOIN orders o ON o.id=p.order_id
WHERE o.order_number='GO-260824-000003' AND p.status='requires_payment';
   step                         | count
   open sessions seen by Cancel |     0        <-- nothing to expire at Stripe
SELECT open_payment(<order id>, 'cs_test_race_probe', 100000) IS NOT NULL;
   row_written | t                                          <-- accepted
SELECT p.provider_ref, p.status, o.fulfillment_status ...
   cs_test_race_probe | requires_payment | cancelled
ROLLBACK;
```

Step 2 — the same order, then the money arriving:

```
SELECT capture_payment('cs_test_race_probe', order_amount_owed(...), NULL, NULL);
ERROR:  order GO-260824-000003 was cancelled and cannot be paid
CONTEXT: PL/pgSQL function payments_require_complete_order() line 43 at RAISE
```

The code path, quoted:

- `internal/payment/handler.go:116` `sessionID, redirectURL, err := h.gateway.StartSession(...)` — the session exists and is payable at Stripe from here.
- `internal/payment/handler.go:124` `if err := h.store.OpenPayment(r.Context(), number, sessionID, o.TotalCents); err != nil {` — the local row is written only afterwards, on the pool, in no transaction with anything.
- `internal/payment/handler.go:130` `h.toCheckout(w, r, number, redirectURL)` — goen then 303s the customer onto that session.
- `internal/cart/cancel.go:30` cancels, `:38` releases the holds, `:56` `q.OpenSessionsForOrder(ctx, number)` inside the tx, `:62` commits; `internal/cart/handler.go:597` `h.closeSessions(...)` expires post-commit. `internal/admin/store.go:284-289` is the same shape for the back office.
- `migrations/001_initial_schema.up.sql:3833-3853` `open_payment` reads nothing but `payments`: no `orders` lookup, no lock, no status test.
- `migrations/001_initial_schema.up.sql:2604-2606` is why the existing trigger cannot catch it: `IF NEW.status <> 'succeeded' THEN RETURN NEW; END IF;` — so the INSERT of a `'requires_payment'` row never reaches the `payments_refuse_cancelled_order` branch at `:2637-2640`.

What is already closed, and is NOT this: the opposite ordering. `internal/payment/integration_test.go:455` `TestACaptureIsRefusedForACancelledOrder` opens the payment first and cancels second — `OpenSessionsForOrder` sees the row and `closeSessions` expires it. CLAUDE.md's "the open sessions are read INSIDE the cancelling transaction" and `closeSessions`' own comment ("money that beats this there arrives as payment.ErrOrderCancelled") both describe only that direction. Nothing in the tree exercises or guards cancel-then-open.


## Blast radius

A customer who is charged real money for an order that is already cancelled and whose stock is already back on the shelf.

The window is from `payableOrder`'s status read to `OpenPayment`'s commit, and it spans a full Stripe API round trip (`StartSession`) — order of 200 ms to seconds, not microseconds. Two doors into it: the customer's own two-tab sequence (press Pay, get impatient on the slow Stripe call, switch tabs and press Cancel), and — more plausibly concurrent — the back office cancelling at `/admin/orders/{number}/status` while the customer is starting a payment.

Consequence, precisely delimited. The order is NEVER marked paid: `payments_refuse_cancelled_order` refuses the capture (reproduced above), `Capture` maps it to `ErrOrderCancelled` (`internal/payment/store.go:249-251`) and the webhook writes `payment_webhook_events.unreconciled`, which `/admin/health` names. So the exposure is money-at-Stripe plus a manual refund, not a corrupted order state.

What makes it worse than the risk this repository has already accepted. CLAUDE.md accepts "money that beats [the ExpireSession call] there" — a millisecond window after a session goen KNOWS about has been asked to expire. Here the session is never known and never expired: it stays payable until its own `ExpiresAt`, which `StartSession` sets from the stock hold, i.e. up to `cart.HoldTTL` (60 minutes). And goen does not merely leave it open — it 303s the customer straight onto it at `handler.go:130`, after the cancellation has committed. The 60-minute hold has also been released by `Cancel`, so the units can be sold to somebody else while that session is still payable.

Silent to nobody permanently — the customer lands on `success_url` = `/orders/{number}?paid=1` showing a cancelled order, and the shop sees the unreconciled row — but the money has moved and only a human at the Stripe console can move it back.


## Fix

Two edits. The schema one is the lock; the Go one turns the refusal into a correct response and closes the orphaned session at Stripe.

**1. `migrations/001_initial_schema.up.sql`, function `open_payment` (line 3833-3853).** `001` is still amended in place per CLAUDE.md ("nothing is deployed"; `make schema-drift` is the safety net), so amend, do not add `002`.

Insert, immediately after `BEGIN` and BEFORE the existing idempotency `SELECT`, a locking read of the order and a refusal:

```
DECLARE
    payment_id uuid;
    order_status text;
BEGIN
    -- The session is already payable at Stripe by the time this runs, and the
    -- cancelling transaction reads its expire-list from payments — a row it
    -- cannot see does not get closed. FOR UPDATE makes the two mutually
    -- exclusive: either this refuses, or Cancel's UPDATE waits and then finds
    -- this row in OpenSessionsForOrder.
    SELECT fulfillment_status INTO order_status
    FROM orders WHERE id = p_order_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'no order %', p_order_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_open_needs_order';
    END IF;
    IF order_status <> 'pending' THEN
        RAISE EXCEPTION 'order % is % and cannot open a checkout', p_order_id, order_status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_open_refuses_settled_order';
    END IF;
```

Use `<> 'pending'` and not `= 'cancelled'`: `Start` is only ever reached for a pending order (`payableOrder` at `handler.go:~200` refuses anything else), so the tighter predicate costs nothing and also refuses a session opened against an order the back office advanced. Keep the existing early-return on `(order_id, provider_ref)` AFTER this block, so a genuine retry of the same session id on a still-pending order stays idempotent.

Why `FOR UPDATE` is the load-bearing half, not just the status test: `Cancel` takes the same row lock at `CancelOrderByCustomer` (`internal/cart/query.sql:298`) and reads `OpenSessionsForOrder` AFTER it, in the same transaction. With the lock, exactly one of the two orderings happens — `open_payment` blocks and then refuses, or it commits first and `Cancel`'s post-UPDATE statement snapshot sees the row and expires it. Without it, the status test alone still loses to a cancel that commits a microsecond later.

Do NOT put this in `payments_require_complete_order`: that trigger's `NEW.status <> 'succeeded'` early return is correct for the capture rules it holds, and widening it would drag `payments_require_complete_order` and `payments_capture_matches_order` onto every session open.

**2. `internal/payment/store.go`, `OpenPayment` (lines 118-134).** Map the refusal to a sentinel by `PgError.ConstraintName` — never `strings.Contains` on the message (CLAUDE.md mistake #32; this is a trigger-style `RAISE`, so the name is in the struct field and nowhere in the text). Reuse the existing `ErrOrderCancelled` (`internal/payment/payment.go:23-25`) or add `ErrNotOpenable`; `ErrOrderCancelled`'s doc comment says "money arriving", so prefer a new `ErrNotOpenable = errors.New("payment: the order is no longer awaiting payment")`:

```go
if _, err := s.q.OpenPayment(ctx, db.OpenPaymentParams{...}); err != nil {
    if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
        pgErr.ConstraintName == "payments_open_refuses_settled_order" {
        return fmt.Errorf("%w: order %s", ErrNotOpenable, number)
    }
    return fmt.Errorf("open payment for order %s: %w", number, err)
}
```

**3. `internal/payment/handler.go`, `Start` (line 124).** On `ErrNotOpenable`, do not `serverError` (a 500 tells the customer to try again, which is wrong and misleading). Instead: expire the session that was just created at Stripe, then answer the same 409 refusal `payableOrder` gives:

```go
if err := h.store.OpenPayment(r.Context(), number, sessionID, o.TotalCents); err != nil {
    if errors.Is(err, ErrNotOpenable) {
        // The order was cancelled while Stripe was being asked. Nothing recorded
        // this session, so nothing else will ever close it.
        if expErr := h.gateway.ExpireSession(r.Context(), sessionID); expErr != nil {
            h.log.WarnContext(r.Context(), "expire the session of an order cancelled mid-open",
                "order", number, "session", sessionID, "error", expErr)
        }
        h.notice(w, r, http.StatusConflict,
            i18n.T(r.Context(), i18n.KeyPayRefusedTitle),
            i18n.T(r.Context(), i18n.KeyPayRefusedTitle),
            i18n.T(r.Context(), i18n.KeyPayRefusedBody))
        return
    }
    ... existing 500 path
}
```

Reuse the existing `KeyPayRefused*` keys — no new i18n keys, so `TestEveryKeyIsRendered` and `TestEveryKeyIsTranslatedInEveryLocale` are unaffected.

Must NOT do: do not "fix" this by having `Cancel` re-read sessions after commit (a second read has the same window one step later), and do not move the Stripe call after `OpenPayment` (that reintroduces mistake #15's shape — a recorded session id that Stripe never issued).

After the migration, run `make sqlc` (signature unchanged, so `internal/db` should not move — verify with `make sqlc-check`) and `make verify`, then `make test-integration`.


## The lock, and how to see it fail first

Two locks, both in `internal/payment/integration_test.go` (build tag `integration`, package `payment_test`), sitting beside the existing `TestACaptureIsRefusedForACancelledOrder` at line 455 which holds only the opposite ordering.

**Lock A — `TestOpeningAPaymentIsRefusedOnACancelledOrder`.** The behavioural half.

```
number, id := order(t, 88800)
// the cancel wins the race: it commits while Stripe is still being asked
pool.Exec(ctx, `UPDATE orders SET fulfillment_status='cancelled', cancelled_at=now() WHERE id=$1`, id)
err := payment.NewStore(pool).OpenPayment(ctx, number, "cs_race_"+number, 88800)
```
Assert `errors.Is(err, payment.ErrNotOpenable)`, and assert the row was NOT written:
`SELECT count(*) FROM payments WHERE provider_ref = 'cs_race_'+number` must be 0.
Also drive the raw function so the schema's own name is what is measured, the way line 468-478 already does:
`pool.Exec(ctx, "SELECT open_payment($1,$2,$3::bigint)", id, "cs_race2_"+number, int64(88800))` must fail with `errors.AsType[*pgconn.PgError]` and `pgErr.ConstraintName == "payments_open_refuses_settled_order"` — the name in a Go STRING LITERAL, which is what `TestEveryRaisedRuleIsAssertedByName` (derived from `pg_proc`) requires of any new `RAISE`.

**Lock B — `TestACancelledOrderLeavesNoSessionUnclosed`.** The half that actually states the invariant, and the one that fails for the right reason. Prove the two paths are mutually exclusive under a real overlap, holding T1's transaction OPEN across T2 — CLAUDE.md mistake #9 says two goroutines and a start channel finish microseconds apart and never overlap, so this must be structured as:

- T1: `tx1 := pool.Begin(ctx)`; `tx1.Exec("SELECT open_payment($1,$2,$3)", id, session, owed)` — this takes the order's `FOR UPDATE` lock and HOLDS it.
- T2 (goroutine): `cartStore.Cancel(ctx, number)`, which blocks on `CancelOrderByCustomer`'s UPDATE.
- T1: `tx1.Commit(ctx)`.
- Join T2 and assert `sessions` (its return value) CONTAINS `session` — the cancelling transaction saw the row it must expire.
- Then the mirror: T1 opens a tx, runs `UPDATE orders SET fulfillment_status='cancelled'` and holds; T2 calls `store.OpenPayment`, which blocks; T1 commits; assert T2 returns `ErrNotOpenable` and wrote no row.

The invariant asserted in one sentence: after `Cancel` returns, no `payments` row for that order is `requires_payment` unless it is in the returned session list.

**Proving them by mutation — each must be SEEN red before the fix counts as a lock (CLAUDE.md mistake #6, and #6's own recorded false-green mode #3: the edit must be seen to APPLY, so re-read the function body out of `pg_proc` after re-running the migration, do not trust a grep count).**

1. Delete the `IF order_status <> 'pending' THEN RAISE ... END IF;` block from `open_payment`, leave the `FOR UPDATE` in place, `make db-reset`, re-run. Lock A must go RED on both assertions; Lock B's second half must go RED. If Lock A stays green, the fixture never cancelled — check the order actually reached `'cancelled'` before `OpenPayment` was called.
2. Separately, delete only `FOR UPDATE` (keep the status test), `make db-reset`, re-run Lock B. Its first half must go RED (or become non-deterministic): without the lock T2's `OpenSessionsForOrder` can still miss the row. A `FOR UPDATE` removal that leaves everything green means the test is not actually overlapping the two transactions — that is the false-green to hunt, and the fix is to confirm T2 really blocked (assert T2 had not returned before T1's commit, e.g. by a `sync.WaitGroup` plus a flag set immediately before `tx1.Commit`).
3. Revert both mutations, `make db-reset`, confirm GREEN, and confirm `TestACaptureIsRefusedForACancelledOrder` (line 455) is STILL green — it opens the payment while the order is pending, so the new refusal must not touch it. If it goes red, the predicate is wrong (probably `<> 'pending'` applied before the idempotency short-circuit for a legitimate retry).

Record all three mutation results in the PR body; per `.claude/rules/review-process.md` a fix is new work and gets `/self-review` run on it again.
