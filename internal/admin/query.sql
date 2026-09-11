-- name: AdminOrders :many
SELECT
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
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @row_limit::integer;

-- An order-number-shaped term is matched exactly and anything else as a prefix,
-- told apart rather than OR-ed with wildcards so each path stays index-backed.
-- An erased order matches nothing: erase_user NULLs the name and the address.
-- name: AdminSearchOrders :many
SELECT
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
WHERE o.order_number = upper(@term::text)
   OR lower(pd.email) LIKE lower(@term::text) || '%'
   OR pd.recipient_name LIKE @term::text || '%'
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @row_limit::integer;

-- name: AdminOrderCounts :many
SELECT fulfillment_status, count(*)::bigint AS n
FROM orders GROUP BY fulfillment_status;

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
    coalesce(pd.pickup_brand, '') AS pickup_brand,
    coalesce(pd.pickup_store_code, '') AS pickup_store_code,
    coalesce(pd.pickup_store_name, '') AS pickup_store_name,
    coalesce(ip.invoice_type, '') AS invoice_type,
    coalesce(ip.carrier_code, '') AS invoice_carrier,
    coalesce(ip.tax_id, '') AS invoice_tax_id,
    order_is_committed(o.id) AS committed,
    order_amount_owed(o.id) AS owed_cents
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
LEFT JOIN invoice_preferences ip ON ip.order_id = o.id
WHERE o.order_number = $1;

-- Only parcels with no stamp, so a re-run cannot move a recorded date, and never
-- earlier than shipped_at, which order_shipments_delivered_after_shipped refuses.
-- name: MarkShipmentsDelivered :exec
UPDATE order_shipments SET delivered_at = greatest(now(), shipped_at)
WHERE order_id = $1 AND delivered_at IS NULL;

-- orders_check_transition validates the move, so this does not re-derive it.
-- cancelled_at and completed_at are set here because the schema requires them
-- for those two states and orders_history_frozen refuses a later change.
-- name: AdvanceOrder :exec
UPDATE orders
SET fulfillment_status = @status::text,
    cancelled_at = CASE WHEN @status::text = 'cancelled' THEN now() ELSE cancelled_at END,
    completed_at = CASE WHEN @status::text = 'completed' THEN now() ELSE completed_at END
WHERE order_number = @order_number::text;

-- name: SetStaffNote :exec
UPDATE orders SET staff_note = $2 WHERE order_number = $1;

-- name: AdminVariants :many
SELECT
    pv.id,
    pv.sku,
    pv.price_cents,
    pv.compare_at_price_cents,
    pv.stock_quantity,
    pv.safety_stock,
    pv.is_active,
    p.slug,
    p.name AS product_name,
    p.status AS product_status,
    b.name AS brand
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
JOIN brands b ON b.id = p.brand_id
WHERE (@low_only::boolean = false OR pv.stock_quantity <= pv.safety_stock)
ORDER BY (pv.stock_quantity - pv.safety_stock), p.name, pv.position
LIMIT @row_limit::integer;

-- price_cents is read for the audit trail's "before": a reprice recorded without
-- the price it replaced records the least interesting half of the fact.
-- name: AdminVariantBySKU :one
SELECT pv.id, pv.sku, pv.stock_quantity, pv.safety_stock, pv.is_active,
       pv.price_cents, p.name AS product_name, p.slug
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.sku = $1;

-- record_inventory_movement is the ONLY door: admin has no UPDATE on
-- stock_quantity, so a direct write is refused by the database.
-- name: AdjustStock :exec
SELECT record_inventory_movement(
    @variant_id, @delta::integer, 'adjustment',
    @idempotency_key::text, 'admin', NULL, @actor_user_id::uuid
);

-- sqlc.narg on the actor: actor_user_id is nullable with a foreign key, so a
-- zero UUID is not "nobody" — it is an id that does not exist, and the FK
-- refuses it.
-- name: RestockReturnedUnits :exec
SELECT record_inventory_movement(
    @variant_id, @delta::integer, 'return',
    @idempotency_key::text, 'return_request', @request_id, sqlc.narg(actor_user_id)::uuid
);

-- name: SetVariantActive :exec
UPDATE product_variants SET is_active = $2 WHERE id = $1;

-- name: SetVariantPrice :exec
UPDATE product_variants
SET price_cents = @price_cents, compare_at_price_cents = @compare_at_price_cents
WHERE id = @id;

-- name: AdminSummary :one
SELECT
    -- Genuinely UNPAID, not merely pending: an order funded by store credit or
    -- a full discount sits at pending for good, and counting it here sends
    -- somebody looking for money that has already arrived.
    (SELECT count(*) FROM orders o WHERE o.fulfillment_status = 'pending'
       AND NOT order_is_committed(o.id) AND order_amount_owed(o.id) > 0)::bigint AS pending_orders,
    (SELECT count(*) FROM orders WHERE fulfillment_status = 'picking')::bigint AS picking_orders,
    (SELECT count(*) FROM product_variants
     WHERE is_active AND stock_quantity <= safety_stock)::bigint AS low_stock,
    (SELECT count(*) FROM products WHERE status = 'active')::bigint AS active_products,
    (SELECT count(*) FROM contact_messages WHERE handled_at IS NULL)::bigint AS open_messages;

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

-- A payout source commits before this customer-visible append. Keying the row
-- on the return makes a retry safe after a transient database/context failure.
-- The WHERE is a second authority: no caller can announce money which neither
-- the provider ledger nor the store-credit ledger says has moved.
-- name: RecordReturnRefundedEvent :exec
INSERT INTO order_events (
    order_id, kind, note, actor_user_id, return_request_id
)
SELECT r.order_id, 'refunded', (
           SELECT rf.provider_ref
           FROM refunds rf
           WHERE rf.return_request_id = r.id AND rf.status = 'succeeded'
           ORDER BY rf.attempt_no DESC
           LIMIT 1
       ), @actor_user_id, r.id
FROM return_requests r
WHERE r.id = @return_request_id
  AND r.status IN ('approved', 'completed')
  AND (
      EXISTS (
          SELECT 1 FROM refunds rf
          WHERE rf.return_request_id = r.id AND rf.status = 'succeeded'
      )
      OR EXISTS (
          SELECT 1 FROM store_credit_entries e
          WHERE e.idempotency_key = 'return-credit:' || r.id::text
            AND e.amount_cents > 0
      )
  )
ON CONFLICT (return_request_id) WHERE return_request_id IS NOT NULL DO NOTHING;

-- name: OrderIDByNumber :one
SELECT id, fulfillment_status FROM orders WHERE order_number = $1;

-- Oldest first: occurred_at then id, because two events recorded in the same
-- statement share a timestamp and the uuidv7 key is the tie-break.
-- name: OrderEvents :many
SELECT e.kind, e.note, e.occurred_at, coalesce(u.full_name, '') AS actor_name
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

-- rescission_window: Consumer Protection Act §19 I runs seven days from RECEIPT,
-- Civil Code §120 II excludes the day of receipt, and §19 IV fixes the moment on
-- the customer's side — so created_at against delivered_at, both database
-- clocks, and both on the SHOP's calendar through shop_day. `::date` answers
-- in the session TimeZone, which is UTC here and stated nowhere: a parcel
-- handed over at 07:00 Taipei is the previous day in UTC, which closes an
-- unwaivable window a day early. Undelivered is neither answer, because the
-- window has not started.
-- name: ReturnQueue :many
SELECT r.id, r.status, r.reason, r.created_at, r.decided_at,
       o.order_number,
       (SELECT coalesce(sum(rl.quantity), 0) FROM return_request_lines rl
        WHERE rl.return_request_id = r.id)::integer AS units,
       return_refundable_amount(r.id)::bigint AS refundable_cents,
       (CASE
            WHEN d.delivered_at IS NULL THEN 'undelivered'
            WHEN shop_day(r.created_at) <= shop_day(d.delivered_at) + 7 THEN 'within'
            ELSE 'after'
        END)::text AS rescission_window
