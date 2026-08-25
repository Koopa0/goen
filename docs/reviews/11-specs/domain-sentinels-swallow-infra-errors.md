# domain-sentinels-swallow-infra-errors

**Verdict** CONFIRMED · **Severity** high · **Origin** external report (verified this round)

**Files** internal/loyalty/store.go, internal/loyalty/handler.go, internal/cart/store.go, internal/cart/handler.go, internal/returns/store.go, internal/returns/handler.go, internal/admin/product.go, internal/admin/handler.go, internal/admin/admin.go, internal/cart/coupon.go, internal/i18n/chrome_account.go, migrations/001_initial_schema.up.sql, .claude/rules/error-handling.md, internal/loyalty/integration_test.go, internal/cart/integration_test.go

> **RESOLVED — two semantic mismatches in this spec's constraint→sentinel table.**
> Reported by the implementing agent, verified, and decided here. Where this
> block and the body below disagree, this block wins.
>
> **1. `return_within_shipment` does NOT map to `ErrNotReturnable`.**
> That sentinel says "nothing on this order can be returned" and its own comment
> means it. The constraint fires on a QUANTITY that exceeds what remains
> returnable — some units, and other lines, may still be returnable. Mapping it
> there tells a customer who asked for one too many that the whole order is
> closed to returns.
>
> Add `ErrTooMany` to `internal/returns`, map the constraint to it, and render
> `i18n.KeyReturnTooMany` — **the key already exists** (`returns.toomany`,
> 「數量超出可退貨的範圍。」/ "That is more than can be returned.") and
> `handler.go:71` already renders it for the same customer-facing situation when
> the handler's own loop catches it. One outcome, one sentinel, one key, whether
> Go caught it or the database did.
>
> **`reject` must re-render from a FRESH read**, because the constraint firing
> means the returnable figure moved between the read and the write — a stale form
> would show the number that was just refused.
>
> **While you are there: `ErrInvalid` is already overloaded and already renders
> the wrong message.** `parseWanted` (`internal/returns/store.go:182`) returns
> `ErrInvalid` when a quantity exceeds its ceiling, and the handler maps
> `ErrInvalid` to `KeyReturnNeedsReason` — so a customer whose quantity is too
> large is told to write a reason. Point `parseWanted` at the new `ErrTooMany`.
> This is not scope creep: it is the same defect this spec exists to fix, one
> layer up, found while fixing it.
>
> **2. `store_credit_never_negative` does NOT map to `cart.ErrUnavailable`.**
> That sentinel means "variant unavailable" and the handler treats it as sold out,
> redirecting to `/cart`. The constraint means the customer's own store-credit
> balance moved between the read and the spend. Telling them an item is sold out
> is the wrong fact and sends them to the wrong page.
>
> **Model it on the re-quote precedent already in this handler.** `requote`
> (`internal/cart/handler.go:453`) answers exactly this shape for the shipping
> surcharge: the figure moved, so the checkout re-renders at `422` naming the real
> figure, the form carries the new value, and the second submission goes through.
> `pages.CheckoutView.Repriced` is the field, rendered at `cart.templ:216` as
> `ui-alert--info` with `role="status"`.
>
> Add a `cart.ErrCreditChanged` sentinel and a sibling view field carrying the
> refreshed balance, with a new `i18n.Message` pair naming the figure. Do not
> reuse `Repriced` — two causes behind one string is what this whole finding is
> about.


## Root cause

A `%w: %w` wrap that is CATEGORY-ASSIGNING is being used where only a CATEGORY-PRESERVING wrap is sound. `fmt.Errorf("%w: %w", ErrNotEnough, err)` keeps the cause in the chain — which is why this looks harmless on the line — but it also makes `errors.Is(err, ErrNotEnough)` true for causes that have nothing to do with the balance, and `errors.Is` is the only thing every handler asks. The preserved cause is never read by anything: no arm that matches a sentinel logs, so the chain carries the truth to a place with no reader.

