# Round 6 — third-party acceptance (Codex): dispositions

The review filed **5 Critical, 10 High, 7 Medium**. Its own verification transcript
(`go test -race`, integration, vet, lint, `govulncheck`, 85 layout viewports) was all
green — which is the finding underneath the findings: **every gate in this repository
asks about ABSENCE**, and none of them can see two correct halves that disagree.

`.claude/rules/review-process.md` says every finding reaches exactly one of three
states before merge. This file is that record. Each verdict below was re-derived from
the code rather than taken from the report.

## Scoreboard

| # | Sev | Finding | State |
|---|---|---|---|
| C1 | Critical | Guest order cookie is forgeable | **Fixed** (follow-ups closed in round 7) |
| C2 | Critical | Store credit spent, Stripe charged gross | **Fixed** (both halves) |
| C3 | Critical | Password alone replaces a confirmed TOTP | **Fixed** |
| C4 | Critical | Staff can promote themselves to admin | **Fixed** |
| C5 | Critical | Many Stripe sessions per order → double charge | **Fixed** |
| H6 | High | Payment unbound to order state / live hold | **Fixed** (1 window bounded, §H6.3) |
| H7 | High | `async_payment_succeeded` unhandled | **Fixed** |
| H8 | High | Refund reports success it does not have | **Fixed** |
| H9 | High | Concurrent double-click places two orders | **Fixed** |
| H10 | High | Empty TOTP key silently disables MFA | **Fixed** |
| H11 | High | `maintenance` pool has no DSN of its own | **Fixed** |
| H12 | High | Envelope sender, token logging, socket deadline | **Fixed** |
| H13 | High | Image rendition is a CPU DoS | **Fixed** (in-process; persistent cache queued) |
| H14 | High | Rate limiter collapses behind a proxy | **Fixed** (`GOEN_TRUSTED_PROXIES`) |
| H15 | High | Process does not exit when listen fails | **Fixed** |
| M1 | Med | `/healthz` reaches the database | **Fixed** |
| M2 | Med | Outbox lease shorter than a serial batch | Split: half queued, half **refused** |
| M3 | Med | Storefront role's blast radius | **Refused in writing** |
| M4 | Med | Contact form has no rate limit | **Fixed** |
| M5 | Med | `GOEN_BASE_URL` defaults against its own doc | **Fixed** |
| M6 | Med | `MaxConns` write lands on a copy | **Fixed** |
| M7 | Med | README and `.env.example` drift | **Fixed** |

Two defects were found **during** this verification that Codex did not file, and both
are Critical-adjacent. They are §NEW-1 and §NEW-2.

## Fixed in this branch

### C1 — the forgeable guest-order cookie

The cookie carried the ORDER NUMBER and the number was the proof. Numbers come off a
per-day counter, so the cookie was mintable with `curl` and walkable by increment:
a stranger's email, address and items, and then cancel, pay, or open a return.

`__Host-`, `Secure`, `HttpOnly` and `SameSite` all govern how a BROWSER treats a
cookie. **None of them says the value came from this server.**

It carries high-entropy tokens now; `order_access_grants` holds only the sha256, the
comparison happens in the database and returns a bare boolean, and an error is not
access. All six entry points route through one `PlacedHere` — the cart's order page,
reorder and cancel, plus payment and returns through consumer-defined interfaces.
Both grant paths are covered (`PlaceOrder` and `/orders/find`).

**Its two tests were false-green when written, and that is worth recording.** They
passed with `g.digest = ANY(...)` replaced by `OR true`, because the attacked order
had no grant row at all — `EXISTS` was false whatever the digest said, so they
refused the forgery for the wrong reason: not "your token is not for this order" but
"nobody has a token for this order". Giving the victim a real grant first is what
made the digest comparison the only thing that can refuse. Under the same mutation
they now report `status 200` and `leaked the customer's email`.

### C2 — store credit spent, Stripe charged the gross total

The payment half was fixed in `0a35e63`: `order_amount_owed(o.id)` is the one
definition, read by the funding check, the capture guard and the payment page, and a
fully funded order never reaches Stripe.

**The checkout half was still open**, and it is the worse one. `spendCredit` capped at
`subtotal + shipping` with the DISCOUNT omitted, so a coupon and a credit balance on
one order spent more credit than the order was worth — `order_amount_owed` went
NEGATIVE. Nothing underneath refuses that: `store_credit_never_negative` guards the
ACCOUNT, not the order, and no rule caps a spend at what its order owes.

So the customer lost the discount **and the order was bricked**: `FullyFunded()` keeps
a non-positive figure away from Stripe, while `orders_funded_to_leave_pending` asks
`owed <> 0`, which a negative satisfies. Unpaid, unshippable, forever.

The right figure was already three lines above the call as `orderParts.totalCents` —
the one the confirmation email quotes. Two expressions for one fact, and the one that
was wrong was the one that moved money. **That is mistake #13 a second time**, in the
same feature, four commits after it was written down.

### C3 — a password alone replaced a confirmed second factor

The enrolment route needs only an ordinary signed-in session, and the upsert
overwrote any existing secret. A stolen password was therefore the whole back office:
sign in, enrol your own authenticator over the real one, confirm — and confirming
marks the session step-up verified.