FROM return_requests r
JOIN orders o ON o.id = r.order_id
LEFT JOIN LATERAL (
    SELECT max(s.delivered_at) AS delivered_at
    FROM order_shipments s WHERE s.order_id = o.id
) d ON true
-- Recovery is the only retry door. Rank it before the intake queue and before
-- LIMIT, or fifty newer requests can make an older approved-but-unpaid customer
-- disappear from every actionable screen.
ORDER BY return_payout_outstanding(r.id) DESC,
         (r.status = 'requested') DESC,
         r.created_at DESC
LIMIT $1;

-- The amount comes from return_refundable_amount and never from anything the
-- request carried. It is a FUNCTION rather than an expression because the queue
-- needs the same number, and two copies are two figures free to disagree.
-- name: ReturnForDecision :one
SELECT r.id, r.status, r.reason, r.order_id,
       o.order_number, o.fulfillment_status,
       return_refundable_amount(r.id)::bigint AS refundable_cents,
       p.id AS payment_id,
       p.provider_ref,
       p.captured_amount_cents,
       o.user_id
FROM return_requests r
JOIN orders o ON o.id = r.order_id
LEFT JOIN payments p ON p.order_id = o.id AND p.status = 'succeeded'
WHERE r.id = $1;

-- received_quantity is NULL until somebody opens the parcel: "not looked at yet"
-- and "looked at, nothing arrived" are different facts. restockable is false for
-- a line whose variant was deleted, so the form cannot offer a refused control.
-- name: ReturnLines :many
SELECT rl.return_request_id, ol.id AS order_line_id, ol.sku, ol.product_name,
       ol.variant_label, ol.unit_price_cents, rl.quantity,
       rl.received_quantity, rl.restocked_quantity,
       coalesce(rl.inspection_note, '')::text AS inspection_note,
       (ol.variant_id IS NOT NULL)::boolean AS restockable
FROM return_request_lines rl
JOIN order_lines ol ON ol.id = rl.order_line_id
WHERE rl.return_request_id = ANY(@request_ids::uuid[])
ORDER BY rl.return_request_id, ol.position, ol.id;

-- One row per return, carrying its frozen source allocation and exact durable
-- settlement. The queue asks for the whole visible set in one call; an approved
-- retry asks for its one id through the same projection. Provider attempt state
-- is deliberately absent: pending work reuses its key and a known terminal
-- generation appends a successor, so neither makes the recovery button unsafe.
-- name: ReturnPayoutFacts :many
WITH selected AS (
    SELECT r.id, r.order_id, o.user_id, r.status,
           return_refundable_amount(r.id)::bigint AS refundable_cents,
           coalesce(r.card_refund_cents, 0)::bigint AS card_refund_cents,
           coalesce(r.credit_refund_cents, 0)::bigint AS credit_refund_cents
    FROM return_requests r
    JOIN orders o ON o.id = r.order_id
    WHERE r.id = ANY(@request_ids::uuid[])
)
SELECT s.id AS return_request_id,
       s.refundable_cents,
       s.card_refund_cents,
       s.credit_refund_cents,
       (s.user_id IS NOT NULL)::boolean AS has_account,
       coalesce((
           SELECT sum(rf.amount_cents)
           FROM refunds rf
           WHERE rf.return_request_id = s.id AND rf.status = 'succeeded'
       ), 0)::bigint AS card_paid_cents,
       coalesce((
           SELECT sum(sc.amount_cents)
           FROM store_credit_entries sc
           WHERE sc.idempotency_key = 'return-credit:' || s.id::text
       ), 0)::bigint AS credit_paid_cents,
       EXISTS (
           SELECT 1 FROM order_events e
           WHERE e.return_request_id = s.id AND e.kind = 'refunded'
       )::boolean AS refund_event_recorded,
       CASE
           -- Erasure detaches the order owner, but deliberately retains the
           -- order's award lot and loyalty account. A money-settled return may
           -- therefore still owe its idempotent clawback after user deletion.
           WHEN s.refundable_cents <= 0 THEN false
           ELSE (
			   return_loyalty_points_allocation(s.id) > 0
               AND EXISTS (
                   SELECT 1 FROM loyalty_entries e
                   WHERE e.order_id = s.order_id AND e.kind = 'award'
               )
               AND NOT EXISTS (
                   SELECT 1 FROM loyalty_entries e
                   WHERE e.return_request_id = s.id AND e.kind = 'clawback'
               )
           )
       END::boolean AS points_outstanding
FROM selected s
ORDER BY s.id;

-- `received_quantity IS NULL` makes a line inspectable ONCE: the restock behind
-- it posts a movement keyed on (request, line), so a second inspection would be
-- swallowed by that index and show a corrected count over unmoved stock.
-- name: InspectReturnLine :execrows
UPDATE return_request_lines rl
SET received_quantity = @received::integer,
    restocked_quantity = @restocked::integer,
    inspection_note = nullif(@note::text, '')
FROM return_requests r
WHERE r.id = rl.return_request_id
  AND rl.return_request_id = @request_id
  AND rl.order_line_id = @order_line_id
  AND r.status = 'approved'
  AND rl.received_quantity IS NULL;

-- The cast on variant_id is load-bearing: the column is nullable and the WHERE
-- clause excludes the NULLs, but sqlc reads the declaration and not the
-- predicate, so without it every caller unwraps a NullUUID that cannot be null.
-- name: ReturnRestockLines :many
SELECT ol.variant_id::uuid AS variant_id,
       rl.restocked_quantity::integer AS quantity, rl.order_line_id
FROM return_request_lines rl
JOIN order_lines ol ON ol.id = rl.order_line_id
WHERE rl.return_request_id = @request_id
  AND rl.restocked_quantity > 0
  AND ol.variant_id IS NOT NULL
-- record_inventory_movement locks the variant; use the same global order as
-- checkout and reservation release, with line id only as a stable tie-breaker.
ORDER BY ol.variant_id, rl.order_line_id;

-- return_requests_completed_is_inspected refuses this while any line is
-- un-inspected. `status = 'approved'` is restated for DecideReturn's reason: it
-- is what makes two staff members closing one return resolve to one winner.
-- name: LockReturnOrder :one
SELECT o.id
FROM orders o JOIN return_requests r ON r.order_id = o.id
WHERE r.id = @id
FOR UPDATE OF o;

-- name: CompleteReturn :execrows
UPDATE return_requests
SET status = 'completed', resolution = coalesce(nullif(@resolution::text, ''), resolution)
WHERE id = @id AND status = 'approved';

-- :execrows, because `status = 'requested'` here is the ONLY place the question
-- is asked under a lock: as :exec, the loser of two simultaneous decisions
-- updates zero rows, SQL calls that success, and an audit row claims a decision
-- nobody made — after paying a refund.
-- name: DecideReturn :execrows
UPDATE return_requests
SET status = @status::text, resolution = @resolution, decided_at = now()
WHERE id = @id AND status = 'requested';

-- The database derives the payment, request key, amount and reason from the
-- approved return. It also records this request's actor before any provider
-- operation begins, so a retry by another staff member remains attributable.
-- name: ClaimReturnRefundExecution :one
SELECT claim_return_refund_execution(
    @return_request_id::uuid, @actor_user_id::uuid, @request_id::text
);

-- A separate statement deliberately reads after the claim committed. A SELECT
-- invoking a mutating function keeps its outer snapshot and cannot see the row
-- that function just inserted.
-- name: RefundExecution :one
SELECT r.id AS refund_id, r.request_key,
       p.provider_ref AS payment_provider_ref,
       r.amount_cents, r.status
