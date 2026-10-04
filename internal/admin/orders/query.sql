-- name: AdminOrders :many
SELECT json_build_object('At', o.placed_at, 'ID', o.id)::text AS page_cursor,
    o.id,
    o.order_number,
    o.fulfillment_status,
    o.placed_at,
    o.shipping_cents,
    o.discount_cents,
    o.tax_cents,
    coalesce(pd.recipient_name, '') AS recipient,
    coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
              WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
    order_is_committed(o.id) AS committed,
    order_amount_owed(o.id) AS owed_cents
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE (@status::text = '' OR o.fulfillment_status = @status::text)
-- Pending is two queues: money still owed, and funded and waiting to be picked.
-- FundedStatusLabel draws the same line, so a tab and the row's own label agree.
AND (@funding::text = '' OR (@funding::text = 'funded') = (order_is_committed(o.id) OR order_amount_owed(o.id) <= 0))
AND (NOT @has_cursor::boolean OR (o.placed_at < @after_at::timestamptz)
       OR (o.placed_at = @after_at::timestamptz AND o.id < @after_id::uuid))
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @row_limit::integer;

-- An order-number-shaped term is matched exactly and anything else as a prefix.
-- Each probe reads one table by its own index and the UNION joins the ids back to
-- orders; OR-ing the three across the join could only be a join filter.
-- An erased order matches nothing: erase_user NULLs the name and the address.
-- The prefixes take @escaped_term, the same words with LIKE's own syntax
-- escaped: a typed % or _ would otherwise match any address or name.
-- name: AdminSearchOrders :many
WITH hits AS (
    SELECT o.id FROM orders o WHERE o.order_number = upper(@term::text)
    UNION
    SELECT pd.order_id FROM order_private_data pd
    WHERE lower(pd.email) LIKE lower(@escaped_term::text) || '%'
    UNION
    SELECT pd.order_id FROM order_private_data pd
    WHERE pd.recipient_name LIKE @escaped_term::text || '%'
)
SELECT json_build_object('At', o.placed_at, 'ID', o.id)::text AS page_cursor,
    o.id,
    o.order_number,
    o.fulfillment_status,
    o.placed_at,
    o.shipping_cents,
    o.discount_cents,
    o.tax_cents,
    coalesce(pd.recipient_name, '') AS recipient,
    coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
              WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
    order_is_committed(o.id) AS committed,
    order_amount_owed(o.id) AS owed_cents
FROM orders o
JOIN hits h ON h.id = o.id
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE (NOT @has_cursor::boolean OR (o.placed_at < @after_at::timestamptz)
       OR (o.placed_at = @after_at::timestamptz AND o.id < @after_id::uuid))
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @row_limit::integer;

-- name: AdminOrderCounts :many
SELECT fulfillment_status,
       (fulfillment_status = 'pending' AND (order_is_committed(id) OR order_amount_owed(id) <= 0))::boolean AS funded,
       count(*)::bigint AS n
FROM orders GROUP BY fulfillment_status, funded;

