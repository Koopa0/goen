# Round 10 — cold review: verified findings and fix specifications

Twenty-eight findings, every one verified by an independent adversarial pass
whose default was REFUTED and which had to RUN something before confirming.
Full specifications are one file per finding in [`11-specs/`](11-specs/); this
document is the list, the order, and what collides.

`review-process.md`: every finding reaches exactly one of three states before
merge — **fixed here**, **queued by name**, or **refused in writing**. Nothing
below is waved through in either direction.

## How this was produced

Two reviews ran independently and were merged only after both had finished:

1. **This repository's own cold sweep** — nine dimension reviewers over the
   tree (Go idiom, HTTP layer, package architecture, dead code, tests, system
   design, UI/UX, documentation, build/CI), each adversarially refuted, plus
   two critics: one asking what nobody looked at, one hunting the shape
   CLAUDE.md says every guard here is blind to — *two correct halves that
   disagree*.
2. **An external review** (codex), read only AFTER the above had formed its own
   findings. `review-process.md` asks for exactly this ordering: a report is a
   warm reading and hands you its own frame.

Then every open claim from both — 19 of them — got its own verifier with a live
database, a running server, and a requirement to reproduce rather than argue.

**Nothing in this document is transcribed from either report.** Every entry
below was executed against the running system.

## What verification changed

Re-running a claim is what turns it into a fact, and here it moved eight of them.

| Claim | As reported | Measured |
|---|---|---|
| README links a LICENSE that does not exist | missing file, repo under exclusive copyright | **REFUTED** — `LICENSE` is present, tracked, Apache 2.0, 11 KB |
| `SitePath` open redirect via `///` | exploitable through the redirect handlers | **the redirect sinks are closed** — `http.Redirect` normalises to a single slash, measured live. The live sink is the promo/hero CTA rendered as `templ.SafeURL` into an `<a href>` |
| Production starts without Stripe or SMTP | both are release blockers | **Stripe is REFUTED** — a recorded decision, and a half-configuration already refuses to start. **SMTP survives in full** |
| ECPay remote-first split | provider-first write ordering is the defect | **that ordering is a recorded decision.** What survives is the void date, reproduced against ECPay staging: `RtnCode 1600003` on any day after issuance |
| Argon2 timing enumeration | theoretical | **reproduced: ~2 ms vs ~30 ms**, and it discloses *how* somebody signs in, not only whether they exist |
| Rate limiter unbounded | earlier pass called it a nit | **confirmed high** — the `TTL` comment states the invariant the code does not implement |
| `Store.Answer(…, staff bool)` | live authorization hole | **no reachable caller** — deleting the method still builds. Latent API hazard, low |
| Concurrent returns | two open requests *and* double shipping refund | **both reproduced**, holding T1 open across T2 |

Two findings were refuted outright — one from each side. That is the point of
running both.

## The findings

Severity is what survived verification, not what was filed.

### Critical

| # | Finding | What breaks |
|---|---|---|
| 1 | [`loyalty-two-defects`](11-specs/loyalty-two-defects.md) | Two routes to the same failure: an order whose point award truncates to zero, and a balance that goes negative when an award expires after being spent. Either aborts the Stripe capture transaction → 500 → Stripe retries → fails identically forever. **Money at Stripe, order never paid.** Proven with rolled-back SQL: same rows, `0` today and `-100` tomorrow |

### High