FROM refunds r
JOIN payments p ON p.id = r.payment_id
WHERE r.id = @refund_id
  AND r.status IN ('pending', 'requires_action');

-- Each provider state has its own door. A caller cannot pair a status with the
-- wrong identity/timestamp shape through one stringly settle function.
-- name: RecordRefundPending :one
SELECT record_refund_pending(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- name: RecordRefundRequiresAction :one
SELECT record_refund_requires_action(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- name: RecordRefundSucceeded :one
SELECT record_refund_succeeded(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- name: RecordRefundFailed :one
SELECT record_refund_failed(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- name: RecordRefundCancelled :one
SELECT record_refund_cancelled(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- Only a rejection specifically returned by Stripe's CREATE endpoint takes the
-- no-provider-object door. Lookup, transport and decode errors stay pending.
-- name: RecordRefundAPIRejection :one
SELECT record_refund_api_rejection(
    @refund_id::uuid, @actor_user_id::uuid, @request_id::text
);

-- Show only the latest generation of a return refund. A failed predecessor is
-- evidence, not current work; once its successor succeeds it must not keep the
-- health page red forever. Non-return refunds have no generation lineage.
-- name: OpenRefundCount :one
SELECT count(*)::bigint
FROM refunds r
WHERE r.status IN ('pending', 'requires_action', 'failed', 'cancelled')
  AND (
      r.return_request_id IS NULL
      OR NOT EXISTS (
          SELECT 1 FROM refunds newer
          WHERE newer.return_request_id = r.return_request_id
            AND newer.attempt_no > r.attempt_no
      )
  );

-- This is a bounded diagnostic sample. OpenRefundCount, not the length of this
-- sample, is the health figure rendered above it.
-- name: OpenRefunds :many
SELECT r.request_key, r.status, r.amount_cents, r.created_at,
       coalesce(r.provider_ref, '')::text AS provider_ref,
       o.order_number
FROM refunds r
JOIN payments p ON p.id = r.payment_id
JOIN orders o ON o.id = p.order_id
WHERE r.status IN ('pending', 'requires_action', 'failed', 'cancelled')
  AND (
      r.return_request_id IS NULL
      OR NOT EXISTS (
          SELECT 1 FROM refunds newer
          WHERE newer.return_request_id = r.return_request_id
            AND newer.attempt_no > r.attempt_no
      )
  )
ORDER BY r.created_at
LIMIT $1;

-- internal/account already defines UserByEmail for sign-in and sqlc generates
-- one db package, so this one is named for what it is FOR. It also selects less:
-- the back office has no business reading a password hash.
-- name: CustomerByEmail :one
SELECT id, email, coalesce(full_name, '') AS full_name FROM users
WHERE lower(email) = lower(@email::text);

-- From store_credit_balances, the ONE view that defines a balance, never a sum
-- written out again here.
-- name: CreditBalance :one
SELECT coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = $1), 0)::bigint;

-- The casts are what make the nullability explicit: sqlc reads a bare parameter
-- as non-nullable, and a grant has no order behind it and may have no actor.
-- name: PostStoreCredit :one
SELECT grant_store_credit(
    @user_id, @amount_cents::bigint, @reason::text,
    @actor_user_id::uuid, @operation_id::uuid
)::uuid AS entry_id;

-- name: RecentCredit :many
SELECT e.amount_cents, e.reason, e.created_at,
       coalesce(u.email, '') AS email
FROM store_credit_entries e
JOIN store_credit_accounts a ON a.id = e.account_id
LEFT JOIN users u ON u.id = a.user_id
ORDER BY e.created_at DESC, e.id DESC
LIMIT $1;

-- name: AdminProducts :many
SELECT p.id, p.slug, p.name, p.status, p.published_at,
       (p.name_en IS NOT NULL)::boolean AS translated,
       b.name AS brand, c.name AS category,
       (SELECT count(*) FROM product_variants pv WHERE pv.product_id = p.id)::integer AS variants,
       (SELECT coalesce(min(pv.price_cents), 0) FROM product_variants pv
        WHERE pv.product_id = p.id AND pv.is_active)::bigint AS from_cents
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN categories c ON c.id = p.category_id
ORDER BY p.updated_at DESC, p.id DESC
LIMIT $1;

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

-- The redemption count comes from the ledger and never from a column: the ledger
-- is what the limit is counted from at checkout.
-- name: AdminCoupons :many
SELECT c.id, c.code, c.description, c.kind, c.amount_cents, c.percent_bp,
       c.min_subtotal_cents, c.max_discount_cents, c.max_redemptions,
       c.per_customer_limit, c.is_active, c.starts_at, c.ends_at,
       (SELECT count(*) FROM coupon_redemptions r WHERE r.coupon_id = c.id)::bigint AS redeemed,
       (SELECT coalesce(sum(r.amount_cents), 0) FROM coupon_redemptions r
        WHERE r.coupon_id = c.id)::bigint AS given_cents,
       (c.starts_at <= now() AND (c.ends_at IS NULL OR c.ends_at > now()))::boolean AS is_current
FROM coupons c
ORDER BY c.is_active DESC, c.created_at DESC
LIMIT $1;

-- name: CreateCoupon :exec
INSERT INTO coupons (code, description, kind, amount_cents, percent_bp,
                     max_discount_cents, min_subtotal_cents,
                     max_redemptions, per_customer_limit, ends_at)
VALUES (@code::text, @description::text, @kind::text,
        sqlc.narg(amount_cents)::bigint, sqlc.narg(percent_bp)::integer,
        sqlc.narg(max_discount_cents)::bigint, @min_subtotal_cents::bigint,
        sqlc.narg(max_redemptions)::integer, @per_customer_limit::integer,
        CASE WHEN @days::integer > 0
             THEN now() + make_interval(days => @days::integer)
             ELSE NULL END);

-- Switched off, never deleted: coupon_redemptions references it, and a promotion
-- that ran is part of what past orders were charged.
-- name: SetCouponActive :execrows
UPDATE coupons SET is_active = @is_active::boolean WHERE upper(code) = upper(@code::text);

-- name: AdminCampaigns :many
SELECT c.id, c.slug, c.title, c.starts_at, c.ends_at, c.is_active,
       (SELECT count(*) FROM sale_campaign_products p WHERE p.campaign_id = c.id)::bigint AS products,
       (c.is_active AND c.starts_at <= now() AND c.ends_at > now())::boolean AS is_running
FROM sale_campaigns c
ORDER BY c.is_active DESC, c.ends_at DESC
LIMIT $1;

-- name: CreateCampaign :exec
INSERT INTO sale_campaigns (slug, title, title_en, ends_at)
VALUES (@slug::text, @title::text, nullif(@title_en::text, ''),
        now() + (@days::integer || ' days')::interval);

-- name: SetCampaignActive :execrows
UPDATE sale_campaigns SET is_active = @is_active::boolean WHERE slug = @slug::text;

-- ONE statement: sale_campaign_needs_discount refuses a product with nothing
-- marked down and takes a lock on it first, so a check here would be a check a
-- concurrent price change invalidates.
-- name: AddCampaignProduct :exec
INSERT INTO sale_campaign_products (campaign_id, product_id, position)
SELECT c.id, p.id,
       coalesce((SELECT max(position) + 1 FROM sale_campaign_products x
                 WHERE x.campaign_id = c.id), 0)
FROM sale_campaigns c, products p
WHERE c.slug = @campaign::text AND p.slug = @product::text
ON CONFLICT (campaign_id, product_id) DO NOTHING;

-- name: RemoveCampaignProduct :exec
DELETE FROM sale_campaign_products cp
USING sale_campaigns c, products p
WHERE cp.campaign_id = c.id AND cp.product_id = p.id
  AND c.slug = @campaign::text AND p.slug = @product::text;

-- name: AdminCampaignProducts :many
SELECT p.slug, p.name, cp.position
FROM sale_campaign_products cp
JOIN products p ON p.id = cp.product_id
JOIN sale_campaigns c ON c.id = cp.campaign_id
WHERE c.slug = @campaign::text
ORDER BY cp.position, p.id;

-- name: RecordAuditEvent :one
SELECT record_audit_event(@actor, @action::text, @entity_table::text,
                          sqlc.narg('entity_id')::uuid,
                          @before, @after, sqlc.narg('request_id')::text);

-- name: AuditEvents :many
SELECT a.action, a.entity_table, a.entity_id, a.before, a.after,
       a.request_id, a.occurred_at,
       coalesce(u.full_name, u.email, a.actor_id_snapshot::text) AS actor
FROM audit_events a
LEFT JOIN users u ON u.id = a.actor_user_id
ORDER BY a.occurred_at DESC, a.id DESC
LIMIT $1;

-- No foreign key on storage_key: product_images predates media_objects and still
-- holds embedded-asset names from the seed, so the column carries two kinds of
-- key.
-- name: AttachProductImage :exec
INSERT INTO product_images (product_id, storage_key, alt_text, alt_text_en,
                            width, height, position)
SELECT p.id, @storage_key::text, @alt_text::text, nullif(@alt_text_en::text, ''),
       @width::integer, @height::integer,
       coalesce((SELECT max(position) + 1 FROM product_images x WHERE x.product_id = p.id), 0)
FROM products p
WHERE p.slug = @slug::text;

-- name: DetachProductImage :execrows
DELETE FROM product_images pi
USING products p
WHERE pi.product_id = p.id AND p.slug = @slug::text AND pi.storage_key = @storage_key::text;

-- name: AdminProductImages :many
SELECT pi.storage_key, pi.alt_text, pi.width, pi.height
FROM product_images pi
JOIN products p ON p.id = pi.product_id
WHERE p.slug = @slug::text
ORDER BY pi.position, pi.id;

-- name: AdminHeroSlides :many
SELECT h.id, h.eyebrow, h.headline, h.primary_cta_label, h.primary_cta_href,
       h.image_key, h.position, h.is_active, h.starts_at, h.ends_at,
       (h.is_active
        AND (h.starts_at IS NULL OR h.starts_at <= now())
        AND (h.ends_at IS NULL OR h.ends_at > now()))::boolean AS in_window
FROM hero_slides h
ORDER BY h.position, h.id
LIMIT $1;

-- name: CreateHeroSlide :exec
INSERT INTO hero_slides (
    eyebrow, headline, body, primary_cta_label, primary_cta_href,
    secondary_cta_label, secondary_cta_href, image_key, image_alt,
    eyebrow_en, headline_en, body_en, primary_cta_label_en,
    secondary_cta_label_en, image_alt_en,
    position, ends_at
) VALUES (
    nullif(@eyebrow::text, ''), @headline::text, nullif(@body::text, ''),
    @primary_cta_label::text, @primary_cta_href::text,
    nullif(@secondary_cta_label::text, ''), nullif(@secondary_cta_href::text, ''),
    nullif(@image_key::text, ''), nullif(@image_alt::text, ''),
    nullif(@eyebrow_en::text, ''), nullif(@headline_en::text, ''),
    nullif(@body_en::text, ''), nullif(@primary_cta_label_en::text, ''),
    nullif(@secondary_cta_label_en::text, ''), nullif(@image_alt_en::text, ''),
    coalesce((SELECT max(position) + 1 FROM hero_slides), 0),
    CASE WHEN @days::integer > 0 THEN now() + make_interval(days => @days::integer) END
);

-- name: SetHeroSlideActive :execrows
UPDATE hero_slides SET is_active = @is_active::boolean WHERE id = @id;

-- ONE row: the promoted slide takes a position below every other, rather than
-- everything else shifting up and position growing without bound.
-- name: PromoteHeroSlide :execrows
UPDATE hero_slides h
SET position = coalesce((SELECT min(o.position) FROM hero_slides o), 0) - 1
WHERE h.id = @id;

-- name: ManagedBrands :many
SELECT b.id, b.slug, b.name,
       (SELECT count(*) FROM products p WHERE p.brand_id = b.id)::bigint AS products
FROM brands b
ORDER BY b.name;

-- name: CreateBrand :exec
INSERT INTO brands (slug, name) VALUES (@slug::text, @name::text);

-- name: RenameBrand :execrows
UPDATE brands SET name = @name::text WHERE slug = @slug::text;

-- The emptiness check is in the statement, not read first. The foreign keys are
-- ON DELETE RESTRICT and would refuse anyway; this turns the refusal into a row
-- count, which is the difference between a sentence and a constraint name.
-- name: DeleteBrand :execrows
DELETE FROM brands b
WHERE b.slug = @slug::text
  AND NOT EXISTS (SELECT 1 FROM products p WHERE p.brand_id = b.id);

-- name: ManagedCategories :many
WITH RECURSIVE tree AS (
    SELECT c.id, c.parent_id, c.slug, c.name, c.name_en, c.icon_key, c.position,
           0 AS depth, array[c.position, 0] AS path
    FROM categories c WHERE c.parent_id IS NULL
    UNION ALL
    SELECT c.id, c.parent_id, c.slug, c.name, c.name_en, c.icon_key, c.position,
           t.depth + 1, t.path || array[c.position, 0]
    FROM categories c JOIN tree t ON t.id = c.parent_id
)
SELECT t.id, t.slug, t.name, coalesce(t.name_en, '') AS name_en,
       coalesce(t.icon_key, '') AS icon_key,
       t.depth::integer AS depth,
       coalesce(p.name, '') AS parent_name,
       (SELECT count(*) FROM products x WHERE x.category_id = t.id)::bigint AS products,
       (SELECT count(*) FROM categories k WHERE k.parent_id = t.id)::bigint AS children
FROM tree t
LEFT JOIN categories p ON p.id = t.parent_id
ORDER BY t.path, t.name;

-- The parent is a derived table and not a scalar subquery: that would yield NULL
-- for a slug that does not exist, creating a ROOT category and reporting
-- success. No rows is how the caller learns the parent was not found.
-- name: CreateCategory :execrows
INSERT INTO categories (slug, name, name_en, icon_key, parent_id, position)
SELECT @slug::text, @name::text, nullif(@name_en::text, ''),
       nullif(@icon_key::text, ''), parent.id,
       coalesce((SELECT max(c.position) + 1 FROM categories c
                 WHERE c.parent_id IS NOT DISTINCT FROM parent.id), 0)
FROM (
    SELECT c.id FROM categories c WHERE c.slug = @parent_slug::text
    UNION ALL
    SELECT NULL::uuid WHERE @parent_slug::text = ''
) parent;

-- The DISPLAY names only: a slug is in every URL a search engine has indexed and
-- goen has no redirect table. nullif('') is what lets name_en be cleared.
-- name: RenameCategory :execrows
UPDATE categories SET name = @name::text, name_en = nullif(@name_en::text, ''),
                     icon_key = nullif(@icon_key::text, '')
WHERE slug = @slug::text;

-- name: DeleteCategory :execrows
DELETE FROM categories c
WHERE c.slug = @slug::text
  AND NOT EXISTS (SELECT 1 FROM products p WHERE p.category_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM categories k WHERE k.parent_id = c.id);

-- COMMITTED orders only, and the total is recomputed from the lines because
-- orders carries no total column. Integer division on the average, so no float
-- touches money, and greatest(count, 1) because an empty window divides by zero.
-- name: RevenueSince :one
SELECT
    count(*)::bigint AS orders,
    coalesce(sum(t.total), 0)::bigint AS revenue_cents,
    (coalesce(sum(t.total), 0) / greatest(count(*), 1))::bigint AS average_cents,
    -- What went back, as its own figure rather than subtracted from the one
    -- above. Consumer Protection Act §19 makes a seven-day rescission
    -- unrefusable, so returns are certain rather than hypothetical, and an owner
    -- needs the return rate as much as the net. Counted by when each source
    -- moved: succeeded_at for a card refund (created_at can be days earlier
    -- while Stripe still says pending), and created_at for the synchronous
    -- credit post.
    --
    -- The positive-credit predicate deliberately matches order_refunds. That
    -- includes reverse_order_credit on a CANCELLED order whose revenue was never
    -- counted here; excluding cancellations in this caller would create another
    -- definition. If the report should exclude them, change order_refunds so the
    -- 折讓 form and invoice bound make the same decision. Neither time column has
    -- an index yet; these are small ledgers, so a speculative index is not
    -- warranted.
    (coalesce((SELECT sum(r.amount_cents) FROM refunds r
               WHERE r.status = 'succeeded'
                 AND r.succeeded_at >= now() - make_interval(days => @window_days::integer)), 0)::bigint
     + coalesce((SELECT sum(e.amount_cents) FROM store_credit_entries e
                 WHERE e.order_id IS NOT NULL AND e.amount_cents > 0
                   AND e.created_at >= now() - make_interval(days => @window_days::integer)), 0)::bigint
    )::bigint AS refunded_cents
FROM (
    SELECT (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                      FROM order_lines ol WHERE ol.order_id = o.id), 0)
            - o.discount_cents + o.shipping_cents + o.tax_cents)::bigint AS total
    FROM orders o
    JOIN committed_orders c ON c.id = o.id
    WHERE o.placed_at >= now() - make_interval(days => @window_days::integer)
) t;

-- name: BestSellersSince :many
SELECT
    p.slug,
    p.name,
    coalesce(b.name, '') AS brand,
    sum(ol.quantity)::bigint AS units,
    sum(ol.unit_price_cents * ol.quantity)::bigint AS revenue_cents
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
JOIN products p ON p.id = ol.product_id
JOIN committed_orders c ON c.id = o.id
LEFT JOIN brands b ON b.id = p.brand_id
WHERE o.placed_at >= now() - make_interval(days => @window_days::integer)
GROUP BY p.slug, p.name, b.name
ORDER BY units DESC, revenue_cents DESC
LIMIT @limit_to::integer;

-- NOT a conversion rate: goen collects no traffic data. This is the fraction of
-- started orders that were paid for. A LEFT JOIN and a CASE, never a per-row
-- function call — measured at 106 ms over 14,000 orders against 7.7 ms.
-- name: CheckoutCompletionSince :one
SELECT
    count(*)::bigint AS placed,
    coalesce(sum(CASE WHEN c.id IS NOT NULL THEN 1 ELSE 0 END), 0)::bigint AS committed
FROM orders o
LEFT JOIN committed_orders c ON c.id = o.id
WHERE o.placed_at >= now() - make_interval(days => @window_days::integer);

-- days_cover is never NULL because the WHERE clause admits only variants that
-- sold something, so the divisor cannot be zero.
-- name: StockAtRisk :many
SELECT
    pv.sku,
    p.name AS product_name,
    p.slug,
    pv.stock_quantity,
    pv.safety_stock,
    sold.units::bigint AS units_sold,
    (pv.stock_quantity::numeric
     / (sold.units::numeric / @window_days::integer))::integer AS days_cover
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
JOIN LATERAL (
    SELECT coalesce(sum(ol.quantity), 0) AS units
    FROM order_lines ol
    JOIN orders o ON o.id = ol.order_id
    JOIN committed_orders c ON c.id = o.id
    WHERE ol.variant_id = pv.id
      AND o.placed_at >= now() - make_interval(days => @window_days::integer)
) sold ON true
WHERE pv.is_active AND p.status = 'active' AND sold.units > 0
ORDER BY days_cover NULLS LAST, pv.stock_quantity
LIMIT @limit_to::integer;

-- name: UnansweredQuestions :many
SELECT q.id, q.body, q.created_at,
       p.slug AS product_slug, p.name AS product_name,
       coalesce(u.full_name, '') AS asker,
       (SELECT count(*) FROM product_answers a
        WHERE a.question_id = q.id AND a.hidden_at IS NULL)::bigint AS answers,
       EXISTS (SELECT 1 FROM product_answers a
               WHERE a.question_id = q.id AND a.is_staff AND a.hidden_at IS NULL) AS answered_by_shop
FROM product_questions q
JOIN products p ON p.id = q.product_id
LEFT JOIN users u ON u.id = q.user_id
WHERE q.hidden_at IS NULL
ORDER BY answered_by_shop, q.created_at
LIMIT $1;

-- name: HideQuestion :execrows
UPDATE product_questions SET hidden_at = now()
WHERE id = @question_id AND hidden_at IS NULL;

-- Overdue is measured from available_at — when a message became DUE — because
-- the claim lease and the backoff push it forward. copurchase_ever_built is
-- separate from the age because max() over an empty table is NULL, which sqlc
-- infers as non-nullable and pgx then refuses to scan: a fresh deployment only.
-- name: WorkerHealth :one
SELECT
    (SELECT count(*) FROM outbox_messages
     WHERE delivered_at IS NULL)::bigint AS outbox_pending,
    (SELECT greatest(coalesce(extract(epoch FROM now() - min(available_at)), 0), 0)
     FROM outbox_messages WHERE delivered_at IS NULL)::bigint AS outbox_oldest_seconds,
    (SELECT count(*) FROM outbox_messages
     WHERE delivered_at IS NULL AND attempts >= @max_attempts::integer)::bigint AS outbox_stuck,
    -- The sweeper's own predicate, not merely expired: release_reservation
    -- refuses a committed or fully-funded order's hold, so counting every
    -- expired row reports stock the sweeper is designed never to release, on a
    -- page whose caption says a backlog means goods nobody can buy. It can only
    -- grow, which is alarm fatigue on the page built to make failure visible.
    (SELECT count(*) FROM inventory_reservations ir
     JOIN orders o ON o.id = ir.order_id
     WHERE ir.state = 'held' AND ir.expires_at < now()
       AND NOT order_is_committed(ir.order_id)
       AND (o.fulfillment_status = 'cancelled'
            OR order_amount_owed(ir.order_id) <> 0)
       -- Match ExpiredReservations: reconciliation deliberately pins stock
       -- while provider money may exist, so it is not a sweeper backlog.
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
       )))::bigint AS expired_holds,
    (SELECT coalesce(extract(epoch FROM now() - max(computed_at)), 0)
     FROM product_copurchases)::bigint AS copurchase_age_seconds,
    EXISTS (SELECT 1 FROM product_copurchases) AS copurchase_ever_built,
    (SELECT count(*) FROM sessions WHERE expires_at <= now())::bigint AS expired_sessions,
    (SELECT count(*) FROM media_objects m
     WHERE NOT EXISTS (SELECT 1 FROM product_images p WHERE p.storage_key = m.digest)
       AND NOT EXISTS (SELECT 1 FROM hero_slides h WHERE h.image_key = m.digest)
       AND m.created_at < now() - interval '24 hours')::bigint AS unreferenced_media,
    -- Events accepted and NOT acted on: a known Stripe object this binary could
    -- not read, paid money with no local payment row, paid money for an order
    -- already cancelled, or a completed checkout whose money is still in
    -- flight. Each is still marked processed because retrying the same event
    -- changes nothing; the durable reason makes the human action countable
    -- instead of leaving only a log line nobody reads.
    ((SELECT count(*) FROM payment_webhook_events
      WHERE unreconciled IS NOT NULL AND reconciled_at IS NULL)
     +
     (SELECT count(*) FROM payments p
      WHERE p.status = 'requires_reconciliation'
        AND NOT EXISTS (
            SELECT 1 FROM payment_webhook_events e
            WHERE e.provider = p.provider AND e.object_ref = p.provider_ref
              AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
        )))::bigint AS unreconciled_payments;

