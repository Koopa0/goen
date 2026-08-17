-- name: CartByToken :one
SELECT id, user_id FROM carts WHERE token_hash = $1;

-- name: CreateCart :one
INSERT INTO carts (token_hash, user_id) VALUES ($1, $2) RETURNING id;

-- least() caps a repeat add at the CHECK's own ceiling rather than raising a
-- constraint violation the visitor did nothing to deserve.
-- name: AddCartItem :exec
INSERT INTO cart_items (cart_id, variant_id, quantity)
VALUES ($1, $2, $3)
ON CONFLICT (cart_id, variant_id) DO UPDATE
SET quantity = least(cart_items.quantity + EXCLUDED.quantity, 999);

-- name: SetCartItemQuantity :exec
UPDATE cart_items SET quantity = $3
WHERE cart_id = $1 AND variant_id = $2;

-- name: RemoveCartItem :exec
DELETE FROM cart_items WHERE cart_id = $1 AND variant_id = $2;

-- name: ClearCart :exec
DELETE FROM cart_items WHERE cart_id = $1;

-- Everything in a cart, at CURRENT prices and availability. sellable_quantity is
-- stock above safety_stock, the floor record_inventory_movement enforces.
-- name: CartLines :many
SELECT
    pv.id AS variant_id,
    pv.sku,
    pv.price_cents,
    pv.compare_at_price_cents,
    ci.quantity,
    (pv.stock_quantity - pv.safety_stock)::integer AS sellable_quantity,
    pv.is_active,
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    b.name AS brand,
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
    coalesce(img.storage_key, '') AS image_key,
    coalesce(img.alt_text, '') AS image_alt
FROM cart_items ci
JOIN product_variants pv ON pv.id = ci.variant_id
JOIN products p ON p.id = pv.product_id
JOIN brands b ON b.id = p.brand_id
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text FROM product_images
    WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE ci.cart_id = $1
ORDER BY ci.added_at, pv.id;

-- How many items a cart holds, for the header badge. UNITS, not lines.
-- name: CartItemCount :one
SELECT coalesce(sum(quantity), 0)::bigint FROM cart_items WHERE cart_id = $1;

-- name: VariantForCart :one
SELECT pv.id, pv.is_active,
       (pv.stock_quantity - pv.safety_stock)::integer AS sellable_quantity,
       p.slug, p.status
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.id = $1;

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
    v.free_over_cents
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
    shipping_method_name, shipping_cents, discount_cents, tax_cents, customer_note,
    locale
) VALUES (
    next_order_number(), @user_id, @shipping_version_id, @shipping_method_code,
    @shipping_method_name, @shipping_cents, @discount_cents, 0, @customer_note,
    @locale
)
RETURNING id, order_number;

