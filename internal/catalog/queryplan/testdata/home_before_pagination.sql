
WITH global AS (
    SELECT coalesce(avg(rating), 0)::float8 AS m FROM visible_reviews
)
SELECT
    p.slug,
    localized_name(p.name, p.name_en, $2::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, $2::text), '')::text AS summary,
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
    coalesce(localized_name(img.alt_text, img.alt_text_en, $2::text), '')::text AS image_alt,
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
LIMIT $1
