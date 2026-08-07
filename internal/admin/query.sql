-- The order queue, newest first, optionally narrowed to one fulfilment state.
-- name: AdminOrders :many
SELECT
    o.id,
    o.order_number,
    o.fulfillment_status,
    o.placed_at,
    o.shipping_cents,
    o.discount_cents,
    o.tax_cents,
    coalesce(pd.recipient_name, '(已抹除)') AS recipient,
    coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
              WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
    order_is_committed(o.id) AS committed
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE (@status::text = '' OR o.fulfillment_status = @status::text)
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @row_limit::integer;

-- Find an order from whatever the customer said on the phone.
--
-- ONE box, and what it matches depends on what it looks like. A string shaped like
-- an order number is looked up exactly, on the unique index; anything else is a
-- PREFIX of the recipient's name or their address. Each path is index-backed, which
-- is the reason the shapes are told apart here rather than OR-ed together — a query
-- that tried all three at once with leading wildcards would scan order history on
-- every keystroke a staff member makes.
--
-- The term is bounded by the caller: below two characters this matches most of the
-- table, and a prefix that broad is a list rather than a search.
--
-- An erased order matches nothing, and nothing extra is needed for that: erase_user
-- sets the name and the address to NULL, so both comparisons are NULL.
-- name: AdminSearchOrders :many
SELECT
    o.id,
    o.order_number,
    o.fulfillment_status,
    o.placed_at,
    o.shipping_cents,
    o.discount_cents,
    o.tax_cents,
    coalesce(pd.recipient_name, '(已抹除)') AS recipient,
    coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
              WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
    order_is_committed(o.id) AS committed
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

-- name: AdminOrderByNumber :one
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
    -- The 發票 the customer asked for. Collected at checkout since payment shipped
    -- and read by NOTHING, so a staff member packing an order could not see whether
    -- it needed a 統編 invoice — data collected and never shown, which is a feature
    -- with no door from the other side.
    --
    -- Issuing is still not built (a real 統一發票 goes through a 加值中心), and that
    -- is exactly why showing it matters: until the integration exists, somebody
    -- issues these by hand, and they cannot do it from a table they cannot read.
    coalesce(ip.invoice_type, '') AS invoice_type,
    coalesce(ip.carrier_code, '') AS invoice_carrier,
    coalesce(ip.tax_id, '') AS invoice_tax_id,
    order_is_committed(o.id) AS committed
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
LEFT JOIN invoice_preferences ip ON ip.order_id = o.id
WHERE o.order_number = $1;

-- Move an order along its lifecycle.
--
-- The transition itself is validated by orders_check_transition, which knows
-- the state machine and refuses an illegal move — so this does not re-derive
-- it. cancelled_at and completed_at are set here because the schema requires
-- them for those two states and orders_history_frozen refuses a later change.
-- Stamp the parcels of an order that has just been marked delivered.
--
-- delivered_at was READ in two places and written in NONE: the order-level status
-- moved to 已送達 while every parcel row still said it was in transit, so the two
-- halves of the same fact disagreed and the customer's own page reads the parcel.
--
-- Only the ones with no stamp, so a re-run cannot move a date that has already
-- been recorded — and never earlier than shipped_at, which
-- order_shipments_delivered_after_shipped would refuse anyway: a clock cannot make
-- a parcel arrive before it left.
-- name: MarkShipmentsDelivered :exec
UPDATE order_shipments SET delivered_at = greatest(now(), shipped_at)
WHERE order_id = $1 AND delivered_at IS NULL;

-- name: AdvanceOrder :exec
UPDATE orders
SET fulfillment_status = @status::text,
    cancelled_at = CASE WHEN @status::text = 'cancelled' THEN now() ELSE cancelled_at END,
    completed_at = CASE WHEN @status::text = 'completed' THEN now() ELSE completed_at END
WHERE order_number = @order_number::text;

-- name: SetStaffNote :exec
UPDATE orders SET staff_note = $2 WHERE order_number = $1;

-- The variants a back office needs to see: what is low, what is off.
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

-- name: AdminVariantBySKU :one
-- price_cents is read for the audit trail's "before": a reprice recorded
-- without the price it replaced records the least interesting half of the fact.
SELECT pv.id, pv.sku, pv.stock_quantity, pv.safety_stock, pv.is_active,
       pv.price_cents, p.name AS product_name, p.slug
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.sku = $1;

-- Adjust stock through the ledger.
--
-- record_inventory_movement is the ONLY door: admin has no UPDATE on
-- stock_quantity, so a direct write is refused by the database rather than by
-- convention. Every adjustment therefore lands in inventory_movements with a
-- reason and an actor.
-- name: AdjustStock :exec
SELECT record_inventory_movement(
    @variant_id, @delta::integer, 'adjustment',
    @idempotency_key::text, 'admin', NULL, @actor_user_id::uuid
);

-- Put a returned unit back on the shelf.
--
-- reason 'return' rather than 'adjustment', which is the whole point: the ledger
-- had the reason, its delta-direction CHECK and its safety-stock exemption from
-- the day it was written, and NOTHING ever posted one — so goods coming back
-- were indistinguishable from a staff member correcting a miscount. A shop
-- reading /admin/stock/{sku} could see the number move and not why.
--
-- source_type/source_id point at the RETURN, so the ledger row answers "which
-- return put this back" the way a hold points at its order and a release at its
-- reservation. The idempotency key is per (request, line), so a resubmitted
-- inspection posts one movement.
--
-- sqlc.narg on the actor: inventory_movements.actor_user_id is nullable with a
-- foreign key, so a zero UUID is not "nobody" — it is a user id that does not
-- exist, and the FK would refuse it. NULL is how the ledger says a movement had
-- no human behind it.
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

-- What the dashboard leads with.
-- name: AdminSummary :one
SELECT
    (SELECT count(*) FROM orders WHERE fulfillment_status = 'pending')::bigint AS pending_orders,
    (SELECT count(*) FROM orders WHERE fulfillment_status = 'picking')::bigint AS picking_orders,
    (SELECT count(*) FROM product_variants
     WHERE is_active AND stock_quantity <= safety_stock)::bigint AS low_stock,
    (SELECT count(*) FROM products WHERE status = 'active')::bigint AS active_products,
    (SELECT count(*) FROM contact_messages WHERE handled_at IS NULL)::bigint AS open_messages;

-- Record a shipment. carrier and tracking_number both carry CHECKs requiring a
-- non-blank value, so a shipment with an empty tracking number is refused by the
-- schema rather than saved as a shipment nobody can follow.
-- name: CreateShipment :one
INSERT INTO order_shipments (order_id, carrier, tracking_number, estimated_delivery_on)
VALUES (@order_id, @carrier::text, @tracking_number::text, @estimated_delivery_on)
RETURNING id;

-- Settle the part of a hold that is actually going out in this parcel.
-- name: ConsumeReservationPartial :exec
SELECT consume_reservation_partial(@reservation_id, @quantity::integer);

-- What each order line still owes a dispatch, and which hold covers it.
--
-- The two questions are answered TOGETHER because a partial dispatch has to
-- reconcile them line by line: shipping two of three units settles two of the
-- three that line's variant holds, and reading the remaining quantities from one
-- query and the reservations from another leaves the two free to disagree about
-- an order somebody is editing.
--
-- LEFT JOIN on the reservation, not JOIN. A line whose variant was deleted has
-- no hold and never did, and dropping the row here would silently ship it
-- without anybody noticing the stock did not move — which is the shape the
-- empty-reservation dispatch had. The caller refuses instead.
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

-- ReleaseReservation and HeldReservationsForOrder are what a cancellation needs,
-- and they are defined in internal/cart/query.sql. sqlc generates ONE db package
-- for the whole module, so a second copy here is a duplicate-name error rather
-- than a second query — which is how this was found.