-- The events a person has to act on, named rather than counted: a page saying
-- "1 unreconciled" that cannot say WHICH tells an operator something is wrong
-- and nothing about what to do, which is the reason outbox.Stuck() lists.
-- name: UnreconciledPayments :many
SELECT event_id, type, coalesce(object_ref, '') AS object_ref,
       unreconciled::text AS reason, received_at
FROM payment_webhook_events
WHERE unreconciled IS NOT NULL AND reconciled_at IS NULL
ORDER BY received_at
LIMIT 50;

-- Provider-complete payment identities without an outstanding event alarm.
-- These cover the window before a webhook arrives. Understood-but-unpaid
-- completion is an event alarm, not this list. They are excluded when an
-- event alarm already names the same work, so health shows one resolution
-- door rather than two competing ones.
-- name: UnreconciledCompletePayments :many
SELECT o.order_number, p.provider_ref, p.created_at,
       coalesce(NOT EXISTS (
           SELECT 1 FROM inventory_reservations ir
           WHERE ir.order_id = p.order_id AND ir.state = 'released'
       ), false)::boolean AS paid_attribution_allowed
FROM payments p
JOIN orders o ON o.id = p.order_id
WHERE p.status = 'requires_reconciliation'
  AND NOT EXISTS (
      SELECT 1 FROM payment_webhook_events e
      WHERE e.provider = p.provider AND e.object_ref = p.provider_ref
        AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
  )
