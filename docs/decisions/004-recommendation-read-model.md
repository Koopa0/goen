# 004 — The co-purchase read model is a projection

**Decision:** 買了又買 is read from `product_copurchases`, rebuilt every fifteen
minutes by a background worker, not computed per request.

**Status:** implemented.

## What was measured

The per-request query — "products in the same committed order as this one" —
against synthetic order history on the dev database (PostgreSQL 18, 15 active
products, ~2.5 lines per order):

| Committed orders | Product measured | Execution |
|---|---|---|
| 1,000 | first in the catalogue | 0.31 ms |
| 5,000 | first in the catalogue | 1.10 ms |
| 15,000 | first in the catalogue | 3.14 ms |
| 15,000 | **the most-bought product** | **136 ms** |

The first three readings said the query was affordable. They were taken against
a product with almost no order history, and the same query for the product
people actually buy costs forty times more — because the work is proportional to
*that product's* history, not to the catalogue's size.

`EXPLAIN` says where it goes: 14,963 candidate rows, and `order_is_committed()`
called once per row. That call is correct — CLAUDE.md records three guards that
got "committed" wrong by using `EXISTS(succeeded payment)` instead, and a
fully store-credited order has no payment row — but it is a PL/pgSQL invocation,
and 14,963 of them is one page view.

Read from the projection, the same answer is **0.04 ms**: a 3,400x difference,
and one that stops growing with order history.

The rebuild itself costs 246–584 ms at 15,000 orders, once per interval.

## Why staleness is acceptable here and nowhere else

An hour-old answer to "what goes with this" is the same answer. An hour-old
stock count is an oversell.

That asymmetry is the whole argument. goen computes stock, price, availability
and cart totals live, and projects only where being slightly behind changes
nothing a customer can act on. This is the first thing that qualifies.

## Why a full rebuild

The input is every committed order. An incremental update would need to know
which orders changed state since the last run — a second piece of bookkeeping
that can drift from the thing it describes, and whose drift is invisible
because a wrong recommendation looks like a recommendation.

`DELETE` and re-`INSERT` inside one transaction, not `TRUNCATE`: truncate takes
an `ACCESS EXCLUSIVE` lock and would block every product page for the duration.

When one query stops being enough, the fix is to bound it by date — the last
year of orders is the same recommendation as all of history — not to make it
incremental.

## Why a third database role

`refresh_copurchases` is `SECURITY DEFINER`, so WHO may call it is the entire
control. Granting it to `store` would make a 584 ms rebuild callable from any
handler, by anyone, as often as they like.

`maintenance` / `maintenance_svc` exist for that one function, on their own
pool. A separate pool rather than `SET ROLE` on a borrowed connection, for the
reason `admin_svc` has one: a role set on a request's connection is still set
when the connection goes back to the pool.

## What is deliberately not done

- **No fallback.** A product with no co-purchase data renders nothing. Filling
  the slot with popular products instead would present a guess as a pattern,
  and a shopper cannot tell the two apart.
- **A minimum of two shared orders.** One is a coincidence. At this catalogue
  size a threshold of one would make any two products that ever met
  "frequently bought together".
- **No personalisation.** This is "what goes with this product", not "what goes
  with you". The second needs browsing history goen does not collect.
