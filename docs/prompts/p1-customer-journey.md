# P1 — Walk the customer journey, and design the account around what you find

> Self-contained brief. Assume no context beyond this file and the repository.
> **For:** Codex · **Run before:** everything except P2 — this and P2 are the only two
> that can find defects in what already exists.

You are reviewing `goen`, a Traditional-Chinese 3C storefront. One Go binary, Go 1.26,
`net/http` (no framework), `templ` server-rendered HTML, PostgreSQL 18 via pgx + sqlc,
Stripe hosted Checkout, htmx only as progressive enhancement. Read `CLAUDE.md` at the
repository root first — it is the design record.

```bash
make db-up && make migrate-up && make db-seed && make run   # 127.0.0.1:9700
```

## Part 1 — walk it, as a person

**Nobody has ever done this.** The only browser automation that has run is
`make check-layout`, which measures geometry and seven decidable accessibility rules
over 109 viewport rows. No one has gone from browsing to a completed order and read
what the screens actually say.

Do it twice, in a real browser, at 375px and at 1440px:

- **As a guest**: land on `/`, browse a category, use search, open a product, choose a
  variant, add to cart, check out, choose 宅配到府 and then 超商取貨, pay (Stripe test
  mode — set `GOEN_STRIPE_SECRET_KEY`/`GOEN_STRIPE_WEBHOOK_SECRET` and run
  `stripe listen`), reach the confirmation, then find the order again at
  `/orders/find`, and open a return.
- **As a registered customer**: register, verify the email (`/verify`), do the same
  purchase, then use the account: orders, points, wishlist, warranty, addresses,
  profile, password, email change.
- **With JavaScript disabled**, for both. The project's central rule is that every
  mutation is a plain form that works with scripting off. Find where that is false.

Report every place where the journey **stalls, lies, or makes you retype something you
already gave**. Be specific: a screenshot reference, the URL, what you expected, what
happened. Dead ends, missing links, a button that appears to do nothing, a validation
message that does not say what to fix, a page that says "0 items" when it should say
why, a form that loses what you typed on refusal.

## Part 2 — the account, as an information problem

`/account` exists and links to orders, points, wishlist, warranty, addresses, profile.
Judge it as a **design** rather than a checklist:

- What does a customer actually come here to find, and how many clicks is it?
- What is missing that a 3C shop's customer would expect — order tracking status,
  invoice/發票 record, return status, a reorder path, a saved payment method, a
  notification preference?
- Is anything here that should not be?

## Part 3 — the address problem, specifically

The user's stated requirement: **a returning customer must not retype an address, and
must be able to decide whether to use a saved one.**

`internal/cart` has `SavedAddresses`, and `CheckoutLink` carries the delivery-method
choice alongside an address choice. Read how the checkout currently offers them, then
answer:

- Does a returning customer actually avoid retyping today? Trace it and say yes or no.
- The address chooser and the delivery-method chooser are both LINKS carrying state in
  the URL (so they work with scripting off). Does composing them work in every order of
  operations, or can one silently reset the other?
- What happens on a 422 — are the saved-address choice and the typed values both
  preserved?
- 超商取貨 collects a 門市 (brand, 店號, 店名) instead of an address. **Is there a saved
  pickup store, the way there is a saved address?** If not, is that a gap worth
  closing, and where would it live?
- Design the interaction you would actually want, including the case where somebody
  wants to ship to a NEW address once without saving it.

## Part 4 — Google sign-in

**There is no OAuth code in this repository at all.** `user_identities` exists as a
table with a foreign key and is cleaned by `erase_user`, and nothing writes it — it is
one of three tables documented as waiting for credentials.

Answer, with the repository's own constraints in view (no framework, no new heavy
dependency without measuring `go list -m all | wc -l` before and after, every mutation
a plain form that works without scripting):

- What does adding Google sign-in actually cost here — which library, how many modules,
  what routes, what session handling, what CSRF/state handling?
- How does it compose with the existing password account? A customer who registered
  with a password and then signs in with Google at the same address: one account or
  two, and what does the schema already force?
- `users.password_hash` is nullable and a staff account is deliberately created without
  one. Does an OAuth-only account collide with anything that assumes a password exists
  — `/forgot`, the password-change flow, the email-change flow that demands the current
  password?
- What does it mean for `email_verified_at`? Google asserts a verified address; goen's
  own flow proves one. Are those the same fact?
- Recommend: build it, or not, and why. A clear "not worth it, here is what it buys and
  costs" is a legitimate answer.

## Report

File:line for every claim about the code. Screenshots or exact URLs for every claim
about the journey. Rank findings by what a customer would actually feel.

Then end with the same three lists every brief in this set ends with:

1. **Verified by running** — executed, result observed.
2. **Read but could not verify** — a view formed from the code or the documentation,
   without running it.
3. **Did not look at** — in scope on paper, not examined.
