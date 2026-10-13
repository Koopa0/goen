-- name: CartByToken :one
SELECT id, user_id FROM carts WHERE token_hash = $1;

-- name: CreateCart :one
INSERT INTO carts (token_hash, user_id) VALUES ($1, $2) RETURNING id;

-- Serialize every writer of a cart aggregate before it touches cart_items.
-- Sorting makes two-cart operations such as account adoption use one lock order.
-- name: LockCarts :many
SELECT id FROM carts
WHERE id = ANY(@cart_ids::uuid[])
ORDER BY id
FOR UPDATE;

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

-- The caller has already clamped the line's total against sellable stock and
-- the line ceiling, so a conflict stores the total it was handed. It overwrites
-- rather than adds: two unlocked callers would each write a total computed from
-- the same stale read, so every caller must hold the cart row lock that
-- lockCart takes in mutateCart.
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

-- name: CartLineQuantity :one
SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2;

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
    coalesce(img.storage_key, '') AS image_key,
    coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
    coalesce(img.width, 0)::integer AS image_width
FROM cart_items ci
JOIN product_variants pv ON pv.id = ci.variant_id
JOIN products p ON p.id = pv.product_id
LEFT JOIN brands b ON b.id = p.brand_id
LEFT JOIN LATERAL (
    -- The line's own photograph when one shows its option value, else the
    -- product's first.
    SELECT i.storage_key, i.alt_text, i.alt_text_en, i.width FROM product_images i
    WHERE i.product_id = p.id
    ORDER BY EXISTS (
                 SELECT 1 FROM variant_option_values vov
                 WHERE vov.variant_id = pv.id AND vov.option_value_id = i.option_value_id
             ) DESC,
             i.position
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

-- name: OrderSummaryByNumber :one
SELECT o.id, o.order_number, o.fulfillment_status,
       o.shipping_cents, o.discount_cents, o.tax_cents,
       -- WHICH discount, joined rather than snapshotted: coupons.code is never
       -- updated and the FK is ON DELETE RESTRICT, so one join always reaches it.
       coalesce((SELECT c.code || ' · ' || c.description
                 FROM coupon_redemptions cr JOIN coupons c ON c.id = cr.coupon_id
                 WHERE cr.order_id = o.id), '')::text AS discount_reason,
       -- The version the order was priced from, which is append-only, so an
       -- English name is read without rewriting what the order chose.
       localized_name(sv.name, sv.name_en, @locale::text) AS shipping_method_name,
       o.placed_at,
       coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                 WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
       -- What store credit paid, as the difference between the total and what is
       -- still owed rather than a second sum over the ledger: order_amount_after_credit
       -- is the one definition of that arithmetic, and TestEveryCreditBalanceReadsTheOneView
       -- refuses a page that re-derives it.
       (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                  WHERE ol.order_id = o.id), 0)
        - o.discount_cents + o.shipping_cents + o.tax_cents
        - order_amount_after_credit(o.id))::bigint AS credit_cents,
       coalesce(pd.email, '') AS email,
       coalesce(pd.postal_code, '') AS postal_code,
       coalesce(pd.city, '') AS city,
       coalesce(pd.district, '') AS district,
       coalesce(pd.street, '') AS street,
       coalesce(pd.pickup_chain, '') AS pickup_chain,
       coalesce(pd.pickup_store_code, '') AS pickup_store_code,
       coalesce(pd.pickup_store_name, '') AS pickup_store_name,
       -- 'pending' does NOT mean unpaid: a webhook can capture minutes before
       -- the shop moves the order to picking.
       EXISTS (SELECT 1 FROM committed_orders c WHERE c.id = o.id) AS committed,
       -- NOT derivable from `committed`: a fully store-credited order has no
       -- payment row and stays 'pending' while the customer owes nothing.
       order_amount_after_credit(o.id)::bigint AS owed_cents
FROM orders o
JOIN shipping_method_versions sv ON sv.id = o.shipping_version_id
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.order_number = @number;

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

-- The customer's order page: each line with its photograph and its warranty promise.
-- name: OrderPageLines :many
SELECT ol.id, ol.sku, ol.product_name, ol.variant_label, ol.unit_price_cents, ol.quantity,
       ol.warranty_months,
       coalesce(img.storage_key, '') AS image_key,
       coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
       coalesce(img.width, 0)::integer AS image_width
FROM order_lines ol
LEFT JOIN LATERAL (
    -- The photograph that shows the line's own option value, else the product's first.
    SELECT i.storage_key, i.alt_text, i.alt_text_en, i.width FROM product_images i
    WHERE i.product_id = ol.product_id
    ORDER BY EXISTS (
                 SELECT 1 FROM variant_option_values vov
                 WHERE vov.variant_id = ol.variant_id AND vov.option_value_id = i.option_value_id
             ) DESC,
             i.position
    LIMIT 1
) img ON true
WHERE ol.order_id = @order_id ORDER BY ol.position, ol.id;

