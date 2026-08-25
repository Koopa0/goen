-- name: ProductBySlug :one
SELECT
    p.id,
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    localized_name(p.description, p.description_en, @locale::text) AS description,
    p.warranty_note,
    coalesce(p.warranty_months, 0)::integer AS warranty_months,
    b.name AS brand,
    b.slug AS brand_slug,
    p.category_id,
    c.slug AS category_slug,
    localized_name(c.name, c.name_en, @locale::text) AS category_name,
    c.parent_id AS category_parent_id
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN categories c ON c.id = p.category_id
WHERE p.slug = $1 AND p.status = 'active';

-- Root-first.
-- name: CategoryAncestors :many
WITH RECURSIVE trail AS (
    SELECT c.id, c.parent_id, c.slug,
           localized_name(c.name, c.name_en, @locale::text) AS name, 0 AS depth
    FROM categories c WHERE c.id = @category_id
    UNION ALL
    SELECT c.id, c.parent_id, c.slug,
           localized_name(c.name, c.name_en, @locale::text), t.depth + 1
    FROM categories c JOIN trail t ON c.id = t.parent_id
)
SELECT trail.slug, trail.name FROM trail ORDER BY trail.depth DESC;

-- name: ProductImages :many
SELECT storage_key,
       localized_name(alt_text, alt_text_en, @locale::text) AS alt_text,
       coalesce(width, 0)::integer AS width,
       coalesce(height, 0)::integer AS height
FROM product_images
WHERE product_id = @product_id
ORDER BY position, id;

-- name: ProductSpecs :many
SELECT localized_name(label, label_en, @locale::text) AS label,
       localized_name(value, value_en, @locale::text) AS value
FROM product_specs
WHERE product_id = $1
ORDER BY position, id;

-- sellable is stock_quantity > safety_stock, the floor record_inventory_movement
-- enforces.
-- name: ProductVariants :many
SELECT
    pv.id,
    pv.sku,
    pv.price_cents,
    pv.compare_at_price_cents,
    (pv.stock_quantity > pv.safety_stock) AS sellable,
    (pv.stock_quantity - pv.safety_stock)::integer AS sellable_quantity,
    pv.preorder_release_on,
    coalesce(
        (SELECT array_agg(o.name ORDER BY o.position, o.id)
         FROM variant_option_values vov
         JOIN product_options o ON o.id = vov.option_id
         WHERE vov.variant_id = pv.id),
        ARRAY[]::text[]
    )::text[] AS option_names,
    coalesce(
        (SELECT array_agg(v.value ORDER BY o.position, o.id)
         FROM variant_option_values vov
         JOIN product_options o ON o.id = vov.option_id
         JOIN product_option_values v ON v.id = vov.option_value_id
         WHERE vov.variant_id = pv.id),
        ARRAY[]::text[]
    )::text[] AS option_values
FROM product_variants pv
WHERE pv.product_id = $1 AND pv.is_active
ORDER BY pv.position, pv.id;

-- option_name and value are identity, what the URL selects on; the _label columns
-- are what the visitor reads.
-- name: ProductOptions :many
SELECT o.name AS option_name,
       localized_name(o.name, o.name_en, @locale::text) AS option_label,
       v.value,
       localized_name(v.value, v.value_en, @locale::text) AS value_label
FROM product_options o
JOIN product_option_values v ON v.option_id = o.id
WHERE o.product_id = $1
  AND EXISTS (
      SELECT 1 FROM variant_option_values vov
      JOIN product_variants pv ON pv.id = vov.variant_id
      WHERE vov.option_value_id = v.id AND pv.is_active
  )
ORDER BY o.position, o.id, v.position, v.id;

-- name: ProductReviews :many
SELECT r.rating, r.title, r.body, r.is_verified_purchase, r.created_at,
       coalesce(u.full_name, '') AS author
FROM visible_reviews r
LEFT JOIN users u ON u.id = r.user_id
WHERE r.product_id = $1
ORDER BY r.is_verified_purchase DESC, r.created_at DESC, r.id DESC
LIMIT $2;

-- name: ProductRating :one
SELECT
    coalesce(avg(rating), 0)::float8 AS rating,
    count(*)::bigint AS rating_count,
    count(*) FILTER (WHERE rating = 5)::bigint AS five,
    count(*) FILTER (WHERE rating = 4)::bigint AS four,
    count(*) FILTER (WHERE rating = 3)::bigint AS three,
    count(*) FILTER (WHERE rating = 2)::bigint AS two,
    count(*) FILTER (WHERE rating = 1)::bigint AS one
