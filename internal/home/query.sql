-- The root categories only; children hang off these.

-- One tile query for the three rows the home page draws: a campaign's products
-- in the position the back office set, a department's products, or the newest
-- of the shop. A NULL filter is no filter. status = 'active' is a literal, not
-- a parameter, so the partial index stays usable. A product that can be bought
-- and has a photograph comes before one that cannot, whatever the order within.
-- name: HomeTiles :many
WITH RECURSIVE d AS (
    SELECT c.id FROM categories c WHERE c.id = sqlc.narg(department_id)::uuid
    UNION ALL
    SELECT c.id FROM categories c JOIN d ON c.parent_id = d.id
)
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    coalesce(b.name, '') AS brand,
    mv.price_cents AS tile_price_cents,
    -- Whether that price is the cheapest of several, so a card can say "from"
    -- rather than state one variant's price as the product's. On a campaign's row
    -- the price can be a discounted variant's with a cheaper one that can be
    -- bought beside it.
    (EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    ) AND (sqlc.narg(campaign_id)::uuid IS NULL OR NOT EXISTS (
        SELECT 1 FROM product_variants cv
        WHERE cv.product_id = p.id AND cv.is_active AND cv.price_cents < mv.price_cents
          AND cv.stock_quantity > cv.safety_stock
    )))::boolean AS price_varies,
    mv.compare_at_price_cents,
    EXISTS (
        SELECT 1 FROM sale_campaign_products fp
        JOIN listed_campaigns l ON l.id = fp.campaign_id
        WHERE fp.product_id = p.id
    ) AS in_campaign,
    coalesce(rv.rating, 0)::float8 AS rating,
    coalesce(rv.n, 0)::bigint AS rating_count,
    -- A product with no image yields NULL, which sqlc types as a non-null string
    -- and pgx cannot scan.
    coalesce(img.media_digest, '') AS image_digest,
    coalesce(img.bundled_key, '') AS image_bundled_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height,
    -- Sellable, not merely present: record_inventory_movement refuses a hold
    -- that would take stock below safety_stock.
    EXISTS (
        SELECT 1 FROM product_variants
        WHERE product_id = p.id AND is_active
          AND stock_quantity > safety_stock
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
    ), ARRAY[]::text[])::text[] AS colours
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    -- A buyable variant first: the price on a tile is a promise. Falls back to
    -- the cheapest overall so a sold-out product still shows what it costs. On a
    -- campaign's row a discounted variant leads, as on the campaign's own page.
    ORDER BY (sqlc.narg(campaign_id)::uuid IS NOT NULL AND compare_at_price_cents IS NOT NULL
              AND compare_at_price_cents > price_cents AND stock_quantity > safety_stock) DESC,
             (stock_quantity > safety_stock) DESC,
             (sqlc.narg(campaign_id)::uuid IS NOT NULL AND compare_at_price_cents IS NOT NULL
              AND compare_at_price_cents > price_cents) DESC,
             price_cents, id
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n, sum(rating) AS s
    FROM visible_reviews
    WHERE product_id = p.id
) rv ON true
LEFT JOIN LATERAL (
    SELECT media_digest, bundled_key, alt_text, alt_text_en, width, height
    FROM product_images
    WHERE product_id = p.id
    ORDER BY position
    LIMIT 1
) img ON true
WHERE p.status = 'active'
  AND (sqlc.narg(campaign_id)::uuid IS NULL OR EXISTS (
      SELECT 1 FROM sale_campaign_products cp
      WHERE cp.campaign_id = sqlc.narg(campaign_id)::uuid AND cp.product_id = p.id
  ))
  AND (sqlc.narg(department_id)::uuid IS NULL OR p.category_id IN (SELECT d.id FROM d))
ORDER BY NOT (EXISTS (
             SELECT 1 FROM product_variants sv
             WHERE sv.product_id = p.id AND sv.is_active
               AND sv.stock_quantity > sv.safety_stock
         ) AND num_nonnulls(img.media_digest, img.bundled_key) > 0),
         (SELECT cp.position FROM sale_campaign_products cp
          WHERE cp.campaign_id = sqlc.narg(campaign_id)::uuid AND cp.product_id = p.id) NULLS LAST,
         p.published_at DESC, p.id
LIMIT @max_tiles::integer;

-- The scheduled slides in the order an editor queued them by `position`. The
-- window is judged against the database's clock, which wrote the timestamps.
-- name: HeroSlides :many
-- Every word follows the visitor; the HREFs do not, because a link goes to one
-- page. The nullable fields are coalesced as well as wrapped: localized_name(NULL,
-- NULL, ...) is NULL and sqlc types the result as non-null.
SELECT h.id, coalesce(localized_name(h.eyebrow, h.eyebrow_en, @locale::text), '')::text
           AS eyebrow,
       localized_name(h.headline, h.headline_en, @locale::text) AS headline,
       coalesce(localized_name(h.body, h.body_en, @locale::text), '')::text AS body,
       localized_name(h.primary_cta_label, h.primary_cta_label_en, @locale::text)
           AS primary_cta_label,
       h.primary_cta_href,
       coalesce(localized_name(h.secondary_cta_label, h.secondary_cta_label_en,
                               @locale::text), '')::text AS secondary_cta_label,
       h.secondary_cta_href, h.media_digest,
       coalesce(localized_name(h.image_alt, h.image_alt_en, @locale::text), '')::text
           AS image_alt,
       -- Dimensions come from media_objects: one row of bytes, one row of size.
       -- Both, because a width without a height reserves no space in the layout.
       coalesce(m.width, 0)::integer AS image_width,
       coalesce(m.height, 0)::integer AS image_height
FROM hero_slides h
LEFT JOIN media_objects m ON m.digest = h.media_digest
WHERE h.is_active
  AND (h.starts_at IS NULL OR h.starts_at <= now())
  AND (h.ends_at IS NULL OR h.ends_at > now())
ORDER BY h.position, h.id
LIMIT @max_slides::integer;

-- The direct children of every root, in the order the department's own page
-- lists them.
-- name: HomeSubcategories :many
SELECT c.parent_id, localized_name(c.name, c.name_en, @locale::text) AS name
FROM categories c
JOIN categories r ON r.id = c.parent_id AND r.parent_id IS NULL
ORDER BY c.position, c.name, c.id;
