-- Every customer's spend in one pass over member_order_spend, the customers
-- counted per spend, and each distinct spend given its band once. Calling
-- member_tier per account cost a spend aggregate per account, most of them for
-- accounts that have never ordered (#1934).
-- name: AdminMembershipTiers :many
WITH spend AS (
    SELECT u.id, coalesce(sum(s.spend_cents), 0)::bigint AS cents
    FROM users u
    LEFT JOIN member_order_spend s ON s.user_id = u.id
    GROUP BY u.id
), per_spend AS (
    SELECT cents, count(*) AS members FROM spend GROUP BY cents
), per_tier AS (
    SELECT membership_tier_for(cents) AS tier_id, sum(members) AS members
    FROM per_spend
    GROUP BY 1
)
SELECT t.id, t.code, t.name, coalesce(t.name_en, '') AS name_en,
       t.min_spend_cents, t.points_multiplier_bp, t.position,
       coalesce(m.members, 0)::bigint AS members
FROM membership_tiers t
LEFT JOIN per_tier m ON m.tier_id = t.id
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
