# Round 9 — codex whole-project cold review: dispositions

`review-process.md`: every finding reaches exactly one of three states before
merge — **fixed here**, **queued by name**, or **refused in writing**. Triage is
line by line; nothing is waved through in either direction.

Twelve findings were returned. Each was re-verified by an independent
adversarial pass whose default was REFUTED and which had to RUN something before
confirming. **None was refuted.** Two more were found while verifying.

## What the verification changed

Re-running a report is what turns a claim into a fact, and here it moved four of
them:

| Finding | The report said | Measured |
|---|---|---|
| F5 | the itemisation disagrees with the header | **a discounted order that earned 免運 cannot be invoiced at all** — ECPay refuses it, one staff click failing every time |
| F3 | the capture is refused and only a log line remains | the refusal **aborts the transaction**, so the event was never marked processed, the handler answered 500, and Stripe retried forever |
| F10 | a race between the list read and the enqueue | the race is the **smaller half**: delivery never re-read consent at all, so an unsubscribe *after* the send had the same outcome |
| F1 | the loser of a race refunds | also true with **no provider involved** — the store-credit path proves it with no Stripe call |

And one framing correction: F2's promotion-of-an-existing-customer is a recorded
decision and remains one. What the decision never accounted for is an
**unverified** account with live sessions.

## Fixed

| # | Finding | Where |
|---|---|---|
| F2 | Pre-registering a staff address takes over the back office | #31 |
| F1 | Money leaves on a return decision that lost | #31 |
| F8 | Removing a second factor left its sessions alive | #32 |
| S5 | The last-admin guard was a read-then-write | #32 |
| F9 | An erased address survived in `audit_events` and the outbox | #32 |
| F12a | Every checkout chooser discarded the form | #33 |
| F12b | A product with no reviews could never receive its first | #33 |
| F5 | The invoice itemisation inferred the header | #34 |
| F6 | 折讓 was claimed delivered and had no door | #34 |
| F4 | An order could finish while still owing a parcel | #34 |
| F3 | Money for a cancelled order left no record — and 500ed | #35 |
| F7 | The media sweeper deleted a just-attached image | #35 |
| F11 | Two carriers sharing a tracking number, one silent parcel | #35 |
| F10 | An unsubscribe during a send was still posted | #35 |
| S7 | `/deals` could quote a price with no discount on it | #35 |

Every one carries a lock that has been **watched failing for its own reason**.

## Queued by name

- **A short-ship door.** `orders_finished_when_shipped` refuses to finish an
  order that still owes a parcel, which is right — but a shop that genuinely
  cannot send the remainder has no way to say so, and is stuck at `shipped`.
  Abandoning a remainder must settle the holds in the same transaction, which is
  a decision a person makes rather than a status move.

## What the guards found on their own

Worth recording, because it is the machinery working rather than the review:

- `TestEveryTableIsRead` reported **its own allowlist entry as stale** —
  `payment_webhook_events` was exempt because "the row exists to be collided with
  rather than to be read", and `/admin/health` reads it now.
- `TestEveryGeneratedQueryHasACaller` caught `CountAdmins` losing its last caller
  when the count moved into the statement.
- `TestEveryCheckConstraintIsExercised` and
  `TestTheStatedSchemaTotalsAreTheRealOnes` each demanded work for a new
  constraint before it could be merged.
- `TestEveryUniqueConstraintIsExercised` named a new index as untested,
  unprompted.

## What went wrong in the fixing, recorded rather than tidied away

- **A credit test was false-green.** It used a card-funded fixture, where the
  credit split is always zero. The mutation is what said so.
- **A JSON erasure sweep was false-green.** Deleting the outbox line from
  `erase_user` left it passing, because the fixture enqueued no letter and an
  empty outbox is a clean outbox.
- **A sweeper test was false-green.** Calling `Sweep` re-reads the candidate
  list, so the attached upload was never offered; the window had to be driven by
  separating the two halves.
- **A mutation that did not compile is not a red test.** One attempt removed the
  last reference to an import.
- **`git checkout` on a file with uncommitted work reverted my own fixes** twice.

## A correction to the section above

It says every one of the fifteen "Fixed" rows "carries a lock that has been
watched failing for its own reason". That was true of each lock at the moment it
was written and it is not the same claim as "the fix was right": the round after
this document landed found a defect inside FIVE of those rows, and a sixth in
the sixteenth. A lock proves the behaviour it asserts; it says nothing about the
behaviour nobody thought to assert.

