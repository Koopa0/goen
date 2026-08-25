# loyalty-two-defects

**Verdict** CONFIRMED · **Severity** critical · **Origin** found in this round's own sweep (already established)

**Files** migrations/001_initial_schema.up.sql, migrations/001_initial_schema.down.sql, internal/payment/query.sql, internal/payment/store.go, internal/payment/integration_test.go, internal/loyalty/query.sql, internal/loyalty/store.go, internal/loyalty/integration_test.go, internal/loyalty/loyalty.go, internal/db/rules_integration_test.go, internal/db/, CLAUDE.md, README.md

> **⚠ SUPERSEDED IN ONE RESPECT — read `../11-round10-findings.md` §"The clawback decision" first.**
>
> This spec proposes that a refund clawback may overdraw the points balance, and
> adds an exemption to `loyalty_never_negative` to allow it. That is **overruled**
> on goen's own numbers (0.1% earn rate; the farm the clamp leaves open yields
> less than the §19 return postage the shop already pays). A clawback reverses
> the **unconsumed remainder** of the lot and never more; the balance does not go
> negative; `loyalty_never_negative` acquires no exemption. Everything else in
> this spec stands.


## Root cause

One root cause with two faces: the ledger records what happened to POINTS but not to which AWARD, so no read can pair a spend with the lot it consumed.

The pairing is what expiry needs. loyalty_entries_expiry_matches_sign makes it structurally impossible — an award carries a date, a spend carries NULL — and loyalty_balances then patches over the missing pairing with the escape clause `e.points < 0 OR e.expires_on >= current_date`: "count every spend forever, count an award only while it lives." Those two halves are each defensible and together they are an arithmetic that drifts negative with the passage of time, which is #13's shape (two correct halves that disagree) with the disagreement resolved by the calendar rather than by a writer. loyalty_never_negative cannot see it because it is AFTER INSERT: it evaluates the balance when a row is written and never when a date passes, so the state it forbids is reachable by doing nothing at all.

The second face is that award_loyalty_points treats "this order earned nothing" as a caller error. The function already knows the difference between an error and a legitimate no-op — "A guest order has no account to credit, and that is not an error. → RETURN 0", migrations/001:4482-4485 — and an order below the earning threshold is the same kind of fact. Raising instead of returning turns an ordinary commercial outcome into an aborted transaction, and because the call sits outside postCapture's savepoint it takes the webhook claim down with it, defeating ProcessWebhook's own rule (store.go:24-31: a refused posting function aborts the transaction, so an effect that is allowed to fail must run inside a SAVEPOINT).

Underneath both, the award arithmetic exists twice — numeric-with-rounding in internal/payment/query.sql and truncating in loyalty.PointsFor — and nothing holds the copies together.


## Reproduction — the evidence this rests on

EXECUTED, not argued. All against the live dev database in transactions that were ROLLBACK'd. Working tree verified clean before and after (`git status --porcelain` empty).

=== Cited lines confirmed ===

internal/payment/query.sql:94-108 (AwardOrderPoints) says exactly what the summary quotes:
```
 94: -- name: AwardOrderPoints :one
 95: SELECT award_loyalty_points(
 96:     o.id,
 97:     -- One point per NT$100 times the customer's tier, integer division so a
 98:     -- NT$50 order earns nothing. The multiplier is read from the spend the
 99:     -- customer had BEFORE this order, which this statement is committing.
100:     ((coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
101:                 WHERE ol.order_id = o.id), 0)
102:       - o.discount_cents + o.shipping_cents + o.tax_cents) / 10000
103:      * coalesce((SELECT t.points_multiplier_bp FROM membership_tiers t
104:                  WHERE t.id = member_tier(o.user_id, @window_days::integer, o.id)), 10000)
105:      / 10000)::bigint,
106:     (current_date + @validity_days::integer)
107: )
108: FROM orders o WHERE o.id = @order_id;
```

migrations/001_initial_schema.up.sql:4467 inside award_loyalty_points:
```
    IF p_points <= 0 THEN
        RAISE EXCEPTION 'an award must be positive, got %', p_points
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_points_nonzero';
    END IF;
```

internal/payment/store.go:263-268 calls it unconditionally, with no zero guard, INSIDE ProcessWebhook's transaction and OUTSIDE postCapture's savepoint:
```
263: 	if _, err := s.q.AwardOrderPoints(ctx, db.AwardOrderPointsParams{
264: 		OrderID: row.ID, ValidityDays: LoyaltyValidityDays,
265: 		WindowDays: MembershipWindowDays,
266: 	}); err != nil {
267: 		return "", fmt.Errorf("award points for order %s: %w", row.OrderNumber, err)
268: 	}
```
The savepoint at store.go:204-216 wraps `post(...)` (capture_payment) only. AwardOrderPoints runs on `s.q` directly, so its RAISE aborts ProcessWebhook's whole transaction with 25P02 — the exact failure store.go:24-31 documents for the cancelled-order case.

