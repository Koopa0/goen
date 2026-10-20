-- name: AdminHeroSlides :many
SELECT h.id, h.eyebrow, h.headline, h.primary_cta_label, h.primary_cta_href,
       h.media_digest, h.position, h.is_active, h.starts_at, h.ends_at,
       (h.is_active
        AND (h.starts_at IS NULL OR h.starts_at <= now())
        AND (h.ends_at IS NULL OR h.ends_at > now()))::boolean AS in_window,
       json_build_object('Position', h.position, 'ID', h.id)::text AS page_cursor
FROM hero_slides h
WHERE NOT @has_cursor::boolean
   OR (h.position, h.id) > (@after_position::integer, @after_id::uuid)
ORDER BY h.position, h.id
LIMIT @row_limit::integer;

-- name: LockHeroAppendPosition :exec
SELECT pg_advisory_xact_lock(hashtextextended(
    'append:hero_slides', 628471039582915603::bigint));

-- name: CreateHeroSlide :exec
INSERT INTO hero_slides (
    eyebrow, headline, body, primary_cta_label, primary_cta_href,
    secondary_cta_label, secondary_cta_href, media_digest, image_alt,
    eyebrow_en, headline_en, body_en, primary_cta_label_en,
    secondary_cta_label_en, image_alt_en,
    position, ends_at
) VALUES (
    nullif(@eyebrow::text, ''), @headline::text, nullif(@body::text, ''),
    @primary_cta_label::text, @primary_cta_href::text,
    nullif(@secondary_cta_label::text, ''), nullif(@secondary_cta_href::text, ''),
    nullif(@media_digest::text, ''), nullif(@image_alt::text, ''),
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
       is_active, starts_at, ends_at, created_at,
       json_build_object('Active', is_active, 'At', created_at, 'ID', id)::text AS page_cursor
FROM promo_banners
WHERE NOT @has_cursor::boolean
   OR (is_active, created_at, id) < (@after_active::boolean, @after_at::timestamptz, @after_id::uuid)
ORDER BY is_active DESC, created_at DESC, id DESC
LIMIT @row_limit::integer;

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
