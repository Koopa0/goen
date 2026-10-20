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

-- name: ShowQuestion :execrows
UPDATE product_questions SET hidden_at = NULL
WHERE id = @question_id AND hidden_at IS NOT NULL;

-- A staff answer names its author by CURRENT email, not by full_name, which the
-- author can change on /account/profile; a customer's answer keeps the name
-- that customer gave.
-- name: AdminQuestionAnswers :many
SELECT a.id, a.question_id, a.body, a.is_staff, a.hidden_at, a.created_at,
       CASE WHEN a.is_staff THEN coalesce(u.email, '') ELSE coalesce(u.full_name, '') END::text AS author
FROM product_answers a
LEFT JOIN users u ON u.id = a.user_id
WHERE a.question_id = ANY(@question_ids::uuid[])
ORDER BY a.question_id, a.created_at, a.id;

-- name: HideQuestionAnswer :execrows
UPDATE product_answers SET hidden_at = now()
WHERE id = @answer_id AND question_id = @question_id AND hidden_at IS NULL;

-- name: AnswerQuestionAsStaff :execrows
INSERT INTO product_answers (question_id, user_id, body, is_staff)
SELECT q.id, @user_id, @body::text, true
FROM product_questions q
WHERE q.id = @question_id AND q.hidden_at IS NULL;
