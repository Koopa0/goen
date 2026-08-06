# Third-party acceptance: goen

You are accepting a project you did not build. Everything below is written by the
builder, which means **it is a claim, not evidence** — your job is to turn the claims
you can into facts, and to say plainly which ones you could not.

## What this is

`goen` — a Traditional-Chinese 3C storefront. One Go binary serves the storefront,
the customer account and `/admin`. Go 1.26.5, `net/http` (no framework), `templ` for
server-rendered HTML, PostgreSQL 18 via pgx + sqlc, Stripe hosted Checkout, htmx only
as progressive enhancement.

**It is a demo/portfolio project, not a live shop.** Calibrate severity accordingly:
a missing enterprise feature is not a finding; a way to lose somebody's money, take
stock that is not there, or read another customer's data is.

Read `CLAUDE.md` at the repository root first. It is long and it is the design
record: every unusual decision is written there with the reasoning and, in most
cases, the defect that produced it.

## Set it up

```bash
make db-up                 # PostgreSQL 18 in Docker on 127.0.0.1:5433
make migrate-up            # migrations/001_initial_schema.up.sql
make db-seed               # dev catalogue
make run                   # 127.0.0.1:9700
```

`GOEN_DATABASE_URL` is required and has no default. `.env` is read by the Makefile
in development. Stripe and TOTP keys may be empty — the features then say so rather
than half-working.

## Run the gates yourself. Do not read the transcripts.

```bash
make verify            # fmt, templ, squawk, sqlc-check, vet, lint, race tests. No Docker.
make test-integration  # the schema conformance suite + every feature's integration tests
make check-layout      # 105 rows in a real headless Chrome; needs `make run` first
```

Run `make verify` **twice**. A gate that repairs what it checks passes on the second
run and not the first, and this repository has been bitten by exactly that.

## The specific things the builder could NOT prove

These are the honest gaps in the builder's own verification. Start here.

1. **Two behaviours are recorded as "mutation green"** — the builder claims they
   cannot be locked by a behavioural test and documented that instead of dressing it
   up. Judge whether that call is right, or whether a test was simply not found:
   - the constant-time compare in `internal/twofactor` (a timing test would be
     flakier than the guarantee);
   - the clock-skew fix in `/admin/messages` (`floor(extract(epoch FROM now() -
     created_at) / 86400)`) — neither implementation is distinguishable from the
     other except by skew nobody controls.
2. **Every test connects as the database OWNER**, who is subject to no `REVOKE`. The
   privilege model (`store` / `admin` / `reporting` / `maintenance` roles, and
   `SECURITY DEFINER` functions as the only door to money, stock and the ledgers) is
   therefore asserted by `coverage_integration_test.go` under an assumed role, not by
   the way the suite normally connects. **Try to write stock, a payment, a ledger row
   or an audit row directly as `store` and as `admin`.** If you get through, that is
   the most serious finding available in this codebase.
3. **~15 mechanised guards were added late** (`internal/db/*_test.go`,
   `internal/i18n/hardcoded_test.go`, `internal/ui/pages/fields_test.go`, and the
   accessibility rules in `scripts/check-layout.mjs`). Each has an allowlist, and
   **every allowlist entry is a claim that somebody looked and agreed**. Three of
   these guards were, at first, satisfied by something other than what they were
   meant to check. Assume a fourth is.
4. **No load test, no deployment, no second reader.** Nothing here has run anywhere
   but a laptop.

## Where the money and the stock are

If you have time for only one pass, spend it here.

- `internal/payment` — Stripe webhook. The claim is that only the signature-verified
  webhook marks an order paid, that the claim row and the capture share one
  transaction (so a failed capture leaves the event reprocessable), and that the
  order is identified by goen's own payment row rather than by anything in the event.
- `internal/cart` — `PlaceOrder`: one transaction writing the header, lines, delivery
  details, the stock hold, the store-credit spend and the coupon redemption; an
  idempotency key so a double-click is one order; the shipping fee recomputed from
  the version rather than trusted from the form.
- `internal/admin` — refunds (`splitRefund`: card first, store credit last), store
  credit grants, stock adjustments, the return decision.
- `migrations/001_initial_schema.up.sql` — ~26 rule triggers, 130+ CHECKs. The claim
  is that anything that would corrupt money, stock or history is refused by
  PostgreSQL, where there is no second write path. Look for a rule that reads a row
  it has not locked.

## Where a customer's data is

- `internal/account` — argon2id, SHA-256 session digests, `erase_user`, the password
  reset (the token is spent in the UPDATE's own WHERE clause), the email-change flow.
- Order access: the browser that placed it, the account that owns it, or
  `/orders/find` with the number AND the email. Order numbers come off a per-day
  counter and are guessable — the address is the secret half.
- `/admin/customers` audits the READ. Check the audit row does not itself contain
  personal data: `audit_events` is append-only and `erase_user` does not reach it.

## How to report

Follow `.claude/rules/review-process.md`, which this project holds itself to:

- **Form your findings from the code before reading the builder's summaries.** A
  report hands you its own frame.
- Every finding gets a **file:line**, a **concrete failure scenario** (inputs → wrong
  output), and a severity. "This looks fragile" is not a finding.
- If a finding's mechanism is wrong but the point stands, say both.
- Rank most severe first. An empty list is a legitimate result; padding it is not.

Explicitly tell me at the end:

1. What you **verified by running** (and what the command printed).
2. What you **read but could not verify**.
3. What you **did not look at at all**.

That third list is the most useful thing you can give me, and it is the one a review
usually leaves out.