-- The price is COPIED rather than referenced: a later price change must not
-- rewrite a placed order.
-- name: CreateOrderLine :exec
INSERT INTO order_lines (
    order_id, variant_id, sku, product_name, variant_label,
    unit_price_cents, quantity, position
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- Both destination groups are written and exactly one is non-NULL, which
-- order_private_data_one_destination is what refuses anything else.
-- name: CreateOrderPrivateData :exec
INSERT INTO order_private_data (
    order_id, email, recipient_name, phone, postal_code, city, district, street,
    pickup_brand, pickup_store_code, pickup_store_name
) VALUES (
    @order_id, @email, @recipient_name, @phone,
    nullif(@postal_code::text, ''), nullif(@city::text, ''),
    nullif(@district::text, ''), nullif(@street::text, ''),
    nullif(@pickup_brand::text, ''), nullif(@pickup_store_code::text, ''),
    nullif(@pickup_store_name::text, '')
);

-- name: OrderBelongsTo :one
SELECT EXISTS (
    SELECT 1 FROM orders WHERE order_number = $1 AND user_id = $2
);

-- name: OrderSummaryByNumber :one
SELECT o.id, o.order_number, o.fulfillment_status,
       o.shipping_cents, o.discount_cents, o.tax_cents,
       -- WHICH discount, joined rather than snapshotted: coupons.code is never
       -- updated and the FK is ON DELETE RESTRICT, so one join always reaches it.
       coalesce((SELECT c.code || ' · ' || c.description
                 FROM coupon_redemptions cr JOIN coupons c ON c.id = cr.coupon_id
                 WHERE cr.order_id = o.id), '')::text AS discount_reason,
       o.shipping_method_name, o.placed_at,
       coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                 WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
       coalesce(pd.email, '') AS email,
       coalesce(pd.postal_code, '') AS postal_code,
       coalesce(pd.city, '') AS city,
       coalesce(pd.district, '') AS district,
       coalesce(pd.street, '') AS street,
       coalesce(pd.pickup_brand, '') AS pickup_brand,
       coalesce(pd.pickup_store_code, '') AS pickup_store_code,
       coalesce(pd.pickup_store_name, '') AS pickup_store_name,
       -- 'pending' does NOT mean unpaid: a webhook can capture minutes before
       -- the shop moves the order to picking.
       (o.id IN (SELECT id FROM committed_orders))::boolean AS committed,
       -- NOT derivable from `committed`: a fully store-credited order has no
       -- payment row and stays 'pending' while the customer owes nothing.
       order_amount_owed(o.id)::bigint AS owed_cents
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.order_number = $1;

-- An order's lines as a REORDER sees them. LEFT JOIN and not JOIN: variant_id is
-- nullable so a line survives its variant being deleted, and dropping those rows
-- would make the page say it added everything.
-- name: ReorderLines :many
SELECT ol.variant_id, ol.product_name, ol.variant_label, ol.quantity,
       coalesce(pv.is_active AND p.status = 'active', false)::boolean AS sellable,
       coalesce(pv.stock_quantity - pv.safety_stock, 0)::integer AS available
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
LEFT JOIN product_variants pv ON pv.id = ol.variant_id
LEFT JOIN products p ON p.id = pv.product_id
WHERE o.order_number = $1
ORDER BY ol.position, ol.id;

-- name: OrderLinesByOrder :many
SELECT sku, product_name, variant_label, unit_price_cents, quantity
FROM order_lines WHERE order_id = $1 ORDER BY position, id;

-- name: RecordCheckoutAttempt :exec
INSERT INTO checkout_attempts (idempotency_key, cart_id, order_id)
VALUES ($1, $2, $3)
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: CheckoutAttempt :one
SELECT order_id FROM checkout_attempts WHERE idempotency_key = $1;

-- name: HoldForOrder :one
SELECT hold_inventory(
    @order_id, @variant_id, @quantity::integer, @expires_at, @idempotency_key::text
);

-- name: RecordPlacedEvent :exec
INSERT INTO order_events (order_id, kind) VALUES ($1, 'placed');

-- The customer sees WHAT happened, never WHO did it; the back office reads the
-- same table with the actor joined.
-- name: OrderTimeline :many
SELECT kind, note, occurred_at
FROM order_events WHERE order_id = $1 ORDER BY occurred_at, id;

-- name: OrderTracking :many
SELECT carrier, tracking_number, shipped_at, delivered_at
FROM order_shipments WHERE order_id = $1 ORDER BY shipped_at, id;

-- Reservations whose hold has run out and whose order never got funded.
-- name: ExpiredReservations :many
SELECT ir.id
FROM inventory_reservations ir
JOIN orders o ON o.id = ir.order_id
WHERE ir.state = 'held'
  AND ir.expires_at < now()
  AND NOT order_is_committed(ir.order_id)
  -- Committed is not the whole question: a zero-owed order has no payment row and
  -- sits at 'pending' while the customer has already paid in full.
  AND (o.fulfillment_status = 'cancelled' OR order_amount_owed(ir.order_id) <> 0)
ORDER BY ir.expires_at
LIMIT $1;

-- name: ReleaseReservation :exec
SELECT release_reservation($1);

-- The customer's own cancellation. The ABSENCE of an actor is what distinguishes
-- it from a back-office cancel, and it is carried structurally because the
-- customer's own order page renders any note in whatever language it was written.
-- name: RecordCancellation :exec
INSERT INTO order_events (order_id, kind)
SELECT id, 'cancelled' FROM orders WHERE order_number = $1;

-- Ordered by id so two cancellations of one order take the variant locks in the
-- same sequence; release_reservation locks the variant and then the order.
-- name: HeldReservationsForOrder :many
SELECT r.id FROM inventory_reservations r
JOIN orders o ON o.id = r.order_id
WHERE o.order_number = $1 AND r.state = 'held'
ORDER BY r.id;

-- Both predicates are load-bearing: `pending` refuses a second cancellation,
-- `not committed` refuses one somebody has paid for. In the WHERE clause, so two
-- cancellations racing a capture cannot both decide it was cancellable.
-- name: CancelOrderByCustomer :execrows
UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
WHERE order_number = $1
  AND fulfillment_status = 'pending'
  AND id NOT IN (SELECT id FROM committed_orders);

-- From store_credit_balances, the one definition of the figure.
-- name: AvailableCredit :one
SELECT coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = $1), 0)::bigint;

-- A NEGATIVE amount, keyed on the order so a retried checkout debits once.
-- name: SpendCredit :one
SELECT post_store_credit(@user_id, @amount_cents::bigint, @reason::text,
                         @order_id, @idempotency_key::text, NULL);

-- name: CreateInvoicePreference :exec
INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id)
VALUES (@order_id, @invoice_type::text, nullif(@carrier_code::text, ''), nullif(@tax_id::text, ''));

