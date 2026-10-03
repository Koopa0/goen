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
    -- The queue's own predicate (UnansweredQuestions, Question.Waiting): visible,
    -- and no visible answer from the shop. A customer's reply does not answer it.
    (SELECT count(*) FROM product_questions q
     WHERE q.hidden_at IS NULL
       AND NOT EXISTS (SELECT 1 FROM product_answers a
                       WHERE a.question_id = q.id AND a.is_staff AND a.hidden_at IS NULL)
    )::bigint AS unanswered_questions;

-- When the oldest open return request was filed, which is how long a person has
-- been waiting for a decision. Two columns, not one nullable timestamp: min()
-- over no rows is NULL and sqlc infers the column non-nullable, so pgx cannot
-- scan it.
-- name: OldestPendingReturn :one
SELECT coalesce(min(created_at), now())::timestamptz AS filed_at,
       (count(*) > 0) AS any_open
FROM return_requests
WHERE status = 'requested';

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

-- Oldest first: occurred_at then id, because two events recorded in the same
-- statement share a timestamp and the uuidv7 key is the tie-break.
-- name: OrderEvents :many
SELECT e.kind, e.note, e.occurred_at, coalesce(u.full_name, '') AS actor_name, e.by_system
FROM order_events e
LEFT JOIN users u ON u.id = e.actor_user_id
WHERE e.order_id = $1
ORDER BY e.occurred_at, e.id;

-- name: OrderShipments :many
SELECT carrier, tracking_number, shipped_at, delivered_at, estimated_delivery_on
FROM order_shipments WHERE order_id = $1 ORDER BY shipped_at, id;

-- Written with the shipment: a return has to be bounded by what actually went
-- out, not by what was ordered.
-- name: CreateShipmentLine :exec
INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
VALUES (@order_id, @shipment_id, @order_line_id, @quantity::integer);

-- name: AdminProducts :many
SELECT json_build_object('At', p.updated_at, 'ID', p.id)::text AS page_cursor, p.id, p.slug, p.name, p.status, p.published_at,
       (p.name_en IS NOT NULL)::boolean AS translated,
       b.name AS brand, c.name AS category,
       (SELECT count(*) FROM product_variants pv WHERE pv.product_id = p.id)::integer AS variants,
       (SELECT coalesce(min(pv.price_cents), 0) FROM product_variants pv
        WHERE pv.product_id = p.id AND pv.is_active)::bigint AS from_cents
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN categories c ON c.id = p.category_id
WHERE (NOT @has_cursor::boolean OR (p.updated_at < @after_at::timestamptz)
       OR (p.updated_at = @after_at::timestamptz AND p.id < @after_id::uuid))
ORDER BY p.updated_at DESC, p.id DESC
LIMIT @row_limit::integer;

-- name: AdminProduct :one
SELECT p.id, p.slug, p.name, coalesce(p.summary, '') AS summary, p.description,
       coalesce(p.warranty_months, 0)::integer AS warranty_months,
       coalesce(p.name_en, '') AS name_en,
       coalesce(p.summary_en, '') AS summary_en,
       coalesce(p.description_en, '') AS description_en,
       coalesce(p.warranty_note, '') AS warranty_note, p.status, p.published_at,
       p.brand_id, p.category_id
FROM products p WHERE p.slug = $1;

-- name: AdminProductVariants :many
SELECT id, sku, price_cents, compare_at_price_cents, stock_quantity,
       safety_stock, position, is_active
FROM product_variants WHERE product_id = $1 ORDER BY position, id;

-- name: AdminBrands :many
SELECT id, name FROM brands ORDER BY name;

-- name: AdminCategories :many
WITH RECURSIVE tree AS (
    SELECT id, name, slug, parent_id, 0 AS depth,
           lpad(position::text, 4, '0') || name AS sort
    FROM categories WHERE parent_id IS NULL
    UNION ALL
    SELECT c.id, c.name, c.slug, c.parent_id, t.depth + 1,
           t.sort || '/' || lpad(c.position::text, 4, '0') || c.name
    FROM categories c JOIN tree t ON t.id = c.parent_id
)
SELECT id, name, slug, depth::integer AS depth FROM tree ORDER BY sort;

-- Born a DRAFT: products_active_is_published wants a published_at before a
-- product may go active, and a product with no variants has no price.
-- name: CreateProduct :one
INSERT INTO products (brand_id, category_id, slug, name, summary, description,
                      name_en, summary_en, description_en, warranty_note,
                      warranty_months)
VALUES (@brand_id, @category_id, @slug::text, @name::text,
        nullif(@summary::text, ''), @description::text,
        nullif(@name_en::text, ''), nullif(@summary_en::text, ''),
        nullif(@description_en::text, ''), nullif(@warranty_note::text, ''),
        nullif(@warranty_months::integer, 0))
RETURNING slug;

