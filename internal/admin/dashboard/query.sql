-- name: AdminSummary :one
-- Each desk is read once: its count and the age of its oldest item come from
-- the same rows, so the two cannot follow different rules. An age is how long
-- ago the oldest item began waiting, on the database's clock.
SELECT
    pending.pending_orders,
    ready.ready_orders,
    ready.ready_oldest_seconds,
    picking.picking_orders,
    picking.picking_oldest_seconds,
    stock.sold_out,
    messages.open_messages,
    messages.open_messages_oldest_seconds,
    requested.pending_returns,
    requested.pending_returns_oldest_seconds,
    uninspected.uninspected_returns,
    uninspected.uninspected_returns_oldest_seconds,
    questions.unanswered_questions,
    questions.unanswered_questions_oldest_seconds
FROM
    -- Genuinely UNPAID, not merely pending: an order funded by store credit or
    -- a full discount sits at pending for good, and counting it here sends
    -- somebody looking for money that has already arrived.
    (SELECT count(*)::bigint AS pending_orders FROM orders o
     WHERE o.fulfillment_status = 'pending'
       AND NOT order_is_committed(o.id) AND order_amount_after_credit(o.id) > 0) pending,
    -- Waiting since the order was funded, as admin/health reads it
    -- (UninvoicedOrders); placed_at is the fallback for an order store credit
    -- paid in full, which has no payment and no paid event.
    (SELECT count(*)::bigint AS ready_orders,
            coalesce(greatest(extract(epoch FROM now() - min(coalesce(
                (SELECT min(e.occurred_at) FROM order_events e
                 WHERE e.order_id = o.id AND e.kind = 'paid'),
                (SELECT max(p.paid_at) FROM payments p
                 WHERE p.order_id = o.id AND p.status = 'succeeded'),
                o.placed_at))), 0), 0)::bigint AS ready_oldest_seconds
     FROM orders o
     WHERE o.fulfillment_status = 'pending'
       AND (order_is_committed(o.id) OR order_amount_after_credit(o.id) <= 0)) ready,
    -- Waiting since a person took it into picking; an order moved there with no
    -- event recorded falls back to when it was placed.
    (SELECT count(*)::bigint AS picking_orders,
            coalesce(greatest(extract(epoch FROM now() - min(coalesce(
                (SELECT min(e.occurred_at) FROM order_events e
                 WHERE e.order_id = o.id AND e.kind = 'picking'),
                o.placed_at))), 0), 0)::bigint AS picking_oldest_seconds
     FROM orders o WHERE o.fulfillment_status = 'picking') picking,
    -- The SKUs the stock days cover lists as sold out.
    (SELECT count(*)::bigint AS sold_out FROM product_variants pv
     JOIN products p ON p.id = pv.product_id
     WHERE pv.is_active AND p.status = 'active'
       AND pv.stock_quantity <= pv.safety_stock) stock,
    (SELECT count(*)::bigint AS open_messages,
            coalesce(greatest(extract(epoch FROM now() - min(m.created_at)), 0), 0)::bigint
                AS open_messages_oldest_seconds
     FROM contact_messages m WHERE m.handled_at IS NULL) messages,
    (SELECT count(*)::bigint AS pending_returns,
            coalesce(greatest(extract(epoch FROM now() - min(rr.created_at)), 0), 0)::bigint
                AS pending_returns_oldest_seconds
     FROM return_requests rr WHERE rr.status = 'requested') requested,
    -- Approved, with a parcel to open: a refund before shipment closes its own
    -- lines and never has one.
    (SELECT count(*)::bigint AS uninspected_returns,
            coalesce(greatest(extract(epoch FROM now() - min(r.decided_at)), 0), 0)::bigint
                AS uninspected_returns_oldest_seconds
     FROM return_requests r
     WHERE r.status = 'approved' AND NOT r.before_shipment
       AND EXISTS (SELECT 1 FROM return_request_lines rl
                   WHERE rl.return_request_id = r.id AND rl.received_quantity IS NULL)) uninspected,
    -- The queue's own predicate (UnansweredQuestions, Question.Waiting): visible,
    -- and no visible answer from the shop. A customer's reply does not answer it.
    (SELECT count(*)::bigint AS unanswered_questions,
            coalesce(greatest(extract(epoch FROM now() - min(q.created_at)), 0), 0)::bigint
                AS unanswered_questions_oldest_seconds
     FROM product_questions q
     WHERE q.hidden_at IS NULL
       AND NOT EXISTS (SELECT 1 FROM product_answers a
                       WHERE a.question_id = q.id AND a.is_staff AND a.hidden_at IS NULL)) questions;

-- The newest orders whatever state they are in, as the landing page lists them.
-- The columns are the ones admin/orders' AdminOrders returns for a queue row,
-- plus returned: whether every unit is in a return whose refund has settled.
-- returned repeats the predicate of ReturnedOrders in internal/cart/query.sql,
-- so that this package reads no other package's query; the test that reads an
-- order through both desks holds the two to the same answer.
-- name: RecentOrders :many
SELECT o.order_number,
    o.fulfillment_status,
    o.placed_at,
    o.shipping_cents,
    o.discount_cents,
    o.tax_cents,
    coalesce(pd.recipient_name, '') AS recipient,
    coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
              WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
    order_is_committed(o.id) AS committed,
    order_amount_after_credit(o.id) AS owed_cents,
    (EXISTS (SELECT 1 FROM order_lines ol WHERE ol.order_id = o.id)
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
                                                                    AND e.kind = 'refunded'))))), 0)))::boolean AS returned
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @row_limit::integer;