The underlying design error is that these call sites classify by CALL SITE ("this statement is the one that can be short of points / short of stock") instead of by the DATABASE'S OWN ANSWER. goen's whole schema exists to make the second possible: CLAUDE.md — "Each raises with an explicit `CONSTRAINT` name so a caller — and a test — can say which rule refused." `hold_inventory` raises `inventory_never_negative` (migrations/001:853), `redeem_loyalty_points`'s trigger raises `loyalty_never_negative` (001:4199). The discriminator is sitting there, typed, in `pgconn.PgError.ConstraintName`, and the repository already binds to it in 14 places — including internal/cart/coupon.go:130, twelve lines from the defective `spendCredit`.

So this is mistake #32's shape one turn further on. #32 was "the error text cannot tell you which rule refused." This is "the call site cannot tell you WHETHER a rule refused at all." Both are answered by the same field, and CLAUDE.md's own summary of every guard in this repository — "every one of them asks what is ABSENT" — explains why nothing caught it: no sentinel is missing, no field unassigned, no table doorless. Two correct-looking halves disagree about what an error means.

Secondary: the handler arms treat "a domain outcome" as "nothing to record". That is right for a genuine domain outcome and is what makes the misclassification total rather than merely misleading — a WarnContext on those arms, as internal/admin already does, would have left the truth visible even with the wrap in place.


## Reproduction — the evidence this rests on

EXECUTED, twice, against the live dev database. Both probe files were deleted; `git status --porcelain` is empty.

--- SITE 1: internal/loyalty/store.go:72-74 ---
```go
	cents, err := s.q.RedeemPoints(ctx, db.RedeemPointsParams{...})
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrNotEnough, err)   // line 73
	}
```
Probe (temporary `internal/loyalty/zzprobe_test.go`, package `loyalty`, now deleted): a fixture account holding 500 points; a pool whose `RuntimeParams["statement_timeout"] = "1500"`; a SECOND pgx session holding `LOCK TABLE store_credit_entries IN ACCESS EXCLUSIVE MODE`. `redeem_loyalty_points` (migrations/001, line 4529) writes `store_credit_entries` as its second statement, and `PointsBalance` does not touch that table — so the fault lands on exactly the `RedeemPoints` call, after `Balance` has already succeeded.

Output:
```
zzprobe_test.go:65: balance before: 500 points
zzprobe_test.go:85: Redeem returned after 1.503s: cents=0 err=loyalty: not enough points: ERROR: canceling statement due to statement timeout (SQLSTATE 57014)
zzprobe_test.go:86: errors.Is(err, ErrNotEnough) = true
zzprobe_test.go:92: REPRODUCED: an infrastructure fault is reported as ErrNotEnough with 500 points on the ledger
```
What the customer gets: internal/loyalty/handler.go:70 `case errors.Is(err, ErrNotEnough), errors.Is(err, ErrNoAccount): http.Redirect(w, r, "/account/points?short=1", ...)` → `noticeFor` → `i18n.KeyPointsShort` (internal/i18n/chrome_account.go:313) = 「點數不夠 —— 可能剛剛有一筆到期了。」 / "Not enough points — some may have just expired." The page INVENTS an explanation for a database timeout, to a customer whose ledger holds 500 points. Nothing logs: that case arm has no `h.log` call; only `default:` at handler.go:74 logs.

--- SITE 2: internal/cart/store.go:632-634 (holdOrderStock) ---
```go
		}); err != nil {
			return fmt.Errorf("%w: %w", ErrUnavailable, err)   // line 633
		}
```
Probe (temporary `internal/cart/zzprobe_test.go`, now deleted): real cart via `Store.Create`/`Store.Add` on seeded variant `00000027-…0027` (12 in stock), real shipping version `ffff0002-…0001`, same statement_timeout pool, second session holding `LOCK TABLE inventory_movements IN ACCESS EXCLUSIVE MODE` (`hold_inventory` → `record_inventory_movement` writes it; nothing earlier in `PlaceOrder` does).

