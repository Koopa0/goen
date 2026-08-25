# refund-completion-definitions

**Verdict** PARTLY · **Severity** high · **Origin** external report (verified this round)

**Files** internal/admin/store.go, internal/admin/query.sql, internal/db/query.sql.go, internal/admin/integration_test.go, internal/db/refund_test.go, migrations/001_initial_schema.up.sql, internal/invoice/query.sql, internal/invoice/store.go, internal/ui/pages/admin.go, internal/ui/pages/admin.templ, internal/ui/pages/cart.templ, internal/admin/report.go, internal/ui/pages/adminreport.templ


## Root cause

"The refund completed" is decided three times from three different pieces of evidence, and only one of them is the documented definition.

`order_refunds` was created (commit fcaded7) precisely because "what has gone back" was being re-derived per caller, and its comment says the rule "would otherwise be copied into whichever caller was written next". Two callers were converted — the 折讓 form and the invoice allowance — and two were not, because they do not LOOK like refund totals: one is a Go boolean (`providerRef != ""`) and one is a windowed aggregate that cannot literally SELECT from a per-order view.

Underneath both is the same substitution: the Stripe provider reference is being used as the proxy for "money moved". It is a proxy that is exactly right for the card half and empty for a source that has no provider at all — the identical shape as CLAUDE.md's recorded `EXISTS(succeeded payment)` ⇒ "committed" trap, which also silently excludes the zero-owed, credit-funded order. `refundSplit` exists specifically to say money has two sources; the two sites read only one field of it.

And no lock holds refund totals to the view the way `TestEveryCreditBalanceReadsTheOneView` holds balances, so the third and fourth copy were free to keep existing after the second was removed.


## Reproduction — the evidence this rests on

EXECUTED against the live dev database, in a transaction that was ROLLED BACK.

The dev DB already contains the exact order the claim needs: GO-260824-000002 is wholly store-credit funded (no `payments` row, `order_amount_owed` = 0, one `store_credit_entries` row of -3,690,000 keyed `order:...`), status `delivered`, with an open return request 01a03177-9451-7ebb-ac34-d24ce51d0e98 whose `return_refundable_amount` is 3,690,000.

  $ psql "$GOEN_DATABASE_URL"
  BEGIN;
  -- payApprovedReturn's split here is Card=0 (row.PaymentID is invalid, so
  -- capturedRemaining=0 and split.Card = min(3690000, 0) = 0) and Credit=3690000.
  SELECT post_store_credit(<user>, 3690000, '退貨退回購物金', <order>,
                           'return-credit:01a03177-9451-7ebb-ac34-d24ce51d0e98', NULL);
  -- and payApprovedReturn writes NO order_events row, because providerRef == "".

  --- 1. order_refunds (the documented ONE definition) ---
   card_cents | credit_cents |  total
  ------------+--------------+---------
            0 |      3690000 | 3690000

  --- 2. RevenueSince.refunded_cents (/admin/reports, 30d), verbatim from
  ---    internal/admin/query.sql:719-722 ---
   reports_refunded_cents
  ------------------------
                        0

  --- 3. the customer timeline (order_events) ---
   refunded_events
  -----------------
                 0
  ROLLBACK;

So NT$36,900 demonstrably went back to the customer, and three surfaces of one shop report it as 3,690,000 / 0 / nothing.

WHERE THE THREE DEFINITIONS LIVE (the side-by-side the task asked for):

(a) `order_refunds` — migrations/001_initial_schema.up.sql:4244. Card + credit. Its COMMENT (4260) says "The one definition: a 折讓 may not relieve more than this, and the form that files one offers exactly this."