-- Which lines, and how many of each, went in which parcel, in the order OrderTracking lists the parcels.
-- name: OrderParcelLines :many
SELECT sl.shipment_id, sl.order_line_id, sl.quantity
FROM order_shipment_lines sl
JOIN order_shipments s ON s.id = sl.shipment_id
WHERE sl.order_id = $1
ORDER BY s.shipped_at, s.id, sl.order_line_id;

-- name: OrderWarrantyRegistrations :many
SELECT w.order_line_id, w.unit_no, w.expires_on
FROM warranty_registrations w
JOIN order_lines ol ON ol.id = w.order_line_id
WHERE ol.order_id = $1
ORDER BY w.order_line_id, w.unit_no;

-- The orders among @order_ids whose every unit is in a return whose refund has settled; a refund before shipment
-- returns nothing. Approval only starts the payout: a card refund can fail or wait and a credit posting can fail,
-- leaving the return approved with the money not sent. So a return counts once it is completed, or approved with
-- nothing to send back, or approved with its refunded event written, which the payout writes only after every
-- source has landed. The completed-status trigger holds the same definition of settled.
-- name: ReturnedOrders :many
SELECT o.id
FROM orders o
WHERE o.id = ANY(@order_ids::uuid[])
  AND EXISTS (SELECT 1 FROM order_lines ol WHERE ol.order_id = o.id)
  AND NOT EXISTS (
      SELECT 1 FROM order_lines ol
      WHERE ol.order_id = o.id
        AND ol.quantity > coalesce((SELECT sum(rl.quantity) FROM return_request_lines rl
                                    JOIN return_requests rr ON rr.id = rl.return_request_id
                                    WHERE rl.order_line_id = ol.id
                                      AND NOT rr.before_shipment
                                      AND (rr.status = 'completed'
                                           OR (rr.status = 'approved'
                                               AND (rr.goods_refund_cents + rr.shipping_refund_cents = 0
                                                    OR EXISTS (SELECT 1 FROM order_events e
                                                               WHERE e.return_request_id = rr.id
                                                                 AND e.kind = 'refunded'))))), 0));

-- The returns whose refund has settled, as ReturnedOrders counts them, with the money each sent back and the
-- day it was refunded: the refunded event the payout wrote once every source landed, or the decision for a
-- return that sent nothing back.
-- name: OrderReturns :many
SELECT coalesce((SELECT e.occurred_at FROM order_events e
                 WHERE e.return_request_id = rr.id AND e.kind = 'refunded'),
                rr.decided_at)::timestamptz AS refunded_at,
       (rr.goods_refund_cents + rr.shipping_refund_cents)::bigint AS refund_cents
FROM return_requests rr
WHERE rr.order_id = $1
  AND NOT rr.before_shipment
  AND (rr.status = 'completed'
       OR (rr.status = 'approved'
           AND (rr.goods_refund_cents + rr.shipping_refund_cents = 0
                OR EXISTS (SELECT 1 FROM order_events e
                           WHERE e.return_request_id = rr.id AND e.kind = 'refunded'))))
ORDER BY refunded_at, rr.id;

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

-- The customer sees WHAT happened, never WHO did it; the back office reads the
-- same table with the actor joined. A refund's note is the provider's refund
-- id, which the back office needs and the shopper has no use for.
-- name: OrderTimeline :many
SELECT kind,
       (CASE WHEN kind = 'refunded' THEN '' ELSE coalesce(note, '') END)::text AS note,
       occurred_at
FROM order_events WHERE order_id = $1 ORDER BY occurred_at, id;

-- What the customer may read of the order's filed invoice: nothing exists
-- before issue, so an order with no rows shows no panel.
-- name: OrderInvoiceDocuments :many
SELECT kind, number, amount_cents, status,
       coalesce(provider_ref, '')::text AS provider_ref, issued_at
FROM invoice_documents WHERE order_id = $1 ORDER BY issued_at, id;

-- name: OrderInvoicePreference :one
SELECT invoice_type, coalesce(carrier_code, '')::text AS carrier_code,
       coalesce(donation_code, '')::text AS donation_code,
       coalesce(tax_id, '')::text AS tax_id
FROM invoice_preferences WHERE order_id = $1;