migrations/001:4174-4180 loyalty_balances is as claimed:
```
    coalesce(sum(e.points) FILTER (
        WHERE e.points < 0 OR e.expires_on >= current_date), 0)::bigint AS points
```
migrations/001:4157-4158 `loyalty_entries_expiry_matches_sign CHECK ((points > 0) = (expires_on IS NOT NULL))` — so a spend is always expires_on NULL and counts forever.
migrations/001:4206-4208 `CREATE CONSTRAINT TRIGGER loyalty_never_negative AFTER INSERT ON loyalty_entries` — INSERT only, so nothing re-evaluates the balance when an award lapses by the passage of time.

=== Defect A reproduced ===

1. The raise itself:
```
BEGIN; SELECT award_loyalty_points('000...0'::uuid, 0::bigint, current_date+365);
ERROR:  an award must be positive, got 0
CONTEXT:  PL/pgSQL function award_loyalty_points(uuid,bigint,date) line 6 at RAISE
```

2. The real statement, verbatim from query.sql, against a real order (GO-260824-000003, 金卡 tier, bp=13000), with discount_cents replaced by a coupon figure leaving a net of 3000 cents:
```
BEGIN;
SELECT award_loyalty_points(o.id, ((coalesce((SELECT sum(...)),0) - 3387000 + o.shipping_cents
   + o.tax_cents)/10000 * coalesce(bp,10000)/10000)::bigint, current_date+365)
FROM orders o WHERE o.id = '01a03195-45dc-71bc-b3f9-6aa4a5b5ace2';
ERROR:  an award must be positive, got 0
SELECT 'transaction still usable?';
ERROR:  current transaction is aborted, commands ignored until end of transaction block
ROLLBACK
```
The second ERROR is the whole point: the transaction is dead, so MarkWebhookProcessed can never run, the handler answers 500, Stripe retries, and the retry fails identically. Nothing is recorded — not even a payment_webhook_events row — so /admin/health cannot see it and the stock hold lapses 30 minutes later.

=== ONE THING THE SUMMARY GETS WRONG, and it matters for the fix ===

The comment's claim of "integer division" is FALSE, and so is "a NT$50 order earns nothing". `sum(ol.unit_price_cents * ol.quantity)` returns NUMERIC, so `coalesce(..., 0)` is numeric and the whole expression is NUMERIC division; `::bigint` then ROUNDS half away from zero. Measured:
```
 net_cents |        raw_numeric         | as_bigint
      4999 |     0.49990000000000000000 |         0
      5000 |     0.50000000000000000000 |         1
      9999 |     0.99990000000000000000 |         1
```
So today the zero-award window is net < NT$50, not net < NT$100, and a NT$50 order earns 1 point. Two consequences the spec must carry:
  (a) internal/loyalty/loyalty.go:38 `PointsFor` truncates (`totalCents / 10000`) and its lock internal/loyalty/loyalty_test.go:16 asserts `PointsFor(9999) == 0`. The SQL awards 1 for the same order. **Two definitions of one fact, disagreeing — mistake #13's shape — and no test can see it because no test compares them.**
  (b) Fixing the rounding (making the SQL agree with PointsFor) WIDENS defect A from "under NT$50" to "under NT$100". That is why the two halves of A must land together, and it is a second reason A and B cannot be fixed independently.

The summary's "reachable well above NT$100 via a percentage coupon" is right in kind: discount_cents is subtracted before the division and shipping/tax are added after, so an order of any size with a large enough percentage coupon lands in the window. My reproduction used net = NT$30, which is above Stripe's TWD minimum and therefore a session goen really opens.

=== Defect B reproduced ===

Probe 1 — the two rows that are legal on day 0 and negative on day 1:
```
BEGIN;
INSERT INTO loyalty_entries (...) VALUES (acct, 100,'order','probe-award', current_date);
INSERT INTO loyalty_entries (...) VALUES (acct,-100,'redeem','probe-spend');   -- guard sees 0, permits
SELECT points FROM loyalty_balances WHERE account_id = acct;      -> balance_day0 =    0
SELECT coalesce(sum(points) FILTER (WHERE points < 0 OR expires_on >= current_date+1),0)
  FROM loyalty_entries WHERE account_id = acct;                   -> balance_day1 = -100
ROLLBACK;
```

