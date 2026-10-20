-- name: AdminFAQEntries :many
SELECT e.id, c.name AS category, e.question, e.answer,
       coalesce(e.question_en, '') AS question_en,
       coalesce(e.answer_en, '') AS answer_en,
       e.position, e.updated_at
FROM faq_entries e
JOIN faq_categories c ON c.id = e.category_id
ORDER BY c.position, e.position, e.id
LIMIT $1;

-- name: AdminFAQCategories :many
SELECT c.id, c.name, coalesce(c.name_en, '') AS name_en,
       EXISTS (SELECT 1 FROM faq_entries e WHERE e.category_id = c.id) AS in_use
FROM faq_categories c
ORDER BY c.position;

-- Two staff members adding a heading at the same moment would both read the same
-- max(position); faq_categories_position_key would refuse the second.
-- name: LockFAQCategoryAppendPosition :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'append:faq-category', 628471039582915603::bigint));

-- name: CreateFAQCategory :exec
INSERT INTO faq_categories (name, name_en, position)
VALUES (@name::text, nullif(@name_en::text, ''),
        coalesce((SELECT max(c.position) FROM faq_categories c), 0) + 1);

-- name: RenameFAQCategory :execrows
UPDATE faq_categories
SET name = @name::text, name_en = nullif(@name_en::text, '')
WHERE id = @category_id;

-- A category still holding entries is refused by faq_entries_category_id_fkey.
-- name: DeleteFAQCategory :execrows
DELETE FROM faq_categories WHERE id = @category_id;

-- The position is computed WITHIN the category, because faq_entries_position_key
-- is unique on (category_id, position).
-- name: LockFAQAppendPosition :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'append:faq:' || (@category_id::uuid)::text, 628471039582915603::bigint));

-- name: CreateFAQEntry :exec
INSERT INTO faq_entries (category_id, question, answer,
                         question_en, answer_en, position)
VALUES (@category_id::uuid, @question::text, @answer::text,
        nullif(@question_en::text, ''), nullif(@answer_en::text, ''),
        coalesce((SELECT max(f.position) FROM faq_entries f
                  WHERE f.category_id = @category_id::uuid), 0) + 1);

-- The CATEGORY is not editable: moving an entry between categories has to
-- renumber its position, and a form that silently collides with
-- faq_entries_position_key is worse than one that does not offer the move.
-- name: UpdateFAQEntry :execrows
UPDATE faq_entries
SET question = @question::text, answer = @answer::text,
    question_en = nullif(@question_en::text, ''),
    answer_en = nullif(@answer_en::text, '')
WHERE id = @entry_id;

-- name: DeleteFAQEntry :execrows
DELETE FROM faq_entries WHERE id = @entry_id;
