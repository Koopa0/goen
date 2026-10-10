-- Prefix on both, each index-backed, with a floor on the term enforced by the
-- caller. Every role is searched, for AdminCustomer's reason. An erased
-- customer's row is gone, so nothing extra is needed to exclude one. The term
-- arrives LIKE-escaped: "%%" passes the floor and would otherwise list everyone.
-- name: AdminSearchCustomers :many
SELECT json_build_object('At', u.created_at, 'ID', u.id)::text AS page_cursor, u.id, u.email, coalesce(u.full_name, '') AS full_name, u.created_at,
       (u.email_verified_at IS NOT NULL)::boolean AS verified,
       (SELECT count(*) FROM orders o WHERE o.user_id = u.id)::bigint AS orders
FROM users u
WHERE (lower(u.email) LIKE lower(@escaped_term::text) || '%'
       OR u.full_name LIKE @escaped_term::text || '%')
AND (NOT @has_cursor::boolean OR (u.created_at < @after_at::timestamptz)
       OR (u.created_at = @after_at::timestamptz AND u.id < @after_id::uuid))
ORDER BY u.created_at DESC, u.id DESC
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
                 WHERE lb.account_id = (SELECT a.id FROM store_credit_accounts a
                                        WHERE a.user_id = u.id)), 0)::bigint AS points
FROM users u
WHERE u.id = $1;

-- name: AdminCustomerOrders :many
SELECT o.id, o.order_number, o.fulfillment_status, o.placed_at,
       o.shipping_cents, o.discount_cents, o.tax_cents,
       order_is_committed(o.id) AS committed,
       order_amount_after_credit(o.id) AS owed_cents,
       coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                 WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents
FROM orders o
WHERE o.user_id = $1
ORDER BY o.placed_at DESC
LIMIT $2;

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
