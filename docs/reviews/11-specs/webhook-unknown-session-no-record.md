# webhook-unknown-session-no-record

**Verdict** CONFIRMED · **Severity** medium · **Origin** external report (verified this round)

**Files** internal/payment/handler.go, internal/payment/store.go, internal/payment/stripe.go, internal/payment/query.sql, internal/payment/integration_test.go, internal/admin/query.sql, internal/admin/health.go, docs/reviews/10-codex-round9-dispositions.md


## Root cause

One switch in internal/payment/handler.go classifies two capture failures by whether goen can NAME the order, and then treats "cannot name it" as "nothing happened".

`ErrOrderCancelled` carries an order number, so the branch had somewhere to point and got a durable record. `ErrNotFound` carries no order number, so it was written as `return nil`. But the discriminator that actually matters is not whether goen knows the order — it is whether MONEY ARRIVED, and `CaptureFrom` has already answered yes (`PaymentStatus == paid`, `AmountTotal > 0`) before either branch is reachable.

An unattributable payment is strictly worse than a payment against a cancelled order: in the cancelled case goen at least knows whose money it is and what to refund. The branch with less information got less recording.

This is the shape CLAUDE.md names as the one no guard here can see — "two correct halves that disagree", mistakes #13/#30/#31 — with the added twist that the correct sentence is the comment sitting eight lines above the incorrect line (mistake #31, and #34's "the comment and the line under it are touching"). The comment at handler.go:225-230 states the rule in full; the branch immediately below it does not implement it.

The existing lock could not see it. `TestCaptureForAnUnknownSessionIsNotFound` (integration_test.go:347-358) asserts only that `Store.Capture` returns `ErrNotFound` — a fact about the store, not about what the handler does with the event. That is exactly the false-green shape round 9 already found and fixed for the neighbouring branch ("it proved the store could record the outcome, never that the switch in handler.go asks it to", docs/reviews/10-codex-round9-dispositions.md:130-136) — the lesson was applied to the cancelled branch and not to the one beside it.


## Reproduction — the evidence this rests on

EXECUTED end-to-end through the real signed-webhook path, not reasoned about.

I wrote a temporary integration test (internal/payment/zzprobe_integration_test.go, //go:build integration, now deleted) that is the exact mirror of the existing TestTheWebhookItselfFlagsMoneyForACancelledOrder (integration_test.go:1037) — same harness (enabledGateway, signed, typed, sessionEvent), same real Handler.Webhook, same real Stripe signature verification — but for a session id that has NO payment row:

    session := "cs_orphan_" + uuid.NewString()[:12]   // never OpenPayment'd
    body, header := signed(t, typed(sessionEvent(eventID, session, "paid", 88800),
        "checkout.session.completed"))
    h.Webhook(w, req)

`go test -tags=integration -run TestZZProbeUnknownSessionLeavesNothingToActOn -v ./internal/payment/` (testcontainers, postgres:18-alpine) output:

    zzprobe_integration_test.go:33: HTTP status = 200
    zzprobe_integration_test.go:42: event row: processed_at=2026-08-24 03:07:25.74691+00 unreconciled=NULL
    zzprobe_integration_test.go:52: /admin/health would list it: 0 row(s)
    zzprobe_integration_test.go:60: payments rows for the session: 0
    --- FAIL: TestZZProbeUnknownSessionLeavesNothingToActOn (0.01s)

So: a signature-verified `checkout.session.completed` with `payment_status=paid` and `amount_total=88800` is answered 200, marked processed, and leaves `unreconciled` NULL. The "/admin/health would list it" line runs the literal predicate from internal/admin/query.sql:851-853 (`WHERE unreconciled IS NOT NULL AND reconciled_at IS NULL`) and returns 0.

The asymmetry is real and is in one switch, internal/payment/handler.go:219-243:

    if errors.Is(captureErr, ErrOrderCancelled) {
        cancelledOrder = true
        number = n
        return st.Unreconciled(ctx, ev.ID,
            "money arrived for an order that was already cancelled")
    }
    if errors.Is(captureErr, ErrNotFound) {
        unknownSession = true
        return nil                      // <-- no durable record
    }

Both branches sit under a comment (handler.go:225-230) whose reasoning applies verbatim to the second: "A log line is not a record: nothing reads it, /admin/health cannot count it, and the shop finds out when the customer asks." The only output for the ErrNotFound case is handler.go:272-274, `h.log.WarnContext(... "capture for a session goen never opened" ...)`.

REACHABILITY — I checked the counter-hypothesis in the task and it is FALSE. goen does NOT write the payment row before calling Stripe. internal/payment/handler.go:116 calls `h.gateway.StartSession` (which creates the Checkout Session at Stripe) and only handler.go:124 calls `h.store.OpenPayment`. A session therefore exists at Stripe before any local row does. Being precise about what that window does and does not produce:
- If OpenPayment fails, the customer is NOT redirected (500 at handler.go:125-128), so that particular orphan session's URL is never disclosed and it is not payable. This window alone does not produce paid money.
- The genuinely money-carrying causes are: (a) a restore/point-in-time recovery that loses the payments row while Stripe is still retrying (Stripe retries for ~3 days) — the claim's own scenario; (b) a Payment Link or a Dashboard-created Checkout Session on the same Stripe account, which requires no failure at all; (c) two deployments (staging/production) sharing one Stripe account and webhook endpoint.

The branch is only reached for real money: CaptureFrom (internal/payment/stripe.go:205-227) requires `sess.PaymentStatus == paid` and `sess.AmountTotal > 0` before `isCapture` is true. So reaching `ErrNotFound` means a definitively PAID, positive-amount session on goen's own account that goen cannot attribute to any order.

I also checked the abandoned twin and it needs no change: `cancel_payment` (read from pg_proc on the live dev DB) is a bare `UPDATE payments SET status='cancelled' WHERE provider_ref = ... AND status <> 'succeeded'` — zero rows matched is harmless, and no money is involved there.

ONE CORRECTION to the claim's wording, so the reader is not misled: it is not true that only a transient log line survives. The `payment_webhook_events` row IS written durably, with the full raw Stripe payload (`payload` column) and `processed_at` set, and nothing sweeps that table (CLAUDE.md's retention sweeps cover outbox_messages, password_reset_tokens and checkout_attempts only). The evidence is permanent; what is missing is any way to SURFACE it. The stated consequence — "real money never appears in /admin/health reconciliation" — is exactly what I reproduced.

