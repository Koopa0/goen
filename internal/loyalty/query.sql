-- No award query here: the award runs inside the capture's own transaction,
-- from internal/payment.

-- Spend points and post the credit they bought, in one transaction.
-- name: RedeemPoints :one
SELECT redeem_loyalty_points(@account_id, @points::bigint, @cents::bigint, @key::text);

-- A customer's spendable balance and their account, which may not exist.
-- name: PointsBalance :one
SELECT a.id AS account_id, coalesce(b.points, 0)::bigint AS points
FROM store_credit_accounts a
LEFT JOIN loyalty_balances b ON b.account_id = a.id
WHERE a.user_id = @user_id;

-- The ledger a customer sees, expired awards included and marked.
-- name: PointsHistory :many
SELECT e.points, e.reason, e.expires_on, e.created_at,
       coalesce(o.order_number, '') AS order_number,
       (e.points > 0 AND e.expires_on < current_date) AS expired
FROM loyalty_entries e
JOIN store_credit_accounts a ON a.id = e.account_id
LEFT JOIN orders o ON o.id = e.order_id
WHERE a.user_id = @user_id
ORDER BY e.created_at DESC
LIMIT $1;

-- What is about to expire, so the page can say so before it happens.
-- name: PointsExpiringSoon :one
SELECT coalesce(sum(e.points), 0)::bigint AS points,
       -- Two columns, not one nullable date: min() over no rows is NULL and sqlc
       -- infers the column non-nullable however it is cast, so pgx cannot scan it.
       coalesce(min(e.expires_on), current_date)::date AS soonest,
       (count(*) > 0) AS any_expiring
FROM loyalty_entries e
JOIN store_credit_accounts a ON a.id = e.account_id
WHERE a.user_id = @user_id
  AND e.points > 0
  AND e.expires_on >= current_date
  AND e.expires_on < current_date + @within_days::integer;
