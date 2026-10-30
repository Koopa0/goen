-- The cheapest sellable variant sets the price, the way the listing reads it.
-- name: WishlistItems :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    p.summary,
    coalesce(b.name, '') AS brand,
    mv.price_cents AS min_price_cents,
    -- Whether that price is the cheapest of several, so a card can say "from"
    -- rather than state one variant's price as the product's.
    EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    ) AS price_varies,
    mv.compare_at_price_cents,
    EXISTS (
        SELECT 1 FROM sale_campaign_products fp
        JOIN listed_campaigns l ON l.id = fp.campaign_id
        WHERE fp.product_id = p.id
    ) AS in_campaign,
    coalesce(rv.rating, 0)::float8 AS rating,
    coalesce(rv.n, 0)::bigint AS rating_count,
    EXISTS (
        SELECT 1 FROM product_variants sv
        WHERE sv.product_id = p.id AND sv.is_active
          AND sv.stock_quantity > sv.safety_stock
    ) AS in_stock,
    coalesce((
        SELECT array_agg(cv.swatch_hex ORDER BY cv.position, cv.id)
        FROM product_option_values cv
        WHERE cv.option_id = (
            SELECT co.id FROM product_options co
            WHERE co.product_id = p.id
              AND EXISTS (
                  SELECT 1 FROM product_option_values x
                  WHERE x.option_id = co.id AND EXISTS (
                      SELECT 1 FROM variant_option_values vov
                      JOIN product_variants pv ON pv.id = vov.variant_id
                      WHERE vov.option_value_id = x.id AND pv.is_active
                  )
              )
              AND NOT EXISTS (
                  SELECT 1 FROM product_option_values x
                  WHERE x.option_id = co.id AND x.swatch_hex IS NULL AND EXISTS (
                      SELECT 1 FROM variant_option_values vov
                      JOIN product_variants pv ON pv.id = vov.variant_id
                      WHERE vov.option_value_id = x.id AND pv.is_active
                  )
              )
            ORDER BY co.position, co.id
            LIMIT 1
        )
          AND EXISTS (
                      SELECT 1 FROM variant_option_values vov
                      JOIN product_variants pv ON pv.id = vov.variant_id
                      WHERE vov.option_value_id = cv.id AND pv.is_active
                  )
    ), ARRAY[]::text[])::text[] AS colours,
    -- The one variant a product has, when it has only one and it is in stock:
    -- the only case where saying "add to cart" names what goes in the cart.
    -- The nil uuid when there is none.
    coalesce((
        SELECT CASE WHEN count(*) = 1 AND bool_and(sv.stock_quantity > sv.safety_stock)
                    THEN (array_agg(sv.id))[1] END
        FROM product_variants sv
        WHERE sv.product_id = p.id AND sv.is_active
    ), '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS sole_variant_id,
    coalesce(img.media_digest, '') AS image_digest,
    coalesce(img.bundled_key, '') AS image_bundled_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM wishlist_items w
JOIN products p ON p.id = w.product_id
LEFT JOIN brands b ON b.id = p.brand_id
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
    SELECT media_digest, bundled_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
-- status = 'active' as a LITERAL: a parameter cannot use the partial index.
WHERE w.user_id = $1 AND p.status = 'active'
ORDER BY w.created_at DESC, p.id DESC;

-- name: AddWishlistItem :exec
INSERT INTO wishlist_items (user_id, product_id)
SELECT $1, p.id FROM products p WHERE p.slug = @slug::text AND p.status = 'active'
ON CONFLICT (user_id, product_id) DO NOTHING;

-- name: RemoveWishlistItem :exec
DELETE FROM wishlist_items w
USING products p
WHERE w.product_id = p.id AND w.user_id = $1 AND p.slug = @slug::text;

-- name: WishlistHas :one
SELECT EXISTS (
    SELECT 1 FROM wishlist_items w JOIN products p ON p.id = w.product_id
    WHERE w.user_id = $1 AND p.slug = @slug::text
);

-- Whether the customer has a saved product the wishlist page would show.
-- name: WishlistAnyListed :one
SELECT EXISTS (
    SELECT 1 FROM wishlist_items w JOIN products p ON p.id = w.product_id
    WHERE w.user_id = $1 AND p.status = 'active'
);