`Store.Remove` names this exact threat as the reason nobody may drop their OWN
factor. `Begin` let one skip the removal entirely.

The refusal is `WHERE staff_totp_credentials.confirmed_at IS NULL` on the `DO UPDATE`,
in the statement rather than in Go because a read-then-write is a race two concurrent
enrolments both win. An unconfirmed enrolment is still restartable — nothing has been
proved, so there is nothing to protect. Recovery is what it always claimed to be:
another admin removes the credential.

**The existing test asserted the defect.** `TestRestartingEnrolmentInvalidatesTheOldSecret`
called `Begin` straight over a confirmed credential and asserted the overwrite was
correct, so it could never have failed on this. It goes through `Remove` now — the
recovery path it always claimed to be testing.

## Findings, in detail

Everything below was written when these were still open. The mechanism descriptions
stand; the state is the scoreboard above, and each was fixed in this branch with a
recorded mutation. Round 7's section at the end covers what came after.

### C4 — a staff member can promote themselves to admin

`IsStaff()` accepts `staff` OR `admin` and is the **only** role predicate in the tree;
`IsAdmin`/`RequireAdmin` do not exist. All four `/admin/staff` routes are gated on it.
`AddStaff` reads the role off the form and `UpsertStaff` does
`ON CONFLICT DO UPDATE SET role = EXCLUDED.role`.

Escalation does not even need self-promotion: POST a new attacker-controlled email
with `role=admin`, then `/forgot` — the flow the query's own comment documents as
intended. A plain staff member can also demote any admin (while ≥2 remain) and strip
any admin's second factor.

CSRF protection does **not** mitigate this: the attacker is a legitimately signed-in
staff member.

`TestANewColleagueHasNoPassword` locks the bug in by asserting `AddStaff(…, "admin")`
promotes an existing account.

Fix is both halves or neither: `RequireAdmin` on the four routes, **and** the actor
passed into `AddStaff` so nobody changes their own role. `RequireAdmin` alone still
leaves `staff` meaning nothing.

### C5 — one order, many Stripe sessions, two charges

`StartSession` sets no `Idempotency-Key`; the only idempotency in the package is
webhook dedupe. `open_payment` dedupes on `(order_id, provider_ref)`, so every new
session id inserts a new `requires_payment` row, and
`payments_one_capture_per_order` is partial on `status = 'succeeded'` — the second
capture is refused **after** the money is at Stripe. `ProcessWebhook` 500s, Stripe
retries forever, nothing refunds.

Fix: reuse a live `requires_payment` row whose intended amount still equals what is
owed; create only when there is none. `IdempotencyKey` on order number + owed amount
is the second line, not the first. Test asserts `count(payments) = 1`, not the
capture — the capture is already guarded.

### H6 — payment bound to neither the order's state nor a live hold

Three separate holes:

1. **The session outlives the hold and the comment says the opposite.**
   `SessionTTL` and `HoldTTL` are both 30 minutes, but the hold starts at
   `PlaceOrder` and the session at session creation — so the session is strictly
   longer by however long the customer sat on the pay page. The code claims
   "deliberately shorter than `cart.HoldTTL`". Derive the expiry from the
   reservation's own `expires_at`.
2. **Capture never checks the order is still pending.**
   `payments_require_complete_order` checks lines, delivery details and a
   non-negative total — never `fulfillment_status`. Cancel in another tab, then
   capture: order cancelled, stock released, payment succeeded. A credit-funded
   order is caught incidentally; a plain card order is caught by nothing.
3. **Cancelling never cancels the session.** `cancel_payment` is reached only from
   `checkout.session.expired`.

### H7 — the asynchronous success event has no handler

`CaptureFrom` accepts only `checkout.session.completed` with `payment_status = paid`.
Dynamic payment methods are deliberately enabled, and for a delayed method the
`completed` event carries `payment_status = unpaid` — which
`TestOnlyAPaidSessionIsACapture` **asserts is rejected, naming "an asynchronous
payment method still processing" as the reason.** The code documents the case and has
no handler for the success that follows.

`async_payment_succeeded` appears nowhere in the repository, and
`docs/stripe-setup.md` tells the operator to subscribe to `completed` only.

**The trap when fixing:** the instinct is to add the event to
`TestOtherEventTypesAreNotCaptures`, which would lock the bug in. It belongs in
`TestOnlyAPaidSessionIsACapture` expecting `true`.

### H8 — the refund reports success it does not have, and cannot be retried

`StripeRefunder` discards Stripe's status and returns only the id, so a `pending` or
`requires_action` refund is written `succeeded` with `succeeded_at` stamped. That is
**the inverse of mistake #16**: goen asserts money moved that has not.
`requires_action` appears in no Go file, though the schema allows it.

Every API error — including an ambiguous timeout where Stripe may well have
refunded — is written as a terminal `failed`. And `RefundedSoFar` counts `pending` as
already refunded, so the retry is refused before Stripe is ever called.

**CLAUDE.md's claim that "a crash between the two leaves something reconciliation can
find" is false in both halves**: nothing reads `refunds` except that arithmetic — no
page, no `/admin/health` row — and the row actively BLOCKS recovery rather than
enabling it. `TestEveryTableIsRead` passes it because a `SELECT` counts; the guard
cannot see that the SELECT is an arithmetic input rather than a door. Same shape as
`product_specs` and `promo_banners`.

