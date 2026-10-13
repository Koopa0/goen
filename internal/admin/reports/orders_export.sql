-- The month uses RevenueBetween's sold set, placement clock and gross total.
-- Payment time follows LatestPaidOrder; credit-funded orders have no card row.
-- name: OrdersExportBetween :many
SELECT o.order_number, o.placed_at,
       coalesce((SELECT min(e.occurred_at) FROM order_events e
                 WHERE e.order_id = o.id AND e.kind = 'paid'),
                (SELECT p.paid_at FROM payments p
                 WHERE p.order_id = o.id AND p.status = 'succeeded'),
                o.placed_at)::timestamptz AS paid_at,
       t.total_cents, o.discount_cents, o.shipping_cents,
       (t.total_cents - order_amount_after_credit(o.id))::bigint AS credit_cents,
       coalesce((SELECT p.captured_amount_cents FROM payments p
                 WHERE p.order_id = o.id AND p.status = 'succeeded'), 0)::bigint AS card_cents,
       coalesce((SELECT d.number FROM invoice_documents d
                 WHERE d.order_id = o.id AND d.kind = 'invoice' AND d.status <> 'voided'), '')::text AS invoice_number
FROM orders o
JOIN sold_orders s ON s.id = o.id
CROSS JOIN LATERAL (
    SELECT (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                      FROM order_lines ol WHERE ol.order_id = o.id), 0)
            - o.discount_cents + o.shipping_cents + o.tax_cents)::bigint AS total_cents
) t
WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
ORDER BY o.placed_at, o.id;