-- Every nullif('') is what lets a translation be CLEARED; absence is the state
-- the column expresses. warranty_months zero means the shop has stated no term,
-- and registration is then refused rather than given a default. :execrows is
-- part of the write contract: an absent immutable slug must not look like a
-- successful customer-visible edit or acquire an audit row.
-- name: UpdateProduct :execrows
UPDATE products
SET brand_id = @brand_id, category_id = @category_id, name = @name::text,
    summary = nullif(@summary::text, ''), description = @description::text,
    name_en = nullif(@name_en::text, ''),
    summary_en = nullif(@summary_en::text, ''),
    description_en = nullif(@description_en::text, ''),
    warranty_note = nullif(@warranty_note::text, ''),
    warranty_months = nullif(@warranty_months::integer, 0)
WHERE slug = @slug::text;

-- published_at is stamped on the FIRST publish and kept, so re-publishing an old
-- product does not make it new again. :execrows because an UPDATE matching
-- nothing is not an error in SQL: as :exec, a slug that does not exist reports
-- success to the staff member and to the audit trail.
-- name: SetProductStatus :execrows
UPDATE products
SET status = @status::text,
    published_at = CASE
        WHEN @status::text = 'active' THEN coalesce(published_at, now())
        ELSE published_at
    END
WHERE slug = @slug::text;

-- stock_quantity is deliberately absent: it is not in admin's INSERT grant, so
-- it takes DEFAULT 0 and stock arrives only through record_inventory_movement.
-- A zero parcel measurement stores NULL, meaning UNMEASURED.
-- name: CreateVariant :exec
INSERT INTO product_variants (product_id, sku, price_cents, compare_at_price_cents,
                              safety_stock, position, is_active,
                              parcel_longest_mm, parcel_sum_mm, parcel_weight_g)
SELECT p.id, @sku::text, @price_cents::bigint,
       nullif(@compare_at_price_cents::bigint, 0), @safety_stock::integer,
       coalesce((SELECT max(position) + 1 FROM product_variants v WHERE v.product_id = p.id), 0),
       true,
       nullif(@parcel_longest_mm::integer, 0),
       nullif(@parcel_sum_mm::integer, 0),
       nullif(@parcel_weight_g::integer, 0)
FROM products p WHERE p.slug = @slug::text;

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

-- No foreign key on storage_key: product_images predates media_objects and still
-- holds embedded-asset names from the seed, so the column carries two kinds of
-- key.
-- name: AttachProductImage :exec
INSERT INTO product_images (product_id, storage_key, alt_text, alt_text_en,
                            width, height, position, option_value_id)
SELECT p.id, @storage_key::text, @alt_text::text, nullif(@alt_text_en::text, ''),
       @width::integer, @height::integer,
       coalesce((SELECT max(position) + 1 FROM product_images x WHERE x.product_id = p.id), 0),
       sqlc.narg('option_value_id')::uuid
FROM products p
WHERE p.slug = @slug::text;

-- name: SetProductImageOptionValue :execrows
UPDATE product_images
SET option_value_id = sqlc.narg('option_value_id')::uuid
FROM products p
WHERE product_images.product_id = p.id AND p.slug = @slug::text
  AND product_images.storage_key = @storage_key::text;

-- name: DetachProductImage :execrows
DELETE FROM product_images pi
USING products p
WHERE pi.product_id = p.id AND p.slug = @slug::text AND pi.storage_key = @storage_key::text;

-- name: AdminProductImages :many
SELECT pi.storage_key, pi.alt_text, pi.width, pi.height, pi.option_value_id
FROM product_images pi
JOIN products p ON p.id = pi.product_id
WHERE p.slug = @slug::text
ORDER BY pi.position, pi.id;

-- One product's images in display order. Read after LockProductCatalogue, as its
-- own statement: in the same statement the read would use a snapshot taken
-- before that lock was won.
-- name: ProductImageOrder :many
SELECT pi.id, pi.storage_key
FROM product_images pi
JOIN products p ON p.id = pi.product_id
WHERE p.slug = @slug::text
ORDER BY pi.position, pi.id;

-- (product_id, position) is a unique index checked row by row, so a reorder
-- first moves every image clear of the range it is about to fill.
-- name: ParkProductImages :exec
UPDATE product_images pi SET position = pi.position + 1000000
FROM products p
WHERE pi.product_id = p.id AND p.slug = @slug::text;

-- name: SetProductImageOrder :exec
UPDATE product_images pi SET position = o.n::integer - 1
FROM unnest(@ids::uuid[]) WITH ORDINALITY AS o(id, n)
WHERE pi.id = o.id;

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

-- name: AdminProductSpecs :many
SELECT s.id, s.label, s.value,
       coalesce(s.label_en, '') AS label_en, coalesce(s.value_en, '') AS value_en,
       s.position
FROM product_specs s
JOIN products p ON p.id = s.product_id
WHERE p.slug = $1
ORDER BY s.position, s.label;

-- The position is computed IN the insert: product_specs_position_key is unique
-- on (product_id, position), so reading max(position) in Go and then writing it
-- is a race two staff members editing one product would meet.
-- name: AddProductSpec :one
INSERT INTO product_specs (product_id, label, value, label_en, value_en, position)
SELECT p.id, @label::text, @value::text,
       nullif(@label_en::text, ''), nullif(@value_en::text, ''),
       coalesce((SELECT max(sp.position) FROM product_specs sp
                 WHERE sp.product_id = p.id), 0) + 1