`settle_refund`'s grant to `store` has no caller.

`TestAFailedRefundLeavesARowToReconcile` asserts the unrecoverable state is fine.

Fix has three parts and a reader: propagate the status; treat an ambiguous transport
error as `pending` rather than terminal (mistake #16's rule applied to status instead
of last4); resume from the existing row via `open_refund`'s `request_key`; and put
outstanding refunds on `/admin/health`, without which the row is still nothing anybody
can find.

### H9 — a concurrent double-click places two orders

`priorOrder` reads on the POOL, before `Begin`. `RecordCheckoutAttempt` is the
second-to-last statement inside `finishOrder`, and its `ON CONFLICT DO NOTHING` is
`:exec`, so the row count is discarded and the loser commits its own order.

Nothing else serializes the pair: the stock-hold key and the outbox dedupe key are
both derived from the new order's own identity, so two holds and two emails. With
stock for two, both commit — and **both spend store credit**, because the spend's
idempotency key is `"order:"+orderID`.

`checkout_attempts.idempotency_key` **is** the primary key. The serialization point
already exists and is used at the wrong end of the transaction.

Fix: `pg_advisory_xact_lock(hashtextextended(@key, 0))` as the first statement after
`Begin`, then `priorOrder` on the tx. Per mistake #9 the test must hold T1's
transaction open while T2 runs — two goroutines and a start channel finish
microseconds apart and never overlap.

### H10 — an empty TOTP key silently disables the back office's MFA

Empty key → `stepUp` nil → `RequireStaff` skips MFA for the whole back office. The
operator warning is **dead code**: it is assigned for the missing key and then
unconditionally overwritten by `staffNotice(r)` with no `else`, which on a plain visit
is the empty string.

Stripe fails closed on a half-configuration and the server refuses to start.
`GOEN_TOTP_KEY` is validated and logged nowhere, despite the wiring claiming "the same
shape as Stripe: the feature is off, loudly". It is off silently.

**The MFA gate is exercised by no test at all** — the back-office test passes `nil`
stepUp.

### NEW-1 — the last admin can erase themselves and lock the shop out

Not in Codex's report. `POST /account/erase` verifies only that the user typed their
own email and calls `erase_user`; `guardLastAdmin` is never consulted. The last admin
erases themselves and nobody can ever reach `/admin` again — the exact outcome
`ErrLastAdmin` exists to prevent, through a door that never asks.

The fix belongs in `erase_user` itself, since CLAUDE.md calls it the only door.

### NEW-2 — `order_access_grants` has no retention, no erasure and no revoke

Follow-ups on C1's own fix, found by asking this repository's standard questions of
the new table:

1. **`reporting` can read the credential digests.** The `REVOKE SELECT … FROM
   reporting` list names `sessions`, `password_reset_tokens`,
   `staff_totp_credentials`, `user_identities`, `order_private_data` and
   `payment_webhook_events` — its own comment describes exactly this table. Worse:
   **no test asserts that list at all**, so a new credential table silently joins the
   readable set. It should be table-driven off the named tables, so the next one is
   covered by the row it adds.
2. **`erase_user` does not reach it.** An erased customer's grants survive as live
   credentials. The same argument the function already makes for
   `newsletter_confirmations`: a link sitting in a mailbox is a cookie sitting in a
   browser.
3. **No sweep and no expiry.** Every other credential here expires — sessions 14d,
   reset tokens 1h with a 7d sweep, outbox 30d, `checkout_attempts` 30d. These rows
   are permanent while the cookie that uses them has a 30-day `MaxAge`, so they
   outlive their only consumer by construction. This is the lesson CLAUDE.md already
   records under "three tables grew without bound and one of them held the mailed
   tokens". A `created_at`-keyed delete on the existing daily worker also gives
   `created_at` its first reader.
4. **`store` holds UPDATE/DELETE it never uses** — only INSERT and SELECT are needed.
5. **Neither the payment nor the returns half is locked.** Deleting the access check
   from either handler turns nothing red; neither package's tests construct a
   `Handler`.
6. *Minor:* `GrantOrderAccess` is `:exec`, so a grant for a non-existent order
   silently writes nothing — while the comment claims the write-before-cookie
   ordering protects against exactly that state. `:execrows` + `ErrNotFound` would
   make the claim true.

**CLAUDE.md carries no record of this design at all**, which for this project is part
of the fix rather than a docs chore: the acceptance prompt tells a reviewer CLAUDE.md
IS the design record.

### ops — H11, H12, H13, H14, H15

- **H11:** the maintenance pool is built from the storefront DSN. The admin pool has
  `GOEN_ADMIN_DATABASE_URL`; maintenance has no equivalent, so `SET ROLE maintenance`
  fails, `pgxpool` connects lazily so startup survives, and the recommendation worker
  logs permission denied at boot and every 15 minutes after.
