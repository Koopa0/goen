-- Immutable production SQL from 2793db97058cdce2f3601142fd4ab6b6c0df182c.
-- name: CategoryListingCount :one
SELECT count(*)::bigint
FROM products p
WHERE p.status = 'active'
  AND p.category_id = ANY($1::uuid[])
  AND ($2::uuid[] = ARRAY[]::uuid[] OR p.brand_id = ANY($2::uuid[]))
  AND (
      NOT $3::boolean
      OR EXISTS (
          SELECT 1 FROM product_variants v
          WHERE v.product_id = p.id AND v.is_active
            AND (NOT $4::boolean OR v.stock_quantity > v.safety_stock)
            AND ($5::bigint = 0 OR v.price_cents >= $5::bigint)
            AND ($6::bigint = 0 OR v.price_cents <= $6::bigint)
      )
  )
;
