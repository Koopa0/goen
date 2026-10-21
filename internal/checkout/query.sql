-- Logged-in checkout writes several user foreign keys after it owns the cart.
-- Acquire their natural KEY SHARE first so account erasure and cart adoption use
-- the same user -> cart order. Guest checkout has no user and skips this query.
-- Lock order and privilege: see LockUser in internal/account/query.sql.
-- name: LockUserForCheckout :one
SELECT id FROM users WHERE id = @user_id::uuid FOR KEY SHARE;

-- Checkout locks catalogue roots in one canonical order before it snapshots
-- publication, prices and availability. SECURITY DEFINER keeps store's direct
-- product/variant UPDATE revoked while satisfying PostgreSQL's FOR UPDATE
-- privilege requirement.
-- name: LockCartCatalogue :exec
SELECT lock_cart_catalogue(@cart_id::uuid);

-- The name and phone a signed-in customer keeps on their account, for the
-- checkout's 「收件人同會員資料」. Blank is as good as none.
-- name: CheckoutProfile :one
SELECT coalesce(full_name, '')::text AS full_name, coalesce(phone, '')::text AS phone
FROM users WHERE id = @user_id;

-- name: ClearCart :exec
DELETE FROM cart_items WHERE cart_id = $1;

-- What the shopper had typed when they left for the carrier's store map. Written
-- on the cart so it ends with the cart.
-- name: SaveCheckoutDraft :exec
UPDATE carts SET checkout_draft = @draft::jsonb, checkout_draft_at = now()
WHERE id = @cart_id;

-- Read inside the window only; an older draft is as good as none.
-- name: ReadCheckoutDraft :one
SELECT checkout_draft::jsonb AS draft FROM carts
WHERE id = @cart_id AND checkout_draft IS NOT NULL
  AND checkout_draft_at > now() - @ttl::interval;

-- name: ClearCheckoutDraft :exec
UPDATE carts SET checkout_draft = NULL, checkout_draft_at = NULL
WHERE id = @cart_id AND checkout_draft IS NOT NULL;

-- name: ClearStaleCheckoutDrafts :exec
UPDATE carts SET checkout_draft = NULL, checkout_draft_at = NULL
WHERE checkout_draft IS NOT NULL AND checkout_draft_at < now() - @ttl::interval;

-- The delivery addresses an account has saved, scoped to the owner IN the query
-- rather than checked after the read: the id comes off a URL.
-- name: SavedAddresses :many
SELECT id, label, recipient_name, phone, postal_code, city, district, street, is_default
FROM addresses WHERE user_id = $1
ORDER BY is_default DESC, created_at;

-- What one version charges to send an order to one postal code. postal_code may
-- be empty: a convenience-store pickup has no postal code, so it matches no
-- prefix and gets the mainland answer.
-- name: ShippingZoneFor :one
SELECT coalesce(vz.surcharge_cents, 0)::bigint AS surcharge_cents,
       coalesce(localized_name(z.name, z.name_en, @locale::text), '')::text AS zone_name
FROM shipping_method_versions v
LEFT JOIN shipping_zone_prefixes zp
       ON nullif(@postal_code::text, '') IS NOT NULL
      AND zp.prefix = left(@postal_code::text, 3)
LEFT JOIN shipping_zones z ON z.id = zp.zone_id
LEFT JOIN shipping_version_zones vz
       ON vz.version_id = v.id AND vz.zone_id = zp.zone_id
WHERE v.id = @version_id;

-- name: ShippingVersion :one
SELECT v.id, sm.code, sm.destination_kind,
       localized_name(v.name, v.name_en, @locale::text) AS name,
       v.fee_cents, v.free_over_cents
FROM shipping_method_versions v
JOIN shipping_methods sm ON sm.id = v.method_id
WHERE v.id = $1 AND sm.is_active AND v.effective_at <= now();

-- next_order_number() is SECURITY DEFINER because store cannot write the
-- counter directly.
-- name: CreateOrder :one
INSERT INTO orders (
    order_number, user_id, shipping_version_id, shipping_method_code,
    shipping_method_name, shipping_cents, discount_cents, customer_note,
    locale
) VALUES (
    next_order_number(), @user_id, @shipping_version_id, @shipping_method_code,
    @shipping_method_name, @shipping_cents, @discount_cents, @customer_note,
    @locale
)
RETURNING id, order_number;

-- The price and warranty promise are COPIED rather than referenced: a later
-- catalogue change must not rewrite a placed order or shorten its cover.
-- name: CreateOrderLine :exec
INSERT INTO order_lines (
    order_id, product_id, variant_id, sku, product_name, variant_label,
    warranty_note, warranty_months, unit_price_cents, quantity, position
) VALUES (
    @order_id, @product_id, @variant_id, @sku, @product_name, @variant_label,
    @warranty_note, @warranty_months, @unit_price_cents, @quantity, @position
);

