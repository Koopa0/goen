# P5 — What is not built, and what should be

> Self-contained brief. Assume no context beyond this file and the repository.
> **For:** Codex · **Order:** any time.

`goen` is a Traditional-Chinese 3C 選品店 — a curated shop whose stated promise is
規格看得懂. One Go binary, Go 1.27, `net/http` (no framework), `templ` server-rendered
HTML, PostgreSQL 18 via pgx + sqlc, Stripe hosted Checkout, htmx only as progressive
enhancement.

Read `CLAUDE.md` end to end, plus `docs/reviews/07-codex-round6-dispositions.md` and
`docs/decisions/`. Then read the code, because **the point of this task is that the
documentation and the code disagree in places, and the interesting items are the ones
neither mentions.**

## Produce a roadmap with three sections

1. **Named as unbuilt, and still unbuilt.** `CLAUDE.md` names some: issuing 統一發票
   (needs a 加值中心), OAuth (`user_identities` has no writer), the bigram tsvector
   projection for CJK search, the storefront read-model projection. For each: what
   triggers it becoming necessary, and what it costs.
2. **Built but incomplete** — a feature whose schema, page or query exists while some
   path through it does not. This project has found five of these by asking "what table
   does the application write and never read, or read and never write" and two more by
   asking "what column". **Ask the next version of that question**: what feature has a
   page but no link, a link but no page, a status no transition reaches, an enum value
   no code produces, an error no handler renders, a config no deployment sets?
3. **Not thought of.** What would a person running an actual Taiwanese 3C shop expect
   that is simply absent? Judge against the project's stated identity — a curated shop
   whose promise is 規格看得懂 — and say which absences are consistent with that
   identity and which are gaps.

For each item: a one-line statement of the gap, the evidence (file:line or a URL that
404s), the size, and what it depends on. Rank by **what a user or shopkeeper would
notice**, not by implementation order.

Do not pad. An honest "the list is short because the project is more finished than it
looks" is a legitimate result.

## Report

The roadmap first.

Then end with the same three lists every brief in this set ends with:

1. **Verified by running** — executed, result observed.
2. **Read but could not verify** — a view formed from the code or the documentation,
   without running it.
3. **Did not look at** — in scope on paper, not examined.
