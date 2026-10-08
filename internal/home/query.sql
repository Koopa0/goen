-- The root categories only; children hang off these.

-- One tile query for the three rows the home page draws: a campaign's products
-- in the position the back office set, a department's products, or the newest
-- of the shop. A NULL filter is no filter. status = 'active' is a literal, not
-- a parameter, so the partial index stays usable.
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
    SELECT storage_key, alt_text, alt_text_en, width, height
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
ORDER BY (SELECT cp.position FROM sale_campaign_products cp
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
       h.secondary_cta_href, h.image_key,
       coalesce(localized_name(h.image_alt, h.image_alt_en, @locale::text), '')::text
           AS image_alt,
       -- Dimensions come from media_objects: one row of bytes, one row of size.
       -- Both, because a width without a height reserves no space in the layout.
       coalesce(m.width, 0)::integer AS image_width,
       coalesce(m.height, 0)::integer AS image_height
FROM hero_slides h
LEFT JOIN media_objects m ON m.digest = h.image_key
WHERE h.is_active
  AND (h.starts_at IS NULL OR h.starts_at <= now())
  AND (h.ends_at IS NULL OR h.ends_at > now())
ORDER BY h.position, h.id
LIMIT @max_slides::integer;

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
-- The header's row AND the home page's tiles: the same six rows in the same
-- order, because two queries answering "in what order are the categories" gave
-- two answers the moment CreateCategory's max(position)+1 handed out a
-- duplicate. Nothing stops it: there is no unique index on (parent_id,
-- position), so two staff creating a category at once both read the same max.
-- A root's tone is its own, or 'stone'. The photograph is the department's own.
-- name: RootCategories :many
SELECT c.id, c.slug, localized_name(c.name, c.name_en, @locale::text) AS name, c.icon_key,
       coalesce(c.tone, 'stone')::text AS tone,
       coalesce(c.image_key, '')::text AS image_key,
       coalesce(localized_name(c.image_alt, c.image_alt_en, @locale::text), '')::text AS image_alt,
       coalesce(m.width, 0)::integer AS image_width
FROM categories c
LEFT JOIN media_objects m ON m.digest = c.image_key
WHERE c.parent_id IS NULL
ORDER BY c.position, c.name, c.id;

-- The direct children of every root, in the order the department's own page
-- lists them.
-- name: HomeSubcategories :many
SELECT c.parent_id, localized_name(c.name, c.name_en, @locale::text) AS name
FROM categories c
JOIN categories r ON r.id = c.parent_id AND r.parent_id IS NULL
ORDER BY c.position, c.name, c.id;

-- How many active products each root holds across its whole subtree: a
-- department with fewer than three has no band to show, and the header prints
-- it beside each department in the phone menu, so it runs on every page with a
-- header. Unlike the header's other reads it counts the catalogue, not the
-- categories, and its plan has not been measured.
-- name: HomeDepartmentStock :many
WITH RECURSIVE tree AS (
    SELECT id, id AS root FROM categories WHERE parent_id IS NULL
    UNION ALL
    SELECT k.id, t.root FROM categories k JOIN tree t ON k.parent_id = t.id
)
SELECT t.root AS id, count(p.id)::bigint AS products
FROM tree t
JOIN products p ON p.category_id = t.id AND p.status = 'active'
GROUP BY t.root;

-- The sub-categories under every root, for the header's department panels. One
-- read for all of them, in the order the catalogue lists them, so a header with
-- seven departments is two queries and not eight.
-- name: ChildCategories :many
SELECT parent_id, slug, localized_name(name, name_en, @locale::text) AS name
FROM categories
WHERE parent_id IS NOT NULL
ORDER BY position, name, id;

-- Each department's three newest products that can be bought, for its header
-- panel. One read for all of them, like ChildCategories, on every page with a
-- header, so its cost is bounded by the number of categories and never by the
-- catalogue: each category reads newest-first off
-- products_category_published_idx and stops at its third buyable product, and
-- a department's three newest are among its categories' three newest. Only
-- those few rows are ranked per department, and only the three kept are priced
-- and given a picture. The price is the cheapest buyable variant's, the one a
-- tile would state.
-- name: NavPicks :many
WITH RECURSIVE tree AS (
    SELECT id, id AS root FROM categories WHERE parent_id IS NULL
    UNION ALL
    SELECT k.id, t.root FROM categories k JOIN tree t ON k.parent_id = t.id
),
recent AS (
    SELECT t.root, p.id, p.slug, p.name, p.name_en, p.published_at,
           row_number() OVER (PARTITION BY t.root ORDER BY p.published_at DESC, p.id DESC) AS nth
    FROM tree t
    CROSS JOIN LATERAL (
        SELECT p.id, p.slug, p.name, p.name_en, p.published_at
        FROM products p
        WHERE p.category_id = t.id AND p.status = 'active'
          AND EXISTS (
              SELECT 1 FROM product_variants v
              WHERE v.product_id = p.id AND v.is_active AND v.stock_quantity > v.safety_stock
          )
        ORDER BY p.published_at DESC, p.id DESC
        LIMIT 3
    ) p
)
SELECT r.root AS root_id,
       r.slug,
       localized_name(r.name, r.name_en, @locale::text) AS name,
       mv.price_cents,
       EXISTS (
           SELECT 1 FROM product_variants dv
           WHERE dv.product_id = r.id AND dv.is_active AND dv.price_cents > mv.price_cents
       ) AS price_varies,
       coalesce(img.storage_key, '') AS image_key,
       coalesce(img.width, 0)::integer AS image_width
FROM recent r
JOIN LATERAL (
    SELECT price_cents
    FROM product_variants
    WHERE product_id = r.id AND is_active AND stock_quantity > safety_stock
    ORDER BY price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT storage_key, width
    FROM product_images
    WHERE product_id = r.id
    ORDER BY position
    LIMIT 1
) img ON true
WHERE r.nth <= 3
ORDER BY r.root, r.published_at DESC, r.id DESC;

-- with_pickup is false where the store map is not configured: checkout offers no
-- pickup there, so a floor or threshold that counted it would promise a price
-- nobody can choose.
-- The strip makes one claim, so it is the cart's: the amount at which EVERY
-- method is free, the highest threshold. A method that costs nothing is free at
-- any amount and takes no part; one that charges and never turns free leaves no
-- claim. coalesce AND cast, because the aggregates over an empty set are NULL
-- and sqlc types the result as non-null.
-- name: FreeDeliveryThreshold :one
SELECT coalesce(
    CASE WHEN bool_or(coalesce(v.free_over_cents, 0) = 0) THEN 0 ELSE max(v.free_over_cents) END,
    0)::bigint AS free_over_cents
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE sm.is_active
  AND (@with_pickup::boolean OR sm.destination_kind <> 'pickup_point')
  AND v.effective_at <= now()
  AND v.fee_cents > 0
  AND v.id = (SELECT id FROM shipping_method_versions
              WHERE method_id = sm.id AND effective_at <= now()
              ORDER BY effective_at DESC LIMIT 1);

-- with_pickup is false where the store map is not configured: checkout offers no
-- pickup there, so a floor or threshold that counted it would promise a price
-- nobody can choose.
-- MIN across methods: the strip states one floor, and the honest one is the
-- lowest fee any active method charges; a method that is always free charges
-- none, so it is not the fee below the threshold. coalesce AND cast, because
-- min() over an empty set is NULL and sqlc types the result as non-null.
-- name: LowestDeliveryFee :one
SELECT coalesce(min(v.fee_cents), 0)::bigint AS fee_cents
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE sm.is_active
  AND (@with_pickup::boolean OR sm.destination_kind <> 'pickup_point')
  AND v.effective_at <= now()
  AND v.fee_cents > 0
  AND v.id = (SELECT id FROM shipping_method_versions
              WHERE method_id = sm.id AND effective_at <= now()
              ORDER BY effective_at DESC LIMIT 1);
