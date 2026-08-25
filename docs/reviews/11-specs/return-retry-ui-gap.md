# return-retry-ui-gap

**Verdict** CONFIRMED · **Severity** high · **Origin** external report (verified this round)

**Files** internal/admin/store.go, internal/admin/query.sql, internal/ui/pages/adminreturns.go, internal/ui/pages/adminreturns.templ, internal/i18n/chrome_admin_pages.go, internal/admin/handler.go, internal/admin/integration_test.go, internal/ui/pages/adminreturns_test.go, CLAUDE.md


## Root cause

The view model answers a question the page no longer needs. `Decided` was written when "decided" and "finished" were the same fact — a failed refund used to leave the return at `requested`. Round 6 moved the claim ahead of the money (approve commits, then Stripe is called), which split that fact in two: **decided** and **settled**. `Store.Returns` still projects only the first (`Decided: r.Status != "requested"`, store.go:795), and `ReturnQueue` (internal/admin/query.sql:233) reads nothing from `refunds` or `store_credit_entries` at all, so the template physically cannot tell "approved and paid" from "approved and the money never went". `Decide` grew the retry; the queue that renders the button never learned the state existed. Mistake #35's shape — a capability every guard reports as wired, with no entrance — and #13's — two correct halves that disagree, which every guard here is blind to because they all ask what is ABSENT.


## Reproduction — the evidence this rests on

EXECUTED, three ways.

(1) Live HTTP against the running dev server. I minted a staff session for `layout-check@goen.invalid`, inserted one extra `return_requests` row and moved it to the exact post-failure state (`status='approved'`, `decided_at` set — the state `Decide` commits BEFORE it calls Stripe), then fetched `/admin/returns`:

    rows in queue: 3
    decide forms:  2
      -> returns/01a03177-9451-7ebb-ac34-d24ce51d0e98/decide
         returns/01a03195-4839-7732-a818-4eec8c5c3d57/decide
    probe id present in any action? 0

The two `requested` rows get a decision form; the approved row's id appears NOWHERE in the HTML — it renders with no control at all (not even a link). A second run parsed the row and confirmed it IS listed: `order=GO-260824-000002 status='已同意 · 2026-08-24 11:14'`. Probe row and session deleted afterwards; `select count(*) from return_requests where reason like 'PROBE%'` = 0, `git status --porcelain` empty.

(2) Template render, executed as a temporary Go test in `internal/ui/pages` (since deleted), rendering `pages.AdminReturns` with one row twice:

    status=requested decide-form=true  inspect-form=false complete-form=false
    status=approved  decide-form=false inspect-form=true  complete-form=false

The only form an approved return gets is `/inspect`, which records received/restocked counts and never touches the refund.

(3) What the page tells the staff member — fetched live from `/admin/returns?refundfailed=1`:

    <p class="ui-alert ui-alert--info" role="status">這筆退貨已經核准,但退款沒有完成。退款紀錄已經留下,請確認 Stripe 後台再處理一次 —— 核准本身不需要、也無法重做。

English half (internal/i18n/chrome_admin_pages.go:117): "…check the Stripe dashboard before running it again." The page says "run it again" and offers nothing to run.

The exact branch chain, quoted:
- internal/admin/store.go:807 `Decide` — on approve it calls `closeReturn(... "approved" ...)` FIRST and `payApprovedReturn` after (store.go:853), so a Stripe failure leaves `return_requests.status = 'approved'`. `internal/admin/integration_test.go` asserts exactly that: "return is %q after a refund that did not happen, want approved".
- internal/admin/store.go:795 `Decided: r.Status != "requested",` — every non-requested status collapses to Decided.
- internal/ui/pages/adminreturns.templ:74 `if !r.Decided {` wraps the entire decision form (the only thing that posts to `/admin/returns/{id}/decide`, the only route registered for it, cmd/goen/server.go:253).
- The retry the UI cannot reach is real and tested at the store level: store.go:879 `retry := row.Status == "approved" && decision == "approved"`, and `TestAStalledRefundCanBeRetriedToCompletion` (internal/admin/integration_test.go:837) drives `Store.Decide` DIRECTLY — never through a handler or a rendered form — and asserts "the retry was refused, so a stalled refund can never be finished and the customer is never paid".

Two corrections to the claim's wording, neither changing the outcome: the method is `Store.Decide` (store.go:807), not `Store.Approve`, and the retry gate is at store.go:879, not ~792. Not a recorded decision: `docs/roadmap.md` contains no "retry" entry, and docs/reviews/07-codex-round6-dispositions.md §H8 records the retry being BUILT (round 6) with no note that the door was left out.


## Blast radius

Every return whose payout did not complete for a resumable reason, which is precisely the case the refund state machine was rewritten to make recoverable:

- a card refund left `pending` by an ambiguous transport error (timeout) — `refunds` row survives, `refunds_settled_is_history` still allows pending→succeeded, and re-posting `decision=approved` finishes it;
- a split refund whose card half landed and whose store-credit compensation did not — `stillOwedOnReturn` (store.go:896) exists solely to resume that half, and its comment says "no other door posts that credit";
- a wholly credit-funded return whose `CompensateReturnWithCredit` failed.

