-- AwardPoints used to sit here, a second door onto award_loyalty_points. The
-- award happens inside the CAPTURE's transaction — internal/payment calls the
-- function from AwardOrderPoints, where the money is — so a Store method that
-- opened its own connection could only ever be the wrong place to do it. One
-- door, in the transaction that owes the points.

-- Spend points and post the credit they bought, in one transaction.
-- name: RedeemPoints :one
SELECT redeem_loyalty_points(@account_id, @points::bigint, @cents::bigint, @key::text);

-- A customer's spendable balance and their account.
--
-- The account may not exist — a customer who has never held points or credit —
-- which is a real state and not an error.
-- name: PointsBalance :one
SELECT a.id AS account_id, coalesce(b.points, 0)::bigint AS points
FROM store_credit_accounts a
LEFT JOIN loyalty_balances b ON b.account_id = a.id
WHERE a.user_id = @user_id;

-- The ledger a customer sees.
--
-- Expired awards are shown, marked: a balance that silently shrank is a support
-- ticket, and "these 40 points expired in March" is the answer to it.
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
       -- Two columns, not one nullable date.
       --
       -- min() over no rows is NULL, and sqlc infers the column non-nullable
       -- whichever way it is cast — so pgx would refuse to scan it for any
       -- customer with nothing expiring, which is most of them. The third time
       -- this shape has appeared in goen; it is written this way every time.
       coalesce(min(e.expires_on), current_date)::date AS soonest,
       (count(*) > 0) AS any_expiring
FROM loyalty_entries e
JOIN store_credit_accounts a ON a.id = e.account_id
WHERE a.user_id = @user_id
  AND e.points > 0
  AND e.expires_on >= current_date
  AND e.expires_on < current_date + @within_days::integer;
