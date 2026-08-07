-- The account an email names, for sign-in. lower(email) matches the unique
-- index, so a visitor who registered as Ming@Example.com signs in as
-- ming@example.com.
-- name: UserByEmail :one
SELECT id, email, password_hash, full_name, role
FROM users WHERE lower(email) = lower($1);

-- name: UserByID :one
SELECT id, email, full_name, phone, role, created_at,
       -- Whether this account can be signed into with a password at ALL. An
       -- account created from an identity provider has none, and unlinking the
       -- provider would then leave nobody able to reach it — which is what
       -- UnlinkGoogle refuses and what the account page decides from.
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

-- Sessions are found by the HASH of the cookie's token, never by the token: the
-- column holds a digest so a database leak does not hand over live sessions.
--
-- The expiry is checked HERE rather than by a sweeper, so an expired session is
-- dead the moment it expires even if nothing has cleaned it up yet.
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

-- Every other session of this account. Used when a password changes: a password
-- reset that leaves the thief's session alive has changed nothing.
-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= now();

-- Reset tokens that have stopped meaning anything: spent, or past their hour.
--
-- Nothing reads either state. SpendPasswordResetToken has `used_at IS NULL AND
-- expires_at > now()` in its own WHERE clause, so a dead row is already nobody —
-- and every reset goen has ever issued was still in the table, each carrying the
-- user id it belonged to.
--
-- A grace period rather than `now()`, so a row survives long enough to answer
-- "did they ask for a reset yesterday?" while support has somebody on the phone.
-- name: DeleteDeadResetTokens :exec
DELETE FROM password_reset_tokens
WHERE (used_at IS NOT NULL OR expires_at <= now())
  AND created_at < now() - sqlc.arg(grace)::interval;

-- Password reset. The token is stored hashed for the same reason a session is.
-- Issue a reset token.
--
-- The expiry is computed from the DATABASE's clock, not Go's. The window is
-- read back by `expires_at > now()`, which is the database's clock too, and
-- deriving the two ends of one comparison from two clocks is what made a coupon
-- created that instant read as "not started yet".
-- name: CreatePasswordResetToken :exec
INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
VALUES (@token_hash, @user_id, now() + @ttl::interval);

-- An unused, unexpired reset token. Both conditions are in the query, so a
-- token cannot be spent twice and cannot outlive its window.
-- name: PasswordResetToken :one
SELECT user_id FROM password_reset_tokens
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now();

-- Spend a reset token, ONCE.
--
-- :execrows and `used_at IS NULL` in the WHERE, so two requests carrying the
-- same token cannot both proceed: the loser updates zero rows and is refused.
-- A read-then-write guard in Go is a guard both of them pass, and the prize
-- here is somebody else's account.
--
-- The caller runs this in the SAME transaction as the password change. Spending
-- first and changing after would leave a token burnt on a password that never
-- changed — the customer is locked out AND their one reset is gone.
-- It RETURNS the user, so the account whose password changes is the one on the
-- row that was actually spent — not one read a moment earlier by a separate
-- statement that could disagree.
-- name: SpendPasswordResetToken :one
UPDATE password_reset_tokens SET used_at = now()
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
RETURNING user_id;

-- Every other unused token for this user, invalidated.
--
-- Somebody who clicked "forgot password" three times has three live tokens, and
-- two of them are in their mailbox after the reset. A mailbox is exactly what
-- an attacker reads.
-- name: InvalidateResetTokens :exec
UPDATE password_reset_tokens SET used_at = now()
WHERE user_id = $1 AND used_at IS NULL;

-- A guest cart adopted on sign-in.
--
-- The guest's lines are merged into the account's cart rather than replacing
-- it: someone who added things while signed out has not agreed to lose what
-- was already there. Quantities add, capped at the line ceiling.
-- name: MergeCartItems :exec
INSERT INTO cart_items (cart_id, variant_id, quantity)
SELECT $2, src.variant_id, src.quantity FROM cart_items src WHERE src.cart_id = $1
ON CONFLICT (cart_id, variant_id) DO UPDATE
SET quantity = least(cart_items.quantity + EXCLUDED.quantity, 999);

-- name: CartForUser :one
SELECT id FROM carts WHERE user_id = $1;

-- name: AdoptCart :exec
UPDATE carts SET user_id = $2 WHERE id = $1;

-- name: DeleteCart :exec
DELETE FROM carts WHERE id = $1;

-- The orders on an account, newest first.
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
    -- The funding state, which the status cannot supply. This list badged every
    -- 'pending' order 待付款, so an order paid minutes ago read as unpaid until a
    -- human at the shop moved it to picking. Both columns for the reason
    -- OrderSummaryByNumber carries both: a captured card leaves the order
    -- committed and still owing, a fully store-credited one owes nothing and is
    -- not committed until it leaves pending.
    (o.id IN (SELECT id FROM committed_orders))::boolean AS committed,
    order_amount_owed(o.id)::bigint AS owed_cents
