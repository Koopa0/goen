-- The category a listing URL names, plus the trail above it for the crumbs.
-- Walking upward is bounded by the tree's depth, and categories_acyclic
-- guarantees the walk terminates.
-- Every name goes through localized_name, the one place that decides which name a
-- reader gets. A category name is in the header of every page, so getting it in one
-- query and not another shows a visitor Phones at the top and 手機 in the crumb.
-- name: CategoryBySlug :one
WITH RECURSIVE trail AS (
    SELECT c.id, c.parent_id, c.slug,
           localized_name(c.name, c.name_en, @locale::text) AS name, 0 AS depth
    FROM categories c
    WHERE c.slug = $1
    UNION ALL
    SELECT c.id, c.parent_id, c.slug,
           localized_name(c.name, c.name_en, @locale::text), t.depth + 1
    FROM categories c
    JOIN trail t ON c.id = t.parent_id
)
SELECT
    self.id,
    self.name,
    -- Ancestors root-first, which is the order the crumbs render in. Empty for
    -- a root category.
    coalesce(
        (SELECT array_agg(a.slug ORDER BY a.depth DESC) FROM trail a WHERE a.depth > 0),
        ARRAY[]::text[]
    )::text[] AS ancestor_slugs,
    coalesce(
        (SELECT array_agg(a.name ORDER BY a.depth DESC) FROM trail a WHERE a.depth > 0),
        ARRAY[]::text[]
    )::text[] AS ancestor_names
FROM trail self
WHERE self.depth = 0;

-- Every category in the subtree rooted at $1, including $1 itself.
--
-- A listing shows its descendants' products: /c/accessories holds none of its
-- own and the site header links straight to it, so an exact category_id match
-- renders an empty page from goen's own navigation.
-- name: CategoryDescendants :many
WITH RECURSIVE d AS (
    SELECT c.id FROM categories c WHERE c.id = $1
    UNION ALL
    SELECT c.id FROM categories c JOIN d ON c.parent_id = d.id
)
SELECT d.id FROM d;

-- The brands present in a subtree, for the brand facet. Counted over products
-- that would appear with no other filter applied, so a brand offering nothing
-- is not listed.
-- name: CategoryBrands :many
SELECT b.id, b.slug, b.name, count(*)::bigint AS product_count
FROM products p
JOIN brands b ON b.id = p.brand_id
WHERE p.status = 'active'
  AND p.category_id = ANY(@category_ids::uuid[])
GROUP BY b.id, b.slug, b.name
ORDER BY b.name;

-- One page of a category listing.
--
-- Three things here are load-bearing and each is measured in
-- docs/decisions/003-listing-read-model.md:
--
--  1. status = 'active' is a LITERAL. The planner cannot prove a parameter is
--     always 'active', so parameterising it loses
--     products_category_published_idx entirely (CLAUDE.md, predictable mistake
--     #10). Verified in the plan, not assumed.
--
--  2. Every variant condition sits inside ONE EXISTS. Splitting them lets each
--     find a different variant, which is how a listing answers "in stock and
--     under NT$10,000" with a product whose cheap variant is sold out and whose
--     available one costs ten times that. The rule fires without any option
--     facets; price and stock alone collide.
--
--  3. Sellable is stock_quantity > safety_stock, never > 0.
--     record_inventory_movement refuses a sale or hold that would breach the
--     floor, so a variant sitting AT it has stock and cannot be bought.
--
-- Sorting is chosen by $-parameter rather than composed in Go: an ORDER BY built
-- from a request is where an injection gets in, and sqlc would not see it.
-- name: CategoryListing :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    mv.price_cents AS min_price_cents,
    mv.compare_at_price_cents,
    coalesce(rv.rating, 0)::float8 AS rating,
    coalesce(rv.n, 0)::bigint AS rating_count,
    EXISTS (
        SELECT 1 FROM product_variants sv
        WHERE sv.product_id = p.id AND sv.is_active
          AND sv.stock_quantity > sv.safety_stock
    ) AS in_stock,
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    -- A buyable variant first: the price on a card is a promise, so it has to
    -- be the price of something a visitor can actually put in a cart.
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n
    FROM visible_reviews WHERE product_id = p.id
) rv ON true
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE p.status = 'active'
  AND p.category_id = ANY(@category_ids::uuid[])
  AND (@brand_ids::uuid[] = ARRAY[]::uuid[] OR p.brand_id = ANY(@brand_ids::uuid[]))
  -- One variant satisfies every variant-level filter at once. See note 2.
  AND (
      NOT @filter_variants::boolean
      OR EXISTS (
          SELECT 1 FROM product_variants v
          WHERE v.product_id = p.id AND v.is_active
            AND (NOT @in_stock_only::boolean OR v.stock_quantity > v.safety_stock)
            AND (@min_price::bigint = 0 OR v.price_cents >= @min_price::bigint)
            AND (@max_price::bigint = 0 OR v.price_cents <= @max_price::bigint)
      )
  )
