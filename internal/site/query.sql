-- The FAQ, grouped by category in the order the back office set. The handler
-- groups by category_id, never by the name: a name is a label, and two headings
-- may read the same in one language.
-- name: FAQEntries :many
SELECT c.id AS category_id,
       localized_name(c.name, c.name_en, @locale::text) AS category,
       localized_name(e.question, e.question_en, @locale::text) AS question,
       localized_name(e.answer, e.answer_en, @locale::text) AS answer
FROM faq_entries e
JOIN faq_categories c ON c.id = e.category_id
-- Category first, because position is unique only within a category and the
-- handler groups by adjacency.
ORDER BY c.position, e.position, e.id;

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
-- duplicate. categories_position_key now refuses two roots at one position,
-- and the home page reads these rows through Store.Departments, so the header
-- and the tiles are this one query either way.
-- A root's tone is its own, or 'stone'. The photograph is the department's own.
-- name: RootCategories :many
SELECT c.id, c.slug, localized_name(c.name, c.name_en, @locale::text) AS name, c.icon_key,
       coalesce(c.tone, 'stone')::text AS tone,
       coalesce(c.media_digest, '')::text AS image_digest,
       coalesce(c.bundled_key, '')::text AS image_bundled_key,
       coalesce(localized_name(c.image_alt, c.image_alt_en, @locale::text), '')::text AS image_alt,
       coalesce(m.width, 0)::integer AS image_width
FROM categories c
LEFT JOIN media_objects m ON m.digest = c.media_digest
WHERE c.parent_id IS NULL
ORDER BY c.position, c.name, c.id;

-- How many active products each root holds across its whole subtree: a
-- department with fewer than three has no band to show, and the header prints
-- it beside each department in the phone menu, so it runs on every page with a
-- header. Unlike the header's other reads it counts the catalogue, not the
-- categories, and its plan has not been measured.
-- name: DepartmentStock :many
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
       coalesce(img.media_digest, '') AS image_digest,
       coalesce(img.bundled_key, '') AS image_bundled_key,
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
    SELECT media_digest, bundled_key, width
    FROM product_images
    WHERE product_id = r.id
    ORDER BY position
    LIMIT 1
) img ON true
WHERE r.nth <= 3
ORDER BY r.root, r.published_at DESC, r.id DESC;
