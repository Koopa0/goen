# Home read model: per-view now, projection when measured

## Decision

The home page reads its product tiles — minimum active variant price, a
Bayesian-averaged rating, the primary image, the brand — with **per-view
aggregation** (LATERAL joins over `product_variants`, `product_reviews`,
`product_images`). No projection is built yet.

## Why, with the measurement

CLAUDE.md left this open on purpose: "a projection is the likely answer, but the
decision waits for a measurement rather than a guess." Here is the measurement,
run on PostgreSQL 18 against the dev seed and a scaled copy:

| Active products | Execution time | Plan |
| --------------- | -------------- | ---- |
| 15 (the seed)   | 0.26 ms        | trivial |
| ~10,000         | 60 ms warm, 221 ms cold | Seq Scan on `products`, then top-N heapsort |

The recommended tiles rank by a Bayesian rating (`(C·m + Σr) / (C + n)`,
prior weight `C = 5`, global mean `m`). That score is computed per product, so
ordering by it forces a scan and aggregation of **every** active product before
the `LIMIT 8` — no index can serve a computed-score order. Cost is therefore
`O(active products)` and grows linearly, which is why 10,000 products already
cost 60 ms.

At the current and demonstration scale (tens of products) per-view is 0.26 ms:
correct, and building a projection for it would be the premature optimization
this project avoids. The measurement also tells us exactly when that stops being
true.

## The trigger point

When the catalogue passes **~1,000–2,000 active products**, the recommended
query crosses into tens of milliseconds and keeps climbing. That is the point to
introduce a projection: a denormalized `product_tile` read model (product,
min price, rating count/sum, precomputed rating score, primary image),
maintained out of band and indexed on the score and on `published_at`, so the
home and listing reads become an indexed top-N instead of a full aggregation.

It is a read projection (derived data), not an integrity rule, so it should be a
materialized view or an out-of-band refresh — not the integrity triggers goen
uses elsewhere. Building it is a named follow-up, gated on the catalogue
reaching that size, not on a guess.

## Index note

"New arrivals" (newest active products across all categories) is not the
`products_category_published_idx` shape (that leads with `category_id`). If the
home grows a cross-category new-arrivals row that measures slow, add
`products (published_at DESC, id DESC) WHERE status = 'active'` in a new
migration at that time.
