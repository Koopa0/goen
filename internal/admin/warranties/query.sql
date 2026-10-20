-- EXACT on both, told apart by the caller: a prefix would widen the answer
-- without widening what the person on the phone can tell you. LEFT JOIN on the
-- user, because user_id is ON DELETE SET NULL and erase_user leaves the row.
-- name: AdminSearchWarranties :many
SELECT json_build_object('At', w.expires_on::timestamptz, 'ID', w.id)::text AS page_cursor, w.id, w.unit_no, coalesce(w.serial_number, '') AS serial_number,
       w.registered_at, w.expires_on,
       (w.expires_on >= shop_today())::boolean AS in_force,
       ol.product_name, coalesce(ol.variant_label, '') AS variant_label,
       o.id AS order_id, o.order_number, o.fulfillment_status,
       coalesce(u.full_name, '') AS customer_name,
       coalesce(u.id::text, '')::text AS customer_id,
       coalesce(u.email, '') AS customer_email
FROM warranty_registrations w
JOIN order_lines ol ON ol.id = w.order_line_id
JOIN orders o ON o.id = ol.order_id
LEFT JOIN users u ON u.id = w.user_id
WHERE (w.serial_number = @term::text OR o.order_number = @term::text)
AND (NOT @has_cursor::boolean OR (w.expires_on::timestamptz < @after_at::timestamptz)
       OR (w.expires_on::timestamptz = @after_at::timestamptz AND w.id > @after_id::uuid))
ORDER BY w.expires_on::timestamptz DESC, w.id ASC
LIMIT @row_limit::integer;
