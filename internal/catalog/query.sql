-- categories_acyclic is what guarantees the upward walk terminates.
-- The tone and the photograph are the nearest ones up the trail: a
-- sub-category shows its department's. The photograph's key, alt text and width
-- come from ONE row, so a description never belongs to another category's
-- picture.
-- name: CategoryBySlug :one
WITH RECURSIVE trail AS (
    SELECT c.id, c.parent_id, c.slug,
           localized_name(c.name, c.name_en, @locale::text) AS name,
           c.tone, c.image_key,
           localized_name(c.image_alt, c.image_alt_en, @locale::text) AS image_alt,
           0 AS depth
    FROM categories c
    WHERE c.slug = $1
    UNION ALL
    SELECT c.id, c.parent_id, c.slug,
           localized_name(c.name, c.name_en, @locale::text),
           c.tone, c.image_key,
           localized_name(c.image_alt, c.image_alt_en, @locale::text),
           t.depth + 1
    FROM categories c
    JOIN trail t ON c.id = t.parent_id
)
SELECT
    self.id,
    self.name,
    -- Root-first, the order the crumbs render in. Empty for a root category.
    coalesce(
        (SELECT array_agg(a.slug ORDER BY a.depth DESC) FROM trail a WHERE a.depth > 0),
        ARRAY[]::text[]
    )::text[] AS ancestor_slugs,
    coalesce(
        (SELECT array_agg(a.name ORDER BY a.depth DESC) FROM trail a WHERE a.depth > 0),
        ARRAY[]::text[]
    )::text[] AS ancestor_names,
    coalesce(
        (SELECT a.tone FROM trail a WHERE a.tone IS NOT NULL ORDER BY a.depth LIMIT 1),
        'stone'
    )::text AS tone,
    coalesce(photo.image_key, '')::text AS image_key,
    coalesce(photo.image_alt, '')::text AS image_alt,
    coalesce(m.width, 0)::integer AS image_width
FROM trail self
LEFT JOIN LATERAL (
    SELECT a.image_key, a.image_alt FROM trail a
    WHERE a.image_key IS NOT NULL ORDER BY a.depth LIMIT 1
) photo ON true
LEFT JOIN media_objects m ON m.digest = photo.image_key
WHERE self.depth = 0;

-- The direct children of the category with slug $1, in shelf order: the chips
-- under a department's title.
-- name: CategoryChildren :many
SELECT c.slug, localized_name(c.name, c.name_en, @locale::text) AS name
FROM categories c
JOIN categories p ON p.id = c.parent_id
WHERE p.slug = $1
ORDER BY c.position, c.name, c.id;

-- Every category in the subtree rooted at $1, including $1 itself.
-- name: CategoryDescendants :many
WITH RECURSIVE d AS (
    SELECT c.id FROM categories c WHERE c.id = $1
    UNION ALL
    SELECT c.id FROM categories c JOIN d ON c.parent_id = d.id
)
SELECT d.id FROM d;

-- Every brand with an active product in the category, counted over the products
-- the other filters leave: a brand's count is what choosing it would show, brand
-- filters aside. A brand the other filters empty stays listed at zero, so a
-- chosen one can always be unchosen.
-- name: CategoryBrands :many
SELECT b.id, b.slug, b.name,
       (count(*) FILTER (
           WHERE NOT @filter_variants::boolean
              OR EXISTS (
                  SELECT 1 FROM product_variants v
                  WHERE v.product_id = p.id AND v.is_active
            AND NOT EXISTS (
              SELECT 1 FROM unnest(@option_names::text[]) AS chosen(name)
              WHERE NOT EXISTS (
                  SELECT 1 FROM variant_option_values carried
                  JOIN product_options axis ON axis.id = carried.option_id
                  JOIN product_option_values axis_value ON axis_value.id = carried.option_value_id
                  WHERE carried.variant_id = v.id
                    AND axis.name = chosen.name AND EXISTS (
                      SELECT 1 FROM unnest(@option_names::text[]) WITH ORDINALITY chosen_value(name, position)
                      WHERE chosen_value.name = chosen.name AND (@option_values::text[])[chosen_value.position] = axis_value.value
                    )
              )
          )
                    AND (NOT @in_stock_only::boolean OR v.stock_quantity > v.safety_stock)
                    AND (@min_price::bigint = 0 OR v.price_cents >= @min_price::bigint)
                    AND (@max_price::bigint = 0 OR v.price_cents <= @max_price::bigint)
              )
       ))::bigint AS product_count