Probe 2 — the consequence, with the day-0 rows planted as they were written (trigger disabled for the plant only, re-enabled before the act under test):
```
BEGIN;
ALTER TABLE loyalty_entries DISABLE TRIGGER loyalty_never_negative;
INSERT ... (100,'order','probe-a', current_date - 1);   -- awarded then, lapsed since
INSERT ... (-100,'redeem','probe-b');                   -- spent while it was live
ALTER TABLE loyalty_entries ENABLE TRIGGER loyalty_never_negative;
SELECT points FROM loyalty_balances WHERE account_id = acct;   -> balance_today = -100
INSERT ... (50,'order','probe-c', current_date + 365);         -- the NEXT order's award
ERROR:  account 01a031c7-8e41-... would hold -50 points
CONTEXT:  PL/pgSQL function loyalty_never_negative() line 9 at RAISE
ROLLBACK;
```
That RAISE is inside award_loyalty_points' INSERT, which runs at internal/payment/store.go:263 — so defect B delivers defect A's exact failure mode (aborted webhook transaction, 500, permanent Stripe retry loop) by a second route, and closing A's zero guard does not close it. That is the "a fix to either alone may not be sound" linkage, in both directions.

Not a recorded decision. CLAUDE.md's loyalty paragraph asserts the opposite of what is true here — "Expiry is applied ON READ, per entry. A job that writes expiry rows and has not run yet leaves expired points spendable, and a customer spending points the shop believes are gone is the failure this must not have." — and says nothing about a spend outliving the award it consumed. docs/roadmap.md carries no loyalty item.


## Blast radius

Defect A. Every captured order whose award truncates to zero. Today that is net < NT$50 (rounding); after the arithmetic is made to agree with loyalty.PointsFor it is net < NT$100. A percentage coupon puts an order of ANY size in that window, because discount_cents is subtracted before the division. The customer reaches Stripe, pays, and then: the webhook transaction aborts, nothing at all is written (no payments capture — the savepoint commit is rolled back with its parent — no payment_webhook_events row, no order_events row, no receipt), the handler answers 500, Stripe retries on its schedule and every retry fails identically because the input never changes. Money is at Stripe; the order sits at pending with its payment row still `requires_payment`. Thirty minutes later ExpiredReservations releases the stock hold and the goods go back on the shelf. The customer's own order page reads 尚未付款 with a 前往付款 link, so they can pay a second time. Silent in every direction: /admin/health counts undelivered outbox messages, unreleased expired holds and projection age — none of which this produces — and payment_webhook_events.unreconciled, the alarm built for exactly "money arrived and goen could not act on it", is never written because the transaction that would write it is the one that died.

Defect B. Every customer who spends points and then lets an award lapse — which is every customer who redeems, since redemption is capped at the balance and the balance is what expires. loyalty.Validity is 365 days, so the first lapses arrive one year after the first awards; from then on the account's balance is understated by the amount of the lapsed-but-already-spent award, and once it is negative the NEXT captured order for that customer hits loyalty_never_negative inside award_loyalty_points and lands in defect A's failure mode: money at Stripe, order never paid, permanent 500 loop. It is self-perpetuating — the customer can never be awarded again, so the balance can never recover. The customer-facing damage arrives earlier and quieter: /account/points, the checkout's credit chooser and /admin/customers all read loyalty_balances, so a customer who spent 100 points that later lapsed is shown a balance 100 lower than the ledger justifies, and loyalty.Redeem refuses redemptions against points they still hold. A shop looking at /admin/customers sees the same wrong figure, so the customer and the person answering the phone agree on a number that is wrong.

Both defects are on the buying mainline and both fail closed on money that has already left the customer.


## Fix

ONE change set. Amend migrations/001_initial_schema.up.sql in place (CLAUDE.md: "`001` is still amended in place rather than superseded: goen has not been deployed... Amending it costs one `DROP DATABASE goen` + `make migrate-up` locally"), rebuild with `make db-reset`, then `make schema-drift` per the roadmap note that is what makes amending safe. Do NOT hand-edit internal/db; run `make sqlc` after the .sql changes.

────────────────────────────────────────────────────────
PART 1 — the model for B: LOT-LINKED SPENDS
────────────────────────────────────────────────────────

CHOSEN: a spend names the award it consumes, is split into one row per lot, and carries that lot's expires_on. FIFO by soonest expiry, allocated inside redeem_loyalty_points under the account lock it already takes.

