-- name: CartByToken :one
SELECT id, user_id FROM carts WHERE digest = $1;

-- name: CreateCart :one
INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id;

-- Serialize every writer of a cart aggregate before it touches cart_items.
-- Sorting makes two-cart operations such as account adoption use one lock order.
-- name: LockCarts :many
SELECT id FROM carts
WHERE id = ANY(@cart_ids::uuid[])
ORDER BY id
FOR UPDATE;

-- Guest-cart adoption takes the account row before any cart row, so two first
-- adopters meet here rather than at carts_one_per_user, and checkout's KEY SHARE
-- on the same row (LockUserForCheckout) never waits for a cart lock held by an
-- adopter. NO KEY UPDATE is the same strength as LockUser in
-- internal/account/query.sql, whose comment states the order every path keeps;
-- this copy exists so cart calls no account query. store's column UPDATE on
-- users is what PostgreSQL asks for.
-- name: LockUserForAdoption :one
SELECT id FROM users WHERE id = @user_id::uuid FOR NO KEY UPDATE;

-- Guest lines for adoption and merge, read after the cart lock is held, in the
-- order the guest added them: a merge that meets the line cap keeps the earliest.
-- name: CartItemRows :many
SELECT variant_id, quantity FROM cart_items WHERE cart_id = @cart_id::uuid
ORDER BY added_at, variant_id;

-- name: CartForUser :one
SELECT id FROM carts WHERE user_id = $1;

-- Read only after LockCarts has returned. The recheck keeps a second account
-- carrying the same guest cookie from taking over a cart the first account just
-- adopted.
-- name: CartOwner :one
SELECT user_id FROM carts WHERE id = @cart_id::uuid;

-- The predicate is a final ownership fence in addition to CartOwner. :execrows
-- makes a lost race distinguishable from a successful adoption.
-- name: AdoptCart :execrows
UPDATE carts SET user_id = @user_id::uuid
WHERE id = @cart_id::uuid AND user_id IS NULL;

-- name: DeleteCart :exec
DELETE FROM carts WHERE id = $1;

-- The caller has already clamped the line's total against sellable stock and
-- the line ceiling, so a conflict stores the total it was handed. It overwrites
-- rather than adds: two unlocked callers would each write a total computed from
-- the same stale read, so every caller must hold the cart row lock that
-- Lock takes in mutateCart.
-- name: AddCartItem :exec
INSERT INTO cart_items (cart_id, variant_id, quantity)
VALUES ($1, $2, $3)
ON CONFLICT (cart_id, variant_id) DO UPDATE
SET quantity = EXCLUDED.quantity;

-- name: SetCartItemQuantity :exec
UPDATE cart_items SET quantity = $3
WHERE cart_id = $1 AND variant_id = $2;

-- name: RemoveCartItem :exec
DELETE FROM cart_items WHERE cart_id = $1 AND variant_id = $2;

-- Everything in a cart, at CURRENT prices and availability. sellable_quantity is
-- stock above safety_stock, the floor record_inventory_movement enforces.
-- name: CartLines :many
SELECT
    pv.id AS variant_id,
    pv.product_id,
    pv.sku,
    pv.price_cents,
    pv.compare_at_price_cents,
    ci.quantity,
    (pv.stock_quantity - pv.safety_stock)::integer AS sellable_quantity,
    pv.is_active,
    p.status AS product_status,
    p.tax_type,
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    p.warranty_note,
    p.warranty_months,
    coalesce(b.name, '') AS brand,
    -- Localized because the cart line SHOWS the selection; the PDP's variant
    -- query matches on it and is exempt for exactly that reason.
    coalesce(
        (SELECT array_agg(localized_name(o.name, o.name_en, @locale::text)
                          ORDER BY o.position, o.id)
         FROM variant_option_values vov
         JOIN product_options o ON o.id = vov.option_id
         WHERE vov.variant_id = pv.id),
        ARRAY[]::text[]
    )::text[] AS option_names,
    coalesce(
        (SELECT array_agg(localized_name(v.value, v.value_en, @locale::text)
                          ORDER BY o.position, o.id)
         FROM variant_option_values vov
         JOIN product_options o ON o.id = vov.option_id
         JOIN product_option_values v ON v.id = vov.option_value_id
         WHERE vov.variant_id = pv.id),
        ARRAY[]::text[]
    )::text[] AS option_values,
    coalesce(img.media_digest, '') AS image_digest,
    coalesce(img.bundled_key, '') AS image_bundled_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width
FROM cart_items ci
JOIN product_variants pv ON pv.id = ci.variant_id
JOIN products p ON p.id = pv.product_id
LEFT JOIN brands b ON b.id = p.brand_id
LEFT JOIN LATERAL (
    -- The line's photograph, as the product page shows that variant. LIMIT 1
    -- keeps the subquery unflattened, so the view is read for this line only.
    SELECT v.media_digest, v.bundled_key, v.alt_text, v.alt_text_en, v.width
    FROM variant_primary_image v
    WHERE v.product_id = p.id AND v.variant_id = pv.id
    LIMIT 1
) img ON true
WHERE ci.cart_id = $1
ORDER BY ci.added_at, pv.id;