In all three the customer has sent the goods back, the shop has agreed, and money it owes is unsent. The only ways to send it are a hand-crafted POST to `/admin/returns/{id}/decide` with `decision=approved` (nothing in the UI produces one) or SQL. Not silent, but actionless: `/admin/health` lists the refund read-only (internal/admin/health.go:94, query.sql `OpenRefunds`) with no link to the return and no control (internal/ui/pages/adminhealth.templ:165-193), and the queue notice tells staff to "再處理一次 / run it again". Rare per order — it needs a Stripe/network failure — but recoverability is the whole point of the design it defeats.

One narrowing, in the shop's favour: a refund Stripe REFUSED outright is terminal by design (`refunds_settled_is_history` forbids failed→succeeded) and a retry there correctly returns `ErrRefundIncomplete` — see `TestARefundStripeRefusedOutrightLeavesTheDecisionStanding` (integration_test.go:961) and its comment "A refund Stripe REFUSED is not retryable here, and that is the schema's deliberate position". So the fix must offer the control for outstanding-and-resumable and must NOT offer a button that can only ever fail.


## Fix

No handler, route or store-write change is needed: `POST /admin/returns/{id}/decide` with `decision=approved` already resumes an approved return (store.go:879) and `Handler.Decide` already maps the outcomes (handler.go:537-559). What is missing is the state on the view model and the control in the template.

1) `internal/ui/pages/adminreturns.go` — add two fields to `AdminReturn` beside `Decided` (line 24), each with a comment saying decided and settled are two facts:
   - `PayoutOutstanding bool` — approved, and money it owes has not gone.
   - `PayoutBlocked bool` — that outstanding money is behind a refund the provider terminally refused, which no retry can move.
   Add `func (r AdminReturn) CanRetryPayout() bool { return r.Decided && r.PayoutOutstanding && !r.PayoutBlocked }` and `func (r AdminReturn) PayoutStranded() bool { return r.Decided && r.PayoutOutstanding && r.PayoutBlocked }`. Do NOT re-derive either from `Status`; both must be assigned in the store or `TestEveryViewModelFieldIsAssigned` fails, which is the intent.

2) `internal/admin/store.go` — in `Returns` (753), for each row with `r.Status == "approved"` only, fill the two fields from THE SAME path `Decide`'s retry takes, so the button appears exactly when a retry would do something. Extract from `Decide` a method

       func (s *Store) outstandingOnReturn(ctx context.Context, requestID uuid.UUID) (owed refundSplit, done bool, err error)

   that does `s.q.ReturnForDecision(ctx, requestID)` → `s.splitRefund(ctx, &row)` → `s.stillOwedOnReturn(ctx, requestID, split)`; `Decide` keeps calling `splitRefund`/`stillOwedOnReturn` as it does today, so there is one definition of "what is still owed" and not two. `PayoutOutstanding = !done`. A `splitRefund` refusal (`ErrRefused`) here must NOT fail the page: treat it as outstanding-and-blocked and log, because a return whose money no longer fits across the two sources is exactly a row a person has to look at. Never call this for a non-approved row — `rejected`/`completed` must stay `false`.

3) `internal/admin/query.sql` — add, next to `ReturnRefundSettled` (line 1412), the mirror this needs:

       -- name: ReturnRefundTerminal :one
       SELECT EXISTS (
           SELECT 1 FROM refunds
           WHERE return_request_id = $1 AND status IN ('failed', 'cancelled')
       )::boolean AS terminal;

   with a comment naming `refunds_settled_is_history` as the reason it is terminal. `PayoutBlocked = terminal && owed.Card > 0` — a card half the schema forbids from ever succeeding. Run `make sqlc`; never hand-edit `internal/db`.

4) `internal/ui/pages/adminreturns.templ` — at line 74 turn the single `if !r.Decided` into three arms:

       if !r.Decided {            // existing decision form, unchanged
       } else if r.CanRetryPayout() {
           <form class="goen-admin__decide" method="post" action={ templ.SafeURL(r.Action()) }>
               <p class="goen-admin__hint">{ i18n.T(ctx, i18n.KeyAdminRetPayoutOutstanding) }</p>
               <input type="hidden" name="decision" value="approved"/>
               <div class="ui-field">
                   <label class="ui-label" for={ "retry-" + r.ID }>{ i18n.T(ctx, i18n.KeyAdminRetResolution) }</label>
                   <input class="ui-input" id={ "retry-" + r.ID } name="resolution" maxlength="300" autocomplete="off"/>
               </div>
               <div class="goen-admin__decidebtns">
                   <button class="ui-btn ui-btn--primary" type="submit">{ i18n.T(ctx, i18n.KeyAdminRetRetryPayout) }</button>
               </div>
           </form>
       } else if r.PayoutStranded() {
           <p class="goen-admin__hint">{ i18n.T(ctx, i18n.KeyAdminRetPayoutStranded) }</p>
       }

   The hidden `decision=approved` is required — that is the store's retry gate — and the button must be labelled "重新退款 / Send the refund again", never 同意: the decision is not being retaken, which is what the existing notice already tells the reader. Plain `<form method="post">`, no `hx-*`: the write-face rule. Regenerate with `templ generate` (from the repository root only — mistake #22) and never edit `*_templ.go`.