- **H12:** three, all confirmed. The default `From` is a display-name string passed
  straight to `smtp.Client.Mail`, which needs a bare addr-spec — and `email.Valid`
  already encodes that rule and is applied to `To` only. `LogSender` logs the whole
  body and returns success, so reset, verification and unsubscribe tokens land in the
  log while the outbox marks them delivered. `SendTimeout` covers only `DialContext`;
  greeting, STARTTLS, AUTH and DATA have no socket deadline.
- **H13:** every non-304 rendition request decodes, CatmullRom-resizes and re-encodes
  a source bounded only at 40 megapixels, with no server-side cache, singleflight or
  concurrency limit. The width allowlist bounds the OUTPUT, not the decode. The
  comment claiming "the result is cached for a year" describes the CLIENT cache.
- **H14:** `ClientIP` reads `RemoteAddr` alone. CLAUDE.md calls this the honest
  failure mode needing a trusted-proxy configuration — that configuration does not
  exist, in any form, anywhere. Only prose.
- **H15:** `defer stop()` is registered before `defer background.Wait()`, so LIFO runs
  `Wait()` first with the context uncancelled. **Codex overstates the severity**: the
  signal handler is still installed, so a later SIGTERM does drain it. The real damage
  is that a port-in-use failure hangs instead of exiting, so the crash-loop signal an
  orchestrator depends on is lost.

### medium — M1, M2 (half), M4, M5, M6, M7

- **M1** is PARTIAL as filed: `withBanner` already excludes `/media` and `/static`, so
  that half of the claim is wrong. But `withTopNav` skips only non-GET and `/admin`,
  so `/healthz`, `/readyz`, media and static all run `store.Nav()`, and
  `bannerFreePrefixes` omits the two probes. Both middlewares degrade gracefully, so
  the cost is latency rather than a failed probe.
- **M2**, first half only: 50 messages claimed under one 5-minute lease and delivered
  serially at a 30-second timeout, so ten slow ones expose the rest to a second
  replica. Derive the lease from `BatchSize × SendTimeout`.
- **M4:** `POST /contact` is bare while every other public write path is guarded.
- **M5:** `BaseURL` defaults to `http://` + the listen address, and the field's own
  doc says it has no default because guessing it "silently break[s] the moment
  anything is deployed". A container gets `http://0.0.0.0:9700` into Stripe's
  `success_url`, every mail link and the sitemap.
- **M6:** `pool.Config()` returns a COPY, so the `MaxConns = 2` write lands on a
  discarded object and the reasoning in the comment beside it was never in force.
- **M7:** README still lists home, cart, checkout, account and admin as unbuilt;
  `.env.example` names `goen_app`/`goen_web`, which no longer exist, and omits
  `GOEN_BASE_URL`, `GOEN_ADMIN_DATABASE_URL`, and every Stripe, SMTP and TOTP setting.

## Refused in writing

**M3 — the storefront role's blast radius.** `store` receives DML on all tables and
has it revoked on the privileged ones. The direction is deliberate and the header
comment says so; more importantly the revoke list is DERIVED from the catalog by
`TestEveryDefinerWrittenTableIsRevoked` rather than hand-maintained, so the gap is
bounded by a test rather than by vigilance. Inverting to explicit per-table grants is
a large change with no defect behind it. The point stands as blast radius and is
recorded here rather than actioned.

**M2, second half — the outbox retries past `MaxAttempts` forever.** Filed as a
defect; it is a decision, documented at the code and in CLAUDE.md: a message that
exhausted its attempts is kept and surfaced by `Stuck()` rather than deleted or marked
failed, because a queue that forgets what it could not deliver reports itself empty.

## What this round says about the gates

Every automated check in this repository was green throughout, and three of the five
Criticals were live. The guards ask *does anything write this column*, *does anything
read this table*, *does any handler fill this field* — questions about **absence**.
None of them can see:

- two correct halves that disagree (C2, both times);
- a route whose authorisation is weaker than the thing it protects (C3, C4);
- a state machine that reports an outcome it has not reached (H8);
- a test that asserts the defect (C3, C4, H8 — three of them, in three features).

The last is the one to take seriously. `TestRestartingEnrolmentInvalidatesTheOldSecret`,
`TestANewColleagueHasNoPassword` and `TestAFailedRefundLeavesARowToReconcile` each
encode the bug as the expected result, so each would have gone RED on the fix. A test
written from the implementation asserts what the code does; only a test written from
the CONTRACT can disagree with it.

And the two order-access tests written in *this* branch were false-green on their
first run. The mutation discipline caught them. It is the only thing that could have.

---

# Round 7 — second third-party acceptance

A second reviewer probed the running database with `SET ROLE` and mutated two of
the mechanised guards. It filed **2 High, 6 Medium, 1 Low-Medium**. Every one is
now Fixed except two Refused in writing, and the round produced three findings
the reviewer did not file.

Its own closing recommendation was not a code change: *add the write-direction
privilege guard*. That is the largest thing in this batch, and it is what turned
F1, F2 and F3 from a stranger's probe script into a build failure.

## Fixed

**F1 — `store` could rewrite the authentication surface.** Proven by the
reviewer, three statements from a storefront request to a back-office account:
`UPDATE users SET role='admin'`, then `DELETE FROM staff_totp_credentials`, then
sign in. It could also INSERT a session, publish a shipping version at
`fee_cents = 0`, and delete the catalogue.

