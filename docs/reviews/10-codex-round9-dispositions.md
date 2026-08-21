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
