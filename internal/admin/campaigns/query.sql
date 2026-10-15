-- name: AdminCampaigns :many
SELECT json_build_object('Rank', c.is_active, 'At', c.ends_at, 'ID', c.id)::text AS page_cursor, c.id, c.slug, c.title, c.starts_at, c.ends_at, c.is_active,
       (SELECT count(*) FROM sale_campaign_products p WHERE p.campaign_id = c.id)::bigint AS products,
       EXISTS (SELECT 1 FROM running_campaigns r WHERE r.id = c.id) AS is_running,
       EXISTS (SELECT 1 FROM campaign_deals d WHERE d.campaign_id = c.id) AS is_sellable
FROM sale_campaigns c
WHERE (NOT @has_cursor::boolean OR (c.is_active < @after_rank::boolean)
       OR (c.is_active = @after_rank::boolean AND c.ends_at < @after_at::timestamptz)
       OR (c.is_active = @after_rank::boolean AND c.ends_at = @after_at::timestamptz AND c.id < @after_id::uuid))
ORDER BY c.is_active DESC, c.ends_at DESC, c.id DESC
LIMIT @row_limit::integer;

-- name: CreateCampaign :exec
INSERT INTO sale_campaigns (slug, title, title_en, tone, ends_at)
VALUES (@slug::text, @title::text, nullif(@title_en::text, ''), @tone::text,
        now() + (@days::integer || ' days')::interval);

-- An upload replaces an embedded header too: sale_campaigns_image_one_source
-- allows one of the two.
-- name: SetCampaignImage :execrows
UPDATE sale_campaigns
SET media_digest = @media_digest::text, bundled_key = NULL, image_alt = @image_alt::text,
    image_alt_en = nullif(@image_alt_en::text, '')
WHERE slug = @slug::text;

-- name: ClearCampaignImage :execrows
UPDATE sale_campaigns
SET media_digest = NULL, bundled_key = NULL, image_alt = NULL, image_alt_en = NULL
WHERE slug = @slug::text;

-- The stored width comes from media_objects, and is 0 for an embedded file.
-- name: AdminCampaignImage :one
SELECT coalesce(c.media_digest, c.bundled_key, '')::text AS image_key,
       coalesce(c.image_alt, '')::text AS image_alt,
       coalesce(c.image_alt_en, '')::text AS image_alt_en,
       c.tone,
       coalesce(m.width, 0)::integer AS image_width
FROM sale_campaigns c
LEFT JOIN media_objects m ON m.digest = c.media_digest
WHERE c.slug = @slug::text;

-- name: AdminCampaign :one
SELECT c.title, localized_name(c.title, c.title_en, @locale::text) AS label, c.starts_at, c.ends_at, c.is_active,
       EXISTS (SELECT 1 FROM running_campaigns r WHERE r.id = c.id) AS is_running,
       EXISTS (SELECT 1 FROM campaign_deals d WHERE d.campaign_id = c.id) AS is_sellable
FROM sale_campaigns c
WHERE c.slug = @slug::text;

-- Locked so a concurrent edit cannot leave the audit row with a stale Before.
-- name: AdminCampaignWindowForUpdate :one
SELECT starts_at, ends_at FROM sale_campaigns WHERE slug = @slug::text FOR UPDATE;

-- name: SetCampaignWindow :execrows
UPDATE sale_campaigns
SET starts_at = @starts_at::timestamptz, ends_at = @ends_at::timestamptz
WHERE slug = @slug::text;

-- The units of the products on the campaign's list that each shop day from
-- first_day to last_day sold, a day without any included. The orders are
-- PaidByShopDay's. order_lines records no campaign, so it is the list as it is
-- now. The bounds are cut on the shop's clock by the caller.
-- name: CampaignDailyUnits :many
SELECT d.day::date AS day, coalesce(sum(t.units), 0)::bigint AS units
FROM generate_series(@first_day::date, @last_day::date, interval '1 day') AS d(day)
LEFT JOIN (
    SELECT shop_day(o.placed_at) AS day, sum(ol.quantity) AS units
    FROM order_lines ol
    JOIN orders o ON o.id = ol.order_id
    JOIN sold_orders s ON s.id = o.id
    WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
      AND ol.product_id IN (SELECT cp.product_id
                            FROM sale_campaign_products cp
                            JOIN sale_campaigns sc ON sc.id = cp.campaign_id
                            WHERE sc.slug = @slug::text)
    GROUP BY shop_day(o.placed_at)
) t ON t.day = d.day::date
GROUP BY d.day
ORDER BY d.day;