The root cause was WIRING, not the privilege model. `twofactor.NewStore(pool, …)`
put the entire second-factor feature on the STOREFRONT pool, so `store` needed
write on `staff_totp_credentials` to enrol and on `users.role` to manage
colleagues. It runs on the admin pool now — every route it serves is a
back-office route — and `store` gives both up. `users` and `sessions` are column
grants; the merchandising tables are revoked outright.

**The column guard found one the reviewer did not:** `store` held table-level
INSERT on `sessions`, which includes `totp_verified_at` — so a sign-in could
create a session BORN step-up verified, clearing the back office's gate with no
code ever entered.

**F2 — `admin` could take over any customer account** with `UPDATE users SET
password_hash` plus `INSERT INTO sessions`, leaving no audit row because `admin`
holds no INSERT on `audit_events`. Both revoked; the back office keeps
`(role, full_name)` on users, and on sessions only `UPDATE (totp_verified_at)`
and DELETE. The argument is the one `email_verifications` was already given three
lines away, and it applies with more force here.

**F3 — the revokes were bypassable through auto-updatable views.** A write
through a simple view is permission-checked against the base table AS THE VIEW'S
OWNER, so `DELETE FROM committed_orders` as `admin` was accepted while `admin`
holds no DELETE on `orders`. Caused by ORDERING — the sweeping grant to `store`
runs before the views exist, the one to `admin` after — which is trap #21 from a
third side. Now revoked from all three roles in the final `DO` block, derived
from `pg_views` so a view added later is covered by existing.

**F4 and F5 — two guards satisfied by a NAMESAKE.**
`TestEveryColumnIsReadOrWritten` matched a column's bare name anywhere in the
whole-repo SQL corpus, so a dead `order_access_grants.note` passed on the
strength of `order_events.note`; 31 of 511 columns passed that way, and all three
defects the guard exists to catch were caught for the same accidental reason.
`TestEveryViewModelFieldIsAssigned` had the identical flaw on field names. Both
are scoped now — to the column's own table, and to `Type.Field` — and both
mutations are recorded green→red→revert.

Tightening them surfaced two real schema defects: **`coupons` had no
`set_updated_at` trigger at all** (departure #3 reopened by a table that never
got one — and switching a coupon off is exactly the change it failed to record),
and **`orders.created_at` was a second stamp beside `placed_at`**, which
everything actually reads. Trigger added; column deleted, following the
`orders.discount_code` precedent.

**F6 — `erase_user` never reached `contact_messages`.** A name, an address, a
subject and whatever the customer typed — which for "my order has not arrived" is
routinely a delivery address and a phone number — survived erasure, and
`/admin/messages` reads that table. The reasoning to catch it was already written
IN the function, about the newsletter: "it keys on the ADDRESS rather than the
account — so a plain DELETE of a user never reaches it." Applied once, not twice.

The four-entry hand-written probe list is replaced by a catalog-derived guard
over every table with a text `email` column, with fixtures that make it able to
fail.

**F7 — `make check-layout` was RED.** The `__Host-goen_placed` cookie changed
from carrying order numbers to carrying opaque tokens, and the Makefile still
lifted that one value into both the cookie AND the `/orders/…/pay` URL. One value
doing two jobs; they are two variables now, with the order number read through
the grant the token names. The marker guard did its job — this is mistake #26
being caught rather than repeated.

**F9 — `order_access_grants` had no expiry and no sweep.** A permanent bearer
credential: the token opens the order page, the cancel form and the return form,
and one recovered from a proxy log worked forever. Retention now matches the
cookie's own MaxAge, `erase_user` reaches it, `reporting` cannot read the
digests, `store` is narrowed to INSERT and SELECT, `GrantOrderAccess` is
`:execrows` so an unknown number is `ErrNotFound` rather than a cookie no grant
backs, and the misplaced `CREATE TABLE` comment is back above the table it
describes.

**F8 — `/admin/customers` audited the detail page and not the search** that
already discloses email and name. (The reviewer confirmed the audit row itself is
clean: it carries the user id and nothing read.)

**M5, M6, M7, M1, M4** — `GOEN_BASE_URL` no longer guesses in a production
posture; `MaxConns` is set on the config before the pool is built rather than on
the copy `Config()` returns; README and `.env.example` rewritten from
`cmd/goen/main.go`'s actual `envOr` list; `/healthz` and `/readyz` no longer
reach the database for a navigation bar; the contact form is rate limited.

## The three the reviewer did not file

1. **A comment claiming `admin` is a member of `store`**, and calling a
   load-bearing `GRANT SELECT ON committed_orders … TO admin` "strictly
   redundant — removing this line leaves every test green". `pg_auth_members`
   holds no such edge. It invited somebody to delete a grant every back-office
   report depends on. Corrected in place, with the reasoning, because the
   independence is also what makes the F2 revokes possible at all.
2. **The last admin could erase themselves** and lock the shop out of its own
   back office permanently. `/account/erase` never consults `guardLastAdmin`.
   Fixed in `erase_user`, which CLAUDE.md documents as the only door.
3. **`settle_refund` was granted to `store` with no caller** — a `SECURITY
   DEFINER` path around the `refunds` revoke, from the customer-facing role, able
   to move a refund to `failed` and free the same capture's allowance to be
   claimed twice. Deleted; it comes back in the change that writes the refund
   webhook.

