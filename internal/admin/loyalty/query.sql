-- kind says what the posting is; reason is only what a staff member typed, and
-- is NULL for every other kind. The label comes from kind, never from reason.
-- The page of entries is cut first and the rows around it joined after, so a
-- page reads fifty-one orders and customers and not one per entry in the ledger.
-- name: RecentCredit :many
WITH page AS (
    SELECT e.id, e.account_id, e.amount_cents, e.kind, e.reason, e.order_id,
           e.actor_user_id, e.idempotency_key, e.created_at
    FROM store_credit_entries e
    WHERE (NOT @has_cursor::boolean OR (e.created_at < @after_at::timestamptz)
           OR (e.created_at = @after_at::timestamptz AND e.id < @after_id::uuid))
    ORDER BY e.created_at DESC, e.id DESC
    LIMIT @row_limit::integer
)
SELECT json_build_object('At', e.created_at, 'ID', e.id)::text AS page_cursor, e.amount_cents,
       coalesce(e.kind, '')::text AS kind, coalesce(e.reason, '')::text AS reason, e.created_at,
       coalesce(u.email, '') AS email,
       coalesce(u.id::text, '')::text AS customer_id,
       coalesce(o.order_number, '') AS order_number,
       coalesce(r.id::text, '')::text AS return_id,
       coalesce(actor.email, '') AS actor_email
FROM page e
JOIN store_credit_accounts a ON a.id = e.account_id
LEFT JOIN users u ON u.id = a.user_id
LEFT JOIN orders o ON o.id = e.order_id
LEFT JOIN return_requests r ON e.kind = 'return_payout' AND e.idempotency_key = 'return-credit:' || r.id::text
LEFT JOIN users actor ON actor.id = e.actor_user_id
ORDER BY e.created_at DESC, e.id DESC;

-- One customer's postings with the running total each leaves: the sum of that
-- account's entries up to and including the row, in (created_at, id) order. The
-- window reads only this account's entries, through store_credit_entries_account_idx,
-- and the keyset test comes after it, so a later page still starts from the whole
-- history. The desk for every customer has no such column: one window over every
-- account is what that page cannot afford.
-- name: CustomerCredit :many
WITH ledger AS (
    SELECT e.id, e.account_id, e.amount_cents, e.kind, e.reason, e.order_id,
           e.actor_user_id, e.idempotency_key, e.created_at,
           sum(e.amount_cents) OVER (ORDER BY e.created_at, e.id)::bigint AS running_cents
    FROM store_credit_entries e
    WHERE e.account_id = (SELECT a.id FROM store_credit_accounts a WHERE a.user_id = @customer_id::uuid)
), page AS (
    SELECT l.*
    FROM ledger l
    WHERE (NOT @has_cursor::boolean OR (l.created_at < @after_at::timestamptz)
           OR (l.created_at = @after_at::timestamptz AND l.id < @after_id::uuid))
    ORDER BY l.created_at DESC, l.id DESC
    LIMIT @row_limit::integer
)
SELECT json_build_object('At', e.created_at, 'ID', e.id)::text AS page_cursor, e.amount_cents,
       coalesce(e.kind, '')::text AS kind, coalesce(e.reason, '')::text AS reason, e.created_at,
       e.running_cents,
       coalesce(o.order_number, '') AS order_number,
       coalesce(r.id::text, '')::text AS return_id,
       coalesce(actor.email, '') AS actor_email
FROM page e
LEFT JOIN orders o ON o.id = e.order_id
LEFT JOIN return_requests r ON e.kind = 'return_payout' AND e.idempotency_key = 'return-credit:' || r.id::text
LEFT JOIN users actor ON actor.id = e.actor_user_id
ORDER BY e.created_at DESC, e.id DESC;

-- Any user, whatever their role now: a customer promoted to staff keeps the
-- history they earned, as their profile and the grant form's recipient lookup
-- already allow.
-- name: CreditCustomerByID :one
SELECT id, email, coalesce(full_name, '') AS full_name FROM users
WHERE id = $1;

-- internal/account already defines UserByEmail for sign-in and sqlc generates
-- one db package, so this one is named for what it is FOR. It also selects less:
-- the back office has no business reading a password hash.
-- name: CustomerByEmail :one
SELECT id, email, coalesce(full_name, '') AS full_name FROM users
WHERE lower(email) = lower(@email::text);

-- From store_credit_balances, the ONE view that defines a balance, never a sum
-- written out again here.
-- name: CreditBalance :one
SELECT coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = $1), 0)::bigint;

-- The casts are what make the nullability explicit: sqlc reads a bare parameter
-- as non-nullable, and a grant has no order behind it and may have no actor.
-- name: PostStoreCredit :one
SELECT grant_store_credit(
    @user_id, @amount_cents::bigint, @reason::text,
    @actor_user_id::uuid, @operation_id::uuid
)::uuid AS entry_id;

-- Each customer's tier is derived once and the customers counted per band, not
-- one member_tier call per customer and band.
-- name: AdminMembershipTiers :many
SELECT t.id, t.code, t.name, coalesce(t.name_en, '') AS name_en,
       t.min_spend_cents, t.points_multiplier_bp, t.position,
       coalesce(m.members, 0)::bigint AS members
FROM membership_tiers t
LEFT JOIN (SELECT member_tier(u.id, NULL) AS tier_id, count(*) AS members
           FROM users u
           GROUP BY 1) m ON m.tier_id = t.id
ORDER BY t.min_spend_cents;

-- name: CreateMembershipTier :exec
INSERT INTO membership_tiers (code, name, name_en, min_spend_cents,
                              points_multiplier_bp, position)
VALUES (@code, @name, nullif(@name_en::text, ''), @min_spend_cents,
        @points_multiplier_bp, @position);

-- A DELETE and not a flag: no order references a tier, and the customers who
-- were in it are re-derived into whichever band they now qualify for.
-- name: DeleteMembershipTier :execrows
DELETE FROM membership_tiers WHERE id = $1;