-- discount_reason is JOINED and not snapshotted: coupons.code is never updated
-- and the FK is ON DELETE RESTRICT, so one join always reaches it.
-- name: AdminOrderByNumber :one
SELECT
    o.id, o.order_number, o.fulfillment_status, o.placed_at,
    o.shipping_cents, o.discount_cents, o.tax_cents, o.shipping_method_name,
       coalesce((SELECT c.code || ' · ' || c.description
                 FROM coupon_redemptions cr JOIN coupons c ON c.id = cr.coupon_id
                 WHERE cr.order_id = o.id), '')::text AS discount_reason,
    o.customer_note, o.staff_note,
    coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
              WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
    coalesce(pd.email, '') AS email,
    coalesce(pd.recipient_name, '') AS recipient_name,
    coalesce(pd.phone, '') AS phone,
    coalesce(pd.postal_code, '') AS postal_code,
    coalesce(pd.city, '') AS city,
    coalesce(pd.district, '') AS district,
    coalesce(pd.street, '') AS street,
    coalesce(pd.pickup_chain, '') AS pickup_chain,
    coalesce(pd.pickup_store_code, '') AS pickup_store_code,
    coalesce(pd.pickup_store_name, '') AS pickup_store_name,
    coalesce(ip.invoice_type, '') AS invoice_type,
    coalesce(ip.carrier_code, '') AS invoice_mobile_barcode,
 coalesce(ip.donation_code, '') AS invoice_donation_code,
    coalesce(ip.tax_id, '') AS invoice_tax_id,
    order_is_committed(o.id) AS committed,
    order_amount_owed(o.id) AS owed_cents,
    -- What store credit paid, read as total less what is still owed so
    -- order_amount_owed stays the one definition of that arithmetic.
    (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
               WHERE ol.order_id = o.id), 0)
     - o.discount_cents + o.shipping_cents + o.tax_cents
     - order_amount_owed(o.id))::bigint AS credit_cents,
    (SELECT sm.destination_kind FROM shipping_method_versions v
     JOIN shipping_methods sm ON sm.id = v.method_id
     WHERE v.id = o.shipping_version_id)::text AS destination_kind
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
LEFT JOIN invoice_preferences ip ON ip.order_id = o.id
WHERE o.order_number = $1;

-- Only parcels with no stamp, so a re-run cannot move a recorded date, and never
-- earlier than shipped_at, which order_shipments_delivered_after_shipped refuses.
-- name: MarkShipmentsDelivered :exec
UPDATE order_shipments SET delivered_at = greatest(now(), shipped_at)
WHERE order_id = $1 AND delivered_at IS NULL;

-- Lock before reading the prior state so concurrent completion cannot duplicate arrival mail.
-- name: LockOrderForAdvance :one
SELECT o.id, o.fulfillment_status, order_is_committed(o.id) AS committed,
       order_amount_owed(o.id) AS owed_cents,
       (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                  WHERE ol.order_id = o.id), 0)
        - o.discount_cents + o.shipping_cents + o.tax_cents
        - order_amount_owed(o.id))::bigint AS credit_cents
FROM orders o WHERE o.order_number = $1 FOR UPDATE OF o;

-- orders_check_transition validates the move, so this does not re-derive it.
-- cancelled_at and completed_at are set here because the schema requires them
-- for those two states and orders_history_frozen refuses a later change.
-- name: AdvanceOrder :exec
UPDATE orders
SET fulfillment_status = @status::text,
    cancelled_at = CASE WHEN @status::text = 'cancelled' THEN now() ELSE cancelled_at END,
    completed_at = CASE WHEN @status::text = 'completed' THEN now() ELSE completed_at END
WHERE order_number = @order_number::text;

-- Serialize changes so the audit operation describes the note actually replaced.
-- name: LockOrderForStaffNote :one
SELECT id, staff_note FROM orders WHERE order_number = $1 FOR UPDATE;

-- name: SetStaffNote :exec
UPDATE orders SET staff_note = $2 WHERE order_number = $1;

-- name: AdminSummary :one
SELECT
    -- Genuinely UNPAID, not merely pending: an order funded by store credit or
    -- a full discount sits at pending for good, and counting it here sends
    -- somebody looking for money that has already arrived.
    (SELECT count(*) FROM orders o WHERE o.fulfillment_status = 'pending'
       AND NOT order_is_committed(o.id) AND order_amount_owed(o.id) > 0)::bigint AS pending_orders,
    (SELECT count(*) FROM orders o WHERE o.fulfillment_status = 'pending'
       AND (order_is_committed(o.id) OR order_amount_owed(o.id) <= 0))::bigint AS ready_orders,
    (SELECT count(*) FROM orders WHERE fulfillment_status = 'picking')::bigint AS picking_orders,
    (SELECT count(*) FROM product_variants
     WHERE is_active AND stock_quantity <= safety_stock)::bigint AS low_stock,
    (SELECT count(*) FROM products WHERE status = 'active')::bigint AS active_products,
    (SELECT count(*) FROM contact_messages WHERE handled_at IS NULL)::bigint AS open_messages,
    (SELECT count(*) FROM return_requests WHERE status = 'requested')::bigint AS pending_returns,
    -- Approved, with a parcel to open: a refund before shipment closes its own
    -- lines and never has one.
    (SELECT count(*) FROM return_requests r
     WHERE r.status = 'approved' AND NOT r.before_shipment
       AND EXISTS (SELECT 1 FROM return_request_lines rl
                   WHERE rl.return_request_id = r.id AND rl.received_quantity IS NULL)
    )::bigint AS uninspected_returns,
    -- The queue's own predicate (UnansweredQuestions, Question.Waiting): visible,
    -- and no visible answer from the shop. A customer's reply does not answer it.
    (SELECT count(*) FROM product_questions q
     WHERE q.hidden_at IS NULL
       AND NOT EXISTS (SELECT 1 FROM product_answers a
                       WHERE a.question_id = q.id AND a.is_staff AND a.hidden_at IS NULL)
    )::bigint AS unanswered_questions;

