-- lower(email) matches the unique index.
-- name: UserByEmail :one
SELECT id, email, password_hash, full_name, role
FROM users WHERE lower(email) = lower($1);

-- A reset token and its outbox message are created while this lock is held.
-- erase_user takes FOR UPDATE on the same row, so either both reset records
-- commit first and erasure purges them, or erasure wins and this returns no row.
-- name: UserForPasswordReset :one
SELECT id, email
FROM users
WHERE lower(email) = lower($1)
FOR KEY SHARE;

-- name: UserByID :one
SELECT id, email, full_name, phone, role, created_at,
       (password_hash IS NOT NULL)::boolean AS has_password
FROM users WHERE id = $1;

-- name: CreateUser :one
INSERT INTO users (email, password_hash, full_name, phone)
VALUES ($1, $2, $3, $4)
RETURNING id, email, full_name, role;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: TouchLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: UpdateProfile :exec
UPDATE users SET full_name = $2, phone = $3 WHERE id = $1;

-- The expiry is checked here, so an expired session is dead before anything sweeps it.
-- name: SessionUser :one
SELECT u.id, u.email, u.full_name, u.role
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, user_agent, ip, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= now();

-- A grace period rather than now(), so support can still answer "did they ask
-- for a reset yesterday?".
-- name: DeleteDeadResetTokens :exec
DELETE FROM password_reset_tokens
WHERE (used_at IS NOT NULL OR expires_at <= now())
  AND created_at < now() - sqlc.arg(grace)::interval;

-- The expiry is computed from the DATABASE's clock, which is what reads it back.
-- name: CreatePasswordResetToken :exec
INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
VALUES (@token_hash, @user_id, now() + @ttl::interval);

-- name: PasswordResetToken :one
SELECT user_id FROM password_reset_tokens
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now();

-- Completion takes the account row before it spends the token. erase_user uses
-- the same user-before-token order, so the two operations cannot deadlock. The
-- exclusive row lock also serializes different live tokens for one account;
-- otherwise two KEY SHARE holders can deadlock while both upgrade to write the
-- password, or let the later writer silently replace the earlier reset.
-- name: LockUserForPasswordReset :one
SELECT id FROM users WHERE id = @user_id::uuid FOR UPDATE;

-- used_at IS NULL is in the WHERE, so two requests carrying one token cannot both
-- win, and the caller runs this in the SAME transaction as the password change.
-- name: SpendPasswordResetToken :one
UPDATE password_reset_tokens SET used_at = now()
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
RETURNING user_id;

-- Somebody who asked three times leaves two more live links in their mailbox.
-- name: InvalidateResetTokens :exec
UPDATE password_reset_tokens SET used_at = now()
WHERE user_id = $1 AND used_at IS NULL;

-- Once an account moves to a new mailbox, a queued reset for its old address is
-- both dead authority and retained PII. Keep this query operation-specific: an
-- address change has no authority to remove other outbox topics.
-- name: DeletePasswordResetMessagesForEmail :exec
DELETE FROM outbox_messages
WHERE topic = 'account.password_reset'
  AND lower(coalesce(payload ->> 'email', payload ->> 'Email', '')) =
      lower(@email::text);

-- Quantities add rather than replace, capped at the line ceiling.
-- name: MergeCartItems :exec
INSERT INTO cart_items (cart_id, variant_id, quantity)
SELECT $2, src.variant_id, src.quantity FROM cart_items src WHERE src.cart_id = $1
ON CONFLICT (cart_id, variant_id) DO UPDATE
SET quantity = least(cart_items.quantity + EXCLUDED.quantity, 999);

-- The account row is the stable lock for deciding which of two guest carts is
-- the first one this user adopts. A SECURITY DEFINER function is required
-- because store has only narrow authentication-column UPDATE grants, not
-- authority for a general users row lock.
-- name: LockUserForCartAdoption :one
SELECT lock_user_for_cart_adoption(@user_id::uuid);

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

-- name: UserOrders :many
SELECT
    o.order_number,
    o.fulfillment_status,
    o.placed_at,
    o.shipping_cents,
    o.discount_cents,
    o.tax_cents,
    coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
              WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
    (SELECT count(*) FROM order_lines ol WHERE ol.order_id = o.id)::bigint AS line_count,
    -- Both columns: a captured card leaves the order committed and still owing,
    -- a fully store-credited one owes nothing and is not committed until it
    -- leaves pending.
    (o.id IN (SELECT id FROM committed_orders))::boolean AS committed,
    order_amount_owed(o.id)::bigint AS owed_cents
