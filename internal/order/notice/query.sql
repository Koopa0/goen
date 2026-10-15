-- Delivery reads current private data so an erasure cannot be undone by a queued address.
-- rescission_ends is the earliest parcel's last day. sqlc cannot type a nullable
-- date from an expression, so before any delivery it is shop_today() and
-- `delivered` is false; a reader must check that, never the date.
-- name: TerminalOrderRecipient :one
SELECT o.order_number, o.locale, pd.email, pd.recipient_name,
       coalesce((SELECT min(return_window_ends(s.delivered_at))
                 FROM order_shipments s WHERE s.order_id = o.id), shop_today())::date AS rescission_ends,
       EXISTS (SELECT 1 FROM order_shipments s
               WHERE s.order_id = o.id AND s.delivered_at IS NOT NULL) AS delivered
FROM orders o
JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.id = $1 AND pd.erased_at IS NULL;

-- What an order.part_refunded letter says, read at delivery: the units of one
-- settled unsent refund and the split its approval froze. Nothing here is a
-- copy kept in the outbox, so an erasure meanwhile leaves nothing to send.
-- name: PartRefundLines :many
SELECT ol.product_name, coalesce(ol.variant_label, '')::text AS variant_label, rl.quantity,
       (r.goods_refund_cents + r.shipping_refund_cents)::bigint AS refund_cents,
       coalesce(r.card_refund_cents, 0)::bigint AS card_cents,
       coalesce(r.credit_refund_cents, 0)::bigint AS credit_cents
FROM return_requests r
JOIN return_request_lines rl ON rl.return_request_id = r.id
JOIN order_lines ol ON ol.id = rl.order_line_id
WHERE r.id = @return_request_id AND r.order_id = @order_id
  AND r.unsent AND r.status = 'completed'
ORDER BY ol.position, ol.id;
