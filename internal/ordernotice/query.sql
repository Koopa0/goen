-- Delivery reads current private data so an erasure cannot be undone by a queued address.
-- name: TerminalOrderRecipient :one
SELECT o.order_number, o.locale, pd.email, pd.recipient_name
FROM orders o
JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.id = $1 AND pd.erased_at IS NULL;