-- Append to an order's history. The table is append-only three ways — a
-- forbid_change trigger, REVOKE UPDATE and REVOKE DELETE — so this is the only
-- thing that may ever be done to it.
-- name: RecordOrderEvent :exec
INSERT INTO order_events (order_id, kind, note, actor_user_id)
VALUES (@order_id, @kind::text, @note, @actor_user_id);

-- name: OrderIDByNumber :one
SELECT id, fulfillment_status FROM orders WHERE order_number = $1;

-- An order's history, oldest first. occurred_at then id, because two events
-- recorded in the same statement share a timestamp and the uuidv7 primary key
-- is the tie-break that keeps them in the order they happened.
-- name: OrderEvents :many
SELECT e.kind, e.note, e.occurred_at, coalesce(u.full_name, '') AS actor_name
FROM order_events e
LEFT JOIN users u ON u.id = e.actor_user_id
WHERE e.order_id = $1
ORDER BY e.occurred_at, e.id;

-- name: OrderShipments :many
SELECT carrier, tracking_number, shipped_at, delivered_at, estimated_delivery_on
FROM order_shipments WHERE order_id = $1 ORDER BY shipped_at, id;

-- What a shipment contains. Written with the shipment, because a dispatch that
-- records no lines is one nothing can later reconcile against: a return has to
-- be bounded by what actually went out, not by what was ordered.
-- name: CreateShipmentLine :exec
INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
VALUES (@order_id, @shipment_id, @order_line_id, @quantity::integer);

-- The return queue. Undecided first, because that is the work.
-- name: ReturnQueue :many
SELECT r.id, r.status, r.reason, r.created_at, r.decided_at,
       o.order_number,
       (SELECT coalesce(sum(rl.quantity), 0) FROM return_request_lines rl
        WHERE rl.return_request_id = r.id)::integer AS units,
       -- The ONE definition, not a second copy of the arithmetic. The queue and
       -- the decision page used to compute this separately, and both were wrong
       -- the same two ways — a figure a staff member reads on one page and acts
       -- on from another must not be able to differ.
       return_refundable_amount(r.id)::bigint AS refundable_cents,
       -- Whether this request is a statutory rescission or a goodwill return,
       -- which the page could not tell apart and a staff member therefore could
       -- not either. 消保法 §19 I runs seven days from RECEIPT of the goods,
       -- 民法 §120 II excludes the day of receipt so day one is the day after,
       -- and §19 IV fixes the moment on the customer's SIDE — the request going
       -- out, not the shop reading it. So the comparison is created_at against
       -- delivered_at, both written by this database: one clock at both ends,
       -- which is the /admin/messages lesson.
       --
       -- Undelivered is neither answer. The window has not started, so nothing
       -- here is late; a return before the parcel lands is bounded by
       -- return_lines_within_purchase instead.
       (CASE
            WHEN d.delivered_at IS NULL THEN 'undelivered'
            WHEN r.created_at::date <= d.delivered_at::date + 7 THEN 'within'
            ELSE 'after'
        END)::text AS rescission_window
FROM return_requests r
JOIN orders o ON o.id = r.order_id
LEFT JOIN LATERAL (
    SELECT max(s.delivered_at) AS delivered_at
    FROM order_shipments s WHERE s.order_id = o.id
) d ON true
ORDER BY (r.status = 'requested') DESC, r.created_at DESC
LIMIT $1;

-- One return, with what it would cost to refund.
--
-- The amount comes from return_refundable_amount, never from anything the
-- request carried: a refund figure that came in on a form is the oldest hole
-- there is, and this one pays out real money. It is a FUNCTION rather than an
-- expression here because the queue needs the same number, and the two copies
-- this replaced were each wrong in the same two ways.
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

-- WHAT is being sent back, for every request on the page.
--
-- Takes an ARRAY rather than one id, so a queue of fifty returns is one query
-- and not fifty. It was written for a single request and never called at all,
-- so the shape was free to be the one the only caller needs.
--
-- Without it the queue said "3 件 · 可退 NT$4,500" and nothing else: a staff
-- member decided a return without being able to see what was in it.
-- name: ReturnLines :many
SELECT rl.return_request_id, ol.id AS order_line_id, ol.sku, ol.product_name,
       ol.variant_label, ol.unit_price_cents, rl.quantity,
       -- The inspection, NULL until somebody opens the parcel. "Not looked at
       -- yet" and "looked at, nothing arrived" are different facts and the form
       -- has to tell them apart: one is work outstanding, the other is a
       -- conversation with the customer.
       rl.received_quantity, rl.restocked_quantity,
       coalesce(rl.inspection_note, '')::text AS inspection_note,
       -- Whether the unit can go back on a shelf at all. A line whose variant was
       -- deleted, or which never had one, cannot be restocked however sellable it
       -- looks — order_lines.variant_id is nullable precisely so a line survives
       -- its variant, and the form must not offer a control the write would then
       -- refuse.
       (ol.variant_id IS NOT NULL)::boolean AS restockable
FROM return_request_lines rl
JOIN order_lines ol ON ol.id = rl.order_line_id
WHERE rl.return_request_id = ANY(@request_ids::uuid[])
ORDER BY rl.return_request_id, ol.position, ol.id;

-- Record what came back on one line of a return.
--
-- Scoped to an APPROVED request in its own WHERE clause, and :execrows so zero
-- means the caller is told rather than the write silently doing nothing: a
-- rejected return has no parcel coming, and inspecting one that was never
-- approved would put stock back for goods the shop refused to take.
--
-- ONCE, which is what `received_quantity IS NULL` is doing here. A parcel is
-- opened once, and the restock behind it posts an inventory movement keyed on
-- (request, line) — so a second inspection either double-restocks or is
-- swallowed by the unique index, and the swallowed one is worse: a staff member
-- who miscounted, corrected the figure and resubmitted would see the new number
-- on screen with the stock still at the old one. Refusing says so.
--
-- The correction path is the one that already exists and is already audited:
-- /admin/stock/{sku} posts an 'adjustment' with an actor, which is exactly what
-- a recount is. A second door into the same ledger is how the two come to
-- disagree.
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

-- The variant and quantity a restock has to post, read back from the inspection.
--
-- Read AFTER the inspection is written and inside the same transaction, so the
-- movement posted is the one this transaction recorded rather than whatever a
-- later read finds. Only lines that restocked something and still have a variant
-- to restock into.
--
-- The cast on variant_id is load-bearing: order_lines.variant_id is NULLABLE so
-- a line survives its variant being deleted, and the WHERE clause below excludes
-- the NULLs — but sqlc reads the column's declaration and not the predicate, so
-- without it every caller unwraps a NullUUID that can never be null. Cast, the
-- way localized_name's callers coalesce.
-- name: ReturnRestockLines :many
SELECT ol.variant_id::uuid AS variant_id,
       rl.restocked_quantity::integer AS quantity, rl.order_line_id
FROM return_request_lines rl
JOIN order_lines ol ON ol.id = rl.order_line_id
WHERE rl.return_request_id = @request_id
  AND rl.restocked_quantity > 0
  AND ol.variant_id IS NOT NULL
ORDER BY rl.order_line_id;

-- Close an inspected return.
--
-- return_requests_completed_is_inspected refuses this while any line is
-- un-inspected, so the WHERE clause here does not restate that rule — the
-- database is the one place it lives. `status = 'approved'` IS restated, for the
-- reason DecideReturn restates it: it is what makes two staff members closing
-- one return resolve to one winner.
-- name: CompleteReturn :execrows
UPDATE return_requests
SET status = 'completed', resolution = coalesce(nullif(@resolution::text, ''), resolution)
WHERE id = @id AND status = 'approved';