-- name: CreateShipment :one
INSERT INTO order_shipments (order_id, carrier, tracking_number, estimated_delivery_on)
VALUES (@order_id, @carrier::text, @tracking_number::text, @estimated_delivery_on)
RETURNING id;

-- name: ConsumeReservationPartial :exec
SELECT consume_reservation_partial(@reservation_id, @quantity::integer);

-- LEFT JOIN on the reservation, not JOIN: a line whose variant was deleted has
-- no hold, and dropping the row here would ship it without moving stock. The
-- caller refuses instead. ReleaseReservation and HeldReservationsForOrder live
-- in internal/cart/query.sql — sqlc generates ONE db package for the module.
-- name: ShippableLines :many
SELECT ol.id AS order_line_id,
       ol.sku,
       ol.product_name,
       ol.variant_label,
       (ol.quantity - coalesce((
           SELECT sum(sl.quantity) FROM order_shipment_lines sl
           WHERE sl.order_line_id = ol.id), 0))::integer AS remaining,
       ir.id AS reservation_id,
       coalesce(ir.quantity, 0)::integer AS held
FROM order_lines ol
LEFT JOIN inventory_reservations ir
       ON ir.order_id = ol.order_id
      AND ir.variant_id = ol.variant_id
      AND ir.state = 'held'
WHERE ol.order_id = @order_id
  AND ol.quantity > coalesce((
      SELECT sum(sl.quantity) FROM order_shipment_lines sl
      WHERE sl.order_line_id = ol.id), 0)
ORDER BY ol.position, ol.id;

-- order_events is append-only three ways — a forbid_change trigger, REVOKE
-- UPDATE and REVOKE DELETE — so this is the only thing that may touch it.
-- name: RecordOrderEvent :exec
INSERT INTO order_events (order_id, kind, note, actor_user_id)
VALUES (@order_id, @kind::text, @note, @actor_user_id);

-- name: OrderIDByNumber :one
SELECT id, fulfillment_status FROM orders WHERE order_number = $1;

-- name: OrderDispatchDestination :one
SELECT sm.destination_kind, coalesce(pd.pickup_chain, '')::text AS pickup_chain
FROM orders o
JOIN shipping_method_versions v ON v.id = o.shipping_version_id
JOIN shipping_methods sm ON sm.id = v.method_id
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.id = $1;

