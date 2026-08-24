-- Store an image, or recognise one already stored. The digest IS the content,
-- so a conflict means these exact bytes are here; media_objects_immutable
-- forbids the rewrite an upsert would do.
-- name: PutMedia :exec
INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size)
VALUES (@digest::text, @content_type::text, @bytes, @width::integer,
        @height::integer, @byte_size::integer)
ON CONFLICT (digest) DO NOTHING;

-- The bytes to serve.
-- name: MediaBytes :one
SELECT content_type, bytes, byte_size FROM media_objects WHERE digest = $1;

-- What the back office's picker shows. Deliberately does NOT select bytes.
-- name: RecentMedia :many
SELECT digest, content_type, width, height, byte_size, created_at
FROM media_objects
ORDER BY created_at DESC
LIMIT $1;

-- One stored object's metadata, including the dimensions the srcset is built
-- from.
-- name: MediaObject :one
SELECT digest, content_type, width, height, byte_size
FROM media_objects WHERE digest = $1;

-- Uploads nothing points at. Every referencing column has to appear here: a
-- delete that missed one would break a live image.
-- name: UnreferencedMedia :many
SELECT digest FROM media_objects m
WHERE NOT EXISTS (SELECT 1 FROM product_images p WHERE p.storage_key = m.digest)
  AND NOT EXISTS (SELECT 1 FROM hero_slides h WHERE h.image_key = m.digest)
  AND m.created_at < now() - interval '24 hours'
ORDER BY m.created_at
LIMIT $1;

-- The reference predicate is REPEATED here, not assumed. Selecting candidates
-- and deleting them are two statements, and an upload attached in between is
-- invisible to the second — the sweeper's own comment said a foreign key caught
-- that, and there is none: product_images.storage_key holds either an embedded
-- filename from the seed or a digest, so it cannot point at media_objects.
-- Asking again inside the DELETE makes a COMMITTED attach win, and :execrows is
-- what lets the caller tell "somebody attached it" from "deleted".
--
-- COMMITTED is the word this comment was missing, and it is not a detail. Under
-- READ COMMITTED the DELETE cannot see an attach that is still in flight, so a
-- transaction that inserts the hero slide, is interrupted, and commits after the
-- sweeper has run leaves a slide pointing at a deleted object. Measured — it is
-- not a theory.
--
-- Left open, and the reason is the shape of the column rather than the size of
-- the window: closing it properly is a foreign key, and storage_key deliberately
-- holds two kinds of key — an embedded filename from the seed or a digest — so
-- it cannot reference media_objects at all. The window itself is the
-- milliseconds of one DELETE against an orphan more than 24 hours old.
-- name: DeleteMedia :execrows
DELETE FROM media_objects m
WHERE m.digest = @digest::text
  AND NOT EXISTS (SELECT 1 FROM product_images p WHERE p.storage_key = m.digest)
  AND NOT EXISTS (SELECT 1 FROM hero_slides h WHERE h.image_key = m.digest);