FROM products p
JOIN brands b ON b.id = p.brand_id
WHERE p.status = 'active'
  AND p.category_id = ANY(@category_ids::uuid[])
GROUP BY b.id, b.slug, b.name
ORDER BY b.name;

-- status = 'active' is a literal, or the planner cannot use
-- products_category_published_idx. Every variant condition sits in ONE EXISTS, or
-- each finds a different variant. Sellable is stock_quantity > safety_stock.
-- name: CategoryListing :many
SELECT
    p.slug,
    p.category_id,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    coalesce(b.name, '') AS brand,
    mv.price_cents AS min_price_cents,
    -- Whether that price is the cheapest of several within the filters, so a card
    -- can say "from" rather than state one variant's price as the product's.
    EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
          AND NOT EXISTS (
              SELECT 1 FROM unnest(@option_names::text[]) AS chosen(name)
              WHERE NOT EXISTS (
                  SELECT 1 FROM variant_option_values carried
                  JOIN product_options axis ON axis.id = carried.option_id
                  JOIN product_option_values axis_value ON axis_value.id = carried.option_value_id
                  WHERE carried.variant_id = dv.id
                    AND axis.name = chosen.name AND EXISTS (
                      SELECT 1 FROM unnest(@option_names::text[]) WITH ORDINALITY chosen_value(name, position)
                      WHERE chosen_value.name = chosen.name AND (@option_values::text[])[chosen_value.position] = axis_value.value
                    )
              )
          )
          AND (NOT @in_stock_only::boolean OR dv.stock_quantity > dv.safety_stock)
          AND (@max_price::bigint = 0 OR dv.price_cents <= @max_price::bigint)
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
          AND NOT EXISTS (
              SELECT 1 FROM unnest(@option_names::text[]) AS chosen(name)
              WHERE NOT EXISTS (
                  SELECT 1 FROM variant_option_values carried
                  JOIN product_options axis ON axis.id = carried.option_id
                  JOIN product_option_values axis_value ON axis_value.id = carried.option_value_id
                  WHERE carried.variant_id = sv.id
                    AND axis.name = chosen.name AND EXISTS (
                      SELECT 1 FROM unnest(@option_names::text[]) WITH ORDINALITY chosen_value(name, position)
                      WHERE chosen_value.name = chosen.name AND (@option_values::text[])[chosen_value.position] = axis_value.value
                    )
              )
          )
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
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants candidate
    WHERE product_id = p.id AND is_active
      AND NOT EXISTS (
              SELECT 1 FROM unnest(@option_names::text[]) AS chosen(name)
              WHERE NOT EXISTS (
                  SELECT 1 FROM variant_option_values carried
                  JOIN product_options axis ON axis.id = carried.option_id
                  JOIN product_option_values axis_value ON axis_value.id = carried.option_value_id
                  WHERE carried.variant_id = candidate.id
                    AND axis.name = chosen.name AND EXISTS (
                      SELECT 1 FROM unnest(@option_names::text[]) WITH ORDINALITY chosen_value(name, position)
                      WHERE chosen_value.name = chosen.name AND (@option_values::text[])[chosen_value.position] = axis_value.value
                    )
              )
          )
      -- The card shows a variant the filters accepted, or it states a price the
      -- shopper excluded; these are the predicates of the EXISTS below.
      AND (NOT @in_stock_only::boolean OR stock_quantity > safety_stock)
      AND (@min_price::bigint = 0 OR price_cents >= @min_price::bigint)
      AND (@max_price::bigint = 0 OR price_cents <= @max_price::bigint)
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
  AND p.category_id = ANY(@category_ids::uuid[])
  AND (@brand_ids::uuid[] = ARRAY[]::uuid[] OR p.brand_id = ANY(@brand_ids::uuid[]))
  -- One variant satisfies every variant-level filter at once.
  AND (
      NOT @filter_variants::boolean
      OR EXISTS (
          SELECT 1 FROM product_variants v
          WHERE v.product_id = p.id AND v.is_active
            AND NOT EXISTS (
              SELECT 1 FROM unnest(@option_names::text[]) AS chosen(name)
              WHERE NOT EXISTS (
                  SELECT 1 FROM variant_option_values carried
                  JOIN product_options axis ON axis.id = carried.option_id
                  JOIN product_option_values axis_value ON axis_value.id = carried.option_value_id
                  WHERE carried.variant_id = v.id
                    AND axis.name = chosen.name AND EXISTS (
                      SELECT 1 FROM unnest(@option_names::text[]) WITH ORDINALITY chosen_value(name, position)
                      WHERE chosen_value.name = chosen.name AND (@option_values::text[])[chosen_value.position] = axis_value.value
                    )
              )
          )
            AND (NOT @in_stock_only::boolean OR v.stock_quantity > v.safety_stock)
            AND (@min_price::bigint = 0 OR v.price_cents >= @min_price::bigint)
            AND (@max_price::bigint = 0 OR v.price_cents <= @max_price::bigint)
      )
  )
