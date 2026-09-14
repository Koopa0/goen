package queryplan

import (
	"github.com/google/uuid"
)

// Query is one measured catalogue read.
type Query struct {
	Route Route
	SQL   string
	Args  []any
}

// Queries returns every route the acceptance harness measures.
func Queries(scale Scale, categoryIDs []uuid.UUID, phonesCategory uuid.UUID) []Query {
	pageSize := int32(24)
	deepOffset := int32(4800)
	if scale == ScaleSmall {
		deepOffset = 0
	}
	searchLatin := "%Pixelight%"
	if scale == ScaleLarge {
		searchLatin = "%Scale%"
	}

	return []Query{
		{Route: RouteHomeRecommended, SQL: homeRecommendedSQL, Args: []any{int32(8)}},
		{Route: RouteHomeCategories, SQL: rootCategoriesSQL, Args: nil},
		{
			Route: RouteCategoryListing,
			SQL:   categoryListingSQL,
			Args: []any{
				categoryIDs, []uuid.UUID{}, false, false, int64(0), int64(0),
				"", int32(0), pageSize,
			},
		},
		{
			Route: RouteCategoryFiltered,
			SQL:   categoryListingSQL,
			Args: []any{
				categoryIDs, []uuid.UUID{}, true, true, int64(100_000), int64(0),
				"", int32(0), pageSize,
			},
		},
		{
			Route: RouteCategoryPriceAsc,
			SQL:   categoryListingSQL,
			Args: []any{
				categoryIDs, []uuid.UUID{}, false, false, int64(0), int64(0),
				"price_asc", int32(0), pageSize,
			},
		},
		{
			Route: RouteCategoryDeepPage,
			SQL:   categoryListingSQL,
			Args: []any{
				[]uuid.UUID{phonesCategory}, []uuid.UUID{}, false, false, int64(0), int64(0),
				"", deepOffset, pageSize,
			},
		},
		{Route: RouteSearchNameLatin, SQL: searchProductsSQL, Args: []any{searchLatin, int32(0), pageSize}},
		{Route: RouteSearchBrand, SQL: searchProductsSQL, Args: []any{"%Meridian%", int32(0), pageSize}},
		{Route: RouteSearchChinese, SQL: searchProductsSQL, Args: []any{"%" + "\u91cf\u6e2b" + "%", int32(0), pageSize}},
		{Route: RouteSearchNoMatch, SQL: searchProductsSQL, Args: []any{"%zzznomatchzz%", int32(0), pageSize}},
	}
}

const homeRecommendedSQL = `
SELECT p.slug
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n, sum(rating) AS s
    FROM visible_reviews WHERE product_id = p.id
) rv ON true
CROSS JOIN (SELECT coalesce(avg(rating), 0)::float8 AS m FROM visible_reviews) global
WHERE p.status = 'active'
ORDER BY (5 * global.m + coalesce(rv.s, 0)) / (5 + coalesce(rv.n, 0)) DESC,
         p.published_at DESC
LIMIT $1`

const rootCategoriesSQL = `
SELECT id, slug
FROM categories
WHERE parent_id IS NULL
ORDER BY position, name, id`

const categoryListingSQL = `
SELECT p.slug
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
LEFT JOIN LATERAL (
    SELECT avg(rating)::float8 AS rating, count(*) AS n
    FROM visible_reviews WHERE product_id = p.id
) rv ON true
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
ORDER BY
    CASE WHEN $7::text = 'price_asc'  THEN mv.price_cents END ASC,
    CASE WHEN $7::text = 'price_desc' THEN mv.price_cents END DESC,
    CASE WHEN $7::text = 'rating'     THEN coalesce(rv.rating, 0) END DESC,
    p.published_at DESC, p.id DESC
LIMIT $9 OFFSET $8`

const searchProductsSQL = `
SELECT p.slug
FROM products p
JOIN brands b ON b.id = p.brand_id
JOIN LATERAL (
    SELECT price_cents
    FROM product_variants
    WHERE product_id = p.id AND is_active
    ORDER BY (stock_quantity > safety_stock) DESC, price_cents
    LIMIT 1
) mv ON true
WHERE p.status = 'active'
  AND (p.name ILIKE $1::text
       OR coalesce(p.name_en, '') ILIKE $1::text
       OR coalesce(p.summary, '') ILIKE $1::text
       OR coalesce(p.summary_en, '') ILIKE $1::text
       OR b.name ILIKE $1::text)
ORDER BY
    (p.name ILIKE $1::text OR coalesce(p.name_en, '') ILIKE $1::text) DESC,
    p.published_at DESC, p.id DESC
LIMIT $3 OFFSET $2`
