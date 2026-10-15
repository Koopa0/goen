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

-- Uploads nothing points at. Every referencing column has to appear here: one
-- missed is offered for deletion on every pass, and only its foreign key keeps
-- the image.
-- name: UnreferencedMedia :many
SELECT digest FROM media_objects m
WHERE NOT EXISTS (SELECT 1 FROM product_images p WHERE p.media_digest = m.digest)
  AND NOT EXISTS (SELECT 1 FROM hero_slides h WHERE h.media_digest = m.digest)
  AND NOT EXISTS (SELECT 1 FROM sale_campaigns c WHERE c.media_digest = m.digest)
  AND NOT EXISTS (SELECT 1 FROM categories k WHERE k.media_digest = m.digest)
  AND m.created_at < now() - interval '24 hours'
ORDER BY m.created_at
LIMIT $1;

-- The reference predicate is REPEATED here, not assumed. Selecting candidates
-- and deleting them are two statements, and an upload attached in between is
-- invisible to the second. Asking again inside the DELETE makes a COMMITTED
-- attach win, and :execrows is what lets the caller tell "somebody attached it"
-- from "deleted".
--
-- COMMITTED is the word that matters. Under READ COMMITTED the DELETE cannot see
-- an attach that is still in flight, so without a foreign key a transaction that
-- attaches, is interrupted, and commits after the sweeper has run leaves a page
-- pointing at a deleted object. Measured — it is not a theory.
--
-- The foreign keys close it. Every column named here (product_images,
-- hero_slides, sale_campaigns and categories, each media_digest) references
-- media_objects ON DELETE RESTRICT: the in-flight attach holds a lock this DELETE
-- waits on, and the delete is then refused, which Reclaim reads as "attached".
-- A new image column needs both. Without its foreign key, the in-flight window
-- is open again; without its line here and in UnreferencedMedia, every upload it
-- holds stays a candidate that each pass tries, is refused and logs as attached.
-- name: DeleteMedia :execrows
DELETE FROM media_objects m
WHERE m.digest = @digest::text
  AND NOT EXISTS (SELECT 1 FROM product_images p WHERE p.media_digest = m.digest)
  AND NOT EXISTS (SELECT 1 FROM hero_slides h WHERE h.media_digest = m.digest)
  AND NOT EXISTS (SELECT 1 FROM sale_campaigns c WHERE c.media_digest = m.digest)
  AND NOT EXISTS (SELECT 1 FROM categories k WHERE k.media_digest = m.digest);
