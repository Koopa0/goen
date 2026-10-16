-- name: UpdateProfile :exec
UPDATE users SET full_name = $2, phone = $3 WHERE id = $1;

-- name: UserOrders :many
SELECT
    o.id,
    o.order_number,
    o.fulfillment_status,
    o.placed_at,
    t.total_cents,
    (SELECT count(*) FROM order_lines ol WHERE ol.order_id = o.id)::bigint AS line_count,
    -- Both columns: a captured card leaves the order committed and still owing,
    -- a fully store-credited one owes nothing and is not committed until it
    -- leaves pending.
    EXISTS (SELECT 1 FROM committed_orders c WHERE c.id = o.id) AS committed,
    order_amount_after_credit(o.id)::bigint AS owed_cents,
    -- one_last_day: every ordered unit is in a parcel, every parcel has arrived, and all share one last day to
    -- cancel. Anything else has no single day, as return_line_policy_window judges each line by its own parcel.
    -- rescission_ends is shop_today() while no parcel has arrived, so a reader checks one_last_day, never the date.
    (p.parcels > 0 AND p.arrived = p.parcels AND p.last_days = 1
     AND NOT EXISTS (SELECT 1 FROM order_lines ol WHERE ol.order_id = o.id
                     AND ol.quantity > coalesce((SELECT sum(sl.quantity) FROM order_shipment_lines sl
                                                 WHERE sl.order_line_id = ol.id), 0)))::boolean AS one_last_day,
    coalesce(p.last_day, shop_today())::date AS rescission_ends
FROM orders o
JOIN order_totals t ON t.order_id = o.id
LEFT JOIN LATERAL (
    SELECT count(*) AS parcels, count(s.delivered_at) AS arrived,
           count(DISTINCT return_window_ends(s.delivered_at)) AS last_days,
           min(return_window_ends(s.delivered_at)) AS last_day
    FROM order_shipments s WHERE s.order_id = o.id
) p ON true
WHERE o.user_id = @user_id
  AND (NOT @has_cursor::boolean OR (o.placed_at, o.id) < (@after_at::timestamptz, @after_id::uuid))
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @row_limit::integer;

-- A scalar subquery, so an account that has never held credit gets 0 and not no row.
-- name: StoreCreditBalance :one
SELECT coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = $1), 0)::bigint AS balance_cents;

-- name: AddressesForUser :many
SELECT id, label, recipient_name, phone, postal_code, city, district, street, is_default
FROM addresses WHERE user_id = $1
ORDER BY is_default DESC, created_at;

-- name: AddressForUser :one
SELECT id, label, recipient_name, phone, postal_code, city, district, street, is_default
FROM addresses
WHERE user_id = @user_id::uuid AND id = @address_id::uuid;

-- Keeping is_default out of the edit preserves a concurrent default selection.
-- name: UpdateAddress :execrows
UPDATE addresses
SET label = sqlc.narg(label)::text,
    recipient_name = @recipient_name::text,
    phone = @phone::text,
    postal_code = @postal_code::text,
    city = @city::text,
    district = @district::text,
    street = @street::text
WHERE user_id = @user_id::uuid AND id = @address_id::uuid;

-- name: CreateAddress :exec
INSERT INTO addresses (user_id, label, recipient_name, phone,
                       postal_code, city, district, street, is_default)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- Every path that locks both the account row and its carts or addresses takes
-- the account row first: cart adoption (NO KEY UPDATE through cart's
-- LockUserForAdoption, then LockCarts), a
-- default-address write (NO KEY UPDATE, then the address rows), checkout (KEY
-- SHARE via cart.LockUserForCheckout, then its cart) and erase_user (FOR
-- UPDATE, then everything it deletes). Cart-item and checkout-draft writes lock
-- only the cart, and non-default address writes take at most the foreign key's
-- KEY SHARE. None of them takes the account row after a cart or address row,
-- which keeps the order acyclic. Checkout's KEY SHARE does not wait for
-- adoption, but an erasure's UPDATE/DELETE still waits for it. NO KEY UPDATE
-- conflicts with itself and with UPDATE/DELETE, not with the KEY SHARE that
-- user foreign keys take. PostgreSQL asks for UPDATE on at least one column for
-- any row lock; store's column UPDATE on users is enough.
-- name: LockUser :one
SELECT id FROM users WHERE id = @user_id::uuid FOR NO KEY UPDATE;