(b) internal/admin/store.go:997-1032, `payApprovedReturn`. `providerRef := ""` (997); set only at 1006 and only `if state == RefundSucceeded`; then

      1026: if providerRef != "" {
      1028:     OrderID: row.OrderID, Kind: "refunded", ActorUserID: actor,
      1029:     Note: text(providerRef),

    with the comment at 1003-1005 stating the intended rule — "order_events is rendered on the customer's own order page, so a refunded event is written only once the money has actually left." The predicate under that comment does not implement it: it asks whether CARD money left. This is CLAUDE.md mistake #31's shape (a comment naming the rule, and the line beneath it implementing a narrower one), and the code's own struct doc names the reachable case three lines up — store.go:924-925: "Zero means there is no provider call to make, which is the wholly-credit-funded case."

(c) internal/admin/query.sql:709-722, `RevenueSince`. `refunded_cents` is `sum(r.amount_cents) FROM refunds r WHERE r.status='succeeded' AND r.created_at >= …` — card only. Rendered on /admin/reports as its own figure (internal/admin/report.go:26,50 → pages.AdminReportView.RefundedCents → adminreport.templ:48-49).

WHAT THE CLAIM GETS WRONG — the 折讓 / e-invoice half is already closed, by commit fcaded7 "Give \"what has gone back to the customer\" one definition":
 - internal/admin/query.sql:1424-1426 `SettledRefundsForOrder` → `SELECT (card_cents + credit_cents) FROM order_refunds`, read at internal/admin/store.go:379-383 into `view.RefundedCents`, which is what `CanAllowInvoice()` (internal/ui/pages/admin.go:419-420) and `AllowanceDefault()` (425-426) use, and what gates the 折讓 form at internal/ui/pages/admin.templ:485-508.
 - internal/invoice/query.sql:120-123 `RefundedForOrder` → the same view, read by `refundableRoom` at internal/invoice/store.go:304.
Both saw 3,690,000 in the reproduction above. So the wholly-credit refund does NOT vanish from the 折讓 form or from what an allowance may relieve.

SAME DEFECT OR TWO: one root cause, two independent code sites and two independent fixes. (b) is a Go predicate, (c) is a SQL sum; neither reads the other and neither reads the view. There is no guard tying refund totals to `order_refunds` — `internal/db/credit_test.go`'s `TestEveryCreditBalanceReadsTheOneView` does the equivalent job for `store_credit_balances` and matches only `store_credit_entries`, so it cannot see either of these.

WORKING TREE: I wrote no files. `git status --porcelain` shows five `zz*probe*_test.go` files created 11:07 by other agents in this review run, still in flight; I left them alone rather than deleting concurrent work.


## Blast radius

Two surfaces, both silent, on every refund with no successful CARD component — i.e. every return on an order paid wholly from store credit or zeroed by a 100% coupon, and the credit half of every split refund whose card half is still `pending`/`requires_action`.

1. THE CUSTOMER IS NOT TOLD. `order_events` is the timeline on the customer's own order page (internal/ui/pages/cart.templ:600-610, label at internal/ui/pages/cart.go:409). No `refunded` event is written, so the page shows the return decided and nothing about money. The customer has no per-entry credit ledger either — internal/account/query.sql:140 `StoreCreditBalance` reads only `store_credit_balances`, a single number — so their balance changes with nothing anywhere saying which order it came from. The shop's own /admin/orders/{number} timeline (internal/ui/pages/admin.templ:332) is missing it too, so a staff member answering "did we refund this?" reads a timeline that says no.

2. THE SHOP'S NUMBERS UNDERSTATE REFUNDS. /admin/reports shows 「已退款」 as 0 for the window. Understating what went back is the direction that flatters the shop, on the page an owner reads to judge the return rate — and CLAUDE.md's own note on that query argues returns are certain rather than hypothetical under 消保法 §19, which is exactly the population most likely to be credit-funded.

Frequency is not marginal: a fully store-credit-funded order is a first-class documented path here ("fully covered by credit, an order is committed with no payment row at all"), and the goodwill/§19 return is the ordinary reason such an order comes back. Nothing raises, nothing logs, no constraint fires — the refund is correct in the ledger and correct in the 折讓 form, so the only symptom is two pages quietly disagreeing with a third.

Not critical: no money is lost or double-paid, the credit reaches the customer, and the allowance path is right.


## Fix

Two independent fixes plus one guard. `internal/db` is sqlc output — edit `query.sql` and run `make sqlc`, never the generated file. `migrations/001` is still amended in place per CLAUDE.md (nothing deployed), but neither fix needs a schema change.

A) internal/admin/store.go, `payApprovedReturn` (lines 993-1033).
Replace the provider-reference proxy with an explicit "money moved" flag set by EITHER half:

    providerRef := ""
    moved := false
    if split.Card > 0 {
        ref, state, err := s.refundCard(ctx, row, split.Card, resolution)
        if err != nil { return fmt.Errorf("%w: %w", ErrRefundIncomplete, err) }
        if state == RefundSucceeded { providerRef = ref; moved = true }
    }
    if split.Credit > 0 {
        if _, err := s.q.CompensateReturnWithCredit(ctx, …); err != nil { … }
        moved = true          // post_store_credit is synchronous and committed:
                              // this money HAS left, and has no provider ref to show for it.
    }
    if moved {
        if err := s.q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
            OrderID: row.OrderID, Kind: "refunded", ActorUserID: actor,
            Note: text(providerRef),
        }); err != nil { … }
    }

