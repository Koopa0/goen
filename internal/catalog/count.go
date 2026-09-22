package catalog

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CountListingPredicate is the CategoryListing predicate evaluated without
// going through CategoryListingCount. A drift between the two queries is the
// bug TestListingCountAgreesWithFilters locks.
func CountListingPredicate(ctx context.Context, pool *pgxpool.Pool, categoryIDs, brandIDs []uuid.UUID, f Filters) (int64, error) {
	const sql = `
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
  )`
	var total int64
	err := pool.QueryRow(ctx, sql,
		categoryIDs,
		brandIDs,
		f.FiltersVariants(),
		f.InStockOnly,
		f.MinPrice,
		f.MaxPrice,
	).Scan(&total)
	return total, err
}

// ResolveListingBrandIDs resolves selected brand slugs the same way Store.Listing does.
func ResolveListingBrandIDs(ctx context.Context, pool *pgxpool.Pool, categoryIDs []uuid.UUID, brandSlugs []string) ([]uuid.UUID, error) {
	if len(brandSlugs) == 0 {
		return []uuid.UUID{}, nil
	}
	rows, err := pool.Query(ctx, `
SELECT b.id, b.slug
FROM products p
JOIN brands b ON b.id = p.brand_id
WHERE p.status = 'active' AND p.category_id = ANY($1::uuid[])
GROUP BY b.id, b.slug`, categoryIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	selected := make(map[string]bool, len(brandSlugs))
	for _, s := range brandSlugs {
		selected[s] = true
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var slug string
		if err := rows.Scan(&id, &slug); err != nil {
			return nil, err
		}
		if selected[slug] {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(brandSlugs) > 0 && len(ids) == 0 {
		return []uuid.UUID{uuid.Nil}, nil
	}
	return ids, nil
}
