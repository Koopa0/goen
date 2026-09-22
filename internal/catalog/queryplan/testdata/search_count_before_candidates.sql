-- name: SearchProductsCount :one
SELECT count(*)::bigint
FROM products p
JOIN brands b ON b.id = p.brand_id
WHERE p.status = 'active'
  AND (p.name ILIKE $1::text
       OR coalesce(p.name_en, '') ILIKE $1::text
       OR coalesce(p.summary, '') ILIKE $1::text
       OR coalesce(p.summary_en, '') ILIKE $1::text
       OR b.name ILIKE $1::text
       OR EXISTS (
           SELECT 1 FROM product_specs ps
           WHERE ps.product_id = p.id
             AND (ps.label ILIKE $1::text
                  OR coalesce(ps.label_en, '') ILIKE $1::text
                  OR ps.value ILIKE $1::text
                  OR coalesce(ps.value_en, '') ILIKE $1::text)
       ));