NOT a recorded decision. `grep -n "never opened|unattributable|unknown session|unreconciled" docs/roadmap.md CLAUDE.md docs/reviews/*.md` returns only the cancelled-order material (CLAUDE.md:1909-1913 and docs/reviews/10-codex-round9-dispositions.md:111-127, which introduced `unreconciled` and later `reconciled_at`). Nothing anywhere argues the unknown-session case should be a silent no-op.

Working tree: my probe file is deleted; `git status --porcelain | grep -i payment` returns nothing. (Four untracked zz_probe files remain under internal/account, internal/admin, internal/invoice and internal/ratelimit — those belong to sibling review agents, not to me, and I left them alone.)


## Blast radius

Who: the shop operator, and any customer whose money lands unattributed.

What: a paid, positive-amount Stripe Checkout Session that goen cannot map to an order is accepted with 200, marked processed, and made invisible to the one page goen built for exactly this — `/admin/health`'s unreconciled list (internal/admin/health.go:64, internal/admin/query.sql:849-855). The money is at Stripe. No refund row exists, no outbox topic carries it, no page counts it. The shop finds out when the customer asks, which is the failure the `unreconciled` column was introduced to end for the neighbouring branch.

Silent: yes, and doubly so. The event is marked processed, so Stripe stops retrying and the endpoint stays healthy — nothing degrades, no alarm fires. The only trace is a WARN log line plus a `payment_webhook_events` row that no query in the repository selects on. Recovering it requires an operator who already suspects the problem to write ad-hoc SQL against `payment_webhook_events`.

How often: not in normal operation — this is a gap in a safety net rather than a live leak on the mainline. It needs a restore that loses the payments row, a Stripe account shared across deployments, or a Payment Link / Dashboard session on the same account. That is why I judge this medium and not high: the money-carrying paths are abnormal conditions, and the raw payload is durably retained so forensics remain possible after the fact. But it is precisely the class of event goen already decided must leave a countable record, and the sibling branch that got one (a cancel racing a slow webhook) is arguably rarer than a Payment Link sale.

Scope: internal/payment/handler.go only. No schema change, no privilege change — the cancelled branch already performs the identical UPDATE as the `store` role, so the column grant is in place.


## Fix

File: internal/payment/handler.go, inside the `case isCapture:` apply closure (currently lines 219-243).

Change the ErrNotFound branch from returning nil to writing the durable record, mirroring the cancelled branch four lines above it:

    if errors.Is(captureErr, ErrNotFound) {
        // Money arrived on a session goen has no payment row for, so there is
        // no order to attribute it to and nothing to capture against. The claim
        // must still commit — retrying can never succeed — but the event is
        // marked UNRECONCILED in that same transaction, because a payment goen
        // cannot name is money somebody has to go and find at the provider.
        // object_ref already carries the session id, so /admin/health names it.
        unknownSession = true
        return st.Unreconciled(ctx, ev.ID,
            "a paid Checkout Session goen has no payment row for")
    }