ORDER BY
    CASE WHEN @sort::text = 'price_asc'  THEN mv.price_cents END ASC,
    CASE WHEN @sort::text = 'price_desc' THEN mv.price_cents END DESC,
    CASE WHEN @sort::text = 'rating'     THEN coalesce(rv.rating, 0) END DESC,
    p.published_at DESC, p.id DESC
LIMIT @page_size::integer OFFSET @page_offset::integer;

-- The same predicate as CategoryListing, and nothing else: this is only a number.
-- name: CategoryListingCount :one
SELECT count(*)::bigint
FROM products p
WHERE p.status = 'active'
  AND p.category_id = ANY(@category_ids::uuid[])
  AND (@brand_ids::uuid[] = ARRAY[]::uuid[] OR p.brand_id = ANY(@brand_ids::uuid[]))
  AND (
      NOT @filter_variants::boolean
      OR EXISTS (
          SELECT 1 FROM product_variants v
          WHERE v.product_id = p.id AND v.is_active
            AND NOT EXISTS (
              SELECT 1 FROM unnest(@option_names::text[]) AS chosen(name)
              WHERE NOT EXISTS (
                  SELECT 1 FROM variant_option_values carried
                  JOIN product_options axis ON axis.id = carried.option_id
                  JOIN product_option_values axis_value ON axis_value.id = carried.option_value_id
                  WHERE carried.variant_id = v.id
                    AND axis.name = chosen.name AND EXISTS (
                      SELECT 1 FROM unnest(@option_names::text[]) WITH ORDINALITY chosen_value(name, position)
                      WHERE chosen_value.name = chosen.name AND (@option_values::text[])[chosen_value.position] = axis_value.value
                    )
              )
          )
            AND (NOT @in_stock_only::boolean OR v.stock_quantity > v.safety_stock)
            AND (@min_price::bigint = 0 OR v.price_cents >= @min_price::bigint)
            AND (@max_price::bigint = 0 OR v.price_cents <= @max_price::bigint)
      )
  );