| # | Finding | What breaks |
|---|---|---|
| 2 | [`stripe-webhook-tristate`](11-specs/stripe-webhook-tristate.md) | A boolean carries three states, so a *known but unreadable* event is ACKed as *not mine*. Reproduced end to end with a real HMAC: HTTP 200, `processed_at` stamped, `unreconciled` NULL. `IgnoreAPIVersionMismatch: true` makes version skew the live trigger |
| 3 | [`refund-does-not-reverse-points`](11-specs/refund-does-not-reverse-points.md) | Points and tier survive a full return — buy, return, keep the points. `committed_orders` is answering a question it was never split to answer |
| 4 | [`stripe-session-cancel-race`](11-specs/stripe-session-cancel-race.md) | `open_payment` asks nothing about the order it attaches to, so a cancelled order can hold a payable Checkout Session |
| 5 | [`shipping-name-en-wiped`](11-specs/shipping-name-en-wiped.md) | Publishing a shipping version silently clears its English names — **and a guard's allowlist excuses it.** A test institutionalising the bug |
| 6 | [`concurrent-returns-double-open`](11-specs/concurrent-returns-double-open.md) | `HasOpen` is checked on the pool before the transaction and nowhere under a lock. Two open requests coexist; each claims the whole delivery fee. One is then permanently unpayable |
| 7 | [`ratelimit-unbounded`](11-specs/ratelimit-unbounded.md) | Full-map sweep per new key under one mutex, no cap. The `TTL` comment states the invariant the code does not implement |
| 8 | [`domain-sentinels-swallow-infra-errors`](11-specs/domain-sentinels-swallow-infra-errors.md) | A database timeout reaches the customer as "not enough points". Category-assigning wrap where only category-preserving is sound |
| 9 | [`setzoneprefixes-appends`](11-specs/setzoneprefixes-appends.md) | Named and presented as replace, implemented as append. Removing a postal prefix from the form leaves it assigned |
| 10 | [`return-retry-ui-gap`](11-specs/return-retry-ui-gap.md) | After a transient refund failure the page tells staff to retry and renders no control to do it |
| 11 | [`refund-completion-definitions`](11-specs/refund-completion-definitions.md) | A non-empty `providerRef` stands in for "refund complete", so a wholly store-credit refund vanishes from the timeline, the reports and the 折讓 form. Reproduced on a real order in the dev database |
| 12 | [`production-starts-without-deps`](11-specs/production-starts-without-deps.md) | No SMTP in a production posture → every letter marked delivered, none sent. Password resets, verification, receipts, dispatch notices |
| 13 | [`ecpay-remote-first`](11-specs/ecpay-remote-first.md) | Void sends today's date, not the issue date. Reproduced against ECPay staging: every void after the day of issuance fails, and void-then-reissue is the only correction path a 統一發票 has |
| 14 | [`totp-key-derivation`](11-specs/totp-key-derivation.md) | Bare unsalted SHA-256 as a KDF, and `.env.example` says "any length will do" two lines above the property that defeats |
| 15 | [`rescission-window-timezone`](11-specs/rescission-window-timezone.md) | 消保法 §19's seven days counted in UTC while the schema's only statement of the shop's calendar is `Asia/Taipei`. Proven divergence on a real boundary — the screen built to inform an unwaivable right misinforms it, in the shop's favour |
| 16 | [`sitepath-triple-slash`](11-specs/sitepath-triple-slash.md) | `SitePath("///evil.example/x")` returns ok. Go's `net/url` resolves it same-origin; every browser resolves it cross-origin. Two implementations of one question, and the sophisticated one is the wrong one |
| 17 | [`parse-helpers-collapse-states`](11-specs/parse-helpers-collapse-states.md) | One parser, nine fields, four inputs collapsed to `0`. A typo in warranty months silently disables registration; a typo in a parcel dimension re-enables 超商取貨 for an oversized item |
| 18 | [`static-assets-hit-the-database`](11-specs/static-assets-hit-the-database.md) | Session and cart middleware run on every asset. **Measured: 48 discarded queries per home-page view** for a signed-in visitor. Two of the four chrome middlewares already carry the prefix list |
| 19 | [`no-compression`](11-specs/no-compression.md) | Nothing is compressed anywhere, and there is no proxy in the deployment story. **Measured: `app.css` 82,856 B → 17,688 gzip**, render-blocking |
| 20 | [`restore-drill-false-green`](11-specs/restore-drill-false-green.md) | Swallows `pg_restore` errors, compares row-count *estimates*, never compares the schema — then prints "the dump restores to the same schema" |

