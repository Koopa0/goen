-- The root categories only; children hang off these.
-- name: HomeCategories :many
SELECT id, slug, localized_name(name, name_en, @locale::text) AS name, icon_key
FROM categories
WHERE parent_id IS NULL
ORDER BY position;

-- Bayesian-averaged rating (prior weight 5, global mean), so a lone 5-star does
-- not outrank a well-reviewed 4.6. status = 'active' is a literal, not a
-- parameter, so the partial index stays usable.
-- name: HomeRecommendedTiles :many
WITH global AS (
    SELECT coalesce(avg(rating), 0)::float8 AS m FROM visible_reviews
)
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
    -- A product with no image yields NULL, which sqlc types as a non-null string
    -- and pgx cannot scan.
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height,
    -- Sellable, not merely present: record_inventory_movement refuses a hold
    -- that would take stock below safety_stock.
    EXISTS (
        SELECT 1 FROM product_variants
        WHERE product_id = p.id AND is_active
          AND stock_quantity > safety_stock
    ) AS in_stock
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    -- A buyable variant first: the price on a tile is a promise. Falls back to
    -- the cheapest overall so a sold-out product still shows what it costs.
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n, sum(rating) AS s
    FROM visible_reviews
    WHERE product_id = p.id
) rv ON true
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images
    WHERE product_id = p.id
    ORDER BY position
    LIMIT 1
) img ON true
CROSS JOIN global
WHERE p.status = 'active'
ORDER BY (5 * global.m + coalesce(rv.s, 0)) / (5 + coalesce(rv.n, 0)) DESC,
         p.published_at DESC
LIMIT $1;

-- One slide, not a carousel: `position` is how an editor queues the next one.
-- The window is judged against the database's clock, which wrote the timestamps.
-- name: CurrentHeroSlide :one
-- Every word follows the visitor; the HREFs do not, because a link goes to one
-- page. The nullable fields are coalesced as well as wrapped: localized_name(NULL,
-- NULL, ...) is NULL and sqlc types the result as non-null.
SELECT coalesce(localized_name(h.eyebrow, h.eyebrow_en, @locale::text), '')::text
           AS eyebrow,
       localized_name(h.headline, h.headline_en, @locale::text) AS headline,
       coalesce(localized_name(h.body, h.body_en, @locale::text), '')::text AS body,
       localized_name(h.primary_cta_label, h.primary_cta_label_en, @locale::text)
           AS primary_cta_label,
       h.primary_cta_href,
       coalesce(localized_name(h.secondary_cta_label, h.secondary_cta_label_en,
                               @locale::text), '')::text AS secondary_cta_label,
       h.secondary_cta_href, h.image_key,
       coalesce(localized_name(h.image_alt, h.image_alt_en, @locale::text), '')::text
           AS image_alt,
       -- Width comes from media_objects: one row of bytes, one row of dimensions.
       coalesce(m.width, 0)::integer AS image_width
FROM hero_slides h
LEFT JOIN media_objects m ON m.digest = h.image_key
WHERE h.is_active
  AND (h.starts_at IS NULL OR h.starts_at <= now())
  AND (h.ends_at IS NULL OR h.ends_at > now())
ORDER BY h.position, h.id
LIMIT 1;

-- The window is judged against the database's clock, which wrote the timestamps.
-- name: CurrentPromoBanner :one
SELECT id,
       localized_name(message, message_en, @locale::text) AS message,
       coalesce(localized_name(message_short, message_short_en, @locale::text), '')::text
           AS message_short,
       coalesce(code, '')::text AS code,
       coalesce(localized_name(cta_label, cta_label_en, @locale::text), '')::text
           AS cta_label,
       coalesce(cta_href, '')::text AS cta_href
FROM promo_banners
WHERE is_active
  AND (starts_at IS NULL OR starts_at <= now())
  AND (ends_at IS NULL OR ends_at > now())
ORDER BY created_at DESC
LIMIT 1;

-- A query rather than a list in Go: a category has one name and one place it is
-- translated, and a second copy in the header drifts from the catalogue.
-- name: NavCategories :many
SELECT slug, localized_name(name, name_en, @locale::text) AS name
FROM categories
WHERE parent_id IS NULL
ORDER BY position, name;

-- MIN across methods: the strip makes one claim, and the most generous true one
-- is the lowest threshold any active method honours. coalesce AND cast, because
-- min() over an empty set is NULL and sqlc types the result as non-null.
-- name: FreeDeliveryThreshold :one
SELECT coalesce(min(v.free_over_cents), 0)::bigint AS free_over_cents
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE sm.is_active
  AND v.effective_at <= now()
  AND v.free_over_cents > 0
  AND v.id = (SELECT id FROM shipping_method_versions
              WHERE method_id = sm.id AND effective_at <= now()
              ORDER BY effective_at DESC LIMIT 1);