FROM orders o
WHERE o.user_id = $1
ORDER BY o.placed_at DESC, o.id DESC
LIMIT $2;

-- The user_id is part of the WHERE, never checked afterwards in Go.
-- name: UserOrderByNumber :one
SELECT
    o.id, o.order_number, o.fulfillment_status, o.placed_at,
    o.shipping_cents, o.discount_cents, o.tax_cents, o.shipping_method_name,
       -- Joined rather than snapshotted: coupons.code is never updated and the
       -- FK is ON DELETE RESTRICT, so one join always reaches it.
       coalesce((SELECT c.code || ' · ' || c.description
                 FROM coupon_redemptions cr JOIN coupons c ON c.id = cr.coupon_id
                 WHERE cr.order_id = o.id), '')::text AS discount_reason,
    coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
              WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
    coalesce(pd.email, '') AS email,
    coalesce(pd.recipient_name, '') AS recipient_name,
    coalesce(pd.phone, '') AS phone,
    coalesce(pd.postal_code, '') AS postal_code,
    coalesce(pd.city, '') AS city,
    coalesce(pd.district, '') AS district,
    coalesce(pd.street, '') AS street,
    coalesce(pd.pickup_brand, '') AS pickup_brand,
    coalesce(pd.pickup_store_code, '') AS pickup_store_code,
    coalesce(pd.pickup_store_name, '') AS pickup_store_name,
    -- See UserOrders: the status alone cannot say whether anything is still owed.
    (o.id IN (SELECT id FROM committed_orders))::boolean AS committed,
    order_amount_owed(o.id)::bigint AS owed_cents
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.order_number = $1 AND o.user_id = $2;

-- A scalar subquery, so an account that has never held credit gets 0 and not no row.
-- name: StoreCreditBalance :one
SELECT coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = $1), 0)::bigint AS balance_cents;

-- name: AddressesForUser :many
SELECT id, label, recipient_name, phone, postal_code, city, district, street, is_default
FROM addresses WHERE user_id = $1
ORDER BY is_default DESC, created_at;

-- name: CreateAddress :exec
INSERT INTO addresses (user_id, label, recipient_name, phone,
                       postal_code, city, district, street, is_default)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- Run in the same transaction as the set: addresses_one_default_per_user is unique.
-- name: ClearDefaultAddress :exec
UPDATE addresses SET is_default = false WHERE user_id = $1 AND is_default;

-- :execrows, so an id belonging to somebody else is ErrNotFound, not a silent success.
-- name: SetDefaultAddress :execrows
UPDATE addresses SET is_default = true WHERE id = $2 AND user_id = $1;

-- Scoped to the owner: an id belonging to somebody else must do nothing.
-- name: DeleteAddress :exec
DELETE FROM addresses WHERE id = $2 AND user_id = $1;

-- name: EraseUser :exec
SELECT erase_user($1);

-- The cheapest sellable variant sets the price, the way the listing reads it.
-- name: WishlistItems :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    p.summary,
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
    coalesce(img.height, 0)::integer AS image_height
FROM wishlist_items w
JOIN products p ON p.id = w.product_id
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
    member_spend(@user_id, @window_days::integer, NULL)::bigint AS spend_cents,
    coalesce((SELECT localized_name(t.name, t.name_en, @locale::text)
              FROM membership_tiers t
              WHERE t.id = member_tier(@user_id, @window_days::integer, NULL)), '')::text
        AS tier_name,
    coalesce((SELECT t.points_multiplier_bp FROM membership_tiers t
              WHERE t.id = member_tier(@user_id, @window_days::integer, NULL)), 10000)::integer
        AS multiplier_bp,
    coalesce((SELECT localized_name(n.name, n.name_en, @locale::text)
              FROM membership_tiers n
              WHERE n.min_spend_cents > member_spend(@user_id, @window_days::integer, NULL)
              ORDER BY n.min_spend_cents LIMIT 1), '')::text AS next_name,
    coalesce((SELECT n.min_spend_cents - member_spend(@user_id, @window_days::integer, NULL)
              FROM membership_tiers n
              WHERE n.min_spend_cents > member_spend(@user_id, @window_days::integer, NULL)
              ORDER BY n.min_spend_cents LIMIT 1), 0)::bigint AS next_needs_cents;

