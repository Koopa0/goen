# refund-does-not-reverse-points

**Verdict** CONFIRMED · **Severity** high · **Origin** external report (verified this round)

**Files** migrations/001_initial_schema.up.sql, internal/admin/store.go, internal/admin/query.sql, internal/payment/query.sql, internal/payment/store.go, internal/loyalty/loyalty.go, internal/admin/integration_test.go, internal/db/statedtotals_integration_test.go, README.md, CLAUDE.md

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

"Committed" is being used to answer a question it was never split to answer.

`committed_orders` (migrations/001:3540) was carved out of `settled_orders` to fix exactly one direction — a CANCELLED order counting as revenue. Its comment says "committed means the shop is doing the work". A returned order genuinely is work the shop did, so the view is right about its own question. What it cannot say, and what `member_spend` and `award_loyalty_points` both need, is a third question: **how much of this order did the customer ultimately keep and pay for.**

There is no order status for a return. `orders_fulfillment_status_known` has no 'returned' member and `orders_legal_transition` offers no move out of delivered/completed, deliberately — a return is modelled as `return_requests` + `refunds` + a compensating `store_credit_entries` row beside an order whose fulfilment history is genuinely unchanged. That is the right model, and it means the fulfilment status can never carry the answer. Anything reading order status or `committed_orders` to mean "money the shop kept" is reading a proxy, and this is the third such proxy defect in the file's own record (after the `EXISTS(succeeded payment)` proxy for committed, and the `status='pending'` proxy for awaiting payment).

The loyalty ledger has the mirror gap and it is structural, not an omission at a call site: `award_loyalty_points` is the ONLY door into `loyalty_entries` (both roles hold no INSERT) and it hard-refuses `p_points <= 0`. Points were designed as a strictly monotonic accrual. There is no mechanism for a clawback to exist, so `payApprovedReturn` had nothing to call — which is why the absence reads as intentional in review and is not.


## Reproduction — the evidence this rests on

EXECUTED against the live dev database, in a transaction I rolled back. No files written; my footprint is zero.

Setup already present in the dev DB: user 01a03177-9161-7a09-9eac-a33ac95e2b77 has two `delivered` orders totalling 7,380,000 cents, and an open `requested` return (01a03195-4839-7732-a818-4eec8c5c3d57) covering the whole of order 01a03195-47b4-7d4d-8056-2b8cda6c1166 (3,690,000 cents). Tiers: 銀卡 ≥1,000,000, 金卡 ≥5,000,000, 白金 ≥15,000,000.

The script replayed the real code paths: `award_loyalty_points` (what `internal/payment/query.sql:94` AwardOrderPoints calls), then `DecideReturn` → `InspectReturnLine` → close-to-`completed` (internal/admin/query.sql:286,323), then `CompensateReturnWithCredit` → `post_store_credit(... 'return-credit:'||return_id ...)` (internal/admin/query.sql:1105, called from internal/admin/store.go:1015).

    BEGIN;
    SELECT award_loyalty_points(:o, 3690, current_date+365);          -- 3690
    -- before:  spend 7380000 | tier 金卡會員 | points 3690
    UPDATE return_requests SET status='approved', resolution='refund', decided_at=now() WHERE id=:r AND status='requested';
    UPDATE return_request_lines rl SET received_quantity=1, restocked_quantity=1 FROM return_requests r
      WHERE r.id=rl.return_request_id AND rl.return_request_id=:r AND r.status='approved' AND rl.received_quantity IS NULL;
    UPDATE return_requests SET status='completed' WHERE id=:r;
    SELECT post_store_credit(:u, return_refundable_amount(:r), '退貨退回購物金', :o, 'return-credit:'||:r, NULL);  -- 3,690,000 back
    ROLLBACK;

Result row after the goods came back AND the full 3,690,000 was refunded:

     order_status | still_committed | spend_after | tier_after | points_after | reversing_entries
     delivered    | t               |     7380000 | 金卡會員   |         3690 |                 0

Every figure is unchanged. `reversing_entries` counts `loyalty_entries WHERE order_id = :o AND points < 0`: zero. Goods returned, money fully returned, points and 金卡 kept.