ORDER BY p.created_at
LIMIT 50;

-- Durable e-invoice operations which either explicitly alarmed or have remained
-- pending beyond several worker polls. Rejected and succeeded evidence remains
-- durable but is not an active health alarm.
-- name: StrandedInvoiceClaims :many
SELECT op.id AS operation_id, o.order_number, op.kind, op.status,
       op.amount_cents, op.reconcile_attempts, op.send_attempts,
       coalesce(op.last_error, '')::text AS last_error, op.created_at,
       (op.kind = 'allowance'
        AND op.status = 'pending'
        AND op.send_attempts > op.resend_authorizations
        AND op.last_error = 'allowance_not_yet_visible'
        AND op.last_send_at IS NOT NULL
        AND op.last_send_at <= now() - interval '15 minutes'
        AND (op.lease_until IS NULL OR op.lease_until <= now()))::boolean
           AS can_authorize_resend
FROM invoice_operations op
JOIN orders o ON o.id = op.order_id
WHERE op.status = 'attention'
   OR (op.status = 'pending' AND op.created_at < now() - interval '15 minutes')
ORDER BY op.created_at
LIMIT 50;

-- A human has independently checked ECPay and confirmed the missing Allowance.
-- The database rechecks age/state/lease and records actor + request atomically.
-- name: AuthorizeInvoiceAllowanceResend :one
SELECT authorize_invoice_allowance_resend(
    @operation_id::uuid, @actor_user_id::uuid, @request_id::text
)::boolean AS authorized;