-- rescission_ends and goodwill_ends are shop_today() for a parcel not yet delivered: sqlc cannot
-- type a nullable date from an expression, so a reader checks delivered_at, never the dates.
-- goodwill_ends is the day return_line_policy_window stops reading 'goodwill'; TestTheParcelCarriesTheDatabasesLastDays
-- holds the 14 to that function.
-- name: OrderTracking :many
SELECT id, carrier, tracking_number, shipped_at, delivered_at,
       coalesce(return_window_ends(delivered_at), shop_today())::date AS rescission_ends,
       coalesce(shop_day(delivered_at) + 14, shop_today())::date AS goodwill_ends
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
  AND (o.fulfillment_status = 'cancelled' OR order_amount_after_credit(ir.order_id) <> 0)
  -- A complete Session / verified capture awaiting a human outcome may already
  -- hold money. Keep its goods pinned until paid attribution commits the order,
  -- or an explicit refund/unpaid resolution releases the payment gate.
  AND (o.fulfillment_status = 'cancelled' OR (
      NOT EXISTS (
          SELECT 1 FROM payments p
          WHERE p.order_id = ir.order_id
            AND p.status = 'requires_reconciliation'
      )
      AND NOT EXISTS (
          SELECT 1
          FROM payment_webhook_events e
          JOIN payments p
            ON p.provider = e.provider AND p.provider_ref = e.object_ref
          WHERE p.order_id = ir.order_id
            AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
      )
  ))
ORDER BY ir.expires_at
LIMIT $1;

-- name: ReleaseReservation :exec
SELECT release_reservation($1);

-- The actor is the staff member who cancelled; none for the customer's own
-- cancellation and, with by_system, the sweeper's at the payment deadline. Both
-- are carried structurally because the customer's own order page renders any
-- note in whatever language it was written.
-- name: RecordCancellation :exec
INSERT INTO order_events (order_id, kind, actor_user_id, by_system)
VALUES (@order_id, 'cancelled', @actor_user_id, @by_system::boolean);

-- What a cancellation notice needs to decide whether money may have reached
-- Stripe for this unpaid order: the status of every payment it has had, and
-- whether any of their provider events ever needed a person.
-- name: OrderPaymentFacts :one
SELECT ARRAY(
           SELECT DISTINCT p.status FROM payments p WHERE p.order_id = $1 ORDER BY p.status
       )::text[] AS statuses,
       EXISTS (
           SELECT 1
           FROM payments p
           JOIN payment_webhook_events e
             ON e.provider = p.provider AND e.object_ref = p.provider_ref
           WHERE p.order_id = $1 AND e.unreconciled IS NOT NULL
       )::boolean AS provider_flagged;

-- Ordered by variant first so cancellation shares the global stock-root lock
-- order with checkout and returns; id is the stable tie-breaker.
-- name: HeldReservationsForOrder :many
SELECT r.id FROM inventory_reservations r
JOIN orders o ON o.id = r.order_id
WHERE o.order_number = $1 AND r.state = 'held'
ORDER BY r.variant_id, r.id;

-- Whether store credit alone paid the order, read before the cancellation
-- returns the credit: checkout queued its 統一發票 then. Only an uncommitted
-- order is cancelled this way, which no card has paid, so owing nothing after a
-- credit spend means credit paid it. Read under the order lock.
-- name: PaidByCreditAlone :one
SELECT coalesce(order_amount_after_credit(o.id) = 0
                AND EXISTS (SELECT 1 FROM store_credit_entries s
                            WHERE s.order_id = o.id AND s.amount_cents < 0),
                false)::boolean AS paid_by_credit
FROM orders o
WHERE o.id = $1;

-- Pending refuses a second cancellation; committed and unresolved payment
-- facts protect stock when money has arrived or may still arrive. Run it after
-- LockOrderByNumber: a capture holds the order lock without updating the row,
-- so an UPDATE that waited for it would judge payment by the snapshot taken
-- before the wait and reach the transition trigger, which store may not run.
-- name: CancelOrderByCustomer :execrows
UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
WHERE orders.id = $1
  AND orders.fulfillment_status = 'pending'
  AND NOT order_is_committed(orders.id)
  AND NOT EXISTS (
      SELECT 1 FROM payments p
      WHERE p.order_id = orders.id
        AND p.status IN ('requires_payment', 'requires_action', 'processing',
                         'requires_reconciliation')
  )
  AND NOT EXISTS (
      SELECT 1
      FROM payment_webhook_events e
      JOIN payments p
        ON p.provider = e.provider AND p.provider_ref = e.object_ref
      WHERE p.order_id = orders.id
        AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
  );

