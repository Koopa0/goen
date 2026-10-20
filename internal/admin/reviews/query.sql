-- The BASE table, so hidden reviews are listed too: un-hiding one is not
-- possible from a list that cannot show it.
-- name: AdminReviews :many
SELECT json_build_object('At', r.created_at, 'ID', r.id)::text AS page_cursor, r.id, r.rating, coalesce(r.title, '') AS title, r.body,
       r.is_verified_purchase, r.hidden_at, r.created_at,
       p.slug, p.name AS product_name,
       coalesce(u.full_name, '') AS author
FROM product_reviews r
JOIN products p ON p.id = r.product_id
LEFT JOIN users u ON u.id = r.user_id
WHERE (NOT @three_stars_and_below::boolean OR r.rating <= 3)
  AND (NOT @has_cursor::boolean OR (r.created_at < @after_at::timestamptz)
       OR (r.created_at = @after_at::timestamptz AND r.id < @after_id::uuid))
ORDER BY r.created_at DESC, r.id DESC
LIMIT @row_limit::integer;

-- name: HideReview :execrows
UPDATE product_reviews SET hidden_at = now()
WHERE id = $1 AND hidden_at IS NULL;

-- name: ShowReview :execrows
UPDATE product_reviews SET hidden_at = NULL
WHERE id = $1 AND hidden_at IS NOT NULL;
