# Cold review: the whole of goen

You are reviewing goen entire — not a diff, not a release. Every file in this
repository is in scope, including the ones nobody has touched in months, which
are the ones least likely to have been looked at twice.

## What it is, and what it is for

A Traditional-Chinese storefront for 3C goods. One Go binary serves the
storefront, the customer account and `/admin`; pages are rendered server-side
with `templ`; PostgreSQL holds everything including the catalogue's photography.
It is a demonstration and reference project, so **the software engineering is
part of what is being demonstrated** — a defect in how a rule is enforced counts
as much as a defect a customer would meet.

| | |
|---|---|
| Go source (excluding generated) | ~33,400 lines across 25 packages |
| Tests | ~32,800 lines, 665 top-level test functions |
| Templates | ~7,900 lines of `templ` |
| Schema | one 4,509-line migration: 68 tables, 5 views, 88 functions, 8 roles |
| HTTP routes | 173 |
| Chrome strings | 1,430 i18n keys, two locales |

## Read the code before you read the claims

`CLAUDE.md` is 2,946 lines and `README.md` is 282. Together they are the most
detailed account of this system that exists — and they are **claims**, not
evidence. Form your findings from the code, the schema and the running site
first; consult the prose afterwards, and then chiefly to ask a different
question: *is this still true?*

That is not a stylistic preference. This project has three recorded cases of a
comment or document asserting an enforcement that did not exist:

- `product_search_documents` carried a note claiming it had "exactly one
  writer". It had none, and no reader. The whole table was deleted.
- `Store.Remove` shipped with a comment saying "another admin does this" and no
  caller, so an admin who lost their 2FA phone was locked out permanently.
- A header comment named `TestTopNavPointsAtRealCategories` as the guard keeping
  two copies of the category list in step. **That test was never written.**

A fourth was found last week: `CLAUDE.md` said its schema counts were "measured
… rather than counted by hand", and three of the six had drifted. Treat every
sentence of the form *"X is enforced by Y"* as a hypothesis with a name you can
grep for.

## Use the site, do not only read it

`make db-up && make migrate-up && make db-seed && make run` brings it up on
`127.0.0.1:9700`; `make check-layout` drives a real browser over 85 viewports.
The back office is at `/admin` behind TOTP step-up.

The most expensive defect this project has shipped was found by *using* it: a
whole comparison feature — decision record, localized spec table, a query, its
own lock — that no path on the site could reach with more than one product.
Every guard was green. `TestEveryHardCodedLinkResolvesToARoute` passed, because
it asks whether a link **resolves**, never whether a state is **reachable**.

Place an order. Cancel it. Return something. Register a warranty. Run the shop
from `/admin` and try to make it contradict itself.

## Where the machinery is structurally blind

Stated so you spend effort where the guards cannot reach — not so you trust them
elsewhere.

- **Every completeness guard here asks about ABSENCE**: a table with no writer, a
  table nobody reads, a column nobody mentions, a view-model field nobody
  assigns, an i18n key nothing renders. None of them can see **two correct
  halves that disagree**. Six recorded mistakes are exactly that shape, and
  every one was found by a person: a payment page charging gross while the
  constraint demanded net; a session flag that reopened a decision made ten lines
  above it; a comment naming a forbidden state whose predicate still allowed it;
  a warranty clock started at dispatch under a comment saying delivery; a tile
  and a product page quoting two different prices for one product.
- **Linked is not reachable.** See above.
- **A test written from the implementation asserts what the code does.** Three
  findings in an earlier round were each *locked in* by a passing test, so each
  fix had to change a green test. Assume some of the 665 are in that state.
- **A fixture that reaches past the application is a fixture for a claim nobody
  is testing.** Several have been found seeding tables the app cannot write.

## Attack in this order

1. **Money and stock.** `internal/payment`, `internal/cart`, `internal/returns`,
   the `SECURITY DEFINER` posting functions, `order_amount_owed`. Anywhere one
   number is computed in two places, or a webhook is trusted for more than "what
   happened".
2. **Authorisation and privacy.** `order_access_grants`, the `store` / `admin` /
   `reporting` / `maintenance` role split and its column-level grants,
   `RequireStaff` / `RequireAdmin` / `StaffOnly`, `erase_user`. Ask whether the
   guard on a route is weaker than the thing it protects, and whether anything a
   customer wrote can outlive their erasure.
3. **The state machines.** Order status, reservation lifecycle, return →
   refund → restock, invoice void/allowance. Look for a status that claims work
   is finished with nothing making it true, and for a transition whose side
   effects live outside the transition.
4. **Concurrency.** ~39 rule triggers claim to lock their aggregate root before
   reading. Check the claim per trigger. Two writers, both passing, is the
   failure this schema exists to prevent.
5. **The tests themselves.** They are review targets. For each guard you meet:
   *what mutation would this not catch?* Say where a lock is weaker than the
   error it sits beside.
6. **The prose.** Find a stated number, list or limit that nothing holds to the
   code.

## Decisions, not defects

Some things are deliberate and recorded with reasons: triggers used for
cross-row integrity, `updated_at` by trigger, `uuidv7()` keys, amending `001` in
place while nothing is deployed, images in PostgreSQL, no observability yet,
card-only payment methods, 超商取貨 carrying no 離島 surcharge. **Challenge the
reasoning if it is wrong — but say which recorded reason you are refuting**,
rather than reporting the decision as an oversight. A finding that a recorded
trade-off has stopped being true is among the most valuable kinds here.

## What a useful finding looks like

- The **mechanism**: inputs, and the wrong outcome. Not a smell.
- **Which existing guard should have caught it, and why it did not.** A finding
  that also explains the gap in the machinery is worth several that do not.
- If you cannot construct the failing path, mark it uncertain and say so.
  Every finding must reach one of three states before merge — fixed, queued by
  name, or refused in writing — so an unproven mechanism naming a real question
  is worth more than silence.

Do not soften findings, and do not batch-approve areas you did not read. A
too-large or skipped notice is answered, never absorbed: say which parts you did
not reach.
