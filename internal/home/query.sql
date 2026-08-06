-- The root categories, in their display order, for the home page's category
-- tiles. Children hang off these but the home shows only the top level.
-- name: HomeCategories :many
SELECT id, slug, localized_name(name, name_en, @locale::text) AS name, icon_key
FROM categories
WHERE parent_id IS NULL
ORDER BY position;

-- The recommended product tiles: the cheapest active variant's price, a
-- Bayesian-averaged rating (prior weight 5, global mean, so a lone 5-star does
-- not outrank a well-reviewed 4.6), and the primary image. status = 'active' is
-- a literal, not a parameter, so the partial index stays usable. The read model
-- is per-view by measurement (docs/decisions/001-home-read-model.md).
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
    mv.compare_at_price_cents,
    coalesce(rv.rating, 0)::float8 AS rating,
    coalesce(rv.n, 0)::bigint AS rating_count,
    -- LEFT JOIN: a product with no image yields NULL, which sqlc types as a
    -- non-null string and pgx cannot scan. coalesce keeps it a real empty
    -- string the view treats as "no image, show the placeholder".
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height,
    -- Sellable, not merely present. record_inventory_movement refuses a sale or
    -- hold that would take stock below safety_stock, so a variant sitting AT the
    -- floor cannot be bought however available `stock_quantity > 0` makes it
    -- look. Reading it the naive way makes the storefront promise what the
    -- database is going to refuse.
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
    -- A buyable variant first. The price on a tile is a promise, so when
    -- anything can be bought it has to be the price of something that can;
    -- otherwise the cheapest sold-out colour sets a figure no visitor can pay.
    -- Falls back to the cheapest overall so a wholly sold-out product still
    -- shows what it costs rather than disappearing.
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

-- The hero slide showing right now.
--
-- ONE, not a carousel. A rotating hero moves what somebody is reading, needs
-- JavaScript to rotate, and is a keyboard trap unless carefully built — all of
-- which the write-face rule argues against for the first thing on the page.
-- `position` is how an editor queues the next one, not how five rotate.
--
-- The window is judged in SQL against the DATABASE's clock, for the same reason
-- the coupon and campaign windows are: these timestamps were written by now()
-- here, and comparing them to Go's time.Now() is comparing two clocks.
-- name: CurrentHeroSlide :one
-- Every word follows the visitor; the HREFs do not, because a link goes to one page.
-- pages.DefaultHero has always been translated because it is compiled in — a
-- SCHEDULED slide was not, so using the feature turned the largest thing on the home
-- page Chinese for everybody.
-- The nullable fields are COALESCED, not merely wrapped. localized_name(NULL, NULL,
-- ...) is NULL, and sqlc types the function's result as non-null — so a slide with no
-- eyebrow made the home page fail to scan its own hero. Found by a mutation run
-- against a slide that had one; the seed ships no slides at all, which is exactly why
-- an empty table has to be a working site rather than an untested path.
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
       -- The image's width comes from media_objects, not from a column on this
       -- table. product_images already showed what happens when a stored
       -- object's dimensions are copied next to every reference: two rows
       -- disagree and a unique index gets bolted on to stop them. One row of
       -- bytes, one row of dimensions.
       coalesce(m.width, 0)::integer AS image_width
FROM hero_slides h
LEFT JOIN media_objects m ON m.digest = h.image_key
WHERE h.is_active
  AND (h.starts_at IS NULL OR h.starts_at <= now())
  AND (h.ends_at IS NULL OR h.ends_at > now())
ORDER BY h.position, h.id
LIMIT 1;

-- The banner running right now, if there is one.
--
-- The window is judged in SQL against the database's clock, for the reason
-- every other window in goen is: these timestamps were written by now() here.
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

-- The header's category row.
--
-- The same root categories the home page tiles, but only what a link needs — the
-- header renders on every page, so it reads three columns and no icon. It is a
-- query rather than a list in Go because a category has ONE name and one place it
-- is translated; the header carrying its own copy is how 耳機 came to point at a
-- category called audio.
-- name: NavCategories :many
SELECT slug, localized_name(name, name_en, @locale::text) AS name
FROM categories
WHERE parent_id IS NULL
ORDER BY position, name;