ORDER BY
    CASE WHEN @sort::text = 'price_asc'  THEN mv.price_cents END ASC,
    CASE WHEN @sort::text = 'price_desc' THEN mv.price_cents END DESC,
    CASE WHEN @sort::text = 'rating'     THEN coalesce(rv.rating, 0) END DESC,
    p.published_at DESC, p.id DESC
LIMIT @page_size::integer OFFSET @page_offset::integer;

-- How many products the current filters match, for the pager. Deliberately the
-- same predicate as CategoryListing and nothing else — no LATERAL joins, no
-- image, no rating — because this is only ever a number.
-- name: CategoryListingCount :one
SELECT count(*)::bigint
FROM products p
WHERE p.status = 'active'
  AND p.category_id = ANY(@category_ids::uuid[])
  AND (@brand_ids::uuid[] = ARRAY[]::uuid[] OR p.brand_id = ANY(@brand_ids::uuid[]))
  AND (
      NOT @filter_variants::boolean
      OR EXISTS (
          SELECT 1 FROM product_variants v
          WHERE v.product_id = p.id AND v.is_active
            AND (NOT @in_stock_only::boolean OR v.stock_quantity > v.safety_stock)
            AND (@min_price::bigint = 0 OR v.price_cents >= @min_price::bigint)
            AND (@max_price::bigint = 0 OR v.price_cents <= @max_price::bigint)
      )
  );

-- Search across the catalogue.
--
-- ILIKE with a trigram GIN index: measured, that index serves Latin queries
-- ("%pixel%", 1.5 ms at 10,000 products) and short Chinese queries fall back to
-- a sequential scan (8.8 ms) because their trigrams are too unselective for the
-- planner to prefer it. That is the honest limit today; the bigram projection
-- that fixes Chinese properly is a named follow-up in
-- docs/decisions/003-listing-read-model.md.
--
-- The caller escapes %, _ and \ before binding, so a query string of "%" finds
-- products containing a percent sign rather than everything.
-- name: SearchProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    mv.price_cents AS min_price_cents,
    mv.compare_at_price_cents,
    coalesce(rv.rating, 0)::float8 AS rating,
    coalesce(rv.n, 0)::bigint AS rating_count,
    EXISTS (
        SELECT 1 FROM product_variants sv
        WHERE sv.product_id = p.id AND sv.is_active
          AND sv.stock_quantity > sv.safety_stock
    ) AS in_stock,
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n
    FROM visible_reviews WHERE product_id = p.id
) rv ON true
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE p.status = 'active'
  -- BOTH names, and that is not the same rule as the display one. A search matches
  -- on IDENTITY: an English visitor typing "case" must find 保護殼 and a Chinese
  -- visitor typing 保護殼 must still find it after somebody adds an English name.
  -- Matching only the localized column would make the catalogue searchable in one
  -- language at a time, which is worse than not translating it at all.
  AND (p.name ILIKE @pattern::text
       OR coalesce(p.name_en, '') ILIKE @pattern::text
       OR coalesce(p.summary, '') ILIKE @pattern::text
       OR coalesce(p.summary_en, '') ILIKE @pattern::text
       OR b.name ILIKE @pattern::text)