-- Decide a return. return_requests_recount guards the transition and
-- return_requests_decided_has_time requires the timestamp to arrive with it.
--
-- :execrows, because `status = 'requested'` in this WHERE clause is the ONLY
-- place the question is asked under a lock. Decide reads the row on the pool
-- BEFORE opening its transaction, so two staff members clicking 同意 and 不同意
-- on one request both pass that check; as :exec the loser updated zero rows,
-- SQL called it success, and it committed an audit row asserting a decision that
-- never happened — and, for an approval, after paying a refund. Zero rows is
-- "somebody decided this first", which is a sentence a caller can act on.
-- The SetProductStatus lesson, in the one place that also moves money.
-- name: DecideReturn :execrows
UPDATE return_requests
SET status = @status::text, resolution = @resolution, decided_at = now()
WHERE id = @id AND status = 'requested';

-- name: OpenRefund :one
-- nullif on the reason: "no note" is NULL, not an empty string.
SELECT open_refund(@payment_id, @request_key::text, @amount_cents::bigint,
                   nullif(@reason::text, ''), @return_request_id);

-- name: SettleRefund :exec
-- nullif again: a failed refund has no provider reference, and settle_refund
-- coalesces NULL onto whatever is already there rather than blanking it.
SELECT settle_refund(@request_key::text, nullif(@provider_ref::text, ''), @status::text);

-- How much is already claimed against a payment, so the back office can show
-- what is left rather than letting refunds_within_capture be the first time
-- anyone finds out.
--
-- It mirrors refunds_guard, and it has to: the trigger counts every refund on
-- the payment that is not 'failed' and not 'cancelled', EXCLUDING the row being
-- written. Two ways this had drifted from it, both of which make the back
-- office compute headroom the database will not honour.
--
-- 'requires_action' was missing from the list. A refund Stripe has accepted and
-- not settled was money goen believed it could still claim and the trigger did
-- not — so the refusal would arrive from a constraint at the end of a refund
-- instead of from splitRefund's own sentence at the start.
--
-- And the row belonging to THIS request_key is excluded, the way the trigger
-- excludes NEW.id. open_refund is idempotent on request_key, so a Decide
-- retried after a stalled provider call finds the row it wrote last time.
-- Counting that row made capturedRemaining zero, so the retry was refused with
-- ErrRefused before Stripe was ever called: the refund the two-transaction
-- design exists to make resumable could not be resumed by any door.
-- name: RefundedSoFar :one
SELECT coalesce(sum(amount_cents), 0)::bigint
FROM refunds
WHERE payment_id = @payment_id
  AND status IN ('pending', 'requires_action', 'succeeded')
  AND request_key <> @request_key::text;

-- Refunds that have not landed, for /admin/health.
--
-- This repository's own comments claimed that the refund row is committed
-- before the provider is called "so a crash between the two leaves something
-- reconciliation can find". NOTHING READ THAT ROW. The only query over `refunds`
-- was the arithmetic above, so an outstanding claim on real money was visible
-- to nobody — a table with no door, the shape product_specs and promo_banners
-- were each found in, except that this one holds money a customer is waiting
-- for.
--
-- 'failed' is listed beside the two outstanding states on purpose. It is
-- terminal at Stripe, which is exactly why a person has to see it: the goods
-- came back, the return did NOT close, and nobody has been paid.
--
-- OLDEST first, like /admin/questions and /admin/messages. A refund outstanding
-- for three days is more urgent than one opened this morning, and newest-first
-- buries it exactly as it becomes the one worth chasing.
-- name: OpenRefunds :many
SELECT r.request_key, r.status, r.amount_cents, r.created_at,
       coalesce(r.provider_ref, '')::text AS provider_ref,
       o.order_number
FROM refunds r
JOIN payments p ON p.id = r.payment_id
JOIN orders o ON o.id = p.order_id
WHERE r.status IN ('pending', 'requires_action', 'failed')
ORDER BY r.created_at
LIMIT $1;

-- A customer by email, for granting credit.
--
-- internal/account already defines UserByEmail for sign-in, and sqlc generates
-- one db package for the module, so this one is named for what it is FOR. It
-- also selects less: the back office has no business reading a password hash.
-- name: CustomerByEmail :one
SELECT id, email, coalesce(full_name, '') AS full_name FROM users
WHERE lower(email) = lower(@email::text);

-- What a customer's ledger comes to. Summed rather than stored, so it cannot
-- drift from the entries that justify it.
-- What the back office is about to add to or spend from. From the one view that
-- defines a balance — this was the FOURTH hand-written copy of the same sum.
-- name: CreditBalance :one
SELECT coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = $1), 0)::bigint;

-- A grant has no order behind it, and may have no actor if the posting came
-- from somewhere other than a staff member's form. sqlc reads a bare parameter
-- as non-nullable, so both are cast to make the nullability explicit.
-- name: PostStoreCredit :one
SELECT post_store_credit(@user_id, @amount_cents::bigint, @reason::text,
                         NULL::uuid, @idempotency_key::text,
                         sqlc.narg(actor_user_id)::uuid);

-- The most recent postings, so the back office can see what it has been doing.
-- name: RecentCredit :many
SELECT e.amount_cents, e.reason, e.created_at,
       coalesce(u.email, '(已刪除)') AS email
FROM store_credit_entries e
JOIN store_credit_accounts a ON a.id = e.account_id
LEFT JOIN users u ON u.id = a.user_id
ORDER BY e.created_at DESC, e.id DESC
LIMIT $1;

-- The catalogue as the back office sees it: every product whatever its status,
-- because draft and archived ones are exactly what needs managing.
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

-- The choices the product form offers. Both are small and rarely change, so
-- they are read whole rather than paged.
-- name: AdminBrands :many
SELECT id, name FROM brands ORDER BY name;

-- Categories as a flat list with their depth, so the form can indent them
-- rather than pretending the tree is flat. Ordered by the path from the root,
-- which is what puts a child directly under its parent.
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

-- Create a product. It is born a DRAFT: products_active_is_published requires a
-- published_at before a product may go active, and a product with no variants
-- has no price — publishing is its own decision, made once it is ready.
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

-- The English copy is set here too, and an empty box CLEARS it — the same rule the
-- category rename follows, and for the same reason: a shop that added a translation
-- must be able to take it back, and absence is the one state the column expresses.
-- name: UpdateProduct :exec
UPDATE products
SET brand_id = @brand_id, category_id = @category_id, name = @name::text,
    summary = nullif(@summary::text, ''), description = @description::text,
    name_en = nullif(@name_en::text, ''),
    summary_en = nullif(@summary_en::text, ''),
    description_en = nullif(@description_en::text, ''),
    warranty_note = nullif(@warranty_note::text, ''),
    -- Zero means "the shop has not stated a term", which is what NULL means in the
    -- column: registration is then REFUSED rather than given a default, because
    -- expires_on is NOT NULL and defaulting it would have goen invent a promise
    -- nobody made.
    warranty_months = nullif(@warranty_months::integer, 0)
WHERE slug = @slug::text;

-- Publish or unpublish.
--
-- published_at is stamped on the FIRST publish and kept afterwards: 本週新品 is
-- a query over it, so re-publishing an old product must not make it new again.
-- :execrows, not :exec. An UPDATE whose WHERE matches nothing is not an error
-- in SQL, so a status change against a slug that does not exist reported
-- success — to the staff member, and to the audit trail, which then held a row
-- saying a product had been published when no such product existed. The row
-- count is how the caller can tell the difference.
-- name: SetProductStatus :execrows
UPDATE products
SET status = @status::text,
    published_at = CASE
        WHEN @status::text = 'active' THEN coalesce(published_at, now())
        ELSE published_at
    END
WHERE slug = @slug::text;

-- Add a variant. stock_quantity is deliberately absent: the column is not in
-- admin's INSERT grant, so it takes DEFAULT 0 and stock arrives only through
-- record_inventory_movement.
-- The parcel measurements are collected here rather than left for later,
-- because they decide which shipping methods the CUSTOMER is offered: a variant
-- with no measurement is refused by no method, so an unmeasured monitor is
-- offered 超商取貨 and the shop finds out at the counter. Zero means unmeasured
-- and stores NULL — the form cannot express "I do not know" any other way.
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