-- The locale comes off the ORDER and never off the staff member who pressed
-- Ship, which would send a Taiwanese shopkeeper's language to an English
-- customer.
-- name: ShipmentRecipient :one
SELECT coalesce(pd.email, '') AS email,
       coalesce(pd.recipient_name, '') AS recipient_name,
       o.locale
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.id = $1;

-- The claim and the enqueue are ONE transaction, so notified_at means "the
-- outbox has this": claiming without enqueuing tells nobody and never retries.
-- The threshold is the one the listing calls in stock, stock > safety_stock.
-- name: ClaimRestockNotices :many
UPDATE stock_notifications sn SET notified_at = now()
WHERE sn.variant_id = $1
  AND sn.notified_at IS NULL
  AND EXISTS (SELECT 1 FROM product_variants pv
              WHERE pv.id = sn.variant_id
                AND pv.is_active
                AND pv.stock_quantity > pv.safety_stock)
RETURNING sn.id, sn.email, sn.locale;

-- Called once per distinct LOCALE in the claimed set, not once per recipient:
-- the product name has to follow the reader as the letter's words do.
-- name: RestockSubject :one
SELECT p.slug,
       localized_name(p.name, p.name_en, @locale::text) AS product_name,
       pv.sku
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.id = @variant_id;

-- DISTINCT ON the method, ordered by effective_at DESC: the versions table is
-- append-only, so the current fee is the newest row that has taken effect.
-- name: AdminShippingMethods :many
SELECT DISTINCT ON (sm.id)
    sm.id AS method_id, sm.code, sm.destination_kind, sm.is_active,
    v.id AS version_id, v.name, v.carrier,
    coalesce(v.name_en, '') AS name_en, coalesce(v.carrier_en, '') AS carrier_en,
    v.fee_cents, v.free_over_cents, v.effective_at,
    (SELECT count(*) FROM shipping_method_versions mv WHERE mv.method_id = sm.id)::bigint
        AS version_count
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE v.effective_at <= now()
ORDER BY sm.id, v.effective_at DESC;

-- name: AdminVersionZones :many
SELECT z.id AS zone_id, z.code, z.name, vz.surcharge_cents
FROM shipping_version_zones vz
JOIN shipping_zones z ON z.id = vz.zone_id
WHERE vz.version_id = ANY(@version_ids::uuid[])
ORDER BY z.position, z.name;

-- An INSERT and never an UPDATE: shipping_method_versions_append_only refuses
-- one, because every past order names the version it was priced from.
-- name: PublishShippingVersion :one
INSERT INTO shipping_method_versions (method_id, name, carrier, name_en, carrier_en,
                                      fee_cents, free_over_cents)
VALUES (@method_id, @name, nullif(@carrier::text, ''),
        nullif(@name_en::text, ''), nullif(@carrier_en::text, ''),
        @fee_cents, nullif(@free_over_cents::bigint, 0))
RETURNING id;

-- Without this, publishing a new base fee silently drops every surcharge: the
-- rows key on the VERSION, and the new version has none — so a shop raising its
-- home-delivery fee would start shipping to the outlying islands at that fee.
-- name: CarryZoneSurcharges :exec
INSERT INTO shipping_version_zones (version_id, zone_id, surcharge_cents)
SELECT @new_version_id, vz.zone_id, vz.surcharge_cents
FROM shipping_version_zones vz
WHERE vz.version_id = (
    SELECT v.id FROM shipping_method_versions v
    WHERE v.method_id = @method_id AND v.id <> @new_version_id
      AND v.effective_at <= now()
    ORDER BY v.effective_at DESC, v.id DESC
    LIMIT 1
)
ON CONFLICT (version_id, zone_id) DO NOTHING;

-- name: AdminShippingZones :many
SELECT z.id, z.code, z.name, coalesce(z.name_en, '') AS name_en, z.position,
       (SELECT count(*) FROM shipping_zone_prefixes zp WHERE zp.zone_id = z.id)::bigint
           AS prefix_count,
       coalesce((SELECT string_agg(zp.prefix, ' ' ORDER BY zp.prefix)
                 FROM shipping_zone_prefixes zp WHERE zp.zone_id = z.id), '')::text
           AS prefixes
FROM shipping_zones z
ORDER BY z.position, z.name;

-- ON CONFLICT so the form is idempotent: a staff member who submits twice has
-- set one surcharge, not failed the second time.
-- name: SetZoneSurcharge :exec
INSERT INTO shipping_version_zones (version_id, zone_id, surcharge_cents)
VALUES (@version_id, @zone_id, @surcharge_cents)
ON CONFLICT (version_id, zone_id) DO UPDATE
SET surcharge_cents = EXCLUDED.surcharge_cents;

