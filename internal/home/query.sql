-- One tile query for every shelf the home page draws: a department's products
-- (the department and every category under it), the products under a price, or
-- the shop's newest. A NULL or zero filter is no filter. status = 'active' is a
-- literal, not a parameter, so the partial index stays usable. A product that
-- can be bought and has a photograph comes first; within each group the price,
-- where price_order asks for it, then the newest.
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
    -- rather than state one variant's price as the product's.
    EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    )::boolean AS price_varies,
    mv.compare_at_price_cents,
    EXISTS (
        SELECT 1 FROM sale_campaign_products fp
        JOIN listed_campaigns l ON l.id = fp.campaign_id
        WHERE fp.product_id = p.id
    ) AS in_campaign,
    -- When the sale ends: the soonest end among the listed campaigns the product
    -- is in, so a card can say how long its struck price holds. The epoch when
    -- it is in none, which in_campaign already says; sqlc would type a NULL as
    -- a time it cannot scan.
    coalesce((
        SELECT min(sc.ends_at) FROM sale_campaign_products fp
        JOIN listed_campaigns l ON l.id = fp.campaign_id
        JOIN sale_campaigns sc ON sc.id = l.id
        WHERE fp.product_id = p.id
    ), 'epoch')::timestamptz AS sale_ends_at,
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
    coalesce(p.warranty_months, 0)::integer AS warranty_months,
    -- The one variant a card can add without a choice: the product's only
    -- active variant, while it can be bought; '' for any other product.
    coalesce((
        SELECT min(qv.id::text) FROM product_variants qv
        WHERE qv.product_id = p.id AND qv.is_active
        HAVING count(*) = 1 AND bool_and(qv.stock_quantity > qv.safety_stock)
    ), '')::text AS quick_add,
    -- The first three specification values, in the order the product lists them.
    ARRAY(
        SELECT localized_name(s.value, s.value_en, @locale::text)
        FROM product_specs s WHERE s.product_id = p.id
        ORDER BY s.position, s.id
        LIMIT 3
    )::text[] AS highlights,
    -- The colour option's values in use, each with its hex, its name, its name
    -- again where no variant of it is in stock, and the first photograph that
    -- shows it, all in one order.
    ARRAY(
        SELECT cv.swatch_hex FROM product_option_values cv
        WHERE cv.option_id = col.id AND EXISTS (
            SELECT 1 FROM variant_option_values vov
            JOIN product_variants pv ON pv.id = vov.variant_id
            WHERE vov.option_value_id = cv.id AND pv.is_active
        )
        ORDER BY cv.position, cv.id
    )::text[] AS colours,
    ARRAY(
        SELECT CASE WHEN EXISTS (
            SELECT 1 FROM variant_option_values sov
            JOIN product_variants spv ON spv.id = sov.variant_id
            WHERE sov.option_value_id = cv.id AND spv.is_active AND spv.stock_quantity > spv.safety_stock
        ) THEN '' ELSE localized_name(cv.value, cv.value_en, @locale::text) END
        FROM product_option_values cv
        WHERE cv.option_id = col.id AND EXISTS (
            SELECT 1 FROM variant_option_values vov
            JOIN product_variants pv ON pv.id = vov.variant_id
            WHERE vov.option_value_id = cv.id AND pv.is_active
        )
        ORDER BY cv.position, cv.id
    )::text[] AS colours_sold_out,
    ARRAY(
        SELECT localized_name(cv.value, cv.value_en, @locale::text)
        FROM product_option_values cv
        WHERE cv.option_id = col.id AND EXISTS (
            SELECT 1 FROM variant_option_values vov
            JOIN product_variants pv ON pv.id = vov.variant_id
            WHERE vov.option_value_id = cv.id AND pv.is_active
        )
        ORDER BY cv.position, cv.id
    )::text[] AS colour_names,
    ARRAY(
        SELECT coalesce(ci.media_digest, '')
        FROM product_option_values cv
        LEFT JOIN LATERAL (
            SELECT media_digest FROM product_images
            WHERE product_id = p.id AND option_value_id = cv.id
            ORDER BY position, id LIMIT 1
        ) ci ON true
        WHERE cv.option_id = col.id AND EXISTS (
            SELECT 1 FROM variant_option_values vov
            JOIN product_variants pv ON pv.id = vov.variant_id
            WHERE vov.option_value_id = cv.id AND pv.is_active
        )
        ORDER BY cv.position, cv.id
    )::text[] AS colour_digests,
    ARRAY(
        SELECT coalesce(ci.bundled_key, '')
        FROM product_option_values cv
        LEFT JOIN LATERAL (
            SELECT bundled_key FROM product_images
            WHERE product_id = p.id AND option_value_id = cv.id
            ORDER BY position, id LIMIT 1
        ) ci ON true
        WHERE cv.option_id = col.id AND EXISTS (
            SELECT 1 FROM variant_option_values vov
            JOIN product_variants pv ON pv.id = vov.variant_id
            WHERE vov.option_value_id = cv.id AND pv.is_active
        )
        ORDER BY cv.position, cv.id
    )::text[] AS colour_bundled_keys,
    ARRAY(
        SELECT coalesce(ci.width, 0)
        FROM product_option_values cv
        LEFT JOIN LATERAL (
            SELECT width FROM product_images
            WHERE product_id = p.id AND option_value_id = cv.id
            ORDER BY position, id LIMIT 1
        ) ci ON true
        WHERE cv.option_id = col.id AND EXISTS (
            SELECT 1 FROM variant_option_values vov
            JOIN product_variants pv ON pv.id = vov.variant_id
            WHERE vov.option_value_id = cv.id AND pv.is_active
        )
        ORDER BY cv.position, cv.id
    )::integer[] AS colour_widths
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    -- A buyable variant first: the price on a tile is a promise. Falls back to
    -- the cheapest overall so a sold-out product still shows what it costs.
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents, id
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n
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
LEFT JOIN LATERAL (
    -- The colour option: the first option all of whose values in use are colours.
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
) col ON true
WHERE p.status = 'active'
  AND (sqlc.narg(department_id)::uuid IS NULL OR p.category_id IN (SELECT d.id FROM d))
  -- Under a price, only what can be bought: the rail is for adding to a cart.
  AND (@below_cents::bigint = 0 OR (mv.price_cents < @below_cents::bigint AND EXISTS (
      SELECT 1 FROM product_variants bv
      WHERE bv.product_id = p.id AND bv.is_active AND bv.stock_quantity > bv.safety_stock
  )))
  AND (NOT @in_stock_only::boolean OR EXISTS (
      SELECT 1 FROM product_variants iv
      WHERE iv.product_id = p.id AND iv.is_active AND iv.stock_quantity > iv.safety_stock
  ))