-- The window is decided HERE against the DATABASE's clock: starts_at defaults to
-- its now(), and comparing that to Go's is comparing two clocks. The LIMITS are
-- not: redeem_coupon counts them under a lock on the coupon row.
-- name: CouponByCode :one
SELECT id, code, description, kind, amount_cents, percent_bp,
       min_subtotal_cents, max_discount_cents,
       max_redemptions, per_customer_limit, is_active,
       (starts_at <= now() AND (ends_at IS NULL OR ends_at > now()))::boolean AS is_current
FROM coupons WHERE upper(code) = upper(@code::text);

-- sqlc.narg on the user: redeem_coupon reads NULL as "no per-customer limit"
-- rather than as a customer whose id happens to be zero.
-- name: RedeemCoupon :one
SELECT redeem_coupon(@coupon_id, @order_id, sqlc.narg(user_id)::uuid, @amount_cents::bigint);

-- Enqueue a message in the SAME transaction as the fact it is about. ON CONFLICT
-- DO NOTHING against (topic, dedupe_key), so a retried checkout enqueues once.
-- name: EnqueueMessage :exec
INSERT INTO outbox_messages (topic, dedupe_key, payload)
VALUES (@topic::text, @dedupe_key::text, @payload)
ON CONFLICT (topic, dedupe_key) DO NOTHING;

-- The same enqueue, behind everything transactional. A separate query rather
-- than a parameter, because every other caller is transactional.
-- name: EnqueueBulkMessage :exec
INSERT INTO outbox_messages (topic, dedupe_key, payload, priority)
VALUES (@topic::text, @dedupe_key::text, @payload, @priority)
ON CONFLICT (topic, dedupe_key) DO NOTHING;

-- Checkout idempotency keys past their replay window. Deleting a row frees its
-- key to be replayed, which is why the window is weeks rather than hours.
-- name: DeleteOldCheckoutAttempts :exec
DELETE FROM checkout_attempts
WHERE created_at < now() - sqlc.arg(retain)::interval;

-- Called in the cancellation's own transaction, beside the stock release: the
-- status change is what makes both legal.
-- name: ReverseOrderCredit :one
SELECT reverse_order_credit(@order_id)::bigint AS returned_cents;

-- Does this order number belong to this email address? Both halves in ONE
-- statement answering a boolean: a caller that got a row back could report WHICH
-- half was wrong, and only the address is secret.
-- name: OrderBelongsToEmail :one
SELECT EXISTS (
    SELECT 1 FROM orders o
    JOIN order_private_data pd ON pd.order_id = o.id
    WHERE o.order_number = @order_number::text
      AND pd.erased_at IS NULL
      AND lower(pd.email) = lower(@email::text)
) AS ok;

-- Give a browser access to an order it just placed, or just proved the email for.
-- :execrows, because the INSERT ... SELECT writes NO ROWS when the order number
-- matches nothing and a cookie no grant backs is a silent lockout.
-- name: GrantOrderAccess :execrows
INSERT INTO order_access_grants (digest, order_id)
SELECT @digest, id FROM orders WHERE order_number = @order_number::text
ON CONFLICT (digest) DO NOTHING;

-- The cookie is RE-ISSUED with a fresh MaxAge on every order, carrying older
-- tokens forward, so their grants' retention clock restarts on the same event or
-- one dies under a live cookie. Scoped to the digests actually presented.
-- name: TouchOrderAccessGrants :exec
UPDATE order_access_grants SET created_at = now()
WHERE digest = ANY(@digests::bytea[]);

-- Hold the checkout's idempotency key for the length of this transaction. An
-- advisory lock rather than an early INSERT, whose row would hold the key with
-- order_id still NULL; xact, so it releases on commit or rollback.
-- name: LockCheckoutKey :one
SELECT pg_advisory_xact_lock(hashtextextended(@idempotency_key::text, 0));

-- The order a completed attempt produced.
-- name: OrderNumberByID :one
SELECT order_number FROM orders WHERE id = @id;

-- Drop access grants nobody can present any more: older than the cookie's
-- MaxAge, they are live bearer credentials kept forever for nobody.
-- name: DeleteOldOrderAccessGrants :exec
DELETE FROM order_access_grants WHERE created_at < now() - @retain::interval;

-- Compared IN the database and answered as a boolean: which token matched is not
-- something any page needs to disclose.
-- name: OrderAccessibleWith :one
SELECT EXISTS (
    SELECT 1 FROM order_access_grants g
    JOIN orders o ON o.id = g.order_id
    WHERE o.order_number = @order_number::text AND g.digest = ANY(@digests::bytea[])
);

-- Every Checkout Session this order still has open at Stripe. 'requires_payment'
-- is only ever a HINT: a customer who paid seconds ago still has this row,
-- because only the webhook moves it, and Stripe refuses to expire anything else.
-- name: OpenSessionsForOrder :many
SELECT p.provider_ref
FROM payments p
JOIN orders o ON o.id = p.order_id
WHERE o.order_number = @order_number::text AND p.status = 'requires_payment'
ORDER BY p.created_at;