-- Absence is what "no surcharge" means to the lookup, which coalesces a missing
-- row to zero, so clearing is a DELETE and never a stored zero.
-- name: ClearZoneSurcharge :execrows
DELETE FROM shipping_version_zones WHERE version_id = $1 AND zone_id = $2;

-- name: AdminMembershipTiers :many
SELECT t.id, t.code, t.name, coalesce(t.name_en, '') AS name_en,
       t.min_spend_cents, t.points_multiplier_bp, t.position,
       (SELECT count(*) FROM users u
        WHERE member_tier(u.id, @window_days::integer, NULL) = t.id)::bigint AS members
FROM membership_tiers t
ORDER BY t.min_spend_cents;

-- name: CreateMembershipTier :exec
INSERT INTO membership_tiers (code, name, name_en, min_spend_cents,
                              points_multiplier_bp, position)
VALUES (@code, @name, nullif(@name_en::text, ''), @min_spend_cents,
        @points_multiplier_bp, @position);

-- A DELETE and not a flag: no order references a tier, and the customers who
-- were in it are re-derived into whichever band they now qualify for.
-- name: DeleteMembershipTier :execrows
DELETE FROM membership_tiers WHERE id = $1;

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
    pickup_brand = nullif(@pickup_brand::text, ''),
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

-- The BASE table, so hidden reviews are listed too: un-hiding one is not
-- possible from a list that cannot show it.
-- name: AdminReviews :many
SELECT r.id, r.rating, coalesce(r.title, '') AS title, r.body,
       r.is_verified_purchase, r.hidden_at, r.created_at,
       p.slug, p.name AS product_name,
       coalesce(u.full_name, '') AS author
FROM product_reviews r
JOIN products p ON p.id = r.product_id
LEFT JOIN users u ON u.id = r.user_id
ORDER BY r.created_at DESC, r.id DESC
LIMIT $1;

-- name: HideReview :execrows
UPDATE product_reviews SET hidden_at = now()
WHERE id = $1 AND hidden_at IS NULL;

-- name: ShowReview :execrows
UPDATE product_reviews SET hidden_at = NULL
WHERE id = $1 AND hidden_at IS NOT NULL;

-- waiting_days is computed HERE because created_at is written by the database's
-- clock: taking the difference in Go subtracts two clocks, and a container
-- milliseconds ahead of its host reports a four-day-old message as three.
-- name: AdminMessages :many
SELECT id, name, email, subject, coalesce(order_ref, '') AS order_ref,
       message, handled_at, created_at,
       floor(extract(epoch FROM now() - created_at) / 86400)::integer AS waiting_days
FROM contact_messages
ORDER BY (handled_at IS NOT NULL), created_at
LIMIT $1;

-- name: HandleMessage :execrows
UPDATE contact_messages SET handled_at = now()
WHERE id = $1 AND handled_at IS NULL;

-- name: ReopenMessage :execrows
UPDATE contact_messages SET handled_at = NULL
WHERE id = $1 AND handled_at IS NOT NULL;

-- A NEW POSITIVE entry and not a reversal of the spend, which the schema
-- prescribes for an order that has shipped: a reversal un-funds the order, and
-- this one was paid for and went out. Idempotent on the return.
-- name: CompensateReturnWithCredit :one
SELECT compensate_return_with_credit(
    @return_id::uuid, @amount_cents::bigint, sqlc.narg(actor)::uuid
)::uuid AS entry_id;

-- name: ReverseReturnPoints :one
-- The return is the sole capability. The database derives its order, durable
-- refund amount and award proportion after verifying that the payout landed.
SELECT reverse_return_points(@return_id::uuid)::bigint AS points_reversed;

-- Prefix on both, each index-backed, with a floor on the term enforced by the
-- caller. Every role is searched, for AdminCustomer's reason. An erased
-- customer's row is gone, so nothing extra is needed to exclude one.
-- name: AdminSearchCustomers :many
SELECT u.id, u.email, coalesce(u.full_name, '') AS full_name, u.created_at,
       (u.email_verified_at IS NOT NULL)::boolean AS verified,
       (SELECT count(*) FROM orders o WHERE o.user_id = u.id)::bigint AS orders
FROM users u
WHERE (lower(u.email) LIKE lower(@term::text) || '%'
       OR u.full_name LIKE @term::text || '%')
ORDER BY u.created_at DESC
LIMIT @row_limit::integer;

-- Spend counts COMMITTED orders only, and both balances come from the VIEWS that
-- define them. No role predicate, deliberately: /admin/staff promotes an
-- existing customer, whose order history must stay reachable from this page.
-- name: AdminCustomer :one
SELECT u.id, u.email, coalesce(u.full_name, '') AS full_name,
       coalesce(u.phone, '') AS phone, u.created_at,
       (u.email_verified_at IS NOT NULL)::boolean AS verified,
       (SELECT count(*) FROM orders o WHERE o.user_id = u.id)::bigint AS orders,
       coalesce((SELECT sum(greatest(
                            coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                                      FROM order_lines ol WHERE ol.order_id = o.id), 0)
                            + o.shipping_cents + o.tax_cents - o.discount_cents
                            - (rf.card_cents + rf.credit_cents), 0))
                 FROM orders o
                 JOIN committed_orders c ON c.id = o.id
                 JOIN order_refunds rf ON rf.order_id = o.id
                 WHERE o.user_id = u.id), 0)::bigint AS spent,
       coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = u.id), 0)::bigint AS credit_cents,
       coalesce((SELECT lb.points FROM loyalty_balances lb
                 JOIN store_credit_accounts a ON a.id = lb.account_id
                 WHERE a.user_id = u.id), 0)::bigint AS points
FROM users u
WHERE u.id = $1;

-- name: AdminCustomerOrders :many
SELECT o.order_number, o.fulfillment_status, o.placed_at,
       o.shipping_cents, o.discount_cents, o.tax_cents,
       order_is_committed(o.id) AS committed,
       order_amount_owed(o.id) AS owed_cents,
       coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                 WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents
FROM orders o
WHERE o.user_id = $1
ORDER BY o.placed_at DESC
LIMIT $2;

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
       )::text[] AS value_labels
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
INSERT INTO product_option_values (product_id, option_id, value, value_en, position)
SELECT o.product_id, o.id, @value::text, nullif(@value_en::text, ''),
       coalesce((SELECT max(v.position) FROM product_option_values v
                 WHERE v.option_id = o.id), 0) + 1
FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = @slug::text AND o.id = @option_id
RETURNING id;

-- name: ProductOptionCount :one
SELECT count(*)::bigint FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = $1;

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

-- name: ManagedBanners :many
SELECT id, message, coalesce(message_short, '') AS message_short,
       coalesce(code, '') AS code,
       coalesce(cta_label, '') AS cta_label, coalesce(cta_href, '') AS cta_href,
       coalesce(message_en, '') AS message_en,
       coalesce(message_short_en, '') AS message_short_en,
       coalesce(cta_label_en, '') AS cta_label_en,
       is_active, starts_at, ends_at, created_at
FROM promo_banners
ORDER BY is_active DESC, created_at DESC
LIMIT $1;

-- The CTA is both-or-neither, which promo_banners_cta_complete also says.
-- name: CreateBanner :exec
INSERT INTO promo_banners (
    message, message_short, code, cta_label, cta_href,
    message_en, message_short_en, cta_label_en, ends_at
) VALUES (
    @message::text, nullif(@message_short::text, ''), nullif(@code::text, ''),
    nullif(@cta_label::text, ''), nullif(@cta_href::text, ''),
    nullif(@message_en::text, ''), nullif(@message_short_en::text, ''),
    nullif(@cta_label_en::text, ''),
    CASE WHEN @days::integer > 0 THEN now() + make_interval(days => @days::integer) END
);

-- Switched off, never deleted: the dismissal cookie is keyed on the id, so a new
-- row with the same copy would reappear for everybody who had closed it.
-- name: SetBannerActive :execrows
UPDATE promo_banners SET is_active = @is_active::boolean WHERE id = @banner_id;

