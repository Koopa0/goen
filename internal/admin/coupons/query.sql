-- name: AdminCoupons :many
SELECT json_build_object('Rank', c.is_active, 'At', c.created_at, 'ID', c.id)::text AS page_cursor, c.id, c.code, c.description, c.kind, c.amount_cents, c.percent_bp,
       c.min_subtotal_cents, c.max_discount_cents, c.max_redemptions,
       c.per_customer_limit, c.is_active, c.starts_at, c.ends_at,
       (SELECT count(*) FROM coupon_redemptions r JOIN orders o ON o.id = r.order_id
        WHERE r.coupon_id = c.id AND o.fulfillment_status <> 'cancelled')::bigint AS redeemed,
       (SELECT coalesce(sum(r.amount_cents), 0) FROM coupon_redemptions r JOIN orders o ON o.id = r.order_id
        WHERE r.coupon_id = c.id AND o.fulfillment_status <> 'cancelled')::bigint AS given_cents,
       (c.starts_at <= now() AND (c.ends_at IS NULL OR c.ends_at > now()))::boolean AS is_current
FROM coupons c
WHERE (NOT @has_cursor::boolean OR (c.is_active < @after_rank::boolean)
       OR (c.is_active = @after_rank::boolean AND c.created_at < @after_at::timestamptz)
       OR (c.is_active = @after_rank::boolean AND c.created_at = @after_at::timestamptz AND c.id < @after_id::uuid))
ORDER BY c.is_active DESC, c.created_at DESC, c.id DESC
LIMIT @row_limit::integer;

-- name: CreateCoupon :exec
INSERT INTO coupons (code, description, kind, amount_cents, percent_bp,
                     max_discount_cents, min_subtotal_cents,
                     max_redemptions, per_customer_limit, ends_at)
VALUES (@code::text, @description::text, @kind::text,
        sqlc.narg(amount_cents)::bigint, sqlc.narg(percent_bp)::integer,
        sqlc.narg(max_discount_cents)::bigint, @min_subtotal_cents::bigint,
        sqlc.narg(max_redemptions)::integer, @per_customer_limit::integer,
        CASE WHEN @days::integer > 0
             THEN now() + make_interval(days => @days::integer)
             ELSE NULL END);

-- Switched off, never deleted: coupon_redemptions references it, and a promotion
-- that ran is part of what past orders were charged.
-- name: SetCouponActive :execrows
UPDATE coupons SET is_active = @is_active::boolean WHERE upper(code) = upper(@code::text);