-- Search is a sequential scan of the active products: a term may match a column
-- of products, brands, variants, specs or categories, and no index serves an OR
-- across tables, so the term bound is what limits the work.
-- A category matches by its own name or an ancestor's, so searching a
-- department finds what is filed under its sub-categories.
-- @patterns holds one pattern per term; the caller escapes %, _ and \ in each
-- before binding, and @exact_pattern is the whole query.
-- name: SearchProducts :many
WITH RECURSIVE category_match AS (
    SELECT t.pattern, c.id
    FROM unnest(@patterns::text[]) AS t(pattern)
    JOIN categories c ON c.name ILIKE t.pattern OR coalesce(c.name_en, '') ILIKE t.pattern
    UNION
    SELECT m.pattern, c.id FROM categories c JOIN category_match m ON c.parent_id = m.id
)
SELECT
    p.slug,
    p.category_id,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
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
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM products p
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
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE p.status = 'active'
  -- Both names: matching only the localized column would make the catalogue
  -- searchable in one language at a time. Every term must match some field, and
  -- a term may match a different field from its neighbour: "aurora 65w" is a
  -- brand and a spec.
  AND NOT EXISTS (
      SELECT 1 FROM unnest(@patterns::text[]) AS t(pattern)
      WHERE NOT (
          p.name ILIKE t.pattern
          OR coalesce(p.name_en, '') ILIKE t.pattern
          OR coalesce(p.summary, '') ILIKE t.pattern
          OR coalesce(p.summary_en, '') ILIKE t.pattern
          OR coalesce(b.name, '') ILIKE t.pattern
          OR EXISTS (
              SELECT 1 FROM category_match m
              WHERE m.pattern = t.pattern AND m.id = p.category_id
          )
          OR EXISTS (
              SELECT 1 FROM product_variants sku_match
              WHERE sku_match.product_id = p.id AND sku_match.is_active
                AND sku_match.sku ILIKE t.pattern
          )
          OR EXISTS (
              SELECT 1 FROM product_specs ps
              WHERE ps.product_id = p.id
                AND (ps.label ILIKE t.pattern
                     OR coalesce(ps.label_en, '') ILIKE t.pattern
                     OR ps.value ILIKE t.pattern
                     OR coalesce(ps.value_en, '') ILIKE t.pattern)
          )
      )
  )
ORDER BY
    -- A chosen sort leads and relevance breaks its ties; the default is
    -- relevance alone, which is the zero value of @sort.
    CASE WHEN @sort::text = 'price_asc'  THEN mv.price_cents END ASC,
    CASE WHEN @sort::text = 'price_desc' THEN mv.price_cents END DESC,
    CASE WHEN @sort::text = 'rating'     THEN coalesce(rv.rating, 0) END DESC,
    -- Field relevance is explicit; repeated words, sales and ratings do not change it.
    -- The exact tiers compare the whole query; a name that holds the whole query
    -- leads one that holds every term in another order, which leads one that
    -- holds only some, then category, SKU, brand and summary follow on any term.
    CASE
        WHEN EXISTS (
            SELECT 1 FROM product_variants exact_sku
            WHERE exact_sku.product_id = p.id AND exact_sku.is_active
              AND exact_sku.sku ILIKE @exact_pattern::text
        ) THEN 9
        WHEN p.name ILIKE @exact_pattern::text OR coalesce(p.name_en, '') ILIKE @exact_pattern::text THEN 8
        WHEN p.name ILIKE '%' || @exact_pattern::text || '%'
             OR coalesce(p.name_en, '') ILIKE '%' || @exact_pattern::text || '%' THEN 7
        WHEN NOT EXISTS (
            SELECT 1 FROM unnest(@patterns::text[]) AS t(pattern)
            WHERE NOT (p.name ILIKE t.pattern OR coalesce(p.name_en, '') ILIKE t.pattern)
        ) THEN 6
        WHEN EXISTS (
            SELECT 1 FROM unnest(@patterns::text[]) AS t(pattern)
            WHERE p.name ILIKE t.pattern OR coalesce(p.name_en, '') ILIKE t.pattern
        ) THEN 5
        WHEN EXISTS (SELECT 1 FROM category_match m WHERE m.id = p.category_id) THEN 4
        WHEN EXISTS (
            SELECT 1 FROM product_variants partial_sku, unnest(@patterns::text[]) AS t(pattern)
            WHERE partial_sku.product_id = p.id AND partial_sku.is_active
              AND partial_sku.sku ILIKE t.pattern
        ) THEN 3
        WHEN EXISTS (
            SELECT 1 FROM unnest(@patterns::text[]) AS t(pattern) WHERE coalesce(b.name, '') ILIKE t.pattern
        ) THEN 2
        WHEN EXISTS (
            SELECT 1 FROM unnest(@patterns::text[]) AS t(pattern)
            WHERE coalesce(p.summary, '') ILIKE t.pattern OR coalesce(p.summary_en, '') ILIKE t.pattern
        ) THEN 1
        ELSE 0
    END DESC,
    p.published_at DESC, p.id DESC
