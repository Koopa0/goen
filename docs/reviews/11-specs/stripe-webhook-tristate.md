# stripe-webhook-tristate

**Verdict** CONFIRMED · **Severity** high · **Origin** external report (verified this round)

**Files** internal/payment/stripe.go, internal/payment/handler.go, internal/payment/payment_test.go, internal/payment/integration_test.go


## Root cause

A boolean is being asked to carry three states. `CaptureFrom`, `AbandonedSessionFrom` and `UnsettledSessionFrom` each answer "did I produce a value", which collapses "this event is not mine" (a normal, expected outcome — most Stripe traffic) into "this event IS mine and I could not read it" (an alarm). The handler's `switch` has an arm for every reader that succeeded and a `default:` for everything else, so the alarm lands in the arm built for the normal outcome.

The repository already holds the correct rule and applies it one branch away: `Store.Unreconciled`'s doc comment (store.go:218-223) says an event may not "be recorded as seen without also marking that it needs somebody — the rule ProcessWebhook already holds for the effect, applied to the outcome." That rule was applied to exactly one outcome (the cancelled order) rather than to the class, and "I could not read this" is an outcome that needs somebody.

Underneath that: the `default:` arm is defined by exclusion rather than by enumeration. Nothing anywhere says which event types goen has a branch for, so there is no set to subtract from and no way for the handler to notice that an event it wanted fell through.


## Reproduction — the evidence this rests on

EXECUTED, not reasoned — twice: a Go probe over `CaptureFrom` and a signed request to the live dev server.

1) Go probe (temporary `internal/payment/zzprobe_test.go`, since deleted). Every row below returns the SAME `(Capture{}, false)` from `CaptureFrom`, and false from both other readers:

```
good                         capture=true(...)  abandoned=false unsettled=false unmarshalErr=<nil>
amount_total_string          capture=false      abandoned=false unsettled=false unmarshalErr=json: cannot unmarshal string into Go struct field checkoutSession.amount_total of type int64
payment_status_object        capture=false      abandoned=false unsettled=false unmarshalErr=json: cannot unmarshal object into ... stripe.CheckoutSessionPaymentStatus
id_object                    capture=false      abandoned=false unsettled=false unmarshalErr=json: cannot unmarshal object into ... checkoutSession.id
amount_total_renamed         capture=false      abandoned=false unsettled=false unmarshalErr=<nil>
payment_status_renamed_value capture=false      abandoned=false unsettled=false unmarshalErr=<nil>
truncated                    capture=false      abandoned=false unsettled=false unmarshalErr=unexpected end of JSON input
unrelated_type               capture=false      abandoned=false unsettled=false unmarshalErr=<nil>
```

`unrelated_type` (customer.created) and `truncated` (a `checkout.session.completed` goen cannot decode) are byte-for-byte the same answer. Note `payment_status_renamed_value` — a `completed` session whose `payment_status` is neither `paid` nor `unpaid` — decodes cleanly, is not a capture, and is not caught by `UnsettledSessionFrom` either (internal/payment/stripe.go:262 tests `!= ...StatusUnpaid`), so it does not even reach the ERROR log added for that case.

2) End to end against the running dev server, with a real HMAC over `t.payload` using `$GOEN_STRIPE_WEBHOOK_SECRET`:

```
# checkout.session.completed, payment_status:"succeeded"
HTTP=200
event_id     | evt_probe_drift_1
type         | checkout.session.completed
object_ref   | cs_test_probe_drift
processed_at | 2026-08-24 03:06:32.439668+00
unreconciled |            <-- NULL
reconciled_at|            <-- NULL

# checkout.session.completed, payment_status:"paid", amount_total:"67000" (a hard decode failure)
HTTP=200
event_id     | evt_probe_drift_2
processed_at | 2026-08-24 03:06:54.012254+00
unreconciled |            <-- NULL
```

Both rows deleted afterwards; `internal/payment/zzprobe_test.go` deleted.

The code path, quoted:

internal/payment/stripe.go:193-207 — four distinct failures, one return value:
```go
func CaptureFrom(ev *stripe.Event) (Capture, bool) {
	if !captureEvents[ev.Type] {
		return Capture{}, false          // not our event
	}
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(ev.Data.Raw, &sess); err != nil {
		return Capture{}, false          // OUR event, unreadable — err discarded
	}
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return Capture{}, false          // OUR event, unrecognised status
	}
	if sess.ID == "" || sess.AmountTotal <= 0 {
		return Capture{}, false          // OUR event, anomalous shape
	}
```

internal/payment/handler.go:210-238 — `apply` stays nil for all four, so `ProcessWebhook` records the claim and `MarkWebhookProcessed` stamps `processed_at` (internal/payment/store.go:173-180) with nothing else written. handler.go:277-279 then logs it in the `default:` arm as `"stripe webhook recorded"` at INFO, and handler.go:281 answers `w.WriteHeader(http.StatusOK)`.