FROM orders o
WHERE o.user_id = $1
ORDER BY o.placed_at DESC, o.id DESC
LIMIT $2;

-- One order, scoped to its owner.
--
-- The user_id is part of the WHERE, not checked afterwards in Go: an order
-- belonging to someone else must be indistinguishable from one that does not
-- exist, and a query that returns the row and then filters is one forgotten
-- branch away from leaking it.
-- name: UserOrderByNumber :one
SELECT
    o.id, o.order_number, o.fulfillment_status, o.placed_at,
    o.shipping_cents, o.discount_cents, o.tax_cents, o.shipping_method_name,
       -- WHICH discount, joined rather than snapshotted: coupons.code is never
       -- updated and the FK is ON DELETE RESTRICT, so one join always reaches it.
       -- An order used to show "折扣 −NT$200" and nothing said why, to the
       -- customer or to the shop.
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

-- The store-credit balance on an account, from the one view that defines it.
--
-- Wrapped in a scalar subquery so a customer who has never held credit gets 0
-- rather than no row: the account is created on first use, and having none is the
-- ordinary state of most accounts rather than an error.
-- name: StoreCreditBalance :one
SELECT coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = $1), 0)::bigint AS balance_cents;

-- name: AddressesForUser :many
SELECT id, label, recipient_name, phone, postal_code, city, district, street, is_default
FROM addresses WHERE user_id = $1
ORDER BY is_default DESC, created_at;

-- Save a delivery address to an account.
-- name: CreateAddress :exec
INSERT INTO addresses (user_id, label, recipient_name, phone,
                       postal_code, city, district, street, is_default)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- Clear every default on an account, so setting a new one cannot collide with
-- addresses_one_default_per_user. Run in the same transaction as the set.
-- name: ClearDefaultAddress :exec
UPDATE addresses SET is_default = false WHERE user_id = $1 AND is_default;

-- :execrows, so an id belonging to somebody else is ErrNotFound rather than a
-- silent success — the same defect the three admin toggles had.
-- name: SetDefaultAddress :execrows
UPDATE addresses SET is_default = true WHERE id = $2 AND user_id = $1;

-- Deleting is scoped to the owner for the same reason reading is: an id that
-- belongs to someone else must do nothing, not delete their address.
-- name: DeleteAddress :exec
DELETE FROM addresses WHERE id = $2 AND user_id = $1;

-- name: EraseUser :exec
SELECT erase_user($1);

-- A customer's saved products, newest first.
--
-- The tile carries everything the product grid needs, read the same way the
-- listing reads it: the cheapest sellable variant sets the price, so a product
-- does not show one figure here and another on its own page.
-- name: WishlistItems :many
SELECT
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    p.summary,
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
-- status = 'active' as a LITERAL: the partial index cannot be used against a
-- parameter the planner cannot prove is always 'active'.
WHERE w.user_id = $1 AND p.status = 'active'
ORDER BY w.created_at DESC, p.id DESC;

-- Save a product. Idempotent: saving something twice is one entry, not an
-- error the customer did nothing to deserve.
-- name: AddWishlistItem :exec
INSERT INTO wishlist_items (user_id, product_id)
SELECT $1, p.id FROM products p WHERE p.slug = @slug::text AND p.status = 'active'
ON CONFLICT (user_id, product_id) DO NOTHING;

-- name: RemoveWishlistItem :exec
DELETE FROM wishlist_items w
USING products p
WHERE w.product_id = p.id AND w.user_id = $1 AND p.slug = @slug::text;

-- Whether this customer has saved this product, for the button on its page.
-- name: WishlistHas :one
SELECT EXISTS (
    SELECT 1 FROM wishlist_items w JOIN products p ON p.id = w.product_id
    WHERE w.user_id = $1 AND p.slug = @slug::text
);

-- A customer's 會員等級: what they have spent in the window, the tier it earns,
-- and what the next one needs.
--
-- Derived every time it is read, never stored. A stored tier drifts from the
-- orders behind it the moment one is refunded, and nobody notices until a
-- customer asks why a benefit they were told they had has gone.
--
-- The NEXT tier is part of the same answer because "NT$8,000 more for 金卡" is
-- the only part of this a customer can act on.
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

