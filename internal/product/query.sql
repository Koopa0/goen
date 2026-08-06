-- The product a detail URL names.
--
-- status = 'active' is a literal for the same reason it is everywhere else, and
-- it is also the access rule: a draft or archived product is not a 404 by
-- accident here, it is one on purpose. A URL that renders a draft is how an
-- unannounced product leaks.
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

-- The trail above a category, for the crumbs. Root-first.
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

-- Every image on the product, in display order.
-- The PDP's gallery. alt_text is read ALOUD by a screen reader in the language
-- <html lang> declares, so it follows the visitor like every other word on the page.
-- name: ProductImages :many
SELECT storage_key,
       localized_name(alt_text, alt_text_en, @locale::text) AS alt_text,
       coalesce(width, 0)::integer AS width,
       coalesce(height, 0)::integer AS height
FROM product_images
WHERE product_id = @product_id
ORDER BY position, id;

-- The spec table.
-- name: ProductSpecs :many
SELECT localized_name(label, label_en, @locale::text) AS label,
       localized_name(value, value_en, @locale::text) AS value
FROM product_specs
WHERE product_id = $1
ORDER BY position, id;

-- Every active variant with its option values flattened into one row.
--
-- The page builds its option pickers from this: each variant is a combination,
-- and a picker entry links to the URL that selects it. That is what makes
-- variant selection work with scripting off — the choice is a link, not a
-- click handler.
--
-- sellable is stock_quantity > safety_stock, the floor
-- record_inventory_movement enforces. A variant at the floor has stock and
-- cannot be bought.
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

-- The option groups and their values, in the order the page renders the
-- pickers. Values a product declares but no active variant uses are excluded:
-- a swatch that selects nothing is worse than no swatch.
-- Four columns and not two, and the distinction is the point: option_name and
-- value are IDENTITY — what the URL selects on and what variant matching compares —
-- while the _label columns are what the visitor reads. Selecting on the label would
-- make a shared link resolve differently for a reader in another language.
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

-- The reviews shown on the page, newest first, and the rating summary.
-- name: ProductReviews :many
SELECT r.rating, r.title, r.body, r.is_verified_purchase, r.created_at,
       coalesce(u.full_name, '') AS author
FROM visible_reviews r
LEFT JOIN users u ON u.id = r.user_id
WHERE r.product_id = $1
-- Verified purchases lead. A page whose first review is from somebody who
-- never bought the thing is a page a reader learns to distrust.
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

-- Products to show alongside: same category, excluding this one.
-- name: RelatedProducts :many
SELECT
    p.slug, localized_name(p.name, p.name_en, @locale::text) AS name, b.name AS brand,
    mv.price_cents AS min_price_cents,
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

-- ProductRatingSummary used to sit here: the same seven aggregates as
-- ProductRating above, keyed on slug instead of id, and never called. Two
-- queries answering one question is how one of them comes to disagree with the
-- other, so it is deleted rather than given a caller.

-- Whether this customer has bought this product on an order that went through.
--
-- order_is_committed, never "EXISTS a succeeded payment": an order fully
-- covered by store credit is committed with no payment row at all, and its
-- buyer has as much right to review as anyone.
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

-- Whether this customer has already reviewed this product.
--
-- The BASE table, not visible_reviews. The unique index is on the base table, so
-- a customer whose review was hidden must still be told they have written one —
-- otherwise the form offers to take a second and the insert meets the index.
-- Every OTHER reader wants the visible set; this one wants the truth.
-- name: HasReviewed :one
SELECT EXISTS (
    SELECT 1 FROM product_reviews r
    JOIN products p ON p.id = r.product_id
    WHERE r.user_id = @user_id AND p.slug = @slug::text
);

-- Leave a review.
--
-- is_verified_purchase is passed in rather than computed here, and
-- product_reviews_verified_is_real refuses it if the claim is false — so a bug
-- in the caller becomes a refusal rather than a badge nobody earned.
-- name: CreateReview :exec
INSERT INTO product_reviews (product_id, user_id, rating, title, body, is_verified_purchase)
SELECT p.id, @user_id, @rating::smallint, nullif(@title::text, ''), @body::text, @verified::boolean
FROM products p WHERE p.slug = @slug::text AND p.status = 'active';

-- Ask to be told when a variant is back.
--
-- Idempotent through stock_notifications_pending_key, the partial unique index
-- on (variant_id, lower(email)) WHERE notified_at IS NULL.
--
-- ON CONFLICT and not a NOT EXISTS guard: the guard reads and then writes, and
-- two concurrent requests both pass the read. The index decides, once, under
-- the write. Its partial predicate is also exactly right — a customer notified
-- about a previous restock may ask again for the next one.
-- name: RequestStockNotice :exec
INSERT INTO stock_notifications (variant_id, user_id, email, locale)
VALUES (@variant_id, @user_id, @email::text, @locale)
ON CONFLICT (variant_id, lower(email)) WHERE notified_at IS NULL DO NOTHING;

-- Whether this visitor is already waiting, so the page says so instead of
-- offering a button that does nothing visible.
-- name: HasStockNotice :one
SELECT EXISTS (
    SELECT 1 FROM stock_notifications
    WHERE variant_id = @variant_id AND lower(email) = lower(@email::text)
      AND notified_at IS NULL
);

-- 買了又買, read from the projection.
--
-- An indexed lookup, not an aggregation. The per-request version cost 136 ms
-- for the most-bought product because order_is_committed() ran once per
-- candidate row — 14,963 PL/pgSQL calls for one page view — and grew with order
-- history forever. This is 0.04 ms and does not.
--
-- The minimum is applied HERE rather than in the projection: what counts as a
-- pattern rather than a coincidence is a presentation decision, and baking it
-- into the stored rows would mean rebuilding to change it.
-- name: BoughtTogether :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    coalesce(localized_name(p.summary, p.summary_en, @locale::text), '')::text AS summary,
    b.name AS brand,
    mv.price_cents AS min_price_cents,
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
  -- Only what can still be bought. A recommendation slot pointing at an
  -- archived product is a 404 somebody chose to click, and the projection
  -- outlives a product being retired.
  AND p.status = 'active'
ORDER BY cp.orders DESC, p.id
LIMIT @limit_to::integer;

-- The questions on a product, with their answers.
--
-- Two queries and not one join: a question with three answers would repeat the
-- question three times, and assembling that back into a tree in Go is work the
-- database already did. Two round trips is the cheaper mistake.
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
-- Staff answers first: the shop's answer is the one somebody deciding came for,
-- and burying it under three customer replies is the same as not having it.
ORDER BY a.question_id, a.is_staff DESC, a.created_at;

-- name: AskQuestion :exec
INSERT INTO product_questions (product_id, user_id, body)
SELECT p.id, @user_id, @body::text FROM products p
WHERE p.slug = @slug::text AND p.status = 'active';

-- Answer a question.
--
-- is_staff is passed in and stored, never derived from the author's role at
-- read time — see the column's comment. The question must still be visible: an
-- answer to something staff hid would be published under nothing.
-- name: AnswerQuestion :execrows
INSERT INTO product_answers (question_id, user_id, body, is_staff)
SELECT q.id, @user_id, @body::text, @is_staff::boolean
FROM product_questions q
WHERE q.id = @question_id AND q.hidden_at IS NULL;
