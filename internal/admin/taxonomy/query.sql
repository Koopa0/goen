-- name: ManagedBrands :many
SELECT b.id, b.slug, b.name,
       (SELECT count(*) FROM products p WHERE p.brand_id = b.id)::bigint AS products
FROM brands b
ORDER BY b.name;

-- name: CreateBrand :exec
INSERT INTO brands (slug, name) VALUES (@slug::text, @name::text);

-- name: RenameBrand :execrows
UPDATE brands SET name = @name::text WHERE slug = @slug::text;

-- The emptiness check is in the statement, not read first. The foreign keys are
-- ON DELETE RESTRICT and would refuse anyway; this turns the refusal into a row
-- count, which is the difference between a sentence and a constraint name.
-- name: DeleteBrand :execrows
DELETE FROM brands b
WHERE b.slug = @slug::text
  AND NOT EXISTS (SELECT 1 FROM products p WHERE p.brand_id = b.id);

-- name: ManagedCategories :many
WITH RECURSIVE tree AS (
    SELECT c.id, c.parent_id, c.slug, c.name, c.name_en, c.icon_key, c.tone, c.comparable, c.position,
           0 AS depth, array[c.position, 0] AS path
    FROM categories c WHERE c.parent_id IS NULL
    UNION ALL
    SELECT c.id, c.parent_id, c.slug, c.name, c.name_en, c.icon_key, c.tone, c.comparable, c.position,
           t.depth + 1, t.path || array[c.position, 0]
    FROM categories c JOIN tree t ON t.id = c.parent_id
)
SELECT t.id, t.slug, t.name, coalesce(t.name_en, '') AS name_en,
       coalesce(t.icon_key, '') AS icon_key,
       coalesce(t.tone, '') AS tone,
       coalesce(t.comparable, false)::boolean AS comparable,
       t.depth::integer AS depth,
       coalesce(p.name, '') AS parent_name,
       (SELECT count(*) FROM products x WHERE x.category_id = t.id)::bigint AS products,
       (SELECT count(*) FROM categories k WHERE k.parent_id = t.id)::bigint AS children
FROM tree t
LEFT JOIN categories p ON p.id = t.parent_id
ORDER BY t.path, t.name;

-- The parent is a derived table and not a scalar subquery: that would yield NULL
-- for a slug that does not exist, creating a ROOT category and reporting
-- success. No rows is how the caller learns the parent was not found.
-- name: CreateCategory :execrows
INSERT INTO categories (slug, name, name_en, icon_key, tone, comparable, parent_id, position)
SELECT @slug::text, @name::text, nullif(@name_en::text, ''),
       nullif(@icon_key::text, ''), nullif(@tone::text, ''),
       -- Only a department answers: a sub-category keeps NULL and takes its department's.
       CASE WHEN parent.id IS NULL THEN @comparable::boolean END, parent.id,
       coalesce((SELECT max(c.position) + 1 FROM categories c
                 WHERE c.parent_id IS NOT DISTINCT FROM parent.id), 0)
FROM (
    SELECT c.id FROM categories c WHERE c.slug = @parent_slug::text
    UNION ALL
    SELECT NULL::uuid WHERE @parent_slug::text = ''
) parent;

-- The DISPLAY names only: a slug is in every URL a search engine has indexed and
-- goen has no redirect table. nullif('') is what lets name_en be cleared, and
-- an empty tone is what makes the category inherit its department's.
-- name: RenameCategory :execrows
UPDATE categories SET name = @name::text, name_en = nullif(@name_en::text, ''),
                     icon_key = nullif(@icon_key::text, ''),
                     tone = nullif(@tone::text, ''),
                     comparable = CASE WHEN parent_id IS NULL THEN @comparable::boolean END
WHERE slug = @slug::text;

-- name: SetCategoryImage :execrows
UPDATE categories
SET image_key = @image_key::text, image_alt = @image_alt::text,
    image_alt_en = nullif(@image_alt_en::text, '')
WHERE slug = @slug::text;

-- name: ClearCategoryImage :execrows
UPDATE categories
SET image_key = NULL, image_alt = NULL, image_alt_en = NULL
WHERE slug = @slug::text;

-- The stored width comes from media_objects, and is 0 for a key that is not an
-- upload.
-- name: AdminCategoryImage :one
SELECT c.name,
       coalesce(c.tone, '')::text AS tone,
       coalesce(c.image_key, '')::text AS image_key,
       coalesce(c.image_alt, '')::text AS image_alt,
       coalesce(c.image_alt_en, '')::text AS image_alt_en,
       coalesce(m.width, 0)::integer AS image_width
FROM categories c
LEFT JOIN media_objects m ON m.digest = c.image_key
WHERE c.slug = @slug::text;

-- name: DeleteCategory :execrows
DELETE FROM categories c
WHERE c.slug = @slug::text
  AND NOT EXISTS (SELECT 1 FROM products p WHERE p.category_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM categories k WHERE k.parent_id = c.id);