-- Every coupon, with what it has actually done. The redemption count comes from
-- the ledger, never from a column: the ledger is what the limit is counted from
-- at checkout, and a second number here would be one that could disagree.
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
        sqlc.narg(ends_at)::timestamptz);

-- Switch a coupon off. Never deleted: coupon_redemptions references it, and a
-- promotion that ran is part of what past orders were charged.
-- name: SetCouponActive :execrows
UPDATE coupons SET is_active = @is_active::boolean WHERE upper(code) = upper(@code::text);

-- Every campaign, with what it features and whether it is on right now.
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

-- Feature a product.
--
-- sale_campaign_needs_discount refuses a product with nothing marked down, and
-- it takes a lock on the product first — so this is one statement and the guard
-- decides, rather than a check here that a concurrent price change invalidates.
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

-- What one campaign features, for its edit page.
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

-- The trail, newest first. Joined to users so a page shows a name rather than a
-- uuid — the actor is the whole reason this table exists.
-- name: AuditEvents :many
SELECT a.action, a.entity_table, a.entity_id, a.before, a.after,
       a.request_id, a.occurred_at,
       coalesce(u.full_name, u.email, '(已刪除的帳號)') AS actor
FROM audit_events a
LEFT JOIN users u ON u.id = a.actor_user_id
ORDER BY a.occurred_at DESC, a.id DESC
LIMIT $1;

-- Attach an uploaded image to a product.
--
-- The storage key is a media_objects digest, not a filename. No foreign key:
-- product_images predates media_objects and still holds embedded-asset names
-- from the seed, so the column carries two kinds of key. UnreferencedMedia is
-- what keeps the two consistent from the other direction.
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

-- What a product currently shows.
-- name: AdminProductImages :many
SELECT pi.storage_key, pi.alt_text, pi.width, pi.height
FROM product_images pi
JOIN products p ON p.id = pi.product_id
WHERE p.slug = @slug::text
ORDER BY pi.position, pi.id;

-- Every hero slide, with whether it is the one showing.
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

-- Move a slide to the front, which is how an editor chooses which one shows.
--
-- One statement touching ONE row: the promoted slide takes a position below
-- every other, rather than everything else shifting up. Shifting would rewrite
-- the whole table per promotion and grow position without bound; this rewrites
-- one row. Ties do not matter — CurrentHeroSlide orders by (position, id), so
-- the order is total either way.
-- name: PromoteHeroSlide :execrows
UPDATE hero_slides h
SET position = coalesce((SELECT min(o.position) FROM hero_slides o), 0) - 1
WHERE h.id = @id;

-- Brands, with how many products each carries.
--
-- The count is what makes deletion decidable: a brand with products cannot go,
-- and a page that offered the button anyway would be a button that always
-- fails.
-- name: ManagedBrands :many
SELECT b.id, b.slug, b.name,
       (SELECT count(*) FROM products p WHERE p.brand_id = b.id)::bigint AS products
FROM brands b
ORDER BY b.name;

-- name: CreateBrand :exec
INSERT INTO brands (slug, name) VALUES (@slug::text, @name::text);

-- name: RenameBrand :execrows
UPDATE brands SET name = @name::text WHERE slug = @slug::text;

-- Delete a brand nothing references.
--
-- The emptiness check is in the statement, not in Go: a product created between
-- a check and a delete would be orphaned — except products.brand_id is NOT NULL
-- with ON DELETE RESTRICT, so the database would refuse it anyway. This makes
-- the refusal a row count instead of a foreign-key error, which is the
-- difference between a sentence and a constraint name.
-- name: DeleteBrand :execrows
DELETE FROM brands b
WHERE b.slug = @slug::text
  AND NOT EXISTS (SELECT 1 FROM products p WHERE p.brand_id = b.id);

-- Categories as a tree, with each one's depth and product count.
--
-- Recursive, because the tree has no fixed depth and the back office shows it
-- indented. categories_acyclic is what makes the recursion terminate — without
-- it a cycle would make this query hang rather than return wrong rows.
-- name: ManagedCategories :many
WITH RECURSIVE tree AS (
    SELECT c.id, c.parent_id, c.slug, c.name, c.name_en, c.position, 0 AS depth,
           array[c.position, 0] AS path
    FROM categories c WHERE c.parent_id IS NULL
    UNION ALL
    SELECT c.id, c.parent_id, c.slug, c.name, c.name_en, c.position, t.depth + 1,
           t.path || array[c.position, 0]
    FROM categories c JOIN tree t ON t.id = c.parent_id
)
SELECT t.id, t.slug, t.name, coalesce(t.name_en, '') AS name_en,
       t.depth::integer AS depth,
       coalesce(p.name, '') AS parent_name,
       (SELECT count(*) FROM products x WHERE x.category_id = t.id)::bigint AS products,
       (SELECT count(*) FROM categories k WHERE k.parent_id = t.id)::bigint AS children
FROM tree t
LEFT JOIN categories p ON p.id = t.parent_id
ORDER BY t.path, t.name;

-- Create a category, optionally under a parent.
--
-- :execrows, and the parent resolved by a JOIN rather than a scalar subquery.
--
-- The first version wrote `(SELECT id FROM categories WHERE slug = @parent)`,
-- which yields NULL for a slug that does not exist — so naming a parent that
-- was not there created a ROOT category and reported success. The staff member
-- asked for one thing and silently got another, which is worse than a refusal.
--
-- The derived table has exactly one row when the parent exists, exactly one
-- (NULL) row when no parent was named, and NO rows when a parent was named and
-- not found. That last case inserts nothing, and the row count is how the
-- caller learns it.
-- name: CreateCategory :execrows
INSERT INTO categories (slug, name, name_en, parent_id, position)
SELECT @slug::text, @name::text, nullif(@name_en::text, ''), parent.id,
       coalesce((SELECT max(c.position) + 1 FROM categories c
                 WHERE c.parent_id IS NOT DISTINCT FROM parent.id), 0)
FROM (
    SELECT c.id FROM categories c WHERE c.slug = @parent_slug::text
    UNION ALL
    SELECT NULL::uuid WHERE @parent_slug::text = ''
) parent;

-- Rename the DISPLAY names, never the slug. A slug is in every URL a search engine
-- has indexed and goen has no redirect table.
--
-- The English name is set here too, and nullif('') is what lets it be CLEARED: an
-- empty box means "no translation", which is the one state the column expresses as
-- NULL. Without that, a shop could add an English name and never take it back.
-- name: RenameCategory :execrows
UPDATE categories SET name = @name::text, name_en = nullif(@name_en::text, '')
WHERE slug = @slug::text;

-- Delete a category with nothing in it and nothing under it.
-- name: DeleteCategory :execrows
DELETE FROM categories c
WHERE c.slug = @slug::text
  AND NOT EXISTS (SELECT 1 FROM products p WHERE p.category_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM categories k WHERE k.parent_id = c.id);

-- Revenue over a window, from COMMITTED orders only.
--
-- An order that was placed and never paid is not revenue, and counting it would
-- make every abandoned checkout look like a sale. order_is_committed is the one
-- place that decides what committed means — it counts a fully store-credited
-- order with no payment row, which "EXISTS a succeeded payment" would miss.
--
-- The total is recomputed from the lines plus the order's own shipping, tax and
-- discount, because orders carries no total column: the total IS the lines, and
-- a stored copy is a second answer waiting to disagree.
-- name: RevenueSince :one
SELECT
    count(*)::bigint AS orders,
    coalesce(sum(t.total), 0)::bigint AS revenue_cents,
    -- Integer division, so no float ever touches money, and coalesced because
    -- an empty window divides by zero. Whole cents: an average order value with
    -- fractions of a cent is not a number anybody can act on.
    (coalesce(sum(t.total), 0) / greatest(count(*), 1))::bigint AS average_cents
FROM (
    SELECT (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                      FROM order_lines ol WHERE ol.order_id = o.id), 0)
            - o.discount_cents + o.shipping_cents + o.tax_cents)::bigint AS total
    FROM orders o
    JOIN committed_orders c ON c.id = o.id
    WHERE o.placed_at >= now() - make_interval(days => @window_days::integer)
) t;