Output:
```
zzprobe_test.go:73: PlaceOrder returned after 1.535s: number="" err=cart: variant unavailable: ERROR: canceling statement due to statement timeout (SQLSTATE 57014)
zzprobe_test.go:74: errors.Is(err, ErrUnavailable) = true
```
What the customer gets: internal/cart/handler.go:375-378 `case errors.Is(err, ErrUnavailable): // Something sold out between the cart page and this write; the cart page says which line and why. http.Redirect(w, r, "/cart", http.StatusSeeOther)`. Nothing logs. The stock never moved, so the cart page re-renders every line as available with no message at all — a silent bounce off the checkout button on the money path.

--- OTHER SITES (read, not separately executed) ---
Same shape, same silence:
- internal/cart/store.go:715 (`spendCredit`, `post_store_credit`) → same `ErrUnavailable` arm at handler.go:375.
- internal/returns/store.go:162 `return fmt.Errorf("%w: %w", ErrInvalid, lineErr)` → internal/returns/handler.go:92 → `KeyReturnNeedsReason` 「請填寫退貨原因,並至少選擇一件商品。」 with no log. A DB fault tells a customer to fill in a reason they already filled in — and CLAUDE.md records that a blank reason is legal under 消保法 §19, so the message is wrong twice.
- internal/admin/product.go:153 `s.q.AdminProduct` (a plain `:one` read) → `ErrRefused` → internal/admin/handler.go:761 `h.notFound(w, r)` with no log, and internal/admin/handler.go:1550-1552 `if err != nil { h.notFound(w, r); return }` which swallows EVERY error with no log. Database down → the back office says "no such product".

Same misclassification but NOT silent (~50 sites): every other `%w: %w` with `ErrRefused` in internal/admin (product.go, shipping.go, store.go, campaign.go, taxonomy.go, coupon.go, image.go, tiers.go, banner.go, faq.go, hero.go, delivery.go, question.go). I checked all 17 `ErrRefused` handler arms: every one except handler.go:761 and 1564 logs the wrapped error in full at `WarnContext` first (e.g. handler.go:212 "order transition refused"). An operator DOES see the truth there. internal/media/decode.go:31 and internal/admin/image.go:39,56 are likewise logged at Warn (handler.go:1147, 1156, 1186).

REFUTED as a site: internal/payment/stripe.go:191 `fmt.Errorf("%w: %w", ErrBadSignature, err)` — `webhook.ConstructEventWithOptions` fails only on a bad signature or an unparseable body. No infrastructure error reaches it.

RULE CHECK (as asked): this is a RULE VIOLATION, not a new policy question. `.claude/rules/error-handling.md`, "Sentinel Error Design", states verbatim: "Map pgx errors in store: `pgx.ErrNoRows`→`ErrNotFound`, unique violation (23505)→`ErrConflict`" and "NEVER create sentinels for infrastructure errors (timeouts, connection failures)". CLAUDE.md mistake #32 supplies the discriminator ("Bind to `PgError.ConstraintName`"), and the repository already applies it correctly in 14 places — including internal/cart/coupon.go:127-137, TWELVE LINES from the defective `spendCredit`, with the right comment above it. internal/loyalty/store.go:40-45 does it correctly in `Balance` and then abandons it at line 73 of the same file. Nothing in CLAUDE.md or docs/roadmap.md records this as a decision.

DB residue, reported rather than hidden: the loyalty probe's fixture row cannot be removed — `loyalty_entries` is append-only (`forbid_change()` refuses DELETE even to the owner, and `loyalty_entries_account_id_fkey` is RESTRICT). One dormant user `probe-loyalty-0699c708-…@goen.invalid` remains with its account; I posted an offsetting `-500 'redeem'` entry so `loyalty_balances` reads 0. Nothing else was left: no carts, no cart_items, no checkout_attempts, no orders, no reservations.


## Blast radius

Silent sites, in descending order of cost.

