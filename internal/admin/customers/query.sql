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

-- Lifetime spend and both balances come from the VIEWS that define them. No
-- role predicate, deliberately: /admin/staff promotes an existing customer,
-- whose order history must stay reachable from this page.
-- name: AdminCustomer :one
SELECT u.id, u.email, coalesce(u.full_name, '') AS full_name,
       coalesce(u.phone, '') AS phone, u.created_at,
       (u.email_verified_at IS NOT NULL)::boolean AS verified,
       (SELECT count(*) FROM orders o WHERE o.user_id = u.id)::bigint AS orders,
       coalesce((SELECT sum(s.spend_cents) FROM committed_order_spend s
                 WHERE s.user_id = u.id), 0)::bigint AS spent,
       coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = u.id), 0)::bigint AS credit_cents,
       coalesce((SELECT lb.points FROM loyalty_balances lb
                 WHERE lb.account_id = (SELECT a.id FROM store_credit_accounts a
                                        WHERE a.user_id = u.id)), 0)::bigint AS points
FROM users u
WHERE u.id = $1;

-- name: AdminCustomerOrders :many
SELECT o.id, o.order_number, o.fulfillment_status, o.placed_at, t.total_cents,
       order_is_committed(o.id) AS committed,
       order_amount_after_credit(o.id) AS owed_cents
FROM orders o
JOIN order_totals t ON t.order_id = o.id
WHERE o.user_id = $1
ORDER BY o.placed_at DESC
LIMIT $2;