-- What sold, over a window.
--
-- By PRODUCT and not by variant: a shop owner asks "how is the Pixelight 9
-- doing", not "how is the 256GB black one doing". The variant breakdown is a
-- different question and would be a different report.
-- name: BestSellersSince :many
SELECT
    p.slug,
    p.name,
    coalesce(b.name, '') AS brand,
    sum(ol.quantity)::bigint AS units,
    sum(ol.unit_price_cents * ol.quantity)::bigint AS revenue_cents
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
JOIN product_variants pv ON pv.id = ol.variant_id
JOIN products p ON p.id = pv.product_id
JOIN committed_orders c ON c.id = o.id
LEFT JOIN brands b ON b.id = p.brand_id
WHERE o.placed_at >= now() - make_interval(days => @window_days::integer)
GROUP BY p.slug, p.name, b.name
ORDER BY units DESC, revenue_cents DESC
LIMIT @limit_to::integer;

-- Checkout completion: orders placed against orders that became revenue.
--
-- NOT a conversion rate. goen collects no traffic data, so what fraction of
-- VISITORS bought is a number it cannot know — and presenting one would be
-- inventing it. This is the fraction of started orders that were paid for,
-- which is real and is the number a shop can act on.
-- name: CheckoutCompletionSince :one
SELECT
    count(*)::bigint AS placed,
    -- A LEFT JOIN and a CASE, not a per-row function call. Asked row by row
    -- this costs 106 ms over 14,000 orders; asked as a join it is 7.7 ms,
    -- because the planner can turn a join into a merge and cannot turn a
    -- function call into anything.
    coalesce(sum(CASE WHEN c.id IS NOT NULL THEN 1 ELSE 0 END), 0)::bigint AS committed
FROM orders o
LEFT JOIN committed_orders c ON c.id = o.id
WHERE o.placed_at >= now() - make_interval(days => @window_days::integer);

-- Stock about to run out on something that is selling.
--
-- Velocity AND level together, which is the only way the question is useful: a
-- variant with two left that sells one a month is fine, and one with twenty
-- left that sells fifty a week is the emergency. Ordered by days of cover, so
-- the top of the list is what runs out first.
-- name: StockAtRisk :many
SELECT
    pv.sku,
    p.name AS product_name,
    p.slug,
    pv.stock_quantity,
    pv.safety_stock,
    sold.units::bigint AS units_sold,
    -- Days of cover at the recent rate.
    --
    -- Never NULL, because the WHERE below admits only variants that sold
    -- something — so the divisor is never zero and there is no unknowable case
    -- to render. The first version guarded against a NULL that could not
    -- happen, and sqlc typed the column non-nullable anyway, which would have
    -- been a scan error the day the guard mattered.
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

-- The questions waiting for the shop, oldest first.
--
-- Oldest FIRST, unlike every other back-office list: a question that has been
-- waiting three days is more urgent than one asked this morning, and newest-
-- first would bury it exactly as it becomes worth answering.
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

-- What the background workers have and have not done.
--
-- ONE query, because these are read together and separately they would be four
-- round trips to answer one question — "is anything wrong". Every figure is a
-- COUNT or an AGE, never a status somebody has to keep updated: a health signal
-- derived from the work itself cannot say "fine" while the work is not being
-- done.
-- name: WorkerHealth :one
SELECT
    -- Undelivered messages, and the oldest one's age. A backlog that is
    -- growing and a backlog that is old are different problems: the first is a
    -- worker too slow, the second is a worker stopped.
    (SELECT count(*) FROM outbox_messages
     WHERE delivered_at IS NULL)::bigint AS outbox_pending,
    -- Measured from available_at, which is when the message became DUE — not
    -- when it was written. The claim pushes available_at forward by a lease and
    -- the backoff pushes it further, so "overdue" is exactly the number that
    -- says delivery is not keeping up. A negative value means everything due is
    -- in the future, which is healthy, so it floors at zero.
    (SELECT greatest(coalesce(extract(epoch FROM now() - min(available_at)), 0), 0)
     FROM outbox_messages WHERE delivered_at IS NULL)::bigint AS outbox_oldest_seconds,
    -- Messages that have exhausted their attempts. These never resolve on
    -- their own — the worker has given up — so one is worth a person's time.
    (SELECT count(*) FROM outbox_messages
     WHERE delivered_at IS NULL AND attempts >= @max_attempts::integer)::bigint AS outbox_stuck,
    -- Reservations past their expiry that the sweeper has not released. A
    -- handful is normal between ticks; a growing number is a sweeper that
    -- stopped, and every one of them is stock nobody can buy.
    (SELECT count(*) FROM inventory_reservations
     WHERE state = 'held' AND expires_at < now())::bigint AS expired_holds,
    -- How stale the co-purchase projection is.
    --
    -- Two columns and not one nullable age, because "never rebuilt" and
    -- "rebuilt just now" are different facts that a single number collapses —
    -- and because max() over an empty table is NULL, which sqlc infers as a
    -- non-nullable bigint and pgx then refuses to scan. The bug would have
    -- appeared on exactly one deployment: a fresh one.
    (SELECT coalesce(extract(epoch FROM now() - max(computed_at)), 0)
     FROM product_copurchases)::bigint AS copurchase_age_seconds,
    EXISTS (SELECT 1 FROM product_copurchases) AS copurchase_ever_built,
    -- Sessions past their expiry that the pruner has not deleted. They are
    -- already nobody — every read enforces expiry in its own WHERE clause — so
    -- this is not a correctness signal. It is the table growing without bound,
    -- and each row holds the user id it belonged to.
    (SELECT count(*) FROM sessions WHERE expires_at <= now())::bigint AS expired_sessions,
    -- Uploads nothing points at, past their grace period. The same shape: not
    -- wrong, just never reclaimed — and these are image bytes in PostgreSQL,
    -- which is the whole cost of the storage decision paid for nothing.
    (SELECT count(*) FROM media_objects m
     WHERE NOT EXISTS (SELECT 1 FROM product_images p WHERE p.storage_key = m.digest)
       AND NOT EXISTS (SELECT 1 FROM hero_slides h WHERE h.image_key = m.digest)
       AND m.created_at < now() - interval '24 hours')::bigint AS unreferenced_media;

-- Who to tell that an order shipped, and in which language.
--
-- The locale comes off the ORDER, never off the staff member who pressed Ship.
-- Reading it from the request would send a Taiwanese shopkeeper's language to an
-- English customer.
-- name: ShipmentRecipient :one
SELECT coalesce(pd.email, '') AS email,
       coalesce(pd.recipient_name, '') AS recipient_name,
       o.locale
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.id = $1;

-- Claim every pending restock notice for a variant that is back in stock.
--
-- The claim and the enqueue are ONE transaction, so notified_at means "the
-- outbox has this" rather than "an email was sent" — which is the honest
-- reading, because the outbox is what guarantees delivery from there. Claiming
-- without enqueuing would tell nobody and never try again.
--
-- RETURNING drives the enqueue, so the set claimed is exactly the set told.
--
-- The threshold is the same one the listing calls "in stock":
-- stock_quantity > safety_stock. Telling somebody about a unit the shop will
-- not sell them is worse than not telling them.
-- name: ClaimRestockNotices :many
UPDATE stock_notifications sn SET notified_at = now()
WHERE sn.variant_id = $1
  AND sn.notified_at IS NULL
  AND EXISTS (SELECT 1 FROM product_variants pv
              WHERE pv.id = sn.variant_id
                AND pv.is_active
                AND pv.stock_quantity > pv.safety_stock)
RETURNING sn.id, sn.email, sn.locale;

