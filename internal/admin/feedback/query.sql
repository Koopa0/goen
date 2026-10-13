-- name: AdminQuestions :many
WITH queued AS (
 SELECT q.id, q.body, q.created_at, q.hidden_at,
        p.slug AS product_slug, p.name AS product_name,
        coalesce(u.full_name, '') AS asker,
        (SELECT count(*) FROM product_answers a WHERE a.question_id = q.id AND a.hidden_at IS NULL)::bigint AS answers,
        EXISTS (SELECT 1 FROM product_answers a WHERE a.question_id = q.id AND a.is_staff AND a.hidden_at IS NULL) AS answered_by_shop
 FROM product_questions q
 JOIN products p ON p.id = q.product_id
 LEFT JOIN users u ON u.id = q.user_id
 WHERE (q.hidden_at IS NOT NULL) = @hidden::boolean
)
SELECT json_build_object('Rank', CASE WHEN @hidden::boolean THEN false ELSE q.answered_by_shop END,
                         'At', coalesce(q.hidden_at, q.created_at), 'ID', q.id)::text AS page_cursor,
       q.*
FROM queued q
WHERE NOT @has_cursor::boolean
 OR (@hidden::boolean AND (q.hidden_at < @after_at::timestamptz OR (q.hidden_at = @after_at::timestamptz AND q.id < @after_id::uuid)))
 OR (NOT @hidden::boolean AND (q.answered_by_shop > @after_rank::boolean
     OR (q.answered_by_shop = @after_rank::boolean AND q.created_at > @after_at::timestamptz)
     OR (q.answered_by_shop = @after_rank::boolean AND q.created_at = @after_at::timestamptz AND q.id > @after_id::uuid)))
ORDER BY CASE WHEN NOT @hidden::boolean THEN q.answered_by_shop END,
         CASE WHEN @hidden::boolean THEN q.hidden_at END DESC,
         CASE WHEN NOT @hidden::boolean THEN q.created_at END,
         CASE WHEN @hidden::boolean THEN q.id END DESC,
         CASE WHEN NOT @hidden::boolean THEN q.id END
LIMIT @row_limit::integer;

-- name: HideQuestion :execrows
UPDATE product_questions SET hidden_at = now()
WHERE id = @question_id AND hidden_at IS NULL;

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

-- waiting_days is computed HERE because created_at is written by the database's
-- clock: taking the difference in Go subtracts two clocks, and a container
-- milliseconds ahead of its host reports a four-day-old message as three.
-- name: AdminMessages :many
SELECT json_build_object('Rank', (handled_at IS NOT NULL), 'At', created_at, 'ID', id)::text AS page_cursor, id, name, email, subject, coalesce(order_ref, '') AS order_ref,
       message, handled_at, created_at,
       floor(extract(epoch FROM now() - created_at) / 86400)::integer AS waiting_days
FROM contact_messages
WHERE (NOT @has_cursor::boolean OR ((handled_at IS NOT NULL) > @after_rank::boolean)
       OR ((handled_at IS NOT NULL) = @after_rank::boolean AND created_at > @after_at::timestamptz)
       OR ((handled_at IS NOT NULL) = @after_rank::boolean AND created_at = @after_at::timestamptz AND id > @after_id::uuid))
ORDER BY (handled_at IS NOT NULL) ASC, created_at ASC, id ASC
LIMIT @row_limit::integer;

-- name: HandleMessage :execrows
UPDATE contact_messages SET handled_at = now()
WHERE id = $1 AND handled_at IS NULL;

-- name: ReopenMessage :execrows
UPDATE contact_messages SET handled_at = NULL
WHERE id = $1 AND handled_at IS NOT NULL;

-- name: ShowQuestion :execrows
UPDATE product_questions SET hidden_at = NULL
WHERE id = @question_id AND hidden_at IS NOT NULL;

-- name: AdminQuestionAnswers :many
SELECT a.id, a.question_id, a.body, a.is_staff, a.hidden_at, a.created_at,
       coalesce(u.full_name, '')::text AS author
FROM product_answers a
LEFT JOIN users u ON u.id = a.user_id
WHERE a.question_id = ANY(@question_ids::uuid[])
ORDER BY a.question_id, a.created_at, a.id;


-- name: HideQuestionAnswer :execrows
UPDATE product_answers SET hidden_at = now()
WHERE id = @answer_id AND question_id = @question_id AND hidden_at IS NULL;
