# Listing and search read model: measured, batch ②

The precedent is `001-home-read-model.md`: measure at the seed and at ~10,000
products, then decide. Every number below is from `EXPLAIN (ANALYZE, BUFFERS)`
on PostgreSQL 18 against scratch databases holding 10,000–62,000 generated
products, not from reasoning about what ought to be fast.

## 1. A category listing includes its descendants

`/c/accessories` has **no products of its own** — its children `chargers` and
`cases` hold four between them — and the site header links straight to it. An
exact `category_id` match renders an empty page from goen's own navigation.

The listing therefore reads the category **and all its descendants**, through a
recursive CTE feeding `= ANY(ARRAY(...))`.

| Shape | Scale | Time | Plan |
| --- | --- | --- | --- |
| Single (leaf) category | 62,000 products | **0.088 ms** | `Index Scan using products_category_published_idx` — **no sort at all** |
| Recursive CTE + `JOIN` | 1,599 in subtree | 6.8 ms | Bitmap + top-N heapsort, 1,517 buffers |
| Recursive CTE + `= ANY(ARRAY(...))` | 1,599 in subtree | **1.3 ms** | Bitmap + top-N heapsort, 592 buffers |
| Same, largest subtree | **50,000** in subtree | 8.4 ms | Bitmap + top-N heapsort |
| LATERAL per-category top-N, merged | 50,000 in subtree | 3.1 ms | more complex, `O(categories × page)` |

Two findings matter more than the ranking:

- **The partial index survives the set match.** `products_category_published_idx`
  is used as a `Bitmap Index Scan` even when `category_id` is compared against
  33 ids — provided `status = 'active'` stays a **literal**. That is CLAUDE.md's
  predictable mistake #10 and it was verified, not assumed.
- **The set match loses the ordering.** A single category walks the index in
  `published_at DESC` order and touches only the 24 rows the page needs. A set
  match produces a bitmap, which has no order, so every product in the subtree
  is fetched and sorted. Cost goes from `O(page)` to `O(subtree)`.

`= ANY` is chosen over the LATERAL merge because 8.4 ms at 50,000 products in one
subtree is well inside budget and the LATERAL shape complicates pagination — each
category has to contribute enough rows to fill every subsequent page. Revisit if
a single subtree passes ~50,000 active products.

## 2. Facets must match on ONE variant — and it bites without colour filters

CLAUDE.md states the rule; this is the proof, run against a fixture built to
break it.

A product with **blue sold out** and **black in stock**, filtered by
"blue AND in stock":

| Query | Returns | Correct? |
| --- | --- | --- |
| Naive — each condition finds its own variant | **1** | No |
| Correct — one `EXISTS` over variants, conditions nested inside | **0** | Yes |
| Correct, "black AND in stock" (positive control) | **1** | Yes |

The positive control is what makes this a proof rather than a query that always
returns nothing.

**The trap does not need option facets to fire.** goen needs price and stock
filters on day one, and they collide the same way. A product with a sold-out
NT$5,000 variant and an available NT$99,000 variant, filtered by
"in stock AND under NT$10,000":

| Query | Returns | Correct? |
| --- | --- | --- |
| Naive — stock and price each find their own variant | **1** | No |
| Correct — one variant satisfies both | **0** | Yes |

So the one-variant rule is implemented from the start, whatever facets ship.

### "In stock" is `stock_quantity > safety_stock`, never `> 0`

`record_inventory_movement` refuses a `sale` or `hold` that would take stock
below `safety_stock`. A variant sitting **at** the floor has stock and cannot be
bought, so `stock_quantity > 0` makes the storefront promise what the database
will refuse. The dev seed now carries variants in that band specifically so the
wrong predicate has something to fail against.

### Which facets ship now

Option values are **product-scoped**: every product owns its own `顏色 / 黑` row.
Measured on the real catalogue, each colour value inside a category maps to about
**one product**, and `穿戴裝置` carries both `石墨黑` and `黑` — the same colour
twice. Six checkboxes filtering to one product each is worse than no filter.

| Facet | Ships | Why |
| --- | --- | --- |
| Brand | yes | Cross-product and indexed (`products_category_brand_published_idx`) |
| Price range | yes | Cross-product, on the variant, needs the one-variant rule |
| In stock | yes | Cross-product, on the variant, needs the one-variant rule |
| Colour / capacity | **no** | Product-scoped; ~1 product per value per category today |

Option facets become worthwhile when a category holds enough products per value
to filter anything — call it 5+ products behind a value. They then have to match
on the option value's **text**, not its id, because the ids are per-product.
Measured at 10,000 products: **14.5 ms** with sequential scans, which would want
an index on `product_option_values (value)` first.

## 3. Search: trigrams carry Latin, not short Chinese

pg_trgm is installed. Measured on 10,000 products with a
`gin (name gin_trgm_ops)` index:

| Query | Plan | Time |
| --- | --- | --- |
| `%pixel%` (Latin, 5 chars) | **Bitmap Index Scan** on the trigram index | 1.5 ms |
| `%耳機%` (Chinese, 2 chars) | **Seq Scan**, 9,642 rows discarded | 8.8 ms |
| `%機%` (Chinese, 1 char) | Seq Scan | 5.6 ms |

Trigrams *are* extracted from Chinese — `show_trgm('耳機')` returns three — but
they are too unselective for the planner to prefer the index, so short CJK
queries fall back to a scan. This is the honest limit: **goen has Latin search
with an index and Chinese search by scan.**

At 10,000 products a scan costs 8.8 ms, which is fine; goen has fifteen. The
index still ships because it makes Latin queries indexed immediately and costs
nothing on the CJK path.

The real answer for Chinese at scale is a **bigram tsvector projection** — a
`product_search_documents` table with `gin (bigram_tsv(body))`, which segments
CJK into overlapping two-character tokens the way trigrams cannot. It is a
projection, with all the refresh burden that implies, so it is a **named
follow-up gated on the catalogue passing ~5,000 active products** or on Chinese
search latency being measured above ~50 ms — whichever comes first.

## 4. Pagination

Not separately measured to a conclusion this round; the listing ships with
`LIMIT/OFFSET` because the crux above already bounds it. The subtree read is
`O(subtree)` regardless of page, so offset adds nothing to the dominant cost
until the catalogue is far larger than it is. The index's `id` tie-break exists
precisely so keyset pagination is available when it is needed — the schema
comment records why — and that is the upgrade path, gated on the same ~50,000
figure as the descendant read.

A total count is **not** shown. An exact count over a subtree costs a second full
scan of the same rows for a number nobody acts on.

## What would change these answers

- A single subtree past ~50,000 active products → LATERAL merge, or keyset.
- A category with enough products per option value → option facets, matched by
  value text, with an index on `product_option_values (value)`.
- ~5,000 active products, or Chinese search over ~50 ms → the bigram projection.
