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