ORDER BY NOT (EXISTS (
             SELECT 1 FROM product_variants sv
             WHERE sv.product_id = p.id AND sv.is_active
               AND sv.stock_quantity > sv.safety_stock
         ) AND num_nonnulls(img.media_digest, img.bundled_key) > 0),
         CASE WHEN @price_order::text = 'asc' THEN mv.price_cents END ASC,
         CASE WHEN @price_order::text = 'desc' THEN mv.price_cents END DESC,
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
       h.primary_cta_href, h.media_digest,
       coalesce(m.width, 0)::integer AS image_width
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

-- One department's direct children, each with the active products of its whole
-- subtree, for the featured shelf's filter; counted as the header counts a
-- department (site's DepartmentStock).
-- name: HomeShelfCategories :many
WITH RECURSIVE tree AS (
    SELECT c.id AS top, c.id FROM categories c WHERE c.parent_id = @department_id::uuid
    UNION ALL
    SELECT t.top, k.id FROM categories k JOIN tree t ON k.parent_id = t.id
)
SELECT c.id, c.slug, localized_name(c.name, c.name_en, @locale::text) AS name,
       (SELECT count(*) FROM products p JOIN tree t ON t.id = p.category_id
        WHERE t.top = c.id AND p.status = 'active')::bigint AS products
FROM categories c
WHERE c.parent_id = @department_id::uuid
ORDER BY c.position, c.name, c.id;

-- Whether the featured shelf puts its products side by side, for the compare
-- toggle on its cards: the shown category's own answer, else its department's,
-- as catalog's ComparableCategoryIDs inherits it. The shelf is a department or
-- one of its children, and only a department stores an answer
-- (admin/taxonomy), so this is the answer for every card on the shelf.
-- name: ShelfCompares :one
SELECT coalesce(s.comparable, d.comparable, false)::boolean AS compares
FROM categories s
LEFT JOIN categories d ON d.id = s.parent_id
WHERE s.id = @category_id::uuid;
