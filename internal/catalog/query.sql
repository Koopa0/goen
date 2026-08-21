-- categories_acyclic is what guarantees the upward walk terminates.
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
    -- Root-first, the order the crumbs render in. Empty for a root category.
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
-- name: CategoryDescendants :many
WITH RECURSIVE d AS (
    SELECT c.id FROM categories c WHERE c.id = $1
    UNION ALL
    SELECT c.id FROM categories c JOIN d ON c.parent_id = d.id
)
SELECT d.id FROM d;

-- Counted over products that would appear with no other filter applied, so a
-- brand offering nothing is not listed.
-- name: CategoryBrands :many
SELECT b.id, b.slug, b.name, count(*)::bigint AS product_count
FROM products p
JOIN brands b ON b.id = p.brand_id
WHERE p.status = 'active'
  AND p.category_id = ANY(@category_ids::uuid[])
GROUP BY b.id, b.slug, b.name
ORDER BY b.name;

-- status = 'active' is a literal, or the planner cannot use
-- products_category_published_idx. Every variant condition sits in ONE EXISTS, or
-- each finds a different variant. Sellable is stock_quantity > safety_stock.
-- name: CategoryListing :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    mv.price_cents AS min_price_cents,
    -- Whether that price is the cheapest of several, so a card can say "from"
    -- rather than state one variant's price as the product's.
    EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    ) AS price_varies,
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
    -- A buyable variant first: the price on a card is a promise.
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
  -- One variant satisfies every variant-level filter at once.
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

-- The same predicate as CategoryListing, and nothing else: this is only a number.
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

-- The trigram GIN index serves Latin queries; short Chinese ones fall back to a
-- sequential scan. The caller escapes %, _ and \ before binding.
-- name: SearchProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    mv.price_cents AS min_price_cents,
    -- Whether that price is the cheapest of several, so a card can say "from"
    -- rather than state one variant's price as the product's.
    EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    ) AS price_varies,
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
  -- Both names: matching only the localized column would make the catalogue
  -- searchable in one language at a time.
  AND (p.name ILIKE @pattern::text
       OR coalesce(p.name_en, '') ILIKE @pattern::text
       OR coalesce(p.summary, '') ILIKE @pattern::text
       OR coalesce(p.summary_en, '') ILIKE @pattern::text
       OR b.name ILIKE @pattern::text)
ORDER BY
    -- A name match outranks a summary or brand match. Either name counts.
    (p.name ILIKE @pattern::text OR coalesce(p.name_en, '') ILIKE @pattern::text) DESC,
    p.published_at DESC, p.id DESC
LIMIT @page_size::integer OFFSET @page_offset::integer;

-- The same predicate as SearchProducts, and it has to stay the same.
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

-- "On sale" is a variant fact, and a product qualifies when any active variant
-- carries one.
-- name: DealProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    mv.price_cents AS min_price_cents,
    -- Whether that price is the cheapest of several, so a card can say "from"
    -- rather than state one variant's price as the product's.
    EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    ) AS price_varies,
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
-- A DISCOUNTED variant first, which is what puts the product on this page at
-- all. The listing's LATERAL takes the cheapest buyable one, and a product
-- qualifies here when ANY variant carries a discount — two different variants
-- whenever the discounted one is dearer or out of stock, so the sale page could
-- quote a price with no discount on it and no badge beside it. They agree on
-- every product in the dev seed, which is what a fixture where two rules agree
-- is worth.
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (compare_at_price_cents IS NOT NULL
              AND compare_at_price_cents > price_cents) DESC,
             (stock_quantity > safety_stock) DESC,
             price_cents
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
    -- Deepest discount first, as a fraction rather than an amount.
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


-- Only active products, and only categories with something in them: a sitemap is
-- a claim that these URLs are worth crawling.
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

-- The window is judged against the database's clock, which wrote the timestamps.
-- name: RunningCampaign :one
SELECT id, slug, localized_name(title, title_en, @locale::text) AS title, ends_at
FROM sale_campaigns
WHERE slug = @slug::text AND is_active
  AND starts_at <= now() AND ends_at > now();

-- name: RunningCampaigns :many
SELECT c.id, c.slug, localized_name(c.title, c.title_en, @locale::text) AS title,
       c.ends_at,
       (SELECT count(*) FROM sale_campaign_products p WHERE p.campaign_id = c.id)::bigint AS products
FROM sale_campaigns c
WHERE c.is_active AND c.starts_at <= now() AND c.ends_at > now()
ORDER BY c.ends_at
LIMIT $1;

-- Ordered by the position the back office set: a campaign is merchandising.
-- name: CampaignProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    mv.price_cents AS min_price_cents,
    -- Whether that price is the cheapest of several, so a card can say "from"
    -- rather than state one variant's price as the product's.
    EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    ) AS price_varies,
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

-- WITH ORDINALITY, so the columns appear in the order the URL named them.
-- name: CompareProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    localized_name(c.name, c.name_en, @locale::text) AS category,
    mv.price_cents AS min_price_cents,
    -- Whether that price is the cheapest of several, so a card can say "from"
    -- rather than state one variant's price as the product's.
    EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    ) AS price_varies,
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

-- Ordered so the rows a reader can compare come first; ties break on the label's
-- own position.
-- name: CompareSpecs :many
SELECT
    p.slug,
    localized_name(s.label, s.label_en, @locale::text) AS label,
    -- The untranslated label identifies a row, and the Go that builds the table
    -- must group on the same thing or two labels sharing a translation merge.
    s.label AS label_key,
    localized_name(s.value, s.value_en, @locale::text) AS value,
    -- Counted on the untranslated label: grouping by what the reader sees would
    -- split one spec in two and report each as stated by one product.
    (SELECT count(DISTINCT sp.product_id)
     FROM product_specs sp
     JOIN products op ON op.id = sp.product_id
     WHERE sp.label = s.label AND op.slug = ANY(@slugs::text[]))::bigint AS shared_by,
    s.position
FROM product_specs s
JOIN products p ON p.id = s.product_id
WHERE p.slug = ANY(@slugs::text[])
ORDER BY shared_by DESC, s.label, s.position;