-- Run in the same transaction as the set: addresses_one_default_per_user is unique.
-- name: ClearDefaultAddress :exec
UPDATE addresses SET is_default = false WHERE user_id = $1 AND is_default;

-- :execrows, so an id belonging to somebody else is ErrNotFound, not a silent success.
-- name: SetDefaultAddress :execrows
UPDATE addresses SET is_default = true WHERE id = $2 AND user_id = $1;

-- Scoped to the owner: an id belonging to somebody else must do nothing.
-- name: DeleteAddress :exec
DELETE FROM addresses WHERE id = $2 AND user_id = $1;

-- The cheapest sellable variant sets the price, the way the listing reads it.
-- name: WishlistItems :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    p.summary,
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
    -- The one variant a product has, when it has only one and it is in stock:
    -- the only case where saying "add to cart" names what goes in the cart.
    -- The nil uuid when there is none.
    coalesce((
        SELECT CASE WHEN count(*) = 1 AND bool_and(sv.stock_quantity > sv.safety_stock)
                    THEN (array_agg(sv.id))[1] END
        FROM product_variants sv
        WHERE sv.product_id = p.id AND sv.is_active
    ), '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS sole_variant_id,
    coalesce(img.media_digest, '') AS image_digest,
    coalesce(img.bundled_key, '') AS image_bundled_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width,
    coalesce(img.height, 0)::integer AS image_height
FROM wishlist_items w
JOIN products p ON p.id = w.product_id
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
    SELECT media_digest, bundled_key, alt_text, alt_text_en, width, height
    FROM product_images WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
-- status = 'active' as a LITERAL: a parameter cannot use the partial index.
WHERE w.user_id = $1 AND p.status = 'active'
ORDER BY w.created_at DESC, p.id DESC;

-- name: AddWishlistItem :exec
INSERT INTO wishlist_items (user_id, product_id)
SELECT $1, p.id FROM products p WHERE p.slug = @slug::text AND p.status = 'active'
ON CONFLICT (user_id, product_id) DO NOTHING;

-- name: RemoveWishlistItem :exec
DELETE FROM wishlist_items w
USING products p
WHERE w.product_id = p.id AND w.user_id = $1 AND p.slug = @slug::text;

-- name: WishlistHas :one
SELECT EXISTS (
    SELECT 1 FROM wishlist_items w JOIN products p ON p.id = w.product_id
    WHERE w.user_id = $1 AND p.slug = @slug::text
);

-- Derived on every read, never stored: a stored tier drifts from the orders
-- behind it the moment one is cancelled.
-- name: MemberStanding :one
SELECT
    membership_window_days()::integer AS window_days,
    member_spend(@user_id, NULL)::bigint AS spend_cents,
    coalesce((SELECT localized_name(t.name, t.name_en, @locale::text)
              FROM membership_tiers t
              WHERE t.id = member_tier(@user_id, NULL)), '')::text
        AS tier_name,
    coalesce((SELECT t.points_multiplier_bp FROM membership_tiers t
              WHERE t.id = member_tier(@user_id, NULL)), 10000)::integer
        AS multiplier_bp,
    coalesce((SELECT localized_name(n.name, n.name_en, @locale::text)
              FROM membership_tiers n
              WHERE n.min_spend_cents > member_spend(@user_id, NULL)
              ORDER BY n.min_spend_cents LIMIT 1), '')::text AS next_name,
    coalesce((SELECT n.min_spend_cents - member_spend(@user_id, NULL)
              FROM membership_tiers n
              WHERE n.min_spend_cents > member_spend(@user_id, NULL)
              ORDER BY n.min_spend_cents LIMIT 1), 0)::bigint AS next_needs_cents;

-- name: EmailVerification :one
SELECT (u.email_verified_at IS NOT NULL)::boolean AS verified,
       coalesce((SELECT v.email FROM email_verifications v
                 WHERE v.user_id = u.id AND v.expires_at > now()), '')::text AS pending_email
FROM users u WHERE u.id = $1;

-- name: IdentitiesForUser :many
SELECT provider, created_at FROM user_identities
WHERE user_id = @user_id ORDER BY created_at;