Rewrite the comment at 1003-1005 so it states the rule the predicate now implements ("money has actually left by EITHER source; a card refund Stripe has only accepted has not"), because the current sentence is the one that outlived its predicate.

It MUST NOT write the event when `split.Card > 0`, the card is not `RefundSucceeded`, and `split.Credit == 0` — internal/admin/integration_test.go:930-943 locks that and must stay green.

Note stays `providerRef`, so a credit-only refund writes an EMPTY note. Do not invent a note string: both renderers skip an empty note (cart.templ:606, admin.templ:332), and `order_events.note` is customer-facing prose written from Go with no locale to read — the `RecordCancellation` '顧客自行取消' mistake CLAUDE.md records. If a credit-only refund should say something on the timeline, that is a new i18n key rendered from `Kind`, not a literal in the note column, and it is out of scope for this fix.

Known and unchanged by this fix, worth naming so it is not reintroduced as a surprise: on a RESUME after a crash, `refundsAlreadyDone` (store.go ~890-918) zeroes the half that already settled, so a resume whose only remaining work is nothing writes no event. `RecordOrderEvent` is a bare INSERT with no idempotency key, so widening the flag to "already settled" would duplicate the event on every retry. Keep the flag meaning "this pass moved money".

B) internal/admin/query.sql:709-722, `RevenueSince`. Add the credit half, windowed identically, with the SAME predicate `order_refunds.credit_cents` uses (positive entries carrying an order_id), so the window figure sums exactly the set the view sums per order:

    coalesce((SELECT sum(r.amount_cents) FROM refunds r
              WHERE r.status = 'succeeded'
                AND r.created_at >= now() - make_interval(days => @window_days::integer)), 0)::bigint
    + coalesce((SELECT sum(e.amount_cents) FROM store_credit_entries e
                WHERE e.order_id IS NOT NULL AND e.amount_cents > 0
                  AND e.created_at >= now() - make_interval(days => @window_days::integer)), 0)::bigint
        AS refunded_cents

`store_credit_entries_order_idx` exists but not on `created_at`; at this scale it is a scan of a small table and no index is warranted yet — say so in the comment rather than adding one speculatively. `admin` already holds SELECT on the table (internal/admin/query.sql:385 reads it on the same pool). Then `make sqlc && go build ./...`.

Make ONE decision explicitly in that comment rather than by omission: this set includes `reverse_order_credit`'s positive entry on a CANCELLED order, whose revenue was never counted (the order is not in `committed_orders`). That is exactly what `order_refunds` already counts, and excluding it here would create a fourth definition. If the shop wants cancellations out of "refunded", it comes out of the VIEW, and the 折讓 form follows — not out of one caller.