The ones that needed a second pass are named in the round-10 section below, and
the pattern in every case is the same — **a fix that closes one hole opens the
next**, which is what `review-process.md` says to expect and why it asks for
`/self-review` on the fix itself.

## Round 10: what the role reviews found in the fixes

A fix made in response to a finding is new work. Six role lenses over the
completed set produced 42 findings; 14 were adversarially verified and none was
refuted. The most severe were regressions from the earlier fixes in this same
document, which is the rule `review-process.md` states and the reason it is
stated: **a fix that closes one hole routinely opens the next.**

Four survived into a second pass, each because the earlier lock could not see
the thing it named.

### The unreconciled alarm had no off switch

`payment_webhook_events.unreconciled` was added so money arriving for a cancelled
order left something countable rather than one ERROR log. Nothing could clear it,
so `/admin/health` was unhealthy forever after the first arrival — **and an alarm
that is always on is one nobody reads**, which is the failure the flag was added
to prevent, arriving one step later.

`reconciled_at` is the acknowledgement, written from a plain form on
`/admin/health` and audited. The only thing anybody can do about that money is a
refund by hand at the provider, which goen cannot see land, so the operator
saying they did it IS the record. The question is asked in the UPDATE's own WHERE
clause, so two staff members pressing it is one row and one audit entry.

`store` INSERTs that table, so a whole-table grant would have carried the new
column. It is narrowed to the five columns the webhook writes: `reconciled_at` is
the shop's statement about money, not the storefront's.

### The lock on that flag drove a callback of its own

`TestMoneyForACancelledOrderLeavesSomethingToActOn` passed its own function to
`ProcessWebhook`, and that function called `Unreconciled`. So it proved the store
could record the outcome, never that the switch in `handler.go` asks it to —
deleting the real branch left it green, with 671 tests still passing. The
replacement drives the signed webhook through the handler and is proven red
against that deletion.

### Eleven rules were raised by a function and asserted by nothing

