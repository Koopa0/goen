-- Delivery reads current private data so an erasure cannot be undone by a queued address.
-- rescission_ends is the earliest parcel's last day, or '' before any delivery,
-- so it is never later than the right of any parcel the notice may be about.
-- name: TerminalOrderRecipient :one
SELECT o.order_number, o.locale, pd.email, pd.recipient_name,
       coalesce((SELECT to_char(min(return_window_ends(s.delivered_at)), 'YYYY-MM-DD')
                 FROM order_shipments s WHERE s.order_id = o.id), '')::text AS rescission_ends
FROM orders o
JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.id = $1 AND pd.erased_at IS NULL;