-- What a restock notice has to say: which product, and where to find it.
-- The product a restock notice is about, named in the RECIPIENT's language.
--
-- The letter's words already followed stock_notifications.locale and the product
-- name did not, so an English subscriber got an English letter about 保護殼 — the
-- half-translated failure the locale work exists to stop, arriving where nobody
-- would see it in review.
--
-- Called once per distinct locale in the claimed set rather than once per recipient:
-- there are two locales and there can be dozens of subscribers.
-- name: RestockSubject :one
SELECT p.slug,
       localized_name(p.name, p.name_en, @locale::text) AS product_name,
       pv.sku
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.id = @variant_id;

-- Every shipping method with the version currently in force, and what it
-- charges extra for.
--
-- DISTINCT ON the method, ordered by effective_at DESC: the versions table is
-- append-only, so "the current fee" is the newest row that has taken effect and
-- never the only row.
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

-- The surcharges on one version, for the page to list under it.
-- name: AdminVersionZones :many
SELECT z.id AS zone_id, z.code, z.name, vz.surcharge_cents
FROM shipping_version_zones vz
JOIN shipping_zones z ON z.id = vz.zone_id
WHERE vz.version_id = ANY(@version_ids::uuid[])
ORDER BY z.position, z.name;

-- Publish a new version of a shipping method.
--
-- An INSERT and never an UPDATE: shipping_method_versions_append_only refuses
-- one, and the reason is that every past order names the version it was priced
-- from. Editing a fee would rewrite what a customer was charged last month.
-- name: PublishShippingVersion :one
INSERT INTO shipping_method_versions (method_id, name, carrier, name_en, carrier_en,
                                      fee_cents, free_over_cents)
VALUES (@method_id, @name, nullif(@carrier::text, ''),
        nullif(@name_en::text, ''), nullif(@carrier_en::text, ''),
        @fee_cents, nullif(@free_over_cents, 0))
RETURNING id;

-- Carry a method's zone surcharges onto a newly published version.
--
-- Without this, publishing a new base fee silently drops every surcharge: the
-- rows key on the VERSION, and the new version has none. A shop that raised
-- 宅配 from NT$80 to NT$100 would start shipping to 金門 for NT$100 — under-
-- charging exactly where it was already losing money.
--
-- Copied from the version that was in force, which is what the staff member
-- was looking at when they typed the new fee. Changing the base rate is not a
-- statement about zones.
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

-- Set what one version charges for one zone.
--
-- ON CONFLICT so the form is idempotent: a staff member who submits twice has
-- set one surcharge, not failed the second time.
-- name: SetZoneSurcharge :exec
INSERT INTO shipping_version_zones (version_id, zone_id, surcharge_cents)
VALUES (@version_id, @zone_id, @surcharge_cents)
ON CONFLICT (version_id, zone_id) DO UPDATE
SET surcharge_cents = EXCLUDED.surcharge_cents;

-- Remove a surcharge. Absence is what "no surcharge" means — the lookup
-- coalesces a missing row to zero — so clearing is a DELETE and not a zero.
-- name: ClearZoneSurcharge :execrows
DELETE FROM shipping_version_zones WHERE version_id = $1 AND zone_id = $2;

-- The 會員等級 bands, and how many customers are in each.
--
-- The count is derived like the tier is: nothing stores which band a customer
-- is in, so "how many are in 金卡" is a question about their orders.
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

-- Retiring a band. A DELETE and not a flag, because a tier nobody is in is not
-- history: no order references it, and the customers who were in it are simply
-- re-derived into whichever band they now qualify for.
-- name: DeleteMembershipTier :execrows
DELETE FROM membership_tiers WHERE id = $1;

-- Correct an order's delivery details before the parcel leaves.
--
-- A customer who typed the wrong street has no way to fix it and, until this
-- existed, neither did the shop: the only option was to cancel and re-order,
-- which loses the payment and the stock hold with it.
--
-- The state guard is in the WHERE clause, not read first. Once an order is
-- shipped the parcel has gone, and rewriting the address then makes the record
-- lie about where it went — which is worse than not being able to change it.
--
-- Both destination groups are written and exactly one survives, the same way
-- the insert does it. order_private_data_one_destination refuses anything else,
-- and the CALLER decides which half to blank from the order's own shipping
-- method rather than from the form.
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

-- What an order collects, so the edit form asks for the right half.
-- name: OrderDestinationKind :one
SELECT sm.destination_kind, o.fulfillment_status
FROM orders o
JOIN shipping_method_versions v ON v.id = o.shipping_version_id
JOIN shipping_methods sm ON sm.id = v.method_id
WHERE o.order_number = $1;

-- The review queue, newest first.
--
-- Newest first, unlike /admin/questions which is oldest first. A question
-- waiting three days is more urgent than one asked this morning because it is
-- owed an answer; a review is owed nothing, and what a shop wants to see is
-- what has just appeared on its product pages.
--
-- The BASE table, so hidden reviews are listed too — un-hiding one is not
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

-- Hide a review, which takes it out of the list AND out of the score.
-- name: HideReview :execrows
UPDATE product_reviews SET hidden_at = now()
WHERE id = $1 AND hidden_at IS NULL;

-- Put one back. Hiding is reversible because moderation is a judgement, and a
-- judgement made in a hurry is one somebody should be able to undo.
-- name: ShowReview :execrows
UPDATE product_reviews SET hidden_at = NULL
WHERE id = $1 AND hidden_at IS NOT NULL;

-- The customer-service inbox, OLDEST first.
--
-- Oldest first for the reason /admin/questions is: somebody who wrote in three
-- days ago is more urgent than somebody who wrote this morning, and newest-first
-- buries them exactly as they stop being answerable in time.
--
-- Unhandled ahead of handled, so the queue is work rather than an archive.
-- contact_messages_unhandled_idx is the partial index this was designed around
-- and nothing had used.
-- name: AdminMessages :many
--
-- waiting_days is computed HERE, by the database's clock, because created_at is
-- written by the database's clock. Go was subtracting one from the other: a
-- container milliseconds ahead of its host made a message inserted exactly four
-- days ago report three, which is the coupon-window lesson at the other end of the
-- same comparison. A whole-day figure is presentation, but the arithmetic under it
-- is not, and two clocks cannot be subtracted.
SELECT id, name, email, subject, coalesce(order_ref, '') AS order_ref,
       message, handled_at, created_at,
       floor(extract(epoch FROM now() - created_at) / 86400)::integer AS waiting_days
FROM contact_messages
ORDER BY (handled_at IS NOT NULL), created_at
LIMIT $1;

-- Mark a message dealt with.
-- name: HandleMessage :execrows
UPDATE contact_messages SET handled_at = now()
WHERE id = $1 AND handled_at IS NULL;

-- Put one back in the queue. Marking something handled by mistake is the
-- ordinary kind of mistake, and a queue you cannot correct is one people stop
-- trusting.
-- name: ReopenMessage :execrows
UPDATE contact_messages SET handled_at = NULL
WHERE id = $1 AND handled_at IS NOT NULL;

-- Give back store credit spent on an order the back office is cancelling.
--
-- The same door the customer's own cancellation uses. Defined in
-- internal/cart/query.sql — sqlc builds ONE db package for the module, so
-- ReverseOrderCredit is written once and called from both.

-- How much of an order was paid with store credit, and how much of that has
-- already been given back.
--
-- Two figures rather than one net number, because they answer different
-- questions: what CAN still be returned is the difference, and a reader
-- reconciling a return needs to see both sides of it.
--
-- Signs as the ledger stores them: a spend is negative, a compensation positive.
-- name: OrderCreditPosition :one
SELECT
    coalesce(-sum(amount_cents) FILTER (WHERE amount_cents < 0), 0)::bigint AS spent,
    coalesce(sum(amount_cents) FILTER (WHERE amount_cents > 0), 0)::bigint  AS returned
FROM store_credit_entries
WHERE order_id = $1;