Why nothing catches it, read from the schema:
- migrations/001_initial_schema.up.sql:3540 `committed_orders` excludes only `fulfillment_status='cancelled'`. `orders_fulfillment_status_known` (line 1491) has no 'returned' state and `orders_legal_transition` (1553) has no transition out of delivered/completed for a return. A fully returned order is therefore permanently COMMITTED — CLAUDE.md's "a cancelled order is settled and not committed" closes the cancel door and leaves the return door open.
- migrations/001:3650 `member_spend` sums the gross order amount over `JOIN committed_orders` and mentions neither `refunds` nor `order_refunds`. Its only subtraction is `p_exclude_order`, which is the order being paid for right now.
- migrations/001:4458 `award_loyalty_points` is the only door into `loyalty_entries` (INSERT is revoked from store and admin, line 4212) and it refuses non-positive points outright: `IF p_points <= 0 THEN RAISE ... CONSTRAINT='loyalty_entries_points_nonzero'`. There is no negative-posting function at all — so no caller could reverse an award even if one wanted to.
- internal/admin/store.go:994 `payApprovedReturn` pays exactly two things: `refundCard` (line 1045) and `CompensateReturnWithCredit` (line 1015). A repo-wide grep for loyalty/points intersected with return|refund|revers|clawback returns nothing outside internal/db.

Not a recorded decision: `docs/roadmap.md` and `docs/reviews/*.md` contain no line on points, loyalty, tier or member_spend versus returns; CLAUDE.md's loyalty section states the opposite intent ("Spend counts COMMITTED orders only", offered as the protection against a cancelled order counting as money the shop took).

I also EXECUTED the proposed correct definition in the same rolled-back transaction, netting each order by `order_refunds` (migrations/001:4244, CLAUDE.md's "one definition" of what has gone back):

     spend_today | spend_netted
         7380000 |      3690000

which is the right answer and drops the customer 金卡會員 → 銀卡會員.


## Blast radius

Every signed-in customer, unbounded and repeatable, silent on both sides.

The cycle costs the customer nothing: 消保法 §19 gives seven days from receipt with return postage on the shop (stated in internal/site/policies.go and paid by goen), and `return_refundable_amount` returns the delivery fee too on a full rescission. So buy → return → full refund is free, and each pass permanently adds the gross order total to `member_spend` for a rolling 365 days.

Two distinct harms:

1. TIER. Demonstrated above: one buy-and-return cycle of NT$36,900 is the difference between 銀卡會員 and 金卡會員 for a year. `membership_tiers.points_multiplier_bp` is 11000 / 13000 / 15000, so a farmed band raises the earn rate on every REAL order for the whole window — and `award_loyalty_points` reads `member_tier` at capture (internal/payment/query.sql:103), so the inflated spend compounds into inflated awards. NT$150,000 of buy-and-return reaches 白金會員 at a permanent +50%.

2. POINTS. 1 point per NT$100 (`loyalty.PointsPerHundred`), 10 points per NT$1 of credit (`PointsPerCredit`), so a cycle yields 0.1% of the order value as spendable store credit — NT$36.90 on the NT$36,900 order above. Small per pass, but the loop is free and unbounded, and `redeem_loyalty_points` turns it into real `store_credit_entries` that `PlaceOrder` spends as money.

Silent by construction: `loyalty_entries` is append-only, so the ledger looks internally consistent; `/admin/health` counts stuck outbox rows and expired holds, not this; `/admin/customers` renders the inflated spend as fact. Nothing in the tree can distinguish a customer who spent NT$73,800 from one who spent NT$36,900 and sent the other half back. The shop finds out when it prices a benefit against a number that is not true.

Note the shape: this is CLAUDE.md's own #13/#30/#31 pattern — two correct halves that disagree. Every guard here asks what is ABSENT, and both `loyalty_entries` and `member_spend` have writers, readers and doors.


## Fix

Two independent halves. Both are needed: netting the spend alone leaves the already-awarded points, and clawing back points alone leaves the tier inflated.

=== HALF 1: member_spend nets what went back ===

migrations/001_initial_schema.up.sql:3650. Replace the body of `member_spend` so each order contributes its gross LESS what has gone back on it, floored at zero:

    CREATE FUNCTION member_spend(p_user_id uuid, p_days integer,
                                 p_exclude_order uuid DEFAULT NULL)
    RETURNS bigint LANGUAGE sql STABLE AS $$
        SELECT coalesce(sum(greatest(
            (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                       FROM order_lines ol WHERE ol.order_id = o.id), 0)
             - o.discount_cents + o.shipping_cents + o.tax_cents)
            - (rf.card_cents + rf.credit_cents), 0)), 0)::bigint
        FROM orders o
        JOIN committed_orders c ON c.id = o.id
        JOIN order_refunds rf ON rf.order_id = o.id
        WHERE o.user_id = p_user_id
          AND o.placed_at >= now() - make_interval(days => p_days)
          AND (p_exclude_order IS NULL OR o.id <> p_exclude_order);
    $$;

