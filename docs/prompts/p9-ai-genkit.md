# P9 — AI features worth building, on Genkit

> Self-contained brief. Assume no context beyond this file and the repository.
> **For:** Codex · **Order:** any time; this changes the product rather than the code.

`goen` is a Traditional-Chinese 3C 選品店 — a **curated** shop, whose stated promise is
規格看得懂: the specs are understandable, there is a comparison table, and the copy is
honest. One Go binary, `net/http` (no framework), `templ` server-rendered HTML,
PostgreSQL 18 via pgx + sqlc. Read `CLAUDE.md` before proposing anything; the project's
identity is written there and several of its rules will constrain what is buildable.

**The AI framework is fixed: `genkit-ai`.** Note that `CLAUDE.md` currently lists
Genkit among the rules imported from `~/go-spec` that goen has **no use for** — so
proposing AI features changes that, and your report should say what it costs to bring
that dependency in (`go list -m all | wc -l` before and after; the project has 103
modules and an explicit rule about the module graph).

## What to produce

**Five to eight candidate features**, each argued rather than listed. For each:

- **What a customer or shopkeeper actually gets**, in one sentence.
- **Why an LLM** — and this is the test most ideas fail. If a SQL query, a lookup table
  or a `LIKE` would do it, say so and drop it. The project has a documented habit of
  refusing features that present a guess as a fact (it will not fill an empty
  recommendation slot with popular products, because "a shopper cannot tell those
  apart").
- **Where the data comes from.** The schema has `product_specs`, `products.description`
  and `description_en`, `product_reviews`, `product_questions`/`product_answers`,
  `faq_entries`, `contact_messages`, `order_events`, `product_copurchases`.
- **What happens when it is wrong**, and whether the failure is visible or silent. A
  wrong spec summary on a PDP is a shop lying about a product it sells.
- **Cost and latency**, and whether it sits on a request path or a worker. goen already
  has a background worker pattern (outbox, projection rebuild on its own pool) and a
  documented view that staleness is acceptable for recommendations and nowhere else.
- **The i18n consequence.** The project's rule is that **copy compiled into the binary
  is goen's to say in both languages; copy typed into a table is the shop's to say
  however it likes** — and it deliberately does NOT machine-translate product
  descriptions, because "translating this is an editorial job". An AI feature that
  generates or translates product copy runs directly into that decision. Address it
  head-on: does the rule change, or does the feature respect it?

Ideas worth considering seriously (do not treat as a list to implement): a spec
explainer that turns a spec table into plain language for a non-technical buyer;
comparison prose for `/compare`; a question-answering draft for `/admin/questions` that
a human sends; review summarisation; semantic search over the catalogue (note the
documented CJK search problem — short Chinese queries are a sequential scan today, and
the planned fix is a bigram tsvector projection, so an embedding approach competes with
a concrete alternative); a shopping assistant that resolves "a phone under NT$20,000
with a good camera" into a facet query; support-message triage; a 選品 assistant that
drafts the shop's own curation copy.

## The three that matter most

Pick **three** and design them properly: the Genkit flow, the prompt file, the tools it
would call, where it runs, what it stores, how it degrades when the model is
unavailable, and how you would test it. This project tests against real dependencies
and forbids mock frameworks — say how an AI flow is tested under that rule, because it
is the hard part.

## Report

Recommendation first: which one to build first and why, and which of the eight you
would refuse.

Then end with the same three lists every brief in this set ends with:

1. **Verified by running** — executed, result observed.
2. **Read but could not verify** — a view formed from the code or the documentation,
   without running it.
3. **Did not look at** — in scope on paper, not examined.