The durable-record mechanism the repo already built for exactly this shape is one branch away and unused here — handler.go:221-231 calls `st.Unreconciled(ctx, ev.ID, "money arrived for an order that was already cancelled")`, and `/admin/health` reads it at internal/admin/query.sql:851-853 (`WHERE unreconciled IS NOT NULL AND reconciled_at IS NULL`).

Version-skew reachability is real, not hypothetical: stripe.go:179-181 disables the one guard the SDK offers —
```go
// The API-version check is off on purpose: Stripe upgrades an account's
// version independently of this binary. Nothing else is relaxed.
ev, err := webhook.ConstructEventWithOptions(body, sigHeader, g.webhookSecret,
	webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
```
stripe-go v86.2.0 pins `APIVersion = "2026-07-29.dahlia"` (api_version.go:10); nothing in this repo pins the webhook endpoint's version, which lives in the Stripe Dashboard. My `evt_probe_drift_1` carried `"api_version":"2099-01-01.acacia"` and was accepted and ACKed.

NOT a recorded decision. CLAUDE.md:1925-1930 records the repo closing ONE instance of this exact class and does not claim the rest is closed: "`UnsettledSessionFrom` is the alarm rather than the cure. A completed-but-unpaid session used to fall to the webhook handler's `default` branch and be logged as one more event goen does not act on, indistinguishable from the dozen it genuinely does not." That fix covers `payment_status == "unpaid"` only. Nothing in CLAUDE.md or docs/roadmap.md covers an unparseable or otherwise unrecognised payload.


## Blast radius

Every `checkout.session.completed` / `async_payment_succeeded` / `checkout.session.expired` / `async_payment_failed` that this binary cannot read, from the moment the payload shape stops matching stripe-go v86 — an account API-version upgrade in the Stripe Dashboard, a per-endpoint version set there, or any new `payment_status` value. Because `IgnoreAPIVersionMismatch: true` is set, goen accepts and ACKs such an event rather than rejecting it.

When it fires it fires for ALL captures at once, not one customer: money is at Stripe, the order sits `pending`, the stock hold lapses 30 minutes later and the units are re-sold, and the customer reads 尚未付款 with a 前往付款 button. Silent by construction — the only trace is an INFO line reading `"stripe webhook recorded"`, identical to the line every ignored `customer.created` produces.

Two refinements to the stated consequence, both narrowing it:

- **"Stripe will not retry" is true but is the weaker half of the harm.** A retry redelivers the same bytes, so retrying could never have succeeded. The real cost is the missing alarm: `unreconciled` is NULL, so `/admin/health` counts zero and names nothing, and the shop finds out when a customer asks. That is verbatim the harm the schema comment on `payment_webhook_events.unreconciled` (migrations/001, "the only trace used to be a log line ... nothing for /admin/health to name") was written to prevent.
- **No current deployment is losing money.** The trigger is a configuration/version event, not anything a customer can do. This is a latent hole in the one path that takes money, not an active loss — which is why I rate it high rather than critical.

One adjacent instance of the same silence, one line away and inside the stated blast radius: handler.go:233-236, `errors.Is(captureErr, ErrNotFound)` sets `unknownSession = true` and `return nil`. A capture for a session goen has no payment row for is money at Stripe with no order, ACKed 200, WARN-logged, `unreconciled` NULL.


## Fix

Make the handler able to tell "not mine" from "mine and unreadable", and route the second to the durable record that already exists. No schema change, no sqlc regeneration, no new dependency — `MarkWebhookUnreconciled` and `/admin/health`'s query are already in place.

**1. internal/payment/stripe.go** — beside `captureEvents` (line 187) and `abandonedEvents` (line 211), add the enumeration the `default:` arm currently lacks:

```go
// Actionable reports whether goen has a branch for this event type. A type in
// here that yields nothing from every reader is a payload this binary could not
// read — not an event goen does not act on, and the two must not share an arm.
func Actionable(ev *stripe.Event) bool {
	return captureEvents[ev.Type] || abandonedEvents[ev.Type]
}
```

Do NOT change `CaptureFrom`'s signature to return an error. Three call sites and two existing tests read the boolean, and the handler only needs to know that SOMETHING it wanted did not arrive; which of the four checks refused is a detail for the reason string, and the payload is in `payment_webhook_events.payload` for anyone who needs it.

**2. internal/payment/handler.go**, in `Webhook` after line 212 (`unsettledSession, isUnsettled := UnsettledSessionFrom(&ev)`), add:

```go
unreadable := Actionable(&ev) && !isCapture && !isAbandoned && !isUnsettled
```

`isUnsettled` is in the expression on purpose: a `completed` session at `payment_status: "unpaid"` is a state goen understands and already alarms on, and must not be double-reported.

**3.** Add a first arm to the `switch` at handler.go:216, ahead of `case isAbandoned:`:

```go
case unreadable:
	apply = func(ctx context.Context, st *Store) error {
		return st.Unreconciled(ctx, ev.ID,
			"goen could not read a "+string(ev.Type)+
				" it acts on: the payload is not the shape this binary expects")
	}
```