-- Ask for an address to be proved.
--
-- Replaces any earlier request for this customer, so a mailbox holds one live link.
-- The address is stored as given and compared with lower() at confirmation, the way
-- users.email is.
-- name: RequestEmailVerification :exec
INSERT INTO email_verifications (user_id, email, digest, expires_at)
VALUES (@user_id, @email::text, @digest, now() + @ttl::interval)
ON CONFLICT (user_id) DO UPDATE
    SET email      = EXCLUDED.email,
        digest     = EXCLUDED.digest,
        expires_at = EXCLUDED.expires_at,
        created_at = now();

-- Spend a verification link.
--
-- Spent BY THIS STATEMENT, not by a check above it: several requests carrying one
-- token all reach this line and exactly one deletes a row. Expiry is judged by the
-- database's clock, the same one that wrote expires_at.
-- name: SpendEmailVerification :one
DELETE FROM email_verifications
WHERE digest = $1 AND expires_at > now()
RETURNING user_id, email;

-- Move the address and mark it proved.
--
-- One statement, because they are one fact: an address goen has proved and an
-- address goen is using must not be able to disagree. At registration the address
-- is already the user's and the move is a no-op; on a change it is the point.
--
-- users_email_key refuses an address that now belongs to somebody else — which can
-- happen between the request and the confirmation, and is the only place that race
-- can be caught.
-- name: SetVerifiedEmail :exec
UPDATE users SET email = @email::text, email_verified_at = now()
WHERE id = @user_id;

-- Is this customer's current address proved?
-- name: EmailVerification :one
SELECT (u.email_verified_at IS NOT NULL)::boolean AS verified,
       coalesce((SELECT v.email FROM email_verifications v
                 WHERE v.user_id = u.id AND v.expires_at > now()), '')::text AS pending_email
FROM users u WHERE u.id = $1;

-- Does this address already belong to a DIFFERENT account?
--
-- Asked before a change request so the refusal is a sentence rather than a
-- constraint name. It is not the guard: users_email_key is, because an address can
-- be taken between this question and the confirmation.
-- name: EmailBelongsToSomebodyElse :one
SELECT EXISTS (
    SELECT 1 FROM users
    WHERE lower(email) = lower(@email::text) AND id <> @user_id
) AS taken;
-- Who a Google account belongs to here, if anybody.
--
-- Keyed on the SUBJECT rather than the email, and that is the whole reason
-- user_identities exists as a table instead of a column on users: a Google
-- account can change its address, and a released Workspace address can be
-- reassigned to a different person. The subject is stable for the life of the
-- account and identifies the same human across both.
-- name: UserByGoogleSubject :one
SELECT u.id, u.email, u.full_name, u.role
FROM user_identities i
JOIN users u ON u.id = i.user_id
WHERE i.provider = 'google' AND i.provider_subject = @subject::text;

-- The account an address belongs to, and whether it has been PROVED.
--
-- Both halves, because linking a Google identity to an existing account turns on
-- the second: an address goen has not verified may belong to whoever registered
-- it rather than to whoever reads the mailbox. See linkOrCreate.
-- name: UserForOAuthLink :one
SELECT id, email, full_name, role,
       (email_verified_at IS NOT NULL)::boolean AS verified,
       (password_hash IS NOT NULL)::boolean AS has_password
FROM users WHERE lower(email) = lower(@email::text);

-- Create an account from an identity provider.
--
-- No password hash at all, which is legal: users.password_hash is nullable and
-- Authenticate refuses a NULL one by name. Somebody who wants a password later
-- gets it through /forgot, which is already the one path that proves they own
-- the mailbox.
--
-- email_verified_at is set from Google's own claim, and the CALLER checks that
-- claim first — a provider that has not verified an address has proved nothing
-- about it, and copying that here would launder somebody else's guess into
-- goen's own record.
-- name: CreateUserFromIdentity :one
INSERT INTO users (email, full_name, email_verified_at)
VALUES (@email::text, nullif(@full_name::text, ''), now())
RETURNING id, email, full_name, role;

-- Link a provider account to a goen one.
--
-- ON CONFLICT DO NOTHING against (provider, provider_subject): two tabs
-- finishing one sign-in are one link rather than a unique-violation the customer
-- reads as a failed login.
-- name: LinkIdentity :exec
INSERT INTO user_identities (user_id, provider, provider_subject)
VALUES (@user_id, 'google', @subject::text)
ON CONFLICT (provider, provider_subject) DO NOTHING;

-- The providers linked to an account, for the account page.
-- name: IdentitiesForUser :many
SELECT provider, created_at FROM user_identities
WHERE user_id = @user_id ORDER BY created_at;

-- Unlink a provider.
--
-- :execrows, and the caller refuses when the account has NO PASSWORD: unlinking
-- the only way in locks somebody out of their own account, and the row count is
-- how the caller learns whether it actually happened.
-- name: UnlinkIdentity :execrows
DELETE FROM user_identities
WHERE user_id = @user_id AND provider = @provider::text;
