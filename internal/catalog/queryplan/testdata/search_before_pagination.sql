-- Semantic oracle: canonical SearchProducts from 4ed2935 before page projection deferral.
-- name: SearchProducts :many
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
  AND (p.name ILIKE $2::text
       OR coalesce(p.name_en, '') ILIKE $2::text
       OR coalesce(p.summary, '') ILIKE $2::text
       OR coalesce(p.summary_en, '') ILIKE $2::text
       OR b.name ILIKE $2::text
       OR EXISTS (
           SELECT 1 FROM product_specs ps
           WHERE ps.product_id = p.id
             AND (ps.label ILIKE $2::text
                  OR coalesce(ps.label_en, '') ILIKE $2::text
                  OR ps.value ILIKE $2::text
                  OR coalesce(ps.value_en, '') ILIKE $2::text)
       ))
ORDER BY
    -- A name match outranks a summary or brand match. Either name counts.
    (p.name ILIKE $2::text OR coalesce(p.name_en, '') ILIKE $2::text) DESC,
    p.published_at DESC, p.id DESC
LIMIT $4::integer OFFSET $3::integer