LIMIT @page_size::integer OFFSET @page_offset::integer;

-- The newest active products with a buyable price, for a page with nothing else to show.
-- name: NewestProducts :many
SELECT
    p.slug,
    p.category_id,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
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
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM products p
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
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE p.status = 'active'
ORDER BY p.published_at DESC, p.id DESC
LIMIT @page_size::integer;

-- The same predicate as SearchProducts, and it has to stay the same.
-- name: SearchProductsCount :one
WITH RECURSIVE category_match AS (
    SELECT t.pattern, c.id
    FROM unnest(@patterns::text[]) AS t(pattern)
    JOIN categories c ON c.name ILIKE t.pattern OR coalesce(c.name_en, '') ILIKE t.pattern
    UNION
    SELECT m.pattern, c.id FROM categories c JOIN category_match m ON c.parent_id = m.id
)
SELECT count(*)::bigint
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
WHERE p.status = 'active'
  -- Every term must match some field, and a term may match a different field
  -- from its neighbour.
  AND NOT EXISTS (
      SELECT 1 FROM unnest(@patterns::text[]) AS t(pattern)
      WHERE NOT (
          p.name ILIKE t.pattern
          OR coalesce(p.name_en, '') ILIKE t.pattern
          OR coalesce(p.summary, '') ILIKE t.pattern
          OR coalesce(p.summary_en, '') ILIKE t.pattern
          OR coalesce(b.name, '') ILIKE t.pattern
          OR EXISTS (
              SELECT 1 FROM category_match m
              WHERE m.pattern = t.pattern AND m.id = p.category_id
          )
          OR EXISTS (
              SELECT 1 FROM product_variants sku_match
              WHERE sku_match.product_id = p.id AND sku_match.is_active
                AND sku_match.sku ILIKE t.pattern
          )
          OR EXISTS (
              SELECT 1 FROM product_specs ps
              WHERE ps.product_id = p.id
                AND (ps.label ILIKE t.pattern
                     OR coalesce(ps.label_en, '') ILIKE t.pattern
                     OR ps.value ILIKE t.pattern
                     OR coalesce(ps.value_en, '') ILIKE t.pattern)
          )
      )
  );

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
    coalesce(img.storage_key, '') AS image_key,
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
    SELECT storage_key, alt_text, alt_text_en, width, height
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


-- Only active products, and only categories with something in them: a sitemap is
-- a claim that these URLs are worth crawling.
-- name: SitemapProducts :many
SELECT slug, updated_at FROM products
WHERE status = 'active'
ORDER BY updated_at DESC
LIMIT $1;

-- A department holds no product itself and lists those of every category below
-- it, so the test is over the subtree, as CategoryDescendants is.
-- name: SitemapCategories :many
WITH RECURSIVE tree AS (
    SELECT id, id AS root FROM categories
    UNION ALL
    SELECT k.id, t.root FROM categories k JOIN tree t ON k.parent_id = t.id
)
SELECT DISTINCT c.slug, c.updated_at
FROM categories c
WHERE EXISTS (
    SELECT 1 FROM tree t
    JOIN products p ON p.category_id = t.id
    WHERE t.root = c.id AND p.status = 'active'
)
ORDER BY c.updated_at DESC
LIMIT $1;