-- Everything that happened to one order, oldest first, in one statement so the
-- sources share one snapshot and one sort. A provider's event is the order's
-- when its object is one of the order's Checkout Sessions; a mail when its
-- payload names the order, since the shipped mail's dedupe key is the parcel's.
-- A transaction writes a fact and its mail at one now(), so within an instant
-- the provider's notice comes first and the mail last.
-- An order event with no actor: 'placed' and 'cancelled' are the customer's (the
-- sweeper's cancel is by_system); 'paid' is the provider's when a payment
-- succeeded, and otherwise store credit or a discount closing the funding.
-- name: AdminOrderTimeline :many
SELECT at, source, kind, status, note, actor_kind, actor_name
FROM (
    SELECT e.occurred_at AS at, 1 AS precedence, e.id::text AS tie,
           'order'::text AS source, e.kind::text AS kind, ''::text AS status,
           coalesce(e.note, '')::text AS note,
           (CASE WHEN e.actor_user_id IS NOT NULL THEN 'staff'
                 WHEN e.by_system THEN 'system'
                 WHEN e.kind IN ('placed', 'cancelled') THEN 'customer'
                 WHEN e.kind = 'paid' AND EXISTS (
                     SELECT 1 FROM payments p
                     WHERE p.order_id = e.order_id AND p.status = 'succeeded'
                 ) THEN 'provider'
                 ELSE 'system' END)::text AS actor_kind,
           coalesce(u.full_name, u.email, '')::text AS actor_name
    FROM order_events e
    LEFT JOIN users u ON u.id = e.actor_user_id
    WHERE e.order_id = @order_id
    UNION ALL
    -- awaiting_buyer is a sent online allowance, which waits on the buyer's
    -- consent rather than on goen.
    SELECT op.created_at, 2, op.id::text, 'invoice', op.kind,
           CASE WHEN op.kind = 'allowance' AND op.status = 'pending' AND op.send_attempts > 0
                THEN 'awaiting_buyer' ELSE op.status END,
           '', op.actor_kind, coalesce(u.full_name, u.email, '')
    FROM invoice_operations op
    LEFT JOIN users u ON u.id = op.actor_user_id
    WHERE op.order_id = @order_id
    UNION ALL
    SELECT w.received_at, 0, w.event_id, 'provider', '', '',
           w.type || coalesce(' · ' || w.unreconciled, ''), 'provider', ''
    FROM payment_webhook_events w
    JOIN payments p ON p.provider = w.provider AND p.provider_ref = w.object_ref
    WHERE p.order_id = @order_id
    UNION ALL
    SELECT m.created_at, 3, m.id::text, 'mail', m.topic,
           CASE WHEN m.delivered_at IS NULL THEN 'queued' ELSE 'sent' END,
           '', 'system', ''
    FROM outbox_messages m
    JOIN orders o ON o.id = @order_id
    WHERE m.topic = ANY(@mail_topics::text[])
      AND (m.payload->>'order_number' = o.order_number
           OR m.payload->>'order_id' = o.id::text)
) timeline
ORDER BY at, precedence, tie;

-- name: OrderShipments :many
SELECT carrier, tracking_number, shipped_at, delivered_at, estimated_delivery_on
FROM order_shipments WHERE order_id = $1 ORDER BY shipped_at, id;

-- Written with the shipment: a return has to be bounded by what actually went
-- out, not by what was ordered.
-- name: CreateShipmentLine :exec
INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
VALUES (@order_id, @shipment_id, @order_line_id, @quantity::integer);

-- name: OrderCapturedPayment :one
SELECT p.provider, coalesce(p.card_brand, '')::text AS card_brand,
       coalesce(p.card_last4, '')::text AS card_last4,
       coalesce(p.captured_amount_cents, 0)::bigint AS captured_cents,
       p.paid_at
FROM payments p
WHERE p.order_id = @order_id AND p.status = 'succeeded'
ORDER BY p.paid_at DESC
LIMIT 1;

-- Every refund of one order, card and store credit together, oldest first.
-- Credit refunds are the positive store-credit entries, the same definition
-- order_refunds uses. Who: the staff member the audit trail names for a card
-- refund, and the entry's own actor for a credit one; empty when none is known.
-- name: OrderRefundRows :many
SELECT * FROM (
    SELECT 'card'::text AS channel, rf.amount_cents, rf.succeeded_at AS at,
           coalesce(rf.reason, '')::text AS reason,
           coalesce((SELECT coalesce(u.full_name, u.email)
                     FROM audit_events a JOIN users u ON u.id = a.actor_user_id
                     WHERE a.entity_table = 'refunds' AND a.entity_id = rf.id
                     ORDER BY a.occurred_at LIMIT 1), '')::text AS staff
    FROM refunds rf JOIN payments p ON p.id = rf.payment_id
    WHERE p.order_id = @order_id AND rf.status = 'succeeded'
    UNION ALL
    SELECT 'credit'::text, e.amount_cents, e.created_at,
           e.reason::text,
           coalesce((SELECT coalesce(u.full_name, u.email) FROM users u
                     WHERE u.id = e.actor_user_id), '')::text
    FROM store_credit_entries e
    WHERE e.order_id = @order_id AND e.amount_cents > 0
) refunds_of_order
ORDER BY at, channel;