Rejected alternatives and why:
• An expiration ledger (a job posts a negative entry when an award lapses) is refused by the view's own comment, migrations/001:4172-4173 and CLAUDE.md: "Expiry is applied ON READ, per entry. A job that writes expiry rows and has not run yet leaves expired points spendable, and a customer spending points the shop believes are gone is the failure this must not have." A job also puts the balance in two places — the ledger and whether the job ran — against "one definition per fact".
• Read-time FIFO computed in the view (window function over the ledger) keeps the ledger untouched but has to answer "was this lot still alive when that spend happened?" per spend in date order, which is a recursive CTE or a plpgsql loop in the hot path read by /account/points, the checkout, /admin/customers and the guard. Worse: it floors each lot's remainder at zero, so the balance becomes non-negative BY CONSTRUCTION and an overdraw is silently absorbed instead of refused — it would delete the rule while appearing to keep it.
• A consumed_points column on the award row is refused by loyalty_entries_append_only, and adding an allocation table has the same "non-negative by construction" problem plus a new table, a new writer and new grants.

Why lot-linked spends is the repository's own shape: it is consume_reservation_partial, exactly. CLAUDE.md: "`consume_reservation_partial` settles part of a reservation by reducing the held row and inserting a `consumed` one, rather than adding a `consumed_quantity` column — one row then records one settled fact... The consumed row carries the ORIGINAL's `created_at` and `expires_at`". A spend across two lots is two settled facts, and each carries its lot's expiry for the same reason. It keeps the ledger append-only, keeps expiry on read with no job, keeps loyalty_balances the one definition of a balance, and makes the balance non-negative at EVERY future date by a per-lot invariant the database enforces — because for each distinct expires_on the sum of entries is >= 0, so no subset of dates can sum negative.

1.1 Table (migrations/001:4137-4159). In CREATE TABLE loyalty_entries:
  • `expires_on date NOT NULL` — every entry expires now, award and spend alike. This is what makes a lapsing award take its own spends out of the balance with it.
  • add:
```sql
    -- The award this spend consumed. NULL on an award. A spend is split across
    -- as many rows as lots it takes and each carries its lot's expires_on, so
    -- an award and the spends against it leave the balance together. Without
    -- it a spend counted forever while the award it paid for lapsed, and the
    -- balance drifted negative by doing nothing at all.
    lot_id uuid REFERENCES loyalty_entries (id) ON DELETE RESTRICT,
```
  • DELETE constraint loyalty_entries_expiry_matches_sign.
  • ADD `CONSTRAINT loyalty_entries_lot_matches_sign CHECK ((points < 0) = (lot_id IS NOT NULL))`.
  • ADD `CREATE INDEX loyalty_entries_lot_idx ON loyalty_entries (lot_id) WHERE lot_id IS NOT NULL;` — the lot guard sums the spends against one lot.
  Leave loyalty_entries_points_nonzero, _reason_present, _key_present, the idempotency unique index, loyalty_entries_account_idx and loyalty_entries_append_only untouched.

1.2 View (migrations/001:4174-4184). Delete the escape clause:
```sql
CREATE VIEW loyalty_balances AS
    SELECT a.id AS account_id,
           coalesce(sum(e.points) FILTER (WHERE e.expires_on >= current_date), 0)::bigint AS points
    FROM store_credit_accounts a
    LEFT JOIN loyalty_entries e ON e.account_id = a.id
    GROUP BY a.id;
```
The comment must say what the deleted `e.points < 0 OR` cost: it counted a spend forever while the award it consumed lapsed, so an account that spent an award to zero read as negative the day after that award expired, and the next capture's award then met the never-negative guard inside the webhook's transaction.

1.3 Replace loyalty_never_negative with a lot guard (migrations/001:4186-4208). DELETE FUNCTION loyalty_never_negative() and its CONSTRAINT TRIGGER. It is not being weakened, it is becoming unreachable: with 1.1's CHECK every spend names a lot, and the lot guard below refuses any spend a lot cannot pay for, so no insert — legal or forged — can drive the account negative. Keeping a rule no statement can reach is mistake #7, a guard satisfied by nothing. Trigger count is unchanged (one out, one in).