-- Give part of a return back as store credit.
--
-- A NEW POSITIVE entry rather than a reversal of the spend, which is what the
-- schema has always prescribed for an order that has shipped: a reversal un-funds
-- the order, and this order was paid for and went out. The compensation carries
-- the order id so the ledger says which return it belongs to.
--
-- Idempotent on the return: a retried decision compensates once.
-- name: CompensateReturnWithCredit :one
SELECT post_store_credit(
    @user_id, @amount_cents::bigint, @reason::text, @order_id,
    'return-credit:' || @return_id::text, sqlc.narg(actor)::uuid
)::uuid AS entry_id;

-- Find a customer from whatever the shop was told.
--
-- The same shape as the order search and for the same reasons: a PREFIX of the
-- address or the name, each index-backed, and a floor on the term enforced by the
-- caller. Told apart from an exact match is unnecessary here — an email IS the
-- prefix somebody gives you in full.
--
-- Only real accounts. An erased customer's row is gone (erase_user DELETEs it), so
-- nothing extra is needed for that.
-- name: AdminSearchCustomers :many
SELECT u.id, u.email, coalesce(u.full_name, '') AS full_name, u.created_at,
       (u.email_verified_at IS NOT NULL)::boolean AS verified,
       (SELECT count(*) FROM orders o WHERE o.user_id = u.id)::bigint AS orders
FROM users u
-- Searched across every role, for the reason AdminCustomer takes no role
-- predicate: a promoted customer is still the person who placed those orders.
WHERE (lower(u.email) LIKE lower(@term::text) || '%'
       OR u.full_name LIKE @term::text || '%')
ORDER BY u.created_at DESC
LIMIT @row_limit::integer;

-- One customer, as the back office needs to see them.
--
-- Everything about a person in ONE read: who they are, whether the address has been
-- proved, what they have spent, and what the shop owes them. Each of these existed
-- somewhere already and nothing brought them together — so answering "what is going
-- on with this customer" meant three pages and a guess.
-- name: AdminCustomer :one
SELECT u.id, u.email, coalesce(u.full_name, '') AS full_name,
       coalesce(u.phone, '') AS phone, u.created_at,
       (u.email_verified_at IS NOT NULL)::boolean AS verified,
       (SELECT count(*) FROM orders o WHERE o.user_id = u.id)::bigint AS orders,
       -- Spend counts COMMITTED orders only: a cancelled order is not money the
       -- shop took, and treating it as spend is the defect committed_orders was
       -- split out to stop.
       coalesce((SELECT sum(o.subtotal + o.shipping_cents + o.tax_cents - o.discount_cents)
                 FROM (SELECT o.id, o.shipping_cents, o.tax_cents, o.discount_cents,
                              coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                                        FROM order_lines ol WHERE ol.order_id = o.id), 0) AS subtotal
                       FROM orders o WHERE o.user_id = u.id
                         AND o.id IN (SELECT id FROM committed_orders)) o), 0)::bigint AS spent,
       -- Both balances come from the VIEWS that define them, never re-summed
       -- here. The first cut of this query wrote out both sums and got the points
       -- one subtly wrong — it kept an award with a NULL expiry, which
       -- loyalty_entries_expiry_matches_sign forbids anyway, so the two agreed by
       -- luck. A back office showing a customer a different balance from the one
       -- their own account page shows is the failure this avoids.
       coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = u.id), 0)::bigint AS credit_cents,
       coalesce((SELECT lb.points FROM loyalty_balances lb
                 JOIN store_credit_accounts a ON a.id = lb.account_id
                 WHERE a.user_id = u.id), 0)::bigint AS points
FROM users u
-- No role predicate, and that is deliberate. /admin/staff PROMOTES an existing
-- customer, which moves their role and leaves every order they have placed where
-- it was: a `role = 'customer'` filter here would make a colleague's own order
-- history unreachable from the one page built to answer questions about it.
WHERE u.id = $1;

-- A customer's orders, newest first.
-- name: AdminCustomerOrders :many
SELECT o.order_number, o.fulfillment_status, o.placed_at,
       o.shipping_cents, o.discount_cents, o.tax_cents,
       coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                 WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents
FROM orders o
WHERE o.user_id = $1
ORDER BY o.placed_at DESC
LIMIT $2;

-- ---------------------------------------------------------------------------
-- Product specs
--
-- 規格 is the whole promise of a 選品店 — /compare exists to put two of them side
-- by side — and until this section the table was written by the dev seed and by
-- nothing else. The back office could create a product, price it, photograph it
-- and publish it, and the comparison table for it was empty.
-- ---------------------------------------------------------------------------

-- name: AdminProductSpecs :many
SELECT s.id, s.label, s.value,
       coalesce(s.label_en, '') AS label_en, coalesce(s.value_en, '') AS value_en,
       s.position
FROM product_specs s
JOIN products p ON p.id = s.product_id
WHERE p.slug = $1
ORDER BY s.position, s.label;

-- Append a spec at the end.
--
-- The position is computed IN the insert, from max(position) under the row lock
-- the insert takes on the index — product_specs_position_key is unique on
-- (product_id, position), so reading the maximum in Go and then writing it is a
-- race two staff members editing one product would meet.
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


-- ---------------------------------------------------------------------------
-- Product options
--
-- 顏色 / 容量 and their values. Read by the PDP's variant picker, by the cart line
-- and by the facets — and written, until this section, by the dev seed and by
-- nothing else. A shop creating its own product could give it variants but no way
-- to tell them apart: the picker had nothing to pick.
-- ---------------------------------------------------------------------------

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

-- Append an option to a product.
-- name: AddProductOption :one
INSERT INTO product_options (product_id, name, name_en, position)
SELECT p.id, @name::text, nullif(@name_en::text, ''),
       coalesce((SELECT max(o.position) FROM product_options o
                 WHERE o.product_id = p.id), 0) + 1
FROM products p
WHERE p.slug = @slug::text
RETURNING id;

-- Append a value to one of a product's options.
--
-- product_id comes from the OPTION rather than from the caller, so a value cannot
-- be attached to an option of a different product — the composite foreign key
-- would refuse it, and reading it from the row means the caller cannot try.
-- name: AddProductOptionValue :one
INSERT INTO product_option_values (product_id, option_id, value, value_en, position)
SELECT o.product_id, o.id, @value::text, nullif(@value_en::text, ''),
       coalesce((SELECT max(v.position) FROM product_option_values v
                 WHERE v.option_id = o.id), 0) + 1
FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = @slug::text AND o.id = @option_id
RETURNING id;

-- How many options a product declares. A variant must name a value for each one,
-- or the picker cannot resolve it.
-- name: ProductOptionCount :one
SELECT count(*)::bigint FROM product_options o
JOIN products p ON p.id = o.product_id
WHERE p.slug = $1;

-- Attach a variant to one option value.
--
-- Every id is resolved from the SKU and the value id in this statement, so nothing
-- crosses products: variant_option_values carries product_id precisely so the
-- composite keys can enforce that, and reading it here means a caller cannot
-- present a mismatched pair to be checked.
-- name: SetVariantOptionValue :execrows
INSERT INTO variant_option_values (product_id, variant_id, option_id, option_value_id)
SELECT pv.product_id, pv.id, v.option_id, v.id
FROM product_variants pv
JOIN product_option_values v
  ON v.product_id = pv.product_id AND v.id = @option_value_id
WHERE pv.sku = @sku::text;

-- The values a variant carries, for the back office's variant list.
-- name: AdminVariantOptionValues :many
SELECT pv.sku, o.name AS option_name, v.value
FROM variant_option_values vov
JOIN product_variants pv ON pv.id = vov.variant_id
JOIN product_options o ON o.id = vov.option_id
JOIN product_option_values v ON v.id = vov.option_value_id
JOIN products p ON p.id = pv.product_id
WHERE p.slug = $1
ORDER BY pv.sku, o.position, o.id;


-- ---------------------------------------------------------------------------
-- The promotional strip
--
-- promo_banners had NO door. The strip is documented as a feature the shop runs,
-- middleware decides which paths carry it, dismissing one writes a cookie keyed on a
-- digest of its id — and the only way to create one was SQL. The layout check seeds
-- one with psql, which is the tell: a fixture that has to reach past the application
-- is a fixture for a feature with no entrance.
-- ---------------------------------------------------------------------------

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