## Refused in writing

**M3 — the storefront role's blast radius.** Unchanged from round 6, and now with
a second reason: the write direction is derived from the source rather than
listed, so the boundary is bounded by a test instead of by vigilance.

**M2's second half — the outbox retries past `MaxAttempts` forever.** A
documented decision: a queue that forgets what it could not deliver reports
itself empty. Its first half (a 5-minute lease over 50 serial sends) is fixed.

## The limit that is written down rather than closed

Asking the column question over EVERY table surfaces a larger finding, and it is
**queued, not fixed**: `store` can also set `product_reviews.hidden_at`,
`product_questions.hidden_at`, `product_answers.hidden_at`,
`return_requests.resolution` and `.decided_at`, `orders.staff_note`,
`orders.completed_at`, `contact_messages.handled_at` and
`stock_notifications.notified_at` — every one a BACK-OFFICE decision on a table
the storefront legitimately writes. Symmetrically `admin` can rewrite
`contact_messages.message`, a customer's own words.

Closing them means hand-authoring column grants for ten more tables, and **a
grant one column too NARROW fails nowhere in this test suite** — every suite
connects as the OWNER, who is subject to no missing grant. It would surface first
in production, on a write path a customer is standing in. That is a worse outcome
than the finding. Doing it safely means deriving each grant and then proving
every write path under `SET ROLE`, an extension of
`coverage_integration_test.go`. The guard is scoped to the four deliberately
narrowed tables meanwhile, and says so in its own doc comment.

## What round 7 says about the gates

Round 6's lesson was that every guard here asks about ABSENCE. Round 7 adds the
sharper version: **the guards asked the READ direction and never the write one.**
`TestEveryRoleCanReadWhatItsQueriesRead` had existed since round 3. Its mirror —
does any role hold a write its own queries never make — is four findings' worth
of difference, and it did not exist because nobody had asked the question in that
direction, not because it was hard.

Two of its own mutations are worth keeping:

- The table-level guard stayed GREEN when `store` was handed back whole-table
  UPDATE on `users`, because `store` legitimately writes that table. Only the
  COLUMN question finds `users.role`.
- The column guard then stayed GREEN too, because its noise filter excluded every
  column with a DEFAULT — and `users.role` defaults to `'customer'`. A filter
  written for readability had quietly removed the subject. It tests the default
  EXPRESSION now: a value manufactured per row (`uuidv7()`, `now()`) is
  bookkeeping, a business constant is a real column somebody could set.

---

# Round 8 — third acceptance

Filed **3 High, 3 Medium, 1 Low**, plus a correction to the acceptance prompt itself.
All three gates were green when it started, and two of the Highs were live defects in
background workers.

The reviewer's own summary is the right frame: findings 1, 2 and 3 are **one story — a
privilege model tightened without the guard that would say when it went too far.**

## Fixed

**F3 (root cause) — nothing asked whether a role CAN do what its queries need.**
`TestNoRoleHoldsAWriteItsQueriesNeverMake` computed both sets — what a role's queries
write, and what it is granted — and errored only when the grant was WIDER. The narrower
case was never asked, with both sets already in hand. And
`TestEveryRoleCanReadWhatItsQueriesRead`, despite its name, was seven hand-written
statements.

Proven by mutation: `REVOKE DELETE ON checkout_attempts FROM store` → **green**;
`REVOKE SELECT ON products FROM store` → **green**.

`TestEveryRoleCanRunItsOwnQueries` asks it now, over every generated query for every
role, and both mutations go red — the second reporting 33 affected queries.

**The technique matters more than the guard.** Round 8's acceptance prompt told the
reviewer this was structurally impossible to test, because "every suite connects as the
schema OWNER, who is subject to no missing grant", and sent them to click through the
application as `store_svc`. That was **wrong**. `SET ROLE` binds ACLs even for a
superuser, and `EXPLAIN (GENERIC_PLAN)` resolves every table, column and function
privilege without executing the statement or binding parameters. The reviewer wrote it
in ~60 lines and it found both live defects in seconds.

That is a builder talking themselves out of a check and then writing the excuse into
the brief. The prompt is corrected, and the lesson is recorded in the guard's own doc
comment: **do not accept "structurally impossible" without asking what would make it
possible.**

**F1 — `store` could not DELETE `media_objects`, so the media sweeper had never
reclaimed anything.** Worse than the missing grant was the error handling: the failure
was logged at Warn under a comment saying a failed delete is "a foreign key refusing,
and that is the guard working", so a permission denial read as the guard working.
`Sweep` returned nil, `reclaimed` stayed 0, and `SweepForever` logs nothing when it
reclaims nothing. Hourly, forever, with images stored as bytes at up to 8 MiB each.

The sweeper runs on the ADMIN pool now, and the expected refusal is told from every
other one by SQLSTATE `23503` — an unexpected failure stops the pass and is returned,
because a sweeper that cannot delete is broken rather than busy.

**F2 — `store` could not DELETE `order_access_grants`, so the retention sweep added in
round 7 never ran once.** The bearer credential this repository's own comment calls "a
live bearer credential for nobody" was kept forever, and the comment claiming it was
expired shipped in the same commit that broke it.

