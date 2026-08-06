# P8 — Concurrency under a flash sale: PostgreSQL and Go best practice

> Self-contained brief. Assume no context beyond this file and the repository.
> **For:** Codex · **Order:** third, after P1 and P2 — this is the one whose answer
> could invalidate a schema decision, so it is worth doing carefully.

`goen` is a Traditional-Chinese 3C storefront on PostgreSQL 18 with pgx/v5 + sqlc, one
Go binary, `net/http`, no cache layer, no queue except a database-backed outbox. Read
`CLAUDE.md` first — particularly "The database enforces what it can" and the four
documented departures from the imported rules.

## What the project already does

- **~39 rule triggers** hold cross-row invariants, and each **locks its aggregate root
  before it reads**: `refunds_within_capture` takes the payment row `FOR UPDATE`,
  `store_credit_never_negative` takes the account row. The stated reasoning is that
  without the lock, two concurrent writers each pass a test the other is about to
  invalidate.
- **Money and stock go through `SECURITY DEFINER` functions** — `hold_inventory`,
  `consume_reservation`, `release_reservation`, `record_inventory_movement`,
  `capture_payment`, `post_store_credit`, `redeem_coupon` — and the application role
  cannot write those tables directly.
- **Stock is held at checkout** for 30 minutes (`hold_inventory` inside the order's
  transaction), swept by a worker, and the Stripe session now expires with the hold.
- **Checkout takes `pg_advisory_xact_lock`** on its idempotency key as the first
  statement of the transaction, so a double-click serialises.
- **Coupon limits are counted from `coupon_redemptions` under a lock** the posting
  function takes, never from a counter.
- A **deadlock was found and fixed** in `redeem_loyalty_points`: an INSERT took
  `FOR KEY SHARE` on the account for the foreign key and the AFTER trigger then wanted
  `FOR UPDATE` on the same row — a lock upgrade, which several concurrent transactions
  turn into a deadlock. PostgreSQL resolving it by killing all but one **looked exactly
  like the guard working**. The fix takes the strongest lock first.

## The question

**What happens to all of this under a real 促銷 / 特賣 / 限量 spike** — a thousand people
trying to buy fifty units in the same ten seconds?

Work out and report:

1. **Where it serialises, and how badly.** Which locks become the bottleneck, in what
   order are they taken, and is there a path that takes two of them in different orders
   anywhere? Include the new advisory lock in that analysis.
2. **The hot-row problem.** Fifty units of one variant means one `product_variants` row
   and one reservation set. What is the actual throughput ceiling, and what are the
   known techniques — and which are appropriate here? Consider: `SELECT … FOR UPDATE
   SKIP LOCKED` over a pool of stock rows, an advisory-lock queue, a Redis/atomic
   pre-gate (which this architecture has no room for), optimistic retry with a bounded
   backoff, or simply admitting a queue.
3. **Measure it.** Build a load harness against the real schema (the project uses
   testcontainers) and produce numbers: orders/second, p50/p99 latency, deadlock rate,
   serialisation-failure rate, and what breaks first. Numbers beat opinions, and this
   project's rules require `benchstat` over `-count=10` for any performance claim.
4. **Oversell.** Try to break it. Concurrent checkouts, a cancel racing a capture, a
   sweep racing a consume, a coupon at its last redemption with fifty simultaneous
   claims, store credit spent twice. **Hold one transaction OPEN while another runs** —
   the project's own mistake #9 is that two goroutines behind a start channel finish
   microseconds apart and never actually overlap, and every guard here stayed green with
   its lock removed until the tests were rewritten that way.
5. **Go-side practice.** `pgxpool` sizing, statement timeouts, `context` deadlines under
   load, retry on `40001`/`40P01` (serialisation failure and deadlock) — where should
   retry live, and what must be idempotent for it to be safe? The project has an
   idempotency key on checkout; is that enough?
6. **The isolation-level question**, asked properly: goen runs at READ COMMITTED. Which
   of its invariants actually depend on the locks rather than the isolation level, and
   would REPEATABLE READ or SERIALIZABLE simplify or complicate them? What would the
   retry burden be?

Cite the PostgreSQL documentation for every locking claim. Where a technique is
folklore, say so.

## Report

Findings ranked by what breaks first under load, each with a reproduction. Then a
recommendation: what to change, what to leave, and what to measure again at ten times
the catalogue size.

Then end with the same three lists every brief in this set ends with:

1. **Verified by running** — executed, result observed.
2. **Read but could not verify** — a view formed from the code or the documentation,
   without running it.
3. **Did not look at** — in scope on paper, not examined.
