-- name: DealProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    coalesce(b.name, '') AS brand,
    -- Named for what it IS: the price this tile shows, which on THIS page is the
    -- discounted variant rather than the cheapest one. Calling it
    -- min_price_cents here would be a claim the LATERAL below does not make.
    mv.price_cents AS tile_price_cents,
    -- "From X" says X is the bottom of the range, so it needs BOTH halves:
    -- something dearer exists AND nothing cheaper does. On the listing the
    -- chosen variant is the cheapest buyable one, so the second half is free;
    -- here it is not, and asking only the first put 起 on a price with cheaper
    -- variants sitting under it.
    (EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    ) AND NOT EXISTS (
        SELECT 1 FROM product_variants cv
        WHERE cv.product_id = p.id AND cv.is_active AND cv.price_cents < mv.price_cents
    ))::boolean AS price_varies,
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
    coalesce(img.media_digest, '') AS image_digest,
    coalesce(img.bundled_key, '') AS image_bundled_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
-- A DISCOUNTED variant that can be bought first, so the price the tile strikes
-- is one the shopper can add to the cart. The listing's LATERAL takes the
-- cheapest buyable one, which is a different variant whenever the discounted
-- one is dearer.
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (compare_at_price_cents IS NOT NULL AND compare_at_price_cents > price_cents
              AND stock_quantity > safety_stock) DESC,
             (stock_quantity > safety_stock) DESC,
             (compare_at_price_cents IS NOT NULL AND compare_at_price_cents > price_cents) DESC,
             price_cents
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
WHERE p.status = 'active'
  AND EXISTS (SELECT 1 FROM campaign_deals d WHERE d.product_id = p.id)
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
  AND EXISTS (SELECT 1 FROM campaign_deals d WHERE d.product_id = p.id);

-- Any active campaign by its slug, inside its window or not: the page says
-- honestly whether it has not started or has ended.
-- name: CampaignBySlug :one
SELECT c.id, c.slug, localized_name(c.title, c.title_en, @locale::text) AS title,
       c.starts_at, c.ends_at,
       c.tone,
       coalesce(c.media_digest, '')::text AS image_digest,
       coalesce(c.bundled_key, '')::text AS image_bundled_key,
       coalesce(localized_name(c.image_alt, c.image_alt_en, @locale::text), '')::text AS image_alt,
       coalesce(m.width, 0)::integer AS image_width
FROM sale_campaigns c
LEFT JOIN media_objects m ON m.digest = c.media_digest
WHERE c.slug = @slug::text AND c.is_active;

-- A campaign left out keeps its page at /s/{slug} (CampaignBySlug), reachable by
-- a direct link.
-- name: ListedCampaigns :many
SELECT c.id, c.slug, localized_name(c.title, c.title_en, @locale::text) AS title,
       c.starts_at, c.ends_at, c.tone,
       coalesce(c.media_digest, '')::text AS image_digest,
       coalesce(c.bundled_key, '')::text AS image_bundled_key,
       coalesce(localized_name(c.image_alt, c.image_alt_en, @locale::text), '')::text AS image_alt,
       coalesce(m.width, 0)::integer AS image_width,
       (SELECT count(*) FROM sale_campaign_products cp
        JOIN products p ON p.id = cp.product_id
        WHERE cp.campaign_id = c.id AND p.status = 'active')::bigint AS products
FROM sale_campaigns c
JOIN listed_campaigns l ON l.id = c.id
LEFT JOIN media_objects m ON m.digest = c.media_digest
ORDER BY c.ends_at, c.id
LIMIT @page_size::integer OFFSET @page_offset::integer;

-- name: ListedCampaignsCount :one
SELECT count(*)::bigint FROM listed_campaigns;

-- Whether /deals has anything to buy: a product DealProducts lists. The header
-- asks on every page; it stops at the first row. Its plan has not been measured.
-- name: DealsHaveSomethingToBuy :one
SELECT EXISTS (SELECT 1 FROM campaign_deals)::boolean AS offered;

-- Ordered by the position the back office set: a campaign is merchandising.
-- name: CampaignProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    coalesce(b.name, '') AS brand,
    mv.price_cents AS tile_price_cents,
    -- Whether that price is the cheapest of several, so a card can say "from"
    -- rather than state one variant's price as the product's.
    NOT EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents < mv.price_cents
          AND dv.stock_quantity > dv.safety_stock
    ) AND EXISTS (
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
    coalesce(img.media_digest, '') AS image_digest,
    coalesce(img.bundled_key, '') AS image_bundled_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM sale_campaign_products cp
JOIN products p ON p.id = cp.product_id
LEFT JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    -- A discounted variant that can be bought, else any variant that can be
    -- bought, so the price shown is never one a shopper cannot pay; a missing
    -- compare price is not a discount.
    ORDER BY (compare_at_price_cents IS NOT NULL AND compare_at_price_cents > price_cents
              AND stock_quantity > safety_stock) DESC,
             (stock_quantity > safety_stock) DESC,
             (compare_at_price_cents IS NOT NULL AND compare_at_price_cents > price_cents) DESC,
             price_cents, id
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
WHERE cp.campaign_id = $1 AND p.status = 'active'
-- Sellable products first, then the position the back office set.
ORDER BY in_stock DESC, cp.position, p.id;