### Medium

| # | Finding | What breaks |
|---|---|---|
| 21 | [`ship-pending-bypasses-guard`](11-specs/ship-pending-bypasses-guard.md) | `Ship` only advances status from `picking`, so for a pending order no `orders` UPDATE happens and `orders_legal_transition` never fires. The comment above it claims that trigger is what prevents this |
| 22 | [`webhook-unknown-session-no-record`](11-specs/webhook-unknown-session-no-record.md) | Money for a session goen cannot name is ACKed with a log line and no durable record, while the sibling branch writes `unreconciled` |
| 23 | [`argon2-timing-and-phc`](11-specs/argon2-timing-and-phc.md) | A 513-byte password distinguishes account states by response time (~2 ms vs ~30 ms), and discloses whether an account signs in with a password or Google |
| 24 | [`baseurl-not-an-origin`](11-specs/baseurl-not-an-origin.md) | `checkProductionPosture` checks non-emptiness only; its own doc comment states the contract it does not implement |
| 25 | [`suites-hardcode-migration-001`](11-specs/suites-hardcode-migration-001.md) | Six suites bypass `dbtest`'s runner and hard-code `001`. Latent until `002`, then silently green on a stale schema |
| 26 | [`deadcode-guards-false-green`](11-specs/deadcode-guards-false-green.md) | Both dead-code guards match identifier presence over a corpus including tests and comments. The real analyser finds 14 unreachable exports |
| 27 | [`checkout-autocomplete-and-inputmode`](11-specs/checkout-autocomplete-and-inputmode.md) | One of seven checkout fields carries an autocomplete token; `inputmode` appears zero times site-wide |

### Low

| # | Finding | What breaks |
|---|---|---|
| 28 | [`answer-staff-flag-is-caller-supplied`](11-specs/answer-staff-flag-is-caller-supplied.md) | `Answer(…, staff bool)` makes authorization a parameter. No reachable caller today — deleting the method still builds. The only thing between a forged 店家回覆 badge and the storefront is that a handler was never written |

## Also found, not specified here

Three gaps in the verification apparatus itself, filed because
`review-process.md` says instruments are review targets in their own right:

- **No Go test builds the real router.** `cmd/goen/server_test.go:38` constructs
  its own `http.NewServeMux()`. 150+ route patterns, the middleware order, and
  every `RequireStaff` / `RequireUser` wrapper are asserted by nothing that
  executes.
- **`internal/returns` has no handler test of any kind.** Its
  `integration_test.go` contains zero `httptest` or `ServeHTTP` — `ownOrder` and
  `ownedBySignedInUser`, which decide who may open a return against an order,
  are untested.
- **CLAUDE.md says `check-layout` measures 85 viewports; it measures 113.**
  Re-ran, PASS. `TestTheStatedSchemaTotalsAreTheRealOnes` exists to stop exactly
  this drift and covers only the schema totals.

## Implementation order

Five waves. Within a wave order is free; across waves it is not. The rule that
produced it: **whatever defines a vocabulary lands before whatever speaks it,
and whatever a fix makes newly reachable lands with the surface that shows it.**

### Wave 0 — vocabulary and instruments

Nothing else can be written correctly until these exist.

1. `domain-sentinels-swallow-infra-errors` — three later fixes add database
   refusals that must be mapped in this grammar.
2. `suites-hardcode-migration-001` — before the suites acquire new tests.
3. `restore-drill-false-green` — the instrument that will be asked to prove the
   schema amendments round-trip.