1. CHECKOUT (internal/cart/store.go:633 and :715). Every customer, on the money path. Any transient database condition during `PlaceOrder` — statement timeout, connection reset, failover, lock wait, `pg_terminate_backend`, pool exhaustion — bounces the customer to /cart with no message and no log line. The cart still shows everything in stock, so the customer sees a checkout button that does nothing. The shop sees NOTHING: no error log, no failed-order row (the transaction rolled back), no `/admin/health` figure. This is a total-outage-shaped failure that is invisible from both ends; a partial database degradation reads as "conversion dropped" with no diagnostic anywhere. Frequency scales with database health, not with traffic mix.

2. POINTS (internal/loyalty/store.go:73). Every redemption attempt during a database fault. The customer is told 「點數不夠 —— 可能剛剛有一筆到期了。」 — an affirmative, false factual claim about their own balance, complete with a fabricated cause. Reproduced with 500 points on the ledger and a 100-point request. They will retry, get the same sentence, and reasonably conclude the shop ate their points. No log, so a support ticket cannot be answered.

3. RETURNS (internal/returns/store.go:162). A customer exercising the unwaivable 消保法 §19 right is told to supply a reason. CLAUDE.md records that demanding a reason was itself a defect ("A blank return reason is legal for the same statute… a customer exercising an unwaivable right could not submit the form"). The fix removed the demand; this branch re-creates the same dead end whenever the database hiccups, and logs nothing. Statutory exposure, not just annoyance: §19 III extends the window while the customer is obstructed.

4. BACK OFFICE PRODUCT PAGE (internal/admin/product.go:153 → handler.go:761, 1550). Staff get a 404 for a product that exists. handler.go:1550 is the worse of the two: it discards the error entirely with no log, so a failed 422 re-render of the edit form looks like the product vanished mid-edit.

Not silent (~50 admin `ErrRefused` sites plus media): staff are told "refused" instead of "the database is unreachable", which sends them looking for a business rule that does not exist — but the real error IS in the log at Warn. Wrong, cheap to fix, and materially smaller than the four above. The stated mechanism does apply to them; the stated consequence ("does anything log the real error") does not.

The cross-cutting cost: every one of these paths reports a HEALTHY 200/303 to any external monitor. `/readyz` pings the pools, but a database that answers a ping and times out a write is exactly the state these branches disguise.


## Fix

Principle, to be applied at each site: a domain sentinel is returned ONLY when `errors.AsType[*pgconn.PgError](err)` succeeds AND `pgErr.ConstraintName` is one of a named set (or, for a read, when `errors.Is(err, pgx.ErrNoRows)`). Every other error is returned wrapped with context and NOTHING else — `fmt.Errorf("<what was happening> %s: %w", input, err)` — so it falls to each handler's `default:` arm, which already logs at Error and renders 500. Do not add a new sentinel for infrastructure; the absence of a sentinel IS the signal. Follow the existing idiom exactly — internal/cart/coupon.go:127-137 and internal/admin/taxonomy.go:222-227 are the models. No new shared package (`.claude/rules/package-organization.md` blocks `util`/`common`, and a one-file package is forbidden); each feature keeps its own predicate, as the 14 existing call sites already do.

1. internal/loyalty/store.go, `Redeem`, replace line 73:
```go
	if err != nil {
		// Bound to the constraint NAME, never to the message: pgconn renders a
		// PgError as severity + message + SQLSTATE, and the name is not in it.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "loyalty_never_negative" {
			// The Go check above reads the balance without a lock; this is the
			// same answer taken under redeem_loyalty_points' own lock.
			return 0, ErrNotEnough
		}
		return 0, fmt.Errorf("redeem %d points: %w", points, err)
	}
```
Add `"github.com/jackc/pgx/v5/pgconn"` to the imports. Do NOT map `loyalty_entries_points_nonzero` (001:4514) to `ErrNotEnough` — `Redeem` already refuses a non-positive request in Go, so reaching it is a programming error and belongs in the 500 with a log.