C) The lock that stops the next copy: new test `TestEveryRefundTotalReadsTheOneView` in internal/db, modelled line-for-line on internal/db/credit_test.go (`queryFiles`/`splitQueries`, allowlist checked by IDENTITY with a stated reason per entry, and an error when an allowlist entry matches nothing). It refuses any query that computes a refund TOTAL outside `order_refunds` — `sum(...amount_cents)` over `refunds`, or over positive `store_credit_entries` — and names the allowed exceptions with their reasons: `RefundedSoFar` (per-PAYMENT, and excludes its own request_key so a retry is not refused by itself), and after fix (B) `RevenueSince` (a WINDOW over time, which a per-order view cannot express — the entry must say the predicate is required to match `order_refunds.credit_cents` exactly). `ReturnRefundSettled` and `ReturnCreditPosted` are EXISTS probes, not totals, and should not match the pattern at all — if they do, tighten the regex rather than allowlisting them.


## The lock, and how to see it fail first

Every lock below must be seen RED before the fix. The fixture rule that decides all of them: the order must be WHOLLY STORE-CREDIT FUNDED (no `payments` row). A card-funded fixture passes with and without the fix — CLAUDE.md mistake #37, "a fixture where two rules agree is a fixture that tests neither."

1. `TestACreditOnlyRefundIsOnTheCustomersTimeline` — internal/admin/integration_test.go, beside the existing return-decision cases.
   Fixture: user, credit granted, order placed and fully funded from credit (owed 0, no payments row), picked, shipped, delivered, return requested, lines inspected. Then `Decide(ctx, requestID, "approved", …)`.
   Assert: exactly one `order_events` row with `kind='refunded'` on that order, AND one positive `store_credit_entries` row keyed `return-credit:<id>`.
   MUTATION (must go RED): restore `if providerRef != ""` at store.go:1026 → event count 0. Prove the mutation applied by seeing the diff, not by assuming — CLAUDE.md's false-green mode #3.

2. Re-run the EXISTING case at internal/admin/integration_test.go:930-943 (card refund Stripe has not landed ⇒ 0 `refunded` events) unchanged, to prove the fix did not widen the rule.
   MUTATION (must go RED): change the new gate to `if true` → that case reports an event on an order whose money has not left.

3. `TestASplitRefundWhoseCardIsPendingStillRecordsTheCreditThatLanded` — card half returns `RefundPending` from the fake `refunder`, credit half posts. Event count must be 1.
   This is the case that tells "the card succeeded" apart from "money moved", and neither (1) nor (2) can see it: (1) has no card half and (2) has no credit half.
   MUTATION: gate on `state == RefundSucceeded` alone → RED.

4. `TestTheRefundFigureCountsCreditToo` — internal/admin/integration_test.go. After the fixture of (1), call `s.Report(ctx, 30)` and assert `view.RefundedCents` equals the compensation, and equals `card_cents + credit_cents` read straight from `order_refunds` for that order — asserting the two AGREE, not just that a number is non-zero, so the lock is about the definition rather than about an amount.
   MUTATION (must go RED): delete the added credit term from `RevenueSince`, `make sqlc`, re-run → 0 against an expected 3,690,000.
   An assertion of "> 0" would be satisfiable by the card term and is not a lock — CLAUDE.md mistake #34's "an assertion coarser than the error it is meant to catch is not a weak lock, it is no lock."

5. `TestEveryRefundTotalReadsTheOneView` (the new guard in internal/db) proven by mutation itself, as review-process.md requires of a new verification instrument: point `RevenueSince` back at the card-only sum and remove its allowlist entry → the guard must name the query and go RED; and delete an allowlist entry's query → the "the allowlist names X and nothing matched it" branch must fire, the identity check credit_test.go already carries.

Run `make verify` (no Docker) and `make test-integration` (Docker) before calling it done; the integration suite shuffles, so the new fixtures must create everything they need rather than leaning on the seed (CLAUDE.md mistake #23).
