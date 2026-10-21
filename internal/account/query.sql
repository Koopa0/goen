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
