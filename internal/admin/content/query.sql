-- name: AdminHeroSlides :many
SELECT h.id, h.eyebrow, h.headline, h.primary_cta_label, h.primary_cta_href,
       h.image_key, h.position, h.is_active, h.starts_at, h.ends_at,
       (h.is_active
        AND (h.starts_at IS NULL OR h.starts_at <= now())
        AND (h.ends_at IS NULL OR h.ends_at > now()))::boolean AS in_window
FROM hero_slides h
ORDER BY h.position, h.id
LIMIT $1;

-- name: LockHeroAppendPosition :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'append:hero_slides', 628471039582915603::bigint));

-- name: CreateHeroSlide :exec
INSERT INTO hero_slides (
    eyebrow, headline, body, primary_cta_label, primary_cta_href,
    secondary_cta_label, secondary_cta_href, image_key, image_alt,
    eyebrow_en, headline_en, body_en, primary_cta_label_en,
    secondary_cta_label_en, image_alt_en,
    position, ends_at
) VALUES (
    nullif(@eyebrow::text, ''), @headline::text, nullif(@body::text, ''),
    @primary_cta_label::text, @primary_cta_href::text,
    nullif(@secondary_cta_label::text, ''), nullif(@secondary_cta_href::text, ''),
    nullif(@image_key::text, ''), nullif(@image_alt::text, ''),
    nullif(@eyebrow_en::text, ''), nullif(@headline_en::text, ''),
    nullif(@body_en::text, ''), nullif(@primary_cta_label_en::text, ''),
    nullif(@secondary_cta_label_en::text, ''), nullif(@image_alt_en::text, ''),
    coalesce((SELECT max(position) + 1 FROM hero_slides), 0),
    CASE WHEN @days::integer > 0 THEN now() + make_interval(days => @days::integer) END
);

-- name: SetHeroSlideActive :execrows
UPDATE hero_slides SET is_active = @is_active::boolean WHERE id = @id;

-- ONE row: the promoted slide takes a position below every other, rather than
-- everything else shifting up and position growing without bound.
-- name: PromoteHeroSlide :execrows
UPDATE hero_slides h
SET position = coalesce((SELECT min(o.position) FROM hero_slides o), 0) - 1
WHERE h.id = @id;

-- name: ManagedBanners :many
SELECT id, message, coalesce(message_short, '') AS message_short,
       coalesce(code, '') AS code,
       coalesce(cta_label, '') AS cta_label, coalesce(cta_href, '') AS cta_href,
       coalesce(message_en, '') AS message_en,
       coalesce(message_short_en, '') AS message_short_en,
       coalesce(cta_label_en, '') AS cta_label_en,
       is_active, starts_at, ends_at, created_at
FROM promo_banners
ORDER BY is_active DESC, created_at DESC
LIMIT $1;

-- name: CreateBanner :exec
INSERT INTO promo_banners (
    message, message_short, code, cta_label, cta_href,
    message_en, message_short_en, cta_label_en, ends_at
) VALUES (
    @message::text, nullif(@message_short::text, ''), nullif(@code::text, ''),
    nullif(@cta_label::text, ''), nullif(@cta_href::text, ''),
    nullif(@message_en::text, ''), nullif(@message_short_en::text, ''),
    nullif(@cta_label_en::text, ''),
    CASE WHEN @days::integer > 0 THEN now() + make_interval(days => @days::integer) END
);

-- Switched off, never deleted: the dismissal cookie is keyed on the id, so a new
-- row with the same copy would reappear for everybody who had closed it.
-- name: SetBannerActive :execrows
UPDATE promo_banners SET is_active = @is_active::boolean WHERE id = @banner_id;

-- name: AdminFAQEntries :many
SELECT id, category, question, answer,
       coalesce(category_en, '') AS category_en,
       coalesce(question_en, '') AS question_en,
       coalesce(answer_en, '') AS answer_en,
       position, updated_at
FROM faq_entries
ORDER BY category, position, id
LIMIT $1;

-- The position is computed WITHIN the category, because faq_entries_position_key
-- is unique on (category, position).
-- name: LockFAQAppendPosition :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'append:faq:' || @category::text, 628471039582915603::bigint));

-- name: CreateFAQEntry :exec
INSERT INTO faq_entries (category, question, answer,
                         category_en, question_en, answer_en, position)
VALUES (@category::text, @question::text, @answer::text,
        nullif(@category_en::text, ''), nullif(@question_en::text, ''),
        nullif(@answer_en::text, ''),
        coalesce((SELECT max(f.position) FROM faq_entries f
                  WHERE f.category = @category::text), 0) + 1);

-- The CATEGORY is not editable: moving an entry between categories has to
-- renumber its position, and a form that silently collides with
-- faq_entries_position_key is worse than one that does not offer the move.
-- name: UpdateFAQEntry :execrows
UPDATE faq_entries
SET question = @question::text, answer = @answer::text,
    question_en = nullif(@question_en::text, ''),
    answer_en = nullif(@answer_en::text, ''),
    category_en = nullif(@category_en::text, '')
WHERE id = @entry_id;

-- name: DeleteFAQEntry :execrows
DELETE FROM faq_entries WHERE id = @entry_id;