-- name: AdminFAQEntries :many
SELECT id, category, question, answer,
       coalesce(category_en, '') AS category_en,
       coalesce(question_en, '') AS question_en,
       coalesce(answer_en, '') AS answer_en,
       position, updated_at
FROM faq_entries
ORDER BY category, position, id
LIMIT $1;

-- The position is computed WITHIN the category, because faq_entries_position_key
-- is unique on (category, position).
-- name: CreateFAQEntry :exec
INSERT INTO faq_entries (category, question, answer,
                         category_en, question_en, answer_en, position)
VALUES (@category::text, @question::text, @answer::text,
        nullif(@category_en::text, ''), nullif(@question_en::text, ''),
        nullif(@answer_en::text, ''),
        coalesce((SELECT max(f.position) FROM faq_entries f
                  WHERE f.category = @category::text), 0) + 1);

-- The CATEGORY is not editable: moving an entry between categories has to
-- renumber its position, and a form that silently collides with
-- faq_entries_position_key is worse than one that does not offer the move.
-- name: UpdateFAQEntry :execrows
UPDATE faq_entries
SET question = @question::text, answer = @answer::text,
    question_en = nullif(@question_en::text, ''),
    answer_en = nullif(@answer_en::text, ''),
    category_en = nullif(@category_en::text, '')
WHERE id = @entry_id;

-- name: DeleteFAQEntry :execrows
DELETE FROM faq_entries WHERE id = @entry_id;

-- The caller writes the first VERSION in the same transaction: a method with no
-- version is one the checkout finds and cannot price. Zero on a parcel ceiling
-- means "no stated limit" and stores NULL, the honest default for home delivery.
-- name: CreateShippingMethod :one
INSERT INTO shipping_methods (code, destination_kind, position,
                              max_parcel_longest_mm, max_parcel_sum_mm, max_parcel_weight_g)
VALUES (@code::text, @destination_kind::text,
        coalesce((SELECT max(position) FROM shipping_methods), 0) + 1,
        nullif(@max_parcel_longest_mm::integer, 0),
        nullif(@max_parcel_sum_mm::integer, 0),
        nullif(@max_parcel_weight_g::integer, 0))
RETURNING id;

-- name: SetShippingMethodActive :execrows
UPDATE shipping_methods SET is_active = @is_active::boolean WHERE id = @method_id;

-- name: CreateShippingZone :one
INSERT INTO shipping_zones (code, name, name_en, position)
VALUES (@code::text, @name::text, nullif(@name_en::text, ''),
        coalesce((SELECT max(position) FROM shipping_zones), 0) + 1)
RETURNING id;

-- Serializes whole-set edits for one zone. Without this, two forms can each
-- sweep against the other's partial work and commit a union neither submitted.
-- name: LockShippingZone :one
SELECT id FROM shipping_zones WHERE id = @zone_id FOR UPDATE;

-- prefix is the PRIMARY KEY, so a postal code belongs to exactly one zone by
-- construction and moving one is an upsert rather than an insert.
-- name: AssignZonePrefix :exec
INSERT INTO shipping_zone_prefixes (prefix, zone_id)
VALUES (@prefix::text, @zone_id)
ON CONFLICT (prefix) DO UPDATE SET zone_id = @zone_id;

-- The field carries this zone's WHOLE set. Scoped by zone_id so one zone's
-- stale form cannot sweep a prefix that has since moved to another zone.
-- name: RemoveZonePrefixesExcept :execrows
DELETE FROM shipping_zone_prefixes
WHERE zone_id = @zone_id
  AND NOT (prefix = ANY(coalesce(@keep::text[], '{}'::text[])));

-- Decided by the DELETE's own WHERE clause, like DeleteBrand.
-- name: DeleteShippingZone :execrows
DELETE FROM shipping_zones z
WHERE z.id = @zone_id
  AND NOT EXISTS (SELECT 1 FROM shipping_zone_prefixes p WHERE p.zone_id = z.id)
  AND NOT EXISTS (SELECT 1 FROM shipping_version_zones v WHERE v.zone_id = z.id);

-- source_id is a bare uuid with no foreign key — it points at whichever table
-- source_type names — so each join is guarded by that discriminator. A HOLD
-- points at the reservation, because it is taken before the order exists.
-- name: VariantMovements :many
SELECT m.created_at, m.delta, m.reason, m.source_type,
       coalesce(o.order_number, ro.order_number, '') AS order_number,
       coalesce(u.full_name, u.email, '') AS actor,
       (SELECT sum(e.delta) FROM inventory_movements e
        WHERE e.variant_id = m.variant_id AND e.id <= m.id)::integer AS running_total
FROM inventory_movements m
JOIN product_variants pv ON pv.id = m.variant_id
LEFT JOIN users u ON u.id = m.actor_user_id
LEFT JOIN orders o ON m.source_type = 'order' AND o.id = m.source_id
LEFT JOIN inventory_reservations r
       ON m.source_type = 'reservation' AND r.id = m.source_id
LEFT JOIN orders ro ON ro.id = r.order_id
WHERE pv.sku = @sku::text
ORDER BY m.id DESC
LIMIT @row_limit::integer;

-- reason 'receipt' and not 'adjustment', which is the whole of it: goods a shop
-- bought must be distinguishable in its own ledger from a corrected miscount.
-- There is no source_id, because goen has no purchasing table to point at.
-- name: ReceiveStock :exec
SELECT record_inventory_movement(
    @variant_id, @delta::integer, 'receipt',
    @idempotency_key::text, 'admin', NULL, @actor_user_id::uuid
);

-- EXACT on both, told apart by the caller: a prefix would widen the answer
-- without widening what the person on the phone can tell you. LEFT JOIN on the
-- user, because user_id is ON DELETE SET NULL and erase_user leaves the row.
-- name: AdminSearchWarranties :many
SELECT w.id, w.unit_no, coalesce(w.serial_number, '') AS serial_number,
       w.registered_at, w.expires_on,
       (w.expires_on >= shop_today())::boolean AS in_force,
       ol.product_name, coalesce(ol.variant_label, '') AS variant_label,
       o.order_number, o.fulfillment_status,
       coalesce(u.full_name, '') AS customer_name,
       coalesce(u.email, '') AS customer_email
FROM warranty_registrations w
JOIN order_lines ol ON ol.id = w.order_line_id
JOIN orders o ON o.id = ol.order_id
LEFT JOIN users u ON u.id = w.user_id
WHERE w.serial_number = @term::text OR o.order_number = @term::text
ORDER BY w.expires_on DESC, w.id
LIMIT @row_limit::integer;

-- What has actually gone back to the customer on this order, so an allowance
-- form can default to it. A staff member typing a refund figure from memory is
-- how the wrong number reaches the 財政部.
-- What the 折讓 form offers, which must be what an allowance is allowed to
-- relieve: both sources, from the one view. Card-only defaulted the form to the
-- card half of a split refund, so the 統一發票 kept recording a reversed sale.
-- name: SettledRefundsForOrder :one
SELECT (card_cents + credit_cents)::bigint AS refunded_cents
FROM order_refunds
WHERE order_number = @order_number::text;

-- Staff explicitly confirmed every provider-side cent was refunded or already
-- represented by a succeeded payment. The function also terminates a linked
-- active payment, so safe release cannot leave a completed Session resumable.
-- name: ReleasePaymentEvent :one
SELECT release_payment_event(@event_id::text);

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

-- Staff have confirmed that this complete Session took no money, or that all
-- of it was refunded at Stripe. This is the only outcome that permits a later
-- Checkout generation; paid attribution has a separate capture path.
-- name: ReleaseCompletePayment :one
SELECT release_complete_payment(@provider_ref::text);