-- Any active campaign by its slug, inside its window or not: the page says
-- honestly whether it has not started or has ended.
-- name: CampaignBySlug :one
SELECT c.id, c.slug, localized_name(c.title, c.title_en, @locale::text) AS title,
       c.starts_at, c.ends_at,
       c.tone,
       coalesce(c.image_key, '')::text AS image_key,
       coalesce(localized_name(c.image_alt, c.image_alt_en, @locale::text), '')::text AS image_alt,
       coalesce(m.width, 0)::integer AS image_width
FROM sale_campaigns c
LEFT JOIN media_objects m ON m.digest = c.image_key
WHERE c.slug = @slug::text AND c.is_active;

-- A campaign left out keeps its page at /s/{slug} (CampaignBySlug), reachable by
-- a direct link.
-- name: ListedCampaigns :many
SELECT c.id, c.slug, localized_name(c.title, c.title_en, @locale::text) AS title,
       c.starts_at, c.ends_at, c.tone,
       coalesce(c.image_key, '')::text AS image_key,
       coalesce(localized_name(c.image_alt, c.image_alt_en, @locale::text), '')::text AS image_alt,
       coalesce(m.width, 0)::integer AS image_width,
       (SELECT count(*) FROM sale_campaign_products cp
        JOIN products p ON p.id = cp.product_id
        WHERE cp.campaign_id = c.id AND p.status = 'active')::bigint AS products
FROM sale_campaigns c
JOIN listed_campaigns l ON l.id = c.id
LEFT JOIN media_objects m ON m.digest = c.image_key
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
    coalesce(img.storage_key, '') AS image_key,
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
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE cp.campaign_id = $1 AND p.status = 'active'
-- Sellable products first, then the position the back office set.
ORDER BY in_stock DESC, cp.position, p.id;

-- WITH ORDINALITY, so the columns appear in the order the URL named them.
-- name: CompareProducts :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    coalesce(b.name, '') AS brand,
    localized_name(c.name, c.name_en, @locale::text) AS category,
    c.slug AS category_slug,
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
    coalesce(p.warranty_months, 0)::integer AS warranty_months,
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height,
    asked.ord::integer AS position
FROM unnest(@slugs::text[]) WITH ORDINALITY AS asked(slug, ord)
JOIN products p ON p.slug = asked.slug AND p.status = 'active'
LEFT JOIN brands b ON b.id = p.brand_id
JOIN categories c ON c.id = p.category_id
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
ORDER BY asked.ord;

-- Ordered so the rows a reader can compare come first; ties break on the label's
-- own position.
-- name: CompareSpecs :many
SELECT
    p.slug,
    localized_name(s.label, s.label_en, @locale::text) AS label,
    -- The untranslated label identifies a row, and the Go that builds the table
    -- must group on the same thing or two labels sharing a translation merge.
    s.label AS label_key,
    localized_name(s.value, s.value_en, @locale::text) AS value,
    -- Counted on the untranslated label: grouping by what the reader sees would
    -- split one spec in two and report each as stated by one product.
    (SELECT count(DISTINCT sp.product_id)
     FROM product_specs sp
     JOIN products op ON op.id = sp.product_id
     WHERE sp.label = s.label AND op.slug = ANY(@slugs::text[]))::bigint AS shared_by,
    s.position
FROM product_specs s
JOIN products p ON p.id = s.product_id
WHERE p.slug = ANY(@slugs::text[])
ORDER BY shared_by DESC, s.label, s.position;

-- Every category that offers comparison. The value is the nearest one up the
-- trail that sets it, and a root that sets none is false, so a sub-category
-- takes its department's answer; read top-down, the same rule as the tone's
-- upward walk. categories_acyclic is what guarantees it terminates.
-- name: ComparableCategoryIDs :many
WITH RECURSIVE eff AS (
    SELECT c.id, c.comparable FROM categories c WHERE c.parent_id IS NULL
    UNION ALL
    SELECT c.id, coalesce(c.comparable, e.comparable)
    FROM categories c JOIN eff e ON c.parent_id = e.id
)
SELECT eff.id FROM eff WHERE eff.comparable IS TRUE;