-- Archived products are left out: a campaign on one shows nothing.
-- name: AdminCampaignProductSearch :many
SELECT p.slug, localized_name(p.name, p.name_en, @locale::text) AS name,
       coalesce(img.storage_key, '')::text AS image_key,
       coalesce(mv.price_cents, 0)::bigint AS price_cents,
       coalesce(mv.compare_at_price_cents, 0)::bigint AS compare_at_price_cents,
       (mv.price_cents IS NOT NULL)::boolean AS has_variant,
       EXISTS (SELECT 1 FROM product_variants v
               WHERE v.product_id = p.id AND v.is_active AND v.compare_at_price_cents IS NOT NULL) AS has_discounted_variant,
       (p.status = 'draft')::boolean AS is_draft,
       coalesce((SELECT sum(greatest(v.stock_quantity - v.safety_stock, 0))
                 FROM product_variants v WHERE v.product_id = p.id AND v.is_active), 0)::bigint AS sellable_units
FROM products p
LEFT JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (compare_at_price_cents IS NOT NULL AND compare_at_price_cents > price_cents
              AND stock_quantity > safety_stock) DESC,
             (stock_quantity > safety_stock) DESC,
             (compare_at_price_cents IS NOT NULL AND compare_at_price_cents > price_cents) DESC,
             price_cents, id
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT storage_key FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE p.status <> 'archived'
  AND (p.name ILIKE '%' || @escaped_term::text || '%'
       OR p.name_en ILIKE '%' || @escaped_term::text || '%'
       OR p.slug ILIKE '%' || @escaped_term::text || '%')
  AND NOT EXISTS (SELECT 1
                  FROM sale_campaign_products cp
                  JOIN sale_campaigns c ON c.id = cp.campaign_id
                  WHERE c.slug = @campaign::text AND cp.product_id = p.id)
ORDER BY p.name, p.id
LIMIT @row_limit::integer;

-- name: SetCampaignTone :execrows
UPDATE sale_campaigns SET tone = @tone::text WHERE slug = @slug::text;

-- name: SetCampaignActive :execrows
UPDATE sale_campaigns SET is_active = @is_active::boolean WHERE slug = @slug::text;

-- ONE statement: sale_campaign_needs_discount refuses a product with nothing
-- marked down and takes a lock on it first, so a check here would be a check a
-- concurrent price change invalidates.
-- name: LockCampaignAppendPosition :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'append:campaign:' || c.id::text, 628471039582915603::bigint))
FROM sale_campaigns c
WHERE c.slug = @campaign::text;

-- name: AddCampaignProduct :execrows
INSERT INTO sale_campaign_products (campaign_id, product_id, position)
SELECT c.id, p.id,
       coalesce((SELECT max(position) + 1 FROM sale_campaign_products x
                 WHERE x.campaign_id = c.id), 0)
FROM sale_campaigns c, products p
WHERE c.slug = @campaign::text AND p.slug = @product::text
ON CONFLICT (campaign_id, product_id) DO NOTHING;

-- name: RemoveCampaignProduct :exec
DELETE FROM sale_campaign_products cp
USING sale_campaigns c, products p
WHERE cp.campaign_id = c.id AND cp.product_id = p.id
  AND c.slug = @campaign::text AND p.slug = @product::text;

-- name: AdminCampaignProducts :many
SELECT p.slug, p.name AS name,
       coalesce(img.storage_key, '')::text AS image_key,
       coalesce(mv.price_cents, 0)::bigint AS price_cents,
       coalesce(mv.compare_at_price_cents, 0)::bigint AS compare_at_price_cents,
       (mv.price_cents IS NOT NULL)::boolean AS has_variant,
       EXISTS (SELECT 1 FROM product_variants v
               WHERE v.product_id = p.id AND v.is_active AND v.compare_at_price_cents IS NOT NULL) AS has_discounted_variant,
       (p.status = 'draft')::boolean AS is_draft,
       coalesce((SELECT sum(greatest(v.stock_quantity - v.safety_stock, 0))
                 FROM product_variants v WHERE v.product_id = p.id AND v.is_active), 0)::bigint AS sellable_units, cp.position
FROM sale_campaign_products cp
JOIN products p ON p.id = cp.product_id
JOIN sale_campaigns c ON c.id = cp.campaign_id
LEFT JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (compare_at_price_cents IS NOT NULL AND compare_at_price_cents > price_cents
              AND stock_quantity > safety_stock) DESC,
             (stock_quantity > safety_stock) DESC,
             (compare_at_price_cents IS NOT NULL AND compare_at_price_cents > price_cents) DESC,
             price_cents, id
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT storage_key FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE c.slug = @campaign::text
ORDER BY cp.position, p.id;