Define, ABOVE the final DO block (mistake #19 — anything appended below it keeps PUBLIC EXECUTE):
```sql
-- A spend may not take more than the lot it names still holds, and may not take
-- from a lot that has lapsed. This is what makes loyalty_balances non-negative
-- at every future date rather than only on the day a row is written: for each
-- expires_on the entries sum to >= 0, so no set of dates can sum negative.
CREATE FUNCTION loyalty_lot_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    lot loyalty_entries%ROWTYPE;
    taken bigint;
BEGIN
    IF NEW.lot_id IS NULL THEN
        RETURN NEW;                       -- an award names no lot
    END IF;

    -- The account is the aggregate root, exactly as the guard this replaces
    -- locked it, and redeem_loyalty_points takes it FIRST so the AFTER trigger
    -- is never a lock upgrade (the deadlock that looked like the guard working).
    PERFORM 1 FROM store_credit_accounts WHERE id = NEW.account_id FOR UPDATE;

    SELECT * INTO lot FROM loyalty_entries WHERE id = NEW.lot_id;

    IF lot.points <= 0 OR lot.account_id <> NEW.account_id THEN
        RAISE EXCEPTION 'spend % names % which is not an award on this account', NEW.id, NEW.lot_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_lot_is_an_award';
    END IF;

    IF NEW.expires_on <> lot.expires_on THEN
        RAISE EXCEPTION 'spend % expires % against a lot expiring %',
            NEW.id, NEW.expires_on, lot.expires_on
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_lot_expiry_matches';
    END IF;

    IF lot.expires_on < current_date THEN
        RAISE EXCEPTION 'lot % lapsed on %', lot.id, lot.expires_on
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_lot_not_expired';
    END IF;

    SELECT coalesce(sum(-points), 0) INTO taken
    FROM loyalty_entries WHERE lot_id = NEW.lot_id;

    IF taken > lot.points THEN
        RAISE EXCEPTION 'lot % holds % and % has been taken from it', lot.id, lot.points, taken
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_lot_not_overdrawn';
    END IF;
    RETURN NEW;
END;
$$;

-- AFTER, so the sum it reads includes the row being checked.
CREATE CONSTRAINT TRIGGER loyalty_lot_guard
    AFTER INSERT ON loyalty_entries
    FOR EACH ROW EXECUTE FUNCTION loyalty_lot_guard();
```
loyalty_entries_lot_expiry_matches is not decoration: it is what makes the per-date invariant true, and without it a spend could be dated to a lot it did not consume.

1.4 redeem_loyalty_points (migrations/001:4503-4534). Same signature, same grants, same lock order, same paired store_credit_entries insert. Between the lock and the credit, allocate:
```sql
    -- FIFO by soonest expiry: spend what is about to lapse first, which is the
    -- answer that costs the customer least. The remainder of each lot is its
    -- award less every spend already named against it.
    v_left := p_points;
    FOR lot IN
        SELECT e.id, e.expires_on,
               e.points - coalesce((SELECT sum(-s.points) FROM loyalty_entries s
                                    WHERE s.lot_id = e.id), 0) AS remaining
        FROM loyalty_entries e
        WHERE e.account_id = p_account_id
          AND e.points > 0
          AND e.expires_on >= current_date
        ORDER BY e.expires_on, e.created_at, e.id
    LOOP
        EXIT WHEN v_left = 0;
        CONTINUE WHEN lot.remaining <= 0;
        v_take := least(v_left, lot.remaining);
        v_n := v_n + 1;
        INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key,
                                     expires_on, lot_id)
        VALUES (p_account_id, -v_take, 'redeem', p_key || '#' || v_n,
                lot.expires_on, lot.id);
        v_left := v_left - v_take;
    END LOOP;

    IF v_left > 0 THEN
        RAISE EXCEPTION 'account % is short % of % points', p_account_id, v_left, p_points
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_within_balance';
    END IF;
```
The `#n` suffix keeps loyalty_entries_idempotency_key unique across the rows of one redemption and gives the history a group key; '#' appears in neither 'earn:'||uuid nor 'points:'||uuid. The shortfall is raised HERE, under the lock, because that is where the allocation discovers it — the balance is still never read in Go for the decision (store.go:52-55's reason stands). The credit insert and its 'redeem:'||p_key key are unchanged.

1.5 Callers of the shape change.
• internal/loyalty/query.sql PointsExpiringSoon: delete `AND e.points > 0` from the sum's predicate, so the figure is the NET of lots about to lapse rather than the gross award — with lot-linked spends the spends now sit inside the window and must be subtracted, or the page warns about points the customer already spent. Keep `soonest` as `min(e.expires_on)` over rows with `points > 0`, and keep the two-column shape (its comment on min() over no rows still applies).
• internal/loyalty/query.sql PointsHistory: group the rows of one redemption so a 150-point redemption is one line and not three. `GROUP BY split_part(e.idempotency_key, '#', 1), e.reason, e.created_at, e.expires_on, o.order_number`, selecting `sum(e.points)`; keep `expired` as `(sum(e.points) > 0 AND e.expires_on < current_date)`. Order by `max(e.created_at) DESC`.
• internal/loyalty/store.go Redeem (line ~64): the error mapping is currently `fmt.Errorf("%w: %w", ErrNotEnough, err)` for anything. Bind it: on a *pgconn.PgError whose ConstraintName is "loyalty_entries_within_balance", return ErrNotEnough; anything else is a real error and must not be reported to the customer as "not enough points". Use errors.AsType and ConstraintName, never strings.Contains (mistake #32, and .claude/rules/error-handling.md).
• Nothing else writes the ledger: `grep -rn loyalty_entries --include=*.sql .` finds only migrations/001 and internal/loyalty/query.sql, and no seed touches it. loyalty_balances readers (internal/loyalty/query.sql PointsBalance, internal/admin/query.sql:1140) need no change — the view keeps its shape.

1.6 Existing rows / migration. None. Nothing is deployed, `make db-reset` rebuilds from 001, and the dev seed writes no loyalty rows. Record in the amended file's comment what a deployed instance WOULD have needed, because the escape clause in CLAUDE.md turns on exactly this: a 002 that adds lot_id nullable, allocates every historical spend FIFO over that account's awards (splitting spends across lots), then sets expires_on and NOT NULL — and that reconciles by hand any account whose historical spends exceed its awards, since those are rows the new invariant refuses and no automatic rule can decide which lot they should have taken. Also update the 001 down migration if it names the dropped objects.

────────────────────────────────────────────────────────
PART 2 — Defect A
────────────────────────────────────────────────────────

2.1 award_loyalty_points (migrations/001:4467-4470). Zero becomes a legal no-op; negative stays an error:
```sql
    -- An order that earned nothing is not an error, exactly as a guest order is
    -- not one: this runs inside the capture's transaction and a RAISE aborts it,
    -- so the webhook could never be marked processed and Stripe retried a
    -- capture that failed identically every time. Money at Stripe, order unpaid.
    IF p_points = 0 THEN
        RETURN 0;
    END IF;
    IF p_points < 0 THEN
        RAISE EXCEPTION 'an award must be positive, got %', p_points
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_points_nonzero';
    END IF;
```
Keeping the negative branch keeps loyalty_entries_points_nonzero raised, so TestEveryRaisedRuleIsAssertedByName stays satisfied by the existing assertion.

2.2 internal/payment/query.sql:97-105. Make the arithmetic truncate, so it says what its comment says and agrees with loyalty.PointsFor: cast the subtotal sum to bigint so every division is integer division.
```sql
    ((coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)::bigint FROM order_lines ol
                WHERE ol.order_id = o.id), 0)
      - o.discount_cents + o.shipping_cents + o.tax_cents) / 10000
     * coalesce((SELECT t.points_multiplier_bp FROM membership_tiers t
                 WHERE t.id = member_tier(o.user_id, @window_days::integer, o.id)), 10000)
     / 10000)::bigint,
```
Rewrite the comment to state the measured fact rather than the intention: sum() is numeric, so `::bigint` on the whole expression ROUNDED — an order netting NT$50 earned a point the Go definition says it does not — and the cast on the sum is what makes the division integer. Name loyalty.PointsFor as the other half and say the two are one fact.
Verify the tier arithmetic is unmoved: TestPointsAreMultipliedByTheCustomersTier expects 120 then 240 at bp=20000 on NT$12,000; 1200000/10000 = 120, 120*20000/10000 = 240. Unchanged.

2.3 internal/payment/store.go:263-268 needs NO zero guard and must not grow one. The database is the door; a second check in Go is the second definition this whole spec is removing. Leave the call and its error wrap exactly as they are.

2.4 Do NOT wrap AwardOrderPoints in a savepoint. The savepoint at store.go:204-216 exists for an outcome goen chooses to RECORD and move past (money for a cancelled order). A failed award is not that: after 2.1 and Part 1 the only remaining way it fails is a real fault, and swallowing it would mark the webhook processed with the points never awarded and nothing to find it by.

────────────────────────────────────────────────────────
PART 3 — the documents these numbers live in
────────────────────────────────────────────────────────
Run TestTheStatedSchemaTotalsAreTheRealOnes (internal/db/statedtotals_integration_test.go) and update the sentence it reads in BOTH CLAUDE.md ("245 CHECKs, 80 foreign keys, 62 unique indexes and 39 rule triggers") and README.md:114-115 to whatever the catalogue now says. The CHECK count moves (one dropped, one added, plus the NOT NULL), the FK count moves by one (lot_id), the index count is unchanged (the new lot index is not unique), the trigger count is unchanged.
Rewrite CLAUDE.md's loyalty paragraph: the "never-negative guard deadlocked instead of refusing" story stays (the lock-first ordering is still why redeem_loyalty_points takes the account FOR UPDATE before inserting), but the guard it names is gone, and the paragraph must now state the lot model and why expiry-on-read needed it. Add a numbered entry to "Predictable mistakes here" for the shape neither of these defects had a name for: **a read-time rule whose two halves are applied to different rows — the balance counted a spend forever and an award only while it lived, so the arithmetic went wrong by the passage of time, and every guard here fires on a WRITE.**


## The lock, and how to see it fail first

Every lock below must be seen RED before the fix, with the mutation seen to apply (CLAUDE.md false-green mode #3: "The mutation must be seen to apply, not assumed" — re-read the edited file, do not trust a grep count).

═══ DEFECT A ═══

A1. `TestAnOrderThatEarnsNothingIsStillCaptured` — internal/payment/integration_test.go, beside TestPointsAreMultipliedByTheCustomersTier.
FIXTURE DETAIL THAT MAKES IT REAL: the order's award must truncate to ZERO. Reuse payForOwnedOrder's shape (payment/integration_test.go:783) with a base-rate customer (a fresh user, no tier) and one line at 4900 cents (NT$49) with shipping_cents 0 — after 2.2 anything under 10000 works, before 2.2 it must be under 5000 or the rounding cast lifts it to 1 and the test passes against the defect. Assert, in this order: Capture returns a nil error; OrderIsPaid is true; the order_events 'paid' row exists; the receipt is enqueued (the existing outbox assertion from TestACaptureEnqueuesTheReceipt); and `SELECT count(*) FROM loyalty_entries WHERE order_id = $1` is 0 — nothing earned, and nothing conjured.
MUTATION: restore `IF p_points <= 0 THEN RAISE ...` in award_loyalty_points and rebuild. RED: Capture returns "award points for order …: ERROR: an award must be positive, got 0". Also assert with `errors.AsType[*pgconn.PgError]` that the failure carried ConstraintName "loyalty_entries_points_nonzero" while mutated, so the test is bound to which rule refused (#8) rather than to any error.
WHY NO EXISTING TEST SEES THIS: TestAnOrderIsAwardedOnce (loyalty/integration_test.go:202) and payForOwnedOrder both pass a hand-written positive award or a NT$2,500+ order. Every payment fixture earns points. This is #17's shape — every fixture ships the same shape of the thing.

A2. `TestTheAwardedPointsAreWhatPointsForSays` — internal/payment/integration_test.go. This is the one-definition lock, and no test in the tree compares the two halves.
Table-driven over the cases loyalty_test.go:11-19 already asserts in Go plus the boundary the rounding turns on: 0, 4999, 5000, 9999, 10000, 19999, 100000, 2590000. For each, place and capture an order at that net for a base-rate user through payForOwnedOrder's shape, and assert `sum(points) FROM loyalty_entries WHERE order_id = $1` equals `loyalty.PointsFor(net)`. Depends on A1's fix to run at all for the zero cases — which is the point: the two halves of A are one test away from each other.
MUTATION: drop the `::bigint` cast from the sum in internal/payment/query.sql, re-run `make sqlc`, verify the generated statement in internal/db actually changed. RED on 5000 (SQL awards 1, PointsFor says 0) and 19999→2 vs 1. Both directions of the rounding are covered by the case list, so a fix that only clamps zero does not go green.

═══ DEFECT B ═══

B1. `TestASpentAwardLeavesTheBalanceWithIt` — internal/loyalty/integration_test.go. The lock for B, and the fixture detail is the whole test.
FIXTURE THAT ACTUALLY CROSSES THE BOUNDARY: two lots, and the earliest must expire TODAY, not inside the window.
  lot A: 100 points, `expires_on = current_date`  — live today, gone tomorrow
  lot B: 100 points, `expires_on = current_date + 365`
Seed both through award_loyalty_points against two orders (not by raw INSERT — a fixture that reaches past the application is a fixture for a claim nobody is testing, CLAUDE.md on the dev seed). Then `redeem_loyalty_points(account, 150, CreditFor(150), key)`, which must take all 100 of A and 50 of B.
Assert four things:
 (a) today's `loyalty_balances.points` is 50;
 (b) FIFO: the spend rows against lot A total -100 and against lot B total -50 — bind on lot_id, so a fix that allocates by created_at or picks the longest-lived lot first is caught;
 (c) THE TEMPORAL ASSERTION, which needs no clock: for every distinct expires_on on that account, `sum(points) >= 0`. That is precisely the property that makes the balance non-negative at every future date, and it is decidable today;
 (d) tomorrow's answer: `SELECT coalesce(sum(points),0) FROM loyalty_entries WHERE account_id=$1 AND expires_on >= current_date + 1` is 50 — the reproduction's own probe, stated as a positive expectation.
MUTATION (must be applied as one, since the old model is both halves): put `points < 0 OR` back in loyalty_balances and make redeem_loyalty_points insert a single unlotted row with expires_on NULL (dropping the lot CHECK to allow it). RED twice: (c) reports the NULL-date group at -150 and lot A's group at +100, and (d) gives -50 instead of 50. If ONLY the view is mutated the test still goes red on (c), so the mutation is not load-bearing on both halves at once.
A fixture with both lots at `current_date + 365` sits inside the window: (a)–(d) all pass with the defect fully present. Say so in the test's comment — it is the trap this test exists to have avoided, and #33's lesson ("a fixture that does not reach the state under test passes for a reason that has nothing to do with the fix").

B2. `TestPointsCannotBeSpentFromALapsedLot` — internal/loyalty/integration_test.go. Award a lot at `expires_on = current_date - 1` (raw INSERT is legitimate here: it is planting a lot that lapsed, and award_loyalty_points cannot produce a past date), then attempt a spend against it in both reachable ways: through redeem_loyalty_points (must be refused by ConstraintName "loyalty_entries_within_balance", because the lot is outside the allocator's `expires_on >= current_date` predicate and the shortfall is the whole amount), and by direct INSERT naming that lot as owner (must be refused by ConstraintName "loyalty_entries_lot_not_expired"). Assert on ConstraintName, never on the message.
MUTATION: delete the `expires_on >= current_date` predicate from the allocator loop — RED on the first half; delete the lot_not_expired branch — RED on the second. Two branches, two mutations (a rule a FUNCTION raises is asserted by name, not by its trigger).

B3. `TestALotCannotBeOverdrawn` — internal/db/rules_integration_test.go, as entries in the table at line ~117. Replace the loyalty_never_negative entry (that rule is gone) with one per new raised name: loyalty_entries_lot_is_an_award, loyalty_entries_lot_expiry_matches, loyalty_entries_lot_not_expired, loyalty_entries_lot_not_overdrawn, loyalty_entries_lot_matches_sign. Each needs a reject AND an accept, and the ACCEPT must be a statement the WRONG version of the rule refuses — mistake #28: "An accepting statement that both versions of a rule accept proves nothing about either." For lot_not_overdrawn: reject = a lot of 100 plus two spends of -60 against it; accept = the same lot plus spends of -60 and -40. This satisfies TestEveryRuleTriggerIsExercised (keyed on tgname, and loyalty_lot_guard is the new tgname) and TestEveryRaisedRuleIsAssertedByName (derives its corpus from pg_proc and asks for the name in a Go string literal — the four new names must appear in test source, and "loyalty_never_negative" must disappear from it, or it stays in the corpus of nothing).

B4. `TestTheLoyaltyGuardHoldsOnItsOwn` (internal/loyalty/integration_test.go:301) must be RETARGETED, not deleted. It drives redeem_loyalty_points from 8 goroutines behind one barrier and asserts exactly one wins and every loser was refused by name — that is the lock proving the account FOR UPDATE serialises rather than PostgreSQL's deadlock detector, and it is the only test of that. Change the expected ConstraintName from "loyalty_never_negative" to "loyalty_entries_within_balance", rename the test to what it now proves, and add to its comment that the rule moved from an AFTER-INSERT trigger to the allocator because with lot-linked spends the shortfall is discovered during allocation, under the same lock. Keep the 8-goroutine barrier: two goroutines and a start channel finish microseconds apart and never overlap (#9).
MUTATION: move the allocator's work above the `PERFORM ... FOR UPDATE`. RED with more than one winner, or losers refused by loyalty_entries_lot_not_overdrawn (the trigger catching what the lock should have) rather than by the allocator — either outcome is the test doing its job.

B5. Existing tests that must be re-run and adjusted, each for a stated reason:
• TestExpiredPointsAreNotSpendable (loyalty/integration_test.go:175) seeds raw entries with a spend of expires_on NULL implicitly — its Redeem(200) call now produces lotted rows. It must still pass unchanged in its assertions; if it does not, the allocator is wrong.
• TestARedemptionPostsPointsAndCreditTogether (:146) counts loyalty rows — update it to expect one row per lot and assert the store_credit_entries side is still exactly one.
• TestTheLedgerIsAppendOnly (:242) is unaffected and must stay green.
• TestAGuestOrderEarnsNothingAndDoesNotError (:227) is A1's sibling and documents the precedent 2.1 follows; add a cross-reference comment in each.
• TestPointsAreEarnedOnWholeHundredsOnly (loyalty/loyalty_test.go:7) stays exactly as it is — it is the Go half that A2 now binds the SQL to.

═══ GATES ═══
`make sqlc` then `make verify` twice (mistake #22: run a gate twice before believing it), then `make test-integration` (shuffled — fixtures must create what they need, #23), then `make schema-drift` against the rebuilt dev database, then TestTheStatedSchemaTotalsAreTheRealOnes and update CLAUDE.md and README.md to the figures it reports. Report each with `cmd && echo PASS || echo FAIL`, never from a piped command (#5).