-- Where to start choosing products to compare: the department nearest the top,
-- then first in the shop's order, that offers comparison. Same inheritance as
-- ComparableCategoryIDs.
-- name: FirstComparableCategorySlug :one
WITH RECURSIVE eff AS (
    SELECT c.id, c.slug, c.position, 0 AS depth, c.comparable
    FROM categories c WHERE c.parent_id IS NULL
    UNION ALL
    SELECT c.id, c.slug, c.position, e.depth + 1, coalesce(c.comparable, e.comparable)
    FROM categories c JOIN eff e ON c.parent_id = e.id
)
SELECT eff.slug FROM eff WHERE eff.comparable IS TRUE
ORDER BY eff.depth, eff.position, eff.slug
LIMIT 1;

-- What to compare a product with: the other active products on its own shelf,
-- the ones priced closest first, with a stable order for equal distances.
-- A product with no active variant has no price to show and is left out.
-- name: CompareSuggestions :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(b.name, '') AS brand,
    mv.price_cents AS min_price_cents,
    EXISTS (
        SELECT 1 FROM product_variants dv
        WHERE dv.product_id = p.id AND dv.is_active AND dv.price_cents > mv.price_cents
    ) AS price_varies,
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE p.status = 'active'
  AND p.category_id = (SELECT x.category_id FROM products x WHERE x.slug = @product_slug::text)
  AND NOT (p.slug = ANY(@exclude_slugs::text[]))
ORDER BY abs(mv.price_cents - @anchor_cents::bigint), p.id
LIMIT @row_limit::integer;

-- A facet keeps its own zero-count choices so the checked value can still be removed.
-- name: CategoryOptionValues :many
SELECT axis.name AS option_name,
       min(localized_name(axis.name, axis.name_en, @locale::text))::text AS option_label,
       axis_value.value,
       min(localized_name(axis_value.value, axis_value.value_en, @locale::text))::text AS value_label,
       (count(DISTINCT p.id) FILTER (WHERE
          (@brand_ids::uuid[] = ARRAY[]::uuid[] OR p.brand_id = ANY(@brand_ids::uuid[]))
          AND EXISTS (
             SELECT 1 FROM product_variants candidate
             JOIN variant_option_values carried ON carried.variant_id = candidate.id
             WHERE candidate.product_id = p.id AND candidate.is_active
               AND carried.option_id = axis.id AND carried.option_value_id = axis_value.id
               AND (NOT @in_stock_only::boolean OR candidate.stock_quantity > candidate.safety_stock)
               AND (@min_price::bigint = 0 OR candidate.price_cents >= @min_price::bigint)
               AND (@max_price::bigint = 0 OR candidate.price_cents <= @max_price::bigint)
               AND NOT EXISTS (
                  SELECT 1 FROM unnest(@option_names::text[]) AS chosen(name)
                  WHERE chosen.name <> axis.name AND NOT EXISTS (
                    SELECT 1 FROM variant_option_values other_carried
                    JOIN product_options other_axis ON other_axis.id = other_carried.option_id
                    JOIN product_option_values other_axis_value ON other_axis_value.id = other_carried.option_value_id
                    WHERE other_carried.variant_id = candidate.id
                      AND other_axis.name = chosen.name AND EXISTS (
                      SELECT 1 FROM unnest(@option_names::text[]) WITH ORDINALITY chosen_value(name, position)
                      WHERE chosen_value.name = chosen.name AND (@option_values::text[])[chosen_value.position] = other_axis_value.value
                    )
                  )
               )
          )
       ))::bigint AS product_count
FROM products p
JOIN product_options axis ON axis.product_id = p.id
JOIN product_option_values axis_value ON axis_value.option_id = axis.id
WHERE p.status = 'active' AND p.category_id = ANY(@category_ids::uuid[])
GROUP BY axis.name, axis_value.value
ORDER BY min(axis.position), axis.name, min(axis_value.position), axis_value.value;

