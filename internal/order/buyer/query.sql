-- What an order's customer may ask to return: what SHIPPED, minus what an
-- unrejected request already claims. return_within_shipment enforces the same.
-- An unsent refund claims units that never shipped, so it does not use up the
-- ones that did: return_within_shipment counts each kind against its own.
-- name: ReturnableLines :many
SELECT ol.id,
       ol.sku,
       ol.product_name,
       ol.variant_label,
       ol.unit_price_cents,
       (coalesce((SELECT sum(sl.quantity) FROM order_shipment_lines sl
                  WHERE sl.order_line_id = ol.id), 0)
        - coalesce((SELECT sum(rl.quantity) FROM return_request_lines rl
                    JOIN return_requests r ON r.id = rl.return_request_id
                    WHERE rl.order_line_id = ol.id AND r.status <> 'rejected'
                      AND NOT r.unsent), 0)
       )::integer AS returnable
FROM order_lines ol
WHERE ol.order_id = $1
ORDER BY ol.position, ol.id;

-- The order a return is being asked for.
-- name: OrderForReturn :one
SELECT o.id, o.order_number, o.fulfillment_status, coalesce(pd.pickup_chain, '') <> '' AS pickup
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.order_number = $1;

-- name: CreateReturnRequest :one
INSERT INTO return_requests (order_id, requested_by_user_id, reason)
VALUES (@order_id, @requested_by_user_id, @reason::text)
RETURNING id;

-- name: CreateReturnRequestLine :exec
INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
VALUES (@order_id, @return_request_id, @order_line_id, @quantity::integer);

-- An order's return requests, for the customer's own page. No actor: the page is
-- reachable by anyone holding the number, so it must not name staff. A refund
-- before shipment and a refund of units that were never sent are not requests
-- the customer made; the order page's refunded entry already tells them the
-- money is back.
-- name: ReturnsForOrder :many
SELECT r.id, r.status, r.reason, r.resolution, r.created_at, r.decided_at
FROM return_requests r WHERE r.order_id = $1 AND NOT r.before_shipment AND NOT r.unsent
ORDER BY r.created_at DESC, r.id;

-- Whether this order already has a request nobody has decided yet.
-- name: HasOpenReturn :one
SELECT EXISTS (
    SELECT 1 FROM return_requests WHERE order_id = $1 AND status = 'requested'
);

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

-- name: OrderSummaryByNumber :one
SELECT o.id, o.order_number, o.fulfillment_status,
       o.shipping_cents, o.discount_cents,
       -- WHICH discount, joined rather than snapshotted: coupons.code is never
       -- updated and the FK is ON DELETE RESTRICT, so one join always reaches it,
       -- and coupon_redemptions_order_key keeps it to one row. discount_reason is
       -- the line the order email prints; the pages name the coupon from its parts.
       coalesce(cp.code || ' · ' || cp.description, '')::text AS discount_reason,
       coalesce(cp.code, '')::text AS coupon_code,
       coalesce(cp.description, '')::text AS coupon_description,
       coalesce(cp.kind, '')::text AS coupon_kind,
       -- The version the order was priced from, which is append-only, so an
       -- English name is read without rewriting what the order chose.
       localized_name(sv.name, sv.name_en, @locale::text) AS shipping_method_name,
       o.placed_at,
       t.subtotal_cents, t.total_cents,
       -- What store credit paid, as the difference between the total and what is
       -- still owed rather than a second sum over the ledger: order_amount_after_credit
       -- is the one definition of that arithmetic, and TestEveryCreditBalanceReadsTheOneView
       -- refuses a page that re-derives it.
       (t.total_cents - order_amount_after_credit(o.id))::bigint AS credit_cents,
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
JOIN order_totals t ON t.order_id = o.id
JOIN shipping_method_versions sv ON sv.id = o.shipping_version_id
LEFT JOIN order_private_data pd ON pd.order_id = o.id
LEFT JOIN coupon_redemptions cr ON cr.order_id = o.id
LEFT JOIN coupons cp ON cp.id = cr.coupon_id
WHERE o.order_number = @number;

-- The customer's order page: each line with its photograph and its warranty promise.
-- name: OrderPageLines :many
SELECT ol.id, ol.sku, ol.product_name, ol.variant_label, ol.unit_price_cents, ol.quantity,
       ol.warranty_months,
       coalesce(img.media_digest, '') AS image_digest,
       coalesce(img.bundled_key, '') AS image_bundled_key,
       coalesce(localized_name(img.alt_text, img.alt_text_en, @locale::text), '')::text AS image_alt,
       coalesce(img.width, 0)::integer AS image_width
FROM order_lines ol
LEFT JOIN LATERAL (
    -- LIMIT 1 keeps the subquery unflattened, so the view is read for this line only.
    SELECT v.media_digest, v.bundled_key, v.alt_text, v.alt_text_en, v.width
    FROM variant_primary_image v
    WHERE v.product_id = ol.product_id AND v.variant_id IS NOT DISTINCT FROM ol.variant_id
    LIMIT 1
) img ON true
WHERE ol.order_id = @order_id ORDER BY ol.position, ol.id;

-- How many units of each line an unsent refund claims. They will not be sent, so they are no longer waiting for a
-- parcel; shipment_lines_within_purchase refuses one that carries them.
-- name: OrderUnsentUnits :many
SELECT rl.order_line_id, sum(rl.quantity)::integer AS quantity
FROM return_request_lines rl
JOIN return_requests r ON r.id = rl.return_request_id
WHERE r.order_id = $1 AND r.unsent AND r.status <> 'rejected'
GROUP BY rl.order_line_id;

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

-- The customer sees WHAT happened, never WHO did it; the back office reads the
-- same table with the actor joined. A refund's note is the provider's refund
-- id, which the back office needs and the shopper has no use for. unsent marks
-- the refund of units the shop could not send (open_unsent_refund), which the
-- page names as such.
-- name: OrderTimeline :many
SELECT e.kind,
       (CASE WHEN e.kind = 'refunded' THEN '' ELSE coalesce(e.note, '') END)::text AS note,
       e.occurred_at,
       EXISTS (SELECT 1 FROM return_requests r
               WHERE r.id = e.return_request_id AND r.unsent)::boolean AS unsent
FROM order_events e WHERE e.order_id = $1 ORDER BY e.occurred_at, e.id;

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
-- goodwill_ends is the day return_line_policy_window stops reading 'goodwill'. rescission_days is
-- what a parcel not yet delivered states in place of its dates.
-- name: OrderTracking :many
SELECT id, carrier, tracking_number, shipped_at, delivered_at,
       coalesce(return_window_ends(delivered_at), shop_today())::date AS rescission_ends,
       coalesce(return_goodwill_ends(delivered_at), shop_today())::date AS goodwill_ends,
       rescission_days()::bigint AS rescission_days
FROM order_shipments WHERE order_id = $1 ORDER BY shipped_at, id;

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
