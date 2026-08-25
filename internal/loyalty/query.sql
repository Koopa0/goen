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
WITH grouped AS (
    SELECT
        CASE WHEN e.kind = 'spend'
             THEN split_part(e.idempotency_key, '#', 1)
             ELSE e.idempotency_key
        END AS entry_key,
        e.kind,
        e.reason,
        e.order_id,
        sum(e.points)::bigint AS points,
        coalesce(max(e.requested_points), 0)::bigint AS requested_points,
        max(e.expires_on)::date AS expires_on,
        max(e.created_at)::timestamptz AS created_at
    FROM loyalty_entries e
    JOIN store_credit_accounts a ON a.id = e.account_id
    WHERE a.user_id = @user_id
    GROUP BY entry_key, e.kind, e.reason, e.order_id
)
SELECT g.points, g.kind, g.reason, g.requested_points, g.expires_on, g.created_at,
       coalesce(o.order_number, '') AS order_number,
       (g.kind = 'award' AND g.expires_on < shop_today()) AS expired
FROM grouped g
LEFT JOIN orders o ON o.id = g.order_id
ORDER BY g.created_at DESC
LIMIT $1;

-- What is about to expire, so the page can say so before it happens.
-- name: PointsExpiringSoon :one
WITH lots AS (
    SELECT e.expires_on,
           (e.points + coalesce(sum(child.points), 0))::bigint AS remaining
    FROM loyalty_entries e
    JOIN store_credit_accounts a ON a.id = e.account_id
    LEFT JOIN loyalty_entries child ON child.lot_id = e.id
    WHERE a.user_id = @user_id
      AND e.kind = 'award'
    GROUP BY e.id, e.expires_on, e.points
)
SELECT coalesce(sum(remaining), 0)::bigint AS points,
       -- Two columns, not one nullable date: min() over no rows is NULL and sqlc
       -- infers the column non-nullable however it is cast, so pgx cannot scan it.
       coalesce(min(expires_on), shop_today())::date AS soonest,
       (count(*) > 0) AS any_expiring
FROM lots
WHERE remaining > 0
  AND expires_on >= shop_today()
  AND expires_on < shop_today() + @within_days::integer;
