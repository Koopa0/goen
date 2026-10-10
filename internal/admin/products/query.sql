-- name: AdminProducts :many
SELECT json_build_object('At', p.updated_at, 'ID', p.id)::text AS page_cursor, p.id, p.slug, p.name, p.status, p.published_at,
       (p.name_en IS NOT NULL)::boolean AS translated,
       coalesce(b.name, '') AS brand, c.name AS category,
       (SELECT count(*) FROM product_variants pv WHERE pv.product_id = p.id)::integer AS variants,
       (SELECT coalesce(min(pv.price_cents), 0) FROM product_variants pv
        WHERE pv.product_id = p.id AND pv.is_active)::bigint AS from_cents
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
JOIN categories c ON c.id = p.category_id
WHERE (NOT @has_cursor::boolean OR (p.updated_at < @after_at::timestamptz)
       OR (p.updated_at = @after_at::timestamptz AND p.id < @after_id::uuid))
ORDER BY p.updated_at DESC, p.id DESC
LIMIT @row_limit::integer;

-- name: PublishedProductCount :one
SELECT count(*)::bigint FROM products WHERE status = 'active';

-- name: AdminProduct :one
SELECT p.id, p.slug, p.name, coalesce(p.summary, '') AS summary, p.description,
       coalesce(p.warranty_months, 0)::integer AS warranty_months,
       coalesce(p.name_en, '') AS name_en,
       coalesce(p.summary_en, '') AS summary_en,
       coalesce(p.description_en, '') AS description_en,
       coalesce(p.warranty_note, '') AS warranty_note, p.status, p.published_at,
       p.brand_id, p.category_id, p.tax_type, p.invoice_unit,
       coalesce(p.origin, '') AS origin, coalesce(p.origin_en, '') AS origin_en,
       coalesce(p.domestic_party_name, '') AS domestic_party_name,
       coalesce(p.domestic_party_phone, '') AS domestic_party_phone,
       coalesce(p.domestic_party_address, '') AS domestic_party_address,
       coalesce(trim_scale(p.net_quantity)::text, '')::text AS net_quantity,
       coalesce(p.net_unit, '') AS net_unit, p.min_age_months
FROM products p WHERE p.slug = $1;

-- name: AdminProductVariants :many
SELECT id, sku, price_cents, compare_at_price_cents, stock_quantity,
       safety_stock, position, is_active
FROM product_variants WHERE product_id = $1 ORDER BY position, id;

-- name: AdminBrands :many
SELECT id, name FROM brands ORDER BY name;

-- name: AdminCategories :many
WITH RECURSIVE tree AS (
    SELECT id, name, slug, parent_id, 0 AS depth,
           lpad(position::text, 4, '0') || name AS sort
    FROM categories WHERE parent_id IS NULL
    UNION ALL
    SELECT c.id, c.name, c.slug, c.parent_id, t.depth + 1,
           t.sort || '/' || lpad(c.position::text, 4, '0') || c.name
    FROM categories c JOIN tree t ON t.id = c.parent_id
)
SELECT id, name, slug, depth::integer AS depth FROM tree ORDER BY sort;

-- Born a DRAFT: products_active_is_published wants a published_at before a
-- product may go active, and a product with no variants has no price.
-- name: CreateProduct :one
INSERT INTO products (brand_id, category_id, slug, name, summary, description,
                      name_en, summary_en, description_en, warranty_note,
                      warranty_months)
VALUES (@brand_id, @category_id, @slug::text, @name::text,
        nullif(@summary::text, ''), @description::text,
        nullif(@name_en::text, ''), nullif(@summary_en::text, ''),
        nullif(@description_en::text, ''), nullif(@warranty_note::text, ''),
        nullif(@warranty_months::integer, 0))
RETURNING slug;