-- The locale comes off the ORDER and never off the staff member who pressed
-- Ship, which would send a Taiwanese shopkeeper's language to an English
-- customer.
-- name: ShipmentRecipient :one
SELECT coalesce(pd.email, '') AS email,
       coalesce(pd.recipient_name, '') AS recipient_name,
       o.locale,
       coalesce((SELECT sm.destination_kind
                 FROM shipping_method_versions v
                 JOIN shipping_methods sm ON sm.id = v.method_id
                 WHERE v.id = o.shipping_version_id), '')::text AS destination_kind
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.id = $1;

-- The state guard is in the WHERE clause and never read first: once an order has
-- shipped, rewriting the address makes the record lie about where it went. Both
-- destination groups are written and exactly one survives; the CALLER decides
-- which half to blank, from the order's own shipping method not from the form.
-- name: UpdateOrderDelivery :execrows
UPDATE order_private_data pd SET
    email = @email,
    recipient_name = @recipient_name,
    phone = @phone,
    postal_code = nullif(@postal_code::text, ''),
    city = nullif(@city::text, ''),
    district = nullif(@district::text, ''),
    street = nullif(@street::text, ''),
    pickup_chain = nullif(@pickup_chain::text, ''),
    pickup_store_code = nullif(@pickup_store_code::text, ''),
    pickup_store_name = nullif(@pickup_store_name::text, '')
FROM orders o
WHERE pd.order_id = o.id
  AND o.order_number = @order_number
  AND pd.erased_at IS NULL
  AND o.fulfillment_status NOT IN ('shipped', 'delivered', 'completed');

-- name: OrderDestinationKind :one
SELECT sm.destination_kind, o.fulfillment_status
FROM orders o
JOIN shipping_method_versions v ON v.id = o.shipping_version_id
JOIN shipping_methods sm ON sm.id = v.method_id
WHERE o.order_number = $1;

-- The order lock also belongs to shipment, cancellation and erasure. Read the
-- destination after acquiring it so a correction cannot outlive that decision.
-- name: LockOrderDelivery :one
SELECT o.id, o.fulfillment_status, sm.destination_kind
FROM orders o
JOIN shipping_method_versions v ON v.id = o.shipping_version_id
JOIN shipping_methods sm ON sm.id = v.method_id
WHERE o.order_number = @order_number
FOR UPDATE OF o;

-- Whether the saved postcode and the proposed one sit in the same surcharge
-- zone. Identity of the zone, not today's amount: a surcharge edited or removed
-- after checkout must not open a cross-zone correction. The order records the
-- postcode it was priced for, so that is the side compared. Two postcodes in no
-- zone are both the mainland; a malformed one resolves to neither.
-- name: DeliveryZoneComparison :one
SELECT (pd.erased_at IS NOT NULL)::boolean AS erased,
       coalesce(pd.postal_code ~ '^[0-9]{3,6}$', false)::boolean AS old_resolved,
       coalesce(pd.postal_code ~ '^[0-9]{3,6}$'
                AND old_zone.zone_id IS NOT DISTINCT FROM new_zone.zone_id, false)::boolean AS same_zone
FROM order_private_data pd
LEFT JOIN shipping_zone_prefixes old_zone ON old_zone.prefix = left(pd.postal_code, 3)
LEFT JOIN shipping_zone_prefixes new_zone ON new_zone.prefix = left(@new_postal_code::text, 3)
WHERE pd.order_id = @order_id;