-- Unpaid orders none of whose holds is live any more. A Checkout Session must
-- end before the order's hold, so such an order can never be paid. The rest of
-- the predicate keeps any order that has money or may still take some: zero-owed
-- is paid in full while pending, and a live session, a provider-complete session
-- awaiting its webhook, or an unresolved provider event may already hold money.
-- CancelLapsedOrder repeats this predicate under the order lock.
-- name: LapsedUnpaidOrders :many
SELECT o.order_number
FROM orders o
WHERE o.fulfillment_status = 'pending'
  AND NOT order_is_committed(o.id)
  AND order_amount_after_credit(o.id) <> 0
  AND EXISTS (SELECT 1 FROM inventory_reservations ir WHERE ir.order_id = o.id)
  AND NOT EXISTS (
      SELECT 1 FROM inventory_reservations ir
      WHERE ir.order_id = o.id AND ir.state = 'held' AND ir.expires_at >= now()
  )
  AND NOT EXISTS (
      SELECT 1 FROM payments p
      WHERE p.order_id = o.id
        AND p.status IN ('requires_payment', 'requires_action', 'processing',
                         'requires_reconciliation')
  )
  AND NOT EXISTS (
      SELECT 1
      FROM payment_webhook_events e
      JOIN payments p
        ON p.provider = e.provider AND p.provider_ref = e.object_ref
      WHERE p.order_id = o.id
        AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
  )
ORDER BY o.placed_at
LIMIT $1;

-- Its own statement, before CancelLapsedOrder and CancelOrderByCustomer: open_payment and capture_payment
-- lock this row without updating it, so an UPDATE that waited for them would
-- still judge their payments by the snapshot it took before waiting.
-- name: LockOrderByNumber :one
SELECT id FROM orders WHERE order_number = $1 FOR UPDATE;

-- LapsedUnpaidOrders' predicate, read again after LockOrderByNumber: `pending`
-- refuses a second cancellation, and the payment clauses now see every payment
-- committed before the lock was granted.
-- name: CancelLapsedOrder :execrows
UPDATE orders o SET fulfillment_status = 'cancelled', cancelled_at = now()
WHERE o.id = $1
  AND o.fulfillment_status = 'pending'
  AND NOT order_is_committed(o.id)
  AND order_amount_after_credit(o.id) <> 0
  AND EXISTS (SELECT 1 FROM inventory_reservations ir WHERE ir.order_id = o.id)
  AND NOT EXISTS (
      SELECT 1 FROM inventory_reservations ir
      WHERE ir.order_id = o.id AND ir.state = 'held' AND ir.expires_at >= now()
  )
  AND NOT EXISTS (
      SELECT 1 FROM payments p
      WHERE p.order_id = o.id
        AND p.status IN ('requires_payment', 'requires_action', 'processing',
                         'requires_reconciliation')
  )
  AND NOT EXISTS (
      SELECT 1
      FROM payment_webhook_events e
      JOIN payments p
        ON p.provider = e.provider AND p.provider_ref = e.object_ref
      WHERE p.order_id = o.id
        AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
  );

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

-- name: LockCouponForCheckout :exec
SELECT lock_coupon_for_checkout(@code::text);

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

-- Many messages of one topic in ONE statement. A restock or a newsletter fans out
-- to every subscriber, and a statement each holds the caller's transaction, and
-- the row locks it took, open for a round trip per recipient. The payloads are
-- JSON text, paired with their dedupe keys by position. A bulk send passes
-- the priority that lets transactional mail go first.
-- name: EnqueueMessages :exec
INSERT INTO outbox_messages (topic, dedupe_key, payload, priority)
SELECT @topic::text, t.dedupe_key, t.payload::jsonb, @priority::smallint
FROM (SELECT unnest(@dedupe_keys::text[]) AS dedupe_key,
             unnest(@payloads::text[]) AS payload) t
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

-- name: ReverseOrderPoints :one
SELECT reverse_order_points(@order_id)::bigint AS points_reversed;

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

-- Hold the checkout's idempotency key for the length of this transaction. An
-- advisory lock rather than an early INSERT, whose row would hold the key with
-- order_id still NULL; xact, so it releases on commit or rollback.
-- name: LockCheckoutKey :exec
SELECT pg_advisory_xact_lock(hashtextextended(@idempotency_key::text, 0));

-- The order a completed attempt produced.
-- name: OrderNumberByID :one
SELECT order_number FROM orders WHERE id = @id;

-- Every Checkout Session this order still has open at Stripe. 'requires_payment'
-- is only ever a HINT: a customer who paid seconds ago still has this row,
-- because only the webhook moves it, and Stripe refuses to expire anything else.
-- name: OpenSessionsForOrder :many
SELECT p.provider_ref
FROM payments p
JOIN orders o ON o.id = p.order_id
WHERE o.order_number = @order_number::text AND p.status = 'requires_payment'
ORDER BY p.created_at;