-- Of the campaigns with a deal on a product of the department, the one that ends
-- first.
-- name: DepartmentCampaign :one
WITH RECURSIVE d AS (
    SELECT c.id FROM categories c WHERE c.slug = @slug::text
    UNION ALL
    SELECT c.id FROM categories c JOIN d ON c.parent_id = d.id
)
SELECT c.slug, localized_name(c.title, c.title_en, @locale::text) AS title,
       c.starts_at, c.ends_at
FROM sale_campaigns c
WHERE EXISTS (
    SELECT 1 FROM campaign_deals cd
    JOIN products p ON p.id = cd.product_id
    WHERE cd.campaign_id = c.id AND p.category_id IN (SELECT id FROM d))
ORDER BY c.ends_at, c.id
LIMIT 1;

-- Per category of the department that holds at least two products that can be bought, its three
-- newest. A category is a candidate for the comparison; whether it may be compared
-- is the caller's to say.
-- name: DepartmentCompareCandidates :many
WITH RECURSIVE d AS (
    SELECT c.id FROM categories c WHERE c.slug = @slug::text
    UNION ALL
    SELECT c.id FROM categories c JOIN d ON c.parent_id = d.id
),
held AS (
    SELECT p.category_id, p.slug,
           row_number() OVER (PARTITION BY p.category_id ORDER BY p.published_at DESC, p.id DESC) AS nth,
           count(*) OVER (PARTITION BY p.category_id) AS held
    FROM products p
    WHERE p.status = 'active' AND p.category_id IN (SELECT id FROM d)
      AND EXISTS (
          SELECT 1 FROM product_variants v
          WHERE v.product_id = p.id AND v.is_active AND v.stock_quantity > v.safety_stock)
)
SELECT category_id, slug FROM held
WHERE held >= 2 AND nth <= 3
ORDER BY held DESC, category_id, nth;

-- The newest product of the department with three or more colours that each have a
-- photograph, and those colours with the first photograph of each, in the order the
-- product lists them. A swatch marks a colour: the schema leaves it NULL on a value
-- that is not one.
-- name: DepartmentColourStory :many
WITH RECURSIVE d AS (
    SELECT c.id FROM categories c WHERE c.slug = @slug::text
    UNION ALL
    SELECT c.id FROM categories c JOIN d ON c.parent_id = d.id
),
story AS (
    SELECT p.id
    FROM products p
    JOIN product_option_values v ON v.product_id = p.id AND v.swatch_hex IS NOT NULL
    JOIN product_images i ON i.product_id = p.id AND i.option_value_id = v.id
    WHERE p.status = 'active' AND p.category_id IN (SELECT id FROM d)
      AND EXISTS (
          SELECT 1 FROM product_variants sv
          WHERE sv.product_id = p.id AND sv.is_active AND sv.stock_quantity > sv.safety_stock)
    GROUP BY p.id
    HAVING count(DISTINCT v.id) >= 3
    ORDER BY p.published_at DESC, p.id DESC
    LIMIT 1
)
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    coalesce(b.name, '') AS brand,
    mv.price_cents AS price_cents,
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
    localized_name(v.value, v.value_en, @locale::text) AS colour,
    v.swatch_hex::text AS swatch,
    img.storage_key AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width
FROM story s
JOIN products p ON p.id = s.id
LEFT JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
JOIN product_option_values v ON v.product_id = p.id AND v.swatch_hex IS NOT NULL
JOIN LATERAL (
    SELECT storage_key, alt_text, alt_text_en, width
    FROM product_images WHERE product_id = p.id AND option_value_id = v.id ORDER BY position LIMIT 1
) img ON true
ORDER BY v.position, v.id;

-- The first two specifications each product lists, for the line under its name on a
-- department whose products are compared.
-- name: ListingHighlights :many
SELECT slug, value FROM (
    SELECT p.slug, localized_name(s.value, s.value_en, @locale::text) AS value,
           row_number() OVER (PARTITION BY s.product_id ORDER BY s.position, s.id) AS nth
    FROM product_specs s
    JOIN products p ON p.id = s.product_id
    WHERE p.slug = ANY(@slugs::text[])
) ranked
WHERE nth <= 2
ORDER BY slug, nth;