-- Every nullif('') is what lets a translation be CLEARED; absence is the state
-- the column expresses. warranty_months zero means the shop has stated no term,
-- and registration is then refused rather than given a default. :execrows is
-- part of the write contract: an absent immutable slug must not look like a
-- successful customer-visible edit or acquire an audit row.
-- name: UpdateProduct :execrows
UPDATE products
SET brand_id = @brand_id, category_id = @category_id, name = @name::text,
    summary = nullif(@summary::text, ''), description = @description::text,
    name_en = nullif(@name_en::text, ''),
    summary_en = nullif(@summary_en::text, ''),
    description_en = nullif(@description_en::text, ''),
    warranty_note = nullif(@warranty_note::text, ''),
    warranty_months = nullif(@warranty_months::integer, 0)
WHERE slug = @slug::text;

-- published_at is stamped on the FIRST publish and kept, so re-publishing an old
-- product does not make it new again. :execrows because an UPDATE matching
-- nothing is not an error in SQL: as :exec, a slug that does not exist reports
-- success to the staff member and to the audit trail.
-- name: SetProductStatus :execrows
UPDATE products
SET status = @status::text,
    published_at = CASE
        WHEN @status::text = 'active' THEN coalesce(published_at, now())
        ELSE published_at
    END
WHERE slug = @slug::text;

-- stock_quantity is deliberately absent: it is not in admin's INSERT grant, so
-- it takes DEFAULT 0 and stock arrives only through record_inventory_movement.
-- A zero parcel measurement stores NULL, meaning UNMEASURED.
-- name: CreateVariant :exec
INSERT INTO product_variants (product_id, sku, price_cents, compare_at_price_cents,
                              safety_stock, position, is_active,
                              parcel_longest_mm, parcel_sum_mm, parcel_weight_g)
SELECT p.id, @sku::text, @price_cents::bigint,
       nullif(@compare_at_price_cents::bigint, 0), @safety_stock::integer,
       coalesce((SELECT max(position) + 1 FROM product_variants v WHERE v.product_id = p.id), 0),
       true,
       nullif(@parcel_longest_mm::integer, 0),
       nullif(@parcel_sum_mm::integer, 0),
       nullif(@parcel_weight_g::integer, 0)
FROM products p WHERE p.slug = @slug::text;

-- No foreign key on storage_key: product_images predates media_objects and still
-- holds embedded-asset names from the seed, so the column carries two kinds of
-- key.
-- name: AttachProductImage :exec
INSERT INTO product_images (product_id, storage_key, alt_text, alt_text_en,
                            width, height, position, option_value_id)
SELECT p.id, @storage_key::text, @alt_text::text, nullif(@alt_text_en::text, ''),
       @width::integer, @height::integer,
       coalesce((SELECT max(position) + 1 FROM product_images x WHERE x.product_id = p.id), 0),
       sqlc.narg('option_value_id')::uuid
FROM products p
WHERE p.slug = @slug::text;

-- name: SetProductImageOptionValue :execrows
UPDATE product_images
SET option_value_id = sqlc.narg('option_value_id')::uuid
FROM products p
WHERE product_images.product_id = p.id AND p.slug = @slug::text
  AND product_images.storage_key = @storage_key::text;

-- name: DetachProductImage :execrows
DELETE FROM product_images pi
USING products p
WHERE pi.product_id = p.id AND p.slug = @slug::text AND pi.storage_key = @storage_key::text;

-- name: AdminProductImages :many
SELECT pi.storage_key, pi.alt_text, pi.width, pi.height, pi.option_value_id
FROM product_images pi
JOIN products p ON p.id = pi.product_id
WHERE p.slug = @slug::text
ORDER BY pi.position, pi.id;

-- One product's images in display order. Read after LockProductCatalogue, as its
-- own statement: in the same statement the read would use a snapshot taken
-- before that lock was won.
-- name: ProductImageOrder :many
SELECT pi.id, pi.storage_key
FROM product_images pi
JOIN products p ON p.id = pi.product_id
WHERE p.slug = @slug::text
ORDER BY pi.position, pi.id;