MUST read `order_refunds` (migrations/001:4244) and MUST NOT re-derive the sums — CLAUDE.md names that view "the one definition" of what has gone back, and re-deriving it is the fifth copy of an arithmetic this repository already had to collapse once (`store_credit_balances`).

`greatest(..., 0)` per order, never on the total: a shop that over-compensates one order must not let it eat another order's spend.

ORDERING, and it is load-bearing. `order_refunds` is created at line 4244, 594 lines BELOW `member_spend`. A `LANGUAGE sql` function with a dollar-quoted body resolves its references at CALL time, so `001` still applies cleanly and the failure would appear only when a customer loads /account — in production and in nothing else. Move the `member_spend` and `member_tier` pair (lines 3650–3679) and their two `GRANT EXECUTE` lines (3689 and the `member_tier` line beside it, plus the admin grant at 3804) to immediately after `GRANT SELECT ON order_refunds ...` at line ~4265. `award_loyalty_points` at 4458 calls `member_tier` and stays below it. Do NOT move `order_refunds` up instead: it depends on `refunds` and `payments`, which are declared after 3650.

Update the comment above it. It currently reads "What a customer has spent on committed orders in a rolling window"; it must state that a refund reduces it and that the floor is per order.

=== HALF 2: a return claws its points back ===

(a) migrations/001, immediately after `award_loyalty_points` (line ~4500, before the `GRANT EXECUTE` at 4536). New SECURITY DEFINER function — `loyalty_entries` INSERT is revoked from both roles (line 4212), so this is the only shape a reversal can take:

    CREATE FUNCTION reverse_order_points(
        p_order_id uuid, p_return_request_id uuid, p_points bigint
    ) RETURNS bigint
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
    DECLARE
        v_account uuid;
        v_outstanding bigint;
    BEGIN
        IF p_points <= 0 THEN RETURN 0; END IF;   -- nothing to take back is not an error

        SELECT a.id INTO v_account FROM store_credit_accounts a
        JOIN orders o ON o.user_id = a.user_id WHERE o.id = p_order_id;
        IF v_account IS NULL THEN RETURN 0; END IF;   -- a guest order earned nothing

        -- The strongest lock FIRST. An INSERT takes FOR KEY SHARE for the FK and
        -- the AFTER trigger then wants FOR UPDATE: the upgrade deadlock that LOOKS
        -- like the guard working, which redeem_loyalty_points already learned.
        PERFORM 1 FROM store_credit_accounts WHERE id = v_account FOR UPDATE;

        -- Never claw back more than this order actually awarded, net of what
        -- earlier returns on the same order already took.
        SELECT coalesce(sum(points), 0) INTO v_outstanding
        FROM loyalty_entries WHERE order_id = p_order_id;
        IF v_outstanding <= 0 THEN RETURN 0; END IF;

        INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key,
                                     order_id, expires_on)
        VALUES (v_account, -least(p_points, v_outstanding), 'return',
                'return:' || p_return_request_id::text, p_order_id, NULL)
        ON CONFLICT (idempotency_key) DO NOTHING;

        IF NOT FOUND THEN RETURN 0; END IF;
        RETURN least(p_points, v_outstanding);
    END;
    $$;

    GRANT EXECUTE ON FUNCTION reverse_order_points(uuid, uuid, bigint) TO admin;

`expires_on` must be NULL: `loyalty_entries_expiry_matches_sign` (line 4157) is `(points > 0) = (expires_on IS NOT NULL)`. The key is the RETURN id, not the order id, so several partial returns on one order each post once and a retried decision posts nothing twice — the same shape as `refundRequestKey` and `'return-credit:'`.

It MUST be declared above the privilege sweep at the end of `001` — CLAUDE.md mistake #19; anything appended below it keeps PUBLIC EXECUTE.