-- Create one. The CTA is both-or-neither, which promo_banners_cta_complete also says.
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

-- Switch one off rather than delete it: a promotion that ran is part of what the
-- storefront said, the same reason a coupon is switched off.
-- name: SetBannerActive :execrows
UPDATE promo_banners SET is_active = @is_active::boolean WHERE id = @banner_id;


-- ---------------------------------------------------------------------------
-- The FAQ
--
-- CLAUDE.md says /faq reads faq_entries "so support can answer a recurring question
-- WITHOUT A DEPLOY". That was not true: nothing could write the table. The promise
-- was the design intent and the door was never built — the third feature this sweep
-- found in that state, after product_specs and promo_banners.
-- ---------------------------------------------------------------------------

-- name: AdminFAQEntries :many
SELECT id, category, question, answer,
       coalesce(category_en, '') AS category_en,
       coalesce(question_en, '') AS question_en,
       coalesce(answer_en, '') AS answer_en,
       position, updated_at
FROM faq_entries
ORDER BY category, position, id
LIMIT $1;

-- Append an entry to a category.
--
-- The position is computed IN the insert from max(position) WITHIN that category,
-- because faq_entries_position_key is unique on (category, position) — two staff
-- members adding to the same category would otherwise both read the same maximum.
-- name: CreateFAQEntry :exec
INSERT INTO faq_entries (category, question, answer,
                         category_en, question_en, answer_en, position)
VALUES (@category::text, @question::text, @answer::text,
        nullif(@category_en::text, ''), nullif(@question_en::text, ''),
        nullif(@answer_en::text, ''),
        coalesce((SELECT max(f.position) FROM faq_entries f
                  WHERE f.category = @category::text), 0) + 1);

-- Rewrite one. The CATEGORY is not editable here: moving an entry between
-- categories has to renumber its position, and a form that silently collides with
-- faq_entries_position_key is worse than one that does not offer the move.
-- name: UpdateFAQEntry :execrows
UPDATE faq_entries
SET question = @question::text, answer = @answer::text,
    question_en = nullif(@question_en::text, ''),
    answer_en = nullif(@answer_en::text, ''),
    category_en = nullif(@category_en::text, '')
WHERE id = @entry_id;

-- Delete one. A FAQ answer is not history: nothing references it, and an answer the
-- shop no longer stands behind should stop being on the page.
-- name: DeleteFAQEntry :execrows
DELETE FROM faq_entries WHERE id = @entry_id;


-- ---------------------------------------------------------------------------
-- Delivery methods and zones
--
-- shipping_methods and shipping_zones had no door either: /admin/shipping could
-- publish a new VERSION of a method the seed created, and set a surcharge for a zone
-- the seed created, and neither of the two things underneath. A shop could not offer
-- its third carrier, and could not say which postal codes cost more to reach.
-- ---------------------------------------------------------------------------

-- Create a method AND its first version, so a method that exists can be priced.
--
-- Two statements in the caller's transaction rather than one: a method with no
-- version is one the checkout finds and cannot price, which is worse than a method
-- that does not exist.
-- The parcel ceilings are the carrier's, and they are asked for HERE because a
-- method that has them and a method that does not are different offers. 超商取貨
-- is 45cm on the longest side, 105cm across three, 10kg — 萊爾富 5kg. Zero means
-- "no stated limit" and stores NULL, which is the honest default for 宅配.
-- name: CreateShippingMethod :one
INSERT INTO shipping_methods (code, destination_kind, position,
                              max_parcel_longest_mm, max_parcel_sum_mm, max_parcel_weight_g)
VALUES (@code::text, @destination_kind::text,
        coalesce((SELECT max(position) FROM shipping_methods), 0) + 1,
        nullif(@max_parcel_longest_mm::integer, 0),
        nullif(@max_parcel_sum_mm::integer, 0),
        nullif(@max_parcel_weight_g::integer, 0))
RETURNING id;

-- Switch a method off. Never a DELETE: shipping_method_versions references it with
-- ON DELETE RESTRICT and every past order names the version it was priced from, so a
-- method that ever carried a parcel is part of the record.
-- name: SetShippingMethodActive :execrows
UPDATE shipping_methods SET is_active = @is_active::boolean WHERE id = @method_id;

-- name: CreateShippingZone :one
INSERT INTO shipping_zones (code, name, name_en, position)
VALUES (@code::text, @name::text, nullif(@name_en::text, ''),
        coalesce((SELECT max(position) FROM shipping_zones), 0) + 1)
RETURNING id;

-- Give a zone a postal prefix.
--
-- prefix is the PRIMARY KEY of the table, so a prefix belongs to exactly one zone by
-- construction — "which zone is 880 in" cannot have two answers. Moving one is
-- therefore an upsert rather than an insert.
-- name: AssignZonePrefix :exec
INSERT INTO shipping_zone_prefixes (prefix, zone_id)
VALUES (@prefix::text, @zone_id)
ON CONFLICT (prefix) DO UPDATE SET zone_id = @zone_id;

-- Take a prefix out of every zone. Scoped to the zone in the DELETE's own WHERE
-- clause, so a stale form cannot remove a prefix that has since moved elsewhere.
-- name: RemoveZonePrefix :execrows
DELETE FROM shipping_zone_prefixes WHERE prefix = @prefix::text AND zone_id = @zone_id;

-- Delete a zone that nothing points at.
--
-- Decided by the DELETE's own WHERE clause rather than by a count read first, the
-- same way the taxonomy delete is: the foreign keys would refuse an orphaning delete
-- anyway, and doing it this way turns the refusal into a row count the page can
-- explain instead of a constraint name.
-- name: DeleteShippingZone :execrows
DELETE FROM shipping_zones z
WHERE z.id = @zone_id
  AND NOT EXISTS (SELECT 1 FROM shipping_zone_prefixes p WHERE p.zone_id = z.id)
  AND NOT EXISTS (SELECT 1 FROM shipping_version_zones v WHERE v.zone_id = z.id);


-- ---------------------------------------------------------------------------
-- The stock ledger, read
--
-- inventory_movements is the ledger every stock change goes through —
-- record_inventory_movement is the only writer of stock_quantity, which is what
-- makes "one writer" true rather than aspirational. And NOTHING read it. A shop
-- could see that a SKU has four units and could not see how it got there: which
-- sale, which return, which hand adjustment and by whom.
--
-- That is the same shape as a feature with no door, from the other side: data
-- collected and never shown.
-- ---------------------------------------------------------------------------

-- name: VariantMovements :many
SELECT m.created_at, m.delta, m.reason, m.source_type,
       coalesce(o.order_number, ro.order_number, '') AS order_number,
       coalesce(u.full_name, u.email, '') AS actor,
       (SELECT sum(e.delta) FROM inventory_movements e
        WHERE e.variant_id = m.variant_id AND e.id <= m.id)::integer AS running_total
FROM inventory_movements m
JOIN product_variants pv ON pv.id = m.variant_id
LEFT JOIN users u ON u.id = m.actor_user_id
-- The order a movement belongs to, when it has one. source_id is a bare uuid with
-- no foreign key — it points at whichever table source_type names — so the join is
-- guarded by that discriminator rather than by a constraint.
LEFT JOIN orders o ON m.source_type = 'order' AND o.id = m.source_id
-- A HOLD points at the reservation rather than the order, because the hold is taken
-- during checkout before the order exists. Reaching through it is what makes the two
-- units under a customer's unpaid order legible as that order rather than as a
-- reservation id nobody can look up.
LEFT JOIN inventory_reservations r
       ON m.source_type = 'reservation' AND r.id = m.source_id
LEFT JOIN orders ro ON ro.id = r.order_id
WHERE pv.sku = @sku::text
ORDER BY m.id DESC
LIMIT @row_limit::integer;