-- (product_id, position) is a unique index checked row by row, so a reorder
-- first moves every image clear of the range it is about to fill.
-- name: ParkProductImages :exec
UPDATE product_images pi SET position = pi.position + 1000000
FROM products p
WHERE pi.product_id = p.id AND p.slug = @slug::text;

-- name: SetProductImageOrder :exec
UPDATE product_images pi SET position = o.n::integer - 1
FROM unnest(@ids::uuid[]) WITH ORDINALITY AS o(id, n)
WHERE pi.id = o.id;

-- name: AdminProductSpecs :many
SELECT s.id, s.label, s.value,
       coalesce(s.label_en, '') AS label_en, coalesce(s.value_en, '') AS value_en,
       s.position
FROM product_specs s
JOIN products p ON p.id = s.product_id
WHERE p.slug = $1
ORDER BY s.position, s.label;

-- name: LockProductSpecAppendPosition :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'append:product_specs:' || p.id::text, 628471039582915603::bigint))
FROM products p
WHERE p.slug = @slug::text;

-- Read the maximum in a separate statement after LockProductSpecAppendPosition:
-- a statement that waits for the lock keeps its pre-wait snapshot.
-- name: AddProductSpec :one
INSERT INTO product_specs (product_id, label, value, label_en, value_en, position)
SELECT p.id, @label::text, @value::text,
       nullif(@label_en::text, ''), nullif(@value_en::text, ''),
       coalesce((SELECT max(sp.position) FROM product_specs sp
                 WHERE sp.product_id = p.id), 0) + 1
FROM products p
WHERE p.slug = @slug::text
RETURNING id;

-- name: RemoveProductSpec :execrows
DELETE FROM product_specs s
USING products p
WHERE p.id = s.product_id AND p.slug = @slug::text AND s.id = @spec_id;

-- name: AdminProductOptions :many
SELECT o.id, o.name, coalesce(o.name_en, '') AS name_en, o.position,
       coalesce(
           (SELECT array_agg(v.id::text ORDER BY v.position, v.id)
            FROM product_option_values v WHERE v.option_id = o.id),
           ARRAY[]::text[]
       )::text[] AS value_ids,
       coalesce(
           (SELECT array_agg(v.value ORDER BY v.position, v.id)
            FROM product_option_values v WHERE v.option_id = o.id),
           ARRAY[]::text[]
       )::text[] AS values,
       coalesce(
           (SELECT array_agg(coalesce(v.value_en, '') ORDER BY v.position, v.id)
            FROM product_option_values v WHERE v.option_id = o.id),
           ARRAY[]::text[]
       )::text[] AS value_labels,
       coalesce(
           (SELECT array_agg(coalesce(v.swatch_hex, '') ORDER BY v.position, v.id)
            FROM product_option_values v WHERE v.option_id = o.id),
           ARRAY[]::text[]
       )::text[] AS swatch_hexes
FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = $1
ORDER BY o.position, o.id;

-- name: AddProductOption :one
INSERT INTO product_options (product_id, name, name_en, position)
SELECT p.id, @name::text, nullif(@name_en::text, ''),
       coalesce((SELECT max(o.position) FROM product_options o
                 WHERE o.product_id = p.id), 0) + 1
FROM products p
WHERE p.slug = @slug::text
RETURNING id;

-- product_id comes from the OPTION and not from the caller, so a value cannot be
-- attached to an option of a different product: the composite foreign key would
-- refuse it, and resolving it here means the caller cannot try.
-- name: AddProductOptionValue :one
INSERT INTO product_option_values (product_id, option_id, value, value_en, swatch_hex, position)
SELECT o.product_id, o.id, @value::text, nullif(@value_en::text, ''),
       nullif(@swatch_hex::text, ''),
       coalesce((SELECT max(v.position) FROM product_option_values v
                 WHERE v.option_id = o.id), 0) + 1
FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = @slug::text AND o.id = @option_id
RETURNING id;

-- Lock before reading options so a concurrently added axis participates in validation.
-- name: LockProductCatalogue :one
SELECT id FROM products WHERE slug = $1 FOR NO KEY UPDATE;

-- name: ProductOptionCount :one
SELECT count(*)::bigint FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = $1;

-- A variant of the product already carries every one of the chosen values. The
-- storefront sells a selection only when exactly one variant matches it, so a
-- second variant on the same combination leaves neither of them buyable.
-- name: VariantCombinationTaken :one
SELECT EXISTS (
    SELECT 1
    FROM variant_option_values vov
    JOIN products p ON p.id = vov.product_id
    WHERE p.slug = @slug::text
      AND vov.option_value_id = ANY(@option_value_ids::uuid[])
    GROUP BY vov.variant_id
    HAVING count(*) = cardinality(@option_value_ids::uuid[])
);

-- Every id is resolved inside the statement, so nothing crosses products:
-- variant_option_values carries product_id precisely so the composite keys can
-- refuse a variant of A paired with a value of B.
-- name: SetVariantOptionValue :execrows
INSERT INTO variant_option_values (product_id, variant_id, option_id, option_value_id)
SELECT pv.product_id, pv.id, v.option_id, v.id
FROM product_variants pv
JOIN product_option_values v
  ON v.product_id = pv.product_id AND v.id = @option_value_id
WHERE pv.sku = @sku::text;

-- name: AdminVariantOptionValues :many
SELECT pv.sku, o.name AS option_name, v.value
FROM variant_option_values vov
JOIN product_variants pv ON pv.id = vov.variant_id
JOIN product_options o ON o.id = vov.option_id
JOIN product_option_values v ON v.id = vov.option_value_id
JOIN products p ON p.id = pv.product_id
WHERE p.slug = $1
ORDER BY pv.sku, o.position, o.id;

-- Lock before reading the label replaced, so the audit records the actual prior facts.
-- name: LockProductLabel :one
SELECT id, slug, origin, origin_en, domestic_party_name, domestic_party_phone,
       domestic_party_address, net_quantity, net_unit, min_age_months
FROM products WHERE slug = $1 FOR NO KEY UPDATE;

-- name: SetProductLabel :exec
UPDATE products SET origin = nullif(@origin::text, ''), origin_en = nullif(@origin_en::text, ''),
    domestic_party_name = nullif(@domestic_party_name::text, ''),
    domestic_party_phone = nullif(@domestic_party_phone::text, ''),
    domestic_party_address = nullif(@domestic_party_address::text, ''),
    net_quantity = @net_quantity, net_unit = nullif(@net_unit::text, ''), min_age_months = @min_age_months
WHERE id = @id;

-- name: LockProductInvoiceLine :one
SELECT id, tax_type, invoice_unit FROM products WHERE slug=$1 FOR NO KEY UPDATE;

-- name: SetProductInvoiceLine :exec
UPDATE products SET tax_type=$2, invoice_unit=$3 WHERE id=$1;

-- One row per shop day of [from_at, to_at), a day without sales included. The
-- units are those of the orders PaidByShopDay counts.
-- name: ProductUnitsByShopDay :many
SELECT d.day::date AS day, coalesce(sum(t.units), 0)::bigint AS units
FROM generate_series(shop_day(@from_at::timestamptz),
                     shop_day((@to_at::timestamptz) - interval '1 microsecond'),
                     interval '1 day') AS d(day)
LEFT JOIN (
    SELECT shop_day(o.placed_at) AS day, ol.quantity AS units
    FROM order_lines ol
    JOIN orders o ON o.id = ol.order_id
    JOIN sold_orders s ON s.id = o.id
    WHERE ol.product_id = @product_id::uuid
      AND o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
) t ON t.day = d.day::date
GROUP BY d.day
ORDER BY d.day;
