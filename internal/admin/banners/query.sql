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