FROM products p
WHERE p.slug = @slug::text
RETURNING id;

-- name: RemoveProductSpec :execrows
DELETE FROM product_specs s
USING products p
WHERE p.id = s.product_id AND p.slug = @slug::text AND s.id = @spec_id;

-- name: AdminProductOptions :many
SELECT o.id, o.name, coalesce(o.name_en, '') AS name_en, o.position,
       coalesce(
           (SELECT array_agg(v.id::text ORDER BY v.position, v.id)
            FROM product_option_values v WHERE v.option_id = o.id),
           ARRAY[]::text[]
       )::text[] AS value_ids,
       coalesce(
           (SELECT array_agg(v.value ORDER BY v.position, v.id)
            FROM product_option_values v WHERE v.option_id = o.id),
           ARRAY[]::text[]
       )::text[] AS values,
       coalesce(
           (SELECT array_agg(coalesce(v.value_en, '') ORDER BY v.position, v.id)
            FROM product_option_values v WHERE v.option_id = o.id),
           ARRAY[]::text[]
       )::text[] AS value_labels,
       coalesce(
           (SELECT array_agg(coalesce(v.swatch_hex, '') ORDER BY v.position, v.id)
            FROM product_option_values v WHERE v.option_id = o.id),
           ARRAY[]::text[]
       )::text[] AS swatch_hexes
FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = $1
ORDER BY o.position, o.id;

-- name: AddProductOption :one
INSERT INTO product_options (product_id, name, name_en, position)
SELECT p.id, @name::text, nullif(@name_en::text, ''),
       coalesce((SELECT max(o.position) FROM product_options o
                 WHERE o.product_id = p.id), 0) + 1
FROM products p
WHERE p.slug = @slug::text
RETURNING id;

-- product_id comes from the OPTION and not from the caller, so a value cannot be
-- attached to an option of a different product: the composite foreign key would
-- refuse it, and resolving it here means the caller cannot try.
-- name: AddProductOptionValue :one
INSERT INTO product_option_values (product_id, option_id, value, value_en, swatch_hex, position)
SELECT o.product_id, o.id, @value::text, nullif(@value_en::text, ''),
       nullif(@swatch_hex::text, ''),
       coalesce((SELECT max(v.position) FROM product_option_values v
                 WHERE v.option_id = o.id), 0) + 1
FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = @slug::text AND o.id = @option_id
RETURNING id;

-- Lock before reading options so a concurrently added axis participates in validation.
-- name: LockProductCatalogue :one
SELECT id FROM products WHERE slug = $1 FOR NO KEY UPDATE;

-- name: ProductOptionCount :one
SELECT count(*)::bigint FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = $1;

-- A variant of the product already carries every one of the chosen values. The
-- storefront sells a selection only when exactly one variant matches it, so a
-- second variant on the same combination leaves neither of them buyable.
-- name: VariantCombinationTaken :one
SELECT EXISTS (
    SELECT 1
    FROM variant_option_values vov
    JOIN products p ON p.id = vov.product_id
    WHERE p.slug = @slug::text
      AND vov.option_value_id = ANY(@option_value_ids::uuid[])
    GROUP BY vov.variant_id
    HAVING count(*) = cardinality(@option_value_ids::uuid[])
);

-- Every id is resolved inside the statement, so nothing crosses products:
-- variant_option_values carries product_id precisely so the composite keys can
-- refuse a variant of A paired with a value of B.
-- name: SetVariantOptionValue :execrows
INSERT INTO variant_option_values (product_id, variant_id, option_id, option_value_id)
SELECT pv.product_id, pv.id, v.option_id, v.id
FROM product_variants pv
JOIN product_option_values v
  ON v.product_id = pv.product_id AND v.id = @option_value_id
WHERE pv.sku = @sku::text;

-- name: AdminVariantOptionValues :many
SELECT pv.sku, o.name AS option_name, v.value
FROM variant_option_values vov
JOIN product_variants pv ON pv.id = vov.variant_id
JOIN product_options o ON o.id = vov.option_id
JOIN product_option_values v ON v.id = vov.option_value_id
JOIN products p ON p.id = pv.product_id
WHERE p.slug = $1
ORDER BY pv.sku, o.position, o.id;

-- Staff have verified at Stripe that this provider-complete Session was paid.
-- The SECURITY DEFINER function accepts no amount from the operator: it posts
-- the payment row's immutable intent through capture_payment and returns the
-- order facts needed for payment-owned side effects in this admin tx.
-- name: AttributeCompletePaymentPaid :one
WITH attributed AS MATERIALIZED (
    SELECT attribute_complete_payment_paid(@provider_ref::text) AS payment_id
)
SELECT p.order_id, o.order_number, p.intended_amount_cents AS amount_cents
FROM attributed a
JOIN payments p ON p.id = a.payment_id
JOIN orders o ON o.id = p.order_id;

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