(b) migrations/001:4189 `loyalty_never_negative`. Add, as the first statement of the body, before the balance read:

    IF NEW.points < 0 AND NEW.reason = 'return' THEN RETURN NEW; END IF;

with a comment saying why: a customer who farmed points and redeemed them before the parcel came back must end OWING points, not have the clawback refused. `loyalty_balances` then reports a negative figure and `loyalty.Store.Redeem` already refuses `points > balance` (internal/loyalty/store.go:64), so a negative balance is unspendable and earns itself back. Clamping the clawback to the current balance instead would make redeem-before-return the new farm, which is the same defect one step along.

(c) internal/admin/query.sql, beside `CompensateReturnWithCredit` at line 1105:

    -- name: ReverseReturnPoints :one
    SELECT reverse_order_points(@order_id, @return_id,
        -- What the REFUNDED amount earned, at the multiplier the award used.
        (@refunded_cents::bigint / 10000
         * coalesce((SELECT t.points_multiplier_bp FROM membership_tiers t
                     WHERE t.id = member_tier(@user_id, @window_days::integer, @order_id)), 10000)
         / 10000)::bigint)::bigint AS points_reversed;

Proportional to what went back, not the whole award: a partial return must take back only the part it refunded. Run `make sqlc` afterwards; internal/db is never hand-edited.

(d) internal/admin/store.go:994 `payApprovedReturn`. After the `split.Credit` block (ends line 1021) and before the `providerRef` order-event block, add the call, passing `split.Card + split.Credit` as `refunded_cents`, `row.OrderID`, `row.ID`, `row.UserID.UUID`. Guard it with `if row.UserID.Valid` — a guest order has no account.

It goes HERE and not in `closeReturn`: the file's own rule is that a compensation is posted once the money has actually gone, and `payApprovedReturn` is already the resume-safe half (`stillOwedOnReturn`). Because the key is the return id and the function is `ON CONFLICT DO NOTHING`, a retry that re-enters this path posts nothing twice, so it needs no entry in `stillOwedOnReturn`.

`internal/admin` must not import `internal/payment`; `MembershipWindowDays` currently lives in internal/payment. Either move that constant to `internal/loyalty` (which both may import and which already owns `PointsPerHundred`) or re-declare it in `internal/admin` — moving it is correct, since two copies of a window is the mirror-constant problem `/admin/health` already had to undo.

=== What the fix MUST NOT do ===

- MUST NOT add a 'returned' member to `orders_fulfillment_status_known` or a transition to `orders_legal_transition`. The order's fulfilment history is a true record of what the shop did; the correction belongs to the money, not the status. A new status would also silently move the order out of `committed_orders` and strand its stock and its coupon redemption.
- MUST NOT UPDATE or DELETE any `loyalty_entries` row: `loyalty_entries_append_only` (line 4167) refuses it for everyone including the owner.
- MUST NOT reverse points from `Decide` before the refund is paid — a claim that fails at Stripe would take the points off a customer who got no money.
- MUST NOT touch `committed_orders`. It is right about its own question and four other callers depend on it.

=== Two things that will break and must be updated in the same commit ===

- README.md:120 states "20 `SECURITY DEFINER` functions" and CLAUDE.md's departure #2 describes the store-executable set. `reverse_order_points` makes it 21. `TestTheStatedSchemaTotalsAreTheRealOnes` (internal/db/statedtotals_integration_test.go:29) reads both documents and the catalogue and will fail until both are corrected. Note the function is granted to `admin` only, so the store-executable count is unchanged.
- CLAUDE.md's loyalty paragraph ("會員等級 is `membership_tiers` … DERIVED from what they have spent") must gain the sentence that spend is net of refunds, and the loyalty paragraph must state that a return claws back proportionally. Leaving them is exactly the comment-outlives-the-code drift the file records as #31 and #34.


## The lock, and how to see it fail first

One new integration test in internal/admin/integration_test.go (build tag `integration`, package admin_test), plus one schema case. Three mutations, each proven RED before the fix is called a lock.

TEST — `TestAFullReturnTakesBackItsPointsAndSpend`