`TestEveryRuleTriggerIsExercised` keys on the TRIGGER, and a trigger function
raises as many distinct rules as it has branches: `orders_check_complete` raises
three, `redeem_coupon` four. Deleting one branch removes no trigger, so the
coverage guard stayed green while the rule stopped being enforced — and a case
asserting only that a row was refused cannot tell the difference either (#8).

`TestEveryRaisedRuleIsAssertedByName` derives its corpus from `pg_proc`, so a
rule added to a function body is covered the moment it is written. It asks for
the name inside a Go string literal, because a name in a comment is how a guard
comes to be satisfied by nothing. Twelve rules were in the gap and all twelve are
proven red.

### Three branches ran with no test behind them

- **main's newsletter consent gate.** The send freezes one outbox row per
  subscriber and drains at bulk priority behind every transactional message, so
  an unsubscribe committing in that window is only visible at DELIVERY — and
  delivery is wiring, which is where a rule goes to be deleted without anything
  noticing. `cmd/goen` has an integration test now.
- **The checkout's 422 for a spent coupon.** Store-tested, never driven through
  the handler where the mapping lives. `FindCoupon` reads no limit by design, so
  that refusal is an ordinary outcome on the buying mainline; the 500 it replaced
  discarded the whole address at the moment of paying.
- **A form action nothing answers.** `TestEveryFormWorksWithScriptingOff` asks
  that a form HAS an action and never that a handler exists for it. A form
  posting to an unregistered path reaches the mux's 404, which looks like a
  working page until somebody presses the button — #35 on the write face.

## Round 10, continued: the whole-project cold review

Six lenses over `f9949fd..HEAD` — money, privileges/schema, test quality,
reachability/UX, concurrency/workers, docs-vs-code. Each formed findings from
the raw diff before reading any report, and each was asked to RUN rather than
reason. Every disposition below is `Fixed in this PR` unless it says otherwise.

Two lenses independently reproduced the same P0, which is worth recording: the
partial index and the stranded claim were found from the money side and from the
privilege side, by different routes, with different probes.

### Fixed

| # | Finding | What it cost |
|---|---|---|
| 1 | `invoice_documents_number_key` not partial | One refused 折讓 killed the feature shop-wide, permanently |
| 2 | `isUniqueViolation` bound to SQLSTATE, not the name | The refusal named the wrong order's key |
| 3 | A refused claim had no door | That refund could never be relieved |
| 4 | `request_key` mutable once issued | The same refund filable twice at the 財政部 |
| 5 | Three definitions of "what has gone back" | The 折讓 form offered the card half of a split refund |
| 6 | `erase_user` missed `stock_notifications` by address | A signed-out restock notice survived erasure |
| 7 | `erase_user` reached the outbox by key | An erased address and a live reset token survived 30 days |
| 8 | Twelve rules raised by a function, asserted by nothing | Each deletable with every suite green |
| 9 | The unreconciled alarm was monotone | `/admin/health` unhealthy for ever after the first arrival |
| 10 | Its lock drove its own callback | Deleting the real branch left 671 tests green |
| 11 | `delivered` a terminal trap for a partial order | Wedged for ever, stock stranded invisibly |
| 12 | `Advance` wrapped with `%s` | The constraint-name assertion was dead code |
| 13 | The saved-address chooser was inert | A repeat customer could use only their default address |
| 14 | Pressing 更新 could place the order | Changing your 發票 type bought the basket |
| 15 | `splitRefund` did not exclude its own compensation | A split return whose credit half landed could never resume |
| 16 | A pending claim rendered as a filed 折讓 | An operator reads a blank number as done |
| 17 | A stranded claim appeared on no page | The only sign was a button that refused, months later |
| 18 | Seven redirect parameters rendered nothing | A tax filing confirmed nothing |
| 19 | 折讓 refusals mapped to the 統編 message | Sent staff to the wrong fields |
| 20 | Three checkout choosers carried no `aria-invalid` | A refused delivery method announced nothing |
| 21 | Eleven more fields in the same state | Their files were already counted as covered |
| 22 | main's newsletter consent gate had no test | Deletable in silence |
| 23 | The checkout's 422 for a spent coupon had no handler test | Same |
| 24 | `secure_promoted_account`'s "always" had no test | A pre-promotion session survived becoming a staff session |
| 25 | The mid-sweep re-check had one of two subjects | A product photo could be reclaimed mid-attach |
| 26 | `Store.Issue` was driven by nothing | The itemisation defect was revertible green |
| 27 | "The card half was not sent twice" could not fail | Idempotency made the sum identical either way |
| 28 | `/admin/health` measured the HEALTHY page | Neither alarm table had ever rendered |
| 29 | `/admin/orders/{number}` had no layout row | It scrolled sideways at 375px the whole time |
| 30 | Six stated facts had drifted | See below |

### Guards added, each proven red

`TestEveryRaisedRuleIsAssertedByName` (corpus: `pg_proc`),
`TestEveryRedirectNoticeHasAMessage` (both directions),
`TestEveryRefusableControlCanBeMarkedInvalid`,
`TestEveryFormActionResolvesToAPostRoute` widened from 15 actions to 73.

### Documents corrected

The writer allowlist described as holding three entries is empty. The roadmap
said 33 named mistakes against 37. The `SECURITY DEFINER` sentence named six of
the fourteen functions `store` may execute and omitted all three payment
writers, in a sentence whose subject is "a ledger, or a payment".
`secure_promoted_account` was documented nowhere. "Invoice allowance vs refund"
was recorded as an open question and is closed. The short-ship door was queued in
this file while CLAUDE.md designates `docs/roadmap.md` as the only such list; it
is `A8` there now.

### Queued by name

- **The short-ship door** — `docs/roadmap.md` §A8. What has not gone out is
  either still going out or it is an abandonment, and abandonment is a decision
  a person makes rather than a side effect of a dropdown.

### Refused in writing

- **A foreign key from `hero_slides`/`product_images` to `media_objects`.** The
  concurrency lens showed the sweeper can still delete an object whose attach is
  in flight and UNCOMMITTED, which the re-asked predicate cannot see under READ
  COMMITTED. The comment overclaims and is corrected; the mechanism is left. The
  window is the milliseconds of one DELETE against a >24h-old orphan, and closing
  it properly means a real foreign key on a column that deliberately holds two
  kinds of key — an embedded filename from the seed or a digest — which is a
  schema change with a wider blast radius than the defect.

### Not reached, and said so

No lens read the migration SQL in full, and the reachability lens ran no browser.
`make check-layout` was run separately and now covers both surfaces it named.