`store` holds DELETE now, and the two tables get **opposite answers on purpose**:
deleting stored bytes is irreversible, so `media_objects` moved to admin instead. The
worst a bug here does is force a guest through `/orders/find` — an inconvenience with a
documented way back.

**F5 — a client inside the trusted-proxy CIDR could pick its own rate-limit key.**
Proven by the reviewer. The rightmost-untrusted walk is correct only if the trusted set
contains nothing but proxies, and `.env.example` suggests `10.0.0.0/8` — under which
every pod and VPN user is trusted. A client at `10.0.0.55` sending
`X-Forwarded-For: 8.8.8.8` got the key `8.8.8.8`, a different one per request. That
defeats the limiter bounding argon2id at 64 MiB a hash and makes `/orders/find` an
unbounded oracle for a customer's address.

It takes the rightmost entry outright now, which cannot be gamed: a forged entry always
lands to the LEFT of what the trusted proxy appended. **The cost is stated rather than
hidden** — behind two proxies everyone shares the inner proxy's bucket, which is the
same failure as configuring nothing, degraded and never forged. Closing that needs a
hop COUNT, because no set of addresses can tell a proxy from a client in the same range.

The test table had a spoof from OUTSIDE the set and an all-trusted case; the MIXED one
was uncovered, and it was the only one that broke.

**F4 — a render timeout answered 200 OK with an empty body and no log line.** The
handler treated `DeadlineExceeded` as "the caller left", which it cannot be: the render
detaches with `context.WithoutCancel` and imposes its own timeout, so a caller who
leaves yields `Canceled`. A deadline is always goen's own — every slot busy, or a stuck
read. It answers 503 with `Retry-After` and an ERROR line now. A 200 with an empty body
is also **cacheable**, so a CDN would have served the emptiness onward.

**F6 — a stalled partial refund retried after 24 hours could pay twice.** Stripe drops
idempotency keys after 24 h, `refundRequestKey` never changes, and `RefundedSoFar`
deliberately excludes this return's own key — so goen could not see the first refund and
`refunds_within_capture` still summed to one. The refund now carries goen's request key
in Stripe **metadata**, which has no expiry, and `Refund` looks for an existing one
before creating. Reachable only for partial refunds, which is exactly what `splitRefund`
produces.

**F8 — the acceptance prompt's claim that the media upload path had never been tested
with a malformed file was FALSE.** `internal/media/media_test.go` tests a polyglot
trailer, rejects HTML/PHP/SVG/bare-magic/truncated/zip, and asserts a decompression bomb
is refused with `ErrTooLarge` specifically, recording the mutation. A false claim in a
brief scoped a whole review round; corrected, with what genuinely remains untested (the
path through HTTP as `admin_svc`).

## Partly fixed

**F7 — four `check-layout` admin rows used the generic `.goen-admin` chrome marker.**
`/admin/health` now uses `.goen-health`, its unconditional worker list.

The first attempt used `.goen-admin__table` and **the marker guard refused it** — that
table only renders when something is wrong. Which is finding 7's own lesson landing on
the fix for finding 7: a check over DATA needs that data seeded. `/admin/questions`,
`/admin/messages` and `/admin/returns` still carry the generic marker, because giving
them a real one means seeding a question, a message and a return in the fixture.
**Queued by name**, not fixed.

## Also learned

`make check-layout` is **not deterministic on the admin session**: one run failed all 34
admin rows with "the staff session is not being accepted" and the next two passed with
109 rows. Worth chasing before it is trusted as a merge gate.

Separately, the H15 fix from round 7 proved itself by accident: a second `make run`
against a held port exited 1 immediately instead of hanging.

---

# Round 8 — the five items that were queued, now closed

Everything below was **Queued by name** at the end of round 8 and is now **Fixed in this
branch**. They are recorded together because four of the five turned out to share one
shape, and it is not the shape any of them was filed as.

## The queued reason that had already expired

**Column-level privileges — `store` could set ten back-office decision columns.**

The note refusing to fix it was explicit, and it was the right call *when it was
written*:

> a grant one column too NARROW fails nowhere in this test suite — every suite connects
> as the OWNER, who is subject to no missing grant. It would surface first in
> production, on a write path a customer is standing in.

That stopped being true **one commit earlier**, in the same round. `TestEveryRoleCanRunItsOwnQueries`
plans every generated query under `SET ROLE` with `EXPLAIN (GENERIC_PLAN)`, and
PostgreSQL resolves COLUMN privileges in `ExecutorStart` — so a too-narrow grant is a
red test over 425 (role, query) pairs in about a second. The guard that made this safe
was written for a different finding (`F3`), and **nobody re-read the note beside it.**

That is the lesson worth keeping: *a queued item is a claim about the world, and the
world moves.* Round 8's own headline was that a builder talked themselves out of a
check and wrote the excuse into the brief. This is the sequel — the excuse outlived its
own refutation, in the same file, for one commit.

