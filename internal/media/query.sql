-- Store an image, or recognise one already stored.
--
-- ON CONFLICT DO NOTHING and not an upsert: the digest IS the content, so a
-- conflict means these exact bytes are already here. Rewriting the row would
-- write identical values, and media_objects_immutable forbids it anyway.
-- name: PutMedia :exec
INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size)
VALUES (@digest::text, @content_type::text, @bytes, @width::integer,
        @height::integer, @byte_size::integer)
ON CONFLICT (digest) DO NOTHING;

-- The bytes to serve.
-- name: MediaBytes :one
SELECT content_type, bytes, byte_size FROM media_objects WHERE digest = $1;

-- What the back office's picker shows. Deliberately does NOT select bytes: a
-- listing that carried every image's pixels would read megabytes to render a
-- grid of thumbnails.
-- name: RecentMedia :many
SELECT digest, content_type, width, height, byte_size, created_at
FROM media_objects
ORDER BY created_at DESC
LIMIT $1;

-- Uploads nothing points at.
--
-- An upload is stored before it is attached — the two are separate steps, and
-- an abandoned form leaves a row. This is how those are reclaimed, and every
-- referencing column has to appear below: a delete that missed one would break
-- a live image.
-- One stored object's metadata, for a caller attaching it somewhere.
--
-- The dimensions are read from HERE rather than taken from a form: they are
-- what the srcset is built from, and a browser lays out against them before a
-- byte of the image arrives.
-- name: MediaObject :one
SELECT digest, content_type, width, height, byte_size
FROM media_objects WHERE digest = $1;

-- name: UnreferencedMedia :many
SELECT digest FROM media_objects m
WHERE NOT EXISTS (SELECT 1 FROM product_images p WHERE p.storage_key = m.digest)
  AND NOT EXISTS (SELECT 1 FROM hero_slides h WHERE h.image_key = m.digest)
  AND m.created_at < now() - interval '24 hours'
ORDER BY m.created_at
LIMIT $1;

-- name: DeleteMedia :exec
DELETE FROM media_objects WHERE digest = @digest::text;
