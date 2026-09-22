-- Immutable production SQL from 2793db97058cdce2f3601142fd4ab6b6c0df182c.
-- name: CategoryListing :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, $1::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, $1::text), '')::text AS summary,
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
    coalesce(localized_name(img.alt_text, img.alt_text_en, $1::text), '')::text AS image_alt,
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
  AND p.category_id = ANY($2::uuid[])
  AND ($3::uuid[] = ARRAY[]::uuid[] OR p.brand_id = ANY($3::uuid[]))
  -- One variant satisfies every variant-level filter at once.
  AND (
      NOT $4::boolean
      OR EXISTS (
          SELECT 1 FROM product_variants v
          WHERE v.product_id = p.id AND v.is_active
            AND (NOT $5::boolean OR v.stock_quantity > v.safety_stock)
            AND ($6::bigint = 0 OR v.price_cents >= $6::bigint)
            AND ($7::bigint = 0 OR v.price_cents <= $7::bigint)
      )
  )
ORDER BY
    CASE WHEN $8::text = 'price_asc'  THEN mv.price_cents END ASC,
    CASE WHEN $8::text = 'price_desc' THEN mv.price_cents END DESC,
    CASE WHEN $8::text = 'rating'     THEN coalesce(rv.rating, 0) END DESC,
    p.published_at DESC, p.id DESC
LIMIT $10::integer OFFSET $9::integer
;