2. internal/cart/store.go, `holdOrderStock`, replace line 633:
```go
		}); err != nil {
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
				pgErr.ConstraintName == "inventory_never_negative" {
				return ErrUnavailable
			}
			return fmt.Errorf("hold %d of variant %s: %w", l.Quantity, l.VariantID, err)
		}
```
`inventory_never_negative` is raised by `record_inventory_movement` at migrations/001:853 and is the ONLY oversell answer `hold_inventory` can give. `inventory_reservations_quantity_positive` (001:915) is unreachable — `subtotalOf` and the cart's own quantity rules keep it positive — so it must reach the 500, not the cart page.

3. internal/cart/store.go, `spendCredit`, replace line 715 the same way, mapping only `store_credit_never_negative`:
```go
	}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "store_credit_never_negative" {
			return ErrUnavailable
		}
		return fmt.Errorf("spend %d cents of credit on order %s: %w", spend, orderID, err)
	}
```
(The balance read four lines above is already correct: `fmt.Errorf("read store credit: %w", err)`.)

4. internal/returns/store.go, replace line 162 with a name-bound map. Confirm the constraint set first with `\d+ return_request_lines` — the raised rule name is **`return_within_shipment`**, which is what `PgError.ConstraintName` carries (`return_lines_within_purchase` is the trigger FUNCTION, not the name in its `USING ... CONSTRAINT =` clause) — and map it to the new **`ErrTooMany`** per the RESOLVED block at the top of this file, NOT to `ErrNotReturnable` and NOT to `ErrInvalid`. Everything else: `fmt.Errorf("add return line for order line %s: %w", lineID, err)`.

5. internal/admin/product.go:153, `Store.Product` — this is a `:one` READ, so there is no constraint to name:
```go
	p, err := s.q.AdminProduct(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.AdminProductView{}, ErrNotFound
		}
		return pages.AdminProductView{}, fmt.Errorf("read product %s: %w", slug, err)
	}
```
Then internal/admin/handler.go:761 becomes `if errors.Is(err, ErrNotFound)` (the `default:` two lines below already logs and 500s), and internal/admin/handler.go:1549-1552 MUST stop swallowing:
```go
	view, err := h.store.Product(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "read product for re-render", "slug", slug, "error", err)
		h.serverError(w, r)
		return
	}
```

6. Separately and cheaply, for the ~50 non-silent admin sites: leave the `ErrRefused` wraps as they are for now (they log in full, and narrowing 50 call sites is a different change with its own risk), but fix the two that do NOT log — handler.go:761 is covered by item 5, and handler.go:1564 `attachReason` is already preceded by a Warn at handler.go:1156/1186, so it needs nothing. Queue the ~50 by name as a follow-up rather than mixing it into this fix; `ErrRefused`'s doc comment at internal/admin/admin.go:21-23 ("a write the database declined; its message is the database's own") is a claim that becomes false the moment a timeout wears it, and that is the sentence the follow-up has to change.

Do not touch internal/db (sqlc-generated) or any `*_templ.go`. Run `make verify` and `make test-integration`.


## The lock, and how to see it fail first

Two behavioural locks, one per feature, each proven RED by restoring the exact line the fix replaces. I executed both mechanisms already (see reproduction), so the RED is not a prediction.