5) `internal/i18n/chrome_admin_pages.go` — three new keys through `key(...)` with both `ZhHant` and `En` (`i18n.Message` is the one exhaustruct type):
   - `KeyAdminRetRetryPayout` — 「重新退款」 / "Send the refund again".
   - `KeyAdminRetPayoutOutstanding` — this return was approved and the money has not gone; sending again asks the provider with the same request key, so it cannot pay twice.
   - `KeyAdminRetPayoutStranded` — the provider refused this refund outright; it cannot be re-sent from here, so refund it by hand at Stripe and see /admin/health.
   Amend `KeyAdminNoticeRefundFailed` (line 117) so it points at the new button instead of only the Stripe dashboard — the sentence that sent staff looking for a control that did not exist.

6) `CLAUDE.md` — the returns paragraph should gain a sentence that decided and settled are two facts on that queue, in the shape of the `committed_orders` / `settled_orders` split already recorded.

Out of scope but worth queueing by name (`docs/roadmap.md`): `payApprovedReturn` (store.go:994) calls `refundCard` before `CompensateReturnWithCredit`, so a terminally-failed card half also strands a credit half that is otherwise postable. `PayoutBlocked` will render that row as stranded, which is honest, but the credit is still unpayable by any door.


## The lock, and how to see it fail first

Two locks, each proven by mutation before the fix is believed.

A. `internal/admin/integration_test.go` (build tag `integration`, real PostgreSQL) — `TestAStalledRefundOffersItsRetryInTheQueue`. Three sub-cases over `returnedOrder(t, 1)` fixtures, each then reading `s.Returns(ctx)` and finding the row BY ID (the suite is shuffled and other tests leave returns behind — follow the identity-lookup habit of `TestTheHealthPageNamesARefundThatDidNotLand`):
  1. `fakeRefunder{refundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout")}` → `Decide(...,"approved",...)` errors; assert `Decided == true`, `PayoutOutstanding == true`, `PayoutBlocked == false`.
  2. `fakeRefunder{state: admin.RefundFailed}` → assert `PayoutOutstanding == true`, `PayoutBlocked == true`.
  3. `fakeRefunder{}` (succeeds) → assert `PayoutOutstanding == false`. This third case is what stops the flag being hard-coded true, which is the false-green this repo has met twice.
 Add a fourth over the split path modelled on the existing both-sources retry test: card settled, credit not posted → `PayoutOutstanding == true`, so the flag cannot be implemented as "no succeeded refund exists" (which reads that row as paid — the exact defect `stillOwedOnReturn`'s comment records).
 Mutations that must go RED, run and recorded: (i) hard-code `PayoutOutstanding: false` in `Returns` → 1, 2 and 4 fail; (ii) hard-code `true` → 3 fails; (iii) drop the `ReturnRefundTerminal` read so `PayoutBlocked` is always false → 2 fails; (iv) implement `PayoutOutstanding` as `!ReturnRefundSettled` alone → 4 fails.

B. `internal/ui/pages/adminreturns_test.go`, package `pages`, no database (the `cart_test.go` shape) — `TestAnApprovedReturnWithMoneyOutstandingOffersToSendItAgain`. Render `AdminReturns` with `ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)` over three rows built by hand (`Window: "within"` so `WindowText` does not panic):
  - `Decided: false` → the HTML contains a decision form posting to `/admin/returns/<id>/decide` carrying `value="approved"` AND `value="rejected"`;
  - `Decided: true, PayoutOutstanding: true, PayoutBlocked: false` → the HTML contains a `method="post"` form with `action="/admin/returns/<id>/decide"` and a `name="decision"` control whose value is `approved`, and contains NO `value="rejected"` for that row (a retry is not a re-decision);
  - `Decided: true, PayoutOutstanding: false` → the row's id appears in no `action=`, which is what the live probe measured today.
 Add the stranded row and assert the explanatory text renders and no `/decide` action does.
 Mutation: delete the new `else if r.CanRetryPayout()` arm from the templ, run `templ generate`, and the second case must go RED before the arm is restored. The mutation must be SEEN to apply (diff the regenerated `_templ.go`), not assumed — false-green mode #3.

`TestEveryFormWorksWithScriptingOff`, `TestEveryKeyIsRendered`, `TestNoChromeStringIsHardCoded`, `TestEveryKeyIsTranslatedInEveryLocale` and `TestEveryViewModelFieldIsAssigned` all cover the new form, keys and fields for free; `make verify` then `make test-integration`.