ORDER BY
    -- A name match outranks a summary or brand match: someone typing a model
    -- number wants that product, not everything the brand makes. Either name
    -- counts, for the reason the predicate takes both.
    (p.name ILIKE @pattern::text OR coalesce(p.name_en, '') ILIKE @pattern::text) DESC,
    p.published_at DESC, p.id DESC
LIMIT @page_size::integer OFFSET @page_offset::integer;

-- The same predicate as SearchProducts, and it has to STAY the same: a count that
-- matches on fewer columns than the list reports a different number of results from
-- the number of rows shown.
-- name: SearchProductsCount :one
SELECT count(*)::bigint
FROM products p
JOIN brands b ON b.id = p.brand_id
WHERE p.status = 'active'
  AND (p.name ILIKE @pattern::text
       OR coalesce(p.name_en, '') ILIKE @pattern::text
       OR coalesce(p.summary, '') ILIKE @pattern::text
       OR coalesce(p.summary_en, '') ILIKE @pattern::text
       OR b.name ILIKE @pattern::text);

-- Products with something marked down.
--
-- "On sale" is a VARIANT fact — compare_at_price_cents above price_cents — and
-- a product qualifies when any active variant carries one. The row shown is the
-- cheapest sellable variant, the same one every other listing shows, so a
-- product does not appear at one price here and another on its own page.
--
-- Ordered by how deep the cut is. A deals page sorted by newest buries the
-- reason anyone opened it.
-- name: DealProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    mv.price_cents AS min_price_cents,
    mv.compare_at_price_cents,
    coalesce(rv.rating, 0)::float8 AS rating,
    coalesce(rv.n, 0)::bigint AS rating_count,
    EXISTS (
        SELECT 1 FROM product_variants sv
        WHERE sv.product_id = p.id AND sv.is_active
          AND sv.stock_quantity > sv.safety_stock
    ) AS in_stock,
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n
    FROM visible_reviews WHERE product_id = p.id
) rv ON true
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE p.status = 'active'
  AND EXISTS (
      SELECT 1 FROM product_variants dv
      WHERE dv.product_id = p.id AND dv.is_active
        AND dv.compare_at_price_cents IS NOT NULL
        AND dv.compare_at_price_cents > dv.price_cents
  )
ORDER BY
    -- Deepest discount first, as a fraction rather than an amount: 30% off a
    -- NT$900 case is a better deal than NT$500 off a NT$50,000 laptop, and a
    -- shopper reading a deals page is looking for the former.
    ((mv.compare_at_price_cents - mv.price_cents)::float8
     / nullif(mv.compare_at_price_cents, 0)) DESC NULLS LAST,
    p.published_at DESC, p.id DESC
LIMIT @page_size::integer OFFSET @page_offset::integer;

-- name: DealProductsCount :one
SELECT count(*)::bigint
FROM products p
WHERE p.status = 'active'
  AND EXISTS (
      SELECT 1 FROM product_variants dv
      WHERE dv.product_id = p.id AND dv.is_active
        AND dv.compare_at_price_cents IS NOT NULL
        AND dv.compare_at_price_cents > dv.price_cents
  );


-- Everything a sitemap lists, with when it last changed.
--
-- Only ACTIVE products and only categories that have something in them: a
-- sitemap is a claim that these URLs are worth crawling, and pointing a crawler
-- at an empty category spends its budget on a page with nothing on it.
-- name: SitemapProducts :many
SELECT slug, updated_at FROM products
WHERE status = 'active'
ORDER BY updated_at DESC
LIMIT $1;

-- name: SitemapCategories :many
SELECT DISTINCT c.slug, c.updated_at
FROM categories c
WHERE EXISTS (
    SELECT 1 FROM products p
    WHERE p.category_id = c.id AND p.status = 'active'
)
ORDER BY c.updated_at DESC;

-- The campaign a slug names, if it is running right now.
--
-- The window is judged in SQL against the DATABASE's clock, for the reason the
-- coupon window is: starts_at and ends_at were written by now() here, and
-- comparing them to Go's time.Now() is comparing two clocks.
-- name: RunningCampaign :one
SELECT id, slug, localized_name(title, title_en, @locale::text) AS title, ends_at
FROM sale_campaigns
WHERE slug = @slug::text AND is_active
  AND starts_at <= now() AND ends_at > now();