The reason text must be non-blank — `payment_webhook_events_unreconciled_present` refuses whitespace — and `string(ev.Type)` never is. `MarkWebhookUnreconciled` sets `processed_at` and `unreconciled` together, and `MarkWebhookProcessed` (query.sql:22-24) touches only `processed_at`, so the later stamp inside `ProcessWebhook` does not clear the flag; this is the same ordering the cancelled-order path already relies on.

**4.** Add a matching arm to the logging `switch`, immediately before `default:` at handler.go:277:

```go
case unreadable:
	h.log.ErrorContext(r.Context(), "a stripe event goen acts on could not be read — check the endpoint's API version",
		"event", ev.ID, "type", ev.Type, "object", ObjectRef(&ev), "age", EventAge(&ev))
```

**5. Keep the 200.** Do not change the status to 500 to force a retry: a retry redelivers the same bytes, which is the forever-loop CLAUDE.md records the SAVEPOINT being added to stop ("Stripe retried a capture that can never succeed"). The durable row is the fix; the status code is not. Say so in a comment on the `case unreadable:` arm so the next reader does not "improve" it into a retry storm.

**6.** Close the adjacent instance while here: at handler.go:233, replace

```go
if errors.Is(captureErr, ErrNotFound) {
	unknownSession = true
	return nil
}
```
with a body that also calls `st.Unreconciled(ctx, ev.ID, "a capture arrived for a checkout session goen never opened")` before returning nil, keeping `unknownSession = true` for the log arm. Money at Stripe with no order to attribute it to is precisely what `unreconciled` is for.

Constraints this must respect: `internal/db` is sqlc output and is not touched (no query changes are needed); `Actionable` reads the two unexported maps and so must live in `internal/payment`; `/admin/health` needs no change because its predicate is already `unreconciled IS NOT NULL AND reconciled_at IS NULL`.


## The lock, and how to see it fail first

Two tests. The unit one states the contract; only the integration one can see the row, so only it is the lock.

**Unit — `internal/payment/payment_test.go` (package `payment_test`), `TestAKnownEventGoenCannotReadIsNotAnEventItIgnores`.** Build each event through `signed()` + `g.VerifyWebhook` like the existing tests do, then assert the tri-state `payment.Actionable(&ev) && !isCapture && !isAbandoned && !isUnsettled`:

- unreadable=true: `checkout.session.completed` with `"amount_total":"67000"` (string); with `"payment_status":{"v":"paid"}`; with a truncated body; with `"payment_status":"no_payment_required"`; `checkout.session.async_payment_succeeded` whose `payment_status` is not `paid`; `checkout.session.expired` with an unparseable body.
- unreadable=false: the three good shapes (`paid` completed, `paid` async, a good `expired`), the unpaid completed session (it belongs to `isUnsettled`), and `customer.created`.

Also EXTEND, do not merely add to, `TestOnlyAPaidSessionIsACapture` (payment_test.go:173-209). Its `{"still processing", sessionEvent("e3", "cs_3", "no_payment_required", 199900), false}` row was written from the implementation and asserts only "not a capture"; under the fix that same event is additionally unreadable, so the row must assert both. A test that keeps passing unchanged here is the CLAUDE.md pattern of a test asserting the defect.

**Integration — `internal/payment/integration_test.go`, `TestAnUnreadableKnownEventIsRecordedForAPerson`.** Sign a `checkout.session.completed` whose `amount_total` is the string `"67000"`, POST it through the real handler (`httptest`), then assert ALL of:

1. response is 200;
2. `SELECT unreconciled FROM payment_webhook_events WHERE provider='stripe' AND event_id=$1` is NON-NULL;
3. the row is visible through `/admin/health`'s own predicate (`unreconciled IS NOT NULL AND reconciled_at IS NULL`), so the alarm is reachable and not just the column set;
4. the order is still `pending` and no payment was captured.

Assertion 2 is the whole lock and 1 is deliberately kept beside it: the status code is IDENTICAL before and after the fix, so a test asserting only the 200 would pass through the entire defect — CLAUDE.md #34, "an assertion coarser than the error it is meant to catch is not a weak lock, it is no lock."

**Mutations, each proven to apply before it is believed (#6, and false-green mode #3 — see the edit, do not assume it):**

- **M1** — in handler.go delete the `case unreadable:` arm from the `switch` that assigns `apply`. Expected: unit test unaffected, integration test RED on assertion 2 (`unreconciled` NULL) while assertions 1 and 4 still pass. This is what proves the lock is watching the record and not the status.
- **M2** — in stripe.go change `Actionable` to `return captureEvents[ev.Type]`. Expected: unit test RED on the unparseable `checkout.session.expired` row.
- **M3** — in handler.go drop `&& !isUnsettled` from the `unreadable` expression. Expected: unit test RED on the unpaid-completed row (it must stay false), proving the fix does not double-report the case CLAUDE.md:1925 already closed.
- **M4** (for fix item 6) — revert the `ErrNotFound` arm to `return nil` with no `Unreconciled` call. Expected: a companion integration case (a capture for a session with no payment row) RED on `unreconciled` being NULL.

Run `make verify` and `make test-integration`. The working tree must be clean afterwards.