Proven by mutation before anything was trusted: removing `fulfillment_status` from
`store`'s UPDATE grant on `orders` → `store CANNOT run CancelOrderByCustomer:
permission denied for table orders`. Restored, green.

Fourteen grants over eight tables, every column list **derived** (all columns minus
what the guard reports the role never writes), none hand-picked. Two pairs are
deliberately left out and named in the migration: `admin` on `order_private_data` and
on `stock_notifications`, where the write-column parser resolves no set — and a list
nobody derived is the hand-authored grant this approach exists to avoid.

**It immediately broke a neighbouring guard, which is the interesting part.**
`TestEveryColumnIsReadOrWritten` counted a `GRANT` as a use of a column. Harmless while
four tables carried column grants; the moment fourteen more did, every column named in
a grant would have read as USED and the dead-column guard would have gone blind on
exactly the eight tables just narrowed. It announced itself only because two allowlist
entries went stale in the same run. `GRANT`/`REVOKE` are cut from the corpus now,
beside `CREATE TABLE` and `COMMENT ON`, for the same reason: a privilege list is a
statement ABOUT a column, not a use of it. Proven by removing an allowlist entry and
watching `product_answers.id is mentioned by no query, no seed and nothing outside its
own declaration`.

## H6.3 — cancelling never closed the checkout

`Gateway.ExpireSession`, called post-commit from BOTH cancel doors — the customer's own
and the back office's — through a one-method interface each package defines for itself.
The open sessions are read INSIDE the cancelling transaction, so the set acted on is the
one that transaction decided.

**Whether money is in flight is Stripe's question, not goen's.** goen's own payment row
still says `requires_payment` until the webhook lands, so a customer who paid two
seconds ago looks unpaid from here. Stripe expires an OPEN session and refuses anything
else; that refusal reaching the log rather than being swallowed is what keeps the
provider the authority. Best effort and post-commit: the cancellation has already
committed and is correct, the session dies with the stock hold anyway, and money that
beats it there still arrives as `ErrOrderCancelled`.

## Stripe's HTTP interface had no test of any kind

`ResumeSession`'s five lines had never been executed once, and they carry the rule its
own doc comment calls "the double charge arriving through the code that exists to
prevent it".

Covered now against an `httptest.Server` the SDK's own backend injection points at —
not a hand-written fake, which would agree with whatever goen believes. Three mutations
recorded red:

| Mutation | Red |
| --- | --- |
| an unreachable Stripe reads as `open=false` | `TestOnlyAnOpenSessionIsResumable/Stripe_could_not_answer` |
| `ExpireSession` swallows Stripe's refusal | `TestStripeDecidesWhetherASessionCanBeClosed` |
| TWD divided by 100 as a zero-decimal currency | `TestTheSessionRequestCarriesWhatStripeCharges` |

The third is mistake #18, and until now **nothing asserted the amount that leaves the
process**.

## `check-layout`'s admin session was not non-deterministic

One run failing all 34 admin rows and the next two passing was read as flakiness. It is
not: the check had **one sentence for at least four causes**, and the Makefile hid the
most likely one.

- The staff-user and session inserts ended `>/dev/null 2>&1`, one with `|| true`. A
  missing user made the session `INSERT ... SELECT` write ZERO ROWS in silence, and
  every admin row then reported "the staff session is not being accepted" — which names
  a rejected cookie, not an absent one. Both are verified now and fail loudly.
- The session is probed ONCE before the sweep and the failure names which cause it is:
  a rejected session, a `GOEN_TOTP_KEY` that turns on a step-up gate this target cannot
  answer, or an unrecognised landing. It then skips the 48 rows rather than reporting
  one cause 48 times.

Verified by running the whole target with a token no session row matches: one failure
instead of forty-eight, naming the cause. The classifier had to learn that
`RequireStaff` answers **404 rather than redirecting to /signin** — it does not disclose
that `/admin` exists — so "still at /admin with no chrome" IS the rejected-session case.

**This is CLAUDE.md #17 and #26 on the same three lines**, and it is why "flaky" was the
wrong diagnosis: nothing about it was random.

## F7 — the last three generic admin markers

`/admin/questions`, `/admin/messages` and `/admin/returns` now carry
`.goen-admin__questions` / `.goen-admin__returns`, the elements that exist only when
there are rows, and the fixture seeds all three through **the site's own forms**.

The return is the whole commercial path, because `return_lines_within_purchase` refuses
a return of something that never shipped: the shop grants store credit, the customer
spends it at checkout (leaving the order owing nothing, and therefore committed with no
payment row — the zero-owed case `order_is_committed` exists for), the shop picks and
ships, and only then can the customer send it back. Six requests, five features, and if
any of them breaks the check breaks with it.

**Two values in that chain had to be made unique per run, and both were found by the
shop REFUSING them rather than by the check.** `GrantCredit` is idempotent on
(customer, amount, reason) — correct, and it meant a fixed reason funded only the very
first run; every run after it checked out an unfunded order, could not ship, created no
return, **and passed anyway on the row the first run had left behind.**
`order_shipments_tracking_key` is unique, so a fixed tracking number ships exactly once
ever.

A fixture that stops working and leaves its evidence lying around is worse than one
that never worked. Proven the other way as well: with the ship step removed and the
table emptied, exactly the two `admin returns` rows go red with "its fixture did not
run, so this check proved nothing" — and the questions and messages fixtures stay green,
so the mutation is telling the truth about what it broke.