-- Every campaign running now, for the home page and the deals page.
-- name: RunningCampaigns :many
SELECT c.id, c.slug, localized_name(c.title, c.title_en, @locale::text) AS title,
       c.ends_at,
       (SELECT count(*) FROM sale_campaign_products p WHERE p.campaign_id = c.id)::bigint AS products
FROM sale_campaigns c
WHERE c.is_active AND c.starts_at <= now() AND c.ends_at > now()
ORDER BY c.ends_at
LIMIT $1;

-- What a campaign features.
--
-- The same tile shape every other listing uses, so a campaign page is the
-- product grid with a different heading rather than a second way to draw a
-- product. Ordered by the position the back office set: a campaign is
-- merchandising, and the order it lists things in is the point.
-- name: CampaignProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    mv.price_cents AS min_price_cents,
    mv.compare_at_price_cents,
    coalesce(rv.rating, 0)::float8 AS rating,
    coalesce(rv.n, 0)::bigint AS rating_count,
    EXISTS (
        SELECT 1 FROM product_variants sv
        WHERE sv.product_id = p.id AND sv.is_active
          AND sv.stock_quantity > sv.safety_stock
    ) AS in_stock,
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM sale_campaign_products cp
JOIN products p ON p.id = cp.product_id
JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n
    FROM visible_reviews WHERE product_id = p.id
) rv ON true
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE cp.campaign_id = $1 AND p.status = 'active'
ORDER BY cp.position, p.id;

-- The products in a comparison, in the order the URL named them.
--
-- WITH ORDINALITY, so the columns appear in the order somebody chose rather
-- than in whatever order the join produced — a comparison whose columns move
-- between page loads is one nobody can point at.
-- name: CompareProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    localized_name(c.name, c.name_en, @locale::text) AS category,
    mv.price_cents AS min_price_cents,
    mv.compare_at_price_cents,
    coalesce(rv.rating, 0)::float8 AS rating,
    coalesce(rv.n, 0)::bigint AS rating_count,
    EXISTS (
        SELECT 1 FROM product_variants sv
        WHERE sv.product_id = p.id AND sv.is_active
          AND sv.stock_quantity > sv.safety_stock
    ) AS in_stock,
    coalesce(p.warranty_months, 0)::integer AS warranty_months,
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height,
    asked.ord::integer AS position
FROM unnest(@slugs::text[]) WITH ORDINALITY AS asked(slug, ord)
JOIN products p ON p.slug = asked.slug AND p.status = 'active'
JOIN brands b ON b.id = p.brand_id
JOIN categories c ON c.id = p.category_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n
    FROM visible_reviews WHERE product_id = p.id
) rv ON true
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
ORDER BY asked.ord;

-- Every spec of every product in the comparison.
--
-- Ordered so the rows a reader can actually compare come first: a label two
-- products share is the point of the table, and one only a single product
-- carries is a footnote. Ties break on the label's own position, so a product's
-- own ordering survives where nothing else decides.
-- name: CompareSpecs :many
SELECT
    p.slug,
    localized_name(s.label, s.label_en, @locale::text) AS label,
    localized_name(s.value, s.value_en, @locale::text) AS value,
    -- Counted on the UNTRANSLATED label, deliberately. Two products state 螢幕 and
    -- one of them has an English label for it: grouping by what the reader sees
    -- would split that row in two and report each as stated by one product, which
    -- is the opposite of what this number is for. The rows are the same spec; only
    -- the words shown differ.
    (SELECT count(DISTINCT sp.product_id)
     FROM product_specs sp
     JOIN products op ON op.id = sp.product_id
     WHERE sp.label = s.label AND op.slug = ANY(@slugs::text[]))::bigint AS shared_by,
    s.position
FROM product_specs s
JOIN products p ON p.id = s.product_id
WHERE p.slug = ANY(@slugs::text[])
ORDER BY shared_by DESC, s.label, s.position;