4. `deadcode-guards-false-green` — corpus decided (`./...`, no `-test`, no
   integration tag) and **the gate lands here, not in wave 4**. The deferral's
   reason was parallel-branch attribution; execution is sequential in one tree,
   so an orphan surfaces in the finding that made it — the right PR, not the
   wrong one. `internal/product.Store.Answer` rides on a bidirectional allowlist
   entry naming the wave-1 spec and the queued storefront route, and fails
   stale the day the route lands. (Corrected from the original wave-4 deferral;
   the deadcode spec's RESOLVED block records the experiment.)

### Wave 1 — the schema

`make db-reset` + `make test-integration` between each; `make schema-drift` once
at the **end** of the wave, not after each step.

1. `loyalty-two-defects` — first, and **its model must be widened at design time
   to admit a clawback as its own kind**. Wave 1c depends on that answer.
2. `refund-completion-definitions` — settles "what has gone back" before
   anything reads it.
3. `refund-does-not-reverse-points` — carries the view/grant split below, and
   its clawback half **must land in the same commit as** `return-retry-ui-gap`.
4. `rescission-window-timezone` — last of the date changes; it is a substitution
   across sites the three above are still moving.
5. `stripe-session-cancel-race`, `ship-pending-bypasses-guard`,
   `concurrent-returns-double-open` — independent of each other.
6. `argon2-timing-and-phc`, `answer-staff-flag-is-caller-supplied`.

**End of wave 1, one commit:** update the stated schema totals in CLAUDE.md and
README.md once, read from the catalogue. Do not let nine branches each edit
those two lines — that is the conflict that resolves clean and lands wrong.

### Wave 2 — the payment surface, as ONE commit

`stripe-webhook-tristate` + `webhook-unknown-session-no-record`, with the third
`unreconciled` cause from wave 1e folded in. One tristate, one enumeration.

### Wave 3 — the process surface

1. `sitepath-triple-slash`, then `baseurl-not-an-origin` written in its grammar.
2. `production-starts-without-deps` + `totp-key-derivation` + baseurl's posture
   arm as **one commit** to `checkProductionPosture`.
3. `ratelimit-unbounded`, `ecpay-remote-first` — independent.

### Wave 4 — surfaces and gates

1. `static-assets-hit-the-database` + `no-compression` as **one commit** — the
   prefix filter can silently disable the compression.
2. `shipping-name-en-wiped`, `setzoneprefixes-appends`, then
   `parse-helpers-collapse-states`.
3. `checkout-autocomplete-and-inputmode`.
4. `deadcode-guards-false-green` — landed in wave 0 (see above); here, only
   re-run it against the finished state and empty what the allowlist no longer
   needs.

## The clawback decision — DECIDED

`11-specs/refund-does-not-reverse-points.md` asks whether a loyalty balance may
go negative when a refund invalidates points the customer has already redeemed.
**It may not.** A clawback reverses the UNCONSUMED REMAINDER of the lot the
refunded order created, and never more.

The spec argues the other way, and its argument is real: clamping makes
redeem-before-return a deliberate farm. It is overruled on goen's own numbers.

The earn rate is `PointsPerHundred = 1` and the exchange is
`PointsPerCredit = 10` — one point per NT$100 spent, ten points per NT$1 of
credit, so **0.1%**. `MinRedemption` is 100 points, which takes NT$10,000 of
spend to reach. The maximum shortfall on a fully-redeemed refunded order is
therefore one thousandth of its value: NT$10 on a NT$10,000 order.

消保法 §19 I's 「不負擔任何費用」 puts the return postage on the shop — NT$80–200
a parcel, which this repository already states it pays. **The farm the clamp
leaves open yields the attacker less than the shop is already paying in postage
on the same transaction, and it costs the attacker NT$10,000 fronted and a
fortnight's wait to collect it.** Closing it does not close the abuse it rides
on; a shop with a returns problem has a postage problem, and can already see
repeated returns. A residue whose exploitation costs more than it yields is not
the same defect one step along.

Against that, the overdraw's own failure mode lands on the innocent case: a
customer who redeemed points months ago and then returns one item reads a
negative number on their own account page, having exercised an unwaivable
statutory right. That is the shape this repository is careful about everywhere
else on the §19 surface.

Three consequences for implementation:

1. **The lot model gives the clamp for free.** Once a spend is paired with the
   lot it consumed, "reverse what is left of this order's lot" cannot overdraw,
   and `loyalty_never_negative` stays the authority rather than acquiring an
   exemption. Do not add a `reason = 'return'` early return to that trigger.
2. **The shortfall is recorded, never silently written off.** When the lot is
   partly or wholly consumed, the clawback entry records what was actually
   reversed, and the difference is readable — a number the shop cannot see is
   the failure mode this repository names most often.
3. **The value the customer kept is not chased.** A redemption writes
   `store_credit_entries` with reason `'points'`, so the extracted value lives
   in the credit ledger, which has its own reversal rules — and this repository
   has already decided that a shipped order's credit is not reversed. The mirror
   applies.

The other half of that finding — `member_spend` counting refunded orders toward
the rolling-year tier — is not a judgement call and carries the real commercial
consequence. Buy, return, keep the tier, keep the multiplier. Fix it as written.

## Collisions

Nine pairs collide. The three that will cost real time if missed:

**The `refund-does-not-reverse-points` fix does not apply as written.**
`member_spend` is `LANGUAGE sql`, so its body resolves at CREATE time, and
`order_refunds` does not exist at that line. The repair is trapped from the
other side too: `order_refunds`' GRANT names `admin`, created later still.
Resolution: split the object from its grant — the `CREATE VIEW` moves above
`member_spend`, the `GRANT` stays where it is, and the comment says why, or the
next reader tidies them back together. Do **not** restate the refund sum inline;
that is exactly the copy `order_refunds` exists to prevent.

**Three fixes edit one ledger.** `loyalty-two-defects` rewrites the model so a
spend pairs with the lot it consumed; `refund-does-not-reverse-points` posts a
clawback — negative, therefore `expires_on IS NULL`, therefore indistinguishable
from a spend under the new pairing; `rescission-window-timezone` changes what
`current_date` means, and the balance view applies expiry with `current_date`.
Any two merge textually clean and produce a balance no branch computed.

**A clawback larger than the balance is covered by no rule.**
`loyalty_never_negative` refuses it, which is the wrong answer: the points were
legitimately earned and legitimately spent, and the shop's own refund is what
invalidated them. **Somebody has to decide** — may the balance go negative for a
clawback, is it capped and the difference written off, or does it become a
store-credit debit? All three are defensible; only the first needs a schema
change. Decide before writing code.

## Invisible to every gate

`make verify` runs no database suite. `check-layout` asks geometry plus seven
DOM-decidable rules. So a bad implementation of these would pass everything:

- `no-compression` — nothing in the tree mentions `Accept-Encoding`
- `checkout-autocomplete-and-inputmode` — `autocomplete` appears only in
  generated `*_templ.go`
- `static-assets-hit-the-database` — no gate counts queries per request; the
  filter could be applied to the wrong prefix set and stay green
- `restore-drill-false-green` — the instrument being repaired is the only thing
  that could catch a bad repair
- the startup refusals — observable only by starting the binary with that
  configuration, so `cmd/goen/main_test.go` is where they have to live
- `argon2-timing-and-phc` — a timing property no behavioural test can prove.
  CLAUDE.md's own standard applies: record the mutation GREEN and say so, as the
  constant-time compare and the `/admin/messages` clock already do

## Does this force `002`?

Nine findings amend `001`. **None forces `002`** — nothing is deployed, so the
escape clause CLAUDE.md states still holds and `make schema-drift` is what keeps
amending safe.

One caveat worth stating: `loyalty-two-defects` is a model change to a ledger
that in a deployed world could not be amended at all. If the answer to "when
does this deploy" is *soon*, cut `002` at that finding and let the other eight
amend `001` beneath it. The cost of being wrong in that direction is a rebuilt
development database; in the other direction it is a ledger migration written
after the fact.