-- Delete the queued copy before replacing its verification row. Both statements
-- run after the user lock in the same transaction, so every surviving message
-- has a surviving digest that erasure can identify without claiming the mailbox.
-- name: DeleteOutstandingEmailVerificationMessage :exec
DELETE FROM outbox_messages m
USING email_verifications v
WHERE v.user_id = @user_id::uuid
  AND m.topic = 'account.email_verify'
  AND m.dedupe_key = 'verify:' || encode(v.digest, 'hex');

-- Replaces any earlier request, so a mailbox holds one live link.
-- name: RequestEmailVerification :exec
INSERT INTO email_verifications (user_id, email, digest, expires_at)
VALUES (@user_id, @email::text, @digest, now() + @ttl::interval)
ON CONFLICT (user_id) DO UPDATE
    SET email      = EXCLUDED.email,
        digest     = EXCLUDED.digest,
        expires_at = EXCLUDED.expires_at,
        created_at = now();

-- Spent BY THIS STATEMENT: several requests carrying one token reach it and one wins.
-- name: SpendEmailVerification :one
DELETE FROM email_verifications
WHERE digest = $1 AND expires_at > now()
RETURNING user_id, email;

-- A plain lookup names the account to lock before SpendEmailVerification takes
-- the token row. erase_user uses the same user-before-token order.
-- name: EmailVerificationToken :one
SELECT user_id, email FROM email_verifications
WHERE digest = $1 AND expires_at > now();

-- name: LockUserForEmailVerification :one
SELECT id, email FROM users WHERE id = @user_id::uuid FOR UPDATE;

-- One statement, because an address goen has proved and one goen is using must
-- not be able to disagree. users_email_key catches an address taken in between.
-- name: SetVerifiedEmail :exec
UPDATE users SET email = @email::text, email_verified_at = now()
WHERE id = @user_id;

-- name: EmailVerification :one
SELECT (u.email_verified_at IS NOT NULL)::boolean AS verified,
       coalesce((SELECT v.email FROM email_verifications v
                 WHERE v.user_id = u.id AND v.expires_at > now()), '')::text AS pending_email
FROM users u WHERE u.id = $1;

-- Not the guard: users_email_key is, because an address can be taken in between.
-- name: EmailBelongsToSomebodyElse :one
SELECT EXISTS (
    SELECT 1 FROM users
    WHERE lower(email) = lower(@email::text) AND id <> @user_id
) AS taken;
-- Keyed on the SUBJECT: a Google account can change address, and a released
-- Workspace address can be reassigned to somebody else.
-- name: UserByGoogleSubject :one
SELECT u.id, u.email, u.full_name, u.role
FROM user_identities i
JOIN users u ON u.id = i.user_id
WHERE i.provider = 'google' AND i.provider_subject = @subject::text;

-- verified is what linking turns on: an unverified address may belong to whoever
-- registered it rather than to whoever reads the mailbox.
-- name: UserForOAuthLink :one
SELECT id, email, full_name, role,
       (email_verified_at IS NOT NULL)::boolean AS verified,
       (password_hash IS NOT NULL)::boolean AS has_password
FROM users WHERE lower(email) = lower(@email::text);

-- No password hash, which is legal; the CALLER checks the provider's verified
-- claim before email_verified_at is set from it.
-- name: CreateUserFromIdentity :one
INSERT INTO users (email, full_name, email_verified_at)
VALUES (@email::text, nullif(@full_name::text, ''), now())
RETURNING id, email, full_name, role;

-- ON CONFLICT DO NOTHING: two tabs finishing one sign-in are one link.
-- name: LinkIdentity :exec
INSERT INTO user_identities (user_id, provider, provider_subject)
VALUES (@user_id, 'google', @subject::text)
ON CONFLICT (provider, provider_subject) DO NOTHING;

-- name: IdentitiesForUser :many
SELECT provider, created_at FROM user_identities
WHERE user_id = @user_id ORDER BY created_at;

-- :execrows: the caller refuses when the account has no password, and the row
-- count is how it learns whether the delete happened.
-- name: UnlinkIdentity :execrows
DELETE FROM user_identities
WHERE user_id = @user_id AND provider = @provider::text;
