-- name: RecentCredit :many
SELECT json_build_object('At', e.created_at, 'ID', e.id)::text AS page_cursor, e.amount_cents, e.reason, e.created_at,
       coalesce(u.email, '') AS email
FROM store_credit_entries e
JOIN store_credit_accounts a ON a.id = e.account_id
LEFT JOIN users u ON u.id = a.user_id
WHERE (NOT @has_cursor::boolean OR (e.created_at < @after_at::timestamptz)
       OR (e.created_at = @after_at::timestamptz AND e.id < @after_id::uuid))
ORDER BY e.created_at DESC, e.id DESC
LIMIT @row_limit::integer;

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

-- name: AdminMembershipTiers :many
SELECT t.id, t.code, t.name, coalesce(t.name_en, '') AS name_en,
       t.min_spend_cents, t.points_multiplier_bp, t.position,
       (SELECT count(*) FROM users u
        WHERE member_tier(u.id, @window_days::integer, NULL) = t.id)::bigint AS members
FROM membership_tiers t
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