Fixture must run the real commercial path, not INSERT the end state (CLAUDE.md #26: a fixture reaching past the application is a fixture for a claim nobody is testing). Place an order through the cart, capture it through the payment path so `AwardOrderPoints` actually fires, ship it, deliver it, then drive `admin.Store.Decide(ctx, returnID, "approved", ...)` to a settled refund. The order total must sit ABOVE a `membership_tiers.min_spend_cents` threshold and be the customer's only order, so the tier answer is unambiguous. Use a unique tracking number and a unique credit reason per run — `order_shipments_tracking_key` is unique and `GrantCredit` is idempotent on (customer, amount, reason), which is how the /admin/returns layout fixture silently stopped working.

Assert, after the refund settles:
1. `member_spend(user, MembershipWindowDays, NULL)` == 0 EXACTLY. Not "less than before" — an assertion coarser than the error is no lock (CLAUDE.md #34, the warranty month-count).
2. `member_tier(user, MembershipWindowDays, NULL)` IS NULL.
3. `SELECT points FROM loyalty_balances b JOIN store_credit_accounts a ON a.id=b.account_id WHERE a.user_id=$1` == 0 EXACTLY.
4. Exactly one row in `loyalty_entries` with `order_id = order AND points < 0`, its `idempotency_key = 'return:'||returnID` and its `expires_on IS NULL`.

TEST — the PARTIAL case, in the same function or a sibling. Order of TWO lines at different prices; return ONE. Assert `member_spend` equals the gross minus exactly the refunded amount (compute the expected figure from `return_refundable_amount`, do not hard-code), and that the negative entry is the points the REFUNDED amount earned, not the whole award. This case is mandatory: a full-return-only fixture is one where "reverse everything" and "reverse proportionally" give the same answer, which is CLAUDE.md #37's "a fixture where two rules agree is a fixture that tests neither".

TEST — idempotency. Call `Decide(..., "approved", ...)` a second time on the already-approved return (the documented resume path). Assert `count(*) FROM loyalty_entries WHERE order_id = $1 AND points < 0` is still 1 and `member_spend` is unchanged.

TEST — the overdrawn clawback, in internal/db's conformance suite. Award points, redeem the whole balance to store credit through `redeem_loyalty_points`, THEN return the order. Assert the clawback SUCCEEDS and `loyalty_balances.points` is negative. Bind any refusal assertion to `PgError.ConstraintName`, never to the error text (CLAUDE.md #8/#32).

MUTATIONS — each must be SEEN to apply and SEEN to go red.

M1 (points half). Delete the `ReverseReturnPoints` call from `payApprovedReturn` in internal/admin/store.go. Assertions 3 and 4 must go RED. Prove the edit applied by grepping for the exact post-`gofmt` text and checking the count went to 0 before running — CLAUDE.md's false-green mode #3 was an edit that matched nothing while a `grep -c` on the wrong pattern reported success.

M2 (spend half). Restore `member_spend`'s old body (drop the `JOIN order_refunds` and the `greatest(...)`), `make db-reset`, re-run. Assertions 1 and 2 must go RED. Verify the mutation landed by running `SELECT prosrc FROM pg_proc WHERE proname='member_spend'` and reading it, not by trusting the sed.

M3 (proportionality). Change `ReverseReturnPoints`'s expression to reverse the FULL order award regardless of what was refunded. The full-return case must stay green and the PARTIAL case must go RED. If the partial case stays green, the partial fixture is not exercising the split and the lock is not a lock.

M4 (the overdraw exemption). Remove the `NEW.reason = 'return'` early return from `loyalty_never_negative`. The overdrawn-clawback case must go RED with `PgError.ConstraintName = 'loyalty_never_negative'` (or whatever name that trigger raises), proving the exemption is what admits it rather than the balance happening to be positive.

M5 — record GREEN, do not dress it up. `reverse_order_points`'s `FOR UPDATE`-first lock ordering cannot be distinguished by any test that is not flaky: every posting path takes the same lock first, so removing it leaves the suite green, exactly as `loyalty_never_negative`'s own comment already records for the same reason. Note it in the test file the way the constant-time compare in internal/twofactor is noted.

Existing guards to re-run, both of which this change will trip: `TestTheStatedSchemaTotalsAreTheRealOnes` (README.md's SECURITY DEFINER count moves 20 → 21) and `TestEveryRaisedRuleIsAssertedByName` (it derives its corpus from `pg_proc`, so any `RAISE ... CONSTRAINT` added to `reverse_order_points` needs a case naming that constraint inside a Go string literal the moment the body is written). Then `make verify && make test-integration` with the shuffle on.
