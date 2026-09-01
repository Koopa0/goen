# P2 — Walk the back office, as the person who runs the shop

> Self-contained brief. Assume no context beyond this file and the repository.
> **For:** Codex · **Run early:** this and P1 are the only two that can find defects in
> what already exists.

You are reviewing `goen`, a Traditional-Chinese 3C storefront. One Go binary, Go 1.27,
`net/http` (no framework), `templ` server-rendered HTML, PostgreSQL 18 via pgx + sqlc,
Stripe hosted Checkout, htmx only as progressive enhancement. Read `CLAUDE.md` at the
repository root first — it is the design record.

```bash
make db-up && make migrate-up && make db-seed && make run   # 127.0.0.1:9700
```

The back office is `/admin`, behind a password AND a TOTP second factor
(`GOEN_TOTP_KEY` must be set; it is now required whenever cookies are secure). Roles
are `staff` and `admin`, and **only `admin` may reach `/admin/staff`** — that
distinction is new and lightly exercised.

**Nobody has walked these flows.** `check-layout` renders the pages; no one has run the
shop through them.

## Run the shop

Set yourself up with an admin account, enrol a TOTP credential, then:

1. **Onboard a product from nothing**: brand → category → product (draft) → option axes
   and values → variants → specs → images (upload, and reuse) → warranty term → publish.
   Then find it on the storefront, on `/compare`, and in search — in **both locales**.
2. **Sell it**: place an order on the storefront, then in the back office find it, read
   it, correct the delivery address, ship it with a tracking number, mark it delivered.
3. **Unsell it**: cancel one order; take a return on another and decide it, with a
   refund. Check the customer's own order page tells the truth at each step.
4. **Money and stock**: grant store credit; adjust stock and read the ledger at
   `/admin/stock/{sku}`; issue a coupon and redeem it; run a campaign.
5. **The shop's voice**: hero, promotional banner, FAQ, shipping methods and zones,
   membership tiers, taxonomy — including the English fields, and what the storefront
   does when an English field is empty.
6. **People**: add a colleague, promote, revoke, remove a second factor. Try to promote
   yourself; try to revoke the last admin.
7. **Answer somebody**: `/admin/questions`, `/admin/reviews`, `/admin/messages`.
8. **Read the numbers**: `/admin/reports`, `/admin/health`, `/admin/customers`.

For each, report: does it work, does it say what happened, and **is there any step that
requires SQL because no page offers it?** That last question has found five features in
this repository already — the pattern is a table the schema describes, the docs promise,
and nothing can write.

Also judge it as a working environment: how many clicks to answer "where is order
GO-…?", "why does this SKU say 4?", "what has this customer spent?" Is the information
where the person needs it, or one page away?

## Two specific things to attack

They are new and thinly tested:

- **`RequireAdmin`**: verify a plain `staff` account gets a 404 on all four
  `/admin/staff` routes and reaches everything else, and that an `admin` reaches all.
- **The refund state machine**: `/admin/health` now lists refunds that are pending,
  requires_action or failed. Force each state (Stripe test mode makes this possible)
  and check the page tells an operator something they can act on.

## Report

File:line for every claim about the code; screenshots or exact URLs for every claim
about a page. Rank by what would actually stop a shop being run.

Then end with the same three lists every brief in this set ends with:

1. **Verified by running** — executed, result observed.
2. **Read but could not verify** — a view formed from the code or the documentation,
   without running it.
3. **Did not look at** — in scope on paper, not examined.
