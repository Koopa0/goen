-- name: AdminSummary :one
-- Each desk is read once: its count and the moment its oldest item began
-- waiting come from the same rows, so the two cannot follow different rules.
-- The Go side turns a moment into shop days (shoptime.DaysAgo), as the orders
-- desk does, so a task and the list it opens count the same days.
SELECT
    pending.pending_orders,
    ready.ready_orders,
    ready.ready_oldest_since,
    picking.picking_orders,
    picking.picking_oldest_since,
    stock.sold_out,
    messages.open_messages,
    messages.open_messages_oldest_since,
    requested.pending_returns,
    requested.pending_returns_oldest_since,
    uninspected.uninspected_returns,
    uninspected.uninspected_returns_oldest_since,
    questions.unanswered_questions,
    questions.unanswered_questions_oldest_since
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
            coalesce(min(coalesce(
                (SELECT min(e.occurred_at) FROM order_events e
                 WHERE e.order_id = o.id AND e.kind = 'paid'),
                (SELECT max(p.paid_at) FROM payments p
                 WHERE p.order_id = o.id AND p.status = 'succeeded'),
                o.placed_at)), now())::timestamptz AS ready_oldest_since
     FROM orders o
     WHERE o.fulfillment_status = 'pending'
       AND (order_is_committed(o.id) OR order_amount_after_credit(o.id) <= 0)) ready,
    -- Waiting since the order was funded, as for ready orders and as the orders
    -- desk's work tabs count it: taking an order into picking does not restart
    -- its wait.
    (SELECT count(*)::bigint AS picking_orders,
            coalesce(min(coalesce(
                (SELECT min(e.occurred_at) FROM order_events e
                 WHERE e.order_id = o.id AND e.kind = 'paid'),
                (SELECT max(p.paid_at) FROM payments p
                 WHERE p.order_id = o.id AND p.status = 'succeeded'),
                o.placed_at)), now())::timestamptz AS picking_oldest_since
     FROM orders o WHERE o.fulfillment_status = 'picking') picking,
    -- The SKUs the stock days cover lists as sold out.
    (SELECT count(*)::bigint AS sold_out FROM product_variants pv
     JOIN products p ON p.id = pv.product_id
     WHERE pv.is_active AND p.status = 'active'
       AND pv.stock_quantity <= pv.safety_stock) stock,
    (SELECT count(*)::bigint AS open_messages,
            coalesce(min(m.created_at), now())::timestamptz AS open_messages_oldest_since
     FROM contact_messages m WHERE m.handled_at IS NULL) messages,
    (SELECT count(*)::bigint AS pending_returns,
            coalesce(min(rr.created_at), now())::timestamptz AS pending_returns_oldest_since
     FROM return_requests rr WHERE rr.status = 'requested') requested,
    -- Approved, with a parcel to open: a refund before shipment closes its own
    -- lines and never has one.
    (SELECT count(*)::bigint AS uninspected_returns,
            coalesce(min(r.decided_at), now())::timestamptz AS uninspected_returns_oldest_since
     FROM return_requests r
     WHERE r.status = 'approved' AND NOT r.before_shipment
       AND EXISTS (SELECT 1 FROM return_request_lines rl
                   WHERE rl.return_request_id = r.id AND rl.received_quantity IS NULL)) uninspected,
    -- The queue's own predicate (UnansweredQuestions, Question.Waiting): visible,
    -- and no visible answer from the shop. A customer's reply does not answer it.
    (SELECT count(*)::bigint AS unanswered_questions,
            coalesce(min(q.created_at), now())::timestamptz AS unanswered_questions_oldest_since
     FROM product_questions q
     WHERE q.hidden_at IS NULL
       AND NOT EXISTS (SELECT 1 FROM product_answers a
                       WHERE a.question_id = q.id AND a.is_staff AND a.hidden_at IS NULL)) questions;

-- The newest orders whatever state they are in, as the landing page lists them.
-- The columns are the ones admin/orders' AdminOrders returns for a queue row,
-- plus returned: whether every unit is in a return whose refund has settled.
-- returned repeats the predicate of ReturnedOrders in
-- internal/order/buyer/query.sql, so that this package reads no other package's
-- query; the test that reads an order through both desks holds the two to the
-- same answer.
-- name: RecentOrders :many
SELECT o.order_number,
    o.fulfillment_status,
    o.placed_at,
    t.total_cents,
    coalesce(pd.recipient_name, '') AS recipient,
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
JOIN order_totals t ON t.order_id = o.id
LEFT JOIN order_private_data pd ON pd.order_id = o.id
ORDER BY o.placed_at DESC, o.id DESC
LIMIT @row_limit::integer;