FROM visible_reviews WHERE product_id = $1;

-- name: RelatedProducts :many
SELECT
    p.slug, localized_name(p.name, p.name_en, @locale::text) AS name, b.name AS brand,
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
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents, compare_at_price_cents FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n
    FROM visible_reviews WHERE product_id = p.id
) rv ON true
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text, alt_text_en, width, height FROM product_images
    WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE p.status = 'active'
  AND p.category_id = @category_id
  AND p.id <> @exclude_id
ORDER BY p.published_at DESC, p.id DESC
LIMIT @row_limit::integer;

-- order_is_committed, never "EXISTS a succeeded payment": a store-credit-funded
-- order is committed with no payment row at all.
-- name: HasBoughtProduct :one
SELECT EXISTS (
    SELECT 1
    FROM orders o
    JOIN order_lines ol ON ol.order_id = o.id
    JOIN product_variants pv ON pv.id = ol.variant_id
    JOIN products p ON p.id = pv.product_id
    WHERE o.user_id = @user_id AND p.slug = @slug::text
      AND order_is_committed(o.id)
);

-- The base table, not visible_reviews: the unique index is on the base table, so
-- a hidden review must still block a second one.
-- name: HasReviewed :one
SELECT EXISTS (
    SELECT 1 FROM product_reviews r
    JOIN products p ON p.id = r.product_id
    WHERE r.user_id = @user_id AND p.slug = @slug::text
);

-- product_reviews_verified_is_real refuses a false is_verified_purchase.
-- name: CreateReview :exec
INSERT INTO product_reviews (product_id, user_id, rating, title, body, is_verified_purchase)
SELECT p.id, @user_id, @rating::smallint, nullif(@title::text, ''), @body::text, @verified::boolean
FROM products p WHERE p.slug = @slug::text AND p.status = 'active';

-- Idempotent through the partial unique index stock_notifications_pending_key, so
-- somebody notified about one restock may ask again for the next.
-- name: RequestStockNotice :exec
INSERT INTO stock_notifications (variant_id, user_id, email, locale)
VALUES (@variant_id, @user_id, @email::text, @locale)
ON CONFLICT (variant_id, lower(email)) WHERE notified_at IS NULL DO NOTHING;

-- name: BoughtTogether :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
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
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height,
    cp.orders::bigint AS bought_together
FROM product_copurchases cp
JOIN products p ON p.id = cp.other_product_id
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
WHERE cp.product_id = @product_id
  AND cp.orders >= @min_orders::integer
  -- The projection outlives a product being retired.
  AND p.status = 'active'
ORDER BY cp.orders DESC, p.id
LIMIT @limit_to::integer;

-- name: ProductQuestions :many
SELECT q.id, q.body, q.created_at,
       coalesce(u.full_name, '') AS asker
FROM product_questions q
LEFT JOIN users u ON u.id = q.user_id
WHERE q.product_id = $1 AND q.hidden_at IS NULL
ORDER BY q.created_at DESC
LIMIT $2;

-- name: AnswersForQuestions :many
SELECT a.question_id, a.body, a.is_staff, a.created_at,
       coalesce(u.full_name, '') AS author
FROM product_answers a
LEFT JOIN users u ON u.id = a.user_id
WHERE a.question_id = ANY(@question_ids::uuid[]) AND a.hidden_at IS NULL
ORDER BY a.question_id, a.is_staff DESC, a.created_at;

-- name: AskQuestion :exec
INSERT INTO product_questions (product_id, user_id, body)
SELECT p.id, @user_id, @body::text FROM products p
WHERE p.slug = @slug::text AND p.status = 'active';

-- name: AnswerQuestionAsCustomer :execrows
-- Omit is_staff so the database default is the storefront authority. The
-- customer role is not granted that column, so a caller cannot turn this into
-- an official shop answer by supplying another parameter.
INSERT INTO product_answers (question_id, user_id, body)
SELECT q.id, @user_id, @body::text
FROM product_questions q
WHERE q.id = @question_id AND q.hidden_at IS NULL;

-- name: AnswerQuestionAsStaff :execrows
INSERT INTO product_answers (question_id, user_id, body, is_staff)
SELECT q.id, @user_id, @body::text, true
FROM product_questions q
WHERE q.id = @question_id AND q.hidden_at IS NULL;