-- How many items a cart holds, for the header badge. UNITS, not lines.
-- name: CartItemCount :one
SELECT coalesce(sum(quantity), 0)::bigint FROM cart_items WHERE cart_id = $1;

-- The cart row is already locked by every caller. Updating an existing variant
-- does not consume another ECPay ItemSeq; inserting a distinct one does.
-- name: CartLineCapacity :one
SELECT count(*)::integer AS line_count,
       (count(*) FILTER (WHERE variant_id = @variant_id::uuid) > 0)::boolean
           AS already_present,
       coalesce(sum(quantity) FILTER (WHERE variant_id = @variant_id::uuid), 0)::integer
           AS existing_quantity
FROM cart_items
WHERE cart_id = @cart_id::uuid;

-- name: VariantForCart :one
SELECT pv.id, pv.is_active,
       (pv.stock_quantity - pv.safety_stock)::integer AS sellable_quantity,
       p.slug, p.status
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.id = $1;

-- option_name and value are identity, what the URL selects on; the PDP's own
-- query matches on them and is exempt for exactly that reason.
-- name: VariantProductSelection :one
SELECT
    p.slug,
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
JOIN products p ON p.id = pv.product_id
WHERE pv.id = $1 AND p.status = 'active' AND pv.is_active;

-- Each method's newest version, offered only for a cart its carrier will take.
-- The test is PER ITEM: one item that does not fit cannot be split, while NULL
-- on either side is unmeasured rather than too big.
-- name: ShippingChoices :many
SELECT DISTINCT ON (sm.id)
    v.id AS version_id,
    sm.code,
    sm.destination_kind,
    localized_name(v.name, v.name_en, @locale::text) AS name,
    coalesce(localized_name(v.carrier, v.carrier_en, @locale::text), '')::text AS carrier,
    v.fee_cents,
    v.free_over_cents,
    coalesce(
        (SELECT array_agg(localized_name(z.name, z.name_en, @locale::text) ORDER BY z.position, z.id)
         FROM shipping_version_zones vz
         JOIN shipping_zones z ON z.id = vz.zone_id
         WHERE vz.version_id = v.id),
        ARRAY[]::text[]
    )::text[] AS surcharge_zones
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE sm.is_active AND v.effective_at <= now()
  AND NOT EXISTS (
      SELECT 1
      FROM cart_items ci
      JOIN product_variants pv ON pv.id = ci.variant_id
      WHERE ci.cart_id = @cart_id
        AND ((sm.max_parcel_longest_mm IS NOT NULL AND pv.parcel_longest_mm IS NOT NULL
              AND pv.parcel_longest_mm > sm.max_parcel_longest_mm)
          OR (sm.max_parcel_sum_mm IS NOT NULL AND pv.parcel_sum_mm IS NOT NULL
              AND pv.parcel_sum_mm > sm.max_parcel_sum_mm)
          OR (sm.max_parcel_weight_g IS NOT NULL AND pv.parcel_weight_g IS NOT NULL
              AND pv.parcel_weight_g > sm.max_parcel_weight_g)))
ORDER BY sm.id, v.effective_at DESC;

-- An order's lines as a REORDER sees them. LEFT JOIN and not JOIN: variant_id is
-- nullable so a line survives its variant being deleted, and dropping those rows
-- would make the page say it added everything. A left-out line links to its
-- product only while the product is listed, even when its variant is not.
-- name: ReorderLines :many
SELECT ol.variant_id, ol.product_name, ol.variant_label, ol.quantity,
       coalesce(pv.is_active AND p.status = 'active', false)::boolean AS sellable,
       coalesce(pv.stock_quantity - pv.safety_stock, 0)::integer AS available,
       coalesce(p.status = 'active', false)::boolean AS listed,
       coalesce(p.slug, '')::text AS product_slug
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
LEFT JOIN product_variants pv ON pv.id = ol.variant_id
LEFT JOIN products p ON p.id = pv.product_id
WHERE o.order_number = $1
ORDER BY ol.position, ol.id;

-- A guest cart nobody has touched for the retention window. Its cookie lives as
-- long as the window and is set once, when the cart is created, so past it no
-- browser can name the cart. An account's cart (user_id set) is never listed.
-- Idle means none of created_at, updated_at (a checkout draft moves it) or a
-- line's added_at is inside the window. One batch at a time: the delete cascades
-- to cart_items, and SKIP LOCKED leaves a cart a writer holds for the next pass.
-- name: DeleteIdleGuestCarts :execrows
DELETE FROM carts
WHERE id IN (
    SELECT c.id FROM carts c
    WHERE c.user_id IS NULL
      AND c.updated_at < now() - sqlc.arg(retain)::interval
      AND NOT EXISTS (
          SELECT 1 FROM cart_items i
          WHERE i.cart_id = c.id AND i.added_at >= now() - sqlc.arg(retain)::interval)
    LIMIT sqlc.arg(batch)::integer
    FOR UPDATE OF c SKIP LOCKED
);