-- Both destination groups are written and exactly one is non-NULL, which
-- order_private_data_one_destination is what refuses anything else.
-- name: CreateOrderPrivateData :exec
INSERT INTO order_private_data (
    order_id, email, recipient_name, phone, postal_code, city, district, street,
    pickup_chain, pickup_store_code, pickup_store_name
) VALUES (
    @order_id, @email, @recipient_name, @phone,
    nullif(@postal_code::text, ''), nullif(@city::text, ''),
    nullif(@district::text, ''), nullif(@street::text, ''),
    nullif(@pickup_chain::text, ''), nullif(@pickup_store_code::text, ''),
    nullif(@pickup_store_name::text, '')
);

-- name: RecordCheckoutAttempt :exec
INSERT INTO checkout_attempts (idempotency_key, cart_id, order_id)
VALUES ($1, $2, $3);

-- name: CheckoutAttempt :one
SELECT cart_id, order_id FROM checkout_attempts WHERE idempotency_key = $1;

-- name: HoldForOrder :one
SELECT hold_inventory(
    @order_id, @variant_id, @quantity::integer, @hold_for::interval, @idempotency_key::text
);

-- name: RecordPlacedEvent :exec
INSERT INTO order_events (order_id, kind) VALUES ($1, 'placed');

-- From store_credit_balances, the one definition of the figure.
-- name: AvailableCredit :one
SELECT coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = $1), 0)::bigint;

-- Checkout holds the account row while it compares and spends the exact credit
-- in its quote. The narrow SECURITY DEFINER function supplies the row-lock
-- privilege without restoring UPDATE on the account table to store.
-- name: LockAvailableCredit :one
SELECT lock_store_credit_for_checkout(@user_id)::bigint;

-- A NEGATIVE amount, keyed on the order so a retried checkout debits once.
-- name: SpendCredit :one
SELECT spend_store_credit(@order_id, @amount_cents::bigint);

-- name: CreateInvoicePreference :exec
INSERT INTO invoice_preferences
    (order_id, invoice_type, carrier_code, donation_code, tax_id, customer_name, customer_email)
VALUES
    (@order_id, @invoice_type::text, nullif(@carrier_code::text, ''), nullif(@donation_code::text, ''),
     nullif(@tax_id::text, ''), @customer_name::text, @customer_email::text);

-- The window is decided HERE against the DATABASE's clock: starts_at defaults to
-- its now(), and comparing that to Go's is comparing two clocks. Checkout first
-- calls LockCouponForCheckout through a narrow privilege door; ordinary reads
-- need no row lock.
-- name: CouponByCode :one
SELECT id, code, description, kind, amount_cents, percent_bp,
       min_subtotal_cents, max_discount_cents,
       max_redemptions, per_customer_limit, is_active,
       (starts_at <= now() AND (ends_at IS NULL OR ends_at > now()))::boolean AS is_current
FROM coupons WHERE upper(code) = upper(@code::text);

-- Shared, so checkouts never wait for each other: what it waits for is an edit
-- to the zone surcharges or prefixes (LockZoneRates in internal/admin/shipping
-- takes the same key exclusively). Held from the quote to the COMMIT, where
-- orders_have_lines prices the order again from those rows; without it an edit
-- committed in between would make the two disagree and refuse the order.
-- name: LockZoneRatesForCheckout :exec
SELECT pg_advisory_xact_lock_shared(hashtextextended(
    'zone_prefixes', 628471039582915603::bigint));

-- name: LockCouponForCheckout :exec
SELECT lock_coupon_for_checkout(@code::text);

-- sqlc.narg on the user: redeem_coupon reads NULL as "no per-customer limit"
-- rather than as a customer whose id happens to be zero.
-- name: RedeemCoupon :one
SELECT redeem_coupon(@coupon_id, @order_id, sqlc.narg(user_id)::uuid, @amount_cents::bigint);

-- Checkout idempotency keys past their replay window. Deleting a row frees its
-- key to be replayed, which is why the window is weeks rather than hours.
-- name: DeleteOldCheckoutAttempts :exec
DELETE FROM checkout_attempts
WHERE created_at < now() - sqlc.arg(retain)::interval;

-- Hold the checkout's idempotency key for the length of this transaction. An
-- advisory lock rather than an early INSERT, whose row would hold the key with
-- order_id still NULL; xact, so it releases on commit or rollback.
-- name: LockCheckoutKey :exec
SELECT pg_advisory_xact_lock(hashtextextended(@idempotency_key::text, 0));

-- The order a completed attempt produced.
-- name: OrderNumberByID :one
SELECT order_number FROM orders WHERE id = @id;