It must NOT:
- return `captureErr` (or any non-nil error other than Unreconciled's own). That rolls the claim back, answers 500, and makes Stripe retry a capture that can never succeed — forever, until Stripe disables the endpoint. This is the trap ProcessWebhook's savepoint machinery (store.go:180-215) exists to avoid and the reason the branch returns nil today. The HTTP answer stays 200.
- create a payment row or guess an order. CLAUDE.md mistake #14: "Which order it happened to comes from the payment row goen wrote at session-open time, never from the event." There is no payment row, so there is no order, and inventing one is worse than recording the gap.
- touch the schema, the sqlc queries, or role grants. `Store.Unreconciled` (store.go:224-232) runs the existing `MarkWebhookUnreconciled` query (query.sql:29-31), and the cancelled branch already executes that exact statement as the `store` role, so the column grant round 9 narrowed to five columns already permits it. internal/db is sqlc-generated and needs no regeneration.

Second, smaller change in the same file, at the reporting switch (currently handler.go:272-274): raise the `case unknownSession:` log from `WarnContext` to `ErrorContext` and reword it to say a person must act, matching the `cancelledOrder` case at handler.go:268-271. The severity now denotes money needing a human, not a curiosity.

Nothing else changes. `/admin/health` picks the row up with no work: internal/admin/query.sql:849-855 already selects `coalesce(object_ref,'')`, and `ObjectRef` (stripe.go:269-274) returns `ev.Data.Object["id"]`, which for a checkout session event is the session id — so the operator gets the `cs_...` to look up in the Stripe Dashboard. `reconciled_at` (the off switch added in round 9) already acknowledges any unreconciled row, so this does not reintroduce the always-on alarm.

Consider, and reject in writing if rejected: whether a Payment Link or Dashboard sale on the same account should flag. It should — goen cannot distinguish "the shop took this deliberately elsewhere" from "money goen lost track of", and `reconciled_at` is the one-click acknowledgement for the benign case. Failing loud with a cheap dismissal is the right direction here.


## The lock, and how to see it fail first

Add to internal/payment/integration_test.go (the one integration file this feature is allowed, per project-structure.md), immediately beside TestTheWebhookItselfFlagsMoneyForACancelledOrder so the pair reads as one rule:

    // TestTheWebhookFlagsMoneyItCannotAttribute is the sibling of the test
    // above. CaptureFrom has already established the session is PAID for a
    // positive amount, so reaching ErrNotFound means money arrived that goen
    // can attribute to no order — strictly less recoverable than money for a
    // cancelled order, and it used to leave nothing countable at all.
    func TestTheWebhookFlagsMoneyItCannotAttribute(t *testing.T)

Drive the REAL handler, never a callback of your own — that is the false-green round 9 already caught here (docs/reviews/10-codex-round9-dispositions.md:130-136): a test that passes its own function to ProcessWebhook proves the store can record the outcome, not that the switch in handler.go asks it to.

    s := payment.NewStore(pool)
    h := payment.NewHandler(s, enabledGateway(t), alwaysPlacedHere{},
        slog.New(slog.DiscardHandler), false)
    session := "cs_unattributable_" + uuid.NewString()[:12]   // deliberately NO OpenPayment
    eventID := "evt_" + uuid.NewString()[:12]
    body, header := signed(t, typed(sessionEvent(eventID, session, "paid", 88800),
        "checkout.session.completed"))
    // POST to /webhooks/stripe with the Stripe-Signature header, httptest.NewRecorder.

Assert THREE things. All three are load-bearing and each catches a different wrong fix:

1. `w.Code == http.StatusOK`. Catches the naive fix that returns captureErr: that answers 500 and makes Stripe retry a capture that can never succeed until it disables the endpoint. Without this assertion the "fix" that breaks the endpoint passes.
2. `unreconciled IS NOT NULL` on the `payment_webhook_events` row for eventID. The finding itself.
3. `SELECT count(*) FROM payments WHERE provider_ref = $1` is 0. Catches a fix that invents a payment row to have something to point at, which would violate CLAUDE.md mistake #14.

Optionally assert the row is returned by the literal `/admin/health` predicate (`WHERE unreconciled IS NOT NULL AND reconciled_at IS NULL`) rather than only that the column is non-NULL, so the test is bound to what the operator actually sees.

PROVING IT RED BY MUTATION (required — rules/testing.md, "Locks Are Proven by Mutation"; CLAUDE.md mistake #6):

Mutation A, the exact defect. In internal/payment/handler.go revert the branch to:
        if errors.Is(captureErr, ErrNotFound) {
            unknownSession = true
            return nil
        }
Run `go test -tags=integration -run TestTheWebhookFlagsMoneyItCannotAttribute ./internal/payment/`. It MUST fail on assertion 2. I have already executed this exact state — it is today's committed code — and captured the output: status 200, `unreconciled=NULL`, `/admin/health would list it: 0 row(s)`. So this mutation is confirmed red before the fix exists, not assumed.

Mutation B, the over-correction. Change the branch to `return captureErr`. The test MUST fail on assertion 1 (status 500, not 200). This is the half a reviewer would otherwise leave unlocked, and it is the more expensive wrong fix.

SEE THE MUTATION APPLY, do not assume it (CLAUDE.md false-green mode #3): after each edit run `git diff internal/payment/handler.go` and confirm the branch body actually changed before trusting the red.

Do NOT weaken or delete TestCaptureForAnUnknownSessionIsNotFound (integration_test.go:347-358). It stays — it locks the store-level contract that Capture returns ErrNotFound rather than a generic error, which is what lets the handler branch on it at all. The new test locks what the handler does with that sentinel. They are two different claims.