LOCK A — `internal/loyalty/integration_test.go` (build tag `integration`, package `loyalty_test`), `TestAnInfrastructureFailureIsNotReportedAsAnEmptyBalance`:
1. Fixture: a user, a `store_credit_accounts` row, and a `loyalty_entries` award of 500 points with `expires_on = current_date + 30`. Create it in the test, not from the seed (#26).
2. Build a SECOND `pgxpool` over the container URL with `cfg.ConnConfig.RuntimeParams["statement_timeout"] = "1500"`, and a Store over it.
3. Open a THIRD, plain `pgx.Connect`, `Begin`, and `LOCK TABLE store_credit_entries IN ACCESS EXCLUSIVE MODE`. Hold it. This is the #9 rule: the fault must be produced by a transaction held open across the call, not by two goroutines racing.
4. Call `Redeem(ctx, userID, 100)` — a request the balance comfortably covers.
5. Assert THREE things, and the third is what makes the lock more than a smoke test:
   - `!errors.Is(err, ErrNotEnough)` — with the message "a statement timeout is not a statement about the customer's balance";
   - `errors.AsType[*pgconn.PgError](err)` succeeds with `pgErr.Code == "57014"`, i.e. the cause survived the wrap;
   - `!errors.Is(err, ErrTooSmall) && !errors.Is(err, ErrNoAccount)` — no OTHER domain sentinel picked it up either.
6. Roll back the blocker.
MUTATION, to be run and recorded: restore `return 0, fmt.Errorf("%w: %w", ErrNotEnough, err)` at internal/loyalty/store.go:73 and re-run. Assertion 1 must FAIL. I have already executed exactly this state and captured `errors.Is(err, ErrNotEnough) = true` with 500 points on the ledger, so the mutation is known to apply — but per CLAUDE.md #6 and mode #3, the mutation must be SEEN to apply in the tree, not assumed: confirm the edited line is the one compiled (`go test -count=1`, and check the assertion message changes), because `gofmt` padding has silently swallowed a mutation in this repository before.
Note the fixture trap: a test written against an account with a balance of ZERO would pass with the mutation in place, because `points > balance` at store.go:64 returns `ErrNotEnough` before the query is ever reached. The balance MUST exceed the request, or the lock proves nothing — this is the #33 shape (a fixture that does not reach the state under test).
Also note that no existing test covers line 73: `internal/loyalty/integration_test.go:192` asserts `ErrNotEnough` for expired points, which `loyalty_balances` excludes, so that path returns at store.go:65 and the defective line stays unexercised.

LOCK B — `internal/cart/integration_test.go`, `TestAnInfrastructureFailureIsNotReportedAsSoldOut`:
Same three-connection shape. Fixture: a real cart with one line on an active variant with ample stock, and an active shipping version. Blocker holds `LOCK TABLE inventory_movements IN ACCESS EXCLUSIVE MODE` (`hold_inventory` → `record_inventory_movement` writes it; `CartLines` and `claimCheckoutKey` do not, so the fault lands on the hold and not earlier — verify that by asserting the error is a 57014 and not a "read cart lines" wrap). Call `PlaceOrder`. Assert `!errors.Is(err, ErrUnavailable)` and that the `PgError` survives with code 57014.
MUTATION: restore `return fmt.Errorf("%w: %w", ErrUnavailable, err)` at internal/cart/store.go:633 → must go RED. Executed: `err=cart: variant unavailable: ERROR: canceling statement due to statement timeout (SQLSTATE 57014)`, `errors.Is(err, ErrUnavailable) = true`.
Add a POSITIVE twin in the same test file, or the lock is one-directional and a fix that simply stops returning `ErrUnavailable` at all would pass it: order more units than exist so `inventory_never_negative` genuinely fires, and assert `errors.Is(err, ErrUnavailable)` IS true. Bind that assertion to the constraint name, not to the fact that an error happened (#8).

LOCK C — for internal/admin/product.go:153, a cheaper one: call `Store.Product(ctx, "no-such-slug")` and assert `errors.Is(err, ErrNotFound)` and `!errors.Is(err, ErrRefused)`. Mutation: restore the `ErrRefused` wrap → RED.

Do NOT attempt a static/AST guard as the primary lock. The corpus ("a sentinel returned from a branch holding a bare `err`") cannot be derived without deciding which wraps are legitimate, and a guard that is coarse here would refuse internal/cart/coupon.go:137, which is correct. If a guard is wanted later, the honest derivable rule is narrower: every `fmt.Errorf("%w: %w", <package sentinel>, err)` must be lexically preceded in the same block by a `ConstraintName` comparison or an `errors.Is(err, pgx.ErrNoRows)`. Queue it by name; it is not what makes this fix safe.
